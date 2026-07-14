# CONTRACT-001: OTel GenAI Telemetry Capture Contract

**Status:** Draft  
**Owner:** Fizeau maintainers  
**Related:** [ADR-001](../adr/ADR-001-observability-surfaces-and-cost-attribution.md), [FEAT-005](../../01-frame/features/FEAT-005-logging-and-cost.md), [SD-001](../solution-designs/SD-001-agent-core.md)

## Purpose

This contract defines the OpenTelemetry trace and metric surface that a
DDX-compatible coding harness should emit so one analytics tool can consume
telemetry from multiple harnesses without per-harness schema logic.

This is a telemetry contract, not a replay contract.

- **Telemetry contract**: standard analytics surface for traces and metrics
- **Replay artifact**: harness-specific local transcript or session log

For Fizeau, JSONL session logs remain the replay artifact and this contract
defines the analytics surface emitted alongside them.

## Scope Boundary

This contract covers:
- required span taxonomy for agent runs, LLM calls, and tool execution
- correlation rules across runs, conversations, turns, and retries
- required OpenTelemetry GenAI attributes
- required DDX extension attributes for gaps not covered by OTel
- cost attribution fields and precedence rules
- timing fields and throughput derivation rules
- default content-capture policy and opt-in payload rules
- minimum metric emission rules for analytics consumers

This contract does **not** define:
- local replay file formats such as JSONL transcripts
- collector topology, exporters, or backend storage products
- project-specific dashboards
- provider SDK internals
- budget policy or runtime throttling behavior

## Conformance Goal

A harness conforms to this contract when another team can implement analytics
for that harness and compare it with other conforming harnesses without
requiring harness-specific schema guesses.

## Required Span Taxonomy

### 1. Root run span

Every bounded harness run emits one root span representing the run itself.

- **Span name**: `invoke_agent <harness-name>`
- **Required operation**: `gen_ai.operation.name=invoke_agent`
- **Required meaning**: one top-level harness run or autonomous sub-run

This span is the rollup parent for all LLM and tool spans emitted by that run.

### 2. LLM call spans

Every outbound provider call attempt emits one child span.

- **Span name**: `chat <request-model>`
- **Required operation**: `gen_ai.operation.name=chat`
- **Required meaning**: one actual provider/gateway request attempt

Retries are separate spans, not hidden inside one aggregated span.

### 3. Tool execution spans

Every tool execution requested by the model emits one child span.

- **Span name**: `execute_tool <tool-name>`
- **Required operation**: `gen_ai.operation.name=execute_tool`
- **Required meaning**: one tool execution attempt

### 4. Optional sub-spans

Harnesses MAY emit additional internal spans for provider translation,
streaming assembly, sandbox startup, or transport details, but those spans are
not part of the required cross-harness analytics contract.

## Span Relationships

### Trace boundary

- One top-level harness run SHOULD produce one trace.
- All required spans for that run MUST share the same trace ID.
- A child autonomous worker or subagent MAY use a new trace if it is an
  independent run, but it MUST preserve conversation linkage using the fields
  in this contract.

### Parent-child rules

- The root `invoke_agent` span is the direct or indirect parent of every
  `chat` and `execute_tool` span in the run.
- A `chat` span and a following `execute_tool` span MAY be siblings under the
  root span or the tool span MAY be nested under an agent/framework span, but
  the harness MUST preserve the logical turn linkage via attributes.
- Analytics consumers MUST rely on the required identity fields below, not
  parentage alone, to correlate turns and retries.

## Identity and Correlation Contract

### Required DDX identity fields

These attributes are required on the root span and SHOULD be copied to all
required child spans unless stated otherwise.

| Attribute | Type | Requirement | Meaning |
|---|---|---|---|
| `ddx.harness.name` | string | Required | Logical harness name such as `fiz`, `claude-code`, `codex-cli` |
| `ddx.harness.version` | string | Recommended | Harness version or build identifier |
| `ddx.session.id` | string | Required | Run-local session identifier for the current harness run |
| `ddx.parent.session.id` | string | Optional | Parent run/session when this run continues or forks from another run |
| `ddx.turn.index` | int | Required on `chat` and `execute_tool` | Logical turn number within the run, starting at `1` |
| `ddx.attempt.index` | int | Required on `chat` | Attempt number within the logical turn, starting at `1` |
| `ddx.tool.execution.index` | int | Required on `execute_tool` | 1-based position of the tool execution within the logical turn |

