package fizeau

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	serviceimplImportPath = "github.com/easel/fizeau/internal/serviceimpl"
	routehealthImportPath = "github.com/easel/fizeau/internal/routehealth"
)

// TestRootFacadeSourceAllowlist locks the deliberate non-test source files that
// remain at the module root after the ADR-008 extraction chain. This keeps the
// root facade audit from drifting back toward the pre-refactor "everything at
// root" inventory.
func TestRootFacadeSourceAllowlist(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}

	var got []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		got = append(got, name)
	}
	slices.Sort(got)

	want := []string{
		"metadata_billing.go",
		"options_prod.go",
		"options_testseam.go",
		"provider_burn_rate.go",
		"provider_quota_state.go",
		"public_api.go",
		"public_cli_api.go",
		"role_correlation.go",
		"routing_errors.go",
		"service.go",
		"service_aliveness.go",
		"service_capabilities.go",
		"service_catalog_cache.go",
		"service_catalog_impl_adapters.go",
		"service_dispatch_feedback.go",
		"service_events.go",
		"service_execute.go",
		"service_execute_dispatch.go",
		"service_execute_fanout.go",
		"service_execute_route.go",
		"service_execute_seam_prod.go",
		"service_execute_seam_testseam.go",
		"service_harness_instances.go",
		"service_model_resolution.go",
		"service_models.go",
		"service_native_provider.go",
		"service_openrouter_credit.go",
		"service_override.go",
		"service_policies.go",
		"service_probe.go",
		"service_projection.go",
		"service_providers.go",
		"service_reasoning.go",
		"service_routing.go",
		"service_routing_quality.go",
		"service_session_projection.go",
		"service_snapshot.go",
		"service_stale_harness_reaper.go",
		"service_status.go",
		"testseam_types.go",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("root source allowlist mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestRootRoutingInputMechanicsStayInternal locks the root routing-input
// surface to public contract identity plus the four intended internal calls.
// Surface preference, credential classification, snapshot-health adaptation,
// and OpenRouter key-shape validation belong to internal/serviceimpl.
func TestRootRoutingInputMechanicsStayInternal(t *testing.T) {
	files := parseRootProductionFiles(t)
	forbiddenDecls := map[string]bool{
		"routingSurfacePreference":            true,
		"providerCredentialMissingMap":        true,
		"credentialMissingForProvider":        true,
		"openrouterAPIKeyWellFormed":          true,
		"providerCooldownsFromSnapshotErrors": true,
		"isSnapshotDialFailure":               true,
		"isDispatchabilityFailure":            true,
	}
	targetInternalCalls := map[string]bool{
		"RoutingSurfacePreference":            true,
		"ProviderCredentialMissing":           true,
		"ProviderCooldownsFromSnapshotErrors": true,
		"OpenRouterAPIKeyWellFormed":          true,
	}
	forbiddenRoutehealthRefs := map[string]bool{
		"ProviderCooldownsFromSnapshotErrors": true,
		"IsDispatchabilityFailure":            true,
	}
	wantRootCalls := map[string]int{
		"service_routing.go:routingInputs:RoutingSurfacePreference":                   1,
		"service_routing.go:routingInputs:ProviderCredentialMissing":                  1,
		"service_routing.go:routingInputs:ProviderCooldownsFromSnapshotErrors":        1,
		"service_openrouter_credit.go:openrouterProbeMaps:OpenRouterAPIKeyWellFormed": 1,
	}
	rootCalls := make(map[string]int)
	rootRefs := make(map[string]int)
	publicTypes := make(map[string]*ast.TypeSpec)
	publicTypeFiles := make(map[string]string)
	providerUsesLiveDiscoveryDecls := 0
	serviceConfigSourceIdentifiers := 0
	serviceConfigSourceDeclarationIdentifiers := 0

	for path, file := range files {
		serviceimplBindings, invalidServiceimplBindings := importBindingsForPath(file, serviceimplImportPath, "serviceimpl")
		for _, binding := range invalidServiceimplBindings {
			t.Errorf("root %s imports %s with forbidden binding %q", path, serviceimplImportPath, binding)
		}
		routehealthBindings, invalidRoutehealthBindings := importBindingsForPath(file, routehealthImportPath, "routehealth")
		for _, binding := range invalidRoutehealthBindings {
			t.Errorf("root %s imports %s with forbidden binding %q", path, routehealthImportPath, binding)
		}
		serviceConfigSourceIdentifiers += countUnqualifiedRootIdentifiers(file, "ServiceConfigSource")
		ast.Inspect(file, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.SelectorExpr:
				if forbiddenImportedSelector(current, routehealthBindings, forbiddenRoutehealthRefs) {
					t.Errorf("root %s references forbidden direct routehealth seam %s", path, current.Sel.Name)
				}
			}
			return true
		})
		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				if forbiddenDecls[current.Name.Name] {
					t.Errorf("root %s declares migrated routing-input mechanic %s", path, current.Name.Name)
				}
				if current.Name.Name == "providerUsesLiveDiscovery" {
					providerUsesLiveDiscoveryDecls++
					if path != "service_routing.go" {
						t.Errorf("providerUsesLiveDiscovery declared in %s, want service_routing.go pending fizeau-4623caea", path)
					}
				}
				if current.Body == nil {
					continue
				}
				ast.Inspect(current.Body, func(node ast.Node) bool {
					switch expression := node.(type) {
					case *ast.CallExpr:
						if name, ok := expression.Fun.(*ast.Ident); ok && forbiddenDecls[name.Name] {
							t.Errorf("root %s %s calls migrated root mechanic %s", path, current.Name.Name, name.Name)
						}
						selector, ok := expression.Fun.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						if !selectorUsesImport(selector, serviceimplBindings) || !targetInternalCalls[selector.Sel.Name] {
							return true
						}
						key := path + ":" + current.Name.Name + ":" + selector.Sel.Name
						rootCalls[key]++
						assertRootRoutingInputCallArguments(t, selector.Sel.Name, expression)
					case *ast.SelectorExpr:
						if selectorUsesImport(expression, serviceimplBindings) && targetInternalCalls[expression.Sel.Name] {
							key := path + ":" + current.Name.Name + ":" + expression.Sel.Name
							rootRefs[key]++
						}
					}
					return true
				})
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						if forbiddenDecls[named.Name.Name] {
							t.Errorf("root %s declares migrated routing-input type %s", path, named.Name.Name)
						}
						if named.Name.Name == "ServiceConfigSource" {
							serviceConfigSourceDeclarationIdentifiers++
						}
						if isRootRoutingPublicType(named.Name.Name) {
							if previous := publicTypeFiles[named.Name.Name]; previous != "" {
								t.Errorf("root type %s declared in both %s and %s", named.Name.Name, previous, path)
							}
							publicTypes[named.Name.Name] = named
							publicTypeFiles[named.Name.Name] = path
						}
					case *ast.ValueSpec:
						for _, name := range named.Names {
							if forbiddenDecls[name.Name] {
								t.Errorf("root %s aliases migrated routing-input mechanic %s", path, name.Name)
							}
						}
					}
				}
				ast.Inspect(current, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if selectorUsesImport(selector, serviceimplBindings) && targetInternalCalls[selector.Sel.Name] {
						t.Errorf("root %s aliases serviceimpl.%s outside an intended call site", path, selector.Sel.Name)
					}
					return true
				})
			}
		}
	}

	if !reflectIntMapEqual(rootCalls, wantRootCalls) {
		t.Errorf("root routing-input internal calls = %v, want %v", rootCalls, wantRootCalls)
	}
	if !reflectIntMapEqual(rootRefs, wantRootCalls) {
		t.Errorf("root routing-input internal references = %v, want only direct calls %v", rootRefs, wantRootCalls)
	}
	if providerUsesLiveDiscoveryDecls != 1 {
		t.Errorf("root providerUsesLiveDiscovery declarations = %d, want 1 pending fizeau-4623caea", providerUsesLiveDiscoveryDecls)
	}
	if serviceConfigSourceIdentifiers != 1 || serviceConfigSourceDeclarationIdentifiers != 1 {
		t.Errorf(
			"root ServiceConfigSource identifiers = %d with %d declarations, want its sole occurrence to be one TypeSpec declaration",
			serviceConfigSourceIdentifiers,
			serviceConfigSourceDeclarationIdentifiers,
		)
	}

	assertRootRoutingPublicTypes(t, publicTypes, publicTypeFiles)
	assertServiceConfigSourceDeprecated(t)
	assertInternalRoutingCredentialValidationCall(t)
}

