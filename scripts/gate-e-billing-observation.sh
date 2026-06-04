#!/usr/bin/env bash
#
# gate-e-billing-observation.sh
#
# Gate-E billing-observation measurement harness for ADR-013 (claude-tui PTY fork).
#
# Pre-registered methodology: docs/helix/02-design/billing-observation-claude-tui.md
#
# PURPOSE
#   Empirically measure whether a single claude turn lands on SUBSCRIPTION billing
#   (vs. API/per-token metering) by capturing the TUI `/usage` snapshot BEFORE and
#   AFTER one turn, in BOTH transports (PTY+hooks and `--print` batch), then diffing
#   the weekly message-count delta and the "Billing Mode" classification.
#
# THIS SCRIPT DOES NOT RUN IN CI. It requires a LIVE Claude Pro/Max subscription and
# an installed `claude` binary (>= 2.1.160). The operator runs it by hand.
#
# DECISION RULE (per billing-observation-claude-tui.md §Verdict Decision Rule):
#   subscription-confirmed : BEFORE and AFTER both report `Billing Mode: subscription`
#                            AND weekly-usage delta == +1 message
#                            AND AFTER timestamp >= completion + refresh-delay floor
#   subscription-rejected  : billing-mode flip (subscription -> api/per-token)
#                            OR delta != +1
#                            OR refresh-delay violated (AFTER < completion + floor)
#   subscription-ambiguous : a snapshot is malformed/unreadable, `/usage` failed,
#                            or the billing-mode / weekly-usage field is missing.
#
#   Overall (aggregate across the 6 runs, see gate-e-aggregate.sh):
#     accepted     : >= 5 runs subscription-confirmed, no rejected
#     rejected     : any run subscription-rejected
#     inconclusive : >= 1 ambiguous and no rejected
#
# REFRESH-DELAY SAFETY MARGIN (§/usage Refresh-Delay Safety Margin):
#   floor 60s for `--print`; 90s ceiling for PTY+hooks (more conservative).
#
# USAGE
#   bash scripts/gate-e-billing-observation.sh <transport> <run-number>
#     <transport>  : pty | print
#     <run-number> : 1 | 2 | 3
#
#   Environment:
#     VERDICTDIR   : output dir for snapshots + verdict JSON (default: /tmp/gate-e-verdicts)
#     CLAUDE_BIN   : claude binary (default: claude)
#     MODEL        : model for the turn (default: claude-sonnet-4-6)
#     REFRESH_FLOOR: override refresh-delay floor seconds (default: 90 for pty, 60 for print)
#
# OUTPUT
#   $VERDICTDIR/run-<transport>-<run>.before.json
#   $VERDICTDIR/run-<transport>-<run>.after.json
#   $VERDICTDIR/run-<transport>-<run>.json          (per-run verdict)
#
# The script exits 0 regardless of per-run verdict; aggregation is separate.
#
set -uo pipefail

# ---------------------------------------------------------------------------
# Args + config
# ---------------------------------------------------------------------------
TRANSPORT="${1:-}"
RUN_NUMBER="${2:-}"

if [[ "$TRANSPORT" != "pty" && "$TRANSPORT" != "print" ]]; then
  echo "usage: $0 <pty|print> <run-number>" >&2
  exit 2
