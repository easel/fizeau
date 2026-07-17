package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routing"
)

func TestSupportedModelsForHarnessExpandsDiscoveredClaudeNames(t *testing.T) {
	previous := subprocessHarnessModelIDs
	t.Cleanup(func() { subprocessHarnessModelIDs = previous })
	subprocessHarnessModelIDs = func(name string, _ harnesses.HarnessConfig) []string {
		if name == "claude-tui" {
			return []string{"fable-5", "fable"}
		}
		return nil
	}

	cfg, ok := harnesses.NewRegistry().Get("claude-tui")
	if !ok {
		t.Fatal("claude-tui harness is not registered")
	}
	models := supportedModelsForHarness("claude-tui", cfg, nil)
	for _, want := range []string{"fable-5", "claude-fable-5", "fable"} {
		if !slices.Contains(models, want) {
			t.Fatalf("supported models = %v, want %q", models, want)
		}
		if !modelSupportedForHarness("claude-tui", cfg, want, "") {
			t.Fatalf("modelSupportedForHarness rejected discovered equivalent %q", want)
		}
	}
}

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

func TestExplicitNativeContextGateRejectsUnknownAndInsufficient(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		decision := &RouteDecision{
			Harness: "fiz", Provider: "alpha", Model: "opaque-model",
			RequiredContext: 1, ContextSource: ContextSourceUnknown,
		}
		err := gateExplicitNativeRequiredContext(decision)
		assertExplicitNativeContextFailure(t, decision, err, 0, ContextSourceUnknown,
			"context window unknown < required 1")
	})

	t.Run("insufficient", func(t *testing.T) {
		t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
		t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
		svc := explicitNativeContextTestService(t, ServiceProviderEntry{
			Type: "fixture", Model: "known-context-model", ContextWindow: 150,
		})
		decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
			Harness: "fiz", Provider: "alpha", Model: "known-context-model",
			EstimatedPromptTokens: 100, MaxTokens: 26,
		})
		if decision == nil || decision.RequiredContext != 151 {
			t.Fatalf("required context=%#v, want preserved 151", decision)
		}
		assertExplicitNativeContextFailure(t, decision, err, 150, ContextSourceProviderConfig,
			"context window 150 < required 151")
	})

	t.Run("saturating evidence", func(t *testing.T) {
		t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
		t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
		svc := explicitNativeContextTestService(t, ServiceProviderEntry{
			Type: "fixture", Model: "known-context-model", ContextWindow: math.MaxInt - 1,
		})
		decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
			Harness: "fiz", Provider: "alpha", Model: "known-context-model",
			EstimatedPromptTokens: math.MaxInt, MaxTokens: 1,
		})
		if decision == nil || decision.RequiredContext != math.MaxInt {
			t.Fatalf("saturating required context=%#v, want math.MaxInt", decision)
		}
		assertExplicitNativeContextFailure(t, decision, err, math.MaxInt-1, ContextSourceProviderConfig,
			fmt.Sprintf("context window %d < required %d", math.MaxInt-1, math.MaxInt))
	})
}

func TestExplicitNativeContextGateEqualityPasses(t *testing.T) {
	t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	svc := explicitNativeContextTestService(t, ServiceProviderEntry{
		Type: "fixture", Model: "known-context-model", ContextWindow: 151,
	})
	decision, err := svc.resolveExecuteRouteInternal(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "known-context-model",
		EstimatedPromptTokens: 100, MaxTokens: 26,
	})
	if err != nil {
		t.Fatalf("equality gate: %v", err)
	}
	assertExplicitNativeContextDecision(t, decision, "known-context-model", "", 151, ContextSourceProviderConfig)
	if decision.RequiredContext != 151 {
		t.Fatalf("RequiredContext=%d, want 151", decision.RequiredContext)
	}

	zero := &RouteDecision{Harness: "fiz", RequiredContext: 0, ContextLength: 0, ContextSource: ContextSourceUnknown}
	if err := gateExplicitNativeRequiredContext(zero); err != nil || len(zero.Candidates) != 0 {
		t.Fatalf("zero requirement gate err=%v candidates=%#v, want exact-pin pass", err, zero.Candidates)
	}
}

