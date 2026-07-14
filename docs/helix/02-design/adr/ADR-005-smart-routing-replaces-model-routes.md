---
ddx:
  id: ADR-005
  depends_on:
    - FEAT-004
  review:
    self_hash: e47168fa6ebdb3a0f57d9a5e34cc638563f74fe5c529f73e0bee327259c7bec5
    deps:
      FEAT-004: 9761114849a85ae13627ea086fdfb1d332edda875fd81cb3769096bedc7eaeae
    reviewed_at: "2026-07-14T20:00:14Z"
---
# ADR-005: Power-Based Routing Replaces `model_routes`

## Superseded in part by ADR-009

ADR-009 owns the v0.11 public routing surface. The power-routing mechanics in
this ADR remain implementation reference, but profile, target, alias, and
surface-policy vocabulary here is historical.

ADR-011 owns the current cost model. Any older language in this ADR that made
subscription quota equivalent to zero effective cost is historical;
subscription candidates now carry PAYG-equivalent effective cost for scoring
while preserving `actual_cash_spend=false`.

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-04-25 | Accepted | Fizeau maintainers | `CONTRACT-003`, `SD-005`, `FEAT-004` | Medium |

## Context

SD-005 currently makes `model_routes:` the resolution surface: users
hand-author ordered candidate lists in YAML, the CLI re-reads them and
synthesizes a `RouteDecision` injected through
`ServiceExecuteRequest.PreResolved`, and the service treats that injected
decision as authoritative. The block exists to coordinate same-strength
failover among local LM Studio hosts and to keep the routing engine from
stripping configured candidates whose discovery probe is failing.

That design creates two problems:

1. **Configurable failover in user YAML is the wrong surface.** Provider source
   config should declare transport and auth. The model catalog should own model
   metadata and policy. The routing engine should build live candidates by
   joining discovered inventory with catalog data. Requiring users to also
   maintain `model_routes:` forces them to coordinate three sources of truth.
2. **The CLI synthesis path is leaky.** `cmd/agent/main.go:474-487` builds a
   `RouteDecision{Reason: "cli configured route"}`, threads it through
   `ServiceExecuteRequest.PreResolved`, and overwrites the request's
   `Provider`/`Model`/`Harness` fields, even though the contract says
   `PreResolved` mode ignores those fields. The mechanism exists only because
   the routing engine strips configured candidates on probe failure.

The target shape is automatic routing: users configure provider sources and
endpoints, the agent asks those sources which models they can serve, joins the
inventory with the catalog, tracks service-observed usage and availability, and
selects the best candidate that satisfies the caller's hard constraints and
optional power bounds. Users do not maintain routing tables.

## Decision

Replace the `model_routes`-driven resolution surface with deterministic
power-based routing.

### Power-Driven Candidate Inventory

The primary routing strength input is numeric model `power`, an integer from
1..10 owned by the catalog. Higher means more capable for agent tasks. `0`
means unknown, missing, or not eligible for automatic routing. The routing
contract is numeric: request fields are `MinPower` and `MaxPower`
(`--min-power` / `--max-power` in CLI form).

The service must build a complete joined inventory before choosing:

1. **Provider source and harness inventory** — enumerate every available
   execution surface: prepaid subscription harnesses, native provider sources,
   configured endpoints, and test harnesses only when explicitly requested.
2. **Model inventory** — ask each surface what concrete models it can serve.
   Live `/models` or harness discovery output wins. Configured endpoint default
   models are fallback hints, not the whole inventory.
3. **Catalog join** — match discovered concrete models to catalog entries.
   Catalog metadata supplies family, context window, reasoning support, tool
   support, quality benchmarks, deprecation status, list price,
   provider/deployment class, and required `power`. Discovered models without
   catalog power remain inspectable and may be used when explicitly pinned, but
   they are not eligible for automatic routing.
4. **Usage/cost join** — attach live usage/quota signals where the surface can
   provide them. Prepaid harnesses expose quota remaining and reset time; paid
   metered providers expose static or live cost; local/free providers expose
   zero effective cost plus measured latency/reliability when known.
