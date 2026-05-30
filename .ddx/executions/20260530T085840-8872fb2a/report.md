# bench-pr-A3 — per-cell execution loop verification (fizeau-4f5acc2c)

## Situation

The per-cell run loop, report.json composition, budget halting, and per-cell
retry helpers were already committed to `scripts/benchmark/benchmark` by the
A2/A4 beads (`run_one_cell`, `run_cell_with_per_cell_retry`,
`budget_or_run_cell`, `init_budget`/`append_budget_cell`/`budget_is_halted`,
`write_budget_halted_cell`). However:

1. The four acceptance tests named in the bead
   (`TestRunProducesCellReports`, `TestResumeAndRetryInvalid`,
   `TestBudgetHaltPlaceholder`, `TestPerCellRetryLinks`) did **not** exist, so
   the ACs could not be provably satisfied. This is why a prior `no_changes`
   claim was triaged `no-changes-unjustified`.
2. The per-cell transient-retry path (AC4) was **dead code**: `run_one_cell`
   never surfaced `error_class` to the report top level, so
   `run_cell_with_per_cell_retry`'s transient check (`jq -r '.error_class'`)
   always read empty and no `attempt_of`/`superseded_by` chain was ever
   produced. Verified empirically before the fix (single cell, no chain).

## Changes

- `scripts/benchmark/benchmark` (`run_one_cell`): surface `error_class` from
  `result.json` into `report.json` **only for failed executions**, mirroring
  the conditional `attempt_of` emission. Successful cells never carry
  `error_class`, so they are never spuriously re-run. This is the report.json
  composition fix that activates the per-cell retry described in the bead.
- `scripts/benchmark/test/runner_loop.bats` (new): the four AC-named bats
  tests, fully hermetic via `BENCH_TASK_EXECUTOR_OVERRIDE` (mock executor),
  `BENCH_TASKS_DIR`, `BENCH_HARBOR_DIGEST_OVERRIDE`, and a per-test
  `FIZEAU_BENCH_STATE_DIR`. No Docker, network, or real model required.

## Acceptance evidence

- AC1 `TestRunProducesCellReports`: `--profile sindri-lucebox --bench-set
  tb-2-1-canary` produces 3 cells, each `report.json` embedding the resolved
  profile (`.profile.id == "sindri-lucebox"`), `command`, `env_redacted`,
  artifact pointers, plus on-disk `fiz.txt`, `fiz.err`, `session/`.
- AC2 `TestResumeAndRetryInvalid`: a second run without flags leaves all three
  terminal cells byte-stable (mtime unchanged); after marking one cell
  `invalid_class`, `--retry-invalid` reruns only that cell (gains
  `superseded_by`, a fresh attempt cell points back via `attempt_of`) while
  the valid cell is untouched.
- AC3 `TestBudgetHaltPlaceholder`: `--max-cost-usd 0.01` against a 0.05/cell
  executor flips `budget.json` `halted=true` and writes ≥1 cell with
  `final_status=budget_halted`, `process_outcome=setup_failed`, non-empty
  `note`.
- AC4 `TestPerCellRetryLinks`: a transient executor failure mints a retry cell
  carrying `attempt_of`, and the prior cell is back-written with
  `superseded_by` (the only permitted mutation of a closed cell) plus its
  transient `error_class`.

## Gates

- `bats scripts/benchmark/test` — 10/10 pass (6 pre-existing + 4 new).
- `go test ./...` — pass.
- `lefthook run pre-commit` — pass (Go fmt/vet; no Go files changed).
