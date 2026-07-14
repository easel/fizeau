package modelregistry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
)

func TestParsePropsDiscoveryLimitAliases(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantCtx    int
		wantOutput int
	}{
		{
			name:       "llama default settings and model card",
			payload:    `{"default_generation_settings":{"n_ctx":65536},"model_card":{"max_tokens":8192}}`,
			wantCtx:    65536,
			wantOutput: 8192,
		},
		{
			name:       "top level fields",
			payload:    `{"context_window":131072,"max_completion_tokens":16384}`,
			wantCtx:    131072,
			wantOutput: 16384,
		},
		{
			name:       "runtime and generation params",
			payload:    `{"runtime":{"max_ctx":32768},"default_generation_settings":{"params":{"max_tokens":4096}}}`,
			wantCtx:    32768,
			wantOutput: 4096,
		},
		{
			name:       "non-positive evidence is ignored",
			payload:    `{"context_window":0,"runtime":{"max_ctx":-1},"max_completion_tokens":-2,"default_generation_settings":{"params":{"max_tokens":0}}}`,
			wantCtx:    0,
			wantOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := parsePropsDiscoveryDetails([]byte(tt.payload))
			if details.ContextWindow != tt.wantCtx || details.MaxCompletionTokens != tt.wantOutput {
				t.Fatalf("limits = %d/%d, want %d/%d", details.ContextWindow, details.MaxCompletionTokens, tt.wantCtx, tt.wantOutput)
			}
		})
	}
}

func TestPropsDiscoveryPayloadLabelsProviderEvidence(t *testing.T) {
	capturedAt := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	payload := propsDiscoveryPayload([]byte(`{
		"model_alias":"dflash",
		"default_generation_settings":{"n_ctx":65536},
		"max_completion_tokens":8192
	}`), capturedAt)
	if !payload.CapturedAt.Equal(capturedAt) {
		t.Fatalf("CapturedAt = %v, want %v", payload.CapturedAt, capturedAt)
	}
	if payload.ContextWindow != 65536 || payload.ContextWindowSource != limitSourceProviderAPI {
		t.Fatalf("context evidence = %d/%q, want 65536/provider_api", payload.ContextWindow, payload.ContextWindowSource)
	}
	if payload.MaxCompletionTokens != 8192 || payload.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Fatalf("output evidence = %d/%q, want 8192/provider_api", payload.MaxCompletionTokens, payload.MaxCompletionTokensSource)
	}
}

func TestPropsLimitEvidenceSurvivesDiscoveryCacheRoundTrip(t *testing.T) {
	cache := &discoverycache.Cache{Root: t.TempDir()}
	source := discoverySource("sindri-props", time.Hour, time.Second)
	capturedAt := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(discoveryPayload{
		CapturedAt:                capturedAt,
		Models:                    []string{"Qwen3.6-27B", "dflash"},
		ContextWindow:             65536,
		ContextWindowSource:       limitSourceProviderAPI,
		MaxCompletionTokens:       8192,
		MaxCompletionTokensSource: limitSourceProviderAPI,
		Source:                    "props:/props",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Refresh(source, func(context.Context) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatal(err)
	}

	result := readDiscoveryCache(cache, source, "sindri", SourcePropsAPI, discoveryIdentity{
		Provider:     "sindri",
		ProviderType: "lucebox",
		EndpointName: "sindri",
	})
	if len(result.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(result.Models))
	}
	for _, model := range result.Models {
		if model.ContextWindow != 65536 || model.ContextWindowSource != limitSourceProviderAPI {
			t.Fatalf("%s context evidence = %d/%q, want 65536/provider_api", model.ID, model.ContextWindow, model.ContextWindowSource)
		}
		if model.MaxCompletionTokens != 8192 || model.MaxCompletionTokensSource != limitSourceProviderAPI {
			t.Fatalf("%s output evidence = %d/%q, want 8192/provider_api", model.ID, model.MaxCompletionTokens, model.MaxCompletionTokensSource)
		}
	}

	legacy := []byte(`{"captured_at":"2026-07-14T18:00:00Z","models":["legacy-model"],"source":"old-cache"}`)
	ids, gotCapturedAt, limits, err := parseDiscoveryData(legacy, "sindri")
	if err != nil {
		t.Fatalf("legacy cache decode: %v", err)
	}
	if len(ids) != 1 || ids[0] != "legacy-model" || !gotCapturedAt.Equal(capturedAt) {
		t.Fatalf("legacy cache identity = %#v/%v, want legacy-model/%v", ids, gotCapturedAt, capturedAt)
	}
	if limits != (discoveryLimitEvidence{}) {
		t.Fatalf("legacy cache limits = %#v, want zero evidence", limits)
	}
}

func TestProviderDiscoveryMergePreservesDuplicateLimitEvidence(t *testing.T) {
	result := providerDiscoveryResult{Models: []discoveredModel{{
		Provider:     "sindri",
		ProviderType: "lucebox",
		ID:           "dflash",
		EndpointName: "sindri",
		Via:          SourceNativeAPI,
	}}}
	result.merge(providerDiscoveryResult{Models: []discoveredModel{{
		Provider:                  "sindri",
		ProviderType:              "lucebox",
		ID:                        "dflash",
		EndpointName:              "sindri",
		Via:                       SourcePropsAPI,
		ContextWindow:             65536,
		ContextWindowSource:       limitSourceProviderAPI,
		MaxCompletionTokens:       8192,
		MaxCompletionTokensSource: limitSourceProviderAPI,
	}}})

	if len(result.Models) != 1 {
		t.Fatalf("models = %d, want one merged served alias", len(result.Models))
	}
	got := result.Models[0]
	if got.Via != SourceNativeAPI {
		t.Fatalf("discovery source = %q, want served native API identity", got.Via)
	}
	if got.ContextWindow != 65536 || got.ContextWindowSource != limitSourceProviderAPI || got.MaxCompletionTokens != 8192 || got.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Fatalf("merged limit evidence = %#v", got)
	}
}

