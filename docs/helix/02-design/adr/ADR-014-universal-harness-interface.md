---
ddx:
  id: ADR-014
  depends_on:
    - CONTRACT-003
    - CONTRACT-004
    - ADR-002
    - ADR-011
    - ADR-012
  child_of: fizeau-67f2d585
---
# ADR-014: Universal Harness Interface

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-14 | Accepted | Fizeau maintainers | CONTRACT-004, ADR-011, ADR-012, ADR-013 (withdrawn) | High |

## Context

The internal harness contract today is the 3-method `harnesses.Harness`
interface (`Info`, `HealthCheck`, `Execute`) defined in
`internal/harnesses/types.go`. In practice, the service layer depends on
roughly 80 additional exported symbols across the per-harness packages —
`ClaudeQuotaSnapshot` field reads, `ReadClaudeQuota`, `WriteClaudeQuota`,
`DecideClaudeQuotaRouting`, `ClaudeQuotaCachePath`, `CodexAuthPath`,
`ReadAuthEvidence`, `ResolveClaudeFamilyAlias`, `DefaultCodexModelDiscovery`,
and so on. A 2026-05-14 inventory counted **69 external call sites** across
`service.go`, `service_providers.go`, `service_models.go`,
`service_subscription_quota.go`, `internal/serviceimpl/execute_dispatch.go`,
and `internal/runtimesignals/collect.go` reaching into per-harness packages
beyond the documented interface.

The pattern is uniform but the types are duplicated. Each of `claude`,
`codex`, and `gemini` exports `<Harness>QuotaSnapshot`,
`<Harness>QuotaRoutingDecision`, `<Harness>QuotaCachePath`,
`Read<Harness>Quota`, `Write<Harness>Quota`, `Default<Harness>QuotaStaleAfter`,
`Decide<Harness>QuotaRouting`, `Default<Harness>ModelDiscovery`, and
`Resolve<Harness>Alias` (or close variants) — same concepts, different
types, no shared seam.

Consequences:

1. **Interface drift.** The "Harness interface" claim in the PRD and in
   ADR-013 is structurally misleading; the real contract is the union of
   the documented interface and the per-harness exports the service
   imports by name.
2. **Per-harness sprawl.** Adding a new harness (e.g. the `claude-tui`
   fork in ADR-013) requires either duplicating the per-harness symbols
   under a new prefix or wiring service code to a fifth set of imports.
3. **Tests pin to concrete types.** Service-level tests construct
   `claudeharness.ClaudeQuotaSnapshot{...}` literals and assert against
   field-level values; the test suite cannot validate the interface,
   only specific implementations.
4. **Routing logic leaks across the boundary.** `DecideClaudeQuotaRouting`
   lives in the claude package but encodes routing-side preferences
   (`PreferClaude` boolean, freshness threshold semantics) the service
   re-implements with subtle drift across codex and gemini.

ADR-002 already establishes the PTY transport boundary inside
`internal/pty/`. ADR-012 establishes per-source on-disk cache semantics.
ADR-011 treats quota as a routing-pool input. None of those ADRs specify
that the **interface** to a harness is the only legitimate consumption
point, which is what allowed the per-harness exports to grow.

## Decision

Fizeau defines a universal harness implementation contract
(CONTRACT-004) consisting of four interfaces, composable via Go interface
assertions:

- **`Harness`** — required by every implementation. Same three methods
  as today.
- **`QuotaHarness`** — implemented by harnesses with a subscription or
  quota window. Surfaces `QuotaStatus`, `RefreshQuota`, and
  `QuotaFreshness`.
- **`AccountHarness`** — implemented by harnesses that expose auth or
  account evidence independent of quota.
- **`ModelDiscoveryHarness`** — implemented by harnesses that resolve
  family-style model aliases or seed a discovery snapshot.

The service layer consumes only these interfaces. The per-harness public
exports that today provide cache I/O, routing decisions, and concrete
snapshot types are removed or made package-private. The per-harness
concrete snapshot types (`ClaudeQuotaSnapshot`, `CodexQuotaSnapshot`,
`GeminiQuotaSnapshot`) become package-private; they remain available
inside the harness package for cache I/O but are no longer part of the
service-visible surface.

