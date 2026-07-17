package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/modelcatalog"
)

// TestResolveExecuteRouteWithEngineForwardsPromptShape is the sole
// same-package seam for the public request-to-routing-engine adapter. Concrete
// execute-route validation and dispatch selection are owned and tested by
// internal/serviceimpl.
func TestResolveExecuteRouteWithEngineForwardsPromptShape(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-15T00:00:00Z
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
    context_window: 4096
    surfaces:
      codex: gpt-5.5
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    context_window: 200000
    surfaces:
      codex: gpt-5.4
  gpt-5.4-mini:
    family: gpt
    status: active
    power: 6
    context_window: 200000
    no_tools: true
    surfaces:
      codex: gpt-5.4-mini
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := publicRouteTraceService(nil)
	t.Setenv("PATH", "")
	forceAvailableHarnessesForTest(t, svc, "codex")
	decision, err := svc.resolveExecuteRoute(ServiceExecuteRequest{
		Harness:               "codex",
		Policy:                "default",
		Prompt:                "x",
		EstimatedPromptTokens: 100000,
		RequiresTools:         true,
	})
	if err != nil {
		t.Fatalf("resolveExecuteRoute: %v", err)
	}
	if decision == nil {
		t.Fatal("resolveExecuteRoute returned nil decision")
	}
	if decision.Model != "gpt-5.4" {
		t.Fatalf("Model=%q, want gpt-5.4 after prompt-shape gating", decision.Model)
	}
	smallCtx := findRouteCandidateByHarnessAndModel(t, decision, "codex", "gpt-5.5")
	if smallCtx.Eligible || smallCtx.FilterReason != FilterReasonContextTooSmall {
		t.Fatalf("gpt-5.5 candidate=%#v, want context-window rejection", smallCtx)
	}
	noTools := findRouteCandidateByHarnessAndModel(t, decision, "codex", "gpt-5.4-mini")
	if noTools.Eligible || noTools.FilterReason != FilterReasonNoToolSupport {
		t.Fatalf("gpt-5.4-mini candidate=%#v, want no-tools rejection", noTools)
	}
}

func TestResolveExecuteRouteExplicitPreservesRequiredContextEvidence(t *testing.T) {
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-16T00:00:00Z
catalog_version: explicit-required-context-test
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
    context_window: 512
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))
	svc := newTestService(t, ServiceOptions{ServiceConfig: &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {Type: "test", Model: "budget-model", ContextWindow: 512},
		},
		names:       []string{"alpha"},
		defaultName: "alpha",
	}})

	decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "budget-model",
		EstimatedPromptTokens: 100, MaxTokens: 26,
	})
	if err != nil {
		t.Fatalf("resolve explicit execute route: %v", err)
	}
	if decision == nil {
		t.Fatal("resolve explicit execute route returned nil decision")
	}
	if decision.EstimatedPromptTokens != 100 || decision.MaxTokens != 26 || decision.RequiredContext != 151 {
		t.Fatalf("explicit capacity evidence=%d+%d=>%d, want 100+26=>151",
			decision.EstimatedPromptTokens, decision.MaxTokens, decision.RequiredContext)
	}
}

func TestExplicitNativeContextUsesConfiguredValue(t *testing.T) {
	t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	svc := explicitNativeContextTestService(t, ServiceProviderEntry{
		Type: "lmstudio", Model: "known-context-model", ContextWindow: 65536,
	})

	decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "known-context-model",
	})
	if err != nil {
		t.Fatalf("resolve explicit native route: %v", err)
	}
	assertExplicitNativeContextDecision(t, decision, "known-context-model", "", 65536, ContextSourceProviderConfig)

	defaultDecision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{Harness: "fiz"})
	if err != nil {
		t.Fatalf("resolve default explicit native route: %v", err)
	}
	if defaultDecision.Provider != "" || defaultDecision.Model != "" || defaultDecision.Endpoint != "" ||
		defaultDecision.ServerInstance != "" || len(defaultDecision.Candidates) != 0 {
		t.Fatalf("default explicit route identity changed: %#v", defaultDecision)
	}
	if defaultDecision.ContextLength != 65536 || defaultDecision.ContextSource != ContextSourceProviderConfig {
		t.Fatalf("default explicit native context=%d/%q, want 65536/%q",
			defaultDecision.ContextLength, defaultDecision.ContextSource, ContextSourceProviderConfig)
	}
}

func TestExplicitNativeContextUsesCachedProviderAPI(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	const baseURL = "http://provider.invalid/v1"
	writeSnapshotDiscoveryContextFixture(
		t,
		&discoverycache.Cache{Root: cacheRoot},
		testDiscoverySourceName("alpha", "alpha", baseURL, "alpha-server"),
		"known-context-model",
		49152,
	)
	svc := explicitNativeContextTestService(t, ServiceProviderEntry{
		Type: "lmstudio", BaseURL: baseURL, ServerInstance: "alpha-server", Model: "known-context-model",
	})

	decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "known-context-model",
	})
	if err != nil {
		t.Fatalf("resolve explicit native route: %v", err)
	}
	assertExplicitNativeContextDecision(t, decision, "known-context-model", "alpha-server", 49152, ContextSourceProviderAPI)
}