func importBindingsForPath(file *ast.File, targetPath, defaultName string) (map[string]bool, []string) {
	bindings := make(map[string]bool)
	var invalid []string
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != targetPath {
			continue
		}
		if spec.Name == nil {
			bindings[defaultName] = true
			continue
		}
		switch spec.Name.Name {
		case ".", "_":
			invalid = append(invalid, spec.Name.Name)
		default:
			bindings[spec.Name.Name] = true
		}
	}
	return bindings, invalid
}

func selectorUsesImport(selector *ast.SelectorExpr, bindings map[string]bool) bool {
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Obj == nil && bindings[qualifier.Name]
}

func forbiddenImportedSelector(selector *ast.SelectorExpr, bindings, forbidden map[string]bool) bool {
	return selectorUsesImport(selector, bindings) && forbidden[selector.Sel.Name]
}

func countUnqualifiedRootIdentifiers(file *ast.File, name string) int {
	excluded := make(map[*ast.Ident]bool)
	explicitImportAliases := make(map[string]bool)
	for _, spec := range file.Imports {
		if spec.Name == nil {
			continue
		}
		excluded[spec.Name] = true
		explicitImportAliases[spec.Name.Name] = true
	}

	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.SelectorExpr:
			excluded[current.Sel] = true
			qualifier, ok := current.X.(*ast.Ident)
			if ok && qualifier.Obj == nil && explicitImportAliases[qualifier.Name] {
				excluded[qualifier] = true
			}
		case *ast.Ident:
			if current.Name == name && !excluded[current] {
				count++
			}
		}
		return true
	})
	return count
}

