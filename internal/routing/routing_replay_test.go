// routing_replay_test.go — Phase 0 frozen routing replay fixtures.
//
// Each fixture records the Resolve() decision for a (Request, Inputs) scenario
// as of the baseline commit. Changes to routing logic that alter these decisions
// must update the affected fixtures explicitly so regressions are detected before
// Phase 1-5 work lands.
//
// Fixture categories (per bead fizeau-ffb7cdb9 AC-1):
//   - unpinned: policy-driven routing with no explicit pins
//   - pinned:   explicit Harness, Provider, or Model constraints
//   - policy:   canonical policy behaviour (cheap/default/smart/air-gapped)
//   - power:    MinPower/MaxPower soft-hint routing
//   - exclusion: caller-supplied ExcludedRoutes filtering
package routing

import (
	"errors"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
)

// ---- Shared fixture inputs -----------------------------------------------

// replayStandardInputs returns a two-harness inventory used by the unpinned,
// pinned, and policy fixture categories. One local fiz harness and one
// subscription claude harness provide clear policy contrast.
func replayStandardInputs() Inputs {
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
				SupportedReasoning:  []string{"low", "medium", "high"},
				SupportedPerms:      []string{"safe", "supervised", "unrestricted"},
				Providers: []ProviderEntry{{
					Name:          "local-p",
					CostClass:     "local",
					DefaultModel:  "model-local",
					SupportsTools: true,
				}},
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
				SubscriptionOK:      true,
				SupportsTools:       true,
				SupportedModels:     []string{"model-sub"},
				DefaultModel:        "model-sub",
				Providers: []ProviderEntry{{
					Name:          "cloud-p",
					CostClass:     "medium",
					Billing:       modelcatalog.BillingModelSubscription,
					DefaultModel:  "model-sub",
					SupportsTools: true,
				}},
			},
		},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			switch model {
			case "model-local":
				return ModelEligibility{Power: 7, AutoRoutable: true}, true
			case "model-sub":
				return ModelEligibility{Power: 9, AutoRoutable: true}, true
			default:
				return ModelEligibility{}, false
			}
		},
		Now: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

// replayPowerInputs returns a single-harness inventory with two local
// providers whose models have distinct catalog power values (5 and 9).
// Used by the power fixture category.
func replayPowerInputs() Inputs {
	return Inputs{
		Harnesses: []HarnessEntry{{
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
					Name:          "low-p",
					CostClass:     "local",
					DefaultModel:  "model-low",
					DiscoveredIDs: []string{"model-low"},
					SupportsTools: true,
				},
				{
					Name:          "high-p",
					CostClass:     "local",
					DefaultModel:  "model-high",
					DiscoveredIDs: []string{"model-high"},
					SupportsTools: true,
				},
			},
		}},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			switch model {
			case "model-low":
				return ModelEligibility{Power: 5, AutoRoutable: true}, true
			case "model-high":
				return ModelEligibility{Power: 9, AutoRoutable: true}, true
			default:
				return ModelEligibility{}, false
			}
		},
		Now: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

// replayExclusionInputs returns a single-harness inventory with two local
// providers ("alpha" and "beta") at equal power. Used by the exclusion category.
func replayExclusionInputs() Inputs {
	return Inputs{
		Harnesses: []HarnessEntry{{
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
					Name:          "alpha",
					CostClass:     "local",
					DefaultModel:  "model-alpha",
					DiscoveredIDs: []string{"model-alpha"},
					SupportsTools: true,
				},
				{
					Name:          "beta",
					CostClass:     "local",
					DefaultModel:  "model-beta",
					DiscoveredIDs: []string{"model-beta"},
					SupportsTools: true,
				},
			},
		}},
		ModelEligibility: func(model string) (ModelEligibility, bool) {
			switch model {
			case "model-alpha", "model-beta":
				return ModelEligibility{Power: 7, AutoRoutable: true}, true
			default:
				return ModelEligibility{}, false
			}
		},
		Now: time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
	}
}

// ---- Fixture types -------------------------------------------------------

// routeFixture is one frozen (Request, Inputs) → Decision scenario.
type routeFixture struct {
	// name uniquely identifies the fixture. Sub-tests are run as t.Run(name).
	name string

	req Request
	in  Inputs

	// wantErr=true means Resolve must return a non-nil error.
	wantErr bool

	// wantErrKind, when non-empty, names the expected error type:
	//   "NoViableCandidate", "PolicyRequirement", "UnsatisfiablePin"
	wantErrKind string

	// Frozen decision fields (checked when !wantErr).
	wantHarness  string
	wantProvider string
	wantModel    string

	// wantCandidates lists per-candidate assertions that must hold.
	// Each entry is matched by (harness, provider) against dec.Candidates.
	wantCandidates []frozenCandidate
}

