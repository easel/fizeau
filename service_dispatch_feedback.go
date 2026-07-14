package fizeau

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
	serviceimpl "github.com/easel/fizeau/internal/serviceimpl"
)

// RecordRouteAttempt preserves the public service facade while the internal
// route-health store owns normalization, matching, and reliability state.
func (s *service) RecordRouteAttempt(_ context.Context, attempt RouteAttempt) error {
	if s == nil {
		s = &service{}
	}
	if err := s.routeHealthStore().RecordAttemptWithOptions(
		internalRouteAttempt(attempt),
		routehealth.RecordOptions{ExactSuccessClear: true},
	); err != nil {
		return err
	}
	return s.persistRouteHealthSnapshot()
}

// recordRouteAttemptFromFinal admits legacy/native final evidence. Older
// native paths may lack an explicit failure class, so the internal converter
// retains its bounded diagnostic-text fallback for this mode only.
func (s *service) recordRouteAttemptFromFinal(final harnesses.FinalData) {
	_ = s.observeFinalAttempt(final, routehealth.FinalEvidenceAllowLegacyText)
}

// observeRouteAttemptFromFinal admits wrapped-harness evidence only when the
// adapter supplied an authoritative typed failure class. Persistence errors
// are returned to the subprocess finalizer as bounded terminal warnings.
func (s *service) observeRouteAttemptFromFinal(final harnesses.FinalData) error {
	return s.observeFinalAttempt(final, routehealth.FinalEvidenceTypedOnly)
}

// observeFinalAttempt preserves the observable update order:
//
//  1. update in-memory route health;
//  2. capture any persistence error;
//  3. feed independently-confirmed reachability failures to catalog/probes;
//  4. return the captured persistence error.
//
// The last two stages deliberately do not short-circuit one another. A broken
// persistence path must not make the next request replay a known endpoint
// failure, and a feedback failure must not erase the durable-write result.
func (s *service) observeFinalAttempt(final harnesses.FinalData, mode routehealth.FinalEvidenceMode) error {
	attempt, ok := routehealth.AttemptFromFinal(final, mode)
	if !ok {
		return nil
	}
	if err := s.routeHealthStore().RecordAttemptWithOptions(
		attempt,
		routehealth.RecordOptions{ExactSuccessClear: true},
	); err != nil {
		return err
	}
	persistErr := s.persistRouteHealthSnapshot()
	if provider, endpoint, dispatchErr := dispatchFailureFromAttempt(attempt); dispatchErr != nil {
		s.recordDispatchFailure(provider, endpoint, dispatchErr)
	}
	return persistErr
}

func internalRouteAttempt(attempt RouteAttempt) routehealth.Attempt {
	return routehealth.Attempt{
		Harness:        attempt.Harness,
		Provider:       attempt.Provider,
		Model:          attempt.Model,
		Endpoint:       attempt.Endpoint,
		ServerInstance: attempt.ServerInstance,
		Status:         attempt.Status,
		Reason:         attempt.Reason,
		Error:          attempt.Error,
		Duration:       attempt.Duration,
		Timestamp:      attempt.Timestamp,
	}
}

func (s *service) routeHealthStore() *routehealth.Store {
	if s.routeHealth == nil {
		s.routeHealth = routehealth.NewStore()
	}
	return s.routeHealth
}

func (s *service) activeRouteAttempts(now time.Time, ttl time.Duration) []routehealth.Record {
	if s == nil || s.routeHealth == nil {
		return nil
	}
	return s.routeHealth.ActiveAttempts(now, ttl)
}

func (s *service) routeMetricSignals(now time.Time, ttl time.Duration) (map[string]float64, map[string]float64) {
	if s == nil || s.routeHealth == nil {
		return nil, nil
	}
	return s.routeHealth.MetricSignals(now, ttl)
}

func (s *service) persistRouteHealthSnapshot() error {
	if s == nil || s.opts.PersistRouteHealth == "" {
		return nil
	}
	return routehealth.SavePersistedState(s.opts.PersistRouteHealth, s.routeHealth, s.providerProbe)
}

