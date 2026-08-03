package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainEvents reads from out until the channel closes or ctx fires. It
// returns the recorded sequence so tests can assert on event types/data.
func drainEvents(t *testing.T, ctx context.Context, out <-chan harnesses.Event) []harnesses.Event {
	t.Helper()
	var got []harnesses.Event
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for events; collected %d so far", len(got))
		}
	}
}

// runParser feeds input through parseClaudeStream and returns the emitted
// events plus the aggregate. It runs the parser in a goroutine so the
// channel writes don't block on a sync receiver.
func runParser(t *testing.T, input string) ([]harnesses.Event, *streamAggregate) {
	t.Helper()
	out := make(chan harnesses.Event, 64)
	var seq int64
	type result struct {
		agg *streamAggregate
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		agg, err := parseClaudeStream(context.Background(), strings.NewReader(input), out, map[string]string{"bead_id": "ddx-12345678"}, &seq)
		close(out)
		resCh <- result{agg: agg, err: err}
	}()

	var events []harnesses.Event
	for ev := range out {
		events = append(events, ev)
	}
	r := <-resCh
	require.NoError(t, r.err)
	return events, r.agg
}

// TestParseClaudeStream_BehavioralParity feeds the same fixtures the DDx
// claude_stream_test.go uses through the agent-side parser and asserts
// that the emitted Event sequence matches the semantic shape callers
// expect per CONTRACT-003 §"Event JSON shapes".
func TestParseClaudeStream_BehavioralParity(t *testing.T) {
	cases := []struct {
		name             string
		input            string
		wantEventTypes   []harnesses.EventType
		wantToolCalls    int
		wantTurnCount    int
		wantInputTokens  int
		wantOutputTokens int
		wantCostUSD      float64
		wantFinalText    string
		wantSessionID    string
		wantModel        string
	}{
		{
			name: "full stream with tool use, tool result, and final result",
			input: strings.Join([]string{
				`{"type":"system","subtype":"init","session_id":"sess-abc","model":"claude-sonnet-4-6","tools":["Bash","Read"]}`,
				`{"type":"assistant","message":{"id":"m-1","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Starting"},{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":120,"output_tokens":42}}}`,
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":"README.md\nfoo.go"}]}}`,
				`{"type":"assistant","message":{"id":"m-2","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":260,"output_tokens":88}}}`,
				`{"type":"result","subtype":"success","is_error":false,"duration_ms":1200,"result":"All done.","usage":{"input_tokens":260,"output_tokens":88},"total_cost_usd":0.0123,"session_id":"sess-abc"}`,
			}, "\n"),
			// One text + one tool_call from m-1, one tool_result from user,
			// one text from m-2. result event emits no parser events (final
			// is built by the runner from the aggregate).
			wantEventTypes: []harnesses.EventType{
				harnesses.EventTypeTextDelta,
				harnesses.EventTypeToolCall,
				harnesses.EventTypeToolResult,
				harnesses.EventTypeTextDelta,
			},
			wantToolCalls:    1,
			wantTurnCount:    2,
			wantInputTokens:  260,
			wantOutputTokens: 88,
			wantCostUSD:      0.0123,
			wantFinalText:    "All done.",
			wantSessionID:    "sess-abc",
			wantModel:        "claude-sonnet-4-6",
		},
		{
			name: "garbage lines are skipped",
			input: strings.Join([]string{
				`not json`,
				`{"type":"system","subtype":"init","session_id":"sess-xyz","model":"claude-sonnet-4-6"}`,
				`{garbage`,
				`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu-2","name":"Read","input":{"path":"/tmp/x"}}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
				`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":10,"output_tokens":5},"total_cost_usd":0.001,"session_id":"sess-xyz"}`,
			}, "\n"),
			wantEventTypes: []harnesses.EventType{
				harnesses.EventTypeToolCall,
			},
			wantToolCalls:    1,
			wantTurnCount:    1,
			wantInputTokens:  10,
			wantOutputTokens: 5,
			wantCostUSD:      0.001,
			wantFinalText:    "ok",
			wantSessionID:    "sess-xyz",
			wantModel:        "claude-sonnet-4-6",
		},
		{
			name: "text-only assistant emits a text_delta event",
			input: strings.Join([]string{
				`{"type":"system","subtype":"init","session_id":"sess-t","model":"claude-sonnet-4-6"}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":3,"output_tokens":2}}}`,
				`{"type":"result","subtype":"success","result":"hello","usage":{"input_tokens":3,"output_tokens":2},"total_cost_usd":0.0001,"session_id":"sess-t"}`,
			}, "\n"),
			wantEventTypes: []harnesses.EventType{
				harnesses.EventTypeTextDelta,
			},
			wantToolCalls:    0,
			wantTurnCount:    1,
			wantInputTokens:  3,
			wantOutputTokens: 2,
			wantCostUSD:      0.0001,
			wantFinalText:    "hello",
			wantSessionID:    "sess-t",
			wantModel:        "claude-sonnet-4-6",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			events, agg := runParser(t, tc.input)

			// Check the emitted Event sequence types.
			gotTypes := make([]harnesses.EventType, len(events))
			for i, ev := range events {
				gotTypes[i] = ev.Type
			}
			assert.Equal(t, tc.wantEventTypes, gotTypes, "event type sequence")

			// Aggregate state.
			assert.Equal(t, tc.wantToolCalls, agg.ToolCalls, "tool call count")
			assert.Equal(t, tc.wantTurnCount, agg.TurnCount, "turn count")
			usage, warnings := harnesses.ResolveFinalUsage(agg.UsageSources)
			require.Empty(t, warnings, "usage warnings")
			require.NotNil(t, usage, "usage")
			require.NotNil(t, usage.InputTokens, "input tokens")
			require.NotNil(t, usage.OutputTokens, "output tokens")
			assert.Equal(t, tc.wantInputTokens, *usage.InputTokens, "input tokens")
			assert.Equal(t, tc.wantOutputTokens, *usage.OutputTokens, "output tokens")
			assert.InDelta(t, tc.wantCostUSD, agg.CostUSD, 1e-9, "cost usd")
			require.NotNil(t, agg.FinalCostUSD, "reported cost pointer")
			assert.InDelta(t, tc.wantCostUSD, *agg.FinalCostUSD, 1e-9, "authoritative cost usd")
			assert.Equal(t, harnesses.CostSourceReported, agg.CostSource)
			assert.Equal(t, tc.wantFinalText, agg.FinalText, "final text")
			assert.Equal(t, tc.wantSessionID, agg.SessionID, "session id")
			assert.Equal(t, tc.wantModel, agg.Model, "model")
			assert.False(t, agg.IsError)

			// Sequence numbers should be monotonically increasing.
			for i := 1; i < len(events); i++ {
				assert.Greater(t, events[i].Sequence, events[i-1].Sequence, "sequence must increase")
			}
			// Metadata should be propagated onto every event.
			for _, ev := range events {
				assert.Equal(t, "ddx-12345678", ev.Metadata["bead_id"], "metadata propagated")
			}

			// Spot-check tool_call payload shape.
			for _, ev := range events {
				if ev.Type != harnesses.EventTypeToolCall {
					continue
				}
				var data harnesses.ToolCallData
				require.NoError(t, json.Unmarshal(ev.Data, &data))
				assert.NotEmpty(t, data.Name, "tool call has a name")
			}

			// Spot-check tool_result payload shape carries the tool_use id.
			for _, ev := range events {
				if ev.Type != harnesses.EventTypeToolResult {
					continue
				}
				var data harnesses.ToolResultData
				require.NoError(t, json.Unmarshal(ev.Data, &data))
				assert.Equal(t, "tu-1", data.ID, "tool_result preserves tool_use_id")
				assert.Contains(t, data.Output, "README.md", "tool_result preserves output content")
			}
		})
	}
}

