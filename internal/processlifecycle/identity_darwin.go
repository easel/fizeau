//go:build darwin

package processlifecycle

import (
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/sys/unix"
)

func readUnixProcessIdentity(pid int) (ProcessIdentity, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
			return ProcessIdentity{}, fs.ErrNotExist
		}
		return ProcessIdentity{}, fmt.Errorf("read Darwin process identity for pid %d: %w", pid, err)
	}
	if int(process.Proc.P_pid) != pid {
		return ProcessIdentity{}, fs.ErrNotExist
	}
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read Darwin boot identity: %w", err)
	}
	start := process.Proc.P_starttime
	return ProcessIdentity{
		PID:              pid,
		BirthTokenScheme: "darwin-boottime+sysctl-starttime-usec/v1",
		BirthToken:       fmt.Sprintf("%d.%06d:%d.%06d", boot.Sec, boot.Usec, start.Sec, start.Usec),
	}, nil
}
