package fizeau

import (
	"fmt"
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
	quotaImportPath       = "github.com/easel/fizeau/internal/quota"
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
		"service_continuation.go",
		"service_cost.go",
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
		"service_portable_runtime.go",
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

// TestRootFacadeTestAllowlist locks the package-internal root tests that still
// need direct access to facade adapters, composition seams, or structural
// ownership checks. Public contract tests belong in package fizeau_test, and
// implementation mechanics belong beside their internal package owners.
func TestRootFacadeTestAllowlist(t *testing.T) {
	want := []string{
		"claude_quota_test_helpers_test.go",
		"harness_golden_integration_test.go",
		"no_viable_provider_for_now_test.go",
		"quota_header_observer_test.go",
		"service_adr005_test.go",
		"service_aliveness_test.go",
		"service_cleanup_options_test.go",
		"service_context_capacity_test.go",
		"service_context_overflow_test.go",
		"service_continuation_internal_test.go",
		"service_contract_post_refactor_structural_test.go",
		"service_contract_pre_refactor_baseline_test.go",
		"service_contract_snapshot_test.go",
		"service_cost_test.go",
		"service_dispatch_feedback_test.go",
		"service_execute_claudetui_default_test.go",
		"service_execute_dispatch_test.go",
		"service_execute_harness_pin_test.go",
		"service_execute_session_log_adapter_test.go",
		"service_haiku_alias_eligibility_test.go",
		"service_http_provider_test.go",
		"service_model_resolution_test.go",
		"service_models_test.go",
		"service_new_internal_test.go",
		"service_openrouter_credit_test.go",
		"service_override_internal_test.go",
		"service_portable_runtime_inventory_test.go",
		"service_portable_runtime_providers_test.go",
		"service_probe_test.go",
		"service_projection_internal_test.go",
		"service_providers_test.go",
		"service_root_facade_allowlist_test.go",
		"service_route_attempts_test.go",
		"service_route_budget_test.go",
		"service_route_evidence_test.go",
		"service_route_leases_test.go",
		"service_routehealth_boundary_test.go",
		"service_routehealth_ownership_test.go",
		"service_routestatus_nil_config_test.go",
		"service_routing_errors_test.go",
		"service_routing_quality_test.go",
		"service_routing_test.go",
		"service_snapshot_autorouting_test.go",
		"service_snapshot_test.go",
		"service_stale_harness_reaper_unix_test.go",
		"service_status_internal_test.go",
		"service_test_helpers_test.go",
		"service_transcript_ownership_test.go",
		"service_usage_routing_quality_test.go",
		"service_wrapped_route_observation_test.go",
	}

	got := rootPackageInternalTestFiles(t)
	if mismatch := rootFacadeTestAllowlistMismatch(got, want); mismatch != "" {
		t.Fatal(mismatch)
	}
	if violations := rootFacadeTestOwnershipViolations(parseRootPackageInternalTests(t)); len(violations) != 0 {
		t.Fatalf("root package-fizeau test ownership violations: %v", violations)
	}

	t.Run("rejects deleted session hub file drift", func(t *testing.T) {
		mutated := append(slices.Clone(got), "service_session_hub_test.go")
		slices.Sort(mutated)
		mismatch := rootFacadeTestAllowlistMismatch(mutated, want)
		if !strings.Contains(mismatch, "service_session_hub_test.go") {
			t.Fatalf("known implementation helper drift was not rejected: %q", mismatch)
		}
	})

	t.Run("rejects session hub helper drift in allowed test", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "service_routing_test.go", `package fizeau

func newSessionHub() any { return nil }
`, 0)
		if err != nil {
			t.Fatalf("parse mutation: %v", err)
		}
		violations := rootFacadeTestOwnershipViolations(map[string]*ast.File{
			"service_routing_test.go": file,
		})
		if !slices.ContainsFunc(violations, func(violation string) bool {
			return strings.Contains(violation, "newSessionHub")
		}) {
			t.Fatalf("newSessionHub mutation passed package-fizeau test ownership check: %v", violations)
		}
	})
}

func rootPackageInternalTestFiles(t *testing.T) []string {
	t.Helper()
	files := parseRootPackageInternalTests(t)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func parseRootPackageInternalTests(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}

	files := make(map[string]*ast.File)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if file.Name.Name == "fizeau" {
			files[name] = file
		}
	}
	return files
}

func rootFacadeTestOwnershipViolations(files map[string]*ast.File) []string {
	var violations []string
	for path, file := range files {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.Name == "newSessionHub" {
				violations = append(violations, fmt.Sprintf(
					"root package-fizeau test %s declares newSessionHub; construct serviceimpl.NewSessionHub explicitly",
					path,
				))
			}
		}
	}
	slices.Sort(violations)
	return violations
}

func rootFacadeTestAllowlistMismatch(got, want []string) string {
	if slices.Equal(got, want) {
		return ""
	}
	return fmt.Sprintf("root package-fizeau test allowlist mismatch\nwant: %v\ngot:  %v", want, got)
}

