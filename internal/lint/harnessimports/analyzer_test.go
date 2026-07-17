package harnessimports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanRepositoryAllowsOnlyDocumentedImportSeam(t *testing.T) {
	findings, err := Scan(Options{Root: "../../.."})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected harness import violations: %#v", findings)
	}
}

func TestScanFlagsExternalHarnessImport(t *testing.T) {
	root := t.TempDir()
	writeLintFixture(t, root, "service_feature_branch_probe.go", strings.Join([]string{
		"package fizeau",
		`import claudeharness "github.com/easel/fizeau/internal/harnesses/claude"`,
		"var _ = claudeharness.Runner{}",
	}, "\n")+"\n")

	findings, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("len(findings) = %d, want 1 (%#v)", len(findings), findings)
	}
	if !strings.Contains(findings[0].Message, contractMessage) {
		t.Fatalf("message = %q, want %q", findings[0].Message, contractMessage)
	}
	if !strings.Contains(findings[0].Message, "only packages under internal/harnesses/") {
		t.Fatalf("message = %q, want allowed seam hint", findings[0].Message)
	}
}

func TestScanFlagsExecuteDispatchConcreteImport(t *testing.T) {
	root := t.TempDir()
	writeLintFixture(t, root, filepath.Join("internal", "serviceimpl", "execute_dispatch.go"), strings.Join([]string{
		"package serviceimpl",
		`import claudeharness "github.com/easel/fizeau/internal/harnesses/claude"`,
		"var _ = claudeharness.Runner{}",
	}, "\n")+"\n")

	findings, err := Scan(Options{Root: root})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want one concrete-import violation", findings)
	}
}

func writeLintFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
