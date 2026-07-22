//go:build windows

package processlifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsHelperRoleEnv = "FIZEAU_WINDOWS_JOB_TEST_ROLE"
	windowsHelperPIDFile = "FIZEAU_WINDOWS_JOB_TEST_PID_FILE"
)

type fakeWindowsJobAPI struct {
	attributes  windows.SecurityAttributes
	handleMask  uint32
	handleFlags uint32
	limits      windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
}

func (a *fakeWindowsJobAPI) CreateJobObject(attributes *windows.SecurityAttributes, _ *uint16) (windows.Handle, error) {
	a.attributes = *attributes
	return windows.Handle(42), nil
}

func (a *fakeWindowsJobAPI) SetHandleInformation(_ windows.Handle, mask, flags uint32) error {
	a.handleMask = mask
	a.handleFlags = flags
	return nil
}

func (a *fakeWindowsJobAPI) SetExtendedLimitInformation(_ windows.Handle, limits *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
	a.limits = *limits
	return nil
}

func (a *fakeWindowsJobAPI) CloseHandle(windows.Handle) error {
	return nil
}

func TestWindowsJobPolicyIsNonInheritableAndKillOnClose(t *testing.T) {
	api := &fakeWindowsJobAPI{}
	_, err := createWindowsJob(api)
	if err != nil {
		t.Fatalf("createWindowsJob: %v", err)
	}
	if api.attributes.InheritHandle != 0 {
		t.Fatalf("job security attributes inherit = %d, want 0", api.attributes.InheritHandle)
	}
	if api.handleMask != windows.HANDLE_FLAG_INHERIT || api.handleFlags != 0 {
		t.Fatalf("SetHandleInformation = mask %#x flags %#x, want inherit mask cleared", api.handleMask, api.handleFlags)
	}
	if got := api.limits.BasicLimitInformation.LimitFlags; got != windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE {
		t.Fatalf("job limit flags = %#x, want KILL_ON_JOB_CLOSE", got)
	}
}

func TestWindowsJobKillOnOwnerHandleClose(t *testing.T) {
	batch, _, processHandles := startWindowsLifecycleFixture(t, "target-wait")
	assertLiveWindowsJobPolicy(t, batch.backend.job)
	if err := batch.backend.job.close(); err != nil {
		t.Fatalf("close owner Job handle: %v", err)
	}
	waitWindowsHandlesSignaled(t, processHandles, 5*time.Second)
	select {
	case <-batch.processDone:
	case <-time.After(5 * time.Second):
		t.Fatal("direct child waiter did not observe kill-on-close")
	}
}

func TestWindowsJobReapsGrandchild(t *testing.T) {
	batch, registry, processHandles := startWindowsLifecycleFixture(t, "target-wait")
	recordID := batch.Record().RecordID
	if err := batch.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop waits for the job's active-process count to reach zero, but the
	// kernel can decrement that count a beat before it signals the process
	// handles, so an instant zero-timeout inspection races OS bookkeeping.
	// A bounded wait still proves the grandchild was reaped.
	waitWindowsHandlesSignaled(t, processHandles, 5*time.Second)
	if _, err := registry.Get(context.Background(), recordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("completed lifecycle record retained: %v", err)
	}
}

func TestWindowsCleanupWaitsForZeroActiveProcesses(t *testing.T) {
	batch, registry, _ := startWindowsLifecycleFixture(t, "target-exit")
	jobHandle := duplicateWindowsJobHandle(t, batch.backend.job)
	recordID := batch.Record().RecordID
	if err := batch.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	active, err := queryWindowsJobActiveProcesses(jobHandle)
	if err != nil {
		t.Fatalf("query retained Job Object after Wait: %v", err)
	}
	if active != 0 {
		t.Fatalf("cleanup returned with %d active Job Object processes", active)
	}
	if _, err := registry.Get(context.Background(), recordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cleanup returned before deleting empty-boundary record: %v", err)
	}
}

func TestWindowsBatchPreservesCommandIOEnvironmentAndDirectory(t *testing.T) {
	workDir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLifecycleHelper$")
	cmd.Dir = workDir
	cmd.Env = replaceWindowsTestEnv(os.Environ(), map[string]string{
		windowsHelperRoleEnv:            "echo",
		"FIZEAU_WINDOWS_JOB_TEST_VALUE": "preserved",
	})
	cmd.Stdin = strings.NewReader("stdin-value")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	batch, err := StartBatch(context.Background(), cmd, BatchOptions{Harness: "codex", Registry: NewMemoryRegistry()})
	if err != nil {
		t.Fatalf("StartBatch: %v", err)
	}
	if err := batch.Wait(); err != nil {
		t.Fatalf("Wait: %v (%s)", err, output.String())
	}
	want := "echo:preserved:" + workDir + ":stdin-value"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("child output = %q, want substring %q", output.String(), want)
	}
}

func TestWindowsJobRejectsBreakawayBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLifecycleHelper$")
	cmd.Env = append(os.Environ(), windowsHelperRoleEnv+"=mark", windowsHelperPIDFile+"="+marker)
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	_, err := StartBatch(context.Background(), cmd, BatchOptions{Harness: "codex", Registry: NewMemoryRegistry()})
	if !errors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("breakaway launch error = %v, want ErrPlatformUnsupported", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("breakaway command started before rejection: %v", err)
	}
}

