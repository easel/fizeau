package fizeau

import (
	"context"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/statusview"
)

// RouteStatus returns live routing state for configured providers/models.
// Internal routehealth owns status-row filtering, ordering, health matching,
// and metric fallback; this receiver preserves the public facade and attaches
// root-owned decision-cache and routing-quality projections.
func (s *service) RouteStatus(ctx context.Context) (*RouteStatusReport, error) {
	if s != nil && s.refreshScheduler != nil {
		s.refreshScheduler.RequestPrimaryQuotaRefresh(ctx)
	}
	report := &RouteStatusReport{GeneratedAt: time.Now()}
	if s != nil && s.routingQuality != nil {
		report.RoutingQuality = fromRoutingQualityMetrics(s.routingQuality.MetricsRecent(RouteStatusRoutingQualityWindow, time.Time{}))
	}
	if s == nil || s.opts.ServiceConfig == nil {
		return report, nil
	}

	cat := serviceRoutingCatalog()
	_, snapshot := s.routingInputs(ctx, cat, modelsnapshot.RefreshBackground)
	cooldown := s.routeAttemptTTL()
	activeAttempts := s.activeRouteAttempts(report.GeneratedAt, cooldown)
	successRate, latencyMS := s.routeMetricSignals(report.GeneratedAt, cooldown)
	configuredProviders := make(map[string]struct{}, len(s.opts.ServiceConfig.ProviderNames()))
	for _, provider := range s.opts.ServiceConfig.ProviderNames() {
		if provider = strings.TrimSpace(provider); provider != "" {
			configuredProviders[provider] = struct{}{}
		}
	}
	rows := routehealth.BuildStatusRows(routehealth.StatusRowsInput{
		Snapshot:            snapshot,
		ConfiguredProviders: configuredProviders,
		ActiveAttempts:      activeAttempts,
		SuccessRate:         successRate,
		LatencyMS:           latencyMS,
		CooldownTTL:         cooldown,
	})

	report.SnapshotCapturedAt = snapshot.AsOf
	if len(rows) == 0 {
		return report, nil
	}
	report.Routes = make([]RouteStatusEntry, 0, len(rows))
	for _, row := range rows {
		entry := RouteStatusEntry{
			Model:      row.Model,
			Strategy:   row.Strategy,
			Candidates: make([]RouteCandidateStatus, 0, len(row.Candidates)),
		}
		if cached, ok := s.lookupRouteDecision(row.Model); ok && cached.decision != nil {
			entry.LastDecision = cached.decision
			entry.LastDecisionAt = cached.at
			entry.SelectedEndpoint = cached.decision.Endpoint
			entry.SelectedServerInstance = cached.decision.ServerInstance
			entry.Sticky = cached.decision.Sticky
		}
		for _, candidate := range row.Candidates {
			projected := RouteCandidateStatus{
				Provider:                      candidate.Provider,
				Endpoint:                      candidate.Endpoint,
				Model:                         candidate.Model,
				ServerInstance:                candidate.ServerInstance,
				Billing:                       candidate.Billing,
				ActualCashSpend:               candidate.ActualCashSpend,
				EffectiveCost:                 candidate.EffectiveCost,
				EffectiveCostSource:           candidate.EffectiveCostSource,
				Priority:                      candidate.Priority,
				Healthy:                       candidate.Healthy,
				SourceStatus:                  candidate.SourceStatus,
				AutoRoutable:                  candidate.AutoRoutable,
				ExactPinOnly:                  candidate.ExactPinOnly,
				ExclusionReason:               candidate.ExclusionReason,
				Power:                         candidate.Power,
				ContextLength:                 candidate.ContextLength,
				CostInputPerMTok:              candidate.CostInputPerMTok,
				CostOutputPerMTok:             candidate.CostOutputPerMTok,
				RecentLatencyMS:               candidate.RecentLatencyMS,
				ProviderReliabilityRate:       candidate.ProviderReliabilityRate,
				QuotaRemaining:                candidate.QuotaRemaining,
				SnapshotCapturedAt:            candidate.SnapshotCapturedAt,
				HealthFreshnessAt:             candidate.HealthFreshnessAt,
				HealthFreshnessSource:         candidate.HealthFreshnessSource,
				QuotaFreshnessAt:              candidate.QuotaFreshnessAt,
				QuotaFreshnessSource:          candidate.QuotaFreshnessSource,
				ModelDiscoveryFreshnessAt:     candidate.ModelDiscoveryFreshnessAt,
				ModelDiscoveryFreshnessSource: candidate.ModelDiscoveryFreshnessSource,
			}
			if candidate.Cooldown != nil {
				projected.Cooldown = &CooldownState{
					Reason:      candidate.Cooldown.Reason,
					Until:       candidate.Cooldown.Until,
					FailCount:   candidate.Cooldown.FailCount,
					LastError:   candidate.Cooldown.LastError,
					LastAttempt: candidate.Cooldown.LastAttempt,
				}
			}
			entry.Candidates = append(entry.Candidates, projected)
		}
		report.Routes = append(report.Routes, entry)
	}
	return report, nil
}

type lastDecisionEntry struct {
	decision *RouteDecision
	at       time.Time
}

