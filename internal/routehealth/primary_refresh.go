package routehealth

import (
	"context"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

const (
	defaultPrimaryQuotaRefreshDebounce     = 15 * time.Minute
	defaultPrimaryQuotaRefreshStartupWait  = 2 * time.Second
	defaultPrimaryQuotaRefreshProbeTimeout = 30 * time.Second
)

var primaryQuotaHarnessNames = [...]string{"claude", "codex", "grok"}

// PrimaryQuotaRefreshPolicy configures the service-owned refresh path for the
// primary subscription harnesses. Interval zero disables the explicitly
// configured server timer; the RefreshScheduler's existing
// QuotaFreshness-derived cadence remains independent.
type PrimaryQuotaRefreshPolicy struct {
	Debounce           time.Duration
	StartupWait        time.Duration
	ClaudeProbeTimeout time.Duration
	Interval           time.Duration
}

// DefaultPrimaryQuotaRefreshPolicy returns the established service defaults.
func DefaultPrimaryQuotaRefreshPolicy() PrimaryQuotaRefreshPolicy {
	return PrimaryQuotaRefreshPolicy{
		Debounce:           defaultPrimaryQuotaRefreshDebounce,
		StartupWait:        defaultPrimaryQuotaRefreshStartupWait,
		ClaudeProbeTimeout: defaultPrimaryQuotaRefreshProbeTimeout,
	}
}

// PrimaryQuotaCacheStatus is the scheduler's cache-only decision for one
// primary harness. NeedsRefresh reports whether activity/startup should request
// a best-effort probe. Usable reports whether startup may continue without
// waiting for that probe.
type PrimaryQuotaCacheStatus struct {
	NeedsRefresh bool
	Usable       bool
}

// ConfigurePrimaryQuotaRefresh enables the primary Claude/Codex refresh path.
// It must be called before Start. The zero policy uses the established defaults
// and leaves the explicitly configured timer disabled.
func (s *RefreshScheduler) ConfigurePrimaryQuotaRefresh(policy PrimaryQuotaRefreshPolicy) {
	if s == nil || s.primary == nil {
		return
	}
	s.primary.configure(policy)
}

// RequestPrimaryQuotaRefresh requests debounced, asynchronous refreshes for
// stale primary quota caches. It is intended for status/inventory activity;
// foreground routing remains cache-only.
func (s *RefreshScheduler) RequestPrimaryQuotaRefresh(ctx context.Context) {
	if s == nil || s.primary == nil {
		return
	}
	s.primary.ensure(ctx, false)
}

// PrimaryQuotaCacheStatus returns the cache-only refresh decision for name.
// Unknown or non-quota harnesses return the zero value.
func (s *RefreshScheduler) PrimaryQuotaCacheStatus(ctx context.Context, name string) PrimaryQuotaCacheStatus {
	if s == nil || s.primary == nil {
		return PrimaryQuotaCacheStatus{}
	}
	return s.primary.cacheStatus(ctx, name)
}

// RefreshPrimaryQuota performs one explicit best-effort refresh for name. It
// intentionally bypasses activity debounce: callers use it for an explicit
// health-check request. Claude receives the configured probe timeout while
// Codex inherits only the caller's context.
func (s *RefreshScheduler) RefreshPrimaryQuota(ctx context.Context, name string) {
	if s == nil || s.primary == nil {
		return
	}
	s.primary.refresh(ctx, name)
}

// RefreshPrimaryQuotaForHealthCheck preserves the explicit diagnostic
// behavior that predates the activity scheduler: Claude skips an expensive
// probe while its cache is younger than the service default debounce, while
// Codex refreshes unconditionally. Activity-specific debounce overrides do
// not change this diagnostic contract.
func (s *RefreshScheduler) RefreshPrimaryQuotaForHealthCheck(ctx context.Context, name string) {
	if s == nil || s.primary == nil {
		return
	}
	if name == "claude" && !s.primary.cacheStatusWithDebounce(ctx, name, defaultPrimaryQuotaRefreshDebounce).NeedsRefresh {
		return
	}
	s.primary.refresh(ctx, name)
}

// ResetPrimaryQuotaRefreshForTest clears process-global debounce/in-flight
// state and returns a restore function. Tests must call it before constructing
// a scheduler and must not run concurrently with production refresh work.
func ResetPrimaryQuotaRefreshForTest() func() {
	processPrimaryQuotaRefreshState.mu.Lock()
	oldLastAttempt := processPrimaryQuotaRefreshState.lastAttempt
	oldInFlight := processPrimaryQuotaRefreshState.inFlight
	processPrimaryQuotaRefreshState.lastAttempt = make(map[string]time.Time)
	processPrimaryQuotaRefreshState.inFlight = make(map[string]bool)
	processPrimaryQuotaRefreshState.mu.Unlock()

	return func() {
		processPrimaryQuotaRefreshState.mu.Lock()
		processPrimaryQuotaRefreshState.lastAttempt = oldLastAttempt
		processPrimaryQuotaRefreshState.inFlight = oldInFlight
		processPrimaryQuotaRefreshState.mu.Unlock()
	}
}

type primaryQuotaRefreshState struct {
	mu          sync.Mutex
	lastAttempt map[string]time.Time
	inFlight    map[string]bool
}

func newPrimaryQuotaRefreshState() *primaryQuotaRefreshState {
	return &primaryQuotaRefreshState{
		lastAttempt: make(map[string]time.Time),
		inFlight:    make(map[string]bool),
	}
}

// Debounce remains process-global, matching the original root coordinator.
// Harness implementations own the stronger probe-level single-flight
// guarantee required by CONTRACT-004.
var processPrimaryQuotaRefreshState = newPrimaryQuotaRefreshState()

type primaryQuotaRefreshCoordinator struct {
	lookup func(string) harnesses.Harness
	clock  schedulerClock
	state  *primaryQuotaRefreshState
	after  func(time.Duration) <-chan time.Time

	// timerNotify is a deterministic test seam fired after an explicit server
	// timer tick has requested refreshes.
	timerNotify chan<- struct{}

	mu         sync.Mutex
	policy     PrimaryQuotaRefreshPolicy
	configured bool
	running    bool
	stopping   bool
	ctx        context.Context
	cancel     context.CancelFunc
	timerCtx   context.Context
	wg         sync.WaitGroup
}

func newPrimaryQuotaRefreshCoordinator(lookup func(string) harnesses.Harness, clock schedulerClock, state *primaryQuotaRefreshState) *primaryQuotaRefreshCoordinator {
	return &primaryQuotaRefreshCoordinator{
		lookup: lookup,
		clock:  clock,
		state:  state,
		after:  time.After,
		policy: DefaultPrimaryQuotaRefreshPolicy(),
	}
}

func normalizePrimaryQuotaRefreshPolicy(policy PrimaryQuotaRefreshPolicy) PrimaryQuotaRefreshPolicy {
	defaults := DefaultPrimaryQuotaRefreshPolicy()
	if policy.Debounce <= 0 {
		policy.Debounce = defaults.Debounce
	}
	if policy.StartupWait <= 0 {
		policy.StartupWait = defaults.StartupWait
	}
	if policy.ClaudeProbeTimeout <= 0 {
		policy.ClaudeProbeTimeout = defaults.ClaudeProbeTimeout
	}
	if policy.Interval < 0 {
		policy.Interval = 0
	}
	return policy
}

func (c *primaryQuotaRefreshCoordinator) configure(policy PrimaryQuotaRefreshPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running || c.stopping {
		panic("refreshScheduler: ConfigurePrimaryQuotaRefresh called after Start")
	}
	c.policy = normalizePrimaryQuotaRefreshPolicy(policy)
	c.configured = true
}

func (c *primaryQuotaRefreshCoordinator) start(parent context.Context) {
	c.mu.Lock()
	if !c.configured {
		c.mu.Unlock()
		return
	}
	if c.running || c.stopping {
		c.mu.Unlock()
		panic("refreshScheduler: primary quota refresh started twice")
	}
	if parent == nil {
		parent = context.Background()
	}
	// Coordinator ownership is independent of the optional timer context:
	// callers historically use QuotaRefreshContext to cancel only the
	// periodic worker, while later ListHarnesses/RouteStatus activity remains
	// able to request refreshes with its own context.
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.timerCtx = parent
	c.running = true
	c.mu.Unlock()

	c.ensure(parent, true)
	c.startTimer()
}

func (c *primaryQuotaRefreshCoordinator) startTimer() {
	c.mu.Lock()
	if !c.running || c.stopping || c.policy.Interval <= 0 || c.ctx.Err() != nil || c.timerCtx.Err() != nil {
		c.mu.Unlock()
		return
	}
	ticker := c.clock.NewTicker(c.policy.Interval)
	lifecycle := c.ctx
	timerCtx := c.timerCtx
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-lifecycle.Done():
				return
			case <-timerCtx.Done():
				return
			case <-ticker.C():
				c.ensure(timerCtx, false)
				c.notifyTimerTick()
			}
		}
	}()
}

