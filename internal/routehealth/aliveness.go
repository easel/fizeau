package routehealth

import (
	"context"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultAlivenessProbeInterval is the interval used when no positive
	// background probe interval is configured.
	DefaultAlivenessProbeInterval = 60 * time.Second
	// DefaultAlivenessSignalTTL is the maximum age of aliveness evidence used
	// for routing when no positive TTL is configured.
	DefaultAlivenessSignalTTL = 10 * time.Minute
)

// AlivenessProvider is the API-neutral configured-provider input used to
// select endpoints for aliveness probing.
type AlivenessProvider struct {
	Name         string
	ConfigError  string
	FixedBilling bool
	Endpoints    []AlivenessEndpoint
}

// AlivenessEndpoint is one API-neutral provider endpoint used by aliveness
// probing and cached-evidence projection. Provider is populated by
// BuildAlivenessEndpoints for configured-provider inputs.
type AlivenessEndpoint struct {
	Provider string
	Endpoint string
	BaseURL  string
}

// AlivenessSignals contains cached aliveness evidence keyed for routing.
// Missing and stale evidence is projected separately from fresh failures.
type AlivenessSignals struct {
	Unknown     map[string]time.Time
	Unreachable map[string]time.Time
}

// ResolveAlivenessProbeInterval applies the configured probe interval or the
// internal default when configured is not positive.
func ResolveAlivenessProbeInterval(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return DefaultAlivenessProbeInterval
}

// ResolveAlivenessSignalTTL applies the configured signal TTL or the internal
// default when configured is not positive.
func ResolveAlivenessSignalTTL(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return DefaultAlivenessSignalTTL
}

// BuildAlivenessEndpoints filters invalid and non-fixed providers, drops
// blank endpoint URLs, and preserves the first occurrence of each exact
// provider+endpoint+URL tuple in input order.
func BuildAlivenessEndpoints(providers []AlivenessProvider) []AlivenessEndpoint {
	var endpoints []AlivenessEndpoint
	seen := make(map[string]struct{})
	for _, provider := range providers {
		if provider.ConfigError != "" || !provider.FixedBilling {
			continue
		}
		for _, endpoint := range provider.Endpoints {
			if strings.TrimSpace(endpoint.BaseURL) == "" {
				continue
			}
			key := provider.Name + "\x00" + endpoint.Endpoint + "\x00" + endpoint.BaseURL
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			endpoint.Provider = provider.Name
			endpoints = append(endpoints, endpoint)
		}
	}
	if len(endpoints) == 0 {
		return nil
	}
	return endpoints
}

// AlivenessProbeRecord returns exact endpoint evidence when present, falling
// back to provider-level evidence only when the endpoint has no exact record.
func AlivenessProbeRecord(store *ProbeStore, endpoint AlivenessEndpoint) (ProbeRecord, bool) {
	if store == nil {
		return ProbeRecord{}, false
	}
	if record, ok := store.LastProbe(endpoint.Provider, endpoint.Endpoint); ok {
		return record, true
	}
	if endpoint.Endpoint != "" {
		return store.LastProbe(endpoint.Provider, "")
	}
	return ProbeRecord{}, false
}

// AlivenessRouteKeys returns the routing keys affected by one endpoint. A
// named endpoint contributes both its provider key and provider@endpoint key.
func AlivenessRouteKeys(endpoint AlivenessEndpoint) []string {
	provider := strings.TrimSpace(endpoint.Provider)
	if provider == "" {
		return nil
	}
	name := strings.TrimSpace(endpoint.Endpoint)
	if name == "" {
		return []string{provider}
	}
	ref := provider + "@" + name
	return []string{provider, ref}
}

// AlivenessDueEndpoints returns endpoints whose exact evidence is missing or
// old enough to refresh. Fresh provider-level evidence suppresses a probe for
// a named endpoint whose exact evidence is either missing or stale.
func AlivenessDueEndpoints(store *ProbeStore, endpoints []AlivenessEndpoint, now time.Time, interval time.Duration) []AlivenessEndpoint {
	if store == nil || len(endpoints) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	interval = ResolveAlivenessProbeInterval(interval)
	out := make([]AlivenessEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if !store.ProbeNeeded(endpoint.Provider, endpoint.Endpoint, now, interval) {
			continue
		}
		if endpoint.Endpoint != "" {
			if record, ok := store.LastProbe(endpoint.Provider, ""); ok && now.Sub(record.LastProbeAt) < interval {
				continue
			}
		}
		out = append(out, endpoint)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AlivenessProbeSignals projects fresh failed probes and missing/stale probes
// into the provider and provider@endpoint routing keys used by the engine.
func AlivenessProbeSignals(store *ProbeStore, endpoints []AlivenessEndpoint, now time.Time, ttl time.Duration) AlivenessSignals {
	if store == nil || len(endpoints) == 0 {
		return AlivenessSignals{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	ttl = ResolveAlivenessSignalTTL(ttl)
	var signals AlivenessSignals
	for _, endpoint := range endpoints {
		record, ok := AlivenessProbeRecord(store, endpoint)
		keys := AlivenessRouteKeys(endpoint)
		if !ok {
			for _, key := range keys {
				signals.Unknown = recordSignal(signals.Unknown, key, time.Time{})
			}
			continue
		}
		if now.Sub(record.LastProbeAt) > ttl {
			for _, key := range keys {
				signals.Unknown = recordSignal(signals.Unknown, key, record.LastProbeAt)
			}
			continue
		}
		if record.LastProbeSuccess {
			continue
		}
		for _, key := range keys {
			signals.Unreachable = recordLatestSignal(signals.Unreachable, key, record.LastProbeAt)
		}
	}
	return signals
}

func recordSignal(signals map[string]time.Time, key string, observedAt time.Time) map[string]time.Time {
	if key == "" {
		return signals
	}
	if signals == nil {
		signals = make(map[string]time.Time)
	}
	signals[key] = observedAt
	return signals
}

func recordLatestSignal(signals map[string]time.Time, key string, observedAt time.Time) map[string]time.Time {
	if key == "" {
		return signals
	}
	if signals == nil {
		signals = make(map[string]time.Time)
	}
	if existing, ok := signals[key]; !ok || observedAt.After(existing) {
		signals[key] = observedAt
	}
	return signals
}

// ExtractHostPort extracts a dialable host:port from a base URL, adding the
// scheme default when no explicit port is present. Scheme-less inputs default
// to HTTP, and IPv6 hosts retain the brackets required by net.Dialer.
func ExtractHostPort(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}

// TCPAlivenessProber reports endpoint reachability using a TCP connect probe.
func TCPAlivenessProber(ctx context.Context, _, baseURL string) bool {
	address := ExtractHostPort(baseURL)
	if address == "" {
		return false
	}
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
