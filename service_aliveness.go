package fizeau

import (
	"context"
	"time"

	"github.com/easel/fizeau/internal/routehealth"
)

// ProviderAlivenessProber reports whether a provider endpoint is reachable.
// Returns true if reachable, false if not. The prober must respect ctx for
// cancellation.
type ProviderAlivenessProber func(ctx context.Context, provider, baseURL string) bool

func (s *service) healthProbeInterval() time.Duration {
	return routehealth.ResolveAlivenessProbeInterval(s.opts.HealthProbeInterval)
}

func (s *service) healthSignalTTL() time.Duration {
	return routehealth.ResolveAlivenessSignalTTL(s.opts.HealthSignalTTL)
}

// alivenessEndpoints enumerates the non-cloud provider endpoints that
// should be probed. Only providers whose billing type indicates fixed/local
// billing are included; cloud subscription providers are excluded.
func (s *service) alivenessEndpoints() []routehealth.AlivenessEndpoint {
	if s.opts.ServiceConfig == nil {
		return nil
	}
	names := s.opts.ServiceConfig.ProviderNames()
	providers := make([]routehealth.AlivenessProvider, 0, len(names))
	for _, name := range names {
		entry, ok := s.opts.ServiceConfig.Provider(name)
		if !ok {
			continue
		}
		provider := routehealth.AlivenessProvider{
			Name:         name,
			ConfigError:  entry.ConfigError,
			FixedBilling: providerTypeUsesFixedBilling(entry.Type),
		}
		for _, endpoint := range modelDiscoveryEndpoints(entry) {
			provider.Endpoints = append(provider.Endpoints, routehealth.AlivenessEndpoint{
				Endpoint: endpoint.Name,
				BaseURL:  endpoint.BaseURL,
			})
		}
		providers = append(providers, provider)
	}
	return routehealth.BuildAlivenessEndpoints(providers)
}

// startupAlivenessProbe probes all configured non-cloud providers synchronously.
// It is reserved for explicit diagnostics/tests; New starts only the background
// probe loop so service construction cannot block on dead local endpoints.
func (s *service) startupAlivenessProbe(ctx context.Context) {
	if s == nil || s.aliveness == nil {
		return
	}
	s.aliveness.Startup(ctx, s.alivenessEndpoints(), 0)
}

func (s *service) persistProbeStore() {
	_ = s.persistRouteHealthSnapshot()
}

// requestLocalHealthRefreshForRouting starts at most one asynchronous refresh
// for stale or missing local provider aliveness evidence. Route hot paths use
// cached probe evidence only; this method must not wait for provider IO.
func (s *service) requestLocalHealthRefreshForRouting(_ context.Context) {
	if s == nil || s.aliveness == nil {
		return
	}
	s.aliveness.RequestRefresh(s.alivenessEndpoints(), s.healthProbeInterval())
}

func (s *service) probeUnknownProviders(now time.Time) map[string]time.Time {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	return routehealth.AlivenessProbeSignals(s.providerProbe, endpoints, now, s.healthSignalTTL()).Unknown
}

func (s *service) probeUnreachableProviders(now time.Time) map[string]time.Time {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	return routehealth.AlivenessProbeSignals(s.providerProbe, endpoints, now, s.healthSignalTTL()).Unreachable
}

// startAlivenessProbeLoop spawns the goroutine that periodically re-probes
// configured non-cloud providers. The goroutine is tied to QuotaRefreshContext
// (or context.Background()) so server callers can cancel it on shutdown.
func (s *service) startAlivenessProbeLoop() {
	if s == nil || s.aliveness == nil {
		return
	}
	ctx := s.opts.QuotaRefreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	s.aliveness.StartLoop(ctx, s.alivenessEndpoints(), s.healthProbeInterval())
}
