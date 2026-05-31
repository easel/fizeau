package modelcatalog

import "testing"

// TestSubscriptionTierAutoRoutability enforces the FEAT-004 tier policy: the low
// tier (haiku/nano) is pin-only and never auto-routed, while the middle (sonnet,
// gpt-5.4-mini) and max (opus, gpt-5.5) tiers remain auto-routable. Auto-routing
// therefore starts at the middle tier by default.
func TestSubscriptionTierAutoRoutability(t *testing.T) {
	cat, err := Default()
	if err != nil {
		t.Fatalf("Default catalog: %v", err)
	}
	want := map[string]bool{
		"claude-haiku-5.5": false, // low tier: pin-only
		"sonnet-4.6":       true,  // middle
		"gpt-5.4-mini":     true,  // middle
		"claude-opus-4.7":  true,  // max
		"gpt-5.5":          true,  // max
	}
	for id, exp := range want {
		e, ok := cat.LookupModel(id)
		if !ok {
			t.Errorf("%s: not found in catalog", id)
			continue
		}
		if got := e.AutoRoutable(); got != exp {
			t.Errorf("%s: AutoRoutable()=%v want %v (power=%d exactPinOnly=%v)", id, got, exp, e.Power, e.ExactPinOnly)
		}
	}
}
