package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/runtimesignals"
)

type snapshotAutoroutingFixture struct {
	Providers []snapshotAutoroutingProviderFixture `json:"providers"`
}

type snapshotAutoroutingProviderFixture struct {
	Name                string                              `json:"name"`
	Type                string                              `json:"type"`
	BaseURL             string                              `json:"base_url"`
	ServerInstance      string                              `json:"server_instance"`
	EndpointName        string                              `json:"endpoint_name"`
	Model               string                              `json:"model"`
	IncludeByDefault    bool                                `json:"include_by_default"`
	IncludeByDefaultSet bool                                `json:"include_by_default_set"`
	Discovery           snapshotAutoroutingDiscoveryFixture `json:"discovery"`
	Runtime             *runtimesignals.Signal              `json:"runtime,omitempty"`
}

type snapshotAutoroutingDiscoveryFixture struct {
	CapturedAt time.Time `json:"captured_at"`
	Models     []string  `json:"models"`
	Stale      bool      `json:"stale,omitempty"`
}

// @covers US-004-AC2
func TestSnapshotAutoroutingFromFixtures(t *testing.T) {
	t.Setenv("PATH", "")
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)

	fixture := loadSnapshotAutoroutingFixture(t)
	manifest := loadSnapshotAutoroutingManifest(t)
	catalog := loadRoutingFixtureCatalog(t, manifest)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected network call", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	sc := buildSnapshotAutoroutingServiceConfig(t, fixture, srv.URL+"/v1")
	seedSnapshotAutoroutingCache(t, cacheRoot, fixture, srv.URL+"/v1", false)

	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	decisions := map[string]*RouteDecision{}

	t.Run("cheap", func(t *testing.T) {
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "cheap"})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec.Harness != "fiz" || dec.Provider != "local" || dec.Model != "gpt-5.4-nano" {
			t.Fatalf("winner=%s/%s/%s, want fiz/local/gpt-5.4-nano", dec.Harness, dec.Provider, dec.Model)
		}
		decisions["cheap"] = dec
	})

	t.Run("default", func(t *testing.T) {
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "default"})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec.Harness != "fiz" || dec.Provider != "local-stale" || dec.Model != "gpt-5.4-mini" {
			t.Fatalf("winner=%s/%s/%s, want fiz/local-stale/gpt-5.4-mini", dec.Harness, dec.Provider, dec.Model)
		}
		decisions["default"] = dec
	})

	t.Run("smart", func(t *testing.T) {
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Policy: "smart"})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec.Harness != "fiz" || dec.Provider != "remote-optin" || dec.Model != "gpt-5.5" {
			t.Fatalf("winner=%s/%s/%s, want fiz/remote-optin/gpt-5.5", dec.Harness, dec.Provider, dec.Model)
		}
		var sawOptOut, sawDown bool
		for _, c := range dec.Candidates {
			switch {
			case c.Provider == "remote-open" && c.Model == "gpt-5.5":
				sawOptOut = true
				if c.Eligible {
					t.Fatalf("remote-open candidate should be rejected without metered opt-in: %#v", c)
				}
				if c.FilterReason != FilterReasonMeteredOptInRequired {
					t.Fatalf("remote-open FilterReason=%q, want %q", c.FilterReason, FilterReasonMeteredOptInRequired)
				}
			case c.Provider == "remote-down" && c.Model == "gpt-5.5":
				sawDown = true
				if c.SourceStatus != "unknown" {
					t.Fatalf("remote-down SourceStatus=%q, want unknown", c.SourceStatus)
				}
				if c.FilterReason != FilterReasonMeteredOptInRequired {
					t.Fatalf("remote-down FilterReason=%q, want %q", c.FilterReason, FilterReasonMeteredOptInRequired)
				}
			}
		}
		if !sawOptOut || !sawDown {
			t.Fatalf("missing expected candidates: optOut=%t down=%t", sawOptOut, sawDown)
		}
		decisions["smart"] = dec
	})

	t.Run("exact pin", func(t *testing.T) {
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "gpt-5.4-mini"})
		if err != nil {
			t.Fatalf("ResolveRoute: %v", err)
		}
		if dec.Harness != "fiz" || dec.Provider != "local-stale" || dec.Model != "gpt-5.4-mini" {
			t.Fatalf("winner=%s/%s/%s, want fiz/local-stale/gpt-5.4-mini", dec.Harness, dec.Provider, dec.Model)
		}
		var sawPinned bool
		for _, candidate := range dec.Candidates {
			if candidate.Provider != "local-stale" || candidate.Model != "gpt-5.4-mini" {
				continue
			}
			sawPinned = true
			if candidate.SourceStatus != "unknown" {
				t.Fatalf("exact-pinned candidate source status=%q, want unknown", candidate.SourceStatus)
			}
			if !candidate.Eligible {
				t.Fatalf("exact-pinned candidate should remain eligible: %#v", candidate)
			}
		}
		if !sawPinned {
			t.Fatalf("exact pin candidate not found in %#v", dec.Candidates)
		}
		decisions["exact pin"] = dec
	})

	t.Run("exact pin failure", func(t *testing.T) {
		_, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "does-not-exist"})
		if err == nil {
			t.Fatal("ResolveRoute succeeded for an undispatchable exact pin, want a clear failure")
		}
	})

	if got := calls.Load(); got != 0 {
		t.Fatalf("unexpected provider call count = %d; fresh snapshot replay must not probe providers", got)
	}

	_ = decisions
}

