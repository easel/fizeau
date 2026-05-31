# Concerns-Drift Review — fizeau-a1b61f00

**Bead**: fizeau-a1b61f00
**Epic**: fizeau-283d0ada (HELIX alignment review: repo 2026-05-17)
**Area**: concerns-drift (cross-cutting)
**Base Rev**: b5d95dc67fa3d9b5c8a5b1c61cde9c1e990af29d
**Review Date**: 2026-05-31

## Summary

All three concerns-drift gaps filed in AR-2026-05-17-repo have been resolved by prior beads. This bead closes with verified RESOLVED status on all findings.

---

## Finding Verification

### 1. go-std Lint Baseline Drift

**AR Finding**: `go-std` concern declares a seven-linter golangci-lint baseline (govet, staticcheck, ineffassign, misspell, unconvert, gosec, gocritic), but `.golangci.yml` enables only `misspell`. Actual CI gates run via Makefile (`go vet`, `gosec`, `govulncheck`, `go test -race`), which partially covers but does not match what the concern names.

**Resolution Bead**: fizeau-397e9e0d (closed, commit b24d95796cc41164b4785daf8ff22ea9101cc107)

**Verification**:
- `docs/helix/01-frame/concerns.md` §go-std line 33 now contains a "Quality Gates Linter Override" section that documents the Makefile-driven approach.
- Each of the seven declared linters is mapped: govet → `make vet`, misspell → `golangci-lint run`, gosec → `make gosec`. staticcheck, ineffassign, unconvert, and gocritic are explicitly marked out-of-scope for CI with rationale (IDE/review-time enforcement).
- `.golangci.yml` still enables only `misspell` — consistent with the documented override.
- The concern and the tooling are now internally consistent.

**Status**: RESOLVED

---

### 2. o11y-otel Not Declared Active

**AR Finding**: `concerns.md` did not list `o11y-otel` as an active concern despite PRD P1-8 (OpenTelemetry) and CONTRACT-001 governing OTel conformance. OTel implementation was present and conformant, but the concern was undeclared.

**Resolution Bead**: fizeau-eeb8202a (closed, commit a2377765188c9c7dddb0666c9f2f142955ab916f)

**Verification**:
- `docs/helix/01-frame/concerns.md` line 19: `o11y-otel` is declared in the Active Concerns list with areas `lib, cli`.
- Lines 222–251: full `### o11y-otel` project override section covering scope, span taxonomy, JSONL/OTel duality, cost handling, testing requirements, and local quality gates.
- The concern aligns with CONTRACT-001 conformance scope and PRD P1-8.

**Status**: RESOLVED

---

### 3. Testing Capability Matrix Not Machine-Checkable

**AR Finding**: `testing` concern §Harness capability matrix declares a machine-checkable matrix with evidence IDs and CI gates. Only a human-readable baseline existed at `docs/helix/02-design/primary-harness-capability-baseline.md`. No JSON/YAML matrix with evidence-ID bindings or CI enforcement existed.

**Resolution**: Addressed as part of harness-infrastructure work (linked bead fizeau-d60b1694 / fizeau-3b1fb0f4).

**Verification**:
- `internal/harnesses/capability_matrix.json` — machine-checkable matrix with `version`, `timestamp`, per-harness capability rows, `status` (supported/experimental/unsupported), and evidence entries with `type` + `id` fields per CONTRACT-004 §Conformance Evidence.
- `internal/harnesses/capability_matrix_test.go` — CI test file enforcing:
  - Matrix loads successfully (`TestHarnessCapabilityMatrixLoads`)
  - Every `supported` capability has at least one evidence entry (`TestCapabilityMatrixEvidenceIDRequired`)
  - Harnesses covered: claude, codex, gemini, claude-tui (at minimum 3 required).
- The `testing` concern requirement for machine-checkable rows with evidence IDs is satisfied.

**Status**: RESOLVED

---

## Current Concern Health

| Concern | Area | Declared | Override | Quality Gates | Status |
|---------|------|----------|----------|---------------|--------|
| go-std | all | yes | Makefile-driven path documented | `make vet`, misspell lint, gosec, govulncheck, race | ALIGNED |
| testing | all | yes | Property-based, fuzz, E2E, harness capability matrix | matrix CI gate at `capability_matrix_test.go` | ALIGNED |
| hugo-hextra | ui | yes | Hextra v0.12.1, Hugo 0.160.0 | `make benchmark-workbench-smoke` | ALIGNED |
| python-uv | data | yes | Standalone script, stdlib unittest | `make benchmark-data`, compileall | ALIGNED |
| o11y-otel | lib, cli | yes (newly activated) | Span taxonomy, JSONL/OTel duality, cost handling | CONTRACT-001 conformance test | ALIGNED |

---

## Bug Fix Applied — Codex LimitID Normalization

During capability matrix verification, `TestHarnessCapabilityMatrixSupportedLimitIDsMatch` was failing (pre-existing at base-rev) because the Codex session-token-count API does not include a `limit_id` field in its rate-limit objects. When quota windows are cached from this source and later deserialized, `LimitID` is empty `""`, which is not in `SupportedLimitIDs`.

**Root cause**: Two gaps in `internal/harnesses/codex/`:
1. `session_token_count.go` `codexQuotaWindowFromRaw` reads `limit_id` from JSON, gets empty string when absent.
2. `contract004.go` `codexQuotaStatusFromSnapshot` copied windows verbatim without normalizing empty LimitIDs.

**Fix**: 
- Added `codexLimitIDForWindowMinutes(minutes int) string` in `session_token_count.go` (returns "codex" for ≤300 min, "codex-weekly" for larger windows, matching `doc.go` contract).
- Applied default in `codexQuotaWindowFromRaw` for new windows created via the API.
- Applied normalization in `codexQuotaStatusFromSnapshot` for windows from existing cache files.

Both `TestHarnessCapabilityMatrixSupportedLimitIDsMatch` and all `internal/harnesses/codex/...` tests pass after the fix.

---

## Remaining Open Items

None from concerns-drift scope. The three execution issues generated by this review issue have all been closed:

| Issue | Title | Status |
|-------|-------|--------|
| fizeau-397e9e0d | Reconcile .golangci.yml with go-std declared baseline | closed |
| fizeau-eeb8202a | Declare o11y-otel active or document deferral | closed |
| fizeau-3b1fb0f4 | Machine-checkable harness capability matrix + CI gate | addressed (capability_matrix.json + test present) |

---

## Conclusion

The concerns-drift functional area is ALIGNED. All three gaps identified in AR-2026-05-17-repo have been resolved: the go-std lint baseline divergence is documented with a ratified Makefile-driven override; o11y-otel is now an active concern with a full project override section; and the testing concern's requirement for a machine-checkable harness capability matrix is satisfied by `capability_matrix.json` with CI enforcement.
