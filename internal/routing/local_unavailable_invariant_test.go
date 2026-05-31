package routing

import (
	"testing"
	"time"
)

// TestLocalNeverGatesSubscriptionRouting is the top reliability invariant from
// FEAT-004 Addendum: a local/unavailable/stale provider NEVER gates the
// candidate set and NEVER blocks subscription routing. Provider-down is a clean
// skip, not a routing failure.
//
// Each sub-case puts all local/HTTP providers into a different failure mode and
// asserts that Resolve returns a subscription candidate (claude or codex).
func TestLocalNeverGatesSubscriptionRouting(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	claudeHarness := HarnessEntry{
		Name:                "claude",
		Surface:             "claude",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		SupportsTools:       true,
		DefaultModel:        "claude-sonnet-4-6",
		SupportedModels:     []string{"claude-sonnet-4-6"},
	}
	codexHarness := HarnessEntry{
		Name:                "codex",
		Surface:             "codex",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		SupportsTools:       true,
		DefaultModel:        "gpt-5.4",
		SupportedModels:     []string{"gpt-5.4"},
	}

	// localFizDown is a fiz harness with one local provider. Available=false
	// marks the entire harness as unreachable.
	localFizDown := HarnessEntry{
		Name:                "fiz",
		CostClass:           "local",
		IsLocal:             true,
		AutoRoutingEligible: true,
		Available:           false,
		SupportsTools:       true,
		Providers: []ProviderEntry{{
			Name:          "qwen",
			DefaultModel:  "qwen3.6",
			DiscoveredIDs: []string{"qwen3.6"},
		}},
	}
	// localFizUp has the harness available so individual provider gates can be
	// tested in isolation (ProbeUnreachable, ProviderUnreachable, etc.).
	localFizUp := HarnessEntry{
		Name:                "fiz",
		CostClass:           "local",
		IsLocal:             true,
		AutoRoutingEligible: true,
		Available:           true,
		SupportsTools:       true,
		Providers: []ProviderEntry{{
			Name:          "qwen",
			DefaultModel:  "qwen3.6",
			DiscoveredIDs: []string{"qwen3.6"},
		}},
	}

	cases := []struct {
		name   string
		inputs Inputs
	}{
		{
			name: "local harness Available=false",
			inputs: Inputs{
				Now:       now,
				Harnesses: []HarnessEntry{localFizDown, claudeHarness, codexHarness},
			},
		},
		{
			name: "local provider in ProbeUnreachable",
			inputs: Inputs{
				Now:       now,
				Harnesses: []HarnessEntry{localFizUp, claudeHarness, codexHarness},
				ProbeUnreachable: map[string]time.Time{
					"qwen": now.Add(-5 * time.Minute),
				},
			},
		},
		{
			name: "local provider in ProviderUnreachable within cooldown",
			inputs: Inputs{
				Now:              now,
				CooldownDuration: 10 * time.Minute,
				Harnesses:        []HarnessEntry{localFizUp, claudeHarness, codexHarness},
				ProviderUnreachable: map[string]time.Time{
					"qwen": now.Add(-2 * time.Minute),
				},
			},
		},
		{
			name: "local provider credential missing",
			inputs: Inputs{
				Now:       now,
				Harnesses: []HarnessEntry{localFizUp, claudeHarness, codexHarness},
				ProviderCredentialMissing: map[string]string{
					"qwen": "QWEN_API_KEY",
				},
			},
		},
		{
			name: "multiple local providers all unreachable via different failure modes",
			inputs: Inputs{
				Now:              now,
				CooldownDuration: 10 * time.Minute,
				Harnesses: []HarnessEntry{
					{
						Name:                "fiz",
						CostClass:           "local",
						IsLocal:             true,
						AutoRoutingEligible: true,
						Available:           true,
						SupportsTools:       true,
						Providers: []ProviderEntry{
							{
								Name:          "qwen",
								DefaultModel:  "qwen3.6",
								DiscoveredIDs: []string{"qwen3.6"},
							},
							{
								Name:          "http-local",
								DefaultModel:  "some-model",
								DiscoveredIDs: []string{"some-model"},
							},
						},
					},
					claudeHarness,
					codexHarness,
				},
				ProbeUnreachable: map[string]time.Time{
					"qwen": now.Add(-3 * time.Minute),
				},
				ProviderUnreachable: map[string]time.Time{
					"http-local": now.Add(-2 * time.Minute),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := Resolve(Request{Policy: "default"}, tc.inputs)
			if err != nil {
				t.Fatalf("INVARIANT VIOLATED: local down must not block subscription routing; got error=%v", err)
			}
			if dec == nil || dec.Harness == "" {
				t.Fatal("INVARIANT VIOLATED: got empty decision; expected subscription candidate")
			}
			if dec.Harness != "claude" && dec.Harness != "codex" {
				t.Fatalf("INVARIANT VIOLATED: selected harness=%q; expected subscription harness (claude or codex)", dec.Harness)
			}
		})
	}
}

// TestStaleOrMissingSnapshotDoesNotBlockSubscriptionRouting verifies that
// stale, unknown, or absent local provider facts never block routing when a
// subscription harness is dispatchable.
//
// ProbeUnknown is a scoring demotion (not a hard gate) per FEAT-004: its -100
// penalty outweighs the local locality bonus so healthy subscription candidates
// win without the router failing.
func TestStaleOrMissingSnapshotDoesNotBlockSubscriptionRouting(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	claudeHarness := HarnessEntry{
		Name:                "claude",
		Surface:             "claude",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		ExactPinSupport:     true,
		Available:           true,
		QuotaOK:             true,
		SubscriptionOK:      true,
		SupportsTools:       true,
		DefaultModel:        "claude-sonnet-4-6",
		SupportedModels:     []string{"claude-sonnet-4-6"},
	}

	cases := []struct {
		name            string
		inputs          Inputs
		wantHarness     string // if empty, just assert no error
		wantNotNoViable bool
	}{
		{
			// ProbeUnknown is a scoring demotion (-100) so subscription wins.
			name: "local provider probe status unknown (ProbeUnknown)",
			inputs: Inputs{
				Now: now,
				Harnesses: []HarnessEntry{
					{
						Name:                "fiz",
						CostClass:           "local",
						IsLocal:             true,
						AutoRoutingEligible: true,
						Available:           true,
						SupportsTools:       true,
						Providers: []ProviderEntry{{
							Name:          "qwen",
							DefaultModel:  "qwen3.6",
							DiscoveredIDs: []string{"qwen3.6"},
						}},
					},
					claudeHarness,
				},
				ProbeUnknown: map[string]time.Time{
					"qwen": now.Add(-30 * time.Minute),
				},
			},
			wantHarness: "claude",
		},
		{
			// Discovery attempted but returned no models and no default is set.
			// The local harness produces an unresolved candidate that is rejected
			// at the power-eligibility gate (model="" with a non-nil lookup).
			// Routing must not fail: subscription wins.
			name: "local provider discovery attempted but empty",
			inputs: Inputs{
				Now: now,
				Harnesses: []HarnessEntry{
					{
						Name:                "fiz",
						CostClass:           "local",
						IsLocal:             true,
						AutoRoutingEligible: true,
						Available:           true,
						SupportsTools:       true,
						Providers: []ProviderEntry{{
							Name:               "qwen",
							DefaultModel:       "",   // no default
							DiscoveredIDs:      nil,  // discovery ran but found nothing
							DiscoveryAttempted: true, // confirms discovery ran
						}},
					},
					claudeHarness,
				},
				// A non-nil lookup causes the empty-model local candidate to be
				// rejected (CheckPowerEligibility returns FilterReasonPowerMissing
				// when model=="" and lookup!=nil), leaving subscription to win.
				ModelEligibility: func(model string) (ModelEligibility, bool) {
					if model == "claude-sonnet-4-6" {
						return ModelEligibility{Power: 8, AutoRoutable: true}, true
					}
					return ModelEligibility{}, false
				},
			},
			wantHarness: "claude",
		},
		{
			// No local harness at all (subscription-only inventory).
			name: "no local harness in inventory",
			inputs: Inputs{
				Now:       now,
				Harnesses: []HarnessEntry{claudeHarness},
			},
			wantHarness: "claude",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := Resolve(Request{Policy: "default"}, tc.inputs)
			if err != nil {
				t.Fatalf("stale/missing local snapshot must not block subscription routing: %v", err)
			}
			if tc.wantHarness != "" && dec.Harness != tc.wantHarness {
				t.Fatalf("expected harness=%q, got harness=%q provider=%q model=%q; candidates=%s",
					tc.wantHarness, dec.Harness, dec.Provider, dec.Model,
					policyCandidateSummary(dec.Candidates))
			}
		})
	}
}
