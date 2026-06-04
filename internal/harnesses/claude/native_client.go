package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	provregistry "github.com/easel/fizeau/internal/provider/registry"
)

// NativeStreamingProvider is the minimal provider surface the native claude
// runner drives. It is satisfied by *anthropic.Provider (the real, metered
// Anthropic Messages API client) and by fakes in tests. It is an alias of the
// core streaming-provider interface so the runner reuses fizeau's existing
// native HTTP provider client instead of hand-rolling a new HTTP stack.
type NativeStreamingProvider = agentcore.StreamingProvider

// NativeProviderConfig holds the inputs needed to construct the real
// metered Anthropic provider for the native claude path.
type NativeProviderConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// newAnthropicNativeProvider builds the real metered Anthropic Messages API
// provider via the provider registry's "anthropic" descriptor. That
// descriptor's factory wraps the anthropic-sdk-go streaming client — no
// bespoke HTTP stack is introduced. The registry lookup (rather than a direct
// import of internal/provider/anthropic) avoids an import cycle:
// anthropic -> runtimesignals -> ... -> harnesses/builtin -> harnesses/claude.
//
// This is metered/actual-cash-spend usage (distinct from the claude-tui flat
// subscription surface): every token is billed against the API key.
//
// Returns nil if the anthropic provider is not registered (the importing
// binary did not link it) or does not implement streaming.
func newAnthropicNativeProvider(cfg NativeProviderConfig) NativeStreamingProvider {
	desc, ok := provregistry.Lookup("anthropic")
	if !ok {
		return nil
	}
	p := desc.Factory(provregistry.Inputs{
		ProviderName: "anthropic",
		APIKey:       cfg.APIKey,
		BaseURL:      cfg.BaseURL,
		Model:        cfg.Model,
	})
	sp, ok := p.(NativeStreamingProvider)
	if !ok {
		return nil
	}
	return sp
}

// nativeToolCall accumulates a single streamed tool_use block: the provider
// emits the id/name on content_block_start and the JSON arguments in
// fragments across content_block_delta events. We buffer the fragments and
// only surface the call once the block completes.
type nativeToolCall struct {
	id    string
	name  string
	args  strings.Builder
	dirty bool
}

// nativeStreamResult captures the aggregate state produced by consuming one
// provider turn: the model's text, any completed tool calls, usage, and the
// resolved model id.
type nativeStreamResult struct {
	text      string
	toolCalls []agentcore.ToolCall
	usage     agentcore.TokenUsage
	model     string
	finish    string
}

// consumeNativeTurn streams a single provider turn, emitting text_delta and
// tool_call harness events as content arrives, and returns the aggregated
// turn result so the caller can execute tools and continue the loop.
//
// emit is invoked for each harness Event the turn produces (text_delta per
// text fragment, tool_call once a tool_use block is fully assembled). It must
// honor ctx and return a non-nil error to abort the stream.
func consumeNativeTurn(
	ctx context.Context,
	deltas <-chan agentcore.StreamDelta,
	emit func(t harnesses.EventType, data any) error,
) (nativeStreamResult, error) {
	var res nativeStreamResult
	// tool_use blocks stream in order; the Anthropic stream sends exactly one
	// content_block at a time, so a single in-flight accumulator suffices.
	var cur *nativeToolCall

	flushTool := func() error {
		if cur == nil {
			return nil
		}
		raw := json.RawMessage(strings.TrimSpace(cur.args.String()))
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		tc := agentcore.ToolCall{ID: cur.id, Name: cur.name, Arguments: raw}
		res.toolCalls = append(res.toolCalls, tc)
		err := emit(harnesses.EventTypeToolCall, harnesses.ToolCallData{
			ID:    cur.id,
			Name:  cur.name,
			Input: raw,
		})
		cur = nil
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case d, ok := <-deltas:
			if !ok {
				if err := flushTool(); err != nil {
					return res, err
				}
				return res, nil
			}
			if d.Err != nil {
				return res, fmt.Errorf("claude native stream: %w", d.Err)
			}
			if d.Model != "" {
				res.model = d.Model
			}
			if d.Usage != nil {
				mergeUsage(&res.usage, *d.Usage)
			}
			if d.FinishReason != "" {
				res.finish = d.FinishReason
			}
			// A new tool call starts when ToolCallName is set; flush any prior
			// in-flight tool block first.
			if d.ToolCallName != "" {
				if err := flushTool(); err != nil {
					return res, err
				}
				cur = &nativeToolCall{id: d.ToolCallID, name: d.ToolCallName}
				continue
			}
			// Tool argument fragment.
			if d.ToolCallArgs != "" {
				if cur == nil {
					cur = &nativeToolCall{id: d.ToolCallID}
				}
				cur.args.WriteString(d.ToolCallArgs)
				cur.dirty = true
				continue
			}
			// Plain text fragment.
			if d.Content != "" {
				// A text delta closes any open tool block (Anthropic emits
				// content_block_stop, but defensively flush on text too).
				if cur != nil && cur.dirty {
					if err := flushTool(); err != nil {
						return res, err
					}
				}
				res.text += d.Content
				if err := emit(harnesses.EventTypeTextDelta, harnesses.TextDeltaData{Text: d.Content}); err != nil {
					return res, err
				}
			}
			if d.Done {
				if err := flushTool(); err != nil {
					return res, err
				}
				return res, nil
			}
		}
	}
}

// mergeUsage accumulates incremental usage deltas. The Anthropic stream emits
// input tokens on message_start and output/cache tokens on message_delta, so
// we take the max of each dimension to avoid double-counting partial reports.
func mergeUsage(dst *agentcore.TokenUsage, src agentcore.TokenUsage) {
	if src.Input > dst.Input {
		dst.Input = src.Input
	}
	if src.Output > dst.Output {
		dst.Output = src.Output
	}
	if src.CacheRead > dst.CacheRead {
		dst.CacheRead = src.CacheRead
	}
	if src.CacheWrite > dst.CacheWrite {
		dst.CacheWrite = src.CacheWrite
	}
	dst.Total = dst.Input + dst.Output
}
