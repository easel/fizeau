#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_SCRIPT="${ROOT_DIR}/install.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_file_exists() {
  local path="$1"
  [[ -f "${path}" ]] || fail "expected file to exist: ${path}"
}

assert_executable() {
  local path="$1"
  [[ -x "${path}" ]] || fail "expected file to be executable: ${path}"
}

assert_contains() {
  local file="$1"
  local needle="$2"
  grep -Fq "${needle}" "${file}" || fail "expected '${needle}' in ${file}"
}

assert_line_count() {
  local file="$1"
  local needle="$2"
  local want="$3"
  local got
  got="$(grep -F -c "${needle}" "${file}" || true)"
  [[ "${got}" == "${want}" ]] || fail "expected ${want} matches for '${needle}' in ${file}, got ${got}"
}

platform_suffix() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "${arch}" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) fail "unsupported architecture in test harness: ${arch}" ;;
  esac
  echo "${os}-${arch}"
}

write_fake_binary() {
  local path="$1"
  cat >"${path}" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  echo "fiz test-binary"
  exit 0
fi
echo "fiz test binary"
EOF
  chmod +x "${path}"
}

write_mock_curl() {
  local path="$1"
  local payload="$2"
  local url_log="$3"
  cat >"${path}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

payload="${MOCK_BINARY_PAYLOAD:?}"
url_log="${MOCK_URL_LOG:?}"

url=""
out=""
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  arg="${args[$i]}"
  if [[ "${arg}" == "-o" ]]; then
    out="${args[$((i + 1))]}"
    ((i++))
    continue
  fi
  if [[ "${arg}" == -* ]]; then
    continue
  fi
  url="${arg}"
done

if [[ -z "${url}" ]]; then
  exit 2
fi

echo "${url}" >>"${url_log}"

if [[ "${url}" == *"/releases/latest" ]]; then
  cat <<'JSON'
{"tag_name":"v9.9.9"}
JSON
  exit 0
fi

if [[ "${url}" == *"/releases/download/"* ]]; then
  [[ -n "${out}" ]] || exit 3
  cp "${payload}" "${out}"
  exit 0
fi

exit 4
EOF
  chmod +x "${path}"
}

# @covers US-007-AC1
run_shell_case() {
  local shell_name="$1"
  local rc_rel="$2"
  local rc_mode="$3"

  local tmp home mock_bin payload url_log out install_dir rc_file
  tmp="$(mktemp -d)"
  home="${tmp}/home"
  mock_bin="${tmp}/mock-bin"
  payload="${tmp}/fake-fiz"
  url_log="${tmp}/urls.log"
  out="${tmp}/install-output.log"
  install_dir="${home}/custom-bin"
  rc_file="${home}/${rc_rel}"

  mkdir -p "${home}" "${mock_bin}" "$(dirname "${rc_file}")"
  : >"${url_log}"
  : >"${rc_file}"
  write_fake_binary "${payload}"
  write_mock_curl "${mock_bin}/curl" "${payload}" "${url_log}"

  (
    export HOME="${home}"
    export PATH="${mock_bin}:/usr/bin:/bin"
    export SHELL="/bin/${shell_name}"
    export FIZEAU_INSTALL_DIR="${install_dir}"
    export MOCK_BINARY_PAYLOAD="${payload}"
    export MOCK_URL_LOG="${url_log}"
    bash "${INSTALL_SCRIPT}" >"${out}" 2>&1
  )

  assert_file_exists "${install_dir}/fiz"
  assert_executable "${install_dir}/fiz"
  assert_contains "${out}" "Installation verification passed"
  local installed_version
  installed_version="$("${install_dir}/fiz" --version)"
  [[ "${installed_version}" == "fiz test-binary" ]] || fail "unexpected installed version output: ${installed_version}"
  local expected_rc_line
  if [[ "${rc_mode}" == "fish" ]]; then
    expected_rc_line="fish_add_path ${install_dir}"
  else
    expected_rc_line="export PATH=\"\${PATH}:${install_dir}\""
  fi
  assert_contains "${rc_file}" "${expected_rc_line}"
  assert_contains "${out}" "Please restart your shell"
  assert_contains "${out}" "source ${rc_file}"

  local suffix
  suffix="$(platform_suffix)"
  assert_contains "${url_log}" "releases/download/v9.9.9/fiz-${suffix}"

  (
    export HOME="${home}"
    export PATH="${mock_bin}:/usr/bin:/bin"
    export SHELL="/bin/${shell_name}"
    export FIZEAU_INSTALL_DIR="${install_dir}"
    export MOCK_BINARY_PAYLOAD="${payload}"
    export MOCK_URL_LOG="${url_log}"
    bash "${INSTALL_SCRIPT}" >>"${out}" 2>&1
  )

  assert_line_count "${rc_file}" "${expected_rc_line}" 1
}

main() {
  run_shell_case "bash" ".bashrc" "posix"
  run_shell_case "zsh" ".zshrc" "posix"
  run_shell_case "fish" ".config/fish/config.fish" "fish"
  echo "installer acceptance tests passed"
}

main "$@"
