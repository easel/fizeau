package claudetui_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
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
	exit     session.ExitStatus
	waitOnce sync.Once
	waiting  chan struct{}
	closeOut sync.Once
	onPrompt func() // invoked when a bracketed-paste prompt is observed
}

func newFakePTY() *fakePTY {
	return &fakePTY{out: make(chan session.OutputChunk, 64), waiting: make(chan struct{})}
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

func (f *fakePTY) Wait() session.ExitStatus {
	f.waitOnce.Do(func() { close(f.waiting) })
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exit
}

func (f *fakePTY) closeOutput() {
	f.closeOut.Do(func() { close(f.out) })
}

func (f *fakePTY) setExitStatus(status session.ExitStatus) {
	f.mu.Lock()
	f.exit = status
	f.mu.Unlock()
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

// keysSnapshot returns a copy of the ordered SendKey keys.
func (f *fakePTY) keysSnapshot() []session.Key {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]session.Key(nil), f.keys...)
}

// writeStopPayload writes a Stop-hook payload carrying the nonce + transcript path.
// writeStopPayload writes the Stop payload AND the UserPromptSubmit ack file
// (real Claude Code always fires UserPromptSubmit before Stop for a
// successful turn), so every existing success-path test using this helper
// automatically satisfies the ack requirement without a per-test edit. path's
// parent directory is the turn's hookDir, matching how every caller derives
// path via filepath.Join(hookDir, "stop.json").
func writeStopPayload(t *testing.T, path, nonce, transcript string) {
	t.Helper()
	writePromptAck(t, filepath.Dir(path), nonce)
	if err := writeStopPayloadFile(path, nonce, transcript); err != nil {
		t.Fatalf("write stop payload: %v", err)
	}
}

// writePromptAck writes the UserPromptSubmit ack file directly (bypassing
// writeStopPayload) for tests that exercise the ack mechanism itself.
func writePromptAck(t *testing.T, hookDir, nonce string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nonce": nonce})
	if err := os.WriteFile(claudetui.PromptAckPayloadPathForTest(hookDir), body, 0o644); err != nil {
		t.Fatalf("write prompt ack payload: %v", err)
	}
}

func writeStopPayloadFile(path, nonce, transcript string) error {
	body, _ := json.Marshal(map[string]string{"nonce": nonce, "transcript_path": transcript})
	return os.WriteFile(path, body, 0o644)
}

func testTurnNonce(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:16])
}

func waitForBracketedPaste(ctx context.Context, f *fakePTY) error {
	for !f.sawBracketedPaste() {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for prompt paste: %w", ctx.Err())
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	return nil
}

func runTurnWithTranscriptPath(t *testing.T, transcriptPath, prompt string) []harnesses.Event {
	t.Helper()
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("transcript-finalization")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(5 * time.Millisecond)
			}
		}
		writeStopPayload(t, stopPath, nonce, transcriptPath)
	}()

	return claudetui.RunTurnForTest(ctx, f,
		harnesses.ExecuteRequest{Prompt: prompt, WorkDir: dir},
		hookDir, stopPath, nonce, 200*time.Millisecond, 10*time.Millisecond, 4*time.Second)
}

// TestRunTurnUnreadableTranscriptFailsClosed proves a valid Stop payload is
// not completion authority when its transcript path cannot be resolved or the
// transcript cannot be opened/read.
func TestRunTurnUnreadableTranscriptFailsClosed(t *testing.T) {
	const (
		oauthToken = "oauth-token-must-not-leak"
		apiToken   = "sk-ant-api-token-must-not-leak"
		accountID  = "acct-must-not-leak"
		prompt     = "prompt body must not leak"
	)

	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{
			name: "path expansion failure",
			path: func(t *testing.T) string {
				t.Setenv("HOME", "")
				return "~/" + oauthToken + "/" + accountID + ".jsonl"
			},
		},
		{
			name: "missing transcript",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), apiToken, accountID+".jsonl")
			},
		},
		{
			name: "read failure",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), oauthToken+"-"+accountID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := test.path(t)
			if test.name == "read failure" {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			events := runTurnWithTranscriptPath(t, path, prompt)
			final := requireExactlyOneFinal(t, events, "failed")
			if final.ExitCode == 0 {
				t.Fatal("unreadable transcript final exit code = 0, want nonzero")
			}
			if final.FinalText != "" {
				t.Fatalf("unreadable transcript fabricated final text %q", final.FinalText)
			}
			if final.FinalCostUSD != nil || final.FinalCostSource != harnesses.CostSourceUnknown || final.CostUSD != 0 {
				t.Fatalf("unreadable transcript fabricated cost: %+v", final)
			}
			if final.Usage != nil {
				t.Fatalf("unreadable transcript fabricated usage: %+v", final.Usage)
			}
			if raw := finalEventData(t, events); bytes.Contains(raw, []byte(`"cost_usd"`)) {
				t.Fatalf("unreadable transcript serialized fabricated cost: %s", raw)
			}
			if len(final.Error) == 0 || len(final.Error) > 256 {
				t.Fatalf("diagnostic length = %d, want 1..256", len(final.Error))
			}
			for _, sensitive := range []string{oauthToken, apiToken, accountID, prompt, filepath.Base(path)} {
				if sensitive != "" && strings.Contains(final.Error, sensitive) {
					t.Fatalf("diagnostic retained sensitive value %q: %q", sensitive, final.Error)
				}
			}
		})
	}
}