func (c *primaryQuotaRefreshCoordinator) stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	c.stopping = true
	cancel := c.cancel
	c.mu.Unlock()

	cancel()
	c.wg.Wait()

	c.mu.Lock()
	c.stopping = false
	c.ctx = nil
	c.cancel = nil
	c.timerCtx = nil
	c.mu.Unlock()
}

func (c *primaryQuotaRefreshCoordinator) ensure(ctx context.Context, startup bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	var waits []<-chan struct{}
	for _, name := range primaryQuotaHarnessNames {
		status := c.cacheStatus(ctx, name)
		if !status.NeedsRefresh {
			continue
		}
		done := c.request(ctx, name)
		if startup && !status.Usable && done != nil {
			waits = append(waits, done)
		}
	}
	if !startup || len(waits) == 0 {
		return
	}

	c.mu.Lock()
	wait := c.policy.StartupWait
	after := c.after
	c.mu.Unlock()
	if wait <= 0 {
		return
	}
	deadline := after(wait)
	for _, done := range waits {
		select {
		case <-done:
		case <-deadline:
			return
		}
	}
}

func (c *primaryQuotaRefreshCoordinator) cacheStatus(ctx context.Context, name string) PrimaryQuotaCacheStatus {
	c.mu.Lock()
	debounce := c.policy.Debounce
	c.mu.Unlock()
	return c.cacheStatusWithDebounce(ctx, name, debounce)
}