5. **Inspectable output** — expose the joined inventory through
   `fiz --list-models` and the service `ListModels` API. Operators must
   be able to inspect the same candidate table the router scores: harness,
   provider source, endpoint/host, model, power, family,
   provider/deployment class, effective cost, quota/reset, context, tool
   support, reasoning support, health, recent latency, availability status,
   auto-routable status, exact-pin-only status, and filter reasons.

Power is a catalog-owned ordering. The catalog must assign power to every
model eligible for automatic routing. Initial values can be synthesized from
normalized coding benchmarks, context window, tool/reasoning support, recency,
cost, and provider/deployment class. Cost times recency is the default proxy
when benchmark data is sparse: within a provider/model family, the newest and
most expensive model is assumed to be that provider's strongest model unless
the catalog contains an explicit power/cost override. Older models in the same
family are not eligible for automatic routing unless the caller directly pins
them or the catalog records why their cost/power tradeoff is still useful.

Provider/deployment class is part of power assignment. A local, community, or
self-hosted copy must not receive the same power as a managed cloud frontier
model solely because one benchmark is high. The catalog should keep raw inputs
and derived power together so new benchmark data can revise power
quantitatively instead of relying on hand-guessed membership buckets.

Implementation status as of 2026-04-30: the embedded v4 catalog does not yet
define `power`, and `UpdateManifestPricing` only imports OpenRouter pricing and
context length. Adding catalog power schema and bootstrapping values for every
auto-routable model is prerequisite work before this routing interface can
ship.

### Scoring

Selection is a transparent utility calculation, not a hidden preference:

```text
score = power_weighted_capability
      + latency_weight
      + placement_bonus
      + quota_bonus
      - effective_cost_penalty
      - availability_penalty
      - stale_signal_penalty
```

Prepaid quota changes `actual_cash_spend`, not the basic comparison between
models. Subscription candidates carry PAYG-equivalent effective cost for
scoring while preserving `actual_cash_spend=false`; scarce quota adds penalty
and exhausted quota removes the pool. Local LM Studio, oMLX, Ollama, Lucebox,
vLLM, and llama-server providers are treated as zero effective cost but still
compete on capability, tool support, context, latency, availability, and
endpoint utilization when choosing among equivalent local endpoints.

When no hard axes or power bounds are supplied, the service selects the best
lowest-cost viable auto-routable model it can use from the discovered
inventory. If strong prepaid quota is available and inexpensive at the margin,
the selected model may be a current frontier model. If only local providers are
live, the selected model may be a local model that clears capability gates.

Provider placement is candidate-level. The native `agent` harness is not
itself local/free, prepaid, or metered; its child provider endpoints are. A
single native harness may contain local oMLX, local LM Studio, and paid
OpenRouter providers, and placement filtering must operate on those provider
candidates.

Profile and target resolution remains catalog-owned, but provider-backed
routing must not stop at the target's primary concrete model when a target has
an ordered `candidates` list. For endpoints that publish live model discovery,
the router checks the ordered catalog candidates against the endpoint's
advertised model IDs and uses the first candidate that matches. This preserves
catalog tier policy while allowing local endpoints to serve provider-native
variants such as `Qwen3.6-27B-MLX-8bit` when the primary candidate for the tier
is hosted somewhere else.

Local endpoint routing adds a sticky utilization step inside the eligible
candidate set. If a request carries a sticky route key, normally the validated
`CorrelationID` or a future worker/session sequence ID, and that key has a live
lease for an endpoint that still serves the resolved model, the router reuses
that endpoint. If no valid lease exists, the router assigns the key to the
least-loaded equivalent endpoint. Existing sticky keys move only when the pinned
endpoint disappears, stops serving the model, enters cooldown, or crosses a hard
saturation threshold.

