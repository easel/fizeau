package routehealth

import (
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/serverinstance"
)

// StatusRowsInput is the route-health evidence needed to assemble route-status
// rows without depending on root public API types.
type StatusRowsInput struct {
	Snapshot            modelsnapshot.ModelSnapshot
	ConfiguredProviders map[string]struct{}
	ActiveAttempts      []Record
	SuccessRate         map[string]float64
	LatencyMS           map[string]float64
	CooldownTTL         time.Duration
}

// StatusEntry is one internally assembled model route-status row.
type StatusEntry struct {
	Model      string
	Strategy   string
	Candidates []StatusCandidate
}

// StatusCandidate is the API-neutral route-health projection for one
// provider/model/server candidate.
type StatusCandidate struct {
	Provider                      string
	Endpoint                      string
	Model                         string
	ServerInstance                string
	Billing                       modelcatalog.BillingModel
	ActualCashSpend               bool
	EffectiveCost                 float64
	EffectiveCostSource           string
	Priority                      int
	Healthy                       bool
	Cooldown                      *Cooldown
	SourceStatus                  string
	AutoRoutable                  bool
	ExactPinOnly                  bool
	ExclusionReason               string
	Power                         int
	ContextLength                 int
	CostInputPerMTok              float64
	CostOutputPerMTok             float64
	RecentLatencyMS               float64
	ProviderReliabilityRate       float64
	QuotaRemaining                *int
	SnapshotCapturedAt            time.Time
	HealthFreshnessAt             time.Time
	HealthFreshnessSource         string
	QuotaFreshnessAt              time.Time
	QuotaFreshnessSource          string
	ModelDiscoveryFreshnessAt     time.Time
	ModelDiscoveryFreshnessSource string
}

// BuildStatusRows filters, orders, and projects one model snapshot into the
// deterministic route-health rows consumed by the root facade.
func BuildStatusRows(in StatusRowsInput) []StatusEntry {
	if len(in.Snapshot.Models) == 0 || len(in.ConfiguredProviders) == 0 {
		return nil
	}

	grouped := make(map[string][]modelsnapshot.KnownModel)
	for _, row := range in.Snapshot.Models {
		harness := strings.TrimSpace(row.Harness)
		if harness != "" && harness != "fiz" {
			continue
		}
		provider := strings.TrimSpace(row.Provider)
		if _, ok := in.ConfiguredProviders[provider]; !ok {
			continue
		}
		model := strings.TrimSpace(row.ID)
		if model == "" {
			continue
		}
		grouped[model] = append(grouped[model], row)
	}
	if len(grouped) == 0 {
		return nil
	}

	models := make([]string, 0, len(grouped))
	for model := range grouped {
		models = append(models, model)
	}
	sort.Strings(models)

	entries := make([]StatusEntry, 0, len(models))
	for _, model := range models {
		rows := grouped[model]
		sort.Slice(rows, func(i, j int) bool {
			return statusRowLess(rows[i], rows[j])
		})

		entry := StatusEntry{
			Model:      model,
			Strategy:   "auto",
			Candidates: make([]StatusCandidate, 0, len(rows)),
		}
		for i, row := range rows {
			provider := strings.TrimSpace(row.Provider)
			endpoint := strings.TrimSpace(row.EndpointName)
			server := strings.TrimSpace(serverinstance.Normalize(row.EndpointBaseURL, row.ServerInstance))
			candidate := StatusCandidate{
				Provider:                      provider,
				Endpoint:                      endpoint,
				Model:                         model,
				ServerInstance:                server,
				Billing:                       row.Billing,
				ActualCashSpend:               row.ActualCashSpend,
				EffectiveCost:                 row.EffectiveCost,
				EffectiveCostSource:           row.EffectiveCostSource,
				Priority:                      len(rows) - i,
				Healthy:                       true,
				SourceStatus:                  string(row.Status),
				AutoRoutable:                  row.AutoRoutable,
				ExactPinOnly:                  row.ExactPinOnly,
				ExclusionReason:               row.ExclusionReason,
				Power:                         row.Power,
				ContextLength:                 row.ContextWindow,
				CostInputPerMTok:              row.CostInputPerM,
				CostOutputPerMTok:             row.CostOutputPerM,
				RecentLatencyMS:               float64(row.RecentP50Latency.Milliseconds()),
				ProviderReliabilityRate:       statusMetricValue(in.SuccessRate, provider, endpoint, model),
				QuotaRemaining:                cloneStatusInt(row.QuotaRemaining),
				SnapshotCapturedAt:            in.Snapshot.AsOf,
				HealthFreshnessAt:             row.HealthFreshnessAt.UTC(),
				HealthFreshnessSource:         row.HealthFreshnessSource,
				QuotaFreshnessAt:              row.QuotaFreshnessAt.UTC(),
				QuotaFreshnessSource:          row.QuotaFreshnessSource,
				ModelDiscoveryFreshnessAt:     row.DiscoveredAt.UTC(),
				ModelDiscoveryFreshnessSource: string(row.DiscoveredVia),
			}
			candidate.Cooldown = CandidateCooldown(in.ActiveAttempts, Key{
				Harness:        "fiz",
				Provider:       provider,
				Endpoint:       endpoint,
				ServerInstance: server,
				Model:          model,
			}, in.CooldownTTL)
			if candidate.Cooldown != nil {
				candidate.Healthy = false
			}
			if candidate.RecentLatencyMS == 0 {
				candidate.RecentLatencyMS = statusMetricValue(in.LatencyMS, provider, endpoint, model)
			}
			entry.Candidates = append(entry.Candidates, candidate)
		}
		entries = append(entries, entry)
	}
	return entries
}

func statusRowLess(left, right modelsnapshot.KnownModel) bool {
	leftProvider := strings.TrimSpace(left.Provider)
	rightProvider := strings.TrimSpace(right.Provider)
	if leftProvider != rightProvider {
		return leftProvider < rightProvider
	}
	leftEndpoint := strings.TrimSpace(left.EndpointName)
	rightEndpoint := strings.TrimSpace(right.EndpointName)
	if leftEndpoint != rightEndpoint {
		return leftEndpoint < rightEndpoint
	}
	leftBaseURL := strings.TrimSpace(left.EndpointBaseURL)
	rightBaseURL := strings.TrimSpace(right.EndpointBaseURL)
	if leftBaseURL != rightBaseURL {
		return leftBaseURL < rightBaseURL
	}
	leftServer := strings.TrimSpace(serverinstance.Normalize(left.EndpointBaseURL, left.ServerInstance))
	rightServer := strings.TrimSpace(serverinstance.Normalize(right.EndpointBaseURL, right.ServerInstance))
	if leftServer != rightServer {
		return leftServer < rightServer
	}
	return strings.TrimSpace(left.ID) < strings.TrimSpace(right.ID)
}

func statusMetricValue(values map[string]float64, provider, endpoint, model string) float64 {
	if len(values) == 0 {
		return 0
	}
	if value, ok := values[ProviderModelKey(Key{Provider: provider, Endpoint: endpoint, Model: model})]; ok {
		return value
	}
	if endpoint != "" {
		if value, ok := values[ProviderModelKey(Key{Provider: provider, Model: model})]; ok {
			return value
		}
	}
	return 0
}

func cloneStatusInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
