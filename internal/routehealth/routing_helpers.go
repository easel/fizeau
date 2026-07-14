package routehealth

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/routing"
)

// PowerRequest is the routing-power subset used to compute the effective
// power policy for a public RouteRequest.
type PowerRequest struct {
	Policy   string
	Model    string
	MinPower int
	MaxPower int
}

// PowerPolicy is the internal representation of the effective power-policy
// evidence that the service surfaces publicly.
type PowerPolicy struct {
	PolicyName string
	MinPower   int
	MaxPower   int
}

// PolicySpec is the narrow policy surface PowerPolicy needs from the catalog.
type PolicySpec struct {
	Name     string
	MinPower int
	MaxPower int
}

// PolicyLookup resolves one named policy.
type PolicyLookup func(name string) (PolicySpec, bool)

// EffectivePowerPolicy merges caller-supplied bounds with the matched policy.
func EffectivePowerPolicy(req PowerRequest, lookup PolicyLookup) PowerPolicy {
	policy := PowerPolicy{
		PolicyName: req.Policy,
		MinPower:   req.MinPower,
		MaxPower:   req.MaxPower,
	}
	if strings.TrimSpace(req.Policy) == "" || lookup == nil {
		return policy
	}
	spec, ok := lookup(req.Policy)
	if !ok {
		return policy
	}
	policy.PolicyName = spec.Name
	if spec.MinPower > 0 && (policy.MinPower == 0 || spec.MinPower > policy.MinPower) {
		policy.MinPower = spec.MinPower
	}
	if spec.MaxPower > 0 && (policy.MaxPower == 0 || spec.MaxPower < policy.MaxPower) {
		policy.MaxPower = spec.MaxPower
	}
	return policy
}

// PowerBoundsForRequest returns the bounds the routing engine should enforce.
func PowerBoundsForRequest(req PowerRequest, policy PowerPolicy) (int, int) {
	if strings.TrimSpace(req.Model) != "" {
		return req.MinPower, req.MaxPower
	}
	return policy.MinPower, policy.MaxPower
}

// FilterReason maps the internal typed filter reason to the public string
// surface without reparsing human-readable reason text.
func FilterReason(candidate routing.Candidate) string {
	if candidate.Eligible {
		return ""
	}
	return string(candidate.FilterReason)
}

