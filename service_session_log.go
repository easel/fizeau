package fizeau

import (
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/easel/fizeau/internal/session"
)

// serviceSessionLog is the root-facade adapter over the internal session-log
// runtime. It keeps public request/decision types at the boundary while the
// concrete file writer and progress timing live behind internal/serviceimpl.
type serviceSessionLog struct {
	impl      *serviceimpl.SessionLog
	path      string
	sessionID string
	decision  RouteDecision
	override  *overrideContext
}

// openSessionLog creates the session-log writer for req and emits the
// session.start record. A nil-or-empty req.SessionLogDir yields a no-op
// writer so callers don't need to branch on whether logging is enabled.
func (s *service) openSessionLog(req ServiceExecuteRequest, decision RouteDecision, sessionID string) *serviceSessionLog {
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

	var routingDecision any
	if decision.Harness != "" || decision.Provider != "" || decision.Model != "" || len(decision.Candidates) > 0 || !decision.SnapshotCapturedAt.IsZero() {
		routingDecision = serviceRoutingDecisionDataFromDecision(req, decision, sessionID)
	}
	impl := serviceimpl.OpenSessionLog(serviceimpl.SessionLogOptions{
		Dir:                 req.SessionLogDir,
		SessionID:           sessionID,
		Start:               start,
		RoutingDecision:     routingDecision,
		RoutingDecisionType: agentcore.EventType(ServiceEventTypeRoutingDecision),
	})
	return &serviceSessionLog{
		impl:      impl,
		path:      impl.Path(),
		sessionID: sessionID,
		decision:  decision,
	}
}

// writeEnd records the terminal session.end event. It is idempotent: the
// first call wins. Callers should invoke this whenever a harnesses.FinalData
// is produced so the session log stays in sync with the public event stream.
func (sl *serviceSessionLog) writeEnd(req ServiceExecuteRequest, meta map[string]string, final harnesses.FinalData) {
	if sl == nil || !sl.enabled() {
		return
	}
	end := session.SessionEndData{
		Status:           harnessStatusToCoreStatus(final.Status),
		Outcome:          final.Outcome,
		Cause:            final.Cause,
		Stage:            final.Stage,
		PrimaryOutcome:   final.PrimaryOutcome,
		PrimaryCause:     final.PrimaryCause,
		PrimaryStage:     final.PrimaryStage,
		ProcessOutcome:   processOutcomeForFinal(final.Status),
		Output:           final.FinalText,
		Tokens:           finalUsageToCoreTokens(final.Usage),
		DurationMs:       final.DurationMS,
		SelectedRoute:    req.SelectedRoute,
		SelectedEndpoint: sl.decision.Endpoint,
		Sticky: session.RoutingStickyState{
			KeyPresent:     req.CorrelationID != "",
			Assignment:     sl.decision.Sticky.Assignment,
			ServerInstance: sl.decision.Sticky.ServerInstance,
			Reason:         sl.decision.Sticky.Reason,
			Bonus:          sl.decision.Sticky.Bonus,
		},
		Utilization: session.RoutingUtilizationState{
			Source:         sl.decision.Utilization.Source,
			Freshness:      sl.decision.Utilization.Freshness,
			ActiveRequests: sl.decision.Utilization.ActiveRequests,
			QueuedRequests: sl.decision.Utilization.QueuedRequests,
			MaxConcurrency: sl.decision.Utilization.MaxConcurrency,
			CachePressure:  sl.decision.Utilization.CachePressure,
			ObservedAt:     sl.decision.Utilization.ObservedAt,
		},
		RequestedHarness: req.Harness,
		ResolvedHarness:  "",
		HarnessSource:    harnessSource(req),
		RequestedModel:   req.Model,
		Reasoning:        req.Reasoning,
		Metadata:         meta,
		Error:            final.Error,
	}
	if final.CostUSD > 0 {
		cost := final.CostUSD
		end.CostUSD = &cost
	}
	if req.CostCapUSD > 0 {
		cap := req.CostCapUSD
		end.CostCapUSD = &cap
	}
	if final.RoutingActual != nil {
		end.ResolvedHarness = final.RoutingActual.Harness
		end.Model = final.RoutingActual.Model
		end.SelectedProvider = final.RoutingActual.Provider
		if final.RoutingActual.ServerInstance != "" {
			end.SelectedServerInstance = final.RoutingActual.ServerInstance
		}
		end.ResolvedModel = final.RoutingActual.Model
		end.AttemptedProviders = append([]string(nil), final.RoutingActual.FallbackChainFired...)
		if len(end.AttemptedProviders) > 1 {
			end.FailoverCount = len(end.AttemptedProviders) - 1
		}
	}
	if final.Reasoning != nil {
		end.ResolvedReasoning = agentcore.Reasoning(final.Reasoning.ResolvedReasoning)
		end.ReasoningSource = final.Reasoning.Source
	}
	if end.SelectedServerInstance == "" {
		end.SelectedServerInstance = sl.decision.ServerInstance
	}
	sl.impl.WriteEnd(end)
}

