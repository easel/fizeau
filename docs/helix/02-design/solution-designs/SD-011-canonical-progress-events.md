---
ddx:
  id: SD-011
  depends_on:
    - FEAT-005
    - CONTRACT-003
    - ADR-008
  review:
    self_hash: be2debae8cab7ad3d66b0123c6ef7f240abaf9b4e99129288d4c203b96d1ebd2
    deps:
      ADR-008: 3f36c9ae5997a72d2575876d739d110a7dd6950456a517695ed0d0cd8e118db3
      CONTRACT-003: f51d48b2ea45cfb485b308be753d40b932bc2344aed8d03775ea0f1943827d9b
      FEAT-005: 0a963abf9f30cb7551a30302fa853525e417f03cd1611603aec221d0159998e0
    reviewed_at: "2026-07-15T05:49:26Z"
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
actionable: all execution paths emit the same canonical progress event shape
through one callback/sink boundary; loggers persist that shape; formatters
render it without parsing harness-native events when canonical events exist.

## Goals

- Provide one canonical progress event schema for native and subprocess
  harnesses.
- Preserve compact human progress lines while making action, target, turn,
  timing, throughput, and output summary structured fields.
- Keep provider wrappers thin: parse native stream records, build canonical
  events, and call the progress sink.
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
2. Map provider-specific identifiers onto the canonical fields.
3. Call `ProgressCallback`.

Fizeau service execution owns callback composition. The logger, live
subscriber, session projection, and replay surface subscribe behind that
boundary.

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

The logger writes canonical progress events as structured JSONL. It does not
infer action, target, turn, or output excerpts from raw provider payloads.

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
   - Preserve existing public event compatibility through additive fields.

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
