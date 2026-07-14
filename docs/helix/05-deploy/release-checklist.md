---
ddx:
  id: deployment-checklist
  depends_on:
    - implementation-plan
    - FEAT-007
    - CONTRACT-003
    - CONTRACT-004
    - ADR-002
    - ADR-013
  review:
    self_hash: 11e4b8d6b0656f55e37d0f084cfe6778c61991060b7876670044f9d0070ce2b0
    deps:
      ADR-002: 0d5923abe44d5b3558420fb80e094e996e22f67b406f011f6d0e080270e20d34
      ADR-013: 28e0bf2781e3419d4672215b3604af7ea6f830b1e46bb48a2eaa0074597852c4
      CONTRACT-003: 0c3695b0fa948442d8b2e85e4a93e1c37b88b88971062ca7052d9be036ccae32
      CONTRACT-004: 9d5b9e2470cea4bd8311d63f1f391dac82a8d4f0cdff42d131d3bf5a3bc86e9e
      FEAT-007: 20cf41ca595074feb1345729785859f504ce1fa570547ffc31ea38a264aa719b
      implementation-plan: 584580a6064b8866a3b72fd9bea7702d6fb2a99b035e101cdf40b163f712fc7d
    reviewed_at: "2026-07-14T08:00:37Z"
---
# Release Checklist — Fizeau

## Release Scope

