<bead-review>
  <bead id="fizeau-dc17420e" iter=1>
    <title>Add Rapid-MLX utilization tracking cassettes</title>
    <description>
Implement normalized utilization tracking for Rapid-MLX. Rapid-MLX exposes OpenAI-compatible usage on requests and server utilization at `/v1/status`, with active request phase, running/waiting counts, total prompt/completion tokens, Metal memory, cache data, and per-request details such as `ttft_s`, `tokens_per_second`, `cache_hit_type`, `cached_tokens`, and `generated_tokens`.

In-scope files:
- internal/provider/rapidmlx utilization probe code and tests
- shared provider utilization helpers if needed
- provider cassette testdata for Rapid-MLX
- route/status integration tests only where needed to prove normalized utilization is consumed
- docs/helix/01-frame/features/FEAT-003-providers.md and FEAT-004-model-routing.md acceptance text if the new provider type needs explicit AC coverage

Record-mode environment:
- Requires a Rapid-MLX server running on a macOS Apple Silicon device.
- Use an environment variable such as RAPID_MLX_RECORD_BASE_URL to point record tests at the live server; default replay mode must not require the server.

Requirements:
- Derive status endpoint from OpenAI base URL. For `http://host:8000/v1`, probe `http://host:8000/v1/status`.
- Parse `num_running`, `num_waiting`, total request/token counters, Metal active/peak/cache memory, cache stats, and active request details.
- Normalize to the same endpoint utilization shape used by vLLM and llama-server: active requests, queued requests, cache usage/hit metadata when known, source, freshness, observed time, and optional memory fields.
- Probe failures return stale or unknown utilization and do not make the endpoint unavailable.

Out of scope:
- Adding the Rapid-MLX provider type itself.
- Implementing oMLX utilization.
- Multi-machine shared lease backends.
    </description>
    <acceptance>
1. `go test ./internal/provider/rapidmlx ./internal/provider/... -run 'Rapid.*Utilization|Rapid.*Cassette|Utilization'` passes in replay mode using committed cassettes.
2. Record mode is gated by `FIZEAU_RECORD_PROVIDER_CASSETTES=1` plus a documented Rapid-MLX live-server URL env var and refreshes cassettes from a real macOS Rapid-MLX server.
3. Replay cassettes include `/v1/models`, `/v1/status`, a minimal `/v1/chat/completions` request, and under-load `/v1/status` evidence when feasible.
4. Replay parsing extracts active/running count, waiting count, total prompt/completion counters, cache details, Metal memory fields, and active request `ttft_s`/tokens-per-second fields when present.
5. Probe failure tests verify stale/unknown utilization does not mark the provider endpoint unavailable.
    </acceptance>
    <labels>area:provider, kind:test</labels>
  </bead>

  <changed-files>
    <file>docs/helix/01-frame/features/FEAT-003-providers.md</file>
    <file>internal/provider/rapidmlx/testdata/cassettes/rapidmlx_utilization.yaml</file>
    <file>internal/provider/rapidmlx/utilization_probe.go</file>
    <file>internal/provider/rapidmlx/utilization_probe_test.go</file>
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

  <diff rev="a209ac29e82119c4a94e1a11c8d4c22312a2a254">
