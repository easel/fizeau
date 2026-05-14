<bead-review>
  <bead id="fizeau-fde8c9bd" iter=1>
    <title>Import beadbench reports into benchmark evidence ledger</title>
    <description>
beadbench is the product-native benchmark for DDx/Fizeau execute-bead work. Its reports should become first-class evidence records so FHI reflects real implementation-loop behavior, not only external benchmark scores.

In-scope files:
- scripts/beadbench/run_beadbench.py report schema/metadata if needed
- cmd/bench evidence import-beadbench command or equivalent importer
- fixtures under scripts/beadbench or cmd/bench testdata

Out-of-scope:
- changing beadbench task selection or running paid beadbench jobs
- importing TerminalBench matrices
- FHI formula or claim generation

Implementation notes:
- Import per-task/per-arm records from beadbench report.json.
- Preserve DDx bead id, project/repo, harness/model/provider arm, Fizeau version if present, execution outcome, review outcome if present, runtime/cost/tool-call fields when present, and session/evidence artifact paths/hashes.
- Distinguish implementation failures from setup/auth/provider invalids where the report has enough signal.
    </description>
    <acceptance>
1. A beadbench report fixture imports into benchmark-evidence/v1 JSONL with one record per task/arm/run where available.
2. Imported records preserve bead id, arm id, model/harness/provider, task category/capability, execution outcome, runtime/cost/tool-call fields when present, and session/evidence artifact references.
3. Setup/auth/provider invalid outcomes are represented with invalid_class and denominator metadata when identifiable.
4. `go run ./cmd/bench evidence import-beadbench --report &lt;fixture.json&gt; --out &lt;tmp.jsonl&gt;` produces valid records, or an equivalent documented command does.
5. `go test ./cmd/bench` and `python3 scripts/beadbench/test_run_beadbench.py` pass.
    </acceptance>
    <labels>area:benchmark, area:beadbench, area:evidence, kind:task, phase:build</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="f4c3c6819427336caed3ecebee2335f428a0b2f1">
<untrusted-data>
commit f4c3c6819427336caed3ecebee2335f428a0b2f1
Merge: 971cf51 f913f8b
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 19:30:42 2026 -0400

    Merge bead fizeau-fde8c9bd attempt 20260506T232211- into master
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
