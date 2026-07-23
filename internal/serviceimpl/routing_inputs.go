package serviceimpl

import (
	"context"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modeleligibility"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serverinstance"
)

// RoutingInputsInput carries the API-neutral routing facts assembled by the
// root service's state adapters. BuildRoutingInputs owns the deterministic
// snapshot, catalog, eligibility, capability, and cost projection mechanics;
// Base carries process-local health and quota evidence owned by the service.
type RoutingInputsInput struct {
	Base routing.Inputs

	Harnesses        []routing.HarnessEntry
	Providers        map[string]ProviderEntry
	ProviderNames    []string
	HasServiceConfig bool
	Snapshot         modelsnapshot.ModelSnapshot
	Catalog          *modelcatalog.Catalog

	LocalCostUSDPer1kTokens float64
}

// BuildRoutingInputs projects provider snapshot and catalog evidence onto the
// caller-supplied harness inventory, then installs the routing-owned catalog
// closures on Base. It does not read service state, the network, or the root
// public package.
func BuildRoutingInputs(input RoutingInputsInput) routing.Inputs {
	entries := append([]routing.HarnessEntry(nil), input.Harnesses...)
	for i := range entries {
		entry := &entries[i]
		if entry.Name == "fiz" {
			if !input.HasServiceConfig {
				entry.AutoRoutingEligible = false
			} else {
				entry.Providers = SnapshotProviderEntries(SnapshotProviderEntriesInput{
					Providers:               input.Providers,
					ProviderNames:           input.ProviderNames,
					Snapshot:                input.Snapshot,
					Catalog:                 input.Catalog,
					LocalCostUSDPer1kTokens: input.LocalCostUSDPer1kTokens,
				})
				if len(entry.Providers) > 0 {
					entry.SupportsTools = AnyProviderSupportsTools(entry.Providers)
				} else {
					entry.Available = false
				}
			}
		}

		if entry.IsSubscription {
			if tiers := CatalogTierModelsForHarnessSurface(input.Catalog, entry.Surface); len(tiers) > 0 {
				entry.AutoRoutingModels = tiers
				entry.SupportedModels = AppendUniqueModelIDs(entry.SupportedModels, tiers...)
			}
		}
		ApplySubscriptionRoutingCost(entry, input.Catalog)
	}

	out := input.Base
	out.Harnesses = entries
	out.ModelEligibility = RoutingModelEligibility(entries, input.Catalog)
	out.ReasoningResolver = RoutingReasoningResolver(input.Catalog)
	return out
}

// SnapshotProviderEntriesInput carries the configured provider sources and the
// cache-backed model snapshot used to build native fiz routing candidates.
type SnapshotProviderEntriesInput struct {
	Providers     map[string]ProviderEntry
	ProviderNames []string
	Snapshot      modelsnapshot.ModelSnapshot
	Catalog       *modelcatalog.Catalog

	LocalCostUSDPer1kTokens float64
}

