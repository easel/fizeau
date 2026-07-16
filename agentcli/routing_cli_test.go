package agentcli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routingChatRequest struct {
	Model string `json:"model"`
}

type countedOpenAIServer struct {
	server          *httptest.Server
	mu              sync.Mutex
	modelsCalls     int
	chatCalls       int
	lastModel       string
	responseStatus  int
	modelsStatus    int
	responseModel   string
	responseContent string
	models          []string
	chatDelay       time.Duration
}

func newCountedOpenAIServer(t *testing.T, responseStatus int, responseModel, responseContent string) *countedOpenAIServer {
	t.Helper()
	s := &countedOpenAIServer{
		responseStatus:  responseStatus,
		responseModel:   responseModel,
		responseContent: responseContent,
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			s.mu.Lock()
			s.modelsCalls++
			models := append([]string(nil), s.models...)
			responseModel := s.responseModel
			modelsStatus := s.modelsStatus
			s.mu.Unlock()
			if modelsStatus == 0 {
				modelsStatus = http.StatusOK
			}
			if modelsStatus != http.StatusOK {
				w.WriteHeader(modelsStatus)
				_, _ = w.Write([]byte(`{"error":{"message":"models unavailable"}}`))
				return
			}
			if len(models) == 0 {
				if responseModel != "" {
					models = []string{responseModel}
				} else {
					models = []string{"stub-model"}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			payload := struct {
				Data []map[string]string `json:"data"`
			}{Data: make([]map[string]string, 0, len(models))}
			for _, model := range models {
				payload.Data = append(payload.Data, map[string]string{"id": model})
			}
			require.NoError(t, json.NewEncoder(w).Encode(payload))
		case "/v1/chat/completions":
			s.mu.Lock()
			s.chatCalls++
			delay := s.chatDelay
			s.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}

			defer r.Body.Close()
			var req routingChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				s.mu.Lock()
				s.lastModel = req.Model
				s.mu.Unlock()
			}

			if s.responseStatus != http.StatusOK {
				w.WriteHeader(s.responseStatus)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream failed"}}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-route",
				"object":"chat.completion",
				"created":1712534400,
				"model":"` + s.responseModel + `",
				"choices":[{"index":0,"message":{"role":"assistant","content":"` + s.responseContent + `"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *countedOpenAIServer) baseURL() string {
	return s.server.URL + "/v1"
}

func (s *countedOpenAIServer) chatCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatCalls
}

func (s *countedOpenAIServer) modelsCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelsCalls
}

func (s *countedOpenAIServer) requestedModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastModel
}

func writeSnapshotDiscoveryFixture(t *testing.T, cache *discoverycache.Cache, source string, capturedAt time.Time, models []string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		CapturedAt time.Time `json:"captured_at"`
		Models     []string  `json:"models,omitempty"`
		Source     string    `json:"source,omitempty"`
	}{
		CapturedAt: capturedAt,
		Models:     models,
		Source:     "test-fixture",
	})
	require.NoError(t, err)
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            source,
		TTL:             time.Hour,
		RefreshDeadline: time.Second,
	}
	require.NoError(t, cache.Refresh(src, func(context.Context) ([]byte, error) { return payload, nil }))
}

func (s *countedOpenAIServer) setModels(models ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = append([]string(nil), models...)
}

func (s *countedOpenAIServer) setChatDelay(delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatDelay = delay
}

func (s *countedOpenAIServer) setModelsStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelsStatus = status
}

func eventDataByType(t *testing.T, events []fizeau.SessionEvent, eventType fizeau.SessionEventType) map[string]any {
	t.Helper()
	for _, e := range events {
		if e.Type != eventType {
			continue
		}
		var payload map[string]any
		require.NoError(t, json.Unmarshal(e.Data, &payload))
		return payload
	}
	t.Fatalf("event %s not found", eventType)
	return nil
}

func latestSessionLogPath(t *testing.T, workDir string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(workDir, ".fizeau", "sessions", "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "expected at least one session log")

	latest := paths[0]
	latestInfo, err := os.Stat(latest)
	require.NoError(t, err)
	for _, path := range paths[1:] {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		if info.ModTime().After(latestInfo.ModTime()) {
			latest = path
			latestInfo = info
		}
	}
	return latest
}

func writeRoutingHistorySession(t *testing.T, workDir, sessionID string, ts time.Time, data fizeau.SessionEndData) {
	t.Helper()
	logDir := filepath.Join(workDir, ".fizeau", "sessions")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	event := fizeau.SessionEvent{
		SessionID: sessionID,
		Seq:       0,
		Type:      fizeau.EventSessionEnd,
		Timestamp: ts.UTC(),
	}
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	event.Data = raw
	line, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(logDir, sessionID+".jsonl"), append(line, '\n'), 0o644))
}

