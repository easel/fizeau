<bead-review>
  <bead id="fizeau-33ed2078" iter=1>
    <title>Document Ollama utilization signals</title>
    <description>
Map Ollama request metrics and loaded-model inventory into the provider-signal matrix. Document native /api/chat or /api/generate timing fields, /api/ps inventory, context length, cache absence, and unknown-signal fallback.
    </description>
    <acceptance>
1. The matrix or companion note maps Ollama utilization/performance/context signals to concrete endpoints. 2. Freshness and unknown-fallback behavior are documented. 3. Any missing live probe is either implemented or called out as a documented gap with a clear next-step bead.
    </acceptance>
    <labels>area:provider, area:docs, kind:task</labels>
  </bead>

  <changed-files>
    <file>docs/research/provider-signal-matrix-2026-05-06.md</file>
    <file>docs/resources/local-server-platform-metrics-2026-05-06.md</file>
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

  <diff rev="520d1ab8911b43bae6d6bb9b705190422b591f28">
<untrusted-data>
diff --git a/docs/research/provider-signal-matrix-2026-05-06.md b/docs/research/provider-signal-matrix-2026-05-06.md
index 85fbf011..8b3edeb7 100644
--- a/docs/research/provider-signal-matrix-2026-05-06.md
+++ b/docs/research/provider-signal-matrix-2026-05-06.md
@@ -25,7 +25,7 @@ package already caches a prior observation and can return `FreshnessStale`.
 | LM Studio | No live utilization probe in the provider package; only model discovery and request success/failure evidence | TTFT and request timing are derived from request execution, not from a dedicated status endpoint here | `LookupModelLimits` reads `loaded_context_length` and falls back to `max_context_length` from `/api/v0/models/<id>` | OpenAI-compatible response usage may carry cached-token details when the backend returns them; there is no dedicated cache probe in this package | `/api/v0/models/<id>` for context; OpenAI-compatible `/v1/models` and `/v1/chat/completions` for discovery and requests | Context lookup is fresh when the HTTP call succeeds; otherwise the lookup returns zero values. No stale cache wrapper exists here today | Context length is eligibility-critical for exact pins and bounded routing | Model discovery order, request success, TTFT, any response-level cached-token telemetry | `internal/provider/lmstudio/lmstudio.go`, `internal/provider/lmstudio/lmstudio_test.go`, `internal/provider/openai/discovery_integration_test.go` |
 | OpenRouter | No live utilization probe in the provider package | Gateway-reported usage cost is available through `usage.cost`; request timing is still derived from the request path | `LookupModelLimits` reads `context_length` and `top_provider.max_completion_tokens` from `/models` | Cache signals are pass-through from upstream usage objects: OpenAI-family cached tokens, Anthropic cache read/write fields, or `usage.cost` as a gateway-side billing signal | `/models` for limits; upstream chat endpoint through the OpenAI-compatible transport | Context lookup is fresh when the HTTP call succeeds; otherwise it returns zero values. No stale cache wrapper exists here today | Context length is eligibility-critical for bounded routing; cost can be a policy input but should not gate availability alone | Gateway-reported cost, upstream cached tokens, request timing | `internal/provider/openrouter/openrouter.go`, `internal/provider/openrouter/openrouter_test.go`, `internal/provider/openrouter/openrouter_cost_test.go` |
 | Generic OpenAI-compatible providers | No live utilization probe in the provider package | Request latency and TTFT are derived from the execution path; response usage reports token counts | `openai.DiscoverModels` and model ranking are the discovery path; there is no dedicated context probe in this package | `prompt_tokens_details.cached_tokens` in usage when the upstream reports it | `/v1/models` and `/v1/chat/completions` on the configured OpenAI-compatible base URL | Discovery is fresh on success; otherwise model selection falls back to configured model or prior resolution. No stale utilization cache exists here today | Exact pins and discovered model availability are eligibility-critical; cached-token counts are not | Model discovery order, response usage, any upstream cached-token field, request timing | `internal/provider/openai/discovery.go`, `internal/provider/openai/discovery_test.go`, `internal/provider/openai/discovery_integration_test.go` |
