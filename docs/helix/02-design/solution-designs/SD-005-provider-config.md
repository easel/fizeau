---
ddx:
  id: SD-005
  depends_on:
    - FEAT-003
    - FEAT-004
    - FEAT-006
    - SD-001
  review:
    self_hash: c6778d97161b6c273a32a7b9c51483c613b10d8dd96a639ced9186cc4ad960c2
    deps:
      FEAT-003: 8c4332150f3d5d591015e360231913d4e8f24f9b83f3678e65574e5f45f78e0d
      FEAT-004: 9761114849a85ae13627ea086fdfb1d332edda875fd81cb3769096bedc7eaeae
      FEAT-006: 1c78778fcc8efa7fe750cf233719c21f1f6b07ce6b098c48f6d42855d57faa07
      SD-001: 7123b4d558d2ddd35289bf49390fde9e00b52081cbe90de37986d13fbbf36988
    reviewed_at: "2026-07-15T12:04:09Z"
---
# Solution Design: SD-005 — Provider Sources, Model Catalog, and Power Routing

## Problem

Fizeau started with a single flat provider config (`provider`, `base_url`,
`api_key`, `model`). That shape is sufficient for one local inference server,
but real use has separate concerns:

1. **Provider source and endpoint setup** — concrete transport/auth data for
   model discovery and dispatch.
2. **Shared model policy** — one Fizeau-owned catalog for model identity, numeric
   power, per-surface projections, deprecations, cost, context, and benchmark
   provenance.
3. **Automatic selection** — one routing decision per request based on live
   inventory, catalog metadata, availability, usage, cost, speed, and caller
   constraints.

Prompt presets remain a separate concern for system prompt behavior only.

## Design

Fizeau keeps two layers above the runtime boundary:

- **Provider sources and endpoints** declare transport/auth locations. Endpoint
  labels may exist for diagnostics, host display, and explicit endpoint
  selection, but stable user-authored endpoint labels are not the primary
  routing abstraction.
- **Model catalog** owns reusable policy/data loaded from an embedded snapshot
  plus an optional external manifest override, with published manifest bundles
  distributed outside binary releases. It owns model power, cost, context
  window, capability metadata, provider/deployment class, benchmark provenance,
  and reasoning defaults per model.

There is no user-authored routing-rule layer in the target design. Per-request
routing follows ADR-005: the service discovers what each configured source can
serve, joins that inventory with the catalog, applies hard caller constraints
and optional power hints, scores survivors, dispatches the top candidate once,
and reports the attempted route outcome.

After resolution, the service builds exactly one concrete native provider
adapter and executes it internally. Consumers do not receive provider
instances.

Caller boundary (see CONTRACT-003):

- Callers choose a harness only when they need to constrain the execution
  surface. Otherwise the service may consider all eligible harnesses.
- Callers pass routing intent through public request fields (`Policy`,
  `Provider`, `Model`, `MinPower`, `MaxPower`) plus optional auto-selection
  inputs (`EstimatedPromptTokens`, `MaxTokens`, `RequiresTools`, `Reasoning`).
- Explicit model, provider-source/endpoint, and harness pins always win over
  automatic selection. If a hard pin cannot be satisfied, routing fails with
  detailed no-candidate evidence and never substitutes a broader model, source,
  endpoint, or harness.
- The Fizeau service chooses the concrete provider candidate, constructs the
  adapter, dispatches exactly one candidate, and reports the attempted route
  outcome.
- Callers receive attribution facts from the embedded run: selected candidate,
  rejected candidates, filter reasons, score components, and the actual
  harness/provider-source/endpoint/model used.
- Fizeau may retry transient transport failures against the selected route.
  Callers own semantic retry, task-level retry, and every cross-route
  escalation. Fizeau reports route evidence but does not dispatch another
  candidate in the same request.

## Config Format

The target config declares sources/endpoints for discovery and dispatch. It
does not encode route order, model-strength policy, or fallback chains.

