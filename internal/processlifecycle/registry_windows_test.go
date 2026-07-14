//go:build windows

package processlifecycle

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"
)

func TestWindowsFileRegistryAtomicCreateUpdateDelete(t *testing.T) {
	ctx := context.Background()
	registry := NewFileRegistry(t.TempDir())
	record := validRegistryRecord()

	if err := registry.Create(ctx, record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := registry.Get(ctx, record.RecordID)
	if err != nil {
		t.Fatalf("Get after Create: %v", err)
	}
	if created.Revision != 1 || created.State != StateOwned {
		t.Fatalf("created record = revision %d state %q, want revision 1 state %q", created.Revision, created.State, StateOwned)
	}

	updated := cloneRecord(record)
	updated.Revision = 2
	updated.State = StateCleaning
	updated.Timestamps.UpdatedAt = updated.Timestamps.UpdatedAt.Add(time.Second)
	if err := registry.Update(ctx, updated, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := registry.Get(ctx, record.RecordID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Revision != 2 || got.State != StateCleaning {
		t.Fatalf("updated record = revision %d state %q, want revision 2 state %q", got.Revision, got.State, StateCleaning)
	}

	if err := registry.Delete(ctx, record.RecordID, 2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := registry.Get(ctx, record.RecordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Get after Delete error = %v, want fs.ErrNotExist", err)
	}
}
