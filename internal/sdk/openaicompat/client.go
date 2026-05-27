// Package openaicompat contains shared OpenAI-compatible Chat Completions
// protocol plumbing. Provider identity, cost attribution, routing, and
// provider-specific discovery live in provider packages above this layer.
package openaicompat

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	agent "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/provider/quotaheaders"
	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

// DefaultConnectTimeout bounds the TCP connect phase of every request issued
// by the OpenAI-compatible client. Go's net/http default Transport uses a
// zero-value net.Dialer with no Timeout, which on Linux waits for the kernel's
// full SYN-retransmit window (~30s) before surfacing a dial-class failure.
// A dead local endpoint is a routing input — not a slow upstream — so the
// connect phase is bounded independently of the request-streaming timeout.
const DefaultConnectTimeout = 5 * time.Second

// Config contains the protocol/client settings shared by providers that speak
// the OpenAI-compatible Chat Completions API.
type Config struct {
	BaseURL string
	APIKey  string
	Headers map[string]string
	// QuotaHeaderParser, when set, is called on every response to convert
	// the response Header into a quotaheaders.Signal. The signal is then
	// forwarded to QuotaSignalObserver. Different providers (OpenAI vs
	// OpenRouter) use different parsers, so the protocol layer takes the
	// parser as a function and stays provider-agnostic.
	QuotaHeaderParser func(http.Header, time.Time) quotaheaders.Signal
	// QuotaSignalObserver, when set together with QuotaHeaderParser, receives
	// the parsed signal on every response. The service layer wires this to
	// the provider quota state machine.
	QuotaSignalObserver func(quotaheaders.Signal)
	// RuntimeSignalObserver, when set, receives the raw response headers,
	// request latency, and any terminal error for every provider response.
	RuntimeSignalObserver func(http.Header, time.Duration, error)
	// ConnectTimeout bounds the TCP connect phase (dial) for every request.
	// Zero or negative values fall back to DefaultConnectTimeout (5s). The
	// request-streaming timeout is intentionally not bounded here: large
	// completions legitimately take minutes once the connection is established.
	ConnectTimeout time.Duration
}

// Client wraps openai-go with agent-native request and response conversion.
type Client struct {
	client         *oai.Client
	connectTimeout time.Duration
}

// NewClient creates an OpenAI-compatible protocol client.
func NewClient(cfg Config) *Client {
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = DefaultConnectTimeout
	}
	httpClient := newHTTPClient(connectTimeout)
	opts := []option.RequestOption{
		option.WithBaseURL(cfg.BaseURL),
		option.WithMaxRetries(0),
		option.WithHTTPClient(httpClient),
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	} else {
		opts = append(opts, option.WithAPIKey("not-needed"))
	}
	for k, v := range cfg.Headers {
		opts = append(opts, option.WithHeader(k, v))
	}
	// SSE comment-frame filter must sit before the debug sink so the sink
	// observes the same byte stream the decoder will see. Middlewares are
	// applied in registration order, outermost first.
	opts = append(opts, option.WithMiddleware(SSEFilterMiddleware()))
	if s := ResolveDebugSink(); s != nil {
		opts = append(opts, option.WithMiddleware(DebugMiddleware(s)))
	}
	if cfg.QuotaHeaderParser != nil && cfg.QuotaSignalObserver != nil {
		opts = append(opts, option.WithMiddleware(quotaHeaderMiddleware(cfg.QuotaHeaderParser, cfg.QuotaSignalObserver)))
	}
	if cfg.RuntimeSignalObserver != nil {
		opts = append(opts, option.WithMiddleware(runtimeSignalMiddleware(cfg.RuntimeSignalObserver)))
	}

	client := oai.NewClient(opts...)
	return &Client{client: &client, connectTimeout: connectTimeout}
}

// newHTTPClient returns an *http.Client whose Transport explicitly bounds the
// TCP connect phase via net.Dialer.Timeout. The remaining transport knobs
// mirror http.DefaultTransport so connection pooling, HTTP/2 negotiation, and
// TLS handshake behavior stay identical to stdlib defaults.
func newHTTPClient(connectTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// quotaHeaderMiddleware returns a middleware that runs `parser` on every
// response Header and forwards the resulting Signal to `observer`. The
// middleware is a no-op when the upstream call returns an error before a
// Response is produced, and runs *after* the response has been received but
// *before* the SDK consumes its body — both branches still surface the same
// (resp, err) tuple to the caller.
func quotaHeaderMiddleware(parser func(http.Header, time.Time) quotaheaders.Signal, observer func(quotaheaders.Signal)) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if resp != nil {
			signal := parser(resp.Header, time.Now())
			if signal.Present {
				observer(signal)
			}
		}
		return resp, err
	}
}

