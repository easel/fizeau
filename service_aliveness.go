package fizeau

import (
	"context"
	"time"

	"github.com/easel/fizeau/internal/routehealth"
)

const (
	// routeTimeProbeTimeout bounds one synchronous route-time aliveness probe
	// before the provider is treated as unreachable for that route decision.
	routeTimeProbeTimeout = 2 * time.Second
	// startupProbeTotalTimeout bounds the total wall-clock time spent on
	// startup aliveness probes, regardless of provider count.
	startupProbeTotalTimeout = 5 * time.Second
)

// ProviderAlivenessProber reports whether a provider endpoint is reachable.
// Returns true if reachable, false if not. The prober must respect ctx for
// cancellation.
type ProviderAlivenessProber func(ctx context.Context, provider, baseURL string) bool

// alivenessEndpoint is a transitional root spelling used by the lifecycle
// functions that move in the next extraction slice. Endpoint identity and
// evidence mechanics are owned by internal/routehealth.
type alivenessEndpoint = routehealth.AlivenessEndpoint

func (s *service) healthProbeInterval() time.Duration {
	return routehealth.ResolveAlivenessProbeInterval(s.opts.HealthProbeInterval)
}

func (s *service) healthSignalTTL() time.Duration {
	return routehealth.ResolveAlivenessSignalTTL(s.opts.HealthSignalTTL)
}

// alivenessEndpoints enumerates the non-cloud provider endpoints that
// should be probed. Only providers whose billing type indicates fixed/local
// billing are included; cloud subscription providers are excluded.
func (s *service) alivenessEndpoints() []alivenessEndpoint {
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
	if s.providerProbe == nil {
		return
	}
	endpoints := s.alivenessEndpoints()
	if len(endpoints) == 0 {
		return
	}
	prober := s.opts.AlivenessProber
	if prober == nil {
		prober = ProviderAlivenessProber(routehealth.TCPAlivenessProber)
	}
	runStartupAlivenessProbes(ctx, endpoints, s.providerProbe, prober, startupProbeTotalTimeout)
	s.persistProbeStore()
}

// runStartupAlivenessProbes probes each endpoint sequentially within totalTimeout.
// It is exported as a standalone function for direct testing.
func runStartupAlivenessProbes(
	ctx context.Context,
	endpoints []alivenessEndpoint,
	store *routehealth.ProbeStore,
	prober ProviderAlivenessProber,
	totalTimeout time.Duration,
) {
	if len(endpoints) == 0 || store == nil || prober == nil {
		return
	}
	probeCtx := ctx
	if totalTimeout > 0 {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, totalTimeout)
		defer cancel()
	}
	now := time.Now().UTC()
	for _, ep := range endpoints {
		if probeCtx.Err() != nil {
			break
		}
		success := prober(probeCtx, ep.Provider, ep.BaseURL)
		if probeCtx.Err() != nil {
			success = false
		}
		store.RecordProbe(ep.Provider, ep.Endpoint, success, now)
	}
}

func (s *service) persistProbeStore() {
	_ = s.persistRouteHealthSnapshot()
}

// requestLocalHealthRefreshForRouting starts at most one asynchronous refresh
// for stale or missing local provider aliveness evidence. Route hot paths use
// cached probe evidence only; this method must not wait for provider IO.
func (s *service) requestLocalHealthRefreshForRouting(_ context.Context) {
	if s == nil || s.providerProbe == nil {
		return
	}
	endpoints := s.routeTimeAlivenessEndpoints(time.Now().UTC())
	if len(endpoints) == 0 {
		return
	}
	if !s.providerProbeRefreshInFlight.CompareAndSwap(false, true) {
		return
	}
	prober := s.opts.AlivenessProber
	if prober == nil {
		prober = ProviderAlivenessProber(routehealth.TCPAlivenessProber)
	}
	refreshCtx := context.Background()
	go func() {
		defer s.providerProbeRefreshInFlight.Store(false)
		runRouteTimeAlivenessProbes(refreshCtx, endpoints, s.providerProbe, prober, routeTimeProbeTimeout)
		s.persistProbeStore()
	}()
}

