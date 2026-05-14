<bead-review>
  <bead id="fizeau-dc6d5f1f" iter=1>
    <title>Design shared lease backend for multi-machine routing</title>
    <description>
Design the multi-machine extension for sticky route leases. In-scope files: FEAT-004/ADR-005 follow-up text or a new design note under docs/helix/02-design, plus beads for concrete backend implementation if selected. Required design: explain why server metrics alone are advisory and racy; define shared lease records with sticky key, provider, endpoint, model, owner, expiry, and refresh semantics; define atomic acquire/refresh/release behavior; choose or defer a backend such as Redis or Postgres; document failure behavior when the lease backend is unavailable. Out of scope: implementing Redis/Postgres backend in this bead.
    </description>
    <acceptance>
1. rg -n 'shared lease|multi-machine|server metrics alone|atomic' docs/helix/01-frame/features/FEAT-004-model-routing.md docs/helix/02-design/adr/ADR-005-smart-routing-replaces-model-routes.md docs/helix/02-design || true shows a coherent design note. 2. The design states that metrics are advisory and shared leases are required for cross-process stickiness. 3. Follow-up implementation beads exist if a concrete shared backend is selected. 4. ddx doc stale exits successfully or reports no stale routing docs caused by this design.
    </acceptance>
    <labels>area:routing, kind:design</labels>
  </bead>

  <changed-files>
    <file>docs/helix/01-frame/features/FEAT-004-model-routing.md</file>
    <file>docs/helix/02-design/adr/ADR-005-smart-routing-replaces-model-routes.md</file>
    <file>docs/helix/02-design/plan-2026-05-05-shared-lease-backend.md</file>
  </changed-files>

  <governing>
    <ref id="FEAT-004" path="benchmark-results/beadbench/run-20260423T021643Z/helix-build-selector-readiness__codex-gpt54__r1/verify-worktree/docs/helix/01-frame/features/FEAT-004-plugin-packaging.md" title="Feature Specification: FEAT-004 - Plugin Packaging">
      <content>
<untrusted-data>
---
dun:
  id: FEAT-004
  depends_on:
    - helix.prd
    - FEAT-002
---
# Feature Specification: FEAT-004 - Plugin Packaging

**Feature ID**: FEAT-004
**Status**: Draft
**Priority**: P1
**Owner**: HELIX maintainers

## Overview

HELIX is currently installed via `scripts/install-local-skills.sh`, which
creates symlinks from `~/.claude/skills/` and `~/.agents/skills/` back into
the repo checkout. This works but has operational problems: new skills are
invisible until the installer is re-run, the symlinks are absolute paths tied
to a single checkout location, and the approach bypasses Claude Code's native
plugin discovery entirely.

This feature makes HELIX a proper Claude Code plugin so that skills, the CLI,
shared workflow resources, and hooks are discovered automatically through the
plugin manifest — no manual installer step required.

## Problem Statement

- **Current situation**: `install-local-skills.sh` symlinks each skill into
  `~/.claude/skills/` and `~/.agents/skills/`, and installs a CLI launcher
  into `~/.local/bin/helix`. Adding a new skill (e.g., `helix-worker`) has no
  effect until the installer is re-run. Users hit "Unknown skill" errors with
  no indication that a reinstall is needed.
- **Pain points**:
  1. Silent skill discovery failures — new skills exist on disk but are not
     registered.
  2. Absolute symlinks break when the repo moves or is checked out elsewhere.
  3. No versioning, no manifest, no way for Claude Code to reason about HELIX
     as a coherent package.
  4. `bin/helix` is installed via a side-channel (`~/.local/bin`) rather than
     through the plugin `bin/` PATH injection.
  5. Enterprise and multi-repo deployments have no standard distribution path.
- **Desired outcome**: `claude --plugin-dir ./path-to-helix` (or a plugin
  install) makes all HELIX skills, the CLI, and shared resources available
  immediately — no symlinks, no re-running installers, no absolute paths.

## Design

### Plugin layout

The HELIX repo root *is* the plugin root. No separate build or copy step:

