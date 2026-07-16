---
ddx:
  id: FEAT-005
  depends_on:
    - helix.prd
    - FEAT-001
    - FEAT-003
  review:
    self_hash: 91bfec0fee89364d352de541dadd2414792b33930e70c44614ce96abf26abff7
    deps:
      FEAT-001: cd37386d6fbdf5d388440be2d885fcad38298a0720429cb9fed602b55631260d
      FEAT-003: 8c4332150f3d5d591015e360231913d4e8f24f9b83f3678e65574e5f45f78e0d
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Feature Specification: FEAT-005 — Logging, Replay, and Cost Tracking

**Feature ID**: FEAT-005
**Status**: Approved
**Priority**: P0
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: Measurement & Replay
**Covered PRD Requirements**: FR-5
**Cross-Subsystem Rationale**: Projects execution, tool, provider, and routing
facts into one measured and replayable service-owned record.
**User Stories**: [US-005 — Measure and Replay an Execution](../user-stories/US-005-measurement-replay.md)

## Overview

Measurement is PRD pillar #2: per-turn timing, prefill vs decode breakdown,
cost-per-trial, subscription-quota accounting, and route-attempt feedback are
first-class outputs of the agent loop, not bolted-on observability. FEAT-005
is the surface that delivers that pillar.

Every LLM interaction and tool call in Fizeau is logged to a structured
session log. Sessions can be replayed to understand exactly what happened.
Analytics and cross-tool observability are emitted through OpenTelemetry.
Cost is preserved when a provider, gateway, or explicitly configured runtime
knows it; otherwise it remains unknown — never guessed from generic price
tables. This implements PRD P0 requirements 10-11.

The v0.11 routing redesign is a session-log schema break for routing fields:
new logs use `policy` and `power_policy`, and they do not emit removed
model reference fields. Cost attribution remains model-level: reported or
configured prices attach to the resolved provider system and concrete model,
not to routing policy names.

