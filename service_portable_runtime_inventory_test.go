package fizeau

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/portableruntime"
)

type portableRuntimeFacadeHarness struct {
	name         string
	contribution harnesses.PortableRuntimeContribution
	assetCalls   int
}

func (*portableRuntimeFacadeHarness) Info() harnesses.HarnessInfo {
	panic("portable preparation must not call Info")
}

func (*portableRuntimeFacadeHarness) HealthCheck(context.Context) error {
	panic("portable preparation must not call HealthCheck")
}

func (*portableRuntimeFacadeHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("portable preparation must not execute a harness")
}

func (h *portableRuntimeFacadeHarness) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      h.name,
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

func (h *portableRuntimeFacadeHarness) PortableRuntimeAssets(context.Context, harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	h.assetCalls++
	return h.contribution, nil
}

type portableRuntimeSentinel struct{}

func (*portableRuntimeSentinel) Info() harnesses.HarnessInfo {
	panic("portable inventory must not call Info")
}
func (*portableRuntimeSentinel) HealthCheck(context.Context) error {
	panic("portable inventory must not call HealthCheck")
}
func (*portableRuntimeSentinel) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("portable inventory must not call Execute")
}
func (*portableRuntimeSentinel) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      "codex",
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

func TestPortableRuntimeInventoryUsesConfiguredServiceInstances(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	sentinel := &portableRuntimeSentinel{}
	previousHook := harnessInstanceHook
	harnessInstanceHook = func(instances map[string]harnesses.Harness) map[string]harnesses.Harness {
		instances["codex"] = sentinel
		return instances
	}
	t.Cleanup(func() { harnessInstanceHook = previousHook })

	svc := newTestService(t, ServiceOptions{})
	rows, err := svc.portableRuntimeInventory()
	if err != nil {
		t.Fatalf("portableRuntimeInventory() error = %v", err)
	}

	required := 0
	instances := svc.routeRunners.StructuralInstances()
	for _, row := range rows {
		if row.Inclusion != harnesses.PortableRuntimeInclusionRequired {
			continue
		}
		required++
		if row.Instance != instances[row.Name] {
			t.Errorf("row %q did not retain the configured service runner instance", row.Name)
		}
		if row.Name == "codex" && row.Instance != sentinel {
			t.Error("codex inventory row did not retain the hook-substituted service instance")
		}
	}
	if required != len(instances) {
		t.Fatalf("required subprocess rows = %d, configured service instances = %d", required, len(instances))
	}
	if svc.harnessByName("codex") != sentinel {
		t.Fatal("service lookup and portable inventory do not share the same configured instance authority")
	}
	coordinatorRequest := svc.executeCoordinatorRequest(ServiceExecuteRequest{}, RouteDecision{Harness: "codex"}, "fixture-session", nil)
	if !coordinatorRequest.RouteRunner.Valid() || coordinatorRequest.RouteRunner.Runner() != sentinel {
		t.Fatal("custom authority prototype was not retained for exact execution")
	}
}

func TestPortableRuntimeInventoryDerivesGeminiAndPiDispatchInstances(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	svc := newTestService(t, ServiceOptions{})
	rows, err := svc.portableRuntimeInventory()
	if err != nil {
		t.Fatalf("portableRuntimeInventory() error = %v", err)
	}

	wanted := map[string]bool{"gemini": true, "pi": true}
	seen := make(map[string]bool, len(wanted))
	for _, row := range rows {
		if !wanted[row.Name] {
			continue
		}
		seen[row.Name] = true
		configured := svc.routeRunners.StructuralInstance(row.Name)
		if row.Instance != configured || svc.harnessByName(row.Name) != configured {
			t.Fatalf("%s inventory and dispatch do not share the configured service instance", row.Name)
		}
		coordinatorRequest := svc.executeCoordinatorRequest(ServiceExecuteRequest{}, RouteDecision{Harness: row.Name}, "fixture-session", nil)
		if !coordinatorRequest.RouteRunner.Valid() || coordinatorRequest.RouteRunner.Key().Harness != row.Name {
			t.Fatalf("%s execute coordinator did not receive an exact authority binding", row.Name)
		}
		if coordinatorRequest.RouteRunner.Runner() == configured {
			t.Fatalf("%s exact route runner aliases the route-neutral structural prototype", row.Name)
		}
		if configured.Info().Name != row.Name {
			t.Fatalf("%s configured service instance reports identity %q", row.Name, configured.Info().Name)
		}
		if _, ok := configured.(harnesses.PortableRuntimeHarness); !ok {
			t.Fatalf("%s configured service instance lacks PortableRuntimeHarness", row.Name)
		}
	}
	for name := range wanted {
		if !seen[name] {
			t.Fatalf("portable inventory lacks configured %s dispatch instance", name)
		}
	}
}

