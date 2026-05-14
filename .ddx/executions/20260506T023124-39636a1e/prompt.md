<bead-review>
  <bead id="fizeau-ffc8a964" iter=1>
    <title>Route new sticky keys by endpoint load</title>
    <description>
Wire normalized endpoint utilization and in-process route leases into routing decisions for equivalent local endpoints. In-scope files: service_routing.go, internal/routing engine inputs/scoring, routehealth/service utilization aggregation, and routing tests. Requirements: existing sticky key wins when valid; new sticky keys choose least-loaded equivalent local/free endpoint serving the resolved model; load combines Fizeau in-flight lease counts with fresh provider utilization; saturated endpoints are avoided for new keys; stale utilization falls back to lease counts; utilization only affects ranking among equivalent eligible local endpoints and must not override hard pins, power bounds, model quality, or capability gates. Out of scope: provider probe implementation, route-status rendering, session artifact rendering, and shared multi-machine lease backend.
    </description>
    <acceptance>
1. go test ./internal/routing ./... -run 'Utilization|Sticky|RouteStatus|Routing' passes. 2. New sticky keys distribute across equivalent endpoints by normalized load. 3. Existing sticky keys stay pinned even if another endpoint later becomes less loaded. 4. A saturated endpoint is avoided for new sticky keys but does not move existing keys unless hard-saturation policy requires it. 5. Stale or missing provider utilization falls back to Fizeau lease counts. 6. Hard provider/model/harness pins and MinPower/MaxPower behavior are unchanged.
    </acceptance>
    <labels>area:routing, kind:feature</labels>
  </bead>

  <changed-files>
    <file>internal/routehealth/utilization_store.go</file>
    <file>internal/routehealth/utilization_store_test.go</file>
    <file>internal/routing/engine.go</file>
    <file>internal/routing/sticky_utilization_test.go</file>
    <file>service.go</file>
    <file>service_route_leases.go</file>
    <file>service_route_leases_test.go</file>
    <file>service_routing.go</file>
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

  <diff rev="b2d00d7877c8db143bdfd0e33b53772f0376031b">
