package claudetui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/pty/session"
	"github.com/easel/fizeau/internal/pty/terminal"
)

// bypassConsentCassetteDir is the checked-in real Claude Code consent-screen
// capture (fizeau-70cd12f9). Files are raw PTY bytes; meta.json carries the
// captured_at / claude_version markers required by the acceptance criteria.
const bypassConsentCassetteDir = "testdata/bypass_consent"

type bypassConsentCassetteMeta struct {
	ID             string `json:"id"`
	ClaudeVersion  string `json:"claude_version"`
	CapturedAt     string `json:"captured_at"`
	PermissionMode string `json:"permission_mode"`
	Terminal       struct {
		Rows int    `json:"rows"`
		Cols int    `json:"cols"`
		Term string `json:"term"`
	} `json:"terminal"`
	Files map[string]struct {
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	} `json:"files"`
}

func loadBypassConsentCassette(t *testing.T) (meta bypassConsentCassetteMeta, initial, highlightAccept []byte) {
	t.Helper()
	metaPath := filepath.Join(bypassConsentCassetteDir, "meta.json")
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read cassette meta: %v", err)
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("parse cassette meta: %v", err)
	}
	if strings.TrimSpace(meta.ClaudeVersion) == "" {
		t.Fatal("cassette meta missing claude_version marker")
	}
	if strings.TrimSpace(meta.CapturedAt) == "" {
		t.Fatal("cassette meta missing captured_at marker")
	}

	initial = mustReadCassetteRaw(t, meta, "consent_initial.raw")
	highlightAccept = mustReadCassetteRaw(t, meta, "consent_highlight_accept.raw")
	return meta, initial, highlightAccept
}

