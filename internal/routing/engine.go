package routing

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
)

// Request is the routing input. All fields are optional except at least
// one of {Policy, Model, Harness, Provider} should be set (otherwise the
// engine has nothing to disambiguate on).
//
// Provider is present from day one (fixes ddx-8610020e — no soft-preference
// dropping).
type Request struct {
	Policy             string // "cheap" | "default" | "smart" | "air-gapped"
	Model              string // exact concrete model pin
	Provider           string // exact provider pin; constrains routing to one provider identity
	Harness            string // hard preference; constrains routing to one harness
	Reasoning          string // public reasoning scalar
	Permissions        string // "safe" | "supervised" | "unrestricted"
	ProviderPreference string // "local-first" | "subscription-first" | "local-only" | "subscription-only"
	CorrelationID      string // validated sticky route key, when available
	AllowLocal         bool
	Require            []string

	// EstimatedPromptTokens, when > 0, drives context-window gating.
	EstimatedPromptTokens int

	// RequiresTools, when true, requires the candidate to support tool calling.
	RequiresTools bool
	MinPower      int
	MaxPower      int

	// ExcludedRoutes lists caller-supplied (Provider, Model, Endpoint)
	// combinations to skip during candidate selection. The engine marks
	// matching candidates ineligible with FilterReasonCallerExcluded.
	ExcludedRoutes []ExcludedRoute
}

// ExcludedRoute is a caller-supplied exclusion hint for the routing engine.
// Provider is required; Model and Endpoint are optional (empty matches any).
type ExcludedRoute struct {
	Provider string
	Model    string // optional; empty matches any model on the provider
	Endpoint string // optional; empty matches any endpoint
}

const (
	ProviderPreferenceLocalFirst        = "local-first"
	ProviderPreferenceSubscriptionFirst = "subscription-first"
	ProviderPreferenceLocalOnly         = "local-only"
	ProviderPreferenceSubscriptionOnly  = "subscription-only"

	ContextSourceProviderAPI    = "provider_api"
	ContextSourceProviderConfig = "provider_config"
	ContextSourceCatalog        = "catalog"
	ContextSourceDefault        = "default"
	ContextSourceUnknown        = "unknown"
)

// SurfacePreferenceBias is the score bonus a candidate earns when its harness
// is the configured preferred harness for its surface on an unpinned route.
// It is intentionally small: it only needs to break what would otherwise be an
// exact score tie between two harnesses sharing a surface (e.g. claude-tui vs
// claude on the "claude" surface), without letting the preferred harness
// outrank a genuinely better-scoring candidate on a different surface.
const SurfacePreferenceBias = 0.5

// DefaultSurfacePreference returns the built-in surface→harness preference:
// the shared "claude" surface defaults to the claude-tui (interactive TUI,
// bypassPermissions) harness over claude (--print). Callers gate this behind a
// config flag by passing an explicit (possibly empty, non-nil) map in Inputs.
func DefaultSurfacePreference() map[string]string {
	return map[string]string{"claude": "claude-tui"}
}

// resolveSurfacePreference returns the effective surface→harness preference for
// a routing call. A nil map (the zero value) means "use the default"; an
// explicit empty map disables the preference.
func resolveSurfacePreference(in Inputs) map[string]string {
	if in.SurfacePreference == nil {
		return DefaultSurfacePreference()
	}
	return in.SurfacePreference
}

// surfacePreferred reports whether harness h is the configured preferred
// harness for its surface on THIS route. The preference only applies to
// unpinned automatic routing: an explicit --harness pin selects exactly one
// harness (so the preference is moot) and must remain authoritative, so a
// claude pin keeps resolving to claude even though claude-tui is the default.
func surfacePreferred(h HarnessEntry, req Request, in Inputs) bool {
	if req.Harness != "" {
		return false
	}
	if h.Surface == "" {
		return false
	}
	pref := resolveSurfacePreference(in)
	want, ok := pref[h.Surface]
	return ok && want == h.Name
}

// MinContextWindow returns the minimum context window the request requires,
// derived from EstimatedPromptTokens with a safety margin.
func (r Request) MinContextWindow() int {
	if r.EstimatedPromptTokens <= 0 {
		return 0
	}
	// 1.25x safety margin for response tokens + tool overhead.
	return r.EstimatedPromptTokens + r.EstimatedPromptTokens/4
}

const (
	QuotaTrendUnknown    = "unknown"
	QuotaTrendHealthy    = "healthy"
	QuotaTrendBurning    = "burning"
	QuotaTrendExhausting = "exhausting"
)

// HarnessEntry is the harness-side input the caller (service) supplies.
// It is the routing engine's view of a registered harness; the engine does
// not import the harnesses package directly to keep the dependency narrow.
type HarnessEntry struct {
	Name                string
	Surface             string
	CostClass           string
	IsLocal             bool
	IsSubscription      bool
	IsHTTPProvider      bool
	AutoRoutingEligible bool
	TestOnly            bool
	ExactPinSupport     bool
	DefaultModel        string
	SupportedModels     []string
	// AutoRoutingModels enumerates the concrete model IDs the engine may rank
	// when a caller pins Harness but leaves Model empty. This lets harness-local
	// policy routing choose within the harness instead of being forced onto a
	// single DefaultModel. Nil falls back to DefaultModel resolution.
	AutoRoutingModels  []string
	SupportedReasoning []string
	MaxReasoningTokens int
	SupportedPerms     []string
	SupportsTools      bool

	// Available reflects the harness's discovered availability.
	Available bool

	// QuotaOK / QuotaPercentUsed reflect live quota state (when applicable).
	// SubscriptionOK gates subscription harnesses at the eligibility level:
	// when false, the candidate is ineligible regardless of score.
	QuotaOK          bool
	QuotaPercentUsed int
	QuotaStale       bool
	QuotaTrend       string // unknown|healthy|burning|exhausting
	QuotaReason      string
	SubscriptionOK   bool // false = hard gate; true = score-based demotion

	// InCooldown marks the entire harness as being in a failure cooldown.
	// When true the harness is demoted in score (via candidateInternal.InCooldown)
	// but not hard-rejected, so it can still win when all other harnesses are
	// also unavailable.
	InCooldown bool

	// Providers is the list of providers this harness can dispatch to.
	// For subprocess harnesses (claude/codex) this is typically a single
	// vendor entry. For the native "fiz" harness it is the configured
	// list of HTTP providers.
	Providers []ProviderEntry
}

// ProviderEntry describes one provider available under a harness.
type ProviderEntry struct {
	Name            string
	BaseURL         string
	ServerInstance  string
	EndpointName    string
	EndpointBaseURL string
	DefaultModel    string
	Billing         modelcatalog.BillingModel
	CostClass       string
	DiscoveredIDs   []string // models discovered via /v1/models or equivalent
	// CatalogIDByModel maps a served/wire model ID to the catalog-resolution
	// identity recovered from the provider's /props endpoint (e.g.
	// "dflash" -> "Qwen3.6-27B"). Power/auto-routability gates resolve against the
	// mapped identity so a generic served alias inherits its real model's power,
	// while the wire model the server is invoked with stays the served ID. Absent
	// entries fall back to the model ID itself.
	CatalogIDByModel     map[string]string
	DiscoveryAttempted   bool
	ContextWindows       map[string]int
	ContextWindowSources map[string]string
	ContextWindow        int
	ContextWindowSource  string
	SupportsTools        bool
	SupportsToolsByModel map[string]bool

	// CostUSDPer1kTokens is the estimated blended USD cost per 1,000 tokens.
	// A zero value with CostSourceUnknown means the provider cost is unknown.
	CostUSDPer1kTokens float64
	// CostUSDPer1kTokensByModel maps concrete model ID → blended cost. Used by
	// multi-tier harnesses so each per-tier candidate emitted under the same
	// provider can carry its own catalog cost. When a model ID has an entry
	// here, it overrides CostUSDPer1kTokens for that candidate.
	CostUSDPer1kTokensByModel map[string]float64
	// CostSource describes where CostUSDPer1kTokens came from: catalog,
	// subscription, user-config, or unknown.
	CostSource string
	// ActualCashSpend marks providers that would consume metered spend when
	// selected.
	ActualCashSpend bool

	// InCooldown reflects whether this provider is in a failure-cooldown window.
	InCooldown bool

	// ExcludeFromDefaultRouting, when true, prevents this provider from being
	// selected during unpinned automatic routing. An explicit provider, harness,
	// or model pin bypasses this gate so the operator can still reach the
	// provider intentionally. Corresponds to IncludeByDefault=false in config.
	ExcludeFromDefaultRouting bool
}

const (
	// CostSourceCatalog means cost came from the model catalog.
	CostSourceCatalog = "catalog"
	// CostSourceSubscription means cost came from subscription quota pricing.
	CostSourceSubscription = "subscription"
	// CostSourceUnknown means no reliable cost estimate is available.
	CostSourceUnknown = "unknown"
	// CostSourceUserConfig means cost came from explicit user configuration.
	CostSourceUserConfig = "user-config"
)

// Decision is the routing engine's output: the picked candidate plus the
// full ranked list (including rejected ones with rejection reasons).
type Decision struct {
	Harness        string
	Provider       string
	Endpoint       string
	ServerInstance string
	Model          string
	Reason         string
	Candidates     []Candidate
}

// Candidate is one ranked routing option.
type Candidate struct {
	Harness            string
	Provider           string
	Billing            modelcatalog.BillingModel
	Endpoint           string
	ServerInstance     string
	Model              string
	Score              float64
	CostUSDPer1kTokens float64
	CostSource         string
	ActualCashSpend    bool
	Power              int
	ContextLength      int
	ContextSource      string
	ScoreComponents    map[string]float64
	Eligible           bool
	Reason             string

	// FilterReason is the typed disqualification category, set at the
	// rejection site that decided why this candidate is ineligible.
	// Empty for eligible candidates. Service-layer code maps this to the
	// public FilterReason* string constants without parsing free-form
	// Reason text.
	FilterReason FilterReason

	// LatencyMS, SuccessRate, and CostClass expose the score-component
	// inputs so callers can render per-axis explanations alongside the
	// final Score. Zero / negative values mean unknown (see Inputs docs).
	LatencyMS       float64
	SuccessRate     float64
	CostClass       string
	SpeedTPS        float64
	Utilization     float64
	ContextHeadroom int

	QuotaOK          bool
	QuotaPercentUsed int
	QuotaTrend       string
	StickyAffinity   float64
}

// FilterReason categorizes why a routing candidate was disqualified.
// The zero value (empty string) means the candidate is eligible.
type FilterReason string

