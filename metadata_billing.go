package fizeau

import (
	"strings"

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
	cat := serviceRoutingCatalog()
	billing := harnessPaymentKind(name, cfg)
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
		DefaultModel:        harnessDefaultModel(name, cfg, cat),
		SupportedModels:     supportedModelsForHarness(name, cfg, cat),
		AutoRoutingModels:   autoRoutingModelsForHarness(name, cfg, cat),
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

func harnessDefaultModel(name string, cfg harnesses.HarnessConfig, cat *modelcatalog.Catalog) string {
	switch name {
	case "codex", "claude", "claude-tui", "grok":
		if tiers := catalogModelsForHarness(name, cfg, cat); len(tiers) > 0 {
			return tiers[0]
		}
	}
	return cfg.DefaultModel
}

func supportedModelsForHarness(name string, cfg harnesses.HarnessConfig, cat *modelcatalog.Catalog) []string {
	models := make([]string, 0)
	models = appendUniqueModelIDs(models, catalogModelsForHarness(name, cfg, cat)...)
	if cfg.DefaultModel != "" {
		models = appendUniqueModelIDs(models, cfg.DefaultModel)
	}
	models = appendUniqueModelIDs(models, subprocessHarnessModelIDs(name, cfg)...)
	if name == "claude" || name == "claude-tui" {
		models = serviceimpl.AppendClaudeModelEquivalents(models)
	}
	models = appendUniqueModelIDs(models, staticHarnessAliases(name)...)
	if len(models) == 0 {
		return nil
	}
	return models
}

func autoRoutingModelsForHarness(name string, cfg harnesses.HarnessConfig, cat *modelcatalog.Catalog) []string {
	models := subprocessHarnessAutoRoutingModels(name, cfg)
	if tiers := catalogModelsForHarness(name, cfg, cat); len(tiers) > 0 && cfg.IsSubscription {
		return appendUniqueModelIDs(nil, tiers...)
	}
	return models
}

func catalogModelsForHarness(name string, cfg harnesses.HarnessConfig, cat *modelcatalog.Catalog) []string {
	if name == "fiz" {
		return nil
	}
	surface := cfg.Surface
	if surface == "" {
		switch name {
		case "codex":
			surface = "codex"
		case "claude", "claude-tui":
			surface = "claude"
		case "gemini", "pi":
			surface = "gemini"
		case "opencode":
			surface = "embedded-openai"
		}
	}
	if name == "pi" {
		surface = "gemini"
	}
	models := appendUniqueModelIDs(nil, serviceimpl.CatalogTierModelsForHarnessSurface(cat, surface)...)
	if surface == "claude" {
		for _, model := range models {
			if strings.HasPrefix(model, "opus-") || strings.HasPrefix(model, "sonnet-") ||
				strings.HasPrefix(model, "haiku-") || strings.HasPrefix(model, "fable-") {
				models = appendUniqueModelIDs(models, "claude-"+model)
			}
		}
	}
	return models
}

func staticHarnessAliases(name string) []string {
	switch name {
	case "codex":
		return []string{"gpt"}
	case "claude", "claude-tui":
		return []string{"claude-opus-4-6", "opus", "sonnet", "haiku", "fable", "fable-1.0"}
	case "gemini":
		return []string{"gemini", "gemini-2.5"}
	case "grok":
		return []string{"grok", "grok-4"}
	default:
		return nil
	}
}

func resolveSubprocessModelAliasWithCatalog(harness, model string, cat *modelcatalog.Catalog) string {
	resolved := resolveSubprocessModelAlias(harness, model)
	if resolved != model {
		return resolved
	}
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "codex":
		if strings.EqualFold(strings.TrimSpace(model), "gpt") {
			if tiers := serviceimpl.CatalogTierModelsForHarnessSurface(cat, "codex"); len(tiers) > 0 {
				return tiers[0]
			}
		}
	case "gemini":
		normalized := strings.ToLower(strings.TrimSpace(model))
		if normalized == "gemini" || normalized == "gemini-2.5" {
			if tiers := serviceimpl.CatalogTierModelsForHarnessSurface(cat, "gemini"); len(tiers) > 0 {
				return tiers[0]
			}
		}
	}
	return resolved
}
