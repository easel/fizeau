package fizeau

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// openrouterCreditFailureMode classifies why a credit probe attempt did not
// return a balance reading. Sibling failure-mode bead surfaces these as
// distinct routing filter reasons so operators can distinguish "key rotated
// out" from "openrouter.ai unreachable".
type openrouterCreditFailureMode string

const (
	// openrouterCreditFailureNone is the zero value — probe succeeded, or
	// no attempt has been recorded yet.
	openrouterCreditFailureNone openrouterCreditFailureMode = ""
	// openrouterCreditFailureCredentialInvalid signals a 401 (or 403) from
	// /api/v1/credits: the key is present but rejected. Maps to
	// FilterReasonCredentialInvalid.
	openrouterCreditFailureCredentialInvalid openrouterCreditFailureMode = openrouterCreditFailureMode(
		routing.FilterReasonCredentialInvalid,
	)
	// openrouterCreditFailureProviderUnreachable signals a transient
	// transport error or a non-401 server-side failure (4xx/5xx). Maps to
	// FilterReasonProviderUnreachable and recovers on the next successful
	// probe.
	openrouterCreditFailureProviderUnreachable openrouterCreditFailureMode = "provider_unreachable"
)

// DefaultOpenrouterCreditBalanceThresholdUSD is the floor below which a
// cached openrouter account balance triggers the credit-balance gate.
// Operators can override per-provider via ServiceProviderEntry.
const DefaultOpenrouterCreditBalanceThresholdUSD = 0.50

// DefaultOpenrouterCreditProbeTTL bounds how long a cached balance reading
// stays fresh before the next routing pass re-probes /api/v1/credits.
// Picked at the middle of the 5–15 minute band specified by the bead so
// the cache amortizes across drains while still detecting top-ups quickly.
const DefaultOpenrouterCreditProbeTTL = 10 * time.Minute

// openrouterCreditProbeTimeout bounds one synchronous credit probe so a
// hung openrouter.ai response cannot stall a routing pass.
const openrouterCreditProbeTimeout = 5 * time.Second

// openrouterCreditFreshnessSource labels candidate.QuotaFreshnessAt overrides
// derived from the credit probe so operators can tell which freshness signal
// fed the row.
const openrouterCreditFreshnessSource = "openrouter_credits_probe"

// openrouterCreditRecord is one cached balance reading. The store keeps the
// last attempt timestamp (regardless of success) so probe failures still
// honor the TTL.
type openrouterCreditRecord struct {
	BalanceUSD float64
	ObservedAt time.Time
	// HasBalance is false when the most recent probe attempt did not return
	// a balance reading (transport error, non-200, or empty response). The
	// failure-mode fields below classify the failure so the service layer
	// can project distinct routing filter reasons.
	HasBalance bool
	// FailureMode classifies the most recent probe failure (zero value =
	// no failure on the cached attempt). FailureHTTPStatus and FailureMessage
	// carry the originating evidence so routing_decision rows can surface
	// status code or transport error class without log spelunking. The cache
	// retains a single record per provider; a successful probe clears the
	// failure fields (FailureMode = "", HasBalance = true).
	FailureMode       openrouterCreditFailureMode
	FailureHTTPStatus int
	FailureMessage    string
}

// openrouterCreditStore caches openrouter account balance readings with a
// TTL gate so repeated routing passes within the window share one HTTP
// round-trip to /api/v1/credits.
//
// The store is safe for concurrent use; a per-provider single-flight token
// ensures concurrent routing passes coalesce on one probe.
type openrouterCreditStore struct {
	mu        sync.Mutex
	records   map[string]openrouterCreditRecord
	inFlight  map[string]chan struct{}
	transport http.RoundTripper // nil = http.DefaultClient.Transport
}

func newOpenrouterCreditStore() *openrouterCreditStore {
	return &openrouterCreditStore{
		records:  make(map[string]openrouterCreditRecord),
		inFlight: make(map[string]chan struct{}),
	}
}

