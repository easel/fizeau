<bead-review>
  <bead id="fizeau-fc7b8878" iter=1>
    <title>Score utilization performance context and stickiness</title>
    <description>
Add score components for normalized utilization, performance/latency/throughput, context headroom, and sticky server-instance affinity. Unknown signals should carry a mild explicit penalty or neutral value according to policy, not an accidental zero that dominates routing.
    </description>
    <acceptance>
1. Candidate ScoreComponents include power, cost, deployment/locality, quota/health, utilization, performance, context headroom, and sticky affinity where applicable.
2. Unknown utilization/performance is represented deliberately and tested so it cannot beat known healthy evidence by accident.
3. Hard saturation can reject or heavily penalize a candidate; mild load only affects ranking.
4. Statement-backed tests prove severe pressure can beat sticky affinity while a merely better score does not churn an existing sticky server.
5. go test ./internal/routing ./cmd/fiz -run "Score|Utilization|Performance|Context|Sticky" passes.
    </acceptance>
    <labels>area:routing, kind:task</labels>
  </bead>

  <changed-files>
    <file>internal/routing/engine.go</file>
    <file>internal/routing/score.go</file>
    <file>internal/routing/score_signals_test.go</file>
    <file>internal/routing/sticky_lease_test.go</file>
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

  <diff rev="e9b2022e720c0961eaa34a6a62791f9cc46ff4f0">
<untrusted-data>
diff --git a/internal/routing/engine.go b/internal/routing/engine.go
index 7f3bd565..24822349 100644
--- a/internal/routing/engine.go
+++ b/internal/routing/engine.go
@@ -173,6 +173,7 @@ type Candidate struct {
 	Power              int
 	ContextLength      int
 	ContextSource      string
+	ScoreComponents    map[string]float64
 	Eligible           bool
 	Reason             string
 
@@ -448,6 +449,7 @@ func Resolve(req Request, in Inputs) (*Decision, error) {
 			continue
 		}
 		ranked[i].out.Score = scorePolicy(req.Profile, ranked[i].internal)
+		ranked[i].out.ScoreComponents = scoreComponents(req.Profile, ranked[i].internal)
 		ranked[i].out.Reason = fmt.Sprintf("profile=%s; score=%.1f", req.Profile, ranked[i].out.Score)
 	}
 	neutralCost, hasKnownCost := neutralKnownCost(ranked)
diff --git a/internal/routing/score.go b/internal/routing/score.go
index 16fba48f..227f8a96 100644
--- a/internal/routing/score.go
+++ b/internal/routing/score.go
@@ -26,42 +26,78 @@ const unknownPerformancePenalty = 0.0
 // supplied by the caller (cooldown demotion, observation perf bias,
 // provider-affinity bias).
 func scorePolicy(profile string, cand candidateInternal) float64 {
+	total := 0.0
+	for _, component := range scoreComponents(profile, cand) {
+		total += component
+	}
+	return total
+}
+
+func scoreComponents(profile string, cand candidateInternal) map[string]float64 {
+	components := map[string]float64{
+		"base":                100,
+		"cost":                0,
+		"deployment_locality": 0,
+		"quota_health":        0,
+		"utilization":         0,
+		"performance":         0,
+		"power":               0,
+		"context_headroom":    0,
+		"sticky_affinity":     0,
+	}
 	base := 100.0
 	cr := costClassRank[cand.CostClass]
 	withinQuota := cand.IsSubscription && cand.QuotaOK
+	add := func(name string, value float64) {
+		if value == 0 {
+			return
+		}
+		components[name] += value
+	}
 
 	switch profile {
 	case "cheap":
 		if cand.CostClass == "local" {
 			base += 40
+			add("deployment_locality", 40)
 		} else if withinQuota {
 			base += 20
+			add("quota_health", 20)
 		}
 		base -= float64(cr) * 30
+		add("cost", -float64(cr)*30)
 
 	case "standard":
 		if cand.CostClass == "local" {
 			base += 25
+			add("deployment_locality", 25)
 		} else if withinQuota {
 			base += 15
+			add("quota_health", 15)
 		}
 		base -= float64(cr) * 10
+		add("cost", -float64(cr)*10)
 
 	case "smart":
 		// Quality first; higher cost rank approximates higher capability.
 		base += float64(cr) * 20
+		add("cost", float64(cr)*20)
 		if withinQuota {
 			base += 5
+			add("quota_health", 5)
 		}
 
 	default:
 		// Treat unspecified as standard.
 		if cand.CostClass == "local" {
 			base += 25
+			add("deployment_locality", 25)
 		} else if withinQuota {
 			base += 15
+			add("quota_health", 15)
 		}
 		base -= float64(cr) * 10
+		add("cost", -float64(cr)*10)
 	}
 
 	// Provider preference bias.
@@ -69,10 +105,12 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 	case "local-first", "":
 		if cand.CostClass == "local" {
 			base += 30
+			add("deployment_locality", 30)
 		}
 	case "subscription-first":
 		if cand.IsSubscription && cand.QuotaOK {
 			base += 30
+			add("quota_health", 30)
 		}
 	}
 
