<bead-review>
  <bead id="fizeau-67145c14" iter=1>
    <title>Expose sticky utilization routing evidence</title>
    <description>
Expose sticky route and endpoint utilization evidence through public route/status/session surfaces. In-scope files: public service structs if needed, route-status JSON/text rendering, session-log routing decision projection, and tests. Requirements: RouteDecision/Candidate/RouteStatus show selected endpoint, sticky assignment status, sticky key status without leaking sensitive keys when inappropriate, utilization source, freshness, active/queued counts, max concurrency/cache pressure when known, and score components. Session artifacts record enough facts to explain why a worker stayed pinned or why a new key chose an endpoint. Out of scope: implementing provider probes, lease store internals, and route scoring policy.
    </description>
    <acceptance>
1. go test ./cmd/fiz ./internal/session ./... -run 'RouteStatus|RoutingDecision|Session|Utilization|Sticky' passes. 2. route-status --json includes endpoint, sticky state, utilization source/freshness, active/queued counts when known, and score components. 3. Session artifacts include selected endpoint and sticky assignment reason for routed requests. 4. Text rendering remains readable and does not require parsing private provider internals. 5. Public contract docs stay aligned with any exported fields.
    </acceptance>
    <labels>area:cli, kind:observability</labels>
  </bead>

  <changed-files>
    <file>agentcli/routing_cli_test.go</file>
    <file>agentcli/routing_smart.go</file>
    <file>docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md</file>
    <file>internal/routehealth/utilization_store.go</file>
    <file>internal/serviceimpl/session_projection_test.go</file>
    <file>internal/session/event.go</file>
    <file>internal/session/replay.go</file>
    <file>internal/session/replay_test.go</file>
    <file>service.go</file>
    <file>service_events.go</file>
    <file>service_execute.go</file>
    <file>service_route_leases.go</file>
    <file>service_route_leases_test.go</file>
    <file>service_routestatus.go</file>
    <file>service_routestatus_test.go</file>
    <file>service_routing.go</file>
    <file>service_session_log.go</file>
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

  <diff rev="8998cfa06cb3cb9eb9b9084438c6a82afc2066a8">
<untrusted-data>
diff --git a/agentcli/routing_cli_test.go b/agentcli/routing_cli_test.go
index 541e5ac..cdee158 100644
--- a/agentcli/routing_cli_test.go
+++ b/agentcli/routing_cli_test.go
@@ -480,23 +480,56 @@ providers:
 		SuccessRate float64 `json:"success_rate"`
 		Capability  float64 `json:"capability"`
 	}
+	type sticky struct {
+		KeyPresent bool   `json:"key_present"`
+		Assignment string `json:"assignment"`
+		Reason     string `json:"reason"`
+	}
+	type utilization struct {
+		Source         string   `json:"source"`
+		Freshness      string   `json:"freshness"`
+		ActiveRequests *int     `json:"active_requests"`
+		QueuedRequests *int     `json:"queued_requests"`
+		MaxConcurrency *int     `json:"max_concurrency"`
+		CachePressure  *float64 `json:"cache_pressure"`
+	}
 	type candidate struct {
-		Harness      string    `json:"harness"`
-		Provider     string    `json:"provider"`
-		Model        string    `json:"model"`
-		Score        float64   `json:"score"`
-		Components   component `json:"components"`
-		Eligible     bool      `json:"eligible"`
-		FilterReason string    `json:"filter_reason"`
-		Winner       bool      `json:"winner"`
+		Harness      string      `json:"harness"`
+		Provider     string      `json:"provider"`
+		Endpoint     string      `json:"endpoint"`
+		Model        string      `json:"model"`
+		Score        float64     `json:"score"`
+		Components   component   `json:"components"`
+		Utilization  utilization `json:"utilization"`
+		Eligible     bool        `json:"eligible"`
+		FilterReason string      `json:"filter_reason"`
+		Winner       bool        `json:"winner"`
 	}
 	var parsed struct {
-		Model      string      `json:"model"`
-		Winner     *candidate  `json:"winner"`
-		Candidates []candidate `json:"candidates"`
+		Model            string      `json:"model"`
+		SelectedEndpoint string      `json:"selected_endpoint"`
+		Sticky           sticky      `json:"sticky"`
+		Utilization      utilization `json:"utilization"`
+		Winner           *candidate  `json:"winner"`
+		Candidates       []candidate `json:"candidates"`
 	}
 	require.NoError(t, json.Unmarshal([]byte(out.stdout), &parsed), "stdout=%s", out.stdout)
 	assert.Equal(t, "qwen3.5-27b", parsed.Model)
+	var generic map[string]json.RawMessage
+	require.NoError(t, json.Unmarshal([]byte(out.stdout), &generic), "stdout=%s", out.stdout)
+	for _, key := range []string{"selected_endpoint", "sticky", "utilization"} {
+		if _, ok := generic[key]; !ok {
+			t.Fatalf("missing %q in route-status JSON: %s", key, out.stdout)
+		}
+	}
+	var candidateGeneric []map[string]json.RawMessage
+	require.NoError(t, json.Unmarshal(generic["candidates"], &candidateGeneric))
+	if len(candidateGeneric) == 0 {
+		t.Fatal("expected at least one candidate in route-status JSON")
+	}
+	if _, ok := candidateGeneric[0]["utilization"]; !ok {
+		t.Fatalf("missing candidate utilization in route-status JSON: %s", out.stdout)
+	}
 
 	// Each candidate carries the new structured shape from
 	// service.ResolveRoute: provider, model, score, components, eligible bool,
@@ -537,6 +570,8 @@ providers:
 	require.NotNil(t, parsed.Winner, "an eligible winner must be selected")
 	assert.True(t, parsed.Winner.Eligible)
 	assert.Empty(t, parsed.Winner.FilterReason, "winner must have an empty filter_reason")
+	assert.Equal(t, parsed.Winner.Endpoint, parsed.SelectedEndpoint)
+	assert.False(t, parsed.Sticky.KeyPresent, "no correlation id was supplied, so sticky key should be absent")
 
 	// The winner must also appear inside the candidates array and be flagged.
 	winnerInList := 0
@@ -548,6 +583,10 @@ providers:
 		}
 	}
 	assert.Equal(t, 1, winnerInList, "exactly one candidate should be flagged winner")
+	for _, c := range parsed.Candidates {
+		_ = c.Utilization.Source
+		_ = c.Utilization.Freshness
+	}
 }
 
 // TestRouteStatus_ShowsEligibleCandidatesPerIntent asserts that
