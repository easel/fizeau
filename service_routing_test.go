package fizeau

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routing"
	"gopkg.in/yaml.v3"
)

func TestRouteCandidateFromInternalMapsFields(t *testing.T) {
	candidate := routing.Candidate{
		Harness:            "fiz",
		Provider:           "local",
		Endpoint:           "primary",
		Model:              "model-a",
		Score:              42.5,
		CostUSDPer1kTokens: 0.012,
		CostSource:         routing.CostSourceCatalog,
		Power:              7,
		ContextLength:      200000,
		ContextSource:      routing.ContextSourceCatalog,
		ContextHeadroom:    150000,
		Eligible:           true,
		Reason:             "profile=cheap; score=42.5",
		LatencyMS:          123,
		SpeedTPS:           55,
		SuccessRate:        0.8,
		CostClass:          "local",
		QuotaOK:            true,
		QuotaPercentUsed:   25,
		QuotaTrend:         routing.QuotaTrendHealthy,
		StickyAffinity:     10,
		ScoreComponents: map[string]float64{
			"base":                100,
			"cost":                -4,
			"deployment_locality": 12,
			"quota_health":        6,
			"utilization":         -3,
			"performance":         9,
			"power":               18,
			"context_headroom":    0.15,
			"sticky_affinity":     10,
		},
	}

	got := routeCandidateFromInternal(candidate, RoutePowerPolicy{MinPower: 6, MaxPower: 8})
	if got.Harness != candidate.Harness ||
		got.Provider != candidate.Provider ||
		got.Endpoint != candidate.Endpoint ||
		got.Model != candidate.Model ||
		got.Score != candidate.Score ||
		got.CostUSDPer1kTokens != candidate.CostUSDPer1kTokens ||
		got.CostSource != candidate.CostSource ||
		got.Eligible != candidate.Eligible {
		t.Fatalf("routeCandidateFromInternal()=%#v, want fields from %#v", got, candidate)
	}
	if got.Reason != candidate.Reason {
		t.Fatalf("eligible Reason=%q, want %q", got.Reason, candidate.Reason)
	}
	if got.Components.Power != 7 || got.Components.SpeedTPS != 55 || got.Components.QuotaPercentUsed != 25 || got.Components.QuotaTrend != routing.QuotaTrendHealthy {
		t.Fatalf("components=%#v, want power/speed/quota inputs from candidate", got.Components)
	}
	if got.Components.Utilization != 0 {
		t.Fatalf("components utilization=%v, want zero when unavailable", got.Components.Utilization)
	}
	if got.Components.StickyAffinity != 10 {
		t.Fatalf("components sticky affinity=%v, want 10 from sticky match", got.Components.StickyAffinity)
	}
	if got.Components.PowerWeightedCapability != 18 {
		t.Fatalf("components power_weighted_capability=%v, want 18", got.Components.PowerWeightedCapability)
	}
	if got.Components.PowerHintFit != 0 {
		t.Fatalf("components power_hint_fit=%v, want 0 within bounds", got.Components.PowerHintFit)
	}
	if got.Components.LatencyWeight != 9 {
		t.Fatalf("components latency_weight=%v, want 9", got.Components.LatencyWeight)
	}
	if got.Components.PlacementBonus != 12+10 {
		t.Fatalf("components placement_bonus=%v, want 22", got.Components.PlacementBonus)
	}
	if got.Components.QuotaBonus != 6 {
		t.Fatalf("components quota_bonus=%v, want 6", got.Components.QuotaBonus)
	}
	if got.Components.MarginalCostPenalty != 4 {
		t.Fatalf("components marginal_cost_penalty=%v, want 4", got.Components.MarginalCostPenalty)
	}
	if got.Components.AvailabilityPenalty != 3 {
		t.Fatalf("components availability_penalty=%v, want 3", got.Components.AvailabilityPenalty)
	}
	if got.Components.StaleSignalPenalty != 0 {
		t.Fatalf("components stale_signal_penalty=%v, want 0", got.Components.StaleSignalPenalty)
	}
	if got.ContextLength != candidate.ContextLength || got.ContextSource != candidate.ContextSource {
		t.Fatalf("context evidence=%d/%q, want %d/%q", got.ContextLength, got.ContextSource, candidate.ContextLength, candidate.ContextSource)
	}
	if got.Components.ContextHeadroom != candidate.ContextHeadroom {
		t.Fatalf("context headroom=%d, want %d", got.Components.ContextHeadroom, candidate.ContextHeadroom)
	}

	rejected := candidate
	rejected.Eligible = false
	rejected.Reason = "model not in harness allow-list"
	got = routeCandidateFromInternal(rejected, RoutePowerPolicy{})
	if got.Reason != rejected.Reason {
		t.Fatalf("rejected Reason=%q, want %q", got.Reason, rejected.Reason)
	}
}

func TestRouteDecisionCandidatesExposeRawScoreComponents(t *testing.T) {
	rawComponents := map[string]float64{
		"base":                100,
		"power":               18,
		"cost":                -4,
		"quota_health":        6,
		"deployment_locality": 12,
		"utilization":         -3,
		"context_headroom":    0.15,
		"performance":         9,
	}
	got := routeCandidateFromInternal(routing.Candidate{
		Harness:            "fiz",
		Provider:           "local",
		Model:              "model-a",
		Score:              138.15,
		Eligible:           true,
		Power:              7,
		CostUSDPer1kTokens: 0.012,
		ScoreComponents:    rawComponents,
	}, RoutePowerPolicy{})

	if len(got.ScoreComponents) != len(rawComponents) {
		t.Fatalf("len(ScoreComponents)=%d, want %d: %#v", len(got.ScoreComponents), len(rawComponents), got.ScoreComponents)
	}
	for key, want := range rawComponents {
		if got.ScoreComponents[key] != want {
			t.Fatalf("ScoreComponents[%q]=%v, want %v in %#v", key, got.ScoreComponents[key], want, got.ScoreComponents)
		}
	}
	if got.Components.Power != 7 || got.Components.Cost != 0.012 {
		t.Fatalf("aggregate Components=%#v, want preserved alongside raw ScoreComponents", got.Components)
	}
	rawComponents["base"] = -999
	if got.ScoreComponents["base"] != 100 {
		t.Fatalf("ScoreComponents aliases internal map; base=%v, want copied 100", got.ScoreComponents["base"])
	}
}

func TestRouteCandidateFromInternalUsesExclusionStrengthForAboveMaxPower(t *testing.T) {
	candidate := routing.Candidate{
		Harness:      "codex",
		Provider:     "frontier",
		Model:        "frontier-10",
		Power:        10,
		Eligible:     false,
		Reason:       "model power 10 exceeds max_power=8 while an in-bounds candidate exists",
		FilterReason: routing.FilterReasonAboveMaxPower,
		ScoreComponents: map[string]float64{
			"power": -1002,
		},
	}

	got := routeCandidateFromInternal(candidate, RoutePowerPolicy{MaxPower: 8})
	if got.FilterReason != FilterReasonAboveMaxPower {
		t.Fatalf("filter reason=%q, want %q", got.FilterReason, FilterReasonAboveMaxPower)
	}
	if got.Components.PowerHintFit != -1002 {
		t.Fatalf("power_hint_fit=%v, want exclusion-strength power evidence", got.Components.PowerHintFit)
	}
	if got.Components.PowerWeightedCapability != 0 {
		t.Fatalf("power_weighted_capability=%v, want 0 once max-power exclusion consumes the power signal", got.Components.PowerWeightedCapability)
	}
}

func TestResolveRouteSuccessIncludesCandidates(t *testing.T) {
	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", ServerInstance: "127.0.0.1:9999", Model: "model-a"},
		},
		names:       []string{"local"},
		defaultName: "local",
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:               "fiz",
		Model:                 "model-a",
		EstimatedPromptTokens: 1_000,
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if dec.Harness != "fiz" || dec.Provider != "local" || dec.Model != "model-a" {
		t.Fatalf("decision=%#v, want fiz/local/model-a", dec)
	}
	if dec.ServerInstance == "" {
		t.Fatalf("decision=%#v, want server_instance", dec)
	}
	if len(dec.Candidates) != 1 {
		t.Fatalf("Candidates length=%d, want 1: %#v", len(dec.Candidates), dec.Candidates)
	}
	candidate := dec.Candidates[0]
	if !candidate.Eligible || candidate.Harness != "fiz" || candidate.Provider != "local" || candidate.Model != "model-a" {
		t.Fatalf("candidate=%#v, want eligible fiz/local/model-a", candidate)
	}
	if candidate.ServerInstance == "" {
		t.Fatalf("candidate=%#v, want server_instance", candidate)
	}
	if candidate.ContextLength == 0 || candidate.ContextSource != ContextSourceDefault {
		t.Fatalf("candidate context = %d/%q, want default context evidence", candidate.ContextLength, candidate.ContextSource)
	}
	if candidate.Components.ContextHeadroom == 0 {
		t.Fatalf("candidate context headroom should be populated for eligible candidates: %#v", candidate.Components)
	}
	if !strings.Contains(candidate.Reason, "score=") {
		t.Fatalf("eligible candidate Reason=%q, want scoring reason", candidate.Reason)
	}
}

