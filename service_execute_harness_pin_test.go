package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func TestExecuteExplicitHarnessPinsDispatchRequestedRunner(t *testing.T) {
	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"anthropic":  {Type: "anthropic", APIKey: "sk-test"},
			"openrouter": {Type: "openrouter"},
		},
		names:       []string{"anthropic", "openrouter"},
		defaultName: "anthropic",
	})

	cases := []struct {
		name           string
		req            ServiceExecuteRequest
		wantHarness    string
		wantNative     bool
		wantSubprocess bool
	}{
		{
			name: "codex",
			req: ServiceExecuteRequest{
				Prompt:   "hello",
				Harness:  "codex",
				Provider: "anthropic",
				Model:    "gpt-5.4",
			},
			wantHarness:    "codex",
			wantSubprocess: true,
		},
		{
			name: "pi",
			req: ServiceExecuteRequest{
				Prompt:   "hello",
				Harness:  "pi",
				Provider: "openrouter",
				Model:    "gemini-2.5-flash",
			},
			wantHarness:    "pi",
			wantSubprocess: true,
		},
		{
			name: "opencode",
			req: ServiceExecuteRequest{
				Prompt:   "hello",
				Harness:  "opencode",
				Provider: "anthropic",
				Model:    "opencode/gpt-5.4",
			},
			wantHarness:    "opencode",
			wantSubprocess: true,
		},
		{
			name: "fiz",
			req: ServiceExecuteRequest{
				Prompt:   "hello",
				Harness:  "fiz",
				Provider: "openrouter",
				Model:    "gpt-5.4",
			},
			wantHarness: "fiz",
			wantNative:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := svc.resolveExecuteRoute(tc.req)
			if err != nil {
				t.Fatalf("resolveExecuteRoute: %v", err)
			}
			if decision == nil {
				t.Fatal("resolveExecuteRoute returned nil decision")
			}
			if decision.Harness != tc.wantHarness {
				t.Fatalf("decision.Harness = %q, want %q", decision.Harness, tc.wantHarness)
			}

			var gotNative bool
			var gotSubprocess bool
			var gotRunner string
			serviceimpl.DispatchExecuteRun(context.Background(), serviceimpl.ExecuteDispatchRequest{
				Decision: serviceimpl.ExecuteRunnerDecision{
					Harness:  decision.Harness,
					Provider: decision.Provider,
					Model:    decision.Model,
				},
				Started: time.Now(),
			}, serviceimpl.ExecuteDispatchCallbacks{
				RunNative: func(ctx context.Context) {
					gotNative = true
				},
				RunSubprocess: func(ctx context.Context, runner harnesses.Harness) {
					gotSubprocess = true
					gotRunner = runner.Info().Name
				},
				RunVirtual: func(ctx context.Context) {
					t.Fatal("unexpected virtual dispatch")
				},
				RunScript: func(ctx context.Context) {
					t.Fatal("unexpected script dispatch")
				},
				IsHTTPProvider: func(string) bool {
					return false
				},
				Finalize: func(harnesses.FinalData) {
				},
			})

			if gotNative != tc.wantNative {
				t.Fatalf("RunNative called = %v, want %v", gotNative, tc.wantNative)
			}
			if gotSubprocess != tc.wantSubprocess {
				t.Fatalf("RunSubprocess called = %v, want %v", gotSubprocess, tc.wantSubprocess)
			}
			if tc.wantSubprocess && gotRunner != tc.wantHarness {
				t.Fatalf("subprocess runner = %q, want %q", gotRunner, tc.wantHarness)
			}
		})
	}
}

func TestExecuteExplicitHarnessPinUnknownHarnessFailsWithoutBroaderDispatch(t *testing.T) {
	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"anthropic":  {Type: "anthropic", APIKey: "sk-test"},
			"openrouter": {Type: "openrouter"},
		},
		names:       []string{"anthropic", "openrouter"},
		defaultName: "anthropic",
	})

	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:   "hello",
		Harness:  "does-not-exist",
		Provider: "anthropic",
		Model:    "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("Execute: unexpected synchronous error: %v", err)
	}
	final := readFinalEvent(t, ch, 5*time.Second)
	if final.Status != "failed" {
		t.Fatalf("final status = %q, want failed", final.Status)
	}
	if !strings.Contains(final.Error, "unknown harness") {
		t.Fatalf("final error = %q, want unknown harness", final.Error)
	}
}

func TestExecuteExplicitAgentHarnessNoLongerAliasesNative(t *testing.T) {
	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"openrouter": {Type: "openrouter"},
		},
		names:       []string{"openrouter"},
		defaultName: "openrouter",
	})

	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:   "hello",
		Harness:  "agent",
		Provider: "openrouter",
		Model:    "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("Execute: unexpected synchronous error: %v", err)
	}
	final := readFinalEvent(t, ch, 5*time.Second)
	if final.Status != "failed" || !strings.Contains(final.Error, `unknown harness "agent"`) {
		t.Fatalf("final = %#v, want unknown harness for legacy agent input", final)
	}
}

