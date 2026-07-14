//go:build linux

package processlifecycle

import (
	"fmt"
	"os"
	"syscall"
)

const (
	prSetPdeathsig      = 1
	prSetChildSubreaper = 36
)

func supervisorParentDeathSignal() os.Signal { return syscall.SIGUSR1 }

func enableSupervisorParentDeath(expectedPPID int) error {
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetPdeathsig, uintptr(syscall.SIGUSR1), 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("install supervisor parent-death signal: %w", errno)
	}
	if os.Getppid() != expectedPPID {
		return nil
	}
	return nil
}

func enableSupervisorSubreaper() error {
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("install lifecycle child subreaper: %w", errno)
	}
	return nil
}

func lifecycleChildSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
