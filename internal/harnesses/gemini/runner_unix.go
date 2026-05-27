//go:build !windows

package gemini

import (
	osexec "os/exec"
	"syscall"
	"time"
)

func setProcessGroupAttr(attr *syscall.SysProcAttr) {
	attr.Setpgid = true
}

func killProcessGroup(cmd *osexec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
