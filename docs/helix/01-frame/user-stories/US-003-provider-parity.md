---
ddx:
  id: US-003
  depends_on:
    - FEAT-003
  review:
    self_hash: f204f2e58a405ef53c8a3bab96afcf242e755ec10bbe85b1d7985e81b95abe81
    deps:
      FEAT-003: 8c4332150f3d5d591015e360231913d4e8f24f9b83f3678e65574e5f45f78e0d
    reviewed_at: "2026-07-14T05:16:22Z"
---
# User Story: US-003 — Switch Provider Systems Without Changing the Embedder

**Status**: Approved
**Feature**: FEAT-003
**Feature Requirements**: provider-neutral request, event, and usage contracts
**PRD Requirements**: FR-3

As a tool builder, I want the same execution request to work with local,
cloud, or harness-backed inference, so that deployment choice does not fork my
application or its observability.

## Acceptance Criteria

- **US-003-AC1** — **Given** the same public execution request shape, **when**
  it is dispatched through configured LM Studio, OMLX, and OpenRouter provider
  systems, **then** every run returns the same public final shape with the
  actual provider and model identity.
- **US-003-AC2** — **Given** a connected provider with discovered models,
  **when** an embedder lists providers, **then** the public projection reports
  its concrete type, status, model count, billing, and supported capabilities
  without claiming an unsupported reasoning control.
