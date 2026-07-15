package fizeau

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelsnapshot"
)

// TestServiceStartup_ProbesConfiguredProviders asserts that startupAlivenessProbe
// runs one TCP-connect probe per configured non-cloud provider within the
// startup-bounded time when invoked explicitly.
func TestServiceStartup_ProbesConfiguredProviders(t *testing.T) {
	var mu sync.Mutex
	var probed []string

	fakeProber := ProviderAlivenessProber(func(_ context.Context, provider, _ string) bool {
		mu.Lock()
		probed = append(probed, provider)
		mu.Unlock()
		return false
	})

	sc := &fakeServiceConfig{
		names: []string{"bragi", "openrouter"},
		providers: map[string]ServiceProviderEntry{
			"bragi": {
				// llama-server uses fixed (local) billing — should be probed.
				Type:    "llama-server",
				BaseURL: "http://bragi:1234",
			},
			"openrouter": {
				// openrouter uses per-token billing — should NOT be probed.
				Type:    "openrouter",
				BaseURL: "https://openrouter.ai/api/v1",
			},
		},
	}

	svc := newTestService(t, ServiceOptions{
		ServiceConfig:   sc,
		AlivenessProber: fakeProber,
	})
	resetProviderProbeForTest(svc)

	// New starts the background probe loop; test the synchronous diagnostic path
	// directly here.
	svc.startupAlivenessProbe(context.Background())

	mu.Lock()
	defer mu.Unlock()

	if len(probed) != 1 {
		t.Fatalf("expected 1 probe (bragi only, skip cloud openrouter), got %d: %v", len(probed), probed)
	}
	if probed[0] != "bragi" {
		t.Fatalf("expected probe for bragi, got %q", probed[0])
	}

	// Verify the result is recorded in the probe store.
	r, ok := svc.providerProbe.LastProbe("bragi", "default")
	if !ok {
		t.Fatal("no probe record for bragi in store")
	}
	if r.LastProbeSuccess {
		t.Error("expected probe failure recorded (prober returned false)")
	}
}

func TestNew_DoesNotBlockOnLocalAlivenessProbe(t *testing.T) {
	blocked := make(chan struct{})
	start := time.Now()
	rawSvc, err := New(ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"grendel": {
					Type:    "rapid-mlx",
					BaseURL: "http://grendel:8000/v1",
					Model:   "mlx-community/Qwen3.6-27B-8bit",
				},
			},
			names:       []string{"grendel"},
			defaultName: "grendel",
		},
		QuotaRefreshContext: canceledRefreshContext(),
		AlivenessProber: func(ctx context.Context, _, _ string) bool {
			select {
			case <-ctx.Done():
				return false
			case <-blocked:
				return true
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rawSvc == nil {
		t.Fatal("New returned nil service")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("New elapsed %v, want nonblocking local aliveness startup", elapsed)
	}
}

func TestServiceStartup_FailedProbeHardGatesRouting(t *testing.T) {
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	baseURL := "http://127.0.0.1:1/v1"
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("down", "down", baseURL, ""), time.Now().UTC(), []string{"qwen3.5-27b"})

	rawSvc, err := New(ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"down": {
					Type:                "lmstudio",
					BaseURL:             baseURL,
					Model:               "qwen3.5-27b",
					Billing:             BillingModelFixed,
					IncludeByDefault:    true,
					IncludeByDefaultSet: true,
				},
			},
			names:       []string{"down"},
			defaultName: "down",
		},
		QuotaRefreshContext: canceledRefreshContext(),
		AlivenessProber: func(context.Context, string, string) bool {
			return false
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := rawSvc.(*service)
	svc.startupAlivenessProbe(context.Background())
	inputs, _ := svc.routingInputs(context.Background(), serviceRoutingCatalog(), modelsnapshot.RefreshBackground)
	if _, ok := inputs.ProbeUnreachable["down"]; !ok {
		t.Fatalf("ProbeUnreachable missing down: %#v", inputs.ProbeUnreachable)
	}
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen3.5-27b"})
	if err == nil {
		t.Fatalf("ResolveRoute succeeded, decision=%#v", dec)
	}
	var sawDown bool
	for _, c := range dec.Candidates {
		if c.Provider != "down" {
			continue
		}
		sawDown = true
		if c.Eligible {
			t.Fatalf("down provider should be gated by failed startup probe: %#v", c)
		}
		if c.FilterReason != FilterReasonEndpointUnreachable {
			t.Fatalf("FilterReason=%q, want %q", c.FilterReason, FilterReasonEndpointUnreachable)
		}
	}
	if !sawDown {
		t.Fatalf("missing down provider candidate: %#v", dec.Candidates)
	}
}

