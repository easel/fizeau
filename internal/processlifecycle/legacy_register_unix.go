//go:build !windows

package processlifecycle

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// RegisterStartedProcess preserves the old post-start registry call while
// runners migrate to Acquire. New launchers must use Acquire because this
// compatibility path cannot provide a pre-execution gate.
func RegisterStartedProcess(sessionLogDir, sessionID, harnessName string, cmd *exec.Cmd) error {
	if sessionLogDir == "" || cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	record := LegacyHarnessSessionRecord{
		SchemaID:  LegacyRecordSchemaID,
		SessionID: sessionID,
		Harness:   harnessName,
		Command:   "subprocess",
		PID:       cmd.Process.Pid,
		PGID:      pgid,
		StartedAt: now,
	}
	registryDir := filepath.Join(filepath.Dir(sessionLogDir), "harness-sessions")
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	recordDigest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", sessionID, cmd.Process.Pid)))
	recordPath := filepath.Join(registryDir, fmt.Sprintf("legacy-%x.json", recordDigest[:12]))
	return writeAtomic(recordPath, data)
}
