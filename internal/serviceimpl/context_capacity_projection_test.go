package serviceimpl

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextCapacityProjectionProvider struct {
	calls int
	opts  []agentcore.Options
}

func (p *contextCapacityProjectionProvider) Chat(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolDef, opts agentcore.Options) (agentcore.Response, error) {
	p.calls++
	p.opts = append(p.opts, opts)
	return agentcore.Response{Content: "done"}, nil
}

type contextCapacityProjectionCapture struct {
	coreEvents []agentcore.Event
	capacity   []harnesses.ContextCapacityData
	steps      []string
	final      harnesses.FinalData
	origin     TerminalOrigin
}

func runContextCapacityProjection(t *testing.T, req NativeRequest, provider *contextCapacityProjectionProvider) contextCapacityProjectionCapture {
	t.Helper()
	var capture contextCapacityProjectionCapture
	req.Permissions = "unrestricted"
	req.Started = time.Now()
	if req.Decision.Harness == "" {
		req.Decision.Harness = "fiz"
	}
	if req.Decision.Provider == "" {
		req.Decision.Provider = "alpha"
	}
	if req.Decision.Model == "" {
		req.Decision.Model = "fixture-model"
	}
	RunNative(context.Background(), req, NativeCallbacks{
		ResolveProvider: func(NativeProviderRequest) NativeProviderResolution {
			return NativeProviderResolution{Provider: provider, Name: "alpha", Model: "fixture-model"}
		},
		Compactor: func(string) agentcore.Compactor {
			return func(_ context.Context, input agentcore.CompactionInput, _ agentcore.Provider) ([]agentcore.Message, *agentcore.CompactionResult, error) {
				return input.History, nil, nil
			}
		},
		ObserveAgentEvent: func(event agentcore.Event) {
			capture.coreEvents = append(capture.coreEvents, event)
			capture.steps = append(capture.steps, "core:"+string(event.Type))
		},
		EmitEvent: func(eventType harnesses.EventType, payload any) {
			capture.steps = append(capture.steps, "service:"+string(eventType))
			if eventType == harnesses.EventTypeContextCapacity {
				mapped, ok := payload.(harnesses.ContextCapacityData)
				require.True(t, ok, "context-capacity payload type = %T", payload)
				capture.capacity = append(capture.capacity, mapped)
			}
		},
		Finalize: func(final harnesses.FinalData, origin TerminalOrigin) {
			capture.steps = append(capture.steps, "service:final")
			capture.origin = origin
			capture.final = ClassifyTerminalFinal(final, origin, nil)
		},
	})
	return capture
}

func TestServiceContextCapacityClampAndPlanningSkipEvents(t *testing.T) {
	t.Run("main clamp", func(t *testing.T) {
		provider := &contextCapacityProjectionProvider{}
		capture := runContextCapacityProjection(t, NativeRequest{
			Prompt:                "x",
			MaxTokens:             200,
			SelectedContextWindow: 100,
		}, provider)

		require.Len(t, capture.capacity, 1)
		assert.Equal(t, harnesses.ContextCapacityData{
			Action:                 agentcore.ContextCapacityActionClamped,
			CallKind:               agentcore.ContextCapacityCallMain,
			TurnIndex:              1,
			AttemptIndex:           1,
			ContextWindow:          100,
			EffectiveContextWindow: 95,
			EstimatedInputTokens:   2,
			RequestedMaxTokens:     200,
			EffectiveMaxTokens:     93,
			AvailableOutputTokens:  93,
		}, capture.capacity[0])
		require.Len(t, provider.opts, 1)
		assert.Equal(t, 93, provider.opts[0].MaxTokens)
		assert.Equal(t, "success", capture.final.Status)
		assert.Nil(t, capture.final.ContextCapacity)
		assertEventImmediatelyBefore(t, capture.steps, "service:context_capacity", "core:llm.request")
	})

	t.Run("planning skip", func(t *testing.T) {
		provider := &contextCapacityProjectionProvider{}
		capture := runContextCapacityProjection(t, NativeRequest{
			Prompt:                "x",
			PlanningMode:          true,
			SelectedContextWindow: 100,
		}, provider)

		require.Len(t, capture.capacity, 1)
		assert.Equal(t, harnesses.ContextCapacityData{
			Action:                 agentcore.ContextCapacityActionPlanningSkipped,
			CallKind:               agentcore.ContextCapacityCallPlanning,
			TurnIndex:              0,
			AttemptIndex:           1,
			ContextWindow:          100,
			EffectiveContextWindow: 95,
			EstimatedInputTokens:   110,
			RequestedMaxTokens:     0,
			EffectiveMaxTokens:     0,
			AvailableOutputTokens:  0,
		}, capture.capacity[0])
		assert.Equal(t, 1, provider.calls, "only the main call may reach the provider")
		assert.Equal(t, "success", capture.final.Status)
		assert.Nil(t, capture.final.ContextCapacity)
		for _, event := range capture.coreEvents {
			switch event.Type {
			case agentcore.EventPlanningTurn:
				t.Fatalf("planning.turn emitted after planning capacity skip")
			case agentcore.EventLLMRequest, agentcore.EventLLMResponse:
				var payload map[string]any
				require.NoError(t, json.Unmarshal(event.Data, &payload))
				if planning, _ := payload["planning"].(bool); planning {
					t.Fatalf("planning provider event emitted after planning capacity skip: %s", event.Type)
				}
			}
		}
	})
}

