#!/usr/bin/env bats
# runner_loop.bats — acceptance tests for the per-cell execution loop
# (bead fizeau-4f5acc2c / PR A3). Exercises the run loop end-to-end with a
# hermetic mock task-executor (BENCH_TASK_EXECUTOR_OVERRIDE) so no Docker,
# network, or real model is required.
#
# Covers parent AC2 (self-describing cells + resume/retry-invalid) and AC3
# (budget halting), plus per-cell transient retry linkage.
#
#   TestRunProducesCellReports   — AC1: cells embed profile/command/env/artifacts
#   TestResumeAndRetryInvalid    — AC2: resume skips terminal cells; --retry-invalid reruns
#   TestBudgetHaltPlaceholder    — AC3: --max-cost-usd yields budget_halted placeholders
#   TestPerCellRetryLinks        — AC4: transient failure links attempt_of/superseded_by

setup() {
  SCRIPT_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." && pwd)"
  BENCHMARK_BIN="${SCRIPT_DIR}/benchmark"
  TMP="$(mktemp -d)"
  OUT="${TMP}/bench/results/fiz-tools-v1"

  # Minimal host tasks dir; resolve_tasks_dir honors BENCH_TASKS_DIR.
  mkdir -p "${TMP}/tasks"

  # Hermetic, deterministic defaults shared by every test.
  export BENCH_TASKS_DIR="${TMP}/tasks"
  export BENCH_HARBOR_DIGEST_OVERRIDE="test-image-digest"
  export BENCH_RETRY_BACKOFF_BASE=0
  export FIZEAU_BENCH_STATE_DIR="${TMP}/state"
}

teardown() {
  rm -rf "${TMP}"
}

# write_mock_executor <path> <body-of-result-json-jq>
# Emits a task-executor that reads the task-spec on stdin, writes result.json
# into the cell_dir, and exits 0.
write_success_executor() {
  cat >"${TMP}/mock-exec" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"${spec}")"
task_id="$(jq -r '.task_id' <<<"${spec}")"
mkdir -p "${cell_dir}"
jq -n --arg t "${task_id}" \
  '{task_id:$t, final_status:"completed", status:"completed",
    input_tokens:100, output_tokens:50, cached_input_tokens:0}' \
  >"${cell_dir}/result.json"
exit 0
EOF
  chmod +x "${TMP}/mock-exec"
  printf '%s\n' "${TMP}/mock-exec"
}

# Lists every report.json under OUT, sorted.
reports() {
  find "${OUT}/cells" -name report.json 2>/dev/null | sort
}

# ---------------------------------------------------------------------------
# AC1: a full run produces self-describing cell reports.
# ---------------------------------------------------------------------------
@test "TestRunProducesCellReports" {
  local exec_bin
  exec_bin="$(write_success_executor)"

  BENCH_TASK_EXECUTOR_OVERRIDE="${exec_bin}" \
    "${BENCHMARK_BIN}" --profile sindri-lucebox --bench-set tb-2-1-canary \
    --out "${OUT}" --reps 1

  # Cells land under <out>/cells/<dataset>/<task>/<cell-id>/.
  [ -d "${OUT}/cells/terminal-bench-2-1" ]

  local report_count=0
  while IFS= read -r report; do
    [ -n "${report}" ] || continue
    report_count=$((report_count + 1))
    local cell_dir="$(dirname "${report}")"

    # report.json embeds the resolved profile, command, env_redacted, result,
    # final_status, and artifact pointers.
    run jq -e '
      (.profile.id == "sindri-lucebox")
      and (.command | type == "array")
      and (.env_redacted | type == "object")
      and (.artifacts.fiz_txt == "fiz.txt")
      and (.artifacts.fiz_err == "fiz.err")
      and (.artifacts.session_dir == "session")
      and (.final_status == "completed")
      and (.task_executor_version | type == "string")
      and (.harbor_runner_image_digest == "test-image-digest")
    ' "${report}"
    [ "$status" -eq 0 ]

    # On-disk artifacts exist alongside the report.
    [ -f "${cell_dir}/fiz.txt" ]
    [ -f "${cell_dir}/fiz.err" ]
    [ -d "${cell_dir}/session" ]
  done < <(reports)

  # 3 canary tasks x 1 rep = 3 cells.
  [ "${report_count}" -eq 3 ]
}

