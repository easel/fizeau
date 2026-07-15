package serviceimpl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/provider/openai"
	"golang.org/x/sync/singleflight"
)

// CatalogProbeFunc performs a single /v1/models discovery request against a
// specific endpoint. Implementations should return:
//   - a ReachabilityError (see internal/provider/openai) when the endpoint
//     is unreachable or returns 5xx.
//   - an error wrapping the caller-supplied discovery-unsupported sentinel
//     returns 404 — the endpoint exists but doesn't expose discovery.
//   - a plain error otherwise (auth failures etc. — cache records but
//     does not treat as unreachable).
//
// The ids slice should preserve server-returned order.
type CatalogProbeFunc func(ctx context.Context) (ids []string, err error)

// Default cache timings. Overridable via CatalogCacheOptions.
const (
	defaultCatalogFreshTTL            = 60 * time.Second
	defaultCatalogLocalFreshTTL       = 10 * time.Second
	defaultCatalogStaleTTL            = 10 * time.Minute
	defaultCatalogUnreachableCooldown = 30 * time.Second
	defaultCatalogUnreachableJitter   = 5 * time.Second
	defaultCatalogAsyncRefreshTimeout = 30 * time.Second
)

// CatalogCacheKey identifies a cache entry. It uses fingerprinted auth so
// different API keys for the same baseURL don't share cached state.
type CatalogCacheKey struct {
	BaseURL     string
	APIKeyHash  [sha256.Size]byte
	HeadersHash [sha256.Size]byte
}

// String returns a deterministic key identity suitable for singleflight.
// The hash is already collision-resistant; concatenate the three components
// so the singleflight deduplicates by the full identity, not by baseURL alone.
func (k CatalogCacheKey) String() string {
	var b strings.Builder
	b.WriteString(k.BaseURL)
	b.WriteByte('|')
	for _, x := range k.APIKeyHash {
		b.WriteByte(hexDigit(x >> 4))
		b.WriteByte(hexDigit(x & 0x0f))
	}
	b.WriteByte('|')
	for _, x := range k.HeadersHash {
		b.WriteByte(hexDigit(x >> 4))
		b.WriteByte(hexDigit(x & 0x0f))
	}
	return b.String()
}

func hexDigit(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}

// catalogEntry is the per-key cached state. Never exposed to callers without
// deep-copying, so cache mutation is impossible from the outside.
type catalogEntry struct {
	IDs                []string  // last successful discovery (server order)
	FetchedAt          time.Time // zero when never fetched
	LastErr            error     // the most recent error; nil on success
	UnreachableAt      time.Time // zero unless last attempt produced a ReachabilityError
	DiscoverySupported bool      // false when /v1/models returns 404 (passthrough mode)
}

// CatalogCache is the service-scope live-catalog cache with stale-while-
// revalidate semantics + unreachable cooldown + cold-miss coalescing via
// singleflight. One instance per service.
type CatalogCache struct {
	mu  sync.Mutex
	mem map[CatalogCacheKey]*catalogEntry
	sf  singleflight.Group
	// opts is captured at construction; read without mu (immutable).
	opts CatalogCacheOptions
}

// CatalogCacheOptions controls timings, caller-owned sentinel identity, and
// test hooks. Production callers normally rely on timing defaults.
type CatalogCacheOptions struct {
	FreshTTL time.Duration
	// LocalFreshTTL overrides FreshTTL for endpoints whose provider
	// deployment_class is local (local_free / community_self_hosted).
	// Local servers — LMStudio on a laptop, vLLM on a workstation —
	// suspend/resume far more often than managed clouds, so the cache
	// must re-probe sooner than the default 60s. Zero falls back to
	// defaultCatalogLocalFreshTTL.
	LocalFreshTTL       time.Duration
	StaleTTL            time.Duration
	UnreachableCooldown time.Duration
	UnreachableJitter   time.Duration
	AsyncRefreshTimeout time.Duration
	Now                 func() time.Time // injectable for tests
	RandInt63n          func(n int64) int64
	// DiscoveryUnsupported is the caller-owned sentinel used to classify a
	// discovery endpoint's unsupported response. Keeping it supplied avoids
	// coupling this internal implementation to the root public error identity.
	DiscoveryUnsupported error
}