func TestServiceContextCapacityRejectedFinalProjection(t *testing.T) {
	provider := &contextCapacityProjectionProvider{}
	capture := runContextCapacityProjection(t, NativeRequest{
		Prompt:                "x",
		MaxTokens:             10,
		SelectedContextWindow: 2,
	}, provider)

	require.Len(t, capture.capacity, 1)
	want := harnesses.ContextCapacityData{
		Action:                 agentcore.ContextCapacityActionRejected,
		CallKind:               agentcore.ContextCapacityCallMain,
		TurnIndex:              1,
		AttemptIndex:           1,
		ContextWindow:          2,
		EffectiveContextWindow: 1,
		EstimatedInputTokens:   2,
		RequestedMaxTokens:     10,
		EffectiveMaxTokens:     0,
		AvailableOutputTokens:  0,
	}
	assert.Equal(t, want, capture.capacity[0])
	assert.Zero(t, provider.calls)
	assert.Equal(t, TerminalOriginContextCapacity, capture.origin)
	assert.Equal(t, "failed", capture.final.Status)
	assert.Equal(t, harnesses.SessionOutcomeFailed, capture.final.Outcome)
	assert.Equal(t, harnesses.TerminalCauseContextCapacityExceeded, capture.final.Cause)
	assert.Equal(t, harnesses.SessionStageToolLoop, capture.final.Stage)
	require.NotNil(t, capture.final.ContextCapacity)
	assert.Equal(t, want, *capture.final.ContextCapacity)
	require.NotNil(t, capture.final.RoutingActual)
	assert.Empty(t, capture.final.RoutingActual.FailureClass)
	assertEventImmediatelyBefore(t, capture.steps, "service:context_capacity", "core:session.end")
	assert.Equal(t, "service:final", capture.steps[len(capture.steps)-1])
	for _, event := range capture.coreEvents {
		if event.Type == agentcore.EventLLMRequest || event.Type == agentcore.EventLLMResponse {
			t.Fatalf("prevented provider call emitted %s", event.Type)
		}
	}

	misleading := ClassifyTerminalFinal(harnesses.FinalData{
		Status:          "failed",
		ContextCapacity: &want,
	}, TerminalOriginHarness, nil)
	assert.Equal(t, harnesses.TerminalCauseHarnessFailed, misleading.Cause,
		"an arbitrary subprocess payload must not choose the service-owned terminal cause")
	assert.Nil(t, misleading.ContextCapacity,
		"a non-capacity terminal cause must not retain a harness-supplied capacity payload")
}

