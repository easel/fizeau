---
ddx:
  id: FEAT-003
  depends_on:
    - helix.prd
  review:
    self_hash: 8c4332150f3d5d591015e360231913d4e8f24f9b83f3678e65574e5f45f78e0d
    deps:
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:25:15Z"
---
# Feature Specification: FEAT-003 — LLM Providers

**Feature ID**: FEAT-003
**Status**: Approved
**Priority**: P0
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: Provider Parity
**Covered PRD Requirements**: FR-3
**Cross-Subsystem Rationale**: Normalizes local, cloud, and harness inference
behind the contracts used by execution, routing, and measurement.
**User Stories**: [US-003 — Switch Provider Systems Without Changing the Embedder](../user-stories/US-003-provider-parity.md)

## Overview

The provider surface is what backs PRD pillar #3 (make local models a real
option): one shared abstraction across cloud frontier providers and local
serving runtimes, so callers and benchmarks treat them uniformly.

Fizeau reaches models through concrete provider systems. Provider identity
names the actual source of model service (`lmstudio`, `openrouter`,
`anthropic`, `ollama`, `vllm`, `omlx`, and so on), not the wire protocol. The
OpenAI-compatible request/streaming shape is a shared SDK layer used by several
providers, but it is not a provider identity and must not appear in routing,
billing, telemetry, or cost reports.

Provider data feeds the routing engine in three ways:

- transport and auth: where requests go and how they authenticate;
- live signals: model discovery, health, limits, quota, utilization, and cost;
- policy defaults: billing classification and `include_by_default`.

ADR-009 makes provider billing and default inclusion part of the v0.11 routing
contract. Pay-per-token providers are default-deny for unpinned automatic
routing unless the provider is included by default and explicit metered-spend
opt-in allows them.

## Problem Statement

- Provider configs historically mixed transport details with routing policy.
- OpenAI-compatible local and cloud systems shared request code but needed
  different billing, auth, model discovery, and telemetry identity.
- Automatic routing must avoid accidental paid API spend while still allowing
  explicit pins and opt-in paid routing.

## Requirements

### Provider Interface

1. Providers implement the internal synchronous chat contract used by the core
   loop: messages in, tools/options in, response with content/tool calls/token
   usage/model/cost metadata out.
2. Streaming remains opt-in through the streaming provider interface; the core
   loop detects it at runtime.
3. Provider packages own provider-specific request shaping, auth defaults,
   endpoint defaults, model discovery, limit discovery, cost attribution, quota
   discovery, and utilization discovery.
4. Shared protocol helpers may exist, but they must not own provider identity,
   billing, URL heuristics, or routing decisions.

### Concrete Provider Systems

5. Supported provider `type` values include `lmstudio`, `omlx`, `ollama`,
   `vllm`, `rapid-mlx`, `llama-server`, `lucebox`, `openai`, `openrouter`,
   `anthropic`, `google`, and `virtual`.
6. `type: openai-compat` is rejected at config load. URL inference may map
   known local ports or hosts to concrete provider systems, but only during
   config normalization.
7. Local providers may expose endpoint pools. Endpoint names are operational
   identity for status, logs, sticky routing, and explicit endpoint selection;
   they are not the primary routing policy surface.
8. Cloud providers may use `base_url` as a single-endpoint override when that
   provider supports it.

Example:

```yaml
providers:
  studio:
    type: lmstudio
    include_by_default: true
    endpoints:
      - name: vidar
        base_url: http://vidar:1234/v1
      - name: eitri
        base_url: http://eitri:1234/v1

  openrouter:
    type: openrouter
    api_key: ${OPENROUTER_API_KEY}
    include_by_default: false
```

### Billing Classification

9. Billing is a first-class provider attribute with these values:

| Billing | Provider systems |
|---------|------------------|
| `fixed` | `lmstudio`, `llama-server`, `omlx`, `vllm`, `rapid-mlx`, `ollama`, `lucebox` |
| `per_token` | `openai`, `openrouter`, `anthropic`, `google` |
| `subscription` | built-in account harnesses `claude`, `codex`, `gemini` |
| unknown (`""`) | custom or unrecognized systems until config or manifest supplies billing |

10. The known provider-system rows are hardcoded through
    `BillingForProviderSystem`. Built-in account harnesses use the matching
    harness billing table.
11. Unknown provider systems must supply an explicit billing value before they
    can participate in policy routing. This avoids treating an unknown paid API
    as free or local by accident.

### Default Inclusion

12. `include_by_default` controls whether a provider participates in unpinned
    automatic routing.
13. Pay-per-token providers default to `include_by_default: false`.
14. Fixed/local providers and subscription/account harnesses may be included by
    default when the catalog or effective user config says so.
