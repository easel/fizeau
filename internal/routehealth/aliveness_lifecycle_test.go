package routehealth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const alivenessTestWatchdog = 5 * time.Second

func receiveWithWatchdog[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	timer := time.NewTimer(alivenessTestWatchdog)
	defer timer.Stop()
	select {
	case value := <-channel:
		return value
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func TestAlivenessStartupTotalTimeoutSkipsUnattempted(t *testing.T) {
	store := NewProbeStore()
	probeAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	duration := make(chan time.Duration, 1)
	cancelProbe := make(chan context.CancelFunc, 1)
	probeStarted := make(chan struct{}, 1)
	var persistCalls atomic.Int64

	coordinator := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store: store,
		Prober: func(ctx context.Context, _, _ string) bool {
			probeStarted <- struct{}{}
			<-ctx.Done()
			return true
		},
		Persist: func() error {
			persistCalls.Add(1)
			return errors.New("ignored persistence failure")
		},
		Now: func() time.Time { return probeAt },
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			duration <- timeout
			ctx, cancel := context.WithCancel(parent)
			cancelProbe <- cancel
			return ctx, cancel
		},
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)
	done := make(chan struct{})
	go func() {
		coordinator.Startup(runCtx, []AlivenessEndpoint{
			{Provider: "attempted", BaseURL: "http://attempted:1234"},
			{Provider: "skipped-a", BaseURL: "http://skipped-a:1234"},
			{Provider: "skipped-b", BaseURL: "http://skipped-b:1234"},
		}, 0)
		close(done)
	}()

	cancel := receiveWithWatchdog(t, cancelProbe, "startup timeout cancel function")
	receiveWithWatchdog(t, probeStarted, "startup probe to begin")
	cancel()
	receiveWithWatchdog(t, done, "startup probe pass to stop")

	if got := receiveWithWatchdog(t, duration, "startup default timeout"); got != 5*time.Second {
		t.Fatalf("startup timeout = %v, want 5s", got)
	}
	record, ok := store.LastProbe("attempted", "")
	if !ok {
		t.Fatal("attempted endpoint has no probe record")
	}
	if record.LastProbeSuccess {
		t.Fatal("attempted endpoint recorded success after total-timeout cancellation")
	}
	if !record.LastProbeAt.Equal(probeAt) {
		t.Fatalf("attempted probe time = %v, want %v", record.LastProbeAt, probeAt)
	}
	for _, provider := range []string{"skipped-a", "skipped-b"} {
		if _, ok := store.LastProbe(provider, ""); ok {
			t.Fatalf("unattempted endpoint %q has a probe record", provider)
		}
	}
	if got := persistCalls.Load(); got != 1 {
		t.Fatalf("startup persistence calls = %d, want 1", got)
	}

	overrideDuration := make(chan time.Duration, 1)
	override := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store:  NewProbeStore(),
		Prober: func(context.Context, string, string) bool { return true },
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			overrideDuration <- timeout
			return context.WithCancel(parent)
		},
	})
	override.Startup(context.Background(), []AlivenessEndpoint{{Provider: "override"}}, 17*time.Second)
	if got := receiveWithWatchdog(t, overrideDuration, "startup positive override timeout"); got != 17*time.Second {
		t.Fatalf("startup override timeout = %v, want 17s", got)
	}

	negativeDuration := make(chan time.Duration, 1)
	negative := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store:  NewProbeStore(),
		Prober: func(context.Context, string, string) bool { return true },
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			negativeDuration <- timeout
			return context.WithCancel(parent)
		},
	})
	negative.Startup(context.Background(), []AlivenessEndpoint{{Provider: "negative"}}, -time.Second)
	if got := receiveWithWatchdog(t, negativeDuration, "startup negative-input default timeout"); got != 5*time.Second {
		t.Fatalf("startup negative-input timeout = %v, want 5s", got)
	}
}

