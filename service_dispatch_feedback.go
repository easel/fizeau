package fizeau

import (
	"context"
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
	return routehealth.RecordAttempt(routehealth.AttemptTransaction{
		Store:         s.routeHealthStore(),
		RecordOptions: routehealth.RecordOptions{ExactSuccessClear: true},
		Persist:       s.persistRouteHealthSnapshot,
	}, internalRouteAttempt(attempt))
}

// recordRouteAttemptFromFinal admits legacy/native final evidence. Older
// native paths may lack an explicit failure class, so the internal converter
// retains its bounded diagnostic-text fallback for this mode only.
func (s *service) recordRouteAttemptFromFinal(final harnesses.FinalData) {
	_ = routehealth.ObserveFinalAttempt(routehealth.AttemptTransaction{
		Store:         s.routeHealthStore(),
		RecordOptions: routehealth.RecordOptions{ExactSuccessClear: true},
		Persist:       s.persistRouteHealthSnapshot,
		Dispatch:      s.recordDispatchFailure,
	}, final, routehealth.FinalEvidenceAllowLegacyText)
}

// observeRouteAttemptFromFinal admits wrapped-harness evidence only when the
// adapter supplied an authoritative typed failure class. Persistence errors
// are returned to the subprocess finalizer as bounded terminal warnings.
func (s *service) observeRouteAttemptFromFinal(final harnesses.FinalData) error {
	return routehealth.ObserveFinalAttempt(routehealth.AttemptTransaction{
		Store:         s.routeHealthStore(),
		RecordOptions: routehealth.RecordOptions{ExactSuccessClear: true},
		Persist:       s.persistRouteHealthSnapshot,
		Dispatch:      s.recordDispatchFailure,
	}, final, routehealth.FinalEvidenceTypedOnly)
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

// recordDispatchFailure preserves a chat-completions reachability failure in
// both the catalog cache and the routehealth probe store.
//
// The catalog cache receives endpoint-keyed write-side feedback. The
// probe-store update drives the routing engine's ProbeUnreachable map so the
// endpoint surfaces with FilterReasonEndpointUnreachable in the next
// routing_decision.
//
// Errors that don't classify as a reachability failure (auth 401, malformed
// body, etc.) are ignored — those signals don't indicate the endpoint is
// down. Callers may invoke this from every chat-completions code path
// regardless of error class; the classifier filters internally.
func (s *service) recordDispatchFailure(provider, endpoint string, err error) {
	if s == nil || err == nil {
		return
	}
	feedback := routehealth.DispatchFeedback{
		IsReachabilityFailure: serviceimpl.IsDispatchReachabilityFailure,
		Now:                   s.now,
	}
	if s.catalog != nil && s.opts.ServiceConfig != nil {
		feedback.LookupProvider = func(providerName string) (routehealth.DispatchProvider, bool) {
			pcfg, ok := s.opts.ServiceConfig.Provider(providerName)
			if !ok {
				return routehealth.DispatchProvider{}, false
			}
			endpoints := make([]routehealth.DispatchEndpoint, 0, len(pcfg.Endpoints))
			for _, endpoint := range pcfg.Endpoints {
				endpoints = append(endpoints, routehealth.DispatchEndpoint{Name: endpoint.Name, BaseURL: endpoint.BaseURL})
			}
			return routehealth.DispatchProvider{
				BaseURL:   pcfg.BaseURL,
				Endpoints: endpoints,
				RecordCatalog: func(baseURL string, err error) {
					key := serviceimpl.NewCatalogCacheKey(baseURL, pcfg.APIKey, pcfg.Headers)
					s.catalog.RecordDispatchError(key, err)
				},
			}, true
		}
	}
	if s.providerProbe != nil {
		feedback.RecordProbe = s.providerProbe.RecordProbe
		feedback.PersistProbe = s.persistRouteHealthSnapshot
	}
	feedback.Record(provider, endpoint, err)
}
