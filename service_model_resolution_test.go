package fizeau

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
)

func TestResolveRouteModelConstraintNormalization(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	cases := []struct {
		name      string
		request   string
		models    []string
		wantModel string
	}{
		{
			name:      "separator normalization",
			request:   "qwen36",
			models:    []string{"Qwen-3.6-27b-MLX-8bit"},
			wantModel: "Qwen-3.6-27b-MLX-8bit",
		},
		{
			name:      "slash prefix tolerated",
			request:   "qwen/qwen3.6",
			models:    []string{"Qwen3.6-35B-A3B-4bit"},
			wantModel: "Qwen3.6-35B-A3B-4bit",
		},
		{
			name:      "case insensitive",
			request:   "QWEN3.6",
			models:    []string{"Qwen3.6-35B-A3B-4bit"},
			wantModel: "Qwen3.6-35B-A3B-4bit",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := &discoverycache.Cache{Root: cacheDir}
			writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", "http://example.invalid/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), tc.models)

			catalogCleanup := replaceRoutingCatalogForTest(t, loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-06T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  fallback-model:
    status: active
    surfaces:
      agent.openai: fallback-model
`))
			defer catalogCleanup()

			sc := &fakeServiceConfig{
				providers: map[string]ServiceProviderEntry{
					"live": {Type: "openai", BaseURL: "http://example.invalid/v1", Model: "fallback-model"},
				},
				names:       []string{"live"},
				defaultName: "live",
			}
			svc := newTestService(t, ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})

			dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: tc.request})
			if err != nil {
				t.Fatalf("ResolveRoute: %v", err)
			}
			if dec == nil {
				t.Fatal("ResolveRoute returned nil decision")
			}
			if dec.Model != tc.wantModel {
				t.Fatalf("ResolveRoute model=%q, want %q", dec.Model, tc.wantModel)
			}
		})
	}
}

func TestResolveRouteModelConstraintAmbiguousAndNoMatch(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	catalogCleanup := replaceRoutingCatalogForTest(t, loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-06T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  fallback-model:
    status: active
    surfaces:
      agent.openai: fallback-model
`))
	defer catalogCleanup()

	t.Run("ambiguous", func(t *testing.T) {
		cache := &discoverycache.Cache{Root: cacheDir}
		writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", "http://example.invalid/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{
			"Qwen3.6-35B-A3B-4bit",
			"Qwen3.6-35B-A3B-nvfp4",
		})

		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"live": {Type: "openai", BaseURL: "http://example.invalid/v1", Model: "fallback-model"},
			},
			names:       []string{"live"},
			defaultName: "live",
		}
		svc := newTestService(t, ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})

		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen3.6"})
		if err == nil {
			t.Fatal("expected ambiguous model constraint error")
		}
		var typed *ErrModelConstraintAmbiguous
		if !errors.As(err, &typed) {
			t.Fatalf("errors.As should extract ErrModelConstraintAmbiguous: %T %v", err, err)
		}
		if typed.Model != "qwen3.6" {
			t.Fatalf("Model=%q, want qwen3.6", typed.Model)
		}
		if len(typed.Candidates) < 2 {
			t.Fatalf("Candidates=%v, want multiple candidates", typed.Candidates)
		}
		if dec == nil || len(dec.Candidates) == 0 {
			t.Fatalf("expected evidence candidates on decision, got %#v", dec)
		}
	})

	t.Run("no match", func(t *testing.T) {
		cache := &discoverycache.Cache{Root: cacheDir}
		writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", "http://example.invalid/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"OtherModel"})

		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"live": {Type: "openai", BaseURL: "http://example.invalid/v1", Model: "fallback-model"},
			},
			names:       []string{"live"},
			defaultName: "live",
		}
		svc := newTestService(t, ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})

		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen36"})
		if err == nil {
			t.Fatal("expected no-match model constraint error")
		}
		var typed *ErrModelConstraintNoMatch
		if !errors.As(err, &typed) {
			t.Fatalf("errors.As should extract ErrModelConstraintNoMatch: %T %v", err, err)
		}
		if typed.Model != "qwen36" {
			t.Fatalf("Model=%q, want qwen36", typed.Model)
		}
		if len(typed.Candidates) == 0 {
			t.Fatal("expected no-match error to include nearby candidates")
		}
		if dec == nil || len(dec.Candidates) == 0 {
			t.Fatalf("expected evidence candidates on decision, got %#v", dec)
		}
		if strings.Contains(dec.Model, "OtherModel") {
			t.Fatalf("decision should not silently fall back: %#v", dec)
		}
	})
}

func TestExecuteModelConstraintNormalization(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	catalogCleanup := replaceRoutingCatalogForTest(t, loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-05-06T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  fallback-model:
    status: active
    surfaces:
      agent.openai: fallback-model
`))
	defer catalogCleanup()

	cases := []struct {
		name    string
		request string
	}{
		{name: "separator normalization", request: "qwen36"},
		{name: "slash prefix tolerated", request: "qwen/qwen3.6"},
		{name: "case insensitive", request: "QWEN3.6"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := &discoverycache.Cache{Root: cacheDir}
			srv := openAIModelChatServer(t, []string{"Qwen-3.6-27b-MLX-8bit"}, "Qwen-3.6-27b-MLX-8bit", "pong")
			defer srv.Close()
			writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", srv.URL+"/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"Qwen-3.6-27b-MLX-8bit"})

			sc := &fakeServiceConfig{
				providers: map[string]ServiceProviderEntry{
					"live": {Type: "openai", BaseURL: srv.URL + "/v1", Model: "fallback-model"},
				},
				names:       []string{"live"},
				defaultName: "live",
			}
			svc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			final := executeAndFinal(t, svc, ServiceExecuteRequest{
				Prompt:          "ping",
				Model:           tc.request,
				Timeout:         5 * time.Second,
				ProviderTimeout: 2 * time.Second,
			})
			if final.Status != "success" {
				t.Fatalf("Status = %q, want success (error=%q)", final.Status, final.Error)
			}
			if final.RoutingActual == nil {
				t.Fatal("RoutingActual is nil")
			}
			if final.RoutingActual.Model != "Qwen-3.6-27b-MLX-8bit" {
				t.Fatalf("RoutingActual.Model = %q, want canonical model", final.RoutingActual.Model)
			}
		})
	}
}

func tempDiscoveryCacheDir(t *testing.T) string {
	t.Helper()
	prefix := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	dir, err := os.MkdirTemp("", prefix+"-*")
	if err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	t.Cleanup(func() {
		var err error
		for i := 0; i < 20; i++ {
			err = os.RemoveAll(dir)
			if err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("remove cache dir %s: %v", dir, err)
	})
	return dir
}