func TestExecuteExplicitHarnessPinRejectsUnsupportedCodexCombinationWithoutBroadening(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-06T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  small-ctx-model:
    family: test
    status: active
    surfaces: {agent.openai: small-ctx-model}
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "lmstudio", BaseURL: "http://127.0.0.1:9999/v1", Model: "small-ctx-model"},
		},
		names:       []string{"local"},
		defaultName: "local",
	})

	req := ServiceExecuteRequest{
		Prompt:   "hello",
		Harness:  "codex",
		Provider: "local",
		Model:    "small-ctx-model",
	}

	ch, err := svc.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected explicit codex pin to fail before dispatch")
	}
	if ch != nil {
		t.Fatalf("expected no event channel for typed pre-resolution error, got %#v", ch)
	}

	var typed *ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrHarnessModelIncompatible: %T %v", err, err)
	}
	if typed.Harness != "codex" {
		t.Fatalf("typed Harness = %q, want codex", typed.Harness)
	}
	if typed.Model != "small-ctx-model" {
		t.Fatalf("typed Model = %q, want small-ctx-model", typed.Model)
	}

	// Without the explicit harness pin, the same request is routable.
	decision, routeErr := svc.ResolveRoute(context.Background(), RouteRequest{
		Provider: req.Provider,
		Model:    req.Model,
	})
	if routeErr != nil {
		t.Fatalf("ResolveRoute without harness pin: %v", routeErr)
	}
	if decision == nil {
		t.Fatal("ResolveRoute without harness pin returned nil decision")
	}
	if decision.Harness == "codex" {
		t.Fatalf("ResolveRoute without harness pin still selected codex: %#v", decision)
	}
	if decision.Provider != "local" {
		t.Fatalf("ResolveRoute without harness pin provider = %q, want local", decision.Provider)
	}
}

// TestExecute_ExplicitHarnessEmptyModelWithPolicy_RoutesWithinHarness asserts
// AC4 from ddx-1e516bc9: when Harness is pinned and Model is empty but a
// routing policy is provided, the routing engine runs within the harness's
// eligible models (not the old silent-empty-model path).
//
// The core invariant: the old code returned RouteDecision{Model:""} with no
// error (silent misconfiguration). The fixed code invokes the routing engine —
// which either returns a non-empty model (success) or a routing error (no
// viable candidate due to environment quota state). Either outcome proves the
// engine was called; the old silent-empty-model outcome fails.
//
// When the routing engine succeeds, also assert the model is a claude-family
// alias (opus/sonnet/haiku or claude- prefix), as the catalog maps claude-code
// surface IDs through claudeCLIExecutableModel normalization.
func TestExecute_ExplicitHarnessEmptyModelWithProfile_RoutesWithinHarness(t *testing.T) {
	// Fixture catalog: sonnet-4.6 on the claude-code surface (which the claude
	// harness uses), plus the canonical cheap policy so
	// providerPreferenceForPolicy doesn't fail with ErrUnknownPolicy.
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-08T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
  cheap:
    min_power: 5
    max_power: 6
    allow_local: true
models:
  sonnet-4.6:
    family: claude-sonnet
    status: active
    power: 8
    surfaces:
      claude-code: sonnet-4.6
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	// No ServiceConfig — subscription harnesses (claude, codex) don't need it.
	svc := publicRouteTraceService(nil)
	decision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{
		Prompt:  "hello",
		Harness: "claude",
		Policy:  "cheap",
	})

	// Core invariant: the old code returned (decision.Model=="", err==nil).
	// The fix must invoke the routing engine — either successfully (model != "")
	// or with a routing error (no viable candidate). The old silent path is gone.
	if err == nil && (decision == nil || decision.Model == "") {
		t.Fatal("old silent-empty-model behavior: routing engine was not invoked (err=nil, model=empty)")
	}

	// When the routing engine succeeds, assert the model is a claude-family alias.
	if err == nil && decision != nil && decision.Model != "" {
		model := decision.Model
		if !strings.HasPrefix(model, "sonnet") &&
			!strings.HasPrefix(model, "opus") &&
			!strings.HasPrefix(model, "haiku") &&
			!strings.HasPrefix(model, "claude-") {
			t.Fatalf("resolved model %q is not a claude-family alias", model)
		}
		if decision.Harness != "claude" {
			t.Fatalf("decision.Harness = %q, want claude", decision.Harness)
		}
	}
}