func TestRoutingInputTargetImportBindingsAreAliasAware(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		targetPath    string
		defaultName   string
		wantBinding   string
		wantInvalid   []string
		wantSelector  bool
		wantForbidden bool
	}{
		{
			name:         "default serviceimpl import",
			source:       `package fizeau; import "github.com/easel/fizeau/internal/serviceimpl"; var _ = serviceimpl.RoutingSurfacePreference`,
			targetPath:   serviceimplImportPath,
			defaultName:  "serviceimpl",
			wantBinding:  "serviceimpl",
			wantSelector: true,
		},
		{
			name:         "renamed serviceimpl import",
			source:       `package fizeau; import impl "github.com/easel/fizeau/internal/serviceimpl"; var _ = impl.RoutingSurfacePreference`,
			targetPath:   serviceimplImportPath,
			defaultName:  "serviceimpl",
			wantBinding:  "impl",
			wantSelector: true,
		},
		{
			name:          "renamed routehealth import",
			source:        `package fizeau; import health "github.com/easel/fizeau/internal/routehealth"; var _ = health.IsDispatchabilityFailure`,
			targetPath:    routehealthImportPath,
			defaultName:   "routehealth",
			wantBinding:   "health",
			wantSelector:  true,
			wantForbidden: true,
		},
		{
			name:        "shadowed serviceimpl alias",
			source:      `package fizeau; import impl "github.com/easel/fizeau/internal/serviceimpl"; func f() { impl := struct{ RoutingSurfacePreference bool }{}; _ = impl.RoutingSurfacePreference }`,
			targetPath:  serviceimplImportPath,
			defaultName: "serviceimpl",
			wantBinding: "impl",
		},
		{
			name:        "shadowed routehealth alias",
			source:      `package fizeau; import health "github.com/easel/fizeau/internal/routehealth"; func f() { health := struct{ IsDispatchabilityFailure bool }{}; _ = health.IsDispatchabilityFailure }`,
			targetPath:  routehealthImportPath,
			defaultName: "routehealth",
			wantBinding: "health",
		},
		{
			name:        "dot import rejected",
			source:      `package fizeau; import . "github.com/easel/fizeau/internal/serviceimpl"`,
			targetPath:  serviceimplImportPath,
			defaultName: "serviceimpl",
			wantInvalid: []string{"."},
		},
		{
			name:        "blank import rejected",
			source:      `package fizeau; import _ "github.com/easel/fizeau/internal/routehealth"`,
			targetPath:  routehealthImportPath,
			defaultName: "routehealth",
			wantInvalid: []string{"_"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "mutation.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse mutation source: %v", err)
			}
			bindings, invalid := importBindingsForPath(file, test.targetPath, test.defaultName)
			if !slices.Equal(invalid, test.wantInvalid) {
				t.Fatalf("invalid bindings = %v, want %v", invalid, test.wantInvalid)
			}
			if test.wantBinding == "" {
				if len(bindings) != 0 {
					t.Fatalf("bindings = %v, want none", bindings)
				}
				return
			}
			if len(bindings) != 1 || !bindings[test.wantBinding] {
				t.Fatalf("bindings = %v, want only %q", bindings, test.wantBinding)
			}
			matchedSelector := false
			matchedForbidden := false
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if ok && selectorUsesImport(selector, bindings) {
					matchedSelector = true
				}
				if ok && forbiddenImportedSelector(selector, bindings, map[string]bool{
					"ProviderCooldownsFromSnapshotErrors": true,
					"IsDispatchabilityFailure":            true,
				}) {
					matchedForbidden = true
				}
				return true
			})
			if matchedSelector != test.wantSelector {
				t.Fatalf("selector match = %t, want %t for binding %q", matchedSelector, test.wantSelector, test.wantBinding)
			}
			if matchedForbidden != test.wantForbidden {
				t.Fatalf("forbidden selector match = %t, want %t", matchedForbidden, test.wantForbidden)
			}
		})
	}
}

func TestUnqualifiedRootIdentifierCountingExcludesQualifiedNames(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   int
	}{
		{
			name:   "declaration only",
			source: `package fizeau; type ServiceConfigSource interface{ ProviderNames() []string }`,
			want:   1,
		},
		{
			name:   "unqualified consumer counted",
			source: `package fizeau; type ServiceConfigSource interface{}; var _ ServiceConfigSource`,
			want:   2,
		},
		{
			name:   "unrelated external selector excluded",
			source: `package fizeau; import other "example.com/other"; type ServiceConfigSource interface{}; var _ = other.ServiceConfigSource`,
			want:   1,
		},
		{
			name:   "explicit import alias excluded",
			source: `package fizeau; import ServiceConfigSource "example.com/other"; var _ = ServiceConfigSource.Value`,
			want:   0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "mutation.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse mutation source: %v", err)
			}
			if got := countUnqualifiedRootIdentifiers(file, "ServiceConfigSource"); got != test.want {
				t.Fatalf("unqualified ServiceConfigSource identifiers = %d, want %d", got, test.want)
			}
		})
	}
}

func isRootRoutingPublicType(name string) bool {
	switch name {
	case "ServiceProviderEntry", "RouteDecision", "RouteCandidate", "ServiceConfigSource":
		return true
	default:
		return false
	}
}

func assertRootRoutingInputCallArguments(t *testing.T, name string, call *ast.CallExpr) {
	t.Helper()
	switch name {
	case "RoutingSurfacePreference":
		if len(call.Args) != 1 {
			t.Errorf("serviceimpl.RoutingSurfacePreference arguments = %d, want 1", len(call.Args))
			return
		}
		env, ok := call.Args[0].(*ast.CallExpr)
		if !ok || !selectorMatches(env.Fun, "os", "Getenv") || len(env.Args) != 1 {
			t.Errorf("serviceimpl.RoutingSurfacePreference must receive os.Getenv kill-switch value")
			return
		}
		literal, ok := env.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING || literal.Value != `"FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT"` {
			t.Errorf("surface-preference environment key = %v, want FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", env.Args[0])
		}
	case "ProviderCredentialMissing":
		assertIdentifierArguments(t, call, "providerNames", "providers")
	case "ProviderCooldownsFromSnapshotErrors":
		assertIdentifierArguments(t, call, "snapshot", "providerNames", "now", "healthCooldownTTL")
	case "OpenRouterAPIKeyWellFormed":
		assertIdentifierArguments(t, call, "apiKey")
	}
}

func assertIdentifierArguments(t *testing.T, call *ast.CallExpr, want ...string) {
	t.Helper()
	if len(call.Args) != len(want) {
		t.Errorf("call arguments = %d, want %d (%v)", len(call.Args), len(want), want)
		return
	}
	for index, name := range want {
		if !identMatches(call.Args[index], name) {
			t.Errorf("call argument %d = %T, want identifier %s", index, call.Args[index], name)
		}
	}
}

