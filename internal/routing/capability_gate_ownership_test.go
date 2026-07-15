package routing

import (
	"errors"
	"strings"
	"testing"
)

func TestCapabilityGatesUseTypedReasonsAndZeroValueNoOp(t *testing.T) {
	t.Run("estimated tokens derive context requirement", func(t *testing.T) {
		req := Request{EstimatedPromptTokens: 100_000}
		if got := req.MinContextWindow(); got != 125_000 {
			t.Fatalf("MinContextWindow() = %d, want 125000", got)
		}
		reason, filter := CheckGating(Capabilities{ContextWindow: 4096}, req)
		if filter != FilterReasonContextTooSmall || reason != "context window 4096 < required 125000" {
			t.Fatalf("context gate = %q/%q, want stable typed rejection", reason, filter)
		}
	})

	t.Run("tool requirement uses typed reason", func(t *testing.T) {
		reason, filter := CheckGating(Capabilities{ContextWindow: 200_000}, Request{RequiresTools: true})
		if filter != FilterReasonNoToolSupport || reason != "tool calling not supported" {
			t.Fatalf("tool gate = %q/%q, want stable typed rejection", reason, filter)
		}
	})

	t.Run("zero value imposes no context or tool gate", func(t *testing.T) {
		reason, filter := CheckGating(Capabilities{}, Request{})
		if reason != "" || filter != FilterReasonEligible {
			t.Fatalf("zero-value gate = %q/%q, want eligible no-op", reason, filter)
		}
	})
}

func TestResolveTierDefaultBeforeCapabilityGates(t *testing.T) {
	offOnly := HarnessEntry{
		Name:                "off-only",
		Surface:             "test-surface",
		AutoRoutingEligible: true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		ExactPinSupport:     true,
		DefaultModel:        "off-model",
		SupportsTools:       true,
	}
	resolver := func(policy, surface string) (string, bool) {
		if surface != "test-surface" {
			return "", false
		}
		switch policy {
		case "cheap":
			return "off", true
		case "smart":
			return "high", true
		default:
			return "", false
		}
	}

	t.Run("off default passes", func(t *testing.T) {
		decision, err := Resolve(Request{Policy: "cheap", Reasoning: "auto"}, Inputs{
			Harnesses: []HarnessEntry{offOnly}, ReasoningResolver: resolver,
		})
		if err != nil || decision.Harness != "off-only" || decision.Model != "off-model" {
			t.Fatalf("decision = %#v err=%v, want off-only/off-model", decision, err)
		}
	})

	t.Run("named default rejects off-only candidate", func(t *testing.T) {
		decision, err := Resolve(Request{Policy: "smart", Reasoning: "auto"}, Inputs{
			Harnesses: []HarnessEntry{offOnly}, ReasoningResolver: resolver,
		})
		if err == nil || decision == nil || len(decision.Candidates) != 1 {
			t.Fatalf("decision = %#v err=%v, want one rejected candidate", decision, err)
		}
		candidate := decision.Candidates[0]
		if candidate.Eligible || candidate.FilterReason != FilterReasonReasoningUnsupported ||
			candidate.Reason != `reasoning "high" not supported` {
			t.Fatalf("candidate = %#v, want tier default applied before reasoning gate", candidate)
		}
	})

	t.Run("unset reasoning does not resolve default", func(t *testing.T) {
		decision, err := Resolve(Request{Policy: "smart"}, Inputs{
			Harnesses: []HarnessEntry{offOnly}, ReasoningResolver: resolver,
		})
		if err != nil || decision.Harness != "off-only" {
			t.Fatalf("decision = %#v err=%v, want unset reasoning no-op", decision, err)
		}
	})
}

func TestProviderCredentialMissingGateUsesNeutralEvidence(t *testing.T) {
	decision, err := Resolve(Request{Provider: "openrouter"}, Inputs{
		Harnesses: []HarnessEntry{{
			Name: "fiz", AutoRoutingEligible: true, Available: true, QuotaOK: true,
			SubscriptionOK: true, ExactPinSupport: true, SupportsTools: true,
			Providers: []ProviderEntry{{Name: "openrouter", DefaultModel: "model", SupportsTools: true}},
		}},
		ProviderCredentialMissing: map[string]string{
			"openrouter": "providers.openrouter.api_key",
		},
	})
	if err == nil || decision == nil || len(decision.Candidates) != 1 {
		t.Fatalf("decision = %#v err=%v, want rejected credential-gated candidate", decision, err)
	}
	candidate := decision.Candidates[0]
	if candidate.Eligible || candidate.FilterReason != FilterReasonCredentialMissing {
		t.Fatalf("candidate = %#v, want credential_missing", candidate)
	}
	if candidate.Reason != "provider openrouter credential missing (providers.openrouter.api_key)" ||
		!strings.Contains(candidate.Reason, "credential missing") {
		t.Fatalf("Reason = %q, want stable neutral evidence", candidate.Reason)
	}
}

func TestUnknownPolicyReturnsTypedError(t *testing.T) {
	decision, err := Resolve(Request{Policy: "missing-policy"}, Inputs{})
	if err == nil || decision == nil || len(decision.Candidates) != 0 {
		t.Fatalf("decision = %#v err=%v, want empty decision trace plus typed unknown-policy failure", decision, err)
	}
	if !errors.Is(err, ErrUnknownPolicy{}) {
		t.Fatalf("errors.Is should match ErrUnknownPolicy: %T %v", err, err)
	}
	var typed *ErrUnknownPolicy
	if !errors.As(err, &typed) || typed.Policy != "missing-policy" {
		t.Fatalf("typed error = %#v, want policy missing-policy", typed)
	}
}
