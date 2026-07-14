//go:build windows

package processlifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsJobAPI interface {
	CreateJobObject(*windows.SecurityAttributes, *uint16) (windows.Handle, error)
	SetHandleInformation(windows.Handle, uint32, uint32) error
	SetExtendedLimitInformation(windows.Handle, *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error
	CloseHandle(windows.Handle) error
}

type nativeWindowsJobAPI struct{}

func (nativeWindowsJobAPI) CreateJobObject(attributes *windows.SecurityAttributes, name *uint16) (windows.Handle, error) {
	return windows.CreateJobObject(attributes, name)
}

func (nativeWindowsJobAPI) SetHandleInformation(handle windows.Handle, mask, flags uint32) error {
	return windows.SetHandleInformation(handle, mask, flags)
}

func (nativeWindowsJobAPI) SetExtendedLimitInformation(handle windows.Handle, limits *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
	_, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(limits)),
		uint32(unsafe.Sizeof(*limits)),
	)
	return err
}

func (nativeWindowsJobAPI) CloseHandle(handle windows.Handle) error {
	return windows.CloseHandle(handle)
}

type windowsJob struct {
	mu     sync.Mutex
	handle windows.Handle
}

func createWindowsJob(api windowsJobAPI) (*windowsJob, error) {
	attributes := windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 0,
	}
	handle, err := api.CreateJobObject(&attributes, nil)
	if err != nil {
		return nil, fmt.Errorf("create Windows Job Object: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = api.CloseHandle(handle)
		}
	}()
	if err := api.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, fmt.Errorf("make Windows Job Object non-inheritable: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if err := api.SetExtendedLimitInformation(handle, &limits); err != nil {
		return nil, fmt.Errorf("configure Windows Job Object kill-on-close: %w", err)
	}
	closeOnError = false
	return &windowsJob{handle: handle}, nil
}

func (j *windowsJob) withHandle(fn func(windows.Handle) error) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 || j.handle == windows.InvalidHandle {
		return fs.ErrClosed
	}
	return fn(j.handle)
}

func (j *windowsJob) assign(process windows.Handle) error {
	return j.withHandle(func(job windows.Handle) error {
		return windows.AssignProcessToJobObject(job, process)
	})
}

func (j *windowsJob) terminate() error {
	return j.withHandle(func(job windows.Handle) error {
		return windows.TerminateJobObject(job, 1)
	})
}

func (j *windowsJob) activeProcesses() (uint32, error) {
	var accounting windowsJobBasicAccountingInformation
	err := j.withHandle(func(job windows.Handle) error {
		return windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)),
			uint32(unsafe.Sizeof(accounting)),
			nil,
		)
	})
	return accounting.ActiveProcesses, err
}

func (j *windowsJob) close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 || j.handle == windows.InvalidHandle {
		return nil
	}
	handle := j.handle
	j.handle = windows.InvalidHandle
	return windows.CloseHandle(handle)
}

// windowsJobBasicAccountingInformation matches
// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION. x/sys/windows exposes the query but
// not this fixed-layout structure.
type windowsJobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// Batch owns one suspended-then-contained Windows process tree.
type Batch struct {
	cmd            *exec.Cmd
	lease          *Lease
	backend        *windowsBackend
	request        context.Context
	cleanupTimeout time.Duration

	processDone    chan struct{}
	processWaitErr error
	commandDone    chan struct{}
	commandWaitErr error
	commandWait    sync.Once
	stopOnce       sync.Once
	stopErr        error
}