// TestExecute_ExplicitHarnessEmptyModelNoPolicy_FailsClearly asserts AC5 from
// ddx-1e516bc9: when Harness is pinned and Model is empty with no routing
// inputs (no Policy, no MinPower), the request must fail with a clear
// "under-specified routing" error rather than silently returning an empty model.
func TestExecute_ExplicitHarnessEmptyModelNoPolicy_FailsClearly(t *testing.T) {
	svc := publicRouteTraceService(nil)

	_, err := svc.resolveExecuteRoute(ServiceExecuteRequest{
		Prompt:  "hello",
		Harness: "claude",
		// Model, Policy, MinPower all empty — under-specified
	})
	if err == nil {
		t.Fatal("expected under-specified routing error, got nil")
	}
	if !strings.Contains(err.Error(), "under-specified routing") {
		t.Fatalf("error %q should contain 'under-specified routing'", err.Error())
	}
}

// TestExecute_Class2HarnessEmptyModelWithProfile_FailsClearly asserts that for
// Class 2 harnesses (AutoRoutingEligible=false: gemini, opencode, pi), an
// explicit "no auto-resolution available" error is returned when Model is empty
// but Policy/MinPower is set — not silent empty Model.
func TestExecute_Class2HarnessEmptyModelWithPolicy_FailsClearly(t *testing.T) {
	svc := publicRouteTraceService(nil)

	_, err := svc.resolveExecuteRoute(ServiceExecuteRequest{
		Prompt:  "hello",
		Harness: "gemini",
		Policy:  "cheap",
	})
	if err == nil {
		t.Fatal("expected no-auto-resolution error for Class 2 harness, got nil")
	}
	if !strings.Contains(err.Error(), "no auto-resolution available") {
		t.Fatalf("error %q should contain 'no auto-resolution available'", err.Error())
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Fatalf("error %q should name the harness 'gemini'", err.Error())
	}
}

func TestExecuteHarnessPolicyDefaultSelectsCodexSupportedModel(t *testing.T) {
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
    exact_pin_only: true
    surfaces:
      codex: gpt-5.5
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    surfaces:
      codex: gpt-5.4
  gpt-5.4-mini:
    family: gpt
    status: active
    power: 6
    surfaces:
      codex: gpt-5.4-mini
  qwen3.5-27b:
    family: qwen
    status: active
    power: 8
    surfaces:
      agent.openai: qwen3.5-27b
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))
	stubSubprocessHarnessModelIDs(t, map[string][]string{
		"codex": {"gpt-5.4", "gpt-5.4-mini"},
	})

	svc := publicRouteTraceService(nil)
	t.Setenv("PATH", "")
	forceAvailableHarnessesForTest(t, svc, "codex")
	decision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{
		Harness: "codex",
		Policy:  "default",
		Prompt:  "x",
	})
	if err != nil {
		t.Fatalf("resolveExecuteRoute: %v", err)
	}
	if decision == nil {
		t.Fatal("resolveExecuteRoute returned nil decision")
	}
	if decision.Harness != "codex" {
		t.Fatalf("Harness=%q, want codex", decision.Harness)
	}
	if decision.Model != "gpt-5.4" {
		t.Fatalf("Model=%q, want gpt-5.4", decision.Model)
	}
	cfg, ok := svc.registry.Get("codex")
	if !ok {
		t.Fatal("missing codex registry entry")
	}
	if !slices.Contains(subprocessHarnessModelIDs("codex", cfg), decision.Model) {
		t.Fatalf("Model=%q must be in codex supported models %v", decision.Model, subprocessHarnessModelIDs("codex", cfg))
	}
	if decision.Model == "qwen3.5-27b" || !strings.HasPrefix(decision.Model, "gpt") {
		t.Fatalf("Model=%q must remain on a codex-supported GPT route", decision.Model)
	}
}

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

func TestExecuteHarnessPolicyRejectsUnsupportedEngineDecisionBeforeDispatch(t *testing.T) {
	svc := publicRouteTraceService(nil)
	err := svc.validateEngineResolvedExecuteDecision(
		ServiceExecuteRequest{Harness: "codex", Policy: "default", Prompt: "x"},
		&RouteDecision{Harness: "codex", Model: "qwen3.5-27b"},
	)
	if err == nil {
		t.Fatal("expected unsupported engine decision to fail before dispatch")
	}
	var typed *ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrHarnessModelIncompatible: %T %v", err, err)
	}
	if typed.Harness != "codex" || typed.Model != "qwen3.5-27b" {
		t.Fatalf("typed error=%#v, want codex/qwen3.5-27b", typed)
	}
}

func readFinalEvent(t *testing.T, ch <-chan ServiceEvent, timeout time.Duration) ServiceFinalData {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("Execute channel closed without final event")
			}
			if ev.Type != "final" {
				continue
			}
			var payload ServiceFinalData
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("unmarshal final event: %v", err)
			}
			return payload
		case <-deadline.C:
			t.Fatalf("timed out after %s waiting for final event", timeout)
			return ServiceFinalData{}
		}
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