```
helix/                              # plugin root
├── .claude-plugin/
│   └── plugin.json                 # manifest
├── skills/                         # 17+ skills, auto-discovered
│   ├── helix-run/
│   │   └── SKILL.md
│   ├── helix-worker/
│   │   └── SKILL.md
│   └── ...
├── workflows/                      # shared resource library
│   ├── actions/
│   ├── EXECUTION.md
│   ├── ratchets.md
│   └── ...
├── bin/                            # added to Bash PATH by plugin loader
│   └── helix                       # CLI entrypoint (thin wrapper)
├── scripts/                        # implementation scripts
│   ├── helix                       # actual CLI
│   ├── tracker.sh
│   └── install-local-skills.sh     # legacy, dev convenience only
├── settings.json                   # plugin default settings (optional)
└── hooks/
    └── hooks.json                  # plugin hooks (optional)
```

### Manifest (`plugin.json`)

```json
{
  "name": "helix",
  "version": "0.1.0",
  "description": "HELIX development control system — supervisory autopilot for AI-assisted software delivery",
  "author": {
    "name": "Erik LaBianca",
    "url": "https://github.com/easel"
  },
  "repository": "https://github.com/easel/helix",
  "license": "MIT",
  "skills": "./skills/",
  "hooks": "./hooks/hooks.json"
}
```

Key decisions:
- `skills` points to the existing `skills/` directory — no move needed.
- `bin/helix` is a thin wrapper that invokes `${CLAUDE_PLUGIN_ROOT}/scripts/helix`.
  The plugin loader adds `bin/` to `PATH` automatically.
- Skills reference shared resources via `${CLAUDE_PLUGIN_ROOT}/workflows/`.
- No `commands/` directory — HELIX uses skills exclusively.

### Resource resolution

Skills currently reference `workflows/` via relative paths that assume the repo
root is reachable from the skill directory. The plugin model formalizes this:

- **Inside skill prompts**: Use `${CLAUDE_PLUGIN_ROOT}/workflows/` for
  references that must resolve at runtime.
- **Inside the CLI**: `$HELIX_ROOT` (already used) resolves to the plugin root
  when invoked via the plugin `bin/` path.
- **Validation**: The skill package validator (`tests/validate-skills.sh`) must
  verify that every `workflows/` reference in a SKILL.md can resolve from the
  plugin root.

### Relationship to `install-local-skills.sh`

The installer becomes a **development convenience** for contributors who want
HELIX skills available outside of `--plugin-dir` mode (e.g., when working in
other repos). It is no longer the primary installation path.

The installer should:
1. Detect whether the plugin manifest exists.
2. Print a recommendation to use `--plugin-dir` or `plugin install` instead.
3. Continue with symlink installation for users who explicitly want it.

### Installation modes

| Mode | Mechanism | Use case |
|------|-----------|----------|
| Plugin (local) | `claude --plugin-dir /path/to/helix` | Primary — development and daily use |
| Plugin (installed) | `claude plugin install helix` | Distribution — when marketplace is available |
| Plugin (project) | `.claude/settings.json` with plugin reference | Team — checked into adopting repos |
| Symlink (legacy) | `scripts/install-local-skills.sh` | Development convenience only |

### Skill namespacing

When installed as a plugin, skills are namespaced: `/helix:helix-run`,
`/helix:helix-worker`, etc. The unqualified names (`/helix-run`) continue to
work when no other plugin provides a conflicting skill name.

## Requirements

### Functional Requirements

1. The HELIX repo must contain a `.claude-plugin/plugin.json` manifest that
   declares HELIX as a Claude Code plugin.
2. `bin/helix` must be a thin wrapper that resolves `HELIX_ROOT` to
   `${CLAUDE_PLUGIN_ROOT}` and delegates to `scripts/helix`.
3. All HELIX skills must be discoverable through the plugin's `skills/`
   directory without manual symlink installation.
4. Shared resources in `workflows/` must be accessible from skills via
   `${CLAUDE_PLUGIN_ROOT}/workflows/`.
5. Adding a new skill (creating `skills/<name>/SKILL.md`) must make it
   available immediately in the next Claude Code session — no reinstall step.
6. `install-local-skills.sh` must remain functional as a development
   convenience but must not be the documented primary installation path.
7. The skill package validator must verify plugin layout integrity: manifest
   exists, skills resolve, shared resources reachable.
8. Plugin hooks (if any) must be declared in `hooks/hooks.json` and loaded
   by the plugin system.

### Non-Functional Requirements

- **Portability**: The plugin must work regardless of the repo's absolute path.
- **Zero-copy**: The repo root *is* the plugin root — no build step or file
  duplication.