func TestResolveRouteSnapshotProviderPowerCorrelation(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	var probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeHits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	cache := &discoverycache.Cache{Root: cacheDir}
	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("alpha", "alpha", srv.URL+"/v1", "alpha-1"), capturedAt, []string{"shared-model", "medium-model"})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("beta", "beta", srv.URL+"/v1", "beta-1"), capturedAt, []string{"shared-model", "high-model"})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("gamma", "gamma", srv.URL+"/v1", "gamma-1"), capturedAt, []string{"catalog-only-model"})

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
  medium-model:
    family: tier
    status: active
    power: 5
    context_window: 8192
    surfaces:
      embedded-openai: medium-model
  high-model:
    family: tier
    status: active
    power: 10
    context_window: 32768
    surfaces:
      embedded-openai: high-model
  catalog-only-model:
    family: tier
    status: active
    power: 8
    exact_pin_only: true
    context_window: 4096
    surfaces:
      embedded-openai: catalog-only-model
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {
				Type:           "openai",
				BaseURL:        srv.URL + "/v1",
				APIKey:         "alpha-key",
				ServerInstance: "alpha-1",
				Model:          "medium-model",
			},
			"beta": {
				Type:           "openai",
				BaseURL:        srv.URL + "/v1",
				APIKey:         "beta-key",
				ServerInstance: "beta-1",
				Model:          "high-model",
			},
			"gamma": {
				Type:                "openai",
				BaseURL:             srv.URL + "/v1",
				APIKey:              "gamma-key",
				ServerInstance:      "gamma-1",
				Model:               "catalog-only-model",
				IncludeByDefault:    false,
				IncludeByDefaultSet: true,
			},
		},
		names:       []string{"alpha", "beta", "gamma"},
		defaultName: "alpha",
	}

	newSvc := func(t *testing.T) *service {
		t.Helper()
		return newTestService(t, ServiceOptions{ServiceConfig: sc})
	}

	t.Run("power", func(t *testing.T) {
		svc := newSvc(t)
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if probeHits.Load() != 0 {
			t.Fatalf("unexpected discovery probe count = %d", probeHits.Load())
		}
		if dec == nil {
			t.Fatal("ResolveRoute returned nil decision")
		}
		if dec.Provider != "beta" || dec.Model != "high-model" {
			t.Fatalf("decision=%#v, want snapshot-backed high-model winner", dec)
		}
	})

	t.Run("provider pin", func(t *testing.T) {
		svc := newSvc(t)
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
			Provider: "alpha",
			Model:    "medium-model",
		})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec == nil {
			t.Fatal("ResolveRoute returned nil decision")
		}
		if dec.Provider != "alpha" || dec.Model != "medium-model" {
			t.Fatalf("decision=%#v, want hard-pinned alpha/medium-model", dec)
		}
		if probeHits.Load() != 0 {
			t.Fatalf("unexpected discovery probe count = %d", probeHits.Load())
		}
	})

	t.Run("exact model pin", func(t *testing.T) {
		svc := newSvc(t)
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
			Model: "catalog-only-model",
		})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec == nil {
			t.Fatal("ResolveRoute returned nil decision")
		}
		if dec.Provider != "gamma" || dec.Model != "catalog-only-model" {
			t.Fatalf("decision=%#v, want exact-pinned gamma/catalog-only-model", dec)
		}
		if probeHits.Load() != 0 {
			t.Fatalf("unexpected discovery probe count = %d", probeHits.Load())
		}
	})

	t.Run("correlation", func(t *testing.T) {
		svc := newSvc(t)
		first, err := svc.ResolveRoute(context.Background(), RouteRequest{
			Model:         "shared-model",
			CorrelationID: "snapshot-sticky",
		})
		if err != nil {
			t.Fatalf("first ResolveRoute: %v", err)
		}
		second, err := svc.ResolveRoute(context.Background(), RouteRequest{
			Model:         "shared-model",
			CorrelationID: "snapshot-sticky",
		})
		if err != nil {
			t.Fatalf("second ResolveRoute: %v", err)
		}
		if first == nil || second == nil {
			t.Fatalf("decisions=%#v %#v, want non-nil", first, second)
		}
		if first.ServerInstance == "" || second.ServerInstance == "" {
			t.Fatalf("server instances=%q %q, want sticky selection", first.ServerInstance, second.ServerInstance)
		}
		if first.ServerInstance != second.ServerInstance {
			t.Fatalf("sticky server instance changed: first=%q second=%q", first.ServerInstance, second.ServerInstance)
		}
		if probeHits.Load() != 0 {
			t.Fatalf("unexpected discovery probe count = %d", probeHits.Load())
		}
	})

	if probeHits.Load() != 0 {
		t.Fatalf("unexpected discovery probe count = %d", probeHits.Load())
	}
}

func TestResolveRouteRefreshesStaleSnapshotInBackgroundWithoutBlocking(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on free port: %v", err)
	}
	downBaseURL := "http://" + ln.Addr().String() + "/v1"
	if err := ln.Close(); err != nil {
		t.Fatalf("close free-port listener: %v", err)
	}

	cache := &discoverycache.Cache{Root: cacheDir}
	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)
	downSource := testDiscoverySourceName("aaa-down", "aaa-down", downBaseURL, "down-1")
	upSource := testDiscoverySourceName("zzz-up", "zzz-up", "http://up.example/v1", "up-1")
	writeSnapshotDiscoveryFixture(t, cache, downSource, capturedAt, []string{"shared-model"})
	writeSnapshotDiscoveryFixture(t, cache, upSource, capturedAt, []string{"shared-model"})

	stalePath := filepath.Join(cacheDir, "discovery", downSource+".json")
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stalePath, past, past); err != nil {
		t.Fatalf("mark stale discovery fixture: %v", err)
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
				BaseURL:        downBaseURL,
				ServerInstance: "down-1",
				Model:          "shared-model",
			},
			"zzz-up": {
				Type:           "lmstudio",
				BaseURL:        "http://up.example/v1",
				ServerInstance: "up-1",
				Model:          "shared-model",
			},
		},
		names:       []string{"aaa-down", "zzz-up"},
		defaultName: "aaa-down",
	}
	svc := newTestService(t, ServiceOptions{
		ServiceConfig:       sc,
		QuotaRefreshContext: canceledRefreshContext(),
		AlivenessProber: func(context.Context, string, string) bool {
			t.Fatal("route hot path invoked aliveness prober")
			return false
		},
	})
	svc.providerProbe = routehealth.NewProbeStore()
	now := time.Now().UTC()
	svc.providerProbe.RecordProbe("aaa-down", "", false, now)
	svc.providerProbe.RecordProbe("zzz-up", "", true, now)

	start := time.Now()
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("ResolveRoute elapsed %v, want stale snapshot background refresh path", elapsed)
	}
	if dec.Provider != "zzz-up" {
		t.Fatalf("provider = %q, want zzz-up after cached aaa-down probe failure is hard-gated", dec.Provider)
	}
	var downCandidate *RouteCandidate
	for i := range dec.Candidates {
		if dec.Candidates[i].Provider == "aaa-down" {
			downCandidate = &dec.Candidates[i]
			break
		}
	}
	if downCandidate == nil {
		t.Fatal("aaa-down candidate missing")
	}
	if downCandidate.Eligible {
		t.Fatalf("aaa-down candidate eligible after refresh failure: %#v", *downCandidate)
	}
	if downCandidate.FilterReason != FilterReasonEndpointUnreachable {
		t.Fatalf("aaa-down filter reason = %q, want %q", downCandidate.FilterReason, FilterReasonEndpointUnreachable)
	}
}

