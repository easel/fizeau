package fizeau

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/serviceimpl"
	sessionusage "github.com/easel/fizeau/internal/session"
)

// FizeauService is the entire public Go surface of the fizeau module.
// See CONTRACT-003 for the full specification.
type FizeauService interface {
	Execute(ctx context.Context, req ServiceExecuteRequest) (<-chan ServiceEvent, error)
	TailSessionLog(ctx context.Context, sessionID string) (<-chan ServiceEvent, error)
	ListHarnesses(ctx context.Context) ([]HarnessInfo, error)
	ListProviders(ctx context.Context) ([]ProviderInfo, error)
	ListModels(ctx context.Context, filter ModelFilter) ([]ModelInfo, error)
	ListPolicies(ctx context.Context) ([]PolicyInfo, error)
	HealthCheck(ctx context.Context, health HealthTarget) error
	ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDecision, error)
	RecordRouteAttempt(ctx context.Context, attempt RouteAttempt) error
	RouteStatus(ctx context.Context) (*RouteStatusReport, error)

	// Historical session-log projections. CONTRACT-003 owns these so CLI
	// subcommands such as log/replay/usage do not need to read the
	// internal session-log JSONL schema.
	UsageReport(ctx context.Context, opts UsageReportOptions) (*UsageReport, error)
	ListSessionLogs(ctx context.Context) ([]SessionLogEntry, error)
	WriteSessionLog(ctx context.Context, sessionID string, w io.Writer) error
	ReplaySession(ctx context.Context, sessionID string, w io.Writer) error
}

// ServiceConfig provides provider and routing data to the service without
// creating an import cycle from the root package into agent/config.
// Callers wrap their loaded *config.Config in a type that satisfies this interface.
type ServiceConfig interface {
	// ProviderNames returns provider names in stable order (default first).
	ProviderNames() []string
	// DefaultProviderName returns the name of the configured default provider.
	DefaultProviderName() string
	// Provider returns the raw config values for a named provider.
	Provider(name string) (ServiceProviderEntry, bool)
	// HealthCooldown returns the configured cooldown duration (0 = use default 30s).
	HealthCooldown() time.Duration
	// WorkDir is the base directory for file-backed health state.
	WorkDir() string
	// SessionLogDir returns the configured sessions directory.
	SessionLogDir() string
}

// ServiceProviderEntry carries the minimal provider data the service needs.
type ServiceProviderEntry struct {
	Type           string // "openai" | "openrouter" | "lmstudio" | "llama-server" | "ds4" | "omlx" | "lucebox" | "vllm" | "rapid-mlx" | "ollama" | "anthropic"
	BaseURL        string
	ServerInstance string
	Endpoints      []ServiceProviderEndpoint
	APIKey         string
	Headers        map[string]string
	Model          string // configured default model (may be empty)
	Billing        BillingModel
	// IncludeByDefault reports whether this provider participates in automatic
	// routing when the caller does not pin a provider.
	IncludeByDefault    bool
	IncludeByDefaultSet bool
	// ContextWindow is the configured provider-side context override.
	ContextWindow int
	// ConfigError marks this provider entry invalid while allowing the rest of
	// the service config to load. Invalid providers are reported in status
	// surfaces and excluded from routing.
	ConfigError string
	// DailyTokenBudget is the operator-configured per-UTC-day token budget
	// (request + response) for this provider. Zero means no local burn-rate
	// prediction; the provider's own quota signal still applies.
	DailyTokenBudget int
}

// ServiceProviderEndpoint is one configured provider serving endpoint.
type ServiceProviderEndpoint struct {
	Name           string
	BaseURL        string
	ServerInstance string
}

// ServiceOptions configures a FizeauService instance.
//
// seamOptions is embedded so production builds (no testseam tag) get an
// empty struct — making it a compile-time error to set seam fields without
// the build tag. Test builds inherit the four CONTRACT-003 seams
// (FakeProvider, PromptAssertionHook, CompactionAssertionHook,
// ToolWiringHook) automatically.
type ServiceOptions struct {
	seamOptions

	ConfigPath string    // optional override; default $XDG_CONFIG_HOME/fizeau/config.yaml
	Logger     io.Writer // optional; agent writes structured session logs internally regardless

	// ServiceConfig, when non-nil, supplies provider and routing data for
	// ListProviders and HealthCheck. Pass a value wrapping the loaded config.
	// When nil, those methods return an error.
	ServiceConfig ServiceConfig

	// QuotaRefreshDebounce is the minimum interval between live quota probes for
	// a primary subscription harness. Zero uses the service default.
	QuotaRefreshDebounce time.Duration
	// QuotaRefreshStartupWait bounds startup waiting when the durable quota
	// cache is missing, stale, or incomplete. Zero uses the service default.
	QuotaRefreshStartupWait time.Duration
	// QuotaRefreshInterval enables periodic refresh for long-running server
	// processes. Zero disables the timer; cache refresh still happens on startup
	// and service activity.
	QuotaRefreshInterval time.Duration
	// QuotaRefreshContext optionally cancels the periodic server refresh worker.
	// When nil, the worker uses context.Background().
	QuotaRefreshContext context.Context

	// CatalogProbeTimeout bounds live /v1/models discovery during routing.
	// Zero uses the service default of 2s.
	CatalogProbeTimeout time.Duration
	// CatalogReloadTimeout bounds stale-while-revalidate catalog reloads.
	// Zero uses the service default of 30s.
	CatalogReloadTimeout time.Duration

	// LocalCostUSDPer1kTokens is the operator-supplied electricity/operations
	// estimate for local endpoint providers under the embedded agent harness.
	// Zero means local endpoint cost is unknown.
	LocalCostUSDPer1kTokens float64
	// SubscriptionCostCurve optionally overrides the default subscription
	// effective-cost curve used by routing.
	SubscriptionCostCurve *SubscriptionCostCurve

	// SessionLogDir overrides the directory used by historical session-log
	// projections (UsageReport, ListSessionLogs, WriteSessionLog,
	// ReplaySession). Empty falls back to ServiceConfig.SessionLogDir().
	// Per-Execute requests still set their own
	// ServiceExecuteRequest.SessionLogDir.
	SessionLogDir string

	// StaleHarnessReaperGrace is the minimum age before a startup reaper may
	// terminate an owned subprocess record. Zero uses the default grace window.
	StaleHarnessReaperGrace time.Duration
}

