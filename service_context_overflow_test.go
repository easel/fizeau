package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/serviceimpl"
)

func TestExecuteResidualContextOverflowIsCapabilityAndCallerOwnsCrossRouteRetry(t *testing.T) {
	t.Cleanup(replaceRoutingCatalogForTest(t, loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-17T00:00:00Z
catalog_version: residual-context-overflow-test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  shared-model:
    family: fixture
    status: active
    power: 5
    context_window: 8192
`)))

	var alphaCalls, betaCalls atomic.Int64
	alpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "shared-model"}}})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		alphaCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"message": "context length exceeded for the selected model",
			"type":    "invalid_request_error",
			"code":    "context_length_exceeded",
		}})
	}))
	defer alpha.Close()

	beta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "shared-model"}}})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		betaCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-beta", "object": "chat.completion", "created": time.Now().Unix(), "model": "shared-model",
			"choices": []map[string]any{{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "larger route succeeded"}, "finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer beta.Close()

	svc := newTestService(t, ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"alpha": {Type: "openai", BaseURL: alpha.URL + "/v1", APIKey: "test", Model: "shared-model", ContextWindow: 1024, IncludeByDefault: true, IncludeByDefaultSet: true},
				"beta":  {Type: "openai", BaseURL: beta.URL + "/v1", APIKey: "test", Model: "shared-model", ContextWindow: 4096, IncludeByDefault: false, IncludeByDefaultSet: true},
			},
			names: []string{"alpha", "beta"}, defaultName: "alpha", healthCooldown: time.Minute,
		},
		QuotaRefreshContext: canceledRefreshContext(),
	})
	svc.hub = serviceimpl.NewSessionHub()

	firstDecision, firstFinal := executeContextOverflowEvidence(t, svc, ServiceExecuteRequest{
		Prompt:   "fit routing estimate but fail upstream",
		NoStream: true, MaxTokens: 8, Permissions: "unrestricted", Tools: []Tool{}, Timeout: 5 * time.Second,
	})
	if firstDecision == nil || len(firstDecision.Candidates) < 2 {
		t.Fatalf("routing decision candidates = %+v, want ranked trace", firstDecision)
	}
	alphaIndex, betaIndex := -1, -1
	for index, candidate := range firstDecision.Candidates {
		switch candidate.Provider {
		case "alpha":
			alphaIndex = index
		case "beta":
			betaIndex = index
		}
	}
	if alphaIndex < 0 || betaIndex < 0 || alphaIndex >= betaIndex {
		t.Fatalf("ranked candidate trace = %+v, want selected alpha before rejected beta", firstDecision.Candidates)
	}
	if firstFinal == nil || firstFinal.Status != "failed" || firstFinal.RoutingActual == nil {
		t.Fatalf("first final = %+v, want failed route evidence", firstFinal)
	}
	if firstFinal.RoutingActual.Provider != "alpha" || firstFinal.RoutingActual.FailureClass != "capability" {
		t.Fatalf("first routing actual = %+v, want alpha capability", firstFinal.RoutingActual)
	}
	if firstFinal.Cause != TerminalCauseProviderFailed || firstFinal.ContextCapacity != nil {
		t.Fatalf("first terminal cause/capacity = %q/%+v, want provider_failed without service capacity payload", firstFinal.Cause, firstFinal.ContextCapacity)
	}
	if alphaCalls.Load() != 1 || betaCalls.Load() != 0 {
		t.Fatalf("first Execute provider calls alpha/beta = %d/%d, want 1/0", alphaCalls.Load(), betaCalls.Load())
	}

	records := svc.activeRouteAttempts(time.Now(), time.Minute)
	if len(records) != 1 || records[0].Reason != "capability" || records[0].Key.Provider != "alpha" ||
		records[0].Key.ServerInstance == "" || records[0].Key.ServerInstance != firstFinal.RoutingActual.ServerInstance {
		t.Fatalf("route-attempt evidence = %+v, want exact alpha capability", records)
	}
	in := svc.buildRoutingInputs(context.Background())
	svc.applyRouteAttemptCooldowns(&in)
	if len(in.ProviderCooldowns) != 0 || len(in.ProviderUnreachable) != 0 || len(in.ProbeUnreachable) != 0 {
		t.Fatalf("capability created global cooldown/unreachable state: cooldowns=%v unreachable=%v probes=%v", in.ProviderCooldowns, in.ProviderUnreachable, in.ProbeUnreachable)
	}
	foundExactAlpha := false
	for key := range in.ExactRouteCooldowns {
		if key.Harness == "fiz" && key.Provider == "alpha" && key.Model == "shared-model" && key.ServerInstance == firstFinal.RoutingActual.ServerInstance {
			foundExactAlpha = true
		}
	}
	if !foundExactAlpha {
		t.Fatalf("exact route cooldowns = %v, want alpha capability feedback", in.ExactRouteCooldowns)
	}

	secondDecision, second := executeContextOverflowEvidence(t, svc, ServiceExecuteRequest{
		Prompt: "retry on caller-selected larger route", Harness: "fiz", Provider: "beta", Model: "shared-model",
		NoStream: true, MaxTokens: 8, Permissions: "unrestricted", Tools: []Tool{}, Timeout: 5 * time.Second,
	})
	if secondDecision == nil || secondDecision.ContextLength != 4096 || secondDecision.ContextLength <= firstDecision.ContextLength {
		t.Fatalf("second routing decision = %+v, want larger 4096-token route than %d", secondDecision, firstDecision.ContextLength)
	}
	if second == nil || second.Status != "success" || second.RoutingActual == nil || second.RoutingActual.Provider != "beta" {
		t.Fatalf("caller-owned beta retry final = %+v, want success on beta", second)
	}
	if alphaCalls.Load() != 1 || betaCalls.Load() != 1 {
		t.Fatalf("two Execute calls alpha/beta = %d/%d, want 1/1", alphaCalls.Load(), betaCalls.Load())
	}
}

func executeContextOverflowEvidence(t *testing.T, svc FizeauService, req ServiceExecuteRequest) (*ServiceRoutingDecisionData, *ServiceFinalData) {
	t.Helper()
	events, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var decision *ServiceRoutingDecisionData
	var final *ServiceFinalData
	for event := range events {
		decoded, decodeErr := DecodeServiceEvent(event)
		if decodeErr != nil {
			t.Fatalf("DecodeServiceEvent(%q): %v", event.Type, decodeErr)
		}
		if decoded.RoutingDecision != nil {
			copy := *decoded.RoutingDecision
			decision = &copy
		}
		if decoded.Final != nil {
			copy := *decoded.Final
			final = &copy
		}
	}
	return decision, final
}
