package routing

import (
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
)

// claudeHarnessWithAliases builds a claude subscription harness where
// AutoRoutingModels carries the bare aliases produced by
// subprocessHarnessAutoRoutingModels before the catalog-surface override
// replaces them. ModelEligibility is deliberately configured to resolve the
// "haiku" alias to ExactPinOnly=true so the gate fires correctly.
func claudeHarnessWithAliases() Inputs {
	return Inputs{
		Harnesses: []HarnessEntry{{
			Name:                "claude",
			Surface:             "claude",
			CostClass:           "medium",
			IsSubscription:      true,
			AutoRoutingEligible: true,
			ExactPinSupport:     true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			SupportsTools:       true,
			DefaultModel:        "sonnet-4.6",
			// AutoRoutingModels carries both the surface IDs (post-catalog-override
			// form) and the bare alias "haiku" to simulate the pre-override state
			// that triggers the alias gate miss.
			SupportedModels:   []string{"sonnet-4.6", "haiku-5.5", "haiku"},
			AutoRoutingModels: []string{"sonnet-4.6", "haiku"},
			Providers: []ProviderEntry{{
				Billing:       modelcatalog.BillingModelSubscription,
				CostSource:    CostSourceSubscription,
				SupportsTools: true,
				CostUSDPer1kTokensByModel: map[string]float64{
					"sonnet-4.6": 0.009,
					"haiku-5.5":  0.002,
					"haiku":      0.002,
				},
			}},
		}},
		// ModelEligibility resolves both the surface ID "haiku-5.5" and the bare
		// alias "haiku" to ExactPinOnly=true so the gate fires for either form.
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			switch model {
			case "sonnet-4.6", "claude-sonnet-4-6":
				return ModelEligibility{Power: 8, AutoRoutable: true}, true
			case "haiku-5.5", "claude-haiku-5.5", "haiku":
				return ModelEligibility{Power: 7, ExactPinOnly: true, AutoRoutable: false}, true
			default:
				return ModelEligibility{}, false
			}
		},
		Now: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

// TestHaikuLowTierNotAutoRoutedForDefaultPolicy is AC1: an unpinned
// policy=default request on the claude harness MUST NOT select the haiku/low
// tier even when "haiku" (the bare alias) appears in AutoRoutingModels. The
// middle-tier sonnet must win. This guards the exact_pin_only invariant from
// FEAT-004 phase-1: haiku is only reachable via an explicit model pin.
func TestHaikuLowTierNotAutoRoutedForDefaultPolicy(t *testing.T) {
	in := claudeHarnessWithAliases()

	dec, err := Resolve(Request{Policy: "default"}, in)
	if err != nil {
		t.Fatalf("Resolve policy=default: %v", err)
	}
	if dec.Harness != "claude" || dec.Model != "sonnet-4.6" {
		t.Errorf("policy=default: selected %s/%s, want claude/sonnet-4.6", dec.Harness, dec.Model)
	}

	// haiku must appear in the candidates list with eligible=false and
	// FilterReasonExactPinOnly so the routing_decision trace is observable.
	haikuFound := false
	for _, c := range dec.Candidates {
		if c.Harness != "claude" {
			continue
		}
		if c.Model != "haiku" && c.Model != "haiku-5.5" {
			continue
		}
		haikuFound = true
		if c.Eligible {
			t.Errorf("claude/%s Eligible=true; haiku must not be auto-routed (exact_pin_only)", c.Model)
		}
		if c.FilterReason != FilterReasonExactPinOnly {
			t.Errorf("claude/%s FilterReason=%q, want %q", c.Model, c.FilterReason, FilterReasonExactPinOnly)
		}
	}
	if !haikuFound {
		t.Errorf("claude/haiku row missing from routing_decision candidates; got %v",
			func() []string {
				var names []string
				for _, c := range dec.Candidates {
					names = append(names, c.Harness+"/"+c.Model)
				}
				return names
			}())
	}
}

// TestHaikuAliasEligibilityLookupReturnsExactPinOnly is AC2: the
// ModelEligibility lookup for the bare claude low-tier alias "haiku" must
// resolve ExactPinOnly=true. This is the gate that prevents haiku from winning
// automatic routing when a request carries no explicit power bounds.
func TestHaikuAliasEligibilityLookupReturnsExactPinOnly(t *testing.T) {
	in := claudeHarnessWithAliases()
	lookup := in.ModelEligibility

	cases := []struct {
		alias string
	}{
		{"haiku"},
		{"haiku-5.5"},
		{"claude-haiku-5.5"},
	}
	for _, tc := range cases {
		elig, ok := lookup(tc.alias)
		if !ok {
			t.Errorf("ModelEligibility(%q): ok=false; want ExactPinOnly=true (gate must fire)", tc.alias)
			continue
		}
		if !elig.ExactPinOnly {
			t.Errorf("ModelEligibility(%q).ExactPinOnly=false; want true", tc.alias)
		}
		if elig.AutoRoutable {
			t.Errorf("ModelEligibility(%q).AutoRoutable=true; want false (low tier is pin-only)", tc.alias)
		}
	}
}

// TestHaikuOnlyReachableViaExplicitModelPin asserts that pinning
// --model haiku (or --model haiku-5.5) is still honoured by the routing
// engine even though haiku is exact-pin-only. The explicit Model pin bypasses
// the auto-routing eligibility gate per CheckPowerEligibility contract.
func TestHaikuOnlyReachableViaExplicitModelPin(t *testing.T) {
	in := claudeHarnessWithAliases()

	for _, pin := range []string{"haiku", "haiku-5.5"} {
		dec, err := Resolve(Request{Model: pin}, in)
		if err != nil {
			t.Fatalf("Resolve --model %q: %v", pin, err)
		}
		if dec.Model != pin {
			t.Errorf("--model %q: selected model=%q, want %q", pin, dec.Model, pin)
		}
	}
}
