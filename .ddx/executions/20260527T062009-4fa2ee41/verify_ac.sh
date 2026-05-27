#!/bin/bash
# Verification script for fizeau-3a7b46b2: Verify all AC are satisfied

set -e

DOC="docs/helix/02-design/billing-observation-claude-tui.md"

echo "Verifying AC for fizeau-3a7b46b2..."
echo ""

# AC1: TestPrintModeRun3Labeled
echo "AC1: Run 3 is labeled in --print mode measurements section"
if grep -A 2 "^### Run 3: --print Mode$" "$DOC" > /dev/null; then
  echo "✓ PASS: Run 3 entry found and labeled"
else
  echo "✗ FAIL: Run 3 entry not found"
  exit 1
fi

# AC2: TestPrintModeRun3BeforeAfterSnapshots
echo "AC2: Run 3 has BEFORE and AFTER snapshots"
run3_section=$(sed -n '/^### Run 3: --print Mode$/,/^---$/p' "$DOC")
if echo "$run3_section" | grep -q "BEFORE Snapshot"; then
  echo "✓ PASS: BEFORE snapshot found"
else
  echo "✗ FAIL: BEFORE snapshot not found"
  exit 1
fi
if echo "$run3_section" | grep -q "AFTER Snapshot"; then
  echo "✓ PASS: AFTER snapshot found"
else
  echo "✗ FAIL: AFTER snapshot not found"
  exit 1
fi

# AC3: TestPrintModeRun3TurnOutputRecorded
echo "AC3: Run 3 includes full claude --print turn output"
if echo "$run3_section" | grep -q "Design a simple in-memory cache with get and put operations"; then
  echo "✓ PASS: Input prompt captured"
else
  echo "✗ FAIL: Input prompt not found"
  exit 1
fi
if echo "$run3_section" | grep -q "Claude response:" && echo "$run3_section" | grep -q "thread-safe"; then
  echo "✓ PASS: Full claude response captured"
else
  echo "✗ FAIL: Claude response not found"
  exit 1
fi

# AC4: TestPrintModeRun3SnapshotTimestamps
echo "AC4: Run 3 BEFORE and AFTER snapshots have timestamps"
if echo "$run3_section" | grep -q "2026-05-27T06:20:36Z"; then
  echo "✓ PASS: BEFORE timestamp found (2026-05-27T06:20:36Z)"
else
  echo "✗ FAIL: BEFORE timestamp not found"
  exit 1
fi
if echo "$run3_section" | grep -q "2026-05-27T06:23:46Z"; then
  echo "✓ PASS: AFTER timestamp found (2026-05-27T06:23:46Z)"
else
  echo "✗ FAIL: AFTER timestamp not found"
  exit 1
fi

# AC5: TestPrintModeRun3AfterRespectsRefreshDelay
echo "AC5: Run 3 AFTER snapshot respects >=60s refresh-delay"
if echo "$run3_section" | grep -q "refresh-delay.*70s"; then
  echo "✓ PASS: AFTER snapshot respects refresh-delay (70s ≥ 60s)"
else
  echo "✗ FAIL: Refresh-delay requirement not documented"
  exit 1
fi

# AC6: TestPrintModeRun3SingleAccountAttested
echo "AC6: Run 3 attests no concurrent claude activity"
if echo "$run3_section" | grep -q "Operator attestation"; then
  echo "✓ PASS: Operator attestation present"
else
  echo "✗ FAIL: Operator attestation not found"
  exit 1
fi

echo ""
echo "All AC verified successfully!"
exit 0
