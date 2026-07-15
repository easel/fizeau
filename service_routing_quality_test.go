package fizeau

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// structural test that the legacy provider-reliability metric is surfaced as
// a separate field (not folded into RoutingQuality). Parses service.go
// directly so the assertion is robust to docstring drift.
func TestProviderReliabilityNotRenamedToRoutingQuality(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse service.go: %v", err)
	}

	candidateFields := findStructFields(file, "RouteCandidateStatus")
	if candidateFields == nil {
		t.Fatalf("RouteCandidateStatus struct not found")
	}
	if _, ok := candidateFields["ProviderReliabilityRate"]; !ok {
		t.Fatalf("RouteCandidateStatus.ProviderReliabilityRate missing; available fields: %v", candidateFields)
	}
	if got := candidateFields["ProviderReliabilityRate"]; got != "float64" {
		t.Errorf("ProviderReliabilityRate type = %s, want float64", got)
	}

	reportFields := findStructFields(file, "RouteStatusReport")
	if reportFields == nil {
		t.Fatalf("RouteStatusReport struct not found")
	}
	got, ok := reportFields["RoutingQuality"]
	if !ok {
		t.Fatalf("RouteStatusReport.RoutingQuality missing; available: %v", reportFields)
	}
	if got != "RoutingQualityMetrics" {
		t.Errorf("RouteStatusReport.RoutingQuality type = %s, want RoutingQualityMetrics", got)
	}
	for name := range reportFields {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "providerreliability") || strings.Contains(lower, "successrate") {
			t.Errorf("RouteStatusReport unexpectedly carries provider-reliability field %q (should live on RouteCandidateStatus)", name)
		}
	}
}

// findStructFields parses an *ast.File and returns a map of field name →
// rendered field type for the named struct, or nil if not found.
func findStructFields(file *ast.File, name string) map[string]string {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			out := make(map[string]string)
			for _, f := range st.Fields.List {
				typeStr := exprString(f.Type)
				for _, n := range f.Names {
					out[n.Name] = typeStr
				}
			}
			return out
		}
	}
	return nil
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(v.X)
	case *ast.ArrayType:
		return "[]" + exprString(v.Elt)
	case *ast.MapType:
		return "map[" + exprString(v.Key) + "]" + exprString(v.Value)
	default:
		return ""
	}
}
