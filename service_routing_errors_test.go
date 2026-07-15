package fizeau

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func TestResolveRouteExplicitHarnessModelIncompatible(t *testing.T) {
	svc := testRoutingErrorService()

	_, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness: "gemini",
		Model:   "minimax/minimax-m2.7",
	})
	if err == nil {
		t.Fatal("expected explicit harness/model incompatibility")
	}
	if !errors.Is(err, ErrHarnessModelIncompatible{}) {
		t.Fatalf("errors.Is should match ErrHarnessModelIncompatible: %T %v", err, err)
	}
	var typed *ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrHarnessModelIncompatible: %T %v", err, err)
	}
	if typed.Harness != "gemini" {
		t.Fatalf("Harness=%q, want gemini", typed.Harness)
	}
	if typed.Model != "minimax/minimax-m2.7" {
		t.Fatalf("Model=%q, want minimax/minimax-m2.7", typed.Model)
	}
	want := []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini", "gemini-2.5"}
	if !slices.Equal(typed.SupportedModels, want) {
		t.Fatalf("SupportedModels=%v, want %v", typed.SupportedModels, want)
	}
}

func TestValidateExplicitHarnessModelAcceptsClaudeDiscoveredFamilyVersion(t *testing.T) {
	registry := harnesses.NewRegistry()
	cfg, ok := registry.Get("claude")
	if !ok {
		t.Fatal("missing claude registry entry")
	}

	if err := validateExplicitHarnessModel("claude", cfg, "opus-4.7", ""); err != nil {
		t.Fatalf("opus-4.7 should be accepted as a discovered Claude family version: %v", err)
	}
	err := validateExplicitHarnessModel("claude", cfg, "opus-9.9", "")
	if err == nil {
		t.Fatal("expected unknown claude family version to be rejected")
	}
	var typed *ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("expected ErrHarnessModelIncompatible, got %T %v", err, err)
	}
	if !slices.Contains(typed.SupportedModels, "opus-4.7") {
		t.Fatalf("supported models should include discovered opus version, got %v", typed.SupportedModels)
	}
}

func TestValidateExplicitHarnessModelProviderRoutedHarnessesAcceptLocalProviderPin(t *testing.T) {
	registry := harnesses.NewRegistry()

	for _, name := range []string{"pi", "opencode"} {
		t.Run(name, func(t *testing.T) {
			cfg, ok := registry.Get(name)
			if !ok {
				t.Fatalf("missing %s registry entry", name)
			}

			// With an explicit provider pin, a local model ID must be accepted:
			// the harness itself owns per-provider model validation.
			if err := validateExplicitHarnessModel(name, cfg, "qwen3.6-27b", "lmstudio"); err != nil {
				t.Fatalf("%s+lmstudio+qwen should be accepted: %v", name, err)
			}
			if err := validateExplicitHarnessModel(name, cfg, "qwen3.6-27b", "omlx"); err != nil {
				t.Fatalf("%s+omlx+qwen should be accepted: %v", name, err)
			}

			// Without a provider pin, the harness's static model gate still applies.
			err := validateExplicitHarnessModel(name, cfg, "qwen3.6-27b", "")
			if err == nil {
				t.Fatalf("expected %s to reject non-default model without provider pin", name)
			}
			var typed *ErrHarnessModelIncompatible
			if !errors.As(err, &typed) {
				t.Fatalf("expected ErrHarnessModelIncompatible, got %T %v", err, err)
			}
		})
	}

	piCfg, ok := registry.Get("pi")
	if !ok {
		t.Fatal("missing pi registry entry")
	}
	// Regression: Pi Gemini defaults still validate cleanly.
	if err := validateExplicitHarnessModel("pi", piCfg, "gemini-2.5-flash", ""); err != nil {
		t.Fatalf("gemini-2.5-flash must remain valid for pi: %v", err)
	}
	if err := validateExplicitHarnessModel("pi", piCfg, "gemini-2.5-pro", ""); err != nil {
		t.Fatalf("gemini-2.5-pro must remain valid for pi: %v", err)
	}
}

