package routehealth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/harnesses"
)

type primaryRefreshTestHarness struct {
	name string

	mu          sync.Mutex
	status      harnesses.QuotaStatus
	refreshes   int
	refreshFunc func(context.Context) error
	sf          singleflight.Group
}

func newPrimaryRefreshTestHarness(name string, status harnesses.QuotaStatus) *primaryRefreshTestHarness {
	return &primaryRefreshTestHarness{name: name, status: status}
}

func (h *primaryRefreshTestHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: h.name, Type: "subprocess", Available: true}
}

func (h *primaryRefreshTestHarness) HealthCheck(ctx context.Context) error { return ctx.Err() }

func (h *primaryRefreshTestHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return nil, errors.New("primaryRefreshTestHarness: Execute not supported")
}

func (h *primaryRefreshTestHarness) QuotaStatus(ctx context.Context, _ time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status, nil
}

func (h *primaryRefreshTestHarness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	value, err, _ := h.sf.Do("refresh", func() (any, error) {
		h.mu.Lock()
		h.refreshes++
		refresh := h.refreshFunc
		h.mu.Unlock()
		if refresh != nil {
			if err := refresh(ctx); err != nil {
				return harnesses.QuotaStatus{}, err
			}
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.status, nil
	})
	if err != nil {
		return harnesses.QuotaStatus{}, err
	}
	return value.(harnesses.QuotaStatus), nil
}

func (h *primaryRefreshTestHarness) QuotaFreshness() time.Duration { return 30 * time.Minute }

func (h *primaryRefreshTestHarness) SupportedLimitIDs() []string { return nil }

func (h *primaryRefreshTestHarness) setStatus(status harnesses.QuotaStatus) {
	h.mu.Lock()
	h.status = status
	h.mu.Unlock()
}

func (h *primaryRefreshTestHarness) setRefreshFunc(fn func(context.Context) error) {
	h.mu.Lock()
	h.refreshFunc = fn
	h.mu.Unlock()
}

func (h *primaryRefreshTestHarness) refreshCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refreshes
}

func newPrimaryRefreshTestScheduler(
	clock schedulerClock,
	state *primaryQuotaRefreshState,
	policy PrimaryQuotaRefreshPolicy,
	harnessByName map[string]harnesses.Harness,
) *RefreshScheduler {
	scheduler := newRefreshScheduler(func(name string) harnesses.Harness {
		return harnessByName[name]
	}, nil, clock)
	scheduler.primary.state = state
	scheduler.ConfigurePrimaryQuotaRefresh(policy)
	return scheduler
}

func TestPrimaryQuotaRefreshDebouncesAcrossSchedulers(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	state := newPrimaryQuotaRefreshState()
	status := harnesses.QuotaStatus{
		State:             harnesses.QuotaOK,
		CapturedAt:        start,
		Fresh:             true,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	}
	claude := newPrimaryRefreshTestHarness("claude", status)
	codex := newPrimaryRefreshTestHarness("codex", status)
	harnessByName := map[string]harnesses.Harness{"claude": claude, "codex": codex}
	policy := PrimaryQuotaRefreshPolicy{
		Debounce:           time.Hour,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Hour,
	}

	first := newPrimaryRefreshTestScheduler(clock, state, policy, harnessByName)
	second := newPrimaryRefreshTestScheduler(clock, state, policy, harnessByName)
	first.Start(context.Background())
	second.Start(context.Background())
	t.Cleanup(first.Stop)
	t.Cleanup(second.Stop)

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	claude.setRefreshFunc(func(ctx context.Context) error {
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	claude.setStatus(harnesses.QuotaStatus{State: harnesses.QuotaUnavailable})

	firstDone := first.primary.request(context.Background(), "claude")
	if firstDone == nil {
		t.Fatal("first request was unexpectedly rejected")
	}
	<-started
	if secondDone := second.primary.request(context.Background(), "claude"); secondDone != nil {
		t.Fatal("second scheduler bypassed process-global in-flight debounce")
	}
	close(release)
	<-firstDone

	if done := second.primary.request(context.Background(), "claude"); done != nil {
		t.Fatal("second scheduler bypassed process-global time debounce")
	}
	if got := claude.refreshCount(); got != 1 {
		t.Fatalf("refresh count=%d want 1", got)
	}

	clock.Advance(time.Hour)
	done := second.primary.request(context.Background(), "claude")
	if done == nil {
		t.Fatal("request remained debounced at the interval boundary")
	}
	<-done
	if got := claude.refreshCount(); got != 2 {
		t.Fatalf("refresh count after debounce=%d want 2", got)
	}
}

func TestPrimaryQuotaRefreshStartupWaitIsBoundedAndRefreshContinues(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	started := make(chan struct{})
	release := make(chan struct{})
	claude := newPrimaryRefreshTestHarness("claude", harnesses.QuotaStatus{State: harnesses.QuotaUnavailable})
	claude.setRefreshFunc(func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	codex := newPrimaryRefreshTestHarness("codex", harnesses.QuotaStatus{
		State:             harnesses.QuotaOK,
		Fresh:             true,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	})
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           time.Hour,
		StartupWait:        time.Hour,
		ClaudeProbeTimeout: time.Hour,
	}, map[string]harnesses.Harness{"claude": claude, "codex": codex})
	deadline := make(chan time.Time, 1)
	scheduler.primary.after = func(time.Duration) <-chan time.Time { return deadline }

	startReturned := make(chan struct{})
	go func() {
		scheduler.Start(context.Background())
		close(startReturned)
	}()
	<-started
	select {
	case <-startReturned:
		t.Fatal("Start returned before the bounded startup deadline")
	default:
	}
	deadline <- clock.Now().Add(time.Hour)
	<-startReturned

	if got := claude.refreshCount(); got != 1 {
		t.Fatalf("refresh count at startup timeout=%d want 1", got)
	}
	close(release)
	scheduler.Stop()
	if got := claude.refreshCount(); got != 1 {
		t.Fatalf("refresh count after completion=%d want 1", got)
	}
}

func TestPrimaryQuotaRefreshConfiguredTimerUsesDebouncedPath(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	fresh := harnesses.QuotaStatus{
		State:             harnesses.QuotaOK,
		CapturedAt:        start,
		Fresh:             true,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	}
	claude := newPrimaryRefreshTestHarness("claude", fresh)
	codex := newPrimaryRefreshTestHarness("codex", fresh)
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           time.Minute,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Hour,
		Interval:           30 * time.Second,
	}, map[string]harnesses.Harness{"claude": claude, "codex": codex})
	notify := make(chan struct{}, 4)
	scheduler.primary.timerNotify = notify
	scheduler.Start(context.Background())
	t.Cleanup(scheduler.Stop)
	claude.setStatus(harnesses.QuotaStatus{State: harnesses.QuotaUnavailable})

	clock.Advance(30 * time.Second)
	<-notify
	waitForPrimaryRefreshCount(t, claude, 1)

	clock.Advance(30 * time.Second)
	<-notify
	if got := claude.refreshCount(); got != 1 {
		t.Fatalf("refresh count inside debounce=%d want 1", got)
	}

	clock.Advance(30 * time.Second)
	<-notify
	waitForPrimaryRefreshCount(t, claude, 2)
}

