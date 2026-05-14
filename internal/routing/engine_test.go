package routing

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newTestRoutingEngine returns a baseline Inputs with local and subscription
// harnesses. Mirrors the DDx newTestRunnerForRouting helper.
func newTestRoutingEngine() Inputs {
	return Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportedReasoning:  []string{"low", "medium", "high"},
				SupportedPerms:      []string{"safe", "supervised", "unrestricted"},
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:          "vidar-omlx",
						BaseURL:       "http://vidar:11434",
						DefaultModel:  "qwen/qwen3.6",
						DiscoveredIDs: []string{"Qwen3.6-35B-A3B-4bit", "MiniMax-M2.5-MLX-4bit"},
						SupportsTools: true,
						ContextWindows: map[string]int{
							"Qwen3.6-35B-A3B-4bit": 256000,
						},
					},
					{
						Name:          "openrouter",
						BaseURL:       "https://openrouter.ai/api/v1",
						DefaultModel:  "anthropic/claude-sonnet-4-6",
						DiscoveredIDs: []string{"qwen/qwen3.6", "anthropic/claude-sonnet-4-6"},
						SupportsTools: true,
					},
				},
			},
			{
				Name:                "codex",
				Surface:             "codex",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportedReasoning:  []string{"low", "medium", "high"},
				SupportedPerms:      []string{"safe", "supervised", "unrestricted"},
				SupportsTools:       true,
				DefaultModel:        "gpt-5.4",
			},
			{
				Name:                "claude",
				Surface:             "claude",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportedReasoning:  []string{"low", "medium", "high"},
				SupportedPerms:      []string{"safe", "supervised", "unrestricted"},
				SupportsTools:       true,
				DefaultModel:        "claude-sonnet-4-6",
			},
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}
}

// === Smell 1: ddx-8610020e — Provider field present from day one ===
//
// RouteRequest carries Provider as a hard pin; the engine must never select
// a different provider when Provider is set.
func TestSmellProviderFieldDayOne(t *testing.T) {
	in := newTestRoutingEngine()

	// Provider pin: req.Provider constrains routing.
	req := Request{Policy: "cheap", Provider: "vidar-omlx"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "vidar-omlx" {
		t.Errorf("provider=vidar-omlx soft pref: got %q, want vidar-omlx", dec.Provider)
	}

	// Hard pin: Harness=fiz + Provider=openrouter constrains routing.
	hardReq := Request{Harness: "fiz", Provider: "openrouter", Model: "qwen/qwen3.6"}
	dec2, err := Resolve(hardReq, in)
	if err != nil {
		t.Fatalf("hard pin Resolve: %v", err)
	}
	if dec2.Provider != "openrouter" {
		t.Errorf("hard pin: got provider=%q, want openrouter", dec2.Provider)
	}
}

func TestResolveServerInstanceIdentityFlowsThroughDecisionAndLoadResolver(t *testing.T) {
	var gotProvider, gotEndpoint, gotModel string
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportedReasoning:  []string{"low", "medium", "high"},
				SupportedPerms:      []string{"safe", "supervised", "unrestricted"},
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:           "local",
						BaseURL:        "http://grendel:8000/v1",
						ServerInstance: "shared-grendel",
						EndpointName:   "primary",
						DefaultModel:   "model-a",
						CostClass:      "local",
						SupportsTools:  true,
					},
				},
			},
		},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			if model != "model-a" {
				return ModelEligibility{}, false
			}
			return ModelEligibility{Power: 7, AutoRoutable: true}, true
		},
		EndpointLoadResolver: func(provider, endpoint, model string) (EndpointLoad, bool) {
			gotProvider, gotEndpoint, gotModel = provider, endpoint, model
			return EndpointLoad{NormalizedLoad: 0.25, UtilizationFresh: true}, true
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{Harness: "fiz", Model: "model-a"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec == nil {
		t.Fatal("Resolve returned nil decision")
	}
	if dec.ServerInstance != "shared-grendel" {
		t.Fatalf("decision=%#v, want shared-grendel server instance", dec)
	}
	if len(dec.Candidates) != 1 {
		t.Fatalf("Candidates=%#v, want 1", dec.Candidates)
	}
	if dec.Candidates[0].ServerInstance != "shared-grendel" {
		t.Fatalf("candidate=%#v, want shared-grendel server instance", dec.Candidates[0])
	}
	if gotProvider != "local" || gotEndpoint != "shared-grendel" || gotModel != "model-a" {
		t.Fatalf("EndpointLoadResolver saw %q/%q/%q, want local/shared-grendel/model-a", gotProvider, gotEndpoint, gotModel)
	}
}

func TestExplicitProviderPinDoesNotSubstituteAvailableProvider(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:               "lmstudio",
						DiscoveryAttempted: true,
						SupportsTools:      true,
					},
					{
						Name:          "openrouter",
						DiscoveredIDs: []string{"qwen/qwen3.6"},
						SupportsTools: true,
					},
				},
			},
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	_, err := Resolve(Request{Provider: "lmstudio", Model: "qwen/qwen3.6"}, in)
	if err == nil {
		t.Fatal("Resolve succeeded, want unsatisfiable provider/model pin")
	}
	var unsat *ErrUnsatisfiablePin
	if !errors.As(err, &unsat) {
		t.Fatalf("error type=%T, want *ErrUnsatisfiablePin: %v", err, err)
	}
	if unsat.Pin != "provider=lmstudio+model=qwen/qwen3.6" {
		t.Fatalf("Pin=%q, want provider=lmstudio+model=qwen/qwen3.6", unsat.Pin)
	}
}

func TestResolvePrefersLargerContextHeadroomAmongEligibleCandidates(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "cheap",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:                 "alpha",
						DefaultModel:         "model-a",
						SupportsTools:        true,
						ContextWindows:       map[string]int{"model-a": 4000},
						ContextWindowSources: map[string]string{"model-a": ContextSourceCatalog},
					},
					{
						Name:                 "zeta",
						DefaultModel:         "model-a",
						SupportsTools:        true,
						ContextWindows:       map[string]int{"model-a": 12000},
						ContextWindowSources: map[string]string{"model-a": ContextSourceCatalog},
					},
				},
			},
		},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			if model != "model-a" {
				return ModelEligibility{}, false
			}
			return ModelEligibility{Power: 5, AutoRoutable: true}, true
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{
		Harness:               "fiz",
		Model:                 "model-a",
		EstimatedPromptTokens: 1000,
	}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "zeta" {
		t.Fatalf("winner provider=%q, want zeta; candidates=%+v", dec.Provider, dec.Candidates)
	}
	if len(dec.Candidates) != 2 {
		t.Fatalf("Candidates=%d, want 2", len(dec.Candidates))
	}
	var alpha, zeta Candidate
	for _, c := range dec.Candidates {
		switch c.Provider {
		case "alpha":
			alpha = c
		case "zeta":
			zeta = c
		}
	}
	if alpha.Provider != "alpha" || zeta.Provider != "zeta" {
		t.Fatalf("candidates=%+v, want both alpha and zeta", dec.Candidates)
	}
	if alpha.ContextHeadroom >= zeta.ContextHeadroom {
		t.Fatalf("headroom ordering broken: alpha=%#v zeta=%#v", alpha, zeta)
	}
	if alpha.Score >= zeta.Score {
		t.Fatalf("score should reward headroom: alpha score=%.2f zeta score=%.2f", alpha.Score, zeta.Score)
	}
}

