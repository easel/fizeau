package fizeau_test

import (
	"testing"

	fizeau "github.com/easel/fizeau"
	"github.com/stretchr/testify/assert"
)

// TestAvailableHarnesses_AlwaysIncludesEmbedded verifies the public PATH
// detection helper at least reports the embedded fiz harness, which is
// available without any binary lookup. The exact CLI subset depends on
// what is installed in the test environment, so we only assert invariants.
func TestAvailableHarnesses_AlwaysIncludesEmbedded(t *testing.T) {
	got := fizeau.AvailableHarnesses()
	assert.NotEmpty(t, got)
	assert.Contains(t, got, "fiz", "embedded fiz must always be reported as available")
}

// TestDetectHarnesses_ReportsEveryBuiltin verifies DetectHarnesses returns
// one row per builtin harness with stable shape.
func TestDetectHarnesses_ReportsEveryBuiltin(t *testing.T) {
	got := fizeau.DetectHarnesses()
	assert.NotEmpty(t, got)

	byName := make(map[string]fizeau.HarnessAvailability, len(got))
	for _, h := range got {
		byName[h.Name] = h
	}

	// Every CLI the docs and onboarding flow promise to detect must have
	// a row in DetectHarnesses, even when not installed.
	for _, name := range []string{"claude", "codex", "grok", "opencode", "pi"} {
		row, ok := byName[name]
		assert.True(t, ok, "missing builtin: %s", name)
		assert.NotEmpty(t, row.Binary, "%s should have a binary name", name)
	}

	// Embedded harnesses are always available.
	if row, ok := byName["fiz"]; ok {
		assert.True(t, row.Available)
		assert.Equal(t, "(embedded)", row.Path)
	}
}
