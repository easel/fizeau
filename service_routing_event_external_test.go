//go:build testseam

package fizeau_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func TestPublicRoutingDecisionEventProjectsCandidateComponents(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir, err := os.MkdirTemp("", "fizeau-public-routing-event-*")
	if err != nil {
		t.Fatalf("create routing event cache dir: %v", err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 20; attempt++ {
			if err := os.RemoveAll(cacheDir); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("remove routing event cache dir %s", cacheDir)
	})
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	models := externalModelsServer(t, []string{"gpt-5.4-mini"})
	config := &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"public": {
				Type:                "openai",
				BaseURL:             models.URL + "/v1",
				APIKey:              "public-routing-event-test-key",
				Model:               "gpt-5.4-mini",
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
		},
		names:       []string{"public"},
		defaultName: "public",
	}
	opts := fizeau.ServiceOptions{
		ServiceConfig:       config,
		QuotaRefreshContext: canceledPublicRefreshContext(),
	}
	opts.FakeProvider = &fizeau.FakeProvider{Static: []fizeau.FakeResponse{{Text: "done"}}}
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := svc.ListModels(context.Background(), fizeau.ModelFilter{}); err != nil {
		t.Fatalf("prime public model snapshot: %v", err)
	}

	events, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt: "project the selected route",
		Policy: "default",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var decisionEvent *fizeau.ServiceEvent
	for _, event := range drainEvents(t, events, 5*time.Second) {
		if event.Type == fizeau.ServiceEventTypeRoutingDecision {
			event := event
			decisionEvent = &event
			break
		}
	}
	if decisionEvent == nil {
		t.Fatal("Execute emitted no routing_decision event")
	}

	var decision fizeau.ServiceRoutingDecisionData
	if err := json.Unmarshal(decisionEvent.Data, &decision); err != nil {
		t.Fatalf("unmarshal routing_decision: %v", err)
	}
	var candidate *fizeau.ServiceRoutingDecisionCandidate
	for i := range decision.Candidates {
		if decision.Candidates[i].Provider == "public" && decision.Candidates[i].Model == "gpt-5.4-mini" {
			candidate = &decision.Candidates[i]
			break
		}
	}
	if candidate == nil {
		t.Fatalf("routing candidates=%#v, want public/gpt-5.4-mini", decision.Candidates)
	}
	if !candidate.Eligible || candidate.Score == 0 {
		t.Fatalf("selected candidate=%#v, want eligible with nonzero score", candidate)
	}
	if candidate.Components.Power != 8 {
		t.Fatalf("candidate components=%#v, want catalog power 8", candidate.Components)
	}
	if candidate.Components.Cost != candidate.CostUSDPer1kTokens {
		t.Fatalf("component cost=%v, want candidate cost %v", candidate.Components.Cost, candidate.CostUSDPer1kTokens)
	}
	for _, key := range []string{"base", "power"} {
		if _, ok := candidate.ScoreComponents[key]; !ok {
			t.Fatalf("candidate score_components=%#v, want %q", candidate.ScoreComponents, key)
		}
	}

	var raw struct {
		Candidates []map[string]json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(decisionEvent.Data, &raw); err != nil {
		t.Fatalf("unmarshal raw routing_decision: %v", err)
	}
	var rawCandidate map[string]json.RawMessage
	for _, item := range raw.Candidates {
		var provider, model string
		_ = json.Unmarshal(item["provider"], &provider)
		_ = json.Unmarshal(item["model"], &model)
		if provider == "public" && model == "gpt-5.4-mini" {
			rawCandidate = item
			break
		}
	}
	if rawCandidate == nil {
		t.Fatal("serialized routing_decision omitted selected candidate")
	}
	for _, key := range []string{"score", "components", "score_components"} {
		if _, ok := rawCandidate[key]; !ok {
			t.Fatalf("serialized candidate omitted %q: %s", key, decisionEvent.Data)
		}
	}
	var rawScore float64
	if err := json.Unmarshal(rawCandidate["score"], &rawScore); err != nil {
		t.Fatalf("unmarshal candidate score: %v", err)
	}
	if rawScore != candidate.Score {
		t.Fatalf("serialized candidate score=%v, want typed score %v", rawScore, candidate.Score)
	}
	var rawComponents map[string]json.RawMessage
	if err := json.Unmarshal(rawCandidate["components"], &rawComponents); err != nil {
		t.Fatalf("unmarshal candidate components: %v", err)
	}
	for _, key := range []string{"power", "cost", "power_weighted_capability", "power_hint_fit", "latency_weight", "placement_bonus", "quota_bonus", "marginal_cost_penalty", "availability_penalty", "stale_signal_penalty"} {
		if _, ok := rawComponents[key]; !ok {
			t.Fatalf("serialized components omitted %q: %s", key, rawCandidate["components"])
		}
	}
}