func TestParseClaudeStreamCostPresence(t *testing.T) {
	cases := []struct {
		name      string
		costField string
		wantKnown bool
		wantCost  float64
	}{
		{name: "absent", costField: ""},
		{name: "negative", costField: `,"total_cost_usd":-0.01`},
		{name: "zero", costField: `,"total_cost_usd":0`, wantKnown: true},
		{name: "positive", costField: `,"total_cost_usd":0.0123`, wantKnown: true, wantCost: 0.0123},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := `{"type":"result","subtype":"success","result":"done"` + tc.costField + `}`
			_, agg := runParser(t, result)
			assertClaudeCostPresence(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, tc.wantKnown, tc.wantCost)

			if runtime.GOOS == "windows" {
				t.Skip("final projection fixture relies on POSIX shell")
			}
			binary := filepath.Join(t.TempDir(), "fake-claude-cost")
			script := "#!/bin/sh\nprintf '%s\\n' '" + result + "'\n"
			require.NoError(t, os.WriteFile(binary, []byte(script), 0o755))

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := (&Runner{Binary: binary}).Execute(ctx, harnesses.ExecuteRequest{Prompt: "cost"})
			require.NoError(t, err)
			events := drainEvents(t, ctx, out)
			require.NotEmpty(t, events)
			finalEvent := events[len(events)-1]
			require.Equal(t, harnesses.EventTypeFinal, finalEvent.Type)

			var final harnesses.FinalData
			require.NoError(t, json.Unmarshal(finalEvent.Data, &final))
			assertClaudeCostPresence(t, final.FinalCostUSD, final.FinalCostSource, final.CostUSD, tc.wantKnown, tc.wantCost)
			assertFinalCostJSON(t, finalEvent.Data, tc.wantKnown, tc.wantCost, func() harnesses.CostSource {
				if tc.wantKnown {
					return harnesses.CostSourceReported
				}
				return harnesses.CostSourceUnknown
			}())
		})
	}

	t.Run("latest result without cost clears earlier reported cost", func(t *testing.T) {
		_, agg := runParser(t,
			`{"type":"result","subtype":"success","result":"first","total_cost_usd":0.0123}`+"\n"+
				`{"type":"result","subtype":"success","result":"second"}`,
		)
		assertClaudeCostPresence(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, false, 0)
	})
}

