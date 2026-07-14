package serviceimpl

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/serverinstance"
	"github.com/easel/fizeau/internal/statusview"
)

const defaultProviderProbeTimeout = 5 * time.Second

// ProviderInventoryInput carries the implementation-local inputs needed to
// probe and assemble provider status without depending on root public types.
type ProviderInventoryInput struct {
	ProviderNames   []string
	Providers       map[string]ProviderEntry
	DefaultProvider string
	ProbeTimeout    time.Duration

	// Probe and Now are deterministic test seams. Production callers leave
	// them nil to use ProbeServiceProviderDetailed and the wall clock.
	Probe func(context.Context, ProviderEntry) ProviderProbeResult
	Now   func() time.Time
}

// ProviderEndpointStatus is the implementation-local projection of one
// configured endpoint probe.
type ProviderEndpointStatus struct {
	Name           string
	BaseURL        string
	ServerInstance string
	ProbeURL       string
	Status         string
	Source         string
	CapturedAt     time.Time
	Fresh          bool
	LastSuccessAt  time.Time
	ModelCount     int
	LastError      *statusview.Error
}

// ProviderStatusProbeResult pairs the provider-level probe aggregate with its
// endpoint-level evidence.
type ProviderStatusProbeResult struct {
	ProviderProbeResult
	EndpointStatuses []ProviderEndpointStatus
}

// ProviderInventoryRow is the API-neutral provider inventory projection
// consumed by the root public facade.
type ProviderInventoryRow struct {
	Name             string
	Type             string
	BaseURL          string
	Endpoints        []ProviderEndpoint
	Status           string
	ModelCount       int
	Capabilities     []string
	Billing          modelcatalog.BillingModel
	IncludeByDefault bool
	IsDefault        bool
	DefaultModel     string
	Auth             statusview.Account
	EndpointStatus   []ProviderEndpointStatus
	Quota            *statusview.Quota
	LastError        *statusview.Error
}

// BuildProviderInventory probes configured providers concurrently while
// preserving ProviderNames order in the returned inventory.
func BuildProviderInventory(ctx context.Context, input ProviderInventoryInput) []ProviderInventoryRow {
	probeTimeout := input.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = defaultProviderProbeTimeout
	}
	probe := input.Probe
	if probe == nil {
		probe = ProbeServiceProviderDetailed
	}
	now := input.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	rows := make([]ProviderInventoryRow, len(input.ProviderNames))
	var wg sync.WaitGroup
	for i, name := range input.ProviderNames {
		wg.Add(1)
		go func(index int, providerName string) {
			defer wg.Done()
			rows[index] = buildProviderInventoryRow(ctx, providerName, input, probeTimeout, probe, now)
		}(i, name)
	}
	wg.Wait()
	return rows
}