@@ -81,21 +119,27 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 		// Stale quota penalty.
 		if cand.QuotaStale {
 			base -= 15
+			add("quota_health", -15)
 		}
 
 		// Trend-based adjustments.
 		switch cand.QuotaTrend {
 		case "exhausting":
 			base -= 40
+			add("quota_health", -40)
 		case "burning":
 			base -= 20
+			add("quota_health", -20)
 		case "healthy":
 			base += 10
+			add("quota_health", 10)
 		}
 
 		// Quota near-limit penalty (>= 80% used).
 		if cand.QuotaPercentUsed >= 80 {
-			base -= float64(cand.QuotaPercentUsed-80) * 2
+			penalty := float64(cand.QuotaPercentUsed-80) * 2
+			base -= penalty
+			add("quota_health", -penalty)
 		}
 	}
 
@@ -104,27 +148,33 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 		switch {
 		case cand.HistoricalSuccessRate >= 0.8:
 			base += 20
+			add("quota_health", 20)
 		case cand.HistoricalSuccessRate < 0.5:
 			base -= 30
+			add("quota_health", -30)
 		}
 	}
 	if cand.ProviderSuccessRate >= 0 {
 		switch {
 		case cand.ProviderSuccessRate >= 0.8:
 			base += 25
+			add("quota_health", 25)
 		case cand.ProviderSuccessRate < 0.5:
 			base -= 35
+			add("quota_health", -35)
 		}
 	}
 
 	// Cooldown demotion: candidate has had recent failures.
 	if cand.InCooldown {
 		base -= 50
+		add("quota_health", -50)
 	}
 
 	// Sticky affinity is a bonus after eligibility, not a hard pin.
 	if cand.StickyMatch {
 		base += StickyAffinityBonus
+		add("sticky_affinity", StickyAffinityBonus)
 	}
 
 	// Utilization pressure can outweigh stickiness when the chosen server is
@@ -133,6 +183,7 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 	// neutral rather than a hidden zero-value bonus.
 	if cand.EndpointSaturated {
 		base -= 300
+		add("utilization", -300)
 	}
 	if cand.EndpointLoad > 0 {
 		loadPenalty := cand.EndpointLoad * 10
@@ -145,8 +196,10 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 			loadPenalty = 60
 		}
 		base -= loadPenalty
+		add("utilization", -loadPenalty)
 	} else if !cand.EndpointLoadFresh {
 		base -= unknownUtilizationPenalty
+		add("utilization", -unknownUtilizationPenalty)
 	}
 
 	// Provider affinity: explicit provider pins are filtered before scoring;
@@ -154,10 +207,12 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 	// that share the pinned provider identity.
 	if cand.ProviderAffinityMatch {
 		base += 15
+		add("deployment_locality", 15)
 	}
 
 	if cand.Power > 0 {
 		base += float64(cand.Power) * 12
+		add("power", float64(cand.Power)*12)
 	}
 	if cand.Power > 0 && cand.CostUSDPer1kTokens > 0 {
 		costPenalty := cand.CostUSDPer1kTokens * 500
@@ -165,17 +220,21 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 			costPenalty = 60
 		}
 		base -= costPenalty
