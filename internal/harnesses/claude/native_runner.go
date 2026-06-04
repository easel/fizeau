package claude

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/tool"
)

// defaultNativeMaxIterations bounds the native agentic loop so a model that
// keeps requesting tools cannot run unbounded.
const defaultNativeMaxIterations = 50

// nativeProviderFactory constructs the streaming provider used by the native
// path. It is a field so tests can inject a fake provider without building the
// real Anthropic client. The default builds the metered anthropic provider.
type nativeProviderFactory func(cfg NativeProviderConfig) NativeStreamingProvider

// runNative drives the native Anthropic Messages API path: it streams turns
// through the provider, executes any requested tools against r.NativeTools,
// and emits the SAME harness events the subprocess path produced
// (text_delta / tool_call / tool_result / final-via-aggregate).
//
// It returns a streamAggregate (so run() can build the final event uniformly
// with the subprocess paths) plus exit metadata.
func (r *Runner) runNative(ctx context.Context, req harnesses.ExecuteRequest, out chan<- harnesses.Event, seq *int64) (*streamAggregate, int, string, error, string) {
	provider := r.NativeProvider
	if provider == nil {
		factory := r.nativeFactory
		if factory == nil {
			factory = newAnthropicNativeProvider
		}
		provider = factory(NativeProviderConfig{
			APIKey:  r.NativeAPIKey,
			BaseURL: r.NativeBaseURL,
			Model:   req.Model,
		})
	}
	if provider == nil {
		return nil, -1, "", errors.New("claude native: no provider configured"), "failed"
	}

	modelResolution := harnesses.ResolveRunnerModelWithCache(r.DiscoveryCache, "claude", modelcatalog.SurfaceClaudeCode, req.Model, fallbackDefaultModel)
	model := modelResolution.ResolvedModel
	if model == "" {
		model = fallbackDefaultModel
	}

	emit := func(t harnesses.EventType, data any) error {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		ev := harnesses.Event{
			Type:     t,
			Sequence: *seq,
			Time:     time.Now().UTC(),
			Metadata: req.Metadata,
			Data:     raw,
		}
		*seq++
		select {
		case out <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Resolve the native tool set. An explicitly-wired NativeTools list is
	// honored verbatim (the construction point already chose it). When no list
	// is wired, build the same builtin agent tool set the service native loop
	// uses (tool.BuiltinToolsForPreset) and apply the permission filter so the
	// native claude path is agentic rather than text-only. Permission filtering
	// mirrors serviceimpl.filterNativeToolsForPermission (which claude cannot
	// import without an import cycle: serviceimpl -> harnesses/claude).
	tools := r.NativeTools
	if tools == nil {
		tools = tool.BuiltinToolsForPreset(req.WorkDir, "", tool.BashOutputFilterConfig{})
		tools = filterNativeToolsForPermission(tools, req.Permissions)
	}

	// Build the tool defs + lookup map from the resolved tools.
	toolDefs := make([]agentcore.ToolDef, 0, len(tools))
	toolMap := make(map[string]agentcore.Tool, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, agentcore.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
		toolMap[t.Name()] = t
	}

	// Seed the conversation.
	var messages []agentcore.Message
	if req.SystemPrompt != "" {
		messages = append(messages, agentcore.Message{Role: agentcore.RoleSystem, Content: req.SystemPrompt})
	}
	messages = append(messages, agentcore.Message{Role: agentcore.RoleUser, Content: req.Prompt})

	opts := agentcore.Options{Model: model}
	if req.Temperature != 0 {
		t := float64(req.Temperature)
		opts.Temperature = &t
	}
	// Map the public reasoning effort scalar onto the native Options so the
	// metered Messages API path actually requests model-side reasoning. Empty
	// / "off" leaves Reasoning unset (no thinking budget requested).
	if req.Reasoning != "" {
		opts.Reasoning = agentcore.Reasoning(req.Reasoning)
	}

	agg := &streamAggregate{Model: model}

	maxIter := r.NativeMaxIterations
	if maxIter <= 0 {
		maxIter = defaultNativeMaxIterations
	}

	for iter := 0; iter < maxIter; iter++ {
		if err := ctx.Err(); err != nil {
			return agg, -1, "", err, classifyCtx(err)
		}

		deltas, err := provider.ChatStream(ctx, messages, toolDefs, opts)
		if err != nil {
			return agg, -1, "", err, "failed"
		}

		turn, err := consumeNativeTurn(ctx, deltas, emit)
		if err != nil {
			return agg, -1, "", err, classifyCtx(err)
		}

		agg.TurnCount++
		if turn.model != "" {
			agg.Model = turn.model
		}
		// Accumulate metered usage + cost across turns.
		agg.UsageSources = append(agg.UsageSources, nativeUsageCandidate(turn.usage))
		agg.CostUSD += nativeCostUSD(agg.Model, turn.usage)

		if turn.text != "" {
			agg.FinalText = turn.text
		}

		// No tool calls -> the model produced its final answer.
		if len(turn.toolCalls) == 0 {
			return agg, 0, "", nil, "success"
		}

		// Record the assistant turn (text + tool calls) then execute tools.
		messages = append(messages, agentcore.Message{
			Role:      agentcore.RoleAssistant,
			Content:   turn.text,
			ToolCalls: turn.toolCalls,
		})
		agg.ToolCalls += len(turn.toolCalls)

		for _, tc := range turn.toolCalls {
			output, toolErr := executeNativeTool(ctx, toolMap, tc)
			data := harnesses.ToolResultData{ID: tc.ID, Output: output}
			if toolErr != nil {
				data.Error = toolErr.Error()
				data.Output = ""
			}
			if err := emit(harnesses.EventTypeToolResult, data); err != nil {
				return agg, -1, "", err, classifyCtx(err)
			}
			content := output
			if toolErr != nil {
				content = "error: " + toolErr.Error()
			}
			messages = append(messages, agentcore.Message{
				Role:       agentcore.RoleTool,
				Content:    content,
				ToolCallID: tc.ID,
			})
		}
	}

	// Hit the iteration cap with tool calls still pending.
	return agg, 0, "", nil, "iteration_limit"
}

// nativeReadOnlyTools is the read-only tool allowlist applied under the "safe"
// permission mode. It mirrors serviceimpl.nativeReadOnlyTools — kept as a local
// copy because internal/harnesses/claude cannot import internal/serviceimpl
// (serviceimpl already imports this package, so the reverse edge would cycle).
var nativeReadOnlyTools = map[string]bool{
	"read":       true,
	"read_file":  true,
	"grep":       true,
	"ls":         true,
	"find":       true,
	"cat":        true,
	"head":       true,
	"tail":       true,
	"stat":       true,
	"web_fetch":  true,
	"web_search": true,
}

// filterNativeToolsForPermission narrows the native tool set per permission
// mode, mirroring serviceimpl.filterNativeToolsForPermission. "unrestricted"
// keeps every tool; any other mode (including "" / "safe") keeps only the
// read-only allowlist so the native claude path cannot mutate state under safe
// permissions.
func filterNativeToolsForPermission(tools []agentcore.Tool, permission string) []agentcore.Tool {
	if permission == "unrestricted" {
		return tools
	}
	filtered := make([]agentcore.Tool, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		if nativeReadOnlyTools[t.Name()] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// executeNativeTool runs one tool call against the wired tool set. An unknown
// tool is surfaced as an error result (mirroring the subprocess harness, which
// also returns is_error tool_results for tools it can't run) rather than
// aborting the whole turn.
func executeNativeTool(ctx context.Context, tools map[string]agentcore.Tool, tc agentcore.ToolCall) (string, error) {
	tool, ok := tools[tc.Name]
	if !ok {
		return "", errors.New("unknown tool: " + tc.Name)
	}
	return tool.Execute(ctx, tc.Arguments)
}

// nativeUsageCandidate wraps native streamed usage as a native_stream usage
// candidate so ResolveFinalUsage attributes it with the correct (highest)
// precedence source.
func nativeUsageCandidate(u agentcore.TokenUsage) harnesses.UsageCandidate {
	return harnesses.UsageCandidate{
		Source: harnesses.UsageSourceNativeStream,
		Fresh:  harnesses.BoolPtr(true),
		Counts: harnesses.UsageTokenCounts{
			InputTokens:      harnesses.IntPtr(u.Input),
			OutputTokens:     harnesses.IntPtr(u.Output),
			CacheReadTokens:  optIntPtr(u.CacheRead),
			CacheWriteTokens: optIntPtr(u.CacheWrite),
			TotalTokens:      harnesses.IntPtr(u.Total),
		},
	}
}

func optIntPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return harnesses.IntPtr(v)
}

// nativeCostUSD computes metered (actual_cash_spend) cost from native usage
// using fizeau's pricing table. Unknown models yield 0 (no cost asserted)
// rather than a negative sentinel.
func nativeCostUSD(model string, u agentcore.TokenUsage) float64 {
	cost := agentcore.DefaultPricing.EstimateCost(model, u.Input, u.Output)
	if cost < 0 {
		return 0
	}
	return cost
}

func classifyCtx(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out"
	default:
		return "failed"
	}
}
