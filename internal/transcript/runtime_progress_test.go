package transcript

import (
	"strings"
	"testing"
)

func TestRouteProgressDataIncludesEconomicsWhenPresent(t *testing.T) {
	payload := RouteProgressData(RouteProgressDecision{
		Harness:  "fiz",
		Provider: "alpha",
		Model:    "model-a",
		Power:    7,
		Candidates: []RouteProgressCandidate{{
			Harness:            "fiz",
			Provider:           "alpha",
			Model:              "model-a",
			CostUSDPer1kTokens: 0.012,
			CostSource:         "catalog",
			Components: RouteProgressComponents{
				Power:     7,
				SpeedTPS:  55,
				CostClass: "local",
			},
		}},
	})
	if payload.Phase != "route" || payload.State != "start" {
		t.Fatalf("payload=%#v, want route start", payload)
	}
	if payload.Message == "" {
		t.Fatal("route progress message is empty")
	}
	for _, want := range []string{"fiz/alpha/model-a", "power=", "speed=", "cost=", "cost_source="} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("route progress message %q missing %q", payload.Message, want)
		}
	}
	if len(payload.Message) > DefaultLineLimit {
		t.Fatalf("route progress message too long: %d", len(payload.Message))
	}
	if payload.SessionSummary != payload.Message {
		t.Fatalf("session summary=%q, want same as message %q", payload.SessionSummary, payload.Message)
	}
}
