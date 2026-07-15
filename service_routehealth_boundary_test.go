package fizeau

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var residualRouteHealthMechanicNames = map[string]bool{
	"defaultHealthProbeInterval":      true,
	"defaultHealthSignalTTL":          true,
	"routeTimeProbeTimeout":           true,
	"startupProbeTotalTimeout":        true,
	"alivenessEndpoint":               true,
	"runStartupAlivenessProbes":       true,
	"runRouteTimeAlivenessProbes":     true,
	"recordRouteTimeProbeFailures":    true,
	"runAlivenessProbeLoop":           true,
	"alivenessLoopSleep":              true,
	"tcpAlivenessProber":              true,
	"extractHostPort":                 true,
	"probeNeededForAlivenessEndpoint": true,
	"lastProbeForAlivenessEndpoint":   true,
	"alivenessRouteKeys":              true,
	"providerBaseURLsForEndpoint":     true,
	"dispatchFailureFromAttempt":      true,
	"observeFinalAttempt":             true,
}

var residualRouteHealthAdapterDecls = map[string]int{
	"service_aliveness.go:startupAlivenessProbe":               1,
	"service_aliveness.go:requestLocalHealthRefreshForRouting": 1,
	"service_aliveness.go:startAlivenessProbeLoop":             1,
	"service_dispatch_feedback.go:recordDispatchFailure":       1,
	"service_dispatch_feedback.go:RecordRouteAttempt":          1,
	"service_dispatch_feedback.go:internalRouteAttempt":        1,
	"service_dispatch_feedback.go:persistRouteHealthSnapshot":  1,
	"service_dispatch_feedback.go:activeRouteAttempts":         1,
	"service_dispatch_feedback.go:routeMetricSignals":          1,
}

var residualRouteHealthFileFunctions = map[string]map[string]bool{
	"service_aliveness.go": {
		"healthProbeInterval":                 true,
		"healthSignalTTL":                     true,
		"alivenessEndpoints":                  true,
		"startupAlivenessProbe":               true,
		"requestLocalHealthRefreshForRouting": true,
		"probeUnknownProviders":               true,
		"probeUnreachableProviders":           true,
		"startAlivenessProbeLoop":             true,
	},
	"service_dispatch_feedback.go": {
		"RecordRouteAttempt":           true,
		"recordRouteAttemptFromFinal":  true,
		"observeRouteAttemptFromFinal": true,
		"internalRouteAttempt":         true,
		"routeHealthStore":             true,
		"activeRouteAttempts":          true,
		"routeMetricSignals":           true,
		"persistRouteHealthSnapshot":   true,
		"recordDispatchFailure":        true,
	},
}

var residualRouteHealthImportedCallSites = map[string]int{
	"service_dispatch_feedback.go:RecordRouteAttempt:routehealth.RecordAttempt":                 1,
	"service_dispatch_feedback.go:recordRouteAttemptFromFinal:routehealth.ObserveFinalAttempt":  1,
	"service_dispatch_feedback.go:observeRouteAttemptFromFinal:routehealth.ObserveFinalAttempt": 1,
	"service_dispatch_feedback.go:persistRouteHealthSnapshot:routehealth.SavePersistedState":    1,
	"service_dispatch_feedback.go:recordDispatchFailure:serviceimpl.NewCatalogCacheKey":         1,
}

var residualRouteHealthImportedReferenceSites = map[string]int{
	"service_dispatch_feedback.go:RecordRouteAttempt:routehealth.RecordAttempt":                    1,
	"service_dispatch_feedback.go:recordRouteAttemptFromFinal:routehealth.ObserveFinalAttempt":     1,
	"service_dispatch_feedback.go:observeRouteAttemptFromFinal:routehealth.ObserveFinalAttempt":    1,
	"service_dispatch_feedback.go:persistRouteHealthSnapshot:routehealth.SavePersistedState":       1,
	"service_dispatch_feedback.go:recordDispatchFailure:serviceimpl.IsDispatchReachabilityFailure": 1,
	"service_dispatch_feedback.go:recordDispatchFailure:serviceimpl.NewCatalogCacheKey":            1,
}