// SnapshotProviderEntries converts cache-backed model rows into deterministic
// routing provider entries while preserving endpoint and catalog-alias
// evidence.
func SnapshotProviderEntries(input SnapshotProviderEntriesInput) []routing.ProviderEntry {
	if len(input.ProviderNames) == 0 || len(input.Snapshot.Models) == 0 {
		return nil
	}

	grouped := make(map[snapshotProviderGroupKey][]modelsnapshot.KnownModel)
	for _, row := range input.Snapshot.Models {
		harness := strings.TrimSpace(row.Harness)
		if harness != "" && harness != "fiz" {
			continue
		}
		providerName := strings.TrimSpace(row.Provider)
		if providerName == "" {
			continue
		}
		if _, ok := input.Providers[providerName]; !ok {
			continue
		}
		key := snapshotProviderGroupKey{
			Provider:        providerName,
			EndpointName:    strings.TrimSpace(row.EndpointName),
			EndpointBaseURL: strings.TrimSpace(row.EndpointBaseURL),
			ServerInstance:  strings.TrimSpace(row.ServerInstance),
		}
		grouped[key] = append(grouped[key], row)
	}
	if len(grouped) == 0 {
		return nil
	}

	groupCountByProvider := make(map[string]int)
	for key := range grouped {
		groupCountByProvider[key.Provider]++
	}
	keys := make([]snapshotProviderGroupKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Provider != keys[j].Provider {
			return keys[i].Provider < keys[j].Provider
		}
		if keys[i].EndpointName != keys[j].EndpointName {
			return keys[i].EndpointName < keys[j].EndpointName
		}
		if keys[i].EndpointBaseURL != keys[j].EndpointBaseURL {
			return keys[i].EndpointBaseURL < keys[j].EndpointBaseURL
		}
		return keys[i].ServerInstance < keys[j].ServerInstance
	})

	var entries []routing.ProviderEntry
	for _, key := range keys {
		pcfg, ok := input.Providers[key.Provider]
		if !ok || pcfg.ConfigError != "" {
			continue
		}
		rows := append([]modelsnapshot.KnownModel(nil), grouped[key]...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].EndpointName != rows[j].EndpointName {
				return rows[i].EndpointName < rows[j].EndpointName
			}
			if rows[i].EndpointBaseURL != rows[j].EndpointBaseURL {
				return rows[i].EndpointBaseURL < rows[j].EndpointBaseURL
			}
			if rows[i].ServerInstance != rows[j].ServerInstance {
				return rows[i].ServerInstance < rows[j].ServerInstance
			}
			return rows[i].ID < rows[j].ID
		})

		discoveredIDs := SnapshotModelIDs(rows)
		if defaultModel := strings.TrimSpace(pcfg.Model); defaultModel != "" {
			discoveredIDs = AppendUniqueModelIDs(discoveredIDs, defaultModel)
		}
		ctxWindows, ctxSources := SnapshotProviderContextWindows(pcfg, input.Catalog, rows, discoveredIDs)
		endpointName := SnapshotEndpointName(pcfg, key)
		routeName := key.Provider
		if groupCountByProvider[key.Provider] > 1 {
			switch {
			case endpointName != "":
				routeName = endpointProviderRef(key.Provider, endpointName)
			case key.ServerInstance != "":
				routeName = endpointProviderRef(key.Provider, key.ServerInstance)
			case key.EndpointBaseURL != "":
				routeName = endpointProviderRef(key.Provider, key.EndpointBaseURL)
			}
		}

		baseURL := key.EndpointBaseURL
		if baseURL == "" {
			baseURL = pcfg.BaseURL
		}
		serverID := key.ServerInstance
		if serverID == "" {
			serverID = pcfg.ServerInstance
		}
		serverID = serverinstance.Normalize(baseURL, serverID)

		entry := routing.ProviderEntry{
			Name:                      routeName,
			BaseURL:                   baseURL,
			ServerInstance:            serverID,
			EndpointName:              endpointName,
			EndpointBaseURL:           baseURL,
			DefaultModel:              pcfg.Model,
			Billing:                   pcfg.Billing,
			CostClass:                 ProviderRoutingCostClass(pcfg.Type),
			DiscoveredIDs:             discoveredIDs,
			CatalogIDByModel:          SnapshotCatalogIDByModel(rows),
			DiscoveryAttempted:        true,
			ContextWindows:            ctxWindows,
			ContextWindowSources:      ctxSources,
			ContextWindow:             pcfg.ContextWindow,
			ContextWindowSource:       ContextWindowSourceForProviderConfig(pcfg),
			SupportsTools:             ProviderSupportsTools(input.Catalog, pcfg.Model, discoveredIDs),
			ExcludeFromDefaultRouting: pcfg.IncludeByDefaultSet && !pcfg.IncludeByDefault,
		}
		ApplyEndpointRoutingCost(&entry, pcfg, input.Catalog, input.LocalCostUSDPer1kTokens)
		entries = append(entries, entry)
	}
	return entries
}

