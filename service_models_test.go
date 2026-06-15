package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/provider/utilization"
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

func fakeFailingModelsServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			http.Error(w, "model list unavailable", status)
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

func TestListModels_providerTypesOpenRouterLMStudioOMLXVLLMRapidMLX(t *testing.T) {
	openrouter := fakeModelsServer([]string{"openrouter/model-a"})
	defer openrouter.Close()
	lmstudio := fakeModelsServer([]string{"lmstudio-model-a"})
	defer lmstudio.Close()
	omlx := fakeModelsServer([]string{"omlx-model-a"})
	defer omlx.Close()
	vllm := fakeModelsServer([]string{"vllm-model-a"})
	defer vllm.Close()
	rapidmlx := fakeModelsServer([]string{"rapid-mlx-model-a"})
	defer rapidmlx.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"openrouter": {Type: "openrouter", BaseURL: openrouter.URL + "/api/v1"},
			"studio":     {Type: "lmstudio", BaseURL: lmstudio.URL + "/v1"},
			"vidar-omlx": {Type: "omlx", BaseURL: omlx.URL + "/v1"},
			"sindri":     {Type: "vllm", BaseURL: vllm.URL + "/v1"},
			"grendel":    {Type: "rapid-mlx", BaseURL: rapidmlx.URL + "/v1"},
		},
		names:       []string{"openrouter", "studio", "vidar-omlx", "sindri", "grendel"},
		defaultName: "openrouter",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 5 {
		t.Fatalf("want 5 models, got %d: %v", len(infos), modelIDs(infos))
	}

	wantTypes := map[string]string{
		"openrouter": "openrouter",
		"studio":     "lmstudio",
		"vidar-omlx": "omlx",
		"sindri":     "vllm",
		"grendel":    "rapid-mlx",
	}
	for _, info := range infos {
		if info.ProviderType != wantTypes[info.Provider] {
			t.Errorf("provider %q type=%q, want %q", info.Provider, info.ProviderType, wantTypes[info.Provider])
		}
		if info.EndpointName == "" {
			t.Errorf("provider %q model %q missing EndpointName", info.Provider, info.ID)
		}
		if info.EndpointBaseURL == "" {
			t.Errorf("provider %q model %q missing EndpointBaseURL", info.Provider, info.ID)
		}
		if got := serverinstance.FromBaseURL(info.EndpointBaseURL); info.ServerInstance != got {
			t.Errorf("provider %q model %q server instance = %q, want %q", info.Provider, info.ID, info.ServerInstance, got)
		}
	}
}

func TestListModels_providerTypeLlamaServer(t *testing.T) {
	llama := fakeModelsServer([]string{"llama-3.1"})
	defer llama.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"llama": {Type: "llama-server", BaseURL: llama.URL + "/v1"},
		},
		names:       []string{"llama"},
		defaultName: "llama",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 model, got %d: %v", len(infos), modelIDs(infos))
	}
	info := infos[0]
	if info.ProviderType != "llama-server" {
		t.Fatalf("provider type = %q, want llama-server", info.ProviderType)
	}
	if info.EndpointBaseURL != llama.URL+"/v1" {
		t.Fatalf("endpoint base URL = %q, want %q", info.EndpointBaseURL, llama.URL+"/v1")
	}
}