func TestResolveRoute_UnknownLocalHealthDoesNotBlockSubscriptionFallback(t *testing.T) {
	configureFreshCodexQuotaForRouting(t)
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	baseURL := "http://grendel:8000/v1"
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("grendel", "grendel", baseURL, "grendel"), time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC), []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  gpt-5.5:
    family: gpt
    status: active
    power: 9
    context_window: 200000
    surfaces:
      codex: gpt-5.5
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	probeStarted := make(chan struct{}, 1)
	blocked := make(chan struct{})
	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"grendel": {
				Type:           "rapid-mlx",
				BaseURL:        baseURL,
				ServerInstance: "grendel",
				Model:          modelID,
			},
		},
		names:       []string{"grendel"},
		defaultName: "grendel",
	}, func(ctx context.Context, provider, gotBaseURL string) bool {
		if provider != "grendel" || gotBaseURL != baseURL {
			t.Errorf("probe target = %q %q, want grendel %q", provider, gotBaseURL, baseURL)
			return false
		}
		select {
		case probeStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return false
		case <-blocked:
			return true
		}
	})
	makeOnlyCodexSubprocessAvailable(svc)

	start := time.Now()
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "default"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("ResolveRoute elapsed %v, want nonblocking local aliveness path", elapsed)
	}
	if dec.Harness != "codex" || dec.Model != "gpt-5.5" {
		t.Fatalf("decision=%#v, want codex subscription fallback", dec)
	}
	var candidate *RouteCandidate
	for i := range dec.Candidates {
		if dec.Candidates[i].Provider == "grendel" {
			candidate = &dec.Candidates[i]
			break
		}
	}
	if candidate == nil {
		t.Fatalf("missing grendel candidate in %#v", dec.Candidates)
	}
	if !candidate.Eligible {
		t.Fatalf("grendel candidate=%#v, want eligible but demoted while health unknown", *candidate)
	}
	if candidate.Components.AvailabilityPenalty <= 0 {
		t.Fatalf("grendel availability penalty = %#v, want unknown-health demotion", candidate.Components)
	}
	select {
	case <-probeStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected asynchronous local aliveness refresh request")
	}
	close(blocked)
}

