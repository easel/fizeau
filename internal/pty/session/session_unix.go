//go:build !windows

package session

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"syscall"

	"github.com/easel/fizeau/internal/processlifecycle"
)

// Start launches command under a direct PTY with argv, workdir, env, and size.
// Cancellation is controlled by ctx and WithTimeout.
func Start(ctx context.Context, command string, args []string, workdir string, env []string, size Size, opts ...Option) (*Session, error) {
	if command == "" {
		return nil, errors.New("command is required")
	}
	if err := validateSize(size); err != nil {
		return nil, err
	}
	cfg := applyOptions(opts)
	if ctx == nil {
		ctx = context.Background()
	}
	var timeoutCancel context.CancelFunc
	if cfg.Timeout > 0 {
		ctx, timeoutCancel = context.WithTimeout(ctx, cfg.Timeout)
	}
	runCtx, runCancel := context.WithCancel(ctx)
	cancel := func() {
		runCancel()
		if timeoutCancel != nil {
			timeoutCancel()
		}
	}
	lifecycleOptions := cfg.LifecycleOptions
	if lifecycleOptions.Harness == "" {
		lifecycleOptions.Harness = "pty"
	}
	managedPTY, err := processlifecycle.StartPTYCommand(runCtx, command, args, workdir, env,
		processlifecycle.PTYSize{Rows: size.Rows, Cols: size.Cols}, lifecycleOptions)
	if err != nil {
		cancel()
		return nil, err
	}

	s := &Session{
		start:    cfg.Clock.Now(),
		clock:    cfg.Clock,
		size:     size,
		cancel:   cancel,
		output:   make(chan OutputChunk, 128),
		events:   make(chan Event, 256),
		waitDone: make(chan struct{}),
		readDone: make(chan struct{}),
	}
	s.impl = &unixImpl{pty: managedPTY}

	go s.readLoop(managedPTY, cfg.BufferSize)
	go func() {
		select {
		case <-runCtx.Done():
			select {
			case <-s.waitDone:
				return
			default:
			}
			_ = s.Kill()
		case <-s.waitDone:
			return
		}
	}()
	go func() {
		<-s.waitDone
		<-s.readDone
		s.closeEvents()
	}()
	return s, nil
}

func (s *Session) readLoop(r io.Reader, bufferSize int) {
	defer close(s.readDone)
	defer close(s.output)
	buf := make([]byte, bufferSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.emitOutput(buf[:n], nil, false)
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
				s.emitOutput(nil, nil, true)
			} else {
				s.emitOutput(nil, err, true)
			}
			return
		}
	}
}

type unixImpl struct {
	pty *processlifecycle.PTY
}

func (u *unixImpl) write(b []byte) (int, error) { return u.pty.Write(b) }

func (u *unixImpl) resize(size Size) error {
	return u.pty.Resize(processlifecycle.PTYSize{Rows: size.Rows, Cols: size.Cols})
}

func (u *unixImpl) close() error { return u.pty.Close() }

func (u *unixImpl) kill() error { return u.pty.Kill() }

func (u *unixImpl) wait() ExitStatus {
	err := u.pty.Wait()
	if err == nil {
		return ExitStatus{Code: 0, Exited: true}
	}
	status := ExitStatus{Code: -1, Err: err}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			status.Code = ws.ExitStatus()
			status.Exited = ws.Exited()
			status.Signaled = ws.Signaled()
			if status.Signaled {
				status.Signal = ws.Signal().String()
			}
		} else {
			status.Code = exitErr.ExitCode()
		}
		return status
	}
	return status
}

func (u *unixImpl) pid() int {
	return u.pty.PID()
}
