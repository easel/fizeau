---
ddx:
  id: US-002
  depends_on:
    - FEAT-002
  review:
    self_hash: 6d7fad544c6ba871e42c6b6b2b4926d9e2a78b7c1e69f294fd14d46cca157aff
    deps:
      FEAT-002: 1f53e72517347be0932a7b315aa1cc00cc48fc526ca3c53506cd179e8d0231a9
    reviewed_at: "2026-07-16T07:15:29Z"
---
# User Story: US-002 — Use Auditable Workspace Tools

**Status**: Approved
**Feature**: FEAT-002
**Feature Requirements**: stable schemas and working-directory semantics
**PRD Requirements**: FR-2

As an embedder, I want the agent to inspect and modify a workspace through
stable tools, so that actions are predictable, scoped, and visible in the run
record.

## Acceptance Criteria

- **US-002-AC1** — **Given** a workspace with a target file, **when** the native
  loop uses relative-path navigation, patch, and read tools, **then** it changes
  only the intended file and records those tool calls in the result.
- **US-002-AC2** — **Given** a native read tool call, **when** execution ends,
  **then** ordered service-owned `tool_call` and `tool_result` events expose the
  tool identity, input, output, and error state.
