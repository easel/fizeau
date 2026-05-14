<bead-review>
  <bead id="fizeau-52a7e4db" iter=1>
    <title>Add Rapid-MLX provider type</title>
    <description>
Add a first-class Rapid-MLX provider type. Rapid-MLX is an OpenAI-compatible Apple Silicon server from https://github.com/raullenchai/Rapid-MLX with default serving URL http://localhost:8000/v1, but it should have a concrete provider type distinct from vllm because utilization is exposed through /v1/status rather than root /metrics.

In-scope files:
- internal/provider/rapidmlx/ (new package wrapping the shared OpenAI-compatible provider)
- internal/config/config.go and config tests
- internal/provider/registry tests
- internal/harnesses registry/capability lists if provider types are enumerated there
- docs/helix/01-frame/features/FEAT-003-providers.md if acceptance criteria need the new provider type listed

Requirements:
- Accept config `type: rapid-mlx` and, if local naming conventions prefer no hyphen in package names, map it to package path internal/provider/rapidmlx.
- Default base URL is http://localhost:8000/v1.
- Registry construction, endpoint-pool support, provider/model listing, native provider dispatch, ProviderType telemetry labels, and capability lookup work the same way as other OpenAI-compatible concrete providers.
- Do not collapse Rapid-MLX into vllm or generic openai; provider type must be distinct because utilization probing differs.

Out of scope:
- Implementing /v1/status utilization parsing.
- Adding real-server cassettes.
- Changing routing score policy.
    </description>
    <acceptance>
1. `go test ./internal/provider/registry ./internal/config ./internal/provider/rapidmlx ./... -run 'Registry|ProviderType|Rapid|rapid-mlx'` passes.
2. A config entry with `type: rapid-mlx` and no base_url constructs a provider with default base URL `http://localhost:8000/v1`.
3. Provider/model listing and native provider dispatch preserve `ProviderType == "rapid-mlx"` or the documented canonical spelling chosen in code, without emitting `openai-compat`.
4. `rg -n "rapid-mlx|rapidmlx" internal docs/helix/01-frame/features/FEAT-003-providers.md` shows the provider type is documented where provider types are enumerated.
5. No utilization probe, cassette recorder, or route scoring logic is implemented in this bead.
    </acceptance>
    <labels>area:provider, kind:feature</labels>
  </bead>

  <changed-files>
    <file>docs/helix/01-frame/features/FEAT-003-providers.md</file>
    <file>internal/config/config.go</file>
    <file>internal/config/config_test.go</file>
    <file>internal/provider/rapidmlx/rapidmlx.go</file>
    <file>internal/provider/rapidmlx/rapidmlx_test.go</file>
    <file>internal/provider/registry/registry_test.go</file>
    <file>service_models.go</file>
    <file>service_native_provider.go</file>
    <file>service_providers.go</file>
    <file>service_routing.go</file>
  </changed-files>

  <governing>
    <ref id="FEAT-003" path="benchmark-results/beadbench/run-20260423T021643Z/helix-build-selector-readiness__codex-gpt54__r1/verify-worktree/docs/helix/01-frame/features/FEAT-003-principles.md" title="Feature Specification: FEAT-003 - First-Class Principles">
      <content>
<untrusted-data>
---
dun:
  id: FEAT-003
  depends_on:
    - helix.prd
---
# Feature Specification: FEAT-003 - First-Class Principles

**Feature ID**: FEAT-003
**Status**: Draft
**Priority**: P1
**Owner**: HELIX maintainers

## Overview

Principles are cross-cutting design concerns that guide decision-making across
all HELIX phases. They are not workflow rules or process enforcement — they are
lenses applied when choosing between two valid options.

Today, principles exist as a gate artifact: produce the document, check the
box, move on. Nothing downstream reads them. The six "principles" in
`workflows/principles.md` are actually workflow rules (test-first, spec
completeness) that belong in enforcers and ratchets.

This feature makes principles a live, injectable artifact that shapes every
downstream judgment — from architecture decisions to implementation trade-offs
to review criteria.

## Problem Statement

