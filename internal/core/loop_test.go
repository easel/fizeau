package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// mockProvider is a test provider that returns pre-configured responses.
type mockProvider struct {
	responses []Response
	callCount int
}

func (m *mockProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if m.callCount >= len(m.responses) {
		return Response{Content: "no more responses"}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

type routingReportProvider struct {
	mockProvider
	report RoutingReport
}

func (p *routingReportProvider) RoutingReport() RoutingReport {
	return p.report
}

// mockTool is a test tool that returns a fixed result.
type mockTool struct {
	name   string
	result string
	err    error
}

func (t *mockTool) Name() string            { return t.name }
func (t *mockTool) Description() string     { return "mock tool" }
func (t *mockTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *mockTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	return t.result, t.err
}
func (t *mockTool) Parallel() bool { return false }

type providerOutcome struct {
	response Response
	err      error
}

type recordingProvider struct {
	responses []Response
	callCount int
	calls     [][]Message
	toolCalls [][]ToolDef
	opts      []Options
}

func (r *recordingProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	copied := append([]Message(nil), messages...)
	r.calls = append(r.calls, copied)
	if tools != nil {
		toolCopy := append([]ToolDef(nil), tools...)
		r.toolCalls = append(r.toolCalls, toolCopy)
	}
	r.opts = append(r.opts, opts)
	if r.callCount >= len(r.responses) {
		return Response{Content: "no more responses"}, nil
	}
	resp := r.responses[r.callCount]
	r.callCount++
	return resp, nil
}

func TestRunForwardsRequestSamplingAndReasoningToProviderOptions(t *testing.T) {
	p := &recordingProvider{responses: []Response{{Content: "done"}}}
	temperature := 0.25
	_, err := Run(context.Background(), Request{
		Prompt:      "hello",
		Provider:    p,
		Temperature: &temperature,
		Seed:        12345,
		Reasoning:   ReasoningLow,
		MaxTokens:   17,
	})
	require.NoError(t, err)
	require.Len(t, p.opts, 1)
	require.NotNil(t, p.opts[0].Temperature)
	assert.Equal(t, temperature, *p.opts[0].Temperature)
	assert.Equal(t, int64(12345), p.opts[0].Seed)
	assert.Equal(t, ReasoningLow, p.opts[0].Reasoning)
	assert.Equal(t, 17, p.opts[0].MaxTokens)
}

// retryProvider is a test provider that returns a sequence of outcomes.
type retryProvider struct {
	outcomes  []providerOutcome
	callCount int
}

func (r *retryProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if r.callCount >= len(r.outcomes) {
		return Response{}, errors.New("no more outcomes")
	}
	outcome := r.outcomes[r.callCount]
	r.callCount++
	if outcome.err != nil {
		return Response{}, outcome.err
	}
	return outcome.response, nil
}

type barrierProvider struct {
	id        string
	responses []Response
	callCount int
	ready     chan<- string
	release   <-chan struct{}
}

func (p *barrierProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}

	if p.callCount == 0 && p.ready != nil && p.release != nil {
		select {
		case p.ready <- p.id:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
		select {
		case <-p.release:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}

	if p.callCount >= len(p.responses) {
		return Response{Content: "no more responses"}, nil
	}
	resp := p.responses[p.callCount]
	p.callCount++
	return resp, nil
}

type identityProvider struct {
	mockProvider
	provider string
	model    string
}

func (p *identityProvider) SessionStartMetadata() (string, string) {
	return p.provider, p.model
}

type cancelingIdentityProvider struct {
	provider string
	model    string
	cancel   context.CancelFunc
}

func (p *cancelingIdentityProvider) SessionStartMetadata() (string, string) {
	return p.provider, p.model
}

func (p *cancelingIdentityProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if p.cancel != nil {
		p.cancel()
	}
	return Response{}, errors.New("forced provider failure")
}

type streamingIdentityProvider struct {
	*mockStreamingProvider
	provider string
	model    string
}

func (p *streamingIdentityProvider) SessionStartMetadata() (string, string) {
	return p.provider, p.model
}

func findResponseAttempt(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	attempt, ok := payload["attempt"].(map[string]any)
	require.True(t, ok, "response event should include attempt metadata")
	return attempt
}

func findResponsePayload(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	return payload
}

func TestRun_SimpleTextResponse(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{Content: "Hello, world!", Usage: TokenUsage{Input: 10, Output: 5, Total: 15}},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "Say hello",
		Provider: provider,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "Hello, world!", result.Output)
	assert.Equal(t, 10, result.Tokens.Input)
	assert.Equal(t, 5, result.Tokens.Output)
	assert.Empty(t, result.ToolCalls)
}

func TestRun_ToolCallThenResponse(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
				},
				Usage: TokenUsage{Input: 20, Output: 10, Total: 30},
			},
			{
				Content: "The package is main.",
				Usage:   TokenUsage{Input: 50, Output: 15, Total: 65},
			},
		},
	}

	readTool := &mockTool{name: "read", result: "package main\n"}

	result, err := Run(context.Background(), Request{
		Prompt:   "Read main.go and tell me the package",
		Provider: provider,
		Tools:    []Tool{readTool},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "The package is main.", result.Output)
	assert.Equal(t, 70, result.Tokens.Input)
	assert.Equal(t, 25, result.Tokens.Output)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "read", result.ToolCalls[0].Tool)
	assert.Equal(t, "package main\n", result.ToolCalls[0].Output)
}

