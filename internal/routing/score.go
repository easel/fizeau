package routing

// costClassRank maps cost class to numeric rank (lower = cheaper).
var costClassRank = map[string]int{
	"local":        0,
	"cheap":        1,
	"medium":       2,
	"expensive":    3,
	"experimental": -1,
	"":             2, // unknown = medium
}

const StickyAffinityBonus = 250.0
const unknownUtilizationPenalty = 5.0
const unknownPerformancePenalty = 5.0
const unknownLocalHealthPenalty = 100.0
const belowMinPowerPenalty = 12.0
const aboveMaxPowerExclusionPenalty = 1000.0

// scorePolicy returns a score for a candidate under the named policy.
// Higher is better.
//
// Routing priority policy (ported from DDx routing.go):
//   - cheap: local + low-cost preferred; subscription-within-quota next.
//   - default: balanced; light local/subscription preference to avoid spend.
//   - smart: quality first; cloud capability wins; no local bonus.
//
// The policy is policy-aware AND provider-aware via providerBias hooks
// supplied by the caller (cooldown demotion, observation perf bias,
// provider-affinity bias).
func scorePolicy(policy string, cand candidateInternal) float64 {
	total := 0.0
	for _, component := range scoreComponents(policy, cand) {
		total += component
	}
	return total
}

