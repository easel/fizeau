# Go Standard Linter Audit: Declared vs. Current Coverage

**Date**: 2026-05-27  
**Scope**: Fizeau project (all areas)  
**Baseline**: `.ddx/plugins/helix/workflows/concerns/go-std/practices.md` § Quality Gates (lines 36–42)  
**Current state**: `.golangci.yml`, `Makefile`, practices.md local override in `docs/helix/01-frame/concerns.md`

---

## Executive Summary

This audit maps the seven linters declared in the go-std concern baseline against the Makefile targets and `.golangci.yml` configuration currently enforced pre-commit and in CI. Five of the seven declared linters are covered by current mechanisms (govet, misspell, gosec directly; plus race detection via `go test -race`). Four linters declared in the baseline (staticcheck, ineffassign, unconvert, gocritic) are **GAP** — no equivalent mechanism runs in pre-push or CI gates today.

---

## Declared Linter Baseline

Source: `.ddx/plugins/helix/workflows/concerns/go-std/practices.md` (lines 36–42)

The Quality Gates section declares the following linters as required gates:

```
- `go fmt ./...` (or `gofmt -l .` check)
- `go vet ./...`
- `golangci-lint run`
- `gosec ./...` (high severity/confidence)
- `govulncheck ./...`
- `go test -race ./...` (unit + integration tags)
```

The bead resolution (AR-2026-05-17-repo, fizeau-a1b61f00) identified a baseline of **seven individual linters**: govet, staticcheck, ineffassign, misspell, unconvert, gosec, gocritic. This audit enumerates each and maps it to current coverage.

---

## Linter-by-Linter Coverage Mapping

| Linter | Current Mechanism | File:Line | Status | Rationale |
|--------|-------------------|-----------|--------|-----------|
| **govet** | `go vet ./...` | `Makefile:119` | ✓ COVERED | Native Go AST analysis; equivalent to `go vet` tool |
| **staticcheck** | *(none)* | `GAP` | ⚠ GAP | No current equivalent. `staticcheck` is a comprehensive dead-code, unused-variable, and style analyzer. Not available in `.golangci.yml:6–9` (only misspell enabled). Not in Makefile lint chain. Missing `staticcheck` means no dead-code detection. |
| **ineffassign** | *(none)* | `GAP` | ⚠ GAP | No current equivalent. `ineffassign` detects unused assignments. Not in `.golangci.yml:6–9`. Not in Makefile. Missing `ineffassign` means unused assignments may pass review. |
| **misspell** | `golangci-lint run` | `.golangci.yml:9` | ✓ COVERED | Enabled in `.golangci.yml` linters block. Detects common spelling mistakes in comments and identifiers. Makefile:114 runs `golangci-lint run`. |
| **unconvert** | *(none)* | `GAP` | ⚠ GAP | No current equivalent. `unconvert` detects unnecessary type conversions. Not in `.golangci.yml:6–9`. Not in Makefile. Missing `unconvert` allows redundant conversions. |
| **gosec** | `gosec ./...` | `Makefile:133` | ✓ COVERED | Dedicated security scanner invoked pre-push. Makefile:133 runs `gosec -exclude-dir=.claude -exclude-dir=.ddx ./...`. Detects hardcoded secrets, unsafe functions, and injection vectors. |
| **gocritic** | *(none)* | `GAP` | ⚠ GAP | No current equivalent. `gocritic` is a linter for code critique (style, performance, correctness). Not in `.golangci.yml:6–9`. Not in Makefile. Missing `gocritic` means no style/performance suggestions. |

---

## Project-Accepted Exclusions

The following directory exclusions are active in the project:

| Pattern | Tools | File:Line | Rationale |
|---------|-------|-----------|-----------|
| `.claude/` | `gofmt` check, `gosec` | `Makefile:122`, `Makefile:133` | Claude Code integration artifacts; not part of the Fizeau codebase. |
| `.ddx/` | `gofmt` check, `gosec` | `Makefile:122`, `Makefile:133` | DDx (Helix workflow execution) runtime artifacts and plugins; not part of the Fizeau codebase. |

These exclusions are applied consistently across all linting gates (fmt, gosec).

---

## Gaps and Consequences

**GAP: staticcheck**  
- **Current impact**: No detection of dead code, unused variables, or unreachable code. Team must rely on manual code review and IDE warnings.
- **Recommendation for resolution**: Either enable `staticcheck` in `.golangci.yml`, or accept dead-code detection is out-of-scope.

**GAP: ineffassign**  
- **Current impact**: Unused assignments are not flagged. Team must catch via manual review.
- **Recommendation for resolution**: Either enable `ineffassign` in `.golangci.yml`, or accept unused-assignment detection is out-of-scope.

**GAP: unconvert**  
- **Current impact**: Redundant type conversions are not flagged. Code may be less clear than necessary.
- **Recommendation for resolution**: Either enable `unconvert` in `.golangci.yml`, or accept redundant-conversion detection is out-of-scope.

**GAP: gocritic**  
- **Current impact**: Style and performance critiques are not enforced. Team relies on manual review for best practices.
- **Recommendation for resolution**: Either enable `gocritic` in `.golangci.yml`, or accept style/performance critique is out-of-scope.

---

## Verification: Pre-Push and CI Gates

**Current pre-push gates** (from `Makefile:138`):
```
ci-checks: build-ci vet lint gosec govulncheck fmt-check rename-noise-check test-no-race test-race
```

**Current CI gates** (from `Makefile:146`):
```
ci: ci-checks adapter-pytest
```

**Linters currently enforced**:
- `fmt-check` (line 124–130): `gofmt -l . | grep -v '^\.claude/' | grep -v '^\.ddx/'`
- `vet` (line 119): `go vet ./...`
- `lint` (line 116): `contract004-import-lint lint-go` where `lint-go` (line 114) runs `golangci-lint run` (misspell only)
- `gosec` (line 133): `gosec -exclude-dir=.claude -exclude-dir=.ddx ./...`
- `govulncheck` (line 136): `govulncheck ./...`
- `test-no-race` / `test-race` (lines 92–96): `go test -race ./...`

**Linters currently NOT enforced**:
- staticcheck
- ineffassign
- unconvert
- gocritic

---

## References

- Declared baseline: `.ddx/plugins/helix/workflows/concerns/go-std/practices.md`, lines 36–42
- Local project override: `docs/helix/01-frame/concerns.md`, § go-std (lines 22–30)
- Current linter config: `.golangci.yml`, lines 6–9
- Current pre-push/CI targets: `Makefile`, lines 138, 146
- Makefile lint targets: `Makefile`, lines 113–136