func assertRootRoutingPublicTypes(t *testing.T, specs map[string]*ast.TypeSpec, paths map[string]string) {
	t.Helper()
	for name, wantPath := range map[string]string{
		"ServiceProviderEntry": "service.go",
		"RouteDecision":        "service.go",
		"RouteCandidate":       "service.go",
	} {
		spec := specs[name]
		if spec == nil {
			t.Errorf("missing root public struct %s", name)
			continue
		}
		if paths[name] != wantPath {
			t.Errorf("root public struct %s declared in %s, want %s", name, paths[name], wantPath)
		}
		if spec.Assign.IsValid() {
			t.Errorf("root public struct %s is a type alias", name)
		}
		if _, ok := spec.Type.(*ast.StructType); !ok {
			t.Errorf("root public %s type = %T, want concrete struct", name, spec.Type)
		}
	}

	spec := specs["ServiceConfigSource"]
	if spec == nil {
		t.Error("missing root ServiceConfigSource compatibility interface")
		return
	}
	if paths["ServiceConfigSource"] != "service_routing.go" {
		t.Errorf("ServiceConfigSource declared in %s, want service_routing.go", paths["ServiceConfigSource"])
	}
	if spec.Assign.IsValid() {
		t.Error("ServiceConfigSource is a type alias, want concrete root interface")
	}
	contract, ok := spec.Type.(*ast.InterfaceType)
	if !ok {
		t.Errorf("ServiceConfigSource type = %T, want interface", spec.Type)
		return
	}
	if contract.Methods.NumFields() != 1 || len(contract.Methods.List) != 1 {
		t.Errorf("ServiceConfigSource methods = %d, want only ProviderNames", contract.Methods.NumFields())
		return
	}
	method := contract.Methods.List[0]
	if len(method.Names) != 1 || method.Names[0].Name != "ProviderNames" {
		t.Errorf("ServiceConfigSource method = %v, want ProviderNames", method.Names)
		return
	}
	signature, ok := method.Type.(*ast.FuncType)
	if !ok || signature.Params.NumFields() != 0 || signature.Results.NumFields() != 1 ||
		!sliceOfIdent(signature.Results.List[0].Type, "string") {
		t.Error("ServiceConfigSource.ProviderNames signature must remain func() []string")
	}
}

func assertServiceConfigSourceDeprecated(t *testing.T) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "service_routing.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse service_routing.go comments: %v", err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			named, ok := spec.(*ast.TypeSpec)
			if !ok || named.Name.Name != "ServiceConfigSource" {
				continue
			}
			comment := ""
			if gen.Doc != nil {
				comment += gen.Doc.Text()
			}
			if named.Doc != nil {
				comment += named.Doc.Text()
			}
			lower := strings.ToLower(comment)
			if !strings.Contains(lower, "deprecated:") || !strings.Contains(lower, "compatib") {
				t.Errorf("ServiceConfigSource comment must retain compatibility and Deprecated marker; got %q", comment)
			}
			return
		}
	}
	t.Fatal("ServiceConfigSource declaration missing while checking deprecation comment")
}

func assertInternalRoutingCredentialValidationCall(t *testing.T) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "internal/serviceimpl/provider_credentials.go", nil, 0)
	if err != nil {
		t.Fatalf("parse internal credential projection: %v", err)
	}
	calls := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, nameOK := call.Fun.(*ast.Ident)
			if !nameOK || name.Name != "OpenRouterAPIKeyWellFormed" {
				return true
			}
			calls++
			if fn.Name.Name != "ProviderCredentialMissing" {
				t.Errorf("internal OpenRouterAPIKeyWellFormed called from %s, want ProviderCredentialMissing", fn.Name.Name)
			}
			if len(call.Args) != 1 || !qualifiedFieldMatches(call.Args[0], "provider", "APIKey") {
				t.Error("ProviderCredentialMissing must validate provider.APIKey exactly once")
			}
			return true
		})
	}
	if calls != 1 {
		t.Errorf("internal routing OpenRouterAPIKeyWellFormed calls = %d, want exactly 1", calls)
	}
}

