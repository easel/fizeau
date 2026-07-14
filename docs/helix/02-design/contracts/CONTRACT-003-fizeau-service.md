---
ddx:
  id: CONTRACT-003
  depends_on:
    - helix.prd
    - ADR-008
    - ADR-009
  review:
    self_hash: 3848292ba06e3c78f496a40f8bb94204563efbd4f2266d8779d820e1590ca298
    deps:
      ADR-008: 478df30f7716244dd9b29425624cbe39eab51c589cde5e6610ef456b262c101f
      ADR-009: d9968b4818b0f45508f3e0689b403ff6997c2722924e7457605bc43080ae5a4a
      helix.prd: 12c9ecc92726e3d50896a8afb51224906edfea9863d8114d39a6c2a0a2e54003
    reviewed_at: "2026-07-14T20:00:14Z"
---
# CONTRACT-003: FizeauService Service Interface

**Status:** Draft
**Contract Version:** v0.15 (next product API)
**Owner:** Fizeau maintainers
**Replaces:** CONTRACT-002-ddx-harness-interface

## Purpose

This contract defines the public Go surface of the Fizeau module. The root
package `fizeau` is the facade: service construction, request and response
types, routing/status projections, session-log projections, and public errors.
Concrete execution, transcript rendering, provider adapters, quota state,
routing quality, and catalog mechanics remain behind `internal/`.

Consumers such as DDx, the standalone `fiz` CLI, and future embedders interact
through this surface only. They do not import internal packages and they do not
parse private session-log records when a public projection exists.

ADR-009 owns the v0.11 routing vocabulary: callers express routing intent with
`Policy`, `MinPower`, and `MaxPower`; they express hard overrides with
`Harness`, `Provider`, and `Model`.

The routing entrypoint is conceptually `route(client_inputs, fiz_models_snapshot)`.
Client inputs include policy and numeric power intent, hard pins, `no_remote`, metered opt-in, tools,
context, reasoning needs, and other explicit constraints. The `fiz models`
snapshot is the only source of routing facts. Fizeau does not require a daemon
for correctness. Its freshness contract is synchronous, lock-coordinated
refresh; long-running clients such as a DDx server may call that public refresh
surface on a heartbeat to keep the snapshot warm.

## Scope and Boundaries

- **In scope:** the public root-package service facade; routing and inventory
  projections; execution, continuation, session events, terminal facts,
  session-log projections, and wrapped-harness lifecycle ownership.
- **Out of scope:** concrete adapter implementation, provider-native protocols,
  DDx worktree setup, gates, review, landing, preservation, tracker mutation,
  and bead closure policy.
- **Owning system:** Fizeau owns each accepted service invocation through
  terminal cleanup. The caller owns task semantics and every workflow decision
  outside that invocation.

## Normative Surface

The Go declarations, enum values, field rules, error mappings, process-lifecycle
requirements, compatibility rules, and conformance obligations in this document
are normative. Examples are illustrative but use only normative values.

## Interface

```go
package fizeau

import (
    "context"
    "errors"
    "io"
    "time"
)

type FizeauService interface {
    Execute(ctx context.Context, req ServiceExecuteRequest) (<-chan ServiceEvent, error)
    Continue(ctx context.Context, req ServiceContinuationRequest) (<-chan ServiceEvent, error)
    TailSessionLog(ctx context.Context, sessionID string) (<-chan ServiceEvent, error)

    ListHarnesses(ctx context.Context) ([]HarnessInfo, error)
    ListProviders(ctx context.Context) ([]ProviderInfo, error)
    ListModels(ctx context.Context, filter ModelFilter) ([]ModelInfo, error)
    RefreshModels(ctx context.Context, opts ModelRefreshOptions) (*ModelSnapshotInfo, error)
    ListPolicies(ctx context.Context) ([]PolicyInfo, error)

    HealthCheck(ctx context.Context, target HealthTarget) error
    ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDecision, error)
    RecordRouteAttempt(ctx context.Context, attempt RouteAttempt) error
    RouteStatus(ctx context.Context) (*RouteStatusReport, error)

    UsageReport(ctx context.Context, opts UsageReportOptions) (*UsageReport, error)
    ListSessionLogs(ctx context.Context) ([]SessionLogEntry, error)
    WriteSessionLog(ctx context.Context, sessionID string, w io.Writer) error
    ReplaySession(ctx context.Context, sessionID string, w io.Writer) error
}

func New(opts ServiceOptions) (FizeauService, error)
func ValidateUsageSince(spec string) error
func ValidateCachePolicy(v string) error
func ValidatePowerBounds(minPower, maxPower int) error
```

Sixteen service methods are public. `Execute` is the primary verb and
`Continue` is its harness-neutral conversation-continuation companion. The list
methods expose the live routing inventory and policy metadata. `HealthCheck`,
`ResolveRoute`, `RecordRouteAttempt`, and `RouteStatus` are routing/status
projections. The remaining methods project service-owned session logs for
usage, listing, JSON rendering, and replay.

The v0.11 interface has no removed route-introspection service methods and no
separate model reference request field. Old route-reference names are not
compatibility fallbacks for the new policy surface.

## Construction

```go
type ServiceOptions struct {
    ConfigPath string
    Logger io.Writer
    ServiceConfig ServiceConfig

    QuotaRefreshDebounce time.Duration
    QuotaRefreshStartupWait time.Duration
    QuotaRefreshInterval time.Duration
    QuotaRefreshContext context.Context

    CatalogProbeTimeout time.Duration
    CatalogReloadTimeout time.Duration

    LocalCostUSDPer1kTokens float64
    SubscriptionCostCurve *SubscriptionCostCurve
    SessionLogDir string
    HarnessCleanupTimeout time.Duration
    StaleHarnessReaperGrace time.Duration
}

type ServiceConfig interface {
    ProviderNames() []string
    DefaultProviderName() string
    Provider(name string) (ServiceProviderEntry, bool)
    HealthCooldown() time.Duration
    WorkDir() string
    SessionLogDir() string
}

type ServiceProviderEntry struct {
    Type string
    BaseURL string
    ServerInstance string
    Endpoints []ServiceProviderEndpoint
    APIKey string
    Headers map[string]string
    Model string
    Billing BillingModel
    IncludeByDefault bool
    IncludeByDefaultSet bool
    ContextWindow int
    ConfigError string
    DailyTokenBudget int
}
```

