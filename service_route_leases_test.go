package fizeau

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routing"
)

func seedSnapshotDiscoveryFixtures(t *testing.T, fixtures map[string][]string) {
	t.Helper()
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	cache := &discoverycache.Cache{Root: cacheDir}
	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)
	for source, models := range fixtures {
		writeSnapshotDiscoveryFixture(t, cache, source, capturedAt, models)
	}
}

func newStickyRouteService(t *testing.T, sc *fakeServiceConfig) *service {
	t.Helper()
	raw, err := New(ServiceOptions{
		ServiceConfig:       sc,
		QuotaRefreshContext: canceledRefreshContext(),
		AlivenessProber: func(context.Context, string, string) bool {
			return true
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc, ok := raw.(*service)
	if !ok {
		t.Fatalf("New returned %T, want *service", raw)
	}
	return svc
}

func seedStickyRouteLease(t *testing.T, state *routehealth.StickyState, now time.Time, stickyKey, provider, endpoint, model string) {
	t.Helper()
	if state == nil {
		t.Fatal("sticky state is nil")
	}
	decision := state.ApplyStickyLease(now, routehealth.StickyRequest{
		StickyKey:      stickyKey,
		Harness:        "fiz",
		Provider:       provider + "@" + endpoint,
		Endpoint:       endpoint,
		ServerInstance: endpoint,
		Model:          model,
	})
	if decision.Assignment != "acquired" {
		t.Fatalf("seed sticky assignment=%q, want acquired", decision.Assignment)
	}
}

func TestResolveRouteStickyLeaseReusesEndpoint(t *testing.T) {
	seedSnapshotDiscoveryFixtures(t, map[string][]string{
		testDiscoverySourceName("local", "desk-a", "http://desk-a.invalid/v1", "desk-a"): []string{"qwen/qwen3.6"},
		testDiscoverySourceName("local", "desk-b", "http://desk-b.invalid/v1", "desk-b"): []string{"qwen/qwen3.6"},
	})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1", ServerInstance: "desk-a"},
					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1", ServerInstance: "desk-b"},
				},
				Model: "qwen/qwen3.6",
			},
		},
		names:          []string{"local"},
		defaultName:    "local",
		healthCooldown: 20 * time.Millisecond,
	}
	svc := newStickyRouteService(t, sc)

	now := time.Now().UTC()
	// Make desk-b the initial winner.
	requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "local@desk-b",
		Endpoint:  "desk-b",
		Model:     "qwen/qwen3.6",
		Status:    "success",
		Duration:  10 * time.Millisecond,
		Timestamp: now,
	}))
	requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "local@desk-a",
		Endpoint:  "desk-a",
		Model:     "qwen/qwen3.6",
		Status:    "failed",
		Duration:  80 * time.Millisecond,
		Timestamp: now,
	}))

	first, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "qwen/qwen3.6",
		CorrelationID: "bead-sticky",
	})
	if err != nil {
		t.Fatalf("ResolveRoute first: %v", err)
	}
	firstServer := first.ServerInstance
	if firstServer == "" {
		t.Fatalf("first decision=%#v, want server instance", first)
	}
	if !first.Sticky.KeyPresent || first.Sticky.Assignment != "acquired" {
		t.Fatalf("first sticky evidence=%#v, want acquired sticky lease", first.Sticky)
	}
	firstStickyJSON, err := json.Marshal(first.Sticky)
	if err != nil {
		t.Fatalf("Marshal first sticky state: %v", err)
	}
	wantFirstStickyJSON := fmt.Sprintf(
		`{"key_present":true,"assignment":"acquired","server_instance":%q,"reason":"new sticky lease acquired","bonus":0}`,
		firstServer,
	)
	if string(firstStickyJSON) != wantFirstStickyJSON {
		t.Fatalf("first sticky JSON=%s, want %s", firstStickyJSON, wantFirstStickyJSON)
	}

	time.Sleep(30 * time.Millisecond)
	now = time.Now().UTC()
	// Reverse the live metrics so the baseline would now prefer desk-a.
	requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "local@desk-a",
		Endpoint:  "desk-a",
		Model:     "qwen/qwen3.6",
		Status:    "success",
		Duration:  10 * time.Millisecond,
		Timestamp: now,
	}))
	requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "local@desk-b",
		Endpoint:  "desk-b",
		Model:     "qwen/qwen3.6",
		Status:    "failed",
		Duration:  90 * time.Millisecond,
		Timestamp: now,
	}))

	second, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "qwen/qwen3.6",
		CorrelationID: "bead-sticky",
	})
	if err != nil {
		t.Fatalf("ResolveRoute second decision=%#v: %v", second, err)
	}
	if second.ServerInstance != firstServer {
		t.Fatalf("sticky decision=%#v, want reused server %q despite reversed baseline", second, firstServer)
	}
	if second.Sticky.Assignment != "reused" {
		t.Fatalf("second sticky evidence=%#v, want reused", second.Sticky)
	}
	if second.Sticky.Bonus <= 0 {
		t.Fatalf("second sticky evidence=%#v, want sticky bonus", second.Sticky)
	}
	if second.Sticky.Bonus != routing.StickyAffinityBonus {
		t.Fatalf("second sticky bonus=%v, want routing bonus %v", second.Sticky.Bonus, routing.StickyAffinityBonus)
	}
	secondStickyJSON, err := json.Marshal(second.Sticky)
	if err != nil {
		t.Fatalf("Marshal second sticky state: %v", err)
	}
	wantSecondStickyJSON := fmt.Sprintf(
		`{"key_present":true,"assignment":"reused","server_instance":%q,"reason":"live sticky lease reused","bonus":%v}`,
		firstServer,
		routing.StickyAffinityBonus,
	)
	if string(secondStickyJSON) != wantSecondStickyJSON {
		t.Fatalf("second sticky JSON=%s, want %s", secondStickyJSON, wantSecondStickyJSON)
	}
}

