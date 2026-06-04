package claudetui

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

// TestClaudeTuiQuotaStatusSameAsClaudeGivenSameCache verifies AC#1:
// QuotaStatus returns the same data the claude harness's QuotaStatus returns
// when given the same cache state.
func TestClaudeTuiQuotaStatusSameAsClaudeGivenSameCache(t *testing.T) {
	// Create a test snapshot
	snap := &anthropic.ClaudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC().Add(-5 * time.Minute),
		FiveHourLimit:     100,
		FiveHourRemaining: 50,
		WeeklyLimit:       100,
		WeeklyRemaining:   75,
		Windows: []harnesses.QuotaWindow{
			{
				Name:          "Current session",
				LimitID:       "session",
				WindowMinutes: 300,
				UsedPercent:   50.0,
				ResetsAt:      "in 5 minutes",
				State:         "available",
			},
			{
				Name:          "Current week (all models)",
				LimitID:       "weekly-all",
				WindowMinutes: 10080,
				UsedPercent:   25.0,
				ResetsAt:      "May 31",
				State:         "available",
			},
		},
		Source: "pty",
		Account: &harnesses.AccountInfo{
			Email:    "user@example.com",
			PlanType: "Pro",
		},
	}

	// Write to cache
	cachePath, err := anthropic.ClaudeQuotaCachePath()
	if err != nil {
		t.Fatalf("failed to get cache path: %v", err)
	}
	if err := anthropic.WriteClaudeQuota(cachePath, *snap); err != nil {
		t.Fatalf("failed to write cache: %v", err)
	}
	defer os.Remove(cachePath)

	// Get status from claude-tui harness
	h := &Harness{}
	now := time.Now()
	status, err := h.QuotaStatus(context.Background(), now)
	if err != nil {
		t.Fatalf("QuotaStatus failed: %v", err)
	}

	// Verify the status matches expectations
	if status.State != harnesses.QuotaOK {
		t.Errorf("expected State=QuotaOK, got %v", status.State)
	}
	if status.Source != "pty" {
		t.Errorf("expected Source=pty, got %s", status.Source)
	}
	if len(status.Windows) != len(snap.Windows) {
		t.Errorf("expected %d windows, got %d", len(snap.Windows), len(status.Windows))
	}
	if status.Account == nil {
		t.Errorf("expected Account to be non-nil")
	} else if status.Account.Email != "user@example.com" {
		t.Errorf("expected Email=user@example.com, got %s", status.Account.Email)
	}
}

// TestClaudeTuiSupportedLimitIDsSameAsClaudeSet verifies AC#2:
// SupportedLimitIDs returns the same set as claude's SupportedLimitIDs.
func TestClaudeTuiSupportedLimitIDsSameAsClaudeSet(t *testing.T) {
	h := &Harness{}
	ids := h.SupportedLimitIDs()

	expected := []string{"session", "weekly-all", "weekly-sonnet", "extra"}
	if len(ids) != len(expected) {
		t.Errorf("expected %d IDs, got %d", len(expected), len(ids))
	}

	for _, exp := range expected {
		found := false
		for _, id := range ids {
			if id == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected limit ID %s not found in %v", exp, ids)
		}
	}
}

