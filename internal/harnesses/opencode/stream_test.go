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
	if agg.InputTokens != 13505 {
		t.Fatalf("agg.InputTokens = %d, want 13505", agg.InputTokens)
	}
	if agg.OutputTokens != 3 {
		t.Fatalf("agg.OutputTokens = %d, want 3", agg.OutputTokens)
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
	if !agg.HasUsage {
		t.Fatal("HasUsage = false, want true")
	}
	if agg.InputTokens != 13505 {
		t.Errorf("InputTokens = %d, want 13505", agg.InputTokens)
	}
	if agg.OutputTokens != 3 {
		t.Errorf("OutputTokens = %d, want 3", agg.OutputTokens)
	}
	if agg.ReasoningTokens != 18 {
		t.Errorf("ReasoningTokens = %d, want 18", agg.ReasoningTokens)
	}
	if agg.CacheWriteTokens != 100 {
		t.Errorf("CacheWriteTokens = %d, want 100", agg.CacheWriteTokens)
	}
	if agg.CacheReadTokens != 200 {
		t.Errorf("CacheReadTokens = %d, want 200", agg.CacheReadTokens)
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