func TestReconcilePropsModelsPropagatesLimitsWithoutCatalogIdentity(t *testing.T) {
	models := []discoveredModel{
		{Provider: "sindri", ID: "served-alias", Via: SourceNativeAPI},
		{
			Provider:                  "sindri",
			ID:                        "uncataloged-props-name",
			Via:                       SourcePropsAPI,
			ContextWindow:             65536,
			ContextWindowSource:       limitSourceProviderAPI,
			MaxCompletionTokens:       4096,
			MaxCompletionTokensSource: limitSourceProviderAPI,
		},
	}

	out := reconcilePropsModels(models, true, "available", nil)
	if len(out) != 1 || out[0].ID != "served-alias" {
		t.Fatalf("reconciled models = %#v, want served alias only", out)
	}
	if out[0].CatalogID != "" {
		t.Fatalf("CatalogID = %q, want empty for uncataloged props identity", out[0].CatalogID)
	}
	if out[0].ContextWindow != 65536 || out[0].ContextWindowSource != limitSourceProviderAPI || out[0].MaxCompletionTokens != 4096 || out[0].MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Fatalf("alias limit evidence = %#v", out[0])
	}
}

func TestEnrichModelCatalogFillsOnlyMissingContext(t *testing.T) {
	cat := loadTestCatalog(t)
	providerOwned := EnrichModel(KnownModel{
		ID:                        "gpt-5.5",
		Status:                    StatusAvailable,
		ContextWindow:             65536,
		ContextWindowSource:       limitSourceProviderAPI,
		MaxCompletionTokens:       8192,
		MaxCompletionTokensSource: limitSourceProviderAPI,
	}, true, cat)
	if providerOwned.ContextWindow != 65536 || providerOwned.ContextWindowSource != limitSourceProviderAPI {
		t.Fatalf("provider context was overwritten: %d/%q", providerOwned.ContextWindow, providerOwned.ContextWindowSource)
	}
	if providerOwned.MaxCompletionTokens != 8192 || providerOwned.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Fatalf("provider max output was overwritten: %d/%q", providerOwned.MaxCompletionTokens, providerOwned.MaxCompletionTokensSource)
	}

	catalogFallback := EnrichModel(KnownModel{ID: "gpt-5.5", Status: StatusAvailable}, true, cat)
	if catalogFallback.ContextWindow != 400000 || catalogFallback.ContextWindowSource != limitSourceCatalog {
		t.Fatalf("catalog context fallback = %d/%q, want 400000/catalog", catalogFallback.ContextWindow, catalogFallback.ContextWindowSource)
	}
	if catalogFallback.MaxCompletionTokens != 0 || catalogFallback.MaxCompletionTokensSource != "" {
		t.Fatalf("catalog filled max output unexpectedly: %d/%q", catalogFallback.MaxCompletionTokens, catalogFallback.MaxCompletionTokensSource)
	}
}
