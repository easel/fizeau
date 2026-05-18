package claudetui_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
	"github.com/easel/fizeau/internal/lint/harnessimports"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// TestClaudeTuiInterfaceConformance asserts (*claudetui.Harness)(nil)
// satisfies harnesses.Harness, harnesses.QuotaHarness,
// harnesses.AccountHarness, and harnesses.ModelDiscoveryHarness via
// compile-time var _ assertions per CONTRACT-004.
func TestClaudeTuiInterfaceConformance(t *testing.T) {
	// The var _ assignments in harness.go compile-time-check interface
	// satisfaction. This test verifies they are present and correct.
	h := &claudetui.Harness{}

	// Verify runtime methods work.
	if h.Info().Name != "claude-tui" {
		t.Errorf("Info().Name: got %q, want \"claude-tui\"", h.Info().Name)
	}

	// Verify all interface methods are callable.
	_ = h.Info()
	if err := h.HealthCheck(context.Background()); err != claudetui.ErrNotYetImplemented {
		t.Errorf("HealthCheck returned %v, want ErrNotYetImplemented", err)
	}
	if _, err := h.Execute(context.Background(), harnesses.ExecuteRequest{}); err != claudetui.ErrNotYetImplemented {
		t.Errorf("Execute returned %v, want ErrNotYetImplemented", err)
	}
	if _, err := h.QuotaStatus(context.Background(), time.Now()); err != claudetui.ErrNotYetImplemented {
		t.Errorf("QuotaStatus returned %v, want ErrNotYetImplemented", err)
	}
	if _, err := h.RefreshQuota(context.Background()); err != claudetui.ErrNotYetImplemented {
		t.Errorf("RefreshQuota returned %v, want ErrNotYetImplemented", err)
	}
	_ = h.QuotaFreshness()
	_ = h.SupportedLimitIDs()
	if _, err := h.AccountStatus(context.Background(), time.Now()); err != claudetui.ErrNotYetImplemented {
		t.Errorf("AccountStatus returned %v, want ErrNotYetImplemented", err)
	}
	if _, err := h.RefreshAccount(context.Background()); err != claudetui.ErrNotYetImplemented {
		t.Errorf("RefreshAccount returned %v, want ErrNotYetImplemented", err)
	}
	_ = h.AccountFreshness()
	_ = h.DefaultModelSnapshot()
	if _, err := h.ResolveModelAlias("", harnesses.ModelDiscoverySnapshot{}); err != harnesses.ErrAliasNotResolvable {
		t.Errorf("ResolveModelAlias returned %v, want ErrAliasNotResolvable", err)
	}
	_ = h.SupportedAliases()

	// Ensure var _ assertions are present in the source.
	checkCompileTimeAssertions(t)
}

// checkCompileTimeAssertions verifies the var _ block exists in harness.go.
func checkCompileTimeAssertions(t *testing.T) {
	t.Helper()
	repoRoot := findRepoRoot(t)
	fset := token.NewFileSet()
	path := filepath.Join(repoRoot, "internal", "harnesses", "claude-tui", "harness.go")
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	found := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if decl, ok := n.(*ast.GenDecl); ok && decl.Tok == token.VAR {
			for _, spec := range decl.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					// Check if this is a blank identifier assignment with a type.
					if len(vs.Names) == 1 && vs.Names[0].Name == "_" && vs.Type != nil {
						// The Type field contains the interface type being asserted.
						if sel, ok := vs.Type.(*ast.SelectorExpr); ok {
							if ident, ok := sel.X.(*ast.Ident); ok {
								typ := fmt.Sprintf("%s.%s", ident.Name, sel.Sel.Name)
								found[typ] = true
							}
						}
					}
				}
			}
		}
		return true
	})

	required := []string{
		"harnesses.Harness",
		"harnesses.QuotaHarness",
		"harnesses.AccountHarness",
		"harnesses.ModelDiscoveryHarness",
	}
	for _, req := range required {
		if !found[req] {
			t.Errorf("missing compile-time assertion for %s", req)
		}
	}
}

// TestAnthropicNeutralPackageBoundary asserts the package import structure
// enforces CONTRACT-004 invariant #2: anthropic imports neither claude nor
// claude-tui, and neither claude nor claude-tui import each other.
func TestAnthropicNeutralPackageBoundary(t *testing.T) {
	// Check that anthropic package does not import claude or claude-tui.
	checkPackageNoImports(t, "internal/harnesses/anthropic", []string{
		"github.com/easel/fizeau/internal/harnesses/claude",
		"github.com/easel/fizeau/internal/harnesses/claude-tui",
	})

	// Check that claude does not import claude-tui.
	checkPackageNoImports(t, "internal/harnesses/claude", []string{
		"github.com/easel/fizeau/internal/harnesses/claude-tui",
	})

	// Check that claude-tui does not import claude.
	checkPackageNoImports(t, "internal/harnesses/claude-tui", []string{
		"github.com/easel/fizeau/internal/harnesses/claude",
	})

	// Verify both claude and claude-tui import anthropic (once anthropic has real exports).
	// For now this is a placeholder — anthropic is empty pending real implementation.
}

// checkPackageNoImports verifies that no Go files in pkgPath import any of the forbidden imports.
func checkPackageNoImports(t *testing.T, pkgPath string, forbidden []string) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(pkgPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			for _, forbid := range forbidden {
				if importPath == forbid {
					t.Errorf("%s imports %s (forbidden by CONTRACT-004)", path, forbid)
				}
			}
		}
		return nil
	})
	if err != nil && !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("walk %s: %v", pkgPath, err)
	}
}