const (
	filterReasonAuthMissingLabel = "credential_missing"
	filterReasonAuthInvalidLabel = "credential_invalid"
)

const (
	// FilterReasonEligible is the zero value for an eligible candidate.
	FilterReasonEligible FilterReason = ""
	// FilterReasonContextTooSmall: candidate's context window is below the
	// request's MinContextWindow().
	FilterReasonContextTooSmall FilterReason = "context_too_small"
	// FilterReasonNoToolSupport: request needs tool calling but candidate
	// does not support it.
	FilterReasonNoToolSupport FilterReason = "no_tool_support"
	// FilterReasonReasoningUnsupported: candidate cannot satisfy the
	// requested reasoning policy.
	FilterReasonReasoningUnsupported FilterReason = "reasoning_unsupported"
	// FilterReasonUnhealthy: harness/provider is unavailable, in cooldown,
	// out of quota, or excluded by a hard provider-preference gate.
	FilterReasonUnhealthy FilterReason = "unhealthy"
	// FilterReasonScoredBelowTop: catch-all for ineligibility that does
	// not fit a more specific category (also used for capability
	// mismatches such as permissions/model-pin/exact-pin and for model
	// resolution failures).
	FilterReasonScoredBelowTop FilterReason = "scored_below_top"
	// FilterReasonPinMismatch: candidate was rejected because it does not
	// satisfy an explicit caller pin such as Provider.
	FilterReasonPinMismatch FilterReason = "pin_mismatch"
	// FilterReasonPowerMissing: automatic routing requires catalog power
	// metadata, but the candidate model is unknown or has zero power.
	FilterReasonPowerMissing FilterReason = "power_missing"
	// FilterReasonBelowMinPower: candidate model power is below req.MinPower.
	FilterReasonBelowMinPower FilterReason = "below_min_power"
	// FilterReasonAboveMaxPower: candidate model power is above req.MaxPower.
	FilterReasonAboveMaxPower FilterReason = "above_max_power"
	// FilterReasonExactPinOnly: catalog marks the model as only eligible for
	// explicit model pins.
	FilterReasonExactPinOnly FilterReason = "exact_pin_only"
	// FilterReasonNotAutoRoutable: catalog metadata exists but marks the
	// model inactive, deprecated, stale, or otherwise excluded from
	// automatic routing.
	FilterReasonNotAutoRoutable FilterReason = "not_auto_routable"
	// FilterReasonQuotaExhausted: provider is in the quota_exhausted state
	// with retry_after in the future. The candidate would have been eligible
	// otherwise.
	FilterReasonQuotaExhausted FilterReason = "quota_exhausted"
	// FilterReasonPolicyFiltered: candidate was rejected by a hard policy
	// requirement such as allow_local=false or require=[no_remote].
	FilterReasonPolicyFiltered FilterReason = "policy_filtered"
	// FilterReasonProviderExcludedFromDefault: provider has IncludeByDefault=false
	// in operator config and the request did not explicitly pin a provider or harness.
	FilterReasonProviderExcludedFromDefault FilterReason = "provider_excluded_from_default_routing"
	// FilterReasonMeteredOptInRequired: pay-per-token candidate was removed
	// from unpinned automatic routing because metered spend was not enabled.
	FilterReasonMeteredOptInRequired FilterReason = "metered_opt_in_required"
	// FilterReasonCallerExcluded: candidate was excluded by an ExcludedRoutes
	// entry in the caller's RouteRequest. Distinguishes caller-driven re-routes
	// from internal health signals in routing-quality observability.
	FilterReasonCallerExcluded FilterReason = "caller_excluded"
	// FilterReasonEndpointUnreachable: provider's endpoint failed a proactive
	// aliveness probe within the health signal TTL and alternates are available.
	// Distinct from FilterReasonUnhealthy (route-attempt failure cooldowns).
	// An explicit provider pin bypasses this gate.
	FilterReasonEndpointUnreachable FilterReason = "endpoint_unreachable"
	// FilterReasonCredentialMissing: provider needs an API key to authenticate
	// but none is configured (or the configured key is obviously malformed).
	// Synchronous and network-free: the gate runs every routing pass without
	// issuing any HTTP request. Distinct from FilterReasonUnhealthy and from
	// any future credential_invalid reason reserved for server-side rejection.
	FilterReasonCredentialMissing FilterReason = filterReasonAuthMissingLabel
	// FilterReasonCreditExhausted: provider has a cached account-level balance
	// reading below the configured threshold. The probe lives in the service
	// layer's freshness cache so the engine never issues network I/O. Distinct
	// from FilterReasonQuotaExhausted (per-window quota signal from request
	// headers) — credit_exhausted is an account-spend ceiling that does not
	// recover on a fixed retry_after.
	FilterReasonCreditExhausted FilterReason = "credit_exhausted"
	// FilterReasonCredentialInvalid: provider rejected the configured API key
	// (e.g. a 401 from openrouter's /api/v1/credits endpoint). Distinct from
	// FilterReasonCredentialMissing — a present-but-rejected key requires
	// rotation, not configuration. Evidence body carries the originating HTTP
	// status code so operators can triage from routing_decision without grepping
	// logs.
	FilterReasonCredentialInvalid FilterReason = filterReasonAuthInvalidLabel
	// FilterReasonProviderUnreachable: a best-effort probe against the
	// provider's account endpoint failed transiently (DNS, TCP, TLS, or non-401
	// 5xx). The candidate is gated only for the current freshness window; the
	// next successful probe restores eligibility automatically, so a single bad
	// minute does not hard-exclude the provider. Distinct from
	// FilterReasonEndpointUnreachable, which gates on the routing aliveness
	// probe rather than account-state probes.
	FilterReasonProviderUnreachable FilterReason = "provider_unreachable"
)

// NoViableCandidateError reports that routing evaluated candidates but every
// one failed a gate.
type NoViableCandidateError struct {
	Rejected int
	Model    string
	Provider string
	Harness  string
	MinPower int
	MaxPower int
}

func (e *NoViableCandidateError) Error() string {
	var pins []string
	if e.Model != "" {
		pins = append(pins, "model="+e.Model)
	}
	if e.Provider != "" {
		pins = append(pins, "provider="+e.Provider)
	}
	if e.Harness != "" {
		pins = append(pins, "harness="+e.Harness)
	}
	if e.MinPower > 0 {
		pins = append(pins, fmt.Sprintf("min_power=%d", e.MinPower))
	}
	if e.MaxPower > 0 {
		pins = append(pins, fmt.Sprintf("max_power=%d", e.MaxPower))
	}
	if len(pins) > 0 {
		return fmt.Sprintf("no viable routing candidate for pins %s: %d candidates rejected", strings.Join(pins, " "), e.Rejected)
	}
	return fmt.Sprintf("no viable routing candidate: %d candidates rejected", e.Rejected)
}

// CandidateRejectionReason is the stable reason vocabulary used when a policy
// matches candidates but every candidate is rejected before dispatch.
type CandidateRejectionReason string

const (
	CandidateRejectionQuotaExhausted   CandidateRejectionReason = "quota_exhausted"
	CandidateRejectionUnhealthy        CandidateRejectionReason = "unhealthy"
	CandidateRejectionModelUnavailable CandidateRejectionReason = "model_unavailable"
	CandidateRejectionHarnessUnhealthy CandidateRejectionReason = "harness_unhealthy"
	CandidateRejectionPolicyFiltered   CandidateRejectionReason = "policy_filtered"
)

// CandidateRejection records why a matching candidate could not be used.
type CandidateRejection struct {
	Provider string
	Harness  string
	Model    string
	Reason   string
}

// ErrNoLiveProvider reports that policy escalation walked the entire ladder
// (cheap → default → smart) without finding a live provider that
// can serve the request. Callers translate this into a precise user-facing
// message naming the prompt size and tool requirement so operators know
// what capability is missing across all policies.
type ErrNoLiveProvider struct {
	// PromptTokens is the request's EstimatedPromptTokens at the time
	// escalation began. Zero means no prompt-token gating was active.
	PromptTokens int
	// RequiresTools mirrors the request's RequiresTools flag.
	RequiresTools bool
	// StartingPolicy is the policy name that escalation began from.
	StartingPolicy     string
	MinPower           int
	MaxPower           int
	AllowLocal         bool
	RejectedCandidates []CandidateRejection
}

func (e *ErrNoLiveProvider) Error() string {
	if len(e.RejectedCandidates) == 0 {
		return fmt.Sprintf("no candidates match policy %q (power %d-%d, allow_local=%v); check policy fit and provider configuration",
			e.StartingPolicy, e.MinPower, e.MaxPower, e.AllowLocal)
	}
	counts := map[string]int{}
	for _, rejected := range e.RejectedCandidates {
		counts[rejected.Reason]++
	}
	return fmt.Sprintf("policy %q has %d candidate(s) matching power %d-%d but all rejected: %d quota-exhausted, %d unhealthy, %d unavailable",
		e.StartingPolicy,
		len(e.RejectedCandidates),
		e.MinPower,
		e.MaxPower,
		counts[string(CandidateRejectionQuotaExhausted)],
		counts[string(CandidateRejectionUnhealthy)]+counts[string(CandidateRejectionHarnessUnhealthy)],
		counts[string(CandidateRejectionModelUnavailable)])
}

// PolicyEscalationLadder is the fixed cheap → default → smart progression
// service.ResolveRoute walks when every candidate at the requested tier is
// filtered out (unhealthy or capability-rejected).
var PolicyEscalationLadder = []string{"cheap", "default", "smart"}

