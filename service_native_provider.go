package fizeau

import (
	"fmt"
	"strings"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/provider/quotaheaders"
	"github.com/easel/fizeau/internal/serviceimpl"
)

type nativeProviderResolution struct {
	Provider agentcore.Provider
	Name     string
	Entry    ServiceProviderEntry
}

func (s *service) resolveConfiguredNativeProvider(req ServiceExecuteRequest) nativeProviderResolution {
	sc := s.opts.ServiceConfig
	if sc == nil {
		return nativeProviderResolution{}
	}
	unreachableProviders := s.getUnreachableProvidersForNativeResolution()
	name, entry, ok := selectConfiguredNativeProviderWithReachability(sc, req, unreachableProviders)
	if !ok {
		return nativeProviderResolution{}
	}
	if req.Model != "" {
		entry.Model = req.Model
	}
	provider := serviceimpl.BuildNativeProvider(serviceimpl.NativeProviderBuildInput{
		Name:                name,
		Entry:               serviceImplProviderEntry(entry),
		QuotaSignalObserver: s.quotaSignalObserver(name),
	})
	if provider == nil {
		return nativeProviderResolution{Name: name, Entry: entry}
	}
	return nativeProviderResolution{Provider: provider, Name: name, Entry: entry}
}

func selectConfiguredNativeProvider(sc ServiceConfig, req ServiceExecuteRequest) (string, ServiceProviderEntry, bool) {
	return selectConfiguredNativeProviderWithReachability(sc, req, nil)
}

func selectConfiguredNativeProviderWithReachability(sc ServiceConfig, req ServiceExecuteRequest, unreachableProviders map[string]bool) (string, ServiceProviderEntry, bool) {
	if req.Provider != "" {
		if entry, ok := sc.Provider(req.Provider); ok {
			return req.Provider, entry, true
		}
		if name, entry, ok := selectConfiguredEndpointProvider(sc, req.Provider); ok {
			return name, entry, true
		}
	}

	wantedType := requestedNativeProviderType(req)
	if wantedType != "" {
		if name := sc.DefaultProviderName(); name != "" {
			if entry, ok := sc.Provider(name); ok && normalizeServiceProviderType(entry.Type) == wantedType {
				return name, entry, true
			}
		}
		for _, name := range sc.ProviderNames() {
			entry, ok := sc.Provider(name)
			if ok && normalizeServiceProviderType(entry.Type) == wantedType {
				return name, entry, true
			}
		}
	}

	if req.Provider == "" && wantedType == "" {
		name := sc.DefaultProviderName()
		if name == "" {
			return "", ServiceProviderEntry{}, false
		}
		if unreachableProviders != nil && unreachableProviders[name] {
			return "", ServiceProviderEntry{}, false
		}
		entry, ok := sc.Provider(name)
		return name, entry, ok
	}

	return "", ServiceProviderEntry{}, false
}

func selectConfiguredEndpointProvider(sc ServiceConfig, ref string) (string, ServiceProviderEntry, bool) {
	providerName, endpointName, ok := splitEndpointProviderRef(ref)
	if !ok {
		return "", ServiceProviderEntry{}, false
	}
	entry, ok := sc.Provider(providerName)
	if !ok {
		return "", ServiceProviderEntry{}, false
	}
	for _, endpoint := range modelDiscoveryEndpoints(entry) {
		if endpoint.Name != endpointName {
			continue
		}
		entry.BaseURL = endpoint.BaseURL
		entry.ServerInstance = endpoint.ServerInstance
		entry.Endpoints = []ServiceProviderEndpoint{{Name: endpoint.Name, BaseURL: endpoint.BaseURL}}
		return ref, entry, true
	}
	return "", ServiceProviderEntry{}, false
}

func endpointProviderRef(providerName, endpointName string) string {
	if endpointName == "" {
		return providerName
	}
	return providerName + "@" + endpointName
}

func splitEndpointProviderRef(ref string) (string, string, bool) {
	providerName, endpointName, ok := strings.Cut(ref, "@")
	if !ok || providerName == "" || endpointName == "" {
		return "", "", false
	}
	return providerName, endpointName, true
}

func requestedNativeProviderType(req ServiceExecuteRequest) string {
	if req.Provider != "" {
		return normalizeServiceProviderType(req.Provider)
	}
	switch req.Harness {
	case "", "fiz":
		return ""
	default:
		return normalizeServiceProviderType(req.Harness)
	}
}

func (s *service) nativeProviderNotConfiguredError(req ServiceExecuteRequest, decision RouteDecision) string {
	wantedType := requestedNativeProviderType(req)
	if wantedType == "" {
		errMsg := "orphan model: " + decision.Model
		if decision.Model == "" {
			errMsg = "no provider configured for native harness"
		}
		return errMsg
	}
	available := s.availableProviderTypes()
	harness := decision.Harness
	if harness == "" {
		harness = "fiz"
	}
	return fmt.Sprintf("harness %q: no configured provider matches type %q (available: %s)", harness, wantedType, available)
}

func (s *service) availableProviderTypes() string {
	sc := s.opts.ServiceConfig
	if sc == nil {
		return "[]"
	}
	var parts []string
	for _, name := range sc.ProviderNames() {
		entry, ok := sc.Provider(name)
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, normalizeServiceProviderType(entry.Type)))
	}
	if len(parts) == 0 {
		return "[]"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// quotaSignalObserver returns a callback that updates the provider quota
// state machine when a parsed rate-limit signal indicates the provider's
// subscription/daily cap has been hit (or imminently will be). Returns nil
// when the service has no quota store, which makes the provider middleware
// a no-op.
func (s *service) quotaSignalObserver(providerName string) func(quotaheaders.Signal) {
	if s == nil || s.providerQuota == nil || providerName == "" {
		return nil
	}
	store := s.providerQuota
	return func(signal quotaheaders.Signal) {
		now := time.Now()
		exhausted, retryAt := signal.IsExhausted(now)
		if !exhausted {
			return
		}
		if retryAt.IsZero() {
			// Provider said "exhausted" but gave us no reset window. Fall
			// back to a short cooldown so we don't peg the provider in the
			// excluded set forever; the next response will refresh us.
			retryAt = now.Add(time.Minute)
		}
		store.MarkQuotaExhausted(providerName, retryAt)
	}
}

func (s *service) getUnreachableProvidersForNativeResolution() map[string]bool {
	if s == nil || s.providerProbe == nil {
		return nil
	}
	unreachable := make(map[string]bool)
	for name := range s.probeUnreachableProviders(time.Now().UTC()) {
		unreachable[name] = true
	}
	if len(unreachable) == 0 {
		return nil
	}
	return unreachable
}
