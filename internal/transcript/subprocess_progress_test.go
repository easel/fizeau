package transcript

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestSubprocessProgressPairsToolCallsAndResults(t *testing.T) {
	result := runSubprocessProgressEvents(t, "subprocess prompt", []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}),
		harnessEvent(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: "tool output", DurationMS: 7}),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success"}),
	})

	var sawStart, sawComplete bool
	for _, payload := range result.progress {
		if payload.Phase != "tool" {
			continue
		}
		switch payload.State {
		case "start":
			sawStart = true
		case "complete":
			sawComplete = true
			if payload.ToolName != "bash" || payload.DurationMS != 7 {
				t.Fatalf("tool complete progress = %#v", payload)
			}
			if payload.ToolCallID != "tool-1" || payload.ToolCallIndex != 1 {
				t.Fatalf("tool complete identity = %#v", payload)
			}
			if payload.Command == "" || !strings.Contains(payload.Command, "echo hi") {
				t.Fatalf("tool complete command = %#v", payload)
			}
			if payload.OutputSummary == "" || !strings.Contains(payload.OutputSummary, "out=") {
				t.Fatalf("tool complete output summary = %#v", payload)
			}
			if payload.OutputBytes != len("tool output") || payload.OutputLines != 1 {
				t.Fatalf("tool complete output fields = %#v", payload)
			}
		}
	}
	if sawStart || !sawComplete {
		t.Fatalf("tool progress events = %#v, want only complete", result.progress)
	}
}

func TestSubprocessProgressDerivesMissingToolDurationFromEventTimes(t *testing.T) {
	start := time.Date(2026, 5, 6, 21, 17, 34, 260_000_000, time.UTC)
	end := start.Add(31*time.Second + 624*time.Millisecond)
	result := runSubprocessProgressEvents(t, "subprocess prompt", []harnesses.Event{
		harnessEventAt(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./cmd/bench"}`)}, start),
		harnessEventAt(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: "ok\n"}, end),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success"}),
	})

	for _, event := range result.events {
		if event.Type != harnesses.EventTypeToolResult {
			continue
		}
		var toolResult harnesses.ToolResultData
		if err := json.Unmarshal(event.Data, &toolResult); err != nil {
			t.Fatalf("unmarshal tool result: %v", err)
		}
		if toolResult.DurationMS != 31624 {
			t.Fatalf("tool_result duration = %dms, want 31624ms", toolResult.DurationMS)
		}
		break
	}

	for _, payload := range result.progress {
		if payload.Phase == "tool" && payload.State == "complete" {
			if payload.DurationMS != 31624 || payload.OutputBytes != len("ok") || payload.OutputLines != 1 {
				t.Fatalf("tool complete progress = %#v", payload)
			}
			if !strings.Contains(payload.Message, "31.624s") {
				t.Fatalf("tool complete message = %q, want derived duration", payload.Message)
			}
			return
		}
	}
	t.Fatalf("missing tool complete progress: %#v", result.progress)
}

func TestSubprocessProgressReportsResponseThroughput(t *testing.T) {
	result := runSubprocessProgressEvents(t, "subprocess prompt", []harnesses.Event{
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

	for _, payload := range result.progress {
		if payload.Phase == "response" && payload.State == "complete" {
			if payload.DurationMS != 42 || payload.TotalTokens == nil || *payload.TotalTokens != 15 {
				t.Fatalf("response progress = %#v", payload)
			}
			if payload.TokPerSec == nil || *payload.TokPerSec <= 0 {
				t.Fatalf("response tok/sec = %#v", payload.TokPerSec)
			}
			return
		}
	}
	t.Fatalf("missing response progress: %#v", result.progress)
}

func TestSubprocessProgressDoesNotDuplicateFinalOrToolEvents(t *testing.T) {
	state := NewSubprocessProgressState("subprocess prompt", "")
	toolCall := harnessEvent(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{
		ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`),
	})
	toolResult := harnessEvent(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{
		ID: "tool-1", Output: "tool output", DurationMS: 7,
	})
	final := harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success"})

	if payload, ok := state.NoteEvent(toolCall); ok {
		t.Fatalf("tool call emitted generic progress %#v, want none", payload)
	}
	if payload, ok := state.NoteEvent(toolResult); !ok || payload.Phase != "tool" || payload.State != "complete" {
		t.Fatalf("tool result progress = (%#v, %v), want one tool completion", payload, ok)
	}
	if payload, ok := state.NoteEvent(final); ok {
		t.Fatalf("final emitted generic progress %#v, want dedicated final projection only", payload)
	}
	if payload, ok := state.NoteFinalEvent(final); !ok || payload.Phase != "response" || payload.State != "complete" {
		t.Fatalf("final progress = (%#v, %v), want one response completion", payload, ok)
	}
}

func TestSubprocessProgressBoundsSessionLogSummaries(t *testing.T) {
	prompt := strings.Repeat("PROMPT-SECRET-", 30)
	output := strings.Repeat("TOOL-SECRET-", 40)
	result := runSubprocessProgressEvents(t, prompt, []harnesses.Event{
		harnessEvent(t, harnesses.EventTypeToolCall, harnesses.ToolCallData{ID: "tool-1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}),
		harnessEvent(t, harnesses.EventTypeToolResult, harnesses.ToolResultData{ID: "tool-1", Output: output, DurationMS: 7}),
		harnessEvent(t, harnesses.EventTypeFinal, harnesses.FinalData{Status: "success", FinalText: "done"}),
	})

	for _, payload := range result.progress {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal progress: %v", err)
		}
		if len(payload.SessionSummary) > 240 {
			t.Fatalf("session summary too long: %d", len(payload.SessionSummary))
		}
		if strings.Contains(string(raw), prompt) || strings.Contains(string(raw), output) {
			t.Fatalf("progress leaked prompt or tool output: %s", raw)
		}
	}
}

type subprocessProgressResult struct {
	events   []harnesses.Event
	progress []ProgressPayload
}

func runSubprocessProgressEvents(t *testing.T, prompt string, harnessEvents []harnesses.Event) subprocessProgressResult {
	t.Helper()
	state := NewSubprocessProgressState(prompt, "")
	result := subprocessProgressResult{
		progress: []ProgressPayload{state.NoteRequestStart()},
	}
	for _, event := range harnessEvents {
		event = state.AnnotateToolResultDuration(event)
		if payload, ok := state.NoteEvent(event); ok && event.Type != harnesses.EventTypeProgress {
			result.progress = append(result.progress, payload)
		}
		if event.Type == harnesses.EventTypeFinal {
			if payload, ok := state.NoteFinalEvent(event); ok {
				result.progress = append(result.progress, payload)
			}
		}
		result.events = append(result.events, event)
	}
	return result
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