fi
if ! [[ "$RUN_NUMBER" =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: $0 <pty|print> <run-number>   (run-number must be a positive integer)" >&2
  exit 2
fi

VERDICTDIR="${VERDICTDIR:-/tmp/gate-e-verdicts}"
CLAUDE_BIN="${CLAUDE_BIN:-claude}"
MODEL="${MODEL:-claude-sonnet-4-6}"
if [[ "$TRANSPORT" == "pty" ]]; then
  REFRESH_FLOOR="${REFRESH_FLOOR:-90}"
else
  REFRESH_FLOOR="${REFRESH_FLOOR:-60}"
fi

# Pre-registered prompt (§Prompt). Medium complexity, deterministic, 500-2000 tok.
PROMPT="Design a simple in-memory cache with get and put operations. Include expiration handling."

mkdir -p "$VERDICTDIR"

PREFIX="$VERDICTDIR/run-${TRANSPORT}-${RUN_NUMBER}"
BEFORE_JSON="${PREFIX}.before.json"
AFTER_JSON="${PREFIX}.after.json"
VERDICT_JSON="${PREFIX}.json"

if ! command -v "$CLAUDE_BIN" >/dev/null 2>&1; then
  echo "error: claude binary '$CLAUDE_BIN' not found on PATH" >&2
  exit 2
fi

iso_now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
epoch_now() { date -u +%s; }

# JSON string escape (handles backslash, quote, newline, tab, CR).
json_escape() {
  python3 - "$1" <<'PY' 2>/dev/null || python - "$1" 2>/dev/null
import json,sys
print(json.dumps(sys.argv[1]))
PY
}

# ---------------------------------------------------------------------------
# /usage capture
# ---------------------------------------------------------------------------
# Drives an interactive `claude` session through a PTY, sends `/usage` then quits,
# and returns the raw terminal text on stdout. Uses `script` to allocate a PTY so
# the TUI renders (claude refuses /usage in a non-tty). Works on macOS + Linux.
capture_usage_raw() {
  local out
  local tmp
  tmp="$(mktemp)"

  # Feed `/usage` then a quit. The TUI reads stdin; we pipe a small script of
  # keystrokes. `/usage\r` opens the usage panel; we wait for it to render, then
  # `/quit\r` (or Ctrl-C twice) exits. `script` provides the PTY.
  #
  # macOS `script`:  script -q /dev/null <cmd...>
  # Linux  `script`: script -q -c "<cmd>" /dev/null
  if script -q /dev/null true >/dev/null 2>&1; then
    # BSD/macOS variant: command + args follow the file.
    printf '/usage\r' | script -q "$tmp" "$CLAUDE_BIN" >/dev/null 2>&1 &
    local pid=$!
    sleep 12
    kill "$pid" >/dev/null 2>&1
    wait "$pid" 2>/dev/null
  else
    # GNU/Linux variant: -c "<command string>".
    printf '/usage\r' | script -q -c "$CLAUDE_BIN" "$tmp" >/dev/null 2>&1 &
    local pid=$!
    sleep 12
    kill "$pid" >/dev/null 2>&1
    wait "$pid" 2>/dev/null
  fi

  out="$(cat "$tmp" 2>/dev/null)"
  rm -f "$tmp"
  printf '%s' "$out"
}

# Strip ANSI escape sequences for field extraction.
strip_ansi() {
  sed -E 's/\x1B\[[0-9;?]*[ -/]*[@-~]//g; s/\x1B[][@-Z\\^_]//g'
}

# Extract "Billing Mode: <value>" -> value (lowercased, single token).
extract_billing_mode() {
  strip_ansi | grep -ioE 'Billing Mode:[[:space:]]*[A-Za-z_-]+' | head -1 \
    | sed -E 's/.*Billing Mode:[[:space:]]*//' | tr '[:upper:]' '[:lower:]'
}

# Extract "Weekly usage: <N> messages" -> N (integer).
extract_weekly_messages() {
  strip_ansi | grep -ioE 'Weekly usage:[[:space:]]*[0-9]+[[:space:]]*messages' | head -1 \
    | grep -oE '[0-9]+' | head -1
}

# Capture a snapshot and write it to $1 as JSON. Echoes "<billing_mode>|<weekly>" or
# "ambiguous|" if a required field is missing.
capture_snapshot() {
  local out_file="$1"
  local ts raw billing weekly
  ts="$(iso_now)"
  raw="$(capture_usage_raw)"
  billing="$(printf '%s' "$raw" | extract_billing_mode)"
  weekly="$(printf '%s' "$raw" | extract_weekly_messages)"

  {
    printf '{\n'
    printf '  "timestamp": %s,\n' "$(json_escape "$ts")"
    printf '  "billing_mode": %s,\n' "$(json_escape "${billing:-}")"
    if [[ -n "$weekly" ]]; then
      printf '  "weekly_messages": %s,\n' "$weekly"
    else
      printf '  "weekly_messages": null,\n'
    fi
    printf '  "raw_text": %s\n' "$(json_escape "$raw")"
    printf '}\n'
  } > "$out_file"

  if [[ -z "$billing" || -z "$weekly" ]]; then
    printf 'ambiguous|'
  else
    printf '%s|%s' "$billing" "$weekly"
  fi
}

# ---------------------------------------------------------------------------
# Turn execution
# ---------------------------------------------------------------------------
# Runs ONE turn in the requested transport. Echoes the completion epoch on stdout.
run_turn() {
  if [[ "$TRANSPORT" == "print" ]]; then
    # Batch transport. Single turn to completion.
    "$CLAUDE_BIN" --model "$MODEL" --print "$PROMPT" >/dev/null 2>&1
  else
    # PTY+hooks transport. Drive the prompt through an interactive PTY (the same
    # bracketed-paste + Enter a human would use), no batch flags.
    local tmp
    tmp="$(mktemp)"
    if script -q /dev/null true >/dev/null 2>&1; then
      printf '%s\r' "$PROMPT" | script -q "$tmp" "$CLAUDE_BIN" --model "$MODEL" >/dev/null 2>&1 &
    else
      printf '%s\r' "$PROMPT" | script -q -c "$CLAUDE_BIN --model $MODEL" "$tmp" >/dev/null 2>&1 &
    fi
    local pid=$!
    # Allow the turn to complete. Operator should confirm the turn finished in the
    # transcript before trusting the verdict; 60s is a generous floor for this prompt.
    sleep 60
    kill "$pid" >/dev/null 2>&1
    wait "$pid" 2>/dev/null
    rm -f "$tmp"
  fi
  epoch_now
}

# ---------------------------------------------------------------------------
# Measurement sequence
# ---------------------------------------------------------------------------
echo "==> gate-e: transport=$TRANSPORT run=$RUN_NUMBER model=$MODEL floor=${REFRESH_FLOOR}s"

echo "==> capturing BEFORE /usage snapshot"
BEFORE_FIELDS="$(capture_snapshot "$BEFORE_JSON")"
BILLING_BEFORE="${BEFORE_FIELDS%%|*}"
WEEKLY_BEFORE="${BEFORE_FIELDS#*|}"
echo "    before: billing_mode=${BILLING_BEFORE} weekly_messages=${WEEKLY_BEFORE:-<none>}"

echo "==> running single turn"
COMPLETION_EPOCH="$(run_turn)"
echo "    turn completed at epoch=$COMPLETION_EPOCH"

echo "==> honoring refresh-delay floor (${REFRESH_FLOOR}s)"
sleep "$REFRESH_FLOOR"

echo "==> capturing AFTER /usage snapshot"
AFTER_FIELDS="$(capture_snapshot "$AFTER_JSON")"
BILLING_AFTER="${AFTER_FIELDS%%|*}"
WEEKLY_AFTER="${AFTER_FIELDS#*|}"
AFTER_EPOCH="$(epoch_now)"
echo "    after: billing_mode=${BILLING_AFTER} weekly_messages=${WEEKLY_AFTER:-<none>}"

# ---------------------------------------------------------------------------
# Verdict computation
# ---------------------------------------------------------------------------
AFTER_DELAY=$(( AFTER_EPOCH - COMPLETION_EPOCH ))
DELTA="null"
VERDICT="subscription-ambiguous"
NOTES=""

if [[ "$BILLING_BEFORE" == "ambiguous" || "$BILLING_AFTER" == "ambiguous" \
      || -z "$BILLING_BEFORE" || -z "$BILLING_AFTER" \
      || -z "$WEEKLY_BEFORE" || -z "$WEEKLY_AFTER" ]]; then
  VERDICT="subscription-ambiguous"
  NOTES="malformed or unreadable snapshot: missing billing-mode or weekly-usage field"
else
  DELTA=$(( WEEKLY_AFTER - WEEKLY_BEFORE ))
  if [[ "$BILLING_BEFORE" == "subscription" && "$BILLING_AFTER" != "subscription" ]]; then
    VERDICT="subscription-rejected"
    NOTES="billing-mode flip: before=subscription after=${BILLING_AFTER}"
  elif [[ "$AFTER_DELAY" -lt "$REFRESH_FLOOR" ]]; then
    VERDICT="subscription-rejected"
    NOTES="refresh-delay violated: AFTER captured ${AFTER_DELAY}s post-completion (< ${REFRESH_FLOOR}s floor)"
  elif [[ "$BILLING_BEFORE" == "subscription" && "$BILLING_AFTER" == "subscription" && "$DELTA" -eq 1 ]]; then
    VERDICT="subscription-confirmed"
    NOTES="both snapshots subscription; weekly delta +1 (${WEEKLY_BEFORE} -> ${WEEKLY_AFTER}); refresh-delay honored (${AFTER_DELAY}s)"
  elif [[ "$BILLING_BEFORE" == "subscription" && "$BILLING_AFTER" == "subscription" ]]; then
    VERDICT="subscription-rejected"
    NOTES="unexpected weekly delta ${DELTA} (expected +1): ${WEEKLY_BEFORE} -> ${WEEKLY_AFTER}"
  else
    VERDICT="subscription-ambiguous"
    NOTES="non-subscription billing mode(s): before=${BILLING_BEFORE} after=${BILLING_AFTER}"
  fi
fi

{
  printf '{\n'
  printf '  "transport": %s,\n' "$(json_escape "$TRANSPORT")"
  printf '  "run_number": %s,\n' "$RUN_NUMBER"
  printf '  "verdict": %s,\n' "$(json_escape "$VERDICT")"
  printf '  "before_snapshot_file": %s,\n' "$(json_escape "$BEFORE_JSON")"
  printf '  "after_snapshot_file": %s,\n' "$(json_escape "$AFTER_JSON")"
  printf '  "billing_mode_before": %s,\n' "$(json_escape "${BILLING_BEFORE:-}")"
  printf '  "billing_mode_after": %s,\n' "$(json_escape "${BILLING_AFTER:-}")"
  if [[ "$DELTA" == "null" ]]; then
    printf '  "delta_messages": null,\n'
  else
    printf '  "delta_messages": %s,\n' "$DELTA"
  fi
  printf '  "after_timestamp_sec_post_completion": %s,\n' "$AFTER_DELAY"
  printf '  "refresh_floor_sec": %s,\n' "$REFRESH_FLOOR"
  printf '  "model": %s,\n' "$(json_escape "$MODEL")"
  printf '  "notes": %s\n' "$(json_escape "$NOTES")"
  printf '}\n'
} > "$VERDICT_JSON"

echo "==> verdict: $VERDICT"
echo "    $NOTES"
echo "    wrote $VERDICT_JSON"

# Per-run script always exits 0; aggregation applies the campaign decision rule.
exit 0