func TestRunTurnReadableTranscriptSucceedsExactlyOnce(t *testing.T) {
	transcript := writeTranscript(t, realTranscript)
	events := runTurnWithTranscriptPath(t, transcript, "readable transcript prompt")
	final := requireExactlyOneFinal(t, events, "success")
	if final.ExitCode != 0 {
		t.Fatalf("readable transcript exit code = %d, want 0", final.ExitCode)
	}
	if final.FinalText == "" {
		t.Fatal("readable transcript final text is empty")
	}
	if final.FinalText != "Let me list the directory.The directory contains file.txt and dir/." {
		t.Fatalf("readable transcript final text = %q", final.FinalText)
	}
	if final.Usage == nil || final.Usage.InputTokens == nil || *final.Usage.InputTokens != 200 ||
		final.Usage.OutputTokens == nil || *final.Usage.OutputTokens != 40 {
		t.Fatalf("readable transcript usage = %+v, want input=200 output=40", final.Usage)
	}
	if final.Error != "" {
		t.Fatalf("readable transcript error = %q, want empty", final.Error)
	}
}

func TestRunTurnWaitsForStopPayloadAfterPTYEOF(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.Mkdir(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	transcript := writeTranscript(t, realTranscript)
	const nonce = "11111111111111111111111111111111"

	f := newFakePTY()
	f.setExitStatus(session.ExitStatus{Code: 0, Exited: true})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	producerErr := make(chan error, 1)
	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		if err := waitForBracketedPaste(ctx, f); err != nil {
			producerErr <- err
			return
		}
		f.closeOutput()
		select {
		case <-f.waiting:
		case <-ctx.Done():
			producerErr <- ctx.Err()
			return
		}
		producerErr <- writeStopPayloadFile(stopPath, nonce, transcript)
	}()

	events := claudetui.RunTurnForTestWithStopGrace(
		ctx, f, harnesses.ExecuteRequest{Prompt: "late Stop prompt", WorkDir: dir},
		hookDir, stopPath, nonce, 100*time.Millisecond, 5*time.Millisecond, 150*time.Millisecond, 2*time.Second,
	)
	if err := <-producerErr; err != nil {
		t.Fatal(err)
	}
	final := requireExactlyOneFinal(t, events, "success")
	if final.ExitCode != 0 || final.Error != "" {
		t.Fatalf("late Stop final = %+v, want successful transcript final", final)
	}
	if final.FinalText != "Let me list the directory.The directory contains file.txt and dir/." {
		t.Fatalf("late Stop final text = %q", final.FinalText)
	}
	if final.Usage == nil || final.Usage.InputTokens == nil || *final.Usage.InputTokens != 200 ||
		final.Usage.OutputTokens == nil || *final.Usage.OutputTokens != 40 {
		t.Fatalf("late Stop usage = %+v, want input=200 output=40", final.Usage)
	}
}

func TestRunTurnPTYEOFStopGraceExpires(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.Mkdir(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	transcript := writeTranscript(t, realTranscript)
	const (
		nonce = "22222222222222222222222222222222"
		grace = 80 * time.Millisecond
	)

	f := newFakePTY()
	f.setExitStatus(session.ExitStatus{Code: 0, Exited: true})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	graceStarted := make(chan time.Time, 1)
	producerErr := make(chan error, 1)
	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		if err := waitForBracketedPaste(ctx, f); err != nil {
			producerErr <- err
			return
		}
		f.closeOutput()
		select {
		case <-f.waiting:
			graceStarted <- time.Now()
		case <-ctx.Done():
			producerErr <- ctx.Err()
			return
		}
		producerErr <- writeStopPayloadFile(stopPath, "44444444444444444444444444444444", transcript)
	}()

	events := claudetui.RunTurnForTestWithStopGrace(
		ctx, f, harnesses.ExecuteRequest{Prompt: "stale nonce prompt", WorkDir: dir},
		hookDir, stopPath, nonce, 100*time.Millisecond, 5*time.Millisecond, grace, 2*time.Second,
	)
	if err := <-producerErr; err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(<-graceStarted)
	if elapsed > grace+time.Second {
		t.Fatalf("EOF grace elapsed = %s, want bounded above by %s", elapsed, grace+time.Second)
	}
	final := requireExactlyOneFinal(t, events, "failed")
	if final.ExitCode == 0 || final.FinalText != "" || final.Usage != nil || final.FinalCostUSD != nil {
		t.Fatalf("expired grace fabricated completion evidence: %+v", final)
	}
	if !strings.Contains(final.Error, "PTY closed before Stop hook") {
		t.Fatalf("expired grace diagnostic = %q", final.Error)
	}
}

