package serviceimpl

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/provider/lmstudio"
	"github.com/easel/fizeau/internal/provider/omlx"
	"github.com/easel/fizeau/internal/provider/openrouter"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serverinstance"
)

type ProviderEndpoint struct {
	Name           string
	BaseURL        string
	ServerInstance string
}

type ProviderEntry struct {
	Type                      string
	BaseURL                   string
	ServerInstance            string
	Endpoints                 []ProviderEndpoint
	APIKey                    string
	Headers                   map[string]string
	Model                     string
	Billing                   modelcatalog.BillingModel
	IncludeByDefault          bool
	IncludeByDefaultSet       bool
	ContextWindow             int
	ConfigError               string
	DailyTokenBudget          int
	CreditBalanceThresholdUSD float64
	CreditProbeTTL            time.Duration
}

type ModelDiscoveryEndpoint struct {
	Name           string
	BaseURL        string
	ServerInstance string
}

type CostInfo struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

type PerfSignal struct {
	SpeedTokensPerSec float64
	SWEBenchVerified  float64
}

type PolicyInfo struct {
	Name            string
	MinPower        int
	MaxPower        int
	AllowLocal      bool
	Require         []string
	CatalogVersion  string
	ManifestSource  string
	ManifestVersion int
}

func HarnessPaymentKind(name string, cfg harnesses.HarnessConfig) modelcatalog.BillingModel {
	if name == "" {
		name = cfg.Name
	}
	if billing := modelcatalog.BillingForHarness(name); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	if billing := modelcatalog.BillingForProviderSystem(name); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	if cfg.IsSubscription {
		return modelcatalog.BillingModelSubscription
	}
	if cfg.IsLocal {
		return modelcatalog.BillingModelFixed
	}
	return modelcatalog.BillingModelUnknown
}

func HarnessRunsInProcessOrHTTP(cfg harnesses.HarnessConfig) bool {
	return cfg.IsHTTPProvider || cfg.IsLocal
}

func ServiceProviderBilling(entry ProviderEntry) modelcatalog.BillingModel {
	if entry.Billing != modelcatalog.BillingModelUnknown {
		return entry.Billing
	}
	if billing := modelcatalog.BillingForProviderSystem(entry.Type); billing != modelcatalog.BillingModelUnknown {
		return billing
	}
	return modelcatalog.BillingForHarness(entry.Type)
}

func ServiceProviderDefaultInclusion(entry ProviderEntry) bool {
	if entry.IncludeByDefaultSet {
		return entry.IncludeByDefault
	}
	switch ServiceProviderBilling(entry) {
	case modelcatalog.BillingModelFixed, modelcatalog.BillingModelSubscription:
		return true
	default:
		return false
	}
}

func ProviderTypeUsesFixedBilling(providerType string) bool {
	return modelcatalog.BillingForProviderSystem(providerType) == modelcatalog.BillingModelFixed
}

func SubprocessHarnessModelIDs(name string, cfg harnesses.HarnessConfig) []string {
	mdh, ok := builtin.New(name).(harnesses.ModelDiscoveryHarness)
	if !ok {
		return nil
	}
	payload, ok := cachedSubprocessDiscovery(name, mdh)
	if !ok {
		// Model discovery failed; return no models rather than a fallback.
		// This enforces the no-static-fallback principle.
		return nil
	}
	snapshot := payload.snapshot()
	var models []string
	models = AppendUniqueModelIDs(models, snapshot.Models...)
	for _, family := range mdh.SupportedAliases() {
		resolved, err := mdh.ResolveModelAlias(family, snapshot)
		if err == nil && resolved != family {
			models = AppendUniqueModelIDs(models, resolved)
		}
	}
	return models
}

func SubprocessHarnessAutoRoutingModels(name string, cfg harnesses.HarnessConfig) []string {
	models := make([]string, 0)
	if cfg.DefaultModel != "" {
		models = AppendUniqueModelIDs(models, ResolveSubprocessModelAlias(name, cfg.DefaultModel))
	}
	for _, id := range SubprocessHarnessModelIDs(name, cfg) {
		models = AppendUniqueModelIDs(models, ResolveSubprocessModelAlias(name, id))
	}
	return models
}

func ResolveSubprocessModelAlias(harness, model string) string {
	switch harness {
	case "claude":
		return ClaudeCLIExecutableModel(model)
	default:
		mdh, ok := builtin.New(harness).(harnesses.ModelDiscoveryHarness)
		if !ok {
			return model
		}
		payload, ok := cachedSubprocessDiscovery(harness, mdh)
		if !ok {
			// Model discovery failed; return the unresolved model.
			return model
		}
		resolved, err := mdh.ResolveModelAlias(model, payload.snapshot())
		if err != nil {
			return model
		}
		return resolved
	}
}

func ClaudeCLIExecutableModel(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case normalized == "sonnet" || strings.HasPrefix(normalized, "sonnet-") || strings.HasPrefix(normalized, "claude-sonnet-"):
		return "sonnet"
	case normalized == "opus" || strings.HasPrefix(normalized, "opus-") || strings.HasPrefix(normalized, "claude-opus-"):
		return "opus"
	case normalized == "haiku" || strings.HasPrefix(normalized, "haiku-") || strings.HasPrefix(normalized, "claude-haiku-"):
		return "haiku"
	default:
		return model
	}
}