func TestExplicitNativeContextDoesNotBorrowSiblingEndpointCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	const siblingURL = "http://sibling.invalid/v1"
	writeSnapshotDiscoveryContextFixture(
		t,
		&discoverycache.Cache{Root: cacheRoot},
		testDiscoverySourceName("alpha", "sibling", siblingURL, "sibling-server"),
		"known-context-model",
		99999,
	)
	svc := explicitNativeContextTestService(t, ServiceProviderEntry{
		Type: "lmstudio", BaseURL: "http://direct.invalid/v1", Model: "known-context-model",
		Endpoints: []ServiceProviderEndpoint{{
			Name: "sibling", BaseURL: siblingURL, ServerInstance: "sibling-server",
		}},
	})

	decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "known-context-model",
	})
	if err != nil {
		t.Fatalf("resolve explicit native route: %v", err)
	}
	assertExplicitNativeContextDecision(t, decision, "known-context-model", "", 32768, ContextSourceCatalog)
}

func TestExplicitNativeContextFallsBackToCatalogThenDefault(t *testing.T) {
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	for _, test := range []struct {
		name       string
		model      string
		wantWindow int
		wantSource string
	}{
		{name: "catalog", model: "known-context-model", wantWindow: 32768, wantSource: ContextSourceCatalog},
		{name: "default", model: "opaque-uncataloged-xyz", wantWindow: compaction.DefaultContextWindow, wantSource: ContextSourceDefault},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
			svc := explicitNativeContextTestService(t, ServiceProviderEntry{Type: "fixture", Model: test.model})
			decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
				Harness: "fiz", Provider: "alpha", Model: test.model,
			})
			if err != nil {
				t.Fatalf("resolve explicit native route: %v", err)
			}
			assertExplicitNativeContextDecision(t, decision, test.model, "", test.wantWindow, test.wantSource)
		})
	}
}

func TestExplicitNativeContextDoesNotProbeProvider(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	probed := make(chan *http.Request, 1)
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: explicitNativeProbeRoundTripper(func(req *http.Request) (*http.Response, error) {
		select {
		case probed <- req:
		default:
		}
		return nil, context.Canceled
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })
	svc := explicitNativeContextTestService(t, ServiceProviderEntry{
		Type: "lmstudio", BaseURL: "http://probe.invalid/v1", Model: "opaque-uncataloged-xyz",
	})

	decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "opaque-uncataloged-xyz",
	})
	if err != nil {
		t.Fatalf("resolve explicit native route: %v", err)
	}
	assertExplicitNativeContextDecision(
		t, decision, "opaque-uncataloged-xyz", "", compaction.DefaultContextWindow, ContextSourceDefault,
	)
	select {
	case req := <-probed:
		t.Fatalf("explicit native context probed provider at %s, want cache-only resolution", req.URL)
	case <-time.After(100 * time.Millisecond):
	}
}

func explicitNativeContextCatalog(t *testing.T) *modelcatalog.Catalog {
	t.Helper()
	return loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-16T00:00:00Z
catalog_version: explicit-native-context-test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  known-context-model:
    family: fixture
    status: active
    power: 5
    context_window: 32768
`)
}

func explicitNativeContextTestService(t *testing.T, provider ServiceProviderEntry) *service {
	t.Helper()
	return newTestService(t, ServiceOptions{ServiceConfig: &fakeServiceConfig{
		providers:   map[string]ServiceProviderEntry{"alpha": provider},
		names:       []string{"alpha"},
		defaultName: "alpha",
	}})
}

func assertExplicitNativeContextDecision(t *testing.T, decision *RouteDecision, model, serverInstance string, window int, source string) {
	t.Helper()
	if decision == nil {
		t.Fatal("resolve explicit native route returned nil decision")
	}
	if decision.Harness != "fiz" || decision.Provider != "alpha" || decision.Model != model || decision.Reason != "explicit" {
		t.Fatalf("explicit route identity=%q/%q/%q reason=%q, want fiz/alpha/%s explicit",
			decision.Harness, decision.Provider, decision.Model, decision.Reason, model)
	}
	if decision.Endpoint != "" || decision.ServerInstance != serverInstance || len(decision.Candidates) != 0 {
		t.Fatalf("explicit route axes endpoint=%q server=%q candidates=%d, want empty/%q/0",
			decision.Endpoint, decision.ServerInstance, len(decision.Candidates), serverInstance)
	}
	if decision.ContextLength != window || decision.ContextSource != source {
		t.Fatalf("explicit native context=%d/%q, want %d/%q", decision.ContextLength, decision.ContextSource, window, source)
	}
}

type explicitNativeProbeRoundTripper func(*http.Request) (*http.Response, error)

func (f explicitNativeProbeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func writeSnapshotDiscoveryContextFixture(t *testing.T, cache *discoverycache.Cache, source, model string, window int) {
	t.Helper()
	payload, err := json.Marshal(struct {
		CapturedAt          time.Time `json:"captured_at"`
		Models              []string  `json:"models"`
		Source              string    `json:"source"`
		ContextWindow       int       `json:"context_window"`
		ContextWindowSource string    `json:"context_window_source"`
	}{
		CapturedAt:          time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Models:              []string{model},
		Source:              "test-fixture",
		ContextWindow:       window,
		ContextWindowSource: ContextSourceProviderAPI,
	})
	if err != nil {
		t.Fatalf("marshal context discovery fixture: %v", err)
	}
	src := discoverycache.Source{
		Tier: "discovery", Name: source, TTL: time.Hour, RefreshDeadline: time.Second,
	}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatalf("write context discovery fixture: %v", err)
	}
}

func findRouteCandidateByHarnessAndModel(t *testing.T, decision *RouteDecision, harness, model string) RouteCandidate {
	t.Helper()
	if decision == nil {
		t.Fatal("nil route decision")
	}
	for _, candidate := range decision.Candidates {
		if candidate.Harness == harness && candidate.Model == model {
			return candidate
		}
	}
	t.Fatalf("candidate harness=%q model=%q not found in %#v", harness, model, decision.Candidates)
	return RouteCandidate{}
}
