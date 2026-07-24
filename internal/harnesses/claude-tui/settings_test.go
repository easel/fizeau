package claudetui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSettingsRealHookSchema proves composeSettingsJSON emits Claude Code's
// REAL hook schema:
//
//	{"hooks":{"<Event>":[{"matcher":"...","hooks":[{"type":"command","command":"..."}]}]}}
//
// NOT the old flat {"command","shell"} shape the prior stub emitted.
func TestSettingsRealHookSchema(t *testing.T) {
	const nonce = "00112233445566778899aabbccddeeff"
	hooks := buildHookConfigs("/tmp/hookdir", "/tmp/hookdir/stop.json", nonce)
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
	if !strings.Contains(string(stopRaw), nonce) {
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
	hooks := buildHookConfigs("/tmp/hookdir", "/tmp/hookdir/stop.json", "00112233445566778899aabbccddeeff")
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
	const nonce = "00112233445566778899aabbccddeeff"

	// Missing file -> not complete.
	if _, ok := readStopHookPayloadNonce(path, nonce); ok {
		t.Error("missing payload reported complete")
	}

	// Wrong nonce -> not complete.
	writeFile(t, path, `{"nonce":"OTHER","transcript_path":"/x.jsonl"}`)
	if _, ok := readStopHookPayloadNonce(path, nonce); ok {
		t.Error("mismatched nonce reported complete")
	}

	// Matching nonce -> complete with path.
	writeFile(t, path, `{"nonce":"`+nonce+`","transcript_path":"/x.jsonl"}`)
	got, ok := readStopHookPayloadNonce(path, nonce)
	if !ok || got != "/x.jsonl" {
		t.Errorf("matching nonce: got %q ok=%v, want /x.jsonl true", got, ok)
	}

	// Matching nonce but empty path -> not complete.
	writeFile(t, path, `{"nonce":"`+nonce+`","transcript_path":""}`)
	if _, ok := readStopHookPayloadNonce(path, nonce); ok {
		t.Error("empty transcript path reported complete")
	}

	// An invalid expected nonce can never authorize completion, even if the
	// payload carries the same invalid value.
	writeFile(t, path, `{"nonce":"n1","transcript_path":"/x.jsonl"}`)
	if _, ok := readStopHookPayloadNonce(path, "n1"); ok {
		t.Error("invalid expected nonce reported complete")
	}
	if command := buildHookConfigs(dir, path, "n1")["Stop"][0].Hooks[0].Command; command != "exit 1" {
		t.Fatalf("invalid nonce Stop command = %q, want fail-closed exit", command)
	}
}

// TestReadPromptAckNonceMatch proves the ack-payload reader only reports
// acknowledged when the file exists and carries the exact matching nonce,
// mirroring TestReadStopHookPayloadNonceMatch's coverage for the Stop reader.
func TestReadPromptAckNonceMatch(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/prompt-ack-payload.json"
	const nonce = "00112233445566778899aabbccddeeff"

	// Missing file -> not acknowledged.
	if readPromptAckNonce(path, nonce) {
		t.Error("missing payload reported acknowledged")
	}

	// Wrong nonce -> not acknowledged.
	writeFile(t, path, `{"nonce":"OTHER"}`)
	if readPromptAckNonce(path, nonce) {
		t.Error("mismatched nonce reported acknowledged")
	}

	// Matching nonce -> acknowledged.
	writeFile(t, path, `{"nonce":"`+nonce+`"}`)
	if !readPromptAckNonce(path, nonce) {
		t.Error("matching nonce reported not acknowledged")
	}

	// Malformed JSON -> not acknowledged.
	writeFile(t, path, `not json`)
	if readPromptAckNonce(path, nonce) {
		t.Error("malformed payload reported acknowledged")
	}

	// An invalid expected nonce can never authorize an ack, even if the
	// payload carries the same invalid value.
	writeFile(t, path, `{"nonce":"n1"}`)
	if readPromptAckNonce(path, "n1") {
		t.Error("invalid expected nonce reported acknowledged")
	}
	if command := buildHookConfigs(dir, dir+"/stop.json", "n1")["UserPromptSubmit"][0].Hooks[0].Command; command != "exit 1" {
		t.Fatalf("invalid nonce UserPromptSubmit command = %q, want fail-closed exit", command)
	}
}

// TestPromptAckHookRunsForRealAndPublishesAtomically executes the ACTUAL
// UserPromptSubmit hook command (matching TestStopHookPayloadPublishedAtomically's
// approach for Stop) to prove: adversarial stdin is discarded harmlessly (no
// field of Claude Code's own hook payload is trusted), the destination
// directory may contain shell metacharacters without command injection, and
// publication is atomic (temp file + rename in the same directory).
func TestPromptAckHookRunsForRealAndPublishesAtomically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-executed hook command requires the supported POSIX PTY surface")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "hooks ' $(touch injected) `touch injected2`")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const nonce = "00112233445566778899aabbccddeeff"
	hooks := buildHookConfigs(dir, filepath.Join(dir, "stop.json"), nonce)
	ackCommand := hooks["UserPromptSubmit"][0].Hooks[0].Command

	if !strings.Contains(ackCommand, "dest="+shellSingleQuote(promptAckPayloadPath(dir))) ||
		!strings.Contains(ackCommand, `tmp="${dest}.tmp.$$"`) ||
		!strings.Contains(ackCommand, `mv -f "$tmp"`) {
		t.Fatalf("UserPromptSubmit command lacks same-directory atomic publication: %s", ackCommand)
	}

	// Adversarial stdin (Claude Code's real hook payload, which the ack
	// command must discard rather than parse) must not affect the outcome.
	adversarial := "{\"session_id\":\"$(touch pwned)\",\"prompt\":\"`touch pwned2`\"}"
	cmd := exec.Command("sh", "-c", ackCommand)
	cmd.Dir = base
	cmd.Stdin = strings.NewReader(adversarial)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run UserPromptSubmit hook command: %v: %s", err, output)
	}

	for _, injected := range []string{"pwned", "pwned2", "injected", "injected2"} {
		if _, err := os.Stat(filepath.Join(base, injected)); err == nil {
			t.Fatalf("adversarial stdin or directory name achieved command injection: %s exists", injected)
		}
	}

	if !readPromptAckNonce(promptAckPayloadPath(dir), nonce) {
		t.Fatal("ack file was not published with the expected nonce after running the real hook command")
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp.") {
				t.Errorf("temporary ack file left behind: %s", e.Name())
			}
		}
	}
}

