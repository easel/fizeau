package fizeau

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
)

type exactDispatchFixtureRunner struct {
	name     string
	executes atomic.Int64
}

func (r *exactDispatchFixtureRunner) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: r.name, Type: "subprocess"}
}
func (*exactDispatchFixtureRunner) HealthCheck(context.Context) error { return nil }
func (r *exactDispatchFixtureRunner) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	r.executes.Add(1)
	data, _ := json.Marshal(harnesses.FinalData{Status: "success"})
	events := make(chan harnesses.Event, 1)
	events <- harnesses.Event{Type: harnesses.EventTypeFinal, Data: data}
	close(events)
	return events, nil
}
func (r *exactDispatchFixtureRunner) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      r.name,
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

func TestExecuteUsesRegisteredRouteInstance(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-17T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    context_window: 200000
    surfaces:
      codex: gpt-5.4
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))
	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	cancelRefresh()
	public, err := New(ServiceOptions{ServiceConfig: &fakeServiceConfig{}, QuotaRefreshContext: refreshCtx})
	if err != nil {
		t.Fatal(err)
	}
	svc := public.(*service)
	forceAvailableHarnessesForTest(t, svc, "codex")

	request := ServiceExecuteRequest{Prompt: "use the registered object", Harness: "codex", Model: "gpt-5.4"}
	decision, err := svc.resolveExecuteRoute(request)
	if err != nil {
		t.Fatal(err)
	}
	key := harnesses.RouteRunnerKey{
		Harness: decision.Harness, Provider: decision.Provider, Endpoint: decision.Endpoint,
		ServerInstance: decision.ServerInstance, Model: decision.Model,
	}
	base := &exactDispatchFixtureRunner{name: "codex"}
	registered := &exactDispatchFixtureRunner{name: "codex"}
	fresh := &exactDispatchFixtureRunner{name: "codex"}
	var freshCalls atomic.Int64
	authority := harnesses.NewRouteRunnerAuthority(
		map[string]harnesses.Harness{"codex": base},
		func(harnesses.RouteRunnerKey, harnesses.Harness) (harnesses.Harness, error) {
			freshCalls.Add(1)
			return fresh, nil
		},
	)
	if _, err := authority.Register(key, registered); err != nil {
		t.Fatal(err)
	}
	svc.routeRunners = authority

	var observed harnesses.Harness
	svc.subprocessDispatchObserver = func(runner harnesses.Harness) { observed = runner }
	events, err := svc.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if observed != registered {
		t.Fatalf("production Execute dispatched %p, want pre-registered exact runner %p", observed, registered)
	}
	if registered.executes.Load() != 1 || fresh.executes.Load() != 0 || freshCalls.Load() != 0 || base.executes.Load() != 0 {
		t.Fatalf("registered/fresh/factory/base = %d/%d/%d/%d, want 1/0/0/0",
			registered.executes.Load(), fresh.executes.Load(), freshCalls.Load(), base.executes.Load())
	}
}

func TestExecuteCoordinatorKeepsEndpointDistinctRouteInstances(t *testing.T) {
	base := &exactDispatchFixtureRunner{name: "codex"}
	east := &exactDispatchFixtureRunner{name: "codex"}
	west := &exactDispatchFixtureRunner{name: "codex"}
	created := map[string]*exactDispatchFixtureRunner{"east": east, "west": west}
	svc := &service{
		registry: harnesses.NewRegistryForTest("codex"),
		routeRunners: harnesses.NewRouteRunnerAuthority(
			map[string]harnesses.Harness{"codex": base},
			func(key harnesses.RouteRunnerKey, _ harnesses.Harness) (harnesses.Harness, error) {
				return created[key.Endpoint], nil
			},
		),
	}

	for _, endpoint := range []string{"east", "west"} {
		decision := RouteDecision{
			Harness: "codex", Provider: "openai", Endpoint: endpoint,
			ServerInstance: "server-1", Model: "gpt-5",
		}
		req := svc.executeCoordinatorRequest(ServiceExecuteRequest{Prompt: endpoint}, decision, "session-"+endpoint, nil)
		if req.RouteRunner.Key() != (harnesses.RouteRunnerKey{
			Harness: "codex", Provider: "openai", Endpoint: endpoint,
			ServerInstance: "server-1", Model: "gpt-5",
		}) {
			t.Fatalf("%s binding key = %#v", endpoint, req.RouteRunner.Key())
		}
		var observed harnesses.Harness
		coordinator := serviceimpl.ExecuteCoordinator{Registry: svc.registry}
		events := coordinator.RunResolved(context.Background(), req, serviceimpl.ExecutePorts{
			ObserveSubprocessDispatch: func(runner harnesses.Harness) { observed = runner },
		})
		for range events {
		}
		if observed != created[endpoint] {
			t.Fatalf("%s dispatch runner = %p, want exact registered instance %p", endpoint, observed, created[endpoint])
		}
	}
	if east.executes.Load() != 1 || west.executes.Load() != 1 || base.executes.Load() != 0 {
		t.Fatalf("execute counts east/west/base = %d/%d/%d, want 1/1/0",
			east.executes.Load(), west.executes.Load(), base.executes.Load())
	}
	if _, ok := svc.routeRunners.Lookup(harnesses.RouteRunnerKey{Harness: "codex"}); ok {
		t.Fatal("harness-only lookup resolved an exact route")
	}
}