- **Current situation**: `workflows/principles.md` contains workflow rules
  mislabeled as principles. The per-project artifact scaffolding exists
  (meta.yml, template.md, prompt.md) but no project has ever generated a
  concrete principles document. No skill or action prompt reads principles.
- **Pain points**: Agents make judgment calls (design trade-offs, abstraction
  boundaries, error handling strategies) without reference to what the project
  values. Each skill re-derives its own implicit principles from context,
  producing inconsistent guidance across phases.
- **Desired outcome**: A small, project-owned set of design principles that
  HELIX injects into every skill and action that makes judgment calls. Agents
  apply the same values whether they are framing requirements, designing
  architecture, implementing code, or reviewing work.

## Design

### Two-layer model

**Layer 1 — HELIX defaults** (`workflows/principles.md`): A small set (~5) of
non-controversial design principles that consistently produce good results.
These are not methodology opinions — they are things virtually every
well-run project agrees on.

Example defaults (illustrative, not final):

1. **Design for change** — Prefer structures that are easy to modify over
   structures that are easy to describe today.
2. **Design for simplicity** — Start with the minimal viable approach.
   Additional complexity requires justification.
3. **Validate your work** — Every change should be verified through the most
   appropriate means available (tests, type checks, manual verification).
4. **Make intent explicit** — Code, configuration, and documentation should
   make the *why* visible, not just the *what*.
5. **Prefer reversible decisions** — When uncertain, choose the option that
   is easiest to undo or change later.

**Layer 2 — Project principles** (`docs/helix/01-frame/principles.md`): The
project's own principles. Users can add, modify, reorder, or remove any
principle, including HELIX defaults. The only constraint: principles cannot
negate HELIX mechanics (artifact hierarchy, phase gates, tracker semantics).

### Bootstrap and precedence

1. If `docs/helix/01-frame/principles.md` exists, it is the active principles
   document. HELIX defaults are ignored entirely.
2. If it does not exist, HELIX defaults from `workflows/principles.md` are
   used as the active principles.
3. On first `helix frame` (or when the user explicitly asks to initialize
   principles), HELIX copies the defaults into the project location and
   invites the user to customize. From that point, the project owns the file.
4. The bootstrap prompt should ask the user what their project values, what
   trade-offs they lean toward, and what past mistakes they want to avoid —
   then synthesize project-specific principles alongside the defaults.

### Downstream injection

Every skill and action prompt that makes a judgment call must load the active
principles and include them as context. Specifically:

| Consumer | How principles apply |
|----------|---------------------|
| `helix frame` | Principles shape requirements priorities and feature scoping |
| `helix design` | Principles inform architecture decisions, ADR trade-offs, solution design choices |
| `helix build` / implementation | Principles guide coding trade-offs (abstraction level, error handling, API surface) |
| `helix review` | Principles become review criteria — reviewer checks whether the work aligns with stated values |
| `helix align` | Principles are part of the alignment audit — do artifacts and implementation reflect the project's stated values? |
| `helix evolve` | When threading a change through the stack, principles help decide scope and approach |
| `helix polish` | Issue refinement checks whether acceptance criteria reflect principles |

The injection mechanism is selective: each skill includes the principles most
relevant to its judgment domain, not a full dump of the document. The right
injection strategy is an open research question — what phrasing, selection,
and positioning in the prompt actually changes agent behavior?

**Prompt engineering research** (tracked separately) will use DDx agent
execution, logging, and metrics to measure whether principles injection
produces measurably better alignment with stated project values. This
research should iterate on:

- Which principles matter for which skill types
- Whether full-document vs. selected-subset injection performs better
- Where in the prompt principles have the most effect (preamble, inline, 
  closing constraint)
- Whether principles need rephrasing per skill context or work verbatim

Until research produces evidence, the initial implementation uses a simple
preamble with the full principles document:

```
## Active Principles
{contents of the active principles document}

Apply these principles when making judgment calls in this task.
When two options are both valid, prefer the one that better aligns
with the principles above.
```

This is the baseline to measure against, not the final design.

### Principle management

A principle management capability (within `helix frame` or as a dedicated
sub-action) handles:

1. **Add a principle** — user states a new principle; the system checks for
   conflicts with existing principles and either adds it cleanly or flags
   the tension.
