package quota

import (
	"context"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
	"github.com/easel/fizeau/internal/routing"
)

// SubscriptionView is the normalized quota summary for a subscription-backed
// harness.
type SubscriptionView struct {
	OK      bool
	Present bool
	Fresh   bool
	// Exhausted is true ONLY when the harness reports a PROVEN quota block
	// (QuotaBlocked, or — for gemini — all windows blocked). It is the single
	// fail-closed signal: a missing/stale/unavailable/unknown snapshot is NOT
	// exhausted (Exhausted stays false) so routing fails OPEN on unconfirmed
	// state rather than fabricating "no viable provider". See FEAT-004 fail-open
	// contract: report failure only with positive proof.
	Exhausted   bool
	Windows     []harnesses.QuotaWindow
	PercentUsed int
	Trend       string
	Reason      string
}

// SubscriptionForHarness resolves a harness quota snapshot and normalizes the
// routing-oriented summary fields consumed by the root service facade.
func SubscriptionForHarness(name string, now time.Time) (SubscriptionView, bool) {
	qh, ok := builtin.New(name).(harnesses.QuotaHarness)
	if !ok {
		return SubscriptionView{}, false
	}
	status, err := qh.QuotaStatus(context.Background(), now)
	if err != nil {
		return SubscriptionView{}, false
	}
	return viewFromStatus(name, status), true
}

// viewFromStatus is the pure projection of a QuotaStatus into a routing
// SubscriptionView. It is the fail-open contract's single decision point for
// quota: Exhausted is set ONLY on proven QuotaBlocked (or, for gemini, all
// windows blocked) — unavailable/unknown/stale snapshots leave Exhausted false
// so routing stays eligible (demoted via QuotaOK), never fabricated-rejected.
// Extracted as a pure function (no builtin.New / no I/O) so the fail-open
// behavior is deterministically unit-testable from constructed QuotaStatus.
func viewFromStatus(name string, status harnesses.QuotaStatus) SubscriptionView {
	present := status.State != harnesses.QuotaUnavailable
	view := SubscriptionView{
		OK:      status.RoutingPreference == harnesses.RoutingPreferenceAvailable,
		Present: present,
		Fresh:   status.Fresh,
		// Proven exhaustion only. Unavailable/unknown/stale are NOT exhausted.
		Exhausted: status.State == harnesses.QuotaBlocked,
		Reason:    status.Reason,
		Trend:     routing.QuotaTrendUnknown,
	}
	if !present {
		return view
	}
	view.Windows = append([]harnesses.QuotaWindow(nil), status.Windows...)
	view.PercentUsed = int(MaxQuotaWindowUsedPercent(status.Windows))
	if name == "gemini" {
		exhausted, available := 0, 0
		for _, w := range status.Windows {
			switch w.State {
			case "blocked":
				exhausted++
			case "ok":
				available++
			}
		}
		if exhausted > 0 && available == 0 {
			view.Trend = routing.QuotaTrendExhausting
			// All gemini windows blocked = proven exhaustion (fail-closed).
			view.Exhausted = true
			return view
		}
	}
	view.Trend = TrendFromUsage(view.PercentUsed, status.Fresh)
	return view
}

// MaxQuotaWindowUsedPercent returns the most-constrained usage percentage
// across all known quota windows.
func MaxQuotaWindowUsedPercent(windows []harnesses.QuotaWindow) float64 {
	maxUsed := 0.0
	for _, window := range windows {
		if window.UsedPercent > maxUsed {
			maxUsed = window.UsedPercent
		}
	}
	return maxUsed
}

// TrendFromUsage converts percent-used plus freshness into the public routing
// quota trend vocabulary.
func TrendFromUsage(percentUsed int, fresh bool) string {
	switch {
	case percentUsed >= 90:
		return routing.QuotaTrendExhausting
	case percentUsed >= 70:
		return routing.QuotaTrendBurning
	case fresh:
		return routing.QuotaTrendHealthy
	default:
		return routing.QuotaTrendUnknown
	}
}
