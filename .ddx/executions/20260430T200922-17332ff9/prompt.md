<bead-review>
  <bead id="agent-0fdb9cc0" iter=1>
    <title>FZ-026a Rename benchmark script env vars</title>
    <description>
Rename benchmark shell script and benchmark README env vars from DDX_AGENT_* to FIZEAU_BENCH_* or FIZEAU_* per FZ-002 taxonomy.
    </description>
    <acceptance>
scripts/benchmark README and shell scripts use enumerated new names; a chosen dry-run command passes; old DDX_AGENT_* absent outside allowlist in scripts/benchmark.
    </acceptance>
    <notes>
Prework review before execution: mostly complete and testable. Scope is benchmark shell scripts and scripts/benchmark README env literals only. Improvement for implementer: enumerate the old DDX_AGENT_* names first, map them to FIZEAU_BENCH_* or FIZEAU_* according to FZ-002 taxonomy, run a shell syntax/dry-run check, and prove old DDX_AGENT_* is absent from scripts/benchmark outside explicit historical allowlist. Local bragi/qwen3.6-27b attempt 20260430T195920 stalled after exploration and produced no diff/result; escalate rather than retrying the same local route.
    </notes>
    <labels>area:rename, kind:tooling, area:benchmark</labels>
  </bead>

  <changed-files>
    <file>scripts/benchmark/README.md</file>
    <file>scripts/benchmark/evidence-grade-comparison.env</file>
    <file>scripts/benchmark/run_benchmark.sh</file>
    <file>scripts/benchmark/smoke_run.sh</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="1b799f3ba5ed5c0a55c4662fb4eea87c15425c2a">
diff --git a/scripts/benchmark/README.md b/scripts/benchmark/README.md
index e72d083..0a407d2 100644
--- a/scripts/benchmark/README.md
+++ b/scripts/benchmark/README.md
@@ -57,8 +57,8 @@ You can point the same harness at a prebuilt ddx-agent binary instead of
 rebuilding the current checkout:
 
 ```bash
-DDX_AGENT_BINARY=/path/to/ddx-agent-linux-amd64 \
-DDX_AGENT_SHA=<commit-under-test> \
+FIZEAU_BENCH_BINARY=/path/to/ddx-agent-linux-amd64 \
+FIZEAU_BENCH_SHA=<commit-under-test> \
 ./scripts/benchmark/run_benchmark.sh
 ```
 
@@ -72,7 +72,7 @@ without invoking Harbor:
 
 ```bash
 DDX_BENCH_DRY_RUN=1 \
-DDX_AGENT_BINARY=/path/to/ddx-agent-linux-amd64 \
+FIZEAU_BENCH_BINARY=/path/to/ddx-agent-linux-amd64 \
 DDX_BENCH_PROVIDER_MODEL=qwen/qwen3.6-plus \
 ./scripts/benchmark/run_benchmark.sh
 ```
@@ -109,8 +109,8 @@ freezes the intended `before` and `after` SHAs for the original benchmark-change
 window plus the shared dataset, subset, runtime, preset, and provider/model route.
 The only per-run inputs that should change are:
 
-- `DDX_AGENT_BINARY`
-- `DDX_AGENT_SHA` (set to either `DDX_BENCH_BEFORE_SHA` or `DDX_BENCH_AFTER_SHA`)
+- `FIZEAU_BENCH_BINARY`
+- `FIZEAU_BENCH_SHA` (set to either `DDX_BENCH_BEFORE_SHA` or `DDX_BENCH_AFTER_SHA`)
 
 ---
 
diff --git a/scripts/benchmark/evidence-grade-comparison.env b/scripts/benchmark/evidence-grade-comparison.env
index d3f33cd..e40a9de 100644
--- a/scripts/benchmark/evidence-grade-comparison.env
+++ b/scripts/benchmark/evidence-grade-comparison.env
@@ -2,14 +2,14 @@
 #
 # Usage:
 #   1. Build a linux/amd64 ddx-agent binary from either pinned SHA below.
-#   2. export DDX_AGENT_BINARY=/abs/path/to/ddx-agent-linux-amd64
-#   3. export DDX_AGENT_SHA="$DDX_BENCH_BEFORE_SHA"   # or AFTER
+#   2. export FIZEAU_BENCH_BINARY=/abs/path/to/ddx-agent-linux-amd64
+#   3. export FIZEAU_BENCH_SHA="$DDX_BENCH_BEFORE_SHA"   # or AFTER
 #   4. set OPENROUTER_API_KEY in the shell
 #   5. source ./scripts/benchmark/evidence-grade-comparison.env
 #   6. ./scripts/benchmark/run_benchmark.sh
 #
 # This file pins the shared comparison controls. The only intended per-run
-# variation is DDX_AGENT_BINARY and DDX_AGENT_SHA.
+# variation is FIZEAU_BENCH_BINARY and FIZEAU_BENCH_SHA.
 
 export DDX_BENCH_BEFORE_SHA=9c8b5b09fb4058545201b8d72a21b0861e4a4cdb
 export DDX_BENCH_AFTER_SHA=dcc2f4512207e8b80d9f70bb2aeb7cb6c3913077
diff --git a/scripts/benchmark/run_benchmark.sh b/scripts/benchmark/run_benchmark.sh
index 56b477b..3b3233f 100755
--- a/scripts/benchmark/run_benchmark.sh
+++ b/scripts/benchmark/run_benchmark.sh
@@ -4,7 +4,7 @@
 # Usage:
 #   ANTHROPIC_API_KEY=sk-... ./scripts/benchmark/run_benchmark.sh
 #   OPENROUTER_API_KEY=sk-or-... ./scripts/benchmark/run_benchmark.sh
