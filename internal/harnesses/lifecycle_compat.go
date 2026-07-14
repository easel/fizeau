package harnesses

import (
	"os/exec"

	"github.com/easel/fizeau/internal/processlifecycle"
)

// HarnessSessionRecord is retained as a source-compatible alias while callers
// migrate from the explicitly transitional flat reaper schema.
type HarnessSessionRecord = processlifecycle.LegacyHarnessSessionRecord

// RegisterHarnessSession delegates transitional post-start registration to
// the neutral lifecycle owner. New runners use processlifecycle.Acquire.
func RegisterHarnessSession(sessionLogDir, sessionID, harnessName string, cmd *exec.Cmd) error {
	return processlifecycle.RegisterStartedProcess(sessionLogDir, sessionID, harnessName, cmd)
}