type snapshotProviderGroupKey struct {
	Provider        string
	EndpointName    string
	EndpointBaseURL string
	ServerInstance  string
}

// SnapshotEndpointName resolves the configured diagnostic name for a snapshot
// endpoint identity.
func SnapshotEndpointName(pcfg ProviderEntry, key snapshotProviderGroupKey) string {
	endpoints := ModelDiscoveryEndpoints(pcfg)
	trimmedEndpointName := strings.TrimSpace(key.EndpointName)
	trimmedBaseURL := strings.TrimSpace(key.EndpointBaseURL)
	trimmedServerInstance := strings.TrimSpace(key.ServerInstance)
	if len(endpoints) == 0 {
		if trimmedEndpointName != "" {
			if strings.EqualFold(trimmedEndpointName, strings.TrimSpace(key.Provider)) {
				return "default"
			}
			return trimmedEndpointName
		}
		if trimmedServerInstance != "" {
			return trimmedServerInstance
		}
		if trimmedBaseURL != "" {
			return trimmedBaseURL
		}
		return ""
	}
	for _, endpoint := range endpoints {
		if trimmedEndpointName != "" && strings.EqualFold(endpoint.Name, trimmedEndpointName) {
			return endpoint.Name
		}
		if trimmedBaseURL != "" && strings.TrimSpace(endpoint.BaseURL) == trimmedBaseURL {
			return endpoint.Name
		}
		if trimmedServerInstance != "" && strings.TrimSpace(endpoint.ServerInstance) == trimmedServerInstance {
			return endpoint.Name
		}
	}
	if len(endpoints) == 1 {
		return endpoints[0].Name
	}
	if trimmedEndpointName != "" {
		return trimmedEndpointName
	}
	if trimmedServerInstance != "" {
		return trimmedServerInstance
	}
	if trimmedBaseURL != "" {
		return trimmedBaseURL
	}
	return ""
}

// SnapshotCatalogIDByModel maps served model IDs to provider-recovered catalog
// identities, omitting identity mappings.
func SnapshotCatalogIDByModel(rows []modelsnapshot.KnownModel) map[string]string {
	var out map[string]string
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		catalogID := strings.TrimSpace(row.CatalogID)
		if id == "" || catalogID == "" || catalogID == id {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(rows))
		}
		out[id] = catalogID
	}
	return out
}

