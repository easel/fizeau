//go:build windows

package processlifecycle

import "os/exec"

// RegisterStartedProcess is a transitional compatibility hook. Windows
// launchers must move directly to Acquire with a Job Object backend.
func RegisterStartedProcess(_ string, _ string, _ string, _ *exec.Cmd) error { return nil }
