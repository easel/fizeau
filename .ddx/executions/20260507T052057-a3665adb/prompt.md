<bead-review>
  <bead id="fizeau-ee77ec7f" iter=1>
    <title>Migrate TerminalBench benchmark runners to 2.1</title>
    <description>
TerminalBench 2.1 is now the target benchmark. Update Fizeau benchmark runners and active operator docs to use the official Harbor dataset terminal-bench/terminal-bench-2-1, while preserving TB-2.0 historical fixtures and evidence records as historical 2.0 data.

This migration is now explicitly staged because the full sweep has multiple resource classes:
- canary across all intended lanes,
- local qwen3.6-27b providers × supported harness/fiz lanes,
- Sonnet via fiz native/provider versus fiz Claude harness,
- GPT-5.4-mini via fiz native/provider versus fiz Codex harness.

The runner must be restartable and smart about concurrency: local model providers are grouped by server/base_url and default to one active cell per resource group, while independent managed-provider lanes may run in parallel under budget/rate caps.

This bead intentionally replaces the stale plan to run unexecuted TB-2.0 matrices first. It must coordinate with the in-progress TerminalBench canary/documentation beads and must not relabel checked-in 2.0 subset/result fixtures as 2.1 without verifying task compatibility.

In scope:
- active benchmark runner defaults and env templates
- TerminalBench 2.1 subset/manifest strategy
- staged lane/resource-group plan
- local qwen provider profiles including bragi-club-3090 vLLM if verified
- resumable/provider-aware phase runner
- canary preflight consistency for Harbor dataset vs local TB-2.0 task-dir mode
- focused benchmark docs after the current README workflow bead lands

Out of scope:
- rewriting historical TB-2.0 reports, evidence fixtures, or old research memos
- running the paid/full sweep itself
    </description>
    <acceptance>
1. The official runnable Harbor dataset is recorded as terminal-bench/terminal-bench-2-1, with TerminalBench 2.1 named distinctly from TB-2.0 in active scripts/docs.
2. Child beads define the staged lane/resource plan, add required local qwen provider profiles, and implement the resumable provider-aware phase runner.
3. Active runner defaults use the 2.1 dataset only after any required canary/documentation in-progress work is closed or explicitly reconciled.
4. Existing TB-2.0 fixtures, subset manifests, import tests, and historical research artifacts remain labeled TB-2.0 unless they are regenerated or verified against 2.1.
5. The existing subset task IDs are verified against TerminalBench 2.1, or a new 2.1 subset/full manifest is generated and the old subsets are explicitly marked TB-2.0-only.
6. The runner supports the phases canary, local-qwen, sonnet-comparison, gpt-comparison, and all.
7. The runner defaults to resume-safe behavior and provider-aware concurrency caps so shared local model endpoints are not overloaded.
8. Egress/canary preflight cannot validate a TB-2.0 local task directory while running against the 2.1 Harbor dataset without an explicit compatibility note.
9. bash -n passes for changed benchmark shell scripts.
10. go test ./cmd/bench passes.
11. The follow-up full-sweep bead fizeau-c770193a depends on this migration and runner work.
    </acceptance>
    <notes>
2026-05-07 local profile scope corrected: TerminalBench 2.1 migration should prepare profiles/runner lanes for Vidar native oMLX, Grendel Rapid-MLX, Bragi club-3090, and Sindri club-3090. Vidar OpenAI-compatible is intentionally excluded as a duplicate/confusing lane.
    </notes>
    <labels>area:benchmark, area:terminalbench, area:harness, kind:task, phase:build</labels>
  </bead>

  <changed-files>
    <file>scripts/benchmark/egress_canary.sh</file>
    <file>scripts/benchmark/task-subset-v2.yaml</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="55f7ced88e607158c873e8d10c8c95325295ff47">