Patterned on DDx's `SessionEntry` JSONL session logging and `ddx
usage` reporting, but with deeper granularity — DDx logs one entry per
subprocess invocation; Fizeau logs every turn within the conversation
loop.

## Problem Statement

- **Current situation**: DDx logs one `SessionEntry` per agent subprocess
  invocation — prompt in, response out, tokens, cost. This is the outer
  envelope. What happened inside the agent (which files it read, what edits
  it made, how many LLM turns it took) is opaque.
- **Pain points**: When an agent task fails or produces unexpected results,
  there's no way to see the intermediate steps. Debugging requires re-running
  the task. Cost and timing analytics are not normalized across providers or
  compatible with external observability tooling.
- **Desired outcome**: A session log that captures every LLM turn and tool
  call with full bodies, enabling replay and debugging. OTel spans and metrics
  expose interoperable analytics. Cost is tracked per-turn when known and
  never guessed when unknown.

## Requirements

### Functional Requirements

#### Session Logging

1. Each public `Execute` call creates a session with a unique ID
2. The session log captures events in order:
   - `session.start`: timestamp, config (provider, model, working dir, max
     iterations), prompt
   - `llm.request`: messages sent to provider, tools offered
   - `llm.response`: model response (text and/or tool calls), token usage,
     timing, and cost metadata when known
   - `tool.call`: tool name, inputs, outputs, duration, error (if any)
   - `session.end`: final status, total tokens, total known cost or unknown
     status, total duration, final output
3. Events are written as JSONL — one JSON object per line, appendable
4. Each event includes: `session_id`, `seq` (sequence number), `type`,
   `timestamp`, and type-specific fields
5. Full prompt and response bodies are stored in the event (not external
   files, at least for P0)
6. Log directory is configurable. Default: `.fizeau/sessions/`
7. The caller can provide correlation metadata (bead_id, workflow, etc.)
   that is stored on `session.start` and `session.end` events

#### Replay

8. Given a session log file, Fizeau can reconstruct and display the full
   conversation: system prompt, each user/assistant turn, each tool call
   with inputs and outputs, token counts per turn, cost per turn
9. Replay is a read-only operation on the log file
10. Replay output is human-readable text (not JSON) — suitable for terminal
    display or piping to a pager

#### Telemetry and Analytics

11. Fizeau emits OpenTelemetry spans and metrics alongside JSONL logs for
    analytics and cross-tool observability
12. OTel instrumentation uses OpenTelemetry GenAI semantic conventions for
    token usage, conversation correlation, model calls, and tool execution
    where those conventions exist
13. JSONL is the authoritative replay artifact; OTel is the authoritative
    analytics and aggregation surface
    The normative telemetry schema lives in
    `docs/helix/02-design/contracts/CONTRACT-001-otel-telemetry-capture.md`.
14. Telemetry records broad provider identity and runtime-specific identity so
    local systems such as `bragi`, `vidar`, and `grendel` are distinguishable
    in cost and performance reports
15. LLM timing captures request start, first token when streaming, completion,
    and any provider-specific prefill or cache timing exposed by the backend
16. Throughput metrics such as output tok/s, input tok/s, and cached tok/s are
    derived only when the matching timing window exists; missing timing data is
    omitted rather than inferred
17. Project-specific telemetry fields not covered by standard OTel GenAI
    conventions use a Fizeau namespace (for example `ddx.cost.*` and
    `ddx.provider.*`)

#### Cost Tracking

18. Provider- or gateway-reported cost is recorded per `llm.response` event
    when available
19. Token usage is captured as **four distinct streams** per `llm.response`
    and accumulated per session: `input_tokens`, `output_tokens`,
    `cache_read_tokens`, and `cache_write_tokens`. Any stream may be zero or
    unavailable, but cache traffic is not silently folded into input/output.
    This is the runtime measurement contract; benchmark retry accounting is a
    separate cell-level concern.
20. If no reported cost exists, Fizeau may compute cost only from explicit
    pricing configuration for the exact runtime/provider system and resolved
    model. The four `$-per-Mtok` numbers (input, output, cache-read,
    cache-write) come from the exact runtime pricing map supplied to telemetry.
    Fizeau does not price by routing policy or silently substitute a generic
    price table.
21. If neither reported cost nor a matching runtime/model pricing entry exists,
    cost remains unknown and is never guessed from generic pricing tables
22. Local inference runtimes are not implicitly free; `$0` cost must come from
    reported billing or explicit configuration
23. Session totals are accumulated only when all contributing turn costs are
    known; otherwise the session total is unknown
24. Public `DrainExecuteResult` and the terminal `ServiceFinalData` projection expose both
    `CostUSD` and the public `CostSource` classification defined by
    CONTRACT-003: `reported`, `configured`, or `unknown`. `CostUSD` is optional:
    `nil` means the amount is unknown, while a present zero or positive value is
    known. `CostSource` is always present, so a known zero carries a non-unknown
    source and remains distinguishable from unknown.
25. Usage reporting aggregates token and timing data for all sessions, known
    costs for priced sessions, unknown-cost session counts, concrete model
    attribution, and v0.11 policy/power routing attribution when present

#### Cost-Cap Enforcement

26. The caller may configure a **per-run cost cap** (USD). When the running
    session total reaches or exceeds the cap, Fizeau halts the loop
    deterministically before issuing the next `llm.request`, writes a
    `session.end` event with `process_outcome=budget_halted`, and returns a
    `DrainExecuteResult` whose status reflects the halt.
27. `budget_halted` is a first-class terminal `process_outcome` (per the
    SD-010 / SD-009 §9 failure taxonomy). It is distinct from `completed`,
    `timeout`, and `harness_crash`, and survives resume semantics: a
    `budget_halted` run is not silently retried.
28. Cost-cap enforcement requires that turn cost be **known** (from provider
    report or explicit runtime/model pricing). If cost is unknown, the cap
    cannot fire and the run proceeds; this matches §22 — unknown is never
    coerced to a number.
29. Cost-cap enforcement is a feature requirement of Fizeau itself, not
    solely a property of the benchmark harness. The benchmark runner relies
    on this contract, but standalone `fiz run` invocations honor the
    same cap when one is configured.

**Implementation status (§26-29).** Implemented. The cap is plumbed
end-to-end: `fiz run --cost-cap-usd <USD>` (or `FIZEAU_COST_CAP_USD`) sets
`ServiceExecuteRequest.CostCapUSD`, which threads through
`internal/serviceimpl.NativeRequest.CostCapUSD` into
`internal/core.Request.CostCapUSD`. The native loop
(`internal/core/loop.go`) gates each iteration: if
`running_known_cost + projected_next_turn_cost ≥ cap`, it halts before the
next `llm.request`, the agent `Result.Status` becomes `budget_halted`, the
service emits a final event with `final.Status = "budget_halted"`, the
session log records `process_outcome = "budget_halted"` and `cost_cap_usd`,
and the CLI exits with code `2` (distinct from `1` for other errors). The
projection uses the most recent turn's known cost as a conservative
estimate. When turn cost is unknown (per §28) the cap cannot fire — the
loop logs a one-shot warning so operators can see the cap is inert and
proceeds normally.

#### Usage Reporting (P1 — Standalone CLI)

30. `fiz usage` aggregates session logs and telemetry: per-provider/model
    token counts (broken out by the four streams in §19), known cost, and
    throughput summaries, with time-window filtering (today, 7d, 30d, date
    range)
31. Output formats: table (default), JSON, CSV — patterned on the
    parent DDx orchestrator's session-usage report

### Non-Functional Requirements

- **Performance**: Replay logging overhead < 1ms per event; telemetry emission
  must remain low enough that per-tool-call spans are practical in normal use
- **Reliability**: Log writes are best-effort — logging failure must not
  block the agent loop. Telemetry export is also best-effort. Partial logs are
  still useful.
- **Storage**: Session logs grow at ~10-100KB per session. No automatic
  rotation in P0.
- **Compatibility**: Log format should be forward-compatible — new event
  types can be added without breaking replay of old logs

## Edge Cases and Error Handling

- **Log directory not writable**: Warn once, continue without logging.
  `DrainExecuteResult` still has token counts and cost.
- **Session interrupted (context cancelled)**: Write `session.end` with
  status=cancelled and whatever data was collected
- **Provider/runtime does not expose cost and no explicit pricing exists**:
  `CostUSD` is absent and `CostSource` is `unknown`, not 0 (free)
- **Very large tool output (>1MB)**: Truncate in log with marker, store
  byte count of original
- **Timing breakdown missing**: Emit only available timestamps and durations;
  do not synthesize prefill or cache throughput

## Success Metrics

- Every completed session has a log with all events
- `fiz replay <id>` reproduces the conversation accurately
- Provider- or runtime-reported costs are preserved exactly when available
- Unknown-cost sessions are surfaced explicitly rather than assigned guessed values
- Log files are valid JSONL readable by `jq`
- OTel spans and metrics can be consumed by the same analytics tooling used
  for Codex and Claude Code

## Acceptance Criteria

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-005-01 | JSONL session logs contain ordered `session.start`, `llm.request`, `llm.response`, `tool.call`, and `session.end` events with stable `session_id`, `seq`, timestamps, correlation metadata, and full prompt/response bodies subject only to documented truncation rules. | `go test ./session ./...` |
| AC-FEAT-005-02 | Replay renders a human-readable transcript of prompts, assistant turns, tool calls, tokens, timing, workdir/model/provider metadata, and known-vs-unknown cost state without mutating the underlying log. | `go test ./session ./...` |
| AC-FEAT-005-03 | Provider-reported cost wins over configured pricing, configured runtime pricing applies only on exact runtime/model matches, and mixed or unknown constituent costs force the session total to remain unknown rather than guessed. The four token streams (input, output, cache-read, cache-write) are tracked separately per turn and per session and never folded into one another. Pricing is never inferred from routing policy. | `go test ./session ./telemetry ./...` |
| AC-FEAT-005-08 | v0.11 session-log and usage projections preserve `policy` / `power_policy` routing attribution while replay remains tolerant of pre-v0.11 routing fields. | `go test ./internal/session ./telemetry ./...` |
| AC-FEAT-005-07 | A configured per-run cost cap halts the loop before the next `llm.request` once the known running total meets or exceeds the cap, the `session.end` event records `process_outcome=budget_halted`, and `DrainExecuteResult` surfaces the halt; if cost is unknown the cap does not fire and the run proceeds. | `go test ./session ./...` |
| AC-FEAT-005-04 | OTel export conforms to `CONTRACT-001`, including span taxonomy, identity fields, cost/timing attributes, tool error semantics, and throughput formulas derived only from valid timing windows. | `go test ./telemetry ./...` |
| AC-FEAT-005-05 | `fiz usage` preserves known-cost and unknown-cost session semantics across time-window filtering and supports the documented table, JSON, and CSV output modes. | `go test ./cmd/fiz ./session ./...` |
| AC-FEAT-005-06 | Unwritable log directories and telemetry-export failures are best-effort failures: the run still completes, operators receive a warning, and whatever partial log/telemetry data exists remains readable. | `go test ./session ./telemetry ./...` |
| AC-FEAT-005-09 | Public `DrainExecuteResult` and terminal `ServiceFinalData` preserve an optional session cost amount plus mandatory `reported`, `configured`, or `unknown` provenance: unknown omits the amount, while known zero and positive amounts remain present and are never conflated with unknown. | Root facade and service-event contract tests |

## Constraints and Assumptions

- JSONL remains the replay and forensic artifact
- OTel is the canonical analytics surface for cross-tool comparison and usage
  reporting
- Standard OTel GenAI fields are preferred; Fizeau uses `ddx.*` fields only
  for gaps such as cost source and runtime-specific timing
- `CONTRACT-001` is authoritative for telemetry field names, formulas, and
  capture semantics
- The runtime pricing map is authoritative for `$-per-Mtok` rates across the
  four token streams. Benchmark profiles may supply that map for benchmark
  runs, but FEAT-005 does not make the benchmark profile store a universal
  runtime dependency.
- Log format is Fizeau-specific but designed to be consumable by DDx's
  session inspection tooling with a thin adapter
- No log rotation or retention policy in P0

## Dependencies

- **Other features**: FEAT-001 (loop emits events), FEAT-003 (provider
  reports token usage)
- **External services**: None (logging is local)
- **PRD requirements**: P0-10, P0-11

## Out of Scope

- Log shipping to external systems (Grafana, DataDog, etc.)
- Real-time log streaming to a UI
- Automatic log rotation or retention policies
