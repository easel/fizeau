package claude

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStreamProvider is a test double for agentcore.StreamingProvider. It
// replays a scripted sequence of StreamDelta turns and records the params it
// was called with so tests can prove the native runner drove the native
// Messages API path (and never os/exec'd `claude --print`).
type fakeStreamProvider struct {
	turns [][]agentcore.StreamDelta // one slice of deltas per ChatStream call

	callCount   int
	gotMessages [][]agentcore.Message
	gotTools    [][]agentcore.ToolDef
	gotOpts     []agentcore.Options
}

// Chat satisfies agentcore.Provider; the native runner only uses ChatStream,
// so this should never be invoked in these tests.
func (f *fakeStreamProvider) Chat(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolDef, _ agentcore.Options) (agentcore.Response, error) {
	panic("fakeStreamProvider.Chat must not be called: native runner streams")
}

func (f *fakeStreamProvider) ChatStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolDef, opts agentcore.Options) (<-chan agentcore.StreamDelta, error) {
	idx := f.callCount
	f.callCount++
	f.gotMessages = append(f.gotMessages, messages)
	f.gotTools = append(f.gotTools, tools)
	f.gotOpts = append(f.gotOpts, opts)

	ch := make(chan agentcore.StreamDelta, 16)
	var deltas []agentcore.StreamDelta
	if idx < len(f.turns) {
		deltas = f.turns[idx]
	}
	go func() {
		defer close(ch)
		for _, d := range deltas {
			select {
			case ch <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

var _ agentcore.StreamingProvider = (*fakeStreamProvider)(nil)

// fakeTool is a minimal agentcore.Tool that records its invocation and returns
// a canned output. The native runner executes tool_use blocks against the
// wired tool set and feeds back tool_result.
type fakeTool struct {
	name      string
	output    string
	called    bool
	gotParams json.RawMessage
}

func (t *fakeTool) Name() string            { return t.name }
func (t *fakeTool) Description() string     { return "fake tool for tests" }
func (t *fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *fakeTool) Parallel() bool          { return false }
func (t *fakeTool) Execute(_ context.Context, params json.RawMessage) (string, error) {
	t.called = true
	t.gotParams = params
	return t.output, nil
}

// eventsByType partitions a drained event slice by EventType.
func eventsByType(evs []harnesses.Event) map[harnesses.EventType][]harnesses.Event {
	out := map[harnesses.EventType][]harnesses.Event{}
	for _, e := range evs {
		out[e.Type] = append(out[e.Type], e)
	}
	return out
}

// TestNativeRunner_ToolUsingTurn proves requirement (a): the native path emits
// text_delta + tool_call + tool_result + final for a tool-using turn, driving
// the streaming provider through an agentic loop.
func TestNativeRunner_ToolUsingTurn(t *testing.T) {
	tool := &fakeTool{name: "search", output: "tool says hello"}

	provider := &fakeStreamProvider{
		turns: [][]agentcore.StreamDelta{
			// Turn 1: a tool_use request (id+name, then args fragments, then stop).
			{
				{Model: "claude-sonnet-4-20250514", Usage: &agentcore.TokenUsage{Input: 100}},
				{ToolCallID: "tu_1", ToolCallName: "search"},
				{ToolCallID: "tu_1", ToolCallArgs: `{"q":`},
				{ToolCallID: "tu_1", ToolCallArgs: `"hi"}`},
				{Usage: &agentcore.TokenUsage{Output: 20}, FinishReason: "tool_use"},
				{Done: true},
			},
			// Turn 2: final text answer (no tool calls -> loop terminates).
			{
				{Content: "Final "},
				{Content: "answer."},
				{Usage: &agentcore.TokenUsage{Input: 130, Output: 8}, FinishReason: "end_turn"},
				{Done: true},
			},
		},
	}

	r := &Runner{
		NativeMode:     true,
		NativeProvider: provider,
		NativeTools:    []agentcore.Tool{tool},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:       "do a thing",
		SystemPrompt: "you are a test",
		Model:        "claude-sonnet-4-20250514",
	})
	require.NoError(t, err)

	evs := drainEvents(t, ctx, out)
	by := eventsByType(evs)

	require.NotEmpty(t, by[harnesses.EventTypeTextDelta], "expected text_delta events")
	require.Len(t, by[harnesses.EventTypeToolCall], 1, "expected one tool_call event")
	require.Len(t, by[harnesses.EventTypeToolResult], 1, "expected one tool_result event")
	require.Len(t, by[harnesses.EventTypeFinal], 1, "expected exactly one final event")

	// tool_call carries the assembled input.
	var tcData harnesses.ToolCallData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeToolCall][0].Data, &tcData))
	assert.Equal(t, "search", tcData.Name)
	assert.Equal(t, "tu_1", tcData.ID)
	assert.JSONEq(t, `{"q":"hi"}`, string(tcData.Input))

	// The tool actually ran and its output came back as tool_result.
	assert.True(t, tool.called, "tool should have been executed")
	assert.JSONEq(t, `{"q":"hi"}`, string(tool.gotParams))
	var trData harnesses.ToolResultData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeToolResult][0].Data, &trData))
	assert.Equal(t, "tu_1", trData.ID)
	assert.Equal(t, "tool says hello", trData.Output)
	assert.Empty(t, trData.Error)

	// Final event: success + final text.
	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeFinal][0].Data, &final))
	assert.Equal(t, "success", final.Status)
	assert.Equal(t, "Final answer.", final.FinalText)

	// The provider was driven twice (tool turn + final turn).
	assert.Equal(t, 2, provider.callCount)
}

