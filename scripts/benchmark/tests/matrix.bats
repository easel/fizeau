#!/usr/bin/env bats
# Acceptance tests for matrix expansion.
# Bead fizeau-2eeb9a43: AC4
# - Test_MatrixExpansion_inline_tasks: Create bench-set with inline tasks, verify --plan produces 4 lines (2 tasks × 2 reps)
# - Test_MatrixExpansion_default_executor_harbor: Verify framework=terminal-bench defaults to task_executor=harbor
# - TestMatrixExpandTasksFromAndInline: Verify tasks_from resolution merges with inline tasks

SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
BENCHMARK="${SCRIPT_DIR}/benchmark"
BENCH_SETS_DIR="${SCRIPT_DIR}/bench-sets"

# Test 1: Matrix expansion with inline tasks
@test "Test_MatrixExpansion_inline_tasks" {
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  # Create a temporary bench-set with inline tasks
  local test_bench_set="${tmpdir}/test-inline.yaml"
  cat > "${test_bench_set}" <<'BENCHSET'
id: test-inline
framework: terminal-bench
dataset: test-dataset
default_reps: 2
tasks:
  - task-1
  - task-2
BENCHSET

  # Create a minimal temporary bench-sets directory override
  local temp_bench_sets_dir="${tmpdir}/bench-sets"
  mkdir -p "${temp_bench_sets_dir}"
  cp "${test_bench_set}" "${temp_bench_sets_dir}/"

  # Run --plan with the inline bench-set (2 tasks × 2 reps = 4 lines)
  local output
  output="$(BENCH_SETS_DIR="${temp_bench_sets_dir}" "${BENCHMARK}" \
    --profile sindri-lucebox \
    --bench-set test-inline \
    --plan 2>&1)"

  local line_count
  line_count="$(printf '%s\n' "${output}" | wc -l)"

  # Should produce 4 lines: 2 tasks × 2 reps
  [[ "${line_count}" -eq 4 ]]

  # Verify tasks appear in output
  printf '%s\n' "${output}" | grep -Fq 'task=task-1'
  printf '%s\n' "${output}" | grep -Fq 'task=task-2'
}

# Test 2: Default task executor for terminal-bench framework is harbor
@test "Test_MatrixExpansion_default_executor_harbor" {
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  # Create a bench-set without explicit task_executor
  local test_bench_set="${tmpdir}/test-no-executor.yaml"
  cat > "${test_bench_set}" <<'BENCHSET'
id: test-no-executor
framework: terminal-bench
dataset: test-dataset
default_reps: 1
tasks:
  - test-task
BENCHSET

  # Create a minimal temporary bench-sets directory override
  local temp_bench_sets_dir="${tmpdir}/bench-sets"
  mkdir -p "${temp_bench_sets_dir}"
  cp "${test_bench_set}" "${temp_bench_sets_dir}/"

  # Run --plan and verify task_executor defaults to harbor
  local output
  output="$(BENCH_SETS_DIR="${temp_bench_sets_dir}" "${BENCHMARK}" \
    --profile sindri-lucebox \
    --bench-set test-no-executor \
    --plan 2>&1)"

  # Verify task_executor=harbor is in the output
  printf '%s\n' "${output}" | grep -Fq 'task_executor=harbor'
}

# Test 3: Matrix expansion with tasks_from references
@test "TestMatrixExpandTasksFromAndInline" {
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  # Create a temporary bench-sets directory
  local temp_bench_sets_dir="${tmpdir}/bench-sets"
  mkdir -p "${temp_bench_sets_dir}"

  # Create a separate task manifest file
  local task_manifest="${temp_bench_sets_dir}/task-manifest.yaml"
  cat > "${task_manifest}" <<'MANIFEST'
tasks:
  - id: external-task-1
  - id: external-task-2
MANIFEST

  # Create a bench-set with both tasks_from and inline tasks
  local test_bench_set="${temp_bench_sets_dir}/test-mixed.yaml"
  cat > "${test_bench_set}" <<'BENCHSET'
id: test-mixed
framework: terminal-bench
dataset: test-dataset
default_reps: 1
tasks_from: task-manifest.yaml
tasks:
  - inline-task-1
  - inline-task-2
BENCHSET

  # Run --plan with the bench-set (tasks_from yields 2 tasks, inline adds 2, total 4 unique × 1 rep = 4 lines)
  local output
  output="$(BENCH_SETS_DIR="${temp_bench_sets_dir}" "${BENCHMARK}" \
    --profile sindri-lucebox \
    --bench-set test-mixed \
    --plan 2>&1)"

  local line_count
  line_count="$(printf '%s\n' "${output}" | wc -l)"

  # Should produce 4 lines: 4 unique tasks × 1 rep (tasks_from + inline merged without duplicates)
  [[ "${line_count}" -eq 4 ]]

  # Verify all tasks appear in output
  printf '%s\n' "${output}" | grep -Fq 'task=external-task-1'
  printf '%s\n' "${output}" | grep -Fq 'task=external-task-2'
  printf '%s\n' "${output}" | grep -Fq 'task=inline-task-1'
  printf '%s\n' "${output}" | grep -Fq 'task=inline-task-2'

  # Verify ordered: tasks_from comes first, then inline
  local external_1_line inline_1_line
  external_1_line="$(printf '%s\n' "${output}" | grep -n 'external-task-1' | cut -d: -f1)"
  inline_1_line="$(printf '%s\n' "${output}" | grep -n 'inline-task-1' | cut -d: -f1)"
  [[ "${external_1_line}" -lt "${inline_1_line}" ]]
}
