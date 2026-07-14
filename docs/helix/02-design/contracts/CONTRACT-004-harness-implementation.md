---
ddx:
  id: CONTRACT-004
  depends_on:
    - CONTRACT-003
  child_of: fizeau-67f2d585
  review:
    self_hash: 9d5b9e2470cea4bd8311d63f1f391dac82a8d4f0cdff42d131d3bf5a3bc86e9e
    deps:
      CONTRACT-003: 0c3695b0fa948442d8b2e85e4a93e1c37b88b88971062ca7052d9be036ccae32
    reviewed_at: "2026-07-14T08:00:37Z"
---
# CONTRACT-004: Harness Implementation Contract

| Field | Value |
|-------|-------|
| Status | Draft |
| Date | 2026-05-14 |
| Scope | Internal interface and live-process obligations every Fizeau harness package implements; service-side consumers depend only on this contract, never on harness-specific exports. |
| Companion | CONTRACT-003 (service-facing API); ADR-014 (decision rationale) |

## Purpose

CONTRACT-003 specifies the public Fizeau service surface. It does not specify
the internal contract that each harness implementation
(`internal/harnesses/<name>/`) satisfies.

> **Implementation reference (2026-05-14):** The original inventory found a
> 3-method `harnesses.Harness` interface plus roughly 80 ad-hoc per-harness
> exports across Claude, Codex, and Gemini. That inventory explains why this
> contract was created; it is not the desired interface or current registry.

This contract closes that gap. It defines the complete normative interface
every harness implementation MUST satisfy and constrains the service to
consume only that interface. Per-harness symbol leakage is the failure
mode CONTRACT-004 exists to prevent.

## Scope

In scope:

- The Go interface set every `internal/harnesses/<name>/` package
  implements.
- The universal types those interfaces return.
- Cache and refresh ownership.
- Live subprocess containment, cleanup, and recovery obligations shared by
  normal execution, PTY sessions, and auxiliary probes.
- Conformance evidence requirements.
- Projection rules from internal harness types to the CONTRACT-003
  public types (`QuotaState`, `AccountStatus`, `HarnessInfo`,
  `ProviderInfo`).

Out of scope:

- The CLI/network behavior of any specific harness binary (covered by
  primary-harness-capability-baseline.md and per-harness specs).
- The cassette/replay transport contract (ADR-002).
- The public service contract (CONTRACT-003).

## Interface Set

All six built-in subprocess harness implementations (`claude`, `claude-tui`,
`codex`, `gemini`, `opencode`, and `pi`) MUST implement `Harness`. A harness
MAY additionally
implement any of `QuotaHarness`, `AccountHarness`, and
`ModelDiscoveryHarness`; a cancellation-aware discovery implementation MAY
also implement `ContextModelDiscoveryHarness`. The service uses Go interface
assertions to discover which optional contracts a harness satisfies and
consumes them through the interface only.

