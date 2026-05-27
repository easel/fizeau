#!/usr/bin/env bats
# Acceptance tests for bash benchmark runner core (fizeau-a2883576)

SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
BENCHMARK_SCRIPT="${SCRIPT_DIR}/benchmark"
PROFILES_DIR="${SCRIPT_DIR}/profiles"
BENCH_SETS_DIR="${SCRIPT_DIR}/bench-sets"
TASK_EXECUTORS_DIR="${SCRIPT_DIR}/task-executors"
HARNESS_ADAPTERS_DIR="${SCRIPT_DIR}/harness-adapters"
TEST_PROFILE="test-profile"
TEST_BENCH_SET="test-bench-set"
TEST_PROFILE_FILE=""
TEST_BENCH_SET_FILE=""
TEST_OUT_DIR=""

# Setup: create a temporary output directory and test fixtures
setup() {
  TEST_OUT_DIR="$(mktemp -d)"
  export BENCH_TASKS_DIR_DEFAULT="${SCRIPT_DIR}/external/terminal-bench-2"
  export DEFAULT_OUT_ROOT="${TEST_OUT_DIR}/bench/results/fiz-tools-v1"
  export BENCH_HARBOR_DIGEST_OVERRIDE="test-digest-sha"
  export BENCH_TASK_EXECUTOR_OVERRIDE="${TASK_EXECUTORS_DIR}/test-echo"

  # Create test profile directory
  local test_profiles_dir="${TEST_OUT_DIR}/profiles"
  mkdir -p "${test_profiles_dir}"
  TEST_PROFILE_FILE="${test_profiles_dir}/${TEST_PROFILE}.yaml"

  # Create a minimal test profile with dumb-script adapter
  cat >"${TEST_PROFILE_FILE}" <<'PROFILE_EOF'
id: test-profile
harness: dumb-script
surface: test
concurrency_group: test
metadata:
  test: true
PROFILE_EOF

  # Create test bench-set directory
  local test_bench_sets_dir="${TEST_OUT_DIR}/bench-sets"
  mkdir -p "${test_bench_sets_dir}"
  TEST_BENCH_SET_FILE="${test_bench_sets_dir}/${TEST_BENCH_SET}.yaml"

  # Create a minimal test bench-set
  cat >"${TEST_BENCH_SET_FILE}" <<'BENCH_SET_EOF'
id: test-bench-set
framework: terminal-bench
dataset: test-data
default_reps: 1
tasks:
  - test-task-1
  - test-task-2
BENCH_SET_EOF

  # Override profile and bench-set dirs for this test run
  export PROFILES_DIR="${test_profiles_dir}"
  export BENCH_SETS_DIR="${test_bench_sets_dir}"
  export HARNESS_ADAPTERS_DIR="${SCRIPT_DIR}/harness-adapters"
}

# Teardown: clean up test output
teardown() {
  if [[ -d "${TEST_OUT_DIR}" ]]; then
    rm -rf "${TEST_OUT_DIR}"
  fi
}

# Test 1: --plan prints the matrix and creates no files
@test "Test_plan_prints_matrix_no_files" {
  # Run with --plan
  output=$("${BENCHMARK_SCRIPT}" --plan --profile "${TEST_PROFILE}" --bench-set "${TEST_BENCH_SET}")

  # Verify output is not empty and contains expected columns
  [[ -n "${output}" ]]
  echo "${output}" | grep -q "profile=${TEST_PROFILE}"
  echo "${output}" | grep -q "bench_set=${TEST_BENCH_SET}"

  # Verify no benchmark-results files were created
  [[ ! -d "${TEST_OUT_DIR}/bench" ]]
}

