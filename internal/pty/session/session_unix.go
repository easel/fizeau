//go:build !windows

package session

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
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
	// #nosec G204 -- command and args are explicit PTY session API inputs;
	// exec.Command does not invoke a shell.
	cmd := exec.Command(command, args...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	// Do NOT set Setpgid here. creack/pty sets Setsid=true (and Setctty=true),
	// which makes the child a session AND process-group leader. Calling
	// setpgid(2) on a session leader fails with EPERM on Linux, so combining
	// Setpgid with the PTY's Setsid makes pty.StartWithSize return
	// "fork/exec: operation not permitted" and every PTY discovery/quota probe
	// silently yields zero models. The child already has its own process group
	// via Setsid, and kill() reaps it via Getpgid + killProcessGroup, so
	// no explicit Setpgid is needed. (regression from fizeau-8b09722c)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
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
	s.impl = &unixImpl{cmd: cmd, file: ptmx}

	go s.readLoop(ptmx, cfg.BufferSize)
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
	cmd  *exec.Cmd
	file *os.File
}

func (u *unixImpl) write(b []byte) (int, error) { return u.file.Write(b) }

func (u *unixImpl) resize(size Size) error {
	return pty.Setsize(u.file, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
}

func (u *unixImpl) close() error { return u.file.Close() }

func (u *unixImpl) kill() error {
	if u.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(u.cmd.Process.Pid)
	if err == nil {
		if killProcessGroup(pgid, syscall.SIGTERM) {
			time.Sleep(100 * time.Millisecond)
		}
		_ = killProcessGroup(pgid, syscall.SIGKILL)
		return nil
	}
	return u.cmd.Process.Kill()
}

func killProcessGroup(pgid int, sig syscall.Signal) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, sig)
	return err == nil || errors.Is(err, syscall.ESRCH)
}

func (u *unixImpl) wait() ExitStatus {
	err := u.cmd.Wait()
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
	if u.cmd.Process == nil {
		return -1
	}
	return u.cmd.Process.Pid
}
