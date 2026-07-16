---
ddx:
  id: FEAT-001
  depends_on:
    - helix.prd
  review:
    self_hash: cd37386d6fbdf5d388440be2d885fcad38298a0720429cb9fed602b55631260d
    deps:
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Feature Specification: FEAT-001 — Agent Loop

**Feature ID**: FEAT-001
**Status**: Approved
**Priority**: P0
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: Embedded Execution
**Covered PRD Requirements**: FR-1
**Cross-Subsystem Rationale**: Owns the public execution lifecycle that tools,
providers, measurement, and the proof CLI compose around.
**User Stories**: [US-001 — Execute an Embedded Agent Run](../user-stories/US-001-embedded-execution.md)

## Overview

The agent loop is Fizeau's core — a tool-calling LLM conversation loop that
sends a prompt, executes tool calls from the model's response, feeds results
back, and repeats until the model produces a final text response or limits are
reached. It is the primitive that backs PRD pillar #1 (a reusable, embeddable
agent loop) and the surface that pillar #2 (measurement) instruments. This
implements PRD P0 requirements 1, 8, 10, and 11.

## Problem Statement

- **Current situation**: Tools that need a tool-calling agent on their
  critical path either re-implement the loop themselves or shell out to
  standalone CLIs (claude, codex, pi, opencode), each managing its own
  conversation loop with its own bugs, gaps, and observability semantics.
- **Pain points**: No programmatic control over the loop from Go. Can't
  inspect, pause, or redirect mid-conversation. No shared state. Per-tool
  re-implementation of retry, compaction, sampling, and session logging.
- **Desired outcome**: A Go function that runs the full agent loop in-process,
  returns structured results with full tool-call history, and emits the
  per-turn instrumentation FEAT-005 needs to make the loop measurable.

## Requirements

### Functional Requirements

1. `fizeau.New(...).Execute(ctx, Request)` through `FizeauService` is the
   primary entry point
2. The public request contains prompt and system instructions, routing intent
   or exact pins, tool/permission policy, iteration bounds, working directory,
   and optional callbacks
3. The service resolves and dispatches one provider or harness route, then the
   internal loop sends messages to that resolved provider and processes the
   response
4. When the response contains tool calls, each tool is executed sequentially
   and results are appended to the conversation
5. When the response contains only text (no tool calls), the loop terminates
   with status=success
6. The loop terminates with status=iteration_limit when max_iterations is
   reached
7. The loop terminates with status=cancelled when ctx is cancelled
8. The loop terminates with status=error on provider errors (after retries)
9. All tool calls are recorded in the Result with inputs, outputs, duration,
   and errors
10. Token counts (input, output) are accumulated across all loop iterations
11. Total duration is measured from start to completion

### Non-Functional Requirements

- **Performance**: Loop overhead (excluding model inference and tool execution)
  < 1ms per iteration
- **Memory**: Conversation history is bounded by max_iterations × typical
  response size — no unbounded growth
- **Concurrency**: Multiple public `Execute` calls can run concurrently with
  independent state
- **Testability**: Provider interface is mockable for unit tests

## Edge Cases and Error Handling

- **Model returns empty response**: Treat as final response (status=success,
  empty output)
- **Tool call fails**: Include error in tool result, let model decide how to
  proceed
- **Provider returns rate limit**: Retry with backoff up to 3 times, then fail
- **Model hallucinates unknown tool**: Return error for that tool call, continue
- **Context cancelled mid-tool-call**: Interrupt tool (kill bash process, abort
  file I/O), return partial results with status=cancelled

## Success Metrics

- The public service can complete a multi-step task (read file → edit →
  verify) in a single `Execute` call
- Loop correctly terminates on all exit conditions (success, limit, cancel, error)
- Token counts match provider-reported usage

## Acceptance Criteria