func TestProductionDispatchHasSingleRunnerAuthority(t *testing.T) {
	entries, err := os.ReadDir("internal/serviceimpl")
	if err != nil {
		t.Fatal(err)
	}
	forbiddenImports := []string{
		"internal/harnesses/claude", "internal/harnesses/claude-tui",
		"internal/harnesses/codex", "internal/harnesses/gemini",
		"internal/harnesses/opencode", "internal/harnesses/pi",
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("internal/serviceimpl", entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		src := string(data)
		for _, forbidden := range forbiddenImports {
			if strings.Contains(src, forbidden) {
				t.Fatalf("%s imports concrete runner %q; construction belongs to the runner authority factory", path, forbidden)
			}
		}
		if entry.Name() == "execute_dispatch.go" &&
			(strings.Contains(src, "builtin.New(") || strings.Contains(src, "ConfiguredHarness")) {
			t.Fatalf("%s retains a competing runner construction/lookup seam", path)
		}
	}
	root, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(root), "harnessInstances") ||
		!strings.Contains(string(root), "routeRunners") ||
		!strings.Contains(string(root), "*harnesses.RouteRunnerAuthority") {
		t.Fatal("service does not expose RouteRunnerAuthority as its single runner owner")
	}
	for path, required := range map[string]string{
		"service_harness_instances.go": "s.routeRunners.StructuralInstances()",
		"service_execute_dispatch.go":  "s.routeRunners.Bind(",
	} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(data), required) {
			t.Fatalf("%s does not consume the service-owned runner authority through %q", path, required)
		}
	}
}

func TestExecuteDispatcherSeamsAreExplicit(t *testing.T) {
	files := parseRootProductionFiles(t)

	// Concrete execution and terminal mechanics belong to
	// internal/serviceimpl. Keeping this declaration check package-wide makes
	// moving an old helper to another root file fail just as clearly as leaving
	// it in service_execute.go.
	forbiddenDecls := map[string]bool{
		"runExecute":         true,
		"runVirtual":         true,
		"runScript":          true,
		"runNative":          true,
		"runSubprocess":      true,
		"dispatchExecuteRun": true,
		"emitFinal":          true,
		"emitFatalFinal":     true,
		"finalizeAndEmit":    true,
	}
	for path, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && forbiddenDecls[fn.Name.Name] {
				t.Fatalf("root %s still declares concrete Execute helper %s", path, fn.Name.Name)
			}
		}
	}

	execute := findServiceExecuteMethod(files)
	if execute == nil || execute.Body == nil {
		t.Fatal("missing (*service).Execute implementation")
	}

	var coordinatorLiterals, runResolvedCalls, validationCalls int
	ast.Inspect(execute.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.GoStmt:
			t.Error("root Execute starts a goroutine; execution lifecycle belongs to serviceimpl.ExecuteCoordinator")
		case *ast.SendStmt:
			t.Error("root Execute sends on a channel; event delivery belongs to serviceimpl.ExecuteCoordinator")
		case *ast.ChanType:
			t.Error("root Execute constructs or declares a channel; it must return the coordinator stream")
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				t.Error("root Execute receives from a channel; event lifecycle belongs to serviceimpl.ExecuteCoordinator")
			}
		case *ast.CompositeLit:
			if selectorMatches(n.Type, "serviceimpl", "ExecuteCoordinator") {
				coordinatorLiterals++
			}
		case *ast.CallExpr:
			switch fn := n.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "close" {
					t.Errorf("root Execute calls %s; channel lifecycle belongs to serviceimpl.ExecuteCoordinator", fn.Name)
				}
				if fn.Name == "make" && len(n.Args) > 0 {
					if _, ok := n.Args[0].(*ast.ChanType); ok {
						t.Error("root Execute makes a channel; it must return the coordinator stream")
					}
				}
				if fn.Name == "validateServiceExecuteRequest" {
					validationCalls++
				}
			case *ast.SelectorExpr:
				if fn.Sel.Name == "RunResolved" {
					runResolvedCalls++
				}
			}
		}
		return true
	})

	if validationCalls != 1 {
		t.Errorf("root Execute validation calls = %d, want exactly 1 shared preflight", validationCalls)
	}
	preflight := findRootFunction(files, "validateServiceExecuteRequest")
	if preflight == nil || preflight.Body == nil {
		t.Fatal("missing shared Execute request preflight")
	}
	validated := map[string]bool{}
	ast.Inspect(preflight.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok {
			if _, required := requiredExecuteValidators[ident.Name]; required {
				validated[ident.Name] = true
			}
		}
		return true
	})
	for name := range requiredExecuteValidators {
		if !validated[name] {
			t.Errorf("shared Execute preflight no longer validates via %s", name)
		}
	}
	if coordinatorLiterals != 1 {
		t.Errorf("root Execute serviceimpl.ExecuteCoordinator literals = %d, want exactly 1", coordinatorLiterals)
	}
	if runResolvedCalls != 1 {
		t.Errorf("root Execute RunResolved calls = %d, want exactly 1", runResolvedCalls)
	}
}

