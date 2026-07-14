// Package discoverycache implements the per-source on-disk cache described in
// ADR-012. It provides a two-tier lock + long-lived refresh marker pattern so
// that slow discovery IO (PTY ~30 s, HTTP ~316 ms) never blocks UI reads.
//
// Public surface: Cache.Read, Cache.Refresh, Cache.MaybeRefresh, Cache.Prune.
// The Refresher function is injected; the cache manages single-flight
// deduplication, atomic writes, and crash recovery.
package discoverycache

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	lockAcquisitionTimeout = 100 * time.Millisecond
	lockPollInterval       = 5 * time.Millisecond
	waitPollInterval       = 250 * time.Millisecond
	stalenessMultiplier    = time.Duration(2)
	refreshFailureCooldown = 250 * time.Millisecond
)

// Cache is the root handle. Root is the base directory (e.g. ~/.cache/fizeau).
// Each Cache instance owns one in-process singleflight.Group; a new Cache per
// process is the normal usage.
type Cache struct {
	Root string
	sf   singleflight.Group

	waitForRefreshHook func(Source)
}

// Source describes one cacheable data stream (one file under Root/Tier/Name.json).
type Source struct {
	// Tier is "discovery" or "runtime".
	Tier string
	// Name is the kebab-case source identifier (e.g. "openrouter").
	Name string
	// TTL controls freshness: Read returns Fresh=true when Age < TTL.
	TTL time.Duration
	// RefreshDeadline is the maximum time a refresh is expected to take.
	// Also controls the staleness threshold: 2 × RefreshDeadline.
	RefreshDeadline time.Duration
}

func (s Source) key() string { return s.Tier + "/" + s.Name }

// ReadResult is the output of Cache.Read.
type ReadResult struct {
	// Data is the raw bytes from the cache file; nil if no file exists.
	Data []byte
	// Age is how long ago the data was written (0 if absent).
	Age time.Duration
	// Fresh is true when Age < TTL and Data is non-nil.
	Fresh bool
	// Stale is true when Data is nil or Age >= TTL.
	Stale bool
}

// RefreshState reports the current marker state for a source. It is used by
// higher layers to surface refresh_failed / in-flight diagnostics without
// re-reading raw marker files.
type RefreshState struct {
	InFlight  bool
	Failed    bool
	LastError string
	StartedAt time.Time
	Deadline  time.Time
}

// Refresher is the source-fetch function injected by the caller. The cache
// handles single-flight deduplication, the atomic write, and marker lifecycle.
type Refresher func(ctx context.Context) ([]byte, error)

// Read returns cached data without any IO beyond reading the local file.
// It never blocks on network or PTY. If no cache file exists, Data is nil and
// Stale is true. Stale data is always returned without error; the caller
// decides whether to trigger a background refresh.
func (c *Cache) Read(s Source) (ReadResult, error) {
	path := c.dataPath(s)
	data, err := os.ReadFile(path) // #nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return ReadResult{Stale: true}, nil
	}
	if err != nil {
		return ReadResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		// File was just there; best-effort fallback.
		return ReadResult{Data: data, Stale: true}, nil
	}
	age := time.Since(info.ModTime())
	if age < 0 {
		age = 0
	}
	fresh := age < s.TTL
	return ReadResult{
		Data:  data,
		Age:   age,
		Fresh: fresh,
		Stale: !fresh,
	}, nil
}

// Prune removes data files (and their sidecar lock/marker/tmp files) for
// sources not listed in activeSources. Sources with an active .refreshing
// marker are skipped. Prune acquires the tier-1 lock before removing each
// source's files.
func (c *Cache) Prune(activeSources []Source) error {
	active := make(map[string]bool, len(activeSources))
	for _, s := range activeSources {
		active[s.key()] = true
	}

	return filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		rel, rerr := filepath.Rel(c.Root, path)
		if rerr != nil {
			return nil
		}
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) != 2 {
			return nil
		}
		tier := parts[0]
		name := strings.TrimSuffix(parts[1], ".json")
		if strings.ContainsRune(name, filepath.Separator) {
			return nil // nested dir, skip
		}

		if active[tier+"/"+name] {
			return nil
		}

		s := Source{Tier: tier, Name: name}

		// Skip sources with an active refresh in progress.
		m, _ := readMarker(c.markerPath(s))
		if isMarkerActiveForPrune(m) {
			return nil
		}

		release, lerr := c.acquireLock(s)
		if lerr != nil {
			return nil // can't lock; skip this source
		}
		defer release()

		// Re-check under lock.
		m2, _ := readMarker(c.markerPath(s))
		if isMarkerActiveForPrune(m2) {
			return nil
		}

		_ = os.Remove(c.dataPath(s))
		_ = os.Remove(c.markerPath(s))
		_ = os.Remove(c.tmpPath(s))
		return nil
	})
}

// RefreshState reports the current refresh marker status for s.
func (c *Cache) RefreshState(s Source) (RefreshState, error) {
	m, err := readMarker(c.markerPath(s))
	if err != nil || m == nil {
		return RefreshState{}, err
	}
	return RefreshState{
		InFlight:  !isStale(s, m),
		Failed:    strings.TrimSpace(m.LastError) != "",
		LastError: strings.TrimSpace(m.LastError),
		StartedAt: m.StartedAt,
		Deadline:  m.Deadline,
	}, nil
}

