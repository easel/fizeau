# Harness Cassette Management

## Overview

Cassettes are recorded snapshots of authenticated CLI interactions for each harness. They serve as test fixtures that prove the parser works against current TUI output and capture the stable model catalog surface.

**Important: Cassettes are test fixtures, not runtime fallbacks.** At runtime, live PTY is authoritative. Cassettes exist to keep test fixtures honest and detect drift in TUI output.

## CI Gates

Two CI gates enforce cassette integrity:

### 1. Cassette Presence & Freshness (every PR)

Every harness with a TUI model surface (`claude`, `codex`, `gemini`, `opencode`, `pi`) must have:
- A `testdata/model_surface/` cassette directory checked in
- A `manifest.json` (or `discovery.json` for gemini) with a `captured_at` timestamp
- `CapturedAt` within a configured TTL (default 30 days)

Stale fixtures fail because they no longer prove the parser works against current TUI output.

The `grok` harness discovers models through the non-interactive `grok models`
subcommand rather than a TUI surface, so it carries no `model_surface/`
cassette. Its PTY-derived quota evidence (`/usage show`) can be re-recorded
with:

```bash
FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/grok -run Test_quotaRecordGrokPTY
```

Run locally:
```bash
go test ./internal/harnesses -run TestCassettePresenceAndFreshness
```

### 2. Live Discovery Drift Detector (credentialed, harness_integration tag)

Runs against the live authenticated CLI, captures a fresh snapshot, and diffs against the cassette. Mismatches fail with a re-record instruction.

**CI reporting:**
- `SKIP`: Credentials unavailable or live CLI call fails (older than TTL fails presence check in gate 1)
- `FAIL`: Drift detected (model list, reasoning levels don't match)

Run locally:
```bash
FIZEAU_HARNESS_DRIFT_CHECK=1 go test -tags harness_integration ./internal/harnesses -run TestModelDiscoveryDriftDetection
```

## Re-Recording a Cassette

When a cassette becomes stale or drifts from current TUI output, you must re-record it.

### For Claude or Codex

```bash
# Record fresh cassette
FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/claude -run DiscoveryRecordClaude

# Or for codex:
FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/codex -run DiscoveryRecordCodex
```

The test will:
1. Run the live authenticated CLI
2. Capture the fresh snapshot
3. Write it to the cassette directory
4. Update `manifest.json` with current `captured_at` timestamp

Verify the changes:
```bash
git diff internal/harnesses/claude/testdata/model_surface/

# Check that captured_at is recent
cat internal/harnesses/claude/testdata/model_surface/manifest.json | jq '.harness.captured_at'
```

### For Gemini, OpenCode, PI

Similar process, but note that:

- **Gemini**: Uses a simpler `discovery.json` format (no manifest.json wrapper)
- **OpenCode**: Captures model cost and capabilities in verbose format
- **PI**: Captures the model list from `pi --list-models`

```bash
FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/gemini -run DiscoveryRecordGemini
FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/opencode -run DiscoveryRecordOpenCode
FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/pi -run DiscoveryRecordPI
```

## Understanding the Cassette Format

Each cassette directory contains:

| File | Purpose |
|------|---------|
| `manifest.json` | Metadata: recorded timestamp, harness version, command args, terminal config, provenance |
| `discovery.json` | Model list, reasoning levels, capture metadata |
| `final.json` | Final state record (exit code, timing) |
| `frames.jsonl` | Terminal frame snapshots (JSONL) |
| `input.jsonl` | Input events sent to the PTY (JSONL) |
| `output.jsonl` | Output events from the PTY (JSONL) |
| `output.raw` | Raw terminal output (binary) |
| `service-events.jsonl` | Service-level events (JSONL) |
| `scrub-report.json` | Scrubbing audit (redactions applied) |

### Key Field: `captured_at`

Located in `manifest.json` under `harness.captured_at`:
```json
{
  "harness": {
    "captured_at": "2026-05-27T20:36:47Z",
    "freshness_window": "24h"
  }
}
```

This timestamp is checked by the freshness gate. Re-recording automatically updates it to the current time.

## Diagnosing Drift

If the drift detector reports a mismatch:

```
model discovery drift detected for claude
Expected: ["opus", "opus-4.7", "sonnet", "sonnet-4.6"]
Actual: ["opus", "opus-4.8", "sonnet", "sonnet-4.7"]
```

This means:
- The live CLI now exposes different model IDs or versions
- The cassette is stale and must be re-recorded
- The harness parser may need updates if the TUI format changed

### Steps to Resolve

1. **Verify the live CLI output is expected.** Check with the harness maintainer or release notes.
2. **Re-record the cassette** (see above).
3. **Run tests locally** to ensure no parser regressions:
   ```bash
   go test ./internal/harnesses -tags integration
   ```
4. **Commit the cassette changes** as part of your PR.

## CI Failure Modes

### Presence Gate Fails
```
TestCassettePresenceAndFreshness FAIL:
harness claude: cassette stale: captured 45 days ago (TTL: 30 days)
```

**Action:** Re-record the cassette (see above).

### Drift Gate Skips
```
TestModelDiscoveryDriftDetection SKIP:
credentials unavailable for claude: claude authentication required
```

**Action:** This is expected in CI environments without credentials. The presence gate ensures cassettes are current.

### Drift Gate Fails
```
TestModelDiscoveryDriftDetection FAIL:
model discovery drift detected for claude
Expected: [...]
Actual: [...]
```

**Action:** Re-record the cassette. This indicates the live CLI output has changed.

## Cassette Lifecycle

1. **Created**: Developer records cassette with `FIZEAU_HARNESS_RECORD=1`
2. **Checked in**: Cassette files committed to the repo
3. **Presence gate**: CI ensures cassette exists and `captured_at` is recent
4. **Drift gate (credentialed CI)**: CI runs live CLI, detects output changes
5. **Re-recorded**: When drift is detected or 30 days pass without update, re-record
6. **Verified**: All tests pass; committed with the code change

## Notes for Maintainers

- **Cassettes are stable:** Once recorded, a cassette's model list and reasoning levels define the harness's public model surface.
- **Breaking changes:** If the TUI format changes fundamentally, update the parser AND re-record the cassette.
- **Credentials:** The drift detector requires authenticated CLI credentials (e.g., valid `$HOME/.claude/config` for Claude). In CI, this job is run only on a credentialed executor pool.
- **Freshness window:** The `freshness_window` in the cassette is informational. The CI gate uses the global `DefaultCassetteTTL` constant (30 days), configurable in `cassette_presence_test.go`.

## See Also

- [CONTRACT-004: Harness Implementation](./helix/02-design/contracts/CONTRACT-004-harness-implementation.md) — harness interface contract
- [ADR-002: PTY Cassette Transport](./helix/02-design/adr/ADR-002-pty-cassette-transport.md) — cassette format and transport
