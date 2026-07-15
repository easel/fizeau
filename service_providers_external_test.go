package fizeau_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

// providerFacadeConfig is a public ServiceConfig test double. Provider
// inventory mechanics are covered by internal/serviceimpl; this fixture keeps
// the root suite at the same boundary used by Fizeau consumers.
type providerFacadeConfig struct {
	providers   map[string]fizeau.ServiceProviderEntry
	names       []string
	defaultName string
}

func (f *providerFacadeConfig) ProviderNames() []string { return f.names }
func (f *providerFacadeConfig) DefaultProviderName() string {
	return f.defaultName
}
func (f *providerFacadeConfig) Provider(name string) (fizeau.ServiceProviderEntry, bool) {
	entry, ok := f.providers[name]
	return entry, ok
}
func (f *providerFacadeConfig) HealthCooldown() time.Duration { return 0 }
func (f *providerFacadeConfig) WorkDir() string               { return "" }
func (f *providerFacadeConfig) SessionLogDir() string         { return "" }

func newProviderFacade(t *testing.T, config *providerFacadeConfig) fizeau.FizeauService {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       config,
		QuotaRefreshContext: ctx,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func closedProviderBaseURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	baseURL := server.URL + "/v1"
	server.Close()
	return baseURL
}

// @covers US-003-AC2
func TestListProviders_Connected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "model-a"},
					{"id": "model-b"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"local": {Type: "lmstudio", BaseURL: ts.URL + "/v1", Model: "model-a"},
		},
		names:       []string{"local"},
		defaultName: "local",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	info := infos[0]
	if info.Name != "local" {
		t.Errorf("Name: got %q, want %q", info.Name, "local")
	}
	if info.Status != "connected" {
		t.Errorf("Status: got %q, want %q", info.Status, "connected")
	}
	if info.ModelCount != 2 {
		t.Errorf("ModelCount: got %d, want 2", info.ModelCount)
	}
	if !info.IsDefault {
		t.Error("IsDefault should be true for the default provider")
	}
	if info.DefaultModel != "model-a" {
		t.Errorf("DefaultModel: got %q, want %q", info.DefaultModel, "model-a")
	}
	if info.Type != "lmstudio" {
		t.Errorf("Type: got %q, want %q", info.Type, "lmstudio")
	}
	if info.Billing != fizeau.BillingModelFixed {
		t.Errorf("Billing: got %q, want fixed", info.Billing)
	}
	if !info.IncludeByDefault {
		t.Error("IncludeByDefault should be true for fixed-billing providers")
	}
	for _, capability := range []string{"tool_use", "streaming", "json_mode"} {
		if !slices.Contains(info.Capabilities, capability) {
			t.Errorf("Capabilities should contain %q: %#v", capability, info.Capabilities)
		}
	}
	if slices.Contains(info.Capabilities, "reasoning_control") {
		t.Fatalf("lmstudio capabilities must not claim reasoning_control: %#v", info.Capabilities)
	}
	if len(info.EndpointStatus) != 1 {
		t.Fatalf("EndpointStatus length: got %d, want 1", len(info.EndpointStatus))
	}
	if info.EndpointStatus[0].Status != "connected" || info.EndpointStatus[0].ModelCount != 2 || info.EndpointStatus[0].LastSuccessAt.IsZero() {
		t.Fatalf("EndpointStatus[0]: %#v", info.EndpointStatus[0])
	}
	if info.LastError != nil {
		t.Fatalf("LastError: got %#v, want nil", info.LastError)
	}
}

func TestListProviders_InvalidProviderConfigReportedWithoutProbe(t *testing.T) {
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"broken": {
				Type:        "not-a-provider",
				BaseURL:     "http://broken.invalid/v1",
				ConfigError: `unknown type "not-a-provider"`,
			},
		},
		names:       []string{"broken"},
		defaultName: "broken",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	info := infos[0]
	if info.Status != "error: invalid provider config" {
		t.Fatalf("Status = %q, want invalid provider config", info.Status)
	}
	if info.LastError == nil || info.LastError.Detail != `unknown type "not-a-provider"` {
		t.Fatalf("LastError = %#v, want config detail", info.LastError)
	}
	if len(info.EndpointStatus) != 1 || info.EndpointStatus[0].LastError == nil {
		t.Fatalf("EndpointStatus = %#v, want endpoint-level config error", info.EndpointStatus)
	}
}

