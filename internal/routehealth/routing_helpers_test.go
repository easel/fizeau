package routehealth

import (
	"errors"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/routing"
)

func TestEffectivePowerPolicyAppliesCatalogBounds(t *testing.T) {
	policy := EffectivePowerPolicy(PowerRequest{
		Policy:   "default",
		MinPower: 5,
		MaxPower: 10,
	}, func(name string) (PolicySpec, bool) {
		if name != "default" {
			return PolicySpec{}, false
		}
		return PolicySpec{Name: "default", MinPower: 7, MaxPower: 8}, true
	})

	if policy.PolicyName != "default" || policy.MinPower != 7 || policy.MaxPower != 8 {
		t.Fatalf("policy=%#v, want default 7..8", policy)
	}
}

func TestPowerBoundsForRequestKeepsExplicitModelPins(t *testing.T) {
	minPower, maxPower := PowerBoundsForRequest(PowerRequest{
		Model:    "pinned-model",
		MinPower: 2,
		MaxPower: 11,
	}, PowerPolicy{
		PolicyName: "default",
		MinPower:   7,
		MaxPower:   8,
	})

	if minPower != 2 || maxPower != 11 {
		t.Fatalf("bounds=%d..%d, want explicit 2..11", minPower, maxPower)
	}
}

func TestApplyAttemptCooldownsPromotesDispatchabilityFailures(t *testing.T) {
	recordedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	in := &routing.Inputs{
		Harnesses: []routing.HarnessEntry{{Name: "codex"}},
	}

	ApplyAttemptCooldowns(in, []Record{
		{
			Key: Key{
				Provider: "bragi",
			},
			Error:      `dial tcp 192.168.0.10:1234: connection refused`,
			RecordedAt: recordedAt,
		},
		{
			Key: Key{
				Harness: "codex",
			},
			RecordedAt: recordedAt,
		},
	}, 45*time.Second)

	if got := in.ProviderCooldowns["bragi"]; !got.Equal(recordedAt) {
		t.Fatalf("ProviderCooldowns[bragi]=%v, want %v", got, recordedAt)
	}
	if got := in.ProviderUnreachable["bragi"]; !got.Equal(recordedAt) {
		t.Fatalf("ProviderUnreachable[bragi]=%v, want %v", got, recordedAt)
	}
	if in.CooldownDuration != 45*time.Second {
		t.Fatalf("CooldownDuration=%v, want 45s", in.CooldownDuration)
	}
	if in.Harnesses[0].InCooldown {
		t.Fatal("harness-wide cooldown was applied, want exact route cooldown")
	}
	key := routing.RouteCooldownKey{Harness: "codex"}
	if got := in.ExactRouteCooldowns[key]; !got.Equal(recordedAt) {
		t.Fatalf("ExactRouteCooldowns[%#v]=%v, want %v", key, got, recordedAt)
	}
}

func TestApplyAttemptCooldownsPreservesExactRouteTuple(t *testing.T) {
	newer := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Second)
	in := &routing.Inputs{
		Harnesses: []routing.HarnessEntry{{Name: "fiz"}},
	}
	record := Record{
		Key: Key{
			Harness:        "fiz",
			Provider:       "local",
			Endpoint:       "primary",
			ServerInstance: "desk-a",
			Model:          "model-a",
		},
		Error: `dial tcp 192.0.2.1:8000: connection refused`,
	}
	olderRecord := record
	olderRecord.RecordedAt = older
	newerRecord := record
	newerRecord.RecordedAt = newer

	ApplyAttemptCooldowns(in, []Record{newerRecord, olderRecord}, 45*time.Second)

	key := routing.RouteCooldownKey{
		Harness:        "fiz",
		Provider:       "local",
		Endpoint:       "primary",
		ServerInstance: "desk-a",
		Model:          "model-a",
	}
	if len(in.ExactRouteCooldowns) != 1 {
		t.Fatalf("ExactRouteCooldowns=%#v, want one exact route", in.ExactRouteCooldowns)
	}
	if got := in.ExactRouteCooldowns[key]; !got.Equal(newer) {
		t.Fatalf("ExactRouteCooldowns[%#v]=%v, want newest %v", key, got, newer)
	}
	if len(in.ProviderCooldowns) != 0 {
		t.Fatalf("ProviderCooldowns=%#v, want no provider-wide state", in.ProviderCooldowns)
	}
	if len(in.ProviderUnreachable) != 0 {
		t.Fatalf("ProviderUnreachable=%#v, want no provider-wide dispatchability state", in.ProviderUnreachable)
	}
	if in.Harnesses[0].InCooldown {
		t.Fatal("fiz harness was poisoned by an exact route failure")
	}
}

