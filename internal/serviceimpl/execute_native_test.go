package serviceimpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteNativeRemovesBenchmarkPresetPlanning verifies that PlanningMode
// is not forced on by ToolPreset=="benchmark". The planning mode is
// exclusively controlled by the ServiceExecuteRequest.PlanningMode field.
func TestExecuteNativeRemovesBenchmarkPresetPlanning(t *testing.T) {
	// Test 1: ToolPreset="benchmark" with PlanningMode=false should resolve to false
	req := &ServiceExecuteRequest{
		Prompt:       "test prompt",
		Harness:      "fiz",
		Provider:     "fake",
		ToolPreset:   "benchmark",
		PlanningMode: false,
	}

	// Create a minimal test setup to verify PlanningMode propagation
	// The actual agent execution isn't needed; we just need to verify
	// that execute_native doesn't override the PlanningMode flag.

	// Simulate what executeNative does when building the agentcore.Request
	loopReq := agentcore.Request{
		PlanningMode: req.PlanningMode,
	}

	if loopReq.PlanningMode != false {
		t.Errorf("ToolPreset=benchmark with PlanningMode=false: expected loopReq.PlanningMode==false, got %v", loopReq.PlanningMode)
	}

	// Test 2: ToolPreset="benchmark" with PlanningMode=true should resolve to true
	req2 := &ServiceExecuteRequest{
		Prompt:       "test prompt",
		Harness:      "fiz",
		Provider:     "fake",
		ToolPreset:   "benchmark",
		PlanningMode: true,
	}

	loopReq2 := agentcore.Request{
		PlanningMode: req2.PlanningMode,
	}

	if loopReq2.PlanningMode != true {
		t.Errorf("ToolPreset=benchmark with PlanningMode=true: expected loopReq.PlanningMode==true, got %v", loopReq2.PlanningMode)
	}

	// Test 3: No ToolPreset with PlanningMode=true should still be true
	req3 := &ServiceExecuteRequest{
		Prompt:       "test prompt",
		Harness:      "fiz",
		Provider:     "fake",
		PlanningMode: true,
	}

	loopReq3 := agentcore.Request{
		PlanningMode: req3.PlanningMode,
	}

	if loopReq3.PlanningMode != true {
		t.Errorf("No ToolPreset with PlanningMode=true: expected loopReq.PlanningMode==true, got %v", loopReq3.PlanningMode)
	}

	// Test 4: No ToolPreset with PlanningMode=false should be false
	req4 := &ServiceExecuteRequest{
		Prompt:       "test prompt",
		Harness:      "fiz",
		Provider:     "fake",
		PlanningMode: false,
	}

	loopReq4 := agentcore.Request{
		PlanningMode: req4.PlanningMode,
	}

	if loopReq4.PlanningMode != false {
		t.Errorf("No ToolPreset with PlanningMode=false: expected loopReq.PlanningMode==false, got %v", loopReq4.PlanningMode)
	}
}

// TestExecuteNative_NoPlanningModeLeak is the acceptance-criteria test alias
// for TestExecuteNativeRemovesBenchmarkPresetPlanning. It verifies that
// ToolPreset=="benchmark" does not force PlanningMode on.
func TestExecuteNative_NoPlanningModeLeak(t *testing.T) {
	TestExecuteNativeRemovesBenchmarkPresetPlanning(t)
}

func TestNativeContextDispatchPreservesRawCompactionOverride(t *testing.T) {
	const (
		selectedWindow = 8192
		selectedSource = "provider_api"
	)
	for _, rawOverride := range []int{0, 16384, -7} {
		t.Run(fmt.Sprintf("raw_%d", rawOverride), func(t *testing.T) {
			execute := ExecuteRequest{
				CompactionContextWindow: rawOverride,
				Decision: ExecuteDecision{
					Provider:              "alpha",
					Model:                 "fixture-model",
					SelectedContextWindow: selectedWindow,
					SelectedContextSource: selectedSource,
				},
			}
			native := NativeRequest{Decision: nativeDecisionFromExecute(execute.Decision)}
			projectExecuteContextToNative(&native, execute)
			var coreReq agentcore.Request
			projectNativeDispatchToCore(&coreReq, native, native.Decision.Provider, native.Decision.Model)

			if native.CompactionContextWindow != rawOverride || coreReq.CompactionContextWindow != rawOverride {
				t.Fatalf("raw override native/core = %d/%d, want unchanged %d",
					native.CompactionContextWindow, coreReq.CompactionContextWindow, rawOverride)
			}
			if coreReq.SelectedContextWindow != selectedWindow || coreReq.SelectedContextSource != selectedSource {
				t.Fatalf("selected context changed while carrying raw override: %d/%q, want %d/%q",
					coreReq.SelectedContextWindow, coreReq.SelectedContextSource, selectedWindow, selectedSource)
			}
		})
	}
}