func TestServiceRouteSnapshotCatalogOnlyModelRejected(t *testing.T) {
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
  gpt-5.5:
    family: gpt
    status: active
    power: 10
    surfaces:
      embedded-openai: gpt-5.5
  catalog-only-model:
    family: test
    status: active
    power: 5
    exact_pin_only: true
    surfaces:
      embedded-openai: catalog-only-model
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"known":        {Type: "test", BaseURL: "http://known.invalid/v1", Model: "gpt-5.5"},
			"catalog-only": {Type: "test", BaseURL: "http://pin.invalid/v1", Model: "catalog-only-model"},
		},
		names:       []string{"known", "catalog-only"},
		defaultName: "known",
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if dec.Model != "gpt-5.5" {
		t.Fatalf("decision=%#v, want gpt-5.5 winner from snapshot-backed eligibility", dec)
	}
	var sawCatalogOnly bool
	for _, candidate := range dec.Candidates {
		if candidate.Provider != "catalog-only" {
			continue
		}
		sawCatalogOnly = true
		if candidate.Eligible {
			t.Fatalf("catalog-only candidate should be rejected by snapshot-backed eligibility: %#v", candidate)
		}
		if candidate.FilterReason != string(routing.FilterReasonExactPinOnly) {
			t.Fatalf("catalog-only FilterReason=%q, want %q", candidate.FilterReason, routing.FilterReasonExactPinOnly)
		}
	}
	if !sawCatalogOnly {
		t.Fatalf("missing catalog-only candidate in %#v", dec.Candidates)
	}
}

func TestServiceRouteHardPinBypassesSnapshotEligibility(t *testing.T) {
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
  gpt-5.5:
    family: gpt
    status: active
    power: 10
    surfaces:
      embedded-openai: gpt-5.5
  catalog-only-model:
    family: test
    status: active
    power: 5
    exact_pin_only: true
    surfaces:
      embedded-openai: catalog-only-model
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := publicRouteTraceService(&fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"known":        {Type: "test", BaseURL: "http://known.invalid/v1", Model: "gpt-5.5"},
			"catalog-only": {Type: "test", BaseURL: "http://pin.invalid/v1", Model: "catalog-only-model"},
		},
		names:       []string{"known", "catalog-only"},
		defaultName: "known",
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Provider: "catalog-only",
		Model:    "catalog-only-model",
	})
	if err != nil {
		t.Fatalf("ResolveRoute hard pin: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute hard pin returned nil decision")
	}
	if dec.Provider != "catalog-only" || dec.Model != "catalog-only-model" {
		t.Fatalf("decision=%#v, want hard-pinned catalog-only/model", dec)
	}
}

func TestServiceTranslatesPolicyAirGappedToRequireNoRemote(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-08T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 5
    max_power: 8
    allow_local: true
  cheap:
    min_power: 5
    max_power: 5
    allow_local: true
  smart:
    min_power: 9
    max_power: 10
    allow_local: false
  air-gapped:
    min_power: 5
    max_power: 5
    allow_local: true
    require: [no_remote]
models:
  remote-model:
    family: example
    status: active
    provider_system: openrouter
    deployment_class: managed_cloud_frontier
    power: 5
    surfaces:
      agent.openai: remote-model
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newTestService(t, ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"openrouter": {Type: "openrouter", BaseURL: "http://remote.invalid/v1", Model: "remote-model"},
			},
			names:       []string{"openrouter"},
			defaultName: "openrouter",
		},
	})

	_, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Policy:   "air-gapped",
		Provider: "openrouter",
	})
	if err == nil {
		t.Fatal("expected air-gapped policy to reject remote provider pin")
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "air-gapped" || typed.Requirement != "no_remote" || typed.AttemptedPin != "openrouter" {
		t.Fatalf("ErrPolicyRequirementUnsatisfied=%#v, want air-gapped/no_remote/openrouter", typed)
	}
}

func TestResolveRoutePolicyReportsEffectivePowerPolicy(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-06T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 7
    max_power: 8
    allow_local: true
  smart:
    min_power: 9
    max_power: 10
    allow_local: false
models:
  provider-default:
    family: example
    status: active
    power: 7
    surfaces:
      agent.openai: provider-default
  catalog-smart:
    family: example
    status: active
    power: 9
    surfaces:
      agent.openai: catalog-smart
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newTestService(t, ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", Model: "provider-default"},
			},
			names:       []string{"local"},
			defaultName: "local",
		},
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "default"})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if dec.RequestedPolicy != "default" {
		t.Fatalf("RequestedPolicy=%q, want default", dec.RequestedPolicy)
	}
	if dec.PowerPolicy.PolicyName != "default" || dec.PowerPolicy.MinPower != 7 || dec.PowerPolicy.MaxPower != 8 {
		t.Fatalf("PowerPolicy=%#v, want default 7..8", dec.PowerPolicy)
	}
	if dec.Model != "provider-default" {
		t.Fatalf("Model=%q, want provider-default without treating policy as a model ref", dec.Model)
	}
}

func TestResolveRoutePolicyAppliesEffectivePowerPolicyBeforeFiltering(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-06T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 7
    max_power: 8
    allow_local: true
models:
  power-5:
    family: example
    status: active
    power: 5
    surfaces:
      agent.openai: power-5
  power-7:
    family: example
    status: active
    power: 7
    surfaces:
      agent.openai: power-7
  power-9:
    family: example
    status: active
    power: 9
    surfaces:
      agent.openai: power-9
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newTestService(t, ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"power-5": {Type: "test", BaseURL: "http://127.0.0.1:1111/v1", Model: "power-5"},
				"power-7": {Type: "test", BaseURL: "http://127.0.0.1:2222/v1", Model: "power-7"},
				"power-9": {Type: "test", BaseURL: "http://127.0.0.1:3333/v1", Model: "power-9"},
			},
			names:       []string{"power-5", "power-7", "power-9"},
			defaultName: "power-7",
		},
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Policy: "default",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if dec.Model != "power-7" {
		t.Fatalf("decision=%#v, want power-7 winner", dec)
	}
	if dec.RequestedPolicy != "default" {
		t.Fatalf("RequestedPolicy=%q, want default", dec.RequestedPolicy)
	}
	if dec.PowerPolicy.PolicyName != "default" || dec.PowerPolicy.MinPower != 7 || dec.PowerPolicy.MaxPower != 8 {
		t.Fatalf("PowerPolicy=%#v, want default 7..8", dec.PowerPolicy)
	}

	var sawBelowTarget, sawAboveTarget bool
	for _, candidate := range dec.Candidates {
		switch candidate.Model {
		case "power-5":
			if !candidate.Eligible {
				t.Fatalf("power-5 candidate should remain eligible under soft power scoring: %#v", candidate)
			}
			if candidate.FilterReason != "" {
				t.Fatalf("power-5 FilterReason=%q, want empty under soft power scoring", candidate.FilterReason)
			}
			sawBelowTarget = true
		case "power-7":
			if !candidate.Eligible {
				t.Fatalf("power-7 candidate should remain eligible under default: %#v", candidate)
			}
		case "power-9":
			if candidate.Eligible {
				t.Fatalf("power-9 candidate should be excluded once an in-bounds max_power route exists: %#v", candidate)
			}
			if candidate.FilterReason != FilterReasonAboveMaxPower {
				t.Fatalf("power-9 FilterReason=%q, want %q", candidate.FilterReason, FilterReasonAboveMaxPower)
			}
			sawAboveTarget = true
		}
	}
	if !sawBelowTarget || !sawAboveTarget {
		t.Fatalf("decision candidates did not cover the full power-policy trace: %#v", dec.Candidates)
	}
}

func TestProviderUsesLiveDiscovery_LlamaServer(t *testing.T) {
	if !providerUsesLiveDiscovery("llama-server") {
		t.Fatal("expected llama-server to use live discovery")
	}
}

func TestProviderTypeUsesFixedBilling_LlamaServer(t *testing.T) {
	if !providerTypeUsesFixedBilling("llama-server") {
		t.Fatal("expected llama-server to count as a fixed-billing endpoint")
	}
}

