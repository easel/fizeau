package routing

import (
	"testing"
	"time"
)

func TestExactRouteCooldownMatchesOnlyExecutingCandidate(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	targetHarness := cooldownTestHarness("target", []ProviderEntry{
		// The provider suffix intentionally differs from the explicit endpoint.
		// Matching must split the qualified provider but retain EndpointName.
		{Name: "local@qualified", EndpointName: "primary", ServerInstance: "desk-a", DefaultModel: "model-a", SupportsTools: true},
		{Name: "local@qualified", EndpointName: "secondary", ServerInstance: "desk-a", DefaultModel: "model-a", SupportsTools: true},
		{Name: "local@qualified", EndpointName: "primary", ServerInstance: "desk-b", DefaultModel: "model-a", SupportsTools: true},
		{Name: "local@qualified", EndpointName: "primary", ServerInstance: "desk-a", DefaultModel: "model-b", SupportsTools: true},
		{Name: "other@qualified", EndpointName: "primary", ServerInstance: "desk-a", DefaultModel: "model-a", SupportsTools: true},
	})
	siblingHarness := cooldownTestHarness("sibling", []ProviderEntry{
		{Name: "local@qualified", EndpointName: "primary", ServerInstance: "desk-a", DefaultModel: "model-a", SupportsTools: true},
	})
	baselineInputs := Inputs{
		Harnesses:         []HarnessEntry{targetHarness, siblingHarness},
		SurfacePreference: map[string]string{},
		Now:               now,
	}
	baseline, err := Resolve(Request{}, baselineInputs)
	if err != nil {
		t.Fatalf("baseline Resolve: %v", err)
	}

	cooledInputs := baselineInputs
	cooledInputs.ExactRouteCooldowns = map[RouteCooldownKey]time.Time{
		{
			Harness:        "target",
			Provider:       "local",
			Endpoint:       "primary",
			ServerInstance: "desk-a",
			Model:          "model-a",
		}: now.Add(-5 * time.Second),
	}
	cooledInputs.CooldownDuration = 30 * time.Second
	decision, err := Resolve(Request{}, cooledInputs)
	if err != nil {
		t.Fatalf("Resolve with exact cooldown: %v", err)
	}

	target := cooldownCandidateTuple{"target", "local@qualified", "primary", "desk-a", "model-a"}
	if got, want := cooldownCandidateScore(t, decision, target), cooldownCandidateScore(t, baseline, target)-50; got != want {
		t.Fatalf("target score=%v, want one soft cooldown demotion to %v", got, want)
	}
	siblings := []cooldownCandidateTuple{
		{"target", "local@qualified", "secondary", "desk-a", "model-a"},
		{"target", "local@qualified", "primary", "desk-b", "model-a"},
		{"target", "local@qualified", "primary", "desk-a", "model-b"},
		{"target", "other@qualified", "primary", "desk-a", "model-a"},
		{"sibling", "local@qualified", "primary", "desk-a", "model-a"},
	}
	for _, sibling := range siblings {
		if got, want := cooldownCandidateScore(t, decision, sibling), cooldownCandidateScore(t, baseline, sibling); got != want {
			t.Errorf("sibling %#v score=%v, want unchanged %v", sibling, got, want)
		}
	}

	t.Run("overlapping evidence applies one penalty", func(t *testing.T) {
		in := Inputs{
			Harnesses:         []HarnessEntry{cooldownTestHarness("target", []ProviderEntry{{Name: "local@qualified", EndpointName: "primary", ServerInstance: "desk-a", DefaultModel: "model-a"}})},
			SurfacePreference: map[string]string{},
			Now:               now,
		}
		base, resolveErr := Resolve(Request{}, in)
		if resolveErr != nil {
			t.Fatalf("baseline Resolve: %v", resolveErr)
		}
		in.ExactRouteCooldowns = map[RouteCooldownKey]time.Time{
			{Harness: "target", Provider: "local", Endpoint: "primary", ServerInstance: "desk-a", Model: "model-a"}: now.Add(-time.Second),
			{Harness: "target", Provider: "local", Endpoint: "primary", ServerInstance: "desk-a"}:                   now.Add(-2 * time.Second),
		}
		in.CooldownDuration = 30 * time.Second
		cooled, resolveErr := Resolve(Request{}, in)
		if resolveErr != nil {
			t.Fatalf("Resolve with overlapping cooldown evidence: %v", resolveErr)
		}
		if got, want := cooled.Candidates[0].Score, base.Candidates[0].Score-50; got != want {
			t.Fatalf("score=%v, want exactly one cooldown penalty %v", got, want)
		}
	})

	t.Run("TTL boundary is inactive", func(t *testing.T) {
		in := baselineInputs
		in.ExactRouteCooldowns = map[RouteCooldownKey]time.Time{
			{Harness: "target", Provider: "local", Endpoint: "primary", ServerInstance: "desk-a", Model: "model-a"}: now.Add(-30 * time.Second),
		}
		in.CooldownDuration = 30 * time.Second
		atBoundary, resolveErr := Resolve(Request{}, in)
		if resolveErr != nil {
			t.Fatalf("Resolve at TTL boundary: %v", resolveErr)
		}
		if got, want := cooldownCandidateScore(t, atBoundary, target), cooldownCandidateScore(t, baseline, target); got != want {
			t.Fatalf("target score at TTL boundary=%v, want unchanged %v", got, want)
		}
	})

	t.Run("empty identity is ignored", func(t *testing.T) {
		in := baselineInputs
		in.ExactRouteCooldowns = map[RouteCooldownKey]time.Time{
			{}: now.Add(-time.Second),
		}
		in.CooldownDuration = 30 * time.Second
		withEmptyKey, resolveErr := Resolve(Request{}, in)
		if resolveErr != nil {
			t.Fatalf("Resolve with empty cooldown key: %v", resolveErr)
		}
		for _, candidate := range baseline.Candidates {
			got := cooldownCandidateScore(t, withEmptyKey, cooldownCandidateTuple{
				candidate.Harness,
				candidate.Provider,
				candidate.Endpoint,
				candidate.ServerInstance,
				candidate.Model,
			})
			if got != candidate.Score {
				t.Errorf("candidate %s/%s/%s score=%v, want unchanged %v", candidate.Harness, candidate.Provider, candidate.Model, got, candidate.Score)
			}
		}
	})
}

