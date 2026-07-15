package fizeau_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fizeau "github.com/easel/fizeau"
)

const defaultModelContextWindow = 131072

func externalModelsServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			data := make([]map[string]any, len(models))
			for i, model := range models {
				data[i] = map[string]any{"id": model}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func externalFailingModelsServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			http.Error(w, "model list unavailable", status)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func newProviderModelFacade(t *testing.T, config *providerFacadeConfig) fizeau.FizeauService {
	t.Helper()
	// ListModels appends subscription tiers that are available on PATH. Keep
	// provider-facade tests independent of developer-machine CLI installs.
	t.Setenv("PATH", "")
	t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
	return newProviderFacade(t, config)
}

func externalServerInstance(t *testing.T, baseURL string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL %q: %v", baseURL, err)
	}
	return parsed.Host
}

func externalModelIDs(infos []fizeau.ModelInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Provider + ":" + info.ID
	}
	return out
}

func externalModelInfoDebug(infos []fizeau.ModelInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Provider + ":" + info.ID + "(billing=" + string(info.Billing) + ")"
	}
	return out
}

func TestListModels_providerTypesOpenRouterLMStudioOMLXVLLMRapidMLX(t *testing.T) {
	openrouter := externalModelsServer(t, []string{"openrouter/model-a"})
	lmstudio := externalModelsServer(t, []string{"lmstudio-model-a"})
	omlx := externalModelsServer(t, []string{"omlx-model-a"})
	vllm := externalModelsServer(t, []string{"vllm-model-a"})
	rapidmlx := externalModelsServer(t, []string{"rapid-mlx-model-a"})

	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"openrouter": {Type: "openrouter", BaseURL: openrouter.URL + "/api/v1"},
			"studio":     {Type: "lmstudio", BaseURL: lmstudio.URL + "/v1"},
			"vidar-omlx": {Type: "omlx", BaseURL: omlx.URL + "/v1"},
			"sindri":     {Type: "vllm", BaseURL: vllm.URL + "/v1"},
			"grendel":    {Type: "rapid-mlx", BaseURL: rapidmlx.URL + "/v1"},
		},
		names:       []string{"openrouter", "studio", "vidar-omlx", "sindri", "grendel"},
		defaultName: "openrouter",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 5 {
		t.Fatalf("want 5 models, got %d: %v", len(infos), externalModelIDs(infos))
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
		if got := externalServerInstance(t, info.EndpointBaseURL); info.ServerInstance != got {
			t.Errorf("provider %q model %q server instance = %q, want %q", info.Provider, info.ID, info.ServerInstance, got)
		}
	}
}

func TestListModels_providerTypeLlamaServer(t *testing.T) {
	llama := externalModelsServer(t, []string{"llama-3.1"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"llama": {Type: "llama-server", BaseURL: llama.URL + "/v1"},
		},
		names:       []string{"llama"},
		defaultName: "llama",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 model, got %d: %v", len(infos), externalModelIDs(infos))
	}
	if infos[0].ProviderType != "llama-server" {
		t.Fatalf("provider type = %q, want llama-server", infos[0].ProviderType)
	}
	if infos[0].EndpointBaseURL != llama.URL+"/v1" {
		t.Fatalf("endpoint base URL = %q, want %q", infos[0].EndpointBaseURL, llama.URL+"/v1")
	}
}

func TestListModels_endpointPoolReturnsEndpointMetadata(t *testing.T) {
	vidar := externalModelsServer(t, []string{"vidar-model"})
	eitri := externalModelsServer(t, []string{"eitri-model"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"studio": {
				Type:    "lmstudio",
				BaseURL: vidar.URL + "/v1",
				Endpoints: []fizeau.ServiceProviderEndpoint{
					{Name: "vidar", BaseURL: vidar.URL + "/v1", ServerInstance: "vidar-instance"},
					{Name: "eitri", BaseURL: eitri.URL + "/v1"},
				},
			},
		},
		names:       []string{"studio"},
		defaultName: "studio",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{Provider: "studio"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 endpoint models, got %d: %v", len(infos), externalModelInfoDebug(infos))
	}

	got := map[string]fizeau.ModelInfo{}
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
	if want := externalServerInstance(t, eitri.URL+"/v1"); got["eitri-model"].ServerInstance != want {
		t.Errorf("eitri server instance = %q, want %q", got["eitri-model"].ServerInstance, want)
	}
}

func TestListModels_endpointPoolSkipsFailingEndpoint(t *testing.T) {
	healthy := externalModelsServer(t, []string{"healthy-model"})
	failing := externalFailingModelsServer(t, http.StatusInternalServerError)
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"studio": {
				Type: "lmstudio",
				Endpoints: []fizeau.ServiceProviderEndpoint{
					{Name: "broken", BaseURL: failing.URL + "/v1"},
					{Name: "healthy", BaseURL: healthy.URL + "/v1"},
				},
			},
		},
		names:       []string{"studio"},
		defaultName: "studio",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{Provider: "studio"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 healthy endpoint model, got %d: %v", len(infos), externalModelInfoDebug(infos))
	}
	if infos[0].ID != "healthy-model" || infos[0].EndpointName != "healthy" {
		t.Fatalf("unexpected endpoint result: %#v", infos[0])
	}
}

func TestListModels_emptyFilterReturnsAll(t *testing.T) {
	first := externalModelsServer(t, []string{"model-a", "model-b"})
	second := externalModelsServer(t, []string{"model-c"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi":  {Type: "lmstudio", BaseURL: first.URL + "/v1"},
			"remote": {Type: "lmstudio", BaseURL: second.URL + "/v1"},
		},
		names:       []string{"bragi", "remote"},
		defaultName: "bragi",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("want 3 models total, got %d: %v", len(infos), externalModelIDs(infos))
	}
}