func TestResolveRouteStickyLeasePrefersSameServerAcrossModels(t *testing.T) {
	seedSnapshotDiscoveryFixtures(t, map[string][]string{
		testDiscoverySourceName("local", "desk-a", "http://desk-a.invalid/v1", "desk-a"): []string{"model-a", "model-b"},
		testDiscoverySourceName("local", "desk-b", "http://desk-b.invalid/v1", "desk-b"): []string{"model-a", "model-b"},
	})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1", ServerInstance: "desk-a"},
					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1", ServerInstance: "desk-b"},
				},
			},
		},
		names:          []string{"local"},
		defaultName:    "local",
		healthCooldown: 20 * time.Millisecond,
	}
	svc := newStickyRouteService(t, sc)
	svc.routeSticky.RecordUtilization("local", "desk-a", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(0),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	svc.routeSticky.RecordUtilization("local", "desk-b", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(0),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	svc.routeSticky.RecordUtilization("local", "desk-a", "model-b", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(0),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	svc.routeSticky.RecordUtilization("local", "desk-b", "model-b", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(0),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	now := time.Now().UTC()
	requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "local@desk-b",
		Endpoint:  "desk-b",
		Model:     "model-a",
		Status:    "success",
		Duration:  10 * time.Millisecond,
		Timestamp: now,
	}))
	requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "local@desk-a",
		Endpoint:  "desk-a",
		Model:     "model-a",
		Status:    "failed",
		Duration:  80 * time.Millisecond,
		Timestamp: now,
	}))

	first, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "model-a",
		CorrelationID: "bead-cross-model",
	})
	if err != nil {
		t.Fatalf("ResolveRoute first: %v", err)
	}
	if first.ServerInstance == "" {
		t.Fatalf("first decision=%#v, want server instance", first)
	}
	firstServer := first.ServerInstance
	time.Sleep(30 * time.Millisecond)
	now = time.Now().UTC()
	if firstServer == "desk-a" {
		requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
			Provider:  "local@desk-b",
			Endpoint:  "desk-b",
			Model:     "model-b",
			Status:    "success",
			Duration:  10 * time.Millisecond,
			Timestamp: now,
		}))
		requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
			Provider:  "local@desk-a",
			Endpoint:  "desk-a",
			Model:     "model-b",
			Status:    "failed",
			Duration:  90 * time.Millisecond,
			Timestamp: now,
		}))
		svc.routeSticky.RecordUtilization("local", "desk-a", "model-b", utilization.EndpointUtilization{
			ActiveRequests: utilization.Int(1),
			MaxConcurrency: utilization.Int(2),
			Source:         utilization.SourceLlamaSlots,
			Freshness:      utilization.FreshnessFresh,
		})
		svc.routeSticky.RecordUtilization("local", "desk-b", "model-b", utilization.EndpointUtilization{
			ActiveRequests: utilization.Int(0),
			MaxConcurrency: utilization.Int(2),
			Source:         utilization.SourceLlamaSlots,
			Freshness:      utilization.FreshnessFresh,
		})
	} else {
		requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
			Provider:  "local@desk-a",
			Endpoint:  "desk-a",
			Model:     "model-b",
			Status:    "success",
			Duration:  10 * time.Millisecond,
			Timestamp: now,
		}))
		requireNoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
			Provider:  "local@desk-b",
			Endpoint:  "desk-b",
			Model:     "model-b",
			Status:    "failed",
			Duration:  90 * time.Millisecond,
			Timestamp: now,
		}))
		svc.routeSticky.RecordUtilization("local", "desk-a", "model-b", utilization.EndpointUtilization{
			ActiveRequests: utilization.Int(0),
			MaxConcurrency: utilization.Int(2),
			Source:         utilization.SourceLlamaSlots,
			Freshness:      utilization.FreshnessFresh,
		})
		svc.routeSticky.RecordUtilization("local", "desk-b", "model-b", utilization.EndpointUtilization{
			ActiveRequests: utilization.Int(1),
			MaxConcurrency: utilization.Int(2),
			Source:         utilization.SourceLlamaSlots,
			Freshness:      utilization.FreshnessFresh,
		})
	}

	second, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "model-b",
		CorrelationID: "bead-cross-model",
	})
	if err != nil {
		t.Fatalf("ResolveRoute second decision=%#v: %v", second, err)
	}
	if second.ServerInstance != firstServer {
		t.Fatalf("second decision=%#v, want same server instance %q across model change", second, firstServer)
	}
	if second.Sticky.Assignment != "reused" {
		t.Fatalf("second sticky evidence=%#v, want reused sticky lease", second.Sticky)
	}
	if second.Sticky.Bonus <= 0 {
		t.Fatalf("second sticky evidence=%#v, want sticky bonus", second.Sticky)
	}
}

