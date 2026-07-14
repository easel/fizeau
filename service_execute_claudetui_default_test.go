package fizeau

import (
	"context"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// TestExecuteDefaultClaudeUnrestrictedSelectsClaudeTUI proves the complete
// Service.Execute composition: an unpinned, default-policy unrestricted
// request routes over the shared Claude surface, selects claude-tui, and hands
// the concrete claude-tui runner to subprocess dispatch.
func TestExecuteDefaultClaudeUnrestrictedSelectsClaudeTUI(t *testing.T) {
	t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", "")
	// The registry below supplies hermetic discovery. Keeping the process PATH
	// empty makes the selected claude-tui runner terminate at its binary lookup,
	// before it can create or start a PTY.
	t.Setenv("PATH", "")

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-14T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  claude-sonnet-fixture:
    family: claude-sonnet
    status: active
    power: 8
    surfaces:
      claude-code: sonnet-fixture
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	cancelRefresh()
	public, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		QuotaRefreshContext: refreshCtx,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := public.(*service)
	// claude and claude-tui share the claude binary. Exposing only that binary
	// yields exactly those two routable subscription candidates.
	forceAvailableHarnessesForTest(t, svc, "claude", "claude-tui")

	dispatched := make(chan string, 1)
	svc.subprocessDispatchObserver = func(runner harnesses.Harness) {
		dispatched <- runner.Info().Name
	}

	events, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:      "exercise the default Claude route",
		Policy:      "default",
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	drained := drainUnifiedServiceEvents(t, events, 5*time.Second)

	var decision *ServiceRoutingDecisionData
	for _, event := range drained {
		if event.Type != ServiceEventTypeRoutingDecision {
			continue
		}
		payload := decodeRoutingDecisionEvent(t, event)
		decision = &payload
		break
	}
	if decision == nil {
		t.Fatalf("missing routing_decision event: %#v", drained)
	}
	if decision.RequestedHarness != "" {
		t.Fatalf("requested harness = %q, want unpinned", decision.RequestedHarness)
	}
	if decision.RequestedPolicy != "default" {
		t.Fatalf("requested policy = %q, want default", decision.RequestedPolicy)
	}
	if decision.Permissions != "unrestricted" {
		t.Fatalf("permissions = %q, want unrestricted", decision.Permissions)
	}
	if decision.Harness != "claude-tui" {
		t.Fatalf("routing_decision harness = %q, want claude-tui", decision.Harness)
	}
	seen := map[string]bool{}
	for _, candidate := range decision.Candidates {
		if !candidate.Eligible {
			continue
		}
		seen[candidate.Harness] = true
	}
	if !seen["claude"] || !seen["claude-tui"] || len(seen) != 2 {
		t.Fatalf("eligible candidate harnesses = %v, want exactly claude and claude-tui; trace=%#v", seen, decision.Candidates)
	}

	select {
	case got := <-dispatched:
		if got != "claude-tui" {
			t.Fatalf("dispatched runner = %q, want claude-tui", got)
		}
	default:
		t.Fatal("subprocess dispatch did not select a concrete runner")
	}
}
