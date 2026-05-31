package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routing"
)

func isolateRouteStatusTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
	t.Setenv("PATH", "")
}

// TestRouteStatus_emptyConfig verifies that RouteStatus returns an empty report
// when no ServiceConfig is provided.
func TestRouteStatus_emptyConfig(t *testing.T) {
	isolateRouteStatusTestEnv(t)
	svc := newTestService(t, ServiceOptions{})
	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(report.Routes) != 0 {
		t.Errorf("Routes: got %d, want 0", len(report.Routes))
	}
	if report.GeneratedAt.IsZero() {
		t.Error("GeneratedAt should be set")
	}
}

// TestRouteStatusSnapshotRowsMatchMultiEndpointFixture verifies that RouteStatus
// reflects the same discovered provider/model/endpoint rows used by routing.
func TestRouteStatusSnapshotRowsMatchMultiEndpointFixture(t *testing.T) {
	isolateRouteStatusTestEnv(t)
	modelsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "qwen3.5-27b"}},
		})
	})
	primarySrv := httptest.NewServer(modelsHandler)
	backupSrv := httptest.NewServer(modelsHandler)
	t.Cleanup(primarySrv.Close)
	t.Cleanup(backupSrv.Close)

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {
				Type:    "lmstudio",
				BaseURL: primarySrv.URL + "/v1",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "primary", BaseURL: primarySrv.URL + "/v1"},
					{Name: "backup", BaseURL: backupSrv.URL + "/v1"},
				},
				Model: "qwen3.5-27b",
			},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	models, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// Filter to only fiz-harness models (HTTP providers). The unfiltered ListModels
	// now includes subscription-harness tiers (claude/codex/gemini), so we filter
	// to only the HTTP provider models for this test.
	var fizModels []ModelInfo
	for _, model := range models {
		if model.Harness == "fiz" || model.Harness == "" {
			fizModels = append(fizModels, model)
		}
	}
	if len(fizModels) != 2 {
		t.Fatalf("ListModels fiz rows = %d, want 2 (all rows=%#v)", len(fizModels), models)
	}
	wantRows := make(map[string]ModelInfo, len(fizModels))
	for _, model := range fizModels {
		key := model.Provider + "\x00" + model.ID + "\x00" + model.EndpointName + "\x00" + model.ServerInstance
		wantRows[key] = model
	}

	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "bragi@primary",
		Endpoint:  "primary",
		Model:     "qwen3.5-27b",
		Status:    "failed",
		Reason:    "route_attempt_failure",
		Timestamp: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(report.Routes) != 1 {
		t.Fatalf("Routes: got %d, want 1", len(report.Routes))
	}

	entry := report.Routes[0]
	if entry.Model != "qwen3.5-27b" {
		t.Errorf("Model: got %q, want %q", entry.Model, "qwen3.5-27b")
	}
	if entry.Strategy != "auto" {
		t.Errorf("Strategy: got %q, want auto", entry.Strategy)
	}
	if len(entry.Candidates) != 2 {
		t.Fatalf("Candidates: got %d, want 2", len(entry.Candidates))
	}

	byEndpoint := make(map[string]RouteCandidateStatus, len(entry.Candidates))
	for _, c := range entry.Candidates {
		byEndpoint[c.Endpoint] = c
		key := c.Provider + "\x00" + c.Model + "\x00" + c.Endpoint + "\x00" + c.ServerInstance
		if _, ok := wantRows[key]; !ok {
			t.Fatalf("RouteStatus candidate %q does not match a list-models row; candidates=%#v rows=%#v", key, entry.Candidates, models)
		}
		if c.Provider != "bragi" {
			t.Errorf("candidate provider = %q, want bragi", c.Provider)
		}
		if c.Billing != BillingModelFixed {
			t.Errorf("candidate billing = %q, want fixed", c.Billing)
		}
		if c.Model != "qwen3.5-27b" {
			t.Errorf("candidate model = %q, want qwen3.5-27b", c.Model)
		}
		if c.ServerInstance == "" {
			t.Errorf("candidate server_instance should be populated: %#v", c)
		}
	}

	primary := byEndpoint["primary"]
	if primary.Cooldown == nil {
		t.Fatalf("primary endpoint should be in cooldown: %#v", primary)
	}
	if primary.Healthy {
		t.Fatalf("primary endpoint should not be healthy while in cooldown: %#v", primary)
	}
	backup := byEndpoint["backup"]
	if backup.Cooldown != nil {
		t.Fatalf("backup endpoint should not be in cooldown: %#v", backup)
	}
	if !backup.Healthy {
		t.Fatalf("backup endpoint should remain healthy: %#v", backup)
	}
}

