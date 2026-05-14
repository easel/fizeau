<bead-review>
  <bead id="fizeau-97c486b6" iter=1>
    <title>Normalize provider endpoint utilization</title>
    <description>
Implement provider-owned utilization probes for vLLM and llama-server and normalize them into a shared endpoint utilization shape. In-scope files: internal/provider/vllm, internal/provider/llamaserver, shared provider/utilization helper package if needed, and provider tests using cassette replay. Requirements: derive server root from base_url ending in /v1; vLLM reads /metrics running/waiting/cache metrics; llama-server reads /metrics processing/deferred/cache metrics and falls back to /slots occupancy when metrics are unavailable; probe failures return stale or unknown utilization without marking endpoints unavailable. Out of scope: route lease store, route scoring changes, shared multi-machine lease backend, and server record-mode bootstrapping beyond cassettes already added.
    </description>
    <acceptance>
1. go test ./internal/provider/vllm ./internal/provider/llamaserver ./internal/provider/... -run 'Utilization|Metrics|Slots|Cassette' passes. 2. vLLM base_url http://host:8000/v1 probes http://host:8000/metrics. 3. llama-server base_url http://host:8080/v1 probes http://host:8080/metrics and falls back to http://host:8080/slots. 4. Normalized utilization includes active requests, queued/deferred requests, cache usage when known, source, freshness, observed time, and max concurrency when slots expose it. 5. Probe failure returns stale/unknown utilization and does not make the endpoint unavailable.
    </acceptance>
    <labels>area:provider, kind:feature</labels>
  </bead>

  <changed-files>
    <file>internal/provider/llamaserver/utilization_probe.go</file>
    <file>internal/provider/llamaserver/utilization_probe_test.go</file>
    <file>internal/provider/utilization/utilization.go</file>
    <file>internal/provider/utilization/utilization_test.go</file>
    <file>internal/provider/vllm/utilization_probe.go</file>
    <file>internal/provider/vllm/utilization_probe_test.go</file>
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

  <diff rev="632bd84a8c7bb8bec05bdae0e7f9bfaacb9fcd42">
