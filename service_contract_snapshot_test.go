package fizeau

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routingquality"
	"github.com/easel/fizeau/internal/runtimesignals"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/stretchr/testify/require"
)

type unifiedSnapshotFixture struct {
	svc      *service
	sc       *fakeServiceConfig
	cache    *discoverycache.Cache
	snapshot modelsnapshot.ModelSnapshot
	rows     map[string]modelsnapshot.KnownModel
	models   []ModelInfo
	alpha    *contractOpenAIServer
	beta     *contractOpenAIServer
}

type contractOpenAIServer struct {
	server         *httptest.Server
	mu             sync.Mutex
	models         []string
	modelsCalls    int
	chatCalls      int
	requestedModel string
	responseModel  string
	responseText   string
}

func newContractOpenAIServer(t *testing.T, responseModel, responseText string) *contractOpenAIServer {
	t.Helper()
	s := &contractOpenAIServer{
		responseModel: responseModel,
		responseText:  responseText,
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			s.mu.Lock()
			s.modelsCalls++
			models := append([]string(nil), s.models...)
			responseModel := s.responseModel
			s.mu.Unlock()
			if len(models) == 0 && responseModel != "" {
				models = []string{responseModel}
			}
			if len(models) == 0 {
				models = []string{"stub-model"}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": func() []map[string]string {
					out := make([]map[string]string, 0, len(models))
					for _, model := range models {
						out = append(out, map[string]string{"id": model})
					}
					return out
				}(),
			})
		case "/v1/chat/completions":
			s.mu.Lock()
			s.chatCalls++
			s.requestedModel = ""
			s.mu.Unlock()

			defer r.Body.Close()
			var req struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				s.mu.Lock()
				s.requestedModel = req.Model
				s.mu.Unlock()
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"id":"chatcmpl-contract",
				"object":"chat.completion",
				"created":1712534400,
				"model":%q,
				"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}
			}`, s.responseModel, s.responseText)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *contractOpenAIServer) baseURL() string {
	return s.server.URL + "/v1"
}

func (s *contractOpenAIServer) setModels(models ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = append([]string(nil), models...)
}

func (s *contractOpenAIServer) modelsCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelsCalls
}

func (s *contractOpenAIServer) chatCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatCalls
}

func (s *contractOpenAIServer) requestedChatModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestedModel
}

func newUnifiedSnapshotFixture(t *testing.T) *unifiedSnapshotFixture {
	t.Helper()
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	cache := &discoverycache.Cache{Root: cacheDir}
	alpha := newContractOpenAIServer(t, "qwen3.5-27b", "alpha-ok")
	beta := newContractOpenAIServer(t, "qwen3.5-27b", "beta-ok")
	alpha.setModels("qwen3.5-27b")
	beta.setModels("qwen3.5-27b")

	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("alpha", "primary", alpha.baseURL(), "alpha-instance"), capturedAt, []string{"qwen3.5-27b"})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("beta", "secondary", beta.baseURL(), "beta-instance"), capturedAt, []string{"qwen3.5-27b"})

	quotaAlpha := 17
	quotaBeta := 3
	require.NoError(t, runtimesignals.Write(cache, runtimesignals.Signal{
		Provider:         "alpha",
		Status:           runtimesignals.StatusAvailable,
		QuotaRemaining:   &quotaAlpha,
		RecentP50Latency: 120 * time.Millisecond,
		RecordedAt:       capturedAt,
	}))
	require.NoError(t, runtimesignals.Write(cache, runtimesignals.Signal{
		Provider:         "beta",
		Status:           runtimesignals.StatusAvailable,
		QuotaRemaining:   &quotaBeta,
		RecentP50Latency: 240 * time.Millisecond,
		RecordedAt:       capturedAt,
	}))

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-12T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
  air-gapped:
    min_power: 1
    max_power: 10
    allow_local: true
    require: [no_remote]
models:
  qwen3.5-27b:
    family: qwen
    status: active
    provider_system: openai
    power: 5
    context_window: 32768
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {
				Type:           "lmstudio",
				BaseURL:        alpha.baseURL(),
				ServerInstance: "alpha-instance",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "primary", BaseURL: alpha.baseURL(), ServerInstance: "alpha-instance"},
				},
				Model: "qwen3.5-27b",
			},
			"beta": {
				Type:           "openrouter",
				BaseURL:        beta.baseURL(),
				ServerInstance: "beta-instance",
				Endpoints:      []ServiceProviderEndpoint{{Name: "secondary", BaseURL: beta.baseURL(), ServerInstance: "beta-instance"}},
				Model:          "qwen3.5-27b",
			},
		},
		names:       []string{"alpha", "beta"},
		defaultName: "alpha",
	}

	svc := &service{
		opts:             ServiceOptions{ServiceConfig: sc},
		registry:         harnesses.NewRegistry(),
		hub:              serviceimpl.NewSessionHub(),
		routeHealth:      routehealth.NewStore(),
		routeSticky:      routehealth.NewStickyState(),
		routingQuality:   routingquality.NewStore(routingquality.DefaultStoreCap),
		providerQuota:    NewProviderQuotaStateStore(),
		providerBurnRate: NewProviderBurnRateTracker(),
	}

	require.NoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "alpha",
		Endpoint:  "primary",
		Model:     "qwen3.5-27b",
		Status:    "success",
		Reason:    "route_attempt_success",
		Duration:  120 * time.Millisecond,
		Timestamp: capturedAt,
	}))
	require.NoError(t, svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "beta",
		Endpoint:  "secondary",
		Model:     "qwen3.5-27b",
		Status:    "failed",
		Reason:    "route_attempt_failure",
		Duration:  240 * time.Millisecond,
		Timestamp: capturedAt,
	}))
	svc.routeSticky.RecordUtilization("alpha", "primary", "qwen3.5-27b", utilization.EndpointUtilization{
		ActiveRequests: utilInt(1),
		QueuedRequests: utilInt(0),
		MaxConcurrency: utilInt(4),
		Source:         utilization.SourceLlamaMetrics,
		Freshness:      utilization.FreshnessFresh,
		ObservedAt:     capturedAt,
	})
	svc.routeSticky.RecordUtilization("beta", "secondary", "qwen3.5-27b", utilization.EndpointUtilization{
		ActiveRequests: utilInt(3),
		QueuedRequests: utilInt(1),
		MaxConcurrency: utilInt(4),
		Source:         utilization.SourceLlamaMetrics,
		Freshness:      utilization.FreshnessFresh,
		ObservedAt:     capturedAt,
	})

	rows, err := svc.ListModels(context.Background(), ModelFilter{})
	require.NoError(t, err)
	snapshot, err := assembleModelSnapshotFromServiceConfigWithOptions(context.Background(), sc, catalog, cache.Root, modelsnapshot.AssembleOptions{Refresh: modelsnapshot.RefreshForce})
	require.NoError(t, err)
	rowsByKey := snapshotRowsByKey(snapshot.Models)
	require.Len(t, rows, len(rowsByKey))
	for _, row := range rows {
		snapshotRow, ok := rowsByKey[modelInfoKey(row)]
		require.True(t, ok, "list-models row should match snapshot: %#v", row)
		require.Equal(t, snapshotRow.ProviderType, row.ProviderType)
		require.Equal(t, snapshotRow.Power, row.Power)
		require.Equal(t, snapshotRow.AutoRoutable, row.AutoRoutable)
		require.Equal(t, snapshotRow.ExactPinOnly, row.ExactPinOnly)
		require.Equal(t, snapshotRow.EndpointName, row.EndpointName)
		require.Equal(t, snapshotRow.ServerInstance, row.ServerInstance)
	}

	return &unifiedSnapshotFixture{
		svc:      svc,
		sc:       sc,
		cache:    cache,
		snapshot: snapshot,
		rows:     rowsByKey,
		models:   rows,
		alpha:    alpha,
		beta:     beta,
	}
}

func snapshotRowsByKey(rows []modelsnapshot.KnownModel) map[string]modelsnapshot.KnownModel {
	out := make(map[string]modelsnapshot.KnownModel, len(rows))
	for _, row := range rows {
		out[snapshotRowKey(row.Provider, row.ID, row.EndpointName, row.ServerInstance)] = row
	}
	return out
}

func snapshotRowKey(provider, model, endpoint, serverInstance string) string {
	return strings.Join([]string{provider, model, endpoint, serverInstance}, "\x00")
}

func modelInfoKey(info ModelInfo) string {
	return snapshotRowKey(info.Provider, info.ID, info.EndpointName, info.ServerInstance)
}

func utilInt(v int) *int {
	return &v
}

func drainUnifiedServiceEvents(t *testing.T, ch <-chan ServiceEvent, timeout time.Duration) []ServiceEvent {
	t.Helper()
	var events []ServiceEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timer.C:
			t.Fatalf("timed out waiting for service events")
			return events
		}
	}
}

func decodeRoutingDecisionEvent(t *testing.T, ev ServiceEvent) ServiceRoutingDecisionData {
	t.Helper()
	var payload ServiceRoutingDecisionData
	require.NoError(t, json.Unmarshal(ev.Data, &payload))
	return payload
}

func decodeFinalEvent(t *testing.T, ev ServiceEvent) harnesses.FinalData {
	t.Helper()
	var payload harnesses.FinalData
	require.NoError(t, json.Unmarshal(ev.Data, &payload))
	return payload
}

func TestAutoRoutingUsesUnifiedModelSnapshot(t *testing.T) {
	fixture := newUnifiedSnapshotFixture(t)
	alphaModelsBefore := fixture.alpha.modelsCallCount()
	betaModelsBefore := fixture.beta.modelsCallCount()

	listed := make(map[string]ModelInfo, len(fixture.models))
	for _, row := range fixture.models {
		listed[modelInfoKey(row)] = row
	}
	require.Len(t, listed, 2)

	decision, err := fixture.svc.ResolveRoute(context.Background(), RouteRequest{
		Policy: "air-gapped",
		Model:  "qwen3.5-27b",
	})
	require.NoError(t, err)
	require.NotNil(t, decision)
	candidates := make([]RouteCandidate, 0, 2)
	for _, candidate := range decision.Candidates {
		if candidate.Provider == "alpha" || candidate.Provider == "beta" {
			candidates = append(candidates, candidate)
		}
	}
	require.Len(t, candidates, 2)

	for _, candidate := range candidates {
		row, ok := routeSnapshotEvidenceForCandidate(candidate, fixture.snapshot)
		require.True(t, ok, "candidate should match snapshot row: %#v", candidate)
		require.Contains(t, listed, snapshotRowKey(row.Provider, row.ID, row.EndpointName, row.ServerInstance))
	}

	selected, ok := routeSnapshotEvidenceForCandidate(RouteCandidate{
		Provider:       decision.Provider,
		Endpoint:       decision.Endpoint,
		ServerInstance: decision.ServerInstance,
		Model:          decision.Model,
	}, fixture.snapshot)
	require.True(t, ok, "selected route must match snapshot")
	require.Equal(t, "alpha", selected.Provider)
	require.Equal(t, alphaModelsBefore, fixture.alpha.modelsCallCount(), "ResolveRoute should not probe alpha /v1/models")
	require.Equal(t, betaModelsBefore, fixture.beta.modelsCallCount(), "ResolveRoute should not probe beta /v1/models")

	rejected := false
	for _, candidate := range candidates {
		if candidate.Provider != "beta" {
			continue
		}
		rejected = true
		require.False(t, candidate.Eligible)
		require.Equal(t, "policy_filtered", candidate.FilterReason)
	}
	require.True(t, rejected, "expected beta candidate to be rejected by the air-gapped policy gate")
}

// @covers US-004-AC1
func TestExecuteRouteEvidenceMatchesModelsSnapshot(t *testing.T) {
	fixture := newUnifiedSnapshotFixture(t)
	alphaModelsBefore := fixture.alpha.modelsCallCount()
	betaModelsBefore := fixture.beta.modelsCallCount()

	ch, err := fixture.svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt: "hello",
		Model:  "qwen3.5-27b",
		Policy: "air-gapped",
	})
	require.NoError(t, err)
	events := drainUnifiedServiceEvents(t, ch, 10*time.Second)

	var routingDecision *ServiceRoutingDecisionData
	var final *harnesses.FinalData
	for _, ev := range events {
		switch ev.Type {
		case ServiceEventTypeRoutingDecision:
			payload := decodeRoutingDecisionEvent(t, ev)
			routingDecision = &payload
		case ServiceEventTypeFinal:
			payload := decodeFinalEvent(t, ev)
			final = &payload
		}
	}
	require.NotNil(t, routingDecision, "expected routing_decision event")
	require.NotNil(t, final, "expected final event")
	require.False(t, routingDecision.SnapshotCapturedAt.IsZero(), "routing_decision must carry snapshot_captured_at")
	require.Equal(t, "success", final.Status)
	require.NotNil(t, final.RoutingActual)
	require.Equal(t, "alpha", final.RoutingActual.Provider)
	require.Equal(t, "qwen3.5-27b", final.RoutingActual.Model)
	require.NotEmpty(t, final.RoutingActual.ServerInstance)
	require.Equal(t, 1, fixture.alpha.chatCallCount())
	require.Equal(t, 0, fixture.beta.chatCallCount())
	require.Equal(t, alphaModelsBefore, fixture.alpha.modelsCallCount(), "Execute should not probe alpha /v1/models")
	require.Equal(t, betaModelsBefore, fixture.beta.modelsCallCount(), "Execute should not probe beta /v1/models")

	var selectedCandidate *ServiceRoutingDecisionCandidate
	var routeDecisionCandidates []ServiceRoutingDecisionCandidate
	for i := range routingDecision.Candidates {
		candidate := routingDecision.Candidates[i]
		if candidate.Provider != "alpha" && candidate.Provider != "beta" {
			continue
		}
		routeDecisionCandidates = append(routeDecisionCandidates, candidate)
	}
	require.Len(t, routeDecisionCandidates, 2)
	for i := range routeDecisionCandidates {
		candidate := &routeDecisionCandidates[i]
		if candidate.Eligible && candidate.Provider == final.RoutingActual.Provider && candidate.Model == final.RoutingActual.Model {
			selectedCandidate = candidate
			break
		}
	}
	require.NotNil(t, selectedCandidate, "expected selected candidate in routing_decision")
	require.False(t, selectedCandidate.SnapshotCapturedAt.IsZero(), "selected candidate must carry snapshot_captured_at")
	decisionCandidate := RouteCandidate{
		Provider:       selectedCandidate.Provider,
		Endpoint:       selectedCandidate.Endpoint,
		ServerInstance: final.RoutingActual.ServerInstance,
		Model:          selectedCandidate.Model,
	}
	_, ok := routeSnapshotEvidenceForCandidate(decisionCandidate, fixture.snapshot)
	require.True(t, ok, "final routing_actual should match a snapshot row")

	for _, candidate := range routeDecisionCandidates {
		row, ok := routeSnapshotEvidenceForCandidate(RouteCandidate{
			Provider:       candidate.Provider,
			Endpoint:       candidate.Endpoint,
			ServerInstance: candidate.ServerInstance,
			Model:          candidate.Model,
		}, fixture.snapshot)
		require.True(t, ok, "candidate should match snapshot row: %#v", candidate)
		require.Equal(t, row.ID, candidate.Model)
		require.False(t, candidate.SnapshotCapturedAt.IsZero(), "candidate must carry snapshot_captured_at")
		require.Equal(t, row.ActualCashSpend, candidate.ActualCashSpend)
		require.Equal(t, row.EffectiveCost, candidate.EffectiveCost)
		require.Equal(t, row.EffectiveCostSource, candidate.EffectiveCostSource)
		require.Equal(t, row.HealthFreshnessSource, candidate.HealthFreshnessSource)
		require.Equal(t, row.QuotaFreshnessSource, candidate.QuotaFreshnessSource)
		require.Equal(t, string(row.DiscoveredVia), candidate.ModelDiscoveryFreshnessSource)
	}
}

func TestRouteStatusMatchesModelsSnapshot(t *testing.T) {
	fixture := newUnifiedSnapshotFixture(t)

	_, err := fixture.svc.ResolveRoute(context.Background(), RouteRequest{
		Policy: "air-gapped",
		Model:  "qwen3.5-27b",
	})
	require.NoError(t, err)

	report, err := fixture.svc.RouteStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, report)
	require.False(t, report.SnapshotCapturedAt.IsZero(), "RouteStatus report should carry snapshot_captured_at")
	require.Len(t, report.Routes, 1)

	rowsByKey := make(map[string]modelsnapshot.KnownModel, len(fixture.snapshot.Models))
	for _, row := range fixture.snapshot.Models {
		rowsByKey[snapshotRowKey(row.Provider, row.ID, row.EndpointName, row.ServerInstance)] = row
	}

	entry := report.Routes[0]
	require.Equal(t, "qwen3.5-27b", entry.Model)
	require.NotNil(t, entry.LastDecision)
	require.Equal(t, "alpha", entry.LastDecision.Provider)
	routeStatusCandidates := make([]RouteCandidateStatus, 0, 2)
	for _, candidate := range entry.Candidates {
		if candidate.Provider == "alpha" || candidate.Provider == "beta" {
			routeStatusCandidates = append(routeStatusCandidates, candidate)
		}
	}
	require.Len(t, routeStatusCandidates, 2)
	for _, candidate := range routeStatusCandidates {
		rowKey := snapshotRowKey(candidate.Provider, candidate.Model, candidate.Endpoint, candidate.ServerInstance)
		row, ok := rowsByKey[rowKey]
		require.True(t, ok, "route-status candidate should match snapshot row: %#v", candidate)
		require.NotNil(t, candidate.QuotaRemaining)
		require.Equal(t, *row.QuotaRemaining, *candidate.QuotaRemaining)
		require.Equal(t, row.RecentP50Latency.Milliseconds(), int64(candidate.RecentLatencyMS))
		require.Equal(t, row.ActualCashSpend, candidate.ActualCashSpend)
		require.Equal(t, row.EffectiveCost, candidate.EffectiveCost)
		require.Equal(t, row.EffectiveCostSource, candidate.EffectiveCostSource)
		require.Equal(t, row.HealthFreshnessSource, candidate.HealthFreshnessSource)
		require.Equal(t, row.QuotaFreshnessSource, candidate.QuotaFreshnessSource)
		require.Equal(t, string(row.DiscoveredVia), candidate.ModelDiscoveryFreshnessSource)
	}
}