var residualRouteHealthMethodCallSites = map[string]int{
	"service_aliveness.go:startupAlivenessProbe:s.aliveness.Startup":                      1,
	"service_aliveness.go:requestLocalHealthRefreshForRouting:s.aliveness.RequestRefresh": 1,
	"service_aliveness.go:startAlivenessProbeLoop:s.aliveness.StartLoop":                  1,
	"service_dispatch_feedback.go:recordDispatchFailure:feedback.Record":                  1,
	"service_dispatch_feedback.go:activeRouteAttempts:s.routeHealth.ActiveAttempts":       1,
	"service_dispatch_feedback.go:routeMetricSignals:s.routeHealth.MetricSignals":         1,
}

var residualRouteHealthAllowedGoStatements = map[string]int{
	"service_routing.go:startQuotaRecoveryProbeLoop":           1,
	"service_stale_harness_reaper.go:reapStaleHarnessSessions": 1,
}

type residualRouteHealthAnalysis struct {
	violations    []string
	adapterDecls  map[string]int
	fileFunctions map[string]int
	importedCalls map[string]int
	importedRefs  map[string]int
	methodCalls   map[string]int
	methodRefs    map[string]int
	goStatements  map[string]int
}

// TestResidualRouteHealthMechanicsStayInternal is the production boundary
// lock for the final ADR-008 routehealth extraction. The same analyzer is used
// by the mutation suite below so the production assertion cannot silently be
// weaker than its adversarial controls.
func TestResidualRouteHealthMechanicsStayInternal(t *testing.T) {
	for _, violation := range residualRouteHealthOwnershipViolations(parseRootProductionFiles(t)) {
		t.Error(violation)
	}
}

func residualRouteHealthOwnershipViolations(files map[string]*ast.File) []string {
	analysis := residualRouteHealthAnalysis{
		adapterDecls:  make(map[string]int),
		fileFunctions: make(map[string]int),
		importedCalls: make(map[string]int),
		importedRefs:  make(map[string]int),
		methodCalls:   make(map[string]int),
		methodRefs:    make(map[string]int),
		goStatements:  make(map[string]int),
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file := files[path]
		routehealthBindings := analysis.importBindings(path, file, routehealthImportPath, "routehealth")
		serviceimplBindings := analysis.importBindings(path, file, serviceimplImportPath, "serviceimpl")
		timeBindings := analysis.importBindings(path, file, "time", "time")
		analysis.rejectRootNetworkImports(path, file)

		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				analysis.recordPackageName(path, "function", current.Name.Name)
				owner := current.Name.Name
				analysis.recordFunctionDeclaration(path, current)
				analysis.scanNode(
					path,
					owner,
					current.Body,
					routehealthBindings,
					serviceimplBindings,
					timeBindings,
					residualTypedCoordinatorAliases(current.Type, routehealthBindings),
				)
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						analysis.recordPackageName(path, "type", named.Name.Name)
						analysis.recordForbiddenRefreshStateFields(path, named.Type)
					case *ast.ValueSpec:
						for _, name := range named.Names {
							analysis.recordPackageName(path, "value/alias", name.Name)
							if name.Name == "providerProbeRefreshInFlight" {
								analysis.violations = append(analysis.violations, fmt.Sprintf(
									"root %s declares forbidden routehealth lifecycle state %s", path, name.Name,
								))
							}
						}
						analysis.recordForbiddenRefreshStateFields(path, named.Type)
						for _, value := range named.Values {
							analysis.recordForbiddenRefreshStateFields(path, value)
						}
					}
				}
				analysis.scanNode(path, "package scope", current, routehealthBindings, serviceimplBindings, timeBindings, nil)
			}
		}
	}

	analysis.requireExact("routehealth adapter declarations", analysis.adapterDecls, residualRouteHealthAdapterDecls)
	analysis.requireExact("routehealth imported direct calls", analysis.importedCalls, residualRouteHealthImportedCallSites)
	analysis.requireExact("routehealth imported references", analysis.importedRefs, residualRouteHealthImportedReferenceSites)
	analysis.requireExact("routehealth method direct calls", analysis.methodCalls, residualRouteHealthMethodCallSites)
	analysis.requireExact("routehealth method references", analysis.methodRefs, residualRouteHealthMethodCallSites)
	// This is deliberately a root-wide exact allowlist, not a name heuristic.
	// Otherwise a moved aliveness loop could evade the boundary merely by being
	// renamed. The two unrelated root goroutines are retained explicitly; all
	// route-health lifecycle concurrency belongs to internal/routehealth.
	analysis.requireExact("root goroutine sites", analysis.goStatements, residualRouteHealthAllowedGoStatements)

	for path, allowed := range residualRouteHealthFileFunctions {
		want := make(map[string]int, len(allowed))
		for name := range allowed {
			want[path+":"+name] = 1
		}
		got := make(map[string]int)
		for site, count := range analysis.fileFunctions {
			if strings.HasPrefix(site, path+":") {
				got[site] = count
			}
		}
		analysis.requireExact(path+" function declarations", got, want)
	}
	return analysis.violations
}