The effective service session-log directory is resolved once from
`ServiceOptions.SessionLogDir`, then `ServiceConfig.SessionLogDir()`. It is also
the stable base for service-private continuation locator state; per-request
`ServiceExecuteRequest.SessionLogDir` overrides never change that base. When
the effective service directory is empty, ordinary execution remains valid but
completed-session lookup across hub eviction or restart is unavailable. When
it is non-empty, service construction MUST create or validate the private state
subdirectory with owner-only permissions and fail if that durable location
cannot be made usable. CONTRACT-004 defines its layout and crash recovery.

The service may auto-load configuration when `ServiceConfig` is nil and the
config package registered a loader. Embedders that need deterministic behavior
should pass `ServiceConfig` explicitly.

`HarnessCleanupTimeout` is the v0.15 per-invocation deadline for stopping and
reaping a wrapped harness containment boundary after normal completion,
failure, timeout, cancellation, stream abandonment, or caller death. Zero uses
10 seconds. Negative values are invalid. Execution and provider timeouts may
trigger cleanup, but they do not shorten this service-owned cleanup deadline.

`StaleHarnessReaperGrace` is not a per-invocation cleanup timeout. It is the
minimum age of a persisted non-terminal harness record before a later service
startup may treat that record as stale and attempt recovery. Zero uses five
minutes. The two options MUST remain semantically independent.

`IncludeByDefault` controls unpinned automatic routing participation. For
pay-per-token providers, `IncludeByDefault=true` is necessary but not
sufficient: the configuration projection must also reflect explicit
metered-spend opt-in, such as `routing.allow_metered: true`, before such a
provider participates in unpinned automatic routing. ServiceConfig
implementations that do not expose a separate metered flag should project this
policy by leaving pay-per-token providers default-excluded until spend opt-in is
known. Explicit provider/model/harness pins may still consider a provider that
is not included by default or metered-enabled, but pins do not bypass policy
`Require` constraints.

## Execute Request

```go
type ServiceExecuteRequest struct {
    Prompt string
    SystemPrompt string

    Model string
    Provider string
    Harness string
    Policy string
    MinPower int
    MaxPower int

    Reasoning Reasoning
    Permissions string
    EstimatedPromptTokens int
    RequiresTools bool
    CachePolicy string

    Temperature *float32
    TopP *float64
    TopK *int
    MinP *float64
    RepetitionPenalty *float64
    Seed *int64
    SamplingSource string

    WorkDir string
    NoStream bool
    Tools []Tool
    ToolPreset string
    PlanningMode bool

    Timeout time.Duration
    IdleTimeout time.Duration
    ProviderTimeout time.Duration

    MaxIterations int
    MaxTokens int
    ReasoningByteLimit int
    CompactionContextWindow int
    CompactionReserveTokens int
    StallPolicy *StallPolicy

    SessionLogDir string
    SelectedRoute string
    Metadata map[string]string
    Role string
    CorrelationID string
}
```

`Policy` is the named routing policy. `MinPower` and `MaxPower` are optional
numeric power hints on the catalog's 1..10 scale. `Model`, `Provider`, and
`Harness` are hard pins and are recorded as override signals for routing
quality. A request is unpinned when all three hard-pin fields are empty;
`Policy`, power hints, reasoning, capability flags, and token estimates do not
make a request pinned. `Role` and `CorrelationID` are observational metadata
only; they do not affect candidate eligibility or scoring.

`CachePolicy` accepts `""`, `"default"`, and `"off"`. `Reasoning` is the
single public reasoning control; provider-specific names remain adapter
terminology.

`ResolveRoute` and `Execute` are cache-first on the route hot path. Before
scoring an unpinned or partially pinned automatic route, they read the freshest
cached routing-relevant facts available and may request a coordinated
background refresh for stale health, quota, discovery, context, reasoning,
tool-support, cost, or utilization fields. They must not synchronously contact
local model providers, block on stale `/v1/models` discovery, or fail the
process solely because one configured local provider is unreachable. Known
fresh failed health evidence can still make that provider ineligible with a
typed dispatchability reason; unknown local health is a score penalty, not a
hard gate when alternatives exist.

### Caller-signalled cancellation and stream abandonment

The caller signals stream abandonment by cancelling the context passed to
`Execute` or `Continue`. Silently ceasing to receive from the returned Go
channel is not observable and is not a cancellation signal. A caller that
stops draining an event stream MUST cancel its context.

Fizeau MUST NOT make cleanup or terminalization depend on consumer receive
progress. It MAY coalesce or drop non-terminal events when necessary to retain
capacity for the terminal fact and stream closure. Context cancellation begins
wrapped-harness cleanup, but cleanup runs under a service-owned context bounded
by `HarnessCleanupTimeout`, not under the cancelled request context.

## Session Continuation

Callers request continuation without naming a concrete harness, provider-native
conversation token, subprocess flag, or adapter type. Fizeau resolves the prior
session's continuation capability and owns translation to a supported
subprocess harness. In this contract version, native-provider routes do not
implement the CONTRACT-004 harness capability and therefore follow the
unsupported-route policy behavior; they MUST NOT be wrapped in a subprocess
`Harness` merely to appear resumable.

