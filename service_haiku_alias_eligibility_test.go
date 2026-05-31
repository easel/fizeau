package fizeau

import (
	"testing"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routing"
)

// TestServiceModelEligibilityResolvesHaikuAlias verifies that
// serviceRoutingModelEligibility, given a claude subscription harness that
// lists the canonical model IDs ("claude-haiku-5.5", "haiku-5.5") in
// SupportedModels, returns a lookup closure that also resolves the bare alias
// "haiku" to ExactPinOnly=true.
//
// This is the service-layer counterpart to TestHaikuAliasEligibilityLookupReturnsExactPinOnly
// in internal/routing: it exercises the actual closure builder used in
// production rather than a hand-crafted ModelEligibility stub.
func TestServiceModelEligibilityResolvesHaikuAlias(t *testing.T) {
	cat, err := loadRoutingCatalog()
	if err != nil || cat == nil {
		t.Skip("catalog unavailable; cannot test alias resolution")
	}

	// Simulate the claude subscription harness entry that
	// buildRoutingInputsWithCatalog produces after the catalog-surface override:
	// SupportedModels contains both canonical and surface-ID forms.
	entries := []routing.HarnessEntry{{
		Name:            "claude",
		Surface:         "claude",
		CostClass:       "medium",
		IsSubscription:  true,
		Available:       true,
		SupportedModels: []string{"claude-haiku-5.5", "haiku-5.5", "sonnet-4.6"},
	}}

	lookup := serviceRoutingModelEligibility(entries, cat)
	if lookup == nil {
		t.Fatal("serviceRoutingModelEligibility returned nil; expected a populated closure")
	}

	// The primary surface IDs must be found and carry ExactPinOnly.
	for _, id := range []string{"claude-haiku-5.5", "haiku-5.5"} {
		elig, ok := lookup(id)
		if !ok {
			t.Errorf("lookup(%q): ok=false; want ExactPinOnly=true", id)
			continue
		}
		if !elig.ExactPinOnly {
			t.Errorf("lookup(%q).ExactPinOnly=false; want true (claude-haiku-5.5 is exact_pin_only in catalog)", id)
		}
	}

	// The bare alias "haiku" must ALSO resolve to ExactPinOnly=true so that
	// CheckPowerEligibility gates it when AutoRoutingModels emits the alias
	// before the catalog-surface override fires.
	haikuElig, ok := lookup("haiku")
	if !ok {
		t.Fatalf("lookup(\"haiku\"): ok=false; alias must be registered in eligibility map (exact_pin_only gate miss)")
	}
	if !haikuElig.ExactPinOnly {
		t.Errorf("lookup(\"haiku\").ExactPinOnly=false; want true so the exact_pin_only gate fires for bare alias")
	}
	if haikuElig.AutoRoutable {
		t.Errorf("lookup(\"haiku\").AutoRoutable=true; want false (low tier must not auto-route)")
	}

	// The sonnet alias must resolve as auto-routable (control: ensure the
	// alias mechanism does not incorrectly set ExactPinOnly on routable tiers).
	sonnetElig, ok := lookup("sonnet")
	if !ok {
		t.Errorf("lookup(\"sonnet\"): ok=false; sonnet alias should be registered")
	} else {
		if sonnetElig.ExactPinOnly {
			t.Errorf("lookup(\"sonnet\").ExactPinOnly=true; sonnet is auto-routable, not pin-only")
		}
		if !sonnetElig.AutoRoutable {
			t.Errorf("lookup(\"sonnet\").AutoRoutable=false; sonnet should be auto-routable")
		}
	}

	// Verify the service-layer routing uses BillingModel subscription correctly
	// so the claude harness entry type is classified as subscription.
	if billing := modelcatalog.BillingForHarness("claude"); billing != modelcatalog.BillingModelSubscription {
		t.Errorf("BillingForHarness(\"claude\")=%q, want %q", billing, modelcatalog.BillingModelSubscription)
	}
}
