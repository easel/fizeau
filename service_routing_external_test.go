package fizeau_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func newPublicRoutingFacade(t *testing.T) fizeau.FizeauService {
	t.Helper()
	t.Setenv("PATH", "")
	cacheDir, err := os.MkdirTemp("", "fizeau-public-routing-*")
	if err != nil {
		t.Fatalf("create routing cache dir: %v", err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 20; attempt++ {
			if err := os.RemoveAll(cacheDir); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("remove routing cache dir %s", cacheDir)
	})
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	models := externalModelsServer(t, []string{"qwen3.5-27b", "gpt-5.4-mini", "gpt-5.5"})
	service := newProviderFacade(t, &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"economy": {
				Type:                "openai",
				BaseURL:             models.URL + "/v1",
				APIKey:              "public-routing-test-key",
				Model:               "qwen3.5-27b",
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
			"public": {
				Type:                "openai",
				BaseURL:             models.URL + "/v1",
				APIKey:              "public-routing-test-key",
				Model:               "gpt-5.4-mini",
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
			"frontier": {
				Type:                "openai",
				BaseURL:             models.URL + "/v1",
				APIKey:              "public-routing-test-key",
				Model:               "gpt-5.5",
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
		},
		names:       []string{"economy", "public", "frontier"},
		defaultName: "public",
	})
	if _, err := service.ListModels(context.Background(), fizeau.ModelFilter{}); err != nil {
		t.Fatalf("prime public model snapshot: %v", err)
	}
	return service
}

func publicRouteCandidateByModel(t *testing.T, decision *fizeau.RouteDecision, model string) fizeau.RouteCandidate {
	t.Helper()
	if decision == nil {
		t.Fatal("nil route decision")
	}
	for _, candidate := range decision.Candidates {
		if candidate.Model == model {
			return candidate
		}
	}
	t.Fatalf("candidate model %q not found in %#v", model, decision.Candidates)
	return fizeau.RouteCandidate{}
}

func publicNoMatchRoute(t *testing.T) (*fizeau.RouteDecision, error) {
	t.Helper()
	return newPublicRoutingFacade(t).ResolveRoute(context.Background(), fizeau.RouteRequest{
		Model: "definitely-not-a-public-model",
	})
}

func TestPublicResolveRouteProjectsCandidateScoresAndFilterClassification(t *testing.T) {
	decision, err := newPublicRoutingFacade(t).ResolveRoute(context.Background(), fizeau.RouteRequest{Policy: "default"})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if decision == nil || decision.Harness != "fiz" || decision.Provider != "public" || decision.Model != "gpt-5.4-mini" {
		t.Fatalf("decision = %#v, want fiz/public/gpt-5.4-mini", decision)
	}
	if decision.RequestedPolicy != "default" || decision.PowerPolicy != (fizeau.RoutePowerPolicy{PolicyName: "default", MinPower: 7, MaxPower: 8}) {
		t.Fatalf("policy projection = %q/%#v, want default 7..8", decision.RequestedPolicy, decision.PowerPolicy)
	}

	selected := publicRouteCandidateByModel(t, decision, "gpt-5.4-mini")
	if !selected.Eligible || selected.Score == 0 || selected.Components.Power != 8 || len(selected.ScoreComponents) == 0 {
		t.Fatalf("selected candidate score projection = %#v", selected)
	}
	if selected.Components.Cost != selected.CostUSDPer1kTokens || selected.FilterReason != "" {
		t.Fatalf("selected candidate aggregate/filter projection = %#v", selected)
	}

	below := publicRouteCandidateByModel(t, decision, "qwen3.5-27b")
	if !below.Eligible || below.Components.Power != 5 || below.ScoreComponents["power"] >= 0 {
		t.Fatalf("below-band candidate = %#v, want eligible with a soft power penalty", below)
	}
	above := publicRouteCandidateByModel(t, decision, "gpt-5.5")
	if above.Eligible || above.FilterReason != fizeau.FilterReasonAboveMaxPower || above.Components.Power != 10 {
		t.Fatalf("above-band candidate = %#v, want typed above-max rejection", above)
	}
}

func TestPublicResolveRouteErrorProjectsCandidatesAndTrace(t *testing.T) {
	decision, err := publicNoMatchRoute(t)
	if err == nil || decision == nil || len(decision.Candidates) == 0 {
		t.Fatalf("decision = %#v err=%v, want failed decision with candidate trace", decision, err)
	}
	var noMatch *fizeau.ErrModelConstraintNoMatch
	if !errors.As(err, &noMatch) || noMatch.Model != "definitely-not-a-public-model" {
		t.Fatalf("error = %T %v, want typed no-match", err, err)
	}
	var traced fizeau.DecisionWithCandidates
	if !errors.As(err, &traced) {
		t.Fatalf("errors.As DecisionWithCandidates: %T %v", err, err)
	}
	if got := traced.RouteCandidates(); len(got) != len(decision.Candidates) {
		t.Fatalf("traced candidates = %d, decision candidates = %d", len(got), len(decision.Candidates))
	}
}

func TestPublicDecisionWithCandidatesCopiesInput(t *testing.T) {
	decision, err := publicNoMatchRoute(t)
	var traced fizeau.DecisionWithCandidates
	if decision == nil || !errors.As(err, &traced) {
		t.Fatalf("decision = %#v err=%v, want DecisionWithCandidates", decision, err)
	}
	first := traced.RouteCandidates()
	if len(first) == 0 {
		t.Fatal("empty candidate trace")
	}
	originalReason := first[0].Reason
	first[0].Reason = "mutated caller copy"
	second := traced.RouteCandidates()
	if second[0].Reason != originalReason || decision.Candidates[0].Reason != originalReason {
		t.Fatalf("RouteCandidates aliases stored trace: first=%#v second=%#v decision=%#v", first[0], second[0], decision.Candidates[0])
	}
}

func TestPublicResolveRoutePolicyProjection(t *testing.T) {
	service := newPublicRoutingFacade(t)
	decision, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{Policy: " default "})
	if err != nil {
		t.Fatalf("ResolveRoute whitespace default: %v", err)
	}
	if decision.RequestedPolicy != " default " || decision.PowerPolicy != (fizeau.RoutePowerPolicy{PolicyName: "default", MinPower: 7, MaxPower: 8}) {
		t.Fatalf("policy projection = %#v", decision)
	}

	bounded, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{
		Policy: "default", MinPower: 8, MaxPower: 10,
	})
	if err != nil {
		t.Fatalf("ResolveRoute bounded default: %v", err)
	}
	if bounded.PowerPolicy != (fizeau.RoutePowerPolicy{PolicyName: "default", MinPower: 8, MaxPower: 8}) {
		t.Fatalf("bounded effective policy = %#v, want default 8..8", bounded.PowerPolicy)
	}
	selected := publicRouteCandidateByModel(t, bounded, "gpt-5.4-mini")
	if bounded.Model != "gpt-5.4-mini" || !selected.Eligible || selected.Score == 0 || selected.Components.Power != 8 || selected.ScoreComponents["base"] <= 0 {
		t.Fatalf("bounded selected candidate = decision %#v candidate %#v", bounded, selected)
	}
	below := publicRouteCandidateByModel(t, bounded, "qwen3.5-27b")
	defaultBelow := publicRouteCandidateByModel(t, decision, "qwen3.5-27b")
	if !below.Eligible || below.ScoreComponents["power"] >= defaultBelow.ScoreComponents["power"] {
		t.Fatalf("bounded below-band power score=%v, want a stronger penalty than default 7..8 score %v: %#v", below.ScoreComponents["power"], defaultBelow.ScoreComponents["power"], below)
	}
	above := publicRouteCandidateByModel(t, bounded, "gpt-5.5")
	if above.Eligible || above.FilterReason != fizeau.FilterReasonAboveMaxPower || above.Components.PowerHintFit >= 0 {
		t.Fatalf("bounded above-band candidate = %#v, want typed hard max-power filter evidence", above)
	}

	pinned, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{
		Policy: "default", Model: "gpt-5.5", MinPower: 1, MaxPower: 10,
	})
	if err != nil {
		t.Fatalf("ResolveRoute pinned gpt-5.5: %v", err)
	}
	if pinned.Model != "gpt-5.5" || pinned.PowerPolicy != (fizeau.RoutePowerPolicy{PolicyName: "default", MinPower: 7, MaxPower: 8}) {
		t.Fatalf("pinned policy projection = %#v", pinned)
	}
	if candidate := publicRouteCandidateByModel(t, pinned, "gpt-5.5"); !candidate.Eligible {
		t.Fatalf("hard-pinned candidate = %#v, want eligible", candidate)
	}
}

