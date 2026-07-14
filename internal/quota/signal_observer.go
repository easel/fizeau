package quota

import (
	"strings"
	"time"

	"github.com/easel/fizeau/internal/provider/quotaheaders"
)

const signalExhaustionFallback = time.Minute

// NewSignalObserver returns an observer that projects provider quota-header
// signals into store. Inconclusive signals preserve the current state.
func NewSignalObserver(store *StateStore, provider string, now func() time.Time) func(quotaheaders.Signal) {
	provider = strings.TrimSpace(provider)
	if store == nil || provider == "" {
		return nil
	}
	if now == nil {
		now = time.Now
	}

	return func(signal quotaheaders.Signal) {
		observedAt := now()
		exhausted, retryAfter := signal.IsExhausted(observedAt)
		if exhausted {
			if retryAfter.IsZero() {
				retryAfter = observedAt.Add(signalExhaustionFallback)
			}
			store.MarkQuotaExhausted(provider, retryAfter)
			return
		}

		if !signal.Present || signal.RetryAfter > 0 {
			return
		}
		if signal.RemainingTokens == 0 || signal.RemainingRequests == 0 {
			return
		}
		if signal.RemainingTokens > 0 || signal.RemainingRequests > 0 {
			store.MarkAvailable(provider)
		}
	}
}
