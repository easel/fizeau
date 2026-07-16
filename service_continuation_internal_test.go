package fizeau

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

func TestExecuteOmitsContinuationLineage(t *testing.T) {
	dir := t.TempDir()
	svc := &service{}
	log := svc.openExecuteSessionLog(ServiceExecuteRequest{
		Prompt:        "ordinary execute",
		SessionLogDir: dir,
	}, RouteDecision{}, "ordinary-execute")
	log.WriteEnd(nil, harnesses.FinalData{Status: "success"})
	log.Close()

	events, err := session.ReadEvents(filepath.Join(dir, "ordinary-execute.jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want session.start + session.end", len(events))
	}
	for _, event := range events {
		assertContinuationKeysOmitted(t, event.Data)
	}

	final, err := json.Marshal(ServiceFinalData{Status: "success"})
	if err != nil {
		t.Fatalf("Marshal final: %v", err)
	}
	assertContinuationKeysOmitted(t, final)
}

func TestContinuationEffectiveFreshRequestInternal(t *testing.T) {
	innerMetadata := map[string]string{"source": "inner", "correlation_id": "invalid inner value"}
	outerMetadata := map[string]string{"source": "outer"}
	req := ServiceContinuationRequest{
		Prompt:        "outer prompt",
		Metadata:      outerMetadata,
		CorrelationID: "outer-correlation",
		FreshRequest: ServiceExecuteRequest{
			Prompt:        "inner prompt",
			Metadata:      innerMetadata,
			CorrelationID: "invalid inner correlation id",
			Role:          "reviewer",
			MaxTokens:     42,
		},
	}

	got := effectiveContinuationFreshRequest(req)
	if got.Prompt != req.Prompt || got.CorrelationID != req.CorrelationID {
		t.Fatalf("effective prompt/correlation = %q/%q, want outer %q/%q", got.Prompt, got.CorrelationID, req.Prompt, req.CorrelationID)
	}
	if !reflect.DeepEqual(got.Metadata, outerMetadata) {
		t.Fatalf("effective metadata = %#v, want outer %#v", got.Metadata, outerMetadata)
	}
	if got.Role != "reviewer" || got.MaxTokens != 42 {
		t.Fatalf("unrelated fresh fields were not preserved: %#v", got)
	}
	if req.FreshRequest.Prompt != "inner prompt" || !reflect.DeepEqual(req.FreshRequest.Metadata, innerMetadata) ||
		req.FreshRequest.CorrelationID != "invalid inner correlation id" {
		t.Fatalf("effective request construction mutated caller input: %#v", req.FreshRequest)
	}

	// Unsupported preflight does not consult service-owned routing, hub, log,
	// session, or process state. A nil receiver makes any such consultation a
	// deterministic test failure while the surface remains unimplemented.
	var svc *service
	events, err := svc.Continue(context.Background(), ServiceContinuationRequest{
		SessionID:     "parent",
		Prompt:        "continue",
		Policy:        ContinuationFreshSession,
		CorrelationID: "outer-correlation",
	})
	if events != nil || !errors.Is(err, ErrContinuationUnsupported) {
		t.Fatalf("nil-state continuation preflight = (%v, %v), want nil/%v", events, err, ErrContinuationUnsupported)
	}
}

func assertContinuationKeysOmitted(t *testing.T, raw []byte) {
	t.Helper()
	for _, key := range [][]byte{
		[]byte(`"parent_session_id"`),
		[]byte(`"continuation_policy"`),
		[]byte(`"continuation"`),
	} {
		if bytes.Contains(raw, key) {
			t.Fatalf("ordinary Execute JSON contains %s: %s", key, raw)
		}
	}
}
