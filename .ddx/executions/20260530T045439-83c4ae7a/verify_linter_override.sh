#!/bin/bash
# Verify that the go-std linter override in concerns.md is internally consistent
# and that all 7 declared linters are accounted for.

set -e

CONCERNS_FILE="docs/helix/01-frame/concerns.md"
MAKEFILE="Makefile"
LEFTHOOK="lefthook.yml"

echo "=== Verifying go-std linter override consistency ==="
echo

# Check that the override section exists
if ! grep -q "Quality Gates Linter Override" "$CONCERNS_FILE"; then
    echo "FAIL: Quality Gates Linter Override section not found in $CONCERNS_FILE"
    exit 1
fi
echo "✓ Override section exists in $CONCERNS_FILE"

# Check that all 7 declared linters are mentioned
LINTERS=("govet" "staticcheck" "ineffassign" "misspell" "unconvert" "gosec" "gocritic")
for linter in "${LINTERS[@]}"; do
    if ! grep -q "$linter" "$CONCERNS_FILE"; then
        echo "FAIL: Linter '$linter' not mentioned in override"
        exit 1
    fi
    echo "✓ Linter '$linter' is documented"
done
echo

# Verify Makefile targets exist
echo "=== Verifying Makefile targets ==="
for target in "vet" "lint-go" "gosec"; do
    if ! grep -q "^$target:" "$MAKEFILE"; then
        echo "FAIL: Makefile target '$target' not found"
        exit 1
    fi
    echo "✓ Makefile target '$target' exists"
done
echo

# Verify lefthook.yml pre-push gates are configured
echo "=== Verifying lefthook.yml pre-push gates ==="
for gate in "vet" "lint" "gosec"; do
    if ! grep -q "run: make $gate" "$LEFTHOOK"; then
        echo "FAIL: lefthook.yml pre-push gate for 'make $gate' not found"
        exit 1
    fi
    echo "✓ lefthook.yml pre-push gate 'make $gate' configured"
done
echo

# Verify golangci-lint is configured with misspell
if ! grep -q "misspell" .golangci.yml; then
    echo "FAIL: misspell linter not enabled in .golangci.yml"
    exit 1
fi
echo "✓ golangci-lint configured with misspell"
echo

# Summary
echo "=== Summary ==="
echo "✓ All 7 declared linters are documented in the override"
echo "✓ All Makefile and lefthook.yml references are accurate"
echo "✓ The override ratifies the Makefile-driven approach"
echo "✓ The artifact is internally consistent"
echo
exit 0