func TestRun_IterationLimit(t *testing.T) {
	// Provider always returns tool calls — loop should stop at limit
	provider := &mockProvider{
		responses: []Response{
			{ToolCalls: []ToolCall{{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{}`)}}, Usage: TokenUsage{Total: 10}},
			{ToolCalls: []ToolCall{{ID: "tc2", Name: "read", Arguments: json.RawMessage(`{}`)}}, Usage: TokenUsage{Total: 10}},
			{ToolCalls: []ToolCall{{ID: "tc3", Name: "read", Arguments: json.RawMessage(`{}`)}}, Usage: TokenUsage{Total: 10}},
		},
	}

	readTool := &mockTool{name: "read", result: "content"}

	result, err := Run(context.Background(), Request{
		Prompt:        "loop forever",
		Provider:      provider,
		Tools:         []Tool{readTool},
		MaxIterations: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusIterationLimit, result.Status)
}

func TestRun_RetriesProviderFailures(t *testing.T) {
	provider := &retryProvider{
		outcomes: []providerOutcome{
			{err: errors.New("503 service unavailable (transient)")},
			{err: errors.New("connection reset by peer")},
			{
				response: Response{
					Content: "done",
					Usage:   TokenUsage{Input: 12, Output: 3, Total: 15},
					Model:   "gpt-4o",
				},
			},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "retry until success",
		Provider: provider,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "done", result.Output)
	assert.Equal(t, 3, provider.callCount)
}

func TestRun_RetryExhaustionStopsAtRetryCeiling(t *testing.T) {
	provider := &retryProvider{
		outcomes: []providerOutcome{
			{err: errors.New("503 service unavailable (1)")},
			{err: errors.New("503 service unavailable (2)")},
			{err: errors.New("503 service unavailable (3)")},
			{err: errors.New("503 service unavailable (4)")},
			{err: errors.New("503 service unavailable (5)")},
			{response: Response{Content: "must never execute"}},
		},
	}

	var attempts []int
	var llmErrors []string
	result, err := Run(context.Background(), Request{
		Prompt:   "retry until exhausted",
		Provider: provider,
		Callback: func(e Event) {
			if e.Type != EventLLMResponse {
				return
			}
			payload := findResponsePayload(t, e.Data)
			if attempt, ok := payload["attempt_index"].(float64); ok {
				attempts = append(attempts, int(attempt))
			}
			if errVal, ok := payload["error"].(string); ok {
				llmErrors = append(llmErrors, errVal)
			}
		},
	})
	require.Error(t, err)
	assert.Equal(t, StatusError, result.Status)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "provider error")
	assert.Contains(t, result.Error.Error(), "503 service unavailable (5)")
	assert.Equal(t, 5, provider.callCount, "runtime retry ceiling should prevent a sixth provider call")
	assert.Equal(t, []int{1, 2, 3, 4, 5}, attempts)
	require.Len(t, llmErrors, 5)
}

func TestRun_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	provider := &mockProvider{
		responses: []Response{
			{Content: "should not reach this"},
		},
	}

	result, err := Run(ctx, Request{
		Prompt:   "test",
		Provider: provider,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, result.Status)
}

func TestRun_UnknownToolCall(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "nonexistent", Arguments: json.RawMessage(`{}`)},
				},
				Usage: TokenUsage{Total: 10},
			},
			{Content: "I see, that tool doesn't exist."},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	require.Len(t, result.ToolCalls, 1)
	assert.Contains(t, result.ToolCalls[0].Error, "unknown tool")
}

func TestRun_NilProvider(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Prompt: "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider is required")
}

func TestRun_EmptyResponse(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{Content: "", Usage: TokenUsage{Total: 5}},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "", result.Output)
}

func TestRun_ConversationHistoryCarriesAcrossRuns(t *testing.T) {
	t.Run("default one-shot call", func(t *testing.T) {
		provider := &recordingProvider{
			responses: []Response{
				{Content: "done"},
			},
		}

		result, err := Run(context.Background(), Request{
			Prompt:   "hello",
			Provider: provider,
		})
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, result.Status)
		require.Len(t, provider.calls, 1)
		require.Equal(t, []Message{
			{Role: RoleUser, Content: "hello"},
		}, provider.calls[0])
		require.Equal(t, []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleAssistant, Content: "done"},
		}, result.Messages)
	})

	t.Run("history carries forward across runs", func(t *testing.T) {
		provider := &recordingProvider{
			responses: []Response{
				{Content: "first done"},
				{Content: "second done"},
			},
		}

		systemPrompt := "You are a helpful assistant."

		first, err := Run(context.Background(), Request{
			Prompt:       "first question",
			SystemPrompt: systemPrompt,
			Provider:     provider,
		})
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, first.Status)
		require.Len(t, provider.calls, 1)
		require.Equal(t, []Message{
			{Role: RoleSystem, Content: systemPrompt},
			{Role: RoleUser, Content: "first question"},
		}, provider.calls[0])
		require.Equal(t, []Message{
			{Role: RoleUser, Content: "first question"},
			{Role: RoleAssistant, Content: "first done"},
		}, first.Messages)

		second, err := Run(context.Background(), Request{
			History:      first.Messages,
			Prompt:       "second question",
			SystemPrompt: systemPrompt,
			Provider:     provider,
		})
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, second.Status)
		require.Len(t, provider.calls, 2)
		require.Equal(t, []Message{
			{Role: RoleSystem, Content: systemPrompt},
			{Role: RoleUser, Content: "first question"},
			{Role: RoleAssistant, Content: "first done"},
			{Role: RoleUser, Content: "second question"},
		}, provider.calls[1])
		require.Equal(t, []Message{
			{Role: RoleUser, Content: "first question"},
			{Role: RoleAssistant, Content: "first done"},
			{Role: RoleUser, Content: "second question"},
			{Role: RoleAssistant, Content: "second done"},
		}, second.Messages)
	})
}

func TestRun_ConcurrentRunsKeepIndependentState(t *testing.T) {
	ready := make(chan string, 2)
	release := make(chan struct{})

	providerA := &barrierProvider{
		id: "A",
		responses: []Response{
			{
				ToolCalls: []ToolCall{{ID: "a-tc1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}},
				Usage:     TokenUsage{Input: 11, Output: 7, Total: 18},
			},
			{
				Content: "run A done",
				Usage:   TokenUsage{Input: 13, Output: 5, Total: 18},
			},
		},
		ready:   ready,
		release: release,
	}
	providerB := &barrierProvider{
		id: "B",
		responses: []Response{
			{
				ToolCalls: []ToolCall{{ID: "b-tc1", Name: "read", Arguments: json.RawMessage(`{"path":"b.go"}`)}},
				Usage:     TokenUsage{Input: 19, Output: 3, Total: 22},
			},
			{
				Content: "run B done",
				Usage:   TokenUsage{Input: 23, Output: 2, Total: 25},
			},
		},
		ready:   ready,
		release: release,
	}

	readToolA := &mockTool{name: "read", result: "alpha"}
	readToolB := &mockTool{name: "read", result: "bravo"}

	var wg sync.WaitGroup
	var resultA, resultB Result
	var errA, errB error
	var eventsA, eventsB []Event
	var muA, muB sync.Mutex

	wg.Add(2)
	go func() {
		defer wg.Done()
		resultA, errA = Run(context.Background(), Request{
			Prompt:   "run-a",
			Provider: providerA,
			Tools:    []Tool{readToolA},
			Callback: func(e Event) {
				muA.Lock()
				defer muA.Unlock()
				eventsA = append(eventsA, e)
			},
		})
	}()
	go func() {
		defer wg.Done()
		resultB, errB = Run(context.Background(), Request{
			Prompt:   "run-b",
			Provider: providerB,
			Tools:    []Tool{readToolB},
			Callback: func(e Event) {
				muB.Lock()
				defer muB.Unlock()
				eventsB = append(eventsB, e)
			},
		})
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-ready:
			seen[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for both concurrent runs to enter provider chat")
		}
	}
	close(release)
	wg.Wait()

	require.NoError(t, errA)
	require.NoError(t, errB)
	assert.Equal(t, StatusSuccess, resultA.Status)
	assert.Equal(t, StatusSuccess, resultB.Status)
	assert.Equal(t, 2, providerA.callCount)
	assert.Equal(t, 2, providerB.callCount)
	assert.NotEmpty(t, resultA.SessionID)
	assert.NotEmpty(t, resultB.SessionID)
	assert.NotEqual(t, resultA.SessionID, resultB.SessionID)

	assert.Equal(t, TokenUsage{Input: 24, Output: 12, Total: 36}, resultA.Tokens)
	assert.Equal(t, TokenUsage{Input: 42, Output: 5, Total: 47}, resultB.Tokens)
	require.Len(t, resultA.ToolCalls, 1)
	require.Len(t, resultB.ToolCalls, 1)
	assert.Equal(t, "alpha", resultA.ToolCalls[0].Output)
	assert.Equal(t, "bravo", resultB.ToolCalls[0].Output)
	assert.Equal(t, "run A done", resultA.Output)
	assert.Equal(t, "run B done", resultB.Output)

	require.NotEmpty(t, eventsA)
	require.NotEmpty(t, eventsB)
	for i, e := range eventsA {
		assert.Equal(t, resultA.SessionID, e.SessionID, "run A event %d leaked session id", i)
		assert.Equal(t, i, e.Seq, "run A event seq should be contiguous")
	}
	for i, e := range eventsB {
		assert.Equal(t, resultB.SessionID, e.SessionID, "run B event %d leaked session id", i)
		assert.Equal(t, i, e.Seq, "run B event seq should be contiguous")
	}
}

func TestRun_EventCallback(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{Content: "done", Usage: TokenUsage{Total: 10}},
		},
	}

	var events []Event
	cb := func(e Event) {
		events = append(events, e)
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
		Callback: cb,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	// Should have: session.start, llm.request, llm.response, session.end
	require.Len(t, events, 4)
	assert.Equal(t, EventSessionStart, events[0].Type)
	assert.Equal(t, EventLLMRequest, events[1].Type)
	assert.Equal(t, EventLLMResponse, events[2].Type)
	assert.Equal(t, EventSessionEnd, events[3].Type)
}

func TestRun_TelemetryShutdownFailureIsBestEffort(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{Content: "done", Usage: TokenUsage{Input: 4, Output: 2, Total: 6}},
		},
	}

	logs, restore := captureLoopLogs(t)
	defer restore()

	shutdownCalled := false
	tel := telemetry.New(telemetry.Config{
		Shutdown: func(context.Context) error {
			shutdownCalled = true
			return errors.New("flush failed")
		},
	})

	result, err := Run(context.Background(), Request{
		Prompt:    "test",
		Provider:  provider,
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.True(t, shutdownCalled)
	assert.Contains(t, logs.String(), "telemetry: shutdown failed")
	assert.Contains(t, logs.String(), "flush failed")
}

func TestRun_SessionStartEventIncludesMetadata(t *testing.T) {
	provider := &identityProvider{
		mockProvider: mockProvider{
			responses: []Response{
				{Content: "done", Usage: TokenUsage{Total: 10}},
			},
		},
		provider: "lmstudio",
		model:    "gpt-4o",
	}

	var startPayload map[string]any
	_, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
		WorkDir:  "/tmp/project",
		Callback: func(e Event) {
			if e.Type != EventSessionStart {
				return
			}
			require.NoError(t, json.Unmarshal(e.Data, &startPayload))
		},
	})
	require.NoError(t, err)
	require.NotNil(t, startPayload)
	assert.Equal(t, "lmstudio", startPayload["provider"])
	assert.Equal(t, "gpt-4o", startPayload["model"])
	assert.Equal(t, "/tmp/project", startPayload["work_dir"])
}

func TestRun_ChatSpanFallsBackToSessionIdentityWithoutChatMetadata(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	provider := &cancelingIdentityProvider{
		provider: "virtual",
		model:    "gpt-4o",
		cancel:   cancel,
	}

	result, err := Run(ctx, Request{
		Prompt:    "test",
		Provider:  provider,
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 2)

	chat := findSpan(t, ended, "chat gpt-4o")
	assert.Equal(t, "virtual", attrString(t, chat.Attributes(), telemetry.KeyProviderSystem))
	assert.False(t, hasAttr(chat.Attributes(), telemetry.KeyServerAddress))
	assert.False(t, hasAttr(chat.Attributes(), telemetry.KeyServerPort))
}

func TestRun_NonStreamingProviderPreservesAttemptMetadata(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
	}

	var responseEvent Event
	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
		Callback: func(e Event) {
			if e.Type == EventLLMResponse {
				responseEvent = e
			}
		},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	attempt := findResponseAttempt(t, responseEvent.Data)
	assert.Equal(t, "openai", attempt["provider_name"])
	assert.Equal(t, "openai", attempt["provider_system"])
	assert.Equal(t, "gpt-4o", attempt["requested_model"])
	assert.Equal(t, "gpt-4o", attempt["response_model"])
	assert.Equal(t, "gpt-4o", attempt["resolved_model"])

	cost, ok := attempt["cost"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "unknown", cost["source"])
}

func TestRun_MultipleToolCalls(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
					{ID: "tc2", Name: "read", Arguments: json.RawMessage(`{"path":"b.go"}`)},
				},
				Usage: TokenUsage{Total: 20},
			},
			{Content: "Both files read.", Usage: TokenUsage{Total: 30}},
		},
	}

	readTool := &mockTool{name: "read", result: "content"}

	result, err := Run(context.Background(), Request{
		Prompt:   "read both files",
		Provider: provider,
		Tools:    []Tool{readTool},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	require.Len(t, result.ToolCalls, 2)
}

func TestRun_CostAccumulation(t *testing.T) {
	firstCost := 0.0075
	secondCost := 0.015

	provider := &mockProvider{
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{}`)},
				},
				Usage: TokenUsage{Input: 1000, Output: 500, Total: 1500},
				Model: "unknown-model-xyz",
				Attempt: &AttemptMetadata{
					ProviderName:   "gateway",
					ProviderSystem: "gateway",
					RequestedModel: "unknown-model-xyz",
					ResponseModel:  "unknown-model-xyz",
					ResolvedModel:  "unknown-model-xyz",
					Cost: &CostAttribution{
						Source:   CostSourceConfigured,
						Amount:   &firstCost,
						Currency: "USD",
					},
				},
			},
			{
				Content: "done",
				Usage:   TokenUsage{Input: 2000, Output: 1000, Total: 3000},
				Model:   "unknown-model-xyz",
				Attempt: &AttemptMetadata{
					ProviderName:   "gateway",
					ProviderSystem: "gateway",
					RequestedModel: "unknown-model-xyz",
					ResponseModel:  "unknown-model-xyz",
					ResolvedModel:  "unknown-model-xyz",
					Cost: &CostAttribution{
						Source:   CostSourceProviderReported,
						Amount:   &secondCost,
						Currency: "USD",
					},
				},
			},
		},
	}

	readTool := &mockTool{name: "read", result: "content"}

	var responseCosts []float64

	result, err := Run(context.Background(), Request{
		Prompt:   "test cost",
		Provider: provider,
		Tools:    []Tool{readTool},
		Callback: func(e Event) {
			if e.Type != EventLLMResponse {
				return
			}
			payload := findResponsePayload(t, e.Data)
			if costVal, ok := payload["cost_usd"].(float64); ok {
				responseCosts = append(responseCosts, costVal)
			}
		},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	expected := firstCost + secondCost
	assert.InDelta(t, expected, result.CostUSD, 1e-9)
	assert.Greater(t, result.CostUSD, 0.0)
	require.Len(t, responseCosts, 2)
	assert.InDelta(t, firstCost, responseCosts[0], 1e-9)
	assert.InDelta(t, secondCost, responseCosts[1], 1e-9)
}

func TestRun_UnknownCostDoesNotUseDefaultPricing(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 100, Output: 50, Total: 150},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
	}

	var responsePayload map[string]any
	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
		Callback: func(e Event) {
			if e.Type == EventLLMResponse {
				responsePayload = findResponsePayload(t, e.Data)
			}
		},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Zero(t, result.CostUSD, "deprecated scalar mirror must not expose an unknown sentinel")
	assert.Nil(t, result.FinalCostUSD)
	assert.Equal(t, SessionCostSourceUnknown, result.FinalCostSource)
	require.NotNil(t, responsePayload)
	_, ok := responsePayload["cost_usd"]
	assert.False(t, ok, "unknown-cost llm.response must omit cost_usd")
}