### Conversation identity

- `gen_ai.conversation.id` SHOULD be populated whenever the harness has stable
  conversation/session continuity beyond a single run.
- If the harness has no broader conversation concept, it SHOULD set
  `gen_ai.conversation.id = ddx.session.id`.
- A resumed session, carried-forward history run, or subagent branch MUST NOT
  invent a new unrelated conversation ID if the broader conversation is the
  same.

### Agent identity

The root span SHOULD also populate:

- `gen_ai.agent.name` when the harness exposes a stable agent identity
- `gen_ai.agent.version` when available
- `gen_ai.agent.id` only when the underlying system exposes one

## Provider and Model Identity

### Standard OTel fields

Every `chat` span MUST populate, when available:

- `gen_ai.provider.name`
- `gen_ai.request.model`
- `gen_ai.response.model`
- `server.address`
- `server.port`

### Provider identity rules

- `gen_ai.provider.name` MUST follow the OTel provider naming model and reflect
  the instrumentation's best knowledge of the provider family or API surface.
- `gen_ai.provider.name` is not required to equal the real billing/runtime
  system.

### DDX runtime identity extension

Every `chat` span MUST populate:

| Attribute | Type | Meaning |
|---|---|---|
| `ddx.provider.system` | string | Actual execution or billing system such as `openai`, `openrouter`, `anthropic`, `lmstudio`, `ollama`, `bragi`, `vidar`, `grendel` |
| `ddx.provider.route` | string | Optional routing label or preset used to choose the provider/model |
| `ddx.provider.model_resolved` | string | Optional fully resolved routed model identifier if it differs from request alias |

The combination of `gen_ai.provider.name`, `ddx.provider.system`,
`gen_ai.request.model`, `gen_ai.response.model`, and `server.address` is the
minimum analytics identity surface for comparing providers across harnesses.

## Required OTel GenAI Usage Fields

Every successful or partially successful `chat` span MUST populate token usage
reported by the provider when those values are available:

- `gen_ai.usage.input_tokens`
- `gen_ai.usage.output_tokens`
- `gen_ai.usage.cache_read.input_tokens`
- `gen_ai.usage.cache_creation.input_tokens`

If the provider does not expose a value, the attribute MUST be omitted rather
than guessed.

## Cost Attribution Contract

### Cost precedence

Cost attribution follows this precedence order:

1. provider-reported billed cost
2. gateway-reported billed cost
3. explicit configured pricing for the exact `ddx.provider.system` and resolved
   model
4. unknown cost

Generic stale pricing tables MUST NOT be used as a fallback.

### Cost fields

Every `chat` span MUST populate:

| Attribute | Type | Requirement | Meaning |
|---|---|---|---|
| `ddx.cost.source` | string enum | Required | `provider_reported`, `gateway_reported`, `configured`, or `unknown` |
| `ddx.cost.currency` | string | Required when amount is known | ISO 4217 currency code, currently expected to be `USD` |
| `ddx.cost.amount` | double | Required when amount is known | Total billed or configured amount for the span |

Optional detailed cost fields:

- `ddx.cost.input_amount`
- `ddx.cost.output_amount`
- `ddx.cost.cache_read_amount`
- `ddx.cost.cache_write_amount`
- `ddx.cost.reasoning_amount`
- `ddx.cost.pricing_ref`
- `ddx.cost.raw`

If `ddx.cost.source=unknown`, `ddx.cost.amount` MUST be omitted.

### Root-span cost rollup

The root `invoke_agent` span SHOULD populate the same cost fields as a session
rollup only when every contributing `chat` span has known cost.

If every contributing `chat` span has known cost but the provenance differs
across turns, the root span MUST still preserve `ddx.cost.amount` as the sum of
the known child costs. In that mixed-provenance case the root span MUST NOT
invent a synthetic `ddx.cost.source` or `ddx.cost.pricing_ref`; those fields
MUST be omitted unless the rollup can be represented by a single, coherent
provenance shape. `ddx.cost.currency` MAY be emitted only when it is unambiguous
for the entire rollup.

If any contributing `chat` span has unknown cost:

- root `ddx.cost.source` MUST be `unknown`
- root `ddx.cost.amount` MUST be omitted
- analytics SHOULD roll up known and unknown child counts separately

## Timing Contract

### Base timing

Span start and end timestamps are authoritative for:

- request start time
- request completion time
- total span duration

Harnesses MUST NOT duplicate these timestamps in custom attributes.

### DDX timing availability and extensions

Every `chat` span MUST make timing availability observable. A consumer must be
able to distinguish a measured timing window from an unavailable one without
inferring meaning from a missing number.

| Attribute | Type | Requirement | Meaning |
|---|---|---|---|
| `ddx.timing.availability` | string enum | Required | `available`, `partial`, or `unknown` |
| `ddx.timing.source` | string enum | Required | `provider_reported`, `gateway_reported`, `harness_measured`, `mixed`, or `unknown` |
| `ddx.timing.available_fields` | string array | Required when availability is `available` or `partial` | Names of the numeric `ddx.timing.*` windows carried by the span |

`available` means every timing window the provider or harness claims for that
request mode is present. `partial` means at least one timing window is present
but other applicable windows are unavailable. `unknown` means no phase timing
is available beyond the authoritative span start/end timestamps; in that case
`ddx.timing.source` MUST be `unknown`, `ddx.timing.available_fields` MUST be
omitted or empty, and numeric timing attributes MUST be omitted.

When timing is available from more than one source,
`ddx.timing.source=mixed`. A harness-derived value is permitted only when it is
measured from observable request/stream boundaries; it must not be estimated
from token counts or model characteristics.

Numeric timing attributes use milliseconds:

| Attribute | Type | Meaning |
|---|---|---|
| `ddx.timing.first_token_ms` | double | Milliseconds from span start to first streamed token |
| `ddx.timing.queue_ms` | double | Time spent queued before provider processing |
| `ddx.timing.prefill_ms` | double | Prompt/prefill processing duration excluding generation |
| `ddx.timing.generation_ms` | double | Output generation window |
| `ddx.timing.cache_read_ms` | double | Cache read processing window |
| `ddx.timing.cache_write_ms` | double | Cache write processing window |

An unavailable numeric window MUST be omitted, never encoded as zero. Each
present numeric window MUST appear by its suffix in
`ddx.timing.available_fields` (for example, `first_token_ms`). Zero is valid
only when the source explicitly reports a real zero-duration window.

## Throughput Derivation Rules

Throughput is an analytics computation over traces. Harnesses MAY emit derived
metrics, but if they do, they MUST use these formulas.

### Output throughput

`output_tok_per_s` is defined as:

1. `gen_ai.usage.output_tokens / (ddx.timing.generation_ms / 1000)` when
   `ddx.timing.generation_ms` exists and is greater than zero
2. otherwise, if `ddx.timing.first_token_ms` exists,  
   `gen_ai.usage.output_tokens / ((span_duration_ms - ddx.timing.first_token_ms) / 1000)`
3. otherwise, not available

### Cached input throughput

`cached_tok_per_s` is defined as:

`gen_ai.usage.cache_read.input_tokens / (ddx.timing.cache_read_ms / 1000)`

only when both values exist and `ddx.timing.cache_read_ms > 0`.

### Non-cached input throughput

`input_tok_per_s` is defined as:

`(gen_ai.usage.input_tokens - gen_ai.usage.cache_read.input_tokens) / (ddx.timing.prefill_ms / 1000)`

only when:

- `gen_ai.usage.input_tokens` exists
- `ddx.timing.prefill_ms` exists and is greater than zero

If cache-read tokens are absent, analytics MAY assume zero cache-read tokens
for this formula only when the provider exposes `prefill_ms` and no cache-read
field at all.

No other throughput formula is conformant.

## Tool Execution Contract

Every `execute_tool` span MUST populate:

- `gen_ai.tool.name`
- `gen_ai.tool.type`
- `gen_ai.tool.call.id` when the model or harness provides a stable call ID
- `ddx.turn.index`
- `ddx.tool.execution.index`

Optional fields:

- `gen_ai.tool.call.arguments`
- `gen_ai.tool.call.result`

### Tool success and failure

- Success MUST follow standard OTel error semantics: span status unset, no
  `error.type`
- Failure MUST set span status to `Error` and populate `error.type`