func reflectIntMapEqual(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

// TestRootCatalogCacheMechanicsStayInternal locks the root catalog surface to
// concrete public compatibility declarations plus the two narrow service
// wiring seams. Cache state, defaults, classifiers, and snapshots belong to
// internal/serviceimpl.
func TestRootCatalogCacheMechanicsStayInternal(t *testing.T) {
	files := parseRootProductionFiles(t)
	forbiddenDecls := map[string]bool{
		"catalogCacheOptions":       true,
		"catalogCacheKey":           true,
		"catalogCache":              true,
		"catalogCacheSnapshot":      true,
		"newCatalogCache":           true,
		"newCatalogCacheKey":        true,
		"catalogResultFromInternal": true,
	}
	callSites := make(map[string]int)
	serviceCatalogFields := 0

	for path, file := range files {
		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				if forbiddenDecls[current.Name.Name] {
					t.Errorf("root %s declares %s; catalog mechanics belong to internal/serviceimpl", path, current.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					named, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if forbiddenDecls[named.Name.Name] {
						t.Errorf("root %s declares %s; catalog mechanics belong to internal/serviceimpl", path, named.Name.Name)
					}
					if named.Name.Name != "service" {
						continue
					}
					structure, ok := named.Type.(*ast.StructType)
					if !ok {
						t.Fatal("root service declaration is not a struct")
					}
					for _, field := range structure.Fields.List {
						if len(field.Names) != 1 || field.Names[0].Name != "catalog" {
							continue
						}
						serviceCatalogFields++
						pointer, ok := field.Type.(*ast.StarExpr)
						if !ok || !selectorMatches(pointer.X, "serviceimpl", "CatalogCache") {
							t.Errorf("service.catalog type = %T, want *serviceimpl.CatalogCache", field.Type)
						}
					}
				}
			}
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch {
			case selectorMatches(call.Fun, "serviceimpl", "NewCatalogCache"):
				callSites[path+":NewCatalogCache"]++
				assertCatalogCacheConstruction(t, call)
			case selectorMatches(call.Fun, "serviceimpl", "NewCatalogCacheKey"):
				callSites[path+":NewCatalogCacheKey"]++
				assertCatalogCacheKeyConstruction(t, call)
			case catalogFieldSelectorMatches(call.Fun, "RecordDispatchError"):
				callSites[path+":RecordDispatchError"]++
				assertCatalogDispatchRecording(t, call)
			case catalogFieldSelectorMatches(call.Fun, "Get"),
				catalogFieldSelectorMatches(call.Fun, "Snapshot"),
				catalogFieldSelectorMatches(call.Fun, "snapshot"):
				t.Errorf("root %s calls service.catalog.%s; production catalog reads and snapshots are not part of this boundary", path, selectorName(call.Fun))
			}
			return true
		})
	}

	if serviceCatalogFields != 1 {
		t.Errorf("root service.catalog fields = %d, want exactly 1", serviceCatalogFields)
	}
	wantCallSites := map[string]int{
		"service.go:NewCatalogCache":                       1,
		"service_dispatch_feedback.go:NewCatalogCacheKey":  1,
		"service_dispatch_feedback.go:RecordDispatchError": 1,
	}
	if len(callSites) != len(wantCallSites) {
		t.Fatalf("catalog wiring call sites = %v, want %v", callSites, wantCallSites)
	}
	for site, want := range wantCallSites {
		if got := callSites[site]; got != want {
			t.Errorf("catalog wiring %s calls = %d, want %d", site, got, want)
		}
	}

	assertRootCatalogPublicSurface(t, files["service_catalog_cache.go"])
	assertCatalogFieldComment(t)
	assertInternalCatalogSnapshotAbsent(t)

	if got := ErrDiscoveryUnsupported(); got != errDiscoveryUnsupported {
		t.Fatalf("ErrDiscoveryUnsupported() = %p, want root singleton %p", got, errDiscoveryUnsupported)
	}
	if _, ok := ErrDiscoveryUnsupported().(*discoveryUnsupportedError); !ok {
		t.Fatalf("ErrDiscoveryUnsupported() type = %T, want *discoveryUnsupportedError", ErrDiscoveryUnsupported())
	}
}

func catalogFieldSelectorMatches(expr ast.Expr, method string) bool {
	callSelector, ok := expr.(*ast.SelectorExpr)
	if !ok || callSelector.Sel.Name != method {
		return false
	}
	field, ok := callSelector.X.(*ast.SelectorExpr)
	return ok && field.Sel.Name == "catalog" && identMatches(field.X, "s")
}

func assertCatalogCacheConstruction(t *testing.T, call *ast.CallExpr) {
	t.Helper()
	if len(call.Args) != 1 {
		t.Errorf("serviceimpl.NewCatalogCache arguments = %d, want 1", len(call.Args))
		return
	}
	literal, ok := call.Args[0].(*ast.CompositeLit)
	if !ok || !selectorMatches(literal.Type, "serviceimpl", "CatalogCacheOptions") {
		t.Errorf("serviceimpl.NewCatalogCache argument = %T, want serviceimpl.CatalogCacheOptions literal", call.Args[0])
		return
	}
	fields := make(map[string]ast.Expr)
	for _, element := range literal.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		key, keyOK := kv.Key.(*ast.Ident)
		if !ok || !keyOK {
			t.Errorf("serviceimpl.CatalogCacheOptions contains non-keyed field %T", element)
			continue
		}
		fields[key.Name] = kv.Value
	}
	if len(fields) != 2 {
		t.Errorf("serviceimpl.CatalogCacheOptions fields = %v, want AsyncRefreshTimeout and DiscoveryUnsupported", fields)
	}
	if !zeroArgMethodCall(fields["AsyncRefreshTimeout"], "opts", "catalogRefreshTimeout") {
		t.Errorf("AsyncRefreshTimeout does not call opts.catalogRefreshTimeout()")
	}
	if !zeroArgFunctionCall(fields["DiscoveryUnsupported"], "ErrDiscoveryUnsupported") {
		t.Errorf("DiscoveryUnsupported does not call ErrDiscoveryUnsupported()")
	}
}

func assertCatalogCacheKeyConstruction(t *testing.T, call *ast.CallExpr) {
	t.Helper()
	if len(call.Args) != 3 || !identMatches(call.Args[0], "baseURL") ||
		!qualifiedFieldMatches(call.Args[1], "pcfg", "APIKey") ||
		!qualifiedFieldMatches(call.Args[2], "pcfg", "Headers") {
		t.Errorf("serviceimpl.NewCatalogCacheKey must receive baseURL, pcfg.APIKey, pcfg.Headers")
	}
}

func assertCatalogDispatchRecording(t *testing.T, call *ast.CallExpr) {
	t.Helper()
	if len(call.Args) != 2 || !identMatches(call.Args[0], "key") || !identMatches(call.Args[1], "err") {
		t.Errorf("service.catalog.RecordDispatchError must receive key and err")
	}
}

