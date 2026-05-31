# Review: agent-loop (fizeau-584abce2)

**Area**: `area:agent-loop`  
**Spec**: `FEAT-001-agent-loop.md`  
**Verdict**: APPROVE with one spec-drift finding (REQUEST_CHANGES on the spec, not the code)

---

## Bead review: fizeau-584abce2

### Verdict: APPROVE (with spec update needed)

### Per-criterion

**AC-FEAT-001-01** — Text-only/empty response terminates with `status=success`, messages appended, tokens preserved.  
Verdict: APPROVE  
Evidence: `TestRun_SimpleTextResponse`, `TestRun_EmptyResponse`; loop.go:724-731 terminates on `len(resp.ToolCalls)==0`; tokens accumulated via `result.Tokens.Add(resp.Usage)` at loop.go:512.

**AC-FEAT-001-02** — Tool-calling turns execute sequentially in provider order, record in `Result.ToolCalls`, feed results into next request, terminate on text-only turn.  
Verdict: APPROVE  
Evidence: `TestRun_ToolCallThenResponse`, `TestRun_MultipleToolCalls`; loop.go:795-883: sequential execution path; results merged in order into messages and logs; conversation history correctly rebuilt for each provider call.

**AC-FEAT-001-03** — Iteration limits, context cancellation, transient-provider retry success, and retry exhaustion all terminate with documented status without extra provider calls.  
Verdict: APPROVE  
Evidence: `TestRun_IterationLimit` (StatusIterationLimit), `TestRun_ContextCancellation` (StatusCancelled), `TestRun_RetriesProviderFailures` (3 calls, success), `TestRun_RetryExhaustionStopsAtRetryCeiling` (5 calls, 5 error events, error status). ctx.Err() checked before each provider call; backoff capped at attempt≤10.

**AC-FEAT-001-04** — Session lifecycle events emitted in seq order; correlation metadata, accumulated usage, known-vs-unknown cost semantics preserved.  
Verdict: APPROVE  
Evidence: `TestRun_EventCallback` (4 events: session.start, llm.request, llm.response, session.end); `TestRun_ConcurrentRunsKeepIndependentState` verifies each event's Seq == index within its session; `TestRun_SessionEndEventIncludesKnownCost`, `TestRun_SessionEndEventOmitsUnknownCost`. Cost sentinel -1 for unknown correctly omitted from session.end payload.

**AC-FEAT-001-05** — Streaming: delta assembly, NoStream fallback, attempt metadata propagation, timing capture without counting callback latency.  
Verdict: APPROVE  
Evidence: `TestConsumeStream_ToolCallAssembly`, `TestConsumeStream_MultipleToolCalls`; `TestRun_StreamingChatSpanIncludesServerUsageAndTiming` (first-token ≥12ms, generation ≥18ms); `TestRun_StreamingChatSpanIncludesRequestCallbackLatency` confirms callback sleep (30ms) is NOT excluded from first-token timing (note: this is the expected behavior, callback runs before chatStart is set).  
NoStream fallback: loop.go:431 (`req.NoStream` bypasses StreamingProvider to Chat()).

**AC-FEAT-001-06** — Concurrent `Run()` calls do not share mutable state; compaction no-fit paths fail closed without over-budget provider call.  
Verdict: APPROVE  
Evidence: `TestRun_ConcurrentRunsKeepIndependentState` (barrier test proves isolation: separate SessionIDs, separate token counts, separate event streams); `TestRun_OverflowCompactionNoFitReturnsError` (StatusError, no extra provider call after ErrCompactionNoFit).

**AC-FEAT-001-07** — ErrReasoningOverflow when accumulated `reasoning_content` exceeds byte limit; error message includes model name and threshold; limit=0 disables.  
Verdict: APPROVE (with test-name note below)  
Evidence: `TestConsumeStream_ReasoningOverflow` checks `errors.Is(err, ErrReasoningOverflow)`, model name in error, "32KB" in error; `TestConsumeStream_UnlimitedReasoningByteLimit` verifies limit=0 allows 400KB through.  
**Note**: AC cites `TestConsumeStream_ReasoningUnlimited` and `TestConsumeStream_ReasoningOverflowErrorMessage` — neither exists under those names. The actual tests are `TestConsumeStream_UnlimitedReasoningByteLimit` and the message check is inline in `TestConsumeStream_ReasoningOverflow`. AC verification references are stale.

