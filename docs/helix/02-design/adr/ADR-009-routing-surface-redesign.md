---
ddx:
  id: ADR-009
  depends_on:
    - ADR-005
    - ADR-006
    - FEAT-004
  review:
    self_hash: d9968b4818b0f45508f3e0689b403ff6997c2722924e7457605bc43080ae5a4a
    deps:
      ADR-005: e47168fa6ebdb3a0f57d9a5e34cc638563f74fe5c529f73e0bee327259c7bec5
      ADR-006: 511bae45baeef8e764665393c35d5d80490ef1ed29a1efd769a063965a9e33bc
      FEAT-004: 9761114849a85ae13627ea086fdfb1d332edda875fd81cb3769096bedc7eaeae
    reviewed_at: "2026-07-16T07:25:15Z"
---
# ADR-009: Routing Surface Redesign

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-09 | Accepted | Fizeau maintainers | `ADR-005`, `ADR-006`, `CONTRACT-003`, `FEAT-003`, `FEAT-004`, `FEAT-006` | High |

## Context

ADR-005 and ADR-006 established the power-routing engine and the override-as
failure-signal framing, but the public surface still carried v0.10 vocabulary:
profiles, model references, compatibility targets, aliases, and surface policy
projections. That vocabulary made sense during migration from route tables, but
it now hides the v0.11 contract:

- callers choose a named routing policy or explicit numeric power bounds;
- exact model/provider/harness pins are override hatches;
- model catalog data is schema v5 and intentionally simpler;
- pay-per-token providers are excluded from unpinned automatic routing unless
  provider default inclusion and explicit metered-spend opt-in both allow them.

Keeping both names (`Profile` and `Policy`, `ModelRef` and `Model`, aliases and
canonical entries) would make the service contract harder to reason about and
would keep old CLI flags alive as accidental primary UX.

## Decision

### Profile→Policy Rename

The public routing-intent surface is `Policy`, not `Profile`. The canonical
policy names are `cheap`, `default`, `smart`, and `air-gapped`. The service
exposes `ListPolicies(ctx) ([]PolicyInfo, error)` and accepts `Policy` on
`ServiceExecuteRequest` and `RouteRequest`.

`ListProfiles`, `ResolveProfile`, `ProfileAliases`, `--profile`, profile route
status keys, and profile-specific public request fields are removed. ADR-005
and ADR-006 are superseded in part where they present profile as the primary
user routing surface.

ADR-007 sampling profiles are unaffected. They remain generation-parameter
bundles in the catalog, not routing policies.

### ModelRef Removal

`ModelRef`, `--model-ref`, and `model_ref` are removed from the public routing
surface. Callers use:

- `Policy` / `--policy` for named routing intent;
- `MinPower` and `MaxPower` / `--min-power` and `--max-power` for numeric
  routing intent;
- `Model` / `--model` for an exact model pin.

There is no intermediate catalog alias or tier-reference field in the service
contract. Migration names may exist as explicit compatibility errors or
operator guidance, but not as successful routing inputs.

### Catalog Schema v5

The model catalog manifest is schema v5. Schema v5 removes routing
`target`, `aliases`, and user-visible `surface_policy` concepts from the
primary policy surface. The catalog now has:

- concrete `models` entries with model metadata, power, cost, deployment class,
  context, and surface strings;
- top-level `policies` with `min_power`, `max_power`, `allow_local`, and
  `require[]`;
- top-level `providers` with provider `type`, `include_by_default`, and
  billing classification when the hardcoded table cannot infer it.

`SurfacePolicy` may remain as a narrow compatibility container for reasoning
defaults while those defaults move fully to model metadata. It is not a user
routing primitive.

### Billing, Metered Opt-In, and Default Inclusion

Billing classification is an explicit routing input. Fizeau uses the hardcoded
`BillingForProviderSystem` mapping for known provider systems:

- `fixed`: `lmstudio`, `llama-server`, `omlx`, `vllm`, `rapid-mlx`, `ollama`,
  `lucebox`;
- `per_token`: `openai`, `openrouter`, `anthropic`, `google`;
- `subscription`: built-in harness account surfaces `claude`, `codex`,
  `gemini`.

Unknown provider systems must supply an explicit billing value before they can
participate in policy routing.

Pay-per-token providers are default-deny for unpinned automatic routing. A
metered candidate participates only when both conditions hold:

1. the provider is included by default (`include_by_default: true` from user
   config or catalog policy); and
2. the operator has explicitly accepted metered spend for automatic routing,
   for example with `routing.allow_metered: true`.

Fixed/local and subscription/account surfaces may be included by default when
the catalog marks them as such. They do not require the metered-spend opt-in.

An unpinned request is one with no `Harness`, no `Provider`, and no exact
`Model`. `Policy`, `MinPower`, `MaxPower`, `Reasoning`, capability flags, and
token estimates are routing intent, not pins.

### Pin Precedence

Hard pins still narrow the candidate set before scoring:

1. `Harness`
2. `Provider`
3. `Model`

Pins override default inclusion and metered opt-in: an explicitly pinned
pay-per-token provider or exact model may be considered even when that provider
is default-deny for unpinned automatic routing.

Pins do not override policy `require[]`. In particular, `Policy=air-gapped`
adds `require=["no_remote"]`; pinning `Provider=openrouter`, `Harness=claude`,
or any other remote-only surface fails with `ErrPolicyRequirementUnsatisfied`
instead of silently widening the policy.

In short: a pin overrides default inclusion and metered opt-in but NOT
`require[]`.

Policy power bounds are soft scoring inputs for automatic routing, not closed
candidate lists. Undershooting `MinPower` is penalized more heavily than
overshooting `MaxPower`, because an underpowered model is more likely to fail
the task while a stronger model is primarily a cost/latency tradeoff. Exact
model pins keep exact identity; policy bounds do not substitute a different
model.

## Consequences

### Positive

- The service contract has one naming system: policy for routing intent,
  model/provider/harness for hard pins.
- Default-deny pay-per-token behavior is explainable from provider billing,
  `include_by_default`, and explicit metered-spend opt-in, reducing accidental
  spend.
- The catalog schema is smaller and easier to validate. Compatibility aliases
  and targets no longer leak into user-facing routing docs.
- Route-status and CLI JSON use `policy`, making operator displays match the
  Go API.

### Negative

- v0.11 is a hard break for callers using `--profile`, `--model-ref`,
  `ListProfiles`, `ResolveProfile`, or `ProfileAliases`.
- Historical ADR prose remains partly stale unless clearly marked. ADR-005 and
  ADR-006 are preserved as history, but this ADR owns the v0.11 public surface.
- Operators with pay-per-token providers must opt in deliberately when they
  want those providers to participate in unpinned automatic routing.

## Migration

Callers should migrate as follows:

- `--profile cheap|standard|smart` -> `--policy cheap|default|smart`
- `--model-ref <name>` -> `--policy <name>` when the old name was policy
  intent, or `--model <model>` when it was an exact model choice
- `ListProfiles` -> `ListPolicies`
- `ResolveProfile` / `ProfileAliases` -> no replacement public method; use
  `ResolveRoute` for decision previews and `ListPolicies` for policy metadata
- pay-per-token automatic routing -> set `include_by_default: true` for the
  provider and enable metered routing, such as `routing.allow_metered: true`,
  after accepting spend semantics

The dropped policy/alias names are: `fast`, `code-fast`, `code-economy`,
`code-smart`, `standard`, `local`, `offline`, `code-high`, and `code-medium`.
Compatibility errors should point to `--policy default`, `--policy smart`,
`--policy cheap`, or numeric power bounds as appropriate.

### ADR-012 Addendum: Harness-as-Provider Identity

ADR-012's assembled snapshot treats built-in harnesses as
harness-as-provider identity during candidate assembly. That lets `claude`,
`codex`, and `gemini` appear in the same provider/model snapshot that routing
consumes, while still keeping `Harness` as the explicit hard-pin axis and
`Provider` as the provider-system axis.

## Related

- `ADR-005` — superseded in part: power-routing mechanics remain useful, but
  profile/target framing is no longer the public v0.11 surface.
- `ADR-006` — superseded in part: override metrics remain useful, but
  `Profile`/`ModelRef` are no longer public intent fields.
- `ADR-007` — unchanged: sampling profiles are generation-parameter catalog
  policy, not routing policy.
- `CONTRACT-003`, `FEAT-003`, `FEAT-004`, `FEAT-005`, and `FEAT-006` describe
  the updated service, provider, routing, logging, and CLI contracts.
