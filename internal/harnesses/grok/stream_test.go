package grok

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// grokCapturedSample is real grok 0.2.106 streaming-json output (thought
// deltas trimmed for brevity, values verbatim) from:
//
//	grok -p "Reply with exactly the word: hello" --output-format streaming-json --max-turns 1
const grokCapturedSample = `{"type":"thought","data":"The"}
{"type":"thought","data":" user"}
{"type":"thought","data":" wants"}
{"type":"text","data":"hello"}
{"type":"end","stopReason":"EndTurn","sessionId":"019f9009-b4e7-7502-b35c-3b9ba47cfc14","requestId":"43c05f08-04e9-4def-b351-b541a1f2c53c","usage":{"input_tokens":11368,"cache_read_input_tokens":5248,"output_tokens":31,"reasoning_tokens":26,"total_tokens":16647},"num_turns":1,"total_cost_usd":0.0244964,"total_cost_usd_ticks":244964000,"modelUsage":{"grok-4.5-build":{"inputTokens":11368,"outputTokens":31,"cacheReadInputTokens":5248,"modelCalls":1,"costUSD":0.0244964}}}
`

func collectEvents(t *testing.T, input string) (*streamAggregate, []harnesses.Event) {
	t.Helper()
	out := make(chan harnesses.Event, 256)
	var seq int64
	agg, err := parseGrokStream(context.Background(), strings.NewReader(input), out, nil, &seq)
	if err != nil {
		t.Fatalf("parseGrokStream: %v", err)
	}
	close(out)
	var events []harnesses.Event
	for ev := range out {
		events = append(events, ev)
	}
	return agg, events
}

func TestParseGrokStreamCapturedSample(t *testing.T) {
	agg, events := collectEvents(t, grokCapturedSample)

	if got := agg.FinalText(); got != "hello" {
		t.Errorf("FinalText = %q, want %q", got, "hello")
	}
	if agg.StopReason != "EndTurn" {
		t.Errorf("StopReason = %q, want EndTurn", agg.StopReason)
	}
	if agg.SessionID != "019f9009-b4e7-7502-b35c-3b9ba47cfc14" {
		t.Errorf("SessionID = %q", agg.SessionID)
	}
	if agg.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", agg.TurnCount)
	}
	if agg.IsError {
		t.Error("IsError = true, want false")
	}

	if agg.FinalCostUSD == nil || *agg.FinalCostUSD != 0.0244964 {
		t.Errorf("FinalCostUSD = %v, want 0.0244964", agg.FinalCostUSD)
	}
	if agg.CostSource != harnesses.CostSourceReported {
		t.Errorf("CostSource = %q, want reported", agg.CostSource)
	}

	usage, _ := harnesses.ResolveFinalUsage(agg.UsageSources)
	if usage == nil {
		t.Fatal("resolved usage is nil")
	}
	if usage.InputTokens == nil || *usage.InputTokens != 11368 {
		t.Errorf("InputTokens = %v, want 11368", usage.InputTokens)
	}
	if usage.OutputTokens == nil || *usage.OutputTokens != 31 {
		t.Errorf("OutputTokens = %v, want 31", usage.OutputTokens)
	}
	if usage.CacheReadTokens == nil || *usage.CacheReadTokens != 5248 {
		t.Errorf("CacheReadTokens = %v, want 5248", usage.CacheReadTokens)
	}
	if usage.ReasoningTokens == nil || *usage.ReasoningTokens != 26 {
		t.Errorf("ReasoningTokens = %v, want 26", usage.ReasoningTokens)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 16647 {
		t.Errorf("TotalTokens = %v, want 16647", usage.TotalTokens)
	}

	// One TextDelta for "hello"; thought deltas are not surfaced.
	var textEvents []harnesses.Event
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeTextDelta {
			textEvents = append(textEvents, ev)
		}
	}
	if len(textEvents) != 1 {
		t.Fatalf("got %d text_delta events, want 1", len(textEvents))
	}
	var delta harnesses.TextDeltaData
	if err := json.Unmarshal(textEvents[0].Data, &delta); err != nil {
		t.Fatalf("decode text delta: %v", err)
	}
	if delta.Text != "hello" {
		t.Errorf("delta text = %q, want hello", delta.Text)
	}
}

func TestParseGrokStreamAccumulatesTokenDeltas(t *testing.T) {
	input := `{"type":"text","data":"Here's"}
{"type":"text","data":" a"}
{"type":"text","data":" summary"}
{"type":"end","stopReason":"EndTurn","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13},"num_turns":1}
`
	agg, events := collectEvents(t, input)
	if got := agg.FinalText(); got != "Here's a summary" {
		t.Errorf("FinalText = %q, want %q", got, "Here's a summary")
	}
	count := 0
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeTextDelta {
			count++
		}
	}
	if count != 3 {
		t.Errorf("got %d text_delta events, want 3", count)
	}
	if agg.FinalCostUSD != nil {
		t.Errorf("FinalCostUSD = %v, want nil (no cost reported)", agg.FinalCostUSD)
	}
	if agg.CostSource != harnesses.CostSourceUnknown {
		t.Errorf("CostSource = %q, want unknown", agg.CostSource)
	}
}

func TestParseGrokStreamErrorEvent(t *testing.T) {
	input := `{"type":"error","message":"Couldn't start session: boom"}
`
	agg, _ := collectEvents(t, input)
	if !agg.IsError {
		t.Fatal("IsError = false, want true")
	}
	if agg.ErrorMessage != "Couldn't start session: boom" {
		t.Errorf("ErrorMessage = %q", agg.ErrorMessage)
	}
}

func TestParseGrokStreamSkipsUnknownAndMalformed(t *testing.T) {
	input := `{"type":"max_turns_reached"}
{"type":"auto_compact_started"}
not json at all
{"type":"text","data":"ok"}
{"type":"end","stopReason":"EndTurn","num_turns":2}
`
	agg, _ := collectEvents(t, input)
	if got := agg.FinalText(); got != "ok" {
		t.Errorf("FinalText = %q, want ok", got)
	}
	if agg.TurnCount != 2 {
		t.Errorf("TurnCount = %d, want 2", agg.TurnCount)
	}
}

func TestParseGrokStreamContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := make(chan harnesses.Event) // unbuffered: emit would block without ctx
	var seq int64
	input := `{"type":"text","data":"never"}
`
	_, err := parseGrokStream(ctx, strings.NewReader(input), out, nil, &seq)
	if err == nil {
		t.Fatal("expected context error")
	}
}
