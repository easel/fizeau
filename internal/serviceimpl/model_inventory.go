package serviceimpl

import (
	"context"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/serverinstance"
)

// ModelInventoryInput carries the implementation-local inputs needed to
// project a model snapshot into the service inventory. Public service DTOs are
// deliberately adapted by the root facade instead of leaking into this type.
type ModelInventoryInput struct {
	ProviderNames   []string
	Providers       map[string]ProviderEntry
	DefaultProvider string
	ProviderFilter  string
	Snapshot        modelsnapshot.ModelSnapshot
	Catalog         *modelcatalog.Catalog
}

// ModelInventoryRow is the API-neutral model inventory projection consumed by
// the root service facade. Model carries snapshot/catalog metadata while the
// remaining fields describe service-listing concerns that are not part of the
// discovery snapshot contract.
type ModelInventoryRow struct {
	Model         modelsnapshot.KnownModel
	ContextSource string
	Capabilities  []string
	PerfSignal    PerfSignal
	Available     bool
	IsDefault     bool
	RankPosition  int
}

// AssembleModelInventory filters and orders snapshot rows according to service
// configuration, resolves final context evidence, and assigns endpoint-local
// discovery ranks.
func AssembleModelInventory(ctx context.Context, input ModelInventoryInput) []ModelInventoryRow {
	modelsByProvider := make(map[string][]modelsnapshot.KnownModel, len(input.Snapshot.Models))
	for _, model := range input.Snapshot.Models {
		if input.ProviderFilter != "" && input.ProviderFilter != model.Provider {
			continue
		}
		if _, ok := input.Providers[model.Provider]; !ok {
			continue
		}
		modelsByProvider[model.Provider] = append(modelsByProvider[model.Provider], model)
	}

	rankByEndpoint := make(map[string]int, len(input.Snapshot.Models))
	out := make([]ModelInventoryRow, 0, len(input.Snapshot.Models))
	for _, providerName := range input.ProviderNames {
		if input.ProviderFilter != "" && input.ProviderFilter != providerName {
			continue
		}
		entry, ok := input.Providers[providerName]
		if !ok {
			continue
		}
		for _, snapshotModel := range modelsByProvider[providerName] {
			model := snapshotModel
			model.ServerInstance = serverinstance.Normalize(model.EndpointBaseURL, model.ServerInstance)
			rankKey := strings.Join([]string{model.Provider, model.EndpointName, model.EndpointBaseURL, model.ServerInstance}, "\x00")
			rank := rankByEndpoint[rankKey]
			rankByEndpoint[rankKey] = rank + 1

			contextLength, contextSource := ResolveContextEvidence(ctx, entry, model.ID, input.Catalog)
			model.ContextWindow = contextLength
			if model.Harness == "" {
				model.Harness = "fiz"
			}

			row := ModelInventoryRow{
				Model:         model,
				ContextSource: contextSource,
				Capabilities:  ProviderCapabilities(entry),
				Available:     true,
				IsDefault:     providerName == input.DefaultProvider && entry.Model != "" && model.ID == entry.Model,
				RankPosition:  rank,
			}
			if input.Catalog != nil {
				_, row.PerfSignal = CatalogCostAndPerf(input.Catalog, model.ID)
			}
			out = append(out, row)
		}
	}
	return out
}

// SubscriptionHarnessInventoryInput carries the already-discovered model IDs
// and catalog seams needed to build one subprocess harness's tier inventory.
// Model discovery remains owned by the caller so production and test discovery
// behavior is unchanged by this projection.
type SubscriptionHarnessInventoryInput struct {
	Name                  string
	Config                harnesses.HarnessConfig
	ModelIDs              []string
	Catalog               *modelcatalog.Catalog
	EffectiveCostForModel func(string) (float64, bool)
}

// SubscriptionHarnessTierModels builds API-neutral inventory rows for a
// subprocess subscription harness in the order reported by model discovery.
func SubscriptionHarnessTierModels(ctx context.Context, input SubscriptionHarnessInventoryInput) []ModelInventoryRow {
	if len(input.ModelIDs) == 0 {
		return nil
	}
	out := make([]ModelInventoryRow, 0, len(input.ModelIDs))
	for i, id := range input.ModelIDs {
		model := modelsnapshot.KnownModel{
			ID:             id,
			Provider:       input.Name,
			Harness:        input.Name,
			EndpointName:   input.Name,
			ServerInstance: input.Name,
			Billing:        HarnessPaymentKind(input.Name, input.Config),
		}
		row := ModelInventoryRow{
			Model:        model,
			Capabilities: []string{"streaming", "tool_use"},
			Available:    true,
			IsDefault:    input.Config.DefaultModel != "" && id == input.Config.DefaultModel,
			RankPosition: i,
		}
		if input.Catalog != nil {
			row.Model.ContextWindow, row.ContextSource = ResolveContextEvidence(ctx, ProviderEntry{}, id, input.Catalog)
			cost, perf := CatalogCostAndPerf(input.Catalog, id)
			row.Model.CostInputPerM = cost.InputPerMTok
			row.Model.CostOutputPerM = cost.OutputPerMTok
			row.PerfSignal = perf
			row.Model.Power, row.Model.AutoRoutable, row.Model.ExactPinOnly = CatalogPowerEligibility(input.Catalog, id)
			if input.EffectiveCostForModel != nil {
				if effectiveCost, ok := input.EffectiveCostForModel(id); ok {
					row.Model.EffectiveCost = effectiveCost
					if row.Model.Billing == modelcatalog.BillingModelSubscription {
						row.Model.EffectiveCostSource = "subscription_shadow"
					} else {
						row.Model.EffectiveCostSource = "catalog"
					}
				}
			}
		}
		out = append(out, row)
	}
	return out
}
