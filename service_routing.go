package fizeau

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modeleligibility"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serverinstance"
)

var loadRoutingCatalog = modelcatalog.Default

// ResolveRoute resolves an under-specified RouteRequest to a concrete
// (Harness, Provider, Model) decision per CONTRACT-003.
//
// The implementation delegates to internal/routing.Resolve — the single
// routing engine that consolidates DDx-side harness-tier ranking and
// fiz-side provider failover ordering.
func (s *service) ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDecision, error) {
	if err := ValidatePowerBounds(req.MinPower, req.MaxPower); err != nil {
		return nil, err
	}
	if err := ValidateRole(req.Role); err != nil {
		return nil, err
	}
	if err := ValidateCorrelationID(req.CorrelationID); err != nil {
		return nil, err
	}
	if req.Harness != "" && req.Model != "" {
		canonical := harnesses.ResolveHarnessAlias(req.Harness)
		if !s.registry.Has(canonical) {
			return nil, fmt.Errorf("unknown harness %q", req.Harness)
		}
		cfg, _ := s.registry.Get(canonical)
		if err := validateExplicitHarnessModel(canonical, cfg, req.Model, req.Provider); err != nil {
			return nil, err
		}
	}
	if req.Harness != "" && req.Policy != "" {
		canonical := harnesses.ResolveHarnessAlias(req.Harness)
		if !s.registry.Has(canonical) {
			return nil, fmt.Errorf("unknown harness %q", req.Harness)
		}
		cfg, _ := s.registry.Get(canonical)
		if err := validateExplicitHarnessPolicy(canonical, cfg, req.Policy); err != nil {
			return nil, err
		}
	}
	cat := serviceRoutingCatalog()
	requestedPolicy := req.Policy
	policy := routingPolicyForName(cat, requestedPolicy)
	powerPolicy := routePowerPolicyForRequest(cat, req)
	providerPreference, err := providerPreferenceForPolicy(cat, requestedPolicy)
	if err != nil {
		return &RouteDecision{
			RequestedPolicy: req.Policy,
			PowerPolicy:     powerPolicy,
		}, err
	}
	in, snapshot := s.buildRoutingInputsWithCatalog(ctx, cat, modelsnapshot.RefreshNone)

	resolvedModel, modelCandidates, modelErr := s.resolveModelConstraint(req.Harness, req.Provider, req.Model, in, cat)
	if modelErr != nil {
		result := &RouteDecision{
			RequestedPolicy: req.Policy,
			PowerPolicy:     powerPolicy,
			Candidates:      modelCandidates,
		}
		s.annotateRouteDecisionEvidence(result)
		return result, publicRoutingError(modelErr, result.Candidates, req.Policy)
	}

	rReq := routing.Request{
		Policy:                policy,
		Model:                 resolvedModel,
		Provider:              req.Provider,
		Harness:               req.Harness,
		Reasoning:             effectiveReasoningString(req.Reasoning),
		Permissions:           req.Permissions,
		ProviderPreference:    providerPreference,
		EstimatedPromptTokens: req.EstimatedPromptTokens,
		RequiresTools:         req.RequiresTools,
		CorrelationID:         req.CorrelationID,
		AllowLocal:            req.AllowLocal,
		Require:               append([]string(nil), req.Require...),
		ExcludedRoutes:        publicToRoutingExcludedRoutes(req.ExcludedRoutes),
	}
	if policyEntry, _, ok := policyForName(cat, requestedPolicy); ok {
		rReq.AllowLocal = rReq.AllowLocal || policyEntry.AllowLocal
		rReq.Require = append(append([]string(nil), policyEntry.Require...), rReq.Require...)
	}
	rReq.MinPower, rReq.MaxPower = routePowerBoundsForRequest(req, powerPolicy)
	s.applyRouteAttemptCooldowns(&in)
	dec, err := routing.Resolve(rReq, in)
	if err != nil {
		if escalated, edec, eerr := escalatePolicyLadder(rReq, in, err, req.Policy); escalated {
			dec = edec
			err = eerr
		}
	}
	result := routeDecisionFromInternal(dec, powerPolicy)
	if err != nil {
		if result == nil {
			result = &RouteDecision{}
		}
		result.RequestedPolicy = req.Policy
		result.PowerPolicy = powerPolicy
		s.annotateRouteDecisionEvidence(result)
		return result, publicRoutingError(err, result.Candidates, req.Policy)
	}
	s.applyStickyRouteLease(req.CorrelationID, result)
	if result != nil && result.Endpoint == "" {
		_, endpoint, _ := splitEndpointProviderRef(result.Provider)
		result.Endpoint = endpoint
	}
	s.annotateRouteDecisionSnapshotEvidence(result, snapshot)
	s.annotateRouteDecisionEvidence(result)
	// Cache the decision so RouteStatus can surface LastDecision.
	if result != nil {
		result.RequestedPolicy = req.Policy
		result.PowerPolicy = powerPolicy
		result.Model = resolveSubprocessModelAlias(result.Harness, result.Model)
		result.Power = catalogPowerForModel(cat, result.Model)
	}
	s.cacheRouteDecision(req.Model, result)
	return result, nil
}

func routeDecisionFromInternal(dec *routing.Decision, powerPolicy RoutePowerPolicy) *RouteDecision {
	if dec == nil {
		return nil
	}
	return &RouteDecision{
		Harness:        dec.Harness,
		Provider:       dec.Provider,
		Endpoint:       dec.Endpoint,
		ServerInstance: dec.ServerInstance,
		Model:          dec.Model,
		Reason:         dec.Reason,
		Candidates:     routeCandidatesFromInternal(dec.Candidates, powerPolicy),
	}
}

func routeCandidatesFromInternal(candidates []routing.Candidate, powerPolicy RoutePowerPolicy) []RouteCandidate {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]RouteCandidate, len(candidates))
	for i, candidate := range candidates {
		out[i] = routeCandidateFromInternal(candidate, powerPolicy)
	}
	return out
}

func routeCandidateFromInternal(candidate routing.Candidate, powerPolicy RoutePowerPolicy) RouteCandidate {
	components := RouteCandidateComponents{
		Power:            candidate.Power,
		Cost:             candidate.CostUSDPer1kTokens,
		CostClass:        candidate.CostClass,
		LatencyMS:        candidate.LatencyMS,
		SpeedTPS:         candidate.SpeedTPS,
		Utilization:      candidate.Utilization,
		SuccessRate:      candidate.SuccessRate,
		QuotaOK:          candidate.QuotaOK,
		QuotaPercentUsed: candidate.QuotaPercentUsed,
		QuotaTrend:       candidate.QuotaTrend,
		Capability:       capabilityScoreForCostClass(candidate.CostClass),
		ContextHeadroom:  candidate.ContextHeadroom,
		StickyAffinity:   candidate.StickyAffinity,
	}
	powerHintFit := scorePowerHintFit(candidate.Power, powerPolicy)
	scorePower := candidate.ScoreComponents["power"]
	scoreCost := candidate.ScoreComponents["cost"]
	scorePerformance := candidate.ScoreComponents["performance"]
	scoreLocality := candidate.ScoreComponents["deployment_locality"]
	scoreQuota := candidate.ScoreComponents["quota_health"]
	scoreUtilization := candidate.ScoreComponents["utilization"]
	components.PowerHintFit = powerHintFit
	components.PowerWeightedCapability = scorePower - powerHintFit + positiveScorePart(scoreCost)
	components.LatencyWeight = positiveScorePart(scorePerformance)
	components.StaleSignalPenalty = positiveScorePart(-scorePerformance)
	components.PlacementBonus = scoreLocality + candidate.StickyAffinity
	components.QuotaBonus = positiveScorePart(scoreQuota)
	components.MarginalCostPenalty = positiveScorePart(-scoreCost)
	components.AvailabilityPenalty = positiveScorePart(-scoreQuota) + positiveScorePart(-scoreUtilization)
	return RouteCandidate{
		Harness:             candidate.Harness,
		Provider:            candidate.Provider,
		Billing:             candidate.Billing,
		ActualCashSpend:     candidate.ActualCashSpend,
		Endpoint:            candidate.Endpoint,
		ServerInstance:      candidate.ServerInstance,
		Model:               candidate.Model,
		Score:               candidate.Score,
		CostUSDPer1kTokens:  candidate.CostUSDPer1kTokens,
		CostSource:          candidate.CostSource,
		EffectiveCost:       candidate.CostUSDPer1kTokens,
		EffectiveCostSource: candidate.CostSource,
		Eligible:            candidate.Eligible,
		Reason:              candidate.Reason,
		FilterReason:        publicFilterReason(candidate),
		ContextLength:       candidate.ContextLength,
		ContextSource:       candidate.ContextSource,
		Components:          components,
	}
}

