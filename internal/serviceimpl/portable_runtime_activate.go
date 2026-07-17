package serviceimpl

import (
	"fmt"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/portableruntime"
)

// PortableRuntimeActivation is the pure inverse of the preparation bridge.
// Writable path binding and service construction belong to later activation
// phases.
type PortableRuntimeActivation struct {
	plan      portableruntime.ActivationPlan
	providers PortableRuntimeConfiguredProviders
}

func (a PortableRuntimeActivation) String() string {
	return fmt.Sprintf("{ProviderCount:%d}", len(a.providers.Providers))
}

func (a PortableRuntimeActivation) GoString() string { return a.String() }

func (a PortableRuntimeActivation) Plan() portableruntime.ActivationPlan { return a.plan }

func (a PortableRuntimeActivation) ConfiguredProviders() PortableRuntimeConfiguredProviders {
	return clonePortableRuntimeConfiguredProviders(a.providers)
}

// LoadPortableRuntimeActivation verifies the private bundle and reconstructs
// the API-neutral effective provider configuration without consulting the
// application config loader or starting service activity.
func LoadPortableRuntimeActivation(runtimeRoot string, lookupEnv func(string) (string, bool)) (PortableRuntimeActivation, error) {
	plan, err := portableruntime.LoadActivation(runtimeRoot, lookupEnv)
	if err != nil {
		return PortableRuntimeActivation{}, err
	}
	snapshot := plan.ProviderSnapshot()
	secrets := plan.ProviderSecrets()
	if len(snapshot.Providers) != len(secrets) {
		return PortableRuntimeActivation{}, fmt.Errorf("%w: provider cardinality", portableruntime.ErrActivationInvalid)
	}
	providers := PortableRuntimeConfiguredProviders{
		ProviderNames:       append([]string(nil), snapshot.ProviderNames...),
		DefaultProviderName: snapshot.DefaultProviderName,
		Providers:           make([]PortableRuntimeConfiguredProvider, len(snapshot.Providers)),
		HealthCooldown:      snapshot.HealthCooldown,
		WorkDir: PortableRuntimeConfigField{
			Field: snapshot.WorkDir.Field, Treatment: PortableRuntimeConfigTreatment(snapshot.WorkDir.Treatment), Reason: snapshot.WorkDir.Reason,
		},
		SessionLogDir: PortableRuntimeConfigField{
			Field: snapshot.SessionLogDir.Field, Treatment: PortableRuntimeConfigTreatment(snapshot.SessionLogDir.Treatment), Reason: snapshot.SessionLogDir.Reason,
		},
		sensitiveProviders: make([]PortableRuntimeProviderSensitive, len(secrets)),
	}
	for i, provider := range snapshot.Providers {
		if secrets[i].ProviderName() != provider.Name {
			return PortableRuntimeActivation{}, fmt.Errorf("%w: provider identity", portableruntime.ErrActivationInvalid)
		}
		mapped := PortableRuntimeConfiguredProvider{
			Name: provider.Name, Type: provider.Type, BaseURL: provider.BaseURL, ServerInstance: provider.ServerInstance,
			Endpoints: make([]ProviderEndpoint, len(provider.Endpoints)), Model: provider.Model,
			Billing: modelcatalog.BillingModel(provider.Billing), IncludeByDefault: provider.IncludeByDefault,
			IncludeByDefaultSet: provider.IncludeByDefaultSet, ContextWindow: provider.ContextWindow,
			ConfigError: provider.ConfigError, DailyTokenBudget: provider.DailyTokenBudget,
			CreditBalanceThresholdUSD: provider.CreditBalanceThresholdUSD, CreditProbeTTL: provider.CreditProbeTTL,
		}
		for endpointIndex, endpoint := range provider.Endpoints {
			mapped.Endpoints[endpointIndex] = ProviderEndpoint{Name: endpoint.Name, BaseURL: endpoint.BaseURL, ServerInstance: endpoint.ServerInstance}
		}
		providers.Providers[i] = mapped
		providers.sensitiveProviders[i] = PortableRuntimeProviderSensitive{
			providerName: provider.Name, apiKey: secrets[i].APIKey(), headers: secrets[i].Headers(),
		}
	}
	return PortableRuntimeActivation{plan: plan, providers: providers}, nil
}

func clonePortableRuntimeConfiguredProviders(src PortableRuntimeConfiguredProviders) PortableRuntimeConfiguredProviders {
	out := src
	out.ProviderNames = append([]string(nil), src.ProviderNames...)
	out.Providers = append([]PortableRuntimeConfiguredProvider(nil), src.Providers...)
	for i := range out.Providers {
		out.Providers[i].Endpoints = cloneProviderEndpoints(src.Providers[i].Endpoints)
	}
	out.sensitiveProviders = make([]PortableRuntimeProviderSensitive, len(src.sensitiveProviders))
	for i, secret := range src.sensitiveProviders {
		out.sensitiveProviders[i] = PortableRuntimeProviderSensitive{
			providerName: secret.providerName, apiKey: secret.apiKey, headers: clonePortableRuntimeStringMap(secret.headers),
		}
	}
	return out
}