func TestServiceContextCapacityDoesNotPoisonRouteHealth(t *testing.T) {
	provider := &contextCapacityProjectionProvider{}
	store := routehealth.NewStore()
	probes := routehealth.NewProbeStore()
	log := &coordinatorSessionLog{}
	observeCalls := 0
	projectionCalls := 0

	events := (ExecuteCoordinator{Registry: harnesses.NewRegistry()}).RunResolved(context.Background(), ExecuteRequest{
		SessionID:         "capacity-coordinator",
		Prompt:            "<summary>x</summary>",
		RequestedHarness:  "fiz",
		RequestedProvider: "alpha",
		RequestedModel:    "fixture-model",
		Permissions:       "unrestricted",
		MaxTokens:         10,
		Decision: ExecuteDecision{
			Harness:               "fiz",
			Provider:              "alpha",
			ServerInstance:        "server-alpha",
			Model:                 "fixture-model",
			SelectedContextWindow: 2,
			SelectedContextSource: "fixture",
		},
		RoutingDecisionData: json.RawMessage(`{"harness":"fiz","provider":"alpha","model":"fixture-model"}`),
	}, ExecutePorts{
		OpenSessionLog: func() ExecuteSessionLog { return log },
		ResolveNativeProvider: func(NativeProviderRequest) NativeProviderResolution {
			return NativeProviderResolution{Provider: provider, Name: "alpha", Model: "fixture-model"}
		},
		ProjectContextCapacity: func(payload harnesses.ContextCapacityData) any {
			projectionCalls++
			return projectedContextCapacityDataFromHarness(payload)
		},
		ObserveRouteAttempt: func(harnesses.FinalData) {
			observeCalls++
			_ = store.RecordAttempt(routehealth.Attempt{
				Harness: "fiz", Provider: "alpha", Model: "fixture-model",
				Status: "failed", Reason: "transport", Error: "synthetic poison",
			})
			probes.RecordProbe("alpha", "west", false, time.Now())
		},
	})

	var (
		streamCapacity *projectedContextCapacityData
		streamFinal    *harnesses.FinalData
		capacityIndex  = -1
		finalIndex     = -1
		index          int
	)
	for event := range events {
		switch event.Type {
		case harnesses.EventTypeContextCapacity:
			capacityIndex = index
			var payload projectedContextCapacityData
			require.NoError(t, json.Unmarshal(event.Data, &payload))
			streamCapacity = &payload
		case harnesses.EventTypeFinal:
			finalIndex = index
			var payload harnesses.FinalData
			require.NoError(t, json.Unmarshal(event.Data, &payload))
			streamFinal = &payload
		}
		index++
	}

	assert.Equal(t, 1, projectionCalls)
	assert.Zero(t, observeCalls, "a capacity-origin terminal must bypass route observation")
	assert.Zero(t, provider.calls)
	require.NotNil(t, streamCapacity)
	require.NotNil(t, streamFinal)
	assert.Less(t, capacityIndex, finalIndex)
	assert.Equal(t, harnesses.SessionOutcomeFailed, streamFinal.Outcome)
	assert.Equal(t, harnesses.TerminalCauseContextCapacityExceeded, streamFinal.Cause)
	assert.Equal(t, harnesses.SessionStageToolLoop, streamFinal.Stage)
	require.NotNil(t, streamFinal.ContextCapacity)
	assert.Equal(t, *streamCapacity, projectedContextCapacityDataFromHarness(*streamFinal.ContextCapacity))
	assert.Empty(t, streamFinal.RoutingActual.FailureClass)

	var coreTypes []agentcore.EventType
	for _, event := range log.core {
		switch event.Type {
		case agentcore.EventSessionStart, agentcore.EventContextCapacity,
			agentcore.EventLLMRequest, agentcore.EventLLMResponse, agentcore.EventSessionEnd:
			coreTypes = append(coreTypes, event.Type)
		}
	}
	assert.Equal(t, []agentcore.EventType{
		agentcore.EventSessionStart,
		agentcore.EventContextCapacity,
		agentcore.EventSessionEnd,
	}, coreTypes)
	require.Len(t, log.ends, 1)
	assert.Equal(t, harnesses.TerminalCauseContextCapacityExceeded, log.ends[0].Cause)
	assert.Empty(t, store.ActiveAttempts(time.Now(), time.Minute))
	success, latency := store.MetricSignals(time.Now(), time.Minute)
	assert.Empty(t, success)
	assert.Empty(t, latency)
	_, probed := probes.LastProbe("alpha", "west")
	assert.False(t, probed)
	assert.Empty(t, probes.UnreachableProviders(time.Now(), time.Minute))
	inputs := routing.Inputs{}
	routehealth.ApplyAttemptCooldowns(&inputs, store.ActiveAttempts(time.Now(), time.Minute), time.Minute)
	assert.Empty(t, inputs.ExactRouteCooldowns)
	assert.Empty(t, inputs.ProviderCooldowns)
}

