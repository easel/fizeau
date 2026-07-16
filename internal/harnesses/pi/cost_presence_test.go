package pi

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestPiCostPresence(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantInput  int
		wantOutput int
		wantKnown  bool
		wantCost   float64
	}{
		{
			name:       "absent",
			input:      `{"type":"text_end","message":{"usage":{"input":11,"output":1}}}`,
			wantInput:  11,
			wantOutput: 1,
		},
		{
			name:       "negative",
			input:      `{"type":"text_delta","partial":{"usage":{"input":12,"output":2,"cost":{"total":-0.01}}}}`,
			wantInput:  12,
			wantOutput: 2,
		},
		{
			name:       "zero",
			input:      `{"type":"text_end","message":{"usage":{"input":13,"output":3,"cost":{"total":0}}}}`,
			wantInput:  13,
			wantOutput: 3,
			wantKnown:  true,
		},
		{
			name:       "positive",
			input:      `{"type":"text_delta","partial":{"usage":{"input":14,"output":4,"cost":{"total":0.0123}}}}`,
			wantInput:  14,
			wantOutput: 4,
			wantKnown:  true,
			wantCost:   0.0123,
		},
		{
			name: "newer absent clears older positive",
			input: `{"type":"text_delta","partial":{"usage":{"input":1,"output":1,"cost":{"total":0.0123}}}}` + "\n" +
				`{"type":"text_end","message":{"usage":{"input":15,"output":5}}}`,
			wantInput:  15,
			wantOutput: 5,
		},
		{
			name: "newer negative clears older positive",
			input: `{"type":"text_end","message":{"usage":{"input":1,"output":1,"cost":{"total":0.0123}}}}` + "\n" +
				`{"type":"text_delta","partial":{"usage":{"input":16,"output":6,"cost":{"total":-0.01}}}}`,
			wantInput:  16,
			wantOutput: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := make(chan harnesses.Event, 8)
			var seq int64
			agg, err := parsePiStream(context.Background(), strings.NewReader(tt.input), out, nil, &seq)
			if err != nil {
				t.Fatalf("parsePiStream: %v", err)
			}
			if !agg.HasUsage {
				t.Fatal("expected newest usage envelope to be retained")
			}
			if agg.InputTokens != tt.wantInput || agg.OutputTokens != tt.wantOutput {
				t.Fatalf("usage: got input=%d output=%d, want input=%d output=%d", agg.InputTokens, agg.OutputTokens, tt.wantInput, tt.wantOutput)
			}
			assertPiCostState(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, tt.wantKnown, tt.wantCost)
		})
	}
}

func assertPiCostState(t *testing.T, cost *float64, source harnesses.CostSource, scalar float64, wantKnown bool, wantCost float64) {
	t.Helper()
	if !wantKnown {
		if cost != nil {
			t.Fatalf("FinalCostUSD: got %v, want nil", *cost)
		}
		if source != harnesses.CostSourceUnknown {
			t.Fatalf("CostSource: got %q, want %q", source, harnesses.CostSourceUnknown)
		}
		if scalar != 0 {
			t.Fatalf("CostUSD: got %v, want 0", scalar)
		}
		return
	}

	if cost == nil {
		t.Fatal("FinalCostUSD: got nil, want reported cost")
	}
	if math.Abs(*cost-wantCost) > 1e-12 {
		t.Fatalf("FinalCostUSD: got %v, want %v", *cost, wantCost)
	}
	if source != harnesses.CostSourceReported {
		t.Fatalf("CostSource: got %q, want %q", source, harnesses.CostSourceReported)
	}
	if math.Abs(scalar-wantCost) > 1e-12 {
		t.Fatalf("CostUSD: got %v, want %v", scalar, wantCost)
	}
}

func assertPiFinalCostJSON(t *testing.T, raw json.RawMessage, wantKnown bool, wantCost float64, wantSource harnesses.CostSource) {
	t.Helper()
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal final JSON: %v", err)
	}
	var source harnesses.CostSource
	if err := json.Unmarshal(wire["cost_source"], &source); err != nil {
		t.Fatalf("decode cost_source: %v", err)
	}
	if source != wantSource {
		t.Fatalf("cost_source: got %q, want %q", source, wantSource)
	}
	costRaw, ok := wire["cost_usd"]
	if !wantKnown {
		if ok {
			t.Fatalf("unknown cost must omit cost_usd: %s", raw)
		}
		return
	}
	if !ok {
		t.Fatalf("known cost must include cost_usd: %s", raw)
	}
	var cost float64
	if err := json.Unmarshal(costRaw, &cost); err != nil {
		t.Fatalf("decode cost_usd: %v", err)
	}
	if math.Abs(cost-wantCost) > 1e-12 {
		t.Fatalf("cost_usd: got %v, want %v", cost, wantCost)
	}
}