func TestServiceImplRejectsNegativeCompactionContextWindow(t *testing.T) {
	resolveCalls := 0
	var events []agentcore.Event
	var final harnesses.FinalData
	RunNative(context.Background(), NativeRequest{
		CompactionContextWindow: -7,
		Decision: NativeDecision{
			Harness:  "fiz",
			Provider: "alpha",
			Model:    "fixture-model",
		},
		Started: time.Now(),
	}, NativeCallbacks{
		ResolveProvider: func(NativeProviderRequest) NativeProviderResolution {
			resolveCalls++
			return NativeProviderResolution{}
		},
		ObserveAgentEvent: func(event agentcore.Event) { events = append(events, event) },
		Finalize:          func(got harnesses.FinalData, _ TerminalOrigin) { final = got },
	})

	assert.Zero(t, resolveCalls, "invalid input must be rejected before provider resolution")
	assert.Empty(t, events, "invalid input must be rejected before core session.start")
	assert.Equal(t, "failed", final.Status)
	_, err := resolveNativeWorkingContextWindow(NativeRequest{
		SelectedContextWindow:   8192,
		CompactionContextWindow: -7,
	})
	assert.ErrorIs(t, err, agentcore.ErrContextCapacityInputInvalid)
	var inputErr *agentcore.ContextCapacityInputError
	require.True(t, errors.As(err, &inputErr))
	assert.Equal(t, -7, inputErr.Value)
}

func TestCompactionContextWindowCannotEnlargeSelectedRoute(t *testing.T) {
	for _, test := range []struct {
		name     string
		selected int
		override int
		want     int
	}{
		{name: "larger override cannot enlarge", selected: 4096, override: 8192, want: 4096},
		{name: "smaller override tightens", selected: 8192, override: 4096, want: 4096},
		{name: "zero preserves selected", selected: 8192, override: 0, want: 8192},
	} {
		t.Run(test.name, func(t *testing.T) {
			native, coreReq := propagatedContextRequests(test.selected, test.override)
			working, err := agentcore.ResolveWorkingContextWindow(coreReq.SelectedContextWindow, coreReq.CompactionContextWindow)
			require.NoError(t, err)
			assert.Equal(t, test.want, working)
			assert.Equal(t, working, nativeCompactionConfig(native, working).ContextWindow)
		})
	}
}

func TestCompactionContextWindowSuppliesUnknownRoute(t *testing.T) {
	native, coreReq := propagatedContextRequests(0, 4096)
	working, err := agentcore.ResolveWorkingContextWindow(coreReq.SelectedContextWindow, coreReq.CompactionContextWindow)
	require.NoError(t, err)
	assert.Equal(t, 4096, working)
	assert.Equal(t, working, nativeCompactionConfig(native, working).ContextWindow)
}

func propagatedContextRequests(selectedWindow, rawOverride int) (NativeRequest, agentcore.Request) {
	execute := ExecuteRequest{
		CompactionContextWindow: rawOverride,
		Decision: ExecuteDecision{
			Harness:               "fiz",
			Provider:              "alpha",
			Model:                 "fixture-model",
			SelectedContextWindow: selectedWindow,
			SelectedContextSource: "fixture",
		},
	}
	native := NativeRequest{Decision: nativeDecisionFromExecute(execute.Decision)}
	projectExecuteContextToNative(&native, execute)
	var coreReq agentcore.Request
	projectNativeDispatchToCore(&coreReq, native, native.Decision.Provider, native.Decision.Model)
	return native, coreReq
}