func assertRootCatalogPublicSurface(t *testing.T, file *ast.File) {
	t.Helper()
	if file == nil {
		t.Fatal("missing service_catalog_cache.go")
	}
	for _, imp := range file.Imports {
		if strings.Contains(imp.Path.Value, "internal/serviceimpl") {
			t.Errorf("service_catalog_cache.go imports internal/serviceimpl; it must contain only root public declarations")
		}
	}

	allowedTypes := map[string]bool{
		"CatalogProbeFunc":          false,
		"CatalogResult":             false,
		"discoveryUnsupportedError": false,
	}
	allowedVars := map[string]bool{"errDiscoveryUnsupported": false}
	allowedFuncs := map[string]int{"Error": 0, "ErrDiscoveryUnsupported": 0}

	for _, decl := range file.Decls {
		switch current := decl.(type) {
		case *ast.FuncDecl:
			if _, ok := allowedFuncs[current.Name.Name]; !ok {
				t.Errorf("service_catalog_cache.go declares unexpected function %s", current.Name.Name)
				continue
			}
			allowedFuncs[current.Name.Name]++
			if current.Name.Name == "ErrDiscoveryUnsupported" {
				assertRootDiscoveryUnsupportedFunction(t, current)
			}
		case *ast.GenDecl:
			for _, spec := range current.Specs {
				switch currentSpec := spec.(type) {
				case *ast.TypeSpec:
					if _, ok := allowedTypes[currentSpec.Name.Name]; !ok {
						t.Errorf("service_catalog_cache.go declares unexpected type %s", currentSpec.Name.Name)
						continue
					}
					allowedTypes[currentSpec.Name.Name] = true
					if currentSpec.Assign.IsValid() {
						t.Errorf("%s is a type alias; root public identity must remain concrete", currentSpec.Name.Name)
					}
					switch currentSpec.Name.Name {
					case "CatalogProbeFunc":
						assertCatalogProbeFuncType(t, currentSpec.Type)
					case "CatalogResult":
						assertCatalogResultType(t, currentSpec.Type)
					case "discoveryUnsupportedError":
						structure, ok := currentSpec.Type.(*ast.StructType)
						if !ok || structure.Fields.NumFields() != 0 {
							t.Errorf("discoveryUnsupportedError must remain a concrete empty struct")
						}
					}
				case *ast.ValueSpec:
					for _, name := range currentSpec.Names {
						if _, ok := allowedVars[name.Name]; !ok {
							t.Errorf("service_catalog_cache.go declares unexpected variable %s", name.Name)
							continue
						}
						allowedVars[name.Name] = true
					}
					if len(currentSpec.Names) == 1 && currentSpec.Names[0].Name == "errDiscoveryUnsupported" {
						assertRootDiscoveryUnsupportedSingleton(t, currentSpec)
					}
				}
			}
		}
	}

	for name, found := range allowedTypes {
		if !found {
			t.Errorf("service_catalog_cache.go is missing concrete type %s", name)
		}
	}
	for name, found := range allowedVars {
		if !found {
			t.Errorf("service_catalog_cache.go is missing root singleton %s", name)
		}
	}
	for name, count := range allowedFuncs {
		if count != 1 {
			t.Errorf("service_catalog_cache.go %s declarations = %d, want 1", name, count)
		}
	}
}

func assertCatalogProbeFuncType(t *testing.T, expr ast.Expr) {
	t.Helper()
	fn, ok := expr.(*ast.FuncType)
	if !ok || fn.Params.NumFields() != 1 || fn.Results.NumFields() != 2 {
		t.Errorf("CatalogProbeFunc must remain func(context.Context) ([]string, error)")
		return
	}
	if !selectorMatches(fn.Params.List[0].Type, "context", "Context") ||
		!sliceOfIdent(fn.Results.List[0].Type, "string") ||
		!identMatches(fn.Results.List[1].Type, "error") {
		t.Errorf("CatalogProbeFunc signature changed")
	}
}

func assertCatalogResultType(t *testing.T, expr ast.Expr) {
	t.Helper()
	structure, ok := expr.(*ast.StructType)
	if !ok {
		t.Errorf("CatalogResult is %T, want struct", expr)
		return
	}
	wantTypes := map[string]func(ast.Expr) bool{
		"IDs":                func(expr ast.Expr) bool { return sliceOfIdent(expr, "string") },
		"FetchedAt":          func(expr ast.Expr) bool { return selectorMatches(expr, "time", "Time") },
		"DiscoverySupported": func(expr ast.Expr) bool { return identMatches(expr, "bool") },
		"LastErr":            func(expr ast.Expr) bool { return identMatches(expr, "error") },
		"FromCache":          func(expr ast.Expr) bool { return identMatches(expr, "bool") },
		"Stale":              func(expr ast.Expr) bool { return identMatches(expr, "bool") },
	}
	wantOrder := []string{"IDs", "FetchedAt", "DiscoverySupported", "LastErr", "FromCache", "Stale"}
	if structure.Fields.NumFields() != len(wantOrder) {
		t.Errorf("CatalogResult fields = %d, want %d", structure.Fields.NumFields(), len(wantOrder))
	}
	for index, field := range structure.Fields.List {
		if len(field.Names) != 1 {
			t.Errorf("CatalogResult field declaration must contain exactly one named field")
			continue
		}
		name := field.Names[0].Name
		if index >= len(wantOrder) {
			t.Errorf("CatalogResult has extra field %d = %s", index, name)
		} else if name != wantOrder[index] {
			t.Errorf("CatalogResult field %d = %s, want %s", index, name, wantOrder[index])
		}
		matches, ok := wantTypes[name]
		if !ok {
			t.Errorf("CatalogResult has unexpected field %s", name)
			continue
		}
		if !matches(field.Type) {
			t.Errorf("CatalogResult.%s has unexpected type %T", name, field.Type)
		}
		delete(wantTypes, name)
	}
	for name := range wantTypes {
		t.Errorf("CatalogResult is missing field %s", name)
	}
}

func assertRootDiscoveryUnsupportedSingleton(t *testing.T, value *ast.ValueSpec) {
	t.Helper()
	if len(value.Values) != 1 {
		t.Errorf("errDiscoveryUnsupported initializers = %d, want 1", len(value.Values))
		return
	}
	address, ok := value.Values[0].(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		t.Errorf("errDiscoveryUnsupported must be initialized from &discoveryUnsupportedError{}")
		return
	}
	literal, ok := address.X.(*ast.CompositeLit)
	if !ok || !identMatches(literal.Type, "discoveryUnsupportedError") || len(literal.Elts) != 0 {
		t.Errorf("errDiscoveryUnsupported must be initialized from &discoveryUnsupportedError{}")
	}
}

