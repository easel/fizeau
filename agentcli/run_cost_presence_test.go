package agentcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/easel/fizeau"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteViaServicePreservesFinalCostPresence(t *testing.T) {
	tests := []struct {
		name       string
		wireCost   *float64
		wireSource fizeau.CostSource
		wantCost   *float64
		wantSource fizeau.CostSource
	}{
		{
			name:       "source-less scalar remains unknown",
			wireCost:   costPointer(9.75),
			wireSource: fizeau.CostSourceUnknown,
			wantCost:   nil,
			wantSource: fizeau.CostSourceUnknown,
		},
		{
			name:       "reported zero remains present",
			wireCost:   costPointer(0),
			wireSource: fizeau.CostSourceReported,
			wantCost:   costPointer(0),
			wantSource: fizeau.CostSourceReported,
		},
		{
			name:       "configured positive remains present",
			wireCost:   costPointer(1.25),
			wireSource: fizeau.CostSourceConfigured,
			wantCost:   costPointer(1.25),
			wantSource: fizeau.CostSourceConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldNewService := newServiceFn
			t.Cleanup(func() { newServiceFn = oldNewService })
			newServiceFn = func(fizeau.ServiceOptions) (fizeau.FizeauService, error) {
				return stubFizeauService{
					executeFn: func(context.Context, fizeau.ServiceExecuteRequest) (<-chan fizeau.ServiceEvent, error) {
						ch := make(chan fizeau.ServiceEvent, 1)
						ch <- finalServiceEvent(t, fizeau.ServiceFinalData{
							Status:     "success",
							FinalText:  "answer",
							CostUSD:    tt.wireCost,
							CostSource: tt.wireSource,
						})
						close(ch)
						return ch, nil
					},
				}, nil
			}

			result, err := executeViaService(
				context.Background(),
				fizeau.ServiceExecuteRequest{},
				providerSelection{},
				"",
				nil,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSource, result.CostSource)
			if tt.wantCost == nil {
				assert.Nil(t, result.CostUSD)
			} else {
				require.NotNil(t, result.CostUSD)
				assert.Equal(t, *tt.wantCost, *result.CostUSD)
			}
		})
	}
}

func TestRunCostOutputUsesPresence(t *testing.T) {
	tests := []struct {
		name       string
		jsonOutput bool
		cost       *float64
		source     fizeau.CostSource
		cap        string
		wantCost   string
		wantCap    string
	}{
		{
			name:   "human unknown omits cost",
			cost:   nil,
			source: fizeau.CostSourceUnknown,
		},
		{
			name:    "human unknown still shows cap",
			cost:    nil,
			source:  fizeau.CostSourceUnknown,
			cap:     "2",
			wantCap: " | cap $2.0000",
		},
		{
			name:     "human reported zero prints cost",
			cost:     costPointer(0),
			source:   fizeau.CostSourceReported,
			wantCost: " | cost: $0.0000",
		},
		{
			name:     "human configured positive prints cost and cap",
			cost:     costPointer(1.25),
			source:   fizeau.CostSourceConfigured,
			cap:      "2",
			wantCost: " | cost: $1.2500",
			wantCap:  " / cap $2.0000",
		},
		{
			name:       "json unknown omits amount and carries source",
			jsonOutput: true,
			cost:       nil,
			source:     fizeau.CostSourceUnknown,
		},
		{
			name:       "json reported zero includes amount and source",
			jsonOutput: true,
			cost:       costPointer(0),
			source:     fizeau.CostSourceReported,
			wantCost:   " | cost: $0.0000",
		},
		{
			name:       "json configured positive includes amount and source",
			jsonOutput: true,
			cost:       costPointer(1.25),
			source:     fizeau.CostSourceConfigured,
			wantCost:   " | cost: $1.2500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldExecuteViaService := executeViaServiceFn
			t.Cleanup(func() { executeViaServiceFn = oldExecuteViaService })
			executeViaServiceFn = func(context.Context, fizeau.ServiceExecuteRequest, providerSelection, string, fizeau.ServiceConfig) (cliExecutionResult, error) {
				return cliExecutionResult{
					Status:     "success",
					Output:     "answer",
					CostUSD:    tt.cost,
					CostSource: tt.source,
				}, nil
			}

			isolateCatalogHome(t)
			configureRunEnv(t, "test-model")
			t.Setenv("FIZEAU_COST_CAP_USD", "")
			args := []string{"run", "--harness", "codex", "--work-dir", t.TempDir(), "-p", "hello"}
			if tt.jsonOutput {
				args = append(args, "--json")
			}
			if tt.cap != "" {
				args = append(args, "--cost-cap-usd", tt.cap)
			}

			stdout, stderr, code := captureStdIO(t, func() int {
				return Run(Options{Args: args})
			})
			require.Equal(t, 0, code, "stdout=%s stderr=%s", stdout, stderr)
			assert.Contains(t, stderr, "[success] tokens: 0 in / 0 out")
			if tt.wantCost == "" {
				assert.NotContains(t, stderr, " | cost:")
			} else {
				assert.Contains(t, stderr, tt.wantCost)
			}
			if tt.wantCap == "" {
				assert.NotContains(t, stderr, " cap $")
			} else {
				assert.Contains(t, stderr, tt.wantCap)
			}

			if !tt.jsonOutput {
				assert.Equal(t, "answer\n", stdout)
				return
			}

			require.True(t, json.Valid([]byte(stdout)), "stdout must contain only JSON: %q", stdout)
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(stdout), &raw))
			assert.JSONEq(t, `"answer"`, string(raw["output"]))
			assert.JSONEq(t, `"`+string(tt.source)+`"`, string(raw["cost_source"]))
			costRaw, hasCost := raw["cost_usd"]
			if tt.cost == nil {
				assert.False(t, hasCost, "unknown cost must be omitted from raw JSON: %s", stdout)
				assert.NotContains(t, stdout, `"cost_usd"`)
			} else {
				require.True(t, hasCost, "known cost must be present in raw JSON: %s", stdout)
				var gotCost float64
				require.NoError(t, json.Unmarshal(costRaw, &gotCost))
				assert.Equal(t, *tt.cost, gotCost)
				assert.Contains(t, stdout, `"cost_usd"`)
			}
			assert.True(t, strings.HasSuffix(stdout, "\n"), "JSON output should end with a newline")
		})
	}
}

func costPointer(value float64) *float64 {
	return &value
}
