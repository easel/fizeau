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
    <file>service_harness_dispatch_test.go</file>
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

  <diff rev="491b9e65e40ffa63637874ec7b5cfb8271a10123">
<untrusted-data>
diff --git a/service_harness_dispatch_test.go b/service_harness_dispatch_test.go
index 087b355..8fc0c1e 100644
--- a/service_harness_dispatch_test.go
+++ b/service_harness_dispatch_test.go
@@ -22,6 +22,12 @@ cat <<'EOF'
 {"type":"message","role":"assistant","content":"gemini service response","delta":true}
 {"type":"result","status":"success","stats":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}
 EOF
+`)
+	writeFakeHarness(t, binDir, "codex", `#!/bin/sh
+cat <<'EOF'
+{"type":"output","item":{"type":"agent_message","text":"codex service response"}}
+{"type":"turn.completed","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}
+EOF
 `)
 	writeFakeHarness(t, binDir, "opencode", `#!/bin/sh
 cat <<'EOF'
@@ -47,6 +53,7 @@ EOF
 		reason  fizeau.Reasoning
 		text    string
 	}{
+		{harness: "codex", model: "gpt-5.4", reason: fizeau.ReasoningLow, text: "codex service response"},
 		{harness: "gemini", model: "gemini-2.5-flash", text: "gemini service response"},
 		{harness: "opencode", model: "opencode/gpt-5.4", reason: fizeau.ReasoningLow, text: "opencode service response"},
 		{harness: "pi", model: "gemini-2.5-flash", reason: fizeau.ReasoningLow, text: "pi service response"},
@@ -96,7 +103,8 @@ func TestExecute_SubprocessHarnessMissingBinaryFinalFailure(t *testing.T) {
 
 	ch, err := svc.Execute(ctx, fizeau.ServiceExecuteRequest{
 		Prompt:  "hello",
-		Harness: "gemini",
+		Harness: "codex",
+		Model:   "gpt-5.4",
 	})
 	if err != nil {
 		t.Fatalf("Execute: %v", err)
@@ -108,9 +116,12 @@ func TestExecute_SubprocessHarnessMissingBinaryFinalFailure(t *testing.T) {
 	if result.FinalStatus != "failed" {
 		t.Fatalf("FinalStatus: got %q", result.FinalStatus)
 	}
-	if !strings.Contains(result.TerminalError, "gemini binary not found") {
+	if !strings.Contains(result.TerminalError, "codex binary not found") {
 		t.Fatalf("TerminalError: got %q", result.TerminalError)
 	}
+	if result.RoutingActual == nil || result.RoutingActual.Harness != "codex" {
+		t.Fatalf("RoutingActual: got %#v, want codex", result.RoutingActual)
+	}
 }
 
 func TestExecute_DispatchesVirtualAndScriptHarnesses(t *testing.T) {
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