```yaml
# .fizeau/config.yaml
model_catalog:
  manifest: ~/.config/fizeau/models.yaml   # optional local override

providers:
  lmstudio:
    type: lmstudio
    api_key: lmstudio
    reasoning: off
    endpoints:
      - name: vidar
        base_url: http://vidar:1234/v1
      - name: grendel
        base_url: http://grendel:1234/v1

  vidar-omlx:
    type: omlx
    base_url: http://vidar:1235/v1
    api_key: omlx
    model: Qwen3.5-27B-4bit
    reasoning: off

  openrouter:
    type: openrouter
    base_url: https://openrouter.ai/api/v1
    api_key: ${OPENROUTER_API_KEY}
    headers:
      HTTP-Referer: https://github.com/easel/fizeau
      X-Title: Fizeau
    include_by_default: false
    billing: per_token

routing:
  health_cooldown: 30s
  allow_metered: false

preset: default
max_iterations: 20
session_log_dir: .fizeau/sessions
```

The top-level `providers:` map is the current named-source schema. A provider
may use one `base_url` or an endpoint list. The endpoint-first `endpoints:`
schema is also accepted for serving targets that do not need a stable
user-facing provider name. Both forms declare transport and authentication;
neither encodes route order.

the removed route-table field was deprecated by ADR-005 and its compatibility parser has now
been removed. Automatic routing covers the same intent by combining provider
source discovery, endpoint health, catalog power, and score components without
per-candidate route order in YAML.

### Provider Source and Endpoint Fields

Provider-source fields:

| Field | Type | Description |
|---|---|---|
| `type` | enum | Provider source type such as `lmstudio`, `omlx`, `vllm`, `llama-server`, `ollama`, `openrouter`, `anthropic`, or harness-backed sources where applicable |
| `endpoints` | list | Concrete serving locations for this source |

Endpoint fields:

| Field | Type | Description |
|---|---|---|
| `name` | string | Optional diagnostic and explicit endpoint selector |
| `base_url` | string | API base URL when the source uses HTTP |
| `api_key` | string | Secret reference or literal token for the endpoint |
| `headers` | map | Optional endpoint-specific HTTP headers |
| `model` | string | Optional default model hint for direct dispatch, not catalog policy |
| `reasoning` | scalar string/int | Public reasoning control for this endpoint |
| `placement` | enum | Optional override for placement metadata: local/free, prepaid, metered, or test |
| `max_tokens` | int | Max output tokens per turn; `0` = use provider default |
| `context_window` | int | Explicit context window override; `0` = attempt live discovery |

Provider-specific wire terms such as `thinking`, `effort`, `variant`, and token
budgets are adapter implementation details, not public config.

### OpenRouter Credit Probe

OpenRouter providers expose two source-level knobs that control the proactive
account-balance probe used by the routing engine's `credit_exhausted` gate.
Routing reads a per-provider cached balance; when the cached value is below
threshold, the candidate is filtered with `filter_reason: credit_exhausted`
and the evidence body carries the observed balance plus the probe timestamp.

| Field | Type | Default | Description |
|---|---|---|---|
| `credit_balance_threshold_usd` | float (USD) | `0.50` | Minimum acceptable cached account balance. Cached readings below this value disqualify the candidate with `filter_reason: credit_exhausted` before any dispatch is attempted. |
| `credit_probe_ttl` | duration | `10m` | Lifetime of one cached `/api/v1/credits` reading. Back-to-back routing passes within the TTL share a single round-trip; the next pass after expiry re-probes synchronously. Operator-tunable in the 5–15 minute band so the cache amortizes across drains without missing top-ups. |

Both knobs are openrouter-specific. The credit gate runs only for providers
whose `type: openrouter`. Server-side credential rejection (401) and
transport failures are handled separately and surface as distinct filter
reasons (`credential_invalid` / `provider_unreachable`).

### Reasoning Values

`reasoning` is one scalar rather than separate public level and budget fields.

- Empty or unset means no caller preference.
- `auto` means resolve model, catalog, or provider defaults.
- `off`, `none`, `false`, and numeric `0` mean explicit reasoning off.
- `low`, `medium`, and `high` use portable fallback budgets of 2048, 8192, and
  32768 tokens when provider/catalog metadata does not publish a better map.
