//go:build testseam

package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// @covers US-004-AC3
func TestExecuteRouteEvidenceNoRetryAfterSelectedDispatchFailure(t *testing.T) {
	policyStatement := "agent does not retry candidate 2 after selected candidate dispatch failure"
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {Type: "test", BaseURL: "http://127.0.0.1:1/v1", Model: "route-model"},
			"beta":  {Type: "test", BaseURL: "http://127.0.0.1:2/v1", Model: "route-model"},
		},
		names:       []string{"alpha", "beta"},
		defaultName: "alpha",
	}
	var calls atomic.Int64
	opts := ServiceOptions{ServiceConfig: sc}
	opts.FakeProvider = &FakeProvider{
		InjectError: func(int) error {
			calls.Add(1)
			return errors.New("connection unauthorized")
		},
	}
	svc, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt: "try once",
		Model:  "route-model",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainServiceEvents(t, ch, 2*time.Second)
	if calls.Load() != 1 {
		t.Fatalf("policy_statement=%q: provider calls=%d, want exactly one selected-candidate attempt", policyStatement, calls.Load())
	}
	final := finalServiceData(t, events)
	if final.Status != "failed" {
		t.Fatalf("policy_statement=%q: final status=%q, want failed", policyStatement, final.Status)
	}
	if final.RoutingActual == nil {
		t.Fatalf("policy_statement=%q: final routing_actual is nil", policyStatement)
	}
	if final.RoutingActual.Provider != "alpha" || final.RoutingActual.Model != "route-model" {
		t.Fatalf("policy_statement=%q: routing_actual=%#v, want attempted alpha/route-model", policyStatement, final.RoutingActual)
	}
	if final.RoutingActual.ServerInstance == "" {
		t.Fatalf("policy_statement=%q: routing_actual server instance should be populated", policyStatement)
	}
	if final.RoutingActual.FailureClass != "transport" {
		t.Fatalf("policy_statement=%q: failure_class=%q, want transport", policyStatement, final.RoutingActual.FailureClass)
	}
	if got := final.RoutingActual.FallbackChainFired; len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("policy_statement=%q: attempted providers=%v, want [alpha]", policyStatement, got)
	}
}

func drainServiceEvents(t *testing.T, ch <-chan ServiceEvent, timeout time.Duration) []ServiceEvent {
	t.Helper()
	var events []ServiceEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timer.C:
			t.Fatalf("timed out waiting for service events")
			return events
		}
	}
}

func finalServiceData(t *testing.T, events []ServiceEvent) ServiceFinalData {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "final" {
			continue
		}
		var final ServiceFinalData
		if err := json.Unmarshal(events[i].Data, &final); err != nil {
			t.Fatalf("unmarshal final: %v", err)
		}
		return final
	}
	t.Fatalf("final event not found in %v", events)
	return ServiceFinalData{}
}