func TestResolveRouteErrorIncludesCandidatesAndTraceError(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "redacted")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("GOOGLE_GENAI_USE_GCA", "")
	t.Setenv("GEMINI_CLI_USE_COMPUTE_ADC", "")
	t.Setenv("CLOUD_SHELL", "")

	registry := harnesses.NewRegistry()
	registry.LookPath = func(file string) (string, error) {
		if file == "gemini" {
			return "/usr/bin/gemini", nil
		}
		return "", os.ErrNotExist
	}
	svc := &service{
		opts:     ServiceOptions{},
		registry: registry,
		hub:      newSessionHub(),
	}

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model: "minimax/minimax-m2.7",
	})
	if err == nil {
		t.Fatal("ResolveRoute expected no viable candidate error")
	}
	if dec == nil {
		t.Fatal("ResolveRoute error path returned nil decision")
	}
	if dec.Harness != "" || dec.Provider != "" || dec.Model != "" {
		t.Fatalf("error decision selected a candidate: %#v", dec)
	}
	if len(dec.Candidates) == 0 {
		t.Fatal("error decision Candidates is empty")
	}

	var noMatch *ErrModelConstraintNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("errors.As no-match: %T %v", err, err)
	}
	var traced DecisionWithCandidates
	if !errors.As(err, &traced) {
		t.Fatalf("errors.As DecisionWithCandidates: %T %v", err, err)
	}
	tracedCandidates := traced.RouteCandidates()
	if len(tracedCandidates) != len(dec.Candidates) {
		t.Fatalf("traced candidates length=%d, decision candidates length=%d", len(tracedCandidates), len(dec.Candidates))
	}
	tracedCandidates[0].Reason = "mutated"
	if dec.Candidates[0].Reason == "mutated" {
		t.Fatal("RouteCandidates returned an alias of the decision candidates")
	}

	if !strings.Contains(err.Error(), "no matching model") {
		t.Fatalf("error=%q, want no matching model detail", err.Error())
	}
}

func TestRoutingInputsUseClaudeQuotaWindows(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", cachePath)

	writeClaudeQuotaCacheFile(t, cachePath, claudeTestQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: 90,
		FiveHourLimit:     100,
		WeeklyRemaining:   90,
		WeeklyLimit:       100,
		Source:            "pty",
		Account:           &harnesses.AccountInfo{PlanType: "Claude Max"},
		Windows: []harnesses.QuotaWindow{
			{Name: "extra", LimitID: "claude-extra", UsedPercent: 100, State: "exhausted"},
		},
	})

	registry := harnesses.NewRegistry()
	registry.LookPath = func(file string) (string, error) {
		if file == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	svc := &service{opts: ServiceOptions{}, registry: registry}

	inputs, _ := svc.buildRoutingInputsWithCatalog(context.Background(), nil, modelsnapshot.RefreshBackground)
	claudeEntry, ok := findRoutingHarnessEntry(inputs.Harnesses, "claude")
	if !ok {
		t.Fatalf("missing claude entry in %#v", inputs.Harnesses)
	}
	if claudeEntry.QuotaOK {
		t.Fatalf("Claude QuotaOK=true, want false for exhausted window: %#v", claudeEntry)
	}
	if claudeEntry.SubscriptionOK {
		t.Fatalf("Claude SubscriptionOK=true, want false for exhausted window: %#v", claudeEntry)
	}
	if claudeEntry.QuotaPercentUsed != 100 || claudeEntry.QuotaTrend != routing.QuotaTrendExhausting {
		t.Fatalf("Claude quota components=%d/%q, want 100/%q", claudeEntry.QuotaPercentUsed, claudeEntry.QuotaTrend, routing.QuotaTrendExhausting)
	}
	if !strings.Contains(claudeEntry.QuotaReason, "exhausted claude-extra") {
		t.Fatalf("Claude QuotaReason=%q, want exhausted claude-extra detail", claudeEntry.QuotaReason)
	}
}

func findRoutingHarnessEntry(entries []routing.HarnessEntry, name string) (routing.HarnessEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return routing.HarnessEntry{}, false
}

func TestResolveRouteSnapshotFreshCacheSkipsDiscoveryProbe(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	var probeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := &discoverycache.Cache{Root: cacheDir}
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("alpha", "alpha", srv.URL+"/v1", "alpha-1"), time.Date(2026, 5, 12, 15, 5, 0, 0, time.UTC), []string{"model-a"})

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
  model-a:
    family: test
    status: active
    power: 7
    context_window: 8192
    surfaces:
      embedded-openai: model-a
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {Type: "openai", BaseURL: srv.URL + "/v1", APIKey: "alpha-key", ServerInstance: "alpha-1", Model: "model-a"},
		},
		names:       []string{"alpha"},
		defaultName: "alpha",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if dec.Model != "model-a" || dec.Provider != "alpha" {
		t.Fatalf("decision=%#v, want snapshot-backed alpha/model-a", dec)
	}
	if probeHits.Load() != 0 {
		t.Fatalf("unexpected discovery probe count = %d", probeHits.Load())
	}
}

func TestBuildRoutingInputsPopulatesEndpointProviderCostsFromCatalog(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-04-22T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  alpha-provider-model:
    family: same
    status: active
    cost_input_per_m: 1
    cost_output_per_m: 3
    surfaces: {agent.openai: alpha-provider-model}
  beta-provider-model:
    family: same
    status: active
    cost_input_per_m: 2
    cost_output_per_m: 4
    surfaces: {agent.openai: beta-provider-model}
  gamma-provider-model:
    family: same
    status: active
    cost_input_per_m: 4
    cost_output_per_m: 8
    surfaces: {agent.openai: gamma-provider-model}
`)
	restore := replaceRoutingCatalogForTest(t, catalog)
	defer restore()

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {Type: "openai", BaseURL: "http://alpha.invalid/v1", Model: "alpha-provider-model"},
			"beta":  {Type: "openai", BaseURL: "http://beta.invalid/v1", Model: "beta-provider-model"},
			"gamma": {Type: "openai", BaseURL: "http://gamma.invalid/v1", Model: "gamma-provider-model"},
		},
		names:       []string{"alpha", "beta", "gamma"},
		defaultName: "alpha",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	in := svc.buildRoutingInputs(context.Background())

	got := providerCostsByName(in, "fiz")
	assertProviderCost(t, got, "alpha", 0.002, routing.CostSourceCatalog)
	assertProviderCost(t, got, "beta", 0.003, routing.CostSourceCatalog)
	assertProviderCost(t, got, "gamma", 0.006, routing.CostSourceCatalog)
}

func TestSubscriptionEffectiveCostUsesCatalogShadowCost(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-04-22T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  priced-model:
    family: priced
    status: active
    cost_input_per_m: 10
    cost_output_per_m: 10
    surfaces:
      agent.openai: priced-model
      codex: priced-model
      claude-code: priced-model
      gemini: priced-model
`)
	svc := &service{}
	tests := []struct {
		name string
		want float64
	}{
		{name: "free", want: 0.01},
		{name: "low", want: 0.01},
		{name: "medium", want: 0.01},
		{name: "high", want: 0.01},
	}
	for _, harness := range []string{"claude", "codex", "gemini"} {
		for _, tt := range tests {
			t.Run(harness+"/"+tt.name, func(t *testing.T) {
				entry := routing.HarnessEntry{
					Name:             harness,
					IsSubscription:   true,
					DefaultModel:     "priced-model",
					QuotaPercentUsed: 92,
				}
				svc.applySubscriptionRoutingCost(&entry, catalog)
				if len(entry.Providers) != 1 {
					t.Fatalf("providers=%#v, want one subscription provider", entry.Providers)
				}
				got := entry.Providers[0]
				if got.CostUSDPer1kTokens != tt.want || got.CostSource != routing.CostSourceSubscription || got.ActualCashSpend {
					t.Fatalf("effective cost=(%v,%q), want (%v,%q)", got.CostUSDPer1kTokens, got.CostSource, tt.want, routing.CostSourceSubscription)
				}
			})
		}
	}
}

func TestBuildRoutingInputsHonorsLocalCostOption(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "lmstudio", BaseURL: "http://127.0.0.1:1234/v1", Model: "local-model"},
		},
		names:       []string{"local"},
		defaultName: "local",
	}

	unsetSvc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	unset := providerCostsByName(unsetSvc.buildRoutingInputs(context.Background()), "fiz")
	assertProviderCost(t, unset, "local", 0, routing.CostSourceUnknown)

	setSvc := &service{
		opts:     ServiceOptions{ServiceConfig: sc, LocalCostUSDPer1kTokens: 0.0042},
		registry: harnesses.NewRegistry(),
	}
	set := providerCostsByName(setSvc.buildRoutingInputs(context.Background()), "fiz")
	assertProviderCost(t, set, "local", 0.0042, routing.CostSourceUserConfig)
}

