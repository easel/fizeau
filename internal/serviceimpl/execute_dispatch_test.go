package serviceimpl

import (
	"context"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
)

func TestDispatchExecuteRunSelectsExplicitHarnessRunner(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "")

	tests := []struct {
		name           string
		harness        string
		wantNative     bool
		wantSubprocess string
		wantVirtual    bool
		wantScript     bool
	}{
		{name: "native", harness: "fiz", wantNative: true},
		{name: "claude", harness: "claude", wantSubprocess: "claude"},
		{name: "claude tui", harness: "claude-tui", wantSubprocess: "claude-tui"},
		{name: "codex", harness: "codex", wantSubprocess: "codex"},
		{name: "gemini", harness: "gemini", wantSubprocess: "gemini"},
		{name: "opencode", harness: "opencode", wantSubprocess: "opencode"},
		{name: "pi", harness: "pi", wantSubprocess: "pi"},
		{name: "virtual", harness: "virtual", wantVirtual: true},
		{name: "script", harness: "script", wantScript: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var native, virtual, script bool
			var subprocess string
			var configured harnesses.Harness
			if tc.harness == "gemini" || tc.harness == "pi" {
				configured = builtin.New(tc.harness)
			}
			DispatchExecuteRun(context.Background(), ExecuteDispatchRequest{
				Decision:          ExecuteRunnerDecision{Harness: tc.harness},
				ConfiguredHarness: configured,
				Started:           time.Now(),
			}, ExecuteDispatchCallbacks{
				RunNative: func(context.Context) {
					native = true
				},
				RunSubprocess: func(_ context.Context, runner harnesses.Harness) {
					if configured != nil && runner != configured {
						t.Fatalf("dispatch runner = %p, want exact configured instance %p", runner, configured)
					}
					subprocess = runner.Info().Name
				},
				RunVirtual: func(context.Context) {
					virtual = true
				},
				RunScript: func(context.Context) {
					script = true
				},
				Finalize: func(final harnesses.FinalData) {
					t.Fatalf("unexpected dispatch failure: %#v", final)
				},
			})

			if native != tc.wantNative || subprocess != tc.wantSubprocess || virtual != tc.wantVirtual || script != tc.wantScript {
				t.Fatalf("dispatch = native:%v subprocess:%q virtual:%v script:%v, want native:%v subprocess:%q virtual:%v script:%v",
					native, subprocess, virtual, script,
					tc.wantNative, tc.wantSubprocess, tc.wantVirtual, tc.wantScript)
			}
		})
	}
}

func TestDispatchExecuteRunRejectsMissingConfiguredInventoryRunner(t *testing.T) {
	for _, name := range []string{"gemini", "pi"} {
		t.Run(name, func(t *testing.T) {
			var subprocess bool
			var final harnesses.FinalData
			DispatchExecuteRun(context.Background(), ExecuteDispatchRequest{
				Decision: ExecuteRunnerDecision{Harness: name, Model: "fixture-model"},
				Started:  time.Now(),
			}, ExecuteDispatchCallbacks{
				RunSubprocess: func(context.Context, harnesses.Harness) { subprocess = true },
				Finalize:      func(got harnesses.FinalData) { final = got },
			})
			if subprocess || final.Status != "failed" || final.RoutingActual == nil || final.RoutingActual.Harness != name {
				t.Fatalf("missing configured runner dispatch = subprocess:%v final:%#v", subprocess, final)
			}
		})
	}
}