func TestExplicitModelPinDoesNotSubstituteCloudDefaults(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "codex",
				Surface:             "codex",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportedModels:     []string{"gpt-5.4", "gpt-5.4-mini"},
				SupportsTools:       true,
				DefaultModel:        "gpt-5.4",
			},
			{
				Name:                "claude",
				Surface:             "claude",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportedModels:     []string{"claude-sonnet-4-6", "opus-4.7"},
				SupportsTools:       true,
				DefaultModel:        "claude-sonnet-4-6",
			},
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{Model: "qwen-3.6-27b"}, in)
	if err == nil {
		t.Fatalf("Resolve selected harness=%q model=%q, want exact pin failure", dec.Harness, dec.Model)
	}
	var noViable *NoViableCandidateError
	if !errors.As(err, &noViable) {
		t.Fatalf("error type=%T, want *NoViableCandidateError: %v", err, err)
	}
	if !strings.Contains(err.Error(), "model=qwen-3.6-27b") {
		t.Fatalf("error=%q, want model pin detail", err.Error())
	}
	for _, c := range dec.Candidates {
		if c.Eligible {
			t.Fatalf("candidate must be rejected under unsupported exact model pin: %#v", c)
		}
		if c.Model == "gpt-5.4" || c.Model == "claude-sonnet-4-6" {
			t.Fatalf("candidate substituted default model under exact pin: %#v", c)
		}
		if c.Reason != "model not in harness allow-list" {
			t.Fatalf("candidate reason=%q, want allow-list rejection", c.Reason)
		}
	}
}

func TestExplicitHarnessPinDoesNotSubstituteOtherHarness(t *testing.T) {
	in := newTestRoutingEngine()
	for i, h := range in.Harnesses {
		if h.Name == "fiz" {
			in.Harnesses[i].Available = false
		}
	}

	dec, err := Resolve(Request{Harness: "fiz", Model: "qwen/qwen3.6"}, in)
	if err == nil {
		t.Fatalf("Resolve selected harness=%q, want no viable candidate", dec.Harness)
	}
	if dec == nil {
		t.Fatal("Resolve returned nil decision")
	}
	for _, c := range dec.Candidates {
		if c.Harness != "fiz" {
			t.Fatalf("non-pinned harness appeared in candidate list: %#v", c)
		}
		if c.Eligible {
			t.Fatalf("unavailable pinned harness must not be eligible: %#v", c)
		}
	}
	if !strings.Contains(err.Error(), "harness=fiz") {
		t.Fatalf("error=%q, want harness pin detail", err.Error())
	}
}

func TestAutomaticRoutingFiltersCatalogPowerEligibility(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:          "local-unknown",
						DefaultModel:  "unknown-local",
						DiscoveredIDs: []string{"unknown-local"},
						SupportsTools: true,
					},
					{
						Name:          "local-exact-only",
						DefaultModel:  "exact-only",
						DiscoveredIDs: []string{"exact-only"},
						SupportsTools: true,
					},
					{
						Name:          "openrouter",
						DefaultModel:  "known-cloud",
						DiscoveredIDs: []string{"known-cloud"},
						SupportsTools: true,
					},
				},
			},
		},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			switch model {
			case "known-cloud":
				return ModelEligibility{Power: 7, AutoRoutable: true}, true
			case "exact-only":
				return ModelEligibility{Power: 6, ExactPinOnly: true, AutoRoutable: false}, true
			default:
				return ModelEligibility{}, false
			}
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "openrouter" || dec.Model != "known-cloud" {
		t.Fatalf("selected provider=%q model=%q, want openrouter/known-cloud", dec.Provider, dec.Model)
	}

	byProvider := map[string]Candidate{}
	for _, c := range dec.Candidates {
		byProvider[c.Provider] = c
	}
	if got := byProvider["local-unknown"].FilterReason; got != FilterReasonEligible {
		t.Fatalf("local-unknown FilterReason=%q, want eligible; missing power alone must not block routing", got)
	}
	if got := byProvider["local-exact-only"].FilterReason; got != FilterReasonExactPinOnly {
		t.Fatalf("local-exact-only FilterReason=%q, want %q", got, FilterReasonExactPinOnly)
	}
	if !byProvider["local-unknown"].Eligible {
		t.Fatalf("local-unknown should remain eligible without power metadata: %#v", byProvider["local-unknown"])
	}
}

func TestNativeProviderCostClassOverridesFizHarnessLocalClass(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			IsLocal:             true,
			AutoRoutingEligible: true,
			ExactPinSupport:     true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			SupportsTools:       true,
			Providers: []ProviderEntry{
				{Name: "openrouter", DefaultModel: "cloud-model", CostClass: "medium", SupportsTools: true},
				{Name: "vidar", DefaultModel: "local-model", CostClass: "local", SupportsTools: true},
			},
		}},
	}
	dec, err := Resolve(Request{}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "vidar" {
		t.Fatalf("provider=%q, want local vidar; candidates=%+v", dec.Provider, dec.Candidates)
	}
	for _, c := range dec.Candidates {
		if c.Provider == "openrouter" && c.CostClass != "medium" {
			t.Fatalf("openrouter CostClass=%q, want medium", c.CostClass)
		}
	}
}

func TestAutomaticRoutingFiltersMinMaxPower(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{Name: "small", DefaultModel: "small-model", DiscoveredIDs: []string{"small-model"}, SupportsTools: true},
					{Name: "large", DefaultModel: "large-model", DiscoveredIDs: []string{"large-model"}, SupportsTools: true},
				},
			},
		},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			switch model {
			case "small-model":
				return ModelEligibility{Power: 4, AutoRoutable: true}, true
			case "large-model":
				return ModelEligibility{Power: 8, AutoRoutable: true}, true
			default:
				return ModelEligibility{}, false
			}
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{MinPower: 7}, in)
	if err != nil {
		t.Fatalf("Resolve MinPower: %v", err)
	}
	if dec.Provider != "large" {
		t.Fatalf("MinPower selected provider=%q, want large", dec.Provider)
	}
	for _, c := range dec.Candidates {
		if c.Provider == "small" && !c.Eligible {
			t.Fatalf("small candidate must remain eligible under soft min_power: %#v", c)
		}
	}

	dec, err = Resolve(Request{MaxPower: 5}, in)
	if err != nil {
		t.Fatalf("Resolve MaxPower: %v", err)
	}
	if dec.Provider != "small" {
		t.Fatalf("MaxPower selected provider=%q, want small", dec.Provider)
	}
	for _, c := range dec.Candidates {
		if c.Provider == "large" && !c.Eligible {
			t.Fatalf("large candidate must remain eligible under soft max_power: %#v", c)
		}
	}

	dec, err = Resolve(Request{MinPower: 9, MaxPower: 9}, in)
	if err != nil {
		t.Fatalf("Resolve soft power bounds: %v", err)
	}
	if dec.Provider != "large" {
		t.Fatalf("soft impossible bounds selected provider=%q, want nearest large", dec.Provider)
	}
}

