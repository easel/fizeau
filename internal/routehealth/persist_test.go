package routehealth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRouteHealthPersistencePreservesServerInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routehealth.json")
	now := time.Now().UTC()
	store := NewStore()
	if err := store.RecordAttempt(Attempt{
		Harness:        "fiz",
		Provider:       "local",
		Endpoint:       "primary",
		ServerInstance: "desk-a",
		Model:          "qwen",
		Status:         "failed",
		Timestamp:      now,
	}); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}
	if err := SavePersistedState(path, store, nil); err != nil {
		t.Fatalf("SavePersistedState: %v", err)
	}
	snapshot, err := readPersistedRouteHealthSnapshot(path)
	if err != nil {
		t.Fatalf("readPersistedRouteHealthSnapshot: %v", err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("persisted version = %d, want existing version 1", snapshot.Version)
	}
	if len(snapshot.Failures) != 1 || snapshot.Failures[0].Key.ServerInstance != "desk-a" {
		t.Fatalf("persisted failures = %+v, want exact desk-a server route", snapshot.Failures)
	}

	restored := NewStore()
	if err := LoadPersistedState(path, time.Hour, restored, nil); err != nil {
		t.Fatalf("LoadPersistedState: %v", err)
	}
	records := restored.ActiveAttempts(now, time.Hour)
	if len(records) != 1 {
		t.Fatalf("restored active attempts len = %d, want 1: %+v", len(records), records)
	}
	want := Key{
		Harness:        "fiz",
		Provider:       "local",
		Endpoint:       "primary",
		ServerInstance: "desk-a",
		Model:          "qwen",
	}
	if records[0].Key != want {
		t.Fatalf("restored route key = %+v, want %+v", records[0].Key, want)
	}

	legacy := `{
  "version": 1,
  "failures": [{
    "key": {"Harness":"fiz","Provider":"local","Model":"qwen","Endpoint":"primary"},
    "status": "failed",
    "recorded_at": "` + now.Format(time.RFC3339Nano) + `"
  }]
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}
	legacyStore := NewStore()
	if err := LoadPersistedState(path, time.Hour, legacyStore, nil); err != nil {
		t.Fatalf("LoadPersistedState(legacy): %v", err)
	}
	legacyRecords := legacyStore.ActiveAttempts(now, time.Hour)
	if len(legacyRecords) != 1 {
		t.Fatalf("legacy active attempts len = %d, want 1: %+v", len(legacyRecords), legacyRecords)
	}
	if legacyRecords[0].Key.ServerInstance != "" {
		t.Fatalf("legacy ServerInstance = %q, want empty backward-compatible value", legacyRecords[0].Key.ServerInstance)
	}
	if legacyRecords[0].Key.Harness != "fiz" || legacyRecords[0].Key.Provider != "local" || legacyRecords[0].Key.Endpoint != "primary" || legacyRecords[0].Key.Model != "qwen" {
		t.Fatalf("legacy route key = %+v, want original fields intact", legacyRecords[0].Key)
	}
}
