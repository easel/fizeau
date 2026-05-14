<bead-review>
  <bead id="fizeau-5e68eb28" iter=1>
    <title>Implement service-level TerminalBench harness pins</title>
    <description>
TerminalBench official comparisons need fiz to normalize Claude, Codex, pi, opencode, and provider-path targets behind one Harbor adapter. The service layer must accept an explicit harness hard pin and route execution to that harness without broadening to another harness when provider/model hints are also present.

In-scope files:
- public request/service types that carry run routing options
- internal/serviceimpl routing/execution code
- internal/harnesses metadata or selection helpers
- focused routing/service tests

Out-of-scope:
- agentcli flag parsing; create a separate CLI bead for --harness
- scripts/benchmark/harbor_agent.py
- benchmark matrix aggregation and reporting

Implementation notes:
- Treat the harness pin as a hard constraint for this run.
- Support hard pins for claude, codex, pi, opencode, and the native fiz harness identity where registered.
- If the requested harness cannot satisfy the target, return a clear configuration/setup error before falling back to a different harness.
- Preserve existing provider/model routing behavior when no harness pin is provided.
    </description>
    <acceptance>
1. Service-level tests prove requests pinned to harness=codex, harness=pi, and harness=opencode dispatch through the requested harness even when other providers/harnesses are configured.
2. A service-level test proves an unsatisfied harness pin fails with a clear configuration/setup error and does not broaden to another harness.
3. Existing provider/model routing tests still pass.
4. `go test ./internal/serviceimpl ./internal/harnesses/...` passes.
    </acceptance>
    <labels>area:routing, area:harness, kind:task, phase:build</labels>
  </bead>

  <changed-files>
    <file>internal/harnesses/registry.go</file>
    <file>internal/harnesses/registry_test.go</file>
    <file>service_execute_harness_pin_test.go</file>
  </changed-files>

  <governing>
    <ref id="terminalbench-fiz-wrapper-comparison-2026-05-06" path="docs/helix/02-design/terminalbench-fiz-wrapper-comparison-2026-05-06.md" title="TerminalBench Fiz-Wrapper Comparison">
      <content>
<untrusted-data>
---
ddx:
  id: terminalbench-fiz-wrapper-comparison-2026-05-06
  created: 2026-05-06
  extends:
    - external-benchmarks
    - routing
---

# TerminalBench Fiz-Wrapper Comparison

## Problem

The medium-model TerminalBench comparison attempted to compare native Claude
Code, native Codex, pi, opencode, and fiz by installing separate Harbor agents
for each harness. That duplicates fiz's routing and harness-normalization job
inside benchmark glue. It also creates false failures from Harbor/container
details: TerminalBench images commonly run as root, prebuilt task images may be
cross-architecture, and harness permission/auth flags differ.

The benchmark should not hand-roll any harness CLI semantics that fiz already
wraps. Fizeau owns those wrappers through its harness registry, permission
policy, model aliasing, session logging, quota/account interpretation, and
subprocess event normalization. Using fiz for pi and opencode also increases
coverage of the wrappers operators actually depend on.

## Decision

TerminalBench matrix runs must use one Harbor installed agent:
`scripts/benchmark/harbor_agent.py:FizeauAgent`.

Benchmark profiles select the execution target by passing explicit fiz hard
pins into that single agent:

- `FIZEAU_HARNESS=claude` for fiz-wrapped Claude Code.
- `FIZEAU_HARNESS=codex` for fiz-wrapped Codex.
- `FIZEAU_HARNESS=pi` for fiz-wrapped pi.
- `FIZEAU_HARNESS=opencode` for fiz-wrapped opencode.
- `FIZEAU_PROVIDER=openrouter` for fiz's provider path.
- `FIZEAU_MODEL`, `FIZEAU_MODEL_REF`, and `FIZEAU_REASONING` retain their
  existing meanings.

Raw Harbor Claude/Codex/pi/opencode adapters may remain as diagnostics, but
they are not part of the official medium-model or frontier-reference
TerminalBench comparison.

## Benchmark Lanes

The medium-model comparison uses these cells:

| Cell | Meaning |
| --- | --- |
| `fiz-harness-claude-sonnet-4-6` | Fizeau pinned to the Claude Code harness. |
| `fiz-harness-codex-gpt-5-4-mini` | Fizeau pinned to the Codex harness. |
| `fiz-harness-pi-gpt-5-4-mini` | Fizeau pinned to the pi harness. |
| `fiz-harness-opencode-gpt-5-4-mini` | Fizeau pinned to the opencode harness. |
| `fiz-openrouter-claude-sonnet-4-6` | Fizeau provider path through OpenRouter to Sonnet. |
| `fiz-openrouter-gpt-5-4-mini` | Fizeau provider path through OpenRouter to GPT mini. |

These lanes separate two questions:

1. Harness path: how well does fiz normalize subscription harnesses when the
   underlying model family is held near constant?
2. Provider path: how well do the same model families perform through fiz's
   direct provider/tool loop?

