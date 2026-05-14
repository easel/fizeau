<bead-review>
  <bead id="fizeau-5d4b5f68" iter=1>
    <title>Add go-vcr provider cassette harness</title>
    <description>
Add shared test infrastructure for provider HTTP record/replay using the established go-vcr library. In-scope files: a small helper package under internal/provider or internal/provider/testutil, go.mod/go.sum dependency updates, and tests proving replay-only behavior. The helper should default to replay-only mode, switch to record mode only with FIZEAU_RECORD_PROVIDER_CASSETTES=1, redact Authorization/API-key headers before saving, provide stable request matching suitable for /v1/models, /metrics, /slots, and chat completions, and fail when a replay interaction is missing. Out of scope: actual vLLM or llama-server cassettes, server bootstrapping, routing changes, and provider-specific parsing.
    </description>
    <acceptance>
1. go test ./internal/provider/... -run 'VCR|Cassette|Replay' passes. 2. go.mod declares gopkg.in/dnaeon/go-vcr.v4 or a current maintained go-vcr import as a direct test dependency. 3. Replay mode is default without network access and fails on missing cassette interactions. 4. Record mode is gated by FIZEAU_RECORD_PROVIDER_CASSETTES=1. 5. Saved cassettes do not contain Authorization headers or API keys.
    </acceptance>
    <labels>area:test, kind:infra</labels>
  </bead>

  <changed-files>
    <file>go.mod</file>
    <file>go.sum</file>
    <file>internal/provider/testutil/vcr.go</file>
    <file>internal/provider/testutil/vcr_test.go</file>
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

  <diff rev="5f363e3a005752d2844a96709316d583f8c40430">
<untrusted-data>
diff --git a/go.mod b/go.mod
index daa06f8..d99ae64 100644
--- a/go.mod
+++ b/go.mod
@@ -14,6 +14,7 @@ require (
 	go.opentelemetry.io/otel/sdk/metric v1.43.0
 	go.opentelemetry.io/otel/trace v1.43.0
 	golang.org/x/sync v0.16.0
+	gopkg.in/dnaeon/go-vcr.v4 v4.0.6
 	gopkg.in/yaml.v3 v3.0.1
 	pgregory.net/rapid v1.2.0
 )
@@ -33,5 +34,6 @@ require (
 	github.com/tidwall/pretty v1.2.1 // indirect
 	github.com/tidwall/sjson v1.2.5 // indirect
 	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
+	go.yaml.in/yaml/v4 v4.0.0-rc.3 // indirect
 	golang.org/x/sys v0.42.0 // indirect
 )
diff --git a/go.sum b/go.sum
index 12eed7f..f08d2e2 100644
--- a/go.sum
+++ b/go.sum
@@ -64,6 +64,8 @@ go.opentelemetry.io/otel/trace v1.43.0/go.mod h1:/QJhyVBUUswCphDVxq+8mld+AvhXZLh
 go.uber.org/goleak v1.3.0 h1:2K3zAYmnTNqV73imy9J1T3WC+gmCePx2hEGkimedGto=
 go.uber.org/goleak v1.3.0/go.mod h1:CoHD4mav9JJNrW/WLlf7HGZPjdw8EucARQHekz1X6bE=
 go.yaml.in/yaml/v3 v3.0.4/go.mod h1:DhzuOOF2ATzADvBadXxruRBLzYTpT36CKvDb3+aBEFg=
+go.yaml.in/yaml/v4 v4.0.0-rc.3 h1:3h1fjsh1CTAPjW7q/EMe+C8shx5d8ctzZTrLcs/j8Go=
+go.yaml.in/yaml/v4 v4.0.0-rc.3/go.mod h1:aZqd9kCMsGL7AuUv/m/PvWLdg5sjJsZ4oHDEnfPPfY0=
 golang.org/x/sync v0.16.0 h1:ycBJEhp9p4vXvUZNszeOq0kGTPghopOL8q0fq3vstxw=
 golang.org/x/sync v0.16.0/go.mod h1:1dzgHSNfp02xaA81J2MS99Qcpr2w7fw1gpm99rleRqA=
 golang.org/x/sys v0.42.0 h1:omrd2nAlyT5ESRdCLYdm3+fMfNFE/+Rf4bDIQImRJeo=
