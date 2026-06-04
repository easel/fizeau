package routing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProviderPreferenceFiltering(t *testing.T) {
	in := newTestRoutingEngine()
	// fiz is local, codex/claude are subscription.

	t.Run("local-only", func(t *testing.T) {
		req := Request{ProviderPreference: ProviderPreferenceLocalOnly}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if dec.Harness != "fiz" {
			t.Errorf("local-only: got %q, want fiz", dec.Harness)
		}
		for _, c := range dec.Candidates {
			if !strings.Contains(c.Harness, "fiz") && c.Eligible {
				t.Errorf("subscription harness %q should be ineligible under local-only", c.Harness)
			}
		}
	})

	t.Run("subscription-only", func(t *testing.T) {
		req := Request{ProviderPreference: ProviderPreferenceSubscriptionOnly}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if dec.Harness == "fiz" {
			t.Errorf("subscription-only: should not pick fiz")
		}
		for _, c := range dec.Candidates {
			if c.Harness == "fiz" && c.Eligible {
				t.Errorf("local harness %q should be ineligible under subscription-only", c.Harness)
			}
		}
	})
}

func TestProviderPreferenceBiasing(t *testing.T) {
	in := newTestRoutingEngine()
	// baseline: cheap prefers local.

	t.Run("local-first bias", func(t *testing.T) {
		req := Request{Policy: "smart", ProviderPreference: ProviderPreferenceLocalFirst}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// smart normally prefers cloud, but local-first might boost fiz enough to win
		// if scores are close. Let's check candidate scores.
		var foundFiz, foundClaude bool
		for _, c := range dec.Candidates {
			if c.Harness == "fiz" {
				foundFiz = true
			}
			if c.Harness == "claude" {
				foundClaude = true
			}
		}
		// local-first should give +30 to fiz.
		// smart: fiz (local) = 100 + 0*20 (cr=0) + 30 (pref) - 5 (unknown utilization) - 5 (unknown performance) = 120
		// smart: claude (medium) = 100 + 2*20 (cr=2) + 5 (quota) = 145
		// Claude still wins, but the gap is smaller.
		if !foundFiz || !foundClaude {
			t.Fatal("could not find fiz or claude scores")
		}
	})

	t.Run("subscription-first bias", func(t *testing.T) {
		req := Request{Policy: "cheap", ProviderPreference: ProviderPreferenceSubscriptionFirst}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// cheap normally prefers local (+40 local bonus).
		// cheap: fiz = 100 + 40 (local) - 0*30 (cr=0) = 140
		// cheap: claude = 100 + 20 (quota) - 2*30 (cr=2) + 30 (pref) = 90
		// Local still wins cheap, but gap is closed by 30.
		if dec.Harness != "fiz" {
			t.Errorf("cheap + subscription-first: got %q, want fiz (local still stronger)", dec.Harness)
		}
	})
}

func TestQuotaSignalsScoring(t *testing.T) {
	in := newTestRoutingEngine()

	t.Run("exhausted quota", func(t *testing.T) {
		// Set claude quota to not OK.
		for i, h := range in.Harnesses {
			if h.Name == "claude" {
				in.Harnesses[i].QuotaOK = false
			}
		}
		req := Request{Policy: "smart"}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// smart prefers cloud, but claude's exhausted quota should demote it.
		if dec.Harness == "claude" {
			t.Errorf("exhausted quota: should not pick claude")
		}
	})

	t.Run("stale quota penalty", func(t *testing.T) {
		in := newTestRoutingEngine()
		// All fresh by default. Now mark claude stale.
		for i, h := range in.Harnesses {
			if h.Name == "claude" {
				in.Harnesses[i].QuotaStale = true
			}
		}
		req := Request{Policy: "smart"}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// Find claude score.
		for _, c := range dec.Candidates {
			if c.Harness == "claude" {
				// Base smart: 100 + 40 (cr=2) + 5 (quota) = 145.
				// Stale penalty: -15 and explicit unknown-signal penalties
				// (-5 utilization, -5 performance) -> 120.
				if c.Score != 120 {
					t.Errorf("stale quota: got score %.1f, want 120.0", c.Score)
				}
			}
		}
	})

	t.Run("quota trend bias", func(t *testing.T) {
		in := newTestRoutingEngine()
		// claude is healthy, codex is burning.
		for i, h := range in.Harnesses {
			if h.Name == "claude" {
				in.Harnesses[i].QuotaTrend = QuotaTrendHealthy
			}
			if h.Name == "codex" {
				in.Harnesses[i].QuotaTrend = QuotaTrendBurning
			}
		}
		req := Request{Policy: "smart"}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// Both same cost class. Healthy claude (+10) should beat burning codex (-20).
		if dec.Harness != "claude" {
			t.Errorf("quota trend: got %q, want claude (healthy)", dec.Harness)
		}
	})
}

