package discoverycache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	return &Cache{Root: t.TempDir()}
}

func testSource(name string, ttl, deadline time.Duration) Source {
	return Source{Tier: "discovery", Name: name, TTL: ttl, RefreshDeadline: deadline}
}

func TestReadEmpty(t *testing.T) {
	c := newTestCache(t)
	s := testSource("empty", time.Hour, 10*time.Second)
	res, err := c.Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data != nil {
		t.Errorf("expected nil Data, got %d bytes", len(res.Data))
	}
	if !res.Stale {
		t.Error("expected Stale=true for empty cache")
	}
	if res.Fresh {
		t.Error("expected Fresh=false for empty cache")
	}
}

func TestReadAfterWrite(t *testing.T) {
	c := newTestCache(t)
	s := testSource("after-write", time.Hour, 10*time.Second)
	want := []byte(`{"hello":"world"}`)

	if err := c.Refresh(s, func(_ context.Context) ([]byte, error) { return want, nil }); err != nil {
		t.Fatal(err)
	}

	res, err := c.Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Data) != string(want) {
		t.Errorf("Read() = %q, want %q", res.Data, want)
	}
	if !res.Fresh {
		t.Errorf("expected Fresh=true, Age=%v TTL=%v", res.Age, s.TTL)
	}
	if res.Stale {
		t.Error("expected Stale=false after write")
	}
}

func TestReadStaleByMtime(t *testing.T) {
	c := newTestCache(t)
	s := testSource("stale-mtime", time.Hour, 10*time.Second)

	if err := c.Refresh(s, func(_ context.Context) ([]byte, error) { return []byte(`{}`), nil }); err != nil {
		t.Fatal(err)
	}
	// Backdate the file's mtime by 2h to make it stale.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(c.dataPath(s), past, past); err != nil {
		t.Fatal(err)
	}

	res, err := c.Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fresh {
		t.Errorf("expected Fresh=false, Age=%v TTL=%v", res.Age, s.TTL)
	}
	if !res.Stale {
		t.Error("expected Stale=true for backdated file")
	}
	if res.Data == nil {
		t.Error("expected stale Data to be non-nil")
	}
}

