//go:build linux || darwin

package processlifecycle

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUnixBatchGatePersistsIdentitiesBeforeExec(t *testing.T) {
	dir := t.TempDir()
	registryDir := filepath.Join(dir, "registry")
	marker := filepath.Join(dir, "target-observation")
	target := exec.Command("sh", "-c", `
		if find "$LIFECYCLE_REGISTRY" -maxdepth 1 -name '*.json' -print -quit | grep -q .; then
			printf persisted > "$LIFECYCLE_MARKER"
		else
			printf missing > "$LIFECYCLE_MARKER"
		fi
	`)
	target.Env = append(os.Environ(), "LIFECYCLE_REGISTRY="+registryDir, "LIFECYCLE_MARKER="+marker)
	var stderr bytes.Buffer
	target.Stderr = &stderr

	batch, err := StartBatch(context.Background(), target, BatchOptions{
		Harness:        "codex",
		OperationID:    "gate-test",
		CleanupTimeout: 3 * time.Second,
		GracePeriod:    20 * time.Millisecond,
		Registry:       NewFileRegistry(registryDir),
	})
	if err != nil {
		t.Fatalf("StartBatch: %v (%s)", err, stderr.String())
	}
	record := batch.Record()
	if record.OwnerIdentity.PID != os.Getpid() {
		t.Fatalf("owner pid = %d, want %d", record.OwnerIdentity.PID, os.Getpid())
	}
	if record.SupervisorIdentity.PID == record.DirectChildIdentity.PID {
		t.Fatalf("supervisor and gated direct child share pid %d", record.SupervisorIdentity.PID)
	}
	if record.DirectChildIdentity.PID != record.BoundaryProcessIdentity.PID {
		t.Fatalf("direct child pid %d does not anchor boundary pid %d", record.DirectChildIdentity.PID, record.BoundaryProcessIdentity.PID)
	}
	if record.DirectChildIdentity.BirthToken == "" || record.SupervisorIdentity.BirthToken == "" {
		t.Fatal("birth identities were not captured before gate release")
	}
	wantScheme := map[string]string{
		"linux":  "linux-boot-id+proc-starttime-ticks/v1",
		"darwin": "darwin-boottime+sysctl-starttime-usec/v1",
	}[runtime.GOOS]
	if record.DirectChildIdentity.BirthTokenScheme != wantScheme || !strings.Contains(record.DirectChildIdentity.BirthToken, ":") {
		t.Fatalf("direct-child birth token is not reboot-safe on %s: %#v", runtime.GOOS, record.DirectChildIdentity)
	}
	if err := batch.Wait(); err != nil {
		t.Fatalf("Wait: %v (%s)", err, stderr.String())
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read target marker: %v", err)
	}
	if string(data) != "persisted" {
		t.Fatalf("target observed %q ownership state, want persisted", data)
	}
	if _, err := os.Stat(filepath.Join(registryDir, record.RecordID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("completed record was not deleted: %v", err)
	}
}

func TestUnixBatchCancellationUsesDetachedCleanup(t *testing.T) {
	dir := t.TempDir()
	registry := NewFileRegistry(filepath.Join(dir, "registry"))
	childPIDPath := filepath.Join(dir, "grandchild-pid")
	target := exec.Command("sh", "-c", `sleep 300 & printf '%d' "$!" > "$CHILD_PID"; wait`)
	target.Env = append(os.Environ(), "CHILD_PID="+childPIDPath)
	ctx, cancel := context.WithCancel(context.Background())
	batch, err := StartBatch(ctx, target, BatchOptions{
		Harness: "codex", OperationID: "cancel-test", Registry: registry,
		CleanupTimeout: 3 * time.Second, GracePeriod: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartBatch: %v", err)
	}
	record := batch.Record()
	waitForFile(t, childPIDPath, time.Second)
	cancel()
	if err := batch.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context cancellation", err)
	}
	pgid, err := parseUnixProcessGroupIdentity(record.BoundaryIdentity)
	if err != nil {
		t.Fatalf("parse boundary: %v", err)
	}
	waitForGroupEmpty(t, pgid, time.Second)
	if _, err := registry.Get(context.Background(), record.RecordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancelled boundary record retained after proven cleanup: %v", err)
	}
}

func TestUnixBatchWatcherCompletesWithoutWaitCaller(t *testing.T) {
	registry := NewMemoryRegistry()
	batch, err := StartBatch(context.Background(), exec.Command("true"), BatchOptions{
		Harness: "pi", OperationID: "watcher-test", Registry: registry,
		CleanupTimeout: time.Second, GracePeriod: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartBatch: %v", err)
	}
	recordID := batch.Record().RecordID
	select {
	case <-batch.waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor waiter did not complete")
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, getErr := registry.Get(context.Background(), recordID)
		if errors.Is(getErr, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completion watcher did not clean record: %v", getErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := batch.Wait(); err != nil {
		t.Fatalf("Wait reusing background result: %v", err)
	}
}

func TestUnixPortableLaunchAttachmentRejectsBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	target := exec.Command(os.Args[0], "-test.run=^TestUnixPortableLaunchAttachmentSpawnHelper$")
	target.Env = append(os.Environ(), "FIZEAU_PORTABLE_LAUNCH_MARKER="+marker)
	attachment, err := NewPortableLaunchAttachment(target.Path, target.Args[1:], target.Env, portableLaunchTestRecipe{})
	if err != nil {
		t.Fatalf("NewPortableLaunchAttachment: %v", err)
	}
	// A changed argv[0] is an alias: exec would still run target.Path, but the
	// portable boundary must refuse a caller that has altered the sealed plan.
	target.Args[0] += ".alias"
	_, err = StartBatch(context.Background(), target, BatchOptions{
		Harness: "portable", Registry: NewMemoryRegistry(), PortableLaunch: attachment,
	})
	if err == nil {
		t.Fatal("StartBatch accepted an aliased portable target")
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("aliased portable target started before rejection: %v", statErr)
	}
}

func TestUnixPortablePTYLaunchUsesSealedAttachment(t *testing.T) {
	attachment, err := NewPortableLaunchAttachment("/bin/true", nil, []string{}, portableLaunchTestRecipe{})
	if err != nil {
		t.Fatalf("NewPortableLaunchAttachment: %v", err)
	}
	terminal, err := StartPTYCommand(context.Background(), "/bin/true", nil, "", []string{}, PTYSize{Rows: 24, Cols: 80}, BatchOptions{
		Harness: "portable", Registry: NewMemoryRegistry(), PortableLaunch: attachment,
	})
	if err != nil {
		t.Fatalf("StartPTYCommand: %v", err)
	}
	if err := terminal.Wait(); err != nil {
		t.Fatalf("portable PTY Wait: %v", err)
	}
}

func TestUnixPortableLaunchAttachmentSpawnHelper(t *testing.T) {
	marker := os.Getenv("FIZEAU_PORTABLE_LAUNCH_MARKER")
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestUnixPreparedAbortIsBoundedAndIndeterminateWithoutBoundary(t *testing.T) {
	command := exec.Command("sh", "-c", "exec sleep 300")
	if err := command.Start(); err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	prepared := &unixPreparedBoundary{cmd: command, cleanupTimeout: 30 * time.Millisecond}
	started := time.Now()
	result, err := prepared.Abort(context.Background())
	if result.Status != AbortIndeterminate || err == nil {
		t.Fatalf("Abort = (%#v, %v), want bounded indeterminate", result, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Abort took %s, want bounded cleanup", elapsed)
	}
}

func TestInheritedFileRejectsUnsafeDescriptorRanges(t *testing.T) {
	if _, err := inheritedFile(-1, "negative"); err == nil {
		t.Fatal("negative inherited descriptor was accepted")
	}
	if _, err := inheritedFile(maxLifecycleInheritedFD+1, "too-large"); err == nil {
		t.Fatal("oversized inherited descriptor was accepted")
	}
	if _, err := inheritedFiles(maxLifecycleInheritedFD-1, 3, "overflow"); err == nil {
		t.Fatal("overflowing inherited descriptor range was accepted")
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForGroupEmpty(t *testing.T, pgid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		alive, err := unixProcessGroupAlive(pgid)
		if err == nil && !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %s still alive: %v", strconv.Itoa(pgid), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
