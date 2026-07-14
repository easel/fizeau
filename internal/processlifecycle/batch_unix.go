//go:build linux || darwin

package processlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	lifecycleRoleEnv         = "FIZEAU_PROCESS_LIFECYCLE_ROLE"
	lifecycleRoleSupervisor  = "unix-batch-supervisor-v1"
	lifecycleRoleChild       = "unix-batch-child-v1"
	lifecycleFDBaseEnv       = "FIZEAU_PROCESS_LIFECYCLE_FD_BASE"
	lifecycleExtraCountEnv   = "FIZEAU_PROCESS_LIFECYCLE_EXTRA_COUNT"
	lifecycleExpectedPPIDEnv = "FIZEAU_PROCESS_LIFECYCLE_EXPECTED_PPID"
)

type batchTargetConfig struct {
	Path           string        `json:"path"`
	Args           []string      `json:"args"`
	Env            []string      `json:"env"`
	Dir            string        `json:"dir,omitempty"`
	PTY            bool          `json:"pty,omitempty"`
	GracePeriod    time.Duration `json:"grace_period"`
	CleanupTimeout time.Duration `json:"cleanup_timeout"`
}

type supervisorStartReport struct {
	PID   int    `json:"pid,omitempty"`
	PGID  int    `json:"pgid,omitempty"`
	Error string `json:"error,omitempty"`
}

// Batch owns one supervised Unix batch process and its durable containment
// lease. The wrapped command is started and waited only by this handle.
type Batch struct {
	cmd            *exec.Cmd
	lease          *Lease
	backend        *unixBackend
	control        *os.File
	request        context.Context
	cleanupTimeout time.Duration
	gracePeriod    time.Duration

	stopOnce sync.Once
	stopErr  error
	waitDone chan struct{}
	waitErr  error
}

// StartBatch starts a trusted supervisor and a gated direct-child shim. The
// shim is the target process-group leader but cannot exec untrusted harness
// code until Acquire has durably persisted owner, supervisor, direct-child,
// and boundary-anchor birth identities.
func StartBatch(ctx context.Context, target *exec.Cmd, opts BatchOptions) (*Batch, error) {
	return startUnixTarget(ctx, target, opts, false)
}

