package fizeau

import (
	"encoding/json"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/easel/fizeau/internal/session"
	"github.com/easel/fizeau/internal/transcript"
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
		SessionID:         sessionID,
		ConfiguredHarness: s.harnessByName(decision.Harness),

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
		RouteProgress:       transcript.RouteProgressData(toTranscriptRouteDecision(decision)),
		OverridePayload:     executeOverridePayload(ovr, sessionID),
	}
}

// executeCoordinatorPorts supplies the narrow set of root-owned state and
// projection callbacks needed by the internal coordinator.
func (s *service) executeCoordinatorPorts(req ServiceExecuteRequest, decision RouteDecision, sessionID string, ovr *overrideContext) serviceimpl.ExecutePorts {
	return serviceimpl.ExecutePorts{
		OpenSessionLog: func() serviceimpl.ExecuteSessionLog {
			return s.openExecuteSessionLog(req, decision, sessionID)
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

// openExecuteSessionLog is the narrow public-to-internal projection seam for
// durable execution history. The internal runtime owns writing, terminal
// merge semantics, first-end-wins, and progress timing.
func (s *service) openExecuteSessionLog(req ServiceExecuteRequest, decision RouteDecision, sessionID string) *serviceimpl.SessionLog {
	headerMeta := metaWithRoleAndCorrelation(req.Metadata, req.Role, req.CorrelationID)
	start := session.SessionStartData{
		Provider:               s.providerTypeLabel(decision.Provider),
		Model:                  decision.Model,
		SelectedProvider:       decision.Provider,
		SelectedEndpoint:       decision.Endpoint,
		SelectedServerInstance: decision.ServerInstance,
		SelectedRoute:          req.SelectedRoute,
		Sticky: session.RoutingStickyState{
			KeyPresent:     req.CorrelationID != "",
			Assignment:     decision.Sticky.Assignment,
			ServerInstance: decision.Sticky.ServerInstance,
			Reason:         decision.Sticky.Reason,
			Bonus:          decision.Sticky.Bonus,
		},
		Utilization: session.RoutingUtilizationState{
			Source:         decision.Utilization.Source,
			Freshness:      decision.Utilization.Freshness,
			ActiveRequests: decision.Utilization.ActiveRequests,
			QueuedRequests: decision.Utilization.QueuedRequests,
			MaxConcurrency: decision.Utilization.MaxConcurrency,
			CachePressure:  decision.Utilization.CachePressure,
			ObservedAt:     decision.Utilization.ObservedAt,
		},
		RequestedHarness: req.Harness,
		ResolvedHarness:  decision.Harness,
		HarnessSource:    harnessSource(req),
		RequestedModel:   req.Model,
		ResolvedModel:    decision.Model,
		Reasoning:        req.Reasoning,
		WorkDir:          req.WorkDir,
		MaxIterations:    req.MaxIterations,
		Prompt:           req.Prompt,
		SystemPrompt:     req.SystemPrompt,
		Metadata:         headerMeta,
	}
	endBase := session.SessionEndData{
		SelectedRoute:    req.SelectedRoute,
		SelectedEndpoint: decision.Endpoint,
		Sticky: session.RoutingStickyState{
			KeyPresent:     req.CorrelationID != "",
			Assignment:     decision.Sticky.Assignment,
			ServerInstance: decision.Sticky.ServerInstance,
			Reason:         decision.Sticky.Reason,
			Bonus:          decision.Sticky.Bonus,
		},
		Utilization: session.RoutingUtilizationState{
			Source:         decision.Utilization.Source,
			Freshness:      decision.Utilization.Freshness,
			ActiveRequests: decision.Utilization.ActiveRequests,
			QueuedRequests: decision.Utilization.QueuedRequests,
			MaxConcurrency: decision.Utilization.MaxConcurrency,
			CachePressure:  decision.Utilization.CachePressure,
			ObservedAt:     decision.Utilization.ObservedAt,
		},
		RequestedHarness: req.Harness,
		HarnessSource:    harnessSource(req),
		RequestedModel:   req.Model,
		Reasoning:        req.Reasoning,
	}
	if req.CostCapUSD > 0 {
		cap := req.CostCapUSD
		endBase.CostCapUSD = &cap
	}

	var routingDecision any
	if decision.Harness != "" || decision.Provider != "" || decision.Model != "" || len(decision.Candidates) > 0 || !decision.SnapshotCapturedAt.IsZero() {
		routingDecision = serviceRoutingDecisionDataFromDecision(req, decision, sessionID)
	}
	return serviceimpl.OpenSessionLog(serviceimpl.SessionLogOptions{
		Dir:       req.SessionLogDir,
		SessionID: sessionID,
		Start:     start,
		EndBase:   endBase,
		Decision: serviceimpl.SessionLogDecision{
			ServerInstance: decision.ServerInstance,
		},
		RoutingDecision:     routingDecision,
		RoutingDecisionType: agentcore.EventType(ServiceEventTypeRoutingDecision),
	})
}

// persistRejectedOverride records pre-dispatch pin rejection evidence through
// the same internal runtime used by accepted execution.
func (s *service) persistRejectedOverride(req ServiceExecuteRequest, sessionID string, payload ServiceOverrideData) {
	if req.SessionLogDir == "" || sessionID == "" {
		return
	}
	sl := s.openExecuteSessionLog(req, RouteDecision{}, sessionID)
	if sl == nil || !sl.Enabled() {
		return
	}
	defer sl.Close()
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	sl.WriteOverride(agentcore.EventType(ServiceEventTypeRejectedOverride), raw)
}

// providerTypeLabel maps a configured provider name to its concrete type for
// the durable session.start projection.
func (s *service) providerTypeLabel(name string) string {
	if s == nil || s.opts.ServiceConfig == nil || name == "" {
		return name
	}
	entry, ok := s.opts.ServiceConfig.Provider(name)
	if !ok || entry.Type == "" {
		return name
	}
	return entry.Type
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

func toTranscriptRouteDecision(decision RouteDecision) transcript.RouteProgressDecision {
	out := transcript.RouteProgressDecision{
		Harness:  decision.Harness,
		Provider: decision.Provider,
		Model:    decision.Model,
		Power:    decision.Power,
	}
	if len(decision.Candidates) == 0 {
		return out
	}
	out.Candidates = make([]transcript.RouteProgressCandidate, len(decision.Candidates))
	for i, candidate := range decision.Candidates {
		out.Candidates[i] = transcript.RouteProgressCandidate{
			Harness:            candidate.Harness,
			Provider:           candidate.Provider,
			Model:              candidate.Model,
			CostUSDPer1kTokens: candidate.CostUSDPer1kTokens,
			CostSource:         candidate.CostSource,
			Components: transcript.RouteProgressComponents{
				Power:     candidate.Components.Power,
				SpeedTPS:  candidate.Components.SpeedTPS,
				CostClass: candidate.Components.CostClass,
			},
		}
	}
	return out
}