func (o CatalogCacheOptions) withDefaults() CatalogCacheOptions {
	if o.FreshTTL <= 0 {
		o.FreshTTL = defaultCatalogFreshTTL
	}
	if o.LocalFreshTTL <= 0 {
		o.LocalFreshTTL = defaultCatalogLocalFreshTTL
	}
	if o.StaleTTL <= 0 {
		o.StaleTTL = defaultCatalogStaleTTL
	}
	if o.UnreachableCooldown <= 0 {
		o.UnreachableCooldown = defaultCatalogUnreachableCooldown
	}
	// UnreachableJitter may legitimately be 0 (tests); don't override.
	if o.AsyncRefreshTimeout <= 0 {
		o.AsyncRefreshTimeout = defaultCatalogAsyncRefreshTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RandInt63n == nil {
		o.RandInt63n = cryptoRandInt63n
	}
	return o
}

// cryptoRandInt63n uses crypto/rand for the jitter randomization. Not
// performance-critical — at most one call per unreachable miss.
func cryptoRandInt63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	r, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		// Vanishingly unlikely; fall back to a non-zero bound so we
		// never return the exact cooldown and risk synchronized retries
		// across many goroutines.
		return int64(time.Now().UnixNano()) % n
	}
	return r.Int64()
}

// NewCatalogCache constructs an empty service-scoped catalog cache.
func NewCatalogCache(opts CatalogCacheOptions) *CatalogCache {
	return &CatalogCache{
		mem:  make(map[CatalogCacheKey]*catalogEntry),
		opts: opts.withDefaults(),
	}
}

// NewCatalogCacheKey builds a cache key from endpoint identity. api_key and
// headers are hashed so the key is stable but doesn't carry secrets in the
// map key's string representation.
func NewCatalogCacheKey(baseURL, apiKey string, headers map[string]string) CatalogCacheKey {
	return CatalogCacheKey{
		BaseURL:     baseURL,
		APIKeyHash:  sha256.Sum256([]byte(apiKey)),
		HeadersHash: hashHeaders(headers),
	}
}

