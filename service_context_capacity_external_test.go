package fizeau_test

import (
	"encoding/json"
	"reflect"
	"testing"

	fizeau "github.com/easel/fizeau"
)

func TestServiceContextCapacityPublicSurfaceExternal(t *testing.T) {
	payload := fizeau.ServiceContextCapacityData{
		Action:                 fizeau.ServiceContextCapacityRejected,
		CallKind:               fizeau.ServiceContextCapacityMain,
		TurnIndex:              1,
		AttemptIndex:           2,
		ContextWindow:          3,
		EffectiveContextWindow: 4,
		EstimatedInputTokens:   5,
		RequestedMaxTokens:     6,
		EffectiveMaxTokens:     7,
		AvailableOutputTokens:  8,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal public context capacity: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode public context capacity object: %v", err)
	}
	wantFields := []string{
		"action", "call_kind", "turn_index", "attempt_index", "context_window",
		"effective_context_window", "estimated_input_tokens", "requested_max_tokens",
		"effective_max_tokens", "available_output_tokens",
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("context-capacity fields = %v, want exactly %v", fields, wantFields)
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Errorf("public context-capacity JSON missing %q", field)
		}
	}

	decoded, err := fizeau.DecodeServiceEvent(fizeau.ServiceEvent{
		Type: fizeau.ServiceEventTypeContextCapacity,
		Data: raw,
	})
	if err != nil {
		t.Fatalf("DecodeServiceEvent: %v", err)
	}
	if decoded.ContextCapacity == nil || !reflect.DeepEqual(*decoded.ContextCapacity, payload) {
		t.Fatalf("decoded context capacity = %#v, want %#v", decoded.ContextCapacity, payload)
	}

	final := fizeau.ServiceFinalData{
		Status:          "failed",
		Outcome:         fizeau.SessionOutcomeFailed,
		Cause:           fizeau.TerminalCauseContextCapacityExceeded,
		Stage:           fizeau.SessionStageToolLoop,
		ContextCapacity: &payload,
	}
	finalRaw, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("marshal final: %v", err)
	}
	var roundTrip fizeau.ServiceFinalData
	if err := json.Unmarshal(finalRaw, &roundTrip); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if roundTrip.Cause != fizeau.TerminalCauseContextCapacityExceeded ||
		roundTrip.ContextCapacity == nil || !reflect.DeepEqual(*roundTrip.ContextCapacity, payload) {
		t.Fatalf("final context-capacity projection = %#v", roundTrip)
	}

	var future fizeau.ServiceContextCapacityData
	if err := json.Unmarshal([]byte(`{"action":"future_action","call_kind":"future_call"}`), &future); err != nil {
		t.Fatalf("unmarshal additive enum values: %v", err)
	}
	if future.Action != fizeau.ServiceContextCapacityAction("future_action") ||
		future.CallKind != fizeau.ServiceContextCapacityCallKind("future_call") {
		t.Fatalf("unknown enum values were not preserved: %#v", future)
	}
}