// TestRootRoutingInputMechanicsStayInternal locks the root routing-input
// surface to public contract identity plus the four intended internal calls.
// Surface preference, credential classification, snapshot-health adaptation,
// and OpenRouter key-shape validation belong to internal/serviceimpl.
func TestRootRoutingInputMechanicsStayInternal(t *testing.T) {
	files := parseRootProductionFiles(t)
	forbiddenDecls := rootRoutingInputMechanicNames()
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
	providerUsesLiveDiscoveryRefs := 0
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
			case *ast.Ident:
				if current.Name == "providerUsesLiveDiscovery" {
					providerUsesLiveDiscoveryRefs++
					t.Errorf("root %s references dead routing-input mechanic providerUsesLiveDiscovery", path)
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
	if providerUsesLiveDiscoveryRefs != 0 {
		t.Errorf("root providerUsesLiveDiscovery references = %d, want none", providerUsesLiveDiscoveryRefs)
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

func rootRoutingInputMechanicNames() map[string]bool {
	return map[string]bool{
		"routingSurfacePreference":            true,
		"providerCredentialMissingMap":        true,
		"credentialMissingForProvider":        true,
		"openrouterAPIKeyWellFormed":          true,
		"providerCooldownsFromSnapshotErrors": true,
		"isSnapshotDialFailure":               true,
		"isDispatchabilityFailure":            true,
		"providerUsesLiveDiscovery":           true,
	}
}

// TestOpenRouterCreditMechanicsStayInternal locks the OpenRouter cache,
// single-flight, HTTP, decoding, failure classification, threshold, TTL, and
// candidate-normalization mechanics behind internal/quota. The root retains
// only public-config and routing-evidence adapters plus the compatibility
// constants.
func TestOpenRouterCreditMechanicsStayInternal(t *testing.T) {
	for _, violation := range rootOpenRouterCreditOwnershipViolations(parseRootProductionFiles(t)) {
		t.Error(violation)
	}
}

func rootOpenRouterCreditOwnershipViolations(files map[string]*ast.File) []string {
	var violations []string
	forbiddenDeclarations := map[string]bool{
		"openrouterCreditFailureMode":                true,
		"openrouterCreditFailureNone":                true,
		"openrouterCreditFailureCredentialInvalid":   true,
		"openrouterCreditFailureProviderUnreachable": true,
		"openrouterCreditProbeTimeout":               true,
		"openrouterCreditFreshnessSource":            true,
		"openrouterCreditRecord":                     true,
		"openrouterCreditStore":                      true,
		"openrouterCreditProbeResult":                true,
		"openrouterCreditsResponse":                  true,
		"newOpenrouterCreditStore":                   true,
		"openrouterCreditsEndpoint":                  true,
		"openrouterCreditThresholdFor":               true,
		"openrouterCreditTTLFor":                     true,
		"openrouterCreditExhaustedMap":               true,
		"candidateBaseProviderName":                  true,
	}
	forbiddenCreditFileImports := map[string]bool{
		"encoding/json": true,
		"fmt":           true,
		"io":            true,
		"net/http":      true,
		"sync":          true,
	}
	targetQuotaCalls := map[string]bool{
		"NewOpenRouterCreditStore":  true,
		"ProjectOpenRouterCredits":  true,
		"OpenRouterCreditFreshness": true,
	}
	wantCalls := map[string]int{
		"service.go:New:NewOpenRouterCreditStore":                                                  1,
		"service_openrouter_credit.go:openrouterProbeMaps:ProjectOpenRouterCredits":                1,
		"service_openrouter_credit.go:annotateOpenrouterCreditFreshness:OpenRouterCreditFreshness": 1,
	}
	calls := make(map[string]int)
	refs := make(map[string]int)

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		file := files[path]
		quotaBindings, invalidQuotaBindings := importBindingsForPath(file, quotaImportPath, "quota")
		httpBindings, _ := importBindingsForPath(file, "net/http", "http")
		syncBindings, _ := importBindingsForPath(file, "sync", "sync")
		for _, binding := range invalidQuotaBindings {
			violations = append(violations, fmt.Sprintf("root %s imports %s with forbidden binding %q", path, quotaImportPath, binding))
		}
		if path == "service_openrouter_credit.go" {
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err == nil && forbiddenCreditFileImports[importPath] {
					violations = append(violations, fmt.Sprintf("root %s imports migrated credit-mechanic package %s", path, importPath))
				}
			}
		}

		for _, declaration := range file.Decls {
			owner := "package scope"
			switch current := declaration.(type) {
			case *ast.FuncDecl:
				owner = current.Name.Name
				if forbiddenDeclarations[current.Name.Name] {
					violations = append(violations, fmt.Sprintf("root %s declares migrated OpenRouter credit mechanic %s", path, current.Name.Name))
				} else if rootOpenRouterCreditMechanicName(current.Name.Name) {
					violations = append(violations, fmt.Sprintf("root %s declares renamed OpenRouter credit mechanic %s", path, current.Name.Name))
				}
				if rootOpenRouterCreditProbeSignature(current, httpBindings) {
					violations = append(violations, fmt.Sprintf("root %s function %s contains migrated OpenRouter credit HTTP-probe mechanics", path, current.Name.Name))
				}
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						if forbiddenDeclarations[named.Name.Name] {
							violations = append(violations, fmt.Sprintf("root %s declares migrated OpenRouter credit type %s", path, named.Name.Name))
						} else if rootOpenRouterCreditMechanicName(named.Name.Name) {
							violations = append(violations, fmt.Sprintf("root %s declares renamed OpenRouter credit type %s", path, named.Name.Name))
						}
						if rootOpenRouterCreditStructSignature(named, httpBindings, syncBindings) {
							violations = append(violations, fmt.Sprintf("root %s type %s contains migrated OpenRouter credit cache/response mechanics", path, named.Name.Name))
						}
						if named.Assign.IsValid() && rootOpenRouterCreditMechanicName(named.Name.Name) {
							violations = append(violations, fmt.Sprintf("root %s aliases OpenRouter credit type %s", path, named.Name.Name))
						}
					case *ast.ValueSpec:
						for _, name := range named.Names {
							if forbiddenDeclarations[name.Name] {
								violations = append(violations, fmt.Sprintf("root %s aliases migrated OpenRouter credit mechanic %s", path, name.Name))
							} else if rootOpenRouterCreditMechanicName(name.Name) {
								violations = append(violations, fmt.Sprintf("root %s declares renamed OpenRouter credit value %s", path, name.Name))
							}
						}
					}
				}
			}

			ast.Inspect(declaration, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok || !selectorUsesImport(selector, quotaBindings) || !targetQuotaCalls[selector.Sel.Name] {
					return true
				}
				key := path + ":" + owner + ":" + selector.Sel.Name
				refs[key]++
				return true
			})
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !selectorUsesImport(selector, quotaBindings) || !targetQuotaCalls[selector.Sel.Name] {
					return true
				}
				key := path + ":" + owner + ":" + selector.Sel.Name
				calls[key]++
				return true
			})
		}
	}
	if !reflectIntMapEqual(calls, wantCalls) {
		violations = append(violations, fmt.Sprintf("root OpenRouter quota calls = %v, want %v", calls, wantCalls))
	}
	if !reflectIntMapEqual(refs, wantCalls) {
		violations = append(violations, fmt.Sprintf("root OpenRouter quota references = %v, want direct calls only %v", refs, wantCalls))
	}
	return violations
}

