---
ddx:
  id: FEAT-006
  depends_on:
    - helix.prd
    - FEAT-001
    - FEAT-002
    - FEAT-003
    - FEAT-005
  review:
    self_hash: 1c78778fcc8efa7fe750cf233719c21f1f6b07ce6b098c48f6d42855d57faa07
    deps:
      FEAT-001: cd37386d6fbdf5d388440be2d885fcad38298a0720429cb9fed602b55631260d
      FEAT-002: 1f53e72517347be0932a7b315aa1cc00cc48fc526ca3c53506cd179e8d0231a9
      FEAT-003: 8c4332150f3d5d591015e360231913d4e8f24f9b83f3678e65574e5f45f78e0d
      FEAT-005: 91bfec0fee89364d352de541dadd2414792b33930e70c44614ce96abf26abff7
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Feature Specification: FEAT-006 — Standalone CLI

**Feature ID**: FEAT-006
**Status**: Approved
**Priority**: P0
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: CLI Proof Surface
**Covered PRD Requirements**: FR-6
**Cross-Subsystem Rationale**: Proves the public service, routing, tools, and
measurement surfaces through a thin command adapter.
**User Stories**: [US-006 — Exercise the Public Service from a CLI](../user-stories/US-006-cli-proof-surface.md)

## Overview

The `fiz` CLI is a thin binary over the public Fizeau service facade. It proves
the library works end-to-end, serves as an embeddable harness backend for DDx,
and gives operators inspection commands for providers, policies, routing,
usage, logs, and replay.

The CLI mirrors the v0.11 service contract. Routing intent is expressed with
`--policy`, `--min-power`, and `--max-power`; hard overrides use `--model`,
`--provider`, and `--harness`.

## Problem Statement

- Users need a standalone way to run and inspect Fizeau without writing an
  embedder.
- DDx needs a stable harness command surface with machine-readable output.
- The v0.10 routing flags invited confusion between routing intent, exact pins,
  and compatibility aliases.

## Requirements

### Core CLI

1. `fiz run "prompt"` runs a non-interactive prompt, prints final text to
   stdout, progress/status to stderr, writes a session log, and exits.
2. `fiz run @file.md`, `fiz -p @file.md`, stdin prompts, and DDx prompt
   envelopes are accepted according to the existing prompt-mode rules.
3. `--json` emits structured machine-readable output with final text, status,
   token usage, cost semantics, session identity, and routing evidence.
4. Exit codes are deterministic: `0` success, `1` execution failure, `2`
   config/usage error.
5. Mounted execution through `agentcli.MountCLI` never calls `os.Exit`; the
   standalone `cmd/fiz` binary owns process termination.

### Configuration

6. Config paths use the Fizeau namespace:
   - project: `.fizeau/config.yaml`;
   - global: `~/.config/fizeau/config.yaml`;
   - session logs: `.fizeau/sessions` by default.
7. Environment variables use the `FIZEAU_*` namespace.
8. Config precedence is built-in defaults < global config < project config <
   environment variables < CLI flags.
9. Provider configs name concrete provider systems and may set
   `include_by_default` for default-routing participation.

### Routing Flags

10. `--policy` selects a named v0.11 policy: `cheap`, `default`, `smart`, or
    `air-gapped`.
11. Power flags are `--min-power` and `--max-power`.
12. Hard pins are `--model`, `--provider`, and `--harness`.
13. `--policy` and power flags are routing intent. Hard pins are override
    signals and narrow the candidate set before scoring.
14. Removed flags fail as usage errors with migration guidance:
    - `--profile`;
    - the legacy model pin flag;
    - deprecated backend-selection flags such as `--backend`.
15. Removed compatibility policy names are not advertised in help.

### Inspection Commands

16. `fiz policies` lists canonical policies, power bounds, `allow_local`,
    `require[]`, and catalog metadata. `--json` emits stable keys.
17. `fiz harnesses` lists native/subprocess harnesses, billing, availability,
    account/quota status, reasoning/permission support, and capability matrix
    data. `--json` emits stable keys.
