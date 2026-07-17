package serviceimpl

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/transcript"
)

func TestSelectedContextDispatchTypesAreAPINeutral(t *testing.T) {
	wantFields := map[string]reflect.Type{
		"SelectedContextWindow": reflect.TypeOf(int(0)),
		"SelectedContextSource": reflect.TypeOf(""),
	}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(ExecuteDecision{}),
		reflect.TypeOf(NativeDecision{}),
	} {
		for name, want := range wantFields {
			field, ok := typ.FieldByName(name)
			if !ok {
				t.Errorf("%s is missing %s", typ.Name(), name)
				continue
			}
			if field.Type != want || field.Type.PkgPath() != "" {
				t.Errorf("%s.%s type = %v from %q, want API-neutral builtin %v",
					typ.Name(), name, field.Type, field.Type.PkgPath(), want)
			}
		}
	}

	execute := ExecuteDecision{
		Harness:               "fiz",
		Provider:              "alpha",
		ServerInstance:        "alpha-gpu-1",
		Model:                 "fixture-model",
		SelectedContextWindow: 123456,
		SelectedContextSource: "provider_api",
		Candidates: []NativeRouteCandidate{{
			Provider: "alpha", Endpoint: "west", ServerInstance: "alpha-west-1", Model: "fixture-model", Eligible: true,
		}},
	}
	native := nativeDecisionFromExecute(execute)
	if native.Harness != execute.Harness || native.Provider != execute.Provider ||
		native.ServerInstance != execute.ServerInstance || native.Model != execute.Model {
		t.Fatalf("native decision identity = %#v, want execute identity %#v", native, execute)
	}
	if native.SelectedContextWindow != execute.SelectedContextWindow ||
		native.SelectedContextSource != execute.SelectedContextSource {
		t.Fatalf("native selected context = %d/%q, want %d/%q",
			native.SelectedContextWindow, native.SelectedContextSource,
			execute.SelectedContextWindow, execute.SelectedContextSource)
	}
	if len(native.Candidates) != 1 || native.Candidates[0] != execute.Candidates[0] {
		t.Fatalf("native candidates = %#v, want unchanged %#v", native.Candidates, execute.Candidates)
	}
	native.Candidates[0].Model = "mutated"
	if execute.Candidates[0].Model != "fixture-model" {
		t.Fatal("native decision aliases execute candidate storage")
	}

	for _, path := range []string{"execute_coordinator.go", "execute_native.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "github.com/easel/fizeau" || strings.Contains(importPath, "/internal/provider/") {
				t.Errorf("%s imports non-neutral decision type surface %q", path, importPath)
			}
		}
	}
}

type coordinatorSessionLog struct {
	mu        sync.Mutex
	path      string
	ends      []harnesses.FinalData
	overrides []json.RawMessage
	core      []agentcore.Event
	closed    bool
	last      time.Time
}

func (l *coordinatorSessionLog) Enabled() bool { return l != nil }
func (l *coordinatorSessionLog) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
func (l *coordinatorSessionLog) EndWritten() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.ends) > 0
}
func (l *coordinatorSessionLog) ProgressIntervalMS(now time.Time) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() {
		l.last = now
		return 0
	}
	ms := now.Sub(l.last).Milliseconds()
	l.last = now
	return ms
}
func (l *coordinatorSessionLog) WriteCoreEvent(ev agentcore.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.core = append(l.core, ev)
}
func (l *coordinatorSessionLog) WriteOverride(_ agentcore.EventType, raw json.RawMessage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.overrides = append(l.overrides, append(json.RawMessage(nil), raw...))
}
func (l *coordinatorSessionLog) WriteEnd(_ map[string]string, final harnesses.FinalData) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.ends) == 0 {
		l.ends = append(l.ends, final)
	}
}
func (l *coordinatorSessionLog) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
}

