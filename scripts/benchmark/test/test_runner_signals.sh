#!/usr/bin/env bash
# test_runner_signals.sh — signal handling tests for benchmark runner (A3b)
# Tests SIGTERM/SIGINT handling, cell interrupt status, and in-flight cleanup
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

TESTS_PASSED=0
TESTS_FAILED=0

fail() {
  echo -e "${RED}FAIL${NC}: $*" >&2
  TESTS_FAILED=$((TESTS_FAILED + 1))
}

pass() {
  echo -e "${GREEN}PASS${NC}: $*"
  TESTS_PASSED=$((TESTS_PASSED + 1))
}

# Create a mock executor that sleeps so signals can interrupt it
create_mock_executor() {
  local path="$1"
  cat >"${path}" <<'EOF'
#!/bin/bash
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir // ""' <<<"${spec}")"
mkdir -p "${cell_dir}"
sleep 10
jq -n '{final_status:"completed"}' >"${cell_dir}/result.json"
exit 0
EOF
  chmod +x "${path}"
}

# test_sigterm_marks_interrupted_and_stops_cells: AC1
test_sigterm_marks_interrupted_and_stops_cells() {
  local test_name="test_sigterm_marks_interrupted_and_stops_cells"
  local tmpdir results_dir state_dir mock_executor tasks_dir
  tmpdir="$(mktemp -d)"
  results_dir="${tmpdir}/bench/results"
  state_dir="${tmpdir}/.bench-state"
  mock_executor="${tmpdir}/mock-executor"
  tasks_dir="${tmpdir}/tasks"
  trap "rm -rf '${tmpdir}'" RETURN

  mkdir -p "${results_dir}" "${state_dir}" "${tasks_dir}/test-task"
  echo '{}' >"${tasks_dir}/test-task/data.json"
  create_mock_executor "${mock_executor}"

  set +e
  (
    export FIZEAU_BENCH_STATE_DIR="${state_dir}"
    export BENCH_TASK_EXECUTOR_OVERRIDE="${mock_executor}"
    export BENCH_TASKS_DIR="${tasks_dir}"
    export DEFAULT_OUT_ROOT="${results_dir}"
    cd "${SCRIPT_DIR}"
    ./benchmark \
      --profile noop \
      --bench-set tb-2-1-canary \
      --jobs 1 \
      --reps 1 \
      --force-rerun \
      2>&1
  ) &
  local bench_pid=$!
  set -e

  sleep 3
  kill -TERM "${bench_pid}" 2>/dev/null || true

  local exit_code=0
  local wait_count=0
  while (( wait_count < 30 )); do
    if ! kill -0 "${bench_pid}" 2>/dev/null; then
      wait "${bench_pid}" 2>/dev/null || exit_code=$?
      break
    fi
    sleep 0.5
    wait_count=$((wait_count + 1))
  done

  if kill -0 "${bench_pid}" 2>/dev/null; then
    kill -KILL "${bench_pid}" 2>/dev/null || true
    wait "${bench_pid}" 2>/dev/null || true
    fail "${test_name}: process did not exit after SIGTERM + timeout"
    return 1
  fi

  if [[ ${exit_code} -eq 0 ]]; then
    fail "${test_name}: expected non-zero exit code on SIGTERM, got ${exit_code}"
    return 1
  fi

  local interrupted_cells=0
  if [[ -d "${results_dir}/cells" ]]; then
    while IFS= read -r report; do
      [[ -z "${report}" ]] && continue
      local final_status process_outcome
      final_status="$(jq -r '.final_status // ""' "${report}" 2>/dev/null || echo '')"
      process_outcome="$(jq -r '.process_outcome // ""' "${report}" 2>/dev/null || echo '')"
      if [[ "${final_status}" == "interrupted" && "${process_outcome}" == "killed" ]]; then
        interrupted_cells=$((interrupted_cells + 1))
      fi
    done < <(find "${results_dir}/cells" -name "report.json" -type f 2>/dev/null)
  fi

  if [[ ${interrupted_cells} -gt 0 ]]; then
    pass "${test_name}"
  else
    fail "${test_name}: no interrupted cells found (exit_code=${exit_code})"
    return 1
  fi
}