func TestResolveRouteStickyLeaseDistributesNewKeysByLoad(t *testing.T) {
	seedSnapshotDiscoveryFixtures(t, map[string][]string{
		testDiscoverySourceName("local", "desk-a", "http://desk-a.invalid/v1", "desk-a"): []string{"qwen/qwen3.6"},
		testDiscoverySourceName("local", "desk-b", "http://desk-b.invalid/v1", "desk-b"): []string{"qwen/qwen3.6"},
	})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1", ServerInstance: "desk-a"},
					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1", ServerInstance: "desk-b"},
				},
				Model: "qwen/qwen3.6",
			},
		},
		names:          []string{"local"},
		defaultName:    "local",
		healthCooldown: 20 * time.Millisecond,
	}
	svc := newStickyRouteService(t, sc)
	svc.routeSticky.RecordUtilization("local", "desk-a", "qwen/qwen3.6", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(1),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	svc.routeSticky.RecordUtilization("local", "desk-b", "qwen/qwen3.6", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(0),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})

	first, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "qwen/qwen3.6",
		CorrelationID: "bead-new-a",
	})
	if err != nil {
		t.Fatalf("ResolveRoute first: %v", err)
	}
	if first.Provider != "local@desk-b" || first.Endpoint != "desk-b" {
		t.Fatalf("first decision=%#v, want desk-b as the least-loaded endpoint", first)
	}
	if first.Utilization.Source != string(utilization.SourceLlamaSlots) {
		t.Fatalf("first utilization source=%q, want %q", first.Utilization.Source, utilization.SourceLlamaSlots)
	}
	if first.Utilization.Freshness != string(utilization.FreshnessFresh) {
		t.Fatalf("first utilization freshness=%q, want fresh", first.Utilization.Freshness)
	}

	second, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "qwen/qwen3.6",
		CorrelationID: "bead-new-b",
	})
	if err != nil {
		t.Fatalf("ResolveRoute second decision=%#v: %v", second, err)
	}
	if second.Provider != "local@desk-a" || second.Endpoint != "desk-a" {
		t.Fatalf("second decision=%#v, want desk-a after desk-b lease increased load", second)
	}
	if second.Utilization.Source != string(utilization.SourceLlamaSlots) {
		t.Fatalf("second utilization source=%q, want %q", second.Utilization.Source, utilization.SourceLlamaSlots)
	}
}

