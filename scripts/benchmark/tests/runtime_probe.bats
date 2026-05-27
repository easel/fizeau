#!/usr/bin/env bats
# Acceptance tests for runtime-probe.sh (fizeau-ccef9ff3)

SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && cd .. && pwd)"
RUNTIME_PROBE="${SCRIPT_DIR}/runtime-probe.sh"

# Setup: create mock curl wrapper
setup() {
  export MOCK_CURL_DIR="$(mktemp -d)"
  export PATH="$MOCK_CURL_DIR:$PATH"

  # Create a wrapper curl script that responds to test URLs
  cat >"$MOCK_CURL_DIR/curl" <<'CURLMOCK'
#!/usr/bin/env bash
# Mock curl for testing runtime-probe

# Extract URL from arguments (last arg that doesn't start with --)
url=""
for arg in "$@"; do
  if [[ ! "$arg" =~ ^- ]]; then
    url="$arg"
  fi
done

# Return fixture responses based on environment variables
# These are set by each test before calling runtime-probe
case "$url" in
  */version)
    if [ -n "$MOCK_LUCEBOX_VERSION_RESPONSE" ]; then
      echo "$MOCK_LUCEBOX_VERSION_RESPONSE"
      exit 0
    elif [ -n "$MOCK_VLLM_VERSION_RESPONSE" ]; then
      echo "$MOCK_VLLM_VERSION_RESPONSE"
      exit 0
    elif [ -n "$MOCK_DS4_VERSION_RESPONSE" ]; then
      echo "$MOCK_DS4_VERSION_RESPONSE"
      exit 0
    else
      exit 1
    fi
    ;;
  */props)
    if [ -n "$MOCK_LLAMACPP_PROPS_RESPONSE" ]; then
      echo "$MOCK_LLAMACPP_PROPS_RESPONSE"
      exit 0
    else
      exit 1
    fi
    ;;
  */v1/models)
    if [ -n "$MOCK_VLLM_MODELS_RESPONSE" ]; then
      echo "$MOCK_VLLM_MODELS_RESPONSE"
      exit 0
    elif [ -n "$MOCK_OMLX_MODELS_RESPONSE" ]; then
      echo "$MOCK_OMLX_MODELS_RESPONSE"
      exit 0
    elif [ -n "$MOCK_RAPIDMLX_MODELS_RESPONSE" ]; then
      echo "$MOCK_RAPIDMLX_MODELS_RESPONSE"
      exit 0
    elif [ -n "$MOCK_LLAMACPP_MODELS_RESPONSE" ]; then
      echo "$MOCK_LLAMACPP_MODELS_RESPONSE"
      exit 0
    elif [ -n "$MOCK_DS4_MODELS_RESPONSE" ]; then
      echo "$MOCK_DS4_MODELS_RESPONSE"
      exit 0
    else
      exit 1
    fi
    ;;
  */internal/version)
    if [ -n "$MOCK_DS4_INTERNAL_VERSION_RESPONSE" ]; then
      echo "$MOCK_DS4_INTERNAL_VERSION_RESPONSE"
      exit 0
    else
      exit 1
    fi
    ;;
  *)
    exit 1
    ;;
esac
CURLMOCK

  chmod +x "$MOCK_CURL_DIR/curl"
}

# Teardown: clean up mock curl directory
teardown() {
  rm -rf "$MOCK_CURL_DIR"
}

# Test 1: lucebox backend
@test "Test_RuntimeProbe_lucebox" {
  export MOCK_LUCEBOX_VERSION_RESPONSE='{"version":"0.1.2","commit":"abc123def456"}'

  local profile_json='{
    "id":"test-lucebox",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"lucebox"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify required fields
  local name version commit endpoint status
  name=$(echo "$output" | jq -r '.name')
  version=$(echo "$output" | jq -r '.version')
  commit=$(echo "$output" | jq -r '.commit')
  endpoint=$(echo "$output" | jq -r '.endpoint')
  status=$(echo "$output" | jq -r '.status')

  # Verify values
  [ "$name" = "lucebox" ]
  [ "$version" = "0.1.2" ]
  [ "$commit" = "abc123def456" ]
  [ "$endpoint" = "http://localhost:8000/v1" ]
  [ "$status" = "reachable" ]
}

# Test 2: vllm backend
@test "Test_RuntimeProbe_vllm" {
  export MOCK_VLLM_VERSION_RESPONSE='{"version":"0.4.0"}'

  local profile_json='{
    "id":"test-vllm",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"vllm"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify values
  local name version endpoint status
  name=$(echo "$output" | jq -r '.name')
  version=$(echo "$output" | jq -r '.version')
  endpoint=$(echo "$output" | jq -r '.endpoint')
  status=$(echo "$output" | jq -r '.status')

  [ "$name" = "vllm" ]
  [ "$version" = "0.4.0" ]
  [ "$endpoint" = "http://localhost:8000/v1" ]
  [ "$status" = "reachable" ]
}

