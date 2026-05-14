<bead-review>
  <bead id="fizeau-dcb621f8" iter=1>
    <title>Document the FHI benchmark evidence workflow</title>
    <description>
The benchmark system needs a clear operator workflow from running/importing benchmarks to generating FHI and comparative claims. Current docs describe individual scripts and resources, but not the end-to-end evidence pipeline.

In-scope files:
- scripts/benchmark/README.md
- docs/helix/02-design/solution-designs/SD-012-benchmark-evidence-ledger.md references if needed
- docs/research or docs/resources workflow examples

Out-of-scope:
- implementing importers or formula code
- running paid benchmarks
- rewriting unrelated historical benchmark memos

Implementation notes:
- Document canonical commands for TerminalBench matrix -&gt; evidence ledger -&gt; FHI claim.
- Document how beadbench and curated external benchmark snapshots enter the ledger.
- Include examples for local-vs-frontier and harness-vs-harness claims.
- State where large raw artifacts live and what curated snapshots, if any, may be checked in.
    </description>
    <acceptance>
1. Documentation shows the end-to-end workflow from benchmark run/import to validated ledger to FHI/claim output.
2. Documentation includes local-vs-frontier and harness-vs-harness claim examples with required pinned axes.
3. Documentation states large raw benchmark artifacts remain gitignored and curated snapshots/source hashes are the committed evidence.
4. Documentation links SD-012, the benchmark resource notes, and the relevant import/claim commands.
5. Any documented commands have matching tests or `--help` coverage where practical.
    </acceptance>
    <labels>area:docs, area:benchmark, area:fhi, kind:docs, phase:build</labels>
  </bead>

  <changed-files>
    <file>scripts/benchmark/README.md</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="5a8f3d47354d1feaa7f5c4c6492c2593f51630bc">
<untrusted-data>
diff --git a/scripts/benchmark/README.md b/scripts/benchmark/README.md
index 2057b667..30313ad3 100644
--- a/scripts/benchmark/README.md
+++ b/scripts/benchmark/README.md
@@ -252,8 +252,8 @@ source hashes are the committed evidence.
 
 ### 4. Generate FHI claims
 
-Use `fiz-bench fhi delta` for pairwise benchmark claims and `fiz-bench fhi
-rank` for cross-benchmark FHI rankings:
+Use `go run ./cmd/bench fhi delta` for pairwise benchmark claims and
+`go run ./cmd/bench fhi rank` for cross-benchmark FHI rankings:
 
 ```bash
 go run ./cmd/bench fhi delta \
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
