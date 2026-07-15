package routehealth

import (
	"maps"
	"slices"
	"testing"
	"time"
)

func TestBuildAlivenessEndpointsFiltersAndDeduplicates(t *testing.T) {
	providers := []AlivenessProvider{
		{
			Name:         "invalid",
			ConfigError:  "bad config",
			FixedBilling: true,
			Endpoints:    []AlivenessEndpoint{{Endpoint: "default", BaseURL: "http://invalid:1"}},
		},
		{
			Name:         "cloud",
			FixedBilling: false,
			Endpoints:    []AlivenessEndpoint{{Endpoint: "default", BaseURL: "https://cloud.example"}},
		},
		{
			Name:         "local",
			FixedBilling: true,
			Endpoints: []AlivenessEndpoint{
				{Endpoint: "blank", BaseURL: "  "},
				{Endpoint: "desk-a", BaseURL: "http://127.0.0.1:8001/v1"},
				{Endpoint: "desk-a", BaseURL: "http://127.0.0.1:8001/v1"},
				{Endpoint: "desk-b", BaseURL: "http://127.0.0.1:8001/v1"},
				{Endpoint: "desk-a", BaseURL: "http://127.0.0.1:8002/v1"},
			},
		},
		{
			Name:         "second",
			FixedBilling: true,
			Endpoints:    []AlivenessEndpoint{{Endpoint: "default", BaseURL: "second:9000"}},
		},
	}

	got := BuildAlivenessEndpoints(providers)
	want := []AlivenessEndpoint{
		{Provider: "local", Endpoint: "desk-a", BaseURL: "http://127.0.0.1:8001/v1"},
		{Provider: "local", Endpoint: "desk-b", BaseURL: "http://127.0.0.1:8001/v1"},
		{Provider: "local", Endpoint: "desk-a", BaseURL: "http://127.0.0.1:8002/v1"},
		{Provider: "second", Endpoint: "default", BaseURL: "second:9000"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("BuildAlivenessEndpoints() = %#v, want stable filtered endpoints %#v", got, want)
	}
}

func TestAlivenessProbeSignalsPreserveEndpointFallbackAndTTL(t *testing.T) {
	for _, test := range []struct {
		configured time.Duration
		want       time.Duration
	}{
		{configured: 0, want: 10 * time.Minute},
		{configured: -time.Second, want: 10 * time.Minute},
		{configured: 17 * time.Second, want: 17 * time.Second},
	} {
		if got := ResolveAlivenessSignalTTL(test.configured); got != test.want {
			t.Fatalf("ResolveAlivenessSignalTTL(%v) = %v, want %v", test.configured, got, test.want)
		}
	}

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := NewProbeStore()
	store.RecordProbe("fallback", "", false, now.Add(-time.Minute))
	store.RecordProbe("fallback", "healthy", true, now.Add(-30*time.Second))
	store.RecordProbe("stale", "primary", false, now.Add(-DefaultAlivenessSignalTTL-time.Nanosecond))
	store.RecordProbe("edge", "primary", false, now.Add(-DefaultAlivenessSignalTTL))
	store.RecordProbe("newer", "a", false, now.Add(-2*time.Minute))
	store.RecordProbe("newer", "b", false, now.Add(-time.Minute))
	store.RecordProbe("mixed", "stale", false, now.Add(-DefaultAlivenessSignalTTL-time.Minute))
	store.RecordProbe("mixed-reverse", "stale", false, now.Add(-DefaultAlivenessSignalTTL-time.Minute))

	endpoints := []AlivenessEndpoint{
		{Provider: "fallback", Endpoint: "dead"},
		{Provider: "fallback", Endpoint: "healthy"},
		{Provider: "stale", Endpoint: "primary"},
		{Provider: "edge", Endpoint: "primary"},
		{Provider: "missing", Endpoint: "primary"},
		{Provider: "newer", Endpoint: "a"},
		{Provider: "newer", Endpoint: "b"},
		// Preserve the legacy stable-order overwrite when stale and missing
		// endpoint evidence contribute the same provider routing key.
		{Provider: "mixed", Endpoint: "stale"},
		{Provider: "mixed", Endpoint: "missing"},
		{Provider: "mixed-reverse", Endpoint: "missing"},
		{Provider: "mixed-reverse", Endpoint: "stale"},
	}
	signals := AlivenessProbeSignals(store, endpoints, now, 0)

	wantUnreachable := map[string]time.Time{
		"fallback":      now.Add(-time.Minute),
		"fallback@dead": now.Add(-time.Minute),
		"edge":          now.Add(-DefaultAlivenessSignalTTL),
		"edge@primary":  now.Add(-DefaultAlivenessSignalTTL),
		"newer":         now.Add(-time.Minute),
		"newer@a":       now.Add(-2 * time.Minute),
		"newer@b":       now.Add(-time.Minute),
	}
	if !maps.Equal(signals.Unreachable, wantUnreachable) {
		t.Fatalf("Unreachable = %#v, want exact map %#v", signals.Unreachable, wantUnreachable)
	}
	wantUnknown := map[string]time.Time{
		"stale":                 now.Add(-DefaultAlivenessSignalTTL - time.Nanosecond),
		"stale@primary":         now.Add(-DefaultAlivenessSignalTTL - time.Nanosecond),
		"missing":               {},
		"missing@primary":       {},
		"mixed":                 {},
		"mixed@stale":           now.Add(-DefaultAlivenessSignalTTL - time.Minute),
		"mixed@missing":         {},
		"mixed-reverse":         now.Add(-DefaultAlivenessSignalTTL - time.Minute),
		"mixed-reverse@missing": {},
		"mixed-reverse@stale":   now.Add(-DefaultAlivenessSignalTTL - time.Minute),
	}
	if !maps.Equal(signals.Unknown, wantUnknown) {
		t.Fatalf("Unknown = %#v, want exact map %#v", signals.Unknown, wantUnknown)
	}
}

func TestAlivenessDueEndpointsRespectProviderFallback(t *testing.T) {
	for _, test := range []struct {
		configured time.Duration
		want       time.Duration
	}{
		{configured: 0, want: 60 * time.Second},
		{configured: -time.Second, want: 60 * time.Second},
		{configured: 17 * time.Second, want: 17 * time.Second},
	} {
		if got := ResolveAlivenessProbeInterval(test.configured); got != test.want {
			t.Fatalf("ResolveAlivenessProbeInterval(%v) = %v, want %v", test.configured, got, test.want)
		}
	}

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := NewProbeStore()
	store.RecordProbe("exact-fresh", "primary", true, now.Add(-30*time.Second))
	store.RecordProbe("exact-edge", "primary", true, now.Add(-DefaultAlivenessProbeInterval))
	store.RecordProbe("fallback-fresh", "", true, now.Add(-30*time.Second))
	store.RecordProbe("fallback-stale", "", true, now.Add(-DefaultAlivenessProbeInterval-time.Nanosecond))
	store.RecordProbe("stale-exact-provider-fresh", "", true, now.Add(-30*time.Second))
	store.RecordProbe("stale-exact-provider-fresh", "primary", false, now.Add(-2*DefaultAlivenessProbeInterval))
	store.RecordProbe("sibling", "", false, now.Add(-2*DefaultAlivenessProbeInterval))
	store.RecordProbe("sibling", "healthy", true, now.Add(-30*time.Second))

	endpoints := []AlivenessEndpoint{
		{Provider: "missing", Endpoint: "primary"},
		{Provider: "exact-fresh", Endpoint: "primary"},
		{Provider: "exact-edge", Endpoint: "primary"},
		{Provider: "fallback-fresh", Endpoint: "primary"},
		{Provider: "fallback-stale", Endpoint: "primary"},
		{Provider: "stale-exact-provider-fresh", Endpoint: "primary"},
		{Provider: "sibling", Endpoint: "healthy"},
	}
	got := AlivenessDueEndpoints(store, endpoints, now, 0)
	want := []AlivenessEndpoint{
		{Provider: "missing", Endpoint: "primary"},
		{Provider: "exact-edge", Endpoint: "primary"},
		{Provider: "fallback-stale", Endpoint: "primary"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("AlivenessDueEndpoints() = %#v, want %#v", got, want)
	}
}

func TestExtractHostPort(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "scheme-less explicit port", baseURL: "bragi:1234/v1", want: "bragi:1234"},
		{name: "http explicit port", baseURL: "http://127.0.0.1:11434/v1", want: "127.0.0.1:11434"},
		{name: "http default port", baseURL: "http://localhost/v1", want: "localhost:80"},
		{name: "https default port", baseURL: "https://example.com/v1", want: "example.com:443"},
		{name: "bracketed IPv6 explicit port", baseURL: "http://[2001:db8::1]:8080/v1", want: "[2001:db8::1]:8080"},
		{name: "bracketed IPv6 default port", baseURL: "https://[2001:db8::2]/v1", want: "[2001:db8::2]:443"},
		{name: "blank", baseURL: "   ", want: ""},
		{name: "invalid", baseURL: "not a url ://", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ExtractHostPort(test.baseURL); got != test.want {
				t.Fatalf("ExtractHostPort(%q) = %q, want %q", test.baseURL, got, test.want)
			}
		})
	}
}
