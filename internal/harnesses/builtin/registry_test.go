package builtin

import (
	"os"
	"testing"

	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
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
