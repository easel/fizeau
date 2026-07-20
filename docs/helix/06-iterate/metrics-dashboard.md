---
ddx:
  id: metrics-dashboard
  depends_on:
    - METRIC-test-coverage
    - helix.prd
  review:
    self_hash: 6cc2baa074b0bf1ad4ec935cf18c2f244786019ba4e45b4b2044aaa664ec89b5
    deps:
      METRIC-test-coverage: 1fb6d9e219544807b04f1853bc4d6a4874f17479716da941429e4909555e29ce
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-20T22:54:37Z"
---
# Metrics Dashboard — Current Evidence Index

**Review Window**: 2026-07-14
**Baseline**: `.helix-ratchets/coverage-floor.json` for measured coverage;
`UNKNOWN` for product outcomes without a repeatable instrument
**Status**: complete

## Decision

The coverage ratchet is measured and enforceable. The remaining product
success outcomes are `UNKNOWN`; no cost, adoption, parity, completion-rate, or
loop-overhead claim is supported by a repeatable measurement yet.

## Summary

`test-coverage` has a current command, package floors, and a verified baseline.
The PRD's six outcome metrics remain useful targets but are not metric
definitions until each has a repeatable command and recorded baseline.

## Metrics Table

| Metric | Baseline | Current | Direction | Result | Source |
|---|---|---|---|---|---|
| Test coverage ratchet | 92/92 packages | 92/92 packages | Higher | PASS | `metrics/test-coverage.yaml`; `.helix-ratchets/coverage-floor.json` |
| External embeddable adoption | UNKNOWN | UNKNOWN | Higher | UNKNOWN | `docs/helix/01-frame/prd.md` §Success Metrics |
| Per-turn measurement coverage | UNKNOWN | UNKNOWN | Higher | UNKNOWN | `docs/helix/01-frame/prd.md` §Success Metrics |
| Local/cloud provider parity | UNKNOWN | UNKNOWN | Higher | UNKNOWN | `docs/helix/01-frame/prd.md` §Success Metrics |
| Local-model routine-task completion | UNKNOWN | UNKNOWN | Higher | UNKNOWN | `docs/helix/01-frame/prd.md` §Success Metrics |
| Blended cost per routine task | UNKNOWN | UNKNOWN | Lower | UNKNOWN | `docs/helix/01-frame/prd.md` §Success Metrics |
| Agent-loop overhead | UNKNOWN | UNKNOWN | Lower | UNKNOWN | `docs/helix/01-frame/prd.md` §Success Metrics |

## Interpretation Rules

- `UNKNOWN` means no repeatable instrument and verified baseline exist; it is
  not zero, pass, or fail.
- Coverage compares every numeric package result with its committed floor and
  tolerance only after `go test -cover ./...` succeeds.
- A product outcome becomes measurable only when a metric definition records a
  side-effect-free command, output pattern, actual baseline, and
  `last_verified` date.

## Trend Notes

- The coverage floor set was reset from the retired
  `github.com/DocumentDrivenDX/agent` module to the current
  `github.com/easel/fizeau` package inventory on 2026-07-14.
- No historical numeric series is comparable across that module/package-layout
  migration without a separate normalization study.

## Follow-Up

- Add one metric definition at a time when a PRD outcome has a real collection
  command; do not backfill targets as baselines.
- Keep unknown rows visible until the corresponding measurement has been run.
