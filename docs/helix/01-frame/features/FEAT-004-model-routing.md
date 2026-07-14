---
ddx:
  id: FEAT-004
  depends_on:
    - helix.prd
    - FEAT-003
  review:
    self_hash: 9761114849a85ae13627ea086fdfb1d332edda875fd81cb3769096bedc7eaeae
    deps:
      FEAT-003: 8c4332150f3d5d591015e360231913d4e8f24f9b83f3678e65574e5f45f78e0d
      helix.prd: 12c9ecc92726e3d50896a8afb51224906edfea9863d8114d39a6c2a0a2e54003
    reviewed_at: "2026-07-14T20:00:14Z"
---
# Feature Specification: FEAT-004 — Shared Model Catalog and Policy Routing

**Feature ID**: FEAT-004
**Status**: Approved
**Priority**: P0 (provider sources/endpoints), P1 (catalog), P1 (routing)
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: Routing & Catalog
**Covered PRD Requirements**: FR-4
**Cross-Subsystem Rationale**: Joins provider facts, model policy, and request
constraints to choose one attributable route without taking over caller retry.
**User Stories**: [US-004 — Resolve an Explainable Route](../user-stories/US-004-routing-catalog.md)

## Overview

Fizeau routes requests by evaluating `route(client_inputs, fiz_models_snapshot)`.
Client inputs include policy/profile, pins, `no_remote`, metered opt-in, tools,
context, reasoning needs, and other explicit constraints. The `fiz models`
snapshot is the only source of routing facts: health, quota, model
availability, effective cost, `actual_cash_spend`, billing kind, context/tools/
reasoning support, locality, reliability, latency, utilization, and per-field
freshness.

The snapshot is assembled from configured provider sources and harnesses,
discovered model IDs, catalog metadata, and runtime signals joined into one set
of provider/model facts. Fizeau has no required daemon. Its freshness contract
is cache-first stale-while-revalidate on route hot paths plus explicit blocking
refresh surfaces, all sharing cross-process locks, single-flight coalescing,
TTLs, cooldowns, bounded concurrency, and atomic snapshot writes. A long-running
DDx server may call the same Fizeau refresh entrypoints to keep snapshot facts
warm, but route correctness must not depend on that process.

The public v0.11 routing surface is:

- `Policy`: one of `cheap`, `default`, `smart`, or `air-gapped`;
- `MinPower` and `MaxPower`: numeric soft power hints on the 1..10 catalog
  scale;
- hard pins: `Harness`, `Provider`, and exact `Model`.

Deprecated route tables, model reference aliases, compatibility targets, and
surface policy projections are not public routing controls.

This feature spec defines the required routing behavior and public contracts.
`SD-005` owns the implementation sequence, cache mechanics, candidate scoring
formula, and routing trace construction.

## Problem Statement

- Provider config should describe transport/auth, not route policy.
- The catalog should own model metadata, power, cost, deployment class,
  auto-routable status, and provider surface strings.
- Callers need a small, explainable routing vocabulary that avoids accidental
  paid spend but still allows exact pins and explicit escalation.

## Terminology

- **Policy**: a named bundle of power bounds, local allowance, and hard
  requirements.
- **Power**: catalog model-strength integer from 1 to 10. `0` means unknown or
  exact-pin-only.
- **Hard pin**: caller assertion on `Harness`, `Provider`, or exact `Model`.
- **Route candidate**: one `(harness, provider, endpoint, model)` option after
  live discovery and catalog join.
- **Default inclusion**: provider-level `include_by_default`, used only for
  unpinned automatic routing.
- **Metered opt-in**: operator permission for pay-per-token candidates to
  participate in unpinned automatic routing. Provider default inclusion is still
  required.
- **Effective cost**: normalized request-local scoring cost. Subscription
  candidates use PAYG-equivalent pricing for comparison even when dispatch does
  not create actual cash spend.