+		add("cost", -costPenalty)
 	}
 	if cand.Power > 0 && cand.CostUSDPer1kTokens == 0 {
 		switch {
 		case cand.CostClass == "local":
 			base += 15
+			add("cost", 15)
 		case cand.IsSubscription && cand.QuotaOK && !cand.QuotaStale && cand.QuotaPercentUsed < 80:
 			base += 15
+			add("quota_health", 15)
 		}
 	}
 	if cand.Power >= 9 && cand.IsSubscription && cand.QuotaOK && !cand.QuotaStale && cand.QuotaPercentUsed < 80 {
 		base += 20
+		add("power", 20)
 	}
 
 	// Context headroom is a ranking signal for otherwise eligible candidates.
@@ -187,28 +246,38 @@ func scorePolicy(profile string, cand candidateInternal) float64 {
 			headroomBonus = 30
 		}
 		base += headroomBonus
+		add("context_headroom", headroomBonus)
 	}
 
 	// Observation-derived perf bias.
 	havePerfSignal := false
 	if cand.ObservedTokensPerSec > 0 {
 		// Small additive bonus, scaled.
-		base += cand.ObservedTokensPerSec / 100.0
+		bonus := cand.ObservedTokensPerSec / 100.0
+		base += bonus
+		add("performance", bonus)
 		havePerfSignal = true
 	}
 	if cand.ObservedLatencyMS > 0 {
 		// Latency is a tiebreaker-scale signal: faster endpoints gain a small
 		// bonus while very slow endpoints receive little benefit.
-		base += 1000.0 / cand.ObservedLatencyMS
+		bonus := 1000.0 / cand.ObservedLatencyMS
+		base += bonus
+		add("performance", bonus)
 		havePerfSignal = true
 	}
 	if !havePerfSignal {
 		// Missing performance data is deliberate; current policy is neutral.
 		base -= unknownPerformancePenalty
+		add("performance", -unknownPerformancePenalty)
 	}
 	if cand.CostClass == "experimental" {
 		base -= 75
+		add("deployment_locality", -75)
 	}
 
-	return base
+	// base tracks the implicit profile baseline so the components sum to the
+	// same total as scorePolicy's legacy behavior.
+	components["base"] = base - (components["cost"] + components["deployment_locality"] + components["quota_health"] + components["sticky_affinity"] + components["utilization"] + components["power"] + components["context_headroom"] + components["performance"])
+	return components
 }
