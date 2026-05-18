# Providers Area Review — AR-2026-05-17-repo

## Review Status: COMPLETE

**Date Completed**: 2026-05-18  
**Review Bead**: fizeau-8e37e536  
**Parent Epic**: fizeau-283d0ada  
**Governing Document**: `docs/helix/06-iterate/alignment-reviews/AR-2026-05-17-repo.md`  

## Scope

Functional-area review of the **providers** subsystem (FEAT-003) as part of the repo-wide HELIX alignment review (AR-2026-05-17).

## Findings Summary

The providers area is classified as **INCOMPLETE** with four identified gaps:

### 1. Unit Test Coverage Gap (AC-06/AC-07)
- **Issue**: FEAT-003 AC-06 and AC-07 require unit tests for `LookupModelLimits` and `probeProviderFlavor`
- **Current State**: Only integration tests exist at `internal/provider/openai/discovery_integration_test.go:107,129`
- **Classification**: INCOMPLETE (code-to-plan)
- **Discovered Issue**: [fizeau-8c1d23af](https://...) — unit tests for LookupModelLimits + probeProviderFlavor
- **Reference**: FEAT-003 AC-06, AC-07; AR-2026-04-17 (prior alignment review)

### 2. ADR-010 Introspection Adapter Coverage
- **Issue**: ADR-010 §6 mandates "Introspection adapters as first-class" but only 3/7 provider adapters are implemented
- **Current State**: Adapters present for `ds4`, `llamaserver`, `lucebox`; absent for OpenRouter, Ollama, vLLM, rapid-mlx
- **Classification**: INCOMPLETE (code-to-plan)
- **Discovered Issue**: [fizeau-cdbfcf6d](https://...) — land ADR-010 introspection adapters for OpenRouter, Ollama, vLLM, rapid-mlx
- **Reference**: ADR-010 §6'; `internal/provider/{ds4,llamaserver,lucebox}/introspection.go`

### 3. ADR-010 Amendment Items Pending
- **Issue**: ADR-010 amended 2026-05-11 with new wire format requirements not yet implemented
- **Current State**: 
  - New wire format enum absent
  - llamaserver still emits top-level `enable_thinking` instead of amended format
  - reasoning-token telemetry not falling back from `message.reasoning_content`
- **Classification**: INCOMPLETE (code-to-plan), sequenced after introspection adapters
- **Discovered Issue**: [fizeau-a5d07641](https://...) — finish ADR-010 amendments (OpenAIEffort, llamaserver kwargs, reasoning-token fallback)
- **Reference**: ADR-010 §5'–§10' (amended 2026-05-11)

### 4. OpenRouter Credit Probe Governance
- **Issue**: `service_openrouter_credit.go` implements production-grade credit-balance probing with no governing solution design
- **Current State**: 17 KB implementation with rate-limit handling and quota-pool integration, but no SD/ADR documentation
- **Classification**: UNDERSPECIFIED (plan-to-code)
- **Discovered Issue**: [fizeau-7ca8ec69](https://...) — govern OpenRouter credit probe (SD-013 amendment or ADR-011 extension)
- **Evidence**: `service_openrouter_credit.go`, `service_openrouter_credit_test.go`, `service_openrouter_credit_failure_modes_test.go`; implicit coverage in SD-013 signal matrix and ADR-011 cost-based routing

## Issue Filing Status

All four identified gaps have been filed as execution beads under the current review bead:

| Bead ID | Title | Type | Status |
|---------|-------|------|--------|
| fizeau-8c1d23af | providers: add unit tests for LookupModelLimits + probeProviderFlavor (FEAT-003 AC-06/AC-07) | task/test | open |
| fizeau-cdbfcf6d | providers: land ADR-010 introspection adapters for OpenRouter, Ollama, vLLM, rapid-mlx | task/feature | open |
| fizeau-a5d07641 | providers: finish ADR-010 amendments — OpenAIEffort wire format, llamaserver kwargs envelope, reasoning-token content fallback | task/feature | open |
| fizeau-7ca8ec69 | providers: govern OpenRouter credit probe (SD-013 amendment or ADR-011 extension) | task/docs | open |

All beads are tagged with `discovered-from:fizeau-8e37e536`.

## Quality Findings

| Dimension | Concern | Severity | Resolution |
|-----------|---------|----------|------------|
| Maintainability | OpenRouter credit probe: production code with rate-limit handling and quota-pool integration but no design doc | medium | governance-only doc edit (fizeau-7ca8ec69) |

## Verification

**Review completion checklist**:

- [x] Functional-area scope defined (FEAT-003, ADR-010, SD-013)
- [x] Governing artifacts reviewed (FEAT-003, ADR-010 §5'–§10', SD-013 signal matrix, ADR-011 cost-based routing)
- [x] Test coverage assessed (AC-06/07 unit test gap identified)
- [x] Design coverage assessed (ADR-010 introspection adapters, OpenRouter probe governance)
- [x] Code state inventoried (service_openrouter_credit, provider introspection implementations)
- [x] Gaps classified (INCOMPLETE/UNDERSPECIFIED)
- [x] Issues filed and properly parented (4 beads, all `discovered-from:fizeau-8e37e536`)
- [x] Quality findings documented (maintainability concern with resolution)
- [x] Durable record created (this document in alignment review + individual beads)

## Alignment Assessment

**Classification**: INCOMPLETE

The providers area meets the definition of INCOMPLETE because four identified gaps have been filed as follow-up execution beads and are sequenced for implementation:

1. Two feature-completion gaps (introspection adapters, amendments) are code-to-plan
2. One test-coverage gap (AC-06/07) is code-to-plan (previously filed in AR-2026-04-17)
3. One governance gap (OpenRouter probe) is plan-to-code

None of these gaps block acceptance of other features; each is self-contained or sequenced (amendments depend on introspection adapters).

## Next Steps

1. **Parallel execution**: fizeau-8c1d23af (tests), fizeau-7ca8ec69 (governance) can proceed independently
2. **Sequential execution**: fizeau-cdbfcf6d (adapters) must complete before fizeau-a5d07641 (amendments)
3. **Verification**: Each issue links back to this review via `discovered-from:fizeau-8e37e536` and parent relationships

---

**Review Author**: DDx Checkpoint (ar-2026-05-17-repo process)  
**Review Gate**: fizeau-8e37e536 (Review providers)  
**Alignment Review Document**: `docs/helix/06-iterate/alignment-reviews/AR-2026-05-17-repo.md` (lines 119–121, 141–142, 170)