```go
package harnesses

// Harness is the minimum every harness implements.
type Harness interface {
    // Info returns identity + capability metadata. Stable across the
    // process lifetime; cheap to call from request hot paths.
    Info() HarnessInfo

    // HealthCheck triggers a fresh, lightweight readiness probe (binary
    // present and executable; non-blocking auth state read where
    // possible). Returns nil when ready. MUST NOT drive interactive
    // sessions or block on network.
    HealthCheck(ctx context.Context) error

    // Execute runs one resolved request and returns an event channel.
    // The channel emits zero or more progress/text/tool events followed
    // by exactly one EventTypeFinal event, then closes. Setup failures
    // (binary missing, PTY allocation failure) return as the second
    // return value; per-run failures (auth, quota, parser desync,
    // timeout, cancellation) MUST be reported via the final event with
    // Status != "success".
    Execute(ctx context.Context, req ExecuteRequest) (<-chan Event, error)
}

// QuotaHarness is implemented by harnesses that own a subscription or
// quota window. claude, claude-tui, codex, and gemini implement;
// opencode and pi do not.
type QuotaHarness interface {
    Harness

    // QuotaStatus returns the current quota state from the harness's
    // owned cache, with Fresh/Age computed against `now`. MUST be cheap
    // (no live probe) and safe to call on every routing decision.
    // Absence of evidence is reported via State=QuotaUnavailable on a
    // valid QuotaStatus value; the error return is reserved for call
    // failure (ctx cancelled, IO failure, lock acquisition failure).
    QuotaStatus(ctx context.Context, now time.Time) (QuotaStatus, error)

    // RefreshQuota drives the harness's live probe (PTY, CLI, or other
    // owned transport), persists the result through the harness's owned
    // cache, and returns the resulting status. RefreshQuota probes
    // unconditionally on every successful lock acquisition; it does
    // NOT skip the probe based on cache freshness. Cache-freshness
    // deduplication is the caller's responsibility: callers that want
    // a possibly-stale-but-cheap read use QuotaStatus; callers that
    // orchestrate refresh cadence (e.g. a periodic scheduler) skip
    // ticks when the last-known CapturedAt is within QuotaFreshness()
    // of the current time. Callers that want a fresh probe call
    // RefreshQuota and accept the probe cost.
    //
    // RefreshQuota is single-flight per harness instance: concurrent
    // callers block on the harness's cache lock (per ADR-012). Queued
    // callers MUST NOT initiate a second probe — once the in-flight
    // refresh completes and releases the lock, queued callers read the
    // just-written cached state and return it. The contract guarantee
    // is "one probe per single-flight cohort," not "one probe per
    // call." Probe failure is reported as a QuotaStatus with
    // State=QuotaUnavailable (or QuotaUnauthenticated when the probe
    // identifies the failure as auth-related), not as an error. The
    // error return is reserved for call failure: ctx cancelled before
    // lock acquisition, lock acquisition timeout under deadline,
    // unrecoverable lock-file corruption, or unrecoverable IO failure.
    // Lock contention itself is not a failure — the caller waits.
    RefreshQuota(ctx context.Context) (QuotaStatus, error)

    // QuotaFreshness returns the harness's freshness window (e.g. 15m).
    // Service code uses this for stale-cache scheduling. Constant for
    // the harness; cheap to call.
    QuotaFreshness() time.Duration

    // SupportedLimitIDs returns the harness's stable set of emitted
    // Windows[].LimitID values (e.g. "session", "weekly-all",
    // "weekly-sonnet" for claude; "tier-flash", "tier-pro" for
    // gemini). Constant for the harness; the harness's package doc
    // also enumerates this set for human readers. The conformance
    // suite reads this value to verify that emitted Windows[].LimitID
    // strings are a subset of the documented set, and that the set
    // does not regress between binary versions without a deprecation
    // cycle. Empty slice is allowed for harnesses that emit no
    // windows.
    SupportedLimitIDs() []string
}

// AccountHarness is implemented by harnesses that expose authentication
// or account state independent of quota. gemini implements this so its
// AuthSnapshot can refresh on its own cadence (7-day freshness window vs.
// quota's 15-minute window). claude and codex embed account in their
// quota probe and MAY satisfy AccountHarness by re-projecting from
// QuotaStatus.Account; that is allowed but not required.
type AccountHarness interface {
    Harness

    // AccountStatus returns the harness's current account/auth state.
    // Cheap; reads cached evidence only. Absence of evidence is
    // reported via AccountSnapshot.Authenticated=false and
    // .Unauthenticated=false (i.e. unknown) on a valid snapshot; the
    // error return is reserved for call failure.
    AccountStatus(ctx context.Context, now time.Time) (AccountSnapshot, error)

    // RefreshAccount drives the harness's account probe (file read,
    // CLI call, OAuth state lookup) and persists the result.
    // Single-flight per harness instance via the harness's account
    // cache lock; concurrent callers block. Probe failure is reported
    // via AccountSnapshot fields, not as an error.
    RefreshAccount(ctx context.Context) (AccountSnapshot, error)

    // AccountFreshness returns the harness's account freshness window
    // (e.g. 7 days for gemini, 15 minutes for harnesses whose account
    // refreshes coupled with quota). Constant for the harness; cheap.
    // Used by the service refresh scheduler to decide when account
    // state is stale independent of quota state.
    AccountFreshness() time.Duration
}

// ModelDiscoveryHarness is implemented by harnesses whose model surface
// extends beyond a single Info().DefaultModel — i.e. they support family
// aliases (sonnet, gpt, gemini) that resolve through discovery evidence.
// All six built-in subprocess harnesses implement this in the current
// registry.
type ModelDiscoveryHarness interface {
    Harness

    // DefaultModelSnapshot returns discovery evidence. It returns
    // ErrModelDiscoveryEvidenceMissing when evidence cannot be obtained;
    // it MUST NOT fabricate a static success snapshot.
    DefaultModelSnapshot() (ModelDiscoverySnapshot, error)

    // ResolveModelAlias maps a family-style requested model (e.g.
    // "sonnet", "gpt", "gemini") to a concrete model ID using the
    // provided discovery snapshot. Returns ErrAliasNotResolvable if the
    // family is not recognized or the snapshot has no matching concrete
    // model.
    ResolveModelAlias(family string, snapshot ModelDiscoverySnapshot) (string, error)

    // SupportedAliases returns the harness's stable set of family
    // aliases ResolveModelAlias recognizes (e.g. "sonnet", "opus",
    // "haiku" for claude). Constant for the harness; the harness's
    // package doc also enumerates this set for human readers. The
    // conformance suite uses this value to verify ResolveModelAlias
    // covers each documented family (positive path) and rejects
    // out-of-set families with ErrAliasNotResolvable (negative path).
    // Empty slice is allowed for harnesses that recognize no family
    // aliases (e.g. opencode, pi).
    SupportedAliases() []string
}

// ContextModelDiscoveryHarness is the optional cancellation-aware extension
// preferred by service-owned refreshers for live discovery probes.
type ContextModelDiscoveryHarness interface {
    ModelDiscoveryHarness

    // DefaultModelSnapshotWithContext honors ctx through probe execution and
    // cleanup. It MUST NOT replace ctx with context.Background. Cancellation
    // starts cleanup; the method returns only after cleanup succeeds or the
    // service-owned cleanup deadline is reached.
    DefaultModelSnapshotWithContext(ctx context.Context) (ModelDiscoverySnapshot, error)
}
```

