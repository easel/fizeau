package fizeau

// This file implements ListProviders and HealthCheck for the FizeauService service.
// It lives in the root package to avoid import cycles; provider config data is
// injected via the ServiceConfig interface defined in service.go.
//
// Provider probing and model/provider metadata helpers live behind
// internal/serviceimpl; this root file keeps the public service methods and
// status projections stable at github.com/easel/fizeau.

import (
	"context"
	"fmt"
	"time"

	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// ListProviders returns providers known to the native fiz harness with live
// status, configured-default markers, and cooldown state.
func (s *service) ListProviders(ctx context.Context) ([]ProviderInfo, error) {
	sc := s.opts.ServiceConfig
	if sc == nil {
		return nil, fmt.Errorf("service: no ServiceConfig provided; pass ServiceOptions.ServiceConfig")
	}

	names := sc.ProviderNames()
	providers := make(map[string]serviceimpl.ProviderEntry, len(names))
	for _, name := range names {
		if entry, ok := sc.Provider(name); ok {
			providers[name] = serviceImplProviderEntry(entry)
		}
	}
	rows := serviceimpl.BuildProviderInventory(ctx, serviceimpl.ProviderInventoryInput{
		ProviderNames:   names,
		Providers:       providers,
		DefaultProvider: sc.DefaultProviderName(),
		ProbeTimeout:    5 * time.Second,
	})
	out := make([]ProviderInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, providerInfoFromServiceImpl(row))
	}
	return out, nil
}

func providerInfoFromServiceImpl(row serviceimpl.ProviderInventoryRow) ProviderInfo {
	var endpoints []ServiceProviderEndpoint
	if len(row.Endpoints) > 0 {
		endpoints = make([]ServiceProviderEndpoint, 0, len(row.Endpoints))
		for _, endpoint := range row.Endpoints {
			endpoints = append(endpoints, ServiceProviderEndpoint{
				Name:           endpoint.Name,
				BaseURL:        endpoint.BaseURL,
				ServerInstance: endpoint.ServerInstance,
			})
		}
	}
	var endpointStatuses []EndpointStatus
	if len(row.EndpointStatus) > 0 {
		endpointStatuses = make([]EndpointStatus, 0, len(row.EndpointStatus))
		for _, endpoint := range row.EndpointStatus {
			endpointStatuses = append(endpointStatuses, EndpointStatus{
				Name:           endpoint.Name,
				BaseURL:        endpoint.BaseURL,
				ServerInstance: endpoint.ServerInstance,
				ProbeURL:       endpoint.ProbeURL,
				Status:         endpoint.Status,
				Source:         endpoint.Source,
				CapturedAt:     endpoint.CapturedAt,
				Fresh:          endpoint.Fresh,
				LastSuccessAt:  endpoint.LastSuccessAt,
				ModelCount:     endpoint.ModelCount,
				LastError:      adaptStatusError(endpoint.LastError),
			})
		}
	}
	return ProviderInfo{
		Name:             row.Name,
		Type:             row.Type,
		BaseURL:          row.BaseURL,
		Endpoints:        endpoints,
		Status:           row.Status,
		ModelCount:       row.ModelCount,
		Capabilities:     append([]string(nil), row.Capabilities...),
		Billing:          row.Billing,
		IncludeByDefault: row.IncludeByDefault,
		IsDefault:        row.IsDefault,
		DefaultModel:     row.DefaultModel,
		Auth:             adaptAccountStatusValue(row.Auth),
		EndpointStatus:   endpointStatuses,
		Quota:            adaptQuotaState(row.Quota),
		LastError:        adaptStatusError(row.LastError),
	}
}

// HealthCheck triggers a fresh probe for the named health-check subject and updates internal state.
// health.Type is "harness" or "provider".
func (s *service) HealthCheck(ctx context.Context, health HealthTarget) error {
	switch health.Type {
	case "provider":
		sc := s.opts.ServiceConfig
		if sc == nil {
			return fmt.Errorf("service: no ServiceConfig provided; pass ServiceOptions.ServiceConfig")
		}
		entry, ok := sc.Provider(health.Name)
		if !ok {
			return fmt.Errorf("service: provider %q not found", health.Name)
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		probe := serviceimpl.ProbeProviderStatus(probeCtx, serviceImplProviderEntry(entry), time.Now().UTC(), nil)
		if probe.Status == "connected" {
			return nil
		}
		msg := probe.Status
		if probe.Detail != "" {
			msg = probe.Detail
		}
		return fmt.Errorf("service: provider %q: %s", health.Name, msg)

	case "harness":
		statuses := s.registry.Discover()
		for _, st := range statuses {
			if st.Name != health.Name {
				continue
			}
			if !st.Available {
				return fmt.Errorf("service: harness %q unavailable: %s", health.Name, st.Error)
			}
			// For subscription harnesses, refresh the quota cache when stale.
			if health.Name == "claude" {
				s.healthCheckRefreshClaudeQuota(ctx)
				// Offline auth probe: when credentials look usable after a
				// prior credential_invalid demotion, record harness success so
				// soft cooldowns clear without requiring a worker restart
				// (fizeau-0c5ae39c).
				if usability := anthropic.ProbeClaudeAuthUsability(); usability.Class == "" {
					_ = s.routeHealthStore().RecordAttempt(routehealth.Attempt{
						Harness:   "claude",
						Status:    "success",
						Timestamp: time.Now().UTC(),
					})
				}
			}
			if health.Name == "claude-tui" {
				if usability := anthropic.ProbeClaudeAuthUsability(); usability.Class == "" {
					_ = s.routeHealthStore().RecordAttempt(routehealth.Attempt{
						Harness:   "claude-tui",
						Status:    "success",
						Timestamp: time.Now().UTC(),
					})
				}
			}
			if health.Name == "codex" {
				if s.refreshScheduler != nil {
					s.refreshScheduler.RefreshPrimaryQuotaForHealthCheck(ctx, "codex")
				}
			}
			return nil
		}
		return fmt.Errorf("service: harness %q not registered", health.Name)

	default:
		return fmt.Errorf("service: unknown HealthTarget.Type %q (want \"harness\" or \"provider\")", health.Type)
	}
}

func normalizeServiceProviderType(t string) string {
	return serviceimpl.NormalizeProviderType(t)
}

// healthCheckRefreshClaudeQuota refreshes the Claude quota cache when
// the scheduler classifies the cached snapshot as stale. It is
// a best-effort operation: errors are silently discarded so that a
// claude absence does not fail HealthCheck. Under CONTRACT-004 the
// refresh delegates to QuotaHarness.RefreshQuota, which owns the PTY
// probe and cache I/O.
func (s *service) healthCheckRefreshClaudeQuota(ctx context.Context) {
	if s == nil || s.refreshScheduler == nil {
		return
	}
	s.refreshScheduler.RefreshPrimaryQuotaForHealthCheck(ctx, "claude")
}
