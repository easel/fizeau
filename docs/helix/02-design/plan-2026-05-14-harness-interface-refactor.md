# Implementation Plan: Universal Harness Interface Refactor

**Date**: 2026-05-14
**Status**: DRAFT
**Governs**: [ADR-014](./adr/ADR-014-universal-harness-interface.md), [CONTRACT-004](./contracts/CONTRACT-004-harness-implementation.md)
**Supersedes scope of**: ADR-013 (withdrawn pending this refactor)
**Rough size**: 12 numbered steps totaling ~20–25 PRs over 6–10 weeks for one engineer serialized (less with parallelism across the per-harness migrations after Step 4 lands); Steps 5/6/7 each land as 4–6 sub-PRs

## Problem Statement

The service depends on roughly 80 exported symbols across the per-harness
packages beyond the documented `Harness` interface — quota cache I/O,
routing-decision functions, concrete snapshot types with field-level
reads, family-alias resolvers, PTY probe entry points, and account-file
readers. A 2026-05-14 inventory counted **69 external call sites** across
six service files. The `Harness` interface alone does not capture the
contract; adding any new harness today requires either duplicating ~25
symbols under a new prefix or rewiring service code per-harness.

CONTRACT-004 defines the universal interface. This plan ports every
existing harness, every service consumer, every test, and the
public-facing CONTRACT-003 projection onto that contract, then enforces
the boundary with a lint rule.

## Requirements

### Functional

- Every existing harness implements the CONTRACT-004 interfaces it
  qualifies for: `Harness` (all), `QuotaHarness` (claude, codex,
  gemini), `AccountHarness` (gemini; optional for claude/codex),
  `ModelDiscoveryHarness` (all five).
- Service-side code reads quota, routing preference, account state,
  and model discovery exclusively through the interfaces. No file in
  `service*.go`, `internal/serviceimpl/`, `internal/runtimesignals/`,
  or `cmd/` imports per-harness package symbols beyond the runner
  constructor used by the dispatcher.
- Per-harness `*QuotaSnapshot`, `*QuotaRoutingDecision`,
  `*QuotaCachePath`, `Read*Quota*`, `Write*Quota*`,
  `Default*QuotaStaleAfter`, `Decide*QuotaRouting`,
  `Default*ModelDiscovery`, and `Resolve*Alias` symbols are removed
  from the public surface (deleted or unexported).
- Public CONTRACT-003 JSON shapes (`HarnessInfo`, `ProviderInfo`,
  `QuotaState`, `AccountStatus`) remain structurally identical (same
  field set, same value semantics) to pre-refactor recorded fixtures.
- The service has a single async refresh scheduler that consumes
  `QuotaHarness.QuotaFreshness()` and `AccountHarness.AccountFreshness()`
  to drive refreshes, replacing per-harness `Refresh*Async` functions.

### Non-Functional

- Refactor is staged one harness at a time so regressions surface
  early.
- The lint rule that enforces the boundary lands in the last step;
  partial migrations cannot accidentally satisfy it.
- No record-mode cassette is invalidated; cache file on-disk JSON
  shapes remain stable (cache file schemas use field-tagged JSON,
  not type-name encoding).
- Pre-existing test suite runtime does not regress by more than 10%
  (measured as `go test -count=1 ./... -timeout 30m` wall time before
  Step 0 and after the final step, excluding new test packages
  introduced by Steps 2 and 4). No individual pre-existing test slows
  by more than 10%. New conformance and scheduler tests are budgeted
  and reviewed separately in the Step 11 PR.

## Touch-Point Inventory

Every file that changes, with the symbols being migrated.

### Harness packages

