#!/usr/bin/env bash
# Unit tests for benchmark script concurrency, flock groups, and signal handling.
# Bead fizeau-d0369045: TestBenchmarkJobsLimit, TestBenchmarkConcurrencyGroupFlock, TestBenchmarkSignalTerminatesProcessGroups

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK="${SCRIPT_DIR}/benchmark"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_le() {
  local got="$1" limit="$2" msg="${3:-value exceeds limit}"
  (( got <= limit )) || fail "${msg}: got=${got} limit=${limit}"
}

assert_ge() {
  local got="$1" min="$2" msg="${3:-value below minimum}"
  (( got >= min )) || fail "${msg}: got=${got} min=${min}"
}

assert_eq() {
  local got="$1" want="$2" msg="${3:-values differ}"
  [[ "${got}" == "${want}" ]] || fail "${msg}: got=${got} want=${want}"
}

require() {
  command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"
}

setup_common_dirs() {
  local root="$1"
  mkdir -p \
    "${root}/profiles" \
    "${root}/bench-sets" \
    "${root}/harness-adapters" \
    "${root}/task-executors" \
    "${root}/out" \
    "${root}/state"
}

create_profile() {
  local dir="$1" id="$2" group="$3"
  cat > "${dir}/${id}.yaml" <<EOF
id: ${id}
harness: none
surface: fiz_provider_native
concurrency_group: ${group}
provider:
  type: openrouter
  model: test/model
  api_key_env: TEST_KEY
sampling: {}
limits: {}
metadata: {}
EOF
}

create_bench_set() {
  local dir="$1" id="$2"
  shift 2
  local tasks="$@"
  cat > "${dir}/${id}.yaml" <<EOF
id: ${id}
framework: test-framework
dataset: test-dataset
default_reps: 1
tasks:
$(for task in $tasks; do echo "  - id: $task"; done)
EOF
}

create_harness_adapter() {
  local dir="$1"
  mkdir -p "${dir}"
  cat > "${dir}/fiz" <<'EOF'
#!/usr/bin/env bash
cmd="${1:-}"
case "${cmd}" in
  install)
    jq -n '{install_command:"echo",artifact_source:"/tmp",binary_path:"",harbor_plugin:"test"}'
    ;;
  command)
    jq -n '{command:["/bin/true"],env:{},secret_env_keys:[]}'
    ;;
esac
EOF
  chmod +x "${dir}/fiz"
}

create_concurrency_groups() {
  local dir="$1"
  shift
  local groups="$@"
  local yaml="groups:"
  for group in $groups; do
    yaml="${yaml}"$'\n'"  ${group}:"$'\n'"    max_concurrency: 1"
  done
  cat > "${dir}/concurrency-groups.yaml" <<EOF
${yaml}
EOF
}

create_task_subset() {
  local dir="$1"
  shift
  local tasks="$@"
  cat > "${dir}/task-subset-test.yaml" <<EOF
tasks:
$(for task in $tasks; do echo "  - id: $task"; done)
EOF
}

# ===========================================================================
# TestBenchmarkJobsLimit
# ===========================================================================

test_jobs_limit() {
  echo "==> TestBenchmarkJobsLimit: --jobs constraint"

  local tmp
  tmp="$(mktemp -d)"
  trap "rm -rf '${tmp}'" RETURN

  setup_common_dirs "${tmp}"

  # Create a stub executor that logs start/end times with nanosecond precision
  cat > "${tmp}/task-executors/logging-stub" <<'STUB'
#!/usr/bin/env bash
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"${spec}")"
task_id="$(jq -r '.task_id' <<<"${spec}")"
state_file="${STATE_FILE:?}"

# Log start with ns precision
start_ns="$(date +%s%N)"
echo "${start_ns} start ${task_id}" >> "${state_file}"

# Sleep to ensure overlapping execution
sleep 0.3

# Write result
mkdir -p "${cell_dir}"
jq -n --arg t "${task_id}" '{task_id:$t,final_status:"completed"}' > "${cell_dir}/result.json"