func TestPrimaryQuotaCacheStatusPreservesHarnessRules(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	claude := newPrimaryRefreshTestHarness("claude", harnesses.QuotaStatus{})
	codex := newPrimaryRefreshTestHarness("codex", harnesses.QuotaStatus{})
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           15 * time.Minute,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Second,
	}, map[string]harnesses.Harness{"claude": claude, "codex": codex})

	tests := []struct {
		name    string
		harness *primaryRefreshTestHarness
		status  harnesses.QuotaStatus
		want    PrimaryQuotaCacheStatus
	}{
		{
			name:    "claude unavailable",
			harness: claude,
			status:  harnesses.QuotaStatus{State: harnesses.QuotaUnavailable},
			want:    PrimaryQuotaCacheStatus{NeedsRefresh: true},
		},
		{
			name:    "claude fresh",
			harness: claude,
			status:  harnesses.QuotaStatus{State: harnesses.QuotaOK, CapturedAt: start.Add(-time.Minute)},
			want:    PrimaryQuotaCacheStatus{Usable: true},
		},
		{
			name:    "claude stale",
			harness: claude,
			status:  harnesses.QuotaStatus{State: harnesses.QuotaStale, CapturedAt: start.Add(-15 * time.Minute)},
			want:    PrimaryQuotaCacheStatus{NeedsRefresh: true},
		},
		{
			name:    "claude zero capture preserves legacy usable decision",
			harness: claude,
			status:  harnesses.QuotaStatus{State: harnesses.QuotaOK},
			want:    PrimaryQuotaCacheStatus{Usable: true},
		},
		{
			name:    "codex unavailable",
			harness: codex,
			status:  harnesses.QuotaStatus{State: harnesses.QuotaUnavailable},
			want:    PrimaryQuotaCacheStatus{NeedsRefresh: true},
		},
		{
			name:    "codex available",
			harness: codex,
			status: harnesses.QuotaStatus{
				State:             harnesses.QuotaOK,
				Fresh:             true,
				RoutingPreference: harnesses.RoutingPreferenceAvailable,
			},
			want: PrimaryQuotaCacheStatus{Usable: true},
		},
		{
			name:    "codex stale",
			harness: codex,
			status: harnesses.QuotaStatus{
				State:             harnesses.QuotaStale,
				Fresh:             false,
				RoutingPreference: harnesses.RoutingPreferenceBlocked,
			},
			want: PrimaryQuotaCacheStatus{NeedsRefresh: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.harness.setStatus(test.status)
			got := scheduler.PrimaryQuotaCacheStatus(context.Background(), test.harness.name)
			if got != test.want {
				t.Fatalf("status=%+v want %+v", got, test.want)
			}
		})
	}
}

