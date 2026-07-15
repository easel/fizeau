package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/runtimesignals"
	"github.com/easel/fizeau/internal/serverinstance"
)

// fakeModelsServer returns an httptest.Server that serves the given model IDs from /v1/models.
func fakeModelsServer(models []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			data := make([]map[string]any, len(models))
			for i, m := range models {
				data[i] = map[string]any{"id": m}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		http.NotFound(w, r)
	}))
}

func TestListModels_noServiceConfig(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	_, err := svc.ListModels(context.Background(), ModelFilter{})
	if err == nil {
		t.Fatal("expected error when ServiceConfig is nil")
	}
}

func TestListModels_utilizationProjection(t *testing.T) {
	server := fakeModelsServer([]string{"qwen3.5-27b"})
	defer server.Close()

	config := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: config})
	svc.routeSticky = routehealth.NewStickyState()
	instance := serverinstance.FromBaseURL(server.URL + "/v1")
	svc.routeSticky.RecordUtilization("bragi", instance, "qwen3.5-27b", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(2),
		QueuedRequests: utilization.Int(1),
		Source:         utilization.SourceVLLMMetrics,
		Freshness:      utilization.FreshnessFresh,
	})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 model, got %d: %v", len(infos), modelInfoDebug(infos))
	}
	got := infos[0].Utilization
	if got.Source != string(utilization.SourceVLLMMetrics) || got.Freshness != string(utilization.FreshnessFresh) {
		t.Fatalf("utilization source/freshness = %#v, want fresh vllm metrics", got)
	}
	if got.ActiveRequests == nil || *got.ActiveRequests != 2 {
		t.Errorf("utilization active = %#v, want 2", got.ActiveRequests)
	}
	if got.QueuedRequests == nil || *got.QueuedRequests != 1 {
		t.Errorf("utilization queued = %#v, want 1", got.QueuedRequests)
	}
}
func TestListModelsEffectiveCostAndFreshnessSignals(t *testing.T) {
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"codex-subscription": {Type: "codex", Model: "gpt-5.4", Billing: BillingModelSubscription},
		},
		names:       []string{"codex-subscription"},
		defaultName: "codex-subscription",
	}
	stubSubprocessHarnessModelIDs(t, map[string][]string{
		"codex": {"gpt-5.4"},
	})
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	stubSubscriptionHarnessLookPath(svc, "codex")
	remaining := 14
	if err := runtimesignals.Write(&discoverycache.Cache{Root: cacheDir}, runtimesignals.Signal{
		Provider:         "codex-subscription",
		Status:           runtimesignals.StatusAvailable,
		QuotaRemaining:   &remaining,
		RecentP50Latency: 110 * time.Millisecond,
		RecordedAt:       time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("write runtime signal: %v", err)
	}
	writeSnapshotDiscoveryFixture(t, &discoverycache.Cache{Root: cacheDir}, testDiscoverySourceName("codex-subscription", "codex-subscription", "", ""), time.Date(2026, 5, 12, 14, 20, 0, 0, time.UTC), []string{"gpt-5.4"})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	byID := map[string]ModelInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}
	if len(byID) == 0 {
		t.Fatal("expected at least one codex model")
	}
	subscription := byID["gpt-5.4"]
	if subscription.Billing != BillingModelSubscription {
		t.Fatalf("Billing = %q, want subscription", subscription.Billing)
	}
	if subscription.ActualCashSpend {
		t.Fatalf("ActualCashSpend = true, want false")
	}
	if subscription.EffectiveCost <= 0 {
		t.Fatalf("EffectiveCost = %v, want positive", subscription.EffectiveCost)
	}
	if subscription.EffectiveCostSource != "subscription_shadow" {
		t.Fatalf("EffectiveCostSource = %q, want subscription_shadow", subscription.EffectiveCostSource)
	}
	if subscription.HealthFreshnessSource != "runtime" || subscription.QuotaFreshnessSource != "runtime" {
		t.Fatalf("freshness sources = %q/%q, want runtime/runtime", subscription.HealthFreshnessSource, subscription.QuotaFreshnessSource)
	}
	if subscription.HealthFreshnessAt.IsZero() || subscription.QuotaFreshnessAt.IsZero() || subscription.ModelDiscoveryFreshnessAt.IsZero() {
		t.Fatalf("freshness timestamps missing: %#v", subscription)
	}
	if subscription.ModelDiscoveryFreshnessSource != "harness_pty" {
		t.Fatalf("ModelDiscoveryFreshnessSource = %q, want harness_pty", subscription.ModelDiscoveryFreshnessSource)
	}
	if subscription.SupportsTools != true {
		t.Fatalf("SupportsTools = %v, want true", subscription.SupportsTools)
	}
	if subscription.DeploymentClass != "managed_cloud_frontier" {
		t.Fatalf("DeploymentClass = %q, want managed_cloud_frontier", subscription.DeploymentClass)
	}
}

func modelInfoDebug(infos []ModelInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Provider + ":" + info.ID + "(billing=" + string(info.Billing) + ")"
	}
	return out
}