// StartBatch creates the command suspended, assigns it to a dedicated
// kill-on-close Job Object, durably records the boundary, and only then resumes
// the sole primary thread. exec.Cmd still owns command-line construction,
// environment, working directory, and arbitrary stdin/stdout/stderr plumbing.
func StartBatch(ctx context.Context, target *exec.Cmd, opts BatchOptions) (*Batch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if target == nil || target.Path == "" || len(target.Args) == 0 {
		return nil, fmt.Errorf("%w: a prepared target command is required", ErrInvalidRecord)
	}
	if opts.Harness == "" {
		return nil, fmt.Errorf("%w: harness is required", ErrInvalidRecord)
	}
	if opts.CleanupTimeout <= 0 {
		opts.CleanupTimeout = defaultBatchCleanupTimeout
	}
	if opts.OperationID == "" {
		var err error
		opts.OperationID, err = newRecordID()
		if err != nil {
			return nil, err
		}
	}
	if opts.Registry == nil {
		registryDir, err := batchRegistryDir(opts.SessionLogDir)
		if err != nil {
			return nil, err
		}
		opts.Registry = NewFileRegistry(registryDir)
	}

	job, err := createWindowsJob(nativeWindowsJobAPI{})
	if err != nil {
		return nil, err
	}
	sys := &syscall.SysProcAttr{}
	if target.SysProcAttr != nil {
		copy := *target.SysProcAttr
		sys = &copy
	}
	if sys.CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB != 0 {
		_ = job.close()
		return nil, fmt.Errorf("%w: Windows job breakaway is forbidden", ErrPlatformUnsupported)
	}
	sys.CreationFlags |= windows.CREATE_SUSPENDED
	target.SysProcAttr = sys
	// The lifecycle watcher, not exec.CommandContext's hidden direct-child
	// kill path, owns cancellation of the whole Job Object.
	target.Cancel = nil
	target.WaitDelay = 0
	if err := target.Start(); err != nil {
		_ = job.close()
		return nil, fmt.Errorf("start suspended Windows harness: %w", err)
	}

	thread, err := openOnlySuspendedThread(uint32(target.Process.Pid))
	if err != nil {
		return nil, abortUnassignedWindowsStart(ctx, target, job, 0, opts.CleanupTimeout, fmt.Errorf("open suspended primary thread: %w", err))
	}
	ownerIdentity, err := currentWindowsProcessIdentity()
	if err != nil {
		return nil, abortUnassignedWindowsStart(ctx, target, job, thread, opts.CleanupTimeout, fmt.Errorf("capture lifecycle owner identity: %w", err))
	}
	var childIdentity ProcessIdentity
	var identityErr error
	err = target.Process.WithHandle(func(handle uintptr) {
		childIdentity, identityErr = windowsProcessIdentity(target.Process.Pid, windows.Handle(handle))
	})
	err = errors.Join(err, identityErr)
	if err != nil {
		return nil, abortUnassignedWindowsStart(ctx, target, job, thread, opts.CleanupTimeout, fmt.Errorf("capture suspended child identity: %w", err))
	}

	backend := &windowsBackend{job: job, ownerIdentity: ownerIdentity, childIdentity: childIdentity}
	prepared := &windowsPreparedBoundary{
		cmd: target, job: job, thread: thread, cleanupTimeout: opts.CleanupTimeout,
		descriptor: BoundaryDescriptor{
			OwnerIdentity: ownerIdentity,
			// Windows has no trusted shim process: the owner identity fills the
			// required supervisor slot while the Job Object owns the child tree.
			SupervisorIdentity:      ownerIdentity,
			DirectChildIdentity:     childIdentity,
			BoundaryProcessIdentity: childIdentity,
			BoundaryID:              windowsJobIdentity(childIdentity),
			BoundaryType:            BoundaryTypeWindowsJob,
		},
	}
	lease, err := acquireAssignedBoundary(
		ctx,
		Options{OperationID: opts.OperationID, Harness: opts.Harness},
		opts.Registry,
		backend,
		prepared,
	)
	if err != nil {
		return nil, err
	}

	batch := &Batch{
		cmd: target, lease: lease, backend: backend, request: ctx,
		cleanupTimeout: opts.CleanupTimeout,
		processDone:    make(chan struct{}),
		commandDone:    make(chan struct{}),
	}
	go batch.waitForProcessExit()
	go func() {
		select {
		case <-ctx.Done():
			_ = batch.Stop()
		case <-batch.processDone:
			_ = batch.Stop()
		}
	}()
	return batch, nil
}