Provider-owned utilization probes refine new sticky assignments but do not
replace route leases. `vllm` probes root `/metrics` for
`vllm:num_requests_running`, `vllm:num_requests_waiting`, and cache pressure.
`llama-server` probes root `/metrics` when started with `--metrics`, and falls
back to root `/slots` when metrics are unavailable. A configured
OpenAI-compatible base URL ending in `/v1` is converted to server root for these
probes. Probe failure makes utilization unknown/stale, not unavailable; routing
falls back to service-owned in-flight lease counts. In multi-machine
deployments, a shared lease backend is required for correct cross-process
stickiness and fair distribution because server metrics alone are sampled and
racy. The shared lease contract is specified in
[plan-2026-05-05-shared-lease-backend.md](../plan-2026-05-05-shared-lease-backend.md).

### Hard Constraints

`Execute` auto-fills only the axes the caller left unconstrained. `MinPower`
and `MaxPower` are broad routing policy. `Harness`, `Provider`, and exact model
identity are hard constraints:

- `Harness=claude` means only the Claude harness may be used.
- `Provider=lmstudio` means only that provider source, or a clearly scoped
  endpoint selector on request surfaces that support endpoint selection, may be
  used.
- `Model=qwen-3.6-27b` means only that model identity may be used. The router
  may optimize provider source and endpoint choice inside that model
  constraint, but it must not select a different model.

Catalog model aliases may resolve exact model identity or migration names, but
they do not define routing personas. If a constrained request cannot be
satisfied, routing fails with a detailed candidate/error trace instead of
broadening the constraint.

Power bounds never override hard `--model`, provider-source/endpoint, or
`--harness` pins. Models with missing or zero power remain inspectable and may
be used by exact pin when available, but are excluded from unpinned automatic
routing.

### Routing Decision

Per request:

1. **Build the candidate set** = every available `(harness, provider source,
   endpoint, model)` joined with the catalog and live provider/harness signals.
   Provider-backed profile/target references expand to the target's ordered
   catalog candidates before live discovery filtering.
2. **Apply hard constraints** before scoring: exact model identity, provider
   source/endpoint, harness, and any caller capability requirements.
3. **Filter by liveness** via `HealthCheck`, recent cooldown state, and live
   model discovery. Drop endpoints whose latest probe failed or which do not
   advertise the candidate model. If the filter empties the set, return a
   no-candidate decision with the full rejected trace.
4. **Filter by capability and power**: drop candidates whose context window <
   `EstimatedPromptTokens`, whose `SupportsTools()` is false when
   `RequiresTools` is true, whose reasoning support is below the request, whose
   catalog power is outside `MinPower`/`MaxPower`, or whose catalog status
   excludes automatic routing.
   Provider-native model IDs with unambiguous casing, prefix, quantization, or
   packaging differences must map back to catalog metadata before this gate, so
   discovered IDs inherit the intended power, context, tool-support, and
   auto-routable status.
5. **Apply sticky local endpoint assignment**: reuse an existing live lease for
   the sticky route key when present, otherwise use endpoint utilization and
   service-owned lease counts to choose among equivalent local endpoints serving
   the same resolved model.
6. **Score each survivor** using explicit score components: catalog quality,
   observed latency, effective cost, quota/reset state, local/free preference
   when constraints are satisfied, endpoint utilization pressure, availability,
   and staleness penalties. Candidate trace output must expose these
   components.
7. **Dispatch top-1 once**, return the full ranked candidate trace in the
   routing decision event so callers can see why candidates 2..N lost.
8. **Report dispatch outcome** for the attempted candidate. Do not rotate to a
   second candidate. Record only availability/transport/protocol outcome facts
   for the attempted `(harness, provider source, endpoint, model)` tuple and
   return structured evidence to the caller.

The implementation collapses these user-visible steps into two phases:

**In `routing.Resolve` (`internal/routing/engine.go`):** consume a fully joined
candidate inventory, apply inline gates, score eligible candidates with power,
cost, latency, capability, availability, placement, and quota signals, then
rank and tie-break by cost and latency.

