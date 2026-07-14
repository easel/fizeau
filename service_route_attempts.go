package fizeau

import (
	"context"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
)

func (s *service) RecordRouteAttempt(_ context.Context, attempt RouteAttempt) error {
	if s == nil {
		s = &service{}
	}
	if err := s.routeHealthStore().RecordAttempt(routehealth.Attempt{
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
	}); err != nil {
		return err
	}
	return s.persistRouteHealthSnapshot()
}

func (s *service) recordRouteAttemptFromFinal(final harnesses.FinalData) {
	attempt, ok := routeAttemptFromFinal(final)
	if !ok {
		return
	}
	_ = s.RecordRouteAttempt(context.Background(), attempt)
	// Dispatch reachability failures (transport-class errors) also feed the
	// catalog cache and probe store so the next routing pass within the
	// freshness/cooldown window hard-gates the endpoint with
	// FilterReasonEndpointUnreachable instead of replaying the timeout.
	if provider, endpoint, dispatchErr := dispatchFailureFromFinal(attempt); dispatchErr != nil {
		s.recordDispatchFailure(provider, endpoint, dispatchErr)
	}
}

func routeAttemptFromFinal(final harnesses.FinalData) (RouteAttempt, bool) {
	if final.RoutingActual == nil {
		return RouteAttempt{}, false
	}
	attempt := RouteAttempt{
		Harness:        strings.TrimSpace(final.RoutingActual.Harness),
		Provider:       strings.TrimSpace(final.RoutingActual.Provider),
		Model:          strings.TrimSpace(final.RoutingActual.Model),
		ServerInstance: strings.TrimSpace(final.RoutingActual.ServerInstance),
		Status:         strings.TrimSpace(final.Status),
		Reason:         routeAttemptFailureClass(final),
		Error:          strings.TrimSpace(final.Error),
	}
	if attempt.Status == "" || (attempt.Harness == "" && attempt.Provider == "") {
		return RouteAttempt{}, false
	}
	if final.DurationMS > 0 {
		attempt.Duration = time.Duration(final.DurationMS) * time.Millisecond
	}
	if providerName, endpointName, ok := splitEndpointProviderRef(attempt.Provider); ok {
		attempt.Provider = providerName
		attempt.Endpoint = endpointName
	}
	if routehealth.Succeeded(strings.ToLower(attempt.Status)) {
		return attempt, true
	}
	if !isRouteAttemptDispatchFailure(attempt.Reason) {
		return RouteAttempt{}, false
	}
	return attempt, true
}

func routeAttemptFailureClass(final harnesses.FinalData) string {
	if final.RoutingActual == nil {
		return ""
	}
	if cls := strings.ToLower(strings.TrimSpace(final.RoutingActual.FailureClass)); cls != "" {
		return cls
	}
	return classifyRouteAttemptFailure(final.Error)
}

func isRouteAttemptDispatchFailure(class string) bool {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "availability", "protocol", "transport":
		return true
	default:
		return false
	}
}

func classifyRouteAttemptFailure(errMsg string) string {
	msg := strings.ToLower(strings.TrimSpace(errMsg))
	switch {
	case msg == "":
		return ""
	case strings.Contains(msg, "no provider configured"),
		strings.Contains(msg, "not available"),
		strings.Contains(msg, "exhausted"),
		strings.Contains(msg, "not configured"),
		strings.Contains(msg, "binary not found"):
		return "availability"
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "deadline"),
		strings.Contains(msg, "connection"),
		strings.Contains(msg, "refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "transport"),
		strings.Contains(msg, "dial tcp"),
		strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "no route to host"),
		strings.Contains(msg, "i/o timeout"):
		return "transport"
	case strings.Contains(msg, "http "),
		strings.Contains(msg, "status "),
		strings.Contains(msg, "bad request"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "not found"),
		strings.Contains(msg, "unsupported"):
		return "protocol"
	default:
		return ""
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

func routeAttemptCooldown(record routehealth.Record, ttl time.Duration) *CooldownState {
	cooldown := routehealth.CooldownFromRecord(record, ttl)
	return &CooldownState{
		Reason:      cooldown.Reason,
		Until:       cooldown.Until,
		FailCount:   cooldown.FailCount,
		LastError:   cooldown.LastError,
		LastAttempt: cooldown.LastAttempt,
	}
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