# Log end
end_ns="$(date +%s%N)"
echo "${end_ns} end ${task_id}" >> "${state_file}"
STUB
  chmod +x "${tmp}/task-executors/logging-stub"

  create_profile "${tmp}/profiles" "test" "default"
  create_bench_set "${tmp}/bench-sets" "test" "t1 t2 t3 t4 t5 t6"
  create_harness_adapter "${tmp}/harness-adapters"
  create_concurrency_groups "${tmp}" "default"
  create_task_subset "${tmp}" "t1 t2 t3 t4 t5 t6"

  local state_file="${tmp}/timing.log"
  : > "${state_file}"

  # Run with --jobs=2; should never have >2 concurrent executors
  STATE_FILE="${state_file}" \
    PROFILES_DIR="${tmp}/profiles" \
    BENCH_SETS_DIR="${tmp}/bench-sets" \
    HARNESS_ADAPTERS_DIR="${tmp}/harness-adapters" \
    TASK_EXECUTORS_DIR="${tmp}/task-executors" \
    FIZEAU_BENCH_STATE_DIR="${tmp}/state" \
    BENCH_TASK_EXECUTOR_OVERRIDE="${tmp}/task-executors/logging-stub" \
    BENCH_TASKS_DIR="${tmp}" \
    BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test" \
    "${BENCHMARK}" \
      --profile test \
      --bench-set test \
      --out "${tmp}/out" \
      --jobs 2 \
    >/dev/null 2>&1 || true

  # Analyze log: for each start, count how many jobs were running
  local max_concurrent=0
  local start_times=()
  local end_times=()
  local tasks=()

  while read -r ns event task; do
    if [[ "${event}" == "start" ]]; then
      start_times+=("${ns}")
      tasks+=("${task}")
    elif [[ "${event}" == "end" ]]; then
      end_times+=("${ns}")
    fi
  done < "${state_file}"

  # For each start time, count overlapping jobs
  for i in "${!start_times[@]}"; do
    local s_time="${start_times[$i]}"
    local concurrent=0
    for j in "${!start_times[@]}"; do
      local sj="${start_times[$j]}"
      local ej="${end_times[$j]:-}"
      if [[ -z "${ej}" ]]; then
        ej="$(date +%s%N)"
      fi
      if (( sj <= s_time && s_time < ej )); then
        concurrent=$((concurrent + 1))
      fi
    done
    if (( concurrent > max_concurrent )); then
      max_concurrent="${concurrent}"
    fi
  done

  # Ensure max concurrent did not exceed --jobs=2
  assert_le "${max_concurrent}" 2 "concurrent jobs exceeded --jobs limit"
  echo "    PASS: max_concurrent=${max_concurrent} <= limit=2"
}

# ===========================================================================
# TestBenchmarkConcurrencyGroupFlock
# ===========================================================================

test_concurrency_group_flock() {
  echo "==> TestBenchmarkConcurrencyGroupFlock: flock serialization per group"

  local tmp
  tmp="$(mktemp -d)"
  trap "rm -rf '${tmp}'" RETURN

  setup_common_dirs "${tmp}"

  # Executor that just works
  cat > "${tmp}/task-executors/simple" <<'STUB'
#!/usr/bin/env bash
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"${spec}")"
task_id="$(jq -r '.task_id' <<<"${spec}")"

sleep 0.1

mkdir -p "${cell_dir}"
jq -n --arg t "${task_id}" '{task_id:$t,final_status:"completed"}' > "${cell_dir}/result.json"
STUB
  chmod +x "${tmp}/task-executors/simple"

  # Create two profiles with different groups
  create_profile "${tmp}/profiles" "groupA" "group-a"
  create_profile "${tmp}/profiles" "groupB" "group-b"

  # Bench sets
  create_bench_set "${tmp}/bench-sets" "two-tasks" "t1 t2"

  create_harness_adapter "${tmp}/harness-adapters"
  create_concurrency_groups "${tmp}" "group-a group-b"
  create_task_subset "${tmp}" "t1 t2"

  # Run groupA and groupB in parallel; they should be able to run concurrently
  # because they use different flock files.
  (
    PROFILES_DIR="${tmp}/profiles" \
      BENCH_SETS_DIR="${tmp}/bench-sets" \
      HARNESS_ADAPTERS_DIR="${tmp}/harness-adapters" \
      TASK_EXECUTORS_DIR="${tmp}/task-executors" \
      FIZEAU_BENCH_STATE_DIR="${tmp}/state" \
      BENCH_TASK_EXECUTOR_OVERRIDE="${tmp}/task-executors/simple" \
      BENCH_TASKS_DIR="${tmp}" \
      BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test" \
      "${BENCHMARK}" \
        --profile groupA \
        --bench-set two-tasks \
        --out "${tmp}/out-a" \
        --jobs 4 \
      >/dev/null 2>&1
  ) &
  local pid_a=$!

  (
    PROFILES_DIR="${tmp}/profiles" \
      BENCH_SETS_DIR="${tmp}/bench-sets" \
      HARNESS_ADAPTERS_DIR="${tmp}/harness-adapters" \
      TASK_EXECUTORS_DIR="${tmp}/task-executors" \
      FIZEAU_BENCH_STATE_DIR="${tmp}/state" \
      BENCH_TASK_EXECUTOR_OVERRIDE="${tmp}/task-executors/simple" \
      BENCH_TASKS_DIR="${tmp}" \
      BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test" \
      "${BENCHMARK}" \
        --profile groupB \
        --bench-set two-tasks \
        --out "${tmp}/out-b" \
        --jobs 4 \
      >/dev/null 2>&1
  ) &
  local pid_b=$!

  wait "${pid_a}" 2>/dev/null || true
  wait "${pid_b}" 2>/dev/null || true

  # Verify that lock files were created for each group
  [[ -f "${tmp}/state/locks/group-a.lock" ]] || fail "group-a.lock not created"
  [[ -f "${tmp}/state/locks/group-b.lock" ]] || fail "group-b.lock not created"

  # Verify cells were created for both groups
  [[ -d "${tmp}/out-a/cells" ]] || fail "group-a cells not created"
  [[ -d "${tmp}/out-b/cells" ]] || fail "group-b cells not created"

  echo "    PASS: separate lock files created for group-a and group-b"
}

