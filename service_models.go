package fizeau

// This file implements ListModels for the FizeauService service.
// It lives in the root package to avoid import cycles; provider and catalog
// data is injected via ServiceConfig (defined in service.go).
//
// Provider-backed models are assembled from the unified model snapshot used by
// the CLI model inventory path. Codex and Claude expose a separate
// harness-native surface backed by PTY/CLI evidence.

import (
	"context"
	"fmt"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/runtimesignals"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// ListModels returns models matching the filter, with full metadata.
// Empty filter returns all models from every reachable provider.
func (s *service) ListModels(ctx context.Context, filter ModelFilter) ([]ModelInfo, error) {
	if filter.Harness != "" && harnesses.ResolveHarnessAlias(filter.Harness) != "fiz" {
		return s.listModelsForSubprocessHarness(filter), nil
	}

	sc := s.opts.ServiceConfig
	if sc == nil {
		return nil, fmt.Errorf("service: no ServiceConfig provided; pass ServiceOptions.ServiceConfig")
	}

	// Load the model catalog once for cross-referencing.
	cat, _ := modelcatalog.Default() // ignore error: catalog miss is non-fatal
	cacheRoot, err := serviceSnapshotCacheRoot()
	if err != nil {
		return nil, err
	}
	snapshot, err := assembleModelSnapshotFromServiceConfigWithOptions(ctx, sc, cat, cacheRoot, modelsnapshot.AssembleOptions{Refresh: modelsnapshot.RefreshForce})
	if err != nil {
		return nil, err
	}
	out := s.listModelsFromSnapshot(ctx, sc, cat, snapshot, filter)
	// Provider-backed models come only from the configured providers block. When
	// those endpoints are unreachable (or none are configured) the snapshot is
	// empty, which would leave callers with no routing floor even though
	// subscription CLI harnesses (claude/codex/gemini) may be available on PATH.
	// Append the available subscription-harness tiers so the unfiltered inventory
	// always reflects every reachable surface.
	out = append(out, s.availableSubscriptionHarnessModels(filter, out)...)
	return out, nil
}

// availableSubscriptionHarnessModels enumerates the subprocess subscription
// harnesses (claude/codex/gemini etc.) that are available on the system and
// returns their tier ModelInfos. It honors filter.Provider, excludes the
// embedded "fiz" harness, HTTP-only providers, and test-only harnesses, and
// dedups against harnesses already represented in the provider-backed output.
func (s *service) availableSubscriptionHarnessModels(filter ModelFilter, existing []ModelInfo) []ModelInfo {
	represented := make(map[string]struct{}, len(existing))
	for _, info := range existing {
		represented[info.Harness] = struct{}{}
		represented[info.Provider] = struct{}{}
	}
	cat, _ := modelcatalog.Default()
	var out []ModelInfo
	for _, st := range s.registry.Discover() {
		if !st.Available {
			continue
		}
		name := st.Name
		if filter.Provider != "" && filter.Provider != name {
			continue
		}
		if _, ok := represented[name]; ok {
			continue
		}
		cfg, ok := s.registry.Get(name)
		if !ok {
			continue
		}
		if name == "fiz" || cfg.TestOnly || cfg.IsHTTPProvider || harnessRunsInProcessOrHTTP(cfg) {
			continue
		}
		out = append(out, s.subscriptionHarnessTierModels(name, cfg, cat)...)
		represented[name] = struct{}{}
	}
	return out
}

func (s *service) listModelsForSubprocessHarness(filter ModelFilter) []ModelInfo {
	name := harnesses.ResolveHarnessAlias(filter.Harness)
	cfg, ok := s.registry.Get(name)
	if !ok || harnessRunsInProcessOrHTTP(cfg) {
		return nil
	}
	if filter.Provider != "" && filter.Provider != name {
		return nil
	}
	cat, _ := modelcatalog.Default()
	return s.subscriptionHarnessTierModels(name, cfg, cat)
}

// subscriptionHarnessTierModels builds the tier ModelInfos for a single
// subprocess subscription harness, drawing model IDs from the harness's
// documented CLI surface and cross-referencing the model catalog for power,
// cost, and context metadata. It returns nil when the harness exposes no model
// IDs. Shared by the harness-pinned ListModels path and the unfiltered path.
func (s *service) subscriptionHarnessTierModels(name string, cfg harnesses.HarnessConfig, cat *modelcatalog.Catalog) []ModelInfo {
	modelIDs := subprocessHarnessModelIDs(name, cfg)
	rows := serviceimpl.SubscriptionHarnessTierModels(context.Background(), serviceimpl.SubscriptionHarnessInventoryInput{
		Name:     name,
		Config:   cfg,
		ModelIDs: modelIDs,
		Catalog:  cat,
		EffectiveCostForModel: func(id string) (float64, bool) {
			return serviceimpl.CatalogCostUSDPer1kTokens(cat, id)
		},
	})
	out := make([]ModelInfo, 0, len(rows))
	for _, row := range rows {
		info := modelInfoFromServiceImplInventoryRow(row)
		attachRuntimeSignalToModelInfo(&info, name)
		info.Utilization = s.routeUtilizationEvidence(name, info.ServerInstance, info.EndpointName, info.ID)
		out = append(out, info)
	}
	return out
}

func attachRuntimeSignalToModelInfo(info *ModelInfo, providerName string) {
	if info == nil {
		return
	}
	cacheRoot, err := serviceSnapshotCacheRoot()
	if err != nil {
		return
	}
	sig, ok := runtimesignals.ReadCached(&discoverycache.Cache{Root: cacheRoot}, providerName)
	if !ok || sig == nil || sig.RecordedAt.IsZero() {
		return
	}
	recordedAt := sig.RecordedAt.UTC()
	info.HealthFreshnessAt = recordedAt
	info.HealthFreshnessSource = "runtime"
	info.QuotaFreshnessAt = recordedAt
	info.QuotaFreshnessSource = "runtime"
}

// subprocessHarnessModelIDs resolves the documented CLI model surface for a
// subprocess harness. It is a package-level variable so tests can substitute a
// hermetic model list in environments without an interactive TTY for PTY-based
// discovery; production always uses serviceimpl.SubprocessHarnessModelIDs.
var subprocessHarnessModelIDs = func(name string, cfg harnesses.HarnessConfig) []string {
	return serviceimpl.SubprocessHarnessModelIDs(name, cfg)
}

var subprocessHarnessAutoRoutingModels = func(name string, cfg harnesses.HarnessConfig) []string {
	return serviceimpl.SubprocessHarnessAutoRoutingModels(name, cfg)
}

var resolveSubprocessModelAlias = func(harness, model string) string {
	return serviceimpl.ResolveSubprocessModelAlias(harness, model)
}

func claudeCLIExecutableModel(model string) string {
	return serviceimpl.ClaudeCLIExecutableModel(model)
}

func appendUniqueModelIDs(values []string, additions ...string) []string {
	return serviceimpl.AppendUniqueModelIDs(values, additions...)
}

func (s *service) listModelsFromSnapshot(ctx context.Context, sc ServiceConfig, cat *modelcatalog.Catalog, snapshot modelsnapshot.ModelSnapshot, filter ModelFilter) []ModelInfo {
	entries := make(map[string]ServiceProviderEntry, len(sc.ProviderNames()))
	for _, name := range sc.ProviderNames() {
		if filter.Provider != "" && filter.Provider != name {
			continue
		}
		entry, ok := sc.Provider(name)
		if !ok {
			continue
		}
		entries[name] = entry
	}

	implEntries := make(map[string]serviceimpl.ProviderEntry, len(entries))
	for name, entry := range entries {
		implEntries[name] = serviceImplProviderEntry(entry)
	}
	rows := serviceimpl.AssembleModelInventory(ctx, serviceimpl.ModelInventoryInput{
		ProviderNames:   sc.ProviderNames(),
		Providers:       implEntries,
		DefaultProvider: sc.DefaultProviderName(),
		ProviderFilter:  filter.Provider,
		Snapshot:        snapshot,
		Catalog:         cat,
	})
	out := make([]ModelInfo, 0, len(rows))
	for _, row := range rows {
		info := modelInfoFromServiceImplInventoryRow(row)
		info.Utilization = s.routeUtilizationEvidence(info.Provider, info.ServerInstance, info.EndpointName, info.ID)
		out = append(out, info)
	}
	return out
}

func modelInfoFromServiceImplInventoryRow(row serviceimpl.ModelInventoryRow) ModelInfo {
	model := row.Model
	return ModelInfo{
		ID:              model.ID,
		Provider:        model.Provider,
		ProviderType:    model.ProviderType,
		Harness:         model.Harness,
		EndpointName:    model.EndpointName,
		EndpointBaseURL: model.EndpointBaseURL,
		ServerInstance:  model.ServerInstance,
		ContextLength:   model.ContextWindow,
		ContextSource:   row.ContextSource,
		Capabilities:    append([]string(nil), row.Capabilities...),
		Cost: CostInfo{
			InputPerMTok:  model.CostInputPerM,
			OutputPerMTok: model.CostOutputPerM,
		},
		PerfSignal:                    adaptServiceImplPerfSignal(row.PerfSignal),
		Power:                         model.Power,
		AutoRoutable:                  model.AutoRoutable,
		ExactPinOnly:                  model.ExactPinOnly,
		Billing:                       model.Billing,
		ActualCashSpend:               model.ActualCashSpend,
		EffectiveCost:                 model.EffectiveCost,
		EffectiveCostSource:           model.EffectiveCostSource,
		SupportsTools:                 model.SupportsTools,
		DeploymentClass:               model.DeploymentClass,
		HealthFreshnessAt:             model.HealthFreshnessAt,
		HealthFreshnessSource:         model.HealthFreshnessSource,
		QuotaFreshnessAt:              model.QuotaFreshnessAt,
		QuotaFreshnessSource:          model.QuotaFreshnessSource,
		ModelDiscoveryFreshnessAt:     model.DiscoveredAt,
		ModelDiscoveryFreshnessSource: string(model.DiscoveredVia),
		Available:                     row.Available,
		IsDefault:                     row.IsDefault,
		RankPosition:                  row.RankPosition,
	}
}

type modelDiscoveryEndpoint struct {
	Name           string
	BaseURL        string
	ServerInstance string
}

func modelDiscoveryEndpoints(entry ServiceProviderEntry) []modelDiscoveryEndpoint {
	endpoints := serviceimpl.ModelDiscoveryEndpoints(serviceImplProviderEntry(entry))
	out := make([]modelDiscoveryEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, modelDiscoveryEndpoint{
			Name:           endpoint.Name,
			BaseURL:        endpoint.BaseURL,
			ServerInstance: endpoint.ServerInstance,
		})
	}
	return out
}

