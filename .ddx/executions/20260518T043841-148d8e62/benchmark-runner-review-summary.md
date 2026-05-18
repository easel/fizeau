# Benchmark Runner Functional-Area Review Summary

**Review Date**: 2026-05-17  
**Area**: Benchmark runner stack  
**Governing Artifact**: [AR-2026-05-17-repo](../../docs/helix/06-iterate/alignment-reviews/AR-2026-05-17-repo.md)  
**Parent Epic**: fizeau-283d0ada (HELIX alignment review: repo)  
**Review Issue ID**: fizeau-a15106ab

## Review Scope

The benchmark runner stack review examines the implementation progress of the rewrite from Go-based (`cmd/bench/`, `cmd/benchscore/`) to bash-based shell runner with Node collector. The authoritative plan is [plan-2026-05-15-benchmark-runner-simplification.md](../../docs/helix/02-design/plan-2026-05-15-benchmark-runner-simplification.md).

Governing specifications:
- ADR-016: Cells as self-describing evidence
- SD-009: Benchmark harness matrix
- SD-010: Benchmark system design
- SD-011: Canonical progress events
- SD-012: Evidence ledger
- The plan outlines a seven-PR sequence (A → B/C → D → E → F → G) with 16 active beads under epic fizeau-163efede.

## Findings Summary

### 1. INCOMPLETE — SD-011 Silent on Bash Runner Output Contract

**Classification**: Underspecified  
**Evidence**: 
- `docs/helix/02-design/solution-designs/SD-011-canonical-progress-events.md` describes DDx/Fizeau native progress streams
- `docs/helix/02-design/plan-2026-05-15-benchmark-runner-simplification.md` §PR 1d defines the operator contract but does not standardize the runner-to-operator progress format

**Impact**: Operator visibility, debugging, and downstream ingestion for the new bash runner lack a normative spec.

**Resolution**: Before PR D (Node collector) lands, SD-011 must be amended (or an SD-011-addendum-shell-events.md created) to define:
- The progress-event shape the bash runner emits
- Where formatting/structuring happens (runner vs collector vs website)
- How it relates to the existing canonical-progress-events taxonomy

**Filed As**: [fizeau-66c686ea](https://github.com/anthropics/fizeau/issues/) — chore, phase:design, area:bench, spec:SD-011

### 2. INCOMPLETE — SD-009 and SD-010 Miss Bash-Runner Pivot Banner

**Classification**: Stale plan  
**Evidence**:
- SD-009: Benchmark harness matrix — title and narrative still frame orchestration around the `fiz-bench` (Go) runner
- SD-010: Benchmark system design — similarly legacy-focused
- ADR-016 (committed 2026-05-16) supersedes the execution model; `cmd/bench/` is slated for deletion in PR F

**Impact**: A new reader cannot discern that the orchestrator has changed from Go CLI to bash runner.

**Resolution**: Add a top-of-document banner to both SD-009 and SD-010 pointing readers to ADR-016 and the plan-2026-05-15 for the new execution model.

**Filed As**: [fizeau-dee51a91](https://github.com/anthropics/fizeau/issues/) — chore, phase:design, area:bench, spec:SD-009

## Implementation Status

**16 active beads** under epic fizeau-163efede (benchmark runner rewrite):
- PR A (bash runner + shell adapters + harbor-runner image): multiple beads, with PR A head fizeau-11e9c095 in flight
- PR B–G: queued, awaiting A and predecessor PRs to land
- PR F (hard cutover, delete Go stack, verify-cleanup.sh): fizeau-0410f871 (open, P2)
- PR G (docs cleanup): fizeau-31e6cfe8 (existing, tracks operator migration docs)

All beads are correctly ordered with dependencies declared. No blockers external to the plan.

## Gaps Filed in AR Follow-Up

| ID | Type | Title | Dependency | Impact |
|----|----|--------|------------|--------|
| fizeau-66c686ea | chore | Amend SD-011 with shell-runner progress event taxonomy | none | Prerequisite for PR D; low criticality |
| fizeau-dee51a91 | chore | Banner SD-009 and SD-010 with ADR-016 + bash-runner pointer | none | Documentation quality; no code impact |

Both are pure documentation edits (phase:design, kind:docs); both have "discovered-from:fizeau-a15106ab" provenance.

## Review Classification

**Overall**: INCOMPLETE (planning/spec gaps identified and properly filed)

**Rationale**:
- The rewrite implementation (16-bead epic) is well-scoped, ordered, and on track.
- Two spec/documentation gaps (SD-011 shell events, SD-009/010 banners) have been identified, classified, and filed as execution beads.
- These gaps are non-blocking to PR A execution; they become gating constraints before PR D (Node collector) lands.
- No code-level gaps; the bash runner WIP and spec amendments are the sole remaining work.

## Acceptance Criteria Met

1. ✅ Review scope and governing artifacts identified
2. ✅ Planning stack cross-referenced (AR-2026-05-17-repo §Benchmark runner findings, lines 66-67, 130-131)
3. ✅ Implementation status assessed (16-bead epic fizeau-163efede, all beads accounted for)
4. ✅ Gaps identified, classified, and filed (fizeau-66c686ea, fizeau-dee51a91)
5. ✅ Provenance and dependencies declared (discovered-from:fizeau-a15106ab, spec-id cross-linked)
6. ✅ No unresolved blockers outside the benchmark-runner epic

## Recommended Actions

1. **Immediate**: Proceed with PR A (bash runner, shell adapters, harbor-runner image). The two documentation gaps are non-blocking.
2. **Before PR D**: Ensure fizeau-66c686ea (SD-011 amendment) is closed so the Node collector has a normalized progress-event contract.
3. **Before PRs B/C merge**: Ensure fizeau-dee51a91 (SD-009/010 banners) is closed so readers understand the orchestration shift.

## References

- AR-2026-05-17-repo Benchmark Runner section: lines 66-67 (gap findings), 130-131 (gap register), 177 (review issue summary), 194-195 (execution issues generated), 241-242 (execution order)
- Epic: fizeau-163efede (EPIC: bench — runner rewrite to shell+Node host stack, Python only inside Docker)
- Plan: docs/helix/02-design/plan-2026-05-15-benchmark-runner-simplification.md
- ADR: ADR-016 (cells-as-self-describing-evidence)