func buildProviderInventoryRow(
	ctx context.Context,
	name string,
	input ProviderInventoryInput,
	probeTimeout time.Duration,
	probe func(context.Context, ProviderEntry) ProviderProbeResult,
	now func() time.Time,
) ProviderInventoryRow {
	entry, ok := input.Providers[name]
	if !ok {
		return ProviderInventoryRow{
			Name:   name,
			Status: "error: provider not found in config",
		}
	}

	row := ProviderInventoryRow{
		Name:             name,
		Type:             NormalizeProviderType(entry.Type),
		BaseURL:          entry.BaseURL,
		Endpoints:        cloneProviderEndpoints(entry.Endpoints),
		Billing:          ServiceProviderBilling(entry),
		IncludeByDefault: ServiceProviderDefaultInclusion(entry),
		IsDefault:        name == input.DefaultProvider,
		DefaultModel:     entry.Model,
	}

	if entry.ConfigError != "" {
		capturedAt := now()
		row.Status = "error: invalid provider config"
		providerProbe := ProviderProbeResult{
			Status: "error: invalid provider config",
			Detail: entry.ConfigError,
		}
		row.EndpointStatus = ProviderEndpointStatusesFromProbe(entry, providerProbe, capturedAt)
		row.Auth = statusview.ProviderAuthStatus(statusProvider(entry), row.Status, capturedAt)
		row.Quota = statusview.ProviderQuotaState(statusProvider(entry), capturedAt)
		row.LastError = statusview.ErrorForStatusDetail(row.Status, entry.ConfigError, "service provider config", capturedAt)
		return row
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	capturedAt := now()
	providerProbe := ProbeProviderStatus(probeCtx, entry, capturedAt, probe)
	row.Status = providerProbe.Status
	row.ModelCount = providerProbe.ModelCount
	row.Capabilities = append([]string(nil), providerProbe.Capabilities...)
	row.Auth = statusview.ProviderAuthStatus(statusProvider(entry), row.Status, capturedAt)
	row.EndpointStatus = providerProbe.EndpointStatuses
	row.Quota = statusview.ProviderQuotaState(statusProvider(entry), capturedAt)
	lastErrorSource := "service provider config"
	if len(row.EndpointStatus) > 0 {
		lastErrorSource = row.EndpointStatus[0].Source
	}
	row.LastError = statusview.ErrorForStatusDetail(row.Status, providerProbe.Detail, lastErrorSource, capturedAt)
	return row
}

// ProbeProviderStatus probes every configured endpoint and returns both the
// provider aggregate and the endpoint-level evidence. probe may be nil to use
// the production provider probe.
func ProbeProviderStatus(
	ctx context.Context,
	entry ProviderEntry,
	capturedAt time.Time,
	probe func(context.Context, ProviderEntry) ProviderProbeResult,
) ProviderStatusProbeResult {
	if probe == nil {
		probe = ProbeServiceProviderDetailed
	}
	endpoints := ModelDiscoveryEndpoints(entry)
	if len(endpoints) == 0 {
		result := probe(ctx, entry)
		return ProviderStatusProbeResult{
			ProviderProbeResult: result,
			EndpointStatuses:    ProviderEndpointStatusesFromProbe(entry, result, capturedAt),
		}
	}

	statuses := make([]ProviderEndpointStatus, 0, len(endpoints))
	aggregate := ProviderProbeResult{Status: "error: endpoint probe did not run"}
	for _, endpoint := range endpoints {
		endpointEntry := entry
		endpointEntry.BaseURL = endpoint.BaseURL
		endpointProbe := probe(ctx, endpointEntry)
		endpointProbe.EndpointName = endpoint.Name
		endpointProbe.BaseURL = endpoint.BaseURL
		statuses = append(statuses, providerEndpointStatusFromProbe(endpoint.Name, endpoint.BaseURL, endpointProbe, capturedAt))
		if endpointProbe.Status == "connected" {
			if aggregate.Status != "connected" {
				aggregate.Status = "connected"
				aggregate.Capabilities = append([]string(nil), endpointProbe.Capabilities...)
				aggregate.Detail = ""
			}
			aggregate.ModelCount += endpointProbe.ModelCount
			continue
		}
		if aggregate.Status == "connected" {
			continue
		}
		if ShouldPreferProviderProbe(endpointProbe, aggregate) {
			aggregate.Status = endpointProbe.Status
			aggregate.Detail = endpointProbe.Detail
			aggregate.Capabilities = append([]string(nil), endpointProbe.Capabilities...)
			aggregate.BaseURL = endpointProbe.BaseURL
			aggregate.EndpointName = endpointProbe.EndpointName
		}
	}
	return ProviderStatusProbeResult{
		ProviderProbeResult: aggregate,
		EndpointStatuses:    statuses,
	}
}

// ProviderEndpointStatusesFromProbe projects a provider-level result onto the
// configured endpoint identities when no explicit endpoint breakdown exists.
func ProviderEndpointStatusesFromProbe(entry ProviderEntry, probe ProviderProbeResult, capturedAt time.Time) []ProviderEndpointStatus {
	endpoints := ModelDiscoveryEndpoints(entry)
	if len(endpoints) == 0 {
		probe.ServerInstance = entry.ServerInstance
		return []ProviderEndpointStatus{providerEndpointStatusFromProbe("default", entry.BaseURL, probe, capturedAt)}
	}
	out := make([]ProviderEndpointStatus, 0, len(endpoints))
	for _, endpoint := range endpoints {
		probe.ServerInstance = endpoint.ServerInstance
		out = append(out, providerEndpointStatusFromProbe(endpoint.Name, endpoint.BaseURL, probe, capturedAt))
	}
	return out
}

func providerEndpointStatusFromProbe(name, baseURL string, probe ProviderProbeResult, capturedAt time.Time) ProviderEndpointStatus {
	source := strings.TrimRight(baseURL, "/") + "/models"
	if baseURL == "" {
		source = "service provider config"
	}
	out := ProviderEndpointStatus{
		Name:           EndpointDisplayName(name, baseURL),
		BaseURL:        baseURL,
		ServerInstance: serverinstance.Normalize(baseURL, probe.ServerInstance),
		ProbeURL:       source,
		Status:         statusview.EndpointStatusFor(probe.Status),
		Source:         source,
		CapturedAt:     capturedAt,
		Fresh:          true,
		ModelCount:     probe.ModelCount,
		LastError:      statusview.ErrorForStatusDetail(probe.Status, probe.Detail, source, capturedAt),
	}
	if out.Status == "connected" {
		out.LastSuccessAt = capturedAt
	}
	return out
}

func statusProvider(entry ProviderEntry) statusview.ServiceProvider {
	endpoints := make([]statusview.ServiceProviderEndpoint, 0, len(entry.Endpoints))
	for _, endpoint := range entry.Endpoints {
		endpoints = append(endpoints, statusview.ServiceProviderEndpoint{
			Name:    endpoint.Name,
			BaseURL: endpoint.BaseURL,
		})
	}
	return statusview.ServiceProvider{
		Type:      NormalizeProviderType(entry.Type),
		BaseURL:   entry.BaseURL,
		Endpoints: endpoints,
		APIKey:    entry.APIKey,
	}
}

func cloneProviderEndpoints(endpoints []ProviderEndpoint) []ProviderEndpoint {
	if len(endpoints) == 0 {
		return nil
	}
	return append([]ProviderEndpoint(nil), endpoints...)
}
