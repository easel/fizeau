package fizeau

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/portableruntime"
	"github.com/easel/fizeau/internal/serviceimpl"
)

type panicPortableServiceConfig struct{}

func (panicPortableServiceConfig) ProviderNames() []string { panic("host ServiceConfig inspected") }
func (panicPortableServiceConfig) DefaultProviderName() string {
	panic("host ServiceConfig inspected")
}
func (panicPortableServiceConfig) Provider(string) (ServiceProviderEntry, bool) {
	panic("host ServiceConfig inspected")
}
func (panicPortableServiceConfig) HealthCooldown() time.Duration {
	panic("host ServiceConfig inspected")
}
func (panicPortableServiceConfig) WorkDir() string       { panic("host ServiceConfig inspected") }
func (panicPortableServiceConfig) SessionLogDir() string { panic("host ServiceConfig inspected") }

func TestPortableRuntimeActivationRejectsHostOverrides(t *testing.T) {
	previousLoader := loadServiceConfig
	loadServiceConfig = func(string) (ServiceConfig, error) { panic("ambient config loader called") }
	t.Cleanup(func() { loadServiceConfig = previousLoader })

	for _, test := range []struct {
		name string
		opts ServiceOptions
	}{
		{name: "ConfigPath", opts: ServiceOptions{ConfigPath: "/host/config.yaml"}},
		{name: "ServiceConfig", opts: ServiceOptions{ServiceConfig: panicPortableServiceConfig{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			loaderCalls := 0
			_, err := preparePortableRuntimeActivation(test.opts, "/must-not-be-read", func(string) (string, bool) {
				t.Fatal("environment read before host override rejection")
				return "", false
			}, func(string, func(string) (string, bool)) (serviceimpl.PortableRuntimeActivation, error) {
				loaderCalls++
				return serviceimpl.PortableRuntimeActivation{}, nil
			})
			if !errors.Is(err, portableruntime.ErrActivationInvalid) || loaderCalls != 0 {
				t.Fatalf("preflight = (%v, calls=%d)", err, loaderCalls)
			}
			svc, err := NewFromPortableRuntime(test.opts)
			if svc != nil || !errors.Is(err, ErrPortableRuntimeActivationInvalid) {
				t.Fatalf("NewFromPortableRuntime() = (%v, %v)", svc, err)
			}
		})
	}
}

func TestPortableRuntimeActivationRebuildsServiceConfigFieldForField(t *testing.T) {
	entry := serviceimpl.ProviderEntry{
		Type: "openai-compatible", BaseURL: "https://provider.invalid/v1", ServerInstance: "instance-a",
		Endpoints: []serviceimpl.ProviderEndpoint{{Name: "east", BaseURL: "https://east.invalid/v1", ServerInstance: "east-a"}},
		APIKey:    "activation-api-key", Headers: map[string]string{"Authorization": "Bearer activation-header"},
		Model: "model-a", Billing: modelcatalog.BillingModelPerToken,
		IncludeByDefault: true, IncludeByDefaultSet: true, ContextWindow: 131072,
		ConfigError: "safe structural diagnostic", DailyTokenBudget: 123456,
		CreditBalanceThresholdUSD: 18.75, CreditProbeTTL: 17 * time.Minute,
	}
	configured, err := serviceimpl.BuildPortableRuntimeConfiguredProviders(serviceimpl.PortableRuntimeConfiguredProvidersInput{
		ProviderNames: []string{"provider-a"}, DefaultProviderName: "provider-a",
		Providers: map[string]serviceimpl.ProviderEntry{"provider-a": entry}, HealthCooldown: 43 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := newPortableRuntimeServiceConfig(configured, "/guest/work")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := config.Provider("provider-a")
	want := ServiceProviderEntry{
		Type: entry.Type, BaseURL: entry.BaseURL, ServerInstance: entry.ServerInstance,
		Endpoints: []ServiceProviderEndpoint{{Name: "east", BaseURL: "https://east.invalid/v1", ServerInstance: "east-a"}},
		APIKey:    entry.APIKey, Headers: entry.Headers, Model: entry.Model, Billing: entry.Billing,
		IncludeByDefault: entry.IncludeByDefault, IncludeByDefaultSet: entry.IncludeByDefaultSet,
		ContextWindow: entry.ContextWindow, ConfigError: entry.ConfigError, DailyTokenBudget: entry.DailyTokenBudget,
		CreditBalanceThresholdUSD: entry.CreditBalanceThresholdUSD, CreditProbeTTL: entry.CreditProbeTTL,
	}
	if !ok || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(config.ProviderNames(), []string{"provider-a"}) ||
		config.DefaultProviderName() != "provider-a" || config.HealthCooldown() != 43*time.Second ||
		config.WorkDir() != "/guest/work" || config.SessionLogDir() != "" {
		t.Fatalf("reconstructed ServiceConfig = %#v, provider %#v", config, got)
	}
	state := portableRuntimeActivationState{config: config, options: ServiceOptions{ServiceConfig: config}}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := fmt.Sprintf("%v %+v %#v %s %v %+v %#v %s", config, config, config, configJSON, state, state, state, stateJSON)
	for _, forbidden := range []string{entry.APIKey, entry.Headers["Authorization"], "activation-header"} {
		if strings.Contains(diagnostics, forbidden) {
			t.Fatalf("activation config diagnostics leak %q: %s", forbidden, diagnostics)
		}
	}
	got.Headers["Authorization"] = "mutated"
	got.Endpoints[0].Name = "mutated"
	again, _ := config.Provider("provider-a")
	if again.Headers["Authorization"] != entry.Headers["Authorization"] || again.Endpoints[0].Name != entry.Endpoints[0].Name {
		t.Fatal("ServiceConfig.Provider aliases internal state")
	}

	rootType := reflect.TypeOf(ServiceProviderEntry{})
	bridgeType := reflect.TypeOf(serviceimpl.ProviderEntry{})
	if rootType.NumField() != bridgeType.NumField() {
		t.Fatalf("provider field count root=%d bridge=%d", rootType.NumField(), bridgeType.NumField())
	}
	for i := 0; i < rootType.NumField(); i++ {
		rootField, bridgeField := rootType.Field(i), bridgeType.Field(i)
		if rootField.Name != bridgeField.Name {
			t.Fatalf("provider field %d = %s/%s", i, rootField.Name, bridgeField.Name)
		}
		if rootField.Type != bridgeField.Type && rootField.Name != "Endpoints" {
			t.Fatalf("provider field %s type = %v/%v", rootField.Name, rootField.Type, bridgeField.Type)
		}
	}
}
