package claudetui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
	"github.com/easel/fizeau/internal/pty/session"
)

// fakePTY is a scripted, in-memory PTY for driving runTurnOver with no live
// binary, no network, and no real PTY. Tests push output frames in and read
// back the keys/bytes the turn loop sent.
type fakePTY struct {
	out chan session.OutputChunk

	mu       sync.Mutex
	keys     []session.Key
	sent     [][]byte
	killed   bool
	onPrompt func() // invoked when a bracketed-paste prompt is observed
}

func newFakePTY() *fakePTY {
	return &fakePTY{out: make(chan session.OutputChunk, 64)}
}

func (f *fakePTY) Output() <-chan session.OutputChunk { return f.out }

func (f *fakePTY) SendBytes(b []byte) error {
	f.mu.Lock()
	cp := append([]byte(nil), b...)
	f.sent = append(f.sent, cp)
	isPaste := len(b) >= 6 && string(b[:6]) == "\x1b[200~"
	onPrompt := f.onPrompt
	f.mu.Unlock()
	if isPaste && onPrompt != nil {
		onPrompt()
	}
	return nil
}

func (f *fakePTY) SendKey(k session.Key) error {
	f.mu.Lock()
	f.keys = append(f.keys, k)
	f.mu.Unlock()
	return nil
}

func (f *fakePTY) Size() session.Size { return session.Size{Rows: 50, Cols: 220} }

func (f *fakePTY) Kill() error {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	return nil
}

func (f *fakePTY) push(b []byte) { f.out <- session.OutputChunk{Bytes: b} }

func (f *fakePTY) wasKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

func (f *fakePTY) keyCount(k session.Key) int {
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

func (f *fakePTY) sawBracketedPaste() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.sent {
		if len(b) >= 6 && string(b[:6]) == "\x1b[200~" {
			return true
		}
	}
	return false
}

// writeStopPayload writes a Stop-hook payload carrying the nonce + transcript path.
func writeStopPayload(t *testing.T, path, nonce, transcript string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nonce": nonce, "transcript_path": transcript})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write stop payload: %v", err)
	}
}

// TestRunTurnFolderTrustAnsweredWithEnter proves the turn loop renders the
// screen through the vt10x emulator (NOT naive ANSI stripping) and answers the
// folder-trust dialog with a single Enter (default = "Yes, I trust this
// folder"). Naive ANSI stripping would not match the cursor-positioned dialog.
func TestRunTurnFolderTrustAnsweredWithEnter(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-trust"

	// Minimal but valid transcript so completion has something to read.
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// Render the folder-trust dialog using cursor-position escapes so the
		// text is only legible after vt10x rendering. \x1b[H homes the cursor.
		f.push([]byte("\x1b[H\x1b[2J"))
		f.push([]byte("\x1b[3;5HIs this a project you created or one you trust?"))
		f.push([]byte("\x1b[5;7H1. Yes, I trust this folder"))
		// Wait for the Enter answer, then show the prompt and finish.
		for f.keyCount(session.KeyEnter) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// Wait for the prompt to be pasted, then signal Stop.
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "hello", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		200*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	if f.keyCount(session.KeyEnter) == 0 {
		t.Fatal("folder-trust dialog was not answered with Enter")
	}
	if f.keyCount(session.KeyEnter) > 1 {
		t.Errorf("Enter pressed %d times, want exactly 1 (trust answered once)", f.keyCount(session.KeyEnter))
	}
	if !f.sawBracketedPaste() {
		t.Error("prompt was not delivered via bracketed paste")
	}
	assertExactlyOneFinal(t, events, "success")
}