func assertClaudeCostPresence(t *testing.T, cost *float64, source harnesses.CostSource, scalar float64, wantKnown bool, wantCost float64) {
	t.Helper()
	if !wantKnown {
		assert.Nil(t, cost)
		assert.Equal(t, harnesses.CostSourceUnknown, source)
		assert.Zero(t, scalar)
		return
	}
	require.NotNil(t, cost)
	assert.InDelta(t, wantCost, *cost, 1e-12)
	assert.Equal(t, harnesses.CostSourceReported, source)
	assert.InDelta(t, wantCost, scalar, 1e-12)
}

func assertFinalCostJSON(t *testing.T, raw json.RawMessage, wantKnown bool, wantCost float64, wantSource harnesses.CostSource) {
	t.Helper()
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.JSONEq(t, `"`+string(wantSource)+`"`, string(wire["cost_source"]))
	cost, ok := wire["cost_usd"]
	if !wantKnown {
		assert.False(t, ok, "unknown cost must omit cost_usd: %s", raw)
		return
	}
	require.True(t, ok, "known cost must include cost_usd: %s", raw)
	var got float64
	require.NoError(t, json.Unmarshal(cost, &got))
	assert.InDelta(t, wantCost, got, 1e-12)
}

// TestParseClaudeStream_Empty verifies the parser tolerates an empty stream
// (e.g. claude crashed before producing any events) and returns an empty but
// non-nil aggregate.
func TestParseClaudeStream_Empty(t *testing.T) {
	events, agg := runParser(t, "")
	assert.Empty(t, events)
	require.NotNil(t, agg)
	assert.Equal(t, 0, agg.TurnCount)
	assert.Empty(t, agg.UsageSources)
	assert.Empty(t, agg.FinalText)
}

