package fizeau

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/provider/utilization"
	quotaimpl "github.com/easel/fizeau/internal/quota"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serverinstance"
	"github.com/easel/fizeau/internal/serviceimpl"
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
	in, snapshot := s.routingInputs(ctx, cat, modelsnapshot.RefreshBackground)

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
		s.annotateOpenrouterCreditFreshness(result)
		return result, publicRoutingError(err, result.Candidates, req.Policy)
	}
	if result != nil && s != nil && s.routeSticky != nil {
		sticky := s.routeSticky.ApplyStickyLease(time.Now().UTC(), routehealth.StickyRequest{
			StickyKey:      req.CorrelationID,
			Harness:        result.Harness,
			Provider:       result.Provider,
			Endpoint:       result.Endpoint,
			ServerInstance: result.ServerInstance,
			Model:          result.Model,
		})
		result.Sticky = RouteStickyState{
			KeyPresent:     sticky.KeyPresent,
			Assignment:     sticky.Assignment,
			ServerInstance: sticky.ServerInstance,
			Reason:         sticky.Reason,
			Bonus:          sticky.Bonus,
		}
	}
	if result != nil && result.Endpoint == "" {
		_, endpoint, _ := splitEndpointProviderRef(result.Provider)
		result.Endpoint = endpoint
	}
	s.annotateRouteDecisionSnapshotEvidence(result, snapshot)
	s.annotateRouteDecisionEvidence(result)
	s.annotateOpenrouterCreditFreshness(result)
	// Cache the decision so RouteStatus can surface LastDecision.
	if result != nil {
		result.RequestedPolicy = req.Policy
		result.PowerPolicy = powerPolicy
		result.Model = resolveSubprocessModelAliasWithCatalog(result.Harness, result.Model, cat)
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
	powerHintFit := scorePowerHintFit(candidate, powerPolicy)
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
		ScoreComponents:     copyScoreComponents(candidate.ScoreComponents),
	}
}