func TestRun_ConfiguredRuntimeCostAppliesWhenExactMatch(t *testing.T) {
	configuredCost := 0.0125
	provider := &mockProvider{
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 100, Output: 50, Total: 150},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
	}

	tel := telemetry.New(telemetry.Config{
		Pricing: telemetry.RuntimePricing{
			"openai": {
				"gpt-4o": {
					Amount:     &configuredCost,
					Currency:   "USD",
					PricingRef: "openai/gpt-4o",
				},
			},
		},
	})

	var responseCost float64
	result, err := Run(context.Background(), Request{
		Prompt:    "test",
		Provider:  provider,
		Telemetry: tel,
		Callback: func(e Event) {
			if e.Type != EventLLMResponse {
				return
			}
			payload := findResponsePayload(t, e.Data)
			costVal, ok := payload["cost_usd"].(float64)
			require.True(t, ok, "configured-cost llm.response must include cost_usd")
			responseCost = costVal
		},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.InDelta(t, configuredCost, result.CostUSD, 1e-9)
	assert.InDelta(t, configuredCost, responseCost, 1e-9)
}

func TestRun_ConfiguredRuntimeCostRequiresExactMatch(t *testing.T) {
	configuredCost := 0.0125
	provider := &mockProvider{
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 100, Output: 50, Total: 150},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
	}

	tel := telemetry.New(telemetry.Config{
		Pricing: telemetry.RuntimePricing{
			"openai": {
				"gpt-4.1": {
					Amount:     &configuredCost,
					Currency:   "USD",
					PricingRef: "openai/gpt-4.1",
				},
			},
		},
	})

	var responsePayload map[string]any
	result, err := Run(context.Background(), Request{
		Prompt:    "test",
		Provider:  provider,
		Telemetry: tel,
		Callback: func(e Event) {
			if e.Type == EventLLMResponse {
				responsePayload = findResponsePayload(t, e.Data)
			}
		},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Zero(t, result.CostUSD, "deprecated scalar mirror must not expose an unknown sentinel")
	assert.Nil(t, result.FinalCostUSD)
	assert.Equal(t, SessionCostSourceUnknown, result.FinalCostSource)
	require.NotNil(t, responsePayload)
	_, ok := responsePayload["cost_usd"]
	assert.False(t, ok, "non-matching runtime pricing must not invent cost")
}

func TestRun_EmitsCostAttributesOnChatAndRootSpans(t *testing.T) {
	t.Run("configured-cost", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		configuredCost := 0.0125
		tel := telemetry.New(telemetry.Config{
			TracerProvider: tp,
			Pricing: telemetry.RuntimePricing{
				"openai": {
					"gpt-4o": {
						Amount:     &configuredCost,
						Currency:   "USD",
						PricingRef: "openai/gpt-4o",
					},
				},
			},
		})

		provider := &mockProvider{
			responses: []Response{
				{
					Content: "done",
					Usage:   TokenUsage{Input: 100, Output: 50, Total: 150},
					Model:   "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
			},
		}

		result, err := Run(context.Background(), Request{
			Prompt:    "test",
			Provider:  provider,
			Telemetry: tel,
		})
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, result.Status)
		assert.InDelta(t, configuredCost, result.CostUSD, 1e-9)

		ended := recorder.Ended()
		require.Len(t, ended, 2)
		root := spanByName(t, ended, "invoke_agent fizeau")
		chat := spanByAttrInt(t, ended, telemetry.KeyTurnIndex, 1, telemetry.KeyAttemptIndex, 1)

		assert.Equal(t, string(CostSourceConfigured), attrString(t, chat.Attributes(), telemetry.KeyCostSource))
		assert.InDelta(t, configuredCost, attrFloat(t, chat.Attributes(), telemetry.KeyCostAmount), 1e-9)
		assert.Equal(t, "USD", attrString(t, chat.Attributes(), telemetry.KeyCostCurrency))
		assert.Equal(t, "openai/gpt-4o", attrString(t, chat.Attributes(), telemetry.KeyCostPricingRef))

		assert.Equal(t, string(CostSourceConfigured), attrString(t, root.Attributes(), telemetry.KeyCostSource))
		assert.InDelta(t, configuredCost, attrFloat(t, root.Attributes(), telemetry.KeyCostAmount), 1e-9)
		assert.Equal(t, "USD", attrString(t, root.Attributes(), telemetry.KeyCostCurrency))
		assert.Equal(t, "openai/gpt-4o", attrString(t, root.Attributes(), telemetry.KeyCostPricingRef))
	})

	t.Run("mixed-known-cost-provenance", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		providerCost := 0.01
		configuredCost := 0.02
		tel := telemetry.New(telemetry.Config{
			TracerProvider: tp,
			Pricing: telemetry.RuntimePricing{
				"openai": {
					"gpt-4o": {
						Amount:     &configuredCost,
						Currency:   "USD",
						PricingRef: "openai/gpt-4o",
					},
				},
			},
		})

		readTool := &mockTool{name: "read", result: "package main\n"}
		provider := &mockProvider{
			responses: []Response{
				{
					ToolCalls: []ToolCall{
						{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
					},
					Usage: TokenUsage{Input: 20, Output: 10, Total: 30},
					Model: "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source:     CostSourceProviderReported,
							Currency:   "USD",
							Amount:     &providerCost,
							PricingRef: "openai/billed",
						},
					},
				},
				{
					Content: "done",
					Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
					Model:   "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
			},
		}

		result, err := Run(context.Background(), Request{
			Prompt:    "read main.go and finish",
			Provider:  provider,
			Tools:     []Tool{readTool},
			Telemetry: tel,
		})
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, result.Status)
		assert.InDelta(t, 0.03, result.CostUSD, 1e-9)

		ended := recorder.Ended()
		require.Len(t, ended, 4)
		root := spanByName(t, ended, "invoke_agent fizeau")
		chatOne := spanByAttrInt(t, ended, telemetry.KeyTurnIndex, 1, telemetry.KeyAttemptIndex, 1)
		chatTwo := spanByAttrInt(t, ended, telemetry.KeyTurnIndex, 2, telemetry.KeyAttemptIndex, 1)

		assert.Equal(t, string(CostSourceProviderReported), attrString(t, chatOne.Attributes(), telemetry.KeyCostSource))
		assert.InDelta(t, providerCost, attrFloat(t, chatOne.Attributes(), telemetry.KeyCostAmount), 1e-9)
		assert.Equal(t, string(CostSourceConfigured), attrString(t, chatTwo.Attributes(), telemetry.KeyCostSource))
		assert.InDelta(t, configuredCost, attrFloat(t, chatTwo.Attributes(), telemetry.KeyCostAmount), 1e-9)

		assert.InDelta(t, 0.03, attrFloat(t, root.Attributes(), telemetry.KeyCostAmount), 1e-9)
		assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostSource))
		assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostCurrency))
		assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostPricingRef))
	})

	t.Run("unknown-cost", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		tel := telemetry.New(telemetry.Config{TracerProvider: tp})

		provider := &mockProvider{
			responses: []Response{
				{
					Content: "done",
					Usage:   TokenUsage{Input: 100, Output: 50, Total: 150},
					Model:   "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
			},
		}

		result, err := Run(context.Background(), Request{
			Prompt:    "test",
			Provider:  provider,
			Telemetry: tel,
		})
		require.NoError(t, err)
		assert.Equal(t, StatusSuccess, result.Status)
		assert.Zero(t, result.CostUSD, "deprecated scalar mirror must not expose an unknown sentinel")
		assert.Nil(t, result.FinalCostUSD)
		assert.Equal(t, SessionCostSourceUnknown, result.FinalCostSource)

		ended := recorder.Ended()
		require.Len(t, ended, 2)
		root := spanByName(t, ended, "invoke_agent fizeau")
		chat := spanByAttrInt(t, ended, telemetry.KeyTurnIndex, 1, telemetry.KeyAttemptIndex, 1)

		assert.Equal(t, string(CostSourceUnknown), attrString(t, chat.Attributes(), telemetry.KeyCostSource))
		assert.False(t, hasAttr(chat.Attributes(), telemetry.KeyCostAmount))
		assert.Equal(t, string(CostSourceUnknown), attrString(t, root.Attributes(), telemetry.KeyCostSource))
		assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostAmount))
	})
}

