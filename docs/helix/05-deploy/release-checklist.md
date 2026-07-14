---
ddx:
  id: deployment-checklist
  depends_on:
    - implementation-plan
    - FEAT-007
  review:
    self_hash: 43ddfcc04e212fabdb36d98e6798e3b03ebe70e3068351c91ee3f3ab27c67a82
    deps:
      FEAT-007: 20cf41ca595074feb1345729785859f504ce1fa570547ffc31ea38a264aa719b
      implementation-plan: a2c222193b82dfc9f8842e4f94010fc2cacfd53ecbae821d528ce1344dc4267e
    reviewed_at: "2026-07-14T05:16:22Z"
---
# Release Checklist — Fizeau

## Release Scope

- Component: public Go module plus the thin `fiz` proof CLI
- Version or commit: `v*` tag and its immutable commit SHA
- Release owner: tag creator
- Rollback owner: repository maintainer on duty
- Sources of truth: `.github/workflows/release.yml`, `install.sh`, and
  `tests/install_sh_acceptance.sh`

## Pre-Deploy Checks

| Area | Check | Evidence or Command | Status |
|---|---|---|---|
| Repository | Branch is clean and tag commit is pushed | `git status --short`; upstream SHA | [ ] |
| Quality | Build, static analysis, default tests, and race tests pass | `make ci-checks` | [ ] |
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

## Verification Checks

| Signal or Check | Expected Result | Evidence or Command | Status |
|---|---|---|---|
| Workflow | `release` matrix and `publish` job are green | GitHub Actions run for tag | [ ] |
| Assets | Exactly four supported platform artifacts are present | GitHub release asset list | [ ] |
| Explicit install | Requested tag is downloaded, not latest | `FIZEAU_VERSION=<tag> FIZEAU_INSTALL_DIR=<tmp> ./install.sh` | [ ] |
| Execution | Installed binary reports version successfully | `<tmp>/fiz --version` | [ ] |
| Library | Tagged module remains consumable through the public root package | release commit `go test -count=1 ./...` evidence | [ ] |

## Rollback Triggers

| Trigger | Threshold or Condition | Immediate Action | Owner |
|---|---|---|---|
| Missing/wrong artifact | Any of four matrix assets absent or mislabeled | Mark release unusable and stop installer promotion | Release owner |
| Installer mismatch | Explicit tag downloads a different version/platform or cannot execute | Mark release unusable; fix forward with a new tag | Release owner |
| Tag commit gate failure | Any required repository gate fails against the tag SHA | Mark release unusable; do not retag the same version | Maintainer |
| Public contract regression | A consumer cannot build the tagged module | Mark release unusable; publish a corrective version | Maintainer |

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