**AC-FEAT-001-08** — ErrReasoningStall when only reasoning deltas arrive past stall deadline; adaptive deadline from `reasoningBudgetTokens`; limit=0 disables.  
Verdict: APPROVE  
Evidence: `TestConsumeStream_ReasoningStall` (subtest "stall path via short timeout" with 50ms threshold); `TestConsumeStream_AdaptiveStallDeadline` (512-token budget prevents premature stall); `TestConsumeStream_AdaptiveStallDeadline_NoBudget` (floor fires without budget). Structured error contract verified by `TestConsumeStream_ReasoningStall_StructuredErrorAndEvent` (Code(), Model, Timeout, ReasoningTail, PromptID).

**AC-FEAT-001-09** — ToolCallLoopPivot: `toolCallLoopLimit (=3)` consecutive identical calls; pivot vs abort based on `ToolCallLoopPivotLimit`.  
Verdict: APPROVE (with **spec drift** — see Findings)  
Evidence: `TestRun_ToolCallLoopDetection` (abort at 5, not 3); `TestRun_ToolCallLoopPivot_SinglePivotSucceeds` (pivot emits EventToolCallLoopPivot, injects pivot message, resets counter); `TestRun_ToolCallLoopPivot_ExhaustedPivotsAborts` (second detection aborts); `TestRun_ToolCallLoopPivot_ZeroPivotLimitPreservesLegacyAbort` (legacy abort at 5).

**AC-FEAT-001-10** — Byte limit configurable via `config.yaml`; stall timeout uses hardcoded ceiling; `Request.ReasoningStallTimeout` overrides for tests.  
Verdict: APPROVE  
Evidence: `TestLoad_ReasoningThresholds` (internal/config/config_test.go:1293) verifies `reasoning_byte_limit: 0` → unlimited; `TestDefaultConstants` (stream_consume_test.go:722) verifies `DefaultReasoningByteLimit=256*1024`, `DefaultReasoningStallTimeout=16384s`, `DefaultReasoningTailBytes=2000`.

---

### Summary

The agent-loop implementation is complete, well-tested, and all 10 FEAT-001 acceptance criteria are functionally satisfied. Tests pass (`ok github.com/easel/fizeau/internal/core 22.832s`). The implementation has grown beyond the spec in two areas: (a) the loop limit constant was tuned upward with benchmark evidence, and (b) parallel tool execution was added (not in FEAT-001 scope but implemented in loop.go:752-793 behind the `Tool.Parallel()` predicate). Both extensions are beneficial and tested.

---

### Findings

**FINDING-1 — SPEC DRIFT: toolCallLoopLimit (=3) vs implementation (=5)**  
Severity: Medium (spec gap, no behavioral regression)  
Location: `docs/helix/01-frame/features/FEAT-001-agent-loop.md:98` vs `internal/core/loop.go:143`

The spec states `toolCallLoopLimit (=3)` in AC-FEAT-001-09. The implementation uses `const toolCallLoopLimit = 5`. The change is intentional: the loop.go comment explains it was tuned after TB-2.1 benchmark sweeps showed limit=3 killed ~30% of Vidar (oMLX 8-bit Qwen) trials that were actually recoverable. The test comment at loop_test.go:2098 already says "(=5)".

Resolution: update FEAT-001 AC-FEAT-001-09 to read `toolCallLoopLimit (=5)` and note the tuning rationale. No code change needed.

**FINDING-2 — AC-FEAT-001-07: stale test-name references**  
Severity: Low (documentation gap only)  
Location: `FEAT-001-agent-loop.md:96`

The AC verification column names `TestConsumeStream_ReasoningUnlimited` and `TestConsumeStream_ReasoningOverflowErrorMessage`. These functions do not exist. The actual coverage is:
- `TestConsumeStream_UnlimitedReasoningByteLimit` (for limit=0 behavior)
- Error-message assertions are inline in `TestConsumeStream_ReasoningOverflow`

Resolution: update the verification column to reference the actual test names.

**FINDING-3 — AC-FEAT-001-10: TestLoad_ReasoningThresholds is in config package**  
Severity: Informational  
Location: `FEAT-001-agent-loop.md:99`

`TestLoad_ReasoningThresholds` is at `internal/config/config_test.go:1293`, not `internal/core`. The AC doesn't specify the package, so this is an ambiguity rather than a gap. Coverage is real.

---

### Strengths

- Comprehensive test coverage: 2760 lines of test in loop_test.go, full stream_consume_test.go suite
- Correct concurrent isolation: per-call state, no package globals mutated during Run()
- Parallel tool execution correctly batches read-only tools while serializing side-effecting ones (Tool.Parallel() predicate)
- Cost cap gate (FEAT-005 §26-29) integrated cleanly: fires before the offending request, respects unknown-cost bypass, sentinel -1 preserved
- Adaptive reasoning-stall deadline scales with observed token rate — prevents false positives on slow local inference
- Overflow recovery compacts and resets the provider retry counter atomically without infinite loop risk
