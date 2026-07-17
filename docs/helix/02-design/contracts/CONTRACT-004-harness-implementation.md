---
ddx:
  id: CONTRACT-004
  depends_on:
    - CONTRACT-003
    - SD-006
  child_of: fizeau-67f2d585
  review:
    self_hash: d573fcd5f4a3335b36f4a858095150a25745a52e3ae177031b1f9d70d008d818
    deps:
      CONTRACT-003: 4b053cfd0c66bafac8dedbda6c6d32b724f27ec2db13ec2eff37a9b6683dbb24
      SD-006: bd9f4cf464dbad08e003533906b67eb25735384eac4d522e367adccc9a3a7db6
    reviewed_at: "2026-07-17T06:25:57Z"
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
- The API-neutral `Event` bridge used by serviceimpl, including the
  service-owned `context_capacity` payload that passes through the neutral
  `internal/harnesses` layer without becoming harness-owned behavior.
- Cache and refresh ownership.
- Optional harness-native conversation continuation behind a Fizeau-session-ID
  boundary, including completed-session resolution and private evidence
  ownership.
- Optional harness-owned portable-runtime asset discovery behind an
  API-neutral target and asset-closure boundary.
- Live subprocess containment, cleanup, and recovery obligations shared by
  normal execution, PTY sessions, and auxiliary probes.
- Conformance evidence requirements.
- Projection rules from internal harness types to the CONTRACT-003 public
  types (`QuotaState`, `AccountStatus`, `HarnessInfo`, `ProviderInfo`, and
  `ServiceContextCapacityData`).

Out of scope:

- The CLI/network behavior of any specific harness binary (covered by
  primary-harness-capability-baseline.md and per-harness specs).
- The cassette/replay transport contract (ADR-002).
- The public service contract (CONTRACT-003).
- Core capacity estimation, selected-route enforcement, and public capacity
  terminal semantics (SD-006 and CONTRACT-003). This contract defines only the
  neutral event bridge and its harness-side authority boundary.
- Public continuation policy selection, fallback, child-lineage projection,
  and validation. CONTRACT-003 owns those service behaviors; this contract
  owns only the optional route capability and its private evidence boundary.
- Public portable-runtime request/bundle types, destination materialization,
  and OCI orchestration. CONTRACT-003 owns the public behavior; this contract
  owns only per-harness asset and inherited-environment discovery.

## Interface Set

All six built-in subprocess harness implementations (`claude`, `claude-tui`,
`codex`, `gemini`, `opencode`, and `pi`) MUST implement `Harness`. A harness
MAY additionally
implement any of `QuotaHarness`, `AccountHarness`, and
`ModelDiscoveryHarness`; a cancellation-aware discovery implementation MAY
also implement `ContextModelDiscoveryHarness`. A route that can resume
harness-native conversation state MAY implement `ContinuationHarness`. A
subprocess harness instance that is structurally capable of unpinned execution
within a portable runtime MUST implement `PortableRuntimeHarness`; other
subprocess harnesses MAY implement it to support future inclusion. The service
uses Go interface assertions to discover which optional contracts a harness
satisfies and consumes them through the interface only.

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

// PortableRuntimeTarget is the internal Linux same-platform preparation target.
// CONTRACT-003 v0.15 requires GOOS="linux" and both values to equal the
// preparing process.
type PortableRuntimeTarget struct {
    GOOS   string
    GOARCH string
}

// PortableRuntimeFileIdentity is the exact content identity supplied by a
// harness contributor for one regular runtime file. Publisher, release,
// build, package-integrity, and offline-probe evidence remain contributor-
// owned and do not cross this neutral helper boundary.
type PortableRuntimeFileIdentity struct {
    Size          int64
    ContentSHA256 string
}

type PortableRuntimeAssetKind string
type PortableRuntimePathKind string
type PortableRuntimeClosureClass string
type PortableRuntimeGuestPathScope string
type PortableRuntimeEnvironmentConstraintKind string

const (
    PortableRuntimeAssetExecutable  PortableRuntimeAssetKind = "executable"
    PortableRuntimeAssetInstallTree PortableRuntimeAssetKind = "install_tree"
    PortableRuntimeAssetConfig      PortableRuntimeAssetKind = "config"
    PortableRuntimeAssetCredential  PortableRuntimeAssetKind = "credential"
    PortableRuntimeAssetQuota       PortableRuntimeAssetKind = "quota"
    PortableRuntimeAssetCache       PortableRuntimeAssetKind = "cache"
    PortableRuntimeAssetSupport     PortableRuntimeAssetKind = "runtime_support"

    PortableRuntimePathFile PortableRuntimePathKind = "file"
    PortableRuntimePathTree PortableRuntimePathKind = "tree"

    PortableRuntimeClosureStatic      PortableRuntimeClosureClass = "static"
    PortableRuntimeClosureDynamic     PortableRuntimeClosureClass = "dynamic"
    PortableRuntimeClosureInterpreted PortableRuntimeClosureClass = "interpreted"

    PortableRuntimeGuestPathRuntime PortableRuntimeGuestPathScope = "runtime"
    PortableRuntimeGuestPathHome    PortableRuntimeGuestPathScope = "home"
    PortableRuntimeGuestPathConfig  PortableRuntimeGuestPathScope = "config"
    PortableRuntimeGuestPathData    PortableRuntimeGuestPathScope = "data"
    PortableRuntimeGuestPathCache   PortableRuntimeGuestPathScope = "cache"
    PortableRuntimeGuestPathState   PortableRuntimeGuestPathScope = "state"
    PortableRuntimeGuestPathTmp     PortableRuntimeGuestPathScope = "tmp"

    PortableRuntimeEnvironmentFixedTrue  PortableRuntimeEnvironmentConstraintKind = "fixed_true"
    PortableRuntimeEnvironmentFixedFalse PortableRuntimeEnvironmentConstraintKind = "fixed_false"
    PortableRuntimeEnvironmentGuestPath  PortableRuntimeEnvironmentConstraintKind = "guest_path"
    PortableRuntimeEnvironmentUnset      PortableRuntimeEnvironmentConstraintKind = "unset"
    PortableRuntimeEnvironmentRuntimePath PortableRuntimeEnvironmentConstraintKind = "runtime_path"
)

// PortableRuntimeAsset is one harness-owned member of a verified executable
// or state closure. Source remains internal and may be sensitive. Target is a
// clean slash-relative path below CONTRACT-003's platform-fixed guest root,
// never a host path.
type PortableRuntimeAsset struct {
    Kind          PortableRuntimeAssetKind
    PathKind      PortableRuntimePathKind
    Source        string
    Target        string
    ContentSHA256 string
    Executable    bool
}

// PortableRuntimeLaunch is a guest-relative executable recipe. Every target is
// slash-relative beneath CONTRACT-003's fixed guest root.
type PortableRuntimeLaunch struct {
    EntrypointTarget     string
    EntrypointTreeMember string
    InterpreterTarget    string
    LoaderTarget         string
    RuntimeArgs          []string
    LibraryRootTargets   []string
}

// PortableRuntimeEnvironment carries an inherited variable name only. It
// cannot represent a value or a name=value assignment.
type PortableRuntimeEnvironment struct {
    Name string
}

// PortableRuntimeGuestPath identifies a path beneath one activation-owned
// guest root. Target is slash-relative and never carries a host path.
type PortableRuntimeGuestPath struct {
    Scope  PortableRuntimeGuestPathScope
    Target string
}

// PortableRuntimeEnvironmentConstraint declares one activation-owned
// environment treatment without carrying a raw environment value.
type PortableRuntimeEnvironmentConstraint struct {
    Name      string
    Kind      PortableRuntimeEnvironmentConstraintKind
    GuestPath PortableRuntimeGuestPath
}

// PortableRuntimeFixedOptionValue declares one fixed option followed by one
// fixed, non-secret literal value. It cannot represent a free positional
// argument or an option=value assignment.
type PortableRuntimeFixedOptionValue struct {
    Option string
    Value  string
}

// PortableRuntimeStateProjectionEntry maps one exact declared asset into a
// member below an activation-owned directory. Neither field carries a value or
// a host path.
type PortableRuntimeStateProjectionEntry struct {
    AssetTarget string
    Target      string
}

// PortableRuntimeStateProjection assembles immutable configuration and
// writable state seeds at one native harness directory. Activation owns the
// enforcement boundary between those members.
type PortableRuntimeStateProjection struct {
    Directory PortableRuntimeGuestPath
    Entries   []PortableRuntimeStateProjectionEntry
}

// PortableRuntimeExecutionConstraints is harness-declared execution evidence.
// Activation interprets it generically after materialization persists it.
type PortableRuntimeExecutionConstraints struct {
    Environment         []PortableRuntimeEnvironmentConstraint
    ReadOnlyPaths       []PortableRuntimeGuestPath
    RequiredAbsentPaths []PortableRuntimeGuestPath
    FixedArguments      []string
    FixedOptionValues   []PortableRuntimeFixedOptionValue
}

