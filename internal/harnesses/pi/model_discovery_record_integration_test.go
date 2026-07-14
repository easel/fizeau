//go:build integration

package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/pty/cassette"
)

func Test_modelDiscoveryRecordPI(t *testing.T) {
	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
		t.Skip("set FIZEAU_HARNESS_RECORD=1 to refresh the Pi model cassette")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Record the subscription-backed Gemini surface rather than account-local
	// HTTP providers so the checked-in fixture is portable across machines.
	snapshot, err := readPiModelDiscoveryFromListModels(ctx, "", "--list-models", "google-gemini-cli")
	if err != nil {
		t.Fatalf("record Pi model discovery: %v", err)
	}
	record := cassette.DiscoveryRecord{
		Source:            snapshot.Source,
		Status:            "ok",
		Models:            snapshot.Models,
		ReasoningLevels:   snapshot.ReasoningLevels,
		CapturedAt:        snapshot.CapturedAt.UTC().Format(time.RFC3339),
		FreshnessWindow:   snapshot.FreshnessWindow,
		StalenessBehavior: "stale model discovery evidence requires authenticated CLI refresh before capability promotion",
		Metadata:          map[string]any{"detail": snapshot.Detail},
	}
	writeModelDiscoveryRecord(t, filepath.Join("testdata", "model_surface", cassette.DiscoveryFile), record)
}

func writeModelDiscoveryRecord(t *testing.T, path string, record cassette.DiscoveryRecord) {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
