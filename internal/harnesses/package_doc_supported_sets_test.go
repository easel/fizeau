package harnesses_test

import (
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	grokharness "github.com/easel/fizeau/internal/harnesses/grok"
	opencodeharness "github.com/easel/fizeau/internal/harnesses/opencode"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
)

var supportedValueBullet = regexp.MustCompile(`^\s*-\s+"([^"]+)"`)

func TestHarnessPackageDocsMirrorSupportedSets(t *testing.T) {
	harnessDir := currentHarnessDir(t)
	cases := []struct {
		name   string
		runner any
	}{
		{name: "claude", runner: &claudeharness.Runner{}},
		{name: "codex", runner: &codexharness.Runner{}},
		{name: "gemini", runner: &geminiharness.Runner{}},
		{name: "grok", runner: &grokharness.Runner{}},
		{name: "opencode", runner: &opencodeharness.Runner{}},
		{name: "pi", runner: &piharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docText := loadPackageDoc(t, filepath.Join(harnessDir, tc.name))

			mdh, ok := tc.runner.(harnesses.ModelDiscoveryHarness)
			if !ok {
				t.Fatalf("%s runner does not implement ModelDiscoveryHarness", tc.name)
			}
			assertStringSet(t, "SupportedAliases", mdh.SupportedAliases(), parseDocumentedSupportedSet(t, docText, "SupportedAliases"))

			qh, ok := tc.runner.(harnesses.QuotaHarness)
			if !ok {
				if strings.Contains(docText, "SupportedLimitIDs") {
					t.Fatalf("%s package doc mentions SupportedLimitIDs but Runner does not implement QuotaHarness", tc.name)
				}
				return
			}
			assertStringSet(t, "SupportedLimitIDs", qh.SupportedLimitIDs(), parseDocumentedSupportedSet(t, docText, "SupportedLimitIDs"))
		})
	}
}

func currentHarnessDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(filename)
}

func loadPackageDoc(t *testing.T, dir string) string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("ParseDir(%q): %v", dir, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("ParseDir(%q) produced %d packages, want 1", dir, len(pkgs))
	}
	for _, pkgAST := range pkgs {
		return doc.New(pkgAST, dir, 0).Doc
	}
	t.Fatalf("ParseDir(%q) produced no packages", dir)
	return ""
}

func parseDocumentedSupportedSet(t *testing.T, docText, heading string) []string {
	t.Helper()
	lines := strings.Split(docText, "\n")
	found := false
	empty := false
	var values []string

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !found {
			if strings.HasPrefix(line, heading) {
				found = true
				if strings.Contains(strings.ToLower(line), "empty slice") {
					empty = true
					break
				}
			}
			continue
		}

		if line == "" {
			continue
		}
		if match := supportedValueBullet.FindStringSubmatch(line); match != nil {
			values = append(values, match[1])
			continue
		}
		if len(values) > 0 {
			if strings.HasPrefix(line, "Supported") ||
				strings.HasPrefix(line, "These ") ||
				strings.HasPrefix(line, "The ") ||
				strings.HasPrefix(line, "Tier ") ||
				strings.HasPrefix(line, "CONTRACT-004:") {
				break
			}
			continue
		}
		if strings.Contains(strings.ToLower(line), "empty slice") {
			empty = true
			break
		}
		if strings.HasPrefix(line, "Supported") ||
			strings.HasPrefix(line, "These ") ||
			strings.HasPrefix(line, "The ") ||
			strings.HasPrefix(line, "Tier ") ||
			strings.HasPrefix(line, "CONTRACT-004:") {
			break
		}
	}

	if !found {
		t.Fatalf("package doc missing %s section", heading)
	}
	if empty {
		return nil
	}
	if len(values) == 0 {
		t.Fatalf("package doc %s section did not yield any quoted bullet values", heading)
	}
	return values
}