func TestListModels_endpointPoolReturnsEndpointMetadata(t *testing.T) {
	vidar := fakeModelsServer([]string{"vidar-model"})
	defer vidar.Close()
	eitri := fakeModelsServer([]string{"eitri-model"})
	defer eitri.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"studio": {
				Type:    "lmstudio",
				BaseURL: vidar.URL + "/v1",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "vidar", BaseURL: vidar.URL + "/v1", ServerInstance: "vidar-instance"},
					{Name: "eitri", BaseURL: eitri.URL + "/v1"},
				},
			},
		},
		names:       []string{"studio"},
		defaultName: "studio",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{Provider: "studio"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 endpoint models, got %d: %v", len(infos), modelInfoDebug(infos))
	}

	got := map[string]ModelInfo{}
	for _, info := range infos {
		got[info.ID] = info
	}
	if got["vidar-model"].EndpointName != "vidar" || got["vidar-model"].EndpointBaseURL != vidar.URL+"/v1" {
		t.Errorf("vidar metadata = %#v", got["vidar-model"])
	}
	if got["vidar-model"].ServerInstance != "vidar-instance" {
		t.Errorf("vidar server instance = %q, want explicit override", got["vidar-model"].ServerInstance)
	}
	if got["eitri-model"].EndpointName != "eitri" || got["eitri-model"].EndpointBaseURL != eitri.URL+"/v1" {
		t.Errorf("eitri metadata = %#v", got["eitri-model"])
	}
	if got["eitri-model"].ServerInstance != serverinstance.FromBaseURL(eitri.URL+"/v1") {
		t.Errorf("eitri server instance = %q, want derived host:port", got["eitri-model"].ServerInstance)
	}
}

func TestListModels_endpointPoolSkipsFailingEndpoint(t *testing.T) {
	healthy := fakeModelsServer([]string{"healthy-model"})
	defer healthy.Close()
	failing := fakeFailingModelsServer(http.StatusInternalServerError)
	defer failing.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"studio": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "broken", BaseURL: failing.URL + "/v1"},
					{Name: "healthy", BaseURL: healthy.URL + "/v1"},
				},
			},
		},
		names:       []string{"studio"},
		defaultName: "studio",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{Provider: "studio"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 healthy endpoint model, got %d: %v", len(infos), modelInfoDebug(infos))
	}
	if infos[0].ID != "healthy-model" || infos[0].EndpointName != "healthy" {
		t.Fatalf("unexpected endpoint result: %#v", infos[0])
	}
}

func TestListModels_emptyFilterReturnsAll(t *testing.T) {
	ts1 := fakeModelsServer([]string{"model-a", "model-b"})
	defer ts1.Close()
	ts2 := fakeModelsServer([]string{"model-c"})
	defer ts2.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi":  {Type: "lmstudio", BaseURL: ts1.URL + "/v1"},
			"remote": {Type: "lmstudio", BaseURL: ts2.URL + "/v1"},
		},
		names:       []string{"bragi", "remote"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("want 3 models total, got %d: %v", len(infos), modelIDs(infos))
	}
}

func TestListModels_filtersProvider(t *testing.T) {
	ts1 := fakeModelsServer([]string{"model-a", "model-b"})
	defer ts1.Close()
	ts2 := fakeModelsServer([]string{"model-c"})
	defer ts2.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi":  {Type: "lmstudio", BaseURL: ts1.URL + "/v1"},
			"remote": {Type: "lmstudio", BaseURL: ts2.URL + "/v1"},
		},
		names:       []string{"bragi", "remote"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{Provider: "bragi"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 bragi models, got %d: %v", len(infos), modelIDs(infos))
	}
	for _, info := range infos {
		if info.Provider != "bragi" {
			t.Errorf("model %q has Provider=%q, want bragi", info.ID, info.Provider)
		}
	}
}

func TestListModels_isDefaultMatchesConfig(t *testing.T) {
	ts := fakeModelsServer([]string{"model-a", "model-b", "default-model"})
	defer ts.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1", Model: "default-model"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	var defaultCount int
	for _, info := range infos {
		if info.IsDefault {
			defaultCount++
			if info.ID != "default-model" {
				t.Errorf("IsDefault=true for wrong model %q", info.ID)
			}
		}
	}
	if defaultCount != 1 {
		t.Errorf("want exactly 1 IsDefault model, got %d", defaultCount)
	}
}