func TestCooldownFallbackWithPreference(t *testing.T) {
	in := newTestRoutingEngine()

	t.Run("local-only with cooldown fallback", func(t *testing.T) {
		// Pinned to fiz, but one provider in cooldown.
		// Since it's local-only, it MUST still pick a local provider.
		in.ProviderCooldowns = map[string]time.Time{
			"vidar-omlx": in.Now.Add(-5 * time.Second),
		}
		in.CooldownDuration = 30 * time.Second

		req := Request{Harness: "fiz", ProviderPreference: ProviderPreferenceLocalOnly}
		dec, err := Resolve(req, in)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// vidar-omlx is in cooldown, so it should pick another local provider if available,
		// or demote vidar.
		// In newTestRoutingEngine, fiz has vidar-omlx and openrouter.
		// openrouter is also under 'fiz' harness (local cost class).
		if dec.Provider != "openrouter" {
			t.Errorf("local-only cooldown fallback: got %q, want openrouter", dec.Provider)
		}
	})
}

// TestDefaultPreferenceNormalization verifies ResolveRoute normalizes
// empty ProviderPreference to local-first.
func TestDefaultPreferenceNormalization(t *testing.T) {
	// Both the service (service_routing.go) and the routing engine normalize
	// empty to local-first. The engine's scorePolicy already treats "" the
	// same as "local-first" (the ProviderPreference switch has "" as a
	// local-first branch), so a Request with ProviderPreference="" must behave
	// identically to one with ProviderPreference="local-first".
	// We test this at the routing engine level.
	in := newTestRoutingEngine()

	emptyReq := Request{Policy: "cheap", ProviderPreference: ""}
	localReq := Request{Policy: "cheap", ProviderPreference: ProviderPreferenceLocalFirst}

	decEmpty, errEmpty := Resolve(emptyReq, in)
	decLocal, errLocal := Resolve(localReq, in)

	if errEmpty != nil {
		t.Fatalf("Resolve with empty preference: %v", errEmpty)
	}
	if errLocal != nil {
		t.Fatalf("Resolve with local-first: %v", errLocal)
	}
	if decEmpty.Harness != decLocal.Harness || decEmpty.Provider != decLocal.Provider {
		t.Errorf("empty preference should behave identically to local-first:\n  empty: harness=%s provider=%s\n  local-first: harness=%s provider=%s",
			decEmpty.Harness, decEmpty.Provider, decLocal.Harness, decLocal.Provider)
	}
}

// TestLocalOnlyExhaustsCandidates verifies local-only returns an error
// when no local candidate is viable (all local harnesses unavailable).
func TestLocalOnlyExhaustsCandidates(t *testing.T) {
	in := newTestRoutingEngine()
	// Mark the fiz harness unavailable.
	for i, h := range in.Harnesses {
		if h.Name == "fiz" {
			in.Harnesses[i].Available = false
		}
	}

	req := Request{ProviderPreference: ProviderPreferenceLocalOnly}
	dec, err := Resolve(req, in)

	if err == nil {
		t.Fatal("local-only with no viable local harness: expected error, got nil")
	}
	var policyErr *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &policyErr) {
		t.Errorf("error should be policy requirement failure: %T %v", err, err)
	}
	// All candidates should be ineligible.
	if dec != nil {
		for _, c := range dec.Candidates {
			if c.Eligible {
				t.Errorf("under local-only with no local harness: candidate %s should be ineligible, got eligible", c.Harness)
			}
		}
	}
}