A linter (or `go vet`-shaped check) blocks any new `.go` file outside
`internal/harnesses/` from importing per-harness package symbols beyond
the runner constructor used by `internal/serviceimpl/execute_dispatch.go`.

**Key Points**: four composable interfaces | service consumes only the
interfaces | per-harness snapshot types and cache helpers become
package-private | enforced by a lint rule, not just convention.

## Why Four Interfaces

The natural unit of variation across the existing five harnesses is
*capability presence*, not *capability shape*:

| Capability | claude | codex | gemini | opencode | pi |
|------------|--------|-------|--------|----------|-----|
| Quota window | yes | yes | yes | no | no |
| Independent auth refresh | no (embedded) | no (embedded) | yes (7-day window) | no | no |
| Model alias resolution | yes | yes | yes | no (catalog) | no (catalog) |

A single fat interface forces opencode and pi to return sentinel "not
applicable" responses for quota and account. A two-interface split
(`Harness` + `OptionalEverything`) collapses quota and account into a
single call shape that fits the embedded case (claude, codex) but
contorts the independent-auth case (gemini).

The four-interface split lets each harness implement only what it has.
The service uses interface assertions (`if qh, ok := h.(QuotaHarness);
ok { ... }`) and never has to interpret "not applicable" sentinels.

## Why Package-Private Concrete Snapshots

Three alternatives were considered:

| Option | Verdict |
|--------|---------|
| **Make concrete snapshot types package-private (selected)** | Strongest signal that the interface is the contract. Forces every consumer through `QuotaStatus`. Breaks any external tool that read the type — none exist today. |
| Keep exported, documented as internal-only | Convention-only boundary; the existing leak demonstrates conventions do not hold. |
| Move to a neutral shared package | Useful for sharing within the Anthropic family (claude + future claude-tui) but does not close the service-side leak. Can still be done *inside* the harness package boundary once snapshots are private. |

The selected option is the only one that actually closes the leak.
Shared-within-family use (e.g. claude and a future claude-tui sharing a
single snapshot definition) is achieved by lifting the type into a shared
internal subpackage (e.g. `internal/harnesses/anthropic/`) whose symbols
remain unexported to consumers outside `internal/harnesses/`.

## Why `RoutingPreference` Inside `QuotaStatus`

Today, each harness exports a `<Harness>QuotaRoutingDecision` struct with
`PreferClaude` / `PreferCodex` / `PreferGemini` boolean fields. The service
constructs these by calling per-harness `Decide<Harness>QuotaRouting`
functions and reading the boolean back.

CONTRACT-004 collapses this into `QuotaStatus.RoutingPreference`, a
three-valued enum (`Unknown`, `Available`, `Blocked`) attached to the
quota status itself. The harness owns the rule that maps its windows,
freshness, and account state into this preference; the service consumes
only the enum.