Published memos must state that identical model names across lanes are not a
pure model control. Harnesses still differ in prompt scaffolding, tool schema,
permission semantics, context handling, and quota surface.

## Native Architecture

On arm64 hosts, TerminalBench task images must be built for the native
architecture. The medium comparison defaults `HARBOR_FORCE_BUILD=1` so Harbor
does not reuse amd64 upstream images with arm64 binaries. This is a
reproducibility requirement, not an optimization.

## Invalid Run Classification

Capability aggregates must exclude runs that never reached a meaningful model
attempt. The matrix report must classify and surface these as invalid rather
than as graded failures:

- `invalid_quota` — rate limit, usage exhausted, credits exhausted, quota
  window closed.
- `invalid_auth` — missing or rejected credentials.
- `invalid_setup` — harness installation, binary architecture, permission-mode,
  or task environment failure before agent work.
- `invalid_provider` — provider transport failure before a response is
  produced.

Only verifier failures after a real agent attempt are `graded_fail`.

Invalid runs still appear in `matrix.md` with cause and log path. They are
excluded from mean reward denominators and cost/capability comparisons.

## Implementation Shape

1. The fiz CLI exposes `--harness` as a hard pin on `fiz run`, matching the
   routing docs.
2. `FizeauAgent` forwards `FIZEAU_HARNESS` into the fiz invocation and records
   the resolved harness/provider/model in its trajectory metadata.
3. Benchmark profiles encode lanes; scripts invoke only `HARNESSES=fiz`.
4. Aggregation classifies invalid runs from report fields and known log
   signatures, including Claude Code `api_error_status: 429` and
   `out_of_credits`.
5. Tests prove the official comparison script does not call raw Harbor
   Claude/Codex/pi/opencode adapters.

## Out Of Scope

- Making raw Harbor Claude/Codex/pi/opencode adapters production quality.
- Reimplementing upstream TerminalBench scoring.
- Treating OpenRouter Sonnet and Claude Code Sonnet as the same provider
  surface.
- Introducing concurrent matrix execution.
</untrusted-data>
      </content>
    </ref>
  </governing>

  <diff rev="4c10d631024100afc8528d887bd275e7f99fb265">
