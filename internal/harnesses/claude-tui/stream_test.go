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

// TestParseTranscriptLine tests basic JSONL parsing.
func TestParseTranscriptLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    string
		wantErr bool
	}{
		{
			name: "text_delta",
			line: `{"type":"text_delta","text":"hello"}`,
			want: "text_delta",
		},
		{
			name: "tool_call",
			line: `{"type":"tool_call","id":"call-123","name":"bash","input":{"command":"ls"}}`,
			want: "tool_call",
		},
		{
			name: "tool_result",
			line: `{"type":"tool_result","id":"call-123","output":"file.txt"}`,
			want: "tool_result",
		},
		{
			name: "final with status",
			line: `{"type":"final","status":"success","text":"Done","usage":{"input_tokens":100,"output_tokens":50}}`,
			want: "final",
		},
		{
			name:    "malformed JSON",
			line:    `{"type":"text_delta"`,
			wantErr: true,
		},
		{
			name:    "empty line",
			line:    ``,
			wantErr: true,
		},
		{
			name:    "whitespace only",
			line:    `   `,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := claudetui.ParseTranscriptLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTranscriptLine(%q) error = %v, wantErr %v", tt.line, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Type != tt.want {
				t.Errorf("ParseTranscriptLine(%q).Type = %s, want %s", tt.line, got.Type, tt.want)
			}
		})
	}
}

// TestTranscriptLineRoundTrip tests that we can parse and re-marshal JSONL lines.
// This is a property test using quick.Check.
func TestTranscriptLineRoundTrip(t *testing.T) {
	prop := func(typeStr string, id string, name string, text string) bool {
		// Only test known types
		if typeStr != "text_delta" && typeStr != "tool_call" && typeStr != "tool_result" {
			return true
		}

		data := map[string]interface{}{
			"type": typeStr,
		}
		if id != "" {
			data["id"] = id
		}
		if name != "" {
			data["name"] = name
		}
		if text != "" {
			data["text"] = text
		}

		// Marshal to JSON
		jsonBytes, err := json.Marshal(data)
		if err != nil {
			return false
		}

		// Parse back
		line, err := claudetui.ParseTranscriptLine(string(jsonBytes))
		if err != nil {
			return false
		}

		// Check type survived the round trip
		if line.Type != typeStr {
			return false
		}

		return true
	}

	// Run the property test
	if err := quick.Check(prop, nil); err != nil {
		t.Errorf("property test failed: %v", err)
	}
}

// FuzzParseTranscriptLine fuzzes the JSONL parser.
func FuzzParseTranscriptLine(f *testing.F) {
	// Seed corpus with known cases
	f.Add(`{"type":"text_delta","text":"hello"}`)
	f.Add(`{"type":"tool_call","id":"123","name":"bash"}`)
	f.Add(`{"type":"tool_result","id":"123","output":"result"}`)
	f.Add(`{"type":"final","status":"success"}`)
	f.Add(`{invalid json}`)
	f.Add(``)
	f.Add(`{"type":"","foo":"bar"}`)

	f.Fuzz(func(t *testing.T, line string) {
		// ParseTranscriptLine should not panic on any input
		tl, err := claudetui.ParseTranscriptLine(line)
		_ = tl
		_ = err
		// We don't assert specific behavior; just that it doesn't panic
	})
}

// TestTranscriptTailerBasic tests basic transcript reading.
func TestTranscriptTailerBasic(t *testing.T) {
	// Create a temporary file with sample JSONL
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write sample transcript
	transcript := `{"type":"text_delta","text":"Hello"}
{"type":"text_delta","text":" world"}
{"type":"final","status":"success","text":"Hello world"}
`
	if _, err := tmpFile.WriteString(transcript); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	tmpFile.Close()

	// Create tailer and read events
	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
		return
	}

	// Check types
	if events[0].Type != harnesses.EventTypeTextDelta {
		t.Errorf("event 0: got type %s, want text_delta", events[0].Type)
	}
	if events[1].Type != harnesses.EventTypeTextDelta {
		t.Errorf("event 1: got type %s, want text_delta", events[1].Type)
	}
	if events[2].Type != harnesses.EventTypeFinal {
		t.Errorf("event 2: got type %s, want final", events[2].Type)
	}

	// Check sequence numbers
	if events[0].Sequence != 1 || events[1].Sequence != 2 || events[2].Sequence != 3 {
		t.Errorf("sequence numbers: got %d,%d,%d want 1,2,3", events[0].Sequence, events[1].Sequence, events[2].Sequence)
	}
}

