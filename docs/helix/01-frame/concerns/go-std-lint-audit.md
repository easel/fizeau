# Go Standard Toolchain Linter Baseline Audit

## Summary

This document maps the declared linter baseline for the `go-std` concern (as declared in `.ddx/plugins/helix/workflows/concerns/go-std/practices.md` §Quality Gates) against the current Makefile and `.golangci.yml` coverage.

**Declared baseline**: Seven linters specified in practices.md Quality Gates (lines 36–42):
govet, staticcheck, ineffassign, misspell, unconvert, gosec, gocritic.

**Current coverage**: Two mechanisms in place:
- Makefile targets (`go vet`, `gosec`, `golangci-lint run`)
- `.golangci.yml` linter enablement (currently: `misspell` only)

**Status**: Three linters declared but not enabled (gaps identified below).

## Linter Coverage Mapping

| Declared Linter | Current Mechanism | File:Line | Coverage Status | Rationale |
|---|---|---|---|---|
| govet | Makefile target `vet` | Makefile:119 | ✓ Covered | `go vet ./...` is standard Go toolchain; all files analyzed except `.claude/` and `.ddx/` (see Exclusions). |
| staticcheck | (not enabled) | — | **GAP** | Not listed in `.golangci.yml` linters block (only `misspell` enabled, line 9). `golangci-lint run` would need `enable: [staticcheck, ...]` to activate. **Consequence**: No dead-code, type-assertion, or correctness checks beyond `govet`. |
| ineffassign | (not enabled) | — | **GAP** | Not listed in `.golangci.yml` linters block. **Consequence**: Unused variable assignments not detected; redundancy hidden. |
| misspell | .golangci.yml + Makefile | .golangci.yml:9, Makefile:114 | ✓ Covered | Enabled in `.golangci.yml` linters block; invoked by `make lint-go` and `make ci-checks`. |
| unconvert | (not enabled) | — | **GAP** | Not listed in `.golangci.yml` linters block. **Consequence**: Unnecessary type conversions not detected; potential performance issues masked. |
| gosec | Makefile target `gosec` | Makefile:133 | ✓ Covered | `gosec -exclude-dir=.claude -exclude-dir=.ddx ./...` runs as part of `make ci-checks` (invoked by ci-checks, line 138). All directories analyzed except `.claude` and `.ddx`. |
| gocritic | (not enabled) | — | **GAP** | Not listed in `.golangci.yml` linters block. **Consequence**: Diagnostic improvements and style suggestions not applied. |

## Project-Accepted Exclusions

The following directories are excluded from linting as a project convention:

| Exclusion | Mechanism | File:Line(s) | Rationale |
|---|---|---|---|
| `.claude/` | Makefile `fmt`, `fmt-check`, `gosec` targets; `gofmt -l .` filter | Makefile:122–125 (fmt/fmt-check); Makefile:133 (gosec) | Local Claude Code directory; not part of distributed source. |
| `.ddx/` | Makefile `fmt`, `fmt-check`, `gosec` targets; `gofmt -l .` filter | Makefile:122–125 (fmt/fmt-check); Makefile:133 (gosec) | DDx execution and plugin artifacts; not part of distributed source. |

## Mechanism Details

### Makefile Quality Gates
- **fmt (line 122–130)**: `gofmt -l . | grep -v '^\.claude/' | grep -v '^\.ddx/'` — Syntax check; fails if unformatted files exist outside exclusions.
- **fmt-check (line 124–130)**: Echo unformatted files and exit 1; part of `make ci-checks` (line 138).
- **vet (line 118–119)**: `go vet ./...` — Govet baseline; all packages except those in `.claude/` and `.ddx/` analyzed by default (implicit via file exclusion in Makefile rules).
- **gosec (line 132–133)**: `gosec -exclude-dir=.claude -exclude-dir=.ddx ./...` — Security checks; runs standalone or as part of `make ci-checks`.
- **govulncheck (line 135–136)**: `govulncheck ./...` — Vulnerability scanning; separate from linting (not in AC scope).

### .golangci.yml Configuration
- **version** (line 1): v2 (standard for v5+ of golangci-lint).
- **timeout** (line 4): 5 minutes.
- **linters.enable** (line 8–9): Currently only `misspell` is enabled. No other linters from the declared baseline are activated.

### ci-checks Aggregation
- **Line 138** in Makefile: `ci-checks: build-ci vet lint gosec govulncheck fmt-check rename-noise-check test-no-race test-race`
- Runs: `go vet`, `golangci-lint run` (misspell only), `gosec`, `govulncheck`, format check, and test suite.
- **Missing from ci-checks**: staticcheck, ineffassign, unconvert, gocritic (not enabled in `.golangci.yml`).

## Gaps and Consequences

**Gap 1: staticcheck**
- **Issue**: Not enabled in `.golangci.yml`.
- **Consequence**: No detection of:
  - Unused types, functions, or constants
  - Incorrect implementations (e.g., unreachable code)
  - Type and bounds errors statically inferable
  - Unnecessary complexity in conditionals
- **Resolution path**: Add `staticcheck` to `.golangci.yml` linters.enable block.

**Gap 2: ineffassign**
- **Issue**: Not enabled in `.golangci.yml`.
- **Consequence**: No detection of unused variable assignments that could hide redundancy or correctness bugs.
- **Resolution path**: Add `ineffassign` to `.golangci.yml` linters.enable block.

**Gap 3: unconvert**
- **Issue**: Not enabled in `.golangci.yml`.
- **Consequence**: No detection of unnecessary type conversions that may indicate:
  - Over-engineering
  - Performance overhead
  - Confusion about type compatibility
- **Resolution path**: Add `unconvert` to `.golangci.yml` linters.enable block.

**Gap 4: gocritic**
- **Issue**: Not enabled in `.golangci.yml`.
- **Consequence**: No diagnostic suggestions for:
  - Idiomatic Go improvements
  - Performance hints
  - Readability enhancements
- **Resolution path**: Add `gocritic` to `.golangci.yml` linters.enable block.

## Next Steps

Sibling resolution bead (parent: fizeau-397e9e0d §1) will select one of two paths:
- **Path A**: Close gaps by enabling staticcheck, ineffassign, unconvert, gocritic in `.golangci.yml`.
- **Path B**: Revise declared baseline to match current practice (misspell + govet + gosec + govulncheck only); update practices.md §Quality Gates to remove unimplemented linters.

This audit enumerates exclusions (`.claude/`, `.ddx/`) for verbatim carry-forward under Path A.