func (analysis *residualRouteHealthAnalysis) recordForbiddenRefreshStateFields(path string, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok {
			return true
		}
		for _, name := range field.Names {
			if name.Name == "providerProbeRefreshInFlight" {
				analysis.violations = append(analysis.violations, fmt.Sprintf(
					"root %s declares forbidden routehealth lifecycle state %s", path, name.Name,
				))
			}
		}
		return true
	})
}

func (analysis *residualRouteHealthAnalysis) importBindings(path string, file *ast.File, importPath, defaultName string) map[string]bool {
	bindings, invalid := importBindingsForPath(file, importPath, defaultName)
	for _, binding := range invalid {
		analysis.violations = append(analysis.violations, fmt.Sprintf(
			"root %s imports %s with forbidden binding %q", path, importPath, binding,
		))
	}
	return bindings
}

func (analysis *residualRouteHealthAnalysis) rejectRootNetworkImports(path string, file *ast.File) {
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || (importPath != "net" && importPath != "net/url") {
			continue
		}
		analysis.violations = append(analysis.violations, fmt.Sprintf(
			"root %s imports %s; endpoint parsing and dialing belong to internal/routehealth", path, importPath,
		))
	}
}

func (analysis *residualRouteHealthAnalysis) recordPackageName(path, kind, name string) {
	if residualRouteHealthMechanicNames[name] {
		analysis.violations = append(analysis.violations, fmt.Sprintf(
			"root %s declares forbidden residual routehealth %s %s", path, kind, name,
		))
	}
}

func (analysis *residualRouteHealthAnalysis) recordFunctionDeclaration(path string, function *ast.FuncDecl) {
	key := path + ":" + function.Name.Name
	if _, tracked := residualRouteHealthAdapterDecls[key]; tracked || residualRouteHealthAdapterName(function.Name.Name) {
		analysis.adapterDecls[key]++
	}
	if _, trackedFile := residualRouteHealthFileFunctions[path]; trackedFile {
		analysis.fileFunctions[key]++
	}
}

func residualRouteHealthAdapterName(name string) bool {
	for site := range residualRouteHealthAdapterDecls {
		if strings.HasSuffix(site, ":"+name) {
			return true
		}
	}
	return false
}

func (analysis *residualRouteHealthAnalysis) scanNode(
	path string,
	owner string,
	node ast.Node,
	routehealthBindings map[string]bool,
	serviceimplBindings map[string]bool,
	timeBindings map[string]bool,
	initialReceiverAliases map[*ast.Object]string,
) {
	if node == nil {
		return
	}
	site := path + ":" + owner
	receiverAliases := make(map[*ast.Object]string)
	for object, alias := range initialReceiverAliases {
		receiverAliases[object] = alias
	}
	ast.Inspect(node, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.AssignStmt:
			analysis.recordReceiverAliases(current.Lhs, current.Rhs, receiverAliases)
		case *ast.ValueSpec:
			left := make([]ast.Expr, 0, len(current.Names))
			for _, name := range current.Names {
				left = append(left, name)
			}
			analysis.recordReceiverAliases(left, current.Values, receiverAliases)
		case *ast.CallExpr:
			if selector, ok := current.Fun.(*ast.SelectorExpr); ok {
				analysis.recordImportedSelector(site, selector, true, routehealthBindings, serviceimplBindings)
				chain := residualSelectorChainWithAliases(selector, receiverAliases)
				if method, ok := residualCoordinatorMethodExpression(selector, routehealthBindings); ok {
					chain = "routehealth.AlivenessCoordinator." + method
				}
				if _, protected := residualRouteHealthMethodCallSites[site+":"+chain]; protected || residualRouteHealthMethodChain(chain) {
					analysis.methodCalls[site+":"+chain]++
				}
			}
		case *ast.SelectorExpr:
			analysis.recordImportedSelector(site, current, false, routehealthBindings, serviceimplBindings)
			chain := residualSelectorChainWithAliases(current, receiverAliases)
			if method, ok := residualCoordinatorMethodExpression(current, routehealthBindings); ok {
				chain = "routehealth.AlivenessCoordinator." + method
			}
			if _, protected := residualRouteHealthMethodCallSites[site+":"+chain]; protected || residualRouteHealthMethodChain(chain) {
				analysis.methodRefs[site+":"+chain]++
			}
			if selectorUsesImport(current, timeBindings) && residualRouteHealthTimerFunction(current.Sel.Name) {
				analysis.violations = append(analysis.violations, fmt.Sprintf(
					"root %s references time.%s from %s; aliveness timers and sleeps belong to internal/routehealth", path, current.Sel.Name, owner,
				))
			}
		case *ast.GoStmt:
			analysis.goStatements[site]++
		}
		return true
	})
}