func TestRunTurnPTYEOFPreservesSanitizedExitEvidence(t *testing.T) {
	const (
		apiToken   = "sk-ant-eof-secret-token"
		oauthToken = "oauth-eof-secret-token"
		accountID  = "acct-eof-secret"
		prompt     = "prompt-eof-secret"
		frameText  = "surrounding-frame-eof-secret"
	)
	tests := []struct {
		name         string
		status       session.ExitStatus
		wantEvidence string
		wantExitCode int
	}{
		{
			name: "nonzero exit",
			status: session.ExitStatus{
				Code: 23, Exited: true, Err: fmt.Errorf("raw wait %s %s", apiToken, accountID),
			},
			wantEvidence: "exit code 23",
			wantExitCode: 23,
		},
		{
			name: "signal",
			status: session.ExitStatus{
				Code: -1, Signaled: true, Signal: "killed " + oauthToken, Err: fmt.Errorf("raw wait %s", frameText),
			},
			wantEvidence: "terminated by signal",
			wantExitCode: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			hookDir := filepath.Join(dir, "hooks")
			if err := os.Mkdir(hookDir, 0o755); err != nil {
				t.Fatal(err)
			}
			stopPath := filepath.Join(hookDir, "stop.json")
			f := newFakePTY()
			f.setExitStatus(test.status)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			producerErr := make(chan error, 1)
			go func() {
				f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
				if err := waitForBracketedPaste(ctx, f); err != nil {
					producerErr <- err
					return
				}
				f.push([]byte(frameText + " " + apiToken + " " + oauthToken + " " + accountID + " " + prompt))
				f.closeOutput()
				select {
				case <-f.waiting:
					producerErr <- nil
				case <-ctx.Done():
					producerErr <- ctx.Err()
				}
			}()

			events := claudetui.RunTurnForTestWithStopGrace(
				ctx, f, harnesses.ExecuteRequest{Prompt: prompt, WorkDir: dir},
				hookDir, stopPath, "33333333333333333333333333333333", 100*time.Millisecond, 5*time.Millisecond, 40*time.Millisecond, 2*time.Second,
			)
			if err := <-producerErr; err != nil {
				t.Fatal(err)
			}
			final := requireExactlyOneFinal(t, events, "failed")
			if final.ExitCode != test.wantExitCode {
				t.Fatalf("exit code = %d, want %d", final.ExitCode, test.wantExitCode)
			}
			if !strings.Contains(final.Error, test.wantEvidence) {
				t.Fatalf("diagnostic = %q, want %q", final.Error, test.wantEvidence)
			}
			if !utf8.ValidString(final.Error) || len(final.Error) > anthropic.MaxRouteFailureDiagnosticBytes {
				t.Fatalf("diagnostic is invalid or unbounded: %q", final.Error)
			}
			for _, sensitive := range []string{apiToken, oauthToken, accountID, prompt, frameText, test.status.Signal} {
				if sensitive != "" && strings.Contains(final.Error, sensitive) {
					t.Fatalf("diagnostic retained sensitive value %q: %q", sensitive, final.Error)
				}
			}
			if final.FinalText != "" || final.Usage != nil || final.FinalCostUSD != nil ||
				final.FinalCostSource != harnesses.CostSourceUnknown {
				t.Fatalf("EOF failure fabricated completion evidence: %+v", final)
			}
			if final.RoutingActual == nil || final.RoutingActual.Harness != "claude-tui" || final.RoutingActual.FailureClass != "unknown" {
				t.Fatalf("generic EOF routing actual = %+v, want claude-tui unknown failure", final.RoutingActual)
			}
		})
	}
}

func TestRunTurnPTYEOFGraceHonorsCancellationAndDeadline(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		dir := t.TempDir()
		hookDir := filepath.Join(dir, "hooks")
		if err := os.Mkdir(hookDir, 0o755); err != nil {
			t.Fatal(err)
		}
		stopPath := filepath.Join(hookDir, "stop.json")
		f := newFakePTY()
		ctx, cancel := context.WithCancel(context.Background())
		producerErr := make(chan error, 1)
		go func() {
			f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
			if err := waitForBracketedPaste(ctx, f); err != nil {
				producerErr <- err
				return
			}
			f.closeOutput()
			select {
			case <-f.waiting:
				cancel()
				producerErr <- nil
			case <-time.After(2 * time.Second):
				producerErr <- fmt.Errorf("Wait was not entered after PTY EOF")
			}
		}()

		events := claudetui.RunTurnForTestWithStopGrace(
			ctx, f, harnesses.ExecuteRequest{Prompt: "cancel during EOF grace", WorkDir: dir},
			hookDir, stopPath, "55555555555555555555555555555555",
			100*time.Millisecond, 5*time.Millisecond, time.Second, 3*time.Second,
		)
		if err := <-producerErr; err != nil {
			t.Fatal(err)
		}
		final := requireExactlyOneFinal(t, events, "cancelled")
		if final.ExitCode != 130 || final.FinalText != "" || final.Usage != nil {
			t.Fatalf("cancellation final = %+v", final)
		}
	})

	t.Run("turn deadline", func(t *testing.T) {
		dir := t.TempDir()
		hookDir := filepath.Join(dir, "hooks")
		if err := os.Mkdir(hookDir, 0o755); err != nil {
			t.Fatal(err)
		}
		stopPath := filepath.Join(hookDir, "stop.json")
		f := newFakePTY()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		producerErr := make(chan error, 1)
		go func() {
			f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
			if err := waitForBracketedPaste(ctx, f); err != nil {
				producerErr <- err
				return
			}
			f.closeOutput()
			select {
			case <-f.waiting:
				producerErr <- nil
			case <-ctx.Done():
				producerErr <- ctx.Err()
			}
		}()

		events := claudetui.RunTurnForTestWithStopGrace(
			ctx, f, harnesses.ExecuteRequest{Prompt: "deadline during EOF grace", WorkDir: dir},
			hookDir, stopPath, "66666666666666666666666666666666",
			100*time.Millisecond, 5*time.Millisecond, 2*time.Second, 500*time.Millisecond,
		)
		if err := <-producerErr; err != nil {
			t.Fatal(err)
		}
		final := requireExactlyOneFinal(t, events, "timed_out")
		if final.ExitCode != 124 || final.FinalText != "" || final.Usage != nil {
			t.Fatalf("deadline final = %+v", final)
		}
	})
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
	nonce := testTurnNonce("trust")

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
	nonce := testTurnNonce("complete")
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
	nonce := testTurnNonce("current")
	transcript := writeTranscript(t, realTranscript)

	// Pre-existing STALE payload with the wrong nonce.
	writeStopPayload(t, stopPath, testTurnNonce("old"), transcript)

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
	nonce := testTurnNonce("remove-stale")
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
	nonce := testTurnNonce("tools")
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

// TestRunTurnDocumentsRequestGaps proves req.Reasoning/Permissions with no TUI
// affordance are surfaced as documented-gap progress warnings rather than
// silently dropped. req.Model is NOT a gap: it is honored via the --model launch
// flag (see TestBuildLaunchArgsHonorsModel), so it must NOT emit a gap warning.
func TestRunTurnDocumentsRequestGaps(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("gap")
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
	if len(gapMsgs) != 2 {
		t.Fatalf("documented gaps = %d, want 2 (reasoning, permissions): %v", len(gapMsgs), gapMsgs)
	}
	for _, m := range gapMsgs {
		if strings.Contains(m, "model") {
			t.Fatalf("req.Model must be honored via --model, not reported as a gap: %q", m)
		}
	}
}