func TestStopHookPayloadPublishedAtomically(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "hooks ' $(touch injected) `touch injected2`")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(dir, "stop.json")
	const nonce = "00112233445566778899aabbccddeeff"
	hooks := buildHookConfigs(dir, stopPath, nonce)
	stopCommand := hooks["Stop"][0].Hooks[0].Command
	if !strings.Contains(stopCommand, "dest="+shellSingleQuote(stopPath)) ||
		!strings.Contains(stopCommand, `tmp="${dest}.tmp.$$"`) {
		t.Fatalf("Stop command lacks same-directory temporary path: %s", stopCommand)
	}
	if !strings.Contains(stopCommand, `mv -f "$tmp"`) {
		t.Fatalf("Stop command lacks atomic rename publication: %s", stopCommand)
	}
	if runtime.GOOS == "windows" {
		t.Skip("atomic Stop publication requires the supported POSIX PTY surface")
	}
	runStop := func(input string) ([]byte, error) {
		command := exec.Command("sh", "-c", stopCommand)
		command.Dir = base
		command.Stdin = strings.NewReader(input)
		return command.CombinedOutput()
	}

	// Seed one complete payload so the reader can prove repeated observations
	// before concurrent rewrites begin.
	wantPath := filepath.Join(dir, "transcript-seed.jsonl")
	seedInput, err := json.Marshal(map[string]string{"transcript_path": wantPath})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := runStop(string(seedInput)); err != nil {
		t.Fatalf("seed Stop payload: %v: %s", err, output)
	}

	done := make(chan struct{})
	readerDone := make(chan struct{})
	readerStarted := make(chan struct{})
	readerErr := make(chan error, 1)
	var successfulReads atomic.Int64
	go func() {
		defer close(readerDone)
		close(readerStarted)
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(stopPath)
			if err != nil {
				if os.IsNotExist(err) {
					time.Sleep(100 * time.Microsecond)
					continue
				}
				select {
				case readerErr <- err:
				default:
				}
				return
			}
			var payload struct {
				Nonce          string `json:"nonce"`
				TranscriptPath string `json:"transcript_path"`
			}
			if err := json.Unmarshal(data, &payload); err != nil ||
				(payload.Nonce == nonce && payload.TranscriptPath == "") {
				if err == nil {
					err = fmt.Errorf("matching nonce published with empty transcript path")
				}
				select {
				case readerErr <- err:
				default:
				}
				return
			}
			successfulReads.Add(1)
		}
	}()
	<-readerStarted
	waitForReads := func(minimum int64) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for successfulReads.Load() < minimum && time.Now().Before(deadline) {
			time.Sleep(100 * time.Microsecond)
		}
		if got := successfulReads.Load(); got < minimum {
			close(done)
			<-readerDone
			t.Fatalf("atomic reader completed %d reads, want at least %d", got, minimum)
		}
	}
	waitForReads(20)
	readsBeforeWriters := successfulReads.Load()

	for i := 0; i < 40; i++ {
		transcriptPath := filepath.Join(dir, fmt.Sprintf("transcript-%d-\"-\\-$()-`.jsonl", i))
		wantPath = transcriptPath
		input, err := json.Marshal(map[string]string{"transcript_path": transcriptPath})
		if err != nil {
			close(done)
			t.Fatal(err)
		}
		if output, err := runStop(string(input)); err != nil {
			close(done)
			t.Fatalf("run Stop command: %v: %s", err, output)
		}
	}
	waitForReads(readsBeforeWriters + 1)

	invalidPayloads := map[string]string{
		"empty path":               `{"transcript_path":""}`,
		"string and nested decoys": `{"transcript_path":"","other":"\\\"transcript_path\\\":\\\"decoy\\\"","nested":{"transcript_path":"decoy"}}`,
		"duplicate root key":       `{"transcript_path":"/decoy","transcript_path":""}`,
	}
	for name, input := range invalidPayloads {
		if output, err := runStop(input); err == nil {
			close(done)
			<-readerDone
			t.Fatalf("%s was published: %s", name, output)
		}
	}
	close(done)
	<-readerDone
	select {
	case err := <-readerErr:
		t.Fatal(err)
	default:
	}
	if matches, err := filepath.Glob(stopPath + ".tmp.*"); err != nil || len(matches) != 0 {
		t.Fatalf("temporary payloads after publication = %v, err=%v", matches, err)
	}
	if path, ok := readStopHookPayloadNonce(stopPath, nonce); !ok || path != wantPath {
		t.Fatalf("final payload path=%q ok=%v, want %q true", path, ok, wantPath)
	}
	for _, sentinel := range []string{"injected", "injected2"} {
		if _, err := os.Stat(filepath.Join(base, sentinel)); !os.IsNotExist(err) {
			t.Fatalf("unsafe shell path executed sentinel %q: %v", sentinel, err)
		}
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
	if !isTurnNonce(a) || !isTurnNonce(b) {
		t.Fatalf("nonces do not match the fixed lowercase-hex contract: %q %q", a, b)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
