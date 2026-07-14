package fizeau

import (
	"errors"
	"strings"

	"github.com/easel/fizeau/internal/routehealth"
)

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
	if !isDispatchReachabilityFailure(err) {
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

// dispatchFailureFromFinal builds the (provider, endpoint, err) tuple from a
// finalize callback so the existing route-attempt finalize site can also feed
// the dispatch-feedback path. Returns (_, _, nil) when the final event does
// not describe a dispatch reachability failure.
func dispatchFailureFromFinal(attempt RouteAttempt) (string, string, error) {
	if attempt.Status == "" || attempt.Provider == "" {
		return "", "", nil
	}
	if routehealth.Succeeded(strings.ToLower(strings.TrimSpace(attempt.Status))) {
		return "", "", nil
	}
	if !isRouteAttemptDispatchFailure(attempt.Reason) {
		return "", "", nil
	}
	msg := strings.TrimSpace(attempt.Error)
	if msg == "" {
		return "", "", nil
	}
	return attempt.Provider, attempt.Endpoint, errors.New(msg)
}
