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
  review:
    self_hash: 5bf0e5c1f0fe37d8845804377086bb23d7a2287fe9fa9dffd3de29da56fb8ef6
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      ADR-011: 088af56c3f51ae0ba0bb0d71940195af827b2ec5b73768e11fd0d7427070f8d2
      ADR-012: 5c24642fbb06edd9f8fede71adc0a1a4375c2e17a95f7c61b1add3f24a5f622a
      CONTRACT-003: 00832f8e545c23177a039758eaf8dd9fd8a07f2e54d5293d63de8c275acfa0c5
      CONTRACT-004: 3b5c5a15a83d6f5fa145e645162a72fbd1805262d4362b38b6001b1504f2e7c5
    reviewed_at: "2026-07-16T04:37:11Z"
---
# ADR-014: Universal Harness Interface

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-14 | Accepted | Fizeau maintainers | CONTRACT-004, ADR-011, ADR-012, ADR-013 (withdrawn) | High |

> **Implementation reference (2026-05-14):** The context, inventory counts,
> and “today” statements below describe the pre-CONTRACT-004 implementation
> that motivated this decision. They are retained as decision history, not as
> claims about the current registry or import graph. The dated amendments after
> the original review checklist state the active `claude-tui`, interface, and
> lifecycle design.

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

## Amendment — 2026-07-14: ADR-013 Prerequisite Complete

ADR-014 and CONTRACT-004 are implemented and accepted. The prerequisite that
originally caused ADR-013 to be withdrawn is therefore complete. ADR-013 was
re-proposed after CONTRACT-004 merged, accepted on 2026-05-18 with empirical
billing evidence, and received its Gate-E acceptance on 2026-06-04.

References in the original ADR-014 decision record to ADR-013 as "withdrawn",
"future", or awaiting this refactor are retained only as historical context.
They are not current status or a remaining gate. The current relationship is:
ADR-014 supplies the universal harness boundary and accepted ADR-013 consumes
that boundary for the `claude-tui` implementation.

## Amendment — 2026-07-14: Universal Live-Process Lifecycle Ownership

### Authority and historical scope

CONTRACT-003 v0.15 is authoritative for accepted-session lifecycle, typed
terminal facts, cleanup precedence, caller death, and recovery. This amendment
extends ADR-014's universal harness boundary so every live subprocess-capable
harness path can satisfy that contract.

The original 2026-05-14 inventory, its references to five harnesses, and its
description of ADR-013 or `claude-tui` as withdrawn or future remain historical
implementation reference. The current built-in subprocess harness registry has
six identities: `claude`, `claude-tui`, `codex`, `gemini`, `opencode`, and
`pi`. This amendment does not erase the earlier context or reconsider the
accepted `claude-tui` identity.

Where ADR-013's earlier design-direction notes describe package- or
service-scoped warm live-session pools, `/clear` reuse across Execute calls, or
live-process recovery based only on parent/PID inspection, those notes are
superseded by this amendment. They remain useful history, not current design.
ADR-013 continues to govern the separate `claude-tui` identity and its TUI
transport; CONTRACT-003, CONTRACT-004, and this amendment govern its process
lifecycle.

### Decision

Fizeau will use one neutral internal owner, `internal/processlifecycle`, for
every production path that can start a live child process. Harness packages
continue to own argv, environment, protocol parsing, and harness-native result
data. They do not independently own containment, cleanup contexts, stale
recovery identity, or public terminalization.

The lifecycle owner is mandatory for normal `Harness.Execute` calls, PTY
sessions, quota/account/model-discovery probes, health or auxiliary commands,
and adapter helpers. Each invocation or live probe receives its own lease and
containment boundary. Immutable configuration and durable cache evidence may be
shared. Live subprocesses, PTYs, supervisors, and containment leases may not be
pooled or reused across invocations, including multiple invocations in the same
Fizeau process.