If the tool returns a structured failure that the harness treats as a failed
execution, the span MUST still be marked as an error.

## Error and Retry Contract

### Standard error handling

All spans in this contract MUST follow OTel recording-errors guidance.

- On failure, set span status to `Error`
- On failure, set `error.type`
- `error.type` SHOULD be a low-cardinality identifier

### Retry semantics

- Each actual provider request retry is a separate `chat` span
- All retry spans for the same logical turn MUST share the same
  `ddx.turn.index`
- Retry spans MUST increment `ddx.attempt.index`
- Token usage and known cost belong to the specific attempt span that incurred
  them and MUST NOT be collapsed silently into another span

## Content Capture Policy

### Default policy

By default, conforming harnesses SHOULD NOT record full prompt bodies,
responses, tool arguments, or tool results on span attributes.

This keeps the analytics contract portable and avoids leaking large or
sensitive payloads by default.

### Opt-in structured capture

When a harness is configured to capture content on spans or events, it MUST
use the standard OTel fields and schemas:

- `gen_ai.system_instructions`
- `gen_ai.input.messages`
- `gen_ai.output.messages`
- `gen_ai.tool.call.arguments`
- `gen_ai.tool.call.result`

### Externalized content references

If a harness stores large content externally, it SHOULD record opaque
references using:

- `ddx.content.ref.system`
- `ddx.content.ref.input`
- `ddx.content.ref.output`
- `ddx.content.ref.tool.arguments`
- `ddx.content.ref.tool.result`

Each value SHOULD be an opaque URI or stable handle. Analytics consumers MUST
not assume any specific storage backend.

## Required Metrics

Conforming harnesses SHOULD emit the standard OTel GenAI metrics for each
completed `chat` span when their OTel stack supports them:

- `gen_ai.client.operation.duration`
- `gen_ai.client.token.usage`

Metric dimensions SHOULD include, when available:

- `gen_ai.operation.name`
- `gen_ai.provider.name`
- `gen_ai.request.model`
- `gen_ai.response.model`
- `error.type`
- `server.address`

If a failed attempt reports token usage, that usage MUST be attributed to the
failed attempt rather than dropped.

## Compatibility Rules

### If this contract and OTel diverge

- OTel standard fields win for standard attributes and semantics
- this contract defines behavior only for:
  - DDX extension attributes
  - required mappings where OTel allows multiple valid shapes
  - interoperability rules not fully specified by OTel

### If this contract and a project-specific design diverge

This contract is authoritative for telemetry field names, formulas, and
capture semantics.

## Validation Checklist

A harness implementation is conformant when all of the following are true:

- [ ] one root `invoke_agent` span exists per bounded run
- [ ] every provider request attempt emits its own `chat` span
- [ ] every tool execution emits its own `execute_tool` span
- [ ] `ddx.harness.name`, `ddx.session.id`, and turn/attempt indices are present where required
- [ ] `gen_ai.provider.name`, `ddx.provider.system`, and model fields are populated when known
- [ ] known cost uses `ddx.cost.*` fields exactly as defined here
- [ ] mixed known-cost provenance preserves the root amount and omits unrepresentable provenance fields
- [ ] unknown cost is explicit and does not emit a guessed amount
- [ ] every `chat` span reports timing availability and source, even when both are unknown
- [ ] each present timing value is named in `ddx.timing.available_fields`; unavailable values are omitted rather than written as zero
- [ ] failed spans use standard OTel error semantics
- [ ] throughput is derived only from the formulas in this contract
- [ ] content is not captured by default, and opt-in capture uses standard OTel payload fields

## References

- [ADR-001: Observability Surfaces and Cost Attribution](../adr/ADR-001-observability-surfaces-and-cost-attribution.md)
- [FEAT-005: Logging, Replay, and Cost Tracking](../../01-frame/features/FEAT-005-logging-and-cost.md)
- [SD-001: Agent Core](../solution-designs/SD-001-agent-core.md)
- [OpenTelemetry GenAI Spans](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/)
- [OpenTelemetry GenAI Agent Spans](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-agent-spans/)
- [OpenTelemetry GenAI Metrics](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/)
- [OpenTelemetry Recording Errors](https://opentelemetry.io/docs/specs/semconv/general/recording-errors/)