func TestResolveExecuteRouteNormalizesSubprocessAliases(t *testing.T) {
	previousResolver := resolveSubprocessModelAlias
	t.Cleanup(func() { resolveSubprocessModelAlias = previousResolver })
	resolveSubprocessModelAlias = func(harness, model string) string {
		if harness == "claude" {
			return claudeCLIExecutableModel(model)
		}
		return model
	}

	svc := testRoutingErrorService()

	claudeDecision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "claude", Model: "sonnet"})
	if err != nil {
		t.Fatalf("resolve claude sonnet alias: %v", err)
	}
	if claudeDecision.Model != "sonnet" {
		t.Fatalf("claude sonnet alias resolved to %q, want sonnet", claudeDecision.Model)
	}

	claudeOpusDecision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "claude", Model: "opus-4.7"})
	if err != nil {
		t.Fatalf("resolve claude opus version: %v", err)
	}
	if claudeOpusDecision.Model != "opus" {
		t.Fatalf("claude opus version normalized to %q, want opus", claudeOpusDecision.Model)
	}

	claudeFullOpusDecision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "claude", Model: "claude-opus-4-6"})
	if err != nil {
		t.Fatalf("resolve claude full opus version: %v", err)
	}
	if claudeFullOpusDecision.Model != "opus" {
		t.Fatalf("claude full opus version normalized to %q, want opus", claudeFullOpusDecision.Model)
	}

	codexDecision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "codex", Model: "gpt"})
	if err != nil {
		t.Fatalf("resolve codex gpt alias: %v", err)
	}
	if codexDecision.Model != "gpt-5.5" {
		t.Fatalf("codex gpt alias resolved to %q, want gpt-5.5", codexDecision.Model)
	}

	geminiDecision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "gemini", Model: "gemini"})
	if err != nil {
		t.Fatalf("resolve gemini alias: %v", err)
	}
	if geminiDecision.Model != "gemini-2.5-pro" {
		t.Fatalf("gemini alias resolved to %q, want gemini-2.5-pro", geminiDecision.Model)
	}
}

func TestResolveExplicitClaudeRejectedWhenFreshQuotaExhausted(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", cachePath)

	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour).Unix()
	writeClaudeQuotaCacheFile(t, cachePath, claudeTestQuotaSnapshot{
		CapturedAt:        now,
		FiveHourRemaining: 0,
		FiveHourLimit:     100,
		WeeklyRemaining:   0,
		WeeklyLimit:       100,
		Windows: []harnesses.QuotaWindow{{
			Name:         "Current week (all models)",
			LimitID:      "weekly-all",
			UsedPercent:  100,
			ResetsAtUnix: reset,
			State:        "exhausted",
		}},
		Source:  "runtime_error",
		Account: &harnesses.AccountInfo{PlanType: "Claude Max"},
	})

	svc := testRoutingErrorService()
	_, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "claude", Model: "opus-4.7"})
	if err == nil {
		t.Fatal("expected exhausted Claude quota to reject explicit claude route")
	}
	var quotaErr *NoViableProviderForNow
	if !errors.As(err, &quotaErr) {
		t.Fatalf("error=%T %v, want NoViableProviderForNow", err, err)
	}
	if !slices.Equal(quotaErr.ExhaustedProviders, []string{"claude"}) {
		t.Fatalf("ExhaustedProviders=%v, want [claude]", quotaErr.ExhaustedProviders)
	}
	if got := quotaErr.RetryAfter.Unix(); got != reset {
		t.Fatalf("RetryAfter unix=%d, want %d", got, reset)
	}
}

func TestResolveRouteExplicitPolicyRequirementUnsatisfied(t *testing.T) {
	svc := testRoutingErrorService()

	_, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Policy:   "air-gapped",
		Provider: "openrouter",
	})
	if err == nil {
		t.Fatal("expected air-gapped policy to conflict with remote provider pin")
	}
	if !errors.Is(err, ErrPolicyRequirementUnsatisfied{}) {
		t.Fatalf("errors.Is should match ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "air-gapped" || typed.AttemptedPin != "" || typed.Requirement != "local endpoint" {
		t.Fatalf("policy requirement=%#v, want air-gapped//local endpoint", typed)
	}

	_, err = svc.ResolveRoute(context.Background(), RouteRequest{
		Policy:  "smart",
		Harness: "fiz",
	})
	if err == nil {
		t.Fatal("expected smart policy to conflict with local fiz harness")
	}
	var inverse *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &inverse) {
		t.Fatalf("errors.As inverse: %T %v", err, err)
	}
	if inverse.Policy != "smart" || inverse.AttemptedPin != "Harness=fiz" || inverse.Requirement != "subscription-only" {
		t.Fatalf("inverse policy requirement=%#v, want smart/Harness=fiz/subscription-only", inverse)
	}
}

