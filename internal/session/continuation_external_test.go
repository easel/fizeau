package session_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func TestContinuationLineageRoundTrip(t *testing.T) {
	const (
		parentID = "parent-fizeau-session"
		childID  = "child-fizeau-session"
	)

	finalWant := fizeau.ServiceFinalData{
		Status:          "success",
		ParentSessionID: parentID,
		Continuation:    fizeau.ContinuationResumed,
	}
	finalRaw, err := json.Marshal(finalWant)
	if err != nil {
		t.Fatalf("Marshal final: %v", err)
	}
	if bytes.Contains(finalRaw, []byte(`"continuation_policy"`)) {
		t.Fatalf("public final invented requested-policy field: %s", finalRaw)
	}
	var finalGot fizeau.ServiceFinalData
	if err := json.Unmarshal(finalRaw, &finalGot); err != nil {
		t.Fatalf("Unmarshal final: %v", err)
	}
	if finalGot.ParentSessionID != parentID || finalGot.Continuation != fizeau.ContinuationResumed {
		t.Fatalf("final lineage = %q/%q", finalGot.ParentSessionID, finalGot.Continuation)
	}

	finalEvents := make(chan fizeau.ServiceEvent, 1)
	finalEvents <- fizeau.ServiceEvent{Type: fizeau.ServiceEventTypeFinal, Data: finalRaw}
	close(finalEvents)
	drained, err := fizeau.DrainExecute(context.Background(), finalEvents)
	if err != nil {
		t.Fatalf("DrainExecute: %v", err)
	}
	if drained.ParentSessionID != parentID || drained.Continuation != fizeau.ContinuationResumed {
		t.Fatalf("drained lineage = %q/%q", drained.ParentSessionID, drained.Continuation)
	}

	dir := t.TempDir()
	logger := fizeau.NewSessionLogger(dir, childID)
	logger.Emit(fizeau.EventSessionStart, fizeau.SessionStartData{
		ParentSessionID:    parentID,
		ContinuationPolicy: fizeau.ContinuationPreferResume,
		Continuation:       fizeau.ContinuationResumed,
		Provider:           "subscription",
		Model:              "test-model",
		Prompt:             "continue the work",
	})
	logger.Emit(fizeau.EventSessionEnd, fizeau.SessionEndData{
		Status:             fizeau.StatusSuccess,
		ParentSessionID:    parentID,
		ContinuationPolicy: fizeau.ContinuationPreferResume,
		Continuation:       fizeau.ContinuationResumed,
	})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := fizeau.ReadSessionEvents(filepath.Join(dir, childID+".jsonl"))
	if err != nil {
		t.Fatalf("ReadSessionEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	var start fizeau.SessionStartData
	if err := json.Unmarshal(events[0].Data, &start); err != nil {
		t.Fatalf("decode session.start: %v", err)
	}
	var end fizeau.SessionEndData
	if err := json.Unmarshal(events[1].Data, &end); err != nil {
		t.Fatalf("decode session.end: %v", err)
	}
	for label, got := range map[string]struct {
		parent      string
		requested   fizeau.ContinuationPolicy
		disposition fizeau.ContinuationDisposition
	}{
		"start": {start.ParentSessionID, start.ContinuationPolicy, start.Continuation},
		"end":   {end.ParentSessionID, end.ContinuationPolicy, end.Continuation},
	} {
		if got.parent != parentID || got.requested != fizeau.ContinuationPreferResume || got.disposition != fizeau.ContinuationResumed {
			t.Fatalf("%s lineage = %q/%q/%q", label, got.parent, got.requested, got.disposition)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       continuationSessionConfig{sessionLogDir: dir},
		SessionLogDir:       dir,
		QuotaRefreshContext: ctx,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var replay strings.Builder
	if err := svc.ReplaySession(context.Background(), childID, &replay); err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}
	for _, want := range []string{
		"Parent session: " + parentID,
		"Continuation requested: prefer_resume",
		"Actual: resumed",
	} {
		if !strings.Contains(replay.String(), want) {
			t.Fatalf("replay missing %q:\n%s", want, replay.String())
		}
	}
}

type continuationSessionConfig struct {
	sessionLogDir string
}

func (continuationSessionConfig) ProviderNames() []string     { return nil }
func (continuationSessionConfig) DefaultProviderName() string { return "" }
func (continuationSessionConfig) Provider(string) (fizeau.ServiceProviderEntry, bool) {
	return fizeau.ServiceProviderEntry{}, false
}
func (continuationSessionConfig) HealthCooldown() time.Duration { return 0 }
func (continuationSessionConfig) WorkDir() string               { return "" }
func (c continuationSessionConfig) SessionLogDir() string       { return c.sessionLogDir }
