package modelsnapshot

import (
	"testing"

	"github.com/easel/fizeau/internal/modelcatalog"
)

// TestReconcilePropsModels_ServedAliasInheritsRecoveredIdentity verifies that a
// generic served alias ("dflash", no catalog power) inherits the catalog
// identity recovered from /props ("Qwen3.6-27B", power 5) as its CatalogID, and
// that the standalone /props identities are dropped.
func TestReconcilePropsModels_ServedAliasInheritsRecoveredIdentity(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	models := []discoveredModel{
		{Provider: "sindri", ID: "dflash", Via: SourceNativeAPI, EndpointName: "default"},
		{Provider: "sindri", ID: "Qwen3.6-27B", Via: SourcePropsAPI, EndpointName: "default"},
		{Provider: "sindri", ID: "Qwen3.6-27B-Q4_K_M", Via: SourcePropsAPI, EndpointName: "default"},
	}
	out := reconcilePropsModels(models, true, "available", cat)

	if len(out) != 1 {
		t.Fatalf("want 1 served model, got %d: %+v", len(out), out)
	}
	if out[0].ID != "dflash" {
		t.Errorf("wire ID = %q, want dflash (server alias preserved)", out[0].ID)
	}
	if out[0].CatalogID == "" {
		t.Errorf("served alias did not inherit a recovered catalog identity; CatalogID empty")
	}
	if pw := EnrichModel(KnownModel{ID: out[0].CatalogID, Status: StatusAvailable}, true, cat).Power; pw <= 0 {
		t.Errorf("CatalogID %q resolves to power %d, want > 0", out[0].CatalogID, pw)
	}
}

func TestReconcilePropsModels_NoPropsIsNoOp(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	models := []discoveredModel{
		{Provider: "bragi", ID: "qwen3.5-27b", Via: SourceNativeAPI},
	}
	out := reconcilePropsModels(models, true, "available", cat)
	if len(out) != 1 || out[0].ID != "qwen3.5-27b" || out[0].CatalogID != "" {
		t.Errorf("no-props provider mutated: %+v", out)
	}
}

func TestReconcilePropsModels_ServedAliasInheritsLimitsWithoutCatalogIdentity(t *testing.T) {
	models := []discoveredModel{
		{Provider: "local", ID: "served-alias", Via: SourceNativeAPI, EndpointName: "default"},
		{
			Provider:                  "local",
			ID:                        "uncataloged-model",
			Via:                       SourcePropsAPI,
			EndpointName:              "default",
			ContextWindow:             65536,
			ContextWindowSource:       limitSourceProviderAPI,
			MaxCompletionTokens:       32768,
			MaxCompletionTokensSource: limitSourceProviderAPI,
		},
	}

	out := reconcilePropsModels(models, true, "available", nil)
	if len(out) != 1 {
		t.Fatalf("want one served alias, got %+v", out)
	}
	got := out[0]
	if got.CatalogID != "" {
		t.Errorf("CatalogID = %q, want empty for uncataloged props identity", got.CatalogID)
	}
	if got.ContextWindow != 65536 || got.ContextWindowSource != limitSourceProviderAPI {
		t.Errorf("context evidence = %d/%q, want 65536/%q", got.ContextWindow, got.ContextWindowSource, limitSourceProviderAPI)
	}
	if got.MaxCompletionTokens != 32768 || got.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Errorf("output evidence = %d/%q, want 32768/%q", got.MaxCompletionTokens, got.MaxCompletionTokensSource, limitSourceProviderAPI)
	}
}

func TestProviderDiscoveryMerge_DuplicateAliasFillsMissingLimitEvidence(t *testing.T) {
	base := providerDiscoveryResult{Models: []discoveredModel{{
		Provider: "local", ID: "served-alias", Via: SourceNativeAPI, EndpointName: "default",
	}}}
	base.merge(providerDiscoveryResult{Models: []discoveredModel{{
		Provider:                  "local",
		ID:                        "served-alias",
		Via:                       SourcePropsAPI,
		EndpointName:              "default",
		ContextWindow:             65536,
		ContextWindowSource:       limitSourceProviderAPI,
		MaxCompletionTokens:       32768,
		MaxCompletionTokensSource: limitSourceProviderAPI,
	}}})

	if len(base.Models) != 1 {
		t.Fatalf("merged models = %+v, want one", base.Models)
	}
	got := base.Models[0]
	if got.ContextWindow != 65536 || got.ContextWindowSource != limitSourceProviderAPI ||
		got.MaxCompletionTokens != 32768 || got.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Errorf("merged limit evidence = context %d/%q, output %d/%q", got.ContextWindow, got.ContextWindowSource, got.MaxCompletionTokens, got.MaxCompletionTokensSource)
	}
}
