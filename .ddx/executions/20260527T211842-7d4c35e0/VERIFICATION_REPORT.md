# Bead fizeau-2eeb9a43 Verification Report

## Bead: bench-pr-A2 - bash runner core

### Acceptance Criteria Status: ALL MET ✓

#### AC1: --plan is pure and prints matrix
- **Test**: `Test_PlanIsPure`, `Test_PlanPrintsMatrix`
- **Command**: `./scripts/benchmark/benchmark --plan --profile sindri-lucebox --bench-set tb-2-1-canary`
- **Results**:
  - ✓ No files created
  - ✓ No Docker images pulled or built
  - ✓ Prints matrix with 9 lines (3 tasks × 3 reps)
  - ✓ Correct fields: profile, bench_set, framework, dataset, task, rep, task_executor
  - ✓ Test file: scripts/benchmark/tests/plan.bats

#### AC2: Listing subcommands
- **Test**: `Test_ListingSubcommands`
- **Commands**:
  - `./scripts/benchmark/benchmark profiles` - lists all profiles from YAML
  - `./scripts/benchmark/benchmark bench-sets` - lists all bench-sets from YAML
  - Output is sorted and includes required profiles (sindri-lucebox) and bench-sets (tb-2-1-canary)
- **Test file**: scripts/benchmark/tests/plan.bats

#### AC3: Validate command
- **Test**: `Test_Validate`
- **Command**: `./scripts/benchmark/benchmark validate`
- **Results**:
  - ✓ Exits 0 on valid configuration
  - ✓ Validates concurrency-groups.yaml structure
  - ✓ Validates profile and bench-set catalogs
- **Test file**: scripts/benchmark/tests/plan.bats

#### AC4: Matrix expansion
- **Test**: `Test_MatrixExpansion`, `Test_MatrixExpansionMultipleProfiles`
- **Results**:
  - ✓ Resolves inline tasks from bench-set YAML
  - ✓ Orders cells as: profile × task × rep
  - ✓ Multiple profiles expand correctly (2 profiles × 3 tasks × 3 reps = 18 cells)
  - ✓ Task executor defaults to 'harbor' for terminal-bench framework
  - ✓ Reps override flag works correctly
- **Test file**: scripts/benchmark/tests/matrix.bats

### Test Suite Results

**BATS Tests**: All 10 tests PASS ✓
- plan.bats: 5/5 tests pass
- matrix.bats: 5/5 tests pass

**Go Tests**: All tests PASS ✓
- `go test ./...` completed with exit code 0
- cmd/bench tests all passing

**Pre-commit Hooks**: All PASS ✓
- `go vet ./...` ✓
- `make fmt-check` ✓

### Implementation Details

**Files Created**:
- `scripts/benchmark/tests/plan.bats` - 120 lines, 5 tests
- `scripts/benchmark/tests/matrix.bats` - 113 lines, 5 tests

**Files Modified**:
- None (all existing code in scripts/benchmark/benchmark was already implemented)

**Key Features Verified**:
1. Dispatch: profiles, bench-sets, harness-adapters, task-executors, validate, preflight subcommands ✓
2. Run-mode flags: --profile, --bench-set, --plan, --force-rerun, --retry-invalid, --reps, --jobs, --max-cost-usd, --out ✓
3. Matrix expansion: profile × task × rep ordering ✓
4. Task resolution: inline tasks and tasks_from references ✓
5. Default task_executor: harbor for terminal-bench framework ✓

### Dependencies

- Parent bead: fizeau-163efede ✓
- Preflight invokes: build-harbor-runner.sh ✓
- Governing artifact: plan-2026-05-15-benchmark-runner-simplification ✓

### Conclusion

All acceptance criteria have been met. The bash runner core (dispatch, listing, validation, preflight, and matrix expansion) is fully functional and tested. The implementation passes all gates (go test, lefthook pre-commit).

**Status**: READY FOR REVIEW ✓
