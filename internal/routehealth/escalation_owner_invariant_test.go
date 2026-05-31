package routehealth

import (
	"testing"

	"github.com/easel/fizeau/internal/routing"
)

// ADR-017 makes ddx power-retry the single owner of middle→max tier escalation.
// The service `escalatePolicyLadder` adapter (which delegates to
// EscalatePolicyLadder) is a NON-OWNER: it may only widen the policy band of the
// current request when that request has no dispatchable candidate at the
// requested policy (routing infeasibility). It must never change tier in
// response to an attempt's semantic outcome (a genuine implementation/capability
// failure) or any explicit-constraint error class.
//
// These tests enforce that non-owner prohibition (FEAT-004 acceptance: "assertion
// /test enforces non-owners don't change tier").

// TestNonOwnerEscalationInertOnNonRoutingOutcomes asserts the service-level
// ladder makes NO policy/tier change for inputs that are not current-request
// routing infeasibility: a nil error, an empty policy, and each
// explicit-constraint error class. A genuine implementation/capability failure
// is never a routing error fizeau observes, so the closest fizeau analogs — the
// explicit-constraint classes the orchestrator's classifier outranks — must all
// leave tier unchanged.
func TestNonOwnerEscalationInertOnNonRoutingOutcomes(t *testing.T) {
	req := routing.Request{Policy: "default"}
	var in routing.Inputs

	cases := []struct {
		name string
		err  error
	}{
		{name: "nil error (no failure to react to)", err: nil},
		{
			name: "harness model incompatible (explicit pin)",
			err:  &routing.ErrHarnessModelIncompatible{Harness: "codex", Model: "gpt-5.5"},
		},
		{
			name: "unsatisfiable pin (explicit constraint)",
			err:  &routing.ErrUnsatisfiablePin{Pin: "provider=bragi", Reason: "unknown provider"},
		},
		{
			name: "policy requirement unsatisfied (explicit constraint)",
			err:  &routing.ErrPolicyRequirementUnsatisfied{Policy: "air-gapped", Requirement: "no_remote"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			escalated, dec, err := EscalatePolicyLadder(req, in, tc.err, req.Policy, ShouldEscalateOnError)
			if escalated {
				t.Fatalf("non-owner ladder escalated tier on %s; want inert", tc.name)
			}
			if dec != nil {
				t.Fatalf("non-owner ladder returned a decision on %s; want nil (no tier change)", tc.name)
			}
			if err != nil {
				t.Fatalf("non-owner ladder returned err %v on %s; want nil", err, tc.name)
			}
		})
	}
}

// TestNonOwnerEscalationGateRejectsExplicitConstraints asserts the escalation
// gate the non-owner uses refuses to escalate past explicit caller intent. This
// is the gate that confines the engine/service ladder to routing infeasibility
// and keeps it from ever doubling as the genuine-failure tier-escalation owner.
func TestNonOwnerEscalationGateRejectsExplicitConstraints(t *testing.T) {
	rejected := []error{
		&routing.ErrHarnessModelIncompatible{Harness: "codex", Model: "gpt-5.5"},
		&routing.ErrUnsatisfiablePin{Pin: "provider=bragi"},
		&routing.ErrPolicyRequirementUnsatisfied{Policy: "air-gapped", Requirement: "no_remote"},
	}
	for _, err := range rejected {
		if ShouldEscalateOnError(err) {
			t.Fatalf("ShouldEscalateOnError(%T)=true; non-owner must not escalate past explicit constraint", err)
		}
	}
}
