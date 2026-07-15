package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultOpenRouterCreditBalanceThresholdUSD is the balance floor below
	// which an OpenRouter provider is excluded from routing.
	DefaultOpenRouterCreditBalanceThresholdUSD = 0.50
	// DefaultOpenRouterCreditProbeTTL bounds the freshness of a cached
	// OpenRouter credit observation.
	DefaultOpenRouterCreditProbeTTL = 10 * time.Minute

	openRouterCreditProbeTimeout    = 5 * time.Second
	openRouterCreditFreshnessSource = "openrouter_credits_probe"
)

type openRouterCreditFailureMode string

const (
	openRouterCreditFailureCredentialInvalid   openRouterCreditFailureMode = "credential_invalid"
	openRouterCreditFailureProviderUnreachable openRouterCreditFailureMode = "provider_unreachable"
)

// OpenRouterCreditProvider is the API-neutral provider configuration needed
// by the OpenRouter credit probe. The root service facade adapts its public
// configuration into this shape after validating credentials.
type OpenRouterCreditProvider struct {
	Name                      string
	BaseURL                   string
	APIKey                    string
	CreditBalanceThresholdUSD float64
	CreditProbeTTL            time.Duration
}

// OpenRouterCreditExhaustedEvidence describes a fresh balance below the
// configured routing threshold.
type OpenRouterCreditExhaustedEvidence struct {
	BalanceUSD   float64
	ThresholdUSD float64
	ObservedAt   time.Time
}

// OpenRouterCredentialInvalidEvidence describes an OpenRouter rejection of a
// syntactically valid credential.
type OpenRouterCredentialInvalidEvidence struct {
	HTTPStatus int
	ObservedAt time.Time
}

// OpenRouterProviderUnreachableEvidence describes a failed OpenRouter credit
// probe. StatusCode is zero for transport failures.
type OpenRouterProviderUnreachableEvidence struct {
	StatusCode int
	ErrorClass string
	Message    string
	ObservedAt time.Time
}

// OpenRouterCreditProjection is the routing-neutral output of one cached
// OpenRouter credit-probe pass.
type OpenRouterCreditProjection struct {
	CreditExhausted     map[string]OpenRouterCreditExhaustedEvidence
	CredentialInvalid   map[string]OpenRouterCredentialInvalidEvidence
	ProviderUnreachable map[string]OpenRouterProviderUnreachableEvidence
}

// OpenRouterFreshness describes the cached credit observation used for
// a candidate row.
type OpenRouterFreshness struct {
	Provider   string
	ObservedAt time.Time
	Source     string
}

type openRouterCreditRecord struct {
	BalanceUSD        float64
	ObservedAt        time.Time
	HasBalance        bool
	FailureMode       openRouterCreditFailureMode
	FailureHTTPStatus int
	FailureMessage    string
}

// OpenRouterCreditStore owns the process-local OpenRouter credit cache and
// per-provider single-flight coordination. Its state is intentionally opaque
// outside internal/quota.
type OpenRouterCreditStore struct {
	mu        sync.Mutex
	records   map[string]openRouterCreditRecord
	inFlight  map[string]*openRouterCreditFlight
	transport http.RoundTripper
}

type openRouterCreditFlight struct {
	done    chan struct{}
	waiters int
}

// NewOpenRouterCreditStore returns an empty, concurrency-safe credit store.
func NewOpenRouterCreditStore() *OpenRouterCreditStore {
	return newOpenRouterCreditStore(nil)
}

func newOpenRouterCreditStore(transport http.RoundTripper) *OpenRouterCreditStore {
	return &OpenRouterCreditStore{
		records:   make(map[string]openRouterCreditRecord),
		inFlight:  make(map[string]*openRouterCreditFlight),
		transport: transport,
	}
}

// ProjectOpenRouterCredits refreshes stale provider observations and returns
// the routing evidence derived from the cache. Providers in this input have
// already passed the root facade's credential-shape validation.
func ProjectOpenRouterCredits(ctx context.Context, store *OpenRouterCreditStore, now time.Time, providers []OpenRouterCreditProvider) OpenRouterCreditProjection {
	var out OpenRouterCreditProjection
	if store == nil {
		return out
	}
	for _, provider := range providers {
		name := strings.TrimSpace(provider.Name)
		apiKey := strings.TrimSpace(provider.APIKey)
		if name == "" || apiKey == "" {
			continue
		}
		store.ensureFresh(ctx, name, provider.BaseURL, apiKey, now, creditProbeTTL(provider.CreditProbeTTL))
		record, ok := store.lookup(name)
		if !ok {
			continue
		}
		if record.HasBalance {
			threshold := creditBalanceThreshold(provider.CreditBalanceThresholdUSD)
			if record.BalanceUSD < threshold {
				if out.CreditExhausted == nil {
					out.CreditExhausted = make(map[string]OpenRouterCreditExhaustedEvidence)
				}
				out.CreditExhausted[name] = OpenRouterCreditExhaustedEvidence{
					BalanceUSD:   record.BalanceUSD,
					ThresholdUSD: threshold,
					ObservedAt:   record.ObservedAt,
				}
			}
			continue
		}
		switch record.FailureMode {
		case openRouterCreditFailureCredentialInvalid:
			if out.CredentialInvalid == nil {
				out.CredentialInvalid = make(map[string]OpenRouterCredentialInvalidEvidence)
			}
			out.CredentialInvalid[name] = OpenRouterCredentialInvalidEvidence{
				HTTPStatus: record.FailureHTTPStatus,
				ObservedAt: record.ObservedAt,
			}
		case openRouterCreditFailureProviderUnreachable:
			if out.ProviderUnreachable == nil {
				out.ProviderUnreachable = make(map[string]OpenRouterProviderUnreachableEvidence)
			}
			errorClass := ""
			if record.FailureHTTPStatus == 0 {
				errorClass = "transport_error"
			}
			out.ProviderUnreachable[name] = OpenRouterProviderUnreachableEvidence{
				StatusCode: record.FailureHTTPStatus,
				ErrorClass: errorClass,
				Message:    record.FailureMessage,
				ObservedAt: record.ObservedAt,
			}
		}
	}
	return out
}