func (sl *serviceSessionLog) writeEvent(ev agentcore.Event) {
	if sl == nil || sl.impl == nil {
		return
	}
	sl.impl.WriteEvent(ev)
}

func (sl *serviceSessionLog) writeOverrideEvent(eventType string, payload ServiceOverrideData) {
	if sl == nil || sl.impl == nil {
		return
	}
	payload.SessionID = sl.sessionID
	sl.impl.WriteOverrideEvent(agentcore.EventType(eventType), payload)
}

func (sl *serviceSessionLog) close() {
	if sl == nil || sl.impl == nil {
		return
	}
	sl.impl.Close()
}

func (sl *serviceSessionLog) endWritten() bool {
	if sl == nil || sl.impl == nil {
		return false
	}
	return sl.impl.EndWritten()
}

func (sl *serviceSessionLog) progressIntervalMS(now time.Time) int64 {
	if sl == nil || sl.impl == nil {
		return 0
	}
	return sl.impl.ProgressIntervalMS(now)
}

func (sl *serviceSessionLog) enabled() bool {
	return sl != nil && sl.impl != nil && sl.impl.Enabled()
}

// persistRejectedOverride writes a session.start + rejected_override pair
// to a freshly-opened session log for sessionID. Used by Execute's
// pre-dispatch rejection branch, which never reaches runExecute and so
// never goes through the normal openSessionLog path.
func (s *service) persistRejectedOverride(req ServiceExecuteRequest, sessionID string, payload ServiceOverrideData) {
	if req.SessionLogDir == "" || sessionID == "" {
		return
	}
	sl := s.openSessionLog(req, RouteDecision{}, sessionID)
	if sl == nil || !sl.enabled() {
		return
	}
	defer sl.close()
	sl.writeOverrideEvent(ServiceEventTypeRejectedOverride, payload)
}

// providerTypeLabel maps a configured provider name ("local") to its provider
// type ("lmstudio") when available. Returns the input unchanged if no
// ServiceConfig is attached or the name is not configured.
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

// harnessStatusToCoreStatus maps a public harnesses.FinalData.Status string
// to an internal agentcore.Status. Unknown / error-y statuses collapse to
// StatusError so session.end always carries a well-defined status.
func harnessStatusToCoreStatus(status string) agentcore.Status {
	switch status {
	case "success":
		return agentcore.StatusSuccess
	case "iteration_limit":
		return agentcore.StatusIterationLimit
	case "cancelled":
		return agentcore.StatusCancelled
	case string(agentcore.StatusBudgetHalted):
		return agentcore.StatusBudgetHalted
	default:
		return agentcore.StatusError
	}
}

// processOutcomeForFinal returns the FEAT-005 §27 process_outcome label for
// a session.end record.
func processOutcomeForFinal(status string) string {
	if status == string(agentcore.StatusBudgetHalted) {
		return "budget_halted"
	}
	return ""
}

// finalUsageToCoreTokens converts the public FinalUsage pointer form into
// the internal TokenUsage struct used by session.end.
func finalUsageToCoreTokens(usage *harnesses.FinalUsage) agentcore.TokenUsage {
	if usage == nil {
		return agentcore.TokenUsage{}
	}
	return agentcore.TokenUsage{
		Input:      derefHarnessInt(usage.InputTokens),
		Output:     derefHarnessInt(usage.OutputTokens),
		CacheRead:  derefHarnessInt(usage.CacheReadTokens),
		CacheWrite: derefHarnessInt(usage.CacheWriteTokens),
		Total:      derefHarnessInt(usage.TotalTokens),
	}
}

func derefHarnessInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
