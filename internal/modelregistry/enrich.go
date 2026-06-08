package modelregistry

import (
	"strings"

	"github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modeleligibility"
	"github.com/easel/fizeau/internal/runtimesignals"
)

const (
	exclusionCatalogUnknown     = "catalog_unknown"
	exclusionCatalogNotRoutable = "catalog_not_auto_routable"
	exclusionProviderExcluded   = "provider_include_by_default_false"
	exclusionStatusUnavailable  = "status_unavailable"
)

func EnrichModel(model KnownModel, includeByDefault bool, cat *modelcatalog.Catalog) KnownModel {
	// Catalog enrichment resolves against the recovered catalog identity (from
	// /props) when present, so a generic served alias ("dflash") inherits the
	// power/family/cost of its real model ("Qwen3.6-27B"). model.ID stays the
	// wire name the provider is actually called with.
	catalogID := strings.TrimSpace(model.CatalogID)
	if catalogID == "" {
		catalogID = model.ID
	}
	parsed := modelcatalog.Parse(catalogID)
	model.Family = parsed.Family
	model.Version = append([]int(nil), parsed.Version...)
	model.Tier = parsed.Tier
	model.PreRelease = parsed.PreRelease

	view := modeleligibility.Resolve(catalogID, includeByDefault, string(model.Status), cat)
	model.Power = view.Power
	model.ExactPinOnly = view.ExactPinOnly
	model.AutoRoutable = view.AutoRoutable
	model.ExclusionReason = view.ExclusionReason
	if cat != nil {
		if entry, ok := cat.LookupModel(catalogID); ok {
			model.CostInputPerM = catalogCostInput(entry)
			model.CostOutputPerM = catalogCostOutput(entry)
			model.ContextWindow = entry.ContextWindow
			model.ReasoningLevels = append([]string(nil), entry.ReasoningLevels...)
			model.QuotaPool = entry.EffectiveQuotaPool()
			model.SupportsTools = !entry.NoTools
			model.DeploymentClass = entry.DeploymentClass
			model.ActualCashSpend = billingCreatesCashSpend(model.Billing)
			model.EffectiveCost, model.EffectiveCostSource = effectiveModelCost(entry, model.Billing)
		}
	}
	return model
}

func enrichModel(model KnownModel, includeByDefault bool, cat *modelcatalog.Catalog) KnownModel {
	return EnrichModel(model, includeByDefault, cat)
}

func attachRuntimeSignals(model KnownModel, cache *discoverycache.Cache) KnownModel {
	if cache == nil {
		return model
	}
	sig, ok := runtimesignals.ReadCached(cache, model.Provider)
	if !ok || sig == nil {
		return model
	}
	model.QuotaRemaining = sig.QuotaRemaining
	model.RecentP50Latency = sig.RecentP50Latency
	if !sig.RecordedAt.IsZero() {
		model.HealthFreshnessAt = sig.RecordedAt.UTC()
		model.HealthFreshnessSource = "runtime"
		model.QuotaFreshnessAt = sig.RecordedAt.UTC()
		model.QuotaFreshnessSource = "runtime"
	}
	return model
}

func effectiveProviderIncludeByDefault(providerName string, pc config.ProviderConfig, cat *modelcatalog.Catalog) bool {
	if pc.IncludeByDefault != nil {
		return *pc.IncludeByDefault
	}
	if cat != nil {
		name := strings.ToLower(strings.TrimSpace(providerName))
		providerType := strings.ToLower(strings.TrimSpace(pc.Type))
		for _, provider := range cat.Providers() {
			catalogName := strings.ToLower(strings.TrimSpace(provider.Name))
			catalogType := strings.ToLower(strings.TrimSpace(provider.Type))
			if name != "" && catalogName == name {
				return provider.IncludeByDefault
			}
			if providerType != "" && (catalogType == providerType || catalogName == providerType) {
				return provider.IncludeByDefault
			}
		}
	}
	switch modelcatalog.BillingModel(strings.TrimSpace(pc.Billing)) {
	case modelcatalog.BillingModelFixed, modelcatalog.BillingModelSubscription:
		return true
	case modelcatalog.BillingModelPerToken:
		return false
	}
	if billing := modelcatalog.BillingForProviderSystem(pc.Type); billing != modelcatalog.BillingModelUnknown {
		return billing == modelcatalog.BillingModelFixed || billing == modelcatalog.BillingModelSubscription
	}
	billing := modelcatalog.BillingForHarness(pc.Type)
	return billing == modelcatalog.BillingModelFixed || billing == modelcatalog.BillingModelSubscription
}

func effectiveProviderBilling(providerName string, pc config.ProviderConfig, cat *modelcatalog.Catalog) modelcatalog.BillingModel {
	if billing := modelcatalog.BillingModel(strings.TrimSpace(pc.Billing)); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	if cat != nil {
		name := strings.ToLower(strings.TrimSpace(providerName))
		providerType := strings.ToLower(strings.TrimSpace(pc.Type))
		for _, provider := range cat.Providers() {
			catalogName := strings.ToLower(strings.TrimSpace(provider.Name))
			catalogType := strings.ToLower(strings.TrimSpace(provider.Type))
			if name != "" && catalogName == name {
				return provider.Billing
			}
			if providerType != "" && (catalogType == providerType || catalogName == providerType) {
				return provider.Billing
			}
		}
	}
	if billing := modelcatalog.BillingForProviderSystem(pc.Type); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	return modelcatalog.BillingForHarness(pc.Type)
}

func providerStatus(cache *discoverycache.Cache, providerName string) ModelStatus {
	if cache == nil {
		return StatusAvailable
	}
	sig, ok := runtimesignals.ReadCached(cache, providerName)
	if !ok || sig == nil {
		return StatusAvailable
	}
	switch sig.Status {
	case runtimesignals.StatusAvailable, runtimesignals.StatusDegraded:
		return StatusAvailable
	case runtimesignals.StatusExhausted:
		return StatusRateLimited
	case runtimesignals.StatusUnknown:
		return StatusUnknown
	default:
		return StatusUnknown
	}
}

func catalogCostInput(entry modelcatalog.ModelEntry) float64 {
	if entry.CostInputPerM != 0 {
		return entry.CostInputPerM
	}
	return entry.CostInputPerMTok
}

func catalogCostOutput(entry modelcatalog.ModelEntry) float64 {
	if entry.CostOutputPerM != 0 {
		return entry.CostOutputPerM
	}
	return entry.CostOutputPerMTok
}

func effectiveModelCost(entry modelcatalog.ModelEntry, billing modelcatalog.BillingModel) (float64, string) {
	cost := catalogCostUSDPer1kTokens(entry)
	if cost == 0 {
		return 0, "unknown"
	}
	if billing == modelcatalog.BillingModelSubscription {
		return cost, "subscription_shadow"
	}
	return cost, "catalog"
}

func billingCreatesCashSpend(billing modelcatalog.BillingModel) bool {
	return billing == modelcatalog.BillingModelPerToken
}

func catalogCostUSDPer1kTokens(entry modelcatalog.ModelEntry) float64 {
	input := entry.CostInputPerM
	if input == 0 {
		input = entry.CostInputPerMTok
	}
	output := entry.CostOutputPerM
	if output == 0 {
		output = entry.CostOutputPerMTok
	}
	switch {
	case input > 0 && output > 0:
		return ((input + output) / 2) / 1000
	case input > 0:
		return input / 1000
	case output > 0:
		return output / 1000
	default:
		return 0
	}
}