func TestExecuteExplicitNativeContextFailurePrecedesSession(t *testing.T) {
	cacheRoot := t.TempDir()
	logDir := t.TempDir()
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
	config := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{"alpha": {
			Type: "lmstudio", BaseURL: "http://preflight-probe.invalid/v1",
			Model: "known-context-model", ContextWindow: 150,
		}},
		names: []string{"alpha"}, defaultName: "alpha",
	}
	// newTestService deliberately leaves hub nil. Reaching OpenSession would
	// panic, so a normal returned error proves the capacity gate ran first.
	svc := newTestService(t, ServiceOptions{ServiceConfig: config, SessionLogDir: logDir})
	events, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "known-context-model", Prompt: "x",
		EstimatedPromptTokens: 100, MaxTokens: 26,
	})
	if events != nil || err == nil {
		t.Fatalf("Execute events=%#v err=%v, want synchronous pre-session capacity failure", events, err)
	}
	assertContextFailureCandidates(t, err, "known-context-model", 150, ContextSourceProviderConfig,
		"context window 150 < required 151")
	if _, ok := AsRejectedOverride(err); ok {
		t.Fatalf("pre-session context failure was wrapped as rejected_override: %v", err)
	}
	entries, readErr := os.ReadDir(logDir)
	if readErr != nil {
		t.Fatalf("read session log dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("pre-session context failure wrote %d log entries: %#v", len(entries), entries)
	}
	select {
	case req := <-probed:
		t.Fatalf("pre-session context gate probed provider at %s", req.URL)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertExplicitNativeContextFailure(t *testing.T, decision *RouteDecision, err error, window int, source, reason string) {
	t.Helper()
	if decision == nil || err == nil {
		t.Fatalf("decision=%#v err=%v, want typed context failure", decision, err)
	}
	assertContextFailureCandidates(t, err, decision.Model, window, source, reason)
	var evidence DecisionWithCandidates
	if !errors.As(err, &evidence) {
		t.Fatalf("error %T does not expose DecisionWithCandidates", err)
	}
	if got := evidence.RouteCandidates(); len(got) != 1 || !reflect.DeepEqual(got, decision.Candidates) {
		t.Fatalf("error candidates=%#v decision candidates=%#v, want identical trace", got, decision.Candidates)
	}
}

func assertContextFailureCandidates(t *testing.T, err error, model string, window int, source, reason string) {
	t.Helper()
	var noViable *routing.NoViableCandidateError
	if !errors.As(err, &noViable) || noViable.Rejected != 1 {
		t.Fatalf("error=%T %v, want one rejected NoViableCandidateError", err, err)
	}
	var evidence DecisionWithCandidates
	if !errors.As(err, &evidence) {
		t.Fatalf("error %T does not expose DecisionWithCandidates", err)
	}
	candidates := evidence.RouteCandidates()
	if len(candidates) != 1 {
		t.Fatalf("context candidates=%#v, want one", candidates)
	}
	candidate := candidates[0]
	if candidate.Harness != "fiz" || candidate.Provider != "alpha" || candidate.Endpoint != "" ||
		candidate.ServerInstance != "" || candidate.Model != model {
		t.Fatalf("context candidate identity=%q/%q/%q/%q/%q, want fiz/alpha/empty/empty/%s",
			candidate.Harness, candidate.Provider, candidate.Endpoint, candidate.ServerInstance, candidate.Model, model)
	}
	if candidate.Eligible || candidate.FilterReason != FilterReasonContextTooSmall || candidate.Reason != reason ||
		candidate.ContextLength != window || candidate.ContextSource != source {
		t.Fatalf("context candidate=%#v, want rejected %d/%q reason=%q", candidate, window, source, reason)
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
