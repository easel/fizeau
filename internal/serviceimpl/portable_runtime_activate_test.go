package serviceimpl

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/portableruntime"
)

type activationPureHarness struct {
	contribution harnesses.PortableRuntimeContribution
}

func (activationPureHarness) Info() harnesses.HarnessInfo { panic("activation called Info") }
func (activationPureHarness) HealthCheck(context.Context) error {
	panic("activation called HealthCheck")
}
func (activationPureHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("activation called Execute")
}
func (h activationPureHarness) PortableRuntimeAssets(context.Context, harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	return h.contribution, nil
}

func TestPortableRuntimeActivationIsRouteNeutralAndProcessFree(t *testing.T) {
	assertPortableActivationSourcesArePure(t)
	bundle, expected := prepareServiceActivationFixture(t)
	activation, err := LoadPortableRuntimeActivation(bundle.RuntimeRoot(), func(string) (string, bool) {
		t.Fatal("activation looked up an undeclared environment name")
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadPortableRuntimeActivation() error = %v", err)
	}
	got := activation.ConfiguredProviders()
	if !reflect.DeepEqual(got.ProviderNames, expected.ProviderNames) || got.DefaultProviderName != expected.DefaultProviderName ||
		got.HealthCooldown != expected.HealthCooldown || !reflect.DeepEqual(got.Providers, expected.Providers) {
		t.Fatalf("reconstructed provider structure = %#v, want %#v", got, expected)
	}
	wantSecrets := expected.SensitiveProviders()
	gotSecrets := got.SensitiveProviders()
	if len(gotSecrets) != len(wantSecrets) || gotSecrets[0].ProviderName() != wantSecrets[0].ProviderName() ||
		gotSecrets[0].APIKey() != wantSecrets[0].APIKey() || !reflect.DeepEqual(gotSecrets[0].Headers(), wantSecrets[0].Headers()) {
		t.Fatalf("reconstructed provider secrets = %#v, want %#v", gotSecrets, wantSecrets)
	}

	// Returned provider records cannot mutate the activation-owned template.
	got.ProviderNames[0] = "mutated"
	got.Providers[0].Endpoints[0].Name = "mutated"
	gotSecrets[0].Headers()["Authorization"] = "mutated"
	again := activation.ConfiguredProviders()
	if again.ProviderNames[0] != expected.ProviderNames[0] || again.Providers[0].Endpoints[0].Name != expected.Providers[0].Endpoints[0].Name ||
		again.SensitiveProviders()[0].Headers()["Authorization"] != wantSecrets[0].Headers()["Authorization"] {
		t.Fatal("reconstructed providers alias activation-owned state")
	}
}

func TestPortableRuntimeActivationAssemblesServiceStorage(t *testing.T) {
	bundle, expected := prepareServiceActivationFixture(t)
	writableRoot := t.TempDir()
	activation, err := AssemblePortableRuntimeActivation(context.Background(), bundle.RuntimeRoot(), writableRoot, func(string) (string, bool) {
		t.Fatal("activation looked up an undeclared environment name")
		return "", false
	})
	if err != nil {
		t.Fatalf("AssemblePortableRuntimeActivation() error = %v", err)
	}
	backingRoot := filepath.Join(writableRoot, "activation")
	if activation.BackingRoot() != backingRoot || activation.WorkDir() != filepath.Join(backingRoot, "state", "work") ||
		activation.SessionLogDir() != filepath.Join(backingRoot, "state", "sessions") {
		t.Fatalf("activation paths = backing %q, work %q, sessions %q", activation.BackingRoot(), activation.WorkDir(), activation.SessionLogDir())
	}
	environment, ok := activation.EntrypointEnvironment("fixture")
	if !ok || environment["HOME"] != filepath.Join(backingRoot, "home") || environment["PATH"] == "" {
		t.Fatalf("closed entrypoint environment = %#v, %t", environment, ok)
	}
	if _, ok := activation.EntrypointRecipe("fixture"); !ok {
		t.Fatal("entrypoint recipe missing")
	}
	if got := activation.ConfiguredProviders(); !reflect.DeepEqual(got.ProviderNames, expected.ProviderNames) ||
		got.DefaultProviderName != expected.DefaultProviderName || !reflect.DeepEqual(got.Providers, expected.Providers) {
		t.Fatalf("assembled provider structure = %#v, want %#v", got, expected)
	}
}

func assertPortableActivationSourcesArePure(t *testing.T) {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate activation test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	files := []string{
		"service_portable_runtime.go",
		"service_portable_runtime_activation.go",
		"internal/serviceimpl/portable_runtime_activate.go",
		"internal/portableruntime/activation.go",
		"internal/portableruntime/activation_storage.go",
		"internal/portableruntime/activation_storage_linux.go",
		"internal/portableruntime/activation_storage_unsupported.go",
		"internal/portableruntime/activation_tree_linux.go",
		"internal/portableruntime/activation_tree_unsupported.go",
	}
	forbiddenImports := []string{
		"os/exec", "/internal/routing", "/internal/session", "/internal/provider",
		"/internal/processlifecycle", "/internal/routehealth", "/internal/harnesses/builtin",
	}
	forbiddenCalls := map[string]bool{
		"Execute": true, "HealthCheck": true, "ResolveRoute": true,
		"Command": true, "CommandContext": true, "Start": true, "StartProcess": true, "ForkExec": true,
		"Clone": true, "Clone3": true, "Fork": true, "Exec": true, "Execve": true,
		"Unshare": true, "Setns": true, "Mount": true, "PivotRoot": true,
		"Dial": true, "DialContext": true,
		"NewSession": true, "NewRuntime": true, "NewRefreshScheduler": true,
		"reapStaleHarnessSessions": true, "startQuotaRecoveryProbeLoop": true, "startAlivenessProbeLoop": true,
	}
	for _, relative := range files {
		name := filepath.Join(root, filepath.FromSlash(relative))
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range forbiddenImports {
				if value == forbidden || strings.Contains(value, forbidden) {
					t.Errorf("%s imports activity dependency %q", relative, value)
				}
			}
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			localNew := false
			switch function := call.Fun.(type) {
			case *ast.Ident:
				name = function.Name
				localNew = name == "New"
			case *ast.SelectorExpr:
				name = function.Sel.Name
			}
			if localNew || forbiddenCalls[name] {
				t.Errorf("%s calls forbidden activation activity %s", relative, name)
			}
			return true
		})
	}
}

