package claudetui_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/quick"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
)

// realTranscript is a fixture in the REAL Claude Code 2.1.x transcript schema:
// top-level {type:"assistant"|"user"|...} lines whose message.content holds
// {thinking,text,tool_use,tool_result} blocks. tool_result blocks appear
// inside user lines. Usage + stop_reason ride the last assistant line.
const realTranscript = `{"type":"ai-title","title":"List directory"}
{"type":"assistant","message":{"model":"claude-sonnet-4-6","id":"msg_1","role":"assistant","content":[{"type":"thinking","text":"I should list the dir."},{"type":"text","text":"Let me list the directory."},{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls"}}],"stop_reason":"tool_use","usage":{"input_tokens":120,"output_tokens":30,"cache_read_input_tokens":5}}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"file.txt\ndir/"}]}}
{"type":"assistant","message":{"model":"claude-sonnet-4-6","id":"msg_2","role":"assistant","content":[{"type":"text","text":"The directory contains file.txt and dir/."}],"stop_reason":"end_turn","usage":{"input_tokens":200,"output_tokens":40}}}
`

func writeTranscript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return p
}

func drainTranscript(t *testing.T, path string) []harnesses.Event {
	t.Helper()
	tailer := claudetui.NewTranscriptTailer(path, "test", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch := make(chan harnesses.Event, 64)
	go func() {
		_ = tailer.ReadEvents(ctx, ch)
		close(ch)
	}()
	var events []harnesses.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// TestTranscriptParserRealSchemaWalksContentBlocks proves the rewritten parser
// walks assistant/user message.content blocks into text_delta/tool_call/
// tool_result events and synthesizes exactly one final from the last assistant
// stop_reason + usage. The old flat-schema parser produced ZERO events here.
func TestTranscriptParserRealSchemaWalksContentBlocks(t *testing.T) {
	path := writeTranscript(t, realTranscript)
	events := drainTranscript(t, path)

	var (
		textDeltas  []string
		toolCalls   []harnesses.ToolCallData
		toolResults []harnesses.ToolResultData
		finals      []harnesses.FinalData
	)
	for _, ev := range events {
		switch ev.Type {
		case harnesses.EventTypeTextDelta:
			var d harnesses.TextDeltaData
			_ = json.Unmarshal(ev.Data, &d)
			textDeltas = append(textDeltas, d.Text)
		case harnesses.EventTypeToolCall:
			var d harnesses.ToolCallData
			_ = json.Unmarshal(ev.Data, &d)
			toolCalls = append(toolCalls, d)
		case harnesses.EventTypeToolResult:
			var d harnesses.ToolResultData
			_ = json.Unmarshal(ev.Data, &d)
			toolResults = append(toolResults, d)
		case harnesses.EventTypeFinal:
			var d harnesses.FinalData
			_ = json.Unmarshal(ev.Data, &d)
			finals = append(finals, d)
		}
	}

	if len(textDeltas) != 2 {
		t.Fatalf("text_delta count = %d, want 2 (%v)", len(textDeltas), textDeltas)
	}
	if textDeltas[0] != "Let me list the directory." {
		t.Errorf("text_delta[0] = %q", textDeltas[0])
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != "toolu_01" || toolCalls[0].Name != "Bash" {
		t.Fatalf("tool_call = %+v, want one Bash call toolu_01", toolCalls)
	}
	if len(toolResults) != 1 || toolResults[0].ID != "toolu_01" {
		t.Fatalf("tool_result = %+v, want one for toolu_01", toolResults)
	}
	if toolResults[0].Output != "file.txt\ndir/" {
		t.Errorf("tool_result output = %q", toolResults[0].Output)
	}

	// Exactly one final, from the LAST assistant line.
	if len(finals) != 1 {
		t.Fatalf("final count = %d, want exactly 1", len(finals))
	}
	fin := finals[0]
	if fin.Status != "success" {
		t.Errorf("final status = %q, want success", fin.Status)
	}
	if fin.FinalText != "Let me list the directory.The directory contains file.txt and dir/." {
		t.Errorf("final text = %q", fin.FinalText)
	}
	if fin.Usage == nil {
		t.Fatal("final usage is nil; want usage from last assistant line")
	}
	if fin.Usage.InputTokens == nil || *fin.Usage.InputTokens != 200 {
		t.Errorf("final input_tokens = %v, want 200 (last assistant line)", fin.Usage.InputTokens)
	}
	if fin.Usage.OutputTokens == nil || *fin.Usage.OutputTokens != 40 {
		t.Errorf("final output_tokens = %v, want 40", fin.Usage.OutputTokens)
	}
}

// turn1Transcript and turn2Lines model an append-only session .jsonl where two
// turns share ONE file (the real Claude Code behavior). Turn 1 is the whole of
// turn1Transcript; turn 2 appends turn2Lines.
const turn1Transcript = `{"type":"assistant","message":{"model":"claude-sonnet-4-6","id":"msg_t1","role":"assistant","content":[{"type":"text","text":"Turn one answer."}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}}
`

const turn2Lines = `{"type":"assistant","message":{"model":"claude-sonnet-4-6","id":"msg_t2","role":"assistant","content":[{"type":"text","text":"Turn two answer."}],"stop_reason":"end_turn","usage":{"input_tokens":99,"output_tokens":7}}}
`

// TestTranscriptTailerResumesFromOffset proves F2: a reused session resumes the
// transcript read at a byte offset so it emits ONLY the new turn's events and
// its final reflects ONLY the new turn (not a replay of prior turns folded
// together). Without the offset, turn 2 would re-emit turn 1's text and fold
// turn 1's usage into the final.
func TestTranscriptTailerResumesFromOffset(t *testing.T) {
	// Turn 1: read the whole file, capture the resume offset.
	path := writeTranscript(t, turn1Transcript)

	t1 := claudetui.NewTranscriptTailer(path, "test", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch1 := make(chan harnesses.Event, 16)
	if err := t1.ReadEvents(ctx, ch1); err != nil {
		t.Fatalf("turn1 ReadEvents: %v", err)
	}
	close(ch1)
	off := t1.EndOffset()
	if off <= 0 {
		t.Fatalf("turn1 EndOffset = %d, want > 0", off)
	}

	// Append turn 2 to the SAME file (append-only session transcript).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(turn2Lines); err != nil {
		t.Fatalf("append turn2: %v", err)
	}
	_ = f.Close()

	// Turn 2: resume from the captured offset.
	t2 := claudetui.NewTranscriptTailer(path, "test", nil)
	t2.SetStartOffset(off)
	ch2 := make(chan harnesses.Event, 16)
	if err := t2.ReadEvents(ctx, ch2); err != nil {
		t.Fatalf("turn2 ReadEvents: %v", err)
	}
	close(ch2)

	var texts []string
	var finals []harnesses.FinalData
	for ev := range ch2 {
		switch ev.Type {
		case harnesses.EventTypeTextDelta:
			var d harnesses.TextDeltaData
			_ = json.Unmarshal(ev.Data, &d)
			texts = append(texts, d.Text)
		case harnesses.EventTypeFinal:
			var d harnesses.FinalData
			_ = json.Unmarshal(ev.Data, &d)
			finals = append(finals, d)
		}
	}

	// Turn 2 must NOT replay turn 1's text.
	if len(texts) != 1 || texts[0] != "Turn two answer." {
		t.Fatalf("turn2 texts = %v, want exactly [\"Turn two answer.\"] (no turn-1 replay)", texts)
	}
	if len(finals) != 1 {
		t.Fatalf("turn2 finals = %d, want exactly 1", len(finals))
	}
	// The final's text and usage must reflect ONLY turn 2.
	if finals[0].FinalText != "Turn two answer." {
		t.Errorf("turn2 final text = %q, want only turn-2 text", finals[0].FinalText)
	}
	if finals[0].Usage == nil || finals[0].Usage.InputTokens == nil || *finals[0].Usage.InputTokens != 99 {
		t.Errorf("turn2 final usage input_tokens = %v, want 99 (turn-2 only)", finals[0].Usage)
	}
}

// TestTranscriptParserExactlyOneFinal proves the parser emits exactly one
// final even when many assistant lines are present.
func TestTranscriptParserExactlyOneFinal(t *testing.T) {
	path := writeTranscript(t, realTranscript)
	events := drainTranscript(t, path)
	finals := 0
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeFinal {
			finals++
		}
	}
	if finals != 1 {
		t.Fatalf("final events = %d, want 1", finals)
	}
	if events[len(events)-1].Type != harnesses.EventTypeFinal {
		t.Fatalf("last event type = %v, want final", events[len(events)-1].Type)
	}
}

// TestTranscriptParserMaxTokensMapsIterationLimit proves stop_reason mapping.
func TestTranscriptParserMaxTokensMapsIterationLimit(t *testing.T) {
	body := `{"type":"assistant","message":{"id":"m","role":"assistant","content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":2}}}
`
	path := writeTranscript(t, body)
	events := drainTranscript(t, path)
	var fin *harnesses.FinalData
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeFinal {
			var d harnesses.FinalData
			_ = json.Unmarshal(ev.Data, &d)
			fin = &d
		}
	}
	if fin == nil {
		t.Fatal("no final event")
	}
	if fin.Status != "iteration_limit" {
		t.Errorf("status = %q, want iteration_limit", fin.Status)
	}
}

// TestTranscriptParserSkipsMalformedLines proves malformed lines are skipped
// without aborting the stream.
func TestTranscriptParserSkipsMalformedLines(t *testing.T) {
	body := `{"type":"assistant","message":{"id":"m","role":"assistant","content":[{"type":"text","text":"a"}],"stop_reason":"end_turn"}}
{this is not json
{"type":"assistant","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"b"}],"stop_reason":"end_turn"}}
`
	path := writeTranscript(t, body)
	events := drainTranscript(t, path)
	var texts []string
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeTextDelta {
			var d harnesses.TextDeltaData
			_ = json.Unmarshal(ev.Data, &d)
			texts = append(texts, d.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "a" || texts[1] != "b" {
		t.Errorf("texts = %v, want [a b]", texts)
	}
}

// TestTranscriptParserEmptyOrIncompleteNoFinal proves an empty transcript (no
// assistant line) yields no events at all, so the harness can fall back.
func TestTranscriptParserEmptyOrIncompleteNoFinal(t *testing.T) {
	path := writeTranscript(t, "")
	if events := drainTranscript(t, path); len(events) != 0 {
		t.Errorf("empty transcript yielded %d events, want 0", len(events))
	}

	// A transcript with only a non-assistant line yields no final.
	body := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`
	path2 := writeTranscript(t, body)
	for _, ev := range drainTranscript(t, path2) {
		if ev.Type == harnesses.EventTypeFinal {
			t.Errorf("user-only transcript should not produce a final event")
		}
	}
}

// TestParseTranscriptLineReal exercises the line decoder against real-shape lines.
func TestParseTranscriptLineReal(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{"assistant", `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`, false},
		{"user", `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`, false},
		{"malformed", `{"type":"assistant"`, true},
		{"empty", ``, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := claudetui.ParseTranscriptLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// FuzzParseTranscriptLine ensures the decoder never panics.
func FuzzParseTranscriptLine(f *testing.F) {
	f.Add(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`)
	f.Add(`{"type":"user"}`)
	f.Add(`{invalid}`)
	f.Add(``)
	f.Fuzz(func(t *testing.T, line string) {
		_, _ = claudetui.ParseTranscriptLine(line)
	})
}

// TestTranscriptLineRoundTrip is a property test for the type discriminator.
func TestTranscriptLineRoundTrip(t *testing.T) {
	prop := func(text string) bool {
		obj := map[string]interface{}{
			"type":    "assistant",
			"message": map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": text}}},
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return false
		}
		line, err := claudetui.ParseTranscriptLine(string(b))
		if err != nil {
			return false
		}
		return line.Type == "assistant"
	}
	if err := quick.Check(prop, nil); err != nil {
		t.Errorf("property failed: %v", err)
	}
}

// --- Hook-event tailer ------------------------------------------------------

// TestHookEventTailerEmitsToolEvents proves the hook-event tailer emits
// tool_call (PreToolUse) and tool_result (PostToolUse) ProgressEvents from
// per-tool payload files DURING the turn, in file order, exactly once each.
func TestHookEventTailerEmitsToolEvents(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Filenames are lexically ordered: pre before post.
	write("tool-0001-pre.json", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_01","tool_input":{"command":"ls"}}`)
	write("tool-0002-post.json", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_01","tool_response":"file.txt"}`)
	write("ignore.txt", "not a tool payload")

	tailer := claudetui.NewHookEventTailer(dir, nil)
	var got []harnesses.Event
	seq := tailer.Drain(0, func(ev harnesses.Event) { got = append(got, ev) })

	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2", len(got))
	}
	if got[0].Type != harnesses.EventTypeToolCall {
		t.Errorf("event[0] type = %v, want tool_call", got[0].Type)
	}
	if got[1].Type != harnesses.EventTypeToolResult {
		t.Errorf("event[1] type = %v, want tool_result", got[1].Type)
	}
	var call harnesses.ToolCallData
	_ = json.Unmarshal(got[0].Data, &call)
	if call.Name != "Bash" || call.ID != "toolu_01" {
		t.Errorf("tool_call = %+v", call)
	}
	var res harnesses.ToolResultData
	_ = json.Unmarshal(got[1].Data, &res)
	if res.ID != "toolu_01" || res.Output != "file.txt" {
		t.Errorf("tool_result = %+v", res)
	}

	// Re-draining must not re-emit already-seen payloads (idempotent tail).
	var second []harnesses.Event
	seq2 := tailer.Drain(seq, func(ev harnesses.Event) { second = append(second, ev) })
	if len(second) != 0 {
		t.Errorf("re-drain emitted %d events, want 0", len(second))
	}
	if seq2 != seq {
		t.Errorf("seq advanced on empty re-drain: %d -> %d", seq, seq2)
	}
}

// TestHookEventCorrelationByEmissionOrder proves F3: when real Claude Code
// 2.1.x PreToolUse/PostToolUse payloads OMIT a top-level tool_use_id (the
// documented payloads carry session_id/tool_name/tool_input/tool_response but
// no tool_use_id), the tailer assigns a synthetic correlation ID by emission
// order so the Nth tool_call and Nth tool_result share an ID — a downstream
// consumer can still pair a PreToolUse with its PostToolUse.
func TestHookEventCorrelationByEmissionOrder(t *testing.T) {
	dir := t.TempDir()
	// Two tool roundtrips, NONE carrying tool_use_id (real-schema shape).
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tool-0001-pre.json", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	write("tool-0002-post.json", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":"ok"}`)
	write("tool-0003-pre.json", `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file":"a"}}`)
	write("tool-0004-post.json", `{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_response":"done"}`)

	tailer := claudetui.NewHookEventTailer(dir, nil)
	var calls []harnesses.ToolCallData
	var results []harnesses.ToolResultData
	tailer.Drain(0, func(ev harnesses.Event) {
		switch ev.Type {
		case harnesses.EventTypeToolCall:
			var d harnesses.ToolCallData
			_ = json.Unmarshal(ev.Data, &d)
			calls = append(calls, d)
		case harnesses.EventTypeToolResult:
			var d harnesses.ToolResultData
			_ = json.Unmarshal(ev.Data, &d)
			results = append(results, d)
		}
	})

	if len(calls) != 2 || len(results) != 2 {
		t.Fatalf("got %d calls, %d results; want 2 and 2", len(calls), len(results))
	}
	// Every event must carry a non-empty correlation ID (derived, since the
	// payloads omit tool_use_id).
	for _, c := range calls {
		if c.ID == "" {
			t.Errorf("tool_call %q has empty correlation ID", c.Name)
		}
	}
	// The Nth call and Nth result must share an ID.
	if calls[0].ID != results[0].ID {
		t.Errorf("first roundtrip ID mismatch: call %q result %q", calls[0].ID, results[0].ID)
	}
	if calls[1].ID != results[1].ID {
		t.Errorf("second roundtrip ID mismatch: call %q result %q", calls[1].ID, results[1].ID)
	}
	// Distinct roundtrips must NOT collide.
	if calls[0].ID == calls[1].ID {
		t.Errorf("distinct tool roundtrips share an ID %q", calls[0].ID)
	}
}

// TestHookEventRealToolUseIDTakesPrecedence proves a payload that DOES carry a
// real tool_use_id uses it verbatim instead of a synthetic correlation ID.
func TestHookEventRealToolUseIDTakesPrecedence(t *testing.T) {
	he, err := claudetui.ParseHookEvent([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_real","tool_input":{}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ev, ok := claudetui.HookEventToEvent(he, 1, "tool-7")
	if !ok {
		t.Fatal("event did not map")
	}
	var d harnesses.ToolCallData
	_ = json.Unmarshal(ev.Data, &d)
	if d.ID != "toolu_real" {
		t.Errorf("ID = %q, want toolu_real (real tool_use_id must win over corrID)", d.ID)
	}
}

// TestParseHookEvent proves payload decoding and event mapping.
func TestParseHookEvent(t *testing.T) {
	he, err := claudetui.ParseHookEvent([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_use_id":"x"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ev, ok := claudetui.HookEventToEvent(he, 7, "")
	if !ok || ev.Type != harnesses.EventTypeToolCall || ev.Sequence != 7 {
		t.Errorf("mapping = %+v ok=%v", ev, ok)
	}

	_, ok = claudetui.HookEventToEvent(claudetui.HookEvent{Event: "SessionStart"}, 1, "")
	if ok {
		t.Errorf("unknown hook event should not map to a canonical event")
	}
}

// TestHookPayloadParsing covers the Stop-payload transcript-path extractor.
func TestHookPayloadParsing(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{"valid", `{"transcript_path":"~/.claude/projects/x/abc.jsonl"}`, "~/.claude/projects/x/abc.jsonl", false},
		{"missing", `{"other":"v"}`, "", true},
		{"invalid", `{invalid}`, "", true},
		{"empty", `{"transcript_path":""}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := claudetui.ReadHookPayload([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExpandTranscriptPath covers ~ expansion.
func TestExpandTranscriptPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	tests := []struct{ path, want string }{
		{"~/.claude/x", filepath.Join(home, ".claude/x")},
		{"/abs/x.jsonl", "/abs/x.jsonl"},
		{"rel.jsonl", "rel.jsonl"},
	}
	for _, tt := range tests {
		got, err := claudetui.ExpandTranscriptPath(tt.path)
		if err != nil {
			t.Errorf("expand %q: %v", tt.path, err)
			continue
		}
		if got != tt.want {
			t.Errorf("expand %q = %q, want %q", tt.path, got, tt.want)
		}
	}
}
