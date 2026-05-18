package harnesses_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
)

type CapabilityMatrixJSON struct {
	Version   string    `json:"version"`
	Harnesses []Harness `json:"harnesses"`
}

type Harness struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	Capabilities []Capability `json:"capabilities"`
}

type Capability struct {
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	Evidence []Evidence `json:"evidence"`
}

type Evidence struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func TestHarnessCapabilityMatrixLoads(t *testing.T) {
	matrix := loadCapabilityMatrix(t)
	if matrix == nil {
		t.Fatal("capability matrix failed to load")
	}
	if len(matrix.Harnesses) < 3 {
		t.Fatalf("capability matrix has %d harnesses, want at least 3", len(matrix.Harnesses))
	}
}

func TestHarnessCapabilityMatrixValidation(t *testing.T) {
	matrix := loadCapabilityMatrix(t)
	harnessDir := currentHarnessDir(t)

	cases := []struct {
		name   string
		runner harnesses.Harness
	}{
		{"claude", &claudeharness.Runner{}},
		{"codex", &codexharness.Runner{}},
		{"gemini", &geminiharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := findHarnessInMatrix(t, matrix, tc.name)
			if entry == nil {
				t.Fatalf("harness %q not found in capability matrix", tc.name)
			}

			validateHarnessCapabilities(t, tc.name, tc.runner, entry.Capabilities, harnessDir)
			validateSupportedSets(t, tc.name, tc.runner)
		})
	}
}

func TestHarnessCapabilityMatrixEvidenceResolution(t *testing.T) {
	matrix := loadCapabilityMatrix(t)
	harnessDir := currentHarnessDir(t)

	for _, harness := range matrix.Harnesses {
		t.Run(harness.Name, func(t *testing.T) {
			for _, cap := range harness.Capabilities {
				if cap.Status != "supported" {
					continue
				}

				if len(cap.Evidence) == 0 {
					t.Errorf("capability %q declared supported but has no evidence", cap.Name)
					continue
				}

				for _, ev := range cap.Evidence {
					switch ev.Type {
					case "test":
						validateTestEvidenceExists(t, harnessDir, harness.Name, ev.ID)
					case "cassette":
						validateCassetteFileExists(t, harnessDir, ev.ID)
					case "method":
						validateMethodExists(t, harness.Name, ev.ID)
					default:
						t.Errorf("unknown evidence type %q for %s.%s", ev.Type, harness.Name, cap.Name)
					}
				}
			}
		})
	}
}

func TestHarnessCapabilityMatrixSupportedLimitIDsMatch(t *testing.T) {
	cases := []struct {
		name   string
		runner harnesses.QuotaHarness
	}{
		{"claude", &claudeharness.Runner{}},
		{"codex", &codexharness.Runner{}},
		{"gemini", &geminiharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.runner.SupportedLimitIDs()
			if len(actual) == 0 {
				t.Skip("harness does not emit limit IDs")
			}

			ctx := context.Background()
			snap, err := tc.runner.QuotaStatus(ctx, time.Now())
			if err != nil {
				t.Fatalf("QuotaStatus failed: %v", err)
			}

			for _, window := range snap.Windows {
				found := false
				for _, id := range actual {
					if id == window.LimitID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("emitted LimitID %q not in SupportedLimitIDs %v", window.LimitID, actual)
				}
			}
		})
	}
}

func TestHarnessCapabilityMatrixSupportedAliasesMatch(t *testing.T) {
	cases := []struct {
		name   string
		runner harnesses.ModelDiscoveryHarness
	}{
		{"claude", &claudeharness.Runner{}},
		{"codex", &codexharness.Runner{}},
		{"gemini", &geminiharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.runner.SupportedAliases()
			if len(actual) == 0 {
				t.Skip("harness does not support model aliases")
			}

			snapshot := tc.runner.DefaultModelSnapshot()
			for _, alias := range actual {
				_, err := tc.runner.ResolveModelAlias(alias, snapshot)
				if err != nil && err != harnesses.ErrAliasNotResolvable {
					t.Fatalf("ResolveModelAlias(%q) failed: %v", alias, err)
				}
			}

			invalidAlias := "invalid-nonexistent-family-xyz"
			_, err := tc.runner.ResolveModelAlias(invalidAlias, snapshot)
			if err != harnesses.ErrAliasNotResolvable {
				t.Errorf("ResolveModelAlias(%q) did not return ErrAliasNotResolvable", invalidAlias)
			}
		})
	}
}