-| Ollama | No live utilization probe in the provider package | Request latency and TTFT are derived from the request path | No dedicated context probe in this package | No dedicated cache telemetry in the OpenAI-compatible surface; cache behavior is only inferable from repeated request timing or native Ollama APIs outside this package | OpenAI-compatible `/v1` surface | Unknown when the request succeeds but no utilization data exists; no stale cache wrapper exists today | Context length from model metadata would be eligibility-critical if added; today it is not probed here | Request timing, model residency inference, repeated-turn latency deltas | `internal/provider/ollama/ollama.go`, `internal/provider/ollama/ollama_test.go` |
+| Ollama | Native `/api/chat` and `/api/generate` final-response timings: `total_duration`, `load_duration`, `prompt_eval_duration`, `eval_duration`, `prompt_eval_count`, and `eval_count` | TTFT, total wall time, prompt-eval throughput, generation throughput, and model-load timing from native chat/generate responses | `/api/ps` exposes loaded models with `context_length`, `size`, `size_vram`, `expires_at`, and model details for inventory/context discovery | No documented cache-hit or queue surface in Ollama today; cache pressure remains unknown unless an external probe is added | Native `/api/chat`, `/api/generate`, and `/api/ps`; the OpenAI-compatible `/v1` surface is compatibility-only | Request metrics are fresh per response; loaded-model inventory is fresh on a successful `/api/ps`; unknown on first failure, with no stale cache wrapper today | Context length from `/api/ps` or model metadata is eligibility-critical when known; cache absence is not a hard rejection | Request timing, model residency/inventory, repeated-turn latency deltas, context discovery | `internal/provider/ollama/ollama.go`, `internal/provider/ollama/ollama_test.go` |
 | Lucebox | No live utilization probe in the provider package | Request latency and TTFT are derived from the request path | No dedicated context probe in this package | No dedicated cache telemetry in the OpenAI-compatible surface | OpenAI-compatible `/v1` surface | Unknown when the request succeeds but no utilization data exists | Context limits are eligibility-critical if sourced from catalog or discovery; the provider wrapper does not probe them today | Request timing and any upstream usage fields | `internal/provider/lucebox/lucebox.go` |
 | Subprocess harnesses | No provider-level live utilization probe; routing evidence comes from CLI/TUI quota and usage capture | Native stream events, final event usage, and per-harness timing | Model discovery snapshots from `--help`, `--list-models`, or authenticated PTY discovery | `cache_read_tokens`, `cache_write_tokens`, `cache_tokens`, and provider-specific quota caches | CLI stdout JSON, PTY `/status` or `/usage`, stream JSON, model discovery commands | Fresh when captured during the current run; durable quota caches may return stale snapshots; missing capture should be treated as unknown | Exact model pins and quota exhaustion are eligibility-critical; missing live utilization is not by itself a hard rejection | Cached-token totals, reasoning-token totals, request timing, quota windows, model list order | `internal/harnesses/usage.go`, `internal/harnesses/codex/model_discovery.go`, `internal/harnesses/claude/model_discovery.go`, `internal/harnesses/gemini/model_discovery.go`, `internal/harnesses/pi/model_discovery.go`, `internal/harnesses/claude/quota_cache.go`, `internal/harnesses/codex/quota_cache.go`, `internal/harnesses/gemini/quota_cache.go` |
 
@@ -56,7 +56,7 @@ The following missing probes should be tracked explicitly as later beads:
 - `follow-up: add live utilization probe for LM Studio / OpenAI-compatible local servers`
 - `follow-up: add live utilization probe for OpenRouter gateway surfaces`
 - `follow-up: add live utilization probe for generic OpenAI-compatible providers`
-- `follow-up: add context or status probe for Ollama if routing starts using live model limits`
+- `follow-up: add live utilization probe for Ollama if routing starts requiring queue/cache pressure beyond /api/ps`
 - `follow-up: add harness-level utilization snapshot capture beyond usage/quota parsing`
 
 ## Freshness Rules
@@ -66,6 +66,7 @@ The following missing probes should be tracked explicitly as later beads:
   `FreshnessStale`.
 - A first-time failure or a surface with no implemented probe should return
   unknown evidence rather than pretending the signal exists.
-- Context-length lookups without a stale cache wrapper are fresh-only; they
-  return zero values on failure and should be treated as unknown by callers.
-
+- Ollama request metrics are fresh per completed response and `/api/ps`
+  inventory is fresh per successful poll; neither surface has a stale-cache
+  wrapper today, so callers should treat failures as unknown rather than
+  reusing older values.
diff --git a/docs/resources/local-server-platform-metrics-2026-05-06.md b/docs/resources/local-server-platform-metrics-2026-05-06.md
index e48a4cbf..d5196f5d 100644
--- a/docs/resources/local-server-platform-metrics-2026-05-06.md
+++ b/docs/resources/local-server-platform-metrics-2026-05-06.md
@@ -361,6 +361,9 @@ Capture implications:
 - Poll `/api/ps` for loaded-model inventory, context length, and VRAM size,
   but do not expect per-request queue depth, active request progress, or cache
   hit details from the documented API.
+- Because Ollama does not expose a documented queue/cache utilization probe in
+  this surface, routing should treat missing or failed `/api/ps` samples as
+  unknown rather than reusing older values.
 - Ollama request metrics are reliable and simple. Live utilization is shallower
   than oMLX/Rapid-MLX unless Fizeau supplements it with external process/GPU
   telemetry.
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