func scorePowerHintFit(power int, policy RoutePowerPolicy) float64 {
	if power <= 0 {
		return 0
	}
	if policy.MinPower > 0 && power < policy.MinPower {
		// Mirror the engine scorer: materially underpowered routes should not
		// win just because they are cheap.
		return -float64(policy.MinPower-power) * 12
	}
	if policy.MaxPower > 0 && power > policy.MaxPower {
		return -float64(power - policy.MaxPower)
	}
	return 0
}

func positiveScorePart(v float64) float64 {
	if v > 0 {
		return v
	}
	return 0
}

type routeSnapshotCandidateKey struct {
	Provider       string
	Endpoint       string
	ServerInstance string
	Model          string
}

func routeSnapshotCandidateIndex(snapshot modelsnapshot.ModelSnapshot) map[routeSnapshotCandidateKey]modelsnapshot.KnownModel {
	if len(snapshot.Models) == 0 {
		return nil
	}
	out := make(map[routeSnapshotCandidateKey]modelsnapshot.KnownModel, len(snapshot.Models))
	for _, row := range snapshot.Models {
		key := routeSnapshotCandidateKey{
			Provider:       strings.TrimSpace(row.Provider),
			Endpoint:       strings.TrimSpace(row.EndpointName),
			ServerInstance: strings.TrimSpace(serverinstance.Normalize(row.EndpointBaseURL, row.ServerInstance)),
			Model:          strings.TrimSpace(row.ID),
		}
		if key.Provider == "" || key.Model == "" {
			continue
		}
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = row
	}
	return out
}

func routeSnapshotEvidenceForCandidate(candidate RouteCandidate, snapshot modelsnapshot.ModelSnapshot) (modelsnapshot.KnownModel, bool) {
	index := routeSnapshotCandidateIndex(snapshot)
	if len(index) == 0 {
		return modelsnapshot.KnownModel{}, false
	}
	provider := strings.TrimSpace(candidate.Provider)
	endpoint := strings.TrimSpace(candidate.Endpoint)
	serverInstance := strings.TrimSpace(candidate.ServerInstance)
	model := strings.TrimSpace(candidate.Model)
	if base, ep, ok := splitEndpointProviderRef(provider); ok {
		provider = base
		if endpoint == "" {
			endpoint = ep
		}
	}
	keys := []routeSnapshotCandidateKey{{
		Provider:       provider,
		Endpoint:       endpoint,
		ServerInstance: serverInstance,
		Model:          model,
	}}
	if endpoint == "" {
		keys = append(keys, routeSnapshotCandidateKey{
			Provider:       provider,
			ServerInstance: serverInstance,
			Model:          model,
		})
	}
	if serverInstance == "" {
		keys = append(keys, routeSnapshotCandidateKey{
			Provider: provider,
			Endpoint: endpoint,
			Model:    model,
		})
		if endpoint == "" {
			keys = append(keys, routeSnapshotCandidateKey{
				Provider: provider,
				Model:    model,
			})
		}
	}
	for _, key := range keys {
		if row, ok := index[key]; ok {
			return row, true
		}
	}
	for _, row := range snapshot.Models {
		rowProvider := strings.TrimSpace(row.Provider)
		rowEndpoint := strings.TrimSpace(row.EndpointName)
		rowServerInstance := strings.TrimSpace(serverinstance.Normalize(row.EndpointBaseURL, row.ServerInstance))
		if rowProvider != provider || strings.TrimSpace(row.ID) != model {
			continue
		}
		if endpoint != "" && rowEndpoint != endpoint {
			continue
		}
		if serverInstance != "" && rowServerInstance != serverInstance {
			continue
		}
		return row, true
	}
	return modelsnapshot.KnownModel{}, false
}

func applyRouteSnapshotEvidence(candidate *RouteCandidate, row modelsnapshot.KnownModel) {
	if candidate == nil {
		return
	}
	if candidate.ServerInstance == "" {
		candidate.ServerInstance = strings.TrimSpace(serverinstance.Normalize(row.EndpointBaseURL, row.ServerInstance))
	}
	candidate.SourceStatus = string(row.Status)
	candidate.AutoRoutable = row.AutoRoutable
	candidate.ExactPinOnly = row.ExactPinOnly
	candidate.ExclusionReason = row.ExclusionReason
	candidate.ActualCashSpend = row.ActualCashSpend
	candidate.EffectiveCost = row.EffectiveCost
	candidate.EffectiveCostSource = row.EffectiveCostSource
	candidate.ModelDiscoveryFreshnessAt = row.DiscoveredAt.UTC()
	candidate.ModelDiscoveryFreshnessSource = string(row.DiscoveredVia)
	candidate.HealthFreshnessAt = row.HealthFreshnessAt.UTC()
	candidate.HealthFreshnessSource = row.HealthFreshnessSource
	candidate.QuotaFreshnessAt = row.QuotaFreshnessAt.UTC()
	candidate.QuotaFreshnessSource = row.QuotaFreshnessSource
}

func applyRouteSnapshotEvidenceToStatus(candidate *RouteCandidateStatus, row modelsnapshot.KnownModel) {
	if candidate == nil {
		return
	}
	candidate.SourceStatus = string(row.Status)
	candidate.AutoRoutable = row.AutoRoutable
	candidate.ExactPinOnly = row.ExactPinOnly
	candidate.ExclusionReason = row.ExclusionReason
	candidate.Power = row.Power
	candidate.ContextLength = row.ContextWindow
	candidate.CostInputPerMTok = row.CostInputPerM
	candidate.CostOutputPerMTok = row.CostOutputPerM
	candidate.RecentLatencyMS = float64(row.RecentP50Latency.Milliseconds())
	candidate.QuotaRemaining = row.QuotaRemaining
	candidate.ActualCashSpend = row.ActualCashSpend
	candidate.EffectiveCost = row.EffectiveCost
	candidate.EffectiveCostSource = row.EffectiveCostSource
	candidate.HealthFreshnessAt = row.HealthFreshnessAt.UTC()
	candidate.HealthFreshnessSource = row.HealthFreshnessSource
	candidate.QuotaFreshnessAt = row.QuotaFreshnessAt.UTC()
	candidate.QuotaFreshnessSource = row.QuotaFreshnessSource
	candidate.ModelDiscoveryFreshnessAt = row.DiscoveredAt.UTC()
	candidate.ModelDiscoveryFreshnessSource = string(row.DiscoveredVia)
}

func (s *service) annotateRouteDecisionSnapshotEvidence(decision *RouteDecision, snapshot modelsnapshot.ModelSnapshot) {
	if s == nil || decision == nil {
		return
	}
	decision.SnapshotCapturedAt = snapshot.AsOf
	for i := range decision.Candidates {
		if row, ok := routeSnapshotEvidenceForCandidate(decision.Candidates[i], snapshot); ok {
			applyRouteSnapshotEvidence(&decision.Candidates[i], row)
		}
		decision.Candidates[i].SnapshotCapturedAt = snapshot.AsOf
	}
}

