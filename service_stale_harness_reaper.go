package fizeau

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/easel/fizeau/internal/processlifecycle"
)

const defaultStaleHarnessReaperGrace = 5 * time.Minute

type staleHarnessRecord struct {
	SchemaID  string    `json:"schema_id,omitempty"`
	SessionID string    `json:"session_id"`
	Harness   string    `json:"harness"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"started_at"`
	Terminal  bool      `json:"terminal,omitempty"`
	ReapedAt  time.Time `json:"reaped_at,omitempty"`
}

func (s *service) reapStaleHarnessSessions() {
	dir := s.staleHarnessRegistryDir()
	if dir == "" {
		return
	}
	_ = reapStaleHarnessRecords(dir, s.opts.staleHarnessReaperGrace(), time.Now().UTC())
}

func (s *service) staleHarnessRegistryDir() string {
	if s == nil {
		return ""
	}
	logDir := s.serviceSessionLogDir()
	if logDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(logDir), "harness-sessions")
}

func (o ServiceOptions) staleHarnessReaperGrace() time.Duration {
	if o.StaleHarnessReaperGrace > 0 {
		return o.StaleHarnessReaperGrace
	}
	return defaultStaleHarnessReaperGrace
}

func reapStaleHarnessRecords(dir string, grace time.Duration, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		record, ok := readStaleHarnessRecord(path)
		if !ok {
			continue
		}
		if record.Terminal || record.PGID <= 0 || !processGroupAlive(record.PGID) {
			_ = os.Remove(path)
			continue
		}
		if grace > 0 && now.Sub(record.StartedAt) < grace {
			continue
		}
		terminateOwnedProcessGroup(record.PGID)
		record.Terminal = true
		record.ReapedAt = now
		_ = writeStaleHarnessRecord(path, record)
		_ = os.Remove(path)
	}
	return nil
}

func readStaleHarnessRecord(path string) (staleHarnessRecord, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is service-owned reaper state file
	if err != nil {
		return staleHarnessRecord{}, false
	}
	var record staleHarnessRecord
	if err := json.Unmarshal(data, &record); err != nil {
		_ = os.Remove(path)
		return staleHarnessRecord{}, false
	}
	// The flat PID/PGID reaper is deliberately restricted to its legacy
	// schema. V1 and future records carry birth identities and must be retained
	// for the identity-safe lifecycle recovery path.
	if record.SchemaID != "" && record.SchemaID != processlifecycle.LegacyRecordSchemaID {
		return staleHarnessRecord{}, false
	}
	return record, true
}

func writeStaleHarnessRecord(path string, record staleHarnessRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- reaper state dir must be group-readable for ops tooling
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