- Extended names such as `minimal`, `xhigh`, `x-high`, and `max` are accepted
  only when the selected provider or harness advertises support. `x-high`
  normalizes to `xhigh`; explicit extended requests are never silently
  downgraded.
- Positive integers mean an explicit max reasoning-token budget, or a
  documented provider-equivalent numeric value.

Providers that only accept numeric reasoning controls must map named values to
numeric budgets with capability-aware model metadata and must enforce
model-specific maximum reasoning-token limits. `max` resolves at the provider
or harness boundary to the selected model/provider maximum and is accepted only
when that maximum is known. Auto/default reasoning controls may be dropped for
unsupported providers/models, but explicit unsupported or over-limit values
fail clearly.

Model catalog metadata uses `reasoning_default`. Explicit caller values always
win when supported, including numeric values and values above `high` such as
`xhigh`, `x-high`, or `max`.

## Model Catalog and Power

Power is the canonical routing strength axis. Higher values mean stronger
models for agent tasks. Every catalog model must have power from 1..10 to be
eligible for automatic routing; power `0` means unknown, missing, or
exact-pin-only.

The catalog manifest stores concrete model metadata at the model entry level:
family, display name, status, cost, cache cost, context window, benchmark
metadata, OpenRouter ID, reasoning metadata, provider/deployment class, power,
power provenance, and consumer surface strings. Top-level policy entries define
power and placement intent without duplicating model metadata or candidate
order.

```yaml
version: 5
models:
  qwen3.5-27b:
    family: qwen
    display_name: Qwen3.5 27B
    status: active
    power: 5
    power_provenance:
      method: benchmark_cost_recency
      inputs: [swe_bench, context_window, cost, recency, deployment_class]
    deployment_class: local_free
    cost_input_per_m: 0.10
    cost_output_per_m: 0.30
    context_window: 262144
    surfaces:
      fizeau.openai: qwen3.5-27b
    reasoning_default: off
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
providers:
  lmstudio:
    type: lmstudio
    include_by_default: true
    billing: fixed
```

Bootstrap power from normalized benchmark evidence, model capabilities
(context, tools, reasoning), recency, cost, and provider/deployment class. When
benchmark coverage is missing, cost times recency is the first-order proxy:
within a provider/model family, the newest and most expensive model is assumed
strongest unless the catalog explicitly overrides power or marks an older model
as a useful cost/power exception. Older family members are exact-pin-only for
automatic routing without that override. Keep raw benchmark inputs beside the
derived power value so catalog updates can evolve scores quantitatively as new
models and measurements arrive.

Provider/deployment class prevents benchmark-only equivalence across unlike
surfaces. A local/community/self-hosted copy must not receive the same power as
a managed cloud frontier model solely because one benchmark is high.

## Resolution Model

This section is the implementation plan for the behavior FEAT-004 requires.
FEAT-004 owns the public routing contract; this design owns the sequence of
snapshot assembly, eligibility filtering, scoring, dispatch, and evidence
projection.

Per request, the service:

1. Loads provider source config and the Fizeau model catalog.
2. Assembles or reads the current available-model snapshot:
   1. Enumerates every configured harness, provider source, endpoint, and
      discovered concrete model.
   2. Joins each concrete model to the model catalog. Matched entries provide
      power, family, status, context window, reasoning capability, tool
      support, list price, deployment class, and benchmark quality. Unknown
      models remain inspectable but are not eligible for automatic routing
      unless explicitly pinned.
   3. Joins live operational signals: source/endpoint health, endpoint
      cooldown, observed latency, prepaid quota remaining/reset time, effective
      cost, `actual_cash_spend`, and cost source.
   4. Uses ADR-012 cache semantics: `fiz models` can return stale snapshot data
      immediately while an optional coordinated refresh is requested; explicit
      refresh waits for a current snapshot through cross-process locks.
   5. Before autorouting scores, reads cached routing-relevant fields: health,
      quota, discovery, context/tool/reasoning support, billing/effective-cost
      metadata when dynamic, and utilization when available. Stale or missing
      local-provider facts may request a coordinated asynchronous refresh, but
      route scoring must not block on local provider liveness or model
      discovery; explicit refresh/preflight surfaces own blocking refresh.
