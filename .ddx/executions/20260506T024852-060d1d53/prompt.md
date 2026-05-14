<bead-review>
  <bead id="fizeau-b9d31351" iter=1>
    <title>Add oMLX utilization tracking cassettes</title>
    <description>
Implement normalized utilization tracking for the existing oMLX provider type. oMLX is an OpenAI/Anthropic-compatible Apple Silicon server with default Fizeau URL http://localhost:1235/v1. It exposes serving stats through `/api/status` and richer admin stats through `/admin/api/stats`; request usage also supports `stream_options.include_usage`.

In-scope files:
- internal/provider/omlx utilization probe code and tests
- shared provider utilization helpers if needed
- provider cassette testdata for oMLX
- route/status integration tests only where needed to prove normalized utilization is consumed
- docs/helix/01-frame/features/FEAT-003-providers.md and FEAT-004-model-routing.md acceptance text if oMLX utilization needs explicit AC coverage

Record-mode environment:
- Requires an oMLX server running on a macOS Apple Silicon device.
- Use Vidar for the canonical live endpoint: `http://vidar:1235/v1`.
- Record mode should allow overriding the base URL with an env var, but default documented operator target is Vidar port 1235.

Requirements:
- Derive status endpoint from OpenAI base URL. For `http://vidar:1235/v1`, probe `http://vidar:1235/api/status`.
- Parse `/api/status` fields: total requests, active requests, waiting requests, total prompt/completion/cached tokens, cache efficiency, average prefill/generation TPS, loaded models, model memory used/max, and uptime.
- Optionally support `/admin/api/stats` only when explicit admin credentials/config are supplied; normal utilization tracking must work without admin access.
- Normalize to the same endpoint utilization shape used by vLLM and llama-server: active requests, queued requests, cache usage/hit metadata when known, source, freshness, observed time, and optional memory fields.
- Probe failures return stale or unknown utilization and do not make the endpoint unavailable.

Out of scope:
- Changing oMLX chat/reasoning behavior.
- Implementing Rapid-MLX support.
- Multi-machine shared lease backends.
    </description>
    <acceptance>
1. `go test ./internal/provider/omlx ./internal/provider/... -run 'OMLX.*Utilization|OMLX.*Cassette|Utilization'` passes in replay mode using committed cassettes.
2. Record mode is gated by `FIZEAU_RECORD_PROVIDER_CASSETTES=1` plus a documented oMLX live-server URL env var and can record against `http://vidar:1235/v1`.
3. Replay cassettes include `/v1/models`, `/api/status`, a minimal `/v1/chat/completions` request, and under-load `/api/status` evidence when feasible.
4. Replay parsing extracts active requests, waiting requests, total prompt/completion/cached tokens, cache efficiency, average prefill/generation TPS, model memory used/max, and loaded model identifiers when present.
5. Admin `/admin/api/stats` support, if implemented, is optional and never required for the base replay tests.
6. Probe failure tests verify stale/unknown utilization does not mark the provider endpoint unavailable.
    </acceptance>
    <labels>area:provider, kind:test</labels>
  </bead>

  <changed-files>
    <file>docs/helix/01-frame/features/FEAT-003-providers.md</file>
    <file>docs/helix/01-frame/features/FEAT-004-model-routing.md</file>
    <file>internal/provider/omlx/testdata/cassettes/omlx_utilization.yaml</file>
    <file>internal/provider/omlx/utilization_probe.go</file>
    <file>internal/provider/omlx/utilization_probe_test.go</file>
    <file>internal/provider/utilization/utilization.go</file>
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

  <diff rev="06ab3a41cedef144d026278cbb294c914f7f91da">