// TestSubscriptionRoutingCostPopulatesPerTierCatalogCost asserts that
// applySubscriptionRoutingCost projects per-model catalog cost onto the
// provider entry so the routing engine can attach the correct cost to each
// per-tier candidate emitted under one subscription auth (fizeau-8ebac59d).
func TestSubscriptionRoutingCostPopulatesPerTierCatalogCost(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-16T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  sonnet-4.6:
    family: claude-sonnet
    status: active
    power: 8
    cost_input_per_m: 3.0
    cost_output_per_m: 15.0
    surfaces:
      claude-code: sonnet-4.6
  claude-opus-4.7:
    family: claude-opus
    status: active
    power: 10
    cost_input_per_m: 15.0
    cost_output_per_m: 75.0
    surfaces:
      claude-code: opus-4.7
`)
	svc := &service{}
	entry := routing.HarnessEntry{
		Name:              "claude",
		Surface:           "claude",
		IsSubscription:    true,
		DefaultModel:      "opus-4.7",
		AutoRoutingModels: []string{"opus-4.7", "sonnet-4.6"},
	}
	svc.applySubscriptionRoutingCost(&entry, catalog)
	if len(entry.Providers) != 1 {
		t.Fatalf("providers=%d, want 1 subscription provider", len(entry.Providers))
	}
	costByModel := entry.Providers[0].CostUSDPer1kTokensByModel
	if costByModel == nil {
		t.Fatal("CostUSDPer1kTokensByModel is nil; want catalog-derived per-tier costs")
	}
	// opus-4.7: (15 + 75) / 2 / 1000 = 0.045
	if got := costByModel["opus-4.7"]; got != 0.045 {
		t.Errorf("opus-4.7 cost=%v, want 0.045 (catalog blended)", got)
	}
	// sonnet-4.6: (3 + 15) / 2 / 1000 = 0.009
	if got := costByModel["sonnet-4.6"]; got != 0.009 {
		t.Errorf("sonnet-4.6 cost=%v, want 0.009 (catalog blended)", got)
	}
}

func TestResolveRouteNearQuotaClaudeDemotesBelowOpenRouter(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-04-22T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  sonnet-4.6:
    family: claude-sonnet
    status: active
    cost_input_per_m: 3
    cost_output_per_m: 15
    surfaces:
      agent.openai: sonnet-4.6
      claude-code: sonnet-4.6
`)
	svc := &service{}

	claude := routing.HarnessEntry{
		Name:                "claude",
		Surface:             "claude",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		Available:           true,
		QuotaOK:             true,
		QuotaPercentUsed:    92,
		QuotaTrend:          routing.QuotaTrendExhausting,
		SubscriptionOK:      true,
		DefaultModel:        "sonnet-4.6",
		ExactPinSupport:     true,
		SupportsTools:       true,
	}
	svc.applySubscriptionRoutingCost(&claude, catalog)

	openrouterProvider := routing.ProviderEntry{
		Name:          "openrouter",
		BaseURL:       "https://openrouter.ai/api/v1",
		DefaultModel:  "sonnet-4.6",
		SupportsTools: true,
	}
	svc.applyEndpointRoutingCost(&openrouterProvider, ServiceProviderEntry{
		Type:    "openrouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "sonnet-4.6",
	}, catalog)

	in := routing.Inputs{
		Harnesses: []routing.HarnessEntry{
			claude,
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "medium",
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				ExactPinSupport:     true,
				SupportsTools:       true,
				Providers:           []routing.ProviderEntry{openrouterProvider},
			},
		},
		ObservedSpeedTPS: map[string]float64{
			// Neutralize Claude's near-quota score penalty and keep both
			// candidates on identical performance evidence so the final base
			// scores tie and the cost tiebreak is the deciding dimension.
			routing.ProviderModelKey("", "", "sonnet-4.6"):           1900,
			routing.ProviderModelKey("openrouter", "", "sonnet-4.6"): 1900,
		},
	}
	dec, err := routing.Resolve(routing.Request{
		Policy:             "default",
		Model:              "sonnet-4.6",
		ProviderPreference: routing.ProviderPreferenceSubscriptionFirst,
	}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "fiz" || dec.Provider != "openrouter" {
		t.Fatalf("near-quota route selected harness=%q provider=%q, want fiz/openrouter", dec.Harness, dec.Provider)
	}
	var claudeCandidate, openrouterCandidate routing.Candidate
	for _, candidate := range dec.Candidates {
		switch {
		case candidate.Harness == "claude":
			claudeCandidate = candidate
		case candidate.Harness == "fiz" && candidate.Provider == "openrouter":
			openrouterCandidate = candidate
		}
	}
	if claudeCandidate.Harness == "" || openrouterCandidate.Harness == "" {
		t.Fatalf("expected claude and openrouter candidates in trace: %#v", dec.Candidates)
	}
	if claudeCandidate.Score >= openrouterCandidate.Score {
		t.Fatalf("openrouter should outrank near-quota Claude before any cost tiebreak: claude=%.1f openrouter=%.1f", claudeCandidate.Score, openrouterCandidate.Score)
	}
	if claudeCandidate.CostSource != routing.CostSourceSubscription || !floatNear(claudeCandidate.CostUSDPer1kTokens, 0.009) {
		t.Fatalf("claude cost metadata=%#v, want catalog shadow cost 0.009", claudeCandidate)
	}
	if openrouterCandidate.CostSource != routing.CostSourceCatalog || !floatNear(openrouterCandidate.CostUSDPer1kTokens, 0.009) {
		t.Fatalf("openrouter cost metadata=%#v, want catalog cost 0.009", openrouterCandidate)
	}
	if openrouterCandidate.CostUSDPer1kTokens > claudeCandidate.CostUSDPer1kTokens {
		t.Fatalf("openrouter cost %.4f should not exceed claude %.4f", openrouterCandidate.CostUSDPer1kTokens, claudeCandidate.CostUSDPer1kTokens)
	}
}

func publicRouteTraceService(sc ServiceConfig) *service {
	return &service{
		opts:     ServiceOptions{ServiceConfig: sc},
		registry: harnesses.NewRegistry(),
		hub:      newSessionHub(),
	}
}

func forceAvailableHarnessesForTest(t testing.TB, svc *service, names ...string) {
	t.Helper()
	if svc == nil || svc.registry == nil {
		t.Fatal("service registry is nil")
	}
	available := make(map[string]string, len(names))
	for _, name := range names {
		cfg, ok := svc.registry.Get(name)
		if !ok {
			t.Fatalf("missing harness registry entry %q", name)
		}
		if cfg.Binary == "" {
			continue
		}
		available[cfg.Binary] = "/test/bin/" + cfg.Binary
	}
	svc.registry.LookPath = func(file string) (string, error) {
		if path, ok := available[file]; ok {
			return path, nil
		}
		return "", errors.New("binary not found")
	}
}

func loadRoutingFixtureCatalog(t *testing.T, contents string) *modelcatalog.Catalog {
	t.Helper()
	contents = normalizeRoutingFixtureManifest(t, contents)
	path := filepath.Join(t.TempDir(), "models.yaml")
	requireNoError(t, os.WriteFile(path, []byte(contents), 0o600))
	catalog, err := modelcatalog.Load(modelcatalog.LoadOptions{ManifestPath: path, RequireExternal: true})
	if err != nil {
		t.Fatalf("Load fixture catalog: %v", err)
	}
	return catalog
}

func normalizeRoutingFixtureManifest(t *testing.T, contents string) string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(contents), &doc); err != nil {
		t.Fatalf("parse fixture manifest: %v", err)
	}
	if version, _ := intFromYAML(doc["version"]); version == 5 {
		return contents
	}
	doc["version"] = 5

	if _, ok := doc["policies"]; !ok {
		policies := make(map[string]any)
		if profiles, ok := doc["profiles"].(map[string]any); ok {
			for name, raw := range profiles {
				entry, _ := raw.(map[string]any)
				minPower, ok := intFromYAML(entry["min_power"])
				if !ok || minPower <= 0 {
					minPower = 1
				}
				maxPower, ok := intFromYAML(entry["max_power"])
				if !ok || maxPower <= 0 {
					maxPower = 10
				}
				if _, exists := policies[name]; !exists {
					policies[name] = map[string]any{
						"min_power":   minPower,
						"max_power":   maxPower,
						"allow_local": true,
					}
				}
			}
		}
		if _, ok := policies["default"]; !ok {
			policies["default"] = map[string]any{
				"min_power":   1,
				"max_power":   10,
				"allow_local": true,
			}
		}
		doc["policies"] = policies
	}
	delete(doc, "profiles")
	delete(doc, "targets")

	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture manifest: %v", err)
	}
	return string(out)
}