3. Expands snapshot rows into route candidates. A candidate is the concrete
   `(harness, provider source, endpoint/server instance, model)` tuple that can
   be dispatched. Harness-as-provider snapshot rows are projected back onto the
   `Harness` hard-pin axis while retaining provider/source identity for scoring
   and evidence.
4. Applies caller intent:
   - `Policy` selects policy baseline, local allowance, and hard requirements.
   - `MinPower` and `MaxPower` are score-shaping hints. They do not remove an
     otherwise eligible candidate solely for being above or below the requested
     band; undershooting is penalized more heavily than overshooting.
   - `Model` is an exact concrete model constraint. If the caller asks for
     `qwen-3.6-27b`, the router may choose among provider sources/endpoints that
     serve that model, but it must not substitute a different model.
   - `Provider` is a hard provider-source or endpoint constraint, depending on
     the request surface. `Provider=lmstudio` means only the LM Studio source is
     considered; an endpoint selector means only that endpoint is considered.
   - `Harness` is a hard harness constraint.
   - A request is unpinned when `Harness`, `Provider`, and exact `Model` are all
     empty. Policy, power, reasoning, capability, and token-estimate fields are
     routing intent, not pins. `MaxTokens` is the requested per-call output
     budget and is also routing intent, not a pin.
   - Fully pinned requests still run validation gates. They bypass comparative
     scoring only when a single candidate remains; multiple endpoints under the
     same constrained source are still ranked.
5. Filters candidates:
   1. Hard constraints remove all candidates outside requested harness,
      provider-source/endpoint, and exact-model axes. These constraints are
      never relaxed by power scoring.
   2. Policy requirements remove candidates that cannot satisfy the selected
      policy, such as `air-gapped` rejecting remote or account-billed
      candidates.
   3. Default inclusion removes default-deny providers from unpinned automatic
      routing, but explicit `Harness`, `Provider`, or exact `Model` pins can
      consider them.
   4. Metered opt-in removes pay-per-token providers from unpinned automatic
      routing unless provider default inclusion and explicit spend permission
      both allow them. In config form, `routing.allow_metered: true` is the
      operator-level spend permission. Pins can still consider metered providers,
      but policy requirements such as `no_remote` continue to apply.
   5. Auto-routability removes missing-power, inactive, deprecated,
      exact-pin-only, and catalog-unknown models from unpinned automatic routing.
      Exact model pins may still use them when the selected source can serve the
      model.
   6. Liveness/model-discovery removes endpoints that are down or do not serve
      the candidate model.
   7. Capability applies the context-capacity gate before tool and reasoning
      checks. The route request computes, with non-negative inputs and
      saturating integer addition:

      ```text
      prompt = max(EstimatedPromptTokens, 0)
      output = max(MaxTokens, 0)
      required_context = saturating_add(prompt, floor(prompt / 4), output)
      ```

      `saturating_add` caps at Go `math.MaxInt`; non-negative integer input
      overflow can never wrap to a smaller requirement.

      When `required_context > 0`, an unknown or zero `ContextLength` is
      rejected with `context_too_small`; a positive context length below the
      requirement is also rejected, while equality passes. When
      `required_context == 0`, this gate does not reject an unknown window. A
      permitted exact pin may therefore select an exact-pin-only candidate with
      raw unknown capacity, consistent with FEAT-004 requirements 23–28;
      unpinned auto-routing exclusions still apply. An output-only request
      (`EstimatedPromptTokens == 0`, `MaxTokens > 0`) remains a real capacity
      gate. `MaxTokens == 0` contributes no reserved output tokens. Exact pins
      do not bypass a positive requirement. The remaining capability checks
      remove candidates missing tool support for `RequiresTools`, unsupported
      explicit reasoning, or stale/deprecated catalog status when not
      explicitly allowed.
