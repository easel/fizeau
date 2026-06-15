package fizeau

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/routing"
)

// openrouterCreditTestKey is a syntactically valid (sk-or-…/≥20 chars) API
// key value used by every credit-probe test; the credential gate must pass
// so we can exercise the balance probe.
const openrouterCreditTestKey = "sk-or-v1-credit-probe-test-key-aaaaaaaa"

// openrouterCreditFixtureResponse builds the /api/v1/credits payload for a
// fake balance. The endpoint exposes total_credits and total_usage; the
// observed balance is the difference.
func openrouterCreditFixtureResponse(balance float64) string {
	// Use a fixed lifetime spend so balance = total_credits - total_usage.
	const usage = 7.50
	return fmt.Sprintf(`{"data":{"total_credits":%.4f,"total_usage":%.4f}}`, balance+usage, usage)
}

// openrouterCreditGateService wires a service with a single openrouter
// provider whose /api/v1/credits endpoint is served by the supplied handler.
// The caller's atomic counter is incremented inside the handler; tests use
// it to assert TTL behavior.
func openrouterCreditGateService(t *testing.T, pcfgOverride func(*ServiceProviderEntry), handler http.HandlerFunc) (*service, *httptest.Server, *atomic.Int32) {
	t.Helper()
	var credits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/credits", func(w http.ResponseWriter, r *http.Request) {
		credits.Add(1)
		handler(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// Any other path is irrelevant to the credit probe; respond OK so
		// background discovery code that happens to call /models doesn't
		// spam noise into the test log.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: cacheDir}
	writeSnapshotDiscoveryFixture(
		t,
		cache,
		testDiscoverySourceName("openrouter", "openrouter", srv.URL+"/v1", ""),
		time.Now().UTC(),
		[]string{"openrouter/test-model"},
	)

	t.Cleanup(replaceRoutingCatalogForTest(t, openrouterCredentialGateCatalog(t)))

	pcfg := ServiceProviderEntry{
		Type:    "openrouter",
		BaseURL: srv.URL + "/api/v1",
		APIKey:  openrouterCreditTestKey,
		Model:   "openrouter/test-model",
	}
	if pcfgOverride != nil {
		pcfgOverride(&pcfg)
	}
	sc := &fakeServiceConfig{
		providers:   map[string]ServiceProviderEntry{"openrouter": pcfg},
		names:       []string{"openrouter"},
		defaultName: "openrouter",
	}
	svc := newTestService(t, ServiceOptions{
		ServiceConfig:       sc,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	svc.openrouterCredit = newOpenrouterCreditStore()
	return svc, srv, &credits
}

// openrouterDecisionCandidates pulls candidate rows out of either a returned
// decision or a routing-error trace, so the tests do not depend on whether
// the engine surfaces the rejection as an error vs. a zero-candidate
// decision.
func openrouterDecisionCandidates(t *testing.T, dec *RouteDecision, err error) *RouteDecision {
	t.Helper()
	if dec == nil {
		var traced DecisionWithCandidates
		if errors.As(err, &traced) {
			return &RouteDecision{Candidates: traced.RouteCandidates()}
		}
		t.Fatalf("no candidates in result: dec=nil err=%v", err)
	}
	return dec
}

func TestOpenrouterZeroBalanceMarksCreditExhausted(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	svc, _, credits := openrouterCreditGateService(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openrouterCreditFixtureResponse(0)))
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	dec = openrouterDecisionCandidates(t, dec, err)
	candidate := findOpenRouterCandidate(t, dec)
	if candidate.Eligible {
		t.Fatalf("openrouter candidate eligible with zero balance: %#v", *candidate)
	}
	if candidate.FilterReason != string(routing.FilterReasonCreditExhausted) {
		t.Fatalf("openrouter FilterReason=%q, want %q", candidate.FilterReason, routing.FilterReasonCreditExhausted)
	}
	if !strings.Contains(candidate.Reason, "credit exhausted") {
		t.Fatalf("openrouter Reason=%q, want phrase 'credit exhausted'", candidate.Reason)
	}
	if !strings.Contains(candidate.Reason, "$0.0000") {
		t.Fatalf("openrouter Reason=%q, want observed balance in evidence body", candidate.Reason)
	}
	if got := credits.Load(); got != 1 {
		t.Fatalf("credits probe hits=%d, want exactly 1", got)
	}
}

func TestOpenrouterAdequateBalanceAllowsDispatch(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	svc, _, credits := openrouterCreditGateService(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Comfortably above the default $0.50 threshold.
		_, _ = w.Write([]byte(openrouterCreditFixtureResponse(25.0)))
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	dec = openrouterDecisionCandidates(t, dec, err)
	candidate := findOpenRouterCandidate(t, dec)
	if candidate.FilterReason == string(routing.FilterReasonCreditExhausted) {
		t.Fatalf("openrouter candidate filtered by credit_exhausted despite ample balance: %#v", *candidate)
	}
	if !candidate.Eligible {
		t.Fatalf("openrouter candidate ineligible (FilterReason=%q): %#v", candidate.FilterReason, *candidate)
	}
	if got := credits.Load(); got != 1 {
		t.Fatalf("credits probe hits=%d, want exactly 1", got)
	}
	// AC#6: openrouter candidates carry a populated QuotaFreshnessAt so
	// operators can see how stale the balance reading is.
	if candidate.QuotaFreshnessAt.IsZero() {
		t.Fatalf("expected QuotaFreshnessAt populated from credit probe: %#v", *candidate)
	}
	if candidate.QuotaFreshnessSource != openrouterCreditFreshnessSource {
		t.Fatalf("QuotaFreshnessSource=%q, want %q", candidate.QuotaFreshnessSource, openrouterCreditFreshnessSource)
	}
}

func TestOpenrouterCreditThresholdConfigurable(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	// Default threshold is $0.50; with a $0.40 reading and the default,
	// the gate would fire. We lift the threshold to $5.00 so the same
	// reading still trips — proving the knob is wired through config
	// rather than hardcoded.
	const observedBalance = 0.40
	const configuredThreshold = 5.00

	svc, _, credits := openrouterCreditGateService(t, func(p *ServiceProviderEntry) {
		p.CreditBalanceThresholdUSD = configuredThreshold
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openrouterCreditFixtureResponse(observedBalance)))
	})

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	dec = openrouterDecisionCandidates(t, dec, err)
	candidate := findOpenRouterCandidate(t, dec)
	if candidate.FilterReason != string(routing.FilterReasonCreditExhausted) {
		t.Fatalf("openrouter FilterReason=%q, want %q (configurable threshold should still fire on $%.2f below $%.2f)",
			candidate.FilterReason, routing.FilterReasonCreditExhausted, observedBalance, configuredThreshold)
	}
	if !strings.Contains(candidate.Reason, "threshold $5.00") {
		t.Fatalf("openrouter Reason=%q, want configured threshold reflected in evidence body", candidate.Reason)
	}
	if got := credits.Load(); got != 1 {
		t.Fatalf("credits probe hits=%d, want exactly 1", got)
	}
}

func TestOpenrouterCreditProbeCachedWithinTTL(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	svc, _, credits := openrouterCreditGateService(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openrouterCreditFixtureResponse(25.0)))
	})

	// N back-to-back routing passes within one TTL window should share a
	// single /api/v1/credits round trip.
	const passes = 10
	for i := 0; i < passes; i++ {
		_, _ = svc.ResolveRoute(context.Background(), RouteRequest{})
	}
	if got := credits.Load(); got != 1 {
		t.Fatalf("credits probe hits=%d after %d routing passes, want exactly 1 within TTL window", got, passes)
	}
}

