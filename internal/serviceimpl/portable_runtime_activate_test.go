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
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/portableruntime"
)

type activationPureHarness struct {
	contribution harnesses.PortableRuntimeContribution
}

type activationBindingHarness struct {
	harnesses.PortableRuntimeRunnerState
	name string
}

func (h *activationBindingHarness) Info() harnesses.HarnessInfo {
	panic("portable binding called Info")
}
func (h *activationBindingHarness) HealthCheck(context.Context) error {
	panic("portable binding called HealthCheck")
}
func (h *activationBindingHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("portable binding called Execute")
}
func (h *activationBindingHarness) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	if binding, ok := h.PortableRuntimeBinding(); ok {
		return binding.Structure()
	}
	return harnesses.PortableRuntimeStructure{
		Name: h.name, Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode: harnesses.PortableRuntimeStructuralUnpinned,
	}
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
	activation, err := assemblePortableRuntimeActivationWithIdentity(context.Background(), bundle.RuntimeRoot(), writableRoot, func(string) (string, bool) {
		t.Fatal("activation looked up an undeclared environment name")
		return "", false
	}, testPortableRuntimeActivationIdentity)
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

func TestPortableRuntimeActivationRegistersTypedLaunchRecipes(t *testing.T) {
	activation := preparePortableRuntimeBindingFixture(t)
	prototypes := portableRuntimeBindingPrototypes()
	authority, err := activation.BindPortableRuntimeRouteRunners(prototypes, cloneActivationBindingHarness)
	if err != nil {
		t.Fatalf("BindPortableRuntimeRouteRunners() error = %v", err)
	}

	poisonedPath := filepath.Join(t.TempDir(), "poisoned-path")
	t.Setenv("PATH", poisonedPath)
	tests := []struct {
		name       string
		command    string
		recipeArgs []string
	}{
		{name: "static", command: "/opt/fizeau/runtime/static/tool"},
		{name: "dynamic", command: "/opt/fizeau/runtime/dynamic/loader", recipeArgs: []string{
			"--library-path", "/opt/fizeau/runtime/dynamic/lib", "/opt/fizeau/runtime/dynamic/tool",
		}},
		{name: "interpreted", command: "/opt/fizeau/runtime/interpreted/interpreter", recipeArgs: []string{
			"--runtime-fixed", "/opt/fizeau/runtime/interpreted/tool.js",
		}},
		{name: "nested", command: "/opt/fizeau/runtime/nested/loader", recipeArgs: []string{
			"--library-path", "/opt/fizeau/runtime/nested/lib", "/opt/fizeau/runtime/nested/interpreter",
			"--runtime-fixed", "/opt/fizeau/runtime/nested/tool.js",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			keys := []harnesses.RouteRunnerKey{
				{Harness: test.name, Endpoint: "east", Model: "model-a"},
				{Harness: test.name, Endpoint: "west", Model: "model-a"},
			}
			bindings := make([]harnesses.RouteRunnerBinding, len(keys))
			errors := make([]error, len(keys))
			var wait sync.WaitGroup
			for i := range keys {
				wait.Add(1)
				go func(index int) {
					defer wait.Done()
					bindings[index], errors[index] = authority.Bind(keys[index])
				}(i)
			}
			wait.Wait()
			for i, bindErr := range errors {
				if bindErr != nil {
					t.Fatalf("Bind(%d) error = %v", i, bindErr)
				}
			}
			if bindings[0].Runner() == bindings[1].Runner() || bindings[0].Runner() == prototypes[test.name] {
				t.Fatal("exact runner was not cloned from the activated structural prototype")
			}
			for _, exact := range bindings {
				runner := exact.Runner().(*activationBindingHarness)
				binding, ok := runner.PortableRuntimeBinding()
				if !ok || binding.NamespaceRecipe() == nil {
					t.Fatal("exact runner lost portable launch or opaque namespace state")
				}
				child, buildErr := binding.BuildCommand([]string{"registry-arg"}, []string{"request-arg"})
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				wantArgs := append(append([]string(nil), test.recipeArgs...),
					"--fixed", "--verbose", "-e", "none", "registry-arg", "request-arg")
				if child.Command() != test.command || !reflect.DeepEqual(child.Arguments(), wantArgs) {
					t.Fatalf("child command = %q %q, want %q %q", child.Command(), child.Arguments(), test.command, wantArgs)
				}
				joined := strings.Join(append([]string{child.Command()}, child.Arguments()...), " ")
				for _, forbidden := range []string{poisonedPath, "/host/copied-pt-interp", "/host/absolute-shebang", "/bin/sh -c"} {
					if strings.Contains(joined, forbidden) {
						t.Fatalf("final command consulted forbidden launch source %q: %s", forbidden, joined)
					}
				}
				if environment := child.Environment(); len(environment) == 0 || !containsActivationEnvironment(environment, "HOME=") ||
					!containsActivationEnvironment(environment, "PATH=/opt/fizeau/runtime/") {
					t.Fatalf("child did not receive the activation-owned closed environment: %q", environment)
				}
			}
		})
	}
}