# ---------------------------------------------------------------------------
# AC2: resume skips terminal cells; --retry-invalid reruns invalid cells only.
# ---------------------------------------------------------------------------
@test "TestResumeAndRetryInvalid" {
  local exec_bin
  exec_bin="$(write_success_executor)"

  # First run: create three terminal (completed) cells.
  BENCH_TASK_EXECUTOR_OVERRIDE="${exec_bin}" \
    "${BENCHMARK_BIN}" --profile sindri-lucebox --bench-set tb-2-1-canary \
    --out "${OUT}" --reps 1

  local first_reports
  mapfile -t first_reports < <(reports)
  [ "${#first_reports[@]}" -eq 3 ]

  # Snapshot mtimes of every report.
  declare -A mtime_before
  local r
  for r in "${first_reports[@]}"; do
    mtime_before["${r}"]="$(stat -c %Y "${r}")"
  done

  sleep 1

  # Resume without flags: terminal cells must be skipped (no rewrite, no new cells).
  BENCH_TASK_EXECUTOR_OVERRIDE="${exec_bin}" \
    "${BENCHMARK_BIN}" --profile sindri-lucebox --bench-set tb-2-1-canary \
    --out "${OUT}" --reps 1

  local resume_count=0
  while IFS= read -r r; do
    [ -n "${r}" ] || continue
    resume_count=$((resume_count + 1))
    [ "${mtime_before["${r}"]}" = "$(stat -c %Y "${r}")" ] \
      || { echo "cell rewritten on resume: ${r}"; return 1; }
  done < <(reports)
  [ "${resume_count}" -eq 3 ]

  # Mark one cell invalid; remember a second cell that must stay untouched.
  local invalid_cell="${first_reports[0]}"
  local valid_cell="${first_reports[1]}"
  local tmp_json
  tmp_json="$(mktemp)"
  jq '.invalid_class = "test_invalid"' "${invalid_cell}" >"${tmp_json}"
  mv "${tmp_json}" "${invalid_cell}"
  local invalid_mtime valid_mtime
  invalid_mtime="$(stat -c %Y "${invalid_cell}")"
  valid_mtime="$(stat -c %Y "${valid_cell}")"

  sleep 1

  # --retry-invalid reruns only the invalid cell: it gets a superseded_by
  # back-link and a fresh attempt cell appears; the valid cell is untouched.
  BENCH_TASK_EXECUTOR_OVERRIDE="${exec_bin}" \
    "${BENCHMARK_BIN}" --profile sindri-lucebox --bench-set tb-2-1-canary \
    --out "${OUT}" --reps 1 --retry-invalid

  run jq -e '.superseded_by | type == "string" and length > 0' "${invalid_cell}"
  [ "$status" -eq 0 ]
  [ "${invalid_mtime}" != "$(stat -c %Y "${invalid_cell}")" ]

  # The valid (terminal, non-invalid) cell was not rerun.
  [ "${valid_mtime}" = "$(stat -c %Y "${valid_cell}")" ]

  # A new attempt cell exists pointing back at the invalid cell.
  local invalid_id new_attempts
  invalid_id="$(jq -r '.cell_id' "${invalid_cell}")"
  new_attempts=0
  while IFS= read -r r; do
    [ -n "${r}" ] || continue
    local ao
    ao="$(jq -r '.attempt_of // ""' "${r}")"
    [ -n "${ao}" ] || continue
    if [ "$(basename "${ao}")" = "${invalid_id}" ]; then
      new_attempts=$((new_attempts + 1))
    fi
  done < <(reports)
  [ "${new_attempts}" -ge 1 ]
}

