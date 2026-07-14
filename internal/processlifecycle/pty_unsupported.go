//go:build !linux && !darwin

package processlifecycle

import (
	"context"
	"fmt"
	"os/exec"
)

// PTY is unavailable until the platform has a strong containment adapter.
type PTY struct{}

func StartPTY(context.Context, *exec.Cmd, PTYSize, BatchOptions) (*PTY, error) {
	return nil, fmt.Errorf("%w: no strong PTY containment adapter", ErrPlatformUnsupported)
}

func StartPTYCommand(context.Context, string, []string, string, []string, PTYSize, BatchOptions) (*PTY, error) {
	return nil, fmt.Errorf("%w: no strong PTY containment adapter", ErrPlatformUnsupported)
}

func (*PTY) Read([]byte) (int, error)  { return 0, ErrPlatformUnsupported }
func (*PTY) Write([]byte) (int, error) { return 0, ErrPlatformUnsupported }
func (*PTY) Resize(PTYSize) error      { return ErrPlatformUnsupported }
func (*PTY) PID() int                  { return -1 }
func (*PTY) Record() Record            { return Record{} }
func (*PTY) Close() error              { return nil }
func (*PTY) Kill() error               { return nil }
func (*PTY) Wait() error               { return ErrPlatformUnsupported }
