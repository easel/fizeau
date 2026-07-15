package fizeau

import (
	"context"
	"strings"
	"time"

	quotaimpl "github.com/easel/fizeau/internal/quota"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// DefaultOpenrouterCreditBalanceThresholdUSD is the floor below which a
// cached OpenRouter account balance triggers the credit-balance gate.
// Operators can override it per provider via ServiceProviderEntry.
const DefaultOpenrouterCreditBalanceThresholdUSD = quotaimpl.DefaultOpenRouterCreditBalanceThresholdUSD

// DefaultOpenrouterCreditProbeTTL bounds how long a cached balance reading
// stays fresh before the next routing pass re-probes /api/v1/credits.
const DefaultOpenrouterCreditProbeTTL = quotaimpl.DefaultOpenRouterCreditProbeTTL

// openrouterProbeProjection is the root facade's narrow adaptation from
// API-neutral quota evidence to routing-engine evidence.
type openrouterProbeProjection struct {
	CreditExhausted     map[string]routing.ProviderCreditExhaustedEvidence
	CredentialInvalid   map[string]routing.ProviderCredentialInvalidEvidence
	ProviderUnreachable map[string]routing.ProviderProbeUnreachableEvidence
}

// openrouterProbeMaps adapts public ServiceConfig entries into the
// API-neutral quota input, then projects internal quota evidence into the
// routing engine's evidence types. Credential-shape validation intentionally
// remains at this root boundary because it consumes the public config entry.
func (s *service) openrouterProbeMaps(ctx context.Context, now time.Time) openrouterProbeProjection {
	var out openrouterProbeProjection
	if s == nil || s.openrouterCredit == nil || s.opts.ServiceConfig == nil {
		return out
	}

	cfg := s.opts.ServiceConfig
	providers := make([]quotaimpl.OpenRouterCreditProvider, 0, len(cfg.ProviderNames()))
	for _, name := range cfg.ProviderNames() {
		provider, ok := cfg.Provider(name)
		if !ok || normalizeServiceProviderType(provider.Type) != "openrouter" {
			continue
		}
		apiKey := strings.TrimSpace(provider.APIKey)
		if !serviceimpl.OpenRouterAPIKeyWellFormed(apiKey) {
			continue
		}
		providers = append(providers, quotaimpl.OpenRouterCreditProvider{
			Name:                      name,
			BaseURL:                   provider.BaseURL,
			APIKey:                    apiKey,
			CreditBalanceThresholdUSD: provider.CreditBalanceThresholdUSD,
			CreditProbeTTL:            provider.CreditProbeTTL,
		})
	}

	projection := quotaimpl.ProjectOpenRouterCredits(ctx, s.openrouterCredit, now, providers)
	if len(projection.CreditExhausted) > 0 {
		out.CreditExhausted = make(map[string]routing.ProviderCreditExhaustedEvidence, len(projection.CreditExhausted))
		for name, evidence := range projection.CreditExhausted {
			out.CreditExhausted[name] = routing.ProviderCreditExhaustedEvidence{
				BalanceUSD:   evidence.BalanceUSD,
				ThresholdUSD: evidence.ThresholdUSD,
				ObservedAt:   evidence.ObservedAt,
			}
		}
	}
	if len(projection.CredentialInvalid) > 0 {
		out.CredentialInvalid = make(map[string]routing.ProviderCredentialInvalidEvidence, len(projection.CredentialInvalid))
		for name, evidence := range projection.CredentialInvalid {
			out.CredentialInvalid[name] = routing.ProviderCredentialInvalidEvidence{
				HTTPStatus: evidence.HTTPStatus,
				ObservedAt: evidence.ObservedAt,
			}
		}
	}
	if len(projection.ProviderUnreachable) > 0 {
		out.ProviderUnreachable = make(map[string]routing.ProviderProbeUnreachableEvidence, len(projection.ProviderUnreachable))
		for name, evidence := range projection.ProviderUnreachable {
			out.ProviderUnreachable[name] = routing.ProviderProbeUnreachableEvidence{
				StatusCode: evidence.StatusCode,
				ErrorClass: evidence.ErrorClass,
				Message:    evidence.Message,
				ObservedAt: evidence.ObservedAt,
			}
		}
	}
	return out
}

// annotateOpenrouterCreditFreshness overlays the credit-cache observation on
// both eligible and rejected OpenRouter candidate rows.
func (s *service) annotateOpenrouterCreditFreshness(decision *RouteDecision) {
	if s == nil || decision == nil || s.openrouterCredit == nil || s.opts.ServiceConfig == nil {
		return
	}
	for index := range decision.Candidates {
		freshness, ok := quotaimpl.OpenRouterCreditFreshness(s.openrouterCredit, decision.Candidates[index].Provider)
		if !ok {
			continue
		}
		provider, configured := s.opts.ServiceConfig.Provider(freshness.Provider)
		if !configured || normalizeServiceProviderType(provider.Type) != "openrouter" {
			continue
		}
		decision.Candidates[index].QuotaFreshnessAt = freshness.ObservedAt
		decision.Candidates[index].QuotaFreshnessSource = freshness.Source
	}
}
