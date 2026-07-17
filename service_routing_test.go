package fizeau

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
	"gopkg.in/yaml.v3"
)

// TestRouteCandidateFromInternalProjectionParity is the intentional
// same-package seam for the private routing-candidate mapper. The public
// facade tests exercise routing behavior; this test exhaustively owns the
// field projection and copy boundary that callers cannot construct directly.
func TestRouteCandidateFromInternalProjectionParity(t *testing.T) {
	scoreComponents := map[string]float64{
		"base":                100,
		"cost":                -4,
		"deployment_locality": 12,
		"quota_health":        6,
		"utilization":         -3,
		"performance":         -9,
		"power":               18,
	}
	candidate := routing.Candidate{
		Harness:            "fiz",
		Provider:           "local",
		Billing:            BillingModelFixed,
		ActualCashSpend:    true,
		Endpoint:           "primary",
		ServerInstance:     "127.0.0.1:9999",
		Model:              "model-a",
		Score:              42.5,
		CostUSDPer1kTokens: 0.012,
		CostSource:         routing.CostSourceCatalog,
		Power:              7,
		ContextLength:      200000,
		ContextSource:      routing.ContextSourceCatalog,
		ContextHeadroom:    150000,
		Eligible:           false,
		Reason:             "context window is too small",
		FilterReason:       routing.FilterReasonContextTooSmall,
		LatencyMS:          123,
		SpeedTPS:           55,
		Utilization:        0.25,
		SuccessRate:        0.8,
		CostClass:          "expensive",
		QuotaOK:            true,
		QuotaPercentUsed:   25,
		QuotaTrend:         routing.QuotaTrendHealthy,
		StickyAffinity:     10,
		ScoreComponents:    scoreComponents,
	}
	want := RouteCandidate{
		Harness:             "fiz",
		Provider:            "local",
		Billing:             BillingModelFixed,
		ActualCashSpend:     true,
		Endpoint:            "primary",
		ServerInstance:      "127.0.0.1:9999",
		Model:               "model-a",
		Score:               42.5,
		CostUSDPer1kTokens:  0.012,
		CostSource:          routing.CostSourceCatalog,
		EffectiveCost:       0.012,
		EffectiveCostSource: routing.CostSourceCatalog,
		Eligible:            false,
		Reason:              "context window is too small",
		FilterReason:        FilterReasonContextTooSmall,
		ContextLength:       200000,
		ContextSource:       routing.ContextSourceCatalog,
		Components: RouteCandidateComponents{
			Power:                   7,
			Cost:                    0.012,
			CostClass:               "expensive",
			LatencyMS:               123,
			SpeedTPS:                55,
			Utilization:             0.25,
			SuccessRate:             0.8,
			QuotaOK:                 true,
			QuotaPercentUsed:        25,
			QuotaTrend:              routing.QuotaTrendHealthy,
			Capability:              3,
			ContextHeadroom:         150000,
			StickyAffinity:          10,
			PowerWeightedCapability: 18,
			PlacementBonus:          22,
			QuotaBonus:              6,
			MarginalCostPenalty:     4,
			AvailabilityPenalty:     3,
			StaleSignalPenalty:      9,
		},
		ScoreComponents: map[string]float64{
			"base":                100,
			"cost":                -4,
			"deployment_locality": 12,
			"quota_health":        6,
			"utilization":         -3,
			"performance":         -9,
			"power":               18,
		},
	}
	got := routeCandidateFromInternal(candidate, RoutePowerPolicy{MinPower: 6, MaxPower: 8})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routeCandidateFromInternal()=%#v, want %#v", got, want)
	}

	// Keep this list exhaustive: a new public field must be classified as
	// mapper-owned above or explicitly left for a later evidence annotator.
	wantFields := []string{
		"Harness", "Provider", "Billing", "ActualCashSpend", "Endpoint", "ServerInstance", "Model", "Score",
		"CostUSDPer1kTokens", "CostSource", "EffectiveCost", "EffectiveCostSource", "Eligible", "Reason", "FilterReason",
		"ContextLength", "ContextSource", "SourceStatus", "AutoRoutable", "ExactPinOnly", "ExclusionReason", "SnapshotCapturedAt",
		"HealthFreshnessAt", "HealthFreshnessSource", "QuotaFreshnessAt", "QuotaFreshnessSource", "ModelDiscoveryFreshnessAt",
		"ModelDiscoveryFreshnessSource", "Components", "ScoreComponents", "Utilization", "LastProbeAt", "LastProbeSuccess",
	}
	typ := reflect.TypeOf(RouteCandidate{})
	if typ.NumField() != len(wantFields) {
		t.Fatalf("RouteCandidate has %d fields, mapper parity classifies %d; update this test for the new public field", typ.NumField(), len(wantFields))
	}
	for i, wantField := range wantFields {
		if gotField := typ.Field(i).Name; gotField != wantField {
			t.Fatalf("RouteCandidate field %d=%q, want %q; update mapper parity classification", i, gotField, wantField)
		}
	}
	componentFields := []string{
		"Power", "Cost", "CostClass", "LatencyMS", "SpeedTPS", "Utilization", "SuccessRate", "QuotaOK", "QuotaPercentUsed",
		"QuotaTrend", "Capability", "ContextHeadroom", "StickyAffinity", "PowerWeightedCapability", "PowerHintFit", "LatencyWeight",
		"PlacementBonus", "QuotaBonus", "MarginalCostPenalty", "AvailabilityPenalty", "StaleSignalPenalty",
	}
	componentType := reflect.TypeOf(RouteCandidateComponents{})
	if componentType.NumField() != len(componentFields) {
		t.Fatalf("RouteCandidateComponents has %d fields, mapper parity classifies %d; update this test for the new component", componentType.NumField(), len(componentFields))
	}
	for i, wantField := range componentFields {
		if gotField := componentType.Field(i).Name; gotField != wantField {
			t.Fatalf("RouteCandidateComponents field %d=%q, want %q; update mapper parity classification", i, gotField, wantField)
		}
	}

	scoreComponents["base"] = -999
	if got.ScoreComponents["base"] != 100 {
		t.Fatalf("ScoreComponents aliases internal map; base=%v, want copied 100", got.ScoreComponents["base"])
	}

	for internal, public := range map[routing.FilterReason]string{
		routing.FilterReasonContextTooSmall:      FilterReasonContextTooSmall,
		routing.FilterReasonNoToolSupport:        FilterReasonNoToolSupport,
		routing.FilterReasonReasoningUnsupported: FilterReasonReasoningUnsupported,
		routing.FilterReasonUnhealthy:            FilterReasonUnhealthy,
		routing.FilterReasonScoredBelowTop:       FilterReasonScoredBelowTop,
		routing.FilterReasonAboveMaxPower:        FilterReasonAboveMaxPower,
	} {
		mapped := routeCandidateFromInternal(routing.Candidate{FilterReason: internal}, RoutePowerPolicy{})
		if mapped.FilterReason != public {
			t.Errorf("FilterReason %q mapped to %q, want %q", internal, mapped.FilterReason, public)
		}
	}

	aboveMax := routeCandidateFromInternal(routing.Candidate{
		Power:           10,
		FilterReason:    routing.FilterReasonAboveMaxPower,
		ScoreComponents: map[string]float64{"power": -1002},
	}, RoutePowerPolicy{MaxPower: 8})
	if aboveMax.Components.PowerHintFit != -1002 || aboveMax.Components.PowerWeightedCapability != 0 {
		t.Fatalf("above-max components=%#v, want exclusion power_hint_fit=-1002 and weighted capability=0", aboveMax.Components)
	}
}

