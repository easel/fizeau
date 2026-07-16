package serviceimpl

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

// SessionLogDecision carries the selected-route state needed when the final
// event does not repeat every route field. It is deliberately smaller than
// ExecuteDecision so the durable-log package surface stays independent of
// routing mechanics.
type SessionLogDecision struct {
	ServerInstance string
}

// SessionLogOptions carries the API-neutral pieces needed to open one
// historical session log.
type SessionLogOptions struct {
	Dir                 string
	SessionID           string
	Start               session.SessionStartData
	EndBase             session.SessionEndData
	Decision            SessionLogDecision
	RoutingDecision     any
	RoutingDecisionType agentcore.EventType
}

// SessionLog owns the durable session log writer and progress timing state for
// one Execute call.
type SessionLog struct {
	logger    *session.Logger
	path      string
	endBase   session.SessionEndData
	decision  SessionLogDecision
	endOnce   sync.Once
	endWrote  atomic.Bool
	closeOnce sync.Once

	progressMu     sync.Mutex
	lastProgressAt time.Time
}

// OpenSessionLog opens a historical session log and emits its session.start
// record. Empty dir/session IDs yield a no-op log so callers can stay linear.
func OpenSessionLog(opts SessionLogOptions) *SessionLog {
	if opts.Dir == "" || opts.SessionID == "" {
		return &SessionLog{}
	}
	logger := session.NewLogger(opts.Dir, opts.SessionID)
	sl := &SessionLog{
		logger:   logger,
		path:     filepath.Join(opts.Dir, opts.SessionID+".jsonl"),
		endBase:  cloneSessionEndData(opts.EndBase),
		decision: opts.Decision,
	}
	logger.Emit(agentcore.EventSessionStart, opts.Start)
	if opts.RoutingDecision != nil {
		eventType := opts.RoutingDecisionType
		if eventType == "" {
			eventType = agentcore.EventType("routing_decision")
		}
		logger.Emit(eventType, opts.RoutingDecision)
	}
	return sl
}

// Enabled reports whether this log has a backing writer.
func (sl *SessionLog) Enabled() bool {
	return sl != nil && sl.logger != nil
}

// Path returns the backing JSONL path, or empty when logging is disabled.
func (sl *SessionLog) Path() string {
	if sl == nil {
		return ""
	}
	return sl.path
}

// WriteEnd projects and records the terminal session.end event. The first
// call wins, matching the public event stream's single-terminal invariant.
func (sl *SessionLog) WriteEnd(meta map[string]string, final harnesses.FinalData) {
	if !sl.Enabled() {
		return
	}
	sl.endOnce.Do(func() {
		end := sl.endData(meta, final)
		sl.endWrote.Store(true)
		sl.logger.Emit(agentcore.EventSessionEnd, end)
	})
}

// WriteCoreEvent appends one raw agent/core event, excluding records owned by the
// higher-level session lifecycle helpers.
func (sl *SessionLog) WriteCoreEvent(ev agentcore.Event) {
	if !sl.Enabled() {
		return
	}
	switch ev.Type {
	case agentcore.EventSessionStart, agentcore.EventSessionEnd,
		agentcore.EventOverride, agentcore.EventRejectedOverride:
		return
	}
	sl.logger.Write(ev)
}

// WriteOverride appends an already-projected override-style event to the
// session log. The root facade owns the public payload; this runtime owns only
// durable ordering and intentionally treats the JSON as opaque.
func (sl *SessionLog) WriteOverride(eventType agentcore.EventType, raw json.RawMessage) {
	if !sl.Enabled() || len(raw) == 0 {
		return
	}
	if !json.Valid(raw) {
		return
	}
	sl.logger.Emit(eventType, raw)
}

// Close flushes the underlying log file. Safe to call multiple times.
func (sl *SessionLog) Close() {
	if !sl.Enabled() {
		return
	}
	sl.closeOnce.Do(func() {
		_ = sl.logger.Close()
	})
}

// EndWritten reports whether WriteEnd has already recorded session.end.
func (sl *SessionLog) EndWritten() bool {
	if sl == nil {
		return false
	}
	return sl.endWrote.Load()
}

// ProgressIntervalMS reports elapsed milliseconds since the previous progress
// update and records the current timestamp.
func (sl *SessionLog) ProgressIntervalMS(now time.Time) int64 {
	if sl == nil || now.IsZero() {
		return 0
	}
	sl.progressMu.Lock()
	defer sl.progressMu.Unlock()
	if sl.lastProgressAt.IsZero() {
		sl.lastProgressAt = now
		return 0
	}
	elapsed := now.Sub(sl.lastProgressAt).Milliseconds()
	sl.lastProgressAt = now
	if elapsed <= 0 {
		return 0
	}
	return elapsed
}

func (sl *SessionLog) endData(meta map[string]string, final harnesses.FinalData) session.SessionEndData {
	end := cloneSessionEndData(sl.endBase)
	end.Status = harnessStatusToCoreStatus(final.Status)
	end.Outcome = final.Outcome
	end.Cause = final.Cause
	end.Stage = final.Stage
	end.PrimaryOutcome = final.PrimaryOutcome
	end.PrimaryCause = final.PrimaryCause
	end.PrimaryStage = final.PrimaryStage
	end.ProcessOutcome = processOutcomeForFinal(final.Status)
	end.Output = final.FinalText
	end.Tokens = finalUsageToCoreTokens(final.Usage)
	end.DurationMs = final.DurationMS
	end.Metadata = cloneStringMap(meta)
	end.Error = final.Error

	end.CostUSD, end.CostSource = normalizeProjectedFinalCost(final.FinalCostUSD, final.FinalCostSource)
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
		} else {
			end.FailoverCount = 0
		}
	}
	if final.Reasoning != nil {
		end.ResolvedReasoning = agentcore.Reasoning(final.Reasoning.ResolvedReasoning)
		end.ReasoningSource = final.Reasoning.Source
	}
	if end.SelectedServerInstance == "" {
		end.SelectedServerInstance = sl.decision.ServerInstance
	}
	return end
}

func cloneSessionEndData(in session.SessionEndData) session.SessionEndData {
	out := in
	out.AttemptedProviders = append([]string(nil), in.AttemptedProviders...)
	out.Metadata = cloneStringMap(in.Metadata)
	out.CostUSD, out.CostSource = normalizeProjectedFinalCost(in.CostUSD, in.CostSource)
	if in.CostCapUSD != nil {
		value := *in.CostCapUSD
		out.CostCapUSD = &value
	}
	out.Utilization.ActiveRequests = cloneInt(in.Utilization.ActiveRequests)
	out.Utilization.QueuedRequests = cloneInt(in.Utilization.QueuedRequests)
	out.Utilization.MaxConcurrency = cloneInt(in.Utilization.MaxConcurrency)
	out.Utilization.CachePressure = cloneFloat64(in.Utilization.CachePressure)
	return out
}

func cloneInt(in *int) *int {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

func cloneFloat64(in *float64) *float64 {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

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

func processOutcomeForFinal(status string) string {
	if status == string(agentcore.StatusBudgetHalted) {
		return "budget_halted"
	}
	return ""
}

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

func derefHarnessInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
