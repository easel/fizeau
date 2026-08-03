package routehealth

import (
	"testing"
	"time"
)

// TestClearSoftDemotionAfterAuthSuccess (fizeau-0c5ae39c AC2): a prior soft
// demotion for a Claude route is cleared when a successful health path records
// attempt success for that harness.
func TestClearSoftDemotionAfterAuthSuccess(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	if err := store.RecordAttempt(Attempt{
		Harness:   "claude",
		Provider:  "anthropic",
		Model:     "claude-sonnet-4-6",
		Status:    "failed",
		Error:     "credential_invalid: OAuth session expired",
		Timestamp: now,
	}); err != nil {
		t.Fatal(err)
	}
	active := store.ActiveAttempts(now, time.Minute)
	if len(active) != 1 {
		t.Fatalf("active before clear = %d, want 1", len(active))
	}

	// Simulated re-auth success on health path: record success for harness.
	if err := store.RecordAttempt(Attempt{
		Harness:   "claude",
		Status:    "success",
		Timestamp: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	active = store.ActiveAttempts(now.Add(2*time.Second), time.Minute)
	if len(active) != 0 {
		t.Fatalf("active after re-auth success = %#v, want empty (demotion cleared)", active)
	}
}