func TestPortableRuntimeActivationBuildsFixedPrefix(t *testing.T) {
	activation := preparePortableRuntimeBindingFixture(t)
	prototypes := portableRuntimeBindingPrototypes()
	authority, err := activation.BindPortableRuntimeRouteRunners(prototypes, cloneActivationBindingHarness)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := authority.Bind(harnesses.RouteRunnerKey{Harness: "nested", Provider: "provider", Endpoint: "endpoint", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := exact.Runner().(*activationBindingHarness).PortableRuntimeBinding()
	if !ok {
		t.Fatal("exact runner has no portable binding")
	}
	child, err := binding.BuildCommand([]string{"registry-one", "registry-two"}, []string{"request-one", "request-two"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--library-path", "/opt/fizeau/runtime/nested/lib", "/opt/fizeau/runtime/nested/interpreter",
		"--runtime-fixed", "/opt/fizeau/runtime/nested/tool.js",
		"--fixed", "--verbose", "-e", "none",
		"registry-one", "registry-two", "request-one", "request-two",
	}
	if !reflect.DeepEqual(child.Arguments(), want) {
		t.Fatalf("fixed-prefix order = %q, want %q", child.Arguments(), want)
	}
	for _, token := range []string{"--fixed", "--verbose", "-e", "none"} {
		count := 0
		for _, argument := range child.Arguments() {
			if argument == token {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("fixed token %q applied %d times", token, count)
		}
	}

	// Accessors and exact clones return owned data rather than aliases.
	arguments := child.Arguments()
	environment := binding.Environment()
	arguments[0] = "mutated"
	environment["HOME"] = "mutated"
	again, err := binding.BuildCommand([]string{"registry-one", "registry-two"}, []string{"request-one", "request-two"})
	if err != nil || !reflect.DeepEqual(again.Arguments(), want) || strings.Contains(strings.Join(again.Environment(), "\n"), "HOME=mutated") {
		t.Fatal("portable binding state aliases returned command or environment data")
	}
}

func containsActivationEnvironment(environment []string, prefix string) bool {
	for _, assignment := range environment {
		if strings.HasPrefix(assignment, prefix) {
			return true
		}
	}
	return false
}

func portableRuntimeBindingPrototypes() map[string]harnesses.Harness {
	return map[string]harnesses.Harness{
		"static":      &activationBindingHarness{name: "static"},
		"dynamic":     &activationBindingHarness{name: "dynamic"},
		"interpreted": &activationBindingHarness{name: "interpreted"},
		"nested":      &activationBindingHarness{name: "nested"},
	}
}

func cloneActivationBindingHarness(_ harnesses.RouteRunnerKey, prototype harnesses.Harness) (harnesses.Harness, error) {
	runner, ok := prototype.(*activationBindingHarness)
	if !ok {
		return nil, harnesses.ErrRouteRunnerUnavailable
	}
	clone := *runner
	clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
	return &clone, nil
}

func preparePortableRuntimeBindingFixture(t *testing.T) PortableRuntimeActivation {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	assetRoot := t.TempDir()
	writeFile := func(name, contents string, executable bool) harnesses.PortableRuntimeAsset {
		t.Helper()
		source := filepath.Join(assetRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if executable {
			mode = 0o700
		}
		if err := os.WriteFile(source, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
		digest, err := harnesses.PortableRuntimeFileDigest(source)
		if err != nil {
			t.Fatal(err)
		}
		return harnesses.PortableRuntimeAsset{
			Kind: harnesses.PortableRuntimeAssetSupport, PathKind: harnesses.PortableRuntimePathFile,
			Source: source, Target: name, ContentSHA256: digest, Executable: executable,
		}
	}
	writeTree := func(name string) harnesses.PortableRuntimeAsset {
		t.Helper()
		source := filepath.Join(assetRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "libfixture.so"), []byte("library\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		digest, err := harnesses.PortableRuntimeTreeDigest(source)
		if err != nil {
			t.Fatal(err)
		}
		return harnesses.PortableRuntimeAsset{
			Kind: harnesses.PortableRuntimeAssetSupport, PathKind: harnesses.PortableRuntimePathTree,
			Source: source, Target: name, ContentSHA256: digest,
		}
	}
	fixed := harnesses.PortableRuntimeExecutionConstraints{
		FixedArguments:    []string{"--fixed", "--verbose"},
		FixedOptionValues: []harnesses.PortableRuntimeFixedOptionValue{{Option: "-e", Value: "none"}},
	}
	contributions := map[string]harnesses.PortableRuntimeContribution{
		"static": {
			ClosureClass:         harnesses.PortableRuntimeClosureStatic,
			Launch:               harnesses.PortableRuntimeLaunch{EntrypointTarget: "static/tool"},
			Assets:               []harnesses.PortableRuntimeAsset{writeFile("static/tool", "#!/host/absolute-shebang\n", true)},
			ExecutionConstraints: fixed,
		},
		"dynamic": {
			ClosureClass: harnesses.PortableRuntimeClosureDynamic,
			Launch: harnesses.PortableRuntimeLaunch{
				EntrypointTarget: "dynamic/tool", LoaderTarget: "dynamic/loader",
				LibraryRootTargets: []string{"dynamic/lib"},
			},
			Assets: []harnesses.PortableRuntimeAsset{
				writeFile("dynamic/tool", "copied PT_INTERP=/host/copied-pt-interp\n", true),
				writeFile("dynamic/loader", "loader\n", true), writeTree("dynamic/lib"),
			},
			ExecutionConstraints: fixed,
		},
		"interpreted": {
			ClosureClass: harnesses.PortableRuntimeClosureInterpreted,
			Launch: harnesses.PortableRuntimeLaunch{
				EntrypointTarget: "interpreted/tool.js", InterpreterTarget: "interpreted/interpreter",
				RuntimeArgs: []string{"--runtime-fixed"},
			},
			Assets: []harnesses.PortableRuntimeAsset{
				writeFile("interpreted/tool.js", "#!/host/absolute-shebang\n", false),
				writeFile("interpreted/interpreter", "interpreter\n", true),
			},
			ExecutionConstraints: fixed,
		},
		"nested": {
			ClosureClass: harnesses.PortableRuntimeClosureInterpreted,
			Launch: harnesses.PortableRuntimeLaunch{
				EntrypointTarget: "nested/tool.js", InterpreterTarget: "nested/interpreter",
				LoaderTarget: "nested/loader", LibraryRootTargets: []string{"nested/lib"},
				RuntimeArgs: []string{"--runtime-fixed"},
			},
			Assets: []harnesses.PortableRuntimeAsset{
				writeFile("nested/tool.js", "#!/host/absolute-shebang\n", false),
				writeFile("nested/interpreter", "interpreter PT_INTERP=/host/copied-pt-interp\n", true),
				writeFile("nested/loader", "loader\n", true), writeTree("nested/lib"),
			},
			ExecutionConstraints: fixed,
		},
	}
	names := []string{"static", "dynamic", "interpreted", "nested"}
	inventory := make([]harnesses.PortableRuntimeSurface, 0, len(names))
	for _, name := range names {
		inventory = append(inventory, harnesses.PortableRuntimeSurface{
			Name: name, Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired,
			Instance:  activationPureHarness{contribution: contributions[name]},
		})
	}
	configured, err := BuildPortableRuntimeConfiguredProviders(PortableRuntimeConfiguredProvidersInput{})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := PreparePortableRuntime(context.Background(), PortableRuntimePrepareInput{
		DestinationRoot: destination,
		Target:          harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH},
		Inventory:       inventory, ConfiguredProviders: configured,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	activation, err := assemblePortableRuntimeActivationWithIdentity(context.Background(), bundle.RuntimeRoot(), t.TempDir(), func(string) (string, bool) {
		t.Fatal("binding fixture unexpectedly requested inherited environment")
		return "", false
	}, testPortableRuntimeActivationIdentity)
	if err != nil {
		t.Fatal(err)
	}
	return activation
}

func testPortableRuntimeActivationIdentity() (int, int, []int, error) {
	return 65532, 65532, nil, nil
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