// EscalatePolicyLadder walks routing.PolicyEscalationLadder when a lower tier
// produced only rejected candidates. The caller supplies the escalation gate so
// root-owned errors can stay in the root package without an import cycle.
func EscalatePolicyLadder(req routing.Request, in routing.Inputs, origErr error, displayPolicy string, shouldEscalate func(error) bool) (bool, *routing.Decision, error) {
	if origErr == nil || strings.TrimSpace(req.Policy) == "" {
		return false, nil, nil
	}
	if shouldEscalate != nil && !shouldEscalate(origErr) {
		return false, nil, nil
	}
	startIdx := -1
	for i, policy := range routing.PolicyEscalationLadder {
		if policy == req.Policy {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return false, nil, nil
	}
	for i := startIdx + 1; i < len(routing.PolicyEscalationLadder); i++ {
		probe := req
		probe.Policy = routing.PolicyEscalationLadder[i]
		dec, err := routing.Resolve(probe, in)
		if err == nil && dec != nil && dec.Harness != "" {
			return true, dec, nil
		}
	}
	starting := displayPolicy
	if starting == "" {
		starting = req.Policy
	}
	return true, nil, &routing.ErrNoLiveProvider{
		PromptTokens:   req.EstimatedPromptTokens,
		RequiresTools:  req.RequiresTools,
		StartingPolicy: starting,
		MinPower:       req.MinPower,
		MaxPower:       req.MaxPower,
		AllowLocal:     req.AllowLocal,
	}
}

// ShouldEscalateOnError reports whether a routing-engine error is eligible for
// policy-ladder escalation.
func ShouldEscalateOnError(err error) bool {
	var modelErr *routing.ErrHarnessModelIncompatible
	if errors.As(err, &modelErr) {
		return false
	}
	var pinErr *routing.ErrUnsatisfiablePin
	if errors.As(err, &pinErr) {
		return false
	}
	var policyErr *routing.ErrPolicyRequirementUnsatisfied
	if errors.As(err, &policyErr) {
		return false
	}
	return true
}

// Cooldown is the internal cooldown evidence shape used by adapters in the
// root package.
type Cooldown struct {
	Reason      string
	Until       time.Time
	FailCount   int
	LastError   string
	LastAttempt time.Time
}

// CooldownTTL applies the default route-attempt TTL when the configured
// cooldown is unset.
func CooldownTTL(configured time.Duration) time.Duration {
	if configured <= 0 {
		return DefaultCooldown
	}
	return configured
}

// CooldownFromRecord projects one active failure record into cooldown state.
func CooldownFromRecord(record Record, ttl time.Duration) Cooldown {
	ttl = CooldownTTL(ttl)
	reason := record.Reason
	if reason == "" {
		reason = "route_attempt_failure"
	}
	return Cooldown{
		Reason:      reason,
		Until:       record.RecordedAt.Add(ttl),
		FailCount:   1,
		LastError:   record.Error,
		LastAttempt: record.RecordedAt,
	}
}

// CandidateCooldown finds the newest active failure matching the candidate
// scope and returns its cooldown evidence.
func CandidateCooldown(records []Record, providerName, endpointName, model string, ttl time.Duration) *Cooldown {
	var newest *Record
	for i := range records {
		record := &records[i]
		if record.Key.Provider == "" {
			continue
		}
		recordProvider := record.Key.Provider
		recordEndpoint := record.Key.Endpoint
		if base, endpoint, ok := splitProviderRef(recordProvider); ok {
			recordProvider = base
			if recordEndpoint == "" {
				recordEndpoint = endpoint
			}
		}
		if providerName != "" && recordProvider != providerName {
			continue
		}
		if endpointName != "" && recordEndpoint != "" && recordEndpoint != endpointName {
			continue
		}
		if record.Key.Model != "" && model != "" && record.Key.Model != model {
			continue
		}
		if newest == nil || record.RecordedAt.After(newest.RecordedAt) {
			newest = record
		}
	}
	if newest == nil {
		return nil
	}
	cooldown := CooldownFromRecord(*newest, ttl)
	return &cooldown
}

// ApplyAttemptCooldowns projects active attempt failures into routing inputs.
func ApplyAttemptCooldowns(in *routing.Inputs, records []Record, ttl time.Duration) {
	if in == nil || len(records) == 0 {
		return
	}
	ttl = CooldownTTL(ttl)
	if in.CooldownDuration <= 0 {
		in.CooldownDuration = ttl
	}
	for _, record := range records {
		if record.Key.Harness != "" {
			if in.ExactRouteCooldowns == nil {
				in.ExactRouteCooldowns = make(map[routing.RouteCooldownKey]time.Time)
			}
			key := routing.RouteCooldownKey{
				Harness:        strings.TrimSpace(record.Key.Harness),
				Provider:       strings.TrimSpace(record.Key.Provider),
				Endpoint:       strings.TrimSpace(record.Key.Endpoint),
				ServerInstance: strings.TrimSpace(record.Key.ServerInstance),
				Model:          strings.TrimSpace(record.Key.Model),
			}
			if base, endpoint, ok := splitProviderRef(key.Provider); ok {
				key.Provider = base
				if key.Endpoint == "" {
					key.Endpoint = endpoint
				}
			}
			existing, ok := in.ExactRouteCooldowns[key]
			if !ok || record.RecordedAt.After(existing) {
				in.ExactRouteCooldowns[key] = record.RecordedAt
			}
			continue
		}
		if record.Key.Provider != "" {
			if in.ProviderCooldowns == nil {
				in.ProviderCooldowns = make(map[string]time.Time)
			}
			existing, ok := in.ProviderCooldowns[record.Key.Provider]
			if !ok || record.RecordedAt.After(existing) {
				in.ProviderCooldowns[record.Key.Provider] = record.RecordedAt
			}
			if IsDispatchabilityFailure(record.Error) {
				if in.ProviderUnreachable == nil {
					in.ProviderUnreachable = make(map[string]time.Time)
				}
				existing, ok := in.ProviderUnreachable[record.Key.Provider]
				if !ok || record.RecordedAt.After(existing) {
					in.ProviderUnreachable[record.Key.Provider] = record.RecordedAt
				}
			}
		}
	}
}

var dispatchabilityFailureSubstrings = []string{
	"dial tcp",
	"connection refused",
	"no route to host",
	"network is unreachable",
	"i/o timeout",
	"no such host",
	"502 bad gateway",
	"503 service unavailable",
	"504 gateway timeout",
	" 502 ",
	" 503 ",
	" 504 ",
}

// IsDispatchabilityFailure reports whether the error describes a known-down
// provider endpoint rather than a transient request failure.
func IsDispatchabilityFailure(errMsg string) bool {
	if errMsg == "" {
		return false
	}
	lower := strings.ToLower(errMsg)
	for _, pattern := range dispatchabilityFailureSubstrings {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// SnapshotSource is the narrow snapshot metadata surface needed to project
// discovery failures into routing cooldowns.
type SnapshotSource struct {
	Name            string
	Error           string
	LastRefreshedAt time.Time
}

// ProviderCooldownsFromSnapshotErrors converts recent dial-class discovery
// failures into provider cooldown timestamps keyed by configured provider.
func ProviderCooldownsFromSnapshotErrors(sources []SnapshotSource, providerNames []string, now time.Time, ttl time.Duration) map[string]time.Time {
	if len(sources) == 0 || len(providerNames) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	ttl = CooldownTTL(ttl)

	names := append([]string(nil), providerNames...)
	sort.SliceStable(names, func(i, j int) bool {
		return len(names[i]) > len(names[j])
	})

	cooldowns := make(map[string]time.Time)
	for _, source := range sources {
		if !IsDispatchabilityFailure(source.Error) {
			continue
		}
		failedAt := source.LastRefreshedAt
		if failedAt.IsZero() {
			failedAt = now
		}
		if ttl > 0 && now.Sub(failedAt) >= ttl {
			continue
		}
		for _, name := range names {
			if name == source.Name || strings.HasPrefix(source.Name, name+"-") {
				if existing, ok := cooldowns[name]; !ok || failedAt.After(existing) {
					cooldowns[name] = failedAt
				}
				break
			}
		}
	}
	if len(cooldowns) == 0 {
		return nil
	}
	return cooldowns
}

func splitProviderRef(ref string) (string, string, bool) {
	ref = strings.TrimSpace(ref)
	base, endpoint, ok := strings.Cut(ref, "@")
	if !ok || strings.TrimSpace(base) == "" || strings.TrimSpace(endpoint) == "" {
		return "", "", false
	}
	return strings.TrimSpace(base), strings.TrimSpace(endpoint), true
}
