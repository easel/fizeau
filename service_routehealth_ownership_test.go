package fizeau

import (
	"go/ast"
	"os"
	"testing"
)

func TestRouteAttemptMechanicsStayInternal(t *testing.T) {
	if _, err := os.Stat("service_route_attempts.go"); err == nil {
		t.Fatal("obsolete root implementation file service_route_attempts.go still exists")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat service_route_attempts.go: %v", err)
	}

	forbidden := map[string]bool{
		"routeAttemptFromFinal":         true,
		"wrappedRouteAttemptFromFinal":  true,
		"routeAttemptFailureClass":      true,
		"classifyRouteAttemptFailure":   true,
		"isRouteAttemptFeedbackFailure": true,
		"isRouteAttemptDispatchFailure": true,
		"routeAttemptCooldown":          true,
	}
	for path, file := range parseRootProductionFiles(t) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && forbidden[fn.Name.Name] {
				t.Errorf("root %s declares %s; final-attempt mechanics belong to internal/routehealth", path, fn.Name.Name)
			}
		}
	}
}
