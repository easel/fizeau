package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/portableruntime"
)

// PortableRuntimePrepareInput is the narrow route-neutral materialization
// seam. The root facade supplies its already-authoritative inventory and
// configured-provider snapshots; this bridge performs no discovery itself.
type PortableRuntimePrepareInput struct {
	DestinationRoot     string
	Target              harnesses.PortableRuntimeTarget
	Inventory           []harnesses.PortableRuntimeSurface
	ConfiguredProviders PortableRuntimeConfiguredProviders
}

func (input PortableRuntimePrepareInput) String() string {
	return fmt.Sprintf("{TargetGOOS:%q TargetGOARCH:%q InventoryCount:%d ProviderCount:%d}", input.Target.GOOS, input.Target.GOARCH, len(input.Inventory), len(input.ConfiguredProviders.Providers))
}

func (input PortableRuntimePrepareInput) GoString() string { return input.String() }

func (input PortableRuntimePrepareInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		TargetGOOS     string `json:"target_goos"`
		TargetGOARCH   string `json:"target_goarch"`
		InventoryCount int    `json:"inventory_count"`
		ProviderCount  int    `json:"provider_count"`
	}{input.Target.GOOS, input.Target.GOARCH, len(input.Inventory), len(input.ConfiguredProviders.Providers)})
}

// PreparePortableRuntime maps the service-owned neutral snapshots into the
// private materializer without selecting a route or contacting a provider.
func PreparePortableRuntime(ctx context.Context, input PortableRuntimePrepareInput) (*portableruntime.Bundle, error) {
	providers := input.ConfiguredProviders
	snapshot := portableruntime.ProviderSnapshot{
		ProviderNames:       append([]string(nil), providers.ProviderNames...),
		DefaultProviderName: providers.DefaultProviderName,
		HealthCooldown:      providers.HealthCooldown,
		WorkDir: portableruntime.ConfigField{
			Field:     providers.WorkDir.Field,
			Treatment: string(providers.WorkDir.Treatment),
			Reason:    providers.WorkDir.Reason,
		},
		SessionLogDir: portableruntime.ConfigField{
			Field:     providers.SessionLogDir.Field,
			Treatment: string(providers.SessionLogDir.Treatment),
			Reason:    providers.SessionLogDir.Reason,
		},
		Providers: make([]portableruntime.ConfiguredProvider, len(providers.Providers)),
	}
	for index, provider := range providers.Providers {
		mapped := portableruntime.ConfiguredProvider{
			Name:                      provider.Name,
			Type:                      provider.Type,
			BaseURL:                   provider.BaseURL,
			ServerInstance:            provider.ServerInstance,
			Endpoints:                 make([]portableruntime.ProviderEndpoint, len(provider.Endpoints)),
			Model:                     provider.Model,
			Billing:                   string(provider.Billing),
			IncludeByDefault:          provider.IncludeByDefault,
			IncludeByDefaultSet:       provider.IncludeByDefaultSet,
			ContextWindow:             provider.ContextWindow,
			ConfigError:               provider.ConfigError,
			DailyTokenBudget:          provider.DailyTokenBudget,
			CreditBalanceThresholdUSD: provider.CreditBalanceThresholdUSD,
			CreditProbeTTL:            provider.CreditProbeTTL,
		}
		for endpointIndex, endpoint := range provider.Endpoints {
			mapped.Endpoints[endpointIndex] = portableruntime.ProviderEndpoint{
				Name: endpoint.Name, BaseURL: endpoint.BaseURL, ServerInstance: endpoint.ServerInstance,
			}
		}
		snapshot.Providers[index] = mapped
	}

	sensitive := providers.SensitiveProviders()
	secrets := make([]portableruntime.ProviderSecret, len(sensitive))
	for index, provider := range sensitive {
		secrets[index] = portableruntime.NewProviderSecret(provider.ProviderName(), provider.APIKey(), provider.Headers())
	}
	return portableruntime.Prepare(ctx, portableruntime.Request{
		DestinationRoot: input.DestinationRoot,
		Target:          input.Target,
		Inventory:       append([]harnesses.PortableRuntimeSurface(nil), input.Inventory...),
		Providers:       snapshot,
		ProviderSecrets: secrets,
	})
}
