#!/usr/bin/env bash
# verify-full.sh - run the full ("heavy") suite locally and record evidence.
#
# Suite: fizeau-heavy
# Prereqs: go, make, hugo + playwright chromium, python3 + pytest, jq
#
# Usage:
#   scripts/verify-full.sh              run the suite; on success write
#                                       .ci/evidence/full-suite.json
#   scripts/verify-full.sh --hash-only  print the content hash (used by the
#                                       `evidence-gate` CI job)
#
# Commit .ci/evidence/full-suite.json with your change. The hash covers every
# tracked file (git blob ids) except .ci/evidence, so any later edit invalidates the evidence
# and requires a re-run. Release/tag workflows ignore evidence and always run
# the heavy suite in CI.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

content_hash() {
  # sha256 over the index listing (mode, git blob id, path) of every tracked
  # file except .ci/evidence. Blob ids are content hashes, so this changes iff
  # any tracked file's content, mode or path changes; it also copes with
  # symlinks and submodules. Hashes the committed/staged state.
  if command -v sha256sum >/dev/null; then H=(sha256sum); else H=(shasum -a 256); fi
  git ls-files -s -- . ':!.ci/evidence' | "${H[@]}" | cut -d' ' -f1
}

if [[ "${1:-}" == "--hash-only" ]]; then content_hash; exit 0; fi

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

COMMAND='make install-quality-tools && make build-ci vet contract004-import-lint lint-go fmt-check rename-noise-check && make gosec govulncheck && make test-no-race test-race && make adapter-pytest && make benchmark-workbench-smoke && bash tests/install_sh_acceptance.sh'

run_suite() {
  make install-quality-tools
  make build-ci vet contract004-import-lint lint-go fmt-check rename-noise-check
  make gosec govulncheck
  make test-no-race test-race
  make adapter-pytest
  make benchmark-workbench-smoke
  bash tests/install_sh_acceptance.sh
  # Not covered locally: Windows lifecycle job (add the ci:full label for internal/processlifecycle changes)
}

echo ">> running full suite: fizeau-heavy"
run_suite

mkdir -p .ci/evidence
tools=$( { go version; make --version | head -1; python3 --version; uname -sm; } 2>/dev/null | jq -R -s -c 'split("\n") | map(select(length>0))' )
jq -n \
  --arg suite "fizeau-heavy" \
  --arg hash "$(content_hash)" \
  --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg cmd "$COMMAND" \
  --argjson tools "$tools" \
  '{suite:$suite, tool_versions:$tools, content_hash:$hash, timestamp:$ts, command:$cmd}' \
  > .ci/evidence/full-suite.json
echo ">> wrote .ci/evidence/full-suite.json (commit it with your change)"