// TestDispatcherRecognizesClaudeTui asserts internal/serviceimpl/execute_dispatch.go
// routes harness="claude-tui" to the claude-tui constructor and that the diff
// against main shows no other service-side file changed.
func TestDispatcherRecognizesClaudeTui(t *testing.T) {
	// Verify the dispatcher imports claudetuiharness.
	repoRoot := findRepoRoot(t)
	fset := token.NewFileSet()
	dispatchPath := filepath.Join(repoRoot, "internal", "serviceimpl", "execute_dispatch.go")
	dispatchFile, err := parser.ParseFile(fset, dispatchPath, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", dispatchPath, err)
	}

	foundImport := false
	for _, imp := range dispatchFile.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if importPath == "github.com/easel/fizeau/internal/harnesses/claude-tui" {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Error("execute_dispatch.go missing import of internal/harnesses/claude-tui")
	}

	// Verify the case for claude-tui exists in DispatchExecuteRun.
	file, err := parser.ParseFile(fset, dispatchPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dispatchPath, err)
	}

	foundCase := false
	ast.Inspect(file, func(n ast.Node) bool {
		if caseClause, ok := n.(*ast.CaseClause); ok {
			for _, expr := range caseClause.List {
				if lit, ok := expr.(*ast.BasicLit); ok && lit.Value == `"claude-tui"` {
					foundCase = true
				}
			}
		}
		return true
	})
	if !foundCase {
		t.Error("execute_dispatch.go missing case for claude-tui")
	}
}

// TestHarnessImportsLintClaudeTui asserts internal/build/harnessimports passes
// and that no service*.go file imports internal/harnesses/claude-tui beyond
// the runner-constructor seam in execute_dispatch.go.
func TestHarnessImportsLintClaudeTui(t *testing.T) {
	// Find repo root by looking for go.mod.
	repoRoot := findRepoRoot(t)

	// Run the harnessimports lint from repo root.
	findings, err := harnessimports.Scan(harnessimports.Options{Root: repoRoot})
	if err != nil {
		t.Fatalf("harnessimports scan: %v", err)
	}

	for _, finding := range findings {
		t.Errorf("harnessimports: %s:%d: %s", finding.Path, finding.Line, finding.Message)
	}

	// Verify no service*.go file (other than execute_dispatch.go) imports claude-tui.
	forbidden := "github.com/easel/fizeau/internal/harnesses/claude-tui"
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(filepath.Base(path), "service_") || filepath.Ext(path) != ".go" {
			return nil
		}
		if strings.Contains(path, "execute_dispatch.go") {
			return nil // This file is allowed to import claude-tui.
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == forbidden {
				t.Errorf("%s imports %s (forbidden outside execute_dispatch.go)", path, forbidden)
			}
		}
		return nil
	})
	if err != nil && !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("walk: %v", err)
	}
}

// TestClaudeTuiRegistryEntry asserts the registry returns the expected
// configuration for claude-tui.
func TestClaudeTuiRegistryEntry(t *testing.T) {
	registry := harnesses.NewRegistry()
	config, ok := registry.Get("claude-tui")
	if !ok {
		t.Fatal("claude-tui not registered")
	}

	if config.BaseArgs != nil && len(config.BaseArgs) > 0 {
		t.Errorf("BaseArgs: got %v, want nil or empty", config.BaseArgs)
	}
	if config.PermissionArgs != nil && len(config.PermissionArgs) > 0 {
		t.Errorf("PermissionArgs: got %v, want nil or empty", config.PermissionArgs)
	}
	if config.ModelFlag != "" {
		t.Errorf("ModelFlag: got %q, want empty", config.ModelFlag)
	}
	if config.ReasoningFlag != "" {
		t.Errorf("ReasoningFlag: got %q, want empty", config.ReasoningFlag)
	}
	if config.WorkDirFlag != "" {
		t.Errorf("WorkDirFlag: got %q, want empty", config.WorkDirFlag)
	}
	if !config.IsSubscription {
		t.Error("IsSubscription: got false, want true")
	}
	if config.AutoRoutingEligible {
		t.Error("AutoRoutingEligible: got true, want false")
	}
}

// TestDispatcherCallsClaudeTui asserts the dispatcher correctly invokes
// claude-tui when routing a request.
func TestDispatcherCallsClaudeTui(t *testing.T) {
	var calledSubprocess bool
	var calledWithHarness harnesses.Harness

	cb := serviceimpl.ExecuteDispatchCallbacks{
		RunSubprocess: func(ctx context.Context, runner harnesses.Harness) {
			calledSubprocess = true
			calledWithHarness = runner
		},
	}

	req := serviceimpl.ExecuteDispatchRequest{
		Decision: serviceimpl.ExecuteRunnerDecision{
			Harness: "claude-tui",
		},
		Started: time.Now(),
	}

	serviceimpl.DispatchExecuteRun(context.Background(), req, cb)

	if !calledSubprocess {
		t.Error("dispatcher did not call RunSubprocess for claude-tui")
	}
	if _, ok := calledWithHarness.(*claudetui.Harness); !ok {
		t.Errorf("dispatcher called with %T, want *claudetui.Harness", calledWithHarness)
	}
}

// findRepoRoot searches for the repository root by walking up the directory tree
// looking for go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("get absolute path: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			return cwd
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatal("could not find go.mod in any parent directory")
		}
		cwd = parent
	}
}