// Inputs bundles the engine's external data sources.
type Inputs struct {
	Harnesses                    []HarnessEntry
	HistoricalSuccess            map[string]float64 // by harness name; -1 = insufficient data
	ObservedSpeedTPS             map[string]float64 // by "provider:model"
	ProviderSuccessRate          map[string]float64 // by ProviderModelKey(provider, endpoint, model)
	ObservedLatencyMS            map[string]float64 // by ProviderModelKey(provider, endpoint, model)
	EndpointLoads                map[string]EndpointLoad
	EndpointLoadResolver         func(provider, endpoint, model string) (EndpointLoad, bool)
	StickyServerInstanceResolver func(stickyKey string) (string, bool)
	ExactRouteCooldowns          map[RouteCooldownKey]time.Time // exact route failures; soft demotion only
	ProviderCooldowns            map[string]time.Time           // by provider name; soft demotion only
	CooldownDuration             time.Duration                  // 0 = no cooldown enforcement

	// ProviderUnreachable maps provider name → time of last dial-class
	// discovery failure. Hard gate per FEAT-004 AC-28: known-down endpoints
	// are dispatchability failures, not scoring inputs. Populated from
	// snapshot.Sources entries with errors matching dial-tcp / connection
	// refused / i/o timeout patterns. Honors CooldownDuration as TTL; an
	// explicit provider pin bypasses the gate.
	ProviderUnreachable map[string]time.Time

	// ProviderQuotaExhaustedUntil maps provider name → retry_after time.
	// A provider with retry_after > Now is treated as quota_exhausted and
	// disqualified from candidate selection. The service maintains the
	// per-provider quota state machine and projects it into this map for
	// each routing call.
	ProviderQuotaExhaustedUntil map[string]time.Time

	// ProbeUnreachable maps provider name → time of last failed aliveness probe.
	// Populated from proactive startup and periodic probes (distinct from
	// ProviderUnreachable which is populated from dial failures during route
	// attempts and discovery). An explicit provider pin bypasses this gate.
	ProbeUnreachable map[string]time.Time

	// ProbeUnknown maps local/fixed provider names whose aliveness evidence is
	// missing or stale. It is a scoring input, not a hard gate: healthy cached
	// alternatives should win, but local-only deployments can still route.
	ProbeUnknown map[string]time.Time

	// ProviderCredentialMissing maps provider name → human-readable evidence of
	// the credential gap (which env var / config field was inspected). Hard
	// gate: a candidate listed here is disqualified with
	// FilterReasonCredentialMissing before any network I/O is attempted. The
	// service layer populates the map synchronously from config; the gate is
	// O(1) and side-effect free.
	ProviderCredentialMissing map[string]string

	// ProviderCreditExhausted maps provider name → observed account-balance
	// evidence. Hard gate: a candidate listed here is disqualified with
	// FilterReasonCreditExhausted. The service layer maintains the credit
	// probe and its TTL cache; the engine reads only the projected map, so
	// no network I/O ever happens here.
	ProviderCreditExhausted map[string]ProviderCreditExhaustedEvidence

	// ProviderCredentialInvalid maps provider name → evidence that the
	// configured API key was rejected by the provider (e.g. a 401 from a
	// credit/account-state probe). Hard gate: a listed candidate is
	// disqualified with FilterReasonCredentialInvalid. Distinct from
	// ProviderCredentialMissing — the key is present, just not accepted.
	ProviderCredentialInvalid map[string]ProviderCredentialInvalidEvidence

	// ProviderProbeUnreachable maps provider name → evidence of a transient
	// probe failure against the provider's account endpoint (transport error
	// or non-401 5xx). Soft gate per the current freshness window: a listed
	// candidate is disqualified with FilterReasonProviderUnreachable, but the
	// next successful probe pass clears the entry. Distinct from
	// ProbeUnreachable, which tracks routing-aliveness probes.
	ProviderProbeUnreachable map[string]ProviderProbeUnreachableEvidence

	// ProviderEligibilityOverrides is a generic, provider-agnostic plumbing
	// path for marking any provider eligible=false with a typed FilterReason
	// and freshness timestamp. The engine consults this map inside
	// buildHarnessCandidates after the ProbeUnreachable gate; the explicit
	// provider pin bypasses it, mirroring the endpoint_unreachable gate.
	// Service-layer health checks (credential, credit, account probes) feed
	// the map so the engine never has to know about a specific provider's
	// authentication or billing scheme.
	ProviderEligibilityOverrides map[string]ProviderEligibilityOverride

	// SafeMode, when true, forces the simple Phase-1 routing policy:
	// default tier (middle), no local mixin, no Phase 2+ quota economics.
	// Set via config key "routing.safe_mode" or env ROUTING_SAFE_MODE=1.
	// Intended as a kill-switch: flip it to recover known-good routing
	// without rebuilding or redeploying the service.
	SafeMode bool

	// SurfacePreference maps a catalog surface identifier (e.g. "claude") to the
	// canonical harness name that should win an UNPINNED route on that surface
	// when multiple harnesses share the surface and tie on score. This is the
	// config-gated mechanism that makes claude-tui the default for the shared
	// "claude" surface (claude-tui vs claude --print) without hard-pinning: a
	// preferred harness gets a small score bias plus the final tie-break, so an
	// explicit --harness claude pin or claude-tui being ineligible still falls
	// through to claude. Nil falls back to DefaultSurfacePreference(); an
	// explicit empty (non-nil) map disables the preference entirely.
	SurfacePreference map[string]string

	Now              time.Time // injected for deterministic testing; default time.Now()
	ModelEligibility func(model string) (ModelEligibility, bool)

	// ReasoningResolver returns the catalog's reasoning default for a
	// (policy, surface) pair. When set, buildHarnessCandidates uses it
	// to resolve Reasoning=auto to a concrete level before invoking the
	// capability gate, so candidates that cannot satisfy the resolved level
	// (e.g. an off-only variant under a policy whose default is
	// "high") are correctly disqualified instead of slipping through.
	ReasoningResolver func(policy, surface string) (resolved string, ok bool)
}

// RouteCooldownKey identifies one routed candidate. A non-empty field narrows
// matching to that field, so callers can preserve all route identity they have
// without broadening a failure to sibling harnesses, endpoints, instances, or
// models.
type RouteCooldownKey struct {
	Harness        string
	Provider       string
	Endpoint       string
	ServerInstance string
	Model          string
}

// ProviderCreditExhaustedEvidence is the per-provider account-balance reading
// that fed the credit_exhausted gate. The service layer populates one entry
// per provider whose cached balance fell below the configured threshold.
type ProviderCreditExhaustedEvidence struct {
	// BalanceUSD is the observed account balance, in USD, as of ObservedAt.
	BalanceUSD float64
	// ThresholdUSD is the configured floor; balance below this triggers the gate.
	ThresholdUSD float64
	// ObservedAt is when the balance reading was taken by the credit probe.
	ObservedAt time.Time
}

// ProviderCredentialInvalidEvidence is the per-provider account-probe
// rejection that fed the credential_invalid gate. The originating HTTP
// status (typically 401) is carried so routing_decision evidence rows
// surface the root cause directly.
type ProviderCredentialInvalidEvidence struct {
	// HTTPStatus is the status code returned by the provider's account
	// endpoint. Typically 401 (Unauthorized) or 403 (Forbidden).
	HTTPStatus int
	// ObservedAt is when the rejection was observed by the probe.
	ObservedAt time.Time
}

// ProviderProbeUnreachableEvidence is the per-provider account-probe
// transient failure that fed the provider_unreachable gate. The evidence
// distinguishes HTTP failures (StatusCode != 0) from transport errors
// (ErrorClass set, StatusCode == 0) so the operator can triage without
// log spelunking.
type ProviderProbeUnreachableEvidence struct {
	// StatusCode is the HTTP status code observed on a non-401 server-side
	// failure (e.g. 502, 503, 504). Zero when the failure happened before any
	// HTTP response was received.
	StatusCode int
	// ErrorClass is the transport-failure category for non-HTTP failures
	// (e.g. "transport_error"). Empty when StatusCode != 0.
	ErrorClass string
	// Message is a short human-readable form of the original error or status
	// line, suitable for inclusion in evidence text.
	Message string
	// ObservedAt is when the failed probe attempt returned.
	ObservedAt time.Time
}

// ProviderEligibilityOverride records a service-layer eligibility verdict
// for one provider identity. The engine consults the override after the
// ProbeUnreachable gate and applies FilterReason verbatim, so callers can
// extend the health story (credentials, billing, account state) without
// adding provider-specific code paths to the engine.
type ProviderEligibilityOverride struct {
	// FilterReason is the typed disqualification category recorded on the
	// candidate when this override fires. Must be a non-empty FilterReason*
	// value; an empty reason is treated as "no override".
	FilterReason FilterReason
	// ProbeAt is the freshness timestamp the service layer recorded when it
	// produced this override (e.g. when the credit probe last ran). It is
	// surfaced in the candidate Reason so operators can see how stale the
	// signal is.
	ProbeAt time.Time
}

// EndpointLoad is the routing engine's normalized view of endpoint load for a
// single provider/model tuple.
type EndpointLoad struct {
	LeaseCount           int
	NormalizedLoad       float64
	UtilizationFresh     bool
	UtilizationSaturated bool
}

// ModelEligibility is the routing engine's catalog-power view for one model.
// Unknown or zero-power models are still usable through exact Model pins, but
// are excluded from unpinned automatic routing.
type ModelEligibility struct {
	Power        int
	ExactPinOnly bool
	AutoRoutable bool
}

// candidateInternal carries the engine's intermediate state per (harness, provider, model).
type candidateInternal struct {
	Harness               string
	Provider              string
	Billing               modelcatalog.BillingModel
	EndpointName          string
	ServerInstance        string
	Model                 string
	CostClass             string
	CostUSDPer1kTokens    float64
	CostSource            string
	ActualCashSpend       bool
	Power                 int
	ContextLength         int
	ContextSource         string
	ContextHeadroom       int
	IsSubscription        bool
	QuotaOK               bool
	QuotaPercentUsed      int
	QuotaStale            bool
	QuotaTrend            string
	SubscriptionOK        bool
	HistoricalSuccessRate float64
	ProviderSuccessRate   float64
	ObservedTokensPerSec  float64
	ObservedLatencyMS     float64
	InCooldown            bool
	LocalHealthUnknown    bool
	ProviderAffinityMatch bool
	ProviderPreference    string
	EndpointLoad          float64
	EndpointLoadFresh     bool
	EndpointSaturated     bool
	StickyMatch           bool
	MinPower              int
	MaxPower              int
	// Surface is the catalog surface this candidate's harness belongs to
	// (e.g. "claude"). Used by the surface-preference tie-break/bias.
	Surface string
	// SurfacePreferred is true when this candidate's harness is the configured
	// preferred harness for its Surface on an unpinned route (see
	// Inputs.SurfacePreference). It contributes a small score bias and wins the
	// final same-surface tie-break.
	SurfacePreferred bool
}

// ProviderModelKey is the metrics key used by routing callers for provider
// performance signals. Endpoint is optional; when empty the key remains
// compatible with older provider:model metrics.
func ProviderModelKey(provider, endpoint, model string) string {
	if endpoint == "" {
		return provider + ":" + model
	}
	return provider + "@" + endpoint + ":" + model
}