<untrusted-data>
diff --git a/internal/harnesses/registry.go b/internal/harnesses/registry.go
index 95a76a5..679c919 100644
--- a/internal/harnesses/registry.go
+++ b/internal/harnesses/registry.go
@@ -202,6 +202,7 @@ var builtinHarnesses = map[string]HarnessConfig{
 // fall through to a cloud harness like claude or codex.
 var harnessAliases = map[string]string{
 	"local": "agent",
+	"fiz":   "agent",
 }
 
 // ResolveHarnessAlias returns the canonical harness name for an alias,
diff --git a/internal/harnesses/registry_test.go b/internal/harnesses/registry_test.go
index 16ed153..d823348 100644
--- a/internal/harnesses/registry_test.go
+++ b/internal/harnesses/registry_test.go
@@ -105,6 +105,7 @@ func TestRegistryFirstAvailableEmbeddedFallback(t *testing.T) {
 
 func TestResolveHarnessAlias(t *testing.T) {
 	assert.Equal(t, "agent", ResolveHarnessAlias("local"))
+	assert.Equal(t, "agent", ResolveHarnessAlias("fiz"))
 	assert.Equal(t, "claude", ResolveHarnessAlias("claude"))
 	assert.Equal(t, "unknown", ResolveHarnessAlias("unknown"))
 }
diff --git a/service_execute_harness_pin_test.go b/service_execute_harness_pin_test.go
new file mode 100644
index 0000000..a470046
--- /dev/null
+++ b/service_execute_harness_pin_test.go
@@ -0,0 +1,185 @@
+package fizeau
+
+import (
+	"context"
+	"encoding/json"
+	"strings"
+	"testing"
+	"time"
+
+	"github.com/DocumentDrivenDX/fizeau/internal/harnesses"
+	"github.com/DocumentDrivenDX/fizeau/internal/serviceimpl"
+)
+
+func TestExecuteExplicitHarnessPinsDispatchRequestedRunner(t *testing.T) {
+	svc := publicRouteTraceService(&fakeServiceConfig{
+		providers: map[string]ServiceProviderEntry{
+			"anthropic":  {Type: "anthropic", APIKey: "sk-test"},
+			"openrouter": {Type: "openrouter"},
+		},
+		names:       []string{"anthropic", "openrouter"},
+		defaultName: "anthropic",
+	})
+
+	cases := []struct {
+		name           string
+		req            ServiceExecuteRequest
+		wantHarness    string
+		wantNative     bool
+		wantSubprocess bool
+	}{
+		{
+			name: "codex",
+			req: ServiceExecuteRequest{
+				Prompt:   "hello",
+				Harness:  "codex",
+				Provider: "anthropic",
+				Model:    "gpt-5.4",
+			},
+			wantHarness:    "codex",
+			wantSubprocess: true,
+		},
+		{
+			name: "pi",
+			req: ServiceExecuteRequest{
+				Prompt:   "hello",
+				Harness:  "pi",
+				Provider: "openrouter",
+				Model:    "gemini-2.5-flash",
+			},
+			wantHarness:    "pi",
+			wantSubprocess: true,
+		},
+		{
+			name: "opencode",
+			req: ServiceExecuteRequest{
+				Prompt:   "hello",
+				Harness:  "opencode",
+				Provider: "anthropic",
+				Model:    "opencode/gpt-5.4",
+			},
+			wantHarness:    "opencode",
+			wantSubprocess: true,
+		},
+		{
+			name: "fiz",
+			req: ServiceExecuteRequest{
+				Prompt:   "hello",
+				Harness:  "fiz",
+				Provider: "openrouter",
+				Model:    "gpt-5.4",
+			},
+			wantHarness: "agent",
+			wantNative:  true,
+		},
+	}
+
+	for _, tc := range cases {
+		t.Run(tc.name, func(t *testing.T) {
+			decision, err := svc.resolveExecuteRoute(tc.req)
+			if err != nil {
+				t.Fatalf("resolveExecuteRoute: %v", err)
+			}
+			if decision == nil {
+				t.Fatal("resolveExecuteRoute returned nil decision")
+			}
+			if decision.Harness != tc.wantHarness {
+				t.Fatalf("decision.Harness = %q, want %q", decision.Harness, tc.wantHarness)
+			}
+
+			var gotNative bool
+			var gotSubprocess bool
+			var gotRunner string
+			serviceimpl.DispatchExecuteRun(context.Background(), serviceimpl.ExecuteDispatchRequest{
+				Decision: serviceimpl.ExecuteRunnerDecision{
+					Harness:  decision.Harness,
+					Provider: decision.Provider,
+					Model:    decision.Model,
+				},
+				Started: time.Now(),
+			}, serviceimpl.ExecuteDispatchCallbacks{
+				RunNative: func(ctx context.Context) {
+					gotNative = true
+				},
+				RunSubprocess: func(ctx context.Context, runner harnesses.Harness) {
+					gotSubprocess = true
+					gotRunner = runner.Info().Name
+				},
+				RunVirtual: func(ctx context.Context) {
+					t.Fatal("unexpected virtual dispatch")
+				},
+				RunScript: func(ctx context.Context) {
+					t.Fatal("unexpected script dispatch")
+				},
+				IsHTTPProvider: func(string) bool {
+					return false
+				},
+				Finalize: func(harnesses.FinalData) {
+				},
+			})
+
+			if gotNative != tc.wantNative {
+				t.Fatalf("RunNative called = %v, want %v", gotNative, tc.wantNative)
+			}
+			if gotSubprocess != tc.wantSubprocess {
+				t.Fatalf("RunSubprocess called = %v, want %v", gotSubprocess, tc.wantSubprocess)
+			}
+			if tc.wantSubprocess && gotRunner != tc.wantHarness {
+				t.Fatalf("subprocess runner = %q, want %q", gotRunner, tc.wantHarness)
+			}
+		})
+	}
+}
+
+func TestExecuteExplicitHarnessPinUnknownHarnessFailsWithoutBroaderDispatch(t *testing.T) {
+	svc := publicRouteTraceService(&fakeServiceConfig{
+		providers: map[string]ServiceProviderEntry{
+			"anthropic":  {Type: "anthropic", APIKey: "sk-test"},
+			"openrouter": {Type: "openrouter"},
+		},
+		names:       []string{"anthropic", "openrouter"},
+		defaultName: "anthropic",
+	})
+
+	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
+		Prompt:   "hello",
+		Harness:  "does-not-exist",
+		Provider: "anthropic",
+		Model:    "gpt-5.4",
+	})
+	if err != nil {
+		t.Fatalf("Execute: unexpected synchronous error: %v", err)
+	}
+	final := readFinalEvent(t, ch, 5*time.Second)
+	if final.Status != "failed" {
+		t.Fatalf("final status = %q, want failed", final.Status)
+	}
+	if !strings.Contains(final.Error, "unknown harness") {
+		t.Fatalf("final error = %q, want unknown harness", final.Error)
+	}
+}
+
+func readFinalEvent(t *testing.T, ch <-chan ServiceEvent, timeout time.Duration) ServiceFinalData {
+	t.Helper()
+	deadline := time.NewTimer(timeout)
+	defer deadline.Stop()
+	for {
+		select {
+		case ev, ok := <-ch:
+			if !ok {
+				t.Fatal("Execute channel closed without final event")
+			}
+			if ev.Type != "final" {
+				continue
+			}
+			var payload ServiceFinalData
+			if err := json.Unmarshal(ev.Data, &payload); err != nil {
+				t.Fatalf("unmarshal final event: %v", err)
+			}
+			return payload
+		case <-deadline.C:
+			t.Fatalf("timed out after %s waiting for final event", timeout)
+			return ServiceFinalData{}
+		}
+	}
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