func residualTypedCoordinatorAliases(function *ast.FuncType, routehealthBindings map[string]bool) map[*ast.Object]string {
	aliases := make(map[*ast.Object]string)
	if function == nil || function.Params == nil {
		return aliases
	}
	for _, field := range function.Params.List {
		if !residualRouteHealthCoordinatorType(field.Type, routehealthBindings) {
			continue
		}
		for _, name := range field.Names {
			if name.Obj != nil {
				aliases[name.Obj] = "routehealth.AlivenessCoordinator"
			}
		}
	}
	return aliases
}

func residualRouteHealthCoordinatorType(expr ast.Expr, routehealthBindings map[string]bool) bool {
	for {
		switch current := expr.(type) {
		case *ast.ParenExpr:
			expr = current.X
		case *ast.StarExpr:
			expr = current.X
		default:
			selector, ok := expr.(*ast.SelectorExpr)
			return ok && selector.Sel.Name == "AlivenessCoordinator" && selectorUsesImport(selector, routehealthBindings)
		}
	}
}

func residualCoordinatorMethodExpression(selector *ast.SelectorExpr, routehealthBindings map[string]bool) (string, bool) {
	if selector == nil {
		return "", false
	}
	switch selector.Sel.Name {
	case "Startup", "RequestRefresh", "StartLoop":
	default:
		return "", false
	}
	if !residualRouteHealthCoordinatorType(selector.X, routehealthBindings) {
		return "", false
	}
	return selector.Sel.Name, true
}

func (analysis *residualRouteHealthAnalysis) recordReceiverAliases(left, right []ast.Expr, aliases map[*ast.Object]string) {
	if len(left) != len(right) {
		return
	}
	for index, target := range left {
		name, ok := target.(*ast.Ident)
		if !ok || name.Obj == nil {
			continue
		}
		chain := residualSelectorChainWithAliases(right[index], aliases)
		switch chain {
		case "s.aliveness", "s.routeHealth", "feedback":
			aliases[name.Obj] = chain
		default:
			delete(aliases, name.Obj)
		}
	}
}

func (analysis *residualRouteHealthAnalysis) recordImportedSelector(
	site string,
	selector *ast.SelectorExpr,
	directCall bool,
	routehealthBindings map[string]bool,
	serviceimplBindings map[string]bool,
) {
	qualified := ""
	switch {
	case selectorUsesImport(selector, routehealthBindings) && residualRouteHealthImportedSymbol("routehealth", selector.Sel.Name):
		qualified = "routehealth." + selector.Sel.Name
	case selectorUsesImport(selector, serviceimplBindings) && residualRouteHealthImportedSymbol("serviceimpl", selector.Sel.Name):
		qualified = "serviceimpl." + selector.Sel.Name
	}
	if qualified == "" {
		return
	}
	key := site + ":" + qualified
	if directCall {
		analysis.importedCalls[key]++
		return
	}
	analysis.importedRefs[key]++
}

