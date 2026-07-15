package serviceimpl_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func TestPortableRuntimeConfiguredProvidersPreserveUnpinnedCandidateSet(t *testing.T) {
	const (
		apiSecret    = "portable-api-secret-94f59b"
		headerSecret = "portable-header-secret-2cbade"
		hostWorkDir  = "/home/account-bearing-name/private-work"
		hostLogDir   = "/home/account-bearing-name/private-sessions"
	)
	providerNames := []string{"invalid", "pinned", "unhealthy", "endpoint"}
	headers := map[string]string{"Authorization": "Bearer " + headerSecret, "X-Tenant": "fixture"}
	endpoints := []serviceimpl.ProviderEndpoint{
		{Name: "east", BaseURL: "https://east.example/v1", ServerInstance: "east-1"},
		{Name: "west", BaseURL: "https://west.example/v1", ServerInstance: "west-1"},
	}
	providers := map[string]serviceimpl.ProviderEntry{
		"invalid": {
			Type:                      "openrouter",
			BaseURL:                   "https://router.example/v1",
			ServerInstance:            "router-1",
			Endpoints:                 endpoints,
			APIKey:                    apiSecret,
			Headers:                   headers,
			Model:                     "model-a",
			Billing:                   fizeau.BillingModelPerToken,
			IncludeByDefault:          true,
			IncludeByDefaultSet:       true,
			ContextWindow:             131072,
			ConfigError:               "unknown provider type",
			DailyTokenBudget:          54321,
			CreditBalanceThresholdUSD: 19.75,
			CreditProbeTTL:            47 * time.Minute,
		},
		"pinned": {
			Type:                "openai",
			Model:               "model-pinned",
			IncludeByDefault:    false,
			IncludeByDefaultSet: true,
		},
		"unhealthy": {
			Type:             "lmstudio",
			BaseURL:          "http://unhealthy.invalid/v1",
			ServerInstance:   "unhealthy-instance",
			IncludeByDefault: true,
		},
		"endpoint": {
			Type:      "vllm",
			Endpoints: []serviceimpl.ProviderEndpoint{{Name: "gpu", BaseURL: "http://gpu.invalid/v1", ServerInstance: "gpu-1"}},
		},
	}

	input := serviceimpl.PortableRuntimeConfiguredProvidersInput{
		ProviderNames:       providerNames,
		DefaultProviderName: "endpoint",
		Providers:           providers,
		HealthCooldown:      83 * time.Second,
		WorkDir:             hostWorkDir,
		SessionLogDir:       hostLogDir,
	}
	snapshot, err := serviceimpl.BuildPortableRuntimeConfiguredProviders(input)
	if err != nil {
		t.Fatalf("BuildPortableRuntimeConfiguredProviders() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot.ProviderNames, providerNames) {
		t.Fatalf("provider order = %#v, want %#v", snapshot.ProviderNames, providerNames)
	}
	if snapshot.DefaultProviderName != "endpoint" {
		t.Fatalf("default provider = %q, want endpoint", snapshot.DefaultProviderName)
	}
	if snapshot.HealthCooldown != 83*time.Second {
		t.Fatalf("health cooldown = %v, want 83s", snapshot.HealthCooldown)
	}
	if len(snapshot.Providers) != len(providerNames) {
		t.Fatalf("provider count = %d, want %d", len(snapshot.Providers), len(providerNames))
	}
	for i, name := range providerNames {
		if snapshot.Providers[i].Name != name {
			t.Fatalf("provider %d name = %q, want %q", i, snapshot.Providers[i].Name, name)
		}
	}

	got := snapshot.Providers[0]
	want := providers["invalid"]
	if got.Type != want.Type || got.BaseURL != want.BaseURL || got.ServerInstance != want.ServerInstance ||
		got.Model != want.Model || got.Billing != want.Billing || got.IncludeByDefault != want.IncludeByDefault ||
		got.IncludeByDefaultSet != want.IncludeByDefaultSet || got.ContextWindow != want.ContextWindow ||
		got.ConfigError != want.ConfigError || got.DailyTokenBudget != want.DailyTokenBudget ||
		got.CreditBalanceThresholdUSD != want.CreditBalanceThresholdUSD || got.CreditProbeTTL != want.CreditProbeTTL {
		t.Fatalf("structural provider = %#v, want all fields from %#v", got, want)
	}
	if !reflect.DeepEqual(got.Endpoints, endpoints) {
		t.Fatalf("endpoints = %#v, want %#v", got.Endpoints, endpoints)
	}
	if snapshot.Providers[1].IncludeByDefault || !snapshot.Providers[1].IncludeByDefaultSet {
		t.Fatalf("pinned-only inclusion changed: %#v", snapshot.Providers[1])
	}
	if snapshot.Providers[2].Name != "unhealthy" || snapshot.Providers[2].BaseURL == "" {
		t.Fatalf("unhealthy configured provider was filtered: %#v", snapshot.Providers[2])
	}
	if len(snapshot.Providers[3].Endpoints) != 1 {
		t.Fatalf("endpoint-bearing provider lost endpoints: %#v", snapshot.Providers[3])
	}

	if snapshot.WorkDir.Field != "WorkDir" || snapshot.WorkDir.Treatment != serviceimpl.PortableRuntimeConfigGuestPrivate ||
		snapshot.WorkDir.Reason != serviceimpl.PortableRuntimeWorkDirRemappedReason {
		t.Fatalf("WorkDir treatment = %#v", snapshot.WorkDir)
	}
	if snapshot.SessionLogDir.Field != "SessionLogDir" || snapshot.SessionLogDir.Treatment != serviceimpl.PortableRuntimeConfigExcluded ||
		snapshot.SessionLogDir.Reason != serviceimpl.PortableRuntimeSessionLogDirExcludedReason {
		t.Fatalf("SessionLogDir treatment = %#v", snapshot.SessionLogDir)
	}

	sensitive := snapshot.SensitiveProviders()
	if len(sensitive) != len(providerNames) || sensitive[0].ProviderName() != "invalid" || sensitive[0].APIKey() != apiSecret ||
		sensitive[0].Headers()["Authorization"] != "Bearer "+headerSecret {
		t.Fatalf("sensitive provider projection did not preserve values")
	}

	// Every returned collection is detached from both the input and snapshot.
	providerNames[0] = "mutated-input"
	endpoints[0].Name = "mutated-input"
	headers["Authorization"] = "mutated-input"
	sensitiveHeaders := sensitive[0].Headers()
	sensitiveHeaders["Authorization"] = "mutated-return"
	if snapshot.ProviderNames[0] != "invalid" || snapshot.Providers[0].Endpoints[0].Name != "east" ||
		snapshot.SensitiveProviders()[0].Headers()["Authorization"] != "Bearer "+headerSecret {
		t.Fatal("portable provider snapshot aliases mutable input or returned data")
	}

	for label, diagnostic := range map[string]string{
		"json input":      mustJSON(t, input),
		"string input":    input.String(),
		"fmt input":       fmt.Sprintf("%v %+v %#v", input, input, input),
		"json snapshot":   mustJSON(t, snapshot),
		"string snapshot": snapshot.String(),
		"fmt snapshot":    fmt.Sprintf("%v %+v %#v", snapshot, snapshot, snapshot),
		"json sensitive":  mustJSON(t, sensitive),
		"fmt sensitive":   fmt.Sprintf("%v %+v %#v", sensitive, sensitive, sensitive),
	} {
		for _, forbidden := range []string{apiSecret, headerSecret, hostWorkDir, hostLogDir, "account-bearing-name"} {
			if strings.Contains(diagnostic, forbidden) {
				t.Fatalf("%s leaks %q: %s", label, forbidden, diagnostic)
			}
		}
	}
}

func TestPortableRuntimeConfiguredProvidersFieldParity(t *testing.T) {
	publicType := reflect.TypeOf(fizeau.ServiceProviderEntry{})
	inputType := reflect.TypeOf(serviceimpl.ProviderEntry{})
	structuralType := reflect.TypeOf(serviceimpl.PortableRuntimeConfiguredProvider{})
	sensitive := map[string]bool{"APIKey": true, "Headers": true}
	wantFields := []string{
		"Type", "BaseURL", "ServerInstance", "Endpoints", "APIKey", "Headers", "Model", "Billing",
		"IncludeByDefault", "IncludeByDefaultSet", "ContextWindow", "ConfigError", "DailyTokenBudget",
		"CreditBalanceThresholdUSD", "CreditProbeTTL",
	}
	if publicType.NumField() != len(wantFields) {
		t.Fatalf("ServiceProviderEntry has %d fields, classified parity table has %d", publicType.NumField(), len(wantFields))
	}
	if inputType.NumField() != len(wantFields) {
		t.Fatalf("serviceimpl.ProviderEntry has %d fields, want %d", inputType.NumField(), len(wantFields))
	}
	if want := len(wantFields) - len(sensitive) + 1; structuralType.NumField() != want {
		t.Fatalf("PortableRuntimeConfiguredProvider has %d fields, classified provider name plus structural fields total %d", structuralType.NumField(), want)
	}
	for i, name := range wantFields {
		if publicType.Field(i).Name != name {
			t.Fatalf("ServiceProviderEntry field %d = %q, parity table expects %q", i, publicType.Field(i).Name, name)
		}
		inputField, ok := inputType.FieldByName(name)
		if !ok {
			t.Fatalf("serviceimpl.ProviderEntry does not project %s", name)
		}
		if inputField.Type != publicType.Field(i).Type && name != "Endpoints" {
			t.Fatalf("serviceimpl.ProviderEntry.%s type = %v, want %v", name, inputField.Type, publicType.Field(i).Type)
		}
		_, structural := structuralType.FieldByName(name)
		if sensitive[name] == structural {
			t.Fatalf("PortableRuntimeConfiguredProvider field %s classification mismatch: sensitive=%v structural=%v", name, sensitive[name], structural)
		}
	}

	publicEndpointType := reflect.TypeOf(fizeau.ServiceProviderEndpoint{})
	inputEndpointType := reflect.TypeOf(serviceimpl.ProviderEndpoint{})
	wantEndpointFields := []string{"Name", "BaseURL", "ServerInstance"}
	if publicEndpointType.NumField() != len(wantEndpointFields) || inputEndpointType.NumField() != len(wantEndpointFields) {
		t.Fatalf("endpoint parity field counts: public=%d internal=%d want=%d", publicEndpointType.NumField(), inputEndpointType.NumField(), len(wantEndpointFields))
	}
	for i, name := range wantEndpointFields {
		publicField := publicEndpointType.Field(i)
		inputField := inputEndpointType.Field(i)
		if publicField.Name != name || inputField.Name != name {
			t.Fatalf("endpoint field %d: public=%q internal=%q want=%q", i, publicField.Name, inputField.Name, name)
		}
		if inputField.Type != publicField.Type {
			t.Fatalf("serviceimpl.ProviderEndpoint.%s type = %v, want %v", name, inputField.Type, publicField.Type)
		}
	}

	interfaceType := reflect.TypeOf((*fizeau.ServiceConfig)(nil)).Elem()
	wantMethods := map[string]bool{
		"ProviderNames": true, "DefaultProviderName": true, "Provider": true,
		"HealthCooldown": true, "WorkDir": true, "SessionLogDir": true,
	}
	if interfaceType.NumMethod() != len(wantMethods) {
		t.Fatalf("ServiceConfig has %d methods, parity table classifies %d", interfaceType.NumMethod(), len(wantMethods))
	}
	for i := 0; i < interfaceType.NumMethod(); i++ {
		name := interfaceType.Method(i).Name
		if !wantMethods[name] {
			t.Fatalf("ServiceConfig method %q has no portable classification", name)
		}
	}
}

func TestPortableRuntimeConfiguredProvidersErrorsAreSecretSafe(t *testing.T) {
	_, err := serviceimpl.BuildPortableRuntimeConfiguredProviders(serviceimpl.PortableRuntimeConfiguredProvidersInput{
		ProviderNames: []string{"missing"},
		Providers: map[string]serviceimpl.ProviderEntry{
			"other": {APIKey: "missing-error-api-secret", Headers: map[string]string{"Authorization": "missing-error-header-secret"}},
		},
		WorkDir:       "/home/private-account/work",
		SessionLogDir: "/home/private-account/sessions",
	})
	if err == nil {
		t.Fatal("missing configured provider accepted")
	}
	diagnostic := err.Error()
	for _, forbidden := range []string{"missing-error-api-secret", "missing-error-header-secret", "private-account"} {
		if strings.Contains(diagnostic, forbidden) {
			t.Fatalf("error leaks %q: %v", forbidden, err)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	return string(data)
}
