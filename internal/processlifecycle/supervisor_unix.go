//go:build linux || darwin

package processlifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const maxLifecycleInheritedFD = 1 << 20

func init() {
	switch os.Getenv(lifecycleRoleEnv) {
	case lifecycleRoleSupervisor:
		os.Exit(runUnixBatchSupervisor())
	case lifecycleRoleChild:
		os.Exit(runUnixBatchChild())
	}
}

func runUnixBatchSupervisor() int {
	fdBase, ok := environmentInt(lifecycleFDBaseEnv)
	if !ok {
		return 126
	}
	extraCount, ok := environmentInt(lifecycleExtraCountEnv)
	if !ok || extraCount < 0 || extraCount > maxLifecycleInheritedFD-7 || fdBase != 3+extraCount {
		return 126
	}
	expectedPPID, ok := environmentInt(lifecycleExpectedPPIDEnv)
	if !ok || expectedPPID <= 0 {
		return 126
	}

	configFile, err := inheritedFile(fdBase, "lifecycle-config")
	if err != nil {
		return 126
	}
	gateFile, err := inheritedFile(fdBase+1, "lifecycle-gate")
	if err != nil {
		return 126
	}
	controlFile, err := inheritedFile(fdBase+2, "lifecycle-control")
	if err != nil {
		return 126
	}
	reportFile, err := inheritedFile(fdBase+3, "lifecycle-report")
	if err != nil {
		return 126
	}
	for fd := fdBase; fd <= fdBase+3; fd++ {
		syscall.CloseOnExec(fd)
	}

	// The supervisor alone owns controlFile. Monitoring and signal handling are
	// active before Linux parent-death signaling is installed, closing the
	// startup race where the owner dies between fork and prctl.
	parentGone := make(chan struct{})
	var parentGoneOnce sync.Once
	markParentGone := func() { parentGoneOnce.Do(func() { close(parentGone) }) }
	go func() {
		_, _ = io.Copy(io.Discard, controlFile)
		_ = controlFile.Close()
		markParentGone()
	}()
	deathSignal := make(chan os.Signal, 1)
	signal.Notify(deathSignal, supervisorParentDeathSignal())
	defer signal.Stop(deathSignal)
	go func() {
		select {
		case <-deathSignal:
			markParentGone()
		case <-parentGone:
		}
	}()
	if err := enableSupervisorParentDeath(expectedPPID); err != nil {
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	if os.Getppid() != expectedPPID {
		markParentGone()
	}
	if err := enableSupervisorSubreaper(); err != nil {
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}

	var config batchTargetConfig
	if err := json.NewDecoder(configFile).Decode(&config); err != nil {
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	if config.GracePeriod <= 0 {
		config.GracePeriod = defaultBatchGracePeriod
	}
	if config.CleanupTimeout <= 0 {
		config.CleanupTimeout = defaultBatchCleanupTimeout
	}
	_ = configFile.Close()
	select {
	case <-parentGone:
		writeSupervisorReport(reportFile, supervisorStartReport{Error: "embedding process exited before gated child creation"})
		return 125
	default:
	}

	childConfigR, childConfigW, err := os.Pipe()
	if err != nil {
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	executable, err := os.Executable()
	if err != nil {
		closeFiles(childConfigR, childConfigW)
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	targetExtras, err := inheritedFiles(3, extraCount, "target-extra")
	if err != nil {
		closeFiles(childConfigR, childConfigW)
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	for fd := 3; fd < 3+extraCount; fd++ {
		syscall.CloseOnExec(fd)
	}
	childFDBase := 3 + extraCount
	// #nosec G204 -- executable is the current trusted Fizeau binary returned by os.Executable; target config remains pipe-only.
	child := exec.Command(executable)
	child.Env = lifecycleEnvironment(os.Environ(), map[string]string{
		lifecycleRoleEnv:       lifecycleRoleChild,
		lifecycleFDBaseEnv:     strconv.Itoa(childFDBase),
		lifecycleExtraCountEnv: strconv.Itoa(extraCount),
	})
	child.ExtraFiles = append(targetExtras, childConfigR, gateFile)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.SysProcAttr = lifecycleChildSysProcAttr(config.PTY)
	if err := child.Start(); err != nil {
		closeFiles(childConfigR, childConfigW)
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	_ = childConfigR.Close()
	if err := json.NewEncoder(childConfigW).Encode(config); err != nil {
		_ = childConfigW.Close()
		_ = child.Process.Kill()
		_ = child.Wait()
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	_ = childConfigW.Close()

	pgid, err := syscall.Getpgid(child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		writeSupervisorReport(reportFile, supervisorStartReport{Error: err.Error()})
		return 126
	}
	writeSupervisorReport(reportFile, supervisorStartReport{PID: child.Process.Pid, PGID: pgid})
	// The child now has the only gate read end. Config/report are closed and
	// control was never inherited, so harness descendants cannot suppress EOF.
	_ = gateFile.Close()
	for _, file := range targetExtras {
		_ = file.Close()
	}
	closeFiles(os.Stdin, os.Stdout, os.Stderr)

	waitDone := make(chan error, 1)
	go func() { waitDone <- child.Wait() }()
	var waitErr error
	callerDied := false
	select {
	case waitErr = <-waitDone:
	case <-parentGone:
		callerDied = true
		cleanupSupervisorGroup(pgid, config.GracePeriod, config.CleanupTimeout)
		select {
		case waitErr = <-waitDone:
		default:
		}
	}

	// Normal target exit can leave grandchildren in the saved group. The
	// supervisor remains outside that group, so it can escalate and reap them.
	if callerDied {
		return 125
	}
	cleanupSupervisorGroup(pgid, config.GracePeriod, config.CleanupTimeout)
	return processExitCode(waitErr)
}

func runUnixBatchChild() int {
	fdBase, ok := environmentInt(lifecycleFDBaseEnv)
	if !ok {
		return 126
	}
	extraCount, ok := environmentInt(lifecycleExtraCountEnv)
	if !ok || extraCount < 0 || extraCount > maxLifecycleInheritedFD-5 || fdBase != 3+extraCount {
		return 126
	}
	configFile, err := inheritedFile(fdBase, "target-config")
	if err != nil {
		return 126
	}
	gateFile, err := inheritedFile(fdBase+1, "target-gate")
	if err != nil {
		return 126
	}
	syscall.CloseOnExec(fdBase)
	syscall.CloseOnExec(fdBase + 1)
	var config batchTargetConfig
	if err := json.NewDecoder(configFile).Decode(&config); err != nil {
		return 126
	}
	_ = configFile.Close()
	var release [1]byte
	if _, err := io.ReadFull(gateFile, release[:]); err != nil || release[0] != 1 {
		_ = gateFile.Close()
		return 125
	}
	_ = gateFile.Close()
	if config.Dir != "" {
		if err := os.Chdir(config.Dir); err != nil {
			return 126
		}
	}
	if len(config.Args) == 0 || config.Path == "" {
		return 126
	}
	if err := syscall.Exec(config.Path, config.Args, config.Env); err != nil { // #nosec G204 -- target is the already-resolved builtin harness command
		return 126
	}
	return 126
}

func cleanupSupervisorGroup(pgid int, grace, cleanupTimeout time.Duration) {
	if pgid <= 0 {
		return
	}
	started := time.Now()
	deadline := started.Add(cleanupTimeout)
	graceDeadline := started.Add(grace)
	if graceDeadline.After(deadline) {
		graceDeadline = deadline
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	for time.Now().Before(graceDeadline) {
		reapAdoptedChildren()
		alive, _ := unixProcessGroupAlive(pgid)
		if !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for time.Now().Before(deadline) {
		reapAdoptedChildren()
		alive, _ := unixProcessGroupAlive(pgid)
		if !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reapAdoptedChildren() {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid <= 0 || (err != nil && !errors.Is(err, syscall.EINTR)) {
			return
		}
	}
}

func inheritedFiles(start, count int, prefix string) ([]*os.File, error) {
	if start < 0 || count < 0 || start > maxLifecycleInheritedFD || count > maxLifecycleInheritedFD-start {
		return nil, fmt.Errorf("unsafe inherited file descriptor range: start=%d count=%d", start, count)
	}
	files := make([]*os.File, 0, count)
	for index := 0; index < count; index++ {
		file, err := inheritedFile(start+index, prefix+"-"+strconv.Itoa(index))
		if err != nil {
			for _, opened := range files {
				_ = opened.Close()
			}
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func inheritedFile(fd int, name string) (*os.File, error) {
	if fd < 0 || fd > maxLifecycleInheritedFD {
		return nil, fmt.Errorf("unsafe inherited file descriptor %d", fd)
	}
	// #nosec G115 -- fd is checked nonnegative and capped well below uintptr limits before conversion.
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		return nil, fmt.Errorf("inherited file descriptor %d is unavailable", fd)
	}
	return file, nil
}

func writeSupervisorReport(file *os.File, report supervisorStartReport) {
	if file == nil {
		return
	}
	_ = json.NewEncoder(file).Encode(report)
	_ = file.Close()
}

func environmentInt(name string) (int, bool) {
	value, err := strconv.Atoi(os.Getenv(name))
	return value, err == nil
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}