func TestExecute_RouteResolutionUsesCallerContextAndNonblockingLocalHealth(t *testing.T) {
	configureFreshCodexQuotaForRouting(t)
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	baseURL := "http://grendel:8000/v1"
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("grendel", "grendel", baseURL, "grendel"), time.Now().UTC(), []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  gpt-5.5:
    family: gpt
    status: active
    power: 9
    context_window: 200000
    surfaces:
      codex: gpt-5.5
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	blocked := make(chan struct{})
	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"grendel": {
				Type:           "rapid-mlx",
				BaseURL:        baseURL,
				ServerInstance: "grendel",
				Model:          modelID,
			},
		},
		names:       []string{"grendel"},
		defaultName: "grendel",
	}, func(ctx context.Context, _, _ string) bool {
		select {
		case <-ctx.Done():
			return false
		case <-blocked:
			return true
		}
	})
	makeOnlyCodexSubprocessAvailable(svc)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	dec, err := svc.resolveExecuteRouteContext(ctx, ServiceExecuteRequest{Policy: "default"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("resolveExecuteRouteContext: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("resolveExecuteRouteContext elapsed %v, want nonblocking local aliveness path", elapsed)
	}
	if dec == nil || dec.Harness != "codex" || dec.Model != "gpt-5.5" {
		t.Fatalf("decision=%#v, want codex subscription fallback", dec)
	}
	close(blocked)
}

func TestDefaultProviderFailsOverWhenUnreachable(t *testing.T) {
	configureFreshCodexQuotaForRouting(t)
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	baseURL := "http://grendel:8000/v1"
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("grendel", "grendel", baseURL, "grendel"), time.Now().UTC(), []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  gpt-5.5:
    family: gpt
    status: active
    power: 9
    context_window: 200000
    surfaces:
      codex: gpt-5.5
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"grendel": {
				Type:           "rapid-mlx",
				BaseURL:        baseURL,
				ServerInstance: "grendel",
				Model:          modelID,
			},
		},
		names:       []string{"grendel"},
		defaultName: "grendel",
	}, func(_ context.Context, provider, _ string) bool {
		t.Fatalf("route hot path invoked aliveness prober for %s", provider)
		return false
	})
	stubSubscriptionHarnessLookPath(svc, "codex")
	prevAutoRouting := subprocessHarnessAutoRoutingModels
	t.Cleanup(func() { subprocessHarnessAutoRoutingModels = prevAutoRouting })
	subprocessHarnessAutoRoutingModels = func(name string, _ harnesses.HarnessConfig) []string {
		if name == "codex" {
			return []string{"gpt-5.5"}
		}
		return nil
	}
	prevResolveAlias := resolveSubprocessModelAlias
	t.Cleanup(func() { resolveSubprocessModelAlias = prevResolveAlias })
	resolveSubprocessModelAlias = func(_, model string) string {
		return model
	}
	svc.providerProbe.RecordProbe("grendel", "", false, time.Now().UTC())

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "default"})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil || dec.Harness != "codex" || dec.Model != "gpt-5.5" {
		t.Fatalf("decision=%#v, want codex subscription fallback", dec)
	}
	var local *RouteCandidate
	for i := range dec.Candidates {
		if dec.Candidates[i].Provider == "grendel" {
			local = &dec.Candidates[i]
			break
		}
	}
	if local == nil {
		t.Fatalf("missing grendel candidate in %#v", dec.Candidates)
	}
	if local.Eligible {
		t.Fatalf("grendel candidate=%#v, want rejected by cached probe failure", *local)
	}
	if local.FilterReason != FilterReasonEndpointUnreachable {
		t.Fatalf("grendel FilterReason=%q, want %q", local.FilterReason, FilterReasonEndpointUnreachable)
	}
	if local.LastProbeAt.IsZero() || local.LastProbeSuccess {
		t.Fatalf("grendel probe evidence=%#v, want failed probe evidence", *local)
	}
}