func TestAlivenessRouteTimeCancellationFailsRemaining(t *testing.T) {
	store := NewProbeStore()
	probeAt := time.Date(2026, 7, 15, 12, 1, 0, 0, time.UTC)
	duration := make(chan time.Duration, 1)
	probeStarted := make(chan struct{}, 1)
	var probeCalls atomic.Int64

	coordinator := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store: store,
		Prober: func(ctx context.Context, _, _ string) bool {
			probeCalls.Add(1)
			probeStarted <- struct{}{}
			<-ctx.Done()
			return true
		},
		Now: func() time.Time { return probeAt },
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			duration <- timeout
			return context.WithCancel(parent)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		coordinator.ProbeRouteTime(ctx, []AlivenessEndpoint{
			{Provider: "current", BaseURL: "http://current:1234"},
			{Provider: "remaining-a", BaseURL: "http://remaining-a:1234"},
			{Provider: "remaining-b", BaseURL: "http://remaining-b:1234"},
		}, 0)
		close(done)
	}()

	receiveWithWatchdog(t, probeStarted, "route-time probe to begin")
	cancel()
	receiveWithWatchdog(t, done, "route-time probe pass to stop")

	if got := receiveWithWatchdog(t, duration, "route-time default timeout"); got != 2*time.Second {
		t.Fatalf("route-time timeout = %v, want 2s", got)
	}
	if got := probeCalls.Load(); got != 1 {
		t.Fatalf("prober calls = %d, want only the current endpoint", got)
	}
	for _, provider := range []string{"current", "remaining-a", "remaining-b"} {
		record, ok := store.LastProbe(provider, "")
		if !ok {
			t.Fatalf("failed endpoint %q has no probe record", provider)
		}
		if record.LastProbeSuccess {
			t.Fatalf("failed endpoint %q recorded success", provider)
		}
		if !record.LastProbeAt.Equal(probeAt) {
			t.Fatalf("failed endpoint %q probe time = %v, want %v", provider, record.LastProbeAt, probeAt)
		}
	}

	overrideDuration := make(chan time.Duration, 1)
	override := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store:  NewProbeStore(),
		Prober: func(context.Context, string, string) bool { return true },
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			overrideDuration <- timeout
			return context.WithCancel(parent)
		},
	})
	override.ProbeRouteTime(context.Background(), []AlivenessEndpoint{{Provider: "override"}}, 11*time.Second)
	if got := receiveWithWatchdog(t, overrideDuration, "route-time positive override timeout"); got != 11*time.Second {
		t.Fatalf("route-time override timeout = %v, want 11s", got)
	}

	negativeDuration := make(chan time.Duration, 1)
	negative := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store:  NewProbeStore(),
		Prober: func(context.Context, string, string) bool { return true },
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			negativeDuration <- timeout
			return context.WithCancel(parent)
		},
	})
	negative.ProbeRouteTime(context.Background(), []AlivenessEndpoint{{Provider: "negative"}}, -time.Second)
	if got := receiveWithWatchdog(t, negativeDuration, "route-time negative-input default timeout"); got != 2*time.Second {
		t.Fatalf("route-time negative-input timeout = %v, want 2s", got)
	}
}