func TestRun_EmitsTraceSpansWithTurnAndAttemptIdentity(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	firstCost := func() *float64 {
		v := -1.0
		return &v
	}()

	provider := &identityProvider{
		mockProvider: mockProvider{
			responses: []Response{
				{
					ToolCalls: []ToolCall{
						{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
					},
					Usage: TokenUsage{Input: 20, Output: 10, Total: 30},
					Model: "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
							Amount: firstCost,
						},
					},
				},
				{
					Content: "done",
					Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
					Model:   "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
			},
		},
		provider: "openai",
		model:    "gpt-4o",
	}

	readTool := &mockTool{name: "read", result: "package main\n"}
	result, err := Run(context.Background(), Request{
		Prompt:    "read main.go and finish",
		Provider:  provider,
		Tools:     []Tool{readTool},
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 4)

	root := spanByName(t, ended, "invoke_agent fizeau")
	chatOne := spanByAttrInt(t, ended, telemetry.KeyTurnIndex, 1, telemetry.KeyAttemptIndex, 1)
	chatTwo := spanByAttrInt(t, ended, telemetry.KeyTurnIndex, 2, telemetry.KeyAttemptIndex, 1)
	toolSpan := spanByToolExec(t, ended, 1, 1, "read")

	require.Equal(t, root.SpanContext().TraceID(), chatOne.SpanContext().TraceID())
	require.Equal(t, root.SpanContext().TraceID(), chatTwo.SpanContext().TraceID())
	require.Equal(t, root.SpanContext().TraceID(), toolSpan.SpanContext().TraceID())
	require.Equal(t, root.SpanContext().SpanID(), chatOne.Parent().SpanID())
	require.Equal(t, root.SpanContext().SpanID(), chatTwo.Parent().SpanID())
	require.Equal(t, root.SpanContext().SpanID(), toolSpan.Parent().SpanID())

	require.Equal(t, int64(1), attrInt(t, chatOne.Attributes(), telemetry.KeyTurnIndex))
	require.Equal(t, int64(1), attrInt(t, chatOne.Attributes(), telemetry.KeyAttemptIndex))
	require.Equal(t, int64(2), attrInt(t, chatTwo.Attributes(), telemetry.KeyTurnIndex))
	require.Equal(t, int64(1), attrInt(t, chatTwo.Attributes(), telemetry.KeyAttemptIndex))
	require.Equal(t, int64(1), attrInt(t, toolSpan.Attributes(), telemetry.KeyTurnIndex))
	require.Equal(t, int64(1), attrInt(t, toolSpan.Attributes(), telemetry.KeyToolExecutionIndex))
	require.Equal(t, "read", attrString(t, toolSpan.Attributes(), telemetry.KeyToolName))
}

func TestRun_RoutingReportUpdatesResultAndRootSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	provider := &routingReportProvider{
		mockProvider: mockProvider{
			responses: []Response{{
				Content: "done",
				Usage:   TokenUsage{Input: 8, Output: 4, Total: 12},
				Model:   "healthy-runtime-model",
			}},
		},
		report: RoutingReport{
			SelectedProvider:   "openrouter",
			SelectedRoute:      "qwen3.5-27b",
			AttemptedProviders: []string{"bragi", "openrouter"},
			FailoverCount:      1,
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:           "say hi",
		Provider:         provider,
		Telemetry:        tel,
		SelectedProvider: "bragi",
		SelectedRoute:    "qwen3.5-27b",
		RequestedModel:   "qwen3.5-27b",
		ResolvedModel:    "qwen/qwen3.5-27b",
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "openrouter", result.SelectedProvider)
	assert.Equal(t, "qwen3.5-27b", result.SelectedRoute)
	assert.Equal(t, []string{"bragi", "openrouter"}, result.AttemptedProviders)
	assert.Equal(t, 1, result.FailoverCount)

	root := spanByName(t, recorder.Ended(), "invoke_agent fizeau")
	assert.Equal(t, "openrouter", attrString(t, root.Attributes(), telemetry.KeyProviderName))
	assert.Equal(t, "qwen3.5-27b", attrString(t, root.Attributes(), telemetry.KeyProviderRoute))
	assert.Equal(t, "qwen3.5-27b", attrString(t, root.Attributes(), telemetry.KeyRequestModel))
	assert.Equal(t, "qwen/qwen3.5-27b", attrString(t, root.Attributes(), telemetry.KeyProviderModelResolved))
	assert.Equal(t, "bragi,openrouter", attrString(t, root.Attributes(), telemetry.KeyAttemptedProviders))
	assert.Equal(t, int64(1), attrInt(t, root.Attributes(), telemetry.KeyFailoverCount))
}

func TestRun_EmitsRetryIndexedChatSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	provider := &retryProvider{
		outcomes: []providerOutcome{
			{err: errors.New("503 service unavailable (transient)")},
			{err: errors.New("connection reset by peer")},
			{
				response: Response{
					Content: "done",
					Usage:   TokenUsage{Input: 12, Output: 3, Total: 15},
					Model:   "gpt-4o",
				},
			},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "retry until success",
		Provider:  provider,
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 4)
	chatSpans := spansWithOperation(t, ended, "chat")
	require.Len(t, chatSpans, 3)

	attempts := make(map[int]bool)
	for _, span := range chatSpans {
		assert.Equal(t, int64(1), attrInt(t, span.Attributes(), telemetry.KeyTurnIndex))
		attempts[int(attrInt(t, span.Attributes(), telemetry.KeyAttemptIndex))] = true
	}
	assert.True(t, attempts[1])
	assert.True(t, attempts[2])
	assert.True(t, attempts[3])
}

// @covers US-005-AC1
func TestRun_StreamingChatSpanIncludesServerUsageAndTiming(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	sp := &mockStreamingProvider{
		delayFirst:   12 * time.Millisecond,
		delayBetween: 18 * time.Millisecond,
		deltas: []StreamDelta{
			{
				Content: "streamed ",
				Model:   "gpt-4o",
				Usage: &TokenUsage{
					Input:      11,
					CacheRead:  2,
					CacheWrite: 1,
				},
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
			{
				Content: "response",
				Usage: &TokenUsage{
					Output: 9,
				},
				Done: true,
			},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "test",
		Provider:  sp,
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	chatSpan := spansWithOperation(t, ended, "chat")[0]

	assert.Equal(t, "openai", attrString(t, chatSpan.Attributes(), telemetry.KeyProviderName))
	assert.Equal(t, "openai", attrString(t, chatSpan.Attributes(), telemetry.KeyProviderSystem))
	assert.Equal(t, "api.openai.com", attrString(t, chatSpan.Attributes(), telemetry.KeyServerAddress))
	assert.Equal(t, int64(443), attrInt(t, chatSpan.Attributes(), telemetry.KeyServerPort))
	assert.Equal(t, int64(11), attrInt(t, chatSpan.Attributes(), telemetry.KeyUsageInput))
	assert.Equal(t, int64(9), attrInt(t, chatSpan.Attributes(), telemetry.KeyUsageOutput))
	assert.Equal(t, int64(2), attrInt(t, chatSpan.Attributes(), telemetry.KeyUsageCacheRead))
	assert.Equal(t, int64(1), attrInt(t, chatSpan.Attributes(), telemetry.KeyUsageCacheWrite))
	assert.GreaterOrEqual(t, attrFloat(t, chatSpan.Attributes(), telemetry.KeyTimingFirstTokenMS), 12.0)
	assert.GreaterOrEqual(t, attrFloat(t, chatSpan.Attributes(), telemetry.KeyTimingGenerationMS), 18.0)
}

func TestRun_StreamingChatSpanIncludesRequestCallbackLatency(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	sp := &mockStreamingProvider{
		deltas: []StreamDelta{
			{
				Content: "streamed",
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
				},
			},
			{Done: true},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "test",
		Provider:  sp,
		Telemetry: tel,
		Callback: func(e Event) {
			if e.Type == EventLLMRequest {
				time.Sleep(30 * time.Millisecond)
			}
		},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	chatSpan := spansWithOperation(t, ended, "chat")[0]

	assert.GreaterOrEqual(t, attrFloat(t, chatSpan.Attributes(), telemetry.KeyTimingFirstTokenMS), 30.0)
}

func TestRun_ToolSpanRecordsErrors(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	provider := &identityProvider{
		mockProvider: mockProvider{
			responses: []Response{
				{
					ToolCalls: []ToolCall{
						{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
					},
					Usage: TokenUsage{Input: 20, Output: 10, Total: 30},
					Model: "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
				{
					Content: "done",
					Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
					Model:   "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "openai",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
			},
		},
		provider: "openai",
		model:    "gpt-4o",
	}

	readTool := &mockTool{name: "read", err: errors.New("boom")}
	result, err := Run(context.Background(), Request{
		Prompt:    "read main.go and finish",
		Provider:  provider,
		Tools:     []Tool{readTool},
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	ended := recorder.Ended()
	toolSpan := spanByToolExec(t, ended, 1, 1, "read")
	require.Equal(t, codes.Error, toolSpan.Status().Code)
	assert.Equal(t, "boom", toolSpan.Status().Description)
	assert.NotEmpty(t, attrString(t, toolSpan.Attributes(), telemetry.KeyErrorType))
}

func TestRun_ReasoningOverflowChatSpanRecordsContractErrorSemantics(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	chunk := strings.Repeat("x", 4096)
	var deltas []StreamDelta
	for i := 0; i < 9; i++ {
		deltas = append(deltas, StreamDelta{ReasoningContent: chunk})
	}
	sp := &streamingIdentityProvider{
		mockStreamingProvider: &mockStreamingProvider{deltas: deltas},
		provider:              "openai",
		model:                 "test-overflow-model",
	}

	result, err := Run(context.Background(), Request{
		Prompt:             "test",
		Provider:           sp,
		Telemetry:          tel,
		ReasoningByteLimit: 32 * 1024,
		RequestedModel:     "test-overflow-model",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrReasoningOverflow)
	assert.Equal(t, StatusError, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	chatSpan := spansWithOperation(t, ended, "chat")[0]
	assertContractErrorSpan(t, chatSpan, ErrReasoningOverflow.Error())
	assert.Contains(t, chatSpan.Status().Description, "test-overflow-model")
}

func TestRun_ReasoningStallChatSpanRecordsContractErrorSemantics(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	sp := &streamingIdentityProvider{
		mockStreamingProvider: &mockStreamingProvider{
			delayBetween: 30 * time.Millisecond,
			deltas: []StreamDelta{
				{ReasoningContent: "thinking..."},
				{ReasoningContent: "still thinking..."},
				{ReasoningContent: "more thinking..."},
			},
		},
		provider: "openai",
		model:    "test-stall-model",
	}

	result, err := Run(context.Background(), Request{
		Prompt:                "test",
		Provider:              sp,
		Telemetry:             tel,
		ReasoningStallTimeout: 50 * time.Millisecond,
		RequestedModel:        "test-stall-model",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrReasoningStall)
	assert.Equal(t, StatusError, result.Status)

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	chatSpan := spansWithOperation(t, ended, "chat")[0]
	assertContractErrorSpan(t, chatSpan, ErrReasoningStall.Error())
	assert.Contains(t, chatSpan.Status().Description, "test-stall-model")
}

func TestRun_SessionEndEventIncludesKnownCost(t *testing.T) {
	sessionCost := 0.0234
	provider := &mockProvider{
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 1000, Output: 500, Total: 1500},
				Model:   "claude-sonnet-4-20250514",
				Attempt: &AttemptMetadata{
					ProviderName:   "anthropic",
					ProviderSystem: "anthropic",
					RequestedModel: "claude-sonnet-4-20250514",
					ResponseModel:  "claude-sonnet-4-20250514",
					ResolvedModel:  "claude-sonnet-4-20250514",
					Cost: &CostAttribution{
						Source:   CostSourceGatewayReported,
						Amount:   &sessionCost,
						Currency: "USD",
					},
				},
			},
		},
	}

	var sessionEndData map[string]any
	cb := func(e Event) {
		if e.Type == EventSessionEnd {
			_ = json.Unmarshal(e.Data, &sessionEndData)
		}
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
		Callback: cb,
	})
	require.NoError(t, err)
	assert.InDelta(t, sessionCost, result.CostUSD, 1e-9)
	require.NotNil(t, result.FinalCostUSD)
	assert.InDelta(t, sessionCost, *result.FinalCostUSD, 1e-9)
	assert.Equal(t, SessionCostSourceReported, result.FinalCostSource)
	require.NotNil(t, sessionEndData)
	costVal, ok := sessionEndData["cost_usd"]
	require.True(t, ok, "session.end event must include cost_usd")
	assert.InDelta(t, sessionCost, costVal.(float64), 1e-9)
	assert.Equal(t, string(SessionCostSourceReported), sessionEndData["cost_source"])
}

func TestRun_SessionEndEventOmitsUnknownCost(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 100, Output: 50, Total: 150},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "openai",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
	}

	var sessionEndData map[string]any
	cb := func(e Event) {
		if e.Type == EventSessionEnd {
			_ = json.Unmarshal(e.Data, &sessionEndData)
		}
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "test",
		Provider: provider,
		Callback: cb,
	})
	require.NoError(t, err)
	assert.Zero(t, result.CostUSD, "deprecated scalar mirror must not expose an unknown sentinel")
	assert.Nil(t, result.FinalCostUSD)
	assert.Equal(t, SessionCostSourceUnknown, result.FinalCostSource)
	require.NotNil(t, sessionEndData)
	_, ok := sessionEndData["cost_usd"]
	assert.False(t, ok, "session.end event must omit cost_usd when unknown")
	assert.Equal(t, string(SessionCostSourceUnknown), sessionEndData["cost_source"])
}

func spanByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}

	require.Failf(t, "span not found", "missing span %q", name)
	var zero sdktrace.ReadOnlySpan
	return zero
}

func spansWithOperation(t *testing.T, spans []sdktrace.ReadOnlySpan, operation string) []sdktrace.ReadOnlySpan {
	t.Helper()

	var filtered []sdktrace.ReadOnlySpan
	for _, span := range spans {
		if value, ok := spanAttrString(span.Attributes(), telemetry.KeyOperationName); ok && value == operation {
			filtered = append(filtered, span)
		}
	}
	return filtered
}

func spanByAttrInt(t *testing.T, spans []sdktrace.ReadOnlySpan, key1 string, value1 int64, key2 string, value2 int64) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if v1, ok := spanAttrInt(span.Attributes(), key1); ok && v1 == value1 {
			if v2, ok := spanAttrInt(span.Attributes(), key2); ok && v2 == value2 {
				return span
			}
		}
	}

	require.Failf(t, "span not found", "missing span with %s=%d and %s=%d", key1, value1, key2, value2)
	var zero sdktrace.ReadOnlySpan
	return zero
}

func spanByToolExec(t *testing.T, spans []sdktrace.ReadOnlySpan, turnIndex, execIndex int64, toolName string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		turn, okTurn := spanAttrInt(span.Attributes(), telemetry.KeyTurnIndex)
		exec, okExec := spanAttrInt(span.Attributes(), telemetry.KeyToolExecutionIndex)
		name, okName := spanAttrString(span.Attributes(), telemetry.KeyToolName)
		if okTurn && okExec && okName && turn == turnIndex && exec == execIndex && name == toolName {
			return span
		}
	}

	require.Failf(t, "span not found", "missing tool span %q turn=%d exec=%d", toolName, turnIndex, execIndex)
	var zero sdktrace.ReadOnlySpan
	return zero
}

func attrString(t *testing.T, attrs []attribute.KeyValue, key string) string {
	t.Helper()

	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}

	require.Failf(t, "attribute not found", "missing %q", key)
	return ""
}

