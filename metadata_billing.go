package fizeau

import (
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func harnessPaymentKind(name string, cfg harnesses.HarnessConfig) BillingModel {
	return serviceimpl.HarnessPaymentKind(name, cfg)
}

func harnessRunsInProcessOrHTTP(cfg harnesses.HarnessConfig) bool {
	return serviceimpl.HarnessRunsInProcessOrHTTP(cfg)
}

func serviceProviderBilling(entry ServiceProviderEntry) BillingModel {
	return serviceimpl.ServiceProviderBilling(serviceImplProviderEntry(entry))
}

func serviceProviderDefaultInclusion(entry ServiceProviderEntry) bool {
	return serviceimpl.ServiceProviderDefaultInclusion(serviceImplProviderEntry(entry))
}

func providerTypeUsesFixedBilling(providerType string) bool {
	return serviceimpl.ProviderTypeUsesFixedBilling(providerType)
}

func routingHarnessEntryFromMetadata(name string, cfg harnesses.HarnessConfig, st harnesses.HarnessStatus) routing.HarnessEntry {
	billing := harnessPaymentKind(name, cfg)
	supportedModels := []string(nil)
	autoRoutingModels := []string(nil)
	// Skip expensive model discovery for subscription harnesses and unavailable harnesses.
	// Subscription harnesses use catalog-based models instead of dynamic discovery.
	if st.Available && billing != modelcatalog.BillingModelSubscription {
		supportedModels = subprocessHarnessModelIDs(name, cfg)
		autoRoutingModels = subprocessHarnessAutoRoutingModels(name, cfg)
	}
	return routing.HarnessEntry{
		Name:                name,
		Surface:             cfg.Surface,
		CostClass:           cfg.CostClass,
		IsLocal:             billing == modelcatalog.BillingModelFixed,
		IsSubscription:      billing == modelcatalog.BillingModelSubscription,
		IsHTTPProvider:      cfg.IsHTTPProvider,
		AutoRoutingEligible: cfg.AutoRoutingEligible,
		TestOnly:            cfg.TestOnly,
		ExactPinSupport:     cfg.ExactPinSupport,
		DefaultModel:        cfg.DefaultModel,
		SupportedModels:     supportedModels,
		AutoRoutingModels:   autoRoutingModels,
		SupportedReasoning:  supportedReasoning(cfg),
		MaxReasoningTokens:  cfg.MaxReasoningTokens,
		SupportedPerms:      supportedPermissions(cfg),
		SupportsTools:       true,
		Available:           st.Available,
		QuotaOK:             true,
		QuotaTrend:          routing.QuotaTrendUnknown,
		SubscriptionOK:      true,
	}
}

func routingHarnessUsesAccountBilling(entry *routing.HarnessEntry) bool {
	return entry != nil && entry.IsSubscription
}
