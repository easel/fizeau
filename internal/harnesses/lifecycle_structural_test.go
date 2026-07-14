package harnesses

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestSubprocessLifecycleAppliesToEveryRegisteredRunner(t *testing.T) {
	want := []string{"claude", "claude-tui", "codex", "gemini", "opencode", "pi"}
	sources := map[string]struct {
		path   string
		tokens []string
	}{
		"claude":     {"claude/runner.go", []string{"processlifecycle.StartBatch", "PrepareHarnessOutputPipes"}},
		"claude-tui": {"claude-tui/harness.go", []string{"session.Start(", "session.WithLifecycleOptions", `Harness: "claude-tui"`}},
		"codex":      {"codex/runner.go", []string{"processlifecycle.StartBatch", "PrepareHarnessOutputPipes"}},
		"gemini":     {"gemini/runner.go", []string{"processlifecycle.StartBatch", "PrepareHarnessOutputPipes"}},
		"opencode":   {"opencode/runner.go", []string{"processlifecycle.StartBatch", "PrepareHarnessOutputPipes"}},
		"pi":         {"pi/runner.go", []string{"processlifecycle.StartBatch", "PrepareHarnessOutputPipes"}},
	}
	got := make([]string, 0, len(sources))
	for name := range sources {
		got = append(got, name)
	}
	sort.Strings(got)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("lifecycle runner set = %v, want exactly %v", got, want)
	}
	for _, name := range want {
		config, ok := builtinHarnesses[name]
		if !ok || config.Name != name {
			t.Fatalf("runner %q is not registered under its own identity: %#v", name, config)
		}
		check := sources[name]
		data, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("read %s runner: %v", name, err)
		}
		for _, token := range check.tokens {
			if !strings.Contains(string(data), token) {
				t.Errorf("registered runner %s does not bind through lifecycle token %q in %s", name, token, check.path)
			}
		}
	}
}

func TestAuxiliaryHarnessCommandsUseLifecycle(t *testing.T) {
	expected := map[string][]string{
		"claude/model_discovery.go":   {"HarnessCombinedOutput"},
		"opencode/model_discovery.go": {"HarnessCombinedOutput"},
		"pi/model_discovery.go":       {"HarnessCombinedOutput"},
		"ptyquota/probe.go":           {"session.Start(", "HarnessCombinedOutput"},
		"claude/quota_pty.go":         {"ptyquota.Run"},
		"codex/quota_pty.go":          {"ptyquota.Run"},
		"gemini/quota_pty.go":         {"ptyquota.Run"},
		"codex/model_discovery.go":    {"ptyquota.Run"},
		"claude-tui/contract004.go":   {"ptyquota.Run"},
	}
	for path, tokens := range expected {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read auxiliary path %s: %v", path, err)
		}
		for _, token := range tokens {
			if !strings.Contains(string(data), token) {
				t.Errorf("auxiliary path %s is missing lifecycle route %q", path, token)
			}
		}
	}

	for _, root := range []string{".", filepath.Join("..", "pty")} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return rejectUnmanagedProcessCalls(t, path)
		})
		if err != nil {
			t.Fatalf("scan %s: %v", root, err)
		}
	}
}

func rejectUnmanagedProcessCalls(t *testing.T, path string) error {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return err
		}
		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = importPath
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name == "StdoutPipe" || selector.Sel.Name == "StderrPipe" {
			t.Errorf("unmanaged exec.Cmd.%s in production file %s; use caller-owned harness output pipes", selector.Sel.Name, path)
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath := aliases[ident.Name]
		switch importPath {
		case "os/exec":
			if selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext" {
				return true
			}
			if filepath.Clean(path) == "exec.go" && selector.Sel.Name == "Command" {
				return true
			}
			t.Errorf("unmanaged %s.%s in production file %s", ident.Name, selector.Sel.Name, path)
		case "github.com/creack/pty":
			if selector.Sel.Name == "StartWithSize" {
				t.Errorf("unmanaged PTY start in production file %s", path)
			}
		}
		return true
	})
	return nil
}
