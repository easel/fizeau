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
    - SD-005
    - SD-006
  review:
    self_hash: 01edf0d17161ebdf96abf8872c15f79496d973a06b7883c99720582ecc85be49
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      ADR-004: 0fcd10ef635933ba8c2c9bbbfca7fc7c91d117085ef161082e70c0da71d7c862
      ADR-013: 7b6760fa222d244517cf807e75414d2bf8282531ade62b9ec7ea961bd17b21c1
      ADR-014: 9138f43ef3546a70d66c155eae15946d21773af2c7d452ef4b12d110fad77ed0
      CONTRACT-003: a91944158b13a221f876ac237a3ece118a1a77f9a649e8e77b9c34fa52b2e483
      CONTRACT-004: 3c5588c6c9a872eb34b275a5a0dd248a01b5d06bdae3b55069c6240aa2c00994
      SD-005: e0acdb5a9db144a415aa5831485fe198aa3f9c7fdf0ac7d100f5a01a117df1a0
      SD-006: bd9f4cf464dbad08e003533906b67eb25735384eac4d522e367adccc9a3a7db6
      TP-001: 8b9ac8c637bdc4e7e36eb8271966356efb57d315650bbdf31f6d1e2f697dc8a4
      helix.arch: 076e620580b77517a3f561f5ce842cf1c09e6cef625c13e0a1adb874ae0e19ef
      helix.prd: 12c9ecc92726e3d50896a8afb51224906edfea9863d8114d39a6c2a0a2e54003
    reviewed_at: "2026-07-15T16:59:30Z"
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
- `docs/helix/02-design/solution-designs/SD-005-provider-config.md` and
  `SD-006-compaction.md`
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
- Route and execution hot paths consume cached, type-gated context evidence;
  they never synchronously probe a provider merely to fill a missing limit.
- The service-selected route's resolved context value and source are
  authoritative for native execution. A request-local compaction bound may
  tighten, but never enlarge, that window.
- Eligibility-time context rejection filters candidates before selection, so
  routing may choose the best eligible survivor. One `Execute` call then
  selects and dispatches one route. Same-route transport retries may repeat a
  provider call, but accepted-session capacity failure never dispatches
  another candidate. Semantic retry or escalation is a new caller-owned
  request.
- CONTRACT-003 v0.15 adds capacity JSON/events compatibly for tolerant
  consumers but adds fields to exported Go structs. Public Go fixtures and
  examples use keyed literals for the source-breaking migration.

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

### Selected-Route Capacity Sequence — v0.15

This sequence defines build dependencies and proof for SD-005, SD-006, and
CONTRACT-003. It does not duplicate live bead state.

| Slice | Goal | Depends On | Validation Gate |
|---|---|---|---|
| C-0 Authority alignment | Align routing capacity, compaction, service event, build, and release artifacts | None | HELIX graph validation and freshness checks pass |
| C-1 Route eligibility | Add the saturating prompt-plus-safety-plus-output gate while preserving raw unknown candidate evidence and exact-pin zero-requirement behavior | C-0 | Focused routing fixtures cover saturation, equality, output-only requests, unknown values, and pins |
| C-2 Selected-context handoff | Resolve config/cache/catalog/default evidence once after selection and carry the selected value/source through serviceimpl without a hot-path provider probe | C-1 | Boundary fixtures prove candidate raw evidence and authoritative execution evidence remain distinct |
| C-3 Core per-call enforcement | Use the shared non-enlarging working window, canonical estimator, fixed 95-percent envelope, and monotonic attempt state on every provider-call path | C-2 | Core fixtures cover planning, stream/non-stream, retry, compaction retry, no-stream rerun, clamp, skip, and rejection order |
| C-4 Public projection and migration | Project the exhaustive capacity payload through core, serviceimpl-owned `internal/harnesses` events, and root decode/final types without making harness-native streams authoritative | C-3 | Public contract fixtures prove event/final ordering, unknown-value preservation, keyed Go literals, and no next-route dispatch |
| C-5 Residual overflow evidence | Normalize only provider overflow that remains after preflight and keep it on the selected route | C-4 | Provider fixtures prove typed evidence without semantic rerouting |

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
- [ ] Prove context limits are sourced from explicit config, cached
      provider-API evidence, catalog metadata, or the documented default; route
      and execution hot paths make no synchronous limit probe.
- [ ] Prove the selected context value/source survives routing, execution
      handoff, capacity events, and final projection, and that a positive
      compaction override never enlarges it.
- [ ] Exercise eligibility-time context rejection and eligible-survivor
      selection, then per-call clamp/planning-skip/main rejection, every retry
      path, and event order. After route selection, no capacity failure may
      dispatch a second route candidate.
- [ ] Compile external keyed-literal fixtures for changed v0.15 Go structs and
      decode additive capacity events/terminal values while preserving unknown
      future values.
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
| A missing context limit triggers a request-path probe | High | Keep discovery in explicit/background refresh and route from cached typed evidence | Disable the probing path and fall back to catalog/default evidence |
| Capacity failure silently selects another route | High | Enforce one route per `Execute` and expose typed event/final evidence | Disable semantic fallback and require caller-owned retry |
| v0.15 Go additions break unkeyed downstream literals | Medium | Publish keyed-literal migration guidance and compile external fixtures | Hold v0.15 until migration evidence is complete |

## Exit Criteria

- [ ] The DDx bead contains the live sequence and dependency state
- [ ] Structural acceptance criteria pass locally
- [ ] Repository gates pass
- [ ] Governing docs and generated surfaces agree with behavior
- [ ] The substantive fix, tracker update, and closing SHA are pushed