- **Actual cash spend**: whether dispatching the candidate creates incremental
  pay-per-token billing. This is separate from effective cost.
- **Unpinned request**: a request with no `Harness`, no `Provider`, and no exact
  `Model`. `Policy`, `MinPower`, `MaxPower`, `Reasoning`, capability flags, and
  token estimates do not make a request pinned.
- **Sticky affinity**: a score bonus for reusing a server instance for related
  requests when the candidate is still eligible.

## Requirements

### Catalog and Manifest

1. The manifest schema is v5.
2. Catalog models carry concrete model ID, provider surface strings, power,
   auto-routable/exact-pin-only status, deployment class, cost, context,
   benchmark provenance, capabilities, and reasoning defaults.
3. Catalog policies carry `min_power`, `max_power`, `allow_local`, and
   `require[]`.
4. Catalog providers carry provider `type`, `include_by_default`, and explicit
   billing only when the hardcoded provider-system table cannot infer billing.
5. Removed v0.10 routing concepts (`target`, aliases as routing personas, and
   user-visible `surface_policy`) must not be presented as public routing API.
   Narrow compatibility structs may exist only to keep older internal catalog
   readers working while the primary v5 shape is used.
6. Ordinary execution uses the embedded or configured local manifest. It does
   not fetch manifest updates over the network.

### Canonical Policies

7. The canonical policy set is exactly:

| Policy | MinPower | MaxPower | AllowLocal | Require | Intent |
|--------|----------|----------|------------|---------|--------|
| `cheap` | 5 | 5 | true | none | minimize effective cost; local/fixed candidates preferred |
| `default` | 7 | 8 | true | none | balanced default; local/fixed or healthy subscription can win |
| `smart` | 9 | 10 | false | none | quality-first; subscription/cloud-capable candidates preferred |
| `air-gapped` | 5 | 5 | true | `no_remote` | local-only execution; remote/account providers rejected |

8. `ListPolicies` returns these canonical entries and manifest metadata. It
   does not list dropped compatibility names.
9. `allow_local=false` disallows local/fixed candidates for that policy unless
   the caller changes policy or requirements.
10. `require[]` currently supports `no_remote`. Unknown requirements fail
    validation instead of being ignored.
11. `no_remote` rejects remote or account-billed candidates even when the
    caller pins a provider or harness.

### Assembled Routing Inventory

12. `ResolveRoute`, `RouteStatus`, and `fiz models` use the same assembled
    snapshot as their routing inventory contract. The router must not maintain a
    second discovery view that can diverge from operator-visible model facts.
    `ResolveRoute` is the public `route(client_inputs, fiz_models_snapshot)`
    contract.
13. The assembled snapshot contains one identity per discovered
    `(provider, model_id)` pair, including harness-as-provider identities for
    subscription harnesses. Catalog-only models do not appear as available
    models unless a configured source actually serves them.
14. Live discovery wins over configured model hints. A configured default model
    is a fallback hint when discovery is unavailable, not a closed inventory.
15. Discovered model IDs may be matched to catalog metadata when the mapping is
    unambiguous. Unknown models remain inspectable and exact-pinnable, but are
    not eligible for unpinned automatic routing.
16. The route decision trace records selected, eligible, and rejected
    candidates with typed reasons. Consumers must use typed fields, not parse
    human-readable reason strings.
17. Test-only harnesses never leak into policy-based routing unless explicitly
    requested.

### Snapshot Freshness

18. `fiz models` is quick by default. It reads the current assembled snapshot,
    returns available stale data with freshness metadata, and does not block on
    slow discovery or runtime probes.
19. `fiz models` may request a best-effort background refresh for stale fields,
    but only through the same refresh coordinator, cross-process locks, and
    single-flight markers used by blocking refresh. A short-lived CLI process
    must not spawn independent probe storms or imply that background refresh is
    required for correctness.
20. `fiz models --refresh` blocks until routing-relevant stale fields have been
    refreshed or conclusively failed. `fiz models --refresh-all` blocks on every
    refreshable field.
