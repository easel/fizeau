package routing

import (
	"testing"
	"time"
)

// TestRouter_FiltersUnreachableEndpointsWhenAlternatesExist asserts that when
// bragi is recorded as probe-unreachable and claude/sonnet is a valid
// candidate, the router selects claude without routing to bragi (AC #3).
func TestRouter_FiltersUnreachableEndpointsWhenAlternatesExist(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	in := Inputs{
		Now: now,
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:          "bragi",
						BaseURL:       "http://bragi:1234",
						DefaultModel:  "qwen3.6",
						DiscoveredIDs: []string{"qwen3.6"},
						SupportsTools: true,
					},
				},
			},
			{
				Name:                "claude",
				Surface:             "claude",
				CostClass:           "medium",
				IsSubscription:      true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				DefaultModel:        "claude-sonnet-4-6",
			},
		},
		ProbeUnreachable: map[string]time.Time{
			"bragi": now.Add(-5 * time.Minute),
		},
	}

	req := Request{Policy: "default"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness == "fiz" && dec.Provider == "bragi" {
		t.Fatal("router selected bragi despite probe-unreachable gate; expected fallback to claude")
	}
	if dec.Harness != "claude" {
		t.Errorf("expected claude harness to be selected, got harness=%q provider=%q", dec.Harness, dec.Provider)
	}

	// Verify bragi was recorded with the endpoint_unreachable filter reason.
	found := false
	for _, c := range dec.Candidates {
		if c.Provider != "bragi" {
			continue
		}
		found = true
		if c.Eligible {
			t.Error("bragi should be ineligible (probe-unreachable)")
		}
		if c.FilterReason != FilterReasonEndpointUnreachable {
			t.Errorf("bragi FilterReason = %q, want %q", c.FilterReason, FilterReasonEndpointUnreachable)
		}
	}
	if !found {
		t.Fatal("bragi not found in decision candidates")
	}
}

func TestRouter_ProbeUnreachableTakesPrecedenceOverSnapshotDialFailure(t *testing.T) {
	in := newTestRoutingEngine()
	in.CooldownDuration = 30 * time.Second
	in.ProbeUnreachable = map[string]time.Time{
		"vidar-omlx": in.Now.Add(-5 * time.Second),
	}
	in.ProviderUnreachable = map[string]time.Time{
		"vidar-omlx": in.Now.Add(-5 * time.Second),
	}

	dec, err := Resolve(Request{Policy: "cheap", Harness: "fiz", Model: "qwen/qwen3.6"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for i := range dec.Candidates {
		candidate := &dec.Candidates[i]
		if candidate.Provider != "vidar-omlx" {
			continue
		}
		if candidate.Eligible {
			t.Fatal("vidar-omlx should be ineligible when both unreachable signals exist")
		}
		if candidate.FilterReason != FilterReasonEndpointUnreachable {
			t.Fatalf("FilterReason = %q, want %q", candidate.FilterReason, FilterReasonEndpointUnreachable)
		}
		return
	}
	t.Fatal("vidar-omlx candidate row missing from decision")
}

// TestRouter_RejectsUnreachableEndpointWhenSoleAutomaticCandidate asserts that
// a known-dead endpoint is not re-admitted just because no alternative exists.
func TestRouter_RejectsUnreachableEndpointWhenSoleAutomaticCandidate(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	in := Inputs{
		Now: now,
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:          "bragi",
						BaseURL:       "http://bragi:1234",
						DefaultModel:  "qwen3.6",
						DiscoveredIDs: []string{"qwen3.6"},
						SupportsTools: true,
					},
				},
			},
		},
		ProbeUnreachable: map[string]time.Time{
			"bragi": now.Add(-5 * time.Minute),
		},
	}

	_, err := Resolve(Request{Policy: "default"}, in)
	if err == nil {
		t.Fatal("Resolve succeeded for sole probe-unreachable automatic candidate")
	}
}

// TestRouter_AcceptsUnreachableEndpointWhenExplicitlyPinned asserts that the
// operator can still force a known-dead provider intentionally.
func TestRouter_AcceptsUnreachableEndpointWhenExplicitlyPinned(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	in := Inputs{
		Now: now,
		Harnesses: []HarnessEntry{
			{
				Name:                "fiz",
				Surface:             "embedded-openai",
				CostClass:           "local",
				IsLocal:             true,
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				SupportsTools:       true,
				Providers: []ProviderEntry{
					{
						Name:          "bragi",
						BaseURL:       "http://bragi:1234",
						DefaultModel:  "qwen3.6",
						DiscoveredIDs: []string{"qwen3.6"},
						SupportsTools: true,
					},
				},
			},
		},
		ProbeUnreachable: map[string]time.Time{
			"bragi": now.Add(-5 * time.Minute),
		},
	}

	req := Request{Policy: "default", Provider: "bragi"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve with explicit pin to probe-unreachable provider: %v", err)
	}
	if dec == nil || dec.Provider != "bragi" {
		t.Fatalf("expected bragi to be selected via explicit pin, got %+v", dec)
	}
}
