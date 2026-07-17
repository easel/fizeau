package builtin

import (
	"os"
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinNewClaudeHonorsTransport(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")

		h := New("claude")
		require.NotNil(t, h)

		runner, ok := h.(*claudeharness.Runner)
		require.True(t, ok, "New(\"claude\") must return *claude.Runner")
		assert.True(t, runner.NativeMode, "native transport must set NativeMode=true")
	})

	t.Run("default subprocess", func(t *testing.T) {
		orig, ok := os.LookupEnv("FIZEAU_CLAUDE_TRANSPORT")
		require.NoError(t, os.Unsetenv("FIZEAU_CLAUDE_TRANSPORT"))
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv("FIZEAU_CLAUDE_TRANSPORT", orig)
				return
			}
			_ = os.Unsetenv("FIZEAU_CLAUDE_TRANSPORT")
		})

		h := New("claude")
		require.NotNil(t, h)

		runner, ok := h.(*claudeharness.Runner)
		require.True(t, ok, "New(\"claude\") must return *claude.Runner")
		assert.False(t, runner.NativeMode, "unset transport must keep NativeMode=false")
	})
}

func TestNewRouteRunnerClonesConfiguredStructuralPrototype(t *testing.T) {
	tests := []struct {
		name      string
		prototype harnesses.Harness
		assert    func(t *testing.T, got harnesses.Harness)
	}{
		{
			name:      "gemini",
			prototype: &geminiharness.Runner{Binary: "/runtime/gemini", BaseArgs: []string{"--configured"}, PromptMode: "stdin", EventBuffer: 17},
			assert: func(t *testing.T, got harnesses.Harness) {
				runner := got.(*geminiharness.Runner)
				if runner.Binary != "/runtime/gemini" || !reflect.DeepEqual(runner.BaseArgs, []string{"--configured"}) || runner.PromptMode != "stdin" || runner.EventBuffer != 17 {
					t.Fatalf("Gemini clone lost configured launch state: %#v", runner)
				}
			},
		},
		{
			name:      "pi",
			prototype: &piharness.Runner{Binary: "/runtime/pi", BaseArgs: []string{"--configured"}, PromptMode: "stdin", EventBuffer: 19},
			assert: func(t *testing.T, got harnesses.Harness) {
				runner := got.(*piharness.Runner)
				if runner.Binary != "/runtime/pi" || !reflect.DeepEqual(runner.BaseArgs, []string{"--configured"}) || runner.PromptMode != "stdin" || runner.EventBuffer != 19 {
					t.Fatalf("Pi clone lost configured launch state: %#v", runner)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewRouteRunner(harnesses.RouteRunnerKey{Harness: tc.name, Endpoint: "east"}, tc.prototype)
			require.NoError(t, err)
			require.NotNil(t, got)
			if got == tc.prototype {
				t.Fatal("exact route runner aliases its structural prototype")
			}
			tc.assert(t, got)
		})
	}
}

func TestNewRouteRunnerKeepsClaudeConstructionTimeTransport(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	prototype := New("claude")
	if prototype.(*claudeharness.Runner).NativeMode {
		t.Fatal("subprocess prototype unexpectedly native")
	}

	// Transport identity is a process-lifetime property of the authority.
	// Changing the environment after structural construction must not flip it.
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
	t.Setenv(anthropicAPIKeyEnv, "")
	got, err := NewRouteRunner(harnesses.RouteRunnerKey{Harness: "claude", Endpoint: "east"}, prototype)
	require.NoError(t, err)
	if got.(*claudeharness.Runner).NativeMode {
		t.Fatal("exact runner reread transport environment after authority construction")
	}
}
