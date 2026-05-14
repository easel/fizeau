<bead-review>
  <bead id="fizeau-ed832d22" iter=1>
    <title>Add llama-server provider type</title>
    <description>
Add first-class provider type llama-server for llama.cpp's OpenAI-compatible server. In-scope files: internal/provider/llamaserver/*, internal/config/config.go and config tests, provider registry tests, service provider/model listing tests if they assert known types. The provider should wrap the shared OpenAI-compatible provider path like vllm/lmstudio/omlx, default to http://localhost:8080/v1, use default port 8080 for host-based endpoint expansion, support endpoint pools, and construct through the registry in both config-time and service-time native provider paths. Out of scope: utilization probe implementation, VCR cassette infrastructure, sticky routing, and shared lease backend.
    </description>
    <acceptance>
1. go test ./internal/provider/registry ./internal/config ./... -run 'Registry|ProviderType|llama|NormalizeServiceProviderType' passes. 2. registry.Types includes llama-server and its factory returns a non-nil provider. 3. Loading config with type: llama-server and no base_url resolves default http://localhost:8080/v1 and creates a default endpoint. 4. Host/endpoint expansion for type: llama-server uses port 8080. 5. No existing provider type behavior changes.
    </acceptance>
    <labels>area:provider, kind:feature</labels>
  </bead>

  <changed-files>
    <file>internal/config/config.go</file>
    <file>internal/config/config_test.go</file>
    <file>internal/provider/llamaserver/llamaserver.go</file>
    <file>internal/provider/registry/registry_test.go</file>
    <file>service_models.go</file>
    <file>service_models_test.go</file>
    <file>service_native_provider.go</file>
    <file>service_providers.go</file>
    <file>service_providers_test.go</file>
    <file>service_routing.go</file>
    <file>service_routing_test.go</file>
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

  <diff rev="46e249b71ddd93508ba079f8b0715a27414f0640">