- **Backward compatibility**: Existing `install-local-skills.sh` users must
  not be broken. The installer continues to work but prints a deprecation
  notice.
- **Testability**: Plugin layout validation must be deterministic and runnable
  in CI.

## User Stories

### US-001: Install HELIX as a plugin [FEAT-004]
**As a** HELIX operator
**I want** to add HELIX to my Claude Code session with `--plugin-dir`
**So that** all skills, the CLI, and shared resources are available immediately

**Acceptance Criteria:**
- [ ] Given a HELIX checkout, when `claude --plugin-dir /path/to/helix` starts,
  then all HELIX skills appear in the skill list.
- [ ] Given the plugin is loaded, when the user invokes `/helix-run`, then the
  skill executes with access to `workflows/` resources.
- [ ] Given the plugin is loaded, when `helix status` is run in Bash, then the
  CLI is on PATH and functional.

### US-002: Add a new skill without reinstalling [FEAT-004]
**As a** HELIX maintainer
**I want** new skills to be available in the next session after I create the file
**So that** I do not need to remember to re-run an installer

**Acceptance Criteria:**
- [ ] Given a new `skills/helix-foo/SKILL.md` is created, when a new Claude
  Code session starts with the plugin, then `/helix-foo` is available.
- [ ] Given the old installer has not been re-run, when the plugin is loaded,
  then the new skill is still available.

### US-003: Team distribution via project settings [FEAT-004]
**As a** team lead adopting HELIX for a project
**I want** to declare HELIX as a plugin dependency in `.claude/settings.json`
**So that** all team members get HELIX skills automatically

**Acceptance Criteria:**
- [ ] Given `.claude/settings.json` references the HELIX plugin, when a team
  member starts Claude Code in the project, then HELIX skills are available.
- [ ] Given the plugin is loaded from project settings, when the team member
  invokes `helix run`, then the CLI and shared resources resolve correctly.

### US-004: Validate plugin layout [FEAT-004]
**As a** HELIX maintainer
**I want** CI to catch broken plugin layouts before merge
**So that** users never get a plugin that silently fails

**Acceptance Criteria:**
- [ ] Given `tests/validate-skills.sh` runs, when the plugin manifest is
  missing or malformed, then the test fails with a clear error.
- [ ] Given a skill references a `workflows/` resource that does not exist,
  when the validator runs, then it reports the broken reference.

## Edge Cases and Error Handling

- **Plugin loaded without `workflows/`**: Skills must fail clearly with
  "HELIX shared resources not found at ${CLAUDE_PLUGIN_ROOT}/workflows/"
  rather than silently producing degraded output.
