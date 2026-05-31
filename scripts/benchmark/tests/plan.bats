#!/usr/bin/env bats
# Acceptance tests for --plan mode.
# Bead fizeau-2eeb9a43: AC1-3
# - Test_PlanIsPure: Verify --plan --profile sindri-lucebox --bench-set tb-2-1-canary creates no files
# - Test_PlanPrintsMatrix: Verify --plan prints 9 lines (3 tasks × 3 reps) for canary
# - Test_ListingSubcommands_profiles: Verify ./benchmark profiles lists profiles including sindri-lucebox
# - Test_ListingSubcommands_bench_sets: Verify ./benchmark bench-sets lists bench-sets including tb-2-1-canary
# - Test_Validate: Verify ./benchmark validate exits 0

SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
BENCHMARK="${SCRIPT_DIR}/benchmark"

# Test 1: --plan is pure: no files created
@test "Test_PlanIsPure" {
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" RETURN

  # Run --plan with explicit --out to be sure it doesn't create anything
  local output
  output="$("${BENCHMARK}" \
    --profile sindri-lucebox \
    --bench-set tb-2-1-canary \
    --plan \
    --out "${tmpdir}/should-not-be-created" 2>&1)"

  # Verify the output directory was not created
  [[ ! -d "${tmpdir}/should-not-be-created" ]]

  # Verify no files were written at all in tmpdir
  [[ ! "$(find "${tmpdir}" -type f 2>/dev/null)" ]]
}

# Test 2: --plan prints expected matrix (3 tasks × 3 reps = 9 lines)
@test "Test_PlanPrintsMatrix" {
  local output line_count
  output="$("${BENCHMARK}" \
    --profile sindri-lucebox \
    --bench-set tb-2-1-canary \
    --plan 2>&1)"

  line_count="$(printf '%s\n' "${output}" | wc -l)"

  # tb-2-1-canary has 3 tasks and default_reps=3, so 3*3=9 lines
  [[ "${line_count}" -eq 9 ]]

  # Verify each line contains expected fields
  while IFS= read -r line; do
    [[ -z "${line}" ]] && continue
    [[ "${line}" =~ profile=sindri-lucebox ]]
    [[ "${line}" =~ bench_set=tb-2-1-canary ]]
    [[ "${line}" =~ framework=terminal-bench ]]
    [[ "${line}" =~ dataset=terminal-bench-2-1 ]]
    [[ "${line}" =~ task= ]]
    [[ "${line}" =~ rep= ]]
  done <<<"${output}"
}

# Test 3: ./benchmark profiles lists profiles including sindri-lucebox
@test "Test_ListingSubcommands_profiles" {
  local output
  output="$("${BENCHMARK}" profiles 2>&1)"

  # Verify sindri-lucebox is in the output
  printf '%s\n' "${output}" | grep -Fxq 'sindri-lucebox'

  # Verify output is sorted
  local sorted
  sorted="$(printf '%s\n' "${output}" | LC_ALL=C sort -u)"
  [[ "${output}" == "${sorted}" ]]
}

# Test 4: ./benchmark bench-sets lists bench-sets including tb-2-1-canary
@test "Test_ListingSubcommands_bench_sets" {
  local output
  output="$("${BENCHMARK}" bench-sets 2>&1)"

  # Verify tb-2-1-canary is in the output
  printf '%s\n' "${output}" | grep -Fxq 'tb-2-1-canary'

  # Verify output is sorted
  local sorted
  sorted="$(printf '%s\n' "${output}" | LC_ALL=C sort -u)"
  [[ "${output}" == "${sorted}" ]]
}

# Test 5: ./benchmark validate exits 0
@test "Test_Validate" {
  # validate should complete successfully
  "${BENCHMARK}" validate >/dev/null 2>&1
  # Exit code is already verified by @test framework (exits non-zero = failure)
}

# Test 6: ./benchmark task-executors lists task executors
@test "TestListSubcommands_task_executors" {
  local output
  output="$("${BENCHMARK}" task-executors 2>&1)"

  # Verify harbor executor is listed (it should exist)
  printf '%s\n' "${output}" | grep -q 'harbor'

  # Verify output is not empty
  [[ -n "${output}" ]]

  # Verify output is sorted by name (check first field before tab)
  local names
  names="$(printf '%s\n' "${output}" | cut -f1)"
  local sorted
  sorted="$(printf '%s\n' "${names}" | LC_ALL=C sort -u)"
  [[ "${names}" == "${sorted}" ]]
}

# Test 7: ./benchmark harness-adapters lists harness adapters
@test "TestListSubcommands_harness_adapters" {
  local output
  output="$("${BENCHMARK}" harness-adapters 2>&1)"

  # Verify fiz adapter is listed (it should exist)
  printf '%s\n' "${output}" | grep -q 'fiz'

  # Verify output is not empty
  [[ -n "${output}" ]]

  # Verify output is sorted
  local sorted
  sorted="$(printf '%s\n' "${output}" | LC_ALL=C sort -u)"
  [[ "${output}" == "${sorted}" ]]
}