On Unix-like systems, the lifecycle owner starts a lifecycle supervisor as
Fizeau's direct child. The supervisor leads a dedicated process group or
session and starts the harness as a contained descendant only after a
service-owned control channel and the strongest available parent-death
mechanism are active. Control-channel close or EOF begins group/session
shutdown; cleanup escalates and reaps until the boundary is empty or the
cleanup deadline expires.

On Windows, the lifecycle owner creates the child suspended, assigns it to a
dedicated kill-on-job-close Job Object, and resumes the primary thread only
after assignment succeeds. Assignment failure terminates and reaps the
suspended child. A platform without an equivalent containment implementation
reports live harness execution unsupported before creating a child.

The service owns the one public terminal fact. A harness implementation final
is primary execution evidence only. The service withholds caller-alive terminal
delivery until containment cleanup succeeds or
`ServiceOptions.HarnessCleanupTimeout` expires, then applies CONTRACT-003's
cleanup-failure precedence. Request, idle, provider, tool, and probe timeouts
may initiate cleanup; none shortens or replaces the service-owned cleanup
deadline. Request-context cancellation likewise initiates cleanup but does not
cancel its service-owned context.

Lifecycle recovery records identify a process by containment identity plus an
OS-derived process-birth/start token, not by PID, process name, or command line
alone. Recovery proves that identity before signalling. A PID-reuse mismatch is
retained as unresolved evidence and is not killed as if it were the recorded
process.

`ServiceOptions.HarnessCleanupTimeout` bounds current-invocation cleanup and
caller-visible terminal waiting. `ServiceOptions.StaleHarnessReaperGrace`
controls only when a later startup may adopt an old non-terminal lifecycle
record. It never postpones cleanup for the invocation that created the record
and never authorizes deletion of unresolved ownership evidence.

The model-discovery interface description is also corrected to match the
universal contract:

- `ModelDiscoveryHarness.DefaultModelSnapshot()` returns
  `(ModelDiscoverySnapshot, error)` and reports missing evidence rather than a
  fabricated static success snapshot.
- `ContextModelDiscoveryHarness` embeds `ModelDiscoveryHarness` and adds
  `DefaultModelSnapshotWithContext(context.Context) (ModelDiscoverySnapshot,
  error)` for cancellation-aware live probes. Service refreshers prefer this
  extension when present.

### Alternatives considered

| Option | Evaluation |
|--------|------------|
| Harness-owned cleanup implementations | Rejected: repeats the per-harness contract drift ADR-014 exists to remove and cannot provide one caller-death or recovery guarantee. |
| Cross-invocation warm live-session pools | Rejected: a final event would no longer delimit ownership, cleanup, or continuation; caller death could strand shared state whose owner cannot be attributed to one invocation. |
| Request-context cleanup | Rejected: cancellation would cancel the only context able to reap the process tree. |
| PID/name-based startup reaping | Rejected: PID reuse can target an unrelated process and process-name matching is not identity. |
| Neutral per-invocation lifecycle leases (selected) | Selected: one containment and recovery contract covers all six harnesses and all auxiliary live probes while preserving harness protocol independence. |

### Consequences

| Type | Impact |
|------|--------|
| Positive | Unix, Windows, PTY, batch, and auxiliary probe paths share one containment and caller-death contract. |
| Positive | Exactly-one terminalization remains service-owned and can deterministically apply cleanup-failure precedence. |
| Positive | Process-birth identity prevents stale recovery from signalling a reused PID. |
| Negative | `claude-tui` and any future interactive harness pay per-invocation process startup cost; provider-native continuation may preserve conversation state, but a live TUI process is not the continuation boundary. |
| Negative | Platform launch code must establish containment before execution, including suspended-process setup on Windows. |
| Neutral | ADR-014's interface encapsulation decision is unchanged; lifecycle ownership is an additional universal seam. |

### Validation additions

- Static enforcement rejects production child starts that bypass
  `internal/processlifecycle`.
- Unix subprocess tests prove control-channel/parent-death cleanup for a child
  and grandchild. Windows tests prove Job Object assignment occurs before
  resume and kill-on-close reaches descendants.
