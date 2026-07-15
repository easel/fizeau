package routehealth

import (
	"context"
	"sync/atomic"
	"time"
)

const (
	// DefaultAlivenessRouteTimeProbeTimeout bounds one route-time or periodic
	// endpoint probe when no positive timeout is supplied.
	DefaultAlivenessRouteTimeProbeTimeout = 2 * time.Second
	// DefaultAlivenessStartupTotalTimeout bounds the complete sequential
	// startup probe pass when no positive timeout is supplied.
	DefaultAlivenessStartupTotalTimeout = 5 * time.Second
)

// AlivenessProber reports whether a provider endpoint is reachable.
type AlivenessProber func(ctx context.Context, provider, baseURL string) bool

// AlivenessCoordinatorOptions supplies the stores and runtime seams owned by
// one aliveness coordinator. Nil seams receive production defaults.
type AlivenessCoordinatorOptions struct {
	Store             *ProbeStore
	Prober            AlivenessProber
	Persist           func() error
	Now               func() time.Time
	WithTimeout       func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	Sleep             func(context.Context, time.Duration) bool
	BackgroundContext func() context.Context
}

// AlivenessCoordinator owns aliveness probe lifecycle, launch policy, and
// routing-refresh single-flight state for one caller-supplied ProbeStore.
type AlivenessCoordinator struct {
	store             *ProbeStore
	prober            AlivenessProber
	persist           func() error
	now               func() time.Time
	withTimeout       func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	sleep             func(context.Context, time.Duration) bool
	backgroundContext func() context.Context
	refreshInFlight   atomic.Bool
}

// NewAlivenessCoordinator returns a coordinator over the supplied probe store.
func NewAlivenessCoordinator(opts AlivenessCoordinatorOptions) *AlivenessCoordinator {
	prober := opts.Prober
	if prober == nil {
		prober = TCPAlivenessProber
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	withTimeout := opts.WithTimeout
	if withTimeout == nil {
		withTimeout = context.WithTimeout
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = alivenessSleep
	}
	backgroundContext := opts.BackgroundContext
	if backgroundContext == nil {
		backgroundContext = context.Background
	}
	return &AlivenessCoordinator{
		store:             opts.Store,
		prober:            prober,
		persist:           opts.Persist,
		now:               now,
		withTimeout:       withTimeout,
		sleep:             sleep,
		backgroundContext: backgroundContext,
	}
}

// Startup probes endpoints sequentially within one total timeout and records
// only endpoints whose prober was actually invoked. Persistence is best effort.
func (c *AlivenessCoordinator) Startup(ctx context.Context, endpoints []AlivenessEndpoint, totalTimeout time.Duration) {
	if c == nil || c.store == nil || c.prober == nil || len(endpoints) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if totalTimeout <= 0 {
		totalTimeout = DefaultAlivenessStartupTotalTimeout
	}
	probeCtx, cancel := c.withTimeout(ctx, totalTimeout)
	defer cancel()
	probeAt := c.now().UTC()
	for _, endpoint := range endpoints {
		if probeCtx.Err() != nil {
			break
		}
		success := c.prober(probeCtx, endpoint.Provider, endpoint.BaseURL)
		if probeCtx.Err() != nil {
			success = false
		}
		c.store.RecordProbe(endpoint.Provider, endpoint.Endpoint, success, probeAt)
	}
	c.persistIgnoringError()
}

// ProbeRouteTime probes endpoints sequentially with a per-endpoint timeout.
// Parent cancellation records the current attempted endpoint and all remaining
// endpoints as failed, matching the routing fail-closed transition.
func (c *AlivenessCoordinator) ProbeRouteTime(ctx context.Context, endpoints []AlivenessEndpoint, perProbeTimeout time.Duration) {
	if c == nil || c.store == nil || c.prober == nil || len(endpoints) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if perProbeTimeout <= 0 {
		perProbeTimeout = DefaultAlivenessRouteTimeProbeTimeout
	}
	for index, endpoint := range endpoints {
		if ctx.Err() != nil {
			c.recordFailures(endpoints[index:], c.now().UTC())
			return
		}
		probeAt := c.now().UTC()
		probeCtx, cancel := c.withTimeout(ctx, perProbeTimeout)
		success := c.prober(probeCtx, endpoint.Provider, endpoint.BaseURL)
		if probeCtx.Err() != nil {
			success = false
		}
		cancel()
		c.store.RecordProbe(endpoint.Provider, endpoint.Endpoint, success, probeAt)
		if ctx.Err() != nil {
			c.recordFailures(endpoints[index+1:], probeAt)
			return
		}
	}
}

// RequestRefresh starts at most one background-context route-time refresh for
// endpoints whose evidence is due. A nil result means no refresh was launched;
// a non-nil channel closes after probing, best-effort persistence, and reset of
// the single-flight state.
func (c *AlivenessCoordinator) RequestRefresh(endpoints []AlivenessEndpoint, interval time.Duration) <-chan struct{} {
	if c == nil || c.store == nil || c.prober == nil {
		return nil
	}
	due := AlivenessDueEndpoints(c.store, endpoints, c.now().UTC(), interval)
	if len(due) == 0 || !c.refreshInFlight.CompareAndSwap(false, true) {
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer c.refreshInFlight.Store(false)
		ctx := c.backgroundContext()
		if ctx == nil {
			ctx = context.Background()
		}
		c.ProbeRouteTime(ctx, due, DefaultAlivenessRouteTimeProbeTimeout)
		c.persistIgnoringError()
	}()
	return done
}

// StartLoop launches the periodic probe loop on the supplied service context.
// The returned channel closes when cancellation or the sleep seam stops it.
func (c *AlivenessCoordinator) StartLoop(ctx context.Context, endpoints []AlivenessEndpoint, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if c == nil || c.store == nil || c.prober == nil || len(endpoints) == 0 {
		close(done)
		return done
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval = ResolveAlivenessProbeInterval(interval)
	go func() {
		defer close(done)
		for {
			probeAt := c.now().UTC()
			for _, endpoint := range endpoints {
				if ctx.Err() != nil {
					return
				}
				if !c.store.ProbeNeeded(endpoint.Provider, endpoint.Endpoint, probeAt, interval) {
					continue
				}
				probeCtx, cancel := c.withTimeout(ctx, DefaultAlivenessRouteTimeProbeTimeout)
				success := c.prober(probeCtx, endpoint.Provider, endpoint.BaseURL)
				cancel()
				c.store.RecordProbe(endpoint.Provider, endpoint.Endpoint, success, probeAt)
			}
			c.persistIgnoringError()
			if !c.sleep(ctx, interval) {
				return
			}
		}
	}()
	return done
}

func (c *AlivenessCoordinator) recordFailures(endpoints []AlivenessEndpoint, probeAt time.Time) {
	for _, endpoint := range endpoints {
		c.store.RecordProbe(endpoint.Provider, endpoint.Endpoint, false, probeAt)
	}
}

func (c *AlivenessCoordinator) persistIgnoringError() {
	if c.persist != nil {
		_ = c.persist()
	}
}

func alivenessSleep(ctx context.Context, duration time.Duration) bool {
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