// Lookup returns the cached balance record for provider, if any.
func (s *openrouterCreditStore) Lookup(provider string) (openrouterCreditRecord, bool) {
	if s == nil {
		return openrouterCreditRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[provider]
	return rec, ok
}

// Record forces a balance record for provider. Used by tests; the production
// path goes through EnsureFresh.
func (s *openrouterCreditStore) Record(provider string, rec openrouterCreditRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[provider] = rec
}

// EnsureFresh issues a synchronous /api/v1/credits probe when the cached
// record for provider is missing or older than ttl. Concurrent callers
// coalesce on a single in-flight probe via a per-provider waiter channel.
func (s *openrouterCreditStore) EnsureFresh(ctx context.Context, provider, baseURL, apiKey string, now time.Time, ttl time.Duration) {
	if s == nil || provider == "" || strings.TrimSpace(apiKey) == "" {
		return
	}
	s.mu.Lock()
	rec, ok := s.records[provider]
	if ok && ttl > 0 && now.Sub(rec.ObservedAt) < ttl {
		s.mu.Unlock()
		return
	}
	if waiter, busy := s.inFlight[provider]; busy {
		s.mu.Unlock()
		select {
		case <-waiter:
		case <-ctx.Done():
		}
		return
	}
	waiter := make(chan struct{})
	s.inFlight[provider] = waiter
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inFlight, provider)
		close(waiter)
		s.mu.Unlock()
	}()

	result := s.probe(ctx, baseURL, apiKey)
	next := openrouterCreditRecord{ObservedAt: now}
	if result.OK {
		next.BalanceUSD = result.Balance
		next.HasBalance = true
	} else {
		next.FailureMode = result.FailureMode
		next.FailureHTTPStatus = result.HTTPStatus
		next.FailureMessage = result.Message
	}
	s.mu.Lock()
	s.records[provider] = next
	s.mu.Unlock()
}

// openrouterCreditProbeResult is the classified outcome of one /api/v1/credits
// request. On OK=true the Balance is populated; otherwise FailureMode plus
// HTTPStatus or Message describe the failure so the projection layer can
// surface a typed filter reason.
type openrouterCreditProbeResult struct {
	OK          bool
	Balance     float64
	FailureMode openrouterCreditFailureMode
	HTTPStatus  int    // 0 when the failure happened before a response arrived
	Message     string // short evidence form for routing_decision
}

