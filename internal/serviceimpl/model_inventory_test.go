package serviceimpl

import (
	"context"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serverinstance"
)

func TestAssembleModelInventoryPreservesContextAndBilling(t *testing.T) {
	const (
		provider = "subscription"
		modelID  = "model-a"
		baseURL  = "http://127.0.0.1:8080/v1"
	)
	rows := AssembleModelInventory(context.Background(), ModelInventoryInput{
		ProviderNames: []string{provider},
		Providers: map[string]ProviderEntry{
			provider: {
				Type:          "openai",
				Model:         modelID,
				ContextWindow: 4096,
			},
		},
		DefaultProvider: provider,
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{{
			Provider:            provider,
			ProviderType:        "openai",
			ID:                  modelID,
			EndpointName:        "primary",
			EndpointBaseURL:     baseURL,
			ContextWindow:       262144,
			Billing:             modelcatalog.BillingModelSubscription,
			EffectiveCost:       0.25,
			EffectiveCostSource: "subscription_shadow",
		}}},
	})

	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Model.ContextWindow != 4096 || row.ContextSource != routing.ContextSourceProviderConfig {
		t.Fatalf("context = %d/%q, want 4096/%q", row.Model.ContextWindow, row.ContextSource, routing.ContextSourceProviderConfig)
	}
	if row.Model.Billing != modelcatalog.BillingModelSubscription {
		t.Fatalf("billing = %q, want %q", row.Model.Billing, modelcatalog.BillingModelSubscription)
	}
	if row.Model.EffectiveCost != 0.25 || row.Model.EffectiveCostSource != "subscription_shadow" {
		t.Fatalf("effective cost = %v/%q, want 0.25/subscription_shadow", row.Model.EffectiveCost, row.Model.EffectiveCostSource)
	}
	if row.Model.Harness != "fiz" {
		t.Fatalf("harness = %q, want fiz", row.Model.Harness)
	}
	if row.Model.ServerInstance != serverinstance.FromBaseURL(baseURL) {
		t.Fatalf("server instance = %q, want normalized %q", row.Model.ServerInstance, serverinstance.FromBaseURL(baseURL))
	}
	if !row.Available || !row.IsDefault || row.RankPosition != 0 {
		t.Fatalf("listing state = available:%v default:%v rank:%d, want true/true/0", row.Available, row.IsDefault, row.RankPosition)
	}
}

func TestAssembleModelInventoryUsesCachedProviderAPIContextBeforeCatalog(t *testing.T) {
	cat := loadRoutingInputsTestCatalog(t)
	rows := AssembleModelInventory(context.Background(), ModelInventoryInput{
		ProviderNames: []string{"runtime"},
		Providers: map[string]ProviderEntry{
			"runtime": {Type: "ds4", Model: "priced-model"},
		},
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{{
			Provider:            "runtime",
			ProviderType:        "ds4",
			ID:                  "priced-model",
			ContextWindow:       65536,
			ContextWindowSource: routing.ContextSourceProviderAPI,
		}}},
		Catalog: cat,
	})

	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if rows[0].Model.ContextWindow != 65536 || rows[0].ContextSource != routing.ContextSourceProviderAPI {
		t.Fatalf("context = %d/%q, want 65536/%q", rows[0].Model.ContextWindow, rows[0].ContextSource, routing.ContextSourceProviderAPI)
	}
}

func TestSubscriptionHarnessTierModelsPreservesOrderingAndBilling(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	ids := []string{"opus-4.7", "sonnet-4.6"}
	rows := SubscriptionHarnessTierModels(context.Background(), SubscriptionHarnessInventoryInput{
		Name: "claude",
		Config: harnesses.HarnessConfig{
			Name:           "claude",
			DefaultModel:   ids[1],
			IsSubscription: true,
		},
		ModelIDs: ids,
		Catalog:  cat,
		EffectiveCostForModel: func(string) (float64, bool) {
			return 1.25, true
		},
	})

	if len(rows) != len(ids) {
		t.Fatalf("row count = %d, want %d", len(rows), len(ids))
	}
	for i, row := range rows {
		if row.Model.ID != ids[i] || row.RankPosition != i {
			t.Fatalf("row %d identity/rank = %q/%d, want %q/%d", i, row.Model.ID, row.RankPosition, ids[i], i)
		}
		if row.Model.Billing != modelcatalog.BillingModelSubscription {
			t.Fatalf("row %d billing = %q, want subscription", i, row.Model.Billing)
		}
		if row.Model.EffectiveCost != 1.25 || row.Model.EffectiveCostSource != "subscription_shadow" {
			t.Fatalf("row %d effective cost = %v/%q, want 1.25/subscription_shadow", i, row.Model.EffectiveCost, row.Model.EffectiveCostSource)
		}
		if row.Model.ContextWindow <= 0 || row.ContextSource == "" {
			t.Fatalf("row %d missing context evidence: %d/%q", i, row.Model.ContextWindow, row.ContextSource)
		}
	}
	if rows[0].IsDefault || !rows[1].IsDefault {
		t.Fatalf("default flags = %v/%v, want false/true", rows[0].IsDefault, rows[1].IsDefault)
	}
}

func TestSubscriptionHarnessTierModelsNilCatalogLeavesContextUnknown(t *testing.T) {
	rows := SubscriptionHarnessTierModels(context.Background(), SubscriptionHarnessInventoryInput{
		Name:     "claude",
		Config:   harnesses.HarnessConfig{Name: "claude", IsSubscription: true},
		ModelIDs: []string{"opus-4.7"},
	})
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if rows[0].Model.ContextWindow != 0 || rows[0].ContextSource != "" {
		t.Fatalf("nil-catalog context = %d/%q, want zero/empty", rows[0].Model.ContextWindow, rows[0].ContextSource)
	}
}
