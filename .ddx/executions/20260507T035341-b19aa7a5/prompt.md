<bead-review>
  <bead id="fizeau-66c928b3" iter=1>
    <title>Run and summarize the fiz-wrapper TerminalBench canary matrix</title>
    <description>
Once the service pin, CLI flag, FizeauAgent forwarding, official profiles, invalid classification, and reproducibility docs are in place, run the canary matrix to prove the process works end to end. This bead is for validation and summary, not for adding more implementation except tiny fixes needed to make the documented command run as specified. The canary must include fiz-wrapped Claude, Codex, pi, and opencode lanes plus the fiz OpenRouter lanes.

In-scope files:
- benchmark output artifacts produced by the documented canary command
- docs/research summary memo for the canary result
- tiny follow-up fixes directly required to make the documented command execute

Out-of-scope:
- broad benchmark subset redesign
- changing model choices or official lane definitions
- raw Claude/Codex/pi/opencode Harbor adapter productionization

Implementation notes:
- Run the documented canary command by itself, not concurrently with unrelated pi/fiz/opencode runs on the same provider.
- Confirm each official lane is present in the output.
- Confirm invalid quota/auth/setup/provider cells are classified separately from graded failures.
- Summarize any observed tool-call/session-log differences only from captured artifacts.
    </description>
    <acceptance>
1. The documented canary command completes or exits with only classified invalid cells; record the exact command and exit status in a docs/research memo.
2. The canary output contains all six official lanes: fiz-wrapped Claude, fiz-wrapped Codex, fiz-wrapped pi, fiz-wrapped opencode, fiz OpenRouter Sonnet, and fiz OpenRouter GPT.
3. Any provider quota/auth/setup failures are shown as invalid classes rather than graded failures.
4. The docs/research memo summarizes pass/fail/invalid counts and any tool-call/session-log observations supported by artifacts.
5. `go test ./cmd/bench ./agentcli ./internal/harnesses/...` passes after any tiny fixes made during validation.
    </acceptance>
    <labels>area:benchmark, area:validation, kind:task, phase:verify</labels>
  </bead>

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

## Reproducible Command

Use the single operator entrypoint documented in
[scripts/benchmark/README.md](../../../scripts/benchmark/README.md#medium-model-fiz-wrapper-comparison):

```bash
OPENROUTER_API_KEY=sk-or-... \
  scripts/benchmark/run_medium_model_terminalbench_comparison.sh
```

That wrapper is the official medium-model comparison command. It runs the six
official `fiz-*` lanes through one Harbor-installed `FizeauAgent`, writes the
matrix artifacts under `benchmark-results/matrix-medium-model-<tier>-<UTC>/`,
and keeps raw Harbor Claude/Codex/pi/opencode adapters as diagnostics only.

Invalid cells are reported as `invalid_quota`, `invalid_auth`,
`invalid_setup`, or `invalid_provider`. They remain visible in `matrix.md`
with their cause and log path, but they are excluded from mean reward and
denominator calculations.

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

  <diff rev="712aae7f95b8eb12d923403761c41c552475e6fe">
<untrusted-data>
commit 712aae7f95b8eb12d923403761c41c552475e6fe
Merge: 23f4996b f8e300ec
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 23:53:38 2026 -0400

    Merge bead fizeau-66c928b3 attempt 20260507T035035- into master
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