diff --git a/agentcli/routing_smart.go b/agentcli/routing_smart.go
index 794c9a1..d4a5289 100644
--- a/agentcli/routing_smart.go
+++ b/agentcli/routing_smart.go
@@ -769,30 +769,34 @@ type routeStatusComponents struct {
 }
 
 type routeStatusCandidate struct {
-	Harness      string                `json:"harness,omitempty"`
-	Provider     string                `json:"provider"`
-	Endpoint     string                `json:"endpoint,omitempty"`
-	Model        string                `json:"model"`
-	Score        float64               `json:"score"`
-	Components   routeStatusComponents `json:"components"`
-	Eligible     bool                  `json:"eligible"`
-	FilterReason string                `json:"filter_reason"`
-	Reason       string                `json:"reason,omitempty"`
-	CostSource   string                `json:"cost_source,omitempty"`
-	Winner       bool                  `json:"winner"`
+	Harness      string                           `json:"harness,omitempty"`
+	Provider     string                           `json:"provider"`
+	Endpoint     string                           `json:"endpoint,omitempty"`
+	Model        string                           `json:"model"`
+	Score        float64                          `json:"score"`
+	Components   routeStatusComponents            `json:"components"`
+	Utilization  rootfizeau.RouteUtilizationState `json:"utilization,omitempty"`
+	Eligible     bool                             `json:"eligible"`
+	FilterReason string                           `json:"filter_reason"`
+	Reason       string                           `json:"reason,omitempty"`
+	CostSource   string                           `json:"cost_source,omitempty"`
+	Winner       bool                             `json:"winner"`
 }
 
 type routeStatusOutput struct {
-	Profile    string                 `json:"profile,omitempty"`
-	Model      string                 `json:"model,omitempty"`
-	ModelRef   string                 `json:"model_ref,omitempty"`
-	Provider   string                 `json:"provider,omitempty"`
-	Harness    string                 `json:"harness,omitempty"`
-	MinPower   int                    `json:"min_power,omitempty"`
-	MaxPower   int                    `json:"max_power,omitempty"`
-	Winner     *routeStatusCandidate  `json:"winner,omitempty"`
-	Candidates []routeStatusCandidate `json:"candidates"`
-	Error      string                 `json:"error,omitempty"`
+	Profile          string                           `json:"profile,omitempty"`
+	Model            string                           `json:"model,omitempty"`
+	ModelRef         string                           `json:"model_ref,omitempty"`
+	Provider         string                           `json:"provider,omitempty"`
+	Harness          string                           `json:"harness,omitempty"`
+	MinPower         int                              `json:"min_power,omitempty"`
+	MaxPower         int                              `json:"max_power,omitempty"`
+	SelectedEndpoint string                           `json:"selected_endpoint,omitempty"`
+	Sticky           rootfizeau.RouteStickyState      `json:"sticky,omitempty"`
+	Utilization      rootfizeau.RouteUtilizationState `json:"utilization,omitempty"`
+	Winner           *routeStatusCandidate            `json:"winner,omitempty"`
+	Candidates       []routeStatusCandidate           `json:"candidates"`
+	Error            string                           `json:"error,omitempty"`
 }
 
 // cmdRouteStatus reports the routing engine's eligible-candidate trace for a
