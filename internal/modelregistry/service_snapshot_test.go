package modelregistry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
)

func TestServiceConfigSnapshotParity(t *testing.T) {
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: t.TempDir()}
	capturedAt := time.Date(2026, 5, 12, 14, 0, 0, 0, time.UTC)
	writeSnapshotParityFixture(t, cache, "studio-alpha", capturedAt, []string{"qwen3.5-27b"})
	writeSnapshotParityFixture(t, cache, "studio-beta", capturedAt, []string{"qwen3.5-27b"})
	writeSnapshotParityFixture(t, cache, "claude-subscription", capturedAt, []string{"claude-sonnet-4-20250514"})

	cfg := &config.Config{
		Default: "studio",
		Providers: map[string]config.ProviderConfig{
			"studio": {
				Type: "lmstudio",
				Endpoints: []config.ProviderEndpoint{
					{Name: "alpha", BaseURL: "http://alpha.example/v1", ServerInstance: "alpha"},
					{Name: "beta", BaseURL: "http://beta.example/v1", ServerInstance: "beta"},
				},
			},
			"claude-subscription": {
				Type:    "claude",
				Billing: string(modelcatalog.BillingModelSubscription),
			},
		},
	}
	cat := loadTestCatalog(t)

	got, err := AssembleWithOptions(context.Background(), cfg, cat, cache, AssembleOptions{Refresh: RefreshNone})
	if err != nil {
		t.Fatalf("AssembleWithOptions: %v", err)
	}
	wantCfg := &modelsnapshot.Config{
		Default: cfg.Default,
		Providers: map[string]modelsnapshot.ProviderConfig{
			"studio": {
				Type: "lmstudio",
				Endpoints: []modelsnapshot.ProviderEndpoint{
					{Name: "alpha", BaseURL: "http://alpha.example/v1", ServerInstance: "alpha"},
					{Name: "beta", BaseURL: "http://beta.example/v1", ServerInstance: "beta"},
				},
			},
			"claude-subscription": {
				Type:    "claude",
				Billing: string(modelcatalog.BillingModelSubscription),
			},
		},
	}
	want, err := modelsnapshot.AssembleWithOptions(context.Background(), wantCfg, cat, cache, modelsnapshot.AssembleOptions{Refresh: modelsnapshot.RefreshNone})
	if err != nil {
		t.Fatalf("modelsnapshot.AssembleWithOptions: %v", err)
	}

	assertParityRows(t, got.Models, want.Models)
}

func TestServiceConfigSnapshotRedactsSecrets(t *testing.T) {
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: t.TempDir()}
	cfg := &modelsnapshot.Config{
		Default: "openrouter",
		Providers: map[string]modelsnapshot.ProviderConfig{
			"openrouter": {
				Type:    "openrouter",
				BaseURL: "http://127.0.0.1:1/v1",
				APIKey:  "super-secret-key",
				Headers: map[string]string{
					"Authorization": "Bearer super-secret-key",
				},
			},
		},
	}
	cat := loadTestCatalog(t)

	snapshot, err := modelsnapshot.AssembleWithOptions(context.Background(), cfg, cat, cache, modelsnapshot.AssembleOptions{Refresh: modelsnapshot.RefreshForce})
	if err != nil {
		t.Fatalf("AssembleWithOptions: %v", err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(data), "super-secret-key") {
		t.Fatalf("snapshot JSON leaked secret material: %s", data)
	}
	for source, meta := range snapshot.Sources {
		if strings.Contains(meta.Error, "super-secret-key") {
			t.Fatalf("source %s error leaked secret material: %q", source, meta.Error)
		}
	}
}

func writeSnapshotParityFixture(t *testing.T, cache *discoverycache.Cache, source string, capturedAt time.Time, models []string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		CapturedAt time.Time `json:"captured_at"`
		Models     []string  `json:"models,omitempty"`
		Source     string    `json:"source,omitempty"`
	}{
		CapturedAt: capturedAt,
		Models:     models,
		Source:     "test-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	src := discoverycache.Source{
		Tier:            "discovery",
		Name:            source,
		TTL:             time.Hour,
		RefreshDeadline: time.Second,
	}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatal(err)
	}
}