func TestResolveRouteSelectedContextUsesPositiveCandidate(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-16T00:00:00Z
catalog_version: selected-context-test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  selected-model:
    family: fixture
    status: active
    power: 5
    context_window: 71680
`)
	svc := &service{opts: ServiceOptions{ServiceConfig: &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {ContextWindow: 102400},
		},
		names: []string{"alpha"},
	}}}
	decision := selectedContextTestDecision("selected-model")
	decision.Candidates = []RouteCandidate{
		{
			Harness: "fiz", Provider: "alpha@east", Endpoint: "east", ServerInstance: "server-east", Model: "selected-model",
			Eligible: true, ContextLength: 999999, ContextSource: "wrong-eligible-route",
		},
		{
			Harness: "fiz", Provider: "alpha@west", Endpoint: "west", ServerInstance: "server-west", Model: "selected-model",
			Eligible: false, ContextLength: 888888, ContextSource: "wrong-ineligible-route",
		},
		{
			Harness: "fiz", Provider: "alpha@west", Endpoint: "west", ServerInstance: "server-west", Model: "selected-model",
			Eligible: true, ContextLength: 777777, ContextSource: "selected-exact",
		},
	}
	snapshot := modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{{
		Provider: "alpha", ID: "selected-model", EndpointName: "west", ServerInstance: "server-west",
		ContextWindow: 81920, ContextWindowSource: routing.ContextSourceProviderAPI,
	}}}

	window, source := svc.resolveSelectedRouteContext(decision, snapshot, catalog)
	if window != 777777 || source != "selected-exact" {
		t.Fatalf("selected context=%d/%q, want exact positive candidate 777777/selected-exact", window, source)
	}
	if decision.Harness != "fiz" || decision.Provider != "alpha@west" || decision.Endpoint != "west" ||
		decision.ServerInstance != "server-west" || decision.Model != "selected-model" {
		t.Fatalf("selected context resolution changed route identity: %#v", decision)
	}
}

func TestResolveRouteSelectedContextFallbackPrecedence(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-16T00:00:00Z
catalog_version: selected-context-test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  precedence-model:
    family: fixture
    status: active
    power: 5
    context_window: 71680
`)
	exactSnapshot := []modelsnapshot.KnownModel{
		{
			Provider: "alpha", ID: "precedence-model", EndpointName: "east", ServerInstance: "server-east",
			ContextWindow: 999999, ContextWindowSource: routing.ContextSourceProviderAPI,
		},
		{
			Provider: "alpha", ID: "precedence-model", EndpointName: "west", ServerInstance: "server-west",
			ContextWindow: 81920, ContextWindowSource: routing.ContextSourceProviderAPI,
		},
	}
	nonNativeDecision := selectedContextTestDecisionFor("alpha", "", "", "uncataloged-nonnative-model")
	nonNativeDecision.Harness = "claude"
	nonNativeDecision.Candidates[0].Harness = "claude"
	tests := []struct {
		name       string
		model      string
		provider   ServiceProviderEntry
		snapshot   []modelsnapshot.KnownModel
		decision   *RouteDecision
		wantWindow int
		wantSource string
	}{
		{
			name: "provider config wins", model: "precedence-model",
			provider: ServiceProviderEntry{ContextWindow: 102400}, snapshot: exactSnapshot,
			wantWindow: 102400, wantSource: routing.ContextSourceProviderConfig,
		},
		{
			name: "exact cached provider api wins", model: "precedence-model",
			snapshot:   exactSnapshot,
			wantWindow: 81920, wantSource: routing.ContextSourceProviderAPI,
		},
		{
			name: "exact default endpoint cache wins", model: "precedence-model",
			snapshot: []modelsnapshot.KnownModel{{
				Provider: "alpha", ID: "precedence-model", ContextWindow: 73728,
				ContextWindowSource: routing.ContextSourceProviderAPI,
			}},
			decision:   selectedContextTestDecisionFor("alpha", "", "", "precedence-model"),
			wantWindow: 73728, wantSource: routing.ContextSourceProviderAPI,
		},
		{
			name: "empty axes do not wildcard sibling endpoints", model: "precedence-model",
			snapshot:   exactSnapshot,
			decision:   selectedContextTestDecisionFor("alpha", "", "", "precedence-model"),
			wantWindow: 71680, wantSource: routing.ContextSourceCatalog,
		},
		{
			name: "non-native snapshot row is not provider authority", model: "precedence-model",
			snapshot: []modelsnapshot.KnownModel{{
				Provider: "alpha", Harness: "claude", ID: "precedence-model", EndpointName: "west", ServerInstance: "server-west",
				ContextWindow: 999999, ContextWindowSource: routing.ContextSourceProviderAPI,
			}},
			wantWindow: 71680, wantSource: routing.ContextSourceCatalog,
		},
		{
			name: "non-native route ignores provider config and cache", model: "uncataloged-nonnative-model",
			provider: ServiceProviderEntry{ContextWindow: 102400},
			snapshot: []modelsnapshot.KnownModel{{
				Provider: "alpha", Harness: "fiz", ID: "uncataloged-nonnative-model",
				ContextWindow: 81920, ContextWindowSource: routing.ContextSourceProviderAPI,
			}},
			decision:   nonNativeDecision,
			wantWindow: compaction.DefaultContextWindow, wantSource: routing.ContextSourceDefault,
		},
		{
			name: "catalog wins", model: "precedence-model",
			wantWindow: 71680, wantSource: routing.ContextSourceCatalog,
		},
		{
			name: "default is final fallback", model: "uncataloged-model",
			wantWindow: compaction.DefaultContextWindow, wantSource: routing.ContextSourceDefault,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{opts: ServiceOptions{ServiceConfig: &fakeServiceConfig{
				providers: map[string]ServiceProviderEntry{"alpha": tc.provider},
				names:     []string{"alpha"},
			}}}
			decision := tc.decision
			if decision == nil {
				decision = selectedContextTestDecision(tc.model)
			}
			window, source := svc.resolveSelectedRouteContext(decision, modelsnapshot.ModelSnapshot{Models: tc.snapshot}, catalog)
			if window != tc.wantWindow || source != tc.wantSource {
				t.Fatalf("selected context=%d/%q, want %d/%q", window, source, tc.wantWindow, tc.wantSource)
			}
		})
	}
}

