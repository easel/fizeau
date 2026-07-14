package agentcli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	agentConfig "github.com/easel/fizeau/internal/config"
)

func TestResolveRunModelLimitsUnsupportedProviderDoesNotCallNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	t.Cleanup(server.Close)

	oldLookup := lookupRunModelLimits
	t.Cleanup(func() { lookupRunModelLimits = oldLookup })
	lookupRunModelLimits = func(ctx context.Context, pc agentConfig.ProviderConfig, model string) (int, int) {
		got := agentConfig.LookupModelLimits(ctx, pc, model)
		return got.ContextLength, got.MaxCompletionTokens
	}

	contextWindow, maxTokens := resolveRunModelLimits(context.Background(), nil, agentConfig.ProviderConfig{
		Type:    "openai",
		BaseURL: server.URL,
	}, "model-a")
	if contextWindow != 0 || maxTokens != 0 || calls != 0 {
		t.Fatalf("unsupported lookup = %d/%d with %d network calls, want zero values and no calls", contextWindow, maxTokens, calls)
	}
}

func TestResolveRunModelLimitsAuthorityAndIndependentFill(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(manifestPath, []byte(`
version: 5
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
models:
  model-a:
    family: test
    status: active
    provider_system: openai
    deployment_class: local_free
    power: 5
    context_window: 131072
    surfaces:
      agent.openai: model-a
`), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	cfg := &agentConfig.Config{ModelCatalog: agentConfig.ModelCatalogConfig{Manifest: manifestPath}}

	oldLookup := lookupRunModelLimits
	t.Cleanup(func() { lookupRunModelLimits = oldLookup })
	calls := 0
	lookupRunModelLimits = func(context.Context, agentConfig.ProviderConfig, string) (int, int) {
		calls++
		return 65536, 8192
	}

	contextWindow, maxTokens := resolveRunModelLimits(context.Background(), cfg, agentConfig.ProviderConfig{
		Type:          "lmstudio",
		ContextWindow: 131072,
		MaxTokens:     4096,
	}, "model-a")
	if contextWindow != 131072 || maxTokens != 4096 || calls != 0 {
		t.Fatalf("explicit limits = %d/%d with %d lookups, want 131072/4096 with none", contextWindow, maxTokens, calls)
	}

	contextWindow, maxTokens = resolveRunModelLimits(context.Background(), cfg, agentConfig.ProviderConfig{Type: "lmstudio"}, "model-a")
	if contextWindow != 65536 || maxTokens != 8192 || calls != 1 {
		t.Fatalf("provider limits = %d/%d with %d lookups, want 65536/8192 with one", contextWindow, maxTokens, calls)
	}

	contextWindow, maxTokens = resolveRunModelLimits(context.Background(), cfg, agentConfig.ProviderConfig{
		Type:          "lmstudio",
		ContextWindow: 131072,
	}, "model-a")
	if contextWindow != 131072 || maxTokens != 8192 || calls != 2 {
		t.Fatalf("independent max fill = %d/%d with %d lookups, want 131072/8192 with two", contextWindow, maxTokens, calls)
	}

	contextWindow, maxTokens = resolveRunModelLimits(context.Background(), cfg, agentConfig.ProviderConfig{
		Type:      "lmstudio",
		MaxTokens: 4096,
	}, "model-a")
	if contextWindow != 65536 || maxTokens != 4096 || calls != 3 {
		t.Fatalf("independent context fill = %d/%d with %d lookups, want 65536/4096 with three", contextWindow, maxTokens, calls)
	}
}

func TestResolveRunModelLimitsCatalogFillsOnlyMissingContext(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(manifestPath, []byte(`
version: 5
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
models:
  model-a:
    family: test
    status: active
    provider_system: openai
    deployment_class: local_free
    power: 5
    context_window: 131072
    surfaces:
      agent.openai: model-a
`), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	cfg := &agentConfig.Config{ModelCatalog: agentConfig.ModelCatalogConfig{Manifest: manifestPath}}

	oldLookup := lookupRunModelLimits
	t.Cleanup(func() { lookupRunModelLimits = oldLookup })
	lookupRunModelLimits = func(context.Context, agentConfig.ProviderConfig, string) (int, int) {
		return 0, 0
	}

	contextWindow, maxTokens := resolveRunModelLimits(context.Background(), cfg, agentConfig.ProviderConfig{Type: "lmstudio"}, "model-a")
	if contextWindow != 131072 || maxTokens != 0 {
		t.Fatalf("catalog fallback = %d/%d, want 131072/0", contextWindow, maxTokens)
	}
}
