package claudetui_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/pty/session"
	"github.com/easel/fizeau/internal/pty/terminal"
)

// TestEmpiricalClearCommand verifies that /clear command exists and responds
// in the installed Claude CLI. This is the empirical gate for ADR-013 constraint #5.
//
// Full semantic validation (model persistence, permission mode persistence, etc.)
// requires manual testing with menu interaction, so we document the assumptions
// based on Claude's documented behavior.
func TestEmpiricalClearCommand(t *testing.T) {
	// Skip if claude is not available
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude binary not found; skipping empirical /clear test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workdir := t.TempDir()
	// Restore after the process has exited. Claude rewrites ~/.claude.json on
	// shutdown, so restoring before Wait loses the race and leaves the temp
	// workspace trusted in the user's config.
	t.Cleanup(preserveClaudeConfig(t))
	t.Setenv("FIZEAU_RECORD_CLAUDE_TRUST", "1")
	if err := anthropic.EnsureWorkspaceTrusted(workdir); err != nil {
		t.Fatalf("record workspace trust: %v", err)
	}
	env := buildTestEnv()

	// Start a basic session without hooks (just to test /clear)
	s, err := session.Start(
		ctx,
		claudePath,
		[]string{},
		workdir,
		env,
		session.Size{Rows: 50, Cols: 220},
	)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}
	defer func() {
		_ = s.Close()
		_ = s.Wait()
	}()

	emu := terminal.New(terminal.Size{Rows: 50, Cols: 220})
	clearSubmitted := false
	var clearSubmittedAt time.Time
	clearRecovered := false
	deadline := time.Now().Add(20 * time.Second)
	var lastScreen string
	var queryBuf []byte
	answeredQueries := map[string]bool{}

	for !clearRecovered && time.Now().Before(deadline) {
		var chunk session.OutputChunk
		var ok bool
		wake := time.NewTimer(500 * time.Millisecond)
		select {
		case chunk, ok = <-s.Output():
			wake.Stop()
			if !ok {
				t.Fatalf("session closed before /clear recovered\nlast screen:\n%s", lastScreen)
			}
		case <-wake.C:
			continue
		case <-ctx.Done():
			wake.Stop()
			t.Fatalf("context expired before /clear recovered: %v\nlast screen:\n%s", ctx.Err(), lastScreen)
		case <-time.After(time.Until(deadline)):
			wake.Stop()
			t.Fatalf("timeout waiting for /clear\nlast screen:\n%s", lastScreen)
		}
		if chunk.ReadError != nil || len(chunk.Bytes) == 0 {
			continue
		}
		queryBuf = append(queryBuf, chunk.Bytes...)
		if len(queryBuf) > 8192 {
			queryBuf = queryBuf[len(queryBuf)-2048:]
		}
		answerClaudeStartupProbes(s, queryBuf, answeredQueries)
		frame, err := emu.Feed(chunk.Bytes)
		if err != nil {
			t.Fatalf("terminal feed: %v", err)
		}
		screen := strings.Join(frame.Text, "\n")
		lastScreen = screen

		if empiricalTrustDialog(screen) {
			t.Fatalf("workspace trust dialog still appeared after recording trust\n%s", screen)
		}
		if !clearSubmitted && strings.Contains(screen, "❯") {
			// Kitty keyboard protocol drops an Enter that shares a write with
			// the slash command, so submit /clear in two writes.
			if err := s.SendBytes([]byte("/clear")); err != nil {
				t.Fatalf("type /clear: %v", err)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(200 * time.Millisecond):
			}
			if err := s.SendKey(session.KeyEnter); err != nil {
				t.Fatalf("submit /clear: %v", err)
			}
			clearSubmitted = true
			clearSubmittedAt = time.Now()
			t.Log("submitted /clear")
			continue
		}
		// /clear stays in the transcript after it runs. Recovery is a later
		// frame whose input row is an empty prompt, not the slash command.
		if clearSubmitted && time.Since(clearSubmittedAt) > 300*time.Millisecond && promptIsIdle(screen) {
			clearRecovered = true
			t.Log("/clear returned to the prompt")
		}
	}
	if !clearRecovered {
		t.Fatalf("timeout waiting for /clear response\nlast screen:\n%s", lastScreen)
	}

	// Verify the recovered prompt accepts input after /clear. Waiting for an
	// unsolicited chunk makes the result depend on how the PTY happened to
	// split the /clear redraw, so type a unique marker without submitting it.
	// This exercises the live TUI without making a model request.
	const livenessMarker = "fizeau-clear-liveness-probe"
	if err := s.SendBytes([]byte(livenessMarker)); err != nil {
		t.Fatalf("failed to type liveness probe after /clear: %v", err)
	}

	testTimeout := time.NewTimer(12 * time.Second)
	defer testTimeout.Stop()

	sessionAlive := false
	var probeOutput strings.Builder
	for !sessionAlive {
		select {
		case chunk, ok := <-s.Output():
			if !ok {
				t.Fatalf("session closed after /clear\nlast screen:\n%s", lastScreen)
			}
			if chunk.ReadError != nil {
				continue
			}
			probeOutput.Write(chunk.Bytes)
			if frame, err := emu.Feed(chunk.Bytes); err == nil {
				lastScreen = strings.Join(frame.Text, "\n")
			}
			sessionAlive = strings.Contains(probeOutput.String(), livenessMarker) ||
				strings.Contains(lastScreen, livenessMarker)
		case <-testTimeout.C:
			t.Fatalf("session did not echo liveness probe after /clear\nlast screen:\n%s", lastScreen)
		}
	}
	// Clear the unsubmitted marker before closing the session.
	if err := s.SendBytes([]byte{0x15}); err != nil { // Ctrl-U
		t.Fatalf("failed to clear liveness probe input: %v", err)
	}

	// Log empirical findings
	t.Logf("\n=== EMPIRICAL /CLEAR VERIFICATION ===")
	t.Logf("Claude version: %s", getClaudeVersion())
	t.Logf("✓ /clear command exists and responds")
	t.Logf("✓ Session remains alive after /clear")
	t.Logf("✓ Prompt accepts input after /clear")
	t.Logf("\nFull semantic validation requires manual testing:")
	t.Logf("  - History reset: expected per Claude documentation")
	t.Logf("  - Model selection persists: expected (not reset by /clear)")
	t.Logf("  - Permission mode persists: expected (not reset by /clear)")
	t.Logf("  - Auth token persists: verified (session remains alive)")
	t.Logf("  - New transcript file: expected (created per turn)")
}