func TestListModels_filtersProvider(t *testing.T) {
	first := externalModelsServer(t, []string{"model-a", "model-b"})
	second := externalModelsServer(t, []string{"model-c"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi":  {Type: "lmstudio", BaseURL: first.URL + "/v1"},
			"remote": {Type: "lmstudio", BaseURL: second.URL + "/v1"},
		},
		names:       []string{"bragi", "remote"},
		defaultName: "bragi",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{Provider: "bragi"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 bragi models, got %d: %v", len(infos), externalModelIDs(infos))
	}
	for _, info := range infos {
		if info.Provider != "bragi" {
			t.Errorf("model %q has Provider=%q, want bragi", info.ID, info.Provider)
		}
	}
}

func TestListModels_isDefaultMatchesConfig(t *testing.T) {
	server := externalModelsServer(t, []string{"model-a", "model-b", "default-model"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1", Model: "default-model"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
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
	server := externalModelsServer(t, []string{"qwen3.5-27b", "unknown-model-xyz"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	for _, info := range infos {
		if info.Billing != fizeau.BillingModelFixed {
			t.Errorf("model %q Billing = %q, want fixed", info.ID, info.Billing)
		}
	}
}

func TestListModels_catalogMetadataForKnownAndUnknownProviderModels(t *testing.T) {
	server := externalModelsServer(t, []string{"qwen3.5-27b", "unknown-model-xyz"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 models, got %d: %v", len(infos), externalModelInfoDebug(infos))
	}

	byID := map[string]fizeau.ModelInfo{}
	for _, info := range infos {
		byID[info.ID] = info
	}
	known := byID["qwen3.5-27b"]
	if known.Billing != fizeau.BillingModelFixed {
		t.Fatalf("known model Billing = %q, want fixed: %#v", known.Billing, known)
	}
	if known.Power != 5 || !known.AutoRoutable || known.ExactPinOnly {
		t.Errorf("known model eligibility = power %d auto %v exact %v, want power 5 auto true exact false", known.Power, known.AutoRoutable, known.ExactPinOnly)
	}
	if known.Cost.InputPerMTok != 0.10 || known.Cost.OutputPerMTok != 0.30 {
		t.Errorf("known model cost = %#v, want 0.10/0.30", known.Cost)
	}
	if known.ContextLength != 262144 || known.ContextSource != fizeau.ContextSourceCatalog {
		t.Errorf("known model context = %d/%q, want 262144/%q", known.ContextLength, known.ContextSource, fizeau.ContextSourceCatalog)
	}
	if known.PerfSignal.SWEBenchVerified != 59.0 {
		t.Errorf("known model SWE = %.1f, want 59.0", known.PerfSignal.SWEBenchVerified)
	}
	if known.EndpointName == "" || known.EndpointBaseURL == "" {
		t.Errorf("known model endpoint identity missing: %#v", known)
	}

	unknown := byID["unknown-model-xyz"]
	if unknown.Billing != fizeau.BillingModelFixed {
		t.Errorf("unknown model Billing = %q, want fixed", unknown.Billing)
	}
	if unknown.Power != 0 || unknown.AutoRoutable || unknown.ExactPinOnly {
		t.Errorf("unknown model eligibility = power %d auto %v exact %v, want zero/false/false", unknown.Power, unknown.AutoRoutable, unknown.ExactPinOnly)
	}
	if unknown.EndpointName == "" || unknown.EndpointBaseURL == "" {
		t.Errorf("unknown model endpoint identity missing: %#v", unknown)
	}
	if unknown.ContextLength != defaultModelContextWindow || unknown.ContextSource != fizeau.ContextSourceDefault {
		t.Errorf("unknown model context = %d/%q, want %d/%q", unknown.ContextLength, unknown.ContextSource, defaultModelContextWindow, fizeau.ContextSourceDefault)
	}
}

func TestListModels_contextSourcePrecedence(t *testing.T) {
	t.Run("provider config override wins", func(t *testing.T) {
		server := externalModelsServer(t, []string{"qwen3.5-27b"})
		config := &providerFacadeConfig{
			providers: map[string]fizeau.ServiceProviderEntry{
				"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1", Model: "qwen3.5-27b", ContextWindow: 4096},
			},
			names:       []string{"bragi"},
			defaultName: "bragi",
		}
		infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("want 1 model, got %d", len(infos))
		}
		if infos[0].ContextLength != 4096 || infos[0].ContextSource != fizeau.ContextSourceProviderConfig {
			t.Fatalf("provider config context = %d/%q, want 4096/%q", infos[0].ContextLength, infos[0].ContextSource, fizeau.ContextSourceProviderConfig)
		}
	})

	t.Run("default falls back when catalog missing", func(t *testing.T) {
		server := externalModelsServer(t, []string{"unknown-model-xyz"})
		config := &providerFacadeConfig{
			providers: map[string]fizeau.ServiceProviderEntry{
				"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1"},
			},
			names:       []string{"bragi"},
			defaultName: "bragi",
		}
		infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("want 1 model, got %d", len(infos))
		}
		if infos[0].ContextLength != defaultModelContextWindow || infos[0].ContextSource != fizeau.ContextSourceDefault {
			t.Fatalf("default context = %d/%q, want %d/%q", infos[0].ContextLength, infos[0].ContextSource, defaultModelContextWindow, fizeau.ContextSourceDefault)
		}
	})
}

func TestListModels_rankPosition(t *testing.T) {
	server := externalModelsServer(t, []string{"first-model", "second-model", "third-model"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	infos, err := newProviderModelFacade(t, config).ListModels(context.Background(), fizeau.ModelFilter{})
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