func TestResolveRouteSelectedContextPreservesRawCandidate(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-16T00:00:00Z
catalog_version: selected-context-test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  unrelated-model:
    family: fixture
    status: active
    power: 5
    context_window: 4096
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))
	svc := newTestService(t, ServiceOptions{ServiceConfig: &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {Type: "test", Model: "wired-raw-unknown-model"},
		},
		names:       []string{"alpha"},
		defaultName: "alpha",
	}})

	decision, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness:  "fiz",
		Provider: "alpha",
		Model:    "wired-raw-unknown-model",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if decision == nil {
		t.Fatal("ResolveRoute returned nil decision")
	}
	if decision.ContextLength != compaction.DefaultContextWindow || decision.ContextSource != routing.ContextSourceDefault {
		t.Fatalf("resolved context=%d/%q, want default %d/%q", decision.ContextLength, decision.ContextSource, compaction.DefaultContextWindow, routing.ContextSourceDefault)
	}
	selected, ok := selectedRouteCandidate(decision)
	if !ok {
		t.Fatalf("exact selected candidate missing from trace: decision=%#v candidates=%#v", decision, decision.Candidates)
	}
	if selected.ContextLength != 0 || selected.ContextSource != routing.ContextSourceUnknown {
		t.Fatalf("raw selected candidate context=%d/%q, want 0/%q", selected.ContextLength, selected.ContextSource, routing.ContextSourceUnknown)
	}
	if selected.Harness != decision.Harness || selected.Provider != decision.Provider || selected.Endpoint != decision.Endpoint ||
		selected.ServerInstance != decision.ServerInstance || selected.Model != decision.Model {
		t.Fatalf("selected candidate tuple diverged from decision: selected=%#v decision=%#v", selected, decision)
	}
}

