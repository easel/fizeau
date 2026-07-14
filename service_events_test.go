package fizeau_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/harnesses"
)

func TestDrainExecute_DecodesTypedResult(t *testing.T) {
	ch := make(chan fizeau.ServiceEvent, 7)
	ch <- serviceEvent(t, fizeau.ServiceEventTypeRoutingDecision, 0, map[string]any{
		"harness":         "fiz",
		"provider":        "fake",
		"server_instance": "fake-instance",
		"model":           "fake-model",
		"reason":          "test route",
		"session_id":      "svc-test",
	})
	ch <- serviceEvent(t, fizeau.ServiceEventTypeTextDelta, 1, map[string]any{"text": "APPROVE\n"})
	ch <- serviceEvent(t, fizeau.ServiceEventTypeToolCall, 2, map[string]any{
		"id": "call-1", "name": "read", "input": map[string]any{"path": "README.md"},
	})
	ch <- serviceEvent(t, fizeau.ServiceEventTypeToolResult, 3, map[string]any{
		"id": "call-1", "output": "contents", "duration_ms": 12,
	})
	ch <- serviceEvent(t, fizeau.ServiceEventTypeCompaction, 4, map[string]any{
		"messages_before": 9, "messages_after": 4, "tokens_freed": 500,
	})
	ch <- serviceEvent(t, fizeau.ServiceEventTypeStall, 5, map[string]any{
		"reason": "read_only_tools_exceeded", "count": 25,
	})
	ch <- serviceEvent(t, fizeau.ServiceEventTypeFinal, 6, map[string]any{
		"status":  "success",
		"outcome": "success",
		"cause":   "completed",
		"stage":   "tool_loop",
		// Deliberately contradictory diagnostic text proves DrainExecute does
		// not infer the typed terminal result from TerminalError.
		"error":       "diagnostic text containing timeout and cancellation words",
		"exit_code":   0,
		"final_text":  "APPROVE\nLooks good.",
		"duration_ms": 123,
		"usage": map[string]any{
			"input_tokens": 10, "output_tokens": 5, "total_tokens": 15, "source": "native_stream", "fresh": true,
		},
		"warnings": []map[string]any{
			{"code": "usage_source_disagreement", "message": "selected by precedence"},
		},
		"cost_usd":         0.001,
		"session_log_path": "/tmp/session.jsonl",
		"routing_actual": map[string]any{
			"harness": "fiz", "provider": "fake", "server_instance": "fake-instance", "model": "fake-model",
		},
	})
	close(ch)

	result, err := fizeau.DrainExecute(context.Background(), ch)
	if err != nil {
		t.Fatalf("DrainExecute: %v", err)
	}
	if result.FinalStatus != "success" {
		t.Fatalf("FinalStatus: got %q", result.FinalStatus)
	}
	if result.Outcome != fizeau.SessionOutcomeSuccess || result.Cause != fizeau.TerminalCauseCompleted || result.Stage != fizeau.SessionStageToolLoop {
		t.Fatalf("typed terminal projection: got %q/%q/%q", result.Outcome, result.Cause, result.Stage)
	}
	if result.FinalText != "APPROVE\nLooks good." {
		t.Fatalf("FinalText: got %q", result.FinalText)
	}
	if result.Usage == nil || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 15 {
		t.Fatalf("Usage: got %#v", result.Usage)
	}
	if result.Usage.Source != "native_stream" || result.Usage.Fresh == nil || !*result.Usage.Fresh {
		t.Fatalf("Usage metadata: got %#v", result.Usage)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "usage_source_disagreement" {
		t.Fatalf("Warnings: got %#v", result.Warnings)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "read" {
		t.Fatalf("ToolCalls: got %#v", result.ToolCalls)
	}
	if len(result.ToolResults) != 1 || result.ToolResults[0].Output != "contents" {
		t.Fatalf("ToolResults: got %#v", result.ToolResults)
	}
	if result.RoutingDecision == nil || result.RoutingDecision.SessionID != "svc-test" {
		t.Fatalf("RoutingDecision: got %#v", result.RoutingDecision)
	}
	if result.RoutingDecision.ServerInstance != "fake-instance" {
		t.Fatalf("RoutingDecision.ServerInstance: got %q", result.RoutingDecision.ServerInstance)
	}
	if result.RoutingActual == nil || result.RoutingActual.Provider != "fake" {
		t.Fatalf("RoutingActual: got %#v", result.RoutingActual)
	}
	if result.RoutingActual.ServerInstance != "fake-instance" {
		t.Fatalf("RoutingActual.ServerInstance: got %q", result.RoutingActual.ServerInstance)
	}
	if result.SessionLogPath != "/tmp/session.jsonl" {
		t.Fatalf("SessionLogPath: got %q", result.SessionLogPath)
	}
	if len(result.Compactions) != 1 || result.Compactions[0].TokensFreed != 500 {
		t.Fatalf("Compactions: got %#v", result.Compactions)
	}
	if len(result.Stalls) != 1 || result.Stalls[0].Count != 25 {
		t.Fatalf("Stalls: got %#v", result.Stalls)
	}
}

func serviceEvent(t *testing.T, typ string, seq int64, payload any) fizeau.ServiceEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return fizeau.ServiceEvent{
		Type:     harnesses.EventType(typ),
		Sequence: seq,
		Time:     time.Unix(seq, 0).UTC(),
		Metadata: map[string]string{"bead_id": "test"},
		Data:     raw,
	}
}
