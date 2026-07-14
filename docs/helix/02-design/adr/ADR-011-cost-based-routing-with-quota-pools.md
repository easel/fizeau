---
ddx:
  id: ADR-011
  depends_on:
    - ADR-005
    - ADR-006
    - ADR-007
  review:
    self_hash: 088af56c3f51ae0ba0bb0d71940195af827b2ec5b73768e11fd0d7427070f8d2
    deps:
      ADR-005: e47168fa6ebdb3a0f57d9a5e34cc638563f74fe5c529f73e0bee327259c7bec5
      ADR-006: 511bae45baeef8e764665393c35d5d80490ef1ed29a1efd769a063965a9e33bc
      ADR-007: 8a70a46a9efdc916ea7e3146dce7050a12d054c1c9d56c006bb4c3245b4d4300
    reviewed_at: "2026-07-14T20:00:14Z"
---
# ADR-011: Cost-Based Routing With Quota Pools

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-12 | Accepted | Fizeau maintainers | `ADR-005`, `ADR-006`, `ADR-007`, `fizeau-c04be6b0`, `fizeau-d18e11f5` | Medium |

## Context

ADR-005 established automatic routing over a joined candidate inventory. ADR-006
framed explicit harness, provider, and model pins as override signals rather
than the normal operating mode. ADR-007 moved generation defaults into the
catalog so callers do not compensate for missing policy with ad hoc request
knobs. ADR-009 later tightened the public surface around policies, numeric
power bounds, and hard pins.

The remaining routing gap is cost. For a given required model power, the router
should prefer the cheapest qualified candidate that is likely to complete the
request. "Cheapest" cannot mean list price alone because Fizeau can dispatch
through several billing shapes:

- subscription harnesses, where dispatch does not create actual pay-per-token
  spend but model choices still consume a scarce prepaid allocation;
- per-token APIs, where each request burns metered input and output tokens;
- local or fixed-cost providers, where effective cost is zero but only if the
  provider is eligible for automatic routing;
- provider surfaces excluded from unpinned automatic routing by
  `IncludeByDefault` or
  metered-spend policy, where accidental spend or special-purpose endpoints
  must be impossible unless the caller explicitly pins them.

Recent implementation beads provide two pieces this ADR can rely on without
specifying new code in this bead. `fizeau-c04be6b0` added catalog
`quota_pool` schema and the default semantic that missing pool values derive
from the provider system. `fizeau-d18e11f5` wired configured provider
`IncludeByDefault` into routing eligibility, so excluded providers are removed
from unpinned automatic candidate sets and remain reachable only through
explicit pins.

Operator direction on 2026-05-11 was: for a given model power, choose
intelligently based on cost, and let remaining usage quota influence that cost.
This ADR defines that policy. The router evaluates
`route(client_inputs, fiz_models_snapshot)` and treats the snapshot as the only
source of routing facts. Implementation follows in a separate epic after the
ADR is accepted.

## Decision

### Candidate Eligibility Comes Before Cost

The router first builds the joined inventory described by ADR-005 and filters it
before any cost ranking. Hard gates are limited to explicit user constraints and
dispatchability:

1. Apply hard pins for harness, provider, and exact model identity.
2. Drop candidates that fail policy requirements, including `no_remote`,
   metered-spend opt-in, `IncludeByDefault` for unpinned automatic routing, or
   exact-pin support when the request is not dispatchable to the pinned target.
3. Drop candidates that are not actually dispatchable because the selected
   source, endpoint, or harness cannot serve the requested model.
4. Apply quota-pool exhaustion: when a quota pool is known exhausted, drop every
   candidate in that pool.

Only surviving candidates receive cost scores. This preserves the ADR-006
principle that manual pins are override signals and prevents price scoring from
accidentally making an excluded provider "almost eligible."

Power bounds are deliberately not hard eligibility filters in this ADR. FEAT-004
and ADR-009 define `MinPower` and `MaxPower` as soft scoring inputs after
dispatchability checks: undershooting the requested floor is penalized more
heavily than overshooting the requested ceiling, but an otherwise eligible
model outside the band may still be ranked when its cost, health, and
capability trade-off is better than the alternatives. Exact model pins keep
exact identity and do not substitute another model to satisfy power hints.

### Effective Cost Formula

Effective cost is request-local and expressed in normalized dollars so
subscription, metered, and local candidates can be compared in one ranking.
Effective cost is not the same as actual cash spend. `actual_cash_spend`
answers whether dispatch creates incremental pay-per-token billing;
`effective_cost` answers how expensive the candidate is for scoring and quota
stewardship.

For per-token APIs:

```text
effective_cost =
  cost_input_per_m  * estimated_input_tokens  / 1_000_000
+ cost_output_per_m * estimated_output_tokens / 1_000_000
```

For local or fixed-cost providers, `effective_cost = 0` after eligibility
filters. They can still lose to candidates with a better policy power fit when
the local candidate is materially underpowered for the requested policy, and
they still carry latency, reliability, and health signals outside this ADR's
cost term.

For subscription candidates, `actual_cash_spend=false` remains true because the
operator is not incurring per-request billing. The router computes a
PAYG-equivalent effective cost from the comparable metered price so subscription
models with different underlying prices remain comparable:

```text
payg_equivalent_cost =
  payg_input_per_m  * estimated_input_tokens  / 1_000_000
+ payg_output_per_m * estimated_output_tokens / 1_000_000

quota_fraction = remaining_quota / quota_limit

if quota_fraction <= 0:
  drop the quota pool
if quota_fraction >= 0.20:
  effective_cost = payg_equivalent_cost
else:
  scarcity_multiplier = 1 + (1 - quota_fraction / 0.20)
  effective_cost = payg_equivalent_cost * scarcity_multiplier
```

This quota fraction mapping is deliberately simple. Healthy prepaid quota is
not treated as free for scoring: a maximum-quality frontier model should still
rank as more expensive than a nano, mini, local, or fixed-cost model when those
cheaper candidates are sufficient. The final 20 percent of a pool is still
usable, but it receives a linear scarcity multiplier so another qualified
subscription pool, a local candidate, or a cheap metered API that is explicitly
opted into automatic routing can win before the pool reaches zero. When the
catalog lacks a comparable per-token price for a subscription model, the router
uses the cheapest known per-token model in the same provider family and power
band as the PAYG-equivalent proxy; if no proxy exists, the candidate keeps
`actual_cash_spend=false` but receives an unknown-cost risk penalty instead of a
zero-cost advantage. Pay-per-token providers remain gated by explicit opt-in at
dispatch time so automatic routing never creates actual metered spend without
user consent.

### Quota Pool Semantics

Models belong to quota pools. A catalog `quota_pool` value names an explicit
pool; otherwise the effective pool is the provider system, matching
`fizeau-c04be6b0`.

Pool exhaustion is a hard candidate-set event. If the main OpenAI Codex pool is
exhausted, every model in that pool is dropped together. A model in a different
pool, such as `gpt-5.3-codex-spark` in `openai-codex-spark`, remains eligible
when it passes auto-routability, capability, and policy filters and remains
competitive under the caller's soft power-fit score, even if it is not the
newest model in the family. This intentionally enables "use spark when the main
pool is empty" without encoding that fallback as a version-newest rule.

Quota signals are consumed in this priority order:

1. explicit subscription usage endpoints, when available;
2. HTTP rate-limit or quota-exhaustion response headers, including the
   `quota_exhausted` signal already plumbed by commit `7776890e`;
3. in-memory recent-rejection rate for the same quota pool.

Known exhaustion wins over stale positive data. Unknown quota is not treated as
exhausted; it is scored as scarce only when recent rejection evidence indicates
probable depletion.

### Quota Window Handling

Quota window handling is v1 local and greedy. The router uses quota available
now and does not reserve quota for later requests, future sessions, or
higher-value work. Reset time may appear in trace output and may clear a stale
exhaustion state when observed, but it does not create a reservation policy.
The quota window rule is therefore "available now," not "save for later."

This keeps routing deterministic and avoids a scheduler hidden inside the
router. Per-tenant, per-key, and priority reservation behavior is future scope.

### OpenRouter Credit-Balance Probe as a Quota Signal

OpenRouter accounts carry a prepaid credit balance; when credits are exhausted,
subsequent requests fail with HTTP 429. To detect exhaustion before costly
dispatch attempts, `service_openrouter_credit.go` implements a synchronous
credit-balance probe that runs once per provider per routing pass and caches
the result within a freshness window.

**Probe semantics:**

- **Freshness TTL**: `DefaultOpenrouterCreditProbeTTL = 10 * time.Minute`. Cached
  balance readings remain fresh for 10 minutes; after expiration, the next
  routing pass issues a new `/api/v1/credits` request. Concurrent routing calls
  coalesce on a single in-flight probe via per-provider single-flight signaling.
- **Gating threshold**: `DefaultOpenrouterCreditBalanceThresholdUSD = 0.50`. When
  a cached balance is below this floor, the provider's quota pool is treated as
  exhausted for ranking purposes. Operators may override per-provider via
  `ServiceProviderEntry.CreditBalanceThresholdUSD`.
- **Failure-mode classification**: The probe classifies failures as either
  `credential_invalid` (HTTP 401/403, key rejected) or `provider_unreachable`
  (transport error, 5xx, or other non-2xx). Credential failures persist in the
  cache and are surfaced as `FilterReasonCredentialInvalid` to routing. Provider
  unreachability is cached with a soft fail-open semantic: the entry blocks
  routing only until the TTL elapses or a subsequent probe succeeds, allowing
  transient network failures to self-heal without persistent blackout.
- **Quota-pool integration**: The probe result feeds into `openrouterProbeMaps()`
  which returns `openrouterProbeProjection`:
  - `CreditExhausted`: map of providers with balance < threshold, dropping them
    from unpinned automatic candidate sets.
  - `CredentialInvalid`: map of providers with rejected credentials, surfaced as
    a distinct filter reason so operators can diagnose key rotation or revocation.
  - `ProviderUnreachable`: map of transient probe failures, included in trace
    output but not blocking routing (fail-open until next probe).

