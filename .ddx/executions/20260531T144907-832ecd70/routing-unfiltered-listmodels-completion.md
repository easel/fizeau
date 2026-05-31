# fizeau-6506be18 — completion status & AC3 escalation

**Bead:** routing: ListModels(unfiltered) must enumerate available subscription-harness tiers
**Spec:** FEAT-004 · base-rev `949441d4`
**Date:** 2026-05-31

## TL;DR

The bead's substantive deliverable is **already implemented and committed** at
`5089b0d3` (`fix(routing): include available subscription-harness tiers in
unfiltered ListModels [fizeau-6506be18]`), which is an ancestor of this
execution's base-rev. ACs 1, 2, and 4 are verifiably satisfied. **AC 3
(`go test ./...` fully green) is unsatisfiable from within this bead's scope**:
it is blocked by pre-existing, environment-dependent PTY/subprocess/discovery
test failures in *other* packages and in an *upstream, NON-SCOPE* code path of
the root package. This is escalated for an operator decision rather than
looped again (12th execution of this bead).

## What was delivered (commit 5089b0d3)

`ListModels(ModelFilter{})` (unfiltered) now appends the available
subscription-harness tiers (claude/codex/gemini …) after the provider-backed
snapshot, so the routing ladder is non-empty even when configured HTTP
providers are down/empty. Implementation in `service_models.go`:

- `ListModels` line 52 appends `availableSubscriptionHarnessModels(filter, out)`.
- `availableSubscriptionHarnessModels` enumerates `registry.Discover()`,
  excluding `fiz`, HTTP-only, `TestOnly`, and in-process harnesses; honors
  `filter.Provider`; dedups against provider-backed output.
- `subscriptionHarnessTierModels` is the shared tier-builder used by both the
  harness-pinned path and the unfiltered path (so filtered semantics are
  byte-for-byte unchanged). Scoring untouched.

## AC verification (this base-rev)

| AC | Status | Evidence |
|----|--------|----------|
| 1. `TestListModelsUnfilteredIncludesAvailableSubscriptionTiers` returns non-empty subscription tiers w/ power metadata when no HTTP providers but claude/codex available | **PASS** | `go test . -run '^TestListModelsUnfilteredIncludesAvailableSubscriptionTiers$'` → ok |
| 2. Filtered-by-harness behavior unchanged | **PASS** | `go test . -run '^TestListModelsFilteredByHarnessUnchanged$'` → ok (this is the implemented name of AC2's `TestListModelsFilteredUnchanged`; it asserts the filtered output is `reflect.DeepEqual` to the shared tier-builder) |
| 3. `go test ./...` green | **BLOCKED — out of scope** | see below |
| 4. `lefthook run pre-commit` passes | **PASS** | pre-commit = `make fmt-check` (exit 0) + `make vet` (exit 0) |

Deliverable verification command (deterministic, exits 0):

```
go test . -run '^TestListModelsUnfilteredIncludesAvailableSubscriptionTiers$|^TestListModelsFilteredByHarnessUnchanged$' -count=1 -timeout 120s
```

## Why AC3 cannot be met within this bead's scope

`go test ./...` is red/hung across multiple packages, all due to
environment-dependent PTY / subprocess / harness-discovery behavior, none in
this bead's named scope (`service_models.go` unfiltered enumeration), and none
caused by commit 5089b0d3:

1. **`internal/harnesses/ptyquota`** — `FAIL`
   `TestRunScrubsCassetteOutputBeforePromotion`: "quota probe exited before
   expected output". PTY cassette/quota probe behaves differently in this
   headless sandbox.

2. **`agentcli`** — hang → timeout (~45s), goroutines stuck in
   `os/exec.(*Cmd).Start` (subprocess/PTY spawn).

3. **root `github.com/easel/fizeau`** — deterministic hang in
   `TestListModels_contextSourcePrecedence/default_falls_back_when_catalog_missing`.
   Root cause (fully traced):
   - Snapshot assembly (`service_models.go:41`, the **provider-backed path that
     predates and is upstream of this bead's change**) discovers the implicit
     `claude`/`codex` harness providers via `modelsnapshot.discoverProvider` →
     `discoverHarnessProvider`.
   - The refresher calls `harnesses.CachedModelDiscoverySnapshot(…, "embedded-cassette")`,
     but `LoadEmbeddedModelDiscoverySnapshot` **always returns an error**
     ("embedded model discovery is not supported; harness discovery is
     live-only via PTY", `internal/harnesses/discovery_cache.go:82`).
   - `discoverycache` records a failure marker; a subsequent assembly call for
     the same source hits `claimRefresh` → `waitForRefresh`, which polls up to
     the 60s `RefreshDeadline` (`internal/discoverycache/cache.go:302-318`),
     exceeding the test timeout.
   - The bead's own added code (`service_models.go:52`,
     `availableSubscriptionHarnessModels`) runs *after* line 41 and never
     executes because line 41 hangs first — i.e. the hang is independent of
     this bead.

The bead's stated **NON-SCOPE** ("do not change filtered ListModels semantics;
do not change scoring") and the execution rule "do not modify files outside the
bead's named scope" both forbid touching `internal/harnesses`,
`internal/discoverycache`, `internal/modelsnapshot`, `agentcli`, or the
unrelated `TestListModels_contextSourcePrecedence` test. Fixing AC3 means fixing
these unrelated subsystems, which belongs in separate bead(s).

A prior execution (`20260530T064029-70ac6242`) already recorded the same
finding: "`go test ./...` is still red in unrelated packages (agentcli,
internal/harnesses, internal/harnesses/ptyquota)".

## Recommended operator actions

1. **Accept this bead as done** on the strength of its committed deliverable
   (5089b0d3) and the deterministic AC1/AC2/AC4 evidence above; AC3's
   full-suite gate is unsatisfiable for reasons external to this bead.
2. **File follow-up bead(s)** for the out-of-scope full-suite failures, e.g.:
   - Make root-package snapshot-assembly tests hermetic against harness
     discovery (or fix the `discoverycache` failure-marker `waitForRefresh`
     spin when the embedded loader errors) so `TestListModels_contextSourcePrecedence`
     does not hang.
   - `internal/harnesses/ptyquota` cassette/quota probe failure in headless CI.
   - `agentcli` PTY-spawn hang in headless CI.