func prepareServiceActivationFixture(t *testing.T) (*portableruntime.Bundle, PortableRuntimeConfiguredProviders) {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(source, []byte("runner\n"), 0o700); err != nil {
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
		Type: "openai-compatible", BaseURL: "https://provider.invalid/v1", ServerInstance: "instance-a",
		Endpoints: []ProviderEndpoint{{Name: "east", BaseURL: "https://east.invalid/v1", ServerInstance: "east-a"}},
		APIKey:    "activation-api-key", Headers: map[string]string{"Authorization": "Bearer activation-header"},
		Model: "model-a", Billing: modelcatalog.BillingModelPerToken,
		IncludeByDefault: true, IncludeByDefaultSet: true, ContextWindow: 131072,
		ConfigError: "safe structural diagnostic", DailyTokenBudget: 123456,
		CreditBalanceThresholdUSD: 18.75, CreditProbeTTL: 17 * time.Minute,
	}
	configured, err := BuildPortableRuntimeConfiguredProviders(PortableRuntimeConfiguredProvidersInput{
		ProviderNames: []string{"provider-a"}, DefaultProviderName: "provider-a",
		Providers: map[string]ProviderEntry{"provider-a": entry}, HealthCooldown: 43 * time.Second,
		WorkDir: "/host/work", SessionLogDir: "/host/logs",
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := PreparePortableRuntime(context.Background(), PortableRuntimePrepareInput{
		DestinationRoot: destination, Target: harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH},
		Inventory: []harnesses.PortableRuntimeSurface{{
			Name: "fixture", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: activationPureHarness{contribution},
		}},
		ConfiguredProviders: configured,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	return bundle, configured
}
