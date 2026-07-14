package quota

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecoveryPassElapsedRetryTriggersProbe(t *testing.T) {
	store := NewStateStore()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store.MarkQuotaExhausted("openai", now.Add(-time.Minute))

	var calls []string
	probe := func(_ context.Context, provider string) error {
		calls = append(calls, provider)
		return nil
	}
	backoffs := make(map[string]time.Duration)
	next := runRecoveryPass(context.Background(), store, probe, time.Minute, func() time.Time { return now }, backoffs)

	if len(calls) != 1 || calls[0] != "openai" {
		t.Fatalf("probe calls = %v, want [openai]", calls)
	}
	state, _ := store.State("openai", now)
	if state != StateAvailable {
		t.Fatalf("state after successful probe = %q, want %q", state, StateAvailable)
	}
	if next != time.Minute {
		t.Fatalf("next wake = %v, want fallback %v", next, time.Minute)
	}
}

func TestRecoveryPassFutureRetriesSkipProbeAndWakeEarliest(t *testing.T) {
	store := NewStateStore()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store.MarkQuotaExhausted("openai", now.Add(4*time.Minute))
	store.MarkQuotaExhausted("anthropic", now.Add(2*time.Minute))

	probeCalls := 0
	next := runRecoveryPass(
		context.Background(),
		store,
		func(context.Context, string) error {
			probeCalls++
			return nil
		},
		5*time.Minute,
		func() time.Time { return now },
		make(map[string]time.Duration),
	)

	if probeCalls != 0 {
		t.Fatalf("future retry entries triggered %d probes, want 0", probeCalls)
	}
	if next != 2*time.Minute {
		t.Fatalf("next wake = %v, want earliest retry %v", next, 2*time.Minute)
	}
}

func TestRecoveryPassSuccessClearsBackoff(t *testing.T) {
	store := NewStateStore()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store.MarkQuotaExhausted("openai", now.Add(-time.Second))
	backoffs := map[string]time.Duration{"openai": 20 * time.Minute}

	runRecoveryPass(
		context.Background(),
		store,
		func(context.Context, string) error { return nil },
		time.Hour,
		func() time.Time { return now },
		backoffs,
	)

	if _, ok := backoffs["openai"]; ok {
		t.Fatalf("successful probe retained backoff bookkeeping: %v", backoffs)
	}
	if got := store.AllExhausted(); got != nil {
		t.Fatalf("successful probe retained exhausted state: %v", got)
	}
}

func TestRecoveryPassFailureRetainsAndDoublesToCap(t *testing.T) {
	store := NewStateStore()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store.MarkQuotaExhausted("openai", now.Add(-time.Second))
	backoffs := make(map[string]time.Duration)
	wantBackoffs := []time.Duration{
		5 * time.Minute,
		10 * time.Minute,
		20 * time.Minute,
		40 * time.Minute,
		time.Hour,
		time.Hour,
	}

	for i, want := range wantBackoffs {
		next := runRecoveryPass(
			context.Background(),
			store,
			func(context.Context, string) error { return errors.New("still exhausted") },
			2*time.Hour,
			func() time.Time { return now },
			backoffs,
		)
		if next != want {
			t.Fatalf("pass %d next wake = %v, want %v", i+1, next, want)
		}
		if got := backoffs["openai"]; got != want {
			t.Fatalf("pass %d backoff = %v, want %v", i+1, got, want)
		}
		exhausted := store.AllExhausted()
		retryAfter, ok := exhausted["openai"]
		if !ok {
			t.Fatalf("pass %d removed failed provider from exhausted state: %v", i+1, exhausted)
		}
		wantRetry := now.Add(want)
		if !retryAfter.Equal(wantRetry) {
			t.Fatalf("pass %d retry_after = %v, want %v", i+1, retryAfter, wantRetry)
		}
		now = retryAfter.Add(time.Second)
	}
}

func TestRecoveryLoopEmptyStoreUsesConfiguredAndDefaultFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		fallback time.Duration
		want     time.Duration
	}{
		{name: "configured", fallback: 7 * time.Minute, want: 7 * time.Minute},
		{name: "zero defaults", fallback: 0, want: 5 * time.Minute},
		{name: "negative defaults", fallback: -time.Second, want: 5 * time.Minute},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var slept time.Duration
			RunRecoveryLoop(
				context.Background(),
				NewStateStore(),
				func(context.Context, string) error { return nil },
				RecoveryOptions{
					Fallback: test.fallback,
					Sleep: func(_ context.Context, duration time.Duration) bool {
						slept = duration
						return false
					},
				},
			)
			if slept != test.want {
				t.Fatalf("sleep duration = %v, want %v", slept, test.want)
			}
		})
	}
}

func TestRecoveryPassExternalAvailabilityClearsBookkeeping(t *testing.T) {
	store := NewStateStore()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	store.MarkQuotaExhausted("openai", now.Add(-time.Second))
	backoffs := make(map[string]time.Duration)
	fail := func(context.Context, string) error { return errors.New("still exhausted") }

	runRecoveryPass(context.Background(), store, fail, time.Hour, func() time.Time { return now }, backoffs)
	if got := backoffs["openai"]; got != 5*time.Minute {
		t.Fatalf("initial backoff = %v, want %v", got, 5*time.Minute)
	}

	store.MarkAvailable("openai")
	runRecoveryPass(context.Background(), store, fail, time.Hour, func() time.Time { return now }, backoffs)
	if _, ok := backoffs["openai"]; ok {
		t.Fatalf("external MarkAvailable retained backoff bookkeeping: %v", backoffs)
	}

	store.MarkQuotaExhausted("openai", now.Add(-time.Second))
	next := runRecoveryPass(context.Background(), store, fail, time.Hour, func() time.Time { return now }, backoffs)
	if next != 5*time.Minute {
		t.Fatalf("re-exhausted provider next wake = %v, want fresh initial backoff %v", next, 5*time.Minute)
	}
}

func TestRecoveryLoopExitsOnContextCancellation(t *testing.T) {
	store := NewStateStore()
	ctx, cancel := context.WithCancel(context.Background())
	sleeping := make(chan struct{})
	done := make(chan struct{})

	go func() {
		RunRecoveryLoop(
			ctx,
			store,
			func(context.Context, string) error { return nil },
			RecoveryOptions{Sleep: func(ctx context.Context, _ time.Duration) bool {
				close(sleeping)
				<-ctx.Done()
				return false
			}},
		)
		close(done)
	}()

	select {
	case <-sleeping:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery loop did not begin sleeping")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery loop did not exit after cancellation")
	}
}

func TestRecoveryLoopReturnsForInvalidInputs(t *testing.T) {
	probe := func(context.Context, string) error { return nil }
	RunRecoveryLoop(nil, NewStateStore(), probe, RecoveryOptions{})
	RunRecoveryLoop(context.Background(), nil, probe, RecoveryOptions{})
	RunRecoveryLoop(context.Background(), NewStateStore(), nil, RecoveryOptions{})
}
