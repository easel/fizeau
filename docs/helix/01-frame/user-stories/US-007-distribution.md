---
ddx:
  id: US-007
  depends_on:
    - FEAT-007
  review:
    self_hash: f7d77406d905f1c80b62432b28060e560dc7c8d811124159f3650ff2ab914ebf
    deps:
      FEAT-007: 20cf41ca595074feb1345729785859f504ce1fa570547ffc31ea38a264aa719b
    reviewed_at: "2026-07-14T05:16:22Z"
---
# User Story: US-007 — Install and Explicitly Update the Proof CLI

**Status**: Approved
**Feature**: FEAT-007
**Feature Requirements**: validated install and explicit atomic update
**PRD Requirements**: FR-7

As an operator, I want to install and explicitly update `fiz` from a validated
release artifact, so that the proof surface is easy to obtain without adding
network behavior to embedded executions.

## Acceptance Criteria

- **US-007-AC1** — **Given** a supported platform, **when** an operator installs
  `fiz`, **then** the installer selects the correct versioned platform artifact,
  installs it as an executable, and verifies the installed binary's version.
- **US-007-AC2** — **Given** an existing executable and a downloaded
  replacement, **when** replacement succeeds, **then** the new content takes
  the original path and permissions and the temporary path is consumed.
- **US-007-AC3** — **Given** an invalid undersized download, **when** download
  validation fails, **then** the update returns an error and removes its
  temporary artifact.
- **US-007-AC4** — **Given** `fiz version --check-only`, **when** it prints the
  installed version, **then** it does not query the release API or mutate the
  installation.
