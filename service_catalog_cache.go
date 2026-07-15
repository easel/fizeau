package fizeau

import (
	"context"
	"time"
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
