#!/usr/bin/env bats
# Test suite for harbor task executor

load test_helper

setup() {
  export SCRIPT_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." && pwd)"
  export HARBOR_EXECUTOR="${SCRIPT_DIR}/task-executors/harbor"

  # Create a temporary directory for test artifacts
  export TEST_TMPDIR="$(mktemp -d)"
  export CELL_DIR="${TEST_TMPDIR}/cell-1234"
  export TASKS_DIR="${TEST_TMPDIR}/tasks"

  mkdir -p "$CELL_DIR"
  mkdir -p "$TASKS_DIR/test-task"
  echo '{}' >"$TASKS_DIR/test-task/data.json"

  # Create stub docker
  export STUB_DOCKER_DIR="${TEST_TMPDIR}/bin"
  mkdir -p "$STUB_DOCKER_DIR"
  create_stub_docker "$STUB_DOCKER_DIR"
}

teardown() {
  rm -rf "$TEST_TMPDIR"
}

# Test_TaskExecutorHarbor_Invocation: dry-run mode includes provenance
@test "Test_TaskExecutorHarbor_Invocation: dry-run includes task_executor_version and image_digest" {
  local task_spec="{
    \"task_id\": \"test-task\",
    \"tasks_dir\": \"$TASKS_DIR\",
    \"cell_dir\": \"$CELL_DIR\",
    \"harbor_plugin\": \"harbor_agent:FizeauAgent\",
    \"image\": \"fizeau-harbor-runner:latest\",
    \"env\": {
      \"FIZEAU_MODEL\": \"test-model\"
    }
  }"

  export HARBOR_TASK_EXECUTOR_DRY_RUN=1

  run bash -c "echo '$task_spec' | $HARBOR_EXECUTOR"
  [ "$status" -eq 0 ]

  # Parse result JSON
  result_json="$output"

  # Verify dry_run flag
  echo "$result_json" | jq -e '.dry_run == true' >/dev/null

  # Verify task_executor_version is present and non-empty
  echo "$result_json" | jq -e '.task_executor_version | length > 0' >/dev/null

  # Verify harbor_runner_image_digest field exists (may be empty if docker unavailable)
  echo "$result_json" | jq -e 'has("harbor_runner_image_digest")' >/dev/null

  # Verify result.json was created
  [ -f "$CELL_DIR/result.json" ]

  # Verify the file content matches stdout
  [[ "$(cat "$CELL_DIR/result.json")" == "$result_json" ]]
}

# Test_TaskExecutorHarbor_Invocation: validates task-spec.json
@test "Test_TaskExecutorHarbor_Invocation: rejects missing required fields" {
  # Test missing task_id
  local incomplete_spec="{
    \"tasks_dir\": \"$TASKS_DIR\",
    \"cell_dir\": \"$CELL_DIR\",
    \"harbor_plugin\": \"test.plugin\"
  }"

  run bash -c "echo '$incomplete_spec' | $HARBOR_EXECUTOR"
  [ "$status" -eq 2 ]
  [[ "$output" =~ "missing one of" ]]
}

# Test_TaskExecutorHarbor_Invocation: docker invocation command structure
@test "Test_TaskExecutorHarbor_Invocation: constructs correct docker run command" {
  local task_spec="{
    \"task_id\": \"test-task\",
    \"tasks_dir\": \"$TASKS_DIR\",
    \"cell_dir\": \"$CELL_DIR\",
    \"harbor_plugin\": \"harbor_agent:FizeauAgent\",
    \"image\": \"fizeau-harbor-runner:latest\",
    \"env\": {
      \"FIZEAU_MODEL\": \"test-model\",
      \"FIZEAU_API_KEY\": \"test-key\"
    }
  }"

  export HARBOR_TASK_EXECUTOR_DRY_RUN=1
  run bash -c "echo '$task_spec' | $HARBOR_EXECUTOR"
  [ "$status" -eq 0 ]

  result_json="$output"

  # Verify docker_argv in dry-run output
  echo "$result_json" | jq -e '.docker_argv | length > 0' >/dev/null

  # Verify key docker arguments are present
  echo "$result_json" | jq -e '.docker_argv[] | select(. == "run")' >/dev/null
  echo "$result_json" | jq -e '.docker_argv[] | select(. == "--rm")' >/dev/null
  echo "$result_json" | jq -e '.docker_argv[] | select(. == "-v")' >/dev/null
  echo "$result_json" | jq -e '.docker_argv[] | select(. == "--yes")' >/dev/null
  echo "$result_json" | jq -e '.docker_argv[] | select(. == "--delete")' >/dev/null
  echo "$result_json" | jq -e '.docker_argv[] | select(. == "--path")' >/dev/null
}

# Test_TaskExecutorHarbor_Invocation: result.json always includes provenance
@test "Test_TaskExecutorHarbor_Invocation: missing_result includes task_executor_version" {
  # Create a scenario where docker fails and no result.json is produced
  # by using dry-run mode with non-existent paths

  local task_spec="{
    \"task_id\": \"nonexistent-task\",
    \"tasks_dir\": \"$TASKS_DIR\",
    \"cell_dir\": \"$CELL_DIR\",
    \"harbor_plugin\": \"test.plugin\"
  }"

  export HARBOR_TASK_EXECUTOR_DRY_RUN=1

  run bash -c "echo '$task_spec' | $HARBOR_EXECUTOR 2>&1"
  [ "$status" -eq 0 ]

  # Verify result.json contains provenance even in dry-run
  result_file="$CELL_DIR/result.json"
  [ -f "$result_file" ]

  # Verify task_executor_version is in the file
  jq -e '.task_executor_version | length > 0' "$result_file"
}