-#   DDX_AGENT_BINARY=/path/to/ddx-agent-linux-amd64 ./scripts/benchmark/run_benchmark.sh
+#   FIZEAU_BENCH_BINARY=/path/to/ddx-agent-linux-amd64 ./scripts/benchmark/run_benchmark.sh
 #
 # Output:
 #   benchmark-results/report-<TIMESTAMP>.json
@@ -27,7 +27,7 @@ SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
 REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
 DIST_DIR="${REPO_ROOT}/dist"
 DEFAULT_BINARY="${DIST_DIR}/ddx-agent-linux-amd64"
-INPUT_BINARY="${DDX_AGENT_BINARY:-${DEFAULT_BINARY}}"
+INPUT_BINARY="${FIZEAU_BENCH_BINARY:-${DEFAULT_BINARY}}"
 SUBSET_FILE="${DDX_BENCH_SUBSET_FILE:-${SCRIPT_DIR}/task-subset-v2.yaml}"
 RESULTS_DIR="${DDX_BENCH_RESULTS_DIR:-${REPO_ROOT}/benchmark-results}"
 TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
@@ -45,7 +45,7 @@ PROVIDER_REASONING="${DDX_BENCH_PROVIDER_REASONING:-}"
 SYSTEM_APPEND="${DDX_BENCH_SYSTEM_APPEND:-}"
 AGENT_TIMEOUT_MULTIPLIER="${DDX_BENCH_AGENT_TIMEOUT_MULTIPLIER:-1.0}"
 DRY_RUN="${DDX_BENCH_DRY_RUN:-0}"
-AGENT_SHA_OVERRIDE="${DDX_AGENT_SHA:-}"
+AGENT_SHA_OVERRIDE="${FIZEAU_BENCH_SHA:-}"
 HARBOR_BIN=""
 
 BUNDLE_DIR="$(mktemp -d /tmp/ddx-bench-agent-XXXXXX)"
@@ -156,7 +156,7 @@ echo ""
 # Step 1: Prepare the binary-under-test bundle
 # ---------------------------------------------------------------------------- #
 echo "[1/5] Preparing benchmark harness bundle..."
-if [[ -z "${DDX_AGENT_BINARY:-}" ]]; then
+if [[ -z "${FIZEAU_BENCH_BINARY:-}" ]]; then
     mkdir -p "${DIST_DIR}"
     GOOS=linux GOARCH=amd64 go build \
         -ldflags "-X main.GitCommit=$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo dev)" \
@@ -164,7 +164,7 @@ if [[ -z "${DDX_AGENT_BINARY:-}" ]]; then
     echo "      Built current checkout binary: ${DEFAULT_BINARY}"
 else
     if [[ ! -f "${INPUT_BINARY}" ]]; then
-        echo "ERROR: DDX_AGENT_BINARY does not exist: ${INPUT_BINARY}"
+        echo "ERROR: FIZEAU_BENCH_BINARY does not exist: ${INPUT_BINARY}"
         exit 1
     fi
     echo "      Using supplied binary: ${INPUT_BINARY}"
@@ -215,7 +215,7 @@ fi
 
 if [[ -n "${AGENT_SHA_OVERRIDE}" ]]; then
     AGENT_GIT_SHA="${AGENT_SHA_OVERRIDE}"
-elif [[ -z "${DDX_AGENT_BINARY:-}" ]]; then
+elif [[ -z "${FIZEAU_BENCH_BINARY:-}" ]]; then
     AGENT_GIT_SHA="$(git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null || echo unknown)"
 else
     AGENT_GIT_SHA="unknown"
diff --git a/scripts/benchmark/smoke_run.sh b/scripts/benchmark/smoke_run.sh
index c7a67f2..aed42f6 100755
--- a/scripts/benchmark/smoke_run.sh
+++ b/scripts/benchmark/smoke_run.sh
@@ -18,7 +18,7 @@ SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
 REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
 DIST_DIR="${REPO_ROOT}/dist"
 DEFAULT_BINARY="${DIST_DIR}/ddx-agent-linux-amd64"
-INPUT_BINARY="${DDX_AGENT_BINARY:-${DEFAULT_BINARY}}"
+INPUT_BINARY="${FIZEAU_BENCH_BINARY:-${DEFAULT_BINARY}}"
 SMOKE_TASK="${DDX_BENCH_SMOKE_TASK:-break-filter-js-from-html}"
 DATASET="${DDX_BENCH_DATASET:-terminal-bench@2.0}"
 RUNTIME="${DDX_BENCH_RUNTIME:-docker}"
@@ -116,13 +116,13 @@ echo ""
 
 # Step 1: Prepare binary under test
 echo "[1/4] Preparing binary under test..."
-if [[ -z "${DDX_AGENT_BINARY:-}" ]]; then
+if [[ -z "${FIZEAU_BENCH_BINARY:-}" ]]; then
     mkdir -p "${DIST_DIR}"
     GOOS=linux GOARCH=amd64 go build -o "${DEFAULT_BINARY}" "${REPO_ROOT}/cmd/fiz"
     echo "      Built current checkout binary: ${DEFAULT_BINARY}"
 else
     if [[ ! -f "${INPUT_BINARY}" ]]; then
-        echo "ERROR: DDX_AGENT_BINARY does not exist: ${INPUT_BINARY}"
+        echo "ERROR: FIZEAU_BENCH_BINARY does not exist: ${INPUT_BINARY}"
         exit 1
     fi
     echo "      Using supplied binary: ${INPUT_BINARY}"
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
