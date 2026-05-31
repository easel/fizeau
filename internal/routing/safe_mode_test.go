package routing

import (
	"testing"
	"time"
)

// safeModeBaseInputs returns a mixed inventory: one local harness and one
// subscription harness, both healthy, with quota signals and local discovery
// present. Used to confirm that safe mode produces identical simple-policy
// behavior regardless of quota/local state.
func safeModeBaseInputs() Inputs {
	return Inputs{
		Harnesses: []HarnessEntry{
			{
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
				Providers: []ProviderEntry{
					{
						Name:               "vidar-omlx",
						DefaultModel:       "qwen3-local",
						DiscoveredIDs:      []string{"qwen3-local"},
						DiscoveryAttempted: true,
						SupportsTools:      true,
					},
				},
			},
			{
				Name:                "claude",
				Surface:             "claude",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				ExactPinSupport:     true,
				Available:           true,
				QuotaOK:             true,
				QuotaPercentUsed:    20,
				QuotaTrend:          QuotaTrendHealthy,
				SubscriptionOK:      true,
				DefaultModel:        "claude-sonnet-4-6",
				SupportedModels:     []string{"claude-sonnet-4-6", "claude-opus-4-8"},
				SupportsTools:       true,
				Providers: []ProviderEntry{{
					CostSource: CostSourceSubscription,
				}},
			},
		},
		ModelEligibility: policyPowerLookup(map[string]ModelEligibility{
			"qwen3-local":       {Power: 6, AutoRoutable: true},
			"claude-sonnet-4-6": {Power: 8, AutoRoutable: true},
			"claude-opus-4-8":   {Power: 10, AutoRoutable: true},
		}),
		Now: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

// safeModeWithExhaustedQuota returns inputs where the subscription harness
// reports SubscriptionOK=false (quota exhausted). Safe mode must still route
// through subscription when the harness recovers — this fixture tests the
// local-exclusion invariant, not the quota gate.
func safeModeWithHealthyQuota() Inputs {
	in := safeModeBaseInputs()
	in.Harnesses[1].QuotaPercentUsed = 80
	in.Harnesses[1].QuotaTrend = QuotaTrendBurning
	return in
}

// safeModeWithHighLocalScore returns inputs where the local harness has
// excellent performance signals that would normally outscore subscription.
func safeModeWithHighLocalScore() Inputs {
	in := safeModeBaseInputs()
	in.ProviderSuccessRate = map[string]float64{
		ProviderModelKey("vidar-omlx", "", "qwen3-local"): 1.0,
	}
	in.ObservedSpeedTPS = map[string]float64{
		ProviderModelKey("vidar-omlx", "", "qwen3-local"): 500,
	}
	return in
}

func TestSafeModeSimplePolicy(t *testing.T) {
	cases := []struct {
		name            string
		req             Request
		inputs          Inputs
		wantHarness     string
		wantModel       string
		localMustBeGone bool
	}{
		{
			name:            "safe_mode forces default policy regardless of caller request",
			req:             Request{Policy: "smart"},
			inputs:          safeModeBaseInputs(),
			wantHarness:     "claude",
			wantModel:       "claude-sonnet-4-6",
			localMustBeGone: true,
		},
		{
			name:            "safe_mode excludes local even when local outscores subscription",
			req:             Request{},
			inputs:          safeModeWithHighLocalScore(),
			wantHarness:     "claude",
			wantModel:       "claude-sonnet-4-6",
			localMustBeGone: true,
		},
		{
			name:            "safe_mode still routes subscription when quota is burning but SubscriptionOK",
			req:             Request{},
			inputs:          safeModeWithHealthyQuota(),
			wantHarness:     "claude",
			localMustBeGone: true,
		},
		{
			name:            "safe_mode with no policy specified defaults to default",
			req:             Request{},
			inputs:          safeModeBaseInputs(),
			wantHarness:     "claude",
			wantModel:       "claude-sonnet-4-6",
			localMustBeGone: true,
		},
		{
			name:            "safe_mode overrides cheap policy to default",
			req:             Request{Policy: "cheap"},
			inputs:          safeModeBaseInputs(),
			wantHarness:     "claude",
			localMustBeGone: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.inputs.SafeMode = true
			dec, err := Resolve(tc.req, tc.inputs)
			if err != nil {
				t.Fatalf("safe_mode=%q: Resolve error: %v", tc.name, err)
			}
			if tc.wantHarness != "" && dec.Harness != tc.wantHarness {
				t.Fatalf("safe_mode=%q: harness=%q, want %q; candidates=%s",
					tc.name, dec.Harness, tc.wantHarness, policyCandidateSummary(dec.Candidates))
			}
			if tc.wantModel != "" && dec.Model != tc.wantModel {
				t.Fatalf("safe_mode=%q: model=%q, want %q; candidates=%s",
					tc.name, dec.Model, tc.wantModel, policyCandidateSummary(dec.Candidates))
			}
			if tc.localMustBeGone {
				for _, c := range dec.Candidates {
					if c.Harness == "fiz" && c.Eligible {
						t.Fatalf("safe_mode=%q: local candidate fiz/%s is eligible, want excluded; candidates=%s",
							tc.name, c.Model, policyCandidateSummary(dec.Candidates))
					}
					if c.Harness == "fiz" && c.FilterReason != FilterReasonPolicyFiltered {
						t.Fatalf("safe_mode=%q: local candidate fiz/%s has filter_reason=%q, want %q; reason=%q",
							tc.name, c.Model, c.FilterReason, FilterReasonPolicyFiltered, c.Reason)
					}
				}
			}
		})
	}
}

// TestSafeModeOffDoesNotAffectRouting ensures that SafeMode=false (the zero
// value) leaves normal routing behavior intact: local candidates remain
// eligible and a non-default policy is honoured.
func TestSafeModeOffDoesNotAffectRouting(t *testing.T) {
	in := safeModeBaseInputs()
	// SafeMode defaults to false — do not set it.

	dec, err := Resolve(Request{Policy: "cheap"}, in)
	if err != nil {
		t.Fatalf("safe_mode=false: Resolve error: %v", err)
	}
	// With SafeMode off, local should win on cheap (lower cost class).
	if dec.Harness != "fiz" {
		t.Fatalf("safe_mode=false: harness=%q, want fiz (local should win on cheap policy); candidates=%s",
			dec.Harness, policyCandidateSummary(dec.Candidates))
	}
}

// TestSafeModeDecisionPolicyFieldIsDefault asserts that the Reason string on
// the selected candidate reflects the "default" policy even when the caller
// requested a different policy.
func TestSafeModeDecisionPolicyFieldIsDefault(t *testing.T) {
	in := safeModeBaseInputs()
	in.SafeMode = true

	dec, err := Resolve(Request{Policy: "smart"}, in)
	if err != nil {
		t.Fatalf("safe_mode policy field: Resolve error: %v", err)
	}
	want := "policy=default"
	if dec.Reason == "" {
		t.Fatalf("safe_mode policy field: empty Reason on decision")
	}
	if len(dec.Reason) < len(want) || dec.Reason[:len(want)] != want {
		t.Fatalf("safe_mode policy field: Reason=%q, want prefix %q", dec.Reason, want)
	}
}