func TestExecuteDecisionCarriesSelectedContext(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	for _, test := range []struct {
		name            string
		selectedWindow  int
		selectedSource  string
		candidateWindow int
		candidateSource string
	}{
		{
			name:            "resolved provider evidence",
			selectedWindow:  73728,
			selectedSource:  ContextSourceProviderAPI,
			candidateWindow: 0,
			candidateSource: ContextSourceUnknown,
		},
		{
			name:            "unknown is preserved exactly",
			selectedWindow:  0,
			selectedSource:  ContextSourceUnknown,
			candidateWindow: 999,
			candidateSource: ContextSourceCatalog,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := RouteDecision{
				Harness:        "fiz",
				Provider:       "alpha",
				Endpoint:       "west",
				ServerInstance: "alpha-west-1",
				Model:          "fixture-model",
				ContextLength:  test.selectedWindow,
				ContextSource:  test.selectedSource,
				Candidates: []RouteCandidate{{
					Harness:        "fiz",
					Provider:       "alpha",
					Endpoint:       "west",
					ServerInstance: "alpha-west-1",
					Model:          "fixture-model",
					ContextLength:  test.candidateWindow,
					ContextSource:  test.candidateSource,
					Eligible:       true,
				}},
			}
			req := ServiceExecuteRequest{CompactionContextWindow: 4096}
			gotReq := svc.executeCoordinatorRequest(req, decision, "selected-context", nil)
			got := gotReq.Decision
			if got.Harness != decision.Harness || got.Provider != decision.Provider || got.Endpoint != decision.Endpoint ||
				got.ServerInstance != decision.ServerInstance || got.Model != decision.Model {
				t.Fatalf("execute decision identity = %#v, want selected route %#v", got, decision)
			}
			if got.SelectedContextWindow != decision.ContextLength || got.SelectedContextSource != decision.ContextSource {
				t.Fatalf("execute decision selected context = %d/%q, want %d/%q",
					got.SelectedContextWindow, got.SelectedContextSource, decision.ContextLength, decision.ContextSource)
			}
			if gotReq.CompactionContextWindow != req.CompactionContextWindow {
				t.Fatalf("compaction override = %d, want raw %d", gotReq.CompactionContextWindow, req.CompactionContextWindow)
			}
			if len(got.Candidates) != 1 || got.Candidates[0].Model != decision.Model {
				t.Fatalf("execute decision candidates = %#v, want selected-route trace", got.Candidates)
			}
		})
	}
}

var requiredExecuteValidators = map[string]struct{}{
	"validateMaxTokens":               {},
	"validateCompactionContextWindow": {},
	"ValidateCachePolicy":             {},
	"ValidatePowerBounds":             {},
	"ValidateRole":                    {},
	"ValidateCorrelationID":           {},
}

func findRootFunction(files map[string]*ast.File, name string) *ast.FuncDecl {
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == name {
				return fn
			}
		}
	}
	return nil
}

func TestExecuteDispatcherMovesConcreteRunnerSelectionOutOfExecuteLoop(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service_execute.go", nil, 0)
	if err != nil {
		t.Fatalf("parse service_execute.go: %v", err)
	}

	for _, imp := range file.Imports {
		path := imp.Path.Value
		switch path {
		case `"github.com/easel/fizeau/internal/harnesses/claude"`,
			`"github.com/easel/fizeau/internal/harnesses/codex"`,
			`"github.com/easel/fizeau/internal/harnesses/gemini"`,
			`"github.com/easel/fizeau/internal/harnesses/opencode"`,
			`"github.com/easel/fizeau/internal/harnesses/pi"`:
			t.Fatalf("service_execute.go imports concrete runner package %s; selection belongs behind executeRunnerInvoker", path)
		}
	}
}

func TestVirtualAndScriptMechanicsMovedOutOfRootExecute(t *testing.T) {
	data, err := os.ReadFile("service_execute.go")
	if err != nil {
		t.Fatalf("read service_execute.go: %v", err)
	}
	src := string(data)
	for _, implementationDetail := range []string{
		"virtual.response",
		"virtual.dict_dir",
		"script.stdout",
		"script.exit_code",
		"script.delay_ms",
	} {
		if strings.Contains(src, implementationDetail) {
			t.Fatalf("service_execute.go still contains runner implementation detail %q", implementationDetail)
		}
	}
}

func parseRootProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read root package: %v", err)
	}
	files := make(map[string]*ast.File)
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = file
	}
	return files
}

func findServiceExecuteMethod(files map[string]*ast.File) *ast.FuncDecl {
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Execute" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			if ident, ok := recv.(*ast.Ident); ok && ident.Name == "service" {
				return fn
			}
		}
	}
	return nil
}

func selectorMatches(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
