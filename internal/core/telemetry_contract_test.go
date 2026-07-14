package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/easel/fizeau/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type conformanceProvider struct {
	provider      string
	system        string
	model         string
	serverAddress string
	serverPort    int
	responses     []Response
	callCount     int
}

func (p *conformanceProvider) SessionStartMetadata() (string, string) {
	return p.provider, p.model
}

func (p *conformanceProvider) ChatStartMetadata() (string, string, int) {
	return p.system, p.serverAddress, p.serverPort
}

func (p *conformanceProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, opts Options) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if p.callCount >= len(p.responses) {
		return Response{}, errors.New("no more responses")
	}
	resp := p.responses[p.callCount]
	p.callCount++
	return resp, nil
}

func TestRun_CONTRACT001SpanConformance(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	provider := &conformanceProvider{
		provider:      "lmstudio",
		system:        "openai",
		model:         "gpt-4o",
		serverAddress: "api.openai.com",
		serverPort:    443,
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
				},
				Usage: TokenUsage{Input: 11, Output: 9, CacheRead: 2, CacheWrite: 1, Total: 20},
				Model: "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "lmstudio",
					ProviderSystem: "openai",
					Route:          "default",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
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
					ProviderName:   "lmstudio",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
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

	assert.Equal(t, trace.SpanKindInternal, root.SpanKind())
	assert.Equal(t, trace.SpanKindClient, chatOne.SpanKind())
	assert.Equal(t, trace.SpanKindClient, chatTwo.SpanKind())
	assert.Equal(t, trace.SpanKindInternal, toolSpan.SpanKind())

	assert.Equal(t, root.SpanContext().TraceID(), chatOne.SpanContext().TraceID())
	assert.Equal(t, root.SpanContext().TraceID(), chatTwo.SpanContext().TraceID())
	assert.Equal(t, root.SpanContext().TraceID(), toolSpan.SpanContext().TraceID())
	assert.Equal(t, root.SpanContext().SpanID(), chatOne.Parent().SpanID())
	assert.Equal(t, root.SpanContext().SpanID(), chatTwo.Parent().SpanID())
	assert.Equal(t, root.SpanContext().SpanID(), toolSpan.Parent().SpanID())

	assert.Equal(t, result.SessionID, attrString(t, root.Attributes(), telemetry.KeySessionID))
	assert.Equal(t, result.SessionID, attrString(t, chatOne.Attributes(), telemetry.KeySessionID))
	assert.Equal(t, result.SessionID, attrString(t, chatTwo.Attributes(), telemetry.KeySessionID))
	assert.Equal(t, result.SessionID, attrString(t, toolSpan.Attributes(), telemetry.KeySessionID))
	assert.Equal(t, result.SessionID, attrString(t, root.Attributes(), telemetry.KeyConversationID))
	assert.Equal(t, result.SessionID, attrString(t, chatOne.Attributes(), telemetry.KeyConversationID))
	assert.Equal(t, result.SessionID, attrString(t, chatTwo.Attributes(), telemetry.KeyConversationID))
	assert.Equal(t, result.SessionID, attrString(t, toolSpan.Attributes(), telemetry.KeyConversationID))

	assert.Equal(t, "invoke_agent", attrString(t, root.Attributes(), telemetry.KeyOperationName))
	assert.Equal(t, "fizeau", attrString(t, root.Attributes(), telemetry.KeyServiceName))
	assert.Equal(t, "fizeau", attrString(t, root.Attributes(), telemetry.KeyHarnessName))
	assert.Equal(t, "Fizeau", attrString(t, root.Attributes(), telemetry.KeyAgentName))
	assert.Equal(t, "chat", attrString(t, chatOne.Attributes(), telemetry.KeyOperationName))
	assert.Equal(t, "chat", attrString(t, chatTwo.Attributes(), telemetry.KeyOperationName))
	assert.Equal(t, "execute_tool", attrString(t, toolSpan.Attributes(), telemetry.KeyOperationName))

	assert.Equal(t, "lmstudio", attrString(t, chatOne.Attributes(), telemetry.KeyProviderName))
	assert.Equal(t, "openai", attrString(t, chatOne.Attributes(), telemetry.KeyProviderSystem))
	assert.Equal(t, "default", attrString(t, chatOne.Attributes(), telemetry.KeyProviderRoute))
	assert.Equal(t, "gpt-4o", attrString(t, chatOne.Attributes(), telemetry.KeyRequestModel))
	assert.Equal(t, "gpt-4o", attrString(t, chatOne.Attributes(), telemetry.KeyResponseModel))
	assert.Equal(t, "gpt-4o", attrString(t, chatOne.Attributes(), telemetry.KeyProviderModelResolved))
	assert.Equal(t, "api.openai.com", attrString(t, chatOne.Attributes(), telemetry.KeyServerAddress))
	assert.Equal(t, int64(443), attrInt(t, chatOne.Attributes(), telemetry.KeyServerPort))

	assert.Equal(t, "read", attrString(t, toolSpan.Attributes(), telemetry.KeyToolName))
	assert.Equal(t, "function", attrString(t, toolSpan.Attributes(), telemetry.KeyToolType))
	assert.Equal(t, "call-1", attrString(t, toolSpan.Attributes(), telemetry.KeyToolCallID))
	assert.Equal(t, int64(1), attrInt(t, toolSpan.Attributes(), telemetry.KeyTurnIndex))
	assert.Equal(t, int64(1), attrInt(t, toolSpan.Attributes(), telemetry.KeyToolExecutionIndex))

	assertNoContentCaptureAttrs(t, root.Attributes())
	assertNoContentCaptureAttrs(t, chatOne.Attributes())
	assertNoContentCaptureAttrs(t, chatTwo.Attributes())
	assertNoContentCaptureAttrs(t, toolSpan.Attributes())
}