<untrusted-data>
diff --git a/internal/routehealth/utilization_store.go b/internal/routehealth/utilization_store.go
new file mode 100644
index 0000000..b6e18b6
--- /dev/null
+++ b/internal/routehealth/utilization_store.go
@@ -0,0 +1,192 @@
+package routehealth
+
+import (
+	"sort"
+	"strings"
+	"sync"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+)
+
+// UtilizationKey identifies one provider endpoint/model utilization sample.
+type UtilizationKey struct {
+	Provider string
+	Endpoint string
+	Model    string
+}
+
+// EndpointLoad is the normalized load signal used by routing.
+type EndpointLoad struct {
+	LeaseCount           int
+	NormalizedLoad       float64
+	UtilizationFresh     bool
+	UtilizationSaturated bool
+}
+
+// UtilizationStore retains the most recent utilization sample for each
+// provider endpoint. It is safe for concurrent use.
+type UtilizationStore struct {
+	mu      sync.RWMutex
+	samples map[UtilizationKey]utilization.EndpointUtilization
+}
+
+// NewUtilizationStore returns an empty utilization store.
+func NewUtilizationStore() *UtilizationStore {
+	return &UtilizationStore{
+		samples: make(map[UtilizationKey]utilization.EndpointUtilization),
+	}
+}
+
+// NormalizeUtilizationKey trims whitespace from the utilization dimensions.
+func NormalizeUtilizationKey(provider, endpoint, model string) UtilizationKey {
+	return UtilizationKey{
+		Provider: strings.TrimSpace(provider),
+		Endpoint: strings.TrimSpace(endpoint),
+		Model:    strings.TrimSpace(model),
+	}
+}
+
+// Record stores the latest utilization sample for key.
+func (s *UtilizationStore) Record(provider, endpoint, model string, sample utilization.EndpointUtilization) {
+	if s == nil {
+		return
+	}
+	key := NormalizeUtilizationKey(provider, endpoint, model)
+	if key.Provider == "" && key.Endpoint == "" && key.Model == "" {
+		return
+	}
+	if sample.Freshness == "" {
+		sample.Freshness = utilization.FreshnessFresh
+	}
+	s.mu.Lock()
+	defer s.mu.Unlock()
+	if s.samples == nil {
+		s.samples = make(map[UtilizationKey]utilization.EndpointUtilization)
+	}
+	s.samples[key] = cloneUtilization(sample)
+}
+
+// EndpointLoads returns the normalized load per endpoint for provider/model.
+// Fresh utilization samples are combined with the supplied lease counts;
+// stale or missing samples fall back to lease counts only.
+func (s *UtilizationStore) EndpointLoads(provider, model string, leaseCounts map[string]int) map[string]EndpointLoad {
+	if s == nil {
+		return nil
+	}
+	keyProvider := strings.TrimSpace(provider)
+	keyModel := strings.TrimSpace(model)
+
+	s.mu.RLock()
+	defer s.mu.RUnlock()
+	if len(s.samples) == 0 && len(leaseCounts) == 0 {
+		return nil
+	}
+
+	keys := make(map[string]struct{})
+	for endpoint := range leaseCounts {
+		keys[endpoint] = struct{}{}
+	}
+	for key := range s.samples {
+		if keyProvider != "" && key.Provider != keyProvider {
+			continue
+		}
+		if keyModel != "" && key.Model != keyModel {
+			continue
+		}
+		if key.Endpoint != "" {
+			keys[key.Endpoint] = struct{}{}
+		}
+	}
+	if len(keys) == 0 {
+		return nil
+	}
+
+	out := make(map[string]EndpointLoad, len(keys))
+	for endpoint := range keys {
+		leaseCount := leaseCounts[endpoint]
+		load := EndpointLoad{
+			LeaseCount:       leaseCount,
+			NormalizedLoad:   float64(leaseCount),
+			UtilizationFresh: false,
+		}
+		key := NormalizeUtilizationKey(keyProvider, endpoint, keyModel)
+		sample, ok := s.samples[key]
+		if !ok && keyProvider != "" {
+			// Allow provider-wide samples to match when the caller does not
+			// have a fully-qualified endpoint name.
+			sample, ok = s.samples[NormalizeUtilizationKey(keyProvider, "", keyModel)]
+		}
+		if !ok || sample.Freshness != utilization.FreshnessFresh {
+			out[endpoint] = load
+			continue
+		}
+		normalized, saturated := normalizedLoadFromSample(sample)
+		load.NormalizedLoad = float64(leaseCount) + normalized
+		load.UtilizationFresh = true
+		load.UtilizationSaturated = saturated
+		out[endpoint] = load
+	}
+
+	return out
+}
+
+func normalizedLoadFromSample(sample utilization.EndpointUtilization) (float64, bool) {
+	var active int
+	var queued int
+	if sample.ActiveRequests != nil {
+		active = *sample.ActiveRequests
+	}
+	if sample.QueuedRequests != nil {
+		queued = *sample.QueuedRequests
+	}
+	if sample.MaxConcurrency != nil && *sample.MaxConcurrency > 0 {
+		total := active + queued
+		pressure := float64(total) / float64(*sample.MaxConcurrency)
+		if pressure < 0 {
+			pressure = 0
+		}
+		return pressure, total >= *sample.MaxConcurrency
+	}
+	if sample.CacheUsage != nil {
+		pressure := *sample.CacheUsage
+		if pressure < 0 {
+			pressure = 0
+		}
+		return pressure, pressure >= 1
+	}
+	total := active + queued
+	if total < 0 {
+		total = 0
+	}
+	return float64(total), false
+}
+
+func cloneUtilization(sample utilization.EndpointUtilization) utilization.EndpointUtilization {
+	out := sample
+	if sample.ActiveRequests != nil {
+		out.ActiveRequests = utilization.Int(*sample.ActiveRequests)
+	}
+	if sample.QueuedRequests != nil {
+		out.QueuedRequests = utilization.Int(*sample.QueuedRequests)
+	}
+	if sample.CacheUsage != nil {
+		out.CacheUsage = utilization.Float64(*sample.CacheUsage)
+	}
+	if sample.MaxConcurrency != nil {
+		out.MaxConcurrency = utilization.Int(*sample.MaxConcurrency)
+	}
+	return out
+}
+
+// EndpointLoadList returns a deterministic endpoint ordering for tests.
+func EndpointLoadList(loads map[string]EndpointLoad) []string {
+	if len(loads) == 0 {
+		return nil
+	}
+	out := make([]string, 0, len(loads))
+	for endpoint := range loads {
+		out = append(out, endpoint)
+	}
+	sort.Strings(out)
+	return out
+}
diff --git a/internal/routehealth/utilization_store_test.go b/internal/routehealth/utilization_store_test.go
new file mode 100644
index 0000000..1ac57bc
--- /dev/null
+++ b/internal/routehealth/utilization_store_test.go
@@ -0,0 +1,66 @@
+package routehealth
+
+import (
+	"testing"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+)
+
+func TestUtilizationStoreEndpointLoadsUsesFreshSamplesAndLeaseFallback(t *testing.T) {
+	store := NewUtilizationStore()
+	store.Record("local", "desk-a", "model-a", utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(1),
+		QueuedRequests: utilization.Int(1),
+		MaxConcurrency: utilization.Int(4),
+		Source:         utilization.SourceVLLMMetrics,
+		Freshness:      utilization.FreshnessFresh,
+	})
+	store.Record("local", "desk-b", "model-a", utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(9),
+		QueuedRequests: utilization.Int(9),
+		MaxConcurrency: utilization.Int(10),
+		Source:         utilization.SourceLlamaMetrics,
+		Freshness:      utilization.FreshnessStale,
+	})
+
+	loads := store.EndpointLoads("local", "model-a", map[string]int{
+		"desk-a": 2,
+		"desk-b": 1,
+	})
+
+	if got := loads["desk-a"]; !got.UtilizationFresh {
+		t.Fatalf("desk-a fresh=%v, want true", got.UtilizationFresh)
+	} else if got.UtilizationSaturated {
+		t.Fatalf("desk-a saturated=%v, want false", got.UtilizationSaturated)
+	} else if got.NormalizedLoad != 2.5 {
+		t.Fatalf("desk-a normalized_load=%v, want 2.5", got.NormalizedLoad)
+	}
+
+	if got := loads["desk-b"]; got.UtilizationFresh {
+		t.Fatalf("desk-b fresh=%v, want false for stale sample", got.UtilizationFresh)
+	} else if got.NormalizedLoad != 1 {
+		t.Fatalf("desk-b normalized_load=%v, want lease-count fallback 1", got.NormalizedLoad)
+	}
+}
+
+func TestUtilizationStoreEndpointLoadsMarksSaturationFromFreshCapacity(t *testing.T) {
+	store := NewUtilizationStore()
+	store.Record("local", "desk-a", "model-a", utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(2),
+		QueuedRequests: utilization.Int(0),
+		MaxConcurrency: utilization.Int(2),
+		Source:         utilization.SourceVLLMMetrics,
+		Freshness:      utilization.FreshnessFresh,
+	})
+
+	loads := store.EndpointLoads("local", "model-a", map[string]int{
+		"desk-a": 0,
+	})
+	got := loads["desk-a"]
+	if !got.UtilizationSaturated {
+		t.Fatalf("desk-a saturated=%v, want true", got.UtilizationSaturated)
+	}
+	if got.NormalizedLoad != 1 {
+		t.Fatalf("desk-a normalized_load=%v, want 1", got.NormalizedLoad)
+	}
+}
diff --git a/internal/routing/engine.go b/internal/routing/engine.go
index c95d741..9c83957 100644
--- a/internal/routing/engine.go
+++ b/internal/routing/engine.go
@@ -290,13 +290,15 @@ var ProfileEscalationLadder = []string{"cheap", "standard", "smart"}
 
 // Inputs bundles the engine's external data sources.
 type Inputs struct {
-	Harnesses           []HarnessEntry
-	HistoricalSuccess   map[string]float64   // by harness name; -1 = insufficient data
-	ObservedSpeedTPS    map[string]float64   // by "provider:model"
-	ProviderSuccessRate map[string]float64   // by ProviderModelKey(provider, endpoint, model)
-	ObservedLatencyMS   map[string]float64   // by ProviderModelKey(provider, endpoint, model)
-	ProviderCooldowns   map[string]time.Time // by provider name
-	CooldownDuration    time.Duration        // 0 = no cooldown enforcement
+	Harnesses            []HarnessEntry
+	HistoricalSuccess    map[string]float64 // by harness name; -1 = insufficient data
+	ObservedSpeedTPS     map[string]float64 // by "provider:model"
+	ProviderSuccessRate  map[string]float64 // by ProviderModelKey(provider, endpoint, model)
+	ObservedLatencyMS    map[string]float64 // by ProviderModelKey(provider, endpoint, model)
+	EndpointLoads        map[string]EndpointLoad
+	EndpointLoadResolver func(provider, endpoint, model string) (EndpointLoad, bool)
+	ProviderCooldowns    map[string]time.Time // by provider name
+	CooldownDuration     time.Duration        // 0 = no cooldown enforcement
 
 	// ProviderQuotaExhaustedUntil maps provider name → retry_after time.
 	// A provider with retry_after > Now is treated as quota_exhausted and
@@ -318,6 +320,15 @@ type Inputs struct {
 	ReasoningResolver func(profile, surface string) (resolved string, ok bool)
 }
 
+// EndpointLoad is the routing engine's normalized view of endpoint load for a
+// single provider/model tuple.
+type EndpointLoad struct {
+	LeaseCount           int
+	NormalizedLoad       float64
+	UtilizationFresh     bool
+	UtilizationSaturated bool
+}
+
 // ModelEligibility is the routing engine's catalog-power view for one model.
 // Unknown or zero-power models are still usable through exact Model pins, but
 // are excluded from unpinned automatic routing.
@@ -350,6 +361,9 @@ type candidateInternal struct {
 	InCooldown            bool
 	ProviderAffinityMatch bool
 	ProviderPreference    string
+	EndpointLoad          float64
+	EndpointLoadFresh     bool
+	EndpointSaturated     bool
 }
 
 // ProviderModelKey is the metrics key used by routing callers for provider
@@ -437,6 +451,14 @@ func Resolve(req Request, in Inputs) (*Decision, error) {
 		if li != lj {
 			return li
 		}
+		if sameLocalEndpointGroup(ranked[i].internal, ranked[j].internal) {
+			if ranked[i].internal.EndpointSaturated != ranked[j].internal.EndpointSaturated {
+				return !ranked[i].internal.EndpointSaturated
+			}
+			if ranked[i].internal.EndpointLoad != ranked[j].internal.EndpointLoad {
+				return ranked[i].internal.EndpointLoad < ranked[j].internal.EndpointLoad
+			}
+		}
 		if ranked[i].out.Harness != ranked[j].out.Harness {
 			return ranked[i].out.Harness < ranked[j].out.Harness
 		}
@@ -782,6 +804,15 @@ func buildHarnessCandidates(h HarnessEntry, req Request, in Inputs) []rankedCand
 			latencyMS = in.ObservedLatencyMS[ProviderModelKey(p.Name, "", model)]
 		}
 		power := candidatePower(in.ModelEligibility, model)
+		endpointLoad := EndpointLoad{}
+		if in.EndpointLoadResolver != nil {
+			loadProvider, loadEndpoint := candidateLoadIdentity(h, p)
+			if resolved, ok := in.EndpointLoadResolver(loadProvider, loadEndpoint, model); ok {
+				endpointLoad = resolved
+			}
+		} else if load, ok := in.EndpointLoads[key]; ok {
+			endpointLoad = load
+		}
 
 		gateReq := resolveRequestReasoning(req, h.Surface, in.ReasoningResolver)
 
@@ -897,6 +928,9 @@ func buildHarnessCandidates(h HarnessEntry, req Request, in Inputs) []rankedCand
 			InCooldown:            inCooldown || h.InCooldown,
 			ProviderAffinityMatch: req.Provider != "" && req.Provider == candidateProviderIdentity(h, p),
 			ProviderPreference:    req.ProviderPreference,
+			EndpointLoad:          endpointLoad.NormalizedLoad,
+			EndpointLoadFresh:     endpointLoad.UtilizationFresh,
+			EndpointSaturated:     endpointLoad.UtilizationSaturated,
 		}
 		out = append(out, rankedCandidate{
 			out: Candidate{
@@ -943,6 +977,18 @@ func candidateProviderIdentity(h HarnessEntry, p ProviderEntry) string {
 	return h.Name
 }
 
+func candidateLoadIdentity(h HarnessEntry, p ProviderEntry) (string, string) {
+	provider := candidateProviderIdentity(h, p)
+	endpoint := p.EndpointName
+	if base, ep, ok := strings.Cut(provider, "@"); ok && base != "" {
+		provider = base
+		if endpoint == "" {
+			endpoint = ep
+		}
+	}
+	return provider, endpoint
+}
+
 func candidateCostClass(h HarnessEntry, p ProviderEntry) string {
 	if p.CostClass != "" {
 		return p.CostClass
@@ -959,6 +1005,28 @@ func normalizeCostSource(source string) string {
 	}
 }
 
+func sameLocalEndpointGroup(a, b candidateInternal) bool {
+	if a.Harness == "" || b.Harness == "" || a.Model == "" || b.Model == "" {
+		return false
+	}
+	if a.CostClass != "local" || b.CostClass != "local" {
+		return false
+	}
+	aBase := providerBaseName(a.Provider)
+	bBase := providerBaseName(b.Provider)
+	if aBase == "" || bBase == "" || aBase != bBase {
+		return false
+	}
+	return a.Model == b.Model
+}
+
+func providerBaseName(provider string) string {
+	if base, _, ok := strings.Cut(provider, "@"); ok {
+		return base
+	}
+	return provider
+}
+
 func neutralKnownCost(candidates []rankedCandidate) (float64, bool) {
 	var total float64
 	var count int
diff --git a/internal/routing/sticky_utilization_test.go b/internal/routing/sticky_utilization_test.go
new file mode 100644
index 0000000..6b80f6a
--- /dev/null
+++ b/internal/routing/sticky_utilization_test.go
@@ -0,0 +1,55 @@
+package routing
+
+import "testing"
+
+func TestResolveUsesEndpointLoadToRankEquivalentLocalEndpoints(t *testing.T) {
+	in := Inputs{
+		Harnesses: []HarnessEntry{
+			{
+				Name:                "agent",
+				CostClass:           "local",
+				AutoRoutingEligible: true,
+				Available:           true,
+				ExactPinSupport:     true,
+				SupportsTools:       true,
+				Providers: []ProviderEntry{
+					{Name: "local@desk-a", EndpointName: "desk-a", DefaultModel: "model-a", CostClass: "local"},
+					{Name: "local@desk-b", EndpointName: "desk-b", DefaultModel: "model-a", CostClass: "local"},
+				},
+			},
+		},
+		ModelEligibility: func(model string) (ModelEligibility, bool) {
+			if model != "model-a" {
+				return ModelEligibility{}, false
+			}
+			return ModelEligibility{Power: 7, AutoRoutable: true}, true
+		},
+		EndpointLoadResolver: func(provider, endpoint, model string) (EndpointLoad, bool) {
+			if provider != "local" || model != "model-a" {
+				return EndpointLoad{}, false
+			}
+			switch endpoint {
+			case "desk-a":
+				return EndpointLoad{LeaseCount: 2, NormalizedLoad: 2.5}, true
+			case "desk-b":
+				return EndpointLoad{LeaseCount: 0, NormalizedLoad: 0.5}, true
+			default:
+				return EndpointLoad{}, false
+			}
+		},
+	}
+
+	dec, err := Resolve(Request{Harness: "agent", Model: "model-a"}, in)
+	if err != nil {
+		t.Fatalf("Resolve: %v", err)
+	}
+	if dec.Provider != "local@desk-b" || dec.Endpoint != "desk-b" {
+		t.Fatalf("decision=%#v, want desk-b as least-loaded equivalent local endpoint", dec)
+	}
+	if len(dec.Candidates) < 2 {
+		t.Fatalf("candidates=%#v, want both endpoints", dec.Candidates)
+	}
+	if dec.Candidates[0].Provider != "local@desk-b" {
+		t.Fatalf("top candidate=%#v, want desk-b", dec.Candidates[0])
+	}
+}
diff --git a/service.go b/service.go
index f20520e..738cf2c 100644
--- a/service.go
+++ b/service.go
@@ -721,8 +721,9 @@ type service struct {
 	// ResolveRoute; read by RouteStatus.
 	lastDecisionCache map[string]lastDecisionEntry
 
-	routeHealth *routehealth.Store
-	routeLeases *routehealth.LeaseStore
+	routeHealth      *routehealth.Store
+	routeLeases      *routehealth.LeaseStore
+	routeUtilization *routehealth.UtilizationStore
 
 	// catalog is the service-scope model-catalog cache. Populated lazily
 	// on first use by routing + chat paths; shared across requests so the
@@ -818,6 +819,7 @@ func New(opts ServiceOptions) (FizeauService, error) {
 		catalog:          newCatalogCache(catalogCacheOptions{AsyncRefreshTimeout: opts.catalogRefreshTimeout()}),
 		routeHealth:      routehealth.NewStore(),
 		routeLeases:      routehealth.NewLeaseStore(),
+		routeUtilization: routehealth.NewUtilizationStore(),
 		routingQuality:   newRoutingQualityStore(),
 		providerQuota:    NewProviderQuotaStateStore(),
 		providerBurnRate: NewProviderBurnRateTracker(),
diff --git a/service_route_leases.go b/service_route_leases.go
index 2c27668..2fa6e26 100644
--- a/service_route_leases.go
+++ b/service_route_leases.go
@@ -5,6 +5,7 @@ import (
 	"time"
 
 	"github.com/DocumentDrivenDX/fizeau/internal/routehealth"
+	"github.com/DocumentDrivenDX/fizeau/internal/routing"
 )
 
 const stickyRouteLeaseTTL = routehealth.DefaultLeaseTTL
@@ -16,6 +17,13 @@ func (s *service) routeLeaseStore() *routehealth.LeaseStore {
 	return s.routeLeases
 }
 
+func (s *service) routeUtilizationStore() *routehealth.UtilizationStore {
+	if s.routeUtilization == nil {
+		s.routeUtilization = routehealth.NewUtilizationStore()
+	}
+	return s.routeUtilization
+}
+
 func (s *service) applyStickyRouteLease(stickyKey string, decision *RouteDecision) {
 	if s == nil || decision == nil || strings.TrimSpace(stickyKey) == "" {
 		return
@@ -56,64 +64,20 @@ func (s *service) applyStickyRouteLease(stickyKey string, decision *RouteDecisio
 		}
 		store.Invalidate(now, key, reason)
 	}
-
-	candidate, found := stickyLeasePick(decision.Candidates, decision.Harness, baseProvider, decision.Model, store.LeaseCounts(now, baseProvider, decision.Model))
-	if !found || !candidate.Eligible {
+	if decision.Provider == "" && decision.Endpoint == "" {
 		return
 	}
-	chosenEndpoint := candidate.Endpoint
+	chosenEndpoint := decision.Endpoint
 	if chosenEndpoint == "" {
-		_, chosenEndpoint, _ = splitEndpointProviderRef(candidate.Provider)
+		_, chosenEndpoint, _ = splitEndpointProviderRef(decision.Provider)
 	}
 	if chosenEndpoint == "" {
-		chosenEndpoint = candidate.Provider
+		chosenEndpoint = decision.Provider
 	}
-	store.Acquire(now, stickyRouteLeaseTTL, key, baseProvider, chosenEndpoint, decision.Model)
-	decision.Provider = candidate.Provider
-	decision.Endpoint = candidate.Endpoint
-}
-
-func stickyLeasePick(candidates []RouteCandidate, harness, provider, model string, counts map[string]int) (RouteCandidate, bool) {
-	var chosen RouteCandidate
-	found := false
-	for _, candidate := range candidates {
-		if !candidate.Eligible || candidate.Harness != harness || candidate.Model != model {
-			continue
-		}
-		baseProvider, _, _ := splitEndpointProviderRef(candidate.Provider)
-		if baseProvider == "" {
-			baseProvider = candidate.Provider
-		}
-		if baseProvider != provider {
-			continue
-		}
-		if candidate.Endpoint == "" && candidate.Provider == "" {
-			continue
-		}
-		if !found {
-			chosen = candidate
-			found = true
-			continue
-		}
-		leftCount := counts[endpointOf(candidate)]
-		rightCount := counts[endpointOf(chosen)]
-		if leftCount != rightCount {
-			if leftCount < rightCount {
-				chosen = candidate
-			}
-			continue
-		}
-		if candidate.Score != chosen.Score {
-			if candidate.Score > chosen.Score {
-				chosen = candidate
-			}
-			continue
-		}
-		if endpointOf(candidate) < endpointOf(chosen) {
-			chosen = candidate
-		}
+	if chosenEndpoint == "" {
+		return
 	}
-	return chosen, found
+	store.Acquire(now, stickyRouteLeaseTTL, key, baseProvider, chosenEndpoint, decision.Model)
 }
 
 func stickyLeaseCandidate(candidates []RouteCandidate, harness, provider, model, endpoint string) (RouteCandidate, bool) {
@@ -160,9 +124,31 @@ func stickyLeaseAnyEndpoint(candidates []RouteCandidate, harness, provider, endp
 	return RouteCandidate{}, false
 }
 
-func endpointOf(candidate RouteCandidate) string {
-	if _, endpoint, ok := splitEndpointProviderRef(candidate.Provider); ok && endpoint != "" {
-		return endpoint
+func (s *service) routeEndpointLoadsResolver(now time.Time) func(provider, endpoint, model string) (routing.EndpointLoad, bool) {
+	if s == nil {
+		return nil
+	}
+	leaseStore := s.routeLeaseStore()
+	utilStore := s.routeUtilizationStore()
+	return func(provider, endpoint, model string) (routing.EndpointLoad, bool) {
+		leaseCounts := leaseStore.LeaseCounts(now, provider, model)
+		loads := utilStore.EndpointLoads(provider, model, leaseCounts)
+		load, ok := loads[endpoint]
+		if !ok {
+			if count, ok := leaseCounts[endpoint]; ok {
+				return routing.EndpointLoad{
+					LeaseCount:       count,
+					NormalizedLoad:   float64(count),
+					UtilizationFresh: false,
+				}, true
+			}
+			return routing.EndpointLoad{}, false
+		}
+		return routing.EndpointLoad{
+			LeaseCount:           load.LeaseCount,
+			NormalizedLoad:       load.NormalizedLoad,
+			UtilizationFresh:     load.UtilizationFresh,
+			UtilizationSaturated: load.UtilizationSaturated,
+		}, true
 	}
-	return candidate.Endpoint
 }
diff --git a/service_route_leases_test.go b/service_route_leases_test.go
index 095f9b8..1706938 100644
--- a/service_route_leases_test.go
+++ b/service_route_leases_test.go
@@ -7,6 +7,7 @@ import (
 	"time"
 
 	"github.com/DocumentDrivenDX/fizeau/internal/harnesses"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
 	"github.com/DocumentDrivenDX/fizeau/internal/routehealth"
 )
 
@@ -111,3 +112,141 @@ func TestResolveRouteStickyLeaseReusesEndpoint(t *testing.T) {
 		t.Fatalf("sticky decision=%#v, want reused desk-b despite reversed baseline", second)
 	}
 }
+
+func TestResolveRouteStickyLeaseDistributesNewKeysByLoad(t *testing.T) {
+	originalProbe := probeOpenAIModelsForDiscovery
+	defer func() { probeOpenAIModelsForDiscovery = originalProbe }()
+	probeOpenAIModelsForDiscovery = func(ctx context.Context, baseURL, apiKey string) ([]string, error) {
+		switch {
+		case strings.Contains(baseURL, "desk-a"):
+			return []string{"qwen/qwen3.6"}, nil
+		case strings.Contains(baseURL, "desk-b"):
+			return []string{"qwen/qwen3.6"}, nil
+		default:
+			return nil, nil
+		}
+	}
+
+	sc := &fakeServiceConfig{
+		providers: map[string]ServiceProviderEntry{
+			"local": {
+				Type: "lmstudio",
+				Endpoints: []ServiceProviderEndpoint{
+					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1"},
+					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1"},
+				},
+				Model: "qwen/qwen3.6",
+			},
+		},
+		names:          []string{"local"},
+		defaultName:    "local",
+		healthCooldown: 20 * time.Millisecond,
+	}
+	svc := &service{
+		opts:        ServiceOptions{ServiceConfig: sc},
+		registry:    harnesses.NewRegistry(),
+		hub:         newSessionHub(),
+		catalog:     newCatalogCache(catalogCacheOptions{}),
+		routeHealth: routehealth.NewStore(),
+		routeLeases: routehealth.NewLeaseStore(),
+	}
+	svc.routeUtilizationStore().Record("local", "desk-a", "qwen/qwen3.6", utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(1),
+		MaxConcurrency: utilization.Int(2),
+		Source:         utilization.SourceLlamaSlots,
+		Freshness:      utilization.FreshnessFresh,
+	})
+	svc.routeUtilizationStore().Record("local", "desk-b", "qwen/qwen3.6", utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(0),
+		MaxConcurrency: utilization.Int(2),
+		Source:         utilization.SourceLlamaSlots,
+		Freshness:      utilization.FreshnessFresh,
+	})
+
+	first, err := svc.ResolveRoute(context.Background(), RouteRequest{
+		Harness:       "agent",
+		Model:         "qwen/qwen3.6",
+		CorrelationID: "bead-new-a",
+	})
+	if err != nil {
+		t.Fatalf("ResolveRoute first: %v", err)
+	}
+	if first.Provider != "local@desk-b" || first.Endpoint != "desk-b" {
+		t.Fatalf("first decision=%#v, want desk-b as the least-loaded endpoint", first)
+	}
+
+	second, err := svc.ResolveRoute(context.Background(), RouteRequest{
+		Harness:       "agent",
+		Model:         "qwen/qwen3.6",
+		CorrelationID: "bead-new-b",
+	})
+	if err != nil {
+		t.Fatalf("ResolveRoute second: %v", err)
+	}
+	if second.Provider != "local@desk-a" || second.Endpoint != "desk-a" {
+		t.Fatalf("second decision=%#v, want desk-a after desk-b lease increased load", second)
+	}
+}
+
+func TestResolveRouteStickyLeaseIgnoresStaleUtilizationFallback(t *testing.T) {
+	originalProbe := probeOpenAIModelsForDiscovery
+	defer func() { probeOpenAIModelsForDiscovery = originalProbe }()
+	probeOpenAIModelsForDiscovery = func(ctx context.Context, baseURL, apiKey string) ([]string, error) {
+		switch {
+		case strings.Contains(baseURL, "desk-a"):
+			return []string{"qwen/qwen3.6"}, nil
+		case strings.Contains(baseURL, "desk-b"):
+			return []string{"qwen/qwen3.6"}, nil
+		default:
+			return nil, nil
+		}
+	}
+
+	sc := &fakeServiceConfig{
+		providers: map[string]ServiceProviderEntry{
+			"local": {
+				Type: "lmstudio",
+				Endpoints: []ServiceProviderEndpoint{
+					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1"},
+					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1"},
+				},
+				Model: "qwen/qwen3.6",
+			},
+		},
+		names:          []string{"local"},
+		defaultName:    "local",
+		healthCooldown: 20 * time.Millisecond,
+	}
+	svc := &service{
+		opts:        ServiceOptions{ServiceConfig: sc},
+		registry:    harnesses.NewRegistry(),
+		hub:         newSessionHub(),
+		catalog:     newCatalogCache(catalogCacheOptions{}),
+		routeHealth: routehealth.NewStore(),
+		routeLeases: routehealth.NewLeaseStore(),
+	}
+	// Stale utilization should be ignored in favor of the in-process lease
+	// counts. Make desk-a look idle in stale telemetry but keep more leases
+	// on desk-a so desk-b should still win.
+	svc.routeUtilizationStore().Record("local", "desk-a", "qwen/qwen3.6", utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(0),
+		MaxConcurrency: utilization.Int(2),
+		Source:         utilization.SourceLlamaSlots,
+		Freshness:      utilization.FreshnessStale,
+	})
+	svc.routeLeases.Acquire(time.Now().UTC(), stickyRouteLeaseTTL, routehealth.NormalizeLeaseKey("seed-a", "local", "qwen/qwen3.6"), "local", "desk-a", "qwen/qwen3.6")
+	svc.routeLeases.Acquire(time.Now().UTC(), stickyRouteLeaseTTL, routehealth.NormalizeLeaseKey("seed-b", "local", "qwen/qwen3.6"), "local", "desk-a", "qwen/qwen3.6")
+	svc.routeLeases.Acquire(time.Now().UTC(), stickyRouteLeaseTTL, routehealth.NormalizeLeaseKey("seed-c", "local", "qwen/qwen3.6"), "local", "desk-b", "qwen/qwen3.6")
+
+	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
+		Harness:       "agent",
+		Model:         "qwen/qwen3.6",
+		CorrelationID: "stale-load-key",
+	})
+	if err != nil {
+		t.Fatalf("ResolveRoute: %v", err)
+	}
+	if dec.Provider != "local@desk-b" || dec.Endpoint != "desk-b" {
+		t.Fatalf("decision=%#v, want desk-b from lease-count fallback", dec)
+	}
+}
diff --git a/service_routing.go b/service_routing.go
index b1990bb..f83ef21 100644
--- a/service_routing.go
+++ b/service_routing.go
@@ -358,6 +358,7 @@ func (s *service) buildRoutingInputsWithCatalog(ctx context.Context, cat *modelc
 	for _, st := range statuses {
 		statusByName[st.Name] = st
 	}
+	now := time.Now().UTC()
 
 	var entries []routing.HarnessEntry
 	for _, name := range s.registry.Names() {
@@ -426,16 +427,17 @@ func (s *service) buildRoutingInputsWithCatalog(ctx context.Context, cat *modelc
 		s.applySubscriptionRoutingCost(&entry, cat)
 		entries = append(entries, entry)
 	}
-	successRate, latencyMS := s.routeMetricSignals(time.Now(), s.routeAttemptTTL())
+	successRate, latencyMS := s.routeMetricSignals(now, s.routeAttemptTTL())
 	return routing.Inputs{
 		Harnesses:                   entries,
 		ProviderSuccessRate:         successRate,
 		ObservedLatencyMS:           latencyMS,
-		ProviderQuotaExhaustedUntil: s.providerQuotaExhaustedUntil(time.Now()),
+		ProviderQuotaExhaustedUntil: s.providerQuotaExhaustedUntil(now),
 		CatalogResolver:             serviceRoutingCatalogResolver(cat),
 		CatalogCandidatesResolver:   serviceRoutingCatalogCandidatesResolver(cat),
 		ModelEligibility:            serviceRoutingModelEligibility(cat),
 		ReasoningResolver:           serviceRoutingReasoningResolver(cat),
+		EndpointLoadResolver:        s.routeEndpointLoadsResolver(now),
 	}
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