// TestRunTurnNoGapWarningsForUnsetFields proves the complement of
// TestRunTurnDocumentsRequestGaps: when req.Reasoning and req.Permissions are
// UNSET (empty string, the default), the turn emits ZERO unsupported_request_field
// gap warnings. CONTRACT-004 requires warnings only for fields with no TUI
// affordance that the caller actually requested; an unset field is not a gap.
func TestRunTurnNoGapWarningsForUnsetFields(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("no-gap")
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

	// Reasoning and Permissions left at their zero value; Model set (honored, not a gap).
	req := harnesses.ExecuteRequest{
		Prompt:  "hi",
		WorkDir: dir,
		Model:   "claude-opus-4-6",
	}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	for _, ev := range events {
		if ev.Type != harnesses.EventTypeProgress {
			continue
		}
		var w harnesses.FinalWarning
		if err := json.Unmarshal(ev.Data, &w); err == nil && w.Code == "unsupported_request_field" {
			t.Fatalf("unset request fields must NOT emit gap warnings, got: %q", w.Message)
		}
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

// sentSnapshot returns a copy of the ordered SendBytes payloads.
func (f *fakePTY) sentSnapshot() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.sent))
	for i, b := range f.sent {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

// TestRunTurnSubmitsPromptWithSeparateEnterAfterPaste is the dedicated
// regression test for the live-smoke wedge (round 2). The root cause, proven
// against live Claude Code 2.1.162, was that the bracketed-paste END marker
// glued to a trailing carriage return ("\x1b[201~\r") in ONE write lands the
// text in the Ink input box but never SUBMITS it: the turn never starts, the
// Stop hook never fires, and the loop wedges to the turn timeout. The fix sends
// the paste WITHOUT a trailing "\r", then a SEPARATE bare "\r" after a settle
// delay. This test asserts that exact wire sequence on the scripted fake PTY.
func TestRunTurnSubmitsPromptWithSeparateEnterAfterPaste(t *testing.T) {
	restore := claudetui.SetPromptSubmitDelayForTest(15 * time.Millisecond)
	defer restore()

	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("submit")
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// Only complete AFTER the standalone submit "\r" has been sent, proving
		// the turn loop actually emits the submit keystroke (not just the paste).
		for f.sentContains([]byte("\r")) == 0 || !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "do the thing", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		100*time.Millisecond, 10*time.Millisecond, 4*time.Second)

	assertExactlyOneFinal(t, events, "success")

	sent := f.sentSnapshot()
	// 1) Find the bracketed-paste write and assert it carries the prompt and
	//    does NOT end with a carriage return (the bug was a glued "\r").
	pasteIdx := -1
	for i, b := range sent {
		if len(b) >= 6 && string(b[:6]) == "\x1b[200~" {
			pasteIdx = i
			if !bytes.Contains(b, []byte("do the thing")) {
				t.Errorf("paste write does not carry the prompt: %q", b)
			}
			if bytes.HasSuffix(b, []byte("\r")) {
				t.Errorf("paste write must NOT end with a carriage return (the wedge bug): %q", b)
			}
			if !bytes.HasSuffix(b, []byte("\x1b[201~")) {
				t.Errorf("paste write must end with the bracketed-paste END marker: %q", b)
			}
			break
		}
	}
	if pasteIdx < 0 {
		t.Fatal("no bracketed-paste write observed")
	}
	// 2) A standalone "\r" submit write must appear AFTER the paste write.
	sawSeparateSubmit := false
	for i := pasteIdx + 1; i < len(sent); i++ {
		if string(sent[i]) == "\r" {
			sawSeparateSubmit = true
			break
		}
	}
	if !sawSeparateSubmit {
		t.Errorf("no standalone carriage-return submit write after the paste; sequence=%q", sent)
	}
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
	nonce := testTurnNonce("single-consumer")
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
// (theme/onboarding, MCP-server trust, plugin trust), rendered with cursor-
// positioning escapes so it is only legible after vt10x rendering, is dismissed
// with a single Enter on the turn path — mirroring the folder-trust handler.
// Each dialog is answered exactly once and the prompt is delivered only AFTER
// the dialog clears (never pasted into it).
//
// The Bypass Permissions consent screen is deliberately NOT in this table: it
// is a two-choice decision, not an Enter-dismissed interstitial, and is covered
// by TestRunTurnBypassConsentAcceptsWithUnrestricted and
// TestRunTurnBypassConsentFailsWithoutAuthorization with selection-aware
// keystroke assertions.
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			hookDir := filepath.Join(dir, "hooks")
			if err := os.MkdirAll(hookDir, 0o755); err != nil {
				t.Fatal(err)
			}
			stopPath := filepath.Join(hookDir, "stop.json")
			nonce := testTurnNonce(tc.name)
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

// bypassConsentScreen renders the Claude Code 2.1.218 two-choice Bypass
// Permissions consent screen (full-screen redraw), with the highlight cursor
// "❯" on the option numbered highlightNum. A bare Enter on the default
// (highlight on option 1, "No, exit") quits Claude — the historical bug.
func bypassConsentScreen(highlightNum int) []byte {
	opt := func(n int, text string) string {
		if n == highlightNum {
			return "❯ " + strconv.Itoa(n) + ". " + text
		}
		return "  " + strconv.Itoa(n) + ". " + text
	}
	return []byte("\x1b[H\x1b[2J" +
		"\x1b[3;5HWARNING: Claude Code running in Bypass Permissions mode" +
		"\x1b[4;5HClaude will not ask for approval before running tools." +
		"\x1b[6;5H" + opt(1, "No, exit") +
		"\x1b[7;5H" + opt(2, "Yes, I accept") +
		"\x1b[9;5HEnter to confirm · Esc to go back")
}

// TestRunTurnBypassConsentAcceptsWithUnrestricted proves the accept path
// (acceptance criteria 1 & 2): given an explicitly unrestricted request, the
// driver moves the highlight off the default "No, exit" onto "Yes, I accept"
// (Down BEFORE Enter), never pastes the task while the dialog is visible,
// verifies the screen advanced, emits a bypass_consent_accepted audit event,
// delivers the task, and emits exactly one successful final.
func TestRunTurnBypassConsentAcceptsWithUnrestricted(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("bypass-accept")
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pasteWhileDialogVisible := make(chan bool, 1)
	go func() {
		// Consent screen with the default highlight on option 1 ("No, exit").
		f.push(bypassConsentScreen(1))
		// The driver must step the highlight down before confirming.
		for f.keyCount(session.KeyDown) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		// Redraw with the highlight now on option 2 ("Yes, I accept").
		f.push(bypassConsentScreen(2))
		// The driver confirms only once the accept option is highlighted.
		for f.keyCount(session.KeyEnter) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		pasteWhileDialogVisible <- f.sawBracketedPaste()
		// Consent accepted: clear the dialog and present the ready prompt.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir, Permissions: "unrestricted"}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	if paste := <-pasteWhileDialogVisible; paste {
		t.Error("task was pasted while the bypass consent dialog was still visible")
	}

	// Selection-aware: Down must precede the first Enter so the confirm lands on
	// "Yes, I accept", not the default "No, exit".
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
		t.Fatalf("driver did not send Down to select 'Yes, I accept'; keys=%v", keys)
	}
	if enterIdx == -1 {
		t.Fatalf("driver did not confirm the selection with Enter; keys=%v", keys)
	}
	if downIdx > enterIdx {
		t.Fatalf("Enter (idx %d) preceded Down (idx %d): driver confirmed the default 'No, exit'; keys=%v", enterIdx, downIdx, keys)
	}
	if !f.sawBracketedPaste() {
		t.Error("task not delivered after bypass consent accepted")
	}
	if !hasProgressWarningCode(events, "bypass_consent_accepted") {
		t.Error("missing bypass_consent_accepted audit progress event")
	}
	assertExactlyOneFinal(t, events, "success")
	if f.wasKilled() {
		t.Error("session was evicted despite a successful accepted-consent turn")
	}
}