**In `service.ResolveRoute` (`service_routing.go`):** call the engine once for
the requested power bounds/constraints and preserve the candidate trace even on
failure. Catalog power filtering happens in the engine's inline gates as part
of candidate construction. Retry is not performed here; the service returns the
ordered trace and attempted-route outcome for callers that own retry policy.

### Caller-Owned Retry

Retry is not an agent responsibility. `Execute` selects the best candidate,
dispatches that one candidate, and reports what happened. It does not try a
second candidate and it does not widen power bounds. Provider-specific
authentication, quota, transport, timeout, stream, subprocess, or protocol
failures are reported as facts about the attempted `(harness, provider source,
endpoint, model)` tuple; callers decide whether to issue a new request using a
different power range or different hard pins.

Task-level escalation across power ranges is caller-owned. A caller such as
DDx owns that policy because it has task context, budget, retry limits, and
semantic evidence from tests/reviews. The service must therefore return enough
structured evidence for the caller to decide:

- requested/effective power bounds and hard constraints
- selected candidate and full candidate trace with power and filter reasons
- attempted candidate and availability/transport failure class when dispatch
  failed
- score components and live cost/quota/latency facts

The service exposes numeric power as machine-readable metadata, but it must not
present that metadata as a retry decision. The caller applies budgets, task
policy, and semantic evidence before issuing another request. If DDx later
determines from tests, review, or acceptance evidence that the chosen model was
too weak, DDx may retry the same task with a higher `MinPower` while preserving
first-attempt logs and budget accounting. DDx must not retry on deterministic
setup/config failures.

### Provider Availability Feedback

The agent service owns only provider availability feedback for candidate
selection. Minimum signal key: `(harness, provider source, endpoint, model)`.
A single bad endpoint must not poison its whole provider source, model family,
or power range.

`Execute` records the attempted route's service-observed availability outcome:
success, transport errors, auth/quota/rate limits, 5xx responses, stream loss,
subprocess exit, timeout, malformed protocol output, capability mismatch,
duration, usage, and cost when known. The scoring engine uses availability,
latency, quota, and cooldown state from this store.

Semantic task outcomes are not agent route feedback. If DDx learns that a model
was too weak because tests failed, review blocked, or acceptance criteria were
missed, that evidence belongs to DDx and may be contributed to the model
catalog or catalog-derived power ratings. It does not directly demote a live
provider in agent's transient routing state.

### Subscription Quota Inputs

Subscription harnesses already publish quota signals via harness caches
(`service_routing.go:335`). Cost ramping when at least 80% used already
exists. Keep both unchanged.

OpenRouter and native HTTP providers do not publish live quota. Treat their
cost as static catalog cost in this round; file a follow-up bead for live-quota
plumbing on those providers but do not block this work on it.

### `route-status` Redesigned

Today `route-status` enumerates configured `model_routes` keys. Post-deletion
it must report eligible candidates for requested power bounds, with score
components (power, cost, latency, availability, filter reason) per candidate
and per-(provider source, model, endpoint) availability/latency facts.
Operators read it to answer "why did the router pick X?" rather than to
inspect their own YAML.

### Delete

- `model_routes:` config block; its loader in `internal/config/config.go`;
  `ServiceConfig.ModelRouteConfig`/`ModelRouteNames`.
- `service_routing.go` `model_routes` short-circuit landed in `90d9b03`
  (revert).
- `ServiceExecuteRequest.PreResolved` and `RouteDecision`-as-input.
  `PreResolved` was specified for a dry-run-then-execute flow that has no
  current consumer; its only producer in the repo is the CLI synthesis at
  `cmd/agent/main.go:474-487`, which is itself part of the `model_routes`
  deletion. `ResolveRoute` remains as a public method (operator dashboard /
  debug surface), but its result is informational, not re-injectable.
- CLI `selection.RouteCandidates` and `cmd/agent/routing_provider.go`
  provider-construction wrappers.
- SD-005 D4-D7 (`model_routes` surface). SD-005 rewritten from this ADR.

### Keep