func (b *Batch) Record() Record { return b.lease.Record() }

func (b *Batch) Stop() error {
	if b == nil {
		return nil
	}
	b.stopOnce.Do(func() { b.stopErr = b.stop() })
	return b.stopErr
}

func (b *Batch) Wait() error {
	if b == nil || b.cmd == nil {
		return nil
	}
	select {
	case <-b.processDone:
	case <-b.request.Done():
	}
	stopErr := b.Stop()
	var processWaitErr error
	select {
	case <-b.processDone:
		processWaitErr = b.processWaitErr
	default:
	}
	var commandWaitErr error
	select {
	case <-b.commandDone:
		commandWaitErr = b.commandWaitErr
	default:
	}
	return errors.Join(commandWaitErr, processWaitErr, b.request.Err(), stopErr)
}

func (b *Batch) waitForProcessExit() {
	var waitErr error
	handleErr := b.cmd.Process.WithHandle(func(handle uintptr) {
		event, err := windows.WaitForSingleObject(windows.Handle(handle), windows.INFINITE)
		if err != nil {
			waitErr = err
			return
		}
		if event != windows.WAIT_OBJECT_0 {
			waitErr = fmt.Errorf("wait for Windows harness returned event %#x", event)
		}
	})
	b.processWaitErr = errors.Join(handleErr, waitErr)
	close(b.processDone)
}

func (b *Batch) startCommandWait() {
	b.commandWait.Do(func() {
		go func() {
			b.commandWaitErr = b.cmd.Wait()
			close(b.commandDone)
		}()
	})
}

func (b *Batch) stop() error {
	cleanupCtx, cancel := CleanupContext(b.request, b.cleanupTimeout)
	defer cancel()
	defer b.backend.job.close()
	beginErr := b.lease.BeginCleanup(cleanupCtx)
	terminateErr := b.backend.job.terminate()
	observation := waitForWindowsJobEmpty(cleanupCtx, b.backend, b.lease.Record())

	select {
	case <-b.processDone:
	case <-cleanupCtx.Done():
	}
	b.startCommandWait()
	var commandWaitDeadlineErr error
	select {
	case <-b.commandDone:
	case <-cleanupCtx.Done():
		commandWaitDeadlineErr = cleanupCtx.Err()
	}
	result, completeErr := b.lease.CompleteCleanup(cleanupCtx)
	if !result.BoundaryEmpty && observation.Detail != "" {
		completeErr = errors.Join(completeErr, errors.New(observation.Detail))
	}
	return errors.Join(beginErr, terminateErr, commandWaitDeadlineErr, completeErr)
}

type windowsPreparedBoundary struct {
	cmd            *exec.Cmd
	job            *windowsJob
	thread         windows.Handle
	cleanupTimeout time.Duration
	descriptor     BoundaryDescriptor
	assigned       bool
	resumed        bool
}

func (p *windowsPreparedBoundary) Descriptor() BoundaryDescriptor { return p.descriptor }

func (p *windowsPreparedBoundary) Assign(context.Context) error {
	var assignErr error
	handleErr := p.cmd.Process.WithHandle(func(handle uintptr) {
		assignErr = p.job.assign(windows.Handle(handle))
		if assignErr == nil {
			p.assigned = true
		}
	})
	return errors.Join(handleErr, assignErr)
}

func (p *windowsPreparedBoundary) Release(context.Context) error {
	if p.thread == 0 || p.thread == windows.InvalidHandle {
		return fs.ErrClosed
	}
	previous, err := windows.ResumeThread(p.thread)
	closeErr := windows.CloseHandle(p.thread)
	p.thread = windows.InvalidHandle
	if err != nil {
		return errors.Join(err, closeErr)
	}
	if previous != 1 {
		return errors.Join(fmt.Errorf("primary thread suspend count was %d, want 1", previous), closeErr)
	}
	p.resumed = true
	return closeErr
}

