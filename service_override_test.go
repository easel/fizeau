package fizeau_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
	agent "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

func sessionScanRoutingQuality(dir string) (*session.RoutingQualityScan, error) {
	return session.ScanRoutingQuality(dir, nil)
}

// drainOverrideEvents collects events from ch until close or timeout.
func drainOverrideEvents(t *testing.T, ch <-chan fizeau.ServiceEvent, timeout time.Duration) []fizeau.ServiceEvent {
	t.Helper()
	var events []fizeau.ServiceEvent
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline.C:
			t.Fatalf("timed out after %s waiting for channel close; collected %d events: %v", timeout, len(events), overrideEventTypes(events))
			return events
		}
	}
}

func overrideEventTypes(events []fizeau.ServiceEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = string(ev.Type)
	}
	return out
}

func overrideIndexEventType(events []fizeau.ServiceEvent, want string) int {
	for i, ev := range events {
		if string(ev.Type) == want {
			return i
		}
	}
	return -1
}

func overrideFinalData(t *testing.T, ev *fizeau.ServiceEvent) fizeau.ServiceFinalData {
	t.Helper()
	if ev == nil {
		t.Fatal("expected final event, got nil")
	}
	var payload fizeau.ServiceFinalData
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	return payload
}

func overrideFindFinal(events []fizeau.ServiceEvent) *fizeau.ServiceEvent {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == harnesses.EventTypeFinal {
			ev := events[i]
			return &ev
		}
	}
	return nil
}

// findOverride returns the override event from a drained slice, or nil.
func findOverride(events []fizeau.ServiceEvent) *fizeau.ServiceEvent {
	for i := range events {
		if string(events[i].Type) == fizeau.ServiceEventTypeOverride {
			ev := events[i]
			return &ev
		}
	}
	return nil
}

func decodeOverride(t *testing.T, ev *fizeau.ServiceEvent) fizeau.ServiceOverrideData {
	t.Helper()
	if ev == nil {
		t.Fatal("expected override event, got nil")
	}
	var payload fizeau.ServiceOverrideData
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}
	return payload
}

func countEventType(events []fizeau.ServiceEvent, want string) int {
	count := 0
	for _, ev := range events {
		if string(ev.Type) == want {
			count++
		}
	}
	return count
}

func drainServiceEvents(t *testing.T, ch <-chan fizeau.ServiceEvent, timeout time.Duration) []fizeau.ServiceEvent {
	t.Helper()
	var events []fizeau.ServiceEvent
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline.C:
			t.Fatalf("timed out after %s waiting for channel close; collected %d events", timeout, len(events))
			return events
		}
	}
}

func extractSessionIDFromRoutingDecision(t *testing.T, ev fizeau.ServiceEvent) string {
	t.Helper()
	if ev.Type != fizeau.ServiceEventTypeRoutingDecision {
		t.Fatalf("expected routing_decision event, got %q", ev.Type)
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal routing_decision: %v", err)
	}
	return payload.SessionID
}

func sessionLogTypes(entries []agent.Event) []string {
	types := make([]string, len(entries))
	for i, ev := range entries {
		types[i] = string(ev.Type)
	}
	return types
}

func indexString(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

func countString(xs []string, want string) int {
	count := 0
	for _, x := range xs {
		if x == want {
			count++
		}
	}
	return count
}

func captureServiceLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	return &buf, func() {
		slog.SetDefault(prev)
	}
}

// TestExecuteEmitsNoOverrideEventForUnpinnedRequest covers AC #1: a request
// with none of Harness/Provider/Model set must not produce an override or
// rejected_override event. The deterministic "routing under-specified"
// path with no axis pinned and no routing hints exercises the same Execute
// entrypoint that builds the override context, so we can assert that the
// override-event surface is silent (no ErrRejectedOverride wrapper, no
// override events in any channel that may be returned).
func TestExecuteEmitsNoOverrideEventForUnpinnedRequest(t *testing.T) {
	// Use an explicit empty ServiceConfig so this test does not auto-load the
	// developer's repo-local provider config and accidentally perform a live
	// unpinned routing attempt.
	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, execErr := svc.Execute(ctx, fizeau.ServiceExecuteRequest{
		Prompt: "hi",
	})
	cancel()
	// The Execute contract for the unpinned/under-specified path returns a
	// channel that yields a single failed final event and closes — no
	// pre-dispatch typed error and no override surface activity. Either
	// shape is acceptable for AC #1; what is forbidden is any
	// override/rejected_override event or wrapper.
	if execErr != nil {
		var rej *fizeau.ErrRejectedOverride
		if errors.As(execErr, &rej) {
			t.Fatalf("rejected_override fired for unpinned request: %+v", rej.Event)
		}
		return
	}
	if ch == nil {
		t.Fatal("Execute returned nil channel and nil error for unpinned request")
	}
	events := drainOverrideEvents(t, ch, 2*time.Second)
	if ov := findOverride(events); ov != nil {
		t.Fatalf("unpinned request emitted override event: %+v", ov)
	}
	for _, ev := range events {
		if string(ev.Type) == fizeau.ServiceEventTypeRejectedOverride {
			t.Fatalf("unpinned request emitted rejected_override event: %+v", ev)
		}
	}
}