func TestPublicResolveRouteExplicitPinErrors(t *testing.T) {
	service := newPublicRoutingFacade(t)
	_, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{
		Harness: "gemini", Model: "minimax/minimax-m2.7",
	})
	var incompatible *fizeau.ErrHarnessModelIncompatible
	if !errors.Is(err, fizeau.ErrHarnessModelIncompatible{}) || !errors.As(err, &incompatible) {
		t.Fatalf("error = %T %v, want ErrHarnessModelIncompatible", err, err)
	}
	if incompatible.Harness != "gemini" || incompatible.Model != "minimax/minimax-m2.7" || len(incompatible.SupportedModels) == 0 {
		t.Fatalf("incompatible pin evidence = %#v", incompatible)
	}

	_, err = service.ResolveRoute(context.Background(), fizeau.RouteRequest{Policy: "smart", Harness: "fiz"})
	var policyErr *fizeau.ErrPolicyRequirementUnsatisfied
	if !errors.Is(err, fizeau.ErrPolicyRequirementUnsatisfied{}) || !errors.As(err, &policyErr) {
		t.Fatalf("error = %T %v, want ErrPolicyRequirementUnsatisfied", err, err)
	}
	if policyErr.Policy != "smart" || policyErr.Requirement != "subscription-only" || policyErr.AttemptedPin != "Harness=fiz" {
		t.Fatalf("policy pin evidence = %#v", policyErr)
	}
}