// TestNativeRunner_NoSubprocessSpawned proves requirement (b): the native path
// never os/exec's the claude binary. We assert this two ways:
//  1. Info().Type is "native" (not "subprocess"), and the runner runs to
//     completion with NO claude binary on PATH and Binary set to a path that
//     does not exist — a subprocess invocation would fail to start.
//  2. The provider received native Message structs (a real conversation), not
//     CLI argv containing "--print"; we inspect every recorded param.
func TestNativeRunner_NoSubprocessSpawned(t *testing.T) {
	provider := &fakeStreamProvider{
		turns: [][]agentcore.StreamDelta{
			{
				{Content: "hello"},
				{Usage: &agentcore.TokenUsage{Input: 10, Output: 3}, FinishReason: "end_turn"},
				{Done: true},
			},
		},
	}
	r := &Runner{
		NativeMode:     true,
		NativeProvider: provider,
		// A binary path that cannot be exec'd. If the runner ever tried to
		// spawn it, Execute/run would surface a failure final event.
		Binary: "/nonexistent/definitely-not-claude",
	}

	// Native harness must not claim to be a subprocess and must be available
	// without any claude binary.
	info := r.Info()
	assert.Equal(t, "native", info.Type)
	assert.True(t, info.Available)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hi", Model: "claude-sonnet-4-20250514"})
	require.NoError(t, err)
	evs := drainEvents(t, ctx, out)
	by := eventsByType(evs)

	require.Len(t, by[harnesses.EventTypeFinal], 1)
	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeFinal][0].Data, &final))
	require.Equal(t, "success", final.Status, "native run must succeed without spawning claude; error=%q", final.Error)

	// No recorded provider param may resemble a CLI invocation of `claude
	// --print ...`. The native path passes native Message structs; assert none
	// of the message content is a CLI flag blob.
	require.NotEmpty(t, provider.gotMessages)
	for _, msgs := range provider.gotMessages {
		for _, m := range msgs {
			assert.NotContains(t, m.Content, "--print",
				"native path must not pass CLI args; got message content %q", m.Content)
			assert.NotContains(t, m.Content, "stream-json")
		}
	}
}