func TestErrPolicyRequirementUnsatisfiedShape(t *testing.T) {
	err := fmt.Errorf("preflight: %w", &ErrPolicyRequirementUnsatisfied{
		Policy:       "air-gapped",
		Requirement:  "no_remote",
		AttemptedPin: "Provider=openrouter",
		Rejected:     2,
	})
	if !errors.Is(err, ErrPolicyRequirementUnsatisfied{}) {
		t.Fatalf("errors.Is should match ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "air-gapped" || typed.Requirement != "no_remote" || typed.AttemptedPin != "Provider=openrouter" || typed.Rejected != 2 {
		t.Fatalf("ErrPolicyRequirementUnsatisfied=%#v, want full public shape", typed)
	}
}

func TestResolveRouteUnknownPolicyIsTyped(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		minPower   int
		maxPower   int
		nilCatalog bool
	}{
		{name: "unknown", policy: "does-not-exist"},
		{name: "raw whitespace", policy: " does-not-exist ", minPower: 2, maxPower: 9},
		{name: "spaced deprecated name stays unknown", policy: " code-high ", minPower: 2, maxPower: 9},
		{name: "nil catalog", policy: "default", minPower: 3, maxPower: 7, nilCatalog: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.nilCatalog {
				t.Cleanup(replaceRoutingCatalogForTest(t, nil))
			}
			svc := testRoutingErrorService()
			decision, err := svc.ResolveRoute(context.Background(), RouteRequest{
				Policy:   test.policy,
				MinPower: test.minPower,
				MaxPower: test.maxPower,
			})
			if err == nil {
				t.Fatal("expected unknown policy error")
			}
			if !errors.Is(err, ErrUnknownPolicy{}) {
				t.Fatalf("errors.Is should match ErrUnknownPolicy: %T %v", err, err)
			}
			var typed *ErrUnknownPolicy
			if !errors.As(err, &typed) {
				t.Fatalf("errors.As should extract ErrUnknownPolicy: %T %v", err, err)
			}
			if typed.Policy != test.policy {
				t.Fatalf("Policy=%q, want raw %q", typed.Policy, test.policy)
			}
			if decision == nil {
				t.Fatal("ResolveRoute returned nil decision with policy error")
			}
			if decision.RequestedPolicy != test.policy {
				t.Fatalf("RequestedPolicy=%q, want raw %q", decision.RequestedPolicy, test.policy)
			}
			wantPower := RoutePowerPolicy{PolicyName: test.policy, MinPower: test.minPower, MaxPower: test.maxPower}
			if decision.PowerPolicy != wantPower {
				t.Fatalf("PowerPolicy=%#v, want %#v", decision.PowerPolicy, wantPower)
			}
		})
	}
}

func TestResolveRouteLegacyPolicyAliasesAreRejected(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-12T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
  cheap:
    min_power: 1
    max_power: 5
    allow_local: true
  smart:
    min_power: 6
    max_power: 10
    allow_local: false
  air-gapped:
    min_power: 1
    max_power: 5
    allow_local: true
    require: [no_remote]
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := testRoutingErrorService()
	for _, alias := range []string{"standard", "code-fast", "fast", "code-smart", "code-economy", "local", "offline"} {
		t.Run(alias, func(t *testing.T) {
			_, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: alias})
			if err == nil {
				t.Fatalf("expected legacy policy alias %q to be rejected", alias)
			}
			if !errors.Is(err, ErrUnknownPolicy{}) {
				t.Fatalf("errors.Is should match ErrUnknownPolicy for %q: %T %v", alias, err, err)
			}
			var typed *ErrUnknownPolicy
			if !errors.As(err, &typed) {
				t.Fatalf("errors.As should extract ErrUnknownPolicy for %q: %T %v", alias, err, err)
			}
			if typed.Policy != alias {
				t.Fatalf("Policy=%q, want %q", typed.Policy, alias)
			}
		})
	}
}