func TestTelemetryNoModelRefAttrs(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	provider := &conformanceProvider{
		provider:      "lmstudio",
		system:        "openai",
		model:         "gpt-4o",
		serverAddress: "api.openai.com",
		serverPort:    443,
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "lmstudio",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
				},
			},
		},
	}

	_, err := Run(context.Background(), Request{
		Prompt:         "say hi",
		Provider:       provider,
		Telemetry:      tel,
		RequestedModel: "gpt-4o",
		ResolvedModel:  "gpt-4o",
	})
	require.NoError(t, err)

	legacyKeys := []string{
		"ddx.request.model_ref",
		"ddx.request.resolved_model_ref",
	}
	for _, span := range recorder.Ended() {
		for _, key := range legacyKeys {
			assert.False(t, hasAttr(span.Attributes(), key), "span %q emitted legacy attr %q", span.Name(), key)
		}
	}
}

func TestRun_ReasoningTelemetryFields(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tel := telemetry.New(telemetry.Config{TracerProvider: tp})

	provider := &conformanceProvider{
		provider:      "ds4",
		system:        "ds4",
		model:         "deepseek-v4-flash",
		serverAddress: "localhost",
		serverPort:    8000,
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 10, Output: 5, Total: 15},
				Model:   "deepseek-v4-flash",
				Attempt: &AttemptMetadata{
					ProviderName:     "ds4",
					ProviderSystem:   "ds4",
					RequestedModel:   "deepseek-v4-flash",
					ResponseModel:    "deepseek-v4-flash",
					ResolvedModel:    "deepseek-v4-flash",
					ReasoningEmitted: string(ReasoningHigh),
					Cost: &CostAttribution{
						Source: CostSourceUnknown,
					},
				},
			},
		},
	}

	var events []Event
	_, err := Run(context.Background(), Request{
		Prompt:    "say hi",
		Provider:  provider,
		Reasoning: ReasoningLow,
		Telemetry: tel,
		Callback: func(e Event) {
			events = append(events, e)
		},
	})
	require.NoError(t, err)

	startEvent := eventByType(t, events, EventSessionStart)
	endEvent := eventByType(t, events, EventSessionEnd)
	responseEvent := eventByType(t, events, EventLLMResponse)

	var startData map[string]any
	require.NoError(t, json.Unmarshal(startEvent.Data, &startData))
	assert.Equal(t, "low", startData["reasoning_intent"])

	var endData map[string]any
	require.NoError(t, json.Unmarshal(endEvent.Data, &endData))
	assert.Equal(t, "low", endData["reasoning_intent"])
	assert.Equal(t, "high", endData["reasoning_emitted"])
	assert.Equal(t, "high", endData["resolved_reasoning"])

	var responseData map[string]any
	require.NoError(t, json.Unmarshal(responseEvent.Data, &responseData))
	assert.Equal(t, "low", responseData["reasoning_intent"])
	assert.Equal(t, "high", responseData["reasoning_emitted"])

	chatSpan := spanByAttrString(t, recorder.Ended(), telemetry.KeyReasoningIntent, "low", telemetry.KeyReasoningEmitted, "high")
	assert.Equal(t, "low", attrString(t, chatSpan.Attributes(), telemetry.KeyReasoningIntent))
	assert.Equal(t, "high", attrString(t, chatSpan.Attributes(), telemetry.KeyReasoningEmitted))
}