- `routing.default_model`, `routing.default_model_ref`,
  `routing.health_cooldown` config keys. These are useful defaults, not
  `model_routes`.
- `internal/modelcatalog` as source of truth for cost, context, capability,
  power, provider/deployment class, and deprecation state.
- `internal/routing` engine scoring; refactor input source, do not rewrite
  scoring wholesale.
- Provider adapters, `internal/reasoning`, and the three session-log refactors
  landed earlier in this stack (`agent-7faa0edf`, `agent-b9bd700f`,
  `agent-99549438`).
- `--min-power`, `--max-power`, `--model`, `--provider`, `--reasoning`, and
  `--model-ref` CLI flags.

## Consequences

### Positive

- One source of routing truth: provider source/endpoint config plus catalog
  metadata plus the engine's live inventory join.
- Local/free preference works automatically when local/free candidates satisfy
  requested power, tools, context, and availability constraints.
- Subscription harnesses can win when quota is healthy and PAYG-equivalent
  effective cost plus capability fit beats the alternatives.
- Per-(provider source, endpoint, model) signal recovers from transient
  failures; one bad model or endpoint no longer locks out unrelated candidates.
- `RouteCandidate` exposes structured score components, not a free-form
  `Reason` string. Operator debugging gets a real surface.
- Public `RouteRequest` exposes the prompt-aware inputs the engine needs;
  service-side routing is no longer blind.

### Negative

- Removes a configurable failover surface. Operators who deliberately wire an
  ordered candidate list lose that knob. Mitigation: explicit provider
  source/endpoint and exact model pins remain; chaining failover by ordering
  candidates was already a workaround for the engine's probe-strip behavior,
  which this ADR fixes at the source.
- Public surface change to `RouteRequest`/`ServiceExecuteRequest` (new fields;
  one removed). Consumers re-bind.
- One-release deprecation window means operators with `model_routes:` configs
  do not get an immediate hard error. Acceptable trade-off vs. silent drift.

## Migration

Plan in three sharper beads (replacing the obsolete chain
`agent-9d120ece`/`6dd4ad97`/`873081a9`/`8804194f`, which is canceled with note
"superseded by ADR-005"):

1. **Public surface update** — add `EstimatedPromptTokens` / `RequiresTools` to
   `RouteRequest` and `ServiceExecuteRequest`; remove
   `ServiceExecuteRequest.PreResolved`; add structured score components to
   `RouteCandidate`; update CONTRACT-003. Revert `90d9b03`. Update SD-005 with
   the auto-selection section and deprecation note.
2. **Wire inputs + scoring + route-status** — plumb new `RouteRequest` fields
   from CLI through `Execute`; wire engine gates against them; expose score
   components in routing-decision events; redesign `route-status` to show
   eligible candidates per intent. Add per-(provider source, endpoint, model)
   success/latency keying.
3. **Config + CLI cleanup + deprecation** — delete `model_routes` parser and
   `ServiceConfig.ModelRouteConfig`; delete CLI `selection.RouteCandidates`
   synthesis and `routing_provider.go` provider-construction wrappers; add
   deprecation warning when parsing legacy config; add boundary test forbidding
   `model_routes` re-entry.

Step 1 blocks steps 2 and 3.

## Out of Scope

- Persistent EWMA across process restarts. In-memory + TTL is fine for this
  round; persistence + warm-start is its own design.
- ML-style prompt classification beyond `EstimatedPromptTokens`/`RequiresTools`.
  Ship deterministic power-based routing first.
- Live quota plumbing for OpenRouter and native HTTP providers. Static catalog
  cost suffices in this round.
- Reviewer pipeline overflow fixes, tracked separately in the upstream `ddx`
  repo.

## Related

- `CONTRACT-003` — public service surface; updated in step 1.
- `SD-005` — provider/model/routing config; rewritten from this ADR.
- `internal/routing/engine.go` — existing scoring engine; input source
  refactored, scoring retained.
- `service_routing.go` — subscription quota cost ramp stays; `90d9b03`
  short-circuit reverts.
