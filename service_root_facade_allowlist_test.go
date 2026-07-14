package fizeau

import (
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
		"service_progress.go",
		"service_projection.go",
		"service_providers.go",
		"service_reasoning.go",
		"service_route_attempts.go",
		"service_route_leases.go",
		"service_routestatus.go",
		"service_routing.go",
		"service_routing_quality.go",
		"service_session_log.go",
		"service_session_projection.go",
		"service_snapshot.go",
		"service_stale_harness_reaper.go",
		"service_status.go",
		"service_subscription_quota.go",
		"service_taillog.go",
		"testseam_types.go",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("root source allowlist mismatch\nwant: %v\ngot:  %v", want, got)
	}
}