| Package | Changes |
|---------|---------|
| `internal/harnesses/types.go` | Add `QuotaHarness`, `AccountHarness`, `ModelDiscoveryHarness` interfaces; add `QuotaStatus`, `AccountSnapshot`, `QuotaStateValue`, `RoutingPreference`, `ErrAliasNotResolvable` sentinel. Keep existing `Harness` interface and existing public types untouched. |
| `internal/harnesses/claude/quota_cache.go` | Lowercase `ClaudeQuotaSnapshot` → `claudeQuotaSnapshot`. Unexport `ReadClaudeQuota`, `ReadClaudeQuotaFrom`, `WriteClaudeQuota`, `ClaudeQuotaCachePath`, `IsClaudeQuotaFresh`, `ClaudeQuotaSnapshotAge`, `DefaultClaudeQuotaStaleAfter`. Delete `ClaudeQuotaRoutingDecision`, `DecideClaudeQuotaRouting`, `ReadClaudeQuotaRoutingDecision`. |
| `internal/harnesses/claude/runner.go` | Add `QuotaStatus`, `RefreshQuota`, `QuotaFreshness`, `AccountStatus`, `RefreshAccount`, `AccountFreshness`, `DefaultModelSnapshot`, `ResolveModelAlias` methods on `*Runner`. Internally call the now-unexported cache I/O. Acquire the harness's cache lock around refresh. |
| `internal/harnesses/claude/quota_pty.go` | Unexport `ReadClaudeQuotaViaPTY`, `RefreshClaudeQuotaViaPTY`, `ReadClaudeQuotaFromCassette`. Become internal helpers. |
| `internal/harnesses/claude/model_discovery.go` | Unexport `DefaultClaudeModelDiscovery`, `ResolveClaudeFamilyAlias`, `ReadClaudeReasoningFromHelp`. |
| `internal/harnesses/codex/quota_cache.go` | Mirror of claude: lowercase snapshot, unexport cache I/O, delete decision struct/func. |
| `internal/harnesses/codex/runner.go` | Add interface methods. Codex's session-token-count quota source folds into `RefreshQuota` as an internal fallback. |
| `internal/harnesses/codex/quota_pty.go` | Unexport. |
| `internal/harnesses/codex/session_token_count.go` | Unexport `ReadCodexQuotaFromSessionTokenCounts`, `CodexQuotaSnapshotFromTokenCountRateLimits`, `CodexSessionsRoot`. |
| `internal/harnesses/codex/account.go` | Unexport `ReadCodexAccount`, `ReadCodexAccountFrom`, `CodexAuthPath`. Used internally from `AccountStatus`. |
| `internal/harnesses/codex/model_discovery.go` | Unexport `DefaultCodexModelDiscovery`, `ResolveCodexModelAlias`. |
| `internal/harnesses/gemini/quota_cache.go` | Mirror. Tier-specific facts go into `QuotaStatus.Windows` (one per tier with `LimitID`); `Detail` carries only free-form diagnostic notes. |
| `internal/harnesses/gemini/runner.go` | Add interface methods. Gemini implements `AccountHarness` explicitly with `AccountFreshness()` returning 7 days. |
| `internal/harnesses/gemini/auth.go` | Unexport `ReadAuthEvidence`, `ReadAuthEvidenceFromDir`, `AuthSnapshot`, `GeminiAuthFreshnessWindow`. Used internally from `AccountStatus`. |
| `internal/harnesses/gemini/model_discovery.go` | Unexport `DefaultGeminiModelDiscovery`, `ResolveGeminiModelAlias`. |
| `internal/harnesses/opencode/runner.go` | Add `DefaultModelSnapshot`, `ResolveModelAlias` (returns `ErrAliasNotResolvable` for unknown families). Does not implement `QuotaHarness` or `AccountHarness`. |
| `internal/harnesses/opencode/model_discovery.go` | Unexport `DefaultOpenCodeModelDiscovery`. Decide fate of `OpenCodeModelEvidence`/`OpenCodeModelCost` in Step 8. |
| `internal/harnesses/pi/runner.go` | Add `DefaultModelSnapshot`, `ResolveModelAlias`. |
| `internal/harnesses/pi/model_discovery.go` | Unexport `DefaultPiModelDiscovery`. |

### Service-side consumers

The complete list of files that import per-harness packages today
and the symbols each uses.

| File | Current imports | Symbols used | Migration |
|------|-----------------|--------------|-----------|
| `service.go:12-14` | `claudeharness`, `codexharness`, `geminiharness` | `ReadClaudeQuota` (×2), `ClaudeQuotaCachePath` (×1), `DecideClaudeQuotaRouting` (×2), `ReadCodexQuota` (×2), `CodexQuotaCachePath` (×1), `IsCodexQuotaFresh` (×1), `DecideCodexQuotaRouting` (×1), `ReadGeminiQuota` (×1), `GeminiQuotaCachePath` (×1), `IsGeminiQuotaFresh` (×1), `ReadAuthEvidence` (×1) | Drop the three `*harness` imports. Replace each call site with `qh, ok := h.(harnesses.QuotaHarness); if ok { qs, _ := qh.QuotaStatus(ctx, now) }` etc., fetching `h` via the `harnessByName` helper introduced in Step 4. Both registry patterns (same-instance-per-call vs construct-fresh) work under CONTRACT-004 invariant #6. |
| `service_providers.go:22-23` | `claudeharness`, `codexharness` | `ReadClaudeQuotaViaPTY`, `ClaudeQuotaCachePath`, `ReadClaudeQuotaFrom`, `DecideClaudeQuotaRouting`, `ClaudeQuotaSnapshot{}` (constructor), `WriteClaudeQuota` (×2), codex equivalents | All cache I/O and snapshot construction moves behind `QuotaHarness.RefreshQuota`. Health-check callbacks at lines 37, 41-45 become `qh.RefreshQuota(ctx)` calls. |
| `service_models.go:19-21` | `claudeharness`, `codexharness`, `geminiharness` | `DefaultClaudeModelDiscovery`, `ResolveClaudeFamilyAlias`, codex/gemini equivalents | Replace with `mdh, ok := h.(harnesses.ModelDiscoveryHarness); if ok { snap := mdh.DefaultModelSnapshot(); model, err := mdh.ResolveModelAlias(family, snap) }`. |
| `service_subscription_quota.go:7-9` | `claudeharness`, `codexharness`, `geminiharness` | `ReadClaudeQuotaRoutingDecision`, `ReadCodexQuotaRoutingDecision`, `ReadGeminiQuotaRoutingDecision`, type assertion `*claudeharness.ClaudeQuotaSnapshot` at line 79 | Replace decision reads with `QuotaStatus.RoutingPreference`. The `claudeQuotaMaxUsedPercent` helper at line 79 currently computes max-used over snapshot fields (`FiveHourRemaining`, `WeeklyRemaining`); under the refactor this becomes derivation over `QuotaStatus.Windows[i].UsedPercent`, which both claude and codex expose uniformly. Decided in Step 4 (claude migration) — not deferred. |
| `internal/serviceimpl/execute_dispatch.go:9-13` | All five `*harness` packages | `&claudeharness.Runner{}`, `&codexharness.Runner{}`, etc. (constructor only) | **Retained.** This is the explicit runner-constructor seam allowed by CONTRACT-004. The dispatcher continues to know concrete runner types. |
| `internal/runtimesignals/collect.go:11-13` | `claudecache`, `codexcache`, `geminicache` | Per-harness cache reads for runtime-signal collection | Replace with `qh, ok := h.(harnesses.QuotaHarness); if ok { qs, _ := qh.QuotaStatus(ctx, now); ... }`. |

