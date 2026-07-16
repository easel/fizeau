package routing

import (
	"math"
	"testing"
)

func TestMinContextWindowIncludesPositiveOutputBudget(t *testing.T) {
	req := Request{EstimatedPromptTokens: 100, MaxTokens: 50}
	if got := req.MinContextWindow(); got != 175 {
		t.Fatalf("MinContextWindow()=%d, want 175", got)
	}
}

func TestMinContextWindowZeroMaxTokensPreservesPromptGate(t *testing.T) {
	req := Request{EstimatedPromptTokens: 100}
	if got := req.MinContextWindow(); got != 125 {
		t.Fatalf("MinContextWindow()=%d, want 125", got)
	}
}

func TestMinContextWindowOutputOnlyAndSaturates(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want int
	}{
		{name: "output only", req: Request{MaxTokens: 37}, want: 37},
		{name: "negative prompt ignored", req: Request{EstimatedPromptTokens: -50, MaxTokens: 37}, want: 37},
		{name: "negative output ignored internally", req: Request{EstimatedPromptTokens: 100, MaxTokens: -1}, want: 125},
		{name: "prompt safety saturates", req: Request{EstimatedPromptTokens: math.MaxInt}, want: math.MaxInt},
		{name: "output addition saturates", req: Request{EstimatedPromptTokens: math.MaxInt / 2, MaxTokens: math.MaxInt}, want: math.MaxInt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.req.MinContextWindow(); got != test.want {
				t.Fatalf("MinContextWindow()=%d, want %d", got, test.want)
			}
		})
	}
}

func TestResolveRejectsPromptPlusOutputBudget(t *testing.T) {
	t.Run("one token short", func(t *testing.T) {
		decision, err := Resolve(Request{
			Harness: "fiz", Provider: "local", Model: "budget-model",
			EstimatedPromptTokens: 100, MaxTokens: 26,
		}, contextBudgetInputs(150, true))
		candidate := requireSingleContextBudgetCandidate(t, decision)
		if err == nil {
			t.Fatal("Resolve succeeded, want context rejection")
		}
		if candidate.Eligible || candidate.FilterReason != FilterReasonContextTooSmall {
			t.Fatalf("candidate=%#v, want typed context_too_small rejection", candidate)
		}
		if candidate.Reason != "context window 150 < required 151" {
			t.Fatalf("Reason=%q, want exact required-context evidence", candidate.Reason)
		}
	})

	t.Run("equality passes", func(t *testing.T) {
		decision, err := Resolve(Request{
			Harness: "fiz", Provider: "local", Model: "budget-model",
			EstimatedPromptTokens: 100, MaxTokens: 26,
		}, contextBudgetInputs(151, true))
		if err != nil {
			t.Fatalf("Resolve equality: %v", err)
		}
		candidate := requireSingleContextBudgetCandidate(t, decision)
		if !candidate.Eligible {
			t.Fatalf("candidate=%#v, want equality eligible", candidate)
		}
	})
}

func TestResolveUnknownContextRejectsPositiveBudget(t *testing.T) {
	decision, err := Resolve(Request{
		Harness: "fiz", Provider: "local", Model: "budget-model", MaxTokens: 1,
	}, contextBudgetInputs(0, false))
	candidate := requireSingleContextBudgetCandidate(t, decision)
	if err == nil {
		t.Fatal("Resolve succeeded, want unknown-context rejection")
	}
	if candidate.Model != "budget-model" || candidate.ContextLength != 0 || candidate.ContextSource != ContextSourceUnknown {
		t.Fatalf("candidate context=%q %d/%q, want named model with raw unknown evidence", candidate.Model, candidate.ContextLength, candidate.ContextSource)
	}
	if candidate.Eligible || candidate.FilterReason != FilterReasonContextTooSmall {
		t.Fatalf("candidate=%#v, want typed context_too_small rejection", candidate)
	}
	if candidate.Reason != "context window unknown < required 1" {
		t.Fatalf("Reason=%q, want required-context evidence", candidate.Reason)
	}
}

func TestResolveExplicitPinStillAppliesContextBudget(t *testing.T) {
	decision, err := Resolve(Request{
		Harness: "fiz", Provider: "local", Model: "budget-model", MaxTokens: 101,
	}, contextBudgetInputs(100, true))
	candidate := requireSingleContextBudgetCandidate(t, decision)
	if err == nil {
		t.Fatal("Resolve explicit pin succeeded, want context rejection")
	}
	if candidate.Eligible || candidate.FilterReason != FilterReasonContextTooSmall {
		t.Fatalf("candidate=%#v, want explicit pin rejected by context gate", candidate)
	}
}

func TestResolveContextHeadroomUsesRequiredContext(t *testing.T) {
	req := Request{
		Harness: "fiz", Provider: "local", Model: "budget-model",
		EstimatedPromptTokens: 400, MaxTokens: 200,
	}
	decision, err := Resolve(req, contextBudgetInputs(1000, true))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	candidate := requireSingleContextBudgetCandidate(t, decision)
	if required := req.MinContextWindow(); required != 700 {
		t.Fatalf("MinContextWindow()=%d, want 700", required)
	}
	if candidate.ContextHeadroom != 300 {
		t.Fatalf("ContextHeadroom=%d, want context 1000 - required 700", candidate.ContextHeadroom)
	}
}

func contextBudgetInputs(contextWindow int, includeEvidence bool) Inputs {
	provider := ProviderEntry{
		Name:               "local",
		DefaultModel:       "budget-model",
		DiscoveredIDs:      []string{"budget-model"},
		DiscoveryAttempted: true,
		SupportsTools:      true,
	}
	if includeEvidence {
		provider.ContextWindows = map[string]int{"budget-model": contextWindow}
		provider.ContextWindowSources = map[string]string{"budget-model": ContextSourceProviderAPI}
	}
	return Inputs{Harnesses: []HarnessEntry{{
		Name: "fiz", Surface: "embedded-openai", CostClass: "local",
		IsLocal: true, AutoRoutingEligible: true, ExactPinSupport: true,
		Available: true, QuotaOK: true, SubscriptionOK: true, SupportsTools: true,
		Providers: []ProviderEntry{provider},
	}}}
}

func requireSingleContextBudgetCandidate(t *testing.T, decision *Decision) Candidate {
	t.Helper()
	if decision == nil || len(decision.Candidates) != 1 {
		t.Fatalf("decision=%#v, want one candidate", decision)
	}
	candidate := decision.Candidates[0]
	if candidate.Model != "budget-model" || candidate.Provider != "local" {
		t.Fatalf("candidate=%#v, want local/budget-model", candidate)
	}
	return candidate
}
