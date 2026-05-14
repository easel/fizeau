<bead-review>
  <bead id="fizeau-ff5fcc61" iter=1>
    <title>Import external benchmark resources into evidence ledger</title>
    <description>
SD-012 captures external benchmark resources for Rapid-MLX MHI, SkillsBench, SWE-bench, and HumanEval. Fizeau needs importer support for curated external rows/reports so those sources can contribute to model power and FHI without pretending every source is a live Fizeau run.

In-scope files:
- cmd/bench evidence import-external or benchmark-specific import commands
- parsers/fixtures for Rapid-MLX MHI JSON or curated Markdown/CSV, SkillsBench rows, SWE-bench leaderboard/task rows, and HumanEval pass@k/results JSONL
- docs/resources import guidance updates if needed

Out-of-scope:
- running those benchmarks live
- scraping sites in normal tests
- changing FHI formula weights

Implementation notes:
- Prefer file-based import of curated snapshots over live network scraping.
- Every imported row must record source URL, capture date, benchmark version, model_raw, harness/provider when known or `unknown` when hidden, score metric, score value, and source artifact hash.
- HumanEval should be marked as low-cost raw coding/model-power evidence, not primary harness-intelligence evidence.
    </description>
    <acceptance>
1. Fixtures for Rapid-MLX MHI, SkillsBench, SWE-bench, and HumanEval each import into valid benchmark-evidence/v1 JSONL.
2. Importers preserve unknown harness/provider explicitly rather than omitting them.
3. HumanEval records are component/model-power evidence and do not claim primary FHI coverage.
4. `go run ./cmd/bench evidence import-external --source &lt;fixture&gt; --out &lt;tmp.jsonl&gt;` or equivalent documented commands produce valid records.
5. `go test ./cmd/bench` passes.
    </acceptance>
    <labels>area:benchmark, area:evidence, area:external-benchmarks, kind:task, phase:build</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="29c8b96e2be0b58da506359d39fbcbfa120605ff">
<untrusted-data>
commit 29c8b96e2be0b58da506359d39fbcbfa120605ff
Merge: 968598f d25a876
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 19:48:37 2026 -0400

    Merge bead fizeau-ff5fcc61 attempt 20260506T234036- into master
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
