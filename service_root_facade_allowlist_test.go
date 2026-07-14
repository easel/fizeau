package fizeau

import (
	"go/ast"
	"os"
	"slices"
	"strings"
	"testing"
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