// TestRunTurnBypassConsentFailsWithoutAuthorization proves the fail-closed path
// (acceptance criterion 3): the same two-choice screen, with a request that did
// NOT carry explicit unrestricted authorization, produces exactly one typed
// failed final BEFORE any task injection. The driver must not confirm the
// default "No, exit", must not paste the task, and the diagnostic identifies
// bypass consent while staying sanitized and bounded (never a generic "PTY
// closed before Stop hook").
func TestRunTurnBypassConsentFailsWithoutAuthorization(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("bypass-fail")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { f.push(bypassConsentScreen(1)) }()

	// No Permissions field => no explicit unrestricted authorization.
	req := harnesses.ExecuteRequest{Prompt: "diagnostic-smoke", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	final := requireExactlyOneFinal(t, events, "failed")
	if final.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", final.ExitCode)
	}
	if !strings.Contains(final.Error, "Bypass Permissions") {
		t.Errorf("diagnostic does not identify bypass consent: %q", final.Error)
	}
	if strings.Contains(final.Error, "closed before Stop hook") {
		t.Errorf("diagnostic degraded to generic PTY-EOF message: %q", final.Error)
	}
	if !utf8.ValidString(final.Error) || len(final.Error) > anthropic.MaxRouteFailureDiagnosticBytes {
		t.Errorf("diagnostic invalid or unbounded: %q", final.Error)
	}
	if final.RoutingActual == nil || final.RoutingActual.Harness != "claude-tui" {
		t.Errorf("routing actual = %+v, want claude-tui harness", final.RoutingActual)
	}
	if f.sawBracketedPaste() {
		t.Error("task was pasted despite unauthorized bypass consent")
	}
	if f.keyCount(session.KeyEnter) != 0 || f.keyCount(session.KeyDown) != 0 {
		t.Errorf("driver sent selection keystrokes (down=%d enter=%d); must not touch the dialog without authorization",
			f.keyCount(session.KeyDown), f.keyCount(session.KeyEnter))
	}
	if final.FinalText != "" || final.Usage != nil {
		t.Errorf("fabricated completion evidence on a preflight failure: %+v", final)
	}
}

// TestRunTurnBypassConsentDoesNotTriggerMidTurn proves D1: after the prompt is
// submitted, a frame whose ASSISTANT PROSE echoes the consent wording (warning
// phrase + numbered "Yes, I accept" / "No, exit" lines) must NOT be treated as
// a consent screen — no Kill, no stray keystrokes — because consent handling is
// gated to the pre-submit startup phase.
func TestRunTurnBypassConsentDoesNotTriggerMidTurn(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("bypass-midturn")
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proseDidNotKill := make(chan bool, 1)
	go func() {
		// Ready prompt first so the task is submitted before the prose renders.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		// Assistant explains the bypass consent dialog, reproducing its wording
		// and numbered options as ordinary prose.
		f.push([]byte("\x1b[H\x1b[2J" +
			"\x1b[2;2HThe Bypass Permissions mode screen shows:" +
			"\x1b[3;4H1. No, exit" +
			"\x1b[4;4H2. Yes, I accept"))
		time.Sleep(120 * time.Millisecond)
		proseDidNotKill <- !f.wasKilled()
		writeStopPayload(t, stopPath, nonce, transcript)
	}()

	req := harnesses.ExecuteRequest{Prompt: "explain the bypass consent dialog", WorkDir: dir, Permissions: "unrestricted"}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	if ok := <-proseDidNotKill; !ok {
		t.Error("assistant prose echoing the consent dialog killed the session mid-turn")
	}
	// The only keystrokes must be the pre-submit ones; no consent navigation was
	// triggered by the mid-turn prose (Down is never used on the ready path).
	if f.keyCount(session.KeyDown) != 0 {
		t.Errorf("mid-turn prose triggered consent navigation: down=%d", f.keyCount(session.KeyDown))
	}
	assertExactlyOneFinal(t, events, "success")
}