func TestListModels_billingSetForProviderModels(t *testing.T) {
	ts := fakeModelsServer([]string{"qwen3.5-27b", "unknown-model-xyz"})
	defer ts.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	for _, info := range infos {
		if info.Billing != BillingModelFixed {
			t.Errorf("model %q Billing = %q, want fixed", info.ID, info.Billing)
		}
	}
}

func TestListModels_catalogMetadataForKnownAndUnknownProviderModels(t *testing.T) {
	ts := fakeModelsServer([]string{"qwen3.5-27b", "unknown-model-xyz"})
	defer ts.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	instance := serverinstance.FromBaseURL(ts.URL + "/v1")
	svc.routeUtilizationStore().Record("bragi", instance, "qwen3.5-27b", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(2),
		QueuedRequests: utilization.Int(1),
		Source:         utilization.SourceVLLMMetrics,
		Freshness:      utilization.FreshnessFresh,
	})
	if sample, ok := svc.routeUtilizationStore().Sample("bragi", instance, "qwen3.5-27b"); !ok || sample.Source != utilization.SourceVLLMMetrics {
		t.Fatalf("route utilization sample not recorded: ok=%v sample=%#v", ok, sample)
	}

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 models, got %d: %v", len(infos), modelInfoDebug(infos))
	}

	byID := map[string]ModelInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}

	known := byID["qwen3.5-27b"]
	if known.Billing != BillingModelFixed {
		t.Fatalf("known model Billing = %q, want fixed: %#v", known.Billing, known)
	}
	if known.Power != 5 || !known.AutoRoutable || known.ExactPinOnly {
		t.Errorf("known model eligibility = power %d auto %v exact %v, want power 5 auto true exact false", known.Power, known.AutoRoutable, known.ExactPinOnly)
	}
	if known.Cost.InputPerMTok != 0.10 || known.Cost.OutputPerMTok != 0.30 {
		t.Errorf("known model cost = %#v, want 0.10/0.30", known.Cost)
	}
	if known.ContextLength != 262144 {
		t.Errorf("known model context = %d, want 262144", known.ContextLength)
	}
	if known.ContextSource != ContextSourceCatalog {
		t.Errorf("known model context source = %q, want %q", known.ContextSource, ContextSourceCatalog)
	}
	if known.PerfSignal.SWEBenchVerified != 59.0 {
		t.Errorf("known model SWE = %.1f, want 59.0", known.PerfSignal.SWEBenchVerified)
	}
	if known.Utilization.Source != string(utilization.SourceVLLMMetrics) || known.Utilization.Freshness != string(utilization.FreshnessFresh) {
		t.Errorf("known model utilization = %#v, want fresh vllm metrics", known.Utilization)
	}
	if known.Utilization.ActiveRequests == nil || *known.Utilization.ActiveRequests != 2 {
		t.Errorf("known model utilization active = %#v, want 2", known.Utilization.ActiveRequests)
	}
	if known.Utilization.QueuedRequests == nil || *known.Utilization.QueuedRequests != 1 {
		t.Errorf("known model utilization queued = %#v, want 1", known.Utilization.QueuedRequests)
	}
	if known.EndpointName == "" || known.EndpointBaseURL == "" {
		t.Errorf("known model endpoint identity missing: %#v", known)
	}

	unknown := byID["unknown-model-xyz"]
	if unknown.Billing != BillingModelFixed {
		t.Errorf("unknown model Billing = %q, want fixed", unknown.Billing)
	}
	if unknown.Power != 0 || unknown.AutoRoutable || unknown.ExactPinOnly {
		t.Errorf("unknown model eligibility = power %d auto %v exact %v, want zero/false/false", unknown.Power, unknown.AutoRoutable, unknown.ExactPinOnly)
	}
	if unknown.EndpointName == "" || unknown.EndpointBaseURL == "" {
		t.Errorf("unknown model endpoint identity missing: %#v", unknown)
	}
	if unknown.ContextLength != compaction.DefaultContextWindow {
		t.Errorf("unknown model context = %d, want default %d", unknown.ContextLength, compaction.DefaultContextWindow)
	}
	if unknown.ContextSource != ContextSourceDefault {
		t.Errorf("unknown model context source = %q, want %q", unknown.ContextSource, ContextSourceDefault)
	}
}