# ---------------------------------------------------------------------------
# AC3: --max-cost-usd halts the sweep and writes budget_halted placeholders.
# ---------------------------------------------------------------------------
@test "TestBudgetHaltPlaceholder" {
  # sindri-lucebox pricing is 0, so cost comes from the executor's reported
  # cost_usd. 0.05 per cell over a 0.01 cap halts after the first cell.
  cat >"${TMP}/cost-exec" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"${spec}")"
task_id="$(jq -r '.task_id' <<<"${spec}")"
mkdir -p "${cell_dir}"
jq -n --arg t "${task_id}" \
  '{task_id:$t, final_status:"completed", status:"completed", cost_usd:0.05}' \
  >"${cell_dir}/result.json"
exit 0
EOF
  chmod +x "${TMP}/cost-exec"

  BENCH_TASK_EXECUTOR_OVERRIDE="${TMP}/cost-exec" \
    "${BENCHMARK_BIN}" --profile sindri-lucebox --bench-set tb-2-1-canary \
    --out "${OUT}" --reps 1 --max-cost-usd 0.01

  # budget.json recorded the cap and flipped halted=true.
  [ -f "${OUT}/budget.json" ]
  run jq -e '.halted == true and (.max_cost_usd == 0.01)' "${OUT}/budget.json"
  [ "$status" -eq 0 ]

  # At least one cell is a budget_halted placeholder with the required shape.
  local halted_count=0
  while IFS= read -r r; do
    [ -n "${r}" ] || continue
    if [ "$(jq -r '.final_status' "${r}")" = "budget_halted" ]; then
      run jq -e '
        (.final_status == "budget_halted")
        and (.process_outcome == "setup_failed")
        and (.note | type == "string" and length > 0)
      ' "${r}"
      [ "$status" -eq 0 ]
      halted_count=$((halted_count + 1))
    fi
  done < <(reports)
  [ "${halted_count}" -ge 1 ]
}

# ---------------------------------------------------------------------------
# AC4: a transient failure mints a retry cell linked via attempt_of, and the
# prior (superseded) cell is back-written with superseded_by.
# ---------------------------------------------------------------------------
@test "TestPerCellRetryLinks" {
  # transient-harness fails TRANSIENT_FAIL_COUNT times (transient error_class)
  # then succeeds, driving the per-cell retry path.
  BENCH_TASK_EXECUTOR_OVERRIDE="${SCRIPT_DIR}/test/fixtures/transient-harness" \
  BENCH_RETRY_MAX_ATTEMPTS=3 \
  TRANSIENT_FAIL_COUNT=1 \
    "${BENCHMARK_BIN}" --profile noop --bench-set tb-2-1-canary \
    --out "${OUT}" --reps 1 --force-rerun

  # Find a retry cell (attempt_of set) and confirm the bidirectional link.
  local linked=0
  local r
  while IFS= read -r r; do
    [ -n "${r}" ] || continue
    local attempt_of retry_id
    attempt_of="$(jq -r '.attempt_of // ""' "${r}")"
    [ -n "${attempt_of}" ] || continue
    retry_id="$(jq -r '.cell_id' "${r}")"

    # The prior cell exists, has a transient error_class, and is back-written
    # with superseded_by pointing at this retry cell.
    local prior="${attempt_of}/report.json"
    [ -f "${prior}" ] || { echo "missing prior cell report: ${prior}"; return 1; }
    run jq -e --arg id "${retry_id}" '
      (.superseded_by == $id)
      and (.error_class | type == "string" and length > 0)
    ' "${prior}"
    [ "$status" -eq 0 ]
    linked=$((linked + 1))
  done < <(reports)

  [ "${linked}" -ge 1 ]
}