// TestRunTurnBypassConsentUnrecognizedChoicesFailsLoud proves the resilience
// path (Q5): a bypass DECISION screen (warning phrase + numbered options) whose
// options do not contain a confidently identifiable "accept" choice produces a
// typed, snapshot-bearing failure rather than a guessed keystroke or a silent
// fall-through to EOF.
func TestRunTurnBypassConsentUnrecognizedChoicesFailsLoud(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("bypass-unrecognized")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// A reworded future consent screen: warning phrase + numbered options,
		// but neither option says "accept".
		f.push([]byte("\x1b[H\x1b[2J" +
			"\x1b[3;5HWARNING: Claude Code running in Bypass Permissions mode" +
			"\x1b[6;5H❯ 1. Cancel and quit" +
			"\x1b[7;5H  2. Continue anyway"))
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir, Permissions: "unrestricted"}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		300*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	final := requireExactlyOneFinal(t, events, "failed")
	if !strings.Contains(final.Error, "no option matching") {
		t.Errorf("diagnostic does not identify the unrecognized-choices condition: %q", final.Error)
	}
	if !strings.Contains(final.Error, "last screen:") {
		t.Errorf("diagnostic missing bounded screen snapshot: %q", final.Error)
	}
	if f.keyCount(session.KeyEnter) != 0 || f.keyCount(session.KeyDown) != 0 {
		t.Errorf("driver guessed a keystroke on an unrecognized decision screen: down=%d enter=%d",
			f.keyCount(session.KeyDown), f.keyCount(session.KeyEnter))
	}
	if f.sawBracketedPaste() {
		t.Error("task pasted despite an unrecognized consent screen")
	}
}

// TestRunTurnUnrecognizedStartupScreenFailsLoud proves the startup watchdog
// (Q1/Q2): if nothing recognized (consent, interstitial, or ready prompt)
// appears within the startup window, the driver fails loud with a typed,
// snapshot-bearing diagnostic instead of blind-pasting or wedging to EOF.
func TestRunTurnUnrecognizedStartupScreenFailsLoud(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("startup-unrecognized")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// An unrecognized startup screen: no ready prompt, no known dialog.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;2HSome brand-new onboarding screen we do not recognize"))
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir, Permissions: "unrestricted"}
	// readyTimeout small; maxStartupRearm=1 => ~2 windows before failing loud.
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		120*time.Millisecond, 20*time.Millisecond, 4*time.Second)

	final := requireExactlyOneFinal(t, events, "failed")
	if !strings.Contains(final.Error, "startup UI may have changed") {
		t.Errorf("diagnostic does not identify startup drift: %q", final.Error)
	}
	if !strings.Contains(final.Error, "last screen:") {
		t.Errorf("diagnostic missing bounded screen snapshot: %q", final.Error)
	}
	if f.sawBracketedPaste() {
		t.Error("driver blind-pasted into an unrecognized startup screen")
	}
	if final.RoutingActual == nil || final.RoutingActual.FailureClass != "protocol" {
		t.Errorf("routing actual = %+v, want protocol failure class", final.RoutingActual)
	}
}

// TestRunTurnPromptAckConfirmsAndSucceeds proves the ack happy path: once the
// UserPromptSubmit ack file appears (bound to the per-turn nonce), the turn
// proceeds to a normal Stop-driven success with no retry submit.
func TestRunTurnPromptAckConfirmsAndSucceeds(t *testing.T) {
	restore := claudetui.SetPromptSubmitDelayForTest(10 * time.Millisecond)
	defer restore()

	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("prompt-ack-success")
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// Wait for the real standalone submit "\r" (not just the paste).
		for f.sentContains([]byte("\r")) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		// Ack arrives promptly; the turn must NOT retry the submit.
		writePromptAck(t, hookDir, nonce)
		time.Sleep(30 * time.Millisecond)
		if err := writeStopPayloadFile(stopPath, nonce, transcript); err != nil {
			t.Errorf("write stop payload: %v", err)
		}
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		200*time.Millisecond, 10*time.Millisecond, 4*time.Second)

	assertExactlyOneFinal(t, events, "success")
	if n := f.sentContains([]byte("\r")); n != 1 {
		t.Errorf("submit \"\\r\" sent %d times, want exactly 1 (ack confirmed, no retry)", n)
	}
}

// TestRunTurnPromptAckRetriesOnceThenSucceeds proves the retry path: if the
// ack does not arrive within the first ack window, the driver resends a
// standalone "\r" exactly once; once the (delayed) ack then arrives, the turn
// completes normally.
func TestRunTurnPromptAckRetriesOnceThenSucceeds(t *testing.T) {
	restore := claudetui.SetPromptSubmitDelayForTest(10 * time.Millisecond)
	defer restore()

	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("prompt-ack-retry")
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// Wait for the RETRY submit (a second "\r"): the first ack window must
		// have elapsed with no ack file written, proving the retry fired.
		for f.sentContains([]byte("\r")) < 2 {
			time.Sleep(5 * time.Millisecond)
		}
		// Ack arrives only after the retry; the turn must still succeed.
		writePromptAck(t, hookDir, nonce)
		time.Sleep(20 * time.Millisecond)
		if err := writeStopPayloadFile(stopPath, nonce, transcript); err != nil {
			t.Errorf("write stop payload: %v", err)
		}
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	// Small readyTimeout (reused as the ack window) so the first ack timeout
	// fires quickly and the retry is observable within the test's budget.
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		40*time.Millisecond, 10*time.Millisecond, 4*time.Second)

	assertExactlyOneFinal(t, events, "success")
	if n := f.sentContains([]byte("\r")); n != 2 {
		t.Errorf("submit \"\\r\" sent %d times, want exactly 2 (original submit + one retry)", n)
	}
}