// frozenCandidate is a per-candidate assertion within a routeFixture.
type frozenCandidate struct {
	// provider identifies the candidate row. Required.
	provider string
	// harness narrows the match when set (useful when provider names overlap).
	harness      string
	eligible     bool
	filterReason FilterReason
}

// ---- Frozen fixture corpus -----------------------------------------------

// replayFixtures is the frozen corpus. Order within each category is stable.
// Add new entries at the end of the relevant category block.
var replayFixtures = func() []routeFixture {
	stdIn := replayStandardInputs
	pwrIn := replayPowerInputs
	exclIn := replayExclusionInputs

	return []routeFixture{

		// ================================================================
		// Category: unpinned — policy-driven routing, no explicit pins.
		// Frozen: local-first for cheap/default; subscription for smart.
		// ================================================================

		{
			name:         "unpinned/cheap-routes-local",
			req:          Request{Policy: "cheap"},
			in:           stdIn(),
			wantHarness:  "fiz",
			wantProvider: "local-p",
			wantModel:    "model-local",
		},
		{
			name:         "unpinned/default-routes-local",
			req:          Request{Policy: "default"},
			in:           stdIn(),
			wantHarness:  "fiz",
			wantProvider: "local-p",
			wantModel:    "model-local",
		},
		{
			name:         "unpinned/smart-routes-subscription",
			req:          Request{Policy: "smart"},
			in:           stdIn(),
			wantHarness:  "claude",
			wantProvider: "cloud-p",
			wantModel:    "model-sub",
			wantCandidates: []frozenCandidate{
				// smart policy disallows local candidates.
				{provider: "local-p", harness: "fiz", eligible: false, filterReason: FilterReasonPolicyFiltered},
				// subscription candidate wins.
				{provider: "cloud-p", harness: "claude", eligible: true},
			},
		},

		// ================================================================
		// Category: pinned — explicit Harness, Provider, or Model pins.
		// ================================================================

		{
			// Harness pin: only the named harness is evaluated.
			name:         "pinned/harness-routes-only-named-harness",
			req:          Request{Harness: "claude"},
			in:           stdIn(),
			wantHarness:  "claude",
			wantProvider: "cloud-p",
			wantModel:    "model-sub",
		},
		{
			// Provider pin: constrains to a specific provider under the harness.
			name:         "pinned/provider-routes-specific-provider",
			req:          Request{Harness: "fiz", Provider: "local-p"},
			in:           stdIn(),
			wantHarness:  "fiz",
			wantProvider: "local-p",
			wantModel:    "model-local",
		},
		{
			// Model pin on a subscription-supported model: subscription harness
			// wins over the local harness via applyModelPinSubscriptionPreference.
			name:         "pinned/model-prefers-subscription-harness",
			req:          Request{Model: "model-sub"},
			in:           stdIn(),
			wantHarness:  "claude",
			wantProvider: "cloud-p",
			wantModel:    "model-sub",
			wantCandidates: []frozenCandidate{
				// Local candidate suppressed because subscription covers the model.
				{provider: "local-p", harness: "fiz", eligible: false, filterReason: FilterReasonScoredBelowTop},
				{provider: "cloud-p", harness: "claude", eligible: true},
			},
		},

		// ================================================================
		// Category: policy — canonical policy semantics.
		// ================================================================

		{
			// air-gapped + no_remote filters subscription candidates.
			name: "policy/air-gapped-filters-subscription",
			req:  Request{Policy: "air-gapped", Require: []string{"no_remote"}},
			in:   stdIn(),
			// Local provider passes; cloud provider is policy-filtered.
			wantHarness:  "fiz",
			wantProvider: "local-p",
			wantModel:    "model-local",
			wantCandidates: []frozenCandidate{
				{provider: "local-p", harness: "fiz", eligible: true},
				{provider: "cloud-p", harness: "claude", eligible: false, filterReason: FilterReasonPolicyFiltered},
			},
		},
		{
			// air-gapped + no_remote + remote provider pin → immediate error.
			name:        "policy/air-gapped-remote-pin-is-error",
			req:         Request{Policy: "air-gapped", Require: []string{"no_remote"}, Provider: "cloud-p"},
			in:          stdIn(),
			wantErr:     true,
			wantErrKind: "PolicyRequirement",
		},
		{
			// smart policy filters local candidates (allow_local=false by default).
			name: "policy/smart-filters-local-candidates",
			req:  Request{Policy: "smart"},
			in:   stdIn(),
			wantCandidates: []frozenCandidate{
				{provider: "local-p", harness: "fiz", eligible: false, filterReason: FilterReasonPolicyFiltered},
				{provider: "cloud-p", harness: "claude", eligible: true},
			},
			wantHarness:  "claude",
			wantProvider: "cloud-p",
			wantModel:    "model-sub",
		},

		// ================================================================
		// Category: power — MinPower/MaxPower soft-hint routing.
		// low-p=power5, high-p=power9.
		// ================================================================

		{
			// MinPower is a soft hint: below-min candidates remain eligible but
			// score lower. The engine selects the higher-power candidate.
			name:         "power/min-soft-prefers-higher-power",
			req:          Request{Policy: "default", MinPower: 7},
			in:           pwrIn(),
			wantHarness:  "fiz",
			wantProvider: "high-p",
			wantModel:    "model-high",
			wantCandidates: []frozenCandidate{
				// low-p (power 5) is still eligible — MinPower does not hard-gate.
				{provider: "low-p", harness: "fiz", eligible: true},
				{provider: "high-p", harness: "fiz", eligible: true},
			},
		},
		{
			// MaxPower: when an in-bounds candidate exists, above-max candidates
			// are excluded with FilterReasonAboveMaxPower.
			name:         "power/max-excludes-above-when-in-bounds",
			req:          Request{Policy: "default", MaxPower: 7},
			in:           pwrIn(),
			wantHarness:  "fiz",
			wantProvider: "low-p",
			wantModel:    "model-low",
			wantCandidates: []frozenCandidate{
				{provider: "low-p", harness: "fiz", eligible: true},
				{provider: "high-p", harness: "fiz", eligible: false, filterReason: FilterReasonAboveMaxPower},
			},
		},
		{
			// When no candidate falls within [MinPower, MaxPower], the engine
			// does not hard-reject; the nearest candidate (smallest penalty) wins.
			// Undershooting is penalized more steeply than overshooting (12× vs 1×
			// per power unit), so the above-max candidate wins here.
			name:         "power/soft-bounds-no-in-bounds-nearest-wins",
			req:          Request{Policy: "default", MinPower: 6, MaxPower: 8},
			in:           pwrIn(),
			wantHarness:  "fiz",
			wantProvider: "high-p",
			wantModel:    "model-high",
			wantCandidates: []frozenCandidate{
				// Both remain eligible — no in-bounds candidate, so applyMaxPowerExclusion
				// does not fire.
				{provider: "low-p", harness: "fiz", eligible: true},
				{provider: "high-p", harness: "fiz", eligible: true},
			},
		},

		// ================================================================
		// Category: exclusion — caller-supplied ExcludedRoutes.
		// alpha/beta are equal-power local providers; alpha wins alphabetically.
		// ================================================================

		{
			// Baseline: no exclusion → alphabetical provider tiebreak → alpha wins.
			name:         "exclusion/no-exclusion-alphabetical-first",
			req:          Request{Policy: "default"},
			in:           exclIn(),
			wantHarness:  "fiz",
			wantProvider: "alpha",
			wantModel:    "model-alpha",
		},
		{
			// Provider exclusion: alpha excluded → beta wins.
			name: "exclusion/provider-excluded-routes-to-next",
			req: Request{
				Policy:         "default",
				ExcludedRoutes: []ExcludedRoute{{Provider: "alpha"}},
			},
			in:           exclIn(),
			wantHarness:  "fiz",
			wantProvider: "beta",
			wantModel:    "model-beta",
			wantCandidates: []frozenCandidate{
				{provider: "alpha", harness: "fiz", eligible: false, filterReason: FilterReasonCallerExcluded},
				{provider: "beta", harness: "fiz", eligible: true},
			},
		},
		{
			// Model-scoped exclusion: only alpha+model-alpha is excluded;
			// if a different model on alpha were requested it would be allowed.
			// Here we exclude alpha's specific model so beta wins.
			name: "exclusion/model-scoped-excludes-specific-model",
			req: Request{
				Policy:         "default",
				ExcludedRoutes: []ExcludedRoute{{Provider: "alpha", Model: "model-alpha"}},
			},
			in:           exclIn(),
			wantHarness:  "fiz",
			wantProvider: "beta",
			wantModel:    "model-beta",
			wantCandidates: []frozenCandidate{
				{provider: "alpha", harness: "fiz", eligible: false, filterReason: FilterReasonCallerExcluded},
				{provider: "beta", harness: "fiz", eligible: true},
			},
		},
	}
}()