21. `ResolveRoute` and `Execute` are cache-first before scoring. They read the
    freshest cached routing-relevant facts available for the request: health,
    quota, model availability/discovery, context/tool/reasoning support,
    billing and effective-cost metadata when dynamic, and utilization when
    available. Stale or missing local-provider facts may request a coordinated
    asynchronous refresh, but unpinned autorouting and explicit non-local
    harness/provider selection must not synchronously contact a local model
    provider or block on `/v1/models`. Cached failed probe/discovery evidence
    still gates known-dead local providers with typed dispatchability reasons.
22. A DDx server or other long-running client may maintain freshness by calling
    Fizeau's refresh/warmup entrypoints on a heartbeat. This is an optimization
    over the same lock-coordinated cache contract. If no maintainer is running
    and `fiz models` observes stale fields, it should expose the stale status
    and suggest `fiz models --refresh` or starting a DDx freshness maintainer.

### Eligibility and Pins

23. Hard pins narrow the candidate set before scoring:
    - `Harness` means only that harness may be used.
    - `Provider` means only that provider source or selected endpoint may be
      used.
    - `Model` means only that exact model identity may be used.
24. Unpinned automatic routing excludes pay-per-token candidates unless the
    provider is included by default and metered routing is explicitly opted in
    by user config.
25. Pins override provider `include_by_default` and metered opt-in: a
    deliberately pinned default-deny pay-per-token provider can be considered.
26. Pins do not override policy `require[]`; `air-gapped` plus a remote
    provider pin fails.
27. Missing-power, inactive, deprecated, and exact-pin-only models are excluded
    from unpinned automatic routing. Exact model pins may still use them when
    the selected harness/provider can serve the model.
28. Hard gates are limited to explicit user constraints and dispatchability:
    pins, `require[]`, `no_remote`, metered opt-in, exact-pin support, and
    whether the candidate can actually be dispatched. Known-down endpoints,
    exhausted quota pools, and missing required context/tools/reasoning support
    are dispatchability failures. Cost, quality, non-fatal health risk, latency,
    utilization, and power fit are scoring inputs, not broad vetoes.

### Power Scoring

29. `MinPower` and `MaxPower` are soft scoring hints, not closed candidate
    lists, once a model has passed auto-routable eligibility.
30. A candidate below `MinPower` receives a stronger penalty than a candidate
    above `MaxPower`. This asymmetric scoring reflects failure risk: too weak
    is more likely to fail the task, while too strong is primarily a cost and
    latency concern.
31. If no power hints are supplied, model power contributes positively to the
    score alongside policy cost/placement preferences.
32. Exact `Model` pins keep exact identity. Policy-derived power bounds are
    still reported as evidence, but they do not substitute a different model.

### Ranking

33. Ranking considers:
    - policy baseline (`cheap`, `default`, `smart`, `air-gapped`);
    - catalog power;
    - provider billing and effective cost;
    - subscription shadow cost using PAYG-equivalent effective cost while
      retaining `actual_cash_spend=false`;
    - subscription quota health and burn-rate prediction;
    - route-health cooldown and observed reliability;
    - context headroom and required capabilities;
    - observed latency/speed;
    - endpoint utilization and saturation;
    - sticky affinity.
34. A qualified candidate is one that passes hard constraints, policy
    requirements, default-inclusion and metered opt-in gates, auto-routability,
    liveness, quota, and dispatchability. Power hints shape ranking inside that
    qualified set rather than replacing exact pins.
35. For a given policy and qualified set, Fizeau prefers the lowest effective
    cost candidate whose power fit is sufficient for the policy intent.
    A zero-cost but substantially underpowered candidate should not beat an
    in-band candidate for routine `default` work solely because it is free. A
    subscription model may have `actual_cash_spend=false` and still carry a
    PAYG-equivalent effective cost for scoring.