6. Applies sticky endpoint assignment for equivalent local/free endpoints:
   1. If the request has a live sticky route key with a valid lease, reuse that
      `(provider source, endpoint, model)` assignment before new load balancing.
   2. If no valid lease exists, use normalized endpoint utilization plus
      service-owned in-flight lease counts to prefer the least-loaded equivalent
      local endpoint.
   3. Existing sticky assignments move only when the endpoint disappears, stops
      serving the model, enters cooldown, or crosses a hard saturation threshold.
7. Scores survivors with explicit components:

   ```text
   score = power_weighted_capability
         + power_hint_fit
         + latency_weight
         + placement_bonus
         + quota_bonus
         - effective_cost_penalty
         - availability_penalty
         - stale_signal_penalty
   ```

   `power_hint_fit` applies the FEAT-004 asymmetric rule: candidates below
   `MinPower` receive a stronger penalty than candidates above `MaxPower`.
   The scorer prefers the lowest effective cost candidate whose power
   fit is sufficient for the selected policy, so a free but materially
   underpowered local route does not beat an in-band `default` candidate solely
   because it is free. Subscription candidates use PAYG-equivalent effective
   cost for comparison while retaining `actual_cash_spend=false`; they are not
   treated as zero-cost merely because dispatch does not create per-token
   billing.
8. Dispatches the top candidate exactly once. On provider/harness failure, the
   service records the attempted route outcome and returns the full ranked
   trace. It does not try the next eligible candidate and it does not widen
   power bounds inside the same request. A selected route that later lacks
   enough per-call context capacity fails on that route; execution does not
   fall through to the next ranked candidate.

The full ranked candidate trace and per-candidate score components are emitted
as part of the routing-decision event (CONTRACT-003). Operators explain a
decision through `route-status` and `fiz models`, not by reading route order in
config.

## Failure Evidence and Retry Boundary

The router does not recover by retrying. It has one selection mechanism and one
reporting mechanism:

1. **In-request selection** is service-owned. The service ranks candidates,
   dispatches the top candidate once, and returns the ordered trace.
2. **Retry and escalation** are caller-owned. The caller issues a second
   request with a higher `MinPower`, a different `MaxPower`, or different hard
   pins when its task policy says the extra cost/time is justified.

Every failed routed `Execute` returns enough structured evidence for that
caller decision:

- requested power bounds, hard constraints, and exact pins
- requested prompt estimate and output budget, the saturating
  `required_context`, each candidate's context length and source, and any
  `context_too_small` rejection
- selected candidate, rejected candidates, and filter reasons
- score components and the live/cost/quota facts used for ranking
- final failure class: `setup/config`, `no-candidate`, `provider-transient`,
  `capability`, `cancelled`, or `timeout`
- attempted route outcome for the single dispatched candidate

Hard pins do not suggest broader alternatives. If `--model qwen-3.6-27b`
cannot be satisfied, the error explains that exact constraint and the inspected
provider sources/endpoints rather than recommending an unrelated model.

## Available Model Inventory

The service exposes the joined inventory through `FizeauService.ListModels`.
The CLI exposes the operator-facing equivalent as `fiz models`; JSON output is
the contract and text output is a rendering.

Each row contains:

- identity: harness, provider source, endpoint label/base URL, model ID,
  catalog ID
- policy: power, family, provider/deployment class, deprecation status,
  auto-routable status, exact-pin-only status
- capability: context window, tool support, reasoning support, streaming and
  structured-output support when known
- economics: placement (local/free, prepaid, metered, test), effective cost,
  cost source, `actual_cash_spend`, prepaid quota remaining/reset time
- operations: health, cooldown, recent latency
- routing: power filter reasons and score components for supplied power bounds

This surface is the debugging contract for routing. If `route-status` says a
candidate lost, `fiz models --power-min <n> --json` plus the route decision
trace must show the raw facts that caused the loss.

## Key Design Decisions

**D1: Provider sources and endpoints are transport setup.** They hold endpoint
URLs, credentials, headers, and optional model hints. They are not the
canonical source of power, alias, or route-order policy.

**D2: The model catalog is a first-class layer.** The catalog is loaded from an
embedded manifest snapshot with an optional external override, and it owns
model power, exact identity aliases, deprecations, benchmark inputs,
provider/deployment class, and per-surface projections.

