package quota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const openRouterCreditTestKey = "sk-or-v1-credit-probe-test-key-aaaaaaaa"

func TestOpenRouterCreditZeroBalanceUsesDefaultThreshold(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := newOpenRouterCreditStore(staticCreditTransport(http.StatusOK, creditFixtureResponse(0)))

	projection := ProjectOpenRouterCredits(context.Background(), store, now, []OpenRouterCreditProvider{{
		Name:   "openrouter",
		APIKey: openRouterCreditTestKey,
	}})

	evidence, ok := projection.CreditExhausted["openrouter"]
	if !ok {
		t.Fatalf("credit exhausted evidence missing: %#v", projection)
	}
	if evidence.BalanceUSD != 0 || evidence.ThresholdUSD != DefaultOpenRouterCreditBalanceThresholdUSD || !evidence.ObservedAt.Equal(now) {
		t.Fatalf("credit exhausted evidence = %#v", evidence)
	}
}

func TestOpenRouterCreditAdequateBalanceAndEndpointFreshness(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 123, time.FixedZone("fixture", -4*60*60))
	store := newOpenRouterCreditStore(staticCreditTransport(http.StatusOK, creditFixtureResponse(25)))

	projection := ProjectOpenRouterCredits(context.Background(), store, now, []OpenRouterCreditProvider{{
		Name:   "openrouter",
		APIKey: openRouterCreditTestKey,
	}})
	if len(projection.CreditExhausted) != 0 || len(projection.CredentialInvalid) != 0 || len(projection.ProviderUnreachable) != 0 {
		t.Fatalf("adequate balance projection = %#v, want empty", projection)
	}

	freshness, ok := OpenRouterCreditFreshness(store, "  openrouter@west  ")
	if !ok {
		t.Fatal("endpoint-qualified candidate did not resolve cached freshness")
	}
	if !freshness.ObservedAt.Equal(now) || freshness.ObservedAt.Location() != time.UTC {
		t.Fatalf("freshness observed_at = %v, want UTC %v", freshness.ObservedAt, now.UTC())
	}
	if freshness.Source != openRouterCreditFreshnessSource {
		t.Fatalf("freshness source = %q, want %q", freshness.Source, openRouterCreditFreshnessSource)
	}
}

func TestOpenRouterCreditConfiguredThreshold(t *testing.T) {
	store := newOpenRouterCreditStore(staticCreditTransport(http.StatusOK, creditFixtureResponse(0.40)))
	projection := ProjectOpenRouterCredits(context.Background(), store, time.Now(), []OpenRouterCreditProvider{{
		Name:                      "openrouter",
		APIKey:                    openRouterCreditTestKey,
		CreditBalanceThresholdUSD: 5,
	}})
	if got := projection.CreditExhausted["openrouter"].ThresholdUSD; got != 5 {
		t.Fatalf("threshold = %v, want configured 5", got)
	}
}

func TestOpenRouterCreditProbeUsesConfiguredAndDefaultBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantURL string
	}{
		{name: "configured trailing slash", baseURL: "https://router.example/api/v1/", wantURL: "https://router.example/api/v1/credits"},
		{name: "public default", wantURL: "https://openrouter.ai/api/v1/credits"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotURL, gotAuthorization string
			store := newOpenRouterCreditStore(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				gotURL = request.URL.String()
				gotAuthorization = request.Header.Get("Authorization")
				return creditResponse(http.StatusOK, creditFixtureResponse(25)), nil
			}))
			ProjectOpenRouterCredits(context.Background(), store, time.Now(), []OpenRouterCreditProvider{{
				Name:    "openrouter",
				BaseURL: test.baseURL,
				APIKey:  openRouterCreditTestKey,
			}})
			if gotURL != test.wantURL {
				t.Fatalf("probe URL = %q, want %q", gotURL, test.wantURL)
			}
			if gotAuthorization != "Bearer "+openRouterCreditTestKey {
				t.Fatalf("Authorization = %q", gotAuthorization)
			}
		})
	}
}

