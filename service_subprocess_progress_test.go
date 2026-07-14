package fizeau

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/transcript"
)

type subprocessProgressHarness struct {
	events []harnesses.Event
}

func (h *subprocessProgressHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "codex", Type: "subprocess", Available: true}
}

func (h *subprocessProgressHarness) HealthCheck(context.Context) error { return nil }

func (h *subprocessProgressHarness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	out := make(chan harnesses.Event, len(h.events))
	go func() {
		defer close(out)
		for _, ev := range h.events {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func TestRunSubprocess_EmitsToolProgress(t *testing.T) {
	events := runSubprocessProgressEvents(t, []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}),
		harnessEvent(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: "tool output", DurationMS: 7}),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success"}),
	})

	var sawStart, sawComplete bool
	for _, p := range subprocessProgressEvents(events) {
		if p.Phase != "tool" {
			continue
		}
		switch p.State {
		case "start":
			sawStart = true
			if p.ToolName != "bash" || !strings.Contains(p.Command, "echo hi") {
				t.Fatalf("tool start progress = %#v", p)
			}
		case "complete":
			sawComplete = true
			if p.ToolName != "bash" || p.DurationMS != 7 {
				t.Fatalf("tool complete progress = %#v", p)
			}
			if p.ToolCallID != "tool-1" || p.ToolCallIndex != 1 {
				t.Fatalf("tool complete identity = %#v", p)
			}
			if p.OutputSummary == "" || !strings.Contains(p.OutputSummary, "out=") {
				t.Fatalf("tool complete output summary = %#v", p)
			}
			if p.OutputBytes != len("tool output") || p.OutputLines != 1 {
				t.Fatalf("tool complete output fields = %#v", p)
			}
		}
	}
	if sawStart || !sawComplete {
		t.Fatalf("tool progress events = %#v, want only complete", subprocessProgressEvents(events))
	}
}

func TestRunSubprocess_DerivesMissingToolDurationFromEventTimes(t *testing.T) {
	start := time.Date(2026, 5, 6, 21, 17, 34, 260_000_000, time.UTC)
	end := start.Add(31*time.Second + 624*time.Millisecond)
	events := runSubprocessProgressEvents(t, []harnesses.Event{
		harnessEventAt(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./cmd/bench"}`)}, start),
		harnessEventAt(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: "ok\n"}, end),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success"}),
	})

	for _, ev := range events {
		if ev.Type != harnesses.EventTypeToolResult {
			continue
		}
		var result harnesses.ToolResultData
		if err := json.Unmarshal(ev.Data, &result); err != nil {
			t.Fatalf("unmarshal tool result: %v", err)
		}
		if result.DurationMS != 31624 {
			t.Fatalf("tool_result duration = %dms, want 31624ms", result.DurationMS)
		}
		break
	}

	for _, p := range subprocessProgressEvents(events) {
		if p.Phase == "tool" && p.State == "complete" {
			if p.DurationMS != 31624 || p.OutputBytes != len("ok") || p.OutputLines != 1 {
				t.Fatalf("tool complete progress = %#v", p)
			}
			if !strings.Contains(p.Message, "31.624s") {
				t.Fatalf("tool complete message = %q, want derived duration", p.Message)
			}
			return
		}
	}
	t.Fatalf("missing tool complete progress: %#v", subprocessProgressEvents(events))
}

func TestRunSubprocess_EmitsResponseProgress(t *testing.T) {
	events := runSubprocessProgressEvents(t, []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{
			Status:     "success",
			DurationMS: 42,
			Usage: &harnesses.FinalUsage{
				InputTokens:  harnesses.IntPtr(10),
				OutputTokens: harnesses.IntPtr(5),
				TotalTokens:  harnesses.IntPtr(15),
			},
		}),
	})

	for _, p := range subprocessProgressEvents(events) {
		if p.Phase == "response" && p.State == "complete" {
			if p.DurationMS != 42 || p.TotalTokens == nil || *p.TotalTokens != 15 {
				t.Fatalf("response progress = %#v", p)
			}
			if p.TokPerSec == nil || *p.TokPerSec <= 0 {
				t.Fatalf("response tok/sec = %#v", p.TokPerSec)
			}
			return
		}
	}
	t.Fatalf("missing response progress: %#v", subprocessProgressEvents(events))
}

