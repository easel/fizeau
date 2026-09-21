package modelregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/stretchr/testify/require"
)

func TestAssembleRefreshBackgroundReturnsStaleRowsBeforeRevalidate(t *testing.T) {
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: t.TempDir()}
	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)

	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		select {
		case <-refreshStarted:
		default:
			close(refreshStarted)
		}
		<-releaseRefresh
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"fresh-model"}]}`))
	}))
	t.Cleanup(server.Close)
	source := endpointSourceName("studio", "alpha", server.URL+"/v1", "")
	writeDiscoveryFixture(t, cache, source, capturedAt, []string{"stale-model"})
	stalePath := filepath.Join(cache.Root, "discovery", source+".json")
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stalePath, past, past))

	cfg := &config.Config{Providers: map[string]config.ProviderConfig{
		"studio": {
			Type: "lmstudio",
			Endpoints: []config.ProviderEndpoint{{
				Name:    "alpha",
				BaseURL: server.URL + "/v1",
			}},
			Billing: string(modelcatalog.BillingModelFixed),
		},
	}}
	cat := loadTestCatalog(t)

	start := time.Now()
	snapshot, err := AssembleWithOptions(context.Background(), cfg, cat, cache, AssembleOptions{Refresh: RefreshBackground})
	require.NoError(t, err)
	require.Len(t, snapshot.Models, 1)
	require.Equal(t, "stale-model", snapshot.Models[0].ID)
	require.Less(t, time.Since(start), 100*time.Millisecond, "background refresh should not block stale read")

	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected background refresh to reach /v1/models")
	}

	close(releaseRefresh)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fresh, err := AssembleWithOptions(context.Background(), cfg, cat, cache, AssembleOptions{Refresh: RefreshNone})
		require.NoError(t, err)
		if len(fresh.Models) == 1 && fresh.Models[0].ID == "fresh-model" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("background refresh did not replace stale rows with fresh rows")
}

func TestAssembleRefreshBackgroundSurvivesCanceledCallerContext(t *testing.T) {
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: t.TempDir()}
	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)

	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		select {
		case <-refreshStarted:
		default:
			close(refreshStarted)
		}
		// Hold the response until the caller assertion has observed the stale
		// row. An unblocked handler can commit before that read on a fast host.
		<-releaseRefresh
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"fresh-model"}]}`))
	}))
	t.Cleanup(server.Close)
	source := endpointSourceName("studio", "alpha", server.URL+"/v1", "")
	writeDiscoveryFixture(t, cache, source, capturedAt, []string{"stale-model"})
	stalePath := filepath.Join(cache.Root, "discovery", source+".json")
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stalePath, past, past))

	cfg := &config.Config{Providers: map[string]config.ProviderConfig{
		"studio": {
			Type: "lmstudio",
			Endpoints: []config.ProviderEndpoint{{
				Name:    "alpha",
				BaseURL: server.URL + "/v1",
			}},
			Billing: string(modelcatalog.BillingModelFixed),
		},
	}}
	cat := loadTestCatalog(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snapshot, err := AssembleWithOptions(ctx, cfg, cat, cache, AssembleOptions{Refresh: RefreshBackground})
	require.NoError(t, err)
	require.Len(t, snapshot.Models, 1)
	require.Equal(t, "stale-model", snapshot.Models[0].ID)
	close(releaseRefresh)

	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected background refresh to use cache refresh context, not the canceled caller context")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fresh, err := AssembleWithOptions(context.Background(), cfg, cat, cache, AssembleOptions{Refresh: RefreshNone})
		require.NoError(t, err)
		if len(fresh.Models) == 1 && fresh.Models[0].ID == "fresh-model" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("background refresh did not replace stale rows with fresh rows")
}