func rootOpenRouterCreditMechanicName(name string) bool {
	switch name {
	case "DefaultOpenrouterCreditBalanceThresholdUSD",
		"DefaultOpenrouterCreditProbeTTL",
		"openrouterProbeProjection",
		"openrouterProbeMaps",
		"annotateOpenrouterCreditFreshness":
		return false
	}
	lower := strings.ToLower(name)
	containsAny := func(terms ...string) bool {
		for _, term := range terms {
			if strings.Contains(lower, term) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.Contains(lower, "openrouter") && containsAny("credit", "balance", "probe", "cache", "freshness"):
		return true
	case strings.Contains(lower, "credit") && containsAny("probe", "balance", "cache", "record", "store", "failure", "freshness", "threshold", "ttl", "response"):
		return true
	case strings.Contains(lower, "balance") && containsAny("probe", "cache", "record", "store", "threshold"):
		return true
	case strings.Contains(lower, "probe") && containsAny("credit", "balance", "account", "credential", "response", "cache"):
		return true
	default:
		return false
	}
}

func rootOpenRouterCreditStructSignature(spec *ast.TypeSpec, httpBindings, syncBindings map[string]bool) bool {
	structure, ok := spec.Type.(*ast.StructType)
	if !ok {
		return false
	}
	mapCount := 0
	hasCacheMap := false
	hasMapToChannel := false
	hasSyncMap := false
	hasHTTPState := false
	hasMutex := false
	hasTotalCredits := false
	hasTotalUsage := false
	for _, field := range structure.Fields.List {
		if mapType, isMap := field.Type.(*ast.MapType); isMap {
			mapCount++
			if rootTypeLooksLikeChannel(mapType.Value) {
				hasMapToChannel = true
			} else {
				hasCacheMap = true
			}
		}
		hasSyncMap = hasSyncMap || rootTypeUsesImportedSelector(field.Type, syncBindings, "Map")
		hasMutex = hasMutex || rootTypeUsesImportedSelector(field.Type, syncBindings, "Mutex") || rootTypeUsesImportedSelector(field.Type, syncBindings, "RWMutex")
		hasHTTPState = hasHTTPState ||
			rootTypeUsesImportedSelector(field.Type, httpBindings, "RoundTripper") ||
			rootTypeUsesImportedSelector(field.Type, httpBindings, "Response") ||
			rootTypeUsesImportedSelector(field.Type, httpBindings, "Client")
		for _, name := range field.Names {
			switch strings.ToLower(name.Name) {
			case "totalcredits":
				hasTotalCredits = true
			case "totalusage":
				hasTotalUsage = true
			}
		}
		if field.Tag != nil {
			hasTotalCredits = hasTotalCredits || strings.Contains(field.Tag.Value, "total_credits")
			hasTotalUsage = hasTotalUsage || strings.Contains(field.Tag.Value, "total_usage")
		}
	}
	return (hasMutex && hasHTTPState) ||
		(hasHTTPState && hasSyncMap) ||
		(hasHTTPState && hasMapToChannel && (hasCacheMap || mapCount >= 2)) ||
		(hasMutex && hasMapToChannel && rootOpenRouterCreditMechanicName(spec.Name.Name)) ||
		((hasCacheMap || hasSyncMap) && rootOpenRouterCreditMechanicName(spec.Name.Name)) ||
		(hasHTTPState && rootOpenRouterCreditMechanicName(spec.Name.Name)) ||
		(hasTotalCredits && hasTotalUsage)
}

func rootTypeLooksLikeChannel(expression ast.Expr) bool {
	for {
		switch current := expression.(type) {
		case *ast.ChanType:
			return true
		case *ast.StarExpr:
			expression = current.X
		case *ast.ParenExpr:
			expression = current.X
		case *ast.Ident:
			lower := strings.ToLower(current.Name)
			return strings.Contains(lower, "chan") ||
				strings.Contains(lower, "channel") ||
				strings.Contains(lower, "waiter") ||
				strings.Contains(lower, "flight") ||
				strings.Contains(lower, "signal") ||
				lower == "done"
		case *ast.SelectorExpr:
			lower := strings.ToLower(current.Sel.Name)
			return strings.Contains(lower, "chan") ||
				strings.Contains(lower, "channel") ||
				strings.Contains(lower, "waiter") ||
				strings.Contains(lower, "flight") ||
				strings.Contains(lower, "signal") ||
				lower == "done"
		default:
			return false
		}
	}
}

func rootTypeUsesImportedSelector(expression ast.Expr, bindings map[string]bool, selectorName string) bool {
	for {
		switch current := expression.(type) {
		case *ast.StarExpr:
			expression = current.X
		case *ast.ArrayType:
			expression = current.Elt
		case *ast.ParenExpr:
			expression = current.X
		default:
			selector, ok := expression.(*ast.SelectorExpr)
			return ok && selector.Sel.Name == selectorName && selectorUsesImport(selector, bindings)
		}
	}
}

func rootOpenRouterCreditProbeSignature(function *ast.FuncDecl, httpBindings map[string]bool) bool {
	if function.Body == nil {
		return false
	}
	hasCreditsEndpoint := false
	hasRequestConstruction := false
	hasAuthorization := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.BasicLit:
			if current.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(current.Value)
			if err != nil {
				return true
			}
			hasCreditsEndpoint = hasCreditsEndpoint || strings.Contains(value, "/credits")
			hasAuthorization = hasAuthorization || value == "Authorization" || strings.HasPrefix(value, "Bearer ")
		case *ast.SelectorExpr:
			if selectorUsesImport(current, httpBindings) && (current.Sel.Name == "NewRequest" || current.Sel.Name == "NewRequestWithContext") {
				hasRequestConstruction = true
			}
		}
		return true
	})
	return hasCreditsEndpoint && (hasRequestConstruction || hasAuthorization)
}