// SetWaitForRefreshHookForTesting lets tests observe when Refresh joins an
// already active marker-owned refresh and begins waiting for it to finish.
func (c *Cache) SetWaitForRefreshHookForTesting(h func(Source)) {
	c.waitForRefreshHook = h
}

// ---- internal ---------------------------------------------------------------

type claimResult struct {
	claimed bool
	marker  *refreshMarker
}

// claimRefresh implements Algorithm 1 from ADR-012. Must be called with the
// source directory already created (ensured by refreshAndCommit).
func (c *Cache) claimRefresh(s Source) (claimResult, error) {
	release, err := c.acquireLock(s)
	if err != nil {
		return claimResult{}, err
	}
	defer release()

	existing, _ := readMarker(c.markerPath(s))
	if existing != nil {
		if isStale(s, existing) {
			_ = os.Remove(c.markerPath(s))
		} else {
			return claimResult{claimed: false, marker: existing}, nil
		}
	}

	m := &refreshMarker{
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		Deadline:  time.Now().UTC().Add(s.RefreshDeadline),
	}
	if werr := writeMarker(c.markerPath(s), m); werr != nil {
		return claimResult{}, werr
	}
	return claimResult{claimed: true}, nil
}

// refreshAndCommit implements Algorithm 2 from ADR-012.
func (c *Cache) refreshAndCommit(s Source, fn Refresher) error {
	if err := os.MkdirAll(filepath.Join(c.Root, s.Tier), 0o750); err != nil {
		return err
	}

	claim, err := c.claimRefresh(s)
	if err != nil {
		return err
	}
	if !claim.claimed {
		// A failed marker represents a retry cooldown, not work that is still in
		// flight. Forced refresh callers must honor that cooldown without waiting
		// for the source's (potentially much longer) refresh deadline.
		if strings.TrimSpace(claim.marker.LastError) != "" {
			return nil
		}
		if c.waitForRefreshHook != nil {
			c.waitForRefreshHook(s)
		}
		// Another process is refreshing; wait for it.
		return c.waitForRefresh(s, claim.marker)
	}

	return c.refreshWithClaim(s, fn)
}

// refreshWithClaim assumes the current process already owns the refresh
// marker.
func (c *Cache) refreshWithClaim(s Source, fn Refresher) error {
	// Run slow IO outside any lock.
	refreshCtx := context.Background()
	var cancel context.CancelFunc
	if s.RefreshDeadline > 0 {
		refreshCtx, cancel = context.WithTimeout(refreshCtx, s.RefreshDeadline)
	}
	if cancel != nil {
		defer cancel()
	}
	data, fetchErr := fn(refreshCtx)
	if fetchErr != nil {
		c.recordRefreshFailure(s, fetchErr)
		return fetchErr
	}

	if werr := atomicWrite(c.dataPath(s), data); werr != nil {
		c.recordRefreshFailure(s, werr)
		return werr
	}

	c.releaseMarker(s)
	return nil
}

// recordRefreshFailure keeps the marker in place with the failure details so
// callers observe refresh_failed state and do not immediately re-probe.
func (c *Cache) recordRefreshFailure(s Source, refreshErr error) {
	release, err := c.acquireLock(s)
	if err != nil {
		return
	}
	defer release()

	m, _ := readMarker(c.markerPath(s))
	if m == nil || m.PID != os.Getpid() {
		return
	}
	now := time.Now().UTC()
	m.StartedAt = now
	m.Deadline = now.Add(refreshFailureCooldown)
	m.LastError = refreshErr.Error()
	_ = writeMarker(c.markerPath(s), m)
}

// waitForRefresh implements Algorithm 3 from ADR-012. Polls until the marker
// disappears or becomes stale, then returns (data has been written).
func (c *Cache) waitForRefresh(s Source, m *refreshMarker) error {
	// Poll until deadline + staleness_threshold or s.RefreshDeadline, whichever first.
	threshold := s.RefreshDeadline * stalenessMultiplier
	absoluteDeadline := m.Deadline.Add(threshold)
	maxWait := time.Now().Add(s.RefreshDeadline)
	if maxWait.Before(absoluteDeadline) {
		absoluteDeadline = maxWait
	}

	for time.Now().Before(absoluteDeadline) {
		time.Sleep(waitPollInterval)
		current, _ := readMarker(c.markerPath(s))
		if current == nil || isStale(s, current) {
			return nil
		}
	}
	return nil // timed out; caller will read whatever is on disk
}

// releaseMarker removes the .refreshing marker under the tier-1 lock, but
// only if our PID is still recorded (guard against PID-reuse races, per §2
// of refreshAndCommit pseudocode in ADR-012).
func (c *Cache) releaseMarker(s Source) {
	release, err := c.acquireLock(s)
	if err != nil {
		return
	}
	defer release()
	m, _ := readMarker(c.markerPath(s))
	if m != nil && m.PID == os.Getpid() {
		_ = os.Remove(c.markerPath(s))
	}
}

// ---- path helpers -----------------------------------------------------------

func (c *Cache) dataPath(s Source) string {
	return filepath.Join(c.Root, s.Tier, s.Name+".json")
}

func (c *Cache) lockPath(s Source) string {
	return filepath.Join(c.Root, s.Tier, s.Name+".lock")
}

func (c *Cache) markerPath(s Source) string {
	return filepath.Join(c.Root, s.Tier, s.Name+".refreshing")
}

func (c *Cache) tmpPath(s Source) string {
	return filepath.Join(c.Root, s.Tier, s.Name+".json.tmp")
}