// TestRunTurnCompletesOnStopNonceOnly proves completion is signaled ONLY by the
// Stop payload carrying the per-turn nonce. A ready prompt on screen (which
// fires mid-turn) must NOT complete the turn before the Stop hook fires.
func TestRunTurnCompletesOnStopNonceOnly(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-complete"
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completed := make(chan struct{})
	go func() {
		// Show a ready prompt early (mid-turn). This must NOT complete the turn.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		// Hold for a few poll cycles with no Stop payload; the loop must keep
		// running (no premature final).
		time.Sleep(120 * time.Millisecond)
		select {
		case <-completed:
			t.Errorf("turn completed before Stop hook fired")
		default:
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)
	close(completed)

	assertExactlyOneFinal(t, events, "success")
	// The transcript content must have flowed through (final text present).
	last := events[len(events)-1]
	var fin harnesses.FinalData
	_ = json.Unmarshal(last.Data, &fin)
	if fin.FinalText == "" {
		t.Error("final text empty; transcript was not read on Stop completion")
	}
}

// TestRunTurnIgnoresStaleStopNonce proves a Stop payload with a DIFFERENT nonce
// (e.g. left over from a prior prompt) does not complete the current turn.
func TestRunTurnIgnoresStaleStopNonce(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-current"
	transcript := writeTranscript(t, realTranscript)

	// Pre-existing STALE payload with the wrong nonce.
	writeStopPayload(t, stopPath, "nonce-OLD", transcript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		// Give the loop time to (wrongly) react to the stale payload.
		time.Sleep(120 * time.Millisecond)
		// Now write the correct-nonce payload to complete.
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	assertExactlyOneFinal(t, events, "success")
}

// TestRunTurnUnlinksPriorStopPayload proves the turn loop unlinks any prior
// Stop payload before sending the prompt, so a leftover same-nonce file from a
// crashed prior attempt cannot short-circuit completion before the prompt runs.
func TestRunTurnUnlinksPriorStopPayload(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-x"
	transcript := writeTranscript(t, realTranscript)

	// Stale payload with the SAME nonce, present before the turn starts.
	writeStopPayload(t, stopPath, nonce, transcript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	promptSent := make(chan struct{})
	f.onPrompt = func() {
		select {
		case <-promptSent:
		default:
			close(promptSent)
		}
	}

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// Wait until the prompt is actually delivered.
		<-promptSent
		// The stale payload must have been unlinked: confirm it's gone right
		// after the prompt was sent (before we re-write it).
		if _, err := os.Stat(stopPath); err == nil {
			// File still exists pre-rewrite -> unlink failed.
			t.Errorf("stale Stop payload was not unlinked before prompt send")
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "go", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	if !f.sawBracketedPaste() {
		t.Fatal("prompt was never sent; unlink path likely short-circuited")
	}
	assertExactlyOneFinal(t, events, "success")
}

// TestRunTurnEmitsMidTurnToolEventsBeforeFinal proves PreToolUse/PostToolUse
// payload files written DURING the turn surface as tool_call/tool_result
// ProgressEvents before the final event.
func TestRunTurnEmitsMidTurnToolEvents(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-tools"
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		// Write mid-turn tool payloads, then complete.
		_ = os.WriteFile(filepath.Join(hookDir, "tool-0001-pre.json"),
			[]byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_9","tool_input":{"command":"ls"}}`), 0o644)
		_ = os.WriteFile(filepath.Join(hookDir, "tool-0002-post.json"),
			[]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_9","tool_response":"ok"}`), 0o644)
		time.Sleep(60 * time.Millisecond) // allow at least one poll drain
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "run ls", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	finalIdx := -1
	for i, ev := range events {
		if ev.Type == harnesses.EventTypeFinal {
			finalIdx = i
		}
	}
	if finalIdx < 0 {
		t.Fatal("no final event")
	}
	var sawCall, sawResult bool
	for _, ev := range events[:finalIdx] {
		if ev.Type == harnesses.EventTypeToolCall {
			var d harnesses.ToolCallData
			_ = json.Unmarshal(ev.Data, &d)
			if d.ID == "toolu_9" {
				sawCall = true
			}
		}
		if ev.Type == harnesses.EventTypeToolResult {
			var d harnesses.ToolResultData
			_ = json.Unmarshal(ev.Data, &d)
			if d.ID == "toolu_9" {
				sawResult = true
			}
		}
	}
	if !sawCall {
		t.Error("mid-turn tool_call ProgressEvent not emitted before final")
	}
	if !sawResult {
		t.Error("mid-turn tool_result ProgressEvent not emitted before final")
	}
}

// TestRunTurnContextCancelEmitsCancelledFinal proves ctx cancellation kills the
// session and emits exactly one cancelled final.
func TestRunTurnContextCancelEmitsCancelledFinal(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, "n",
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	assertExactlyOneFinal(t, events, "cancelled")
}

// TestRunTurnTimeoutEmitsTimedOutFinal proves the turn timeout fires exactly
// one timed_out final when no Stop hook ever lands.
func TestRunTurnTimeoutEmitsTimedOutFinal(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, "n",
		50*time.Millisecond, 20*time.Millisecond, 150*time.Millisecond)

	assertExactlyOneFinal(t, events, "timed_out")
}

// TestRunTurnDocumentsRequestGaps proves req.Model/Reasoning/Permissions with
// no TUI affordance are surfaced as documented-gap progress warnings rather
// than silently dropped.
func TestRunTurnDocumentsRequestGaps(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-gap"
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{
		Prompt:      "hi",
		WorkDir:     dir,
		Model:       "claude-opus-4-6",
		Reasoning:   "high",
		Permissions: "safe",
	}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	var gapMsgs []string
	for _, ev := range events {
		if ev.Type != harnesses.EventTypeProgress {
			continue
		}
		var w harnesses.FinalWarning
		if err := json.Unmarshal(ev.Data, &w); err == nil && w.Code == "unsupported_request_field" {
			gapMsgs = append(gapMsgs, w.Message)
		}
	}
	if len(gapMsgs) != 3 {
		t.Fatalf("documented gaps = %d, want 3 (model, reasoning, permissions): %v", len(gapMsgs), gapMsgs)
	}
}

// sentContains reports how many recorded SendBytes payloads contain sub.
func (f *fakePTY) sentContains(sub []byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.sent {
		if bytes.Contains(b, sub) {
			n++
		}
	}
	return n
}

// TestRunTurnSingleOutputConsumerNoStolenChunks is the dedicated R7 regression
// test for the "single Output() consumer / no stolen chunks" requirement.
//
// It pushes a sequence of chunks where EACH chunk carries a DISTINCT Ink
// startup probe (DA1, DA2, DSR, XTVERSION, window-size 18t/19t). The turn loop's
// single Output() consumer feeds every chunk to startupProbe.Feed inline, which
// answers each probe with a distinct reply written back through SendBytes. The
// test asserts every probe reply appears EXACTLY ONCE.
//
// If a second concurrent Output() reader existed (a "stolen chunk"), at least
// one probe-bearing chunk would be routed to that competing goroutine instead
// of the probe-answering consumer, so its reply would be MISSING (count 0). The
// per-probe "answered at least once" assertions therefore directly prove that
// every Output() chunk reached the single probe-answering consumer and no chunk
// was stolen or dropped. (startupProbe.Feed accumulates its buffer, so a probe
// may legitimately be re-answered on later Feed calls; the load-bearing
// invariant a stolen chunk breaks is count == 0, not the exact repeat count.)
func TestRunTurnSingleOutputConsumerNoStolenChunks(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-single-consumer"
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Each distinct probe request maps to a distinct expected reply emitted by
	// startupProbe.Feed. One probe per chunk so every reply is individually
	// attributable to its source chunk having reached the single consumer.
	type probe struct{ req, reply []byte }
	probes := []probe{
		{[]byte("\x1b[c"), []byte("\x1b[?1;0c")},         // DA1
		{[]byte("\x1b[>c"), []byte("\x1b[?62;4;0c")},     // DA2
		{[]byte("\x1b[6n"), []byte("\x1b[1;1R")},         // DSR cursor report
		{[]byte("\x1b[>q"), []byte("\x1b[>0;370;0c")},    // XTVERSION
		{[]byte("\x1b[18t"), []byte("\x1b[8;50;220t")},   // text-area size
		{[]byte("\x1b[19t"), []byte("\x1b[9;700;1760t")}, // screen size (rows*14, cols*8)
	}

	go func() {
		// Home + clear, then one probe-bearing chunk at a time.
		f.push([]byte("\x1b[H\x1b[2J"))
		for _, p := range probes {
			f.push(append([]byte(nil), p.req...))
			// Brief spacing so each chunk is a separate Output() receive; a
			// stealing goroutine would have an opportunity to race here.
			time.Sleep(5 * time.Millisecond)
		}
		// Now present the prompt and complete via the Stop nonce.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	for _, p := range probes {
		n := f.sentContains(p.reply)
		if n < 1 {
			t.Errorf("probe reply %q answered %d times, want >= 1 (count 0 means the probe-bearing chunk was stolen/dropped by a competing Output() consumer)", p.reply, n)
		}
	}
	assertExactlyOneFinal(t, events, "success")
}

// TestRunTurnDismissesInterstitials proves F4: every first-run blocking dialog
// (theme/onboarding, MCP-server trust, plugin trust, bypass-permissions
// warning), rendered with cursor-positioning escapes so it is only legible
// after vt10x rendering, is dismissed with a single Enter on the turn path —
// mirroring the folder-trust handler. Each dialog is answered exactly once and
// the prompt is delivered only AFTER the dialog clears (never pasted into it).
func TestRunTurnDismissesInterstitials(t *testing.T) {
	cases := []struct {
		name   string
		render []byte // cursor-positioned dialog body
	}{
		{
			name:   "theme-onboarding",
			render: []byte("\x1b[3;5HChoose the text style that looks best\x1b[5;7H1. Dark mode\x1b[6;7H2. Light mode"),
		},
		{
			name:   "mcp-trust",
			render: []byte("\x1b[3;5HThis project wants to use the following MCP servers\x1b[5;7HDo you trust this server?"),
		},
		{
			name:   "plugin-trust",
			render: []byte("\x1b[3;5HThis project wants to use the following plugins\x1b[5;7HDo you trust this plugin?"),
		},
		{
			name:   "bypass-warning",
			render: []byte("\x1b[3;5HWARNING: Claude Code running in Bypass Permissions mode\x1b[5;7HI accept the risk. Press Enter to continue."),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			hookDir := filepath.Join(dir, "hooks")
			if err := os.MkdirAll(hookDir, 0o755); err != nil {
				t.Fatal(err)
			}
			stopPath := filepath.Join(hookDir, "stop.json")
			nonce := "nonce-" + tc.name
			transcript := writeTranscript(t, realTranscript)

			f := newFakePTY()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			go func() {
				f.push([]byte("\x1b[H\x1b[2J"))
				f.push(tc.render)
				// Wait for the Enter dismissal, then clear the dialog and show
				// the ready prompt.
				for f.keyCount(session.KeyEnter) == 0 {
					time.Sleep(5 * time.Millisecond)
				}
				f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
				for !f.sawBracketedPaste() {
					time.Sleep(5 * time.Millisecond)
				}
				writeStopPayload(t, stopPath, nonce, transcript)
			}()

			req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
			events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
				300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

			if f.keyCount(session.KeyEnter) == 0 {
				t.Fatalf("%s dialog was not dismissed with Enter", tc.name)
			}
			if f.keyCount(session.KeyEnter) > 1 {
				t.Errorf("%s: Enter pressed %d times, want exactly 1", tc.name, f.keyCount(session.KeyEnter))
			}
			if !f.sawBracketedPaste() {
				t.Errorf("%s: prompt not delivered after dialog dismissed", tc.name)
			}
			assertExactlyOneFinal(t, events, "success")
		})
	}
}

// TestRunTurnSurfacesMidTurnUsageLimit proves F5: a mid-turn usage-limit screen
// is surfaced as a terminal iteration_limit final and evicts the session,
// instead of being absorbed into the (5-minute) turn timeout.
func TestRunTurnSurfacesMidTurnUsageLimit(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// Reach the ready prompt and let the prompt be pasted.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		// Now the model hits its limit mid-turn (rendered, no Stop hook).
		f.push([]byte("\x1b[H\x1b[2J\x1b[3;5HClaude usage limit reached. Your limit will reset at 5pm."))
	}()

	req := harnesses.ExecuteRequest{Prompt: "do work", WorkDir: dir}
	// turnTimeout is LARGE: the test must finish well before it via the fatal
	// screen detection, not the timeout.
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, "nonce-limit",
		100*time.Millisecond, 20*time.Millisecond, 30*time.Second)

	assertExactlyOneFinal(t, events, "iteration_limit")
	if !f.wasKilled() {
		t.Error("session was not killed/evicted on mid-turn usage limit")
	}
}

// TestRunTurnSurfacesMidTurnDisconnect proves F5: a mid-turn connection-error
// screen is surfaced as a terminal failed final (not absorbed into the timeout).
func TestRunTurnSurfacesMidTurnDisconnect(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		f.push([]byte("\x1b[H\x1b[2J\x1b[3;5HConnection error: fetch failed (network error)"))
	}()

	req := harnesses.ExecuteRequest{Prompt: "do work", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, "nonce-disc",
		100*time.Millisecond, 20*time.Millisecond, 30*time.Second)

	assertExactlyOneFinal(t, events, "failed")
}

// TestRunTurnReusedSessionClearsAndScopesTranscript proves F1+F2 together on the
// harness turn path: a REUSED pooled slot (needsClear=true) issues /clear and
// waits for the prompt to return BEFORE sending the new prompt, and resumes the
// transcript read at the prior turn's offset so the new turn's final reflects
// ONLY the new turn (no replay of the prior turn's text/usage).
func TestRunTurnReusedSessionClearsAndScopesTranscript(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := "nonce-reuse"

	// Append-only session transcript: turn 1 already written, turn 2 appended.
	transcriptDir := t.TempDir()
	transcript := filepath.Join(transcriptDir, "session.jsonl")
	if err := os.WriteFile(transcript, []byte(turn1Transcript+turn2Lines), 0o644); err != nil {
		t.Fatal(err)
	}
	// Resume offset = length of turn 1 (so the read scopes to turn 2 only).
	priorOffset := int64(len(turn1Transcript))

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clearObserved := make(chan struct{})
	go func() {
		// Reused slot: the loop must FIRST /clear and wait for the prompt before
		// pasting. Wait for the /clear command, then render the ready prompt.
		for f.sentContains([]byte("/clear")) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		close(clearObserved)
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// The prompt must be pasted only AFTER /clear returned the prompt.
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "second turn", WorkDir: dir}
	events, nextOff, lastPath := claudetui.RunTurnForTestWithOffset(
		ctx, f, req, hookDir, stopPath, nonce,
		priorOffset, transcript,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	select {
	case <-clearObserved:
	default:
		t.Fatal("reused session did not issue /clear before the prompt")
	}
	if f.sentContains([]byte("/clear")) == 0 {
		t.Fatal("no /clear was sent on a reused session")
	}

	// The final must reflect ONLY turn 2 (no replay of turn 1's text/usage).
	var texts []string
	var fin harnesses.FinalData
	finals := 0
	for _, ev := range events {
		switch ev.Type {
		case harnesses.EventTypeTextDelta:
			var d harnesses.TextDeltaData
			_ = json.Unmarshal(ev.Data, &d)
			texts = append(texts, d.Text)
		case harnesses.EventTypeFinal:
			finals++
			_ = json.Unmarshal(ev.Data, &fin)
		}
	}
	if finals != 1 {
		t.Fatalf("final events = %d, want 1", finals)
	}
	if len(texts) != 1 || texts[0] != "Turn two answer." {
		t.Errorf("reused-turn texts = %v, want only [\"Turn two answer.\"] (no turn-1 replay)", texts)
	}
	if fin.FinalText != "Turn two answer." {
		t.Errorf("reused-turn final text = %q, want only turn-2 text", fin.FinalText)
	}
	// The next resume bookmark must advance to end-of-file.
	if nextOff <= priorOffset {
		t.Errorf("next transcript offset = %d, want > prior %d", nextOff, priorOffset)
	}
	if lastPath != transcript {
		t.Errorf("recorded transcript path = %q, want %q", lastPath, transcript)
	}
}

func assertExactlyOneFinal(t *testing.T, events []harnesses.Event, wantStatus string) {
	t.Helper()
	finals := 0
	var fin harnesses.FinalData
	for _, ev := range events {
		if ev.Type == harnesses.EventTypeFinal {
			finals++
			_ = json.Unmarshal(ev.Data, &fin)
		}
	}
	if finals != 1 {
		t.Fatalf("final events = %d, want exactly 1", finals)
	}
	if events[len(events)-1].Type != harnesses.EventTypeFinal {
		t.Fatalf("last event type = %v, want final", events[len(events)-1].Type)
	}
	if wantStatus != "" && fin.Status != wantStatus {
		t.Errorf("final status = %q, want %q", fin.Status, wantStatus)
	}
}
