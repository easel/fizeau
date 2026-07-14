//go:build linux || darwin

package session

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/processlifecycle"
)

const (
	ptyLifecycleTargetFile     = "pty-target.pid"
	ptyLifecycleGrandchildFile = "pty-grandchild.pid"
)

func TestPTYLifecycleReapsGrandchildOnClose(t *testing.T) {
	dir := t.TempDir()
	fixture := writePTYLifecycleFixture(t, dir)
	s, err := Start(context.Background(), fixture, []string{dir}, dir, nil, Size{Rows: 24, Cols: 80},
		WithLifecycleOptions(processlifecycle.BatchOptions{
			Harness: "pty-close", OperationID: "pty-close", SessionLogDir: filepath.Join(dir, "sessions"),
		}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	targetPID := waitForPIDFile(t, filepath.Join(dir, ptyLifecycleTargetFile), 3*time.Second)
	grandchildPID := waitForPIDFile(t, filepath.Join(dir, ptyLifecycleGrandchildFile), 3*time.Second)
	pgid, err := syscall.Getpgid(targetPID)
	if err != nil {
		t.Fatalf("Getpgid(target): %v", err)
	}
	assertSafePTYGroup(t, pgid)
	if observed, err := syscall.Getpgid(grandchildPID); err != nil || observed != pgid {
		t.Fatalf("grandchild group = (%d, %v), want %d", observed, err, pgid)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = s.Wait()
	waitForPTYGroupGone(t, pgid, 2*time.Second)
	waitForPTYProcessGone(t, targetPID, 2*time.Second)
	waitForPTYProcessGone(t, grandchildPID, 2*time.Second)
	registry := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions"))
	records, err := registry.List(context.Background())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("list lifecycle records: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("completed PTY lifecycle records remain: %#v", records)
	}
}

func TestPTYLifecycleDiesWithEmbeddingCaller(t *testing.T) {
	dir := t.TempDir()
	fixture := writePTYLifecycleFixture(t, dir)
	helper := exec.Command(os.Args[0], "-test.run=^TestPTYLifecycleEmbeddingHelper$", "-test.count=1")
	helper.Env = append(os.Environ(),
		"FIZEAU_PTY_LIFECYCLE_HELPER=1",
		"FIZEAU_PTY_LIFECYCLE_DIR="+dir,
		"FIZEAU_PTY_LIFECYCLE_FIXTURE="+fixture,
	)
	logFile, err := os.OpenFile(filepath.Join(dir, "pty-helper.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open helper log: %v", err)
	}
	helper.Stdout = logFile
	helper.Stderr = logFile
	if err := helper.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start helper: %v", err)
	}
	helperDone := false
	pgid := 0
	t.Cleanup(func() {
		if !helperDone {
			_ = helper.Process.Kill()
			_ = helper.Wait()
		}
		if pgid > 1 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		_ = logFile.Close()
	})

	targetPID := waitForPIDFile(t, filepath.Join(dir, ptyLifecycleTargetFile), 5*time.Second)
	grandchildPID := waitForPIDFile(t, filepath.Join(dir, ptyLifecycleGrandchildFile), 5*time.Second)
	record := waitForPTYLifecycleRecord(t, dir, "pty-caller-death", 5*time.Second)
	candidatePGID := parsePTYGroup(t, record.BoundaryIdentity)
	assertSafePTYGroup(t, candidatePGID)
	pgid = candidatePGID
	if targetPID != record.DirectChildIdentity.PID {
		t.Fatalf("target pid = %d, recorded direct child = %d", targetPID, record.DirectChildIdentity.PID)
	}
	if observed, err := syscall.Getpgid(grandchildPID); err != nil || observed != pgid {
		t.Fatalf("grandchild group before caller death = (%d, %v), want %d", observed, err, pgid)
	}

	started := time.Now()
	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL helper: %v", err)
	}
	if err := helper.Wait(); err == nil {
		t.Fatal("SIGKILLed helper exited successfully")
	}
	helperDone = true
	_ = logFile.Close()
	waitForPTYGroupGone(t, pgid, 7*time.Second)
	waitForPTYProcessGone(t, record.SupervisorIdentity.PID, 2*time.Second)
	waitForPTYProcessGone(t, targetPID, 2*time.Second)
	waitForPTYProcessGone(t, grandchildPID, 2*time.Second)
	if elapsed := time.Since(started); elapsed > 7*time.Second {
		t.Fatalf("caller-death cleanup exceeded configured bound: %s", elapsed)
	}
	if _, err := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions")).Get(context.Background(), record.RecordID); err != nil {
		t.Fatalf("caller-death recovery record missing: %v", err)
	}
}

func TestPTYLifecycleEmbeddingHelper(t *testing.T) {
	if os.Getenv("FIZEAU_PTY_LIFECYCLE_HELPER") != "1" {
		t.Skip("embedding helper only")
	}
	dir := os.Getenv("FIZEAU_PTY_LIFECYCLE_DIR")
	fixture := os.Getenv("FIZEAU_PTY_LIFECYCLE_FIXTURE")
	s, err := Start(context.Background(), fixture, []string{dir}, dir, nil, Size{Rows: 24, Cols: 80},
		WithLifecycleOptions(processlifecycle.BatchOptions{
			Harness: "pty-caller-death", OperationID: "pty-caller-death", SessionLogDir: filepath.Join(dir, "sessions"),
		}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for range s.Output() {
	}
	_ = s.Wait()
}

func writePTYLifecycleFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "pty-lifecycle-fixture")
	content := `#!/bin/sh
trap '' TERM HUP INT
dir=$1
printf '%s' "$$" > "$dir/pty-target.pid"
sh -c 'trap "" TERM HUP INT; exec sleep 300' &
child=$!
printf '%s' "$child" > "$dir/pty-grandchild.pid"
wait "$child"
`
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func waitForPTYLifecycleRecord(t *testing.T, dir, harness string, timeout time.Duration) processlifecycle.Record {
	t.Helper()
	registry := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions"))
	deadline := time.Now().Add(timeout)
	for {
		records, err := registry.List(context.Background())
		if err == nil {
			for _, record := range records {
				if record.Harness == harness {
					return record
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s lifecycle record: %v", harness, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPIDFile(t *testing.T, path string, timeout time.Duration) int {
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
			t.Fatalf("timed out waiting for PID file %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func parsePTYGroup(t *testing.T, identity string) int {
	t.Helper()
	const prefix = "unix-pgid:"
	if !strings.HasPrefix(identity, prefix) {
		t.Fatalf("unexpected boundary identity %q", identity)
	}
	pgid, err := strconv.Atoi(strings.TrimPrefix(identity, prefix))
	if err != nil || pgid <= 0 {
		t.Fatalf("parse boundary identity %q: %v", identity, err)
	}
	return pgid
}

func assertSafePTYGroup(t *testing.T, pgid int) {
	t.Helper()
	outerPGID, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid(test): %v", err)
	}
	if pgid <= 1 || pgid == outerPGID {
		t.Fatalf("unsafe PTY process group %d (outer %d)", pgid, outerPGID)
	}
}

func waitForPTYGroupGone(t *testing.T, pgid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d remains: %v", pgid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPTYProcessGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d remains: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
