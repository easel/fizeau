package gemini

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestGeminiCostPresence(t *testing.T) {
	tests := []struct {
		name      string
		costJSON  string
		wantKnown bool
		wantCost  float64
	}{
		{name: "absent"},
		{name: "cost_usd zero", costJSON: `,"cost_usd":0`, wantKnown: true},
		{name: "cost_usd positive", costJSON: `,"cost_usd":0.0123`, wantKnown: true, wantCost: 0.0123},
		{name: "total_cost_usd zero", costJSON: `,"total_cost_usd":0`, wantKnown: true},
		{name: "total_cost_usd positive", costJSON: `,"total_cost_usd":0.0456`, wantKnown: true, wantCost: 0.0456},
		{name: "zero primary wins positive total", costJSON: `,"cost_usd":0,"total_cost_usd":0.0456`, wantKnown: true},
		{name: "negative total is unknown", costJSON: `,"total_cost_usd":-0.01`},
		{name: "negative primary blocks positive total", costJSON: `,"cost_usd":-0.01,"total_cost_usd":0.0456`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := `{"type":"result","status":"success","stats":{"input_tokens":1` + tt.costJSON + `}}`
			agg, ok := parseGeminiStreamOutput(output)
			if !ok {
				t.Fatal("expected stream-json output")
			}
			assertGeminiCostState(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, tt.wantKnown, tt.wantCost)
		})
	}

	t.Run("later stats replaces reported cost with unknown", func(t *testing.T) {
		output := `{"type":"result","status":"success","stats":{"cost_usd":0.0123}}` + "\n" +
			`{"type":"result","status":"success","stats":{"input_tokens":1}}`
		agg, ok := parseGeminiStreamOutput(output)
		if !ok {
			t.Fatal("expected stream-json output")
		}
		assertGeminiCostState(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, false, 0)
	})

	t.Run("later stats replaces unknown cost with reported", func(t *testing.T) {
		output := `{"type":"result","status":"success","stats":{"total_cost_usd":-1}}` + "\n" +
			`{"type":"result","status":"success","stats":{"cost_usd":0}}`
		agg, ok := parseGeminiStreamOutput(output)
		if !ok {
			t.Fatal("expected stream-json output")
		}
		assertGeminiCostState(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, true, 0)
	})
}

func assertGeminiCostState(t *testing.T, cost *float64, source harnesses.CostSource, scalar float64, wantKnown bool, wantCost float64) {
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

func assertGeminiFinalCostJSON(t *testing.T, raw json.RawMessage, wantKnown bool, wantCost float64, wantSource harnesses.CostSource) {
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