// SnapshotModelIDs returns unique, sorted served model IDs.
func SnapshotModelIDs(rows []modelsnapshot.KnownModel) []string {
	if len(rows) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SnapshotProviderContextWindows projects the current context-window evidence
// chain for every configured/discovered model.
func SnapshotProviderContextWindows(pcfg ProviderEntry, cat *modelcatalog.Catalog, rows []modelsnapshot.KnownModel, discoveredIDs []string) (map[string]int, map[string]string) {
	out := make(map[string]int)
	sources := make(map[string]string)
	rowByID := make(map[string]modelsnapshot.KnownModel, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		if _, exists := rowByID[id]; !exists {
			rowByID[id] = row
		}
	}
	add := func(modelID string, snapshotWindow int, snapshotSource string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return
		}
		window, source := SnapshotContextWindow(pcfg, cat, modelID, snapshotWindow, snapshotSource)
		if window <= 0 {
			return
		}
		out[modelID] = window
		sources[modelID] = source
	}
	if defaultModel := strings.TrimSpace(pcfg.Model); defaultModel != "" {
		row, ok := rowByID[defaultModel]
		if ok {
			add(defaultModel, row.ContextWindow, row.ContextWindowSource)
		} else {
			add(defaultModel, 0, "")
		}
	}
	for _, id := range discoveredIDs {
		row, ok := rowByID[id]
		if ok {
			add(id, row.ContextWindow, row.ContextWindowSource)
			continue
		}
		add(id, 0, "")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, sources
}

// SnapshotContextWindow preserves only observed routing evidence. Unknown
// capacity stays raw until post-selection execution fallback resolves it.
func SnapshotContextWindow(pcfg ProviderEntry, cat *modelcatalog.Catalog, modelID string, snapshotWindow int, snapshotSource string) (int, string) {
	if pcfg.ContextWindow > 0 {
		return pcfg.ContextWindow, routing.ContextSourceProviderConfig
	}
	if snapshotWindow > 0 {
		if strings.TrimSpace(snapshotSource) == routing.ContextSourceProviderAPI {
			return snapshotWindow, routing.ContextSourceProviderAPI
		}
		// Empty sources come from legacy cache entries written before evidence
		// provenance was persisted. Those values were catalog-enriched.
		return snapshotWindow, routing.ContextSourceCatalog
	}
	if cat != nil {
		if n := cat.ContextWindowForModel(modelID); n > 0 {
			return n, routing.ContextSourceCatalog
		}
	}
	return 0, routing.ContextSourceUnknown
}

// RoutingCatalogResolver resolves a model reference for a routing surface.
func RoutingCatalogResolver(cat *modelcatalog.Catalog) func(ref, surface string) (string, bool) {
	if cat == nil {
		return nil
	}
	return func(ref, surface string) (string, bool) {
		catalogSurface, ok := RoutingCatalogSurface(surface)
		if !ok {
			return "", false
		}
		resolved, err := cat.Resolve(ref, modelcatalog.ResolveOptions{
			Surface:         catalogSurface,
			AllowDeprecated: true,
		})
		if err != nil || resolved.ConcreteModel == "" {
			return "", false
		}
		return resolved.ConcreteModel, true
	}
}

// RoutingCatalogCandidatesResolver resolves all concrete candidates for a
// model reference on a routing surface.
func RoutingCatalogCandidatesResolver(cat *modelcatalog.Catalog) func(ref, surface string) ([]string, bool) {
	if cat == nil {
		return nil
	}
	return func(ref, surface string) ([]string, bool) {
		catalogSurface, ok := RoutingCatalogSurface(surface)
		if !ok {
			return nil, false
		}
		resolved, err := cat.Resolve(ref, modelcatalog.ResolveOptions{
			Surface:         catalogSurface,
			AllowDeprecated: true,
		})
		if err != nil || resolved.CanonicalID == "" {
			return nil, false
		}
		candidates := cat.CandidatesFor(catalogSurface, resolved.CanonicalID)
		if len(candidates) == 0 {
			if resolved.ConcreteModel == "" {
				return nil, false
			}
			return []string{resolved.ConcreteModel}, true
		}
		return candidates, true
	}
}

// RoutingModelEligibility builds the routing engine's cache-local eligibility
// lookup from the concrete harness/provider inventory.
func RoutingModelEligibility(entries []routing.HarnessEntry, cat *modelcatalog.Catalog) func(model string) (routing.ModelEligibility, bool) {
	if cat == nil {
		return nil
	}
	eligibility := make(map[string]routing.ModelEligibility)
	mergeInto := func(key string, src routing.ModelEligibility) {
		if existing, ok := eligibility[key]; ok {
			if src.Power > existing.Power {
				existing.Power = src.Power
			}
			existing.ExactPinOnly = existing.ExactPinOnly || src.ExactPinOnly
			existing.AutoRoutable = existing.AutoRoutable || src.AutoRoutable
			eligibility[key] = existing
			return
		}
		eligibility[key] = src
	}
	addShortAlias := func(modelID string, known routing.ModelEligibility) {
		parsed := modelcatalog.Parse(modelID)
		if parsed.Tier == modelcatalog.TierUnknown || parsed.Family == "" {
			return
		}
		tiers, ok := modelcatalog.FamilyTiers[parsed.Family]
		if !ok {
			return
		}
		for suffix, tier := range tiers {
			if tier == parsed.Tier && suffix != "" {
				mergeInto(suffix, known)
				break
			}
		}
	}
	add := func(modelID string, includeByDefault bool, status string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return
		}
		view := modeleligibility.Resolve(modelID, includeByDefault, status, cat)
		known := routing.ModelEligibility{
			Power:        view.Power,
			ExactPinOnly: view.ExactPinOnly,
			AutoRoutable: view.AutoRoutable,
		}
		mergeInto(modelID, known)
		addShortAlias(modelID, eligibility[modelID])
	}
	for _, h := range entries {
		status := "available"
		if !h.Available {
			status = "unreachable"
		}
		if h.DefaultModel != "" {
			add(h.DefaultModel, true, status)
		}
		for _, modelID := range h.SupportedModels {
			add(modelID, true, status)
		}
		for _, p := range h.Providers {
			includeByDefault := !p.ExcludeFromDefaultRouting
			add(p.DefaultModel, includeByDefault, status)
			for _, modelID := range p.DiscoveredIDs {
				add(modelID, includeByDefault, status)
			}
			for _, catalogID := range p.CatalogIDByModel {
				add(catalogID, includeByDefault, status)
			}
		}
	}
	if len(eligibility) == 0 {
		return nil
	}
	return func(model string) (routing.ModelEligibility, bool) {
		known, ok := eligibility[strings.TrimSpace(model)]
		return known, ok
	}
}

