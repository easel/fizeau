package serviceimpl

import (
	"encoding/json"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestStampSubprocessFinalRoutingMergesDecisionIdentity(t *testing.T) {
	final := harnesses.FinalData{
		Status: "failed",
		RoutingActual: &harnesses.RoutingActual{
			Harness:            "conflicting-adapter-harness",
			Provider:           "conflicting-adapter-provider",
			ServerInstance:     "conflicting-adapter-server",
			Model:              "conflicting-adapter-model",
			FailureClass:       "credential_invalid",
			FallbackChainFired: []string{"adapter-evidence"},
			Power:              17,
		},
	}
	raw, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	event := stampSubprocessFinalRouting(harnesses.Event{Type: harnesses.EventTypeFinal, Data: raw}, ExecuteRunnerDecision{
		Harness:        "claude",
		Provider:       "anthropic",
		ServerInstance: "anthropic-primary",
		Model:          "claude-sonnet-4-6",
	})

	var got harnesses.FinalData
	if err := json.Unmarshal(event.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.RoutingActual == nil {
		t.Fatal("routing actual is nil")
	}
	actual := got.RoutingActual
	if actual.Harness != "claude" || actual.Provider != "anthropic" ||
		actual.ServerInstance != "anthropic-primary" || actual.Model != "claude-sonnet-4-6" {
		t.Errorf("resolved identity = %+v, want authoritative service decision", actual)
	}
	if actual.FailureClass != "credential_invalid" {
		t.Errorf("failure class = %q, want adapter evidence preserved", actual.FailureClass)
	}
	if len(actual.FallbackChainFired) != 1 || actual.FallbackChainFired[0] != "adapter-evidence" || actual.Power != 17 {
		t.Errorf("unowned adapter evidence changed: %+v", actual)
	}
}
