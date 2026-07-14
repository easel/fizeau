package quota

import (
	"testing"
	"time"

	"github.com/easel/fizeau/internal/provider/quotaheaders"
)

func TestSignalObserverNilInputsReturnNil(t *testing.T) {
	if got := NewSignalObserver(nil, "openai", nil); got != nil {
		t.Fatal("NewSignalObserver(nil store) returned a non-nil observer")
	}

	store := NewStateStore()
	for _, provider := range []string{"", " \t\n"} {
		if got := NewSignalObserver(store, provider, nil); got != nil {
			t.Fatalf("NewSignalObserver(provider %q) returned a non-nil observer", provider)
		}
	}
}

func TestSignalObserverRetryAfterOverridesPositiveCounters(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := NewStateStore()
	observe := NewSignalObserver(store, "openai", func() time.Time { return now })

	observe(quotaheaders.Signal{
		Present:           true,
		RemainingTokens:   500,
		RemainingRequests: 10,
		ResetTime:         now.Add(30 * time.Minute),
		RetryAfter:        7 * time.Minute,
	})

	assertSignalObserverState(t, store, "openai", now, StateQuotaExhausted, now.Add(7*time.Minute))
}

func TestSignalObserverReportedZeroUsesResetTime(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	reset := now.Add(23 * time.Minute)

	tests := []struct {
		name     string
		provider string
		tokens   int64
		requests int64
	}{
		{name: "tokens", provider: "token-zero", tokens: 0, requests: 9},
		{name: "requests", provider: "request-zero", tokens: 99, requests: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStateStore()
			observe := NewSignalObserver(store, tt.provider, func() time.Time { return now })
			observe(quotaheaders.Signal{
				Present:           true,
				RemainingTokens:   tt.tokens,
				RemainingRequests: tt.requests,
				ResetTime:         reset,
			})

			assertSignalObserverState(t, store, tt.provider, now, StateQuotaExhausted, reset)
		})
	}
}

func TestSignalObserverExhaustedWithoutResetUsesOneMinuteFallback(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 123, time.UTC)
	store := NewStateStore()
	observe := NewSignalObserver(store, "anthropic", func() time.Time { return now })

	observe(quotaheaders.Signal{
		Present:           true,
		RemainingTokens:   0,
		RemainingRequests: -1,
	})

	assertSignalObserverState(t, store, "anthropic", now, StateQuotaExhausted, now.Add(time.Minute))
}

func TestSignalObserverPositiveEvidenceMarksAvailable(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		tokens   int64
		requests int64
	}{
		{name: "positive tokens", tokens: 1, requests: -1},
		{name: "positive requests", tokens: -1, requests: 1},
		{name: "both positive", tokens: 1, requests: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStateStore()
			store.MarkQuotaExhausted("openrouter", now.Add(time.Hour))
			observe := NewSignalObserver(store, "openrouter", func() time.Time { return now })
			observe(quotaheaders.Signal{
				Present:           true,
				RemainingTokens:   tt.tokens,
				RemainingRequests: tt.requests,
				ResetTime:         now.Add(10 * time.Minute),
			})

			assertSignalObserverState(t, store, "openrouter", now, StateAvailable, time.Time{})
			if got := store.AllExhausted(); got != nil {
				t.Fatalf("AllExhausted() = %v, want nil after positive evidence", got)
			}
		})
	}
}

func TestSignalObserverInconclusiveEvidencePreservesState(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	retryAfter := now.Add(time.Hour)
	tests := []struct {
		name   string
		signal quotaheaders.Signal
	}{
		{
			name: "absent",
			signal: quotaheaders.Signal{
				RemainingTokens:   100,
				RemainingRequests: 100,
			},
		},
		{
			name: "reset only",
			signal: quotaheaders.Signal{
				Present:           true,
				RemainingTokens:   -1,
				RemainingRequests: -1,
				ResetTime:         now.Add(15 * time.Minute),
			},
		},
		{
			name: "all unknown",
			signal: quotaheaders.Signal{
				Present:           true,
				RemainingTokens:   -1,
				RemainingRequests: -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStateStore()
			store.MarkQuotaExhausted("openai", retryAfter)
			observe := NewSignalObserver(store, "openai", func() time.Time { return now })
			observe(tt.signal)

			assertSignalObserverState(t, store, "openai", now, StateQuotaExhausted, retryAfter)
		})
	}
}

func TestSignalObserverContradictoryEvidenceExhausts(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	reset := now.Add(20 * time.Minute)
	store := NewStateStore()
	observe := NewSignalObserver(store, "openai", func() time.Time { return now })

	observe(quotaheaders.Signal{
		Present:           true,
		RemainingTokens:   200,
		RemainingRequests: 0,
		ResetTime:         reset,
	})

	assertSignalObserverState(t, store, "openai", now, StateQuotaExhausted, reset)
}

func TestSignalObserverNilClockUsesTimeNow(t *testing.T) {
	store := NewStateStore()
	observe := NewSignalObserver(store, "openai", nil)
	before := time.Now()
	observe(quotaheaders.Signal{
		Present:           true,
		RemainingTokens:   -1,
		RemainingRequests: -1,
		RetryAfter:        time.Hour,
	})
	after := time.Now()

	state, retryAfter := store.State("openai", before)
	if state != StateQuotaExhausted {
		t.Fatalf("state = %q, want %q", state, StateQuotaExhausted)
	}
	if retryAfter.Before(before.Add(time.Hour)) || retryAfter.After(after.Add(time.Hour)) {
		t.Fatalf("retryAfter = %v, want between %v and %v", retryAfter, before.Add(time.Hour), after.Add(time.Hour))
	}
}

func assertSignalObserverState(t *testing.T, store *StateStore, provider string, now time.Time, wantState State, wantRetry time.Time) {
	t.Helper()
	gotState, gotRetry := store.State(provider, now)
	if gotState != wantState {
		t.Fatalf("State(%q) = %q, want %q", provider, gotState, wantState)
	}
	if !gotRetry.Equal(wantRetry) {
		t.Fatalf("State(%q) retryAfter = %v, want %v", provider, gotRetry, wantRetry)
	}
}