// ---- Replay harness ------------------------------------------------------

// TestRoutingReplayFixtures replays every entry in replayFixtures and asserts
// that Resolve() matches the frozen decision. This is the Phase 0 regression
// baseline: if routing-logic changes alter a decision, this test surfaces the
// divergence so it can be evaluated and the fixture updated intentionally.
func TestRoutingReplayFixtures(t *testing.T) {
	for _, fix := range replayFixtures {
		fix := fix
		t.Run(fix.name, func(t *testing.T) {
			dec, err := Resolve(fix.req, fix.in)

			// ---- Error path ----
			if fix.wantErr {
				if err == nil {
					t.Fatalf("want error (kind %q), got success: harness=%s provider=%s model=%s",
						fix.wantErrKind, dec.Harness, dec.Provider, dec.Model)
				}
				assertErrKind(t, err, fix.wantErrKind)
				return
			}

			// ---- Success path ----
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if dec.Harness != fix.wantHarness {
				t.Errorf("Harness=%q, want %q", dec.Harness, fix.wantHarness)
			}
			if dec.Provider != fix.wantProvider {
				t.Errorf("Provider=%q, want %q", dec.Provider, fix.wantProvider)
			}
			if dec.Model != fix.wantModel {
				t.Errorf("Model=%q, want %q", dec.Model, fix.wantModel)
			}

			// ---- Candidate assertions ----
			for _, want := range fix.wantCandidates {
				c, ok := replayFindCandidate(dec.Candidates, want.harness, want.provider)
				if !ok {
					t.Errorf("candidate harness=%q provider=%q not found; candidates: %v",
						want.harness, want.provider, replayCandidateSummary(dec.Candidates))
					continue
				}
				if c.Eligible != want.eligible {
					t.Errorf("candidate %s/%s Eligible=%v, want %v (reason: %q)",
						want.harness, want.provider, c.Eligible, want.eligible, c.Reason)
				}
				if want.filterReason != "" && c.FilterReason != want.filterReason {
					t.Errorf("candidate %s/%s FilterReason=%q, want %q",
						want.harness, want.provider, c.FilterReason, want.filterReason)
				}
			}
		})
	}
}