```go
type ContinuationPolicy string

const (
    ContinuationRequireResume ContinuationPolicy = "require_resume"
    ContinuationPreferResume  ContinuationPolicy = "prefer_resume"
    ContinuationFreshSession  ContinuationPolicy = "fresh_session"
)

type ContinuationDisposition string

const (
    ContinuationResumed ContinuationDisposition = "resumed"
    ContinuationFresh   ContinuationDisposition = "fresh"
)

type ServiceContinuationRequest struct {
    SessionID string
    Prompt string
    Policy ContinuationPolicy
    FreshRequest ServiceExecuteRequest
    Metadata map[string]string
    CorrelationID string
}

var (
    ErrContinuationPolicyInvalid = errors.New("invalid continuation policy")
    ErrContinuationSessionUnavailable = errors.New("continuation session unavailable")
    ErrContinuationUnsupported = errors.New("continuation unsupported")
)
```

`SessionID`, `Prompt`, and `Policy` are required. `SessionID` identifies the
completed Fizeau session whose conversation may be resumed. `FreshRequest`
defines the normal `Execute` request used only when policy permits a fresh
session; Fizeau MUST replace `FreshRequest.Prompt` with `Prompt`. Callers MAY
leave hard pins empty and use normal policy and power intent. No continuation
request requires a harness name or harness-specific resume token.

For `require_resume`, `FreshRequest` MUST be the zero value. For
`prefer_resume` and `fresh_session`, normal `ServiceExecuteRequest` validation
applies to `FreshRequest` after Fizeau replaces its prompt. Outer `Metadata`
and `CorrelationID` override fields with the same meaning in `FreshRequest` and
are recorded on the new session.

Each accepted `Continue` call creates a new Fizeau session ID. Its terminal
projection and service-owned session-start log record MUST carry the parent
`SessionID` and actual `ContinuationDisposition`. A resumed session reuses
harness-owned conversation state behind the public boundary. A
fresh session starts an ordinary `Execute` using `FreshRequest`; it preserves
lineage but MUST NOT claim that provider or harness conversation state was
resumed.

Policy behavior is normative:

| Policy | Supported completed parent route | Valid completed parent whose actual route cannot resume |
|--------|------------------------------------|--------------------------------------------------------|
| `require_resume` | Resume and report `resumed`. | Return `ErrContinuationUnsupported`; do not start a session or spawn a process. |
| `prefer_resume` | Resume and report `resumed`. | Start `FreshRequest` as a new session and report `fresh`. |
| `fresh_session` | Do not probe or invoke resume capability; start `FreshRequest` and report `fresh`. | Same behavior; support is irrelevant. |

An empty or unknown policy returns `ErrContinuationPolicyInvalid` before a
session starts. A missing, unreadable, or incomplete prior session returns
`ErrContinuationSessionUnavailable`; `prefer_resume` does not convert missing
lineage into a fresh session because the caller supplied an invalid parent.
Continuation capability is route-specific and MUST be reported as unsupported
rather than inferred from a harness name. The table applies only after Fizeau
has resolved a valid, completed parent and its actual terminal route; an
unresolved lineage is unavailable for every policy and never falls back to a
fresh session.

For a resume attempt, the service first asks the exact registered route to
prepare continuation. Preparation may validate and bind private durable
evidence but MUST NOT create a child session, acquire a child lifecycle lease,
spawn, or emit events. Only after preparation succeeds does the service create
the child Fizeau session and fresh lifecycle lease and allow the prepared
operation to start. If evidence becomes unusable after successful preparation,
the already-created child fails normally; `prefer_resume` MUST NOT start a
second fresh attempt. CONTRACT-004 owns the internal two-phase interface and
crash ordering.

## Session Lifecycle and Terminal Facts

### Stable terminal outcome, terminal cause, and session stage

While the caller process remains alive, every accepted `Execute` or `Continue`
call produces exactly one terminal service event before its event stream
closes. The terminal projection and `DrainExecuteResult` expose these typed
fields in addition to the legacy `Status` and human-readable `Error` fields:

```go
type SessionOutcome string

const (
    SessionOutcomeSuccess   SessionOutcome = "success"
    SessionOutcomeFailed    SessionOutcome = "failed"
    SessionOutcomeCancelled SessionOutcome = "cancelled"
    SessionOutcomeTimedOut  SessionOutcome = "timed_out"
)

type TerminalCause string

const (
    TerminalCauseCompleted         TerminalCause = "completed"
    TerminalCauseRouteUnavailable TerminalCause = "route_unavailable"
    TerminalCauseSpawnFailed      TerminalCause = "spawn_failed"
    TerminalCauseHarnessFailed    TerminalCause = "harness_failed"
    TerminalCauseProviderFailed   TerminalCause = "provider_failed"
    TerminalCauseToolLoopFailed   TerminalCause = "tool_loop_failed"
    TerminalCauseIterationLimit   TerminalCause = "iteration_limit"
    TerminalCauseBudgetHalted     TerminalCause = "budget_halted"
    TerminalCauseDeadlineExceeded TerminalCause = "deadline_exceeded"
    TerminalCauseContextCancelled TerminalCause = "context_cancelled"
    TerminalCauseCallerDied       TerminalCause = "caller_died"
    TerminalCauseCleanupFailed    TerminalCause = "cleanup_failed"
    TerminalCauseInternalError    TerminalCause = "internal_error"
)

type SessionStage string

const (
    SessionStageRouting      SessionStage = "routing"
    SessionStageSpawn        SessionStage = "spawn"
    SessionStageHarness      SessionStage = "harness"
    SessionStageProvider     SessionStage = "provider"
    SessionStageToolLoop     SessionStage = "tool_loop"
    SessionStageTimeout      SessionStage = "timeout"
    SessionStageCancellation SessionStage = "cancellation"
    SessionStageCleanup      SessionStage = "cleanup"
)

type ServiceFinalData struct {
    // Existing public fields omitted.
    Outcome SessionOutcome `json:"outcome"`
    Cause TerminalCause `json:"cause"`
    Stage SessionStage `json:"stage"`
    PrimaryOutcome SessionOutcome `json:"primary_outcome,omitempty"`
    PrimaryCause TerminalCause `json:"primary_cause,omitempty"`
    PrimaryStage SessionStage `json:"primary_stage,omitempty"`
    ParentSessionID string `json:"parent_session_id,omitempty"`
    Continuation ContinuationDisposition `json:"continuation,omitempty"`
}
```