func scoreComponents(policy string, cand candidateInternal) map[string]float64 {
	components := map[string]float64{
		"base":                100,
		"cost":                0,
		"deployment_locality": 0,
		"quota_health":        0,
		"utilization":         0,
		"performance":         0,
		"power":               0,
		"context_headroom":    0,
		"sticky_affinity":     0,
		"surface_preference":  0,
	}
	base := 100.0
	cr := costClassRank[cand.CostClass]
	withinQuota := cand.IsSubscription && cand.QuotaOK
	hasPowerBounds := cand.MinPower > 0 || cand.MaxPower > 0
	add := func(name string, value float64) {
		if value == 0 {
			return
		}
		components[name] += value
	}

	// Policy preference scoring. When no power bounds are set the full policy
	// block applies (original behavior). When power bounds are set, apply only
	// the subscription/quota bonus to in-bounds candidates so that a free
	// subscription candidate still beats a metered one among the eligible set.
	// Locality bonuses and cost-class rank penalties remain gated on
	// !hasPowerBounds: locality bonuses would otherwise let metered providers
	// that inherit a "local" cost class from their harness outrank subscription
	// candidates, and cost penalties would double-penalize metered candidates.
	switch policy {
	case "cheap":
		if !hasPowerBounds {
			if cand.CostClass == "local" {
				base += 40
				add("deployment_locality", 40)
			} else if withinQuota {
				base += 20
				add("quota_health", 20)
			}
			base -= float64(cr) * 30
			add("cost", -float64(cr)*30)
		} else if candidateWithinPowerBounds(cand) && withinQuota {
			base += 20
			add("quota_health", 20)
		}

	case "default":
		if !hasPowerBounds {
			if cand.CostClass == "local" {
				base += 25
				add("deployment_locality", 25)
			} else if withinQuota {
				base += 15
				add("quota_health", 15)
			}
			base -= float64(cr) * 10
			add("cost", -float64(cr)*10)
		} else if candidateWithinPowerBounds(cand) && withinQuota {
			base += 15
			add("quota_health", 15)
		}

	case "smart":
		if !hasPowerBounds {
			// Quality first; higher cost rank approximates higher capability.
			base += float64(cr) * 20
			add("cost", float64(cr)*20)
			if withinQuota {
				base += 5
				add("quota_health", 5)
			}
		} else if candidateWithinPowerBounds(cand) && withinQuota {
			base += 5
			add("quota_health", 5)
		}

	default:
		// Treat unspecified as default.
		if !hasPowerBounds {
			if cand.CostClass == "local" {
				base += 25
				add("deployment_locality", 25)
			} else if withinQuota {
				base += 15
				add("quota_health", 15)
			}
			base -= float64(cr) * 10
			add("cost", -float64(cr)*10)
		} else if candidateWithinPowerBounds(cand) && withinQuota {
			base += 15
			add("quota_health", 15)
		}
	}

	// Provider preference bias. When power bounds are set, apply only the
	// subscription-first preference to in-bounds subscription candidates; the
	// local-first bias is omitted for the same locality-inheritance reason as
	// the policy locality bonuses above.
	if !hasPowerBounds {
		switch cand.ProviderPreference {
		case "local-first", "":
			if cand.CostClass == "local" {
				base += 30
				add("deployment_locality", 30)
			}
		case "subscription-first":
			if cand.IsSubscription && cand.QuotaOK {
				base += 30
				add("quota_health", 30)
			}
		}
	} else if candidateWithinPowerBounds(cand) && cand.ProviderPreference == "subscription-first" && cand.IsSubscription && cand.QuotaOK {
		base += 30
		add("quota_health", 30)
	}

	// Quota signals.
	if cand.IsSubscription {
		// Stale quota penalty.
		if cand.QuotaStale {
			base -= 15
			add("quota_health", -15)
		}

		// Trend-based adjustments.
		switch cand.QuotaTrend {
		case "exhausting":
			base -= 40
			add("quota_health", -40)
		case "burning":
			base -= 20
			add("quota_health", -20)
		case "healthy":
			base += 10
			add("quota_health", 10)
		}

		// Continuous quota pressure: subscription harnesses should start
		// yielding before they are nearly exhausted, with steeper demotion
		// once headroom drops below 50% and again below 20%.
		if cand.QuotaPercentUsed > 0 {
			penalty := quotaPressurePenalty(cand.QuotaPercentUsed)
			base -= penalty
			add("quota_health", -penalty)
		}
	}

	// Historical success-rate adjustment (only when sufficient samples).
	if cand.HistoricalSuccessRate >= 0 {
		switch {
		case cand.HistoricalSuccessRate >= 0.8:
			base += 20
			add("quota_health", 20)
		case cand.HistoricalSuccessRate < 0.5:
			base -= 30
			add("quota_health", -30)
		}
	}
	if cand.ProviderSuccessRate >= 0 {
		switch {
		case cand.ProviderSuccessRate >= 0.8:
			base += 25
			add("quota_health", 25)
		case cand.ProviderSuccessRate < 0.5:
			base -= 35
			add("quota_health", -35)
		}
	}

	// Cooldown demotion: candidate has had recent failures.
	if cand.InCooldown {
		base -= 50
		add("quota_health", -50)
	}
	if cand.LocalHealthUnknown {
		base -= unknownLocalHealthPenalty
		add("quota_health", -unknownLocalHealthPenalty)
	}

	// Sticky affinity is a bonus after eligibility, not a hard pin.
	if cand.StickyMatch {
		base += StickyAffinityBonus
		add("sticky_affinity", StickyAffinityBonus)
	}

	// Surface preference: the configured preferred harness for this surface on
	// an unpinned route gets a small bias so it wins what would otherwise be an
	// exact score tie with another harness sharing the surface (e.g. claude-tui
	// over claude on the "claude" surface). It is small enough not to override a
	// genuinely higher-scoring candidate.
	if cand.SurfacePreferred {
		base += SurfacePreferenceBias
		add("surface_preference", SurfacePreferenceBias)
	}

	// Utilization pressure can outweigh stickiness when the chosen server is
	// already busy or saturated.
	// Unknown or stale utilization is treated explicitly. Missing data gets a
	// small penalty so it cannot outrank a peer with real healthy evidence by
	// accident.
	if cand.EndpointSaturated {
		base -= 300
		add("utilization", -300)
	}
	if cand.EndpointLoad > 0 {
		loadPenalty := cand.EndpointLoad * 10
		if cand.EndpointLoadFresh {
			loadPenalty *= 2
		} else {
			loadPenalty += unknownUtilizationPenalty
		}
		if loadPenalty > 60 {
			loadPenalty = 60
		}
		base -= loadPenalty
		add("utilization", -loadPenalty)
	} else if !cand.EndpointLoadFresh {
		base -= unknownUtilizationPenalty
		add("utilization", -unknownUtilizationPenalty)
	}

	// Provider affinity: explicit provider pins are filtered before scoring;
	// this bonus only affects the ordering among still-eligible candidates
	// that share the pinned provider identity.
	if cand.ProviderAffinityMatch {
		base += 15
		add("deployment_locality", 15)
	}

	if cand.Power > 0 && cand.MinPower == 0 && cand.MaxPower == 0 {
		base += float64(cand.Power) * 12
		add("power", float64(cand.Power)*12)
	}
	if cand.Power > 0 {
		if cand.MinPower > 0 && cand.Power < cand.MinPower {
			// Below-min candidates need a materially steeper hit so a free but
			// underpowered route does not outrank an in-band option purely on cost.
			penalty := float64(cand.MinPower-cand.Power) * belowMinPowerPenalty
			base -= penalty
			add("power", -penalty)
		}
		if cand.MaxPower > 0 && cand.Power > cand.MaxPower {
			penalty := float64(cand.Power - cand.MaxPower)
			base -= penalty
			add("power", -penalty)
		}
	}
	if cand.Power > 0 && cand.CostUSDPer1kTokens > 0 {
		costPenalty := cand.CostUSDPer1kTokens * 500
		if costPenalty > 60 {
			costPenalty = 60
		}
		base -= costPenalty
		add("cost", -costPenalty)
	}
	if cand.Power > 0 && cand.CostUSDPer1kTokens == 0 && candidateWithinPowerBounds(cand) {
		switch {
		case cand.CostClass == "local":
			base += 15
			add("cost", 15)
		case cand.IsSubscription && cand.QuotaOK && !cand.QuotaStale && cand.QuotaPercentUsed < 80:
			base += 15
			add("quota_health", 15)
		}
	}
	if cand.Power >= 9 && cand.IsSubscription && candidateWithinPowerBounds(cand) && cand.QuotaOK && !cand.QuotaStale && cand.QuotaPercentUsed < 80 {
		// Why: an explicit sub-9 MinPower hint (e.g. role=implementer at
		// power=8) means a cheaper in-bounds tier already clears the band, so
		// rewarding a higher tier here would let opus systematically outrank a
		// peer sonnet on cost-aware policies. Smart policy is quality-first and
		// keeps the bonus.
		if policy == "smart" || cand.MinPower == 0 || cand.MinPower >= 9 {
			base += 20
			add("power", 20)
		}
	}

	// Context headroom is a ranking signal for otherwise eligible candidates.
	// A larger spare window gives the model more room for completion and tool
	// overhead, so reward it modestly without overpowering the primary policy.
	if cand.ContextHeadroom > 0 {
		headroomBonus := float64(cand.ContextHeadroom) / 1000.0
		if headroomBonus > 30 {
			headroomBonus = 30
		}
		base += headroomBonus
		add("context_headroom", headroomBonus)
	}

	// Observation-derived perf bias.
	havePerfSignal := false
	if cand.ObservedTokensPerSec > 0 {
		// Small additive bonus, scaled.
		bonus := cand.ObservedTokensPerSec / 100.0
		base += bonus
		add("performance", bonus)
		havePerfSignal = true
	}
	if cand.ObservedLatencyMS > 0 {
		// Latency is a tiebreaker-scale signal: faster endpoints gain a small
		// bonus while very slow endpoints receive little benefit.
		bonus := 1000.0 / cand.ObservedLatencyMS
		base += bonus
		add("performance", bonus)
		havePerfSignal = true
	}
	if !havePerfSignal {
		// Missing performance data is deliberate and mildly penalized rather
		// than treated as a hidden zero-value bonus.
		base -= unknownPerformancePenalty
		add("performance", -unknownPerformancePenalty)
	}
	if cand.CostClass == "experimental" {
		base -= 75
		add("deployment_locality", -75)
	}

	// base tracks the implicit profile baseline so the components sum to the
	// same total as scorePolicy's legacy behavior.
	components["base"] = base - (components["cost"] + components["deployment_locality"] + components["quota_health"] + components["sticky_affinity"] + components["utilization"] + components["power"] + components["context_headroom"] + components["performance"] + components["surface_preference"])
	return components
}

func quotaPressurePenalty(percentUsed int) float64 {
	if percentUsed <= 0 {
		return 0
	}
	penalty := float64(percentUsed) * 0.5
	if percentUsed > 50 {
		penalty += float64(percentUsed-50) * 1.5
	}
	if percentUsed > 80 {
		penalty += float64(percentUsed-80) * 3
	}
	return penalty
}

func candidateWithinPowerBounds(cand candidateInternal) bool {
	if cand.Power <= 0 {
		return false
	}
	if cand.MinPower > 0 && cand.Power < cand.MinPower {
		return false
	}
	if cand.MaxPower > 0 && cand.Power > cand.MaxPower {
		return false
	}
	return true
}