// recordDispatchFailure feeds a chat-completions dispatch failure back into
// both the catalog cache and the routehealth probe store so the next routing
// pass treats the endpoint as unreachable instead of replaying the timeout.
//
// The catalog cache update prevents the next /v1/models discovery within
// FreshTTL from returning a stale "available" entry; the probe-store update
// drives the routing engine's ProbeUnreachable map so the endpoint surfaces
// with FilterReasonEndpointUnreachable in the next routing_decision.
//
// Errors that don't classify as a reachability failure (auth 401, malformed
// body, etc.) are ignored — those signals don't indicate the endpoint is
// down. Callers may invoke this from every chat-completions code path
// regardless of error class; the classifier filters internally.
func (s *service) recordDispatchFailure(provider, endpoint string, err error) {
	if s == nil || err == nil {
		return
	}
	if !serviceimpl.IsDispatchReachabilityFailure(err) {
		return
	}
	providerName := strings.TrimSpace(provider)
	endpointName := strings.TrimSpace(endpoint)
	if base, ep, ok := splitEndpointProviderRef(providerName); ok {
		providerName = base
		if endpointName == "" {
			endpointName = ep
		}
	}

	now := s.now().UTC()

	// Catalog-cache feedback: tag every cache key that matches this provider
	// endpoint's baseURL as unreachable. The key is fingerprinted by
	// (baseURL, apiKey, headers), so we look up the configured entry to build
	// the key. Skip silently when config is unavailable — the probe-store
	// update below is sufficient to gate routing in that case.
	if s.catalog != nil && providerName != "" && s.opts.ServiceConfig != nil {
		if pcfg, ok := s.opts.ServiceConfig.Provider(providerName); ok {
			for _, baseURL := range providerBaseURLsForEndpoint(pcfg, endpointName) {
				key := newCatalogCacheKey(baseURL, pcfg.APIKey, pcfg.Headers)
				s.catalog.RecordDispatchError(key, err)
			}
		}
	}

	// Probe-store feedback: routing.Inputs.ProbeUnreachable is derived from
	// this store. Recording a failed probe at dispatch time makes the next
	// routing pass within HealthSignalTTL hard-gate the candidate with
	// FilterReasonEndpointUnreachable.
	if s.providerProbe != nil && providerName != "" {
		s.providerProbe.RecordProbe(providerName, endpointName, false, now)
		s.persistProbeStore()
	}
}

// providerBaseURLsForEndpoint returns the configured base URLs for one
// endpoint name under a provider entry. When endpoint is empty, returns the
// provider's primary base URL plus all named endpoint URLs so the dispatch
// failure invalidates every cache key the provider could be using.
func providerBaseURLsForEndpoint(pcfg ServiceProviderEntry, endpoint string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if endpoint == "" {
		add(pcfg.BaseURL)
		for _, ep := range pcfg.Endpoints {
			add(ep.BaseURL)
		}
		return out
	}
	for _, ep := range pcfg.Endpoints {
		if ep.Name == endpoint {
			add(ep.BaseURL)
		}
	}
	if len(out) == 0 {
		// Fallback: caller named an endpoint we don't recognize. Use the
		// provider's primary base URL so the cache update isn't silently
		// dropped.
		add(pcfg.BaseURL)
	}
	return out
}

// dispatchFailureFromAttempt performs the class-level half of the two-stage
// reachability gate. recordDispatchFailure independently checks the diagnostic
// text before mutating catalog/probe state.
func dispatchFailureFromAttempt(attempt routehealth.Attempt) (string, string, error) {
	if attempt.Status == "" || attempt.Provider == "" {
		return "", "", nil
	}
	if routehealth.Succeeded(strings.ToLower(strings.TrimSpace(attempt.Status))) {
		return "", "", nil
	}
	if !routehealth.IsDispatchFailureClass(attempt.Reason) {
		return "", "", nil
	}
	msg := strings.TrimSpace(attempt.Error)
	if msg == "" {
		return "", "", nil
	}
	return attempt.Provider, attempt.Endpoint, errors.New(msg)
}