// TestParseClaudeStream_ToolResultBlocks handles the variant where claude
// encodes tool_result.content as an array of content blocks (rather than a
// plain string).
func TestParseClaudeStream_ToolResultBlocks(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s","model":"claude-sonnet-4-6"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu-1","name":"Bash","input":{}}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu-1","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}]}}`,
		`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":1,"output_tokens":1},"total_cost_usd":0.0,"session_id":"s"}`,
	}, "\n")
	events, _ := runParser(t, input)

	var found *harnesses.ToolResultData
	for _, ev := range events {
		if ev.Type != harnesses.EventTypeToolResult {
			continue
		}
		var data harnesses.ToolResultData
		require.NoError(t, json.Unmarshal(ev.Data, &data))
		found = &data
	}
	require.NotNil(t, found, "expected a tool_result event")
	assert.Contains(t, found.Output, "line1")
	assert.Contains(t, found.Output, "line2")
}

func TestParseClaudeStream_UsageCassettes(t *testing.T) {
	cases := []struct {
		name              string
		wantUsage         bool
		wantInput         int
		wantOutput        int
		wantCache         int
		wantMalformed     bool
		wantDisagreement  bool
		wantSelectedInput int
	}{
		{name: "present", wantUsage: true, wantInput: 10, wantOutput: 2, wantCache: 7, wantSelectedInput: 10},
		{name: "absent"},
		{name: "malformed", wantMalformed: true},
		{name: "disagree", wantUsage: true, wantInput: 21, wantOutput: 9, wantDisagreement: true, wantSelectedInput: 21},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "usage_cassettes", tc.name+".jsonl"))
			require.NoError(t, err)
			_, agg := runParser(t, string(data))
			usage, warnings := harnesses.ResolveFinalUsage(agg.UsageSources)
			if !tc.wantUsage {
				assert.Nil(t, usage)
			} else {
				require.NotNil(t, usage)
				assert.Equal(t, harnesses.UsageSourceNativeStream, usage.Source)
				require.NotNil(t, usage.InputTokens)
				require.NotNil(t, usage.OutputTokens)
				assert.Equal(t, tc.wantInput, *usage.InputTokens)
				assert.Equal(t, tc.wantOutput, *usage.OutputTokens)
				if tc.wantCache > 0 {
					require.NotNil(t, usage.CacheTokens)
					assert.Equal(t, tc.wantCache, *usage.CacheTokens)
				}
			}
			assert.Equal(t, tc.wantMalformed, hasUsageWarning(warnings, harnesses.UsageWarningMalformed), "malformed warning")
			assert.Equal(t, tc.wantDisagreement, hasUsageWarning(warnings, harnesses.UsageWarningDisagreement), "disagreement warning")
		})
	}
}

func hasUsageWarning(warnings []harnesses.FinalWarning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

// TestClaudeStreamArgsUnsupported ensures the stderr-detection helper that
// drives fallback-to-legacy-args recognises the phrases we care about.
func TestClaudeStreamArgsUnsupported(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"error: unknown option '--output-format'", true},
		{"Error: unrecognized option --verbose", true},
		{"error: Invalid value for --output-format: stream-json", true},
		{"Usage: claude [options]\n\nerror: unknown argument", true},
		{"error: unknown flag: --output-format", true},
		{"rate limit exceeded", false},
		{"", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, claudeStreamArgsUnsupported(tc.stderr), tc.stderr)
	}
}

// TestParseClaudeStream_CtxCancellation exercises the parser's ctx
// cancellation path: when the parent context fires mid-stream the parser
// returns ctx.Err() with the partial aggregate intact.
func TestParseClaudeStream_CtxCancellation(t *testing.T) {
	// Build a stream long enough that the parser is likely mid-loop when
	// we cancel. Drop event channel buffer to 0 so the first emit blocks
	// until we cancel.
	var lines []string
	lines = append(lines, `{"type":"system","subtype":"init","session_id":"s","model":"m"}`)
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":"chunk-%d"}],"usage":{"input_tokens":1,"output_tokens":1}}}`, i))
	}
	input := strings.Join(lines, "\n")

	out := make(chan harnesses.Event) // unbuffered: first emit blocks
	var seq int64
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := parseClaudeStream(ctx, strings.NewReader(input), out, nil, &seq)
		errCh <- err
	}()

	// Wait for the first event then cancel; parser should then unblock and return ctx.Err().
	select {
	case <-out:
		// first event delivered; cancel and ensure parser exits.
	case <-time.After(2 * time.Second):
		t.Fatal("parser never produced an event")
	}
	cancel()

	// Drain remaining sends so the parser doesn't block.
	go func() {
		for range out {
		}
	}()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled), "expected ctx.Canceled, got %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("parser did not return after cancellation")
	}
}