func TestRouteDecisionPreservesRequiredContextEvidence(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-16T00:00:00Z
catalog_version: required-context-evidence-test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  budget-model:
    family: fixture
    status: active
    power: 5
    context_window: 151
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	newCapacityService := func(contextWindow int) *service {
		t.Helper()
		return newTestService(t, ServiceOptions{ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"alpha": {
					Type:                "test",
					Model:               "budget-model",
					ContextWindow:       contextWindow,
					IncludeByDefault:    true,
					IncludeByDefaultSet: true,
				},
			},
			names:       []string{"alpha"},
			defaultName: "alpha",
		}})
	}
	request := RouteRequest{
		Harness: "fiz", Provider: "alpha", Model: "budget-model",
		EstimatedPromptTokens: 100, MaxTokens: 26,
	}
	assertCapacity := func(t *testing.T, decision *RouteDecision, estimated, maxTokens, required int) {
		t.Helper()
		if decision == nil {
			t.Fatal("ResolveRoute returned nil decision evidence")
		}
		if decision.EstimatedPromptTokens != estimated || decision.MaxTokens != maxTokens || decision.RequiredContext != required {
			t.Fatalf("capacity evidence=%d+%d=>%d, want %d+%d=>%d",
				decision.EstimatedPromptTokens, decision.MaxTokens, decision.RequiredContext,
				estimated, maxTokens, required)
		}
	}

	t.Run("success", func(t *testing.T) {
		decision, err := newCapacityService(151).ResolveRoute(context.Background(), request)
		if err != nil {
			t.Fatalf("ResolveRoute equality: %v", err)
		}
		assertCapacity(t, decision, 100, 26, 151)
	})

	t.Run("typed context failure", func(t *testing.T) {
		decision, err := newCapacityService(150).ResolveRoute(context.Background(), request)
		if err == nil {
			t.Fatal("ResolveRoute one-token-short candidate succeeded")
		}
		assertCapacity(t, decision, 100, 26, 151)
		if len(decision.Candidates) != 1 || decision.Candidates[0].FilterReason != FilterReasonContextTooSmall || decision.Candidates[0].Eligible {
			t.Fatalf("failed candidates=%#v, want one typed context_too_small rejection", decision.Candidates)
		}
		if decision.Candidates[0].ContextLength != 150 || decision.Candidates[0].ContextSource != routing.ContextSourceProviderConfig {
			t.Fatalf("failed candidate context=%d/%q, want raw 150/%q",
				decision.Candidates[0].ContextLength, decision.Candidates[0].ContextSource, routing.ContextSourceProviderConfig)
		}
	})

	t.Run("policy failure saturates", func(t *testing.T) {
		req := request
		req.Policy = "missing-policy"
		req.EstimatedPromptTokens = math.MaxInt
		req.MaxTokens = 1
		decision, err := newCapacityService(151).ResolveRoute(context.Background(), req)
		if err == nil {
			t.Fatal("ResolveRoute unknown policy succeeded")
		}
		assertCapacity(t, decision, math.MaxInt, 1, math.MaxInt)
	})

	t.Run("model failure", func(t *testing.T) {
		req := request
		req.Model = "missing-model"
		decision, err := newCapacityService(151).ResolveRoute(context.Background(), req)
		if err == nil {
			t.Fatal("ResolveRoute missing model succeeded")
		}
		assertCapacity(t, decision, 100, 26, 151)
	})

	for _, test := range []struct {
		name string
		req  RouteRequest
	}{
		{
			name: "unknown harness preflight",
			req: RouteRequest{
				Harness: "missing-harness", Model: "budget-model",
				EstimatedPromptTokens: 100, MaxTokens: 26,
			},
		},
		{
			name: "harness model preflight",
			req: RouteRequest{
				Harness: "gemini", Model: "minimax/minimax-m2.7",
				EstimatedPromptTokens: 100, MaxTokens: 26,
			},
		},
		{
			name: "harness policy preflight",
			req: RouteRequest{
				Harness: "fiz", Policy: "smart",
				EstimatedPromptTokens: 100, MaxTokens: 26,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := newCapacityService(151).ResolveRoute(context.Background(), test.req)
			if err == nil {
				t.Fatal("ResolveRoute explicit preflight succeeded")
			}
			assertCapacity(t, decision, 100, 26, 151)
		})
	}
}

