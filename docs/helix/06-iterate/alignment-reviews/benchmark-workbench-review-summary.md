# Benchmark Workbench Functional-Area Review Summary

> **Historical snapshot (2026-05-17).** This summary preserves the evidence
> considered by the May review. It is superseded for current alignment status
> by `AR-2026-07-14-repo.md`. Manual verification plus one browser smoke test
> did not establish full acceptance-criterion test coverage.

**Review Bead:** fizeau-a880e449  
**Functional Area:** benchmark-workbench (FEAT-008, US-008)  
**Classification:** ALIGNED  
**Parent Epic:** fizeau-283d0ada (repo-wide alignment review AR-2026-05-17)  

## Review Status

The benchmark workbench functional area is **ALIGNED** with vision, requirements, and architecture.

## Summary

- **Feature**: FEAT-008 (benchmark workbench), US-008 (benchmark workbench user story)
- **Architecture**: ADR-015 (browser analytical workbench), ADR-016 (cells as self-describing evidence)
- **Solution Design**: SD-014 (terminal benchmark integration), SD-015 (benchmark workbench)
- **Implementation evidence**: Browser smoke command
  `make benchmark-workbench-smoke` and manual observations were recorded on
  2026-05-15; that evidence does not prove every acceptance criterion is
  automated or fully tested.
- **Prior Work**: Bead fizeau-857f3b8e ("benchmark workbench: add automated browser smoke") — **CLOSED** (2026-05-15)

## Acceptance Criteria Status

Per AR-2026-05-15-benchmark-model-browser (still authoritative):

| Criterion | Test Reference | Status |
|-----------|---|---|
| Workbench data pipeline loads cells from Parquet | `website/benchmark_workbench_smoke_test.go` | Point-in-time smoke evidence |
| Model picker functional | manual verification + smoke test | Point-in-time evidence |
| Enum filters (outcome, task, model, engine, GPU, RAM) | manual verification + smoke test | Point-in-time evidence |
| Pairwise comparison table | manual verification + smoke test | Point-in-time evidence |
| Task link generation | manual verification + smoke test | Point-in-time evidence |
| Absence of default `terminalbench_task_url` column | smoke test assertion | Point-in-time smoke evidence |

## Quality Finding: ADR-016 Compatibility Banner

**Finding:** ADR-016 (cells-as-self-describing-evidence, landed 2026-05-16) changed the cell schema after SD-015 was written. SD-015 needs an ADR-016 compatibility banner.

**Details:**
- ADR-016 embeds full resolved profile snapshots in each `report.json`
- SD-015 describes the old pipeline (PROFILE_ALIASES, profile-YAML joins)
- Data pipeline updated to read embedded metadata; aliases and joins retired
- Existing Parquet artifacts backward-compatible via backfill

**Resolution:** [FILED] Bead **fizeau-5551f116** ("workbench: add ADR-016 compatibility banner to SD-015")
- Type: chore (docs)
- Severity: Low (maintainability)
- Acceptance: SD-015 carries ADR-016 compatibility banner with:
  1. Cells now embed resolved profile snapshots
  2. Profile-YAML joins and PROFILE_ALIASES retired
  3. Existing Parquet artifacts backward-compatible
  4. Pointer to ADR-016

## Conclusion

The May review classified the workbench as aligned from the smoke and manual
evidence available then. This snapshot does not claim complete automated
acceptance coverage. Use the current alignment report and `TP-001` for present
traceability status.
