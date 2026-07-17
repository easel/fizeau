package fizeau

import (
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// harnessInstanceHook, when non-nil, is applied to the default harness map
// before it is returned by defaultHarnessInstances. Tests use this hook to
// substitute fake implementations without modifying service.go or requiring
// a factory parameter on New(). Must be restored after each test (use
// t.Cleanup). Production code must never set this variable.
var harnessInstanceHook func(map[string]harnesses.Harness) map[string]harnesses.Harness

// defaultHarnessInstances returns the production map of registered
// Harness implementations keyed by harness name. Only subprocess
// harnesses with concrete Runner types appear here; embedded
// ("fiz", "virtual", "script") and HTTP-only providers do not own
// quota/account state and are deliberately omitted — the scheduler
// treats absence as "no QuotaHarness/AccountHarness behavior".
//
// This file intentionally stays on the interface side of CONTRACT-004:
// concrete runner construction lives under internal/harnesses/.
func defaultHarnessInstances() map[string]harnesses.Harness {
	instances := builtin.Instances()
	if harnessInstanceHook != nil {
		instances = harnessInstanceHook(instances)
	}
	return instances
}

// defaultRouteRunnerAuthority creates the service's sole runner authority.
// Structural inventory and exact execution bindings share this owner, while
// concrete construction stays inside the built-in marketplace boundary.
func defaultRouteRunnerAuthority() *harnesses.RouteRunnerAuthority {
	return harnesses.NewRouteRunnerAuthority(defaultHarnessInstances(), builtin.NewRouteRunner)
}

// portableRuntimeInventory joins registry metadata to this service's actual
// runner-instance map. Preparation will consume this seam in a later bead;
// keeping the join here prevents a second static runner registry from becoming
// a competing authority.
func (s *service) portableRuntimeInventory() ([]harnesses.PortableRuntimeSurface, error) {
	if s == nil || s.routeRunners == nil {
		return harnesses.BuildPortableRuntimeInventory(s.registry, nil)
	}
	return harnesses.BuildPortableRuntimeInventory(s.registry, s.routeRunners.StructuralInstances())
}

// portableRuntimeConfiguredProviders projects the effective ServiceConfig
// through the same root-to-serviceimpl adapter used by production provider
// paths. It deliberately consults no health, quota, catalog, or route state.
func (s *service) portableRuntimeConfiguredProviders() (serviceimpl.PortableRuntimeConfiguredProviders, error) {
	input := serviceimpl.PortableRuntimeConfiguredProvidersInput{}
	if s.opts.ServiceConfig == nil {
		return serviceimpl.BuildPortableRuntimeConfiguredProviders(input)
	}

	config := s.opts.ServiceConfig
	input.ProviderNames = append([]string(nil), config.ProviderNames()...)
	input.DefaultProviderName = config.DefaultProviderName()
	input.Providers = make(map[string]serviceimpl.ProviderEntry, len(input.ProviderNames))
	for _, name := range input.ProviderNames {
		entry, ok := config.Provider(name)
		if !ok {
			continue
		}
		input.Providers[name] = serviceImplProviderEntry(entry)
	}
	input.HealthCooldown = config.HealthCooldown()
	input.WorkDir = config.WorkDir()
	input.SessionLogDir = config.SessionLogDir()
	return serviceimpl.BuildPortableRuntimeConfiguredProviders(input)
}