func (s *service) annotateRouteDecisionEvidence(decision *RouteDecision) {
	if s == nil || decision == nil {
		return
	}
	decision.Utilization = s.routeUtilizationEvidence(decision.Provider, decision.ServerInstance, decision.Endpoint, decision.Model)
	for i := range decision.Candidates {
		decision.Candidates[i].Utilization = s.routeUtilizationEvidence(
			decision.Candidates[i].Provider,
			decision.Candidates[i].ServerInstance,
			decision.Candidates[i].Endpoint,
			decision.Candidates[i].Model,
		)
	}
}

func (s *service) routeUtilizationEvidence(provider, serverInstance, endpoint, model string) RouteUtilizationState {
	if s == nil || s.routeUtilization == nil {
		return RouteUtilizationState{}
	}
	keyProvider := strings.TrimSpace(provider)
	keyServerInstance := strings.TrimSpace(serverInstance)
	keyEndpoint := strings.TrimSpace(endpoint)
	if base, ep, ok := splitEndpointProviderRef(keyProvider); ok {
		keyProvider = base
		if keyEndpoint == "" {
			keyEndpoint = ep
		}
	}
	if keyServerInstance == "" {
		keyServerInstance = keyEndpoint
	}
	sample, ok := s.routeUtilization.Sample(keyProvider, keyServerInstance, model)
	if !ok && keyEndpoint != "" && keyEndpoint != keyServerInstance {
		sample, ok = s.routeUtilization.Sample(keyProvider, keyEndpoint, model)
	}
	if !ok {
		return RouteUtilizationState{}
	}
	return routeUtilizationStateFromSample(sample)
}

func routeUtilizationStateFromSample(sample utilization.EndpointUtilization) RouteUtilizationState {
	out := RouteUtilizationState{
		Source:     string(sample.Source),
		Freshness:  string(sample.Freshness),
		ObservedAt: sample.ObservedAt,
	}
	if sample.ActiveRequests != nil {
		out.ActiveRequests = utilization.Int(*sample.ActiveRequests)
	}
	if sample.QueuedRequests != nil {
		out.QueuedRequests = utilization.Int(*sample.QueuedRequests)
	}
	if sample.MaxConcurrency != nil {
		out.MaxConcurrency = utilization.Int(*sample.MaxConcurrency)
	}
	if sample.CacheUsage != nil {
		v := *sample.CacheUsage
		out.CachePressure = &v
	}
	if out.CachePressure == nil && sample.MaxConcurrency != nil && *sample.MaxConcurrency > 0 {
		total := 0
		if sample.ActiveRequests != nil {
			total += *sample.ActiveRequests
		}
		if sample.QueuedRequests != nil {
			total += *sample.QueuedRequests
		}
		pressure := float64(total) / float64(*sample.MaxConcurrency)
		out.CachePressure = &pressure
	}
	return out
}

// publicFilterReason maps the typed FilterReason emitted by the internal
// routing engine to the public FilterReason* string constant. The internal
// constants are defined to share string values with the public surface, so
// this is a one-line passthrough — there is no string parsing.
func publicFilterReason(c routing.Candidate) string {
	if c.Eligible {
		return ""
	}
	return string(c.FilterReason)
}

// capabilityScoreForCostClass maps the harness cost class to a coarse
// numeric capability proxy. Mirrors the engine's costClassRank ordering
// (more expensive ≈ more capable) for reporting purposes only.
func capabilityScoreForCostClass(class string) float64 {
	switch class {
	case "local":
		return 0
	case "cheap":
		return 1
	case "medium", "":
		return 2
	case "expensive":
		return 3
	case "experimental":
		return -1
	default:
		return 0
	}
}