- Component: public Go module plus the thin `fiz` proof CLI
- Version or commit: `v*` tag and its immutable commit SHA
- Release owner: tag creator
- Rollback owner: repository maintainer on duty
- Sources of truth: `CONTRACT-003`, `CONTRACT-004`, `ADR-002`, `ADR-013`,
  `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `install.sh`,
  and `tests/install_sh_acceptance.sh`

## Pre-Deploy Checks

| Area | Check | Evidence or Command | Status |
|---|---|---|---|
| Repository | Branch is clean and tag commit is pushed | `git status --short`; upstream SHA | [ ] |
| Quality | Build, static analysis, default tests, and race tests pass | `make ci-checks` | [ ] |
| Unix batch containment | Caller-death and every-batch-runner conformance passes on Linux and macOS | `lifecycle-unix` CI jobs; `TestSubprocessHarnessDiesWithEmbeddingCaller`; `TestUnixBatchLifecycleAppliesToEveryBatchRunner` | [ ] |
| PTY containment | PTY caller-death and grandchild cleanup passes on Linux and macOS | `lifecycle-unix` CI jobs; `TestPTYLifecycleDiesWithEmbeddingCaller`; `TestPTYLifecycleReapsGrandchildOnClose` | [ ] |
| Windows containment | Real Job Object kill-on-close and grandchild tests pass | `lifecycle-windows` CI job; `TestWindowsJobKillOnOwnerHandleClose`; `TestWindowsJobReapsGrandchild` | [ ] |
| Cleanup terminalization | `HarnessCleanupTimeout` bounds current-invocation cleanup and terminal ordering; `StaleHarnessReaperGrace` governs only later startup adoption | `TestHarnessCleanupTimeoutDefaultAndValidation`; `TestStaleHarnessReaperGraceDoesNotDelayCleanup`; `TestTerminalWaitsForHarnessCleanup`; `TestCleanupFailureSupersedesPrimaryTuple` | [ ] |
| Recovery | Reused process identity is refused and cleanup-failed records are retained | `TestRecoveryRefusesReusedIdentity`; `TestCleanupFailureRetainsRecoveryRecord` | [ ] |
| Governed documents | Lifecycle contracts and decisions are current | `ddx doc stale --json` omits `CONTRACT-003`, `CONTRACT-004`, `ADR-002`, `ADR-004`, `ADR-013`, and `ADR-014` | [ ] |
| Installer | Linux and macOS installer acceptance passes | `make test-install-sh` | [ ] |
| Artifact names | Workflow emits only `fiz-<os>-<arch>` names | `go test . -run TestReleaseWorkflowArtifactNamesFiz -count=1` | [ ] |
| Version | Tag starts with `v` and points at the intended commit | tag/SHA record | [ ] |

## Rollout Plan

| Stage | Action | Exit Condition |
|---|---|---|
| Build | Push the `v*` tag; GitHub Actions cross-compiles with `CGO_ENABLED=0` | Linux/darwin × amd64/arm64 jobs upload `fiz-${GOOS}-${GOARCH}` |
| Publish | Publish the four downloaded artifacts as one GitHub release | Release for the exact tag exists and lists all four binaries |
| Install verification | Run `install.sh` with `FIZEAU_VERSION=<tag>` in a temporary install directory | Correct OS/arch artifact downloads, becomes executable, and answers `fiz --version` |

The installer supports Linux and macOS on amd64 and arm64. It normalizes an
explicit version to a `v` tag, otherwise reads the latest GitHub release, writes
to `FIZEAU_INSTALL_DIR` (default `$HOME/.local/bin`), makes `fiz` executable,
and checks the installed binary.

Windows lifecycle CI validates the Go library contract only. It does not add a
Windows release artifact; the release matrix remains Linux and macOS on amd64
and arm64 until a separate artifact decision expands it.

## Verification Checks

| Signal or Check | Expected Result | Evidence or Command | Status |
|---|---|---|---|
| Workflow | `release` matrix and `publish` job are green | GitHub Actions run for tag | [ ] |
| Assets | Exactly four supported platform artifacts are present | GitHub release asset list | [ ] |
| Explicit install | Requested tag is downloaded, not latest | `FIZEAU_VERSION=<tag> FIZEAU_INSTALL_DIR=<tmp> ./install.sh` | [ ] |
| Execution | Installed binary reports version successfully | `<tmp>/fiz --version` | [ ] |
| Library | Tagged module remains consumable through the public root package | release commit `go test -count=1 ./...` evidence | [ ] |
| Caller death | No registered wrapped harness leaves a contained process after embedding-caller death | Linux, macOS, and Windows lifecycle CI evidence | [ ] |
| Claude-TUI | Successful execution leaves no live PTY or Claude process | `TestClaudeTUIExecuteLeavesNoLiveSession` | [ ] |
| Cleanup failure | `cleanup_failed` retains lifecycle ownership evidence for recovery | `TestCleanupFailureRetainsRecoveryRecord` | [ ] |
| Identity safety | Startup recovery never signals an identity-mismatched or reused process | `TestRecoveryRefusesReusedIdentity` | [ ] |

## Rollback Triggers

| Trigger | Threshold or Condition | Immediate Action | Owner |
|---|---|---|---|
| Missing/wrong artifact | Any of four matrix assets absent or mislabeled | Mark release unusable and stop installer promotion | Release owner |
| Installer mismatch | Explicit tag downloads a different version/platform or cannot execute | Mark release unusable; fix forward with a new tag | Release owner |
| Tag commit gate failure | Any required repository gate fails against the tag SHA | Mark release unusable; do not retag the same version | Maintainer |
| Public contract regression | A consumer cannot build the tagged module | Mark release unusable; publish a corrective version | Maintainer |
| Wrapped harness leak | Any contained process remains after successful cleanup or caller-death recovery | Hold release; preserve lifecycle evidence and fix forward | Maintainer |
| Premature terminal event | A final event is emitted before the cleanup decision | Hold release; restore cleanup-before-terminal ordering | Maintainer |
| Unsafe stale recovery | Recovery signals a reused or unowned process identity | Hold release; disable recovery path until identity validation is fixed | Maintainer |
| Unsafe Windows resume | A Windows child resumes before Job Object assignment | Hold release; reject wrapped execution until assignment ordering is fixed | Maintainer |
| Indeterminate record deletion | A lifecycle record is removed before boundary emptiness is confirmed | Hold release; retain the record and fix forward | Maintainer |

Published tags and DDx audit commits are immutable. Rollback means stopping use
of the bad release and publishing a new corrective tag; it never means moving
or rewriting the published tag.

## Go or No-Go Decision

- Decision: [Go / Hold / Mark unusable]
- Tag and commit: [tag / SHA]
- Decision time: [timestamp]
- Workflow run and release: [links]
- Exceptions or follow-up: [none or tracked work]
- Decision owner: [name]
