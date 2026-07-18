//go:build linux || darwin

package harnesses_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/claude"
	"github.com/easel/fizeau/internal/harnesses/codex"
	"github.com/easel/fizeau/internal/harnesses/gemini"
	"github.com/easel/fizeau/internal/harnesses/opencode"
	"github.com/easel/fizeau/internal/harnesses/pi"
	"github.com/easel/fizeau/internal/processlifecycle"
)

const (
	targetPIDFile     = "lifecycle-target.pid"
	grandchildPIDFile = "lifecycle-grandchild.pid"
)

type boundBatchRecipe struct{}

func (boundBatchRecipe) PortableRuntimeNamespaceRecipe() {}

type boundBatchHarness interface {
	harnesses.Harness
	harnesses.PortableRuntimeRunnerBinder
}

// TestUnixBoundBatchRunnersIgnoreAmbientBinaryResolution proves every batch
// adapter consumes the activation-owned command and closed environment. PATH
// intentionally has no executable so a runner that falls back to Binary or
// LookPath cannot pass this test.
func TestUnixBoundBatchRunnersIgnoreAmbientBinaryResolution(t *testing.T) {
	factories := map[string]func() boundBatchHarness{
		"claude":   func() boundBatchHarness { return &claude.Runner{NativeMode: false} },
		"codex":    func() boundBatchHarness { return &codex.Runner{} },
		"gemini":   func() boundBatchHarness { return &gemini.Runner{} },
		"opencode": func() boundBatchHarness { return &opencode.Runner{} },
		"pi":       func() boundBatchHarness { return &pi.Runner{} },
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			guestRoot := filepath.Join(dir, "guest")
			if err := os.MkdirAll(filepath.Join(guestRoot, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(dir, "capture")
			launcher := filepath.Join(guestRoot, "bin", "runner")
			if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf 'path=%s\\nmarker=%s\\n' \"$PATH\" \"$BOUND_MARKER\" > \"$CAPTURE\"\nprintf 'arg=%s\\n' \"$@\" >> \"$CAPTURE\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			binding, err := harnesses.NewPortableRuntimeRunnerBinding(harnesses.PortableRuntimeRunnerBindingInput{
				Structure: harnesses.PortableRuntimeStructure{Name: name, Transport: harnesses.PortableRuntimeTransportSubprocess, Mode: harnesses.PortableRuntimeStructuralUnpinned},
				GuestRoot: guestRoot, ClosureClass: harnesses.PortableRuntimeClosureStatic,
				Launch:          harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/runner"},
				FixedArguments:  []string{"--manifest-fixed"},
				Environment:     map[string]string{"PATH": filepath.Join(dir, "poison-path"), "BOUND_MARKER": name, "CAPTURE": capture},
				NamespaceRecipe: boundBatchRecipe{},
			})
			if err != nil {
				t.Fatal(err)
			}
			runner := factory()
			if err := runner.BindPortableRuntime(binding); err != nil {
				t.Fatal(err)
			}
			events, err := runner.Execute(context.Background(), harnesses.ExecuteRequest{Prompt: "bound-request", WorkDir: dir, SessionLogDir: filepath.Join(dir, "sessions"), SessionID: "bound-" + name})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			drainHarnessEvents(t, events, 4*time.Second)
			captured, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("bound launcher did not run: %v", err)
			}
			output := string(captured)
			if !strings.Contains(output, "path="+filepath.Join(dir, "poison-path")) || !strings.Contains(output, "marker="+name) {
				t.Fatalf("bound closed environment was not used: %q", output)
			}
			var argv []string
			for _, line := range strings.Split(output, "\n") {
				if argument, ok := strings.CutPrefix(line, "arg="); ok {
					argv = append(argv, argument)
				}
			}
			requestIndex := -1
			for index, argument := range argv {
				if argument == "bound-request" {
					requestIndex = index
					break
				}
			}
			if len(argv) < 3 || argv[0] != "--manifest-fixed" || requestIndex < 2 {
				t.Fatalf("bound fixed/request argv was not used: %q", output)
			}
		})
	}
}

