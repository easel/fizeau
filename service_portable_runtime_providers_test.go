package fizeau

import (
	"reflect"
	"testing"
	"time"
)

func TestPortableRuntimeConfiguredProvidersRootProjection(t *testing.T) {
	entry := ServiceProviderEntry{
		Type:                      "openrouter",
		BaseURL:                   "https://router.example/v1",
		ServerInstance:            "router-1",
		Endpoints:                 []ServiceProviderEndpoint{{Name: "primary", BaseURL: "https://primary.example/v1", ServerInstance: "primary-1"}},
		APIKey:                    "root-projection-api-secret",
		Headers:                   map[string]string{"Authorization": "root-projection-header-secret"},
		Model:                     "model-a",
		Billing:                   BillingModelPerToken,
		IncludeByDefault:          false,
		IncludeByDefaultSet:       true,
		ContextWindow:             65536,
		ConfigError:               "fixture invalid provider",
		DailyTokenBudget:          12345,
		CreditBalanceThresholdUSD: 7.25,
		CreditProbeTTL:            29 * time.Minute,
	}
	config := &fakeServiceConfig{
		providers:      map[string]ServiceProviderEntry{"configured": entry},
		names:          []string{"configured"},
		defaultName:    "configured",
		healthCooldown: 71 * time.Second,
		workDir:        "/home/root-projection-account/work",
	}
	svc := &service{opts: ServiceOptions{ServiceConfig: config}}

	snapshot, err := svc.portableRuntimeConfiguredProviders()
	if err != nil {
		t.Fatalf("portableRuntimeConfiguredProviders() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot.ProviderNames, config.names) || snapshot.DefaultProviderName != config.defaultName ||
		snapshot.HealthCooldown != config.healthCooldown || len(snapshot.Providers) != 1 {
		t.Fatalf("root projection metadata = %#v", snapshot)
	}
	got := snapshot.Providers[0]
	if got.Name != "configured" || got.Type != entry.Type || got.BaseURL != entry.BaseURL || got.ServerInstance != entry.ServerInstance ||
		got.Model != entry.Model || got.Billing != entry.Billing || got.IncludeByDefault != entry.IncludeByDefault ||
		got.IncludeByDefaultSet != entry.IncludeByDefaultSet || got.ContextWindow != entry.ContextWindow ||
		got.ConfigError != entry.ConfigError || got.DailyTokenBudget != entry.DailyTokenBudget ||
		got.CreditBalanceThresholdUSD != entry.CreditBalanceThresholdUSD || got.CreditProbeTTL != entry.CreditProbeTTL {
		t.Fatalf("root adapter dropped provider fields: got %#v want %#v", got, entry)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].Name != entry.Endpoints[0].Name ||
		got.Endpoints[0].BaseURL != entry.Endpoints[0].BaseURL || got.Endpoints[0].ServerInstance != entry.Endpoints[0].ServerInstance {
		t.Fatalf("root adapter dropped endpoints: %#v", got.Endpoints)
	}
	sensitive := snapshot.SensitiveProviders()
	if len(sensitive) != 1 || sensitive[0].APIKey() != entry.APIKey ||
		sensitive[0].Headers()["Authorization"] != entry.Headers["Authorization"] {
		t.Fatal("root adapter dropped sensitive provider fields")
	}
}