### Executing-surface route-failure evidence

`ServiceFinalData.RoutingActual.FailureClass` is extensible, typed evidence
about a failure observed by the surface that actually executed. Claude batch
and Claude TUI classifiers produce these stable values:

- `credential_invalid` — the executing surface rejected or could not refresh
  credentials, including HTTP 401 evidence. This is distinct from
  `credential_missing`, which is pre-dispatch configuration evidence and is not
  produced by the Claude execution classifier.
- `quota_exhausted` — the executing surface reported a usage, credit-balance,
  or billing limit. This class does not itself promise a `Retry-After` value or
  a reset time.
- `transport` — connection, name-resolution, timeout, or equivalent transport
  failure from the executing surface.
- `protocol` — an infrastructure protocol failure such as a malformed service
  response or non-authentication HTTP error. Model output quality, task
  failure, and other semantic failures are never `protocol`.
- `unknown` — the executing surface failed without evidence sufficient for a
  more specific class.

The generic native-dispatch class `availability` remains a stable value outside
the Claude classifier's output set. Future minor releases may add classes;
consumers preserve unknown values rather than treating this list as a closed
Go enum.

Failure evidence belongs only to the executing surface. A failed Claude batch
process does not declare Claude TUI, the host account, or any other route
failed, and the inverse is also true. The adapter attaches its class before
terminal delivery. The service-resolved `RouteDecision` is authoritative for
`Harness`, `Provider`, `ServerInstance`, and `Model`: final projection
overwrites conflicting adapter identity with those four values while preserving
the adapter-owned `FailureClass`.

Before emission, adapter diagnostics follow ADR-002 secret and account-data
scrubbing. Claude diagnostics are bounded to 2048 bytes. A TUI adapter retains
only matched fatal lines, never the full rendered frame or user prompt.
`unknown` and semantic failures do not enter route health. Admission of other
classes is service policy; a class is evidence, not an unconditional hard gate
or a claim about sibling surfaces.

Terminalization is the creation of one immutable terminal fact for an accepted
session. `Outcome` is its stable coarse result, `Cause` is its stable reason,
and `Stage` is the Fizeau-owned stage that determined it. Terminalization is
separate from live event delivery and session-log persistence. `Status` remains
a compatibility/detail field; consumers MUST NOT parse `Status` or `Error` to
reconstruct outcome, cause, or stage.

While the caller remains alive, Fizeau MUST deliver exactly one live terminal
event containing that fact before closing the stream. Persistence is
best-effort under the existing session-log reliability policy. After caller
death, Fizeau still MUST terminalize the session and MUST attempt to persist the
terminal fact when its cleanup supervisor or recovery path can write the log,
but there may be neither a live event nor a durable terminal or session-log
record. The pre-spawn lifecycle ownership record remains mandatory recovery
evidence; it is not a successful terminal fact. Delivery and persistence
failures do not create a second terminalization. Absence of a terminal event or
terminal record after caller death MUST NOT be interpreted as success.

The complete session-stage vocabulary is routing, spawn, harness, provider,
tool-loop, timeout, cancellation, and cleanup. The Go/JSON value for tool-loop
is `tool_loop`; there are no workflow-orchestrator stages in this enum.

The required mappings include:

| Condition | Outcome | Cause | Stage |
|-----------|---------|-------|-------|
| Native text-only or empty successful completion | `success` | `completed` | `tool_loop` |
| Wrapped harness completes successfully | `success` | `completed` | `harness` |
| No eligible route | `failed` | `route_unavailable` | `routing` |
| Harness process cannot start | `failed` | `spawn_failed` | `spawn` |
| Harness exits unsuccessfully | `failed` | `harness_failed` | `harness` |
| Native provider exhausts transport retries | `failed` | `provider_failed` | `provider` |
| Tool loop cannot continue | `failed` | `tool_loop_failed` | `tool_loop` |
| Iteration limit reached | `failed` | `iteration_limit` | `tool_loop` |
| Known-cost cap stops the next turn | `failed` | `budget_halted` | `tool_loop` |
| Wall, idle, provider, or tool deadline expires | `timed_out` | `deadline_exceeded` | `timeout` |
| Context is cancelled | `cancelled` | `context_cancelled` | `cancellation` |
| Caller death is observed | `cancelled` | `caller_died` | `cancellation` |
| Owned process tree cannot be reaped by the cleanup deadline | `failed` | `cleanup_failed` | `cleanup` |
| Internal invariant or service implementation fails outside cleanup | `failed` | `internal_error` | The active Fizeau stage |

Every `internal_error` MUST name the active stage; there is no `internal` stage
and an empty stage is invalid. An internal error that prevents cleanup from
finishing is classified as `cleanup_failed / cleanup`, not
`internal_error / cleanup`.

Cleanup failure has deterministic precedence over the primary execution fact.
If the primary execution reaches any tuple and cleanup then misses
`HarnessCleanupTimeout` or establishes a containment escape, the final tuple
MUST be `failed / cleanup_failed / cleanup`. `PrimaryOutcome`, `PrimaryCause`, and
`PrimaryStage` are then required and preserve the pre-cleanup tuple. Those
fields MUST be omitted when cleanup succeeds or no primary tuple exists. Error
text MAY add diagnostics, but it cannot change this precedence.

`SessionStage` has no DDx workflow values. In particular, worktree preparation,
pre- or post-run gates, review, landing, preservation, tracker updates, and bead
closure MUST NOT appear as Fizeau session stages.

### Fizeau session completion is not DDx bead completion

A caller-alive Fizeau session completes observably when its one accepted
`Execute` or `Continue` call has terminalized, cleanup has either succeeded or
reached `HarnessCleanupTimeout`, exactly one terminal event has been delivered,
and the stream has closed. A caller-death session terminalizes after the same
cleanup decision, but live delivery is impossible and durable persistence is
best-effort. A cleanup-failed session is terminal even while recovery ownership
continues asynchronously; terminalization does not claim that surviving
contained processes are already gone. `SessionOutcomeSuccess` means only that
the Fizeau invocation and its cleanup completed successfully.

