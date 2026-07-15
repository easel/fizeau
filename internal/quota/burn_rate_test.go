package quota

import (
	"testing"
	"time"
)

func midUTC(hour, minute int) time.Time {
	return time.Date(2026, 5, 2, hour, minute, 0, 0, time.UTC)
}

func TestBurnRateNoTransitionUnderBudget(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("claude-acct-1", 1_000_000)

	now := midUTC(12, 0)
	exhausted, retryAt := tr.Record("claude-acct-1", 100_000, now)
	if exhausted {
		t.Fatalf("expected no transition under budget, got exhausted=true (retryAt=%v)", retryAt)
	}
	if got := tr.Used("claude-acct-1", now); got != 100_000 {
		t.Errorf("Used = %d, want 100000", got)
	}
}

func TestBurnRatePredictedExhaustionTransitions(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("claude-acct-2", 1_000_000)

	now := midUTC(6, 0)
	exhausted, retryAt := tr.Record("claude-acct-2", 500_000, now)
	if !exhausted {
		t.Fatalf("expected predictive exhaustion at 6h/quarter-day with 500k of 1M budget")
	}
	wantRetry := utcDayStart(now).Add(24 * time.Hour)
	if !retryAt.Equal(wantRetry) {
		t.Errorf("retryAt = %v, want %v (next UTC day boundary)", retryAt, wantRetry)
	}
}

func TestBurnRateOverBudgetExhaustsImmediately(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("p", 1_000)
	now := midUTC(23, 30)
	exhausted, retryAt := tr.Record("p", 1_500, now)
	if !exhausted {
		t.Fatal("over-budget usage should exhaust regardless of projection")
	}
	if !retryAt.Equal(utcDayStart(now).Add(24 * time.Hour)) {
		t.Errorf("retryAt = %v, want next UTC midnight", retryAt)
	}
}

func TestBurnRateNoBudgetIsInert(t *testing.T) {
	tr := NewBurnRateTracker()
	now := midUTC(1, 0)
	exhausted, _ := tr.Record("unconfigured", 9_999_999, now)
	if exhausted {
		t.Fatal("provider without configured budget must never report exhaustion")
	}
	if got := tr.Used("unconfigured", now); got != 9_999_999 {
		t.Errorf("Used = %d, want 9999999", got)
	}
}

func TestBurnRateWindowResetClearsUsage(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("p", 1_000_000)

	day1 := midUTC(20, 0)
	exhausted, _ := tr.Record("p", 50_000, day1)
	if exhausted {
		t.Fatalf("setup: should be well under budget on day1")
	}
	if got := tr.Used("p", day1); got != 50_000 {
		t.Fatalf("day1 used = %d, want 50000", got)
	}

	day2 := day1.Add(8 * time.Hour)
	if got := tr.Used("p", day2); got != 0 {
		t.Errorf("after window reset, Used = %d, want 0", got)
	}
	exhausted, _ = tr.Record("p", 100, day2)
	if exhausted {
		t.Errorf("100 tokens at start of fresh day should not exhaust 1M budget")
	}
	if got := tr.Used("p", day2); got != 100 {
		t.Errorf("day2 used = %d, want 100", got)
	}
}

func TestBurnRateRetryAfterIsNextWindowStart(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("p", 100)
	now := time.Date(2026, 5, 2, 23, 59, 59, 0, time.UTC)
	exhausted, retryAt := tr.Record("p", 1_000, now)
	if !exhausted {
		t.Fatal("over-budget must exhaust")
	}
	want := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	if !retryAt.Equal(want) {
		t.Errorf("retryAt = %v, want %v", retryAt, want)
	}
}

func TestBurnRateSetBudgetZeroDisables(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("p", 1_000)
	tr.SetBudget("p", 0)
	if got := tr.Budget("p"); got != 0 {
		t.Errorf("Budget after zero = %d, want 0", got)
	}
	exhausted, _ := tr.Record("p", 10_000, midUTC(12, 0))
	if exhausted {
		t.Error("disabled provider must not signal exhaustion")
	}
}

func TestBurnRateConcurrentRecordSafe(t *testing.T) {
	tr := NewBurnRateTracker()
	tr.SetBudget("p", 1_000_000)
	now := midUTC(12, 0)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				tr.Record("p", 1, now)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := tr.Used("p", now); got != 800 {
		t.Errorf("Used after concurrent inserts = %d, want 800", got)
	}
}

func TestObserveTokenUsageCascadesToQuotaState(t *testing.T) {
	now := midUTC(12, 0)
	burn := NewBurnRateTracker()
	burn.SetBudget("openai", 100)
	store := NewStateStore()

	ObserveTokenUsage(burn, store, "openai", 200, now)

	state, retryAt := store.State("openai", now)
	if state != StateQuotaExhausted {
		t.Fatalf("State = %q, want quota_exhausted", state)
	}
	if retryAt.IsZero() || !retryAt.After(now) {
		t.Fatalf("retryAt = %v, want a future reset time", retryAt)
	}
}