type PortableRuntimeContribution struct {
    ClosureClass         PortableRuntimeClosureClass
    Launch               PortableRuntimeLaunch
    Assets               []PortableRuntimeAsset
    Environment          []PortableRuntimeEnvironment
    ExecutionConstraints PortableRuntimeExecutionConstraints
    StateProjections     []PortableRuntimeStateProjection
}

// PortableRuntimeHarness is the optional harness-owned asset-discovery
// capability. It is read-only: it does not copy files, select a route, create a
// session, contact a provider, or start a process.
type PortableRuntimeHarness interface {
    Harness

    PortableRuntimeAssets(context.Context, PortableRuntimeTarget) (PortableRuntimeContribution, error)
}

// ContinuationRequest asks the selected route-owned runner to resume the
// completed Fizeau session identified by ParentSessionID. ParentSessionID is
// the sole conversation identifier on this boundary. Request is the fully
// normalized execution request for the new child invocation; it carries no
// public ContinuationPolicy and MUST NOT carry a provider- or harness-native
// conversation ID, resume token, or opaque continuation evidence.
type ContinuationRequest struct {
    ParentSessionID string
    Request ExecuteRequest
}

// PreparedContinuation is a single-use, route-private prepared resume. Start
// follows the event-channel and setup-error rules of Harness.Execute. The
// service calls Start only after it has created the child Fizeau session and
// acquired that child's fresh lifecycle lease.
type PreparedContinuation interface {
    Start(context.Context) (<-chan Event, error)
}

