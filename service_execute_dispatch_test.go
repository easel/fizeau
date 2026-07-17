package fizeau

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

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
