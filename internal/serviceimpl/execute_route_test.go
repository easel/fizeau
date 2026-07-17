package serviceimpl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
)

func TestResolveExecuteRouteProjectsExplicitDecision(t *testing.T) {
	cat := loadRoutingInputsTestCatalog(t)
	input := executeRouteTestInput(cat)
	input.Providers = map[string]ProviderEntry{
		"local": {
			Type:           "lmstudio",
			BaseURL:        "http://default.invalid/v1",
			ServerInstance: "default-instance",
			Endpoints: []ProviderEndpoint{
				{Name: "west", BaseURL: "http://west.invalid/v1", ServerInstance: "west-instance"},
			},
		},
	}
	input.ProviderNames = []string{"local"}
	input.HasServiceConfig = true
	input.Request = ExecuteRouteRequest{
		Harness:  "local",
		Provider: "local@west",
		Model:    "priced-model",
	}

	got, failure := ResolveExecuteRoute(context.Background(), input)
	if failure != nil {
		t.Fatalf("ResolveExecuteRoute failure: %v", failure)
	}
	if got.Harness != "fiz" {
		t.Fatalf("Harness = %q, want canonical fiz", got.Harness)
	}
	if got.Provider != "local@west" || got.Endpoint != "west" {
		t.Fatalf("provider projection = %q endpoint=%q, want local@west/west", got.Provider, got.Endpoint)
	}
	if got.ServerInstance != "west-instance" {
		t.Fatalf("ServerInstance = %q, want west-instance", got.ServerInstance)
	}
	if got.Model != "priced-model" || got.Power != 8 {
		t.Fatalf("model projection = %q power=%d, want priced-model/8", got.Model, got.Power)
	}
	if got.Reason != "explicit" {
		t.Fatalf("Reason = %q, want explicit", got.Reason)
	}
}

