package serviceimpl

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelsnapshot"
)

// subprocessDiscoveryTTL is the freshness window for subprocess-harness model
// discovery. All subprocess harnesses (claude/codex/gemini) document a 24h
// model-discovery freshness window, so a single TTL applies.
const subprocessDiscoveryTTL = 24 * time.Hour

// subprocessDiscoveryRefreshDeadline bounds a single live PTY scrape. The PTY
// scrape is the slow/flaky operation the snapshot cache exists to keep off the
// hot path; a refresh that exceeds this deadline is abandoned.
const subprocessDiscoveryRefreshDeadline = 60 * time.Second

// subprocessDiscoveryPayload is the JSON shape persisted in the discovery
// cache for a subprocess harness. It carries the full discovery snapshot
// evidence so the read path can resolve any alias locally (pure CPU) without
// re-running a live PTY scrape.
type subprocessDiscoveryPayload struct {
	CapturedAt      time.Time `json:"captured_at"`
	Models          []string  `json:"models,omitempty"`
	ReasoningLevels []string  `json:"reasoning_levels,omitempty"`
	Source          string    `json:"source,omitempty"`
	FreshnessWindow string    `json:"freshness_window,omitempty"`
	Detail          string    `json:"detail,omitempty"`
}

// snapshot reconstructs a harness ModelDiscoverySnapshot from the cached
// payload so alias resolution can run locally against the same evidence the
// live scrape produced.
func (p subprocessDiscoveryPayload) snapshot() harnesses.ModelDiscoverySnapshot {
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      p.CapturedAt,
		Models:          append([]string(nil), p.Models...),
		ReasoningLevels: append([]string(nil), p.ReasoningLevels...),
		Source:          p.Source,
		FreshnessWindow: p.FreshnessWindow,
		Detail:          p.Detail,
	}
}

// subprocessDiscoveryCacheRoot resolves the discovery cache root the same way
// the service snapshot path does (FIZEAU_CACHE_DIR override, else the user
// cache dir). It is a package-level var so tests can point the cache at a temp
// dir.
var subprocessDiscoveryCacheRoot = func() (string, error) {
	return modelsnapshot.DefaultCacheRoot(os.Getenv, os.UserCacheDir)
}

// discoveryCache constructs the subprocess-discovery cache from
// subprocessDiscoveryCacheRoot. The root is resolved on each call so a
// FIZEAU_CACHE_DIR override (e.g. t.Setenv in tests) takes effect; the on-disk
// refresh marker provides cross-call single-flight dedup, mirroring how the
// service snapshot path constructs a fresh Cache per ListModels. On
// root-resolution failure it returns nil; callers degrade to a direct live
// snapshot.
func discoveryCache() *discoverycache.Cache {
	root, err := subprocessDiscoveryCacheRoot()
	if err != nil || root == "" {
		return nil
	}
	return &discoverycache.Cache{Root: root}
}

// subprocessDiscoveryRefresher is the live-scrape function the cache invokes to
// (re)populate a harness's discovery payload. It is a package-level var so
// tests can inject a fake refresher and observe call counts. The default runs
// the harness PTY scrape via DefaultModelSnapshot.
var subprocessDiscoveryRefresher = func(name string, mdh harnesses.ModelDiscoveryHarness) discoverycache.Refresher {
	return func(ctx context.Context) ([]byte, error) {
		var (
			snapshot harnesses.ModelDiscoverySnapshot
			err      error
		)
		if contextual, ok := mdh.(harnesses.ContextModelDiscoveryHarness); ok {
			snapshot, err = contextual.DefaultModelSnapshotWithContext(ctx)
		} else {
			snapshot, err = mdh.DefaultModelSnapshot()
		}
		if err != nil {
			return nil, err
		}
		payload := subprocessDiscoveryPayload{
			CapturedAt:      snapshot.CapturedAt,
			Models:          append([]string(nil), snapshot.Models...),
			ReasoningLevels: append([]string(nil), snapshot.ReasoningLevels...),
			Source:          snapshot.Source,
			FreshnessWindow: snapshot.FreshnessWindow,
			Detail:          snapshot.Detail,
		}
		return json.Marshal(payload)
	}
}

// cachedSubprocessDiscovery returns the snapshot-first discovery payload for a
// subprocess harness. It never blocks the hot path on a live PTY scrape beyond
// a single bounded cold-start sync:
//
//   - When the cache has data, it is decoded and returned immediately and an
//     async MaybeRefresh is fired to warm the cache for next time.
//   - When the cache is cold (no data), exactly one bounded MaybeRefreshSync is
//     run so the first call is not empty; subsequent calls are cache + async.
//
// The bool return reports whether a payload was obtained.
func cachedSubprocessDiscovery(name string, mdh harnesses.ModelDiscoveryHarness) (subprocessDiscoveryPayload, bool) {
	cache := discoveryCache()
	refresher := subprocessDiscoveryRefresher(name, mdh)
	if cache == nil {
		// No cache available: fall back to a single direct scrape.
		data, err := refresher(context.Background())
		if err != nil {
			return subprocessDiscoveryPayload{}, false
		}
		return decodeSubprocessDiscoveryPayload(data)
	}

	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            name + "-models",
		TTL:             subprocessDiscoveryTTL,
		RefreshDeadline: subprocessDiscoveryRefreshDeadline,
	}

	read, err := cache.Read(src)
	if err == nil && len(read.Data) > 0 {
		// Warm read: serve immediately, warm asynchronously.
		cache.MaybeRefresh(src, refresher)
		return decodeSubprocessDiscoveryPayload(read.Data)
	}

	// Cold cache (FEAT-004 F2 fail-open): fire an ASYNC refresh to warm the
	// cache for next time and return empty immediately. The routing hot path
	// must NEVER block on the (up to 60s, flaky PTY) discovery scrape — a CLI
	// /model TUI hiccup or a slow terminal cannot be allowed to stall routing.
	// Returning empty here is safe: the caller fills SupportedModels from the
	// static catalog tier set (service_routing.go catalogTierModelsForHarnessSurface),
	// so a subscription harness stays routable with zero discovered models, and
	// subsequent calls return the live set once the async refresh lands.
	// (Was: a synchronous MaybeRefreshSync that stalled the first cold call up
	// to subprocessDiscoveryRefreshDeadline.)
	//
	// Use a background Refresh (not MaybeRefresh): on a truly cold/absent cache
	// Read errors, and MaybeRefresh bails on that error without scheduling work,
	// so the cache would never warm. Refresh unconditionally refresh+commits and
	// is singleflighted (in-process group + cross-process file marker), so
	// concurrent cold callers coalesce into one bounded PTY scrape.
	go func() { _ = cache.Refresh(src, refresher) }()
	return subprocessDiscoveryPayload{}, false
}

func decodeSubprocessDiscoveryPayload(data []byte) (subprocessDiscoveryPayload, bool) {
	var payload subprocessDiscoveryPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return subprocessDiscoveryPayload{}, false
	}
	return payload, true
}