## Universal Types

```go
package harnesses

// QuotaStatus is the universal report consumed by service-side routing,
// status assembly, and operator surfaces. Each harness's private snapshot
// type projects into this; the private snapshot is never exposed.
type QuotaStatus struct {
    // Source identifies how the underlying evidence was captured:
    // "pty", "cache", "session-token-count", "cli", "api".
    Source string

    // CapturedAt is when the underlying evidence was observed (not when
    // this status struct was assembled).
    CapturedAt time.Time

    // Fresh reports whether CapturedAt is within QuotaFreshness() at the
    // time of the call.
    Fresh bool

    // Age is now - CapturedAt at the time of the call.
    Age time.Duration

    // State is the normalized state. Only QuotaOK and QuotaStale carry
    // routing-usable signal; others MUST NOT result in
    // RoutingPreferenceAvailable.
    State QuotaStateValue

    // Windows captures per-window evidence (5h, weekly, tier-specific).
    // Authoritative for any structured fact the routing layer or
    // operator surfaces consume — including tier breakdowns. Each
    // window's LimitID distinguishes it (e.g. "session", "weekly-all",
    // "weekly-sonnet", "tier-flash", "tier-pro"). Empty windows are
    // allowed for harnesses that report aggregate state only.
    //
    // LimitID values are part of the harness's stable public contract:
    // once a harness ships a LimitID, the routing layer may depend on
    // it and the harness MUST NOT silently rename, remove, or repurpose
    // it. Adding new LimitIDs is additive and safe. Renames go through
    // a deprecation cycle with both old and new IDs present long
    // enough for downstream consumers to migrate. Each harness's
    // package documentation enumerates its emitted LimitID set; the
    // primary-harness-capability-baseline tracks the canonical list.
    Windows []QuotaWindow

    // Account is the account/plan/auth evidence captured alongside
    // quota. Nil when the harness has no concept of account or when
    // account evidence is delivered through AccountHarness only.
    Account *AccountSnapshot

    // RoutingPreference indicates whether the routing layer should
    // prefer this harness given the current evidence. Encapsulates the
    // PreferClaude/PreferCodex/PreferGemini distinctions today.
    RoutingPreference RoutingPreference

    // Reason is a short human-readable explanation of State and
    // RoutingPreference — surfaced in operator views and routing logs.
    Reason string

    // Detail is harness-specific opaque metadata for diagnostic display
    // only. Service code MAY surface it verbatim in operator views;
    // service code MUST NOT branch on its keys or values for routing
    // decisions. Detail MUST NOT carry structured facts that the
    // routing layer needs — those belong in Windows. Detail is for
    // free-form notes (e.g. "captured at boot, before first refresh"),
    // not tier breakdowns or window data.
    Detail map[string]string
}

// QuotaStateValue is the normalized state enumeration.
type QuotaStateValue string

const (
    QuotaOK              QuotaStateValue = "ok"
    QuotaStale           QuotaStateValue = "stale"
    QuotaBlocked         QuotaStateValue = "blocked"
    QuotaUnavailable     QuotaStateValue = "unavailable"
    QuotaUnauthenticated QuotaStateValue = "unauthenticated"
    QuotaUnknown         QuotaStateValue = "unknown"
)

// RoutingPreference is the routing layer's consumable signal.
type RoutingPreference int

const (
    RoutingPreferenceUnknown   RoutingPreference = iota
    RoutingPreferenceAvailable
    RoutingPreferenceBlocked
)

// AccountSnapshot is the universal account/auth report. Projects onto
// the public AccountStatus type defined in CONTRACT-003.
type AccountSnapshot struct {
    Authenticated   bool
    Unauthenticated bool
    Email           string
    PlanType        string
    OrgName         string
    Source          string         // file path, env var name, "cache", "cli"
    CapturedAt      time.Time
    Fresh           bool
    Detail          string         // free-form diagnostic detail
}

// Sentinel errors for interface methods. Note: absence of quota or
// account evidence is NOT an error — it is reported via State or
// Authenticated/Unauthenticated fields on a valid returned value.
// Errors are reserved for call failure.
var (
    ErrAliasNotResolvable = errors.New("model alias not resolvable from snapshot")
    ErrModelDiscoveryEvidenceMissing = errors.New("model discovery evidence missing")
)
```

