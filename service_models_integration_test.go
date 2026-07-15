//go:build integration

package fizeau_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

const openRouterModelsBaseURL = "https://openrouter.ai/api/v1"

func TestIntegrationListModelsOpenRouterLive(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not set; skipping live OpenRouter ListModels integration")
	}

	baseURL := strings.TrimSpace(os.Getenv("OPENROUTER_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = openRouterModelsBaseURL
	}

	assertLiveListModelsProvider(t, liveListModelsProvider{
		name:         "openrouter",
		providerType: "openrouter",
		baseURL:      liveListModelsEnsureV1BaseURL(baseURL),
		apiKey:       apiKey,
		endpointName: "default",
	})
}

func TestIntegrationListModelsLMStudioLive(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("LMSTUDIO_URL"))
	if baseURL == "" {
		t.Skip("LMSTUDIO_URL is not set; skipping live LM Studio ListModels integration")
	}

	assertLiveListModelsProvider(t, liveListModelsProvider{
		name:         "lmstudio",
		providerType: "lmstudio",
		baseURL:      liveListModelsEnsureV1BaseURL(baseURL),
		apiKey:       strings.TrimSpace(os.Getenv("LMSTUDIO_API_KEY")),
		endpointName: "live-lmstudio",
	})
}

func TestIntegrationListModelsOMLXLive(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("OMLX_URL"))
	if baseURL == "" {
		t.Skip("OMLX_URL is not set; skipping live oMLX ListModels integration")
	}

	assertLiveListModelsProvider(t, liveListModelsProvider{
		name:         "omlx",
		providerType: "omlx",
		baseURL:      liveListModelsEnsureV1BaseURL(baseURL),
		apiKey:       strings.TrimSpace(os.Getenv("OMLX_API_KEY")),
		endpointName: "live-omlx",
	})
}

type liveListModelsProvider struct {
	name         string
	providerType string
	baseURL      string
	apiKey       string
	endpointName string
}

func assertLiveListModelsProvider(t *testing.T, provider liveListModelsProvider) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig: &liveListModelsServiceConfig{provider: provider},
		// Keep this service-boundary test focused on model listing.
		QuotaRefreshContext:     liveListModelsCanceledRefreshContext(),
		QuotaRefreshStartupWait: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	models, err := svc.ListModels(ctx, fizeau.ModelFilter{Provider: provider.name})
	if err != nil {
		t.Fatalf("ListModels(%s): %v", provider.name, err)
	}
	if len(models) == 0 {
		t.Fatalf("ListModels(%s) returned no live models from %s", provider.name, provider.baseURL)
	}

	for _, model := range models {
		if model.Provider != provider.name {
			t.Errorf("Provider = %q, want %q for model %q", model.Provider, provider.name, model.ID)
		}
		if model.ProviderType != provider.providerType {
			t.Errorf("ProviderType = %q, want %q for model %q", model.ProviderType, provider.providerType, model.ID)
		}
		if model.EndpointName != provider.endpointName {
			t.Errorf("EndpointName = %q, want %q for model %q", model.EndpointName, provider.endpointName, model.ID)
		}
		if model.EndpointBaseURL != provider.baseURL {
			t.Errorf("EndpointBaseURL = %q, want %q for model %q", model.EndpointBaseURL, provider.baseURL, model.ID)
		}
		if strings.TrimSpace(model.ID) == "" {
			t.Errorf("ID is empty for provider %q model info %#v", provider.name, model)
		}
		if !model.Available {
			t.Errorf("Available = false for provider %q model %q", provider.name, model.ID)
		}
	}
}

type liveListModelsServiceConfig struct {
	provider liveListModelsProvider
}

func (c *liveListModelsServiceConfig) ProviderNames() []string {
	return []string{c.provider.name}
}

func (c *liveListModelsServiceConfig) DefaultProviderName() string {
	return c.provider.name
}

func (c *liveListModelsServiceConfig) Provider(name string) (fizeau.ServiceProviderEntry, bool) {
	if name != c.provider.name {
		return fizeau.ServiceProviderEntry{}, false
	}
	return fizeau.ServiceProviderEntry{
		Type:   c.provider.providerType,
		APIKey: c.provider.apiKey,
		Endpoints: []fizeau.ServiceProviderEndpoint{{
			Name:    c.provider.endpointName,
			BaseURL: c.provider.baseURL,
		}},
	}, true
}

func (c *liveListModelsServiceConfig) HealthCooldown() time.Duration {
	return 0
}

func (c *liveListModelsServiceConfig) WorkDir() string {
	return ""
}

func (c *liveListModelsServiceConfig) SessionLogDir() string {
	return ""
}

func liveListModelsCanceledRefreshContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func liveListModelsEnsureV1BaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" || strings.HasSuffix(parsed.Path, "/v1") {
		return raw
	}
	return raw + "/v1"
}