func TestWindowsLifecycleHelper(t *testing.T) {
	role := os.Getenv(windowsHelperRoleEnv)
	if role == "" {
		return
	}
	pidFile := os.Getenv(windowsHelperPIDFile)
	switch role {
	case "mark":
		if err := os.WriteFile(pidFile, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "grandchild":
		time.Sleep(10 * time.Minute)
	case "echo":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		workDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("echo:%s:%s:%s", os.Getenv("FIZEAU_WINDOWS_JOB_TEST_VALUE"), workDir, input)
	case "target-wait", "target-exit":
		grandchild := exec.Command(os.Args[0], "-test.run=^TestWindowsLifecycleHelper$")
		grandchild.Env = replaceWindowsTestEnv(os.Environ(), map[string]string{
			windowsHelperRoleEnv: "grandchild",
			windowsHelperPIDFile: pidFile,
		})
		if err := grandchild.Start(); err != nil {
			t.Fatalf("start grandchild: %v", err)
		}
		contents := fmt.Sprintf("%d\n%d\n", os.Getpid(), grandchild.Process.Pid)
		if err := os.WriteFile(pidFile, []byte(contents), 0o600); err != nil {
			t.Fatalf("write fixture PIDs: %v", err)
		}
		if role == "target-wait" {
			time.Sleep(10 * time.Minute)
		} else {
			// Give the parent test time to retain process handles before this
			// direct child exits and lifecycle cleanup kills the grandchild.
			time.Sleep(250 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown Windows lifecycle helper role %q", role)
	}
}

func startWindowsLifecycleFixture(t *testing.T, role string) (*Batch, *MemoryRegistry, []windows.Handle) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pids")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsLifecycleHelper$")
	cmd.Env = replaceWindowsTestEnv(os.Environ(), map[string]string{
		windowsHelperRoleEnv: role,
		windowsHelperPIDFile: pidFile,
	})
	registry := NewMemoryRegistry()
	batch, err := StartBatch(context.Background(), cmd, BatchOptions{
		Harness: "codex", OperationID: "windows-live-" + role, Registry: registry,
		CleanupTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("StartBatch: %v", err)
	}
	pids := waitWindowsFixturePIDs(t, pidFile, 5*time.Second)
	handles := make([]windows.Handle, 0, len(pids))
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			for _, opened := range handles {
				windows.CloseHandle(opened)
			}
			t.Fatalf("open fixture process %d: %v", pid, err)
		}
		handles = append(handles, handle)
	}
	t.Cleanup(func() {
		_ = batch.Stop()
		for _, handle := range handles {
			_ = windows.CloseHandle(handle)
		}
	})
	return batch, registry, handles
}

func waitWindowsFixturePIDs(t *testing.T, path string, timeout time.Duration) []int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			lines := strings.Fields(string(data))
			if len(lines) == 2 {
				pids := make([]int, 0, 2)
				for _, line := range lines {
					pid, parseErr := strconv.Atoi(line)
					if parseErr != nil {
						t.Fatalf("parse fixture PID %q: %v", line, parseErr)
					}
					pids = append(pids, pid)
				}
				return pids
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Windows fixture PIDs: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitWindowsHandlesSignaled(t *testing.T, handles []windows.Handle, timeout time.Duration) {
	t.Helper()
	for _, handle := range handles {
		event, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
		if err != nil {
			t.Fatalf("wait for fixture process: %v", err)
		}
		if event != windows.WAIT_OBJECT_0 {
			t.Fatalf("fixture process remained live; wait event = %#x", event)
		}
	}
}

func assertWindowsHandlesSignaled(t *testing.T, handles []windows.Handle) {
	t.Helper()
	for _, handle := range handles {
		event, err := windows.WaitForSingleObject(handle, 0)
		if err != nil {
			t.Fatalf("inspect fixture process: %v", err)
		}
		if event != windows.WAIT_OBJECT_0 {
			t.Fatalf("cleanup returned before fixture process exit; wait event = %#x", event)
		}
	}
}

func assertLiveWindowsJobPolicy(t *testing.T, job *windowsJob) {
	t.Helper()
	err := job.withHandle(func(handle windows.Handle) error {
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		if err := windows.QueryInformationJobObject(
			handle,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&limits)),
			uint32(unsafe.Sizeof(limits)),
			nil,
		); err != nil {
			return err
		}
		if limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
			return fmt.Errorf("Job Object lacks KILL_ON_JOB_CLOSE: flags %#x", limits.BasicLimitInformation.LimitFlags)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("live Job Object policy: %v", err)
	}
}

func duplicateWindowsJobHandle(t *testing.T, job *windowsJob) windows.Handle {
	t.Helper()
	var duplicate windows.Handle
	currentProcess := windows.CurrentProcess()
	err := job.withHandle(func(source windows.Handle) error {
		return windows.DuplicateHandle(
			currentProcess,
			source,
			currentProcess,
			&duplicate,
			0,
			false,
			windows.DUPLICATE_SAME_ACCESS,
		)
	})
	if err != nil {
		t.Fatalf("duplicate Job Object handle: %v", err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(duplicate) })
	return duplicate
}

func queryWindowsJobActiveProcesses(handle windows.Handle) (uint32, error) {
	var accounting windowsJobBasicAccountingInformation
	err := windows.QueryInformationJobObject(
		handle,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&accounting)),
		uint32(unsafe.Sizeof(accounting)),
		nil,
	)
	return accounting.ActiveProcesses, err
}

func replaceWindowsTestEnv(base []string, replacements map[string]string) []string {
	result := make([]string, 0, len(base)+len(replacements))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := replacements[key]; !replace {
			result = append(result, entry)
		}
	}
	for key, value := range replacements {
		result = append(result, key+"="+value)
	}
	return result
}