func TestPortableRuntimePlanIsRouteNeutralAndOpaque(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("v0.15 portable preparation is Linux-only")
	}

	const (
		environmentAlpha = "FIZEAU_PORTABLE_FACADE_ALPHA"
		environmentName  = "FIZEAU_PORTABLE_FACADE_TOKEN"
		environmentZeta  = "FIZEAU_PORTABLE_FACADE_ZETA"
		environmentValue = "portable-environment-secret-4c21"
		zetaValue        = "portable-zeta-secret-2a18"
		apiKey           = "portable-provider-secret-7f13"
		headerValue      = "portable-header-secret-8d02"
		credentialValue  = "portable-file-secret-3b16"
	)
	t.Setenv(environmentAlpha, "")
	t.Setenv(environmentName, environmentValue)
	t.Setenv(environmentZeta, zetaValue)

	sourceRoot := filepath.Join(t.TempDir(), "account-bearing-source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceRoot, "runner")
	if err := os.WriteFile(source, []byte("portable facade runner"), 0o700); err != nil {
		t.Fatal(err)
	}
	secondarySource := filepath.Join(sourceRoot, "runner-secondary")
	if err := os.WriteFile(secondarySource, []byte("portable facade secondary runner"), 0o700); err != nil {
		t.Fatal(err)
	}
	credentialSource := filepath.Join(sourceRoot, "credential.json")
	if err := os.WriteFile(credentialSource, []byte(credentialValue), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	credentialDigest, err := harnesses.PortableRuntimeFileDigest(credentialSource)
	if err != nil {
		t.Fatal(err)
	}

	secondaryDigest, err := harnesses.PortableRuntimeFileDigest(secondarySource)
	if err != nil {
		t.Fatal(err)
	}
	runner := &portableRuntimeFacadeHarness{name: "codex", contribution: harnesses.PortableRuntimeContribution{
		ClosureClass: harnesses.PortableRuntimeClosureStatic,
		Launch:       harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/runner"},
		Assets: []harnesses.PortableRuntimeAsset{
			{
				Kind: harnesses.PortableRuntimeAssetExecutable, PathKind: harnesses.PortableRuntimePathFile,
				Source: source, Target: "bin/runner", ContentSHA256: digest, Executable: true,
			},
			{
				Kind: harnesses.PortableRuntimeAssetCredential, PathKind: harnesses.PortableRuntimePathFile,
				Source: credentialSource, Target: "data/credential.json", ContentSHA256: credentialDigest,
			},
		},
		Environment: []harnesses.PortableRuntimeEnvironment{
			{Name: environmentZeta}, {Name: environmentName},
		},
	}}
	secondaryRunner := &portableRuntimeFacadeHarness{name: "claude-tui", contribution: harnesses.PortableRuntimeContribution{
		ClosureClass: harnesses.PortableRuntimeClosureStatic,
		Launch:       harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/runner-secondary"},
		Assets: []harnesses.PortableRuntimeAsset{{
			Kind: harnesses.PortableRuntimeAssetExecutable, PathKind: harnesses.PortableRuntimePathFile,
			Source: secondarySource, Target: "bin/runner-secondary", ContentSHA256: secondaryDigest, Executable: true,
		}},
		Environment: []harnesses.PortableRuntimeEnvironment{{Name: environmentAlpha}, {Name: environmentName}},
	}}
	var logger bytes.Buffer
	externalState := filepath.Join(t.TempDir(), "service-state")
	if err := os.Mkdir(externalState, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(externalState, "ownership-marker")
	if err := os.WriteFile(marker, []byte("caller-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := &fakeServiceConfig{
		names:       []string{"provider-a"},
		defaultName: "provider-a",
		workDir:     externalState,
		providers: map[string]ServiceProviderEntry{"provider-a": {
			Type: "openai", BaseURL: "https://provider.invalid/v1",
			APIKey: apiKey, Headers: map[string]string{"Authorization": headerValue},
		}},
	}
	instances := map[string]harnesses.Harness{
		"codex": runner, "claude-tui": secondaryRunner,
	}
	svc := &service{
		opts: ServiceOptions{
			ServiceConfig: config, Logger: &logger,
			AlivenessProber: func(context.Context, string, string) bool {
				panic("portable preparation must not probe a provider")
			},
		},
		registry:     harnesses.NewRegistryForTest("codex", "claude-tui"),
		routeRunners: harnesses.NewRouteRunnerAuthority(instances, nil),
	}
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}

	bundle, err := svc.PreparePortableRuntime(context.Background(), PortableRuntimeRequest{
		DestinationRoot: destination,
		TargetGOOS:      "linux",
		TargetGOARCH:    runtime.GOARCH,
	})
	if err != nil {
		t.Fatalf("PreparePortableRuntime() error = %v", err)
	}
	if runner.assetCalls != 1 || secondaryRunner.assetCalls != 1 {
		t.Fatalf("PortableRuntimeAssets calls = %d/%d, want 1/1", runner.assetCalls, secondaryRunner.assetCalls)
	}

	runtimeRoot := filepath.Join(destination, "runtime")
	if got := bundle.RuntimeRoot(); got != runtimeRoot {
		t.Fatalf("RuntimeRoot() = %q, want %q", got, runtimeRoot)
	}
	mounts := bundle.Mounts()
	if len(mounts) != 1 || mounts[0] != (PortableRuntimeMount{
		Source: runtimeRoot, Target: PortableRuntimeGuestRoot(), ReadOnly: true,
	}) {
		t.Fatalf("Mounts() = %#v", mounts)
	}
	wantNames := []string{environmentAlpha, environmentName, environmentZeta}
	if names := bundle.EnvironmentNames(); fmt.Sprint(names) != fmt.Sprint(wantNames) {
		t.Fatalf("EnvironmentNames() = %#v", names)
	}
	for _, name := range bundle.EnvironmentNames() {
		if name == "" || strings.Contains(name, "=") {
			t.Fatalf("invalid inherited environment name %q", name)
		}
	}
	if logger.Len() != 0 {
		t.Fatalf("portable preparation emitted logger output: %s", logger.String())
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "caller-owned" {
		t.Fatalf("portable preparation touched external service state: %q, %v", got, err)
	}

	if err := os.WriteFile(credentialSource, []byte("mutated-source-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	materializedCredential, err := os.ReadFile(filepath.Join(runtimeRoot, "data", "credential.json"))
	if err != nil || string(materializedCredential) != credentialValue {
		t.Fatalf("materialized credential ownership = %q, %v", materializedCredential, err)
	}

	mounts[0].Source = source
	names := bundle.EnvironmentNames()
	names[0] = environmentValue
	if bundle.Mounts()[0].Source != runtimeRoot || bundle.EnvironmentNames()[0] != environmentAlpha {
		t.Fatal("bundle accessors did not return defensive copies")
	}

	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := []string{
		string(encoded), fmt.Sprintf("%v %+v %#v", bundle, bundle, bundle),
		fmt.Sprintf("%v", bundle.Mounts()), fmt.Sprintf("%v", bundle.EnvironmentNames()),
	}
	for _, diagnostic := range diagnostics {
		for _, forbidden := range []string{sourceRoot, source, credentialSource, environmentValue, zetaValue, apiKey, headerValue, credentialValue, "mutated-source-secret"} {
			if strings.Contains(diagnostic, forbidden) {
				t.Fatalf("public portable plan leaks %q: %s", forbidden, diagnostic)
			}
		}
	}

	ownedRuntime := filepath.Join(destination, "runtime-owned")
	if err := os.Rename(runtimeRoot, ownedRuntime); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Close(); !errors.Is(err, ErrPortableRuntimeCleanupIncomplete) {
		t.Fatalf("Close() replacement error = %v, want cleanup sentinel", err)
	}
	if bundle.RuntimeRoot() != runtimeRoot || len(bundle.Mounts()) != 1 || len(bundle.EnvironmentNames()) != len(wantNames) {
		t.Fatal("failed Close discarded public cleanup ownership")
	}
	if _, err := os.Stat(runtimeRoot); err != nil {
		t.Fatalf("failed Close touched foreign replacement: %v", err)
	}
	if err := os.Remove(runtimeRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ownedRuntime, runtimeRoot); err != nil {
		t.Fatal(err)
	}
	callerSibling := filepath.Join(destination, "caller-owned")
	if err := os.WriteFile(callerSibling, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	errorsFromClose := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsFromClose <- bundle.Close()
		}()
	}
	wait.Wait()
	close(errorsFromClose)
	for err := range errorsFromClose {
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
	if err := bundle.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	if bundle.RuntimeRoot() != "" || len(bundle.Mounts()) != 0 || len(bundle.EnvironmentNames()) != 0 {
		t.Fatal("closed bundle retained public plan data")
	}
	if got, err := os.ReadFile(callerSibling); err != nil || string(got) != "preserve" {
		t.Fatalf("Close touched caller-owned sibling: %q, %v", got, err)
	}
	if err := os.Remove(callerSibling); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
		t.Fatalf("caller destination after sibling removal = %v, %v", entries, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was touched by bundle cleanup: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "caller-owned" {
		t.Fatalf("Close touched external service state: %q, %v", got, err)
	}
}

func TestPortableRuntimeFacadePreparationStaysRouteNeutral(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "service_portable_runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var method *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Recv != nil && candidate.Name.Name == "PreparePortableRuntime" {
			method = candidate
			break
		}
	}
	if method == nil {
		t.Fatal("PreparePortableRuntime receiver not found")
	}
	wantReceiverCalls := map[string]int{
		"portableRuntimeInventory":           1,
		"portableRuntimeConfiguredProviders": 1,
	}
	gotReceiverCalls := make(map[string]int)
	forbidden := map[string]bool{
		"Execute": true, "Continue": true, "ResolveRoute": true, "RecordRouteAttempt": true,
		"HealthCheck": true, "RouteStatus": true, "TailSessionLog": true,
		"ListHarnesses": true, "ListProviders": true, "ListModels": true, "ListPolicies": true,
	}
	ast.Inspect(method.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if forbidden[selector.Sel.Name] {
			t.Errorf("portable preparation references forbidden activity %s", selector.Sel.Name)
		}
		if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "s" {
			gotReceiverCalls[selector.Sel.Name]++
		}
		return true
	})
	if fmt.Sprint(gotReceiverCalls) != fmt.Sprint(wantReceiverCalls) {
		t.Fatalf("service receiver calls = %v, want %v", gotReceiverCalls, wantReceiverCalls)
	}
}

func TestPortableRuntimePublicErrorPreservesCombinedClassifications(t *testing.T) {
	err := publicPortableRuntimeError(errors.Join(
		portableruntime.ErrRequestInvalid,
		portableruntime.ErrClosureIncomplete,
		portableruntime.ErrCleanupIncomplete,
	))
	for _, want := range []error{
		ErrPortableRuntimeRequestInvalid,
		ErrPortableRuntimeClosureIncomplete,
		ErrPortableRuntimeCleanupIncomplete,
	} {
		if !errors.Is(err, want) {
			t.Fatalf("combined public error %v does not wrap %v", err, want)
		}
	}
}