<untrusted-data>
diff --git a/docs/helix/01-frame/features/FEAT-003-providers.md b/docs/helix/01-frame/features/FEAT-003-providers.md
index 9216def..0ecf7ac 100644
--- a/docs/helix/01-frame/features/FEAT-003-providers.md
+++ b/docs/helix/01-frame/features/FEAT-003-providers.md
@@ -182,6 +182,12 @@ type AttemptMetadata struct {
 15e. Utilization probe failure does not make an endpoint unavailable by itself.
      The provider returns stale or unknown utilization and routing falls back to
      service-owned in-flight lease counts and normal availability health.
+15f. `omlx` probes `GET /api/status` on the server root and normalizes active
+     requests, waiting requests, total prompt/completion/cached tokens, cache
+     efficiency, average prefill/generation TPS, loaded model identifiers, and
+     model memory used/max into a provider-independent endpoint utilization
+     signal. `GET /admin/api/stats` remains an optional admin-only extension
+     and is not required for the base utilization probe.
 
 #### Reasoning Configuration
 
@@ -382,6 +388,7 @@ type AttemptMetadata struct {
 | AC-FEAT-003-13 | vLLM utilization is recorded from a real CPU vLLM server with `go-vcr`: record mode installs or pulls the runtime, starts a trivial CPU model, records `/v1/models`, `/metrics`, minimal chat, and under-load metrics; replay mode is default and parses running, waiting, and cache-pressure metrics from the cassette. | `go test ./internal/provider/vllm ./... -run 'VLLM.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 go test ./internal/provider/vllm -run 'Record'` |
 | AC-FEAT-003-14 | llama-server utilization is recorded from a real llama.cpp server with `go-vcr`: record mode installs or pulls `llama-server`, starts a trivial CPU GGUF model with `--metrics` and `/slots` enabled, records `/v1/models`, `/metrics`, `/slots`, minimal chat, and busy-slot evidence; replay mode is default and parses processing, deferred, cache-ratio, and slot occupancy signals from the cassette. | `go test ./internal/provider/llamaserver ./... -run 'Llama.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 go test ./internal/provider/llamaserver -run 'Record'` |
 | AC-FEAT-003-15 | Rapid-MLX utilization is recorded from a real macOS Apple Silicon Rapid-MLX server with `go-vcr`: record mode requires `FIZEAU_RECORD_PROVIDER_CASSETTES=1` and `RAPID_MLX_RECORD_BASE_URL`, records `/v1/models`, `/v1/status`, minimal chat, and under-load status evidence when feasible; replay mode is default and parses running, waiting, total prompt/completion counters, Metal memory, cache details, and active-request timing fields from the cassette. | `go test ./internal/provider/rapidmlx ./internal/provider/... -run 'Rapid.*Utilization|Rapid.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 RAPID_MLX_RECORD_BASE_URL=http://host:8000/v1 go test ./internal/provider/rapidmlx -run 'Record'` |
+| AC-FEAT-003-16 | oMLX utilization is recorded from a real macOS Apple Silicon oMLX server with `go-vcr`: record mode requires `FIZEAU_RECORD_PROVIDER_CASSETTES=1` and an overrideable live-server URL such as `OMLX_URL`, records `/v1/models`, `/api/status`, a minimal chat completion, and under-load status evidence when feasible; replay mode is default and parses active and waiting requests, total prompt/completion/cached tokens, cache efficiency, average prefill/generation TPS, loaded model identifiers, and model memory used/max from the cassette while treating `/admin/api/stats` as optional. | `go test ./internal/provider/omlx ./internal/provider/... -run 'OMLX.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 OMLX_URL=http://vidar:1235/v1 go test ./internal/provider/omlx -run 'Record'` |
 
 ## Constraints and Assumptions
 
diff --git a/docs/helix/01-frame/features/FEAT-004-model-routing.md b/docs/helix/01-frame/features/FEAT-004-model-routing.md
index 5991d74..859bda1 100644
--- a/docs/helix/01-frame/features/FEAT-004-model-routing.md
+++ b/docs/helix/01-frame/features/FEAT-004-model-routing.md
@@ -254,11 +254,13 @@ prompt behavior and must not be reused for model policy or routing.
      shared leases. See
      [plan-2026-05-05-shared-lease-backend.md](../../02-design/plan-2026-05-05-shared-lease-backend.md)
      for the lease-record and atomic acquire/refresh/release contract.
-30d. `vllm` and `llama-server` utilization is provider-owned and type-derived.
-     `vllm` uses root `/metrics`. `llama-server` uses root `/metrics` when
-     started with `--metrics` and root `/slots` as fallback. A configured
-     OpenAI-compatible `base_url` ending in `/v1` is converted to the server root
-     for these probes.
+30d. `vllm`, `llama-server`, and `omlx` utilization is provider-owned and
+     type-derived. `vllm` uses root `/metrics`. `llama-server` uses root
+     `/metrics` when started with `--metrics` and root `/slots` as fallback.
+     `omlx` uses root `/api/status` and may optionally consume admin-only
+     `/admin/api/stats` when explicitly configured. A configured
+     OpenAI-compatible `base_url` ending in `/v1` is converted to the server
+     root for these probes.
 31. `agent.Run()` still receives one concrete `Provider` per attempt. `Execute`
     selects and dispatches the top candidate once.
 32. The selected concrete harness, provider source, endpoint, model, requested
diff --git a/internal/provider/omlx/testdata/cassettes/omlx_utilization.yaml b/internal/provider/omlx/testdata/cassettes/omlx_utilization.yaml
new file mode 100644
index 0000000..9845dd1
--- /dev/null
+++ b/internal/provider/omlx/testdata/cassettes/omlx_utilization.yaml
@@ -0,0 +1,225 @@
+---
+version: 2
+interactions:
+    - id: 0
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/v1/models
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 115
+        body: '{"object":"list","data":[{"id":"Qwen3.6-27B-MLX-8bit","object":"model","created":1778033308,"owned_by":"omlx"}]}'
+        headers:
+            Content-Length:
+                - "115"
+            Content-Type:
+                - application/json
+            Server:
+                - omlx
+        status: 200 OK
+        code: 200
+        duration: 104.5µs
+    - id: 1
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/api/status
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 342
+        body: |
+            {
+              "total_requests": 0,
+              "active_requests": 0,
+              "waiting_requests": 0,
+              "total_prompt_tokens": 0,
+              "total_completion_tokens": 0,
+              "total_cached_tokens": 0,
+              "cache_efficiency": 0.2,
+              "avg_prefill_tps": 0.0,
+              "avg_generation_tps": 0.0,
+              "loaded_models": [
+                "Qwen3.6-27B-MLX-8bit"
+              ],
+              "model_memory_used_bytes": 34359738368,
+              "model_memory_max_bytes": 42949672960,
+              "uptime_seconds": 1200.5
+            }
+        headers:
+            Content-Length:
+                - "342"
+            Content-Type:
+                - application/json
+            Server:
+                - omlx
+        status: 200 OK
+        code: 200
+        duration: 92.708µs
+    - id: 2
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 131
+        host: vcr.local
+        body: '{"max_tokens":8,"messages":[{"content":"Reply with one short word.","role":"user"}],"model":"Qwen3.6-27B-MLX-8bit","temperature":0}'
+        headers:
+            Content-Type:
+                - application/json
+        url: http://vcr.local/v1/chat/completions
+        method: POST
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 312
+        body: '{"id":"chatcmpl-omlx-1","object":"chat.completion","created":1778033309,"model":"Qwen3.6-27B-MLX-8bit","choices":[{"index":0,"message":{"role":"assistant","content":"swift"},"finish_reason":"stop"}],"usage":{"prompt_tokens":34,"completion_tokens":1,"total_tokens":35,"prompt_tokens_details":{"cached_tokens":0}}}'
+        headers:
+            Content-Length:
+                - "312"
+            Content-Type:
+                - application/json
+            Server:
+                - omlx
+        status: 200 OK
+        code: 200
+        duration: 221.375µs
+    - id: 3
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/api/status
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 358
+        body: |
+            {
+              "total_requests": 1,
+              "active_requests": 0,
+              "waiting_requests": 0,
+              "total_prompt_tokens": 34,
+              "total_completion_tokens": 1,
+              "total_cached_tokens": 0,
+              "cache_efficiency": 0.8125,
+              "avg_prefill_tps": 588.337,
+              "avg_generation_tps": 43.627,
+              "loaded_models": [
+                {
+                  "id": "Qwen3.6-27B-MLX-8bit"
+                }
+              ],
+              "model_memory_used_bytes": 34896609280,
+              "model_memory_max_bytes": 42949672960,
+              "uptime_seconds": 1201.5
+            }
+        headers:
+            Content-Length:
+                - "358"
+            Content-Type:
+                - application/json
+            Server:
+                - omlx
+        status: 200 OK
+        code: 200
+        duration: 95.083µs
+    - id: 4
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 207
+        host: vcr.local
+        body: '{"max_tokens":64,"messages":[{"content":"Write a 200-word paragraph about a small robot.","role":"user"}],"model":"Qwen3.6-27B-MLX-8bit","stream":true,"stream_options":{"include_usage":true},"temperature":0}'
+        headers:
+            Content-Type:
+                - application/json
+        url: http://vcr.local/v1/chat/completions
+        method: POST
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 428
+        body: |
+            : keep-alive
+
+            data: {"id":"chatcmpl-omlx-load","model":"Qwen3.6-27B-MLX-8bit","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}
+
+            : keep-alive
+
+            data: {"id":"chatcmpl-omlx-load","model":"Qwen3.6-27B-MLX-8bit","choices":[{"index":0,"delta":{"content":"The robot wakes and waits."},"finish_reason":null}]}
+
+            data: {"id":"chatcmpl-omlx-load","model":"Qwen3.6-27B-MLX-8bit","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":89,"completion_tokens":64,"total_tokens":153,"prompt_tokens_details":{"cached_tokens":32}}}
+
+            data: [DONE]
+        headers:
+            Content-Length:
+                - "428"
+            Content-Type:
+                - text/event-stream; charset=utf-8
+            Server:
+                - omlx
+        status: 200 OK
+        code: 200
+        duration: 1.872ms
+    - id: 5
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/api/status
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 362
+        body: |
+            {
+              "total_requests": 2,
+              "active_requests": 1,
+              "waiting_requests": 2,
+              "total_prompt_tokens": 123,
+              "total_completion_tokens": 65,
+              "total_cached_tokens": 32,
+              "cache_efficiency": 0.9,
+              "avg_prefill_tps": 550.0,
+              "avg_generation_tps": 43.6,
+              "loaded_models": [
+                "Qwen3.6-27B-MLX-8bit"
+              ],
+              "model_memory_used_bytes": 38654705664,
+              "model_memory_max_bytes": 42949672960,
+              "uptime_seconds": 1202.5
+            }
+        headers:
+            Content-Length:
+                - "362"
+            Content-Type:
+                - application/json
+            Server:
+                - omlx
+        status: 200 OK
+        code: 200
+        duration: 97.25µs
diff --git a/internal/provider/omlx/utilization_probe.go b/internal/provider/omlx/utilization_probe.go
new file mode 100644
index 0000000..e0cd9ee
--- /dev/null
+++ b/internal/provider/omlx/utilization_probe.go
@@ -0,0 +1,334 @@
+package omlx
+
+import (
+	"context"
+	"encoding/json"
+	"errors"
+	"fmt"
+	"io"
+	"net/http"
+	"strconv"
+	"strings"
+	"time"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+)
+
+// UtilizationProbe queries oMLX server-root observability endpoints and
+// normalizes them into the shared endpoint utilization shape.
+type UtilizationProbe struct {
+	baseURL string
+	client  *http.Client
+	cache   utilization.Cache
+}
+
+// NewUtilizationProbe creates a probe for an OpenAI-compatible oMLX base URL.
+func NewUtilizationProbe(baseURL string, client *http.Client) *UtilizationProbe {
+	if client == nil {
+		client = http.DefaultClient
+	}
+	return &UtilizationProbe{
+		baseURL: baseURL,
+		client:  client,
+	}
+}
+
+// Probe fetches /api/status from the server root and returns a normalized
+// sample. Failures return stale or unknown utilization instead of surfacing
+// endpoint unavailability.
+func (p *UtilizationProbe) Probe(ctx context.Context) utilization.EndpointUtilization {
+	snapshot, ok := p.probeStatus(ctx)
+	if !ok {
+		if stale, ok := p.cache.Stale(); ok {
+			return stale
+		}
+		return utilization.Unknown(utilization.SourceOMLXStatus)
+	}
+
+	return p.cache.Remember(snapshot.normalize())
+}
+
+func (p *UtilizationProbe) probeStatus(ctx context.Context) (omlxStatusSnapshot, bool) {
+	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
+	defer cancel()
+
+	body, err := p.get(reqCtx, utilization.ServerRoot(p.baseURL)+"/api/status")
+	if err != nil {
+		return omlxStatusSnapshot{}, false
+	}
+
+	snapshot, err := parseOMLXStatus(body)
+	if err != nil {
+		return omlxStatusSnapshot{}, false
+	}
+	return snapshot, true
+}
+
+func parseOMLXStatus(body string) (omlxStatusSnapshot, error) {
+	var raw any
+	dec := json.NewDecoder(strings.NewReader(body))
+	dec.UseNumber()
+	if err := dec.Decode(&raw); err != nil {
+		return omlxStatusSnapshot{}, err
+	}
+
+	payload, ok := raw.(map[string]any)
+	if !ok {
+		return omlxStatusSnapshot{}, errors.New("omlx status payload was not an object")
+	}
+
+	snapshot := omlxStatusSnapshot{
+		Raw: payload,
+	}
+	snapshot.TotalRequests = firstInt(payload, "total_requests", "requests_total")
+	snapshot.ActiveRequests = firstInt(payload, "active_requests", "requests_active", "num_running", "running")
+	snapshot.WaitingRequests = firstInt(payload, "waiting_requests", "requests_waiting", "num_waiting", "waiting")
+	snapshot.TotalPromptTokens = firstInt(payload, "total_prompt_tokens", "prompt_tokens", "prompt_tokens_total")
+	snapshot.TotalCompletionTokens = firstInt(payload, "total_completion_tokens", "completion_tokens", "completion_tokens_total")
+	snapshot.TotalCachedTokens = firstInt(payload, "total_cached_tokens", "cached_tokens", "cache_tokens")
+	snapshot.CacheEfficiency = firstFloat(payload, "cache_efficiency", "cache_usage", "cache_usage_ratio")
+	snapshot.AvgPrefillTPS = firstFloat(payload, "avg_prefill_tps", "prefill_tps", "avg_prefill_tokens_per_second")
+	snapshot.AvgGenerationTPS = firstFloat(payload, "avg_generation_tps", "generation_tps", "avg_generation_tokens_per_second")
+	snapshot.ModelMemoryUsedBytes = firstInt64(payload, "model_memory_used_bytes", "model_memory_used", "memory_used_bytes", "memory_used")
+	snapshot.ModelMemoryMaxBytes = firstInt64(payload, "model_memory_max_bytes", "model_memory_max", "memory_max_bytes", "memory_max")
+	snapshot.UptimeSeconds = firstFloat(payload, "uptime_seconds", "uptime_s", "uptime")
+	snapshot.LoadedModels = loadedModelIDs(payload)
+
+	if snapshot.CacheEfficiency == nil {
+		if cache, ok := firstMap(payload, "cache", "cache_stats", "cache_state"); ok {
+			snapshot.CacheEfficiency = firstFloat(cache, "efficiency", "usage", "ratio", "pressure")
+			if snapshot.TotalCachedTokens == 0 {
+				snapshot.TotalCachedTokens = firstInt(cache, "cached_tokens", "cache_tokens")
+			}
+		}
+	}
+
+	if snapshot.ModelMemoryUsedBytes == nil || snapshot.ModelMemoryMaxBytes == nil {
+		if model, ok := firstMap(payload, "model", "loaded_model", "memory", "memory_stats"); ok {
+			if snapshot.ModelMemoryUsedBytes == nil {
+				snapshot.ModelMemoryUsedBytes = firstInt64(model, "used_bytes", "used", "memory_used_bytes", "active_bytes")
+			}
+			if snapshot.ModelMemoryMaxBytes == nil {
+				snapshot.ModelMemoryMaxBytes = firstInt64(model, "max_bytes", "max", "memory_max_bytes", "peak_bytes")
+			}
+		}
+	}
+
+	return snapshot, nil
+}
+
+type omlxStatusSnapshot struct {
+	TotalRequests         int
+	ActiveRequests        int
+	WaitingRequests       int
+	TotalPromptTokens     int
+	TotalCompletionTokens int
+	TotalCachedTokens     int
+	CacheEfficiency       *float64
+	AvgPrefillTPS         *float64
+	AvgGenerationTPS      *float64
+	LoadedModels          []string
+	ModelMemoryUsedBytes  *int64
+	ModelMemoryMaxBytes   *int64
+	UptimeSeconds         *float64
+	Raw                   map[string]any
+}
+
+func (s omlxStatusSnapshot) normalize() utilization.EndpointUtilization {
+	out := utilization.EndpointUtilization{
+		ActiveRequests:         utilization.Int(s.ActiveRequests),
+		QueuedRequests:         utilization.Int(s.WaitingRequests),
+		TotalPromptTokens:      utilization.Int(s.TotalPromptTokens),
+		TotalCompletionTokens:  utilization.Int(s.TotalCompletionTokens),
+		CachedTokens:           utilization.Int(s.TotalCachedTokens),
+		Source:                 utilization.SourceOMLXStatus,
+		Freshness:              utilization.FreshnessUnknown,
+		MetalActiveMemoryBytes: s.ModelMemoryUsedBytes,
+		MetalPeakMemoryBytes:   s.ModelMemoryMaxBytes,
+	}
+	if s.CacheEfficiency != nil {
+		out.CacheUsage = utilization.Float64(*s.CacheEfficiency)
+	}
+	if s.AvgGenerationTPS != nil {
+		out.TokensPerSecond = utilization.Float64(*s.AvgGenerationTPS)
+	}
+	return out
+}
+
+func firstInt(payload map[string]any, keys ...string) int {
+	if v, ok := firstNumber(payload, keys...); ok {
+		return int(v)
+	}
+	return 0
+}
+
+func firstInt64(payload map[string]any, keys ...string) *int64 {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		if v, ok := int64Value(raw); ok {
+			return utilization.Int64(v)
+		}
+	}
+	return nil
+}
+
+func firstFloat(payload map[string]any, keys ...string) *float64 {
+	if v, ok := firstNumber(payload, keys...); ok {
+		return utilization.Float64(v)
+	}
+	return nil
+}
+
+func firstNumber(payload map[string]any, keys ...string) (float64, bool) {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		if v, ok := numberValue(raw); ok {
+			return v, true
+		}
+	}
+	return 0, false
+}
+
+func firstMap(payload map[string]any, keys ...string) (map[string]any, bool) {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		if m, ok := raw.(map[string]any); ok {
+			return m, true
+		}
+	}
+	return nil, false
+}
+
+func loadedModelIDs(payload map[string]any) []string {
+	for _, key := range []string{"loaded_models", "loaded_model_ids", "models"} {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		ids := collectModelIDs(raw)
+		if len(ids) > 0 {
+			return ids
+		}
+	}
+	return nil
+}
+
+func collectModelIDs(raw any) []string {
+	switch v := raw.(type) {
+	case []any:
+		ids := make([]string, 0, len(v))
+		for _, item := range v {
+			if id := modelIDFromValue(item); id != "" {
+				ids = append(ids, id)
+			}
+		}
+		return ids
+	case map[string]any:
+		if id := modelIDFromValue(v); id != "" {
+			return []string{id}
+		}
+	}
+	return nil
+}
+
+func modelIDFromValue(raw any) string {
+	switch v := raw.(type) {
+	case string:
+		return strings.TrimSpace(v)
+	case map[string]any:
+		for _, key := range []string{"id", "name", "model", "model_id"} {
+			if value, ok := v[key].(string); ok && strings.TrimSpace(value) != "" {
+				return strings.TrimSpace(value)
+			}
+		}
+	}
+	return ""
+}
+
+func numberValue(raw any) (float64, bool) {
+	switch v := raw.(type) {
+	case json.Number:
+		f, err := v.Float64()
+		return f, err == nil
+	case float64:
+		return v, true
+	case float32:
+		return float64(v), true
+	case int:
+		return float64(v), true
+	case int64:
+		return float64(v), true
+	case int32:
+		return float64(v), true
+	case uint:
+		return float64(v), true
+	case uint64:
+		return float64(v), true
+	case uint32:
+		return float64(v), true
+	case string:
+		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
+		return f, err == nil
+	default:
+		return 0, false
+	}
+}
+
+func int64Value(raw any) (int64, bool) {
+	switch v := raw.(type) {
+	case json.Number:
+		i, err := v.Int64()
+		return i, err == nil
+	case float64:
+		return int64(v), true
+	case float32:
+		return int64(v), true
+	case int:
+		return int64(v), true
+	case int64:
+		return v, true
+	case int32:
+		return int64(v), true
+	case uint:
+		return int64(v), true
+	case uint64:
+		return int64(v), true
+	case uint32:
+		return int64(v), true
+	case string:
+		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
+		return i, err == nil
+	default:
+		return 0, false
+	}
+}
+
+func (p *UtilizationProbe) get(ctx context.Context, endpoint string) (string, error) {
+	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
+	if err != nil {
+		return "", err
+	}
+	resp, err := p.client.Do(req)
+	if err != nil {
+		return "", err
+	}
+	defer resp.Body.Close()
+	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
+		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
+		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
+	}
+	body, err := io.ReadAll(resp.Body)
+	if err != nil {
+		return "", err
+	}
+	return string(body), nil
+}
diff --git a/internal/provider/omlx/utilization_probe_test.go b/internal/provider/omlx/utilization_probe_test.go
new file mode 100644
index 0000000..c3884af
--- /dev/null
+++ b/internal/provider/omlx/utilization_probe_test.go
@@ -0,0 +1,451 @@
+package omlx
+
+import (
+	"bytes"
+	"context"
+	"encoding/json"
+	"fmt"
+	"io"
+	"net/http"
+	"net/http/httptest"
+	"os"
+	"path/filepath"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/testutil"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+	"github.com/stretchr/testify/require"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
+)
+
+const omlxCassetteName = "omlx_utilization"
+
+func TestOMLXUtilizationProbe_ParseStatusAndNormalize(t *testing.T) {
+	body := strings.Join([]string{
+		`{`,
+		`  "total_requests": 9,`,
+		`  "active_requests": 2,`,
+		`  "waiting_requests": 3,`,
+		`  "total_prompt_tokens": 1234,`,
+		`  "total_completion_tokens": 567,`,
+		`  "total_cached_tokens": 77,`,
+		`  "cache_efficiency": 0.42,`,
+		`  "avg_prefill_tps": 588.337,`,
+		`  "avg_generation_tps": 43.627,`,
+		`  "loaded_models": [`,
+		`    "Qwen3.6-27B-MLX-8bit",`,
+		`    {"id":"Qwen3.5-27B-4bit"}`,
+		`  ],`,
+		`  "model_memory_used_bytes": 111,`,
+		`  "model_memory_max_bytes": 222,`,
+		`  "uptime_seconds": 12345.5`,
+		`}`,
+	}, "\n")
+
+	snapshot, err := parseOMLXStatus(body)
+	require.NoError(t, err)
+	require.Equal(t, 9, snapshot.TotalRequests)
+	require.Equal(t, 2, snapshot.ActiveRequests)
+	require.Equal(t, 3, snapshot.WaitingRequests)
+	require.Equal(t, 1234, snapshot.TotalPromptTokens)
+	require.Equal(t, 567, snapshot.TotalCompletionTokens)
+	require.Equal(t, 77, snapshot.TotalCachedTokens)
+	require.NotNil(t, snapshot.CacheEfficiency)
+	require.NotNil(t, snapshot.AvgPrefillTPS)
+	require.NotNil(t, snapshot.AvgGenerationTPS)
+	require.NotNil(t, snapshot.ModelMemoryUsedBytes)
+	require.NotNil(t, snapshot.ModelMemoryMaxBytes)
+	require.NotNil(t, snapshot.UptimeSeconds)
+	require.Equal(t, []string{"Qwen3.6-27B-MLX-8bit", "Qwen3.5-27B-4bit"}, snapshot.LoadedModels)
+	require.InDelta(t, 0.42, *snapshot.CacheEfficiency, 1e-9)
+	require.InDelta(t, 588.337, *snapshot.AvgPrefillTPS, 1e-9)
+	require.InDelta(t, 43.627, *snapshot.AvgGenerationTPS, 1e-9)
+	require.Equal(t, int64(111), *snapshot.ModelMemoryUsedBytes)
+	require.Equal(t, int64(222), *snapshot.ModelMemoryMaxBytes)
+	require.InDelta(t, 12345.5, *snapshot.UptimeSeconds, 1e-9)
+
+	sample := snapshot.normalize()
+	require.Equal(t, utilization.SourceOMLXStatus, sample.Source)
+	require.Equal(t, utilization.FreshnessUnknown, sample.Freshness)
+	require.NotNil(t, sample.ActiveRequests)
+	require.NotNil(t, sample.QueuedRequests)
+	require.NotNil(t, sample.CacheUsage)
+	require.NotNil(t, sample.CachedTokens)
+	require.NotNil(t, sample.TotalPromptTokens)
+	require.NotNil(t, sample.TotalCompletionTokens)
+	require.NotNil(t, sample.MetalActiveMemoryBytes)
+	require.NotNil(t, sample.MetalPeakMemoryBytes)
+	require.Equal(t, 2, *sample.ActiveRequests)
+	require.Equal(t, 3, *sample.QueuedRequests)
+	require.Equal(t, 77, *sample.CachedTokens)
+	require.Equal(t, 1234, *sample.TotalPromptTokens)
+	require.Equal(t, 567, *sample.TotalCompletionTokens)
+	require.Equal(t, int64(111), *sample.MetalActiveMemoryBytes)
+	require.Equal(t, int64(222), *sample.MetalPeakMemoryBytes)
+	require.InDelta(t, 0.42, *sample.CacheUsage, 1e-9)
+	require.InDelta(t, 43.627, *sample.TokensPerSecond, 1e-9)
+}
+
+func TestOMLXUtilizationProbe_CassetteReplay(t *testing.T) {
+	if testutil.ModeForEnvironment() == recorder.ModeRecordOnly {
+		t.Skip("record mode coverage is exercised in TestOMLXRecordCassetteAndUtilization")
+	}
+
+	rec, err := testutil.NewRecorder(testutil.CassettePath("testdata/cassettes", omlxCassetteName))
+	require.NoError(t, err)
+	t.Cleanup(func() {
+		require.NoError(t, rec.Stop())
+	})
+
+	probe := NewUtilizationProbe("http://replay.invalid/v1", rec.GetDefaultClient())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.SourceOMLXStatus, sample.Source)
+	require.Equal(t, utilization.FreshnessFresh, sample.Freshness)
+	require.NotNil(t, sample.ActiveRequests)
+	require.NotNil(t, sample.QueuedRequests)
+	require.NotNil(t, sample.CacheUsage)
+	require.NotNil(t, sample.TotalPromptTokens)
+	require.NotNil(t, sample.TotalCompletionTokens)
+	require.NotNil(t, sample.CachedTokens)
+	require.NotNil(t, sample.MetalActiveMemoryBytes)
+	require.NotNil(t, sample.MetalPeakMemoryBytes)
+	require.NotZero(t, sample.ObservedAt)
+	require.Equal(t, 0, *sample.ActiveRequests)
+	require.Equal(t, 0, *sample.QueuedRequests)
+}
+
+func TestOMLXUtilizationProbe_FailureReturnsStaleOrUnknown(t *testing.T) {
+	var hits int
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		switch r.URL.Path {
+		case "/api/status":
+			hits++
+			if hits == 1 {
+				_, _ = w.Write([]byte(`{"total_requests":1,"active_requests":1,"waiting_requests":0,"cache_efficiency":0.5,"model_memory_used_bytes":111,"model_memory_max_bytes":222}`))
+				return
+			}
+			http.Error(w, "boom", http.StatusServiceUnavailable)
+		default:
+			http.NotFound(w, r)
+		}
+	}))
+	t.Cleanup(srv.Close)
+
+	probe := NewUtilizationProbe(srv.URL+"/v1", srv.Client())
+	fresh := probe.Probe(context.Background())
+	require.Equal(t, utilization.FreshnessFresh, fresh.Freshness)
+	require.NotNil(t, fresh.ActiveRequests)
+	require.Equal(t, 1, *fresh.ActiveRequests)
+
+	stale := probe.Probe(context.Background())
+	require.Equal(t, utilization.FreshnessStale, stale.Freshness)
+	require.NotNil(t, stale.ActiveRequests)
+	require.Equal(t, 1, *stale.ActiveRequests)
+	require.Equal(t, fresh.ObservedAt, stale.ObservedAt)
+}
+
+func TestOMLXUtilizationProbe_UnknownOnInitialFailure(t *testing.T) {
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		http.Error(w, strings.TrimPrefix(r.URL.Path, "/"), http.StatusServiceUnavailable)
+	}))
+	t.Cleanup(srv.Close)
+
+	probe := NewUtilizationProbe(srv.URL+"/v1", srv.Client())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.FreshnessUnknown, sample.Freshness)
+	require.Equal(t, utilization.SourceOMLXStatus, sample.Source)
+	require.Nil(t, sample.ActiveRequests)
+	require.Nil(t, sample.QueuedRequests)
+	require.Nil(t, sample.CacheUsage)
+	require.Nil(t, sample.CachedTokens)
+	require.Nil(t, sample.MetalActiveMemoryBytes)
+	require.Nil(t, sample.MetalPeakMemoryBytes)
+}
+
+func TestOMLXRecordCassetteAndUtilization(t *testing.T) {
+	cassettePath := testutil.CassettePath(filepath.Join("testdata", "cassettes"), omlxCassetteName)
+
+	rec, err := testutil.NewRecorder(cassettePath)
+	require.NoError(t, err)
+	client := rec.GetDefaultClient()
+	t.Cleanup(func() {
+		require.NoError(t, rec.Stop())
+	})
+
+	if testutil.ModeForEnvironment() == recorder.ModeRecordOnly {
+		baseURL := liveOMLXBaseURL(t)
+		recordInteractions(t, client, baseURL, true)
+		return
+	}
+
+	recordInteractions(t, client, "http://replay.invalid/v1", false)
+}
+
+func recordInteractions(t *testing.T, client *http.Client, baseURL string, recordMode bool) {
+	t.Helper()
+	rootURL := utilization.ServerRoot(baseURL)
+
+	models := fetchModels(t, client, baseURL)
+	require.NotEmpty(t, models)
+	model := models[0]
+
+	idleStatus := fetchStatus(t, client, rootURL)
+	require.Equal(t, 0, idleStatus.ActiveRequests)
+	require.Equal(t, 0, idleStatus.WaitingRequests)
+	require.NotNil(t, idleStatus.CacheEfficiency)
+	require.NotNil(t, idleStatus.ModelMemoryUsedBytes)
+	require.NotNil(t, idleStatus.ModelMemoryMaxBytes)
+	require.NotEmpty(t, idleStatus.LoadedModels)
+
+	minimalChat := chatCompletion(t, client, baseURL, model, "Reply with one short word.", 8, false)
+	require.NotEmpty(t, minimalChat)
+
+	afterChatStatus := fetchStatus(t, client, rootURL)
+	require.Equal(t, 0, afterChatStatus.ActiveRequests)
+	require.GreaterOrEqual(t, afterChatStatus.TotalPromptTokens, idleStatus.TotalPromptTokens)
+	require.GreaterOrEqual(t, afterChatStatus.TotalCompletionTokens, idleStatus.TotalCompletionTokens)
+
+	loadResp, err := startStreamingChat(client, baseURL, model, "Write a 200-word paragraph about a small robot.", 64)
+	require.NoError(t, err)
+	require.NoError(t, statusOK(loadResp.StatusCode))
+	if recordMode {
+		t.Cleanup(func() {
+			if loadResp != nil && loadResp.Body != nil {
+				_, _ = io.Copy(io.Discard, loadResp.Body)
+				_ = loadResp.Body.Close()
+			}
+		})
+	}
+
+	loadStatus := waitForStatus(t, client, rootURL, func(s omlxStatusSnapshot) bool {
+		return s.ActiveRequests > 0
+	})
+
+	require.GreaterOrEqual(t, loadStatus.ActiveRequests, 1)
+	require.GreaterOrEqual(t, loadStatus.WaitingRequests, 0)
+	require.NotNil(t, loadStatus.AvgPrefillTPS)
+	require.NotNil(t, loadStatus.AvgGenerationTPS)
+	require.NotEmpty(t, loadStatus.LoadedModels)
+
+	if loadResp != nil && loadResp.Body != nil {
+		_, _ = io.Copy(io.Discard, loadResp.Body)
+		_ = loadResp.Body.Close()
+	}
+}
+
+func liveOMLXBaseURL(t *testing.T) string {
+	t.Helper()
+	if url := strings.TrimSpace(os.Getenv("OMLX_URL")); url != "" {
+		if providerReachable(t, url) {
+			return url
+		}
+		t.Skipf("oMLX at %q is unreachable", url)
+	}
+
+	for _, candidate := range []string{"http://vidar:1235/v1", "http://localhost:1235/v1"} {
+		if providerReachable(t, candidate) {
+			return candidate
+		}
+	}
+
+	t.Skip("No oMLX instance found for record mode (set OMLX_URL or run Vidar on port 1235)")
+	return ""
+}
+
+func providerReachable(t *testing.T, baseURL string) bool {
+	t.Helper()
+
+	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
+	defer cancel()
+
+	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
+	require.NoError(t, err)
+	resp, err := http.DefaultClient.Do(req)
+	if err != nil {
+		return false
+	}
+	defer resp.Body.Close()
+	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
+		return false
+	}
+
+	req, err = http.NewRequestWithContext(ctx, http.MethodGet, utilization.ServerRoot(baseURL)+"/api/status", nil)
+	require.NoError(t, err)
+	resp, err = http.DefaultClient.Do(req)
+	if err != nil {
+		return false
+	}
+	defer resp.Body.Close()
+	return resp.StatusCode >= 200 && resp.StatusCode < 300
+}
+
+func fetchModels(t *testing.T, client *http.Client, baseURL string) []string {
+	t.Helper()
+
+	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
+	require.NoError(t, err)
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	defer resp.Body.Close()
+	require.NoError(t, statusOK(resp.StatusCode))
+
+	var payload struct {
+		Data []struct {
+			ID string `json:"id"`
+		} `json:"data"`
+	}
+	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
+	models := make([]string, 0, len(payload.Data))
+	for _, entry := range payload.Data {
+		if entry.ID != "" {
+			models = append(models, entry.ID)
+		}
+	}
+	return models
+}
+
+func fetchStatus(t *testing.T, client *http.Client, baseURL string) omlxStatusSnapshot {
+	t.Helper()
+
+	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/status", nil)
+	require.NoError(t, err)
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	defer resp.Body.Close()
+	require.NoError(t, statusOK(resp.StatusCode))
+
+	body, err := io.ReadAll(resp.Body)
+	require.NoError(t, err)
+	snapshot, err := parseOMLXStatus(string(body))
+	require.NoError(t, err)
+	return snapshot
+}
+
+func chatCompletion(t *testing.T, client *http.Client, baseURL, model, prompt string, maxTokens int, stream bool) string {
+	t.Helper()
+	content, err := chatCompletionRaw(client, baseURL, model, prompt, maxTokens, stream)
+	require.NoError(t, err)
+	return content
+}
+
+func chatCompletionRaw(client *http.Client, baseURL, model, prompt string, maxTokens int, stream bool) (string, error) {
+	body := struct {
+		MaxTokens    int                    `json:"max_tokens"`
+		Messages     []map[string]string    `json:"messages"`
+		Model        string                 `json:"model"`
+		Stream       bool                   `json:"stream,omitempty"`
+		StreamOption *streamOptionsEnvelope `json:"stream_options,omitempty"`
+		Temperature  int                    `json:"temperature"`
+	}{
+		MaxTokens:   maxTokens,
+		Messages:    []map[string]string{{"role": "user", "content": prompt}},
+		Model:       model,
+		Stream:      stream,
+		Temperature: 0,
+	}
+	if stream {
+		body.StreamOption = &streamOptionsEnvelope{IncludeUsage: true}
+	}
+
+	raw, err := json.Marshal(body)
+	if err != nil {
+		return "", err
+	}
+
+	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
+	if err != nil {
+		return "", err
+	}
+	req.Header.Set("Content-Type", "application/json")
+
+	resp, err := client.Do(req)
+	if err != nil {
+		return "", err
+	}
+	defer resp.Body.Close()
+	if err := statusOK(resp.StatusCode); err != nil {
+		return "", err
+	}
+
+	if stream {
+		_, err := io.Copy(io.Discard, resp.Body)
+		return "", err
+	}
+
+	var payload struct {
+		Choices []struct {
+			Message struct {
+				Content string `json:"content"`
+			} `json:"message"`
+		} `json:"choices"`
+	}
+	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
+		return "", err
+	}
+	if len(payload.Choices) == 0 {
+		return "", fmt.Errorf("chat completion returned no choices")
+	}
+	return payload.Choices[0].Message.Content, nil
+}
+
+func startStreamingChat(client *http.Client, baseURL, model, prompt string, maxTokens int) (*http.Response, error) {
+	body := struct {
+		MaxTokens    int                    `json:"max_tokens"`
+		Messages     []map[string]string    `json:"messages"`
+		Model        string                 `json:"model"`
+		Stream       bool                   `json:"stream,omitempty"`
+		StreamOption *streamOptionsEnvelope `json:"stream_options,omitempty"`
+		Temperature  int                    `json:"temperature"`
+	}{
+		MaxTokens:    maxTokens,
+		Messages:     []map[string]string{{"role": "user", "content": prompt}},
+		Model:        model,
+		Stream:       true,
+		StreamOption: &streamOptionsEnvelope{IncludeUsage: true},
+		Temperature: 0,
+	}
+
+	raw, err := json.Marshal(body)
+	if err != nil {
+		return nil, err
+	}
+
+	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
+	if err != nil {
+		return nil, err
+	}
+	req.Header.Set("Content-Type", "application/json")
+
+	return client.Do(req)
+}
+
+type streamOptionsEnvelope struct {
+	IncludeUsage bool `json:"include_usage"`
+}
+
+func waitForStatus(t *testing.T, client *http.Client, baseURL string, predicate func(omlxStatusSnapshot) bool) omlxStatusSnapshot {
+	t.Helper()
+
+	deadline := time.Now().Add(20 * time.Second)
+	var last omlxStatusSnapshot
+	for time.Now().Before(deadline) {
+		last = fetchStatus(t, client, baseURL)
+		if predicate(last) {
+			return last
+		}
+		time.Sleep(250 * time.Millisecond)
+	}
+	t.Fatalf("timed out waiting for oMLX status predicate; last snapshot: %+v", last)
+	return last
+}
+
+func statusOK(code int) error {
+	if code < 200 || code >= 300 {
+		return fmt.Errorf("unexpected status code %d", code)
+	}
+	return nil
+}
diff --git a/internal/provider/utilization/utilization.go b/internal/provider/utilization/utilization.go
index ab1adde..be7dabb 100644
--- a/internal/provider/utilization/utilization.go
+++ b/internal/provider/utilization/utilization.go
@@ -13,6 +13,7 @@ type Source string
 
 const (
 	SourceUnknown        Source = "unknown"
+	SourceOMLXStatus     Source = "omlx.status"
 	SourceVLLMMetrics    Source = "vllm.metrics"
 	SourceLlamaMetrics   Source = "llama-server.metrics"
 	SourceLlamaSlots     Source = "llama-server.slots"
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