func assertRootDiscoveryUnsupportedFunction(t *testing.T, fn *ast.FuncDecl) {
	t.Helper()
	if fn.Recv != nil || fn.Type.Params.NumFields() != 0 || fn.Type.Results.NumFields() != 1 ||
		!identMatches(fn.Type.Results.List[0].Type, "error") || fn.Body == nil || len(fn.Body.List) != 1 {
		t.Errorf("ErrDiscoveryUnsupported must remain func() error returning the root singleton")
		return
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 || !identMatches(ret.Results[0], "errDiscoveryUnsupported") {
		t.Errorf("ErrDiscoveryUnsupported must return errDiscoveryUnsupported directly")
	}
}

func assertCatalogFieldComment(t *testing.T) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "service.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse service.go with comments: %v", err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			named, ok := spec.(*ast.TypeSpec)
			if !ok || named.Name.Name != "service" {
				continue
			}
			structure := named.Type.(*ast.StructType)
			for _, field := range structure.Fields.List {
				if len(field.Names) != 1 || field.Names[0].Name != "catalog" {
					continue
				}
				comment := ""
				if field.Doc != nil {
					comment = strings.ToLower(field.Doc.Text())
				}
				if !strings.Contains(comment, "dispatch") || !strings.Contains(comment, "reachability feedback") ||
					!strings.Contains(comment, "internal/serviceimpl") {
					t.Errorf("service.catalog comment must describe dispatch reachability feedback and its internal owner; got %q", comment)
				}
				for _, staleClaim := range []string{"populated lazily", "first use", "routing + chat", "probed per-dispatch"} {
					if strings.Contains(comment, staleClaim) {
						t.Errorf("service.catalog comment still claims nonexistent production discovery via %q", staleClaim)
					}
				}
				return
			}
		}
	}
	t.Fatal("service.catalog field not found while checking its comment")
}

func assertInternalCatalogSnapshotAbsent(t *testing.T) {
	t.Helper()
	dir := "internal/serviceimpl"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				if current.Name.Name == "Snapshot" && receiverTypeName(current.Recv) == "CatalogCache" {
					t.Errorf("%s still exposes transition-only (*CatalogCache).Snapshot", path)
				}
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					if named, ok := spec.(*ast.TypeSpec); ok && named.Name.Name == "CatalogSnapshot" {
						t.Errorf("%s still exposes transition-only CatalogSnapshot", path)
					}
				}
			}
		}
	}
}

func receiverTypeName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) != 1 {
		return ""
	}
	expr := fields.List[0].Type
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = pointer.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func identMatches(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func qualifiedFieldMatches(expr ast.Expr, owner, field string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == field && identMatches(selector.X, owner)
}

func sliceOfIdent(expr ast.Expr, element string) bool {
	slice, ok := expr.(*ast.ArrayType)
	return ok && slice.Len == nil && identMatches(slice.Elt, element)
}

func zeroArgFunctionCall(expr ast.Expr, function string) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && len(call.Args) == 0 && identMatches(call.Fun, function)
}

func zeroArgMethodCall(expr ast.Expr, owner, method string) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && len(call.Args) == 0 && qualifiedFieldMatches(call.Fun, owner, method)
}

// TestRootHarnessCapabilityMechanicsStayInternal locks the root capability
// surface to public contract declarations and field-for-field projection.
// Status/detail classification belongs to internal/serviceimpl.
func TestRootHarnessCapabilityMechanicsStayInternal(t *testing.T) {
	forbiddenDecls := map[string]bool{
		"capRequired":              true,
		"capOptional":              true,
		"capUnsupported":           true,
		"capNotApplicable":         true,
		"harnessCapabilityMatrix":  true,
		"serviceExecuteWired":      true,
		"executePromptCapability":  true,
		"modelDiscoveryCapability": true,
		"modelPinningCapability":   true,
		"workdirContextCapability": true,
		"reasoningCapability":      true,
		"permissionCapability":     true,
		"progressEventsCapability": true,
		"usageCaptureCapability":   true,
		"finalTextCapability":      true,
		"toolEventsCapability":     true,
		"quotaStatusCapability":    true,
		"recordReplayCapability":   true,
	}
	classifyCalls := 0
	projectionCalls := 0
	for path, file := range parseRootProductionFiles(t) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if forbiddenDecls[fn.Name.Name] {
				t.Errorf("root %s declares %s; harness capability mechanics belong to internal/serviceimpl", path, fn.Name.Name)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selectorMatches(call.Fun, "serviceimpl", "ClassifyHarnessCapabilities") {
				classifyCalls++
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "publicHarnessCapabilityMatrix" {
				projectionCalls++
			}
			return true
		})
	}
	if classifyCalls != 1 {
		t.Errorf("root internal/serviceimpl ClassifyHarnessCapabilities calls = %d, want exactly 1", classifyCalls)
	}
	if projectionCalls != 1 {
		t.Errorf("root publicHarnessCapabilityMatrix calls = %d, want exactly 1", projectionCalls)
	}

	capabilityFile := parseRootProductionFiles(t)["service_capabilities.go"]
	if capabilityFile == nil {
		t.Fatal("missing public service_capabilities.go")
	}
	allowedAdapters := map[string]bool{
		"publicHarnessCapabilityMatrix": true,
		"publicHarnessCapability":       true,
	}
	for _, decl := range capabilityFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && !allowedAdapters[fn.Name.Name] {
			t.Errorf("service_capabilities.go declares non-projection function %s", fn.Name.Name)
		}
	}
}