@@ -870,6 +874,9 @@ func cmdRouteStatus(workDir string, args []string) int {
 		out.Error = resolveErr.Error()
 	}
 	if decision != nil {
+		out.SelectedEndpoint = decision.Endpoint
+		out.Sticky = decision.Sticky
+		out.Utilization = decision.Utilization
 		winnerSet := decision.Harness != "" || decision.Provider != "" || decision.Model != ""
 		for _, c := range decision.Candidates {
 			entry := routeStatusCandidate{
@@ -882,6 +889,7 @@ func cmdRouteStatus(workDir string, args []string) int {
 				FilterReason: c.FilterReason,
 				Reason:       c.Reason,
 				CostSource:   c.CostSource,
+				Utilization:  c.Utilization,
 				Components: routeStatusComponents{
 					Power:            c.Components.Power,
 					Cost:             c.Components.Cost,
@@ -932,21 +940,52 @@ func cmdRouteStatus(workDir string, args []string) int {
 		fmt.Printf("Max Power: %d\n", out.MaxPower)
 	}
 	if out.Winner != nil {
-		fmt.Printf("Winner: %s/%s/%s score=%.2f\n", out.Winner.Harness, out.Winner.Provider, out.Winner.Model, out.Winner.Score)
+		fmt.Printf("Winner: %s/%s/%s endpoint=%s score=%.2f\n", out.Winner.Harness, out.Winner.Provider, out.Winner.Model, out.Winner.Endpoint, out.Winner.Score)
+	}
+	if out.SelectedEndpoint != "" {
+		fmt.Printf("Selected endpoint: %s\n", out.SelectedEndpoint)
+	}
+	if out.Sticky.KeyPresent || out.Sticky.Assignment != "" || out.Sticky.Reason != "" {
+		fmt.Printf("Sticky: key=%s assignment=%s", stickyLabel(out.Sticky.KeyPresent), labelOrUnknown(out.Sticky.Assignment))
+		if out.Sticky.Reason != "" {
+			fmt.Printf(" reason=%s", out.Sticky.Reason)
+		}
+		fmt.Println()
+	}
+	if out.Utilization.Source != "" || out.Utilization.Freshness != "" || out.Utilization.ActiveRequests != nil || out.Utilization.QueuedRequests != nil || out.Utilization.MaxConcurrency != nil || out.Utilization.CachePressure != nil {
+		fmt.Printf("Utilization: source=%s freshness=%s", labelOrUnknown(out.Utilization.Source), labelOrUnknown(out.Utilization.Freshness))
+		if out.Utilization.ActiveRequests != nil {
+			fmt.Printf(" active=%d", *out.Utilization.ActiveRequests)
+		}
+		if out.Utilization.QueuedRequests != nil {
+			fmt.Printf(" queued=%d", *out.Utilization.QueuedRequests)
+		}
+		if out.Utilization.MaxConcurrency != nil {
+			fmt.Printf(" max=%d", *out.Utilization.MaxConcurrency)
+		}
+		if out.Utilization.CachePressure != nil {
+			fmt.Printf(" cache_pressure=%.2f", *out.Utilization.CachePressure)
+		}
+		fmt.Println()
 	}
 	if resolveErr != nil {
 		fmt.Fprintf(os.Stderr, "error: %s\n", resolveErr)
 	}
-	fmt.Printf("%-10s %-12s %-32s %-5s %-5s %-9s %-10s %-10s %-9s %s\n",
-		"HARNESS", "PROVIDER", "MODEL", "ELIG", "POWER", "SCORE", "COST", "LATENCY", "SUCCESS", "FILTER_REASON")
+	fmt.Printf("%-10s %-12s %-14s %-32s %-5s %-5s %-9s %-10s %-10s %-9s %-10s %s\n",
+		"HARNESS", "PROVIDER", "ENDPOINT", "MODEL", "ELIG", "POWER", "SCORE", "COST", "LATENCY", "SUCCESS", "SELECTED", "FILTER_REASON")
 	for _, c := range out.Candidates {
 		elig := "no"
 		if c.Eligible {
 			elig = "yes"
 		}
-		fmt.Printf("%-10s %-12s %-32s %-5s %-5d %-9.2f %-10.4f %-10.0f %-9.2f %s\n",
+		selected := "-"
+		if c.Winner {
+			selected = "yes"
+		}
+		fmt.Printf("%-10s %-12s %-14s %-32s %-5s %-5d %-9.2f %-10.4f %-10.0f %-9.2f %-10s %s\n",
 			c.Harness,
 			c.Provider,
+			truncate(c.Endpoint, 14),
 			truncate(c.Model, 32),
 			elig,
 			c.Components.Power,
@@ -954,6 +993,7 @@ func cmdRouteStatus(workDir string, args []string) int {
 			c.Components.Cost,
 			c.Components.LatencyMS,
 			c.Components.SuccessRate,
+			selected,
 			c.FilterReason,
 		)
 	}
@@ -994,3 +1034,17 @@ func truncate(value string, n int) string {
 	}
 	return value[:n-2] + ".."
 }
+
+func stickyLabel(present bool) string {
+	if present {
+		return "present"
+	}
+	return "absent"
+}
+
+func labelOrUnknown(v string) string {
+	if v == "" {
+		return "unknown"
+	}
+	return v
+}
diff --git a/docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md b/docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md
index 557d15a..8cb5431 100644
--- a/docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md
+++ b/docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md
@@ -104,9 +104,13 @@ type FizeauService interface {
     // ResolveRoute resolves a single under-specified request to a concrete
     // (Harness, ProviderSource/Endpoint, Model). The returned RouteDecision is
     // informational: operator dashboards, route-status displays, and debug
-    // surfaces. It is not re-injectable into Execute. Execute always re-resolves
-    // on its own inputs (idempotent for the same caller intent, modulo health
-    // changes which is the intended behavior).
+    // surfaces. RouteDecision includes the selected endpoint, sticky-lease
+    // evidence (status only; never the raw key), and endpoint utilization
+    // evidence when available so operators can explain why a worker stayed
+    // pinned or why a fresh key picked a specific endpoint. It is not
+    // re-injectable into Execute. Execute always re-resolves on its own inputs
+    // (idempotent for the same caller intent, modulo health changes which is
+    // the intended behavior).
     ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDecision, error)
 
     // RecordRouteAttempt records availability feedback about externally routed
@@ -115,8 +119,9 @@ type FizeauService interface {
     RecordRouteAttempt(ctx context.Context, attempt RouteAttempt) error
 
     // RouteStatus returns global routing state across live provider/model
-    // candidates: cooldowns, recent decisions, observation-derived
-    // per-(provider source, endpoint, model) latency.
+    // candidates: cooldowns, recent decisions, sticky assignment status,
+    // selected endpoint, and observation-derived per-(provider source,
+    // endpoint, model) latency / utilization evidence.
     // Distinct from per-request ResolveRoute — this is the read-only operator
     // dashboard view.
     RouteStatus(ctx context.Context) (*RouteStatusReport, error)
diff --git a/internal/routehealth/utilization_store.go b/internal/routehealth/utilization_store.go
index b6e18b6..efd5b16 100644
--- a/internal/routehealth/utilization_store.go
+++ b/internal/routehealth/utilization_store.go
@@ -66,6 +66,34 @@ func (s *UtilizationStore) Record(provider, endpoint, model string, sample utili
 	s.samples[key] = cloneUtilization(sample)
 }
 
+// Sample returns the most recent utilization sample for provider/endpoint/model.
+// When the endpoint-specific sample is unavailable, provider-wide samples are
+// considered as a fallback so callers can still surface coarse utilization
+// evidence without guessing at private probe internals.
+func (s *UtilizationStore) Sample(provider, endpoint, model string) (utilization.EndpointUtilization, bool) {
+	if s == nil {
+		return utilization.EndpointUtilization{}, false
+	}
+	keyProvider := strings.TrimSpace(provider)
+	keyEndpoint := strings.TrimSpace(endpoint)
+	keyModel := strings.TrimSpace(model)
+
+	s.mu.RLock()
+	defer s.mu.RUnlock()
+	if len(s.samples) == 0 {
+		return utilization.EndpointUtilization{}, false
+	}
+	if sample, ok := s.samples[NormalizeUtilizationKey(keyProvider, keyEndpoint, keyModel)]; ok {
+		return cloneUtilization(sample), true
+	}
+	if keyProvider != "" {
+		if sample, ok := s.samples[NormalizeUtilizationKey(keyProvider, "", keyModel)]; ok {
+			return cloneUtilization(sample), true
+		}
+	}
+	return utilization.EndpointUtilization{}, false
+}
+
 // EndpointLoads returns the normalized load per endpoint for provider/model.
 // Fresh utilization samples are combined with the supplied lease counts;
 // stale or missing samples fall back to lease counts only.
diff --git a/internal/serviceimpl/session_projection_test.go b/internal/serviceimpl/session_projection_test.go
index 234be67..faf1db9 100644
--- a/internal/serviceimpl/session_projection_test.go
+++ b/internal/serviceimpl/session_projection_test.go
@@ -42,8 +42,28 @@ func TestWriteAndReplaySessionLog(t *testing.T) {
 	dir := t.TempDir()
 	path := filepath.Join(dir, "svc-1.jsonl")
 	logger := session.NewLogger(dir, "svc-1")
-	logger.Emit(agentcore.EventSessionStart, session.SessionStartData{Prompt: "hello"})
-	logger.Emit(agentcore.EventSessionEnd, session.SessionEndData{Status: agentcore.StatusSuccess})
+	logger.Emit(agentcore.EventSessionStart, session.SessionStartData{
+		Prompt:           "hello",
+		SelectedEndpoint: "desk-a",
+		Sticky: session.RoutingStickyState{
+			KeyPresent: true,
+			Assignment: "acquired",
+			Reason:     "new sticky lease acquired",
+		},
+		Utilization: session.RoutingUtilizationState{
+			Source:    "llama-server.slots",
+			Freshness: "fresh",
+		},
+	})
+	logger.Emit(agentcore.EventSessionEnd, session.SessionEndData{
+		Status:           agentcore.StatusSuccess,
+		SelectedEndpoint: "desk-a",
+		Sticky: session.RoutingStickyState{
+			KeyPresent: true,
+			Assignment: "acquired",
+			Reason:     "new sticky lease acquired",
+		},
+	})
 	if err := logger.Close(); err != nil {
 		t.Fatalf("Close: %v", err)
 	}
@@ -55,6 +75,9 @@ func TestWriteAndReplaySessionLog(t *testing.T) {
 	if !strings.Contains(raw.String(), "\"type\": \"session.start\"") {
 		t.Fatalf("raw log missing session.start: %s", raw.String())
 	}
+	if !strings.Contains(raw.String(), "\"selected_endpoint\": \"desk-a\"") {
+		t.Fatalf("raw log missing selected_endpoint: %s", raw.String())
+	}
 
 	var replay bytes.Buffer
 	if err := ReplaySession(context.Background(), path, &replay); err != nil {
@@ -63,6 +86,9 @@ func TestWriteAndReplaySessionLog(t *testing.T) {
 	if !strings.Contains(replay.String(), "Session svc-1") {
 		t.Fatalf("replay missing session header: %s", replay.String())
 	}
+	if !strings.Contains(replay.String(), "Selected endpoint: desk-a") {
+		t.Fatalf("replay missing selected endpoint: %s", replay.String())
+	}
 }
 
 func TestUsageReportDelegatesToSessionAggregation(t *testing.T) {
diff --git a/internal/session/event.go b/internal/session/event.go
index 71ae927..80e4129 100644
--- a/internal/session/event.go
+++ b/internal/session/event.go
@@ -9,25 +9,28 @@ import (
 
 // SessionStartData is the data payload for a session.start event.
 type SessionStartData struct {
-	Provider           string            `json:"provider"`
-	Model              string            `json:"model"`
-	SelectedProvider   string            `json:"selected_provider,omitempty"`
-	SelectedRoute      string            `json:"selected_route,omitempty"`
-	RequestedHarness   string            `json:"requested_harness,omitempty"`
-	ResolvedHarness    string            `json:"resolved_harness,omitempty"`
-	HarnessSource      string            `json:"harness_source,omitempty"`
-	RequestedModel     string            `json:"requested_model,omitempty"`
-	RequestedModelRef  string            `json:"requested_model_ref,omitempty"`
-	ResolvedModelRef   string            `json:"resolved_model_ref,omitempty"`
-	ResolvedModel      string            `json:"resolved_model,omitempty"`
-	Reasoning          agent.Reasoning   `json:"reasoning,omitempty"`
-	AttemptedProviders []string          `json:"attempted_providers,omitempty"`
-	FailoverCount      int               `json:"failover_count,omitempty"`
-	WorkDir            string            `json:"work_dir"`
-	MaxIterations      int               `json:"max_iterations"`
-	Prompt             string            `json:"prompt"`
-	SystemPrompt       string            `json:"system_prompt,omitempty"`
-	Metadata           map[string]string `json:"metadata,omitempty"`
+	Provider           string                  `json:"provider"`
+	Model              string                  `json:"model"`
+	SelectedProvider   string                  `json:"selected_provider,omitempty"`
+	SelectedEndpoint   string                  `json:"selected_endpoint,omitempty"`
+	SelectedRoute      string                  `json:"selected_route,omitempty"`
+	Sticky             RoutingStickyState      `json:"sticky,omitempty"`
+	Utilization        RoutingUtilizationState `json:"utilization,omitempty"`
+	RequestedHarness   string                  `json:"requested_harness,omitempty"`
+	ResolvedHarness    string                  `json:"resolved_harness,omitempty"`
+	HarnessSource      string                  `json:"harness_source,omitempty"`
+	RequestedModel     string                  `json:"requested_model,omitempty"`
+	RequestedModelRef  string                  `json:"requested_model_ref,omitempty"`
+	ResolvedModelRef   string                  `json:"resolved_model_ref,omitempty"`
+	ResolvedModel      string                  `json:"resolved_model,omitempty"`
+	Reasoning          agent.Reasoning         `json:"reasoning,omitempty"`
+	AttemptedProviders []string                `json:"attempted_providers,omitempty"`
+	FailoverCount      int                     `json:"failover_count,omitempty"`
+	WorkDir            string                  `json:"work_dir"`
+	MaxIterations      int                     `json:"max_iterations"`
+	Prompt             string                  `json:"prompt"`
+	SystemPrompt       string                  `json:"system_prompt,omitempty"`
+	Metadata           map[string]string       `json:"metadata,omitempty"`
 }
 
 // LLMRequestData is the data payload for an llm.request event.
@@ -75,26 +78,49 @@ type ToolCallData struct {
 
 // SessionEndData is the data payload for a session.end event.
 type SessionEndData struct {
-	Status             agent.Status      `json:"status"`
-	Output             string            `json:"output"`
-	Tokens             agent.TokenUsage  `json:"tokens"`
-	CostUSD            *float64          `json:"cost_usd,omitempty"`
-	DurationMs         int64             `json:"duration_ms"`
-	Model              string            `json:"model,omitempty"`
-	SelectedProvider   string            `json:"selected_provider,omitempty"`
-	SelectedRoute      string            `json:"selected_route,omitempty"`
-	RequestedHarness   string            `json:"requested_harness,omitempty"`
-	ResolvedHarness    string            `json:"resolved_harness,omitempty"`
-	HarnessSource      string            `json:"harness_source,omitempty"`
-	RequestedModel     string            `json:"requested_model,omitempty"`
-	RequestedModelRef  string            `json:"requested_model_ref,omitempty"`
-	ResolvedModelRef   string            `json:"resolved_model_ref,omitempty"`
-	ResolvedModel      string            `json:"resolved_model,omitempty"`
-	Reasoning          agent.Reasoning   `json:"reasoning,omitempty"`
-	AttemptedProviders []string          `json:"attempted_providers,omitempty"`
-	FailoverCount      int               `json:"failover_count,omitempty"`
-	Metadata           map[string]string `json:"metadata,omitempty"`
-	Error              string            `json:"error,omitempty"`
+	Status             agent.Status            `json:"status"`
+	Output             string                  `json:"output"`
+	Tokens             agent.TokenUsage        `json:"tokens"`
+	CostUSD            *float64                `json:"cost_usd,omitempty"`
+	DurationMs         int64                   `json:"duration_ms"`
+	Model              string                  `json:"model,omitempty"`
+	SelectedProvider   string                  `json:"selected_provider,omitempty"`
+	SelectedEndpoint   string                  `json:"selected_endpoint,omitempty"`
+	SelectedRoute      string                  `json:"selected_route,omitempty"`
+	Sticky             RoutingStickyState      `json:"sticky,omitempty"`
+	Utilization        RoutingUtilizationState `json:"utilization,omitempty"`
+	RequestedHarness   string                  `json:"requested_harness,omitempty"`
+	ResolvedHarness    string                  `json:"resolved_harness,omitempty"`
+	HarnessSource      string                  `json:"harness_source,omitempty"`
+	RequestedModel     string                  `json:"requested_model,omitempty"`
+	RequestedModelRef  string                  `json:"requested_model_ref,omitempty"`
+	ResolvedModelRef   string                  `json:"resolved_model_ref,omitempty"`
+	ResolvedModel      string                  `json:"resolved_model,omitempty"`
+	Reasoning          agent.Reasoning         `json:"reasoning,omitempty"`
+	AttemptedProviders []string                `json:"attempted_providers,omitempty"`
+	FailoverCount      int                     `json:"failover_count,omitempty"`
+	Metadata           map[string]string       `json:"metadata,omitempty"`
+	Error              string                  `json:"error,omitempty"`
+}
+
+// RoutingStickyState summarizes sticky routing behavior without exposing
+// the raw sticky key.
+type RoutingStickyState struct {
+	KeyPresent bool   `json:"key_present,omitempty"`
+	Assignment string `json:"assignment,omitempty"`
+	Reason     string `json:"reason,omitempty"`
+}
+
+// RoutingUtilizationState carries the live endpoint sample that informed a
+// routing decision.
+type RoutingUtilizationState struct {
+	Source         string    `json:"source,omitempty"`
+	Freshness      string    `json:"freshness,omitempty"`
+	ActiveRequests *int      `json:"active_requests,omitempty"`
+	QueuedRequests *int      `json:"queued_requests,omitempty"`
+	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
+	CachePressure  *float64  `json:"cache_pressure,omitempty"`
+	ObservedAt     time.Time `json:"observed_at,omitempty"`
 }
 
 // NewEvent creates an Event with the given type and data, auto-assigning
diff --git a/internal/session/replay.go b/internal/session/replay.go
index ded2444..9870190 100644
--- a/internal/session/replay.go
+++ b/internal/session/replay.go
@@ -54,6 +54,38 @@ func Replay(path string, w io.Writer) error {
 			fmt.Fprintf(w, "=== Session %s ===\n", e.SessionID)
 			fmt.Fprintf(w, "Time: %s\n", e.Timestamp.Format("2006-01-02 15:04:05 UTC"))
 			fmt.Fprintf(w, "Provider: %s | Model: %s\n", data.Provider, data.Model)
+			if data.SelectedEndpoint != "" {
+				fmt.Fprintf(w, "Selected endpoint: %s\n", data.SelectedEndpoint)
+			}
+			if data.Sticky.KeyPresent || data.Sticky.Assignment != "" || data.Sticky.Reason != "" {
+				fmt.Fprintf(w, "Sticky: key=%s assignment=%s",
+					routingStickyLabel(data.Sticky.KeyPresent),
+					labelOrUnknown(data.Sticky.Assignment))
+				if data.Sticky.Reason != "" {
+					fmt.Fprintf(w, " reason=%s", data.Sticky.Reason)
+				}
+				fmt.Fprintln(w)
+			}
+			if data.Utilization.Source != "" || data.Utilization.Freshness != "" ||
+				data.Utilization.ActiveRequests != nil || data.Utilization.QueuedRequests != nil ||
+				data.Utilization.MaxConcurrency != nil || data.Utilization.CachePressure != nil {
+				fmt.Fprintf(w, "Utilization: source=%s freshness=%s",
+					labelOrUnknown(data.Utilization.Source),
+					labelOrUnknown(data.Utilization.Freshness))
+				if data.Utilization.ActiveRequests != nil {
+					fmt.Fprintf(w, " active=%d", *data.Utilization.ActiveRequests)
+				}
+				if data.Utilization.QueuedRequests != nil {
+					fmt.Fprintf(w, " queued=%d", *data.Utilization.QueuedRequests)
+				}
+				if data.Utilization.MaxConcurrency != nil {
+					fmt.Fprintf(w, " max=%d", *data.Utilization.MaxConcurrency)
+				}
+				if data.Utilization.CachePressure != nil {
+					fmt.Fprintf(w, " cache_pressure=%.2f", *data.Utilization.CachePressure)
+				}
+				fmt.Fprintln(w)
+			}
 			fmt.Fprintf(w, "Max iterations: %d | Work dir: %s\n", data.MaxIterations, data.WorkDir)
 			if data.SystemPrompt != "" {
 				fmt.Fprintf(w, "\n[System]\n%s\n", data.SystemPrompt)
@@ -103,6 +135,9 @@ func Replay(path string, w io.Writer) error {
 			if data.Model != "" {
 				fmt.Fprintf(w, "Model: %s\n", data.Model)
 			}
+			if data.SelectedEndpoint != "" {
+				fmt.Fprintf(w, "Selected endpoint: %s\n", data.SelectedEndpoint)
+			}
 			fmt.Fprintf(w, "Duration: %dms | Tokens: %d in / %d out",
 				data.DurationMs, data.Tokens.Input, data.Tokens.Output)
 			if data.CostUSD == nil || *data.CostUSD < 0 {
@@ -128,6 +163,20 @@ func Replay(path string, w io.Writer) error {
 	return nil
 }
 
+func routingStickyLabel(present bool) string {
+	if present {
+		return "present"
+	}
+	return "absent"
+}
+
+func labelOrUnknown(v string) string {
+	if v == "" {
+		return "unknown"
+	}
+	return v
+}
+
 func compactJSON(raw json.RawMessage) string {
 	var v any
 	if json.Unmarshal(raw, &v) != nil {
diff --git a/internal/session/replay_test.go b/internal/session/replay_test.go
index 9816b95..789c53b 100644
--- a/internal/session/replay_test.go
+++ b/internal/session/replay_test.go
@@ -23,8 +23,22 @@ func TestReplay(t *testing.T) {
 	// Write a test session log
 	logger := NewLogger(dir, sessionID)
 	logger.Emit(agent.EventSessionStart, SessionStartData{
-		Provider:      "lmstudio",
-		Model:         "qwen3.5-7b",
+		Provider:         "lmstudio",
+		Model:            "qwen3.5-7b",
+		SelectedEndpoint: "desk-b",
+		Sticky: RoutingStickyState{
+			KeyPresent: true,
+			Assignment: "reused",
+			Reason:     "live sticky lease reused",
+		},
+		Utilization: RoutingUtilizationState{
+			Source:         "llama-server.slots",
+			Freshness:      "fresh",
+			ActiveRequests: intPtr(1),
+			QueuedRequests: intPtr(0),
+			MaxConcurrency: intPtr(2),
+			CachePressure:  float64Ptr(0.5),
+		},
 		WorkDir:       "/tmp/test",
 		MaxIterations: 20,
 		Prompt:        "Read main.go",
@@ -66,6 +80,9 @@ func TestReplay(t *testing.T) {
 	output := buf.String()
 	assert.Contains(t, output, "Session replay-test")
 	assert.Contains(t, output, "qwen3.5-7b")
+	assert.Contains(t, output, "Selected endpoint: desk-b")
+	assert.Contains(t, output, "Sticky: key=present assignment=reused reason=live sticky lease reused")
+	assert.Contains(t, output, "Utilization: source=llama-server.slots freshness=fresh")
 	assert.Contains(t, output, "[System]")
 	assert.Contains(t, output, "You are a helpful assistant.")
 	assert.Contains(t, output, "[User]")
@@ -77,6 +94,10 @@ func TestReplay(t *testing.T) {
 	assert.Contains(t, output, "$0 (local)")
 }
 
+func intPtr(v int) *int {
+	return &v
+}
+
 func TestReplay_UnknownSessionCost(t *testing.T) {
 	dir := t.TempDir()
 	sessionID := "unknown-cost-test"
diff --git a/service.go b/service.go
index 738cf2c..4f4feb6 100644
--- a/service.go
+++ b/service.go
@@ -431,6 +431,12 @@ type RouteDecision struct {
 	Model string
 	// Reason summarizes why the selected candidate won.
 	Reason string
+	// Sticky captures whether this decision reused an existing sticky lease
+	// or created a new one.
+	Sticky RouteStickyState
+	// Utilization captures the endpoint sample that informed the selected
+	// candidate, when known.
+	Utilization RouteUtilizationState
 	// Power is the catalog-projected power of the selected Model
 	// (per CONTRACT-003 § Catalog Power Projection). 0 means
 	// unknown/exact-pin-only/no catalog entry. DDx callers read this
@@ -470,6 +476,8 @@ type RouteCandidate struct {
 	// success rate, quota, capability) that fed the final Score. Consumers use these to
 	// explain rankings without parsing the free-form Reason.
 	Components RouteCandidateComponents
+	// Utilization carries the normalized load sample used by the router.
+	Utilization RouteUtilizationState
 }
 
 // FilterReason* enumerate the canonical disqualification reasons surfaced
@@ -514,6 +522,26 @@ type RouteCandidateComponents struct {
 	Capability float64
 }
 
+// RouteStickyState describes sticky routing evidence without exposing the
+// underlying key.
+type RouteStickyState struct {
+	KeyPresent bool   `json:"key_present,omitempty"`
+	Assignment string `json:"assignment,omitempty"`
+	Reason     string `json:"reason,omitempty"`
+}
+
+// RouteUtilizationState summarizes the live utilization sample associated
+// with a candidate or selected endpoint.
+type RouteUtilizationState struct {
+	Source         string    `json:"source,omitempty"`
+	Freshness      string    `json:"freshness,omitempty"`
+	ActiveRequests *int      `json:"active_requests,omitempty"`
+	QueuedRequests *int      `json:"queued_requests,omitempty"`
+	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
+	CachePressure  *float64  `json:"cache_pressure,omitempty"`
+	ObservedAt     time.Time `json:"observed_at,omitempty"`
+}
+
 // RouteAttempt is caller feedback about one attempted route candidate.
 // Status="success" clears matching active failures; any other non-empty status
 // records a same-process failure until the service health cooldown expires.
@@ -553,11 +581,13 @@ const RouteStatusRoutingQualityWindow = 100
 
 // RouteStatusEntry describes the live provider candidates serving one model.
 type RouteStatusEntry struct {
-	Model          string
-	Strategy       string // informational; route tables are not user-authored
-	Candidates     []RouteCandidateStatus
-	LastDecision   *RouteDecision // most recent ResolveRoute result for this key (cached)
-	LastDecisionAt time.Time
+	Model            string
+	Strategy         string // informational; route tables are not user-authored
+	SelectedEndpoint string
+	Sticky           RouteStickyState
+	Candidates       []RouteCandidateStatus
+	LastDecision     *RouteDecision // most recent ResolveRoute result for this key (cached)
+	LastDecisionAt   time.Time
 }
 
 // RouteCandidateStatus describes a single live provider/model candidate.
diff --git a/service_events.go b/service_events.go
index 0bd5e41..66bec5c 100644
--- a/service_events.go
+++ b/service_events.go
@@ -165,21 +165,32 @@ func routingDecisionEventCandidates(in []RouteCandidate) []ServiceRoutingDecisio
 				QuotaTrend:       c.Components.QuotaTrend,
 				Capability:       c.Components.Capability,
 			},
+			Utilization: ServiceRoutingUtilizationState{
+				Source:         c.Utilization.Source,
+				Freshness:      c.Utilization.Freshness,
+				ActiveRequests: c.Utilization.ActiveRequests,
+				QueuedRequests: c.Utilization.QueuedRequests,
+				MaxConcurrency: c.Utilization.MaxConcurrency,
+				CachePressure:  c.Utilization.CachePressure,
+				ObservedAt:     c.Utilization.ObservedAt,
+			},
 		}
 	}
 	return out
 }
 
 type ServiceRoutingDecisionData struct {
-	Harness          string   `json:"harness"`
-	Provider         string   `json:"provider,omitempty"`
-	Endpoint         string   `json:"endpoint,omitempty"`
-	Model            string   `json:"model"`
-	Reason           string   `json:"reason"`
-	RequestedHarness string   `json:"requested_harness,omitempty"`
-	HarnessSource    string   `json:"harness_source,omitempty"`
-	FallbackChain    []string `json:"fallback_chain,omitempty"`
-	SessionID        string   `json:"session_id,omitempty"`
+	Harness          string                         `json:"harness"`
+	Provider         string                         `json:"provider,omitempty"`
+	Endpoint         string                         `json:"endpoint,omitempty"`
+	Model            string                         `json:"model"`
+	Reason           string                         `json:"reason"`
+	Sticky           ServiceRoutingStickyState      `json:"sticky,omitempty"`
+	Utilization      ServiceRoutingUtilizationState `json:"utilization,omitempty"`
+	RequestedHarness string                         `json:"requested_harness,omitempty"`
+	HarnessSource    string                         `json:"harness_source,omitempty"`
+	FallbackChain    []string                       `json:"fallback_chain,omitempty"`
+	SessionID        string                         `json:"session_id,omitempty"`
 
 	// Candidates exposes the full ranked decision trace. Each candidate
 	// carries per-axis component scores (cost / latency / success rate /
@@ -202,6 +213,7 @@ type ServiceRoutingDecisionCandidate struct {
 	Reason             string                           `json:"reason,omitempty"`
 	FilterReason       string                           `json:"filter_reason,omitempty"`
 	Components         ServiceRoutingDecisionComponents `json:"components"`
+	Utilization        ServiceRoutingUtilizationState   `json:"utilization,omitempty"`
 }
 
 // ServiceRoutingDecisionComponents exposes the per-axis score inputs.
@@ -218,6 +230,22 @@ type ServiceRoutingDecisionComponents struct {
 	Capability       float64 `json:"capability"`
 }
 
+type ServiceRoutingStickyState struct {
+	KeyPresent bool   `json:"key_present,omitempty"`
+	Assignment string `json:"assignment,omitempty"`
+	Reason     string `json:"reason,omitempty"`
+}
+
+type ServiceRoutingUtilizationState struct {
+	Source         string    `json:"source,omitempty"`
+	Freshness      string    `json:"freshness,omitempty"`
+	ActiveRequests *int      `json:"active_requests,omitempty"`
+	QueuedRequests *int      `json:"queued_requests,omitempty"`
+	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
+	CachePressure  *float64  `json:"cache_pressure,omitempty"`
+	ObservedAt     time.Time `json:"observed_at,omitempty"`
+}
+
 type ServiceStallData struct {
 	Reason string `json:"reason"`
 	Count  int64  `json:"count"`
diff --git a/service_execute.go b/service_execute.go
index 25b23a1..9e790bc 100644
--- a/service_execute.go
+++ b/service_execute.go
@@ -186,13 +186,18 @@ func (s *service) resolveExecuteRoute(req ServiceExecuteRequest) (*RouteDecision
 		return nil, err
 	}
 	resolvedModel := resolveSubprocessModelAlias(canonical, req.Model)
-	return &RouteDecision{
+	decision := &RouteDecision{
 		Harness:  canonical,
 		Provider: req.Provider,
 		Model:    resolvedModel,
 		Reason:   "explicit",
 		Power:    catalogPowerForModel(serviceRoutingCatalog(), resolvedModel),
-	}, nil
+	}
+	if decision.Endpoint == "" {
+		_, endpoint, _ := splitEndpointProviderRef(decision.Provider)
+		decision.Endpoint = endpoint
+	}
+	return decision, nil
 }
 
 func validateExplicitHarnessQuota(name string, cfg harnesses.HarnessConfig) error {
@@ -438,11 +443,25 @@ func (s *service) runExecute(ctx context.Context, req ServiceExecuteRequest, dec
 	// reserved keys).
 	routingMeta := metaWithRoleAndCorrelation(meta, req.Role, req.CorrelationID)
 	emitJSON(out, &seq, harnesses.EventTypeRoutingDecision, routingMeta, ServiceRoutingDecisionData{
-		Harness:          decision.Harness,
-		Provider:         decision.Provider,
-		Endpoint:         decision.Endpoint,
-		Model:            decision.Model,
-		Reason:           decision.Reason,
+		Harness:  decision.Harness,
+		Provider: decision.Provider,
+		Endpoint: decision.Endpoint,
+		Model:    decision.Model,
+		Reason:   decision.Reason,
+		Sticky: ServiceRoutingStickyState{
+			KeyPresent: decision.Sticky.KeyPresent,
+			Assignment: decision.Sticky.Assignment,
+			Reason:     decision.Sticky.Reason,
+		},
+		Utilization: ServiceRoutingUtilizationState{
+			Source:         decision.Utilization.Source,
+			Freshness:      decision.Utilization.Freshness,
+			ActiveRequests: decision.Utilization.ActiveRequests,
+			QueuedRequests: decision.Utilization.QueuedRequests,
+			MaxConcurrency: decision.Utilization.MaxConcurrency,
+			CachePressure:  decision.Utilization.CachePressure,
+			ObservedAt:     decision.Utilization.ObservedAt,
+		},
 		RequestedHarness: req.Harness,
 		HarnessSource:    harnessSource(req),
 		SessionID:        sessionID,
diff --git a/service_route_leases.go b/service_route_leases.go
index 2fa6e26..77c9b8e 100644
--- a/service_route_leases.go
+++ b/service_route_leases.go
@@ -28,7 +28,9 @@ func (s *service) applyStickyRouteLease(stickyKey string, decision *RouteDecisio
 	if s == nil || decision == nil || strings.TrimSpace(stickyKey) == "" {
 		return
 	}
+	decision.Sticky.KeyPresent = true
 	if decision.Harness != "agent" || decision.Provider == "" {
+		decision.Sticky.Assignment = "not_applicable"
 		return
 	}
 
@@ -48,6 +50,8 @@ func (s *service) applyStickyRouteLease(stickyKey string, decision *RouteDecisio
 			store.Acquire(now, stickyRouteLeaseTTL, key, baseProvider, lease.Endpoint, decision.Model)
 			decision.Provider = candidate.Provider
 			decision.Endpoint = candidate.Endpoint
+			decision.Sticky.Assignment = "reused"
+			decision.Sticky.Reason = "live sticky lease reused"
 			return
 		}
 		reason := "endpoint disappeared"
@@ -65,6 +69,7 @@ func (s *service) applyStickyRouteLease(stickyKey string, decision *RouteDecisio
 		store.Invalidate(now, key, reason)
 	}
 	if decision.Provider == "" && decision.Endpoint == "" {
+		decision.Sticky.Assignment = "none"
 		return
 	}
 	chosenEndpoint := decision.Endpoint
@@ -78,6 +83,10 @@ func (s *service) applyStickyRouteLease(stickyKey string, decision *RouteDecisio
 		return
 	}
 	store.Acquire(now, stickyRouteLeaseTTL, key, baseProvider, chosenEndpoint, decision.Model)
+	decision.Sticky.Assignment = "acquired"
+	if decision.Sticky.Reason == "" {
+		decision.Sticky.Reason = "new sticky lease acquired"
+	}
 }
 
 func stickyLeaseCandidate(candidates []RouteCandidate, harness, provider, model, endpoint string) (RouteCandidate, bool) {
diff --git a/service_route_leases_test.go b/service_route_leases_test.go
index 05959ff..ced0a2f 100644
--- a/service_route_leases_test.go
+++ b/service_route_leases_test.go
@@ -79,6 +79,9 @@ func TestResolveRouteStickyLeaseReusesEndpoint(t *testing.T) {
 	if first.Provider != "local@desk-b" || first.Endpoint != "desk-b" {
 		t.Fatalf("first decision=%#v, want desk-b", first)
 	}
+	if !first.Sticky.KeyPresent || first.Sticky.Assignment != "acquired" {
+		t.Fatalf("first sticky evidence=%#v, want acquired sticky lease", first.Sticky)
+	}
 
 	time.Sleep(30 * time.Millisecond)
 	now = time.Now().UTC()
@@ -111,6 +114,9 @@ func TestResolveRouteStickyLeaseReusesEndpoint(t *testing.T) {
 	if second.Provider != "local@desk-b" || second.Endpoint != "desk-b" {
 		t.Fatalf("sticky decision=%#v, want reused desk-b despite reversed baseline", second)
 	}
+	if second.Sticky.Assignment != "reused" {
+		t.Fatalf("second sticky evidence=%#v, want reused", second.Sticky)
+	}
 }
 
 func TestResolveRouteStickyLeaseDistributesNewKeysByLoad(t *testing.T) {
@@ -174,6 +180,12 @@ func TestResolveRouteStickyLeaseDistributesNewKeysByLoad(t *testing.T) {
 	if first.Provider != "local@desk-b" || first.Endpoint != "desk-b" {
 		t.Fatalf("first decision=%#v, want desk-b as the least-loaded endpoint", first)
 	}
+	if first.Utilization.Source != string(utilization.SourceLlamaSlots) {
+		t.Fatalf("first utilization source=%q, want %q", first.Utilization.Source, utilization.SourceLlamaSlots)
+	}
+	if first.Utilization.Freshness != string(utilization.FreshnessFresh) {
+		t.Fatalf("first utilization freshness=%q, want fresh", first.Utilization.Freshness)
+	}
 
 	second, err := svc.ResolveRoute(context.Background(), RouteRequest{
 		Harness:       "agent",
@@ -186,6 +198,9 @@ func TestResolveRouteStickyLeaseDistributesNewKeysByLoad(t *testing.T) {
 	if second.Provider != "local@desk-a" || second.Endpoint != "desk-a" {
 		t.Fatalf("second decision=%#v, want desk-a after desk-b lease increased load", second)
 	}
+	if second.Utilization.Source != string(utilization.SourceLlamaSlots) {
+		t.Fatalf("second utilization source=%q, want %q", second.Utilization.Source, utilization.SourceLlamaSlots)
+	}
 }
 
 func TestResolveRouteStickyLeaseAvoidsSaturatedEndpointForNewKey(t *testing.T) {
diff --git a/service_routestatus.go b/service_routestatus.go
index 6b7cff8..806f8f6 100644
--- a/service_routestatus.go
+++ b/service_routestatus.go
@@ -48,6 +48,8 @@ func (s *service) RouteStatus(ctx context.Context) (*RouteStatusReport, error) {
 			if cached, ok := s.lookupRouteDecision(model); ok {
 				entry.LastDecision = cached.decision
 				entry.LastDecisionAt = cached.at
+				entry.SelectedEndpoint = cached.decision.Endpoint
+				entry.Sticky = cached.decision.Sticky
 			}
 			entries[model] = entry
 			order = append(order, model)
diff --git a/service_routestatus_test.go b/service_routestatus_test.go
index 383cb91..04deef2 100644
--- a/service_routestatus_test.go
+++ b/service_routestatus_test.go
@@ -99,8 +99,10 @@ func TestRouteStatus_lastDecisionCached(t *testing.T) {
 	dec := &RouteDecision{
 		Harness:  "agent",
 		Provider: "bragi",
+		Endpoint: "desk-a",
 		Model:    "qwen3-27b",
 		Reason:   "test",
+		Sticky:   RouteStickyState{KeyPresent: true, Assignment: "reused", Reason: "live sticky lease reused"},
 	}
 	svc.cacheRouteDecision("qwen3-27b", dec)
 
@@ -124,6 +126,12 @@ func TestRouteStatus_lastDecisionCached(t *testing.T) {
 	if entry.LastDecisionAt.IsZero() {
 		t.Error("LastDecisionAt: should be non-zero")
 	}
+	if entry.SelectedEndpoint != "desk-a" {
+		t.Fatalf("SelectedEndpoint = %q, want desk-a", entry.SelectedEndpoint)
+	}
+	if !entry.Sticky.KeyPresent || entry.Sticky.Assignment != "reused" {
+		t.Fatalf("Sticky = %#v, want reused sticky evidence", entry.Sticky)
+	}
 }
 
 // TestRouteStatus_lastDecisionCached_viaResolveRoute verifies the full path:
@@ -177,6 +185,9 @@ func TestRouteStatus_lastDecisionCached_viaResolveRoute(t *testing.T) {
 	if found.LastDecision == nil {
 		t.Fatal("LastDecision: expected non-nil after successful ResolveRoute")
 	}
+	if dec.Endpoint != "" && found.SelectedEndpoint != dec.Endpoint {
+		t.Fatalf("SelectedEndpoint = %q, want %q", found.SelectedEndpoint, dec.Endpoint)
+	}
 }
 
 func TestRouteStatus_attemptCooldownStateSurfaces(t *testing.T) {
diff --git a/service_routing.go b/service_routing.go
index f83ef21..4c87055 100644
--- a/service_routing.go
+++ b/service_routing.go
@@ -9,6 +9,7 @@ import (
 
 	"github.com/DocumentDrivenDX/fizeau/internal/harnesses"
 	"github.com/DocumentDrivenDX/fizeau/internal/modelcatalog"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
 	"github.com/DocumentDrivenDX/fizeau/internal/routing"
 )
 
@@ -70,9 +71,15 @@ func (s *service) ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDec
 		if result == nil {
 			result = &RouteDecision{}
 		}
+		s.annotateRouteDecisionEvidence(result)
 		return result, publicRoutingError(err, result.Candidates)
 	}
 	s.applyStickyRouteLease(req.CorrelationID, result)
+	if result != nil && result.Endpoint == "" {
+		_, endpoint, _ := splitEndpointProviderRef(result.Provider)
+		result.Endpoint = endpoint
+	}
+	s.annotateRouteDecisionEvidence(result)
 	// Cache the decision so RouteStatus can surface LastDecision.
 	if result != nil {
 		result.Model = resolveSubprocessModelAlias(result.Harness, result.Model)
@@ -134,6 +141,72 @@ func routeCandidateFromInternal(candidate routing.Candidate) RouteCandidate {
 	}
 }
 
+func (s *service) annotateRouteDecisionEvidence(decision *RouteDecision) {
+	if s == nil || decision == nil {
+		return
+	}
+	decision.Utilization = s.routeUtilizationEvidence(decision.Provider, decision.Endpoint, decision.Model)
+	for i := range decision.Candidates {
+		decision.Candidates[i].Utilization = s.routeUtilizationEvidence(
+			decision.Candidates[i].Provider,
+			decision.Candidates[i].Endpoint,
+			decision.Candidates[i].Model,
+		)
+	}
+}
+
+func (s *service) routeUtilizationEvidence(provider, endpoint, model string) RouteUtilizationState {
+	if s == nil || s.routeUtilization == nil {
+		return RouteUtilizationState{}
+	}
+	keyProvider := strings.TrimSpace(provider)
+	keyEndpoint := strings.TrimSpace(endpoint)
+	if base, ep, ok := splitEndpointProviderRef(keyProvider); ok {
+		keyProvider = base
+		if keyEndpoint == "" {
+			keyEndpoint = ep
+		}
+	}
+	sample, ok := s.routeUtilization.Sample(keyProvider, keyEndpoint, model)
+	if !ok {
+		return RouteUtilizationState{}
+	}
+	return routeUtilizationStateFromSample(sample)
+}
+
+func routeUtilizationStateFromSample(sample utilization.EndpointUtilization) RouteUtilizationState {
+	out := RouteUtilizationState{
+		Source:     string(sample.Source),
+		Freshness:  string(sample.Freshness),
+		ObservedAt: sample.ObservedAt,
+	}
+	if sample.ActiveRequests != nil {
+		out.ActiveRequests = utilization.Int(*sample.ActiveRequests)
+	}
+	if sample.QueuedRequests != nil {
+		out.QueuedRequests = utilization.Int(*sample.QueuedRequests)
+	}
+	if sample.MaxConcurrency != nil {
+		out.MaxConcurrency = utilization.Int(*sample.MaxConcurrency)
+	}
+	if sample.CacheUsage != nil {
+		v := *sample.CacheUsage
+		out.CachePressure = &v
+	}
+	if out.CachePressure == nil && sample.MaxConcurrency != nil && *sample.MaxConcurrency > 0 {
+		total := 0
+		if sample.ActiveRequests != nil {
+			total += *sample.ActiveRequests
+		}
+		if sample.QueuedRequests != nil {
+			total += *sample.QueuedRequests
+		}
+		pressure := float64(total) / float64(*sample.MaxConcurrency)
+		out.CachePressure = &pressure
+	}
+	return out
+}
+
 // publicFilterReason maps the typed FilterReason emitted by the internal
 // routing engine to the public FilterReason* string constant. The internal
 // constants are defined to share string values with the public surface, so
diff --git a/service_session_log.go b/service_session_log.go
index fd45071..cbb56a2 100644
--- a/service_session_log.go
+++ b/service_session_log.go
@@ -22,6 +22,7 @@ type serviceSessionLog struct {
 	logger    *session.Logger
 	path      string
 	sessionID string
+	decision  RouteDecision
 	endOnce   sync.Once
 	endWrote  atomic.Bool
 	closeOnce sync.Once
@@ -39,16 +40,32 @@ func (s *service) openSessionLog(req ServiceExecuteRequest, decision RouteDecisi
 		logger:    logger,
 		path:      filepath.Join(req.SessionLogDir, sessionID+".jsonl"),
 		sessionID: sessionID,
+		decision:  decision,
 	}
 	// CONTRACT-003: echo top-level Role + CorrelationID into the
 	// session-log header (one line per session). Top-level wins over
 	// any caller Metadata entry under the reserved keys.
 	headerMeta := metaWithRoleAndCorrelation(req.Metadata, req.Role, req.CorrelationID)
 	start := session.SessionStartData{
-		Provider:          s.providerTypeLabel(decision.Provider),
-		Model:             decision.Model,
-		SelectedProvider:  decision.Provider,
-		SelectedRoute:     req.SelectedRoute,
+		Provider:         s.providerTypeLabel(decision.Provider),
+		Model:            decision.Model,
+		SelectedProvider: decision.Provider,
+		SelectedEndpoint: decision.Endpoint,
+		SelectedRoute:    req.SelectedRoute,
+		Sticky: session.RoutingStickyState{
+			KeyPresent: req.CorrelationID != "",
+			Assignment: decision.Sticky.Assignment,
+			Reason:     decision.Sticky.Reason,
+		},
+		Utilization: session.RoutingUtilizationState{
+			Source:         decision.Utilization.Source,
+			Freshness:      decision.Utilization.Freshness,
+			ActiveRequests: decision.Utilization.ActiveRequests,
+			QueuedRequests: decision.Utilization.QueuedRequests,
+			MaxConcurrency: decision.Utilization.MaxConcurrency,
+			CachePressure:  decision.Utilization.CachePressure,
+			ObservedAt:     decision.Utilization.ObservedAt,
+		},
 		RequestedHarness:  req.Harness,
 		ResolvedHarness:   decision.Harness,
 		HarnessSource:     harnessSource(req),
@@ -78,11 +95,26 @@ func (sl *serviceSessionLog) writeEnd(req ServiceExecuteRequest, meta map[string
 	sl.endOnce.Do(func() {
 		sl.endWrote.Store(true)
 		end := session.SessionEndData{
-			Status:            harnessStatusToCoreStatus(final.Status),
-			Output:            final.FinalText,
-			Tokens:            finalUsageToCoreTokens(final.Usage),
-			DurationMs:        final.DurationMS,
-			SelectedRoute:     req.SelectedRoute,
+			Status:           harnessStatusToCoreStatus(final.Status),
+			Output:           final.FinalText,
+			Tokens:           finalUsageToCoreTokens(final.Usage),
+			DurationMs:       final.DurationMS,
+			SelectedRoute:    req.SelectedRoute,
+			SelectedEndpoint: sl.decision.Endpoint,
+			Sticky: session.RoutingStickyState{
+				KeyPresent: req.CorrelationID != "",
+				Assignment: sl.decision.Sticky.Assignment,
+				Reason:     sl.decision.Sticky.Reason,
+			},
+			Utilization: session.RoutingUtilizationState{
+				Source:         sl.decision.Utilization.Source,
+				Freshness:      sl.decision.Utilization.Freshness,
+				ActiveRequests: sl.decision.Utilization.ActiveRequests,
+				QueuedRequests: sl.decision.Utilization.QueuedRequests,
+				MaxConcurrency: sl.decision.Utilization.MaxConcurrency,
+				CachePressure:  sl.decision.Utilization.CachePressure,
+				ObservedAt:     sl.decision.Utilization.ObservedAt,
+			},
 			RequestedHarness:  req.Harness,
 			ResolvedHarness:   "",
 			HarnessSource:     harnessSource(req),
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