// probe issues one /api/v1/credits request and classifies the outcome.
//
//   - 200 with a decodable body → OK=true with Balance populated.
//   - 401 / 403 → FailureMode=credential_invalid with HTTPStatus set so the
//     evidence body can name the rejection code (key present but rejected).
//   - any other non-2xx (5xx, 4xx other than 401/403) → provider_unreachable
//     with HTTPStatus populated. Soft fail-open semantics live in the cache
//     layer: the entry only gates routing for the current freshness window.
//   - transport-level errors (DNS, TCP, TLS, timeout, request build failure)
//     → provider_unreachable with HTTPStatus=0 and Message carrying a short
//     form of the error class so operators can triage without log spelunking.
//   - decode failure on a 200 body → provider_unreachable (the upstream
//     returned a malformed payload; that's a remote-side issue, not a key
//     issue).
func (s *openrouterCreditStore) probe(ctx context.Context, baseURL, apiKey string) openrouterCreditProbeResult {
	endpoint := openrouterCreditsEndpoint(baseURL)
	probeCtx, cancel := context.WithTimeout(ctx, openrouterCreditProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return openrouterCreditProbeResult{
			FailureMode: openrouterCreditFailureProviderUnreachable,
			Message:     "request build failed: " + err.Error(),
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := http.DefaultClient
	if s.transport != nil {
		client = &http.Client{Transport: s.transport, Timeout: openrouterCreditProbeTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return openrouterCreditProbeResult{
			FailureMode: openrouterCreditFailureProviderUnreachable,
			Message:     "transport error: " + err.Error(),
		}
	}
	defer resp.Body.Close() //nolint:errcheck
	switch {
	case resp.StatusCode == http.StatusOK:
		var parsed openrouterCreditsResponse
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return openrouterCreditProbeResult{
				FailureMode: openrouterCreditFailureProviderUnreachable,
				HTTPStatus:  resp.StatusCode,
				Message:     "decode error: " + err.Error(),
			}
		}
		return openrouterCreditProbeResult{
			OK:      true,
			Balance: parsed.balanceUSD(),
		}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return openrouterCreditProbeResult{
			FailureMode: openrouterCreditFailureCredentialInvalid,
			HTTPStatus:  resp.StatusCode,
			Message:     fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return openrouterCreditProbeResult{
			FailureMode: openrouterCreditFailureProviderUnreachable,
			HTTPStatus:  resp.StatusCode,
			Message:     fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}
}

// openrouterCreditsResponse decodes /api/v1/credits. OpenRouter returns
// total_credits (lifetime top-ups, USD) and total_usage (lifetime spend, USD)
// nested under a "data" object; the spendable balance is the difference.
type openrouterCreditsResponse struct {
	Data struct {
		TotalCredits float64 `json:"total_credits"`
		TotalUsage   float64 `json:"total_usage"`
	} `json:"data"`
}

func (r openrouterCreditsResponse) balanceUSD() float64 {
	return r.Data.TotalCredits - r.Data.TotalUsage
}

// openrouterCreditsEndpoint resolves the /api/v1/credits URL from a provider's
// configured base URL. An empty base URL falls back to the public OpenRouter
// endpoint.
func openrouterCreditsEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	return base + "/credits"
}

// openrouterCreditThresholdFor returns the configured credit balance threshold
// for the provider, falling back to the package default when unset.
func openrouterCreditThresholdFor(pcfg ServiceProviderEntry) float64 {
	if pcfg.CreditBalanceThresholdUSD > 0 {
		return pcfg.CreditBalanceThresholdUSD
	}
	return DefaultOpenrouterCreditBalanceThresholdUSD
}

// openrouterCreditTTLFor returns the configured credit-probe TTL for the
// provider, falling back to the package default when unset.
func openrouterCreditTTLFor(pcfg ServiceProviderEntry) time.Duration {
	if pcfg.CreditProbeTTL > 0 {
		return pcfg.CreditProbeTTL
	}
	return DefaultOpenrouterCreditProbeTTL
}

// openrouterProbeProjection is the routing engine's view of the openrouter
// credit-probe cache: a credit-exhausted map plus the two failure-mode maps
// surfaced by the failure-classification gate. All three maps share the
// same probe pass so the freshness cache is consulted exactly once per
// provider per routing call.
type openrouterProbeProjection struct {
	CreditExhausted     map[string]routing.ProviderCreditExhaustedEvidence
	CredentialInvalid   map[string]routing.ProviderCredentialInvalidEvidence
	ProviderUnreachable map[string]routing.ProviderProbeUnreachableEvidence
}

// openrouterProbeMaps refreshes the per-provider credit cache (one HTTP call
// at most, per TTL window, per provider) and returns the routing engine's
// view of credit-exhausted, credential-invalid, and provider-unreachable
// evidence in a single pass.
//
// Cache misses and TTL-expired entries trigger a synchronous probe; entries
// still inside the TTL are reused. Providers without an api key are skipped
// (the credential gate already surfaces that case).
//
// Failure-mode semantics:
//   - HasBalance=true and balance < threshold → CreditExhausted entry.
//   - FailureMode=credential_invalid → CredentialInvalid entry carrying the
//     originating HTTP status.
//   - FailureMode=provider_unreachable → ProviderUnreachable entry. The entry
//     persists only until the next probe pass: when the TTL elapses (or a
//     fresh attempt succeeds) the cache record is overwritten with the new
//     outcome, so a transient blip clears automatically (fail-open).
func (s *service) openrouterProbeMaps(ctx context.Context, now time.Time) openrouterProbeProjection {
	var out openrouterProbeProjection
	if s == nil || s.openrouterCredit == nil || s.opts.ServiceConfig == nil {
		return out
	}
	cfg := s.opts.ServiceConfig
	names := cfg.ProviderNames()
	if len(names) == 0 {
		return out
	}
	for _, name := range names {
		pcfg, ok := cfg.Provider(name)
		if !ok {
			continue
		}
		if normalizeServiceProviderType(pcfg.Type) != "openrouter" {
			continue
		}
		apiKey := strings.TrimSpace(pcfg.APIKey)
		if !serviceimpl.OpenRouterAPIKeyWellFormed(apiKey) {
			// Credential gate is responsible for these cases; do not probe.
			continue
		}
		ttl := openrouterCreditTTLFor(pcfg)
		s.openrouterCredit.EnsureFresh(ctx, name, pcfg.BaseURL, apiKey, now, ttl)
		rec, ok := s.openrouterCredit.Lookup(name)
		if !ok {
			continue
		}
		if rec.HasBalance {
			threshold := openrouterCreditThresholdFor(pcfg)
			if rec.BalanceUSD < threshold {
				if out.CreditExhausted == nil {
					out.CreditExhausted = map[string]routing.ProviderCreditExhaustedEvidence{}
				}
				out.CreditExhausted[name] = routing.ProviderCreditExhaustedEvidence{
					BalanceUSD:   rec.BalanceUSD,
					ThresholdUSD: threshold,
					ObservedAt:   rec.ObservedAt,
				}
			}
			continue
		}
		switch rec.FailureMode {
		case openrouterCreditFailureCredentialInvalid:
			if out.CredentialInvalid == nil {
				out.CredentialInvalid = map[string]routing.ProviderCredentialInvalidEvidence{}
			}
			out.CredentialInvalid[name] = routing.ProviderCredentialInvalidEvidence{
				HTTPStatus: rec.FailureHTTPStatus,
				ObservedAt: rec.ObservedAt,
			}
		case openrouterCreditFailureProviderUnreachable:
			if out.ProviderUnreachable == nil {
				out.ProviderUnreachable = map[string]routing.ProviderProbeUnreachableEvidence{}
			}
			errClass := ""
			if rec.FailureHTTPStatus == 0 {
				errClass = "transport_error"
			}
			out.ProviderUnreachable[name] = routing.ProviderProbeUnreachableEvidence{
				StatusCode: rec.FailureHTTPStatus,
				ErrorClass: errClass,
				Message:    rec.FailureMessage,
				ObservedAt: rec.ObservedAt,
			}
		}
	}
	return out
}

// openrouterCreditExhaustedMap is a thin compatibility wrapper that returns
// only the credit-exhausted projection from openrouterProbeMaps. Retained
// for callers that haven't been moved to the projection struct.
func (s *service) openrouterCreditExhaustedMap(ctx context.Context, now time.Time) map[string]routing.ProviderCreditExhaustedEvidence {
	return s.openrouterProbeMaps(ctx, now).CreditExhausted
}

// annotateOpenrouterCreditFreshness overlays QuotaFreshnessAt/QuotaFreshnessSource
// on every openrouter candidate so operators can see how stale the cached
// credit reading is. Runs against the post-engine decision so both eligible
// and credit-gated rows surface the same freshness signal.
func (s *service) annotateOpenrouterCreditFreshness(decision *RouteDecision) {
	if s == nil || decision == nil || s.openrouterCredit == nil || s.opts.ServiceConfig == nil {
		return
	}
	for i := range decision.Candidates {
		provider := candidateBaseProviderName(decision.Candidates[i].Provider)
		if provider == "" {
			continue
		}
		pcfg, ok := s.opts.ServiceConfig.Provider(provider)
		if !ok || normalizeServiceProviderType(pcfg.Type) != "openrouter" {
			continue
		}
		rec, ok := s.openrouterCredit.Lookup(provider)
		if !ok || rec.ObservedAt.IsZero() {
			continue
		}
		decision.Candidates[i].QuotaFreshnessAt = rec.ObservedAt.UTC()
		decision.Candidates[i].QuotaFreshnessSource = openrouterCreditFreshnessSource
	}
}

// candidateBaseProviderName strips any "@endpoint" suffix from a candidate
// provider identity to match ServiceConfig.Provider keys.
func candidateBaseProviderName(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ""
	}
	if i := strings.IndexByte(identity, '@'); i > 0 {
		return identity[:i]
	}
	return identity
}