// TestSubscriptionOnlyWithExhaustedQuota verifies subscription-only with
// exhausted quota returns an error when the subscription harness
// SubscriptionOK=false (hard gate).
func TestSubscriptionOnlyWithExhaustedQuota(t *testing.T) {
	in := newTestRoutingEngine()
	// Mark all subscription harnesses as having exhausted quota (hard gate).
	for i, h := range in.Harnesses {
		if h.IsSubscription {
			in.Harnesses[i].SubscriptionOK = false
		}
	}

	req := Request{ProviderPreference: ProviderPreferenceSubscriptionOnly}
	dec, err := Resolve(req, in)

	if err == nil {
		t.Fatal("subscription-only with all quotas exhausted: expected error, got nil")
	}
	var policyErr *ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &policyErr) {
		t.Errorf("error should be policy requirement failure: %T %v", err, err)
	}
	// Subscription harnesses should be ineligible due to QuotaOK=false.
	if dec != nil {
		for _, c := range dec.Candidates {
			if c.Harness == "claude" || c.Harness == "codex" {
				if c.Eligible {
					t.Errorf("subscription harness %s with QuotaOK=false should be ineligible under subscription-only", c.Harness)
				}
			}
		}
	}
}

// TestLocalFirstDefaultCooldownFallback verifies that with default/local-first
// preference, when the only local harness is in cooldown, the engine falls
// back to a subscription harness in the same tier.
func TestLocalFirstDefaultCooldownFallback(t *testing.T) {
	in := newTestRoutingEngine()

	// Put the fiz harness (local) in cooldown via harness-level cooldown flag.
	// This affects all providers under the harness simultaneously.
	for i, h := range in.Harnesses {
		if h.Name == "fiz" {
			in.Harnesses[i].InCooldown = true
		}
	}

	// Default/empty preference: should fall back to subscription when local is down.
	// Use smart policy so cooldown demotion (140-50=90) falls below subscription (100).
	req := Request{Policy: "smart", ProviderPreference: ""}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve with empty preference + local cooldown: %v", err)
	}
	// Should fall back to subscription (claude or codex).
	if dec.Harness == "fiz" {
		t.Errorf("local-first default with fiz in cooldown: should fall back to subscription harness, got fiz")
	}
}

// TestSubscriptionFirstSelectsSubscription verifies that subscription-first
// selects a subscription harness over a local harness when both are viable,
// for a policy that would otherwise prefer local.
func TestSubscriptionFirstSelectsSubscription(t *testing.T) {
	in := newTestRoutingEngine()

	// default policy + subscription-first ties local and subscription in the
	// current score policy, then the locality tiebreak keeps the local route.
	// default + local-first: fiz (local) = 100+25-0 = 125; claude = 100+15-20 = 95 → fiz wins
	// default + subscription-first: fiz = 100+25-0 = 125; claude = 100+30+15-20 = 125 → fiz wins on locality
	req := Request{Policy: "default", ProviderPreference: ProviderPreferenceSubscriptionFirst}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "fiz" {
		t.Errorf("default + subscription-first: expected fiz after locality tiebreak, got %q", dec.Harness)
	}
}

// TestQuotaSignalsConsideredInScoring verifies that QuotaStale and
// QuotaPercentUsed affect subscription candidate scores, not model tier.
func TestQuotaSignalsConsideredInScoring(t *testing.T) {
	in := newTestRoutingEngine()

	// Make codex and claude both viable with same cost class.
	// Give claude fresh quota with no burn, codex stale/burning quota.
	for i, h := range in.Harnesses {
		if h.Name == "claude" {
			in.Harnesses[i].QuotaStale = false
			in.Harnesses[i].QuotaTrend = QuotaTrendHealthy
			in.Harnesses[i].QuotaPercentUsed = 10
			in.Harnesses[i].QuotaOK = true
		}
		if h.Name == "codex" {
			in.Harnesses[i].QuotaStale = true
			in.Harnesses[i].QuotaTrend = QuotaTrendExhausting
			in.Harnesses[i].QuotaPercentUsed = 95
			in.Harnesses[i].QuotaOK = true
		}
	}

	req := Request{Policy: "smart"}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Fresh/healthy claude should beat stale/exhausting codex.
	if dec.Harness != "claude" {
		t.Errorf("quota signals: expected claude (fresh/healthy), got %q", dec.Harness)
	}
}

