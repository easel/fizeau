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
    self_hash: e2b7f6eac62a08c680ce6257711bd24b828aa33e34abb4b1664a49a8b8aa9973
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      ADR-004: 0fcd10ef635933ba8c2c9bbbfca7fc7c91d117085ef161082e70c0da71d7c862
      ADR-013: e9086db5ed1ca93bb9837c820fda3fdc444e7dfbbae9f8aacadb74d5e2b634c4
      ADR-014: 63c97bfb114774622e662d7f171c1c389776a4c5ff08cbe3c9d540dcbbdf8119
      CONTRACT-003: f013b735dfab41fb60acb1978d41da9d50bb737b7a9dd9d28f0e0b8e86e07ebc
      CONTRACT-004: d573fcd5f4a3335b36f4a858095150a25745a52e3ae177031b1f9d70d008d818
      SD-005: e0acdb5a9db144a415aa5831485fe198aa3f9c7fdf0ac7d100f5a01a117df1a0
      SD-006: bd9f4cf464dbad08e003533906b67eb25735384eac4d522e367adccc9a3a7db6
      TP-001: 6c4ab91699f822620ed7176769f969bc7f54b3ed1b1233e4d0643548f40cfdb9
      helix.arch: 076e620580b77517a3f561f5ce842cf1c09e6cef625c13e0a1adb874ae0e19ef
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-17T00:52:37Z"
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
- CONTRACT-003 v0.15 deliberately changes `CostUSD` from `float64` to
  `*float64` on `ServiceFinalData`, `DrainExecuteResult`, and
  `ServiceOverrideOutcome`. External keyed literals and selector expressions
  migrate to nil/source branching and explicit dereference; no adapter infers
  amount presence from a positive scalar.
- Portable-runtime preparation is route-neutral and complete-or-error. v0.15 is
  Linux same-GOARCH only. It packages every installed, structurally unpinned-capable harness
  closure plus every effective configured provider without selecting a route,
  exposing a secret value, or replacing the later invocation's process
  containment. Structural parity does not freeze live health or reachability.

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
| L-6 Optional route-owned harness continuation | Add continuation only after shared cleanup and recovery semantics are stable; v0.15 native-provider routes remain unsupported | L-5 | Resume preparation creates no child, lease, process, or event. Unsupported `require_resume` returns without a child; unavailable `prefer_resume` may take one ordinary fresh path, and `fresh_session` never probes resume capability. After successful preparation, the resumed child gets a new Fizeau session and fresh lifecycle lease/containment; `Start` runs once after lease acquisition and a `Start` failure never falls back fresh. Every permitted fresh child gets its own session, lease, and containment |

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

### Cost Presence and Provenance Sequence — v0.15

This sequence separates the visible Go source migration from the silent risk
of collapsing unknown cost and known zero into the same value.

| Slice | Goal | Depends On | Validation Gate |
|---|---|---|---|
| F-0 Authority | Align ADR-006's accepted override decision, CONTRACT-003's normative final/override schema, TP-001, this plan, and the release checklist | None | `ddx doc validate` passes and freshness output omits all five evolved artifacts |
| F-1 Core and harness projection | Produce authoritative optional session cost plus normalized `reported` / `configured` / `unknown` provenance in core and every harness without numeric presence inference | F-0 | Focused core and harness tests cover unknown, known zero, positive, mixed all-known provenance, stale-state replacement, and negative upstream values |
| F-2 Service, session, and override projection | Clone the authoritative pointer and source through native execution, service coordination, accepted override JSON, rejected-override omission, and durable session records | F-1 | `TestExecuteNativePreservesFinalCostPresence`, `TestMakeExecuteOverrideEventPreservesFinalCostPresence`, and `TestSessionLogPreservesFinalCostPresence` pass |
| F-3 Public and consumer migration | Stage only bounded source-backed seams: migrate CLI output, add a no-behavior-change comparison provenance ingress, populate it from the service-backed benchmark runner, then cut emitted comparison evidence over; cut all three public `CostUSD` fields to pointers; switch consumers to the normative fields; remove the temporary public bridge | F-2 | Final/override public conformance, `TestRunResultCostSourceIngressCompile`, comparison evidence/report, benchmark-ingestion, and `TestV015CostPointerMigrationCompile` tests pass; every pushed slice preserves existing known-cost behavior and production scans find no positive-value presence inference or temporary public bridge |