DDx bead completion is an outer-orchestrator decision. DDx may still need to
inspect the worktree, run acceptance and repository gates, review evidence,
land or preserve commits, update the tracker, and close the bead. Fizeau MUST
NOT emit a bead-success fact, decide a bead outcome, or classify any of those
DDx-owned stages. A successful Fizeau session is necessary evidence for some
DDx attempts; it is never sufficient proof that a bead is complete.

### Wrapped process tree ownership and caller death

Fizeau MUST launch every wrapped harness tree inside a platform containment
boundary established before untrusted harness code runs:

- On Unix-like systems, a trusted Fizeau supervisor MUST lead a dedicated
  process group or session and release untrusted harness code only after the
  ownership record and launch gate are established. Fizeau MUST retain the
  group and process-birth identities and couple caller liveness through a
  supervisor control channel. A direct-child `Pdeathsig` MAY provide secondary
  protection, but it is not sufficient descendant containment.
- On Windows, the direct child and descendants MUST be assigned to a dedicated,
  non-inheritable job object configured for kill-on-job-close before the child
  resumes normal execution.
- Other platforms MUST provide an equivalent boundary or report wrapped
  harness execution unsupported before untrusted code runs.

The same boundary applies to batch runners, PTY sessions, quota and account
probes, model discovery, and every other live harness subprocess. No accepted
invocation may return a live harness process to a package-global or
service-global pool. Shared durable caches are allowed; shared live process
pools across accepted invocations are not.

Fizeau ownership covers the direct child, PTY helpers, adapter supervisors, and
all descendants that remain in the established containment boundary. A harness
MUST NOT daemonize, create an untracked session, request job breakaway, or
otherwise escape that boundary. An observed or credibly detected escape is a
harness-contract violation and produces `failed / cleanup_failed / cleanup`.
Fizeau does not promise control of an intentionally escaped descendant; the
cleanup guarantee applies to processes inside the boundary. Recovery evidence
MUST retain owner and containment process-birth identities, not only a PID or
PGID, so recovery refuses reused identities and diagnoses an escape rather than
claiming the escaped process was reaped.

Ownership starts when Fizeau durably registers the lifecycle boundary before
the launch gate releases untrusted code, and it continues after terminalization
when recovery is pending. Registration failure prevents harness launch. A
harness returning a final payload does not transfer ownership to the caller.
On normal completion, failure, timeout, caller-signalled context cancellation,
or caller death, Fizeau MUST immediately begin cleanup under a service-owned
context that survives request cancellation:

1. Signal the containment boundary for graceful termination.
2. Escalate to the platform's forceful containment termination before
   `HarnessCleanupTimeout` expires.
3. Signal all processes observed in the boundary, reap every process parented by
   Fizeau or its supervisor, and wait for the boundary to become empty until the
   deadline.
4. If the boundary is empty by the deadline, terminalize using the primary
   execution tuple and remove the lifecycle record only after emptiness is
   confirmed. If any contained process remains or containment state is
   indeterminate, terminalize as `failed / cleanup_failed / cleanup`, preserve
   the primary tuple, retain the recovery record, and close the caller-alive
   stream after its one terminal event.

The per-invocation deadline bounds terminal delivery; it is not a claim that no
process can survive it. After `cleanup_failed`, a separate recovery reaper MUST
continue attempting containment termination and reaping. A later startup MAY
use `StaleHarnessReaperGrace` to decide when a persisted record is old enough
for stale recovery; that startup threshold never delays or replaces current
per-invocation cleanup.

Caller death MUST trigger the same containment cleanup through the strongest
liveness mechanism available. The cleanup supervisor or recovery path MUST make
a best-effort attempt to persist `cancelled / caller_died / cancellation`, or
`failed / cleanup_failed / cleanup` when the cleanup deadline is missed. The
caller is gone, so neither a live terminal event nor a durable terminal or
session-log record is guaranteed. The lifecycle ownership record remains until
the boundary is confirmed empty; cleanup and recovery obligations remain.

## Final Measurement Projection

The result returned by compatibility drains and the terminal service event
preserve cost provenance on the public facade. The normative fields are:

```go
type CostSource string

const (
    CostSourceReported   CostSource = "reported"
    CostSourceConfigured CostSource = "configured"
    CostSourceUnknown    CostSource = "unknown"
)

type DrainExecuteResult struct {
    // Other public result fields omitted.
    CostUSD    float64
    CostSource CostSource
}

type ServiceFinalData struct {
    // Other public final-event fields omitted.
    CostUSD    float64    `json:"cost_usd"`
    CostSource CostSource `json:"cost_source"`
}
```

Provider- or gateway-reported billing wins and uses `reported`. Exact
runtime/provider/model pricing supplied by the caller uses `configured` only
when no reported amount exists. If neither source exists, `CostSource` is
`unknown` and `CostUSD` is `-1`. A reported or configured zero is a known zero,
not an unknown value. Terminal service events always emit `cost_source`; callers
must not infer provenance from amount or JSON field presence.

## Routing Types

```go
type RouteRequest struct {
    Policy string
    Model string
    Provider string
    Harness string
    Reasoning Reasoning
    Permissions string
    AllowLocal bool
    Require []string
    MinPower int
    MaxPower int
    EstimatedPromptTokens int
    RequiresTools bool
    CachePolicy string
    Role string
    CorrelationID string
}

type PolicyInfo struct {
    Name string
    MinPower int
    MaxPower int
    AllowLocal bool
    Require []string
    CatalogVersion string
    ManifestSource string
    ManifestVersion int
}

type RouteDecision struct {
    RequestedPolicy string
    PowerPolicy RoutePowerPolicy
    Harness string
    Provider string
    Endpoint string
    ServerInstance string
    Model string
    Reason string
    Sticky RouteStickyState
    Utilization RouteUtilizationState
    Power int
    Candidates []RouteCandidate
}

type RouteCandidate struct {
    Harness string
    Provider string
    Endpoint string
    ServerInstance string
    Model string
    Score float64
    Eligible bool
    Reason string
    FilterReason string
    Components RouteCandidateComponents
    ScoreComponents map[string]float64
    Utilization RouteUtilizationState
}
```

