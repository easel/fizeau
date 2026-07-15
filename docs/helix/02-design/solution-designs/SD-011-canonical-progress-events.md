---
ddx:
  id: SD-011
  depends_on:
    - FEAT-005
    - CONTRACT-003
    - CONTRACT-004
    - SD-006
    - ADR-008
  review:
    self_hash: 3fee0eeae9b07811de5ebd2c630ef88f21f003bc4203b8e14026727491b1cb08
    deps:
      ADR-008: 3f36c9ae5997a72d2575876d739d110a7dd6950456a517695ed0d0cd8e118db3
      CONTRACT-003: 50cbc8709ce89d676bd10df9ba3d635089cb474823dbc10a468e2f7ecd72cf31
      CONTRACT-004: 0e19d06f34a0697f0f46fde18a66b4f66f074f840307978ffe3d66a0dff27c0e
      FEAT-005: 0a963abf9f30cb7551a30302fa853525e417f03cd1611603aec221d0159998e0
      SD-006: bd9f4cf464dbad08e003533906b67eb25735384eac4d522e367adccc9a3a7db6
    reviewed_at: "2026-07-15T18:54:31Z"
---
# Solution Design: SD-011 — Canonical Progress Events

**Requirement**: FEAT-005 session logging and replay; CONTRACT-003 service
events; ADR-008 transcript/progress ownership.

## Problem

Long-running DDx and Fizeau executions need compact, useful progress output
that carries enough structure to explain what is happening without each
consumer reverse-engineering provider-specific streams. Recent operator output
shows the failure mode clearly: lines such as `sed -n 240` are clean but lack
intent, target, turn number, output size, timing, and throughput. DDx then grows
formatter-side heuristics, while Claude, Codex, native, Gemini, Pi, and
Opencode paths can drift into separate special cases.

ADR-008 already assigns transcript and progress semantics to Fizeau. This
design specifies the implementation contract that makes that decision
actionable: all execution paths project through one Fizeau-owned canonical
event boundary; loggers persist that shape; formatters render it without
parsing harness-native events when canonical events exist. Native provider and
subprocess streams remain input evidence, not an authoritative consumer
surface.

## Goals

- Provide one canonical progress event schema for native and subprocess
  harnesses.
- Preserve compact human progress lines while making action, target, turn,
  timing, throughput, and output summary structured fields.
- Keep provider and harness wrappers thin: parse native records and pass
  normalized evidence into the Fizeau-owned event projection.
- Keep loggers as subscribers: write canonical events without embedding display
  policy.
- Keep formatters presentation-only for new logs, with isolated legacy
  normalization for historical records.
- Support corpus tests from Claude, Codex, native, and secondary harness logs.

## Non-Goals

- No change to routing policy, model selection, quota scoring, or retry policy.
- No requirement that old session logs be rewritten.
- No raw prompt, raw tool output, or unbounded transcript text in progress
  events.
- No DDx parsing of harness-native streams once a Fizeau canonical progress
  event is available.
- No harness-owned capacity calculation, public event construction, terminal
  classification, or next-candidate retry.

## Canonical Event Schema

Fizeau owns a canonical progress event type. The public service boundary may
continue to expose it as `ServiceEvent{Type: "progress", Data:
ServiceProgressData}` or an additive successor, but the fields below are the
stable contract.

```go
type ProgressEvent struct {
    Type      ProgressType `json:"type"`
    Source    string       `json:"source,omitempty"`
    TaskID    string       `json:"task_id,omitempty"`
    TurnIndex int          `json:"turn_index,omitempty"`
    Phase     string       `json:"phase,omitempty"`
    Status    string       `json:"status,omitempty"`
    Message   string       `json:"message,omitempty"`
    Action    string       `json:"action,omitempty"`
    Target    string       `json:"target,omitempty"`
    Tool      ToolProgress `json:"tool,omitempty"`
    LLM       LLMProgress  `json:"llm,omitempty"`
    Timing    Timing      `json:"timing,omitempty"`
    Usage     Usage       `json:"usage,omitempty"`
    Output    Output      `json:"output,omitempty"`
}

type ToolProgress struct {
    Name      string         `json:"name,omitempty"`
    CallID    string         `json:"call_id,omitempty"`
    Input     map[string]any `json:"input,omitempty"`
    ExitCode  *int           `json:"exit_code,omitempty"`
    Error     string         `json:"error,omitempty"`
}

type LLMProgress struct {
    Provider string `json:"provider,omitempty"`
    Model    string `json:"model,omitempty"`
}

type Timing struct {
    DurationMS int64   `json:"duration_ms,omitempty"`
    TokPerSec  float64 `json:"tok_per_sec,omitempty"`
}

type Usage struct {
    InputTokens      int `json:"input_tokens,omitempty"`
    OutputTokens     int `json:"output_tokens,omitempty"`
    CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
    CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
    TotalTokens      int `json:"total_tokens,omitempty"`
}

type Output struct {
    Bytes   int    `json:"bytes,omitempty"`
    Lines   int    `json:"lines,omitempty"`
    Excerpt string `json:"excerpt,omitempty"`
}
```