// @covers US-005-AC2
func TestRun_CONTRACT001CostPrecedence(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	configuredCost := 0.20
	reportedCost := 0.05
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

	provider := &conformanceProvider{
		provider:      "lmstudio",
		system:        "openai",
		model:         "gpt-4o",
		serverAddress: "api.openai.com",
		serverPort:    443,
		responses: []Response{
			{
				Content: "done",
				Usage:   TokenUsage{Input: 12, Output: 3, Total: 15},
				Model:   "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "lmstudio",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
					Cost: &CostAttribution{
						Source:   CostSourceProviderReported,
						Amount:   &reportedCost,
						Currency: "USD",
					},
				},
			},
		},
	}

	result, err := Run(context.Background(), Request{
		Prompt:    "report cost",
		Provider:  provider,
		Telemetry: tel,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSuccess, result.Status)
	assert.InDelta(t, reportedCost, result.CostUSD, 1e-9)

	ended := recorder.Ended()
	require.Len(t, ended, 2)

	chatSpans := spansWithOperation(t, ended, "chat")
	require.Len(t, chatSpans, 1)
	assert.Equal(t, "openai", attrString(t, chatSpans[0].Attributes(), telemetry.KeyProviderSystem))
	assert.Equal(t, string(CostSourceProviderReported), attrString(t, chatSpans[0].Attributes(), telemetry.KeyCostSource))
	assert.InDelta(t, reportedCost, attrFloat(t, chatSpans[0].Attributes(), telemetry.KeyCostAmount), 1e-9)
}