Raw `Model` constraints are normalized against provider-discovered model IDs
before route selection. The resolver uses the canonical fuzzy matcher for
case, vendor-prefix, separator, accelerator, and packaging differences when
the mapping is unambiguous. Ambiguous matches fail with
`ErrModelConstraintAmbiguous`; no match requests fail with
`ErrModelConstraintNoMatch` and return nearby candidates instead of silently
falling back to a different model.

See FEAT-004's candidate-construction rules for the routing-side reference
behavior this contract preserves.

`ListPolicies` returns the canonical v0.11 policy set: `cheap`, `default`,
`smart`, and `air-gapped`. Dropped compatibility names are not listed.

`Policy=air-gapped` carries `Require=["no_remote"]`. A remote provider or
subscription harness pin under that policy fails with
`ErrPolicyRequirementUnsatisfied`. This is deliberate: pins narrow where the
router may look, but they do not weaken policy requirements.

Power bounds are soft scoring inputs in automatic routing. A candidate below
`MinPower` is penalized more than a candidate above `MaxPower`, because using a
weaker model is more likely to fail the task while using a stronger model is
primarily a cost/latency tradeoff. Missing-power and exact-pin-only models are
excluded from unpinned automatic routing but remain visible in inventory and
usable through exact pins when the selected harness/provider can serve them.
Cost, quality, health risk, latency, utilization, and power fit are scoring
inputs. They do not become hard gates unless they make dispatch impossible.
`RouteCandidate.Components` is the stable operator-facing aggregate evidence;
`RouteCandidate.ScoreComponents` preserves the raw routing score component map
used by the engine, including keys such as `base`, `power`, `cost`,
`quota_health`, `deployment_locality`, `utilization`, `context_headroom`, and
`performance` when present.

`fiz models` is the snapshot-first inspection path. It is expected to be quick,
return stale output by default when freshness is pending, and use
`RefreshModels` / `--refresh` for routing-relevant stale fields or
`--refresh-all` for every refreshable field. If no DDx server or other
long-running maintainer is keeping freshness warm, stale output should say so
and suggest an explicit refresh.

## Model Snapshot Freshness

```go
type ModelRefreshScope string

const (
    ModelRefreshRouting ModelRefreshScope = "routing"
    ModelRefreshAll     ModelRefreshScope = "all"
)

type ModelRefreshOptions struct {
    Scope ModelRefreshScope
    Harness string
    Provider string
}

type ModelSnapshotInfo struct {
    Version string
    CapturedAt time.Time
    Fresh bool
    RefreshInFlight bool
    Fields []ModelFieldFreshness
}

type ModelFieldFreshness struct {
    Field string
    Source string
    Fresh bool
    CapturedAt time.Time
    ExpiresAt time.Time
    LastError *StatusError
}
```

`RefreshModels` is synchronous. It blocks until the requested refresh scope is
fresh or has conclusively failed, coalesces with other processes through the
ADR-012 lock/marker contract, and writes snapshot state atomically. A DDx server
maintains asynchronous freshness by calling this method from its own background
task; Fizeau does not require a resident process.

`ModelRefreshRouting` refreshes the fields needed before autorouting can score:
health, quota, model availability/discovery, context/tool/reasoning support,
billing/effective-cost metadata when dynamic, and utilization when available.
`ModelRefreshAll` widens the scope to every refreshable display field.

## Inventory Types

```go
type HarnessInfo struct {
    Name string
    Type string
    Available bool
    Path string
    Error string
    Billing BillingModel
    AutoRoutingEligible bool
    TestOnly bool
    ExactPinSupport bool
    DefaultModel string
    SupportedPermissions []string
    SupportedReasoning []string
    CostClass string
    Quota *QuotaState
    Account *AccountStatus
    UsageWindows []UsageWindow
    LastError *StatusError
    CapabilityMatrix HarnessCapabilityMatrix
}

type ProviderInfo struct {
    Name string
    Type string
    BaseURL string
    Endpoints []ServiceProviderEndpoint
    Status string
    ModelCount int
    Capabilities []string
    Billing BillingModel
    IncludeByDefault bool
    IsDefault bool
    DefaultModel string
    CooldownState *CooldownState
    Auth AccountStatus
    EndpointStatus []EndpointStatus
    Quota *QuotaState
    UsageWindows []UsageWindow
    LastError *StatusError
}

type ModelInfo struct {
    ID string
    Provider string
    ProviderType string
    Harness string
    EndpointName string
    EndpointBaseURL string
    ServerInstance string
    ContextLength int
    ContextSource string
    Utilization RouteUtilizationState
    Capabilities []string
    Cost CostInfo
    EffectiveCostUSD float64
    CostSource string
    ActualCashSpend bool
    PerfSignal PerfSignal
    Power int
    AutoRoutable bool
    ExactPinOnly bool
    Billing BillingModel
    Available bool
    IsDefault bool
    RankPosition int
    SnapshotVersion string
    Freshness []ModelFieldFreshness
}
```

`HarnessInfo.Quota` and `HarnessInfo.Account` are populated by projecting the
internal harness contract defined in
[`CONTRACT-004`](./CONTRACT-004-harness-implementation.md). `ProviderInfo.Quota`
and `ProviderInfo.Auth` use the same projection rules for subscription-backed
providers.

`BillingModel` has four values: unknown (`""`), `fixed`, `per_token`, and
`subscription`. Billing feeds routing cost and default inclusion, but it is
also surfaced on harness, provider, and model inventory rows so operators can
audit why a candidate participated or was skipped.