func TestResolveExecuteRoutePreservesExplicitValidationFailures(t *testing.T) {
	cat := loadRoutingInputsTestCatalog(t)
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	base := executeRouteTestInput(cat)
	base.Now = func() time.Time { return now }
	base.Providers = map[string]ProviderEntry{"local": {Type: "lmstudio"}}
	base.ProviderNames = []string{"local"}
	base.HasServiceConfig = true

	tests := []struct {
		name       string
		request    ExecuteRouteRequest
		mutate     func(*ExecuteRouteInput)
		kind       ExecuteRouteFailureKind
		message    string
		assertMore func(*testing.T, *ExecuteRouteFailure)
	}{
		{
			name:    "unknown harness",
			request: ExecuteRouteRequest{Harness: "missing"},
			kind:    ExecuteRouteFailureUnknownHarness,
			message: `unknown harness "missing"`,
		},
		{
			name:    "policy conflicts with subscription harness",
			request: ExecuteRouteRequest{Harness: "codex", Model: "gpt-5.4", Policy: "air-gapped"},
			kind:    ExecuteRouteFailurePolicyRequirement,
			message: `policy "air-gapped" requires local-only but conflicts with Harness=codex`,
			assertMore: func(t *testing.T, failure *ExecuteRouteFailure) {
				t.Helper()
				if failure.Requirement != "local-only" || failure.AttemptedPin != "Harness=codex" {
					t.Fatalf("policy projection = %#v", failure)
				}
			},
		},
		{
			name:    "unknown provider",
			request: ExecuteRouteRequest{Harness: "codex", Provider: "missing@west", Model: "gpt-5.4"},
			kind:    ExecuteRouteFailureUnknownProvider,
			message: `unknown provider "missing@west"; known providers: local`,
			assertMore: func(t *testing.T, failure *ExecuteRouteFailure) {
				t.Helper()
				if failure.Provider != "missing@west" || !slices.Equal(failure.KnownProviders, []string{"local"}) {
					t.Fatalf("provider projection = %#v", failure)
				}
			},
		},
		{
			name:    "unsupported model",
			request: ExecuteRouteRequest{Harness: "codex", Model: "qwen3.5-27b"},
			kind:    ExecuteRouteFailureHarnessModelIncompatible,
			message: `model "qwen3.5-27b" is not supported by harness "codex"; supported models: gpt-5.4, gpt`,
			assertMore: func(t *testing.T, failure *ExecuteRouteFailure) {
				t.Helper()
				if failure.Harness != "codex" || failure.Model != "qwen3.5-27b" ||
					!slices.Equal(failure.SupportedModels, []string{"gpt-5.4", "gpt"}) {
					t.Fatalf("model projection = %#v", failure)
				}
			},
		},
		{
			name:    "unsupported reasoning",
			request: ExecuteRouteRequest{Harness: "codex", Model: "gpt-5.4", Reasoning: "extreme"},
			kind:    ExecuteRouteFailureUnsupportedReasoning,
			message: `unsupported reasoning "extreme" for harness "codex": reasoning: unsupported value "extreme"`,
			assertMore: func(t *testing.T, failure *ExecuteRouteFailure) {
				t.Helper()
				if failure.Cause == nil || failure.Reasoning != "extreme" {
					t.Fatalf("reasoning projection = %#v", failure)
				}
			},
		},
		{
			name:    "unsupported named reasoning",
			request: ExecuteRouteRequest{Harness: "claude", Model: "sonnet-4.6", Reasoning: "minimal"},
			kind:    ExecuteRouteFailureUnsupportedReasoning,
			message: `unsupported reasoning "minimal" for harness "claude"; supported reasoning: low, medium, high, xhigh, max`,
			assertMore: func(t *testing.T, failure *ExecuteRouteFailure) {
				t.Helper()
				if failure.Cause != nil || failure.Reasoning != "minimal" || failure.Harness != "claude" {
					t.Fatalf("reasoning projection = %#v", failure)
				}
			},
		},
		{
			name:    "fresh exhausted subscription quota",
			request: ExecuteRouteRequest{Harness: "codex", Model: "gpt-5.4"},
			mutate: func(input *ExecuteRouteInput) {
				input.QuotaForHarness = func(name string, gotNow time.Time) (ExecuteRouteQuota, bool) {
					if name != "codex" || !gotNow.Equal(now) {
						t.Fatalf("quota lookup = %q at %s", name, gotNow)
					}
					return ExecuteRouteQuota{
						Present: true,
						Fresh:   true,
						Windows: []harnesses.QuotaWindow{
							{ResetsAtUnix: now.Add(-time.Minute).Unix()},
							{ResetsAtUnix: now.Add(2 * time.Hour).Unix()},
							{ResetsAtUnix: now.Add(time.Hour).Unix()},
						},
					}, true
				}
			},
			kind: ExecuteRouteFailureQuotaUnavailable,
			message: fmt.Sprintf("no viable provider right now: codex quota-exhausted (retry after %s)",
				time.Unix(now.Add(time.Hour).Unix(), 0).Format(time.RFC3339)),
			assertMore: func(t *testing.T, failure *ExecuteRouteFailure) {
				t.Helper()
				if !failure.RetryAfter.Equal(now.Add(time.Hour)) || !slices.Equal(failure.ExhaustedProviders, []string{"codex"}) {
					t.Fatalf("quota projection = %#v", failure)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			input.Request = tc.request
			if tc.mutate != nil {
				tc.mutate(&input)
			}
			_, failure := ResolveExecuteRoute(context.Background(), input)
			if failure == nil {
				t.Fatal("ResolveExecuteRoute succeeded, want failure")
			}
			if failure.Kind != tc.kind {
				t.Fatalf("Kind = %q, want %q (%#v)", failure.Kind, tc.kind, failure)
			}
			if failure.Error() != tc.message {
				t.Fatalf("Error = %q, want %q", failure.Error(), tc.message)
			}
			if tc.assertMore != nil {
				tc.assertMore(t, failure)
			}
		})
	}
}

func TestResolveExecuteRouteNormalizesSubprocessAliases(t *testing.T) {
	input := executeRouteTestInput(loadRoutingInputsTestCatalog(t))
	input.DiscoverModels = func(harness string, _ harnesses.HarnessConfig) []string {
		if harness == "claude" {
			return []string{"opus-4.7"}
		}
		return nil
	}
	input.ResolveModelAlias = func(harness, model string) string {
		switch {
		case harness == "claude":
			return ClaudeCLIExecutableModel(model)
		case harness == "codex" && model == "gpt":
			return "gpt-5.5"
		case harness == "gemini" && model == "gemini":
			return "gemini-2.5-pro"
		default:
			return model
		}
	}

	tests := []struct {
		harness string
		model   string
		want    string
	}{
		{harness: "claude", model: "sonnet", want: "sonnet"},
		{harness: "claude", model: "opus-4.7", want: "opus"},
		{harness: "claude", model: "claude-opus-4-6", want: "opus"},
		{harness: "codex", model: "gpt", want: "gpt-5.5"},
		{harness: "gemini", model: "gemini", want: "gemini-2.5-pro"},
	}

	for _, tc := range tests {
		t.Run(tc.harness+"/"+tc.model, func(t *testing.T) {
			input.Request = ExecuteRouteRequest{Harness: tc.harness, Model: tc.model}
			got, failure := ResolveExecuteRoute(context.Background(), input)
			if failure != nil {
				t.Fatalf("ResolveExecuteRoute failure: %v", failure)
			}
			if got.Model != tc.want {
				t.Fatalf("Model = %q, want %q", got.Model, tc.want)
			}
		})
	}
}

func TestResolveExecuteRouteAcceptsClaudeFamilyWithoutDiscovery(t *testing.T) {
	input := executeRouteTestInput(loadRoutingInputsTestCatalog(t))
	discoveryCalls := 0
	input.DiscoverModels = func(string, harnesses.HarnessConfig) []string {
		discoveryCalls++
		return nil
	}

	tests := []struct {
		harness string
		model   string
		want    string
	}{
		{harness: "claude", model: "sonnet-4.6", want: "sonnet"},
		{harness: "claude", model: "opus", want: "opus"},
		{harness: "claude", model: "haiku-5.5", want: "haiku"},
		{harness: "claude", model: "fable", want: "fable"},
		{harness: "claude-tui", model: "sonnet-4.6", want: "sonnet-4.6"},
		{harness: "claude-tui", model: "opus", want: "opus"},
		{harness: "claude-tui", model: "haiku-5.5", want: "haiku-5.5"},
		{harness: "claude-tui", model: "fable", want: "fable"},
	}

	for _, tc := range tests {
		t.Run(tc.harness+"/"+tc.model, func(t *testing.T) {
			input.Request = ExecuteRouteRequest{Harness: tc.harness, Model: tc.model}
			got, failure := ResolveExecuteRoute(context.Background(), input)
			if failure != nil {
				t.Fatalf("ResolveExecuteRoute failure without discovered models: %v", failure)
			}
			if got.Model != tc.want {
				t.Fatalf("Model = %q, want %q", got.Model, tc.want)
			}
		})
	}

	for _, harness := range []string{"claude", "claude-tui"} {
		t.Run(harness+"/rejects-gpt", func(t *testing.T) {
			input.Request = ExecuteRouteRequest{Harness: harness, Model: "gpt-5.4"}
			_, failure := ResolveExecuteRoute(context.Background(), input)
			if failure == nil || failure.Kind != ExecuteRouteFailureHarnessModelIncompatible {
				t.Fatalf("failure = %#v, want Claude-family validation to reject GPT", failure)
			}
		})
	}
	if discoveryCalls != len(tests)+2 {
		t.Fatalf("discovery calls = %d, want %d validation reads returning no models", discoveryCalls, len(tests)+2)
	}
}

func TestResolveExecuteRouteAcceptsDiscoveredClaudeTUIFable5(t *testing.T) {
	base := executeRouteTestInput(loadRoutingInputsTestCatalog(t))
	base.DiscoverModels = func(name string, _ harnesses.HarnessConfig) []string {
		if name == "claude-tui" {
			return []string{"fable-5", "fable"}
		}
		return nil
	}

	for _, model := range []string{"fable-5", "claude-fable-5"} {
		t.Run(model, func(t *testing.T) {
			input := base
			input.Request = ExecuteRouteRequest{Harness: "claude-tui", Model: model}
			got, failure := ResolveExecuteRoute(context.Background(), input)
			if failure != nil {
				t.Fatalf("ResolveExecuteRoute(%q) failure: %v", model, failure)
			}
			if got.Model != model {
				t.Fatalf("Model = %q, want exact discovered pin %q", got.Model, model)
			}
		})
	}

	base.Request = ExecuteRouteRequest{Harness: "claude-tui", Model: "gpt-5.6-terra"}
	if _, failure := ResolveExecuteRoute(context.Background(), base); failure == nil || failure.Kind != ExecuteRouteFailureHarnessModelIncompatible {
		t.Fatalf("failure = %#v, want incompatible GPT pin", failure)
	}
}

func TestResolveExecuteRouteEmptyModelUsesEngineAndValidatesDecision(t *testing.T) {
	cat := loadRoutingInputsTestCatalog(t)

	t.Run("unpinned request delegates directly", func(t *testing.T) {
		input := executeRouteTestInput(cat)
		calls := 0
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			calls++
			return ExecuteRouteDecision{Harness: "fiz", Provider: "local", Model: "priced-model", Power: 8}, nil
		}
		got, failure := ResolveExecuteRoute(context.Background(), input)
		if failure != nil {
			t.Fatalf("ResolveExecuteRoute failure: %v", failure)
		}
		if calls != 1 || got.Harness != "fiz" || got.Model != "priced-model" {
			t.Fatalf("engine calls=%d decision=%#v", calls, got)
		}
	})

	t.Run("pinned eligible harness normalizes engine result", func(t *testing.T) {
		input := executeRouteTestInput(cat)
		input.Request = ExecuteRouteRequest{Harness: "codex", Policy: "default"}
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			return ExecuteRouteDecision{Harness: "codex", Provider: "local@west", Model: "gpt-5.4"}, nil
		}
		got, failure := ResolveExecuteRoute(context.Background(), input)
		if failure != nil {
			t.Fatalf("ResolveExecuteRoute failure: %v", failure)
		}
		if got.Harness != "codex" || got.Model != "gpt-5.4" || got.Endpoint != "west" {
			t.Fatalf("normalized engine decision = %#v", got)
		}
	})

	t.Run("pinned harness rejects a different engine harness", func(t *testing.T) {
		input := executeRouteTestInput(cat)
		input.Request = ExecuteRouteRequest{Harness: "codex", Policy: "default"}
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			return ExecuteRouteDecision{Harness: "claude", Model: "sonnet"}, nil
		}
		_, failure := ResolveExecuteRoute(context.Background(), input)
		if failure == nil || failure.Kind != ExecuteRouteFailureUnsatisfiablePin {
			t.Fatalf("failure = %#v, want unsatisfiable pin", failure)
		}
		if failure.Pin != "harness=codex" || failure.Reason != "routing engine returned a different harness" {
			t.Fatalf("pin projection = %#v", failure)
		}
	})

	t.Run("pinned harness rejects unsupported engine model", func(t *testing.T) {
		input := executeRouteTestInput(cat)
		input.Request = ExecuteRouteRequest{Harness: "codex", Policy: "default"}
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			return ExecuteRouteDecision{Harness: "codex", Model: "qwen3.5-27b"}, nil
		}
		_, failure := ResolveExecuteRoute(context.Background(), input)
		if failure == nil || failure.Kind != ExecuteRouteFailureHarnessModelIncompatible {
			t.Fatalf("failure = %#v, want model incompatible", failure)
		}
	})

	t.Run("under-specified pinned harness does not call engine", func(t *testing.T) {
		input := executeRouteTestInput(cat)
		input.Request = ExecuteRouteRequest{Harness: "codex"}
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			t.Fatal("engine called for under-specified harness")
			return ExecuteRouteDecision{}, nil
		}
		_, failure := ResolveExecuteRoute(context.Background(), input)
		if failure == nil || failure.Kind != ExecuteRouteFailureUnderSpecified {
			t.Fatalf("failure = %#v, want under-specified", failure)
		}
	})

	t.Run("ineligible pinned harness fails before engine", func(t *testing.T) {
		input := executeRouteTestInput(cat)
		input.Request = ExecuteRouteRequest{Harness: "gemini", Policy: "cheap"}
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			t.Fatal("engine called for auto-routing-ineligible harness")
			return ExecuteRouteDecision{}, nil
		}
		_, failure := ResolveExecuteRoute(context.Background(), input)
		if failure == nil || failure.Kind != ExecuteRouteFailureAutoResolutionUnavailable {
			t.Fatalf("failure = %#v, want auto-resolution unavailable", failure)
		}
	})
}