// Resolve runs the engine end-to-end and returns a Decision.
//
// The engine:
//  1. Enumerates (harness, provider, model) candidates from inputs.
//  2. Applies gating (capability, override, model-pin, surface).
//  3. Scores per policy with cooldown demotion + perf bias.
//  4. Sorts viable → score → cost → locality → name.
//  5. Returns the top viable candidate with the full ranked list.
//
// Returns an error only when no viable candidate exists.
func Resolve(req Request, in Inputs) (*Decision, error) {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}

	// Safe mode: force default (middle-tier) policy and disable local mixin.
	// Phase 2+ quota economics and local speculative routing are bypassed
	// regardless of what the caller requested.
	if in.SafeMode {
		req.Policy = "default"
	}

	policyName, err := canonicalPolicy(req.Policy)
	if err != nil {
		return &Decision{}, err
	}
	req.Policy = policyName
	if err := validateRequirements(req); err != nil {
		return &Decision{}, err
	}

	canonicalHarness := req.Harness
	if canonicalHarness == "local" {
		canonicalHarness = "fiz"
	}
	if err := explicitPinError(req, in); err != nil {
		return &Decision{}, err
	}

	var ranked []rankedCandidate
	for _, h := range in.Harnesses {
		// TestOnly harnesses (script/virtual) only reachable via explicit override.
		if h.TestOnly && canonicalHarness != h.Name {
			continue
		}
		// Automatic profile/tier routing is restricted to harnesses with full
		// coverage. Explicit Harness pins can still use experimental/ad-hoc
		// harnesses.
		if canonicalHarness == "" && !h.AutoRoutingEligible {
			continue
		}
		// Hard harness override: skip non-matching harnesses.
		if canonicalHarness != "" && canonicalHarness != h.Name {
			continue
		}
		entries := buildHarnessCandidates(h, req, in)
		ranked = append(ranked, entries...)
	}

	// Compute scores for candidates that survived the primary gates. A later
	// max-power exclusion pass may turn some of these scored candidates
	// ineligible so the trace can retain power evidence without letting an
	// overpowered route win automatic selection.
	for i := range ranked {
		if !ranked[i].out.Eligible {
			continue
		}
		ranked[i].out.Score = scorePolicy(req.Policy, ranked[i].internal)
		ranked[i].out.ScoreComponents = scoreComponents(req.Policy, ranked[i].internal)
		ranked[i].out.Reason = fmt.Sprintf("policy=%s; score=%.1f", req.Policy, ranked[i].out.Score)
	}
	applyMaxPowerExclusion(ranked, req)
	applyModelPinSubscriptionPreference(ranked, req, in)
	neutralCost, hasKnownCost := neutralKnownCost(ranked)

	// Sort: eligible first, then descending score, then cost, then locality,
	// then alphabetical.
	sort.SliceStable(ranked, func(i, j int) bool {
		ei, ej := ranked[i].out.Eligible, ranked[j].out.Eligible
		if ei != ej {
			return ei
		}
		if !ei {
			return ranked[i].out.Harness < ranked[j].out.Harness
		}
		if ranked[i].out.Score != ranked[j].out.Score {
			return ranked[i].out.Score > ranked[j].out.Score
		}
		if hasKnownCost {
			ci := candidateCostTieValue(ranked[i], neutralCost)
			cj := candidateCostTieValue(ranked[j], neutralCost)
			if ci != cj {
				return ci < cj
			}
		}
		// Locality tiebreak: prefer local cost-class.
		li := costClassRank[ranked[i].internal.CostClass] == 0
		lj := costClassRank[ranked[j].internal.CostClass] == 0
		if li != lj {
			return li
		}
		if sameLocalEndpointGroup(ranked[i].internal, ranked[j].internal) {
			if ranked[i].internal.EndpointSaturated != ranked[j].internal.EndpointSaturated {
				return !ranked[i].internal.EndpointSaturated
			}
			if ranked[i].internal.EndpointLoad != ranked[j].internal.EndpointLoad {
				return ranked[i].internal.EndpointLoad < ranked[j].internal.EndpointLoad
			}
		}
		// Surface preference tie-break: among same-surface candidates that tied
		// on every signal above, the configured preferred harness wins before
		// falling back to alphabetical order (claude-tui < claude is alphabetical
		// the wrong way, so without this the --print runner would win the tie).
		if ranked[i].internal.Surface == ranked[j].internal.Surface {
			pi, pj := ranked[i].internal.SurfacePreferred, ranked[j].internal.SurfacePreferred
			if pi != pj {
				return pi
			}
		}
		if ranked[i].out.Harness != ranked[j].out.Harness {
			return ranked[i].out.Harness < ranked[j].out.Harness
		}
		return ranked[i].out.Provider < ranked[j].out.Provider
	})

	out := make([]Candidate, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].out
	}

	for i := range out {
		if out[i].Eligible {
			return &Decision{
				Harness:        out[i].Harness,
				Provider:       out[i].Provider,
				Endpoint:       out[i].Endpoint,
				ServerInstance: out[i].ServerInstance,
				Model:          out[i].Model,
				Reason:         fmt.Sprintf("policy=%s; score=%.1f", req.Policy, out[i].Score),
				Candidates:     out,
			}, nil
		}
	}
	if quotaErr := allProvidersQuotaExhaustedError(ranked); quotaErr != nil {
		return &Decision{Candidates: out}, quotaErr
	}
	if requested := requestedModelIntent(req); requested != "" && req.Provider == "" && canonicalHarness == "" && hasLiveDiscoveryCandidates(ranked) {
		return &Decision{Candidates: out}, fmt.Errorf("no live endpoint offers a match for %s", requested)
	}
	if missingRequirement := missingPolicyRequirement(req, out); missingRequirement != "" {
		return &Decision{Candidates: out}, &ErrPolicyRequirementUnsatisfied{
			Policy:      req.Policy,
			Requirement: missingRequirement,
			Rejected:    len(out),
		}
	}

	// For unpinned requests with no viable candidates at the current policy,
	// attempt escalation to the next policy in the ladder before returning
	// the error. Hard-pinned requests (with Model/Provider/Harness) must not
	// escalate — they fail with the original no-viable error.
	if isUnpinnedRequest(req) {
		nextPolicy := nextPolicyInLadder(req.Policy)
		if nextPolicy != "" {
			probe := req
			probe.Policy = nextPolicy
			if dec, err := Resolve(probe, in); err == nil && dec != nil && dec.Harness != "" {
				return dec, nil
			}
		}
	}

	return &Decision{Candidates: out}, noViableCandidateError(req, len(out))
}

func hasAnyEligible(ranked []rankedCandidate) bool {
	for _, rc := range ranked {
		if rc.out.Eligible {
			return true
		}
	}
	return false
}

func applyMaxPowerExclusion(ranked []rankedCandidate, req Request) {
	if req.MaxPower <= 0 || req.Model != "" || req.Provider != "" {
		return
	}
	if !hasEligibleInBoundsCandidate(ranked) {
		return
	}
	for i := range ranked {
		if !ranked[i].out.Eligible {
			continue
		}
		if ranked[i].internal.Power <= req.MaxPower || ranked[i].internal.Power <= 0 {
			continue
		}
		ranked[i].out.Eligible = false
		ranked[i].out.FilterReason = FilterReasonAboveMaxPower
		ranked[i].out.Reason = fmt.Sprintf("model power %d exceeds max_power=%d while an in-bounds candidate exists", ranked[i].internal.Power, req.MaxPower)
		if ranked[i].out.ScoreComponents == nil {
			ranked[i].out.ScoreComponents = scoreComponents(req.Policy, ranked[i].internal)
		}
		ranked[i].out.ScoreComponents["power"] -= aboveMaxPowerExclusionPenalty
		ranked[i].out.Score -= aboveMaxPowerExclusionPenalty
	}
}

// applyModelPinSubscriptionPreference enforces that an explicit --model pin
// (with no --harness/--provider override) is honored by a configured
// subscription harness whose SupportedModels covers the pinned model,
// rather than by a provider-routed fallback such as fiz/openrouter.
//
// Why: when the operator says "give me sonnet on the same auth I already
// have," a catalog-known openrouter route for the same concrete model can
// otherwise outscore the subscription harness on perf/cost signals and
// silently switch the dispatch destination to a per-token provider that
// likely lacks an auth header. The preference only fires when at least one
// subscription-harness candidate is still eligible and lists the pin in
// its SupportedModels — if no subscription advertises the model, the
// engine falls back to fiz/openrouter as before.
func applyModelPinSubscriptionPreference(ranked []rankedCandidate, req Request, in Inputs) {
	if req.Model == "" || req.Harness != "" || req.Provider != "" {
		return
	}
	hasEligibleSubscriptionMatch := false
	for i := range ranked {
		if !ranked[i].out.Eligible || !ranked[i].internal.IsSubscription {
			continue
		}
		h, ok := findHarness(in.Harnesses, ranked[i].out.Harness)
		if !ok || len(h.SupportedModels) == 0 {
			continue
		}
		if harnessSupportsModel(h.SupportedModels, req.Model) {
			hasEligibleSubscriptionMatch = true
			break
		}
	}
	if !hasEligibleSubscriptionMatch {
		return
	}
	for i := range ranked {
		if !ranked[i].out.Eligible || ranked[i].internal.IsSubscription {
			continue
		}
		ranked[i].out.Eligible = false
		ranked[i].out.FilterReason = FilterReasonScoredBelowTop
		ranked[i].out.Reason = fmt.Sprintf("model pin %q satisfied by configured subscription harness; non-subscription fallback suppressed", req.Model)
	}
}

func hasEligibleInBoundsCandidate(ranked []rankedCandidate) bool {
	for _, candidate := range ranked {
		if !candidate.out.Eligible {
			continue
		}
		if candidateWithinPowerBounds(candidate.internal) {
			return true
		}
	}
	return false
}

// allProvidersQuotaExhaustedError returns ErrAllProvidersQuotaExhausted when
// at least one routing candidate was rejected solely because its provider is
// in the quota_exhausted state and no other candidate was eligible. The set
// of otherwise-eligible candidates collapses to those flagged with
// quotaExhaustedRetryAfter (the engine sets this only when the candidate
// passed every other gate). When that set is empty the existing
// no-viable / no-profile-candidate / live-discovery errors describe the
// failure more precisely.
func allProvidersQuotaExhaustedError(ranked []rankedCandidate) error {
	var exhausted []string
	seen := make(map[string]struct{})
	var earliest time.Time
	for _, c := range ranked {
		if c.quotaExhaustedRetryAfter.IsZero() {
			continue
		}
		name := c.out.Provider
		if name == "" {
			name = c.out.Harness
		}
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			exhausted = append(exhausted, name)
		}
		if earliest.IsZero() || c.quotaExhaustedRetryAfter.Before(earliest) {
			earliest = c.quotaExhaustedRetryAfter
		}
	}
	if len(exhausted) == 0 {
		return nil
	}
	return &ErrAllProvidersQuotaExhausted{
		RetryAfter:         earliest,
		ExhaustedProviders: exhausted,
	}
}