15. User config may override the catalog default for a provider.
16. Pay-per-token providers additionally require explicit metered-spend opt-in
    before they can participate in unpinned automatic routing. Provider default
    inclusion is necessary but not sufficient.
17. Explicit pins (`Provider`, `Harness`, or exact `Model`) override
    `include_by_default`, so an operator can deliberately use a provider that
    is excluded from unpinned automatic routing.
18. Explicit pins also override metered opt-in, so a deliberately pinned
    pay-per-token provider or exact model can be considered without enabling
    pay-per-token providers for automatic routing.
19. Pins do not override policy `Require` constraints. For example,
    `Policy=air-gapped` requires `no_remote`; pinning `openrouter` under that
    policy fails with a policy requirement error.

### Runtime Identity

20. Internal attempt metadata and public inventory distinguish the configured
    provider name, provider system/type, endpoint or server identity,
    requested/resolved/response model identity, and structured cost
    attribution. Exact field names and optionality are owned by CONTRACT-001
    and the internal provider contract, not duplicated in this feature.
21. The configured provider name identifies the operator's provider entry. The
    provider system identifies the adapter protocol/runtime. Endpoint or server
    identity distinguishes multiple instances of that provider system.
22. No code emits the shared protocol name as a provider identity.

### Limits and Discovery

23. `LookupModelLimits` resolves context window and output-token limits by:
    explicit config, then provider-specific live probe, then zero/unknown.
24. Provider-specific probes include:
    - `lmstudio`: native model metadata for loaded context length;
    - `omlx`: `/v1/models/status`;
    - `openrouter`: OpenRouter model metadata;
    - other providers: zero/unknown unless they expose a verified limit API.
25. Live model discovery wins over configured model hints. Configured default
    models are fallback hints, not the whole inventory.

### Utilization and Health

26. Local OpenAI-compatible providers may expose endpoint utilization. The
    configured `base_url` remains the API base; probes derive the server root
    when needed.
27. `vllm`, `rapid-mlx`, `llama-server`, and `omlx` normalize active/queued
    work, cache pressure where available, and freshness into provider-neutral
    utilization signals.
28. Utilization probe failure does not make an endpoint unavailable by itself.
    It produces stale/unknown utilization and routing falls back to service-owned
    in-flight lease counts plus normal health state.
29. LM Studio's chat stats may provide performance counters, but cache pressure
    remains unknown unless a verified native counter is available.

### Reasoning and Sampling

30. `reasoning` is the only public model-reasoning control in provider config,
    CLI execution, service requests, and embedding callers.
31. Provider-specific terminology such as thinking, effort, variant, and token
    budgets remains adapter terminology.
32. Sampling defaults remain catalog policy per ADR-007. Routing policies do
    not replace sampling profiles; the two surfaces are independent.

## Acceptance Criteria

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-003-01 | Provider configs accept concrete provider systems and reject `openai-compat` as a provider identity. | `go test ./internal/config ./...` |
| AC-FEAT-003-02 | Provider inventory and attempt metadata report configured provider name, provider system, endpoint identity, model, billing, and default-inclusion state. | `go test ./... -run 'ListProviders|AttemptMetadata|ProviderInfo'` |
| AC-FEAT-003-03 | `BillingForProviderSystem` classifies fixed, per-token, and unknown provider systems according to the table above; built-in account harnesses classify as subscription. | `go test ./internal/modelcatalog ./... -run Billing` |
| AC-FEAT-003-04 | Pay-per-token providers are excluded from unpinned automatic routing unless provider default inclusion and metered opt-in both allow them, while explicit pins can still select them. | `go test ./internal/routing ./... -run 'Policy|IncludeByDefault|Metered|Pin'` |
| AC-FEAT-003-05 | Policy requirements still beat pins: a remote provider pinned under `air-gapped` / `no_remote` fails with `ErrPolicyRequirementUnsatisfied`. | `go test ./internal/routing ./... -run Policy` |
| AC-FEAT-003-06 | Provider limit and utilization probes preserve unknown/stale states instead of guessing or marking endpoints unavailable solely because utilization failed. | `go test ./internal/provider ./...` |

## Constraints and Assumptions

- Billing classification is intentionally conservative; unknown means unknown.
- Local/fixed billing does not mean zero operating cost in cost reports unless
  pricing is reported or explicitly configured.
- Ordinary request execution does not fetch remote catalog manifests.
- Endpoint labels are operational diagnostics, not policy names.

## Dependencies

- `FEAT-004` for routing-policy interpretation of provider metadata.
- `FEAT-005` for cost and usage projection.
- `ADR-009` for the v0.11 routing surface.

## Out of Scope

- Managing local model downloads or server processes.
- Automatic paid-provider opt-in.
- Reintroducing provider route tables or user-authored fallback chains.