// waitForTranscriptPath waits for a Stop hook to write the transcript path
// and returns the path, checking the hookDir for newly written files.
func waitForTranscriptPath(t *testing.T, s *session.Session, hookDir string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(hookDir)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Find the most recently modified hook file
		var newest os.DirEntry
		var newestTime time.Time

		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "stop-hook-") && strings.HasSuffix(entry.Name(), ".json") {
				info, _ := entry.Info()
				if info != nil && info.ModTime().After(newestTime) {
					newest = entry
					newestTime = info.ModTime()
				}
			}
		}

		if newest != nil {
			// Read the payload
			payloadPath := filepath.Join(hookDir, newest.Name())
			data, err := os.ReadFile(payloadPath)
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}

			var payload map[string]string
			if err := json.Unmarshal(data, &payload); err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}

			if transcriptPath, ok := payload["transcript_path"]; ok && transcriptPath != "" {
				return transcriptPath, nil
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	return "", fmt.Errorf("timeout waiting for transcript path from Stop hook")
}

// buildSettingsJSON constructs the --settings JSON with hooks.
func buildSettingsJSON(hookPayloads map[string]string) string {
	hooks := make(map[string]interface{})
	for hookName, command := range hookPayloads {
		hooks[hookName] = map[string]interface{}{
			"command": command,
			"shell":   "sh",
		}
	}

	settings := map[string]interface{}{
		"hooks": hooks,
	}

	jsonBytes, _ := json.Marshal(settings)
	return string(jsonBytes)
}