func residualRouteHealthImportedSymbol(owner, name string) bool {
	switch owner + "." + name {
	case "routehealth.RecordAttempt",
		"routehealth.ObserveFinalAttempt",
		"routehealth.SavePersistedState",
		"serviceimpl.IsDispatchReachabilityFailure",
		"serviceimpl.NewCatalogCacheKey":
		return true
	default:
		return false
	}
}

func residualRouteHealthMethodChain(chain string) bool {
	switch chain {
	case "s.aliveness.Startup",
		"s.aliveness.RequestRefresh",
		"s.aliveness.StartLoop",
		"routehealth.AlivenessCoordinator.Startup",
		"routehealth.AlivenessCoordinator.RequestRefresh",
		"routehealth.AlivenessCoordinator.StartLoop",
		"feedback.Record",
		"s.routeHealth.ActiveAttempts",
		"s.routeHealth.MetricSignals":
		return true
	default:
		return false
	}
}

func residualSelectorChainWithAliases(expr ast.Expr, aliases map[*ast.Object]string) string {
	switch current := expr.(type) {
	case *ast.Ident:
		if current.Obj != nil {
			if alias := aliases[current.Obj]; alias != "" {
				return alias
			}
		}
		return current.Name
	case *ast.SelectorExpr:
		prefix := residualSelectorChainWithAliases(current.X, aliases)
		if prefix == "" {
			return current.Sel.Name
		}
		return prefix + "." + current.Sel.Name
	case *ast.ParenExpr:
		return residualSelectorChainWithAliases(current.X, aliases)
	case *ast.StarExpr:
		return residualSelectorChainWithAliases(current.X, aliases)
	case *ast.UnaryExpr:
		if current.Op == token.AND {
			return residualSelectorChainWithAliases(current.X, aliases)
		}
		return ""
	default:
		return ""
	}
}

func residualRouteHealthTimerFunction(name string) bool {
	// Root currently has no sanctioned timer/sleep site. Keeping this list
	// root-wide prevents a renamed local probe loop from bypassing the exact
	// coordinator-call locks; unrelated timing mechanics should live with their
	// internal owner rather than in the public facade.
	switch name {
	case "After", "AfterFunc", "NewTicker", "NewTimer", "Sleep", "Tick":
		return true
	default:
		return false
	}
}

func (analysis *residualRouteHealthAnalysis) requireExact(label string, got, want map[string]int) {
	if reflectIntMapEqual(got, want) {
		return
	}
	analysis.violations = append(analysis.violations, fmt.Sprintf("root %s = %v, want %v", label, got, want))
}

