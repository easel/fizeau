package fizeau

import (
	"encoding/json"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// executeCoordinatorRequest converts the public Execute contract into the
// API-neutral request owned by internal/serviceimpl. Route selection has
// already completed when this adapter runs.
func (s *service) executeCoordinatorRequest(req ServiceExecuteRequest, decision RouteDecision, sessionID string, ovr *overrideContext) serviceimpl.ExecuteRequest {
	var stallMaxReadOnlyIterations *int
	if req.StallPolicy != nil {
		value := req.StallPolicy.MaxReadOnlyToolIterations
		stallMaxReadOnlyIterations = &value
	}

	routingDecisionData, _ := json.Marshal(serviceRoutingDecisionDataFromDecision(req, decision, sessionID))

	return serviceimpl.ExecuteRequest{
		SessionID: sessionID,

		Prompt:            req.Prompt,
		SystemPrompt:      req.SystemPrompt,
		RequestedModel:    req.Model,
		RequestedProvider: req.Provider,
		RequestedHarness:  req.Harness,
		WorkDir:           req.WorkDir,

		Temperature:       req.Temperature,
		TopP:              req.TopP,
		TopK:              req.TopK,
		MinP:              req.MinP,
		RepetitionPenalty: req.RepetitionPenalty,
		Seed:              req.Seed,
		SamplingSource:    req.SamplingSource,
		Reasoning:         effectiveReasoning(req.Reasoning),
		NoStream:          req.NoStream,
		Permissions:       req.Permissions,
		Tools:             req.Tools,
		ToolPreset:        req.ToolPreset,
		PlanningMode:      req.PlanningMode,

		MaxIterations:           req.MaxIterations,
		MaxTokens:               req.MaxTokens,
		ReasoningByteLimit:      req.ReasoningByteLimit,
		CompactionContextWindow: req.CompactionContextWindow,
		CompactionReserveTokens: req.CompactionReserveTokens,

		Timeout:         req.Timeout,
		IdleTimeout:     req.IdleTimeout,
		ProviderTimeout: req.ProviderTimeout,
		CleanupTimeout:  s.opts.harnessCleanupTimeout(),
		CachePolicy:     req.CachePolicy,
		CostCapUSD:      req.CostCapUSD,

		StallMaxReadOnlyIterations: stallMaxReadOnlyIterations,

		SessionLogDir:    req.SessionLogDir,
		LifecycleBaseDir: s.serviceSessionLogDir(),
		Metadata:         req.Metadata,
		FinalMetadata:    metaWithRoleAndCorrelation(req.Metadata, req.Role, req.CorrelationID),
		CollisionWarning: executeCollisionWarning(req),

		Decision: serviceimpl.ExecuteDecision{
			Harness:        decision.Harness,
			Provider:       decision.Provider,
			Endpoint:       decision.Endpoint,
			ServerInstance: decision.ServerInstance,
			Model:          decision.Model,
			Reason:         decision.Reason,
			Power:          decision.Power,
			Candidates:     nativeRouteCandidates(decision.Candidates),
		},
		RoutingDecisionData: routingDecisionData,
		RouteProgress:       toTranscriptProgress(routeProgressData(decision)),
		OverridePayload:     executeOverridePayload(ovr, sessionID),
	}
}

// executeCoordinatorPorts supplies the narrow set of root-owned state and
// projection callbacks needed by the internal coordinator.
func (s *service) executeCoordinatorPorts(req ServiceExecuteRequest, decision RouteDecision, sessionID string, ovr *overrideContext) serviceimpl.ExecutePorts {
	return serviceimpl.ExecutePorts{
		OpenSessionLog: func() serviceimpl.ExecuteSessionLog {
			return s.openSessionLog(req, decision, sessionID)
		},
		ResolveNativeProvider: func(nreq serviceimpl.NativeProviderRequest) serviceimpl.NativeProviderResolution {
			resolved := s.resolveNativeProvider(ServiceExecuteRequest{
				Provider: nreq.Provider,
				Harness:  nreq.Harness,
				Model:    nreq.Model,
			})
			return serviceimpl.NativeProviderResolution{
				Provider: resolved.Provider,
				Name:     resolved.Name,
				Model:    resolved.Entry.Model,
			}
		},
		ProviderNotConfiguredError: func(nreq serviceimpl.NativeProviderRequest, ndecision serviceimpl.NativeDecision) string {
			return s.nativeProviderNotConfiguredError(ServiceExecuteRequest{
				Provider: nreq.Provider,
				Harness:  nreq.Harness,
				Model:    nreq.Model,
			}, routeDecision(ndecision))
		},
		ObserveRouteAttempt:        s.recordRouteAttemptFromFinal,
		ObserveWrappedRouteAttempt: s.observeRouteAttemptFromFinal,
		ObserveTokenUsage:          s.observeTokenUsage,
		CatalogPower: func(model string) int {
			return catalogPowerForModel(serviceRoutingCatalog(), model)
		},
		RecordOverrideOutcome: func(status string) {
			recordExecuteOverrideOutcome(ovr, status)
		},
		ObserveSubprocessDispatch: func(runner harnesses.Harness) {
			if s.subprocessDispatchObserver != nil {
				s.subprocessDispatchObserver(runner)
			}
		},
		ToolWiringHook:          s.toolWiringHook(),
		PromptAssertionHook:     s.promptAssertionHook(),
		CompactionAssertionHook: s.compactionAssertionHook(),
	}
}

func executeCollisionWarning(req ServiceExecuteRequest) *harnesses.FinalWarning {
	collisions := metadataReservedKeyCollisions(req.Metadata, req.Role, req.CorrelationID)
	if len(collisions) == 0 {
		return nil
	}
	return &harnesses.FinalWarning{
		Code:    MetadataWarningCodeKeyCollision,
		Message: metadataKeyCollisionMessage(collisions),
	}
}

func routeDecision(decision serviceimpl.NativeDecision) RouteDecision {
	return RouteDecision{
		Harness:        decision.Harness,
		Provider:       decision.Provider,
		ServerInstance: decision.ServerInstance,
		Model:          decision.Model,
	}
}

func nativeRouteCandidates(in []RouteCandidate) []serviceimpl.NativeRouteCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]serviceimpl.NativeRouteCandidate, len(in))
	for i, candidate := range in {
		out[i] = serviceimpl.NativeRouteCandidate{
			Provider:       candidate.Provider,
			Endpoint:       candidate.Endpoint,
			ServerInstance: candidate.ServerInstance,
			Model:          candidate.Model,
			Eligible:       candidate.Eligible,
		}
	}
	return out
}
