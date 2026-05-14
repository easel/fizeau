<bead-review>
  <bead id="fizeau-60171fa8" iter=1>
    <title>Inventory provider utilization and performance signals</title>
    <description>
Document and codify which providers expose utilization, performance, context, and cache-related routing evidence. Cover vLLM, Rapid-MLX, oMLX, llama-server, LM Studio, OpenRouter/OpenAI-compatible providers, and subprocess harnesses.
    </description>
    <acceptance>
1. A provider-signal matrix documents utilization, performance, context length, cache indicators, source endpoints, freshness rules, and unknown-signal fallback.
2. The matrix distinguishes eligibility-critical signals from ranking-only signals.
3. Existing provider probes are mapped to the matrix, and missing probes are listed as explicit follow-up beads.
4. go test ./internal/provider/... -run "Utilization|Perf|Context|Probe" passes or the bead notes which providers are documentation-only for this step.
    </acceptance>
    <notes>
Provider-signal matrix added to docs/resources/local-server-platform-metrics-2026-05-06.md. Follow-up beads: fizeau-be3cb865 (LM Studio utilization probe), fizeau-33ed2078 (Ollama utilization signals), fizeau-4d01efdc (OpenRouter/OpenAI-compatible signal gaps), fizeau-89312cef (subprocess harness evidence signals). Verification: go test ./internal/provider/... -run "Utilization|Perf|Context|Probe" passed. This bead is docs-only for the remaining missing live probes; the gaps are tracked by the follow-up beads above.
    </notes>
    <labels>area:provider, area:docs, kind:task</labels>
  </bead>

  <changed-files>
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

  <diff rev="2e664938d5bf12126c66364f274e880a89dc3a19">
<untrusted-data>
diff --git a/docs/resources/local-server-platform-metrics-2026-05-06.md b/docs/resources/local-server-platform-metrics-2026-05-06.md
index 3b08b017..e48a4cbf 100644
--- a/docs/resources/local-server-platform-metrics-2026-05-06.md
+++ b/docs/resources/local-server-platform-metrics-2026-05-06.md
@@ -18,6 +18,39 @@ Rapid-MLX, LM Studio, and Ollama.
 | LM Studio | Native `/api/v1/chat` response `stats`; OpenAI usage fields | Native model-management endpoints; stream events for in-flight progress | tokens/s, TTFT, model-load time, prompt-processing events | Medium-high |
 | Ollama | Native `/api/generate` and `/api/chat` final response metrics | `/api/ps` for loaded models and VRAM footprint | total/load/prompt/generation durations, input/output tokens | High for request metrics, medium for live utilization |
 
