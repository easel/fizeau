//go:build darwin

package processlifecycle

import (
	"os"
	"syscall"
)

func supervisorParentDeathSignal() os.Signal { return syscall.SIGUSR1 }

// Darwin has no prctl-style parent-death facility. The already-active control
// pipe is the primary and strongest liveness mechanism on this platform.
func enableSupervisorParentDeath(int) error { return nil }

// Darwin has no child-subreaper analogue. The supervisor still reaps its
// direct child and proves the dedicated process group empty after signalling
// all members; orphaned grandchildren are reparented by the kernel.
func enableSupervisorSubreaper() error { return nil }

func lifecycleChildSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