// startUnixTarget starts either a pipe-backed batch target or a PTY-backed
// target. PTY allocation remains in this package so neither harness adapters
// nor internal/pty/session can bypass the shared containment boundary.
func startUnixTarget(ctx context.Context, target *exec.Cmd, opts BatchOptions, terminal bool) (*Batch, error) {
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
	if opts.GracePeriod <= 0 {
		opts.GracePeriod = defaultBatchGracePeriod
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

	config := batchTargetConfig{
		Path:           target.Path,
		Args:           append([]string(nil), target.Args...),
		Env:            append([]string(nil), target.Environ()...),
		Dir:            target.Dir,
		PTY:            terminal,
		GracePeriod:    opts.GracePeriod,
		CleanupTimeout: opts.CleanupTimeout,
	}
	originalExtraFiles := append([]*os.File(nil), target.ExtraFiles...)

	configR, configW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create supervisor config pipe: %w", err)
	}
	gateR, gateW, err := os.Pipe()
	if err != nil {
		closeFiles(configR, configW)
		return nil, fmt.Errorf("create launch gate: %w", err)
	}
	controlR, controlW, err := os.Pipe()
	if err != nil {
		closeFiles(configR, configW, gateR, gateW)
		return nil, fmt.Errorf("create lifecycle control pipe: %w", err)
	}
	reportR, reportW, err := os.Pipe()
	if err != nil {
		closeFiles(configR, configW, gateR, gateW, controlR, controlW)
		return nil, fmt.Errorf("create supervisor report pipe: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		closeFiles(configR, configW, gateR, gateW, controlR, controlW, reportR, reportW)
		return nil, fmt.Errorf("resolve lifecycle supervisor executable: %w", err)
	}
	fdBase := 3 + len(originalExtraFiles)
	target.Path = executable
	target.Args = []string{executable}
	target.Dir = ""
	target.Env = lifecycleEnvironment(os.Environ(), map[string]string{
		lifecycleRoleEnv:         lifecycleRoleSupervisor,
		lifecycleFDBaseEnv:       strconv.Itoa(fdBase),
		lifecycleExtraCountEnv:   strconv.Itoa(len(originalExtraFiles)),
		lifecycleExpectedPPIDEnv: strconv.Itoa(os.Getpid()),
	})
	target.ExtraFiles = append(originalExtraFiles, configR, gateR, controlR, reportW)
	target.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	target.Cancel = nil

	if err := target.Start(); err != nil {
		closeFiles(configR, configW, gateR, gateW, controlR, controlW, reportR, reportW)
		return nil, fmt.Errorf("start lifecycle supervisor: %w", err)
	}
	// Only the supervisor retains controlR. Keeping another copy in the owner
	// would prevent owner death from producing EOF.
	closeFiles(configR, gateR, controlR, reportW)

	if err := json.NewEncoder(configW).Encode(config); err != nil {
		_ = configW.Close()
		closeFiles(gateW, controlW, reportR)
		_ = target.Process.Kill()
		_ = target.Wait()
		return nil, fmt.Errorf("send supervisor target config: %w", err)
	}
	if err := configW.Close(); err != nil {
		closeFiles(gateW, controlW, reportR)
		_ = target.Process.Kill()
		_ = target.Wait()
		return nil, fmt.Errorf("close supervisor target config: %w", err)
	}

	report, err := readSupervisorReport(ctx, reportR)
	_ = reportR.Close()
	if err != nil || report.Error != "" || report.PID <= 0 || report.PGID <= 0 {
		cause := err
		if report.Error != "" {
			cause = errors.Join(cause, errors.New(report.Error))
		}
		prepared := &unixPreparedBoundary{cmd: target, gate: gateW, control: controlW, pgid: report.PGID, grace: opts.GracePeriod, cleanupTimeout: opts.CleanupTimeout}
		return nil, abortStart(ctx, prepared, fmt.Errorf("prepare gated harness child: %w", cause))
	}

	ownerIdentity, err := readUnixProcessIdentity(os.Getpid())
	if err != nil {
		prepared := &unixPreparedBoundary{cmd: target, gate: gateW, control: controlW, pgid: report.PGID, grace: opts.GracePeriod, cleanupTimeout: opts.CleanupTimeout}
		return nil, abortStart(ctx, prepared, fmt.Errorf("capture lifecycle owner identity: %w", err))
	}
	supervisorIdentity, err := readUnixProcessIdentity(target.Process.Pid)
	if err != nil {
		prepared := &unixPreparedBoundary{cmd: target, gate: gateW, control: controlW, pgid: report.PGID, grace: opts.GracePeriod, cleanupTimeout: opts.CleanupTimeout}
		return nil, abortStart(ctx, prepared, fmt.Errorf("capture lifecycle supervisor identity: %w", err))
	}
	directChildIdentity, err := readUnixProcessIdentity(report.PID)
	if err != nil {
		prepared := &unixPreparedBoundary{cmd: target, gate: gateW, control: controlW, pgid: report.PGID, grace: opts.GracePeriod, cleanupTimeout: opts.CleanupTimeout}
		return nil, abortStart(ctx, prepared, fmt.Errorf("capture gated direct-child identity: %w", err))
	}
	if report.PID != report.PGID {
		prepared := &unixPreparedBoundary{cmd: target, gate: gateW, control: controlW, pgid: report.PGID, grace: opts.GracePeriod, cleanupTimeout: opts.CleanupTimeout}
		return nil, abortStart(ctx, prepared, fmt.Errorf("gated direct child pid %d does not lead reported process group %d", report.PID, report.PGID))
	}

	backend := &unixBackend{}
	prepared := &unixPreparedBoundary{
		cmd: target, gate: gateW, control: controlW, pgid: report.PGID, grace: opts.GracePeriod, cleanupTimeout: opts.CleanupTimeout,
		descriptor: BoundaryDescriptor{
			OwnerIdentity:           ownerIdentity,
			SupervisorIdentity:      supervisorIdentity,
			DirectChildIdentity:     directChildIdentity,
			BoundaryProcessIdentity: directChildIdentity,
			BoundaryID:              unixProcessGroupIdentity(report.PGID),
			BoundaryType:            BoundaryTypeUnixProcessGroup,
		},
	}
	lease, err := Acquire(ctx, Options{OperationID: opts.OperationID, Harness: opts.Harness}, opts.Registry, backend, prepared)
	if err != nil {
		return nil, err
	}
	batch := &Batch{
		cmd: target, lease: lease, backend: backend, control: controlW, request: ctx,
		cleanupTimeout: opts.CleanupTimeout, gracePeriod: opts.GracePeriod, waitDone: make(chan struct{}),
	}
	go func() {
		batch.waitErr = target.Wait()
		close(batch.waitDone)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = batch.Stop()
		case <-batch.waitDone:
			_ = batch.Stop()
		}
	}()
	return batch, nil
}

