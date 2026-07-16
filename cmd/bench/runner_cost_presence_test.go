package main

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	fizeau "github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/comparison"
	"github.com/easel/fizeau/internal/harnesses"
)

func TestBenchmarkFinalCostPresence(t *testing.T) {
	type costCase struct {
		name        string
		raw         string
		wantPresent bool
		wantAmount  float64
		wantSource  fizeau.CostSource
	}

	cases := []costCase{
		{
			name:       "unknown",
			raw:        `{"status":"success","cost_source":"unknown"}`,
			wantSource: fizeau.CostSourceUnknown,
		},
		{
			name:        "explicit reported zero",
			raw:         `{"status":"success","cost_usd":0,"cost_source":"reported"}`,
			wantPresent: true,
			wantSource:  fizeau.CostSourceReported,
		},
		{
			name:        "positive reported",
			raw:         `{"status":"success","cost_usd":0.2,"cost_source":"reported"}`,
			wantPresent: true,
			wantAmount:  0.2,
			wantSource:  fizeau.CostSourceReported,
		},
		{
			name:        "positive configured",
			raw:         `{"status":"success","cost_usd":0.3,"cost_source":"configured"}`,
			wantPresent: true,
			wantAmount:  0.3,
			wantSource:  fizeau.CostSourceConfigured,
		},
		{
			name:       "legacy scalar has no authoritative presence",
			raw:        `{"status":"success","cost_usd":9.5}`,
			wantSource: fizeau.CostSourceUnknown,
		},
	}

	var accumulated float64
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var final harnesses.FinalData
			if err := json.Unmarshal([]byte(tc.raw), &final); err != nil {
				t.Fatalf("decode raw final: %v", err)
			}

			taskCost := benchmarkTaskCostFromFinal(final)
			result := comparison.RunResult{CostSource: fizeau.CostSourceUnknown}
			amount, present := taskCost.populateComparisonResult(&result)
			if present != tc.wantPresent {
				t.Fatalf("cost presence = %v, want %v", present, tc.wantPresent)
			}
			if result.CostSource != tc.wantSource {
				t.Fatalf("comparison cost source = %q, want %q", result.CostSource, tc.wantSource)
			}
			if result.CostUSD != tc.wantAmount {
				t.Fatalf("comparison cost = %v, want %v", result.CostUSD, tc.wantAmount)
			}
			if present {
				accumulated += amount
			}
		})
	}

	if math.Abs(accumulated-0.5) > 1e-12 {
		t.Fatalf("accumulated authoritative cost = %v, want 0.5", accumulated)
	}
	if accumulated < 0.5 {
		t.Fatalf("cost cap should be reached at 0.5, accumulated %v", accumulated)
	}

	t.Run("production uses provenance as presence", func(t *testing.T) {
		source, err := os.ReadFile("runner.go")
		if err != nil {
			t.Fatalf("read runner.go: %v", err)
		}
		text := string(source)
		if strings.Contains(text, "fd.CostUSD") {
			t.Fatal("runner.go must not consume the deprecated FinalData.CostUSD scalar")
		}
		if strings.Contains(text, "result.CostUSD > 0") {
			t.Fatal("runner.go must not infer cost presence from a positive comparison scalar")
		}
		if !strings.Contains(text, "fd.FinalCostUSD") || !strings.Contains(text, "fd.FinalCostSource") {
			t.Fatal("runner.go must consume the authoritative final amount and source together")
		}
	})
}
