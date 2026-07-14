---
ddx:
  id: ADR-017
  depends_on:
    - ADR-005
    - ADR-011
  review:
    self_hash: f9ea7f0371a424b47ab91d7ebe91939427d32f664c89ca213a67374f3ad4ed0b
    deps:
      ADR-005: e47168fa6ebdb3a0f57d9a5e34cc638563f74fe5c529f73e0bee327259c7bec5
      ADR-011: 088af56c3f51ae0ba0bb0d71940195af827b2ec5b73768e11fd0d7427070f8d2
    reviewed_at: "2026-07-14T20:00:14Z"
---
# ADR-017: Single Owner for middle→max Tier Escalation

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-31 | Accepted | Fizeau maintainers | `FEAT-004` (Addendum: tiered, quota-aware routing), `ADR-005`, `ADR-011`, `fizeau-3617c4ee` | High |

## Context

The FEAT-004 addendum "tiered, quota-aware routing" defines three subscription
tiers per flat-rate harness — `low` (haiku / gpt-nano, power 7, pin-only),
`middle` (sonnet / gpt-5.4-mini, power 8, the default auto-route start), and
`max` (opus / gpt-5.5, power 10, the escalation target). It mandates:

> There is exactly ONE owner of middle→max escalation; the other layers (fizeau
> engine ladder, service `escalatePolicyLadder`, ddx power-retry) must not
> independently escalate tier. Escalation is a single hop, fired ONLY on a typed
> **genuine implementation/capability failure** classification.

Today three layers can each move a request toward a stronger model, and nothing
names which one is allowed to do so for the genuine-failure case:

1. **fizeau engine ladder** — `internal/routing/engine.go`:
   `PolicyEscalationLadder = ["cheap", "default", "smart"]`, consumed inside
   `Resolve` (via `nextPolicyInLadder` / `EscalatePolicyAware`). It widens the
   policy band of the **current** `ResolveRoute` call when an *unpinned* request
   has no dispatchable candidate at the requested policy.
2. **service `escalatePolicyLadder`** — `service_routing.go` →
   `routehealth.EscalatePolicyLadder`. The service-level adapter over the same
   routing-infeasibility ladder, gated by `shouldEscalateOnError` /
   `routehealth.ShouldEscalateOnError`.
3. **ddx power-retry** — the queue orchestrator that runs a bead, executes its
   tests, evaluates its acceptance criteria, classifies the attempt outcome
   against the FEAT-004 failure-classification table, and may re-dispatch.

If more than one of these reacts to the same failed attempt, a single genuine
failure can multi-hop middle→max (or stampede several workers onto the max tier)
instead of taking the mandated single, capped hop.