func attrInt(t *testing.T, attrs []attribute.KeyValue, key string) int64 {
	t.Helper()

	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInt64()
		}
	}

	require.Failf(t, "attribute not found", "missing %q", key)
	return 0
}

func hasAttr(attrs []attribute.KeyValue, key string) bool {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return true
		}
	}
	return false
}

func captureLoopLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	return &buf, func() {
		slog.SetDefault(prev)
	}
}

func findSpan(t *testing.T, ended []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range ended {
		if span.Name() == name {
			return span
		}
	}

	require.Failf(t, "span not found", "missing span %q", name)
	var zero sdktrace.ReadOnlySpan
	return zero
}

func attrFloat(t *testing.T, attrs []attribute.KeyValue, key string) float64 {
	t.Helper()

	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsFloat64()
		}
	}

	require.Failf(t, "attribute not found", "missing %q", key)
	return 0
}

func assertContractErrorSpan(t *testing.T, span sdktrace.ReadOnlySpan, wantErr string) {
	t.Helper()

	require.Equal(t, codes.Error, span.Status().Code)
	assert.Contains(t, span.Status().Description, wantErr)
	assert.NotEmpty(t, attrString(t, span.Attributes(), telemetry.KeyErrorType))

	events := span.Events()
	require.NotEmpty(t, events, "failed span should record an exception event")

	foundException := false
	for _, event := range events {
		if event.Name == "exception" {
			foundException = true
			break
		}
	}
	assert.True(t, foundException, "failed span should include an exception event")
}

func spanAttrString(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString(), true
		}
	}
	return "", false
}

func spanAttrInt(attrs []attribute.KeyValue, key string) (int64, bool) {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInt64(), true
		}
	}
	return 0, false
}

func TestRun_ToolsAreExposedToProvider(t *testing.T) {
	prov := &recordingProvider{
		responses: []Response{
			{Content: "done"},
		},
	}

	ctx := context.Background()
	res, err := Run(ctx, Request{
		Prompt:   "test",
		Provider: prov,
		Tools: []Tool{
			&mockTool{name: "read", result: "file content"},
			&mockTool{name: "bash", result: "ok"},
		},
		MaxIterations: 10,
	})
	require.NoError(t, err)
	require.Equal(t, StatusSuccess, res.Status)

	require.Len(t, prov.toolCalls, 1, "provider Chat should have been called once with tools")
	require.Len(t, prov.toolCalls[0], 2, "two tool definitions should reach the provider")
	assert.Equal(t, "read", prov.toolCalls[0][0].Name)
	assert.Equal(t, "bash", prov.toolCalls[0][1].Name)
}

// overflowProvider returns an overflow error for the first N calls, then
// succeeds. Used to test the overflow-compaction recovery path.
type overflowProvider struct {
	failCount int
	calls     int
	success   Response
}

func (p *overflowProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	p.calls++
	if p.calls <= p.failCount {
		return Response{}, errors.New("context length exceeded: reduce your message length")
	}
	return p.success, nil
}