func noViableCandidateError(req Request, rejected int) *NoViableCandidateError {
	return &NoViableCandidateError{
		Rejected: rejected,
		Model:    req.Model,
		Provider: req.Provider,
		Harness:  canonicalHarnessPin(req.Harness),
		MinPower: req.MinPower,
		MaxPower: req.MaxPower,
	}
}

// isUnpinnedRequest reports whether the request lacks all hard pins (Model,
// Provider, Harness). Unpinned requests are eligible for policy-ladder
// escalation when the requested policy has no viable candidates.
func isUnpinnedRequest(req Request) bool {
	return req.Model == "" && req.Provider == "" && canonicalHarnessPin(req.Harness) == ""
}

// nextPolicyInLadder returns the next policy in PolicyEscalationLadder after
// the current policy, or "" if current is the last policy or not in the ladder.
func nextPolicyInLadder(current string) string {
	startIdx := -1
	for i, policy := range PolicyEscalationLadder {
		if policy == current {
			startIdx = i
			break
		}
	}
	if startIdx < 0 || startIdx+1 >= len(PolicyEscalationLadder) {
		return ""
	}
	return PolicyEscalationLadder[startIdx+1]
}

func canonicalPolicy(policy string) (string, error) {
	if policy == "" {
		return "default", nil
	}
	switch policy {
	case "cheap", "default", "smart", "air-gapped":
		return policy, nil
	default:
		return "", &ErrUnknownPolicy{Policy: policy}
	}
}

func validateRequirements(req Request) error {
	for _, requirement := range req.Require {
		if requirement == "no_remote" {
			continue
		}
		return &ErrPolicyRequirementUnsatisfied{
			Policy:      req.Policy,
			Requirement: requirement,
		}
	}
	return nil
}

func explicitPinError(req Request, in Inputs) error {
	canonicalHarness := canonicalHarnessPin(req.Harness)
	if requirement, attemptedPin := requirementPinConflict(req, in, canonicalHarness); requirement != "" {
		return &ErrPolicyRequirementUnsatisfied{
			Policy:       req.Policy,
			Requirement:  requirement,
			AttemptedPin: attemptedPin,
		}
	}

	if canonicalHarness != "" && req.Provider != "" && !harnessCanReachProvider(in.Harnesses, canonicalHarness, req.Provider) {
		return &ErrUnsatisfiablePin{
			Pin:    "harness=" + canonicalHarness + "+provider=" + req.Provider,
			Reason: "provider is not reachable through harness",
		}
	}
	if canonicalHarness != "" && req.Model != "" {
		// Provider-routed subprocess harnesses can route any provider-pinned
		// model (lmstudio, omlx, openrouter, etc.); the harness CLI owns
		// concrete model validation in that case.
		if providerRoutedHarnessAcceptsProviderPinnedModel(canonicalHarness) && req.Provider != "" {
			return nil
		}
		h, ok := findHarness(in.Harnesses, canonicalHarness)
		if ok && h.SupportedModels != nil && !harnessSupportsModel(h.SupportedModels, req.Model) {
			return &ErrUnsatisfiablePin{
				Pin:    "harness=" + canonicalHarness + "+model=" + req.Model,
				Reason: "model is not supported by harness",
			}
		}
		if ok && h.SupportedModels == nil && !harnessCanServeExactModel(h, req.Model) {
			return &ErrUnsatisfiablePin{
				Pin:    "harness=" + canonicalHarness + "+model=" + req.Model,
				Reason: "model is not supported by harness",
			}
		}
	}
	if req.Provider != "" && req.Model != "" && !providerCanServeModel(in.Harnesses, canonicalHarness, req.Provider, req.Model) {
		return &ErrUnsatisfiablePin{
			Pin:    "provider=" + req.Provider + "+model=" + req.Model,
			Reason: "model is not available on provider",
		}
	}
	return nil
}

func providerRoutedHarnessAcceptsProviderPinnedModel(harness string) bool {
	switch harness {
	case "pi", "opencode":
		return true
	default:
		return false
	}
}

func canonicalHarnessPin(harness string) string {
	if harness == "local" {
		return "fiz"
	}
	return harness
}

func findHarness(harnesses []HarnessEntry, name string) (HarnessEntry, bool) {
	for _, h := range harnesses {
		if h.Name == name {
			return h, true
		}
	}
	return HarnessEntry{}, false
}

func harnessCanReachProvider(harnesses []HarnessEntry, harness, provider string) bool {
	h, ok := findHarness(harnesses, harness)
	if !ok {
		return false
	}
	if len(h.Providers) == 0 {
		return h.Name == provider
	}
	for _, p := range h.Providers {
		if candidateProviderIdentity(h, p) == provider {
			return true
		}
	}
	return false
}

func harnessSupportsModel(supported []string, model string) bool {
	for _, candidate := range supported {
		if candidate == model {
			return true
		}
	}
	return false
}

func harnessCanServeExactModel(h HarnessEntry, model string) bool {
	if len(h.SupportedModels) > 0 && harnessSupportsModel(h.SupportedModels, model) {
		return true
	}
	if h.DefaultModel == model {
		return true
	}
	providers := h.Providers
	if len(providers) == 0 {
		providers = []ProviderEntry{{Name: ""}}
	}
	for _, p := range providers {
		if p.DefaultModel == model {
			return true
		}
		if len(p.DiscoveredIDs) > 0 && FuzzyMatch(model, p.DiscoveredIDs) != "" {
			return true
		}
	}
	return false
}

func providerCanServeModel(harnesses []HarnessEntry, canonicalHarness, provider, model string) bool {
	for _, h := range harnesses {
		if canonicalHarness != "" && h.Name != canonicalHarness {
			continue
		}
		providers := h.Providers
		if len(providers) == 0 {
			providers = []ProviderEntry{{Name: ""}}
		}
		for _, p := range providers {
			if candidateProviderIdentity(h, p) != provider {
				continue
			}
			if len(p.DiscoveredIDs) > 0 {
				return FuzzyMatch(model, p.DiscoveredIDs) != ""
			}
			if p.DiscoveryAttempted {
				return false
			}
			return p.DefaultModel == "" || p.DefaultModel == model || harnessCanServeExactModel(h, model)
		}
	}
	return false
}

func requirementPinConflict(req Request, in Inputs, canonicalHarness string) (string, string) {
	if !requiresNoRemote(req) {
		return "", ""
	}
	if req.Provider == "" {
		return "", ""
	}
	for _, h := range in.Harnesses {
		if canonicalHarness != "" && h.Name != canonicalHarness {
			continue
		}
		for _, p := range h.Providers {
			if candidateProviderIdentity(h, p) == req.Provider && !candidateIsLocal(h, p) {
				return "no_remote", req.Provider
			}
		}
	}
	return "", ""
}

func requiresNoRemote(req Request) bool {
	for _, requirement := range req.Require {
		if requirement == "no_remote" {
			return true
		}
	}
	return false
}

func requestAllowsLocal(req Request) bool {
	return req.AllowLocal || req.Policy != "smart"
}

func missingPolicyRequirement(req Request, candidates []Candidate) string {
	if req.Policy == "" {
		return ""
	}
	switch req.ProviderPreference {
	case ProviderPreferenceLocalOnly:
		return "local endpoint"
	case ProviderPreferenceSubscriptionOnly:
		return "subscription harness"
	}
	if requiresNoRemote(req) {
		if anyLocalCandidate(candidates) && !anyEligibleCandidate(candidates) {
			return "no_remote"
		}
		if allCandidatesRejectedFor(candidates, FilterReasonPolicyFiltered) {
			return "no_remote"
		}
	}
	if allCandidatesRejectedFor(candidates, FilterReasonMeteredOptInRequired) {
		return "metered opt-in"
	}
	if !requestAllowsLocal(req) && allCandidatesRejectedFor(candidates, FilterReasonPolicyFiltered) {
		return "allow_local=false"
	}
	return ""
}

func allCandidatesRejectedFor(candidates []Candidate, reason FilterReason) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if candidate.Eligible || candidate.FilterReason != reason {
			return false
		}
	}
	return true
}

func anyEligibleCandidate(candidates []Candidate) bool {
	for _, candidate := range candidates {
		if candidate.Eligible {
			return true
		}
	}
	return false
}

func anyLocalCandidate(candidates []Candidate) bool {
	for _, candidate := range candidates {
		if candidateIsLocalCandidate(candidate) {
			return true
		}
	}
	return false
}

func candidateIsLocalCandidate(candidate Candidate) bool {
	if candidate.Billing == modelcatalog.BillingModelFixed || candidate.CostClass == "local" {
		return true
	}
	return candidate.Harness == "fiz" && candidate.Provider == ""
}

func requestedModelIntent(req Request) string {
	return req.Model
}

func hasLiveDiscoveryCandidates(candidates []rankedCandidate) bool {
	for _, c := range candidates {
		if c.out.Provider != "" && c.internal.Model == "" {
			return true
		}
		if c.out.Provider != "" && c.out.Reason != "" {
			return true
		}
	}
	return false
}

type rankedCandidate struct {
	out      Candidate
	internal candidateInternal
	// quotaExhaustedRetryAfter is non-zero when this candidate would have been
	// eligible save for its provider being in the quota_exhausted state.
	// Resolve uses this to detect the "all eligible providers quota-exhausted"
	// case and surface ErrAllProvidersQuotaExhausted.
	quotaExhaustedRetryAfter time.Time
}

