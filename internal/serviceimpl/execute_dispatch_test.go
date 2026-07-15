package serviceimpl

import (
	"context"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
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
			DispatchExecuteRun(context.Background(), ExecuteDispatchRequest{
				Decision: ExecuteRunnerDecision{Harness: tc.harness},
				Started:  time.Now(),
			}, ExecuteDispatchCallbacks{
				RunNative: func(context.Context) {
					native = true
				},
				RunSubprocess: func(_ context.Context, runner harnesses.Harness) {
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
