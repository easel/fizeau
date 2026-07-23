package modelregistry

import (
	"context"
	"os/exec"
	"sort"
	"time"

	"github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modeleligibility"
)

// RefreshMode controls whether Assemble refreshes stale source cache data.
type RefreshMode int

const (
	// RefreshBackground preserves the default stale-while-revalidate behavior:
	// return cached data immediately and refresh stale sources in the background.
	RefreshBackground RefreshMode = iota
	// RefreshNone returns cached data only.
	RefreshNone
	// RefreshForce refreshes sources synchronously before reading them.
	RefreshForce
	// RefreshIfStale refreshes ONLY stale sources synchronously; fresh sources
	// short-circuit on the cached entry. Reserve this for explicit refresh or
	// preflight surfaces; request routing uses RefreshBackground so stale
	// providers cannot block candidate scoring.
	RefreshIfStale
)

// AssembleOptions controls snapshot assembly behavior.
type AssembleOptions struct {
	Refresh RefreshMode
}

// Assemble builds a ModelSnapshot from configured providers, cache-backed
// discovery, runtime status cache state, and model catalog metadata.
func Assemble(ctx context.Context, cfg *config.Config, cat *modelcatalog.Catalog, cache *discoverycache.Cache) (ModelSnapshot, error) {
	return AssembleWithOptions(ctx, cfg, cat, cache, AssembleOptions{Refresh: RefreshBackground})
}

// AssembleWithOptions builds a ModelSnapshot with explicit cache refresh
// behavior for CLI and routing consumers.
func AssembleWithOptions(ctx context.Context, cfg *config.Config, cat *modelcatalog.Catalog, cache *discoverycache.Cache, opts AssembleOptions) (ModelSnapshot, error) {
	asOf := nowUTC()
	snapshot := ModelSnapshot{
		AsOf:    asOf,
		Sources: map[string]SourceMeta{},
	}
	if cfg == nil {
		return snapshot, nil
	}

	providers := enumerateProviders(cfg)
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, providerName := range names {
		pcfg := providers[providerName]
		discovered := discoverProvider(ctx, providerName, pcfg, cache, opts)
		for source, meta := range discovered.Sources {
			snapshot.Sources[source] = meta
		}
		includeByDefault := effectiveProviderIncludeByDefault(providerName, pcfg, cat)
		billing := effectiveProviderBilling(providerName, pcfg, cat)
		status := providerStatus(cache, providerName)
		reconciled := reconcilePropsModels(discovered.Models, includeByDefault, string(status), cat)
		for _, discoveredModel := range reconciled {
			model := KnownModel{
				Provider:                  providerName,
				ProviderType:              discoveredModel.ProviderType,
				Harness:                   discoveredModel.Harness,
				ID:                        discoveredModel.ID,
				CatalogID:                 discoveredModel.CatalogID,
				Configured:                discoveredModel.Configured,
				EndpointName:              discoveredModel.EndpointName,
				EndpointBaseURL:           discoveredModel.EndpointBaseURL,
				ServerInstance:            discoveredModel.ServerInstance,
				Billing:                   billing,
				IncludeByDefault:          includeByDefault,
				DiscoveredVia:             discoveredModel.Via,
				DiscoveredAt:              discoveredModel.DiscoveredAt,
				ContextWindow:             discoveredModel.ContextWindow,
				ContextWindowSource:       discoveredModel.ContextWindowSource,
				MaxCompletionTokens:       discoveredModel.MaxCompletionTokens,
				MaxCompletionTokensSource: discoveredModel.MaxCompletionTokensSource,
				Status:                    status,
			}
			model = enrichModel(model, includeByDefault, cat)
			model = attachRuntimeSignals(model, cache)
			snapshot.Models = append(snapshot.Models, model)
		}
	}
	sort.Slice(snapshot.Models, func(i, j int) bool {
		if snapshot.Models[i].Provider != snapshot.Models[j].Provider {
			return snapshot.Models[i].Provider < snapshot.Models[j].Provider
		}
		if snapshot.Models[i].ProviderType != snapshot.Models[j].ProviderType {
			return snapshot.Models[i].ProviderType < snapshot.Models[j].ProviderType
		}
		if snapshot.Models[i].Harness != snapshot.Models[j].Harness {
			return snapshot.Models[i].Harness < snapshot.Models[j].Harness
		}
		if snapshot.Models[i].EndpointName != snapshot.Models[j].EndpointName {
			return snapshot.Models[i].EndpointName < snapshot.Models[j].EndpointName
		}
		if snapshot.Models[i].EndpointBaseURL != snapshot.Models[j].EndpointBaseURL {
			return snapshot.Models[i].EndpointBaseURL < snapshot.Models[j].EndpointBaseURL
		}
		if snapshot.Models[i].ServerInstance != snapshot.Models[j].ServerInstance {
			return snapshot.Models[i].ServerInstance < snapshot.Models[j].ServerInstance
		}
		return snapshot.Models[i].ID < snapshot.Models[j].ID
	})
	return snapshot, nil
}