func TestServiceRoutingDecisionProjectsRequiredContextEvidence(t *testing.T) {
	req := ServiceExecuteRequest{
		// Deliberately differ from the decision so this test fails if the event
		// adapter bypasses the preserved decision evidence.
		EstimatedPromptTokens: 999,
		MaxTokens:             888,
	}
	decision := RouteDecision{
		Harness: "fiz", Provider: "alpha", Model: "budget-model", Reason: "selected",
		EstimatedPromptTokens: 100,
		MaxTokens:             26,
		RequiredContext:       151,
		ContextLength:         512,
		ContextSource:         routing.ContextSourceProviderConfig,
		Candidates: []RouteCandidate{
			{
				Harness: "fiz", Provider: "unknown", Model: "budget-model",
				ContextLength: 0, ContextSource: routing.ContextSourceUnknown,
			},
			{
				Harness: "fiz", Provider: "alpha", Model: "budget-model", Eligible: true,
				ContextLength: 512, ContextSource: routing.ContextSourceProviderConfig,
			},
		},
	}

	data := serviceRoutingDecisionDataFromDecision(req, decision, "capacity-session")
	if data.EstimatedPromptTokens != 100 || data.MaxTokens != 26 || data.RequiredContext != 151 {
		t.Fatalf("routing event request capacity=%d+%d=>%d, want 100+26=>151",
			data.EstimatedPromptTokens, data.MaxTokens, data.RequiredContext)
	}
	if data.ContextLength != 512 || data.ContextSource != routing.ContextSourceProviderConfig {
		t.Fatalf("routing event selected context=%d/%q, want 512/%q",
			data.ContextLength, data.ContextSource, routing.ContextSourceProviderConfig)
	}
	if len(data.Candidates) != 2 {
		t.Fatalf("routing event candidates=%#v, want two", data.Candidates)
	}
	if data.Candidates[0].ContextLength != 0 || data.Candidates[0].ContextSource != routing.ContextSourceUnknown {
		t.Fatalf("unknown candidate context=%d/%q, want raw 0/%q",
			data.Candidates[0].ContextLength, data.Candidates[0].ContextSource, routing.ContextSourceUnknown)
	}
	if data.Candidates[1].ContextLength != 512 || data.Candidates[1].ContextSource != routing.ContextSourceProviderConfig {
		t.Fatalf("selected candidate context=%d/%q, want raw 512/%q",
			data.Candidates[1].ContextLength, data.Candidates[1].ContextSource, routing.ContextSourceProviderConfig)
	}
}

