package fizeau

import (
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/routing"
)

// TestRoutingSurfacePreferenceDefaultPrefersClaudeTUI proves the production
// wiring path (the value handed to routing.Inputs.SurfacePreference) defaults
// to the built-in preference that makes claude-tui win the shared "claude"
// surface. This is the operator-facing default that GAP-1 requires be live in
// production code, not just at the engine API level.
func TestRoutingSurfacePreferenceDefaultPrefersClaudeTUI(t *testing.T) {
	// Ensure the kill-switch is unset for this case.
	t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", "")

	got := routingSurfacePreference()
	want := routing.DefaultSurfacePreference()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routingSurfacePreference() = %v, want default %v", got, want)
	}
	if got["claude"] != "claude-tui" {
		t.Fatalf("routingSurfacePreference()[claude] = %q, want claude-tui", got["claude"])
	}
}

// TestRoutingSurfacePreferenceKillSwitchDisables proves the operator-facing
// revert mechanism: setting FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT to any truthy
// value yields an explicit EMPTY (non-nil) map, which the routing engine
// interprets as "preference disabled" (TestSurfacePreferenceDisabledFallsBackToAlphabetical
// in internal/routing proves the empty map falls back to the alphabetical
// tie-break, i.e. claude --print). A nil map would instead re-enable the
// default, so the non-nil-empty distinction is load-bearing and is asserted.
func TestRoutingSurfacePreferenceKillSwitchDisables(t *testing.T) {
	for _, truthy := range []string{"1", "true", "TRUE", "yes", "YES", "on", "On"} {
		t.Run(truthy, func(t *testing.T) {
			t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", truthy)

			got := routingSurfacePreference()
			if got == nil {
				t.Fatalf("routingSurfacePreference() = nil for kill-switch=%q; want explicit empty (non-nil) map so the engine disables the preference (nil re-enables the default)", truthy)
			}
			if len(got) != 0 {
				t.Fatalf("routingSurfacePreference() = %v for kill-switch=%q, want empty map", got, truthy)
			}
		})
	}
}

// TestRoutingSurfacePreferenceNonTruthyKeepsDefault proves the kill-switch is
// only honored for recognized truthy values; an unrecognized value (e.g. "0",
// "false", "maybe") leaves claude-tui as the default rather than silently
// disabling it.
func TestRoutingSurfacePreferenceNonTruthyKeepsDefault(t *testing.T) {
	for _, nonTruthy := range []string{"0", "false", "no", "off", "maybe", "claude"} {
		t.Run(nonTruthy, func(t *testing.T) {
			t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", nonTruthy)

			got := routingSurfacePreference()
			if got["claude"] != "claude-tui" {
				t.Fatalf("routingSurfacePreference()[claude] = %q for env=%q, want claude-tui (non-truthy must not disable the default)", got["claude"], nonTruthy)
			}
		})
	}
}