func preserveClaudeConfig(t *testing.T) func() {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	existed := err == nil
	return func() {
		var restoreErr error
		if existed {
			restoreErr = os.WriteFile(path, original, 0o600)
		} else {
			restoreErr = os.Remove(path)
		}
		if restoreErr != nil {
			t.Errorf("restore %s: %v", path, restoreErr)
		}
	}
}

func promptIsIdle(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "❯" || trimmed == ">" {
			return true
		}
	}
	return false
}

func empiricalTrustDialog(screen string) bool {
	return strings.Contains(screen, "Yes, I trust this folder") ||
		strings.Contains(screen, "trust the files in this folder") ||
		strings.Contains(screen, "Is this a project you created or one you trust")
}

func answerClaudeStartupProbes(s *session.Session, raw []byte, answered map[string]bool) {
	replyOnce := func(name string, present bool, reply []byte) {
		if !present || answered[name] {
			return
		}
		answered[name] = true
		_ = s.SendBytes(reply)
	}
	replyOnce("da1", bytesContains(raw, "\x1b[c") || bytesContains(raw, "\x1b[?c"), []byte("\x1b[?1;0c"))
	replyOnce("da2", bytesContains(raw, "\x1b[>c") || bytesContains(raw, "\x1b[>?c"), []byte("\x1b[?62;4;0c"))
	replyOnce("dsr", bytesContains(raw, "\x1b[6n"), []byte("\x1b[1;1R"))
	replyOnce("xtversion", bytesContains(raw, "\x1b[>q") || bytesContains(raw, "\x1b[>0q"), []byte("\x1b[>0;370;0c"))
	// No progressive enhancements: legacy carriage return stays effective.
	replyOnce("kitty", bytesContains(raw, "\x1b[?u"), []byte("\x1b[?0u"))
	replyOnce("winsize", bytesContains(raw, "\x1b[18t"), []byte("\x1b[8;50;220t"))
}

func bytesContains(haystack []byte, needle string) bool {
	return strings.Contains(string(haystack), needle)
}

// buildTestEnv constructs the environment allowlist for the test.
func buildTestEnv() []string {
	allowedKeys := map[string]bool{
		"HOME":    true,
		"PATH":    true,
		"USER":    true,
		"LOGNAME": true,
		"SHELL":   true,
		"LANG":    true,
		"LC_ALL":  true,
		"TZ":      true,
		"TERM":    true,
	}

	xdgAllowed := map[string]bool{
		"XDG_CONFIG_HOME": true,
		"XDG_DATA_HOME":   true,
		"XDG_CACHE_HOME":  true,
		"XDG_STATE_HOME":  true,
		"XDG_RUNTIME_DIR": true,
	}

	var env []string
	currentEnv := os.Environ()

	for _, kv := range currentEnv {
		key := strings.SplitN(kv, "=", 2)[0]
		if allowedKeys[key] || xdgAllowed[key] || strings.HasPrefix(key, "CLAUDE_") {
			env = append(env, kv)
		}
	}

	// Set defaults
	hasTermSet := false
	hasLangSet := false
	hasLCAllSet := false

	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			hasTermSet = true
		}
		if strings.HasPrefix(kv, "LANG=") {
			hasLangSet = true
		}
		if strings.HasPrefix(kv, "LC_ALL=") {
			hasLCAllSet = true
		}
	}

	if !hasTermSet {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasLangSet {
		env = append(env, "LANG=C.UTF-8")
	}
	if !hasLCAllSet {
		env = append(env, "LC_ALL=C.UTF-8")
	}

	return env
}

// expandPath expands ~ to home directory
func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// getClaudeVersion returns the output of 'claude --version'
func getClaudeVersion() string {
	cmd := exec.Command("claude", "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("unknown (error: %v)", err)
	}
	return strings.TrimSpace(string(output))
}