// TestExecuteEmitsOverrideEventBeforeFinal covers AC #2: any pinned axis
// produces exactly one override event before the final event. Tested with
// each axis combination dispatchable through the virtual harness.
//
// Pure provider-only and model-only cases (without Harness) cannot reach
// successful dispatch — they pre-fail validation and emit
// rejected_override instead. Those paths are covered by
// TestRejectedOverrideOnUnknownProvider and TestRejectedOverrideEventOnOrphanModel.
func TestExecuteEmitsOverrideEventBeforeFinal(t *testing.T) {
	cases := []struct {
		name     string
		req      fizeau.ServiceExecuteRequest
		wantAxes []string
	}{
		{
			name: "harness_only_virtual",
			req: fizeau.ServiceExecuteRequest{
				Prompt:  "hi",
				Harness: "virtual",
				Metadata: map[string]string{
					"virtual.response": "ok",
				},
			},
			wantAxes: []string{"harness"},
		},
		{
			name: "harness_and_provider_virtual",
			req: fizeau.ServiceExecuteRequest{
				Prompt:   "hi",
				Harness:  "virtual",
				Provider: "synthetic",
				Metadata: map[string]string{
					"virtual.response": "ok",
				},
			},
			wantAxes: []string{"harness", "provider"},
		},
		{
			name: "harness_and_model_virtual",
			req: fizeau.ServiceExecuteRequest{
				Prompt:  "hi",
				Harness: "virtual",
				Model:   "recorded",
				Metadata: map[string]string{
					"virtual.response": "ok",
				},
			},
			wantAxes: []string{"harness", "model"},
		},
		{
			name: "all_three_axes_virtual",
			req: fizeau.ServiceExecuteRequest{
				Prompt:   "hi",
				Harness:  "virtual",
				Provider: "synthetic",
				Model:    "recorded",
				Metadata: map[string]string{
					"virtual.response": "ok",
				},
			},
			wantAxes: []string{"harness", "provider", "model"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ch, err := svc.Execute(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			events := drainOverrideEvents(t, ch, 15*time.Second)
			ovIdx := overrideIndexEventType(events, fizeau.ServiceEventTypeOverride)
			finIdx := overrideIndexEventType(events, fizeau.ServiceEventTypeFinal)
			if ovIdx < 0 {
				t.Fatalf("expected override event, got types=%v", overrideEventTypes(events))
			}
			if finIdx < 0 {
				t.Fatalf("expected final event, got types=%v", overrideEventTypes(events))
			}
			if ovIdx >= finIdx {
				t.Fatalf("override event must precede final: ov=%d final=%d types=%v", ovIdx, finIdx, overrideEventTypes(events))
			}
			// Exactly one override event.
			count := 0
			for _, ev := range events {
				if string(ev.Type) == fizeau.ServiceEventTypeOverride {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("override event count: got %d, want 1", count)
			}
			payload := decodeOverride(t, &events[ovIdx])
			if !equalStringSets(payload.AxesOverridden, tc.wantAxes) {
				t.Fatalf("axes_overridden: got %v, want %v", payload.AxesOverridden, tc.wantAxes)
			}
		})
	}
}

func TestExecuteSessionLogOverrideEventPersistsBeforeClose(t *testing.T) {
	dir := t.TempDir()
	logs, restore := captureServiceLogs(t)
	defer restore()

	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := fizeau.ServiceExecuteRequest{
		Prompt:        "hi",
		Harness:       "virtual",
		SessionLogDir: dir,
		Metadata: map[string]string{
			"virtual.response": "virtual ok",
			"virtual.delay_ms": "150",
		},
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var first fizeau.ServiceEvent
	select {
	case firstEv, ok := <-ch:
		if !ok {
			t.Fatal("Execute channel closed before routing_decision")
		}
		first = firstEv
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routing_decision")
	}
	if first.Type != fizeau.ServiceEventTypeRoutingDecision {
		t.Fatalf("first event type: want routing_decision, got %q", first.Type)
	}

	sessionID := extractSessionIDFromRoutingDecision(t, first)
	tailCh, err := svc.TailSessionLog(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("TailSessionLog: %v", err)
	}

	execEvents := append([]fizeau.ServiceEvent{first}, drainServiceEvents(t, ch, 5*time.Second)...)
	tailEvents := drainServiceEvents(t, tailCh, 5*time.Second)

	if countEventType(execEvents, fizeau.ServiceEventTypeFinal) != 1 {
		t.Fatalf("Execute final count: want 1, got %d (types=%v)", countEventType(execEvents, fizeau.ServiceEventTypeFinal), overrideEventTypes(execEvents))
	}
	if countEventType(execEvents, fizeau.ServiceEventTypeOverride) != 1 {
		t.Fatalf("Execute override count: want 1, got %d (types=%v)", countEventType(execEvents, fizeau.ServiceEventTypeOverride), overrideEventTypes(execEvents))
	}
	if countEventType(tailEvents, fizeau.ServiceEventTypeFinal) != 1 {
		t.Fatalf("Tail final count: want 1, got %d (types=%v)", countEventType(tailEvents, fizeau.ServiceEventTypeFinal), overrideEventTypes(tailEvents))
	}
	if countEventType(tailEvents, fizeau.ServiceEventTypeOverride) != 1 {
		t.Fatalf("Tail override count: want 1, got %d (types=%v)", countEventType(tailEvents, fizeau.ServiceEventTypeOverride), overrideEventTypes(tailEvents))
	}
	if overrideIndexEventType(tailEvents, fizeau.ServiceEventTypeOverride) > overrideIndexEventType(tailEvents, fizeau.ServiceEventTypeFinal) {
		t.Fatalf("Tail override must precede final: types=%v", overrideEventTypes(tailEvents))
	}

	entries, err := session.ReadEvents(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	got := sessionLogTypes(entries)
	startIdx := indexString(got, "session.start")
	overrideIdx := indexString(got, "override")
	endIdx := indexString(got, "session.end")
	if startIdx < 0 || overrideIdx < 0 || endIdx < 0 {
		t.Fatalf("session log missing required records: got %v", got)
	}
	if !(startIdx < overrideIdx && overrideIdx < endIdx) {
		t.Fatalf("session log order: want session.start < override < session.end, got %v", got)
	}
	if countString(got, "session.start") != 1 || countString(got, "override") != 1 || countString(got, "session.end") != 1 {
		t.Fatalf("session log counts: want one each of start/override/end, got %v", got)
	}
	if strings.Contains(logs.String(), "session logger: write error") {
		t.Fatalf("unexpected closed-file warning in regression path:\n%s", logs.String())
	}
}

// TestOverrideEventCoincidentalAgreementStillFiresViaPublicAPI covers the
// "event still fires" half of AC #3 against the public Execute entrypoint:
// even when the override-axes-stripped resolution can't synthesize a real
// auto decision (no ServiceConfig, virtual harness), the override event
// must still be emitted before the final event. The match_per_axis=true
// half of AC #3 is exercised in service_override_internal_test.go's
// TestOverrideEventCoincidentalAgreement, where a real fakeServiceConfig
// anchors the auto resolution to the same value the user pinned.
func TestOverrideEventCoincidentalAgreementStillFiresViaPublicAPI(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "virtual",
		Metadata: map[string]string{
			"virtual.response": "ok",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainOverrideEvents(t, ch, 15*time.Second)
	ov := findOverride(events)
	if ov == nil {
		t.Fatalf("override event missing; types=%v", overrideEventTypes(events))
	}
	payload := decodeOverride(t, ov)
	if len(payload.MatchPerAxis) != len(payload.AxesOverridden) {
		t.Fatalf("match_per_axis must have entry per axis: per_axis=%v axes=%v",
			payload.MatchPerAxis, payload.AxesOverridden)
	}
}

// TestOverrideEventAxesOverriddenIsExplicit covers AC #4: axes_overridden
// lists exactly the axes the caller pinned.
func TestOverrideEventAxesOverriddenIsExplicit(t *testing.T) {
	opts := fizeau.ServiceOptions{}
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "virtual",
		Model:   "recorded",
		Metadata: map[string]string{
			"virtual.response": "ok",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainOverrideEvents(t, ch, 15*time.Second)
	payload := decodeOverride(t, findOverride(events))
	want := []string{"harness", "model"}
	if !equalStringSets(payload.AxesOverridden, want) {
		t.Fatalf("axes_overridden: got %v, want %v", payload.AxesOverridden, want)
	}
	if payload.UserPin.Harness != "virtual" || payload.UserPin.Model != "recorded" {
		t.Fatalf("user_pin: got %+v, want harness=virtual model=recorded", payload.UserPin)
	}
	if payload.UserPin.Provider != "" {
		t.Fatalf("user_pin.provider: want empty, got %q", payload.UserPin.Provider)
	}
}

// TestOverrideEventOutcomePopulatedFromFinal covers AC #5: outcome fields
// are populated post-execution from the final event.
func TestOverrideEventOutcomePopulatedFromFinal(t *testing.T) {
	opts := fizeau.ServiceOptions{}
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "virtual",
		Metadata: map[string]string{
			"virtual.response": "ok",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainOverrideEvents(t, ch, 15*time.Second)
	payload := decodeOverride(t, findOverride(events))
	final := overrideFindFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	finalPayload := overrideFinalData(t, final)
	if payload.Outcome == nil {
		t.Fatal("override.outcome: want populated, got nil")
	}
	if payload.Outcome.Status != finalPayload.Status {
		t.Fatalf("outcome.status: got %q, want %q", payload.Outcome.Status, finalPayload.Status)
	}
	if payload.Outcome.DurationMS != finalPayload.DurationMS {
		t.Fatalf("outcome.duration_ms: got %d, want %d", payload.Outcome.DurationMS, finalPayload.DurationMS)
	}
	if (payload.Outcome.CostUSD == nil) != (finalPayload.CostUSD == nil) ||
		(payload.Outcome.CostUSD != nil && *payload.Outcome.CostUSD != *finalPayload.CostUSD) {
		t.Fatalf("outcome.cost_usd: got %v, want %v", payload.Outcome.CostUSD, finalPayload.CostUSD)
	}
	if payload.Outcome.CostSource != finalPayload.CostSource {
		t.Fatalf("outcome.cost_source: got %q, want %q", payload.Outcome.CostSource, finalPayload.CostSource)
	}
}

// TestRejectedOverrideEventOnOrphanModel covers AC #6: a pin that fails
// pre-dispatch (orphan model) produces a rejected_override event and no
// override event. The wrapped typed error carries the payload so callers
// can extract it via AsRejectedOverride.
func TestRejectedOverrideEventOnOrphanModel(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, execErr := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "gemini",
		Model:   "minimax/minimax-m2.7",
	})
	if execErr == nil {
		t.Fatal("expected typed pin error, got nil")
	}
	if ch != nil {
		t.Fatalf("expected nil channel for typed pin error, got %#v", ch)
	}
	// The rejected_override payload is reachable via the wrapper error.
	rejected, ok := fizeau.AsRejectedOverride(execErr)
	if !ok {
		t.Fatalf("AsRejectedOverride: expected wrapper carrying rejected_override payload, got %T %v", execErr, execErr)
	}
	if !equalStringSets(rejected.AxesOverridden, []string{"harness", "model"}) {
		t.Fatalf("rejected.axes_overridden: got %v", rejected.AxesOverridden)
	}
	if rejected.UserPin.Harness != "gemini" || rejected.UserPin.Model != "minimax/minimax-m2.7" {
		t.Fatalf("rejected.user_pin: got %+v", rejected.UserPin)
	}
	if rejected.Outcome != nil {
		t.Fatalf("rejected_override must not carry outcome: got %+v", rejected.Outcome)
	}
	if rejected.RejectionError == "" || !strings.Contains(rejected.RejectionError, "minimax") {
		t.Fatalf("rejected.rejection_error must surface pin failure: got %q", rejected.RejectionError)
	}
	// The original typed error is still extractable via errors.As.
	var typed *fizeau.ErrHarnessModelIncompatible
	if !errors.As(execErr, &typed) {
		t.Fatalf("errors.As must still find ErrHarnessModelIncompatible through the wrapper: %T %v", execErr, execErr)
	}
}

// TestOverrideEventPromptFeaturesPopulation covers AC #7: prompt_features
// reflects ServiceExecuteRequest fields verbatim (estimated_tokens,
// requires_tools, reasoning).
func TestOverrideEventPromptFeaturesPopulation(t *testing.T) {
	opts := fizeau.ServiceOptions{}
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:                "hi",
		Harness:               "virtual",
		EstimatedPromptTokens: 12500,
		RequiresTools:         true,
		Reasoning:             fizeau.ReasoningHigh,
		Metadata: map[string]string{
			"virtual.response": "ok",
			"override.reason":  "needed for benchmark replay",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainOverrideEvents(t, ch, 15*time.Second)
	payload := decodeOverride(t, findOverride(events))
	if payload.PromptFeatures.EstimatedTokens == nil || *payload.PromptFeatures.EstimatedTokens != 12500 {
		t.Fatalf("prompt_features.estimated_tokens: got %v, want 12500",
			payload.PromptFeatures.EstimatedTokens)
	}
	if !payload.PromptFeatures.RequiresTools {
		t.Fatalf("prompt_features.requires_tools: want true")
	}
	if payload.PromptFeatures.Reasoning != string(fizeau.ReasoningHigh) {
		t.Fatalf("prompt_features.reasoning: got %q, want %q",
			payload.PromptFeatures.Reasoning, fizeau.ReasoningHigh)
	}
	if payload.ReasonHint != "needed for benchmark replay" {
		t.Fatalf("reason_hint: got %q", payload.ReasonHint)
	}

	// Without EstimatedPromptTokens, the field is nil.
	ch2, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "virtual",
		Metadata: map[string]string{
			"virtual.response": "ok",
		},
	})
	if err != nil {
		t.Fatalf("Execute2: %v", err)
	}
	events2 := drainOverrideEvents(t, ch2, 5*time.Second)
	payload2 := decodeOverride(t, findOverride(events2))
	if payload2.PromptFeatures.EstimatedTokens != nil {
		t.Fatalf("prompt_features.estimated_tokens unset: got %d, want nil",
			*payload2.PromptFeatures.EstimatedTokens)
	}
	if payload2.ReasonHint != "" {
		t.Fatalf("reason_hint without metadata: got %q", payload2.ReasonHint)
	}
}

// TestRejectedOverridePersistedToSessionLog locks in the bead-2 review fix:
// pre-dispatch rejected_override events must be written to the session log
// so UsageReport's windowed aggregation (which scans session logs, not the
// in-memory ring) sees them. Without this persistence, the rejection class
// is invisible to historical and cross-restart reporting.
func TestRejectedOverrideEventPersistedToSessionLog(t *testing.T) {
	dir := t.TempDir()
	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, execErr := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:        "hi",
		Harness:       "gemini",
		Model:         "minimax/minimax-m2.7",
		SessionLogDir: dir,
	})
	if execErr == nil {
		t.Fatal("expected typed pin error, got nil")
	}
	if ch != nil {
		t.Fatalf("expected nil channel for typed pin error, got %#v", ch)
	}
	if _, ok := fizeau.AsRejectedOverride(execErr); !ok {
		t.Fatalf("expected ErrRejectedOverride wrapper, got %T %v", execErr, execErr)
	}

	// One .jsonl file must exist with session.start + rejected_override.
	scan, err := sessionScanRoutingQuality(dir)
	if err != nil {
		t.Fatalf("ScanRoutingQuality: %v", err)
	}
	if scan.TotalRequests != 1 {
		t.Fatalf("TotalRequests = %d, want 1 (rejection counted in scan)", scan.TotalRequests)
	}
	if len(scan.OverrideEvents) != 1 {
		t.Fatalf("OverrideEvents = %d, want 1 (rejected_override persisted)", len(scan.OverrideEvents))
	}
	got := scan.OverrideEvents[0]
	if string(got.Type) != fizeau.ServiceEventTypeRejectedOverride {
		t.Fatalf("event type = %q, want %q", got.Type, fizeau.ServiceEventTypeRejectedOverride)
	}
	var payload fizeau.ServiceOverrideData
	if err := json.Unmarshal(got.Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.RejectionError == "" {
		t.Fatalf("rejection_error must be set on persisted rejected_override: %+v", payload)
	}
	if payload.Outcome != nil {
		t.Fatalf("rejected_override outcome must be nil, got %+v", payload.Outcome)
	}
}

func equalStringSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gotSet := make(map[string]bool, len(got))
	for _, g := range got {
		gotSet[g] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			return false
		}
	}
	return true
}
