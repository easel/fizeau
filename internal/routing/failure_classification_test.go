package routing

import (
	"testing"
)

// TestClassifyOutcomeTable is the normative table-driven test for the FEAT-004
// failure-classification table. Every row of the spec table must appear here.
// classificationTable is the single source of truth; this test cross-checks it
// against the expected actions from the spec so no row can be silently dropped.
func TestClassifyOutcomeTable(t *testing.T) {
	rows := []struct {
		outcome AttemptOutcome
		want    OutcomeAction
		label   string // mirrors the spec table description
	}{
		{OutcomeSuccess, ActionCloseBead, "success → close bead"},
		{OutcomeAlreadySatisfied, ActionCloseBead, "already_satisfied → close bead"},
		{OutcomeNoChanges, ActionCloseOrUnclaim, "no_changes → close_or_unclaim (NOT escalate)"},
		{OutcomeGenuineFailure, ActionEscalateTier, "genuine failure → escalate middle→max once"},
		{OutcomeDirtyWorktree, ActionSameTierRetry, "dirty worktree → same-tier retry after clean"},
		{OutcomeTransportFailure, ActionSameTierReroute, "transport failure → same-tier reroute, no tier change"},
		{OutcomeQuotaExhausted, ActionGlobalPoolCooldown, "quota exhausted → global pool cooldown/sleep"},
		{OutcomeAuthConfigMissing, ActionOperatorAttention, "auth/config missing → operator-attention, no escalate"},
		{OutcomeTimeoutWatchdog, ActionSameTierRetry, "timeout watchdog → same-tier retry once, then operator"},
		{OutcomeMergeConflict, ActionUnclaimRetryLater, "merge conflict → unclaim + retry later, not escalation"},
		{OutcomeMaxTierGenuineFailure, ActionOperatorAttention, "max-tier genuine failure → operator-attention, stop escalating"},
	}

	// Verify the table covers every known outcome constant.
	allOutcomes := []AttemptOutcome{
		OutcomeSuccess,
		OutcomeAlreadySatisfied,
		OutcomeNoChanges,
		OutcomeGenuineFailure,
		OutcomeDirtyWorktree,
		OutcomeTransportFailure,
		OutcomeQuotaExhausted,
		OutcomeAuthConfigMissing,
		OutcomeTimeoutWatchdog,
		OutcomeMergeConflict,
		OutcomeMaxTierGenuineFailure,
	}
	if len(rows) != len(allOutcomes) {
		t.Errorf("test table has %d rows but there are %d outcome constants; add missing rows", len(rows), len(allOutcomes))
	}

	for _, tt := range rows {
		t.Run(tt.label, func(t *testing.T) {
			got := ClassifyOutcome(tt.outcome)
			if got != tt.want {
				t.Fatalf("ClassifyOutcome(%q) = %q, want %q", tt.outcome, got, tt.want)
			}
		})
	}
}

// TestClassifyOutcomeUnknownDefaultsToOperatorAttention verifies that an
// unrecognized outcome never silently triggers a retry or tier escalation —
// it always surfaces for operator review.
func TestClassifyOutcomeUnknownDefaultsToOperatorAttention(t *testing.T) {
	got := ClassifyOutcome("some_future_outcome_not_in_table")
	if got != ActionOperatorAttention {
		t.Fatalf("unknown outcome should default to %q, got %q", ActionOperatorAttention, got)
	}
}

// TestGenuineFailureVsNoChangesDistinction is the normative test for the most
// important classification boundary: a genuine implementation failure triggers
// tier escalation while no_changes does NOT. Both look like "the agent didn't
// succeed" but only one should ever advance the tier.
func TestGenuineFailureVsNoChangesDistinction(t *testing.T) {
	genuineAction := ClassifyOutcome(OutcomeGenuineFailure)
	if genuineAction != ActionEscalateTier {
		t.Fatalf("OutcomeGenuineFailure must map to %q (tier escalation), got %q", ActionEscalateTier, genuineAction)
	}

	noChangesAction := ClassifyOutcome(OutcomeNoChanges)
	if noChangesAction == ActionEscalateTier {
		t.Fatalf("OutcomeNoChanges must NOT map to %q; a no_changes result is not a capability failure and must never trigger tier escalation; got %q", ActionEscalateTier, noChangesAction)
	}
	if noChangesAction != ActionCloseOrUnclaim {
		t.Fatalf("OutcomeNoChanges must map to %q, got %q", ActionCloseOrUnclaim, noChangesAction)
	}
}

// TestNoOutcomeEscalatesExceptGenuineFailure asserts the non-owner invariant:
// of all outcome classes, only OutcomeGenuineFailure may produce
// ActionEscalateTier. Every other outcome must select a non-escalating action.
func TestNoOutcomeEscalatesExceptGenuineFailure(t *testing.T) {
	nonEscalating := []AttemptOutcome{
		OutcomeSuccess,
		OutcomeAlreadySatisfied,
		OutcomeNoChanges,
		OutcomeDirtyWorktree,
		OutcomeTransportFailure,
		OutcomeQuotaExhausted,
		OutcomeAuthConfigMissing,
		OutcomeTimeoutWatchdog,
		OutcomeMergeConflict,
		OutcomeMaxTierGenuineFailure,
	}
	for _, outcome := range nonEscalating {
		t.Run(string(outcome), func(t *testing.T) {
			action := ClassifyOutcome(outcome)
			if action == ActionEscalateTier {
				t.Fatalf("outcome %q must not produce ActionEscalateTier; only OutcomeGenuineFailure escalates the tier", outcome)
			}
		})
	}
}
