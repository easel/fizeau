#!/usr/bin/env bats
# Acceptance tests for harness-adapters, task-executors, and runtime-probe

# Resolve absolute path to the benchmark directory
SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
ADAPTERS_DIR="${SCRIPT_DIR}/harness-adapters"
TASK_EXECUTORS_DIR="${SCRIPT_DIR}/task-executors"
RUNTIME_PROBE="${SCRIPT_DIR}/runtime-probe.sh"
BENCHMARK_SCRIPT="${SCRIPT_DIR}/benchmark"

# AC1: ./benchmark harness-adapters lists every adapter with its # SUMMARY: line.
@test "Test_AdapterListing_Harness" {
  [ -x "$BENCHMARK_SCRIPT" ]

  local output
  output=$("$BENCHMARK_SCRIPT" harness-adapters)

  # All 8 required adapters must appear in the listing
  for adapter in fiz claude codex opencode pi cost-probe noop dumb-script; do
    echo "$output" | grep -q "^${adapter}"
  done

  # Each adapter entry must carry a non-empty summary (tab + at least one char)
  while IFS=$'\t' read -r name summary _rest; do
    [ -n "$name" ]
    [ -n "$summary" ]
  done < <(echo "$output")

  # At least 8 lines of output
  local line_count
  line_count=$(echo "$output" | wc -l)
  [ "$line_count" -ge 8 ]
}

# AC2: fiz command spec matches Python reference shape.
@test "Test_AdapterContract_fiz" {
  local profile_json='{"id":"noop","provider":{"type":"openai-compat","model":"noop-model","base_url":"http://127.0.0.1:1/noop","api_key_env":"DDX_BENCH_NOOP_KEY"},"sampling":{"temperature":0.0,"reasoning":""},"limits":{"max_output_tokens":1024,"context_tokens":8192}}'

  local cmd_spec
  cmd_spec=$(echo "$profile_json" | "$ADAPTERS_DIR/fiz" command)

  # Valid JSON
  echo "$cmd_spec" | jq . >/dev/null

  # Required top-level fields
  echo "$cmd_spec" | jq -e '.command' >/dev/null
  echo "$cmd_spec" | jq -e '.env' >/dev/null
  echo "$cmd_spec" | jq -e '.secret_env_keys' >/dev/null

  # fiz always emits FIZEAU_* env vars
  echo "$cmd_spec" | jq -e '.env.FIZEAU_MODEL' >/dev/null
  echo "$cmd_spec" | jq -e '.env.FIZEAU_BASE_URL' >/dev/null
  echo "$cmd_spec" | jq -e '.env.FIZEAU_API_KEY' >/dev/null

  # FIZEAU_API_KEY must be in secret_env_keys
  echo "$cmd_spec" | jq -e '.secret_env_keys | index("FIZEAU_API_KEY")' >/dev/null

  # command must invoke /installed-agent/fiz
  echo "$cmd_spec" | jq -re '.command[0]' | grep -q '/installed-agent/fiz'
}

# AC3: claude install emits correct harbor_plugin.
@test "Test_AdapterContract_claude" {
  local artifact="/tmp/test-artifact"

  local install_spec
  install_spec=$("$ADAPTERS_DIR/claude" install "$artifact")

  # Valid JSON with required fields
  echo "$install_spec" | jq . >/dev/null
  echo "$install_spec" | jq -e '.install_command' >/dev/null
  echo "$install_spec" | jq -e '.artifact_source' >/dev/null

  # harbor_plugin must be the ClaudeAgent import path
  local plugin
  plugin=$(echo "$install_spec" | jq -r '.harbor_plugin')
  [ "$plugin" = "scripts.benchmark.harbor_adapters.claude:ClaudeAgent" ]
}

# AC3: codex install emits correct harbor_plugin.
@test "Test_AdapterContract_codex" {
  local artifact="/tmp/test-artifact"

  local install_spec
  install_spec=$("$ADAPTERS_DIR/codex" install "$artifact")

  # Valid JSON with required fields
  echo "$install_spec" | jq . >/dev/null
  echo "$install_spec" | jq -e '.install_command' >/dev/null
  echo "$install_spec" | jq -e '.artifact_source' >/dev/null

  # harbor_plugin must be the CodexAgent import path
  local plugin
  plugin=$(echo "$install_spec" | jq -r '.harbor_plugin')
  [ "$plugin" = "scripts.benchmark.harbor_adapters.codex:CodexAgent" ]
}

# Verifies the harbor task-executor writes a valid result.json in dry-run mode.
@test "Test_harbor_executor_writes_result_json" {
  local temp_dir
  temp_dir="$(mktemp -d)"

  # Create required directories
  local cell_dir="$temp_dir/cells/test-task/20260516T103045Z-a4c1"
  mkdir -p "$cell_dir"

  local tasks_dir="$temp_dir/tasks"
  mkdir -p "$tasks_dir/test-task"

  # Create a minimal task-spec
  local task_spec="{
    \"task_id\": \"test-task\",
    \"tasks_dir\": \"$tasks_dir\",
    \"cell_dir\": \"$cell_dir\",
    \"harbor_plugin\": \"scripts.benchmark.harbor_agent:FizeauAgent\",
    \"image\": \"fizeau-harbor-runner:latest\",
    \"env\": {
      \"FIZEAU_MODEL\": \"test-model\"
    },
    \"secret_env_keys\": [],
    \"extra_args\": []
  }"

  # Run harbor executor in dry-run mode (should not require Docker)
  export HARBOR_TASK_EXECUTOR_DRY_RUN=1
  echo "$task_spec" | "$TASK_EXECUTORS_DIR/harbor" >/dev/null

  # Verify result.json was created in cell_dir
  [ -f "$cell_dir/result.json" ]

  # Verify result.json is valid JSON
  jq . "$cell_dir/result.json" >/dev/null

  # Verify result.json has expected fields in dry-run mode
  jq -e '.dry_run' "$cell_dir/result.json" >/dev/null

  # Cleanup
  rm -rf "$temp_dir"
}

# Verifies runtime-probe emits valid model_server JSON for all backends.
@test "Test_runtime_probe_all_backends" {
  local backends=(lucebox llamacpp vllm omlx ds4 rapid-mlx)

  for backend in "${backends[@]}"; do
    local profile_json="{\"id\":\"test\",\"provider\":{\"type\":\"openai-compat\",\"model\":\"test\",\"base_url\":\"http://localhost:8000/v1\"},\"metadata\":{\"runtime\":\"$backend\"},\"sampling\":{},\"limits\":{}}"

    # Run runtime-probe (may fail due to unreachable endpoint, but should emit JSON)
    local output
    output=$(echo "$profile_json" | "$RUNTIME_PROBE" 2>/dev/null || true)

    # Verify valid JSON
    echo "$output" | jq . >/dev/null

    # Verify required fields
    echo "$output" | jq -e '.name' >/dev/null
    echo "$output" | jq -e '.version' >/dev/null
    echo "$output" | jq -e '.commit' >/dev/null
    echo "$output" | jq -e '.endpoint' >/dev/null
    echo "$output" | jq -e '.status' >/dev/null

    # Verify name field matches backend
    local name
    name=$(echo "$output" | jq -r '.name')
    case "$backend" in
      lucebox) [ "$name" = "lucebox" ] ;;
      llamacpp) [ "$name" = "llama-server" ] ;;
      vllm) [ "$name" = "vllm" ] ;;
      omlx) [ "$name" = "omlx" ] ;;
      ds4) [ "$name" = "ds4" ] ;;
      rapid-mlx) [ "$name" = "rapid-mlx" ] ;;
    esac
  done
}
