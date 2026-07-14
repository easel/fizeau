package modelsnapshot

import (
	"testing"

	"github.com/easel/fizeau/internal/modelcatalog"
)

func TestEnrichModel_ProviderContextSurvivesCatalog(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	provider := EnrichModel(KnownModel{
		ID:                        "qwen3.5-27b",
		Status:                    StatusAvailable,
		ContextWindow:             65536,
		ContextWindowSource:       limitSourceProviderAPI,
		MaxCompletionTokens:       32768,
		MaxCompletionTokensSource: limitSourceProviderAPI,
	}, true, cat)
	if provider.ContextWindow != 65536 || provider.ContextWindowSource != limitSourceProviderAPI {
		t.Errorf("provider context overwritten by catalog: %d/%q", provider.ContextWindow, provider.ContextWindowSource)
	}
	if provider.MaxCompletionTokens != 32768 || provider.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Errorf("provider output evidence changed: %d/%q", provider.MaxCompletionTokens, provider.MaxCompletionTokensSource)
	}

	catalog := EnrichModel(KnownModel{ID: "qwen3.5-27b", Status: StatusAvailable}, true, cat)
	if catalog.ContextWindow != 262144 || catalog.ContextWindowSource != limitSourceCatalog {
		t.Errorf("catalog context fill = %d/%q, want 262144/%q", catalog.ContextWindow, catalog.ContextWindowSource, limitSourceCatalog)
	}
	if catalog.MaxCompletionTokens != 0 || catalog.MaxCompletionTokensSource != "" {
		t.Errorf("catalog unexpectedly filled output evidence: %d/%q", catalog.MaxCompletionTokens, catalog.MaxCompletionTokensSource)
	}
}