**D2A: Publish catalog bundles independently of binary releases.** The embedded
snapshot remains the safe default, but operators and callers can install a
newer shared manifest from a versioned published bundle via an explicit update
flow.

**D2B: Manifest v5 separates concrete models from routing policy.** Top-level
model entries carry concrete model metadata. Top-level policies define power
bounds, locality, and requirements; provider entries define source type,
default inclusion, and billing. Schema v5 has no target, alias, or ordered
candidate routing layer.

**D3: Preserve prompt preset terminology for prompts only.** The top-level
`preset` field and CLI `--preset` flag refer to system prompt presets defined
in SD-003. Routing policy uses `Policy`, numeric power hints, exact model pins,
and catalog entries, never `preset`.

**D4: Power routing replaces the removed route-table field.** Per ADR-005, the service
combines catalog power, provider/harness model inventory, placement, cost,
context, capability, liveness, and usage/quota to pick the best candidate per
request. Users do not author per-candidate route order. the removed route-table field config
is rejected as a removed legacy surface.

**D5: Power is routing intent; model/provider/harness are constraints.**
`MinPower` and `MaxPower` express desired model strength and shape scoring.
`Model`, provider source/endpoint selection, and `Harness` are hard constraints.
Routing may optimize cost and availability inside those constraints but must
fail with a detailed candidate trace when they cannot be met.

**D6: Auto-selection inputs are deterministic.** Auto-selection signals are
`EstimatedPromptTokens` plus `MaxTokens` (the saturating context-capacity gate
defined in Resolution step 5.7), `RequiresTools` (filter by tool support), and
`Reasoning` (filter by reasoning support). `CompactionContextWindow` is not a
routing input; it may only tighten the selected route's execution window. No
prose heuristic complexity classifier is used. `RequiresTools` is explicit
caller intent, or derived only when a request surface has unambiguously enabled
tool execution.

**D7: No Fizeau-owned semantic retry.** The routing engine ranks candidates with explicit
components. `Execute` dispatches the top candidate once and returns the ranked
trace plus attempted-route outcome. DDx or another caller owns any follow-up
request with a stronger `MinPower`, capped `MaxPower`, or different hard pins.
Same-route transient retries may repeat the provider call, but neither a
route-time `context_too_small` rejection nor an execution-time context-capacity
failure advances to the next candidate.
Per-(harness, provider source, endpoint, model) availability/latency replaces
coarser health memory.

**D7A: Placement is provider-candidate metadata.** The native execution path
may front local/fixed, prepaid, and metered providers. Routing placement filters
operate on the provider-source/endpoint candidate, not the harness. Default
placement comes from source type and catalog deployment class, with endpoint
override available only as metadata.

**D8: Environment variable expansion still applies to values.** `${VAR}` is
expanded at config load time. No shell evaluation.

**D9: Backwards compatible with legacy flat format during migration.** Old flat
config still maps to one endpoint under the declared provider source.
the removed route-table field parsing has been removed after its deprecation cycle. A
boundary test forbids re-introduction of that parser.

**D10: Provider limit discovery is cached and type-gated.** Explicit refresh
and background snapshot maintenance may call `LookupModelLimits` when
`context_window` or `max_tokens` are absent. Route resolution and execution read
the resulting snapshot and never synchronously probe a provider merely to fill
a limit. Explicit config values always win. Discovery is keyed by server type:

- **LM Studio** — `GET /api/v0/models/{model}`; prefers
  `loaded_context_length`
- **omlx** — `GET /v1/models/status`; returns `max_context_window` and
  `max_tokens` per model
- **OpenRouter** — `GET /api/v1/models` (public list)

Undiscoverable candidate values stay zero in the candidate trace. A positive
route requirement rejects them with `context_too_small`; a permitted exact pin
with a zero requirement may select one. Before native execution, selected
context resolution uses the candidate value when positive and otherwise uses
explicit provider config, cached provider-API evidence, catalog metadata, then
`compaction.DefaultContextWindow` with source `default`. `RouteDecision`
reports the resolved execution value and source while its candidate trace
retains the raw unknown value. A direct native path uses the same chain without
the candidate-value step.

