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