- Unsupported-platform tests prove failure occurs before spawn.
- Normal completion, harness failure, timeout, cancellation,
  caller-signalled abandonment, and caller death all exercise containment
  cleanup. Caller-alive terminal delivery occurs only after cleanup success or
  `HarnessCleanupTimeout`.
- A separate-process caller-death fixture proves the supervisor reacts when the
  caller cannot emit a final event.
- PID-reuse fixtures prove recovery checks process-birth identity before
  signalling.
- Structural tests reject cross-invocation live pools and verify the six
  built-in subprocess harnesses use the corrected discovery signatures and
  CONTRACT-004 lifecycle seam.

## Amendment — 2026-07-14: Optional Route-Owned Continuation Capability

ADR-014's small-interface rule extends to conversation continuation through
CONTRACT-004's optional `ContinuationHarness`. The base `Harness` interface
does not gain a sentinel continuation method. The service asserts the optional
interface on the registered runner instance that owns the completed parent's
actual route. Harness-name checks, capability inference from static registry
metadata, and construction of a replacement runner for continuation are not
valid substitutes for that assertion.

The optional capability in this contract version is harness-backed only.
Native-provider routes do not implement `ContinuationHarness`, are reported as
unsupported for resume, and must not be wrapped or forced through `Harness` to
obtain the capability. A future provider-native continuation interface is a
separate contract decision. This boundary does not prevent a fresh-policy
child from selecting a native provider through ordinary `Execute` routing.

The service owns the public lineage and routing facts: locating a completed
parent Fizeau session, including one written to a per-request session-log
directory; reading its actual selected route; creating the child Fizeau
session; and recording parent session ID plus continuation disposition. The
route-owned runner owns harness-native continuation evidence and its encoding.
Native conversation IDs, resume tokens, and equivalent derived evidence never
become public fields or values and are never serialized into public JSON,
service events, terminal projections, metadata, or session-log JSONL. They may
be persisted only as opaque data in the owning route's private durable evidence
store; the service passes no native identifier and does not interpret the
stored bytes.

Runner-instance ownership is stable across process lifetime, while evidence
ownership is stable across service restarts. In a running service, continuation
uses the actual route-owned registered runner instance. After restart, the
service reconstructs the parent's actual route and the registry binds that
route to the same private evidence store. A runner for a different route,
another instance that merely shares a harness name, or a runner bound to a
different store has no continuation authority over that parent. The canonical
route key includes the normalized endpoint in addition to harness, provider,
model, and other routing discriminators. Endpoint-distinct routes never share
authority or evidence by display-name coincidence.

`ContinuationHarness` is a two-phase capability. Prepare receives the parent
Fizeau session reference, resolves and validates durable private evidence, and
returns a route-bound prepared continuation. Prepare must not create a child
session, event stream, lifecycle lease, containment boundary, or process. A
prepare-time unavailable result is the only resume failure that
`prefer_resume` may convert into a fresh fallback; `require_resume` returns
unsupported without creating a child.

After successful prepare, the service creates the child Fizeau session and
acquires its fresh lifecycle lease before calling the prepared continuation's
`Start` operation. `Start` may spawn only inside that lease's containment. A
native rejection, evidence race, or other error after prepare is a failure of
the already-created resumed child. The service emits its one terminal fact and
must not reinterpret the disposition or start a fresh fallback.

Private continuation evidence must be durable; an in-memory-only map or runner
field cannot satisfy this decision. The service writes a durable pending
completed-session locator before parent execution. A supporting runner commits
its private evidence atomically under parent Fizeau session ID plus canonical
route key before acknowledging capture; the service promotes the locator only
after the terminal log and actual route are durable. Pending-locator recovery
validates the exact log and route and asks the reconstructed canonical runner
to reopen the same store. Evidence committed before a crash may be recovered
or retained as a private orphan; evidence not committed before a crash is
unavailable. Recovery never scrapes a native ID from public logs or process
memory.

