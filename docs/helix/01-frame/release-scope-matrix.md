---
ddx:
  id: helix.release-scope-matrix
  depends_on:
    - helix.product-vision
    - helix.prd
  review:
    self_hash: 52569e9b934b7fc80919a782c031cf2927668bca2e82239a1e6c3c65f640327b
    deps:
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
      helix.product-vision: eb5af3663734d35e7b42963ce12e39adc19147aa2df25fe9bd3887793217836c
    reviewed_at: "2026-07-20T22:42:56Z"
---
# v0.15 Release Scope Matrix

## Purpose

This is the release-scope authority between the product vision and the release
checklist. A release gate is required only when it proves an outcome in the
canonical PRD requirements (FR-1 through FR-8), or when it is the minimum
reliability condition for safely delivering one. Detailed ADR mechanisms do
not create a product requirement on their own.

**Classification:** Core gates prove a PRD outcome. Supporting reliability
gates prevent a core path from leaking processes, corrupting evidence, or
misrepresenting a release. Experimental/deferred work is not a v0.15 blocker.
Decision-required rows need an explicit compatibility decision before they can
be gates.

## Decision Record: Public Cost Presence and Provenance

**Decision:** retain and freeze the bounded v0.15 public cost migration.

FR-5 requires an observable distinction between a known cost (including a
known zero) and an explicitly unknown cost, plus provenance for a known
amount. That is the product semantic. `CostUSD *float64` paired with
`CostSource` is the already-selected public representation of that semantic;
it is not a reason to widen cost APIs or to turn every internal price or
routing value into an optional public result.

The source-incompatible change relative to v0.14.50 is intentional: it has
landed on the pre-v0.15 mainline before the v0.15 tag. Consumers of the
affected keyed literals and selector expressions must branch on nil and
`CostSource`, then explicitly dereference a present amount. This release does
not add a compatibility adapter that guesses presence from a scalar value.

The freeze applies to the current public final-result and durable end
projections named in CONTRACT-003: `ServiceFinalData`, `DrainExecuteResult`,
`SessionEndData`, and `ServiceOverrideOutcome`. Do not extend this migration
to routing/catalog price fields, benchmark result schemas, new public result
types, or additional compatibility bridges in v0.15. Future public cost-shape
changes require a separately scoped compatibility decision.

## Matrix

| Release-gate area | Classification | PRD trace | v0.15 outcome and boundary |
|---|---|---|---|
| Public construction, bounded `Execute`, service events | Core | FR-1 | An embedder can use the root facade without concrete internals. |
| Workspace tools and tool-attempt records | Core | FR-2, FR-5 | Tool schemas, working-directory semantics, and recorded attempts remain stable. |
| Native providers, local runtimes, and TUI/subprocess wrappers; dynamic model discovery | Core | FR-3, FR-4 | One provider-neutral request/event/usage surface; supported wrapper discovery and launch are verified. |
| Catalog, exact pins/policy intent, selected-route attribution, and one-route dispatch | Core | FR-4 | Fizeau chooses and attributes one route; callers retain semantic retry and cross-harness escalation. |
| Context provenance, capacity enforcement, and residual-overflow behavior | Supporting reliability | FR-4, FR-5 | Prevent an invalid dispatch or false capacity claim; do not grow routing policy beyond correctness of the selected route. |
| Session records, replay, timing/token data, and known-or-unknown cost provenance | Core | FR-5 | Evidence is service-owned, replayable, and never silently invents cost or timing. |
| Thin `fiz` facade and machine-readable inspection/harness surfaces | Core | FR-6 | The CLI proves the library rather than becoming another execution architecture. |
| Versioned platform artifacts, artifact names, explicit installer/update path | Core | FR-7 | Operators can install and verify the proof CLI without runtime update coupling. |
| Self-describing benchmark result cells and preserved provenance | Core | FR-8 | Local/cloud comparisons retain measurement semantics; website presentation is not itself a release gate. |
| Caller-death containment, terminal-after-cleanup ordering, stale-identity refusal, and recovery retention | Supporting reliability | FR-1, FR-3, FR-6 | Wrapped execution must not leave an owned process behind or report a false terminal result. Limit this to supported execution paths. |
| Governed-document validation and freshness | Supporting reliability | FR-1–FR-8 | The PRD, test plan, release checklist, and implementation plan agree on the release scope. |
| Portable-runtime closure preparation, mount projection, namespace launcher, PID-1/signal isolation, and OCI proof | Experimental/deferred | No canonical FR | Retain ADR-014 material as an optional future Linux-isolation experiment. It neither blocks v0.15 nor adds mock/consumer compatibility obligations. |
| Continuation (`FizeauService.Continue`) | Decision required | Indirectly FR-1, FR-5 | Decide whether the existing bounded API remains a v0.15 public commitment or becomes experimental. **Keep:** verify the current API only; no new resume policies, persistence formats, or lifecycle features. **Defer:** remove it from v0.15 gates and preserve it only as additive experimental code. Either choice affects public interface and external mocks. |
| Bounded public cost presence/provenance migration | Core (frozen compatibility boundary) | FR-5 | Retain the existing `CostUSD *float64` plus `CostSource` representation on the four final-result and durable end projections named in the decision record. The deliberate pre-v0.15 source break from v0.14.50 is complete; validate nil/zero/positive/source semantics and affected consumer compilation, but do not widen the migration. |

## Gate Rule

The v0.15 checklist may require the Core and Supporting reliability rows above.
It must not require Experimental/deferred rows. A Decision-required row becomes
required only after its explicit choice is recorded in the public contract and
the checklist is reduced to the selected, bounded evidence. The cost migration
is no longer decision-required: the decision record above fixes its v0.15
boundary.

## Direct Sources

- [Product vision](../00-discover/product-vision.md) — mission, product
  boundary, and runtime ownership rationale.
- [PRD](prd.md) — FR-1 through FR-8 are the canonical product decomposition.
- [Release checklist](../05-deploy/release-checklist.md) — operational gate
  inventory; this matrix controls its v0.15 classification.