func TestUnixBatchLifecycleAppliesToEveryBatchRunner(t *testing.T) {
	factories := map[string]func(string) harnesses.Harness{
		"claude": func(binary string) harnesses.Harness {
			return &claude.Runner{Binary: binary, NativeMode: false}
		},
		"codex":    func(binary string) harnesses.Harness { return &codex.Runner{Binary: binary} },
		"gemini":   func(binary string) harnesses.Harness { return &gemini.Runner{Binary: binary} },
		"opencode": func(binary string) harnesses.Harness { return &opencode.Runner{Binary: binary} },
		"pi":       func(binary string) harnesses.Harness { return &pi.Runner{Binary: binary} },
	}
	wantHarnesses := []string{"claude", "codex", "gemini", "opencode", "pi"}
	gotHarnesses := make([]string, 0, len(factories))
	for name := range factories {
		gotHarnesses = append(gotHarnesses, name)
	}
	sort.Strings(gotHarnesses)
	if fmt.Sprint(gotHarnesses) != fmt.Sprint(wantHarnesses) {
		t.Fatalf("batch conformance harnesses = %v, want exactly %v", gotHarnesses, wantHarnesses)
	}

	for _, name := range wantHarnesses {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fixture := writeBatchFixture(t, dir)
			ctx, cancel := context.WithCancel(context.Background())
			events, err := factories[name](fixture).Execute(ctx, harnesses.ExecuteRequest{
				Prompt: "fixture", WorkDir: dir, SessionLogDir: filepath.Join(dir, "sessions"), SessionID: "conformance-" + name,
			})
			if err != nil {
				cancel()
				t.Fatalf("Execute: %v", err)
			}
			targetPID := waitForPIDFile(t, filepath.Join(dir, targetPIDFile), 2*time.Second)
			record := waitForLifecycleRecord(t, dir, name, 2*time.Second)
			if record.SchemaID != processlifecycle.RecordSchemaID || record.BoundaryType != processlifecycle.BoundaryTypeUnixProcessGroup {
				t.Fatalf("unexpected lifecycle record: %#v", record)
			}
			if record.SupervisorIdentity.PID == record.DirectChildIdentity.PID || record.DirectChildIdentity.PID != record.BoundaryProcessIdentity.PID {
				t.Fatalf("supervisor/direct/boundary identities are not separated: %#v", record)
			}
			pgid := lifecyclePGID(t, record)
			assertSafeBoundary(t, pgid)
			if targetPID != record.DirectChildIdentity.PID {
				t.Fatalf("target pid = %d, want recorded direct child %d", targetPID, record.DirectChildIdentity.PID)
			}
			cancel()
			drainHarnessEvents(t, events, 4*time.Second)
			waitForProcessGroupGone(t, pgid, 2*time.Second)
			if _, err := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions")).Get(context.Background(), record.RecordID); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("completed lifecycle record remains: %v", err)
			}
		})
	}
}

func TestUnixBatchCancellationReapsGrandchild(t *testing.T) {
	dir := t.TempDir()
	fixture := writeBatchFixture(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	events, err := (&codex.Runner{Binary: fixture}).Execute(ctx, harnesses.ExecuteRequest{
		Prompt: "fixture", WorkDir: dir, SessionLogDir: filepath.Join(dir, "sessions"), SessionID: "cancel-grandchild",
	})
	if err != nil {
		cancel()
		t.Fatalf("Execute: %v", err)
	}
	// Process startup can be delayed while the full repository test suite and
	// pre-push hooks run concurrently. Keep evidence discovery tolerant without
	// relaxing the cleanup deadlines asserted after cancellation.
	grandchildPID := waitForPIDFile(t, filepath.Join(dir, grandchildPIDFile), 10*time.Second)
	record := waitForLifecycleRecord(t, dir, "codex", 10*time.Second)
	pgid := lifecyclePGID(t, record)
	assertSafeBoundary(t, pgid)
	if targetPID := waitForPIDFile(t, filepath.Join(dir, targetPIDFile), time.Second); targetPID != record.DirectChildIdentity.PID {
		t.Fatalf("target pid = %d, want recorded direct child %d", targetPID, record.DirectChildIdentity.PID)
	}
	observedPGID, err := syscall.Getpgid(grandchildPID)
	if err != nil {
		t.Fatalf("Getpgid(grandchild): %v", err)
	}
	if observedPGID != pgid {
		t.Fatalf("grandchild pgid = %d, want saved boundary %d", observedPGID, pgid)
	}

	cancel()
	drainHarnessEvents(t, events, 4*time.Second)
	waitForProcessGroupGone(t, pgid, 2*time.Second)
	waitForProcessGone(t, grandchildPID, 2*time.Second)
}

func TestSubprocessHarnessDiesWithEmbeddingCaller(t *testing.T) {
	dir := t.TempDir()
	fixture := writeBatchFixture(t, dir)
	helper := exec.Command(os.Args[0], "-test.run=^TestUnixBatchEmbeddingHelper$", "-test.count=1")
	helper.Env = append(os.Environ(),
		"FIZEAU_LIFECYCLE_EMBED_HELPER=1",
		"FIZEAU_LIFECYCLE_EMBED_DIR="+dir,
		"FIZEAU_LIFECYCLE_EMBED_BINARY="+fixture,
	)
	helperLogPath := filepath.Join(dir, "embedding-helper.log")
	helperLog, err := os.OpenFile(helperLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open embedding helper log: %v", err)
	}
	helper.Stdout = helperLog
	helper.Stderr = helperLog
	if err := helper.Start(); err != nil {
		_ = helperLog.Close()
		t.Fatalf("start embedding helper: %v", err)
	}
	helperDone := false
	helperLogClosed := false
	pgid := 0
	t.Cleanup(func() {
		if !helperDone {
			_ = helper.Process.Kill()
			_ = helper.Wait()
		}
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		if !helperLogClosed {
			_ = helperLog.Close()
		}
	})

	grandchildPID := waitForPIDFile(t, filepath.Join(dir, grandchildPIDFile), 3*time.Second)
	record := waitForLifecycleRecord(t, dir, "codex", 3*time.Second)
	candidatePGID := lifecyclePGID(t, record)
	assertSafeBoundary(t, candidatePGID)
	pgid = candidatePGID
	targetPID := waitForPIDFile(t, filepath.Join(dir, targetPIDFile), time.Second)
	if targetPID != record.DirectChildIdentity.PID {
		t.Fatalf("target pid = %d, want recorded direct child %d", targetPID, record.DirectChildIdentity.PID)
	}
	if observed, err := syscall.Getpgid(grandchildPID); err != nil || observed != pgid {
		t.Fatalf("grandchild containment before helper death = (%d, %v), want pgid %d", observed, err, pgid)
	}

	started := time.Now()
	if err := helper.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL embedding helper: %v", err)
	}
	if err := helper.Wait(); err == nil {
		t.Fatal("SIGKILLed embedding helper exited successfully")
	}
	helperDone = true
	_ = helperLog.Close()
	helperLogClosed = true
	waitForProcessGroupGone(t, pgid, 7*time.Second)
	waitForProcessGone(t, record.SupervisorIdentity.PID, 2*time.Second)
	waitForProcessGone(t, targetPID, 2*time.Second)
	waitForProcessGone(t, grandchildPID, 2*time.Second)
	if elapsed := time.Since(started); elapsed > 7*time.Second {
		t.Fatalf("caller-death cleanup exceeded configured bound: %s", elapsed)
	}
	if _, err := processlifecycle.NewFileRegistry(filepath.Join(dir, "harness-sessions")).Get(context.Background(), record.RecordID); err != nil {
		helperOutput, _ := os.ReadFile(helperLogPath)
		t.Fatalf("caller-death recovery evidence was not retained: %v\nhelper output:\n%s", err, helperOutput)
	}
}

