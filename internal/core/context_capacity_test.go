package core

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capacityRecordingProvider struct {
	responses []Response
	errs      []error
	calls     int
	opts      []Options
	messages  [][]Message
	tools     [][]ToolDef
}

func (p *capacityRecordingProvider) Chat(_ context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	index := p.calls
	p.calls++
	p.opts = append(p.opts, opts)
	p.messages = append(p.messages, append([]Message(nil), messages...))
	p.tools = append(p.tools, append([]ToolDef(nil), tools...))
	var response Response
	if index < len(p.responses) {
		response = p.responses[index]
	} else {
		response = Response{Content: "done"}
	}
	if index < len(p.errs) && p.errs[index] != nil {
		return response, p.errs[index]
	}
	return response, nil
}

type capacityStreamingProvider struct {
	chatCalls  int
	streamOpts []Options
}

func (p *capacityStreamingProvider) Chat(context.Context, []Message, []ToolDef, Options) (Response, error) {
	p.chatCalls++
	return Response{Content: "unexpected fallback"}, nil
}

func (p *capacityStreamingProvider) ChatStream(_ context.Context, _ []Message, _ []ToolDef, opts Options) (<-chan StreamDelta, error) {
	p.streamOpts = append(p.streamOpts, opts)
	stream := make(chan StreamDelta, 1)
	stream <- StreamDelta{Content: "done", Done: true}
	close(stream)
	return stream, nil
}

type capacityOutputTool struct {
	name   string
	output string
}

func (t capacityOutputTool) Name() string          { return t.name }
func (capacityOutputTool) Description() string     { return "expand a fixture" }
func (capacityOutputTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t capacityOutputTool) Execute(context.Context, json.RawMessage) (string, error) {
	return t.output, nil
}
func (capacityOutputTool) Parallel() bool { return false }