func endpointDisplayName(name, baseURL string) string {
	return serviceimpl.EndpointDisplayName(name, baseURL)
}

// resolveContextEvidence resolves the context window for a model using the
// precedence chain: provider config > provider API > catalog > default.
func resolveContextEvidence(ctx context.Context, entry ServiceProviderEntry, modelID string, cat *modelcatalog.Catalog) (int, string) {
	return serviceimpl.ResolveContextEvidence(ctx, serviceImplProviderEntry(entry), modelID, cat)
}

// catalogCostAndPerf extracts CostInfo and PerfSignal for a model from the catalog.
func catalogCostAndPerf(cat *modelcatalog.Catalog, modelID string) (CostInfo, PerfSignal) {
	cost, perf := serviceimpl.CatalogCostAndPerf(cat, modelID)
	return adaptServiceImplCostInfo(cost), adaptServiceImplPerfSignal(perf)
}

func catalogPowerEligibility(cat *modelcatalog.Catalog, modelID string) (int, bool, bool) {
	return serviceimpl.CatalogPowerEligibility(cat, modelID)
}

// catalogPowerForModel returns the catalog-projected power for a model
// (CONTRACT-003 § Catalog Power Projection). Returns 0 when the catalog
// is nil or the model has no entry, which the contract documents as
// "unknown / exact-pin-only / no catalog entry" for the
// ServiceRoutingActual.Power surface.
func catalogPowerForModel(cat *modelcatalog.Catalog, modelID string) int {
	return serviceimpl.CatalogPowerForModel(cat, modelID)
}

// providerCapabilities returns the capability set for a provider entry.
func providerCapabilities(entry ServiceProviderEntry) []string {
	return serviceimpl.ProviderCapabilities(serviceImplProviderEntry(entry))
}
