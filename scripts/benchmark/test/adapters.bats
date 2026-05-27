#!/usr/bin/env bats
# Test suite for task executor and harness adapter listing

load test_helper

setup() {
  export SCRIPT_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." && pwd)"
  export TASK_EXECUTORS_DIR="${SCRIPT_DIR}/task-executors"
  export HARNESS_ADAPTERS_DIR="${SCRIPT_DIR}/harness-adapters"
}

# Test_AdapterListing_TaskExecutors: verify ./benchmark task-executors lists all executors with SUMMARY
@test "Test_AdapterListing_TaskExecutors: lists task executors with SUMMARY headers" {
  # Run task-executors command
  run "${SCRIPT_DIR}/benchmark" task-executors
  [ "$status" -eq 0 ]

  # Verify output contains expected executors
  [[ "$output" =~ "harbor" ]]
  [[ "$output" =~ "test-echo" ]]
  [[ "$output" =~ "test-fail" ]]

  # Verify SUMMARY line format (tab-separated: name \t summary)
  # Check that harbor has a summary
  echo "$output" | grep -q "^harbor[[:space:]]"
  # Check that test-echo has a summary
  echo "$output" | grep -q "^test-echo[[:space:]]"
}

# Test_AdapterListing_TaskExecutors: verify each executor has a SUMMARY line
@test "Test_AdapterListing_TaskExecutors: every executor has SUMMARY header" {
  local executor_count=0
  local found_count=0

  # Count executables in task-executors dir
  for f in "${TASK_EXECUTORS_DIR}"/*; do
    if [[ -f "$f" && -x "$f" ]]; then
      executor_count=$((executor_count + 1))
      local name="$(basename "$f")"

      # Check that executor has a SUMMARY line
      if sed -n '2{/^# SUMMARY:[[:space:]]*/{p;};q;}' "$f" | grep -q "SUMMARY"; then
        found_count=$((found_count + 1))
      fi
    fi
  done

  [ "$executor_count" -gt 0 ] || skip "No executors found"
  [ "$found_count" -eq "$executor_count" ]
}
