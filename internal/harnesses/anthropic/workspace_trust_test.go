package anthropic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWorkspaceTrustedSetsFlagOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_RECORD_CLAUDE_TRUST", "1")
	dir := t.TempDir()

	if err := EnsureWorkspaceTrusted(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hasTrustDialogAccepted") {
		t.Fatalf("trust flag missing: %s", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceTrusted(dir); err != nil {
		t.Fatal(err)
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(info.ModTime()) || info2.Size() != info.Size() {
		t.Fatal("second trust of the same directory rewrote the config")
	}
}
