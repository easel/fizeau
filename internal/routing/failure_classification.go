package routing

// AttemptOutcome is the typed result of one bead execution attempt. Both the
// fizeau engine and ddx orchestrator classify outcomes using this vocabulary
// so escalation decisions are identical regardless of which layer observes
// the result.
//
// Normative reference: FEAT-004 Addendum §"Failure-classification table".
type AttemptOutcome string

const (
	// OutcomeSuccess: agent committed work that satisfies the bead ACs.
	OutcomeSuccess AttemptOutcome = "success"
	// OutcomeAlreadySatisfied: work was already done; no commit needed.
	OutcomeAlreadySatisfied AttemptOutcome = "already_satisfied"
	// OutcomeNoChanges: agent produced no diff and left a no_changes_rationale.
	// Do NOT escalate tier — a weak model that correctly reports nothing to do
	// is not a capability failure.
	OutcomeNoChanges AttemptOutcome = "no_changes"
	// OutcomeGenuineFailure: tests failed after real implementation work, or
	// acceptance criteria are unmet despite a best-effort attempt. This is the
	// only outcome that triggers middle→max tier escalation.
	OutcomeGenuineFailure AttemptOutcome = "genuine_failure"
	// OutcomeDirtyWorktree: the worktree was left with uncommitted partial
	// writes. Requires clean/reset before the next attempt.
	OutcomeDirtyWorktree AttemptOutcome = "dirty_worktree"
	// OutcomeTransportFailure: provider i/o timeout, connection refused, or 5xx.
	// Reroute to another dispatchable candidate at the same tier; do not change tier.
	OutcomeTransportFailure AttemptOutcome = "transport_failure"
	// OutcomeQuotaExhausted: provider returned retry_after indicating quota reset.
	// Global pool cooldown/sleep until reset; not a per-bead mutation, not escalation.
	OutcomeQuotaExhausted AttemptOutcome = "quota_exhausted"
	// OutcomeAuthConfigMissing: API key absent, toolchain broken, or environment
	// setup failure. Requires operator attention; do not escalate.
	OutcomeAuthConfigMissing AttemptOutcome = "auth_config_missing"
	// OutcomeTimeoutWatchdog: hard-kill or no-progress watchdog fired before
	// the agent committed. Clean worktree and retry same-tier once; after one
	// retry, escalate to operator attention.
	OutcomeTimeoutWatchdog AttemptOutcome = "timeout_watchdog"
	// OutcomeMergeConflict: the bead could not land cleanly due to a conflicting
	// concurrent change. Unclaim and retry later; not escalation.
	OutcomeMergeConflict AttemptOutcome = "merge_conflict"
	// OutcomeMaxTierGenuineFailure: genuine implementation failure on the max
	// (opus/gpt-5.5) tier. Stop escalating and surface for operator attention.
	OutcomeMaxTierGenuineFailure AttemptOutcome = "max_tier_genuine_failure"
)

// OutcomeAction is the action the orchestrator must take after classifying an
// attempt outcome. Actions are mutually exclusive per row of the normative
// failure-classification table.
type OutcomeAction string

const (
	// ActionCloseBead: mark the bead done. Used for success and already_satisfied.
	ActionCloseBead OutcomeAction = "close_bead"
	// ActionCloseOrUnclaim: honor the no_changes rationale (close if rationale
	// says already_satisfied, unclaim if retryable). Never escalates tier.
	ActionCloseOrUnclaim OutcomeAction = "close_or_unclaim"
	// ActionEscalateTier: fire one middle→max hop. Only triggered by
	// OutcomeGenuineFailure; all other outcomes must not produce this action.
	ActionEscalateTier OutcomeAction = "escalate_tier"
	// ActionSameTierRetry: clean/reset worktree and retry at the same tier.
	// Used for dirty_worktree and (first) timeout_watchdog.
	ActionSameTierRetry OutcomeAction = "same_tier_retry"
	// ActionSameTierReroute: reroute to another dispatchable candidate at the
	// same tier without tier change. Used for provider transport failures.
	ActionSameTierReroute OutcomeAction = "same_tier_reroute"
	// ActionGlobalPoolCooldown: sleep the entire pool until the quota reset
	// signaled by retry_after. Not a per-bead mutation, not escalation.
	ActionGlobalPoolCooldown OutcomeAction = "global_pool_cooldown"
	// ActionOperatorAttention: transition the bead to an explicit operator-
	// attention state. Used for auth/config failures, max-tier genuine failure,
	// and timeout after the same-tier retry budget is exhausted.
	ActionOperatorAttention OutcomeAction = "operator_attention"
	// ActionUnclaimRetryLater: release the bead claim and return it to the
	// queue for a future attempt. Used for merge/land conflicts.
	ActionUnclaimRetryLater OutcomeAction = "unclaim_retry_later"
)

// classificationTable is the normative mapping from FEAT-004 Addendum
// §"Failure-classification table". Every row must appear here; ddx and fizeau
// classify identically against this single source of truth.
//
// Normative table (reproduced for reference):
//
//	success / already_satisfied         → close_bead
//	no_changes (rationale present)      → close_or_unclaim (NOT escalate)
//	genuine failure (tests fail / AC)   → escalate_tier (middle→max, once)
//	dirty worktree / partial write      → same_tier_retry (clean first)
//	transport failure (i/o / 5xx)       → same_tier_reroute (no tier change)
//	quota exhausted (retry_after)       → global_pool_cooldown (not per-bead)
//	auth/config missing / setup failure → operator_attention (no escalate)
//	timeout / no-progress watchdog      → same_tier_retry (once, then operator)
//	merge/land conflict                 → unclaim_retry_later (not escalate)
//	max-tier genuine failure            → operator_attention (stop escalating)
var classificationTable = map[AttemptOutcome]OutcomeAction{
	OutcomeSuccess:               ActionCloseBead,
	OutcomeAlreadySatisfied:      ActionCloseBead,
	OutcomeNoChanges:             ActionCloseOrUnclaim,
	OutcomeGenuineFailure:        ActionEscalateTier,
	OutcomeDirtyWorktree:         ActionSameTierRetry,
	OutcomeTransportFailure:      ActionSameTierReroute,
	OutcomeQuotaExhausted:        ActionGlobalPoolCooldown,
	OutcomeAuthConfigMissing:     ActionOperatorAttention,
	OutcomeTimeoutWatchdog:       ActionSameTierRetry,
	OutcomeMergeConflict:         ActionUnclaimRetryLater,
	OutcomeMaxTierGenuineFailure: ActionOperatorAttention,
}

// ClassifyOutcome maps an attempt outcome to the action the orchestrator must
// take. The mapping is deterministic and shared between the fizeau engine and
// the ddx orchestrator so both layers produce identical escalation decisions.
//
// Unknown outcomes default to ActionOperatorAttention so novel failure modes
// surface for human review rather than triggering silent retries.
func ClassifyOutcome(outcome AttemptOutcome) OutcomeAction {
	if action, ok := classificationTable[outcome]; ok {
		return action
	}
	return ActionOperatorAttention
}
