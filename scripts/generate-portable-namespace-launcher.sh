#!/usr/bin/env bash
set -euo pipefail

export LC_ALL=C
export TZ=UTC
export SOURCE_DATE_EPOCH=0
umask 077

usage() {
  echo "usage: $0 --check|--write" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
mode=$1
[[ "$mode" == "--check" || "$mode" == "--write" ]] || usage

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
source_file="$repo_root/internal/portableruntime/nslauncher/main.zig"
artifact_dir="$repo_root/internal/portableruntime/nslauncher/artifacts"
zig_bin=${ZIG:-zig}

zig_version=$("$zig_bin" version 2>/dev/null || true)
if [[ "$zig_version" != "0.16.0" ]]; then
  echo "portable namespace launcher generation requires Zig 0.16.0" >&2
  exit 1
fi

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/fizeau-nslauncher.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT
mkdir -p "$tmp_dir/artifacts" "$tmp_dir/cache" "$tmp_dir/global-cache"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

build_artifact() {
  local zig_target=$1
  local artifact_name=$2
  "$zig_bin" build-exe "$source_file" \
    -target "$zig_target" \
    -mcpu baseline \
    -O ReleaseSmall \
    -static \
    -fstrip \
    -fsingle-threaded \
    --build-id=none \
    --cache-dir "$tmp_dir/cache/$artifact_name" \
    --global-cache-dir "$tmp_dir/global-cache" \
    -femit-bin="$tmp_dir/artifacts/$artifact_name"
  sha256_file "$tmp_dir/artifacts/$artifact_name" >"$tmp_dir/artifacts/$artifact_name.sha256"
}

build_artifact x86_64-linux-musl namespace-launcher-linux-amd64
build_artifact aarch64-linux-musl namespace-launcher-linux-arm64
sha256_file "$source_file" >"$tmp_dir/main.zig.sha256"

outputs=(
  artifacts/namespace-launcher-linux-amd64
  artifacts/namespace-launcher-linux-amd64.sha256
  artifacts/namespace-launcher-linux-arm64
  artifacts/namespace-launcher-linux-arm64.sha256
  main.zig.sha256
)

if [[ "$mode" == "--check" ]]; then
  for output in "${outputs[@]}"; do
    if ! cmp -s "$tmp_dir/$output" "$repo_root/internal/portableruntime/nslauncher/$output"; then
      echo "portable namespace launcher artifact is stale: $output" >&2
      exit 1
    fi
  done
  exit 0
fi

mkdir -p "$artifact_dir"
for output in "${outputs[@]}"; do
  destination="$repo_root/internal/portableruntime/nslauncher/$output"
  temporary="$destination.tmp"
  cp "$tmp_dir/$output" "$temporary"
  chmod 0644 "$temporary"
  mv "$temporary" "$destination"
done
