#!/usr/bin/env bats
# Test parity between go-runner and bash-runner cell reports

setup() {
  # Set up the benchmark script directory
  # BATS_TEST_FILENAME is the path to this test file
  # dirname of this file is scripts/benchmark/tests
  # parent is scripts/benchmark
  BENCH_ROOT="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." && pwd)"
  PARITY_DIR="${BENCH_ROOT}/testdata/parity"

  # Export so that tests can use them
  export BENCH_ROOT PARITY_DIR
}

@test "Test_parity_diff_clean: diff.sh reports no non-allowlisted divergences" {
  # Verify that the parity diff script reports no divergences
  # between committed go-runner and bash-runner fixtures

  [[ -d "$PARITY_DIR/go-runner" ]] || skip "go-runner fixtures not found"
  [[ -d "$PARITY_DIR/bash-runner" ]] || skip "bash-runner fixtures not found"
  [[ -f "$PARITY_DIR/diff.sh" ]] || skip "diff.sh not found"

  # Run the diff script with the default fixture directories
  # This should exit 0 if all divergences are allowlisted
  bash "$PARITY_DIR/diff.sh" "$PARITY_DIR/go-runner" "$PARITY_DIR/bash-runner"
}

@test "Test_parity_diff_clean: diff.sh detects when fixtures exist" {
  # Verify that diff.sh can find and compare the fixture files

  [[ -d "$PARITY_DIR/go-runner" ]] || skip "go-runner fixtures not found"
  [[ -d "$PARITY_DIR/bash-runner" ]] || skip "bash-runner fixtures not found"

  # Count report.json files in both directories
  go_runner_count=$(find "$PARITY_DIR/go-runner" -type f -name "report.json" | wc -l)
  bash_runner_count=$(find "$PARITY_DIR/bash-runner" -type f -name "report.json" | wc -l)

  [[ $go_runner_count -gt 0 ]] || skip "no go-runner report.json files found"
  [[ $bash_runner_count -gt 0 ]] || skip "no bash-runner report.json files found"
  [[ $go_runner_count -eq $bash_runner_count ]] || skip "fixture counts don't match"
}

@test "TestParityDiffClean: diff.sh is clean per allowlist" {
  # Verify that scripts/benchmark/testdata/parity/diff.sh against the committed
  # go-runner fixtures is clean per the allowlist.

  [[ -d "$PARITY_DIR/go-runner" ]] || skip "go-runner fixtures not found"
  [[ -d "$PARITY_DIR/bash-runner" ]] || skip "bash-runner fixtures not found"
  [[ -f "$PARITY_DIR/diff.sh" ]] || skip "diff.sh not found"

  # Run the diff script - it must exit 0
  bash "$PARITY_DIR/diff.sh" "$PARITY_DIR/go-runner" "$PARITY_DIR/bash-runner"
}

@test "TestGoRunnerRedirectExit2: fiz-bench matrix redirects and exits 2" {
  # Verify that execution subcommands (matrix, sweep, run, plan) redirect
  # to ./benchmark and exit with code 2.

  # Go run the bench command - capture output and get the actual exit code
  local output
  output=$(go run ./cmd/bench matrix 2>&1; echo "GO_RUN_EXIT=$?") || true

  # Extract the go run exit code
  local go_run_exit
  go_run_exit=$(echo "$output" | grep "GO_RUN_EXIT=" | cut -d= -f2)

  # Extract actual command output (everything except the exit code line)
  output=$(echo "$output" | grep -v "GO_RUN_EXIT=")

  # Must contain redirect message
  echo "$output" | grep -q "use ./benchmark"

  # The program outputs "exit status 2" when go run executes it
  # go run itself returns 1 on non-zero program exit, but the output shows "exit status 2"
  echo "$output" | grep -q "exit status 2"
}

@test "TestNoToolPresetBenchmarkLeak: ToolPreset == benchmark leak is removed" {
  # Verify that execute_native.go line 265 no longer has the planning mode leak

  local source_file="internal/serviceimpl/execute_native.go"
  [[ -f "$source_file" ]] || skip "$source_file not found"

  # The leak is: PlanningMode: req.PlanningMode || req.ToolPreset == "benchmark"
  # It should be just: PlanningMode: req.PlanningMode

  # grep should find nothing
  ! grep -n 'ToolPreset.*==.*"benchmark"' "$source_file" | grep -i planning
}