Continuation preserves logical conversation state, not live execution state.
Each accepted resumed child, `prefer_resume` fresh fallback, and
`fresh_session` child receives a new Fizeau session ID and a fresh lifecycle
lease. A spawning route also receives a fresh `internal/processlifecycle`
containment boundary. No child may reuse a parent process, PTY, supervisor,
process group, Job Object, or lease.

Opacity follows provenance. Service- or harness-derived native evidence must
not escape the private store. The service does not redact coincidentally equal
bytes that the caller deliberately supplied in prompts or metadata; those
remain caller data governed by the normal public contract.

### Continuation conformance additions

- Optional-interface tests prove a runner may implement `Harness` without
  continuation, while a supporting route is recognized only by asserting
  `ContinuationHarness` on the parent's actual registered runner instance.
  Native-provider parents are unsupported, and no adapter or forced harness
  dispatch is attempted.
- Route-identity tests use multiple runner instances with the same harness
  name but distinct endpoint-bearing route keys and private stores. Only the
  instance recorded on the completed parent is called; no name-based fallback
  or replacement construction occurs.
- Restart tests resolve a completed parent from both the default and a
  per-request session-log directory, crash before and after private evidence
  commit and locator promotion, rebuild the registry, and prove recovery uses
  the same durable store rather than process memory.
- Two-phase tests prove prepare creates no child, lease, containment, stream,
  or process; service child/lease creation precedes `Start`; and a
  post-prepare failure creates one failed resumed child with no fresh fallback.
- Opaque-boundary tests tag harness-derived IDs and tokens and prove they are
  absent from every public Go shape and serialized service projection, while
  an equal caller-supplied metadata value is preserved.
- Lifecycle tests issue resumed children serially and concurrently and prove
  every child has a distinct Fizeau session, lifecycle lease, and containment
  identity. Fresh fallbacks and `fresh_session` children also receive new
  leases. No child inherits a live process or PTY, and cleanup failure on one
  child cannot transfer ownership to another invocation.

## Amendment — 2026-07-15: Optional Portable Runtime Asset Capability

ADR-014's small-interface rule also applies when an embedding caller prepares
Fizeau for Linux same-platform isolated execution. The base `Harness` interface does
not gain binary, credential, cache-path, or container methods.
`PortableRuntimeHarness` is an optional CONTRACT-004 capability asserted on the
registered runner. It returns an API-neutral, content-addressed
executable/install-tree closure, harness-owned state-file descriptors, and
inherited environment names for a target GOOS/GOARCH. It also declares typed,
value-opaque execution constraints: fixed boolean or guest-path environment
treatments, explicit unsets, immutable config-tree and required-absent paths,
and an ordered fixed launch prefix made from standalone boolean flags followed
by typed non-secret option/value pairs. It returns no raw environment value and
performs no copy, route selection, provider contact, session creation, or
process start.

Mixed native state remains inside this optional neutral capability. A
`StateProjection` maps exact declared config plus credential/quota/cache asset
targets into one activation-owned home/config/data/cache/state directory. An
unreferenced credential/quota/cache asset keeps the prefix-preserving
data/state/cache seed behavior; a projection-consumed asset is assembled only
through its projection. Normalization is metadata-only, and contributor plus
materializer layers retain source identity, content, and symlink checks.

This capability closes a boundary that a service-side path table would reopen.
Codex, Claude, Gemini, and other harness packages already own their credential,
quota, cache, and launcher semantics. The service therefore cannot copy those
rules into another switch or call concrete exported helpers. A future harness
becomes portable by implementing the optional interface and satisfying the
registry conformance test, just as it adds quota, account, discovery, or
continuation support through the corresponding capability.