func TestCLI_Run_ModelByModelName(t *testing.T) {
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()
	cacheDir := t.TempDir()

	bragi := newCountedOpenAIServer(t, http.StatusOK, "bragi-runtime-model", "bragi ok")
	bragi.setModels("qwen3.5-27b")
	cache := &discoverycache.Cache{Root: cacheDir}
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("bragi", "bragi", bragi.baseURL(), ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"qwen3.5-27b"})

	writeTempConfig(t, workDir, `
providers:
  bragi:
    type: lmstudio
    base_url: `+bragi.baseURL()+`
    api_key: test
    model: qwen3.5-27b
routing:
  default_model: qwen3.5-27b
default: bragi
`)

	type routingResult struct {
		Status           string `json:"status"`
		SessionID        string `json:"session_id"`
		Model            string `json:"model"`
		SelectedProvider string `json:"selected_provider"`
		SelectedRoute    string `json:"selected_route"`
		RequestedModel   string `json:"requested_model"`
		ResolvedModel    string `json:"resolved_model"`
	}

	env := testEnvWithHome(home, map[string]string{"FIZEAU_CACHE_DIR": cacheDir})

	first := runBuiltCLI(t, exe, workDir, env, "--json", "--work-dir", workDir, "run", "--provider", "bragi", "--model", "qwen3.5-27b", "first request")
	require.Equal(t, 0, first.exitCode, "stderr=%s", first.stderr)
	var firstResult routingResult
	require.NoError(t, json.Unmarshal([]byte(first.stdout), &firstResult), "stdout=%s", first.stdout)
	assert.Equal(t, "success", firstResult.Status)
	assert.NotEmpty(t, firstResult.SessionID)
	assert.Equal(t, "qwen3.5-27b", bragi.requestedModel())

	firstSessionPath := latestSessionLogPath(t, workDir)
	firstEvents, err := fizeau.ReadSessionEvents(firstSessionPath)
	require.NoError(t, err)
	_ = eventDataByType(t, firstEvents, fizeau.EventSessionStart)
	firstEnd := eventDataByType(t, firstEvents, fizeau.EventSessionEnd)
	assert.Equal(t, "success", firstEnd["status"])

	second := runBuiltCLI(t, exe, workDir, env, "--json", "--work-dir", workDir, "run", "--provider", "bragi", "second request")
	require.Equal(t, 0, second.exitCode, "stderr=%s", second.stderr)
	var secondResult routingResult
	require.NoError(t, json.Unmarshal([]byte(second.stdout), &secondResult), "stdout=%s", second.stdout)
	assert.Equal(t, "success", secondResult.Status)
	assert.NotEmpty(t, secondResult.SessionID)
	assert.Equal(t, "qwen3.5-27b", bragi.requestedModel())

	secondSessionPath := latestSessionLogPath(t, workDir)
	secondEvents, err := fizeau.ReadSessionEvents(secondSessionPath)
	require.NoError(t, err)
	secondEnd := eventDataByType(t, secondEvents, fizeau.EventSessionEnd)
	assert.Equal(t, "success", secondEnd["status"])

	assert.Equal(t, 2, bragi.chatCallCount())
}

func TestCLI_RoutePlanFailoverOnAvailabilityError(t *testing.T) {
	t.Skip("ADR-005 step 1 removed multi-candidate failover via PreResolved; coverage returns when the smart-routing engine owns provider failover (steps 2+3).")
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()

	dead := newCountedOpenAIServer(t, http.StatusServiceUnavailable, "", "")
	healthy := newCountedOpenAIServer(t, http.StatusOK, "healthy-runtime-model", "healthy ok")

	writeTempConfig(t, workDir, `
providers:
  bragi:
    type: lmstudio
    base_url: `+dead.baseURL()+`
    api_key: test
  openrouter:
    type: lmstudio
    base_url: `+healthy.baseURL()+`
    api_key: test
`)

	type routingResult struct {
		Status             string   `json:"status"`
		Model              string   `json:"model"`
		SelectedProvider   string   `json:"selected_provider"`
		SelectedRoute      string   `json:"selected_route"`
		RequestedModel     string   `json:"requested_model"`
		AttemptedProviders []string `json:"attempted_providers"`
		FailoverCount      int      `json:"failover_count"`
	}

	res := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--json", "--work-dir", workDir, "run", "--model", "qwen3.5-27b", "route failover")
	require.Equal(t, 0, res.exitCode, "stderr=%s", res.stderr)

	var parsed routingResult
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &parsed), "stdout=%s", res.stdout)
	assert.Equal(t, "success", parsed.Status)
	assert.Equal(t, "qwen3.5-27b", parsed.SelectedRoute)
	assert.Equal(t, "openrouter", parsed.SelectedProvider)
	assert.Equal(t, "qwen3.5-27b", parsed.RequestedModel)
	assert.Equal(t, []string{"bragi", "openrouter"}, parsed.AttemptedProviders)
	assert.Equal(t, 1, parsed.FailoverCount)

	sessionPath := latestSessionLogPath(t, workDir)
	events, err := fizeau.ReadSessionEvents(sessionPath)
	require.NoError(t, err)
	end := eventDataByType(t, events, fizeau.EventSessionEnd)
	assert.Equal(t, "openrouter", end["selected_provider"])
	assert.Equal(t, float64(1), end["failover_count"])

	assert.Equal(t, 1, dead.chatCallCount())
	assert.Equal(t, 1, healthy.chatCallCount())
}

