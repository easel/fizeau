package fizeau

import (
	"context"
	"time"

	serviceimpl "github.com/easel/fizeau/internal/serviceimpl"
)

// CatalogProbeFunc performs a single /v1/models discovery request against a
// specific endpoint. Implementations should return:
//   - a ReachabilityError (see internal/provider/openai) when the endpoint
//     is unreachable or returns 5xx.
//   - a sentinel-wrapped ErrDiscoveryUnsupported error when /v1/models
//     returns 404 — the endpoint exists but doesn't expose discovery.
//   - a plain error otherwise (auth failures etc. — cache records but
//     does not treat as unreachable).
//
// The ids slice should preserve server-returned order.
type CatalogProbeFunc func(ctx context.Context) (ids []string, err error)

// CatalogResult is what callers receive from catalog discovery. All fields
// are copies, so mutating them does not affect service cache state.
type CatalogResult struct {
	IDs                []string  // server-order model IDs; empty when discovery unsupported or never fetched successfully
	FetchedAt          time.Time // zero when no successful fetch has occurred
	DiscoverySupported bool      // false when /v1/models returned 404; callers passthrough
	LastErr            error     // last probe's error if any; may be a ReachabilityError
	FromCache          bool      // true when served from cache (fresh or stale)
	Stale              bool      // true when served from a stale cache entry (async refresh kicked)
}

// errDiscoveryUnsupported is the public-package sentinel for an endpoint
// that exists but does not expose /v1/models discovery.
var errDiscoveryUnsupported = &discoveryUnsupportedError{}

type discoveryUnsupportedError struct{}

func (e *discoveryUnsupportedError) Error() string {
	return "agent: endpoint does not support /v1/models discovery"
}

// ErrDiscoveryUnsupported returns the sentinel. Callers compare via
// errors.Is(err, ErrDiscoveryUnsupported()).
func ErrDiscoveryUnsupported() error { return errDiscoveryUnsupported }

// The private types below form a temporary root delegate while the remaining
// root integrations are migrated. Concrete cache mechanics and state live in
// internal/serviceimpl; none of these types alias its API-neutral types.
type catalogCacheOptions struct {
	FreshTTL            time.Duration
	LocalFreshTTL       time.Duration
	StaleTTL            time.Duration
	UnreachableCooldown time.Duration
	UnreachableJitter   time.Duration
	AsyncRefreshTimeout time.Duration
	Now                 func() time.Time
	RandInt63n          func(n int64) int64
}

type catalogCacheKey struct {
	inner serviceimpl.CatalogCacheKey
}

type catalogCache struct {
	inner *serviceimpl.CatalogCache
}

type catalogCacheSnapshot struct {
	IDs                []string
	FetchedAt          time.Time
	LastErr            error
	UnreachableAt      time.Time
	DiscoverySupported bool
}

func newCatalogCache(opts catalogCacheOptions) *catalogCache {
	return &catalogCache{inner: serviceimpl.NewCatalogCache(serviceimpl.CatalogCacheOptions{
		FreshTTL:             opts.FreshTTL,
		LocalFreshTTL:        opts.LocalFreshTTL,
		StaleTTL:             opts.StaleTTL,
		UnreachableCooldown:  opts.UnreachableCooldown,
		UnreachableJitter:    opts.UnreachableJitter,
		AsyncRefreshTimeout:  opts.AsyncRefreshTimeout,
		Now:                  opts.Now,
		RandInt63n:           opts.RandInt63n,
		DiscoveryUnsupported: ErrDiscoveryUnsupported(),
	})}
}

func newCatalogCacheKey(baseURL, apiKey string, headers map[string]string) catalogCacheKey {
	return catalogCacheKey{inner: serviceimpl.NewCatalogCacheKey(baseURL, apiKey, headers)}
}

func (c *catalogCache) Get(ctx context.Context, key catalogCacheKey, probe CatalogProbeFunc) (CatalogResult, error) {
	result, err := c.inner.Get(ctx, key.inner, serviceimpl.CatalogProbeFunc(probe))
	return catalogResultFromInternal(result), err
}

func (c *catalogCache) RecordDispatchError(key catalogCacheKey, err error) {
	if c == nil {
		return
	}
	c.inner.RecordDispatchError(key.inner, err)
}

func (c *catalogCache) snapshot(key catalogCacheKey) (catalogCacheSnapshot, bool) {
	if c == nil {
		return catalogCacheSnapshot{}, false
	}
	snapshot, ok := c.inner.Snapshot(key.inner)
	if !ok {
		return catalogCacheSnapshot{}, false
	}
	return catalogCacheSnapshot{
		IDs:                append([]string(nil), snapshot.IDs...),
		FetchedAt:          snapshot.FetchedAt,
		LastErr:            snapshot.LastErr,
		UnreachableAt:      snapshot.UnreachableAt,
		DiscoverySupported: snapshot.DiscoverySupported,
	}, true
}

func catalogResultFromInternal(result serviceimpl.CatalogResult) CatalogResult {
	return CatalogResult{
		IDs:                append([]string(nil), result.IDs...),
		FetchedAt:          result.FetchedAt,
		DiscoverySupported: result.DiscoverySupported,
		LastErr:            result.LastErr,
		FromCache:          result.FromCache,
		Stale:              result.Stale,
	}
}