func TestExactModelPinBypassesCatalogPowerEligibility(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{{
					Name:               "lmstudio",
					DiscoveredIDs:      []string{"unknown-local"},
					DiscoveryAttempted: true,
					SupportsTools:      true,
				}},
			},
		},
		ModelEligibility: func(string) (ModelEligibility, bool) {
			return ModelEligibility{}, false
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{Harness: "fiz", Provider: "lmstudio", Model: "unknown-local", MinPower: 10}, in)
	if err != nil {
		t.Fatalf("Resolve exact pin: %v", err)
	}
	if dec.Provider != "lmstudio" || dec.Model != "unknown-local" {
		t.Fatalf("selected provider=%q model=%q, want lmstudio/unknown-local", dec.Provider, dec.Model)
	}
	for _, c := range dec.Candidates {
		if c.Provider == "lmstudio" && c.FilterReason != FilterReasonEligible {
			t.Fatalf("exact pin candidate FilterReason=%q, want eligible", c.FilterReason)
		}
	}
}

// === Smell 2: ddx-0486e601 — canonical-form fuzzy matcher ===
//
// "qwen/qwen3.6" must match "Qwen3.6-35B-A3B-4bit" (case + vendor
// prefix normalization).
func TestSmellCanonicalFormFuzzyMatcher(t *testing.T) {
	in := newTestRoutingEngine()

	// Direct fuzzy-match function.
	pool := []string{"Qwen3.6-35B-A3B-4bit", "MiniMax-M2.5-MLX-4bit"}
	matched := FuzzyMatch("qwen/qwen3.6", pool)
	if matched != "Qwen3.6-35B-A3B-4bit" {
		t.Errorf("FuzzyMatch(qwen/qwen3.6): got %q, want Qwen3.6-35B-A3B-4bit", matched)
	}
	pool = []string{
		"Qwen3.5-122B-A10B-RAM-100GB-MLX",
		"Qwen3-Coder-Next-MLX-4bit",
		"Qwen3.5-27B-4bit",
		"Qwen3.6-35B-A3B-4bit",
		"Qwen3.6-35B-A3B-nvfp4",
	}
	if matched := FuzzyMatch("qwen", pool); matched != "Qwen3.6-35B-A3B-4bit" {
		t.Errorf("FuzzyMatch(qwen): got %q, want Qwen3.6-35B-A3B-4bit", matched)
	}

	// End-to-end: Model="qwen/qwen3.6" + Provider=vidar-omlx resolves to
	// the provider-native ID via fuzzy match.
	req := Request{Provider: "vidar-omlx", Harness: "fiz", Model: "qwen/qwen3.6"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Model != "Qwen3.6-35B-A3B-4bit" {
		t.Errorf("end-to-end fuzzy resolution: got model=%q, want Qwen3.6-35B-A3B-4bit", dec.Model)
	}
}

func TestModelRoutingFuzzyMatchesLiveDiscovery(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			IsLocal:             true,
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{{
				Name:               "vidar",
				DefaultModel:       "Qwen3.6-27B-MLX-8bit",
				CostClass:          "local",
				DiscoveredIDs:      []string{"Qwen3.6-27B-MLX-8bit"},
				DiscoveryAttempted: true,
				SupportsTools:      true,
			}},
		}},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			if model == "Qwen3.6-27B-MLX-8bit" {
				return ModelEligibility{Power: 5, AutoRoutable: true}, true
			}
			return ModelEligibility{}, false
		},
	}

	dec, err := Resolve(Request{Model: "qwen/qwen3.6-27b", ProviderPreference: ProviderPreferenceLocalFirst}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "vidar" || dec.Model != "Qwen3.6-27B-MLX-8bit" {
		t.Fatalf("selected provider=%q model=%q, want vidar/Qwen3.6-27B-MLX-8bit", dec.Provider, dec.Model)
	}
}

// === Smell 3: ddx-4817edfd — capability gating ===
//
// Per-(harness, provider, model) capability gating: context window,
// tool support, effort, permissions.
func TestSmellCapabilityGating(t *testing.T) {
	t.Run("context window", func(t *testing.T) {
		in := newTestRoutingEngine()
		// MiniMax has no ContextWindow entry; qwen has 256k.
		// Request a 80k-token prompt — qwen should pass, MiniMax should be neutral
		// (unknown ctx → not rejected).
		req := Request{
			Provider:              "vidar-omlx",
			Harness:               "fiz",
			Model:                 "Qwen3.6-35B-A3B-4bit",
			EstimatedPromptTokens: 80000,
		}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if dec.Model != "Qwen3.6-35B-A3B-4bit" {
			t.Errorf("got model=%q, want Qwen3.6", dec.Model)
		}

		// Now request a 300k-token prompt — qwen (256k) should be rejected.
		req.EstimatedPromptTokens = 300000
		dec2, err := Resolve(req, in)
		if err == nil && dec2.Eligible() {
			// Find qwen candidate, should be ineligible.
			for _, c := range dec2.Candidates {
				if c.Model == "Qwen3.6-35B-A3B-4bit" && c.Eligible {
					t.Errorf("300k prompt: qwen (256k) should be ineligible")
				}
			}
		}
	})

	t.Run("tool support", func(t *testing.T) {
		in := newTestRoutingEngine()
		// Mark vidar-omlx provider as tool-incapable.
		for i, h := range in.Harnesses {
			if h.Name == "fiz" {
				for j, p := range h.Providers {
					if p.Name == "vidar-omlx" {
						in.Harnesses[i].Providers[j].SupportsTools = false
					}
				}
				// Disable harness-level tool support too so the OR doesn't rescue.
				in.Harnesses[i].SupportsTools = false
			}
		}
		req := Request{Policy: "cheap", Provider: "vidar-omlx", RequiresTools: true}
		dec, err := Resolve(req, in)
		// vidar-omlx must not be eligible.
		if err == nil {
			for _, c := range dec.Candidates {
				if c.Provider == "vidar-omlx" && c.Eligible {
					t.Errorf("vidar-omlx without tools must be ineligible when RequiresTools=true")
				}
			}
		}
	})

	t.Run("reasoning", func(t *testing.T) {
		// A harness with no SupportedReasoning must reject reasoning=high.
		in := newTestRoutingEngine()
		in.Harnesses = append(in.Harnesses, HarnessEntry{
			Name:                "no-reasoning-harness",
			Surface:             "test",
			CostClass:           "medium",
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			DefaultModel:        "x",
		})
		req := Request{Policy: "default", Reasoning: "high"}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		for _, c := range dec.Candidates {
			if c.Harness == "no-reasoning-harness" && c.Eligible {
				t.Errorf("no-reasoning-harness must be ineligible when Reasoning=high")
			}
		}
	})

	t.Run("reasoning off imposes no requirement", func(t *testing.T) {
		cap := Capabilities{}
		for _, value := range []string{"off", "0", "none", "false"} {
			if got, _ := CheckGating(cap, Request{Reasoning: value}); got != "" {
				t.Fatalf("Reasoning=%q should not gate candidate, got %q", value, got)
			}
		}
	})

	t.Run("extended reasoning requires advertised support", func(t *testing.T) {
		cap := Capabilities{SupportedReasoning: []string{"low", "medium", "high", "xhigh", "max"}}
		if got, _ := CheckGating(cap, Request{Reasoning: "x-high"}); got != "" {
			t.Fatalf("x-high should normalize to advertised xhigh, got %q", got)
		}
		if got, _ := CheckGating(Capabilities{SupportedReasoning: []string{"low"}}, Request{Reasoning: "max"}); got == "" {
			t.Fatal("max should reject candidates that do not advertise it")
		}
	})

	t.Run("numeric reasoning gates against max", func(t *testing.T) {
		cap := Capabilities{MaxReasoningTokens: 4096}
		if got, _ := CheckGating(cap, Request{Reasoning: "2048"}); got != "" {
			t.Fatalf("numeric value under max should pass, got %q", got)
		}
		if got, _ := CheckGating(cap, Request{Reasoning: "8192"}); got == "" {
			t.Fatal("numeric value over max should fail")
		}
	})
}

