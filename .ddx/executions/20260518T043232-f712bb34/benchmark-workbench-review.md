# Benchmark Workbench Functional-Area Review

**Review Date:** 2026-05-18  
**Parent Epic:** fizeau-283d0ada (repo-wide alignment review 2026-05-17)  
**Functional Area:** benchmark-workbench (FEAT-008, US-008)  
**Classification:** ALIGNED  

## Scope

Review the benchmark workbench functional area per the governing alignment review (AR-2026-05-17-repo). The workbench is a browser-side analytical interface for benchmark cells implemented in FEAT-008 / US-008 and designed in ADR-015 / SD-014 / SD-015.

## Authority Chain

- **Vision**: PRD §honest comparison
- **Feature**: FEAT-008 (benchmark workbench), US-008 (benchmark workbench user story)
- **Architecture**: ADR-015 (browser analytical workbench), ADR-016 (cells as self-describing evidence)
- **Solution Design**: SD-014 (terminal benchmark integration), SD-015 (benchmark workbench)
- **Test Evidence**: `website/benchmark_workbench_smoke_test.go` + manual smoke verification
- **Implementation Status**: Landed and closed 2026-05-15

## Review Findings

### Prior Work Complete

**Prior Alignment Review (AR-2026-05-15-benchmark-model-browser):**  
  Bead **fizeau-857f3b8e** ("benchmark workbench: add automated browser smoke") landed 2026-05-15  
  - Acceptance: "A repository command or script runs the benchmark workbench smoke test"  
  - Delivered: `make benchmark-workbench-smoke` command + test harness  
  - Evidence: `website/benchmark_workbench_smoke_test.go` asserts:
    - Row load from DuckDB/Parquet
    - Model picker functionality
    - Enum filter controls
    - Pairwise gap table rendering
    - Task link generation
    - Absence of default `terminalbench_task_url` column
  - Status: **CLOSED** (closed commit: 13bb591a041e0b6995dfa21afb6150a3ffb9a279)

### Current Assessment

**Classification: ALIGNED**

Evidence:
- ✓ FEAT-008 acceptance criteria satisfied (per AR-2026-05-15)
- ✓ US-008 user story fully addressed (model browser, filters, pairwise comparison, task links)
- ✓ SD-014 and SD-015 both implemented (Hugo page + DuckDB-WASM + Perspective datagrid)
- ✓ Smoke test command present and working (`make benchmark-workbench-smoke`)
- ✓ Manual verification confirms all panes functional (Summary, Data, Comparison, Combinations)

### Quality Finding: ADR-016 Compatibility Banner

**Issue:** ADR-016 (cells-as-self-describing-evidence, 2026-05-16) landed after SD-015 was authored. SD-015 currently documents the workbench against the old data pipeline (PROFILE_ALIASES, profile-YAML joins). The document needs a prominent banner stating the compatibility change.

**Specifics:**
- ADR-016 changes the cell schema: each `report.json` now embeds the full resolved profile snapshot
- SD-015 currently describes the old pipeline with profile-YAML joins and `PROFILE_ALIASES` table
- The workbench data pipeline (`scripts/website/build-benchmark-data.py`) has been updated to read embedded metadata; aliases and joins are retired
- Existing Parquet artifacts remain backward-compatible due to backfill strategy

**Resolution:** [FILED] **Bead fizeau-5551f116** ("workbench: add ADR-016 compatibility banner to SD-015")  
  - Type: chore (docs)  
  - Acceptance: SD-015 carries an ADR-016 compatibility banner stating:
    1. Cells now embed resolved profile snapshots
    2. Profile-YAML joins and PROFILE_ALIASES are retired
    3. Existing Parquet artifacts remain backward-compatible
    4. Pointer to ADR-016 for full context
  - Severity: Low (maintainability)  
  - Status: Open (priority=3)

## Traceability

| Element | Reference | Status |
|---------|-----------|--------|
| Vision anchor | PRD §honest comparison | ✓ |
| Feature | FEAT-008, US-008 | ✓ Implemented |
| Architecture ADRs | ADR-015, ADR-016 | ✓ Landed 2026-05-16 |
| Solution designs | SD-014, SD-015 | ✓ Implemented; SD-015 banner pending |
| Test plan | `website/benchmark_workbench_smoke_test.go` | ✓ Shipped 2026-05-15 |
| Acceptance | AR-2026-05-15 per-AC summary row 109 | ✓ SATISFIED |

## Dependencies

- **Blocking:** None  
- **Blocked by:** None  
- **Follow-up work:** fizeau-5551f116 (ADR-016 banner, non-blocking quality improvement)

## Conclusion

The benchmark workbench functional area is **ALIGNED** with vision, requirements, and architecture. Implementation is complete and tested. All acceptance criteria are satisfied. One low-severity documentation banner task (fizeau-5551f116) is filed to resolve a currency issue between SD-015 and ADR-016. No code changes required for this review closure.

---

**Review Verdict:** ALIGNED — ready to proceed with follow-up ADR-016 banner work on the normal queue.