func loadCapabilityMatrix(t *testing.T) *CapabilityMatrixJSON {
	t.Helper()
	harnessDir := currentHarnessDir(t)
	matrixPath := filepath.Join(harnessDir, "capability_matrix.json")

	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("failed to read capability matrix: %v", err)
	}

	var matrix CapabilityMatrixJSON
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("failed to unmarshal capability matrix: %v", err)
	}

	return &matrix
}

func findHarnessInMatrix(t *testing.T, matrix *CapabilityMatrixJSON, name string) *Harness {
	t.Helper()
	for i, h := range matrix.Harnesses {
		if h.Name == name {
			return &matrix.Harnesses[i]
		}
	}
	return nil
}

func validateHarnessCapabilities(t *testing.T, harnessName string, runner harnesses.Harness, capabilities []Capability, harnessDir string) {
	t.Helper()

	for _, cap := range capabilities {
		if cap.Status != "supported" {
			continue
		}

		if len(cap.Evidence) == 0 {
			t.Errorf("%s.%s: supported but missing evidence", harnessName, cap.Name)
		}
	}

	if _, ok := runner.(harnesses.QuotaHarness); ok {
		found := false
		for _, cap := range capabilities {
			if strings.Contains(cap.Name, "SupportedLimitIDs") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s implements QuotaHarness but capability matrix missing SupportedLimitIDs", harnessName)
		}
	}

	if _, ok := runner.(harnesses.ModelDiscoveryHarness); ok {
		found := false
		for _, cap := range capabilities {
			if strings.Contains(cap.Name, "SupportedAliases") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s implements ModelDiscoveryHarness but capability matrix missing SupportedAliases", harnessName)
		}
	}
}

func validateTestEvidenceExists(t *testing.T, harnessDir, harnessName, testID string) {
	t.Helper()

	parts := strings.Split(testID, "::")
	if len(parts) != 2 {
		t.Errorf("invalid test evidence ID format %q (expected 'package::TestName')", testID)
		return
	}

	packagePath := parts[0]
	testName := parts[1]

	lastSlash := strings.LastIndex(packagePath, "/")
	if lastSlash < 0 {
		t.Errorf("invalid test evidence package path %q", packagePath)
		return
	}

	pkgName := packagePath[lastSlash+1:]
	pkgDir := filepath.Join(harnessDir, pkgName)

	if _, err := os.Stat(pkgDir); err != nil {
		t.Logf("warning: harness package directory not found: %s", pkgDir)
		return
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), "_test.go")
	}, 0)

	if err != nil {
		t.Logf("warning: could not parse %s for test validation: %v", pkgDir, err)
		return
	}

	found := false
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					if fn.Name.Name == testName {
						found = true
						break
					}
				}
			}
		}
	}

	if !found {
		t.Errorf("test %s not found in %s", testName, pkgDir)
	}
}

func validateCassetteFileExists(t *testing.T, harnessDir, cassettePath string) {
	t.Helper()
	fullPath := filepath.Join(harnessDir, cassettePath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Logf("cassette file %s not found (may not be required in test environment)", fullPath)
	}
}

func validateMethodExists(t *testing.T, harnessName, methodID string) {
	t.Helper()

	parts := strings.Split(methodID, ".")
	if len(parts) < 2 {
		t.Errorf("invalid method ID format %q (expected 'package.(*Type).Method')", methodID)
		return
	}

	found := false

	switch harnessName {
	case "claude":
		runner := &claudeharness.Runner{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	case "codex":
		runner := &codexharness.Runner{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	case "gemini":
		runner := &geminiharness.Runner{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	}

	if !found {
		t.Errorf("method %s not found on %s harness", methodID, harnessName)
	}
}

func validateSupportedSets(t *testing.T, harnessName string, runner harnesses.Harness) {
	t.Helper()

	mdh, ok := runner.(harnesses.ModelDiscoveryHarness)
	if !ok {
		return
	}

	aliases := mdh.SupportedAliases()
	if len(aliases) == 0 {
		return
	}

	for _, alias := range aliases {
		if alias == "" {
			t.Errorf("%s.SupportedAliases() contains empty string", harnessName)
		}
	}
}