// === Smell 4: ddx-3c5ba7cc — policy-aware tier escalation ===
//
// EscalatePolicyAware must respect provider affinity: when the
// pinned provider can't serve the next tier's model, that tier is skipped.
func TestSmellPolicyAwareEscalation(t *testing.T) {
	in := newTestRoutingEngine()
	// Restrict vidar-omlx to qwen3.6 (cheap), nothing for smart.
	for i, h := range in.Harnesses {
		if h.Name == "fiz" {
			for j, p := range h.Providers {
				if p.Name == "vidar-omlx" {
					// Only the cheap-tier model is discovered.
					in.Harnesses[i].Providers[j].DiscoveredIDs = []string{"Qwen3.6-35B-A3B-4bit"}
				}
			}
		}
	}
	// With Harness=fiz+Provider=vidar-omlx pin, escalating to "smart"
	// should fail (the catalog smart→claude-opus surface mismatch + provider
	// pin means no candidate is viable on the fiz harness).
	ladder := []string{"cheap", "smart"}
	req := Request{Harness: "fiz", Provider: "vidar-omlx", Policy: "cheap"}
	next := EscalatePolicyAware("cheap", ladder, req, in)
	// smart catalog → claude-opus (surface=claude), but Harness=fiz pinned,
	// so smart isn't viable. EscalatePolicyAware should return "" or skip.
	if next == "smart" {
		t.Errorf("escalation to smart under Harness=fiz+Provider=vidar-omlx should be skipped")
	}
}