`QuotaWindow`, `ModelDiscoverySnapshot`, `Event`, `ExecuteRequest`,
`HarnessInfo`, and `EventType` retain their existing definitions in
`internal/harnesses/types.go`. CONTRACT-004 does not redefine them.

## Cache and Refresh Ownership

Each `QuotaHarness` and `AccountHarness` implementation owns:

- The on-disk cache path (defaulted to `$XDG_STATE_HOME/<harness>/quota.json`
  or `$XDG_STATE_HOME/<harness>/account.json`; harness-specific paths are
  acceptable when documented).
- The cache schema, including version bumps and migration.
- The freshness window returned by `QuotaFreshness()`.
- The probe transport (PTY, CLI, file read).
- Lock coordination per ADR-012 (per-source on-disk cache).

Service-side code MUST NOT read, write, or compute paths for these caches
directly. Operator-visible cache paths SHOULD be exposed through a single
service-level diagnostic surface (e.g. `Service.DiagnosticPaths()`) that
calls back into each harness via a documented method, rather than the
service importing per-harness path functions.

## Live Subprocess Lifecycle

CONTRACT-003 v0.15 is authoritative for accepted-session lifecycle and typed
terminal facts. This section defines the harness-side obligations needed to
satisfy it.

### One neutral lifecycle owner

`internal/processlifecycle` MUST own the platform lifecycle implementation for
every production path that can start a live child process. This includes:

- `Harness.Execute` for all six built-in subprocess harnesses;
- PTY-backed normal execution, including `claude-tui`;
- quota and account refresh probes;
- model-discovery probes, including the contextual extension;
- health checks or auxiliary commands that spawn a process; and
- adapter, recorder, or terminal helpers that start descendants.

Harness packages own argv, environment, working directory, protocol parsing,
and harness-native final data. For an accepted service invocation,
`internal/serviceimpl` MUST acquire the per-invocation lifecycle lease and make
it available to the selected harness adapter before untrusted harness code can
run. For a standalone live probe, the component that owns that probe MUST
acquire its own distinct lease. Harness packages bind their launch plans and
I/O attachments to the supplied lease; they MUST NOT start a production child
through a private `os/exec`, PTY-start, shell, or helper path that bypasses it.
Low-level platform packages MAY use `os/exec` or PTY primitives only behind
`internal/processlifecycle`.