func TestResolveExecuteRoutePreservesEngineErrorIdentity(t *testing.T) {
	sentinel := errors.New("route failed")

	t.Run("ordinary engine error keeps cause under historical wrapper", func(t *testing.T) {
		input := executeRouteTestInput(nil)
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			return ExecuteRouteDecision{}, sentinel
		}
		_, failure := ResolveExecuteRoute(context.Background(), input)
		if failure == nil || failure.Kind != ExecuteRouteFailureEngine {
			t.Fatalf("failure = %#v, want engine failure", failure)
		}
		if failure.EngineErrorPassthrough || failure.Error() != "ResolveRoute: route failed" || !errors.Is(failure, sentinel) {
			t.Fatalf("engine failure projection = %#v", failure)
		}
	})

	t.Run("explicit engine error is marked for direct passthrough", func(t *testing.T) {
		input := executeRouteTestInput(nil)
		input.ResolveWithEngine = func(context.Context) (ExecuteRouteDecision, error) {
			return ExecuteRouteDecision{}, sentinel
		}
		input.PreserveEngineError = func(err error) bool { return errors.Is(err, sentinel) }
		_, failure := ResolveExecuteRoute(context.Background(), input)
		if failure == nil || !failure.EngineErrorPassthrough || failure.Error() != sentinel.Error() || failure.Cause != sentinel {
			t.Fatalf("passthrough projection = %#v", failure)
		}
	})
}