func loadSnapshotAutoroutingFixture(t *testing.T) snapshotAutoroutingFixture {
	t.Helper()
	path := filepath.Join("testdata", "snapshot-autorouting", "fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var fixture snapshotAutoroutingFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return fixture
}

func loadSnapshotAutoroutingManifest(t *testing.T) string {
	t.Helper()
	path := filepath.Join("testdata", "snapshot-autorouting", "models.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %s: %v", path, err)
	}
	return string(data)
}

func buildSnapshotAutoroutingServiceConfig(t *testing.T, fixture snapshotAutoroutingFixture, baseURL string) *fakeServiceConfig {
	t.Helper()
	providers := make(map[string]ServiceProviderEntry, len(fixture.Providers))
	names := make([]string, 0, len(fixture.Providers))
	for _, p := range fixture.Providers {
		provider := ServiceProviderEntry{
			Type:                p.Type,
			BaseURL:             baseURL,
			ServerInstance:      p.ServerInstance,
			Model:               p.Model,
			Billing:             providerBillingForSnapshotFixture(p),
			IncludeByDefault:    p.IncludeByDefault,
			IncludeByDefaultSet: p.IncludeByDefaultSet,
		}
		if strings.EqualFold(p.Type, "openrouter") {
			provider.APIKey = "sk-or-v1-snapshot-autorouting-fixture-key"
		}
		provider.Endpoints = []ServiceProviderEndpoint{{
			Name:           p.EndpointName,
			BaseURL:        baseURL,
			ServerInstance: p.ServerInstance,
		}}
		providers[p.Name] = provider
		names = append(names, p.Name)
	}
	return &fakeServiceConfig{
		providers:   providers,
		names:       names,
		defaultName: "local",
	}
}

func seedSnapshotAutoroutingCache(t *testing.T, cacheRoot string, fixture snapshotAutoroutingFixture, baseURL string, ageStale bool) {
	t.Helper()
	cache := &discoverycache.Cache{Root: cacheRoot}
	for _, p := range fixture.Providers {
		source := testDiscoverySourceName(p.Name, p.EndpointName, baseURL, p.ServerInstance)
		writeSnapshotDiscoveryFixture(t, cache, source, p.Discovery.CapturedAt, append([]string(nil), p.Discovery.Models...))
		if ageStale && p.Discovery.Stale {
			path := filepath.Join(cacheRoot, "discovery", source+".json")
			past := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(path, past, past); err != nil {
				t.Fatalf("age discovery fixture %s: %v", path, err)
			}
		}
		if p.Runtime != nil {
			if err := runtimesignals.Write(cache, *p.Runtime); err != nil {
				t.Fatalf("write runtime fixture for %s: %v", p.Name, err)
			}
		}
	}
}

func providerBillingForSnapshotFixture(p snapshotAutoroutingProviderFixture) BillingModel {
	if strings.EqualFold(p.Type, "openrouter") {
		return BillingModelPerToken
	}
	return BillingModelFixed
}