func TestResidualRouteHealthOwnershipMutations(t *testing.T) {
	base := routeHealthMutationProductionFiles(t)
	assertRouteHealthMutationAllowed(t, base)
	t.Run("allow exact unrelated root goroutine sites", func(t *testing.T) {
		assertRouteHealthMutationAllowed(t, cloneRouteHealthMutationFiles(t, base))
	})

	names := make([]string, 0, len(residualRouteHealthMechanicNames))
	for name := range residualRouteHealthMechanicNames {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		name := name
		for _, mutation := range []struct {
			form   string
			source string
		}{
			{form: "package function", source: "func " + name + "() {}"},
			{form: "package method", source: "func (*service) " + name + "() {}"},
			{form: "package type", source: "type " + name + " struct{}"},
			{form: "package type alias", source: "type " + name + " = int"},
			{form: "package value", source: "var " + name + " = 1"},
			{form: "package function alias", source: "var " + name + " = func() {}"},
		} {
			mutation := mutation
			t.Run("reject "+mutation.form+"/"+name, func(t *testing.T) {
				files := cloneRouteHealthMutationFiles(t, base)
				files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\n"+mutation.source+"\n")
				assertRouteHealthMutationRejected(t, files, name)
			})
		}
		for _, control := range []struct {
			form   string
			source string
		}{
			{form: "local function shadow", source: "func localFunctionShadow() { " + name + " := func() {}; " + name + "() }"},
			{form: "local type shadow", source: "func localTypeShadow() { type " + name + " struct{}; _ = " + name + "{} }"},
			{form: "local value shadow", source: "func localValueShadow() { var " + name + " int; _ = " + name + " }"},
		} {
			control := control
			t.Run("allow "+control.form+"/"+name, func(t *testing.T) {
				files := cloneRouteHealthMutationFiles(t, base)
				files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\n"+control.source+"\n")
				assertRouteHealthMutationAllowed(t, files)
			})
		}
	}

	t.Run("renamed target imports preserve canonical sites", func(t *testing.T) {
		files := routeHealthMutationProductionFiles(t)
		for _, path := range []string{"service_aliveness.go", "service_dispatch_feedback.go"} {
			source := routeHealthMutationSource(t, path)
			source = strings.Replace(source, `"github.com/easel/fizeau/internal/routehealth"`, `health "github.com/easel/fizeau/internal/routehealth"`, 1)
			source = strings.ReplaceAll(source, "routehealth.", "health.")
			if path == "service_dispatch_feedback.go" {
				source = strings.Replace(source, `serviceimpl "github.com/easel/fizeau/internal/serviceimpl"`, `impl "github.com/easel/fizeau/internal/serviceimpl"`, 1)
				source = strings.ReplaceAll(source, "serviceimpl.", "impl.")
			}
			files[path] = parseRouteHealthMutationFile(t, path, source)
		}
		assertRouteHealthMutationAllowed(t, files)
	})

	for _, importCase := range []struct {
		name        string
		declaration string
		needle      string
	}{
		{name: "dot routehealth", declaration: `. "github.com/easel/fizeau/internal/routehealth"`, needle: `forbidden binding "."`},
		{name: "blank routehealth", declaration: `_ "github.com/easel/fizeau/internal/routehealth"`, needle: `forbidden binding "_"`},
		{name: "dot serviceimpl", declaration: `. "github.com/easel/fizeau/internal/serviceimpl"`, needle: `forbidden binding "."`},
		{name: "blank serviceimpl", declaration: `_ "github.com/easel/fizeau/internal/serviceimpl"`, needle: `forbidden binding "_"`},
		{name: "dot time", declaration: `. "time"`, needle: `forbidden binding "."`},
		{name: "blank time", declaration: `_ "time"`, needle: `forbidden binding "_"`},
	} {
		importCase := importCase
		t.Run("reject "+importCase.name+" import", func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nimport "+importCase.declaration+"\n")
			assertRouteHealthMutationRejected(t, files, importCase.needle)
		})
	}

	for _, networkImport := range []string{`"net"`, `network "net"`, `"net/url"`, `location "net/url"`} {
		networkImport := networkImport
		t.Run("reject root network import/"+networkImport, func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nimport "+networkImport+"\n")
			assertRouteHealthMutationRejected(t, files, "endpoint parsing and dialing belong to internal/routehealth")
		})
	}

	for _, timer := range []struct {
		name       string
		importDecl string
		call       string
	}{
		{name: "canonical sleep", importDecl: `"time"`, call: "time.Sleep(0)"},
		{name: "renamed sleep", importDecl: `clock "time"`, call: "clock.Sleep(0)"},
		{name: "canonical timer", importDecl: `"time"`, call: "time.NewTimer(0)"},
		{name: "renamed ticker", importDecl: `clock "time"`, call: "clock.NewTicker(0)"},
	} {
		timer := timer
		t.Run("reject "+timer.name, func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nimport "+timer.importDecl+"\nfunc probingTimer() { _ = "+timer.call+" }\n")
			assertRouteHealthMutationRejected(t, files, "aliveness timers and sleeps belong to internal/routehealth")
		})
	}

	for _, timerAlias := range []struct {
		name   string
		source string
	}{
		{
			name: "local time function alias",
			source: `package fizeau
import clock "time"
func aliasSleep() { sleep := clock.Sleep; _ = sleep }
`,
		},
		{
			name: "package time function alias",
			source: `package fizeau
import "time"
var sleep = time.Sleep
`,
		},
	} {
		timerAlias := timerAlias
		t.Run("reject "+timerAlias.name, func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", timerAlias.source)
			assertRouteHealthMutationRejected(t, files, "aliveness timers and sleeps belong to internal/routehealth")
		})
	}

	t.Run("allow local time qualifier shadow", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", `package fizeau
import "time"
var _ = time.Second
func shadowTimeQualifier() { time := struct{ Sleep int }{}; _ = time.Sleep }
`)
		assertRouteHealthMutationAllowed(t, files)
	})

	t.Run("reject added probing goroutine", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nfunc probingLoop() { go func() {}() }\n")
		assertRouteHealthMutationRejected(t, files, "root goroutine sites")
	})

	for _, state := range []struct {
		name   string
		source string
	}{
		{name: "package state", source: "var providerProbeRefreshInFlight bool"},
		{name: "named struct field", source: "type probeOwner struct { providerProbeRefreshInFlight bool }"},
		{name: "anonymous struct value type", source: "var probeOwner struct { providerProbeRefreshInFlight bool }"},
		{name: "anonymous struct composite value", source: "var probeOwner = struct { providerProbeRefreshInFlight bool }{}"},
	} {
		state := state
		t.Run("reject "+state.name, func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\n"+state.source+"\n")
			assertRouteHealthMutationRejected(t, files, "providerProbeRefreshInFlight")
		})
	}

	t.Run("allow local lifecycle state shadow", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nfunc localState() { providerProbeRefreshInFlight := false; _ = providerProbeRefreshInFlight }\n")
		assertRouteHealthMutationAllowed(t, files)
	})

	t.Run("allow local anonymous lifecycle state field", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nfunc localStateOwner() { var owner struct { providerProbeRefreshInFlight bool }; _ = owner }\n")
		assertRouteHealthMutationAllowed(t, files)
	})

	t.Run("reject duplicate internal entrypoint call", func(t *testing.T) {
		files := routeHealthMutationProductionFiles(t)
		source := routeHealthMutationSource(t, "service_dispatch_feedback.go")
		needle := "return routehealth.RecordAttempt("
		source = strings.Replace(source, needle, "_ = routehealth.RecordAttempt(routehealth.AttemptTransaction{}, routehealth.Attempt{})\n\treturn routehealth.RecordAttempt(", 1)
		files["service_dispatch_feedback.go"] = parseRouteHealthMutationFile(t, "service_dispatch_feedback.go", source)
		assertRouteHealthMutationRejected(t, files, "routehealth imported direct calls")
	})

	t.Run("reject internal entrypoint wrapper", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", `package fizeau
import health "github.com/easel/fizeau/internal/routehealth"
func wrappedRecordAttempt() { health.RecordAttempt(health.AttemptTransaction{}, health.Attempt{}) }
`)
		assertRouteHealthMutationRejected(t, files, "routehealth imported direct calls")
	})

	t.Run("reject local internal function alias", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", `package fizeau
import health "github.com/easel/fizeau/internal/routehealth"
func aliasRecordAttempt() { record := health.RecordAttempt; _ = record }
`)
		assertRouteHealthMutationRejected(t, files, "routehealth imported references")
	})

	t.Run("reject package internal function alias", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", `package fizeau
import health "github.com/easel/fizeau/internal/routehealth"
var recordAttempt = health.RecordAttempt
`)
		assertRouteHealthMutationRejected(t, files, "routehealth imported references")
	})

	t.Run("reject moved coordinator method call", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nfunc (s *service) wrappedStartup() { s.aliveness.Startup(nil, nil, 0) }\n")
		assertRouteHealthMutationRejected(t, files, "routehealth method direct calls")
	})

	t.Run("reject receiver alias duplicate coordinator call", func(t *testing.T) {
		files := routeHealthMutationProductionFiles(t)
		source := routeHealthMutationSource(t, "service_aliveness.go")
		needle := "s.aliveness.Startup(ctx, s.alivenessEndpoints(), 0)"
		replacement := "coordinator := s.aliveness\n\tcoordinator.Startup(ctx, s.alivenessEndpoints(), 0)\n\t" + needle
		source = strings.Replace(source, needle, replacement, 1)
		files["service_aliveness.go"] = parseRouteHealthMutationFile(t, "service_aliveness.go", source)
		assertRouteHealthMutationRejected(t, files, "routehealth method direct calls")
	})

	t.Run("reject receiver alias moved coordinator call", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nfunc (s *service) aliasedStartup() { coordinator := s.aliveness; coordinator.Startup(nil, nil, 0) }\n")
		assertRouteHealthMutationRejected(t, files, "routehealth method direct calls")
	})

	t.Run("reject pointer receiver alias coordinator call", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", "package fizeau\nfunc (s *service) pointerAliasedStartup() { coordinator := &s.aliveness; (*coordinator).Startup(nil, nil, 0) }\n")
		assertRouteHealthMutationRejected(t, files, "routehealth method direct calls")
	})

	for _, method := range []struct {
		name string
		args string
	}{
		{name: "Startup", args: "nil, nil, 0"},
		{name: "RequestRefresh", args: "nil, 0"},
		{name: "StartLoop", args: "nil, nil, 0"},
	} {
		method := method
		t.Run("reject coordinator method expression alias/"+method.name, func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", fmt.Sprintf(`package fizeau
import health "github.com/easel/fizeau/internal/routehealth"
var coordinatorMethod = (*health.AlivenessCoordinator).%s
`, method.name))
			assertRouteHealthMutationRejected(t, files, "routehealth method references")
		})

		t.Run("reject typed coordinator wrapper/"+method.name, func(t *testing.T) {
			files := cloneRouteHealthMutationFiles(t, base)
			files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", fmt.Sprintf(`package fizeau
import health "github.com/easel/fizeau/internal/routehealth"
func coordinatorWrapper(coordinator *health.AlivenessCoordinator) { coordinator.%s(%s) }
`, method.name, method.args))
			assertRouteHealthMutationRejected(t, files, "routehealth method direct calls")
		})
	}

	t.Run("reject wrapper added to adapter file", func(t *testing.T) {
		files := routeHealthMutationProductionFiles(t)
		source := routeHealthMutationSource(t, "service_aliveness.go") + "\nfunc (*service) renamedProbeWrapper() {}\n"
		files["service_aliveness.go"] = parseRouteHealthMutationFile(t, "service_aliveness.go", source)
		assertRouteHealthMutationRejected(t, files, "service_aliveness.go function declarations")
	})

	t.Run("allow local import qualifier shadow", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", `package fizeau
import routehealth "github.com/easel/fizeau/internal/routehealth"
var _ = routehealth.Attempt{}
func shadowQualifier() {
	routehealth := struct{ RecordAttempt int }{}
	_ = routehealth.RecordAttempt
}
`)
		assertRouteHealthMutationAllowed(t, files)
	})

	t.Run("allow unrelated package selectors", func(t *testing.T) {
		files := cloneRouteHealthMutationFiles(t, base)
		files["mutation.go"] = parseRouteHealthMutationFile(t, "mutation.go", `package fizeau
import other "example.com/routehealth"
var _, _, _ = other.RecordAttempt, other.ObserveFinalAttempt, other.SavePersistedState
func unrelatedSelectors() { other.Startup(); other.RequestRefresh(); other.StartLoop() }
`)
		assertRouteHealthMutationAllowed(t, files)
	})
}

func routeHealthMutationProductionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	return cloneRouteHealthMutationFiles(t, parseRootProductionFiles(t))
}

func cloneRouteHealthMutationFiles(t *testing.T, files map[string]*ast.File) map[string]*ast.File {
	t.Helper()
	cloned := make(map[string]*ast.File, len(files))
	for path := range files {
		cloned[path] = parseRouteHealthMutationFile(t, path, routeHealthMutationSource(t, path))
	}
	return cloned
}

func routeHealthMutationSource(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mutation production source %s: %v", path, err)
	}
	return string(contents)
}

func parseRouteHealthMutationFile(t *testing.T, path, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse routehealth mutation %s: %v", path, err)
	}
	return file
}

func assertRouteHealthMutationAllowed(t *testing.T, files map[string]*ast.File) {
	t.Helper()
	if violations := residualRouteHealthOwnershipViolations(files); len(violations) != 0 {
		t.Fatalf("unexpected residual routehealth ownership violations:\n%s", strings.Join(violations, "\n"))
	}
}

func assertRouteHealthMutationRejected(t *testing.T, files map[string]*ast.File, needle string) {
	t.Helper()
	violations := residualRouteHealthOwnershipViolations(files)
	if len(violations) == 0 {
		t.Fatal("mutation passed residual routehealth ownership analysis")
	}
	if !strings.Contains(strings.Join(violations, "\n"), needle) {
		t.Fatalf("ownership violations do not contain %q:\n%s", needle, strings.Join(violations, "\n"))
	}
}