func TestListProviders_LlamaServerConnected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "llama-3.1"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"llama": {Type: "llama-server", BaseURL: ts.URL + "/v1"},
		},
		names:       []string{"llama"},
		defaultName: "llama",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	if infos[0].Type != "llama-server" {
		t.Fatalf("provider type = %q, want llama-server", infos[0].Type)
	}
	if infos[0].Status != "connected" {
		t.Fatalf("provider status = %q, want connected", infos[0].Status)
	}
}

func TestListProviders_OMLXAdvertisesReasoningControl(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "Qwen3.6-27B-MLX-8bit"}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"vidar-omlx": {Type: "omlx", BaseURL: ts.URL + "/v1", Model: "Qwen3.6-27B-MLX-8bit"},
		},
		names:       []string{"vidar-omlx"},
		defaultName: "vidar-omlx",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	if !slices.Contains(infos[0].Capabilities, "reasoning_control") {
		t.Fatalf("omlx capabilities must include reasoning_control: %#v", infos[0].Capabilities)
	}
}

func TestListProviders_Unreachable(t *testing.T) {
	baseURL := closedProviderBaseURL(t)
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"remote": {Type: "lmstudio", BaseURL: baseURL},
		},
		names:       []string{"remote"},
		defaultName: "remote",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	if infos[0].Status != "unreachable" {
		t.Errorf("Status: got %q, want %q", infos[0].Status, "unreachable")
	}
	if infos[0].LastError == nil || infos[0].LastError.Type != "unavailable" {
		t.Fatalf("LastError: got %#v, want unavailable", infos[0].LastError)
	}
	if len(infos[0].EndpointStatus) == 0 || infos[0].EndpointStatus[0].Status != "unreachable" {
		t.Fatalf("EndpointStatus: %#v", infos[0].EndpointStatus)
	}
}

func TestProviderStatus_EndpointDownSurfaced(t *testing.T) {
	deadBaseURL := closedProviderBaseURL(t)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" && r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "healthy-model"}},
		})
	}))
	defer healthy.Close()

	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"omlx": {
				Type: "omlx",
				Endpoints: []fizeau.ServiceProviderEndpoint{
					{Name: "dead", BaseURL: deadBaseURL},
					{Name: "healthy", BaseURL: healthy.URL + "/v1"},
				},
			},
		},
		names:       []string{"omlx"},
		defaultName: "omlx",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	info := infos[0]
	if info.Status != "connected" {
		t.Fatalf("Status: got %q, want connected", info.Status)
	}
	if info.ModelCount != 1 {
		t.Fatalf("ModelCount: got %d, want 1", info.ModelCount)
	}
	if len(info.EndpointStatus) != 2 {
		t.Fatalf("EndpointStatus length: got %d, want 2", len(info.EndpointStatus))
	}
	byName := map[string]fizeau.EndpointStatus{}
	for _, status := range info.EndpointStatus {
		byName[status.Name] = status
	}
	dead := byName["dead"]
	if dead.Status != "unreachable" {
		t.Fatalf("dead endpoint status: got %#v", dead)
	}
	if dead.LastError == nil || strings.TrimSpace(dead.LastError.Detail) == "" || dead.LastError.Detail == "unreachable" {
		t.Fatalf("dead endpoint last error: got %#v, want concrete reachability detail", dead.LastError)
	}
	healthyStatus := byName["healthy"]
	if healthyStatus.Status != "connected" || healthyStatus.ModelCount != 1 || healthyStatus.LastSuccessAt.IsZero() {
		t.Fatalf("healthy endpoint status: %#v", healthyStatus)
	}
}