func TestOpenRouterCreditProbeCachesAndRefreshesAtTTL(t *testing.T) {
	const ttl = 30 * time.Second
	var hits atomic.Int32
	store := newOpenRouterCreditStore(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		hits.Add(1)
		return creditResponse(http.StatusOK, creditFixtureResponse(25)), nil
	}))
	provider := OpenRouterCreditProvider{Name: "openrouter", APIKey: openRouterCreditTestKey, CreditProbeTTL: ttl}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	ProjectOpenRouterCredits(context.Background(), store, now, []OpenRouterCreditProvider{provider})
	ProjectOpenRouterCredits(context.Background(), store, now.Add(ttl-time.Nanosecond), []OpenRouterCreditProvider{provider})
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits inside TTL = %d, want 1", got)
	}
	ProjectOpenRouterCredits(context.Background(), store, now.Add(ttl), []OpenRouterCreditProvider{provider})
	if got := hits.Load(); got != 2 {
		t.Fatalf("hits at TTL boundary = %d, want 2", got)
	}
}

func TestOpenRouterCredit401And403AreCredentialInvalid(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			now := time.Now().UTC()
			store := newOpenRouterCreditStore(staticCreditTransport(status, `{"error":"no"}`))
			projection := ProjectOpenRouterCredits(context.Background(), store, now, []OpenRouterCreditProvider{{Name: "openrouter", APIKey: openRouterCreditTestKey}})
			evidence, ok := projection.CredentialInvalid["openrouter"]
			if !ok || evidence.HTTPStatus != status || !evidence.ObservedAt.Equal(now) {
				t.Fatalf("credential invalid evidence = %#v, present=%t", evidence, ok)
			}
			if len(projection.ProviderUnreachable) != 0 {
				t.Fatalf("credential rejection misclassified as unreachable: %#v", projection)
			}
		})
	}
}

func TestOpenRouterCreditTransportFailureIsUnreachable(t *testing.T) {
	store := newOpenRouterCreditStore(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}))
	projection := ProjectOpenRouterCredits(context.Background(), store, time.Now(), []OpenRouterCreditProvider{{Name: "openrouter", APIKey: openRouterCreditTestKey}})
	evidence, ok := projection.ProviderUnreachable["openrouter"]
	if !ok {
		t.Fatalf("provider unreachable evidence missing: %#v", projection)
	}
	if evidence.StatusCode != 0 || evidence.ErrorClass != "transport_error" || !strings.Contains(evidence.Message, "connection refused") {
		t.Fatalf("transport evidence = %#v", evidence)
	}
}

func TestOpenRouterCreditServerAndDecodeFailuresAreUnreachable(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantText   string
	}{
		{name: "server failure", status: http.StatusBadGateway, wantStatus: http.StatusBadGateway, wantText: "502"},
		{name: "decode failure", status: http.StatusOK, body: "not-json", wantStatus: http.StatusOK, wantText: "decode error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newOpenRouterCreditStore(staticCreditTransport(test.status, test.body))
			projection := ProjectOpenRouterCredits(context.Background(), store, time.Now(), []OpenRouterCreditProvider{{Name: "openrouter", APIKey: openRouterCreditTestKey}})
			evidence, ok := projection.ProviderUnreachable["openrouter"]
			if !ok || evidence.StatusCode != test.wantStatus || !strings.Contains(evidence.Message, test.wantText) {
				t.Fatalf("unreachable evidence = %#v, present=%t", evidence, ok)
			}
		})
	}
}