func TestRun_OverflowTriggersCompactionAndRetrySucceeds(t *testing.T) {
	// Provider fails once with overflow, then succeeds after compaction.
	provider := &overflowProvider{
		failCount: 1,
		success:   Response{Content: "done after compaction", Usage: TokenUsage{Total: 5}},
	}

	compactionCalls := 0
	compactor := func(ctx context.Context, input CompactionInput, prov Provider) ([]Message, *CompactionResult, error) {
		compactionCalls++
		// Return a shorter message list to signal compaction occurred.
		shortened := input.History[:1]
		return shortened, &CompactionResult{Summary: "compacted", TokensBefore: 100, TokensAfter: 20}, nil
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "test overflow recovery",
		Provider:  provider,
		Compactor: compactor,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "done after compaction", result.Output)
	// Pre-turn compaction runs once (no-op since not over budget), overflow
	// compaction runs once after the overflow error.
	assert.GreaterOrEqual(t, compactionCalls, 1, "compaction should have been triggered")
	assert.Equal(t, 2, provider.calls, "provider should have been called twice: once failing, once succeeding")
}

func TestRun_OverflowWithNoCompactorReturnsError(t *testing.T) {
	// Provider always returns overflow; no compactor configured.
	provider := &overflowProvider{
		failCount: 99,
		success:   Response{Content: "should not reach"},
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "test overflow no compactor",
		Provider: provider,
		// No Compactor set.
	})
	require.Error(t, err)
	assert.Equal(t, StatusError, result.Status)
	assert.Contains(t, err.Error(), "provider error")
}

func TestRun_OverflowCompactionNoFitReturnsError(t *testing.T) {
	// Provider returns overflow; pre-turn compaction is a no-op (returns nil
	// result), but overflow-triggered compaction returns ErrCompactionNoFit.
	provider := &overflowProvider{
		failCount: 99,
		success:   Response{Content: "should not reach"},
	}

	compactionCalls := 0
	compactor := func(ctx context.Context, input CompactionInput, prov Provider) ([]Message, *CompactionResult, error) {
		compactionCalls++
		if compactionCalls == 1 {
			// Pre-turn compaction: no-op (not over budget).
			return input.History, nil, nil
		}
		// Overflow-triggered compaction: can't fit.
		return input.History, nil, ErrCompactionNoFit
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "test overflow compaction no fit",
		Provider:  provider,
		Compactor: compactor,
	})
	require.Error(t, err)
	assert.Equal(t, StatusError, result.Status)
	assert.Contains(t, err.Error(), "provider error")
	// Provider called once (overflow error), compaction called twice
	// (pre-turn no-op, then overflow recovery ErrCompactionNoFit).
	assert.Equal(t, 1, provider.calls)
	assert.Equal(t, 2, compactionCalls)
}

// TestRun_NoOpCompactionEmitsNoEvents verifies that when the compactor
// returns the no-op signal (msgs, nil, nil), neither EventCompactionStart
// nor EventCompactionEnd is emitted. Per CONTRACT-003-fizeau-service:
// "type=compaction events fire ONLY when actual work was performed.
// No-op compactions emit nothing."
//
// Regression guard for [agent-fbc6b277]: before this suppression landed,
// every iteration of execute-loop emitted a noisy pair of start+end events
// even when nothing was compacted, polluting session logs at scale.
func TestRun_NoOpCompactionEmitsNoEvents(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{Content: "done", Usage: TokenUsage{Total: 5}},
		},
	}

	compactionCalls := 0
	compactor := func(ctx context.Context, input CompactionInput, prov Provider) ([]Message, *CompactionResult, error) {
		compactionCalls++
		// Pure no-op: tell the loop nothing happened.
		return input.History, nil, nil
	}

	var compactionEvents []Event
	callback := func(e Event) {
		if e.Type == EventCompactionStart || e.Type == EventCompactionEnd {
			compactionEvents = append(compactionEvents, e)
		}
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "no-op compaction must emit no events",
		Provider:  provider,
		Compactor: compactor,
		Callback:  callback,
	})

	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.GreaterOrEqual(t, compactionCalls, 1, "compactor should have been invoked at least once (pre-iteration check)")
	assert.Empty(t, compactionEvents, "no-op compactions must emit zero events at default verbosity (per CONTRACT-003-fizeau-service)")
}

func TestRun_OverflowCompactionSuccessRetryStillOverflowsReturnsError(t *testing.T) {
	// Provider always returns overflow even after compaction.
	provider := &overflowProvider{
		failCount: 99,
		success:   Response{Content: "should not reach"},
	}

	compactionCalls := 0
	compactor := func(ctx context.Context, input CompactionInput, prov Provider) ([]Message, *CompactionResult, error) {
		compactionCalls++
		// Return a shorter list to signal compaction occurred.
		if len(input.History) > 1 {
			return input.History[:1], &CompactionResult{Summary: "compacted", TokensBefore: 100, TokensAfter: 20}, nil
		}
		return input.History, nil, nil
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "test double overflow",
		Provider:  provider,
		Compactor: compactor,
	})
	require.Error(t, err)
	assert.Equal(t, StatusError, result.Status)
	assert.Contains(t, err.Error(), "provider error")
	// Provider should be called at most twice: once initially overflows,
	// compaction runs, retry still overflows — no infinite loop.
	assert.LessOrEqual(t, provider.calls, 3, "must not loop indefinitely on repeated overflow")
}

func TestRun_ToolCallLoopDetection(t *testing.T) {
	// AC-FEAT-001-09: loop exits with ErrToolCallLoop when the agent produces
	// byte-identical tool calls (same name + args fingerprint) for
	// toolCallLoopLimit (=5) consecutive turns.
	//
	// Provider returns the same tool call 6 times in a row. The loop should
	// abort exactly on the 5th identical turn.
	loopCall := ToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}
	provider := &mockProvider{
		responses: []Response{
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{Content: "should not reach"},
		},
	}
	tool := &mockTool{name: "bash", result: "compile error"}

	result, err := Run(context.Background(), Request{
		Prompt:   "run tests",
		Provider: provider,
		Tools:    []Tool{tool},
	})
	require.ErrorIs(t, err, ErrToolCallLoop)
	assert.Equal(t, StatusError, result.Status)
	assert.Equal(t, 5, provider.callCount, "should abort after 5 identical consecutive turns")
}

func TestRun_ToolCallLoopCounterResetsOnDifferentCall(t *testing.T) {
	// Two identical calls, then a different call, then same again — counter resets.
	// With toolCallLoopLimit=5 the loop should not abort in this sequence.
	callA := ToolCall{
		ID:        "call-a",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}
	callB := ToolCall{
		ID:        "call-b",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go build ./..."}`),
	}
	provider := &mockProvider{
		responses: []Response{
			{ToolCalls: []ToolCall{callA}},
			{ToolCalls: []ToolCall{callA}}, // consecutive count = 1
			{ToolCalls: []ToolCall{callB}}, // different — resets counter
			{ToolCalls: []ToolCall{callA}}, // consecutive count starts again at 0
			{Content: "done"},
		},
	}
	tool := &mockTool{name: "bash", result: "ok"}

	result, err := Run(context.Background(), Request{
		Prompt:   "run tests",
		Provider: provider,
		Tools:    []Tool{tool},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, 5, provider.callCount, "should run all 5 turns without aborting")
}

// barrierTool is a test Tool that uses a barrier to prove two goroutines run
// concurrently. Parallel() returns true so the agent loop will batch these.
type barrierTool struct {
	name    string
	barrier chan struct{} // closed once at least 2 goroutines are inside Execute
	arrived *sync.WaitGroup
	mu      *sync.Mutex
	calls   *int
}

func (b *barrierTool) Name() string            { return b.name }
func (b *barrierTool) Description() string     { return "barrier tool" }
func (b *barrierTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (b *barrierTool) Parallel() bool          { return true }
func (b *barrierTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	b.mu.Lock()
	*b.calls++
	b.mu.Unlock()
	b.arrived.Done()
	// Block until the barrier is released (i.e., all tools have arrived).
	<-b.barrier
	return b.name + "_result", nil
}

func TestRun_ParallelToolExecution(t *testing.T) {
	// barrier is closed once all 3 goroutines have called Done() on arrived.
	barrier := make(chan struct{})
	var arrived sync.WaitGroup
	arrived.Add(3)

	var mu sync.Mutex
	calls := 0

	// Release the barrier once all 3 have arrived, in a background goroutine.
	go func() {
		arrived.Wait()
		close(barrier)
	}()

	makeBarrierTool := func(name string) Tool {
		return &barrierTool{
			name:    name,
			barrier: barrier,
			arrived: &arrived,
			mu:      &mu,
			calls:   &calls,
		}
	}

	toolA := makeBarrierTool("read_a")
	toolB := makeBarrierTool("read_b")
	toolC := makeBarrierTool("read_c")

	provider := &mockProvider{
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read_a", Arguments: json.RawMessage(`{}`)},
					{ID: "tc2", Name: "read_b", Arguments: json.RawMessage(`{}`)},
					{ID: "tc3", Name: "read_c", Arguments: json.RawMessage(`{}`)},
				},
				Usage: TokenUsage{Total: 30},
			},
			{Content: "all done", Usage: TokenUsage{Total: 10}},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:   "read three files",
		Provider: provider,
		Tools:    []Tool{toolA, toolB, toolC},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)

	// All three tools must have been called.
	assert.Equal(t, 3, calls, "all 3 parallel tools should have been called")

	// Results must be in original order.
	require.Len(t, result.ToolCalls, 3)
	assert.Equal(t, "read_a", result.ToolCalls[0].Tool)
	assert.Equal(t, "read_b", result.ToolCalls[1].Tool)
	assert.Equal(t, "read_c", result.ToolCalls[2].Tool)
	// The test itself proves concurrency: if tools ran sequentially the barrier
	// would never be released (deadlock / timeout) because each tool blocks until
	// all 3 have arrived. If we reach here without deadlock, they ran concurrently.
}

