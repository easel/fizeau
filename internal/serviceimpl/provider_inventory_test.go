package serviceimpl

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/serverinstance"
)

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