func (s *service) probeUnknownProviders(now time.Time) map[string]time.Time {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	return routehealth.AlivenessProbeSignals(s.providerProbe, endpoints, now, s.healthSignalTTL()).Unknown
}

func (s *service) routeTimeAlivenessEndpoints(now time.Time) []alivenessEndpoint {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	return routehealth.AlivenessDueEndpoints(s.providerProbe, endpoints, now, s.healthProbeInterval())
}

func (s *service) probeUnreachableProviders(now time.Time) map[string]time.Time {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	return routehealth.AlivenessProbeSignals(s.providerProbe, endpoints, now, s.healthSignalTTL()).Unreachable
}

func runRouteTimeAlivenessProbes(
	ctx context.Context,
	endpoints []alivenessEndpoint,
	store *routehealth.ProbeStore,
	prober ProviderAlivenessProber,
	perProbeTimeout time.Duration,
) {
	if len(endpoints) == 0 || store == nil || prober == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if perProbeTimeout <= 0 {
		perProbeTimeout = routeTimeProbeTimeout
	}
	for i, ep := range endpoints {
		if ctx.Err() != nil {
			recordRouteTimeProbeFailures(store, endpoints[i:], time.Now().UTC())
			return
		}
		probeAt := time.Now().UTC()
		probeCtx, cancel := context.WithTimeout(ctx, perProbeTimeout)
		success := prober(probeCtx, ep.Provider, ep.BaseURL)
		if probeCtx.Err() != nil {
			success = false
		}
		cancel()
		store.RecordProbe(ep.Provider, ep.Endpoint, success, probeAt)
		if ctx.Err() != nil {
			recordRouteTimeProbeFailures(store, endpoints[i+1:], probeAt)
			return
		}
	}
}

func recordRouteTimeProbeFailures(store *routehealth.ProbeStore, endpoints []alivenessEndpoint, probeAt time.Time) {
	if store == nil || len(endpoints) == 0 {
		return
	}
	for _, ep := range endpoints {
		store.RecordProbe(ep.Provider, ep.Endpoint, false, probeAt)
	}
}

// startAlivenessProbeLoop spawns the goroutine that periodically re-probes
// configured non-cloud providers. The goroutine is tied to QuotaRefreshContext
// (or context.Background()) so server callers can cancel it on shutdown.
func (s *service) startAlivenessProbeLoop() {
	if s.providerProbe == nil {
		return
	}
	endpoints := s.alivenessEndpoints()
	if len(endpoints) == 0 {
		return
	}
	ctx := s.opts.QuotaRefreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	prober := s.opts.AlivenessProber
	if prober == nil {
		prober = ProviderAlivenessProber(routehealth.TCPAlivenessProber)
	}
	store := s.providerProbe
	interval := s.healthProbeInterval()
	persistPath := s.opts.PersistRouteHealth
	go runAlivenessProbeLoop(ctx, endpoints, store, prober, interval, nil, nil, persistPath)
}

// runAlivenessProbeLoop periodically re-probes each endpoint whose last probe
// is older than interval. now and sleep are seams for deterministic tests;
// pass nil for production defaults.
func runAlivenessProbeLoop(
	ctx context.Context,
	endpoints []alivenessEndpoint,
	store *routehealth.ProbeStore,
	prober ProviderAlivenessProber,
	interval time.Duration,
	now func() time.Time,
	sleep func(ctx context.Context, d time.Duration) bool,
	persistPath string,
) {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = alivenessLoopSleep
	}
	if interval <= 0 {
		interval = routehealth.DefaultAlivenessProbeInterval
	}
	for {
		t := now().UTC()
		for _, ep := range endpoints {
			if ctx.Err() != nil {
				return
			}
			if !store.ProbeNeeded(ep.Provider, ep.Endpoint, t, interval) {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			success := prober(probeCtx, ep.Provider, ep.BaseURL)
			cancel()
			store.RecordProbe(ep.Provider, ep.Endpoint, success, t)
		}
		if persistPath != "" {
			_ = store.Save(persistPath)
		}
		if !sleep(ctx, interval) {
			return
		}
	}
}

func alivenessLoopSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
