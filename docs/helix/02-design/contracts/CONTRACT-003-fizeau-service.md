---
ddx:
  id: CONTRACT-003
  depends_on:
    - helix.prd
    - ADR-008
    - ADR-009
  review:
    self_hash: 45761dfe250b161440de53f0809964d89ce41eb4a7a970d0332456bc71ea1e5c
    deps:
      ADR-008: 478df30f7716244dd9b29425624cbe39eab51c589cde5e6610ef456b262c101f
      ADR-009: d9968b4818b0f45508f3e0689b403ff6997c2722924e7457605bc43080ae5a4a
      helix.prd: edcba06017764a15c820d236ed64e1d4d55eb24f4e684fd9974dd328153da68a
    reviewed_at: "2026-07-14T05:16:22Z"
---
# CONTRACT-003: FizeauService Service Interface

**Status:** Draft
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

## Interface

```go
package fizeau

import (
    "context"
    "io"
    "time"
)

type FizeauService interface {
    Execute(ctx context.Context, req ServiceExecuteRequest) (<-chan ServiceEvent, error)
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

Fifteen service methods are public. `Execute` is the primary verb. The list
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

The service may auto-load configuration when `ServiceConfig` is nil and the
config package registered a loader. Embedders that need deterministic behavior
should pass `ServiceConfig` explicitly.

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

## Compatibility

v0.11 intentionally removes the v0.10 routing names listed in ADR-009. The
contract does not provide success-path compatibility fallbacks for removed flags
or removed service methods. CLI callers receive usage errors with migration
guidance. Go callers update code to `Policy`, `ListPolicies`, and exact
`Model` pins.

ADR-007 sampling defaults are separate catalog generation policy and are not
changed by this routing contract.