**D11: Provider type replaces flavor heuristics for limit discovery.**
Port-based provider detection fails when servers run on non-default ports. The
explicit `type` field lets operators declare the server type. When type is
absent the system:

1. Tries URL-based detection first.
2. Fires concurrent probes to `/v1/models/status` and `/api/v0/models` with a
   3-second timeout to distinguish omlx vs LM Studio on ambiguous ports.
3. Falls back to port heuristics as a last resort.

**D12: omlx is a first-class supported provider source.** omlx is a local
inference runtime that speaks the OpenAI-compatible chat API and exposes
additional endpoints: `GET /v1/models/status` returns per-model
`max_context_window` and `max_tokens`. Set `type: omlx` to use dedicated limit
discovery and avoid probe ambiguity.

**D12A: vLLM and llama-server are first-class local provider sources with
provider-owned utilization probes.** Set `type: vllm` for a vLLM OpenAI server
and `type: llama-server` for llama.cpp `llama-server`. Their `base_url` remains
the OpenAI-compatible API base, usually `/v1`; utilization probes derive the
server root by removing a trailing `/v1`.

- **vLLM** — `GET /metrics` on the server root; normalize
  `vllm:num_requests_running`, `vllm:num_requests_waiting`, and cache pressure
  from `vllm:kv_cache_usage_perc` or legacy `vllm:gpu_cache_usage_perc`.
- **llama-server** — `GET /metrics` on the server root when the process is
  started with `--metrics`; normalize `llamacpp:requests_processing`,
  `llamacpp:requests_deferred`, and `llamacpp:kv_cache_usage_ratio`. If metrics
  are unavailable, fall back to `GET /slots` and count `is_processing` slots.

Provider utilization is not a route table and not a user-authored policy block.
It is an operational input for choosing among otherwise equivalent eligible
local endpoints.

**D12B: Sticky route leases preserve worker affinity.** A long-running worker
sequence with the same sticky route key reuses its assigned local endpoint.
New sticky keys are assigned to the least-loaded equivalent endpoint. On a
single machine, in-process leases are authoritative and provider utilization is
an advisory refinement. Across multiple Fizeau processes, correct stickiness and
fair balancing require a shared lease backend; raw server metrics alone are
sampled and racy.

**D13: Protocol capabilities are type-keyed and conservative.** The provider
exposes `SupportsTools()`, `SupportsStream()`, and
`SupportsStructuredOutput()` accessors that return the effective capability for
the resolved type. Downstream routing consults these before dispatch to avoid
dispatch-and-fail on mismatched prompts. Unknown types return `false` for all
protocol flags so routing rejects rather than dispatches. This surface is
distinct from benchmark-based capability scoring.

**RequiresTools filter scope.** `RequiresTools=true` filters candidates at the
`(harness, provider source, endpoint, model)` level via an OR-permissive gate:
a candidate passes when either `routing.HarnessEntry.SupportsTools` or
`routing.ProviderEntry.SupportsTools` is `true`, and the catalog's per-model
override is not set.

**D14: `DetectedType()` layers on top of `providerSystem` without replacing
it.** `providerSystem` remains the source of truth for per-response telemetry
and cost attribution because those fire on every response and cannot afford a
network probe. `DetectedType()` is the probe-confirmed accessor used for
pre-dispatch gating. It runs the probe at most once per provider via
`sync.Once`, caches the result, and falls back to `providerSystem` when the
probe is inconclusive.

**D15: `reasoning` is the public model-reasoning control.** The public surface
uses one scalar (`reasoning`) for named and numeric values. Config uses
`reasoning`; catalog metadata uses `reasoning_default`; the CLI uses
`--reasoning`. Provider and harness adapters may translate the resolved value
to wire or subprocess knobs named `thinking`, `effort`, `variant`, or numeric
budgets, but those names are not preferred public controls. Unsupported
auto/default controls may be dropped; explicit unsupported or over-limit
values fail clearly.