Each accepted service invocation and each standalone live probe receives a
distinct lease and containment boundary. A runner MAY share immutable
configuration and durable cache state; it MUST NOT retain or reuse a live
process, PTY, supervisor, or containment lease across invocations. Package- or
service-scoped live session pools are forbidden. Conversation continuation
uses a new Fizeau invocation and a new containment boundary even when the
upstream provider resumes logical conversation state.

### Platform containment before execution

On Unix-like systems, `internal/processlifecycle` MUST start a lifecycle
supervisor as Fizeau's direct child. The supervisor leads a dedicated process
group or session and starts the harness as a contained descendant only after
its control channel and parent-death mechanism are active. Close or EOF on the
service control channel triggers containment shutdown. Cleanup signals the
whole group/session, escalates before the cleanup deadline, and waits until the
boundary is empty while reaping every process parented by Fizeau or the
supervisor.

On Windows, `internal/processlifecycle` MUST create the child suspended, create
a dedicated Job Object with kill-on-job-close semantics, assign the suspended
child to the Job Object, and only then resume its primary thread. If assignment
or policy configuration fails, it MUST terminate and reap the suspended child;
uncontained harness code MUST NOT run.

On any other platform, live subprocess harness execution is supported only
when an equivalent containment implementation exists. Support is checked
before process creation. An unsupported platform returns a setup failure before
spawn and creates no child, PTY, or harness event stream.

### Cleanup, terminal ownership, and deadlines

The harness implementation final event is evidence for the primary execution
tuple; it is not the public terminal fact. Fizeau service orchestration owns
classification, cleanup precedence, persistence, and creation of exactly one
public terminal event. A harness MUST NOT publish a service terminal event or
write a service `session.end` record directly.

Normal completion, harness failure, request timeout, context cancellation,
caller-signalled stream abandonment, and caller death all begin cleanup
immediately. The request context controls execution and initiates cleanup, but
MUST NOT be used as the cleanup context after cancellation. Cleanup runs under
a service-owned context bounded by `ServiceOptions.HarnessCleanupTimeout`.
`ServiceExecuteRequest.Timeout`, idle timeout, provider timeout, and probe
timeouts do not shorten or replace that cleanup deadline.

The service MUST withhold the caller-alive terminal event until the containment
boundary is empty or `HarnessCleanupTimeout` expires. Cleanup success preserves
the primary tuple. A missed deadline, indeterminate containment state, or
detected escape produces `failed / cleanup_failed / cleanup` and preserves the
primary tuple as required by CONTRACT-003. Missing, malformed, or duplicate
harness final events cannot create duplicate public terminal facts.

### Durable recovery identity

Every lease MUST expose and persist process-birth identity sufficient to reject
PID reuse. A recovery record includes the Fizeau session or probe identifier,
platform containment identifier, direct-child PID, an OS-derived birth/start
token for that exact process, and lifecycle state. PID alone, process name, and
command-line matching are not sufficient identity.

Before signalling during recovery, the reaper MUST prove that the current
process still matches the recorded birth identity and containment boundary. A
mismatch is retained as unresolved evidence and MUST NOT be signalled as if it
were the recorded process. Current-invocation cleanup starts immediately;
`StaleHarnessReaperGrace` controls only when a later startup may adopt an old
non-terminal record.

## Lifecycle Error Semantics

| Condition | Harness/service outcome | Recovery expectation |
|-----------|-------------------------|----------------------|
| Platform has no containment implementation | `Harness.Execute` returns a setup error before spawn; for an accepted service call, the service emits `failed / spawn_failed / spawn` | Choose a supported execution path. |
| Unix supervisor or control-channel setup fails | `Harness.Execute` returns a setup error before harness execution; the service owns the `spawn_failed` terminal fact | Reap any partially created supervisor and child. |
| Windows Job Object assignment fails | `Harness.Execute` returns a setup error before resume; the service owns the `spawn_failed` terminal fact | Terminate and reap the suspended child. |
| Request context is cancelled | Harness stops work; service classifies `cancelled / context_cancelled / cancellation` after cleanup | Cleanup continues under the service-owned deadline. |
| Execution or probe timeout expires | Harness stops work; service classifies `timed_out / deadline_exceeded / timeout` after cleanup | Cleanup continues under the service-owned deadline. |
| Containment is non-empty or indeterminate at cleanup deadline | Service classifies `failed / cleanup_failed / cleanup` and retains recovery ownership | Recovery reaper continues after terminalization. |
| Recorded PID has a different birth identity | Do not signal it; retain unresolved recovery evidence | Require operator diagnosis or a later identity-safe recovery path. |
| Harness final event is missing, malformed, or duplicated | Service synthesizes or retains one typed terminal fact; raw invalid finals are not forwarded as service terminals | Preserve protocol diagnostics; never emit a second terminal fact. |

