# Review Findings: Agent Loop (AR-2026-05-17-repo)

**Bead**: fizeau-584abce2  
**Review Date**: 2026-05-18  
**Scope**: Agent loop functional area alignment review

## Summary

Two distinct gaps confirmed in the agent-loop functional area, both already tracked in executing issues:

1. **AC-FEAT-001-09 pivot semantics unimplemented** → covered by fizeau-d52a3568
2. **EventPlanningTurn undocumented** → covered by fizeau-b064ffc0

## Gap 1: AC-FEAT-001-09 Pivot Semantics Unimplemented

### Finding

FEAT-001 AC-09 specifies three-tier tool-call loop pivot behavior:

- **(a) Pivot limit = 0** (default): abort immediately with `ErrToolCallLoop`
- **(b) Pivot limit > 0**: emit `EventToolCallLoopPivot`, append pivot message, reset counters, continue
- **(c) Pivot count reaches limit**: fall through to hard abort

Current implementation in `internal/core/loop.go:903-911`:

```go
if consecutiveToolCallCount >= toolCallLoopLimit {
    slog.Warn("tool-call loop: identical calls repeated, aborting", "count", consecutiveToolCallCount, "limit", toolCallLoopLimit)
    result.Status = StatusError
    result.Error = ErrToolCallLoop
    result.Duration = time.Since(start)
    snapshotMessages()
    emitFinalSessionEnd(req.Callback, sessionID, &seq, req.Provider, &result, req.Metadata)
    return result, ErrToolCallLoop
}
```

**Status**: Implements only tier (a) — hardcoded abort at `toolCallLoopLimit=5` with no pivot fields/events.

**Missing**:
- `Request.ToolCallLoopPivotLimit` field
- `Request.ToolCallLoopPivotMessage` field / `DefaultToolCallLoopPivotMessage` constant
- `Result.ToolCallLoopPivots` counter
- `EventToolCallLoopPivot` emission
- Pivot logic branch that appends message and continues

### Evidence

- File: `docs/helix/01-frame/features/FEAT-001-agent-loop.md` lines 101-103
- Spec source: `docs/research/tool-call-loop-pivot-2026-05-01.md` (referenced)
- Code: `internal/core/loop.go:143-145` (limit constant), `903-911` (abort path), no pivot logic anywhere

### Verification

```bash
# Confirm limit is hardcoded and only abort path exists
grep -n "toolCallLoopLimit\|ToolCallLoopPivot" internal/core/loop.go internal/core/types.go

# Confirm pivot constants/messages absent
grep -r "DefaultToolCallLoopPivotMessage\|ToolCallLoopPivotLimit\|ToolCallLoopPivotMessage" . --include="*.go"

# Confirm three named tests exist in spec but not in code
grep -n "TestRun_ToolCallLoop" internal/core/loop_test.go
```

Expected: three tests named in AC-09 should exist; none will be found.

### Recommended Resolution

See **fizeau-d52a3568**: Implement AC-FEAT-001-09 pivot semantics + emit `EventToolCallLoopPivot` + add the three named tests.

---

## Gap 2: EventPlanningTurn Undocumented

### Finding

`internal/core/loop.go:226` emits `EventPlanningTurn` when `req.PlanningMode=true`:

```go
emitCallback(req.Callback, Event{
    SessionID: sessionID,
    Seq:       seq,
    Type:      EventPlanningTurn,
    Timestamp: time.Now().UTC(),
    Data: mustMarshal(map[string]any{
        "plan":  planResp.Content,
        "usage": planResp.Usage,
        "model": planResp.Model,
    }),
})
```

However, AGENTS.md §Event emission (lines 88-96) lists only 8 event types and does not include `EventPlanningTurn`:

- EventSessionStart
- EventCompactionStart
- EventCompactionEnd
- EventLLMRequest
- EventLLMDelta
- EventLLMResponse
- EventToolCall
- EventSessionEnd

**Status**: The event is emitted by the code but is missing from the convention documentation.

**Impact**: Future loop changes risk dropping the event; downstream consumers relying on it may break silently if the event is not listed in the authoritative convention doc.

### Evidence

- Code: `internal/core/loop.go:216-234` (planning turn call + EventPlanningTurn emission)
- Code: `internal/core/agent.go:219` (per alignment review, confirms the event type is defined)
- Documentation: `AGENTS.md:76-96` §Event emission (8 types listed, no EventPlanningTurn)

### Verification

```bash
# Confirm the event is emitted
grep -n "EventPlanningTurn" internal/core/loop.go

# Confirm AGENTS.md does not list it
grep -n "EventPlanningTurn\|EventLLMResponse\|EventToolCall" AGENTS.md
```

Expected: first grep will find the emission; second will find EventLLMResponse and EventToolCall but NOT EventPlanningTurn.

### Recommended Resolution

See **fizeau-b064ffc0**: Document `EventPlanningTurn` in AGENTS.md and (if planning is in-scope for FEAT-001) extend the feature spec.

---

## Quality Observations

1. **Planning mode spec coverage**: FEAT-001 does not mention planning mode or `EventPlanningTurn` in its requirements or acceptance criteria. If planning mode is a keeper feature, FEAT-001 should document it.

2. **Loop-layer session continuity**: ARreport identifies this as a low-severity gap (untested at loop layer). No blocking work; can be filed separately if prioritized.

3. **Pivot-tier test references**: AC-FEAT-001-09 names three specific tests (`TestRun_ToolCallLoopDetection`, `TestRun_ToolCallLoopPivot_SinglePivotSucceeds`, `TestRun_ToolCallLoopPivot_ExhaustedPivotsAborts`, `TestRun_ToolCallLoopPivot_ZeroPivotLimitPreservesLegacyAbort`). Implementing the feature must include these tests.

---

## Execution Issues Covering This Review

| Issue ID | Type | Goal | Verification |
|----------|------|------|--------------|
| fizeau-d52a3568 | task | Implement AC-FEAT-001-09 pivot semantics + three named tests | `go test ./internal/core/... -count=1` |
| fizeau-b064ffc0 | chore | Document `EventPlanningTurn` in AGENTS.md | grep of AGENTS.md for `EventPlanningTurn` |

Both issues are ready to work.
