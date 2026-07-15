package modelsnapshot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/runtimesignals"
)

func TestAssembleWithOptionsProjectsDiscoveryCatalogAndRuntimeFreshness(t *testing.T) {
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: t.TempDir()}
	discoveredAt := time.Date(2026, 5, 12, 14, 20, 0, 0, time.UTC)
	seedModelSnapshotDiscovery(t, cache, "codex-subscription", discoveredAt, []string{"gpt-5.4"})

	remaining := 14
	runtimeAt := time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC)
	if err := runtimesignals.Write(cache, runtimesignals.Signal{
		Provider:         "codex-subscription",
		Status:           runtimesignals.StatusAvailable,
		QuotaRemaining:   &remaining,
		RecentP50Latency: 110 * time.Millisecond,
		RecordedAt:       runtimeAt,
	}); err != nil {
		t.Fatalf("write runtime signal: %v", err)
	}

	includeByDefault := true
	cfg := &Config{
		Default: "codex-subscription",
		Providers: map[string]ProviderConfig{
			"codex-subscription": {
				Type:                "codex",
				Billing:             string(modelcatalog.BillingModelSubscription),
				IncludeByDefault:    &includeByDefault,
				IncludeByDefaultSet: true,
			},
		},
	}
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	snapshot, err := AssembleWithOptions(context.Background(), cfg, cat, cache, AssembleOptions{Refresh: RefreshNone})
	if err != nil {
		t.Fatalf("AssembleWithOptions: %v", err)
	}
	if len(snapshot.Models) != 1 {
		t.Fatalf("model count = %d, want 1: %#v", len(snapshot.Models), snapshot.Models)
	}

	model := snapshot.Models[0]
	if model.Provider != "codex-subscription" || model.ProviderType != "codex" || model.Harness != "codex" {
		t.Fatalf("provider identity = %q/%q/%q, want codex-subscription/codex/codex", model.Provider, model.ProviderType, model.Harness)
	}
	if model.EndpointName != "codex-subscription" || model.EndpointBaseURL != "" || model.ServerInstance != "codex-subscription" {
		t.Fatalf("endpoint identity = %q/%q/%q, want codex-subscription/empty/codex-subscription", model.EndpointName, model.EndpointBaseURL, model.ServerInstance)
	}
	if model.Billing != modelcatalog.BillingModelSubscription || !model.IncludeByDefault {
		t.Fatalf("billing/include default = %q/%v, want subscription/true", model.Billing, model.IncludeByDefault)
	}
	if model.DiscoveredVia != SourceHarnessPTY || !model.DiscoveredAt.Equal(discoveredAt) {
		t.Fatalf("discovery freshness = %q/%v, want harness_pty/%v", model.DiscoveredVia, model.DiscoveredAt, discoveredAt)
	}
	if model.CostInputPerM <= 0 || model.CostOutputPerM <= 0 {
		t.Fatalf("catalog cost = %v/%v, want positive input/output", model.CostInputPerM, model.CostOutputPerM)
	}
	if model.ActualCashSpend || model.EffectiveCost <= 0 || model.EffectiveCostSource != "subscription_shadow" {
		t.Fatalf("effective cost = actual:%v cost:%v source:%q, want false/positive/subscription_shadow", model.ActualCashSpend, model.EffectiveCost, model.EffectiveCostSource)
	}
	if !model.SupportsTools || model.DeploymentClass != "managed_cloud_frontier" {
		t.Fatalf("catalog support/deployment = %v/%q, want true/managed_cloud_frontier", model.SupportsTools, model.DeploymentClass)
	}
	if model.QuotaRemaining == nil || *model.QuotaRemaining != remaining || model.RecentP50Latency != 110*time.Millisecond {
		t.Fatalf("runtime quota/latency = %#v/%v, want %d/110ms", model.QuotaRemaining, model.RecentP50Latency, remaining)
	}
	if !model.HealthFreshnessAt.Equal(runtimeAt) || model.HealthFreshnessSource != "runtime" ||
		!model.QuotaFreshnessAt.Equal(runtimeAt) || model.QuotaFreshnessSource != "runtime" {
		t.Fatalf("runtime freshness = health:%v/%q quota:%v/%q, want %v/runtime", model.HealthFreshnessAt, model.HealthFreshnessSource, model.QuotaFreshnessAt, model.QuotaFreshnessSource, runtimeAt)
	}
}

func TestAssembleWithOptionsRedactsProviderSecrets(t *testing.T) {
	const (
		apiKeySecret = "snapshot-api-key-secret"
		headerSecret = "snapshot-header-secret"
	)
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: t.TempDir()}
	cfg := &Config{
		Default: "openrouter",
		Providers: map[string]ProviderConfig{
			"openrouter": {
				Type:    "openrouter",
				BaseURL: "http://[::1",
				APIKey:  apiKeySecret,
				Headers: map[string]string{"X-Snapshot-Secret": headerSecret},
			},
		},
	}

	snapshot, err := AssembleWithOptions(context.Background(), cfg, nil, cache, AssembleOptions{Refresh: RefreshForce})
	if err != nil {
		t.Fatalf("AssembleWithOptions: %v", err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, secret := range []string{apiKeySecret, headerSecret} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("snapshot JSON leaked %q: %s", secret, data)
		}
	}

	var sawFailedSource bool
	for source, meta := range snapshot.Sources {
		if meta.Error != "" {
			sawFailedSource = true
		}
		for _, secret := range []string{apiKeySecret, headerSecret} {
			if strings.Contains(meta.Error, secret) {
				t.Fatalf("source %s error leaked %q: %q", source, secret, meta.Error)
			}
		}
	}
	if !sawFailedSource {
		t.Fatalf("forced discovery produced no failed source: %#v", snapshot.Sources)
	}
}

func seedModelSnapshotDiscovery(t *testing.T, cache *discoverycache.Cache, source string, capturedAt time.Time, models []string) {
	t.Helper()
	payload, err := json.Marshal(discoveryPayload{
		CapturedAt: capturedAt,
		Models:     models,
		Source:     "test-fixture",
	})
	if err != nil {
		t.Fatalf("marshal discovery payload: %v", err)
	}
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            source,
		TTL:             discoveryTTLPTY,
		RefreshDeadline: discoveryRefreshDeadlinePTY,
	}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatalf("seed discovery cache: %v", err)
	}
}