func TestRefreshPrimaryQuotaAppliesClaudeTimeoutOnly(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	claude := newPrimaryRefreshTestHarness("claude", harnesses.QuotaStatus{})
	codex := newPrimaryRefreshTestHarness("codex", harnesses.QuotaStatus{})
	var claudeHasDeadline, codexHasDeadline bool
	claude.setRefreshFunc(func(ctx context.Context) error {
		_, claudeHasDeadline = ctx.Deadline()
		return nil
	})
	codex.setRefreshFunc(func(ctx context.Context) error {
		_, codexHasDeadline = ctx.Deadline()
		return nil
	})
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           time.Minute,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Hour,
	}, map[string]harnesses.Harness{"claude": claude, "codex": codex})

	scheduler.RefreshPrimaryQuota(context.Background(), "claude")
	scheduler.RefreshPrimaryQuota(context.Background(), "codex")
	if !claudeHasDeadline {
		t.Fatal("Claude refresh context did not receive the configured probe timeout")
	}
	if codexHasDeadline {
		t.Fatal("Codex refresh context unexpectedly received a scheduler deadline")
	}
}

func TestRefreshPrimaryQuotaForHealthCheckUsesDiagnosticDebounce(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	claude := newPrimaryRefreshTestHarness("claude", harnesses.QuotaStatus{
		State:      harnesses.QuotaOK,
		CapturedAt: start.Add(-30 * time.Second),
	})
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           time.Millisecond,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Hour,
	}, map[string]harnesses.Harness{"claude": claude})

	if status := scheduler.PrimaryQuotaCacheStatus(context.Background(), "claude"); !status.NeedsRefresh {
		t.Fatal("activity policy did not honor its configured one-millisecond debounce")
	}
	scheduler.RefreshPrimaryQuotaForHealthCheck(context.Background(), "claude")
	if got := claude.refreshCount(); got != 0 {
		t.Fatalf("fresh diagnostic cache triggered refresh: %d", got)
	}

	claude.setStatus(harnesses.QuotaStatus{
		State:      harnesses.QuotaStale,
		CapturedAt: start.Add(-defaultPrimaryQuotaRefreshDebounce),
	})
	scheduler.RefreshPrimaryQuotaForHealthCheck(context.Background(), "claude")
	if got := claude.refreshCount(); got != 1 {
		t.Fatalf("stale diagnostic cache refresh count=%d want 1", got)
	}
}

func TestPrimaryQuotaRefreshStopCancelsAndJoinsActivityRequest(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	fresh := harnesses.QuotaStatus{
		State:             harnesses.QuotaOK,
		CapturedAt:        start,
		Fresh:             true,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	claude := newPrimaryRefreshTestHarness("claude", fresh)
	claude.setRefreshFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	})
	codex := newPrimaryRefreshTestHarness("codex", fresh)
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           time.Minute,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Hour,
	}, map[string]harnesses.Harness{"claude": claude, "codex": codex})
	scheduler.Start(context.Background())
	claude.setStatus(harnesses.QuotaStatus{State: harnesses.QuotaUnavailable})
	scheduler.RequestPrimaryQuotaRefresh(context.Background())
	<-started

	stopReturned := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopReturned)
	}()
	<-finished
	<-stopReturned
	if got := claude.refreshCount(); got != 1 {
		t.Fatalf("refresh count=%d want 1", got)
	}
}

func TestPrimaryQuotaRefreshCancelledTimerContextAllowsActivityRefresh(t *testing.T) {
	start := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	claude := newPrimaryRefreshTestHarness("claude", harnesses.QuotaStatus{State: harnesses.QuotaUnavailable})
	codex := newPrimaryRefreshTestHarness("codex", harnesses.QuotaStatus{
		State:             harnesses.QuotaOK,
		Fresh:             true,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	})
	scheduler := newPrimaryRefreshTestScheduler(clock, newPrimaryQuotaRefreshState(), PrimaryQuotaRefreshPolicy{
		Debounce:           time.Minute,
		StartupWait:        time.Second,
		ClaudeProbeTimeout: time.Hour,
		Interval:           time.Second,
	}, map[string]harnesses.Harness{"claude": claude, "codex": codex})

	timerCtx, cancelTimer := context.WithCancel(context.Background())
	cancelTimer()
	scheduler.Start(timerCtx)
	t.Cleanup(scheduler.Stop)
	if got := claude.refreshCount(); got != 0 {
		t.Fatalf("cancelled timer context unexpectedly refreshed at startup: %d", got)
	}

	scheduler.RequestPrimaryQuotaRefresh(context.Background())
	waitForPrimaryRefreshCount(t, claude, 1)
}

func waitForPrimaryRefreshCount(t *testing.T, harness *primaryRefreshTestHarness, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for harness.refreshCount() < want {
		if time.Now().After(deadline) {
			t.Fatalf("refresh count=%d want at least %d", harness.refreshCount(), want)
		}
		time.Sleep(time.Millisecond)
	}
}
