package fizeau

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/easel/fizeau/internal/session"
)

func TestServiceSessionLogPersistsReasoningStallEvent(t *testing.T) {
	dir := t.TempDir()
	sessionID := "reasoning-stall-session"
	svc := &service{}
	sl := svc.openSessionLog(ServiceExecuteRequest{SessionLogDir: dir}, RouteDecision{}, sessionID)

	payload := map[string]any{
		"code":           agentcore.ReasoningStallCode,
		"model":          "qwen-test",
		"timeout_ms":     50,
		"reasoning_tail": "thinking tail",
		"prompt_id":      "prompt-1",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	sl.writeEvent(agentcore.Event{
		SessionID: sessionID,
		Seq:       1,
		Type:      agentcore.EventReasoningStall,
		Timestamp: time.Now().UTC(),
		Data:      data,
	})
	sl.close()

	body, err := os.ReadFile(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`"type":"reasoning.stall"`,
		`"code":"REASONING_STALL"`,
		`"reasoning_tail":"thinking tail"`,
		`"prompt_id":"prompt-1"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("session log missing %s:\n%s", want, text)
		}
	}
}

func TestServiceSessionEndTypedTerminalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessionID := "typed-terminal-session"
	req := ServiceExecuteRequest{SessionLogDir: dir}
	svc := &service{}
	sl := svc.openSessionLog(req, RouteDecision{Harness: "codex", Model: "gpt-test"}, sessionID)
	primary := serviceimpl.ClassifyTerminalFinal(harnesses.FinalData{Status: "timed_out"}, serviceimpl.TerminalOriginHarness, nil)
	final := serviceimpl.SupersedeWithCleanupFailure(primary, "cleanup diagnostic")
	sl.writeEnd(req, nil, final)
	sl.close()

	events, err := session.ReadEvents(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var got session.SessionEndData
	for _, event := range events {
		if event.Type == agentcore.EventSessionEnd {
			if err := json.Unmarshal(event.Data, &got); err != nil {
				t.Fatalf("decode session.end: %v", err)
			}
		}
	}
	if got.Outcome != final.Outcome || got.Cause != final.Cause || got.Stage != final.Stage {
		t.Fatalf("durable tuple = %q/%q/%q", got.Outcome, got.Cause, got.Stage)
	}
	if got.PrimaryOutcome != primary.Outcome || got.PrimaryCause != primary.Cause || got.PrimaryStage != primary.Stage {
		t.Fatalf("durable primary tuple = %q/%q/%q", got.PrimaryOutcome, got.PrimaryCause, got.PrimaryStage)
	}
}

func TestRunSubprocessFirstFinalWinsLiveAndDurable(t *testing.T) {
	dir := t.TempDir()
	sessionID := "first-final-wins"
	req := ServiceExecuteRequest{Prompt: "test", SessionLogDir: dir}
	decision := RouteDecision{Harness: "codex", Provider: "codex", Model: "gpt-test"}
	svc := &service{}
	sl := svc.openSessionLog(req, decision, sessionID)
	liveEvents := runSessionLogSubprocess(t, req, decision, sl, sessionID, &subprocessProgressHarness{events: []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "failed", Error: "first"}),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success", FinalText: "second"}),
	}})
	sl.close()

	var live []ServiceFinalData
	for _, event := range liveEvents {
		if event.Type != harnesses.EventTypeFinal {
			continue
		}
		var final ServiceFinalData
		if err := json.Unmarshal(event.Data, &final); err != nil {
			t.Fatalf("decode live final: %v", err)
		}
		live = append(live, final)
	}
	if len(live) != 1 {
		t.Fatalf("live terminal events = %d, want 1", len(live))
	}

	events, err := session.ReadEvents(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var durable session.SessionEndData
	ends := 0
	for _, event := range events {
		if event.Type != agentcore.EventSessionEnd {
			continue
		}
		ends++
		if err := json.Unmarshal(event.Data, &durable); err != nil {
			t.Fatalf("decode session.end: %v", err)
		}
	}
	if ends != 1 {
		t.Fatalf("durable terminal events = %d, want 1", ends)
	}
	if string(durable.Outcome) != string(live[0].Outcome) || string(durable.Cause) != string(live[0].Cause) || string(durable.Stage) != string(live[0].Stage) || durable.Error != live[0].Error {
		t.Fatalf("live/durable terminal facts diverged: live=%#v durable=%#v", live[0], durable)
	}
}

func TestRunSubprocessInvalidTerminalBecomesOneInternalHarnessFinal(t *testing.T) {
	tests := []struct {
		name   string
		events []harnesses.Event
	}{
		{name: "closed without final"},
		{
			name: "malformed final",
			events: []harnesses.Event{{
				Type: harnesses.EventTypeFinal,
				Time: time.Now().UTC(),
				Data: json.RawMessage(`{"status":`),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			sessionID := "invalid-terminal"
			req := ServiceExecuteRequest{Prompt: "test", SessionLogDir: dir}
			decision := RouteDecision{Harness: "codex", Provider: "codex", Model: "gpt-test"}
			svc := &service{}
			sl := svc.openSessionLog(req, decision, sessionID)
			liveEvents := runSessionLogSubprocess(t, req, decision, sl, sessionID, &subprocessProgressHarness{events: tt.events})
			sl.close()

			var live []ServiceFinalData
			for _, event := range liveEvents {
				if event.Type != harnesses.EventTypeFinal {
					continue
				}
				var final ServiceFinalData
				if err := json.Unmarshal(event.Data, &final); err != nil {
					t.Fatalf("decode live final: %v", err)
				}
				live = append(live, final)
			}
			if len(live) != 1 {
				t.Fatalf("live terminal events = %d, want 1", len(live))
			}
			if live[0].Outcome != SessionOutcomeFailed || live[0].Cause != TerminalCauseInternalError || live[0].Stage != SessionStageHarness {
				t.Fatalf("live terminal tuple = %q/%q/%q, want failed/internal_error/harness", live[0].Outcome, live[0].Cause, live[0].Stage)
			}

			events, err := session.ReadEvents(filepath.Join(dir, sessionID+".jsonl"))
			if err != nil {
				t.Fatalf("ReadEvents: %v", err)
			}
			var durable session.SessionEndData
			ends := 0
			for _, event := range events {
				if event.Type != agentcore.EventSessionEnd {
					continue
				}
				ends++
				if err := json.Unmarshal(event.Data, &durable); err != nil {
					t.Fatalf("decode session.end: %v", err)
				}
			}
			if ends != 1 {
				t.Fatalf("durable terminal events = %d, want 1", ends)
			}
			if string(durable.Outcome) != string(live[0].Outcome) || string(durable.Cause) != string(live[0].Cause) || string(durable.Stage) != string(live[0].Stage) || durable.Error != live[0].Error {
				t.Fatalf("live/durable terminal facts diverged: live=%#v durable=%#v", live[0], durable)
			}
		})
	}
}

func runSessionLogSubprocess(t *testing.T, req ServiceExecuteRequest, decision RouteDecision, sl *serviceSessionLog, sessionID string, runner harnesses.Harness) []ServiceEvent {
	t.Helper()
	var events []ServiceEvent
	serviceimpl.RunSubprocess(context.Background(), serviceimpl.SubprocessRequest{
		Prompt:         req.Prompt,
		SessionID:      sessionID,
		SessionLogPath: sl.path,
		Decision: serviceimpl.ExecuteRunnerDecision{
			Harness:        decision.Harness,
			Provider:       decision.Provider,
			ServerInstance: decision.ServerInstance,
			Model:          decision.Model,
		},
		Started: time.Now(),
	}, runner, serviceimpl.SubprocessCallbacks{
		EmitEvent: func(event harnesses.Event) bool {
			events = append(events, event)
			return true
		},
		WriteEnd: func(meta map[string]string, final harnesses.FinalData) {
			sl.writeEnd(req, meta, final)
		},
	})
	return events
}

func TestServiceSessionLogPersistsHarnessProvenance(t *testing.T) {
	dir := t.TempDir()
	sessionID := "routing-provenance-session"
	svc := &service{}
	req := ServiceExecuteRequest{
		SessionLogDir: dir,
		Model:         "sonnet",
		Prompt:        "test prompt",
	}
	sl := svc.openSessionLog(req, RouteDecision{
		Harness:        "claude",
		Provider:       "claude",
		ServerInstance: "claude-sonnet-1",
		Model:          "sonnet",
	}, sessionID)
	sl.writeEnd(req, nil, harnesses.FinalData{
		Status: string(agentcore.StatusSuccess),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        "claude",
			ServerInstance: "claude-sonnet-1",
			Model:          "sonnet",
		},
	})
	sl.close()

	body, err := os.ReadFile(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`"type":"session.start"`,
		`"type":"routing_decision"`,
		`"type":"session.end"`,
		`"resolved_harness":"claude"`,
		`"harness_source":"auto_route"`,
		`"selected_server_instance":"claude-sonnet-1"`,
		`"snapshot_captured_at"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("session log missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"requested_harness"`) {
		t.Fatalf("session log unexpectedly recorded requested_harness for auto route:\n%s", text)
	}
}