// writeFakeClaudeBinary creates a shell script that mimics the claude CLI's
// stream-json output. The script ignores all arguments and prints a minimal
// but complete sequence of stream events so Runner.Execute has real bytes
// to parse and the progress log file ends up with content.
func writeFakeClaudeBinary(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-claude")
	script := `#!/bin/sh
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-fake","model":"claude-sonnet-4-6"}
{"type":"assistant","message":{"id":"m-1","model":"claude-sonnet-4-6","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":5,"output_tokens":2}}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"result":"hello","usage":{"input_tokens":5,"output_tokens":2},"total_cost_usd":0.0001,"session_id":"sess-fake"}
EOF
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// TestRunnerExecute_HappyPath drives Runner.Execute against a fake claude
// binary and asserts the emitted events terminate in a final event with
// status=success and the parsed cost/tokens attached.
func TestRunnerExecute_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary relies on POSIX shell")
	}
	tmp := t.TempDir()
	binPath := writeFakeClaudeBinary(t, tmp)
	logDir := filepath.Join(tmp, "session-logs")

	r := &Runner{Binary: binPath}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:        "hi",
		SessionLogDir: logDir,
		SessionID:     "fake-session",
		Metadata:      map[string]string{"bead_id": "ddx-test"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ctx, out)
	require.NotEmpty(t, events)

	// Last event must be the final.
	last := events[len(events)-1]
	assert.Equal(t, harnesses.EventTypeFinal, last.Type)
	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(last.Data, &final))
	assert.Equal(t, "success", final.Status)
	assert.Equal(t, 0, final.ExitCode)
	assert.Equal(t, "hello", final.FinalText)
	require.NotNil(t, final.Usage)
	require.NotNil(t, final.Usage.InputTokens)
	require.NotNil(t, final.Usage.OutputTokens)
	assert.Equal(t, 5, *final.Usage.InputTokens)
	assert.Equal(t, 2, *final.Usage.OutputTokens)
	require.NotNil(t, final.Usage.TotalTokens)
	assert.Equal(t, 7, *final.Usage.TotalTokens)
	assert.InDelta(t, 0.0001, final.CostUSD, 1e-9)
	require.NotNil(t, final.FinalCostUSD)
	assert.InDelta(t, 0.0001, *final.FinalCostUSD, 1e-9)
	assert.Equal(t, harnesses.CostSourceReported, final.FinalCostSource)

	// Earlier events should include a text_delta.
	var sawText bool
	for _, ev := range events[:len(events)-1] {
		if ev.Type == harnesses.EventTypeTextDelta {
			sawText = true
		}
	}
	assert.True(t, sawText, "expected at least one text_delta before final")

	// Progress log should have been written.
	entries, err := os.ReadDir(logDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "session log dir should contain agent-*.jsonl")
}

func TestClaudeRunnerFinalClassifiesAuthenticationFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary relies on POSIX shell")
	}
	tmp := t.TempDir()
	binPath := filepath.Join(tmp, "fake-claude-auth-failure")
	script := `#!/bin/sh
printf '%s\n' 'Failed to authenticate' 'Could not refresh auth token' >&2
exit 1
`
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))

	runner := &Runner{Binary: binPath}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runner.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hi"})
	require.NoError(t, err)
	events := drainEvents(t, ctx, out)

	var finals []harnesses.FinalData
	for _, event := range events {
		if event.Type != harnesses.EventTypeFinal {
			continue
		}
		var final harnesses.FinalData
		require.NoError(t, json.Unmarshal(event.Data, &final))
		finals = append(finals, final)
	}
	require.Len(t, finals, 1)
	final := finals[0]
	assert.Equal(t, "failed", final.Status)
	assert.Contains(t, final.Error, "exit status 1")
	assert.Contains(t, final.Error, "Failed to authenticate")
	assert.Contains(t, final.Error, "Could not refresh auth token")
	assert.Contains(t, final.Error, anthropic.CredentialRemediationGuidance)
	require.NotNil(t, final.RoutingActual)
	assert.Equal(t, "claude", final.RoutingActual.Harness)
	assert.Equal(t, "credential_invalid", final.RoutingActual.FailureClass)
}

func TestClaudeFinalErrorPreservesAndSanitizesNonSuccessStatuses(t *testing.T) {
	cancelled := claudeFinalError("cancelled", errors.New("context canceled ANTHROPIC_API_KEY=cancel-secret"), "ignored stderr", "")
	assert.Contains(t, cancelled, "context canceled")
	assert.NotContains(t, cancelled, "cancel-secret")
	assert.NotContains(t, cancelled, "ignored stderr", "cancellation preserves legacy process-error precedence")

	failed := claudeFinalError("failed", errors.New("exit status 1"), "Failed to authenticate\nCould not refresh auth token", "")
	assert.Contains(t, failed, "exit status 1")
	assert.Contains(t, failed, "Failed to authenticate")
	assert.LessOrEqual(t, len(failed), 2048)

	quota := claudeFinalError("failed", errors.New("exit status 1"), "stderr detail", "usage limit reached")
	assert.Equal(t, "claude quota exhausted: usage limit reached", quota, "quota keeps its legacy terminal diagnostic precedence")
}

func TestRunnerExecute_QuotaMessageMarksCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary relies on POSIX shell")
	}
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "claude-quota.json")
	t.Setenv(claudeQuotaCacheEnv, cachePath)

	binPath := filepath.Join(tmp, "claude")
	script := `#!/bin/sh
cat <<'EOF'
{"type":"assistant","message":{"id":"m-1","model":"claude-sonnet-4-6","content":[{"type":"text","text":"You're out of extra usage · resets May 7, 12am (America/New_York)"}]}}
EOF
exit 1
`
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))

	r := &Runner{Binary: binPath}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hi"})
	require.NoError(t, err)
	events := drainEvents(t, ctx, out)
	require.NotEmpty(t, events)

	var final harnesses.FinalData
	require.NoError(t, json.Unmarshal(events[len(events)-1].Data, &final))
	assert.Equal(t, "failed", final.Status)
	assert.Contains(t, final.Error, "claude quota exhausted")

	snap, ok := readClaudeQuotaFrom(cachePath)
	require.True(t, ok)
	dec := decideClaudeQuotaRouting(snap, time.Now().UTC(), defaultClaudeQuotaStaleAfter)
	assert.False(t, dec.PreferClaude)
	assert.Contains(t, dec.Reason, "exhausted")
}

func TestRunnerBuildArgs_AppliesRequestControls(t *testing.T) {
	r := &Runner{}
	args := r.buildArgs([]string{"--print", "-p", "--verbose", "--output-format", "stream-json"}, harnesses.ExecuteRequest{
		Model:       "claude-sonnet-4-6",
		Reasoning:   "xhigh",
		Permissions: "unrestricted",
	})
	assert.Equal(t, []string{
		"--print", "-p", "--verbose", "--output-format", "stream-json",
		"--permission-mode", "bypassPermissions", "--dangerously-skip-permissions",
		"--model", "claude-sonnet-4-6",
		"--effort", "xhigh",
	}, args)

	args = r.buildArgs([]string{"--print"}, harnesses.ExecuteRequest{Permissions: "supervised"})
	// When no DiscoveryCache is provided, the runner defaults to claude-sonnet-4-6.
	// Model discovery from DefaultModelSnapshot() is not available in unit tests.
	assert.Equal(t, []string{"--print", "--permission-mode", "default", "--model", "claude-sonnet-4-6"}, args)
}

func TestRunnerBuildArgs_SnapsReasoningToDiscoveryLevels(t *testing.T) {
	cache := harnesses.NewModelDiscoveryCache(func(harnessName, source string) (harnesses.ModelDiscoverySnapshot, error) {
		return harnesses.ModelDiscoverySnapshot{
			CapturedAt:      time.Now().UTC(),
			Models:          []string{"claude-sonnet-4-6"},
			ReasoningLevels: []string{"low", "medium"},
			Source:          source,
		}, nil
	})
	r := &Runner{DiscoveryCache: cache}
	args := r.buildArgs([]string{"--print"}, harnesses.ExecuteRequest{
		Model:     "claude-sonnet-4-6",
		Reasoning: "high",
	})
	assert.Equal(t, []string{"--print", "--model", "claude-sonnet-4-6", "--effort", "medium"}, args)
}

func TestRunnerExecute_AppliesRequestControlsAndWorkdir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary relies on POSIX shell")
	}
	tmp := t.TempDir()
	capture := filepath.Join(tmp, "capture.txt")
	workDir := filepath.Join(tmp, "work")
	require.NoError(t, os.Mkdir(workDir, 0o755))
	binPath := filepath.Join(tmp, "fake-claude")
	script := fmt.Sprintf(`#!/bin/sh
{
  pwd
  i=0
  for arg in "$@"; do
    printf 'ARG[%%s]=%%s\n' "$i" "$arg"
    i=$((i + 1))
  done
} > %q
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess","model":"claude-sonnet-4-6"}
{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":2}}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":1,"result":"ok","usage":{"input_tokens":3,"output_tokens":2},"session_id":"sess"}
EOF
`, capture)
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))

	r := &Runner{Binary: binPath}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "hello prompt",
		Model:       "claude-sonnet-4-6",
		Reasoning:   "high",
		Permissions: "unrestricted",
		WorkDir:     workDir,
	})
	require.NoError(t, err)
	events := drainEvents(t, ctx, out)
	require.NotEmpty(t, events)

	raw, err := os.ReadFile(capture)
	require.NoError(t, err)
	got := string(raw)
	for _, want := range []string{
		workDir,
		"ARG[0]=--print",
		"ARG[1]=-p",
		"ARG[2]=--verbose",
		"ARG[3]=--output-format",
		"ARG[4]=stream-json",
		"ARG[5]=--permission-mode",
		"ARG[6]=bypassPermissions",
		"ARG[7]=--dangerously-skip-permissions",
		"ARG[8]=--model",
		"ARG[9]=claude-sonnet-4-6",
		"ARG[10]=--effort",
		"ARG[11]=high",
		"ARG[12]=hello prompt",
	} {
		require.Contains(t, got, want)
	}
}

func TestRunnerExecute_UsesDiscoveryDefaultWhenModelUnpinned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary relies on POSIX shell")
	}
	tmp := t.TempDir()
	capture := filepath.Join(tmp, "capture.txt")
	binPath := filepath.Join(tmp, "fake-claude")
	script := fmt.Sprintf(`#!/bin/sh
{
  i=0
  for arg in "$@"; do
    printf 'ARG[%%s]=%%s\n' "$i" "$arg"
    i=$((i + 1))
  done
} > %q
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess","model":"opus-4.7"}
{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":2}}}
{"type":"result","subtype":"success","is_error":false,"duration_ms":1,"result":"ok","usage":{"input_tokens":3,"output_tokens":2},"session_id":"sess"}
EOF
`, capture)
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))

	r := &Runner{Binary: binPath}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hello prompt"})
	require.NoError(t, err)
	events := drainEvents(t, ctx, out)
	require.NotEmpty(t, events)

	raw, err := os.ReadFile(capture)
	require.NoError(t, err)
	got := string(raw)
	for _, want := range []string{
		"ARG[0]=--print",
		"ARG[1]=-p",
		"ARG[2]=--verbose",
		"ARG[3]=--output-format",
		"ARG[4]=stream-json",
		"ARG[5]=--model",
		"ARG[6]=claude-sonnet-4-6", // Default model when discovery is unavailable
		"ARG[7]=hello prompt",
	} {
		require.Contains(t, got, want)
	}

	var resolution *harnesses.RunnerModelResolution
	for _, ev := range events {
		if ev.Type != harnesses.EventTypeRoutingDecision {
			continue
		}
		var data harnesses.RunnerModelResolution
		require.NoError(t, json.Unmarshal(ev.Data, &data))
		resolution = &data
	}
	require.NotNil(t, resolution, "expected runner default-resolution signal")
	// When model discovery is not available (PTY fails), the runner uses the default model.
	// With PTY-only model discovery, the test's fake binary doesn't support /model command,
	// so discovery fails and defaults to claude-sonnet-4-6.
	assert.Equal(t, "claude-sonnet-4-6", resolution.ResolvedModel)
	assert.Equal(t, "claude-sonnet-4-6", resolution.PriorDefaultModel)
	assert.Equal(t, "claude-code", resolution.Surface)
}

// writeSlowFakeClaudeBinary emits an init event then sleeps so the test can
// cancel the context and verify the runner kills the subprocess. The script
// also installs a SIGTERM trap so we can confirm graceful termination.
func writeSlowFakeClaudeBinary(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "slow-claude")
	script := `#!/bin/sh
trap 'exit 0' TERM
echo '{"type":"system","subtype":"init","session_id":"s","model":"m"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"alive"}],"usage":{"input_tokens":1,"output_tokens":1}}}'
# Block forever waiting for a signal.
sleep 30 &
wait
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

// TestRunnerExecute_CancellationReapsSubprocess verifies the PTY/orphan
// reaping path: when the parent ctx is cancelled mid-run, the runner
// signals the subprocess and Execute terminates with a final event that
// reflects cancellation rather than hanging.
func TestRunnerExecute_CancellationReapsSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude binary relies on POSIX shell")
	}
	tmp := t.TempDir()
	binPath := writeSlowFakeClaudeBinary(t, tmp)

	r := &Runner{Binary: binPath, PromptMode: "stdin"}
	ctx, cancel := context.WithCancel(context.Background())

	out, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hi"})
	require.NoError(t, err)

	// Wait until at least one event arrives so we know the subprocess started.
	select {
	case <-out:
	case <-time.After(3 * time.Second):
		t.Fatal("subprocess never emitted an event")
	}

	// Cancel and confirm the channel closes within a bounded window.
	cancel()

	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				// Channel closed — runner cleaned up.
				return
			}
			if ev.Type == harnesses.EventTypeFinal {
				var final harnesses.FinalData
				require.NoError(t, json.Unmarshal(ev.Data, &final))
				assert.Contains(t, []string{"cancelled", "timed_out", "failed"}, final.Status,
					"final status after cancel must reflect termination, got %s", final.Status)
			}
		case <-deadline:
			t.Fatal("runner did not terminate within 5s after cancel")
		}
	}
}

// TestRunnerInfo_PathResolution verifies Info reports a path when Binary
// is set, and falls back to PATH lookup otherwise.
func TestRunnerInfo_PathResolution(t *testing.T) {
	r := &Runner{Binary: "/absolutely/not/a/real/claude"}
	info := r.Info()
	assert.Equal(t, "claude", info.Name)
	assert.Equal(t, "subprocess", info.Type)
	assert.Equal(t, "/absolutely/not/a/real/claude", info.Path)
	// Available is path-only (no stat); HealthCheck would catch missing files.
	assert.True(t, info.Available)
	assert.Contains(t, info.SupportedPermissions, "safe")
}

// TestRunnerHealthCheck_MissingBinary returns an error when the configured
// Binary does not exist.
func TestRunnerHealthCheck_MissingBinary(t *testing.T) {
	r := &Runner{Binary: "/absolutely/not/a/real/claude"}
	err := r.HealthCheck(context.Background())
	require.Error(t, err)
}