func runtimeSignalMiddleware(observer func(http.Header, time.Duration, error)) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		start := time.Now()
		resp, err := next(req)
		if observer != nil {
			var headers http.Header
			if resp != nil {
				headers = resp.Header
			}
			observer(headers, time.Since(start), err)
		}
		return resp, err
	}
}

// RequestOptions are request controls that map directly to the compatible Chat
// Completions wire shape. ExtraOptions carries provider-owned extensions such
// as non-standard reasoning/thinking fields.
type RequestOptions struct {
	MaxTokens int
	// Temperature/TopP/TopK/MinP/RepetitionPenalty are sampling controls.
	// TopP is OpenAI-standard; TopK / MinP / RepetitionPenalty are
	// non-standard OpenAI-compat extras that omlx, lmstudio, vLLM, and
	// llama.cpp accept as top-level body fields. Nil means unset
	// (server default applies). RepetitionPenalty > 1.0 prevents
	// exact-token loops.
	Temperature       *float64
	TopP              *float64
	TopK              *int
	MinP              *float64
	RepetitionPenalty *float64
	Seed              int64
	Stop              []string
	ExtraOptions      []option.RequestOption
	// CachePolicy mirrors agent.Options.CachePolicy. The OpenAI-compatible
	// protocol layer does not act on it today; it is plumbed so a future
	// caching-aware OpenAI-style provider has a typed field to consume.
	CachePolicy string
}

// ChatResult is a protocol-level non-streaming response without provider
// identity metadata.
type ChatResult struct {
	Model        string
	Content      string
	FinishReason string
	ToolCalls    []agent.ToolCall
	Usage        agent.TokenUsage
	RawUsage     string
}

// CostHook extracts provider-owned cost data from a raw usage object. The SDK
// only calls the hook; it does not know provider-specific attribution rules.
type CostHook func(rawUsage string) *agent.CostAttribution

// StreamAttemptHook builds provider-owned final attempt metadata for a stream.
type StreamAttemptHook func(responseModel string, cost *agent.CostAttribution) *agent.AttemptMetadata

// StreamHooks are optional provider callbacks used by streaming conversion.
type StreamHooks struct {
	Cost    CostHook
	Attempt StreamAttemptHook
}

// Chat executes one non-streaming Chat Completions request.
func (c *Client) Chat(ctx context.Context, model string, messages []agent.Message, tools []agent.ToolDef, opts RequestOptions) (ChatResult, error) {
	params := buildParams(model, messages, tools, opts)
	completion, err := c.client.Chat.Completions.New(ctx, params, opts.ExtraOptions...)
	if err != nil {
		return ChatResult{}, err
	}

	result := ChatResult{
		Model:    completion.Model,
		RawUsage: completion.Usage.RawJSON(),
	}
	msgReasoningContent := extractMessageReasoningContent(completion.RawJSON())
	if completion.Usage.TotalTokens != 0 {
		result.Usage = tokenUsage(
			int(completion.Usage.PromptTokens),
			int(completion.Usage.CompletionTokens),
			int(completion.Usage.TotalTokens),
			int(completion.Usage.PromptTokensDetails.CachedTokens),
		)
		result.Usage.Reasoning, result.Usage.ReasoningTokensApprox = extractReasoningTokens(result.RawUsage, msgReasoningContent)
	}

	if len(completion.Choices) > 0 {
		choice := completion.Choices[0]
		result.Content = choice.Message.Content
		result.FinishReason = string(choice.FinishReason)
		result.ToolCalls = extractToolCalls(choice.Message.ToolCalls)
	}

	return result, nil
}

