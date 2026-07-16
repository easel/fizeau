---
ddx:
  id: US-001
  depends_on:
    - FEAT-001
  review:
    self_hash: 83ee6bdfd89336cf77cb0dd2a1f6d8250baf1de494605112a56ef21b835c9b83
    deps:
      FEAT-001: cd37386d6fbdf5d388440be2d885fcad38298a0720429cb9fed602b55631260d
    reviewed_at: "2026-07-16T07:25:15Z"
---
# User Story: US-001 — Execute an Embedded Agent Run

**Status**: Approved
**Feature**: FEAT-001
**Feature Requirements**: public construction and bounded execution lifecycle
**PRD Requirements**: FR-1

As a Go tool builder, I want to construct Fizeau and execute a bounded agent run
through the public service contract, so that my product gains agent behavior
without owning another loop or importing runtime internals.

## Acceptance Criteria

- **US-001-AC1** — **Given** a configured subprocess harness cassette,
  **when** a caller invokes and drains public `FizeauService.Execute`, **then**
  it receives a successful final status and text, actual harness identity,
  session identity, progress deltas, echoed metadata, available usage, and
  normalized tool call/result pairs.
- **US-001-AC2** — **Given** a native execution whose provider is still
  producing a response, **when** the caller cancels the context, **then** the
  service closes with a non-success terminal final instead of accepting the
  provider's later response.
