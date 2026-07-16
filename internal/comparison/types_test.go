package comparison

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/easel/fizeau"
)

func TestRunResultCostSourceIngressCompile(t *testing.T) {
	tests := []struct {
		name   string
		result RunResult
	}{
		{name: "unknown", result: RunResult{CostSource: fizeau.CostSourceUnknown}},
		{name: "known zero", result: RunResult{CostUSD: 0, CostSource: fizeau.CostSourceReported}},
		{name: "positive", result: RunResult{CostUSD: 1.25, CostSource: fizeau.CostSourceConfigured}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.CostSource == "" {
				t.Fatal("CostSource must retain its tagged ingress value")
			}

			encoded, err := json.Marshal(tt.result)
			if err != nil {
				t.Fatalf("marshal RunResult: %v", err)
			}
			if strings.Contains(string(encoded), "CostSource") || strings.Contains(string(encoded), "cost_source") {
				t.Fatalf("RunResult CostSource changed serialized output: %s", encoded)
			}
		})
	}
}