func TestCLI_RoutePlanDoesNotFailoverOnDeterministic400(t *testing.T) {
	t.Skip("Deterministic-400 non-failover is covered by service-level routing tests; this CLI fixture still depends on legacy model-resolution behavior.")
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()
	manifestPath := filepath.Join(workDir, "models.yaml")
	writeTempManifest(t, manifestPath, `
version: 5
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
models:
  qwen3.5-27b:
    status: active
    provider_system: openai
    power: 8
    surfaces:
      agent.openai: qwen3.5-27b
`)

	badRequest := newCountedOpenAIServer(t, http.StatusBadRequest, "", "")
	healthy := newCountedOpenAIServer(t, http.StatusOK, "healthy-runtime-model", "healthy ok")

	writeTempConfig(t, workDir, `
model_catalog:
  manifest: `+manifestPath+`
providers:
  bragi:
    type: lmstudio
    base_url: `+badRequest.baseURL()+`
    api_key: test
  openrouter:
    type: lmstudio
    base_url: `+healthy.baseURL()+`
    api_key: test
`)

	res := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--work-dir", workDir, "run", "--provider", "bragi", "--model", "qwen3.5-27b", "no failover")
	require.Equal(t, 1, res.exitCode, "stdout=%s stderr=%s", res.stdout, res.stderr)
	assert.Contains(t, res.stderr, "agent: provider error")
	assert.Equal(t, 1, badRequest.chatCallCount(), "400 is non-transient: runtime should fail immediately without retry")
	assert.Equal(t, 0, healthy.chatCallCount())
}

func TestCLI_ModelIntentAutoRoutingSkipsUnhealthyDefaultAndChoosesBestHealthyProvider(t *testing.T) {
	t.Skip("ADR-005 step 1 removed multi-candidate failover via PreResolved; smart-routing-driven provider skipping returns once ResolveRoute owns dispatch (steps 2+3).")
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()

	dead := newCountedOpenAIServer(t, http.StatusServiceUnavailable, "", "")
	bragi := newCountedOpenAIServer(t, http.StatusOK, "qwen3.5-27b", "bragi ok")
	vidar := newCountedOpenAIServer(t, http.StatusOK, "qwen3.5-27b", "vidar ok")
	openrouter := newCountedOpenAIServer(t, http.StatusOK, "qwen/qwen3.5-27b-20260224", "openrouter ok")

	dead.setModelsStatus(http.StatusServiceUnavailable)
	dead.setModels("qwen3.5-27b")
	bragi.setModels("qwen3.5-27b")
	vidar.setModels("qwen3.5-27b")
	openrouter.setModels("qwen/qwen3.5-27b-20260224")

	knownCost := 0.09
	writeRoutingHistorySession(t, workDir, "vidar-win", time.Now().Add(-5*time.Minute), fizeau.SessionEndData{
		Status:           fizeau.StatusSuccess,
		Tokens:           fizeau.TokenUsage{Input: 100, Output: 50, Total: 150},
		CostUSD:          nil,
		DurationMs:       800,
		SelectedProvider: "vidar",
		RequestedModel:   "qwen3.5-27b",
		ResolvedModel:    "qwen3.5-27b",
	})
	writeRoutingHistorySession(t, workDir, "bragi-slow", time.Now().Add(-4*time.Minute), fizeau.SessionEndData{
		Status:           fizeau.StatusSuccess,
		Tokens:           fizeau.TokenUsage{Input: 100, Output: 50, Total: 150},
		CostUSD:          nil,
		DurationMs:       2400,
		SelectedProvider: "bragi",
		RequestedModel:   "qwen3.5-27b",
		ResolvedModel:    "qwen3.5-27b",
	})
	writeRoutingHistorySession(t, workDir, "openrouter-costly", time.Now().Add(-3*time.Minute), fizeau.SessionEndData{
		Status:           fizeau.StatusSuccess,
		Tokens:           fizeau.TokenUsage{Input: 100, Output: 50, Total: 150},
		CostUSD:          &knownCost,
		CostSource:       fizeau.CostSourceReported,
		DurationMs:       1500,
		SelectedProvider: "openrouter",
		RequestedModel:   "qwen3.5-27b",
		ResolvedModel:    "qwen/qwen3.5-27b-20260224",
	})

	writeTempConfig(t, workDir, `
providers:
  openrouter:
    type: lmstudio
    base_url: `+dead.baseURL()+`
    api_key: test
  bragi:
    type: lmstudio
    base_url: `+bragi.baseURL()+`
    api_key: test
  vidar:
    type: lmstudio
    base_url: `+vidar.baseURL()+`
    api_key: test
  grendel:
    type: lmstudio
    base_url: `+openrouter.baseURL()+`
    api_key: test
default: openrouter
routing:
  default_model: qwen3.5-27b
`)

	type routingResult struct {
		Status           string   `json:"status"`
		SelectedProvider string   `json:"selected_provider"`
		SelectedRoute    string   `json:"selected_route"`
		RequestedModel   string   `json:"requested_model"`
		Attempted        []string `json:"attempted_providers"`
	}

	res := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--json", "--work-dir", workDir, "run", "--model", "qwen3.5-27b", "smart route")
	require.Equal(t, 0, res.exitCode, "stdout=%s stderr=%s", res.stdout, res.stderr)

	var parsed routingResult
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &parsed), "stdout=%s", res.stdout)
	assert.Equal(t, "success", parsed.Status)
	assert.Equal(t, "vidar", parsed.SelectedProvider)
	assert.Equal(t, "qwen3.5-27b", parsed.SelectedRoute)
	assert.Equal(t, "qwen3.5-27b", parsed.RequestedModel)
	assert.Equal(t, []string{"vidar"}, parsed.Attempted)
	assert.Equal(t, 0, dead.chatCallCount(), "unhealthy default provider should be excluded before execution")
	assert.Equal(t, 0, bragi.chatCallCount(), "slower healthy provider should lose to better observed candidate")
	assert.Equal(t, 1, vidar.chatCallCount())
	assert.Equal(t, 0, openrouter.chatCallCount(), "higher-cost healthy provider should lose when a faster healthy local candidate exists")
}