func TestOpenRouterCreditOwnershipCheckRejectsMutations(t *testing.T) {
	mutations := []struct {
		name          string
		source        string
		wantViolation string
	}{
		{name: "failure none", source: `package fizeau; const openrouterCreditFailureNone = ""`, wantViolation: "openrouterCreditFailureNone"},
		{name: "credential failure", source: `package fizeau; const openrouterCreditFailureCredentialInvalid = "credential_invalid"`, wantViolation: "openrouterCreditFailureCredentialInvalid"},
		{name: "unreachable failure", source: `package fizeau; const openrouterCreditFailureProviderUnreachable = "provider_unreachable"`, wantViolation: "openrouterCreditFailureProviderUnreachable"},
		{name: "probe timeout", source: `package fizeau; import "time"; const openrouterCreditProbeTimeout = 5*time.Second`, wantViolation: "openrouterCreditProbeTimeout"},
		{name: "freshness source", source: `package fizeau; const openrouterCreditFreshnessSource = "openrouter_credits_probe"`, wantViolation: "openrouterCreditFreshnessSource"},
		{name: "private cache type", source: `package fizeau; type openrouterCreditStore struct{ records map[string]float64 }`, wantViolation: "openrouterCreditStore"},
		{name: "private alias", source: `package fizeau; import quota "github.com/easel/fizeau/internal/quota"; type openrouterCreditStore = quota.OpenRouterCreditStore`, wantViolation: "aliases OpenRouter credit type"},
		{name: "renamed balance cache", source: `package fizeau; type openrouterBalanceCache struct{records map[string]float64}`, wantViolation: "renamed OpenRouter credit type openrouterBalanceCache"},
		{name: "structural renamed cache", source: `package fizeau; import "net/http"; type accountState struct{ records map[string]float64; inFlight map[string]chan struct{}; transport http.RoundTripper }`, wantViolation: "cache/response mechanics"},
		{name: "generic mutex flight transport cache", source: `package fizeau; import ("net/http"; "sync"); type accountState struct{ mu sync.Mutex; inFlight map[string]chan struct{}; transport http.RoundTripper }`, wantViolation: "cache/response mechanics"},
		{name: "field independent cache owner", source: `package fizeau; import ("net/http"; "sync"); type accountState struct { mu sync.Mutex; observations map[string]float64; pending map[string]chan struct{}; client http.RoundTripper }`, wantViolation: "cache/response mechanics"},
		{name: "renamed client and named waiter", source: `package fizeau; import (web "net/http"; state "sync"); type waiterSignal chan struct{}; type accountState struct { lock state.RWMutex; values map[string]float64; sleepers map[string]*waiterSignal; wire *web.Client }`, wantViolation: "cache/response mechanics"},
		{name: "local map aliases", source: `package fizeau; import ("net/http"; "sync"); type observationSet map[string]float64; type pendingSet map[string]chan struct{}; type accountState struct { lock sync.Mutex; observations observationSet; pending pendingSet; client http.RoundTripper }`, wantViolation: "cache/response mechanics"},
		{name: "sync map HTTP state", source: `package fizeau; import ("net/http"; "sync"); type accountState struct { observations sync.Map; pending sync.Map; client http.RoundTripper }`, wantViolation: "cache/response mechanics"},
		{name: "channel coordinator", source: `package fizeau; import ("net/http"; "sync"); type probeCoordinator struct{ mu sync.Mutex; inFlight map[string]chan struct{}; transport http.RoundTripper }`, wantViolation: "cache/response mechanics"},
		{name: "sync map balance cache", source: `package fizeau; import state "sync"; type balanceCache struct{ records state.Map }`, wantViolation: "cache/response mechanics"},
		{name: "HTTP response mechanics in other file", source: `package fizeau; import "net/http"; type creditProbe struct{response *http.Response}`, wantViolation: "renamed OpenRouter credit type creditProbe"},
		{name: "aliased HTTP response", source: `package fizeau; import web "net/http"; type probeResponse struct{response *web.Response}`, wantViolation: "cache/response mechanics"},
		{name: "renamed credits response", source: "package fizeau; type accountPayload struct { TotalCredits float64 `json:\"total_credits\"`; TotalUsage float64 `json:\"total_usage\"` }", wantViolation: "cache/response mechanics"},
		{name: "renamed HTTP probe", source: `package fizeau; import ("context"; "net/http"); func refreshAccount(ctx context.Context) { req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/credits", nil); req.Header.Set("Authorization", "Bearer key") }`, wantViolation: "HTTP-probe mechanics"},
		{name: "goroutine channel aliased HTTP probe", source: `package fizeau; import ("context"; web "net/http"); func refreshAccount(ctx context.Context) { done := make(chan struct{}); go func() { defer close(done); req, _ := web.NewRequestWithContext(ctx, web.MethodGet, "https://openrouter.ai/api/v1/credits", nil); req.Header.Set("Authorization", "Bearer key") }(); <-done }`, wantViolation: "HTTP-probe mechanics"},
		{name: "quota call alias", source: `package fizeau; import quota "github.com/easel/fizeau/internal/quota"; var projectOpenrouterCredits = quota.ProjectOpenRouterCredits`, wantViolation: "root OpenRouter quota references"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "service_openrouter_credit.go", mutation.source, 0)
			if err != nil {
				t.Fatalf("parse mutation: %v", err)
			}
			files := parseRootProductionFiles(t)
			files["mutation_"+strings.ReplaceAll(mutation.name, " ", "_")+".go"] = file
			violations := rootOpenRouterCreditOwnershipViolations(files)
			if !slices.ContainsFunc(violations, func(violation string) bool { return strings.Contains(violation, mutation.wantViolation) }) {
				t.Fatalf("ownership check did not report %q for mutation; violations=%v", mutation.wantViolation, violations)
			}
		})
	}
}

