//go:build !windows

package claude

import (
	osexec "os/exec"
	"syscall"
	"time"
)

// setProcessGroupAttr puts the child process in its own process group so
// signals sent to the group don't leak back into the parent.
func setProcessGroupAttr(attr *syscall.SysProcAttr) {
	attr.Setpgid = true
}

// killProcessGroup signals SIGTERM to the entire process group of cmd,
// escalating to SIGKILL after a bounded grace period. Best-effort: missing
// process / already-exited cases are ignored.
func killProcessGroup(cmd *osexec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		_ = cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

func forceKillProcessGroup(cmd *osexec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