// ChatStream executes one streaming Chat Completions request and converts
// chunks into agent stream deltas.
func (c *Client) ChatStream(ctx context.Context, model string, messages []agent.Message, tools []agent.ToolDef, opts RequestOptions, hooks StreamHooks) (<-chan agent.StreamDelta, error) {
	params := buildParams(model, messages, tools, opts)
	params.StreamOptions = oai.ChatCompletionStreamOptionsParam{
		IncludeUsage: oai.Bool(true),
	}

	stream := c.client.Chat.Completions.NewStreaming(ctx, params, opts.ExtraOptions...)

	ch := make(chan agent.StreamDelta, 1)
	go func() {
		defer close(ch)
		send := func(delta agent.StreamDelta) {
			delta.ArrivedAt = time.Now()
			ch <- delta
		}

		// OpenAI only sends tool call ID in the first chunk for each index;
		// subsequent argument chunks carry the index but have an empty ID.
		indexToID := make(map[int]string)
		responseModel := model
		var streamCost *agent.CostAttribution
		var aggReasoningContent strings.Builder
		for stream.Next() {
			chunk := stream.Current()
			if chunk.Model != "" {
				responseModel = chunk.Model
			}
			if hooks.Cost != nil {
				if cost := hooks.Cost(chunk.Usage.RawJSON()); cost != nil {
					streamCost = cost
				}
			}

			reasoningContent := extractReasoningContent(chunk.RawJSON())
			if reasoningContent != "" {
				aggReasoningContent.WriteString(reasoningContent)
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]

				for _, tc := range choice.Delta.ToolCalls {
					id := tc.ID
					if id != "" {
						indexToID[int(tc.Index)] = id
					} else {
						id = indexToID[int(tc.Index)]
					}
					send(agent.StreamDelta{
						Model:        chunk.Model,
						ToolCallID:   id,
						ToolCallName: tc.Function.Name,
						ToolCallArgs: tc.Function.Arguments,
					})
				}

				if choice.Delta.Content != "" || choice.FinishReason != "" || reasoningContent != "" {
					send(agent.StreamDelta{
						Model:            chunk.Model,
						Content:          choice.Delta.Content,
						ReasoningContent: reasoningContent,
						FinishReason:     string(choice.FinishReason),
					})
				} else if len(choice.Delta.ToolCalls) == 0 {
					send(agent.StreamDelta{
						Model:        chunk.Model,
						FinishReason: string(choice.FinishReason),
					})
				}
			} else {
				send(agent.StreamDelta{Model: chunk.Model})
			}

			if chunk.Usage.TotalTokens != 0 {
				usage := tokenUsage(
					int(chunk.Usage.PromptTokens),
					int(chunk.Usage.CompletionTokens),
					int(chunk.Usage.TotalTokens),
					int(chunk.Usage.PromptTokensDetails.CachedTokens),
				)
				usage.Reasoning, usage.ReasoningTokensApprox = extractReasoningTokens(chunk.Usage.RawJSON(), aggReasoningContent.String())
				send(agent.StreamDelta{Usage: &usage})
			}
		}

		if err := stream.Err(); err != nil {
			send(agent.StreamDelta{Err: classifyStreamErr(err)})
			return
		}

		done := agent.StreamDelta{
			Model: responseModel,
			Done:  true,
		}
		if hooks.Attempt != nil {
			done.Attempt = hooks.Attempt(responseModel, streamCost)
		}
		send(done)
	}()

	return ch, nil
}

func buildParams(model string, messages []agent.Message, tools []agent.ToolDef, opts RequestOptions) oai.ChatCompletionNewParams {
	params := oai.ChatCompletionNewParams{
		Model:    model,
		Messages: convertMessages(messages),
	}
	if len(tools) > 0 {
		params.Tools = convertTools(tools)
	}
	if opts.MaxTokens > 0 {
		params.MaxTokens = oai.Int(int64(opts.MaxTokens))
	}
	if opts.Temperature != nil {
		params.Temperature = oai.Float(*opts.Temperature)
	}
	if opts.TopP != nil {
		params.TopP = oai.Float(*opts.TopP)
	}
	if opts.Seed != 0 {
		params.Seed = oai.Int(opts.Seed)
	}
	if len(opts.Stop) > 0 {
		params.Stop = oai.ChatCompletionNewParamsStopUnion{OfStringArray: opts.Stop}
	}
	return params
}

