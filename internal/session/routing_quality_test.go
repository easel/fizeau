package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/easel/fizeau/internal/core"
)

func TestScanRoutingQualityWindowAndOverrides(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	writeRoutingQualitySession(t, dir, "in-override", now.Add(-time.Hour), core.EventOverride)
	writeRoutingQualitySession(t, dir, "in-rejected", now.Add(-30*time.Minute), core.EventRejectedOverride)
	writeRoutingQualitySession(t, dir, "in-no-override", now.Add(-15*time.Minute))
	writeRoutingQualitySession(t, dir, "old", now.Add(-30*24*time.Hour), core.EventOverride)

	scan, err := ScanRoutingQuality(dir, &UsageWindow{
		Start: now.Add(-2 * time.Hour),
		End:   now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ScanRoutingQuality: %v", err)
	}
	if scan.TotalRequests != 3 {
		t.Fatalf("TotalRequests = %d, want 3 in-window sessions", scan.TotalRequests)
	}
	if len(scan.OverrideEvents) != 2 {
		t.Fatalf("OverrideEvents = %d, want 2 in-window events", len(scan.OverrideEvents))
	}
	if scan.OverrideEvents[0].Type != core.EventOverride || scan.OverrideEvents[1].Type != core.EventRejectedOverride {
		t.Fatalf("OverrideEvents types = [%s %s], want [override rejected_override]",
			scan.OverrideEvents[0].Type, scan.OverrideEvents[1].Type)
	}
}

func TestScanRoutingQualityToleratesCorruptAndPartialLogs(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	writeRoutingQualitySession(t, dir, "good", now.Add(-time.Hour), core.EventOverride)
	partial := []core.Event{
		{SessionID: "partial", Seq: 0, Type: core.EventSessionStart, Timestamp: now.Add(-30 * time.Minute)},
		{SessionID: "partial", Seq: 1, Type: core.EventRejectedOverride, Timestamp: now.Add(-29 * time.Minute), Data: json.RawMessage(`{"reason":"pin"}`)},
	}
	writeRoutingQualityEvents(t, filepath.Join(dir, "partial.jsonl"), partial)
	appendRoutingQualityBytes(t, filepath.Join(dir, "partial.jsonl"), []byte("{truncated\n"))

	if err := os.WriteFile(filepath.Join(dir, "corrupt.jsonl"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("write corrupt log: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing-target"), filepath.Join(dir, "unreadable.jsonl")); err != nil {
		t.Logf("unreadable-log subcase unavailable on this platform: %v", err)
	}

	scan, err := ScanRoutingQuality(dir, nil)
	if err != nil {
		t.Fatalf("ScanRoutingQuality: %v", err)
	}
	if scan.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want good and readable partial sessions", scan.TotalRequests)
	}
	if len(scan.OverrideEvents) != 2 {
		t.Fatalf("OverrideEvents = %d, want events from good and readable partial logs", len(scan.OverrideEvents))
	}
}

func TestScanRoutingQualityPreservesDirectoryReadErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write non-directory path: %v", err)
	}

	_, err := ScanRoutingQuality(path, nil)
	if err == nil {
		t.Fatal("ScanRoutingQuality error = nil, want directory-read error")
	}
	if !strings.Contains(err.Error(), "routing-quality: reading session log dir") {
		t.Fatalf("ScanRoutingQuality error = %q, want directory-read context", err)
	}
}

func writeRoutingQualitySession(t *testing.T, dir, sessionID string, startedAt time.Time, overrideType ...core.EventType) {
	t.Helper()
	events := []core.Event{{
		SessionID: sessionID,
		Seq:       0,
		Type:      core.EventSessionStart,
		Timestamp: startedAt,
	}}
	if len(overrideType) > 0 {
		events = append(events, core.Event{
			SessionID: sessionID,
			Seq:       1,
			Type:      overrideType[0],
			Timestamp: startedAt.Add(time.Second),
			Data:      json.RawMessage(`{"axes_overridden":["model"]}`),
		})
	}
	writeRoutingQualityEvents(t, filepath.Join(dir, sessionID+".jsonl"), events)
}

func writeRoutingQualityEvents(t *testing.T, path string, events []core.Event) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, event := range events {
		if err := enc.Encode(event); err != nil {
			t.Fatalf("encode event: %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func appendRoutingQualityBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}