func TestListModelsEffectiveCostAndFreshnessSignals(t *testing.T) {
	cacheDir := t.TempDir()
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

func TestListModels_contextSourcePrecedence(t *testing.T) {
	t.Run("provider config override wins", func(t *testing.T) {
		ts := fakeModelsServer([]string{"qwen3.5-27b"})
		defer ts.Close()
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1", Model: "qwen3.5-27b", ContextWindow: 4096},
			},
			names:       []string{"bragi"},
			defaultName: "bragi",
		}
		svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
		infos, err := svc.ListModels(context.Background(), ModelFilter{})
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("want 1 model, got %d", len(infos))
		}
		if infos[0].ContextLength != 4096 || infos[0].ContextSource != ContextSourceProviderConfig {
			t.Fatalf("provider config context = %d/%q, want 4096/%q", infos[0].ContextLength, infos[0].ContextSource, ContextSourceProviderConfig)
		}
	})

	t.Run("default falls back when catalog missing", func(t *testing.T) {
		ts := fakeModelsServer([]string{"unknown-model-xyz"})
		defer ts.Close()
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1"},
			},
			names:       []string{"bragi"},
			defaultName: "bragi",
		}
		svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
		infos, err := svc.ListModels(context.Background(), ModelFilter{})
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("want 1 model, got %d", len(infos))
		}
		if infos[0].ContextLength != compaction.DefaultContextWindow || infos[0].ContextSource != ContextSourceDefault {
			t.Fatalf("default context = %d/%q, want %d/%q", infos[0].ContextLength, infos[0].ContextSource, compaction.DefaultContextWindow, ContextSourceDefault)
		}
	})
}

func TestListModels_catalogMetadataForSubprocessHarnessModels(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})

	infos, err := svc.ListModels(context.Background(), ModelFilter{Harness: "claude"})
	if err != nil {
		t.Fatalf("ListModels harness=claude: %v", err)
	}

	var opus ModelInfo
	for _, info := range infos {
		if info.ID == "opus-4.7" {
			opus = info
			break
		}
	}
	if opus.ID == "" {
		t.Fatalf("want opus-4.7 in claude harness models, got %v", modelInfoDebug(infos))
	}
	if opus.Billing != BillingModelSubscription {
		t.Fatalf("opus Billing = %q, want subscription: %#v", opus.Billing, opus)
	}
	if opus.Power != 10 || !opus.AutoRoutable || opus.ExactPinOnly {
		t.Errorf("opus eligibility = power %d auto %v exact %v, want power 10 auto true exact false", opus.Power, opus.AutoRoutable, opus.ExactPinOnly)
	}
	if opus.Cost.InputPerMTok != 15.00 || opus.Cost.OutputPerMTok != 75.00 {
		t.Errorf("opus cost = %#v, want 15/75", opus.Cost)
	}
	if opus.ContextLength != 1000000 {
		t.Errorf("opus context = %d, want 1000000", opus.ContextLength)
	}
	if opus.EndpointName != "claude" {
		t.Errorf("opus endpoint name = %q, want claude", opus.EndpointName)
	}
}

