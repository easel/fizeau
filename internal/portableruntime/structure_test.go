package portableruntime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPortableRuntimeMaterializerHasNoExecutionSideEffects(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate materializer package")
	}
	directory := filepath.Dir(current)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenImports := []string{
		"os/exec", "/internal/routing", "/internal/session", "/internal/provider", "oci", "container",
	}
	forbiddenCalls := map[string]bool{
		"Command": true, "CommandContext": true, "Execute": true, "HealthCheck": true,
		"Dial": true, "DialContext": true, "Listen": true, "NewSession": true, "RemoveAll": true,
	}
	renameCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
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
					t.Errorf("%s imports forbidden execution dependency %q", entry.Name(), value)
				}
			}
		}
		parsed, err = parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "Renameat2" {
				renameCount++
				if len(call.Args) != 5 {
					t.Errorf("Renameat2 argument count = %d", len(call.Args))
				} else if flag, ok := call.Args[4].(*ast.SelectorExpr); !ok || flag.Sel.Name != "RENAME_NOREPLACE" {
					t.Error("portable runtime commit does not use RENAME_NOREPLACE")
				}
			}
			if forbiddenCalls[selector.Sel.Name] {
				t.Errorf("%s calls forbidden execution/lifecycle operation %s", entry.Name(), selector.Sel.Name)
			}
			return true
		})
	}
	if renameCount != 1 {
		t.Fatalf("Renameat2 commit call count = %d, want exactly 1", renameCount)
	}
}