// RoutingReasoningResolver returns the catalog's surface-policy reasoning
// default for the requested policy and harness surface.
func RoutingReasoningResolver(cat *modelcatalog.Catalog) func(policy, surface string) (string, bool) {
	if cat == nil {
		return nil
	}
	return func(policy, surface string) (string, bool) {
		if policy == "" {
			return "", false
		}
		catalogSurface, ok := RoutingCatalogSurface(surface)
		if !ok {
			return "", false
		}
		resolved, err := cat.Resolve(policy, modelcatalog.ResolveOptions{
			Surface:         catalogSurface,
			AllowDeprecated: true,
		})
		if err != nil {
			return "", false
		}
		def := string(resolved.SurfacePolicy.ReasoningDefault)
		if def == "" {
			return "", false
		}
		return def, true
	}
}

// CatalogTierModelsForHarnessSurface returns active concrete model IDs sorted
// by descending catalog power, then stable ID.
func CatalogTierModelsForHarnessSurface(cat *modelcatalog.Catalog, harnessSurface string) []string {
	if cat == nil {
		return nil
	}
	catalogSurface, ok := RoutingCatalogSurface(harnessSurface)
	if !ok {
		return nil
	}
	concrete := cat.AllConcreteModels(catalogSurface)
	if len(concrete) == 0 {
		return nil
	}
	ids := make([]string, 0, len(concrete))
	for id := range concrete {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		pi, _, _ := CatalogPowerEligibility(cat, ids[i])
		pj, _, _ := CatalogPowerEligibility(cat, ids[j])
		if pi != pj {
			return pi > pj
		}
		return ids[i] < ids[j]
	})
	return ids
}

// RoutingCatalogSurface maps service harness surface names to catalog names.
func RoutingCatalogSurface(surface string) (modelcatalog.Surface, bool) {
	switch surface {
	case "embedded-openai":
		return modelcatalog.SurfaceAgentOpenAI, true
	case "embedded-anthropic":
		return modelcatalog.SurfaceAgentAnthropic, true
	case "codex":
		return modelcatalog.SurfaceCodex, true
	case "claude":
		return modelcatalog.SurfaceClaudeCode, true
	case "gemini":
		return modelcatalog.SurfaceGemini, true
	case "grok":
		return modelcatalog.SurfaceGrok, true
	default:
		return "", false
	}
}