func TestLegacyProviderOnlyCooldownRemainsCompatible(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	in := &routing.Inputs{
		Harnesses: []routing.HarnessEntry{{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			IsLocal:             true,
			AutoRoutingEligible: true,
			ExactPinSupport:     true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			SupportsTools:       true,
			Providers: []routing.ProviderEntry{
				{Name: "failed", DefaultModel: "model-a", SupportsTools: true},
				{Name: "sibling", DefaultModel: "model-a", SupportsTools: true},
			},
		}},
		Now: now,
	}
	baseline, err := routing.Resolve(routing.Request{Harness: "fiz"}, *in)
	if err != nil {
		t.Fatalf("baseline Resolve: %v", err)
	}

	failedAt := now.Add(-5 * time.Second)
	ApplyAttemptCooldowns(in, []Record{{
		Key:        Key{Provider: "failed"},
		RecordedAt: failedAt,
	}}, 30*time.Second)

	if got := in.ProviderCooldowns["failed"]; !got.Equal(failedAt) {
		t.Fatalf("ProviderCooldowns[failed]=%v, want %v", got, failedAt)
	}
	if len(in.ExactRouteCooldowns) != 0 {
		t.Fatalf("ExactRouteCooldowns=%#v, legacy provider-only record must stay provider-wide", in.ExactRouteCooldowns)
	}
	decision, err := routing.Resolve(routing.Request{Harness: "fiz"}, *in)
	if err != nil {
		t.Fatalf("Resolve with provider cooldown: %v", err)
	}
	if got, want := routeCandidateScore(t, decision, "failed"), routeCandidateScore(t, baseline, "failed")-50; got != want {
		t.Fatalf("failed score=%v, want one soft cooldown demotion to %v", got, want)
	}
	if got, want := routeCandidateScore(t, decision, "sibling"), routeCandidateScore(t, baseline, "sibling"); got != want {
		t.Fatalf("sibling score=%v, want unchanged %v", got, want)
	}
}

func routeCandidateScore(t *testing.T, decision *routing.Decision, provider string) float64 {
	t.Helper()
	for _, candidate := range decision.Candidates {
		if candidate.Provider == provider {
			return candidate.Score
		}
	}
	t.Fatalf("candidate for provider %q not found: %#v", provider, decision.Candidates)
	return 0
}

func TestCandidateCooldownMatchesEndpointProviderRefs(t *testing.T) {
	recordedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	cooldown := CandidateCooldown([]Record{
		{
			Key: Key{
				Provider: "bragi@primary",
				Model:    "qwen",
			},
			Reason:     "timeout",
			Error:      "context deadline exceeded",
			RecordedAt: recordedAt,
		},
	}, "bragi", "primary", "qwen", 30*time.Second)

	if cooldown == nil {
		t.Fatal("expected cooldown for bragi@primary")
	}
	if cooldown.Reason != "timeout" {
		t.Fatalf("Reason=%q, want timeout", cooldown.Reason)
	}
	if !cooldown.LastAttempt.Equal(recordedAt) {
		t.Fatalf("LastAttempt=%v, want %v", cooldown.LastAttempt, recordedAt)
	}
}

func TestProviderCooldownsFromSnapshotErrorsUsesLongestProviderPrefix(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	got := ProviderCooldownsFromSnapshotErrors([]SnapshotSource{
		{
			Name:            "rg-bragi-club-3090-props",
			Error:           `dial tcp 10.0.0.8:1234: i/o timeout`,
			LastRefreshedAt: now.Add(-5 * time.Second),
		},
		{
			Name:            "rg-bragi-metadata",
			Error:           "authentication failed",
			LastRefreshedAt: now.Add(-5 * time.Second),
		},
	}, []string{"rg-bragi", "rg-bragi-club-3090"}, now, 30*time.Second)

	if len(got) != 1 {
		t.Fatalf("cooldowns=%v, want one match", got)
	}
	if _, ok := got["rg-bragi-club-3090"]; !ok {
		t.Fatalf("cooldowns=%v, want longest configured provider name", got)
	}
}

func TestShouldEscalateOnErrorRejectsHardPinErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "harness model incompatible",
			err:  &routing.ErrHarnessModelIncompatible{Harness: "codex", Model: "gpt-5.5"},
			want: false,
		},
		{
			name: "unsatisfiable pin",
			err:  &routing.ErrUnsatisfiablePin{Pin: "provider=bragi", Reason: "unknown provider"},
			want: false,
		},
		{
			name: "policy filtered pin",
			err:  &routing.ErrPolicyRequirementUnsatisfied{Policy: "air-gapped", Requirement: "no_remote"},
			want: false,
		},
		{
			name: "no viable candidate",
			err:  &routing.NoViableCandidateError{Rejected: 2},
			want: true,
		},
		{
			name: "wrapped no viable candidate",
			err:  errors.New("wrapper: " + (&routing.NoViableCandidateError{Rejected: 1}).Error()),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldEscalateOnError(tc.err); got != tc.want {
				t.Fatalf("ShouldEscalateOnError(%T)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