func TestEstimateProviderCallTokensCoversMessagesAndTools(t *testing.T) {
	largeExcludedID := strings.Repeat("excluded", 100)
	messages := []Message{
		{
			Role:    RoleAssistant,
			Content: "héllo",
			ToolCalls: []ToolCall{{
				ID:        largeExcludedID,
				Name:      "read",
				Arguments: json.RawMessage(`{"path":"α.go"}`),
			}},
		},
		{Role: RoleTool, Content: "ok", ToolCallID: "call-1"},
	}
	tools := []ToolDef{{Name: "read", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}
	want := 0
	for _, value := range []string{
		string(RoleAssistant), "héllo", "read", `{"path":"α.go"}`,
		string(RoleTool), "ok", "call-1",
		"read", "read a file", `{"type":"object"}`,
	} {
		want = saturatingAdd(want, EstimateTextTokens(value))
	}
	assert.Equal(t, want, EstimateProviderCallTokens(messages, tools))
	assert.Equal(t, 2, EstimateTextTokens("ééé"), "estimation uses UTF-8 bytes, not runes")
	assert.Equal(t, math.MaxInt, saturatingAdd(math.MaxInt-1, 2))

	messages[0].ToolCalls[0].ID = ""
	assert.Equal(t, want, EstimateProviderCallTokens(messages, tools), "assistant tool-call IDs are not in the canonical envelope")
}

func TestRunClampsPositiveMaxTokensToInitialHeadroom(t *testing.T) {
	provider := &capacityRecordingProvider{responses: []Response{{Content: "done"}}}
	var events []Event
	result, err := Run(context.Background(), Request{
		Prompt:                  "x",
		Provider:                provider,
		SelectedContextWindow:   200,
		CompactionContextWindow: 100,
		MaxTokens:               200,
		Callback:                func(event Event) { events = append(events, event) },
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	require.Len(t, provider.opts, 1)
	assert.Equal(t, 93, provider.opts[0].MaxTokens)

	capacityIndex := eventIndex(events, EventContextCapacity)
	requestIndex := eventIndex(events, EventLLMRequest)
	require.GreaterOrEqual(t, capacityIndex, 0)
	assert.Equal(t, capacityIndex+1, requestIndex)
	var capacityData ContextCapacityEventData
	require.NoError(t, json.Unmarshal(events[capacityIndex].Data, &capacityData))
	assert.Equal(t, ContextCapacityActionClamped, capacityData.Action)
	assert.Equal(t, 100, capacityData.ContextWindow)
	assert.Equal(t, 95, capacityData.EffectiveContextWindow)
	assert.Equal(t, 2, capacityData.EstimatedInputTokens)
	assert.Equal(t, 93, capacityData.AvailableOutputTokens)
	assert.Equal(t, 93, capacityData.EffectiveMaxTokens)
	var requestData map[string]any
	require.NoError(t, json.Unmarshal(events[requestIndex].Data, &requestData))
	assert.Equal(t, float64(93), requestData["max_tokens"])
}

func TestRunRecomputesMaxTokensAfterCompactionAndToolResults(t *testing.T) {
	provider := &capacityRecordingProvider{responses: []Response{
		{ToolCalls: []ToolCall{{ID: "call-1", Name: "expand", Arguments: json.RawMessage(`{}`)}}},
		{Content: "done"},
	}}
	var capturedMidTurn CompactionInput
	compactionCalls := 0
	compactor := func(_ context.Context, input CompactionInput, _ Provider) ([]Message, *CompactionResult, error) {
		compactionCalls++
		if compactionCalls == 2 {
			capturedMidTurn = input
			return []Message{{Role: RoleUser, Content: "x"}}, &CompactionResult{Summary: "short", TokensBefore: 100, TokensAfter: 2}, nil
		}
		return input.History, nil, nil
	}
	result, err := Run(context.Background(), Request{
		Prompt:                strings.Repeat("p", 80),
		SystemPrompt:          "sys",
		Provider:              provider,
		Tools:                 []Tool{capacityOutputTool{name: "expand", output: strings.Repeat("o", 200)}},
		Compactor:             compactor,
		SelectedContextWindow: 400,
		MaxTokens:             500,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	require.Len(t, provider.opts, 2)
	assert.Greater(t, provider.opts[1].MaxTokens, provider.opts[0].MaxTokens, "post-compaction call must recompute from shorter messages")
	require.NotEmpty(t, capturedMidTurn.ProviderMessages)
	assert.Equal(t, RoleSystem, capturedMidTurn.ProviderMessages[0].Role)
	for _, message := range capturedMidTurn.History {
		assert.NotEqual(t, RoleSystem, message.Role)
	}
	assert.Equal(t, EstimateProviderCallTokens(capturedMidTurn.ProviderMessages, capturedMidTurn.ToolDefinitions), capturedMidTurn.EstimatedProviderCallTokens)
	require.Len(t, capturedMidTurn.ToolDefinitions, 1)
	assert.Contains(t, capturedMidTurn.ProviderMessages[len(capturedMidTurn.ProviderMessages)-1].Content, strings.Repeat("o", 200))
}

func TestRunCapacityPreflightCoversRetryAndStreaming(t *testing.T) {
	t.Run("transient retry", func(t *testing.T) {
		provider := &capacityRecordingProvider{
			responses: []Response{{}, {Content: "done"}},
			errs:      []error{errors.New("503 Service Unavailable"), nil},
		}
		result, err := Run(context.Background(), Request{
			Prompt:                "x",
			Provider:              provider,
			SelectedContextWindow: 100,
			MaxTokens:             200,
		})
		require.NoError(t, err)
		require.Len(t, provider.opts, 2)
		assert.Equal(t, 93, provider.opts[0].MaxTokens)
		assert.Equal(t, 93, provider.opts[1].MaxTokens)
		assert.Equal(t, 2, result.CapacityAttempts[CapacityAttemptKey{CallKind: ContextCapacityCallMain, TurnIndex: 1}])
	})

	t.Run("streaming resumes attempt state", func(t *testing.T) {
		provider := &capacityStreamingProvider{}
		initial := CapacityAttemptState{{CallKind: ContextCapacityCallMain, TurnIndex: 1}: 4}
		result, err := Run(context.Background(), Request{
			Prompt:                  "x",
			Provider:                provider,
			SelectedContextWindow:   100,
			MaxTokens:               200,
			InitialCapacityAttempts: initial,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, provider.chatCalls)
		require.Len(t, provider.streamOpts, 1)
		assert.Equal(t, 93, provider.streamOpts[0].MaxTokens)
		assert.Equal(t, 5, result.CapacityAttempts[CapacityAttemptKey{CallKind: ContextCapacityCallMain, TurnIndex: 1}])
		assert.Equal(t, 4, initial[CapacityAttemptKey{CallKind: ContextCapacityCallMain, TurnIndex: 1}], "initial state must be defensively copied")
	})
}

func TestRunContextCapacityErrorSkipsProviderCalls(t *testing.T) {
	provider := &capacityRecordingProvider{}
	var events []Event
	result, err := Run(context.Background(), Request{
		Prompt:                "x",
		Provider:              provider,
		SelectedContextWindow: 2,
		MaxTokens:             10,
		Callback:              func(event Event) { events = append(events, event) },
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrContextCapacityExceeded)
	var capacityErr *ContextCapacityError
	require.ErrorAs(t, err, &capacityErr)
	assert.Equal(t, ContextCapacityErrorCode, capacityErr.Code())
	assert.Equal(t, ContextCapacityCallMain, capacityErr.CallKind)
	assert.Equal(t, 1, capacityErr.TurnIndex)
	assert.Equal(t, 1, capacityErr.AttemptIndex)
	assert.Equal(t, 2, capacityErr.ContextWindow)
	assert.Equal(t, 1, capacityErr.EffectiveWindow)
	assert.Equal(t, 2, capacityErr.EstimatedInputTokens)
	assert.Equal(t, 10, capacityErr.RequestedMaxTokens)
	assert.Zero(t, capacityErr.AvailableOutputTokens)
	assert.ErrorIs(t, result.Error, ErrContextCapacityExceeded)
	assert.Zero(t, provider.calls)
	require.Len(t, events, 3)
	assert.Equal(t, []EventType{EventSessionStart, EventContextCapacity, EventSessionEnd}, []EventType{events[0].Type, events[1].Type, events[2].Type})
}

func TestRunPlanningCapacitySkip(t *testing.T) {
	provider := &capacityRecordingProvider{responses: []Response{{Content: "main done"}}}
	var events []Event
	result, err := Run(context.Background(), Request{
		Prompt:                "x",
		Provider:              provider,
		PlanningMode:          true,
		SelectedContextWindow: 100,
		Callback:              func(event Event) { events = append(events, event) },
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	require.Equal(t, 1, provider.calls)
	require.Len(t, provider.messages, 1)
	assert.Equal(t, "x", provider.messages[0][0].Content)

	planningCapacity := 0
	planningRequests := 0
	planningResponses := 0
	planningTurns := 0
	for _, event := range events {
		switch event.Type {
		case EventContextCapacity:
			var payload ContextCapacityEventData
			require.NoError(t, json.Unmarshal(event.Data, &payload))
			if payload.CallKind == ContextCapacityCallPlanning {
				planningCapacity++
				assert.Equal(t, ContextCapacityActionPlanningSkipped, payload.Action)
				assert.Equal(t, 0, payload.TurnIndex)
				assert.Equal(t, 1, payload.AttemptIndex)
			}
		case EventLLMRequest:
			var payload map[string]any
			require.NoError(t, json.Unmarshal(event.Data, &payload))
			if planning, _ := payload["planning"].(bool); planning {
				planningRequests++
			}
		case EventLLMResponse:
			var payload map[string]any
			require.NoError(t, json.Unmarshal(event.Data, &payload))
			if planning, _ := payload["planning"].(bool); planning {
				planningResponses++
			}
		case EventPlanningTurn:
			planningTurns++
		}
	}
	assert.Equal(t, 1, planningCapacity)
	assert.Zero(t, planningRequests)
	assert.Zero(t, planningResponses)
	assert.Zero(t, planningTurns)
}

func TestRunZeroMaxTokensRemainsProviderDefault(t *testing.T) {
	provider := &capacityRecordingProvider{responses: []Response{{Content: "done"}}}
	var events []Event
	_, err := Run(context.Background(), Request{
		Prompt:                "x",
		Provider:              provider,
		SelectedContextWindow: 100,
		MaxTokens:             0,
		Callback:              func(event Event) { events = append(events, event) },
	})
	require.NoError(t, err)
	require.Len(t, provider.opts, 1)
	assert.Zero(t, provider.opts[0].MaxTokens)
	assert.Equal(t, -1, eventIndex(events, EventContextCapacity))

	exhausted := &capacityRecordingProvider{}
	_, err = Run(context.Background(), Request{
		Prompt:                "x",
		Provider:              exhausted,
		SelectedContextWindow: 2,
		MaxTokens:             0,
	})
	assert.ErrorIs(t, err, ErrContextCapacityExceeded)
	assert.Zero(t, exhausted.calls)
}

func TestRunRejectsNegativeMaxTokensBeforeSessionStart(t *testing.T) {
	provider := &capacityRecordingProvider{}
	var events []Event
	result, err := Run(context.Background(), Request{
		Prompt:    "x",
		Provider:  provider,
		MaxTokens: -1,
		Callback:  func(event Event) { events = append(events, event) },
	})
	require.Error(t, err)
	assert.Equal(t, StatusError, result.Status)
	assert.Contains(t, err.Error(), "max_tokens must be non-negative")
	assert.Zero(t, provider.calls)
	assert.Empty(t, events)
}

func eventIndex(events []Event, eventType EventType) int {
	for index, event := range events {
		if event.Type == eventType {
			return index
		}
	}
	return -1
}
