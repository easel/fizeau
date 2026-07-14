//go:build linux || darwin

package processlifecycle

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	creackpty "github.com/creack/pty"
)

// PTY owns the terminal master and the same supervised lifecycle boundary used
// by Batch. Harness and terminal packages may perform I/O through this handle,
// but only processlifecycle starts, stops, and reaps the wrapped process tree.
type PTY struct {
	master *os.File
	batch  *Batch

	closeOnce sync.Once
	closeErr  error
}

// StartPTY allocates a terminal and starts target behind the shared durable
// launch gate. The trusted supervisor remains outside the target session; the
// gated child becomes the target session and process-group leader and acquires
// the terminal as its controlling TTY before untrusted code is exec'd.
func StartPTY(ctx context.Context, target *exec.Cmd, size PTYSize, opts BatchOptions) (*PTY, error) {
	if size.Rows == 0 || size.Cols == 0 {
		return nil, errors.New("process lifecycle PTY size must be non-zero")
	}
	master, slave, err := creackpty.Open()
	if err != nil {
		return nil, err
	}
	if err := creackpty.Setsize(master, &creackpty.Winsize{Rows: size.Rows, Cols: size.Cols}); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return nil, err
	}
	target.Stdin = slave
	target.Stdout = slave
	target.Stderr = slave
	batch, err := startUnixTarget(ctx, target, opts, true)
	_ = slave.Close()
	if err != nil {
		_ = master.Close()
		return nil, err
	}
	return &PTY{master: master, batch: batch}, nil
}

// StartPTYCommand prepares a target command inside the lifecycle boundary and
// then delegates to StartPTY. A nil env inherits the owner environment; a
// non-nil env is the exact child environment.
func StartPTYCommand(ctx context.Context, command string, args []string, workdir string, env []string, size PTYSize, opts BatchOptions) (*PTY, error) {
	resolved, err := exec.LookPath(command)
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- command and args are the explicit internal PTY launch plan;
	// execution remains gated and contained by StartPTY.
	target := exec.Command(resolved, args...)
	target.Dir = workdir
	if env != nil {
		target.Env = append([]string(nil), env...)
	}
	return StartPTY(ctx, target, size, opts)
}

func (p *PTY) Read(dst []byte) (int, error)  { return p.master.Read(dst) }
func (p *PTY) Write(src []byte) (int, error) { return p.master.Write(src) }

// Resize updates the kernel terminal size without changing process ownership.
func (p *PTY) Resize(size PTYSize) error {
	if size.Rows == 0 || size.Cols == 0 {
		return errors.New("process lifecycle PTY size must be non-zero")
	}
	return creackpty.Setsize(p.master, &creackpty.Winsize{Rows: size.Rows, Cols: size.Cols})
}

// PID returns the direct target child captured before the launch gate opened.
func (p *PTY) PID() int {
	if p == nil || p.batch == nil {
		return -1
	}
	return p.batch.Record().DirectChildIdentity.PID
}

// Record returns the durable process ownership fact for this PTY boundary.
func (p *PTY) Record() Record {
	if p == nil || p.batch == nil {
		return Record{}
	}
	return p.batch.Record()
}

// Close requests bounded process-tree cleanup before closing the terminal
// descriptor. Closing the PTY can never mark the session closed while leaving
// the lifecycle boundary running.
func (p *PTY) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		var stopErr, closeErr error
		if p.batch != nil {
			stopErr = p.batch.Stop()
		}
		if p.master != nil {
			closeErr = p.master.Close()
		}
		p.closeErr = errors.Join(stopErr, closeErr)
	})
	return p.closeErr
}

// Kill uses the lifecycle supervisor's staged group cleanup. The name exists
// for the PTY session interface; it never falls back to direct-child Kill.
func (p *PTY) Kill() error { return p.Close() }

// Wait observes target completion and supervisor cleanup, then closes the PTY
// master so readers see EOF. It returns the supervisor/target exit error.
func (p *PTY) Wait() error {
	if p == nil || p.batch == nil {
		return nil
	}
	waitErr := p.batch.Wait()
	return errors.Join(waitErr, p.Close())
}