// TestRun_MidTurnOverflowRetry verifies that when the provider returns a
// context-overflow error mid-turn (after at least one tool call has already
// occurred), the agent runs compaction, rebuilds providerMessages from the
// compacted history, retries the provider call, and completes the turn
// normally with no error returned to the caller.
func TestRun_MidTurnOverflowRetry(t *testing.T) {
	toolCall := ToolCall{
		ID:        "tc1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"echo hello"}`),
	}

	outcomes := []providerOutcome{
		// Iteration 0: return a tool call so we enter the tool-execution path.
		{response: Response{
			ToolCalls: []ToolCall{toolCall},
			Usage:     TokenUsage{Total: 10},
		}},
		// Iteration 1, attempt 1: context overflow after tool output.
		{err: errors.New("context length exceeded: reduce your message length")},
		// Iteration 1, attempt 2 (after compaction): final text response.
		{response: Response{
			Content: "done after mid-turn overflow recovery",
			Usage:   TokenUsage{Total: 5},
		}},
	}

	rp := &retryProvider{outcomes: outcomes}

	compactionCalls := 0
	compactor := func(ctx context.Context, input CompactionInput, prov Provider) ([]Message, *CompactionResult, error) {
		compactionCalls++
		// Calls 1, 2, 3 are pre-turn iter 0, mid-turn iter 0, and pre-turn iter 1
		// respectively — all no-ops. Call 4 is the overflow-triggered compaction
		// in iteration 1 and is where we actually compact.
		if compactionCalls < 4 {
			return input.History, nil, nil
		}
		// Overflow-triggered compaction: shorten the history.
		if len(input.History) <= 1 {
			return input.History, nil, nil
		}
		shortened := input.History[:1]
		return shortened, &CompactionResult{Summary: "overflow compaction", TokensBefore: 200, TokensAfter: 10}, nil
	}

	// Capture compaction end events to verify at least one was emitted.
	var compactionEndEvents []map[string]any
	callback := func(e Event) {
		if e.Type == EventCompactionEnd {
			var payload map[string]any
			if err := json.Unmarshal(e.Data, &payload); err == nil {
				compactionEndEvents = append(compactionEndEvents, payload)
			}
		}
	}

	tool := &mockTool{name: "bash", result: "hello"}

	result, err := Run(context.Background(), Request{
		Prompt:    "run bash and report",
		Provider:  rp,
		Tools:     []Tool{tool},
		Compactor: compactor,
		Callback:  callback,
	})

	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "done after mid-turn overflow recovery", result.Output)

	// Provider should be called 3 times:
	//   call 1 — iteration 0, returns tool call
	//   call 2 — iteration 1, overflow error
	//   call 3 — iteration 1 retry after compaction, returns final text
	assert.Equal(t, 3, rp.callCount, "provider should be called 3 times: tool-call turn, overflow, then retry")

	// Compaction must have run at least once for the overflow recovery.
	assert.GreaterOrEqual(t, compactionCalls, 1, "compaction should have been triggered by overflow")

	// At least one compaction end event should have been emitted.
	require.NotEmpty(t, compactionEndEvents, "at least one compaction end event should have been emitted")
}

// planningRecorder records every Chat call (including those with nil tools)
// so planning-mode tests can assert tool-arg shape and call ordering.
type planningRecorder struct {
	responses []Response
	errs      []error
	callCount int
	calls     []struct {
		messages []Message
		tools    []ToolDef
		toolsNil bool
	}
}

func (p *planningRecorder) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	p.calls = append(p.calls, struct {
		messages []Message
		tools    []ToolDef
		toolsNil bool
	}{
		messages: append([]Message(nil), messages...),
		tools:    append([]ToolDef(nil), tools...),
		toolsNil: tools == nil,
	})
	idx := p.callCount
	p.callCount++
	var err error
	if idx < len(p.errs) {
		err = p.errs[idx]
	}
	if idx < len(p.responses) {
		return p.responses[idx], err
	}
	return Response{}, err
}

func TestPlanningMode(t *testing.T) {
	prov := &planningRecorder{
		responses: []Response{
			{Content: "PLAN BODY", Usage: TokenUsage{Input: 5, Output: 7, Total: 12}, Model: "plan-model"},
			{Content: "final answer", Usage: TokenUsage{Input: 3, Output: 4, Total: 7}, Model: "main-model"},
		},
	}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	result, err := Run(context.Background(), Request{
		Prompt:       "do the thing",
		SystemPrompt: "be helpful",
		Provider:     prov,
		PlanningMode: true,
		Callback:     cb,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "final answer", result.Output)

	require.Equal(t, 2, prov.callCount, "expected planning + main provider calls")

	planCall := prov.calls[0]
	assert.True(t, planCall.toolsNil, "planning call must pass nil tools")
	require.Len(t, planCall.messages, 2)
	assert.Equal(t, RoleSystem, planCall.messages[0].Role)
	assert.Equal(t, "be helpful", planCall.messages[0].Content)
	assert.Equal(t, RoleUser, planCall.messages[1].Role)
	assert.Contains(t, planCall.messages[1].Content, "do the thing")
	assert.Contains(t, planCall.messages[1].Content, "concise plan")

	mainCall := prov.calls[1]
	assert.False(t, mainCall.toolsNil, "main call must pass non-nil tools slice")
	// Main call carries: system + user prompt + injected <plan> assistant msg.
	require.GreaterOrEqual(t, len(mainCall.messages), 3)
	planAssistant := mainCall.messages[len(mainCall.messages)-1]
	assert.Equal(t, RoleAssistant, planAssistant.Role)
	assert.Equal(t, "<plan>\nPLAN BODY\n</plan>", planAssistant.Content)

	// Token accumulation: 12 (planning) + 7 (main) = 19 total.
	assert.Equal(t, 19, result.Tokens.Total, "tokens from planning call must accumulate")
	assert.Equal(t, 8, result.Tokens.Input)
	assert.Equal(t, 11, result.Tokens.Output)

	// Event ordering: session.start, llm.request(plan), llm.response(plan),
	// planning.turn, llm.request(main), llm.response(main), session.end.
	var firstPlanningTurn, firstMainRequest int = -1, -1
	for i, e := range events {
		if e.Type == EventPlanningTurn && firstPlanningTurn == -1 {
			firstPlanningTurn = i
		}
		if e.Type == EventLLMRequest && firstPlanningTurn != -1 && i > firstPlanningTurn && firstMainRequest == -1 {
			firstMainRequest = i
		}
	}
	require.NotEqual(t, -1, firstPlanningTurn, "planning.turn event must be emitted")
	require.NotEqual(t, -1, firstMainRequest, "main loop llm.request must be emitted after planning.turn")
	assert.Less(t, firstPlanningTurn, firstMainRequest, "planning.turn must precede main loop llm.request")

	// EventLLMResponse for the planning call must precede EventPlanningTurn.
	planningRespBeforeTurn := false
	for i := 0; i < firstPlanningTurn; i++ {
		if events[i].Type == EventLLMResponse {
			planningRespBeforeTurn = true
		}
	}
	assert.True(t, planningRespBeforeTurn, "EventLLMResponse must be emitted before EventPlanningTurn")

	// EventPlanningTurn payload includes plan/usage/model.
	var turnPayload map[string]any
	require.NoError(t, json.Unmarshal(events[firstPlanningTurn].Data, &turnPayload))
	assert.Equal(t, "PLAN BODY", turnPayload["plan"])
	assert.Equal(t, "plan-model", turnPayload["model"])
	require.NotNil(t, turnPayload["usage"])
}

func TestPlanningModeFailure(t *testing.T) {
	prov := &planningRecorder{
		responses: []Response{
			{}, // unused — first call errors
			{Content: "final answer despite plan failure", Usage: TokenUsage{Total: 4}},
		},
		errs: []error{errors.New("planner kaboom"), nil},
	}

	var events []Event
	cb := func(e Event) { events = append(events, e) }

	result, err := Run(context.Background(), Request{
		Prompt:       "task",
		Provider:     prov,
		PlanningMode: true,
		Callback:     cb,
	})
	require.NoError(t, err, "planning failure must be non-fatal")
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, "final answer despite plan failure", result.Output)

	// No EventPlanningTurn emitted, no plan injected.
	for _, e := range events {
		assert.NotEqual(t, EventPlanningTurn, e.Type, "no planning.turn event when planning fails")
	}

	require.Equal(t, 2, prov.callCount, "main loop must still run after planning failure")
	mainCall := prov.calls[1]
	for _, m := range mainCall.messages {
		if m.Role == RoleAssistant {
			assert.NotContains(t, m.Content, "<plan>", "no <plan> assistant message when planning fails")
		}
	}
}

