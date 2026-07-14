---
ddx:
  id: US-004
  depends_on:
    - FEAT-004
  review:
    self_hash: 7ee7f81b23b2c2ae22f0c1137c31f100e29e7f89a73f3f71268269d2d2738d25
    deps:
      FEAT-004: 9761114849a85ae13627ea086fdfb1d332edda875fd81cb3769096bedc7eaeae
    reviewed_at: "2026-07-14T20:00:14Z"
---
# User Story: US-004 — Resolve an Explainable Route

**Status**: Approved
**Feature**: FEAT-004
**Feature Requirements**: catalog-backed policy, power, and exact-pin routing
**PRD Requirements**: FR-4

As an embedder, I want Fizeau to resolve one eligible route from explicit
intent and current evidence, so that dispatch is reusable and explainable while
my application retains control of semantic retries.

## Acceptance Criteria

- **US-004-AC1** — **Given** explicit routing intent and a captured model
  snapshot, **when** Fizeau executes a resolved route, **then** the routing
  decision identifies the selected snapshot candidate and the final result
  reports the same provider, model, and server instance without re-probing the
  providers' model endpoints.
- **US-004-AC2** — **Given** local and remote candidates in one model snapshot,
  **when** Fizeau resolves `cheap`, `default`, and `smart` policies, **then** it
  applies the same evidence and eligibility model while selecting the expected
  local or opted-in remote route.
- **US-004-AC3** — **Given** one selected route, **when** its dispatch fails,
  **then** `Execute` reports that attempted route and does not dispatch the next
  ranked provider in the same request.
