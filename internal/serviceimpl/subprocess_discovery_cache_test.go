package serviceimpl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
)

// withTestDiscoveryCache repoints the subprocess discovery cache root at a
// temp dir and installs a fake refresher, restoring both on cleanup. It
// returns a pointer to the refresher call counter and the temp root.
func withTestDiscoveryCache(t *testing.T, payload subprocessDiscoveryPayload) (*int64, string) {
	t.Helper()
	root := retryCleanupTempDir(t)

	origRoot := subprocessDiscoveryCacheRoot
	origRefresher := subprocessDiscoveryRefresher
	t.Cleanup(func() {
		subprocessDiscoveryCacheRoot = origRoot
		subprocessDiscoveryRefresher = origRefresher
	})

	subprocessDiscoveryCacheRoot = func() (string, error) { return root, nil }

	var calls int64
	subprocessDiscoveryRefresher = func(string, harnesses.ModelDiscoveryHarness) discoverycache.Refresher {
		return func(context.Context) ([]byte, error) {
			atomic.AddInt64(&calls, 1)
			return json.Marshal(payload)
		}
	}
	return &calls, root
}

func retryCleanupTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "subprocess-discovery-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		var err error
		for i := 0; i < 20; i++ {
			err = os.RemoveAll(dir)
			if err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("remove temp dir %s: %v", dir, err)
	})
	return dir
}

// waitForRefresherCalls polls until the refresher call count reaches want or
// the deadline elapses, accommodating the async MaybeRefresh goroutine.
func waitForRefresherCalls(t *testing.T, calls *int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(calls) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("refresher calls = %d, want >= %d", atomic.LoadInt64(calls), want)
}

// TestCachedSubprocessDiscoveryColdCacheFailsOpenAsync pins the FEAT-004 F2
// fail-open behavior: a cold cache returns empty IMMEDIATELY (it must never
// block the routing hot path on the up-to-60s PTY scrape) and warms the cache
// ASYNCHRONOUSLY, so a subsequent call serves the discovered payload. The
// caller fills SupportedModels from the static catalog tier set when discovery
// is empty, so routing stays viable in the meantime.
func TestCachedSubprocessDiscoveryColdCacheFailsOpenAsync(t *testing.T) {
	payload := subprocessDiscoveryPayload{
		CapturedAt: time.Now().UTC(),
		Models:     []string{"claude-opus-4", "claude-sonnet-4"},
		Source:     "pty",
	}
	calls, _ := withTestDiscoveryCache(t, payload)

	// First (cold) call must NOT block and must return empty fail-open.
	got, ok := cachedSubprocessDiscovery("claude", nil)
	if ok || len(got.Models) != 0 {
		t.Fatalf("cold cache: want empty fail-open (ok=false, 0 models), got ok=%v models=%#v", ok, got.Models)
	}

	// The refresh is fired asynchronously to warm the cache.
	waitForRefresherCalls(t, calls, 1)

	// Once the async refresh commits, a subsequent call serves the discovered
	// payload. Poll because the commit lands shortly after the refresher runs.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got2, ok2 := cachedSubprocessDiscovery("claude", nil)
		if ok2 {
			if len(got2.Models) != 2 {
				t.Fatalf("warm cache models = %#v, want 2", got2.Models)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("warm cache did not serve payload within deadline (async commit did not land)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCachedSubprocessDiscoveryFreshReadDoesNotCallRefresher(t *testing.T) {
	payload := subprocessDiscoveryPayload{
		CapturedAt: time.Now().UTC(),
		Models:     []string{"claude-opus-4"},
		Source:     "pty",
	}
	calls, root := withTestDiscoveryCache(t, payload)

	// Pre-seed a FRESH cache file directly so the read path finds fresh data.
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            "claude-models",
		TTL:             subprocessDiscoveryTTL,
		RefreshDeadline: subprocessDiscoveryRefreshDeadline,
	}
	cache := &discoverycache.Cache{Root: root}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) {
		return json.Marshal(payload)
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Reset the counter: the seed write went through a direct refresher, not
	// the injected one.
	atomic.StoreInt64(calls, 0)

	got, ok := cachedSubprocessDiscovery("claude", nil)
	if !ok {
		t.Fatalf("fresh read: expected payload, got ok=false")
	}
	if len(got.Models) != 1 {
		t.Fatalf("fresh read models = %#v, want 1", got.Models)
	}
	// A fresh read must neither sync-refresh nor schedule an async refresh.
	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt64(calls); n != 0 {
		t.Fatalf("fresh read refresher calls = %d, want 0 (cache served, no PTY)", n)
	}

	// Confirm the seeded file is at the expected path under the temp root.
	if _, err := os.Stat(filepath.Join(root, "discovery", "claude-models.json")); err != nil {
		t.Fatalf("expected seeded cache file: %v", err)
	}
}

func TestCachedSubprocessDiscoverySubsequentReadServesCachePlusAsyncRefresh(t *testing.T) {
	payload := subprocessDiscoveryPayload{
		CapturedAt: time.Now().UTC(),
		Models:     []string{"claude-opus-4"},
		Source:     "pty",
	}
	calls, root := withTestDiscoveryCache(t, payload)

	// Seed a cache file, then age it past the TTL so Read returns data but the
	// read path schedules an async refresh.
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            "claude-models",
		TTL:             subprocessDiscoveryTTL,
		RefreshDeadline: subprocessDiscoveryRefreshDeadline,
	}
	cache := &discoverycache.Cache{Root: root}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) {
		return json.Marshal(payload)
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Age the data file beyond the TTL.
	dataPath := filepath.Join(root, "discovery", "claude-models.json")
	old := time.Now().Add(-2 * subprocessDiscoveryTTL)
	if err := os.Chtimes(dataPath, old, old); err != nil {
		t.Fatalf("age cache file: %v", err)
	}
	atomic.StoreInt64(calls, 0)

	// First read after staleness: serves cached data immediately AND fires an
	// async refresh (exactly one, claimed synchronously by MaybeRefresh).
	got, ok := cachedSubprocessDiscovery("claude", nil)
	if !ok {
		t.Fatalf("stale read: expected cached payload, got ok=false")
	}
	if len(got.Models) != 1 {
		t.Fatalf("stale read models = %#v, want 1 (served from cache)", got.Models)
	}
	waitForRefresherCalls(t, calls, 1)
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Fatalf("stale read refresher calls = %d, want exactly 1 (async warm)", n)
	}
}
