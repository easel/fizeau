---
ddx:
  id: deployment-checklist
  depends_on:
    - helix.release-scope-matrix
    - implementation-plan
    - CONTRACT-003
    - CONTRACT-004
    - ADR-002
    - ADR-013
    - TP-001
  review:
    self_hash: 95d87c27b75ae667b55e7fd931c0dbd74477ab3f77f0c55f92d33eaf35bb3a32
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      ADR-013: 23f4177615108086d085e2e3f9a70871194825d615f90daffdfea2458fbcda09
      CONTRACT-003: 14c07663bf82781f011226995ad21dc91db82763e3dd4defa1b92dc8a4d1679e
      CONTRACT-004: f64b7056ea5860d1afb164fa63f3c421cc94fb1432d050180d07a0a734576539
      implementation-plan: ede4c3ca8d3c5ec0208bdf67cec76b6398a1e0988bdee733748ab8d4ba847d1b
    reviewed_at: "2026-07-19T22:52:02Z"
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
| Measurement and replay | FR-5 | Service-owned session records and replay preserve timing, tokens, tool attempts, and known-or-unknown cost with normalized provenance; unknown values are never invented. | Final/override/session cost-presence conformance; `TestBenchmarkFinalCostPresence`; `TestBenchmarkEvidenceCostPresence` | [ ] |
| Thin CLI | FR-6 | `fiz` remains a service-backed proof CLI with machine-readable inspection and harness surfaces, not a second execution architecture. | CLI integration and public-facade tests | [ ] |
| Artifacts and installer | FR-7 | The release publishes exactly the supported versioned artifacts and the explicit installer selects the requested tag, platform, and executable. | `go test . -run TestReleaseWorkflowArtifactNamesFiz -count=1`; `make test-install-sh`; release asset list; `FIZEAU_VERSION=<tag> FIZEAU_INSTALL_DIR=<tmp> ./install.sh`; `<tmp>/fiz --version` | [ ] |
| Benchmark provenance | FR-8 | Benchmark result cells remain self-describing and preserve the evidence needed to compare local and cloud execution. Website presentation is not a release gate. | Benchmark evidence/provenance conformance suite | [ ] |
| Governed scope | FR-1–FR-8 | The PRD, scope matrix, test plan, implementation plan, and this checklist agree. | `ddx doc validate`; `ddx doc stale --json` | [ ] |

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
| A tagged public consumer cannot build the root module | Mark the release unusable and publish a corrective version. |
| A supported wrapped harness leaks an owned process, emits terminal before cleanup, or recovery signals a reused/unowned identity | Hold the release; preserve lifecycle evidence and fix the supported execution path. |
| One `Execute` dispatches more than one selected route, or capacity projections disagree with selected-route evidence | Hold the release; restore one-route and projection correctness. |
| Measurement/replay loses timing, tokens, tool attempts, cost presence, or normalized cost provenance | Hold the release; restore the service-owned evidence path. |
| A core gate fails on the tag SHA | Mark the release unusable; do not retag that version. |

## Experimental Appendix — Portable Runtime

Portable-runtime preparation, closure copying, mount projection, namespace
launchers, PID-1/signal isolation, OCI proof, and
`PreparePortableRuntime`/`NewFromPortableRuntime` consumer compatibility are
an ADR-014 experiment. They are **not v0.15 release gates** and are not listed
in the core table or rollback triggers.

Those checks may be run and recorded for experimental work. They may not block
a core tag, consume a release workflow, or add mock/consumer compatibility
obligations until a future approved scope matrix explicitly promotes them.

## Go or No-Go Decision

- Decision: [Go / Hold / Mark unusable]
- Tag and commit: [tag / SHA]
- Decision time: [timestamp]
- Workflow run and release: [links]
- Core-gate exceptions: [none or tracked work]
- Experimental results: [not run or link; non-blocking]
- Decision owner: [name]
