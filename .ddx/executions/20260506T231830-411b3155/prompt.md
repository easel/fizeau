<bead-review>
  <bead id="fizeau-2c59b948" iter=1>
    <title>Import TerminalBench matrices into benchmark evidence ledger</title>
    <description>
TerminalBench is the first high-value FHI benchmark. Matrix outputs must import into the benchmark evidence ledger with task-level rows where possible and with links to Fizeau session logs, Harbor trajectories, verifier rewards, invalid classifications, and matrix metadata. This importer is what enables claims like "fiz native with Opus scores 81 on TerminalBench".

In-scope files:
- cmd/bench evidence import-terminalbench command or equivalent importer
- cmd/bench matrix/matrix-aggregate parsing helpers if needed
- test fixtures for matrix.json, matrix.md-adjacent metadata, session log paths, trajectory paths, reward files, and invalid cells

Out-of-scope:
- changing Harbor or TerminalBench scoring
- running live benchmarks
- FHI formula or claim generation

Implementation notes:
- Produce atomic task-level records when matrix.json contains per-task data.
- Preserve matrix-level aggregate records only when task-level data is unavailable.
- Populate benchmark version/dataset/subset/scorer, subject model/harness/provider, Fizeau version/git commit, harness wrapper/version, runtime metadata, session log path/hash, trajectory path/hash, and invalid-run fields.
    </description>
    <acceptance>
1. A TerminalBench matrix fixture imports into benchmark-evidence/v1 JSONL with one record per task/rep/cell when task-level data exists.
2. Imported records include Fizeau commit/version, harness pin, provider, model raw/canonical where known, benchmark dataset/subset, score/reward, final_status, invalid_class when applicable, and session/trajectory artifact hashes.
3. Invalid quota/auth/setup/provider cells are imported with denominator exclusion metadata.
4. `go run ./cmd/bench evidence import-terminalbench --matrix &lt;fixture-dir&gt; --out &lt;tmp.jsonl&gt;` produces valid records.
5. `go test ./cmd/bench` passes.
    </acceptance>
    <labels>area:benchmark, area:terminalbench, area:evidence, kind:task, phase:build</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="be8332c7c7d60c9098ec35edbe38fae084f091b2">
<untrusted-data>
commit be8332c7c7d60c9098ec35edbe38fae084f091b2
Merge: cceaf1d 1c8ddd3
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 19:18:25 2026 -0400

    Merge bead fizeau-2c59b948 attempt 20260506T230812- into master
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
