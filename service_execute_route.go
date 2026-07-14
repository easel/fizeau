package fizeau

import (
	"context"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	quotaimpl "github.com/easel/fizeau/internal/quota"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// resolveExecuteRouteInternal adapts the public route request and error
// contract around the API-neutral resolver. The routing engine continues to
// return the full public decision trace; internal/serviceimpl owns explicit
// pin validation and normalization.
func (s *service) resolveExecuteRouteInternal(ctx context.Context, req ServiceExecuteRequest) (*RouteDecision, error) {
	catalog := serviceRoutingCatalog()
	providers, providerNames := executeRouteProviders(s.opts.ServiceConfig)
	var engineDecision *RouteDecision

	decision, failure := serviceimpl.ResolveExecuteRoute(ctx, serviceimpl.ExecuteRouteInput{
		Request: serviceimpl.ExecuteRouteRequest{
			Harness:   req.Harness,
			Provider:  req.Provider,
			Model:     req.Model,
			Policy:    req.Policy,
			Reasoning: string(req.Reasoning),
			MinPower:  req.MinPower,
		},
		Harnesses:        s.registry,
		Providers:        providers,
		ProviderNames:    providerNames,
		HasServiceConfig: s.opts.ServiceConfig != nil,
		Catalog:          catalog,
		ResolveWithEngine: func(ctx context.Context) (serviceimpl.ExecuteRouteDecision, error) {
			resolved, err := s.ResolveRoute(ctx, executeRouteRequest(req))
			if err != nil {
				return serviceimpl.ExecuteRouteDecision{}, err
			}
			engineDecision = resolved
			return serviceImplExecuteRouteDecision(resolved), nil
		},
		PreserveEngineError: isExplicitPinError,
		DiscoverModels:      subprocessHarnessModelIDs,
		ResolveModelAlias: func(harness, model string) string {
			return resolveSubprocessModelAliasWithCatalog(harness, model, catalog)
		},
		QuotaForHarness: func(name string, now time.Time) (serviceimpl.ExecuteRouteQuota, bool) {
			quota, ok := quotaimpl.SubscriptionForHarness(name, now)
			return serviceimpl.ExecuteRouteQuota{
				OK:      quota.OK,
				Present: quota.Present,
				Fresh:   quota.Fresh,
				Windows: append([]harnesses.QuotaWindow(nil), quota.Windows...),
			}, ok
		},
	})
	if failure != nil {
		return nil, publicExecuteRouteFailure(failure)
	}

	if engineDecision != nil {
		applyServiceImplExecuteRouteDecision(engineDecision, decision)
		return engineDecision, nil
	}
	return publicExecuteRouteDecision(decision), nil
}

func executeRouteRequest(req ServiceExecuteRequest) RouteRequest {
	return RouteRequest{
		Policy:                req.Policy,
		Model:                 req.Model,
		Provider:              req.Provider,
		Harness:               req.Harness,
		Reasoning:             req.Reasoning,
		Permissions:           req.Permissions,
		CachePolicy:           req.CachePolicy,
		MinPower:              req.MinPower,
		MaxPower:              req.MaxPower,
		EstimatedPromptTokens: req.EstimatedPromptTokens,
		RequiresTools:         req.RequiresTools,
		Role:                  req.Role,
		CorrelationID:         req.CorrelationID,
	}
}

func executeRouteProviders(config ServiceConfig) (map[string]serviceimpl.ProviderEntry, []string) {
	if config == nil {
		return nil, nil
	}
	names := config.ProviderNames()
	providers := make(map[string]serviceimpl.ProviderEntry, len(names))
	for _, name := range names {
		entry, ok := config.Provider(name)
		if !ok {
			continue
		}
		providers[name] = serviceImplProviderEntry(entry)
	}
	return providers, append([]string(nil), names...)
}

func serviceImplExecuteRouteDecision(decision *RouteDecision) serviceimpl.ExecuteRouteDecision {
	if decision == nil {
		return serviceimpl.ExecuteRouteDecision{}
	}
	return serviceimpl.ExecuteRouteDecision{
		Harness:        decision.Harness,
		Provider:       decision.Provider,
		ServerInstance: decision.ServerInstance,
		Endpoint:       decision.Endpoint,
		Model:          decision.Model,
		Reason:         decision.Reason,
		Power:          decision.Power,
	}
}

func applyServiceImplExecuteRouteDecision(result *RouteDecision, decision serviceimpl.ExecuteRouteDecision) {
	if result == nil {
		return
	}
	result.Harness = decision.Harness
	result.Provider = decision.Provider
	result.ServerInstance = decision.ServerInstance
	result.Endpoint = decision.Endpoint
	result.Model = decision.Model
	result.Reason = decision.Reason
	result.Power = decision.Power
}

func publicExecuteRouteDecision(decision serviceimpl.ExecuteRouteDecision) *RouteDecision {
	result := &RouteDecision{}
	applyServiceImplExecuteRouteDecision(result, decision)
	return result
}

func publicExecuteRouteFailure(failure *serviceimpl.ExecuteRouteFailure) error {
	if failure == nil {
		return nil
	}
	switch failure.Kind {
	case serviceimpl.ExecuteRouteFailurePolicyRequirement:
		return &ErrPolicyRequirementUnsatisfied{
			Policy:       failure.Policy,
			Requirement:  failure.Requirement,
			AttemptedPin: failure.AttemptedPin,
		}
	case serviceimpl.ExecuteRouteFailureUnknownProvider:
		return &ErrUnknownProvider{
			Provider:       failure.Provider,
			KnownProviders: append([]string(nil), failure.KnownProviders...),
		}
	case serviceimpl.ExecuteRouteFailureHarnessModelIncompatible:
		return &ErrHarnessModelIncompatible{
			Harness:         failure.Harness,
			Model:           failure.Model,
			SupportedModels: append([]string(nil), failure.SupportedModels...),
		}
	case serviceimpl.ExecuteRouteFailureQuotaUnavailable:
		return &NoViableProviderForNow{
			RetryAfter:         failure.RetryAfter,
			ExhaustedProviders: append([]string(nil), failure.ExhaustedProviders...),
		}
	case serviceimpl.ExecuteRouteFailureUnsatisfiablePin:
		return &ErrUnsatisfiablePin{Pin: failure.Pin, Reason: failure.Reason}
	case serviceimpl.ExecuteRouteFailureEngine:
		if failure.EngineErrorPassthrough && failure.Cause != nil {
			return failure.Cause
		}
	}
	return failure
}
