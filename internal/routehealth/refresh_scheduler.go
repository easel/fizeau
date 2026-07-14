package routehealth

import (
	"context"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// RefreshScheduler owns the single service-wide async refresh cadence across
// registered QuotaHarness and AccountHarness implementations.
type RefreshScheduler struct {
	lookup  func(name string) harnesses.Harness
	names   []string
	clock   schedulerClock
	primary *primaryQuotaRefreshCoordinator

	// tickNotify, when non-nil, receives the harness name after each tick is
	// fully processed. Tests use this to synchronize on tick completion.
	tickNotify chan<- string

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// schedulerClock is the time abstraction the refresh scheduler uses. The
// production implementation wraps time.Now / time.NewTicker; tests inject a
// controllable fake clock to drive cadence deterministically.
type schedulerClock interface {
	Now() time.Time
	NewTicker(d time.Duration) schedulerTicker
}

// schedulerTicker is a minimal time.Ticker abstraction.
type schedulerTicker interface {
	C() <-chan time.Time
	Stop()
}

// realClock is the wall-clock implementation of schedulerClock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTicker(d time.Duration) schedulerTicker {
	return &realTicker{t: time.NewTicker(d)}
}

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

// NewRefreshScheduler constructs a scheduler over the supplied harness names.
func NewRefreshScheduler(lookup func(string) harnesses.Harness, names []string) *RefreshScheduler {
	return newRefreshScheduler(lookup, names, nil)
}

func newRefreshScheduler(lookup func(string) harnesses.Harness, names []string, clock schedulerClock) *RefreshScheduler {
	if clock == nil {
		clock = realClock{}
	}
	scheduler := &RefreshScheduler{
		lookup: lookup,
		names:  append([]string(nil), names...),
		clock:  clock,
	}
	scheduler.primary = newPrimaryQuotaRefreshCoordinator(lookup, clock, processPrimaryQuotaRefreshState)
	return scheduler
}

// Start launches the scheduler. It panics if called twice without Stop.
func (s *RefreshScheduler) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		panic("refreshScheduler: Start called twice without intervening Stop")
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.mu.Unlock()

	// The primary coordinator owns the bounded startup check and optional
	// operator-configured timer. Its work is deliberately separate from the
	// freshness-derived quota/account cadence below: both paths call the same
	// harness contract, whose RefreshQuota/RefreshAccount methods own
	// single-flight.
	s.primary.start(s.ctx)

	now := s.clock.Now()
	for _, name := range s.names {
		h := s.lookup(name)
		if h == nil {
			continue
		}
		qh, isQuota := h.(harnesses.QuotaHarness)
		ah, isAccount := h.(harnesses.AccountHarness)

		if isQuota {
			s.startQuotaTicker(name, qh, now)
		}
		if isAccount {
			if !isQuota || ah.AccountFreshness() != qh.QuotaFreshness() {
				s.startAccountTicker(name, ah, now)
			}
		}
	}
}

// Stop cancels the scheduler context and waits for ticker goroutines to exit.
func (s *RefreshScheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel == nil {
		s.primary.stop()
		return
	}
	cancel()
	s.primary.stop()
	s.wg.Wait()
}

func (s *RefreshScheduler) startQuotaTicker(name string, qh harnesses.QuotaHarness, now time.Time) {
	interval := qh.QuotaFreshness() / 2
	if interval <= 0 {
		return
	}
	if status, err := qh.QuotaStatus(s.ctx, now); err == nil && status.State == harnesses.QuotaUnavailable {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			_, _ = qh.RefreshQuota(s.ctx)
		}()
	}

	ticker := s.clock.NewTicker(interval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C():
				s.maybeRefreshQuota(name, qh)
			}
		}
	}()
}

func (s *RefreshScheduler) maybeRefreshQuota(name string, qh harnesses.QuotaHarness) {
	defer s.notifyTick(name)

	now := s.clock.Now()
	status, err := qh.QuotaStatus(s.ctx, now)
	if err != nil {
		return
	}
	if status.State == harnesses.QuotaUnavailable {
		_, _ = qh.RefreshQuota(s.ctx)
		return
	}
	if !status.CapturedAt.IsZero() && now.Sub(status.CapturedAt) < qh.QuotaFreshness() {
		return
	}
	_, _ = qh.RefreshQuota(s.ctx)
}

func (s *RefreshScheduler) startAccountTicker(name string, ah harnesses.AccountHarness, now time.Time) {
	interval := ah.AccountFreshness() / 2
	if interval <= 0 {
		return
	}
	if snap, err := ah.AccountStatus(s.ctx, now); err == nil && snap.CapturedAt.IsZero() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			_, _ = ah.RefreshAccount(s.ctx)
		}()
	}

	ticker := s.clock.NewTicker(interval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C():
				s.maybeRefreshAccount(name, ah)
			}
		}
	}()
}

func (s *RefreshScheduler) maybeRefreshAccount(name string, ah harnesses.AccountHarness) {
	defer s.notifyTick(name)

	now := s.clock.Now()
	snap, err := ah.AccountStatus(s.ctx, now)
	if err != nil {
		return
	}
	if !snap.CapturedAt.IsZero() && now.Sub(snap.CapturedAt) < ah.AccountFreshness() {
		return
	}
	_, _ = ah.RefreshAccount(s.ctx)
}

func (s *RefreshScheduler) notifyTick(name string) {
	if s == nil || s.tickNotify == nil {
		return
	}
	select {
	case s.tickNotify <- name:
	default:
	}
}