// TestRootCatalogPolicyMechanicsStayInternal locks catalog lookup, policy
// composition, and generic power math behind internal/serviceimpl. The root
// keeps one evaluator call and the two projections needed to preserve public
// error and request types.
func TestRootCatalogPolicyMechanicsStayInternal(t *testing.T) {
	for _, violation := range rootCatalogPolicyOwnershipViolations(parseRootProductionFiles(t)) {
		t.Error(violation)
	}
}

type rootCatalogPolicyOwnership struct {
	violations     []string
	evaluatorCalls map[string]int
	evaluatorRefs  map[string]int
	adapterDecls   map[string]int
	adapterCalls   map[string]int
	adapterRefs    map[string]int
}

func rootCatalogPolicyOwnershipViolations(files map[string]*ast.File) []string {
	result := rootCatalogPolicyOwnership{
		evaluatorCalls: make(map[string]int),
		evaluatorRefs:  make(map[string]int),
		adapterDecls:   make(map[string]int),
		adapterCalls:   make(map[string]int),
		adapterRefs:    make(map[string]int),
	}
	forbiddenServiceimplRefs := map[string]bool{
		"PolicyForName":                   true,
		"ProviderPreferenceForPolicyName": true,
	}
	forbiddenRoutehealthRefs := map[string]bool{
		"EffectivePowerPolicy":  true,
		"PowerBoundsForRequest": true,
	}
	adapters := map[string]bool{
		"publicCatalogPolicyError":                 true,
		"applyCatalogPolicyResultToRoutingRequest": true,
	}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		file := files[path]
		serviceimplBindings, invalidServiceimplBindings := importBindingsForPath(file, serviceimplImportPath, "serviceimpl")
		for _, binding := range invalidServiceimplBindings {
			result.violations = append(result.violations, fmt.Sprintf(
				"root %s imports %s with forbidden binding %q", path, serviceimplImportPath, binding,
			))
		}
		routehealthBindings, invalidRoutehealthBindings := importBindingsForPath(file, routehealthImportPath, "routehealth")
		for _, binding := range invalidRoutehealthBindings {
			result.violations = append(result.violations, fmt.Sprintf(
				"root %s imports %s with forbidden binding %q", path, routehealthImportPath, binding,
			))
		}

		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				result.recordCatalogPolicyPackageName(path, current.Name.Name)
				if adapters[current.Name.Name] && current.Recv == nil {
					result.adapterDecls[path+":"+current.Name.Name]++
				}
				result.scanCatalogPolicyNode(path, current.Name.Name, current.Body, serviceimplBindings, routehealthBindings, forbiddenServiceimplRefs, forbiddenRoutehealthRefs, adapters)
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						result.recordCatalogPolicyPackageName(path, named.Name.Name)
					case *ast.ValueSpec:
						for _, name := range named.Names {
							result.recordCatalogPolicyPackageName(path, name.Name)
						}
					}
				}
				result.scanCatalogPolicyNode(path, "package scope", current, serviceimplBindings, routehealthBindings, forbiddenServiceimplRefs, forbiddenRoutehealthRefs, adapters)
			}
		}
	}

	result.requireExactCatalogPolicySites(
		"serviceimpl.EvaluateCatalogPolicy direct calls",
		result.evaluatorCalls,
		map[string]int{"service_routing.go:ResolveRoute": 1},
	)
	result.requireExactCatalogPolicySites(
		"serviceimpl.EvaluateCatalogPolicy selector references",
		result.evaluatorRefs,
		map[string]int{"service_routing.go:ResolveRoute": 1},
	)
	result.requireExactCatalogPolicySites(
		"catalog-policy adapter declarations",
		result.adapterDecls,
		map[string]int{
			"service_routing.go:publicCatalogPolicyError":                 1,
			"service_routing.go:applyCatalogPolicyResultToRoutingRequest": 1,
		},
	)
	result.requireExactCatalogPolicySites(
		"catalog-policy adapter direct calls",
		result.adapterCalls,
		map[string]int{
			"service_routing.go:ResolveRoute:publicCatalogPolicyError":                 1,
			"service_routing.go:ResolveRoute:applyCatalogPolicyResultToRoutingRequest": 1,
		},
	)
	result.requireExactCatalogPolicySites(
		"catalog-policy adapter references",
		result.adapterRefs,
		map[string]int{
			"service_routing.go:ResolveRoute:publicCatalogPolicyError":                 1,
			"service_routing.go:ResolveRoute:applyCatalogPolicyResultToRoutingRequest": 1,
		},
	)
	return result.violations
}

func (result *rootCatalogPolicyOwnership) recordCatalogPolicyPackageName(path, name string) {
	if rootCatalogPolicyMechanicNames()[name] {
		result.violations = append(result.violations, fmt.Sprintf(
			"root %s declares obsolete package-scope catalog-policy mechanic %s", path, name,
		))
	}
}