func TestAlivenessLoopRetriesAndSkipsFresh(t *testing.T) {
	store := NewProbeStore()
	base := time.Date(2026, 7, 15, 12, 2, 0, 0, time.UTC)
	interval := time.Minute
	store.RecordProbe("fresh", "", true, base.Add(30*time.Second))

	var nowOffsetSeconds atomic.Int64
	var retryCalls atomic.Int64
	var freshCalls atomic.Int64
	probeCalls := make(chan string, 4)
	probeTimeouts := make(chan time.Duration, 4)
	sleepEntered := make(chan time.Duration, 2)
	releaseSleep := make(chan bool, 1)
	persisted := make(chan struct{}, 2)
	coordinator := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store: store,
		Prober: func(_ context.Context, provider, _ string) bool {
			probeCalls <- provider
			if provider == "fresh" {
				freshCalls.Add(1)
				return true
			}
			return retryCalls.Add(1) > 1
		},
		Persist: func() error {
			persisted <- struct{}{}
			return errors.New("ignored persistence failure")
		},
		Now: func() time.Time {
			return base.Add(time.Duration(nowOffsetSeconds.Load()) * time.Second)
		},
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			probeTimeouts <- timeout
			return context.WithCancel(parent)
		},
		Sleep: func(ctx context.Context, duration time.Duration) bool {
			sleepEntered <- duration
			select {
			case proceed := <-releaseSleep:
				return proceed
			case <-ctx.Done():
				return false
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := coordinator.StartLoop(ctx, []AlivenessEndpoint{
		{Provider: "retry", BaseURL: "http://retry:1234"},
		{Provider: "fresh", BaseURL: "http://fresh:1234"},
	}, interval)

	if got := receiveWithWatchdog(t, probeTimeouts, "first loop probe timeout"); got != 2*time.Second {
		t.Fatalf("first loop probe timeout = %v, want 2s", got)
	}
	if got := receiveWithWatchdog(t, probeCalls, "first loop probe"); got != "retry" {
		t.Fatalf("first probe = %q, want retry", got)
	}
	receiveWithWatchdog(t, persisted, "first loop persistence")
	if got := receiveWithWatchdog(t, sleepEntered, "first loop sleep"); got != interval {
		t.Fatalf("first sleep = %v, want %v", got, interval)
	}
	select {
	case got := <-probeCalls:
		t.Fatalf("fresh endpoint was probed in first iteration: %q", got)
	default:
	}

	nowOffsetSeconds.Store(int64(interval / time.Second))
	releaseSleep <- true
	if got := receiveWithWatchdog(t, probeTimeouts, "second loop probe timeout"); got != 2*time.Second {
		t.Fatalf("second loop probe timeout = %v, want 2s", got)
	}
	if got := receiveWithWatchdog(t, probeCalls, "second loop probe"); got != "retry" {
		t.Fatalf("second probe = %q, want retry", got)
	}
	receiveWithWatchdog(t, persisted, "second loop persistence")
	if got := receiveWithWatchdog(t, sleepEntered, "second loop sleep"); got != interval {
		t.Fatalf("second sleep = %v, want %v", got, interval)
	}
	select {
	case got := <-probeCalls:
		t.Fatalf("fresh endpoint was probed in second iteration: %q", got)
	default:
	}

	cancel()
	receiveWithWatchdog(t, done, "loop to stop after cancellation")
	if got := retryCalls.Load(); got != 2 {
		t.Fatalf("retry probe calls = %d, want 2", got)
	}
	if got := freshCalls.Load(); got != 0 {
		t.Fatalf("fresh probe calls = %d, want 0", got)
	}
	record, ok := store.LastProbe("retry", "")
	if !ok || !record.LastProbeSuccess {
		t.Fatalf("retry endpoint final record = %#v, present=%v; want success", record, ok)
	}
}

func TestAlivenessLoopCancellationStopsProbes(t *testing.T) {
	store := NewProbeStore()
	probeStarted := make(chan string, 2)
	probeTimeouts := make(chan time.Duration, 2)
	var probeCalls atomic.Int64
	coordinator := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store: store,
		Prober: func(ctx context.Context, provider, _ string) bool {
			probeCalls.Add(1)
			probeStarted <- provider
			<-ctx.Done()
			return true
		},
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			probeTimeouts <- timeout
			return context.WithCancel(parent)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := coordinator.StartLoop(ctx, []AlivenessEndpoint{
		{Provider: "started", BaseURL: "http://started:1234"},
		{Provider: "must-not-start", BaseURL: "http://must-not-start:1234"},
	}, time.Minute)

	if got := receiveWithWatchdog(t, probeTimeouts, "cancellable loop probe timeout"); got != 2*time.Second {
		t.Fatalf("cancellable loop probe timeout = %v, want 2s", got)
	}
	if got := receiveWithWatchdog(t, probeStarted, "cancellable loop probe to begin"); got != "started" {
		t.Fatalf("first probe = %q, want started", got)
	}
	cancel()
	receiveWithWatchdog(t, done, "cancellable loop to stop")
	if got := probeCalls.Load(); got != 1 {
		t.Fatalf("probe calls after cancellation = %d, want 1", got)
	}
	if _, ok := store.LastProbe("must-not-start", ""); ok {
		t.Fatal("endpoint after canceled probe has a record")
	}
	select {
	case provider := <-probeStarted:
		t.Fatalf("probe started after cancellation: %q", provider)
	default:
	}
}

func TestAlivenessRefreshSingleFlight(t *testing.T) {
	type contextKey struct{}
	const contenders = 32
	store := NewProbeStore()
	probeStarted := make(chan string, contenders+2)
	probeTimeouts := make(chan time.Duration, contenders+2)
	releaseProbe := make(chan struct{}, 2)
	t.Cleanup(func() { close(releaseProbe) })
	var probeCalls atomic.Int64
	var persistCalls atomic.Int64
	var sawBackgroundContext atomic.Bool
	coordinator := NewAlivenessCoordinator(AlivenessCoordinatorOptions{
		Store: store,
		Prober: func(ctx context.Context, provider, _ string) bool {
			probeCalls.Add(1)
			if value, _ := ctx.Value(contextKey{}).(bool); value {
				sawBackgroundContext.Store(true)
			}
			probeStarted <- provider
			<-releaseProbe
			return true
		},
		Persist: func() error {
			persistCalls.Add(1)
			return errors.New("ignored persistence failure")
		},
		BackgroundContext: func() context.Context {
			return context.WithValue(context.Background(), contextKey{}, true)
		},
		WithTimeout: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			probeTimeouts <- timeout
			return context.WithCancel(parent)
		},
	})

	firstDone := coordinator.RequestRefresh([]AlivenessEndpoint{{
		Provider: "first", BaseURL: "http://first:1234",
	}}, time.Minute)
	if firstDone == nil {
		t.Fatal("first refresh was not launched")
	}
	if got := receiveWithWatchdog(t, probeTimeouts, "first refresh probe timeout"); got != 2*time.Second {
		t.Fatalf("first refresh probe timeout = %v, want 2s", got)
	}
	if got := receiveWithWatchdog(t, probeStarted, "first refresh probe to begin"); got != "first" {
		t.Fatalf("first refresh probe = %q, want first", got)
	}

	startContenders := make(chan struct{})
	results := make(chan (<-chan struct{}), contenders)
	for range contenders {
		go func() {
			<-startContenders
			results <- coordinator.RequestRefresh([]AlivenessEndpoint{{
				Provider: "contender", BaseURL: "http://contender:1234",
			}}, time.Minute)
		}()
	}
	close(startContenders)
	for range contenders {
		if got := receiveWithWatchdog(t, results, "concurrent refresh result"); got != nil {
			t.Fatal("concurrent refresh bypassed single-flight")
		}
	}

	releaseProbe <- struct{}{}
	receiveWithWatchdog(t, firstDone, "first refresh to finish")
	if got := persistCalls.Load(); got != 1 {
		t.Fatalf("persistence calls after first refresh = %d, want 1", got)
	}

	secondDone := coordinator.RequestRefresh([]AlivenessEndpoint{{
		Provider: "second", BaseURL: "http://second:1234",
	}}, time.Minute)
	if secondDone == nil {
		t.Fatal("single-flight state did not reset after first refresh")
	}
	if got := receiveWithWatchdog(t, probeTimeouts, "second refresh probe timeout"); got != 2*time.Second {
		t.Fatalf("second refresh probe timeout = %v, want 2s", got)
	}
	if got := receiveWithWatchdog(t, probeStarted, "second refresh probe to begin"); got != "second" {
		t.Fatalf("second refresh probe = %q, want second", got)
	}
	releaseProbe <- struct{}{}
	receiveWithWatchdog(t, secondDone, "second refresh to finish")

	if got := probeCalls.Load(); got != 2 {
		t.Fatalf("refresh probe calls = %d, want 2", got)
	}
	if got := persistCalls.Load(); got != 2 {
		t.Fatalf("refresh persistence calls = %d, want 2", got)
	}
	if !sawBackgroundContext.Load() {
		t.Fatal("refresh did not use the injected background context")
	}
}