@@ -71,6 +73,8 @@ golang.org/x/sys v0.42.0/go.mod h1:4GL1E5IUh+htKOUEOaiffhrAeqysfVGipDYzABqnCmw=
 gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405/go.mod h1:Co6ibVJAznAaIkqp8huTwlJQCZ016jof/cbN4VW5Yz0=
 gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c h1:Hei/4ADfdWqJk1ZMxUNpqntNwaWcugrBjAiHlqqRiVk=
 gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c/go.mod h1:JHkPIbrfpd72SG/EVd6muEfDQjcINNoR0C8j2r3qZ4Q=
+gopkg.in/dnaeon/go-vcr.v4 v4.0.6 h1:PiJkrakkmzc5s7EfBnZOnyiLwi7o7A9fwPzN0X2uwe0=
+gopkg.in/dnaeon/go-vcr.v4 v4.0.6/go.mod h1:sbq5oMEcM4PXngbcNbHhzfCP9OdZodLhrbRYoyg09HY=
 gopkg.in/yaml.v2 v2.2.8 h1:obN1ZagJSUGI0Ek/LBmuj4SNLPfIny3KsKFopxRdj10=
 gopkg.in/yaml.v2 v2.2.8/go.mod h1:hI93XBmqTisBFMUTm0b8Fm+jr3Dg1NNxqwp+5A1VGuI=
 gopkg.in/yaml.v3 v3.0.1 h1:fxVm/GzAzEWqLHuvctI91KS9hhNmmWOoWu0XTYJS7CA=