func TestExecuteCoordinatorVirtualTerminalOrdering(t *testing.T) {
	hub := NewSessionHub()
	hub.OpenSession("coordinator-virtual")
	log := &coordinatorSessionLog{path: "/tmp/coordinator-virtual.jsonl"}
	override := json.RawMessage(`{
        "session_id":"coordinator-virtual",
        "user_pin":{"harness":"virtual","provider":"","model":"test"},
        "auto_decision":{"harness":"fiz","provider":"local","model":"auto"},
        "axes_overridden":["harness","model"],
        "match_per_axis":{"harness":false,"model":false},
        "auto_score":1,
        "auto_components":{},
        "prompt_features":{"requires_tools":false}
    }`)
	var observedOutcome string
	ch := (ExecuteCoordinator{Hub: hub, Registry: harnesses.NewRegistry()}).RunResolved(context.Background(), ExecuteRequest{
		SessionID:           "coordinator-virtual",
		Prompt:              "hello",
		RequestedModel:      "test",
		RequestedHarness:    "virtual",
		Metadata:            map[string]string{"virtual.response": "ok"},
		FinalMetadata:       map[string]string{"role": "implementer"},
		Decision:            ExecuteDecision{Harness: "virtual", Model: "test"},
		RoutingDecisionData: json.RawMessage(`{"harness":"virtual","model":"test"}`),
		RouteProgress:       transcript.ProgressPayload{Phase: "route", State: "selected", Message: "virtual selected"},
		OverridePayload:     override,
	}, ExecutePorts{
		OpenSessionLog: func() ExecuteSessionLog { return log },
		CatalogPower:   func(string) int { return 3 },
		RecordOverrideOutcome: func(status string) {
			observedOutcome = status
		},
	})

	var events []harnesses.Event
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) < 2 {
		t.Fatalf("events = %d, want terminal pair", len(events))
	}
	if got := events[len(events)-2].Type; got != harnesses.EventType("override") {
		t.Fatalf("penultimate event = %q, want override", got)
	}
	if got := events[len(events)-1].Type; got != harnesses.EventTypeFinal {
		t.Fatalf("last event = %q, want final", got)
	}
	if observedOutcome != "success" {
		t.Fatalf("observed override outcome = %q, want success", observedOutcome)
	}
	var final harnesses.FinalData
	if err := json.Unmarshal(events[len(events)-1].Data, &final); err != nil {
		t.Fatalf("decode final: %v", err)
	}
	if final.Outcome != harnesses.SessionOutcomeSuccess || final.SessionLogPath != log.path {
		t.Fatalf("final = %#v", final)
	}
	if final.RoutingActual == nil || final.RoutingActual.Power != 3 {
		t.Fatalf("routing actual = %#v, want power 3", final.RoutingActual)
	}
	if len(log.overrides) != 1 || len(log.ends) != 1 || !log.closed {
		t.Fatalf("durable lifecycle = overrides:%d ends:%d closed:%v", len(log.overrides), len(log.ends), log.closed)
	}

	late, err := hub.Subscribe("coordinator-virtual")
	if err != nil {
		t.Fatalf("late Subscribe: %v", err)
	}
	lateFinal, ok := <-late
	if !ok || lateFinal.Type != harnesses.EventTypeFinal {
		t.Fatalf("late event = %#v ok=%v", lateFinal, ok)
	}
}