// buildHarnessCandidates expands one HarnessEntry into 1..N candidates, one
// per (harness, provider, resolved-model) tuple.
func buildHarnessCandidates(h HarnessEntry, req Request, in Inputs) []rankedCandidate {
	caps := Capabilities{
		SupportsTools:      h.SupportsTools,
		SupportedReasoning: h.SupportedReasoning,
		MaxReasoningTokens: h.MaxReasoningTokens,
		SupportedPerms:     h.SupportedPerms,
		ExactPinSupport:    h.ExactPinSupport,
		SupportedModels:    h.SupportedModels,
	}

	if !h.Available {
		return []rankedCandidate{{
			out: Candidate{
				Harness:      h.Name,
				CostSource:   CostSourceUnknown,
				Reason:       "harness not available",
				FilterReason: FilterReasonUnhealthy,
			},
			internal: candidateInternal{Harness: h.Name, CostClass: h.CostClass, CostSource: CostSourceUnknown},
		}}
	}

	histRate, hasHist := in.HistoricalSuccess[h.Name]
	if !hasHist {
		histRate = -1
	}

	// Enumerate providers. For harnesses with no providers (subprocess harnesses
	// with vendor-managed billing), emit a single virtual provider entry.
	providers := h.Providers
	if len(providers) == 0 {
		providers = []ProviderEntry{{Name: ""}}
	}

	out := make([]rankedCandidate, 0, len(providers))
	minCtx := req.MinContextWindow()
	stickyServerInstance := ""
	if req.CorrelationID != "" && in.StickyServerInstanceResolver != nil {
		stickyServerInstance, _ = in.StickyServerInstanceResolver(req.CorrelationID)
	}
	for _, p := range providers {
		models, reason := routingModelsForProvider(h, p, req, in)
		if len(models) == 0 {
			models = []string{""}
		}
		for _, model := range models {
			candidateReason := reason
			ctxWin := 0
			ctxSrc := ContextSourceUnknown
			if p.ContextWindows != nil {
				if v, ok := p.ContextWindows[model]; ok {
					ctxWin = v
				}
			}
			if ctxWin > 0 && p.ContextWindowSources != nil {
				if src, ok := p.ContextWindowSources[model]; ok && src != "" {
					ctxSrc = src
				}
			}
			if ctxWin == 0 && p.ContextWindow > 0 {
				ctxWin = p.ContextWindow
				if p.ContextWindowSource != "" {
					ctxSrc = p.ContextWindowSource
				}
			}
			if ctxWin == 0 && model != "" {
				ctxWin = 131072
				ctxSrc = ContextSourceDefault
			}
			if model == "" {
				ctxSrc = ContextSourceUnknown
			}
			entryCaps := caps
			entryCaps.ContextWindow = ctxWin
			entryCaps.SupportsTools = caps.SupportsTools || p.SupportsTools
			if p.SupportsToolsByModel != nil {
				if supported, ok := p.SupportsToolsByModel[model]; ok {
					entryCaps.SupportsTools = supported
				}
			}
			if len(p.DiscoveredIDs) > 0 {
				entryCaps.SupportedModels = nil
			}

			key := ProviderModelKey(p.Name, p.EndpointName, model)
			obs := in.ObservedSpeedTPS[key]
			if obs == 0 && p.EndpointName != "" {
				obs = in.ObservedSpeedTPS[ProviderModelKey(p.Name, "", model)]
			}
			providerSuccessRate := -1.0
			if rate, ok := in.ProviderSuccessRate[key]; ok {
				providerSuccessRate = rate
			} else if p.EndpointName != "" {
				if rate, ok := in.ProviderSuccessRate[ProviderModelKey(p.Name, "", model)]; ok {
					providerSuccessRate = rate
				}
			}
			latencyMS := in.ObservedLatencyMS[key]
			if latencyMS == 0 && p.EndpointName != "" {
				latencyMS = in.ObservedLatencyMS[ProviderModelKey(p.Name, "", model)]
			}
			// Resolve power/auto-routability against the /props-recovered catalog
			// identity when present, so a generic served alias ("dflash") inherits
			// its real model's catalog power while `model` stays the wire identity.
			powerModel := model
			if p.CatalogIDByModel != nil {
				if catalogID := p.CatalogIDByModel[model]; catalogID != "" {
					powerModel = catalogID
				}
			}
			power := candidatePower(in.ModelEligibility, powerModel)
			endpointLoad := EndpointLoad{}
			if in.EndpointLoadResolver != nil {
				loadProvider, loadEndpoint := candidateLoadIdentity(h, p)
				if resolved, ok := in.EndpointLoadResolver(loadProvider, loadEndpoint, model); ok {
					endpointLoad = resolved
				}
			} else if load, ok := in.EndpointLoads[key]; ok {
				endpointLoad = load
			}

			gateReq := resolveRequestReasoning(req, h.Surface, in.ReasoningResolver)
			billingKind := candidateBillingKind(h, p)

			eligible := true
			var filterReason FilterReason
			if candidateReason == "" {
				// Subscription quota is a hard availability gate and must be
				// reported before catalog metadata gates. An exhausted Claude/Codex/
				// Gemini account is never a viable candidate, regardless of whether
				// the resolved model has complete power metadata.
				if h.IsSubscription && !h.SubscriptionOK {
					eligible = false
					candidateReason = h.QuotaReason
					if candidateReason == "" {
						if h.QuotaStale {
							candidateReason = "subscription quota unavailable"
						} else {
							candidateReason = "subscription quota exhausted"
						}
					}
					filterReason = FilterReasonUnhealthy
				} else if model != "" && len(entryCaps.SupportedModels) > 0 && !entryCaps.HasModel(model) {
					eligible = false
					candidateReason = "model not in harness allow-list"
					filterReason = FilterReasonScoredBelowTop
				} else if g, fr := CheckPowerEligibility(in.ModelEligibility, powerModel, gateReq); g != "" {
					eligible = false
					candidateReason = g
					filterReason = fr
				} else if g, fr := CheckGating(entryCaps, gateReq); g != "" {
					eligible = false
					candidateReason = g
					filterReason = fr
				}
			} else {
				eligible = false
				// resolveModel rejection — model resolution is a capability
				// mismatch with no specific public category, so fall through
				// to the catch-all.
				filterReason = FilterReasonScoredBelowTop
			}

			// Hard preference filtering.
			if eligible {
				if !requestAllowsLocal(req) && candidateIsLocal(h, p) {
					eligible = false
					candidateReason = "policy disallows local candidates"
					filterReason = FilterReasonPolicyFiltered
				}
			}

			// Safe mode: no local mixin — local candidates are excluded
			// unconditionally so only subscription/cloud routes are considered.
			if eligible && in.SafeMode && candidateIsLocal(h, p) {
				eligible = false
				candidateReason = "safe_mode: local candidates disabled"
				filterReason = FilterReasonPolicyFiltered
			}
			if eligible && requiresNoRemote(req) && !candidateIsLocal(h, p) {
				eligible = false
				candidateReason = "policy requires no_remote"
				filterReason = FilterReasonPolicyFiltered
			}

			if eligible {
				switch req.ProviderPreference {
				case ProviderPreferenceLocalOnly:
					if !h.IsLocal {
						eligible = false
						candidateReason = "preference is local-only"
						filterReason = FilterReasonUnhealthy
					}
				case ProviderPreferenceSubscriptionOnly:
					if !h.IsSubscription {
						eligible = false
						candidateReason = "preference is subscription-only"
						filterReason = FilterReasonUnhealthy
					}
				}
			}

			if eligible {
				if g, fr := CheckProviderDefaultEligibility(candidateProviderIdentity(h, p), p.ActualCashSpend, billingKind, p.ExcludeFromDefaultRouting, req); g != "" {
					eligible = false
					candidateReason = g
					filterReason = fr
				}
			}

			if eligible && req.Provider != "" && req.Provider != candidateProviderIdentity(h, p) {
				// Hard provider pin: reject every other provider identity, even
				// when a non-pinned candidate would otherwise score higher.
				eligible = false
				candidateReason = fmt.Sprintf("provider override requires %s", req.Provider)
				filterReason = FilterReasonPinMismatch
			}

			// Caller-supplied exclusion list: skip candidates matching a caller
			// health hint. Records FilterReasonCallerExcluded so routing-quality
			// observability can distinguish caller-driven re-routes from internal
			// health signals.
			if eligible && len(req.ExcludedRoutes) > 0 {
				providerID := candidateProviderIdentity(h, p)
				for _, ex := range req.ExcludedRoutes {
					if ex.Provider != providerID {
						continue
					}
					if ex.Model != "" && ex.Model != model {
						continue
					}
					if ex.Endpoint != "" && ex.Endpoint != p.EndpointName {
						continue
					}
					eligible = false
					candidateReason = fmt.Sprintf("excluded by caller hint (provider=%s)", ex.Provider)
					filterReason = FilterReasonCallerExcluded
					break
				}
			}

			inCooldown := exactRouteInCooldown(in, h, p, model)
			if p.Name != "" && p.InCooldown {
				inCooldown = true
			} else if !inCooldown && p.Name != "" && in.CooldownDuration > 0 {
				if failedAt, ok := in.ProviderCooldowns[p.Name]; ok {
					if in.Now.Sub(failedAt) < in.CooldownDuration {
						inCooldown = true
					}
				}
			}

			// Proactive probe gate: endpoints known unreachable from background/startup
			// probing are hard-gated. An explicit provider pin bypasses the gate so
			// operators can still force a dead-endpoint route. Evaluate this before
			// snapshot dial failures so the more specific endpoint_unreachable
			// classification wins deterministically when both signals exist.
			if eligible && p.Name != "" && req.Provider != candidateProviderIdentity(h, p) {
				if _, ok := in.ProbeUnreachable[p.Name]; ok {
					eligible = false
					candidateReason = fmt.Sprintf("provider %s endpoint unreachable (aliveness probe failed)", candidateProviderIdentity(h, p))
					filterReason = FilterReasonEndpointUnreachable
				}
			}

			// FEAT-004 AC-28: known-down endpoints (provider snapshot says
			// unreachable) are dispatchability failures — hard-gate them so the
			// router doesn't burn ~30s per cell dialing a host that's already
			// known to be off. Route-attempt cooldowns remain demotions (handled
			// via inCooldown below) so a single transient failure doesn't break
			// sticky-lease continuity; only proactive discovery failure flips
			// this hard gate.
			//
			// An explicit provider pin bypasses the gate so the operator can
			// still reach the provider intentionally.
			if eligible && p.Name != "" && in.CooldownDuration > 0 && req.Provider != candidateProviderIdentity(h, p) {
				if failedAt, ok := in.ProviderUnreachable[p.Name]; ok && in.Now.Sub(failedAt) < in.CooldownDuration {
					eligible = false
					candidateReason = fmt.Sprintf("provider %s known unreachable (last dial failure %s ago)", candidateProviderIdentity(h, p), in.Now.Sub(failedAt).Truncate(time.Second))
					filterReason = FilterReasonUnhealthy
				}
			}

			// Generic provider-eligibility override: the service layer can mark
			// any provider ineligible with a typed FilterReason and freshness
			// timestamp without baking provider-specific knowledge into the
			// engine. Mirrors the endpoint_unreachable gate's explicit-pin
			// bypass so operators can still force a route intentionally.
			if eligible && p.Name != "" && req.Provider != candidateProviderIdentity(h, p) {
				identity := candidateProviderIdentity(h, p)
				if ov, ok := lookupEligibilityOverride(in.ProviderEligibilityOverrides, identity); ok && ov.FilterReason != FilterReasonEligible {
					eligible = false
					candidateReason = formatEligibilityOverrideReason(identity, ov)
					filterReason = ov.FilterReason
				}
			}

			// Credential gate: providers that need an API key but lack one (or
			// hold an obviously malformed value) are disqualified before any
			// dispatch so the operator sees the root cause instead of a 401.
			// The gate runs even when the operator pins the provider — there is
			// no fallback path that could possibly authenticate.
			if eligible && len(in.ProviderCredentialMissing) > 0 {
				identity := candidateProviderIdentity(h, p)
				if location, ok := lookupCredentialMissing(in.ProviderCredentialMissing, identity); ok {
					eligible = false
					candidateReason = fmt.Sprintf("provider %s credential missing (%s)", identity, location)
					filterReason = FilterReasonCredentialMissing
				}
			}

			// Credit-balance gate: providers whose cached account balance fell
			// below the configured threshold are disqualified before dispatch
			// so the operator sees the root cause instead of an
			// insufficient-credit response. The probe runs in the service
			// layer's freshness cache; the engine reads only the projected
			// evidence map, so the gate is O(1) and side-effect free.
			if eligible && len(in.ProviderCreditExhausted) > 0 {
				identity := candidateProviderIdentity(h, p)
				if ev, ok := lookupCreditExhausted(in.ProviderCreditExhausted, identity); ok {
					eligible = false
					candidateReason = fmt.Sprintf(
						"provider %s credit exhausted (balance $%.4f below threshold $%.2f, observed %s)",
						identity,
						ev.BalanceUSD,
						ev.ThresholdUSD,
						ev.ObservedAt.UTC().Format(time.RFC3339),
					)
					filterReason = FilterReasonCreditExhausted
				}
			}

			// Credential-invalid gate: account-state probe returned 401, meaning
			// the configured API key is present but rejected. The fix is key
			// rotation, distinct from FilterReasonCredentialMissing (configure a
			// key).
			if eligible && len(in.ProviderCredentialInvalid) > 0 {
				identity := candidateProviderIdentity(h, p)
				if ev, ok := lookupCredentialInvalid(in.ProviderCredentialInvalid, identity); ok {
					eligible = false
					candidateReason = fmt.Sprintf(
						"provider %s credential rejected (HTTP %d from credit probe, observed %s)",
						identity,
						ev.HTTPStatus,
						ev.ObservedAt.UTC().Format(time.RFC3339),
					)
					filterReason = FilterReasonCredentialInvalid
				}
			}

			// Provider-unreachable gate (account-state probe): transient
			// transport failure or non-401 5xx from the provider's account
			// endpoint. Soft fail-open: the entry only lasts for the current
			// freshness window, so the next successful probe restores
			// eligibility automatically.
			if eligible && len(in.ProviderProbeUnreachable) > 0 {
				identity := candidateProviderIdentity(h, p)
				if ev, ok := lookupProbeUnreachable(in.ProviderProbeUnreachable, identity); ok {
					eligible = false
					detail := ev.Message
					if ev.StatusCode != 0 {
						candidateReason = fmt.Sprintf(
							"provider %s unreachable (HTTP %d from credit probe: %s, observed %s)",
							identity,
							ev.StatusCode,
							detail,
							ev.ObservedAt.UTC().Format(time.RFC3339),
						)
					} else {
						class := ev.ErrorClass
						if class == "" {
							class = "transport_error"
						}
						candidateReason = fmt.Sprintf(
							"provider %s unreachable (%s from credit probe: %s, observed %s)",
							identity,
							class,
							detail,
							ev.ObservedAt.UTC().Format(time.RFC3339),
						)
					}
					filterReason = FilterReasonProviderUnreachable
				}
			}

			localHealthUnknown := false
			if eligible && p.Name != "" && req.Provider != candidateProviderIdentity(h, p) && candidateIsLocal(h, p) {
				_, localHealthUnknown = in.ProbeUnknown[p.Name]
			}

			// Provider quota-exhausted gate. The state machine lives in the
			// service layer; the engine consumes the projected map of
			// provider-name → retry_after. Apply only when the candidate would
			// otherwise have been eligible — disqualifying an already-rejected
			// candidate with a different reason would lose the original signal.
			var quotaExhaustedRetryAfter time.Time
			if eligible {
				providerKey := candidateProviderIdentity(h, p)
				if providerKey != "" && len(in.ProviderQuotaExhaustedUntil) > 0 {
					if retryAfter, ok := in.ProviderQuotaExhaustedUntil[providerKey]; ok && retryAfter.After(in.Now) {
						eligible = false
						candidateReason = fmt.Sprintf("provider %s quota exhausted until %s", providerKey, retryAfter.Format(time.RFC3339))
						filterReason = FilterReasonQuotaExhausted
						quotaExhaustedRetryAfter = retryAfter
					}
				}
			}

			stickyMatch := stickyServerInstance != "" && serverInstanceMatches(stickyServerInstance, p.ServerInstance, p.EndpointName)
			candidateCost := p.CostUSDPer1kTokens
			if perModel, ok := p.CostUSDPer1kTokensByModel[model]; ok {
				candidateCost = perModel
			}
			ci := candidateInternal{
				Harness:               h.Name,
				Provider:              p.Name,
				Billing:               billingKind,
				EndpointName:          p.EndpointName,
				ServerInstance:        p.ServerInstance,
				Model:                 model,
				CostClass:             candidateCostClass(h, p),
				CostUSDPer1kTokens:    candidateCost,
				CostSource:            normalizeCostSource(p.CostSource),
				ActualCashSpend:       p.ActualCashSpend,
				Power:                 power,
				ContextLength:         ctxWin,
				ContextSource:         ctxSrc,
				IsSubscription:        h.IsSubscription,
				QuotaOK:               h.QuotaOK,
				QuotaPercentUsed:      h.QuotaPercentUsed,
				QuotaStale:            h.QuotaStale,
				QuotaTrend:            h.QuotaTrend,
				SubscriptionOK:        h.SubscriptionOK,
				HistoricalSuccessRate: histRate,
				ProviderSuccessRate:   providerSuccessRate,
				ObservedTokensPerSec:  obs,
				ObservedLatencyMS:     latencyMS,
				InCooldown:            inCooldown || h.InCooldown,
				LocalHealthUnknown:    localHealthUnknown,
				ProviderAffinityMatch: req.Provider != "" && req.Provider == candidateProviderIdentity(h, p),
				ProviderPreference:    req.ProviderPreference,
				EndpointLoad:          endpointLoad.NormalizedLoad,
				EndpointLoadFresh:     endpointLoad.UtilizationFresh,
				EndpointSaturated:     endpointLoad.UtilizationSaturated,
				StickyMatch:           stickyMatch,
				MinPower:              req.MinPower,
				MaxPower:              req.MaxPower,
				Surface:               h.Surface,
				SurfacePreferred:      surfacePreferred(h, req, in),
			}
			if eligible && ctxWin > 0 && minCtx > 0 {
				ci.ContextHeadroom = ctxWin - minCtx
			}
			out = append(out, rankedCandidate{
				out: Candidate{
					Harness:            h.Name,
					Provider:           p.Name,
					Billing:            billingKind,
					Endpoint:           p.EndpointName,
					ServerInstance:     p.ServerInstance,
					Model:              model,
					CostUSDPer1kTokens: candidateCost,
					CostSource:         normalizeCostSource(p.CostSource),
					ActualCashSpend:    p.ActualCashSpend,
					Power:              power,
					ContextLength:      ctxWin,
					ContextSource:      ctxSrc,
					Eligible:           eligible,
					Reason:             candidateReason,
					FilterReason:       filterReason,
					LatencyMS:          latencyMS,
					SuccessRate:        providerSuccessRate,
					CostClass:          candidateCostClass(h, p),
					SpeedTPS:           obs,
					Utilization:        endpointLoad.NormalizedLoad,
					ContextHeadroom:    ci.ContextHeadroom,
					QuotaOK:            h.QuotaOK,
					QuotaPercentUsed:   h.QuotaPercentUsed,
					QuotaTrend:         h.QuotaTrend,
					StickyAffinity: func() float64 {
						if stickyMatch {
							return StickyAffinityBonus
						}
						return 0
					}(),
				},
				internal:                 ci,
				quotaExhaustedRetryAfter: quotaExhaustedRetryAfter,
			})
		}
	}
	return out
}