// SubscriptionCostCurve tunes effective subscription cost by quota utilization.
// Thresholds are percentages used, and multipliers are applied to the
// equivalent pay-per-token catalog rate.
type SubscriptionCostCurve struct {
	FreeUntilPercent   int
	LowUntilPercent    int
	MediumUntilPercent int
	LowMultiplier      float64
	MediumMultiplier   float64
	HighMultiplier     float64
}

// QuotaState is a live quota snapshot for a harness. Nil means not applicable.
type QuotaState struct {
	Windows    []harnesses.QuotaWindow `json:"windows"`
	CapturedAt time.Time               `json:"captured_at"`
	Fresh      bool                    `json:"fresh"`
	Source     string                  `json:"source,omitempty"`
	Status     string                  `json:"status,omitempty"` // ok|stale|unavailable|unauthenticated|unknown
	LastError  *StatusError            `json:"last_error,omitempty"`
}

// StatusError describes the most recent normalized status error for a harness,
// provider, or endpoint.
type StatusError struct {
	Type      string    // unavailable|unauthenticated|error
	Detail    string    // human-readable detail, safe for diagnostics
	Source    string    // config path, endpoint, cache path, or probe name
	Timestamp time.Time // zero when the source did not include a timestamp
}

// AccountStatus describes authentication/account state without exposing
// provider-specific native files to consumers.
type AccountStatus struct {
	Authenticated   bool
	Unauthenticated bool
	Email           string
	PlanType        string
	OrgName         string
	Source          string
	CapturedAt      time.Time
	Fresh           bool
	Detail          string
}

// UsageWindow describes normalized usage attribution over a time window.
// Empty token/cost totals mean the service has no historical usage source yet.
type UsageWindow struct {
	Name                string
	Source              string
	CapturedAt          time.Time
	Fresh               bool
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
	CacheReadTokens     int
	CacheWriteTokens    int
	ReasoningTokens     int
	CostUSD             float64
	KnownCostUSD        *float64
	UnknownCostSessions int
}

// EndpointStatus describes one configured provider endpoint probe.
type EndpointStatus struct {
	Name           string
	BaseURL        string
	ServerInstance string
	ProbeURL       string
	Status         string // connected|unreachable|unauthenticated|error|unknown
	Source         string
	CapturedAt     time.Time
	Fresh          bool
	LastSuccessAt  time.Time
	ModelCount     int
	LastError      *StatusError
}

// HarnessInfo describes a registered harness as defined in CONTRACT-003.
type HarnessInfo struct {
	Name                 string
	Type                 string // "native" | "subprocess"
	Available            bool
	Path                 string
	Error                string
	Billing              BillingModel
	AutoRoutingEligible  bool
	TestOnly             bool
	ExactPinSupport      bool
	DefaultModel         string   // built-in default model when no override is supplied
	SupportedPermissions []string // subset of {"safe","supervised","unrestricted"}
	SupportedReasoning   []string // values such as {"low","medium","high","xhigh","max"}
	CostClass            string   // "local" | "cheap" | "medium" | "expensive"
	Quota                *QuotaState
	Account              *AccountStatus
	UsageWindows         []UsageWindow
	LastError            *StatusError
	CapabilityMatrix     HarnessCapabilityMatrix
}

// CooldownState describes an active routing cooldown for a provider.
type CooldownState struct {
	Reason      string    // "consecutive_failures" | "route_attempt_failure" | "manual" | etc.
	Until       time.Time // when the cooldown expires
	FailCount   int       // number of consecutive failures that triggered the cooldown
	LastError   string    // last recorded error message, if available
	LastAttempt time.Time // when the feedback was recorded
}

// ProviderInfo describes a provider with live status per CONTRACT-003.
type ProviderInfo struct {
	Name             string
	Type             string // "openai" | "openrouter" | "lmstudio" | "omlx" | "ollama" | "anthropic" | "virtual"
	BaseURL          string
	Endpoints        []ServiceProviderEndpoint
	Status           string // "connected" | "unreachable" | "error: <msg>"
	ModelCount       int
	Capabilities     []string       // e.g. {"tool_use","streaming","json_mode"}
	Billing          BillingModel   // "fixed" | "per_token" | "subscription" | ""
	IncludeByDefault bool           // participates in unpinned/default routing
	IsDefault        bool           // matches the configured default_provider
	DefaultModel     string         // per-provider configured default model, if any
	CooldownState    *CooldownState // nil if not in cooldown
	Auth             AccountStatus
	EndpointStatus   []EndpointStatus
	Quota            *QuotaState
	UsageWindows     []UsageWindow
	LastError        *StatusError
}

// CostInfo holds per-token cost metadata for a model.
type CostInfo struct {
	InputPerMTok  float64 // USD per 1M input tokens; 0 = unknown/free
	OutputPerMTok float64 // USD per 1M output tokens; 0 = unknown/free
}

// PerfSignal holds observed performance data for a model.
type PerfSignal struct {
	SpeedTokensPerSec float64 // 0 = unknown
	SWEBenchVerified  float64 // 0 = unknown
}

// ModelInfo describes a model with full metadata per CONTRACT-003.
type ModelInfo struct {
	ID                            string
	Provider                      string
	ProviderType                  string
	Harness                       string
	EndpointName                  string
	EndpointBaseURL               string
	ServerInstance                string
	ContextLength                 int
	ContextSource                 string
	Utilization                   RouteUtilizationState
	Capabilities                  []string
	Cost                          CostInfo
	PerfSignal                    PerfSignal
	Power                         int
	AutoRoutable                  bool
	ExactPinOnly                  bool
	Billing                       BillingModel
	ActualCashSpend               bool
	EffectiveCost                 float64
	EffectiveCostSource           string
	SupportsTools                 bool
	DeploymentClass               string
	HealthFreshnessAt             time.Time
	HealthFreshnessSource         string
	QuotaFreshnessAt              time.Time
	QuotaFreshnessSource          string
	ModelDiscoveryFreshnessAt     time.Time
	ModelDiscoveryFreshnessSource string
	Available                     bool
	IsDefault                     bool // matches the configured default model
	RankPosition                  int  // ordinal in latest discovery rank; -1 if unranked
}

// ModelFilter filters ListModels results.
type ModelFilter struct {
	Harness  string
	Provider string
}

type PolicyInfo struct {
	Name            string
	MinPower        int
	MaxPower        int
	AllowLocal      bool
	Require         []string
	CatalogVersion  string
	ManifestSource  string
	ManifestVersion int
}