The credit probe integrates into the quota signal priority order defined by this
ADR: it augments HTTP response header signals (rate-limit, quota-exhausted) by
probing the account state before request dispatch, reducing wasted attempts when
credit is genuinely exhausted.

### Token Estimate Source

The token estimate source for v1 is the request itself plus a coarse heuristic:

- use caller-provided `EstimatedPromptTokens` when present;
- otherwise estimate input tokens from system prompt, retained transcript,
  user prompt, and tool schemas using the same tokenizer when a provider
  tokenizer is available;
- fall back to `ceil(bytes / 4)` when no tokenizer is available.

Estimated output tokens come from the request's explicit max-output setting
when present. Otherwise v1 uses a fixed default budget by power band:

| Candidate power | Estimated output tokens |
|---|---:|
| 1-4 | 2,048 |
| 5-7 | 4,096 |
| 8-10 | 8,192 |

The estimate is intentionally conservative enough for ranking and not intended
to predict final billing exactly. Actual usage continues to be recorded from
provider responses for reporting and future tuning.

### Ranking and Tie-Breaking

Among eligible candidates, choose the lowest effective cost candidate whose
policy power fit is sufficient for the caller's intent. This ADR makes
"cheapest qualified candidate" the primary rule, not "most powerful available
candidate." "Qualified" means the candidate passed explicit user constraints,
dispatchability, default-inclusion and metered opt-in gates, and quota
exhaustion checks, then remains competitive after the FEAT-004 soft power-fit
score is applied. A free but substantially underpowered candidate should not
beat an in-band routine implementation candidate solely because it is free.
Likewise, a subscription frontier model should not beat a cheaper sufficient
subscription, local, or fixed-cost model merely because both avoid actual
pay-per-token billing.

Tie-breaking is deterministic:

1. prefer subscription candidates over pay-as-you-go candidates when effective
   cost is equal and actual_cash_spend is still false for the subscription
   candidate;
2. prefer local or fixed-cost candidates over pay-as-you-go candidates when
   effective cost is equal and capability requirements are still satisfied;
3. prefer the lower-power candidate when both have acceptable power fit;
4. prefer healthier and lower-latency candidates using ADR-005 route-health
   signals;
5. preserve stable catalog order only as the final tie-break.

### Fallback Chain On Mid-Request Exhaustion

The fallback chain is caller-owned retry, consistent with ADR-005. The service
dispatches the top-ranked candidate once. If dispatch fails because the quota
pool becomes exhausted mid-request or the provider returns a quota/rate-limit
error, the service records the pool exhaustion signal, emits the failed
attempt's trace, and returns the error.

The caller may issue a new request. On that next routing pass, the exhausted
quota pool is filtered out and the next cheapest eligible pool can win. The
router does not silently retry an alternate model inside the same service
request because that would hide cost, duplicate side effects, and blur the
operator-visible route decision.

## Consequences

### Positive

- Cost routing now reflects both actual spend and opportunity cost:
  subscription quota avoids actual pay-per-token billing, but model choices are
  still compared with PAYG-equivalent effective cost, scarce quota is protected,
  and exhausted pools are removed.
- Quota pools provide an explicit mechanism for fallback across independent
  subscription allocations without relying on model recency.
- `IncludeByDefault` and metered opt-in compose cleanly with cost ranking
  because excluded providers never enter the unpinned automatic ranked set.
- The routing trace can explain cost decisions with concrete inputs:
  estimated tokens, price data, quota fraction, quota pool, and filter reason.

### Negative

- The quota fraction threshold is a policy choice, not an empirical optimum.
  It should be tuned after route traces show real depletion behavior.
- Coarse token estimates can mis-rank close candidates. This is acceptable for
  v1 because actual billing remains visible and estimates can improve without
  changing the public contract.
- Subscription models without comparable catalog pricing need an unknown-cost
  penalty until proxy data exists. This avoids treating unknown subscription
  pricing as free.
- No within-request retry means callers may see one quota error before fallback
  takes effect on the next request.

## Out of scope

- Runtime implementation of the scorer, candidate trace fields, or quota-pool
  filters. Those belong to the follow-up implementation epic.
- New quota signal infrastructure beyond existing rate-limit/header plumbing
  and future beads for subscription usage endpoints.
- Per-tenant, per-key, or priority quota reservations.
- Learning the quota fraction mapping from historical usage.
- Counterfactual dispatch to measure whether a more expensive candidate would
  have completed better.

## References

- `ADR-005` — power-based automatic routing, candidate inventory, scoring, and
  caller-owned retry.
- `ADR-006` — manual pins are override signals, not the primary routing mode.
- `ADR-007` — catalog-owned generation policy; this ADR follows the same
  catalog-policy principle for cost and quota metadata.
- `ADR-009` — v0.11 routing surface, billing classification, and
  `IncludeByDefault` composition.
- `fizeau-c04be6b0` — catalog `quota_pool` field schema and effective-pool
  default semantics.
- `fizeau-d18e11f5` — provider `IncludeByDefault` routing eligibility filter.
- Commit `7776890e` — existing `quota_exhausted` signal plumbing from HTTP
  rate-limit response handling.