`Cost` carries catalog/list price inputs. `EffectiveCostUSD` is the normalized
request-local or representative scoring cost used for route comparison.
Subscription rows keep `ActualCashSpend=false` while still carrying
PAYG-equivalent effective cost; pay-per-token rows set `ActualCashSpend=true`
when dispatch would create incremental metered billing.

## Routing Status and Usage

`RouteStatus` is an operator projection over recent routing decisions,
cooldowns, sticky assignments, selected endpoints, candidate health, and
routing-quality metrics. Routing-quality metrics measure how often automatic
routing agrees with users and completes; provider reliability remains a
candidate-level signal and must not be labeled as routing quality.

`UsageReport` aggregates service-owned session logs. It reports token streams,
known cost, unknown-cost sessions, runtime/model attribution, and routing
quality. Consumers use this projection rather than parsing private JSONL
records.

## Session Log and Events

Fizeau owns public `ServiceEvent` construction and session-log projection.
Consumers may subscribe through `Execute` or `TailSessionLog` and may render
stored sessions through `WriteSessionLog` and `ReplaySession`.

Successful completion with empty `final_text` is a valid outcome. Consumers
MUST NOT retry, mark failure, or synthesize fallback text on empty text alone;
they must use the terminal status, process outcome, and error fields to decide
whether the run failed.

Session logs are versioned service artifacts. The v0.11 routing redesign is a
schema break for routing fields: route events and final-event routing summaries
use `policy` and `power_policy`; removed route-reference fields are not emitted.

Replay must remain backward-compatible with older logs where practical. Unknown
or removed fields from pre-v0.11 logs are ignored rather than reintroduced into
the public contract.

Cache-aware cost attribution keeps cache token streams separate from ordinary
input/output pricing. Manifest/runtime pricing fields such as
`cost_cache_read_per_m` and `cost_cache_write_per_m` price cache read/write
tokens when known. For nullable reported cache amounts, explicit zero means the
caller or provider opted out, for example through `CachePolicy=off`; nil means
the harness or provider did not report the amount. Consumers must not treat nil
as zero.

## Mountable CLI

The standalone binary and embedding callers use the same Cobra command tree
from `agentcli`:

```go
package agentcli

func MountCLI(opts ...MountOption) *cobra.Command
func Run(opts Options) int
func ExitCode(err error) (int, bool)
```

`MountCLI` returns a fresh command tree, accepts stream/version/use/description
options, and never calls `os.Exit`. The standalone `cmd/fiz` binary owns
process termination. CLI subcommands consume the public service facade and do
not import internal packages.

## Precedence and Compatibility

v0.11 intentionally removes the v0.10 routing names listed in ADR-009. The
contract does not provide success-path compatibility fallbacks for removed flags
or removed service methods. CLI callers receive usage errors with migration
guidance. Go callers update code to `Policy`, `ListPolicies`, and exact
`Model` pins.

ADR-007 sampling defaults are separate catalog generation policy and are not
changed by this routing contract.

The typed session-lifecycle and continuation surface is introduced in product
API v0.15. Adding `Continue` to `FizeauService` is a Go source-compatibility
break for third-party implementations and mocks of that interface. Adding
`HarnessCleanupTimeout` and the typed terminal and primary-fact fields is an
additive public API/schema change. These changes MUST ship only with the v0.15
API update and migration notes. Callers that only consume a service returned by
`New` do not need to implement the new method.

Compatibility rules after v0.15 are:

- `SessionOutcome`, `TerminalCause`, `SessionStage`, `ContinuationPolicy`, and
  `ContinuationDisposition` values MUST NOT be renamed or reused with different
  semantics within v0.x.
- Stable `FailureClass` values MUST NOT be renamed or reused with different
  semantics within v0.x. Consumers MUST preserve an unknown failure-class
  string for diagnostics and MUST NOT admit it to route health by default.
- New cause or stage values MAY be added in a minor release. Consumers MUST
  preserve unknown strings for diagnostics and MUST NOT interpret an unknown
  outcome as success.
- The JSON `outcome`, `cause`, and `stage` keys are required on every terminal
  event created under v0.15 or later. `parent_session_id` and `continuation` are
  required for sessions created by `Continue` and omitted for ordinary
  `Execute` sessions.
- `primary_outcome`, `primary_cause`, and `primary_stage` are required as a set
  only when cleanup failure supersedes a primary execution tuple. Partial sets
  are invalid.
- `HarnessCleanupTimeout` remains the per-invocation cleanup deadline.
  `StaleHarnessReaperGrace` remains only the startup stale-record minimum age;
  compatibility code MUST NOT alias or derive one from the other.
- Legacy `Status` and `Error` remain additive detail fields for at least one
  minor release after typed terminal facts ship. They cannot override the typed
  fields.
- Removing a field or enum, changing a required mapping, allowing a DDx-owned
  workflow stage into `SessionStage`, or weakening process-tree cleanup requires
  an explicit contract-version break.
- Replay of pre-v0.15 logs MAY derive typed fields only through a documented,
  deterministic legacy mapping. When no mapping is safe, replay reports an
  unknown legacy value and never fabricates success.

## Error Semantics

