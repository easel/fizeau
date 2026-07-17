package serviceimpl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAnthropicAPIKeyEnv  = "ANTHROPIC_API_KEY"
	testAnthropicBaseURLEnv = "ANTHROPIC_BASE_URL"
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
			t.Setenv("FIZEAU_CLAUDE_TRANSPORT", tc.value)
		} else {
			t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "")
		}
		if tc.wantNative {
			// Provide a fake key so native cases don't error on missing credential.
			t.Setenv(testAnthropicAPIKeyEnv, "sk-ant-test-selection")
		} else {
			t.Setenv(testAnthropicAPIKeyEnv, "")
		}
		runner, err := newClaudeRouteRunner()
		require.NoError(t, err, "FIZEAU_CLAUDE_TRANSPORT=%q (set=%v)", tc.value, tc.set)
		assert.Equal(t, tc.wantNative, runner.NativeMode,
			"FIZEAU_CLAUDE_TRANSPORT=%q (set=%v)", tc.value, tc.set)
	}
}

// TestClaudeNativeCredentialWiring covers AC1 and AC2:
//   - AC1: native transport + API key set → Runner has NativeMode=true and NativeAPIKey populated.
//   - AC2: native transport + no API key → clear early error naming ANTHROPIC_API_KEY.
func TestClaudeNativeCredentialWiring(t *testing.T) {
	t.Run("key set wires NativeAPIKey", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
		t.Setenv(testAnthropicAPIKeyEnv, "sk-ant-wiring-test")
		t.Setenv(testAnthropicBaseURLEnv, "")

		runner, err := newClaudeRouteRunner()
		require.NoError(t, err)
		require.NotNil(t, runner)
		assert.True(t, runner.NativeMode, "native transport must set NativeMode=true")
		assert.Equal(t, "sk-ant-wiring-test", runner.NativeAPIKey, "NativeAPIKey must be populated from ANTHROPIC_API_KEY")
		assert.Equal(t, "", runner.NativeBaseURL, "NativeBaseURL must be empty when ANTHROPIC_BASE_URL is unset")
	})

	t.Run("base URL wired when set", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
		t.Setenv(testAnthropicAPIKeyEnv, "sk-ant-wiring-test")
		t.Setenv(testAnthropicBaseURLEnv, "https://custom.example.com")

		runner, err := newClaudeRouteRunner()
		require.NoError(t, err)
		require.NotNil(t, runner)
		assert.Equal(t, "https://custom.example.com", runner.NativeBaseURL, "NativeBaseURL must be populated from ANTHROPIC_BASE_URL")
	})

	t.Run("no key produces early error naming ANTHROPIC_API_KEY", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
		t.Setenv(testAnthropicAPIKeyEnv, "")

		runner, err := newClaudeRouteRunner()
		require.Error(t, err, "native without API key must fail fast")
		assert.Nil(t, runner)
		assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY",
			"error must name the missing key so operators know what to set")
		assert.Contains(t, err.Error(), "FIZEAU_CLAUDE_TRANSPORT=native",
			"error must mention the transport knob to give context")
	})
}

// TestRunSubprocess_ClaudeNativeTransport_EndToEnd is the key proof: with the
// transport knob = native, a claude Execute driven through the SAME production
// execute infrastructure (RunSubprocess -> runner.Execute -> native loop)
// emits text_delta + tool_call + tool_result + final, records metered cost
// (IsSubscription=false / actual_cash_spend), and spawns NO claude --print
// subprocess.
func TestRunSubprocess_ClaudeNativeTransport_EndToEnd(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
	t.Setenv(testAnthropicAPIKeyEnv, "sk-ant-e2e-test")

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
	runner, err := newClaudeRouteRunner()
	require.NoError(t, err, "newClaudeRunner must not error when ANTHROPIC_API_KEY is set")
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

// TestClaudeDefaultTransportSubprocessEndToEnd is the service-level guard that the
// default (FIZEAU_CLAUDE_TRANSPORT unset) routes the claude harness through the
// subprocess path, not the native path. It drives DispatchExecuteRun — the
// production dispatch entry point — and asserts that the runner received by
// RunSubprocess is a *claudeharness.Runner with NativeMode=false.
//
// This test will FAIL (guard) if the default transport is later flipped to native,
// ensuring "default behavior unchanged" is provably true at the dispatch level.
func TestClaudeDefaultTransportSubprocessEndToEnd(t *testing.T) {
	// Clear both knobs so we simulate "knob unset" — the pure default state.
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "")
	t.Setenv(testAnthropicAPIKeyEnv, "")

	var capturedRunner harnesses.Harness
	subprocessCalled := false

	cb := ExecuteDispatchCallbacks{
		RunSubprocess: func(_ context.Context, runner harnesses.Harness) {
			subprocessCalled = true
			capturedRunner = runner
		},
	}

	DispatchExecuteRun(context.Background(), ExecuteDispatchRequest{
		Decision: ExecuteRunnerDecision{Harness: "claude"},
		RouteRunner: mustRouteRunnerBinding(t,
			ExecuteRunnerDecision{Harness: "claude"}, builtin.New("claude")),
		Started: time.Now(),
	}, cb)

	require.True(t, subprocessCalled,
		"with FIZEAU_CLAUDE_TRANSPORT unset, DispatchExecuteRun must call RunSubprocess (not native)")
	require.NotNil(t, capturedRunner, "RunSubprocess must receive a non-nil runner")

	info := capturedRunner.Info()
	assert.Equal(t, "claude", info.Name)
	assert.Equal(t, "subprocess", info.Type,
		"default transport (knob unset) must dispatch the subprocess claude harness — if this fails the default was flipped to native")
	assert.True(t, info.IsSubscription,
		"default transport (knob unset) must preserve subscription billing semantics")
}

// Compile-time guard: the fake tool satisfies agentcore.Tool.
var _ agentcore.Tool = (*fakeUnrestrictedTool)(nil)

func newClaudeRouteRunner() (*claudeharness.Runner, error) {
	runner, err := builtin.NewRouteRunner(harnesses.RouteRunnerKey{Harness: "claude"}, builtin.New("claude"))
	if err != nil {
		return nil, err
	}
	return runner.(*claudeharness.Runner), nil
}
