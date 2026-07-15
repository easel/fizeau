package routehealth

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func TestRouteHealthDoesNotImportRootPackage(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", ".")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list internal/routehealth: %v", err)
	}

	var pkg struct {
		Imports []string
		Deps    []string
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("decode go list output: %v", err)
	}

	const root = "github.com/easel/fizeau"
	for _, importPath := range pkg.Imports {
		if importPath == root {
			t.Fatalf("internal/routehealth imports root package %q", root)
		}
	}
	for _, dependency := range pkg.Deps {
		if dependency == root {
			t.Fatalf("internal/routehealth depends on root package %q", root)
		}
	}
}