## Projection to CONTRACT-003

The service layer projects harness-level types onto CONTRACT-003 public
types as follows. The projection is the only place service code converts
between layers; nothing else in the service may read fields off
`QuotaStatus` and re-emit them under different names.

| CONTRACT-003 public field | CONTRACT-004 source |
|---------------------------|---------------------|
| `service.QuotaState.Windows` | `QuotaStatus.Windows` |
| `service.QuotaState.CapturedAt` | `QuotaStatus.CapturedAt` |
| `service.QuotaState.Fresh` | `QuotaStatus.Fresh` |
| `service.QuotaState.Source` | `QuotaStatus.Source` |
| `service.QuotaState.Status` | `QuotaStatus.State` (string-cast); when `QuotaStatus.Reason` is non-empty the projection appends ` (<reason>)` so operator surfaces preserve the explanation regardless of `State` value |
| `service.QuotaState.LastError` | Non-nil only when the harness method itself returned an error (call failure). State-driven absence (`State=QuotaUnavailable` etc.) does not populate `LastError`; it populates `Status`. |
| `service.AccountStatus.Authenticated` | `AccountSnapshot.Authenticated` |
| `service.AccountStatus.Unauthenticated` | `AccountSnapshot.Unauthenticated` |
| `service.AccountStatus.Email` | `AccountSnapshot.Email` |
| `service.AccountStatus.PlanType` | `AccountSnapshot.PlanType` |
| `service.AccountStatus.OrgName` | `AccountSnapshot.OrgName` |
| `service.AccountStatus.Source` | `AccountSnapshot.Source` |
| `service.AccountStatus.CapturedAt` | `AccountSnapshot.CapturedAt` |
| `service.AccountStatus.Fresh` | `AccountSnapshot.Fresh` |
| `service.AccountStatus.Detail` | `AccountSnapshot.Detail` |
| `service.HarnessInfo.Quota` | Result of `QuotaHarness.QuotaStatus()` projected via the rows above; nil when the harness does not implement `QuotaHarness` |
| `service.HarnessInfo.Account` | Result of `AccountHarness.AccountStatus()` projected; nil when the harness does not implement `AccountHarness` |
| `service.ProviderInfo.Quota` | Same projection as `HarnessInfo.Quota` for subscription-backed providers |
| `service.ProviderInfo.Auth` | Same projection as `HarnessInfo.Account` |

Routing decisions inside the service consume `QuotaStatus.RoutingPreference`
directly. The service MUST NOT project `RoutingPreference` into the public
contract; it remains an internal routing signal.

`HarnessInfo.Quota` and `HarnessInfo.Account` projections MUST remain
backwards-compatible in JSON shape with the current CONTRACT-003 schema.
Adding fields is allowed; removing or renaming existing public fields
requires a CONTRACT-003 amendment.

## Conformance Evidence

Every harness implementation MUST carry, at minimum:

