package fizeau

import "testing"

// TestResolveExecuteRouteWithEngineForwardsPromptShape is the sole
// same-package seam for the public request-to-routing-engine adapter. Concrete
// execute-route validation and dispatch selection are owned and tested by
// internal/serviceimpl.
func TestResolveExecuteRouteWithEngineForwardsPromptShape(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  gpt-5.5:
    family: gpt
    status: active
    power: 9
    context_window: 4096
    surfaces:
      codex: gpt-5.5
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    context_window: 200000
    surfaces:
      codex: gpt-5.4
  gpt-5.4-mini:
    family: gpt
    status: active
    power: 6
    context_window: 200000
    no_tools: true
    surfaces:
      codex: gpt-5.4-mini
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := publicRouteTraceService(nil)
	t.Setenv("PATH", "")
	forceAvailableHarnessesForTest(t, svc, "codex")
	decision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{
		Harness:               "codex",
		Policy:                "default",
		Prompt:                "x",
		EstimatedPromptTokens: 100000,
		RequiresTools:         true,
	})
	if err != nil {
		t.Fatalf("resolveExecuteRoute: %v", err)
	}
	if decision == nil {
		t.Fatal("resolveExecuteRoute returned nil decision")
	}
	if decision.Model != "gpt-5.4" {
		t.Fatalf("Model=%q, want gpt-5.4 after prompt-shape gating", decision.Model)
	}
	smallCtx := findRouteCandidateByHarnessAndModel(t, decision, "codex", "gpt-5.5")
	if smallCtx.Eligible || smallCtx.FilterReason != FilterReasonContextTooSmall {
		t.Fatalf("gpt-5.5 candidate=%#v, want context-window rejection", smallCtx)
	}
	noTools := findRouteCandidateByHarnessAndModel(t, decision, "codex", "gpt-5.4-mini")
	if noTools.Eligible || noTools.FilterReason != FilterReasonNoToolSupport {
		t.Fatalf("gpt-5.4-mini candidate=%#v, want no-tools rejection", noTools)
	}
}

func findRouteCandidateByHarnessAndModel(t *testing.T, decision *RouteDecision, harness, model string) RouteCandidate {
	t.Helper()
	if decision == nil {
		t.Fatal("nil route decision")
	}
	for _, candidate := range decision.Candidates {
		if candidate.Harness == harness && candidate.Model == model {
			return candidate
		}
	}
	t.Fatalf("candidate harness=%q model=%q not found in %#v", harness, model, decision.Candidates)
	return RouteCandidate{}
}
