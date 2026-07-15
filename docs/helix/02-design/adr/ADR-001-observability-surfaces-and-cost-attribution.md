---
ddx:
  id: ADR-001
  depends_on:
    - helix.prd
    - helix.arch
    - FEAT-005
    - SD-001
  review:
    self_hash: 18d3c8a6d83435bbe498008e7093031ad4713640c3ac40a2688ebaed16430780
    deps:
      FEAT-005: 0a963abf9f30cb7551a30302fa853525e417f03cd1611603aec221d0159998e0
      SD-001: 7123b4d558d2ddd35289bf49390fde9e00b52081cbe90de37986d13fbbf36988
      helix.arch: 076e620580b77517a3f561f5ce842cf1c09e6cef625c13e0a1adb874ae0e19ef
      helix.prd: 12c9ecc92726e3d50896a8afb51224906edfea9863d8114d39a6c2a0a2e54003
    reviewed_at: "2026-07-15T16:59:30Z"
---
# ADR-001: Observability Surfaces and Cost Attribution

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-04-09 | Accepted | Fizeau maintainers | `FEAT-005`, `FEAT-006`, `SD-001` | High |

## Context

| Aspect | Description |
|--------|-------------|
| Problem | The planning stack still treats OpenTelemetry as an optional add-on while the project needs one analytics model that can be compared against Claude Code and Codex without losing replay fidelity or corrupting cost data. |
| Current State | Fizeau writes JSONL session logs for replay. Existing docs describe per-model pricing-table estimation and leave OTel undecided. Research across Claude Code, Codex, and OTel shows that JSONL-style transcripts are useful for replay, while OTel GenAI conventions are the best available interoperability layer for analytics. |
| Requirements | Preserve exact local replay, support cross-tool analytics, record cost without guessing, distinguish local runtimes such as `bragi`, `vidar`, and `grendel`, and expose enough timing data to compute throughput only when the provider supplies a valid timing window. |

## Decision

We will keep JSONL session logs as the replay artifact and adopt
OpenTelemetry GenAI-aligned spans and metrics as the canonical analytics
surface.

We will record provider- or gateway-reported cost when it is available. If no
reported cost exists, we will use only explicit runtime-specific pricing for
the exact provider system and resolved model. If neither exists, cost remains
unknown. Fizeau will not guess cost from generic stale price tables.

We will use standard OTel GenAI fields wherever the semantic conventions cover
the need, and a `ddx.*` namespace for gaps such as billed cost source,
runtime-specific timing, and provider-system identity.

The detailed telemetry schema, formulas, and interoperability rules live in
`docs/helix/02-design/contracts/CONTRACT-001-otel-telemetry-capture.md`.

**Key Points**: JSONL remains replay-first | OTel is analytics-first |
known cost is preserved and unknown cost stays unknown

## Alternatives

| Option | Pros | Cons | Evaluation |
|--------|------|------|------------|
| Keep JSONL as the only observability surface | Lowest implementation complexity, preserves replay, no collector dependency | Weak interoperability, poor analytics ergonomics, no standard cross-tool schema | Rejected: insufficient for the stated analytics goal |
| Copy Claude Code or Codex local storage formats directly | Easier superficial compatibility with one tool at a time | Vendor internals are not a stable universal contract, locks Fizeau to foreign storage assumptions, still leaves cost/timing gaps | Rejected: wrong abstraction boundary |
| **Use JSONL for replay and OTel for analytics, with project-namespaced extensions for gaps** | Preserves exact session reconstruction, aligns with the strongest emerging standard, supports one analytics tool across Fizeau, Claude, and Codex | Adds telemetry implementation work, requires careful schema discipline, some fields such as cost still need custom attributes | **Selected: best fit for interoperability without sacrificing replay fidelity** |

## Consequences

| Type | Impact |
|------|--------|
| Positive | Replay and analytics are both first-class and no longer compete for one storage shape. |
| Positive | Fizeau can normalize analytics across Claude Code, Codex, and its own session logs using OTel-compatible ingestion. |
| Positive | Variable-price gateways such as OpenRouter can preserve actual billed cost instead of being recomputed later from unstable tables. |
| Positive | Local runtimes such as `bragi`, `vidar`, and `grendel` become explicit analytics dimensions rather than disappearing behind a generic local-provider bucket. |
| Negative | Telemetry adds implementation complexity and collector configuration surface beyond the current JSONL logger. |
| Negative | Some required analytics fields, especially cost source and runtime-specific timing, still need a DDX-specific namespace until OTel standardizes them. |
| Neutral | JSONL remains necessary even after OTel lands, because replay and forensic inspection require exact turn bodies and sequence order. |

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| OTel overhead is high enough to distort per-tool-call sessions | M | M | Keep export best-effort, benchmark overhead, and allow sampling/export config without removing local JSONL logging |
| Providers expose inconsistent timing breakdowns, making throughput incomparable | H | M | Derive rates only from validated timing windows and leave missing rates unset |
| Teams misuse `ddx.*` fields and create schema drift | M | H | Keep a single documented telemetry contract in FEAT-005 and SD-001, with explicit namespaced-field ownership |
| Session totals become ambiguous when some turns have known cost and others do not | M | M | Define totals as unknown if any contributing turn is unknown, and surface unknown-cost counts in usage reporting |

## Validation

| Success Metric | Review Trigger |
|----------------|----------------|
| PRD, feature specs, architecture, and solution design all describe the same split model: JSONL replay plus OTel analytics | Any governing artifact still framing OTel as optional or JSONL as the sole observability surface |
| Providers that report cost preserve that cost without recomputation drift | Reported billing differs from stored billing after ingestion |
| Unknown-cost sessions remain explicitly unknown | Any code path silently fills unknown cost from a generic fallback table |
| Throughput analytics are emitted only when backed by matching timing windows | A dashboard displays input, output, or cache tok/s without source timing evidence |
| One analytics tool can ingest Fizeau telemetry alongside Claude/Codex adapters | Cross-tool ingestion requires bespoke schema logic for Fizeau alone |

## Concern Impact

- **No concern impact**: This ADR does not override the repo's active Go/library
  concerns. It selects an observability strategy within existing concerns.

## References

- [PRD](../../01-frame/prd.md)
- [FEAT-005 Logging, Replay, and Cost Tracking](../../01-frame/features/FEAT-005-logging-and-cost.md)
- [FEAT-006 Standalone CLI](../../01-frame/features/FEAT-006-standalone-cli.md)
- [Architecture](../../02-design/architecture.md)
- [SD-001 Agent Core](../../02-design/solution-designs/SD-001-agent-core.md)
- [CONTRACT-001 OTel Telemetry Capture](../../02-design/contracts/CONTRACT-001-otel-telemetry-capture.md)
- [OpenTelemetry GenAI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/)
- [OpenTelemetry GenAI Metrics](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/)

## Review Checklist

- [x] Context names a specific problem
- [x] Decision statement is actionable
- [x] At least two alternatives were evaluated
- [x] Each alternative has concrete pros and cons
- [x] Selected option's rationale explains why it wins
- [x] Consequences include positive and negative impacts
- [x] Negative consequences have mitigations
- [x] Risks are specific with probability and impact assessments
- [x] Validation section defines review triggers
- [x] Concern impact is complete
- [x] ADR is consistent with governing feature spec and PRD requirements
