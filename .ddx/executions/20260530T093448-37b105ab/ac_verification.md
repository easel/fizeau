# Bead fizeau-4f5acc2c Acceptance Criteria Verification

## Acceptance Criteria Status

### AC1: TestRunProducesCellReports ✓
- **Requirement**: Runs canary and produces cells with report.json embedding profile, command, env_redacted, fiz.txt, fiz.err, session/
- **Test**: `bats scripts/benchmark/test/runner_loop.bats -f TestRunProducesCellReports`
- **Result**: PASS
- **Evidence**: Test validates that:
  - Cells land under `<out>/cells/<dataset>/<task>/<cell-id>/`
  - report.json contains profile.id, command, env_redacted, artifacts, final_status
  - On-disk artifacts exist (fiz.txt, fiz.err, session/)
  - 3 canary tasks × 1 rep = 3 cells created

### AC2: TestResumeAndRetryInvalid ✓
- **Requirement**: Resume skips terminal cells; --retry-invalid reruns invalid/orphan cells
- **Test**: `bats scripts/benchmark/test/runner_loop.bats -f TestResumeAndRetryInvalid`
- **Result**: PASS
- **Evidence**: Test validates that:
  - Resume without flags skips terminal cells (mtimes unchanged)
  - --retry-invalid reruns invalid cells with superseded_by back-link
  - Valid cells are not rewritten
  - New attempt cells point back at invalid cell via attempt_of

### AC3: TestBudgetHaltPlaceholder ✓
- **Requirement**: --max-cost-usd produces budget_halted placeholders
- **Test**: `bats scripts/benchmark/test/runner_loop.bats -f TestBudgetHaltPlaceholder`
- **Result**: PASS
- **Evidence**: Test validates that:
  - budget.json records cap and flips halted=true
  - At least one cell has final_status=budget_halted
  - Cell has process_outcome=setup_failed and note field

### AC4: TestPerCellRetryLinks ✓
- **Requirement**: Transient failure links attempt_of/superseded_by
- **Test**: `bats scripts/benchmark/test/runner_loop.bats -f TestPerCellRetryLinks`
- **Result**: PASS
- **Evidence**: Test validates that:
  - Retry cell carries attempt_of pointing to prior cell
  - Prior cell is back-written with superseded_by
  - Link is bidirectional

## Test Gates

### Gate 1: bats scripts/benchmark/test passes ✓
```
1..4
ok 1 TestRunProducesCellReports
ok 2 TestResumeAndRetryInvalid
ok 3 TestBudgetHaltPlaceholder
ok 4 TestPerCellRetryLinks
```

### Gate 2: go test ./... passes ✓
```
ok  github.com/easel/fizeau/scripts/benchmark	(cached)
PASS
```

### Gate 3: lefthook run pre-commit passes ✓
```
summary: (done in 0.15 seconds)
[no errors]
```

## Implementation Summary

All required functionality is implemented in `scripts/benchmark/benchmark`:

1. **Per-cell execution loop** (run_one_cell): 
   - Writes cell-state.json sentinel
   - Invokes harness-adapters
   - Composes task-spec
   - Invokes task-executors
   - Composes report.json
   - Deletes cell-state.json on clean close

2. **Resume logic** (run_sweep):
   - Skips cells with terminal final_status
   - --force-rerun ignores terminal status
   - --retry-invalid reruns invalid/orphan cells

3. **Budget enforcement** (budget_or_run_cell, compute_cell_cost):
   - Computes cost_usd_at_run_time from profile.pricing
   - Accumulates into budget.json
   - Halts when cap is reached
   - Writes budget_halted placeholders

4. **Per-cell retry** (run_cell_with_per_cell_retry):
   - Bounded exponential backoff
   - Links via attempt_of/superseded_by
   - Handles transient errors (connection_refused, http_5xx, eof_parse)

## Conclusion

✓ All acceptance criteria are met
✓ All tests pass
✓ No changes required (previously implemented)