func TestRouter_AllProvidersDead_FailsFast(t *testing.T) {
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	grendelURL := "http://grendel:8000/v1"
	vidarURL := "http://vidar:8001/v1"
	capturedAt := time.Now().UTC()
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("grendel", "grendel", grendelURL, "grendel"), capturedAt, []string{modelID})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("vidar", "vidar", vidarURL, "vidar"), capturedAt, []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"grendel": {
				Type:           "rapid-mlx",
				BaseURL:        grendelURL,
				ServerInstance: "grendel",
				Model:          modelID,
			},
			"vidar": {
				Type:           "rapid-mlx",
				BaseURL:        vidarURL,
				ServerInstance: "vidar",
				Model:          modelID,
			},
		},
		names:       []string{"grendel", "vidar"},
		defaultName: "grendel",
	}, func(_ context.Context, provider, _ string) bool {
		t.Fatalf("route hot path invoked aliveness prober for %s", provider)
		return false
	})
	svc.registry.LookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	now := time.Now().UTC()
	svc.providerProbe.RecordProbe("grendel", "", false, now)
	svc.providerProbe.RecordProbe("vidar", "", false, now)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dec, err := svc.ResolveRoute(ctx, RouteRequest{Policy: "default"})
	if err == nil {
		t.Fatalf("ResolveRoute succeeded, decision=%#v", dec)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ResolveRoute waited for the deadline instead of failing from cached all-dead evidence: %v", err)
	}
	if strings.Contains(err.Error(), "provider_connectivity") {
		t.Fatalf("error=%q, want no-live/no-viable route error, not provider_connectivity", err.Error())
	}
	var noLive *ErrNoLiveProvider
	if !errors.As(err, &noLive) {
		t.Fatalf("error=%T %v, want ErrNoLiveProvider", err, err)
	}
	for _, candidate := range dec.Candidates {
		if candidate.Provider != "grendel" && candidate.Provider != "vidar" {
			continue
		}
		if candidate.Eligible {
			t.Fatalf("candidate=%#v, want rejected by cached probe failure", candidate)
		}
		if candidate.FilterReason != FilterReasonEndpointUnreachable {
			t.Fatalf("candidate=%#v FilterReason=%q, want %q", candidate, candidate.FilterReason, FilterReasonEndpointUnreachable)
		}
	}
}

func TestResolveRoute_KnownFailedLocalProbeIsEvidence(t *testing.T) {
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	grendelURL := "http://grendel:8000/v1"
	vidarURL := "http://vidar:8001/v1"
	capturedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("grendel", "grendel", grendelURL, "grendel"), capturedAt, []string{modelID})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("vidar", "vidar", vidarURL, "vidar"), capturedAt, []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"grendel": {
				Type:           "rapid-mlx",
				BaseURL:        grendelURL,
				ServerInstance: "grendel",
				Model:          modelID,
			},
			"vidar": {
				Type:           "rapid-mlx",
				BaseURL:        vidarURL,
				ServerInstance: "vidar",
				Model:          modelID,
			},
		},
		names:       []string{"grendel", "vidar"},
		defaultName: "grendel",
	}, func(_ context.Context, provider, _ string) bool {
		t.Fatalf("route hot path invoked aliveness prober for %s", provider)
		return false
	})
	now := time.Now().UTC()
	svc.providerProbe.RecordProbe("grendel", "", false, now)
	svc.providerProbe.RecordProbe("vidar", "", true, now)

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: modelID})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if dec.Provider != "vidar" {
		t.Fatalf("decision=%#v, want vidar after cached grendel probe failure", dec)
	}
	var sawGrendel bool
	for _, candidate := range dec.Candidates {
		if candidate.Provider != "grendel" {
			continue
		}
		sawGrendel = true
		if candidate.Eligible {
			t.Fatalf("grendel candidate=%#v, want rejected after failed route-time probe", candidate)
		}
		if candidate.FilterReason != FilterReasonEndpointUnreachable {
			t.Fatalf("grendel FilterReason=%q, want %q", candidate.FilterReason, FilterReasonEndpointUnreachable)
		}
		if candidate.LastProbeAt.IsZero() || candidate.LastProbeSuccess {
			t.Fatalf("grendel probe evidence=%#v, want recorded failed probe evidence", candidate)
		}
	}
	if !sawGrendel {
		t.Fatalf("missing grendel candidate in %#v", dec.Candidates)
	}
}