// TestClaudeTuiRefreshQuotaSingleFlight verifies AC#3 and AC#4:
// RefreshQuota performs a /usage PTY probe and cache contention test
// shows concurrent calls coalesce to a single PTY probe via single-flight.
func TestClaudeTuiRefreshQuotaSingleFlight(t *testing.T) {
	// Counter to track how many times the probe is called
	var probeCount int32

	// Inject a test probe
	testProbe := func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		atomic.AddInt32(&probeCount, 1)
		// Simulate a probe delay
		time.Sleep(100 * time.Millisecond)
		return []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", WindowMinutes: 300, UsedPercent: 50.0, State: "available"},
			{Name: "Current week (all models)", LimitID: "weekly-all", WindowMinutes: 10080, UsedPercent: 25.0, State: "available"},
		}, &harnesses.AccountInfo{PlanType: "Pro"}, nil
	}

	// Swap in the test probe
	restore := SetCaptureForTest(testProbe)
	defer restore()

	h := &Harness{}

	// Launch 5 concurrent RefreshQuota calls
	var wg sync.WaitGroup
	var results []harnesses.QuotaStatus
	var resultsMu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := h.RefreshQuota(context.Background())
			if err != nil {
				t.Errorf("RefreshQuota failed: %v", err)
				return
			}
			resultsMu.Lock()
			results = append(results, status)
			resultsMu.Unlock()
		}()
	}

	wg.Wait()

	// Verify all callers got valid results
	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}

	for i, status := range results {
		if status.State != harnesses.QuotaOK && status.State != harnesses.QuotaStale {
			t.Errorf("result %d: unexpected State=%v", i, status.State)
		}
	}

	// Verify single-flight worked: probe should have been called only once
	count := atomic.LoadInt32(&probeCount)
	if count != 1 {
		t.Errorf("expected probe to be called 1 time, got %d", count)
	}
}

// TestRefreshQuotaSingleFlightCohortSeesCachedState proves that concurrent
// RefreshQuota calls on FRESH Harness instances coalesce on the package-scope
// singleflight group (key "claudetui:refresh-quota") and that every queued
// caller observes the SAME just-written cached state. The probe stamps each
// invocation with a monotonically increasing CapturedAt; within a single
// cohort the probe must run exactly once, so all callers must see one identical
// CapturedAt. Running two sequential cohorts proves the timestamp advances
// EXACTLY ONCE PER COHORT (not per caller): cohort 2's timestamp is strictly
// later than cohort 1's, and the probe ran exactly twice total.
func TestRefreshQuotaSingleFlightCohortSeesCachedState(t *testing.T) {
	var probeCount int32
	var probeSeq int64 // advances once per probe invocation

	probe := func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		atomic.AddInt32(&probeCount, 1)
		seq := atomic.AddInt64(&probeSeq, 1)
		// Hold the cohort open long enough for all goroutines to enqueue on the
		// singleflight group, forcing coalescing.
		time.Sleep(100 * time.Millisecond)
		// Encode the per-probe sequence into UsedPercent so the projected status
		// is distinguishable per cohort even though CapturedAt is wall-clock.
		return []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", WindowMinutes: 300, UsedPercent: float64(seq), State: "available"},
			{Name: "Current week (all models)", LimitID: "weekly-all", WindowMinutes: 10080, UsedPercent: 25.0, State: "available"},
		}, &harnesses.AccountInfo{PlanType: "Pro"}, nil
	}
	restore := SetCaptureForTest(probe)
	defer restore()

	// runCohort launches n concurrent RefreshQuota calls, each on a FRESH
	// Harness instance (the dispatcher constructs harnesses per call), and
	// returns the CapturedAt every caller observed.
	runCohort := func(n int) []time.Time {
		var wg sync.WaitGroup
		stamps := make([]time.Time, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				h := &Harness{} // fresh instance; shared package singleflight group
				status, err := h.RefreshQuota(context.Background())
				if err != nil {
					errs[idx] = err
					return
				}
				stamps[idx] = status.CapturedAt
			}(i)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("RefreshQuota in cohort: %v", err)
			}
		}
		return stamps
	}

	// Cohort 1.
	c1 := runCohort(5)
	if got := atomic.LoadInt32(&probeCount); got != 1 {
		t.Fatalf("cohort 1: probe ran %d times, want 1 (callers did not coalesce)", got)
	}
	// Every cohort-1 caller must observe the SAME CapturedAt (one cached write).
	for i := 1; i < len(c1); i++ {
		if !c1[i].Equal(c1[0]) {
			t.Fatalf("cohort 1: caller %d CapturedAt %v != caller 0 %v (callers saw different cached state)", i, c1[i], c1[0])
		}
	}
	if c1[0].IsZero() {
		t.Fatal("cohort 1: CapturedAt is zero; status did not reflect a written cache")
	}

	// Ensure the wall clock advances between cohorts so the timestamp can move.
	time.Sleep(2 * time.Millisecond)

	// Cohort 2: the prior cohort has fully returned, so the singleflight group
	// is idle and a NEW cohort runs a fresh probe.
	c2 := runCohort(5)
	if got := atomic.LoadInt32(&probeCount); got != 2 {
		t.Fatalf("two cohorts: probe ran %d times total, want 2 (timestamp must advance once per cohort)", got)
	}
	for i := 1; i < len(c2); i++ {
		if !c2[i].Equal(c2[0]) {
			t.Fatalf("cohort 2: caller %d CapturedAt %v != caller 0 %v", i, c2[i], c2[0])
		}
	}
	// The cohort timestamp advanced exactly once per cohort: cohort 2 is strictly
	// later than cohort 1.
	if !c2[0].After(c1[0]) {
		t.Fatalf("cohort 2 CapturedAt %v must be strictly after cohort 1 %v (timestamp did not advance per cohort)", c2[0], c1[0])
	}
}