The decisive structural fact: a "genuine implementation/capability failure"
(*tests fail after real work; AC unmet*) is only observable **after** an attempt
completes and its tests/ACs are evaluated. Fizeau's router and service dispatch
exactly one selected candidate per request and return (FEAT-004 requirement 42;
"Constraints and Assumptions": *"Fizeau owns routing inside the embedded runtime;
callers own semantic retry and cross-harness orchestration strategy"*). Fizeau
never sees test results or AC verdicts, so it structurally cannot classify a
genuine implementation/capability failure. The only layer that can is the
orchestrator.

## Decision

### Owner: ddx power-retry

**ddx power-retry is the single owner of middle→max tier escalation.** It, and
only it, may move a bead from the middle tier to the max tier, and it does so:

- as a **single hop** (middle→max, never max→beyond, never multi-hop), fired
  **only** on a typed `genuine implementation/capability failure` classification
  from the FEAT-004 failure-classification table;
- subject to a **per-pool in-flight max-attempt cap** so concurrent workers
  cannot stampede onto the max tier;
- never on `no_changes`, dirty-worktree, transport, quota-exhausted, auth/config,
  timeout, or merge-conflict outcomes — those map to their own non-escalating
  actions in the table.

This places escalation at the only layer that owns the failure-classification
input, keeping the engine and service as pure single-shot routers.

### Prohibition: fizeau engine ladder (non-owner)

`routing.PolicyEscalationLadder`, `nextPolicyInLadder`, and `EscalatePolicyAware`
remain **routing-infeasibility only**. They MUST NOT:

- escalate, or be wired to escalate, **in response to an attempt's semantic
  outcome** (a genuine implementation/capability failure, or any other entry in
  the failure-classification table);
- be repurposed as the genuine-failure middle→max owner.

They MAY only widen the policy band of the **current** request when that request
is *unpinned* and has zero dispatchable candidates at the requested policy. That
is a dispatchability concern about one in-flight route, not a reaction to a prior
attempt's test/AC result. Hard-pinned requests do not escalate
(`isUnpinnedRequest` gate), and escalation never crosses an explicit caller
constraint (`ShouldEscalateOnError` rejects `ErrUnsatisfiablePin`,
`ErrPolicyRequirementUnsatisfied`, `ErrHarnessModelIncompatible`).

### Prohibition: service `escalatePolicyLadder` (non-owner)

`escalatePolicyLadder` / `routehealth.EscalatePolicyLadder` is the service
adapter over the engine's routing-infeasibility ladder and inherits the same
prohibitions. It MUST NOT change tier on a genuine implementation/capability
failure or any other post-attempt outcome classification, and MUST NOT be
invoked as the genuine-failure escalation owner. It stays inert (no policy/tier
change) for a `nil` error, an empty policy, and every explicit-constraint error
class; it acts only on current-request routing-infeasibility errors.

### Why not the engine or service

Both fizeau layers dispatch one candidate and return without seeing the bead's
test results or AC verdicts, so neither can produce the typed genuine-failure
classification that is the sole legal trigger. Letting either react to a failed
attempt would also duplicate the orchestrator's hop and defeat the per-pool cap,
re-introducing the multi-worker max-tier stampede the addendum forbids.

## Consequences

### Positive

- Exactly one layer reacts to a genuine failure; a single failure produces at
  most one capped middle→max hop.
- The engine and service stay single-shot routers, consistent with FEAT-004
  requirement 42 and the ADR-005 caller-owned-retry principle.
- The prohibition is machine-checked: see *Enforcement*.

### Negative

- ddx power-retry must implement and own the per-pool in-flight max-attempt cap;
  no fizeau-side ladder will provide a backstop hop.
- The engine/service routing-infeasibility ladder and the orchestrator's
  genuine-failure tier hop look superficially similar and must be kept
  conceptually distinct in future work (Phase 1 implements the actual hop).

## Enforcement

AC2 is enforced by assertion tests that pin the two fizeau non-owner layers as
inert on anything other than current-request routing infeasibility:

- `internal/routehealth/escalation_owner_invariant_test.go` — asserts
  `EscalatePolicyLadder` makes **no** policy/tier change (returns
  `escalated=false, decision=nil, err=nil`) for a `nil` error, an empty policy,
  and each explicit-constraint / non-routing outcome class, and that
  `ShouldEscalateOnError` refuses those classes. This proves the service
  non-owner cannot change tier on a genuine-failure-class outcome.
- `internal/routing/escalation_owner_invariant_test.go` — asserts the engine
  ladder is exactly the routing-infeasibility ladder `["cheap","default","smart"]`
  and that `EscalatePolicyAware` does **not** escalate a hard-pinned request,
  i.e. there is no engine path that bumps tier outside unpinned routing
  infeasibility.

## References

- `FEAT-004` — Addendum "tiered, quota-aware routing"; subscription tier table;
  failure-classification table; requirement 42 (one dispatched candidate per
  request); "callers own semantic retry."
- `ADR-005` — power-based automatic routing with caller-owned retry.
- `ADR-011` — cost-based routing with quota pools; caller-owned fallback chain on
  mid-request exhaustion.
- `fizeau-3617c4ee` — this decision (routing P0: single escalation owner).