func convertMessages(msgs []agent.Message) []oai.ChatCompletionMessageParamUnion {
	var result []oai.ChatCompletionMessageParamUnion
	for _, m := range msgs {
		switch m.Role {
		case agent.RoleSystem:
			result = append(result, oai.SystemMessage(m.Content))
		case agent.RoleUser:
			result = append(result, oai.UserMessage(m.Content))
		case agent.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var toolCalls []oai.ChatCompletionMessageToolCallParam
				for _, tc := range m.ToolCalls {
					toolCalls = append(toolCalls, oai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: oai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(tc.Arguments),
						},
					})
				}
				assistant := oai.ChatCompletionAssistantMessageParam{
					Content: oai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(m.Content),
					},
					ToolCalls: toolCalls,
				}
				result = append(result, oai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
			} else {
				result = append(result, oai.AssistantMessage(m.Content))
			}
		case agent.RoleTool:
			result = append(result, oai.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return result
}

func convertTools(tools []agent.ToolDef) []oai.ChatCompletionToolParam {
	var result []oai.ChatCompletionToolParam
	for _, t := range tools {
		var params map[string]interface{}
		_ = json.Unmarshal(t.Parameters, &params)

		result = append(result, oai.ChatCompletionToolParam{
			Function: oai.FunctionDefinitionParam{
				Name:        t.Name,
				Description: oai.String(t.Description),
				Parameters:  oai.FunctionParameters(params),
			},
		})
	}
	return result
}

func extractToolCalls(calls []oai.ChatCompletionMessageToolCall) []agent.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	var result []agent.ToolCall
	for _, c := range calls {
		result = append(result, agent.ToolCall{
			ID:        c.ID,
			Name:      c.Function.Name,
			Arguments: json.RawMessage(c.Function.Arguments),
		})
	}
	return result
}

func extractReasoningContent(rawJSON string) string {
	if rawJSON == "" {
		return ""
	}
	var raw struct {
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil || len(raw.Choices) == 0 {
		return ""
	}
	return raw.Choices[0].Delta.ReasoningContent
}

func tokenUsage(input, output, total, cacheRead int) agent.TokenUsage {
	usage := agent.TokenUsage{
		Input:  input,
		Output: output,
		Total:  total,
	}
	if cacheRead > 0 {
		usage.CacheRead = cacheRead
	}
	return usage
}

// extractReasoningTokens resolves the reasoning token count per ADR-010
// Amendment §10'. Resolution chain:
//  1. usage.completion_tokens_details.reasoning_tokens when present and > 0.
//  2. Top-level usage.reasoning_tokens when present and > 0.
//  3. message.reasoning_content counted with the shared token estimator when
//     the usage path is absent or reports zero and reasoningContent is
//     non-empty. Returns approx=true.
//
// Returns (tokens, approx). approx is true only in case 3.
func extractReasoningTokens(rawUsageJSON, reasoningContent string) (int, bool) {
	if rawUsageJSON == "" {
		if reasoningContent != "" {
			return compaction.EstimateTokens(reasoningContent), true
		}
		return 0, false
	}
	var raw struct {
		CompletionTokensDetails struct {
			// Pointer so absent vs. explicit-zero are distinguishable: nil means
			// the field was not present; &0 means the server said "no reasoning".
			ReasoningTokens *int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		// Some non-OpenAI servers put reasoning_tokens at the top level.
		ReasoningTokens *int `json:"reasoning_tokens,omitempty"`
	}
	if err := json.Unmarshal([]byte(rawUsageJSON), &raw); err != nil {
		return 0, false
	}
	if raw.CompletionTokensDetails.ReasoningTokens != nil && *raw.CompletionTokensDetails.ReasoningTokens > 0 {
		return *raw.CompletionTokensDetails.ReasoningTokens, false
	}
	if raw.ReasoningTokens != nil && *raw.ReasoningTokens > 0 {
		return *raw.ReasoningTokens, false
	}
	if reasoningContent != "" {
		return compaction.EstimateTokens(reasoningContent), true
	}
	return 0, false
}

// extractMessageReasoningContent reads choices[0].message.reasoning_content
// from a full (non-streaming) Chat Completions response JSON. Providers that
// do thinking inline but do not populate completion_tokens_details (e.g. ds4)
// carry the thinking text here. Returns "" when absent or unparseable.
func extractMessageReasoningContent(rawCompletionJSON string) string {
	if rawCompletionJSON == "" {
		return ""
	}
	var raw struct {
		Choices []struct {
			Message struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(rawCompletionJSON), &raw); err != nil || len(raw.Choices) == 0 {
		return ""
	}
	return raw.Choices[0].Message.ReasoningContent
}