func TestRun_CONTRACT001MixedKnownCostProvenance(t *testing.T) {
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
	provider := &conformanceProvider{
		provider:      "lmstudio",
		system:        "openai",
		model:         "gpt-4o",
		serverAddress: "api.openai.com",
		serverPort:    443,
		responses: []Response{
			{
				ToolCalls: []ToolCall{
					{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
				},
				Usage: TokenUsage{Input: 20, Output: 10, Total: 30},
				Model: "gpt-4o",
				Attempt: &AttemptMetadata{
					ProviderName:   "lmstudio",
					ProviderSystem: "openai",
					Route:          "default",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
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
					ProviderName:   "lmstudio",
					ProviderSystem: "openai",
					RequestedModel: "gpt-4o",
					ResponseModel:  "gpt-4o",
					ResolvedModel:  "gpt-4o",
					ServerAddress:  "api.openai.com",
					ServerPort:     443,
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

	assert.InDelta(t, 0.03, attrFloat(t, root.Attributes(), telemetry.KeyCostAmount), 1e-9)
	assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostSource))
	assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostCurrency))
	assert.False(t, hasAttr(root.Attributes(), telemetry.KeyCostPricingRef))
}

func TestRun_CONTRACT001ThroughputFormulas(t *testing.T) {
	t.Run("output-throughput-uses-generation-window", func(t *testing.T) {
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
						Output: 9,
					},
					Attempt: &AttemptMetadata{
						ProviderName:   "lmstudio",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
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

		outputTokens := attrInt(t, chatSpan.Attributes(), telemetry.KeyUsageOutput)
		generationMS := attrFloat(t, chatSpan.Attributes(), telemetry.KeyTimingGenerationMS)
		durationMS := float64(chatSpan.EndTime().Sub(chatSpan.StartTime()) / time.Millisecond)
		expected := float64(outputTokens) / (generationMS / 1000)

		derived, ok := outputTokensPerSecond(chatSpan.Attributes(), durationMS)
		require.True(t, ok)
		assert.InDelta(t, expected, derived, 0.001)
	})

	t.Run("fallback-output-throughput-uses-first-token-when-generation-missing", func(t *testing.T) {
		attrs := []attribute.KeyValue{
			attribute.Int(telemetry.KeyUsageOutput, 100),
			attribute.Float64(telemetry.KeyTimingFirstTokenMS, 200),
		}

		derived, ok := outputTokensPerSecond(attrs, 1200)
		require.True(t, ok)
		assert.InDelta(t, 100.0, derived, 0.001)
	})

	t.Run("cached-and-input-throughput-follow-contracted-windows", func(t *testing.T) {
		attrs := []attribute.KeyValue{
			attribute.Int(telemetry.KeyUsageInput, 150),
			attribute.Int(telemetry.KeyUsageCacheRead, 25),
			attribute.Float64(telemetry.KeyTimingCacheReadMS, 250),
			attribute.Float64(telemetry.KeyTimingPrefillMS, 500),
		}

		cached, ok := cachedTokensPerSecond(attrs)
		require.True(t, ok)
		assert.InDelta(t, 100.0, cached, 0.001)

		input, ok := inputTokensPerSecond(attrs)
		require.True(t, ok)
		assert.InDelta(t, 250.0, input, 0.001)
	})

	t.Run("input-throughput-may-assume-zero-cache-read-when-field-absent", func(t *testing.T) {
		attrs := []attribute.KeyValue{
			attribute.Int(telemetry.KeyUsageInput, 80),
			attribute.Float64(telemetry.KeyTimingPrefillMS, 400),
		}

		input, ok := inputTokensPerSecond(attrs)
		require.True(t, ok)
		assert.InDelta(t, 200.0, input, 0.001)
	})
}

func BenchmarkRun_CONTRACT001TelemetryOverhead(b *testing.B) {
	benchmarkRun := func(tel telemetry.Telemetry) {
		provider := &conformanceProvider{
			provider:      "lmstudio",
			system:        "openai",
			model:         "gpt-4o",
			serverAddress: "api.openai.com",
			serverPort:    443,
			responses: []Response{
				{
					ToolCalls: []ToolCall{
						{ID: "call-1", Name: "read", Arguments: json.RawMessage(`{"path":"main.go"}`)},
					},
					Usage: TokenUsage{Input: 11, Output: 9, Total: 20},
					Model: "gpt-4o",
					Attempt: &AttemptMetadata{
						ProviderName:   "lmstudio",
						ProviderSystem: "openai",
						Route:          "default",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						ServerAddress:  "api.openai.com",
						ServerPort:     443,
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
						ProviderName:   "lmstudio",
						ProviderSystem: "openai",
						RequestedModel: "gpt-4o",
						ResponseModel:  "gpt-4o",
						ResolvedModel:  "gpt-4o",
						ServerAddress:  "api.openai.com",
						ServerPort:     443,
						Cost: &CostAttribution{
							Source: CostSourceUnknown,
						},
					},
				},
			},
		}
		readTool := &mockTool{name: "read", result: "package main\n"}
		_, _ = Run(context.Background(), Request{
			Prompt:    "read main.go and finish",
			Provider:  provider,
			Tools:     []Tool{readTool},
			Telemetry: tel,
		})
	}

	b.Run("noop", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkRun(telemetry.NewNoop())
		}
	})

	b.Run("trace-drop", func(b *testing.B) {
		b.ReportAllocs()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(dropSpanProcessor{}))
		b.Cleanup(func() {
			_ = tp.Shutdown(context.Background())
		})

		tel := telemetry.New(telemetry.Config{TracerProvider: tp})
		for i := 0; i < b.N; i++ {
			benchmarkRun(tel)
		}
	})
}