// OpenRouterCreditFreshness returns the cached observation used by a
// candidate identity. Endpoint-qualified identities (provider@endpoint) are
// normalized here so that naming mechanics remain owned by internal/quota.
func OpenRouterCreditFreshness(store *OpenRouterCreditStore, candidateIdentity string) (OpenRouterFreshness, bool) {
	if store == nil {
		return OpenRouterFreshness{}, false
	}
	provider := openRouterBaseProviderName(candidateIdentity)
	record, ok := store.lookup(provider)
	if !ok || record.ObservedAt.IsZero() {
		return OpenRouterFreshness{}, false
	}
	return OpenRouterFreshness{
		Provider:   provider,
		ObservedAt: record.ObservedAt.UTC(),
		Source:     openRouterCreditFreshnessSource,
	}, true
}

func (s *OpenRouterCreditStore) lookup(provider string) (openRouterCreditRecord, bool) {
	if s == nil || provider == "" {
		return openRouterCreditRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[provider]
	return record, ok
}

func (s *OpenRouterCreditStore) record(provider string, record openRouterCreditRecord) {
	if s == nil || provider == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string]openRouterCreditRecord)
	}
	s.records[provider] = record
}

func (s *OpenRouterCreditStore) ensureFresh(ctx context.Context, provider, baseURL, apiKey string, now time.Time, ttl time.Duration) {
	if s == nil || provider == "" || strings.TrimSpace(apiKey) == "" {
		return
	}
	s.mu.Lock()
	record, ok := s.records[provider]
	if ok && ttl > 0 && now.Sub(record.ObservedAt) < ttl {
		s.mu.Unlock()
		return
	}
	if flight, busy := s.inFlight[provider]; busy {
		flight.waiters++
		s.mu.Unlock()
		select {
		case <-flight.done:
		case <-ctx.Done():
		}
		s.mu.Lock()
		flight.waiters--
		s.mu.Unlock()
		return
	}
	if s.inFlight == nil {
		s.inFlight = make(map[string]*openRouterCreditFlight)
	}
	flight := &openRouterCreditFlight{done: make(chan struct{})}
	s.inFlight[provider] = flight
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.inFlight, provider)
		close(flight.done)
		s.mu.Unlock()
	}()

	result := s.probe(ctx, baseURL, apiKey)
	next := openRouterCreditRecord{ObservedAt: now}
	if result.OK {
		next.BalanceUSD = result.Balance
		next.HasBalance = true
	} else {
		next.FailureMode = result.FailureMode
		next.FailureHTTPStatus = result.HTTPStatus
		next.FailureMessage = result.Message
	}
	s.record(provider, next)
}

type openRouterCreditProbeResult struct {
	OK          bool
	Balance     float64
	FailureMode openRouterCreditFailureMode
	HTTPStatus  int
	Message     string
}

func (s *OpenRouterCreditStore) probe(ctx context.Context, baseURL, apiKey string) openRouterCreditProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, openRouterCreditProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, openRouterCreditsEndpoint(baseURL), nil)
	if err != nil {
		return openRouterCreditProbeResult{
			FailureMode: openRouterCreditFailureProviderUnreachable,
			Message:     "request build failed: " + err.Error(),
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := http.DefaultClient
	if s.transport != nil {
		client = &http.Client{Transport: s.transport, Timeout: openRouterCreditProbeTimeout}
	}
	response, err := client.Do(req)
	if err != nil {
		return openRouterCreditProbeResult{
			FailureMode: openRouterCreditFailureProviderUnreachable,
			Message:     "transport error: " + err.Error(),
		}
	}
	defer response.Body.Close() //nolint:errcheck

	switch {
	case response.StatusCode == http.StatusOK:
		var parsed openRouterCreditsResponse
		if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
			return openRouterCreditProbeResult{
				FailureMode: openRouterCreditFailureProviderUnreachable,
				HTTPStatus:  response.StatusCode,
				Message:     "decode error: " + err.Error(),
			}
		}
		return openRouterCreditProbeResult{OK: true, Balance: parsed.balanceUSD()}
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return openRouterCreditProbeResult{
			FailureMode: openRouterCreditFailureCredentialInvalid,
			HTTPStatus:  response.StatusCode,
			Message:     fmt.Sprintf("HTTP %d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		}
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return openRouterCreditProbeResult{
			FailureMode: openRouterCreditFailureProviderUnreachable,
			HTTPStatus:  response.StatusCode,
			Message:     fmt.Sprintf("HTTP %d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		}
	}
}

type openRouterCreditsResponse struct {
	Data struct {
		TotalCredits float64 `json:"total_credits"`
		TotalUsage   float64 `json:"total_usage"`
	} `json:"data"`
}

func (r openRouterCreditsResponse) balanceUSD() float64 {
	return r.Data.TotalCredits - r.Data.TotalUsage
}

func openRouterCreditsEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	return base + "/credits"
}

func creditBalanceThreshold(configured float64) float64 {
	if configured > 0 {
		return configured
	}
	return DefaultOpenRouterCreditBalanceThresholdUSD
}

func creditProbeTTL(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return DefaultOpenRouterCreditProbeTTL
}

func openRouterBaseProviderName(identity string) string {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ""
	}
	if index := strings.IndexByte(identity, '@'); index > 0 {
		return identity[:index]
	}
	return identity
}