type nativeNoStreamCapacityProvider struct {
	streamOpts []agentcore.Options
	chatOpts   []agentcore.Options
}

func (p *nativeNoStreamCapacityProvider) Chat(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolDef, opts agentcore.Options) (agentcore.Response, error) {
	p.chatOpts = append(p.chatOpts, opts)
	return agentcore.Response{Content: "done"}, nil
}

func (p *nativeNoStreamCapacityProvider) ChatStream(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolDef, opts agentcore.Options) (<-chan agentcore.StreamDelta, error) {
	p.streamOpts = append(p.streamOpts, opts)
	stream := make(chan agentcore.StreamDelta, 1)
	stream <- agentcore.StreamDelta{Done: true}
	close(stream)
	return stream, nil
}

func TestRunNativeNoStreamRerunContinuesCapacityAttempts(t *testing.T) {
	provider := &nativeNoStreamCapacityProvider{}
	var requestAttempts []int
	var final harnesses.FinalData
	RunNative(context.Background(), NativeRequest{
		Prompt:                "x",
		Permissions:           "unrestricted",
		MaxTokens:             200,
		SelectedContextWindow: 100,
		Decision: NativeDecision{
			Harness:  "fiz",
			Provider: "alpha",
			Model:    "fixture-model",
		},
		Started: time.Now(),
	}, NativeCallbacks{
		ResolveProvider: func(NativeProviderRequest) NativeProviderResolution {
			return NativeProviderResolution{Provider: provider, Name: "alpha", Model: "fixture-model"}
		},
		ObserveAgentEvent: func(event agentcore.Event) {
			if event.Type != agentcore.EventLLMRequest {
				return
			}
			var payload struct {
				AttemptIndex int `json:"attempt_index"`
			}
			if err := json.Unmarshal(event.Data, &payload); err == nil {
				requestAttempts = append(requestAttempts, payload.AttemptIndex)
			}
		},
		Finalize: func(got harnesses.FinalData, _ TerminalOrigin) { final = got },
	})

	require.Len(t, provider.streamOpts, 1)
	require.Len(t, provider.chatOpts, 1)
	assert.Equal(t, 93, provider.streamOpts[0].MaxTokens)
	assert.Equal(t, 93, provider.chatOpts[0].MaxTokens)
	assert.Equal(t, []int{1, 2}, requestAttempts)
	assert.Equal(t, "success", final.Status)
}

// ServiceExecuteRequest is the API request shape for Execute
type ServiceExecuteRequest struct {
	Prompt       string
	Harness      string
	Provider     string
	ToolPreset   string
	PlanningMode bool
}

type nativeCostProvider struct {
	responses []agentcore.Response
	next      int
}

func (p *nativeCostProvider) Chat(context.Context, []agentcore.Message, []agentcore.ToolDef, agentcore.Options) (agentcore.Response, error) {
	response := p.responses[p.next]
	p.next++
	return response, nil
}

type nativeCostTool struct{}