| Capability | Evidence |
|------------|----------|
| `Harness` (all six) | Unit tests for `Info()`, `HealthCheck()`, and an `Execute` happy path with one implementation final event of correct shape. |
| `QuotaHarness` (claude, claude-tui, codex, gemini) | Unit tests for `QuotaStatus` against a synthetic cache fixture (fresh and stale cases); unit tests for `RefreshQuota` against a recorded cassette per ADR-002; unit test asserting `QuotaFreshness` is the documented constant; conformance assertion that every emitted `Windows[].LimitID` value is a member of `SupportedLimitIDs()`. |
| `AccountHarness` (every implementing harness) | Unit tests for `AccountStatus` returning each documented state (Authenticated, Unauthenticated, no evidence) with correct `Fresh` against a synthetic file fixture. |
| `ModelDiscoveryHarness` (all six) | Unit tests prove `DefaultModelSnapshot` returns a non-empty model list with nil error or returns `ErrModelDiscoveryEvidenceMissing`, never an empty successful fallback. Conformance asserts that `ResolveModelAlias` resolves every family returned by `SupportedAliases()` and returns `ErrAliasNotResolvable` for an out-of-set family. Package documentation MUST enumerate the same set returned by `SupportedAliases()`. |
| `ContextModelDiscoveryHarness` (implementers) | Cancellation reaches the live probe, no background context replaces it, and the method waits for containment cleanup or the service-owned cleanup deadline before returning. |
| Live subprocess lifecycle (every spawning path) | Platform subprocess tests cover normal completion, failure, timeout, cancellation, caller-signalled abandonment, and caller death with a grandchild in the containment boundary. Static enforcement proves production child creation routes through `internal/processlifecycle`. |
| Recovery identity | Tests simulate PID reuse or a changed process-birth token and prove the recovery reaper does not signal the mismatched process. |
| Projection | Service-level test asserting CONTRACT-003 JSON shape (e.g. `HarnessInfo.Quota`) is identical before and after the harness migration — pinned to a recorded fixture. |

## Invariants

These invariants are enforced by reviewer attention and (where practical)
by `go vet`-shaped tooling:

1. **No service-side import of `internal/harnesses/<name>` symbols beyond
   `Runner{}` construction.** Permitted imports from outside the harness's
   own package and `internal/harnesses` itself: the runner constructor
   used by `internal/serviceimpl/execute_dispatch.go`. All other
   consumption MUST go through the interface methods on `Harness`,
   `QuotaHarness`, `AccountHarness`, or `ModelDiscoveryHarness`.
2. **Cache I/O is harness-owned.** No code outside
   `internal/harnesses/<name>/` reads or writes the harness's cache
   files.
3. **Concrete snapshot types are package-private.** The per-harness
   types that today are exported as `ClaudeQuotaSnapshot`,
   `CodexQuotaSnapshot`, `GeminiQuotaSnapshot`, etc., become lowercase
   (package-private). The interface contract returns `QuotaStatus`.
4. **Service routing reads only `RoutingPreference`, `State`, and
   `Windows`.** No service-side scoring rule may branch on `Detail`
   map contents or internal fields of the per-harness snapshot.
   `Windows` is the authoritative structured surface for tier or
   per-window facts.
5. **Every live child is lifecycle-owned.** All production subprocess and PTY
   starts, including probes and auxiliary helpers, go through
   `internal/processlifecycle` and acquire a distinct lease before harness code
   runs. Unsupported platforms fail before spawn.
6. **No cross-harness imports.** `claude` does not import `codex`;
   `claude-tui` does not import `claude`; etc. Shared
   helpers go in a neutral package (e.g.
   `internal/harnesses/anthropic/`).
7. **Runners are stateless wrappers around harness-owned cache files.**
   The `Runner` struct MAY hold immutable configuration (binary path,
   discovery cache pointer) and lock handles; it MUST NOT hold mutable
   quota or account state that would diverge across instances, and no runner,
   package singleton, or service object may pool a live process, PTY,
   supervisor, or lifecycle lease across accepted invocations. Two
   `&Runner{}` instances of the same harness MUST observe identical
   `QuotaStatus`/`AccountSnapshot` results for the same cache state.
   The service MAY hold a single registered instance per harness or
   construct fresh instances per call; both patterns MUST produce
   equivalent observable behavior.
8. **`RefreshQuota` and `RefreshAccount` are single-flight per harness
   instance, mediated by the harness's on-disk cache lock (ADR-012).**
   Concurrent callers block on the lock; once the in-flight refresh
   completes, queued callers observe the new cached state. Probe
   failure surfaces in the returned status's `State` field, not as an
   error return.
9. **Errors are reserved for call failure.** `QuotaStatus`,
   `RefreshQuota`, `AccountStatus`, and `RefreshAccount` return an
   error ONLY for context cancellation, lock acquisition failure, or
   unrecoverable IO error. Absence of evidence, missing auth, blocked
   quota, and probe failures are reported as state on a valid
   returned value. This makes consumer code uniform across success and
   failure surfaces.
10. **The service owns terminalization.** Harness implementation finals are
    primary evidence only. The service waits for cleanup success or deadline,
    applies cleanup precedence, emits at most one public terminal fact, and
    writes the service-owned terminal projection.