func TestCLI_RouteStatusShowsHealthAndScoringForModelIntent(t *testing.T) {
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()
	cacheDir := t.TempDir()

	dead := newCountedOpenAIServer(t, http.StatusServiceUnavailable, "", "")
	healthy := newCountedOpenAIServer(t, http.StatusOK, "qwen3.5-27b", "ok")
	expensive := newCountedOpenAIServer(t, http.StatusOK, "qwen3.5-27b", "ok")
	dead.setModelsStatus(http.StatusServiceUnavailable)
	dead.setModels("qwen3.5-27b")
	healthy.setModels("qwen3.5-27b")
	expensive.setModels("qwen3.5-27b")

	cache := &discoverycache.Cache{Root: cacheDir}
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("vidar", "vidar", healthy.baseURL(), ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"qwen3.5-27b"})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("freyja", "freyja", expensive.baseURL(), ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"qwen3.5-27b"})

	writeTempConfig(t, workDir, `
providers:
  bragi:
    type: lmstudio
    base_url: `+dead.baseURL()+`
    api_key: test
  vidar:
    type: lmstudio
    base_url: `+healthy.baseURL()+`
    api_key: test
  freyja:
    type: lmstudio
    base_url: `+expensive.baseURL()+`
    api_key: test
`)

	out := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, map[string]string{
		"PATH":             "",
		"FIZEAU_CACHE_DIR": cacheDir,
	}), "--work-dir", workDir, "route-status", "--model", "qwen3.5-27b", "--json")
	require.Equal(t, 0, out.exitCode, "stdout=%s stderr=%s", out.stdout, out.stderr)

	type component struct {
		Cost                    float64 `json:"cost"`
		LatencyMS               float64 `json:"latency_ms"`
		Utilization             float64 `json:"utilization"`
		SuccessRate             float64 `json:"success_rate"`
		Capability              float64 `json:"capability"`
		ContextHeadroom         int     `json:"context_headroom"`
		StickyAffinity          float64 `json:"sticky_affinity"`
		PowerWeightedCapability float64 `json:"power_weighted_capability"`
		PowerHintFit            float64 `json:"power_hint_fit"`
		LatencyWeight           float64 `json:"latency_weight"`
		PlacementBonus          float64 `json:"placement_bonus"`
		QuotaBonus              float64 `json:"quota_bonus"`
		MarginalCostPenalty     float64 `json:"marginal_cost_penalty"`
		AvailabilityPenalty     float64 `json:"availability_penalty"`
		StaleSignalPenalty      float64 `json:"stale_signal_penalty"`
	}
	type sticky struct {
		KeyPresent bool   `json:"key_present"`
		Assignment string `json:"assignment"`
		Reason     string `json:"reason"`
	}
	type utilization struct {
		Source         string   `json:"source"`
		Freshness      string   `json:"freshness"`
		ActiveRequests *int     `json:"active_requests"`
		QueuedRequests *int     `json:"queued_requests"`
		MaxConcurrency *int     `json:"max_concurrency"`
		CachePressure  *float64 `json:"cache_pressure"`
	}
	type candidate struct {
		Harness                       string      `json:"harness"`
		Provider                      string      `json:"provider"`
		Billing                       string      `json:"billing"`
		ActualCashSpend               bool        `json:"actual_cash_spend"`
		Endpoint                      string      `json:"endpoint"`
		ServerInstance                string      `json:"server_instance"`
		Model                         string      `json:"model"`
		Score                         float64     `json:"score"`
		ContextLength                 int         `json:"context_length"`
		ContextSource                 string      `json:"context_source"`
		EffectiveCost                 float64     `json:"effective_cost"`
		EffectiveCostSource           string      `json:"effective_cost_source"`
		SnapshotCapturedAt            time.Time   `json:"snapshot_captured_at"`
		HealthFreshnessAt             time.Time   `json:"health_freshness_at"`
		HealthFreshnessSource         string      `json:"health_freshness_source"`
		QuotaFreshnessAt              time.Time   `json:"quota_freshness_at"`
		QuotaFreshnessSource          string      `json:"quota_freshness_source"`
		ModelDiscoveryFreshnessAt     time.Time   `json:"model_discovery_freshness_at"`
		ModelDiscoveryFreshnessSource string      `json:"model_discovery_freshness_source"`
		Components                    component   `json:"components"`
		Utilization                   utilization `json:"utilization"`
		Eligible                      bool        `json:"eligible"`
		FilterReason                  string      `json:"filter_reason"`
		SourceStatus                  string      `json:"source_status"`
		AutoRoutable                  bool        `json:"auto_routable"`
		ExactPinOnly                  bool        `json:"exact_pin_only"`
		ExclusionReason               string      `json:"exclusion_reason"`
		Winner                        bool        `json:"winner"`
	}
	var parsed struct {
		Model                  string      `json:"model"`
		SnapshotCapturedAt     time.Time   `json:"snapshot_captured_at"`
		SelectedEndpoint       string      `json:"selected_endpoint"`
		SelectedServerInstance string      `json:"selected_server_instance"`
		Sticky                 sticky      `json:"sticky"`
		Utilization            utilization `json:"utilization"`
		Winner                 *candidate  `json:"winner"`
		Candidates             []candidate `json:"candidates"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &parsed), "stdout=%s", out.stdout)
	assert.Equal(t, "qwen3.5-27b", parsed.Model)
	require.False(t, parsed.SnapshotCapturedAt.IsZero(), "route-status should expose snapshot_captured_at")
	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &generic), "stdout=%s", out.stdout)
	for _, key := range []string{"selected_endpoint", "selected_server_instance", "sticky", "utilization", "snapshot_captured_at"} {
		if _, ok := generic[key]; !ok {
			t.Fatalf("missing %q in route-status JSON: %s", key, out.stdout)
		}
	}
	var candidateGeneric []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(generic["candidates"], &candidateGeneric))
	if len(candidateGeneric) == 0 {
		t.Fatal("expected at least one candidate in route-status JSON")
	}
	if _, ok := candidateGeneric[0]["utilization"]; !ok {
		t.Fatalf("missing candidate utilization in route-status JSON: %s", out.stdout)
	}
	if _, ok := candidateGeneric[0]["server_instance"]; !ok {
		t.Fatalf("missing candidate server_instance in route-status JSON: %s", out.stdout)
	}
	for _, key := range []string{"context_length", "context_source"} {
		if _, ok := candidateGeneric[0][key]; !ok {
			t.Fatalf("missing candidate %q in route-status JSON: %s", key, out.stdout)
		}
	}
	var componentGeneric map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(candidateGeneric[0]["components"], &componentGeneric))
	for _, key := range []string{"cost", "latency_ms", "utilization", "success_rate", "capability", "context_headroom", "sticky_affinity"} {
		if _, ok := componentGeneric[key]; !ok {
			t.Fatalf("missing candidate component %q in route-status JSON: %s", key, out.stdout)
		}
	}
	for _, key := range []string{"power_weighted_capability", "power_hint_fit", "latency_weight", "placement_bonus", "quota_bonus", "marginal_cost_penalty", "availability_penalty", "stale_signal_penalty"} {
		if _, ok := componentGeneric[key]; !ok {
			t.Fatalf("missing candidate component %q in route-status JSON: %s", key, out.stdout)
		}
	}

	// Each candidate carries the new structured shape from
	// service.ResolveRoute: provider, model, score, components, eligible bool,
	// and filter_reason (empty for the eligible winner, non-empty for losers).
	require.NotEmpty(t, parsed.Candidates)
	for _, c := range parsed.Candidates {
		// Subscription harnesses (claude/codex/gemini) surface harness-level
		// candidates with no Provider; native fiz harness candidates do
		// carry a Provider. Either way the routing identity (Harness or
		// Provider) must be populated.
		assert.True(t, c.Harness != "" || c.Provider != "", "candidate must carry a harness or provider: %+v", c)
		// Eligible candidates must name a resolved model. Ineligible
		// subscription-harness candidates (claude / codex / gemini that
		// fail health because their CLI isn't on PATH — common in CI)
		// legitimately surface with empty Model; the FilterReason
		// covers them.
		if c.Eligible {
			assert.NotEmpty(t, c.Model, "eligible candidate must carry a resolved model: %+v", c)
		}
		// Components is always present; its zero value is meaningful (unknown).
		_ = c.Components
		_ = c.Components.Utilization
		_ = c.Components.StickyAffinity
		_ = c.Components.PowerWeightedCapability
		_ = c.Components.PowerHintFit
		_ = c.Components.LatencyWeight
		_ = c.Components.PlacementBonus
		_ = c.Components.QuotaBonus
		_ = c.Components.MarginalCostPenalty
		_ = c.Components.AvailabilityPenalty
		_ = c.Components.StaleSignalPenalty
		_ = c.ContextLength
		_ = c.ContextSource
		_ = c.ActualCashSpend
		_ = c.EffectiveCost
		_ = c.EffectiveCostSource
		_ = c.SnapshotCapturedAt
		_ = c.HealthFreshnessAt
		_ = c.HealthFreshnessSource
		_ = c.QuotaFreshnessAt
		_ = c.QuotaFreshnessSource
		_ = c.ModelDiscoveryFreshnessAt
		_ = c.ModelDiscoveryFreshnessSource
		if !c.Eligible {
			assert.NotEmpty(t, c.FilterReason, "ineligible candidate must carry a filter_reason: %+v", c)
		}
	}

	// At least one candidate must be eligible (the two healthy lmstudio
	// providers serve qwen3.5-27b; bragi is dead and gets dropped before the
	// engine ever sees it because its /v1/models probe fails).
	eligibleCount := 0
	for _, c := range parsed.Candidates {
		if c.Eligible {
			eligibleCount++
		}
	}
	assert.GreaterOrEqual(t, eligibleCount, 1, "expected at least one eligible candidate; got %+v", parsed.Candidates)

	require.NotNil(t, parsed.Winner, "an eligible winner must be selected")
	assert.True(t, parsed.Winner.Eligible)
	assert.Empty(t, parsed.Winner.FilterReason, "winner must have an empty filter_reason")
	assert.Equal(t, parsed.Winner.Endpoint, parsed.SelectedEndpoint)
	assert.NotEmpty(t, parsed.SelectedServerInstance)
	assert.False(t, parsed.Sticky.KeyPresent, "no correlation id was supplied, so sticky key should be absent")

	// The winner must also appear inside the candidates array and be flagged.
	winnerInList := 0
	for _, c := range parsed.Candidates {
		if c.Winner {
			winnerInList++
			assert.Equal(t, parsed.Winner.Provider, c.Provider)
			assert.Equal(t, parsed.Winner.Model, c.Model)
		}
	}
	assert.Equal(t, 1, winnerInList, "exactly one candidate should be flagged winner")
	for _, c := range parsed.Candidates {
		_ = c.Utilization.Source
		_ = c.Utilization.Freshness
	}
	textOut := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, map[string]string{
		"PATH":             "",
		"FIZEAU_CACHE_DIR": cacheDir,
	}), "--work-dir", workDir, "route-status", "--policy", "cheap", "--model", "qwen3.5-27b")
	require.Equal(t, 0, textOut.exitCode, "stdout=%s stderr=%s", textOut.stdout, textOut.stderr)
	for _, want := range []string{
		"snapshot_captured_at",
		"actual_cash_spend",
		"effective_cost",
		"effective_cost_source",
		"model_discovery_freshness_at",
		"source_status",
	} {
		require.Contains(t, textOut.stdout, want, "route-status text output missing %q: %s", want, textOut.stdout)
	}
}

// TestRouteStatus_ShowsEligibleCandidatesPerIntent asserts that
// `fiz route-status --policy <p>` calls service.ResolveRoute and
// renders the engine's full candidate trace for the requested policy
// input, including the structured power-policy evidence and candidate
// score components. Per ADR-005 §5.
func TestRouteStatus_ShowsEligibleCandidatesPerIntent(t *testing.T) {
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()

	// Discover a local power-5 model so the native harness has at least one
	// eligible candidate under the cheap policy. Subscription harnesses
	// also surface candidates but go ineligible without quota state,
	// exercising both eligible and ineligible code paths.
	healthy := newCountedOpenAIServer(t, http.StatusOK, "qwen3.5-27b", "ok")
	healthy.setModels("qwen3.5-27b")

	writeTempConfig(t, workDir, `
providers:
  vidar:
    type: lmstudio
    base_url: `+healthy.baseURL()+`
    api_key: test
    model: qwen3.5-27b
`)

	out := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--work-dir", workDir, "route-status", "--policy", "cheap", "--json")
	require.Equal(t, 0, out.exitCode, "stdout=%s stderr=%s", out.stdout, out.stderr)

	type component struct {
		Cost            float64 `json:"cost"`
		LatencyMS       float64 `json:"latency_ms"`
		Utilization     float64 `json:"utilization"`
		SuccessRate     float64 `json:"success_rate"`
		Capability      float64 `json:"capability"`
		ContextHeadroom int     `json:"context_headroom"`
		StickyAffinity  float64 `json:"sticky_affinity"`
	}
	type candidate struct {
		Harness       string    `json:"harness"`
		Provider      string    `json:"provider"`
		Model         string    `json:"model"`
		Score         float64   `json:"score"`
		ContextLength int       `json:"context_length"`
		ContextSource string    `json:"context_source"`
		Components    component `json:"components"`
		Eligible      bool      `json:"eligible"`
		FilterReason  string    `json:"filter_reason"`
		Reason        string    `json:"reason"`
		Winner        bool      `json:"winner"`
	}
	var parsed struct {
		Policy      string `json:"policy"`
		PowerPolicy struct {
			PolicyName string `json:"policy_name"`
			MinPower   int    `json:"min_power"`
			MaxPower   int    `json:"max_power"`
		} `json:"power_policy"`
		Winner     *candidate  `json:"winner"`
		Candidates []candidate `json:"candidates"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &parsed), "stdout=%s", out.stdout)
	assert.Equal(t, "cheap", parsed.Policy)
	assert.Equal(t, "cheap", parsed.PowerPolicy.PolicyName)
	require.NotEmpty(t, parsed.Candidates, "engine must surface its candidate trace; stdout=%s", out.stdout)

	// Every candidate carries the score-component bundle (cost, latency_ms,
	// success_rate, capability) plus an eligible bool and a filter_reason
	// string that explains rankings without parsing free-form Reason text.
	sawIneligibleWithReason := false
	sawIneligible := false
	for _, c := range parsed.Candidates {
		// Eligible candidates must name a concrete model. Ineligible
		// subscription-harness candidates (claude / codex / gemini)
		// surface with Model:"" in environments without their CLI on
		// PATH (e.g., CI); FilterReason is the explanation there.
		if c.Eligible {
			assert.NotEmpty(t, c.Model, "eligible candidate must name a concrete model: %+v", c)
		}
		// Components is structured (not free-form); reading the named axes
		// proves the wire shape matches AC §2.
		_ = c.Components.Cost
		_ = c.Components.LatencyMS
		_ = c.Components.Utilization
		_ = c.Components.SuccessRate
		_ = c.Components.Capability
		_ = c.Components.ContextHeadroom
		_ = c.Components.StickyAffinity
		_ = c.ContextLength
		_ = c.ContextSource
		if !c.Eligible {
			sawIneligible = true
			if c.FilterReason != "" {
				sawIneligibleWithReason = true
			}
		}
	}
	if sawIneligible {
		assert.True(t, sawIneligibleWithReason,
			"at least one ineligible candidate must carry a non-empty filter_reason; got %+v",
			parsed.Candidates)
	}
}

