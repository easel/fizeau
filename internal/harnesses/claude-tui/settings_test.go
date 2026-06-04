package claudetui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestSettingsRealHookSchema proves composeSettingsJSON emits Claude Code's
// REAL hook schema:
//
//	{"hooks":{"<Event>":[{"matcher":"...","hooks":[{"type":"command","command":"..."}]}]}}
//
// NOT the old flat {"command","shell"} shape the prior stub emitted.
func TestSettingsRealHookSchema(t *testing.T) {
	hooks := buildHookConfigs("/tmp/hookdir", "/tmp/hookdir/stop.json", "abc123")
	jsonStr, _ := composeSettingsJSON(hooks, nil)

	var root struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
		t.Fatalf("settings JSON does not decode: %v", err)
	}

	for _, event := range []string{"Stop", "PreToolUse", "PostToolUse"} {
		raw, ok := root.Hooks[event]
		if !ok {
			t.Errorf("missing hook event %q", event)
			continue
		}
		// Each event maps to an ARRAY of matcher groups.
		var groups []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &groups); err != nil {
			t.Errorf("event %q is not an array of matcher groups: %v", event, err)
			continue
		}
		if len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Errorf("event %q has no command hooks", event)
			continue
		}
		if groups[0].Hooks[0].Type != "command" {
			t.Errorf("event %q hook type = %q, want command", event, groups[0].Hooks[0].Type)
		}
		if groups[0].Hooks[0].Command == "" {
			t.Errorf("event %q command is empty", event)
		}
	}

	// The old flat schema must NOT appear.
	if strings.Contains(jsonStr, `"shell"`) {
		t.Errorf("settings JSON still contains old flat {command,shell} shape: %s", jsonStr)
	}

	// The Stop hook command must embed the per-turn nonce and the payload path.
	stopRaw := root.Hooks["Stop"]
	if !strings.Contains(string(stopRaw), "abc123") {
		t.Errorf("Stop hook does not embed nonce: %s", stopRaw)
	}
	if !strings.Contains(string(stopRaw), "/tmp/hookdir/stop.json") {
		t.Errorf("Stop hook does not target the payload path: %s", stopRaw)
	}
}

// TestHookMatcherIsMatchAll proves F3: PreToolUse/PostToolUse/Stop all use the
// documented match-all matcher "" (a NON-empty matcher is a tool-name regex in
// Claude Code 2.1.x; the prior "*" only matched as a zero-width regex). Using
// "" gives schema parity and unconditional firing on every tool.
func TestHookMatcherIsMatchAll(t *testing.T) {
	hooks := buildHookConfigs("/tmp/hookdir", "/tmp/hookdir/stop.json", "abc123")
	for _, event := range []string{"PreToolUse", "PostToolUse", "Stop"} {
		groups, ok := hooks[event]
		if !ok || len(groups) == 0 {
			t.Errorf("event %q missing hook group", event)
			continue
		}
		if groups[0].Matcher != "" {
			t.Errorf("event %q matcher = %q, want \"\" (documented match-all)", event, groups[0].Matcher)
		}
	}
}

// TestReadStopHookPayloadNonceMatch proves the Stop-payload reader only returns
// a transcript path when the payload nonce matches, and reports not-complete
// otherwise.
func TestReadStopHookPayloadNonceMatch(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/stop.json"

	// Missing file -> not complete.
	if _, ok := readStopHookPayloadNonce(path, "n1"); ok {
		t.Error("missing payload reported complete")
	}

	// Wrong nonce -> not complete.
	writeFile(t, path, `{"nonce":"OTHER","transcript_path":"/x.jsonl"}`)
	if _, ok := readStopHookPayloadNonce(path, "n1"); ok {
		t.Error("mismatched nonce reported complete")
	}

	// Matching nonce -> complete with path.
	writeFile(t, path, `{"nonce":"n1","transcript_path":"/x.jsonl"}`)
	got, ok := readStopHookPayloadNonce(path, "n1")
	if !ok || got != "/x.jsonl" {
		t.Errorf("matching nonce: got %q ok=%v, want /x.jsonl true", got, ok)
	}

	// Matching nonce but empty path -> not complete.
	writeFile(t, path, `{"nonce":"n1","transcript_path":""}`)
	if _, ok := readStopHookPayloadNonce(path, "n1"); ok {
		t.Error("empty transcript path reported complete")
	}
}

// TestNewTurnNonceUnique proves nonces differ per call.
func TestNewTurnNonceUnique(t *testing.T) {
	a := newTurnNonce()
	b := newTurnNonce()
	if a == "" || b == "" {
		t.Fatal("empty nonce")
	}
	if a == b {
		t.Errorf("nonces collided: %q == %q", a, b)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
