# Billing Observation: claude-tui PTY+Hooks Mode

**Date**: 2026-05-27  
**Purpose**: Pre-registered methodology for empirical capture of subscription-mode billing classification for Claude TUI invocations via PTY+hooks transport.  
**Status**: METHODOLOGY — Document defines experiment method; measurement placeholders below are filled by sibling beads.

---

## Measurement Methodology

This section pre-registers the method for capturing empirical billing evidence per ADR-013 constraint #8.

### Model

**Recommended Model**: `claude-sonnet-4-6`  
**Rationale**: Widely deployed, stable TUI support across versions; fallback to `claude-opus-4-7` if sonnet unavailable at measurement time.  
**Selection**: Single model across all measurement runs to ensure billing classification is consistent per model version.

### Prompt

**Prompt Text** (verbatim, delivered via bracketed paste):

```
Design a simple in-memory cache with get and put operations. Include expiration handling.
```

**Prompt Characteristics**:
- **Type**: Non-trivial coding task
- **Expected Output Tokens**: 500–2000 range (medium-length generated code + explanation)
- **Justification**: Sufficient complexity to generate meaningful subscription-window activity while remaining deterministic across runs; avoids trivial arithmetic prompts that may produce <100 tokens and risk quota-accounting edge cases

### Claude Binary Version

**Recorded at Measurement Time**: `claude --version`

Output from execution environment:
```
2.1.152 (Claude Code)
```

### Operator Account Plan

**Account Type**: Claude Pro  
**Account Status**: Active/Authorized  
**Subscription Validity**: Extends at least 7 days beyond measurement window  

All measurements must use the same authenticated Anthropic account to ensure quota statistics are coherent (single account, single quota pool per ADR-013 design direction).

### Single-Account Constraint

**Constraint**: No concurrent Claude CLI invocations from the same account during the measurement window.

**Enforcement**:
- Measurement window must not overlap with other harness invocations consuming quota
- Operator attestation: explicitly confirm no other Claude sessions active during [START_TIME] through [END_TIME]
- Cassette manifest records `env_allowlist` and operator attesting single-account isolation

**Scope**: Applies to both PTY+hooks and `--print` mode measurement windows. Windows may be non-overlapping (different times on the same day), but must not have concurrent activity.

### /usage Refresh-Delay Safety Margin

**Documented Refresh Delay**: `/usage` snapshots in the Claude TUI reflect quota state with a post-completion delay.

**Safety Margin Applied**: ≥60 seconds post-completion  
**Rationale**: Empirical observation (from prior measurements) shows quota deltas are observable within 60–90s; using 60s as the floor ensures AFTER snapshots see the message-count delta.

**Measurement Protocol**:
1. Record completion timestamp (wall-clock, UTC)
2. Wait ≥60 seconds
3. Run `/usage` command and capture the snapshot
4. Record snapshot timestamp (wall-clock, UTC)
5. Verify delta: completion-time ≤ snapshot-time (confirm refresh was honored)

---

## Verdict Decision Rule

This section defines the classification verdicts based on observed /usage delta patterns.

### Three-Outcome Classification

**Input**: BEFORE snapshot, AFTER snapshot (both captured per refresh-delay protocol), delta computed across the pair.

**Outcomes**:

| Verdict | Criteria | Evidence |
|---------|----------|----------|
| **subscription-confirmed** | (1) BEFORE and AFTER both report `Billing Mode: subscription`; (2) Weekly usage delta is exactly +1 message; (3) AFTER timestamp ≥ completion-time + 60s; (4) No API-metering indicators in either snapshot. | Billing classification changed from subscription to subscription; quota window incremented; refresh-delay respected. |
| **subscription-rejected** | (1) BEFORE reports `Billing Mode: subscription` but AFTER reports `Billing Mode: api` or `per-token`; OR (2) BEFORE and AFTER both report subscription but delta ≠ +1 message (e.g., +0 or +2); OR (3) AFTER timestamp < completion-time + 60s (refresh-delay violated, measurement invalid). | Classification flip or unexpected delta or timing violation indicates measurement did not respect methodology. |
| **subscription-ambiguous** | (1) BEFORE or AFTER snapshots are malformed/unreadable; OR (2) `/usage` command failed during measurement; OR (3) Unclear or missing billing-classification field in snapshot; OR (4) Single-account attestation not provided. | Insufficient evidence to classify; measurement must be re-run with corrected protocol. |

