#!/usr/bin/env bash
# BATS test helper

# Source bats library functions if available
if [[ -f /usr/lib/bats/bats-core/bats.bash ]]; then
  source /usr/lib/bats/bats-core/bats.bash
fi

# Helper function to create stub docker command for testing
create_stub_docker() {
  local stub_dir="$1"
  local stub_path="${stub_dir}/docker"

  mkdir -p "$stub_dir"
  cat >"$stub_path" <<'EOF'
#!/usr/bin/env bash
# Stub docker for testing task executor invocation
# Records all arguments and exits with code 0 for verification

DOCKER_STUB_LOG="${DOCKER_STUB_LOG:-.docker-stub.log}"

# Record the invocation
{
  echo "docker called at $(date -u +%s)"
  echo "ARGV: $@"
  echo "---"
} >>"$DOCKER_STUB_LOG"

exit 0
EOF
  chmod +x "$stub_path"
}

# Helper to clean up temp directories
cleanup_temp() {
  rm -rf "$BATS_TEMP_DIR" 2>/dev/null || true
}
