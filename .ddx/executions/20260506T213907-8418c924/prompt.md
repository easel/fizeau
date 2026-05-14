<bead-review>
  <bead id="fizeau-dbc21cce" iter=1>
    <title>Classify invalid TerminalBench runs separately from graded failures</title>
    <description>
The medium comparison must not treat provider quota, authentication, setup, architecture, or provider routing failures as model capability failures. Recent Claude canaries produced 429/out_of_credits failures; pi and opencode can similarly fail from wrapper setup, local binary/account state, or provider/auth surfaces before a meaningful model attempt. Those should be reported as invalid cells, not failed benchmark attempts. Only verifier failures after a real agent attempt should count as graded failures.

In-scope files:
- cmd/bench matrix aggregation/reporting code
- benchmark result parsing helpers
- fixtures for representative invalid logs/results across Claude, Codex, pi, and opencode lanes
- matrix Markdown/JSON output tests

Out-of-scope:
- changing Harbor or TerminalBench verifier behavior
- changing fiz session-log event schema unless needed for existing metadata consumption
- benchmark profile selection

Implementation notes:
- Add stable invalid classes: `invalid_quota`, `invalid_auth`, `invalid_setup`, and `invalid_provider`.
- Classify Claude 429/out_of_credits as `invalid_quota`.
- Classify pi/opencode missing binary, account, permission, or wrapper startup failures before agent progress as `invalid_setup` or `invalid_auth` as appropriate.
- Classify Docker/task image/native arch/setup failures before agent execution as `invalid_setup`.
- Exclude invalid cells from pass-rate or mean-reward denominators while still displaying them prominently.
    </description>
    <acceptance>
1. A fixture containing Claude 429/out_of_credits is classified as `invalid_quota`.
2. Fixtures containing pi/opencode missing binary, account, or wrapper startup failure before agent progress are classified as `invalid_setup` or `invalid_auth` as appropriate.
3. A fixture containing setup/native-arch/task-image failure before agent progress is classified as `invalid_setup`.
4. A fixture containing a verifier failure after an agent attempt remains a graded failure, not invalid.
5. Matrix Markdown and JSON outputs include invalid counts/classes and exclude invalid cells from capability pass-rate denominators.
6. `go test ./cmd/bench` passes.
    </acceptance>
    <labels>area:benchmark, area:reporting, kind:task, phase:build</labels>
  </bead>

  <changed-files>
    <file>.ddx/executions/20260506T213721-bbfcd208/no_changes_rationale.txt</file>
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

  <diff rev="50ba08b17a84aed3ea879191280416ebc8171cd2">
<untrusted-data>
diff --git a/.ddx/executions/20260506T213721-bbfcd208/no_changes_rationale.txt b/.ddx/executions/20260506T213721-bbfcd208/no_changes_rationale.txt
new file mode 100644
index 0000000..28707ea
--- /dev/null
+++ b/.ddx/executions/20260506T213721-bbfcd208/no_changes_rationale.txt
@@ -0,0 +1,6 @@
+verification_command: go test ./cmd/bench
+evidence:
+  - cmd/bench/matrix_invalid.go classifies invalid_quota, invalid_auth, invalid_setup, and invalid_provider from report fields and log text.
+  - cmd/bench/matrix.go excludes invalid runs from reward and pass-rate denominators in summarizeMatrixCells, writeMarkdownRewardTable, and writeMarkdownPassCountTable.
+  - cmd/bench/matrix_aggregate.go surfaces InvalidRuns and InvalidByClass in matrix.json and renders an Invalid runs section in matrix.md.
+  - cmd/bench/matrix_invalid_test.go covers Claude quota, pi/opencode setup/auth cases, setup/native-arch failure, verifier failure after attempt, and invalid-denominator aggregation.
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
