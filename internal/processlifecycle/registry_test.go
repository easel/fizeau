package processlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileRegistryListsV1AndSkipsLegacyRecords(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	registry := NewFileRegistry(dir)
	record := validRegistryRecord()
	if err := registry.Create(ctx, record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	legacy := LegacyHarnessSessionRecord{
		SchemaID:  LegacyRecordSchemaID,
		SessionID: "",
		Harness:   "codex",
		Command:   "subprocess",
		PID:       10,
		PGID:      10,
		StartedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), data, 0o600); err != nil {
		t.Fatalf("Write legacy: %v", err)
	}
	records, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 || records[0].RecordID != record.RecordID {
		t.Fatalf("List returned %+v, want only %q", records, record.RecordID)
	}
}

func TestFileRegistryRejectsStaleRevision(t *testing.T) {
	ctx := context.Background()
	registry := NewFileRegistry(t.TempDir())
	record := validRegistryRecord()
	if err := registry.Create(ctx, record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated := cloneRecord(record)
	updated.Revision = 2
	updated.State = StateCleaning
	updated.Timestamps.UpdatedAt = updated.Timestamps.UpdatedAt.Add(time.Second)
	if err := registry.Update(ctx, updated, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	stale := cloneRecord(record)
	stale.Revision = 2
	if err := registry.Update(ctx, stale, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale Update error = %v, want ErrRevisionConflict", err)
	}
	if err := registry.Delete(ctx, record.RecordID, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale Delete error = %v, want ErrRevisionConflict", err)
	}
}

func validRegistryRecord() Record {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	return Record{
		SchemaID:                RecordSchemaID,
		RecordID:                "record-file-1",
		Revision:                1,
		OperationID:             "session-file-1",
		Harness:                 "codex",
		OwnerIdentity:           ProcessIdentity{PID: 101, BirthTokenScheme: "test/v1", BirthToken: "owner-a"},
		BoundaryIdentity:        "pgid:202",
		BoundaryType:            BoundaryTypeTest,
		BoundaryProcessIdentity: ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "boundary-a"},
		State:                   StateOwned,
		Timestamps:              Timestamps{CreatedAt: now, UpdatedAt: now},
		EscapeEvidence:          make([]EscapeEvidence, 0),
	}
}