// escalatePolicyLadder walks routing.PolicyEscalationLadder when Resolve
// returns a "no eligible candidate" error and the request's policy is in
// the ladder. Returns (true, decision, nil) when a higher tier resolves to
// an eligible candidate, or (true, nil, *routing.ErrNoLiveProvider) when
// the entire remaining ladder is also empty. Returns (false, _, _) when
// escalation does not apply (hard pin error, policy not in ladder, etc.).
func escalatePolicyLadder(req routing.Request, in routing.Inputs, origErr error, displayPolicy string) (bool, *routing.Decision, error) {
	if origErr == nil || req.Policy == "" {
		return false, nil, nil
	}
	if !shouldEscalateOnError(origErr) {
		return false, nil, nil
	}
	startIdx := -1
	for i, p := range routing.PolicyEscalationLadder {
		if p == req.Policy {
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

// shouldEscalateOnError gates ladder escalation to "no eligible candidate"
// errors. Hard caller-pin conflicts (ErrHarnessModelIncompatible,
// ErrPolicyRequirementUnsatisfied) are surfaced as-is — escalating past an explicit
// pin would silently change the caller's intent.
func shouldEscalateOnError(err error) bool {
	var modelConstraintAmbiguous *ErrModelConstraintAmbiguous
	if errors.As(err, &modelConstraintAmbiguous) {
		return false
	}
	var modelConstraintNoMatch *ErrModelConstraintNoMatch
	if errors.As(err, &modelConstraintNoMatch) {
		return false
	}
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

func publicRoutingError(err error, candidates []RouteCandidate, requestedPolicy ...string) error {
	displayPolicy := func(policy string) string {
		if len(requestedPolicy) > 0 && requestedPolicy[0] != "" {
			return requestedPolicy[0]
		}
		return policy
	}
	var modelErr *routing.ErrHarnessModelIncompatible
	if errors.As(err, &modelErr) {
		return withRouteCandidates(&ErrHarnessModelIncompatible{
			Harness:         modelErr.Harness,
			Model:           modelErr.Model,
			SupportedModels: append([]string(nil), modelErr.SupportedModels...),
		}, candidates)
	}
	var policyErr *routing.ErrPolicyRequirementUnsatisfied
	if errors.As(err, &policyErr) {
		return withRouteCandidates(&ErrPolicyRequirementUnsatisfied{
			Policy:       displayPolicy(policyErr.Policy),
			Requirement:  policyErr.Requirement,
			AttemptedPin: policyErr.AttemptedPin,
			Rejected:     policyErr.Rejected,
		}, candidates)
	}
	var unknownPolicyErr *routing.ErrUnknownPolicy
	if errors.As(err, &unknownPolicyErr) {
		return withRouteCandidates(&ErrUnknownPolicy{
			Policy: displayPolicy(unknownPolicyErr.Policy),
		}, candidates)
	}
	var pinErr *routing.ErrUnsatisfiablePin
	if errors.As(err, &pinErr) {
		return withRouteCandidates(&ErrUnsatisfiablePin{
			Pin:    pinErr.Pin,
			Reason: pinErr.Reason,
		}, candidates)
	}
	var noLiveErr *routing.ErrNoLiveProvider
	if errors.As(err, &noLiveErr) {
		return withRouteCandidates(&ErrNoLiveProvider{
			PromptTokens:   noLiveErr.PromptTokens,
			RequiresTools:  noLiveErr.RequiresTools,
			StartingPolicy: displayPolicy(noLiveErr.StartingPolicy),
		}, candidates)
	}
	var quotaErr *routing.ErrAllProvidersQuotaExhausted
	if errors.As(err, &quotaErr) {
		return withRouteCandidates(&NoViableProviderForNow{
			RetryAfter:         quotaErr.RetryAfter,
			ExhaustedProviders: append([]string(nil), quotaErr.ExhaustedProviders...),
		}, candidates)
	}
	return withRouteCandidates(err, candidates)
}

func withRouteCandidates(err error, candidates []RouteCandidate) error {
	if err == nil || len(candidates) == 0 {
		return err
	}
	return &routeDecisionError{
		err:        err,
		candidates: append([]RouteCandidate(nil), candidates...),
	}
}

func (s *service) applyRouteAttemptCooldowns(in *routing.Inputs) {
	if in == nil {
		return
	}
	ttl := s.routeAttemptTTL()
	records := s.activeRouteAttempts(time.Now(), ttl)
	if len(records) == 0 {
		return
	}
	if in.ProviderCooldowns == nil {
		in.ProviderCooldowns = make(map[string]time.Time)
	}
	if in.CooldownDuration <= 0 {
		in.CooldownDuration = ttl
	}
	for _, record := range records {
		if record.Key.Provider != "" {
			existing, ok := in.ProviderCooldowns[record.Key.Provider]
			if !ok || record.RecordedAt.After(existing) {
				in.ProviderCooldowns[record.Key.Provider] = record.RecordedAt
			}
			// FEAT-004 AC-28: route-attempt failures with dial-class errors
			// (host unreachable, not a stream-level glitch) are dispatchability
			// failures, not just non-fatal health risk. Promote them to
			// ProviderUnreachable so the engine hard-gates on the next call
			// without waiting for the snapshot's background refresh to catch
			// up. Non-dial failures (5xx mid-stream, 4xx, context canceled)
			// stay as soft demotion to preserve sticky-lease across single
			// transient hiccups.
			if isDispatchabilityFailure(record.Error) {
				if in.ProviderUnreachable == nil {
					in.ProviderUnreachable = make(map[string]time.Time)
				}
				existing, ok := in.ProviderUnreachable[record.Key.Provider]
				if !ok || record.RecordedAt.After(existing) {
					in.ProviderUnreachable[record.Key.Provider] = record.RecordedAt
				}
			}
		}
		if record.Key.Provider == "" && record.Key.Harness != "" {
			for i := range in.Harnesses {
				if in.Harnesses[i].Name == record.Key.Harness {
					in.Harnesses[i].InCooldown = true
				}
			}
		}
	}
}

func (s *service) routeAttemptTTL() time.Duration {
	if s.opts.ServiceConfig == nil {
		return defaultRouteAttemptCooldown
	}
	ttl := s.opts.ServiceConfig.HealthCooldown()
	if ttl <= 0 {
		return defaultRouteAttemptCooldown
	}
	return ttl
}

// buildRoutingInputs assembles routing.Inputs from the service's registry
// and snapshot-derived provider inventory. The public routing engine stays
// unchanged; only the source of provider/model candidates changes.
func (s *service) buildRoutingInputs(ctx context.Context) routing.Inputs {
	// FEAT-004 §Snapshot Freshness — synchronous-freshness contract: at route
	// decision time, refresh stale providers in-line so the engine sees
	// current reachability. Fresh providers short-circuit on cache. Caps the
	// per-route cost at ~5s × stale-provider-count (5s = discoveryRefresh
	// deadline per source) instead of forcing every source to re-probe.
	inputs, _ := s.buildRoutingInputsWithCatalog(ctx, serviceRoutingCatalog(), modelsnapshot.RefreshIfStale)
	return inputs
}

func (s *service) buildRoutingInputsWithCatalog(ctx context.Context, cat *modelcatalog.Catalog, refresh modelsnapshot.RefreshMode) (routing.Inputs, modelsnapshot.ModelSnapshot) {
	statuses := s.registry.Discover()
	statusByName := make(map[string]harnesses.HarnessStatus, len(statuses))
	for _, st := range statuses {
		statusByName[st.Name] = st
	}
	now := time.Now().UTC()
	var snapshot modelsnapshot.ModelSnapshot
	if s.opts.ServiceConfig != nil {
		if cacheRoot, err := serviceSnapshotCacheRoot(); err == nil {
			snapshot, _ = assembleModelSnapshotFromServiceConfigWithOptions(
				ctx,
				s.opts.ServiceConfig,
				cat,
				cacheRoot,
				modelsnapshot.AssembleOptions{Refresh: refresh},
			)
		}
	}

	var entries []routing.HarnessEntry
	for _, name := range s.registry.Names() {
		cfg, ok := s.registry.Get(name)
		if !ok {
			continue
		}
		st := statusByName[name]
		entry := routingHarnessEntryFromMetadata(name, cfg, st)
		if name == "fiz" && s.opts.ServiceConfig == nil {
			entry.AutoRoutingEligible = false
		}

		if qs, ok := subscriptionQuotaForHarness(name, time.Now()); ok {
			entry.QuotaOK = qs.OK
			entry.QuotaStale = qs.Present && !qs.Fresh
			entry.SubscriptionOK = qs.OK
			entry.QuotaPercentUsed = qs.PercentUsed
			entry.QuotaTrend = qs.Trend
			entry.QuotaReason = qs.Reason
		}

		// Native "fiz" harness: enumerate snapshot-derived provider rows.
		if name == "fiz" && s.opts.ServiceConfig != nil {
			entry.Providers = s.snapshotProviderEntries(ctx, cat, snapshot)
			// Tool support for the agent harness is per-(provider, model);
			// the harness-level baseline is whether ANY provider supports
			// tools. Engine OR-combines harness and provider SupportsTools
			// so this lets a per-model no_tools catalog flag actually fire
			// the RequiresTools gate when every provider's resolved model
			// is no-tools.
			if len(entry.Providers) > 0 {
				entry.SupportsTools = anyProviderSupportsTools(entry.Providers)
			} else {
				entry.Available = false
			}
		}
		s.applySubscriptionRoutingCost(&entry, cat)
		entries = append(entries, entry)
	}
	successRate, latencyMS := s.routeMetricSignals(now, s.routeAttemptTTL())

	// FEAT-004 AC-28: known-down endpoints are dispatchability failures.
	// Surface provider-level dial failures from the snapshot's discovery
	// sources via ProviderUnreachable so the routing engine hard-gates them
	// before any dispatch attempt. This is the synchronous-freshness
	// contract from FEAT-004 §Snapshot Freshness — autorouting reads the
	// snapshot, the snapshot owns reachability evidence, and stale-but-
	// failed providers don't get re-dialed until the cooldown TTL expires.
	healthCooldownTTL := s.routeAttemptTTL()
	providerUnreachable := providerCooldownsFromSnapshotErrors(snapshot, s.opts.ServiceConfig, now, healthCooldownTTL)

	return routing.Inputs{
		Harnesses:                    entries,
		ProviderSuccessRate:          successRate,
		ObservedLatencyMS:            latencyMS,
		ProviderQuotaExhaustedUntil:  s.providerQuotaExhaustedUntil(now),
		ProviderUnreachable:          providerUnreachable,
		CooldownDuration:             healthCooldownTTL,
		Now:                          now,
		ModelEligibility:             serviceRoutingModelEligibility(entries, cat),
		ReasoningResolver:            serviceRoutingReasoningResolver(cat),
		EndpointLoadResolver:         s.routeEndpointLoadsResolver(now),
		StickyServerInstanceResolver: s.routeStickyServerInstanceResolver(now),
	}, snapshot
}

// dispatchabilityFailureSubstrings matches errors that indicate the upstream
// host is unreachable or the provider is currently unusable: TCP-level dial
// failures, unrouted DNS, and HTTP 5xx gateway statuses (502/503/504/Bad
// Gateway). Treated as dispatchability failures per FEAT-004 AC-28. Both
// snapshot discovery errors and route-attempt record errors are classified
// through this same predicate.
//
// Excluded on purpose:
//   - 429 / rate-limit (different state machine, ProviderQuotaExhaustedUntil)
//   - "context canceled" (operator interrupt; not a provider signal)
//   - "provider request timeout: wall-clock" (could mean slow but live host)
//   - generic 4xx (auth, validation — surfaces via FilterReasonAuth elsewhere)
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
	" 502 ", // bare status code form (e.g. `: 502 Bad Gateway`)
	" 503 ",
	" 504 ",
}

// isSnapshotDialFailure preserved as a back-compat alias for the v0.13.0
// snapshot-side caller. Both now share the same broader predicate.
func isSnapshotDialFailure(errMsg string) bool { return isDispatchabilityFailure(errMsg) }

func isDispatchabilityFailure(errMsg string) bool {
	if errMsg == "" {
		return false
	}
	lower := strings.ToLower(errMsg)
	for _, pat := range dispatchabilityFailureSubstrings {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// providerCooldownsFromSnapshotErrors walks snapshot.Sources and returns a map
// of providerName → failure-time for any provider whose most recent discovery
// attempt failed with a dial-class error within the cooldown window. The map
// feeds routing.Inputs.ProviderCooldowns so engine.go can hard-gate the
// candidate before any dispatch attempt.
//
// Source names are produced by endpointSourceName: they start with the
// provider name (optionally followed by "-<endpoint>-<hash>" or "-props").
// We match by prefix against the configured provider name set so a source
// name like "rg-bragi-club-3090-props" correctly maps to provider
// "rg-bragi-club-3090".
func providerCooldownsFromSnapshotErrors(snapshot modelsnapshot.ModelSnapshot, cfg ServiceConfigSource, now time.Time, ttl time.Duration) map[string]time.Time {
	if len(snapshot.Sources) == 0 {
		return nil
	}
	providerNames := []string{}
	if cfg != nil {
		providerNames = cfg.ProviderNames()
	}
	if len(providerNames) == 0 {
		return nil
	}
	// Sort longest first so "rg-bragi-club-3090" wins over a hypothetical
	// "rg-bragi" prefix when both are configured.
	sort.SliceStable(providerNames, func(i, j int) bool {
		return len(providerNames[i]) > len(providerNames[j])
	})

	cooldowns := make(map[string]time.Time)
	for srcName, meta := range snapshot.Sources {
		if !isSnapshotDialFailure(meta.Error) {
			continue
		}
		// Use LastRefreshedAt when present (when the cache holds the failure
		// record itself); otherwise treat as "fresh enough" — the discovery
		// only emits an error when it tried recently.
		failedAt := meta.LastRefreshedAt
		if failedAt.IsZero() {
			failedAt = now
		}
		if ttl > 0 && now.Sub(failedAt) >= ttl {
			continue
		}
		for _, name := range providerNames {
			if name == srcName || strings.HasPrefix(srcName, name+"-") {
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

// ServiceConfigSource is the minimal interface providerCooldownsFromSnapshotErrors
// needs from the service config. The real service.opts.ServiceConfig satisfies
// it, and tests can pass a stub.
type ServiceConfigSource interface {
	ProviderNames() []string
}

func (s *service) snapshotProviderEntries(ctx context.Context, cat *modelcatalog.Catalog, snapshot modelsnapshot.ModelSnapshot) []routing.ProviderEntry {
	if s == nil || s.opts.ServiceConfig == nil {
		return nil
	}
	providerNames := s.opts.ServiceConfig.ProviderNames()
	if len(providerNames) == 0 || len(snapshot.Models) == 0 {
		return nil
	}
	grouped := make(map[snapshotProviderGroupKey][]modelsnapshot.KnownModel)
	for _, row := range snapshot.Models {
		harness := strings.TrimSpace(row.Harness)
		if harness != "" && harness != "fiz" {
			continue
		}
		providerName := strings.TrimSpace(row.Provider)
		if providerName == "" {
			continue
		}
		if _, ok := s.opts.ServiceConfig.Provider(providerName); !ok {
			continue
		}
		key := snapshotProviderGroupKey{
			Provider:        providerName,
			EndpointName:    strings.TrimSpace(row.EndpointName),
			EndpointBaseURL: strings.TrimSpace(row.EndpointBaseURL),
			ServerInstance:  strings.TrimSpace(row.ServerInstance),
		}
		grouped[key] = append(grouped[key], row)
	}
	if len(grouped) == 0 {
		return nil
	}

	groupCountByProvider := make(map[string]int)
	for key := range grouped {
		groupCountByProvider[key.Provider]++
	}

	keys := make([]snapshotProviderGroupKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Provider != keys[j].Provider {
			return keys[i].Provider < keys[j].Provider
		}
		if keys[i].EndpointName != keys[j].EndpointName {
			return keys[i].EndpointName < keys[j].EndpointName
		}
		if keys[i].EndpointBaseURL != keys[j].EndpointBaseURL {
			return keys[i].EndpointBaseURL < keys[j].EndpointBaseURL
		}
		return keys[i].ServerInstance < keys[j].ServerInstance
	})

	var entries []routing.ProviderEntry
	for _, key := range keys {
		pcfg, ok := s.opts.ServiceConfig.Provider(key.Provider)
		if !ok || pcfg.ConfigError != "" {
			continue
		}
		rows := append([]modelsnapshot.KnownModel(nil), grouped[key]...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].EndpointName != rows[j].EndpointName {
				return rows[i].EndpointName < rows[j].EndpointName
			}
			if rows[i].EndpointBaseURL != rows[j].EndpointBaseURL {
				return rows[i].EndpointBaseURL < rows[j].EndpointBaseURL
			}
			if rows[i].ServerInstance != rows[j].ServerInstance {
				return rows[i].ServerInstance < rows[j].ServerInstance
			}
			return rows[i].ID < rows[j].ID
		})
		discoveredIDs := snapshotModelIDs(rows)
		if defaultModel := strings.TrimSpace(pcfg.Model); defaultModel != "" {
			discoveredIDs = appendUniqueModelIDs(discoveredIDs, defaultModel)
		}
		ctxWindows, ctxSources := snapshotProviderContextWindows(ctx, pcfg, cat, rows, discoveredIDs)
		endpointName := snapshotEndpointName(pcfg, key)
		routeName := key.Provider
		if groupCountByProvider[key.Provider] > 1 {
			switch {
			case endpointName != "":
				routeName = endpointProviderRef(key.Provider, endpointName)
			case key.ServerInstance != "":
				routeName = endpointProviderRef(key.Provider, key.ServerInstance)
			case key.EndpointBaseURL != "":
				routeName = endpointProviderRef(key.Provider, key.EndpointBaseURL)
			}
		}
		baseURL := key.EndpointBaseURL
		if baseURL == "" {
			baseURL = pcfg.BaseURL
		}
		serverInstance := key.ServerInstance
		if serverInstance == "" {
			serverInstance = pcfg.ServerInstance
		}
		serverInstance = serverinstance.Normalize(baseURL, serverInstance)
		entry := routing.ProviderEntry{
			Name:                      routeName,
			BaseURL:                   baseURL,
			ServerInstance:            serverInstance,
			EndpointName:              endpointName,
			EndpointBaseURL:           baseURL,
			DefaultModel:              pcfg.Model,
			Billing:                   pcfg.Billing,
			CostClass:                 providerRoutingCostClass(pcfg.Type),
			DiscoveredIDs:             discoveredIDs,
			DiscoveryAttempted:        true,
			ContextWindows:            ctxWindows,
			ContextWindowSources:      ctxSources,
			ContextWindow:             pcfg.ContextWindow,
			ContextWindowSource:       contextWindowSourceForProviderConfig(pcfg),
			SupportsTools:             providerSupportsTools(cat, pcfg.Model, discoveredIDs),
			ExcludeFromDefaultRouting: pcfg.IncludeByDefaultSet && !pcfg.IncludeByDefault,
		}
		s.applyEndpointRoutingCost(&entry, pcfg, cat)
		entries = append(entries, entry)
	}
	return entries
}

type snapshotProviderGroupKey struct {
	Provider        string
	EndpointName    string
	EndpointBaseURL string
	ServerInstance  string
}

func snapshotEndpointName(pcfg ServiceProviderEntry, key snapshotProviderGroupKey) string {
	endpoints := modelDiscoveryEndpoints(pcfg)
	trimmedEndpointName := strings.TrimSpace(key.EndpointName)
	trimmedBaseURL := strings.TrimSpace(key.EndpointBaseURL)
	trimmedServerInstance := strings.TrimSpace(key.ServerInstance)
	if len(endpoints) == 0 {
		if trimmedEndpointName != "" {
			if strings.EqualFold(trimmedEndpointName, strings.TrimSpace(key.Provider)) {
				return "default"
			}
			return trimmedEndpointName
		}
		if trimmedServerInstance != "" {
			return trimmedServerInstance
		}
		if trimmedBaseURL != "" {
			return trimmedBaseURL
		}
		return ""
	}
	for _, endpoint := range endpoints {
		if trimmedEndpointName != "" && strings.EqualFold(endpoint.Name, trimmedEndpointName) {
			return endpoint.Name
		}
		if trimmedBaseURL != "" && strings.TrimSpace(endpoint.BaseURL) == trimmedBaseURL {
			return endpoint.Name
		}
		if trimmedServerInstance != "" && strings.TrimSpace(endpoint.ServerInstance) == trimmedServerInstance {
			return endpoint.Name
		}
	}
	if len(endpoints) == 1 {
		return endpoints[0].Name
	}
	if trimmedEndpointName != "" {
		return trimmedEndpointName
	}
	if trimmedServerInstance != "" {
		return trimmedServerInstance
	}
	if trimmedBaseURL != "" {
		return trimmedBaseURL
	}
	return ""
}

func snapshotModelIDs(rows []modelsnapshot.KnownModel) []string {
	if len(rows) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func snapshotProviderContextWindows(ctx context.Context, pcfg ServiceProviderEntry, cat *modelcatalog.Catalog, rows []modelsnapshot.KnownModel, discoveredIDs []string) (map[string]int, map[string]string) {
	_ = ctx
	out := make(map[string]int)
	sources := make(map[string]string)
	rowByID := make(map[string]modelsnapshot.KnownModel, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		if _, exists := rowByID[id]; !exists {
			rowByID[id] = row
		}
	}
	add := func(modelID string, snapshotWindow int) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return
		}
		window, source := snapshotContextWindow(pcfg, cat, modelID, snapshotWindow)
		if window <= 0 {
			return
		}
		out[modelID] = window
		sources[modelID] = source
	}
	if defaultModel := strings.TrimSpace(pcfg.Model); defaultModel != "" {
		row, ok := rowByID[defaultModel]
		if ok {
			add(defaultModel, row.ContextWindow)
		} else {
			add(defaultModel, 0)
		}
	}
	for _, id := range discoveredIDs {
		row, ok := rowByID[id]
		if ok {
			add(id, row.ContextWindow)
			continue
		}
		add(id, 0)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, sources
}

func snapshotContextWindow(pcfg ServiceProviderEntry, cat *modelcatalog.Catalog, modelID string, snapshotWindow int) (int, string) {
	if pcfg.ContextWindow > 0 {
		return pcfg.ContextWindow, ContextSourceProviderConfig
	}
	if snapshotWindow > 0 {
		return snapshotWindow, ContextSourceCatalog
	}
	if cat != nil {
		if n := cat.ContextWindowForModel(modelID); n > 0 {
			return n, ContextSourceCatalog
		}
	}
	return compaction.DefaultContextWindow, ContextSourceDefault
}

// providerQuotaExhaustedUntil snapshots the per-provider quota state machine
// at the given instant for the routing engine. Returns nil when no provider
// is currently in quota_exhausted state, which keeps the routing path
// allocation-free in the common case.
func (s *service) providerQuotaExhaustedUntil(now time.Time) map[string]time.Time {
	if s == nil || s.providerQuota == nil {
		return nil
	}
	return s.providerQuota.ExhaustedAt(now)
}

// startQuotaRecoveryProbeLoop spawns the goroutine that periodically probes
// quota_exhausted providers and either restores them to available or extends
// their retry_after with bounded backoff. The goroutine is tied to
// QuotaRefreshContext (or context.Background()) so server callers can cancel
// it on shutdown.
func (s *service) startQuotaRecoveryProbeLoop() {
	if s == nil || s.providerQuota == nil {
		return
	}
	ctx := s.opts.QuotaRefreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	probe := s.quotaRecoveryProber()
	if probe == nil {
		return
	}
	go runQuotaRecoveryProbeLoop(ctx, s.providerQuota, probe, defaultQuotaRecoveryFallbackInterval, nil, nil)
}

// quotaRecoveryProber returns the QuotaRecoveryProber used by the recovery
// loop. It looks up the provider entry in ServiceConfig and reuses the same
// probeProviderStatus the HealthCheck endpoint uses; a "connected" status
// counts as recovery, anything else is reported as a probe failure so the
// retry_after gets extended with backoff.
func (s *service) quotaRecoveryProber() QuotaRecoveryProber {
	sc := s.opts.ServiceConfig
	if sc == nil {
		return nil
	}
	return func(ctx context.Context, name string) error {
		entry, ok := sc.Provider(name)
		if !ok {
			return fmt.Errorf("provider %q not found", name)
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		probe := probeProviderStatus(probeCtx, entry, time.Now().UTC())
		if probe.status == "connected" {
			return nil
		}
		if probe.detail != "" {
			return fmt.Errorf("%s", probe.detail)
		}
		return fmt.Errorf("%s", probe.status)
	}
}

// ProviderQuotaState returns the per-provider quota state machine for this
// service. Callers (notably the quota-signal ingest path defined in sibling
// beads) drive transitions via MarkQuotaExhausted / MarkAvailable.
func (s *service) ProviderQuotaState() *ProviderQuotaStateStore {
	if s == nil {
		return nil
	}
	return s.providerQuota
}

func publicToRoutingExcludedRoutes(in []ExcludedRoute) []routing.ExcludedRoute {
	if len(in) == 0 {
		return nil
	}
	out := make([]routing.ExcludedRoute, len(in))
	for i, r := range in {
		out[i] = routing.ExcludedRoute{
			Provider: r.Provider,
			Model:    r.Model,
			Endpoint: r.Endpoint,
		}
	}
	return out
}

func serviceRoutingCatalog() *modelcatalog.Catalog {
	cat, err := loadRoutingCatalog()
	if err != nil || cat == nil {
		return nil
	}
	return cat
}

func routingPolicyForName(cat *modelcatalog.Catalog, name string) string {
	name = strings.TrimSpace(name)
	switch name {
	case "":
		return ""
	case "cheap", "default", "smart", "air-gapped":
		return name
	}
	if cat == nil {
		return name
	}
	_, policyName, ok := policyForName(cat, name)
	if !ok {
		return name
	}
	switch policyName {
	case "smart":
		return "smart"
	case "default":
		return "default"
	case "cheap":
		return "cheap"
	default:
		return policyName
	}
}

func serviceRoutingCatalogResolver(cat *modelcatalog.Catalog) func(ref, surface string) (string, bool) {
	if cat == nil {
		return nil
	}
	return func(ref, surface string) (string, bool) {
		catalogSurface, ok := serviceRoutingCatalogSurface(surface)
		if !ok {
			return "", false
		}
		resolved, err := cat.Resolve(ref, modelcatalog.ResolveOptions{
			Surface:         catalogSurface,
			AllowDeprecated: true,
		})
		if err != nil || resolved.ConcreteModel == "" {
			return "", false
		}
		return resolved.ConcreteModel, true
	}
}

func serviceRoutingCatalogCandidatesResolver(cat *modelcatalog.Catalog) func(ref, surface string) ([]string, bool) {
	if cat == nil {
		return nil
	}
	return func(ref, surface string) ([]string, bool) {
		catalogSurface, ok := serviceRoutingCatalogSurface(surface)
		if !ok {
			return nil, false
		}
		resolved, err := cat.Resolve(ref, modelcatalog.ResolveOptions{
			Surface:         catalogSurface,
			AllowDeprecated: true,
		})
		if err != nil || resolved.CanonicalID == "" {
			return nil, false
		}
		candidates := cat.CandidatesFor(catalogSurface, resolved.CanonicalID)
		if len(candidates) == 0 {
			if resolved.ConcreteModel == "" {
				return nil, false
			}
			return []string{resolved.ConcreteModel}, true
		}
		return candidates, true
	}
}

func serviceRoutingModelEligibility(entries []routing.HarnessEntry, cat *modelcatalog.Catalog) func(model string) (routing.ModelEligibility, bool) {
	if cat == nil {
		return nil
	}
	eligibility := make(map[string]routing.ModelEligibility)
	add := func(modelID string, includeByDefault bool, status string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return
		}
		view := modeleligibility.Resolve(modelID, includeByDefault, status, cat)
		known := routing.ModelEligibility{
			Power:        view.Power,
			ExactPinOnly: view.ExactPinOnly,
			AutoRoutable: view.AutoRoutable,
		}
		if existing, ok := eligibility[modelID]; ok {
			if known.Power > existing.Power {
				existing.Power = known.Power
			}
			existing.ExactPinOnly = existing.ExactPinOnly || known.ExactPinOnly
			existing.AutoRoutable = existing.AutoRoutable || known.AutoRoutable
			eligibility[modelID] = existing
			return
		}
		eligibility[modelID] = known
	}
	for _, h := range entries {
		status := "available"
		if !h.Available {
			status = "unreachable"
		}
		if h.DefaultModel != "" {
			add(h.DefaultModel, true, status)
		}
		for _, modelID := range h.SupportedModels {
			add(modelID, true, status)
		}
		for _, p := range h.Providers {
			includeByDefault := !p.ExcludeFromDefaultRouting
			add(p.DefaultModel, includeByDefault, status)
			for _, modelID := range p.DiscoveredIDs {
				add(modelID, includeByDefault, status)
			}
		}
	}
	if len(eligibility) == 0 {
		return nil
	}
	return func(model string) (routing.ModelEligibility, bool) {
		known, ok := eligibility[strings.TrimSpace(model)]
		return known, ok
	}
}

// serviceRoutingReasoningResolver returns the catalog's surface_policy
// reasoning_default for a (policy, surface) pair. Used by the routing engine
// to resolve Reasoning=auto to a concrete level before the capability gate.
func serviceRoutingReasoningResolver(cat *modelcatalog.Catalog) func(policy, surface string) (string, bool) {
	if cat == nil {
		return nil
	}
	return func(policy, surface string) (string, bool) {
		if policy == "" {
			return "", false
		}
		catalogSurface, ok := serviceRoutingCatalogSurface(surface)
		if !ok {
			return "", false
		}
		resolved, err := cat.Resolve(policy, modelcatalog.ResolveOptions{
			Surface:         catalogSurface,
			AllowDeprecated: true,
		})
		if err != nil {
			return "", false
		}
		def := string(resolved.SurfacePolicy.ReasoningDefault)
		if def == "" {
			return "", false
		}
		return def, true
	}
}

func serviceRoutingCatalogSurface(surface string) (modelcatalog.Surface, bool) {
	switch surface {
	case "embedded-openai":
		return modelcatalog.SurfaceAgentOpenAI, true
	case "embedded-anthropic":
		return modelcatalog.SurfaceAgentAnthropic, true
	case "codex":
		return modelcatalog.SurfaceCodex, true
	case "claude":
		return modelcatalog.SurfaceClaudeCode, true
	case "gemini":
		return modelcatalog.SurfaceGemini, true
	default:
		return "", false
	}
}

// buildProviderContextWindows assembles the ContextWindows map for a
// ProviderEntry from the model catalog. Entries are added for the provider's
// configured DefaultModel and every DiscoveredID that has a non-zero
// context_window declared in the catalog. Models the catalog does not know
// about are omitted (engine treats missing entries as unknown context).
func buildProviderContextWindows(ctx context.Context, pcfg ServiceProviderEntry, cat *modelcatalog.Catalog, discoveredIDs []string) (map[string]int, map[string]string) {
	out := make(map[string]int)
	sources := make(map[string]string)
	if defaultModel := strings.TrimSpace(pcfg.Model); defaultModel != "" {
		if length, source := resolveContextEvidence(ctx, pcfg, defaultModel, cat); length > 0 {
			out[defaultModel] = length
			sources[defaultModel] = source
		}
	}
	for _, id := range discoveredIDs {
		if id == "" {
			continue
		}
		if _, exists := out[id]; exists {
			continue
		}
		if length, source := resolveContextEvidence(ctx, pcfg, id, cat); length > 0 {
			out[id] = length
			sources[id] = source
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, sources
}

func contextWindowSourceForProviderConfig(pcfg ServiceProviderEntry) string {
	if pcfg.ContextWindow > 0 {
		return ContextSourceProviderConfig
	}
	return ""
}

// providerSupportsTools returns whether the provider should be advertised as
// supporting tools to the routing engine. Defaults to true; only flips to
// false when the catalog explicitly marks every relevant model (the
// DefaultModel and any DiscoveredIDs) with no_tools=true.
func providerSupportsTools(cat *modelcatalog.Catalog, defaultModel string, discoveredIDs []string) bool {
	if cat == nil {
		return true
	}
	checked := false
	if defaultModel != "" {
		if cat.SupportsToolsForModel(defaultModel) {
			return true
		}
		checked = true
	}
	for _, id := range discoveredIDs {
		if id == "" {
			continue
		}
		if cat.SupportsToolsForModel(id) {
			return true
		}
		checked = true
	}
	if !checked {
		return true
	}
	return false
}

func anyProviderSupportsTools(providers []routing.ProviderEntry) bool {
	for _, p := range providers {
		if p.SupportsTools {
			return true
		}
	}
	return false
}

func providerUsesLiveDiscovery(providerType string) bool {
	switch normalizeServiceProviderType(providerType) {
	case "openai", "openrouter", "lmstudio", "llama-server", "ds4", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm", "minimax", "qwen", "zai":
		return true
	default:
		return false
	}
}

func (s *service) applyEndpointRoutingCost(entry *routing.ProviderEntry, pcfg ServiceProviderEntry, cat *modelcatalog.Catalog) {
	if entry == nil {
		return
	}
	if providerTypeUsesFixedBilling(pcfg.Type) {
		entry.ActualCashSpend = false
		if s.opts.LocalCostUSDPer1kTokens > 0 {
			entry.CostUSDPer1kTokens = s.opts.LocalCostUSDPer1kTokens
			entry.CostSource = routing.CostSourceUserConfig
		} else {
			entry.CostUSDPer1kTokens = 0
			entry.CostSource = routing.CostSourceUnknown
		}
		return
	}
	if cost, ok := catalogCostUSDPer1kTokens(cat, entry.DefaultModel); ok {
		entry.ActualCashSpend = true
		entry.CostUSDPer1kTokens = cost
		entry.CostSource = routing.CostSourceCatalog
		return
	}
	entry.ActualCashSpend = true
	entry.CostUSDPer1kTokens = 0
	entry.CostSource = routing.CostSourceUnknown
}

func (s *service) applySubscriptionRoutingCost(entry *routing.HarnessEntry, cat *modelcatalog.Catalog) {
	if !routingHarnessUsesAccountBilling(entry) {
		return
	}
	baseCost, ok := catalogCostUSDPer1kTokens(cat, entry.DefaultModel)
	if !ok {
		baseCost, ok = catalogCostUSDPer1kTokens(cat, subscriptionFallbackPolicy(entry.Name))
		if !ok {
			baseCost = 0
		}
	}
	cost := baseCost
	entry.Providers = []routing.ProviderEntry{{
		Billing:            modelcatalog.BillingModelSubscription,
		CostUSDPer1kTokens: cost,
		CostSource:         routing.CostSourceSubscription,
		ActualCashSpend:    false,
	}}
}

func providerRoutingCostClass(providerType string) string {
	if providerTypeUsesFixedBilling(providerType) {
		return "local"
	}
	return "medium"
}

func subscriptionFallbackPolicy(harnessName string) string {
	switch harnessName {
	case "claude", "codex", "gemini":
		return "default"
	default:
		return ""
	}
}

func catalogCostUSDPer1kTokens(cat *modelcatalog.Catalog, modelID string) (float64, bool) {
	if cat == nil || strings.TrimSpace(modelID) == "" {
		return 0, false
	}
	entry, ok := cat.LookupModel(modelID)
	if !ok {
		resolved := resolveCatalogCostModel(cat, modelID)
		if resolved == "" {
			return 0, false
		}
		entry, ok = cat.LookupModel(resolved)
		if !ok {
			return 0, false
		}
	}
	input := entry.CostInputPerM
	if input == 0 {
		input = entry.CostInputPerMTok
	}
	output := entry.CostOutputPerM
	if output == 0 {
		output = entry.CostOutputPerMTok
	}
	switch {
	case input > 0 && output > 0:
		return ((input + output) / 2) / 1000, true
	case input > 0:
		return input / 1000, true
	case output > 0:
		return output / 1000, true
	default:
		return 0, false
	}
}

func resolveCatalogCostModel(cat *modelcatalog.Catalog, ref string) string {
	for _, surface := range []modelcatalog.Surface{
		modelcatalog.SurfaceAgentOpenAI,
		modelcatalog.SurfaceAgentAnthropic,
		modelcatalog.SurfaceCodex,
		modelcatalog.SurfaceClaudeCode,
		modelcatalog.SurfaceGemini,
	} {
		resolved, err := cat.Resolve(ref, modelcatalog.ResolveOptions{
			Surface:         surface,
			AllowDeprecated: true,
		})
		if err == nil && resolved.ConcreteModel != "" {
			return resolved.ConcreteModel
		}
	}
	return ""
}

func (s *service) subscriptionCostCurve() SubscriptionCostCurve {
	if s.opts.SubscriptionCostCurve == nil {
		return defaultSubscriptionCostCurve()
	}
	curve := *s.opts.SubscriptionCostCurve
	def := defaultSubscriptionCostCurve()
	if curve.FreeUntilPercent == 0 {
		curve.FreeUntilPercent = def.FreeUntilPercent
	}
	if curve.LowUntilPercent == 0 {
		curve.LowUntilPercent = def.LowUntilPercent
	}
	if curve.MediumUntilPercent == 0 {
		curve.MediumUntilPercent = def.MediumUntilPercent
	}
	if curve.LowMultiplier == 0 {
		curve.LowMultiplier = def.LowMultiplier
	}
	if curve.MediumMultiplier == 0 {
		curve.MediumMultiplier = def.MediumMultiplier
	}
	if curve.HighMultiplier == 0 {
		curve.HighMultiplier = def.HighMultiplier
	}
	return curve
}

func defaultSubscriptionCostCurve() SubscriptionCostCurve {
	return SubscriptionCostCurve{
		FreeUntilPercent:   70,
		LowUntilPercent:    80,
		MediumUntilPercent: 90,
		LowMultiplier:      0.1,
		MediumMultiplier:   0.3,
		HighMultiplier:     1.2,
	}
}

func subscriptionEffectiveCostUSDPer1kTokens(baseCost float64, quotaPercentUsed int, curve SubscriptionCostCurve) float64 {
	switch {
	case quotaPercentUsed <= curve.FreeUntilPercent:
		return 0
	case quotaPercentUsed <= curve.LowUntilPercent:
		return baseCost * curve.LowMultiplier
	case quotaPercentUsed <= curve.MediumUntilPercent:
		return baseCost * curve.MediumMultiplier
	default:
		return baseCost * curve.HighMultiplier
	}
}

// resolveExecuteRouteWithEngine is the post-engine variant of resolveExecuteRoute.
// It is invoked by Execute when the request is under-specified
// (no PreResolved, no fully-specified Harness). Returns nil when the request
// is already specific enough that the legacy resolveExecuteRoute path applies.
func (s *service) resolveExecuteRouteWithEngine(req ServiceExecuteRequest) (*RouteDecision, error) {
	rr := RouteRequest{
		Policy:        req.Policy,
		Model:         req.Model,
		Provider:      req.Provider,
		Harness:       req.Harness,
		Reasoning:     req.Reasoning,
		Permissions:   req.Permissions,
		CachePolicy:   req.CachePolicy,
		MinPower:      req.MinPower,
		MaxPower:      req.MaxPower,
		Role:          req.Role,
		CorrelationID: req.CorrelationID,
	}
	dec, err := s.ResolveRoute(context.Background(), rr)
	if err != nil {
		if isExplicitPinError(err) {
			return nil, err
		}
		return nil, fmt.Errorf("ResolveRoute: %w", err)
	}
	return dec, nil
}

func providerPreferenceForPolicy(cat *modelcatalog.Catalog, policy string) (string, error) {
	if policy == "" {
		return routing.ProviderPreferenceLocalFirst, nil
	}
	switch policy {
	case "code-medium":
		return "", fmt.Errorf("policy %q is deprecated; use --policy default or --min-power/--max-power", policy)
	case "code-high":
		return "", fmt.Errorf("policy %q is deprecated; use --policy smart or --min-power/--max-power", policy)
	}
	if cat == nil {
		return "", &ErrUnknownPolicy{Policy: policy}
	}
	if _, _, ok := policyForName(cat, policy); !ok {
		return "", &ErrUnknownPolicy{Policy: policy}
	}
	preference := providerPreferenceForPolicyName(policy)
	switch preference {
	case routing.ProviderPreferenceLocalOnly, routing.ProviderPreferenceSubscriptionOnly,
		routing.ProviderPreferenceLocalFirst, routing.ProviderPreferenceSubscriptionFirst:
		return preference, nil
	default:
		return "", fmt.Errorf("policy %q has unsupported provider preference %q", policy, preference)
	}
}

func routePowerPolicyForRequest(cat *modelcatalog.Catalog, req RouteRequest) RoutePowerPolicy {
	policy := RoutePowerPolicy{
		PolicyName: req.Policy,
		MinPower:   req.MinPower,
		MaxPower:   req.MaxPower,
	}
	if req.Policy == "" || cat == nil {
		return policy
	}
	policyEntry, policyName, ok := policyForName(cat, req.Policy)
	if !ok {
		return policy
	}
	policy.PolicyName = policyName
	if policyEntry.MinPower > 0 {
		if policy.MinPower == 0 || policyEntry.MinPower > policy.MinPower {
			policy.MinPower = policyEntry.MinPower
		}
	}
	if policyEntry.MaxPower > 0 {
		if policy.MaxPower == 0 || policyEntry.MaxPower < policy.MaxPower {
			policy.MaxPower = policyEntry.MaxPower
		}
	}
	return policy
}

func routePowerBoundsForRequest(req RouteRequest, policy RoutePowerPolicy) (int, int) {
	// Exact model pins remain exact model identity pins. The policy still
	// reports its effective power policy for evidence, but it does not widen
	// or override the caller's model.
	if req.Model != "" {
		return req.MinPower, req.MaxPower
	}
	return policy.MinPower, policy.MaxPower
}
