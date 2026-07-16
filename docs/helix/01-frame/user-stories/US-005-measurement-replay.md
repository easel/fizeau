---
ddx:
  id: US-005
  depends_on:
    - FEAT-005
  review:
    self_hash: a1c55221d28ebcf4415320a93c02126c8d27a782b0cb1917e8dce4415e1d8180
    deps:
      FEAT-005: 91bfec0fee89364d352de541dadd2414792b33930e70c44614ce96abf26abff7
    reviewed_at: "2026-07-16T07:15:29Z"
---
# User Story: US-005 — Measure and Replay an Execution

**Status**: Approved
**Feature**: FEAT-005
**Feature Requirements**: per-turn measurement and service-owned replay
**PRD Requirements**: FR-5

As an evaluation builder, I want every run to produce comparable measurements
and a replayable record, so that I can explain differences between prompts,
routes, and provider systems.

## Acceptance Criteria

- **US-005-AC1** — **Given** a streaming LLM attempt with server usage, **when**
  its response completes, **then** the chat span contains provider identity,
  four token streams, and measured first-token and generation timing.
- **US-005-AC2** — **Given** provider-reported cost and configured fallback
  pricing, **when** an attempt completes, **then** provider-reported cost wins
  in the numeric result and the chat span preserves its source and amount.
- **US-005-AC3** — **Given** a completed session containing system, user, LLM,
  and tool events, **when** a consumer replays it, **then** service-owned data
  reconstructs the ordered conversation and tool attempt without
  provider-native transcript parsing.