diff --git a/internal/routing/score_signals_test.go b/internal/routing/score_signals_test.go
index 03d3f17c..fa11931d 100644
--- a/internal/routing/score_signals_test.go
+++ b/internal/routing/score_signals_test.go
@@ -60,12 +60,35 @@ func TestResolvePenalizesUnknownUtilizationAndPerformance(t *testing.T) {
 	if known.Score <= unknown.Score {
 		t.Fatalf("known score %.1f should beat unknown score %.1f", known.Score, unknown.Score)
 	}
+	if known.ScoreComponents == nil || unknown.ScoreComponents == nil {
+		t.Fatalf("score components must be populated: known=%v unknown=%v", known.ScoreComponents, unknown.ScoreComponents)
+	}
+	for _, key := range []string{"base", "cost", "deployment_locality", "quota_health", "utilization", "performance", "power", "context_headroom", "sticky_affinity"} {
+		if _, ok := known.ScoreComponents[key]; !ok {
+			t.Fatalf("known score components missing %q: %+v", key, known.ScoreComponents)
+		}
+		if _, ok := unknown.ScoreComponents[key]; !ok {
+			t.Fatalf("unknown score components missing %q: %+v", key, unknown.ScoreComponents)
+		}
+	}
 	if known.Utilization <= 0 {
 		t.Fatalf("known utilization component=%v, want positive known load", known.Utilization)
 	}
 	if unknown.Utilization != 0 {
 		t.Fatalf("unknown utilization component=%v, want 0 for unknown load", unknown.Utilization)
 	}
+	if known.ScoreComponents["utilization"] == 0 {
+		t.Fatalf("known utilization score component=%v, want explicit known-load adjustment", known.ScoreComponents["utilization"])
+	}
+	if unknown.ScoreComponents["utilization"] != 0 {
+		t.Fatalf("unknown utilization score component=%v, want 0 for unknown load", unknown.ScoreComponents["utilization"])
+	}
+	if known.ScoreComponents["performance"] <= 0 {
+		t.Fatalf("known performance score component=%v, want positive known performance", known.ScoreComponents["performance"])
+	}
+	if unknown.ScoreComponents["performance"] != 0 {
+		t.Fatalf("unknown performance score component=%v, want 0 for unknown performance", unknown.ScoreComponents["performance"])
+	}
 	if known.StickyAffinity != 0 || unknown.StickyAffinity != 0 {
 		t.Fatalf("unexpected sticky affinity components: known=%v unknown=%v", known.StickyAffinity, unknown.StickyAffinity)
 	}
@@ -133,6 +156,9 @@ func TestResolveReportsStickyAffinityComponentWhenStickyKeyMatches(t *testing.T)
 	if sticky.StickyAffinity != StickyAffinityBonus {
 		t.Fatalf("sticky affinity component=%v, want %v", sticky.StickyAffinity, StickyAffinityBonus)
 	}
+	if sticky.ScoreComponents["sticky_affinity"] != StickyAffinityBonus {
+		t.Fatalf("sticky score component=%v, want %v", sticky.ScoreComponents["sticky_affinity"], StickyAffinityBonus)
+	}
 	if sticky.Utilization <= 0 {
 		t.Fatalf("sticky utilization component=%v, want positive load", sticky.Utilization)
 	}
diff --git a/internal/routing/sticky_lease_test.go b/internal/routing/sticky_lease_test.go
index f465dccc..5efe841f 100644
--- a/internal/routing/sticky_lease_test.go
+++ b/internal/routing/sticky_lease_test.go
@@ -90,6 +90,24 @@ func TestResolveStickyServerInstanceAcrossModelChanges(t *testing.T) {
 	if second.Candidates[0].ServerInstance != "server-a" {
 		t.Fatalf("sticky top candidate=%#v, want server-a", second.Candidates[0])
 	}
+	var sticky, nonSticky Candidate
+	for _, c := range second.Candidates {
+		switch c.ServerInstance {
+		case "server-a":
+			sticky = c
+		case "server-b":
+			nonSticky = c
+		}
+	}
+	if sticky.ServerInstance != "server-a" || nonSticky.ServerInstance != "server-b" {
+		t.Fatalf("second candidates=%#v, want both server-a and server-b", second.Candidates)
+	}
+	if nonSticky.Utilization >= sticky.Utilization {
+		t.Fatalf("non-sticky utilization=%v, want lower than sticky utilization=%v", nonSticky.Utilization, sticky.Utilization)
+	}
+	if sticky.Score <= nonSticky.Score {
+		t.Fatalf("sticky score=%.2f should beat the merely better non-sticky score=%.2f", sticky.Score, nonSticky.Score)
+	}
 
 	controlInputs := baseInputs
 	controlInputs.StickyServerInstanceResolver = nil
@@ -128,4 +146,22 @@ func TestResolveStickyServerInstanceAcrossModelChanges(t *testing.T) {
 	if saturated.Candidates[0].ServerInstance != "server-b" {
 		t.Fatalf("saturated top candidate=%#v, want server-b", saturated.Candidates[0])
 	}
+	var saturatedSticky, saturatedNonSticky Candidate
+	for _, c := range saturated.Candidates {
+		switch c.ServerInstance {
+		case "server-a":
+			saturatedSticky = c
+		case "server-b":
+			saturatedNonSticky = c
+		}
+	}
+	if !saturatedSticky.Eligible || !saturatedNonSticky.Eligible {
+		t.Fatalf("saturated candidates must remain eligible for score comparison: %+v", saturated.Candidates)
+	}
+	if saturatedSticky.ScoreComponents["utilization"] >= saturatedNonSticky.ScoreComponents["utilization"] {
+		t.Fatalf("saturated sticky utilization component=%v, want worse than non-sticky=%v", saturatedSticky.ScoreComponents["utilization"], saturatedNonSticky.ScoreComponents["utilization"])
+	}
+	if saturatedSticky.Score >= saturatedNonSticky.Score {
+		t.Fatalf("saturated sticky score=%.2f should lose to non-sticky score=%.2f", saturatedSticky.Score, saturatedNonSticky.Score)
+	}
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