func AppendUniqueModelIDs(values []string, additions ...string) []string {
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, existing := range values {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			values = append(values, value)
		}
	}
	return values
}

func ModelDiscoveryEndpoints(entry ProviderEntry) []ModelDiscoveryEndpoint {
	if len(entry.Endpoints) > 0 {
		out := make([]ModelDiscoveryEndpoint, 0, len(entry.Endpoints))
		for _, endpoint := range entry.Endpoints {
			if strings.TrimSpace(endpoint.BaseURL) == "" {
				continue
			}
			out = append(out, ModelDiscoveryEndpoint{
				Name:           EndpointDisplayName(endpoint.Name, endpoint.BaseURL),
				BaseURL:        endpoint.BaseURL,
				ServerInstance: serverinstance.Normalize(endpoint.BaseURL, endpoint.ServerInstance),
			})
		}
		return out
	}
	if strings.TrimSpace(entry.BaseURL) == "" {
		return nil
	}
	return []ModelDiscoveryEndpoint{{
		Name:           EndpointDisplayName("default", entry.BaseURL),
		BaseURL:        entry.BaseURL,
		ServerInstance: serverinstance.Normalize(entry.BaseURL, entry.ServerInstance),
	}}
}

func EndpointDisplayName(name, baseURL string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	u, err := url.Parse(baseURL)
	if err == nil && u.Host != "" {
		return u.Host
	}
	return "default"
}

func ResolveContextEvidence(ctx context.Context, entry ProviderEntry, modelID string, cat *modelcatalog.Catalog) (int, string) {
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
	return compaction.DefaultContextWindow, routing.ContextSourceDefault
}

func providerAPIContextEvidence(ctx context.Context, entry ProviderEntry, modelID string) (int, string) {
	switch entry.Type {
	case "lmstudio":
		if entry.BaseURL == "" {
			return 0, ""
		}
		if limits := lmstudio.LookupModelLimits(ctx, entry.BaseURL, modelID); limits.ContextLength > 0 {
			return limits.ContextLength, routing.ContextSourceProviderAPI
		}
	case "omlx":
		if entry.BaseURL == "" {
			return 0, ""
		}
		if limits := omlx.LookupModelLimits(ctx, entry.BaseURL, modelID); limits.ContextLength > 0 {
			return limits.ContextLength, routing.ContextSourceProviderAPI
		}
	case "openrouter":
		if entry.BaseURL == "" {
			return 0, ""
		}
		if limits := openrouter.LookupModelLimits(ctx, entry.BaseURL, entry.APIKey, entry.Headers, modelID); limits.ContextLength > 0 {
			return limits.ContextLength, routing.ContextSourceProviderAPI
		}
	}
	return 0, ""
}

func CatalogCostAndPerf(cat *modelcatalog.Catalog, modelID string) (CostInfo, PerfSignal) {
	entry, ok := cat.LookupModel(modelID)
	if !ok {
		return CostInfo{}, PerfSignal{}
	}
	return CostInfo{
		InputPerMTok:  catalogEntryInputCost(entry),
		OutputPerMTok: catalogEntryOutputCost(entry),
	}, PerfSignal{
		SpeedTokensPerSec: entry.SpeedTokensPerSec,
		SWEBenchVerified:  entry.SWEBenchVerified,
	}
}

func CatalogPowerEligibility(cat *modelcatalog.Catalog, modelID string) (int, bool, bool) {
	eligibility, ok := cat.ModelEligibility(modelID)
	if !ok {
		return 0, false, false
	}
	return eligibility.Power, eligibility.AutoRoutable, eligibility.ExactPinOnly
}

func CatalogPowerForModel(cat *modelcatalog.Catalog, modelID string) int {
	if cat == nil || modelID == "" {
		return 0
	}
	power, _, _ := CatalogPowerEligibility(cat, modelID)
	return power
}

func ProviderCapabilities(entry ProviderEntry) []string {
	switch NormalizeProviderType(entry.Type) {
	case "anthropic":
		return []string{"tool_use", "vision", "streaming"}
	case "omlx":
		return []string{"tool_use", "streaming", "json_mode", "reasoning_control"}
	default:
		return []string{"tool_use", "streaming", "json_mode"}
	}
}

func PolicyInfoFromCatalog(policy modelcatalog.Policy, meta modelcatalog.Metadata) PolicyInfo {
	return PolicyInfo{
		Name:            policy.Name,
		MinPower:        policy.MinPower,
		MaxPower:        policy.MaxPower,
		AllowLocal:      policy.AllowLocal,
		Require:         append([]string(nil), policy.Require...),
		CatalogVersion:  meta.CatalogVersion,
		ManifestSource:  meta.ManifestSource,
		ManifestVersion: meta.ManifestVersion,
	}
}

func NormalizeProviderType(providerType string) string {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "" {
		return "openai"
	}
	return providerType
}

func catalogEntryInputCost(entry modelcatalog.ModelEntry) float64 {
	if entry.CostInputPerM != 0 {
		return entry.CostInputPerM
	}
	return entry.CostInputPerMTok
}

func catalogEntryOutputCost(entry modelcatalog.ModelEntry) float64 {
	if entry.CostOutputPerM != 0 {
		return entry.CostOutputPerM
	}
	return entry.CostOutputPerMTok
}