// BuildProviderContextWindows projects available context evidence for a
// subscription or provider routing inventory. Unknown capacity is omitted.
func BuildProviderContextWindows(ctx context.Context, pcfg ProviderEntry, cat *modelcatalog.Catalog, discoveredIDs []string) (map[string]int, map[string]string) {
	out := make(map[string]int)
	sources := make(map[string]string)
	if defaultModel := strings.TrimSpace(pcfg.Model); defaultModel != "" {
		if length, source := resolveRoutingContextEvidence(ctx, pcfg, defaultModel, cat); length > 0 {
			out[defaultModel] = length
			sources[defaultModel] = source
		}
	}
	for _, id := range discoveredIDs {
		if id == "" {
			continue
		}
		if _, exists := out[id]; exists {
			continue
		}
		if length, source := resolveRoutingContextEvidence(ctx, pcfg, id, cat); length > 0 {
			out[id] = length
			sources[id] = source
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, sources
}

func resolveRoutingContextEvidence(ctx context.Context, entry ProviderEntry, modelID string, cat *modelcatalog.Catalog) (int, string) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return 0, routing.ContextSourceUnknown
	}
	if entry.ContextWindow > 0 {
		return entry.ContextWindow, routing.ContextSourceProviderConfig
	}
	if limits, source := providerAPIContextEvidence(ctx, entry, modelID); limits > 0 {
		return limits, source
	}
	if cat != nil {
		if n := cat.ContextWindowForModel(modelID); n > 0 {
			return n, routing.ContextSourceCatalog
		}
	}
	return 0, routing.ContextSourceUnknown
}

// ContextWindowSourceForProviderConfig returns the source label for an
// explicit configured context override.
func ContextWindowSourceForProviderConfig(pcfg ProviderEntry) string {
	if pcfg.ContextWindow > 0 {
		return routing.ContextSourceProviderConfig
	}
	return ""
}

// ProviderSupportsTools returns false only when the catalog explicitly marks
// every relevant model as not supporting tools.
func ProviderSupportsTools(cat *modelcatalog.Catalog, defaultModel string, discoveredIDs []string) bool {
	if cat == nil {
		return true
	}
	checked := false
	if defaultModel != "" {
		if cat.SupportsToolsForModel(defaultModel) {
			return true
		}
		checked = true
	}
	for _, id := range discoveredIDs {
		if id == "" {
			continue
		}
		if cat.SupportsToolsForModel(id) {
			return true
		}
		checked = true
	}
	return !checked
}

// ModelSupportsToolsByID returns per-model tool capability evidence.
func ModelSupportsToolsByID(cat *modelcatalog.Catalog, modelIDs []string) map[string]bool {
	if len(modelIDs) == 0 {
		return nil
	}
	support := make(map[string]bool, len(modelIDs))
	for _, id := range modelIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if cat == nil {
			support[id] = true
			continue
		}
		support[id] = cat.SupportsToolsForModel(id)
	}
	if len(support) == 0 {
		return nil
	}
	return support
}

// AnyProviderSupportsTools reports whether at least one provider candidate
// advertises tool support.
func AnyProviderSupportsTools(providers []routing.ProviderEntry) bool {
	for _, provider := range providers {
		if provider.SupportsTools {
			return true
		}
	}
	return false
}