// === Smell 5: single observation store + cooldown abstraction ===
//
// Cooldown demotion is applied uniformly via Inputs.ProviderCooldowns.
// A provider in cooldown loses score; without demotion it would have won.
func TestSmellSingleCooldownAbstraction(t *testing.T) {
	in := newTestRoutingEngine()
	// Without cooldown: with provider affinity to vidar-omlx, vidar wins.
	baseReq := Request{Policy: "cheap", Harness: "fiz", Provider: "vidar-omlx", Model: "qwen/qwen3.6"}
	dec0, err := Resolve(baseReq, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec0.Provider != "vidar-omlx" {
		t.Fatalf("baseline: vidar should win with affinity; got %q", dec0.Provider)
	}

	// Now put vidar-omlx in cooldown. Other providers are still eligible
	// (provider pin is soft when paired only with Harness — not a hard reject)
	// so the cooldown demotion lets a non-cooldowned candidate take over.
	in.ProviderCooldowns = map[string]time.Time{
		"vidar-omlx": in.Now.Add(-5 * time.Second),
	}
	in.CooldownDuration = 30 * time.Second

	// Use a cheap-tier request without the hard provider pin so cooldown
	// demotion is observable.
	cooldownReq := Request{Policy: "cheap", Harness: "fiz", Model: "qwen/qwen3.6"}
	dec, err := Resolve(cooldownReq, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Vidar should still resolve but with a -50 cooldown demotion — openrouter
	// (no cooldown) overtakes via score, even though both share local cost class.
	if dec.Provider == "vidar-omlx" {
		t.Errorf("under cooldown vidar-omlx should NOT be top pick; got %q", dec.Provider)
	}

	// After cooldown expires (Now > failedAt + cooldown), vidar is no longer demoted.
	in.Now = in.Now.Add(60 * time.Second)
	dec2, err := Resolve(cooldownReq, in)
	if err != nil {
		t.Fatalf("Resolve after cooldown: %v", err)
	}
	// Find both candidates' eligibility/scores.
	var vidarScore, openrouterScore float64
	for _, c := range dec2.Candidates {
		switch c.Provider {
		case "vidar-omlx":
			vidarScore = c.Score
		case "openrouter":
			openrouterScore = c.Score
		}
	}
	// Confirm cooldown demotion is gone (scores within 1.0 of each other).
	if vidarScore < openrouterScore-1 {
		t.Errorf("after cooldown expiry, vidar should not be demoted; vidar=%.1f openrouter=%.1f", vidarScore, openrouterScore)
	}
}

// TestProviderUnreachableHardGatesCandidate verifies that a provider marked
// unreachable in Inputs.ProviderUnreachable (within CooldownDuration) is hard-
// gated — eligible=false with FilterReasonUnhealthy — instead of merely
// demoted in score. This is the FEAT-004 AC-28 path: known-down endpoints are
// dispatchability failures. Discovery failures feed this map via
// service_routing.providerCooldownsFromSnapshotErrors.
func TestProviderUnreachableHardGatesCandidate(t *testing.T) {
	in := newTestRoutingEngine()

	// Baseline: vidar-omlx wins with provider affinity.
	baseReq := Request{Policy: "cheap", Harness: "fiz", Provider: "vidar-omlx", Model: "qwen/qwen3.6"}
	dec0, err := Resolve(baseReq, in)
	if err != nil {
		t.Fatalf("baseline Resolve: %v", err)
	}
	if dec0.Provider != "vidar-omlx" {
		t.Fatalf("baseline: vidar should win with affinity; got %q", dec0.Provider)
	}

	// Mark vidar-omlx unreachable. Without an explicit pin, the candidate
	// should be hard-rejected (not just demoted) with FilterReasonUnhealthy.
	in.ProviderUnreachable = map[string]time.Time{
		"vidar-omlx": in.Now.Add(-5 * time.Second),
	}
	in.CooldownDuration = 30 * time.Second

	cooldownReq := Request{Policy: "cheap", Harness: "fiz", Model: "qwen/qwen3.6"}
	dec, err := Resolve(cooldownReq, in)
	if err != nil {
		t.Fatalf("Resolve under unreachable: %v", err)
	}
	if dec.Provider == "vidar-omlx" {
		t.Errorf("unreachable vidar should NOT be top pick; got %q", dec.Provider)
	}

	// Find vidar's candidate row and assert it's hard-rejected, not demoted.
	var vidar *Candidate
	for i := range dec.Candidates {
		if dec.Candidates[i].Provider == "vidar-omlx" {
			vidar = &dec.Candidates[i]
			break
		}
	}
	if vidar == nil {
		t.Fatal("vidar-omlx candidate row missing from decision")
	}
	if vidar.Eligible {
		t.Errorf("vidar-omlx should be Eligible=false under ProviderUnreachable; got Eligible=true")
	}
	if vidar.FilterReason != FilterReasonUnhealthy {
		t.Errorf("vidar-omlx FilterReason = %q, want %q", vidar.FilterReason, FilterReasonUnhealthy)
	}
	if !strings.Contains(vidar.Reason, "known unreachable") {
		t.Errorf("vidar-omlx Reason = %q, want it to contain 'known unreachable'", vidar.Reason)
	}

	// After cooldown TTL expires, vidar is eligible again.
	in.Now = in.Now.Add(60 * time.Second)
	dec2, err := Resolve(baseReq, in)
	if err != nil {
		t.Fatalf("Resolve after TTL: %v", err)
	}
	if dec2.Provider != "vidar-omlx" {
		t.Errorf("after TTL expiry with affinity, vidar should win; got %q", dec2.Provider)
	}
}

// TestProviderUnreachableBypassedByExplicitPin verifies that an explicit
// provider pin overrides the unreachable hard gate (the operator gets what
// they asked for, with the dispatchability failure surfacing downstream).
func TestProviderUnreachableBypassedByExplicitPin(t *testing.T) {
	in := newTestRoutingEngine()
	in.ProviderUnreachable = map[string]time.Time{
		"vidar-omlx": in.Now.Add(-5 * time.Second),
	}
	in.CooldownDuration = 30 * time.Second

	// Explicit pin to vidar-omlx must bypass the gate.
	req := Request{Policy: "cheap", Harness: "fiz", Provider: "vidar-omlx", Model: "qwen/qwen3.6"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve with pin: %v", err)
	}
	if dec.Provider != "vidar-omlx" {
		t.Errorf("explicit pin should bypass unreachable gate; got %q", dec.Provider)
	}
}

// === Smell 6: TestOnly harnesses excluded from tier routing ===
//
// Regression for ddx-869848ec (carried forward from DDx routing.go):
// TestOnly harnesses (script, virtual) must never leak into policy-based
// routing — only explicit Harness override reaches them.
func TestSmellTestOnlyHarnessExcluded(t *testing.T) {
	in := newTestRoutingEngine()
	for _, name := range []string{"script", "virtual"} {
		in.Harnesses = append(in.Harnesses, HarnessEntry{
			Name:            name,
			Surface:         name,
			CostClass:       "local",
			IsLocal:         true,
			TestOnly:        true,
			Available:       true,
			QuotaOK:         true,
			SubscriptionOK:  true,
			ExactPinSupport: true,
			DefaultModel:    "recorded",
		})
	}

	for _, policy := range []string{"cheap", "default", "smart"} {
		req := Request{Policy: policy}
		dec, err := Resolve(req, in)
		if err != nil {
			continue
		}
		for _, c := range dec.Candidates {
			if c.Harness == "script" || c.Harness == "virtual" {
				t.Errorf("policy=%s: TestOnly harness %q leaked into candidates", policy, c.Harness)
			}
		}
	}

	for _, name := range []string{"script", "virtual"} {
		req := Request{Harness: name}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("explicit Harness=%s must succeed: %v", name, err)
		}
		if dec.Harness != name {
			t.Errorf("explicit Harness=%s: got %q", name, dec.Harness)
		}
	}
}

func TestAutoRoutingEligibilityGate(t *testing.T) {
	in := newTestRoutingEngine()
	for _, h := range []HarnessEntry{
		{
			Name:            "gemini",
			Surface:         "gemini",
			CostClass:       "experimental",
			Available:       true,
			QuotaOK:         true,
			SubscriptionOK:  true,
			ExactPinSupport: true,
			SupportsTools:   true,
			DefaultModel:    "gemini-test",
		},
		{
			Name:            "opencode",
			Surface:         "embedded-openai",
			CostClass:       "medium",
			Available:       true,
			QuotaOK:         true,
			SubscriptionOK:  true,
			ExactPinSupport: true,
			SupportsTools:   true,
			DefaultModel:    "opencode-test",
		},
		{
			Name:            "pi",
			Surface:         "pi",
			CostClass:       "medium",
			Available:       true,
			QuotaOK:         true,
			SubscriptionOK:  true,
			ExactPinSupport: true,
			SupportsTools:   true,
			DefaultModel:    "pi-test",
		},
	} {
		in.Harnesses = append(in.Harnesses, h)
	}

	dec, err := Resolve(Request{Policy: "smart", AllowLocal: true}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, c := range dec.Candidates {
		switch c.Harness {
		case "fiz", "codex", "claude":
			// Harnesses marked auto-routing eligible may appear.
		case "gemini", "opencode", "pi":
			t.Fatalf("non-full-coverage harness %q leaked into automatic routing candidates", c.Harness)
		default:
			t.Fatalf("unexpected harness %q in automatic routing candidates", c.Harness)
		}
	}

	for _, name := range []string{"gemini", "opencode", "pi"} {
		req := Request{Harness: name, Model: name + "-test"}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("explicit Harness=%s must remain routable: %v", name, err)
		}
		if dec.Harness != name {
			t.Fatalf("explicit Harness=%s: got %q", name, dec.Harness)
		}
	}
}

func TestSecondaryHarnessesRequireOperationalEvidenceForAutoRouting(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "codex",
				Surface:             "codex",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				DefaultModel:        "gpt-5.4",
			},
			{
				Name:            "opencode",
				Surface:         "embedded-openai",
				CostClass:       "medium",
				Available:       true,
				QuotaOK:         true,
				SubscriptionOK:  true,
				ExactPinSupport: true,
				SupportsTools:   true,
				DefaultModel:    "opencode/gpt-5.4",
			},
			{
				Name:            "pi",
				Surface:         "pi",
				CostClass:       "medium",
				Available:       true,
				QuotaOK:         true,
				SubscriptionOK:  true,
				ExactPinSupport: true,
				SupportsTools:   true,
				DefaultModel:    "gemini-2.5-flash",
			},
			{
				Name:            "gemini",
				Surface:         "gemini",
				CostClass:       "experimental",
				Available:       true,
				QuotaOK:         true,
				SubscriptionOK:  true,
				ExactPinSupport: true,
				SupportsTools:   true,
				DefaultModel:    "gemini-2.5-flash",
			},
		},
		Now: time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}

	dec, err := Resolve(Request{Policy: "default"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	seen := map[string]Candidate{}
	for _, c := range dec.Candidates {
		seen[c.Harness] = c
	}
	for _, name := range []string{"opencode", "pi"} {
		if _, ok := seen[name]; ok {
			t.Fatalf("secondary harness %q should remain outside auto-routing candidates without cost/quota evidence", name)
		}
	}
	if _, ok := seen["gemini"]; ok {
		t.Fatalf("gemini should remain outside auto-routing candidates when AutoRoutingEligible is false")
	}
}

// === Policy policy semantics ported from DDx routing_test.go ===

func TestCheapPrefersLocal(t *testing.T) {
	in := newTestRoutingEngine()
	req := Request{Policy: "cheap"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "fiz" {
		t.Errorf("cheap policy: got harness=%q, want fiz (local)", dec.Harness)
	}
}

func TestSmartPrefersCloud(t *testing.T) {
	in := newTestRoutingEngine()
	req := Request{Policy: "smart"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness == "fiz" {
		t.Errorf("smart policy: got harness=fiz (local); should prefer cloud")
	}
}

func TestFirstClassPolicyRoutingSemantics(t *testing.T) {
	tests := []struct {
		name  string
		req   Request
		in    Inputs
		check func(*testing.T, *Decision, error)
	}{
		{
			name: "local-only success",
			req:  Request{Policy: "air-gapped", Require: []string{"no_remote"}, ProviderPreference: ProviderPreferenceLocalOnly},
			in:   newTestRoutingEngine(),
			check: func(t *testing.T, dec *Decision, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Resolve local: %v", err)
				}
				if dec.Harness != "fiz" {
					t.Fatalf("local policy selected harness=%q provider=%q, want local fiz harness", dec.Harness, dec.Provider)
				}
			},
		},
		{
			name: "local-only miss returns typed error",
			req:  Request{Policy: "air-gapped", Require: []string{"no_remote"}, ProviderPreference: ProviderPreferenceLocalOnly},
			in: func() Inputs {
				in := newTestRoutingEngine()
				for i := range in.Harnesses {
					if in.Harnesses[i].Name == "fiz" {
						in.Harnesses[i].Available = false
					}
				}
				return in
			}(),
			check: func(t *testing.T, _ *Decision, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected local policy miss")
				}
				var typed *ErrPolicyRequirementUnsatisfied
				if !errors.As(err, &typed) {
					t.Fatalf("error type=%T, want ErrPolicyRequirementUnsatisfied: %v", err, err)
				}
				if typed.Policy != "air-gapped" || typed.Requirement != "local endpoint" {
					t.Fatalf("ErrPolicyRequirementUnsatisfied=%#v, want air-gapped/local endpoint", typed)
				}
			},
		},
		{
			name: "default applies deterministic provider tiebreak",
			req:  Request{Policy: "default", ProviderPreference: ProviderPreferenceLocalFirst},
			in: Inputs{
				Harnesses: []HarnessEntry{{
					Name:                "fiz",
					Surface:             "embedded-openai",
					CostClass:           "local",
					IsLocal:             true,
					AutoRoutingEligible: true,
					Available:           true,
					QuotaOK:             true,
					SubscriptionOK:      true,
					ExactPinSupport:     true,
					SupportsTools:       true,
					Providers: []ProviderEntry{
						{Name: "z-local", DefaultModel: "capable-model", SupportsTools: true},
						{Name: "a-local", DefaultModel: "capable-model", SupportsTools: true},
					},
				}},
			},
			check: func(t *testing.T, dec *Decision, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Resolve default: %v", err)
				}
				if dec.Harness != "fiz" || dec.Provider != "a-local" {
					t.Fatalf("default tiebreak selected harness=%q provider=%q, want fiz/a-local", dec.Harness, dec.Provider)
				}
			},
		},
		{
			name: "default prefers lower known cost",
			req:  Request{Policy: "default", ProviderPreference: ProviderPreferenceLocalFirst},
			in: Inputs{
				Harnesses: []HarnessEntry{
					{
						Name:                "codex",
						Surface:             "codex",
						CostClass:           "medium",
						IsSubscription:      true,
						AutoRoutingEligible: true,
						Available:           true,
						QuotaOK:             true,
						SubscriptionOK:      true,
						ExactPinSupport:     true,
						SupportsTools:       true,
						Providers: []ProviderEntry{{
							Name:               "codex-sub",
							DefaultModel:       "default-model",
							CostUSDPer1kTokens: 0.02,
							CostSource:         CostSourceUserConfig,
							SupportsTools:      true,
						}},
					},
					{
						Name:                "claude",
						Surface:             "claude",
						CostClass:           "medium",
						IsSubscription:      true,
						AutoRoutingEligible: true,
						Available:           true,
						QuotaOK:             true,
						SubscriptionOK:      true,
						ExactPinSupport:     true,
						SupportsTools:       true,
						Providers: []ProviderEntry{{
							Name:               "claude-sub",
							DefaultModel:       "default-model",
							CostUSDPer1kTokens: 0.01,
							CostSource:         CostSourceUserConfig,
							SupportsTools:      true,
						}},
					},
				},
			},
			check: func(t *testing.T, dec *Decision, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Resolve default: %v", err)
				}
				if dec.Harness != "claude" || dec.Provider != "claude-sub" {
					t.Fatalf("default selected harness=%q provider=%q, want cheaper claude/claude-sub", dec.Harness, dec.Provider)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := Resolve(tt.req, tt.in)
			tt.check(t, dec, err)
		})
	}
}