<untrusted-data>
diff --git a/internal/provider/llamaserver/utilization_probe.go b/internal/provider/llamaserver/utilization_probe.go
new file mode 100644
index 0000000..a716272
--- /dev/null
+++ b/internal/provider/llamaserver/utilization_probe.go
@@ -0,0 +1,150 @@
+package llamaserver
+
+import (
+	"context"
+	"encoding/json"
+	"fmt"
+	"io"
+	"net/http"
+	"strings"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+)
+
+// UtilizationProbe queries llama-server observability endpoints and
+// normalizes them into the shared endpoint utilization shape.
+type UtilizationProbe struct {
+	baseURL string
+	client  *http.Client
+	cache   utilization.Cache
+}
+
+// NewUtilizationProbe creates a probe for an OpenAI-compatible llama-server
+// base URL.
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
+// Probe first tries /metrics on the server root and falls back to /slots when
+// metrics are unavailable.
+func (p *UtilizationProbe) Probe(ctx context.Context) utilization.EndpointUtilization {
+	if sample, ok := p.probeMetrics(ctx); ok {
+		return p.cache.Remember(sample)
+	}
+	if sample, ok := p.probeSlots(ctx); ok {
+		return p.cache.Remember(sample)
+	}
+	if stale, ok := p.cache.Stale(); ok {
+		return stale
+	}
+	return utilization.Unknown(utilization.SourceLlamaMetrics)
+}
+
+func (p *UtilizationProbe) probeMetrics(ctx context.Context) (utilization.EndpointUtilization, bool) {
+	body, err := p.get(ctx, utilization.ServerRoot(p.baseURL)+"/metrics")
+	if err != nil {
+		return utilization.EndpointUtilization{}, false
+	}
+
+	processing, ok := utilization.ParsePrometheusMetricValue(body, "llamacpp:requests_processing")
+	if !ok {
+		return utilization.EndpointUtilization{}, false
+	}
+	deferred, ok := utilization.ParsePrometheusMetricValue(body, "llamacpp:requests_deferred")
+	if !ok {
+		return utilization.EndpointUtilization{}, false
+	}
+
+	sample := utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(int(processing)),
+		QueuedRequests: utilization.Int(int(deferred)),
+		Source:         utilization.SourceLlamaMetrics,
+	}
+	if cacheUsage, ok := utilization.ParsePrometheusMetricValue(body, "llamacpp:kv_cache_usage_ratio"); ok {
+		sample.CacheUsage = utilization.Float64(cacheUsage)
+	}
+	return sample, true
+}
+
+func (p *UtilizationProbe) probeSlots(ctx context.Context) (utilization.EndpointUtilization, bool) {
+	body, err := p.get(ctx, utilization.ServerRoot(p.baseURL)+"/slots")
+	if err != nil {
+		return utilization.EndpointUtilization{}, false
+	}
+
+	var arrayPayload []map[string]any
+	slots := 0
+	processing := 0
+	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &arrayPayload); err == nil {
+		slots = len(arrayPayload)
+		for _, slot := range arrayPayload {
+			if isProcessing(slot) {
+				processing++
+			}
+		}
+		return p.slotSample(processing, slots), true
+	}
+
+	var objectPayload struct {
+		Slots     []map[string]any `json:"slots"`
+		SlotCount int              `json:"slot_count"`
+	}
+	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &objectPayload); err != nil {
+		return utilization.EndpointUtilization{}, false
+	}
+	slots = objectPayload.SlotCount
+	if slots == 0 {
+		slots = len(objectPayload.Slots)
+	}
+	for _, slot := range objectPayload.Slots {
+		if isProcessing(slot) {
+			processing++
+		}
+	}
+	return p.slotSample(processing, slots), true
+}
+
+func (p *UtilizationProbe) slotSample(processing, slots int) utilization.EndpointUtilization {
+	sample := utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(processing),
+		Source:         utilization.SourceLlamaSlots,
+	}
+	if slots > 0 {
+		sample.MaxConcurrency = utilization.Int(slots)
+		occupancy := float64(processing) / float64(slots)
+		sample.CacheUsage = utilization.Float64(occupancy)
+	}
+	return sample
+}
+
+func isProcessing(slot map[string]any) bool {
+	processing, _ := slot["is_processing"].(bool)
+	return processing
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
diff --git a/internal/provider/llamaserver/utilization_probe_test.go b/internal/provider/llamaserver/utilization_probe_test.go
new file mode 100644
index 0000000..459dbe2
--- /dev/null
+++ b/internal/provider/llamaserver/utilization_probe_test.go
@@ -0,0 +1,82 @@
+package llamaserver
+
+import (
+	"context"
+	"net/http"
+	"net/http/httptest"
+	"strings"
+	"testing"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/testutil"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+	"github.com/stretchr/testify/require"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
+)
+
+func TestLlamaServerUtilizationProbe_CassetteReplay(t *testing.T) {
+	if testutil.ModeForEnvironment() == recorder.ModeRecordOnly {
+		t.Skip("cassette replay coverage is exercised in the existing record-mode test")
+	}
+
+	rec, err := testutil.NewRecorder(testutil.CassettePath("testdata/cassettes", llamaCassetteName))
+	require.NoError(t, err)
+	t.Cleanup(func() {
+		require.NoError(t, rec.Stop())
+	})
+
+	probe := NewUtilizationProbe("http://replay.invalid/v1", rec.GetDefaultClient())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.SourceLlamaMetrics, sample.Source)
+	require.Equal(t, utilization.FreshnessFresh, sample.Freshness)
+	require.NotNil(t, sample.ActiveRequests)
+	require.NotNil(t, sample.QueuedRequests)
+	require.Zero(t, *sample.ActiveRequests)
+	require.Zero(t, *sample.QueuedRequests)
+	require.Nil(t, sample.CacheUsage)
+	require.NotZero(t, sample.ObservedAt)
+}
+
+func TestLlamaServerUtilizationProbe_FallsBackToSlots(t *testing.T) {
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		switch r.URL.Path {
+		case "/metrics":
+			http.Error(w, "metrics disabled", http.StatusNotFound)
+		case "/slots":
+			_, _ = w.Write([]byte(`{"slots":[{"is_processing":true},{"is_processing":false},{"is_processing":true}],"slot_count":3}`))
+		default:
+			http.NotFound(w, r)
+		}
+	}))
+	t.Cleanup(srv.Close)
+
+	probe := NewUtilizationProbe(srv.URL+"/v1", srv.Client())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.SourceLlamaSlots, sample.Source)
+	require.Equal(t, utilization.FreshnessFresh, sample.Freshness)
+	require.NotNil(t, sample.ActiveRequests)
+	require.NotNil(t, sample.MaxConcurrency)
+	require.NotNil(t, sample.CacheUsage)
+	require.Equal(t, 2, *sample.ActiveRequests)
+	require.Equal(t, 3, *sample.MaxConcurrency)
+	require.InDelta(t, 2.0/3.0, *sample.CacheUsage, 1e-9)
+	require.Nil(t, sample.QueuedRequests)
+}
+
+func TestLlamaServerUtilizationProbe_UnknownOnInitialFailure(t *testing.T) {
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		http.Error(w, strings.TrimPrefix(r.URL.Path, "/"), http.StatusServiceUnavailable)
+	}))
+	t.Cleanup(srv.Close)
+
+	probe := NewUtilizationProbe(srv.URL+"/v1", srv.Client())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.FreshnessUnknown, sample.Freshness)
+	require.Equal(t, utilization.SourceLlamaMetrics, sample.Source)
+	require.Nil(t, sample.ActiveRequests)
+	require.Nil(t, sample.QueuedRequests)
+	require.Nil(t, sample.CacheUsage)
+	require.Nil(t, sample.MaxConcurrency)
+}
diff --git a/internal/provider/utilization/utilization.go b/internal/provider/utilization/utilization.go
new file mode 100644
index 0000000..54595f0
--- /dev/null
+++ b/internal/provider/utilization/utilization.go
@@ -0,0 +1,160 @@
+package utilization
+
+import (
+	"net/url"
+	"strconv"
+	"strings"
+	"sync"
+	"time"
+)
+
+// Source identifies the probe path that produced a utilization sample.
+type Source string
+
+const (
+	SourceUnknown      Source = "unknown"
+	SourceVLLMMetrics  Source = "vllm.metrics"
+	SourceLlamaMetrics Source = "llama-server.metrics"
+	SourceLlamaSlots   Source = "llama-server.slots"
+)
+
+// Freshness describes whether a sample was observed just now, reused after a
+// failed probe, or has no known prior observation.
+type Freshness string
+
+const (
+	FreshnessFresh   Freshness = "fresh"
+	FreshnessStale   Freshness = "stale"
+	FreshnessUnknown Freshness = "unknown"
+)
+
+// EndpointUtilization is the normalized utilization shape shared by local
+// provider probes.
+type EndpointUtilization struct {
+	ActiveRequests *int
+	QueuedRequests *int
+	CacheUsage     *float64
+	MaxConcurrency *int
+	Source         Source
+	Freshness      Freshness
+	ObservedAt     time.Time
+}
+
+// Cache preserves the most recent successful sample so probe failures can
+// return stale utilization instead of surfacing hard endpoint unavailability.
+type Cache struct {
+	mu   sync.Mutex
+	last *EndpointUtilization
+}
+
+// Remember stores a fresh sample and returns a normalized copy with fresh
+// freshness and an observed timestamp.
+func (c *Cache) Remember(sample EndpointUtilization) EndpointUtilization {
+	c.mu.Lock()
+	defer c.mu.Unlock()
+
+	now := time.Now().UTC()
+	sample.Freshness = FreshnessFresh
+	sample.ObservedAt = now
+	stored := clone(sample)
+	c.last = &stored
+	return stored
+}
+
+// Stale returns the last successful sample marked stale. The boolean reports
+// whether a previous sample existed.
+func (c *Cache) Stale() (EndpointUtilization, bool) {
+	c.mu.Lock()
+	defer c.mu.Unlock()
+
+	if c.last == nil {
+		return EndpointUtilization{}, false
+	}
+	stale := clone(*c.last)
+	stale.Freshness = FreshnessStale
+	return stale, true
+}
+
+// Unknown returns a sample with unknown freshness and no numeric values.
+func Unknown(source Source) EndpointUtilization {
+	return EndpointUtilization{
+		Source:    source,
+		Freshness: FreshnessUnknown,
+	}
+}
+
+// Int returns a pointer to v.
+func Int(v int) *int {
+	return &v
+}
+
+// Float64 returns a pointer to v.
+func Float64(v float64) *float64 {
+	return &v
+}
+
+// ServerRoot strips a trailing /v1 path component from an OpenAI-compatible
+// base URL while preserving the scheme, host, and any prefix path.
+func ServerRoot(baseURL string) string {
+	trimmed := strings.TrimSpace(strings.TrimRight(baseURL, "/"))
+	if trimmed == "" {
+		return ""
+	}
+
+	parsed, err := url.Parse(trimmed)
+	if err != nil {
+		return strings.TrimSuffix(trimmed, "/v1")
+	}
+
+	parsed.Fragment = ""
+	parsed.RawQuery = ""
+	path := strings.TrimRight(parsed.Path, "/")
+	if strings.HasSuffix(path, "/v1") {
+		path = strings.TrimSuffix(path, "/v1")
+	}
+	parsed.Path = path
+	return strings.TrimRight(parsed.String(), "/")
+}
+
+// ParsePrometheusMetricValue returns the first numeric value for metric from
+// a Prometheus-style plaintext metrics body.
+func ParsePrometheusMetricValue(body, metric string) (float64, bool) {
+	for _, line := range strings.Split(body, "\n") {
+		line = strings.TrimSpace(line)
+		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, metric) {
+			continue
+		}
+		rest := strings.TrimSpace(strings.TrimPrefix(line, metric))
+		if strings.HasPrefix(rest, "{") {
+			if idx := strings.Index(rest, "}"); idx >= 0 {
+				rest = strings.TrimSpace(rest[idx+1:])
+			}
+		}
+		fields := strings.Fields(rest)
+		if len(fields) == 0 {
+			continue
+		}
+		val, err := strconv.ParseFloat(fields[0], 64)
+		if err == nil {
+			return val, true
+		}
+	}
+	return 0, false
+}
+
+func clone(sample EndpointUtilization) EndpointUtilization {
+	out := sample
+	if sample.ActiveRequests != nil {
+		out.ActiveRequests = Int(*sample.ActiveRequests)
+	}
+	if sample.QueuedRequests != nil {
+		out.QueuedRequests = Int(*sample.QueuedRequests)
+	}
+	if sample.CacheUsage != nil {
+		out.CacheUsage = Float64(*sample.CacheUsage)
+	}
+	if sample.MaxConcurrency != nil {
+		out.MaxConcurrency = Int(*sample.MaxConcurrency)
+	}
+	return out
+}
diff --git a/internal/provider/utilization/utilization_test.go b/internal/provider/utilization/utilization_test.go
new file mode 100644
index 0000000..c2797fc
--- /dev/null
+++ b/internal/provider/utilization/utilization_test.go
@@ -0,0 +1,33 @@
+package utilization
+
+import (
+	"testing"
+
+	"github.com/stretchr/testify/require"
+)
+
+func TestServerRootStripsTrailingV1(t *testing.T) {
+	require.Equal(t, "http://host:8000", ServerRoot("http://host:8000/v1"))
+	require.Equal(t, "http://host:8000", ServerRoot("http://host:8000/v1/"))
+	require.Equal(t, "http://host:8000/base", ServerRoot("http://host:8000/base/v1"))
+}
+
+func TestCacheReturnsFreshThenStale(t *testing.T) {
+	var cache Cache
+	fresh := cache.Remember(EndpointUtilization{
+		ActiveRequests: Int(2),
+		QueuedRequests: Int(3),
+		CacheUsage:     Float64(0.25),
+		MaxConcurrency: Int(4),
+		Source:         SourceVLLMMetrics,
+	})
+	require.Equal(t, FreshnessFresh, fresh.Freshness)
+	require.NotZero(t, fresh.ObservedAt)
+
+	stale, ok := cache.Stale()
+	require.True(t, ok)
+	require.Equal(t, FreshnessStale, stale.Freshness)
+	require.Equal(t, fresh.ObservedAt, stale.ObservedAt)
+	require.NotNil(t, stale.ActiveRequests)
+	require.Equal(t, 2, *stale.ActiveRequests)
+}
diff --git a/internal/provider/vllm/utilization_probe.go b/internal/provider/vllm/utilization_probe.go
new file mode 100644
index 0000000..1d5e003
--- /dev/null
+++ b/internal/provider/vllm/utilization_probe.go
@@ -0,0 +1,91 @@
+package vllm
+
+import (
+	"context"
+	"fmt"
+	"io"
+	"net/http"
+	"strings"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+)
+
+// UtilizationProbe queries vLLM server-root observability endpoints and
+// normalizes them into the shared endpoint utilization shape.
+type UtilizationProbe struct {
+	baseURL string
+	client  *http.Client
+	cache   utilization.Cache
+}
+
+// NewUtilizationProbe creates a probe for an OpenAI-compatible vLLM base URL.
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
+// Probe fetches /metrics from the server root and returns a normalized sample.
+// Failures return stale or unknown utilization instead of surfacing endpoint
+// unavailability.
+func (p *UtilizationProbe) Probe(ctx context.Context) utilization.EndpointUtilization {
+	body, err := p.get(ctx, utilization.ServerRoot(p.baseURL)+"/metrics")
+	if err != nil {
+		if stale, ok := p.cache.Stale(); ok {
+			return stale
+		}
+		return utilization.Unknown(utilization.SourceVLLMMetrics)
+	}
+
+	running, ok := utilization.ParsePrometheusMetricValue(body, "vllm:num_requests_running")
+	if !ok {
+		if stale, ok := p.cache.Stale(); ok {
+			return stale
+		}
+		return utilization.Unknown(utilization.SourceVLLMMetrics)
+	}
+	waiting, ok := utilization.ParsePrometheusMetricValue(body, "vllm:num_requests_waiting")
+	if !ok {
+		if stale, ok := p.cache.Stale(); ok {
+			return stale
+		}
+		return utilization.Unknown(utilization.SourceVLLMMetrics)
+	}
+
+	sample := utilization.EndpointUtilization{
+		ActiveRequests: utilization.Int(int(running)),
+		QueuedRequests: utilization.Int(int(waiting)),
+		Source:         utilization.SourceVLLMMetrics,
+	}
+	if cacheUsage, ok := utilization.ParsePrometheusMetricValue(body, "vllm:kv_cache_usage_perc"); ok {
+		sample.CacheUsage = utilization.Float64(cacheUsage)
+	} else if cacheUsage, ok := utilization.ParsePrometheusMetricValue(body, "vllm:gpu_cache_usage_perc"); ok {
+		sample.CacheUsage = utilization.Float64(cacheUsage)
+	}
+	return p.cache.Remember(sample)
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
diff --git a/internal/provider/vllm/utilization_probe_test.go b/internal/provider/vllm/utilization_probe_test.go
new file mode 100644
index 0000000..dbe65d1
--- /dev/null
+++ b/internal/provider/vllm/utilization_probe_test.go
@@ -0,0 +1,64 @@
+package vllm
+
+import (
+	"context"
+	"net/http"
+	"net/http/httptest"
+	"testing"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/testutil"
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/utilization"
+	"github.com/stretchr/testify/require"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
+)
+
+func TestVLLMUtilizationProbe_CassetteReplay(t *testing.T) {
+	if testutil.ModeForEnvironment() == recorder.ModeRecordOnly {
+		t.Skip("cassette replay coverage is exercised in the existing record-mode test")
+	}
+
+	rec, err := testutil.NewRecorder(testutil.CassettePath("testdata/cassettes", vllmCassetteName))
+	require.NoError(t, err)
+	t.Cleanup(func() {
+		require.NoError(t, rec.Stop())
+	})
+
+	probe := NewUtilizationProbe("http://replay.invalid/v1", rec.GetDefaultClient())
+	sample := probe.Probe(context.Background())
+
+	require.Equal(t, utilization.SourceVLLMMetrics, sample.Source)
+	require.Equal(t, utilization.FreshnessFresh, sample.Freshness)
+	require.NotNil(t, sample.ActiveRequests)
+	require.NotNil(t, sample.QueuedRequests)
+	require.NotNil(t, sample.CacheUsage)
+	require.Zero(t, *sample.ActiveRequests)
+	require.Zero(t, *sample.QueuedRequests)
+	require.NotZero(t, sample.ObservedAt)
+}
+
+func TestVLLMUtilizationProbe_FailureReturnsStaleOrUnknown(t *testing.T) {
+	var hits int
+	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		require.Equal(t, "/metrics", r.URL.Path)
+		hits++
+		if hits == 1 {
+			_, _ = w.Write([]byte("vllm:num_requests_running 2\nvllm:num_requests_waiting 1\nvllm:kv_cache_usage_perc 0.5\n"))
+			return
+		}
+		http.Error(w, "boom", http.StatusServiceUnavailable)
+	}))
+	t.Cleanup(srv.Close)
+
+	probe := NewUtilizationProbe(srv.URL+"/v1", srv.Client())
+
+	fresh := probe.Probe(context.Background())
+	require.Equal(t, utilization.FreshnessFresh, fresh.Freshness)
+	require.NotNil(t, fresh.ActiveRequests)
+	require.Equal(t, 2, *fresh.ActiveRequests)
+
+	stale := probe.Probe(context.Background())
+	require.Equal(t, utilization.FreshnessStale, stale.Freshness)
+	require.NotNil(t, stale.ActiveRequests)
+	require.Equal(t, 2, *stale.ActiveRequests)
+	require.Equal(t, fresh.ObservedAt, stale.ObservedAt)
+}
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
