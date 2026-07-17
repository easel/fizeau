package serviceimpl

import (
	"context"
	"fmt"

	"github.com/easel/fizeau/internal/harnesses"
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

func (a PortableRuntimeActivation) BackingRoot() string { return a.plan.BackingRoot() }
func (a PortableRuntimeActivation) WorkDir() string     { return a.plan.WorkDir() }
func (a PortableRuntimeActivation) SessionLogDir() string {
	return a.plan.SessionLogDir()
}

func (a PortableRuntimeActivation) EntrypointEnvironment(name string) (map[string]string, bool) {
	return a.plan.EntrypointEnvironment(name)
}

func (a PortableRuntimeActivation) EntrypointRecipe(name string) (portableruntime.ActivationRecipe, bool) {
	return a.plan.EntrypointRecipe(name)
}

// BindPortableRuntimeRouteRunners installs manifest-owned launch state on the
// same structural prototypes that RouteRunnerAuthority later exact-clones.
// It is deliberately process-free and uses only the generic runner binder.
func (a PortableRuntimeActivation) BindPortableRuntimeRouteRunners(
	structural map[string]harnesses.Harness,
	factory harnesses.RouteRunnerFactory,
) (*harnesses.RouteRunnerAuthority, error) {
	manifest := a.plan.Manifest()
	surfaces := make(map[string]portableruntime.ManifestSurface, len(manifest.Inventory))
	for _, surface := range manifest.Inventory {
		surfaces[surface.Name] = surface
	}
	bound := make(map[string]harnesses.Harness, len(structural))
	for name, prototype := range structural {
		surface, exists := surfaces[name]
		if !exists || prototype == nil {
			return nil, portableRuntimeRunnerBindingError("structural prototype identity")
		}
		binder, ok := prototype.(harnesses.PortableRuntimeRunnerBinder)
		if !ok {
			return nil, portableRuntimeRunnerBindingError("structural prototype binding capability")
		}
		structure := harnesses.PortableRuntimeStructure{Name: name, Transport: surface.Transport}
		switch surface.Inclusion {
		case harnesses.PortableRuntimeInclusionRequired:
			structure.Mode = harnesses.PortableRuntimeStructuralUnpinned
		case harnesses.PortableRuntimeInclusionExactPinOnly:
			structure.Mode = harnesses.PortableRuntimeStructuralExactPinOnly
		case harnesses.PortableRuntimeInclusionNonSubprocess:
			structure.Mode = harnesses.PortableRuntimeStructuralNonSubprocess
		default:
			return nil, portableRuntimeRunnerBindingError("structural prototype inclusion")
		}
		input := harnesses.PortableRuntimeRunnerBindingInput{Structure: structure}
		if surface.Transport == harnesses.PortableRuntimeTransportSubprocess {
			entrypoint, entrypointExists := manifest.Entrypoints[name]
			environment, environmentExists := a.plan.EntrypointEnvironment(name)
			recipe, recipeExists := a.plan.EntrypointRecipe(name)
			if !entrypointExists || !environmentExists || !recipeExists {
				return nil, portableRuntimeRunnerBindingError("subprocess entrypoint binding")
			}
			input.GuestRoot = manifest.GuestRoot
			input.ClosureClass = entrypoint.ClosureClass
			input.Launch = entrypoint.Launch
			input.FixedArguments = entrypoint.ExecutionConstraints.FixedArguments
			input.FixedOptionValues = entrypoint.ExecutionConstraints.FixedOptionValues
			input.Environment = environment
			input.NamespaceRecipe = recipe
		} else if _, entrypointExists := manifest.Entrypoints[name]; entrypointExists {
			return nil, portableRuntimeRunnerBindingError("non-subprocess entrypoint binding")
		}
		binding, err := harnesses.NewPortableRuntimeRunnerBinding(input)
		if err != nil || binder.BindPortableRuntime(binding) != nil {
			return nil, portableRuntimeRunnerBindingError("structural prototype binding")
		}
		descriptor, ok := prototype.(harnesses.PortableRuntimeStructuralHarness)
		if !ok || descriptor.PortableRuntimeStructure() != structure {
			return nil, portableRuntimeRunnerBindingError("manifest transport binding")
		}
		bound[name] = prototype
	}
	for name := range manifest.Entrypoints {
		if bound[name] == nil {
			return nil, portableRuntimeRunnerBindingError("required structural prototype")
		}
	}
	return harnesses.NewRouteRunnerAuthority(bound, factory), nil
}

func portableRuntimeRunnerBindingError(reason string) error {
	return fmt.Errorf("%w: %s", portableruntime.ErrActivationInvalid, reason)
}

// LoadPortableRuntimeActivation verifies the private bundle and reconstructs
// the API-neutral effective provider configuration without consulting the
// application config loader or starting service activity.
func LoadPortableRuntimeActivation(runtimeRoot string, lookupEnv func(string) (string, bool)) (PortableRuntimeActivation, error) {
	plan, err := portableruntime.LoadActivation(runtimeRoot, lookupEnv)
	if err != nil {
		return PortableRuntimeActivation{}, err
	}
	return portableRuntimeActivationFromPlan(plan)
}

// AssemblePortableRuntimeActivation adds caller-owned writable storage and
// closed per-entrypoint environments without starting a process.
func AssemblePortableRuntimeActivation(ctx context.Context, runtimeRoot, writableRoot string, lookupEnv func(string) (string, bool)) (PortableRuntimeActivation, error) {
	plan, err := portableruntime.AssembleActivation(ctx, runtimeRoot, writableRoot, lookupEnv)
	if err != nil {
		return PortableRuntimeActivation{}, err
	}
	return portableRuntimeActivationFromPlan(plan)
}

func assemblePortableRuntimeActivationWithIdentity(ctx context.Context, runtimeRoot, writableRoot string, lookupEnv func(string) (string, bool), reader portableruntime.ActivationIdentityReader) (PortableRuntimeActivation, error) {
	plan, err := portableruntime.AssembleActivationWithIdentityReader(ctx, runtimeRoot, writableRoot, lookupEnv, reader)
	if err != nil {
		return PortableRuntimeActivation{}, err
	}
	return portableRuntimeActivationFromPlan(plan)
}

func portableRuntimeActivationFromPlan(plan portableruntime.ActivationPlan) (PortableRuntimeActivation, error) {
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