func (result *rootCatalogPolicyOwnership) scanCatalogPolicyNode(
	path string,
	owner string,
	node ast.Node,
	serviceimplBindings map[string]bool,
	routehealthBindings map[string]bool,
	forbiddenServiceimplRefs map[string]bool,
	forbiddenRoutehealthRefs map[string]bool,
	adapters map[string]bool,
) {
	if node == nil {
		return
	}
	site := path + ":" + owner
	ast.Inspect(node, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.CallExpr:
			if selector, ok := current.Fun.(*ast.SelectorExpr); ok &&
				selectorUsesImport(selector, serviceimplBindings) && selector.Sel.Name == "EvaluateCatalogPolicy" {
				result.evaluatorCalls[site]++
			}
			if name, ok := current.Fun.(*ast.Ident); ok && packageFunctionReference(name, adapters) {
				result.adapterCalls[site+":"+name.Name]++
			}
		case *ast.SelectorExpr:
			switch {
			case selectorUsesImport(current, serviceimplBindings) && current.Sel.Name == "EvaluateCatalogPolicy":
				result.evaluatorRefs[site]++
			case forbiddenImportedSelector(current, serviceimplBindings, forbiddenServiceimplRefs):
				result.violations = append(result.violations, fmt.Sprintf(
					"root %s references obsolete serviceimpl.%s from %s", path, current.Sel.Name, owner,
				))
			case forbiddenImportedSelector(current, routehealthBindings, forbiddenRoutehealthRefs):
				result.violations = append(result.violations, fmt.Sprintf(
					"root %s references direct routehealth.%s power mechanic from %s", path, current.Sel.Name, owner,
				))
			}
		case *ast.Ident:
			if packageFunctionReference(current, adapters) {
				result.adapterRefs[site+":"+current.Name]++
			}
		}
		return true
	})
}

func packageFunctionReference(ident *ast.Ident, names map[string]bool) bool {
	if ident == nil || !names[ident.Name] || ident.Obj == nil || ident.Obj.Kind != ast.Fun {
		return false
	}
	decl, ok := ident.Obj.Decl.(*ast.FuncDecl)
	return ok && decl.Recv == nil && decl.Name.Name == ident.Name
}

func (result *rootCatalogPolicyOwnership) requireExactCatalogPolicySites(label string, got, want map[string]int) {
	if reflectIntMapEqual(got, want) {
		return
	}
	result.violations = append(result.violations, fmt.Sprintf("root %s = %v, want %v", label, got, want))
}

func rootCatalogPolicyMechanicNames() map[string]bool {
	return map[string]bool{
		"routingPolicyForName":            true,
		"providerPreferenceForPolicy":     true,
		"routePowerPolicyForRequest":      true,
		"routePowerBoundsForRequest":      true,
		"policyForName":                   true,
		"providerPreferenceForPolicyName": true,
	}
}