// TestRunTurnPromptAckTimeoutFailsLoud proves the fail-loud path: if the ack
// never arrives even after the one retry, the driver kills the session and
// emits a typed, sanitized, protocol-classified diagnostic naming the
// swallowed-submit condition — never a silent wedge to the turn timeout.
func TestRunTurnPromptAckTimeoutFailsLoud(t *testing.T) {
	restore := claudetui.SetPromptSubmitDelayForTest(10 * time.Millisecond)
	defer restore()

	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	nonce := testTurnNonce("prompt-ack-timeout")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		// Never write an ack or a Stop payload.
	}()

	req := harnesses.ExecuteRequest{Prompt: "hi", WorkDir: dir}
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, nonce,
		40*time.Millisecond, 10*time.Millisecond, 4*time.Second)

	final := requireExactlyOneFinal(t, events, "failed")
	if !strings.Contains(final.Error, "never acknowledged turn start") {
		t.Errorf("diagnostic does not identify the swallowed-submit condition: %q", final.Error)
	}
	if !strings.Contains(final.Error, "last screen:") {
		t.Errorf("diagnostic missing bounded screen snapshot: %q", final.Error)
	}
	if final.RoutingActual == nil || final.RoutingActual.FailureClass != "protocol" {
		t.Errorf("routing actual = %+v, want protocol failure class", final.RoutingActual)
	}
	if n := f.sentContains([]byte("\r")); n != 2 {
		t.Errorf("submit \"\\r\" sent %d times, want exactly 2 (original submit + one retry) before failing loud", n)
	}
	if !f.wasKilled() {
		t.Error("session was not killed on ack timeout")
	}
}

// hasProgressWarningCode reports whether any progress event carries a
// FinalWarning with the given code.
func hasProgressWarningCode(events []harnesses.Event, code string) bool {
	for _, ev := range events {
		if ev.Type != harnesses.EventTypeProgress {
			continue
		}
		var w harnesses.FinalWarning
		if err := json.Unmarshal(ev.Data, &w); err == nil && w.Code == code {
			return true
		}
	}
	return false
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
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, testTurnNonce("limit"),
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
	events := claudetui.RunTurnForTest(ctx, f, req, hookDir, stopPath, testTurnNonce("disconnect"),
		100*time.Millisecond, 20*time.Millisecond, 30*time.Second)

	assertExactlyOneFinal(t, events, "failed")
}

func TestRunTurnIgnoresFatalMarkersInAssistantProse(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")
	transcript := writeTranscript(t, realTranscript)

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	proseDidNotKill := make(chan bool, 1)

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		f.push([]byte("\x1b[H\x1b[2J" +
			"\x1b[2;2HOrdinary assistant explanation" +
			"\x1b[4;2HAn Invalid API key means the supplied credential was rejected."))
		time.Sleep(100 * time.Millisecond)
		proseDidNotKill <- !f.wasKilled()
		writeStopPayload(t, stopPath, testTurnNonce("assistant-prose"), transcript)
	}()

	events := claudetui.RunTurnForTest(ctx, f,
		harnesses.ExecuteRequest{Prompt: "explain invalid API keys", WorkDir: dir},
		hookDir, stopPath, testTurnNonce("assistant-prose"), 100*time.Millisecond, 20*time.Millisecond, 4*time.Second)
	if ok := <-proseDidNotKill; !ok {
		t.Error("ordinary assistant prose containing a fatal marker killed the session")
	}
	assertExactlyOneFinal(t, events, "success")
	if f.wasKilled() {
		t.Error("successful assistant response was evicted as a fatal screen")
	}
}

func TestRunTurnSurfacesAuthenticationFailureWithTypedClass(t *testing.T) {
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	promptLead := "Explain these authentication strings without treating them as runtime evidence:"
	promptFirstRow := "Please run /login; Invalid API key; authentication_error;"
	promptSecondRow := "OAuth token has expired; Failed to authenticate; Could not refresh auth token"
	prompt := promptLead + " " + promptFirstRow + " " + promptSecondRow
	promptOnlyDidNotKill := make(chan bool, 1)

	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		for !f.sawBracketedPaste() {
			time.Sleep(5 * time.Millisecond)
		}
		// Claude echoes caller input in the rendered frame. This snapshot is
		// deliberately clipped/scrolled so neither the prompt glyph nor the
		// first input row remains; marker-bearing continuation rows are still
		// caller input, not execution-failure evidence.
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;3H" + promptFirstRow + "\x1b[3;3H" + promptSecondRow))
		time.Sleep(100 * time.Millisecond)
		promptOnlyDidNotKill <- !f.wasKilled()

		// A later, separate fatal screen is authoritative executing-surface
		// evidence and must terminate the turn.
		f.push([]byte("\x1b[H\x1b[2J" +
			"\x1b[2;2Hsurrounding frame text must not be retained" +
			"\x1b[4;2HAPI Error: Failed to authenticate" +
			"\x1b[5;2HAPI Error: Could not refresh auth token" +
			"\x1b[7;2Hprompt text must not be retained"))
	}()

	events := claudetui.RunTurnForTest(ctx, f,
		harnesses.ExecuteRequest{Prompt: prompt, WorkDir: dir},
		hookDir, stopPath, testTurnNonce("auth"), 100*time.Millisecond, 20*time.Millisecond, 30*time.Second)
	if ok := <-promptOnlyDidNotKill; !ok {
		t.Error("prompt-only marker frame killed the session or emitted a terminal failure")
	}

	var finals []harnesses.FinalData
	for _, event := range events {
		if event.Type != harnesses.EventTypeFinal {
			continue
		}
		var final harnesses.FinalData
		if err := json.Unmarshal(event.Data, &final); err != nil {
			t.Fatalf("decode final: %v", err)
		}
		finals = append(finals, final)
	}
	if len(finals) != 1 {
		t.Fatalf("final events = %d, want exactly 1", len(finals))
	}
	final := finals[0]
	if final.Status != "failed" {
		t.Errorf("final status = %q, want failed", final.Status)
	}
	if final.RoutingActual == nil {
		t.Fatal("routing actual is nil")
	}
	if final.RoutingActual.Harness != "claude-tui" || final.RoutingActual.FailureClass != "credential_invalid" {
		t.Errorf("routing actual = %+v, want claude-tui credential_invalid", final.RoutingActual)
	}
	if final.Error != "API Error: Failed to authenticate\nAPI Error: Could not refresh auth token" {
		t.Errorf("retained evidence = %q, want only matched fatal lines", final.Error)
	}
	for _, excluded := range []string{"surrounding frame text", "Explain:", "prompt text"} {
		if strings.Contains(final.Error, excluded) {
			t.Errorf("retained evidence contains excluded frame/prompt text %q", excluded)
		}
	}
	if !f.wasKilled() {
		t.Error("session was not killed/evicted on authentication failure")
	}
}