// ApplyEndpointRoutingCost attaches fixed/local or metered catalog cost
// evidence to one provider entry.
func ApplyEndpointRoutingCost(entry *routing.ProviderEntry, pcfg ProviderEntry, cat *modelcatalog.Catalog, localCostUSDPer1kTokens float64) {
	if entry == nil {
		return
	}
	if ProviderTypeUsesFixedBilling(pcfg.Type) {
		entry.ActualCashSpend = false
		if localCostUSDPer1kTokens > 0 {
			entry.CostUSDPer1kTokens = localCostUSDPer1kTokens
			entry.CostSource = routing.CostSourceUserConfig
		} else {
			entry.CostUSDPer1kTokens = 0
			entry.CostSource = routing.CostSourceUnknown
		}
		return
	}
	if cost, ok := CatalogCostUSDPer1kTokens(cat, entry.DefaultModel); ok {
		entry.ActualCashSpend = true
		entry.CostUSDPer1kTokens = cost
		entry.CostSource = routing.CostSourceCatalog
		return
	}
	entry.ActualCashSpend = true
	entry.CostUSDPer1kTokens = 0
	entry.CostSource = routing.CostSourceUnknown
}

// ApplySubscriptionRoutingCost attaches PAYG-equivalent shadow cost and
// per-model capability evidence to a subscription harness. Quota utilization
// does not modify the shadow cost; it remains a separate routing signal.
func ApplySubscriptionRoutingCost(entry *routing.HarnessEntry, cat *modelcatalog.Catalog) {
	if entry == nil || !entry.IsSubscription {
		return
	}
	baseCost, ok := CatalogCostUSDPer1kTokens(cat, entry.DefaultModel)
	if !ok {
		baseCost, ok = CatalogCostUSDPer1kTokens(cat, SubscriptionFallbackPolicy(entry.Name))
		if !ok {
			baseCost = 0
		}
	}
	ctxWindows, ctxSources := BuildProviderContextWindows(context.Background(), ProviderEntry{}, cat, entry.AutoRoutingModels)
	modelTools := ModelSupportsToolsByID(cat, entry.AutoRoutingModels)
	supportsTools := ProviderSupportsTools(cat, entry.DefaultModel, entry.AutoRoutingModels)
	costByModel := SubscriptionCostByModel(cat, entry.AutoRoutingModels)
	entry.Providers = []routing.ProviderEntry{{
		Billing:                   modelcatalog.BillingModelSubscription,
		CostUSDPer1kTokens:        baseCost,
		CostUSDPer1kTokensByModel: costByModel,
		CostSource:                routing.CostSourceSubscription,
		ActualCashSpend:           false,
		ContextWindows:            ctxWindows,
		ContextWindowSources:      ctxSources,
		SupportsTools:             supportsTools,
		SupportsToolsByModel:      modelTools,
	}}
}