<untrusted-data>
diff --git a/internal/config/config.go b/internal/config/config.go
index 48f8565..72b1411 100644
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -16,14 +16,15 @@ import (
 	"github.com/DocumentDrivenDX/fizeau/internal/modelcatalog"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/anthropic"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/limits"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/llamaserver"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/lmstudio"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/lucebox"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/ollama"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/omlx"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/openrouter"
-	provregistry "github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/rapidmlx"
+	provregistry "github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
 	"github.com/DocumentDrivenDX/fizeau/internal/provider/vllm"
 	"github.com/DocumentDrivenDX/fizeau/internal/reasoning"
 	"github.com/DocumentDrivenDX/fizeau/internal/safefs"
@@ -34,7 +35,7 @@ import (
 
 // ProviderConfig describes a single named provider.
 type ProviderConfig struct {
-	Type      string             `yaml:"type"`               // "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", or "anthropic"
+	Type      string             `yaml:"type"`               // "openai", "openrouter", "lmstudio", "llama-server", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", or "anthropic"
 	BaseURL   string             `yaml:"base_url,omitempty"` // shorthand for one endpoint
 	Endpoints []ProviderEndpoint `yaml:"endpoints,omitempty"`
 	APIKey    string             `yaml:"api_key,omitempty"`
@@ -1024,6 +1025,8 @@ func defaultEndpointPort(providerType string) int {
 	switch providerType {
 	case "lmstudio":
 		return 1234
+	case "llama-server":
+		return 8080
 	case "omlx":
 		return 1235
 	case "lucebox":
@@ -1090,6 +1093,10 @@ func normalizeProviderConfig(pc ProviderConfig) ProviderConfig {
 		if pc.BaseURL == "" {
 			pc.BaseURL = lmstudio.DefaultBaseURL
 		}
+	case "llama-server":
+		if pc.BaseURL == "" {
+			pc.BaseURL = llamaserver.DefaultBaseURL
+		}
 	case "omlx":
 		if pc.BaseURL == "" {
 			pc.BaseURL = omlx.DefaultBaseURL
@@ -1151,7 +1158,7 @@ func inferProviderTypeFromBaseURL(baseURL string) string {
 
 func providerUsesEndpoint(providerType string) bool {
 	switch providerType {
-	case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "llama-server", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
 		return true
 	default:
 		return false
@@ -1190,7 +1197,7 @@ func ProviderImplicitGenerationConfig(providerType string) bool {
 
 func surfaceForProviderType(providerType string) (modelcatalog.Surface, error) {
 	switch providerType {
-	case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "llama-server", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
 		return modelcatalog.SurfaceAgentOpenAI, nil
 	case "anthropic":
 		return modelcatalog.SurfaceAgentAnthropic, nil
@@ -1245,12 +1252,12 @@ func (c *Config) validateProviders() error {
 	}
 	for name, pc := range c.Providers {
 		if pc.Type == "openai-compat" {
-			return fmt.Errorf("config: provider %q: type openai-compat is no longer supported; use openai, openrouter, lmstudio, omlx, rapid-mlx, or ollama", name)
+			return fmt.Errorf("config: provider %q: type openai-compat is no longer supported; use openai, openrouter, lmstudio, llama-server, omlx, rapid-mlx, or ollama", name)
 		}
 		switch pc.Type {
-		case "openai", "openrouter", "lmstudio", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai", "anthropic":
+		case "openai", "openrouter", "lmstudio", "llama-server", "omlx", "lucebox", "vllm", "rapid-mlx", "ollama", "minimax", "qwen", "zai", "anthropic":
 		default:
-			return fmt.Errorf("config: provider %q has unknown type %q (use openai, openrouter, lmstudio, omlx, luce, vllm, rapid-mlx, ollama, or anthropic)", name, pc.Type)
+			return fmt.Errorf("config: provider %q has unknown type %q (use openai, openrouter, lmstudio, llama-server, omlx, luce, vllm, rapid-mlx, ollama, or anthropic)", name, pc.Type)
 		}
 		if providerUsesEndpoint(pc.Type) {
 			for i, endpoint := range pc.Endpoints {
diff --git a/internal/config/config_test.go b/internal/config/config_test.go
index 009a67e..92c7f19 100644
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -341,6 +341,7 @@ func TestBuildProvider_ConcreteProviderTypes(t *testing.T) {
 			"openai":     {Type: "openai", APIKey: "sk-test", Model: "gpt-4o"},
 			"openrouter": {Type: "openrouter", APIKey: "sk-test", Model: "openai/gpt-4o-mini"},
 			"lmstudio":   {Type: "lmstudio", BaseURL: "http://localhost:1234/v1", Model: "qwen3"},
+			"llama":      {Type: "llama-server", Model: "llama-3.1"},
 			"omlx":       {Type: "omlx", BaseURL: "http://localhost:1235/v1", Model: "qwen3"},
 			"ollama":     {Type: "ollama", BaseURL: "http://localhost:11434/v1", Model: "llama3.2"},
 			"rapidmlx":   {Type: "rapid-mlx", Model: "qwen3"},
@@ -355,6 +356,55 @@ func TestBuildProvider_ConcreteProviderTypes(t *testing.T) {
 	}
 }
 
+func TestLoad_LlamaServerDefaultBaseURLAndEndpoint(t *testing.T) {
+	isolateHome(t)
+	dir := t.TempDir()
+	cfgDir := filepath.Join(dir, ".fizeau")
+	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
+
+	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`
+providers:
+  llama:
+    type: llama-server
+    model: llama-3.1
+default: llama
+`), 0o644))
+
+	cfg, err := Load(dir)
+	require.NoError(t, err)
+
+	pc, ok := cfg.GetProvider("llama")
+	require.True(t, ok)
+	assert.Equal(t, "llama-server", pc.Type)
+	assert.Equal(t, "http://localhost:8080/v1", pc.BaseURL)
+	require.Len(t, pc.Endpoints, 1)
+	assert.Equal(t, "default", pc.Endpoints[0].Name)
+	assert.Equal(t, "http://localhost:8080/v1", pc.Endpoints[0].BaseURL)
+}
+
+func TestLoad_LlamaServerEndpointPoolUsesPort8080(t *testing.T) {
+	isolateHome(t)
+	dir := t.TempDir()
+	cfgDir := filepath.Join(dir, ".fizeau")
+	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
+
+	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(`
+endpoints:
+  - type: llama-server
+    host: vidar
+`), 0o644))
+
+	cfg, err := Load(dir)
+	require.NoError(t, err)
+
+	pc, ok := cfg.GetProvider("llama-server-vidar")
+	require.True(t, ok)
+	assert.Equal(t, "llama-server", pc.Type)
+	assert.Equal(t, "http://vidar:8080/v1", pc.BaseURL)
+	require.Len(t, pc.Endpoints, 1)
+	assert.Equal(t, "http://vidar:8080/v1", pc.Endpoints[0].BaseURL)
+}
+
 func TestBuildProvider_WithHeaders(t *testing.T) {
 	cfg := Config{
 		Providers: map[string]ProviderConfig{
diff --git a/internal/provider/llamaserver/llamaserver.go b/internal/provider/llamaserver/llamaserver.go
new file mode 100644
index 0000000..8e2d774
--- /dev/null
+++ b/internal/provider/llamaserver/llamaserver.go
@@ -0,0 +1,63 @@
+// Package llamaserver wraps the OpenAI-compatible HTTP surface exposed by
+// llama.cpp's built-in server.
+package llamaserver
+
+import (
+	agentcore "github.com/DocumentDrivenDX/fizeau/internal/core"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
+	"github.com/DocumentDrivenDX/fizeau/internal/reasoning"
+)
+
+const DefaultBaseURL = "http://localhost:8080/v1"
+
+func init() {
+	registry.Register(registry.Descriptor{
+		Type: "llama-server",
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
+		DefaultPort:    8080,
+	})
+}
+
+// ProtocolCapabilities mirrors the standard OpenAI-compatible surface.
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
+		ProviderName:   "llama-server",
+		ProviderSystem: "llama-server",
+		ModelPattern:   cfg.ModelPattern,
+		KnownModels:    cfg.KnownModels,
+		Headers:        cfg.Headers,
+		Reasoning:      cfg.Reasoning,
+		Capabilities:   &ProtocolCapabilities,
+	})
+}
diff --git a/internal/provider/registry/registry_test.go b/internal/provider/registry/registry_test.go
index 1f9e232..a7beae4 100644
--- a/internal/provider/registry/registry_test.go
+++ b/internal/provider/registry/registry_test.go
@@ -8,14 +8,15 @@ import (
 	// graph (cmd/fiz transitively imports all providers via service)
 	// without depending on those packages directly here.
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/anthropic"
+	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/llamaserver"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/lmstudio"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/lucebox"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/ollama"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/omlx"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openrouter"
-	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/rapidmlx"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/vllm"
 
 	"github.com/stretchr/testify/assert"
@@ -33,6 +34,7 @@ import (
 var expectedTypes = []string{
 	"anthropic",
 	"lmstudio",
+	"llama-server",
 	"lucebox",
 	"minimax",
 	"ollama",
diff --git a/service_models.go b/service_models.go
index 08e5a5e..ba7bc44 100644
--- a/service_models.go
+++ b/service_models.go
@@ -314,7 +314,7 @@ type discoveredModelSet struct {
 // ranks results against the catalog. IDs preserve discovery order per endpoint.
 func discoverAndRankModels(ctx context.Context, entry ServiceProviderEntry, cat *modelcatalog.Catalog) []discoveredModelSet {
 	switch normalizeServiceProviderType(entry.Type) {
-	case "openai", "openrouter", "lmstudio", "omlx", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "llama-server", "omlx", "rapid-mlx", "ollama", "minimax", "qwen", "zai":
 		endpoints := modelDiscoveryEndpoints(entry)
 		if len(endpoints) == 0 {
 			return nil
diff --git a/service_models_test.go b/service_models_test.go
index b05eaa8..dcc94e9 100644
--- a/service_models_test.go
+++ b/service_models_test.go
@@ -90,6 +90,35 @@ func TestListModels_providerTypesOpenRouterLMStudioOMLX(t *testing.T) {
 	}
 }
 
+func TestListModels_providerTypeLlamaServer(t *testing.T) {
+	llama := fakeModelsServer([]string{"llama-3.1"})
+	defer llama.Close()
+
+	sc := &fakeServiceConfig{
+		providers: map[string]ServiceProviderEntry{
+			"llama": {Type: "llama-server", BaseURL: llama.URL + "/v1"},
+		},
+		names:       []string{"llama"},
+		defaultName: "llama",
+	}
+	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
+
+	infos, err := svc.ListModels(context.Background(), ModelFilter{})
+	if err != nil {
+		t.Fatalf("ListModels: %v", err)
+	}
+	if len(infos) != 1 {
+		t.Fatalf("want 1 model, got %d: %v", len(infos), modelIDs(infos))
+	}
+	info := infos[0]
+	if info.ProviderType != "llama-server" {
+		t.Fatalf("provider type = %q, want llama-server", info.ProviderType)
+	}
+	if info.EndpointBaseURL != llama.URL+"/v1" {
+		t.Fatalf("endpoint base URL = %q, want %q", info.EndpointBaseURL, llama.URL+"/v1")
+	}
+}
+
 func TestListModels_endpointPoolReturnsEndpointMetadata(t *testing.T) {
 	vidar := fakeModelsServer([]string{"vidar-model"})
 	defer vidar.Close()
diff --git a/service_native_provider.go b/service_native_provider.go
index bbda3a1..6715ed4 100644
--- a/service_native_provider.go
+++ b/service_native_provider.go
@@ -14,14 +14,15 @@ import (
 	// the case branches and stayed even after agent-8e4eb44c collapsed
 	// them — they're load-bearing for the init() registration.
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/anthropic"
+	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/llamaserver"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/lmstudio"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/lucebox"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/ollama"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/omlx"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openai"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/openrouter"
-	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/rapidmlx"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/registry"
 	_ "github.com/DocumentDrivenDX/fizeau/internal/provider/vllm"
 )
 
diff --git a/service_providers.go b/service_providers.go
index 307386c..a552b80 100644
--- a/service_providers.go
+++ b/service_providers.go
@@ -383,7 +383,7 @@ func probeServiceProviderDetailed(ctx context.Context, entry ServiceProviderEntr
 		// Treat key presence as the connectivity signal.
 		return providerProbeResult{status: "connected", caps: providerCapabilities(entry)}
 
-	case "openai", "openrouter", "lmstudio", "omlx", "rapid-mlx", "ollama", "minimax", "qwen", "zai", "":
+	case "openai", "openrouter", "lmstudio", "llama-server", "omlx", "rapid-mlx", "ollama", "minimax", "qwen", "zai", "":
 		if entry.BaseURL == "" {
 			return providerProbeResult{status: "error: base_url not configured", detail: "base_url not configured"}
 		}
diff --git a/service_providers_test.go b/service_providers_test.go
index af2397a..3c1ef86 100644
--- a/service_providers_test.go
+++ b/service_providers_test.go
@@ -116,6 +116,43 @@ func TestListProviders_Connected(t *testing.T) {
 	}
 }
 
+func TestListProviders_LlamaServerConnected(t *testing.T) {
+	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
+			w.Header().Set("Content-Type", "application/json")
+			json.NewEncoder(w).Encode(map[string]any{
+				"data": []map[string]any{{"id": "llama-3.1"}},
+			})
+			return
+		}
+		http.NotFound(w, r)
+	}))
+	defer ts.Close()
+
+	sc := &fakeServiceConfig{
+		providers: map[string]ServiceProviderEntry{
+			"llama": {Type: "llama-server", BaseURL: ts.URL + "/v1"},
+		},
+		names:       []string{"llama"},
+		defaultName: "llama",
+	}
+	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
+
+	infos, err := svc.ListProviders(context.Background())
+	if err != nil {
+		t.Fatalf("ListProviders: %v", err)
+	}
+	if len(infos) != 1 {
+		t.Fatalf("want 1 provider, got %d", len(infos))
+	}
+	if infos[0].Type != "llama-server" {
+		t.Fatalf("provider type = %q, want llama-server", infos[0].Type)
+	}
+	if infos[0].Status != "connected" {
+		t.Fatalf("provider status = %q, want connected", infos[0].Status)
+	}
+}
+
 func TestListProviders_OMLXAdvertisesReasoningControl(t *testing.T) {
 	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
diff --git a/service_routing.go b/service_routing.go
index 06f97b3..b1990bb 100644
--- a/service_routing.go
+++ b/service_routing.go
@@ -742,7 +742,7 @@ func anyProviderSupportsTools(providers []routing.ProviderEntry) bool {
 
 func providerUsesLiveDiscovery(providerType string) bool {
 	switch normalizeServiceProviderType(providerType) {
-	case "openai", "openrouter", "lmstudio", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm", "minimax", "qwen", "zai":
+	case "openai", "openrouter", "lmstudio", "llama-server", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm", "minimax", "qwen", "zai":
 		return true
 	default:
 		return false
@@ -792,7 +792,7 @@ func (s *service) applySubscriptionRoutingCost(entry *routing.HarnessEntry, cat
 
 func providerTypeIsLocalEndpoint(providerType string) bool {
 	switch normalizeServiceProviderType(providerType) {
-	case "lmstudio", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm":
+	case "lmstudio", "llama-server", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm":
 		return true
 	default:
 		return false
diff --git a/service_routing_test.go b/service_routing_test.go
index 767237d..75bb1be 100644
--- a/service_routing_test.go
+++ b/service_routing_test.go
@@ -99,6 +99,18 @@ func TestResolveRouteSuccessIncludesCandidates(t *testing.T) {
 	}
 }
 
+func TestProviderUsesLiveDiscovery_LlamaServer(t *testing.T) {
+	if !providerUsesLiveDiscovery("llama-server") {
+		t.Fatal("expected llama-server to use live discovery")
+	}
+}
+
+func TestProviderTypeIsLocalEndpoint_LlamaServer(t *testing.T) {
+	if !providerTypeIsLocalEndpoint("llama-server") {
+		t.Fatal("expected llama-server to count as a local endpoint")
+	}
+}
+
 func TestResolveRouteErrorIncludesCandidatesAndTraceError(t *testing.T) {
 	t.Setenv("GEMINI_API_KEY", "redacted")
 	t.Setenv("GOOGLE_API_KEY", "")
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