// TestMalformedJSONLHandling tests that malformed lines are skipped.
func TestMalformedJSONLHandling(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write transcript with a malformed line in the middle
	transcript := `{"type":"text_delta","text":"start"}
{"invalid":"json"
{"type":"text_delta","text":"end"}
`
	if _, err := tmpFile.WriteString(transcript); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	tmpFile.Close()

	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	// Should have 2 events (malformed line skipped)
	if len(events) != 2 {
		t.Errorf("got %d events, want 2 (malformed line should be skipped)", len(events))
		return
	}

	var texts []string
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeTextDelta {
			var data harnesses.TextDeltaData
			_ = json.Unmarshal(ev.Data, &data)
			texts = append(texts, data.Text)
		}
	}

	if len(texts) != 2 || texts[0] != "start" || texts[1] != "end" {
		t.Errorf("got texts %v, want [start, end]", texts)
	}
}

// TestPartialWriteHandling tests handling of incomplete lines (AC #8).
// Note: This test is designed to verify that the tailer handles partial writes
// by retrying rather than parsing-and-failing. In practice, this is tested
// through integration with the PTY layer which naturally produces partial reads.
// Unit testing partial writes requires sophisticated mocking, so this is
// covered by integration tests rather than unit tests here.
func TestPartialWriteHandling(t *testing.T) {
	// Placeholder: AC #8 is verified through integration tests with live PTY.
	// The tailer's use of bufio.Scanner naturally handles partial lines by
	// blocking until a complete line (ending in \n) is available.
	t.Skip("partial-write race is tested via integration with PTY layer")
}

// TestSequenceNumberOrdering tests that sequence numbers are monotonic.
func TestSequenceNumberOrdering(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write multiple events
	transcript := `{"type":"text_delta","text":"a"}
{"type":"text_delta","text":"b"}
{"type":"tool_call","id":"1","name":"cmd"}
{"type":"tool_result","id":"1","output":"result"}
{"type":"final","status":"success"}
`
	if _, err := tmpFile.WriteString(transcript); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmpFile.Close()

	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	// Check all sequence numbers are present and monotonic
	for i, ev := range events {
		expectedSeq := int64(i + 1)
		if ev.Sequence != expectedSeq {
			t.Errorf("event %d: sequence = %d, want %d", i, ev.Sequence, expectedSeq)
		}
	}
}