The current v0.15 target is intentionally narrow: preparation runs on Linux and
packages only for the preparing process's Linux GOARCH. Darwin, Windows, and
cross-target preparation are later decisions. A PATH result, resolved symlink,
or platform label is not enough. Each contributor classifies one static,
dynamic, or interpreted entrypoint, content-addresses the complete
loader/interpreter, exact recursive library files, required install-tree, and
runtime-support closure without copying unrelated host library directories, supplies
a typed guest-relative launch recipe that bypasses copied `PT_INTERP`/shebang
paths, and passes its offline same-target conformance probe. An installed non-test subprocess instance that
is structurally capable of unpinned routing cannot be silently omitted because
its layout is difficult. The authoritative decision joins the production
registry to the actual registered instance so native/HTTP-backed transports are
not mislabeled from a static row. Test-only and exact-pin-only surfaces do not
become unpinned-capable through preparation.

An interpreted contribution selects its interpreter only through the explicit
source supplied by the harness package. The contributor supplies the expected
file size and SHA-256 from its reviewed release evidence; the neutral analyzer
resolves the source, retains one opened ELF descriptor through dependency
inspection, hashes that descriptor, and rejects path replacement or identity
drift. `PATH`, absolute or env shebang text, and a locally computed
self-attestation cannot choose or authorize the interpreter. Publisher,
version, build, package-integrity, and offline-probe evidence remain owned by
the contributing harness.

For a recognized single-file dynamic runtime that imports generic loader symbols,
the owning contributor may declare verified-exact lookup only after its
credential-free, network-disabled probe demonstrates that startup opens no
runtime code outside the exact recursive library assets. That contributor must
reject enabled plugins, hooks, helpers, wrappers, MCP servers, marketplaces, and
external-path settings. This keeps exact closure small without treating an
arbitrary `dlopen`-capable executable as closed; absent that evidence, the
runtime must contribute the exercised lookup tree or fail incomplete.

Claude binds that evidence to the publisher-signed manifest row for the exact
`$HOME/.local/share/claude/versions/<x.y.z>` Linux binary: version, GOARCH,
size, and SHA-256 all match an embedded reviewed record. Discovery stays
offline and rejects a newer digest until its exact binary passes the isolated
root/network-namespace probe. The probe has a missing-library negative case,
and configuration inspection rejects installed plugin state even when no
`enabledPlugins` map is present. Claude-TUI uses one classifier for its live
PTY and portable environment policy: `CLAUDE_*` and locale/terminal names are
inherited, `ANTHROPIC_*` is excluded, and host/platform path identities are
regenerated during activation.

The first Claude registry entry is 2.1.210 Linux arm64, which has durable local
same-target probe evidence. A publisher-authenticated amd64 checksum alone does
not claim verified-exact conformance; amd64 remains incomplete until its exact
artifact passes the same gate.

Configured native and HTTP provider instances remain service configuration,
not harness capabilities. A complete route-neutral inventory combines
CONTRACT-004 contributions with a field-exhaustive API-neutral projection of
effective `ServiceConfig`. The neutral materializer owns restrictive copying,
generated config, deterministic deduplication/conflict handling, staging,
rollback, diagnostics redaction, private persistence of the normalized
constraints, and cleanup. The public root facade maps that result to
CONTRACT-003's opaque generic bundle: one atomically committed host child, one
fixed guest-root read-only mount, and inherited environment names without
values. The typed constraints remain private manifest state rather than a new
caller-interpreted plan surface. `NewFromPortableRuntime` is the public
activation seam that verifies the mounted manifest, reconstructs a closed-world
child environment, enforces read-only/absent path rules, and reconstructs the
configured service in the new process without the application-only config
loader. It also copies unprojected target-prefixed credential, quota, and cache
state seeds into owner-only writable generated data/state/cache scopes and
assembles projection-consumed mixed directories generically. For a mixed
directory, activation makes the directory root identity and config members
mount/filesystem-owned boundaries. The root identity denies unlink, rename,
replacement, and shadowing; config members additionally deny writes. Writable
member refresh, locks, and generated siblings remain permitted. Chmod on
ordinary files inside a writable parent is not sufficient. The read-only mount
remains the source of truth.
Activation installs the typed launch recipes and ordered fixed flag plus
option/value prefixes into the production `Execute` dispatcher, not only a
refresh/scheduler instance map. The embedding caller applies the opaque plan,
orchestrates the external container as the mapped preparing UID, destroys its
writable storage, and only then performs bundle cleanup; it never resolves a
harness path, execution constraint, or provider field.