// TestNativeRunner_MeteredBilling proves requirement (c): native runs are
// metered (actual_cash_spend), not flat subscription. IsSubscription must be
// false and a known-priced model must yield CostUSD > 0 computed from native
// usage.
func TestNativeRunner_MeteredBilling(t *testing.T) {
	r := &Runner{NativeMode: true}
	info := r.Info()
	assert.False(t, info.IsSubscription,
		"native metered runner must report IsSubscription=false (distinct from claude-tui flat subscription)")

	provider := &fakeStreamProvider{
		turns: [][]agentcore.StreamDelta{
			{
				{Content: "answer"},
				// 1,000,000 input + 1,000,000 output tokens against
				// claude-sonnet-4-20250514 ($3/$15 per MTok) = $18.00.
				{Usage: &agentcore.TokenUsage{Input: 1_000_000, Output: 1_000_000}, FinishReason: "end_turn"},
				{Done: true},
			},
		},
	}
	r.NativeProvider = provider

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "x", Model: "claude-sonnet-4-20250514"})
	require.NoError(t, err)
	evs := drainEvents(t, ctx, out)
	by := eventsByType(evs)

	require.Len(t, by[harnesses.EventTypeFinal], 1)
	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeFinal][0].Data, &final))
	assert.Equal(t, "success", final.Status)
	assert.InDelta(t, 18.0, final.CostUSD, 0.0001, "metered cost must be computed from native usage")
	require.NotNil(t, final.FinalCostUSD)
	assert.InDelta(t, 18.0, *final.FinalCostUSD, 0.0001)
	assert.Equal(t, harnesses.CostSourceConfigured, final.FinalCostSource)

	// Usage must be attributed to the native_stream source with the real token
	// counts (proving usage flows through the metered path).
	require.NotNil(t, final.Usage)
	require.NotNil(t, final.Usage.InputTokens)
	require.NotNil(t, final.Usage.OutputTokens)
	assert.Equal(t, 1_000_000, *final.Usage.InputTokens)
	assert.Equal(t, 1_000_000, *final.Usage.OutputTokens)
}

// TestNativeRunner_CacheCost proves that when a native turn's TokenUsage
// includes CacheRead and CacheWrite tokens the final CostUSD is cache-inclusive
// (strictly greater than the input+output-only figure). AC#2 of fizeau-38cb69d4.
func TestNativeRunner_CacheCost(t *testing.T) {
	// claude-sonnet-4-20250514: $3/MTok input, $15/MTok output,
	//                           $0.30/MTok cache-read, $3.75/MTok cache-write
	const (
		inputTokens      = 1_000_000
		outputTokens     = 1_000_000
		cacheReadTokens  = 500_000
		cacheWriteTokens = 200_000
	)
	// input+output only: $3 + $15 = $18
	// cache-read: 0.5 * $0.30 = $0.15
	// cache-write: 0.2 * $3.75 = $0.75
	// total: $18.90
	wantCacheInclusive := 18.90

	provider := &fakeStreamProvider{
		turns: [][]agentcore.StreamDelta{
			{
				{Content: "answer"},
				{
					Usage: &agentcore.TokenUsage{
						Input:      inputTokens,
						Output:     outputTokens,
						CacheRead:  cacheReadTokens,
						CacheWrite: cacheWriteTokens,
					},
					FinishReason: "end_turn",
				},
				{Done: true},
			},
		},
	}
	r := &Runner{NativeMode: true, NativeProvider: provider}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "x", Model: "claude-sonnet-4-20250514"})
	require.NoError(t, err)
	evs := drainEvents(t, ctx, out)
	by := eventsByType(evs)

	require.Len(t, by[harnesses.EventTypeFinal], 1)
	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeFinal][0].Data, &final))
	assert.Equal(t, "success", final.Status)

	// Must exceed input+output-only cost ($18.00).
	assert.Greater(t, final.CostUSD, 18.0,
		"cache-inclusive cost must exceed input+output-only cost")
	assert.InDelta(t, wantCacheInclusive, final.CostUSD, 0.001,
		"expected cache-inclusive cost $%.4f, got $%.4f", wantCacheInclusive, final.CostUSD)
	require.NotNil(t, final.FinalCostUSD)
	assert.InDelta(t, wantCacheInclusive, *final.FinalCostUSD, 0.001)
	assert.Equal(t, harnesses.CostSourceConfigured, final.FinalCostSource)
}

