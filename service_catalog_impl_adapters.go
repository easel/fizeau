package fizeau

import "github.com/easel/fizeau/internal/serviceimpl"

func serviceImplProviderEntry(entry ServiceProviderEntry) serviceimpl.ProviderEntry {
	endpoints := make([]serviceimpl.ProviderEndpoint, 0, len(entry.Endpoints))
	for _, endpoint := range entry.Endpoints {
		endpoints = append(endpoints, serviceimpl.ProviderEndpoint{
			Name:           endpoint.Name,
			BaseURL:        endpoint.BaseURL,
			ServerInstance: endpoint.ServerInstance,
		})
	}
	return serviceimpl.ProviderEntry{
		Type:                      entry.Type,
		BaseURL:                   entry.BaseURL,
		ServerInstance:            entry.ServerInstance,
		Endpoints:                 endpoints,
		APIKey:                    entry.APIKey,
		Headers:                   entry.Headers,
		Model:                     entry.Model,
		Billing:                   entry.Billing,
		IncludeByDefault:          entry.IncludeByDefault,
		IncludeByDefaultSet:       entry.IncludeByDefaultSet,
		ContextWindow:             entry.ContextWindow,
		ConfigError:               entry.ConfigError,
		DailyTokenBudget:          entry.DailyTokenBudget,
		CreditBalanceThresholdUSD: entry.CreditBalanceThresholdUSD,
		CreditProbeTTL:            entry.CreditProbeTTL,
	}
}

func adaptServiceImplCostInfo(info serviceimpl.CostInfo) CostInfo {
	return CostInfo{
		InputPerMTok:  info.InputPerMTok,
		OutputPerMTok: info.OutputPerMTok,
	}
}

func adaptServiceImplPerfSignal(signal serviceimpl.PerfSignal) PerfSignal {
	return PerfSignal{
		SpeedTokensPerSec: signal.SpeedTokensPerSec,
		SWEBenchVerified:  signal.SWEBenchVerified,
	}
}

func adaptServiceImplPolicyInfo(info serviceimpl.PolicyInfo) PolicyInfo {
	return PolicyInfo{
		Name:            info.Name,
		MinPower:        info.MinPower,
		MaxPower:        info.MaxPower,
		AllowLocal:      info.AllowLocal,
		Require:         append([]string(nil), info.Require...),
		CatalogVersion:  info.CatalogVersion,
		ManifestSource:  info.ManifestSource,
		ManifestVersion: info.ManifestVersion,
	}
}