func mustReadCassetteRaw(t *testing.T, meta bypassConsentCassetteMeta, name string) []byte {
	t.Helper()
	path := filepath.Join(bypassConsentCassetteDir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	info, ok := meta.Files[name]
	if !ok {
		t.Fatalf("cassette meta missing file entry %q", name)
	}
	if info.Bytes != 0 && len(raw) != info.Bytes {
		t.Fatalf("%s size = %d, meta says %d", name, len(raw), info.Bytes)
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if info.SHA256 != "" && got != info.SHA256 {
		t.Fatalf("%s sha256 = %s, meta says %s", name, got, info.SHA256)
	}
	return raw
}

// TestBypassConsentCassetteDetectsAndSelects feeds the real 2.1.x consent PTY
// capture through vt10x (not naive ANSI strip) and asserts the structural
// detectors fire: decision screen, unique "Yes, I accept" option, and the
// highlight-accept delta.
func TestBypassConsentCassetteDetectsAndSelects(t *testing.T) {
	meta, initial, highlightAccept := loadBypassConsentCassette(t)
	t.Logf("replaying cassette %s captured_at=%s claude_version=%q",
		meta.ID, meta.CapturedAt, meta.ClaudeVersion)

	if !strings.Contains(meta.ClaudeVersion, "2.1.") {
		t.Fatalf("cassette claude_version %q does not look like a 2.1.x Claude Code release", meta.ClaudeVersion)
	}

	rows, cols := meta.Terminal.Rows, meta.Terminal.Cols
	if rows <= 0 {
		rows = 40
	}
	if cols <= 0 {
		cols = 120
	}
	emu := terminal.New(terminal.Size{Rows: rows, Cols: cols})
	frame, err := emu.Feed(initial)
	if err != nil {
		t.Fatalf("feed initial cassette: %v", err)
	}

	if !detectBypassDecisionScreen(frame) {
		t.Fatalf("detectBypassDecisionScreen=false on real cassette frame; lines=%v", nonEmptyLines(frame))
	}
	accept, ok := bypassAcceptOption(frame)
	if !ok {
		t.Fatalf("bypassAcceptOption failed to uniquely find accept; options=%v lines=%v",
			parseNumberedOptions(frame), nonEmptyLines(frame))
	}
	if !strings.Contains(strings.ToLower(accept.text), "accept") {
		t.Fatalf("accept option text = %q, want it to contain accept", accept.text)
	}
	if accept.number != 2 {
		t.Fatalf("accept option number = %d, want 2 (real 2.1.x layout)", accept.number)
	}
	if accept.highlighted {
		t.Fatal("initial cassette has accept highlighted; expected default highlight on No, exit")
	}
	if got := highlightedOptionNumber(frame); got != 1 {
		t.Fatalf("initial highlight = %d, want 1 (No, exit is the dangerous default)", got)
	}

	// Ink moves the highlight with a small delta; feed it into the same emulator.
	frame2, err := emu.Feed(highlightAccept)
	if err != nil {
		t.Fatalf("feed highlight-accept delta: %v", err)
	}
	if !detectBypassDecisionScreen(frame2) {
		t.Fatalf("detectBypassDecisionScreen=false after highlight delta; lines=%v", nonEmptyLines(frame2))
	}
	accept2, ok := bypassAcceptOption(frame2)
	if !ok {
		t.Fatalf("bypassAcceptOption failed after highlight delta; lines=%v", nonEmptyLines(frame2))
	}
	if !accept2.highlighted {
		t.Fatalf("accept option not highlighted after Down delta; highlight=%d lines=%v",
			highlightedOptionNumber(frame2), nonEmptyLines(frame2))
	}
	if got := highlightedOptionNumber(frame2); got != accept2.number {
		t.Fatalf("highlight=%d accept.number=%d after Down delta", got, accept2.number)
	}
}

// TestBypassConsentCassetteDriverAccepts drives runTurnOver against the real
// cassette bytes: Down before Enter, no paste while the dialog is visible,
// screen-advanced verification, bypass_consent_accepted audit event, and a
// successful final after the ready prompt appears.
func TestBypassConsentCassetteDriverAccepts(t *testing.T) {
	meta, initial, highlightAccept := loadBypassConsentCassette(t)
	t.Logf("driver replay cassette %s claude_version=%q", meta.ID, meta.ClaudeVersion)

	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := cassetteTurnNonce("bypass-consent-cassette")
	transcript := writeCassetteTranscript(t, dir)

	f := newCassetteFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pasteWhileDialogVisible := make(chan bool, 1)
	go func() {
		// Real consent screen: highlight starts on "No, exit".
		f.push(initial)
		// Driver must step Down before confirming.
		for f.keyCount(session.KeyDown) == 0 {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
		// Real Ink delta moves the highlight onto "Yes, I accept".
		f.push(highlightAccept)
		for f.keyCount(session.KeyEnter) == 0 {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
		pasteWhileDialogVisible <- f.sawBracketedPaste()
		// Screen advanced: consent cleared, ready prompt present.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
		writeCassetteStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir, Permissions: "unrestricted"}
	events := RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	if paste := <-pasteWhileDialogVisible; paste {
		t.Error("task was pasted while the real bypass consent dialog was still visible")
	}

	keys := f.keysSnapshot()
	downIdx, enterIdx := -1, -1
	for i, k := range keys {
		if k == session.KeyDown && downIdx == -1 {
			downIdx = i
		}
		if k == session.KeyEnter && enterIdx == -1 {
			enterIdx = i
		}
	}
	if downIdx == -1 {
		t.Fatalf("driver did not send Down against real cassette; keys=%v", keys)
	}
	if enterIdx == -1 {
		t.Fatalf("driver did not confirm with Enter against real cassette; keys=%v", keys)
	}
	if downIdx > enterIdx {
		t.Fatalf("Enter (idx %d) preceded Down (idx %d); keys=%v", enterIdx, downIdx, keys)
	}
	if !f.sawBracketedPaste() {
		t.Error("task not delivered after real-cassette consent accepted")
	}
	if !cassetteHasProgressCode(events, "bypass_consent_accepted") {
		t.Error("missing bypass_consent_accepted audit progress event")
	}
	if status := cassetteFinalStatus(events); status != "success" {
		t.Fatalf("final status = %q, want success; events=%d", status, len(events))
	}
}

func nonEmptyLines(frame terminal.Frame) []string {
	var out []string
	for _, line := range frame.Text {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func cassetteTurnNonce(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:16])
}

func writeCassetteTranscript(t *testing.T, dir string) string {
	t.Helper()
	// Same terminal shape as stream_test.go's realTranscript: a completed
	// assistant end_turn so finalization succeeds offline.
	path := filepath.Join(dir, "transcript.jsonl")
	body := `{"type":"assistant","message":{"model":"claude-sonnet-4-6","id":"msg_cassette","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCassetteStopPayload(t *testing.T, path, nonce, transcript string) {
	t.Helper()
	hookDir := filepath.Dir(path)
	ack, _ := json.Marshal(map[string]string{"nonce": nonce})
	if err := os.WriteFile(promptAckPayloadPath(hookDir), ack, 0o644); err != nil {
		t.Fatalf("write prompt ack: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"nonce": nonce, "transcript_path": transcript})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write stop payload: %v", err)
	}
}

func cassetteHasProgressCode(events []harnesses.Event, code string) bool {
	for _, ev := range events {
		if ev.Type != harnesses.EventTypeProgress {
			continue
		}
		var w harnesses.FinalWarning
		if json.Unmarshal(ev.Data, &w) == nil && w.Code == code {
			return true
		}
	}
	return false
}

func cassetteFinalStatus(events []harnesses.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != harnesses.EventTypeFinal {
			continue
		}
		var fd harnesses.FinalData
		if json.Unmarshal(events[i].Data, &fd) == nil {
			return fd.Status
		}
	}
	return ""
}

// cassetteFakePTY is a minimal scripted PTY for cassette driver tests.
type cassetteFakePTY struct {
	out      chan session.OutputChunk
	mu       sync.Mutex
	keys     []session.Key
	sent     [][]byte
	killed   bool
	exit     session.ExitStatus
	waitOnce sync.Once
	waiting  chan struct{}
	closeOut sync.Once
}

func newCassetteFakePTY() *cassetteFakePTY {
	return &cassetteFakePTY{out: make(chan session.OutputChunk, 64), waiting: make(chan struct{})}
}

func (f *cassetteFakePTY) Output() <-chan session.OutputChunk { return f.out }
func (f *cassetteFakePTY) SendBytes(b []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, append([]byte(nil), b...))
	f.mu.Unlock()
	return nil
}
func (f *cassetteFakePTY) SendKey(k session.Key) error {
	f.mu.Lock()
	f.keys = append(f.keys, k)
	f.mu.Unlock()
	return nil
}
func (f *cassetteFakePTY) Size() session.Size { return session.Size{Rows: 40, Cols: 120} }
func (f *cassetteFakePTY) Kill() error {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	return nil
}
func (f *cassetteFakePTY) Wait() session.ExitStatus {
	f.waitOnce.Do(func() { close(f.waiting) })
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exit
}
func (f *cassetteFakePTY) push(b []byte) { f.out <- session.OutputChunk{Bytes: b} }
func (f *cassetteFakePTY) keyCount(k session.Key) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, got := range f.keys {
		if got == k {
			n++
		}
	}
	return n
}
func (f *cassetteFakePTY) sawBracketedPaste() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.sent {
		if len(b) >= 6 && string(b[:6]) == "\x1b[200~" {
			return true
		}
	}
	return false
}
func (f *cassetteFakePTY) keysSnapshot() []session.Key {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]session.Key(nil), f.keys...)
}