36. `cheap` selects the lowest effective-cost candidate with enough expected
    capability for the request. It should naturally prefer nano, mini, local, or
    fixed-cost candidates over maximum-quality frontier models when those
    cheaper candidates are available and sufficient; this is a scoring outcome,
    not a frontier-model exclusion gate.
37. `default` selects a routine balanced candidate. It should avoid maximum
    frontier models when lower-cost candidates satisfy the same routing
    constraints and expected capability.
38. Local/fixed candidates are preferred by `cheap` and `default` when they are
    eligible and capable. This preference never beats hard pins or
    `require[]`.
39. `smart` prefers higher-capability subscription/cloud routes when healthy
    and allowed.
40. `air-gapped` is local-only through `require=["no_remote"]`.
41. If at least one candidate is dispatchable under the user's explicit
    constraints, automatic routing must select one candidate even when the
    result is imperfect. If user constraints remove all candidates, routing
    fails clearly and attributes the failure to those constraints.
42. The router dispatches one selected candidate per request. Semantic retry or
    escalation belongs to the caller.

### Status and Evidence

43. `ResolveRoute` returns the selected candidate plus the full candidate
    trace, power policy, sticky evidence, utilization evidence, and the selected
    model's catalog-projected power.
44. `RouteStatus` reports recent decisions, cooldowns, provider reliability,
    sticky assignments, and routing-quality metrics. Routing quality is
    distinct from provider reliability.
45. Session logs and final events record the actual attempted route and failure
    class. They use v0.11 `policy` / `power_policy` fields.
46. When a route succeeds, fails, or rejects candidates, the evidence must be
    explainable from the same assembled snapshot facts exposed by `fiz models`
    plus request-local constraints.
47. Route evidence records the snapshot version, per-field freshness,
    refresh-failure status, effective cost, cost source, billing kind, and
    `actual_cash_spend` for selected and rejected candidates.

## Acceptance Criteria

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-004-01 | The embedded manifest is schema v5 and validates models, policies, providers, billing, and supported requirements. | `go test ./internal/modelcatalog ./...` |
| AC-FEAT-004-02 | `ListPolicies` returns exactly `air-gapped`, `cheap`, `default`, and `smart` with power bounds, `allow_local`, `require[]`, and manifest metadata. | `go test ./... -run ListPolicies` |
| AC-FEAT-004-03 | `cheap`, `default`, `smart`, and `air-gapped` produce the documented local/subscription/remote behavior under representative inventories. | `go test ./internal/routing ./... -run Policy` |
| AC-FEAT-004-04 | Pay-per-token providers are skipped in unpinned automatic routing unless provider default inclusion and metered opt-in both allow them, while explicit pins can select them. | `go test ./... -run 'IncludeByDefault|Metered'` |
| AC-FEAT-004-05 | Pins override default inclusion and metered opt-in but not `require[]`; `air-gapped` plus a remote pin returns `ErrPolicyRequirementUnsatisfied`. | `go test ./internal/routing ./... -run Policy` |
| AC-FEAT-004-06 | Soft power scoring penalizes undershooting `MinPower` more than overshooting `MaxPower` and does not replace an exact model pin. | `go test ./internal/routing ./... -run Power` |
| AC-FEAT-004-07 | Route decisions consume the assembled snapshot, expose typed candidate rejection reasons, score components, selected endpoint/server instance, sticky evidence, and utilization evidence. | `go test ./... -run 'ResolveRoute|RouteStatus|routing_decision|ModelSnapshot'` |
| AC-FEAT-004-08 | `ResolveRoute` and `Execute` are cache-first and may request coordinated asynchronous refresh for stale routing-relevant fields, while `fiz models` stays quick by default and `--refresh`/`--refresh-all` block explicitly. | `go test ./internal/discoverycache ./internal/modelregistry ./agentcli ./... -run 'Refresh|Fresh|Models'` |
| AC-FEAT-004-09 | Subscription candidates use PAYG-equivalent effective cost for scoring while surfacing `actual_cash_spend=false`; pay-per-token candidates are hard-gated only when dispatch would create actual metered spend without opt-in. | `go test ./internal/routing ./... -run 'EffectiveCost|ActualCashSpend|Metered'` |
| AC-FEAT-004-10 | Removed v0.10 names are not advertised by policy listing, CLI help, or public service fields. | `go test ./agentcli ./cmd/fiz ./...` |