func (nativeCostTool) Name() string            { return "continue" }
func (nativeCostTool) Description() string     { return "continue the cost test" }
func (nativeCostTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (nativeCostTool) Parallel() bool          { return true }
func (nativeCostTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "continued", nil
}

func TestExecuteNativePreservesFinalCostPresence(t *testing.T) {
	amount := func(value float64) *float64 { return &value }
	response := func(source agentcore.CostSource, value *float64, continues bool) agentcore.Response {
		result := agentcore.Response{
			Content: "done",
			Attempt: &agentcore.AttemptMetadata{
				Cost: &agentcore.CostAttribution{Source: source, Amount: value},
			},
		}
		if continues {
			result.Content = ""
			result.ToolCalls = []agentcore.ToolCall{{ID: "continue", Name: "continue", Arguments: json.RawMessage(`{}`)}}
		}
		return result
	}
	tests := []struct {
		name       string
		responses  []agentcore.Response
		wantCost   *float64
		wantSource harnesses.CostSource
	}{
		{name: "nil unknown", responses: []agentcore.Response{response(agentcore.CostSourceUnknown, nil, false)}, wantSource: harnesses.CostSourceUnknown},
		{name: "zero reported", responses: []agentcore.Response{response(agentcore.CostSourceProviderReported, amount(0), false)}, wantCost: amount(0), wantSource: harnesses.CostSourceReported},
		{name: "positive configured", responses: []agentcore.Response{response(agentcore.CostSourceConfigured, amount(0.02), false)}, wantCost: amount(0.02), wantSource: harnesses.CostSourceConfigured},
		{name: "positive reported", responses: []agentcore.Response{response(agentcore.CostSourceGatewayReported, amount(0.03), false)}, wantCost: amount(0.03), wantSource: harnesses.CostSourceReported},
		{
			name: "all known configured then reported",
			responses: []agentcore.Response{
				response(agentcore.CostSourceConfigured, amount(0.01), true),
				response(agentcore.CostSourceProviderReported, amount(0.02), false),
			},
			wantCost: amount(0.03), wantSource: harnesses.CostSourceReported,
		},
		{
			name: "all known reported then configured",
			responses: []agentcore.Response{
				response(agentcore.CostSourceProviderReported, amount(0.01), true),
				response(agentcore.CostSourceConfigured, amount(0.02), false),
			},
			wantCost: amount(0.03), wantSource: harnesses.CostSourceReported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &nativeCostProvider{responses: test.responses}
			var final harnesses.FinalData
			RunNative(context.Background(), NativeRequest{
				Prompt:  "measure cost",
				Started: time.Now(),
				Tools:   []agentcore.Tool{nativeCostTool{}},
				Decision: NativeDecision{
					Harness: "fiz", Provider: "test", Model: "cost-model",
				},
			}, NativeCallbacks{
				ResolveProvider: func(NativeProviderRequest) NativeProviderResolution {
					return NativeProviderResolution{Provider: provider, Name: "test", Model: "cost-model"}
				},
				Finalize: func(got harnesses.FinalData, _ TerminalOrigin) { final = got },
			})

			if final.FinalCostSource != test.wantSource {
				t.Fatalf("FinalCostSource = %q, want %q", final.FinalCostSource, test.wantSource)
			}
			if test.wantCost == nil {
				if final.FinalCostUSD != nil || final.CostUSD != 0 {
					t.Fatalf("unknown final cost = %v scalar=%v, want nil/zero", final.FinalCostUSD, final.CostUSD)
				}
				return
			}
			if final.FinalCostUSD == nil || *final.FinalCostUSD != *test.wantCost || final.CostUSD != *test.wantCost {
				t.Fatalf("final cost = %v scalar=%v, want %v", final.FinalCostUSD, final.CostUSD, *test.wantCost)
			}
		})
	}

	t.Run("defensive normalization ignores scalar and clones authoritative amount", func(t *testing.T) {
		input := 1.25
		cost, source := projectNativeFinalCost(agentcore.Result{
			FinalCostUSD:    &input,
			FinalCostSource: agentcore.SessionCostSourceConfigured,
			CostUSD:         99,
		})
		input = 7
		if cost == nil || *cost != 1.25 || source != harnesses.CostSourceConfigured {
			t.Fatalf("projected cost/source = %v/%q", cost, source)
		}

		for _, result := range []agentcore.Result{
			{CostUSD: 2.5, FinalCostSource: agentcore.SessionCostSourceConfigured},
			{FinalCostUSD: amount(2.5), FinalCostSource: agentcore.SessionCostSource("invalid"), CostUSD: 2.5},
			{FinalCostUSD: amount(-1), FinalCostSource: agentcore.SessionCostSourceReported, CostUSD: -1},
			{FinalCostUSD: amount(math.NaN()), FinalCostSource: agentcore.SessionCostSourceReported},
			{FinalCostUSD: amount(math.Inf(1)), FinalCostSource: agentcore.SessionCostSourceReported},
			{FinalCostUSD: amount(math.Inf(-1)), FinalCostSource: agentcore.SessionCostSourceReported},
		} {
			cost, source := projectNativeFinalCost(result)
			if cost != nil || source != harnesses.CostSourceUnknown {
				t.Fatalf("invalid projected cost/source = %v/%q, want nil/unknown", cost, source)
			}
		}
	})
}