// ContinuationHarness is the optional route-specific continuation capability.
// PrepareContinuation resolves and binds ParentSessionID through durable
// evidence privately owned by this registered runner. It MUST return
// ErrContinuationEvidenceUnavailable without creating a child session,
// acquiring a child lease, spawning, or returning an event channel when usable
// evidence is absent.
type ContinuationHarness interface {
    Harness

    PrepareContinuation(context.Context, ContinuationRequest) (PreparedContinuation, error)
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

// EventType is an additive API-neutral union. Consumers preserve unknown
// values. Not every native or subprocess backend originates every event type;
// service-owned event types may pass through this neutral envelope.
type EventType string

// EventTypeContextCapacity is the API-neutral internal bridge for the
// service-owned context-capacity decision defined by CONTRACT-003 and SD-006.
// Harness-native parsers never originate this event type.
const EventTypeContextCapacity EventType = "context_capacity"

// ContextCapacityData preserves the complete core capacity decision while it
// crosses internal/serviceimpl. Harness-native parsers do not construct it.
type ContextCapacityData struct {
    Action                 string `json:"action"`
    CallKind               string `json:"call_kind"`
    TurnIndex              int    `json:"turn_index"`
    AttemptIndex           int    `json:"attempt_index"`
    ContextWindow          int    `json:"context_window"`
    EffectiveContextWindow int    `json:"effective_context_window"`
    EstimatedInputTokens   int    `json:"estimated_input_tokens"`
    RequestedMaxTokens     int    `json:"requested_max_tokens"`
    EffectiveMaxTokens     int    `json:"effective_max_tokens"`
    AvailableOutputTokens  int    `json:"available_output_tokens"`
}

const TerminalCauseContextCapacityExceeded TerminalCause = "context_capacity_exceeded"

// Relevant additive FinalData field; all existing fields remain unchanged.
type FinalData struct {
    // ...existing fields...
    ContextCapacity *ContextCapacityData `json:"context_capacity,omitempty"`
}

// Sentinel errors for interface methods. Note: absence of quota or
// account evidence is NOT an error — it is reported via State or
// Authenticated/Unauthenticated fields on a valid returned value.
// Errors are reserved for call failure.
var (
    ErrAliasNotResolvable = errors.New("model alias not resolvable from snapshot")
    ErrModelDiscoveryEvidenceMissing = errors.New("model discovery evidence missing")
    ErrContinuationRequestInvalid = errors.New("invalid continuation request")
    ErrContinuationEvidenceUnavailable = errors.New("continuation evidence unavailable")
    ErrPortableRuntimeTargetUnsupported = errors.New("portable runtime target unsupported")
    ErrPortableRuntimeClosureIncomplete = errors.New("portable runtime asset closure incomplete")
)
```

`ErrContinuationRequestInvalid` covers an empty `ParentSessionID` or a
non-normalized child `Request`; service orchestration MUST prevent this error
by validating before capability invocation. `ErrContinuationEvidenceUnavailable`
means the route implements `ContinuationHarness` but its private evidence for
the parent is absent, unreadable, stale, or no longer accepted.
`PrepareContinuation` MUST detect this condition without session creation,
lease acquisition, spawn, or events. The service maps it to CONTRACT-003's
unsupported-capability policy behavior: `require_resume` returns
`ErrContinuationUnsupported`, while `prefer_resume` may take the fresh path.
After preparation succeeds, native rejection, evidence invalidation, or any
other `PreparedContinuation.Start` failure belongs to the already-created child
and MUST NOT trigger a second fresh attempt.

`ErrPortableRuntimeTargetUnsupported` reports a target GOOS/GOARCH that the
contributor cannot package; v0.15 contributors require Linux and reject any
target different from the preparing process. `ErrPortableRuntimeClosureIncomplete` reports an
installed launcher whose complete executable or install-tree dependency
closure cannot be verified. Both errors are stable classes with redacted
detail: error text MUST NOT contain credential contents, environment values,
or account-bearing source paths. The service treats either error from an
installed structurally included subprocess as an atomic preparation failure
rather than silently omitting that structural candidate.

`QuotaWindow`, `ModelDiscoverySnapshot`, `Event`, `ExecuteRequest`,
`ContinuationRequest`, `PreparedContinuation`, `PortableRuntimeTarget`,
`PortableRuntimeAssetKind`, `PortableRuntimePathKind`,
`PortableRuntimeClosureClass`, `PortableRuntimeAsset`, `PortableRuntimeLaunch`,
`PortableRuntimeEnvironment`, `PortableRuntimeGuestPathScope`,
`PortableRuntimeGuestPath`, `PortableRuntimeEnvironmentConstraintKind`,
`PortableRuntimeEnvironmentConstraint`, `PortableRuntimeExecutionConstraints`,
`PortableRuntimeStateProjectionEntry`, `PortableRuntimeStateProjection`,
`PortableRuntimeContribution`, `HarnessInfo`,
`EventType`, and `ContextCapacityData` retain or receive their definitions in
`internal/harnesses/types.go` as specified here.

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

Portable preparation is the one additional neutral consumer of harness-owned
paths. It obtains those paths only from `PortableRuntimeHarness` contributions;
service and materializer code MUST NOT reproduce `~/.codex`, `~/.claude`,
`~/.gemini`, XDG-state, or harness-specific cache rules. A contribution may
classify an absent optional cache as absent, but it cannot hide a required
credential or executable closure from an otherwise eligible surface.

## Portable Runtime Asset Ownership

`PortableRuntimeHarness` describes assets; it does not materialize them. The
harness package owns discovery of its executable/install closure, credentials,
config, quota state, cache state, required runtime support, and inherited
environment names. It also owns declaration of execution constraints required
to keep that closure closed. The neutral materializer owns generic validation,
canonical private persistence, copying, permissions, staging, rollback,
public-plan projection, and cleanup. Activation owns generic enforcement of the
persisted constraints. Service and activation code MUST NOT reproduce an
OpenCode-, Claude-, Codex-, Gemini-, or other harness-specific rule.

### Portable runtime execution constraints

The portable process environment is closed-world. Activation starts from an
empty environment, constructs the finite Fizeau baseline below, copies only the
names in the contribution's inherited `Environment` allowlist, then applies
`ExecutionConstraints.Environment`. A name has exactly one effective mode.
An inherited name MUST NOT also have a typed constraint, and baseline names
MUST NOT be inherited. Host variables absent from the allowlist and typed rules
remain absent.

| Baseline name | Activation-owned semantic value |
|---|---|
| `HOME` | Root of the generated `home` scope. |
| `PATH` | `runtime_path`: the stable, deduplicated, lexical list of guest parent directories containing declared owner-executable assets, followed by the fixed guest tool directories `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`. Arbitrary host PATH segments are never retained. |
| `USER`, `LOGNAME` | The fixed non-secret runtime identity `fizeau`. |
| `SHELL` | The fixed guest tool path `/bin/sh`. |
| `TERM` | The fixed generated terminal type `xterm-256color`. |
| `LANG`, `LC_ALL` | The fixed generated locale `C.UTF-8`. |
| `XDG_CONFIG_HOME` | Root of the generated `config` scope. |
| `XDG_DATA_HOME` | Root of the generated `data` scope. |
| `XDG_CACHE_HOME` | Root of the generated `cache` scope. |
| `XDG_STATE_HOME` | Root of the generated `state` scope. |
| `XDG_RUNTIME_DIR` | `runtime` child beneath the generated `tmp` scope. |
| `TMPDIR` | Root of the generated `tmp` scope. |

An unprojected credential, quota, or cache asset is a prefix-preserving seed.
Its target MUST begin with `data/`, `state/`, or `cache/`; activation copies it
with owner-only permissions into the matching generated scope before process
start and preserves the suffix after that prefix exactly. It never points an
XDG variable at the read-only runtime mount merely to expose a seed. This lets
a contributor declare `data/opencode/auth.json` while OpenCode remains free to
create sibling log and database files under its generated data directory.

`StateProjections` governs the distinct case where a harness's native directory
must contain both immutable configuration and writable credential, quota, or
cache state. `Directory` MUST have a non-empty clean target in exactly one of
the activation-owned `home`, `config`, `data`, `cache`, or `state` scopes;
`runtime` and `tmp` are invalid. Each entry maps one exact declared
`AssetTarget` to one non-empty clean slash-relative member `Target` below that
directory. A projection MUST contain at least one `config` asset and at least
one `credential`, `quota`, or `cache` asset. Asset `Kind` is the sole
mutability authority: executable, install-tree, and runtime-support assets are
invalid projection members. One asset target may be consumed by at most one
projection entry and is not also copied by the prefix-preserving seed rule.

Projection directories MUST be pairwise disjoint under exact, ancestor, and
descendant comparison. Concrete projected outputs MUST separately be pairwise
disjoint under the same comparison. A
`RequiredAbsentPaths` entry may name a generated sibling inside a projection
directory, but it MUST remain disjoint from every concrete projected output.
Normalization deep-copies the projection and entry slices, sorts projections
by `Directory.Scope` then `Directory.Target`, and sorts entries by `Target`
then `AssetTarget`. It is metadata-only: it MUST NOT inspect, follow, or stat
an asset source. Contributor discovery verifies the original source, and the
materializer revalidates source identity, content, file type, and symlink
absence while copying and persists the normalized projection.

Activation MUST assemble each projection through a filesystem or mount-
namespace enforcement boundary. The projection directory's identity MUST
resist unlink, rename, replacement, and shadowing by the harness UID. Every
`config` member MUST resist write, unlink, rename, replacement, and shadowing,
while credential/quota/cache members remain refreshable and declared lock files
plus unprojected siblings remain creatable in the directory. Owner-only or
read-only mode bits on ordinary files inside a writable parent are
insufficient: that parent would still permit unlink, rename, replacement, or
shadowing. Activation therefore MUST make the projection root and immutable
members namespace-owned boundaries while selectively exposing writable state
members and sibling space; it MUST verify these properties before process
start. Configuration assets outside a state projection remain read-only
runtime assets and do not use either state-seed rule.

Typed environment treatments have these exact meanings:

| `Kind` | Required fields | Activation behavior |
|---|---|---|
| `fixed_true` | Valid `Name`; zero `GuestPath` | Generate the ASCII boolean `true`. |
| `fixed_false` | Valid `Name`; zero `GuestPath` | Generate the ASCII boolean `false`. |
| `guest_path` | Valid `Name` and typed `GuestPath` | Generate the absolute guest path for `Scope` plus `Target`. An empty `Target` means the scope root and is valid only here. |
| `unset` | Valid `Name`; zero `GuestPath` | Ensure the name is absent. |
| `runtime_path` | `Name` exactly `PATH`; zero `GuestPath` | Regenerate the baseline search path above without accepting a colon-delimited value. |

For baseline overrides, `PATH` accepts only `runtime_path`;
`USER`/`LOGNAME`/`SHELL`/`TERM`/`LANG`/`LC_ALL` accept only `unset`; and
`HOME`, the XDG names, and `TMPDIR` accept only `guest_path` or `unset`. Other valid names may use
`fixed_true`, `fixed_false`, `guest_path`, or `unset`. The schema contains no
environment value, raw assignment, source selector, or free-form absolute
path. A `guest_path` in the `runtime` scope MUST name an exact declared asset
or an ancestor of a declared asset tree or read-only tree. This permits, for example,
`XDG_CONFIG_HOME={runtime,config}` only when a declared config tree such as
`{runtime,config/opencode}` backs that path.

`PortableRuntimeGuestPath.Scope` has exactly seven values:

| Scope | Root owner and mutability |
|---|---|
| `runtime` | The immutable mounted portable-runtime tree. |
| `home` | Activation-owned private home root. |
| `config` | Activation-owned private configuration root. |
| `data` | Activation-owned private data root. |
| `cache` | Activation-owned private cache overlay root. |
| `state` | Activation-owned private state overlay root. |
| `tmp` | Activation-owned private temporary root. |

Every non-empty `Target` is a clean slash-relative path: absolute paths,
traversal, backslashes, NUL, and a work-directory scope are invalid.
`ReadOnlyPaths` accepts only `runtime` paths backed by the exact target of a
declared `Kind=config`, `PathKind=tree` asset. A file, cache/quota asset, or
writable-state overlay cannot satisfy the rule. The declaration represents the
required policy; it does not certify `W_OK` denial. Activation MUST enforce the
read-only boundary generically, and required OCI conformance MUST prove that
the runtime user cannot write it or run an installer through it.

`RequiredAbsentPaths` accepts only the immutable/generated scopes above. Each
path MUST be disjoint from every declared asset, read-only path, explicitly
generated `guest_path`, concrete state-projection output, and other
required-absent path under exact, ancestor, and descendant comparison. The
containing writable state-projection directory is not itself a conflict, so a
contributor can forbid an undeclared generated sibling. Activation MUST verify every declared path
immediately before every harness process start, including the first; an earlier
launch creating a path in a writable generated scope therefore prevents every
later launch until the runtime is destroyed. Fizeau's activation and service
code MUST NOT create a declared path between validation and process start.

Environment constraints sort by `Name`; read-only and required-absent paths
sort by `Scope`, then `Target`. Duplicate or conflicting rules fail with
`ErrPortableRuntimeClosureIncomplete`. Errors identify only rule classes and
indexes. `FixedArguments` remains in contributor order and contains unique
boolean long-option tokens with the exact grammar
`--[a-z][a-z0-9]*(?:-[a-z0-9]+)*`. It carries no positional value, option
value, assignment, or path. `FixedOptionValues` is a distinct contributor-
ordered sequence of typed `{Option, Value}` pairs. In v0.15 the only governed
short option is Gemini's `-e`; other short options fail closed because their
semantics cannot be distinguished from route selectors. A long option uses the
same grammar as `FixedArguments`; a value is a lowercase non-secret literal with the grammar
`[a-z][a-z0-9]*(?:-[a-z0-9]+)*`. Assignments, slashes, backslashes, path-like
values, account/model/policy/provider/route/endpoint/surface selectors,
secret-like values, duplicate options, and an option
that conflicts with a fixed flag fail closed. Short and long forms with the
same name, such as `-e` and `--e`, conflict. Validation never includes either
the rejected option or value in diagnostics.

The fixed launch prefix is constructed deterministically after the complete
executable/loader/interpreter recipe: first every `FixedArguments` token in
contributor order, then each `FixedOptionValues` entry as adjacent `Option`,
`Value` tokens in contributor order, then registry arguments and request-
derived harness arguments. Neither a registry nor request can move, replace,
or interleave any part of that prefix. No free positional argument, route
selector, secret, environment assignment, or placeholder is valid.

A PATH result or final symlink target is not automatically a complete
executable closure. Each subprocess contribution declares one supported
`ClosureClass`, one `Launch` recipe, and a content-addressed asset set. Every
launch and asset target is slash-relative beneath CONTRACT-003's fixed Linux
guest root and never repeats a discovered host path. `ContentSHA256` is the
ordinary file digest for `PathKind=file`; for `PathKind=tree` it is the digest
of a canonical sorted manifest of relative path, declared type, owner
permission bits, and per-file content digest. The materializer recomputes these
identities while copying and again before commit.

The supported v0.15 closure classes are mechanically distinct:

| Class | Required discovery and evidence |
|-------|---------------------------------|
| `static` | Resolve every launcher symlink to a regular Linux executable, verify target GOARCH and absence of an ELF interpreter, and include any data files required by the offline probe. `EntrypointTreeMember`, `InterpreterTarget`, `LoaderTarget`, `RuntimeArgs`, and `LibraryRootTargets` are empty. |
| `dynamic` | Resolve the Linux ELF entrypoint, its ELF interpreter, and recursive shared libraries into private search roots. Ordinary `DT_NEEDED` closure members are individual digest-bound files; a complete tree is used only when verified runtime lookup needs more than those exact files. `LoaderTarget` names the bundled loader, `LibraryRootTargets` names every private search root that contains an emitted member, and `EntrypointTreeMember`/`InterpreterTarget`/`RuntimeArgs` are empty. Unknown `dlopen`/plugin/runtime lookup behavior is incomplete unless its additional runtime surface is included and its offline probe passes. A recognized single-file runtime may instead use verified-exact lookup only when its contributor-owned offline probe proves that startup loads no executable or library code beyond the exact dependency assets and discovery rejects every enabled plugin, hook, helper, wrapper, MCP server, marketplace, workflow, or external-path setting. Declared credential, configuration, quota, and cache assets remain ordinary state reads and do not weaken that code-closure claim. |
| `interpreted` | Include the launcher/script, the contributor-selected interpreter with its required `PortableRuntimeFileIdentity`, the interpreter's own static/dynamic closure, package-tree root, and runtime data required by the offline probe. `InterpreterTarget` names the bundled interpreter and `RuntimeArgs` contains only fixed non-secret interpreter arguments. A standalone entrypoint leaves `EntrypointTreeMember` empty. A package-tree-owned entrypoint stores its exact clean slash-relative member path in `EntrypointTreeMember`; no overlapping duplicate entrypoint asset is emitted. If the interpreter is dynamic, `LoaderTarget` and `LibraryRootTargets` describe its loader closure; both are empty for a static interpreter. A copied JavaScript/Python/shell launcher without that closure is incomplete. |

`PortableRuntimeInterpretedClosureRequest.InterpreterIdentity` has type
`PortableRuntimeFileIdentity` and is required. `Size` MUST be greater than
zero. `ContentSHA256` MUST contain exactly 64 lowercase hexadecimal
characters. A zero or malformed identity fails with
`ErrPortableRuntimeClosureIncomplete` before interpreter filesystem
inspection.

The neutral interpreted-closure analyzer selects an interpreter exclusively
from the caller-supplied `InterpreterSource`. It MUST NOT search `PATH`, run a
command, or use the launcher's shebang to select an interpreter. It still
traverses the selected interpreter's `PT_INTERP` loader and contributor-
declared library roots when the selected ELF is dynamic.

`PortableRuntimeInterpretedClosureRequest.EntrypointPackageTreeTarget` is the
only opt-in to a tree-owned entrypoint. It MUST equal exactly one declared
`PackageTrees` target. The analyzer resolves `EntrypointSource`, proves that it
is a direct non-symlink regular member of that captured tree, derives its clean
slash-relative member path, and requires
`EntrypointTarget == path.Join(EntrypointPackageTreeTarget, member)`. The
emitted launch stores the derived member in `EntrypointTreeMember`, not the
tree target. Normalization then derives exactly one owning non-overlapping
`install_tree` asset for which
`path.Join(asset.Target, EntrypointTreeMember) == EntrypointTarget` and
revalidates the source member as a non-symlink regular file. Missing ownership,
multiple ownership, target-only drift, member-only drift, and tree-owner drift
are incomplete. When `EntrypointTreeMember` is empty, the legacy standalone
entrypoint rule still requires an exact declared file asset.

An initially supplied symlink chain resolves to one regular final target. The
analyzer opens that resolved target once, verifies path metadata against
`descriptor.Stat`, and parses the same-target owner-executable ELF from that
descriptor. It retains the descriptor through closure traversal, computes
`Size` and SHA-256 from the same still-open descriptor, then re-stats the
descriptor and resolved path. Identity, file mode, size, and modification time
MUST remain unchanged. The emitted interpreter asset uses the
descriptor-derived digest. A mismatch fails with
`ErrPortableRuntimeClosureIncomplete` without exposing the expected or actual
path, size, digest, or file bytes.

The exact file identity is necessary but not sufficient contributor evidence.
The contributing harness separately binds the accepted identity to the named
install form, publisher-authenticated release, version, build, package
integrity, and offline probe. The neutral analyzer MUST NOT infer or create
that evidence from the locally installed interpreter.

Interpreted contributors MAY declare runtime-loadable native Node addons through
the following internal-only surface:

```go
type PortableRuntimeNativeAddon struct {
    PackageTreeTarget string
    RelativePath      string
    Identity          PortableRuntimeFileIdentity
}

type PortableRuntimeInterpretedClosureRequest struct {
    // Existing fields omitted.
    NativeAddons []PortableRuntimeNativeAddon
}
```

`PackageTreeTarget` MUST equal exactly one declared `PackageTrees` target.
`RelativePath` MUST be a unique, clean, non-empty slash-relative path with no
dot, dot-dot, absolute, backslash, or NUL component and a basename ending in
`.node`. `Identity` follows the exact `PortableRuntimeFileIdentity` rules
above. Invalid, duplicate, unknown, or ambiguous declarations fail before the
analyzer accesses an addon member. A nil or empty declaration set is valid.
The contributing harness owns evidence that the supplied set is exhaustive for
its accepted package layout and code paths, including credential-free offline
positive and missing-library probes. The neutral analyzer MUST validate exactly
the supplied set and MUST NOT discover addons by scanning `.node` files;
unselected foreign-platform prebuilds remain opaque package-tree content.

Native addons are valid only with a dynamic interpreted closure that declares
an explicit loader and library roots. For each declaration, the analyzer MUST
capture the owning package-tree manifest, retain a descriptor for that exact
regular directory, traverse every relative-path component with Linux
root-anchored no-follow semantics, and parse and hash one retained regular-file
descriptor. Intermediate and final symlinks are invalid. Before success, the
root descriptor, member descriptor, manifest record, and a newly captured full
tree snapshot MUST agree on file identity, mode, size, modification time, and
content digest. Replacement between initial snapshot and descriptor open, or
between descriptor inspection and final snapshot verification, fails with
`ErrPortableRuntimeClosureIncomplete`.

Each selected addon MUST be a same-target Linux `ET_DYN` file with no
`PT_INTERP` and no `DF_1_PIE`. `DT_SONAME` is absent or occurs exactly once as
the non-empty basename of `RelativePath`; malformed, empty, duplicate, or
mismatched values are incomplete. The request's `RuntimeLookup` policy applies
to every addon and recursive dependency. `RPATH`, `RUNPATH`, `AUDIT`,
`DEPAUDIT`, `FILTER`, and `AUXILIARY` metadata are always incomplete, and a
closed lookup rejects runtime-lookup symbols. Addon `DT_NEEDED` closure merges
with interpreter and loader closure before exact-root pruning. Shared
dependency files deduplicate only when the same source identity maps to the
same guest target; different sources claiming one target are ambiguous even
when their bytes match. One narrower loader rule applies after recursive
closure merge: when an exact-root resolved dependency's canonical source and
file identity equal the already selected explicit `LoaderTarget` source, the
analyzer omits that redundant library-root alias because the loader is already
loaded. The
match is never basename-only, and the loader's other recursive dependencies
remain in the closure.

The owning `PackageTrees` asset is the only emitted asset for a selected
`.node` member. The analyzer MUST NOT emit an overlapping addon file asset;
only dependency libraries outside that package tree are emitted separately.
Every native-addon failure is `ErrPortableRuntimeClosureIncomplete` and omits
package roots, relative paths, expected or actual sizes and digests, `SONAME`
and `NEEDED` values, and binary bytes.

Launch construction is exhaustive and does not use guest PATH, the copied
ELF `PT_INTERP`, an absolute shebang, or a shell wrapper:

```text
static:      <guest EntrypointTarget> + FixedArguments + flattened FixedOptionValues + registry argv + request argv
dynamic:     <guest LoaderTarget> --library-path <colon-joined guest LibraryRootTargets> <guest EntrypointTarget> + FixedArguments + flattened FixedOptionValues + registry argv + request argv
interpreted, static interpreter:
             <guest InterpreterTarget> + RuntimeArgs + <guest EntrypointTarget> + FixedArguments + flattened FixedOptionValues + registry argv + request argv
interpreted, dynamic interpreter:
             <guest LoaderTarget> --library-path <colon-joined guest LibraryRootTargets> <guest InterpreterTarget> + RuntimeArgs + <guest EntrypointTarget> + FixedArguments + flattened FixedOptionValues + registry argv + request argv
```

Every target in `Launch` MUST resolve to a declared asset, the exact
`EntrypointTreeMember` of one derived install-tree owner, a declared directory
within an asset tree, or a private library directory implied by exact library
file assets beneath it. Unused host search roots are not retained in the launch
recipe. The static/dynamic entrypoint plus every loader and interpreter target
are regular owner-executable files. An interpreted entrypoint may be a
non-executable regular file because the bundled interpreter opens it directly.
`RuntimeArgs` contains no placeholder, environment assignment, route selector,
or secret. The fixed launch prefix follows the complete executable recipe and
precedes all registry/request arguments. `NewFromPortableRuntime` must install this recipe into the actual
execution dispatch path; a scheduler-only or refresh-only instance map is not
enough.

Every contributing package has layout fixtures for the installed forms it
recognizes and rejects an unknown form with
`ErrPortableRuntimeClosureIncomplete`. Static-symlink, dynamic-binary, and
interpreter/package-tree fixtures must execute a deterministic, credential-free,
network-free probe in the same-target OCI conformance job. A contributor never
invokes a package manager, downloads an artifact, contacts a provider, or
starts an authenticated harness to discover its closure. Linux GOOS/GOARCH without
the declared loader/interpreter closure is insufficient evidence.

Verified-exact lookup is a narrow contributor evidence class, not a general
escape from runtime-lookup analysis. It is valid only for exact-library dynamic
closures with no runtime tree. It does not make an arbitrary ELF acceptable,
does not permit `RPATH`, `RUNPATH`, audit/filter metadata, or unresolved
`DT_NEEDED` entries, and becomes incomplete as soon as configuration can enable
external execution or runtime-loaded code. Merely importing `dlopen` or `dlsym`
is not proof that a recognized single-file runtime loads another file during
the offline probe; without the contributor evidence above, those imports remain
incomplete.

A contributor using verified-exact lookup binds that claim to a named install
form, publisher-authenticated release digest, and contributor-specific
conformance test. Its fixture imports a runtime lookup symbol and executes the
emitted loader recipe in a network namespace and isolated root containing only
the emitted code assets. Removing a required emitted library must make that
probe fail. A generic dynamic ELF without the product identity, exact release
evidence, and probe evidence is not a recognized layout.

Claude v0.15 recognizes only the resolved
`$HOME/.local/share/claude/versions/<x.y.z>` native Linux ELF whose version,
GOARCH, byte size, and SHA-256 match a checked-in row derived from Anthropic's
signed release manifest after that exact release passed the isolated probe.
Unknown and auto-updated digests fail closed until the evidence registry is
reviewed. Shell wrappers, arbitrary ELFs, legacy interpreted launchers, and
unrecognized package-manager layouts are incomplete. The configuration profile
rejects enabled or installed plugins, hooks, MCP servers, workflows, helpers,
wrappers, marketplaces, remote-refresh controls, external paths, unsupported
provider chains, and unprojected loader controls. Host-indexed `.claude.json`
project/trust state is re-digested after recursive inspection for executable
configuration but is not copied into a guest whose workdir identity differs.
Portable activation is separately responsible for regenerating the guest
workdir and preventing uncopied project/local setting sources from entering the
launch.

The initial reviewed release row is Claude Code 2.1.210 for Linux arm64. The
signed manifest's amd64 checksum is not treated as verified-exact evidence
until that exact amd64 artifact passes the same-target isolated probe.

Claude-TUI uses a default-deny exact-name policy rather than a `CLAUDE_`
prefix. Portable execution may inherit `TZ`, `CLAUDE_CODE_OAUTH_TOKEN`,
`CLAUDE_CODE_OAUTH_REFRESH_TOKEN`, `CLAUDE_CODE_OAUTH_SCOPES`, and
`CLAUDE_CODE_DEBUG_LOG_LEVEL` when present. It excludes `ANTHROPIC_*`, every
unknown Claude name, and all parent/session identity or control markers,
including `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID`,
`CLAUDE_CODE_CHILD_SESSION`, and `CLAUDE_CODE_BRIDGE_SESSION_ID`. Activation
regenerates HOME, PATH, USER, LOGNAME, SHELL, TERM, LANG, LC_ALL,
`CLAUDE_CONFIG_DIR`, and XDG path variables inside the guest. One classifier
drives both the live PTY allowlist and this inherited-versus-generated
projection; portable contributions declare no broad Claude prefix.

Asset-kind consistency is validated before any copy. `credential` is always a
sensitive regular file. `config`, `quota`, and `cache` files and trees are
sensitive regardless of contents or contributor intent. Launch targets and
asset executable bits must satisfy the class-specific rules above;
non-executable state cannot declare owner execution bits. Directories are recreated as mode `0700`, sensitive
regular files as `0600`, and hard links are copied into independent inodes.
Contributions contain no secret value: credential-bearing bytes remain only in
the named source asset, and environment output contains a validated name only.
Original source paths never appear in public plans and are redacted from errors
and logs when they reveal an account or home path.

The authoritative surface inventory is a stable join of the production
registry and the actual runner-instance map used by the configured service.
The join records actual transport and structural inclusion; it does not infer
subprocess behavior from a stale static row. Test-only and exact-pin-only
surfaces are excluded from the unpinned structural set. Embedded and HTTP
surfaces are explicit classifications that require no subprocess binary.
Every installed, non-test, structurally unpinned-capable subprocess instance
must implement this capability. A future registry or instance addition fails
the conformance drift test until it contributes a closure or receives an
explicit classification. Shared assets deduplicate only when normalized target,
kind, path kind, content identity, executable state, and source identity all
agree. Launch recipes deduplicate only when every target, runtime argument, and
library root agrees. Any other target collision is an error.

Configured native and HTTP provider instances are not harness capabilities.
The service combines the stable harness contribution inventory with a
field-exhaustive API-neutral projection of effective `ServiceConfig`. Parity
means provider order, `DefaultProviderName`, every field of
`ServiceProviderEntry` (`Type`, `BaseURL`, `ServerInstance`, `Endpoints`,
`APIKey`, `Headers`, `Model`, `Billing`, both inclusion fields,
`ContextWindow`, `ConfigError`, `DailyTokenBudget`,
`CreditBalanceThresholdUSD`, and `CreditProbeTTL`), and `HealthCooldown`.
`WorkDir` is remapped to guest-private state rather than copied as a host path.
`SessionLogDir` is deliberately excluded because service session logs are not
portable. Activation uses a new guest-private log directory unless
`ServiceOptions.SessionLogDir` or a later public execution request supplies a
valid in-runtime override. Provider secrets follow the same redaction and
sensitive-materialization rules. Network reachability,
fresh health, auth validity, and quota are evaluated by the later `Execute`,
not frozen as portable structural parity.

## Continuation Evidence and Completed-Session Resolution

CONTRACT-003 owns continuation policy and exposes only a completed Fizeau
`SessionID`. CONTRACT-004 translates that identifier into a route capability
without exposing provider-native identity.

### Service-owned completed-session locator

The service MUST maintain a stable, durable completed-session locator under
`<effective-service-session-log-dir>/.fizeau-state/continuation/`, where the
effective service directory is resolved once by CONTRACT-003 from
`ServiceOptions.SessionLogDir` and then `ServiceConfig.SessionLogDir()`. A
per-request session-log override never changes this root. The service creates
the private directories with mode `0700` and locator files with mode `0600`;
construction fails when a non-empty configured root cannot be created,
permission-checked, or written. When the effective service directory is empty,
ordinary execution remains available but no durable locator is written and
parents are unavailable after hub eviction or restart. A locator entry
contains only:

| Field | Required rule |
|-------|---------------|
| Fizeau session ID | Exact key supplied by CONTRACT-003 callers. |
| Canonical session-log path | Clean absolute path actually opened for the parent, including a per-request `ServiceExecuteRequest.SessionLogDir` override. |
| Completion state | Not complete until exactly one valid service-owned terminal record has been written. |
| Terminal route key | Exact normalized `{harness, provider, endpoint, server_instance, model}` values from the winning `RouteDecision`. Together these five fields are the canonical route-registry key; no display name or partial tuple may substitute. Empty members are allowed only when that route type does not use them. |

The locator MUST NOT contain a native conversation ID, resume token, opaque
evidence bytes, harness argv, or a public continuation policy. The in-memory
`SessionHub` MAY cache active and completed sessions, but it is not the
continuation source of truth and may forget completed entries.

Before route execution the service atomically writes a pending locator entry
with the canonical path and selected canonical route key. After the
service-owned `session.end` is durably written, it atomically records completion
in the locator. `Continue` resolves the parent by Fizeau session ID,
reads that exact path, and verifies that the log is readable, its session ID
matches, it contains one terminal record, and its terminal route agrees with
the locator. Endpoint is part of that comparison. An absent locator, unreadable
log, non-terminal parent, duplicate terminal, session-ID mismatch, or incomplete
terminal route key returns
CONTRACT-003 `ErrContinuationSessionUnavailable` before a child session is
created or a process is spawned.

This locator is the restart mechanism. After service restart, lookup MUST work
without the old `SessionHub` and without scanning arbitrary directories. In
particular, a parent written through a per-request `SessionLogDir` override is
found through its persisted canonical path; callers do not resupply that
directory. Pre-locator sessions and sessions whose logging was disabled are
not guessed or reconstructed and are unavailable for continuation.

A crash may leave a pending locator whose exact log already contains one
durable, valid terminal record. On startup or parent lookup, the service MAY
complete that locator only after validating the recorded session ID and full
route key against that exact log. A pending locator without such a record stays
unavailable; it is never repaired by scanning another directory. This is the
only completion-recovery path.

### Route-owned runner and opaque native evidence

The terminal route key resolves to the canonical runner stored in the
service's route registry. Execute dispatch and continuation capability lookup
MUST use that same registered runner instance within a service process. A
dispatcher MUST NOT construct one runner for Execute and consult a separate
instance map or a fresh runner for `ContinuationHarness`. After restart the
registry may reconstruct its canonical instance for the same route key; that
instance reopens any harness-owned durable evidence store.

A `ContinuationHarness` implementation owns all harness-native conversation
evidence. Its Execute/protocol parser records that evidence under the parent
Fizeau session ID and canonical route key in a harness-owned durable store;
in-memory-only evidence does not satisfy this capability. The service passes
only `ParentSessionID`; it does not read, compare, copy, or deserialize the
evidence. The normalized child `ExecuteRequest` MUST NOT acquire a
native-evidence field.

Service- or harness-derived native conversation IDs, resume tokens, and opaque
evidence MUST NOT serialize
into public `ServiceEvent` data, public final projections, service-owned
`session.start` or `session.end` JSONL, locator entries, event metadata,
`FinalData.Extra`, diagnostic `Detail`, or error text. Harness-private durable
storage is permitted only behind the runner boundary and MUST NOT be projected
or exposed through service diagnostics. Missing private evidence after restart
is `ErrContinuationEvidenceUnavailable`; the service does not scrape native
IDs from logs or reconstruct them from text.

Caller-supplied metadata remains opaque caller data: Fizeau does not scan or
filter arbitrary values merely because they resemble a native identifier. The
opacity invariant forbids implementations from adding, deriving, or copying
route-native evidence into metadata or another public/service-owned surface.

`ContinuationHarness` never receives `ContinuationPolicy` and never decides
between resume and fresh execution. The service validates lineage and policy,
selects resume or fresh behavior, and invokes the capability only for a resume
attempt. `fresh_session` does not probe, assert, or invoke the optional
interface.

### Prepare, persistence, and start ordering

Resume dispatch is two-phase:

1. The service validates the public request, resolves the completed parent and
   exact canonical route key, obtains that registered runner, and calls
   `PrepareContinuation`. Preparation may read and validate the runner's
   private durable evidence and return a single-use private handle. It MUST NOT
   create a child session, acquire a child lease, spawn, or emit an event.
2. If preparation reports unavailable evidence, CONTRACT-003 policy runs with
   no child session. Otherwise the service creates the child Fizeau session,
   writes its pending locator, and acquires a fresh lifecycle lease.
3. The service makes that lease available through the ordinary registered-runner
   launch seam and calls `PreparedContinuation.Start` exactly once. Start may
   spawn. Evidence loss or native rejection at this point is a failure of the
   child; no `prefer_resume` fresh fallback occurs.

For a continuation-capable route, the runner MUST durably commit private native
evidence before emitting a successful implementation final. The service then
durably writes `session.end`, atomically completes the pending locator, and only
then makes the successful public terminal fact observable. A crash before the
private commit leaves no successful implementation final; a crash after the
log commit but before locator completion uses the pending-locator recovery rule
above. Orphan private evidence for an incomplete parent is never sufficient to
continue and MAY be garbage-collected by the owning runner.

## Live Subprocess Lifecycle

CONTRACT-003 v0.15 is authoritative for accepted-session lifecycle and typed
terminal facts. This section defines the harness-side obligations needed to
satisfy it.

### One neutral lifecycle owner

`internal/processlifecycle` MUST own the platform lifecycle implementation for
every production path that can start a live child process. This includes:

- `Harness.Execute` for all six built-in subprocess harnesses;
- every `PreparedContinuation.Start` implementation;
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

For `PreparedContinuation.Start`, the registered runner MAY reuse only the
private durable continuation evidence bound during preparation. The service
MUST acquire a fresh child lifecycle lease before Start can launch the resumed
invocation, and the runner MUST bind the new process and all descendants to
that lease. The parent lease, process, PTY, supervisor, control channel, and
cleanup context are never reopened or reused. Fresh `prefer_resume` fallback
and `fresh_session` paths go through ordinary Execute dispatch and acquire
their own fresh child leases under the same rule.

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
| Completed-session locator is missing, unreadable, non-terminal, or lacks a terminal route | Service returns CONTRACT-003 `ErrContinuationSessionUnavailable` before child-session creation | Restore the completed Fizeau session record; do not guess a native ID or scan arbitrary directories. |
| Resolved route does not implement `ContinuationHarness`, or preparation reports private evidence unavailable | `require_resume` returns CONTRACT-003 `ErrContinuationUnsupported`; `prefer_resume` may start its normalized fresh request | Service policy decides fallback; the harness never does. |
| Prepared evidence becomes unusable or native resume is rejected during Start | One failed implementation final event for the created child; no automatic fresh attempt | Caller may submit a later explicit fresh request. |
| Continuation request carries an empty Fizeau parent ID or a non-normalized child request | `ErrContinuationRequestInvalid`; no spawn | Fix the service-to-harness adapter; this is not a caller-facing policy outcome. |

## Projection to CONTRACT-003

The service layer projects harness-level types onto CONTRACT-003 public
types as follows. Serviceimpl and the root facade are the only projection
seams; nothing else may read an internal payload and re-emit it under different
names.

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
| `service.ServiceContextCapacityData` | Field-exhaustive root mapping from `ContextCapacityData`; every field and enum string is preserved |
| `service.ServiceDecodedEvent.ContextCapacity` | Root `DecodeServiceEvent` decodes `ServiceEventTypeContextCapacity` as the public `ServiceContextCapacityData` payload; it does not consume the internal event type or DTO |

Routing decisions inside the service consume `QuotaStatus.RoutingPreference`
directly. The service MUST NOT project `RoutingPreference` into the public
contract; it remains an internal routing signal.

`HarnessInfo.Quota` and `HarnessInfo.Account` projections MUST remain
backwards-compatible in JSON shape with the current CONTRACT-003 schema.
Adding fields is allowed; removing or renaming existing public fields
requires a CONTRACT-003 amendment.

### `context_capacity` event ownership and order

Core alone computes capacity. It emits primitive `core.ContextCapacityEventData`
as `core.EventContextCapacity`; it MUST NOT import this package or a root/public
DTO. `internal/serviceimpl` field-exhaustively maps that event to
`ContextCapacityData` and `EventTypeContextCapacity`. The root facade performs
the second exhaustive mapping into CONTRACT-003 `ServiceContextCapacityData`,
constructs the public `ServiceEvent`, and owns session-log and replay
projections. A subprocess harness MAY supply native records used to construct
ordinary text, tool, progress, or implementation-final evidence, but it MUST
NOT calculate, synthesize, reorder, or terminalize a `context_capacity` event.
Harness-native streams are evidence, never an authoritative public event
surface.

Ordering is normative:

- `clamped` is non-terminal and immediately precedes the corresponding
  `llm.request`, whose effective maximum matches the capacity payload.
- `planning_skipped` emits no `clamped`, `llm.request`, `llm.response`, or
  `planning.turn` for the prevented call; main execution continues.
- `rejected` emits no `clamped` or `llm.request`, precedes `session.end` and the
  public final, and terminates as
  `failed / context_capacity_exceeded / tool_loop`. The service does not try
  another route candidate.

The `context_capacity` event type and JSON payload are additive in v0.15.
Adding their corresponding fields to exported root Go structs is
source-breaking only for external unkeyed composite literals and therefore
ships in the same v0.15 keyed-literal migration defined by CONTRACT-003.
Consumers MUST preserve unknown event and enum values; they MAY ignore the new
non-terminal event when capacity telemetry is not needed.

## Conformance Evidence

Every harness implementation MUST carry, at minimum:

| Capability | Evidence |
|------------|----------|
| `Harness` (all six) | Unit tests for `Info()`, `HealthCheck()`, and an `Execute` happy path with one implementation final event of correct shape. |
| `QuotaHarness` (claude, claude-tui, codex, gemini) | Unit tests for `QuotaStatus` against a synthetic cache fixture (fresh and stale cases); unit tests for `RefreshQuota` against a recorded cassette per ADR-002; unit test asserting `QuotaFreshness` is the documented constant; conformance assertion that every emitted `Windows[].LimitID` value is a member of `SupportedLimitIDs()`. |
| `AccountHarness` (every implementing harness) | Unit tests for `AccountStatus` returning each documented state (Authenticated, Unauthenticated, no evidence) with correct `Fresh` against a synthetic file fixture. |
| `ModelDiscoveryHarness` (all six) | Unit tests prove `DefaultModelSnapshot` returns a non-empty model list with nil error or returns `ErrModelDiscoveryEvidenceMissing`, never an empty successful fallback. Conformance asserts that `ResolveModelAlias` resolves every family returned by `SupportedAliases()` and returns `ErrAliasNotResolvable` for an out-of-set family. Package documentation MUST enumerate the same set returned by `SupportedAliases()`. |
| `ContextModelDiscoveryHarness` (implementers) | Cancellation reaches the live probe, no background context replaces it, and the method waits for containment cleanup or the service-owned cleanup deadline before returning. |
| `ContinuationHarness` (implementers) | `TestContinuationHarnessReceivesOnlyFizeauSessionRef` proves `ParentSessionID` is the only conversation identifier and `Request` contains no native evidence; `TestContinuationPrepareOrdersChildAndSpawn` proves prepare has no child, lease, spawn, or events and Start occurs only after child plus fresh lease; `TestContinuationEvidenceUnavailableBeforeSpawn` proves missing evidence returns the sentinel without a child or event stream. |
| `PortableRuntimeHarness` (structurally unpinned-capable subprocess implementers) | `TestPortableRuntimeInventoryCoversEveryEligibleRegisteredHarness` proves exhaustive registry/actual-instance joining, actual-transport classification, same-target content-addressed closures and launch recipes, deterministic shared-asset deduplication, typed failure for unknown/incomplete layouts, and no route selection or process start. `TestPortableRuntimeInventoryContainsNoEnvironmentValues` proves only validated inherited names and typed value-opaque execution rules cross the interface. `TestPortableRuntimeExecutionConstraints`, `TestPortableRuntimeFixedOptionValues`, and `TestPortableRuntimeMixedStateProjection` prove the exact value-opaque schema, closed-world name ownership, deterministic sorting/ownership, runtime-backed typed paths, config-tree read-only requirements, mixed immutable/writable native-directory projection, absence conflicts, ordered standalone fixed flags and typed option/value pairs, and redacted rejection. `TestPortableRuntimeNodeInterpreterBypassesShebangAndPATH`, `TestPortableRuntimeNodeInterpreterIdentity`, and `TestPortableRuntimeNodeInterpreterRejectsRPATH` prove caller-selected interpreter identity, descriptor-bound hashing and replacement rejection, direct or explicit-loader recipes independent of shebang and `PATH`, and fail-closed absolute `DT_RPATH`. Static, dynamic, and interpreted layout fixtures execute their typed launch recipe offline; OCI activation fixtures additionally prove the declared config tree and projected config members deny write, unlink, rename, replacement, and shadowing while writable seeds can refresh and create locks/siblings. |
| Completed-session resolution | `TestCompletedSessionRouteResolutionRequiresTerminalRoute` rejects missing, unreadable, incomplete, duplicate-terminal, and route-less parents; `TestCompletedSessionRouteResolutionUsesPerRequestLogOverrideAfterRestart` discards the in-memory hub, reloads the durable locator, and resolves the exact overridden path and full endpoint-aware route key. |
| Continuation evidence boundary | `TestContinuationNativeReferenceIsNotSerialized` seeds a recognizable native token and proves it is absent from public final JSON, every service-owned session-log record, locator bytes, metadata, diagnostics, and error text. |
| Route instance and lifecycle | `TestContinuationUsesRegisteredRouteInstance` proves Execute evidence and continuation use the actual endpoint-aware route-registry object rather than an ad hoc runner; `TestContinuationDispatchAcquiresFreshLifecycleLease` proves a resumed child uses a different lease and containment identity from its parent; `TestContinuationFreshPoliciesAcquireFreshLifecycleLease` table-tests `prefer_resume` fallback and `fresh_session`. |
| Persistence ordering | `TestContinuationEvidenceCommitsBeforeSuccessfulTerminal` proves durable private evidence precedes a successful implementation final and public success follows durable log plus completed locator; `TestContinuationRecoversPendingLocatorAfterTerminalCommit` proves crash recovery uses only the pending locator's exact path and full route key. |
| Live subprocess lifecycle (every spawning path) | Platform subprocess tests cover normal completion, failure, timeout, cancellation, caller-signalled abandonment, and caller death with a grandchild in the containment boundary. Static enforcement proves production child creation routes through `internal/processlifecycle`. |
| Recovery identity | Tests simulate PID reuse or a changed process-birth token and prove the recovery reaper does not signal the mismatched process. |
| Projection | Service-level test asserting CONTRACT-003 JSON shape (e.g. `HarnessInfo.Quota`) is identical before and after the harness migration — pinned to a recorded fixture. |
| Context-capacity bridge | Exhaustive mapping tests cover core -> `ContextCapacityData` -> root payload and decoder. Ordering fixtures prove clamp-before-request, skip-without-request, and reject-before-terminal without another route. Harness fixtures prove native streams cannot originate this service-owned event. |

## Invariants

These invariants are enforced by reviewer attention and (where practical)
by `go vet`-shaped tooling:

1. **No production service-side import of `internal/harnesses/<name>`
   symbols.** Concrete runner construction belongs under
   `internal/harnesses/builtin`. The service owns one endpoint-aware route
   authority and passes its exact five-field binding into dispatch; dispatch
   MUST NOT construct or select a runner by harness/display name. All other
   consumption MUST go through the interface methods on `Harness`,
   `QuotaHarness`, `AccountHarness`, or `ModelDiscoveryHarness`.
   Continuation consumption MUST go through `ContinuationHarness`; service
   code never calls a concrete runner's resume helper.
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
   Routes that can continue MUST use the single route-registry instance for
   Execute, private evidence ownership, and continuation capability lookup.
   Routes with no continuation capability MAY construct fresh instances per
   call only when doing so preserves equivalent observable cache behavior.
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
12. **Continuation carries one conversation identifier.**
    `ContinuationRequest.ParentSessionID` is a Fizeau session ID and is the
    only conversation identifier crossing into `ContinuationHarness`.
    `ContinuationRequest.Request` is a normalized child `ExecuteRequest`; it
    carries neither service policy nor native evidence.
13. **Completed-session lookup is durable and exact.** The stable locator
    records the canonical path actually opened, so per-request log overrides
    resolve after restart. The hub is a cache, not authority; arbitrary
    directory scans and reconstruction from text are forbidden.
14. **Native continuation evidence is durable and opaque.** The route-owned
    runner alone stores and interprets it. Service- or harness-derived native
    IDs and tokens never appear in service events, service session JSONL,
    locator records, metadata, diagnostic maps, public structs, or errors.
    Caller-authored metadata is not inspected for coincidental values.
15. **Continuation uses the registered route object.** Execute dispatch,
    evidence capture, and optional capability lookup resolve through one
    canonical instance per endpoint-aware route key. A separate capability map
    or ad hoc runner construction is forbidden.
16. **Every continued invocation is a new lifecycle owner.** Logical native
    conversation reuse never reuses the parent's process, PTY, supervisor,
    control channel, cleanup context, or lease.
17. **Preparation precedes child ownership.** Evidence availability is decided
    synchronously before child-session creation. Start is single-use and occurs
    only after the child and lease exist; a post-prepare failure cannot fall
    back to a second invocation.
18. **Successful continuation evidence precedes public success.** Private
    evidence, the service terminal log, and completed locator become durable in
    that order. Pending-locator recovery validates the exact path and full
    route key rather than scanning.
19. **The `context_capacity` event is service-owned.** Core decides it,
    serviceimpl and the root facade map it exhaustively, and harness-native
    streams never originate or terminalize it. The event order defined above
    is preserved in live, logged, decoded, and replayed projections.
20. **Portable asset and constraint discovery is harness-owned and value-opaque.** Service
    code consumes only `PortableRuntimeHarness`; it does not reproduce concrete
    path rules. Contributions contain content-addressed same-target dependency
    closures, typed guest-relative launch recipes, validated inherited
    environment names, and typed execution constraints, never raw environment
    values/assignments, host paths, route decisions, sessions, processes, or
    public diagnostics. The private materializer persists the normalized record;
    activation enforces it generically.
21. **Portable inventory is complete or fails.** Every installed non-test
    structurally unpinned-capable subprocess instance contributes a closure.
    Actual native/HTTP transports, explicitly pinned-only surfaces, and
    test-only surfaces are exhaustively classified. Missing support never
    silently narrows the structural candidate set; later live health remains a
    separate `Execute` decision.

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
- Selecting `require_resume`, `prefer_resume`, or `fresh_session`, or exposing
  those values to harness code. CONTRACT-003 service orchestration owns policy
  and fallback.
- Exposing a provider- or harness-native conversation identifier through a
  public type, event, session log, locator record, diagnostic, or error.
- Adding native-provider continuation to CONTRACT-004. This version's optional
  capability is subprocess-harness-backed; native-provider routes report
  unsupported under CONTRACT-003 and require a separate neutral contract if
  support is added later.
- Copying portable assets, creating a public bundle, or orchestrating a
  container. Harnesses describe their owned assets through the optional
  capability; the neutral materializer and embedding caller own those later
  stages.

## Acceptance Criteria

1. `internal/harnesses/types.go` declares `Harness`, `QuotaHarness`,
   `AccountHarness`, `ModelDiscoveryHarness`, and
   `ContextModelDiscoveryHarness` interfaces with the signatures above;
   `DefaultModelSnapshot` returns `(ModelDiscoverySnapshot, error)`. It also
   declares optional `ContinuationHarness` with the exact method
   `PrepareContinuation(context.Context, ContinuationRequest) (PreparedContinuation, error)`
   and `PreparedContinuation` with exact method
   `Start(context.Context) (<-chan Event, error)`.
2. `QuotaStatus`, `AccountSnapshot`, `QuotaStateValue`,
   `RoutingPreference`, `ErrAliasNotResolvable`, and
   `ErrModelDiscoveryEvidenceMissing` are declared in
   `internal/harnesses/types.go`. The same file declares
   `ContinuationRequest` with exactly `ParentSessionID string` and
   `Request ExecuteRequest`, plus `ErrContinuationRequestInvalid` and
   `ErrContinuationEvidenceUnavailable`; it declares no native-session field.
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
14. `TestContinuationHarnessReceivesOnlyFizeauSessionRef` proves the optional
    capability receives the Fizeau parent ID plus normalized child request and
    no native identifier, token, evidence, or policy.
15. `TestCompletedSessionRouteResolutionRequiresTerminalRoute` and
    `TestCompletedSessionRouteResolutionUsesPerRequestLogOverrideAfterRestart`
    prove durable locator validation, terminal-route recovery, exact override
    paths, and restart behavior without the in-memory hub.
16. `TestContinuationNativeReferenceIsNotSerialized` proves a recognizable
    native reference is absent from public final/session JSON, locator bytes,
    event metadata, diagnostics, and error text.
17. `TestContinuationUsesRegisteredRouteInstance` proves the runner that
    captured Execute evidence is the route-registry object asserted for and
    invoked through `ContinuationHarness`; no separate instance map or ad hoc
    constructor satisfies the test.
18. `TestContinuationDispatchAcquiresFreshLifecycleLease` proves the child
    continuation invocation has a new lease and containment identity and does
    not retain any live parent resource.
19. `TestContinuationEvidenceUnavailableBeforeSpawn` proves unusable private
    evidence returns its sentinel without an event stream or child process.
    Service-level policy tests prove this becomes CONTRACT-003 unsupported or
    fresh-fallback behavior without exposing policy to the harness.
20. `TestContinuationPrepareOrdersChildAndSpawn` proves preparation creates no
    child, lease, process, or event; the child and fresh lease exist before
    single-use Start; and a Start-time evidence failure produces one failed
    child without a fresh fallback.
21. `TestContinuationFreshPoliciesAcquireFreshLifecycleLease` table-tests
    `prefer_resume` fallback and `fresh_session` and proves each uses a new
    child Fizeau session, lease, and containment identity.
22. `TestContinuationEvidenceCommitsBeforeSuccessfulTerminal` and
    `TestContinuationRecoversPendingLocatorAfterTerminalCommit` prove private
    evidence -> session log -> locator -> public-success ordering and exact-path,
    full-route-key recovery after an interrupted completion commit.
23. `internal/harnesses/types.go` declares `EventTypeContextCapacity`,
    `TerminalCauseContextCapacityExceeded`, the exact ten-field
    `ContextCapacityData` payload, and `FinalData.ContextCapacity` above.
    Exhaustive mapping and decoder fixtures fail if core, internal, or root
    fields drift; ordering fixtures prove clamp, planning-skip, and rejection
    behavior without native stream authority or next-candidate dispatch.
24. `internal/harnesses/types.go` declares `PortableRuntimeTarget`,
    `PortableRuntimeFileIdentity`,
    `PortableRuntimeAssetKind`, `PortableRuntimePathKind`,
    `PortableRuntimeClosureClass`, `PortableRuntimeAsset`,
    `PortableRuntimeLaunch`, `PortableRuntimeEnvironment`,
    `PortableRuntimeGuestPathScope`, `PortableRuntimeGuestPath`,
    `PortableRuntimeEnvironmentConstraintKind`,
    `PortableRuntimeEnvironmentConstraint`,
    `PortableRuntimeExecutionConstraints`, `PortableRuntimeStateProjectionEntry`,
    `PortableRuntimeStateProjection`, `PortableRuntimeContribution`, optional
    `PortableRuntimeHarness`, `ErrPortableRuntimeTargetUnsupported`, and
    `ErrPortableRuntimeClosureIncomplete` with the signatures, content
    identities, closure classes, and value-opaque behavior above.
25. `TestPortableRuntimeInventoryCoversEveryEligibleRegisteredHarness` fails
    if the production-registry/actual-instance join is incomplete; actual
    transport or structural inclusion drifts; any installed non-test,
    structurally unpinned-capable subprocess lacks a content-addressed
    same-target closure; provider/config field parity drifts; or native, HTTP,
    pinned-only, and test-only classifications drift. Package fixtures reject
    unknown installed layouts, and required OCI fixtures execute static,
    dynamic, and interpreted closures offline.
26. `TestPortableRuntimeInventoryContainsNoEnvironmentValues` and redaction
    fixtures prove contributions contain unique valid inherited names and
    typed generated/unset rules only and no secret value, empty/unset ambiguity, `name=value` assignment,
    account-bearing diagnostic path, route selector, process start, provider
    contact, or session/lifecycle record. Asset and launch validation rejects
    inconsistent kind/path/executable/loader/interpreter combinations before
    copying.
27. `TestPortableRuntimeExecutionConstraints` and
    `TestPortableRuntimeFixedOptionValues` prove closed-world baseline,
    inherited, and typed-rule name ownership; the exact enum/field shape;
    runtime-backed environment paths; exact config-tree read-only backing;
    required-absent disjointness; deterministic sorting and defensive copies;
    ordered unique flag-only fixed arguments; contributor-ordered typed fixed
    option/value pairs including `-e none`; exact fixed-prefix ordering before
    registry/request arguments; positional, assignment, path-bearing,
    duplicate/conflicting-option, and secret-like value rejection; and index-
    only redacted errors. Later activation and OCI tests prove actual
    read-only enforcement and absence before process start.
28. `TestPortableRuntimeMixedStateProjection` and
    `TestPortableRuntimeStateProjectionValidation` prove exact config plus
    credential/quota/cache asset references, activation-owned non-empty
    directories, deterministic two-level sorting, bidirectional deep ownership,
    prefix-seed versus projection-consumed classification, concrete-output-only
    absence conflicts, no reused asset or overlapping output/directory, and
    typed redacted failures. Materialization tests separately revalidate source
    identity and symlinks. Activation/OCI tests prove projected config cannot be
    written, unlinked, renamed, replaced, or shadowed while credential refresh,
    lock creation, and sibling state creation succeed.
29. `TestPortableRuntimeNodeInterpreterBypassesShebangAndPATH`,
    `TestPortableRuntimeNodeInterpreterIdentity`, and
    `TestPortableRuntimeNodeInterpreterRejectsRPATH` prove that interpreted
    closures bind the caller-selected interpreter to one exact size and SHA-256,
    hash the retained ELF descriptor, reject path replacement and malformed
    identity without leaking identity material, ignore absolute and env
    shebangs plus poisoned `PATH` for interpreter selection, and retain the
    absolute `DT_RPATH` prohibition. Publisher, version, build,
    package-integrity, and offline-probe evidence remain contributor-owned.

## References

- [CONTRACT-003 Fizeau Service Interface](./CONTRACT-003-fizeau-service.md)
- [ADR-002 PTY Cassette Transport](../adr/ADR-002-pty-cassette-transport.md)
- [ADR-011 Cost-Based Routing With Quota Pools](../adr/ADR-011-cost-based-routing-with-quota-pools.md)
- [ADR-012 Per-Source On-Disk Cache](../adr/ADR-012-per-source-on-disk-cache.md)
- [ADR-013 claude-tui PTY Harness](../adr/ADR-013-claude-tui-pty-harness-fork.md)
- [ADR-014 Universal Harness Interface](../adr/ADR-014-universal-harness-interface.md)
- [Primary Harness Capability Baseline](../primary-harness-capability-baseline.md)
- [Implementation plan: harness interface refactor](../plan-2026-05-14-harness-interface-refactor.md)