## Constraints and Assumptions

- Fizeau owns routing inside the embedded runtime; callers own semantic retry
  and cross-harness orchestration strategy.
- Provider configs remain transport/auth definitions.
- Catalog data can be refreshed explicitly, but normal request execution is
  offline with respect to manifest fetching. Runtime snapshot facts may request
  asynchronous refresh before or after autorouting, but route scoring uses cached
  evidence and must not block on local provider contact.
- Benchmark inputs inform power, but deployment class and cost prevent local
  community copies from tying managed frontier models solely on one benchmark.

## Dependencies

- `FEAT-003` for provider identity, billing, and default inclusion.
- `FEAT-005` for cost/session projections.
- `FEAT-006` for the CLI surface.
- `ADR-009` for the v0.11 naming and migration decision.
- `ADR-012` for assembled snapshot cache and harness-as-provider identity.

## Out of Scope

- User-authored route tables or per-request fallback chains.
- Automatic learning from routing-quality metrics.
- Network manifest refresh during ordinary execution.

## Historical Implementation Proposal (2026-05-31; Non-Normative)

> This dated DDx queue-drain proposal is retained only as decision history. It
> is not a Fizeau requirement, acceptance surface, or implementation plan. The
> approved design above supersedes it: Fizeau applies provider-neutral
> eligibility and scoring to local and cloud provider systems, dispatches one
> selected route, and returns typed evidence. The caller owns bead outcomes,
> semantic retry, and cross-harness escalation. The tier names, model examples,
> retry actions, thresholds, and phases below MUST NOT be treated as current
> product behavior.

The proposal's historical goal was to drain queues with consistent progress
without exhausting subscription quota before reset.

### Subscription tiers (per harness)
Each flat-rate subscription harness exposes three tiers:

| tier | claude | codex | catalog power | auto-routing |
|---|---|---|---|---|
| low | haiku | gpt-nano | 7 | **pin-only** (`exact_pin_only`); never auto-selected |
| **middle** | sonnet | gpt-5.4-mini | 8 | **default auto-route start** |
| max | opus | gpt-5.5 | 10 | escalation target; (later) direct on trusted quota surplus |

Cheap work belongs on **local compute**, not a weak subscription tier — a weak
subscription tier wastes flat throughput on give-ups. Weak tiers are usable only
when explicitly pinned.

### Local models = non-blocking, speculative mixin
Local (qwen) endpoints are slow/weak and frequently unavailable. They are NOT a
primary tier. When healthy, a small sample of low-stakes work may be routed to them
**speculatively** to gather usage data: isolated git worktree, hard-kill timeout at
the checkout layer, **no durable bead claim until the result validates**, separate
metrics namespace, never eligible for dirty/migration/refactor/fragile beads, off the
critical path. **INVARIANT (top acceptance test): a local/unavailable/stale provider
NEVER gates the candidate set and NEVER blocks subscription routing — provider-down is
a clean skip, not a routing failure.**

### Quota signal = best-effort telemetry, not a hard control input
Subscription quota is observed best-effort (per-response token headers preferred over
PTY `/usage` deltas, which are contaminated by concurrent workers + manual CLI + async
refresh and lack reliable reset timestamps). A data-quality state machine labels each
snapshot `fresh | stale | missing | conflicted | reset_detected`. **When quota is not
fresh+attributable, behave conservatively: route MIDDLE, no max-start, no burn-model
update.** Burn estimates use conservative percentiles (p75/p90), not mean, with an
explicit cold-start prior of "unknown => scarce". (This amends "Out of Scope: automatic
learning from routing metrics" — telemetry informs policy but thresholds remain
operator/data-tuned, not self-learned closed-loop.)

