package serviceimpl

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

var _ ExecuteSessionLog = (*SessionLog)(nil)

func TestSessionLogProjectsTerminalAndFirstEndWins(t *testing.T) {
	dir := t.TempDir()
	costCap := 2.5
	activeRequests := 3
	endBase := session.SessionEndData{
		SelectedEndpoint: "desk-a",
		SelectedRoute:    "local-fast",
		Sticky: session.RoutingStickyState{
			KeyPresent: true,
			Assignment: "acquired",
			Reason:     "new sticky lease acquired",
			Bonus:      0.25,
		},
		Utilization: session.RoutingUtilizationState{
			Source:         "llama-server.slots",
			Freshness:      "fresh",
			ActiveRequests: &activeRequests,
		},
		RequestedHarness: "fiz",
		HarnessSource:    "request_harness",
		RequestedModel:   "requested-model",
		Reasoning:        agentcore.ReasoningHigh,
		CostCapUSD:       &costCap,
	}
	sl := OpenSessionLog(SessionLogOptions{
		Dir:       dir,
		SessionID: "terminal-projection",
		Start:     session.SessionStartData{Prompt: "test"},
		EndBase:   endBase,
		Decision: SessionLogDecision{
			ServerInstance: "desk-a-slot-2",
		},
	})

	// Mutating caller-owned option values after OpenSessionLog must not alter
	// the immutable request facts retained for session.end.
	*endBase.CostCapUSD = 99
	*endBase.Utilization.ActiveRequests = 99

	input, output, cacheRead, cacheWrite, total := 10, 4, 3, 2, 19
	finalCost := 1.75
	sl.WriteEnd(map[string]string{"role": "implementer"}, harnesses.FinalData{
		Status:          string(agentcore.StatusBudgetHalted),
		Outcome:         harnesses.SessionOutcomeFailed,
		Cause:           harnesses.TerminalCauseBudgetHalted,
		Stage:           harnesses.SessionStageProvider,
		PrimaryOutcome:  harnesses.SessionOutcomeFailed,
		PrimaryCause:    harnesses.TerminalCauseBudgetHalted,
		PrimaryStage:    harnesses.SessionStageProvider,
		Error:           "cost cap reached",
		FinalText:       "partial answer",
		DurationMS:      1250,
		FinalCostUSD:    &finalCost,
		FinalCostSource: harnesses.CostSourceConfigured,
		CostUSD:         99,
		Usage: &harnesses.FinalUsage{
			InputTokens:      &input,
			OutputTokens:     &output,
			CacheReadTokens:  &cacheRead,
			CacheWriteTokens: &cacheWrite,
			TotalTokens:      &total,
		},
		RoutingActual: &harnesses.RoutingActual{
			Harness:            "fiz",
			Provider:           "local",
			Model:              "resolved-model",
			FallbackChainFired: []string{"primary", "fallback"},
		},
		Reasoning: &harnesses.ReasoningActual{
			ResolvedReasoning: string(agentcore.ReasoningMedium),
			Source:            "model_catalog",
		},
	})
	sl.WriteEnd(map[string]string{"role": "should-not-win"}, harnesses.FinalData{
		Status:    "success",
		FinalText: "second terminal",
	})
	sl.Close()

	events, err := session.ReadEvents(filepath.Join(dir, "terminal-projection.jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var got session.SessionEndData
	ends := 0
	for _, event := range events {
		if event.Type != agentcore.EventSessionEnd {
			continue
		}
		ends++
		if err := json.Unmarshal(event.Data, &got); err != nil {
			t.Fatalf("decode session.end: %v", err)
		}
	}
	if ends != 1 {
		t.Fatalf("session.end count = %d, want 1", ends)
	}
	if got.Status != agentcore.StatusBudgetHalted || got.ProcessOutcome != "budget_halted" {
		t.Fatalf("status/process_outcome = %q/%q", got.Status, got.ProcessOutcome)
	}
	if got.Outcome != harnesses.SessionOutcomeFailed || got.Cause != harnesses.TerminalCauseBudgetHalted || got.Stage != harnesses.SessionStageProvider {
		t.Fatalf("terminal tuple = %q/%q/%q", got.Outcome, got.Cause, got.Stage)
	}
	if got.PrimaryOutcome != harnesses.SessionOutcomeFailed || got.PrimaryCause != harnesses.TerminalCauseBudgetHalted || got.PrimaryStage != harnesses.SessionStageProvider {
		t.Fatalf("primary tuple = %q/%q/%q", got.PrimaryOutcome, got.PrimaryCause, got.PrimaryStage)
	}
	if got.Output != "partial answer" || got.Error != "cost cap reached" || got.DurationMs != 1250 {
		t.Fatalf("terminal content = %#v", got)
	}
	if got.Tokens.Input != input || got.Tokens.Output != output || got.Tokens.CacheRead != cacheRead || got.Tokens.CacheWrite != cacheWrite || got.Tokens.Total != total {
		t.Fatalf("tokens = %#v", got.Tokens)
	}
	if got.CostUSD == nil || *got.CostUSD != 1.75 || got.CostSource != harnesses.CostSourceConfigured || got.CostCapUSD == nil || *got.CostCapUSD != 2.5 {
		t.Fatalf("cost/source/cap = %v/%q/%v", got.CostUSD, got.CostSource, got.CostCapUSD)
	}
	if got.ResolvedHarness != "fiz" || got.SelectedProvider != "local" || got.Model != "resolved-model" || got.ResolvedModel != "resolved-model" {
		t.Fatalf("resolved route = %#v", got)
	}
	if got.SelectedServerInstance != "desk-a-slot-2" {
		t.Fatalf("server instance = %q, want decision fallback", got.SelectedServerInstance)
	}
	if len(got.AttemptedProviders) != 2 || got.FailoverCount != 1 {
		t.Fatalf("fallback evidence = %v/%d", got.AttemptedProviders, got.FailoverCount)
	}
	if got.ResolvedReasoning != agentcore.ReasoningMedium || got.ReasoningSource != "model_catalog" {
		t.Fatalf("reasoning = %q/%q", got.ResolvedReasoning, got.ReasoningSource)
	}
	if got.Metadata["role"] != "implementer" || got.Utilization.ActiveRequests == nil || *got.Utilization.ActiveRequests != 3 {
		t.Fatalf("immutable metadata/utilization = %#v/%#v", got.Metadata, got.Utilization)
	}
	if !sl.EndWritten() {
		t.Fatal("EndWritten = false after terminal write")
	}
}

func TestSessionLogPreservesFinalCostPresence(t *testing.T) {
	tests := []struct {
		name       string
		final      harnesses.FinalData
		wantCost   *float64
		wantSource harnesses.CostSource
	}{
		{name: "nil unknown", final: harnesses.FinalData{Status: "success", FinalCostSource: harnesses.CostSourceUnknown}, wantSource: harnesses.CostSourceUnknown},
		{name: "nil amount forces unknown", final: harnesses.FinalData{Status: "success", FinalCostSource: harnesses.CostSourceReported}, wantSource: harnesses.CostSourceUnknown},
		{name: "known zero", final: harnesses.FinalData{Status: "success", FinalCostUSD: float64Pointer(0), FinalCostSource: harnesses.CostSourceReported}, wantCost: float64Pointer(0), wantSource: harnesses.CostSourceReported},
		{name: "positive configured", final: harnesses.FinalData{Status: "success", FinalCostUSD: float64Pointer(1.25), FinalCostSource: harnesses.CostSourceConfigured}, wantCost: float64Pointer(1.25), wantSource: harnesses.CostSourceConfigured},
		{name: "positive reported", final: harnesses.FinalData{Status: "success", FinalCostUSD: float64Pointer(2.5), FinalCostSource: harnesses.CostSourceReported}, wantCost: float64Pointer(2.5), wantSource: harnesses.CostSourceReported},
		{name: "empty source", final: harnesses.FinalData{Status: "success", FinalCostUSD: float64Pointer(3)}, wantSource: harnesses.CostSourceUnknown},
		{name: "invalid source", final: harnesses.FinalData{Status: "success", FinalCostUSD: float64Pointer(3), FinalCostSource: harnesses.CostSource("invalid")}, wantSource: harnesses.CostSourceUnknown},
		{name: "negative amount", final: harnesses.FinalData{Status: "success", FinalCostUSD: float64Pointer(-1), FinalCostSource: harnesses.CostSourceReported}, wantSource: harnesses.CostSourceUnknown},
		{name: "legacy scalar is not promoted", final: harnesses.FinalData{Status: "success", CostUSD: 4, FinalCostSource: harnesses.CostSourceReported}, wantSource: harnesses.CostSourceUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			sl := OpenSessionLog(SessionLogOptions{Dir: dir, SessionID: "cost", Start: session.SessionStartData{Prompt: "test"}})
			sl.WriteEnd(nil, test.final)
			sl.Close()

			events, err := session.ReadEvents(filepath.Join(dir, "cost.jsonl"))
			if err != nil {
				t.Fatalf("ReadEvents: %v", err)
			}
			var (
				got    session.SessionEndData
				gotRaw map[string]json.RawMessage
			)
			for _, event := range events {
				if event.Type == agentcore.EventSessionEnd {
					if err := json.Unmarshal(event.Data, &got); err != nil {
						t.Fatalf("decode session.end: %v", err)
					}
					if err := json.Unmarshal(event.Data, &gotRaw); err != nil {
						t.Fatalf("decode raw session.end: %v", err)
					}
				}
			}
			if _, ok := gotRaw["cost_source"]; !ok {
				t.Fatal("cost_source omitted from session.end")
			}
			if got.CostSource != test.wantSource {
				t.Fatalf("cost source = %q, want %q", got.CostSource, test.wantSource)
			}
			if test.wantCost == nil {
				if got.CostUSD != nil {
					t.Fatalf("cost = %v, want nil", got.CostUSD)
				}
				if _, ok := gotRaw["cost_usd"]; ok {
					t.Fatal("unknown cost_usd was not omitted")
				}
				return
			}
			if got.CostUSD == nil || *got.CostUSD != *test.wantCost {
				t.Fatalf("cost = %v, want %v", got.CostUSD, *test.wantCost)
			}
			if _, ok := gotRaw["cost_usd"]; !ok {
				t.Fatal("known cost_usd was omitted")
			}
		})
	}

	t.Run("clones authoritative pointer and first end wins", func(t *testing.T) {
		dir := t.TempDir()
		sl := OpenSessionLog(SessionLogOptions{Dir: dir, SessionID: "first", Start: session.SessionStartData{Prompt: "test"}})
		cost := 0.0
		projected := sl.endData(nil, harnesses.FinalData{Status: "success", FinalCostUSD: &cost, FinalCostSource: harnesses.CostSourceReported})
		cost = 8
		if projected.CostUSD == nil || *projected.CostUSD != 0 {
			t.Fatalf("projected pointer was not cloned: %v", projected.CostUSD)
		}

		first := 0.0
		second := 9.0
		sl.WriteEnd(nil, harnesses.FinalData{Status: "success", FinalCostUSD: &first, FinalCostSource: harnesses.CostSourceReported})
		sl.WriteEnd(nil, harnesses.FinalData{Status: "success", FinalCostUSD: &second, FinalCostSource: harnesses.CostSourceConfigured})
		sl.Close()

		events, err := session.ReadEvents(filepath.Join(dir, "first.jsonl"))
		if err != nil {
			t.Fatalf("ReadEvents: %v", err)
		}
		ends := 0
		var got session.SessionEndData
		for _, event := range events {
			if event.Type == agentcore.EventSessionEnd {
				ends++
				if err := json.Unmarshal(event.Data, &got); err != nil {
					t.Fatalf("decode session.end: %v", err)
				}
			}
		}
		if ends != 1 || got.CostUSD == nil || *got.CostUSD != 0 || got.CostSource != harnesses.CostSourceReported {
			t.Fatalf("first-end result count=%d cost=%v source=%q", ends, got.CostUSD, got.CostSource)
		}
	})
}

func TestSessionLogPersistsCoreAndOpaqueOverrideEvents(t *testing.T) {
	dir := t.TempDir()
	sl := OpenSessionLog(SessionLogOptions{
		Dir:       dir,
		SessionID: "event-filtering",
		Start:     session.SessionStartData{Prompt: "test"},
	})
	sl.WriteCoreEvent(agentcore.Event{
		SessionID: "event-filtering",
		Seq:       10,
		Type:      agentcore.EventReasoningStall,
		Timestamp: time.Now().UTC(),
		Data:      json.RawMessage(`{"code":"REASONING_STALL"}`),
	})
	for _, eventType := range []agentcore.EventType{
		agentcore.EventSessionStart,
		agentcore.EventSessionEnd,
		agentcore.EventOverride,
		agentcore.EventRejectedOverride,
	} {
		sl.WriteCoreEvent(agentcore.Event{Type: eventType, Data: json.RawMessage(`{"duplicate":true}`)})
	}
	sl.WriteOverride(agentcore.EventOverride, json.RawMessage(`{"session_id":"event-filtering","outcome":{"status":"success"}}`))
	sl.WriteOverride(agentcore.EventRejectedOverride, json.RawMessage(`{"invalid":`))
	sl.Close()

	events, err := session.ReadEvents(filepath.Join(dir, "event-filtering.jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	counts := make(map[agentcore.EventType]int)
	for _, event := range events {
		counts[event.Type]++
	}
	if counts[agentcore.EventSessionStart] != 1 || counts[agentcore.EventReasoningStall] != 1 || counts[agentcore.EventOverride] != 1 {
		t.Fatalf("event counts = %v", counts)
	}
	if counts[agentcore.EventSessionEnd] != 0 || counts[agentcore.EventRejectedOverride] != 0 {
		t.Fatalf("filtered event counts = %v", counts)
	}
}

func TestSessionLogProgressIntervalAndDisabledLog(t *testing.T) {
	disabled := OpenSessionLog(SessionLogOptions{})
	if disabled.Enabled() || disabled.Path() != "" || disabled.EndWritten() {
		t.Fatalf("disabled log state = enabled:%v path:%q end:%v", disabled.Enabled(), disabled.Path(), disabled.EndWritten())
	}
	disabled.WriteCoreEvent(agentcore.Event{Type: agentcore.EventReasoningStall})
	disabled.WriteOverride(agentcore.EventOverride, json.RawMessage(`{}`))
	disabled.WriteEnd(nil, harnesses.FinalData{Status: "success"})
	disabled.Close()

	dir := t.TempDir()
	sl := OpenSessionLog(SessionLogOptions{Dir: dir, SessionID: "progress"})
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if got := sl.ProgressIntervalMS(start); got != 0 {
		t.Fatalf("first interval = %d, want 0", got)
	}
	if got := sl.ProgressIntervalMS(start.Add(1250 * time.Millisecond)); got != 1250 {
		t.Fatalf("second interval = %d, want 1250", got)
	}
	sl.Close()
}