// Record returns the identities captured while the gated direct child was
// alive. Callers never derive the process group from an exited leader.
func (b *Batch) Record() Record { return b.lease.Record() }

// Stop enters the one detached cleanup path. It is safe to call from request
// cancellation, timeout handling, and Wait.
func (b *Batch) Stop() error {
	if b == nil {
		return nil
	}
	b.stopOnce.Do(func() { b.stopErr = b.stop() })
	return b.stopErr
}

// Wait reaps the trusted supervisor, then confirms the saved target boundary
// empty through the same cleanup path used by cancellation and timeout.
func (b *Batch) Wait() error {
	if b == nil || b.cmd == nil {
		return nil
	}
	select {
	case <-b.waitDone:
		return errors.Join(b.waitErr, b.request.Err(), b.Stop())
	case <-b.request.Done():
		stopErr := b.Stop()
		select {
		case <-b.waitDone:
			return errors.Join(b.waitErr, b.request.Err(), stopErr)
		default:
			return errors.Join(b.request.Err(), stopErr)
		}
	}
}

func (b *Batch) stop() error {
	cleanupCtx, cancel := CleanupContext(b.request, b.cleanupTimeout)
	defer cancel()
	beginErr := b.lease.BeginCleanup(cleanupCtx)
	if b.control != nil {
		_ = b.control.Close()
		b.control = nil
	}
	var waitErr error
	select {
	case <-b.waitDone:
	case <-cleanupCtx.Done():
		waitErr = cleanupCtx.Err()
	}
	record := b.lease.Record()
	observation := waitForBoundaryEmpty(cleanupCtx, b.backend, record)
	result, completeErr := b.lease.CompleteCleanup(cleanupCtx)
	if !result.BoundaryEmpty && observation.Detail != "" {
		completeErr = errors.Join(completeErr, errors.New(observation.Detail))
	}
	return errors.Join(beginErr, waitErr, completeErr)
}

type unixPreparedBoundary struct {
	cmd            *exec.Cmd
	gate           *os.File
	control        *os.File
	pgid           int
	grace          time.Duration
	cleanupTimeout time.Duration
	descriptor     BoundaryDescriptor
}

func (p *unixPreparedBoundary) Descriptor() BoundaryDescriptor { return p.descriptor }

func (p *unixPreparedBoundary) Release(context.Context) error {
	if p.gate == nil {
		return fs.ErrClosed
	}
	_, err := p.gate.Write([]byte{1})
	closeErr := p.gate.Close()
	p.gate = nil
	return errors.Join(err, closeErr)
}