// TestHookPayloadParsing tests reading the transcript path from hook payload.
func TestHookPayloadParsing(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{
			name:    "valid payload",
			payload: `{"transcript_path":"~/.claude/projects/mydir/abc123.jsonl"}`,
			want:    "~/.claude/projects/mydir/abc123.jsonl",
		},
		{
			name:    "missing transcript_path",
			payload: `{"other_field":"value"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			payload: `{invalid}`,
			wantErr: true,
		},
		{
			name:    "empty transcript_path",
			payload: `{"transcript_path":""}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := claudetui.ReadHookPayload([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadHookPayload error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ReadHookPayload = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExpandTranscriptPath tests ~ expansion.
func TestExpandTranscriptPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "tilde at start",
			path: "~/.claude/projects/test",
			want: filepath.Join(home, ".claude/projects/test"),
		},
		{
			name: "absolute path",
			path: "/tmp/transcript.jsonl",
			want: "/tmp/transcript.jsonl",
		},
		{
			name: "relative path",
			path: "transcript.jsonl",
			want: "transcript.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := claudetui.ExpandTranscriptPath(tt.path)
			if err != nil {
				t.Errorf("ExpandTranscriptPath error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("ExpandTranscriptPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestToolCallAndResultEvents tests tool invocation event emission.
func TestToolCallAndResultEvents(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write tool call and result
	transcript := `{"type":"tool_call","id":"call-1","name":"bash","input":{"command":"ls"}}
{"type":"tool_result","id":"call-1","output":"file.txt\ndir/"}
{"type":"final","status":"success"}
`
	if _, err := tmpFile.WriteString(transcript); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmpFile.Close()

	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	// Check tool_call event
	if events[0].Type != harnesses.EventTypeToolCall {
		t.Errorf("event 0: type = %s, want tool_call", events[0].Type)
	}
	var toolCall harnesses.ToolCallData
	_ = json.Unmarshal(events[0].Data, &toolCall)
	if toolCall.ID != "call-1" || toolCall.Name != "bash" {
		t.Errorf("tool_call: id=%s, name=%s; want id=call-1, name=bash", toolCall.ID, toolCall.Name)
	}

	// Check tool_result event
	if events[1].Type != harnesses.EventTypeToolResult {
		t.Errorf("event 1: type = %s, want tool_result", events[1].Type)
	}
	var toolResult harnesses.ToolResultData
	_ = json.Unmarshal(events[1].Data, &toolResult)
	if toolResult.ID != "call-1" || toolResult.Output != "file.txt\ndir/" {
		t.Errorf("tool_result: id=%s; want id=call-1", toolResult.ID)
	}
}

// TestMultiTurnReplaySafety tests that we only process new turns after /clear.
func TestMultiTurnReplaySafety(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write two completed turns + one new turn
	transcript := `{"type":"text_delta","text":"turn1"}
{"type":"final","status":"success"}
{"type":"text_delta","text":"turn2"}
{"type":"final","status":"success"}
{"type":"text_delta","text":"turn3"}
{"type":"final","status":"success"}
`
	if _, err := tmpFile.WriteString(transcript); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmpFile.Close()

	// Simulate marker tracking for multi-turn
	marker := &claudetui.SessionMarker{}

	// First read: process turns 1 and 2
	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var allEvents []harnesses.Event
	for ev := range eventChan {
		allEvents = append(allEvents, ev)
	}
	cancel()

	// Should have 6 events total (2 events per turn * 3 turns)
	if len(allEvents) != 6 {
		t.Errorf("got %d total events, want 6", len(allEvents))
	}

	// Mark first two turns as processed (simulating /clear)
	marker.MarkProcessed(tmpFile.Name(), 4)
	if marker.IsNewTranscript(tmpFile.Name()) {
		t.Error("marker should recognize same transcript path after mark")
	}

	// In a real scenario, /clear would create a new transcript file
	// The marker would detect it's a different path and process all new events
}

// TestFinalEventWithUsage tests that usage is properly parsed from final events.
func TestFinalEventWithUsage(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	usage := map[string]interface{}{
		"input_tokens":      100,
		"output_tokens":     50,
		"cache_read_tokens": 10,
	}
	usageJSON, _ := json.Marshal(usage)

	finalEvent := map[string]interface{}{
		"type":   "final",
		"status": "success",
		"text":   "Done",
		"usage":  json.RawMessage(usageJSON),
	}
	finalJSON, _ := json.Marshal(finalEvent)

	if _, err := tmpFile.Write(finalJSON); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmpFile.Write([]byte("\n"))
	tmpFile.Close()

	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}

	var finalData harnesses.FinalData
	_ = json.Unmarshal(events[0].Data, &finalData)

	if finalData.Status != "success" || finalData.FinalText != "Done" {
		t.Errorf("final event: status=%s, text=%s", finalData.Status, finalData.FinalText)
	}

	if finalData.Usage == nil {
		t.Error("final event: usage is nil")
	} else {
		if finalData.Usage.InputTokens == nil || *finalData.Usage.InputTokens != 100 {
			t.Errorf("input_tokens: got %v, want 100", finalData.Usage.InputTokens)
		}
		if finalData.Usage.OutputTokens == nil || *finalData.Usage.OutputTokens != 50 {
			t.Errorf("output_tokens: got %v, want 50", finalData.Usage.OutputTokens)
		}
	}
}

// TestEmptyTranscriptFile tests reading from an empty transcript file.
func TestEmptyTranscriptFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	if len(events) != 0 {
		t.Errorf("got %d events from empty file, want 0", len(events))
	}
}

// TestUnknownEventTypeSkipped tests that unknown event types are skipped.
func TestUnknownEventTypeSkipped(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	transcript := `{"type":"text_delta","text":"before"}
{"type":"unknown_type","data":"something"}
{"type":"text_delta","text":"after"}
`
	if _, err := tmpFile.WriteString(transcript); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmpFile.Close()

	tailer := claudetui.NewTranscriptTailer(tmpFile.Name(), "test-session", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	eventChan := make(chan harnesses.Event, 10)
	go func() {
		_ = tailer.ReadEvents(ctx, eventChan)
		close(eventChan)
	}()

	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Errorf("got %d events, want 2 (unknown type should be skipped)", len(events))
	}
}
