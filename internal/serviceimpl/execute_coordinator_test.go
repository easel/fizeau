package serviceimpl

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/transcript"
)

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