func intFromYAML(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func replaceRoutingCatalogForTest(t *testing.T, catalog *modelcatalog.Catalog) func() {
	t.Helper()
	old := loadRoutingCatalog
	loadRoutingCatalog = func() (*modelcatalog.Catalog, error) {
		return catalog, nil
	}
	return func() { loadRoutingCatalog = old }
}

func providerCostsByName(in routing.Inputs, harness string) map[string]routing.ProviderEntry {
	out := make(map[string]routing.ProviderEntry)
	for _, h := range in.Harnesses {
		if h.Name != harness {
			continue
		}
		for _, p := range h.Providers {
			out[p.Name] = p
		}
	}
	return out
}

func assertProviderCost(t *testing.T, providers map[string]routing.ProviderEntry, name string, wantCost float64, wantSource string) {
	t.Helper()
	provider, ok := providers[name]
	if !ok {
		t.Fatalf("provider %q not found in %#v", name, providers)
	}
	if provider.CostUSDPer1kTokens != wantCost || provider.CostSource != wantSource {
		t.Fatalf("provider %q cost=(%v,%q), want (%v,%q)", name, provider.CostUSDPer1kTokens, provider.CostSource, wantCost, wantSource)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func floatNear(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}

// gateFixtureCatalog returns a catalog used by the ContextWindow / RequiresTools
// gate-firing tests below. It declares two concrete models on the agent.openai
// surface: small-ctx-model has a 4096-token context window (and supports
// tools), while no-tools-model has a generous context window but is marked
// no_tools=true so RequiresTools=true requests are rejected against it.
const gateFixtureCatalogYAML = `
version: 5
generated_at: 2026-04-25T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  small-ctx-model:
    family: gate
    status: active
    context_window: 4096
    surfaces: {agent.openai: small-ctx-model}
  no-tools-model:
    family: gate
    status: active
    context_window: 200000
    no_tools: true
    surfaces: {agent.openai: no-tools-model}
`

func newGateFixtureService(t *testing.T, providerModel string) *service {
	t.Helper()
	catalog := loadRoutingFixtureCatalog(t, gateFixtureCatalogYAML)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", Model: providerModel},
		},
		names:       []string{"local"},
		defaultName: "local",
	}
	return publicRouteTraceService(sc)
}

func findCandidate(t *testing.T, dec *RouteDecision, harness, provider string) RouteCandidate {
	t.Helper()
	if dec == nil {
		t.Fatal("nil decision")
	}
	for _, c := range dec.Candidates {
		if c.Harness == harness && c.Provider == provider {
			return c
		}
	}
	t.Fatalf("candidate harness=%q provider=%q not found in %#v", harness, provider, dec.Candidates)
	return RouteCandidate{}
}

// TestResolveRoute_FiltersByEstimatedPromptTokens proves the engine's
// context-window gate fires end-to-end: with ContextWindows wired from the
// catalog, a 1M-token prompt against a 4096-token model is marked ineligible
// with FilterReasonContextTooSmall.
func TestResolveRoute_FiltersByEstimatedPromptTokens(t *testing.T) {
	svc := newGateFixtureService(t, "small-ctx-model")

	dec, _ := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:               "fiz",
		Model:                 "small-ctx-model",
		EstimatedPromptTokens: 1_000_000,
	})
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	candidate := findCandidate(t, dec, "fiz", "local")
	if candidate.Eligible {
		t.Fatalf("candidate eligible with 1M tokens against 4096 context: %#v", candidate)
	}
	if candidate.FilterReason != FilterReasonContextTooSmall {
		t.Fatalf("FilterReason=%q, want %q (Reason=%q)", candidate.FilterReason, FilterReasonContextTooSmall, candidate.Reason)
	}
}

// TestResolveRoute_FiltersByRequiresTools proves the RequiresTools gate fires
// when the catalog marks the resolved model with no_tools=true.
func TestResolveRoute_FiltersByRequiresTools(t *testing.T) {
	svc := newGateFixtureService(t, "no-tools-model")

	dec, _ := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:       "fiz",
		Model:         "no-tools-model",
		RequiresTools: true,
	})
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	candidate := findCandidate(t, dec, "fiz", "local")
	if candidate.Eligible {
		t.Fatalf("candidate eligible despite no_tools=true: %#v", candidate)
	}
	if candidate.FilterReason != FilterReasonNoToolSupport {
		t.Fatalf("FilterReason=%q, want %q (Reason=%q)", candidate.FilterReason, FilterReasonNoToolSupport, candidate.Reason)
	}
}

// TestResolveRoute_NoOpWhenZero proves that an unset EstimatedPromptTokens /
// RequiresTools request does not change which candidates are eligible compared
// to a baseline request — no spurious filtering on the same model that the
// previous two tests reject under stress.
func TestResolveRoute_NoOpWhenZero(t *testing.T) {
	smallCtxSvc := newGateFixtureService(t, "small-ctx-model")
	noToolsSvc := newGateFixtureService(t, "no-tools-model")

	smallDec, err := smallCtxSvc.ResolveRoute(context.Background(), RouteRequest{
		Harness: "fiz",
		Model:   "small-ctx-model",
	})
	if err != nil {
		t.Fatalf("ResolveRoute small-ctx-model: %v", err)
	}
	smallCandidate := findCandidate(t, smallDec, "fiz", "local")
	if !smallCandidate.Eligible {
		t.Fatalf("small-ctx-model candidate ineligible without EstimatedPromptTokens: %#v", smallCandidate)
	}
	if smallCandidate.FilterReason != "" {
		t.Fatalf("small-ctx-model FilterReason=%q, want empty (no-op gate)", smallCandidate.FilterReason)
	}

	noToolsDec, err := noToolsSvc.ResolveRoute(context.Background(), RouteRequest{
		Harness: "fiz",
		Model:   "no-tools-model",
	})
	if err != nil {
		t.Fatalf("ResolveRoute no-tools-model: %v", err)
	}
	noToolsCandidate := findCandidate(t, noToolsDec, "fiz", "local")
	if !noToolsCandidate.Eligible {
		t.Fatalf("no-tools-model candidate ineligible without RequiresTools=true: %#v", noToolsCandidate)
	}
	if noToolsCandidate.FilterReason != "" {
		t.Fatalf("no-tools-model FilterReason=%q, want empty (no-op gate)", noToolsCandidate.FilterReason)
	}
}

// TestBuildRoutingInputsWiresContextWindowsFromCatalog asserts the structural
// wiring requested by the bead: ProviderEntry.ContextWindows is populated for
// the configured DefaultModel from the catalog so the engine's context-window
// gate has data to act on.
func TestBuildRoutingInputsWiresContextWindowsFromCatalog(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, gateFixtureCatalogYAML)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", Model: "small-ctx-model"},
		},
		names:       []string{"local"},
		defaultName: "local",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	in := svc.buildRoutingInputs(context.Background())
	providers := providerCostsByName(in, "fiz")
	provider, ok := providers["local"]
	if !ok {
		t.Fatalf("fiz/local provider not in inputs: %#v", providers)
	}
	if got := provider.ContextWindows["small-ctx-model"]; got != 4096 {
		t.Fatalf("ContextWindows[small-ctx-model]=%d, want 4096 (full map=%#v)", got, provider.ContextWindows)
	}
	if got := provider.ContextWindowSources["small-ctx-model"]; got != routing.ContextSourceCatalog {
		t.Fatalf("ContextWindowSources[small-ctx-model]=%q, want %q (full map=%#v)", got, routing.ContextSourceCatalog, provider.ContextWindowSources)
	}
}