// HealthTarget identifies what to health-check.
type HealthTarget struct {
	Type string // "harness" | "provider"
	Name string
}

// RouteRequest specifies a routing query.
type RouteRequest struct {
	Policy      string // optional named policy bundle: cheap|default|smart|air-gapped
	Model       string
	Provider    string
	Harness     string
	Reasoning   Reasoning
	Permissions string
	AllowLocal  bool
	Require     []string
	MinPower    int
	MaxPower    int

	// EstimatedPromptTokens, when > 0, filters out candidates whose context
	// window cannot accommodate the prompt (with a safety margin).
	EstimatedPromptTokens int

	// RequiresTools, when true, filters out candidates that do not support
	// tool calling.
	RequiresTools bool

	// CachePolicy mirrors ServiceExecuteRequest.CachePolicy. Routing decisions
	// today do not act on it; it is carried through so callers using
	// ResolveRoute as a public surface can plumb the same opt-out the
	// Execute path honors.
	CachePolicy string

	// Role tags the kind of work this call performs (e.g. "implementer",
	// "reviewer", "decomposer"). Observational only — it does NOT enter the
	// routing precedence chain. Mirrors ServiceExecuteRequest.Role so
	// ResolveRoute previews don't diverge from Execute.
	Role string

	// CorrelationID joins calls that share work context (e.g.
	// "bead_123:attempt_4"). Observational only — it does NOT enter the
	// routing precedence chain. Mirrors ServiceExecuteRequest.CorrelationID.
	CorrelationID string

	// ExcludedRoutes lists (Provider, Model, Endpoint) combinations the caller
	// has determined are currently unavailable. The router skips any candidate
	// matching an entry. Provider is required; Model and Endpoint are optional
	// (empty matches any value for that field).
	//
	// Use this to communicate caller-side health signals across calls without
	// redesigning provider config. The routing engine records excluded
	// candidates with FilterReasonCallerExcluded for observability.
	ExcludedRoutes []ExcludedRoute
}

// ExcludedRoute identifies a (Provider, Model, optional Endpoint) combination
// that the caller has determined is currently unavailable or unsuitable. Used
// with RouteRequest.ExcludedRoutes to express caller-side health signals.
type ExcludedRoute struct {
	// Provider is the provider identity to exclude (required).
	Provider string
	// Model restricts the exclusion to a specific model. Empty matches any model
	// on the provider.
	Model string
	// Endpoint restricts the exclusion to a specific named endpoint. Empty
	// matches any endpoint.
	Endpoint string
}

// Valid CachePolicy values.
const (
	CachePolicyDefault = "default"
	CachePolicyOff     = "off"
)

// ValidateCachePolicy returns nil when v is one of the accepted CachePolicy
// values ("", "default", "off") and a typed error otherwise. The empty
// string is treated as "default".
func ValidateCachePolicy(v string) error {
	switch v {
	case "", CachePolicyDefault, CachePolicyOff:
		return nil
	default:
		return fmt.Errorf("invalid CachePolicy %q: want \"\", %q, or %q", v, CachePolicyDefault, CachePolicyOff)
	}
}

// ValidatePowerBounds returns nil when the optional numeric routing power
// bounds are unset or coherent. Zero means "unset"; positive values are on
// the catalog's 1-10 power scale.
func ValidatePowerBounds(minPower, maxPower int) error {
	if minPower < 0 {
		return fmt.Errorf("invalid MinPower %d: must be >= 0", minPower)
	}
	if maxPower < 0 {
		return fmt.Errorf("invalid MaxPower %d: must be >= 0", maxPower)
	}
	if minPower > 0 && maxPower > 0 && maxPower < minPower {
		return fmt.Errorf("invalid power bounds: MaxPower %d must be >= MinPower %d", maxPower, minPower)
	}
	return nil
}

// RouteDecision is the result of ResolveRoute.
type RouteDecision struct {
	// RequestedPolicy is the caller-supplied policy, when any.
	RequestedPolicy string
	// SnapshotCapturedAt records when the model snapshot used for scoring
	// was assembled.
	SnapshotCapturedAt time.Time
	// PowerPolicy records the effective policy inputs used for this
	// resolution. It stays separate from the chosen model so operator
	// surfaces can explain policy without re-deriving it.
	PowerPolicy RoutePowerPolicy
	// Harness is the selected harness name.
	Harness string
	// Provider is the selected provider for native agent routes.
	Provider string
	// Endpoint is the selected named endpoint when the provider exposes more
	// than one endpoint.
	Endpoint string
	// ServerInstance is the normalized server identity used for sticky
	// affinity and route evidence.
	ServerInstance string
	// Model is the selected concrete model.
	Model string
	// Reason summarizes why the selected candidate won.
	Reason string
	// Sticky captures whether this decision reused an existing sticky lease
	// or created a new one.
	Sticky RouteStickyState
	// Utilization captures the endpoint sample that informed the selected
	// candidate, when known.
	Utilization RouteUtilizationState
	// Power is the catalog-projected power of the selected Model
	// (per CONTRACT-003 § Catalog Power Projection). 0 means
	// unknown/exact-pin-only/no catalog entry. DDx callers read this
	// to compute next-attempt MinPower without importing catalog code.
	Power int
	// Candidates is the full ranked decision trace, including rejected
	// candidates and their rejection reasons.
	Candidates []RouteCandidate
}

// RoutePowerPolicy captures the numeric power-policy inputs associated with
// one ResolveRoute call.
type RoutePowerPolicy struct {
	PolicyName string
	MinPower   int
	MaxPower   int
}