### Escalation: single owner, genuine-failure only
There is exactly ONE owner of middle->max escalation; the other layers (fizeau engine
ladder, service `escalatePolicyLadder`, ddx power-retry) must not independently
escalate tier. Escalation is a single hop, fired ONLY on a typed **genuine
implementation/capability failure** classification. A per-pool in-flight max-attempt
cap prevents multi-worker stampede onto the max tier.

### Historical failure-classification table (rejected as Fizeau authority)
Every attempt outcome maps to exactly one action. ddx and fizeau MUST classify
identically against this table:

| outcome | action |
|---|---|
| `success` / `already_satisfied` | close bead |
| `no_changes` (agent produced nothing, rationale present) | close or unclaim per rationale; **do NOT escalate tier** |
| genuine implementation/capability failure (tests fail after real work; AC unmet) | **escalate middle->max once** |
| dirty worktree / partial write | clean/reset worktree, **same-tier retry** (not escalation) |
| provider transport failure (i/o timeout, conn refused, 5xx) | **same-tier reroute** to another dispatchable candidate; no tier change |
| quota exhausted (`retry_after`) | **global pool cooldown/sleep** until reset; not per-bead mutation, not escalation |
| auth/config missing, toolchain/setup failure | **operator-attention** (typed bead state); do not escalate |
| timeout / no-progress watchdog | clean + same-tier retry once, then operator-attention |
| merge/land conflict | unclaim + retry later; not escalation |
| max-tier genuine failure | **operator-attention** (stop escalating) |

`operator-attention` = an explicit bead state transition (not a notification or an
indefinite queue wait).

### Total-subscription-exhaustion behavior
Both subscriptions exhausted + local down => **global pool cooldown/sleep** honoring
`retry_after`, NOT N per-bead cooldown mutations and NOT spin-polling.

### Phasing (reliability first; economics last, telemetry-gated)
- **Phase 0 - Foundations:** repo isolation + dirty-repo guard with cleanup/revert
  contract; freeze routing replay fixtures (baseline before any change); define the
  consistent-progress throughput metric; pick the single escalation owner and freeze
  what the other layers must not do.
- **Phase 1 - Boring reliable router (first slice):** low tier `exact_pin_only`;
  default-start MIDDLE; the local-never-gates invariant + test; the failure-class
  table + tests; the single one-hop middle->max retry on typed genuine failure with a
  per-pool max-attempt cap; **no max-start / no quota economics yet**; kill-switch
  to force a known-good simple policy.
- **Phase 2 - Telemetry (best-effort):** header-based burn per (harness,tier); data-
  quality state machine; conservative percentiles + cold-start prior; fix codex
  `/status` probe and gate codex-pool routing on it (until then codex = unknown=scarce).
- **Phase 3 - Quota economics (only once telemetry is trusted):** abundance gate with
  two-threshold hysteresis (>60% enter max-start, <40% leave) + minimum dwell; requires
  real reset timestamps; cross-pool normalized surplus after reserve + in-flight burn +
  escalation budget, sticky primary; E[quota] escalation with double-spend buffer gated
  on an INPUT-side difficulty signal.
- **Phase 4 - Local mixin:** the speculative/isolated sampling above.
- **Phase 5 - Gate-model cleanup:** collapse the ~14 ad-hoc reject gates to
  {explicit-constraints, network-dispatchability, capability-dispatchability} + scoring,
  behind a kill-switch, with a full gate-mapping table and replay equivalence tests.

### Cross-cutting
Concurrency model (workers-per-pool, in-flight burn reservation); route observability
(chosen route, snapshot age/source/confidence, scarcity score, fallback reason);
simulation/replay harness before any live routing change. All thresholds are
data-tunable, NOT spec-locked constants.