func TestRefreshIdempotent(t *testing.T) {
	c := newTestCache(t)
	s := testSource("idempotent", time.Hour, 10*time.Second)
	for i := range 3 {
		if err := c.Refresh(s, func(_ context.Context) ([]byte, error) { return []byte(`{}`), nil }); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	res, err := c.Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Fresh {
		t.Error("expected Fresh=true after repeated Refresh")
	}
}

func TestRefreshPassesDeadlineContextToRefresher(t *testing.T) {
	c := newTestCache(t)
	s := testSource("deadline", time.Hour, 20*time.Millisecond)

	start := time.Now()
	err := c.Refresh(s, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Refresh error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Refresh elapsed %v, want deadline-bounded refresh", elapsed)
	}
}

func TestMaybeRefreshSyncFailedRefreshRetryAfterMarkerStaleness(t *testing.T) {
	c := newTestCache(t)
	// Use a production-shaped long refresh deadline to prove the failure
	// cooldown is independent from the ordinary in-flight staleness window.
	s := testSource("failed-retry", time.Hour, time.Minute)

	attempts := 0
	if err := c.MaybeRefreshSync(s, func(_ context.Context) ([]byte, error) {
		attempts++
		return nil, fmt.Errorf("boom")
	}); err == nil {
		t.Fatal("expected initial failed refresh to return an error")
	}
	if attempts != 1 {
		t.Fatalf("initial refresh attempts = %d, want 1", attempts)
	}

	state, err := c.RefreshState(s)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Failed || !state.InFlight {
		t.Fatalf("after failed refresh state Failed=%v InFlight=%v, want both true", state.Failed, state.InFlight)
	}

	start := time.Now()
	if err := c.MaybeRefreshSync(s, func(_ context.Context) ([]byte, error) {
		attempts++
		return []byte(`{"v":"unexpected"}`), nil
	}); err != nil {
		t.Fatalf("immediate retry suppression returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("active failed marker suppression took %v, want < 100ms", elapsed)
	}
	if attempts != 1 {
		t.Fatalf("immediate sync call retried while failed marker was active: attempts=%d, want 1", attempts)
	}

	waitForFailedMarkerStale(t, c, s)

	if err := c.MaybeRefreshSync(s, func(_ context.Context) ([]byte, error) {
		attempts++
		return []byte(`{"v":"fresh"}`), nil
	}); err != nil {
		t.Fatalf("retry after marker staleness: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("retry after marker staleness attempts = %d, want 2", attempts)
	}

	state, err = c.RefreshState(s)
	if err != nil {
		t.Fatal(err)
	}
	if state.Failed {
		t.Fatalf("successful stale-marker retry left failed state: %+v", state)
	}
	res, err := c.Read(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Data) != `{"v":"fresh"}` {
		t.Fatalf("Read() after retry = %q, want fresh payload", res.Data)
	}
	if !res.Fresh {
		t.Fatalf("Read() after retry Fresh=false, Age=%v TTL=%v", res.Age, s.TTL)
	}
}

func TestRefreshFailedRefreshDoesNotWaitForSourceDeadline(t *testing.T) {
	c := newTestCache(t)
	s := testSource("failed-force", time.Hour, time.Minute)

	if err := c.Refresh(s, func(_ context.Context) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}); err == nil {
		t.Fatal("expected initial failed refresh to return an error")
	}

	start := time.Now()
	if err := c.Refresh(s, func(_ context.Context) ([]byte, error) {
		return []byte(`{"v":"unexpected"}`), nil
	}); err != nil {
		t.Fatalf("forced refresh during failure cooldown returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("forced refresh during failure cooldown took %v, want < 100ms", elapsed)
	}
}

func waitForFailedMarkerStale(t *testing.T, c *Cache, s Source) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := c.RefreshState(s)
		if err != nil {
			t.Fatal(err)
		}
		if state.Failed && !state.InFlight {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, err := c.RefreshState(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("failed refresh marker did not become stale before timeout: %+v", state)
}

func TestReadIsSubHundredMs(t *testing.T) {
	// AC: Cache.Read returns within 100ms p99 under no-contention baseline.
	c := newTestCache(t)
	s := testSource("perf", time.Hour, 10*time.Second)
	if err := c.Refresh(s, func(_ context.Context) ([]byte, error) { return []byte(`{}`), nil }); err != nil {
		t.Fatal(err)
	}

	var maxDuration time.Duration
	for range 200 {
		start := time.Now()
		if _, err := c.Read(s); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > maxDuration {
			maxDuration = d
		}
	}
	if maxDuration > 100*time.Millisecond {
		t.Errorf("Read() max = %v, want < 100ms", maxDuration)
	}
}

func TestReadP99Contention(t *testing.T) {
	c := newTestCache(t)
	s := testSource("contention", time.Hour, 10*time.Second)
	if err := os.MkdirAll(filepath.Join(c.Root, s.Tier), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(c.dataPath(s), []byte(`{"v":0,"pad":"seed"}`)); err != nil {
		t.Fatal(err)
	}

	const (
		numReaders     = 32
		readsPerReader = 200
		numWrites      = 200
	)

	start := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	reportErr := func(err error) {
		if err == nil {
			return
		}
		once.Do(func() {
			close(done)
		})
		t.Error(err)
	}

	samples := make(chan time.Duration, numReaders*readsPerReader)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-start:
		case <-done:
			return
		}
		for i := 0; i < numWrites; i++ {
			pad := strings.Repeat(fmt.Sprintf("%04d", i), 256)
			payload := []byte(fmt.Sprintf(`{"v":%d,"pad":"%s"}`, i, pad))
			if err := c.Refresh(s, func(context.Context) ([]byte, error) {
				time.Sleep(2 * time.Millisecond)
				return payload, nil
			}); err != nil {
				reportErr(err)
				return
			}
		}
	}()

	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-start:
			case <-done:
				return
			}
			for i := 0; i < readsPerReader; i++ {
				select {
				case <-done:
					return
				default:
				}
				begin := time.Now()
				if _, err := c.Read(s); err != nil {
					reportErr(err)
					return
				}
				select {
				case samples <- time.Since(begin):
				case <-done:
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()
	close(samples)

	durations := make([]time.Duration, 0, numReaders*readsPerReader)
	for d := range samples {
		durations = append(durations, d)
	}
	if len(durations) == 0 {
		t.Fatal("no contention samples collected")
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	idx := int(float64(len(durations)) * 0.99)
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	p99 := durations[idx]
	if p99 > 100*time.Millisecond {
		t.Fatalf("Read p99 under contention = %v, want <= 100ms", p99)
	}
}

func TestPruneRemovesInactive(t *testing.T) {
	c := newTestCache(t)
	active := testSource("active", time.Hour, 10*time.Second)
	inactive := testSource("old", time.Hour, 10*time.Second)

	for _, s := range []Source{active, inactive} {
		if err := c.Refresh(s, func(_ context.Context) ([]byte, error) { return []byte(`{}`), nil }); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.Prune([]Source{active}); err != nil {
		t.Fatal(err)
	}

	res, _ := c.Read(active)
	if res.Data == nil {
		t.Error("active source was pruned")
	}
	res2, _ := c.Read(inactive)
	if res2.Data != nil {
		t.Error("inactive source was not pruned")
	}
}

func TestPruneSkipsActiveMarker(t *testing.T) {
	c := newTestCache(t)
	s := testSource("locked", time.Hour, 30*time.Second)
	dir := filepath.Join(c.Root, s.Tier)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(c.dataPath(s), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	m := &refreshMarker{
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		Deadline:  time.Now().UTC().Add(30 * time.Second),
	}
	if err := writeMarker(c.markerPath(s), m); err != nil {
		t.Fatal(err)
	}

	if err := c.Prune(nil); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(c.dataPath(s)); os.IsNotExist(err) {
		t.Error("Prune removed data file of actively-refreshing source")
	}
}
