package fizeau

import (
	"go/ast"
	"os"
	"strings"
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

func TestRouteStatusAssemblyStaysInternal(t *testing.T) {
	if _, err := os.Stat("service_routestatus.go"); err == nil {
		t.Fatal("obsolete root implementation file service_routestatus.go still exists")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat service_routestatus.go: %v", err)
	}

	files := parseRootProductionFiles(t)
	forbiddenDecls := map[string]bool{
		"routeStatusEntriesFromSnapshot":     true,
		"routeStatusMetricKey":               true,
		"routeStatusMetricValue":             true,
		"routeAttemptCandidateCooldown":      true,
		"applyRouteSnapshotEvidenceToStatus": true,
	}
	var routeStatus *ast.FuncDecl
	for path, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if forbiddenDecls[fn.Name.Name] || strings.HasPrefix(fn.Name.Name, "routeStatusMetric") {
				t.Errorf("root %s declares %s; route-status assembly belongs to internal/routehealth", path, fn.Name.Name)
			}
			if fn.Name.Name == "RouteStatus" && fn.Recv != nil {
				routeStatus = fn
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selectorMatches(call.Fun, "routehealth", "CandidateCooldown") ||
				selectorMatches(call.Fun, "routehealth", "ProviderModelKey") {
				t.Errorf("root %s calls %s; cooldown and metric matching belong to internal/routehealth", path, callName(call.Fun))
			}
			return true
		})
	}
	if routeStatus == nil || routeStatus.Body == nil {
		t.Fatal("missing (*service).RouteStatus implementation")
	}

	buildCalls := 0
	ast.Inspect(routeStatus.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch {
		case selectorMatches(call.Fun, "routehealth", "BuildStatusRows"):
			buildCalls++
		case selectorMatches(call.Fun, "sort", "Slice"), selectorMatches(call.Fun, "sort", "Strings"):
			t.Error("root RouteStatus sorts rows; deterministic assembly belongs to routehealth.BuildStatusRows")
		case selectorMatches(call.Fun, "serverinstance", "Normalize"):
			t.Error("root RouteStatus normalizes server identity; assembly belongs to routehealth.BuildStatusRows")
		}
		return true
	})
	if buildCalls != 1 {
		t.Fatalf("root RouteStatus routehealth.BuildStatusRows calls = %d, want exactly 1", buildCalls)
	}
}

func callName(expr ast.Expr) string {
	switch fn := expr.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return callName(fn.X) + "." + fn.Sel.Name
	default:
		return "<call>"
	}
}
