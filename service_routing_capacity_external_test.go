package fizeau_test

import (
	"encoding/json"
	"testing"

	fizeau "github.com/easel/fizeau"
)

func TestServiceRoutingDecisionCapacityJSONRoundTripExternal(t *testing.T) {
	tests := []struct {
		name              string
		data              fizeau.ServiceRoutingDecisionData
		wantDecisionKeys  []string
		wantCandidateKeys []string
	}{
		{
			name: "zero unknown is not fabricated",
			data: fizeau.ServiceRoutingDecisionData{
				Harness: "fiz", Model: "unknown-model", Reason: "fixture",
				Candidates: []fizeau.ServiceRoutingDecisionCandidate{{
					Harness: "fiz", Model: "unknown-model",
				}},
			},
		},
		{
			name: "positive evidence survives",
			data: fizeau.ServiceRoutingDecisionData{
				Harness: "fiz", Model: "known-model", Reason: "fixture",
				EstimatedPromptTokens: 100,
				MaxTokens:             26,
				RequiredContext:       151,
				ContextLength:         512,
				ContextSource:         fizeau.ContextSourceProviderConfig,
				Candidates: []fizeau.ServiceRoutingDecisionCandidate{{
					Harness: "fiz", Model: "known-model",
					ContextLength: 512, ContextSource: fizeau.ContextSourceProviderConfig,
				}},
			},
			wantDecisionKeys:  []string{"estimated_prompt_tokens", "max_tokens", "required_context", "context_length", "context_source"},
			wantCandidateKeys: []string{"context_length", "context_source"},
		},
		{
			name: "typed unknown source survives without a length",
			data: fizeau.ServiceRoutingDecisionData{
				Harness: "fiz", Model: "unknown-model", Reason: "fixture",
				Candidates: []fizeau.ServiceRoutingDecisionCandidate{{
					Harness: "fiz", Model: "unknown-model", ContextSource: fizeau.ContextSourceUnknown,
				}},
			},
			wantCandidateKeys: []string{"context_source"},
		},
	}

	capacityKeys := []string{"estimated_prompt_tokens", "max_tokens", "required_context", "context_length", "context_source"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.data)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatalf("decode object: %v", err)
			}
			assertJSONKeySet(t, object, capacityKeys, test.wantDecisionKeys)

			var candidates []map[string]json.RawMessage
			if err := json.Unmarshal(object["candidates"], &candidates); err != nil || len(candidates) != 1 {
				t.Fatalf("decode candidates=%d err=%v: %s", len(candidates), err, raw)
			}
			assertJSONKeySet(t, candidates[0], []string{"context_length", "context_source"}, test.wantCandidateKeys)

			var roundTrip fizeau.ServiceRoutingDecisionData
			if err := json.Unmarshal(raw, &roundTrip); err != nil {
				t.Fatalf("round trip: %v", err)
			}
			if roundTrip.EstimatedPromptTokens != test.data.EstimatedPromptTokens ||
				roundTrip.MaxTokens != test.data.MaxTokens ||
				roundTrip.RequiredContext != test.data.RequiredContext ||
				roundTrip.ContextLength != test.data.ContextLength ||
				roundTrip.ContextSource != test.data.ContextSource ||
				len(roundTrip.Candidates) != 1 ||
				roundTrip.Candidates[0].ContextLength != test.data.Candidates[0].ContextLength ||
				roundTrip.Candidates[0].ContextSource != test.data.Candidates[0].ContextSource {
				t.Fatalf("capacity round trip=%#v, want %#v", roundTrip, test.data)
			}
		})
	}
}

func assertJSONKeySet(t *testing.T, object map[string]json.RawMessage, universe, want []string) {
	t.Helper()
	wanted := make(map[string]bool, len(want))
	for _, key := range want {
		wanted[key] = true
	}
	for _, key := range universe {
		_, present := object[key]
		if present != wanted[key] {
			t.Errorf("JSON key %q present=%v, want %v in %#v", key, present, wanted[key], object)
		}
	}
}
