package routing

import (
	"errors"
	"testing"

	"github.com/easel/fizeau/internal/modelcatalog"
)

func TestPolicyRequireNoRemoteFiltersRemoteCandidates(t *testing.T) {
	in := policyFilterInputs()

	dec, err := Resolve(Request{Policy: "air-gapped", Require: []string{"no_remote"}}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "local" {
		t.Fatalf("Provider=%q, want local", dec.Provider)
	}
	remote, ok := candidateByProvider(dec.Candidates, "openrouter")
	if !ok {
		t.Fatal("openrouter candidate not found")
	}
	if remote.Eligible || remote.FilterReason != FilterReasonPolicyFiltered {
		t.Fatalf("openrouter candidate=%#v, want policy-filtered", remote)
	}
}

func TestPolicyRequireBlocksRemotePinConflict(t *testing.T) {
	in := policyFilterInputs()

	_, err := Resolve(Request{Policy: "air-gapped", Require: []string{"no_remote"}, Provider: "openrouter"}, in)
	if err == nil {
		t.Fatal("expected no_remote policy to reject openrouter pin")
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "air-gapped" || typed.Requirement != "no_remote" || typed.AttemptedPin != "openrouter" {
		t.Fatalf("policy requirement error=%#v, want air-gapped/no_remote/openrouter", typed)
	}
}

func TestPolicyRequireNoRemotePrefersTheUserConstraintWhenLocalCandidatesAreDown(t *testing.T) {
	in := Inputs{Harnesses: []HarnessEntry{
		{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			IsLocal:             true,
			AutoRoutingEligible: true,
			Available:           false,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{{
				Name:          "local",
				CostClass:     "local",
				DefaultModel:  "local-good",
				SupportsTools: true,
			}},
		},
		{
			Name:                "openrouter",
			Surface:             "embedded-openai",
			CostClass:           "medium",
			AutoRoutingEligible: true,
			Available:           true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{{
				Name:          "remote",
				CostClass:     "medium",
				DefaultModel:  "remote-good",
				SupportsTools: true,
			}},
		},
	}}

	_, err := Resolve(Request{Policy: "air-gapped", Require: []string{"no_remote"}}, in)
	if err == nil {
		t.Fatal("expected no_remote policy to fail when only local candidates are down")
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "air-gapped" || typed.Requirement != "no_remote" {
		t.Fatalf("policy requirement error=%#v, want air-gapped/no_remote", typed)
	}
}

func TestAllowLocalFalseExcludesLocalCandidates(t *testing.T) {
	in := Inputs{Harnesses: []HarnessEntry{
		{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			IsLocal:             true,
			AutoRoutingEligible: true,
			Available:           true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{{
				Name:          "local",
				CostClass:     "local",
				DefaultModel:  "local-good",
				SupportsTools: true,
			}},
		},
		{
			Name:                "codex",
			Surface:             "codex",
			CostClass:           "medium",
			IsSubscription:      true,
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			DefaultModel:        "frontier",
			SupportsTools:       true,
		},
	}}

	dec, err := Resolve(Request{Policy: "smart", AllowLocal: false}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "codex" {
		t.Fatalf("Harness=%q, want codex", dec.Harness)
	}
	local, ok := candidateByProvider(dec.Candidates, "local")
	if !ok {
		t.Fatal("local candidate not found")
	}
	if local.Eligible || local.FilterReason != FilterReasonPolicyFiltered {
		t.Fatalf("local candidate=%#v, want policy-filtered", local)
	}
}

func TestMeteredOptInRequirementSurfacesWhenOnlyOptOutCandidatesRemain(t *testing.T) {
	in := Inputs{Harnesses: []HarnessEntry{
		{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "medium",
			AutoRoutingEligible: true,
			Available:           true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{{
				Name:                      "payg",
				Billing:                   modelcatalog.BillingModelPerToken,
				ActualCashSpend:           true,
				DefaultModel:              "remote-good",
				ExcludeFromDefaultRouting: true,
				SupportsTools:             true,
			}},
		},
	}}

	_, err := Resolve(Request{Policy: "default"}, in)
	if err == nil {
		t.Fatal("expected metered opt-in policy to fail when only opt-out candidates remain")
	}
	var typed *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "default" || typed.Requirement != "metered opt-in" {
		t.Fatalf("policy requirement error=%#v, want default/metered opt-in", typed)
	}
}

func policyFilterInputs() Inputs {
	return Inputs{Harnesses: []HarnessEntry{{
		Name:                "fiz",
		Surface:             "embedded-openai",
		CostClass:           "local",
		IsLocal:             true,
		AutoRoutingEligible: true,
		Available:           true,
		ExactPinSupport:     true,
		SupportsTools:       true,
		Providers: []ProviderEntry{
			{Name: "local", CostClass: "local", DefaultModel: "local-good", SupportsTools: true},
			{Name: "openrouter", CostClass: "medium", DefaultModel: "remote-good", SupportsTools: true},
		},
	}}}
}

func candidateByProvider(candidates []Candidate, provider string) (Candidate, bool) {
	for _, candidate := range candidates {
		if candidate.Provider == provider {
			return candidate, true
		}
	}
	return Candidate{}, false
}

func TestUnpinnedEscalatesWhenBandEmpty(t *testing.T) {
	in := Inputs{Harnesses: []HarnessEntry{
		{
			Name:                "codex",
			Surface:             "codex",
			CostClass:           "medium",
			IsSubscription:      true,
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			DefaultModel:        "frontier",
			SupportsTools:       true,
		},
	}}

	dec, err := Resolve(Request{Policy: "default"}, in)
	if err != nil {
		t.Fatalf("Resolve with escalation: %v", err)
	}
	if dec.Harness != "codex" {
		t.Fatalf("Harness=%q, want codex", dec.Harness)
	}
	if dec.Model != "frontier" {
		t.Fatalf("Model=%q, want frontier", dec.Model)
	}
}

func TestHardPinDoesNotEscalate(t *testing.T) {
	in := Inputs{Harnesses: []HarnessEntry{
		{
			Name:                "codex",
			Surface:             "codex",
			CostClass:           "medium",
			IsSubscription:      true,
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			DefaultModel:        "frontier",
			SupportsTools:       true,
		},
	}}

	_, err := Resolve(Request{Policy: "default", Provider: "nonexistent-provider"}, in)
	if err == nil {
		t.Fatalf("expected hard-pinned request with nonexistent provider to fail, got success")
	}
	var typed *NoViableCandidateError
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As NoViableCandidateError: got %T %v", err, err)
	}
	if typed.Provider != "nonexistent-provider" {
		t.Fatalf("NoViableCandidateError.Provider=%q, want nonexistent-provider", typed.Provider)
	}
}