`Blocked` is distinct from `State=QuotaUnavailable` because a harness
may have fresh evidence that it is over quota (state `ok`, but explicitly
exhausted by the harness's policy) and a harness may be unavailable for
reasons that do not imply quota exhaustion. The two signals serve
different consumers (routing vs. operator surfaces).

## Scope of the Refactor

In scope (this ADR + the plan):

- New interface and type declarations in `internal/harnesses/types.go`.
- Implementation of the new interfaces on each existing harness.
- Migration of every service-side call site to consume the interfaces.
- Removal of per-harness `Read*Quota`, `Write*Quota`, `*QuotaCachePath`,
  `Default*QuotaStaleAfter`, `Decide*QuotaRouting`, `Read*QuotaRoutingDecision`,
  `*QuotaSnapshot`, `*QuotaRoutingDecision`, `Default*ModelDiscovery`,
  `Resolve*ModelAlias`, and account-access exports from the public
  surface.
- Lowercase rename (export → unexport) of the concrete snapshot types.
- Lint rule enforcement.
- Conformance tests on every harness.
- Service-level JSON shape preservation tests for CONTRACT-003 fields.

Out of scope:

- Changing CONTRACT-003 public types.
- Restructuring `HarnessConfig` registry struct (it remains
  subprocess-config metadata).
- Introducing the `claude-tui` fork (ADR-013 is withdrawn pending this
  refactor; re-proposed after).
- Out-of-tree harness plugin loading.

## Alternatives

| Option | Pros | Cons | Evaluation |
|--------|------|------|------------|
| **Universal interface refactor (selected)** | Closes 69 leak sites; one contract; lint-enforceable; unblocks claude-tui cleanly; tests target the interface, not implementations. | Substantial refactor across ~12 service files and all five harness packages. | **Selected** because the alternative is permanent sprawl. |
| Per-harness sprawl, document the leak | No code change; ADR-013 (claude-tui) ships sooner. | Codifies the very pattern this ADR identifies; each new harness adds another 25-symbol surface; tests cannot validate "interface compatibility" because there is no interface. | Rejected. |
| Single fat `Harness` interface with optional sentinel returns | Fewer interfaces. | Opencode/pi must return `ErrNotApplicable` on quota/account; service must check sentinels everywhere. Gemini's independent-auth case is awkward. | Rejected — sentinel checks are conditional branching by another name. |
| Build a plugin system / out-of-tree harness loader | Forces a strict ABI. | Massive scope; no current need; harnesses remain in-tree under `internal/`. | Rejected as out of scope. |
| Make the snapshot types exported interfaces instead of structs | Closes the field-read leak. | Routing-side scoring still needs to assert into a concrete type or proliferate accessor methods; doesn't simplify the contract. | Rejected — the concrete snapshot is a harness-internal cache shape, not a contract. |

## Consequences

| Type | Impact |
|------|--------|
| Positive | One contract, lint-enforced. Adding a new harness or forking an existing one (e.g. claude-tui) becomes additive: implement the interfaces, register the runner. No service-side changes required. |
| Positive | Service code drops ~12 per-harness imports across ~6 files; routing logic centralizes around `RoutingPreference`. |
| Positive | Tests can target the interface with shared assertion helpers, reducing test duplication. |
| Positive | CONTRACT-003 public JSON shapes stay stable while their internal source unifies. |
| Negative | Substantial one-time refactor. The companion plan sequences this as 12 numbered steps, with Steps 5–7 (the per-harness migrations for claude, codex, gemini) each landing as 4–6 sub-PRs to keep diffs reviewable. Total: ~20–25 PRs touching ~40 files. Rough wall-clock: 6–10 weeks for one engineer serialized; less with parallelism across the per-harness migrations after the contract scaffolding (Steps 0–4) lands. |
| Negative | The per-harness cache I/O helper symbols disappear from the public surface; any external tooling relying on them (none known today) breaks. |
| Negative | Tests that today construct `claudeharness.ClaudeQuotaSnapshot{...}` literals must be rewritten to seed fixtures through the harness's cache I/O path instead. |
| Neutral | The four-interface split is more interfaces than today's one, but each is small and each is independently testable. |

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| JSON shape regression in CONTRACT-003 output during projection refactor | M | H | Pin recorded fixtures of `HarnessInfo`, `ProviderInfo`, `QuotaState`, `AccountStatus` JSON before the refactor; assert byte-equal post-refactor; refuse merge on diff. |
| Routing semantic regression (`PreferX` boolean → `RoutingPreference` enum) | M | H | Migrate one harness end-to-end first (claude), prove parity through `service_routing_test.go` fixtures, then proceed to codex and gemini. |
| Cache file schema drift during snapshot unexport | L | M | Cache files remain on disk in the harness's existing format; only the Go type name changes. Pre-existing cache files keep loading. |
| Lint rule produces false positives that block legitimate refactor PRs | M | L | Lint exemption file lists the runner constructors used by execute_dispatch.go and any test files that legitimately import for fixture seeding (record-mode tests). |
| The refactor stalls partway, leaving both the old per-harness surface and the new interface coexisting | M | M | Plan sequences harness-at-a-time migration with explicit "old surface deleted" acceptance criterion at each step; lint rule is added in the last step so partial migrations cannot accidentally satisfy it. |
| ADR-013 (claude-tui) re-proposal arrives during the refactor and gets implemented against the partial contract | L | M | ADR-013 is explicitly withdrawn (not "paused"); re-proposal requires citing the merged CONTRACT-004 and reaching agreement that the contract is stable enough. |

## Validation

| Success Metric | Review Trigger |
|----------------|----------------|
| `internal/harnesses/types.go` declares `QuotaHarness`, `AccountHarness`, `ModelDiscoveryHarness` with the CONTRACT-004 signatures | An ADR amendment changes signatures without updating CONTRACT-004 |
| Every existing harness compiles with the new interfaces; conformance tests pass | A harness ships with a missing interface implementation it should have |
| `go vet` (or the project's lint pass) reports zero external imports of per-harness symbols beyond the documented runner constructor seam | New external import lands without an ADR amendment |
| Pre/post refactor JSON fixtures for `HarnessInfo`, `ProviderInfo`, `QuotaState`, `AccountStatus` are byte-equal | A diff appears in any of those fixtures |
| Per-harness `*QuotaSnapshot` types are lowercase | A public uppercase snapshot type re-appears |
| `service_subscription_quota.go`, `service_providers.go`, `service.go`, `service_models.go`, `internal/runtimesignals/collect.go` import only `internal/harnesses` (the parent package), not any `internal/harnesses/<name>` | A `<name>harness` import re-appears outside the dispatcher |
| `claude-tui` re-proposal cites CONTRACT-004 AND empirical evidence that PTY-driven Claude lands on subscription quota while `claude --print` lands on per-token API pricing | A re-proposal lands without citing the contract, or without the billing-observation evidence |

## Concern Impact

- **Resolves harness encapsulation gap**: Defines the actual contract,
  removes per-harness sprawl, and enforces the boundary by lint rather
  than convention.
- **Supports CONTRACT-003**: Public JSON shapes remain stable; only the
  internal source changes.
- **Unblocks ADR-013 (claude-tui) cleanly**: A future claude-tui
  implementation satisfies `Harness` + `QuotaHarness` + `AccountHarness`
  + `ModelDiscoveryHarness` and is drop-in routable. Today's leak
  pattern would force claude-tui to duplicate 24+ exports under a new
  prefix. **Re-proposal of ADR-013 still requires empirical evidence
  that PTY-driven Claude actually lands on subscription quota while
  `claude --print` lands on per-token API pricing — the original
  premise was asserted, not verified. The refactor merely removes the
  structural obstacle; it does not validate the billing assumption.**
- **Supports ADR-011 cost-based routing**: `RoutingPreference` is the
  routing signal; the routing layer consumes it uniformly across
  subscription harnesses.

## References

- [CONTRACT-003 Fizeau Service Interface](../contracts/CONTRACT-003-fizeau-service.md)
- [CONTRACT-004 Harness Implementation Contract](../contracts/CONTRACT-004-harness-implementation.md)
- [ADR-002 PTY Cassette Transport](./ADR-002-pty-cassette-transport.md)
- [ADR-011 Cost-Based Routing With Quota Pools](./ADR-011-cost-based-routing-with-quota-pools.md)
- [ADR-012 Per-Source On-Disk Cache](./ADR-012-per-source-on-disk-cache.md)
- [ADR-013 claude-tui PTY harness fork (withdrawn pending this refactor)](./ADR-013-claude-tui-pty-harness-fork.md)
- [Implementation plan: harness interface refactor](../plan-2026-05-14-harness-interface-refactor.md)
- `internal/harnesses/types.go` — current `Harness` interface
- `internal/harnesses/registry.go` — current `HarnessConfig`
- 2026-05-14 leak inventory (in conversation history) — 69 external call sites identified

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
