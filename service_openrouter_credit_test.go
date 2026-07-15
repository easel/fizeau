package fizeau

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	quotaimpl "github.com/easel/fizeau/internal/quota"
	"github.com/easel/fizeau/internal/routing"
)

const rootOpenRouterCreditTestKey = "sk-or-v1-credit-probe-test-key-aaaaaaaa"

// TestOpenrouterCreditCompositionProjectsQuotaEvidence is the root-level
// composition contract retained after credit mechanics moved to
// internal/quota. Its subtests prove every neutral evidence map reaches
// ResolveRoute and that BaseURL, threshold, and TTL public config fields cross
// the adapter boundary.
func TestOpenrouterCreditCompositionProjectsQuotaEvidence(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	tests := []struct {
		name             string
		status           int
		body             string
		wantFilterReason routing.FilterReason
		wantReason       []string
	}{
		{
			name:             "credit exhausted with configured threshold",
			status:           http.StatusOK,
			body:             `{"data":{"total_credits":11.5,"total_usage":7.5}}`,
			wantFilterReason: routing.FilterReasonCreditExhausted,
			wantReason:       []string{"$4.0000", "threshold $5.00"},
		},
		{
			name:             "credential invalid",
			status:           http.StatusUnauthorized,
			body:             `{"error":"invalid key"}`,
			wantFilterReason: routing.FilterReasonCredentialInvalid,
			wantReason:       []string{"401"},
		},
		{
			name:             "provider unreachable",
			status:           http.StatusBadGateway,
			body:             `{"error":"upstream unavailable"}`,
			wantFilterReason: routing.FilterReasonProviderUnreachable,
			wantReason:       []string{"502"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var hits atomic.Int32
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/credits", func(w http.ResponseWriter, request *http.Request) {
				hits.Add(1)
				if got := request.Header.Get("Authorization"); got != "Bearer "+rootOpenRouterCreditTestKey {
					t.Errorf("Authorization = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			})
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"data":[]}`)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)

			cacheDir := tempDiscoveryCacheDir(t)
			t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
			t.Setenv("PATH", "")
			cache := &discoverycache.Cache{Root: cacheDir}
			writeSnapshotDiscoveryFixture(
				t,
				cache,
				testDiscoverySourceName("openrouter", "openrouter", server.URL+"/v1", ""),
				time.Now().UTC(),
				[]string{"openrouter/test-model"},
			)
			t.Cleanup(replaceRoutingCatalogForTest(t, openrouterCredentialGateCatalog(t)))

			config := &fakeServiceConfig{
				providers: map[string]ServiceProviderEntry{
					"openrouter": {
						Type:                      "openrouter",
						BaseURL:                   server.URL + "/api/v1/",
						APIKey:                    rootOpenRouterCreditTestKey,
						Model:                     "openrouter/test-model",
						CreditBalanceThresholdUSD: 5,
						CreditProbeTTL:            time.Nanosecond,
					},
				},
				names:       []string{"openrouter"},
				defaultName: "openrouter",
			}
			service := newTestService(t, ServiceOptions{
				ServiceConfig:       config,
				QuotaRefreshContext: canceledRefreshContext(),
			})
			service.openrouterCredit = quotaimpl.NewOpenRouterCreditStore()

			candidate := resolveOpenRouterCreditCandidate(t, service)
			if candidate.Eligible || candidate.FilterReason != string(test.wantFilterReason) {
				t.Fatalf("OpenRouter candidate = %#v, want %s rejection", *candidate, test.wantFilterReason)
			}
			for _, fragment := range test.wantReason {
				if !strings.Contains(candidate.Reason, fragment) {
					t.Errorf("OpenRouter reason %q missing %q", candidate.Reason, fragment)
				}
			}
			if candidate.QuotaFreshnessAt.IsZero() || candidate.QuotaFreshnessSource != "openrouter_credits_probe" {
				t.Errorf("OpenRouter freshness not projected: %#v", *candidate)
			}

			// A second route resolution exercises the custom TTL projection. A
			// nanosecond is deterministically expired by this point, so correct
			// projection causes a second probe while the ten-minute default would
			// incorrectly reuse the first observation.
			_ = resolveOpenRouterCreditCandidate(t, service)
			if got := hits.Load(); got != 2 {
				t.Fatalf("credit probe hits = %d, want 2 with nanosecond configured TTL", got)
			}
		})
	}
}

func TestAnnotateOpenrouterCreditFreshnessRequiresCurrentOpenrouterConfig(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(*fakeServiceConfig)
		wantAnnotated bool
	}{
		{name: "retained", mutate: func(*fakeServiceConfig) {}, wantAnnotated: true},
		{name: "removed", mutate: func(config *fakeServiceConfig) { delete(config.providers, "openrouter") }},
		{name: "retyped", mutate: func(config *fakeServiceConfig) {
			provider := config.providers["openrouter"]
			provider.Type = "openai"
			config.providers["openrouter"] = provider
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/v1/credits" {
					t.Errorf("probe path = %q", request.URL.Path)
				}
				_, _ = fmt.Fprint(w, `{"data":{"total_credits":25,"total_usage":0}}`)
			}))
			t.Cleanup(server.Close)
			config := &fakeServiceConfig{
				providers: map[string]ServiceProviderEntry{
					"openrouter": {Type: "openrouter", BaseURL: server.URL + "/api/v1", APIKey: rootOpenRouterCreditTestKey},
				},
				names: []string{"openrouter"},
			}
			service := newTestService(t, ServiceOptions{ServiceConfig: config})
			service.openrouterCredit = quotaimpl.NewOpenRouterCreditStore()
			service.openrouterProbeMaps(context.Background(), time.Now().UTC())
			test.mutate(config)

			decision := &RouteDecision{Candidates: []RouteCandidate{{Provider: "openrouter@west"}}}
			service.annotateOpenrouterCreditFreshness(decision)
			annotated := !decision.Candidates[0].QuotaFreshnessAt.IsZero() && decision.Candidates[0].QuotaFreshnessSource != ""
			if annotated != test.wantAnnotated {
				t.Fatalf("freshness annotated = %t, want %t: %#v", annotated, test.wantAnnotated, decision.Candidates[0])
			}
		})
	}
}

func resolveOpenRouterCreditCandidate(t *testing.T, service *service) *RouteCandidate {
	t.Helper()
	decision, err := service.ResolveRoute(context.Background(), RouteRequest{})
	if decision == nil {
		var traced DecisionWithCandidates
		if !errors.As(err, &traced) {
			t.Fatalf("ResolveRoute decision=nil err=%v", err)
		}
		decision = &RouteDecision{Candidates: traced.RouteCandidates()}
	}
	return findOpenRouterCandidate(t, decision)
}