# test_sigint_same_behavior: AC2
test_sigint_same_behavior() {
  local test_name="test_sigint_same_behavior"
  local tmpdir results_dir state_dir mock_executor tasks_dir
  tmpdir="$(mktemp -d)"
  results_dir="${tmpdir}/bench/results"
  state_dir="${tmpdir}/.bench-state"
  mock_executor="${tmpdir}/mock-executor"
  tasks_dir="${tmpdir}/tasks"
  trap "rm -rf '${tmpdir}'" RETURN

  mkdir -p "${results_dir}" "${state_dir}" "${tasks_dir}/test-task"
  echo '{}' >"${tasks_dir}/test-task/data.json"
  create_mock_executor "${mock_executor}"

  set +e
  (
    export FIZEAU_BENCH_STATE_DIR="${state_dir}"
    export BENCH_TASK_EXECUTOR_OVERRIDE="${mock_executor}"
    export BENCH_TASKS_DIR="${tasks_dir}"
    export DEFAULT_OUT_ROOT="${results_dir}"
    cd "${SCRIPT_DIR}"
    ./benchmark \
      --profile noop \
      --bench-set tb-2-1-canary \
      --jobs 1 \
      --reps 1 \
      --force-rerun \
      2>&1
  ) &
  local bench_pid=$!
  set -e

  sleep 1
  kill -INT "${bench_pid}" 2>/dev/null || true

  local exit_code=0
  local wait_count=0
  while (( wait_count < 30 )); do
    if ! kill -0 "${bench_pid}" 2>/dev/null; then
      wait "${bench_pid}" 2>/dev/null || exit_code=$?
      break
    fi
    sleep 0.5
    wait_count=$((wait_count + 1))
  done

  if kill -0 "${bench_pid}" 2>/dev/null; then
    kill -KILL "${bench_pid}" 2>/dev/null || true
    wait "${bench_pid}" 2>/dev/null || true
    fail "${test_name}: process did not exit after SIGINT + timeout"
    return 1
  fi

  if [[ ${exit_code} -eq 0 ]]; then
    fail "${test_name}: expected non-zero exit code on SIGINT, got ${exit_code}"
    return 1
  fi

  local interrupted_cells=0
  if [[ -d "${results_dir}/cells" ]]; then
    while IFS= read -r report; do
      [[ -z "${report}" ]] && continue
      local final_status process_outcome
      final_status="$(jq -r '.final_status // ""' "${report}" 2>/dev/null || echo '')"
      process_outcome="$(jq -r '.process_outcome // ""' "${report}" 2>/dev/null || echo '')"
      if [[ "${final_status}" == "interrupted" && "${process_outcome}" == "killed" ]]; then
        interrupted_cells=$((interrupted_cells + 1))
      fi
    done < <(find "${results_dir}/cells" -name "report.json" -type f 2>/dev/null)
  fi

  if [[ ${interrupted_cells} -gt 0 ]]; then
    pass "${test_name}"
  else
    fail "${test_name}: no interrupted cells found (exit_code=${exit_code})"
    return 1
  fi
}

# test_inflight_json_cleanup_on_signal: AC3
test_inflight_json_cleanup_on_signal() {
  local test_name="test_inflight_json_cleanup_on_signal"
  local tmpdir results_dir state_dir mock_executor tasks_dir json_path hostname
  tmpdir="$(mktemp -d)"
  results_dir="${tmpdir}/bench/results"
  state_dir="${tmpdir}/.bench-state"
  mock_executor="${tmpdir}/mock-executor"
  tasks_dir="${tmpdir}/tasks"
  trap "rm -rf '${tmpdir}'" RETURN

  hostname="$(hostname)"
  json_path="${state_dir}/${hostname}/in-flight.json"

  mkdir -p "${results_dir}" "${state_dir}" "${tasks_dir}/test-task"
  echo '{}' >"${tasks_dir}/test-task/data.json"
  create_mock_executor "${mock_executor}"

  set +e
  (
    export FIZEAU_BENCH_STATE_DIR="${state_dir}"
    export BENCH_TASK_EXECUTOR_OVERRIDE="${mock_executor}"
    export BENCH_TASKS_DIR="${tasks_dir}"
    export DEFAULT_OUT_ROOT="${results_dir}"
    cd "${SCRIPT_DIR}"
    ./benchmark \
      --profile noop \
      --bench-set tb-2-1-canary \
      --jobs 1 \
      --reps 1 \
      --force-rerun \
      2>&1
  ) &
  local bench_pid=$!
  set -e

  sleep 3
  kill -TERM "${bench_pid}" 2>/dev/null || true

  local wait_count=0
  while (( wait_count < 30 )); do
    if ! kill -0 "${bench_pid}" 2>/dev/null; then
      wait "${bench_pid}" 2>/dev/null || true
      break
    fi
    sleep 0.5
    wait_count=$((wait_count + 1))
  done

  if kill -0 "${bench_pid}" 2>/dev/null; then
    kill -KILL "${bench_pid}" 2>/dev/null || true
    wait "${bench_pid}" 2>/dev/null || true
  fi

  if [[ -f "${json_path}" ]]; then
    if ! jq -e '.' "${json_path}" >/dev/null 2>&1; then
      fail "${test_name}: in-flight.json is not valid JSON"
      return 1
    fi
    local cell_count
    cell_count="$(jq -r '.cells | length' "${json_path}" 2>/dev/null || printf '0')"
    if [[ "${cell_count}" != "0" ]]; then
      fail "${test_name}: in-flight.json cells not cleaned (count=${cell_count})"
      return 1
    fi
  fi

  pass "${test_name}"
}

main() {
  echo "Running signal handling tests for benchmark runner..."
  echo ""

  test_sigterm_marks_interrupted_and_stops_cells || true
  test_sigint_same_behavior || true
  test_inflight_json_cleanup_on_signal || true

  echo ""
  echo "========================================"
  echo "Test Summary:"
  echo "  Tests passed: $TESTS_PASSED"
  echo "  Tests failed: $TESTS_FAILED"
  echo "========================================"

  if [[ $TESTS_FAILED -gt 0 ]]; then
    exit 1
  fi
  exit 0
}

main "$@"
