package comparison

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/easel/fizeau"
)

func testCostPointer(amount float64) *float64 {
	return &amount
}

func TestBenchmarkEvidenceCostPresence(t *testing.T) {
	t.Run("source-tagged ingress is normalized and cloned", func(t *testing.T) {
		tests := []struct {
			name        string
			result      RunResult
			wantPresent bool
			wantAmount  float64
			wantSource  fizeau.CostSource
		}{
			{name: "unknown", result: RunResult{CostUSD: 7, CostSource: fizeau.CostSourceUnknown}, wantSource: fizeau.CostSourceUnknown},
			{name: "known zero", result: RunResult{CostSource: fizeau.CostSourceReported}, wantPresent: true, wantSource: fizeau.CostSourceReported},
			{name: "positive configured", result: RunResult{CostUSD: 1.25, CostSource: fizeau.CostSourceConfigured}, wantPresent: true, wantAmount: 1.25, wantSource: fizeau.CostSourceConfigured},
			{name: "negative", result: RunResult{CostUSD: -1, CostSource: fizeau.CostSourceReported}, wantSource: fizeau.CostSourceUnknown},
			{name: "invalid source", result: RunResult{CostUSD: 2, CostSource: fizeau.CostSource("estimated")}, wantSource: fizeau.CostSourceUnknown},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ingress := tc.result
				arm := runCompareArm(func(_, _, _ string) RunResult {
					return ingress
				}, CompareOptions{}, 0, "test", "", "", "prompt", "")

				if (arm.CostUSD != nil) != tc.wantPresent {
					t.Fatalf("cost presence = %v, want %v", arm.CostUSD != nil, tc.wantPresent)
				}
				if arm.CostSource != tc.wantSource {
					t.Fatalf("cost source = %q, want %q", arm.CostSource, tc.wantSource)
				}
				if arm.CostUSD != nil && *arm.CostUSD != tc.wantAmount {
					t.Fatalf("cost = %v, want %v", *arm.CostUSD, tc.wantAmount)
				}

				ingress.CostUSD = 99
				if arm.CostUSD != nil && *arm.CostUSD != tc.wantAmount {
					t.Fatalf("mapped arm aliases ingress after mutation: got %v", *arm.CostUSD)
				}
			})
		}
	})

	t.Run("arm JSON normalizes raw and legacy evidence", func(t *testing.T) {
		tests := []struct {
			name        string
			raw         string
			wantPresent bool
			wantAmount  float64
			wantSource  fizeau.CostSource
		}{
			{name: "source-less legacy", raw: `{"harness":"a","output":"","cost_usd":4.5,"duration_ms":0,"exit_code":0}`, wantSource: fizeau.CostSourceUnknown},
			{name: "unknown", raw: `{"harness":"a","output":"","cost_usd":8,"cost_source":"unknown","duration_ms":0,"exit_code":0}`, wantSource: fizeau.CostSourceUnknown},
			{name: "absent amount", raw: `{"harness":"a","output":"","cost_source":"reported","duration_ms":0,"exit_code":0}`, wantSource: fizeau.CostSourceUnknown},
			{name: "known zero", raw: `{"harness":"a","output":"","cost_usd":0,"cost_source":"reported","duration_ms":0,"exit_code":0}`, wantPresent: true, wantSource: fizeau.CostSourceReported},
			{name: "positive reported", raw: `{"harness":"a","output":"","cost_usd":1.5,"cost_source":"reported","duration_ms":0,"exit_code":0}`, wantPresent: true, wantAmount: 1.5, wantSource: fizeau.CostSourceReported},
			{name: "positive", raw: `{"harness":"a","output":"","cost_usd":2.75,"cost_source":"configured","duration_ms":0,"exit_code":0}`, wantPresent: true, wantAmount: 2.75, wantSource: fizeau.CostSourceConfigured},
			{name: "negative", raw: `{"harness":"a","output":"","cost_usd":-1,"cost_source":"reported","duration_ms":0,"exit_code":0}`, wantSource: fizeau.CostSourceUnknown},
			{name: "invalid source", raw: `{"harness":"a","output":"","cost_usd":1,"cost_source":"estimated","duration_ms":0,"exit_code":0}`, wantSource: fizeau.CostSourceUnknown},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var arm ComparisonArm
				if err := json.Unmarshal([]byte(tc.raw), &arm); err != nil {
					t.Fatalf("unmarshal arm: %v", err)
				}
				if (arm.CostUSD != nil) != tc.wantPresent || arm.CostSource != tc.wantSource {
					t.Fatalf("decoded cost = (%v, %q), want present=%v source=%q", arm.CostUSD, arm.CostSource, tc.wantPresent, tc.wantSource)
				}
				if arm.CostUSD != nil && *arm.CostUSD != tc.wantAmount {
					t.Fatalf("decoded cost = %v, want %v", *arm.CostUSD, tc.wantAmount)
				}

				reencoded, err := json.Marshal(arm)
				if err != nil {
					t.Fatalf("marshal arm: %v", err)
				}
				var wire map[string]json.RawMessage
				if err := json.Unmarshal(reencoded, &wire); err != nil {
					t.Fatalf("decode re-emitted arm: %v", err)
				}
				if got := string(wire["cost_source"]); got != `"`+string(tc.wantSource)+`"` {
					t.Fatalf("re-emitted cost_source = %s, want %q", got, tc.wantSource)
				}
				_, emittedCost := wire["cost_usd"]
				if emittedCost != tc.wantPresent {
					t.Fatalf("re-emitted cost presence = %v, want %v: %s", emittedCost, tc.wantPresent, reencoded)
				}
			})
		}
	})

	t.Run("summary requires every contribution", func(t *testing.T) {
		configuredZeroA := testCostPointer(0)
		configuredZeroB := testCostPointer(0)
		mixedConfigured := testCostPointer(0.2)
		mixedReported := testCostPointer(0.3)
		knownBeforeUnknown := testCostPointer(4)
		result := &BenchmarkResult{
			Arms: []BenchmarkArm{{Label: "zero"}, {Label: "mixed"}, {Label: "unknown"}},
			Comparisons: []ComparisonRecord{
				{Arms: []ComparisonArm{
					{Harness: "zero", CostUSD: configuredZeroA, CostSource: fizeau.CostSourceConfigured},
					{Harness: "mixed", CostUSD: mixedConfigured, CostSource: fizeau.CostSourceConfigured},
					{Harness: "unknown", CostUSD: knownBeforeUnknown, CostSource: fizeau.CostSourceReported},
				}},
				{Arms: []ComparisonArm{
					{Harness: "zero", CostUSD: configuredZeroB, CostSource: fizeau.CostSourceConfigured},
					{Harness: "mixed", CostUSD: mixedReported, CostSource: fizeau.CostSourceReported},
					{Harness: "unknown", CostSource: fizeau.CostSourceUnknown},
				}},
			},
		}

		summary := summarizeBenchmark(result)
		if len(summary.Arms) != 3 {
			t.Fatalf("summary arms = %d, want 3", len(summary.Arms))
		}
		zero := summary.Arms[0]
		if zero.TotalCostUSD == nil || *zero.TotalCostUSD != 0 || zero.CostSource != fizeau.CostSourceConfigured {
			t.Fatalf("zero total = (%v, %q), want (0, configured)", zero.TotalCostUSD, zero.CostSource)
		}
		mixed := summary.Arms[1]
		if mixed.TotalCostUSD == nil || math.Abs(*mixed.TotalCostUSD-0.5) > 1e-12 || mixed.CostSource != fizeau.CostSourceReported {
			t.Fatalf("mixed total = (%v, %q), want (0.5, reported)", mixed.TotalCostUSD, mixed.CostSource)
		}
		unknown := summary.Arms[2]
		if unknown.TotalCostUSD != nil || unknown.CostSource != fizeau.CostSourceUnknown {
			t.Fatalf("unknown total = (%v, %q), want (nil, unknown)", unknown.TotalCostUSD, unknown.CostSource)
		}

		*mixedConfigured = 99
		if *mixed.TotalCostUSD != 0.5 {
			t.Fatalf("summary aliases contributing arm after mutation: got %v", *mixed.TotalCostUSD)
		}

		encoded, err := json.Marshal(summary)
		if err != nil {
			t.Fatalf("marshal summary: %v", err)
		}
		var wire struct {
			Arms []map[string]json.RawMessage `json:"arms"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatalf("decode summary JSON: %v", err)
		}
		if _, ok := wire.Arms[0]["total_cost_usd"]; !ok {
			t.Fatal("known zero total omitted from JSON")
		}
		if _, ok := wire.Arms[2]["total_cost_usd"]; ok {
			t.Fatal("unknown total emitted as scalar JSON")
		}
		if got := string(wire.Arms[2]["cost_source"]); got != `"unknown"` {
			t.Fatalf("unknown summary source = %s, want unknown", got)
		}
	})

	t.Run("source-less legacy summary re-emits unknown", func(t *testing.T) {
		var summary BenchmarkArmSummary
		if err := json.Unmarshal([]byte(`{"label":"legacy","total_cost_usd":3}`), &summary); err != nil {
			t.Fatalf("unmarshal legacy summary: %v", err)
		}
		if summary.TotalCostUSD != nil || summary.CostSource != fizeau.CostSourceUnknown {
			t.Fatalf("legacy summary cost = (%v, %q), want nil unknown", summary.TotalCostUSD, summary.CostSource)
		}
		encoded, err := json.Marshal(summary)
		if err != nil {
			t.Fatalf("marshal legacy summary: %v", err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatalf("decode legacy summary output: %v", err)
		}
		if _, ok := wire["total_cost_usd"]; ok || string(wire["cost_source"]) != `"unknown"` {
			t.Fatalf("legacy summary re-emitted without normalization: %s", encoded)
		}
	})
}