**D16: Provider model listing is public and endpoint-aware.** `FizeauService.ListModels`
is the public service surface consumers use to list configured
provider-backed models. For OpenRouter, LM Studio, and oMLX, the service
queries each configured endpoint's `<base_url>/models` endpoint and returns one
result per discovered model per endpoint. Source type and endpoint identity are
explicit `ModelInfo` fields so consumers do not read provider config or infer
type from URLs. Endpoint failures are local to that endpoint during listing;
status diagnostics remain in `ListProviders` and `HealthCheck`.

**D17: Provider observability cassettes come from real servers.** vLLM and
llama-server provider compatibility tests use the established `go-vcr` library.
Replay mode is the default test path. Record mode is opt-in and owns the full
server lifecycle: install or pull the runtime, start a trivial CPU model on
temporary ports, wait for readiness, record `/v1/models`, provider
observability endpoints, minimal chat, and under-load utilization evidence, then
stop the server. The acceptance path must not require a developer to manually
start servers.

## CLI UX

### Prompt Preset Selection

The `--preset` flag (or `preset` in config) selects the system prompt style.
Built-in preset details are defined by SD-003 and implemented in
`prompt/presets.go`. This surface is intentionally unrelated to routing.

### Direct Source / Model Selection

```bash
fiz run --provider lmstudio "prompt"
fiz run --provider anthropic --model opus-4.7 "prompt"
fiz run --model qwen-3.6-27b "prompt"
fiz run --provider lmstudio --reasoning 8192 "prompt"
```

The public CLI flag is `--reasoning <value>`. Do not introduce alternate public
reasoning flags.

### Power-Routed Selection

```bash
fiz run --model qwen3.5-27b "prompt"  # pin a concrete model
fiz run --min-power 5 "prompt"        # request stronger automatic candidates
fiz run --min-power 8 "prompt"        # retry with a stronger floor
fiz run "prompt"                      # automatic routing over eligible candidates
fiz models --json                     # inspect joined inventory
```

Compatibility:

```bash
fiz -p "prompt" --backend code-work-local
```

The compatibility flag remains temporarily, but it is not the preferred UX.

## Library and Package Boundaries

The public runtime boundary is `FizeauService.Execute`. It resolves one route,
constructs the selected native provider or harness internally, dispatches once,
and emits normalized public events. `internal/core.Run` and its single-provider
request are implementation details, not an embedding API.

Package split:

- `internal/config/` — load provider source/endpoint config, routing defaults,
  and optional manifest override path
- `internal/modelcatalog/` — load, validate, and resolve shared model policy
- `internal/reasoning/` — shared leaf package for the Reasoning scalar,
  parser, normalization, constants, `ReasoningTokens(n)`, and resolved policy
  representation
- root `fizeau` package — public service, request, event, inventory, and route
  projections
- `internal/serviceimpl/` — service execution and native/subprocess dispatch
- `cmd/fiz/` and `agentcli/` — thin consumers that translate CLI flags into
  public request intent and expose `fiz models`

## Traceability

- FEAT-004 defines the ownership split and terminology.
- ADR-005 defines the power-routing decision model and retry boundary.
- CONTRACT-003 defines the public service surface and routing attribution
  events.
- SD-003 reserves `preset` for system prompt behavior.
- `plan-2026-04-08-shared-model-catalog.md` defines the catalog package/API,
  manifest format, and consumer examples.
- `plan-2026-04-10-catalog-distribution-and-refresh.md` defines published
  manifest bundles, explicit update flow, and the initial reasoning baseline.
- D10-D12 define provider limit discovery, flavor detection, and omlx support;
  provider adapters implement the probes, while refresh and snapshot ownership
  keep those probes off the route and execution hot paths.
- D15 (reasoning contract) is implemented through `reasoning`,
  `reasoning_default`, and CLI `--reasoning`.
- D16 (endpoint-aware provider model listing) is implemented through
  `FizeauService.ListModels` and the exported `ModelInfo` provider/endpoint fields.
- D12A-D12B and D17 govern local endpoint utilization, sticky route leases, and
  real-server VCR acceptance for vLLM and llama-server.
