---
ddx:
  id: implementation-plan
  depends_on:
    - helix.prd
    - helix.arch
    - TP-001
    - CONTRACT-003
    - CONTRACT-004
    - ADR-002
    - ADR-004
    - ADR-013
    - ADR-014
  review:
    self_hash: 584580a6064b8866a3b72fd9bea7702d6fb2a99b035e101cdf40b163f712fc7d
    deps:
      ADR-002: 0d5923abe44d5b3558420fb80e094e996e22f67b406f011f6d0e080270e20d34
      ADR-004: 0fcd10ef635933ba8c2c9bbbfca7fc7c91d117085ef161082e70c0da71d7c862
      ADR-013: 28e0bf2781e3419d4672215b3604af7ea6f830b1e46bb48a2eaa0074597852c4
      ADR-014: df628e6bb4c8918ee13cc858720f600b6585678b0d9b441a2f18ff5ba25cd709
      CONTRACT-003: 0c3695b0fa948442d8b2e85e4a93e1c37b88b88971062ca7052d9be036ccae32
      CONTRACT-004: 9d5b9e2470cea4bd8311d63f1f391dac82a8d4f0cdff42d131d3bf5a3bc86e9e
      TP-001: 8b9ac8c637bdc4e7e36eb8271966356efb57d315650bbdf31f6d1e2f697dc8a4
      helix.arch: 344acca10c549dbb281ccdc7de6edcf67f61f12f530f74f7654ec67ccafb0a9b
      helix.prd: edcba06017764a15c820d236ed64e1d4d55eb24f4e684fd9974dd328153da68a
    reviewed_at: "2026-07-14T08:00:37Z"
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
- Every production child-process start uses one per-invocation lease from
  `internal/processlifecycle`; PTY, batch, probe, and adapter code do not own a
  parallel containment or recovery path.
- Service terminalization waits for boundary emptiness or
  `HarnessCleanupTimeout`. A cleanup deadline can end caller-visible waiting,
  but it cannot discard recovery ownership for a non-empty or indeterminate
  boundary.
- Durable recovery checks containment and process-birth identity before
  signalling. PID, PGID, job ID, process name, and command line alone are not
  ownership evidence.

## Implementation Slices

Each ready DDx bead instantiates the same bounded slice; its acceptance criteria
select the concrete files and focused checks.

| Slice | Goal | Depends On | Validation Gate |
|---|---|---|---|
| B-1 Reproduce | Prove the named gap or structural mismatch | Claimed ready bead | Focused failing test, grep, AST, or fixture check |
| B-2 Implement | Change the smallest owning package and its tests | B-1 | Focused package tests and named structural ACs |
| B-3 Reconcile | Update governed docs/generated surfaces affected by behavior | B-2 | Generators and document-specific checks are clean |
| B-4 Close | Verify, record tracker state, commit, and push without rewriting history | B-3 | Full gates and successful upstream push |

### Lifecycle Sequence — 2026-07-14

This is the desired build order for CONTRACT-003 v0.15 and CONTRACT-004. It
records dependencies and required proof, not implementation status. The DDx
queue remains authoritative for claim, progress, and close state.

| Slice | Goal | Depends On | Validation Gate |
|---|---|---|---|
| L-0 Authority alignment | Align CONTRACT-003, CONTRACT-004, ADR-002, ADR-004, ADR-013, ADR-014, this plan, and the release checklist on one lifecycle boundary | None | HELIX validation passes and the selected artifacts are not stale |
| L-1 Neutral lifecycle core | Add `internal/processlifecycle` leases, launch gating, service-control-channel ownership, durable process-birth records, cleanup results, and identity-safe recovery primitives | L-0 | Package tests cover registration-before-release, identity mismatch, record retention, and record removal only after proven emptiness |
| L-2 Unix batch containment | Route every Unix batch harness start through the neutral lifecycle supervisor and dedicated group/session boundary | L-1 | Live fixtures include a grandchild and cover normal completion, failure, timeout, cancellation, and separate-process caller death; static checks reject production bypasses |
| L-3 PTY and auxiliary containment | Route PTY sessions, `claude-tui`, quota/account/model-discovery probes, health checks, and subprocess helpers through the same per-invocation lease | L-2 | A successful `claude-tui` invocation leaves no live Claude or PTY process; contextual probes cancel and clean up; structural checks reject cross-invocation live pools |
| L-4 Windows and unsupported platforms | Add suspended creation, non-inheritable kill-on-job-close assignment before resume, and fail-before-spawn rejection where no equivalent boundary exists | L-3 | Injectable Windows adapter tests prove ordering and failure cleanup; a Windows live-grandchild run is required before claiming Windows execution support; unsupported-platform tests prove no child starts |
| L-5 Service cleanup and recovery | Connect lifecycle results to exactly-one service terminalization, cleanup precedence, caller-death persistence, and stale recovery | L-4 | Caller-alive terminal delivery follows cleanup success or deadline; `cleanup_failed` preserves the primary tuple and lifecycle record |
| L-6 Harness-neutral continuation | Add continuation only after shared cleanup and recovery semantics are stable | L-5 | Every continuation attempt starts a fresh containment boundary and reports the contract-defined disposition without retaining a live subprocess |

No slice may substitute cassette evidence for OS containment evidence. Cassettes
cover terminal/protocol projection; live subprocess fixtures cover process
ownership, caller death, descendant cleanup, and platform behavior.

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

Lifecycle beads additionally name every production spawn path they change,
the platform boundary they establish, and the live-descendant or structural
evidence that prevents an unowned path from remaining.

## Validation Plan

- [ ] Run every test function and structural property named by the bead.
- [ ] Run `go test -count=1 ./...` before every substantive commit.
- [ ] Run `make test-race` before push.
- [ ] Run `make build-ci`, `make vet`, `make lint`, `make gosec`,
      `make govulncheck`, `make fmt-check`, and `make rename-noise-check`.
- [ ] Run `make coverage-ratchet`; measurement errors and zero packages block.
- [ ] Run `make test-install-sh` or `make benchmark-workbench-smoke` when the
      affected surface requires it.
- [ ] For lifecycle slices, exercise a contained grandchild and use a separate
      caller process for caller-death evidence; a direct-child-only test is
      insufficient.
- [ ] Prove recovery refuses a mismatched process-birth identity and retains
      unresolved records instead of signalling a reused PID.
- [ ] Prove production child creation cannot bypass
      `internal/processlifecycle`, including PTY and auxiliary probe helpers.
- [ ] Record the actual platform where each live containment test ran. Compile
      checks and mocked syscall ordering do not count as live Windows evidence.
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
| Harness runs before containment is durable | High | Gate normal execution until boundary identity and recovery ownership are recorded | Reject the spawn and reap the gated process |
| Cleanup record is deleted while membership is non-empty or indeterminate | High | Retain ownership through `cleanup_failed` and later identity-safe recovery | Disable the affected harness route; do not erase unresolved evidence |
| Windows behavior is inferred from non-Windows tests | High | Separate adapter-ordering tests from required live Windows support evidence | Keep Windows execution unsupported until live evidence exists |

## Exit Criteria

- [ ] The DDx bead contains the live sequence and dependency state
- [ ] Structural acceptance criteria pass locally
- [ ] Repository gates pass
- [ ] Governing docs and generated surfaces agree with behavior
- [ ] The substantive fix, tracker update, and closing SHA are pushed
