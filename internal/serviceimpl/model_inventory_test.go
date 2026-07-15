package serviceimpl

import (
	"context"
	"slices"
	"testing"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serverinstance"
)

func TestAssembleModelInventoryProviderFilterOrderingAndRanking(t *testing.T) {
	const (
		betaPrimary   = "http://127.0.0.1:9001/v1"
		betaSecondary = "http://127.0.0.1:9002/v1"
		alphaPrimary  = "http://127.0.0.1:9003/v1"
	)
	input := ModelInventoryInput{
		ProviderNames: []string{"beta", "alpha"},
		Providers: map[string]ProviderEntry{
			"beta":  {Type: "anthropic", Model: "beta-2"},
			"alpha": {Type: "omlx"},
		},
		DefaultProvider: "beta",
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{
			{Provider: "alpha", ID: "alpha-1", EndpointName: "primary", EndpointBaseURL: alphaPrimary},
			{Provider: "beta", ID: "beta-1", EndpointName: "primary", EndpointBaseURL: betaPrimary},
			{Provider: "beta", ID: "beta-2", EndpointName: "primary", EndpointBaseURL: betaPrimary},
			{Provider: "beta", ID: "beta-3", EndpointName: "secondary", EndpointBaseURL: betaSecondary},
		}},
	}

	rows := AssembleModelInventory(context.Background(), input)
	if len(rows) != 4 {
		t.Fatalf("row count = %d, want 4", len(rows))
	}
	wantIDs := []string{"beta-1", "beta-2", "beta-3", "alpha-1"}
	for i, want := range wantIDs {
		if rows[i].Model.ID != want {
			t.Fatalf("row %d ID = %q, want %q", i, rows[i].Model.ID, want)
		}
		if rows[i].Model.Harness != "fiz" || !rows[i].Available {
			t.Fatalf("row %d harness/available = %q/%v, want fiz/true", i, rows[i].Model.Harness, rows[i].Available)
		}
	}
	if rows[0].RankPosition != 0 || rows[1].RankPosition != 1 || rows[2].RankPosition != 0 || rows[3].RankPosition != 0 {
		t.Fatalf("endpoint-local ranks = [%d %d %d %d], want [0 1 0 0]",
			rows[0].RankPosition, rows[1].RankPosition, rows[2].RankPosition, rows[3].RankPosition)
	}
	if rows[0].IsDefault || !rows[1].IsDefault || rows[2].IsDefault || rows[3].IsDefault {
		t.Fatalf("default flags = [%v %v %v %v], want [false true false false]",
			rows[0].IsDefault, rows[1].IsDefault, rows[2].IsDefault, rows[3].IsDefault)
	}
	if !slices.Equal(rows[0].Capabilities, []string{"tool_use", "vision", "streaming"}) {
		t.Fatalf("beta capabilities = %#v", rows[0].Capabilities)
	}
	if !slices.Equal(rows[3].Capabilities, []string{"tool_use", "streaming", "json_mode", "reasoning_control"}) {
		t.Fatalf("alpha capabilities = %#v", rows[3].Capabilities)
	}
	if rows[0].Model.ServerInstance != serverinstance.FromBaseURL(betaPrimary) ||
		rows[2].Model.ServerInstance != serverinstance.FromBaseURL(betaSecondary) {
		t.Fatalf("normalized server instances = %q/%q", rows[0].Model.ServerInstance, rows[2].Model.ServerInstance)
	}

	input.ProviderFilter = "alpha"
	filtered := AssembleModelInventory(context.Background(), input)
	if len(filtered) != 1 || filtered[0].Model.Provider != "alpha" || filtered[0].Model.ID != "alpha-1" {
		t.Fatalf("filtered rows = %#v, want alpha/alpha-1", filtered)
	}
}

