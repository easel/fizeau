package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunFinalCostAggregation(t *testing.T) {
	amount := func(value float64) *float64 {
		return &value
	}
	response := func(source CostSource, value *float64, continues bool) Response {
		resp := Response{
			Content: "done",
			Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
			Model:   "cost-model",
			Attempt: &AttemptMetadata{
				ProviderName:  "test",
				ResolvedModel: "cost-model",
				Cost: &CostAttribution{
					Source: source,
					Amount: value,
				},
			},
		}
		if continues {
			resp.Content = ""
			resp.ToolCalls = []ToolCall{{ID: "continue", Name: "read", Arguments: json.RawMessage(`{}`)}}
		}
		return resp
	}

	tests := []struct {
		name       string
		responses  []Response
		wantAmount *float64
		wantSource SessionCostSource
	}{
		{
			name:       "unknown",
			responses:  []Response{response(CostSourceUnknown, nil, false)},
			wantSource: SessionCostSourceUnknown,
		},
		{
			name:       "negative amount is unknown",
			responses:  []Response{response(CostSourceProviderReported, amount(-1), false)},
			wantSource: SessionCostSourceUnknown,
		},
		{
			name: "any unknown constituent poisons aggregate",
			responses: []Response{
				response(CostSourceConfigured, amount(0.01), true),
				response(CostSourceUnknown, nil, false),
			},
			wantSource: SessionCostSourceUnknown,
		},
		{
			name: "unknown first still poisons aggregate",
			responses: []Response{
				response(CostSourceUnknown, nil, true),
				response(CostSourceProviderReported, amount(0.01), false),
			},
			wantSource: SessionCostSourceUnknown,
		},
		{
			name:       "explicit configured zero",
			responses:  []Response{response(CostSourceConfigured, amount(0), false)},
			wantAmount: amount(0),
			wantSource: SessionCostSourceConfigured,
		},
		{
			name:       "positive configured",
			responses:  []Response{response(CostSourceConfigured, amount(0.02), false)},
			wantAmount: amount(0.02),
			wantSource: SessionCostSourceConfigured,
		},
		{
			name:       "positive provider reported",
			responses:  []Response{response(CostSourceProviderReported, amount(0.03), false)},
			wantAmount: amount(0.03),
			wantSource: SessionCostSourceReported,
		},
		{
			name:       "positive gateway reported",
			responses:  []Response{response(CostSourceGatewayReported, amount(0.04), false)},
			wantAmount: amount(0.04),
			wantSource: SessionCostSourceReported,
		},
		{
			name: "reported wins all-known mixture",
			responses: []Response{
				response(CostSourceConfigured, amount(0.01), true),
				response(CostSourceProviderReported, amount(0.02), false),
			},
			wantAmount: amount(0.03),
			wantSource: SessionCostSourceReported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &mockProvider{responses: test.responses}
			var sessionEnd map[string]any
			result, err := Run(context.Background(), Request{
				Prompt:   "measure cost",
				Provider: provider,
				Tools:    []Tool{&mockTool{name: "read", result: "ok"}},
				Callback: func(event Event) {
					if event.Type == EventSessionEnd {
						require.NoError(t, json.Unmarshal(event.Data, &sessionEnd))
					}
				},
			})
			require.NoError(t, err)
			assert.Equal(t, StatusSuccess, result.Status)
			assert.Equal(t, test.wantSource, result.FinalCostSource)
			require.NotNil(t, sessionEnd)
			assert.Equal(t, string(test.wantSource), sessionEnd["cost_source"])

			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(encoded, &payload))
			assert.Equal(t, string(test.wantSource), payload["cost_source"])

			if test.wantAmount == nil {
				assert.Nil(t, result.FinalCostUSD)
				assert.Zero(t, result.CostUSD, "compatibility mirror must not expose an unknown sentinel")
				_, present := payload["cost_usd"]
				assert.False(t, present, "unknown result JSON must omit cost_usd")
				_, present = sessionEnd["cost_usd"]
				assert.False(t, present, "unknown session.end must omit cost_usd")
				assert.NotContains(t, string(encoded), `"cost_usd":-1`)
				return
			}

			require.NotNil(t, result.FinalCostUSD)
			assert.InDelta(t, *test.wantAmount, *result.FinalCostUSD, 1e-12)
			assert.InDelta(t, *test.wantAmount, result.CostUSD, 1e-12)
			serialized, present := payload["cost_usd"]
			require.True(t, present, "known zero and positive costs must be serialized")
			assert.InDelta(t, *test.wantAmount, serialized.(float64), 1e-12)
			serialized, present = sessionEnd["cost_usd"]
			require.True(t, present, "known zero and positive costs must reach session.end")
			assert.InDelta(t, *test.wantAmount, serialized.(float64), 1e-12)
		})
	}
}