2. **Tension detection** — when principles pull in opposite directions (e.g.,
   "design for simplicity" vs. "design for extensibility"), the system
   requires a resolution strategy documented in the principles file. This
   could be a priority ordering, a scoping rule ("simplicity wins for
   internal tools, extensibility wins for public APIs"), or an explicit
   acknowledgment of the tension with guidance.
3. **Review principles** — triggered when the principles document changes
   (tracked via the DDx document graph). The DDx document graph should track
   `principles.md` as a dependency of downstream artifacts; when principles
   change, dependents are marked stale for re-review. If the DDx document
   graph lacks features needed for this dependency tracking, open beads on
   the DDx repo to evolve the capability there.
4. **Remove / modify** — straightforward editing with a coherence check
   afterward.

### Relationship with `helix evolve`

`helix evolve` threads changes through the artifact stack. When evolving,
it must:

- **Read and respect** the active principles — use them as guidance when
  deciding how to thread the change.
- **Never modify** the principles document as a side effect. Principles are
  upstream authority; evolve operates downstream of them.
- If an evolve operation reveals that a principle is now misaligned with
  project direction, evolve should flag this for the user rather than
  silently editing the principles file.

### Tension resolution format

When principles can conflict, the principles document should include a
resolution section:

```markdown
## Tension Resolution

- **Simplicity vs. Extensibility**: For internal components, prefer
  simplicity. For public interfaces, prefer extensibility. When unclear,
  prefer simplicity and refactor when the extension point proves necessary.
```

This section is not required when no tensions exist, but the management
skill should proactively identify tensions and prompt the user to resolve
them.

## Requirements

### Functional Requirements

1. HELIX must ship a small set of default design principles in
   `workflows/principles.md` that are genuine design guidance, not workflow
   rules.
2. The current workflow rules in `workflows/principles.md` must be relocated
   to the appropriate enforcers and ratchets.
3. When no project principles exist, HELIX must use the defaults as the
   active principles for all downstream injection.
4. `helix frame` must bootstrap project principles from HELIX defaults when
   no project principles file exists, prompting the user to customize.
5. Once project principles exist, they take full precedence over HELIX
   defaults.
6. Every HELIX skill and action that makes judgment calls must load and
   apply the active principles.
7. The principles artifact scaffolding (meta.yml, template.md, prompt.md)
   must be updated to reflect the new design.
8. Principle management must detect and flag tensions between principles.
9. The principles document must include a tension resolution section when
   conflicting principles exist.

### Non-Functional Requirements

- **Consistency**: The same principles must be applied across all phases —
  no skill should derive its own implicit principles.
- **Maintainability**: Adding a new skill to HELIX should make it obvious
  that principles injection is expected.
- **Simplicity**: The injection mechanism should be simple enough that it
  does not become a maintenance burden itself.

## User Stories

### US-001: Bootstrap project principles [FEAT-003]
**As a** HELIX operator starting a new project
**I want** HELIX to initialize a principles document from sensible defaults
**So that** I have a starting point that I can customize for my project

**Acceptance Criteria:**
- [ ] Given no `docs/helix/01-frame/principles.md`, when `helix frame` runs,
  then HELIX creates the file from defaults and prompts for customization.
- [ ] Given the bootstrap runs, when it completes, then the resulting document
  includes both HELIX defaults and any user-specified principles.
- [ ] Given the user removes a HELIX default during bootstrap, then it stays
  removed — HELIX does not re-add it.

### US-002: Principles guide downstream work [FEAT-003]
**As a** HELIX operator
**I want** my project's principles to be applied when HELIX generates designs,
implementations, and reviews
**So that** the work reflects my project's values consistently

**Acceptance Criteria:**
- [ ] Given active principles exist, when any judgment-making skill runs, then
  the skill prompt includes the active principles as context.
- [ ] Given a principle like "design for simplicity", when `helix design`
  generates an architecture, then it demonstrably favors simpler options.
- [ ] Given a principle like "validate your work", when `helix review` runs,
  then it checks whether the implementation includes appropriate validation.

### US-003: Manage principles coherently [FEAT-003]
**As a** HELIX operator
**I want** to add, modify, and remove principles with automatic tension
detection
**So that** my principles document stays internally consistent

**Acceptance Criteria:**
- [ ] Given the user adds a principle that tensions with an existing one, when
  the management skill runs, then it flags the tension and asks for a
  resolution strategy.
- [ ] Given a principles document with unresolved tensions, when any downstream
  skill loads it, then the tension resolution section is included in the
  injection.
- [ ] Given the user removes a principle, when the management skill runs, then
  it checks whether the tension resolution section needs updating.

### US-004: Fall back to HELIX defaults [FEAT-003]
**As a** HELIX operator who has not customized principles
**I want** HELIX to apply sensible defaults rather than operating with no
principles at all
**So that** I get consistently reasonable results out of the box

**Acceptance Criteria:**
- [ ] Given no project principles file exists, when a downstream skill runs,
  then it loads and applies HELIX defaults from `workflows/principles.md`.
- [ ] Given HELIX defaults are active, when they are injected into a skill,
  then the skill applies them identically to how it would apply project
  principles.

## Edge Cases and Error Handling

- **Empty principles file**: If the user creates `docs/helix/01-frame/principles.md`
  but leaves it empty, treat it as "no active principles" and fall back to
  defaults. Warn the user.
- **Principles that negate HELIX mechanics**: If a principle says "never write
  tests" or "ignore the artifact hierarchy", the management skill should warn
  that this may break HELIX but not hard-block it. The user owns the file.
- **Very large principles documents**: The management skill warns at 8
  principles ("consider whether all of these are decision-changing"), nudges
  consolidation at 12 ("the Agile Manifesto has 12 and most teams can only
  name 4-5"), and strongly recommends pruning at 15+. Above 12, the
  document has likely become a wish list rather than a decision framework.
  Injection adds to every prompt, so size has direct cost.

## Success Metrics

- Every judgment-making skill includes active principles in its prompt.
- Projects that customize principles produce work that demonstrably reflects
  those principles (verifiable through review).
- Principle tensions are caught and resolved before they produce inconsistent
  downstream artifacts.

## Constraints and Assumptions

- The injection mechanism must work with the existing skill/action prompt
  structure — no new runtime infrastructure.
- Principles are a static document, not a database — they are read at skill
  invocation time, not queried dynamically.
- The HELIX defaults should be stable and change rarely. They are the
  "obviously correct" baseline, not a living methodology document.

## Dependencies

- **FEAT-001**: Supervisory control (principles injection into the run loop)
- **helix.prd**: Principles feature is governed by the PRD
- **Workflow contract**: Enforcers and ratchets must absorb the relocated
  workflow rules from the current `workflows/principles.md`

## Out of Scope

- Per-phase principles (e.g., "build-phase principles" distinct from
  "design-phase principles") — principles are cross-cutting by definition.
- Automated principle enforcement in CI — principles guide judgment, they
  are not linting rules.
- Principle versioning or history beyond what git provides.

## Research Dependencies

- **Prompt engineering for principles injection**: What injection strategy
  (full doc vs. selective, preamble vs. inline, verbatim vs. rephrased)
  actually changes agent behavior? Use DDx agent execution, logging, and
  metrics to measure. This should be tracked as a research bead and iterated
  on across the existing HELIX skills.

## Open Questions

- What DDx document graph features are needed to track principles as an
  upstream dependency of downstream artifacts? Does this require new DDx
  beads?
- Should principle changes trigger automatic re-review of all dependent
  artifacts, or only flag them as stale for the next `helix align`?
</untrusted-data>
      </content>
    </ref>
  </governing>

  <diff rev="4e556672911f623c245ac6c1b05a75961b0125ce">
<untrusted-data>
diff --git a/docs/helix/01-frame/features/FEAT-003-providers.md b/docs/helix/01-frame/features/FEAT-003-providers.md
index 55ba589..6ae142a 100644
--- a/docs/helix/01-frame/features/FEAT-003-providers.md
+++ b/docs/helix/01-frame/features/FEAT-003-providers.md
@@ -61,7 +61,7 @@ concern and must not be used as the routing key or analytics label.
 #### Concrete OpenAI-Compatible Providers
 
 6. Each concrete provider (`lmstudio`, `omlx`, `ollama`, `vllm`,
-   `llama-server`, `openai`, `openrouter`) wraps a shared
+   `rapid-mlx`, `llama-server`, `openai`, `openrouter`) wraps a shared
    `internal/sdk/openaicompat` layer that owns:
    Chat Completions request shaping, tool schema serialization, streaming chunk
    parsing, tool-call delta accumulation, debug wire capture, request timeouts,
@@ -78,6 +78,7 @@ concern and must not be used as the routing key or analytics label.
    - `ollama`: local defaults, Ollama capability claims.
    - `vllm`: local or self-hosted vLLM defaults, OpenAI-compatible serving
      behavior, and `/metrics` utilization discovery.
+   - `rapid-mlx`: Rapid-MLX defaults and OpenAI-compatible serving behavior.
    - `llama-server`: llama.cpp `llama-server` defaults, OpenAI-compatible
      serving behavior, `/metrics` utilization discovery when started with
      `--metrics`, and `/slots` utilization fallback when slots are enabled.
@@ -110,7 +111,8 @@ providers:
 ```
 
 10. Supported provider `type` values: `openai`, `openrouter`, `lmstudio`,
-    `omlx`, `vllm`, `llama-server`, `ollama`, `anthropic`, `virtual`.
+    `omlx`, `vllm`, `rapid-mlx`, `llama-server`, `ollama`, `anthropic`,
+    `virtual`.
     `type: openai-compat` is rejected at config load. URL inference maps
     well-known hosts/ports to concrete types at config load only.
 11. For cloud providers, `base_url` is a shorthand for a single endpoint when
@@ -275,7 +277,7 @@ type AttemptMetadata struct {
     (`<base_url>/models`, typically `/v1/models`).
 27. `ModelInfo` results for provider-backed models include the configured
     provider name, concrete provider type (`openrouter`, `lmstudio`, `vllm`,
-    `llama-server`, or `omlx`), endpoint name, endpoint base URL, model ID,
+    `rapid-mlx`, `llama-server`, or `omlx`), endpoint name, endpoint base URL, model ID,
     availability, ranking, context/cost/catalog metadata when known, and
     route/default markers.
 28. Endpoint-pool behavior is additive and deterministic: a reachable endpoint
@@ -294,7 +296,7 @@ type AttemptMetadata struct {
     - `SupportsStructuredOutput() bool` — honors `response_format: json_object`
       or equivalent JSON-mode / tool-use-required semantics
 30. Capability flags are type-keyed (`lmstudio` / `omlx` / `vllm` /
-    `llama-server` / `openrouter` / `ollama` / `openai`). Unknown types return
+    `rapid-mlx` / `llama-server` / `openrouter` / `ollama` / `openai`). Unknown types return
     `false` conservatively so routing rejects rather than dispatches-and-fails.
 31. Protocol capability is distinct from routing capability (the benchmark-
     quality score used by smart-routing scoring). These axes do not interact.
diff --git a/internal/config/config.go b/internal/config/config.go
index 56bc827..48f8565 100644
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -23,6 +23,7 @@ import (
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/openrouter"
 	provregistry "github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/rapidmlx"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/vllm"
 	"github.com/DocumentDrivenDX/fizeau/internal/reasoning"
 	"github.com/DocumentDrivenDX/fizeau/internal/safefs"
@@ -33,7 +34,7 @@ import (
 
 // ProviderConfig describes a single named provider.
 type ProviderConfig struct {
-	Type      string             `yaml:"type"`               // "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "ollama", or "anthropic"
+	Type      string             `yaml:"type"`               // "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", or "anthropic"
 	BaseURL   string             `yaml:"base_url,omitempty"` // shorthand for one endpoint
 	Endpoints []ProviderEndpoint `yaml:"endpoints,omitempty"`
 	APIKey    string             `yaml:"api_key,omitempty"`
@@ -1101,6 +1102,10 @@ func normalizeProviderConfig(pc ProviderConfig) ProviderConfig {
 		if pc.BaseURL == "" {
 			pc.BaseURL = vllm.DefaultBaseURL
 		}
+	case "rapid-mlx":
+		if pc.BaseURL == "" {
+			pc.BaseURL = rapidmlx.DefaultBaseURL
+		}
 	case "ollama":
 		if pc.BaseURL == "" {
 			pc.BaseURL = ollama.DefaultBaseURL
@@ -1146,7 +1151,7 @@ func inferProviderTypeFromBaseURL(baseURL string) string {
 
 func providerUsesEndpoint(providerType string) bool {
 	switch providerType {
-	case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "ollama", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
 		return true
 	default:
 		return false
@@ -1185,7 +1190,7 @@ func ProviderImplicitGenerationConfig(providerType string) bool {
 
 func surfaceForProviderType(providerType string) (modelcatalog.Surface, error) {
 	switch providerType {
-	case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "ollama", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
 		return modelcatalog.SurfaceAgentOpenAI, nil
 	case "anthropic":
 		return modelcatalog.SurfaceAgentAnthropic, nil
@@ -1240,12 +1245,12 @@ func (c *Config) validateProviders() error {
 	}
 	for name, pc := range c.Providers {
 		if pc.Type == "openai-compat" {
-			return fmt.Errorf("config: provider %q: type openai-compat is no longer supported; use openai, openrouter, lmstudio, omlx, or ollama", name)
+			return fmt.Errorf("config: provider %q: type openai-compat is no longer supported; use openai, openrouter, lmstudio, omlx, rapid-mlx, or ollama", name)
 		}
 		switch pc.Type {
-		case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "ollama", "minimax", "qwen", "zai", "anthropic":
+		case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai", "anthropic":
 		default:
-			return fmt.Errorf("config: provider %q has unknown type %q (use openai, openrouter, lmstudio, omlx, luce, vllm, ollama, or anthropic)", name, pc.Type)
+			return fmt.Errorf("config: provider %q has unknown type %q (use openai, openrouter, lmstudio, omlx, luce, vllm, rapid-mlx, ollama, or anthropic)", name, pc.Type)
 		}
 		if providerUsesEndpoint(pc.Type) {
 			for i, endpoint := range pc.Endpoints {
diff --git a/internal/config/config_test.go b/internal/config/config_test.go
index 7099f73..009a67e 100644
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -343,6 +343,7 @@ func TestBuildProvider_ConcreteProviderTypes(t *testing.T) {
 			"lmstudio":   {Type: "lmstudio", BaseURL: "http://localhost:1234/v1", Model: "qwen3"},
 			"omlx":       {Type: "omlx", BaseURL: "http://localhost:1235/v1", Model: "qwen3"},
 			"ollama":     {Type: "ollama", BaseURL: "http://localhost:11434/v1", Model: "llama3.2"},
+			"rapidmlx":   {Type: "rapid-mlx", Model: "qwen3"},
 			"anthropic":  {Type: "anthropic", APIKey: "sk-ant-test", Model: "claude-sonnet-4-20250514"},
 		},
 	}
@@ -375,6 +376,31 @@ func TestBuildProvider_WithHeaders(t *testing.T) {
 	assert.NotNil(t, p)
 }
 
+func TestBuildProvider_RapidMLXDefaultBaseURL(t *testing.T) {
+	cfg := Config{
+		Providers: map[string]ProviderConfig{
+			"rapidmlx": {
+				Type:  "rapid-mlx",
+				Model: "qwen3",
+			},
+		},
+	}
+
+	p, err := cfg.BuildProvider("rapidmlx")
+	require.NoError(t, err)
+	require.NotNil(t, p)
+
+	metadata, ok := p.(interface {
+		ChatStartMetadata() (string, string, int)
+	})
+	require.True(t, ok, "provider must expose chat start metadata")
+
+	system, host, port := metadata.ChatStartMetadata()
+	assert.Equal(t, "rapid-mlx", system)
+	assert.Equal(t, "localhost", host)
+	assert.Equal(t, 8000, port)
+}
+
 func TestResolveProviderConfig_ModelRefOpenAI(t *testing.T) {
 	isolateHome(t)
 	cfg := Config{
@@ -1131,6 +1157,28 @@ default: studio
 	assert.Equal(t, "lmstudio", studio.Type)
 }
 
+func TestLoad_RapidMLXDefaultBaseURL(t *testing.T) {
+	isolateHome(t)
+	dir := t.TempDir()
+	cfgDir := filepath.Join(dir, ".fizeau")
+	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
+
+	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`
+providers:
+  rapid:
+    type: rapid-mlx
+    model: qwen3
+default: rapid
+`), 0o644))
+
+	cfg, err := Load(dir)
+	require.NoError(t, err)
+	pc, ok := cfg.GetProvider("rapid")
+	require.True(t, ok)
+	assert.Equal(t, "rapid-mlx", pc.Type)
+	assert.Equal(t, "http://localhost:8000/v1", pc.BaseURL)
+}
+
 func TestLoad_MissingTypeUnknownBaseURLRejected(t *testing.T) {
 	isolateHome(t)
 	dir := t.TempDir()
diff --git a/internal/provider/rapidmlx/rapidmlx.go b/internal/provider/rapidmlx/rapidmlx.go
new file mode 100644
index 0000000..30f8149
--- /dev/null
+++ b/internal/provider/rapidmlx/rapidmlx.go
@@ -0,0 +1,67 @@
+// Package rapidmlx wraps the OpenAI-compatible HTTP surface exposed by
+// Rapid-MLX (https://github.com/raullenchai/Rapid-MLX). It is a concrete
+// provider type distinct from vLLM so the service layer can keep provider
+// identity separate from utilization probing, which Rapid-MLX exposes on a
+// different observability endpoint family.
+package rapidmlx
+
+import (
+	agentcore "github.com/DocumentDrivenDX/fizeau/internal/core"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
+	"github.com/DocumentDrivenDX/fizeau/internal/reasoning"
+)
+
+const DefaultBaseURL = "http://localhost:8000/v1"
+
+func init() {
+	registry.Register(registry.Descriptor{
+		Type: "rapid-mlx",
+		Factory: func(in registry.Inputs) agentcore.Provider {
+			return New(Config{
+				BaseURL:      in.BaseURL,
+				APIKey:       in.APIKey,
+				Model:        in.Model,
+				ModelPattern: in.ModelPattern,
+				KnownModels:  in.KnownModels,
+				Headers:      in.Headers,
+				Reasoning:    in.Reasoning,
+			})
+		},
+		DefaultBaseURL: DefaultBaseURL,
+		DefaultPort:    8000,
+	})
+}
+
+// ProtocolCapabilities keeps Rapid-MLX on the standard OpenAI-compatible
+// surface. The provider remains distinct from vLLM at the type level.
+var ProtocolCapabilities = openai.OpenAIProtocolCapabilities
+
+type Config struct {
+	BaseURL      string
+	APIKey       string
+	Model        string
+	ModelPattern string
+	KnownModels  map[string]string
+	Headers      map[string]string
+	Reasoning    reasoning.Reasoning
+}
+
+func New(cfg Config) *openai.Provider {
+	baseURL := cfg.BaseURL
+	if baseURL == "" {
+		baseURL = DefaultBaseURL
+	}
+	return openai.New(openai.Config{
+		BaseURL:        baseURL,
+		APIKey:         cfg.APIKey,
+		Model:          cfg.Model,
+		ProviderName:   "rapid-mlx",
+		ProviderSystem: "rapid-mlx",
+		ModelPattern:   cfg.ModelPattern,
+		KnownModels:    cfg.KnownModels,
+		Headers:        cfg.Headers,
+		Reasoning:      cfg.Reasoning,
+		Capabilities:   &ProtocolCapabilities,
+	})
+}
diff --git a/internal/provider/rapidmlx/rapidmlx_test.go b/internal/provider/rapidmlx/rapidmlx_test.go
new file mode 100644
index 0000000..aad4e67
--- /dev/null
+++ b/internal/provider/rapidmlx/rapidmlx_test.go
@@ -0,0 +1,31 @@
+package rapidmlx
+
+import (
+	"testing"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
+	"github.com/stretchr/testify/assert"
+	"github.com/stretchr/testify/require"
+)
+
+func TestRapidMLX_DefaultBaseURLAndIdentity(t *testing.T) {
+	p := New(Config{Model: "qwen3"})
+	require.NotNil(t, p)
+
+	sessionProvider, model := p.SessionStartMetadata()
+	assert.Equal(t, "rapid-mlx", sessionProvider)
+	assert.Equal(t, "qwen3", model)
+
+	system, host, port := p.ChatStartMetadata()
+	assert.Equal(t, "rapid-mlx", system)
+	assert.Equal(t, "localhost", host)
+	assert.Equal(t, 8000, port)
+}
+
+func TestRapidMLX_RegistryRegistration(t *testing.T) {
+	d, ok := registry.Lookup("rapid-mlx")
+	require.True(t, ok)
+	require.NotNil(t, d.Factory)
+	assert.Equal(t, DefaultBaseURL, d.DefaultBaseURL)
+	assert.Equal(t, 8000, d.DefaultPort)
+}
diff --git a/internal/provider/registry/registry_test.go b/internal/provider/registry/registry_test.go
index 0f2046f..1f9e232 100644
--- a/internal/provider/registry/registry_test.go
+++ b/internal/provider/registry/registry_test.go
@@ -15,6 +15,7 @@ import (
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openrouter"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
+	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/rapidmlx"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/vllm"
 
 	"github.com/stretchr/testify/assert"
@@ -38,6 +39,7 @@ var expectedTypes = []string{
 	"omlx",
 	"openai",
 	"openrouter",
+	"rapid-mlx",
 	"qwen",
 	"vllm",
 	"zai",
diff --git a/service_models.go b/service_models.go
index c55e707..08e5a5e 100644
--- a/service_models.go
+++ b/service_models.go
@@ -314,7 +314,7 @@ type discoveredModelSet struct {
 // ranks results against the catalog. IDs preserve discovery order per endpoint.
 func discoverAndRankModels(ctx context.Context, entry ServiceProviderEntry, cat *modelcatalog.Catalog) []discoveredModelSet {
 	switch normalizeServiceProviderType(entry.Type) {
-	case "openai", "openrouter", "lmstudio", "omlx", "ollama", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "omlx", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
 		endpoints := modelDiscoveryEndpoints(entry)
 		if len(endpoints) == 0 {
 			return nil
diff --git a/service_native_provider.go b/service_native_provider.go
index 97d7472..bbda3a1 100644
--- a/service_native_provider.go
+++ b/service_native_provider.go
@@ -21,6 +21,7 @@ import (
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openrouter"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
+	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/rapidmlx"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/vllm"
 )
 
diff --git a/service_providers.go b/service_providers.go
index 4f670c9..307386c 100644
--- a/service_providers.go
+++ b/service_providers.go
@@ -383,7 +383,7 @@ func probeServiceProviderDetailed(ctx context.Context, entry ServiceProviderEntr
 		// Treat key presence as the connectivity signal.
 		return providerProbeResult{status: "connected", caps: providerCapabilities(entry)}
 
-	case "openai", "openrouter", "lmstudio", "omlx", "ollama", "minimax", "qwen", "zai", "":
+	case "openai", "openrouter", "lmstudio", "omlx", "rapid-mlx", "ollama", "minimax", "qwen", "zai", "":
 		if entry.BaseURL == "" {
 			return providerProbeResult{status: "error: base_url not configured", detail: "base_url not configured"}
 		}
diff --git a/service_routing.go b/service_routing.go
index f5c431a..06f97b3 100644
--- a/service_routing.go
+++ b/service_routing.go
@@ -742,7 +742,7 @@ func anyProviderSupportsTools(providers []routing.ProviderEntry) bool {
 
 func providerUsesLiveDiscovery(providerType string) bool {
 	switch normalizeServiceProviderType(providerType) {
-	case "openai", "openrouter", "lmstudio", "omlx", "ollama", "lucebox", "vllm", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm", "minimax", "qwen", "zai":
 		return true
 	default:
 		return false
@@ -792,7 +792,7 @@ func (s *service) applySubscriptionRoutingCost(entry *routing.HarnessEntry, cat
 
 func providerTypeIsLocalEndpoint(providerType string) bool {
 	switch normalizeServiceProviderType(providerType) {
-	case "lmstudio", "omlx", "ollama", "lucebox", "vllm":
+	case "lmstudio", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm":
 		return true
 	default:
 		return false
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