func TestRunTurnAuthenticationFailureImmediatelyBeforePTYEOFFinalizesTypedExactlyOnce(t *testing.T) {
	const incident = "Failed to authenticate: OAuth session expired and could not be refreshed"
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	f.setExitStatus(session.ExitStatus{Code: 1, Exited: true})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	producerErr := make(chan error, 1)
	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		if err := waitForBracketedPaste(ctx, f); err != nil {
			producerErr <- err
			return
		}
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;2H" + incident))
		f.closeOutput()
		producerErr <- nil
	}()

	events := claudetui.RunTurnForTestWithStopGrace(
		ctx, f, harnesses.ExecuteRequest{Prompt: "auth-exit regression", WorkDir: dir},
		hookDir, stopPath, testTurnNonce("auth-exit"),
		100*time.Millisecond, 5*time.Millisecond, 40*time.Millisecond, 2*time.Second,
	)
	if err := <-producerErr; err != nil {
		t.Fatal(err)
	}
	final := requireExactlyOneFinal(t, events, "failed")
	if final.ExitCode == 0 {
		t.Fatal("authentication failure exit code is zero")
	}
	if final.RoutingActual == nil || final.RoutingActual.Harness != "claude-tui" ||
		final.RoutingActual.FailureClass != "credential_invalid" {
		t.Fatalf("routing actual = %+v, want claude-tui credential_invalid", final.RoutingActual)
	}
	if final.Error != incident {
		t.Fatalf("retained authentication evidence = %q, want exact incident line", final.Error)
	}
	if final.FinalText != "" || final.Usage != nil || final.FinalCostUSD != nil ||
		final.FinalCostSource != harnesses.CostSourceUnknown {
		t.Fatalf("authentication failure fabricated completion evidence: %+v", final)
	}
	for _, event := range events {
		if event.Type == harnesses.EventTypeTextDelta {
			t.Fatalf("authentication failure emitted fabricated text event: %+v", event)
		}
	}
	if !f.wasKilled() {
		t.Fatal("authentication failure did not evict the request-local PTY")
	}
}

func TestRunTurnClaudeBulletAuthenticationFailureBeforePTYEOFFinalizesTypedExactlyOnce(t *testing.T) {
	const (
		incident          = "● Please run /login · API Error: 401 OAuth access token is invalid."
		sanitizedIncident = "● Please run /login · API Error: 401 OAuth access token: [REDACTED] invalid."
	)
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(hookDir, "stop.json")

	f := newFakePTY()
	f.setExitStatus(session.ExitStatus{Code: 1, Exited: true})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	producerErr := make(chan error, 1)
	go func() {
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;1H❯ "))
		if err := waitForBracketedPaste(ctx, f); err != nil {
			producerErr <- err
			return
		}
		f.push([]byte("\x1b[H\x1b[2J\x1b[2;2H" + incident))
		f.closeOutput()
		producerErr <- nil
	}()

	events := claudetui.RunTurnForTestWithStopGrace(
		ctx, f, harnesses.ExecuteRequest{Prompt: "decorated auth-exit regression", WorkDir: dir},
		hookDir, stopPath, testTurnNonce("decorated-auth-exit"),
		100*time.Millisecond, 5*time.Millisecond, 40*time.Millisecond, 2*time.Second,
	)
	if err := <-producerErr; err != nil {
		t.Fatal(err)
	}
	final := requireExactlyOneFinal(t, events, "failed")
	if final.ExitCode == 0 {
		t.Fatal("authentication failure exit code is zero")
	}
	if final.RoutingActual == nil || final.RoutingActual.Harness != "claude-tui" ||
		final.RoutingActual.FailureClass != "credential_invalid" {
		t.Fatalf("routing actual = %+v, want claude-tui credential_invalid", final.RoutingActual)
	}
	if final.Error != sanitizedIncident {
		t.Fatalf("retained authentication evidence = %q, want sanitized decorated incident line", final.Error)
	}
	if final.FinalText != "" || final.Usage != nil || final.FinalCostUSD != nil ||
		final.FinalCostSource != harnesses.CostSourceUnknown {
		t.Fatalf("authentication failure fabricated completion evidence: %+v", final)
	}
	for _, event := range events {
		if event.Type == harnesses.EventTypeTextDelta {
			t.Fatalf("authentication failure emitted fabricated text event: %+v", event)
		}
	}
	if !f.wasKilled() {
		t.Fatal("authentication failure did not evict the request-local PTY")
	}
}

func assertExactlyOneFinal(t *testing.T, events []harnesses.Event, wantStatus string) {
	t.Helper()
	_ = requireExactlyOneFinal(t, events, wantStatus)
}

func requireExactlyOneFinal(t *testing.T, events []harnesses.Event, wantStatus string) harnesses.FinalData {
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
	return fin
}

func finalEventData(t *testing.T, events []harnesses.Event) json.RawMessage {
	t.Helper()
	for _, event := range events {
		if event.Type == harnesses.EventTypeFinal {
			return event.Data
		}
	}
	t.Fatal("no final event")
	return nil
}
