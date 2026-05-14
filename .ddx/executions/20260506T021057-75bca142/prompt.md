<bead-review>
  <bead id="fizeau-1a8614fe" iter=1>
    <title>Record llama-server utilization cassettes</title>
    <description>
Add llama.cpp llama-server real-server cassette coverage. In-scope files: internal/provider/llamaserver tests and committed testdata/cassettes, plus any small server bootstrap helper used only by record-mode tests. Record mode must install or pull llama-server, obtain or generate a trivial CPU GGUF model suitable for local test execution, start llama-server on a free local port with --metrics, /slots enabled, and --parallel 1, wait for /v1/models readiness, record /v1/models, /metrics, /slots, minimal /v1/chat/completions, and /metrics plus /slots while a request is in flight. Replay mode must be the default CI path and parse cassette data only. Out of scope: vLLM cassettes, route scoring, sticky leases, and hand-written HTTP mocks.
    </description>
    <acceptance>
1. go test ./internal/provider/llamaserver ./... -run 'Llama.*Cassette|Llama.*Utilization' passes in replay mode using committed cassettes. 2. FIZEAU_RECORD_PROVIDER_CASSETTES=1 go test ./internal/provider/llamaserver -run 'Record' installs or starts llama-server itself and refreshes cassettes without requiring a manually pre-started server. 3. Cassettes include real /v1/models, /metrics, /slots, minimal chat, and busy-slot evidence. 4. Replay parsing extracts llamacpp:requests_processing, llamacpp:requests_deferred, llamacpp:kv_cache_usage_ratio, and slot is_processing occupancy. 5. No hand-written endpoint mocks define the provider contract.
    </acceptance>
    <labels>area:provider, kind:test</labels>
  </bead>

  <changed-files>
    <file>internal/provider/llamaserver/llamaserver_cassette_test.go</file>
    <file>internal/provider/llamaserver/testdata/cassettes/llama_server_utilization.yaml</file>
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

  <diff rev="2f0150c06b1dc6b58974bf43ac1883b55034860b">