The criteria below are an **implementation reference** for the internal native
loop. They preserve the verified mechanics behind the public service but do not
replace US-001's public `FizeauService.Execute` acceptance criteria. Harness
dispatch and route selection are specified by CONTRACT-003, FEAT-003, and
FEAT-004.

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-001-01 | A text-only or empty provider response terminates `Run()` with `status=success`, appends the assistant message to `Result.Messages`, and preserves provider-reported token totals in `Result.Tokens`. | `go test ./...` |
| AC-FEAT-001-02 | Tool-calling turns execute tool calls sequentially in provider order, record each call in `Result.ToolCalls`, feed tool results into the next provider request, and terminate successfully when a later turn returns text only. | `go test ./...` |
| AC-FEAT-001-03 | Iteration limits, context cancellation, transient-provider retry success, and retry exhaustion all terminate the loop with the documented status and without issuing extra provider calls beyond the runtime retry policy. | `go test ./...` |
| AC-FEAT-001-04 | Session lifecycle events are emitted in `seq` order with `session.start`, `llm.request`, optional `llm.delta`, `llm.response`, optional `tool.call`, and `session.end`; correlation metadata, accumulated usage, and known-vs-unknown cost semantics are preserved in emitted event payloads. | `go test ./...` |
| AC-FEAT-001-05 | Streaming providers support delta assembly, `NoStream` fallback, attempt metadata propagation, and timing capture for request start, first token, and completion without counting callback latency toward provider timing windows. | `go test ./...` |
| AC-FEAT-001-06 | Concurrent `Run()` calls do not share mutable state, and compaction no-fit paths fail closed without issuing an over-budget provider call. | `go test ./...` |
| AC-FEAT-001-07 | When accumulated `reasoning_content` exceeds the configurable byte limit (default 256 KB, `Request.ReasoningByteLimit`) with no `content` or `tool_call` delta seen, the stream is aborted with `ErrReasoningOverflow`. The error message includes the model name and the current threshold. Setting the limit to 0 disables the check. | `TestConsumeStream_ReasoningOverflow`, `TestConsumeStream_ReasoningUnlimited`, `TestConsumeStream_ReasoningOverflowErrorMessage` |
| AC-FEAT-001-08 | When only `reasoning_content` deltas arrive for longer than the effective stall deadline with no `content` or `tool_call` delta, the stream is aborted with `ErrReasoningStall`. The effective deadline is computed adaptively when a `reasoningBudgetTokens` is set: `stall_start + (budget_tokens × 4 bytes/tok) / observed_rate × 2`, with a 30 s hard floor. Without a budget, the static `Request.ReasoningStallTimeout` (default 300 s) is used as a fixed deadline. Setting the timeout to 0 disables the check. | `TestConsumeStream_ReasoningStall`, `TestConsumeStream_AdaptiveStallDeadline`, `TestConsumeStream_AdaptiveStallDeadline_NoBudget` |
| AC-FEAT-001-09 | When the agent produces identical tool calls (same name + args fingerprint) for `toolCallLoopLimit` (=3) consecutive turns, loop behavior depends on `Request.ToolCallLoopPivotLimit`. (a) When the pivot limit is `0` (default, legacy behavior), the loop exits immediately with non-retryable `ErrToolCallLoop` and emits a final `session.end`. (b) When the pivot limit is `> 0` and `Result.ToolCallLoopPivots < ToolCallLoopPivotLimit`, the loop instead increments `Result.ToolCallLoopPivots`, emits an `EventToolCallLoopPivot` (data: `pivot_count`, `pivot_limit`, `fingerprint`), appends the configured `ToolCallLoopPivotMessage` (or `DefaultToolCallLoopPivotMessage` if empty) as a `RoleUser` message, resets the consecutive-call counter and last-fingerprint to zero, and continues the loop from the next iteration. (c) Once the pivot count reaches the limit, the legacy hard-abort path fires. | `TestRun_ToolCallLoopDetection`, `TestRun_ToolCallLoopPivot_SinglePivotSucceeds`, `TestRun_ToolCallLoopPivot_ExhaustedPivotsAborts`, `TestRun_ToolCallLoopPivot_ZeroPivotLimitPreservesLegacyAbort` |
| AC-FEAT-001-10 | The byte limit is configurable via `config.yaml` (`reasoning_byte_limit`) and `Request.ReasoningByteLimit`. `config.yaml` value `0` means unlimited. The stall timeout is not configurable; it uses a hardcoded worst-case ceiling (`DefaultReasoningStallTimeout` = 32 768 tokens ÷ 2 tok/s = 16 384 s) which the adaptive mechanism overrides at runtime for any run where the reasoning budget is known. `Request.ReasoningStallTimeout` may be set programmatically (e.g. in tests) to override the default. | `TestLoad_ReasoningThresholds`, `TestDefaultConstants` |

**Source provenance**: AC-FEAT-001-09 pivot semantics extracted from
`docs/research/tool-call-loop-pivot-2026-05-01.md` (Desired Behavior + Exact
File Changes sections).

## Constraints and Assumptions

- The caller supplies service configuration and routing intent. Fizeau owns
  provider/harness selection and construction; the internal native loop
  receives the already-resolved concrete provider.
- Tool set is fixed at compile time for P0; extensible tool registration is P2

## Dependencies

- **Other features**: FEAT-002 (tools), FEAT-003 (providers)
- **External services**: An LLM provider (local or cloud)
- **PRD requirements**: P0-1, P0-8, P0-9, P0-10, P0-11

## Out of Scope

- Interactive/streaming output to a terminal (headless only)
- Continuation orchestration, which is a separate public service operation
  governed by CONTRACT-003 rather than persistence across `Execute` calls
- Parallel tool execution (tools execute sequentially)