func TestResolveRouteStickyLeaseAvoidsSaturatedEndpointForNewKey(t *testing.T) {
	seedSnapshotDiscoveryFixtures(t, map[string][]string{
		testDiscoverySourceName("local", "desk-a", "http://desk-a.invalid/v1", "desk-a"): []string{"qwen/qwen3.6"},
		testDiscoverySourceName("local", "desk-b", "http://desk-b.invalid/v1", "desk-b"): []string{"qwen/qwen3.6"},
	})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1", ServerInstance: "desk-a"},
					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1", ServerInstance: "desk-b"},
				},
				Model: "qwen/qwen3.6",
			},
		},
		names:          []string{"local"},
		defaultName:    "local",
		healthCooldown: 20 * time.Millisecond,
	}
	svc := newStickyRouteService(t, sc)
	svc.routeSticky.RecordUtilization("local", "desk-a", "qwen/qwen3.6", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(1),
		MaxConcurrency: utilization.Int(1),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	seedStickyRouteLease(t, svc.routeSticky, time.Now().UTC(), "seed-b", "local", "desk-b", "qwen/qwen3.6")

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "qwen/qwen3.6",
		CorrelationID: "saturated-load-key",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec.Provider != "local@desk-b" || dec.Endpoint != "desk-b" {
		t.Fatalf("decision=%#v, want desk-b because desk-a is saturated", dec)
	}
}

func TestResolveRouteStickyLeaseIgnoresStaleUtilizationFallback(t *testing.T) {
	seedSnapshotDiscoveryFixtures(t, map[string][]string{
		testDiscoverySourceName("local", "desk-a", "http://desk-a.invalid/v1", "desk-a"): []string{"qwen/qwen3.6"},
		testDiscoverySourceName("local", "desk-b", "http://desk-b.invalid/v1", "desk-b"): []string{"qwen/qwen3.6"},
	})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "desk-a", BaseURL: "http://desk-a.invalid/v1", ServerInstance: "desk-a"},
					{Name: "desk-b", BaseURL: "http://desk-b.invalid/v1", ServerInstance: "desk-b"},
				},
				Model: "qwen/qwen3.6",
			},
		},
		names:          []string{"local"},
		defaultName:    "local",
		healthCooldown: 20 * time.Millisecond,
	}
	svc := newStickyRouteService(t, sc)
	// Stale utilization should be ignored in favor of the in-process lease
	// counts. Make desk-a look idle in stale telemetry but keep more leases
	// on desk-a so desk-b should still win.
	svc.routeSticky.RecordUtilization("local", "desk-a", "qwen/qwen3.6", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(0),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessStale,
	})
	seedAt := time.Now().UTC()
	seedStickyRouteLease(t, svc.routeSticky, seedAt, "seed-a", "local", "desk-a", "qwen/qwen3.6")
	seedStickyRouteLease(t, svc.routeSticky, seedAt, "seed-b", "local", "desk-a", "qwen/qwen3.6")
	seedStickyRouteLease(t, svc.routeSticky, seedAt, "seed-c", "local", "desk-b", "qwen/qwen3.6")

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "qwen/qwen3.6",
		CorrelationID: "stale-load-key",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec.Provider != "local@desk-b" || dec.Endpoint != "desk-b" {
		t.Fatalf("decision=%#v, want desk-b from lease-count fallback", dec)
	}
}
