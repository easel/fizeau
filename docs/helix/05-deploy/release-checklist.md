---
ddx:
  id: deployment-checklist
  depends_on:
    - helix.release-scope-matrix
    - implementation-plan
    - CONTRACT-003
    - CONTRACT-004
    - ADR-002
    - TP-001
  review:
    self_hash: 6b99383dfff4e431ba9f0ec207a8da8a6d615ae59697b96fbf14937a5a6b1fde
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      CONTRACT-003: 01fd520e5200f41c120be3f561973a35c7c573703812e1a45d498fe5214188af
      CONTRACT-004: f64b7056ea5860d1afb164fa63f3c421cc94fb1432d050180d07a0a734576539
      TP-001: 330c9b002d2e84719534ab126f66001ae73cfb4f20986e28ff59cdc9ec179c9d
      helix.release-scope-matrix: dc3373a8837b404f86607a1a66cf4aba1df019b038b767a4e91354e2b8cd9662
      implementation-plan: ede4c3ca8d3c5ec0208bdf67cec76b6398a1e0988bdee733748ab8d4ba847d1b
    reviewed_at: "2026-07-20T22:54:37Z"
---
# Release Checklist — Fizeau

## Release Scope

- Component: public Go module plus the thin `fiz` proof CLI.
- Version or commit: immutable `v*` tag and its commit SHA.
- Scope authority: [v0.15 release scope matrix](../01-frame/release-scope-matrix.md).
- Release owner: tag creator. Rollback owner: repository maintainer on duty.

This is the complete v0.15 core gate. Every live row maps to one or more
canonical PRD requirements (FR-1 through FR-8), or is the minimum reliability
condition for a supported execution path. A check not in this table does not
block the tag.

## v0.15 Core Gates

| Area | PRD trace | Required outcome | Evidence | Status |
|---|---|---|---|---|
| Repository quality | FR-1–FR-8 | The tagged commit is clean, pushed, builds, and passes the repository test suite. | `git status --short`; upstream SHA; `go test ./...` | [ ] |
| Public service and tools | FR-1, FR-2 | The root facade supports bounded `Execute`, service events, workspace tools, and recorded tool attempts without exposing concrete internals. | Public contract/compile fixtures; core and tool tests | [ ] |
| Supported wrapper lifecycle | FR-1, FR-3, FR-6 | Native and supported TUI/subprocess execution leaves no owned process after normal cleanup or embedding-caller death; a terminal event follows cleanup. Reused identities are refused and cleanup-failed records remain recoverable. | `TestSubprocessHarnessDiesWithEmbeddingCaller`; `TestPTYLifecycleDiesWithEmbeddingCaller`; `TestPTYLifecycleReapsGrandchildOnClose`; `TestTerminalWaitsForHarnessCleanup`; `TestRecoveryRefusesReusedIdentity`; `TestCleanupFailureRetainsRecoveryRecord` | [ ] |
| Provider and model discovery | FR-3, FR-4 | Native providers, local runtimes, and supported wrappers use one provider-neutral request/event/usage surface. Supported dynamic discovery and launch selection are verified, including the default unrestricted Claude TUI path. | Provider and harness conformance; `TestExecuteDefaultClaudeUnrestrictedSelectsClaudeTUI`; `TestBuildLaunchArgsBypassPermissions` | [ ] |
| One-route routing | FR-4 | Catalog/pin/policy selection attributes one route. Once selected, capacity or residual-overflow handling never dispatches a second route; cross-route retry remains caller-owned. | `TestNativeResidualContextOverflowProjectsCapabilityAndSingleRoute`; `TestExecuteResidualContextOverflowIsCapabilityAndCallerOwnsCrossRouteRetry` | [ ] |
| Measurement and replay | FR-5 | Service-owned session records and replay preserve timing, tokens, tool attempts, and known-or-unknown cost with normalized provenance; unknown values are never invented. | `TestServiceFinalCostPresenceJSONRoundTrip`; `TestSessionEndCostPresenceJSONRoundTrip`; `TestBenchmarkFinalCostPresence`; `TestBenchmarkEvidenceCostPresence` | [ ] |
| Frozen public cost contract | FR-5 | The v0.15 boundary remains the existing `CostUSD *float64` plus `CostSource` on `ServiceFinalData`, `DrainExecuteResult`, `SessionEndData`, and `ServiceOverrideOutcome`. A consumer distinguishes nil, known zero, and positive values with provenance. No new cost result types, routing/catalog fields, adapters, or compatibility bridges are added. | `TestV015CostPointerMigrationCompile`; `TestDrainExecutePreservesFinalCostPresence`; `TestExecuteOverridePreservesFinalCostPresence` | [ ] |
| Thin CLI | FR-6 | `fiz` remains a service-backed proof CLI with machine-readable inspection and harness surfaces, not a second execution architecture. | CLI integration and public-facade tests | [ ] |
| Artifacts and installer | FR-7 | The release publishes exactly the supported versioned artifacts and the explicit installer selects the requested tag, platform, and executable. | `go test . -run TestReleaseWorkflowArtifactNamesFiz -count=1`; `make test-install-sh`; release asset list; `FIZEAU_VERSION=<tag> FIZEAU_INSTALL_DIR=<tmp> ./install.sh`; `<tmp>/fiz --version` | [ ] |
| Benchmark provenance | FR-8 | Benchmark result cells remain self-describing and preserve the evidence needed to compare local and cloud execution. Website presentation is not a release gate. | Benchmark evidence/provenance conformance suite | [ ] |
| Governed scope | FR-1–FR-8 | The PRD, scope matrix, test plan, implementation plan, and this checklist agree. | `ddx doc validate`; `ddx doc stale --json` | [ ] |