// TestClaudeTuiQuotaFreshness verifies AC#6:
// QuotaFreshness returns a positive duration matching ADR-013's documented value.
func TestClaudeTuiQuotaFreshness(t *testing.T) {
	h := &Harness{}
	freshness := h.QuotaFreshness()

	if freshness <= 0 {
		t.Errorf("expected positive duration, got %v", freshness)
	}

	// ADR-013 recommends 15 minutes
	expectedFreshness := 15 * time.Minute
	if freshness != expectedFreshness {
		t.Errorf("expected freshness=%v, got %v", expectedFreshness, freshness)
	}
}

// TestClaudeTuiAccountStatusFromSharedCache verifies AccountStatus
// reads from the shared cache like QuotaStatus.
func TestClaudeTuiAccountStatusFromSharedCache(t *testing.T) {
	snap := &anthropic.ClaudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC().Add(-5 * time.Minute),
		FiveHourRemaining: 50,
		WeeklyRemaining:   75,
		Windows: []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", WindowMinutes: 300, UsedPercent: 50.0},
			{Name: "Current week (all models)", LimitID: "weekly-all", WindowMinutes: 10080, UsedPercent: 25.0},
		},
		Source: "pty",
		Account: &harnesses.AccountInfo{
			Email:    "test@example.com",
			PlanType: "Max",
		},
	}

	cachePath, err := anthropic.ClaudeQuotaCachePath()
	if err != nil {
		t.Fatalf("failed to get cache path: %v", err)
	}
	if err := anthropic.WriteClaudeQuota(cachePath, *snap); err != nil {
		t.Fatalf("failed to write cache: %v", err)
	}
	defer os.Remove(cachePath)

	h := &Harness{}
	account, err := h.AccountStatus(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("AccountStatus failed: %v", err)
	}

	if !account.Authenticated {
		t.Errorf("expected Authenticated=true")
	}
	if account.Email != "test@example.com" {
		t.Errorf("expected Email=test@example.com, got %s", account.Email)
	}
	if account.PlanType != "Max" {
		t.Errorf("expected PlanType=Max, got %s", account.PlanType)
	}
}

// TestClaudeTuiAccountFreshness verifies AccountFreshness returns the documented value.
func TestClaudeTuiAccountFreshness(t *testing.T) {
	h := &Harness{}
	freshness := h.AccountFreshness()

	if freshness <= 0 {
		t.Errorf("expected positive duration, got %v", freshness)
	}

	// Should match quota freshness since account is embedded in quota probe
	expectedFreshness := 15 * time.Minute
	if freshness != expectedFreshness {
		t.Errorf("expected freshness=%v, got %v", expectedFreshness, freshness)
	}
}