- **Both plugin and symlink install active**: Plugin takes precedence (Claude
  Code's plugin skills override user-level skills of the same name). The
  symlink install becomes a no-op when the plugin is active.
- **Repo moved after symlink install**: Symlinks break (existing behavior).
  Plugin mode is unaffected since it uses the current plugin root.
- **Multiple HELIX versions**: If two plugin dirs both provide `helix-*`
  skills, Claude Code's plugin precedence rules apply. Users should not load
  two HELIX plugins simultaneously.

## Success Metrics

- HELIX is loadable as a Claude Code plugin with zero manual setup beyond
  `--plugin-dir`.
- New skills are available in the next session without any installer step.
- `install-local-skills.sh` is no longer referenced in primary documentation
  as the installation path.
- Plugin layout validation runs in CI and catches broken layouts.

## Constraints and Assumptions

- The HELIX repo root doubles as the plugin root — no separate packaging step.
- Claude Code's plugin loader must support `bin/` PATH injection and
  `${CLAUDE_PLUGIN_ROOT}` variable resolution in skill prompts.
- The plugin manifest format follows the Claude Code plugin specification.
- Marketplace distribution is a future concern — local `--plugin-dir` is
  sufficient for V1.

## Dependencies

- **FEAT-002**: CLI feature spec (installation section needs updating)
- **SD-001**: Solution design (packaging component needs plugin layout)
- **helix.prd**: PRD packaging requirements
- Claude Code plugin specification (external dependency)

## Out of Scope

- Marketplace publishing workflow
- Plugin auto-update mechanism
- Plugin configuration UI
- Enterprise managed-settings distribution (future, once marketplace exists)

## Open Questions

- Does `${CLAUDE_PLUGIN_ROOT}` resolve inside SKILL.md reference paths, or
  only in hook commands and MCP configs? If not, skills may need a different
  mechanism to locate `workflows/`.
- Should the plugin declare MCP servers (e.g., for tracker access) or keep
  tracker interaction purely CLI-based?
- What is the right versioning strategy — semver tied to HELIX releases, or
  independent plugin version?
</untrusted-data>
      </content>
    </ref>
  </governing>

  <diff rev="a2c16887a78fc734118f3da02742b3290b1cbd00">
<untrusted-data>
diff --git a/docs/helix/01-frame/features/FEAT-004-model-routing.md b/docs/helix/01-frame/features/FEAT-004-model-routing.md
index 76e9608..5991d74 100644
--- a/docs/helix/01-frame/features/FEAT-004-model-routing.md
+++ b/docs/helix/01-frame/features/FEAT-004-model-routing.md
@@ -251,7 +251,9 @@ prompt behavior and must not be reused for model policy or routing.
      sticky assignment and fallback load count. On multiple machines, correct
      cross-process stickiness and fair distribution require a shared lease
      backend; server metrics are advisory and racy, not a replacement for
-     shared leases.
+     shared leases. See
+     [plan-2026-05-05-shared-lease-backend.md](../../02-design/plan-2026-05-05-shared-lease-backend.md)
+     for the lease-record and atomic acquire/refresh/release contract.
 30d. `vllm` and `llama-server` utilization is provider-owned and type-derived.
      `vllm` uses root `/metrics`. `llama-server` uses root `/metrics` when
      started with `--metrics` and root `/slots` as fallback. A configured
diff --git a/docs/helix/02-design/adr/ADR-005-smart-routing-replaces-model-routes.md b/docs/helix/02-design/adr/ADR-005-smart-routing-replaces-model-routes.md
index 01140f7..84451f3 100644
--- a/docs/helix/02-design/adr/ADR-005-smart-routing-replaces-model-routes.md
+++ b/docs/helix/02-design/adr/ADR-005-smart-routing-replaces-model-routes.md
@@ -165,7 +165,8 @@ probes. Probe failure makes utilization unknown/stale, not unavailable; routing
 falls back to service-owned in-flight lease counts. In multi-machine
 deployments, a shared lease backend is required for correct cross-process
 stickiness and fair distribution because server metrics alone are sampled and
-racy.
+racy. The shared lease contract is specified in
+[plan-2026-05-05-shared-lease-backend.md](../plan-2026-05-05-shared-lease-backend.md).
 
 ### Hard Constraints
 
diff --git a/docs/helix/02-design/plan-2026-05-05-shared-lease-backend.md b/docs/helix/02-design/plan-2026-05-05-shared-lease-backend.md
new file mode 100644
index 0000000..30999a1
--- /dev/null
+++ b/docs/helix/02-design/plan-2026-05-05-shared-lease-backend.md
@@ -0,0 +1,129 @@
+# Design Note: Shared Lease Backend for Sticky Route Leases
+
+**Date**: 2026-05-05
+**Status**: Proposed
+**Scope**: Multi-machine sticky routing only; no backend implementation in this note
+
+## Context
+
+ADR-005 and FEAT-004 already establish that sticky route leases preserve
+worker affinity and that server metrics are advisory. This note makes the
+cross-process contract explicit so the routing docs do not have to repeat the
+backend semantics in multiple places.
+
+The key operational fact is that server metrics are sampled observations of a
+single endpoint. They can help rank equivalent endpoints, but they cannot be
+the authority for stickiness across processes:
+
+- two processes can observe the same endpoint at different moments and make
+  conflicting decisions
+- a healthy-looking metric sample can race with a new request, so load derived
+  only from metrics is stale as soon as it is read
+- a process-local view cannot prevent two workers on different machines from
+  assigning the same sticky key to different endpoints
+
+For single-machine deployments, in-process route leases remain authoritative.
+For multi-machine deployments, the system needs a shared lease backend so the
+same sticky key resolves to one live owner across processes.
+
+## Lease Record
+
+A shared sticky lease record must include:
+
+- `sticky_key` - the validated request sequence identity, normally the
+  correlation ID or equivalent worker/session sequence key
+- `provider` - the provider source or type
+- `endpoint` - the concrete endpoint identity or selector
+- `model` - the resolved concrete model string
+- `owner` - the owning service instance or worker identity
+- `expires_at` - the lease deadline
+- `refreshed_at` - the last successful refresh timestamp
+
+Recommended additional fields:
+
+- `lease_token` - an opaque value returned on acquire and required for
+  refresh/release
+- `generation` - a monotonic compare-and-swap value for optimistic updates
+- `reason` - optional diagnostic text for invalidation or release
+
+The record is keyed by sticky route identity, not by model alone. That lets the
+same model exist on multiple equivalent endpoints while preserving affinity for
+one long-running worker.
+
+## Atomic Semantics
+
+The backend contract is intentionally small:
+
+### Acquire
+
+- Acquire succeeds only if no unexpired lease exists for the same
+  `sticky_key`, or if the caller is replacing its own expired lease with a new
+  token.
+- Acquire writes the full lease record atomically and returns the lease token
+  plus the new expiry.
+- If another owner already holds an unexpired lease, acquire fails without
+  partially updating the record.
+
+### Refresh
+
+- Refresh succeeds only when the caller presents the current lease token for
+  the active owner.
+- Refresh extends `expires_at` atomically and updates `refreshed_at`.
+- A refresh after expiry is a miss, not a resurrection. The caller must
+  acquire again.
+
+### Release
+
+- Release succeeds only when the caller owns the current lease token.
+- Release is idempotent: releasing an already-missing lease is a no-op.
+- Release removes the record or marks it vacant so a later acquire can claim it
+  without ambiguity.
+
+### Invalidation
+
+- If the selected endpoint stops advertising the resolved model, the lease may
+  be released early or allowed to expire, but a new acquire must not reuse it
+  blindly.
+- If the backend observes a conflicting owner, the stale lease loses on the
+  next acquire or refresh attempt.
+
+## Backend Choice
+
+This note intentionally defers the concrete backend choice.
+
+Either Redis or Postgres can satisfy the lease contract:
+
+- Redis fits a short-lived lease with `SET ... NX PX`, compare-and-delete
+  release, and token-checked refresh
+- Postgres fits the same contract with row-level transactions and a unique key
+  on `sticky_key`
+
+The implementation bead should choose one backend only when the project is
+ready to commit to operational tradeoffs such as deployment simplicity,
+consistency guarantees, and observability expectations.
+
+## Failure Behavior
+
+If the shared lease backend is unavailable:
+
+- single-machine routing continues with in-process leases only
+- multi-machine routing treats cross-process stickiness as degraded rather
+  than pretending it is authoritative
+- new sticky assignments fall back to the best local process view, but the
+  routing evidence must report that the shared backend was unavailable
+- no endpoint should be marked unavailable solely because the shared lease
+  backend is down
+
+This preserves request progress while making the stickiness loss explicit. The
+system is allowed to route, but it must not claim a cross-process lease it
+cannot actually enforce.
+
+## Follow-Up
+
+If a concrete backend is selected, create implementation beads for:
+
+- backend client and lease store
+- atomic acquire/refresh/release paths
+- routing integration and evidence reporting
+- failure-mode tests for unavailable backend and lease conflicts
+
</untrusted-data>
  </diff>

  <instructions>
You are reviewing a bead implementation against its acceptance criteria.

For each acceptance-criteria (AC) item, decide whether it is implemented correctly, then assign one overall verdict:

- APPROVE — every AC item is fully and correctly implemented.
- REQUEST_CHANGES — some AC items are partial or have fixable minor issues.
- BLOCK — at least one AC item is not implemented or incorrectly implemented; or the diff is insufficient to evaluate.

## Required output format (schema_version: 1)

Respond with EXACTLY one JSON object as your final response, fenced as a single ```json … ``` code block. Do not include any prose outside the fenced block. The JSON must match this schema:

```json
{
  "schema_version": 1,
  "verdict": "APPROVE",
  "summary": "≤300 char human-readable verdict justification",
  "findings": [
    { "severity": "info", "summary": "what is wrong or notable", "location": "path/to/file.go:42" }
  ]
}
```

Rules:
- "verdict" must be exactly one of "APPROVE", "REQUEST_CHANGES", "BLOCK".
- "severity" must be exactly one of "info", "warn", "block".
- Output the JSON object inside ONE fenced ```json … ``` block. No additional prose, no extra fences, no markdown headings.
- Do not echo this template back. Do not write the words APPROVE, REQUEST_CHANGES, or BLOCK anywhere except as the JSON value of the verdict field.
  </instructions>
</bead-review>