// TestResolveRoute_LivenessEscalation exercises the policy→tier ladder
// (cheap → default → smart) wired into ResolveRoute. When every candidate
// at the requested tier is filtered out (here: per-provider context-window
// rejection driven by the catalog), ResolveRoute walks the ladder and
// returns the first higher-tier candidate that still satisfies the request.
// When the entire remaining ladder is also empty, ResolveRoute surfaces
// the precise *ErrNoLiveProvider error rather than the engine's
// "no viable routing candidate" jargon.
func TestResolveRoute_LivenessEscalation(t *testing.T) {
	const fixtureCatalog = `
version: 5
generated_at: 2026-04-25T00:00:00Z
policies:
  default:
    min_power: 5
    max_power: 5
    allow_local: true
  cheap:
    min_power: 5
    max_power: 5
    allow_local: true
  smart:
    min_power: 8
    max_power: 8
    allow_local: true
models:
  medium-model:
    family: tier
    status: active
    power: 5
    context_window: 4096
    surfaces: {agent.openai: medium-model}
  high-model:
    family: tier
    status: active
    power: 8
    context_window: 200000
    surfaces: {agent.openai: high-model}
`

	newSvc := func(t *testing.T) (*service, func()) {
		t.Helper()
		// Block claude/codex/gemini subprocess harnesses from the
		// candidate set so the test isolates the fiz harness's
		// per-provider tier escalation behavior.
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "")
		t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
		t.Setenv("GOOGLE_GENAI_USE_GCA", "")
		t.Setenv("GEMINI_CLI_USE_COMPUTE_ADC", "")
		t.Setenv("CLOUD_SHELL", "")

		mediumSrv := openAIModelChatServer(t, []string{"medium-model"}, "medium-model", "ok")
		highSrv := openAIModelChatServer(t, []string{"high-model"}, "high-model", "ok")
		catalog := loadRoutingFixtureCatalog(t, fixtureCatalog)
		restore := replaceRoutingCatalogForTest(t, catalog)
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"alpha-medium": {Type: "openai", BaseURL: mediumSrv.URL + "/v1", APIKey: "k", Model: "medium-model"},
				"beta-high":    {Type: "openai", BaseURL: highSrv.URL + "/v1", APIKey: "k", Model: "high-model"},
			},
			names:       []string{"alpha-medium", "beta-high"},
			defaultName: "alpha-medium",
		}
		registry := harnesses.NewRegistry()
		registry.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
		svc := &service{
			opts:     ServiceOptions{ServiceConfig: sc},
			registry: registry,
			hub:      newSessionHub(),
			catalog:  newCatalogCache(catalogCacheOptions{}),
		}
		cleanup := func() {
			mediumSrv.Close()
			highSrv.Close()
			restore()
		}
		return svc, cleanup
	}

	t.Run("escalates_when_lower_tier_filtered_out", func(t *testing.T) {
		svc, cleanup := newSvc(t)
		defer cleanup()

		// Record a route attempt failure on the lower-tier provider so the
		// real cooldown bookkeeping path (applyRouteAttemptCooldowns) runs
		// against this fixture, exercising the AC's "real ServiceConfig +
		// cooldown fixture" requirement.
		if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
			Harness:  "fiz",
			Provider: "alpha-medium",
			Model:    "medium-model",
			Status:   "failed",
			Reason:   "synthetic unhealthy fixture",
		}); err != nil {
			t.Fatalf("RecordRouteAttempt: %v", err)
		}

		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
			Policy:                "default",
			EstimatedPromptTokens: 50_000,
		})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec == nil || dec.Harness != "fiz" {
			t.Fatalf("decision=%#v, want fiz harness", dec)
		}
		if dec.Provider != "beta-high" {
			t.Fatalf("decision provider=%q, want beta-high (escalated to smart tier)", dec.Provider)
		}
		if dec.Model != "high-model" {
			t.Fatalf("decision model=%q, want high-model", dec.Model)
		}
	})

	t.Run("returns_no_live_provider_when_ladder_exhausted", func(t *testing.T) {
		svc, cleanup := newSvc(t)
		defer cleanup()

		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
			Policy:                "default",
			EstimatedPromptTokens: 1_000_000, // exceeds both 4096 and 200000 contexts
		})
		if err == nil {
			t.Fatalf("ResolveRoute returned no error, decision=%#v", dec)
		}
		if !strings.Contains(err.Error(), "no live provider") {
			t.Fatalf("error=%q, want 'no live provider' message", err.Error())
		}
		if strings.Contains(err.Error(), "no viable routing candidate") {
			t.Fatalf("error=%q must NOT contain engine 'no viable routing candidate' jargon", err.Error())
		}
		var noLive *ErrNoLiveProvider
		if !errors.As(err, &noLive) {
			t.Fatalf("errors.As ErrNoLiveProvider: %T %v", err, err)
		}
		if noLive.StartingPolicy != "default" {
			t.Fatalf("StartingPolicy=%q, want default", noLive.StartingPolicy)
		}
		if noLive.PromptTokens != 1_000_000 {
			t.Fatalf("PromptTokens=%d, want 1000000", noLive.PromptTokens)
		}
	})
}

// TestResolveRouteAutoResolvesToTierDefaultBeforeGate proves that when a
// request specifies Reasoning=auto, the routing engine resolves it to the
// catalog's surface_policy reasoning_default for the requested profile/surface
// BEFORE invoking the capability gate. Without this resolution an off-only
// candidate could win under a profile whose surface default is "high",
// silently dropping reasoning the operator implicitly asked for.
func TestResolveRouteAutoResolvesToTierDefaultBeforeGate(t *testing.T) {
	// off-only harness: SupportedReasoning is empty so the gate accepts
	// "off" (which imposes no requirement) but rejects any named level.
	offOnly := func() routing.HarnessEntry {
		return routing.HarnessEntry{
			Name:                "off-only",
			Surface:             "test-surface",
			CostClass:           "medium",
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			DefaultModel:        "off-model",
			SupportsTools:       true,
		}
	}

	resolver := func(profile, surface string) (string, bool) {
		switch profile {
		case "cheap":
			return "off", true
		case "smart":
			return "high", true
		}
		return "", false
	}

	t.Run("cheap_resolves_to_off_gate_passes", func(t *testing.T) {
		dec, err := routing.Resolve(routing.Request{
			Policy:    "cheap",
			Reasoning: "auto",
		}, routing.Inputs{
			Harnesses:         []routing.HarnessEntry{offOnly()},
			ReasoningResolver: resolver,
		})
		if err != nil {
			t.Fatalf("Resolve cheap+auto: %v", err)
		}
		if dec.Harness != "off-only" || dec.Model != "off-model" {
			t.Fatalf("decision=%#v, want off-only/off-model (auto must resolve to off and pass)", dec)
		}
	})

	t.Run("smart_resolves_to_high_gate_rejects", func(t *testing.T) {
		dec, err := routing.Resolve(routing.Request{
			Policy:    "smart",
			Reasoning: "auto",
		}, routing.Inputs{
			Harnesses:         []routing.HarnessEntry{offOnly()},
			ReasoningResolver: resolver,
		})
		if err == nil {
			t.Fatalf("Resolve smart+auto: expected NoViableCandidateError, got decision=%#v", dec)
		}
		var noViable *routing.NoViableCandidateError
		if !errors.As(err, &noViable) {
			t.Fatalf("error=%T %v, want *routing.NoViableCandidateError", err, err)
		}
		if dec == nil || len(dec.Candidates) != 1 {
			t.Fatalf("Candidates=%#v, want exactly one off-only candidate", dec)
		}
		c := dec.Candidates[0]
		if c.Harness != "off-only" || c.Eligible {
			t.Fatalf("candidate=%#v, want ineligible off-only", c)
		}
		if c.FilterReason != routing.FilterReasonReasoningUnsupported {
			t.Fatalf("FilterReason=%q, want %q (Reason=%q)", c.FilterReason, routing.FilterReasonReasoningUnsupported, c.Reason)
		}
	})

	t.Run("unset_reasoning_does_not_resolve", func(t *testing.T) {
		// Regression guard: the Reasoning=unset path keeps its existing
		// "no requirement" behavior — only Reasoning=auto triggers
		// surface_policy resolution.
		dec, err := routing.Resolve(routing.Request{
			Policy: "smart",
		}, routing.Inputs{
			Harnesses:         []routing.HarnessEntry{offOnly()},
			ReasoningResolver: resolver,
		})
		if err != nil {
			t.Fatalf("Resolve smart+unset: %v", err)
		}
		if dec.Harness != "off-only" {
			t.Fatalf("unset+smart decision=%#v, want off-only (unset must not trigger auto resolution)", dec)
		}
	})
}

func TestDecisionWithCandidatesCopiesInput(t *testing.T) {
	candidates := []RouteCandidate{{Harness: "fiz", Reason: "original"}}
	err := withRouteCandidates(errors.New("no viable routing candidate"), candidates)

	candidates[0].Reason = "changed"
	var traced DecisionWithCandidates
	if !errors.As(err, &traced) {
		t.Fatalf("errors.As DecisionWithCandidates: %T %v", err, err)
	}
	got := traced.RouteCandidates()
	if len(got) != 1 || got[0].Reason != "original" {
		t.Fatalf("RouteCandidates=%#v, want copied original candidate", got)
	}
}