func TestSubprocessProgress_DoesNotDuplicateFinalOrToolEvents(t *testing.T) {
	events := runSubprocessProgressEvents(t, []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}),
		harnessEvent(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: "tool output", DurationMS: 7}),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success"}),
	})

	counts := map[harnesses.EventType]int{}
	for _, ev := range events {
		counts[ev.Type]++
	}
	if counts[harnesses.EventTypeToolCall] != 1 || counts[harnesses.EventTypeToolResult] != 1 || counts[harnesses.EventTypeFinal] != 1 {
		t.Fatalf("event counts = %#v, want one original tool_call/tool_result/final", counts)
	}
}

func TestSubprocessProgress_BoundsSessionLogSummaries(t *testing.T) {
	prompt := strings.Repeat("PROMPT-SECRET-", 30)
	output := strings.Repeat("TOOL-SECRET-", 40)
	events := runSubprocessProgressEvents(t, []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}),
		harnessEvent(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: output, DurationMS: 7}),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success", FinalText: "done"}),
	}, func(req *ServiceExecuteRequest) {
		req.Prompt = prompt
	})

	for _, p := range subprocessProgressEvents(events) {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal progress: %v", err)
		}
		if len(p.SessionSummary) > 240 {
			t.Fatalf("session summary too long: %d", len(p.SessionSummary))
		}
		if strings.Contains(string(raw), prompt) || strings.Contains(string(raw), output) {
			t.Fatalf("progress leaked prompt or tool output: %s", raw)
		}
	}
}

func runSubprocessProgressEvents(t *testing.T, harnessEvents []harnesses.Event, mutateReq ...func(*ServiceExecuteRequest)) []ServiceEvent {
	t.Helper()
	req := ServiceExecuteRequest{
		Prompt: "subprocess prompt",
	}
	for _, mutate := range mutateReq {
		mutate(&req)
	}

	// Runtime progress projection belongs to internal/transcript. Exercise it
	// at that ownership boundary rather than reaching through the former root
	// runSubprocess implementation helper.
	progress := transcript.NewSubprocessProgressState(req.Prompt, req.SystemPrompt)
	events := []ServiceEvent{progressEvent(t, progress.NoteRequestStart())}
	for _, event := range harnessEvents {
		event = progress.AnnotateToolResultDuration(event)
		if payload, ok := progress.NoteEvent(event); ok && event.Type != harnesses.EventTypeProgress {
			events = append(events, progressEvent(t, payload))
		}
		if event.Type == harnesses.EventTypeFinal {
			if payload, ok := progress.NoteFinalEvent(event); ok {
				events = append(events, progressEvent(t, payload))
			}
		}
		events = append(events, event)
	}
	return events
}

func progressEvent(t *testing.T, payload transcript.ProgressPayload) ServiceEvent {
	t.Helper()
	raw, err := json.Marshal(fromTranscriptProgress(payload))
	if err != nil {
		t.Fatalf("marshal progress: %v", err)
	}
	return ServiceEvent{Type: harnesses.EventTypeProgress, Data: raw}
}

func subprocessProgressEvents(events []ServiceEvent) []ServiceProgressData {
	var out []ServiceProgressData
	for _, ev := range events {
		if ev.Type != harnesses.EventTypeProgress {
			continue
		}
		var payload ServiceProgressData
		_ = json.Unmarshal(ev.Data, &payload)
		out = append(out, payload)
	}
	return out
}

func harnessEvent(t *testing.T, typ harnesses.EventType, payload any) harnesses.Event {
	t.Helper()
	return harnessEventAt(t, typ, payload, time.Now().UTC())
}

func harnessEventAt(t *testing.T, typ harnesses.EventType, payload any, at time.Time) harnesses.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal harness event: %v", err)
	}
	return harnesses.Event{
		Type: typ,
		Time: at,
		Data: raw,
	}
}