func TestRootCatalogPolicyOwnershipMutations(t *testing.T) {
	const canonical = `package fizeau
import (
	serviceimpl "github.com/easel/fizeau/internal/serviceimpl"
)
type service struct{}
func (*service) ResolveRoute() {
	_, failure := serviceimpl.EvaluateCatalogPolicy(nil, serviceimpl.CatalogPolicyRequest{})
	if failure != nil { _ = publicCatalogPolicyError(failure) }
	var request struct{}
	applyCatalogPolicyResultToRoutingRequest(&request, nil)
}
func publicCatalogPolicyError(any) error { return nil }
func applyCatalogPolicyResultToRoutingRequest(any, any) {}
`

	withImport := func(source, importLine string) string {
		return strings.Replace(source, "import (\n", "import (\n\t"+importLine+"\n", 1)
	}
	renameServiceimpl := func(source string) string {
		source = strings.Replace(source, `serviceimpl "github.com/easel/fizeau/internal/serviceimpl"`, `impl "github.com/easel/fizeau/internal/serviceimpl"`, 1)
		return strings.ReplaceAll(source, "serviceimpl.", "impl.")
	}

	tests := []struct {
		name            string
		source          string
		wantViolation   bool
		violationNeedle string
	}{
		{name: "canonical site", source: canonical},
		{name: "renamed serviceimpl import at canonical site", source: renameServiceimpl(canonical)},
		{
			name: "lexically shadowed serviceimpl qualifier",
			source: canonical + `
func shadowQualifier() {
	serviceimpl := struct{ EvaluateCatalogPolicy, PolicyForName int }{}
	_, _ = serviceimpl.EvaluateCatalogPolicy, serviceimpl.PolicyForName
}
`,
		},
		{
			name: "unrelated package selectors",
			source: withImport(canonical, `other "example.com/serviceimpl"`) + `
var _, _, _, _ = other.EvaluateCatalogPolicy, other.PolicyForName, other.EffectivePowerPolicy, other.PowerBoundsForRequest
`,
		},
		{
			name: "local obsolete names",
			source: canonical + `
func localObsoleteNames() {
	routingPolicyForName := func() {}
	providerPreferenceForPolicyName := routingPolicyForName
	routingPolicyForName()
	providerPreferenceForPolicyName()
}
`,
		},
		{
			name: "renamed wrapper evaluation site",
			source: strings.Replace(canonical,
				"serviceimpl.EvaluateCatalogPolicy(nil, serviceimpl.CatalogPolicyRequest{})",
				"evaluateCatalogPolicyForRoute()", 1,
			) + `
func evaluateCatalogPolicyForRoute() (any, any) {
	return serviceimpl.EvaluateCatalogPolicy(nil, serviceimpl.CatalogPolicyRequest{})
}
`,
			wantViolation:   true,
			violationNeedle: "EvaluateCatalogPolicy direct calls",
		},
		{
			name:            "package function alias",
			source:          canonical + "\nvar evaluateCatalogPolicy = serviceimpl.EvaluateCatalogPolicy\n",
			wantViolation:   true,
			violationNeedle: "EvaluateCatalogPolicy selector references",
		},
		{
			name: "local function alias",
			source: canonical + `
func localEvaluatorAlias() {
	evaluateCatalogPolicy := serviceimpl.EvaluateCatalogPolicy
	_ = evaluateCatalogPolicy
}
`,
			wantViolation:   true,
			violationNeedle: "EvaluateCatalogPolicy selector references",
		},
		{
			name: "second evaluator call",
			source: strings.Replace(canonical,
				"_, failure := serviceimpl.EvaluateCatalogPolicy(nil, serviceimpl.CatalogPolicyRequest{})",
				"_, failure := serviceimpl.EvaluateCatalogPolicy(nil, serviceimpl.CatalogPolicyRequest{})\n\t_, _ = serviceimpl.EvaluateCatalogPolicy(nil, serviceimpl.CatalogPolicyRequest{})", 1,
			),
			wantViolation:   true,
			violationNeedle: "EvaluateCatalogPolicy direct calls",
		},
		{
			name: "projection call moved behind wrapper",
			source: strings.Replace(canonical,
				"applyCatalogPolicyResultToRoutingRequest(&request, nil)",
				"projectCatalogPolicyResult(&request)", 1,
			) + `
func projectCatalogPolicyResult(request any) {
	applyCatalogPolicyResultToRoutingRequest(request, nil)
}
`,
			wantViolation:   true,
			violationNeedle: "catalog-policy adapter direct calls",
		},
		{
			name: "second projection call",
			source: strings.Replace(canonical,
				"applyCatalogPolicyResultToRoutingRequest(&request, nil)",
				"applyCatalogPolicyResultToRoutingRequest(&request, nil)\n\tapplyCatalogPolicyResultToRoutingRequest(&request, nil)", 1,
			),
			wantViolation:   true,
			violationNeedle: "catalog-policy adapter direct calls",
		},
		{
			name: "renamed routehealth direct call",
			source: withImport(canonical, `health "github.com/easel/fizeau/internal/routehealth"`) + `
func directPowerMath() { health.EffectivePowerPolicy(nil, nil) }
`,
			wantViolation:   true,
			violationNeedle: "direct routehealth.EffectivePowerPolicy",
		},
		{
			name: "renamed routehealth function alias",
			source: withImport(canonical, `health "github.com/easel/fizeau/internal/routehealth"`) + `
var powerBounds = health.PowerBoundsForRequest
`,
			wantViolation:   true,
			violationNeedle: "direct routehealth.PowerBoundsForRequest",
		},
		{
			name: "dot serviceimpl import",
			source: strings.Replace(canonical,
				`serviceimpl "github.com/easel/fizeau/internal/serviceimpl"`,
				`. "github.com/easel/fizeau/internal/serviceimpl"`, 1,
			),
			wantViolation:   true,
			violationNeedle: `forbidden binding "."`,
		},
		{
			name: "blank serviceimpl import",
			source: strings.Replace(canonical,
				`serviceimpl "github.com/easel/fizeau/internal/serviceimpl"`,
				`_ "github.com/easel/fizeau/internal/serviceimpl"`, 1,
			),
			wantViolation:   true,
			violationNeedle: `forbidden binding "_"`,
		},
		{
			name:            "dot routehealth import",
			source:          withImport(canonical, `. "github.com/easel/fizeau/internal/routehealth"`),
			wantViolation:   true,
			violationNeedle: `forbidden binding "."`,
		},
		{
			name:            "blank routehealth import",
			source:          withImport(canonical, `_ "github.com/easel/fizeau/internal/routehealth"`),
			wantViolation:   true,
			violationNeedle: `forbidden binding "_"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			violations := catalogPolicyMutationViolations(t, test.source)
			if !test.wantViolation {
				if len(violations) != 0 {
					t.Fatalf("unexpected ownership violations: %v", violations)
				}
				return
			}
			if len(violations) == 0 {
				t.Fatal("mutation passed catalog-policy ownership analysis")
			}
			if test.violationNeedle != "" && !strings.Contains(strings.Join(violations, "\n"), test.violationNeedle) {
				t.Fatalf("ownership violations %v do not contain %q", violations, test.violationNeedle)
			}
		})
	}

	for name := range rootCatalogPolicyMechanicNames() {
		name := name
		t.Run("obsolete package function/"+name, func(t *testing.T) {
			violations := catalogPolicyMutationViolations(t, canonical+"\nfunc "+name+"() {}\n")
			assertCatalogPolicyMutationRejected(t, violations, "obsolete package-scope catalog-policy mechanic "+name)
		})
		t.Run("obsolete package value alias/"+name, func(t *testing.T) {
			violations := catalogPolicyMutationViolations(t, canonical+"\nvar "+name+" = func() {}\n")
			assertCatalogPolicyMutationRejected(t, violations, "obsolete package-scope catalog-policy mechanic "+name)
		})
		t.Run("local function shadow allowed/"+name, func(t *testing.T) {
			source := canonical + "\nfunc local" + name + "() {\n\t" + name + " := func() {}\n\t" + name + "()\n}\n"
			if violations := catalogPolicyMutationViolations(t, source); len(violations) != 0 {
				t.Fatalf("local function-valued shadow produced ownership violations: %v", violations)
			}
		})
	}

	for _, protected := range []string{"PolicyForName", "ProviderPreferenceForPolicyName"} {
		protected := protected
		for _, importCase := range []struct {
			name      string
			qualifier string
			source    string
		}{
			{name: "canonical import", qualifier: "serviceimpl", source: canonical},
			{name: "renamed import", qualifier: "impl", source: renameServiceimpl(canonical)},
		} {
			importCase := importCase
			forms := []struct {
				name   string
				suffix string
			}{
				{
					name:   "direct call",
					suffix: "\nfunc forbiddenServiceimplCall() { " + importCase.qualifier + "." + protected + "() }\n",
				},
				{
					name:   "package reference",
					suffix: "\nvar _ = " + importCase.qualifier + "." + protected + "\n",
				},
				{
					name:   "local function-value alias",
					suffix: "\nfunc forbiddenServiceimplAlias() {\n\tlookup := " + importCase.qualifier + "." + protected + "\n\t_ = lookup\n}\n",
				},
			}
			for _, form := range forms {
				form := form
				t.Run("obsolete serviceimpl "+protected+"/"+importCase.name+"/"+form.name, func(t *testing.T) {
					violations := catalogPolicyMutationViolations(t, importCase.source+form.suffix)
					assertCatalogPolicyMutationRejected(t, violations, "obsolete serviceimpl."+protected)
				})
			}
		}
	}
}

func catalogPolicyMutationViolations(t *testing.T, source string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "service_routing.go", source, 0)
	if err != nil {
		t.Fatalf("parse mutation source: %v", err)
	}
	return rootCatalogPolicyOwnershipViolations(map[string]*ast.File{"service_routing.go": file})
}

func assertCatalogPolicyMutationRejected(t *testing.T, violations []string, needle string) {
	t.Helper()
	if len(violations) == 0 {
		t.Fatal("mutation passed catalog-policy ownership analysis")
	}
	if !strings.Contains(strings.Join(violations, "\n"), needle) {
		t.Fatalf("ownership violations %v do not contain %q", violations, needle)
	}
}

// TestResidualServiceImplMechanicsStayInternal provides one aggregate lock for
// the four residual ADR-008 serviceimpl extraction families. It checks only
// package-scope implementation names, leaving local names and the sanctioned
// public declarations/projection adapters alone.
func TestResidualServiceImplMechanicsStayInternal(t *testing.T) {
	type family struct {
		name  string
		names map[string]bool
	}
	families := []family{
		{name: "routing input", names: rootRoutingInputMechanicNames()},
		{name: "catalog cache", names: rootCatalogCacheMechanicNames()},
		{name: "harness capability", names: rootHarnessCapabilityMechanicNames()},
		{name: "catalog policy", names: rootCatalogPolicyMechanicNames()},
	}
	owners := make(map[string]string)
	for _, family := range families {
		for name := range family.names {
			if previous := owners[name]; previous != "" {
				t.Fatalf("residual mechanic %s appears in both %s and %s families", name, previous, family.name)
			}
			owners[name] = family.name
		}
	}

	wantAdapters := map[string]int{
		"service_capabilities.go:publicHarnessCapabilityMatrix":       1,
		"service_capabilities.go:publicHarnessCapability":             1,
		"service_routing.go:publicCatalogPolicyError":                 1,
		"service_routing.go:applyCatalogPolicyResultToRoutingRequest": 1,
	}
	wantPublicTypes := map[string]int{
		"service.go:ServiceProviderEntry":                 1,
		"service.go:RouteDecision":                        1,
		"service.go:RouteCandidate":                       1,
		"service_routing.go:ServiceConfigSource":          1,
		"service_catalog_cache.go:CatalogProbeFunc":       1,
		"service_catalog_cache.go:CatalogResult":          1,
		"service_capabilities.go:HarnessCapabilityStatus": 1,
		"service_capabilities.go:HarnessCapability":       1,
		"service_capabilities.go:HarnessCapabilityMatrix": 1,
	}
	gotAdapters := make(map[string]int)
	gotPublicTypes := make(map[string]int)

	for path, file := range parseRootProductionFiles(t) {
		for _, decl := range file.Decls {
			switch current := decl.(type) {
			case *ast.FuncDecl:
				if family := owners[current.Name.Name]; family != "" {
					t.Errorf("root %s declares residual %s mechanic %s", path, family, current.Name.Name)
				}
				key := path + ":" + current.Name.Name
				if _, allowed := wantAdapters[key]; allowed && current.Recv == nil {
					gotAdapters[key]++
				}
			case *ast.GenDecl:
				for _, spec := range current.Specs {
					switch named := spec.(type) {
					case *ast.TypeSpec:
						if family := owners[named.Name.Name]; family != "" {
							t.Errorf("root %s declares residual %s type %s", path, family, named.Name.Name)
						}
						key := path + ":" + named.Name.Name
						if _, allowed := wantPublicTypes[key]; allowed {
							gotPublicTypes[key]++
							if named.Assign.IsValid() {
								t.Errorf("sanctioned root public type %s is an alias", key)
							}
						}
					case *ast.ValueSpec:
						for _, name := range named.Names {
							if family := owners[name.Name]; family != "" {
								t.Errorf("root %s declares residual %s value/alias %s", path, family, name.Name)
							}
						}
					}
				}
			}
		}
	}
	if !reflectIntMapEqual(gotAdapters, wantAdapters) {
		t.Errorf("sanctioned root serviceimpl adapters = %v, want %v", gotAdapters, wantAdapters)
	}
	if !reflectIntMapEqual(gotPublicTypes, wantPublicTypes) {
		t.Errorf("sanctioned root public declarations = %v, want %v", gotPublicTypes, wantPublicTypes)
	}
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
	forbiddenDecls := rootCatalogCacheMechanicNames()
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

func rootCatalogCacheMechanicNames() map[string]bool {
	return map[string]bool{
		"catalogCacheOptions":       true,
		"catalogCacheKey":           true,
		"catalogCache":              true,
		"catalogCacheSnapshot":      true,
		"newCatalogCache":           true,
		"newCatalogCacheKey":        true,
		"catalogResultFromInternal": true,
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
	forbiddenDecls := rootHarnessCapabilityMechanicNames()
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

func rootHarnessCapabilityMechanicNames() map[string]bool {
	return map[string]bool{
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