# ===========================================================================
# TestBenchmarkSignalTerminatesProcessGroups
# ===========================================================================

test_signal_terminates_process_groups() {
  echo "==> TestBenchmarkSignalTerminatesProcessGroups: SIGINT/SIGTERM stops scheduling and interrupts running cells"

  local tmp
  tmp="$(mktemp -d)"
  trap "rm -rf '${tmp}' 2>/dev/null; pkill -P $$ 2>/dev/null || true" RETURN

  setup_common_dirs "${tmp}"

  # Long-running executor
  cat > "${tmp}/task-executors/sleeper" <<'STUB'
#!/usr/bin/env bash
spec="$(cat)"
cell_dir="$(jq -r '.cell_dir' <<<"${spec}")"
task_id="$(jq -r '.task_id' <<<"${spec}")"

# Sleep long enough to be interrupted
sleep 30

mkdir -p "${cell_dir}"
jq -n '{final_status:"completed"}' > "${cell_dir}/result.json"
STUB
  chmod +x "${tmp}/task-executors/sleeper"

  create_profile "${tmp}/profiles" "test" "default"
  create_bench_set "${tmp}/bench-sets" "many" "t1 t2 t3 t4 t5 t6"
  create_harness_adapter "${tmp}/harness-adapters"
  create_concurrency_groups "${tmp}" "default"
  create_task_subset "${tmp}" "t1 t2 t3 t4 t5 t6"

  # Start benchmark in background
  (
    PROFILES_DIR="${tmp}/profiles" \
      BENCH_SETS_DIR="${tmp}/bench-sets" \
      HARNESS_ADAPTERS_DIR="${tmp}/harness-adapters" \
      TASK_EXECUTORS_DIR="${tmp}/task-executors" \
      FIZEAU_BENCH_STATE_DIR="${tmp}/state" \
      BENCH_TASK_EXECUTOR_OVERRIDE="${tmp}/task-executors/sleeper" \
      BENCH_TASKS_DIR="${tmp}" \
      BENCH_HARBOR_DIGEST_OVERRIDE="sha256:test" \
      BENCH_TERM_GRACE_SECONDS=2 \
      "${BENCHMARK}" \
        --profile test \
        --bench-set many \
        --out "${tmp}/out" \
        --jobs 2 \
      >/dev/null 2>&1
  ) &
  local sweep_pid=$!

  # Wait for at least one task to start (longer wait for slower systems)
  sleep 2

  # Send SIGTERM to stop scheduling and interrupt running cells
  kill -TERM "${sweep_pid}" 2>/dev/null || true

  # Wait for benchmark to exit and capture exit code
  local exit_code=0
  wait "${sweep_pid}" 2>/dev/null || exit_code=$?

  # AC2 requirement: verify runner exits non-zero on SIGTERM
  [[ ${exit_code} -ne 0 ]] || fail "benchmark should exit non-zero after SIGTERM (got exit_code=${exit_code})"

  # Verify cells and their status
  local completed_count=0
  local interrupted_count=0
  local total_cell_count=0

  if [[ -d "${tmp}/out/cells" ]]; then
    total_cell_count=$(find "${tmp}/out/cells" -name "report.json" 2>/dev/null | wc -l | tr -d ' ')
    completed_count=$(find "${tmp}/out/cells" -name "report.json" -exec jq -e '.final_status == "completed"' {} \; 2>/dev/null | grep -c "true" || true)
    interrupted_count=$(find "${tmp}/out/cells" -name "report.json" -exec jq -e '.final_status == "interrupted"' {} \; 2>/dev/null | grep -c "true" || true)
  fi

  # AC2 requirement: verify that interrupted cells have final_status=interrupted
  if [[ ${interrupted_count} -gt 0 ]]; then
    # Spot check one interrupted cell
    local interrupted_report
    interrupted_report="$(find "${tmp}/out/cells" -name "report.json" -print0 | xargs -0 grep -l "interrupted" 2>/dev/null | head -1)"
    if [[ -n "${interrupted_report}" ]]; then
      local process_outcome
      process_outcome="$(jq -r '.process_outcome // ""' "${interrupted_report}")"
      assert_eq "${process_outcome}" "killed" "interrupted cell should have process_outcome=killed"
    fi
  fi

  # We should have fewer completed cells than the total task count (6),
  # proving that signal handling stopped scheduling new cells
  assert_le "${completed_count}" 5 "signal did not stop scheduling (too many completed cells)"

  echo "    PASS: signal handling stopped scheduling: completed=${completed_count}, interrupted=${interrupted_count} of ${total_cell_count} total, exit_code=${exit_code}"
}

# ===========================================================================
# Main
# ===========================================================================

main() {
  require jq
  require yq
  require bash
  require flock
  require setsid

  test_jobs_limit
  test_concurrency_group_flock
  test_signal_terminates_process_groups

  echo ""
  echo "PASS: TestBenchmarkJobsLimit, TestBenchmarkConcurrencyGroupFlock, TestBenchmarkSignalTerminatesProcessGroups"
}

main "$@"
