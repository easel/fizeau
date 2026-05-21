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
	OK          bool
	Present     bool
	Fresh       bool
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
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	status, err := qh.QuotaStatus(ctx, now)
	if err != nil {
		return SubscriptionView{}, false
	}
	present := status.State != harnesses.QuotaUnavailable
	view := SubscriptionView{
		OK:      status.RoutingPreference == harnesses.RoutingPreferenceAvailable,
		Present: present,
		Fresh:   status.Fresh,
		Reason:  status.Reason,
		Trend:   routing.QuotaTrendUnknown,
	}
	if !present {
		return view, true
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
			return view, true
		}
	}
	view.Trend = TrendFromUsage(view.PercentUsed, status.Fresh)
	return view, true
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