// assertErrKind checks that err matches the named error kind.
func assertErrKind(t *testing.T, err error, kind string) {
	t.Helper()
	switch kind {
	case "NoViableCandidate":
		var typed *NoViableCandidateError
		if !errors.As(err, &typed) {
			t.Errorf("error kind %T, want *NoViableCandidateError: %v", err, err)
		}
	case "PolicyRequirement":
		var typed *ErrPolicyRequirementUnsatisfied
		if !errors.As(err, &typed) {
			t.Errorf("error kind %T, want *ErrPolicyRequirementUnsatisfied: %v", err, err)
		}
	case "UnsatisfiablePin":
		if !errors.Is(err, ErrUnsatisfiablePin{}) {
			t.Errorf("error kind %T, want ErrUnsatisfiablePin: %v", err, err)
		}
	case "":
		// No specific kind expected; any error satisfies wantErr=true.
	default:
		t.Errorf("unknown wantErrKind %q", kind)
	}
}

// replayFindCandidate returns the first candidate matching (harness, provider).
// An empty string for either field is a wildcard.
func replayFindCandidate(candidates []Candidate, harness, provider string) (Candidate, bool) {
	for _, c := range candidates {
		if (harness == "" || c.Harness == harness) && (provider == "" || c.Provider == provider) {
			return c, true
		}
	}
	return Candidate{}, false
}

// replayCandidateSummary returns a compact representation of the candidate list
// for test failure diagnostics.
func replayCandidateSummary(candidates []Candidate) string {
	if len(candidates) == 0 {
		return "[]"
	}
	var out []byte
	out = append(out, '[')
	for i, c := range candidates {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, c.Harness...)
		out = append(out, '/')
		out = append(out, c.Provider...)
		if !c.Eligible {
			out = append(out, '!')
		}
	}
	out = append(out, ']')
	return string(out)
}