// RouteCandidate is one routing candidate evaluated by ResolveRoute.
type RouteCandidate struct {
	// Harness is the candidate harness name.
	Harness string
	// Provider is the candidate provider name for native agent routes.
	Provider string
	// Billing is the candidate payment model.
	Billing BillingModel
	// ActualCashSpend reports whether selecting the candidate would create
	// metered cash spend.
	ActualCashSpend bool
	// Endpoint is the provider endpoint name when applicable.
	Endpoint string
	// ServerInstance is the normalized server identity for the candidate.
	ServerInstance string
	// Model is the candidate concrete model.
	Model string
	// Score is the routing score assigned before final ordering.
	Score float64
	// CostUSDPer1kTokens is the estimated blended USD cost per 1,000 tokens.
	CostUSDPer1kTokens float64
	// CostSource indicates whether cost came from catalog, subscription,
	// user-config, or is unknown.
	CostSource string
	// EffectiveCost is the score-time cost estimate surfaced explicitly for
	// operator traces and session logs.
	EffectiveCost float64
	// EffectiveCostSource records where EffectiveCost came from.
	EffectiveCostSource string
	// Eligible reports whether the candidate passed all routing gates.
	Eligible bool
	// Reason is the scoring reason for eligible candidates or the rejection
	// reason for ineligible candidates.
	Reason string
	// FilterReason names the gate that disqualified an ineligible candidate.
	// Empty when Eligible. See the FilterReason* constants.
	FilterReason string
	// ContextLength is the resolved maximum context window for the candidate.
	ContextLength int
	// ContextSource records where ContextLength came from.
	ContextSource string
	// SourceStatus records the model snapshot status for this candidate.
	SourceStatus string
	// AutoRoutable reports whether the snapshot/catalog marks the candidate as
	// eligible for automatic routing.
	AutoRoutable bool
	// ExactPinOnly reports whether the candidate is only eligible when pinned
	// explicitly.
	ExactPinOnly bool
	// ExclusionReason explains why the snapshot marked the candidate as
	// excluded from automatic routing.
	ExclusionReason string
	// SnapshotCapturedAt records when the model snapshot used for scoring
	// was assembled.
	SnapshotCapturedAt time.Time
	// HealthFreshnessAt/Source, QuotaFreshnessAt/Source, and
	// ModelDiscoveryFreshnessAt/Source expose the freshness state that fed the
	// candidate's routing evidence.
	HealthFreshnessAt             time.Time
	HealthFreshnessSource         string
	QuotaFreshnessAt              time.Time
	QuotaFreshnessSource          string
	ModelDiscoveryFreshnessAt     time.Time
	ModelDiscoveryFreshnessSource string
	// Components carries the raw score inputs plus the SD-005 score-evidence
	// breakdown used to explain the final Score without parsing Reason.
	Components RouteCandidateComponents
	// Utilization carries the normalized load sample used by the router.
	Utilization RouteUtilizationState
}

// FilterReason* enumerate the canonical disqualification reasons surfaced
// in RouteCandidate.FilterReason and the routing-decision event.
const (
	FilterReasonUnhealthy                   = "unhealthy"
	FilterReasonContextTooSmall             = "context_too_small"
	FilterReasonNoToolSupport               = "no_tool_support"
	FilterReasonReasoningUnsupported        = "reasoning_unsupported"
	FilterReasonScoredBelowTop              = "scored_below_top"
	FilterReasonProviderExcludedFromDefault = "provider_excluded_from_default_routing"
	FilterReasonMeteredOptInRequired        = "metered_opt_in_required"
)

// RouteCandidateComponents breaks down the inputs that fed a candidate's
// final Score so consumers can explain rankings without parsing the
// free-form Reason. Zero fields mean "unknown / not contributing".
type RouteCandidateComponents struct {
	// Power is the catalog model power used by automatic routing. Zero means
	// unknown or not contributing.
	Power int
	// Cost is the per-1k-token cost expressed as a numeric component (USD).
	// Mirrors RouteCandidate.CostUSDPer1kTokens; surfaced here so the event
	// payload carries a single component bundle.
	Cost float64
	// CostClass is the candidate cost class used by the router.
	CostClass string
	// LatencyMS is the observed median latency for this candidate, in
	// milliseconds. Zero when no observations are available.
	LatencyMS float64
	// SpeedTPS is the observed output speed in tokens per second. Zero when
	// no observations are available.
	SpeedTPS float64
	// Utilization is the normalized load signal used to bias ranking. Zero
	// means unknown or not contributing.
	Utilization float64
	// SuccessRate is the observed success rate (0–1) for this candidate.
	// Negative means insufficient data; zero means unknown.
	SuccessRate float64
	// QuotaOK/QuotaPercentUsed/QuotaTrend are subscription quota inputs used
	// by routing. They are zero values for non-subscription candidates.
	QuotaOK          bool
	QuotaPercentUsed int
	QuotaTrend       string
	// Capability is a coarse capability score derived from the candidate's
	// cost class / surface (higher = more capable).
	Capability float64
	// ContextHeadroom is the remaining context after subtracting the request's
	// minimum required prompt window. Zero means unknown or no spare room.
	ContextHeadroom int
	// StickyAffinity is the bonus applied when the candidate matches the
	// request's sticky server-instance assignment. Zero means no match.
	StickyAffinity float64

	// SD-005 score evidence fields. These mirror the public route trace and
	// keep the score decomposition aligned with the design vocabulary.
	PowerWeightedCapability float64 `json:"power_weighted_capability"`
	PowerHintFit            float64 `json:"power_hint_fit"`
	LatencyWeight           float64 `json:"latency_weight"`
	PlacementBonus          float64 `json:"placement_bonus"`
	QuotaBonus              float64 `json:"quota_bonus"`
	MarginalCostPenalty     float64 `json:"marginal_cost_penalty"`
	AvailabilityPenalty     float64 `json:"availability_penalty"`
	StaleSignalPenalty      float64 `json:"stale_signal_penalty"`
}

const (
	ContextSourceProviderAPI    = "provider_api"
	ContextSourceProviderConfig = "provider_config"
	ContextSourceCatalog        = "catalog"
	ContextSourceDefault        = "default"
	ContextSourceUnknown        = "unknown"
)