11. **Recovery is identity-safe.** Lifecycle records carry containment and
    process-birth identity. No current process is signalled from a stale PID
    alone.

## Non-Goals

- Refactoring CONTRACT-003 public types. Their shapes stay the same;
  only their *source* changes from per-harness exports to interface
  projection.
- Generalizing the `HarnessConfig` registry struct. Subprocess-specific
  fields (`BaseArgs`, `PermissionArgs`, `ModelFlag`) remain on the
  registry struct; they are configuration, not part of the interface.
- Introducing a plugin loader or out-of-tree harness support. Harnesses
  remain in-tree under `internal/harnesses/`.
- Defining `claude-tui` product or billing behavior. ADR-013 owns that accepted
  harness identity; this contract applies the same lifecycle rules to it as to
  the other five built-in subprocess harnesses.
- Maintaining warm live harness sessions between invocations. Durable caches
  and provider-native continuation evidence may be reused; live processes and
  containment leases may not.

## Acceptance Criteria

1. `internal/harnesses/types.go` declares `Harness`, `QuotaHarness`,
   `AccountHarness`, `ModelDiscoveryHarness`, and
   `ContextModelDiscoveryHarness` interfaces with the signatures above;
   `DefaultModelSnapshot` returns `(ModelDiscoverySnapshot, error)`.
2. `QuotaStatus`, `AccountSnapshot`, `QuotaStateValue`,
   `RoutingPreference`, `ErrAliasNotResolvable`, and
   `ErrModelDiscoveryEvidenceMissing` are declared in
   `internal/harnesses/types.go`.
3. The six built-in subprocess implementations (`claude`, `claude-tui`,
   `codex`, `gemini`, `opencode`, and `pi`) satisfy `Harness` plus their
   documented optional sub-interfaces.
4. No `.go` file outside `internal/harnesses/` imports a symbol from
   `internal/harnesses/claude`, `internal/harnesses/codex`,
   `internal/harnesses/claude-tui`, `internal/harnesses/gemini`,
   `internal/harnesses/opencode`, or `internal/harnesses/pi` other than the
   documented runner-construction seam. A linter check enforces this.
5. Public CONTRACT-003 JSON shapes for `HarnessInfo`, `ProviderInfo`,
   `QuotaState`, and `AccountStatus` are byte-identical (modulo
   intentionally added fields) to pre-refactor fixtures.
6. Per-harness `*QuotaSnapshot` types are lowercased (package-private).
7. Per-harness `*QuotaRoutingDecision` types are removed in favor of
   `QuotaStatus.RoutingPreference`.
8. Per-harness cache I/O functions (`Read*Quota`, `Write*Quota`,
   `*QuotaCachePath`) are unexported.
9. Conformance evidence (above) lands for all six built-in subprocess
   harnesses.
10. CONTRACT-003 cross-references CONTRACT-004 as the source for
    `HarnessInfo.Quota` and `HarnessInfo.Account` population.
11. Every production live-child path uses `internal/processlifecycle`; static
    checks reject direct bypasses, and Unix and Windows subprocess fixtures
    prove containment is established before harness execution.
12. Caller-alive terminal delivery waits for cleanup success or
    `HarnessCleanupTimeout`; caller-death and PID-reuse fixtures prove control
    channel cleanup, best-effort recovery, and identity-safe signalling.
13. Structural tests reject any cross-invocation live process or PTY pool,
    including package-scope pools in `claude-tui`.

## References

- [CONTRACT-003 Fizeau Service Interface](./CONTRACT-003-fizeau-service.md)
- [ADR-002 PTY Cassette Transport](../adr/ADR-002-pty-cassette-transport.md)
- [ADR-011 Cost-Based Routing With Quota Pools](../adr/ADR-011-cost-based-routing-with-quota-pools.md)
- [ADR-012 Per-Source On-Disk Cache](../adr/ADR-012-per-source-on-disk-cache.md)
- [ADR-013 claude-tui PTY Harness](../adr/ADR-013-claude-tui-pty-harness-fork.md)
- [ADR-014 Universal Harness Interface](../adr/ADR-014-universal-harness-interface.md)
- [Primary Harness Capability Baseline](../primary-harness-capability-baseline.md)
- [Implementation plan: harness interface refactor](../plan-2026-05-14-harness-interface-refactor.md)
