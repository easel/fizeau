package serviceimpl

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/serverinstance"
)

func TestNormalizeProviderType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "lmstudio", want: "lmstudio"},
		{input: "openai", want: "openai"},
		{input: "", want: "openai"},
		{input: "anthropic", want: "anthropic"},
		{input: "custom", want: "custom"},
		{input: "  OpenRouter\t", want: "openrouter"},
	}
	for _, test := range tests {
		if got := NormalizeProviderType(test.input); got != test.want {
			t.Errorf("NormalizeProviderType(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestServiceProviderMetadataHonorsExplicitConfigPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		entry        ProviderEntry
		wantBilling  modelcatalog.BillingModel
		wantIncluded bool
	}{
		{
			name:         "type metadata supplies defaults",
			entry:        ProviderEntry{Type: "lmstudio"},
			wantBilling:  modelcatalog.BillingModelFixed,
			wantIncluded: true,
		},
		{
			name: "explicit billing overrides type metadata",
			entry: ProviderEntry{
				Type:    "lmstudio",
				Billing: modelcatalog.BillingModelPerToken,
			},
			wantBilling:  modelcatalog.BillingModelPerToken,
			wantIncluded: false,
		},
		{
			name: "explicit exclusion overrides fixed billing default",
			entry: ProviderEntry{
				Type:                "lmstudio",
				IncludeByDefault:    false,
				IncludeByDefaultSet: true,
			},
			wantBilling:  modelcatalog.BillingModelFixed,
			wantIncluded: false,
		},
		{
			name: "explicit inclusion overrides metered billing default",
			entry: ProviderEntry{
				Type:                "anthropic",
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
			wantBilling:  modelcatalog.BillingModelPerToken,
			wantIncluded: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ServiceProviderBilling(test.entry); got != test.wantBilling {
				t.Errorf("ServiceProviderBilling() = %q, want %q", got, test.wantBilling)
			}
			if got := ServiceProviderDefaultInclusion(test.entry); got != test.wantIncluded {
				t.Errorf("ServiceProviderDefaultInclusion() = %t, want %t", got, test.wantIncluded)
			}
		})
	}
}

func TestBuildProviderInventoryInvalidConfigSkipsProbe(t *testing.T) {
	var probeCalls atomic.Int32
	rows := BuildProviderInventory(context.Background(), ProviderInventoryInput{
		ProviderNames: []string{"broken"},
		Providers: map[string]ProviderEntry{
			"broken": {
				Type:        "not-a-provider",
				BaseURL:     "http://broken.invalid/v1",
				ConfigError: `unknown type "not-a-provider"`,
			},
		},
		Probe: func(context.Context, ProviderEntry) ProviderProbeResult {
			probeCalls.Add(1)
			return ProviderProbeResult{Status: "connected"}
		},
	})

	if got := probeCalls.Load(); got != 0 {
		t.Fatalf("probe calls = %d, want 0 for invalid config", got)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Status != "error: invalid provider config" {
		t.Fatalf("status = %q, want invalid provider config", row.Status)
	}
	if row.LastError == nil || row.LastError.Detail != `unknown type "not-a-provider"` {
		t.Fatalf("LastError = %#v, want config detail", row.LastError)
	}
	if len(row.EndpointStatus) != 1 || row.EndpointStatus[0].LastError == nil {
		t.Fatalf("EndpointStatus = %#v, want endpoint-level config error", row.EndpointStatus)
	}
}

func TestBuildProviderInventoryPreservesEndpointStatus(t *testing.T) {
	capturedAt := time.Date(2026, 7, 14, 16, 30, 0, 0, time.UTC)
	entry := ProviderEntry{
		Type: "omlx",
		Endpoints: []ProviderEndpoint{
			{Name: "dead", BaseURL: "http://dead.example:1234/v1"},
			{Name: "healthy", BaseURL: "http://healthy.example:1234/v1"},
		},
		Model: "Qwen3.6-27B-MLX-8bit",
	}

	rows := BuildProviderInventory(context.Background(), ProviderInventoryInput{
		ProviderNames:   []string{"missing", "local"},
		Providers:       map[string]ProviderEntry{"local": entry},
		DefaultProvider: "local",
		Now:             func() time.Time { return capturedAt },
		Probe: func(_ context.Context, endpoint ProviderEntry) ProviderProbeResult {
			switch endpoint.BaseURL {
			case "http://dead.example:1234/v1":
				return ProviderProbeResult{Status: "unreachable", Detail: "dial tcp: connection refused"}
			case "http://healthy.example:1234/v1":
				return ProviderProbeResult{
					Status:       "connected",
					ModelCount:   2,
					Capabilities: []string{"tool_use", "streaming", "json_mode", "reasoning_control"},
				}
			default:
				return ProviderProbeResult{Status: "error: unexpected endpoint " + endpoint.BaseURL}
			}
		},
	})

	if len(rows) != 2 || rows[0].Name != "missing" || rows[1].Name != "local" {
		t.Fatalf("rows must preserve ProviderNames order: %#v", rows)
	}
	if rows[0].Status != "error: provider not found in config" {
		t.Fatalf("missing provider status = %q", rows[0].Status)
	}

	row := rows[1]
	if row.Status != "connected" || row.ModelCount != 2 {
		t.Fatalf("aggregate status/count = %q/%d, want connected/2", row.Status, row.ModelCount)
	}
	if !slices.Equal(row.Capabilities, []string{"tool_use", "streaming", "json_mode", "reasoning_control"}) {
		t.Fatalf("capabilities = %#v", row.Capabilities)
	}
	if row.Type != "omlx" || !row.IsDefault || !row.IncludeByDefault || row.Billing != "fixed" || row.DefaultModel != entry.Model {
		t.Fatalf("provider metadata was not preserved: %#v", row)
	}
	if len(row.Endpoints) != 2 || row.Endpoints[0] != entry.Endpoints[0] || row.Endpoints[1] != entry.Endpoints[1] {
		t.Fatalf("configured endpoints = %#v, want %#v", row.Endpoints, entry.Endpoints)
	}
	if len(row.EndpointStatus) != 2 {
		t.Fatalf("endpoint statuses = %#v, want two", row.EndpointStatus)
	}

	dead := row.EndpointStatus[0]
	if dead.Name != "dead" || dead.BaseURL != entry.Endpoints[0].BaseURL || dead.ProbeURL != entry.Endpoints[0].BaseURL+"/models" {
		t.Fatalf("dead endpoint identity = %#v", dead)
	}
	if dead.ServerInstance != serverinstance.Normalize(dead.BaseURL, "") {
		t.Fatalf("dead server instance = %q", dead.ServerInstance)
	}
	if dead.Status != "unreachable" || !dead.Fresh || !dead.CapturedAt.Equal(capturedAt) || !dead.LastSuccessAt.IsZero() {
		t.Fatalf("dead endpoint status = %#v", dead)
	}
	if dead.LastError == nil || dead.LastError.Type != "unavailable" || dead.LastError.Detail != "dial tcp: connection refused" || dead.LastError.Source != dead.ProbeURL {
		t.Fatalf("dead endpoint error = %#v", dead.LastError)
	}

	healthy := row.EndpointStatus[1]
	if healthy.Name != "healthy" || healthy.Status != "connected" || healthy.ModelCount != 2 || healthy.LastError != nil {
		t.Fatalf("healthy endpoint status = %#v", healthy)
	}
	if !healthy.CapturedAt.Equal(capturedAt) || !healthy.LastSuccessAt.Equal(capturedAt) {
		t.Fatalf("healthy endpoint timestamps = %#v", healthy)
	}
	if row.LastError != nil {
		t.Fatalf("connected aggregate LastError = %#v, want nil", row.LastError)
	}
}