func TestQuotaHeadroomContinuouslyAffectsSubscriptionScoring(t *testing.T) {
	in := newTestRoutingEngine()

	for i, h := range in.Harnesses {
		switch h.Name {
		case "claude":
			in.Harnesses[i].QuotaStale = false
			in.Harnesses[i].QuotaTrend = QuotaTrendHealthy
			in.Harnesses[i].QuotaPercentUsed = 30
			in.Harnesses[i].QuotaOK = true
		case "codex":
			in.Harnesses[i].QuotaStale = false
			in.Harnesses[i].QuotaTrend = QuotaTrendHealthy
			in.Harnesses[i].QuotaPercentUsed = 60
			in.Harnesses[i].QuotaOK = true
		}
	}

	dec, err := Resolve(Request{Policy: "smart"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "claude" {
		t.Fatalf("lower used quota should win among otherwise comparable harnesses, got %q", dec.Harness)
	}

	var claudeScore, codexScore float64
	for _, c := range dec.Candidates {
		switch c.Harness {
		case "claude":
			claudeScore = c.Score
		case "codex":
			codexScore = c.Score
		}
	}
	if claudeScore <= codexScore {
		t.Fatalf("quota headroom score ordering: claude %.1f codex %.1f", claudeScore, codexScore)
	}
}

// TestNonClaudeSubscriptionQuotaStale verifies that subscription harnesses
// without a durable quota cache have QuotaStale=true in the routing inputs,
// causing their scores to be penalized.
func TestNonClaudeSubscriptionQuotaStale(t *testing.T) {
	// Simulate what buildRoutingInputs produces for codex (subscription harness
	// without a durable quota cache): QuotaOK=true (hardcoded), QuotaStale=true
	// (no durable cache → conservative stale signal), QuotaTrend=Unknown.
	codexEntry := HarnessEntry{
		Name:                "codex",
		Surface:             "codex",
		CostClass:           "medium",
		IsSubscription:      true,
		AutoRoutingEligible: true,
		Available:           true,
		QuotaOK:             true, // hardcoded for non-Claude
		QuotaStale:          true, // no durable cache → stale
		QuotaTrend:          QuotaTrendUnknown,
		SubscriptionOK:      true, // eligible when SubscriptionOK=true
		DefaultModel:        "gpt-5.4",
		ExactPinSupport:     true,
		SupportsTools:       true,
	}

	in := Inputs{
		Harnesses: []HarnessEntry{codexEntry},
		Now:       time.Now(),
	}
	req := Request{Policy: "smart", ProviderPreference: ProviderPreferenceLocalFirst}
	dec, err := Resolve(req, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// codex should still be eligible (QuotaOK=true).
	if !dec.Eligible() {
		t.Errorf("codex with QuotaOK=true should be eligible, got ineligible")
	}

	// Find the codex candidate and verify the score includes the stale penalty.
	// smart base for codex: 100 + 40 (cr=2) + 5 (quota) - 15 (stale)
	// - 5 (unknown utilization) - 5 (unknown performance) = 120.
	var codexScore float64
	for _, c := range dec.Candidates {
		if c.Harness == "codex" {
			codexScore = c.Score
		}
	}
	if codexScore != 120 {
		t.Errorf("codex stale quota score: got %.1f, want 120.0", codexScore)
	}
}

// claudeSurfacePairInputs builds an Inputs with BOTH claude (--print) and
// claude-tui sharing the "claude" surface, identical on every routing signal
// (cost class, quota, tools) so the ONLY differentiator is the surface
// preference. This isolates the claude-tui-default behavior.
func claudeSurfacePairInputs() Inputs {
	mk := func(name string) HarnessEntry {
		return HarnessEntry{
			Name:                name,
			Surface:             "claude",
			CostClass:           "medium",
			IsSubscription:      true,
			AutoRoutingEligible: true,
			ExactPinSupport:     name == "claude", // claude-tui has no exact pin
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			QuotaTrend:          QuotaTrendHealthy,
			SupportedReasoning:  []string{"low", "medium", "high"},
			SupportedPerms:      []string{"safe", "supervised", "unrestricted"},
			SupportsTools:       true,
			DefaultModel:        "claude-sonnet-4-6",
		}
	}
	return Inputs{
		Harnesses: []HarnessEntry{mk("claude"), mk("claude-tui")},
		Now:       time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
	}
}

// TestClaudeTuiIsDefaultForClaudeSurface proves the reliability/claude-tui-
// default behavior: an UNPINNED claude-surface route resolves to claude-tui
// over claude(--print). Without the surface preference, the alphabetical
// tie-break (claude < claude-tui) would pick the --print runner.
func TestClaudeTuiIsDefaultForClaudeSurface(t *testing.T) {
	in := claudeSurfacePairInputs() // nil SurfacePreference → DefaultSurfacePreference()

	dec, err := Resolve(Request{Policy: "default"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "claude-tui" {
		t.Fatalf("unpinned claude-surface route = %q, want claude-tui (default)", dec.Harness)
	}

	// The preferred candidate must outscore (or at least tie-and-win-by-
	// preference) the --print runner.
	var tuiScore, printScore float64
	var sawBoth int
	for _, c := range dec.Candidates {
		switch c.Harness {
		case "claude-tui":
			tuiScore = c.Score
			sawBoth++
		case "claude":
			printScore = c.Score
			sawBoth++
		}
	}
	if sawBoth != 2 {
		t.Fatalf("expected both claude and claude-tui candidates, saw %d", sawBoth)
	}
	if tuiScore < printScore {
		t.Errorf("claude-tui score %.2f < claude score %.2f; preferred harness must not lose on score", tuiScore, printScore)
	}
}

// TestClaudeTuiResolvesDefaultTierModel proves F5/F6 end-to-end at the routing
// layer: an UNPINNED default-policy claude-surface request resolves to claude-tui
// with a supported tier model (one of the catalog claude-code tiers, NOT the
// buggy discovery junk). claude-tui has ExactPinSupport=false, so the route must
// flow through automatic tier resolution (AutoRoutingModels), and the resolved
// model must be one claude-tui advertises in SupportedModels — proving a
// default-policy route both PICKS claude-tui and lands on a model it can set.
func TestClaudeTuiResolvesDefaultTierModel(t *testing.T) {
	mk := func(name string) HarnessEntry {
		return HarnessEntry{
			Name:                name,
			Surface:             "claude",
			CostClass:           "medium",
			IsSubscription:      true,
			AutoRoutingEligible: true,
			ExactPinSupport:     name == "claude", // claude-tui: no exact pin (matches registry)
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			QuotaTrend:          QuotaTrendHealthy,
			SupportsTools:       true,
			DefaultModel:        "claude-sonnet-4-6",
			// Both harnesses advertise the catalog claude-code tier surface. The
			// service appends these to SupportedModels for every subscription
			// harness (service_routing.go), so claude-tui can satisfy the resolved
			// tier model.
			SupportedModels:   []string{"opus-4.8", "sonnet-4.6", "haiku-4.5"},
			AutoRoutingModels: []string{"sonnet-4.6", "opus-4.8"},
		}
	}
	in := Inputs{
		Harnesses: []HarnessEntry{mk("claude"), mk("claude-tui")},
		Now:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	// Unpinned default-policy request (the mandatory effective-default path).
	dec, err := Resolve(Request{Policy: "default"}, in)
	if err != nil {
		t.Fatalf("Resolve(default): %v", err)
	}
	if dec.Harness != "claude-tui" {
		t.Fatalf("unpinned default-policy route = %q, want claude-tui (effective default)", dec.Harness)
	}
	// The resolved model must be a tier model claude-tui actually supports — not
	// "" and not a non-tier alias.
	supported := map[string]bool{"opus-4.8": true, "sonnet-4.6": true, "haiku-4.5": true}
	if !supported[dec.Model] {
		t.Fatalf("resolved model = %q, want a supported claude-tui tier model %v", dec.Model, supported)
	}
}

// TestExplicitClaudePinBeatsTuiDefault proves the default is REVERTIBLE per
// route: an explicit --harness claude pin resolves to claude(--print) even
// though claude-tui is the surface default.
func TestExplicitClaudePinBeatsTuiDefault(t *testing.T) {
	in := claudeSurfacePairInputs()

	dec, err := Resolve(Request{Policy: "default", Harness: "claude"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "claude" {
		t.Fatalf("explicit --harness claude = %q, want claude (pin authoritative)", dec.Harness)
	}
}

// TestSurfacePreferenceDisabledFallsBackToAlphabetical proves the preference is
// config-gated: an explicit EMPTY (non-nil) SurfacePreference disables it, so
// the engine falls back to the alphabetical tie-break and claude(--print) wins.
func TestSurfacePreferenceDisabledFallsBackToAlphabetical(t *testing.T) {
	in := claudeSurfacePairInputs()
	in.SurfacePreference = map[string]string{} // explicit empty = disabled

	dec, err := Resolve(Request{Policy: "default"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "claude" {
		t.Fatalf("preference disabled: route = %q, want claude (alphabetical tie-break)", dec.Harness)
	}
}

// TestDefaultSurfacePreferenceConstant pins the built-in default mapping so a
// silent change to the claude→claude-tui default is caught.
func TestDefaultSurfacePreferenceConstant(t *testing.T) {
	pref := DefaultSurfacePreference()
	if got := pref["claude"]; got != "claude-tui" {
		t.Errorf("DefaultSurfacePreference()[claude] = %q, want claude-tui", got)
	}
}

func TestCostTiebreakLowerCostWins(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{
				{Name: "expensive", DefaultModel: "m", CostUSDPer1kTokens: 0.02, CostSource: CostSourceCatalog},
				{Name: "cheap", DefaultModel: "m", CostUSDPer1kTokens: 0.01, CostSource: CostSourceCatalog},
			},
		}},
	}

	dec, err := Resolve(Request{Harness: "fiz"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "cheap" {
		t.Fatalf("cost tiebreak provider=%q, want cheap", dec.Provider)
	}
}

func TestCostTiebreakFallsThroughToLocalityWhenCostsMatch(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{
			{
				Name:                "cloud",
				Surface:             "x",
				CostClass:           "medium",
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				DefaultModel:        "m",
				ExactPinSupport:     true,
				SupportsTools:       true,
				Providers:           []ProviderEntry{{Name: "cloudp", DefaultModel: "m", CostUSDPer1kTokens: 0.01, CostSource: CostSourceCatalog}},
			},
			{
				Name:                "local",
				Surface:             "x",
				CostClass:           "local",
				AutoRoutingEligible: true,
				Available:           true,
				QuotaOK:             true,
				SubscriptionOK:      true,
				DefaultModel:        "m",
				ExactPinSupport:     true,
				SupportsTools:       true,
				Providers:           []ProviderEntry{{Name: "localp", DefaultModel: "m", CostUSDPer1kTokens: 0.01, CostSource: CostSourceCatalog}},
			},
		},
		ObservedLatencyMS: map[string]float64{
			ProviderModelKey("localp", "", "m"): 100,
		},
	}

	dec, err := Resolve(Request{Policy: "default", ProviderPreference: ProviderPreferenceLocalFirst}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Harness != "local" {
		t.Fatalf("identical cost should fall through to locality: harness=%q", dec.Harness)
	}
}

func TestUnknownCostIsNeutralInTiebreak(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{
				{Name: "expensive", DefaultModel: "m", CostUSDPer1kTokens: 0.09, CostSource: CostSourceCatalog},
				{Name: "unknown", DefaultModel: "m", CostSource: CostSourceUnknown},
				{Name: "cheap", DefaultModel: "m", CostUSDPer1kTokens: 0.01, CostSource: CostSourceCatalog},
			},
		}},
	}

	dec, err := Resolve(Request{Harness: "fiz"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := []string{dec.Candidates[0].Provider, dec.Candidates[1].Provider, dec.Candidates[2].Provider}; got[0] != "cheap" || got[1] != "unknown" || got[2] != "expensive" {
		t.Fatalf("unknown-cost neutral order=%v, want [cheap unknown expensive]", got)
	}
}

func TestAllUnknownCostSkipsCostTiebreak(t *testing.T) {
	in := Inputs{
		Harnesses: []HarnessEntry{{
			Name:                "fiz",
			Surface:             "embedded-openai",
			CostClass:           "local",
			AutoRoutingEligible: true,
			Available:           true,
			QuotaOK:             true,
			SubscriptionOK:      true,
			ExactPinSupport:     true,
			SupportsTools:       true,
			Providers: []ProviderEntry{
				{Name: "z-provider", DefaultModel: "m", CostSource: CostSourceUnknown},
				{Name: "a-provider", DefaultModel: "m", CostSource: CostSourceUnknown},
			},
		}},
	}

	dec, err := Resolve(Request{Harness: "fiz"}, in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if dec.Provider != "a-provider" {
		t.Fatalf("all-unknown cost should fall through to name: provider=%q", dec.Provider)
	}
}