<untrusted-data>
diff --git a/scripts/benchmark/egress_canary.sh b/scripts/benchmark/egress_canary.sh
index ecd2b7fe..ccd11fc3 100755
--- a/scripts/benchmark/egress_canary.sh
+++ b/scripts/benchmark/egress_canary.sh
@@ -6,6 +6,16 @@
 # cheapest tool-capable OpenAI-compat smoke model and a single concrete
 # TB-2 task that actually exists at the pinned commit (fix-git).
 #
+# TB-2.0 ONLY — COMPATIBILITY NOTE:
+#   This script validates provider egress using a local terminal-bench@2.0
+#   task directory (scripts/benchmark/external/terminal-bench-2/). It is NOT
+#   a TB-2.1 preflight. Running it with FIZEAU_BENCH_DATASET set to a TB-2.1
+#   identifier (e.g. terminal-bench/terminal-bench-2-1) will mix a TB-2.0
+#   local task directory with a TB-2.1 Harbor dataset reference, producing a
+#   misleading canary result. Use the TB-2.1 sweep's canary phase instead:
+#     fiz-bench sweep --phase canary --dry-run   # plan
+#     fiz-bench sweep --phase canary             # run
+#
 # This replaces an earlier "hello-world" formulation: terminal-bench@2.0 at
 # commit 53ff2b87 has no hello-world task, so the canary now targets
 # fix-git (easy / software-engineering, ~5 min expert time).
@@ -103,13 +113,30 @@ require_task_exists() {
     fi
 }
 
-echo "=== fiz egress canary ==="
+# Compatibility guard: this script validates against a local TB-2.0 task
+# directory. If DATASET is set to anything other than terminal-bench@2.0,
+# the local task directory check (TB-2.0) and the Harbor dataset reference
+# diverge — results are not comparable to a true TB-2.1 preflight.
+warn_dataset_mismatch() {
+    if [[ "${DATASET}" != "terminal-bench@2.0" ]]; then
+        echo "WARNING: DATASET='${DATASET}' is not terminal-bench@2.0."
+        echo "         This script validates a local TB-2.0 task directory."
+        echo "         To preflight the TB-2.1 Harbor dataset, use instead:"
+        echo "           fiz-bench sweep --phase canary --dry-run  # plan"
+        echo "           fiz-bench sweep --phase canary            # run"
+        echo ""
+    fi
+}
+
+echo "=== fiz egress canary (TB-2.0 local task-dir mode) ==="
 echo "Repo:      ${REPO_ROOT}"
-echo "Task:      ${CANARY_TASK}  (TB-2 @ pinned commit)"
+echo "Task:      ${CANARY_TASK}  (TB-2.0 @ pinned commit)"
+echo "Dataset:   ${DATASET}"
 echo "Model:     ${PROVIDER_MODEL}"
 echo "Archive:   ${ARCHIVE_DIR}"
 echo ""
 
+warn_dataset_mismatch
 require_task_exists
 
 # Step 1: binary
diff --git a/scripts/benchmark/task-subset-v2.yaml b/scripts/benchmark/task-subset-v2.yaml
index 5a039851..40e87a6f 100644
--- a/scripts/benchmark/task-subset-v2.yaml
+++ b/scripts/benchmark/task-subset-v2.yaml
@@ -1,6 +1,11 @@
 # Fixed benchmark subset for fizeau — v2 (2026-04-10)
 # Evidence-grade comparison subset with real Terminal-Bench 2.0 task IDs.
 #
+# TB-2.0-ONLY: This manifest uses task IDs from terminal-bench@2.0 at commit
+# 53ff2b87d621bdb97b455671f2bd9728b7d86c11. Do NOT use it for TB-2.1 runs —
+# task IDs may not exist in terminal-bench/terminal-bench-2-1. For TB-2.1 work
+# use task-subset-tb21-canary.yaml or task-subset-tb21-full.yaml instead.
+#
 # Preserve task-subset-v1.yaml as the historical placeholder artifact from the
 # initial benchmark design. v2 is the first manifest suitable for before/after
 # comparison runs.
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