func TestUnixBatchEmbeddingHelper(t *testing.T) {
	if os.Getenv("FIZEAU_LIFECYCLE_EMBED_HELPER") != "1" {
		t.Skip("embedding helper only")
	}
	dir := os.Getenv("FIZEAU_LIFECYCLE_EMBED_DIR")
	binary := os.Getenv("FIZEAU_LIFECYCLE_EMBED_BINARY")
	events, err := (&codex.Runner{Binary: binary}).Execute(context.Background(), harnesses.ExecuteRequest{
		Prompt: "fixture", WorkDir: dir, SessionLogDir: filepath.Join(dir, "sessions"), SessionID: "embedding-helper",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for range events {
	}
}

func writeBatchFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "batch-fixture")
	content := `#!/bin/sh
trap '' TERM
printf '%s\n' "$$" > lifecycle-target.pid
sh -c 'trap "" TERM; exec sleep 300' &
child=$!
printf '%s\n' "$child" > lifecycle-grandchild.pid
wait "$child"
`
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func waitForLifecycleRecord(t *testing.T, dir, harness string, timeout time.Duration) processlifecycle.Record {
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

func drainHarnessEvents(t *testing.T, events <-chan harnesses.Event, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("timed out waiting for harness event stream to close after cleanup")
		}
	}
}

func lifecyclePGID(t *testing.T, record processlifecycle.Record) int {
	t.Helper()
	const prefix = "unix-pgid:"
	if !strings.HasPrefix(record.BoundaryIdentity, prefix) {
		t.Fatalf("unexpected boundary identity %q", record.BoundaryIdentity)
	}
	pgid, err := strconv.Atoi(strings.TrimPrefix(record.BoundaryIdentity, prefix))
	if err != nil || pgid <= 0 {
		t.Fatalf("parse boundary identity %q: %v", record.BoundaryIdentity, err)
	}
	return pgid
}

func assertSafeBoundary(t *testing.T, pgid int) {
	t.Helper()
	outerPGID, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid(test process): %v", err)
	}
	if pgid <= 1 || pgid == outerPGID {
		t.Fatalf("unsafe saved process group %d (outer test group %d)", pgid, outerPGID)
	}
}

func waitForPIDFile(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastData []byte
	var lastErr error
	for {
		data, err := os.ReadFile(path)
		lastData, lastErr = data, err
		// The fixture writes a trailing newline in the same printf operation.
		// Seeing it proves the reader did not race the shell's open-before-write
		// window or consume a partial PID.
		if err == nil && bytes.HasSuffix(data, []byte{'\n'}) {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
			lastErr = parseErr
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for complete pid file %s (data %q): %v", path, lastData, lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcessGroupGone(t *testing.T, pgid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d remains alive: %v", pgid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcessGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d remains alive: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
