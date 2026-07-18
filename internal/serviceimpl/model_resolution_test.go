package serviceimpl

import (
	"errors"
	"slices"
	"testing"

	"github.com/easel/fizeau/internal/routing"
)

func TestResolveModelConstraintNormalizesAndReportsAmbiguity(t *testing.T) {
	t.Run("normalizes to the concrete model", func(t *testing.T) {
		inputs := modelResolutionInputs("Qwen-3.6-27b-MLX-8bit")
		for _, requested := range []string{"qwen36", "qwen/qwen3.6", "QWEN3.6"} {
			result, err := ResolveModelConstraint(ModelConstraintRequest{Model: requested}, inputs, nil)
			if err != nil {
				t.Fatalf("ResolveModelConstraint(%q): %v", requested, err)
			}
			if result.Model != "Qwen-3.6-27b-MLX-8bit" {
				t.Fatalf("ResolveModelConstraint(%q) model = %q, want concrete model", requested, result.Model)
			}
			if result.Candidates != nil {
				t.Fatalf("ResolveModelConstraint(%q) candidates = %v, want nil on success", requested, result.Candidates)
			}
		}
	})

	t.Run("reports ordered ambiguity and full evidence", func(t *testing.T) {
		inputs := modelResolutionInputs(
			"OtherModel",
			"Qwen3.6-35B-A3B-4bit",
			"Qwen3.6-35B-A3B-nvfp4",
		)
		result, err := ResolveModelConstraint(ModelConstraintRequest{Model: " qwen3.6 "}, inputs, nil)
		if err == nil {
			t.Fatal("ResolveModelConstraint returned nil error, want ambiguity")
		}
		var constraintErr *ModelConstraintError
		if !errors.As(err, &constraintErr) {
			t.Fatalf("error = %T %v, want *ModelConstraintError", err, err)
		}
		if constraintErr.Kind != ModelConstraintErrorAmbiguous {
			t.Fatalf("error kind = %q, want %q", constraintErr.Kind, ModelConstraintErrorAmbiguous)
		}
		if constraintErr.Model != "qwen3.6" {
			t.Fatalf("error model = %q, want trimmed request", constraintErr.Model)
		}
		wantMatches := []string{"Qwen3.6-35B-A3B-4bit", "Qwen3.6-35B-A3B-nvfp4"}
		if !slices.Equal(constraintErr.Candidates, wantMatches) {
			t.Fatalf("error candidates = %v, want matched candidates %v", constraintErr.Candidates, wantMatches)
		}
		wantEvidence := []string{"OtherModel", "Qwen3.6-35B-A3B-4bit", "Qwen3.6-35B-A3B-nvfp4"}
		if !slices.Equal(result.Candidates, wantEvidence) {
			t.Fatalf("result candidates = %v, want full evidence %v", result.Candidates, wantEvidence)
		}
	})
}

