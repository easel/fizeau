package fizeau

import (
	"go/ast"
	"os"
	"testing"
)

func TestTranscriptAndSessionLogMechanicsStayInternal(t *testing.T) {
	for _, path := range []string{
		"service_progress.go",
		"service_session_log.go",
		"service_taillog.go",
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("obsolete root implementation file %s still exists", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
	}

	forbidden := map[string]bool{
		"serviceSessionLog":          true,
		"nativeProgressState":        true,
		"subprocessProgressState":    true,
		"emitProgress":               true,
		"newNativeProgressState":     true,
		"newSubprocessProgressState": true,
		"harnessStatusToCoreStatus":  true,
		"processOutcomeForFinal":     true,
		"finalUsageToCoreTokens":     true,
		"newSessionHub":              true,
	}
	for path, file := range parseRootProductionFiles(t) {
		for _, decl := range file.Decls {
			switch node := decl.(type) {
			case *ast.FuncDecl:
				if forbidden[node.Name.Name] {
					t.Errorf("root %s declares %s; transcript/session mechanics belong to internal owners", path, node.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range node.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok && forbidden[typeSpec.Name.Name] {
						t.Errorf("root %s declares type %s; transcript/session mechanics belong to internal owners", path, typeSpec.Name.Name)
					}
				}
			}
		}
	}
}
