//go:build linux || darwin

package claudetui_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
	"github.com/easel/fizeau/internal/processlifecycle"
)

func TestClaudeTUIExecuteLeavesNoLiveSession(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "claude-tui-target.pid")
	grandchildFile := filepath.Join(dir, "claude-tui-grandchild.pid")
	writeClaudeTUILifecycleFixture(t, dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAUDE_TUI_TARGET_FILE", targetFile)
	t.Setenv("CLAUDE_TUI_GRANDCHILD_FILE", grandchildFile)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := (&claudetui.Harness{}).Execute(ctx, harnesses.ExecuteRequest{
		Prompt: "fixture", WorkDir: dir, SessionID: "claude-tui-lifecycle", SessionLogDir: filepath.Join(dir, "sessions"),
	})
	if err != nil {
		cancel()
		t.Fatalf("Execute: %v", err)
	}
	targetPID := waitForClaudeTUIPID(t, targetFile, 5*time.Second)
	grandchildPID := waitForClaudeTUIPID(t, grandchildFile, 5*time.Second)
	record := waitForClaudeTUIRecord(t, dir, 5*time.Second)
	pgid := parseClaudeTUIGroup(t, record.BoundaryIdentity)
	if targetPID != record.DirectChildIdentity.PID {
		t.Fatalf("target pid = %d, recorded direct child = %d", targetPID, record.DirectChildIdentity.PID)
	}
	if observed, err := syscall.Getpgid(grandchildPID); err != nil || observed != pgid {
		t.Fatalf("grandchild group = (%d, %v), want %d", observed, err, pgid)
	}
	cancel()

	finals := 0
	for event := range events {
		if event.Type != harnesses.EventTypeFinal {
			continue
		}
		finals++
		// This is the contract edge: the final must not become observable until
		// the request-local PTY leader and stubborn grandchild are both gone.
		assertClaudeTUIProcessGoneNow(t, targetPID)
		assertClaudeTUIProcessGoneNow(t, grandchildPID)
		if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("final observed while PTY process group %d remains: %v", pgid, err)
		}
	}
	if finals != 1 {
		t.Fatalf("final events = %d, want exactly 1", finals)
	}
	if _, err := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions")).Get(context.Background(), record.RecordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("completed claude-tui lifecycle record remains: %v", err)
	}
}

func writeClaudeTUILifecycleFixture(t *testing.T, dir string) {
	t.Helper()
	content := `#!/bin/sh
trap '' TERM HUP INT
printf '%s' "$$" > "$CLAUDE_TUI_TARGET_FILE"
sh -c 'trap "" TERM HUP INT; exec sleep 300' &
child=$!
printf '%s' "$child" > "$CLAUDE_TUI_GRANDCHILD_FILE"
printf '\033[H\033[2J\033[2;1H> '
wait "$child"
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(content), 0o700); err != nil {
		t.Fatalf("write claude fixture: %v", err)
	}
}

func waitForClaudeTUIPID(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForClaudeTUIRecord(t *testing.T, dir string, timeout time.Duration) processlifecycle.Record {
	t.Helper()
	registry := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions"))
	deadline := time.Now().Add(timeout)
	for {
		records, err := registry.List(context.Background())
		if err == nil {
			for _, record := range records {
				if record.Harness == "claude-tui" {
					return record
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for claude-tui lifecycle record: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func parseClaudeTUIGroup(t *testing.T, identity string) int {
	t.Helper()
	const prefix = "unix-pgid:"
	if !strings.HasPrefix(identity, prefix) {
		t.Fatalf("unexpected boundary identity %q", identity)
	}
	pgid, err := strconv.Atoi(strings.TrimPrefix(identity, prefix))
	if err != nil || pgid <= 1 {
		t.Fatalf("parse boundary identity %q: %v", identity, err)
	}
	outerPGID, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid(test): %v", err)
	}
	if pgid == outerPGID {
		t.Fatalf("unsafe claude-tui process group %d equals test group", pgid)
	}
	return pgid
}

func assertClaudeTUIProcessGoneNow(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("final observed while process %d remains: %v", pid, err)
	}
}