+## Provider Signal Matrix
+
+Routing uses two different classes of evidence:
+
+- **Eligibility-critical** signals can make an endpoint preferred or
+  deprioritized for the current request because they reflect live load,
+  freshness, or a hard capability boundary.
+- **Ranking-only** signals improve ordering, telemetry, or display but do not
+  by themselves make a healthy endpoint ineligible.
+
+The matrix below maps the current provider surfaces to those classes and shows
+where the existing Go probes already live. Missing live probes are listed as
+tracked follow-up beads so the gap stays explicit.
+
+| Provider | Utilization signals | Performance signals | Context length / max tokens | Cache indicators | Source endpoint(s) | Freshness / unknown fallback | Signal class | Existing probe(s) | Follow-up bead |
+| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
+| vLLM | `num_requests_running`, `num_requests_waiting`, KV cache pressure | Prometheus latency histograms, token counters, request-queue timing | Model inventory from `/v1/models`; otherwise catalog/context metadata | `vllm:kv_cache_usage_perc` or `vllm:gpu_cache_usage_perc` | Server-root `/metrics` after stripping `/v1` | Fresh on a successful metrics fetch; stale cache or `unknown` on failure | Eligibility-critical: active/queued/cache pressure. Ranking-only: latency histograms, counters, context inventory | `internal/provider/vllm/utilization_probe.go`, `internal/provider/vllm/utilization_probe_test.go`, cassette tests | None |
+| Rapid-MLX | `num_running`, `num_waiting`, active-request snapshots | `ttft_s`, `tokens_per_second`, total prompt/completion counters | Model inventory / catalog metadata; no dedicated limit probe in this step | `cache_hit_type`, `cached_tokens`, `generated_tokens`, Metal active/peak/cache memory | `/v1/status` with root fallback when the base URL ends in `/v1` | Fresh on a successful status fetch; stale cache or `unknown` on failure | Eligibility-critical: active/waiting/cache pressure. Ranking-only: per-request timings, totals, memory | `internal/provider/rapidmlx/utilization_probe.go`, `internal/provider/rapidmlx/utilization_probe_test.go`, cassette tests | None |
+| oMLX | `active_requests`, `waiting_requests`, `cache_efficiency`, model memory used/max | `avg_prefill_tps`, `avg_generation_tps`, total request/token counters, uptime, loaded models | `LookupModelLimits` from `/v1/models/status` for `max_context_window` and `max_tokens` | `total_cached_tokens`, `cache_efficiency`, cache-state fields | `/api/status`; optional `/admin/api/stats`; `/v1/models/status` for limits | Fresh on a successful status fetch; stale cache or `unknown` on failure | Eligibility-critical: active/waiting/cache efficiency and memory pressure. Ranking-only: totals, TPS, loaded-model inventory, limit discovery | `internal/provider/omlx/utilization_probe.go`, `internal/provider/omlx/utilization_probe_test.go`, `internal/provider/omlx/omlx.go`, `internal/provider/omlx/omlx_test.go` | None |
+| llama-server | `requests_processing`, `requests_deferred`, slot occupancy | Per-slot `is_processing`, slot count, cache ratio | Slot metadata and `/v1/models` inventory; no dedicated context probe in this step | `llamacpp:kv_cache_usage_ratio`, `/slots` occupancy ratio | `/metrics` first, then `/slots` fallback on the server root | Fresh on a successful metrics or slots fetch; stale cache or `unknown` on failure | Eligibility-critical: processing/deferred requests and slot occupancy. Ranking-only: slot count / derived occupancy | `internal/provider/llamaserver/utilization_probe.go`, `internal/provider/llamaserver/utilization_probe_test.go`, cassette tests | None |
+| LM Studio | No provider-owned live utilization probe yet; use per-request stats and model-state evidence | Native `/api/v1/chat` stats: `time_to_first_token_seconds`, `tokens_per_second`, `model_load_time_seconds` | `LookupModelLimits` from `/api/v0/models/{model}` using `loaded_context_length` or `max_context_length` | No dedicated cache indicator in the current surface | Native `/api/v1/chat`; `/api/v0/models/{model}`; OpenAI-compatible `/v1/chat/completions` for compatibility | Per-request stats are fresh; limit discovery is stale/unknown when the model endpoint is unreachable | Ranking-only for this step: context limits and per-request timing. Eligibility-critical utilization is not yet probed | `internal/provider/lmstudio/lmstudio.go`, `internal/provider/lmstudio/lmstudio_test.go`, `internal/provider/openai/discovery.go` | `fizeau-be3cb865` |
+| OpenRouter / generic OpenAI-compatible providers | No provider-owned live utilization probe yet; quota headers and response usage are the available routing evidence | Response usage, OpenRouter gateway cost, rate-limit/quota headers | `LookupModelLimits` from `/models` for `context_length` and `top_provider.max_completion_tokens` | OpenRouter gateway cost attribution; no cache-usage surface in the generic OpenAI-compatible layer | `/v1/models`; OpenRouter response headers; OpenAI-compatible chat completions | Per-response and per-model-list data are fresh; utilization is `unknown` when no gateway probe exists | Ranking-only for this step: context limits, cost attribution, and quota evidence | `internal/provider/openrouter/openrouter.go`, `internal/provider/openrouter/openrouter_test.go`, `internal/provider/openai/discovery.go`, `internal/provider/quotaheaders/quotaheaders.go` | `fizeau-4d01efdc` |
+| Ollama | No provider-owned live utilization probe yet; request metrics and loaded-model inventory are the observable surfaces | `total_duration`, `load_duration`, `prompt_eval_duration`, `eval_duration`, input/output token counts | `/api/ps` model inventory exposes `context_length`; model docs expose additional detail | No documented cache-hit signal in the current provider wrapper | Native `/api/chat`, `/api/generate`, `/api/ps` | Request metrics are fresh; live utilization is `unknown` without a dedicated probe | Ranking-only for this step: request timing, tokens, and model inventory. Eligibility-critical utilization is not yet probed | `internal/provider/ollama/ollama.go`, `internal/provider/ollama/ollama_test.go` | `fizeau-33ed2078` |
+| Subprocess harnesses | No provider-owned endpoint utilization; use harness quota/account/model-evidence freshness instead | Request runtime, final text latency, progress events, cancellation timing | Harness-specific model discovery / quota cache / config metadata, not a live server probe | No cache indicator on the provider surface; route decisions rely on durable quota caches instead | PTY/subprocess execution, harness cassettes, quota/account discovery, session logs | Freshness comes from durable cache state; missing or stale evidence should fall back to service-owned lease counts or mark the harness secondary | Eligibility-critical: quota/account freshness and hard pins. Ranking-only: progress and latency evidence | `internal/harnesses/*`, `internal/serviceimpl/execute_subprocess.go`, `internal/provider/conformance/run.go`, harness-specific quota/model discovery tests | `fizeau-89312cef` |
+
+The current implementation therefore splits cleanly into two groups:
+
+1. Providers with concrete live utilization probes today: vLLM, Rapid-MLX,
+   oMLX, and llama-server.
+2. Providers with useful routing evidence but no live utilization probe in this
+   bead: LM Studio, OpenRouter/generic OpenAI-compatible providers, Ollama, and
+   subprocess harnesses.
+
 ## Cross-Platform Capture Model
 
 Fizeau should treat local server metric capture as three related but separate
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
