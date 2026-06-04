package serviceimpl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaudeStreamProvider is a serviceimpl-local test double for
// agentcore.StreamingProvider. It replays scripted turns and records that the
// native claude path drove the Messages API (never os/exec'd claude --print).
type fakeClaudeStreamProvider struct {
	turns       [][]agentcore.StreamDelta
	callCount   int
	gotMessages [][]agentcore.Message
	gotOpts     []agentcore.Options
}

func (f *fakeClaudeStreamProvider) Chat(context.Context, []agentcore.Message, []agentcore.ToolDef, agentcore.Options) (agentcore.Response, error) {
	panic("Chat must not be called: native claude path streams")
}

func (f *fakeClaudeStreamProvider) ChatStream(ctx context.Context, messages []agentcore.Message, _ []agentcore.ToolDef, opts agentcore.Options) (<-chan agentcore.StreamDelta, error) {
	idx := f.callCount
	f.callCount++
	f.gotMessages = append(f.gotMessages, messages)
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

var _ agentcore.StreamingProvider = (*fakeClaudeStreamProvider)(nil)

// fakeUnrestrictedTool is a mutating (non read-only) agentcore.Tool, so it only
// survives the native permission filter under "unrestricted".
type fakeUnrestrictedTool struct {
	called bool
}

func (t *fakeUnrestrictedTool) Name() string            { return "write" }
func (t *fakeUnrestrictedTool) Description() string     { return "fake mutating tool" }
func (t *fakeUnrestrictedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *fakeUnrestrictedTool) Parallel() bool          { return false }
func (t *fakeUnrestrictedTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.called = true
	return "wrote it", nil
}

// TestClaudeTransportSelection proves requirement 3's default-selection half:
// the FIZEAU_CLAUDE_TRANSPORT knob (default = subprocess) controls whether the
// production claude Runner is built native. Unset / "subprocess" / garbage must
// keep NativeMode=false (byte-for-byte unchanged: still spawns claude --print);
// only an explicit "native" flips it on.
func TestClaudeTransportSelection(t *testing.T) {
	cases := []struct {
		value      string
		set        bool
		wantNative bool
	}{
		{set: false, wantNative: false}, // unset -> default subprocess
		{value: "", set: true, wantNative: false},
		{value: "subprocess", set: true, wantNative: false},
		{value: "garbage", set: true, wantNative: false},
		{value: "native", set: true, wantNative: true},
		{value: "NATIVE", set: true, wantNative: true}, // case-insensitive
		{value: " native ", set: true, wantNative: true},
	}
	for _, tc := range cases {
		if tc.set {
			t.Setenv(claudeTransportEnv, tc.value)
		} else {
			t.Setenv(claudeTransportEnv, "")
		}
		runner := newClaudeRunner()
		assert.Equal(t, tc.wantNative, runner.NativeMode,
			"FIZEAU_CLAUDE_TRANSPORT=%q (set=%v)", tc.value, tc.set)
	}
}

// TestRunSubprocess_ClaudeNativeTransport_EndToEnd is the key proof: with the
// transport knob = native, a claude Execute driven through the SAME production
// execute infrastructure (RunSubprocess -> runner.Execute -> native loop)
// emits text_delta + tool_call + tool_result + final, records metered cost
// (IsSubscription=false / actual_cash_spend), and spawns NO claude --print
// subprocess.
func TestRunSubprocess_ClaudeNativeTransport_EndToEnd(t *testing.T) {
	t.Setenv(claudeTransportEnv, "native")
	require.True(t, claudeNativeTransportSelected(), "knob must select native")

	tool := &fakeUnrestrictedTool{}
	provider := &fakeClaudeStreamProvider{
		turns: [][]agentcore.StreamDelta{
			// Turn 1: a tool_use request.
			{
				{Model: "claude-sonnet-4-20250514", Usage: &agentcore.TokenUsage{Input: 100}},
				{ToolCallID: "tu_1", ToolCallName: "write"},
				{ToolCallID: "tu_1", ToolCallArgs: `{"path":"x"}`},
				{FinishReason: "tool_use"},
				{Done: true},
			},
			// Turn 2: final text answer; 1M in + 1M out against sonnet-4
			// ($3/$15 per MTok) = $18.00 metered.
			{
				{Content: "done "},
				{Content: "answer."},
				{Usage: &agentcore.TokenUsage{Input: 1_000_000, Output: 1_000_000}, FinishReason: "end_turn"},
				{Done: true},
			},
		},
	}

	// Build the runner the way newClaudeRunner does (native, because the knob is
	// set) and inject the fake streaming provider + an explicit tool set. The
	// Binary points at a nonexistent path: a subprocess invocation would fail,
	// so a "success" final proves no claude --print was ever spawned.
	runner := newClaudeRunner()
	require.True(t, runner.NativeMode, "knob=native must build a native Runner")
	runner.NativeProvider = provider
	runner.NativeTools = []agentcore.Tool{tool}
	runner.Binary = "/nonexistent/definitely-not-claude"

	// Native harness reports metered billing, not flat subscription.
	assert.False(t, runner.Info().IsSubscription,
		"native claude transport must be metered (IsSubscription=false)")

	var events []harnesses.Event
	cb := SubprocessCallbacks{
		EmitEvent: func(ev harnesses.Event) bool {
			events = append(events, ev)
			return true
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	RunSubprocess(ctx, SubprocessRequest{
		Prompt:       "do a thing",
		SystemPrompt: "you are a test",
		Permissions:  "unrestricted",
		Reasoning:    "high",
		Decision: ExecuteRunnerDecision{
			Harness: "claude",
			Model:   "claude-sonnet-4-20250514",
		},
		Started: time.Now(),
	}, runner, cb)

	by := map[harnesses.EventType]int{}
	var finalEvents []harnesses.Event
	for _, ev := range events {
		by[ev.Type]++
		if ev.Type == harnesses.EventTypeFinal {
			finalEvents = append(finalEvents, ev)
		}
	}
	assert.GreaterOrEqual(t, by[harnesses.EventTypeTextDelta], 1, "expected text_delta events")
	assert.Equal(t, 1, by[harnesses.EventTypeToolCall], "expected one tool_call")
	assert.Equal(t, 1, by[harnesses.EventTypeToolResult], "expected one tool_result")
	require.Len(t, finalEvents, 1, "expected exactly one final event")

	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(finalEvents[0].Data, &final))

	assert.True(t, tool.called, "native loop must execute the wired tool")
	require.Equal(t, "success", final.Status, "native run must succeed without spawning claude; error=%q", final.Error)
	assert.Equal(t, "done answer.", final.FinalText)
	// 1M input + 1M output against sonnet-4 ($3/$15 per MTok) ≈ $18.00, plus the
	// small turn-1 priming input. Assert metered cost in the expected band.
	assert.InDelta(t, 18.0, final.CostUSD, 0.01, "metered actual_cash_spend cost from native usage")
	assert.Greater(t, final.CostUSD, 0.0, "metered cost must be > 0 (actual_cash_spend)")
	require.NotNil(t, final.Usage)
	require.NotNil(t, final.Usage.InputTokens)
	assert.GreaterOrEqual(t, *final.Usage.InputTokens, 1_000_000, "native usage must flow to metered final")

	// The provider was driven natively (two streamed turns) and reasoning was
	// mapped onto Options.
	require.Equal(t, 2, provider.callCount, "native path must stream both turns")
	require.NotEmpty(t, provider.gotOpts)
	assert.Equal(t, agentcore.Reasoning("high"), provider.gotOpts[0].Reasoning,
		"req.Reasoning must map onto native Options")
	// No message content may resemble a CLI invocation of claude --print.
	require.NotEmpty(t, provider.gotMessages)
	for _, msgs := range provider.gotMessages {
		for _, m := range msgs {
			assert.NotContains(t, m.Content, "--print")
			assert.NotContains(t, m.Content, "stream-json")
		}
	}
}

// Compile-time guard: the fake tool satisfies agentcore.Tool.
var _ agentcore.Tool = (*fakeUnrestrictedTool)(nil)