func TestNativeRunnerCostPresence(t *testing.T) {
	type result struct {
		final harnesses.FinalData
		raw   json.RawMessage
	}
	run := func(t *testing.T, turns [][]agentcore.StreamDelta, model string) result {
		t.Helper()
		tool := &fakeTool{name: "search", output: "ok"}
		runner := &Runner{
			NativeMode:     true,
			NativeProvider: &fakeStreamProvider{turns: turns},
			NativeTools:    []agentcore.Tool{tool},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := runner.Execute(ctx, harnesses.ExecuteRequest{Prompt: "cost", Model: model})
		require.NoError(t, err)
		events := drainEvents(t, ctx, out)
		by := eventsByType(events)
		require.Len(t, by[harnesses.EventTypeFinal], 1)
		var final harnesses.FinalData
		require.NoError(t, json.Unmarshal(by[harnesses.EventTypeFinal][0].Data, &final))
		return result{final: final, raw: by[harnesses.EventTypeFinal][0].Data}
	}
	assertUnknown := func(t *testing.T, got result) {
		t.Helper()
		assert.Nil(t, got.final.FinalCostUSD)
		assert.Equal(t, harnesses.CostSourceUnknown, got.final.FinalCostSource)
		assert.Zero(t, got.final.CostUSD)
		assertFinalCostJSON(t, got.raw, false, 0, harnesses.CostSourceUnknown)
	}
	assertConfigured := func(t *testing.T, got result, want float64) {
		t.Helper()
		require.NotNil(t, got.final.FinalCostUSD)
		assert.InDelta(t, want, *got.final.FinalCostUSD, 1e-12)
		assert.Equal(t, harnesses.CostSourceConfigured, got.final.FinalCostSource)
		assert.InDelta(t, want, got.final.CostUSD, 1e-12)
		assertFinalCostJSON(t, got.raw, true, want, harnesses.CostSourceConfigured)
	}

	t.Run("no usage frame", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{{{Content: "answer"}, {Done: true}}}, "claude-sonnet-4-20250514")
		assertUnknown(t, got)
	})

	t.Run("explicit zero usage on known pricing", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{{{Usage: &agentcore.TokenUsage{}}, {Done: true}}}, "claude-sonnet-4-20250514")
		assertConfigured(t, got, 0)
	})

	t.Run("positive usage on known pricing", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{{{Usage: &agentcore.TokenUsage{Input: 1_000_000, Output: 1_000_000}}, {Done: true}}}, "claude-sonnet-4-20250514")
		assertConfigured(t, got, 18)
	})

	t.Run("usage on unknown model", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{{{Model: "claude-unknown", Usage: &agentcore.TokenUsage{}}, {Done: true}}}, "claude-sonnet-4-20250514")
		assertUnknown(t, got)
	})

	t.Run("negative computed cost", func(t *testing.T) {
		assert.Nil(t, nativeCostUSD("claude-sonnet-4-20250514", agentcore.TokenUsage{Input: -1}))
	})

	t.Run("failed turn without usage", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{{{Err: errors.New("provider failed")}}}, "claude-sonnet-4-20250514")
		assert.Equal(t, "failed", got.final.Status)
		assertUnknown(t, got)
	})

	t.Run("unknown then known remains unknown", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{
			{
				{ToolCallID: "tc-1", ToolCallName: "search"},
				{ToolCallID: "tc-1", ToolCallArgs: `{}`},
				{Done: true},
			},
			{{Usage: &agentcore.TokenUsage{}}, {Done: true}},
		}, "claude-sonnet-4-20250514")
		assertUnknown(t, got)
	})

	t.Run("known then unknown remains unknown", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{
			{
				{Usage: &agentcore.TokenUsage{}},
				{ToolCallID: "tc-1", ToolCallName: "search"},
				{ToolCallID: "tc-1", ToolCallArgs: `{}`},
				{Done: true},
			},
			{{Content: "answer"}, {Done: true}},
		}, "claude-sonnet-4-20250514")
		assertUnknown(t, got)
	})

	t.Run("later failed turn poisons known total", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{
			{
				{Usage: &agentcore.TokenUsage{}},
				{ToolCallID: "tc-1", ToolCallName: "search"},
				{ToolCallID: "tc-1", ToolCallArgs: `{}`},
				{Done: true},
			},
			{{Err: errors.New("provider failed")}},
		}, "claude-sonnet-4-20250514")
		assert.Equal(t, "failed", got.final.Status)
		assertUnknown(t, got)
	})

	t.Run("all known turns sum", func(t *testing.T) {
		got := run(t, [][]agentcore.StreamDelta{
			{
				{Usage: &agentcore.TokenUsage{Input: 1_000_000}},
				{ToolCallID: "tc-1", ToolCallName: "search"},
				{ToolCallID: "tc-1", ToolCallArgs: `{}`},
				{Done: true},
			},
			{{Usage: &agentcore.TokenUsage{Output: 1_000_000}}, {Done: true}},
		}, "claude-sonnet-4-20250514")
		assertConfigured(t, got, 18)
	})
}

