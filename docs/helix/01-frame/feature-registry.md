---
ddx:
  id: helix.feature-registry
  depends_on:
    - helix.prd
  review:
    self_hash: 61d08d6d5029ace1f3ae4d9fd6d7167f54c41573ed6b1e133d31b567384d9459
    deps:
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:25:15Z"
---
# Feature Registry — Fizeau

The registry is the canonical one-to-one trace from product requirements to
approved feature authority. Designs, tests, and plans depend downward from the
feature named here.

| PRD requirement | Subsystem | Approved feature | Primary story |
|---|---|---|---|
| FR-1 | Embedded Execution | [FEAT-001](features/FEAT-001-agent-loop.md) | [US-001](user-stories/US-001-embedded-execution.md) |
| FR-2 | Workspace Tools | [FEAT-002](features/FEAT-002-tools.md) | [US-002](user-stories/US-002-workspace-tools.md) |
| FR-3 | Provider Parity | [FEAT-003](features/FEAT-003-providers.md) | [US-003](user-stories/US-003-provider-parity.md) |
| FR-4 | Routing & Catalog | [FEAT-004](features/FEAT-004-model-routing.md) | [US-004](user-stories/US-004-routing-catalog.md) |
| FR-5 | Measurement & Replay | [FEAT-005](features/FEAT-005-logging-and-cost.md) | [US-005](user-stories/US-005-measurement-replay.md) |
| FR-6 | CLI Proof Surface | [FEAT-006](features/FEAT-006-standalone-cli.md) | [US-006](user-stories/US-006-cli-proof-surface.md) |
| FR-7 | Distribution | [FEAT-007](features/FEAT-007-self-update-and-installer.md) | [US-007](user-stories/US-007-distribution.md) |
| FR-8 | Benchmark Evidence | [FEAT-008](features/FEAT-008-benchmark-workbench.md) | [US-008](user-stories/US-008-benchmark-workbench.md) |
