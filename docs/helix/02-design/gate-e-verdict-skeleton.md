# Gate-E Billing-Observation Verdict (skeleton)

Status: **PENDING — operator must run the measurement campaign on a live subscription.**

This is a fill-in skeleton. The aggregated, machine-generated report is written to
`$VERDICTDIR/gate-e-verdict.md` by `scripts/gate-e-aggregate.sh`. Copy its table in
here, then complete the operator attestation.

Methodology (pre-registered): `docs/helix/02-design/billing-observation-claude-tui.md`
ADR under test: `docs/helix/02-design/adr/ADR-013-claude-tui-pty-harness-fork.md`

## Procedure

The campaign is six single-turn measurements: three in the PTY+hooks transport and
three in the `--print` batch transport. Each measurement captures the TUI `/usage`
snapshot before and after exactly one turn, then diffs the weekly message-count
delta and the `Billing Mode` classification.

Run (operator, by hand — these need a live Claude Pro/Max subscription):

```bash
export VERDICTDIR=/tmp/gate-e-verdicts
mkdir -p "$VERDICTDIR"

# Model and binary are overridable via MODEL / CLAUDE_BIN env vars.
for pair in "pty 1" "pty 2" "pty 3" "print 1" "print 2" "print 3"; do
  set -- $pair
  bash scripts/gate-e-billing-observation.sh "$1" "$2"
done

bash scripts/gate-e-aggregate.sh "$VERDICTDIR"
cat "$VERDICTDIR/gate-e-verdict.md"
```

Decision rule (per `billing-observation-claude-tui.md` §Verdict Decision Rule):

| Per-run verdict | Condition |
|-----------------|-----------|
| `subscription-confirmed` | BEFORE and AFTER both `Billing Mode: subscription`; weekly delta exactly +1; AFTER captured ≥ completion + refresh-delay floor (60s `--print`, 90s PTY). |
| `subscription-rejected` | billing-mode flip; OR delta ≠ +1; OR refresh-delay violated. |
| `subscription-ambiguous` | snapshot malformed/unreadable; `/usage` failed; or billing-mode / weekly-usage field missing. |

| Overall | Condition |
|---------|-----------|
| `accepted` | ≥ 5 runs `subscription-confirmed` and no `rejected`. |
| `rejected` | any run `subscription-rejected`. |
| `inconclusive` | ≥ 1 `ambiguous`/missing and no `rejected`. |

## Per-Run Results (paste from generated report)

| Transport | Run | Verdict | Billing Before | Billing After | Δ messages | Notes |
|-----------|-----|---------|----------------|---------------|------------|-------|
| pty   | 1 | _<verdict>_ | | | | |
| pty   | 2 | _<verdict>_ | | | | |
| pty   | 3 | _<verdict>_ | | | | |
| print | 1 | _<verdict>_ | | | | |
| print | 2 | _<verdict>_ | | | | |
| print | 3 | _<verdict>_ | | | | |

## Overall Verdict

**_<accepted | rejected | inconclusive>_**

## Operator Attestation

- [ ] Single Claude account in use; no concurrent Claude sessions during any window.
- [ ] Subscription valid ≥ 7 days beyond the measurement window.
- [ ] `claude --version`: `__________` (must be ≥ 2.1.160 for `--permission-mode bypassPermissions`).
- [ ] Concurrent-activity windows reviewed and confirmed clean:
  - pty run 1: _<start>–<end>_
  - pty run 2: _<start>–<end>_
  - pty run 3: _<start>–<end>_
  - print run 1: _<start>–<end>_
  - print run 2: _<start>–<end>_
  - print run 3: _<start>–<end>_
