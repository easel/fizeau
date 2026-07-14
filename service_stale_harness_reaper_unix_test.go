//go:build linux || darwin

package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/processlifecycle"
)

func TestServiceStartupReapsStaleHarnessSessions(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "sessions")
	registryDir := filepath.Join(dir, "harness-sessions")
	registry := processlifecycle.NewFileRegistry(registryDir)
	record := staleEmptyLifecycleRecord("stale-session", time.Now().Add(-time.Hour).UTC())
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("create lifecycle record: %v", err)
	}

	_, err := New(ServiceOptions{
		ServiceConfig:           &fakeServiceConfig{},
		QuotaRefreshContext:     canceledRefreshContext(),
		SessionLogDir:           logDir,
		StaleHarnessReaperGrace: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := registry.Get(context.Background(), record.RecordID)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("confirmed-empty lifecycle record was not removed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStaleHarnessReaperPreservesMalformedUnknownAndLegacyEvidence(t *testing.T) {
	dir := t.TempDir()
	entries := map[string]any{
		"malformed.json": json.RawMessage(`{"schema_id":`),
		"future.json": map[string]any{
			"schema_id": "fizeau.process-lifecycle/v99", "record_id": "future",
		},
		"legacy.json": processlifecycle.LegacyHarnessSessionRecord{
			SchemaID: processlifecycle.LegacyRecordSchemaID, SessionID: "legacy",
			Harness: "codex", PID: 100, PGID: 100, StartedAt: time.Now().Add(-time.Hour),
		},
	}
	for name, value := range entries {
		var data []byte
		if raw, ok := value.(json.RawMessage); ok {
			data = raw
		} else {
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal %s: %v", name, err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := reapStaleHarnessRecords(dir, 0, time.Now().UTC()); err != nil {
		t.Fatalf("reap records: %v", err)
	}
	for name := range entries {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("recovery evidence %s was not preserved: %v", name, err)
		}
	}
}

func staleEmptyLifecycleRecord(operationID string, updated time.Time) processlifecycle.Record {
	missing := processlifecycle.ProcessIdentity{PID: 999999, BirthTokenScheme: "test/v1", BirthToken: "missing"}
	return processlifecycle.Record{
		SchemaID: processlifecycle.RecordSchemaID, RecordID: operationID + "-record", Revision: 1,
		OperationID: operationID, Harness: "codex",
		OwnerIdentity: missing, SupervisorIdentity: missing, DirectChildIdentity: missing,
		BoundaryIdentity: "unix-pgid:999999", BoundaryType: processlifecycle.BoundaryTypeUnixProcessGroup,
		BoundaryProcessIdentity: missing, State: processlifecycle.StateCleanupFailed,
		Timestamps:     processlifecycle.Timestamps{CreatedAt: updated, UpdatedAt: updated},
		EscapeEvidence: []processlifecycle.EscapeEvidence{{Kind: "prior_cleanup_failed", ObservedAt: updated}},
	}
}