### Verdict Aggregation

**Per-Run Verdict**: Each of the 3 PTY+hooks runs and each of the 3 `--print` mode runs receives a single verdict (subscription-confirmed, subscription-rejected, or subscription-ambiguous).

**Overall Verdict** (across all 6 runs):
- **Billing evidence accepted** if 5 or 6 runs report subscription-confirmed
- **Billing evidence rejected** if any run reports subscription-rejected
- **Billing evidence inconclusive** if ≥1 run reports subscription-ambiguous and no subscription-rejected runs

**Implication**:
- If overall verdict is **accepted**: ADR-013 constraint #8 is fulfilled; promotion decision for `claude-tui` auto-routing eligibility can proceed
- If overall verdict is **rejected** or **inconclusive**: evidence insufficient; re-run campaign with corrected methodology

---

## PTY+Hooks Mode Measurements

**Placeholder**: This section contains empty labeled blocks for 3 PTY+hooks measurements, each with BEFORE snapshot, turn output, and AFTER snapshot.

To be filled by sibling bead: each run captures /usage state before and after a single PTY-driven prompt execution.

### Run 1: PTY+Hooks Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via bracketed paste):
```
[Captured turn output will be filled here]
```

Claude TUI response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 1 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 2: PTY+Hooks Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via bracketed paste):
```
[Captured turn output will be filled here]
```

Claude TUI response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 2 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 3: PTY+Hooks Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via bracketed paste):
```
[Captured turn output will be filled here]
```

Claude TUI response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 3 Analysis**

- Verdict: `[to be determined from snapshots]`

---

## Batch Mode (--print) Measurements

**Placeholder**: This section contains empty labeled blocks for 3 `--print` mode measurements, each with BEFORE snapshot, turn output, and AFTER snapshot.

To be filled by sibling bead: each run captures /usage state before and after a single `claude --print` prompt execution.

### Run 1: --print Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via --print flag):
```
[Captured turn output will be filled here]
```

Claude response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 1 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 2: --print Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via --print flag):
```
[Captured turn output will be filled here]
```

Claude response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 2 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 3: --print Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via --print flag):
```
[Captured turn output will be filled here]
```

Claude response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 3 Analysis**

- Verdict: `[to be determined from snapshots]`

---

## Account Activity Attestation (To Be Recorded)

**Single Account Constraint Attestation**: Will be recorded by measurement operator.

**PTY+Hooks Measurement Window**: `[START_TIME]` through `[END_TIME]` UTC  
- No concurrent Claude sessions from this account during this window: `[OPERATOR ATTESTATION]`

**--print Mode Measurement Window**: `[START_TIME]` through `[END_TIME]` UTC  
- No concurrent Claude sessions from this account during this window: `[OPERATOR ATTESTATION]`
- Verified isolated from PTY+Hooks window: `[OPERATOR ATTESTATION]`

---

## Evidence References

**Related ADR**: `docs/helix/02-design/adr/ADR-013-claude-tui-pty-harness-fork.md`
- Constraint #8 (subscription billing observation): Addressed by this methodology and measurement campaign
- Status: Pending empirical evidence from measurement beads

**Sibling Measurement Beads**:
- `fizeau-48a861f2`: Captures PTY+hooks mode measurements (3 runs)
- `fizeau-cc0dd5b2`: Captures --print mode measurements (3 runs)
- Both beads reference this methodology document for model, prompt, refresh-delay, and verdict decision rule

---

## Impact

This methodology document establishes a pre-registered standard for evaluating whether Claude invocations (PTY+hooks vs. `--print` batch) land on subscription quota or API metering.

**Scope of Evidence**:
- 3 PTY+hooks measurement runs against `claude-sonnet-4-6`
- 3 `--print` batch-mode measurement runs against same model and account
- Each run captured with pre/post /usage snapshots and refresh-delay safety margin

**Decision Criteria**:
- 5+ runs showing subscription-confirmed → billing evidence accepted
- Any run showing subscription-rejected → evidence rejected
- ≥1 subscription-ambiguous without rejected → evidence inconclusive

**Next Steps** (pending measurement completion):
1. Sibling beads populate measurement tables
2. Per-run verdicts are computed using decision rule
3. Overall verdict is determined
4. If accepted: ADR-013 constraint #8 fulfilled; `claude-tui` promotion decision can proceed