## Verification Boundary

The cost checks above verify the frozen FR-5 representation only. They do not
authorize further public cost-shape changes or migration of routing, catalog,
or benchmark schemas.

### FR-8 Evidence Boundary

The Benchmark provenance core gate requires durable, self-describing result
cells with enough provenance and measurement semantics to inspect or replay an
individual run. Its comparison evidence must preserve the workload,
model/provider/harness, and relevant environment or runtime facts needed for
an honest local/cloud comparison. Missing, unknown, or non-comparable facts
must remain explicit; a comparison cannot become valid by omitting them.

The gate does not require a website narrative, curated presentation, a
presentation-specific cell schema, or collection/import/report pipeline work
that neither creates nor validates durable comparison evidence. Those surfaces
may evolve independently and are non-blocking for v0.15.

`FizeauService.Continue` is experimental/deferred. Its tests, public mock and
consumer compatibility, policies, persistence, and lifecycle behavior are not
v0.15 verification evidence and cannot block a core release.

## Rollout and Go/No-Go

| Stage | Action | Exit condition |
|---|---|---|
| Validate | Run every core gate locally before tagging. | All core evidence is recorded against the intended SHA. |
| Build | Push the `v*` tag once. | Linux/darwin × amd64/arm64 jobs upload `fiz-<os>-<arch>`. |
| Publish | Publish the four artifacts as one GitHub release. | The exact tag has the four expected assets. |
| Install | Verify an explicit tag in a temporary install directory. | `fiz --version` reports the tagged version. |

No tag retry is authorized for incomplete scope work. A failed tag is fixed by a
new version; published tags are never moved or rewritten.

## Core Rollback Triggers

| Trigger | Immediate action |
|---|---|
| Missing, mislabeled, or non-executable artifact; installer downloads the wrong version/platform | Mark the release unusable and stop installer promotion. Fix forward with a new tag. |
| A tagged consumer cannot build the bounded v0.15 root surface, including the frozen FR-5 cost contract | Mark the release unusable and publish a corrective version. This does not include experimental continuation compatibility. |
| A supported wrapped harness leaks an owned process, emits terminal before cleanup, or recovery signals a reused/unowned identity | Hold the release; preserve lifecycle evidence and fix the supported execution path. |
| One `Execute` dispatches more than one selected route, or capacity projections disagree with selected-route evidence | Hold the release; restore one-route and projection correctness. |
| Measurement/replay loses timing, tokens, tool attempts, cost presence, or normalized cost provenance; or a frozen final/end projection collapses nil, zero, positive, or source semantics | Hold the release; restore the service-owned evidence path without widening the cost migration. |
| A core gate fails on the tag SHA | Mark the release unusable; do not retag that version. |

## Experimental Appendix — Portable Runtime and Continuation

Portable-runtime preparation, closure copying, mount projection, namespace
launchers, PID-1/signal isolation, OCI proof, and
`PreparePortableRuntime`/`NewFromPortableRuntime` consumer compatibility are
an ADR-014 experiment. They are **not v0.15 release gates** and are not listed
in the core table or rollback triggers.

Those checks may be run and recorded for experimental work. They may not block
a core tag, consume a release workflow, or add mock/consumer compatibility
obligations until a future approved scope matrix explicitly promotes them.

`FizeauService.Continue` is likewise retained only as experimental code. It
adds no v0.15 public compatibility, mock, consumer, policy, persistence,
lifecycle, conformance, or rollback obligation. A separately scoped API
proposal must promote it before any release gate may rely on it.

## Go or No-Go Decision

- Decision: [Go / Hold / Mark unusable]
- Tag and commit: [tag / SHA]
- Decision time: [timestamp]
- Workflow run and release: [links]
- Core-gate exceptions: [none or tracked work]
- Experimental results: [not run or link; non-blocking]
- Decision owner: [name]
