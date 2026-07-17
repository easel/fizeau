package fizeau

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/easel/fizeau/internal/portableruntime"
	"github.com/easel/fizeau/internal/serviceimpl"
)

type portableRuntimeActivationLoader func(string, func(string) (string, bool)) (serviceimpl.PortableRuntimeActivation, error)

type portableRuntimeActivationState struct {
	options    ServiceOptions
	activation serviceimpl.PortableRuntimeActivation
	config     *portableRuntimeServiceConfig
}

func (s portableRuntimeActivationState) String() string {
	return fmt.Sprintf("{ProviderCount:%d}", s.providerCount())
}

func (s portableRuntimeActivationState) GoString() string { return s.String() }

func (s portableRuntimeActivationState) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProviderCount int `json:"provider_count"`
	}{ProviderCount: s.providerCount()})
}

func (s portableRuntimeActivationState) providerCount() int {
	if s.config == nil {
		return 0
	}
	return len(s.config.names)
}

func preparePortableRuntimeActivation(opts ServiceOptions, runtimeRoot string, lookupEnv func(string) (string, bool), loader portableRuntimeActivationLoader) (*portableRuntimeActivationState, error) {
	if opts.ConfigPath != "" || opts.ServiceConfig != nil {
		return nil, fmt.Errorf("%w: host configuration overrides are forbidden", portableruntime.ErrActivationInvalid)
	}
	if loader == nil {
		return nil, fmt.Errorf("%w: activation loader is unavailable", portableruntime.ErrActivationInvalid)
	}
	activation, err := loader(runtimeRoot, lookupEnv)
	if err != nil {
		return nil, err
	}
	config, err := newPortableRuntimeServiceConfig(activation.ConfiguredProviders(), "")
	if err != nil {
		return nil, fmt.Errorf("%w: provider reconstruction", portableruntime.ErrActivationInvalid)
	}
	opts.ServiceConfig = config
	return &portableRuntimeActivationState{options: opts, activation: activation, config: config}, nil
}

// portableRuntimeServiceConfig is the root-private inverse of the preparation
// projection. The storage phase binds its deferred WorkDir; service config
// session logs remain excluded, while the separate ServiceOptions override
// keeps its documented meaning.
type portableRuntimeServiceConfig struct {
	names          []string
	defaultName    string
	providers      map[string]ServiceProviderEntry
	healthCooldown time.Duration
	workDir        string
}

func (c portableRuntimeServiceConfig) String() string {
	return fmt.Sprintf("{ProviderCount:%d}", len(c.names))
}

func (c portableRuntimeServiceConfig) GoString() string { return c.String() }

func (c portableRuntimeServiceConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProviderCount int `json:"provider_count"`
	}{ProviderCount: len(c.names)})
}

func newPortableRuntimeServiceConfig(configured serviceimpl.PortableRuntimeConfiguredProviders, workDir string) (*portableRuntimeServiceConfig, error) {
	secrets := configured.SensitiveProviders()
	if len(configured.ProviderNames) != len(configured.Providers) || len(configured.Providers) != len(secrets) {
		return nil, fmt.Errorf("provider cardinality mismatch")
	}
	config := &portableRuntimeServiceConfig{
		names: append([]string(nil), configured.ProviderNames...), defaultName: configured.DefaultProviderName,
		providers: make(map[string]ServiceProviderEntry, len(configured.Providers)), healthCooldown: configured.HealthCooldown,
		workDir: workDir,
	}
	for i, provider := range configured.Providers {
		if provider.Name == "" || provider.Name != configured.ProviderNames[i] || secrets[i].ProviderName() != provider.Name {
			return nil, fmt.Errorf("provider identity mismatch")
		}
		entry := ServiceProviderEntry{
			Type: provider.Type, BaseURL: provider.BaseURL, ServerInstance: provider.ServerInstance,
			Endpoints: make([]ServiceProviderEndpoint, len(provider.Endpoints)), APIKey: secrets[i].APIKey(),
			Headers: secrets[i].Headers(), Model: provider.Model, Billing: provider.Billing,
			IncludeByDefault: provider.IncludeByDefault, IncludeByDefaultSet: provider.IncludeByDefaultSet,
			ContextWindow: provider.ContextWindow, ConfigError: provider.ConfigError,
			DailyTokenBudget: provider.DailyTokenBudget, CreditBalanceThresholdUSD: provider.CreditBalanceThresholdUSD,
			CreditProbeTTL: provider.CreditProbeTTL,
		}
		for endpointIndex, endpoint := range provider.Endpoints {
			entry.Endpoints[endpointIndex] = ServiceProviderEndpoint{Name: endpoint.Name, BaseURL: endpoint.BaseURL, ServerInstance: endpoint.ServerInstance}
		}
		config.providers[provider.Name] = entry
	}
	return config, nil
}

func (c *portableRuntimeServiceConfig) ProviderNames() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.names...)
}

func (c *portableRuntimeServiceConfig) DefaultProviderName() string {
	if c == nil {
		return ""
	}
	return c.defaultName
}

func (c *portableRuntimeServiceConfig) Provider(name string) (ServiceProviderEntry, bool) {
	if c == nil {
		return ServiceProviderEntry{}, false
	}
	entry, ok := c.providers[name]
	if !ok {
		return ServiceProviderEntry{}, false
	}
	entry.Endpoints = append([]ServiceProviderEndpoint(nil), entry.Endpoints...)
	entry.Headers = clonePortableRuntimeHeaders(entry.Headers)
	return entry, true
}

func (c *portableRuntimeServiceConfig) HealthCooldown() time.Duration {
	if c == nil {
		return 0
	}
	return c.healthCooldown
}

func (c *portableRuntimeServiceConfig) WorkDir() string {
	if c == nil {
		return ""
	}
	return c.workDir
}

func (c *portableRuntimeServiceConfig) SessionLogDir() string {
	return ""
}

func clonePortableRuntimeHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

var _ ServiceConfig = (*portableRuntimeServiceConfig)(nil)