func TestSmartDoesNotSelectUnmodeledGeminiOverModeledFiz(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				DefaultModel:        "qwen3.5-27b",
			},
			{
				Name:            "gemini",
				Surface:         "gemini",
				CostClass:       "experimental",
				Available:       true,
				QuotaOK:         true,
				SubscriptionOK:  true,
				ExactPinSupport: true,
				SupportsTools:   true,
			},
		},
	}

	dec, err := Resolve(Request{Policy: "smart", AllowLocal: true}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "fiz" {
		t.Fatalf("smart should use modeled fiz route, got harness=%q model=%q", dec.Harness, dec.Model)
	}
	for _, c := range dec.Candidates {
		if c.Harness == "gemini" && c.Eligible {
			t.Fatalf("gemini should not be eligible without a smart policy model: %#v", c)
		}
	}
}

func TestReasoningRequestsDoNotSelectGemini(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "gemini",
				Surface:             "gemini",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				DefaultModel:        "gemini-2.5-pro",
			},
			{
				Name:                "codex",
				Surface:             "codex",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				SupportedReasoning:  []string{"low", "medium", "high"},
				DefaultModel:        "gpt-5.4",
			},
		},
	}

	dec, err := Resolve(Request{Policy: "smart", Reasoning: "high"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness == "gemini" {
		t.Fatalf("reasoning request should not select gemini: %#v", dec)
	}
	foundGemini := false
	for _, c := range dec.Candidates {
		if c.Harness == "gemini" {
			foundGemini = true
			if c.Eligible || !strings.Contains(c.Reason, `reasoning "high" not supported`) {
				t.Fatalf("gemini reasoning candidate: %#v", c)
			}
		}
	}
	if !foundGemini {
		t.Fatal("expected gemini candidate to prove reasoning gate")
	}
}

func TestSmartSelectsGeminiOnlyWhenEligibleBestCandidate(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				DefaultModel:        "qwen3.5-27b",
			},
			{
				Name:                "gemini",
				Surface:             "gemini",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				DefaultModel:        "gemini-2.5-pro",
			},
		},
	}

	dec, err := Resolve(Request{Policy: "smart"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "gemini" || dec.Model != "gemini-2.5-pro" {
		t.Fatalf("smart should select eligible gemini over lower-scored local route, got harness=%q model=%q", dec.Harness, dec.Model)
	}

	in.Harnesses[1].SubscriptionOK = false
	in.Harnesses[1].QuotaOK = false
	dec, err = Resolve(Request{Policy: "smart", AllowLocal: true}, in)
	if err != nil {
		t.Fatalf("Resolve after auth gate: %v", err)
	}
	if dec.Harness == "gemini" {
		t.Fatalf("gemini should not win when auth/quota evidence is missing: %#v", dec)
	}
}

func TestStableTieBreakerAlphabetical(t *testing.T) {
	// Two equal-score candidates → alphabetical winner.
	in := Inputs{
		Harnesses: []HarnessEntry{
			{Name: "zharness", Surface: "x", CostClass: "medium", AutoRoutingEligible: true, Available: true, QuotaOK: true, SubscriptionOK: true, DefaultModel: "z", ExactPinSupport: true, SupportsTools: true},
			{Name: "aharness", Surface: "x", CostClass: "medium", AutoRoutingEligible: true, Available: true, QuotaOK: true, SubscriptionOK: true, DefaultModel: "a", ExactPinSupport: true, SupportsTools: true},
		},
	}
	req := Request{Policy: "default"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "aharness" {
		t.Errorf("alphabetical tiebreak: got %q, want aharness", dec.Harness)
	}
}

func TestNoViableCandidate(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{Name: "down", AutoRoutingEligible: true, Available: false},
		},
	}
	req := Request{Policy: "cheap"}
	_, err := Resolve(req, in)
	if err == nil {
		t.Fatal("expected error when no harness available")
	}
	if !strings.Contains(err.Error(), "no viable") {
		t.Errorf("error should mention 'no viable': %v", err)
	}
}