func TestMakeExecuteOverrideEventPreservesFinalCostPresence(t *testing.T) {
	tests := []struct {
		name       string
		final      string
		wantCost   *float64
		wantSource harnesses.CostSource
	}{
		{name: "nil unknown", final: `{"status":"success","cost_source":"unknown","duration_ms":7}`, wantSource: harnesses.CostSourceUnknown},
		{name: "nil amount forces unknown", final: `{"status":"success","cost_source":"reported","duration_ms":7}`, wantSource: harnesses.CostSourceUnknown},
		{name: "known zero", final: `{"status":"success","cost_usd":0,"cost_source":"reported","duration_ms":7}`, wantCost: float64Pointer(0), wantSource: harnesses.CostSourceReported},
		{name: "positive configured", final: `{"status":"success","cost_usd":1.25,"cost_source":"configured","duration_ms":7}`, wantCost: float64Pointer(1.25), wantSource: harnesses.CostSourceConfigured},
		{name: "positive reported", final: `{"status":"success","cost_usd":2.5,"cost_source":"reported","duration_ms":7}`, wantCost: float64Pointer(2.5), wantSource: harnesses.CostSourceReported},
		{name: "invalid source", final: `{"status":"success","cost_usd":3,"cost_source":"invalid","duration_ms":7}`, wantSource: harnesses.CostSourceUnknown},
		{name: "empty source", final: `{"status":"success","cost_usd":3,"cost_source":"","duration_ms":7}`, wantSource: harnesses.CostSourceUnknown},
		{name: "negative amount", final: `{"status":"success","cost_usd":-1,"cost_source":"reported","duration_ms":7}`, wantSource: harnesses.CostSourceUnknown},
		{name: "legacy scalar is not promoted", final: `{"status":"success","cost_usd":4,"duration_ms":7}`, wantSource: harnesses.CostSourceUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, _, ok := makeExecuteOverrideEvent(ExecuteRequest{
				OverridePayload: json.RawMessage(`{"session_id":"cost-test"}`),
			}, harnesses.Event{Type: harnesses.EventTypeFinal, Data: json.RawMessage(test.final)})
			if !ok {
				t.Fatal("makeExecuteOverrideEvent returned ok=false")
			}
			var payload struct {
				Outcome struct {
					CostUSD    *float64             `json:"cost_usd"`
					CostSource harnesses.CostSource `json:"cost_source"`
				} `json:"outcome"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatalf("decode override: %v", err)
			}
			if payload.Outcome.CostSource != test.wantSource {
				t.Fatalf("cost source = %q, want %q", payload.Outcome.CostSource, test.wantSource)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(event.Data, &raw); err != nil {
				t.Fatalf("decode raw override: %v", err)
			}
			var rawOutcome map[string]json.RawMessage
			if err := json.Unmarshal(raw["outcome"], &rawOutcome); err != nil {
				t.Fatalf("decode raw outcome: %v", err)
			}
			if _, ok := rawOutcome["cost_source"]; !ok {
				t.Fatal("cost_source omitted from override outcome")
			}
			if test.wantCost == nil {
				if payload.Outcome.CostUSD != nil {
					t.Fatalf("cost = %v, want nil", payload.Outcome.CostUSD)
				}
				if _, ok := rawOutcome["cost_usd"]; ok {
					t.Fatal("unknown cost_usd was not omitted")
				}
				return
			}
			if payload.Outcome.CostUSD == nil || *payload.Outcome.CostUSD != *test.wantCost {
				t.Fatalf("cost = %v, want %v", payload.Outcome.CostUSD, *test.wantCost)
			}
			if _, ok := rawOutcome["cost_usd"]; !ok {
				t.Fatal("known cost_usd was omitted")
			}
		})
	}
}

func float64Pointer(value float64) *float64 { return &value }

func TestExecuteCoordinatorTerminalSurvivesBackpressure(t *testing.T) {
	out := make(chan harnesses.Event, 1)
	out <- harnesses.Event{Type: harnesses.EventTypeProgress}
	state := &executeRunState{out: out}
	done := make(chan struct{})
	go func() {
		defer close(done)
		state.emitFinal(nil, time.Time{}, harnesses.FinalData{Status: "success"})
	}()

	select {
	case <-done:
		t.Fatal("terminal send returned while the stream was backpressured")
	case <-time.After(1100 * time.Millisecond):
	}
	<-out
	select {
	case ev := <-out:
		if ev.Type != harnesses.EventTypeFinal {
			t.Fatalf("event type = %q, want final", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal was not delivered after backpressure cleared")
	}
	<-done
}

type coordinatorHarness struct {
	events []harnesses.Event
}

func (h coordinatorHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "coordinator-test"}
}
func (h coordinatorHarness) HealthCheck(context.Context) error { return nil }
func (h coordinatorHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	ch := make(chan harnesses.Event, len(h.events))
	for _, ev := range h.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func TestExecuteCoordinatorMalformedSubprocessFinalBecomesOneTerminal(t *testing.T) {
	log := &coordinatorSessionLog{}
	out := make(chan harnesses.Event, 16)
	state := &executeRunState{
		req: ExecuteRequest{
			SessionID: "malformed-subprocess",
			Prompt:    "test",
			Decision:  ExecuteDecision{Harness: "codex", Model: "gpt-test"},
		},
		out:   out,
		start: time.Now(),
		log:   log,
	}
	state.runSubprocess(context.Background(), coordinatorHarness{events: []harnesses.Event{{
		Type: harnesses.EventTypeFinal,
		Time: time.Now().UTC(),
		Data: json.RawMessage(`{"status":`),
	}}})
	close(out)

	var (
		finals  []harnesses.FinalData
		types   []harnesses.EventType
		lastSeq int64 = -1
	)
	for ev := range out {
		types = append(types, ev.Type)
		if ev.Sequence <= lastSeq {
			t.Fatalf("sequence %d after %d", ev.Sequence, lastSeq)
		}
		lastSeq = ev.Sequence
		if ev.Type == harnesses.EventTypeFinal {
			var final harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &final); err != nil {
				t.Fatalf("decode final: %v", err)
			}
			finals = append(finals, final)
		}
	}
	if len(finals) != 1 {
		t.Fatalf("final count = %d, types=%v", len(finals), types)
	}
	if finals[0].Cause != harnesses.TerminalCauseInternalError || finals[0].Stage != harnesses.SessionStageHarness {
		t.Fatalf("terminal tuple = %q/%q", finals[0].Cause, finals[0].Stage)
	}
	if len(types) < 2 || types[len(types)-2] != harnesses.EventTypeProgress || types[len(types)-1] != harnesses.EventTypeFinal {
		t.Fatalf("terminal progress ordering = %v", types)
	}
	if len(log.ends) != 1 {
		t.Fatalf("durable ends = %d, want 1", len(log.ends))
	}
}

func TestExecuteCoordinatorRoutingFailureRetainsFinalForTail(t *testing.T) {
	hub := NewSessionHub()
	hub.OpenSession("routing-failure")
	ch := (ExecuteCoordinator{Hub: hub}).RoutingFailure("routing-failure", map[string]string{"k": "v"}, "no route")
	ev, ok := <-ch
	if !ok || ev.Type != harnesses.EventTypeFinal {
		t.Fatalf("direct event = %#v ok=%v", ev, ok)
	}
	var final harnesses.FinalData
	if err := json.Unmarshal(ev.Data, &final); err != nil {
		t.Fatalf("decode final: %v", err)
	}
	if final.Cause != harnesses.TerminalCauseRouteUnavailable || final.Stage != harnesses.SessionStageRouting {
		t.Fatalf("terminal tuple = %q/%q", final.Cause, final.Stage)
	}
	late, err := hub.Subscribe("routing-failure")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	lateEv, ok := <-late
	if !ok || lateEv.Type != harnesses.EventTypeFinal || string(lateEv.Data) != string(ev.Data) {
		t.Fatalf("late event differs: %#v ok=%v", lateEv, ok)
	}
}