func TestListProviders_Anthropic(t *testing.T) {
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"claude-api": {Type: "anthropic", APIKey: "sk-test"},
		},
		names:       []string{"claude-api"},
		defaultName: "claude-api",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 provider, got %d", len(infos))
	}
	info := infos[0]
	if info.Status != "connected" {
		t.Errorf("anthropic with key: Status got %q, want %q", info.Status, "connected")
	}
	if info.Type != "anthropic" {
		t.Errorf("Type: got %q, want %q", info.Type, "anthropic")
	}
	if info.Billing != fizeau.BillingModelPerToken {
		t.Errorf("Billing: got %q, want per_token", info.Billing)
	}
	if info.IncludeByDefault {
		t.Error("IncludeByDefault should be false for per-token providers by default")
	}
}

func TestListProviders_AnthropicNoKey(t *testing.T) {
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"claude-api": {Type: "anthropic"},
		},
		names:       []string{"claude-api"},
		defaultName: "claude-api",
	}
	infos, err := newProviderFacade(t, config).ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if infos[0].Status != "error: api_key not configured" {
		t.Errorf("unexpected status: %s", infos[0].Status)
	}
	if !infos[0].Auth.Unauthenticated {
		t.Fatalf("Auth: got %#v, want unauthenticated", infos[0].Auth)
	}
	if infos[0].LastError == nil || infos[0].LastError.Type != "unauthenticated" {
		t.Fatalf("LastError: got %#v, want unauthenticated", infos[0].LastError)
	}
}

func TestHealthCheck_Provider_Connected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer ts.Close()

	config := &providerFacadeConfig{providers: map[string]fizeau.ServiceProviderEntry{
		"local": {Type: "lmstudio", BaseURL: ts.URL + "/v1"},
	}}
	if err := newProviderFacade(t, config).HealthCheck(context.Background(), fizeau.HealthTarget{Type: "provider", Name: "local"}); err != nil {
		t.Errorf("HealthCheck connected provider: unexpected error: %v", err)
	}
}

func TestHealthCheckProviders_UnreachableIncludesReason(t *testing.T) {
	baseURL := closedProviderBaseURL(t)
	config := &providerFacadeConfig{providers: map[string]fizeau.ServiceProviderEntry{
		"dead": {Type: "lmstudio", BaseURL: baseURL},
	}}
	err := newProviderFacade(t, config).HealthCheck(context.Background(), fizeau.HealthTarget{Type: "provider", Name: "dead"})
	if err == nil {
		t.Fatal("expected error for unreachable provider")
	}
	const prefix = `service: provider "dead": `
	detail := strings.TrimSpace(strings.TrimPrefix(err.Error(), prefix))
	if !strings.HasPrefix(err.Error(), prefix) || detail == "" || detail == "unreachable" {
		t.Fatalf("expected provider-scoped concrete reachability detail, got %v", err)
	}
}

func TestHealthCheck_Provider_NotFound(t *testing.T) {
	config := &providerFacadeConfig{providers: map[string]fizeau.ServiceProviderEntry{}}
	err := newProviderFacade(t, config).HealthCheck(context.Background(), fizeau.HealthTarget{Type: "provider", Name: "missing"})
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestHealthCheck_Harness_Available(t *testing.T) {
	config := &providerFacadeConfig{providers: map[string]fizeau.ServiceProviderEntry{}}
	// "fiz" is always available because it is embedded.
	if err := newProviderFacade(t, config).HealthCheck(context.Background(), fizeau.HealthTarget{Type: "harness", Name: "fiz"}); err != nil {
		t.Errorf("HealthCheck embedded harness: unexpected error: %v", err)
	}
}

func TestHealthCheck_Harness_NotRegistered(t *testing.T) {
	config := &providerFacadeConfig{providers: map[string]fizeau.ServiceProviderEntry{}}
	err := newProviderFacade(t, config).HealthCheck(context.Background(), fizeau.HealthTarget{Type: "harness", Name: "nonexistent-harness-xyz"})
	if err == nil {
		t.Fatal("expected error for unregistered harness")
	}
}

func TestHealthCheck_InvalidType(t *testing.T) {
	config := &providerFacadeConfig{providers: map[string]fizeau.ServiceProviderEntry{}}
	err := newProviderFacade(t, config).HealthCheck(context.Background(), fizeau.HealthTarget{Type: "invalid", Name: "x"})
	if err == nil {
		t.Fatal("expected error for invalid HealthTarget.Type")
	}
}
