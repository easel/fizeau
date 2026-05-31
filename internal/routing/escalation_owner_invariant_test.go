package routing

import (
	"reflect"
	"testing"
)

// ADR-017 makes ddx power-retry the single owner of middle→max tier escalation.
// The fizeau engine ladder (PolicyEscalationLadder / nextPolicyInLadder /
// EscalatePolicyAware) is a NON-OWNER: it only widens the policy band of the
// current request when that request is unpinned and has no dispatchable
// candidate at the requested policy (routing infeasibility). It must never bump
// tier in response to an attempt's semantic outcome, and never past an explicit
// caller pin.
//
// These tests enforce that non-owner prohibition (FEAT-004 acceptance: "assertion
// /test enforces non-owners don't change tier").

// TestEngineLadderIsRoutingInfeasibilityOnly pins the engine ladder to the
// documented cheap→default→smart routing-infeasibility progression. The point is
// structural: there is exactly one engine escalation ladder, and it is keyed to
// the current request's policy — not to any post-attempt failure classification.
func TestEngineLadderIsRoutingInfeasibilityOnly(t *testing.T) {
	want := []string{"cheap", "default", "smart"}
	if !reflect.DeepEqual(PolicyEscalationLadder, want) {
		t.Fatalf("PolicyEscalationLadder=%v, want the routing-infeasibility ladder %v", PolicyEscalationLadder, want)
	}
}

// TestEngineLadderDoesNotEscalateHardPinPastServableTier asserts the engine
// ladder never advances a hard-pinned request to a tier the pin cannot serve. A
// non-owner must never move a pinned request onto a higher-tier model the pin
// can't dispatch; only the orchestrator (ddx power-retry) may change tier, and
// only on a genuine-failure classification it alone can observe.
func TestEngineLadderDoesNotEscalateHardPinPastServableTier(t *testing.T) {
	in := newTestRoutingEngine()
	// Restrict the fiz/vidar-omlx provider to the cheap-tier qwen model only.
	// The `smart` policy resolves to claude-opus (surface=claude), which the
	// fiz+vidar-omlx pin cannot serve — so escalation must never reach it.
	for i, h := range in.Harnesses {
		if h.Name != "fiz" {
			continue
		}
		for j, p := range h.Providers {
			if p.Name == "vidar-omlx" {
				in.Harnesses[i].Providers[j].DiscoveredIDs = []string{"Qwen3.6-35B-A3B-4bit"}
			}
		}
	}

	// Hard pin to harness=fiz + provider=vidar-omlx at the bottom of the ladder.
	req := Request{Harness: "fiz", Provider: "vidar-omlx", Policy: "cheap"}
	if next := EscalatePolicyAware("cheap", PolicyEscalationLadder, req, in); next == "smart" {
		t.Fatalf("engine ladder escalated a hard-pinned request to the unservable %q tier; non-owner must not change tier past the pin", next)
	}
}
