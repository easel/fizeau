package serviceimpl

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
)

type residualOverflowProvider struct {
	calls int
}

func (p *residualOverflowProvider) Chat(context.Context, []agentcore.Message, []agentcore.ToolDef, agentcore.Options) (agentcore.Response, error) {
	p.calls++
	return agentcore.Response{}, errors.New("HTTP 400: context length exceeded for selected model")
}

func TestNativeResidualContextOverflowProjectsCapabilityAndSingleRoute(t *testing.T) {
	alpha := &residualOverflowProvider{}
	beta := &residualOverflowProvider{}
	resolveCalls := map[string]int{}
	compactionCalls := 0
	var final harnesses.FinalData

	RunNative(context.Background(), NativeRequest{
		Prompt:                "small accepted prompt",
		Permissions:           "unrestricted",
		NoStream:              true,
		SelectedContextWindow: 4096,
		Decision: NativeDecision{
			Harness:               "fiz",
			Provider:              "alpha@west",
			ServerInstance:        "server-alpha",
			Model:                 "shared-model",
			SelectedContextWindow: 4096,
			SelectedContextSource: "fixture",
			Candidates: []NativeRouteCandidate{
				{Provider: "alpha@west", Endpoint: "west", ServerInstance: "server-alpha", Model: "shared-model", Eligible: true},
				{Provider: "beta@east", Endpoint: "east", ServerInstance: "server-beta", Model: "shared-model", Eligible: true},
			},
		},
		Started: time.Now(),
	}, NativeCallbacks{
		ResolveProvider: func(req NativeProviderRequest) NativeProviderResolution {
			resolveCalls[req.Provider]++
			switch req.Provider {
			case "alpha@west":
				return NativeProviderResolution{Provider: alpha, Name: req.Provider, Model: req.Model}
			case "beta@east":
				return NativeProviderResolution{Provider: beta, Name: req.Provider, Model: req.Model}
			default:
				return NativeProviderResolution{}
			}
		},
		Compactor: func(string) agentcore.Compactor {
			return func(_ context.Context, input agentcore.CompactionInput, _ agentcore.Provider) ([]agentcore.Message, *agentcore.CompactionResult, error) {
				compactionCalls++
				if compactionCalls == 1 {
					return input.History, nil, nil
				}
				return input.History[:0], &agentcore.CompactionResult{
					Summary: "reduced", TokensBefore: 100, TokensAfter: 20,
				}, nil
			}
		},
		Finalize: func(got harnesses.FinalData, _ TerminalOrigin) {
			final = got
		},
	})

	if final.Status != "failed" || final.RoutingActual == nil {
		t.Fatalf("final = %+v, want failed route evidence", final)
	}
	if final.RoutingActual.FailureClass != "capability" {
		t.Fatalf("failure class = %q, want capability", final.RoutingActual.FailureClass)
	}
	if final.RoutingActual.Provider != "alpha@west" || final.RoutingActual.Model != "shared-model" || final.RoutingActual.ServerInstance != "server-alpha" {
		t.Fatalf("routing actual = %+v, want selected alpha tuple", final.RoutingActual)
	}
	if len(final.RoutingActual.FallbackChainFired) != 2 || final.RoutingActual.FallbackChainFired[0] != "alpha@west" || final.RoutingActual.FallbackChainFired[1] != "alpha@west" {
		t.Fatalf("attempted providers = %v, want [alpha@west alpha@west]", final.RoutingActual.FallbackChainFired)
	}
	if alpha.calls != 2 || beta.calls != 0 {
		t.Fatalf("provider calls alpha/beta = %d/%d, want 2/0", alpha.calls, beta.calls)
	}
	if compactionCalls != 2 {
		t.Fatalf("compaction calls = %d, want pre-turn no-op plus one effective overflow pass", compactionCalls)
	}
	if resolveCalls["beta"] != 0 || resolveCalls["beta@east"] != 0 {
		t.Fatalf("unselected beta was resolved: %v", resolveCalls)
	}
}