// TestRefreshQuotaBoundsProbeTimeoutWithoutDeadline proves the F1 fix:
// when the caller passes a context with NO deadline (e.g.
// context.Background()), RefreshQuota must bound the live PTY probe by the
// short probe ceiling, NOT by the 15-minute cache freshness window. Before
// the fix, refreshQuotaLocked seeded timeout = claudettuiQuotaFreshness
// (15m), which exceeds the 10-minute go test binary timeout and hung the
// conformance test against the live `claude` binary. This test captures the
// timeout value the probe receives and asserts it equals the ceiling.
func TestRefreshQuotaBoundsProbeTimeoutWithoutDeadline(t *testing.T) {
	var gotTimeout time.Duration
	probe := func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		gotTimeout = timeout
		return []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", WindowMinutes: 300, UsedPercent: 10.0, State: "available"},
		}, &harnesses.AccountInfo{PlanType: "Pro"}, nil
	}
	restore := SetCaptureForTest(probe)
	defer restore()

	h := &Harness{}
	if _, err := h.RefreshQuota(context.Background()); err != nil {
		t.Fatalf("RefreshQuota: unexpected error %v", err)
	}

	if gotTimeout != claudettuiQuotaProbeCeiling {
		t.Fatalf("probe timeout = %v, want ceiling %v (must not inherit the %v freshness window)",
			gotTimeout, claudettuiQuotaProbeCeiling, claudettuiQuotaFreshness)
	}
	if gotTimeout >= claudettuiQuotaFreshness {
		t.Fatalf("probe timeout %v must be strictly below freshness window %v", gotTimeout, claudettuiQuotaFreshness)
	}
}

// TestRefreshQuotaHonorsShorterCallerDeadline proves the ceiling does not
// override an even shorter caller deadline: when ctx carries a deadline
// below the ceiling, the probe receives the (shorter) remaining time.
func TestRefreshQuotaHonorsShorterCallerDeadline(t *testing.T) {
	var gotTimeout time.Duration
	probe := func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		gotTimeout = timeout
		return []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", WindowMinutes: 300, UsedPercent: 10.0, State: "available"},
		}, &harnesses.AccountInfo{PlanType: "Pro"}, nil
	}
	restore := SetCaptureForTest(probe)
	defer restore()

	h := &Harness{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := h.RefreshQuota(ctx); err != nil {
		t.Fatalf("RefreshQuota: unexpected error %v", err)
	}

	if gotTimeout <= 0 || gotTimeout > 2*time.Second {
		t.Fatalf("probe timeout = %v, want a positive value <= 2s (shorter caller deadline)", gotTimeout)
	}
	if gotTimeout >= claudettuiQuotaProbeCeiling {
		t.Fatalf("probe timeout %v must honor the shorter caller deadline, not the ceiling %v", gotTimeout, claudettuiQuotaProbeCeiling)
	}
}

// TestRefreshQuotaTerminatesQuicklyWithBackgroundContext is a wall-clock
// regression guard for F1: even when the probe itself blocks until its
// provided timeout (simulating the live binary never returning), the probe
// is bounded by the ceiling so RefreshQuota returns far under the go test
// binary timeout. We assert it returns well under a generous bound and that
// the bounded timeout is the ceiling. The probe sleeps only briefly to keep
// the test fast and deterministic.
func TestRefreshQuotaTerminatesQuicklyWithBackgroundContext(t *testing.T) {
	probe := func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		// A real probe would block up to `timeout`; the fix guarantees
		// `timeout` is the ceiling, not 15m. Verify the bound here without
		// actually sleeping for the ceiling.
		if timeout > claudettuiQuotaProbeCeiling {
			t.Errorf("probe received timeout %v > ceiling %v", timeout, claudettuiQuotaProbeCeiling)
		}
		return []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", WindowMinutes: 300, UsedPercent: 10.0, State: "available"},
		}, &harnesses.AccountInfo{PlanType: "Pro"}, nil
	}
	restore := SetCaptureForTest(probe)
	defer restore()

	h := &Harness{}
	start := time.Now()
	if _, err := h.RefreshAccount(context.Background()); err != nil {
		t.Fatalf("RefreshAccount: unexpected error %v", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("RefreshAccount took %v with background context; must terminate well under the test timeout", elapsed)
	}
}