// RouteStickyState describes sticky routing evidence without exposing the
// underlying key.
type RouteStickyState struct {
	KeyPresent     bool    `json:"key_present,omitempty"`
	Assignment     string  `json:"assignment,omitempty"`
	ServerInstance string  `json:"server_instance,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	Bonus          float64 `json:"bonus"`
}

// RouteUtilizationState summarizes the live utilization sample associated
// with a candidate or selected endpoint.
type RouteUtilizationState struct {
	Source         string    `json:"source,omitempty"`
	Freshness      string    `json:"freshness,omitempty"`
	ActiveRequests *int      `json:"active_requests,omitempty"`
	QueuedRequests *int      `json:"queued_requests,omitempty"`
	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
	CachePressure  *float64  `json:"cache_pressure,omitempty"`
	ObservedAt     time.Time `json:"observed_at,omitempty"`
}

// RouteAttempt is caller feedback about one attempted route candidate.
// Status="success" clears matching active failures; any other non-empty status
// records a same-process failure until the service health cooldown expires.
type RouteAttempt struct {
	Harness   string
	Provider  string
	Model     string
	Endpoint  string
	Status    string
	Reason    string
	Error     string
	Duration  time.Duration
	Timestamp time.Time // zero = time.Now()
}

// RouteStatusReport is returned by RouteStatus.
//
// RoutingQuality (ADR-006 §5) is the operator-facing measure of how often
// auto-routing produces an acceptable decision. It is intentionally
// distinct from per-(provider, model) provider-reliability surfaced on
// each RouteCandidateStatus: the two compose, and conflating them is the
// UI bug ADR-006 fixes.
type RouteStatusReport struct {
	Routes             []RouteStatusEntry
	GeneratedAt        time.Time
	SnapshotCapturedAt time.Time
	GlobalCooldowns    []CooldownState
	// RoutingQuality holds the three first-class routing-quality metrics
	// over a recent window (last RouteStatusRoutingQualityWindow requests).
	RoutingQuality RoutingQualityMetrics
}

// RouteStatusRoutingQualityWindow caps how many recent Execute calls
// contribute to RouteStatusReport.RoutingQuality. ADR-006 §5 calls for a
// "recent window"; the constant is exported so operator UIs can label the
// metric appropriately.
const RouteStatusRoutingQualityWindow = 100

// RouteStatusEntry describes the live provider candidates serving one model.
type RouteStatusEntry struct {
	Model                  string
	Strategy               string // informational; route tables are not user-authored
	SelectedEndpoint       string
	SelectedServerInstance string
	Sticky                 RouteStickyState
	Candidates             []RouteCandidateStatus
	LastDecision           *RouteDecision // most recent ResolveRoute result for this key (cached)
	LastDecisionAt         time.Time
}

// RouteCandidateStatus describes a single live provider/model candidate.
//
// ProviderReliabilityRate is the per-(provider, model) observed completion
// rate (the legacy "success rate" metric). Per ADR-006 §5 it is labeled
// "provider reliability" to distinguish it from RoutingQualityMetrics on
// the parent report — the two metrics measure different things and are
// surfaced as separate fields.
type RouteCandidateStatus struct {
	Provider                      string
	Endpoint                      string
	Model                         string
	ServerInstance                string
	Billing                       BillingModel
	ActualCashSpend               bool
	EffectiveCost                 float64
	EffectiveCostSource           string
	Priority                      int
	Healthy                       bool
	Cooldown                      *CooldownState
	SourceStatus                  string
	AutoRoutable                  bool
	ExactPinOnly                  bool
	ExclusionReason               string
	Power                         int
	ContextLength                 int
	CostInputPerMTok              float64
	CostOutputPerMTok             float64
	RecentLatencyMS               float64 // observation-derived; 0 when unavailable
	ProviderReliabilityRate       float64 // 0-1; 0 when unavailable. Legacy success-rate field, relabeled per ADR-006 §5 to disambiguate from routing-quality.
	QuotaRemaining                *int
	SnapshotCapturedAt            time.Time
	HealthFreshnessAt             time.Time
	HealthFreshnessSource         string
	QuotaFreshnessAt              time.Time
	QuotaFreshnessSource          string
	ModelDiscoveryFreshnessAt     time.Time
	ModelDiscoveryFreshnessSource string
}

// ServiceEvent is a contract-level event (mirrors harnesses.Event).
type ServiceEvent = harnesses.Event

// ServiceExecuteRequest is the public ExecuteRequest type per CONTRACT-003.
// See docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md
// (§"Public types") for the canonical shape; this struct is its in-process
// twin under the agent module.
type ServiceExecuteRequest struct {
	Prompt            string
	SystemPrompt      string
	Model             string
	Provider          string
	Harness           string
	Policy            string
	WorkDir           string
	Temperature       *float32
	TopP              *float64
	TopK              *int
	MinP              *float64
	RepetitionPenalty *float64
	Seed              *int64
	// SamplingSource is the comma-separated layer attribution produced by
	// internal/sampling.Resolve. Plumbed through to the llm.request
	// telemetry event for ADR-007 override-tracking; never on the wire.
	SamplingSource string
	Reasoning      Reasoning
	NoStream       bool
	Permissions    string
	// Tools overrides the built-in native agent tool set when Harness is
	// "fiz". Nil uses the native built-ins for ToolPreset and WorkDir.
	Tools []Tool
	// ToolPreset is passed through to BuiltinToolsForPreset when Tools is nil.
	// Empty uses the default tool set.
	ToolPreset string

	// PlanningMode, when true, performs one no-tool LLM "planning" call
	// before the main native agent loop and injects the response as an
	// assistant message wrapped in <plan> tags. The benchmark tool preset
	// auto-enables planning; this flag is the per-request opt-in for other
	// callers (e.g. the CLI --plan flag). Only honored when Harness=="fiz"
	// (the native loop). See internal/core.Request.PlanningMode.
	PlanningMode bool

	// EstimatedPromptTokens, when > 0, drives auto-selection's
	// context-window gate (filter out candidates whose context window is
	// too small for the prompt + safety margin).
	EstimatedPromptTokens int
	// RequiresTools, when true, drives auto-selection's tool-support gate
	// (filter out candidates that cannot invoke tools).
	RequiresTools bool
	MinPower      int
	MaxPower      int

	// CachePolicy is the public opt-out for prompt caching. Empty (the zero
	// value) and "default" both request the per-provider default caching
	// behavior; "off" disables caching for this request. Any other value is
	// rejected at the service boundary (see ValidateCachePolicy). Providers
	// that do not implement caching ignore the field; this field is read by
	// the Anthropic cache_control writer (bead C) and the cache-aware cost
	// attribution path (bead D).
	CachePolicy string

	// Three independent timeout knobs:
	//   Timeout         — wall-clock cap on the entire request.
	//   IdleTimeout     — streaming-quiet cap; per-stream gap.
	//   ProviderTimeout — per-HTTP-request cap to the provider.
	Timeout         time.Duration
	IdleTimeout     time.Duration
	ProviderTimeout time.Duration

	// Optional native-loop overrides used when Harness == "fiz". These let
	// the CLI and other callers route fully-resolved execution settings through
	// the service path instead of maintaining a divergent direct loop path.
	MaxIterations           int
	MaxTokens               int
	ReasoningByteLimit      int
	CompactionContextWindow int
	CompactionReserveTokens int

	// CostCapUSD is the per-run cost cap in USD, mirrored to
	// internal/core.Request.CostCapUSD. When > 0, the native loop halts
	// before issuing the next llm.request once running known cost plus the
	// projected next-turn cost would meet or exceed the cap; the resulting
	// final event reports Status="budget_halted". Zero means no cap. Per
	// FEAT-005 §28 / AC-FEAT-005-07, the cap requires turn cost to be known;
	// unknown-cost runs proceed past the cap with a stderr warning. Honored
	// only when Harness=="fiz" (the native loop) — subprocess harnesses
	// manage cost externally.
	CostCapUSD float64

	// Optional stall policy. When non-nil agent enforces and ends with
	// Status="stalled" if any limit hits. Default policy applies when nil.
	StallPolicy *StallPolicy

	// SessionLogDir overrides the default session-log directory for this
	// request (e.g. an execute-bead per-bundle evidence directory).
	SessionLogDir string

	// SelectedRoute is the configured model-route name the caller picked
	// (e.g. "code-pool"). Recorded into the service-owned session log so
	// post-hoc routing analytics can correlate logs to route keys without
	// reconstructing attribution from the event stream.
	SelectedRoute string

	// Metadata is bidirectional: echoed back in every Event AND stamped
	// onto every line of the session log so external consumers correlate.
	Metadata map[string]string

	// Role tags the kind of work this call performs (e.g. "implementer",
	// "reviewer", "decomposer", "summarizer"). Observational: echoed into
	// the routing_decision and final event Metadata, plus the session-log
	// header. Per CONTRACT-003 it is NOT part of the selection precedence
	// chain (Day 1) and does NOT affect routing. Empty means unset.
	//
	// Normalization: lowercased, alphanumeric + hyphen only, max 64 chars;
	// invalid values are rejected pre-dispatch with RoleNormalizationError.
	Role string
	// CorrelationID links calls that share work context (e.g.
	// "bead_123:attempt_4") so reviewer/implementer/retry attempts can be
	// joined in logs and aggregations. Observational: echoed into
	// routing_decision and final event Metadata, plus the session-log
	// header. Per CONTRACT-003 it is NOT part of the selection precedence
	// chain (Day 1) and does NOT affect routing. Empty means unset.
	//
	// Normalization: printable ASCII (no control chars, no whitespace
	// except hyphen, colon, underscore), max 256 chars; invalid values
	// are rejected pre-dispatch with CorrelationIDNormalizationError.
	CorrelationID string
}

// StallPolicy bounds how long the agent will spin without making progress
// before terminating with Status="stalled". A nil policy resolves to the
// default in service_execute.go.
type StallPolicy struct {
	MaxReadOnlyToolIterations int // 0 = disabled
}

// service is the concrete FizeauService implementation.
type service struct {
	opts     ServiceOptions
	registry *harnesses.Registry
	hub      *sessionHub

	// lastDecisionMu guards lastDecisionCache.
	lastDecisionMu sync.RWMutex
	// lastDecisionCache maps route key → (decision, time). Populated by
	// ResolveRoute; read by RouteStatus.
	lastDecisionCache map[string]lastDecisionEntry

	routeHealth      *routehealth.Store
	routeLeases      *routehealth.LeaseStore
	routeUtilization *routehealth.UtilizationStore

	// catalog is the service-scope model-catalog cache. Populated lazily
	// on first use by routing + chat paths; shared across requests so the
	// same endpoint isn't probed per-dispatch during a drain. See
	// service_catalog_cache.go.
	catalog *catalogCache

	// routingQuality records routing-quality observations (ADR-006 §5).
	// Populated by Execute on every request and read by RouteStatus and
	// UsageReport.
	routingQuality *routingQualityStore

	// providerQuota is the per-provider quota state machine. Routing reads
	// the projected exhausted set on every Resolve so quota_exhausted
	// providers are excluded from candidate selection.
	providerQuota *ProviderQuotaStateStore

	// providerBurnRate observes per-provider token consumption against the
	// operator-configured daily_token_budget and pre-emptively transitions
	// providers into quota_exhausted when local burn-rate predicts the
	// budget will be exceeded before the next UTC daily reset.
	providerBurnRate *ProviderBurnRateTracker

	runtime serviceimpl.Runtime
}

const (
	defaultCatalogProbeTimeout  = 2 * time.Second
	defaultCatalogReloadTimeout = 30 * time.Second
)

func (o ServiceOptions) catalogProbeTimeout() time.Duration {
	if o.CatalogProbeTimeout > 0 {
		return o.CatalogProbeTimeout
	}
	return defaultCatalogProbeTimeout
}

func (o ServiceOptions) catalogRefreshTimeout() time.Duration {
	if o.CatalogReloadTimeout > 0 {
		return o.CatalogReloadTimeout
	}
	return defaultCatalogReloadTimeout
}

func (s *service) now() time.Time {
	return s.runtime.Now()
}

// lastDecisionEntry caches the most recent RouteDecision for a route key.
type lastDecisionEntry struct {
	decision *RouteDecision
	at       time.Time
}

// loadServiceConfig, when non-nil, is called by New to load a ServiceConfig
// from a directory path when opts.ServiceConfig is nil. It is registered by
// the config package via init() to break the import cycle (config imports root).
var loadServiceConfig func(dir string) (ServiceConfig, error)

// RegisterConfigLoader is called by the config package's init() to install the
// config-loading function. Do not call this from application code.
func RegisterConfigLoader(fn func(dir string) (ServiceConfig, error)) {
	loadServiceConfig = fn
}

// New constructs a FizeauService. When opts.ServiceConfig is nil, New attempts to
// load configuration automatically:
//  1. If opts.ConfigPath is non-empty, load from filepath.Dir(opts.ConfigPath).
//  2. Otherwise, load from the default global config location.
//
// Automatic loading requires the config package to be imported somewhere in the
// binary (it registers the loader via init). If the config package is not
// imported and ServiceConfig is nil, the service starts without provider config
// (ListProviders/HealthCheck will return errors until config is injected).
func New(opts ServiceOptions) (FizeauService, error) {
	if opts.ServiceConfig == nil && loadServiceConfig != nil && shouldAutoLoadServiceConfig(opts) {
		dir := ""
		if opts.ConfigPath != "" {
			dir = filepath.Dir(opts.ConfigPath)
		}
		sc, err := loadServiceConfig(dir)
		if err != nil {
			return nil, fmt.Errorf("agent.New: load config: %w", err)
		}
		opts.ServiceConfig = sc
	}
	svc := &service{
		opts:             opts,
		registry:         harnesses.NewRegistry(),
		hub:              newSessionHub(),
		runtime:          serviceimpl.NewRuntime(serviceimpl.RuntimeDeps{}),
		catalog:          newCatalogCache(catalogCacheOptions{AsyncRefreshTimeout: opts.catalogRefreshTimeout()}),
		routeHealth:      routehealth.NewStore(),
		routeLeases:      routehealth.NewLeaseStore(),
		routeUtilization: routehealth.NewUtilizationStore(),
		routingQuality:   newRoutingQualityStore(),
		providerQuota:    NewProviderQuotaStateStore(),
		providerBurnRate: NewProviderBurnRateTracker(),
	}
	// Hydrate per-provider daily_token_budget from ServiceConfig so the
	// burn-rate tracker can predict exhaustion before the upstream quota
	// signal arrives.
	if opts.ServiceConfig != nil {
		for _, name := range opts.ServiceConfig.ProviderNames() {
			entry, ok := opts.ServiceConfig.Provider(name)
			if !ok {
				continue
			}
			if entry.DailyTokenBudget > 0 {
				svc.providerBurnRate.SetBudget(name, entry.DailyTokenBudget)
			}
		}
	}
	svc.reapStaleHarnessSessions()
	svc.ensurePrimaryQuotaRefresh(context.Background(), quotaRefreshStartup)
	svc.startPrimaryQuotaRefreshWorker()
	svc.startQuotaRecoveryProbeLoop()
	return svc, nil
}

// harnessType returns "native" for HTTP/embedded harnesses, "subprocess" for CLI-invoked ones.
func harnessType(cfg harnesses.HarnessConfig) string {
	if harnessRunsInProcessOrHTTP(cfg) {
		return "native"
	}
	return "subprocess"
}

// supportedPermissions extracts the permission levels from PermissionArgs keys,
// returning them in canonical order.
func supportedPermissions(cfg harnesses.HarnessConfig) []string {
	if len(cfg.PermissionArgs) == 0 {
		return nil
	}
	order := []string{"safe", "supervised", "unrestricted"}
	var out []string
	for _, level := range order {
		if _, ok := cfg.PermissionArgs[level]; ok {
			out = append(out, level)
		}
	}
	return out
}

func supportedReasoning(cfg harnesses.HarnessConfig) []string {
	return append([]string(nil), cfg.ReasoningLevels...)
}

// claudeQuotaState reads the durable Claude quota cache and converts it to QuotaState.
func claudeQuotaState() *QuotaState {
	snap, ok := claudeharness.ReadClaudeQuota()
	if !ok || snap == nil {
		source, err := claudeharness.ClaudeQuotaCachePath()
		if err != nil {
			source = "claude quota cache"
		}
		return unavailableQuotaState(source, "claude quota cache unavailable")
	}
	now := time.Now()
	decision := claudeharness.DecideClaudeQuotaRouting(snap, now, 0)
	qs := &QuotaState{
		CapturedAt: snap.CapturedAt,
		Fresh:      decision.Fresh,
		Source:     snap.Source,
	}
	if len(snap.Windows) > 0 {
		qs.Windows = append(qs.Windows, snap.Windows...)
	} else if snap.FiveHourLimit > 0 {
		var used float64
		if snap.FiveHourLimit > 0 {
			used = float64(snap.FiveHourLimit-snap.FiveHourRemaining) / float64(snap.FiveHourLimit) * 100
		}
		qs.Windows = append(qs.Windows, harnesses.QuotaWindow{
			Name:          "5h",
			WindowMinutes: 300,
			UsedPercent:   used,
			State:         harnesses.QuotaStateFromUsedPercent(int(used)),
		})
	}
	if len(snap.Windows) == 0 && snap.WeeklyLimit > 0 {
		var used float64
		if snap.WeeklyLimit > 0 {
			used = float64(snap.WeeklyLimit-snap.WeeklyRemaining) / float64(snap.WeeklyLimit) * 100
		}
		qs.Windows = append(qs.Windows, harnesses.QuotaWindow{
			Name:          "7d",
			WindowMinutes: 10080,
			UsedPercent:   used,
			State:         harnesses.QuotaStateFromUsedPercent(int(used)),
		})
	}
	qs.Status = quotaStatus(qs.Fresh, qs.Windows)
	return qs
}

func claudeAccountStatus() *AccountStatus {
	snap, ok := claudeharness.ReadClaudeQuota()
	if !ok || snap == nil {
		return nil
	}
	decision := claudeharness.DecideClaudeQuotaRouting(snap, time.Now(), 0)
	return accountStatusFromInfo(snap.Account, snap.Source, snap.CapturedAt, decision.Fresh)
}

// codexQuotaState reads the durable Codex quota cache and converts it to QuotaState.
func codexQuotaState() *QuotaState {
	snap, ok := codexharness.ReadCodexQuota()
	if !ok || snap == nil {
		source, err := codexharness.CodexQuotaCachePath()
		if err != nil {
			source = "codex quota cache"
		}
		return unavailableQuotaState(source, "codex quota cache unavailable")
	}
	fresh := codexharness.IsCodexQuotaFresh(snap, time.Now(), 0)
	windows := append([]harnesses.QuotaWindow(nil), snap.Windows...)
	return &QuotaState{
		Windows:    windows,
		CapturedAt: snap.CapturedAt,
		Fresh:      fresh,
		Source:     snap.Source,
		Status:     quotaStatus(fresh, windows),
	}
}

func codexAccountStatus() *AccountStatus {
	snap, ok := codexharness.ReadCodexQuota()
	if !ok || snap == nil {
		return nil
	}
	decision := codexharness.DecideCodexQuotaRouting(snap, time.Now(), 0)
	return accountStatusFromInfo(snap.Account, snap.Source, snap.CapturedAt, decision.Fresh)
}

// geminiQuotaState reads the durable Gemini quota cache and converts it to
// QuotaState. Auth/account freshness alone is NOT promoted to quota status —
// a missing or stale quota cache returns an "unavailable" state.
func geminiQuotaState() *QuotaState {
	snap, ok := geminiharness.ReadGeminiQuota()
	if !ok || snap == nil {
		source, err := geminiharness.GeminiQuotaCachePath()
		if err != nil {
			source = "gemini quota cache"
		}
		return unavailableQuotaState(source, "gemini quota cache unavailable")
	}
	fresh := geminiharness.IsGeminiQuotaFresh(snap, time.Now(), 0)
	windows := append([]harnesses.QuotaWindow(nil), snap.Windows...)
	return &QuotaState{
		Windows:    windows,
		CapturedAt: snap.CapturedAt,
		Fresh:      fresh,
		Source:     snap.Source,
		Status:     quotaStatus(fresh, windows),
	}
}

func geminiAccountStatus() *AccountStatus {
	snap := geminiharness.ReadAuthEvidence(time.Now())
	if !snap.Authenticated {
		return &AccountStatus{
			Unauthenticated: true,
			Source:          snap.Source,
			CapturedAt:      snap.CapturedAt,
			Fresh:           snap.Fresh,
			Detail:          snap.Detail,
		}
	}
	status := accountStatusFromInfo(snap.Account, snap.Source, snap.CapturedAt, snap.Fresh)
	if status == nil {
		status = &AccountStatus{Authenticated: true, Source: snap.Source, CapturedAt: snap.CapturedAt, Fresh: snap.Fresh}
	}
	status.Detail = snap.Detail
	return status
}

func (s *service) codexUsageWindows() []UsageWindow {
	return s.harnessUsageWindows("codex")
}

func (s *service) geminiUsageWindows() []UsageWindow {
	out := make([]UsageWindow, 0, 3)
	for _, since := range []string{"today", "7d", "30d"} {
		window := s.harnessUsageWindow("gemini", since)
		if window != nil {
			out = append(out, *window)
		}
	}
	return out
}

func (s *service) harnessUsageWindows(provider string) []UsageWindow {
	window := s.harnessUsageWindow(provider, "30d")
	if window == nil {
		return nil
	}
	return []UsageWindow{*window}
}

func (s *service) harnessUsageWindow(provider, since string) *UsageWindow {
	logDir := s.serviceSessionLogDir()
	if logDir == "" {
		return nil
	}
	now := s.now()
	report, err := sessionusage.AggregateUsage(logDir, sessionusage.UsageOptions{Since: since, Now: now})
	if err != nil || report == nil {
		return nil
	}
	var total sessionusage.UsageRow
	for _, row := range report.Rows {
		if row.Provider != provider {
			continue
		}
		total.Sessions += row.Sessions
		total.SuccessSessions += row.SuccessSessions
		total.FailedSessions += row.FailedSessions
		total.InputTokens += row.InputTokens
		total.OutputTokens += row.OutputTokens
		total.TotalTokens += row.TotalTokens
		total.CacheReadTokens += row.CacheReadTokens
		total.CacheWriteTokens += row.CacheWriteTokens
		total.UnknownCostSessions += row.UnknownCostSessions
		if row.KnownCostUSD == nil || total.UnknownCostSessions > 0 {
			total.KnownCostUSD = nil
		} else {
			if total.KnownCostUSD == nil {
				total.KnownCostUSD = new(float64)
			}
			*total.KnownCostUSD += *row.KnownCostUSD
		}
	}
	if total.Sessions == 0 {
		return nil
	}
	window := UsageWindow{
		Name:                since,
		Source:              logDir,
		CapturedAt:          now,
		Fresh:               true,
		InputTokens:         total.InputTokens,
		OutputTokens:        total.OutputTokens,
		TotalTokens:         total.TotalTokens,
		CacheReadTokens:     total.CacheReadTokens,
		CacheWriteTokens:    total.CacheWriteTokens,
		KnownCostUSD:        total.KnownCostUSD,
		UnknownCostSessions: total.UnknownCostSessions,
	}
	if total.KnownCostUSD != nil {
		window.CostUSD = *total.KnownCostUSD
	}
	return &window
}

func (s *service) serviceSessionLogDir() string {
	if s == nil {
		return ""
	}
	configDir := ""
	if s.opts.ServiceConfig == nil {
		return serviceimpl.SessionLogDir(s.opts.SessionLogDir, configDir)
	}
	configDir = s.opts.ServiceConfig.SessionLogDir()
	return serviceimpl.SessionLogDir(s.opts.SessionLogDir, configDir)
}

func unavailableQuotaState(source, detail string) *QuotaState {
	return &QuotaState{
		Source: source,
		Status: "unavailable",
		LastError: &StatusError{
			Type:      "unavailable",
			Detail:    detail,
			Source:    source,
			Timestamp: time.Now().UTC(),
		},
	}
}

// ListHarnesses returns metadata for every registered harness.
func (s *service) ListHarnesses(ctx context.Context) ([]HarnessInfo, error) {
	s.ensurePrimaryQuotaRefresh(ctx, quotaRefreshAsync)
	statuses := s.registry.Discover()

	// Index statuses by name for O(1) lookup.
	statusByName := make(map[string]harnesses.HarnessStatus, len(statuses))
	for _, st := range statuses {
		statusByName[st.Name] = st
	}

	// Emit in registry preference order.
	names := s.registry.Names()
	out := make([]HarnessInfo, 0, len(names))

	for _, name := range names {
		cfg, ok := s.registry.Get(name)
		if !ok {
			continue
		}
		st := statusByName[name]

		info := HarnessInfo{
			Name:                 name,
			Type:                 harnessType(cfg),
			Available:            st.Available,
			Path:                 st.Path,
			Error:                st.Error,
			Billing:              harnessPaymentKind(name, cfg),
			AutoRoutingEligible:  cfg.AutoRoutingEligible,
			TestOnly:             cfg.TestOnly,
			ExactPinSupport:      cfg.ExactPinSupport,
			DefaultModel:         cfg.DefaultModel,
			SupportedPermissions: supportedPermissions(cfg),
			SupportedReasoning:   supportedReasoning(cfg),
			CostClass:            cfg.CostClass,
			CapabilityMatrix:     harnessCapabilityMatrix(name, cfg),
		}
		if !st.Available {
			info.LastError = statusError(st.Error, "harness discovery", time.Now())
		}

		// Populate live Quota for harnesses that have durable quota caches.
		switch name {
		case "claude":
			info.Quota = claudeQuotaState()
			info.Account = claudeAccountStatus()
		case "codex":
			info.Quota = codexQuotaState()
			info.Account = codexAccountStatus()
			info.UsageWindows = s.codexUsageWindows()
		case "gemini":
			info.Quota = geminiQuotaState()
			info.Account = geminiAccountStatus()
			info.UsageWindows = s.geminiUsageWindows()
		}

		out = append(out, info)
	}

	return out, nil
}

// ListProviders and HealthCheck are implemented in service_providers.go.

// ListModels is implemented in service_models.go.

// ResolveRoute is implemented in service_routing.go.

// RouteStatus is implemented in service_routestatus.go.

// Execute is implemented in service_execute.go.

// TailSessionLog is implemented in service_taillog.go.