func routingModelsForProvider(h HarnessEntry, p ProviderEntry, req Request, in Inputs) ([]string, string) {
	// Enumerate every tier the harness lists for unpinned automatic routing so
	// sibling tiers under one auth (e.g. claude opus-4.7 + sonnet-4.6) survive
	// into the candidate pool instead of collapsing to DefaultModel. Pinned
	// Model/Provider still constrain to a single resolution.
	if req.Model == "" && req.Provider == "" && len(h.AutoRoutingModels) > 0 {
		return append([]string(nil), h.AutoRoutingModels...), ""
	}
	model, reason := resolveModel(h, p, req, in)
	if reason != "" {
		return nil, reason
	}
	return []string{model}, ""
}

func candidatePower(lookup func(string) (ModelEligibility, bool), model string) int {
	if lookup == nil || model == "" {
		return 0
	}
	eligibility, ok := lookup(model)
	if !ok {
		return 0
	}
	return eligibility.Power
}

func candidateProviderIdentity(h HarnessEntry, p ProviderEntry) string {
	if p.Name != "" {
		return p.Name
	}
	return h.Name
}

func exactRouteInCooldown(in Inputs, h HarnessEntry, p ProviderEntry, model string) bool {
	if in.CooldownDuration <= 0 || len(in.ExactRouteCooldowns) == 0 {
		return false
	}
	provider, endpoint := cooldownCandidateProviderEndpoint(p)
	for key, failedAt := range in.ExactRouteCooldowns {
		if key == (RouteCooldownKey{}) {
			continue
		}
		if in.Now.Sub(failedAt) >= in.CooldownDuration {
			continue
		}
		if key.Harness != "" && key.Harness != h.Name {
			continue
		}
		if key.Provider != "" && key.Provider != provider {
			continue
		}
		if key.Endpoint != "" && key.Endpoint != endpoint {
			continue
		}
		if key.ServerInstance != "" && key.ServerInstance != p.ServerInstance {
			continue
		}
		if key.Model != "" && key.Model != model {
			continue
		}
		return true
	}
	return false
}