func TestPublicResolveRouteCatalogPolicyErrors(t *testing.T) {
	service := newPublicRoutingFacade(t)
	for _, policy := range []string{"does-not-exist", " does-not-exist ", "standard", "code-fast", "offline"} {
		t.Run(policy, func(t *testing.T) {
			decision, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{Policy: policy, MinPower: 2, MaxPower: 9})
			var typed *fizeau.ErrUnknownPolicy
			if !errors.Is(err, fizeau.ErrUnknownPolicy{}) || !errors.As(err, &typed) || typed.Policy != policy {
				t.Fatalf("decision=%#v error=%T %v, want raw ErrUnknownPolicy(%q)", decision, err, err, policy)
			}
			if decision == nil || decision.RequestedPolicy != policy {
				t.Fatalf("decision policy evidence = %#v, want %q", decision, policy)
			}
		})
	}

	for policy, replacement := range map[string]string{"code-medium": "default", "code-high": "smart"} {
		t.Run(policy, func(t *testing.T) {
			decision, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{Policy: policy, MinPower: 2, MaxPower: 9})
			want := `policy "` + policy + `" is deprecated; use --policy ` + replacement + ` or --min-power/--max-power`
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if decision == nil || decision.RequestedPolicy != policy {
				t.Fatalf("deprecated policy evidence = %#v", decision)
			}
		})
	}
}

func TestPublicResolveRouteAirGappedTypedError(t *testing.T) {
	service := newPublicRoutingFacade(t)
	decision, err := service.ResolveRoute(context.Background(), fizeau.RouteRequest{Policy: "air-gapped", Provider: "public"})
	var typed *fizeau.ErrPolicyRequirementUnsatisfied
	if !errors.Is(err, fizeau.ErrPolicyRequirementUnsatisfied{}) || !errors.As(err, &typed) {
		t.Fatalf("decision=%#v error=%T %v, want typed air-gapped failure", decision, err, err)
	}
	if typed.Policy != "air-gapped" || typed.Requirement != "no_remote" {
		t.Fatalf("air-gapped evidence = %#v", typed)
	}
}

func TestPublicNoViableProviderForNowContract(t *testing.T) {
	retry := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	err := &fizeau.NoViableProviderForNow{
		RetryAfter:         retry,
		ExhaustedProviders: []string{"openai", "openrouter"},
	}
	wrapped := errors.Join(errors.New("dispatch paused"), err)
	var typed *fizeau.NoViableProviderForNow
	if !errors.Is(wrapped, &fizeau.NoViableProviderForNow{}) || !errors.As(wrapped, &typed) {
		t.Fatalf("public identity failed: %T %v", wrapped, wrapped)
	}
	if !typed.RetryAfter.Equal(retry) || !slices.Equal(typed.ExhaustedProviders, []string{"openai", "openrouter"}) {
		t.Fatalf("public fields = %#v", typed)
	}
	if !strings.Contains(typed.Error(), "openai, openrouter quota-exhausted") || !strings.Contains(typed.Error(), retry.Format(time.RFC3339)) {
		t.Fatalf("public error text = %q", typed.Error())
	}
	if errors.Is(typed, fizeau.ErrUnknownProvider{}) || errors.Is(typed, fizeau.ErrNoLiveProvider{}) || errors.Is(typed, fizeau.ErrPolicyRequirementUnsatisfied{}) {
		t.Fatal("transient quota identity aliases a permanent routing error")
	}
}
