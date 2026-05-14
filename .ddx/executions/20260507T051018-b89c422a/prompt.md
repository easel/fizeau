<bead-review>
  <bead id="fizeau-85d36567" iter=1>
    <title>Implement resumable TerminalBench 2.1 phase runner</title>
    <description>
Implement the runner support needed for the TerminalBench 2.1 sweep plan. The current matrix command has tuple-level reports, --resume, --force-rerun, and --jobs, but the sweep needs higher-level phase orchestration and provider-aware parallelism.

Required behavior:
- Run named phases independently: canary, local-qwen, sonnet-comparison, gpt-comparison, or all.
- Use the official TerminalBench 2.1 dataset/task source selected by the migration bead.
- Resume at cell granularity using existing report.json/matrix.json artifacts.
- Run cells in parallel where safe. Managed provider lanes may use jobs &gt; 1 subject to budget/rate caps. Local model providers must be serialized or capped by resource_group/server/base_url so two cells do not overload the same local model server.
- Support dry-run/plan output that prints the exact cells, resource groups, concurrency caps, and commands without launching Harbor.
- Preserve invalid-run classification and matrix aggregation/import compatibility.

Likely in-scope files:
- scripts/benchmark/run_terminalbench_2_1_sweep.sh or equivalent orchestrator
- cmd/bench/matrix.go if provider-aware scheduling belongs in the Go runner
- scripts/benchmark/profiles/*.yaml and/or a lane config under scripts/benchmark/
- focused tests for planning/resume/resource-group behavior

Out of scope:
- running the full paid sweep
- broad benchmark evidence schema redesign
- changing historical TB-2.0 fixtures
    </description>
    <acceptance>
1. A documented command can dry-run the full 2.1 sweep and prints phases, lane ids, comparison_group ids, task count, reps, resource groups, max parallelism, and output directory without invoking Harbor.
2. Canary, local-qwen, sonnet-comparison, and gpt-comparison can each be invoked independently and all share the same resume/output layout.
3. Resume mode skips terminal completed cells and reruns only missing/nonterminal cells unless force-rerun is set; tests or fixtures prove this behavior.
4. Resource-group scheduling prevents more than the configured concurrency for each local provider endpoint while still allowing independent managed-provider/resource groups to run in parallel.
5. Budget/per-run budget/rate-limit knobs are preserved or documented for managed lanes.
6. `bash -n` passes for changed shell scripts.
7. `go test ./cmd/bench` passes if Go runner code changes; otherwise focused script/Python tests cover the orchestrator planning logic.
8. The runner writes enough metadata for evidence import: benchmark dataset/version, subset/manifest id, phase, lane id, comparison_group, comparison_type, model_family, exact model id, quant/config label, profile id, harness pin/provider/model/base_url, resource_group, reps, and command.
9. Matrix summary output supports the model-selection calculus by exposing pass/fail/invalid counts, wall time, token counts, cost, effective cost per valid run, effective cost per pass when computable, and invalid-class denominators per lane/comparison group.
    </acceptance>
    <labels>area:benchmark, area:terminalbench, area:tooling, kind:task, phase:build</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="ee378c4f4f382e11fc2c30ea2bce426fa381c18e">
<untrusted-data>
commit ee378c4f4f382e11fc2c30ea2bce426fa381c18e
Merge: 009833fa 553231cd
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Thu May 7 01:10:14 2026 -0400

    Merge bead fizeau-85d36567 attempt 20260507T045229- into master
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