func copyScoreComponents(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func scorePowerHintFit(candidate routing.Candidate, policy RoutePowerPolicy) float64 {
	power := candidate.Power
	if power <= 0 {
		return 0
	}
	if policy.MaxPower > 0 && candidate.FilterReason == routing.FilterReasonAboveMaxPower && candidate.ScoreComponents != nil {
		return candidate.ScoreComponents["power"]
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
		s.annotateProbeEvidence(&decision.Candidates[i])
	}
}

// annotateProbeEvidence populates LastProbeAt and LastProbeSuccess on a
// RouteCandidate from the service's probe store when a record is available.
func (s *service) annotateProbeEvidence(c *RouteCandidate) {
	if s == nil || s.providerProbe == nil || c == nil {
		return
	}
	provider := strings.TrimSpace(c.Provider)
	if provider == "" {
		return
	}
	endpoint := strings.TrimSpace(c.Endpoint)
	if base, ep, ok := splitEndpointProviderRef(provider); ok {
		provider = base
		if endpoint == "" {
			endpoint = ep
		}
	}
	if r, ok := s.providerProbe.LastProbe(provider, endpoint); ok {
		c.LastProbeAt = r.LastProbeAt
		c.LastProbeSuccess = r.LastProbeSuccess
		return
	}
	if endpoint != "" {
		if r, ok := s.providerProbe.LastProbe(provider, ""); ok {
			c.LastProbeAt = r.LastProbeAt
			c.LastProbeSuccess = r.LastProbeSuccess
		}
	}
}

func (s *service) routeUtilizationEvidence(provider, serverInstance, endpoint, model string) RouteUtilizationState {
	if s == nil || s.routeSticky == nil {
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
	sample, ok := s.routeSticky.UtilizationSample(keyProvider, keyServerInstance, model)
	if !ok && keyEndpoint != "" && keyEndpoint != keyServerInstance {
		sample, ok = s.routeSticky.UtilizationSample(keyProvider, keyEndpoint, model)
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
	return routehealth.FilterReason(c)
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
	return routehealth.EscalatePolicyLadder(req, in, origErr, displayPolicy, shouldEscalateOnError)
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
	return routehealth.ShouldEscalateOnError(err)
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
	routehealth.ApplyAttemptCooldowns(in, records, ttl)
}

func (s *service) routeAttemptTTL() time.Duration {
	var ttl time.Duration
	if s.opts.ServiceConfig == nil {
		return routehealth.CooldownTTL(ttl)
	}
	ttl = s.opts.ServiceConfig.HealthCooldown()
	return routehealth.CooldownTTL(ttl)
}

// buildRoutingInputs assembles routing.Inputs from the service's registry
// and snapshot-derived provider inventory. The public routing engine stays
// unchanged; only the source of provider/model candidates changes.
func (s *service) buildRoutingInputs(ctx context.Context) routing.Inputs {
	// Route hot paths are cache-first: stale or missing provider facts may
	// request a coordinated background refresh, but routing never blocks on
	// local provider probes or model discovery before scoring candidates.
	inputs, _ := s.routingInputs(ctx, serviceRoutingCatalog(), modelsnapshot.RefreshBackground)
	return inputs
}

// routingInputs gathers service-owned live state and delegates API-neutral
// snapshot, catalog, eligibility, and cost projection to internal/serviceimpl.
func (s *service) routingInputs(ctx context.Context, cat *modelcatalog.Catalog, refresh modelsnapshot.RefreshMode) (routing.Inputs, modelsnapshot.ModelSnapshot) {
	if refresh == modelsnapshot.RefreshBackground {
		s.requestLocalHealthRefreshForRouting(ctx)
	}
	statuses := s.registry.Discover()
	statusByName := make(map[string]harnesses.HarnessStatus, len(statuses))
	for _, st := range statuses {
		statusByName[st.Name] = st
	}
	now := time.Now().UTC()
	var snapshot modelsnapshot.ModelSnapshot
	var providerNames []string
	var providers map[string]serviceimpl.ProviderEntry
	if s.opts.ServiceConfig != nil {
		providerNames = s.opts.ServiceConfig.ProviderNames()
		providers = make(map[string]serviceimpl.ProviderEntry, len(providerNames))
		for _, name := range providerNames {
			if entry, ok := s.opts.ServiceConfig.Provider(name); ok {
				providers[name] = serviceImplProviderEntry(entry)
			}
		}
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

		if qs, ok := quotaimpl.SubscriptionForHarness(name, time.Now()); ok {
			entry.QuotaOK = qs.OK
			entry.QuotaStale = qs.Present && !qs.Fresh
			// Fail-open contract: a subscription harness is hard-gated ONLY on
			// PROVEN exhaustion (qs.Exhausted). Unknown/stale/unavailable quota
			// keeps it eligible — QuotaOK=false still demotes it in score.go, so
			// a healthy harness is preferred, but an unconfirmed-quota harness is
			// never fabricated into "no viable provider". (Was: =qs.OK, which
			// collapsed Unknown into the same hard gate as Blocked.)
			entry.SubscriptionOK = !qs.Exhausted
			entry.QuotaPercentUsed = qs.PercentUsed
			entry.QuotaTrend = qs.Trend
			entry.QuotaReason = qs.Reason
		}

		entries = append(entries, entry)
	}
	successRate, latencyMS := s.routeMetricSignals(now, s.routeAttemptTTL())

	// FEAT-004 AC-28: known-down endpoints are dispatchability failures.
	// Surface provider-level dial failures from cached snapshot discovery
	// sources via ProviderUnreachable so the routing engine hard-gates known
	// failures before any dispatch attempt.
	healthCooldownTTL := s.routeAttemptTTL()
	providerUnreachable := providerCooldownsFromSnapshotErrors(snapshot, s.opts.ServiceConfig, now, healthCooldownTTL)

	// Proactive probe failures feed ProbeUnreachable (separate from
	// ProviderUnreachable which is populated from dial failures). TTL is
	// HealthSignalTTL (default 10 min) — longer than the cooldown window.
	var probeUnreachable map[string]time.Time
	var probeUnknown map[string]time.Time
	if s.providerProbe != nil {
		probeUnreachable = s.probeUnreachableProviders(now)
		probeUnknown = s.probeUnknownProviders(now)
	}

	// Credential gate (synchronous, network-free): providers that need an API
	// key but lack one are filtered out before any HTTP I/O can occur, so the
	// operator sees the missing-credential root cause instead of a 401 from
	// the dispatch path.
	providerCredentialMissing := providerCredentialMissingMap(s.opts.ServiceConfig)

	// Credit-balance gate plus failure-mode classification: the credit probe
	// produces credit_exhausted, credential_invalid, and provider_unreachable
	// evidence in one cache pass. The probe is cached per-provider with a TTL
	// so back-to-back Execute calls within the window share one
	// /api/v1/credits round-trip.
	probeMaps := s.openrouterProbeMaps(ctx, now)

	var endpointLoadResolver func(provider, endpoint, model string) (routing.EndpointLoad, bool)
	var stickyServerInstanceResolver func(stickyKey string) (string, bool)
	if s != nil && s.routeSticky != nil {
		endpointLoadResolver = s.routeSticky.EndpointLoadResolver(now)
		stickyServerInstanceResolver = s.routeSticky.StickyServerInstanceResolver(now)
	}

	inputs := serviceimpl.BuildRoutingInputs(serviceimpl.RoutingInputsInput{
		Base: routing.Inputs{
			ProviderSuccessRate:          successRate,
			ObservedLatencyMS:            latencyMS,
			ProviderQuotaExhaustedUntil:  s.providerQuotaExhaustedUntil(now),
			ProviderUnreachable:          providerUnreachable,
			ProbeUnreachable:             probeUnreachable,
			ProbeUnknown:                 probeUnknown,
			ProviderCredentialMissing:    providerCredentialMissing,
			ProviderCreditExhausted:      probeMaps.CreditExhausted,
			ProviderCredentialInvalid:    probeMaps.CredentialInvalid,
			ProviderProbeUnreachable:     probeMaps.ProviderUnreachable,
			CooldownDuration:             healthCooldownTTL,
			Now:                          now,
			SurfacePreference:            routingSurfacePreference(),
			EndpointLoadResolver:         endpointLoadResolver,
			StickyServerInstanceResolver: stickyServerInstanceResolver,
		},
		Harnesses:               entries,
		Providers:               providers,
		ProviderNames:           providerNames,
		HasServiceConfig:        s.opts.ServiceConfig != nil,
		Snapshot:                snapshot,
		Catalog:                 cat,
		LocalCostUSDPer1kTokens: s.opts.LocalCostUSDPer1kTokens,
	})
	return inputs, snapshot
}

// routingSurfacePreference returns the surface→harness preference that makes
// claude-tui the DEFAULT for the shared "claude" surface. It is config-gated
// and revertible via the FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT kill-switch env var:
// when that is set to a truthy value the preference is disabled (returns an
// explicit empty map) and routing falls back to the alphabetical tie-break
// (claude --print). Unset (the default) returns the built-in preference.
func routingSurfacePreference() map[string]string {
	if v := strings.TrimSpace(os.Getenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return map[string]string{} // explicit empty = preference disabled
		}
	}
	return routing.DefaultSurfacePreference()
}

// providerCredentialMissingMap inspects each configured provider that
// authenticates with an API key and reports the ones whose credential is
// missing or obviously malformed. The map's value is the credential location
// (env var / config field) that was inspected, so operator-facing evidence
// can show WHICH key location was checked.
//
// The check is synchronous and side-effect free: it never issues an HTTP
// request. Server-side credential rejection (401 after dispatch) is a
// separate failure mode reserved for a future filter reason.
func providerCredentialMissingMap(cfg ServiceConfigSource) map[string]string {
	if cfg == nil {
		return nil
	}
	names := cfg.ProviderNames()
	if len(names) == 0 {
		return nil
	}
	provider, ok := cfg.(interface {
		Provider(name string) (ServiceProviderEntry, bool)
	})
	if !ok {
		return nil
	}
	out := make(map[string]string)
	for _, name := range names {
		pcfg, ok := provider.Provider(name)
		if !ok {
			continue
		}
		location, missing := credentialMissingForProvider(name, pcfg)
		if missing {
			out[name] = location
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// credentialMissingForProvider returns (location, true) when the named
// provider needs an API key but the configured value is empty or obviously
// malformed. location names the field the operator should set.
//
// The malformed heuristic is intentionally narrow: it catches blatant typos
// (wrong prefix or length far below any plausible key) without ever calling
// the provider. Server-side validity is out of scope here — the rejection
// path will materialize the credential_invalid reason separately.
func credentialMissingForProvider(name string, pcfg ServiceProviderEntry) (string, bool) {
	if normalizeServiceProviderType(pcfg.Type) != "openrouter" {
		return "", false
	}
	key := strings.TrimSpace(pcfg.APIKey)
	location := fmt.Sprintf("providers.%s.api_key (or OPENROUTER_API_KEY env)", name)
	if key == "" {
		return location, true
	}
	// Unexpanded env placeholder: config load preserves the literal
	// "${VAR}" verbatim when VAR is unset (internal/config/config_test.go
	// TestLoad_EnvExpansion_Unset). This catches both the bare placeholder
	// and partial substitutions like "sk-or-${KEY_SUFFIX}" that would
	// otherwise sneak past the prefix/length heuristic.
	if strings.Contains(key, "${") {
		return location, true
	}
	if !openrouterAPIKeyWellFormed(key) {
		return location, true
	}
	return "", false
}

// openrouterAPIKeyWellFormed reports whether s plausibly resembles a real
// OpenRouter API key. The heuristic checks the well-known "sk-or-" prefix and
// a minimum length floor that excludes obvious typos. It does NOT confirm
// server-side validity.
func openrouterAPIKeyWellFormed(s string) bool {
	const (
		expectedPrefix  = "sk-or-"
		minPlausibleLen = 20
	)
	if !strings.HasPrefix(s, expectedPrefix) {
		return false
	}
	if len(s) < minPlausibleLen {
		return false
	}
	return true
}

// isSnapshotDialFailure preserved as a back-compat alias for the v0.13.0
// snapshot-side caller. Both now share the same broader predicate.
func isSnapshotDialFailure(errMsg string) bool { return routehealth.IsDispatchabilityFailure(errMsg) }

func isDispatchabilityFailure(errMsg string) bool {
	return routehealth.IsDispatchabilityFailure(errMsg)
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
	sources := make([]routehealth.SnapshotSource, 0, len(snapshot.Sources))
	for name, meta := range snapshot.Sources {
		sources = append(sources, routehealth.SnapshotSource{
			Name:            name,
			Error:           meta.Error,
			LastRefreshedAt: meta.LastRefreshedAt,
		})
	}
	return routehealth.ProviderCooldownsFromSnapshotErrors(sources, providerNames, now, ttl)
}

// ServiceConfigSource is the minimal interface providerCooldownsFromSnapshotErrors
// needs from the service config. The real service.opts.ServiceConfig satisfies
// it, and tests can pass a stub.
type ServiceConfigSource interface {
	ProviderNames() []string
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

// startQuotaRecoveryProbeLoop delegates recovery scheduling to internal/quota.
// Root retains only the ServiceConfig-backed probe construction. The goroutine
// is tied to QuotaRefreshContext (or context.Background()) so server callers
// can cancel it on shutdown.
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
	go quotaimpl.RunRecoveryLoop(ctx, s.providerQuota.innerStore(), probe, quotaimpl.RecoveryOptions{})
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
		probe := serviceimpl.ProbeProviderStatus(probeCtx, serviceImplProviderEntry(entry), time.Now().UTC(), nil)
		if probe.Status == "connected" {
			return nil
		}
		if probe.Detail != "" {
			return fmt.Errorf("%s", probe.Detail)
		}
		return fmt.Errorf("%s", probe.Status)
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

func providerUsesLiveDiscovery(providerType string) bool {
	switch normalizeServiceProviderType(providerType) {
	case "openai", "openrouter", "lmstudio", "llama-server", "ds4", "omlx", "rapid-mlx", "ollama", "lucebox", "vllm", "minimax", "qwen", "zai":
		return true
	default:
		return false
	}
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
	internal := routehealth.EffectivePowerPolicy(routehealth.PowerRequest{
		Policy:   req.Policy,
		Model:    req.Model,
		MinPower: req.MinPower,
		MaxPower: req.MaxPower,
	}, func(name string) (routehealth.PolicySpec, bool) {
		if cat == nil {
			return routehealth.PolicySpec{}, false
		}
		policy, policyName, ok := policyForName(cat, name)
		if !ok {
			return routehealth.PolicySpec{}, false
		}
		return routehealth.PolicySpec{
			Name:     policyName,
			MinPower: policy.MinPower,
			MaxPower: policy.MaxPower,
		}, true
	})
	return RoutePowerPolicy{
		PolicyName: internal.PolicyName,
		MinPower:   internal.MinPower,
		MaxPower:   internal.MaxPower,
	}
}

func routePowerBoundsForRequest(req RouteRequest, policy RoutePowerPolicy) (int, int) {
	return routehealth.PowerBoundsForRequest(routehealth.PowerRequest{
		Policy:   req.Policy,
		Model:    req.Model,
		MinPower: req.MinPower,
		MaxPower: req.MaxPower,
	}, routehealth.PowerPolicy{
		PolicyName: policy.PolicyName,
		MinPower:   policy.MinPower,
		MaxPower:   policy.MaxPower,
	})
}
