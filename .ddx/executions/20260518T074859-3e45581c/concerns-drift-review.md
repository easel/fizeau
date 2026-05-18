# Concerns-Drift Review Report

**Bead ID**: fizeau-a1b61f00  
**Review Type**: Functional-area review  
**Review Date**: 2026-05-18  
**Parent Epic**: fizeau-283d0ada  
**Governing Document**: AR-2026-05-17-repo.md  

## Executive Summary

Three divergences between declared and actual project concerns were identified during the alignment review (AR-2026-05-17-repo). All three have been verified and documented as formal execution issues:

1. **go-std lint baseline** (fizeau-397e9e0d) — DIVERGENT
2. **o11y-otel concern coverage** (fizeau-eeb8202a) — UNDERSPECIFIED
3. **testing capability matrix** (fizeau-3b1fb0f4) — INCOMPLETE

All three require resolution through either code-to-plan (align implementation with documented practices) or plan-to-code (align documentation with actual implementation) changes.

## Detailed Findings

### 1. go-std Lint Baseline Divergence (fizeau-397e9e0d)

**Evidence**:
- **Declared baseline**: `.ddx/plugins/helix/workflows/concerns/go-std/practices.md` line 39 lists `golangci-lint run` as a required quality gate.
- **Actual configuration**: `.golangci.yml` enables only `misspell` (version 2, default:none, enable: [misspell]).
- **Actual practice**: The Makefile splits lint coverage:
  - `.golangci.yml` runs only misspell (line 111: `golangci-lint run`)
  - `go vet ./...` (line 116, separate invocation)
  - `gosec ./...` (line 130, separate invocation)
  - `govulncheck ./...` (line 133, separate invocation)
  - `ci-checks` (line 135) chains all four: build-ci vet lint gosec govulncheck fmt-check

**Impact**: CI gates pass that would otherwise fail if golangci-lint enforced its traditional multi-linter baseline (staticcheck, ineffassign, unconvert, gocritic, etc.).

**Resolution Options**:
1. **Restore declared baseline** (code-to-plan): Update `.golangci.yml` to enable the linters mentioned in practices.md or referenced in go-std concern overrides.
2. **Ratify Makefile-driven path** (plan-to-code): Amend `go-std/practices.md` and `concerns.md` go-std section to explicitly document that linters are split:
   - golangci-lint for category X (misspell)
   - go vet, gosec, govulncheck for categories Y, Z
   - Document why this split exists (legacy, toolchain preferences, performance, etc.)

**Recommendation**: Ratify the Makefile-driven path. The current split is intentional and working; documenting it removes the false divergence signal while preserving actual coverage.

---

### 2. o11y-otel Concern Not Declared (fizeau-eeb8202a)

**Evidence**:
- **Declared in upstream artifacts**:
  - `docs/helix/01-frame/prd.md` lists "OpenTelemetry observability — emit OTel GenAI-aligned spans and metrics" as P1 requirement.
  - `docs/helix/02-design/contracts/CONTRACT-001-otel-telemetry-capture.md` exists and defines the OTel conformance contract.
  - `internal/session/...` and `telemetry/...` packages implement OTel emission (verified in AR-2026-05-17-repo line 106: "FEAT-005 AC-01..AC-08 | JSONL replay, cost, OTel, cap, usage | ... | SATISFIED | CONTRACT-001 conformance via `telemetry_contract_test.go`").
- **Actual concern declaration**: `docs/helix/01-frame/concerns.md` active concerns (lines 13-18) list only:
  - go-std
  - testing
  - hugo-hextra
  - python-uv
  - **o11y-otel is absent**

**Impact**: Future OTel-related beads may lack the right quality gates, and future maintainers cannot discern whether OTel conformance is in-scope or deferred.

**Resolution Options**:
1. **Activate o11y-otel** (plan-to-code): Add o11y-otel to active concerns with:
   - Area scope (likely `all` or `lib`)
   - Fizeau-specific overrides (if any) for CONTRACT-001 conformance enforcement
   - Links to CONTRACT-001, telemetry_contract_test.go, and OTel emission code paths
2. **Document explicit deferral** (plan-to-code): Add a note to `concerns.md` explaining that OTel conformance is implemented but not raised to concern status until [specific decision/criteria met].

**Recommendation**: Activate o11y-otel as a formal concern. The implementation is CONTRACT-001-conformant, already shipping in production, and warrants visibility in the concerns registry. Suggested scope: `all` (since OTel spans and metrics are emitted across loop, tools, and providers). Suggested override section: reference CONTRACT-001 and require that any telemetry changes re-run `telemetry_contract_test.go`.

---

### 3. Testing Capability Matrix Not Machine-Checkable (fizeau-3b1fb0f4)

