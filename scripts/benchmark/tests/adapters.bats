#!/usr/bin/env bats
# Acceptance tests for harness-adapters, task-executors, and runtime-probe

# Resolve absolute path to the benchmark directory
SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
ADAPTERS_DIR="${SCRIPT_DIR}/harness-adapters"
TASK_EXECUTORS_DIR="${SCRIPT_DIR}/task-executors"
RUNTIME_PROBE="${SCRIPT_DIR}/runtime-probe.sh"

# Test 1: Each adapter has SUMMARY header and is executable
@test "Test_each_adapter_has_summary_header" {
  local adapters=(fiz claude codex opencode pi cost-probe noop dumb-script)

  for adapter in "${adapters[@]}"; do
    local adapter_path="${ADAPTERS_DIR}/${adapter}"

    # Check file exists
    [ -f "$adapter_path" ]

    # Check is executable
    [ -x "$adapter_path" ]

    # Check for SUMMARY header on line 2 (after shebang)
    head -2 "$adapter_path" | grep -q '^# SUMMARY:'
  done
}

# Test 2: fiz command spec matches Python reference
@test "Test_fiz_command_spec_matches_python_reference" {
  # Use noop profile as a simple reference case
  local profile_json='{"id":"noop","provider":{"type":"openai-compat","model":"noop-model","base_url":"http://127.0.0.1:1/noop","api_key_env":"DDX_BENCH_NOOP_KEY"},"sampling":{"temperature":0.0,"reasoning":""},"limits":{"max_output_tokens":1024,"context_tokens":8192}}'

  # Generate command spec
  local cmd_spec
  cmd_spec=$(echo "$profile_json" | "$ADAPTERS_DIR/fiz" command)

  # Verify it's valid JSON
  echo "$cmd_spec" | jq . >/dev/null

  # Verify required fields exist
  echo "$cmd_spec" | jq -e '.command' >/dev/null
  echo "$cmd_spec" | jq -e '.env' >/dev/null
  echo "$cmd_spec" | jq -e '.secret_env_keys' >/dev/null

  # Verify env contains expected FIZEAU_* vars
  echo "$cmd_spec" | jq -e '.env.FIZEAU_MODEL' >/dev/null
  echo "$cmd_spec" | jq -e '.env.FIZEAU_BASE_URL' >/dev/null
  echo "$cmd_spec" | jq -e '.env.FIZEAU_API_KEY' >/dev/null

  # Verify secret_env_keys includes FIZEAU_API_KEY
  echo "$cmd_spec" | jq -e '.secret_env_keys | index("FIZEAU_API_KEY")' >/dev/null
}

# Test 3: install spec names harbor_plugin
@test "Test_install_spec_names_harbor_plugin" {
  local artifact="/tmp/test-agent"

  # Test fiz adapter
  local fiz_install
  fiz_install=$("$ADAPTERS_DIR/fiz" install "$artifact")
  echo "$fiz_install" | jq . >/dev/null
  local fiz_plugin
  fiz_plugin=$(echo "$fiz_install" | jq -r '.harbor_plugin // ""')
  [ -n "$fiz_plugin" ]

  # Test claude adapter
  local claude_install
  claude_install=$("$ADAPTERS_DIR/claude" install "$artifact")
  echo "$claude_install" | jq . >/dev/null
  local claude_plugin
  claude_plugin=$(echo "$claude_install" | jq -r '.harbor_plugin // ""')
  [ -n "$claude_plugin" ]

  # Test codex adapter
  local codex_install
  codex_install=$("$ADAPTERS_DIR/codex" install "$artifact")
  echo "$codex_install" | jq . >/dev/null
  local codex_plugin
  codex_plugin=$(echo "$codex_install" | jq -r '.harbor_plugin // ""')
  [ -n "$codex_plugin" ]
}

# Test 4: harbor executor writes result.json
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

# Test 5: runtime-probe emits valid model_server JSON for all backends
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