# Test 3: llamacpp backend
@test "Test_RuntimeProbe_llamacpp" {
  export MOCK_LLAMACPP_PROPS_RESPONSE='{"build_info":{"build_version":"3.2.0","commit":"xyz789abc"}}'

  local profile_json='{
    "id":"test-llamacpp",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"llamacpp"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify values
  local name version commit endpoint status
  name=$(echo "$output" | jq -r '.name')
  version=$(echo "$output" | jq -r '.version')
  commit=$(echo "$output" | jq -r '.commit')
  endpoint=$(echo "$output" | jq -r '.endpoint')
  status=$(echo "$output" | jq -r '.status')

  [ "$name" = "llama-server" ]
  [ "$version" = "3.2.0" ]
  [ "$commit" = "xyz789abc" ]
  [ "$endpoint" = "http://localhost:8000/v1" ]
  [ "$status" = "reachable" ]
}

# Test 4: omlx backend
@test "Test_RuntimeProbe_omlx" {
  export MOCK_OMLX_MODELS_RESPONSE='{"omlx_version":"1.0.5","commit":"def123ghi456"}'

  local profile_json='{
    "id":"test-omlx",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"omlx"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify values
  local name version commit endpoint status
  name=$(echo "$output" | jq -r '.name')
  version=$(echo "$output" | jq -r '.version')
  commit=$(echo "$output" | jq -r '.commit')
  endpoint=$(echo "$output" | jq -r '.endpoint')
  status=$(echo "$output" | jq -r '.status')

  [ "$name" = "omlx" ]
  [ "$version" = "1.0.5" ]
  [ "$commit" = "def123ghi456" ]
  [ "$endpoint" = "http://localhost:8000/v1" ]
  [ "$status" = "reachable" ]
}

# Test 5: ds4 backend
@test "Test_RuntimeProbe_ds4" {
  # Provide models response for ds4
  export MOCK_DS4_MODELS_RESPONSE='{"data":[{"id":"ds4-model","ds4_version":"2.1.0"}]}'

  local profile_json='{
    "id":"test-ds4",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"ds4"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify values
  local name version endpoint status
  name=$(echo "$output" | jq -r '.name')
  version=$(echo "$output" | jq -r '.version')
  endpoint=$(echo "$output" | jq -r '.endpoint')
  status=$(echo "$output" | jq -r '.status')

  [ "$name" = "ds4" ]
  [ "$version" = "2.1.0" ]
  [ "$endpoint" = "http://localhost:8000/v1" ]
  [ "$status" = "reachable" ]
}

# Test 6: rapid-mlx backend
@test "Test_RuntimeProbe_rapid_mlx" {
  export MOCK_RAPIDMLX_MODELS_RESPONSE='{"rapid_mlx_version":"0.8.0","commit":"vwx345yz678"}'

  local profile_json='{
    "id":"test-rapid-mlx",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"rapid-mlx"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify values
  local name version commit endpoint status
  name=$(echo "$output" | jq -r '.name')
  version=$(echo "$output" | jq -r '.version')
  commit=$(echo "$output" | jq -r '.commit')
  endpoint=$(echo "$output" | jq -r '.endpoint')
  status=$(echo "$output" | jq -r '.status')

  [ "$name" = "rapid-mlx" ]
  [ "$version" = "0.8.0" ]
  [ "$commit" = "vwx345yz678" ]
  [ "$endpoint" = "http://localhost:8000/v1" ]
  [ "$status" = "reachable" ]
}

# Test 7: lucebox-* alias is normalized
@test "Test_RuntimeProbe_lucebox_alias" {
  export MOCK_LUCEBOX_VERSION_RESPONSE='{"version":"0.2.0","commit":"alias123abc"}'

  local profile_json='{
    "id":"test-lucebox-alias",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:8000/v1"},
    "metadata":{"runtime":"lucebox-gpu"},
    "sampling":{},"limits":{}
  }'

  local output
  output=$(echo "$profile_json" | "$RUNTIME_PROBE")

  # Verify JSON structure
  echo "$output" | jq . >/dev/null

  # Verify name is normalized to lucebox
  local name
  name=$(echo "$output" | jq -r '.name')
  [ "$name" = "lucebox" ]
}

# Test 8: unreachable endpoint returns status=unreachable with exit code 3
@test "Test_RuntimeProbe_unreachable_endpoint" {
  # Don't set any mock response - curl will fail
  unset MOCK_LUCEBOX_VERSION_RESPONSE
  unset MOCK_VLLM_MODELS_RESPONSE
  unset MOCK_OMLX_MODELS_RESPONSE

  local profile_json='{
    "id":"test-unreachable",
    "provider":{"type":"openai-compat","model":"test","base_url":"http://localhost:9999/v1"},
    "metadata":{"runtime":"lucebox"},
    "sampling":{},"limits":{}
  }'

  # Run runtime-probe and capture exit code
  local output
  local exit_code=0
  output=$(echo "$profile_json" | "$RUNTIME_PROBE") || exit_code=$?

  # Verify JSON structure even when unreachable
  echo "$output" | jq . >/dev/null

  # Verify status is unreachable
  local status
  status=$(echo "$output" | jq -r '.status')
  [ "$status" = "unreachable" ]

  # Verify exit code is 3
  [ "$exit_code" = "3" ]
}