func assertParityRows(t *testing.T, got []KnownModel, want []modelsnapshot.KnownModel) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d", len(got), len(want))
	}
	type rowKey struct {
		Provider        string
		ID              string
		EndpointName    string
		EndpointBaseURL string
		ServerInstance  string
		ProviderType    string
		Harness         string
		Billing         string
	}
	indexGot := func(rows []KnownModel) map[rowKey]KnownModel {
		out := make(map[rowKey]KnownModel, len(rows))
		for _, row := range rows {
			out[rowKey{
				Provider:        row.Provider,
				ID:              row.ID,
				EndpointName:    row.EndpointName,
				EndpointBaseURL: row.EndpointBaseURL,
				ServerInstance:  row.ServerInstance,
				ProviderType:    row.ProviderType,
				Harness:         row.Harness,
				Billing:         string(row.Billing),
			}] = row
		}
		return out
	}
	indexWant := func(rows []modelsnapshot.KnownModel) map[rowKey]modelsnapshot.KnownModel {
		out := make(map[rowKey]modelsnapshot.KnownModel, len(rows))
		for _, row := range rows {
			out[rowKey{
				Provider:        row.Provider,
				ID:              row.ID,
				EndpointName:    row.EndpointName,
				EndpointBaseURL: row.EndpointBaseURL,
				ServerInstance:  row.ServerInstance,
				ProviderType:    row.ProviderType,
				Harness:         row.Harness,
				Billing:         string(row.Billing),
			}] = row
		}
		return out
	}
	gotRows := indexGot(got)
	wantRows := indexWant(want)
	if len(gotRows) != len(wantRows) {
		t.Fatalf("row key count = %d, want %d", len(gotRows), len(wantRows))
	}
	for key, wantRow := range wantRows {
		gotRow, ok := gotRows[key]
		if !ok {
			t.Fatalf("missing row %s; got %#v", key, gotRows)
		}
		if gotRow.ProviderType != wantRow.ProviderType || gotRow.Harness != wantRow.Harness || string(gotRow.Billing) != string(wantRow.Billing) {
			t.Fatalf("row %s mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if gotRow.ActualCashSpend != wantRow.ActualCashSpend || gotRow.EffectiveCost != wantRow.EffectiveCost || gotRow.EffectiveCostSource != wantRow.EffectiveCostSource {
			t.Fatalf("row %s cost mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if gotRow.SupportsTools != wantRow.SupportsTools || gotRow.DeploymentClass != wantRow.DeploymentClass {
			t.Fatalf("row %s support mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if gotRow.ContextWindow != wantRow.ContextWindow || gotRow.ContextWindowSource != wantRow.ContextWindowSource {
			t.Fatalf("row %s context limit mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if gotRow.MaxCompletionTokens != wantRow.MaxCompletionTokens || gotRow.MaxCompletionTokensSource != wantRow.MaxCompletionTokensSource {
			t.Fatalf("row %s output limit mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if string(gotRow.DiscoveredVia) != string(wantRow.DiscoveredVia) || !gotRow.DiscoveredAt.Equal(wantRow.DiscoveredAt) {
			t.Fatalf("row %s discovery freshness mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if gotRow.HealthFreshnessSource != wantRow.HealthFreshnessSource || !gotRow.HealthFreshnessAt.Equal(wantRow.HealthFreshnessAt) {
			t.Fatalf("row %s health freshness mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
		if gotRow.QuotaFreshnessSource != wantRow.QuotaFreshnessSource || !gotRow.QuotaFreshnessAt.Equal(wantRow.QuotaFreshnessAt) {
			t.Fatalf("row %s quota freshness mismatch:\n got %#v\nwant %#v", key, gotRow, wantRow)
		}
	}
}