// cacheRouteDecision stores a ResolveRoute result keyed by routeKey.
// Called by ResolveRoute after a successful resolution.
func (s *service) cacheRouteDecision(routeKey string, decision *RouteDecision) {
	if routeKey == "" || decision == nil {
		return
	}
	if s.routeStatusCache == nil {
		s.routeStatusCache = routehealth.NewDecisionStore[*RouteDecision]()
	}
	s.routeStatusCache.Store(routeKey, decision, time.Now())
}

// lookupRouteDecision retrieves a cached decision for routeKey.
func (s *service) lookupRouteDecision(routeKey string) (lastDecisionEntry, bool) {
	if s == nil || s.routeStatusCache == nil {
		return lastDecisionEntry{}, false
	}
	decision, ok := s.routeStatusCache.Lookup(routeKey)
	if !ok {
		return lastDecisionEntry{}, false
	}
	return lastDecisionEntry{decision: decision.Decision, at: decision.At}, true
}

func statusError(status, source string, capturedAt time.Time) *StatusError {
	return adaptStatusError(statusview.ErrorForStatus(status, source, capturedAt))
}

func statusErrorDetail(status, detail, source string, capturedAt time.Time) *StatusError {
	return adaptStatusError(statusview.ErrorForStatusDetail(status, detail, source, capturedAt))
}

func statusErrorType(status string) string {
	return statusview.ErrorType(status)
}

func quotaStatus(fresh bool, windows []harnesses.QuotaWindow) string {
	return statusview.QuotaStatus(fresh, windows)
}

func accountStatusFromInfo(info *harnesses.AccountInfo, source string, capturedAt time.Time, fresh bool) *AccountStatus {
	return adaptAccountStatus(statusview.AccountFromInfo(info, source, capturedAt, fresh))
}

func providerAuthStatus(entry ServiceProviderEntry, status string, capturedAt time.Time) AccountStatus {
	return adaptAccountStatusValue(statusview.ProviderAuthStatus(statusViewProvider(entry), status, capturedAt))
}

func providerEndpointStatus(entry ServiceProviderEntry, status string, modelCount int, capturedAt time.Time) []EndpointStatus {
	return adaptEndpointStatuses(statusview.ProviderEndpointStatus(statusViewProvider(entry), status, modelCount, capturedAt))
}

func providerQuotaState(entry ServiceProviderEntry, capturedAt time.Time) *QuotaState {
	return adaptQuotaState(statusview.ProviderQuotaState(statusViewProvider(entry), capturedAt))
}

func endpointStatus(status string) string {
	return statusview.EndpointStatusFor(status)
}

func statusViewProvider(entry ServiceProviderEntry) statusview.ServiceProvider {
	endpoints := make([]statusview.ServiceProviderEndpoint, 0, len(entry.Endpoints))
	for _, endpoint := range entry.Endpoints {
		endpoints = append(endpoints, statusview.ServiceProviderEndpoint{
			Name:    endpoint.Name,
			BaseURL: endpoint.BaseURL,
		})
	}
	return statusview.ServiceProvider{
		Type:      normalizeServiceProviderType(entry.Type),
		BaseURL:   entry.BaseURL,
		Endpoints: endpoints,
		APIKey:    entry.APIKey,
	}
}

func adaptStatusError(err *statusview.Error) *StatusError {
	if err == nil {
		return nil
	}
	return &StatusError{
		Type:      err.Type,
		Detail:    err.Detail,
		Source:    err.Source,
		Timestamp: err.Timestamp,
	}
}

func adaptAccountStatus(account *statusview.Account) *AccountStatus {
	if account == nil {
		return nil
	}
	out := adaptAccountStatusValue(*account)
	return &out
}

func adaptAccountStatusValue(account statusview.Account) AccountStatus {
	return AccountStatus{
		Authenticated:   account.Authenticated,
		Unauthenticated: account.Unauthenticated,
		Email:           account.Email,
		PlanType:        account.PlanType,
		OrgName:         account.OrgName,
		Source:          account.Source,
		CapturedAt:      account.CapturedAt,
		Fresh:           account.Fresh,
		Detail:          account.Detail,
	}
}

func adaptEndpointStatuses(endpoints []statusview.Endpoint) []EndpointStatus {
	if len(endpoints) == 0 {
		return nil
	}
	out := make([]EndpointStatus, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, EndpointStatus{
			Name:          endpoint.Name,
			BaseURL:       endpoint.BaseURL,
			ProbeURL:      endpoint.ProbeURL,
			Status:        endpoint.Status,
			Source:        endpoint.Source,
			CapturedAt:    endpoint.CapturedAt,
			Fresh:         endpoint.Fresh,
			LastSuccessAt: endpoint.LastSuccessAt,
			ModelCount:    endpoint.ModelCount,
			LastError:     adaptStatusError(endpoint.LastError),
		})
	}
	return out
}

func adaptQuotaState(quota *statusview.Quota) *QuotaState {
	if quota == nil {
		return nil
	}
	return &QuotaState{
		Source:     quota.Source,
		Status:     quota.Status,
		CapturedAt: quota.CapturedAt,
		LastError:  adaptStatusError(quota.LastError),
	}
}