func TestSiblingHarnessSurfaceIsNotPoisoned(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	baselineInputs := claudeSurfacePairInputs()
	baselineInputs.Now = now
	baseline, err := Resolve(Request{}, baselineInputs)
	if err != nil {
		t.Fatalf("baseline Resolve: %v", err)
	}

	for _, tc := range []struct {
		name    string
		failed  string
		sibling string
	}{
		{name: "claude failure leaves claude-tui healthy", failed: "claude", sibling: "claude-tui"},
		{name: "claude-tui failure leaves claude healthy", failed: "claude-tui", sibling: "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := claudeSurfacePairInputs()
			in.Now = now
			in.ExactRouteCooldowns = map[RouteCooldownKey]time.Time{
				{Harness: tc.failed, Model: "claude-sonnet-4-6"}: now.Add(-5 * time.Second),
			}
			in.CooldownDuration = 30 * time.Second
			decision, resolveErr := Resolve(Request{}, in)
			if resolveErr != nil {
				t.Fatalf("Resolve: %v", resolveErr)
			}
			if got, want := cooldownHarnessScore(t, decision, tc.failed), cooldownHarnessScore(t, baseline, tc.failed)-50; got != want {
				t.Fatalf("failed harness %q score=%v, want %v", tc.failed, got, want)
			}
			if got, want := cooldownHarnessScore(t, decision, tc.sibling), cooldownHarnessScore(t, baseline, tc.sibling); got != want {
				t.Fatalf("sibling harness %q score=%v, want unchanged %v", tc.sibling, got, want)
			}
			if decision.Harness != tc.sibling {
				t.Fatalf("selected harness=%q, want healthy sibling %q", decision.Harness, tc.sibling)
			}
		})
	}
}

func cooldownTestHarness(name string, providers []ProviderEntry) HarnessEntry {
	return HarnessEntry{
		Name:                name,
		Surface:             name,
		CostClass:           "local",
		IsLocal:             true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		SupportsTools:       true,
		Providers:           providers,
	}
}

type cooldownCandidateTuple struct {
	harness        string
	provider       string
	endpoint       string
	serverInstance string
	model          string
}

func cooldownCandidateScore(t *testing.T, decision *Decision, want cooldownCandidateTuple) float64 {
	t.Helper()
	for _, candidate := range decision.Candidates {
		if candidate.Harness == want.harness &&
			candidate.Provider == want.provider &&
			candidate.Endpoint == want.endpoint &&
			candidate.ServerInstance == want.serverInstance &&
			candidate.Model == want.model {
			return candidate.Score
		}
	}
	t.Fatalf("candidate %#v not found: %#v", want, decision.Candidates)
	return 0
}

func cooldownHarnessScore(t *testing.T, decision *Decision, harness string) float64 {
	t.Helper()
	for _, candidate := range decision.Candidates {
		if candidate.Harness == harness {
			return candidate.Score
		}
	}
	t.Fatalf("candidate for harness %q not found: %#v", harness, decision.Candidates)
	return 0
}