func cooldownCandidateProviderEndpoint(p ProviderEntry) (string, string) {
	provider := strings.TrimSpace(p.Name)
	endpoint := strings.TrimSpace(p.EndpointName)
	base, qualifiedEndpoint, ok := strings.Cut(provider, "@")
	if !ok || strings.TrimSpace(base) == "" || strings.TrimSpace(qualifiedEndpoint) == "" {
		return provider, endpoint
	}
	provider = strings.TrimSpace(base)
	if endpoint == "" {
		endpoint = strings.TrimSpace(qualifiedEndpoint)
	}
	return provider, endpoint
}

// lookupCreditExhausted resolves the credit-balance evidence for a routing
// identity, accepting either the raw identity or its base provider name
// (stripped of any "@endpoint" suffix).
func lookupCreditExhausted(m map[string]ProviderCreditExhaustedEvidence, identity string) (ProviderCreditExhaustedEvidence, bool) {
	if len(m) == 0 || identity == "" {
		return ProviderCreditExhaustedEvidence{}, false
	}
	if ev, ok := m[identity]; ok {
		return ev, true
	}
	if i := strings.IndexByte(identity, '@'); i > 0 {
		if ev, ok := m[identity[:i]]; ok {
			return ev, true
		}
	}
	return ProviderCreditExhaustedEvidence{}, false
}

// lookupCredentialMissing resolves a credential-missing record for a routing
// identity. It accepts both the raw identity and the base provider name
// (stripped of any "@endpoint" suffix) so service config keyed by base name
// still matches endpoint-split candidates.
func lookupCredentialMissing(m map[string]string, identity string) (string, bool) {
	if len(m) == 0 || identity == "" {
		return "", false
	}
	if location, ok := m[identity]; ok {
		return location, true
	}
	if i := strings.IndexByte(identity, '@'); i > 0 {
		if location, ok := m[identity[:i]]; ok {
			return location, true
		}
	}
	return "", false
}

// lookupCredentialInvalid resolves credential-invalid evidence for a routing
// identity. Mirrors lookupCreditExhausted's @endpoint-aware fallback so
// service config keyed by base provider name still matches endpoint-split
// candidate identities.
func lookupCredentialInvalid(m map[string]ProviderCredentialInvalidEvidence, identity string) (ProviderCredentialInvalidEvidence, bool) {
	if len(m) == 0 || identity == "" {
		return ProviderCredentialInvalidEvidence{}, false
	}
	if ev, ok := m[identity]; ok {
		return ev, true
	}
	if i := strings.IndexByte(identity, '@'); i > 0 {
		if ev, ok := m[identity[:i]]; ok {
			return ev, true
		}
	}
	return ProviderCredentialInvalidEvidence{}, false
}

// lookupProbeUnreachable resolves provider-unreachable evidence for a routing
// identity. Mirrors lookupCreditExhausted's @endpoint-aware fallback so
// service config keyed by base provider name still matches endpoint-split
// candidate identities.
func lookupProbeUnreachable(m map[string]ProviderProbeUnreachableEvidence, identity string) (ProviderProbeUnreachableEvidence, bool) {
	if len(m) == 0 || identity == "" {
		return ProviderProbeUnreachableEvidence{}, false
	}
	if ev, ok := m[identity]; ok {
		return ev, true
	}
	if i := strings.IndexByte(identity, '@'); i > 0 {
		if ev, ok := m[identity[:i]]; ok {
			return ev, true
		}
	}
	return ProviderProbeUnreachableEvidence{}, false
}

// lookupEligibilityOverride resolves a generic eligibility override for a
// routing identity. Mirrors the @endpoint-aware fallback used by the typed
// evidence maps so service-layer config keyed by base provider name still
// matches endpoint-split candidate identities. Returns ok=false when the
// map is nil/empty or the identity is unset.
func lookupEligibilityOverride(m map[string]ProviderEligibilityOverride, identity string) (ProviderEligibilityOverride, bool) {
	if len(m) == 0 || identity == "" {
		return ProviderEligibilityOverride{}, false
	}
	if ov, ok := m[identity]; ok {
		return ov, true
	}
	if i := strings.IndexByte(identity, '@'); i > 0 {
		if ov, ok := m[identity[:i]]; ok {
			return ov, true
		}
	}
	return ProviderEligibilityOverride{}, false
}

// formatEligibilityOverrideReason produces the stable human-readable Reason
// string surfaced on a candidate disqualified by a ProviderEligibilityOverride.
// The freshness timestamp is included when populated so operators can see how
// stale the underlying probe signal is.
func formatEligibilityOverrideReason(identity string, ov ProviderEligibilityOverride) string {
	if ov.ProbeAt.IsZero() {
		return fmt.Sprintf("provider %s ineligible (%s)", identity, ov.FilterReason)
	}
	return fmt.Sprintf("provider %s ineligible (%s, observed %s)", identity, ov.FilterReason, ov.ProbeAt.UTC().Format(time.RFC3339))
}

func candidateBillingKind(h HarnessEntry, p ProviderEntry) modelcatalog.BillingModel {
	if p.Billing != modelcatalog.BillingModelUnknown {
		return p.Billing
	}
	if h.IsSubscription {
		return modelcatalog.BillingModelSubscription
	}
	if candidateIsLocal(h, p) {
		return modelcatalog.BillingModelFixed
	}
	if billing := modelcatalog.BillingForProviderSystem(candidateProviderIdentity(h, p)); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	if billing := modelcatalog.BillingForHarness(h.Name); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	return modelcatalog.BillingModelUnknown
}

func candidateIsLocal(h HarnessEntry, p ProviderEntry) bool {
	return candidateCostClass(h, p) == "local" || (p.CostClass == "" && h.IsLocal)
}

func candidateLoadIdentity(h HarnessEntry, p ProviderEntry) (string, string) {
	provider := candidateProviderIdentity(h, p)
	endpoint := p.ServerInstance
	if base, ep, ok := strings.Cut(provider, "@"); ok && base != "" {
		provider = base
		if endpoint == "" {
			endpoint = ep
		}
	}
	if endpoint == "" {
		endpoint = p.EndpointName
	}
	return provider, endpoint
}

func serverInstanceMatches(stickyServerInstance, candidateServerInstance, candidateEndpoint string) bool {
	candidateServerInstance = strings.TrimSpace(candidateServerInstance)
	candidateEndpoint = strings.TrimSpace(candidateEndpoint)
	stickyServerInstance = strings.TrimSpace(stickyServerInstance)
	if stickyServerInstance == "" {
		return false
	}
	if candidateServerInstance != "" {
		return candidateServerInstance == stickyServerInstance
	}
	return candidateEndpoint != "" && candidateEndpoint == stickyServerInstance
}

func candidateCostClass(h HarnessEntry, p ProviderEntry) string {
	if p.CostClass != "" {
		return p.CostClass
	}
	return h.CostClass
}

func normalizeCostSource(source string) string {
	switch source {
	case CostSourceCatalog, CostSourceSubscription, CostSourceUserConfig:
		return source
	default:
		return CostSourceUnknown
	}
}

func sameLocalEndpointGroup(a, b candidateInternal) bool {
	if a.Harness == "" || b.Harness == "" || a.Model == "" || b.Model == "" {
		return false
	}
	if a.CostClass != "local" || b.CostClass != "local" {
		return false
	}
	aBase := providerBaseName(a.Provider)
	bBase := providerBaseName(b.Provider)
	if aBase == "" || bBase == "" || aBase != bBase {
		return false
	}
	return a.Model == b.Model
}

func providerBaseName(provider string) string {
	if base, _, ok := strings.Cut(provider, "@"); ok {
		return base
	}
	return provider
}

func neutralKnownCost(candidates []rankedCandidate) (float64, bool) {
	var total float64
	var count int
	for _, candidate := range candidates {
		if !candidate.out.Eligible {
			continue
		}
		if normalizeCostSource(candidate.out.CostSource) == CostSourceUnknown {
			continue
		}
		total += candidate.out.CostUSDPer1kTokens
		count++
	}
	if count == 0 {
		return 0, false
	}
	return total / float64(count), true
}

func candidateCostTieValue(candidate rankedCandidate, neutralCost float64) float64 {
	if normalizeCostSource(candidate.out.CostSource) == CostSourceUnknown {
		return neutralCost
	}
	return candidate.out.CostUSDPer1kTokens
}

// resolveModel picks the concrete model string for a (harness, provider) pair
// given the request. Returns the model and a non-empty rejection reason if
// resolution fails.
func resolveModel(h HarnessEntry, p ProviderEntry, req Request, in Inputs) (string, string) {
	// 1. Exact pin.
	if req.Model != "" {
		// If the provider has discovery data, try fuzzy matching to map the
		// canonical/short ref to the provider-native ID.
		if len(p.DiscoveredIDs) > 0 {
			if matched := FuzzyMatch(req.Model, p.DiscoveredIDs); matched != "" {
				return matched, ""
			}
			return "", fmt.Sprintf("model %q not on provider %q", req.Model, p.Name)
		}
		if p.DiscoveryAttempted {
			return "", fmt.Sprintf("provider %q has no live discovered models", p.Name)
		}
		return req.Model, ""
	}

	// 2. Provider default → harness default. Empty default is acceptable
	// when no request fields constrained model selection — orphan validation
	// happens at dispatch time.
	if p.DefaultModel != "" {
		if len(p.DiscoveredIDs) > 0 {
			if matched := FuzzyMatch(p.DefaultModel, p.DiscoveredIDs); matched != "" {
				return matched, ""
			}
			return "", fmt.Sprintf("provider default %q not on provider %q", p.DefaultModel, p.Name)
		}
		return p.DefaultModel, ""
	}
	if h.DefaultModel != "" {
		return h.DefaultModel, ""
	}
	return "", ""
}

// EscalatePolicyAware is a helper for policy escalation. Given a request that
// failed at one policy, return the next policy to try, restricted to those
// that have a viable candidate under the current Inputs (i.e., the policy's
// resolved concrete model exists on the request's pinned provider, if any).
//
// Fixes ddx-3c5ba7cc: tier escalation respects --provider affinity.
//
// Returns "" when no further policy is viable.
func EscalatePolicyAware(current string, ladder []string, req Request, in Inputs) string {
	startIdx := -1
	for i, p := range ladder {
		if p == current {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return ""
	}
	for i := startIdx + 1; i < len(ladder); i++ {
		next := ladder[i]
		probe := req
		probe.Policy = next
		if dec, err := Resolve(probe, in); err == nil && dec != nil && dec.Harness != "" {
			return next
		}
	}
	return ""
}