Retry accounting is not a fifth token stream. A benchmark may derive a
separate retried-input counter from attempt records, but canonical runtime
progress preserves the four FEAT-005 streams.

`turn_index` is the canonical turn counter. New code must not introduce
parallel fields such as `turn` or `round` except in legacy normalization.

`message` remains the canonical compact human line for consumers that only
display text. Normal progress messages should fit within 80 characters; tool
command lines may use up to 120 characters when preserving the target basename
or recognizable command materially improves debugging.

## Progress Sink Boundary

Execution paths receive or construct a single callback:

```go
type ProgressCallback func(ProgressEvent)
```

Provider and subprocess wrappers may parse provider-native records, but they
must not open progress log files, truncate paths for display, compute terminal
rendering, or call formatter helpers. Their job is:

1. Decode the native stream record.
2. Map provider-specific identifiers into API-neutral execution evidence.
3. Deliver that evidence to service-owned transcript/progress projection.

Fizeau service execution owns callback composition. The logger, live
subscriber, session projection, and replay surface subscribe behind that
boundary. A wrapper cannot make its native event stream canonical merely by
matching the public JSON shape.

## `context_capacity` Service Event

`context_capacity` is a sibling Fizeau service event, not a
`ProgressEvent.Type` value and not a harness-native event. CONTRACT-003 owns the
exact public payload and CONTRACT-004 owns the API-neutral internal bridge:

```text
core.EventContextCapacity + primitive ContextCapacityEventData
    -> internal/serviceimpl exhaustive mapping
    -> internal/harnesses.EventTypeContextCapacity + ContextCapacityData
    -> root exhaustive mapping
    -> ServiceEventTypeContextCapacity + ServiceContextCapacityData
```

Core owns capacity estimation and decisions. Subprocess/native wrappers do not
calculate, synthesize, reorder, or terminalize this event. The root facade owns
the public event, decoded-event field, session-log projection, replay
projection, and final capacity payload.

Ordering is part of the canonical stream:

- `clamped` immediately precedes the corresponding `llm.request`; the request
  reports the same effective maximum sent to the provider.
- `planning_skipped` emits no clamp, request, response, or planning-turn event
  for that prevented call; main execution continues.
- `rejected` emits no clamp or request, precedes `session.end` and the public
  final, and terminates as
  `failed / context_capacity_exceeded / tool_loop`. Fizeau does not dispatch a
  next-ranked route.

Loggers and replay preserve this order. Progress formatters may render the
service event but MUST NOT infer it from prompt size, provider error text, or a
harness-native stream.

## Summarization and Redaction

Action, target, and output summaries live in the transcript/progress package,
not in individual wrappers or DDx formatters.

Required helpers:

```go
SummarizeToolCall(toolName string, input map[string]any) ToolSummary
SummarizeOutput(raw string) Output
SummarizeLLMResponse(usage Usage, timing Timing) LLMProgress
RedactProgressOutput(raw string) string
```

`SummarizeToolCall` must preserve meaningful targets. For a path such as
`cli/internal/agent/session_log_format.go`, the basename must remain visible
even if parent directories are compacted.

`SummarizeOutput` records byte and line counts plus a bounded excerpt. The
excerpt is produced only after redaction. It must never include the full raw
output when the output is long.

## Logger Contract

The logger writes Fizeau-owned canonical events as structured JSONL. It does
not infer action, target, turn, output excerpts, or capacity decisions from raw
provider payloads.

Example:

```json
{
  "type": "tool.complete",
  "source": "codex",
  "task_id": "ddx-1234",
  "turn_index": 22,
  "action": "add test implementation",
  "target": "cli/internal/file.go",
  "timing": {"duration_ms": 812},
  "output": {"bytes": 2480, "lines": 38, "excerpt": "ok cli/internal/file.go"}
}
```

## Formatter Contract

Formatters prefer canonical events. They may normalize historical records, but
legacy normalization must be isolated from the canonical renderer.

```go
NormalizeLegacyEvent(raw map[string]any) (ProgressEvent, bool)
FormatProgressEvent(event ProgressEvent) string
```

Expected compact output:

```text
▶ ddx-1234 22 add test implementation to cli/internal/file.go
✓ ddx-1234 22 add test implementation to cli/internal/file.go · 812ms · 38 lines
✓ ddx-1234 23 response · 4.2s · 1,284 tok · 305 tok/s
```

Formatter tests should assert that important basenames survive compaction and
that useful lines do not chase arbitrary sub-40-character limits. The practical
target is 72-80 characters for normal lines, with the 120-character exception
above.

## Legacy Compatibility

Historical logs can contain `turn`, `round`, harness-native Claude stream JSON,
or formatter-specific fields such as `output_excerpt`. Compatibility code may
map those records into `ProgressEvent`, but new capture paths must not depend
on compatibility helpers.