func (p *windowsPreparedBoundary) Abort(ctx context.Context) (AbortResult, error) {
	if p.thread != 0 && p.thread != windows.InvalidHandle {
		_ = windows.CloseHandle(p.thread)
		p.thread = windows.InvalidHandle
	}
	if p.assigned {
		_ = p.job.terminate()
	} else if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	timeout := p.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultBatchCleanupTimeout
	}
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	select {
	case waitErr := <-done:
		if p.assigned {
			observation := waitForWindowsJobEmpty(abortCtx, &windowsBackend{job: p.job}, p.descriptorRecord())
			closeErr := p.job.close()
			if observation.Status != BoundaryEmpty {
				return AbortResult{Status: AbortIndeterminate, Detail: observation.Detail}, errors.Join(waitErr, closeErr, ErrAbortIndeterminate)
			}
			return AbortResult{Status: AbortEmpty}, closeErr
		}
		return AbortResult{Status: AbortEmpty}, p.job.close()
	case <-abortCtx.Done():
		_ = p.job.close()
		return AbortResult{Status: AbortIndeterminate, Detail: "suspended child reap deadline expired"}, abortCtx.Err()
	}
}

func (p *windowsPreparedBoundary) descriptorRecord() Record {
	return Record{BoundaryIdentity: p.descriptor.BoundaryID}
}

type windowsBackend struct {
	job           *windowsJob
	ownerIdentity ProcessIdentity
	childIdentity ProcessIdentity
}

func (b *windowsBackend) ObserveBoundary(ctx context.Context, record Record) (BoundaryObservation, error) {
	if err := ctx.Err(); err != nil {
		return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: err.Error()}, err
	}
	active, err := b.job.activeProcesses()
	if err != nil {
		return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: err.Error()}, err
	}
	if active == 0 {
		return BoundaryObservation{Status: BoundaryEmpty, BoundaryIdentity: record.BoundaryIdentity}, nil
	}
	return BoundaryObservation{
		Status:                  BoundaryMatching,
		BoundaryIdentity:        record.BoundaryIdentity,
		SupervisorIdentity:      record.SupervisorIdentity,
		DirectChildIdentity:     record.DirectChildIdentity,
		BoundaryProcessIdentity: record.BoundaryProcessIdentity,
		OwnerStatus:             OwnerMatching,
		OwnerIdentity:           record.OwnerIdentity,
		Detail:                  strconv.FormatUint(uint64(active), 10) + " active processes remain in Windows Job Object",
	}, nil
}

func waitForWindowsJobEmpty(ctx context.Context, backend *windowsBackend, record Record) BoundaryObservation {
	for {
		observation, err := backend.ObserveBoundary(ctx, record)
		if err != nil || observation.Status == BoundaryEmpty {
			return observation
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: ctx.Err().Error()}
		case <-timer.C:
		}
	}
}

func openOnlySuspendedThread(pid uint32) (windows.Handle, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	err = windows.Thread32First(snapshot, &entry)
	var threadID uint32
	count := 0
	for err == nil {
		if entry.OwnerProcessID == pid {
			count++
			threadID = entry.ThreadID
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Thread32Next(snapshot, &entry)
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return 0, err
	}
	if count != 1 {
		return 0, fmt.Errorf("suspended process %d has %d threads, want exactly one", pid, count)
	}
	return windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
}

func abortUnassignedWindowsStart(ctx context.Context, cmd *exec.Cmd, job *windowsJob, thread windows.Handle, timeout time.Duration, cause error) error {
	prepared := &windowsPreparedBoundary{cmd: cmd, job: job, thread: thread, cleanupTimeout: timeout}
	result, abortErr := prepared.Abort(context.WithoutCancel(ctx))
	return errors.Join(cause, abortErr, abortStatusError(result))
}

func windowsJobIdentity(child ProcessIdentity) string {
	return "windows-job:" + strconv.Itoa(child.PID) + ":" + child.BirthToken
}
