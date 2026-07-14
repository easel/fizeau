package fizeau

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/routehealth"
)

const (
	// defaultHealthProbeInterval is the interval between background aliveness
	// probes when ServiceOptions.HealthProbeInterval is zero.
	defaultHealthProbeInterval = 60 * time.Second
	// defaultHealthSignalTTL is the maximum age of a probe result used to
	// populate routing.Inputs.ProbeUnreachable when HealthSignalTTL is zero.
	defaultHealthSignalTTL = 10 * time.Minute
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

// alivenessEndpoint describes one provider endpoint to probe.
type alivenessEndpoint struct {
	provider string
	endpoint string
	baseURL  string
}

func (s *service) healthProbeInterval() time.Duration {
	if s.opts.HealthProbeInterval > 0 {
		return s.opts.HealthProbeInterval
	}
	return defaultHealthProbeInterval
}

func (s *service) healthSignalTTL() time.Duration {
	if s.opts.HealthSignalTTL > 0 {
		return s.opts.HealthSignalTTL
	}
	return defaultHealthSignalTTL
}

// alivenessEndpoints enumerates the non-cloud provider endpoints that
// should be probed. Only providers whose billing type indicates fixed/local
// billing are included; cloud subscription providers are excluded.
func (s *service) alivenessEndpoints() []alivenessEndpoint {
	if s.opts.ServiceConfig == nil {
		return nil
	}
	var endpoints []alivenessEndpoint
	seen := make(map[string]struct{})
	for _, name := range s.opts.ServiceConfig.ProviderNames() {
		entry, ok := s.opts.ServiceConfig.Provider(name)
		if !ok || entry.ConfigError != "" {
			continue
		}
		if !providerTypeUsesFixedBilling(entry.Type) {
			continue
		}
		for _, endpoint := range modelDiscoveryEndpoints(entry) {
			if endpoint.BaseURL == "" {
				continue
			}
			key := name + "|" + endpoint.Name + "|" + endpoint.BaseURL
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				endpoints = append(endpoints, alivenessEndpoint{
					provider: name,
					endpoint: endpoint.Name,
					baseURL:  endpoint.BaseURL,
				})
			}
		}
	}
	return endpoints
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
		prober = tcpAlivenessProber
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
		success := prober(probeCtx, ep.provider, ep.baseURL)
		if probeCtx.Err() != nil {
			success = false
		}
		store.RecordProbe(ep.provider, ep.endpoint, success, now)
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
		prober = tcpAlivenessProber
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
	if len(endpoints) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ttl := s.healthSignalTTL()
	out := make(map[string]time.Time)
	for _, ep := range endpoints {
		record, ok := s.lastProbeForAlivenessEndpoint(ep)
		if !ok {
			for _, key := range alivenessRouteKeys(ep) {
				out[key] = time.Time{}
			}
			continue
		}
		if ttl > 0 && now.Sub(record.LastProbeAt) > ttl {
			for _, key := range alivenessRouteKeys(ep) {
				out[key] = record.LastProbeAt
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *service) routeTimeAlivenessEndpoints(now time.Time) []alivenessEndpoint {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	if len(endpoints) == 0 {
		return nil
	}
	interval := s.healthProbeInterval()
	out := make([]alivenessEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		if s.probeNeededForAlivenessEndpoint(ep, now, interval) {
			out = append(out, ep)
		}
	}
	return out
}

func (s *service) probeNeededForAlivenessEndpoint(ep alivenessEndpoint, now time.Time, interval time.Duration) bool {
	if s == nil || s.providerProbe == nil {
		return false
	}
	if !s.providerProbe.ProbeNeeded(ep.provider, ep.endpoint, now, interval) {
		return false
	}
	if ep.endpoint == "" {
		return true
	}
	if r, ok := s.providerProbe.LastProbe(ep.provider, ""); ok {
		return now.Sub(r.LastProbeAt) >= interval
	}
	return true
}

func (s *service) probeUnreachableProviders(now time.Time) map[string]time.Time {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	endpoints := s.alivenessEndpoints()
	if len(endpoints) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ttl := s.healthSignalTTL()
	out := make(map[string]time.Time)
	for _, ep := range endpoints {
		record, ok := s.lastProbeForAlivenessEndpoint(ep)
		if !ok || record.LastProbeSuccess {
			continue
		}
		if ttl > 0 && now.Sub(record.LastProbeAt) > ttl {
			continue
		}
		for _, key := range alivenessRouteKeys(ep) {
			if existing, ok := out[key]; !ok || record.LastProbeAt.After(existing) {
				out[key] = record.LastProbeAt
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *service) lastProbeForAlivenessEndpoint(ep alivenessEndpoint) (routehealth.ProbeRecord, bool) {
	if s == nil || s.providerProbe == nil {
		return routehealth.ProbeRecord{}, false
	}
	if r, ok := s.providerProbe.LastProbe(ep.provider, ep.endpoint); ok {
		return r, true
	}
	if ep.endpoint != "" {
		return s.providerProbe.LastProbe(ep.provider, "")
	}
	return routehealth.ProbeRecord{}, false
}

func alivenessRouteKeys(ep alivenessEndpoint) []string {
	provider := strings.TrimSpace(ep.provider)
	if provider == "" {
		return nil
	}
	endpoint := strings.TrimSpace(ep.endpoint)
	if endpoint == "" {
		return []string{provider}
	}
	ref := endpointProviderRef(provider, endpoint)
	if ref == provider {
		return []string{provider}
	}
	return []string{provider, ref}
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
		success := prober(probeCtx, ep.provider, ep.baseURL)
		if probeCtx.Err() != nil {
			success = false
		}
		cancel()
		store.RecordProbe(ep.provider, ep.endpoint, success, probeAt)
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
		store.RecordProbe(ep.provider, ep.endpoint, false, probeAt)
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
		prober = tcpAlivenessProber
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
		interval = defaultHealthProbeInterval
	}
	for {
		t := now().UTC()
		for _, ep := range endpoints {
			if ctx.Err() != nil {
				return
			}
			if !store.ProbeNeeded(ep.provider, ep.endpoint, t, interval) {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			success := prober(probeCtx, ep.provider, ep.baseURL)
			cancel()
			store.RecordProbe(ep.provider, ep.endpoint, success, t)
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

// tcpAlivenessProber tests endpoint reachability via a TCP connect probe.
func tcpAlivenessProber(ctx context.Context, _, baseURL string) bool {
	addr := extractHostPort(baseURL)
	if addr == "" {
		return false
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// extractHostPort extracts host:port from a base URL, adding the scheme's
// default port when none is specified.
func extractHostPort(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	host := u.Host
	if host == "" {
		return ""
	}
	if !strings.Contains(host, ":") {
		switch u.Scheme {
		case "https":
			host += ":443"
		default:
			host += ":80"
		}
	}
	return host
}