func TestResolveRoute_GeminiRejectsNonGeminiModel(t *testing.T) {
	gemini := HarnessEntry{
		Name:                "gemini",
		Surface:             "gemini",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		DefaultModel:        "gemini-2.5-flash",
		SupportedModels:     []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"},
		SupportsTools:       true,
	}
	fiz := HarnessEntry{
		Name:                "fiz",
		Surface:             "embedded-openai",
		CostClass:           "local",
		IsLocal:             true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		SupportedModels:     []string{"qwen/qwen3.6"},
		SupportsTools:       true,
		Providers: []ProviderEntry{{
			Name:               "minimax",
			EndpointName:       "local",
			DiscoveredIDs:      []string{"minimax/minimax-m2.7"},
			DiscoveryAttempted: true,
			SupportsTools:      true,
		}},
	}

	in := Inputs{Harnesses: []HarnessEntry{gemini, fiz}}
	dec, err := Resolve(Request{Policy: "default", Model: "minimax/minimax-m2.7"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "fiz" || dec.Model != "minimax/minimax-m2.7" {
		t.Fatalf("got harness=%q model=%q, want fiz/minimax", dec.Harness, dec.Model)
	}
	for _, c := range dec.Candidates {
		if c.Harness == "gemini" && c.Eligible {
			t.Fatalf("gemini must reject non-gemini exact pin: %#v", c)
		}
		if c.Harness == "gemini" && c.Reason != "model not in harness allow-list" {
			t.Fatalf("gemini rejection reason=%q, want allow-list reason", c.Reason)
		}
	}

	dec, err = Resolve(Request{Policy: "default", Model: "minimax/minimax-m2.7"}, Inputs{Harnesses: []HarnessEntry{gemini}})
	if err == nil {
		t.Fatal("expected no viable candidate without fiz live endpoint")
	}
	var noViable *NoViableCandidateError
	if !errors.As(err, &noViable) {
		t.Fatalf("error type=%T, want *NoViableCandidateError: %v", err, err)
	}
	if dec != nil && dec.Harness == "gemini" {
		t.Fatalf("must not pick gemini for non-gemini model: %#v", dec)
	}
	for _, c := range dec.Candidates {
		if c.Harness == "gemini" {
			if c.Eligible {
				t.Fatalf("gemini candidate must be ineligible: %#v", c)
			}
			if c.Reason != "model not in harness allow-list" {
				t.Fatalf("gemini rejection reason=%q, want allow-list reason", c.Reason)
			}
		}
	}
}

func TestResolveExplicitHarnessModelIncompatible(t *testing.T) {
	gemini := HarnessEntry{
		Name:                "gemini",
		Surface:             "gemini",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		DefaultModel:        "gemini-2.5-flash",
		SupportedModels:     []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"},
		SupportsTools:       true,
	}

	_, err := Resolve(Request{Harness: "gemini", Model: "minimax/minimax-m2.7"}, Inputs{Harnesses: []HarnessEntry{gemini}})
	if err == nil {
		t.Fatal("expected explicit harness/model incompatibility")
	}
	if !errors.Is(err, ErrUnsatisfiablePin{}) {
		t.Fatalf("errors.Is should match ErrUnsatisfiablePin: %T %v", err, err)
	}
	var typed *ErrUnsatisfiablePin
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrUnsatisfiablePin: %T %v", err, err)
	}
	if typed.Pin != "harness=gemini+model=minimax/minimax-m2.7" {
		t.Fatalf("Pin=%q, want harness=gemini+model=minimax/minimax-m2.7", typed.Pin)
	}

	wrapped := fmt.Errorf("ddx preflight: %w", err)
	if !errors.Is(wrapped, ErrUnsatisfiablePin{}) {
		t.Fatal("wrapped error should still match ErrUnsatisfiablePin")
	}
}

// TestResolveExplicitProviderRoutedHarnessProviderPinAcceptsAnyModel verifies
// that provider-routed subprocess harnesses accept provider-pinned local model
// IDs. The harness CLI owns concrete per-provider validation in that case.
func TestResolveExplicitProviderRoutedHarnessProviderPinAcceptsAnyModel(t *testing.T) {
	for _, harness := range []struct {
		name            string
		defaultModel    string
		supportedModels []string
	}{
		{name: "pi", defaultModel: "gemini-2.5-flash", supportedModels: []string{"gemini-2.5-flash", "gemini-2.5-pro"}},
		{name: "opencode", defaultModel: "opencode/gpt-5.4", supportedModels: []string{"opencode/gpt-5.4", "opencode/claude-sonnet-4-6"}},
	} {
		t.Run(harness.name, func(t *testing.T) {
			entry := HarnessEntry{
				Name:                harness.name,
				Surface:             harness.name,
				CostClass:           "medium",
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				DefaultModel:        harness.defaultModel,
				SupportedModels:     harness.supportedModels,
				Providers: []ProviderEntry{
					{Name: "omlx-vidar-1235", DefaultModel: "Qwen3.6-27B-MLX-8bit"},
				},
				SupportsTools: true,
			}

			_, err := Resolve(Request{Harness: harness.name, Model: "Qwen3.6-27B-MLX-8bit"}, Inputs{Harnesses: []HarnessEntry{entry}})
			if err == nil {
				t.Fatalf("expected %s+Qwen without provider pin to fail model validation", harness.name)
			}
			if !errors.Is(err, ErrUnsatisfiablePin{}) {
				t.Fatalf("errors.Is should match ErrUnsatisfiablePin without provider pin: %T %v", err, err)
			}

			_, err = Resolve(Request{
				Harness:  harness.name,
				Provider: "omlx-vidar-1235",
				Model:    "Qwen3.6-27B-MLX-8bit",
			}, Inputs{Harnesses: []HarnessEntry{entry}})
			if errors.Is(err, ErrUnsatisfiablePin{}) {
				t.Fatalf("%s+provider-pin must NOT yield ErrUnsatisfiablePin: %T %v", harness.name, err, err)
			}
		})
	}
}

func TestNoViableCandidateIsNotExplicitPinError(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{Name: "down", AutoRoutingEligible: true, Available: false},
		},
	}
	_, err := Resolve(Request{Policy: "cheap"}, in)
	if err == nil {
		t.Fatal("expected no viable candidate")
	}
	var noViable *NoViableCandidateError
	if !errors.As(err, &noViable) {
		t.Fatalf("error type=%T, want NoViableCandidateError", err)
	}
	if errors.Is(err, ErrUnsatisfiablePin{}) {
		t.Fatal("ambient no viable error must not match ErrUnsatisfiablePin")
	}
}