func TestListModels_harnessFilter(t *testing.T) {
	ts := fakeModelsServer([]string{"model-a"})
	defer ts.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	// Agent harness should return results.
	infos, err := svc.ListModels(context.Background(), ModelFilter{Harness: "fiz"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 model for harness=fiz, got %d", len(infos))
	}

	// Claude harness should return the documented CLI/TUI model surface.
	infos2, err := svc.ListModels(context.Background(), ModelFilter{Harness: "claude"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos2) == 0 {
		t.Fatal("want harness-native models for harness=claude")
	}
	claudeIDs := modelIDs(infos2)
	if !containsModelString(claudeIDs, "claude:opus") || !containsModelString(claudeIDs, "claude:opus-4.7") {
		t.Fatalf("want claude alias and discovered version in model list, got %v", claudeIDs)
	}
	for _, info := range infos2 {
		if info.Provider != "claude" || info.Harness != "claude" || !info.Available {
			t.Errorf("unexpected claude model info: %#v", info)
		}
	}

	infosCodex, err := svc.ListModels(context.Background(), ModelFilter{Harness: "codex"})
	if err != nil {
		t.Fatalf("ListModels harness=codex: %v", err)
	}
	codexIDs := modelIDs(infosCodex)
	if !containsModelString(codexIDs, "codex:gpt") || !containsModelString(codexIDs, "codex:gpt-5.4") {
		t.Fatalf("want codex generic alias and discovered version in model list, got %v", codexIDs)
	}

	// Promoted subprocess harnesses expose their documented CLI model surface.
	for _, harness := range []string{"opencode", "pi"} {
		infos, err := svc.ListModels(context.Background(), ModelFilter{Harness: harness})
		if err != nil {
			t.Fatalf("ListModels harness=%s: %v", harness, err)
		}
		if len(infos) == 0 {
			t.Fatalf("want harness-native models for harness=%s", harness)
		}
		for _, info := range infos {
			if info.Provider != harness || info.Harness != harness || !info.Available {
				t.Errorf("unexpected %s model info: %#v", harness, info)
			}
		}
	}

	infos3, err := svc.ListModels(context.Background(), ModelFilter{Harness: "gemini"})
	if err != nil {
		t.Fatalf("ListModels harness=gemini: %v", err)
	}
	if len(infos3) == 0 {
		t.Fatal("want harness-native models for promoted harness=gemini")
	}
	if got, want := infos3[0].ID, "gemini-2.5-pro"; got != want {
		t.Fatalf("first gemini model: got %q, want %q (all: %v)", got, want, modelInfoDebug(infos3))
	}
	for _, info := range infos3 {
		if info.Provider != "gemini" || info.Harness != "gemini" || !info.Available {
			t.Errorf("unexpected gemini model info: %#v", info)
		}
		if info.Billing != BillingModelSubscription {
			t.Errorf("gemini model Billing = %q, want subscription: %#v", info.Billing, info)
		}
	}
}

// stubSubscriptionHarnessLookPath makes the given binaries discoverable via the
// registry's LookPath seam and reports every other binary as missing, so the
// test is hermetic regardless of which CLIs are installed on the host.
func stubSubscriptionHarnessLookPath(svc *service, available ...string) {
	set := make(map[string]struct{}, len(available))
	for _, name := range available {
		set[name] = struct{}{}
	}
	svc.registry.LookPath = func(file string) (string, error) {
		if _, ok := set[file]; ok {
			return "/usr/local/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
}

// stubSubprocessHarnessModelIDs replaces the package-level model-ID resolver
// with a hermetic map for the duration of the test, so the subscription-tier
// surface does not depend on launching real CLIs via PTY (which requires an
// interactive TTY unavailable in CI/sandboxes).
func stubSubprocessHarnessModelIDs(t *testing.T, byHarness map[string][]string) {
	t.Helper()
	prev := subprocessHarnessModelIDs
	t.Cleanup(func() { subprocessHarnessModelIDs = prev })
	subprocessHarnessModelIDs = func(name string, _ harnesses.HarnessConfig) []string {
		return byHarness[name]
	}
}

func TestListModelsUnfilteredIncludesAvailableSubscriptionTiers(t *testing.T) {
	// ServiceConfig with NO configured providers: the provider-backed snapshot
	// is empty, exercising the regression where unfiltered ListModels returned
	// zero models even though subscription CLIs are on PATH.
	sc := &fakeServiceConfig{
		providers:   map[string]ServiceProviderEntry{},
		names:       nil,
		defaultName: "",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	stubSubscriptionHarnessLookPath(svc, "claude", "codex")
	stubSubprocessHarnessModelIDs(t, map[string][]string{
		"claude": {"opus-4.7", "sonnet-4.6"},
		"codex":  {"gpt-5.4"},
	})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels unfiltered: %v", err)
	}
	if len(infos) == 0 {
		t.Fatalf("want subscription-harness tiers in unfiltered inventory, got 0")
	}

	byHarness := map[string]int{}
	for _, info := range infos {
		if info.Harness == "" {
			t.Errorf("model %q missing Harness", info.ID)
		}
		byHarness[info.Harness]++
	}
	if byHarness["claude"] == 0 {
		t.Errorf("want claude subscription tiers in unfiltered inventory, got none: %v", modelInfoDebug(infos))
	}
	if byHarness["codex"] == 0 {
		t.Errorf("want codex subscription tiers in unfiltered inventory, got none: %v", modelInfoDebug(infos))
	}

	// Power metadata must be populated so the caller's escalation ladder is
	// non-empty (the original no-viable-floor failure mode).
	var sawPower bool
	for _, info := range infos {
		if info.Harness == "claude" && info.Billing != BillingModelSubscription {
			t.Errorf("claude model %q Billing = %q, want subscription", info.ID, info.Billing)
		}
		if info.Power > 0 {
			sawPower = true
		}
	}
	if !sawPower {
		t.Errorf("want at least one subscription tier with Power metadata populated, got %v", modelInfoDebug(infos))
	}
}

func TestListModelsFilteredByHarnessUnchanged(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	stubSubscriptionHarnessLookPath(svc, "claude", "codex", "gemini")
	stubSubprocessHarnessModelIDs(t, map[string][]string{
		"claude": {"opus-4.7", "sonnet-4.6"},
		"codex":  {"gpt-5.4"},
		"gemini": {"gemini-2.5-pro"},
	})

	for _, harness := range []string{"claude", "codex", "gemini"} {
		filtered, err := svc.ListModels(context.Background(), ModelFilter{Harness: harness})
		if err != nil {
			t.Fatalf("ListModels harness=%s: %v", harness, err)
		}
		// The shared tier-building helper must produce byte-for-byte identical
		// output to the harness-pinned path.
		cfg, ok := svc.registry.Get(harness)
		if !ok {
			t.Fatalf("registry missing %s", harness)
		}
		cat, _ := modelcatalog.Default()
		want := svc.subscriptionHarnessTierModels(harness, cfg, cat)
		if !reflect.DeepEqual(filtered, want) {
			t.Fatalf("harness=%s filtered output changed:\n got %#v\nwant %#v", harness, filtered, want)
		}
		if len(filtered) == 0 {
			t.Fatalf("want harness-native models for harness=%s", harness)
		}
		for _, info := range filtered {
			if info.Provider != harness || info.Harness != harness {
				t.Errorf("harness=%s unexpected model: %#v", harness, info)
			}
		}
	}
}

func TestListModels_rankPosition(t *testing.T) {
	ts := fakeModelsServer([]string{"first-model", "second-model", "third-model"})
	defer ts.Close()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: ts.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("want 3 models, got %d", len(infos))
	}
	for _, info := range infos {
		if info.RankPosition < 0 {
			t.Errorf("model %q has RankPosition=%d, want >= 0", info.ID, info.RankPosition)
		}
	}
}

// helpers

func modelIDs(infos []ModelInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Provider + ":" + info.ID
	}
	return out
}

func containsModelString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func modelInfoDebug(infos []ModelInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Provider + ":" + info.ID + "(billing=" + string(info.Billing) + ")"
	}
	return out
}