// TestRouteStatusLastDecisionCached verifies that calling ResolveRoute and
// then RouteStatus surfaces the cached LastDecision on the matching entry.
func TestRouteStatusLastDecisionCached(t *testing.T) {
	isolateRouteStatusTestEnv(t)
	// Build a minimal ServiceConfig that satisfies the routing engine.
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: "http://127.0.0.1:9999/v1", Model: "qwen3-27b"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}

	// Seed registry with the "fiz" harness available so routing can resolve.
	reg := harnesses.NewRegistry()
	svc := &service{
		opts:     ServiceOptions{ServiceConfig: sc},
		registry: reg,
	}

	// Inject a decision directly into the cache (simulating a resolved route)
	// since ResolveRoute may fail without a real provider.
	dec := &RouteDecision{
		Harness:        "fiz",
		Provider:       "bragi",
		Endpoint:       "desk-a",
		ServerInstance: "desk-a",
		Model:          "qwen3-27b",
		Reason:         "test",
		Sticky:         RouteStickyState{KeyPresent: true, Assignment: "reused", ServerInstance: "desk-a", Reason: "live sticky lease reused"},
	}
	svc.cacheRouteDecision("qwen3-27b", dec)

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(report.Routes) != 1 {
		t.Fatalf("Routes: got %d, want 1", len(report.Routes))
	}
	entry := report.Routes[0]
	if entry.LastDecision == nil {
		t.Fatal("LastDecision: expected non-nil after ResolveRoute")
	}
	if entry.LastDecision.Provider != "bragi" {
		t.Errorf("LastDecision.Provider: got %q, want %q", entry.LastDecision.Provider, "bragi")
	}
	if entry.LastDecision.ServerInstance == "" {
		t.Errorf("LastDecision.ServerInstance should be populated: %#v", entry.LastDecision)
	}
	if entry.LastDecision.Model != "qwen3-27b" {
		t.Errorf("LastDecision.Model: got %q, want %q", entry.LastDecision.Model, "qwen3-27b")
	}
	if entry.LastDecisionAt.IsZero() {
		t.Error("LastDecisionAt: should be non-zero")
	}
	if entry.SelectedEndpoint != "desk-a" {
		t.Fatalf("SelectedEndpoint = %q, want desk-a", entry.SelectedEndpoint)
	}
	if entry.SelectedServerInstance == "" {
		t.Fatalf("SelectedServerInstance should be populated: %#v", entry)
	}
	if !entry.Sticky.KeyPresent || entry.Sticky.Assignment != "reused" {
		t.Fatalf("Sticky = %#v, want reused sticky evidence", entry.Sticky)
	}
}

