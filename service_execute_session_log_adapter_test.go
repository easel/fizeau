package fizeau

import (
	"encoding/json"
	"path/filepath"
	"testing"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

func TestExecuteSessionLogAdapterProjectsPublicRoute(t *testing.T) {
	dir := t.TempDir()
	const sessionID = "routing-provenance-session"
	svc := &service{}
	req := ServiceExecuteRequest{
		SessionLogDir: dir,
		Model:         "sonnet",
		Prompt:        "test prompt",
	}
	sl := svc.openExecuteSessionLog(req, RouteDecision{
		Harness:        "claude",
		Provider:       "claude",
		Endpoint:       "subscription",
		ServerInstance: "claude-sonnet-1",
		Model:          "sonnet",
	}, sessionID)
	sl.WriteEnd(nil, harnesses.FinalData{
		Status: string(agentcore.StatusSuccess),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        "claude",
			ServerInstance: "claude-sonnet-1",
			Model:          "sonnet",
		},
	})
	sl.Close()

	events, err := session.ReadEvents(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var start session.SessionStartData
	var end session.SessionEndData
	var routingDecisions int
	for _, event := range events {
		switch event.Type {
		case agentcore.EventSessionStart:
			if err := json.Unmarshal(event.Data, &start); err != nil {
				t.Fatalf("decode session.start: %v", err)
			}
		case agentcore.EventType(ServiceEventTypeRoutingDecision):
			routingDecisions++
		case agentcore.EventSessionEnd:
			if err := json.Unmarshal(event.Data, &end); err != nil {
				t.Fatalf("decode session.end: %v", err)
			}
		}
	}
	if routingDecisions != 1 {
		t.Fatalf("routing_decision count = %d, want 1", routingDecisions)
	}
	if start.ResolvedHarness != "claude" || start.HarnessSource != "auto_route" || start.SelectedEndpoint != "subscription" || start.SelectedServerInstance != "claude-sonnet-1" {
		t.Fatalf("session.start route = %#v", start)
	}
	if start.RequestedHarness != "" {
		t.Fatalf("requested harness = %q, want omitted auto route", start.RequestedHarness)
	}
	if end.ResolvedHarness != "claude" || end.HarnessSource != "auto_route" || end.SelectedEndpoint != "subscription" || end.SelectedServerInstance != "claude-sonnet-1" {
		t.Fatalf("session.end route = %#v", end)
	}
}
