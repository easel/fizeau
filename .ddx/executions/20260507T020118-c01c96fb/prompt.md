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

  <diff rev="7ca8dc25455ea50fbb4dbd4759af0c5b9619249a">
<untrusted-data>
diff --git a/scripts/benchmark/README.md b/scripts/benchmark/README.md
index 9d72741f..4d3f353a 100644
--- a/scripts/benchmark/README.md
+++ b/scripts/benchmark/README.md
@@ -133,6 +133,113 @@ The only per-run inputs that should change are:
 
 ---
 
+## Evidence Workflow
+
+The benchmark evidence pipeline is: run a benchmark, import the resulting
+artifacts into the evidence ledger, validate and append the records, then ask
+`fiz-bench fhi` for comparative claims.
+
+### 1. Run benchmark jobs
+
+Use the TerminalBench runners to produce the raw matrix or report artifacts:
+
+```bash
+# Harness comparison matrix
+BASELINE=local TIER=wide REPS=1 FORCE_RERUN=1 JOBS=1 \
+  scripts/benchmark/run_vidar_qwen36_terminalbench_baseline.sh
+
+# Compact native-vs-fiz comparison wrapper
+OPENROUTER_API_KEY=sk-or-... \
+  scripts/benchmark/run_medium_model_terminalbench_comparison.sh canary
+
+# Single-run benchmark report
+ANTHROPIC_API_KEY=sk-... ./scripts/benchmark/run_benchmark.sh
+```
+
+Those scripts write their raw outputs under `benchmark-results/`, which stays
+gitignored. Large logs, model outputs, tarballs, and upstream artifacts belong
+there, not in git.
+
+### 2. Import benchmark evidence
+
+Project the source artifacts into normalized JSONL evidence with the bench CLI:
+
+```bash
+go run ./cmd/bench evidence import-terminalbench \
+  --matrix cmd/bench/testdata/terminalbench-matrix \
+  --out benchmark-results/evidence/terminalbench.jsonl
+
+go run ./cmd/bench evidence import-beadbench \
+  --report cmd/bench/testdata/beadbench-report/report.json \
+  --out benchmark-results/evidence/beadbench.jsonl
+
+go run ./cmd/bench evidence import-external \
+  --source cmd/bench/testdata/external-benchmarks/rapid-mlx-mhi.md \
+  --out benchmark-results/evidence/external.jsonl
+```
+
+`import-external` is the path for curated external benchmark snapshots,
+including Rapid-MLX MHI, SkillsBench, SWE-bench, and HumanEval fixtures.
+Beadbench and other local runs enter the ledger through their importer paths,
+not by hand-editing the schema.
+
+### 3. Validate and append to the ledger
+
+```bash
+go run ./cmd/bench evidence validate benchmark-results/evidence/terminalbench.jsonl
+go run ./cmd/bench evidence append \
+  --in benchmark-results/evidence/terminalbench.jsonl \
+  --ledger benchmark-results/evidence/ledger.jsonl
+```
+
+Append-only ledgers keep the normalized records and their source hashes or
+source URLs together. Checked-in curated snapshots, when approved, live under
+`scripts/benchmark/evidence/<snapshot-id>.jsonl` and must contain normalized
+records only. Raw artifacts stay in `benchmark-results/`; curated snapshots or
+source hashes are the committed evidence.
+
+### 4. Generate FHI claims
+
+Use `fiz-bench fhi delta` for pairwise benchmark claims and `fiz-bench fhi
+rank` for cross-benchmark FHI rankings:
+
+```bash
+go run ./cmd/bench fhi delta \
+  --ledger benchmark-results/evidence/ledger.jsonl \
+  --left terminalbench-delta-left-opus-claude-code \
+  --right terminalbench-delta-right-opus-claude-code
+
+go run ./cmd/bench fhi rank \
+  --ledger benchmark-results/evidence/ledger.jsonl
+```
+
+The command surface is covered by the `cmd/bench` tests, and `go run ./cmd/bench
+<subcommand> --help` exposes the documented flags.
+
+### Claim axes
+
+Harness-vs-harness claims must pin the benchmark axes and vary only the harness.
+For a TerminalBench comparison, that means keeping the model, provider,
+benchmark version, subset id/version, scorer, evidence window, denominator
+policy, and run environment fixed while comparing two harness rows such as
+`fiz-native` and `claude-code`.
+
+Local-vs-frontier claims must pin the same formula version, evidence window,
+benchmark set, and denominator rules on both sides. The local row also needs the
+deployment-class facts that make the environment auditable:
+
+- runtime/server name and version
+- model artifact id or checksum when available
+- quantization and precision
+- hardware class, memory, accelerator backend, and OS/architecture
+- endpoint type and provider surface
+- context limit and the reasoning/sampling controls actually applied
+
+The frontier row should carry the same benchmark and formula pins, plus the
+provider and model snapshot/version captured by the source.
+
+---
+
 ## Vidar Qwen3.6 Harness Matrix
 
 `run_vidar_qwen36_terminalbench_baseline.sh` runs the shared Vidar oMLX
@@ -306,6 +413,10 @@ should be derived from normalized raw evidence keyed by model, harness,
 provider, and benchmark. New importers should project source reports into the
 schema at `scripts/benchmark/benchmark-evidence.schema.json`; see
 `docs/helix/02-design/solution-designs/SD-012-benchmark-evidence-ledger.md`.
+The relevant command surface is `go run ./cmd/bench evidence import-terminalbench`,
+`go run ./cmd/bench evidence import-beadbench`, `go run ./cmd/bench evidence
+import-external`, `go run ./cmd/bench evidence append`, and `go run ./cmd/bench
+fhi delta|rank`.
 
 Curated external snapshots can be imported with:
 
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