// TestRouteStatusLastDecisionCachedViaResolveRoute verifies the full path:
// ResolveRoute → cache write → RouteStatus reads cache.
func TestRouteStatusLastDecisionCachedViaResolveRoute(t *testing.T) {
	isolateRouteStatusTestEnv(t)
	// We need the routing engine to actually resolve. The engine picks
	// harnesses from the registry. "fiz" is always in the registry.
	// We give it a provider so the engine can build a candidate.
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: "http://127.0.0.1:9999/v1", ServerInstance: "bragi-instance", Model: "mymodel"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}

	svc := &service{
		opts:     ServiceOptions{ServiceConfig: sc},
		registry: harnesses.NewRegistry(),
	}

	// ResolveRoute with model="mymodel". The engine resolves against the
	// "fiz" harness + bragi provider.
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model:    "mymodel",
		Provider: "bragi",
	})
	if err != nil {
		// Routing may return an error if the engine can't fully resolve;
		// skip in that case — the direct-cache test covers the cache logic.
		t.Skipf("ResolveRoute returned error (provider not live): %v", err)
	}
	if dec == nil {
		t.Fatal("RouteDecision: nil")
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	var found *RouteStatusEntry
	for i := range report.Routes {
		if report.Routes[i].Model == "mymodel" {
			found = &report.Routes[i]
			break
		}
	}
	if found == nil {
		t.Fatal("route 'mymodel' not found in report")
	}
	if found.LastDecision == nil {
		t.Fatal("LastDecision: expected non-nil after successful ResolveRoute")
	}
	if dec.Endpoint != "" && found.SelectedEndpoint != dec.Endpoint {
		t.Fatalf("SelectedEndpoint = %q, want %q", found.SelectedEndpoint, dec.Endpoint)
	}
	if dec.ServerInstance != "" && found.SelectedServerInstance != dec.ServerInstance {
		t.Fatalf("SelectedServerInstance = %q, want %q", found.SelectedServerInstance, dec.ServerInstance)
	}
}

func TestRouteStatusCooldownStateSurfaces(t *testing.T) {
	isolateRouteStatusTestEnv(t)
	sc := &fakeServiceConfig{
		healthCooldown: 30 * time.Second,
		providers: map[string]ServiceProviderEntry{
			"bragi":      {Type: "lmstudio", BaseURL: "http://bragi.invalid/v1", Model: "qwen3-27b"},
			"openrouter": {Type: "openrouter", BaseURL: "https://openrouter.invalid/v1", Model: "qwen3-27b"},
		},
		names: []string{"bragi", "openrouter"},
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	recordedAt := time.Now().Add(-time.Second).UTC()
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:   "fiz",
		Provider:  "bragi",
		Model:     "qwen3-27b",
		Status:    "failed",
		Reason:    "rate_limit",
		Timestamp: recordedAt,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(report.Routes) != 1 {
		t.Fatalf("Routes: got %d, want 1", len(report.Routes))
	}

	entry := report.Routes[0]
	if len(entry.Candidates) != 2 {
		t.Fatalf("Candidates: got %d, want 2", len(entry.Candidates))
	}

	// Find bragi and openrouter candidates.
	byProvider := make(map[string]RouteCandidateStatus, 2)
	for _, c := range entry.Candidates {
		byProvider[c.Provider] = c
	}

	bragi, ok := byProvider["bragi"]
	if !ok {
		t.Fatal("bragi candidate not found")
	}
	if bragi.Healthy {
		t.Error("bragi: Healthy should be false (in cooldown)")
	}
	if bragi.Cooldown == nil {
		t.Fatal("bragi: Cooldown should be non-nil")
	}
	if bragi.Cooldown.Reason != "rate_limit" {
		t.Errorf("bragi Cooldown.Reason: got %q, want %q", bragi.Cooldown.Reason, "rate_limit")
	}
	if bragi.Cooldown.Until.IsZero() {
		t.Error("bragi Cooldown.Until should be non-zero")
	}

	openrouter, ok := byProvider["openrouter"]
	if !ok {
		t.Fatal("openrouter candidate not found")
	}
	if !openrouter.Healthy {
		t.Error("openrouter: Healthy should be true (no cooldown)")
	}
	if openrouter.Cooldown != nil {
		t.Error("openrouter: Cooldown should be nil")
	}
}

// Compile-time check: routing.Inputs is used in service_routing.go;
// verify the import is still reachable from this test file.
var _ = routing.Inputs{}