func TestResolveExecuteRouteQuotaFallbackMatchesCurrentBehavior(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	input := executeRouteTestInput(nil)
	input.Request = ExecuteRouteRequest{Harness: "codex", Model: "gpt-5.4"}
	input.Now = func() time.Time { return now }
	input.QuotaRecoveryFallback = 17 * time.Minute
	input.QuotaForHarness = func(string, time.Time) (ExecuteRouteQuota, bool) {
		return ExecuteRouteQuota{Present: true, Fresh: true}, true
	}

	_, failure := ResolveExecuteRoute(context.Background(), input)
	if failure == nil || failure.Kind != ExecuteRouteFailureQuotaUnavailable {
		t.Fatalf("failure = %#v, want quota unavailable", failure)
	}
	if !failure.RetryAfter.Equal(now.Add(17 * time.Minute)) {
		t.Fatalf("RetryAfter = %s, want %s", failure.RetryAfter, now.Add(17*time.Minute))
	}
}

func executeRouteTestInput(cat *modelcatalog.Catalog) ExecuteRouteInput {
	return ExecuteRouteInput{
		Harnesses: harnesses.NewRegistry(),
		Catalog:   cat,
		DiscoverModels: func(string, harnesses.HarnessConfig) []string {
			return nil
		},
		ResolveModelAlias: func(harness, model string) string {
			return ResolveSubprocessModelAlias(harness, model)
		},
		QuotaForHarness: func(string, time.Time) (ExecuteRouteQuota, bool) {
			return ExecuteRouteQuota{OK: true, Present: true, Fresh: true}, true
		},
	}
}