func TestResolveRoute_FailedProbeGatesOnlyMatchingEndpoint(t *testing.T) {
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	deskAURL := "http://127.0.0.1:18001/v1"
	deskBURL := "http://127.0.0.1:18002/v1"
	capturedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("local", "desk-a", deskAURL, "desk-a"), capturedAt, []string{modelID})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("local", "desk-b", deskBURL, "desk-b"), capturedAt, []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "desk-a", BaseURL: deskAURL, ServerInstance: "desk-a"},
					{Name: "desk-b", BaseURL: deskBURL, ServerInstance: "desk-b"},
				},
				Model:               modelID,
				Billing:             BillingModelFixed,
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
		},
		names:       []string{"local"},
		defaultName: "local",
	}, func(_ context.Context, provider, _ string) bool {
		t.Fatalf("route hot path invoked aliveness prober for %s", provider)
		return false
	})
	now := time.Now().UTC()
	svc.providerProbe.RecordProbe("local", "desk-a", false, now)
	svc.providerProbe.RecordProbe("local", "desk-b", true, now)

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: modelID})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil || dec.Provider != "local@desk-b" {
		t.Fatalf("decision=%#v, want healthy endpoint local@desk-b", dec)
	}

	var sawDead, sawLive bool
	for _, candidate := range dec.Candidates {
		switch candidate.Provider {
		case "local@desk-a":
			sawDead = true
			if candidate.Eligible {
				t.Fatalf("dead endpoint candidate=%#v, want rejected", candidate)
			}
			if candidate.FilterReason != FilterReasonEndpointUnreachable {
				t.Fatalf("dead endpoint FilterReason=%q, want %q", candidate.FilterReason, FilterReasonEndpointUnreachable)
			}
		case "local@desk-b":
			sawLive = true
			if !candidate.Eligible {
				t.Fatalf("live endpoint candidate=%#v, want eligible", candidate)
			}
		}
	}
	if !sawDead || !sawLive {
		t.Fatalf("endpoint candidates missing: sawDead=%v sawLive=%v candidates=%#v", sawDead, sawLive, dec.Candidates)
	}
}

func TestResolveRoute_DoesNotProbeFreshHealthOnEveryRun(t *testing.T) {
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	modelID := "mlx-community/Qwen3.6-27B-8bit"
	baseURL := "http://grendel:8000/v1"
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("grendel", "grendel", baseURL, "grendel"), time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC), []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  mlx-community/Qwen3.6-27B-8bit:
    family: qwen
    status: active
    power: 7
    context_window: 32768
    surfaces:
      embedded-openai: mlx-community/Qwen3.6-27B-8bit
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"grendel": {
				Type:           "rapid-mlx",
				BaseURL:        baseURL,
				ServerInstance: "grendel",
				Model:          modelID,
			},
		},
		names:       []string{"grendel"},
		defaultName: "grendel",
	}, func(_ context.Context, _, _ string) bool {
		t.Fatal("route hot path invoked aliveness prober despite fresh cached health")
		return false
	})
	svc.providerProbe.RecordProbe("grendel", "", true, time.Now().UTC())

	first, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: modelID})
	if err != nil {
		t.Fatalf("first ResolveRoute: %v", err)
	}
	second, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: modelID})
	if err != nil {
		t.Fatalf("second ResolveRoute: %v", err)
	}
	if first == nil || second == nil {
		t.Fatalf("decisions=%#v %#v, want non-nil decisions", first, second)
	}
}

func newResolveRouteProbeTestService(t *testing.T, sc *fakeServiceConfig, prober ProviderAlivenessProber) *service {
	t.Helper()
	svc := newTestService(t, ServiceOptions{
		ServiceConfig:       sc,
		AlivenessProber:     prober,
		HealthProbeInterval: time.Hour,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	resetProviderProbeForTest(svc)
	return svc
}

func configureFreshCodexQuotaForRouting(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codex-quota.json")
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", path)
	t.Setenv("FIZEAU_CODEX_AUTH", filepath.Join(dir, "missing-auth.json"))
	writeCodexQuotaCacheFile(t, path, time.Now().UTC(), "pty",
		&harnesses.AccountInfo{PlanType: "ChatGPT Pro"},
		[]harnesses.QuotaWindow{{
			Name:        "5h",
			LimitID:     "codex",
			UsedPercent: 10,
			State:       "ok",
		}},
	)
}

func makeOnlyCodexSubprocessAvailable(svc *service) {
	svc.registry.LookPath = func(binary string) (string, error) {
		if binary == "codex" {
			return "/usr/bin/codex", nil
		}
		return "", exec.ErrNotFound
	}
}
