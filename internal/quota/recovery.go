package quota

import (
	"context"
	"time"
)

const (
	defaultRecoveryFallback = 5 * time.Minute
	recoveryBackoffInitial  = 5 * time.Minute
	recoveryBackoffMax      = time.Hour
)

// RecoveryOptions supplies scheduling policy and deterministic seams for the
// quota recovery loop. Zero-value options use the production defaults.
type RecoveryOptions struct {
	Fallback time.Duration
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) bool
}

// RunRecoveryLoop periodically probes providers whose quota-exhaustion retry
// time has elapsed. Successful probes make the provider available; failures
// retain exhaustion and schedule another probe with bounded exponential
// backoff. The loop exits when ctx is cancelled.
func RunRecoveryLoop(
	ctx context.Context,
	store *StateStore,
	probe func(context.Context, string) error,
	opts RecoveryOptions,
) {
	if ctx == nil || store == nil || probe == nil {
		return
	}
	if opts.Fallback <= 0 {
		opts.Fallback = defaultRecoveryFallback
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = recoverySleep
	}

	backoffs := make(map[string]time.Duration)
	for ctx.Err() == nil {
		next := runRecoveryPass(ctx, store, probe, opts.Fallback, opts.Now, backoffs)
		if ctx.Err() != nil || !opts.Sleep(ctx, next) {
			return
		}
	}
}

// runRecoveryPass executes one sweep over the quota-exhausted set and returns
// the duration until the next sweep.
func runRecoveryPass(
	ctx context.Context,
	store *StateStore,
	probe func(context.Context, string) error,
	fallback time.Duration,
	now func() time.Time,
	backoffs map[string]time.Duration,
) time.Duration {
	entries := store.AllExhausted()
	current := now()

	// Providers may be restored by an external signal between passes. Forget
	// their prior failures so a later exhaustion starts at the initial delay.
	for provider := range backoffs {
		if _, ok := entries[provider]; !ok {
			delete(backoffs, provider)
		}
	}

	if len(entries) == 0 {
		return fallback
	}

	nextWake := fallback
	for provider, retryAfter := range entries {
		if ctx.Err() != nil {
			return fallback
		}
		if !retryAfter.IsZero() && retryAfter.After(current) {
			if untilRetry := retryAfter.Sub(current); untilRetry < nextWake {
				nextWake = untilRetry
			}
			continue
		}

		if err := probe(ctx, provider); err == nil {
			store.MarkAvailable(provider)
			delete(backoffs, provider)
			continue
		}

		nextBackoff := nextRecoveryBackoff(backoffs[provider])
		backoffs[provider] = nextBackoff
		store.MarkQuotaExhausted(provider, current.Add(nextBackoff))
		if nextBackoff < nextWake {
			nextWake = nextBackoff
		}
	}

	if nextWake <= 0 {
		return fallback
	}
	return nextWake
}

func nextRecoveryBackoff(previous time.Duration) time.Duration {
	if previous <= 0 {
		return recoveryBackoffInitial
	}
	next := previous * 2
	if next > recoveryBackoffMax || next < previous {
		return recoveryBackoffMax
	}
	return next
}

func recoverySleep(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
