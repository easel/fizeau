package fizeau_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func TestHarnessCleanupTimeoutExternalCompatibility(t *testing.T) {
	opts := fizeau.ServiceOptions{HarnessCleanupTimeout: time.Second}
	if opts.HarnessCleanupTimeout != time.Second {
		t.Fatalf("external HarnessCleanupTimeout = %s", opts.HarnessCleanupTimeout)
	}
}

func TestServiceFinalTypedClassificationRoundTrip(t *testing.T) {
	want := fizeau.ServiceFinalData{
		Status:         "failed",
		Outcome:        fizeau.SessionOutcome("future_outcome"),
		Cause:          fizeau.TerminalCause("future_cause"),
		Stage:          fizeau.SessionStage("future_stage"),
		PrimaryOutcome: fizeau.SessionOutcomeSuccess,
		PrimaryCause:   fizeau.TerminalCauseCompleted,
		PrimaryStage:   fizeau.SessionStageHarness,
		Error:          "diagnostic only",
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range [][]byte{[]byte(`"outcome"`), []byte(`"cause"`), []byte(`"stage"`)} {
		if !bytes.Contains(raw, key) {
			t.Fatalf("required key %s missing from %s", key, raw)
		}
	}

	var got fizeau.ServiceFinalData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Outcome != want.Outcome || got.Cause != want.Cause || got.Stage != want.Stage {
		t.Fatalf("unknown additive values changed: got %q/%q/%q", got.Outcome, got.Cause, got.Stage)
	}
	if got.PrimaryOutcome != want.PrimaryOutcome || got.PrimaryCause != want.PrimaryCause || got.PrimaryStage != want.PrimaryStage {
		t.Fatalf("primary tuple changed: got %q/%q/%q", got.PrimaryOutcome, got.PrimaryCause, got.PrimaryStage)
	}

	// Legacy pre-v0.15 JSON remains decodable. Empty typed fields are an
	// explicit unknown legacy fact and never fabricate success.
	var legacy fizeau.ServiceFinalData
	if err := json.Unmarshal([]byte(`{"status":"success","error":"legacy"}`), &legacy); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if legacy.Outcome != "" || legacy.Cause != "" || legacy.Stage != "" {
		t.Fatalf("legacy terminal tuple was fabricated: %#v", legacy)
	}
}

func TestPublicFinalCostPointerFieldsCompile(t *testing.T) {
	cost := 1.25
	final := fizeau.ServiceFinalData{
		CostUSD:    &cost,
		CostSource: fizeau.CostSourceReported,
	}
	drain := fizeau.DrainExecuteResult{
		CostUSD:    &cost,
		CostSource: fizeau.CostSourceConfigured,
	}
	override := fizeau.ServiceOverrideOutcome{
		CostUSD:    &cost,
		CostSource: fizeau.CostSourceReported,
	}

	if final.CostUSD == nil || *final.CostUSD != cost || final.CostSource != fizeau.CostSourceReported {
		t.Fatalf("final cost = %v/%q, want %v/reported", final.CostUSD, final.CostSource, cost)
	}
	if drain.CostUSD == nil || *drain.CostUSD != cost || drain.CostSource != fizeau.CostSourceConfigured {
		t.Fatalf("drain cost = %v/%q, want %v/configured", drain.CostUSD, drain.CostSource, cost)
	}
	if override.CostUSD == nil || *override.CostUSD != cost || override.CostSource != fizeau.CostSourceReported {
		t.Fatalf("override cost = %v/%q, want %v/reported", override.CostUSD, override.CostSource, cost)
	}

	unknownFinal := fizeau.ServiceFinalData{CostUSD: nil, CostSource: fizeau.CostSourceUnknown}
	unknownDrain := fizeau.DrainExecuteResult{CostUSD: nil, CostSource: fizeau.CostSourceUnknown}
	unknownOverride := fizeau.ServiceOverrideOutcome{CostUSD: nil, CostSource: fizeau.CostSourceUnknown}
	if unknownFinal.CostUSD != nil || unknownDrain.CostUSD != nil || unknownOverride.CostUSD != nil {
		t.Fatal("unknown public costs must preserve nil pointer presence")
	}
	if unknownFinal.CostSource != fizeau.CostSourceUnknown ||
		unknownDrain.CostSource != fizeau.CostSourceUnknown ||
		unknownOverride.CostSource != fizeau.CostSourceUnknown {
		t.Fatal("unknown public costs must expose unknown provenance")
	}
}

func TestPublicSessionEndTypedValuesDurableRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cost := 0.0
	logger := fizeau.NewSessionLogger(dir, "public-terminal")
	logger.Emit(fizeau.EventSessionEnd, fizeau.SessionEndData{
		Status:         fizeau.StatusSuccess,
		Outcome:        fizeau.SessionOutcomeFailed,
		Cause:          fizeau.TerminalCauseCleanupFailed,
		Stage:          fizeau.SessionStageCleanup,
		PrimaryOutcome: fizeau.SessionOutcomeSuccess,
		PrimaryCause:   fizeau.TerminalCauseCompleted,
		PrimaryStage:   fizeau.SessionStageHarness,
		CostUSD:        &cost,
		CostSource:     fizeau.CostSourceReported,
	})
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events, err := fizeau.ReadSessionEvents(filepath.Join(dir, "public-terminal.jsonl"))
	if err != nil {
		t.Fatalf("ReadSessionEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	var got fizeau.SessionEndData
	if err := json.Unmarshal(events[0].Data, &got); err != nil {
		t.Fatalf("decode session.end: %v", err)
	}
	if got.Cause != fizeau.TerminalCauseCleanupFailed || got.Stage != fizeau.SessionStageCleanup {
		t.Fatalf("root-only durable tuple = %q/%q/%q", got.Outcome, got.Cause, got.Stage)
	}
	if got.PrimaryCause != fizeau.TerminalCauseCompleted || got.PrimaryStage != fizeau.SessionStageHarness {
		t.Fatalf("root-only durable primary tuple = %q/%q/%q", got.PrimaryOutcome, got.PrimaryCause, got.PrimaryStage)
	}
	if got.CostUSD == nil || *got.CostUSD != 0 || got.CostSource != fizeau.CostSourceReported {
		t.Fatalf("root-only durable cost = %v/%q, want 0/reported", got.CostUSD, got.CostSource)
	}
}
