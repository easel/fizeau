package modelregistry

import (
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
)

// Source records how a model ID was discovered.
type Source string

const (
	SourceNativeAPI  Source = "native_api"
	SourceHarnessPTY Source = "harness_pty"
	SourcePropsAPI   Source = "props_api"
)

// ModelStatus records the provider runtime state relevant to routing.
type ModelStatus string

const (
	StatusAvailable   ModelStatus = "available"
	StatusRateLimited ModelStatus = "rate_limited"
	StatusUnreachable ModelStatus = "unreachable"
	StatusUnknown     ModelStatus = "unknown"
)

// KnownModel is one discovered provider/model identity enriched with catalog
// metadata when the catalog knows the model.
type KnownModel struct {
	Provider     string `json:"provider,omitempty"`
	ProviderType string `json:"provider_type,omitempty"`
	Harness      string `json:"harness,omitempty"`
	ID           string `json:"model,omitempty"`
	// CatalogID, when set, is the catalog-resolution identity recovered from a
	// provider's /props endpoint (e.g. "Qwen3.6-27B" behind the served alias
	// "dflash"). Power/family/cost enrichment resolves against CatalogID while ID
	// stays the wire model the server is actually invoked with. Empty => use ID.
	CatalogID  string `json:"catalog_id,omitempty"`
	Configured bool   `json:"configured,omitempty"`

	EndpointName     string                    `json:"endpoint_name,omitempty"`
	EndpointBaseURL  string                    `json:"endpoint_base_url,omitempty"`
	ServerInstance   string                    `json:"server_instance,omitempty"`
	Billing          modelcatalog.BillingModel `json:"billing,omitempty"`
	IncludeByDefault bool                      `json:"include_by_default,omitempty"`

	DiscoveredVia Source    `json:"model_discovery_freshness_source,omitempty"`
	DiscoveredAt  time.Time `json:"model_discovery_freshness_at,omitempty"`

	Family     string            `json:"family,omitempty"`
	Version    []int             `json:"version,omitempty"`
	Tier       modelcatalog.Tier `json:"tier,omitempty"`
	PreRelease bool              `json:"pre_release,omitempty"`

	Power                     int           `json:"power,omitempty"`
	CostInputPerM             float64       `json:"cost_input_per_m,omitempty"`
	CostOutputPerM            float64       `json:"cost_output_per_m,omitempty"`
	ContextWindow             int           `json:"context_window,omitempty"`
	ContextWindowSource       string        `json:"context_window_source,omitempty"`
	MaxCompletionTokens       int           `json:"max_completion_tokens,omitempty"`
	MaxCompletionTokensSource string        `json:"max_completion_tokens_source,omitempty"`
	ReasoningLevels           []string      `json:"reasoning_levels,omitempty"`
	QuotaPool                 string        `json:"quota_pool,omitempty"`
	QuotaRemaining            *int          `json:"quota_remaining,omitempty"`
	RecentP50Latency          time.Duration `json:"recent_p50_latency_ns,omitempty"`

	Status                ModelStatus `json:"status,omitempty"`
	HealthFreshnessAt     time.Time   `json:"health_freshness_at,omitempty"`
	HealthFreshnessSource string      `json:"health_freshness_source,omitempty"`
	QuotaFreshnessAt      time.Time   `json:"quota_freshness_at,omitempty"`
	QuotaFreshnessSource  string      `json:"quota_freshness_source,omitempty"`
	ActualCashSpend       bool        `json:"actual_cash_spend"`
	EffectiveCost         float64     `json:"effective_cost"`
	EffectiveCostSource   string      `json:"effective_cost_source,omitempty"`
	SupportsTools         bool        `json:"supports_tools,omitempty"`
	DeploymentClass       string      `json:"deployment_class,omitempty"`

	AutoRoutable    bool   `json:"auto_routable,omitempty"`
	ExactPinOnly    bool   `json:"exact_pin_only,omitempty"`
	ExclusionReason string `json:"exclusion_reason,omitempty"`
}

// ModelSnapshot is the in-memory model registry view assembled from discovery
// cache entries and catalog metadata.
type ModelSnapshot struct {
	Models  []KnownModel          `json:"models,omitempty"`
	AsOf    time.Time             `json:"as_of,omitempty"`
	Sources map[string]SourceMeta `json:"sources,omitempty"`
}

// SourceMeta summarizes cache state for a discovery source.
type SourceMeta struct {
	LastRefreshedAt time.Time `json:"last_refreshed_at,omitempty"`
	Stale           bool      `json:"stale,omitempty"`
	Error           string    `json:"error,omitempty"`
}