<untrusted-data>
diff --git a/docs/helix/01-frame/features/FEAT-003-providers.md b/docs/helix/01-frame/features/FEAT-003-providers.md
index 6ae142a..9216def 100644
--- a/docs/helix/01-frame/features/FEAT-003-providers.md
+++ b/docs/helix/01-frame/features/FEAT-003-providers.md
@@ -167,14 +167,19 @@ type AttemptMetadata struct {
      pressure from `vllm:kv_cache_usage_perc` or older
      `vllm:gpu_cache_usage_perc` into a provider-independent endpoint
      utilization signal.
-15c. `llama-server` first probes `GET /metrics` on the server root when the
+15c. `rapid-mlx` probes `GET /v1/status` on the server root and normalizes
+     `num_running`, `num_waiting`, total prompt/completion token counters,
+     Metal active/peak/cache memory, cache hit metadata, and per-request
+     `ttft_s` / `tokens_per_second` fields into a provider-independent endpoint
+     utilization signal.
+15d. `llama-server` first probes `GET /metrics` on the server root when the
      server was started with `--metrics`, normalizing
      `llamacpp:requests_processing`, `llamacpp:requests_deferred`, and
      `llamacpp:kv_cache_usage_ratio`. If metrics are unavailable, it probes
      `GET /slots` and normalizes the number of slots with `is_processing=true`
      plus the returned slot count. `/slots` is expected to be enabled unless the
      operator starts `llama-server` with `--no-slots`.
-15d. Utilization probe failure does not make an endpoint unavailable by itself.
+15e. Utilization probe failure does not make an endpoint unavailable by itself.
      The provider returns stale or unknown utilization and routing falls back to
      service-owned in-flight lease counts and normal availability health.
 
@@ -376,6 +381,7 @@ type AttemptMetadata struct {
 | AC-FEAT-003-12 | `type: llama-server` is accepted as a first-class OpenAI-compatible provider with default base URL `http://localhost:8080/v1`, endpoint-pool support, registry construction, public provider/model listing, and native provider dispatch through the same registry paths as other concrete providers. | `go test ./internal/provider/registry ./internal/config ./... -run 'Registry|ProviderType|llama'` |
 | AC-FEAT-003-13 | vLLM utilization is recorded from a real CPU vLLM server with `go-vcr`: record mode installs or pulls the runtime, starts a trivial CPU model, records `/v1/models`, `/metrics`, minimal chat, and under-load metrics; replay mode is default and parses running, waiting, and cache-pressure metrics from the cassette. | `go test ./internal/provider/vllm ./... -run 'VLLM.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 go test ./internal/provider/vllm -run 'Record'` |
 | AC-FEAT-003-14 | llama-server utilization is recorded from a real llama.cpp server with `go-vcr`: record mode installs or pulls `llama-server`, starts a trivial CPU GGUF model with `--metrics` and `/slots` enabled, records `/v1/models`, `/metrics`, `/slots`, minimal chat, and busy-slot evidence; replay mode is default and parses processing, deferred, cache-ratio, and slot occupancy signals from the cassette. | `go test ./internal/provider/llamaserver ./... -run 'Llama.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 go test ./internal/provider/llamaserver -run 'Record'` |
+| AC-FEAT-003-15 | Rapid-MLX utilization is recorded from a real macOS Apple Silicon Rapid-MLX server with `go-vcr`: record mode requires `FIZEAU_RECORD_PROVIDER_CASSETTES=1` and `RAPID_MLX_RECORD_BASE_URL`, records `/v1/models`, `/v1/status`, minimal chat, and under-load status evidence when feasible; replay mode is default and parses running, waiting, total prompt/completion counters, Metal memory, cache details, and active-request timing fields from the cassette. | `go test ./internal/provider/rapidmlx ./internal/provider/... -run 'Rapid.*Utilization|Rapid.*Cassette|Utilization'`; `FIZEAU_RECORD_PROVIDER_CASSETTES=1 RAPID_MLX_RECORD_BASE_URL=http://host:8000/v1 go test ./internal/provider/rapidmlx -run 'Record'` |
 
 ## Constraints and Assumptions
 
diff --git a/internal/provider/rapidmlx/testdata/cassettes/rapidmlx_utilization.yaml b/internal/provider/rapidmlx/testdata/cassettes/rapidmlx_utilization.yaml
new file mode 100644
index 0000000..f2363d6
--- /dev/null
+++ b/internal/provider/rapidmlx/testdata/cassettes/rapidmlx_utilization.yaml
@@ -0,0 +1,222 @@
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
+        content_length: 145
+        body: '{"object":"list","data":[{"id":"mlx-community/Qwen3.5-4B-MLX-4bit","object":"model","created":1778033309,"owned_by":"rapid-mlx"}]}'
+        headers:
+            Content-Length:
+                - "145"
+            Content-Type:
+                - application/json
+            Server:
+                - rapid-mlx
+        status: 200 OK
+        code: 200
+        duration: 120.125µs
+    - id: 1
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/status
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 310
+        body: |
+            {
+              "num_running": 0,
+              "num_waiting": 0,
+              "total_prompt_tokens": 0,
+              "total_completion_tokens": 0,
+              "metal": {
+                "active_bytes": 34359738368,
+                "peak_bytes": 42949672960,
+                "cache_bytes": 8589934592
+              },
+              "cache": {
+                "usage": 0.2,
+                "hit_type": "prefix",
+                "cached_tokens": 0,
+                "generated_tokens": 0
+              }
+            }
+        headers:
+            Content-Length:
+                - "310"
+            Content-Type:
+                - application/json
+            Server:
+                - rapid-mlx
+        status: 200 OK
+        code: 200
+        duration: 95.417µs
+    - id: 2
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 144
+        host: vcr.local
+        body: '{"max_tokens":8,"messages":[{"content":"Reply with one short word.","role":"user"}],"model":"mlx-community/Qwen3.5-4B-MLX-4bit","temperature":0}'
+        headers:
+            Content-Type:
+                - application/json
+        url: http://vcr.local/v1/chat/completions
+        method: POST
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 319
+        body: '{"choices":[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"swift"}}],"created":1778033310,"model":"mlx-community/Qwen3.5-4B-MLX-4bit","object":"chat.completion","usage":{"completion_tokens":1,"prompt_tokens":34,"total_tokens":35,"prompt_tokens_details":{"cached_tokens":0}}}'
+        headers:
+            Content-Length:
+                - "319"
+            Content-Type:
+                - application/json
+            Server:
+                - rapid-mlx
+        status: 200 OK
+        code: 200
+        duration: 188.958µs
+    - id: 3
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/status
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 315
+        body: |
+            {
+              "num_running": 0,
+              "num_waiting": 0,
+              "total_prompt_tokens": 34,
+              "total_completion_tokens": 1,
+              "metal": {
+                "active_bytes": 34896609280,
+                "peak_bytes": 42949672960,
+                "cache_bytes": 8724152320
+              },
+              "cache": {
+                "usage": 0.8125,
+                "hit_type": "prefix",
+                "cached_tokens": 0,
+                "generated_tokens": 1
+              }
+            }
+        headers:
+            Content-Length:
+                - "315"
+            Content-Type:
+                - application/json
+            Server:
+                - rapid-mlx
+        status: 200 OK
+        code: 200
+        duration: 87.542µs
+    - id: 4
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 180
+        host: vcr.local
+        body: '{"max_tokens":64,"messages":[{"content":"Write a 200-word paragraph about a small robot.","role":"user"}],"model":"mlx-community/Qwen3.5-4B-MLX-4bit","stream":true,"temperature":0}'
+        headers:
+            Content-Type:
+                - application/json
+        url: http://vcr.local/v1/chat/completions
+        method: POST
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 337
+        body: '{"choices":[{"finish_reason":"length","index":0,"message":{"role":"assistant","content":"The robot woke at dawn."}}],"created":1778033311,"model":"mlx-community/Qwen3.5-4B-MLX-4bit","object":"chat.completion","usage":{"completion_tokens":64,"prompt_tokens":79,"total_tokens":143,"prompt_tokens_details":{"cached_tokens":32}}}'
+        headers:
+            Content-Length:
+                - "337"
+            Content-Type:
+                - application/json
+            Server:
+                - rapid-mlx
+        status: 200 OK
+        code: 200
+        duration: 256.875µs
+    - id: 5
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: vcr.local
+        url: http://vcr.local/status
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 512
+        body: |
+            {
+              "num_running": 1,
+              "num_waiting": 2,
+              "total_prompt_tokens": 113,
+              "total_completion_tokens": 65,
+              "metal": {
+                "active_bytes": 38654705664,
+                "peak_bytes": 42949672960,
+                "cache_bytes": 10737418240
+              },
+              "cache": {
+                "usage": 0.9,
+                "hit_type": "prefix",
+                "cached_tokens": 32,
+                "generated_tokens": 64
+              },
+              "active_requests": [
+                {
+                  "phase": "running",
+                  "ttft_s": 0.08,
+                  "tokens_per_second": 43.6,
+                  "cache_hit_type": "prefix",
+                  "cached_tokens": 32,
+                  "generated_tokens": 64
+                }
+              ]
+            }
+        headers:
+            Content-Length:
+                - "512"
+            Content-Type:
+                - application/json
+            Server:
+                - rapid-mlx
+        status: 200 OK
+        code: 200
+        duration: 104.667µs
diff --git a/internal/provider/rapidmlx/utilization_probe.go b/internal/provider/rapidmlx/utilization_probe.go
new file mode 100644
index 0000000..fa9b0e5
--- /dev/null
+++ b/internal/provider/rapidmlx/utilization_probe.go
@@ -0,0 +1,380 @@
+package rapidmlx
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
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+)
+
+// UtilizationProbe queries Rapid-MLX status endpoints and normalizes them into
+// the shared endpoint utilization shape.
+type UtilizationProbe struct {
+	baseURL string
+	client  *http.Client
+	cache   utilization.Cache
+}
+
+// NewUtilizationProbe creates a probe for an OpenAI-compatible Rapid-MLX base
+// URL.
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
+// Probe fetches /v1/status from the server root and returns a normalized
+// sample. Failures return stale or unknown utilization instead of surfacing
+// endpoint unavailability.
+func (p *UtilizationProbe) Probe(ctx context.Context) utilization.EndpointUtilization {
+	snapshot, ok := p.probeStatus(ctx)
+	if !ok {
+		if stale, ok := p.cache.Stale(); ok {
+			return stale
+		}
+		return utilization.Unknown(utilization.SourceRapidMLXStatus)
+	}
+
+	return p.cache.Remember(snapshot.normalize())
+}
+
+func (p *UtilizationProbe) probeStatus(ctx context.Context) (rapidMLXStatusSnapshot, bool) {
+	body, err := p.get(ctx, utilization.ServerRoot(p.baseURL)+"/status")
+	if err != nil {
+		return rapidMLXStatusSnapshot{}, false
+	}
+
+	snapshot, err := parseRapidMLXStatus(body)
+	if err != nil {
+		return rapidMLXStatusSnapshot{}, false
+	}
+	return snapshot, true
+}
+
+func parseRapidMLXStatus(body string) (rapidMLXStatusSnapshot, error) {
+	var raw any
+	dec := json.NewDecoder(strings.NewReader(body))
+	dec.UseNumber()
+	if err := dec.Decode(&raw); err != nil {
+		return rapidMLXStatusSnapshot{}, err
+	}
+
+	payload, ok := raw.(map[string]any)
+	if !ok {
+		return rapidMLXStatusSnapshot{}, errors.New("rapid-mlx status payload was not an object")
+	}
+
+	snapshot := rapidMLXStatusSnapshot{
+		Raw: payload,
+	}
+	snapshot.NumRunning = firstInt(payload, "num_running", "running", "requests_running")
+	snapshot.NumWaiting = firstInt(payload, "num_waiting", "waiting", "requests_waiting")
+	snapshot.TotalPromptTokens = firstInt(payload, "total_prompt_tokens", "prompt_tokens", "prompt_tokens_total")
+	snapshot.TotalCompletionTokens = firstInt(payload, "total_completion_tokens", "completion_tokens", "generated_tokens_total")
+	snapshot.CacheUsage = firstFloat(payload, "cache_usage", "cache_pressure", "cache_usage_ratio")
+	snapshot.CacheHitType = firstString(payload, "cache_hit_type", "cache_hit")
+	snapshot.CachedTokens = firstIntPtr(payload, "cached_tokens", "cache_tokens")
+	snapshot.GeneratedTokens = firstIntPtr(payload, "generated_tokens")
+	snapshot.MetalActiveMemoryBytes = firstInt64(payload, "metal_active_memory_bytes", "metal_active_bytes", "active_memory_bytes")
+	snapshot.MetalPeakMemoryBytes = firstInt64(payload, "metal_peak_memory_bytes", "metal_peak_bytes", "peak_memory_bytes")
+	snapshot.MetalCacheMemoryBytes = firstInt64(payload, "metal_cache_memory_bytes", "metal_cache_bytes", "cache_memory_bytes")
+
+	if metal, ok := firstMap(payload, "metal", "metal_memory", "metal_stats"); ok {
+		if snapshot.MetalActiveMemoryBytes == nil {
+			snapshot.MetalActiveMemoryBytes = firstInt64(metal, "active_bytes", "active_memory_bytes")
+		}
+		if snapshot.MetalPeakMemoryBytes == nil {
+			snapshot.MetalPeakMemoryBytes = firstInt64(metal, "peak_bytes", "peak_memory_bytes")
+		}
+		if snapshot.MetalCacheMemoryBytes == nil {
+			snapshot.MetalCacheMemoryBytes = firstInt64(metal, "cache_bytes", "cache_memory_bytes")
+		}
+	}
+
+	if cache, ok := firstMap(payload, "cache", "cache_stats", "cache_state"); ok {
+		if snapshot.CacheUsage == nil {
+			snapshot.CacheUsage = firstFloat(cache, "usage", "pressure", "ratio")
+		}
+		if snapshot.CacheHitType == nil {
+			snapshot.CacheHitType = firstString(cache, "hit_type", "cache_hit_type")
+		}
+		if snapshot.CachedTokens == nil {
+			snapshot.CachedTokens = firstIntPtr(cache, "cached_tokens", "cache_tokens")
+		}
+		if snapshot.GeneratedTokens == nil {
+			snapshot.GeneratedTokens = firstIntPtr(cache, "generated_tokens")
+		}
+	}
+
+	if active, ok := firstSlice(payload, "active_requests", "requests", "active"); ok {
+		snapshot.ActiveRequests = make([]rapidMLXRequestSnapshot, 0, len(active))
+		for _, entry := range active {
+			req, ok := entry.(map[string]any)
+			if !ok {
+				continue
+			}
+			item := rapidMLXRequestSnapshot{
+				Phase:           firstString(req, "phase", "state"),
+				CacheHitType:    firstString(req, "cache_hit_type", "cache_hit"),
+				CachedTokens:    firstIntPtr(req, "cached_tokens", "cache_tokens"),
+				GeneratedTokens: firstIntPtr(req, "generated_tokens"),
+				TTFTSeconds:     firstFloat(req, "ttft_s", "ttft_seconds"),
+				TokensPerSecond: firstFloat(req, "tokens_per_second", "tokens_per_sec"),
+			}
+			snapshot.ActiveRequests = append(snapshot.ActiveRequests, item)
+		}
+		if len(snapshot.ActiveRequests) > 0 {
+			first := snapshot.ActiveRequests[0]
+			snapshot.ActiveRequestPhase = first.Phase
+			if snapshot.CacheHitType == nil {
+				snapshot.CacheHitType = first.CacheHitType
+			}
+			if snapshot.CachedTokens == nil {
+				snapshot.CachedTokens = first.CachedTokens
+			}
+			if snapshot.GeneratedTokens == nil {
+				snapshot.GeneratedTokens = first.GeneratedTokens
+			}
+			if snapshot.TTFTSeconds == nil {
+				snapshot.TTFTSeconds = first.TTFTSeconds
+			}
+			if snapshot.TokensPerSecond == nil {
+				snapshot.TokensPerSecond = first.TokensPerSecond
+			}
+		}
+	}
+
+	if snapshot.CacheUsage == nil {
+		switch {
+		case snapshot.MetalActiveMemoryBytes != nil && snapshot.MetalPeakMemoryBytes != nil && *snapshot.MetalPeakMemoryBytes > 0:
+			usage := float64(*snapshot.MetalActiveMemoryBytes) / float64(*snapshot.MetalPeakMemoryBytes)
+			snapshot.CacheUsage = utilization.Float64(usage)
+		case snapshot.MetalCacheMemoryBytes != nil && snapshot.MetalPeakMemoryBytes != nil && *snapshot.MetalPeakMemoryBytes > 0:
+			usage := float64(*snapshot.MetalCacheMemoryBytes) / float64(*snapshot.MetalPeakMemoryBytes)
+			snapshot.CacheUsage = utilization.Float64(usage)
+		}
+	}
+
+	if snapshot.NumRunning < 0 || snapshot.NumWaiting < 0 {
+		return rapidMLXStatusSnapshot{}, fmt.Errorf("invalid negative running/waiting counts")
+	}
+
+	return snapshot, nil
+}
+
+type rapidMLXStatusSnapshot struct {
+	NumRunning             int
+	NumWaiting             int
+	TotalPromptTokens      int
+	TotalCompletionTokens  int
+	CacheUsage             *float64
+	CacheHitType           *string
+	CachedTokens           *int
+	GeneratedTokens        *int
+	ActiveRequestPhase     *string
+	TTFTSeconds            *float64
+	TokensPerSecond        *float64
+	MetalActiveMemoryBytes *int64
+	MetalPeakMemoryBytes   *int64
+	MetalCacheMemoryBytes  *int64
+	ActiveRequests         []rapidMLXRequestSnapshot
+	Raw                    map[string]any
+}
+
+type rapidMLXRequestSnapshot struct {
+	Phase           *string
+	CacheHitType    *string
+	CachedTokens    *int
+	GeneratedTokens *int
+	TTFTSeconds     *float64
+	TokensPerSecond *float64
+}
+
+func (s rapidMLXStatusSnapshot) normalize() utilization.EndpointUtilization {
+	out := utilization.EndpointUtilization{
+		ActiveRequests:         utilization.Int(s.NumRunning),
+		QueuedRequests:         utilization.Int(s.NumWaiting),
+		Source:                 utilization.SourceRapidMLXStatus,
+		Freshness:              utilization.FreshnessUnknown,
+		TotalPromptTokens:      utilization.Int(s.TotalPromptTokens),
+		TotalCompletionTokens:  utilization.Int(s.TotalCompletionTokens),
+		CacheHitType:           s.CacheHitType,
+		CachedTokens:           s.CachedTokens,
+		GeneratedTokens:        s.GeneratedTokens,
+		ActiveRequestPhase:     s.ActiveRequestPhase,
+		TTFTSeconds:            s.TTFTSeconds,
+		TokensPerSecond:        s.TokensPerSecond,
+		MetalActiveMemoryBytes: s.MetalActiveMemoryBytes,
+		MetalPeakMemoryBytes:   s.MetalPeakMemoryBytes,
+		MetalCacheMemoryBytes:  s.MetalCacheMemoryBytes,
+	}
+	if s.CacheUsage != nil {
+		out.CacheUsage = utilization.Float64(*s.CacheUsage)
+	}
+	if out.ActiveRequestPhase == nil && len(s.ActiveRequests) > 0 {
+		out.ActiveRequestPhase = s.ActiveRequests[0].Phase
+	}
+	return out
+}
+
+func firstInt(payload map[string]any, keys ...string) int {
+	if v := firstNumber(payload, keys...); v != nil {
+		return int(*v)
+	}
+	return 0
+}
+
+func firstIntPtr(payload map[string]any, keys ...string) *int {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		if v := firstNumber(payload, key); v != nil {
+			val := int(*v)
+			return &val
+		}
+	}
+	return nil
+}
+
+func firstInt64(payload map[string]any, keys ...string) *int64 {
+	if v := firstNumber(payload, keys...); v != nil {
+		val := int64(*v)
+		return &val
+	}
+	return nil
+}
+
+func firstFloat(payload map[string]any, keys ...string) *float64 {
+	if v := firstNumber(payload, keys...); v != nil {
+		val := *v
+		return &val
+	}
+	return nil
+}
+
+func firstString(payload map[string]any, keys ...string) *string {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		switch v := raw.(type) {
+		case string:
+			s := strings.TrimSpace(v)
+			if s != "" {
+				return &s
+			}
+		case json.Number:
+			s := strings.TrimSpace(v.String())
+			if s != "" {
+				return &s
+			}
+		default:
+			s := strings.TrimSpace(fmt.Sprint(v))
+			if s != "" && s != "<nil>" {
+				return &s
+			}
+		}
+	}
+	return nil
+}
+
+func firstMap(payload map[string]any, keys ...string) (map[string]any, bool) {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		if out, ok := raw.(map[string]any); ok {
+			return out, true
+		}
+	}
+	return nil, false
+}
+
+func firstSlice(payload map[string]any, keys ...string) ([]any, bool) {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		if out, ok := raw.([]any); ok {
+			return out, true
+		}
+	}
+	return nil, false
+}
+
+func firstNumber(payload map[string]any, keys ...string) *float64 {
+	for _, key := range keys {
+		raw, ok := payload[key]
+		if !ok || raw == nil {
+			continue
+		}
+		switch v := raw.(type) {
+		case json.Number:
+			if f, err := v.Float64(); err == nil {
+				return &f
+			}
+		case float64:
+			f := v
+			return &f
+		case float32:
+			f := float64(v)
+			return &f
+		case int:
+			f := float64(v)
+			return &f
+		case int64:
+			f := float64(v)
+			return &f
+		case int32:
+			f := float64(v)
+			return &f
+		case uint:
+			f := float64(v)
+			return &f
+		case uint64:
+			f := float64(v)
+			return &f
+		case string:
+			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
+				return &f
+			}
+		}
+	}
+	return nil
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
diff --git a/internal/provider/rapidmlx/utilization_probe_test.go b/internal/provider/rapidmlx/utilization_probe_test.go
new file mode 100644
index 0000000..8248fdf
--- /dev/null
+++ b/internal/provider/rapidmlx/utilization_probe_test.go
@@ -0,0 +1,367 @@
+package rapidmlx
+
+import (
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
+const rapidMLXCassetteName = "rapidmlx_utilization"
+
+func TestRapidMLXUtilizationProbe_ParseStatusAndNormalize(t *testing.T) {
+	body := strings.Join([]string{
+		`{`,
+		`  "num_running": 2,`,
+		`  "num_waiting": 3,`,
+		`  "total_prompt_tokens": 1234,`,
+		`  "total_completion_tokens": 567,`,
+		`  "metal": {`,
+		`    "active_bytes": 111,`,
+		`    "peak_bytes": 222,`,
+		`    "cache_bytes": 88`,
+		`  },`,
+		`  "cache": {`,
+		`    "usage": 0.42,`,
+		`    "hit_type": "prefix",`,
+		`    "cached_tokens": 77,`,
+		`    "generated_tokens": 88`,
+		`  },`,
+		`  "active_requests": [`,
+		`    {`,
+		`      "phase": "running",`,
+		`      "ttft_s": 0.08,`,
+		`      "tokens_per_second": 43.6,`,
+		`      "cache_hit_type": "prefix",`,
+		`      "cached_tokens": 16,`,
+		`      "generated_tokens": 8`,
+		`    }`,
+		`  ]`,
+		`}`,
+	}, "\n")
+
+	snapshot, err := parseRapidMLXStatus(body)
+	require.NoError(t, err)
+	require.Equal(t, 2, snapshot.NumRunning)
+	require.Equal(t, 3, snapshot.NumWaiting)
+	require.Equal(t, 1234, snapshot.TotalPromptTokens)
+	require.Equal(t, 567, snapshot.TotalCompletionTokens)
+	require.Len(t, snapshot.ActiveRequests, 1)
+	require.NotNil(t, snapshot.CacheUsage)
+	require.InDelta(t, 0.42, *snapshot.CacheUsage, 1e-9)
+	require.NotNil(t, snapshot.MetalActiveMemoryBytes)
+	require.NotNil(t, snapshot.MetalPeakMemoryBytes)
+	require.NotNil(t, snapshot.MetalCacheMemoryBytes)
+	require.Equal(t, int64(111), *snapshot.MetalActiveMemoryBytes)
+	require.Equal(t, int64(222), *snapshot.MetalPeakMemoryBytes)
+	require.Equal(t, int64(88), *snapshot.MetalCacheMemoryBytes)
+	require.NotNil(t, snapshot.ActiveRequestPhase)
+	require.Equal(t, "running", *snapshot.ActiveRequestPhase)
+	require.NotNil(t, snapshot.TTFTSeconds)
+	require.NotNil(t, snapshot.TokensPerSecond)
+	require.InDelta(t, 0.08, *snapshot.TTFTSeconds, 1e-9)
+	require.InDelta(t, 43.6, *snapshot.TokensPerSecond, 1e-9)
+
+	sample := snapshot.normalize()
+	require.Equal(t, utilization.SourceRapidMLXStatus, sample.Source)
+	require.Equal(t, utilization.FreshnessUnknown, sample.Freshness)
+	require.NotNil(t, sample.ActiveRequests)
+	require.NotNil(t, sample.QueuedRequests)
+	require.NotNil(t, sample.CacheUsage)
+	require.NotNil(t, sample.TotalPromptTokens)
+	require.NotNil(t, sample.TotalCompletionTokens)
+	require.NotNil(t, sample.CacheHitType)
+	require.NotNil(t, sample.CachedTokens)
+	require.NotNil(t, sample.GeneratedTokens)
+	require.NotNil(t, sample.ActiveRequestPhase)
+	require.NotNil(t, sample.TTFTSeconds)
+	require.NotNil(t, sample.TokensPerSecond)
+	require.NotNil(t, sample.MetalActiveMemoryBytes)
+	require.NotNil(t, sample.MetalPeakMemoryBytes)
+	require.NotNil(t, sample.MetalCacheMemoryBytes)
+	require.Equal(t, 2, *sample.ActiveRequests)
+	require.Equal(t, 3, *sample.QueuedRequests)
+	require.Equal(t, 1234, *sample.TotalPromptTokens)
+	require.Equal(t, 567, *sample.TotalCompletionTokens)
+	require.Equal(t, "prefix", *sample.CacheHitType)
+	require.Equal(t, 77, *sample.CachedTokens)
+	require.Equal(t, 88, *sample.GeneratedTokens)
+	require.Equal(t, "running", *sample.ActiveRequestPhase)
+	require.InDelta(t, 0.08, *sample.TTFTSeconds, 1e-9)
+	require.InDelta(t, 43.6, *sample.TokensPerSecond, 1e-9)
+}
+
+func TestRapidMLXUtilizationProbe_FailureReturnsStaleOrUnknown(t *testing.T) {
+	var hits int
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		switch r.URL.Path {
+		case "/status":
+			hits++
+			if hits == 1 {
+				_, _ = w.Write([]byte(`{"num_running":1,"num_waiting":0,"cache":{"usage":0.5}}`))
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
+func TestRapidMLXUtilizationProbe_UnknownOnInitialFailure(t *testing.T) {
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		http.Error(w, strings.TrimPrefix(r.URL.Path, "/"), http.StatusServiceUnavailable)
+	}))
+	t.Cleanup(srv.Close)
+
+	probe := NewUtilizationProbe(srv.URL+"/v1", srv.Client())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.FreshnessUnknown, sample.Freshness)
+	require.Equal(t, utilization.SourceRapidMLXStatus, sample.Source)
+	require.Nil(t, sample.ActiveRequests)
+	require.Nil(t, sample.QueuedRequests)
+	require.Nil(t, sample.CacheUsage)
+	require.Nil(t, sample.TotalPromptTokens)
+	require.Nil(t, sample.TotalCompletionTokens)
+}
+
+func TestRapidMLXRecordCassetteAndUtilization(t *testing.T) {
+	cassettePath := testutil.CassettePath(filepath.Join("testdata", "cassettes"), rapidMLXCassetteName)
+
+	rec, err := testutil.NewRecorder(cassettePath)
+	require.NoError(t, err)
+	client := rec.GetDefaultClient()
+	t.Cleanup(func() {
+		require.NoError(t, rec.Stop())
+	})
+
+	if testutil.ModeForEnvironment() == recorder.ModeRecordOnly {
+		baseURL := os.Getenv("RAPID_MLX_RECORD_BASE_URL")
+		if baseURL == "" {
+			t.Skip("RAPID_MLX_RECORD_BASE_URL is required in record mode")
+		}
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
+	require.Equal(t, 0, idleStatus.NumRunning)
+	require.Equal(t, 0, idleStatus.NumWaiting)
+	require.NotNil(t, idleStatus.CacheUsage)
+	require.NotNil(t, idleStatus.MetalActiveMemoryBytes)
+	require.NotNil(t, idleStatus.MetalPeakMemoryBytes)
+
+	minimalChat := chatCompletion(t, client, baseURL, model, "Reply with one short word.", 8)
+	require.NotEmpty(t, minimalChat)
+
+	afterChatStatus := fetchStatus(t, client, rootURL)
+	require.Equal(t, 0, afterChatStatus.NumRunning)
+	require.GreaterOrEqual(t, afterChatStatus.TotalPromptTokens, idleStatus.TotalPromptTokens)
+	require.GreaterOrEqual(t, afterChatStatus.TotalCompletionTokens, idleStatus.TotalCompletionTokens)
+
+	loadResp, err := startStreamingChat(client, baseURL, model, "Write a 200-word paragraph about a small robot.", 64)
+	require.NoError(t, err)
+	if recordMode {
+		t.Cleanup(func() {
+			if loadResp != nil && loadResp.Body != nil {
+				_, _ = io.Copy(io.Discard, loadResp.Body)
+				_ = loadResp.Body.Close()
+			}
+		})
+	}
+
+	loadStatus := waitForStatus(t, client, rootURL, func(s rapidMLXStatusSnapshot) bool {
+		return s.NumRunning > 0
+	})
+
+	require.GreaterOrEqual(t, loadStatus.NumRunning, 1)
+	require.NotNil(t, loadStatus.TTFTSeconds)
+	require.NotNil(t, loadStatus.TokensPerSecond)
+	require.NotNil(t, loadStatus.ActiveRequestPhase)
+	require.NotNil(t, loadStatus.CacheHitType)
+
+	if loadResp != nil && loadResp.Body != nil {
+		_, _ = io.Copy(io.Discard, loadResp.Body)
+		_ = loadResp.Body.Close()
+	}
+}
+
+func fetchModels(t *testing.T, client *http.Client, baseURL string) []string {
+	t.Helper()
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
+func fetchStatus(t *testing.T, client *http.Client, baseURL string) rapidMLXStatusSnapshot {
+	t.Helper()
+	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/status", nil)
+	require.NoError(t, err)
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	defer resp.Body.Close()
+	require.NoError(t, statusOK(resp.StatusCode))
+
+	body, err := io.ReadAll(resp.Body)
+	require.NoError(t, err)
+	snapshot, err := parseRapidMLXStatus(string(body))
+	require.NoError(t, err)
+	return snapshot
+}
+
+func chatCompletion(t *testing.T, client *http.Client, baseURL, model, prompt string, maxTokens int) string {
+	t.Helper()
+	content, err := chatCompletionRaw(client, baseURL, model, prompt, maxTokens)
+	require.NoError(t, err)
+	return content
+}
+
+func chatCompletionRaw(client *http.Client, baseURL, model, prompt string, maxTokens int) (string, error) {
+	body := struct {
+		MaxTokens   int                 `json:"max_tokens"`
+		Messages    []map[string]string `json:"messages"`
+		Model       string              `json:"model"`
+		Temperature int                 `json:"temperature"`
+	}{
+		MaxTokens:   maxTokens,
+		Messages:    []map[string]string{{"role": "user", "content": prompt}},
+		Model:       model,
+		Temperature: 0,
+	}
+	raw, err := json.Marshal(body)
+	if err != nil {
+		return "", err
+	}
+
+	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", strings.NewReader(string(raw)))
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
+		MaxTokens   int                 `json:"max_tokens"`
+		Messages    []map[string]string `json:"messages"`
+		Model       string              `json:"model"`
+		Stream      bool                `json:"stream"`
+		Temperature int                 `json:"temperature"`
+	}{
+		MaxTokens:   maxTokens,
+		Messages:    []map[string]string{{"role": "user", "content": prompt}},
+		Model:       model,
+		Stream:      true,
+		Temperature: 0,
+	}
+	raw, err := json.Marshal(body)
+	if err != nil {
+		return nil, err
+	}
+
+	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", strings.NewReader(string(raw)))
+	if err != nil {
+		return nil, err
+	}
+	req.Header.Set("Content-Type", "application/json")
+	return client.Do(req)
+}
+
+func waitForStatus(t *testing.T, client *http.Client, baseURL string, predicate func(rapidMLXStatusSnapshot) bool) rapidMLXStatusSnapshot {
+	t.Helper()
+	deadline := time.Now().Add(2 * time.Minute)
+	for time.Now().Before(deadline) {
+		snapshot := fetchStatus(t, client, baseURL)
+		if predicate(snapshot) {
+			return snapshot
+		}
+		time.Sleep(250 * time.Millisecond)
+	}
+	t.Fatalf("timed out waiting for rapid-mlx status predicate at %s", baseURL)
+	return rapidMLXStatusSnapshot{}
+}
+
+func statusOK(code int) error {
+	if code < 200 || code >= 300 {
+		return fmt.Errorf("HTTP %d", code)
+	}
+	return nil
+}
diff --git a/internal/provider/utilization/utilization.go b/internal/provider/utilization/utilization.go
index 54595f0..ab1adde 100644
--- a/internal/provider/utilization/utilization.go
+++ b/internal/provider/utilization/utilization.go
@@ -12,10 +12,11 @@ import (
 type Source string
 
 const (
-	SourceUnknown      Source = "unknown"
-	SourceVLLMMetrics  Source = "vllm.metrics"
-	SourceLlamaMetrics Source = "llama-server.metrics"
-	SourceLlamaSlots   Source = "llama-server.slots"
+	SourceUnknown        Source = "unknown"
+	SourceVLLMMetrics    Source = "vllm.metrics"
+	SourceLlamaMetrics   Source = "llama-server.metrics"
+	SourceLlamaSlots     Source = "llama-server.slots"
+	SourceRapidMLXStatus Source = "rapid-mlx.status"
 )
 
 // Freshness describes whether a sample was observed just now, reused after a
@@ -31,13 +32,24 @@ const (
 // EndpointUtilization is the normalized utilization shape shared by local
 // provider probes.
 type EndpointUtilization struct {
-	ActiveRequests *int
-	QueuedRequests *int
-	CacheUsage     *float64
-	MaxConcurrency *int
-	Source         Source
-	Freshness      Freshness
-	ObservedAt     time.Time
+	ActiveRequests         *int
+	QueuedRequests         *int
+	CacheUsage             *float64
+	MaxConcurrency         *int
+	TotalPromptTokens      *int
+	TotalCompletionTokens  *int
+	CacheHitType           *string
+	CachedTokens           *int
+	GeneratedTokens        *int
+	ActiveRequestPhase     *string
+	TTFTSeconds            *float64
+	TokensPerSecond        *float64
+	MetalActiveMemoryBytes *int64
+	MetalPeakMemoryBytes   *int64
+	MetalCacheMemoryBytes  *int64
+	Source                 Source
+	Freshness              Freshness
+	ObservedAt             time.Time
 }
 
 // Cache preserves the most recent successful sample so probe failures can
@@ -93,6 +105,16 @@ func Float64(v float64) *float64 {
 	return &v
 }
 
+// Int64 returns a pointer to v.
+func Int64(v int64) *int64 {
+	return &v
+}
+
+// String returns a pointer to v.
+func String(v string) *string {
+	return &v
+}
+
 // ServerRoot strips a trailing /v1 path component from an OpenAI-compatible
 // base URL while preserving the scheme, host, and any prefix path.
 func ServerRoot(baseURL string) string {
@@ -156,5 +178,38 @@ func clone(sample EndpointUtilization) EndpointUtilization {
 	if sample.MaxConcurrency != nil {
 		out.MaxConcurrency = Int(*sample.MaxConcurrency)
 	}
+	if sample.TotalPromptTokens != nil {
+		out.TotalPromptTokens = Int(*sample.TotalPromptTokens)
+	}
+	if sample.TotalCompletionTokens != nil {
+		out.TotalCompletionTokens = Int(*sample.TotalCompletionTokens)
+	}
+	if sample.CacheHitType != nil {
+		out.CacheHitType = String(*sample.CacheHitType)
+	}
+	if sample.CachedTokens != nil {
+		out.CachedTokens = Int(*sample.CachedTokens)
+	}
+	if sample.GeneratedTokens != nil {
+		out.GeneratedTokens = Int(*sample.GeneratedTokens)
+	}
+	if sample.ActiveRequestPhase != nil {
+		out.ActiveRequestPhase = String(*sample.ActiveRequestPhase)
+	}
+	if sample.TTFTSeconds != nil {
+		out.TTFTSeconds = Float64(*sample.TTFTSeconds)
+	}
+	if sample.TokensPerSecond != nil {
+		out.TokensPerSecond = Float64(*sample.TokensPerSecond)
+	}
+	if sample.MetalActiveMemoryBytes != nil {
+		out.MetalActiveMemoryBytes = Int64(*sample.MetalActiveMemoryBytes)
+	}
+	if sample.MetalPeakMemoryBytes != nil {
+		out.MetalPeakMemoryBytes = Int64(*sample.MetalPeakMemoryBytes)
+	}
+	if sample.MetalCacheMemoryBytes != nil {
+		out.MetalCacheMemoryBytes = Int64(*sample.MetalCacheMemoryBytes)
+	}
 	return out
 }
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