The F-3 source break is expected and compile-visible: scalar keyed literals,
comparisons, formatting, and arithmetic stop compiling until migrated. A cost
collapse is different: it can compile while silently fabricating zero billing
or discarding a known zero. External compile fixtures gate the former;
nil/zero/positive/source round trips at every boundary gate the latter.
The separate matrix, TerminalBench, and website cell-evidence pipeline remains
owned by SD-010, SD-012, SD-015, and its benchmark-rewrite queue; F-3 does not
silently redefine those schemas as part of the public service migration.

### Portable Runtime Sequence — v0.15

This sequence governs the DDx-facing isolated-runtime bundle. The bundle is an
artifact plan and owned cleanup handle, not Docker orchestration and not a
preselected route.

| Slice | Goal | Depends On | Validation Gate |
|---|---|---|---|
| P-0 Authority alignment | Align CONTRACT-003 public opacity/completeness, CONTRACT-004 harness ownership, ADR-014, this plan, and release gates | Lifecycle containment authority | HELIX graph validation and freshness checks pass; same-target and complete-or-error rules are unambiguous |
| P-1 Complete runtime inventory | Join the production registry to actual service instances, classify actual transports and structural inclusion, collect content-addressed Linux static/dynamic/interpreted closures plus typed launch recipes, value-opaque execution constraints, and normalized mixed-state projections through `PortableRuntimeHarness`, and combine them with a field-exhaustive effective configured-provider snapshot | P-0 | Drift tests cover every classification and provider field; package layout fixtures reject unknown closures; interpreted contributors supply reviewed exact interpreter size/SHA identities and exhaustive native-addon declarations that the neutral analyzer verifies from retained root/member descriptors and immutable package snapshots without `PATH`, shebang selection, or blind `.node` scanning; verified-exact contributors bind publisher-authenticated release digests to same-target isolated positive and missing-library-negative probes; `TestPortableRuntimeNodeInterpreterBypassesShebangAndPATH`, `TestPortableRuntimeNodeInterpreterIdentity`, `TestPortableRuntimeNodeInterpreterRejectsRPATH`, `TestPortableRuntimeNodeAddonDeclaration`, `TestPortableRuntimeNodeAddonDescriptorIdentity`, `TestPortableRuntimeNodeAddonELFPolicy`, `TestPortableRuntimeNodeAddonClosure`, and `TestPortableRuntimeInventoryIncludesGeminiAndPi` pass; ordering, merge-before-prune, dedupe/conflict, package-tree ownership, target mismatch, inherited-name, typed environment/path constraint, mixed-state projection, standalone fixed-flag, and typed fixed option/value rules are deterministic without raw environment values. Gemini and Pi execution receives the exact configured runner retained by the portable inventory; no fresh competing constructor is used |
| P-2 Secure materialization | Stage one sibling tree, revalidate the empty caller directory by identity, commit one `runtime` child with no-replace rename, emit one fixed read-only guest mount, persist normalized projections, distinguish unprojected prefix-preserving seeds from projection-consumed seeds, revalidate every source identity/content/type/symlink condition while copying, and retain retryable cleanup ownership | P-1 | `TestPortableRuntimeMixedStateProjection` plus filesystem fixtures cover deterministic private persistence, concurrent preparers, traversal, links, source identity/content races, partial failure, cancellation, modes, redaction, both seed classifications, and failed-then-retried `Close` |
| P-3 Public activation and OCI conformance | Add the opaque root facade plus `NewFromPortableRuntime`; reconstruct the configured service and production dispatch mapping in a separate public-only process from the fixed guest manifest before unpinned `Execute`; copy unprojected prefix seeds and generically assemble projected mixed native directories through namespace-owned immutable boundaries | P-2 | `TestPortableRuntimeActivationFeedsProductionDispatch`, `TestPortableRuntimeProjectionDeniesConfigMutation`, `TestPortableRuntimeInventoryUsesGeminiAndPiDispatchInstances`, `TestDispatchExecuteRunSelectsExplicitHarnessRunner`, `TestDispatchExecuteRunRejectsMissingConfiguredInventoryRunner`, and required non-root Linux OCI execute each static, dynamic, and interpreted recipe through unpinned `Execute`; Gemini/Pi use the exact configured inventory-owned runner and fail before subprocess dispatch when it is absent or identity-mismatched; prove projected config write/unlink/rename/replacement/shadow denial while credential refresh, lock creation, and sibling state creation succeed; prove an unprojected OpenCode credential seed is readable while sibling data remains writable; and cover opaque environment inheritance, configured-provider bootstrap, and structural candidate parity without skipping |

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
- [ ] Run final and override cost-presence fixtures across core, harness,
      service/session, root facade, CLI, and benchmark consumers. Unknown omits
      the amount, known zero remains present, and provenance is mandatory.