func (c *primaryQuotaRefreshCoordinator) cacheStatusWithDebounce(ctx context.Context, name string, debounce time.Duration) PrimaryQuotaCacheStatus {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil || c.lookup == nil {
		return PrimaryQuotaCacheStatus{}
	}
	qh, ok := c.lookup(name).(harnesses.QuotaHarness)
	if !ok {
		return PrimaryQuotaCacheStatus{}
	}
	now := c.clock.Now()
	status, err := qh.QuotaStatus(ctx, now)
	if err != nil {
		return PrimaryQuotaCacheStatus{}
	}

	switch name {
	case "claude":
		if status.State == harnesses.QuotaUnavailable {
			return PrimaryQuotaCacheStatus{NeedsRefresh: true}
		}
		stale := !status.CapturedAt.IsZero() && now.Sub(status.CapturedAt) >= debounce
		return PrimaryQuotaCacheStatus{NeedsRefresh: stale, Usable: !stale}
	case "codex":
		if status.State == harnesses.QuotaUnavailable {
			return PrimaryQuotaCacheStatus{NeedsRefresh: true}
		}
		usable := status.Fresh && status.RoutingPreference == harnesses.RoutingPreferenceAvailable
		return PrimaryQuotaCacheStatus{NeedsRefresh: !usable, Usable: usable}
	default:
		return PrimaryQuotaCacheStatus{}
	}
}

func (c *primaryQuotaRefreshCoordinator) request(ctx context.Context, name string) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil
	}

	c.mu.Lock()
	if !c.running || c.stopping || c.ctx == nil || c.ctx.Err() != nil {
		c.mu.Unlock()
		return nil
	}
	lifecycle := c.ctx
	policy := c.policy

	now := c.clock.Now()
	c.state.mu.Lock()
	if c.state.inFlight[name] {
		c.state.mu.Unlock()
		c.mu.Unlock()
		return nil
	}
	if last := c.state.lastAttempt[name]; !last.IsZero() && now.Sub(last) < policy.Debounce {
		c.state.mu.Unlock()
		c.mu.Unlock()
		return nil
	}
	c.state.lastAttempt[name] = now
	c.state.inFlight[name] = true
	done := make(chan struct{})
	c.wg.Add(1)
	c.state.mu.Unlock()
	c.mu.Unlock()

	workerCtx, cancel := context.WithCancel(ctx)
	stopLifecycleCancel := context.AfterFunc(lifecycle, cancel)
	go func() {
		defer c.wg.Done()
		defer close(done)
		defer stopLifecycleCancel()
		defer cancel()
		defer func() {
			c.state.mu.Lock()
			c.state.inFlight[name] = false
			c.state.mu.Unlock()
		}()
		c.refreshWithPolicy(workerCtx, name, policy)
	}()
	return done
}

func (c *primaryQuotaRefreshCoordinator) refresh(ctx context.Context, name string) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	policy := c.policy
	c.mu.Unlock()
	c.refreshWithPolicy(ctx, name, policy)
}

func (c *primaryQuotaRefreshCoordinator) refreshWithPolicy(ctx context.Context, name string, policy PrimaryQuotaRefreshPolicy) {
	if ctx.Err() != nil || c.lookup == nil {
		return
	}
	if name != "claude" && name != "codex" {
		return
	}
	qh, ok := c.lookup(name).(harnesses.QuotaHarness)
	if !ok {
		return
	}
	if name == "claude" {
		probeCtx, cancel := context.WithTimeout(ctx, policy.ClaudeProbeTimeout)
		defer cancel()
		_, _ = qh.RefreshQuota(probeCtx)
		return
	}
	_, _ = qh.RefreshQuota(ctx)
}

func (c *primaryQuotaRefreshCoordinator) notifyTimerTick() {
	if c.timerNotify == nil {
		return
	}
	select {
	case c.timerNotify <- struct{}{}:
	default:
	}
}