type dropSpanProcessor struct{}

func (dropSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (dropSpanProcessor) OnEnd(sdktrace.ReadOnlySpan) {}

func (dropSpanProcessor) Shutdown(context.Context) error { return nil }

func (dropSpanProcessor) ForceFlush(context.Context) error { return nil }

func assertNoContentCaptureAttrs(t *testing.T, attrs []attribute.KeyValue) {
	t.Helper()

	for _, key := range []string{
		"gen_ai.system_instructions",
		"gen_ai.input.messages",
		"gen_ai.output.messages",
		"gen_ai.tool.call.arguments",
		"gen_ai.tool.call.result",
	} {
		assert.False(t, hasAttr(attrs, key), "unexpected content capture attribute %q", key)
	}
}

func outputTokensPerSecond(attrs []attribute.KeyValue, spanDurationMS float64) (float64, bool) {
	outputTokens, ok := attrIntOk(attrs, telemetry.KeyUsageOutput)
	if !ok || outputTokens <= 0 {
		return 0, false
	}

	if generationMS, ok := attrFloatOk(attrs, telemetry.KeyTimingGenerationMS); ok && generationMS > 0 {
		return float64(outputTokens) / (generationMS / 1000), true
	}

	if firstTokenMS, ok := attrFloatOk(attrs, telemetry.KeyTimingFirstTokenMS); ok && firstTokenMS > 0 {
		remainingMS := spanDurationMS - firstTokenMS
		if remainingMS <= 0 {
			return 0, false
		}
		return float64(outputTokens) / (remainingMS / 1000), true
	}

	return 0, false
}

func cachedTokensPerSecond(attrs []attribute.KeyValue) (float64, bool) {
	cacheTokens, ok := attrIntOk(attrs, telemetry.KeyUsageCacheRead)
	if !ok || cacheTokens <= 0 {
		return 0, false
	}

	cacheReadMS, ok := attrFloatOk(attrs, telemetry.KeyTimingCacheReadMS)
	if !ok || cacheReadMS <= 0 {
		return 0, false
	}

	return float64(cacheTokens) / (cacheReadMS / 1000), true
}

func inputTokensPerSecond(attrs []attribute.KeyValue) (float64, bool) {
	inputTokens, ok := attrIntOk(attrs, telemetry.KeyUsageInput)
	if !ok || inputTokens <= 0 {
		return 0, false
	}

	prefillMS, ok := attrFloatOk(attrs, telemetry.KeyTimingPrefillMS)
	if !ok || prefillMS <= 0 {
		return 0, false
	}

	cacheReadTokens, ok := attrIntOk(attrs, telemetry.KeyUsageCacheRead)
	if !ok {
		cacheReadTokens = 0
	}

	return float64(inputTokens-cacheReadTokens) / (prefillMS / 1000), true
}

func attrIntOk(attrs []attribute.KeyValue, key string) (int64, bool) {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInt64(), true
		}
	}
	return 0, false
}

func attrFloatOk(attrs []attribute.KeyValue, key string) (float64, bool) {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsFloat64(), true
		}
	}
	return 0, false
}

func eventByType(t *testing.T, events []Event, eventType EventType) Event {
	t.Helper()
	for _, e := range events {
		if e.Type == eventType {
			return e
		}
	}
	require.Failf(t, "event not found", "missing event type %q", eventType)
	return Event{}
}

func spanByAttrString(t *testing.T, spans []sdktrace.ReadOnlySpan, key, value, key2, value2 string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if spanAttrStringValue(span.Attributes(), key) == value && spanAttrStringValue(span.Attributes(), key2) == value2 {
			return span
		}
	}
	require.Failf(t, "span not found", "missing span with %s=%s and %s=%s", key, value, key2, value2)
	var zero sdktrace.ReadOnlySpan
	return zero
}

func spanAttrStringValue(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