18. `fiz models` is the top-level model registry inspection command:
    - `fiz models [flags]` lists the assembled per-source cache snapshot with
      columns `PROVIDER MODEL FAMILY VERSION TIER POWER COST/M STATUS QUOTA AUTO`;
    - `fiz models <ref> [flags]` shows detail for a canonical
      `<provider>/<id>` ref or a shortform fuzzy ref, reporting ambiguous
      candidates with full canonical IDs;
    - `fiz models --json` emits the assembled snapshot as JSON;
    - list flags are `--refresh`, `--no-refresh`, `--provider`,
      `--power-min`, `--power-max`, and `--include-noise`;
    - default list output suppresses low-power/unranked long-tail entries while
      `--include-noise` shows the complete snapshot for debugging.
19. `fiz cache prune` removes stale discovery/runtime cache files for sources
    not present in the current config while preserving active sources.
20. Legacy `--list-models` remains a compatibility model-listing surface over
    the service facade for harness/provider inventory.
21. `fiz route-status` reports `policy` and `power_policy` keys, not removed
    routing names.
22. `fiz usage`, `fiz log`, and `fiz replay` consume public service
    projections instead of parsing internal session-log structs.

### DDx Harness Integration

23. When invoked by DDx, `fiz` accepts prompt envelopes through stdin or final
    argument and returns structured JSON suitable for DDx parsing.
24. DDx may pass policy, power bounds, or exact hard pins through the CLI.
    DDx does not name inner provider routes as routing policy.
25. Output preserves token usage, known-vs-unknown cost semantics, session ID,
    routing actual, and continuity-ready metadata.

## Acceptance Criteria

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-006-01 | Prompt input resolves correctly from `run <prompt>`, `-p`, `@file`, stdin, and DDx prompt-envelope inputs, with malformed envelopes failing as usage/config errors. | `go test ./agentcli ./cmd/fiz ./...` |
| AC-FEAT-006-02 | Success, agent failure, and usage/config failure produce deterministic stdout/stderr behavior, JSON output, and exit codes `0`, `1`, and `2`. | `go test ./agentcli ./cmd/fiz ./...` |
| AC-FEAT-006-03 | Config precedence is built-in defaults < global config < project config < environment variables < CLI flags. | `go test ./internal/config ./agentcli ./cmd/fiz ./...` |
| AC-FEAT-006-04 | `fiz policies` and `fiz harnesses` return table and JSON output from the public service facade. | `go test ./agentcli ./cmd/fiz -run 'Policies|Harnesses'` |
| AC-FEAT-006-05 | `--policy`, `--min-power`, and `--max-power` feed `ServiceExecuteRequest` and route-status JSON uses `policy` / `power_policy`. | `go test ./agentcli ./cmd/fiz -run 'Policy|Power|RouteStatus'` |
| AC-FEAT-006-06 | `--profile`, the legacy model pin flag, and `--backend` are removed or rejected with clear migration guidance. The accepted root-level flag set is `--policy`, `--min-power`, `--max-power`, `--model`, `--provider`, `--harness`, `--preset`, `--system`, `--reasoning`, `--max-iter`, `--work-dir`, `--allow-deprecated-model`, `--list-models`, `--json`. | `go test ./agentcli ./cmd/fiz -run 'RejectsProfile|Backend|Preset|RootFlags'` |
| AC-FEAT-006-07 | `log`, `replay`, and `usage` operate against the effective session-log directory and consume public service projections. | `go test ./agentcli ./cmd/fiz ./...` |
| AC-FEAT-006-08 | DDx harness mode returns structured JSON containing output, token usage, cost semantics, session identity, and routing evidence without scraping human output. | `go test ./agentcli ./cmd/fiz ./...` |

## Constraints and Assumptions

- The CLI is a showcase and harness backend, not an interactive TUI.
- The library has no config file opinions beyond the `ServiceConfig` facade;
  config loading is a CLI/config-package concern.
- CLI inspection output must prefer public service projections over internal
  package data.

## Dependencies

- `CONTRACT-003` for the service facade.
- `FEAT-003` for provider config and billing.
- `FEAT-004` for policies and routing.
- `FEAT-005` for session-log projections.

## Out of Scope

- REPL or chat UI.
- Shell completions and man pages.
- Plugin system.
- Colorized rich terminal output.