func TestOpenRouterCreditRecoversAfterTTL(t *testing.T) {
	const ttl = 30 * time.Second
	var calls atomic.Int32
	store := newOpenRouterCreditStore(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("temporary network failure")
		}
		return creditResponse(http.StatusOK, creditFixtureResponse(25)), nil
	}))
	provider := OpenRouterCreditProvider{Name: "openrouter", APIKey: openRouterCreditTestKey, CreditProbeTTL: ttl}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	first := ProjectOpenRouterCredits(context.Background(), store, now, []OpenRouterCreditProvider{provider})
	if _, ok := first.ProviderUnreachable["openrouter"]; !ok {
		t.Fatalf("first projection = %#v, want unreachable", first)
	}
	cached := ProjectOpenRouterCredits(context.Background(), store, now.Add(ttl-time.Nanosecond), []OpenRouterCreditProvider{provider})
	if _, ok := cached.ProviderUnreachable["openrouter"]; !ok || calls.Load() != 1 {
		t.Fatalf("cached projection = %#v calls=%d", cached, calls.Load())
	}
	recovered := ProjectOpenRouterCredits(context.Background(), store, now.Add(ttl), []OpenRouterCreditProvider{provider})
	if len(recovered.ProviderUnreachable) != 0 || len(recovered.CreditExhausted) != 0 || calls.Load() != 2 {
		t.Fatalf("recovered projection = %#v calls=%d", recovered, calls.Load())
	}
}

func TestOpenRouterCreditConcurrentSingleFlight(t *testing.T) {
	const callers = 32
	var hits atomic.Int32
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	store := newOpenRouterCreditStore(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if hits.Add(1) == 1 {
			close(probeStarted)
		}
		<-releaseProbe
		return creditResponse(http.StatusOK, creditFixtureResponse(0)), nil
	}))
	provider := OpenRouterCreditProvider{Name: "openrouter", APIKey: openRouterCreditTestKey}
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	results := make(chan OpenRouterCreditProjection, callers)
	for range callers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			results <- ProjectOpenRouterCredits(context.Background(), store, now, []OpenRouterCreditProvider{provider})
		}()
	}
	ready.Wait()
	close(start)
	<-probeStarted
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		flight := store.inFlight["openrouter"]
		waiters := 0
		if flight != nil {
			waiters = flight.waiters
		}
		store.mu.Unlock()
		if waiters == callers-1 {
			break
		}
		if time.Now().After(deadline) {
			close(releaseProbe)
			done.Wait()
			t.Fatalf("single-flight waiters = %d, want %d before releasing probe", waiters, callers-1)
		}
		runtime.Gosched()
	}
	close(releaseProbe)
	done.Wait()
	close(results)

	if got := hits.Load(); got != 1 {
		t.Fatalf("concurrent probe hits = %d, want exactly 1", got)
	}
	for projection := range results {
		if _, ok := projection.CreditExhausted["openrouter"]; !ok {
			t.Fatalf("concurrent projection missing cached evidence: %#v", projection)
		}
	}
}

func TestOpenRouterCreditSkipsIncompleteInput(t *testing.T) {
	var hits atomic.Int32
	store := newOpenRouterCreditStore(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		hits.Add(1)
		return creditResponse(http.StatusOK, creditFixtureResponse(25)), nil
	}))
	projection := ProjectOpenRouterCredits(context.Background(), store, time.Now(), []OpenRouterCreditProvider{
		{APIKey: openRouterCreditTestKey},
		{Name: "openrouter"},
		{Name: "blank", APIKey: "  "},
	})
	if hits.Load() != 0 || len(projection.CreditExhausted) != 0 || len(projection.CredentialInvalid) != 0 || len(projection.ProviderUnreachable) != 0 {
		t.Fatalf("incomplete input produced I/O/evidence: hits=%d projection=%#v", hits.Load(), projection)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func staticCreditTransport(status int, body string) http.RoundTripper {
	return roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return creditResponse(status, body), nil
	})
}

func creditResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func creditFixtureResponse(balance float64) string {
	const usage = 7.50
	return fmt.Sprintf(`{"data":{"total_credits":%.4f,"total_usage":%.4f}}`, balance+usage, usage)
}
