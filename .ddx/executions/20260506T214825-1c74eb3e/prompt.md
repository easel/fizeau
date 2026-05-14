<bead-review>
  <bead id="fizeau-395d124d" iter=1>
    <title>Add benchmark evidence ledger writer CLI</title>
    <description>
Benchmark importers need a common append-only writer so evidence records are validated, assigned stable record IDs, and written consistently. This slice owns the generic ledger mechanics, not any benchmark-specific parser.

In-scope files:
- cmd/bench evidence subcommand or equivalent cmd/bench importer entrypoint
- Go package/helper for validating records against scripts/benchmark/benchmark-evidence.schema.json or an equivalent typed representation
- tests and fixtures for append, duplicate handling, source hashes, and invalid record rejection

Out-of-scope:
- TerminalBench, beadbench, SkillsBench, SWE-bench, HumanEval, or MHI parsing
- FHI formula or claim generation
- changing existing matrix runner behavior

Implementation notes:
- Provide a command shape such as `go run ./cmd/bench evidence validate &lt;file&gt;` and `go run ./cmd/bench evidence append --in &lt;records.jsonl&gt; --ledger &lt;ledger.jsonl&gt;`.
- Records should get deterministic content-derived `record_id` values when absent.
- The writer should be append-only and should report duplicates without silently mutating existing records.
    </description>
    <acceptance>
1. `go run ./cmd/bench evidence validate &lt;valid-fixture.jsonl&gt;` exits 0.
2. `go run ./cmd/bench evidence validate &lt;invalid-fixture.jsonl&gt;` exits non-zero with a useful error.
3. `go run ./cmd/bench evidence append --in &lt;valid-fixture.jsonl&gt; --ledger &lt;tmp-ledger.jsonl&gt;` writes validated records with stable record IDs.
4. A duplicate append test proves duplicate records are detected and not silently duplicated.
5. `go test ./cmd/bench` passes.
    </acceptance>
    <labels>area:benchmark, area:evidence, kind:task, phase:build</labels>
  </bead>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="a265df43def8b67e70fbb1533bfb3c21cd6389c5">
<untrusted-data>
commit a265df43def8b67e70fbb1533bfb3c21cd6389c5
Merge: f5d4b69 3b41f5c
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 17:48:20 2026 -0400

    Merge bead fizeau-395d124d attempt 20260506T214200- into master
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
