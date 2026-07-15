package fizeau

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
)

// TestServiceConfigToModelSnapshotConfigParity is the one narrow same-package
// seam retained for the root facade's public ServiceConfig-to-internal adapter.
// Snapshot assembly and enrichment mechanics are covered in internal/modelsnapshot.
func TestServiceConfigToModelSnapshotConfigParity(t *testing.T) {
	if got := serviceConfigToModelSnapshotConfig(nil); got != nil {
		t.Fatalf("nil ServiceConfig mapped to %#v, want nil", got)
	}

	headers := map[string]string{
		"Authorization": "Bearer adapter-secret",
		"X-Trace":       "trace-a",
	}
	endpoints := []ServiceProviderEndpoint{
		{Name: "primary", BaseURL: "https://primary.example/v1", ServerInstance: "primary-instance"},
		{Name: "secondary", BaseURL: "https://secondary.example/v1", ServerInstance: "secondary-instance"},
	}
	entry := ServiceProviderEntry{
		Type:                      "openrouter",
		BaseURL:                   "https://default.example/v1",
		ServerInstance:            "default-instance",
		Endpoints:                 endpoints,
		APIKey:                    "adapter-api-key",
		Headers:                   headers,
		Model:                     "gpt-5.4",
		Billing:                   BillingModelSubscription,
		IncludeByDefault:          false,
		IncludeByDefaultSet:       true,
		ContextWindow:             65536,
		ConfigError:               "fixture config error",
		DailyTokenBudget:          123456,
		CreditBalanceThresholdUSD: 17.75,
		CreditProbeTTL:            37 * time.Minute,
	}
	sc := &fakeServiceConfig{
		providers:   map[string]ServiceProviderEntry{"primary": entry},
		names:       []string{"primary", "missing"},
		defaultName: "primary",
	}

	got := serviceConfigToModelSnapshotConfig(sc)
	if got == nil {
		t.Fatal("serviceConfigToModelSnapshotConfig returned nil")
	}
	if got.Default != "primary" {
		t.Fatalf("default provider = %q, want primary", got.Default)
	}
	if len(got.Providers) != 1 {
		t.Fatalf("provider count = %d, want 1: %#v", len(got.Providers), got.Providers)
	}
	provider, ok := got.Providers["primary"]
	if !ok {
		t.Fatalf("primary provider missing: %#v", got.Providers)
	}
	if _, ok := got.Providers["missing"]; ok {
		t.Fatalf("missing provider was projected: %#v", got.Providers["missing"])
	}
	if provider.Type != entry.Type || provider.BaseURL != entry.BaseURL || provider.ServerInstance != entry.ServerInstance ||
		provider.APIKey != entry.APIKey || provider.Model != entry.Model || provider.Billing != string(entry.Billing) ||
		provider.ContextWindow != entry.ContextWindow || provider.ConfigError != entry.ConfigError || provider.DailyTokenBudget != entry.DailyTokenBudget {
		t.Fatalf("scalar projection = %#v, want fields from %#v", provider, entry)
	}
	if len(provider.Endpoints) != len(endpoints) {
		t.Fatalf("endpoint count = %d, want %d", len(provider.Endpoints), len(endpoints))
	}
	for i, want := range endpoints {
		gotEndpoint := provider.Endpoints[i]
		if gotEndpoint.Name != want.Name || gotEndpoint.BaseURL != want.BaseURL || gotEndpoint.ServerInstance != want.ServerInstance {
			t.Fatalf("endpoint %d = %#v, want %#v", i, gotEndpoint, want)
		}
	}
	if provider.IncludeByDefault == nil || *provider.IncludeByDefault || !provider.IncludeByDefaultSet {
		t.Fatalf("include-by-default = %#v/set:%v, want false/set", provider.IncludeByDefault, provider.IncludeByDefaultSet)
	}
	if len(provider.Headers) != len(headers) || provider.Headers["Authorization"] != headers["Authorization"] || provider.Headers["X-Trace"] != headers["X-Trace"] {
		t.Fatalf("headers = %#v, want %#v", provider.Headers, headers)
	}

	headers["Authorization"] = "changed-source"
	if provider.Headers["Authorization"] != "Bearer adapter-secret" {
		t.Fatalf("projected headers alias source: %#v", provider.Headers)
	}
	provider.Headers["X-Trace"] = "changed-projection"
	if headers["X-Trace"] != "trace-a" {
		t.Fatalf("source headers alias projection: %#v", headers)
	}
	endpoints[0].Name = "changed-source"
	if provider.Endpoints[0].Name != "primary" {
		t.Fatalf("projected endpoints alias source: %#v", provider.Endpoints)
	}

	// CreditBalanceThresholdUSD and CreditProbeTTL are routing/quota controls,
	// not model-snapshot inputs, and intentionally have no ProviderConfig fields.
}

func writeSnapshotDiscoveryFixture(t *testing.T, cache *discoverycache.Cache, source string, capturedAt time.Time, models []string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		CapturedAt time.Time `json:"captured_at"`
		Models     []string  `json:"models,omitempty"`
		Source     string    `json:"source,omitempty"`
	}{
		CapturedAt: capturedAt,
		Models:     models,
		Source:     "test-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            source,
		TTL:             time.Hour,
		RefreshDeadline: time.Second,
	}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatal(err)
	}
}

func testDiscoverySourceName(providerName, endpointName, baseURL, serverInstance string) string {
	name := strings.TrimSpace(providerName)
	trimmedEndpoint := strings.TrimSpace(endpointName)
	if trimmedEndpoint == "" || trimmedEndpoint == "default" || trimmedEndpoint == name {
		name = sanitizeDiscoveryName(name)
	} else {
		name = sanitizeDiscoveryName(name + "-" + trimmedEndpoint)
	}
	identity := strings.TrimSpace(baseURL) + "|" + strings.TrimSpace(serverInstance)
	if strings.TrimSpace(identity) != "|" {
		sum := sha256.Sum256([]byte(identity))
		name = sanitizeDiscoveryName(name + "-" + hex.EncodeToString(sum[:4]))
	}
	return name
}

func sanitizeDiscoveryName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "discovery"
	}
	var b strings.Builder
	b.Grow(len(name))
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "discovery"
	}
	return out
}