// TestNativeRunner_HealthCheckIgnoresBinary proves a native-mode Runner reports
// healthy WITHOUT a claude CLI on disk: native mode reaches the metered HTTP API
// and never os/exec's the binary, so HealthCheck must not gate on its presence.
// (Regression guard: HealthCheck previously LookPath'd "claude" unconditionally,
// so a native Runner on a box without the CLI wrongly reported unavailable.)
func TestNativeRunner_HealthCheckIgnoresBinary(t *testing.T) {
	// Point Binary at a path that does not exist; subprocess mode would fail.
	r := &Runner{NativeMode: true, Binary: "/nonexistent/claude-binary-xyz"}
	if err := r.HealthCheck(context.Background()); err != nil {
		t.Fatalf("native-mode HealthCheck must succeed without a claude binary, got: %v", err)
	}

	// Sanity: the SAME nonexistent binary in subprocess mode DOES fail health,
	// confirming the test isn't vacuously passing.
	sub := &Runner{NativeMode: false, Binary: "/nonexistent/claude-binary-xyz"}
	if err := sub.HealthCheck(context.Background()); err == nil {
		t.Fatal("subprocess-mode HealthCheck with a missing binary must fail (control)")
	}
}

// TestNativeRunner_UnknownToolErrors verifies a tool_use for a tool the runner
// does not have wired produces an error tool_result rather than aborting.
func TestNativeRunner_UnknownToolErrors(t *testing.T) {
	provider := &fakeStreamProvider{
		turns: [][]agentcore.StreamDelta{
			{
				{ToolCallID: "tu_x", ToolCallName: "nope"},
				{ToolCallID: "tu_x", ToolCallArgs: `{}`},
				{FinishReason: "tool_use"},
				{Done: true},
			},
			{
				{Content: "recovered"},
				{FinishReason: "end_turn"},
				{Done: true},
			},
		},
	}
	r := &Runner{NativeMode: true, NativeProvider: provider}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "x"})
	require.NoError(t, err)
	evs := drainEvents(t, ctx, out)
	by := eventsByType(evs)

	require.Len(t, by[harnesses.EventTypeToolResult], 1)
	var tr harnesses.ToolResultData
	require.NoError(t, json.Unmarshal(by[harnesses.EventTypeToolResult][0].Data, &tr))
	assert.Contains(t, tr.Error, "unknown tool")
}

// TestNativeRunner_AutoToolsPermissionFilter proves that when no NativeTools
// list is wired the runner auto-builds the builtin agent tool set
// (tool.BuiltinToolsForPreset) and applies the permission filter: a mutating
// tool (write) is available under "unrestricted" but stripped under the default
// "safe" mode (surfacing as an "unknown tool" error result). This exercises the
// reused builtin tool set + permission-filter path used in production native
// dispatch.
func TestNativeRunner_AutoToolsPermissionFilter(t *testing.T) {
	makeProvider := func() *fakeStreamProvider {
		return &fakeStreamProvider{
			turns: [][]agentcore.StreamDelta{
				{
					{ToolCallID: "tu_w", ToolCallName: "write"},
					{ToolCallID: "tu_w", ToolCallArgs: `{"path":"out.txt","content":"hi"}`},
					{FinishReason: "tool_use"},
					{Done: true},
				},
				{
					{Content: "done"},
					{FinishReason: "end_turn"},
					{Done: true},
				},
			},
		}
	}

	run := func(t *testing.T, permission string) harnesses.ToolResultData {
		t.Helper()
		// NativeTools intentionally left nil so the runner auto-builds + filters.
		r := &Runner{NativeMode: true, NativeProvider: makeProvider()}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := r.Execute(ctx, harnesses.ExecuteRequest{
			Prompt:      "write a file",
			WorkDir:     t.TempDir(),
			Permissions: permission,
		})
		require.NoError(t, err)
		evs := drainEvents(t, ctx, out)
		by := eventsByType(evs)
		require.Len(t, by[harnesses.EventTypeToolResult], 1)
		var tr harnesses.ToolResultData
		require.NoError(t, json.Unmarshal(by[harnesses.EventTypeToolResult][0].Data, &tr))
		return tr
	}

	// Safe (default): write is filtered out -> unknown tool error.
	safe := run(t, "safe")
	assert.Contains(t, safe.Error, "unknown tool",
		"write must be filtered out under safe permissions")

	// Unrestricted: write survives the filter and executes against the WorkDir.
	unrestricted := run(t, "unrestricted")
	assert.Empty(t, unrestricted.Error,
		"write must be available under unrestricted permissions; got error=%q", unrestricted.Error)
}