diff --git a/internal/provider/testutil/vcr.go b/internal/provider/testutil/vcr.go
new file mode 100644
index 0000000..6ef5d80
--- /dev/null
+++ b/internal/provider/testutil/vcr.go
@@ -0,0 +1,155 @@
+package testutil
+
+import (
+	"bytes"
+	"io"
+	"net/http"
+	"net/url"
+	"os"
+	"path/filepath"
+	"strings"
+	"testing"
+
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
+)
+
+const RecordProviderCassettesEnv = "FIZEAU_RECORD_PROVIDER_CASSETTES"
+
+// ModeForEnvironment selects the cassette mode for provider tests.
+//
+// Replay-only is the default so tests stay offline unless an explicit record
+// run is requested.
+func ModeForEnvironment() recorder.Mode {
+	if os.Getenv(RecordProviderCassettesEnv) == "1" {
+		return recorder.ModeRecordOnly
+	}
+	return recorder.ModeReplayOnly
+}
+
+// NewRecorder creates a recorder rooted at cassettePath.
+//
+// The recorder defaults to replay-only mode, uses a stable matcher that ignores
+// host/port churn and common auth/header noise, and redacts secret headers
+// before saving.
+func NewRecorder(cassettePath string) (*recorder.Recorder, error) {
+	return recorder.New(
+		cassettePath,
+		recorder.WithMode(ModeForEnvironment()),
+		recorder.WithMatcher(StableMatcher()),
+		recorder.WithHook(RedactSensitiveHeaders, recorder.BeforeSaveHook),
+	)
+}
+
+// UseDefaultTransport installs the recorder as the process-wide default
+// transport for the duration of the test.
+func UseDefaultTransport(t testing.TB, rec *recorder.Recorder) {
+	t.Helper()
+	old := http.DefaultTransport
+	http.DefaultTransport = rec
+	t.Cleanup(func() {
+		http.DefaultTransport = old
+	})
+}
+
+// StableMatcher compares requests by method, normalized URL, headers, and body.
+//
+// The URL normalization intentionally ignores scheme/host/port so the same
+// cassette can be replayed against different local servers while still
+// distinguishing /v1/models, /metrics, /slots, and /v1/chat/completions.
+func StableMatcher() recorder.MatcherFunc {
+	return func(r *http.Request, i cassette.Request) bool {
+		clone, rawBody, err := cloneRequest(r)
+		if err != nil {
+			return false
+		}
+
+		clone.URL = normalizeURL(clone.URL)
+		clone.Host = ""
+
+		i.URL = normalizeURLString(i.URL)
+		i.Host = ""
+
+		clone.Body = io.NopCloser(bytes.NewReader(rawBody))
+		return cassette.NewDefaultMatcher(
+			cassette.WithIgnoreAuthorization(),
+			cassette.WithIgnoreUserAgent(),
+			cassette.WithIgnoreHeaders(
+				"Accept-Encoding",
+				"Api-Key",
+				"X-Api-Key",
+				"Openai-Api-Key",
+				"Anthropic-Api-Key",
+			),
+		)(clone, i)
+	}
+}
+
+// RedactSensitiveHeaders removes auth/API key headers before a cassette is
+// written to disk.
+func RedactSensitiveHeaders(i *cassette.Interaction) error {
+	for key := range i.Request.Headers {
+		if isSecretHeader(key) {
+			delete(i.Request.Headers, key)
+		}
+	}
+	for key := range i.Response.Headers {
+		if isSecretHeader(key) {
+			delete(i.Response.Headers, key)
+		}
+	}
+	return nil
+}
+
+func cloneRequest(r *http.Request) (*http.Request, []byte, error) {
+	clone := r.Clone(r.Context())
+	var body []byte
+	if r.Body != nil && r.Body != http.NoBody {
+		var err error
+		body, err = io.ReadAll(r.Body)
+		if err != nil {
+			return nil, nil, err
+		}
+	}
+	r.Body = io.NopCloser(bytes.NewReader(body))
+	clone.Body = io.NopCloser(bytes.NewReader(body))
+	return clone, body, nil
+}
+
+func normalizeURL(raw *url.URL) *url.URL {
+	if raw == nil {
+		return nil
+	}
+	clone := *raw
+	clone.Scheme = "http"
+	clone.Host = "vcr.local"
+	clone.Fragment = ""
+	clone.RawQuery = clone.Query().Encode()
+	return &clone
+}
+
+func normalizeURLString(raw string) string {
+	parsed, err := url.Parse(raw)
+	if err != nil {
+		return raw
+	}
+	normalized := normalizeURL(parsed)
+	if normalized == nil {
+		return raw
+	}
+	return normalized.String()
+}
+
+func isSecretHeader(name string) bool {
+	switch strings.ToLower(strings.TrimSpace(name)) {
+	case "authorization", "api-key", "x-api-key", "openai-api-key", "anthropic-api-key":
+		return true
+	}
+	return strings.Contains(strings.ToLower(name), "api-key")
+}
+
+// CassettePath joins a base directory and cassette name using the file layout
+// go-vcr expects.
+func CassettePath(dir, name string) string {
+	return filepath.Join(dir, name)
+}
diff --git a/internal/provider/testutil/vcr_test.go b/internal/provider/testutil/vcr_test.go
new file mode 100644
index 0000000..e51cbae
--- /dev/null
+++ b/internal/provider/testutil/vcr_test.go
@@ -0,0 +1,146 @@
+package testutil
+
+import (
+	"bytes"
+	"errors"
+	"io"
+	"net/http"
+	"net/http/httptest"
+	"os"
+	"path/filepath"
+	"testing"
+
+	"github.com/stretchr/testify/assert"
+	"github.com/stretchr/testify/require"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
+	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
+)
+
+func TestVCR_ModeForEnvironment(t *testing.T) {
+	t.Setenv(RecordProviderCassettesEnv, "")
+	assert.Equal(t, recorder.ModeReplayOnly, ModeForEnvironment())
+
+	t.Setenv(RecordProviderCassettesEnv, "1")
+	assert.Equal(t, recorder.ModeRecordOnly, ModeForEnvironment())
+}
+
+func TestVCR_ReplayOnlyStableMatcherAndMissingReplayFails(t *testing.T) {
+	cassettePath := filepath.Join(t.TempDir(), "provider-http")
+
+	t.Setenv(RecordProviderCassettesEnv, "1")
+	recorded, err := NewRecorder(cassettePath)
+	require.NoError(t, err)
+	require.Equal(t, recorder.ModeRecordOnly, recorded.Mode())
+
+	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		switch r.URL.Path {
+		case "/v1/models":
+			w.Header().Set("Content-Type", "application/json")
+			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model-a","object":"model"}]}`)
+		case "/metrics":
+			w.Header().Set("Content-Type", "text/plain")
+			_, _ = io.WriteString(w, "requests_running 1\n")
+		case "/slots":
+			w.Header().Set("Content-Type", "application/json")
+			_, _ = io.WriteString(w, `{"slots":[{"is_processing":true}],"slot_count":1}`)
+		case "/v1/chat/completions":
+			raw, err := io.ReadAll(r.Body)
+			require.NoError(t, err)
+			require.Contains(t, string(raw), `"model":"model-a"`)
+			w.Header().Set("Content-Type", "application/json")
+			_, _ = io.WriteString(w, `{"id":"chatcmpl-1","model":"model-a","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
+		default:
+			t.Fatalf("unexpected path recorded: %s", r.URL.Path)
+		}
+	}))
+	defer server.Close()
+
+	recordClient := recorded.GetDefaultClient()
+	doRecordRequest(t, recordClient, http.MethodGet, server.URL+"/v1/models", nil)
+	doRecordRequest(t, recordClient, http.MethodGet, server.URL+"/metrics", nil)
+	doRecordRequest(t, recordClient, http.MethodGet, server.URL+"/slots", nil)
+	doRecordRequest(t, recordClient, http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`))
+
+	require.NoError(t, recorded.Stop())
+
+	t.Setenv(RecordProviderCassettesEnv, "")
+	replayed, err := NewRecorder(cassettePath)
+	require.NoError(t, err)
+	require.Equal(t, recorder.ModeReplayOnly, replayed.Mode())
+
+	replayClient := replayed.GetDefaultClient()
+	doReplayRequest(t, replayClient, http.MethodGet, "http://replay.invalid/v1/models", nil, true)
+	doReplayRequest(t, replayClient, http.MethodGet, "http://replay.invalid/metrics", nil, true)
+	doReplayRequest(t, replayClient, http.MethodGet, "http://replay.invalid/slots", nil, true)
+	doReplayRequest(t, replayClient, http.MethodPost, "http://replay.invalid/v1/chat/completions", bytes.NewBufferString(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`), true)
+
+	_, err = doReplayRequest(t, replayClient, http.MethodGet, "http://replay.invalid/v1/does-not-exist", nil, false)
+	require.Error(t, err)
+	require.True(t, errors.Is(err, cassette.ErrInteractionNotFound), "expected missing replay interaction error, got %v", err)
+}
+
+func TestVCR_RedactsSecretHeadersBeforeSave(t *testing.T) {
+	cassettePath := filepath.Join(t.TempDir(), "secrets")
+
+	t.Setenv(RecordProviderCassettesEnv, "1")
+	rec, err := NewRecorder(cassettePath)
+	require.NoError(t, err)
+
+	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.Header().Set("Content-Type", "application/json")
+		_, _ = io.WriteString(w, `{"ok":true}`)
+	}))
+	defer server.Close()
+
+	client := rec.GetDefaultClient()
+	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
+	require.NoError(t, err)
+	req.Header.Set("Authorization", "Bearer secret-token")
+	req.Header.Set("X-API-Key", "secret-key")
+	req.Header.Set("OpenAI-API-Key", "openai-secret")
+
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	_, _ = io.ReadAll(resp.Body)
+	_ = resp.Body.Close()
+
+	require.NoError(t, rec.Stop())
+
+	raw, err := os.ReadFile(cassettePath + ".yaml")
+	require.NoError(t, err)
+	assert.NotContains(t, string(raw), "Authorization")
+	assert.NotContains(t, string(raw), "secret-token")
+	assert.NotContains(t, string(raw), "X-API-Key")
+	assert.NotContains(t, string(raw), "secret-key")
+	assert.NotContains(t, string(raw), "OpenAI-API-Key")
+	assert.NotContains(t, string(raw), "openai-secret")
+}
+
+func doRecordRequest(t *testing.T, client *http.Client, method, url string, body io.Reader) {
+	t.Helper()
+	req, err := http.NewRequest(method, url, body)
+	require.NoError(t, err)
+	req.Header.Set("Accept", "application/json")
+	req.Header.Set("Authorization", "Bearer record-secret")
+	resp, err := client.Do(req)
+	require.NoError(t, err)
+	_, _ = io.ReadAll(resp.Body)
+	_ = resp.Body.Close()
+}
+
+func doReplayRequest(t *testing.T, client *http.Client, method, url string, body io.Reader, wantOK bool) (*http.Response, error) {
+	t.Helper()
+	req, err := http.NewRequest(method, url, body)
+	require.NoError(t, err)
+	req.Header.Set("Accept", "application/json")
+	req.Header.Set("Authorization", "Bearer replay-secret")
+	resp, err := client.Do(req)
+	if !wantOK {
+		return resp, err
+	}
+	require.NoError(t, err)
+	require.NotNil(t, resp)
+	_, _ = io.ReadAll(resp.Body)
+	_ = resp.Body.Close()
+	return resp, nil
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