type projectedContextCapacityData struct {
	Action                 string `json:"action"`
	CallKind               string `json:"call_kind"`
	TurnIndex              int    `json:"turn_index"`
	AttemptIndex           int    `json:"attempt_index"`
	ContextWindow          int    `json:"context_window"`
	EffectiveContextWindow int    `json:"effective_context_window"`
	EstimatedInputTokens   int    `json:"estimated_input_tokens"`
	RequestedMaxTokens     int    `json:"requested_max_tokens"`
	EffectiveMaxTokens     int    `json:"effective_max_tokens"`
	AvailableOutputTokens  int    `json:"available_output_tokens"`
}

func projectedContextCapacityDataFromHarness(payload harnesses.ContextCapacityData) projectedContextCapacityData {
	return projectedContextCapacityData{
		Action: payload.Action, CallKind: payload.CallKind,
		TurnIndex: payload.TurnIndex, AttemptIndex: payload.AttemptIndex,
		ContextWindow: payload.ContextWindow, EffectiveContextWindow: payload.EffectiveContextWindow,
		EstimatedInputTokens: payload.EstimatedInputTokens, RequestedMaxTokens: payload.RequestedMaxTokens,
		EffectiveMaxTokens: payload.EffectiveMaxTokens, AvailableOutputTokens: payload.AvailableOutputTokens,
	}
}

func TestContextCapacityProjectionMapsEveryField(t *testing.T) {
	corePayload := agentcore.ContextCapacityEventData{
		Action: "a", CallKind: "b", TurnIndex: 3, AttemptIndex: 4,
		ContextWindow: 5, EffectiveContextWindow: 6, EstimatedInputTokens: 7,
		RequestedMaxTokens: 8, EffectiveMaxTokens: 9, AvailableOutputTokens: 10,
	}
	mapped := contextCapacityDataFromCore(corePayload)
	coreType := reflect.TypeOf(corePayload)
	neutralType := reflect.TypeOf(mapped)
	if coreType.NumField() != 10 || neutralType.NumField() != 10 {
		t.Fatalf("core/neutral context-capacity field counts = %d/%d, want 10/10", coreType.NumField(), neutralType.NumField())
	}
	for index := 0; index < coreType.NumField(); index++ {
		if coreType.Field(index).Name != neutralType.Field(index).Name ||
			coreType.Field(index).Tag.Get("json") != neutralType.Field(index).Tag.Get("json") {
			t.Fatalf("core/neutral field %d drift: %#v vs %#v", index, coreType.Field(index), neutralType.Field(index))
		}
	}
	assert.Equal(t, harnesses.ContextCapacityData{
		Action: "a", CallKind: "b", TurnIndex: 3, AttemptIndex: 4,
		ContextWindow: 5, EffectiveContextWindow: 6, EstimatedInputTokens: 7,
		RequestedMaxTokens: 8, EffectiveMaxTokens: 9, AvailableOutputTokens: 10,
	}, mapped)
}

func assertEventImmediatelyBefore(t *testing.T, steps []string, before, after string) {
	t.Helper()
	for index := 0; index+1 < len(steps); index++ {
		if steps[index] == before && steps[index+1] == after {
			return
		}
	}
	t.Fatalf("event order %q immediately before %q not found in %v", before, after, steps)
}
