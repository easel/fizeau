package harnesses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/easel/fizeau/internal/sessionlog"
)

func OpenProgressLog(sessionLogDir, sessionID, prefix string) (*os.File, error) {
	if sessionLogDir == "" {
		return nil, nil
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return sessionlog.OpenAppend(sessionLogDir, sessionID)
}

func MirrorEvents(dst chan<- Event, log io.Writer, ctx context.Context) (chan Event, <-chan struct{}) {
	mid := make(chan Event, cap(dst))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range mid {
			WriteProgressEvent(log, ev)
			select {
			case dst <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return mid, done
}

func WriteProgressEvent(log io.Writer, ev Event) {
	if log == nil {
		return
	}
	if data, err := json.Marshal(ev); err == nil {
		_, _ = log.Write(data)
		_, _ = log.Write([]byte("\n"))
	}
}

// HarnessSessionRecord represents a registered harness process for reaping.
type HarnessSessionRecord struct {
	SessionID string    `json:"session_id"`
	Harness   string    `json:"harness"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"started_at"`
}

// RegisterHarnessSession writes a harness session record to the reaper registry.
// This enables the stale-harness reaper to clean up any orphaned process groups.
func RegisterHarnessSession(sessionLogDir, sessionID, harnessName string, cmd *exec.Cmd) error {
	if sessionLogDir == "" || cmd == nil || cmd.Process == nil {
		return nil
	}

	// Derive the harness-sessions directory from the parent of sessionLogDir.
	registryDir := filepath.Join(filepath.Dir(sessionLogDir), "harness-sessions")

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// If we can't get the PGID, skip registration (best-effort).
		return nil
	}

	record := HarnessSessionRecord{
		SessionID: sessionID,
		Harness:   harnessName,
		Command:   "subprocess",
		PID:       cmd.Process.Pid,
		PGID:      pgid,
		StartedAt: time.Now().UTC(),
	}

	// Write the record as JSON.
	if err := os.MkdirAll(registryDir, 0o750); err != nil {
		return nil // Best-effort
	}

	recordPath := filepath.Join(registryDir, fmt.Sprintf("%s-%d.json", sessionID, cmd.Process.Pid))
	data, _ := json.MarshalIndent(record, "", "  ")
	data = append(data, '\n')
	_ = os.WriteFile(recordPath, data, 0o600) // Best-effort
	return nil
}
