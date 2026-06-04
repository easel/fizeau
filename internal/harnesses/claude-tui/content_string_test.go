package claudetui

import (
	"encoding/json"
	"testing"
)

// TestMessageContentAcceptsStringOrArray pins that a transcript message's
// content decodes from BOTH the array-of-blocks form (assistant turns) and the
// plain-string form (simple text messages) Claude Code emits. The string form
// was previously dropped with "cannot unmarshal string into []transcriptBlock",
// skipping the line and losing its text (observed in the live smoke).
func TestMessageContentAcceptsStringOrArray(t *testing.T) {
	// array form
	var arr transcriptMessage
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"Write"}]}`), &arr); err != nil {
		t.Fatalf("array content: %v", err)
	}
	if len(arr.Content) != 2 || arr.Content[0].Type != "text" || arr.Content[1].Type != "tool_use" {
		t.Fatalf("array content parsed wrong: %#v", arr.Content)
	}

	// string form -> single text block
	var str transcriptMessage
	if err := json.Unmarshal([]byte(`{"role":"user","content":"just some text"}`), &str); err != nil {
		t.Fatalf("string content: %v", err)
	}
	if len(str.Content) != 1 || str.Content[0].Type != "text" || str.Content[0].Text != "just some text" {
		t.Fatalf("string content should become one text block, got: %#v", str.Content)
	}

	// a full transcript line with string content must not error (no skipped line)
	var line transcriptLine
	if err := json.Unmarshal([]byte(`{"type":"user","message":{"role":"user","content":"hello"}}`), &line); err != nil {
		t.Fatalf("transcript line with string content errored (would be skipped live): %v", err)
	}
	if line.Message == nil || len(line.Message.Content) != 1 || line.Message.Content[0].Text != "hello" {
		t.Fatalf("line.Message.Content = %#v", line.Message)
	}
}
