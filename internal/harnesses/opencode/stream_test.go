package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestParseOpencodeStream_RealJSONL(t *testing.T) {
	data, err := os.ReadFile("testdata/jsonl/text_only.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan harnesses.Event, 16)
	var seq int64
	agg, err := parseOpencodeStream(context.Background(), bytes.NewReader(data), out, nil, &seq)
	close(out)
	if err != nil {
		t.Fatalf("parseOpencodeStream: %v", err)
	}

	var textDeltas []harnesses.TextDeltaData
	for ev := range out {
		if ev.Type == harnesses.EventTypeTextDelta {
			var d harnesses.TextDeltaData
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				t.Fatalf("unmarshal text_delta: %v", err)
			}
			textDeltas = append(textDeltas, d)
		}
	}

	if len(textDeltas) != 1 {
		t.Fatalf("expected exactly 1 text_delta, got %d", len(textDeltas))
	}
	if textDeltas[0].Text != "PONG" {
		t.Fatalf("text_delta.Text = %q, want \"PONG\"", textDeltas[0].Text)
	}
	if agg.FinalText != "PONG" {
		t.Fatalf("agg.FinalText = %q, want \"PONG\"", agg.FinalText)
	}
	usage, warnings := harnesses.ResolveFinalUsage(agg.UsageSources)
	if len(warnings) != 0 {
		t.Fatalf("ResolveFinalUsage warnings = %#v, want none", warnings)
	}
	if usage == nil {
		t.Fatal("usage = nil, want native stream usage")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 13505 {
		t.Fatalf("usage.InputTokens = %#v, want 13505", usage.InputTokens)
	}
	if usage.OutputTokens == nil || *usage.OutputTokens != 3 {
		t.Fatalf("usage.OutputTokens = %#v, want 3", usage.OutputTokens)
	}
	if usage.ReasoningTokens == nil || *usage.ReasoningTokens != 18 {
		t.Fatalf("usage.ReasoningTokens = %#v, want 18", usage.ReasoningTokens)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 13526 {
		t.Fatalf("usage.TotalTokens = %#v, want 13526", usage.TotalTokens)
	}
	if usage.Source != harnesses.UsageSourceNativeStream {
		t.Fatalf("usage.Source = %q, want %q", usage.Source, harnesses.UsageSourceNativeStream)
	}
}

func TestParseOpencodeStream_ToolUseEmitsCallAndResult(t *testing.T) {
	data, err := os.ReadFile("testdata/jsonl/tool_use.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan harnesses.Event, 16)
	var seq int64
	agg, err := parseOpencodeStream(context.Background(), bytes.NewReader(data), out, nil, &seq)
	close(out)
	if err != nil {
		t.Fatalf("parseOpencodeStream: %v", err)
	}

	var events []harnesses.Event
	for ev := range out {
		events = append(events, ev)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Type != harnesses.EventTypeToolCall {
		t.Fatalf("events[0].Type = %q, want %q", events[0].Type, harnesses.EventTypeToolCall)
	}
	if events[1].Type != harnesses.EventTypeToolResult {
		t.Fatalf("events[1].Type = %q, want %q", events[1].Type, harnesses.EventTypeToolResult)
	}
	if events[2].Type != harnesses.EventTypeTextDelta {
		t.Fatalf("events[2].Type = %q, want %q", events[2].Type, harnesses.EventTypeTextDelta)
	}

	var call harnesses.ToolCallData
	if err := json.Unmarshal(events[0].Data, &call); err != nil {
		t.Fatalf("unmarshal tool_call: %v", err)
	}
	if call.ID != "call_abc123" {
		t.Fatalf("tool_call.ID = %q, want %q", call.ID, "call_abc123")
	}
	if call.Name != "write" {
		t.Fatalf("tool_call.Name = %q, want %q", call.Name, "write")
	}
	var input struct {
		FilePath string `json:"filePath"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		t.Fatalf("unmarshal tool_call.Input: %v", err)
	}
	if input.FilePath != "hello.txt" {
		t.Fatalf("tool_call.Input.filePath = %q, want %q", input.FilePath, "hello.txt")
	}
	if input.Content != "hello from opencode\n" {
		t.Fatalf("tool_call.Input.content = %q, want %q", input.Content, "hello from opencode\n")
	}

	var result harnesses.ToolResultData
	if err := json.Unmarshal(events[1].Data, &result); err != nil {
		t.Fatalf("unmarshal tool_result: %v", err)
	}
	if result.ID != call.ID {
		t.Fatalf("tool_result.ID = %q, want %q", result.ID, call.ID)
	}
	if !strings.Contains(result.Output, "Wrote file successfully") {
		t.Fatalf("tool_result.Output = %q, want to contain %q", result.Output, "Wrote file successfully")
	}
	if agg.FinalText != "Done." {
		t.Fatalf("agg.FinalText = %q, want %q", agg.FinalText, "Done.")
	}
}

func TestParseOpencodeStream_StepFinishUsage(t *testing.T) {
	input := `{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":13526,"input":13505,"output":3,"reasoning":18,"cache":{"write":100,"read":200}},"cost":0.005}}`
	out := make(chan harnesses.Event, 8)
	var seq int64
	agg, err := parseOpencodeStream(context.Background(), strings.NewReader(input), out, nil, &seq)
	close(out)
	if err != nil {
		t.Fatalf("parseOpencodeStream: %v", err)
	}
	if len(agg.UsageSources) != 1 {
		t.Fatalf("UsageSources len = %d, want 1", len(agg.UsageSources))
	}
	candidate := agg.UsageSources[0]
	if candidate.Source != harnesses.UsageSourceNativeStream {
		t.Fatalf("candidate.Source = %q, want %q", candidate.Source, harnesses.UsageSourceNativeStream)
	}
	if candidate.Fresh == nil || !*candidate.Fresh {
		t.Fatalf("candidate.Fresh = %#v, want true", candidate.Fresh)
	}
	if candidate.Counts.InputTokens == nil || *candidate.Counts.InputTokens != 13505 {
		t.Errorf("InputTokens = %#v, want 13505", candidate.Counts.InputTokens)
	}
	if candidate.Counts.OutputTokens == nil || *candidate.Counts.OutputTokens != 3 {
		t.Errorf("OutputTokens = %#v, want 3", candidate.Counts.OutputTokens)
	}
	if candidate.Counts.ReasoningTokens == nil || *candidate.Counts.ReasoningTokens != 18 {
		t.Errorf("ReasoningTokens = %#v, want 18", candidate.Counts.ReasoningTokens)
	}
	if candidate.Counts.CacheWriteTokens == nil || *candidate.Counts.CacheWriteTokens != 100 {
		t.Errorf("CacheWriteTokens = %#v, want 100", candidate.Counts.CacheWriteTokens)
	}
	if candidate.Counts.CacheReadTokens == nil || *candidate.Counts.CacheReadTokens != 200 {
		t.Errorf("CacheReadTokens = %#v, want 200", candidate.Counts.CacheReadTokens)
	}
	if candidate.Counts.CacheTokens == nil || *candidate.Counts.CacheTokens != 300 {
		t.Errorf("CacheTokens = %#v, want 300", candidate.Counts.CacheTokens)
	}
	if candidate.Counts.TotalTokens == nil || *candidate.Counts.TotalTokens != 13526 {
		t.Errorf("TotalTokens = %#v, want 13526", candidate.Counts.TotalTokens)
	}
	if agg.CostUSD != 0.005 {
		t.Errorf("CostUSD = %f, want 0.005", agg.CostUSD)
	}
	// step_finish emits no text_delta events
	if n := len(out); n != 0 {
		t.Errorf("expected 0 events in channel, got %d", n)
	}
}

func TestParseOpencodeStream_ErrorEventInStream(t *testing.T) {
	input := `{"type":"text","part":{"type":"text","text":"partial"}}` + "\n" +
		`{"type":"error","error":{"name":"APIError","data":{"message":"Model not found"}}}`
	out := make(chan harnesses.Event, 8)
	var seq int64
	_, err := parseOpencodeStream(context.Background(), strings.NewReader(input), out, nil, &seq)
	close(out)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Model not found") {
		t.Fatalf("error = %q, want to contain \"Model not found\"", err.Error())
	}
}