func selectedContextTestDecision(model string) *RouteDecision {
	return selectedContextTestDecisionFor("alpha@west", "west", "server-west", model)
}

func selectedContextTestDecisionFor(provider, endpoint, serverInstance, model string) *RouteDecision {
	return &RouteDecision{
		Harness: "fiz", Provider: provider, Endpoint: endpoint, ServerInstance: serverInstance, Model: model,
		Candidates: []RouteCandidate{{
			Harness: "fiz", Provider: provider, Endpoint: endpoint, ServerInstance: serverInstance, Model: model,
			Eligible: true, ContextSource: routing.ContextSourceUnknown,
		}},
	}
}

func TestResolveRouteSnapshotProviderPowerCorrelation(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
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
	cacheDir := tempDiscoveryCacheDir(t)
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
	resetProviderProbeForTest(svc)
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

	inputs, _ := svc.routingInputs(context.Background(), nil, modelsnapshot.RefreshBackground)
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
	cacheDir := tempDiscoveryCacheDir(t)
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
	serviceimpl.ApplySubscriptionRoutingCost(&claude, catalog)

	openrouterProvider := routing.ProviderEntry{
		Name:          "openrouter",
		BaseURL:       "https://openrouter.ai/api/v1",
		DefaultModel:  "sonnet-4.6",
		SupportsTools: true,
	}
	serviceimpl.ApplyEndpointRoutingCost(&openrouterProvider, serviceImplProviderEntry(ServiceProviderEntry{
		Type:    "openrouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "sonnet-4.6",
	}), catalog, svc.opts.LocalCostUSDPer1kTokens)

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
		hub:      serviceimpl.NewSessionHub(),
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

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func floatNear(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
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
			hub:      serviceimpl.NewSessionHub(),
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