// TestRootSubscriptionQuotaOwnershipBoundary prevents the deleted quota
// adapter from returning under another filename. Root routing seams consume
// the API-neutral internal/quota projection directly; subscription quota math
// and projection helpers remain internal implementation details.
func TestRootSubscriptionQuotaOwnershipBoundary(t *testing.T) {
	if _, err := os.Stat("service_subscription_quota.go"); err == nil {
		t.Fatal("obsolete root implementation file service_subscription_quota.go still exists")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat service_subscription_quota.go: %v", err)
	}

	forbidden := map[string]bool{
		"subscriptionQuotaView":       true,
		"subscriptionQuotaForHarness": true,
		"maxQuotaWindowUsedPercent":   true,
		"quotaTrend":                  true,
	}
	directCalls := make(map[string]int)
	for path, file := range parseRootProductionFiles(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			switch current := node.(type) {
			case *ast.Ident:
				if forbidden[current.Name] {
					t.Errorf("root %s contains %s; subscription quota mechanics belong to internal/quota", path, current.Name)
				}
			case *ast.CallExpr:
				if selectorMatches(current.Fun, "quotaimpl", "SubscriptionForHarness") {
					directCalls[path]++
				}
			}
			return true
		})
	}

	wantCalls := map[string]int{
		"service_execute_route.go": 1,
		"service_routing.go":       1,
	}
	if len(directCalls) != len(wantCalls) {
		t.Fatalf("direct internal/quota SubscriptionForHarness calls = %v, want %v", directCalls, wantCalls)
	}
	for path, want := range wantCalls {
		if got := directCalls[path]; got != want {
			t.Errorf("%s direct internal/quota SubscriptionForHarness calls = %d, want %d", path, got, want)
		}
	}
}

// TestRootQuotaOwnershipBoundary locks recovery scheduling and signal
// transition mechanics behind internal/quota while preserving the named
// public facades and the ServiceConfig-backed recovery probe seam at root.
func TestRootQuotaOwnershipBoundary(t *testing.T) {
	forbiddenDecls := map[string]bool{
		"runQuotaRecoveryProbeLoop": true,
		"runQuotaRecoveryProbePass": true,
		"nextQuotaRecoveryBackoff":  true,
		"quotaRecoverySleep":        true,
	}
	requiredTypes := map[string]bool{
		"QuotaRecoveryProber":     false,
		"ProviderQuotaStateStore": false,
		"ProviderBurnRateTracker": false,
	}
	recoveryCalls := 0
	observerCalls := 0
	var observer *ast.FuncDecl

	for path, file := range parseRootProductionFiles(t) {
		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				if forbiddenDecls[current.Name.Name] {
					t.Errorf("root %s declares %s; quota recovery scheduling belongs to internal/quota", path, current.Name.Name)
				}
				if current.Name.Name == "quotaSignalObserver" && current.Recv != nil {
					observer = current
				}
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					if named, ok := spec.(*ast.TypeSpec); ok {
						if _, required := requiredTypes[named.Name.Name]; required {
							requiredTypes[named.Name.Name] = true
						}
					}
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch {
			case selectorMatches(call.Fun, "quotaimpl", "RunRecoveryLoop"):
				recoveryCalls++
			case selectorMatches(call.Fun, "quotaimpl", "NewSignalObserver"):
				observerCalls++
			case selectorName(call.Fun) == "IsExhausted":
				t.Errorf("root %s calls Signal.IsExhausted; signal transitions belong to internal/quota", path)
			}
			return true
		})
	}

	if recoveryCalls != 1 {
		t.Fatalf("root internal/quota RunRecoveryLoop calls = %d, want exactly 1", recoveryCalls)
	}
	if observerCalls != 1 {
		t.Fatalf("root internal/quota NewSignalObserver calls = %d, want exactly 1", observerCalls)
	}
	for name, found := range requiredTypes {
		if !found {
			t.Errorf("root public facade type %s is missing", name)
		}
	}
	if observer == nil || observer.Body == nil {
		t.Fatal("missing (*service).quotaSignalObserver adapter")
	}
	ast.Inspect(observer.Body, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.SelectorExpr:
			if ident, ok := current.X.(*ast.Ident); ok && ident.Name == "time" {
				t.Errorf("root quotaSignalObserver contains time arithmetic %s; defaults belong to internal/quota", current.Sel.Name)
			}
		case *ast.CallExpr:
			switch selectorName(current.Fun) {
			case "MarkQuotaExhausted", "MarkAvailable", "State", "AllExhausted":
				t.Errorf("root quotaSignalObserver calls %s; StateStore transitions belong to internal/quota", selectorName(current.Fun))
			}
		}
		return true
	})
}

func selectorName(expr ast.Expr) string {
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		return selector.Sel.Name
	}
	return ""
}

// TestRootStickyStateOwnershipBoundary prevents the deleted sticky adapter
// from returning under a different filename or through direct concrete-store
// access. Root production may retain the service-owned StickyState and narrow
// API-neutral calls, but lease/utilization mechanics belong to routehealth.
func TestRootStickyStateOwnershipBoundary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}

	forbidden := []string{
		"routeStickyState",
		"routeLeaseStore",
		"routeUtilizationStore",
		"routeEndpointLoadsResolver",
		"routeStickyServerInstanceResolver",
		"stickyRouteLeaseTTL",
		"stickyRouteAffinityBonus",
		"routehealth.LeaseStore",
		"routehealth.UtilizationStore",
		"NewLeaseStore(",
		"NewUtilizationStore(",
		".LeaseStore()",
		".UtilizationStore()",
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(contents), needle) {
				t.Errorf("root production file %s contains forbidden sticky implementation seam %q", name, needle)
			}
		}
	}
}