// ActiveSources returns the cache sources that are currently configured and
// should be preserved by cache garbage collection.
func ActiveSources(cfg *config.Config) []discoverycache.Source {
	if cfg == nil {
		return nil
	}
	providers := enumerateProviders(cfg)
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)

	sources := make([]discoverycache.Source, 0, len(names)*2)
	for _, providerName := range names {
		pcfg := providers[providerName]
		switch normalizeProviderType(pcfg.Type) {
		case "claude", "codex", "grok":
			sources = append(sources, discoverycache.Source{
				Tier:            "discovery",
				Name:            providerName,
				TTL:             discoveryTTLPTY,
				RefreshDeadline: discoveryRefreshDeadlinePTY,
			})
		case "openai", "openrouter", "vidar-ds4", "sindri-llamacpp", "ds4", "lucebox", "lmstudio", "llama-server", "omlx", "rapid-mlx", "vllm", "ollama", "minimax", "qwen", "zai":
			for _, endpoint := range discoveryEndpoints(providerName, pcfg) {
				sources = append(sources, discoverySource(endpointSourceName(providerName, endpoint.Name, endpoint.BaseURL, endpoint.ServerInstance), discoveryTTLForProvider(pcfg), discoveryRefreshDeadlineHTTP))
			}
			if hasPropsDiscovery(pcfg.Type) {
				sources = append(sources, discoverySource(providerName+"-props", discoveryTTLHTTPLocal, discoveryRefreshDeadlineHTTP))
			}
		default:
			if pcfg.Model != "" {
				sources = append(sources, discoverySource(providerName, discoveryTTLHTTPRemote, discoveryRefreshDeadlineHTTP))
			}
		}
		sources = append(sources, discoverycache.Source{
			Tier:            "runtime",
			Name:            providerName,
			TTL:             5 * time.Minute,
			RefreshDeadline: 5 * time.Second,
		})
	}
	return sources
}

// reconcilePropsModels collapses /props-recovered identities into the served
// models for a provider. /props discovery exists to recover the real catalog
// identity behind a generic served alias (e.g. "dflash" -> "Qwen3.6-27B"); those
// recovered names are not separately-served models, so they must not become their
// own route candidates. For each served model whose own ID has no catalog power,
// attach the best catalog-matchable /props-recovered name as its CatalogID so
// enrichment resolves power/family from it while the wire ID stays the served
// alias. The standalone /props entries are then dropped. If a provider has only
// /props models (its /v1/models returned nothing), the best /props identity is
// promoted to the served model so the provider stays routable.
//
// Scoped per provider (these speculative-decoding servers are single-endpoint);
// multi-endpoint /props providers are not a current configuration.
func reconcilePropsModels(models []discoveredModel, includeByDefault bool, status string, cat *modelcatalog.Catalog) []discoveredModel {
	bestCatalogID := ""
	bestPower := 0
	hasProps := false
	var propsLimits discoveredModel
	for _, m := range models {
		if m.Via != SourcePropsAPI {
			continue
		}
		hasProps = true
		mergeMissingLimitEvidence(&propsLimits, m)
		if pw := modeleligibility.Resolve(m.ID, includeByDefault, status, cat).Power; pw > bestPower {
			bestPower = pw
			bestCatalogID = m.ID
		}
	}
	if !hasProps {
		return models
	}

	out := make([]discoveredModel, 0, len(models))
	servedCount := 0
	for _, m := range models {
		if m.Via == SourcePropsAPI {
			continue // /props identities are catalog hints, not separately-served models
		}
		servedCount++
		mergeMissingLimitEvidence(&m, propsLimits)
		if bestCatalogID != "" && modeleligibility.Resolve(m.ID, includeByDefault, status, cat).Power <= 0 {
			m.CatalogID = bestCatalogID
		}
		out = append(out, m)
	}
	if servedCount == 0 && bestCatalogID != "" {
		for _, m := range models {
			if m.Via == SourcePropsAPI && m.ID == bestCatalogID {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func enumerateProviders(cfg *config.Config) map[string]config.ProviderConfig {
	out := make(map[string]config.ProviderConfig, len(cfg.Providers)+2)
	for name, pc := range cfg.Providers {
		out[name] = pc
	}
	addImplicitHarness(out, "claude-subscription", "claude", "claude")
	addImplicitHarness(out, "codex-subscription", "codex", "codex")
	return out
}

func addImplicitHarness(providers map[string]config.ProviderConfig, name, providerType, binary string) {
	if _, exists := providers[name]; exists {
		return
	}
	if _, err := exec.LookPath(binary); err != nil {
		return
	}
	include := true
	providers[name] = config.ProviderConfig{
		Type:             providerType,
		IncludeByDefault: &include,
		Billing:          string(modelcatalog.BillingModelSubscription),
	}
}