This opacity is a programming boundary, not a confidentiality boundary against
the embedding caller. The caller owns the source environment and destination
directory and can read both. The design prevents caller code from needing to
interpret or serialize concrete semantics and prevents accidental exposure in
plans/diagnostics; it does not claim owner-only modes hide bytes from their
owner.

### Alternatives considered

| Option | Evaluation |
|--------|------------|
| Service-owned per-harness path switch | Rejected: recreates the concrete symbol/path leak ADR-014 exists to prevent and drifts when harnesses change. |
| Add asset methods to base `Harness` | Rejected: HTTP, embedded, pinned-only, and test-only surfaces would return sentinel values on every call. |
| Copy only the resolved PATH entry | Rejected: symlinked Node launchers, install trees, interpreters, and dynamic dependencies would produce an incomplete bundle. |
| Return files without an in-runtime activation seam | Rejected: the public `ServiceConfig` is an in-memory interface, and external consumers cannot import the application-only config loader to reconstruct the prepared service. |
| Optional harness-owned contribution plus exhaustive registry classification | Selected: ownership stays with the adapter while the service consumes one neutral route-runtime inventory. |

### Validation additions

- `TestPortableRuntimeInventoryCoversEveryEligibleRegisteredHarness` fails for
  an incomplete registry/actual-instance join, an unclassified transport, a
  structurally included subprocess runner without the optional capability,
  target mismatch, unknown layout, or incomplete content-addressed closure.
- `TestPortableRuntimeInventoryContainsNoEnvironmentValues` proves only names
  cross the interface and that typed errors contain no credential value or
  account-bearing source path.
- Provider-field parity fixtures prove the combined inventory preserves every
  execution-relevant configured-provider fact without selecting a route.
- Public activation fixtures reconstruct the service in a new process from the
  fixed read-only guest mount, reject host config overrides, and compare
  structural candidate identities rather than transient health or reachability.
- `TestPortableRuntimeActivationFeedsProductionDispatch` makes each closure
  class the sole unpinned candidate in turn and proves `Execute` consumes the
  activated launch recipe rather than constructing an unconfigured fresh runner.
- Linux OCI fixtures execute static-symlink, dynamic-ELF, and
  interpreter/package-tree closures without credentials or network as a mapped
  non-root UID.
- `TestPortableRuntimeNodeInterpreterBypassesShebangAndPATH`,
  `TestPortableRuntimeNodeInterpreterIdentity`, and
  `TestPortableRuntimeNodeInterpreterRejectsRPATH` prove explicit interpreter
  selection, descriptor-bound size/digest verification, replacement and
  redaction failures, and the unchanged absolute `DT_RPATH` prohibition.
- Claude contributor fixtures reject arbitrary dynamic ELF files, exercise
  verified-exact lookup with `dlopen`/`dlsym` imports through the emitted loader
  recipe inside a network-disabled isolated root, reject an unknown release
  digest and a closure missing one required library, reject external
  configuration/code-loading surfaces, prove shared
  Claude/Claude-TUI assets deduplicate, and prove the TUI environment projection
  matches its real `CLAUDE_*` boundary.
- OpenCode fixtures bind the exact reviewed 1.14.33 Linux arm64 payload across
  direct and npm layouts, run the emitted loader recipe in an isolated writable
  generated-state environment, and fail when a required library is removed.
  They reject legacy or executable configuration, remote `wellknown` auth, and
  provider SDK selectors outside the audited bundled set; activation evidence
  must seed `data/opencode/auth.json` without making configuration writable.
- Mixed-state conformance uses `TestPortableRuntimeMixedStateProjection` for
  exact value-opaque mapping and activation tests that deny projected config
  write/unlink/rename/replacement/shadow operations while allowing credential
  refresh, lock creation, and sibling state creation.
- Static enforcement rejects service-side imports or calls to concrete
  harness path helpers for portable preparation.