| Condition | Error / Outcome | Retry | Recovery Expectation |
|-----------|-----------------|-------|----------------------|
| Request rejected before a session starts | Public validation error; no event channel | After correcting request | Caller fixes the request; no cleanup or terminal event is expected because Fizeau accepted no session. |
| Unknown continuation policy | `ErrContinuationPolicyInvalid`; no session | No, without correction | Use one of the three declared policy values. |
| Prior session missing or unusable | `ErrContinuationSessionUnavailable`; no session | After restoring lineage | Supply a readable completed Fizeau session ID. |
| Resume required but route lacks continuation | `ErrContinuationUnsupported`; no session | With a different policy | Choose `prefer_resume` or `fresh_session`; do not guess a harness-specific token. |
| Accepted session fails while caller remains alive | Exactly one terminal event with typed outcome, cause, and stage | Caller policy | Drain the event stream and use typed facts; do not parse error text. |
| Event stream closes without a terminal event while caller is alive | Contract violation | No automatic retry | Treat as failure and retain partial evidence for diagnosis. |
| Internal service failure outside cleanup | `failed / internal_error / <active-stage>` | Caller policy | Preserve the stage and diagnostics; never invent an `internal` stage. |
| Cleanup misses `HarnessCleanupTimeout` after another result | `failed / cleanup_failed / cleanup` plus required primary tuple | No immediate retry | Terminal delivery proceeds after the deadline while the recovery reaper retains ownership. |
| Harness escapes containment | `failed / cleanup_failed / cleanup` | No | Disable or fix the harness; report escaped identity as unresolved rather than reaped. |
| Caller process dies | Required terminalization; best-effort terminal persistence; live event unavailable | Outer orchestrator policy | Containment cleanup and recovery continue from the mandatory lifecycle ownership record; absence of a terminal record is never success evidence. |

## Examples

Required-resume request with no harness-specific knowledge:

```go
events, err := svc.Continue(ctx, fizeau.ServiceContinuationRequest{
    SessionID: "01JZ8W6P4X8Z3FD2A6TQ0R3M1C",
    Prompt:    "Run the focused tests and summarize failures.",
    Policy:    fizeau.ContinuationRequireResume,
    Metadata:  map[string]string{"bead_id": "fizeau-1234"},
})
```

If the prior route supports resume, this creates a new child session with
`continuation="resumed"`. If it does not, `Continue` returns
`ErrContinuationUnsupported` and creates no session.

Fresh fallback without a concrete harness pin:

```go
events, err := svc.Continue(ctx, fizeau.ServiceContinuationRequest{
    SessionID: "01JZ8W6P4X8Z3FD2A6TQ0R3M1C",
    Prompt:    "Try again from the recorded repository state.",
    Policy:    fizeau.ContinuationPreferResume,
    FreshRequest: fizeau.ServiceExecuteRequest{
        Policy:   "default",
        MinPower: 6,
        WorkDir:  "/work/fizeau",
    },
})
```

When resume is unsupported, Fizeau routes the fresh request normally and emits
`parent_session_id` plus `continuation="fresh"`. A terminal
`outcome="success"` proves only that this child Fizeau session completed; DDx
still owns repository gates, landing, and bead closure.

## Conformance-Test Obligations

Every implementation of this contract MUST provide automated conformance tests
that prove:

1. Public Go compile fixtures can call `Execute`, `Continue`, and the session-log
   methods and configure `HarnessCleanupTimeout` without importing `internal/`
   packages.
2. Every accepted session creates exactly one immutable terminalization. A
   caller-alive session delivers that fact in exactly one terminal event before
   channel closure; a caller-death session may deliver no event and persist no
   terminal record without creating a second terminalization or implying
   success. Its lifecycle ownership record remains recovery evidence.
3. No caller-alive terminal event is emitted before contained-process cleanup
   succeeds or `HarnessCleanupTimeout` expires. A missed deadline emits
   `cleanup_failed` once and leaves a recovery record without waiting forever.
4. Unix fixtures prove dedicated process-group/session containment; Windows
   fixtures prove kill-on-job-close containment. Cancellation and timeout
   fixtures exercise a harness grandchild, not only the direct child.
5. A caller-death fixture uses a separate caller process and proves the
   containment boundary receives cleanup, accepts either successful cleanup or
   `cleanup_failed` at `HarnessCleanupTimeout`, and permits neither a live event
   nor a durable terminal record to be required after the caller is gone.
6. Supported continuation reports `resumed`; unsupported `require_resume`
   returns `ErrContinuationUnsupported` without spawning; unsupported
   `prefer_resume` and explicit `fresh_session` create a child session reporting
   `fresh`.
7. Missing lineage and invalid continuation policies return their typed errors
   without creating a session.
8. A structural assertion rejects DDx-only stage values, including worktree,
   gate, review, landing, preservation, tracker, and bead-closure vocabulary.
9. Replay accepts pre-v0.15 terminal records and handles unknown additive enum
   values without treating them as success.
10. `internal_error` maps to `failed` plus each non-cleanup active Fizeau stage.
    An internal cleanup error maps to `cleanup_failed / cleanup`. If cleanup
    supersedes an earlier tuple, all three primary fields preserve that tuple.
11. `HarnessCleanupTimeout` and `StaleHarnessReaperGrace` have independent zero
    defaults and behavior. A stubborn contained process can survive the
    per-invocation deadline only with `cleanup_failed` and an active recovery
    record; the recovery reaper continues and is tested separately.
12. A fixture that attempts containment escape is rejected or terminalizes as
    `cleanup_failed`; test evidence MUST NOT claim the escaped identity was
    reaped.
13. A caller stops receiving events and cancels its context; cleanup begins,
    terminalization remains bounded, and no receiver-liveness heuristic is
    required.
14. A successful Claude-TUI invocation leaves no live PTY or Claude process
    after its terminal event.
15. Recovery refuses a lifecycle record whose PID, PGID, or job identity has a
    mismatched process-birth identity.
16. Cleanup failure or indeterminate boundary state retains its lifecycle
    ownership record until a later recovery confirms emptiness.

## Validation Checklist

- [ ] Normative request, terminal, continuation, and error fields exist on the
  public root-package facade.
- [ ] Fizeau session completion and DDx bead completion are tested as separate
  facts.
- [ ] Platform containment, per-invocation cleanup timeout, recovery reaping,
  and caller-death delivery/persistence modes pass subprocess conformance tests.
- [ ] Caller-signalled cancellation, Claude-TUI per-invocation teardown,
  process-birth identity reuse, and lifecycle-record retention have named tests.
- [ ] Compatibility fixtures cover legacy logs and unknown additive values.
- [ ] The full repository test gate passes with the public conformance suite.
- [ ] Non-normative implementation notes cannot override this contract.
