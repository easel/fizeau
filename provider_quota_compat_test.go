package fizeau_test

import (
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func TestProviderQuotaPublicWrappersRemainSourceCompatible(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	retryAt := now.Add(time.Hour)

	store := fizeau.NewProviderQuotaStateStore()
	store.MarkQuotaExhausted("openai", retryAt)
	state, gotRetryAt := store.State("openai", now)
	if state != fizeau.ProviderQuotaStateQuotaExhausted {
		t.Fatalf("state = %q, want quota_exhausted", state)
	}
	if !gotRetryAt.Equal(retryAt) {
		t.Fatalf("retry_at = %v, want %v", gotRetryAt, retryAt)
	}
	if got := store.ExhaustedAt(now); len(got) != 1 {
		t.Fatalf("ExhaustedAt size = %d, want 1: %v", len(got), got)
	}

	store.MarkAvailable("openai")
	state, _ = store.State("openai", now)
	if state != fizeau.ProviderQuotaStateAvailable {
		t.Fatalf("state after MarkAvailable = %q, want available", state)
	}
}

func TestProviderBurnRatePublicWrapperRemainsSourceCompatible(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	tracker := fizeau.NewProviderBurnRateTracker()
	tracker.SetBudget("openai", 100)
	if got := tracker.Budget("openai"); got != 100 {
		t.Fatalf("Budget = %d, want 100", got)
	}
	exhausted, retryAt := tracker.Record("openai", 200, now)
	if !exhausted {
		t.Fatal("Record = exhausted false, want true")
	}
	if got := tracker.Used("openai", now); got != 200 {
		t.Fatalf("Used = %d, want 200", got)
	}
	if retryAt.IsZero() || !retryAt.After(now) {
		t.Fatalf("retryAt = %v, want a future reset time", retryAt)
	}
	tracker.Reset()
	if got := tracker.Used("openai", now); got != 0 {
		t.Fatalf("Used after Reset = %d, want 0", got)
	}
}