// hashHeaders fingerprints a headers map in a deterministic way so ordering
// doesn't affect the hash.
func hashHeaders(headers map[string]string) [sha256.Size]byte {
	if len(headers) == 0 {
		return sha256.Sum256(nil)
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	lenBuf := make([]byte, 8)
	for _, k := range keys {
		v := headers[k]
		binary.BigEndian.PutUint64(lenBuf, uint64(len(k)))
		_, _ = h.Write(lenBuf)
		_, _ = h.Write([]byte(k))
		binary.BigEndian.PutUint64(lenBuf, uint64(len(v)))
		_, _ = h.Write(lenBuf)
		_, _ = h.Write([]byte(v))
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// CatalogResult is what callers receive from Get. All fields are copies —
// mutating them does not affect cache state.
type CatalogResult struct {
	IDs                []string  // server-order model IDs; empty when discovery unsupported or never fetched successfully
	FetchedAt          time.Time // zero when no successful fetch has occurred
	DiscoverySupported bool      // false when /v1/models returned 404; callers passthrough
	LastErr            error     // last probe's error if any; may be a ReachabilityError
	FromCache          bool      // true when served from cache (fresh or stale)
	Stale              bool      // true when served from a stale cache entry (async refresh kicked)
}

// Get looks up the cache and probes if necessary. Returns a CatalogResult
// plus an error. Semantics:
//
//   - Fresh hit: returns cached IDs + no error. FromCache=true, Stale=false.
//   - Stale hit: returns cached IDs + kicks background refresh via
//     singleflight. FromCache=true, Stale=true.
//   - Unreachable within cooldown: returns the cached ReachabilityError.
//     FromCache=true. No fresh probe attempted.
//   - Cold miss: synchronous probe via singleflight (one probe for N
//     concurrent callers). Populates cache with result.
//
// The CatalogResult is always returned; inspect it even when err is non-nil.
func (c *CatalogCache) Get(ctx context.Context, key CatalogCacheKey, probe CatalogProbeFunc) (CatalogResult, error) {
	c.mu.Lock()
	e, ok := c.mem[key]
	now := c.opts.Now()
	if ok {
		// Unreachable cooldown active?
		if !e.UnreachableAt.IsZero() {
			cooldown := c.opts.UnreachableCooldown
			if c.opts.UnreachableJitter > 0 {
				cooldown += time.Duration(c.opts.RandInt63n(int64(c.opts.UnreachableJitter)))
			}
			if now.Sub(e.UnreachableAt) < cooldown {
				result := snapshotEntry(e)
				result.FromCache = true
				c.mu.Unlock()
				return result, e.LastErr
			}
		}
		// Fresh?
		if !e.FetchedAt.IsZero() && now.Sub(e.FetchedAt) < c.opts.FreshTTL && e.LastErr == nil {
			result := snapshotEntry(e)
			result.FromCache = true
			c.mu.Unlock()
			return result, nil
		}
		// Stale — within StaleTTL of last successful fetch?
		if !e.FetchedAt.IsZero() && now.Sub(e.FetchedAt) < c.opts.StaleTTL && e.LastErr == nil {
			result := snapshotEntry(e)
			result.FromCache = true
			result.Stale = true
			c.mu.Unlock()
			// Kick async refresh — singleflight coalesces parallel callers.
			parentCtx := context.WithoutCancel(ctx)
			go func() {
				ctx, cancel := context.WithTimeout(parentCtx, c.opts.AsyncRefreshTimeout)
				defer cancel()
				_, _, _ = c.sf.Do(key.String(), func() (interface{}, error) {
					return c.probe(ctx, key, probe)
				})
			}()
			return result, nil
		}
	}
	c.mu.Unlock()

	// Cold miss or beyond StaleTTL or previous failure outside cooldown:
	// synchronous probe via singleflight.
	r, err, _ := c.sf.Do(key.String(), func() (interface{}, error) {
		return c.probe(ctx, key, probe)
	})
	if r == nil {
		return CatalogResult{LastErr: err}, err
	}
	result := r.(CatalogResult)
	return result, err
}

// probe executes the probe, updates the cache, and returns a CatalogResult.
// Runs inside singleflight — one call per key even under concurrent misses.
func (c *CatalogCache) probe(ctx context.Context, key CatalogCacheKey, probeFn CatalogProbeFunc) (CatalogResult, error) {
	ids, err := probeFn(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Now()
	e, ok := c.mem[key]
	if !ok {
		e = &catalogEntry{DiscoverySupported: true}
		c.mem[key] = e
	}
	e.LastErr = err
	switch {
	case err == nil:
		// Successful probe.
		e.IDs = append([]string(nil), ids...)
		e.FetchedAt = now
		e.UnreachableAt = time.Time{}
		e.DiscoverySupported = true
	case c.isDiscoveryUnsupported(err):
		// 404 on /v1/models — endpoint exists but doesn't expose discovery.
		// Fall back to passthrough: callers send their original model string
		// without normalization.
		e.DiscoverySupported = false
		e.UnreachableAt = time.Time{}
		// IDs intentionally left as-is from prior success (if any); most
		// likely empty on fresh endpoints.
	case isReachabilityErr(err):
		// 5xx or network failure — start cooldown.
		e.UnreachableAt = now
	default:
		// Other errors (auth 401/403, unexpected shape). Record but do NOT
		// treat as unreachable. Downstream Chat calls may still succeed
		// if auth works at the chat layer (unusual but possible), or will
		// surface their own errors.
		e.UnreachableAt = time.Time{}
	}
	return snapshotEntry(e), err
}

// snapshotEntry copies entry state for safe return outside the lock.
func snapshotEntry(e *catalogEntry) CatalogResult {
	return CatalogResult{
		IDs:                append([]string(nil), e.IDs...),
		FetchedAt:          e.FetchedAt,
		DiscoverySupported: e.DiscoverySupported,
		LastErr:            e.LastErr,
	}
}

func (c *CatalogCache) isDiscoveryUnsupported(err error) bool {
	return err != nil && c.opts.DiscoveryUnsupported != nil && errors.Is(err, c.opts.DiscoveryUnsupported)
}

// isReachabilityErr reports whether err carries the openai.ErrEndpointUnreachable
// sentinel. Probe implementations wrap transport/5xx failures as
// *openai.ReachabilityError; this cache uses errors.Is to detect them.
func isReachabilityErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, openai.ErrEndpointUnreachable)
}

// IsDispatchReachabilityFailure reports whether a chat-completions dispatch
// error should be treated as endpoint-unreachable evidence. Mirrors the
// classifier in probeOpenAIModels so probe-time and dispatch-time signals
// agree on what counts as an unreachable endpoint.
func IsDispatchReachabilityFailure(err error) bool {
	if err == nil {
		return false
	}
	if isReachabilityErr(err) {
		return true
	}
	if IsNetworkFailure(err) {
		return true
	}
	return IsServerError(err.Error())
}

// RecordDispatchError feeds a chat-completions dispatch failure back into the
// cache so the next routing pass can skip the endpoint instead of replaying
// the timeout. Errors that don't classify as reachability failures (auth 401,
// malformed body, etc.) are ignored — they're cheap signals from the dispatch
// layer that don't indicate the endpoint is down.
//
// The bug this closes: probeOpenAIModels was the sole writer for
// UnreachableAt, so a dead endpoint discovered only at dispatch time stayed
// "available" in the cache until the next /v1/models probe — costing every
// concurrent caller within FreshTTL a 5-second i/o timeout.
func (c *CatalogCache) RecordDispatchError(key CatalogCacheKey, err error) {
	if c == nil || err == nil {
		return
	}
	if !IsDispatchReachabilityFailure(err) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Now()
	e, ok := c.mem[key]
	if !ok {
		e = &catalogEntry{DiscoverySupported: true}
		c.mem[key] = e
	}
	e.LastErr = err
	e.UnreachableAt = now
}

// freshTTLFor resolves the freshness window for a given deployment_class.
// Local-class endpoints (laptop/workstation servers) get LocalFreshTTL
// because their up/down state changes far more often than managed-cloud
// endpoints. Unknown classes get the default FreshTTL.
func (c *CatalogCache) freshTTLFor(deploymentClass string) time.Duration {
	if c == nil {
		return defaultCatalogFreshTTL
	}
	if isLocalDeploymentClass(deploymentClass) {
		return c.opts.LocalFreshTTL
	}
	return c.opts.FreshTTL
}

// IsServerError reports whether a discovery error message contains one of the
// exact HTTP 500-505 markers historically classified as endpoint failures.
func IsServerError(msg string) bool {
	for _, prefix := range []string{"HTTP 500", "HTTP 501", "HTTP 502", "HTTP 503", "HTTP 504", "HTTP 505"} {
		if strings.Contains(msg, prefix) {
			return true
		}
	}
	return false
}

// IsNetworkFailure reports whether err has one of the exact transport marker
// spellings used by discovery and dispatch feedback. Matching remains
// intentionally case-sensitive, including both historical TLS spellings.
func IsNetworkFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	markers := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"tls: handshake",
		"TLS handshake",
		"i/o timeout",
		"dial tcp",
		"broken pipe",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	// Explicitly do not classify context.DeadlineExceeded: that is the
	// caller's own deadline rather than endpoint evidence.
	return errors.Is(err, ErrDialishNetwork)
}

// ErrDialishNetwork lets an internal caller mark a transport-shaped failure
// whose message does not contain one of the literal compatibility markers.
var ErrDialishNetwork = errors.New("agent: network failure")

// ExtractStatusCode pulls the first status code from an "HTTP NNN" marker.
// It returns zero when the marker is absent or malformed.
func ExtractStatusCode(msg string) int {
	const prefix = "HTTP "
	idx := strings.Index(msg, prefix)
	if idx < 0 {
		return 0
	}
	tail := msg[idx+len(prefix):]
	if len(tail) < 3 {
		return 0
	}
	code := 0
	for i := 0; i < 3 && i < len(tail); i++ {
		char := tail[i]
		if char < '0' || char > '9' {
			return 0
		}
		code = code*10 + int(char-'0')
	}
	return code
}

// isLocalDeploymentClass reports whether deployment_class names a
// laptop/workstation-class endpoint that may suspend/resume between
// dispatches. Matches the local_free / community_self_hosted values produced
// by the model catalog (see internal/modelcatalog/power.go) plus the
// shorthand "local" for callers that don't carry the full class string.
func isLocalDeploymentClass(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "local", "local_free", "community_self_hosted":
		return true
	default:
		return false
	}
}