// openrouterCredentialGateCatalog returns a minimal catalog with one
// openrouter-surfaced model so the credential gate has a candidate to
// evaluate. Power 5 keeps it inside the default 1..10 policy bounds.
func openrouterCredentialGateCatalog(t *testing.T) *modelcatalog.Catalog {
	t.Helper()
	return loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-12T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  openrouter/test-model:
    family: gpt
    status: active
    provider_system: openrouter
    deployment_class: managed_cloud_frontier
    power: 5
    surfaces:
      agent.openai: openrouter/test-model
`)
}

// openrouterCredentialGateService wires a service whose only configured
// provider is openrouter pointing at an httptest server. The server's hit
// counter must stay 0 through every credential-gate test: the gate is
// synchronous and must not trigger discovery or dispatch.
//
// A fresh discovery cache fixture pre-seeds the snapshot path so background
// /v1/models refresh has nothing to do — the only HTTP I/O an unfiltered
// openrouter candidate could trigger is dispatch, which the credential gate
// must block.
func openrouterCredentialGateService(t *testing.T, apiKey string) (*service, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: cacheDir}
	writeSnapshotDiscoveryFixture(
		t,
		cache,
		testDiscoverySourceName("openrouter", "openrouter", srv.URL+"/v1", ""),
		time.Now().UTC(),
		[]string{"openrouter/test-model"},
	)

	t.Cleanup(replaceRoutingCatalogForTest(t, openrouterCredentialGateCatalog(t)))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"openrouter": {
				Type:    "openrouter",
				BaseURL: srv.URL + "/v1",
				APIKey:  apiKey,
				Model:   "openrouter/test-model",
			},
		},
		names:       []string{"openrouter"},
		defaultName: "openrouter",
	}
	svc := newTestService(t, ServiceOptions{
		ServiceConfig:       sc,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	return svc, &hits
}

// installPanickingOpenRouterTransport replaces http.DefaultClient.Transport
// with a roundtripper that panics on any request directed at the openrouter
// host. Routing should never call out before the credential gate fires; if
// it does, the panic fails the test immediately rather than masking the
// regression behind a recovered error.
func installPanickingOpenRouterTransport(t *testing.T, host string) {
	t.Helper()
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = &openrouterPanicTransport{t: t, host: host, base: prev}
	t.Cleanup(func() { http.DefaultClient.Transport = prev })
}

type openrouterPanicTransport struct {
	t    *testing.T
	host string
	base http.RoundTripper
}

func (p *openrouterPanicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.URL != nil && p.host != "" && req.URL.Host == p.host {
		p.t.Fatalf("unexpected HTTP call to openrouter host %s while credential gate should have filtered the candidate: %s %s", p.host, req.Method, req.URL)
	}
	base := p.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func findOpenRouterCandidate(t *testing.T, dec *RouteDecision) *RouteCandidate {
	t.Helper()
	if dec == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	for i := range dec.Candidates {
		if dec.Candidates[i].Provider == "openrouter" {
			return &dec.Candidates[i]
		}
	}
	t.Fatalf("openrouter candidate missing from decision %#v", dec.Candidates)
	return nil
}

func TestOpenrouterCredentialMissingNoNetwork(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	svc, hits := openrouterCredentialGateService(t, "")
	pcfg, _ := svc.opts.ServiceConfig.Provider("openrouter")
	srvURL, err := url.Parse(pcfg.BaseURL)
	if err != nil {
		t.Fatalf("parse base URL %q: %v", pcfg.BaseURL, err)
	}
	installPanickingOpenRouterTransport(t, srvURL.Host)

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	if err == nil {
		// no eligible candidates is fine: the gate emits an ineligible row
		// in the trace, which is what AC1 asks us to inspect. A surfaced
		// error is also acceptable as long as the candidate is in the
		// candidate list. The decision pointer may still be non-nil with
		// the rejected candidates attached, depending on the error path.
		_ = dec
	}
	var traced DecisionWithCandidates
	if dec == nil && errors.As(err, &traced) {
		candidates := traced.RouteCandidates()
		dec = &RouteDecision{Candidates: candidates}
	}
	candidate := findOpenRouterCandidate(t, dec)
	if candidate.Eligible {
		t.Fatalf("openrouter candidate eligible with no API key: %#v", *candidate)
	}
	if candidate.FilterReason != string(routing.FilterReasonCredentialMissing) {
		t.Fatalf("openrouter FilterReason=%q, want %q", candidate.FilterReason, routing.FilterReasonCredentialMissing)
	}
	if !strings.Contains(candidate.Reason, "credential missing") {
		t.Fatalf("openrouter Reason=%q, want evidence body to mention credential location", candidate.Reason)
	}
	if !strings.Contains(candidate.Reason, "api_key") {
		t.Fatalf("openrouter Reason=%q, want evidence to name the inspected key location", candidate.Reason)
	}
	if hits.Load() != 0 {
		t.Fatalf("openrouter HTTP hits=%d, want zero — credential gate must not issue any network I/O", hits.Load())
	}
}

func TestOpenrouterMalformedKeyTreatedAsMissing(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"wrong prefix", "abc-not-a-real-openrouter-key-1234567890"},
		{"too short", "sk-or-x"},
		// Bare unexpanded env placeholder: config load preserves the
		// literal "${OPENROUTER_API_KEY}" when the env var is unset
		// (internal/config/config_test.go TestLoad_EnvExpansion_Unset).
		// The gate must catch this before any dispatch attempts a 401.
		{"unexpanded placeholder", "${OPENROUTER_API_KEY}"},
		// Partial env substitution: the value carries the well-known
		// "sk-or-" prefix and clears the length floor, but contains an
		// unexpanded placeholder fragment that would still produce a
		// 401 at dispatch. The literal "${" substring is the signal.
		{"partial placeholder substitution", "sk-or-v1-${KEY_SUFFIX_UNSET}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENROUTER_API_KEY", "")
			svc, hits := openrouterCredentialGateService(t, tc.key)
			pcfg, _ := svc.opts.ServiceConfig.Provider("openrouter")
			srvURL, err := url.Parse(pcfg.BaseURL)
			if err != nil {
				t.Fatalf("parse base URL %q: %v", pcfg.BaseURL, err)
			}
			installPanickingOpenRouterTransport(t, srvURL.Host)

			dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
			_ = err
			var traced DecisionWithCandidates
			if dec == nil && errors.As(err, &traced) {
				dec = &RouteDecision{Candidates: traced.RouteCandidates()}
			}
			candidate := findOpenRouterCandidate(t, dec)
			if candidate.Eligible {
				t.Fatalf("openrouter candidate eligible with malformed key %q: %#v", tc.key, *candidate)
			}
			if candidate.FilterReason != string(routing.FilterReasonCredentialMissing) {
				t.Fatalf("openrouter FilterReason=%q, want %q", candidate.FilterReason, routing.FilterReasonCredentialMissing)
			}
			if hits.Load() != 0 {
				t.Fatalf("openrouter HTTP hits=%d, want zero", hits.Load())
			}
		})
	}
}

func TestOpenrouterValidKeyPassesCredentialGate(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	svc, _ := openrouterCredentialGateService(t, "sk-or-v1-"+strings.Repeat("a", 32))

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	var traced DecisionWithCandidates
	if dec == nil && errors.As(err, &traced) {
		dec = &RouteDecision{Candidates: traced.RouteCandidates()}
	}
	candidate := findOpenRouterCandidate(t, dec)
	if candidate.FilterReason == string(routing.FilterReasonCredentialMissing) {
		t.Fatalf("openrouter candidate gated by credential_missing despite well-formed key: %#v", *candidate)
	}
}

func TestDefaultProviderSkippedWhenUnreachable(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"lmstudio": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", ServerInstance: "127.0.0.1:9999", Model: "model-a"},
		},
		names:       []string{"lmstudio"},
		defaultName: "lmstudio",
	}
	unreachable := map[string]bool{"lmstudio": true}
	name, entry, ok := selectConfiguredNativeProviderWithReachability(sc, ServiceExecuteRequest{}, unreachable)
	if ok {
		t.Fatalf("selectConfiguredNativeProviderWithReachability returned ok=true with unreachable default provider, got name=%q entry=%#v", name, entry)
	}
}

func TestDefaultProviderUsedWhenReachable(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"lmstudio": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", ServerInstance: "127.0.0.1:9999", Model: "model-a"},
		},
		names:       []string{"lmstudio"},
		defaultName: "lmstudio",
	}
	name, entry, ok := selectConfiguredNativeProviderWithReachability(sc, ServiceExecuteRequest{}, nil)
	if !ok {
		t.Fatalf("selectConfiguredNativeProviderWithReachability returned ok=false with reachable default provider")
	}
	if name != "lmstudio" || entry.Model != "model-a" {
		t.Fatalf("selectConfiguredNativeProviderWithReachability returned name=%q entry.Model=%q, want lmstudio/model-a", name, entry.Model)
	}
}