# Test 2: --plan builds no Docker images
@test "Test_plan_builds_no_image" {
  # Capture stderr to check for docker pull/build
  output=$("${BENCHMARK_SCRIPT}" --plan --profile "${TEST_PROFILE}" --bench-set "${TEST_BENCH_SET}" 2>&1)

  # Verify no docker build/pull commands in output
  echo "${output}" | grep -v "^profile=" | grep -v "^bench_set=" | grep -i "docker" && exit 1 || true

  # Verify no image tag was attempted to be pulled
  [[ ! -f "${TEST_OUT_DIR}/.docker-pull-attempted" ]]
}

# Test 3: run mode produces cells under bench/results/fiz-tools-v1/cells/
@test "Test_run_produces_cells" {
  # Run benchmark (without --plan)
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1

  # Verify cells directory structure exists
  [[ -d "${DEFAULT_OUT_ROOT}/cells" ]]

  # Verify at least one cell exists with the correct structure
  local cell_count
  cell_count=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | wc -l)
  [[ ${cell_count} -gt 0 ]]
}

# Test 4: report.json embeds profile, command, env_redacted, fiz.txt, fiz.err, session/
@test "Test_report_has_embedded_fields" {
  # Run benchmark first to create cells
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1

  # Find first cell report
  local first_report
  first_report=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | head -1)
  [[ -f "${first_report}" ]]

  # Verify required fields in report.json
  jq -e '.profile' "${first_report}" >/dev/null
  jq -e '.command' "${first_report}" >/dev/null
  jq -e '.env_redacted' "${first_report}" >/dev/null

  # Verify artifacts references
  jq -e '.artifacts.fiz_txt' "${first_report}" | grep -q "fiz.txt"
  jq -e '.artifacts.fiz_err' "${first_report}" | grep -q "fiz.err"
  jq -e '.artifacts.session_dir' "${first_report}" | grep -q "session"

  # Verify actual artifact files exist
  local cell_dir
  cell_dir="$(dirname "${first_report}")"
  [[ -f "${cell_dir}/fiz.txt" ]]
  [[ -f "${cell_dir}/fiz.err" ]]
  [[ -d "${cell_dir}/session" ]]
}

# Test 5: resume skips terminal cells; --force-rerun does not
@test "Test_resume_skips_terminal_cell" {
  # First run
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1

  # Count cells after first run
  local first_run_count
  first_run_count=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | wc -l)

  # Second run without --force-rerun (should skip terminal cells)
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1

  # Count cells after second run (should be same)
  local second_run_count
  second_run_count=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | wc -l)
  [[ ${first_run_count} -eq ${second_run_count} ]]

  # Third run with --force-rerun (should re-run and create new cells)
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1 \
    --force-rerun

  # Count cells after force-rerun (should be more)
  local force_rerun_count
  force_rerun_count=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | wc -l)
  [[ ${force_rerun_count} -gt ${first_run_count} ]]
}

# Test 6: --retry-invalid reruns invalid cells with attempt_of/superseded_by links
@test "Test_retry_invalid_reruns" {
  # First run (creates cells)
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1

  # Find a cell and mark it as invalid
  local first_cell
  first_cell=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | head -1)
  [[ -f "${first_cell}" ]]

  local cell_dir
  cell_dir="$(dirname "${first_cell}")"

  # Mark report as invalid
  jq '.invalid_class = "test_invalid"' "${first_cell}" >"${cell_dir}/report.json.tmp"
  mv "${cell_dir}/report.json.tmp" "${first_cell}"

  # Count cells before retry
  local before_count
  before_count=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | wc -l)

  # Run with --retry-invalid
  "${BENCHMARK_SCRIPT}" \
    --profile "${TEST_PROFILE}" \
    --bench-set "${TEST_BENCH_SET}" \
    --out "${DEFAULT_OUT_ROOT}" \
    --reps 1 \
    --retry-invalid

  # Count cells after retry (should have more)
  local after_count
  after_count=$(find "${DEFAULT_OUT_ROOT}/cells" -name "report.json" -type f | wc -l)
  [[ ${after_count} -gt ${before_count} ]]

  # Verify superseded_by link exists in old cell
  jq -e '.superseded_by' "${first_cell}" >/dev/null
}