func (p *unixPreparedBoundary) Abort(ctx context.Context) (AbortResult, error) {
	timeout := p.cleanupTimeout
	if timeout <= 0 {
		timeout = defaultBatchCleanupTimeout
	}
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if p.gate != nil {
		_ = p.gate.Close() // EOF is abort; only a byte releases the child shim.
		p.gate = nil
	}
	if p.control != nil {
		_ = p.control.Close()
		p.control = nil
	}
	if p.pgid > 0 {
		_ = syscall.Kill(-p.pgid, syscall.SIGTERM)
		if p.grace > 0 {
			timer := time.NewTimer(p.grace)
			select {
			case <-abortCtx.Done():
			case <-timer.C:
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-done:
		if p.pgid <= 0 {
			return AbortResult{Status: AbortIndeterminate, Detail: "saved process group is unavailable"}, ErrAbortIndeterminate
		}
		alive, err := unixProcessGroupAlive(p.pgid)
		if err != nil || alive {
			return AbortResult{Status: AbortIndeterminate, Detail: "saved process group was not proven empty"}, errors.Join(ErrAbortIndeterminate, err)
		}
		return AbortResult{Status: AbortEmpty}, nil
	case <-abortCtx.Done():
		_ = p.cmd.Process.Kill()
		<-done
		return AbortResult{Status: AbortIndeterminate, Detail: "abort deadline expired"}, abortCtx.Err()
	}
}

func abortStart(ctx context.Context, prepared *unixPreparedBoundary, cause error) error {
	result, abortErr := prepared.Abort(context.WithoutCancel(ctx))
	return errors.Join(cause, abortErr, abortStatusError(result))
}

type unixBackend struct{}

func (*unixBackend) ObserveBoundary(ctx context.Context, record Record) (BoundaryObservation, error) {
	if err := ctx.Err(); err != nil {
		return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity}, err
	}
	pgid, err := parseUnixProcessGroupIdentity(record.BoundaryIdentity)
	if err != nil || pgid <= 0 {
		return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: "invalid saved process group"}, ErrBoundaryIndeterminate
	}
	supervisor, supervisorErr := readUnixProcessIdentity(record.SupervisorIdentity.PID)
	supervisorMatches := supervisorErr == nil && record.SupervisorIdentity.matches(supervisor)
	alive, err := unixProcessGroupAlive(pgid)
	if err != nil {
		return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: err.Error()}, err
	}
	if !alive {
		if supervisorMatches {
			return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, SupervisorIdentity: supervisor, Detail: "target group is empty but matching supervisor has not exited"}, nil
		}
		if supervisorErr == nil || (supervisorErr != nil && !errors.Is(supervisorErr, fs.ErrNotExist)) {
			return BoundaryObservation{Status: BoundaryMismatch, BoundaryIdentity: record.BoundaryIdentity, SupervisorIdentity: supervisor, Detail: "supervisor identity changed before lifecycle completion"}, nil
		}
		return BoundaryObservation{Status: BoundaryEmpty, BoundaryIdentity: record.BoundaryIdentity}, nil
	}

	observation := BoundaryObservation{Status: BoundaryMatching, BoundaryIdentity: record.BoundaryIdentity}
	observation.SupervisorIdentity = supervisor
	if !supervisorMatches {
		observation.Status = BoundaryMismatch
		observation.Detail = "saved supervisor identity is no longer observable while target group exists"
		return observation, nil
	}
	observation.DirectChildIdentity, err = readUnixProcessIdentity(record.DirectChildIdentity.PID)
	if err != nil {
		observation.Status = BoundaryMismatch
		observation.Detail = "saved direct-child identity is no longer observable while target group exists"
		return observation, nil
	}
	observation.BoundaryProcessIdentity, err = readUnixProcessIdentity(record.BoundaryProcessIdentity.PID)
	if err != nil {
		observation.Status = BoundaryMismatch
		observation.Detail = "saved boundary-anchor identity is no longer observable while target group exists"
		return observation, nil
	}
	owner, ownerErr := readUnixProcessIdentity(record.OwnerIdentity.PID)
	switch {
	case ownerErr == nil && record.OwnerIdentity.matches(owner):
		observation.OwnerStatus = OwnerMatching
		observation.OwnerIdentity = owner
	case errors.Is(ownerErr, fs.ErrNotExist):
		observation.OwnerStatus = OwnerGone
	case ownerErr == nil:
		observation.OwnerStatus = OwnerMismatch
		observation.OwnerIdentity = owner
	default:
		observation.OwnerStatus = OwnerIndeterminate
		observation.Detail = ownerErr.Error()
	}
	return observation, nil
}

func waitForBoundaryEmpty(ctx context.Context, backend *unixBackend, record Record) BoundaryObservation {
	for {
		observation, err := backend.ObserveBoundary(ctx, record)
		if err != nil || observation.Status == BoundaryEmpty {
			return observation
		}
		select {
		case <-ctx.Done():
			return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: ctx.Err().Error()}
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func unixProcessGroupIdentity(pgid int) string { return "unix-pgid:" + strconv.Itoa(pgid) }

func parseUnixProcessGroupIdentity(value string) (int, error) {
	const prefix = "unix-pgid:"
	if !strings.HasPrefix(value, prefix) {
		return 0, fmt.Errorf("invalid Unix process-group identity %q", value)
	}
	return strconv.Atoi(strings.TrimPrefix(value, prefix))
}

func readSupervisorReport(ctx context.Context, report *os.File) (supervisorStartReport, error) {
	type result struct {
		report supervisorStartReport
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var value supervisorStartReport
		err := json.NewDecoder(report).Decode(&value)
		done <- result{report: value, err: err}
	}()
	select {
	case result := <-done:
		return result.report, result.err
	case <-ctx.Done():
		_ = report.Close()
		return supervisorStartReport{}, ctx.Err()
	}
}

func batchRegistryDir(sessionLogDir string) (string, error) {
	if sessionLogDir != "" {
		return filepath.Join(filepath.Dir(sessionLogDir), "harness-sessions"), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve lifecycle state directory: %w", err)
	}
	return filepath.Join(cacheDir, "fizeau", "harness-sessions"), nil
}

func lifecycleEnvironment(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[key]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func unixProcessGroupAlive(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func closeFiles(files ...io.Closer) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