func TestResolveModelConstraintPreservesConcretePrecedenceAndPinScope(t *testing.T) {
	t.Run("concrete match wins before defaults", func(t *testing.T) {
		inputs := routing.Inputs{Harnesses: []routing.HarnessEntry{{
			Name:         "fiz",
			DefaultModel: "Qwen3.6-default",
			Providers: []routing.ProviderEntry{{
				Name:          "live",
				DefaultModel:  "Qwen3.6-provider-default",
				DiscoveredIDs: []string{"Qwen3.6-concrete"},
			}},
		}}}
		result, err := ResolveModelConstraint(ModelConstraintRequest{Model: "qwen3.6"}, inputs, nil)
		if err != nil {
			t.Fatalf("ResolveModelConstraint: %v", err)
		}
		if result.Model != "Qwen3.6-concrete" {
			t.Fatalf("model = %q, want concrete candidate", result.Model)
		}
	})

	t.Run("local alias and endpoint-qualified provider select one provider", func(t *testing.T) {
		inputs := routing.Inputs{Harnesses: []routing.HarnessEntry{
			{
				Name: "fiz",
				Providers: []routing.ProviderEntry{
					{Name: "first", DiscoveredIDs: []string{"Qwen3.6-first"}},
					{Name: "second", DiscoveredIDs: []string{"Qwen3.6-second"}},
				},
			},
			{Name: "claude", Providers: []routing.ProviderEntry{{Name: "second", DiscoveredIDs: []string{"Qwen3.6-wrong-harness"}}}},
		}}
		result, err := ResolveModelConstraint(ModelConstraintRequest{
			Harness:  "local",
			Provider: "second@blue",
			Model:    "qwen3.6",
		}, inputs, nil)
		if err != nil {
			t.Fatalf("ResolveModelConstraint: %v", err)
		}
		if result.Model != "Qwen3.6-second" {
			t.Fatalf("model = %q, want pinned provider model", result.Model)
		}
	})

	t.Run("provider pin excludes the harness default", func(t *testing.T) {
		inputs := routing.Inputs{Harnesses: []routing.HarnessEntry{{
			Name:         "fiz",
			DefaultModel: "Qwen3.6-harness-default",
			Providers: []routing.ProviderEntry{{
				Name:         "live",
				DefaultModel: "Qwen3.6-provider-default",
			}},
		}}}
		result, err := ResolveModelConstraint(ModelConstraintRequest{Provider: "live", Model: "qwen3.6"}, inputs, nil)
		if err != nil {
			t.Fatalf("ResolveModelConstraint: %v", err)
		}
		if result.Model != "Qwen3.6-provider-default" {
			t.Fatalf("model = %q, want provider default", result.Model)
		}
	})

	t.Run("exact duplicate candidates do not create ambiguity", func(t *testing.T) {
		inputs := routing.Inputs{Harnesses: []routing.HarnessEntry{{
			Name: "fiz",
			Providers: []routing.ProviderEntry{
				{Name: "first", DiscoveredIDs: []string{"Qwen3.6-shared"}},
				{Name: "second", DiscoveredIDs: []string{"Qwen3.6-shared"}},
			},
		}}}
		result, err := ResolveModelConstraint(ModelConstraintRequest{Model: "qwen3.6"}, inputs, nil)
		if err != nil {
			t.Fatalf("ResolveModelConstraint: %v", err)
		}
		if result.Model != "Qwen3.6-shared" {
			t.Fatalf("model = %q, want de-duplicated candidate", result.Model)
		}
	})
}

func TestResolveModelConstraintPrefersExactProviderQualifiedIdentity(t *testing.T) {
	inputs := modelResolutionInputs(
		"openai/gpt-5.6-sol",
		"openai/gpt-5.6-terra",
		"openai/gpt-5.6-terra-pro",
		"openai/gpt-5.6-luna",
	)

	for _, want := range []string{
		"openai/gpt-5.6-sol",
		"openai/gpt-5.6-terra",
		"openai/gpt-5.6-terra-pro",
		"openai/gpt-5.6-luna",
	} {
		t.Run(want, func(t *testing.T) {
			result, err := ResolveModelConstraint(ModelConstraintRequest{Model: want}, inputs, nil)
			if err != nil {
				t.Fatalf("ResolveModelConstraint(%q): %v", want, err)
			}
			if result.Model != want {
				t.Fatalf("model = %q, want exact provider-qualified ID %q", result.Model, want)
			}
		})
	}

	t.Run("bare alias remains deterministically ambiguous", func(t *testing.T) {
		_, err := ResolveModelConstraint(ModelConstraintRequest{Model: "gpt-5.6-terra"}, inputs, nil)
		var constraintErr *ModelConstraintError
		if !errors.As(err, &constraintErr) {
			t.Fatalf("error = %T %v, want *ModelConstraintError", err, err)
		}
		if constraintErr.Kind != ModelConstraintErrorAmbiguous {
			t.Fatalf("error kind = %q, want %q", constraintErr.Kind, ModelConstraintErrorAmbiguous)
		}
		wantCandidates := []string{"openai/gpt-5.6-terra", "openai/gpt-5.6-terra-pro"}
		if !slices.Equal(constraintErr.Candidates, wantCandidates) {
			t.Fatalf("candidates = %v, want %v", constraintErr.Candidates, wantCandidates)
		}
	})
}

func modelResolutionInputs(models ...string) routing.Inputs {
	return routing.Inputs{Harnesses: []routing.HarnessEntry{{
		Name: "fiz",
		Providers: []routing.ProviderEntry{{
			Name:          "live",
			DiscoveredIDs: models,
		}},
	}}}
}