func TestResolveRoute_GeminiAllowListExactPinSucceeds(t *testing.T) {
	gemini := HarnessEntry{
		Name:                "gemini",
		Surface:             "gemini",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		DefaultModel:        "gemini-2.5-flash",
		SupportedModels:     []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"},
		SupportsTools:       true,
	}

	dec, err := Resolve(Request{Policy: "default", Model: "gemini-2.5-flash"}, Inputs{Harnesses: []HarnessEntry{gemini}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "gemini" || dec.Model != "gemini-2.5-flash" {
		t.Fatalf("got harness=%q model=%q, want gemini/gemini-2.5-flash", dec.Harness, dec.Model)
	}
}

func TestHarnessOverrideRejectsOthers(t *testing.T) {
	in := newTestRoutingEngine()
	req := Request{Harness: "codex"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "codex" {
		t.Errorf("Harness=codex pin: got %q, want codex", dec.Harness)
	}
	// Only codex candidates should appear.
	for _, c := range dec.Candidates {
		if c.Harness != "codex" {
			t.Errorf("Harness=codex pin: candidate %q leaked", c.Harness)
		}
	}
}

func TestLocalAliasResolvesToFiz(t *testing.T) {
	in := newTestRoutingEngine()
	req := Request{Harness: "local"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "fiz" {
		t.Errorf("Harness=local must resolve to fiz; got %q", dec.Harness)
	}
}

// Eligible reports whether the Decision picked a viable candidate.
func (d *Decision) Eligible() bool {
	return d != nil && d.Harness != ""
}

// TestRouteRequest_ExcludedRoutesSkipsCandidate asserts that a candidate
// matching an entry in RouteRequest.ExcludedRoutes is filtered out of the
// route decision with FilterReasonCallerExcluded.
func TestRouteRequest_ExcludedRoutesSkipsCandidate(t *testing.T) {
	in := newTestRoutingEngine()
	req := Request{
		Policy:         "cheap",
		ExcludedRoutes: []ExcludedRoute{{Provider: "vidar-omlx"}},
	}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider == "vidar-omlx" {
		t.Errorf("want vidar-omlx excluded from selection, got %q", dec.Provider)
	}
	// vidar-omlx candidate must appear in the list with FilterReasonCallerExcluded.
	var found bool
	for _, c := range dec.Candidates {
		if c.Provider == "vidar-omlx" && c.FilterReason == FilterReasonCallerExcluded {
			found = true
			break
		}
	}
	if !found {
		t.Error("want vidar-omlx candidate with FilterReasonCallerExcluded in candidates list")
	}
}

// TestRouteRequest_ExcludedRoutesAllowsOtherCandidates asserts that an
// ExcludedRoutes entry with a specific model does not block a different model
// on the same provider.
func TestRouteRequest_ExcludedRoutesAllowsOtherCandidates(t *testing.T) {
	in := newTestRoutingEngine()
	// Exclude vidar-omlx only for "Qwen3.6-35B-A3B-4bit"; the other model
	// "MiniMax-M2.5-MLX-4bit" on the same provider must remain selectable.
	req := Request{
		Policy:   "cheap",
		Provider: "vidar-omlx",
		Model:    "MiniMax-M2.5-MLX-4bit",
		ExcludedRoutes: []ExcludedRoute{
			{Provider: "vidar-omlx", Model: "Qwen3.6-35B-A3B-4bit"},
		},
	}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve with non-matching exclusion: %v", err)
	}
	if dec.Provider != "vidar-omlx" {
		t.Errorf("want vidar-omlx selected for non-excluded model, got %q", dec.Provider)
	}
	if dec.Model != "MiniMax-M2.5-MLX-4bit" {
		t.Errorf("want model MiniMax-M2.5-MLX-4bit, got %q", dec.Model)
	}
}