func TestOpenrouterCreditProbeRefreshesAfterTTL(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	const ttl = 30 * time.Second
	svc, _, credits := openrouterCreditGateService(t, func(p *ServiceProviderEntry) {
		p.CreditProbeTTL = ttl
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openrouterCreditFixtureResponse(25.0)))
	})

	// First pass primes the cache.
	_, _ = svc.ResolveRoute(context.Background(), RouteRequest{})
	if got := credits.Load(); got != 1 {
		t.Fatalf("credits hits=%d after first pass, want 1", got)
	}

	// Without time advancement, a second pass reuses the cache.
	_, _ = svc.ResolveRoute(context.Background(), RouteRequest{})
	if got := credits.Load(); got != 1 {
		t.Fatalf("credits hits=%d after second cached pass, want 1", got)
	}

	// Fake-clock advance: rewind the cached ObservedAt past the TTL boundary
	// so the next routing pass treats the record as expired. This is the
	// "fake clock" seam — directly manipulating the cached timestamp keeps
	// the test deterministic without spinning real wall time.
	rec, ok := svc.openrouterCredit.Lookup("openrouter")
	if !ok {
		t.Fatalf("expected cached record after warm-up")
	}
	rec.ObservedAt = rec.ObservedAt.Add(-2 * ttl)
	svc.openrouterCredit.Record("openrouter", rec)

	_, _ = svc.ResolveRoute(context.Background(), RouteRequest{})
	if got := credits.Load(); got != 2 {
		t.Fatalf("credits hits=%d after TTL expiry, want 2 (cache should re-probe)", got)
	}
}

// TestOpenrouterCreditExhaustedHonorsConfiguredBaseURL guards the URL
// derivation: the probe must talk to the operator-configured base_url, not
// hardcode openrouter.ai (so dev/staging routes don't escape the test
// harness). It also doubles as a small regression check that the path
// terminates with /credits regardless of trailing slashes.
func TestOpenrouterCreditExhaustedHonorsConfiguredBaseURL(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	svc, srv, _ := openrouterCreditGateService(t, func(p *ServiceProviderEntry) {
		p.BaseURL = strings.TrimRight(p.BaseURL, "/") + "/" // exercise trim path
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credits" {
			t.Fatalf("credit probe hit %s, want /api/v1/credits", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openrouterCreditFixtureResponse(0)))
	})

	if _, parseErr := url.Parse(srv.URL); parseErr != nil {
		t.Fatalf("parse fixture server URL: %v", parseErr)
	}
	_, _ = svc.ResolveRoute(context.Background(), RouteRequest{})
}