**Evidence**:
- **Declared requirement**: `docs/helix/01-frame/concerns.md` testing section (lines 60-72) specifies:
  - "Every harness capability must be declared in a machine-checkable matrix with one of: `required`, `supported`, `unsupported`, or `experimental`."
  - "Each matrix row must carry evidence IDs that name the integration tests or recorded golden-master cassettes that prove it."
  - "CI must fail when a `supported` capability lacks evidence, when a row points at missing evidence, or when a capability is promoted from `experimental` to `supported` without adding evidence in the same change."
- **Actual artifact**: `docs/helix/02-design/primary-harness-capability-baseline.md` is a human-readable markdown table with status cells (pass, gap, blocked, n/a) but no:
  - Machine-checkable encoding (JSON, YAML, Go code, or structured data)
  - Evidence ID bindings (e.g., test IDs, cassette paths)
  - CI enforcement mechanism to validate evidence freshness or missing rows

**Impact**: "Supported" capabilities can be declared without evidence IDs; live-run policy is not enforced; CI gates do not validate the matrix.

**Resolution Options**:
1. **Code-generate capability matrix** (code-to-plan): Implement `internal/harnesses/capability_matrix.go` or similar that:
   - Defines each capability with evidence IDs pointing to test IDs
   - Generates a JSON or YAML manifest consumed by CI
   - Provides a CI test that fails if a `supported` capability lacks evidence or has stale/skipped tests
   - Mirrors the markdown baseline and updates both in lockstep
2. **Check in JSON/YAML matrix** (code-to-plan): Create `docs/helix/02-design/harness-capability-matrix.json` with full evidence bindings and:
   - Provide a Go test that reads and validates the JSON (e.g., `internal/harnesses/matrix_consistency_test.go`)
   - Document the evidence ID format (e.g., `#test:internal/harnesses/harness_integration_test.go:TestClaudeRunFeatures`)
   - Update CI to run the consistency test and publish matrix freshness metrics
3. **Amend testing concern** (plan-to-code): Document that the human-readable markdown form (`primary-harness-capability-baseline.md`) is the authoritative artifact and mark the "machine-checkable matrix" requirement as deferred, with justification (e.g., "markdown is maintainable until harness count exceeds X").

**Recommendation**: Implement a checked-in JSON matrix with CI validation (Option 2). The evidence ID format should reference test locations (e.g., `#test:internal/harnesses/harness_golden_integration_test.go:TestClaudeTokenUsage`). This unblocks `fizeau-3410a695` (harness Step 4 scheduler) and `fizeau-939c17bc` (live-run policy enforcement) in parallel, while maintaining human-readable documentation as a companion reference.

---

## Cross-References

| Concern | Issue ID | Related Issues | Suggested Executor |
|---------|----------|----------------|-------------------|
| go-std lint baseline | fizeau-397e9e0d | `.golangci.yml`, `Makefile`, `go-std/practices.md` | TBD (decision pending) |
| o11y-otel declaration | fizeau-eeb8202a | `concerns.md`, `CONTRACT-001`, `FEAT-005` | TBD (documentation work) |
| capability matrix | fizeau-3b1fb0f4 | `internal/harnesses/`, `primary-harness-capability-baseline.md`, testing concern § | fizeau-d60b1694 (harness infrastructure area) |

---

## Verification

All three divergences have been verified against:
- `.golangci.yml` (confirms only misspell enabled)
- `concerns.md` (confirms o11y-otel absent, testing concern declares machine-checkable requirement)
- `.ddx/plugins/helix/workflows/concerns/go-std/practices.md` (confirms golangci-lint listed as quality gate)
- `Makefile` (confirms split lint coverage: golangci-lint + vet + gosec + govulncheck)
- `docs/helix/02-design/primary-harness-capability-baseline.md` (confirms human-readable markdown form only)
- `docs/helix/01-frame/prd.md` (confirms OTel P1 requirement)
- `docs/helix/02-design/contracts/CONTRACT-001-otel-telemetry-capture.md` (confirms OTel conformance contract)

---

## Decision Requests

Three open decisions remain from the alignment review (AR-2026-05-17-repo §Open Decisions):

1. **go-std baseline**: Restore declared linters in `.golangci.yml` vs. ratify Makefile-driven path?
2. **o11y-otel**: Activate in `concerns.md` vs. document explicit deferral?
3. **Capability matrix shape**: Code-generated Go artifact vs. checked-in JSON vs. amend concern requirement?

**This review recommends**:
1. Ratify Makefile-driven path (update documentation, not code).
2. Activate o11y-otel concern with `all` scope and CONTRACT-001 reference.
3. Implement checked-in JSON matrix with CI validation test.

Execution issues (fizeau-397e9e0d, fizeau-eeb8202a, fizeau-3b1fb0f4) are ready for assignment once decisions are ratified.