### Tests that consume per-harness packages

| File | Current usage | Migration |
|------|---------------|-----------|
| `service_status_test.go` | `claudeharness`, `codexharness` snapshot constructors | Synthetic `QuotaHarness` implementations (Step 2's conformance suite ships a `synthQuotaHarness` helper). |
| `service_route_attempts_test.go` | `codexharness`, `geminiharness` | Synthetic `QuotaHarness` implementations. |
| `harness_golden_integration_test.go` | `claudeharness`, `codexharness` | Cassette-driven; cassettes unaffected. Test seeds become interface-level. |
| `service_subscription_quota_test.go` | `claudeharness` snapshots | Synthetic `QuotaHarness`. |
| `service_routing_errors_test.go` | `claudeharness` | Synthetic `QuotaHarness`. |
| `service_providers_test.go` | `claudeharness`, `codexharness` | Synthetic `QuotaHarness` plus fixture cache writes for refresh tests. |
| `service_routing_test.go` | `claudeharness` | Synthetic `QuotaHarness`. |
| `service_execute_dispatch_test.go` | All five package strings (lines 45-49) | No change — test asserts dispatcher recognizes harness names; package paths remain. |
| `internal/harnesses/runner_info_parity_test.go` | `claudeharness` | Moves into Step 2's conformance suite as a shared assertion. |
| `internal/runtimesignals/collect_test.go` | `claudecache` | Synthetic `QuotaHarness` or fixture cache. |
| `internal/harnesses/<name>/*_test.go` | Currently mixed `package <name>` and `package <name>_test` | All harness-local tests run as `package <name>` (white-box) so they access unexported helpers after the refactor. Black-box tests that exist today are reviewed and either flipped to white-box or rewritten against the public interface. |

### Embedding-interface projection sites

| Public field (CONTRACT-003) | Defined at | Current source | Refactor source |
|------------------------------|------------|----------------|-----------------|
| `service.QuotaState.Windows` | `service.go:167` | Per-harness snapshot's `Windows` field | `QuotaStatus.Windows` |
| `service.QuotaState.CapturedAt` | `service.go:168` | Per-harness snapshot's `CapturedAt` | `QuotaStatus.CapturedAt` |
| `service.QuotaState.Fresh` | `service.go:169` | Per-harness `Is*QuotaFresh` | `QuotaStatus.Fresh` |
| `service.QuotaState.Source` | `service.go:170` | Per-harness snapshot's `Source` | `QuotaStatus.Source` |
| `service.QuotaState.Status` | `service.go:171` | Computed by service from snapshot windows | `string(QuotaStatus.State)` with ` (<Reason>)` suffix when non-empty (per CONTRACT-004 projection) |
| `service.QuotaState.LastError` | `service.go:172` | Set by service when probe error surfaces | Non-nil only on interface-method call failure |
| `service.AccountStatus.*` | `service.go:186-196` | Per-harness account read | `AccountSnapshot.*` for harnesses implementing `AccountHarness` |
| `service.HarnessInfo.Quota` | `service.go:232+` | Built per harness name | Single helper consuming `QuotaStatus` |
| `service.HarnessInfo.Account` | `service.go:232+` | Built per harness name | Single helper consuming `AccountSnapshot` |
| `service.ProviderInfo.Quota` | `service.go` ProviderInfo assembly | Same | Same |
| `service.ProviderInfo.Auth` | Same | Same | Same |

## Implementation Steps

Sequenced so the contract is testable before any migration, projection
helpers and the conformance suite exist before the first harness lands,
and the lint rule lands last. Each step is one or more PRs as noted;
the per-harness migration steps (Step 5–8) are explicitly multi-PR
sub-sequences.

### Step 0 — Pre-refactor baseline

**Goal**: Pin the surfaces that must not regress.

**Work**:
- Add `service_contract_snapshot_test.go` fixtures (or extend the
  existing one) serializing `HarnessInfo`, `ProviderInfo`,
  `QuotaState`, and `AccountStatus` to JSON under a deterministic
  scenario set: one of each of the five harnesses, plus a
  representative provider with quota. Fixtures pinned under
  `testdata/contract-003/pre-refactor/`.
- Run `grep -rn "internal/harnesses/\(claude\|codex\|gemini\|opencode\|pi\)" --include="*.go" .`
  over the entire repository, excluding the harness packages themselves,
  `node_modules/`, `.claude/worktrees/`, `benchmark-results/`, and
  `.ddx/`. List the full result set in the Step 0 PR description.
  Add any file not already in the "Service-side consumers" or "Tests"
  inventory tables above. (Current expectation: zero unknowns — CLI
  binaries route through `Service.*` — but verify before any
  migration step starts.)
- Record `go test -count=1 ./... -timeout 30m` wall time as the
  pre-refactor baseline. Save in the PR description and in
  `testdata/perf-baselines/2026-05-14-pre-refactor.txt`. This is the
  baseline that Step 11's regression budget compares against. Note
  that Step 2 (conformance suite) and Step 4 (scheduler) add genuine
  new test coverage; Step 11's budget accounts for them separately
  rather than asserting a flat 10% cap on total runtime.

**Verification of refactor**: Step 11 re-runs the fixtures with a
structural JSON diff (not byte-equal) that ignores `CapturedAt`
formatting variance and any documented additive fields, asserting
all other shapes are preserved.

**Size**: 1 PR.

### Step 1 — Add interfaces and universal types

**Goal**: CONTRACT-004 declarations exist; no harness implements them yet.

**Work**:
- Edit `internal/harnesses/types.go`:
  - Add `QuotaHarness`, `AccountHarness`, `ModelDiscoveryHarness`
    interface declarations.
  - Add `QuotaStatus`, `AccountSnapshot`, `QuotaStateValue`,
    `RoutingPreference` type declarations.
  - Add `ErrAliasNotResolvable` sentinel error.
- Add unit tests covering type defaults, RoutingPreference enum
  identity, and that `QuotaStateValue` string conversion matches the
  documented CONTRACT-004 values.

**Size**: 1 PR.

### Step 2 — Conformance suite and synthetic harness

**Goal**: The contract is testable. Every later migration step runs
against this suite.

**Work**:
- New package `internal/harnesses/harnesstest/` (test helper, not
  test-only) containing:
  - `RunHarnessConformance(t, h harnesses.Harness)` — asserts
    `Info`, `HealthCheck` (without invoking the binary), and the
    `Execute` setup-vs-runtime-error contract.
  - `RunQuotaHarnessConformance(t, h harnesses.QuotaHarness)` —
    asserts `QuotaStatus` returns a valid `QuotaStatus` value (not
    an error) when the cache is empty, asserts `QuotaFreshness` is
    positive, reads `SupportedLimitIDs()` and asserts every emitted
    `Windows[].LimitID` (after driving a `RefreshQuota` against a
    cassette or synthetic state) is a member of that set. The suite
    asserts the **contract shape** of single-flight semantics —
    that concurrent `RefreshQuota` calls return without error and
    share a consistent post-state — but does not assert "exactly one
    probe ran." Probe-count single-flight is testable on the
    synthetic harness (in-memory counter; covered in the synthetic
    test); on real harnesses (claude/codex/gemini) it requires
    binary-specific or filesystem-specific harnessing and lives in
    each harness's own package tests, not the shared suite.
  - `RunAccountHarnessConformance(t, h harnesses.AccountHarness)` —
    analogous, including `AccountFreshness`.
  - `RunModelDiscoveryHarnessConformance(t, h harnesses.ModelDiscoveryHarness)` —
    asserts non-empty `DefaultModelSnapshot`, reads
    `SupportedAliases()`, asserts `ResolveModelAlias` resolves each
    listed alias (positive path) and returns
    `ErrAliasNotResolvable` for an out-of-set family (negative
    path). Empty `SupportedAliases()` skips the positive-path
    check (opencode/pi case).
  - `NewSyntheticQuotaHarness(name string, status QuotaStatus, limitIDs []string) QuotaHarness`,
    `NewSyntheticAccountHarness(name string, snapshot AccountSnapshot, freshness time.Duration) AccountHarness`,
    `NewSyntheticModelDiscoveryHarness(name string, snapshot ModelDiscoverySnapshot, aliases []string) ModelDiscoveryHarness` —
    in-memory implementations used by service-level tests to replace
    per-harness snapshot construction. Constructors accept the values
    each `Supported*` method should return so tests can exercise both
    populated and empty contract surfaces.
- Add a smoke test that runs the synthetic harnesses against the
  conformance suite (proves the suite works before any real harness
  consumes it).

**Size**: 1 PR.

### Step 3 — CONTRACT-003 projection helpers

**Goal**: One projection helper per public type. The first harness
migration consumes them.

**Work**:
- New file `service_projection.go` (or extend existing) with:
  - `func projectQuotaStatus(qs harnesses.QuotaStatus) *QuotaState`
  - `func projectAccountSnapshot(as harnesses.AccountSnapshot) *AccountStatus`
- Unit tests drive the helpers with synthetic
  `QuotaStatus`/`AccountSnapshot` values covering every
  `QuotaStateValue`, every `RoutingPreference`, populated and empty
  `Account`/`Detail`, and `Reason` present/absent. Assert the public
  JSON shape matches Step 0 fixtures structurally.
- No service code calls the helpers yet — they exist for Step 5
  onward.

**Size**: 1 PR.

### Step 4 — Async refresh scheduler

**Goal**: A single service-level scheduler consumes
`QuotaHarness.QuotaFreshness()` and `AccountHarness.AccountFreshness()`
to drive refreshes. Replaces today's per-harness
`Refresh*Async`/`refreshClaudeQuotaAsync` helpers.

**Work**:
- **Introduce a `harnessByName(name string) harnesses.Harness`
  method on the service struct** (consulting the registered-harnesses
  field) if it does not already exist. This is the seam every later
  step uses to fetch the registered harness instance and type-assert
  it to the optional sub-interfaces. Logically belongs with the
  scheduler; lifting it here means Step 5b can consume it without
  introducing it.
- New file `service_refresh_scheduler.go`. Owns:
  - A debounced ticker per registered `QuotaHarness` keyed by
    harness name, firing at `QuotaFreshness()/2` and calling
    `RefreshQuota(ctx)`.
  - Analogous tickers for `AccountHarness` instances whose
    freshness window differs from quota's (e.g. gemini's 7-day
    auth).
  - Startup refresh kickoff (cache-cold path from
    `primary-harness-capability-baseline.md`).
  - Tests with a fake clock that prove the scheduler debounces,
    respects single-flight (because `RefreshQuota` blocks on the
    harness's cache lock), and exits cleanly on context cancel.
- **Harness registration mechanism**: at construction time the
  scheduler iterates `service.harnessByName(name)` over every name
  in the existing harness registry and type-asserts each result to
  `QuotaHarness` and `AccountHarness` independently. Pre-migration,
  type assertions fail for non-migrated harnesses and the scheduler
  registers nothing for them — they continue to use their existing
  `Refresh*Async` helpers. Post-migration (Steps 5–8), the
  assertions succeed and the migrated harness participates. No
  explicit per-harness registration call is needed; the scheduler
  picks up new harnesses on its next construction.
- The scheduler is the only place that deduplicates probe calls by
  cache freshness; `QuotaHarness.RefreshQuota` itself probes
  unconditionally per CONTRACT-004. The scheduler skips a tick when
  the last-known `QuotaStatus.CapturedAt` is within
  `QuotaFreshness()` of `now`.
- Per-harness `Refresh*Async` functions are not yet deleted — they
  stay until their owning harness migrates in Steps 5–8. Each
  harness's Step 5e/6e/7e sub-PR deletes its `Refresh*Async` after
  confirming the scheduler is driving its refresh cadence.

**Size**: 1 PR.

### Step 5 — Migrate claude harness (multi-PR sub-sequence)

**Goal**: `claude.Runner` implements `QuotaHarness`, `AccountHarness`,
`ModelDiscoveryHarness`. All claude-related service consumers route
through the interface. Old surface deleted.

This is the largest migration. It lands as a sub-sequence of PRs
because the surface change is too big for a single reviewable diff
and because intermediate states must compile and pass tests.

**5a — Add interface methods on `claude.Runner`** (new methods only;
old surface stays):
- Add `QuotaStatus`, `RefreshQuota`, `QuotaFreshness`,
  `SupportedLimitIDs`, `AccountStatus`, `RefreshAccount`,
  `AccountFreshness`, `DefaultModelSnapshot`, `ResolveModelAlias`,
  `SupportedAliases` methods.
- Routing-preference mapping helper translates today's claude
  routing decision (PreferClaude boolean plus freshness) into
  `RoutingPreference`.
- The methods internally call the still-exported cache I/O
  functions (which become unexported in 5e).
- Add a `package claude` doc comment (`doc.go` or top of `runner.go`)
  enumerating the values returned by `SupportedLimitIDs()` (e.g.
  `session`, `weekly-all`, `weekly-sonnet`) and `SupportedAliases()`
  (e.g. `sonnet`, `opus`, `haiku`) for human readers. The
  programmatic source of truth is the method return value; the
  doc comment mirrors it.
- Run `harnesstest.RunQuotaHarnessConformance` and the other
  conformance functions against `&claude.Runner{}`. The conformance
  suite reads `SupportedLimitIDs()` and `SupportedAliases()` to
  assert that emitted `Windows[].LimitID` values and the aliases
  `ResolveModelAlias` accepts both match the documented sets.

**5b — Migrate `service.go` claude consumers** to interface calls,
fetching the runner instance via the `harnessByName` helper
introduced in Step 4. Drop `claudeharness` import from `service.go`.
Old surface still imported by other service files.

**5c — Migrate `service_providers.go`** claude consumers. Includes
moving snapshot construction (`ClaudeQuotaSnapshot{...}` in
service_providers.go:620) into `claude.Runner.RefreshQuota`'s
internal implementation.

**5d — Migrate `service_models.go`** and `service_subscription_quota.go`
claude consumers. Includes refactoring `claudeQuotaMaxUsedPercent` to
consume `QuotaStatus.Windows`.

**5e — Delete and unexport** the claude package public surface:
delete `ClaudeQuotaRoutingDecision` / `DecideClaudeQuotaRouting` /
`ReadClaudeQuotaRoutingDecision`; lowercase `ClaudeQuotaSnapshot` →
`claudeQuotaSnapshot`; unexport `ReadClaudeQuota`,
`ReadClaudeQuotaFrom`, `WriteClaudeQuota`, `ClaudeQuotaCachePath`,
`IsClaudeQuotaFresh`, `ClaudeQuotaSnapshotAge`,
`DefaultClaudeQuotaStaleAfter`, `ReadClaudeQuotaViaPTY`,
`RefreshClaudeQuotaViaPTY`, `ReadClaudeQuotaFromCassette`,
`DefaultClaudeModelDiscovery`, `ResolveClaudeFamilyAlias`,
`ReadClaudeReasoningFromHelp`, `IsClaudeQuotaExhaustedMessage`,
`MarkClaudeQuotaExhaustedFromMessage`, `RefreshClaudeQuotaAsync`.

Before merging 5e, verify that reverting only 5e (without reverting
5a–5d) leaves the tree compiling and tests green: the consumers in
5b–5d talk to the interface, not to the snapshot type, so they do
not care whether the snapshot type is exported. This is the
rollback safety property and is asserted in the 5e PR as a
pre-merge check.

**5f — Migrate claude-related tests** to interface mocks or fixture
cache files. Run Step 0 fixtures with structural diff; assert no
unintentional shape changes.

**Acceptance** (the sub-sequence as a whole):
- No file outside `internal/harnesses/claude/` and
  `internal/serviceimpl/execute_dispatch.go` imports
  `internal/harnesses/claude`.
- Step 0 fixtures pass structural diff (claude rows).
- `go test ./...` passes; runtime baseline tracked.

**Size**: 5–6 PRs over 1–2 weeks.

### Step 6 — Migrate codex harness

**Goal**: Mirror of Step 5 for codex.

Codex-specific notes:
- Session-token-count quota source (`ReadCodexQuotaFromSessionTokenCounts`)
  folds into `RefreshQuota` as an internal fallback for when the PTY
  probe is unavailable.
- Codex's separate account file (`CodexAuthPath`, `ReadCodexAccount`)
  becomes the `AccountStatus`/`RefreshAccount` implementation. Codex
  satisfies `AccountHarness` explicitly with
  `AccountFreshness()` returning the same window as quota (today's
  behavior — account refreshes alongside quota).
- Step 6a implements `SupportedLimitIDs()` and `SupportedAliases()`
  on `codex.Runner` and mirrors their values in a `package codex`
  doc comment (e.g. aliases `gpt`, `gpt-5`).

Same 5a–5f sub-sequence shape.

**Size**: 5–6 PRs over 1–2 weeks.

### Step 7 — Migrate gemini harness

**Goal**: Mirror of Step 5 for gemini.

Gemini-specific notes:
- `AuthSnapshot` becomes the source of `AccountSnapshot`.
- `AccountFreshness()` returns 7 days; `QuotaFreshness()` returns
  15 minutes. The refresh scheduler from Step 4 runs them on
  independent cadences.
- Tier-specific information (Flash / Flash Lite / Pro) goes into
  `QuotaStatus.Windows` (one per tier, distinguished by `LimitID`).
  No tier names appear in `Detail`.
- Step 7a implements `SupportedLimitIDs()` (per-tier IDs included)
  and `SupportedAliases()` on `gemini.Runner` and mirrors their
  values in a `package gemini` doc comment.

Same 5a–5f sub-sequence shape.

**Size**: 4–5 PRs over 1–2 weeks.

### Step 8 — Migrate opencode and pi

**Goal**: opencode and pi satisfy `ModelDiscoveryHarness`. No
`QuotaHarness` or `AccountHarness`.

**Work**:
- Add `DefaultModelSnapshot`, `ResolveModelAlias`, and
  `SupportedAliases` to each `Runner`. Empty `SupportedAliases()`
  return is acceptable for harnesses with no family aliases.
- Unexport `DefaultOpenCodeModelDiscovery`, `DefaultPiModelDiscovery`,
  related helpers.
- Decide fate of `OpenCodeModelEvidence`, `OpenCodeModelCost` (used
  internally only today; unexport unless external need surfaces).
- Add package doc comments to both `package opencode` and
  `package pi` mirroring the `SupportedAliases()` return value (empty
  or otherwise).
- Migrate `service_models.go` consumers for these two harnesses.

**Acceptance**: No service-side import of `opencodeharness` or
`piharness` beyond the dispatcher.

**Size**: 1 PR (these are small).

### Step 9 — Migrate `internal/runtimesignals/collect.go`

**Goal**: Runtime-signal collection consumes the interface only.

**Work**:
- Replace `claudecache`/`codexcache`/`geminicache` imports with
  `internal/harnesses` interface usage.
- Update `collect_test.go` to use synthetic `QuotaHarness` fixtures
  from Step 2.

**Acceptance**: `internal/runtimesignals/` does not import any
`internal/harnesses/<name>` package.

**Size**: 1 PR.

### Step 10 — Add the lint rule

**Goal**: Prevent regressions.

**Work**:
- Add a check in `internal/lint/` (or the project's existing lint
  pass) that fails if any `.go` file outside `internal/harnesses/`
  imports `internal/harnesses/claude`, `internal/harnesses/codex`,
  `internal/harnesses/gemini`, `internal/harnesses/opencode`, or
  `internal/harnesses/pi`, with one allow-listed exception:
  `internal/serviceimpl/execute_dispatch.go`.
- Wire the check into `lefthook.yml` and CI.

**Acceptance**: Lint runs in CI; introducing a forbidden import fails
the build with a clear error citing CONTRACT-004 invariant #1.

**Size**: 1 PR.

### Step 11 — Post-refactor JSON fixture re-validation

**Goal**: Confirm public CONTRACT-003 JSON shapes are structurally
identical to Step 0 captures.

**Work**:
- Re-run the Step 0 fixtures with structural diff. Implementation: a
  small test helper at `internal/test/structuraldiff/diff.go` (or
  equivalent location the Go toolchain compiles — NOT under
  `testdata/`, which is excluded from builds) that unmarshals both
  fixtures into `map[string]any`, walks the tree, and applies
  documented field-level ignores (RFC3339-shape check for
  `CapturedAt` and other `time.Time` fields; presence-only for any
  field flagged opaque in CONTRACT-004).
  - Field set must be identical (no fields added or removed unless
    intentional and documented).
  - Field values must match semantically (string equality except
    `time.Time` fields, which are compared under the "valid RFC3339,
    present iff source was present" rule).
  - Documented additive fields (none expected; if any, listed in
    the PR description) are allowed.
- Runtime regression check (two separate budgets):
  - Pre-existing test suite (everything outside the new
    `internal/harnesses/harnesstest` and `service_refresh_scheduler*`
    paths) within 10% of Step 0 baseline.
  - No individual pre-existing test slows by more than 10%
    (measured with `-count=1` and the `-v` flag plus a
    timing-extraction script).
  - The new conformance and scheduler tests are budgeted separately
    in the Step 11 PR description (they did not exist at Step 0 so
    a percentage delta is meaningless; instead the absolute new
    runtime is reported and reviewed).

**Acceptance**: Public JSON shapes match Step 0 fixtures structurally;
test runtime within budget.

**Size**: 1 PR.

### Step 12 — Documentation pass

**Goal**: Spec and code-doc consistency.

**Work**:
- Update PRD line 204 ("implement the harness interface
  (CONTRACT-003)") to cite CONTRACT-004.
- Update `primary-harness-capability-baseline.md` if any capability
  evidence pointer cites a deleted symbol.
- Update CONTRACT-003 to cross-reference CONTRACT-004 as the source
  for `HarnessInfo.Quota` and `HarnessInfo.Account` population.
- Update AGENTS.md (or equivalent contributor guide) to describe the
  harness implementation pattern: implement `Harness` plus optional
  sub-interfaces, keep snapshot types package-private, no cross-harness
  imports, expose stable contract surfaces via `SupportedAliases()`
  and `SupportedLimitIDs()`.
- Verify each `internal/harnesses/<name>/` package has a doc comment
  enumerating its `SupportedLimitIDs()` (where applicable) and
  `SupportedAliases()` return values, matching the programmatic
  source of truth. A Go test using `go/doc` parses each harness's
  package comment, extracts the documented sets, and asserts equality
  with the values the `Supported*` methods return on a default
  `Runner{}` instance. Drift fails CI.

**Acceptance**: All inbound references resolve; no spec references a
deleted symbol; every harness package has a doc-comment mirror of
its `Supported*` exports.

**Size**: 1 PR.

## Test Strategy

| Layer | Coverage |
|-------|----------|
| Unit (per harness) | Interface method implementations; routing-preference mapping; cache I/O against synthetic files. |
| Conformance suite | Every harness runs Step 2 assertions for its declared interfaces. Suite tests itself against the synthetic harness in Step 2. |
| Service-level | JSON-shape preservation against Step 0 fixtures via structural diff in Step 11; routing decisions against synthetic `QuotaHarness` implementations covering every `RoutingPreference` × `QuotaStateValue` combination. |
| Integration | Existing `harness_golden_integration_test.go` cassettes continue to pass without modification (cassettes are below the interface boundary). |
| Concurrency | The conformance suite asserts `RefreshQuota` and `RefreshAccount` single-flight under concurrent invocation (CONTRACT-004 invariant #7). |
| Lint | Step 10 boundary rule runs in CI. |
| Regression | Pre-existing test suite within 10% of Step 0 baseline (asserted in Step 11); no individual pre-existing test slower by more than 10%; new conformance + scheduler tests budgeted separately. |

## Rollback Strategy

Each step's PR is independently revertable.

- **Cache file compatibility**: All per-harness cache files use
  field-tagged JSON encoding. Renaming the Go type from
  `ClaudeQuotaSnapshot` to `claudeQuotaSnapshot` does not change the
  on-disk JSON shape (verified for each harness in Step 5a/6a/7a
  before unexporting). A revert of Step 5e reintroduces the exported
  type name without invalidating cached files written under the new
  code.
- **Per-harness rollback**: Reverting Step 5 (claude) does not require
  reverting Steps 6–8 (codex/gemini/opencode/pi) because each
  harness's interface implementation is independent. The reverted
  harness goes back to its per-harness public surface; the others
  remain on the interface.
- **Pre-lint coexistence**: Step 10's lint rule lands last
  intentionally so that partial-state environments (some harnesses
  migrated, some not) compile and test. A revert during this window
  produces a working tree; a revert after Step 10 may require
  reverting Step 10 alongside.
- **Public JSON shape**: Step 11's structural-diff assertion is the
  guarantee that any merged state preserves CONTRACT-003. A diff at
  Step 11 blocks merge; rollback is a revert of the offending step,
  not a forward-fix on a broken contract.

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| JSON shape regression during projection refactor | Step 0 fixtures pinned before any refactor work; Step 11 asserts structural equality with documented time-field handling. |
| Routing semantics drift (boolean `PreferX` → `RoutingPreference` enum) | Step 5 (claude) lands fully and runs the routing test suite before Step 6 (codex) begins. Each subsequent harness re-runs the suite. |
| Cache file schema drift when snapshots are renamed | Cache files use field-tagged JSON; type renames don't affect on-disk shape. Each Step 5a/6a/7a includes a load-old-write-new round-trip test before unexporting. |
| Conformance suite is too strict and blocks legitimate variation | Conformance covers only CONTRACT-004 contract behavior. Harness-specific tests stay in the harness package. The suite is itself tested in Step 2 against the synthetic harness. |
| Lint exemption file accumulates as a backdoor | The exemption list in Step 10 is one line; growing it requires an ADR amendment. |
| Refactor stalls partway, leaving mixed surface | Step 10 (lint rule) lands last; partial state is a valid intermediate. Each harness migration's sub-sequence (5e/6e/7e/8) deletes the old surface as an explicit acceptance gate before the next harness starts. |
| `claude-tui` work resurfaces during the refactor and bypasses it | ADR-013 is withdrawn (not paused); re-proposal must cite the merged CONTRACT-004. |
| Async refresh scheduler is wrong and refreshes too often or too rarely | Step 4's scheduler tests use a fake clock and assert exact tick counts per `QuotaFreshness()` value. Production scheduling defects are caught by `service_status` integration tests measuring actual refresh cadence. |
| Step 5 sub-sequence stalls partway (5b lands, 5c does not) | Intermediate states (5a-5d) are valid: claude's new interface methods coexist with the old surface. The old surface only disappears in 5e. |

## Acceptance Criteria

1. `internal/harnesses/types.go` declares the four interfaces and
   universal types per CONTRACT-004.
2. Every existing harness implements the interfaces appropriate to it
   (verified by the Step 2 conformance suite).
3. No `.go` file outside `internal/harnesses/` imports a symbol from
   `internal/harnesses/<name>/` except
   `internal/serviceimpl/execute_dispatch.go`.
4. Per-harness `*QuotaSnapshot` types are lowercase.
5. Per-harness `*QuotaRoutingDecision` types are deleted.
6. Per-harness cache I/O functions are unexported.
7. The async refresh scheduler from Step 4 owns all per-harness
   refresh cadence; per-harness `Refresh*Async` helpers are deleted.
8. CONTRACT-003 JSON fixtures (Step 0) pass structural diff
   post-refactor (Step 11).
9. Lint rule from Step 10 runs in CI and blocks new forbidden imports.
10. PRD, CONTRACT-003, and AGENTS.md cross-references are updated.
11. Pre-existing test suite runtime is within 10% of the Step 0
    baseline (excluding new packages from Steps 2 and 4); no
    individual pre-existing test slows by more than 10%.

## Follow-Ups (Out of Scope)

- **`claude-tui` re-proposal.** Once this refactor lands, ADR-013
  may be re-proposed citing CONTRACT-004. The fork then becomes
  additive: a new `internal/harnesses/claude-tui/` package
  implementing the same four interfaces, sharing a snapshot type with
  `claude` through an `internal/harnesses/anthropic/` neutral
  subpackage (since both snapshots are now package-private, sharing
  them requires only moving the type into a shared internal
  package). Re-proposal still requires empirical evidence that
  PTY-driven Claude lands on subscription quota while
  `claude --print` lands on per-token API pricing — the structural
  refactor here does not validate that premise.
- **`HarnessConfig` registry cleanup.** The current `HarnessConfig`
  struct mixes interface-relevant metadata with subprocess-specific
  knobs. Splitting these is a separate cleanup; CONTRACT-004 does
  not require it.
- **Provider-side interface.** HTTP providers (`openrouter`,
  `lmstudio`, `omlx`) have their own per-provider exports. A
  parallel `ProviderHarness` contract is plausible but not in scope.
