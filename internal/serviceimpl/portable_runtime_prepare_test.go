package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/portableruntime"
)

type portablePrepareTestHarness struct {
	contribution harnesses.PortableRuntimeContribution
}

func (portablePrepareTestHarness) Info() harnesses.HarnessInfo       { panic("unexpected Info") }
func (portablePrepareTestHarness) HealthCheck(context.Context) error { panic("unexpected HealthCheck") }
func (portablePrepareTestHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("unexpected Execute")
}
func (h portablePrepareTestHarness) PortableRuntimeAssets(context.Context, harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	return h.contribution, nil
}

func TestPortableRuntimePrepareMapsConfiguredProvidersFieldForField(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(source, []byte("runner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	contribution := harnesses.PortableRuntimeContribution{
		ClosureClass: harnesses.PortableRuntimeClosureStatic,
		Launch:       harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/runner"},
		Assets: []harnesses.PortableRuntimeAsset{{
			Kind: harnesses.PortableRuntimeAssetExecutable, PathKind: harnesses.PortableRuntimePathFile,
			Source: source, Target: "bin/runner", ContentSHA256: digest, Executable: true,
		}},
	}
	entry := ProviderEntry{
		Type:                      "openai-compatible",
		BaseURL:                   "https://provider.invalid/v1",
		ServerInstance:            "instance-a",
		Endpoints:                 []ProviderEndpoint{{Name: "east", BaseURL: "https://east.invalid/v1", ServerInstance: "east-a"}},
		APIKey:                    "bridge-api-secret-4b37",
		Headers:                   map[string]string{"Authorization": "Bearer bridge-header-secret-9d2a"},
		Model:                     "model-a",
		Billing:                   modelcatalog.BillingModel("per_token"),
		IncludeByDefault:          true,
		IncludeByDefaultSet:       true,
		ContextWindow:             131072,
		ConfigError:               "safe structural diagnostic",
		DailyTokenBudget:          123456,
		CreditBalanceThresholdUSD: 18.75,
		CreditProbeTTL:            17 * time.Minute,
	}
	configured, err := BuildPortableRuntimeConfiguredProviders(PortableRuntimeConfiguredProvidersInput{
		ProviderNames:       []string{"provider-a"},
		DefaultProviderName: "provider-a",
		Providers:           map[string]ProviderEntry{"provider-a": entry},
		HealthCooldown:      43 * time.Second,
		WorkDir:             "/home/private/work",
		SessionLogDir:       "/home/private/logs",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PortableRuntimePrepareInput{
		DestinationRoot: destination,
		Target:          harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH},
		Inventory: []harnesses.PortableRuntimeSurface{{
			Name: "fixture", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: portablePrepareTestHarness{contribution},
		}},
		ConfiguredProviders: configured,
	}
	bundle, err := PreparePortableRuntime(context.Background(), input)
	if err != nil {
		t.Fatalf("PreparePortableRuntime() error = %v", err)
	}
	defer bundle.Close()
	manifestBytes, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), ".fizeau", "activation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest portableruntime.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.Providers.ProviderNames, []string{"provider-a"}) || manifest.Providers.DefaultProviderName != "provider-a" ||
		manifest.Providers.HealthCooldown != 43*time.Second || len(manifest.Providers.Providers) != 1 {
		t.Fatalf("provider snapshot header = %#v", manifest.Providers)
	}
	got := manifest.Providers.Providers[0]
	if got.Name != "provider-a" || got.Type != entry.Type || got.BaseURL != entry.BaseURL || got.ServerInstance != entry.ServerInstance ||
		got.Model != entry.Model || got.Billing != string(entry.Billing) || got.IncludeByDefault != entry.IncludeByDefault ||
		got.IncludeByDefaultSet != entry.IncludeByDefaultSet || got.ContextWindow != entry.ContextWindow || got.ConfigError != entry.ConfigError ||
		got.DailyTokenBudget != entry.DailyTokenBudget || got.CreditBalanceThresholdUSD != entry.CreditBalanceThresholdUSD || got.CreditProbeTTL != entry.CreditProbeTTL ||
		len(got.Endpoints) != 1 || got.Endpoints[0] != (portableruntime.ProviderEndpoint{Name: "east", BaseURL: "https://east.invalid/v1", ServerInstance: "east-a"}) {
		t.Fatalf("mapped provider = %#v", got)
	}
	if manifest.Providers.WorkDir.Treatment != string(PortableRuntimeConfigGuestPrivate) ||
		manifest.Providers.SessionLogDir.Treatment != string(PortableRuntimeConfigExcluded) {
		t.Fatalf("path treatments = %#v / %#v", manifest.Providers.WorkDir, manifest.Providers.SessionLogDir)
	}
	secretBytes, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), ".fizeau", "provider-secrets.json"))
	if err != nil || !strings.Contains(string(secretBytes), entry.APIKey) || !strings.Contains(string(secretBytes), entry.Headers["Authorization"]) {
		t.Fatalf("private provider values missing: %v", err)
	}
}

func TestPortableRuntimePrepareInputRedactsDiagnostics(t *testing.T) {
	const (
		destination = "/home/account-name/private-destination"
		configError = "/home/account-name/provider-error"
	)
	configured := PortableRuntimeConfiguredProviders{
		ProviderNames: []string{"provider"},
		Providers:     []PortableRuntimeConfiguredProvider{{Name: "provider", ConfigError: configError}},
	}
	input := PortableRuntimePrepareInput{DestinationRoot: destination, ConfiguredProviders: configured}
	for label, value := range map[string]string{
		"input json": mustPortablePrepareJSON(t, input),
		"input fmt":  fmt.Sprintf("%v %+v %#v", input, input, input),
		"snapshot":   fmt.Sprintf("%s %s %#v", configured.String(), mustPortablePrepareJSON(t, configured), configured),
		"row":        fmt.Sprintf("%s %s %v %+v %#v", configured.Providers[0].String(), mustPortablePrepareJSON(t, configured.Providers[0]), configured.Providers[0], configured.Providers[0], configured.Providers[0]),
		"row slice":  fmt.Sprintf("%s %v %+v %#v", mustPortablePrepareJSON(t, configured.Providers), configured.Providers, configured.Providers, configured.Providers),
	} {
		for _, forbidden := range []string{destination, configError, "account-name"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("%s leaks %q: %s", label, forbidden, value)
			}
		}
	}
}

func TestPortableRuntimePrepareProviderFieldParity(t *testing.T) {
	serviceType := reflect.TypeOf(PortableRuntimeConfiguredProvider{})
	materializerType := reflect.TypeOf(portableruntime.ConfiguredProvider{})
	if serviceType.NumField() != materializerType.NumField() {
		t.Fatalf("provider field counts: service=%d materializer=%d", serviceType.NumField(), materializerType.NumField())
	}
	for index := 0; index < serviceType.NumField(); index++ {
		serviceField := serviceType.Field(index)
		materializerField := materializerType.Field(index)
		if serviceField.Name != materializerField.Name {
			t.Fatalf("field %d: service=%s materializer=%s", index, serviceField.Name, materializerField.Name)
		}
		if serviceField.Type != materializerField.Type && serviceField.Name != "Billing" && serviceField.Name != "Endpoints" {
			t.Fatalf("field %s type mismatch: %v / %v", serviceField.Name, serviceField.Type, materializerField.Type)
		}
	}
}

func mustPortablePrepareJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