// TestRun_CostCapHaltsBeforeNextRequest verifies AC-FEAT-005-07: a configured
// per-run cost cap halts the loop deterministically BEFORE issuing the next
// llm.request once the running known cost plus the projected next-turn cost
// would meet or exceed the cap. We feed five turns of fixed $0.012 cost (each
// returning a tool call so the loop continues), set CostCapUSD = $0.05, and
// expect the loop to halt at the start of iteration 5: after iterations
// 0..3 the running total is $0.048; projecting +$0.012 yields $0.060 >= $0.05,
// so iteration 5's llm.request is never sent.
func TestRun_CostCapHaltsBeforeNextRequest(t *testing.T) {
	const perTurnCost = 0.012
	const cap = 0.05

	// Build five tool-call responses so the loop iterates without
	// terminating naturally; the sixth would be a final-text response but
	// we expect the cap to halt the loop before then.
	cost := perTurnCost
	responses := make([]Response, 0, 6)
	for i := 0; i < 5; i++ {
		c := cost
		// Vary ID + arguments so the tool-call-loop detector doesn't fire.
		args := json.RawMessage(`{"i":` + strconv.Itoa(i) + `}`)
		responses = append(responses, Response{
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "tc-" + strconv.Itoa(i), Name: "read", Arguments: args},
			},
			Usage: TokenUsage{Input: 100, Output: 50, Total: 150},
			Model: "test-model",
			Attempt: &AttemptMetadata{
				ProviderName:   "test",
				ProviderSystem: "test",
				ResolvedModel:  "test-model",
				Cost: &CostAttribution{
					Source:   CostSourceProviderReported,
					Amount:   &c,
					Currency: "USD",
				},
			},
		})
	}
	// Sentinel final-text response that should NEVER fire under the cap.
	responses = append(responses, Response{Content: "should not be reached"})
	provider := &mockProvider{responses: responses}
	readTool := &mockTool{name: "read", result: "ok"}

	var llmRequestCount int
	result, err := Run(context.Background(), Request{
		Prompt:     "cost cap",
		Provider:   provider,
		Tools:      []Tool{readTool},
		CostCapUSD: cap,
		Callback: func(e Event) {
			if e.Type == EventLLMRequest {
				llmRequestCount++
			}
		},
	})

	require.NoError(t, err)
	assert.Equal(t, StatusBudgetHalted, result.Status, "expected budget_halted status")
	assert.Equal(t, cap, result.CostCapUSD, "Result.CostCapUSD echoes Request.CostCapUSD")
	assert.NotNil(t, result.Error, "budget_halted run must surface a non-nil error")
	// Running cost must be at or just below cap when the gate fires.
	// 4 turns at $0.012 = $0.048; projecting +$0.012 = $0.060 >= $0.05 cap.
	assert.InDelta(t, 4*perTurnCost, result.CostUSD, 1e-9, "running cost should be 4 turns when gate fires")
	// We must have issued exactly 4 llm.request events (one per completed
	// turn) — never the 5th, which would have pushed cost past the cap.
	assert.Equal(t, 4, llmRequestCount, "5th llm.request must NOT have been issued")
	assert.Equal(t, 4, provider.callCount, "provider must have been called 4 times only")
}

// TestRun_CostCapDoesNotFireOnUnknownCost verifies the FEAT-005 §28 contract:
// when turn cost is unknown, the configured cap cannot fire and the run
// proceeds. We give the provider 3 unknown-cost turns then a terminating
// final-text response and expect a normal StatusSuccess.
func TestRun_CostCapDoesNotFireOnUnknownCost(t *testing.T) {
	provider := &mockProvider{
		responses: []Response{
			{
				ToolCalls: []ToolCall{{ID: "tc1", Name: "read", Arguments: json.RawMessage(`{}`)}},
				Usage:     TokenUsage{Input: 10, Output: 5, Total: 15},
				Model:     "unpriced-model",
				Attempt: &AttemptMetadata{
					ProviderName:  "test",
					ResolvedModel: "unpriced-model",
					Cost:          &CostAttribution{Source: CostSourceUnknown},
				},
			},
			{
				ToolCalls: []ToolCall{{ID: "tc2", Name: "read", Arguments: json.RawMessage(`{}`)}},
				Usage:     TokenUsage{Input: 10, Output: 5, Total: 15},
				Model:     "unpriced-model",
				Attempt: &AttemptMetadata{
					ProviderName:  "test",
					ResolvedModel: "unpriced-model",
					Cost:          &CostAttribution{Source: CostSourceUnknown},
				},
			},
			{
				Content: "done",
				Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
				Model:   "unpriced-model",
				Attempt: &AttemptMetadata{
					ProviderName:  "test",
					ResolvedModel: "unpriced-model",
					Cost:          &CostAttribution{Source: CostSourceUnknown},
				},
			},
		},
	}
	readTool := &mockTool{name: "read", result: "ok"}

	result, err := Run(context.Background(), Request{
		Prompt:     "unknown cost",
		Provider:   provider,
		Tools:      []Tool{readTool},
		CostCapUSD: 0.001, // tiny cap that would trip immediately if known
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status, "unknown-cost run must NOT trip the cap (FEAT-005 §28)")
	assert.Zero(t, result.CostUSD, "deprecated scalar mirror must not expose an unknown sentinel")
	assert.Nil(t, result.FinalCostUSD)
	assert.Equal(t, SessionCostSourceUnknown, result.FinalCostSource)
	assert.Equal(t, 3, provider.callCount, "all turns must execute when cost is unknown")
}

func TestRun_ToolCallLoopPivot_SinglePivotSucceeds(t *testing.T) {
	// AC-FEAT-001-09: loop hits identical-call detection at turn 5, pivots once,
	// then recovers with a different call on turn 6.
	loopCall := ToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}
	differentCall := ToolCall{
		ID:        "call-2",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go build ./..."}`),
	}
	provider := &mockProvider{
		responses: []Response{
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}}, // 5 identical — triggers pivot
			{ToolCalls: []ToolCall{differentCall}},
			{Content: "success"},
		},
	}
	tool := &mockTool{name: "bash", result: "ok"}

	var events []Event
	eventCb := func(e Event) { events = append(events, e) }

	result, err := Run(context.Background(), Request{
		Prompt:                   "test task",
		Provider:                 provider,
		Tools:                    []Tool{tool},
		ToolCallLoopPivotLimit:   1,
		ToolCallLoopPivotMessage: "Try a different approach.",
		Callback:                 eventCb,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.Equal(t, 1, result.ToolCallLoopPivots, "should have exactly one pivot")
	assert.Equal(t, 7, provider.callCount, "should run 5 identical + pivot + different + final = 7 calls")

	// Verify EventToolCallLoopPivot was emitted.
	var pivotEvent *Event
	for i := range events {
		if events[i].Type == EventToolCallLoopPivot {
			pivotEvent = &events[i]
			break
		}
	}
	require.NotNil(t, pivotEvent, "EventToolCallLoopPivot should be emitted")
	var pivotData map[string]any
	err = json.Unmarshal(pivotEvent.Data, &pivotData)
	require.NoError(t, err)
	assert.Equal(t, float64(1), pivotData["pivot_count"], "pivot_count should be 1")
	assert.Equal(t, float64(1), pivotData["pivot_limit"], "pivot_limit should be 1")
	assert.NotEmpty(t, pivotData["fingerprint"], "fingerprint should be present")

	// Verify pivot message was injected into conversation.
	found := false
	for _, msg := range result.Messages {
		if msg.Role == RoleUser && strings.Contains(msg.Content, "different approach") {
			found = true
			break
		}
	}
	assert.True(t, found, "pivot message should be injected into conversation")
}

func TestRun_ToolCallLoopPivot_ExhaustedPivotsAborts(t *testing.T) {
	// AC-FEAT-001-09: loop pivots once, but identical calls continue,
	// and we exhaust the pivot budget (limit=1), then abort.
	loopCall := ToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}
	provider := &mockProvider{
		responses: []Response{
			{ToolCalls: []ToolCall{loopCall}}, // 1
			{ToolCalls: []ToolCall{loopCall}}, // 2
			{ToolCalls: []ToolCall{loopCall}}, // 3
			{ToolCalls: []ToolCall{loopCall}}, // 4
			{ToolCalls: []ToolCall{loopCall}}, // 5 — trigger pivot (count=1/1)
			{ToolCalls: []ToolCall{loopCall}}, // 1 (reset counter)
			{ToolCalls: []ToolCall{loopCall}}, // 2
			{ToolCalls: []ToolCall{loopCall}}, // 3
			{ToolCalls: []ToolCall{loopCall}}, // 4
			{ToolCalls: []ToolCall{loopCall}}, // 5 again — abort (pivot count=1/1 exhausted)
			{Content: "should not reach"},
		},
	}
	tool := &mockTool{name: "bash", result: "ok"}

	result, err := Run(context.Background(), Request{
		Prompt:                   "test task",
		Provider:                 provider,
		Tools:                    []Tool{tool},
		ToolCallLoopPivotLimit:   1,
		ToolCallLoopPivotMessage: "Try a different approach.",
	})
	require.ErrorIs(t, err, ErrToolCallLoop)
	assert.Equal(t, StatusError, result.Status)
	assert.Equal(t, 1, result.ToolCallLoopPivots, "should have pivoted once")
	// Should stop at the second detection (turn 10), not continue further.
	assert.Equal(t, 10, provider.callCount, "should abort after second detection")
}

func TestRun_ToolCallLoopPivot_ZeroPivotLimitPreservesLegacyAbort(t *testing.T) {
	// AC-FEAT-001-09(a): ToolCallLoopPivotLimit=0 (default) preserves legacy
	// abort-only behavior: no pivoting, immediate abort on detection.
	loopCall := ToolCall{
		ID:        "call-1",
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}
	provider := &mockProvider{
		responses: []Response{
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{ToolCalls: []ToolCall{loopCall}},
			{Content: "should not reach"},
		},
	}
	tool := &mockTool{name: "bash", result: "ok"}

	var events []Event
	eventCb := func(e Event) { events = append(events, e) }

	result, err := Run(context.Background(), Request{
		Prompt:   "test task",
		Provider: provider,
		Tools:    []Tool{tool},
		// ToolCallLoopPivotLimit explicitly omitted (zero-value), preserving legacy.
		Callback: eventCb,
	})
	require.ErrorIs(t, err, ErrToolCallLoop)
	assert.Equal(t, StatusError, result.Status)
	assert.Equal(t, 0, result.ToolCallLoopPivots, "should not have pivoted")
	assert.Equal(t, 5, provider.callCount, "should abort immediately on 5 identical calls")

	// Verify no EventToolCallLoopPivot was emitted.
	for _, e := range events {
		assert.NotEqual(t, EventToolCallLoopPivot, e.Type, "should not emit pivot event in legacy mode")
	}
}