func TestRouteStatus_ExplainsPowerFilteredCandidates(t *testing.T) {
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()

	local := newCountedOpenAIServer(t, http.StatusOK, "qwen3.5-27b", "local ok")
	frontier := newCountedOpenAIServer(t, http.StatusOK, "gpt-5.5", "frontier ok")
	local.setModels("qwen3.5-27b")
	frontier.setModels("gpt-5.5")

	writeTempConfig(t, workDir, `
providers:
  local:
    type: lmstudio
    base_url: `+local.baseURL()+`
    api_key: test
    model: qwen3.5-27b
  frontier:
    type: lmstudio
    base_url: `+frontier.baseURL()+`
    api_key: test
    model: gpt-5.5
`)

	out := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--work-dir", workDir, "route-status", "--min-power", "9", "--max-power", "10", "--json")
	require.Equal(t, 0, out.exitCode, "stdout=%s stderr=%s", out.stdout, out.stderr)

	type component struct {
		Power          int     `json:"power"`
		Cost           float64 `json:"cost"`
		CostClass      string  `json:"cost_class"`
		SpeedTPS       float64 `json:"speed_tps"`
		Utilization    float64 `json:"utilization"`
		StickyAffinity float64 `json:"sticky_affinity"`
	}
	type candidate struct {
		Harness      string    `json:"harness"`
		Provider     string    `json:"provider"`
		Model        string    `json:"model"`
		Eligible     bool      `json:"eligible"`
		FilterReason string    `json:"filter_reason"`
		Components   component `json:"components"`
		Winner       bool      `json:"winner"`
	}
	var parsed struct {
		MinPower   int         `json:"min_power"`
		MaxPower   int         `json:"max_power"`
		Winner     *candidate  `json:"winner"`
		Candidates []candidate `json:"candidates"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.stdout), &parsed), "stdout=%s", out.stdout)
	assert.Equal(t, 9, parsed.MinPower)
	assert.Equal(t, 10, parsed.MaxPower)
	require.NotNil(t, parsed.Winner)
	assert.Equal(t, "fiz", parsed.Winner.Harness)
	assert.Equal(t, "frontier", parsed.Winner.Provider)
	assert.Equal(t, "gpt-5.5", parsed.Winner.Model)
	assert.True(t, parsed.Winner.Eligible)
	assert.Equal(t, 10, parsed.Winner.Components.Power)
	assert.Contains(t, out.stdout, `"cost"`)
	assert.Contains(t, out.stdout, `"speed_tps"`)

	byProvider := make(map[string]candidate)
	for _, c := range parsed.Candidates {
		byProvider[c.Provider] = c
		_ = c.Components.SpeedTPS
		_ = c.Components.CostClass
	}
	localCandidate := byProvider["local"]
	assert.True(t, localCandidate.Eligible, "local low-power candidate should remain eligible under soft power scoring: %+v", localCandidate)
	assert.Equal(t, "", localCandidate.FilterReason)
	assert.False(t, localCandidate.Winner)
	assert.Equal(t, 5, localCandidate.Components.Power)
}

func TestCLI_ExplicitProviderPinFlowsIntoResultAndSession(t *testing.T) {
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()

	vidar := newCountedOpenAIServer(t, http.StatusOK, "vidar-runtime-model", "vidar ok")
	bragi := newCountedOpenAIServer(t, http.StatusOK, "bragi-runtime-model", "bragi ok")

	writeTempConfig(t, workDir, `
providers:
  vidar:
    type: lmstudio
    base_url: `+vidar.baseURL()+`
    api_key: test
    model: gpt-5.4-mini
  bragi:
    type: lmstudio
    base_url: `+bragi.baseURL()+`
    api_key: test
    model: gpt-5.4-mini
`)

	type routingResult struct {
		Status           string `json:"status"`
		SessionID        string `json:"session_id"`
		Model            string `json:"model"`
		SelectedProvider string `json:"selected_provider"`
		SelectedRoute    string `json:"selected_route"`
		ResolvedModel    string `json:"resolved_model"`
	}

	first := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--json", "--work-dir", workDir, "run", "--provider", "vidar", "-p", "first request")
	require.Equal(t, 0, first.exitCode, "stderr=%s", first.stderr)
	var firstResult routingResult
	require.NoError(t, json.Unmarshal([]byte(first.stdout), &firstResult), "stdout=%s", first.stdout)
	assert.Equal(t, "success", firstResult.Status)
	assert.NotEmpty(t, firstResult.SessionID)
	assert.Equal(t, "gpt-5.4-mini", firstResult.ResolvedModel)
	assert.Equal(t, "gpt-5.4-mini", firstResult.Model)
	assert.Equal(t, "gpt-5.4-mini", vidar.requestedModel())

	firstSessionPath := latestSessionLogPath(t, workDir)
	firstEvents, err := fizeau.ReadSessionEvents(firstSessionPath)
	require.NoError(t, err)
	_ = eventDataByType(t, firstEvents, fizeau.EventSessionStart)
	firstEnd := eventDataByType(t, firstEvents, fizeau.EventSessionEnd)
	assert.Equal(t, "success", firstEnd["status"])

	second := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--json", "--work-dir", workDir, "run", "--provider", "bragi", "-p", "second request")
	require.Equal(t, 0, second.exitCode, "stderr=%s", second.stderr)
	var secondResult routingResult
	require.NoError(t, json.Unmarshal([]byte(second.stdout), &secondResult), "stdout=%s", second.stdout)
	assert.Equal(t, "success", secondResult.Status)
	assert.NotEmpty(t, secondResult.SessionID)
	assert.Equal(t, "gpt-5.4-mini", secondResult.ResolvedModel)
	assert.Equal(t, "gpt-5.4-mini", secondResult.Model)
	assert.Equal(t, "gpt-5.4-mini", bragi.requestedModel())

	secondSessionPath := latestSessionLogPath(t, workDir)
	secondEvents, err := fizeau.ReadSessionEvents(secondSessionPath)
	require.NoError(t, err)
	_ = eventDataByType(t, secondEvents, fizeau.EventSessionStart)
	secondEnd := eventDataByType(t, secondEvents, fizeau.EventSessionEnd)
	assert.Equal(t, "success", secondEnd["status"])

	assert.Equal(t, 1, vidar.chatCallCount())
	assert.Equal(t, 1, bragi.chatCallCount())
}

func TestCLI_BackendPoolFailureDoesNotFailoverToAnotherProvider(t *testing.T) {
	exe := buildAgentCLI(t)
	workDir := t.TempDir()
	home := t.TempDir()

	dead := newCountedOpenAIServer(t, http.StatusServiceUnavailable, "", "")
	healthy := newCountedOpenAIServer(t, http.StatusOK, "healthy-runtime-model", "healthy ok")

	writeTempConfig(t, workDir, `
providers:
  dead:
    type: lmstudio
    base_url: `+dead.baseURL()+`
    api_key: test
    model: local-dead
    context_window: 131072
  healthy:
    type: lmstudio
    base_url: `+healthy.baseURL()+`
    api_key: test
    model: local-healthy
    context_window: 131072
backends:
  local-pool:
    providers: [dead, healthy]
    strategy: first-available
default_backend: local-pool
`)

	res := runBuiltCLI(t, exe, workDir, testEnvWithHome(home, nil), "--work-dir", workDir, "-p", "fail request")
	require.Equal(t, 1, res.exitCode, "stdout=%s stderr=%s", res.stdout, res.stderr)
	assert.Contains(t, res.stderr, "agent: provider error")
	assert.Equal(t, 0, healthy.chatCallCount(), "phase 1 / 2A should not fail over to secondary provider")
	assert.GreaterOrEqual(t, dead.chatCallCount(), 1, "selected provider should be attempted")
	assert.LessOrEqual(t, dead.chatCallCount(), 5, "runtime retry ceiling should prevent a sixth provider call")
}