- [ ] Run `TestV015CostPointerMigrationCompile` against all three public cost
      types and reject scalar literals, numeric presence checks, or selector
      use without nil/source handling.
- [ ] Compile the public portable-runtime request, opaque bundle, and separate
      `NewFromPortableRuntime` consumer fixture; prove the plan contains no
      routing selector, original source path, or environment value.
- [ ] Run `TestPortableRuntimeInventoryIncludesGeminiAndPi`,
      `TestPortableRuntimeInventoryUsesGeminiAndPiDispatchInstances`,
      `TestDispatchExecuteRunSelectsExplicitHarnessRunner`, and
      `TestDispatchExecuteRunRejectsMissingConfiguredInventoryRunner`; prove
      Gemini/Pi dispatch consumes the exact configured inventory-owned runner
      and fails before subprocess dispatch when it is absent or mismatched.
- [ ] Exercise empty-root identity, one-child no-replace commit, concurrent
      preparers, copied-symlink/hardlink rejection or re-creation, traversal,
      source identity/content races, restrictive modes, cancellation, rollback,
      redaction, concurrent cleanup, and failed-then-retried cleanup.
- [ ] Run the required Linux OCI portable-runtime job. When opted in, missing
      OCI support is a failure, not a skip. Static, dynamic, and interpreted
      launch recipes, opaque environment inheritance, generated provider
      bootstrap, production dispatch, mapped non-root UID, and structural
      candidate identity must execute. Inject a writable-storage destruction
      failure, prove the bundle is retained and `Close` is not called, then
      retry destruction and host cleanup successfully; product code still
      contains no Docker orchestration.
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
| Cost pointer migration leaves a downstream consumer uncompilable | Medium | Compile an external consumer that migrates keyed literals, selectors, comparisons, formatting, and arithmetic through nil/source branching | Hold the pointer cutover until the consumer and migration fixture compile; do not weaken the pointer contract |
| Cost presence collapses across a compiling adapter | High | Exercise unknown, known zero, positive, and source at every F-1 through F-3 boundary; reject negative producer values and numeric presence inference | Revert or disable the faulty projection adapter while preserving the governing optional-amount contract |
| Portable inventory silently drops a difficult harness or provider | High | Exhaustive registry/provider parity tests and complete-or-error preparation | Hold the feature; do not publish a narrowed bundle as complete |
| Copied launcher lacks its interpreter, package tree, loader, or shared runtime | High | Require content-addressed same-target closure classes plus offline layout/OCI probes from the owning harness | Reject preparation with a typed redacted error |
| Declared native addon changes, escapes its package snapshot, or has an incomplete dependency closure | High | Require root-anchored no-follow descriptors, exact identities, immutable package snapshots, recursive ELF policy, and contributor-owned exhaustive probes | Reject the interpreted contribution with `ErrPortableRuntimeClosureIncomplete`; do not scan or emit the addon separately |
| Mounted files cannot reconstruct the configured service | High | Fix one guest root and require `NewFromPortableRuntime` to validate and activate the generated manifest in a separate process | Hold the feature; do not fall back to host config or an internal-only loader |
| Activation updates a scheduler map but production dispatch constructs a fresh unconfigured runner | High | Feed typed launch recipes into the actual `Execute` dispatcher and test distinguishable activated/fresh instances | Hold the feature until unpinned `Execute` consumes the activated recipe |
| Credential material survives failure or leaks through diagnostics | High | Private staging, owner-only modes, sentinel redaction tests, and retryable cleanup ownership | Hold release and remove the affected preparation path until cleanup is proven |

## Exit Criteria

- [ ] The DDx bead contains the live sequence and dependency state
- [ ] Structural acceptance criteria pass locally
- [ ] Repository gates pass
- [ ] Governing docs and generated surfaces agree with behavior
- [ ] The substantive fix, tracker update, and closing SHA are pushed
