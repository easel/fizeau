---
ddx:
  id: implementation-plan
  depends_on:
    - helix.prd
    - helix.arch
    - TP-001
    - CONTRACT-003
    - CONTRACT-004
  review:
    self_hash: a2c222193b82dfc9f8842e4f94010fc2cacfd53ecbae821d528ce1344dc4267e
    deps:
      CONTRACT-003: 45761dfe250b161440de53f0809964d89ce41eb4a7a970d0332456bc71ea1e5c
      CONTRACT-004: 81034b21e3585506776265f15d543eb0f9be15f1c02dd15c3d4141017c3f848d
      TP-001: 8b9ac8c637bdc4e7e36eb8271966356efb57d315650bbdf31f6d1e2f697dc8a4
      helix.arch: 344acca10c549dbb281ccdc7de6edcf67f61f12f530f74f7654ec67ccafb0a9b
      helix.prd: edcba06017764a15c820d236ed64e1d4d55eb24f4e684fd9974dd328153da68a
    reviewed_at: "2026-07-14T05:16:22Z"
---
# Build Plan — Fizeau

## Scope

This plan defines durable execution and verification gates. The DDx bead queue
is the sole source of truth for live scope, priority, dependencies, claims, and
close state; this document does not copy that queue or freeze a second backlog.

**Governing Artifacts**:

- `docs/helix/00-discover/product-vision.md`
- `docs/helix/01-frame/prd.md` and `FEAT-001` through `FEAT-008`
- `docs/helix/02-design/architecture.md`, accepted ADRs, and contracts
- `docs/helix/03-test/test-plan.md`

## Shared Constraints

- The root `fizeau` package is the public facade; concrete mechanics remain in
  the owning `internal/` package.
- `agentcli` and `cmd/fiz` consume the public service rather than creating a
  second execution path.
- One bead should stay within one reviewable package or boundary. Cross-boundary
  work is dependency-chained into smaller beads.
- Tracker/audit commits are preserved. No rebase, squash, filter, or amendment
  may rewrite a DDx execution trail.

## Implementation Slices

Each ready DDx bead instantiates the same bounded slice; its acceptance criteria
select the concrete files and focused checks.

| Slice | Goal | Depends On | Validation Gate |
|---|---|---|---|
| B-1 Reproduce | Prove the named gap or structural mismatch | Claimed ready bead | Focused failing test, grep, AST, or fixture check |
| B-2 Implement | Change the smallest owning package and its tests | B-1 | Focused package tests and named structural ACs |
| B-3 Reconcile | Update governed docs/generated surfaces affected by behavior | B-2 | Generators and document-specific checks are clean |
| B-4 Close | Verify, record tracker state, commit, and push without rewriting history | B-3 | Full gates and successful upstream push |

## Issue Decomposition

DDx beads own assignee, claim, status, dependencies, attempt history, and
closing commit SHA. A bead is execution-ready only when it names:

- governing `spec-id` or artifact paths;
- exact file/package scope;
- deterministic acceptance properties;
- focused commands plus any structural grep/AST check;
- dependency links for prerequisite work.

If a bead crosses CLI, service, and engine boundaries or prescribes three or
more test files, split it into dependency-chained beads before implementation.

## Validation Plan

- [ ] Run every test function and structural property named by the bead.
- [ ] Run `go test -count=1 ./...` before every substantive commit.
- [ ] Run `make test-race` before push.
- [ ] Run `make build-ci`, `make vet`, `make lint`, `make gosec`,
      `make govulncheck`, `make fmt-check`, and `make rename-noise-check`.
- [ ] Run `make coverage-ratchet`; measurement errors and zero packages block.
- [ ] Run `make test-install-sh` or `make benchmark-workbench-smoke` when the
      affected surface requires it.
- [ ] Commit `.ddx/beads.jsonl` with tracker mutations and push each completed
      fix before starting the next one.

Worker outcome labels are not acceptance evidence. Local structural checks and
the commands above decide whether a bead can close.

## Risks and Rollbacks

| Risk | Impact | Response | Rollback |
|---|---|---|---|
| Oversized bead mixes boundaries | High | Split by facade, wiring, and rigor | Close superseded bead only after child dependencies exist |
| Environmental reviewer failure | Medium | Re-run named AC locally and record exact evidence | Reopen only for a real structural defect |
| Spec and code disagree | High | Preserve desired design; track implementation gap | Revert the bounded code commit, not the governing intent |
| Push gate fails after close | High | Keep bead open until push succeeds | Fix forward in a new commit; never amend the audit trail |

## Exit Criteria

- [ ] The DDx bead contains the live sequence and dependency state
- [ ] Structural acceptance criteria pass locally
- [ ] Repository gates pass
- [ ] Governing docs and generated surfaces agree with behavior
- [ ] The substantive fix, tracker update, and closing SHA are pushed