// SubscriptionCostByModel returns catalog shadow cost per subscription tier.
func SubscriptionCostByModel(cat *modelcatalog.Catalog, modelIDs []string) map[string]float64 {
	if cat == nil || len(modelIDs) == 0 {
		return nil
	}
	out := make(map[string]float64, len(modelIDs))
	for _, id := range modelIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if cost, ok := CatalogCostUSDPer1kTokens(cat, id); ok {
			out[id] = cost
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ProviderRoutingCostClass returns the legacy coarse cost class for a native
// provider type.
func ProviderRoutingCostClass(providerType string) string {
	if ProviderTypeUsesFixedBilling(providerType) {
		return "local"
	}
	return "medium"
}

// SubscriptionFallbackPolicy returns the catalog policy used when a
// subscription harness's default model has no catalog price.
func SubscriptionFallbackPolicy(harnessName string) string {
	switch harnessName {
	case "claude", "codex", "gemini", "grok":
		return "default"
	default:
		return ""
	}
}

// CatalogCostUSDPer1kTokens resolves a model or surface ID and returns the
// blended catalog price per 1,000 tokens.
func CatalogCostUSDPer1kTokens(cat *modelcatalog.Catalog, modelID string) (float64, bool) {
	if cat == nil || strings.TrimSpace(modelID) == "" {
		return 0, false
	}
	entry, ok := cat.LookupModel(modelID)
	if !ok {
		resolved := ResolveCatalogCostModel(cat, modelID)
		if resolved == "" {
			return 0, false
		}
		entry, ok = cat.LookupModel(resolved)
		if !ok {
			return 0, false
		}
	}
	input := entry.CostInputPerM
	if input == 0 {
		input = entry.CostInputPerMTok
	}
	output := entry.CostOutputPerM
	if output == 0 {
		output = entry.CostOutputPerMTok
	}
	switch {
	case input > 0 && output > 0:
		return ((input + output) / 2) / 1000, true
	case input > 0:
		return input / 1000, true
	case output > 0:
		return output / 1000, true
	default:
		return 0, false
	}
}

// ResolveCatalogCostModel resolves a cost reference across every supported
// catalog surface in the legacy precedence order.
func ResolveCatalogCostModel(cat *modelcatalog.Catalog, ref string) string {
	for _, surface := range []modelcatalog.Surface{
		modelcatalog.SurfaceAgentOpenAI,
		modelcatalog.SurfaceAgentAnthropic,
		modelcatalog.SurfaceCodex,
		modelcatalog.SurfaceClaudeCode,
		modelcatalog.SurfaceGemini,
	} {
		resolved, err := cat.Resolve(ref, modelcatalog.ResolveOptions{
			Surface:         surface,
			AllowDeprecated: true,
		})
		if err == nil && resolved.ConcreteModel != "" {
			return resolved.ConcreteModel
		}
	}
	return ""
}

// SubscriptionCostCurve preserves the legacy quota-utilization curve shape.
// Active routing uses flat PAYG-equivalent subscription shadow cost; this type
// remains for compatibility with the root option and historical tuning.
type SubscriptionCostCurve struct {
	FreeUntilPercent   int
	LowUntilPercent    int
	MediumUntilPercent int
	LowMultiplier      float64
	MediumMultiplier   float64
	HighMultiplier     float64
}

// NormalizeSubscriptionCostCurve fills zero fields from the legacy defaults.
func NormalizeSubscriptionCostCurve(curve *SubscriptionCostCurve) SubscriptionCostCurve {
	if curve == nil {
		return DefaultSubscriptionCostCurve()
	}
	out := *curve
	def := DefaultSubscriptionCostCurve()
	if out.FreeUntilPercent == 0 {
		out.FreeUntilPercent = def.FreeUntilPercent
	}
	if out.LowUntilPercent == 0 {
		out.LowUntilPercent = def.LowUntilPercent
	}
	if out.MediumUntilPercent == 0 {
		out.MediumUntilPercent = def.MediumUntilPercent
	}
	if out.LowMultiplier == 0 {
		out.LowMultiplier = def.LowMultiplier
	}
	if out.MediumMultiplier == 0 {
		out.MediumMultiplier = def.MediumMultiplier
	}
	if out.HighMultiplier == 0 {
		out.HighMultiplier = def.HighMultiplier
	}
	return out
}

// DefaultSubscriptionCostCurve returns the legacy utilization thresholds and
// multipliers.
func DefaultSubscriptionCostCurve() SubscriptionCostCurve {
	return SubscriptionCostCurve{
		FreeUntilPercent:   70,
		LowUntilPercent:    80,
		MediumUntilPercent: 90,
		LowMultiplier:      0.1,
		MediumMultiplier:   0.3,
		HighMultiplier:     1.2,
	}
}

// SubscriptionEffectiveCostUSDPer1kTokens evaluates the legacy curve. Active
// subscription routing deliberately does not call this function.
func SubscriptionEffectiveCostUSDPer1kTokens(baseCost float64, quotaPercentUsed int, curve SubscriptionCostCurve) float64 {
	switch {
	case quotaPercentUsed <= curve.FreeUntilPercent:
		return 0
	case quotaPercentUsed <= curve.LowUntilPercent:
		return baseCost * curve.LowMultiplier
	case quotaPercentUsed <= curve.MediumUntilPercent:
		return baseCost * curve.MediumMultiplier
	default:
		return baseCost * curve.HighMultiplier
	}
}
