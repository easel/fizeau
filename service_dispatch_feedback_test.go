package fizeau

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/routehealth"
)

// TestRoutePicksAlternateAfterDispatchFailureFeedback verifies AC3 from
// fizeau-e8f12982: after a dispatch failure for endpoint A is fed back into
// the service via recordDispatchFailure (the bug fix path), the next routing
// pass within the same service instance hard-gates A as
// endpoint_unreachable and selects equivalent endpoint B instead.
//
// Pre-fix, ddx-on-project-A's chat-completions timeout against bragi:8020
// never reached the cache or probe store; ddx-on-project-A's next routing
// pass within FreshTTL returned `source_status: "available"` for bragi and
// burned another 5s i/o timeout. This test pins the contract that closes
// that loop.
func TestRoutePicksAlternateAfterDispatchFailureFeedback(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	cache := &discoverycache.Cache{Root: cacheDir}
	capturedAt := time.Now().UTC().Add(-30 * time.Second)
	aBaseURL := "http://aaa-down.example/v1"
	bBaseURL := "http://zzz-up.example/v1"
	aSource := testDiscoverySourceName("aaa-down", "aaa-down", aBaseURL, "down-1")
	bSource := testDiscoverySourceName("zzz-up", "zzz-up", bBaseURL, "up-1")
	writeSnapshotDiscoveryFixture(t, cache, aSource, capturedAt, []string{"shared-model"})
	writeSnapshotDiscoveryFixture(t, cache, bSource, capturedAt, []string{"shared-model"})

	// Mark both discovery fixtures fresh so neither attempts a background
	// refresh that could mask the routing signal under test.
	for _, src := range []string{aSource, bSource} {
		_ = os.Chtimes(filepath.Join(cacheDir, "discovery", src+".json"), time.Now(), time.Now())
	}

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-12T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  shared-model:
    family: shared
    status: active
    power: 6
    context_window: 16384
    surfaces:
      embedded-openai: shared-model
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"aaa-down": {
				Type:           "lmstudio",
				BaseURL:        aBaseURL,
				ServerInstance: "down-1",
				Model:          "shared-model",
				Billing:        BillingModelFixed,
			},
			"zzz-up": {
				Type:           "lmstudio",
				BaseURL:        bBaseURL,
				ServerInstance: "up-1",
				Model:          "shared-model",
				Billing:        BillingModelFixed,
			},
		},
		names:       []string{"aaa-down", "zzz-up"},
		defaultName: "aaa-down",
	}
	svc := newTestService(t, ServiceOptions{
		ServiceConfig:       sc,
		QuotaRefreshContext: canceledRefreshContext(),
		AlivenessProber: func(context.Context, string, string) bool {
			t.Fatal("dispatch-feedback test must not invoke live aliveness probes")
			return false
		},
	})
	svc.providerProbe = routehealth.NewProbeStore()
	svc.catalog = newCatalogCache(catalogCacheOptions{})

	// Seed both endpoints as alive so the first routing pass treats them
	// equally — mirrors the start-of-day state where /v1/models probes
	// have just succeeded.
	now := time.Now().UTC().Add(-5 * time.Second)
	svc.providerProbe.RecordProbe("aaa-down", "", true, now)
	svc.providerProbe.RecordProbe("zzz-up", "", true, now)

	// Sticky leases are deterministic by CorrelationID, but here both
	// endpoints win in alphabetic / no-correlation tie-breaking. We don't
	// assert which one wins first — we assert that AFTER feedback, A is
	// eliminated and B is selected.

	// Simulate a chat-completions dispatch failure against aaa-down with
	// the canonical bragi:8020 i/o-timeout error shape.
	dispatchErr := errors.New(`Post "http://aaa-down.example/v1/chat/completions": dial tcp 10.0.0.1:443: i/o timeout`)
	svc.recordDispatchFailure("aaa-down", "", dispatchErr)

	// Catalog cache: the feedback hook must have stamped UnreachableAt on
	// the cache key derived from the provider's baseURL/apiKey/headers.
	cacheKey := newCatalogCacheKey(aBaseURL, "", nil)
	svc.catalog.mu.Lock()
	entry, ok := svc.catalog.mem[cacheKey]
	svc.catalog.mu.Unlock()
	if !ok {
		t.Fatal("catalog cache missing entry for aaa-down after dispatch failure")
	}
	if entry.UnreachableAt.IsZero() {
		t.Fatal("catalog cache UnreachableAt not stamped after dispatch failure feedback")
	}

	// Routing pass: aaa-down is now gated as endpoint_unreachable; zzz-up wins.
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	if err != nil {
		t.Fatalf("ResolveRoute after dispatch failure: %v", err)
	}
	if dec.Provider != "zzz-up" {
		t.Fatalf("provider after dispatch failure = %q, want zzz-up", dec.Provider)
	}
	var aCandidate *RouteCandidate
	for i := range dec.Candidates {
		if dec.Candidates[i].Provider == "aaa-down" {
			aCandidate = &dec.Candidates[i]
			break
		}
	}
	if aCandidate == nil {
		t.Fatal("aaa-down candidate missing from routing decision")
	}
	if aCandidate.Eligible {
		t.Fatalf("aaa-down still eligible after dispatch failure: %#v", *aCandidate)
	}
	if aCandidate.FilterReason != FilterReasonEndpointUnreachable {
		t.Fatalf("aaa-down filter_reason = %q, want %q",
			aCandidate.FilterReason, FilterReasonEndpointUnreachable)
	}
}