func TestResolveRouteLegacyCodePoliciesRejectWithReplacementGuidance(t *testing.T) {
	svc := testRoutingErrorService()

	for policy, want := range map[string]string{
		"code-medium": `policy "code-medium" is deprecated; use --policy default or --min-power/--max-power`,
		"code-high":   `policy "code-high" is deprecated; use --policy smart or --min-power/--max-power`,
	} {
		t.Run(policy, func(t *testing.T) {
			decision, err := svc.ResolveRoute(context.Background(), RouteRequest{
				Policy:   policy,
				MinPower: 2,
				MaxPower: 9,
			})
			if err == nil {
				t.Fatalf("expected %s to be rejected", policy)
			}
			if err.Error() != want {
				t.Fatalf("error=%q, want exact %q", err.Error(), want)
			}
			if decision == nil {
				t.Fatal("ResolveRoute returned nil decision with deprecated policy error")
			}
			wantPower := RoutePowerPolicy{PolicyName: policy, MinPower: 2, MaxPower: 9}
			if decision.RequestedPolicy != policy || decision.PowerPolicy != wantPower {
				t.Fatalf("decision=%#v, want RequestedPolicy=%q PowerPolicy=%#v", decision, policy, wantPower)
			}
		})
	}
}

func TestPublicCatalogPolicyErrorProjection(t *testing.T) {
	t.Run("unknown identity", func(t *testing.T) {
		err := publicCatalogPolicyError(&serviceimpl.CatalogPolicyFailure{
			Kind:   serviceimpl.CatalogPolicyFailureUnknownPolicy,
			Policy: " raw-policy ",
		})
		if !errors.Is(err, ErrUnknownPolicy{}) {
			t.Fatalf("errors.Is should match ErrUnknownPolicy: %T %v", err, err)
		}
		var typed *ErrUnknownPolicy
		if !errors.As(err, &typed) || typed.Policy != " raw-policy " {
			t.Fatalf("error=%#v, want raw *ErrUnknownPolicy", err)
		}
	})

	t.Run("deprecated text", func(t *testing.T) {
		err := publicCatalogPolicyError(&serviceimpl.CatalogPolicyFailure{
			Kind:              serviceimpl.CatalogPolicyFailureDeprecatedPolicy,
			Policy:            "code-high",
			ReplacementPolicy: "smart",
		})
		want := `policy "code-high" is deprecated; use --policy smart or --min-power/--max-power`
		if err == nil || err.Error() != want {
			t.Fatalf("error=%v, want exact %q", err, want)
		}
	})

	t.Run("unsupported preference text", func(t *testing.T) {
		err := publicCatalogPolicyError(&serviceimpl.CatalogPolicyFailure{
			Kind:               serviceimpl.CatalogPolicyFailureUnsupportedProviderPreference,
			Policy:             "custom",
			ProviderPreference: "sideways",
		})
		want := `policy "custom" has unsupported provider preference "sideways"`
		if err == nil || err.Error() != want {
			t.Fatalf("error=%v, want exact %q", err, want)
		}
	})
}

func TestResolveRouteAirGappedNoLocalCandidateIsTyped(t *testing.T) {
	svc := testRoutingErrorService()

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "air-gapped"})
	if err == nil {
		t.Fatal("expected air-gapped policy without local candidates to fail")
	}
	if !errors.Is(err, ErrPolicyRequirementUnsatisfied{}) {
		t.Fatalf("errors.Is should match ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "air-gapped" || typed.Requirement != "local endpoint" {
		t.Fatalf("ErrPolicyRequirementUnsatisfied=%#v, want air-gapped/local endpoint", typed)
	}
	if dec == nil || len(dec.Candidates) == 0 {
		t.Fatalf("expected rejected candidate trace, got decision=%#v", dec)
	}
}

func testRoutingErrorService() *service {
	registry := harnesses.NewRegistry()
	registry.LookPath = func(file string) (string, error) { return "/bin/" + file, nil }
	return &service{
		opts:     ServiceOptions{},
		registry: registry,
		hub:      newSessionHub(),
	}
}