DDx may keep a compatibility parser for old logs. New DDx worker output must
prefer Fizeau canonical progress events when present and treat harness-native
records as fallback-only.

The v0.15 `context_capacity` event, terminal cause, decoded-event field, and
JSON payload fields are additive. Consumers preserve unknown event and enum
values and may ignore non-terminal capacity telemetry. The corresponding
fields added to exported Go structs are source-breaking for external unkeyed
composite literals; the v0.15 migration requires keyed literals, as defined by
CONTRACT-003. Legacy normalization does not fabricate capacity events for logs
that lack authoritative Fizeau evidence.

## Implementation Plan

### Dependency Graph

```text
SD-011 design
    ↓
Canonical Fizeau progress schema + sink boundary
    ↓
Summarization/redaction helpers
    ↓
Native path migration
    ↓
Subprocess harness migration
    ↓
Conformance/corpus tests
    ↓
Fizeau release
    ↓
DDx canonical consumer + legacy formatter isolation
```

### Issue Breakdown

1. **Design: canonical progress event contract**
   - Add this solution design and link implementation beads to `SD-011`.

2. **Fizeau schema and callback boundary**
   - Add canonical progress event fields and a single progress sink/callback
     boundary.
   - Add the CONTRACT-003/CONTRACT-004 context-capacity projection without
     turning it into a progress or harness-native event.
   - Preserve JSON/event compatibility through additive fields and ship Go
     struct additions under the v0.15 keyed-literal migration.

3. **Fizeau summarization and redaction**
   - Move action, target, output excerpt, byte/line count, timing, and
     throughput helpers into the transcript/progress package.
   - Add table tests for `sed`, `rg`, `git`, test commands, long paths,
     long output, and sensitive output.

4. **Fizeau native migration**
   - Make native execution emit canonical progress events for LLM turns, tool
     calls/results, output summaries, timing, and throughput.

5. **Fizeau subprocess harness migration**
   - Make Claude, Codex, Gemini, Pi, and Opencode wrappers call the shared
     sink with canonical events.
   - Remove per-wrapper progress formatting and output-excerpt logic.

6. **Fizeau conformance corpus**
   - Add fixture-backed conformance tests across native, Claude, Codex, and
     secondary harness paths without live provider access.
   - Prove exhaustive core -> internal/harnesses -> root capacity mapping and
     clamp/skip/reject order, including no next-candidate dispatch.

7. **DDx canonical consumer**
   - Prefer Fizeau canonical progress records for worker logs and live output.
   - Do not parse harness-native streams when canonical records exist.

8. **DDx legacy formatter isolation**
   - Split legacy normalization from canonical formatting.
   - Remove any Claude capture dependency on formatter helpers.

9. **DDx formatter corpus**
   - Add golden tests for Claude, Codex, native, `<out lines>`, long paths,
     turn counter, timing, and `tok/sec`.

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| Compatibility fields proliferate | M | M | Keep legacy normalization isolated and forbid new `turn`/`round` fields |
| Wrapper migration changes event order | M | H | Add conformance tests for event order and tool call/result pairing |
| Capacity events become harness-derived or progress-only | M | H | Enforce exhaustive service-owned projection and capacity ordering fixtures |
| Output excerpts leak sensitive data | M | H | Redact before excerpting and add sensitive-pattern tests |
| DDx and Fizeau implementations drift | M | H | DDx beads depend on a Fizeau release that contains this contract |
| Lines become too terse again | M | M | Test practical 72-80 character display and preserve basenames |

## Verification

- `go test ./...` in Fizeau passes.
- Fizeau conformance tests cover native, Claude, Codex, Gemini, Pi, and
  Opencode fixtures or fakes.
- DDx `cli/internal/agent` tests cover canonical formatting and legacy
  normalization separately.
- Golden samples include turn numbers 21, 22, and 23 so the operator-visible
  counter is maintained.
- Golden samples include LLM timing and token usage so `tok/sec` appears when
  calculable and is omitted when the timing window is absent.
- Capacity fixtures cover complete payload projection and ordering: clamp
  immediately before request, planning skip without request, and rejection
  before terminal without next-route dispatch.
- Compatibility fixtures preserve unknown additive event/enum values and
  require keyed external literals for v0.15 exported-struct additions.

## Shell-Runner Progress Events

See [SD-011-addendum-shell-runner-events](./SD-011-addendum-shell-runner-events.md)
for how the shell-based benchmark runner (`./benchmark`) emits progress events
within this canonical framework. The addendum specifies:

- The progress event stream shape and JSON schema for shell orchestration.
- Event taxonomy for sweep lifecycle, resume/skip, cell execution, retry, budget,
  and interrupt.
- How shell-runner events relate to canonical ProgressEvent (constraints,
  extensions, fallback reconstruction).
- Collector and formatter contracts for consuming progress.jsonl files.