<untrusted-data>
diff --git a/internal/provider/llamaserver/llamaserver_cassette_test.go b/internal/provider/llamaserver/llamaserver_cassette_test.go
new file mode 100644
index 0000000..8eb3333
--- /dev/null
+++ b/internal/provider/llamaserver/llamaserver_cassette_test.go
@@ -0,0 +1,559 @@
+package llamaserver
+
+import (
+	"context"
+	"encoding/json"
+	"fmt"
+	"io"
+	"net"
+	"net/http"
+	"os"
+	"os/exec"
+	"path/filepath"
+	"strconv"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/provider/testutil"
+	"github.com/stretchr/testify/require"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
+)
+
+const (
+	llamaCassetteName      = "llama_server_utilization"
+	llamaRecordImage       = "ghcr.io/ggml-org/llama.cpp:server"
+	llamaRecordRepo        = "QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF"
+	llamaRecordFile        = "tinyllama-15M-alpaca-finetuned.Q4_K_M.gguf"
+	llamaRecordPrompt      = "Reply with one short word."
+	llamaBusyPrompt        = "Write a 200-word paragraph about a small robot."
+	llamaRequestMaxTokens  = 8
+	llamaBusyRequestTokens = 64
+)
+
+type llamaMetricsSnapshot struct {
+	RequestsProcessing int
+	RequestsDeferred   int
+	KVCacheUsageRatio  float64
+}
+
+type llamaSlotsSnapshot struct {
+	SlotCount       int
+	ProcessingSlots int
+}
+
+type llamaChatMessage struct {
+	Content string `json:"content"`
+	Role    string `json:"role"`
+}
+
+func TestLlamaServerCassetteAndUtilization(t *testing.T) {
+	cassettePath := testutil.CassettePath(filepath.Join("testdata", "cassettes"), llamaCassetteName)
+
+	rec, err := testutil.NewRecorder(cassettePath)
+	require.NoError(t, err)
+	client := rec.GetDefaultClient()
+	t.Cleanup(func() {
+		require.NoError(t, rec.Stop())
+	})
+
+	if testutil.ModeForEnvironment() == recorder.ModeRecordOnly {
+		srv := mustStartLlamaServer(t)
+		t.Cleanup(srv.Stop)
+
+		recordInteractions(t, client, srv.BaseURL, srv.DirectClient, true)
+		return
+	}
+
+	recordInteractions(t, client, "http://replay.invalid/v1", http.DefaultClient, false)
+}
+
+func TestLlamaServerUtilization_ParseMetricsAndSlots(t *testing.T) {
+	metricSamples := []struct {
+		name string
+		body string
+		want llamaMetricsSnapshot
+	}{
+		{
+			name: "metrics snapshot",
+			body: strings.Join([]string{
+				"# HELP llamacpp:requests_processing Number of requests processing.",
+				"llamacpp:requests_processing 1",
+				"llamacpp:requests_deferred 2",
+				"llamacpp:kv_cache_usage_ratio 0.375",
+			}, "\n"),
+			want: llamaMetricsSnapshot{
+				RequestsProcessing: 1,
+				RequestsDeferred:   2,
+				KVCacheUsageRatio:  0.375,
+			},
+		},
+	}
+
+	for _, tc := range metricSamples {
+		tc := tc
+		t.Run(tc.name, func(t *testing.T) {
+			got, err := parseLlamaMetrics(tc.body)
+			require.NoError(t, err)
+			require.Equal(t, tc.want.RequestsProcessing, got.RequestsProcessing)
+			require.Equal(t, tc.want.RequestsDeferred, got.RequestsDeferred)
+			require.InDelta(t, tc.want.KVCacheUsageRatio, got.KVCacheUsageRatio, 1e-6)
+		})
+	}
+
+	slotSamples := []struct {
+		name string
+		body string
+		want llamaSlotsSnapshot
+	}{
+		{
+			name: "array payload",
+			body: `[
+  {"id":0,"id_task":101,"is_processing":true},
+  {"id":1,"id_task":102,"is_processing":false}
+]`,
+			want: llamaSlotsSnapshot{
+				SlotCount:       2,
+				ProcessingSlots: 1,
+			},
+		},
+		{
+			name: "object payload",
+			body: `{"slots":[{"is_processing":true},{"is_processing":true}],"slot_count":2}`,
+			want: llamaSlotsSnapshot{
+				SlotCount:       2,
+				ProcessingSlots: 2,
+			},
+		},
+	}
+
+	for _, tc := range slotSamples {
+		tc := tc
+		t.Run(tc.name, func(t *testing.T) {
+			got, err := parseLlamaSlots(tc.body)
+			require.NoError(t, err)
+			require.Equal(t, tc.want.SlotCount, got.SlotCount)
+			require.Equal(t, tc.want.ProcessingSlots, got.ProcessingSlots)
+		})
+	}
+}
+
+func recordInteractions(t *testing.T, client *http.Client, baseURL string, probeClient *http.Client, recordMode bool) {
+	t.Helper()
+	rootURL := serverRootURL(baseURL)
+	models := fetchModels(t, client, baseURL)
+	require.NotEmpty(t, models)
+	model := models[0]
+
+	idleMetrics := fetchMetrics(t, client, rootURL)
+	require.GreaterOrEqual(t, idleMetrics.RequestsProcessing, 0)
+	require.GreaterOrEqual(t, idleMetrics.RequestsDeferred, 0)
+
+	idleSlots := fetchSlots(t, client, rootURL)
+	require.GreaterOrEqual(t, idleSlots.SlotCount, 1)
+	require.Equal(t, 0, idleSlots.ProcessingSlots)
+
+	minimalChat := chatCompletion(t, client, baseURL, model, llamaRecordPrompt, llamaRequestMaxTokens)
+	require.NotEmpty(t, minimalChat)
+
+	if recordMode {
+		busyResp := startStreamingChat(t, probeClient, baseURL, model, llamaBusyPrompt, llamaBusyRequestTokens)
+		t.Cleanup(func() {
+			if busyResp != nil && busyResp.Body != nil {
+				_, _ = io.Copy(io.Discard, busyResp.Body)
+				_ = busyResp.Body.Close()
+			}
+		})
+
+		waitForLlamaMetrics(t, probeClient, rootURL, func(s llamaMetricsSnapshot) bool {
+			return s.RequestsProcessing > 0
+		})
+	}
+
+	busyMetrics := fetchMetrics(t, client, rootURL)
+	require.GreaterOrEqual(t, busyMetrics.RequestsProcessing, 1)
+
+	busySlots := fetchSlots(t, client, rootURL)
+	require.GreaterOrEqual(t, busySlots.ProcessingSlots, 1)
+	require.GreaterOrEqual(t, busySlots.SlotCount, busySlots.ProcessingSlots)
+
+	busyUtilization := combineLlamaUtilization(busyMetrics, busySlots)
+	require.Greater(t, busyUtilization.KVCacheUsageRatio, 0.0)
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
+func fetchMetrics(t *testing.T, client *http.Client, baseURL string) llamaMetricsSnapshot {
+	t.Helper()
+	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/metrics", nil)
+	require.NoError(t, err)
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	defer resp.Body.Close()
+	require.NoError(t, statusOK(resp.StatusCode))
+
+	body, err := io.ReadAll(resp.Body)
+	require.NoError(t, err)
+	snap, err := parseLlamaMetrics(string(body))
+	require.NoError(t, err)
+	return snap
+}
+
+func fetchSlots(t *testing.T, client *http.Client, baseURL string) llamaSlotsSnapshot {
+	t.Helper()
+	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/slots", nil)
+	require.NoError(t, err)
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	defer resp.Body.Close()
+	require.NoError(t, statusOK(resp.StatusCode))
+
+	body, err := io.ReadAll(resp.Body)
+	require.NoError(t, err)
+	snap, err := parseLlamaSlots(string(body))
+	require.NoError(t, err)
+	return snap
+}
+
+func chatCompletion(t *testing.T, client *http.Client, baseURL, model, prompt string, maxTokens int) string {
+	t.Helper()
+	content, err := chatCompletionRaw(client, baseURL, model, prompt, maxTokens)
+	require.NoError(t, err)
+	return content
+}
+
+func startStreamingChat(t *testing.T, client *http.Client, baseURL, model, prompt string, maxTokens int) *http.Response {
+	t.Helper()
+	resp, err := startStreamingChatRaw(client, baseURL, model, prompt, maxTokens)
+	require.NoError(t, err)
+	return resp
+}
+
+func chatCompletionRaw(client *http.Client, baseURL, model, prompt string, maxTokens int) (string, error) {
+	body := struct {
+		MaxTokens   int                `json:"max_tokens"`
+		Messages    []llamaChatMessage `json:"messages"`
+		Model       string             `json:"model"`
+		Temperature int                `json:"temperature"`
+	}{
+		MaxTokens:   maxTokens,
+		Messages:    []llamaChatMessage{{Content: prompt, Role: "user"}},
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
+func startStreamingChatRaw(client *http.Client, baseURL, model, prompt string, maxTokens int) (*http.Response, error) {
+	body := struct {
+		MaxTokens   int                `json:"max_tokens"`
+		Messages    []llamaChatMessage `json:"messages"`
+		Model       string             `json:"model"`
+		Temperature int                `json:"temperature"`
+		Stream      bool               `json:"stream"`
+	}{
+		MaxTokens:   maxTokens,
+		Messages:    []llamaChatMessage{{Content: prompt, Role: "user"}},
+		Model:       model,
+		Temperature: 0,
+		Stream:      true,
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
+
+	resp, err := client.Do(req)
+	if err != nil {
+		return nil, err
+	}
+	if err := statusOK(resp.StatusCode); err != nil {
+		_ = resp.Body.Close()
+		return nil, err
+	}
+	return resp, nil
+}
+
+func waitForLlamaMetrics(t *testing.T, client *http.Client, baseURL string, predicate func(llamaMetricsSnapshot) bool) {
+	t.Helper()
+	deadline := time.Now().Add(2 * time.Minute)
+	for time.Now().Before(deadline) {
+		snap := fetchMetrics(t, client, baseURL)
+		if predicate(snap) {
+			return
+		}
+		time.Sleep(250 * time.Millisecond)
+	}
+	t.Fatalf("timed out waiting for llama-server metrics predicate at %s", baseURL)
+}
+
+func parseLlamaMetrics(body string) (llamaMetricsSnapshot, error) {
+	out := llamaMetricsSnapshot{}
+	processing, ok := parsePromMetricValue(body, "llamacpp:requests_processing")
+	if !ok {
+		return out, fmt.Errorf("parse llamacpp:requests_processing: not found")
+	}
+	deferred, ok := parsePromMetricValue(body, "llamacpp:requests_deferred")
+	if !ok {
+		return out, fmt.Errorf("parse llamacpp:requests_deferred: not found")
+	}
+	cacheUsage, ok := parsePromMetricValue(body, "llamacpp:kv_cache_usage_ratio")
+	if ok {
+		out.KVCacheUsageRatio = cacheUsage
+	}
+	out.RequestsProcessing = int(processing)
+	out.RequestsDeferred = int(deferred)
+	return out, nil
+}
+
+func parseLlamaSlots(body string) (llamaSlotsSnapshot, error) {
+	out := llamaSlotsSnapshot{}
+	trimmed := strings.TrimSpace(body)
+	if trimmed == "" {
+		return out, fmt.Errorf("parse /slots: empty body")
+	}
+
+	var arrayPayload []map[string]any
+	if err := json.Unmarshal([]byte(trimmed), &arrayPayload); err == nil {
+		out.SlotCount = len(arrayPayload)
+		for _, slot := range arrayPayload {
+			if processing, _ := slot["is_processing"].(bool); processing {
+				out.ProcessingSlots++
+			}
+		}
+		if out.SlotCount == 0 {
+			out.SlotCount = out.ProcessingSlots
+		}
+		return out, nil
+	}
+
+	var objectPayload struct {
+		Slots     []map[string]any `json:"slots"`
+		SlotCount int              `json:"slot_count"`
+	}
+	if err := json.Unmarshal([]byte(trimmed), &objectPayload); err != nil {
+		return out, fmt.Errorf("parse /slots: %w", err)
+	}
+	out.SlotCount = objectPayload.SlotCount
+	if out.SlotCount == 0 {
+		out.SlotCount = len(objectPayload.Slots)
+	}
+	for _, slot := range objectPayload.Slots {
+		if processing, _ := slot["is_processing"].(bool); processing {
+			out.ProcessingSlots++
+		}
+	}
+	return out, nil
+}
+
+func parsePromMetricValue(body, metric string) (float64, bool) {
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
+func combineLlamaUtilization(metrics llamaMetricsSnapshot, slots llamaSlotsSnapshot) llamaMetricsSnapshot {
+	if metrics.KVCacheUsageRatio > 0 {
+		return metrics
+	}
+	if slots.SlotCount > 0 {
+		metrics.KVCacheUsageRatio = float64(slots.ProcessingSlots) / float64(slots.SlotCount)
+	}
+	return metrics
+}
+
+func statusOK(code int) error {
+	if code >= 200 && code < 300 {
+		return nil
+	}
+	return fmt.Errorf("HTTP %d", code)
+}
+
+type llamaServer struct {
+	BaseURL      string
+	DirectClient *http.Client
+	stop         func() error
+}
+
+func (s *llamaServer) Stop() {
+	if s.stop != nil {
+		_ = s.stop()
+	}
+}
+
+func mustStartLlamaServer(t *testing.T) *llamaServer {
+	t.Helper()
+
+	if os.Getenv(testutil.RecordProviderCassettesEnv) != "1" {
+		t.Skip("record-mode bootstrap is disabled outside FIZEAU_RECORD_PROVIDER_CASSETTES=1")
+	}
+
+	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
+	t.Cleanup(cancel)
+
+	ensureDockerImage(t, ctx, llamaRecordImage)
+
+	port := pickFreePort(t)
+	containerName := fmt.Sprintf("fizeau-llama-server-%d", time.Now().UnixNano())
+	cacheDir := filepath.Join(t.TempDir(), "hf-cache")
+	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
+
+	args := []string{
+		"run", "-d", "--rm",
+		"--name", containerName,
+		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
+		"-p", fmt.Sprintf("%d:8080", port),
+		"-e", "HOME=/tmp",
+		"-e", "HF_HOME=/tmp/huggingface",
+		"-e", "HF_HUB_DISABLE_TELEMETRY=1",
+		"-v", cacheDir + ":/tmp/huggingface",
+		llamaRecordImage,
+		"--hf-repo", llamaRecordRepo,
+		"--hf-file", llamaRecordFile,
+		"--chat-template", "chatml",
+		"--metrics",
+		"--parallel", "1",
+		"--host", "0.0.0.0",
+		"--port", "8080",
+		"-c", "256",
+	}
+	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
+	require.NoError(t, err, "docker run failed: %s", string(out))
+	containerID := strings.TrimSpace(string(out))
+	require.NotEmpty(t, containerID)
+
+	baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", port)
+	probeClient := &http.Client{Timeout: 5 * time.Second}
+	waitForHTTP(t, probeClient, baseURL+"/models")
+
+	return &llamaServer{
+		BaseURL:      baseURL,
+		DirectClient: &http.Client{},
+		stop: func() error {
+			stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
+			defer stopCancel()
+			_, _ = exec.CommandContext(stopCtx, "docker", "rm", "-f", containerID).CombinedOutput()
+			return nil
+		},
+	}
+}
+
+func ensureDockerImage(t *testing.T, ctx context.Context, image string) {
+	t.Helper()
+	if _, err := exec.CommandContext(ctx, "docker", "image", "inspect", image).CombinedOutput(); err == nil {
+		return
+	}
+	out, err := exec.CommandContext(ctx, "docker", "pull", image).CombinedOutput()
+	require.NoError(t, err, "docker pull failed: %s", string(out))
+}
+
+func pickFreePort(t *testing.T) int {
+	t.Helper()
+	l, err := net.Listen("tcp", "127.0.0.1:0")
+	require.NoError(t, err)
+	defer l.Close()
+	return l.Addr().(*net.TCPAddr).Port
+}
+
+func waitForHTTP(t *testing.T, client *http.Client, endpoint string) {
+	t.Helper()
+	deadline := time.Now().Add(10 * time.Minute)
+	for time.Now().Before(deadline) {
+		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
+		require.NoError(t, err)
+		resp, err := client.Do(req)
+		if err == nil {
+			_, _ = io.Copy(io.Discard, resp.Body)
+			_ = resp.Body.Close()
+			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
+				return
+			}
+		}
+		time.Sleep(2 * time.Second)
+	}
+	t.Fatalf("timed out waiting for %s", endpoint)
+}
+
+func serverRootURL(baseURL string) string {
+	trimmed := strings.TrimRight(baseURL, "/")
+	return strings.TrimSuffix(trimmed, "/v1")
+}
diff --git a/internal/provider/llamaserver/testdata/cassettes/llama_server_utilization.yaml b/internal/provider/llamaserver/testdata/cassettes/llama_server_utilization.yaml
new file mode 100644
index 0000000..8dcfa8f
--- /dev/null
+++ b/internal/provider/llamaserver/testdata/cassettes/llama_server_utilization.yaml
@@ -0,0 +1,251 @@
+---
+version: 2
+interactions:
+    - id: 0
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: 127.0.0.1:37069
+        url: http://127.0.0.1:37069/v1/models
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 701
+        body: '{"models":[{"name":"QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF","model":"QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF","modified_at":"","size":"","digest":"","type":"model","description":"","tags":[""],"capabilities":["completion"],"parameters":"","details":{"parent_model":"","format":"gguf","family":"","families":[""],"parameter_size":"","quantization_level":""}}],"object":"list","data":[{"id":"QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF","aliases":["QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF"],"tags":[],"object":"model","created":1778033309,"owned_by":"llamacpp","meta":{"vocab_type":1,"n_vocab":32000,"n_ctx_train":256,"n_embd":288,"n_params":15191712,"size":13923072}}]}'
+        headers:
+            Access-Control-Allow-Origin:
+                - ""
+            Content-Length:
+                - "701"
+            Content-Type:
+                - application/json; charset=utf-8
+            Keep-Alive:
+                - timeout=5, max=100
+            Server:
+                - llama.cpp
+        status: 200 OK
+        code: 200
+        duration: 86.833µs
+    - id: 1
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: 127.0.0.1:37069
+        url: http://127.0.0.1:37069/metrics
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 1623
+        body: |
+            # HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
+            # TYPE llamacpp:prompt_tokens_total counter
+            llamacpp:prompt_tokens_total 0
+            # HELP llamacpp:prompt_seconds_total Prompt process time
+            # TYPE llamacpp:prompt_seconds_total counter
+            llamacpp:prompt_seconds_total 0
+            # HELP llamacpp:tokens_predicted_total Number of generation tokens processed.
+            # TYPE llamacpp:tokens_predicted_total counter
+            llamacpp:tokens_predicted_total 0
+            # HELP llamacpp:tokens_predicted_seconds_total Predict process time
+            # TYPE llamacpp:tokens_predicted_seconds_total counter
+            llamacpp:tokens_predicted_seconds_total 0
+            # HELP llamacpp:n_decode_total Total number of llama_decode() calls
+            # TYPE llamacpp:n_decode_total counter
+            llamacpp:n_decode_total 0
+            # HELP llamacpp:n_tokens_max Largest observed n_tokens.
+            # TYPE llamacpp:n_tokens_max counter
+            llamacpp:n_tokens_max 0
+            # HELP llamacpp:n_busy_slots_per_decode Average number of busy slots per llama_decode() call
+            # TYPE llamacpp:n_busy_slots_per_decode counter
+            llamacpp:n_busy_slots_per_decode 0
+            # HELP llamacpp:prompt_tokens_seconds Average prompt throughput in tokens/s.
+            # TYPE llamacpp:prompt_tokens_seconds gauge
+            llamacpp:prompt_tokens_seconds 0
+            # HELP llamacpp:predicted_tokens_seconds Average generation throughput in tokens/s.
+            # TYPE llamacpp:predicted_tokens_seconds gauge
+            llamacpp:predicted_tokens_seconds 0
+            # HELP llamacpp:requests_processing Number of requests processing.
+            # TYPE llamacpp:requests_processing gauge
+            llamacpp:requests_processing 0
+            # HELP llamacpp:requests_deferred Number of requests deferred.
+            # TYPE llamacpp:requests_deferred gauge
+            llamacpp:requests_deferred 0
+        headers:
+            Access-Control-Allow-Origin:
+                - ""
+            Content-Length:
+                - "1623"
+            Content-Type:
+                - text/plain; version=0.0.4
+            Keep-Alive:
+                - timeout=5, max=100
+            Process-Start-Time-Unix:
+                - "654018500168"
+            Server:
+                - llama.cpp
+        status: 200 OK
+        code: 200
+        duration: 216.791µs
+    - id: 2
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: 127.0.0.1:37069
+        url: http://127.0.0.1:37069/slots
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 64
+        body: '[{"id":0,"n_ctx":256,"speculative":false,"is_processing":false}]'
+        headers:
+            Access-Control-Allow-Origin:
+                - ""
+            Content-Length:
+                - "64"
+            Content-Type:
+                - application/json; charset=utf-8
+            Keep-Alive:
+                - timeout=5, max=100
+            Server:
+                - llama.cpp
+        status: 200 OK
+        code: 200
+        duration: 148.542µs
+    - id: 3
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 159
+        host: 127.0.0.1:37069
+        body: '{"max_tokens":8,"messages":[{"content":"Reply with one short word.","role":"user"}],"model":"QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF","temperature":0}'
+        headers:
+            Content-Type:
+                - application/json
+        url: http://127.0.0.1:37069/v1/chat/completions
+        method: POST
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 668
+        body: '{"choices":[{"finish_reason":"length","index":0,"message":{"role":"assistant","content":"\n### Input:\n\n\n"}}],"created":1778033309,"model":"QuantFactory/tinyllama-15M-alpaca-finetuned-GGUF","system_fingerprint":"b9014-d4b0c22f9","object":"chat.completion","usage":{"completion_tokens":8,"prompt_tokens":34,"total_tokens":42,"prompt_tokens_details":{"cached_tokens":0}},"id":"chatcmpl-SOtkGKHJCWOvP067pDyn97nhBdTyILKM","timings":{"cache_n":0,"prompt_n":34,"prompt_ms":57.79,"prompt_per_token_ms":1.6997058823529412,"prompt_per_second":588.3370825402319,"predicted_n":8,"predicted_ms":183.372,"predicted_per_token_ms":22.9215,"predicted_per_second":43.62716227123006}}'
+        headers:
+            Access-Control-Allow-Origin:
+                - ""
+            Content-Length:
+                - "668"
+            Content-Type:
+                - application/json; charset=utf-8
+            Keep-Alive:
+                - timeout=5, max=100
+            Server:
+                - llama.cpp
+        status: 200 OK
+        code: 200
+        duration: 242.650937ms
+    - id: 4
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: 127.0.0.1:37069
+        url: http://127.0.0.1:37069/metrics
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 1646
+        body: |
+            # HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
+            # TYPE llamacpp:prompt_tokens_total counter
+            llamacpp:prompt_tokens_total 65
+            # HELP llamacpp:prompt_seconds_total Prompt process time
+            # TYPE llamacpp:prompt_seconds_total counter
+            llamacpp:prompt_seconds_total 0.106
+            # HELP llamacpp:tokens_predicted_total Number of generation tokens processed.
+            # TYPE llamacpp:tokens_predicted_total counter
+            llamacpp:tokens_predicted_total 8
+            # HELP llamacpp:tokens_predicted_seconds_total Predict process time
+            # TYPE llamacpp:tokens_predicted_seconds_total counter
+            llamacpp:tokens_predicted_seconds_total 0.183
+            # HELP llamacpp:n_decode_total Total number of llama_decode() calls
+            # TYPE llamacpp:n_decode_total counter
+            llamacpp:n_decode_total 11
+            # HELP llamacpp:n_tokens_max Largest observed n_tokens.
+            # TYPE llamacpp:n_tokens_max counter
+            llamacpp:n_tokens_max 43
+            # HELP llamacpp:n_busy_slots_per_decode Average number of busy slots per llama_decode() call
+            # TYPE llamacpp:n_busy_slots_per_decode counter
+            llamacpp:n_busy_slots_per_decode 1
+            # HELP llamacpp:prompt_tokens_seconds Average prompt throughput in tokens/s.
+            # TYPE llamacpp:prompt_tokens_seconds gauge
+            llamacpp:prompt_tokens_seconds 613.208
+            # HELP llamacpp:predicted_tokens_seconds Average generation throughput in tokens/s.
+            # TYPE llamacpp:predicted_tokens_seconds gauge
+            llamacpp:predicted_tokens_seconds 43.7158
+            # HELP llamacpp:requests_processing Number of requests processing.
+            # TYPE llamacpp:requests_processing gauge
+            llamacpp:requests_processing 1
+            # HELP llamacpp:requests_deferred Number of requests deferred.
+            # TYPE llamacpp:requests_deferred gauge
+            llamacpp:requests_deferred 0
+        headers:
+            Access-Control-Allow-Origin:
+                - ""
+            Content-Length:
+                - "1646"
+            Content-Type:
+                - text/plain; version=0.0.4
+            Keep-Alive:
+                - timeout=5, max=100
+            Process-Start-Time-Unix:
+                - "654018500168"
+            Server:
+                - llama.cpp
+        status: 200 OK
+        code: 200
+        duration: 30.599106ms
+    - id: 5
+      request:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 0
+        host: 127.0.0.1:37069
+        url: http://127.0.0.1:37069/slots
+        method: GET
+      response:
+        proto: HTTP/1.1
+        proto_major: 1
+        proto_minor: 1
+        content_length: 1110
+        body: '[{"id":0,"n_ctx":256,"speculative":false,"is_processing":true,"id_task":11,"params":{"seed":4294967295,"temperature":0.0,"dynatemp_range":0.0,"dynatemp_exponent":1.0,"top_k":40,"top_p":0.949999988079071,"min_p":0.05000000074505806,"top_n_sigma":-1.0,"xtc_probability":0.0,"xtc_threshold":0.10000000149011612,"typical_p":1.0,"repeat_last_n":64,"repeat_penalty":1.0,"presence_penalty":0.0,"frequency_penalty":0.0,"dry_multiplier":0.0,"dry_base":1.75,"dry_allowed_length":2,"dry_penalty_last_n":256,"mirostat":0,"mirostat_tau":5.0,"mirostat_eta":0.10000000149011612,"max_tokens":64,"n_predict":64,"n_keep":0,"n_discard":0,"ignore_eos":false,"stream":true,"n_probs":0,"min_keep":0,"chat_format":"peg-native","reasoning_format":"deepseek","reasoning_in_content":false,"generation_prompt":"<|im_start|>assistant\n","samplers":["penalties","dry","top_n_sigma","top_k","typ_p","top_p","min_p","xtc","temperature"],"speculative.type":"none","timings_per_token":false,"post_sampling_probs":false,"backend_sampling":false,"lora":[]},"next_token":[{"has_next_token":true,"has_new_line":true,"n_remain":59,"n_decoded":5}]}]'
+        headers:
+            Access-Control-Allow-Origin:
+                - ""
+            Content-Length:
+                - "1110"
+            Content-Type:
+                - application/json; charset=utf-8
+            Keep-Alive:
+                - timeout=5, max=100
+            Server:
+                - llama.cpp
+        status: 200 OK
+        code: 200
+        duration: 25.625651ms
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