func TestAssembleModelInventoryCatalogMetadataAndFallback(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	known := modelsnapshot.EnrichModel(modelsnapshot.KnownModel{
		Provider: "local",
		ID:       "qwen3.5-27b",
		Billing:  modelcatalog.BillingModelFixed,
		Status:   modelsnapshot.StatusAvailable,
	}, true, cat)
	unknown := modelsnapshot.EnrichModel(modelsnapshot.KnownModel{
		Provider: "local",
		ID:       "unknown-model-xyz",
		Billing:  modelcatalog.BillingModelFixed,
		Status:   modelsnapshot.StatusAvailable,
	}, true, cat)
	rows := AssembleModelInventory(context.Background(), ModelInventoryInput{
		ProviderNames: []string{"local"},
		Providers: map[string]ProviderEntry{
			"local": {Type: "custom"},
		},
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{known, unknown}},
		Catalog:  cat,
	})
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}

	gotKnown := rows[0]
	if gotKnown.Model.ID != "qwen3.5-27b" || gotKnown.Model.Billing != modelcatalog.BillingModelFixed {
		t.Fatalf("known identity/billing = %q/%q", gotKnown.Model.ID, gotKnown.Model.Billing)
	}
	if gotKnown.Model.Power != 5 || !gotKnown.Model.AutoRoutable || gotKnown.Model.ExactPinOnly {
		t.Fatalf("known eligibility = power:%d auto:%v exact:%v", gotKnown.Model.Power, gotKnown.Model.AutoRoutable, gotKnown.Model.ExactPinOnly)
	}
	if gotKnown.Model.CostInputPerM != 0.10 || gotKnown.Model.CostOutputPerM != 0.30 {
		t.Fatalf("known cost = %v/%v, want 0.10/0.30", gotKnown.Model.CostInputPerM, gotKnown.Model.CostOutputPerM)
	}
	if gotKnown.Model.ContextWindow != 262144 || gotKnown.ContextSource != routing.ContextSourceCatalog {
		t.Fatalf("known context = %d/%q, want 262144/%q", gotKnown.Model.ContextWindow, gotKnown.ContextSource, routing.ContextSourceCatalog)
	}
	if gotKnown.PerfSignal.SWEBenchVerified != 59.0 {
		t.Fatalf("known SWE-bench = %.1f, want 59.0", gotKnown.PerfSignal.SWEBenchVerified)
	}

	gotUnknown := rows[1]
	if gotUnknown.Model.ID != "unknown-model-xyz" || gotUnknown.Model.Power != 0 || gotUnknown.Model.AutoRoutable || gotUnknown.Model.ExactPinOnly {
		t.Fatalf("unknown eligibility = %#v", gotUnknown.Model)
	}
	if gotUnknown.Model.CostInputPerM != 0 || gotUnknown.Model.CostOutputPerM != 0 || gotUnknown.PerfSignal != (PerfSignal{}) {
		t.Fatalf("unknown catalog metadata = %#v/%#v", gotUnknown.Model, gotUnknown.PerfSignal)
	}
	if gotUnknown.Model.ContextWindow != compaction.DefaultContextWindow || gotUnknown.ContextSource != routing.ContextSourceDefault {
		t.Fatalf("unknown context = %d/%q, want %d/%q", gotUnknown.Model.ContextWindow, gotUnknown.ContextSource, compaction.DefaultContextWindow, routing.ContextSourceDefault)
	}
}

func TestResolveSnapshotContextEvidencePrecedence(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	tests := []struct {
		name       string
		entry      ProviderEntry
		model      modelsnapshot.KnownModel
		wantLength int
		wantSource string
	}{
		{
			name:       "provider config overrides provider API evidence",
			entry:      ProviderEntry{Type: "custom", ContextWindow: 4096},
			model:      modelsnapshot.KnownModel{ID: "qwen3.5-27b", ContextWindow: 65536, ContextWindowSource: routing.ContextSourceProviderAPI},
			wantLength: 4096,
			wantSource: routing.ContextSourceProviderConfig,
		},
		{
			name:       "cached provider API evidence overrides catalog",
			entry:      ProviderEntry{Type: "custom"},
			model:      modelsnapshot.KnownModel{ID: "qwen3.5-27b", ContextWindow: 65536, ContextWindowSource: routing.ContextSourceProviderAPI},
			wantLength: 65536,
			wantSource: routing.ContextSourceProviderAPI,
		},
		{
			name:       "catalog supplies known model fallback",
			entry:      ProviderEntry{Type: "custom"},
			model:      modelsnapshot.KnownModel{ID: "qwen3.5-27b"},
			wantLength: 262144,
			wantSource: routing.ContextSourceCatalog,
		},
		{
			name:       "default supplies unknown model fallback",
			entry:      ProviderEntry{Type: "custom"},
			model:      modelsnapshot.KnownModel{ID: "unknown-model-xyz"},
			wantLength: compaction.DefaultContextWindow,
			wantSource: routing.ContextSourceDefault,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			length, source := ResolveSnapshotContextEvidence(context.Background(), test.entry, test.model, cat)
			if length != test.wantLength || source != test.wantSource {
				t.Fatalf("context = %d/%q, want %d/%q", length, source, test.wantLength, test.wantSource)
			}
		})
	}
}

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
