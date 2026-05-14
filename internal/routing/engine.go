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
	SupportedReasoning  []string
	MaxReasoningTokens  int
	SupportedPerms      []string
	SupportsTools       bool

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
	Name                 string
	BaseURL              string
	ServerInstance       string
	EndpointName         string
	EndpointBaseURL      string
	DefaultModel         string
	Billing              modelcatalog.BillingModel
	CostClass            string
	DiscoveredIDs        []string // models discovered via /v1/models or equivalent
	DiscoveryAttempted   bool
	ContextWindows       map[string]int
	ContextWindowSources map[string]string
	ContextWindow        int
	ContextWindowSource  string
	SupportsTools        bool

	// CostUSDPer1kTokens is the estimated blended USD cost per 1,000 tokens.
	// A zero value with CostSourceUnknown means the provider cost is unknown.
	CostUSDPer1kTokens float64
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
	ProviderCooldowns            map[string]time.Time // by provider name; soft demotion only
	CooldownDuration             time.Duration        // 0 = no cooldown enforcement

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
	Now                         time.Time // injected for deterministic testing; default time.Now()
	ModelEligibility            func(model string) (ModelEligibility, bool)

	// ReasoningResolver returns the catalog's reasoning default for a
	// (policy, surface) pair. When set, buildHarnessCandidates uses it
	// to resolve Reasoning=auto to a concrete level before invoking the
	// capability gate, so candidates that cannot satisfy the resolved level
	// (e.g. an off-only variant under a policy whose default is
	// "high") are correctly disqualified instead of slipping through.
	ReasoningResolver func(policy, surface string) (resolved string, ok bool)
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
	ProviderAffinityMatch bool
	ProviderPreference    string
	EndpointLoad          float64
	EndpointLoadFresh     bool
	EndpointSaturated     bool
	StickyMatch           bool
	MinPower              int
	MaxPower              int
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

	// Compute scores only after eligibility is final. Rejected candidates keep
	// a zero score because cost/utilization/performance/sticky ranking should
	// never influence the eligibility boundary.
	for i := range ranked {
		if !ranked[i].out.Eligible {
			continue
		}
		ranked[i].out.Score = scorePolicy(req.Policy, ranked[i].internal)
		ranked[i].out.ScoreComponents = scoreComponents(req.Policy, ranked[i].internal)
		ranked[i].out.Reason = fmt.Sprintf("policy=%s; score=%.1f", req.Policy, ranked[i].out.Score)
	}
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
	return &Decision{Candidates: out}, noViableCandidateError(req, len(out))
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
		model, reason := resolveModel(h, p, req, in)
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
		power := candidatePower(in.ModelEligibility, model)
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
		if reason == "" {
			// Subscription quota is a hard availability gate and must be
			// reported before catalog metadata gates. An exhausted Claude/Codex/
			// Gemini account is never a viable candidate, regardless of whether
			// the resolved model has complete power metadata.
			if h.IsSubscription && !h.SubscriptionOK {
				eligible = false
				reason = h.QuotaReason
				if reason == "" {
					if h.QuotaStale {
						reason = "subscription quota unavailable"
					} else {
						reason = "subscription quota exhausted"
					}
				}
				filterReason = FilterReasonUnhealthy
			} else if g, fr := CheckPowerEligibility(in.ModelEligibility, model, gateReq); g != "" {
				eligible = false
				reason = g
				filterReason = fr
			} else if g, fr := CheckGating(entryCaps, gateReq); g != "" {
				eligible = false
				reason = g
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
				reason = "policy disallows local candidates"
				filterReason = FilterReasonPolicyFiltered
			}
		}
		if eligible && requiresNoRemote(req) && !candidateIsLocal(h, p) {
			eligible = false
			reason = "policy requires no_remote"
			filterReason = FilterReasonPolicyFiltered
		}

		if eligible {
			switch req.ProviderPreference {
			case ProviderPreferenceLocalOnly:
				if !h.IsLocal {
					eligible = false
					reason = "preference is local-only"
					filterReason = FilterReasonUnhealthy
				}
			case ProviderPreferenceSubscriptionOnly:
				if !h.IsSubscription {
					eligible = false
					reason = "preference is subscription-only"
					filterReason = FilterReasonUnhealthy
				}
			}
		}

		if eligible {
			if g, fr := CheckProviderDefaultEligibility(candidateProviderIdentity(h, p), p.ActualCashSpend, billingKind, p.ExcludeFromDefaultRouting, req); g != "" {
				eligible = false
				reason = g
				filterReason = fr
			}
		}

		if eligible && req.Provider != "" && req.Provider != candidateProviderIdentity(h, p) {
			// Hard provider pin: reject every other provider identity, even
			// when a non-pinned candidate would otherwise score higher.
			eligible = false
			reason = fmt.Sprintf("provider override requires %s", req.Provider)
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
				reason = fmt.Sprintf("excluded by caller hint (provider=%s)", ex.Provider)
				filterReason = FilterReasonCallerExcluded
				break
			}
		}

		inCooldown := false
		if p.Name != "" && p.InCooldown {
			inCooldown = true
		} else if p.Name != "" && in.CooldownDuration > 0 {
			if failedAt, ok := in.ProviderCooldowns[p.Name]; ok {
				if in.Now.Sub(failedAt) < in.CooldownDuration {
					inCooldown = true
				}
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
				reason = fmt.Sprintf("provider %s known unreachable (last dial failure %s ago)", candidateProviderIdentity(h, p), in.Now.Sub(failedAt).Truncate(time.Second))
				filterReason = FilterReasonUnhealthy
			}
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
					reason = fmt.Sprintf("provider %s quota exhausted until %s", providerKey, retryAfter.Format(time.RFC3339))
					filterReason = FilterReasonQuotaExhausted
					quotaExhaustedRetryAfter = retryAfter
				}
			}
		}

		stickyMatch := stickyServerInstance != "" && serverInstanceMatches(stickyServerInstance, p.ServerInstance, p.EndpointName)
		ci := candidateInternal{
			Harness:               h.Name,
			Provider:              p.Name,
			Billing:               billingKind,
			EndpointName:          p.EndpointName,
			ServerInstance:        p.ServerInstance,
			Model:                 model,
			CostClass:             candidateCostClass(h, p),
			CostUSDPer1kTokens:    p.CostUSDPer1kTokens,
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
			ProviderAffinityMatch: req.Provider != "" && req.Provider == candidateProviderIdentity(h, p),
			ProviderPreference:    req.ProviderPreference,
			EndpointLoad:          endpointLoad.NormalizedLoad,
			EndpointLoadFresh:     endpointLoad.UtilizationFresh,
			EndpointSaturated:     endpointLoad.UtilizationSaturated,
			StickyMatch:           stickyMatch,
			MinPower:              req.MinPower,
			MaxPower:              req.MaxPower,
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
				CostUSDPer1kTokens: p.CostUSDPer1kTokens,
				CostSource:         normalizeCostSource(p.CostSource),
				ActualCashSpend:    p.ActualCashSpend,
				Power:              power,
				ContextLength:      ctxWin,
				ContextSource:      ctxSrc,
				Eligible:           eligible,
				Reason:             reason,
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
	return out
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
