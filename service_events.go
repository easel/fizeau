package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	ServiceEventTypeTextDelta        = "text_delta"
	ServiceEventTypeToolCall         = "tool_call"
	ServiceEventTypeToolResult       = "tool_result"
	ServiceEventTypeCompaction       = "compaction"
	ServiceEventTypeProgress         = "progress"
	ServiceEventTypeRoutingDecision  = "routing_decision"
	ServiceEventTypeStall            = "stall"
	ServiceEventTypeFinal            = "final"
	ServiceEventTypeOverride         = "override"
	ServiceEventTypeRejectedOverride = "rejected_override"
)

// ServiceOverridePin captures a (harness, provider, model) tuple, used both
// for the user-supplied pin and the unconstrained auto decision in
// override / rejected_override events. Empty fields mean "axis not asserted /
// not produced" rather than zero.
type ServiceOverridePin struct {
	Harness  string `json:"harness"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// ServiceOverrideAutoComponents mirrors RouteCandidateComponents for the
// candidate the unconstrained auto pipeline would have picked. Zero fields
// mean "unknown / not contributing".
type ServiceOverrideAutoComponents struct {
	Power                   int     `json:"power"`
	Cost                    float64 `json:"cost"`
	CostClass               string  `json:"cost_class,omitempty"`
	LatencyMS               float64 `json:"latency_ms"`
	SpeedTPS                float64 `json:"speed_tps"`
	Utilization             float64 `json:"utilization"`
	SuccessRate             float64 `json:"success_rate"`
	QuotaOK                 bool    `json:"quota_ok"`
	QuotaPercentUsed        int     `json:"quota_percent_used"`
	QuotaTrend              string  `json:"quota_trend,omitempty"`
	Capability              float64 `json:"capability"`
	StickyAffinity          float64 `json:"sticky_affinity"`
	PowerWeightedCapability float64 `json:"power_weighted_capability"`
	PowerHintFit            float64 `json:"power_hint_fit"`
	LatencyWeight           float64 `json:"latency_weight"`
	PlacementBonus          float64 `json:"placement_bonus"`
	QuotaBonus              float64 `json:"quota_bonus"`
	MarginalCostPenalty     float64 `json:"marginal_cost_penalty"`
	AvailabilityPenalty     float64 `json:"availability_penalty"`
	StaleSignalPenalty      float64 `json:"stale_signal_penalty"`
}

// ServiceOverridePromptFeatures captures prompt-classification inputs that
// fed routing — used by post-hoc analysis to pivot override-class breakdowns.
// EstimatedTokens is nullable (nil = harness tokenizer did not produce a
// value); RequiresTools is the boolean carried on the request; Reasoning is
// the request's Reasoning level as a string.
type ServiceOverridePromptFeatures struct {
	EstimatedTokens *int   `json:"estimated_tokens,omitempty"`
	RequiresTools   bool   `json:"requires_tools"`
	Reasoning       string `json:"reasoning,omitempty"`
}

// ServiceOverrideOutcome carries post-execution status mirrored from the
// final event. Always omitted on rejected_override events.
type ServiceOverrideOutcome struct {
	Status     string     `json:"status"`
	CostUSD    float64    `json:"cost_usd,omitempty"`
	CostSource CostSource `json:"cost_source"`
	DurationMS int64      `json:"duration_ms"`
}

// ServiceOverrideData is the payload for both override and rejected_override
// events. Outcome is nil for rejected_override and populated post-execution
// for override.
type ServiceOverrideData struct {
	SessionID      string                        `json:"session_id,omitempty"`
	UserPin        ServiceOverridePin            `json:"user_pin"`
	AutoDecision   ServiceOverridePin            `json:"auto_decision"`
	AxesOverridden []string                      `json:"axes_overridden"`
	MatchPerAxis   map[string]bool               `json:"match_per_axis"`
	AutoScore      float64                       `json:"auto_score"`
	AutoComponents ServiceOverrideAutoComponents `json:"auto_components"`
	PromptFeatures ServiceOverridePromptFeatures `json:"prompt_features"`
	ReasonHint     string                        `json:"reason_hint,omitempty"`
	Outcome        *ServiceOverrideOutcome       `json:"outcome,omitempty"`
	RejectionError string                        `json:"rejection_error,omitempty"`
}

type ServiceTextDeltaData struct {
	Text string `json:"text"`
}

type ServiceToolCallData struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

type ServiceToolResultData struct {
	ID         string `json:"id"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type ServiceCompactionData struct {
	MessagesBefore int `json:"messages_before"`
	MessagesAfter  int `json:"messages_after"`
	TokensFreed    int `json:"tokens_freed"`
}

// ServiceProgressData is the bounded progress payload emitted alongside the
// public service event stream and persisted into the session log. The fields
// are intentionally compact so operators get useful turn state without
// leaking full prompts or raw tool output.
type ServiceProgressData struct {
	Phase                 string   `json:"phase"`
	State                 string   `json:"state"`
	Source                string   `json:"source,omitempty"`
	TaskID                string   `json:"task_id,omitempty"`
	Round                 int      `json:"round,omitempty"`
	Message               string   `json:"message,omitempty"`
	TurnIndex             int      `json:"turn_index,omitempty"`
	ToolName              string   `json:"tool_name,omitempty"`
	ToolCallID            string   `json:"tool_call_id,omitempty"`
	ToolCallIndex         int      `json:"tool_call_index,omitempty"`
	Command               string   `json:"command,omitempty"`
	Action                string   `json:"action,omitempty"`
	Target                string   `json:"subject,omitempty"`
	OutputSummary         string   `json:"output_summary,omitempty"`
	OutputBytes           int      `json:"output_bytes,omitempty"`
	OutputLines           int      `json:"output_lines,omitempty"`
	OutputExcerpt         string   `json:"output_excerpt,omitempty"`
	DurationMS            int64    `json:"duration_ms,omitempty"`
	SinceLastMS           int64    `json:"since_last_ms,omitempty"`
	TokPerSec             *float64 `json:"tok_per_sec,omitempty"`
	InputTokens           *int     `json:"input_tokens,omitempty"`
	OutputTokens          *int     `json:"output_tokens,omitempty"`
	TotalTokens           *int     `json:"total_tokens,omitempty"`
	ContextMessages       int      `json:"context_messages,omitempty"`
	ContextTokensEstimate int      `json:"context_tokens_estimate,omitempty"`
	SessionSummary        string   `json:"session_summary,omitempty"`
}

func routingDecisionEventCandidates(in []RouteCandidate) []ServiceRoutingDecisionCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]ServiceRoutingDecisionCandidate, len(in))
	for i, c := range in {
		out[i] = ServiceRoutingDecisionCandidate{
			Harness:                       c.Harness,
			Provider:                      c.Provider,
			Billing:                       c.Billing,
			ActualCashSpend:               c.ActualCashSpend,
			Endpoint:                      c.Endpoint,
			ServerInstance:                c.ServerInstance,
			Model:                         c.Model,
			Score:                         c.Score,
			CostUSDPer1kTokens:            c.CostUSDPer1kTokens,
			CostSource:                    c.CostSource,
			EffectiveCost:                 c.EffectiveCost,
			EffectiveCostSource:           c.EffectiveCostSource,
			Eligible:                      c.Eligible,
			Reason:                        c.Reason,
			FilterReason:                  c.FilterReason,
			ScoreComponents:               copyScoreComponents(c.ScoreComponents),
			SourceStatus:                  c.SourceStatus,
			AutoRoutable:                  c.AutoRoutable,
			ExactPinOnly:                  c.ExactPinOnly,
			ExclusionReason:               c.ExclusionReason,
			SnapshotCapturedAt:            c.SnapshotCapturedAt,
			HealthFreshnessAt:             c.HealthFreshnessAt,
			HealthFreshnessSource:         c.HealthFreshnessSource,
			QuotaFreshnessAt:              c.QuotaFreshnessAt,
			QuotaFreshnessSource:          c.QuotaFreshnessSource,
			ModelDiscoveryFreshnessAt:     c.ModelDiscoveryFreshnessAt,
			ModelDiscoveryFreshnessSource: c.ModelDiscoveryFreshnessSource,
			Components: ServiceRoutingDecisionComponents{
				Power:                   c.Components.Power,
				Cost:                    c.Components.Cost,
				CostClass:               c.Components.CostClass,
				LatencyMS:               c.Components.LatencyMS,
				SpeedTPS:                c.Components.SpeedTPS,
				Utilization:             c.Components.Utilization,
				SuccessRate:             c.Components.SuccessRate,
				QuotaOK:                 c.Components.QuotaOK,
				QuotaPercentUsed:        c.Components.QuotaPercentUsed,
				QuotaTrend:              c.Components.QuotaTrend,
				Capability:              c.Components.Capability,
				StickyAffinity:          c.Components.StickyAffinity,
				PowerWeightedCapability: c.Components.PowerWeightedCapability,
				PowerHintFit:            c.Components.PowerHintFit,
				LatencyWeight:           c.Components.LatencyWeight,
				PlacementBonus:          c.Components.PlacementBonus,
				QuotaBonus:              c.Components.QuotaBonus,
				MarginalCostPenalty:     c.Components.MarginalCostPenalty,
				AvailabilityPenalty:     c.Components.AvailabilityPenalty,
				StaleSignalPenalty:      c.Components.StaleSignalPenalty,
			},
			Utilization: ServiceRoutingUtilizationState{
				Source:         c.Utilization.Source,
				Freshness:      c.Utilization.Freshness,
				ActiveRequests: c.Utilization.ActiveRequests,
				QueuedRequests: c.Utilization.QueuedRequests,
				MaxConcurrency: c.Utilization.MaxConcurrency,
				CachePressure:  c.Utilization.CachePressure,
				ObservedAt:     c.Utilization.ObservedAt,
			},
		}
	}
	return out
}

func serviceRoutingDecisionDataFromDecision(req ServiceExecuteRequest, decision RouteDecision, sessionID string) ServiceRoutingDecisionData {
	data := ServiceRoutingDecisionData{
		Harness:               decision.Harness,
		Provider:              decision.Provider,
		Endpoint:              decision.Endpoint,
		ServerInstance:        decision.ServerInstance,
		Model:                 decision.Model,
		Reason:                decision.Reason,
		Sticky:                ServiceRoutingStickyState{},
		Utilization:           ServiceRoutingUtilizationState{},
		RequestedPolicy:       req.Policy,
		RequestedModel:        req.Model,
		RequestedProvider:     req.Provider,
		RequestedHarness:      req.Harness,
		MinPower:              req.MinPower,
		MaxPower:              req.MaxPower,
		RequiresTools:         req.RequiresTools,
		EstimatedPromptTokens: req.EstimatedPromptTokens,
		Permissions:           req.Permissions,
		Role:                  req.Role,
		CorrelationID:         req.CorrelationID,
		HarnessSource:         harnessSource(req),
		SessionID:             sessionID,
		SnapshotCapturedAt:    decision.SnapshotCapturedAt,
		Candidates:            routingDecisionEventCandidates(decision.Candidates),
	}
	data.Sticky = ServiceRoutingStickyState{
		KeyPresent:     decision.Sticky.KeyPresent,
		Assignment:     decision.Sticky.Assignment,
		ServerInstance: decision.Sticky.ServerInstance,
		Reason:         decision.Sticky.Reason,
		Bonus:          decision.Sticky.Bonus,
	}
	data.Utilization = ServiceRoutingUtilizationState{
		Source:         decision.Utilization.Source,
		Freshness:      decision.Utilization.Freshness,
		ActiveRequests: decision.Utilization.ActiveRequests,
		QueuedRequests: decision.Utilization.QueuedRequests,
		MaxConcurrency: decision.Utilization.MaxConcurrency,
		CachePressure:  decision.Utilization.CachePressure,
		ObservedAt:     decision.Utilization.ObservedAt,
	}
	return data
}

type ServiceRoutingDecisionData struct {
	Harness               string                         `json:"harness"`
	Provider              string                         `json:"provider,omitempty"`
	Endpoint              string                         `json:"endpoint,omitempty"`
	ServerInstance        string                         `json:"server_instance,omitempty"`
	Model                 string                         `json:"model"`
	Reason                string                         `json:"reason"`
	Sticky                ServiceRoutingStickyState      `json:"sticky,omitempty"`
	Utilization           ServiceRoutingUtilizationState `json:"utilization,omitempty"`
	SourceStatus          string                         `json:"source_status,omitempty"`
	AutoRoutable          bool                           `json:"auto_routable,omitempty"`
	ExactPinOnly          bool                           `json:"exact_pin_only,omitempty"`
	ExclusionReason       string                         `json:"exclusion_reason,omitempty"`
	RequestedPolicy       string                         `json:"requested_policy,omitempty"`
	RequestedModel        string                         `json:"requested_model,omitempty"`
	RequestedProvider     string                         `json:"requested_provider,omitempty"`
	RequestedHarness      string                         `json:"requested_harness,omitempty"`
	MinPower              int                            `json:"min_power,omitempty"`
	MaxPower              int                            `json:"max_power,omitempty"`
	RequiresTools         bool                           `json:"requires_tools,omitempty"`
	EstimatedPromptTokens int                            `json:"estimated_prompt_tokens,omitempty"`
	Permissions           string                         `json:"permissions,omitempty"`
	Role                  string                         `json:"role,omitempty"`
	CorrelationID         string                         `json:"correlation_id,omitempty"`
	HarnessSource         string                         `json:"harness_source,omitempty"`
	FallbackChain         []string                       `json:"fallback_chain,omitempty"`
	SessionID             string                         `json:"session_id,omitempty"`
	SnapshotCapturedAt    time.Time                      `json:"snapshot_captured_at,omitempty"`

	// Candidates exposes the full ranked decision trace. Each candidate
	// carries per-axis component scores (cost / latency / success rate /
	// capability) plus an explicit filter_reason for rejected entries.
	Candidates []ServiceRoutingDecisionCandidate `json:"candidates,omitempty"`
}

// ServiceRoutingDecisionCandidate is one entry in the routing-decision
// event's candidates list. Mirrors RouteCandidate but with JSON tags
// suited for event consumers.
type ServiceRoutingDecisionCandidate struct {
	Harness                       string                           `json:"harness"`
	Provider                      string                           `json:"provider,omitempty"`
	Billing                       BillingModel                     `json:"billing,omitempty"`
	ActualCashSpend               bool                             `json:"actual_cash_spend"`
	Endpoint                      string                           `json:"endpoint,omitempty"`
	ServerInstance                string                           `json:"server_instance,omitempty"`
	Model                         string                           `json:"model,omitempty"`
	Score                         float64                          `json:"score"`
	CostUSDPer1kTokens            float64                          `json:"cost_usd_per_1k_tokens,omitempty"`
	CostSource                    string                           `json:"cost_source,omitempty"`
	EffectiveCost                 float64                          `json:"effective_cost"`
	EffectiveCostSource           string                           `json:"effective_cost_source,omitempty"`
	Eligible                      bool                             `json:"eligible"`
	Reason                        string                           `json:"reason,omitempty"`
	FilterReason                  string                           `json:"filter_reason,omitempty"`
	ScoreComponents               map[string]float64               `json:"score_components,omitempty"`
	SourceStatus                  string                           `json:"source_status,omitempty"`
	AutoRoutable                  bool                             `json:"auto_routable,omitempty"`
	ExactPinOnly                  bool                             `json:"exact_pin_only,omitempty"`
	ExclusionReason               string                           `json:"exclusion_reason,omitempty"`
	SnapshotCapturedAt            time.Time                        `json:"snapshot_captured_at,omitempty"`
	HealthFreshnessAt             time.Time                        `json:"health_freshness_at,omitempty"`
	HealthFreshnessSource         string                           `json:"health_freshness_source,omitempty"`
	QuotaFreshnessAt              time.Time                        `json:"quota_freshness_at,omitempty"`
	QuotaFreshnessSource          string                           `json:"quota_freshness_source,omitempty"`
	ModelDiscoveryFreshnessAt     time.Time                        `json:"model_discovery_freshness_at,omitempty"`
	ModelDiscoveryFreshnessSource string                           `json:"model_discovery_freshness_source,omitempty"`
	Components                    ServiceRoutingDecisionComponents `json:"components"`
	Utilization                   ServiceRoutingUtilizationState   `json:"utilization,omitempty"`
}

// ServiceRoutingDecisionComponents exposes the per-axis score inputs.
type ServiceRoutingDecisionComponents struct {
	Power                   int     `json:"power"`
	Cost                    float64 `json:"cost"`
	CostClass               string  `json:"cost_class,omitempty"`
	LatencyMS               float64 `json:"latency_ms"`
	SpeedTPS                float64 `json:"speed_tps"`
	Utilization             float64 `json:"utilization"`
	SuccessRate             float64 `json:"success_rate"`
	QuotaOK                 bool    `json:"quota_ok"`
	QuotaPercentUsed        int     `json:"quota_percent_used"`
	QuotaTrend              string  `json:"quota_trend,omitempty"`
	Capability              float64 `json:"capability"`
	StickyAffinity          float64 `json:"sticky_affinity"`
	PowerWeightedCapability float64 `json:"power_weighted_capability"`
	PowerHintFit            float64 `json:"power_hint_fit"`
	LatencyWeight           float64 `json:"latency_weight"`
	PlacementBonus          float64 `json:"placement_bonus"`
	QuotaBonus              float64 `json:"quota_bonus"`
	MarginalCostPenalty     float64 `json:"marginal_cost_penalty"`
	AvailabilityPenalty     float64 `json:"availability_penalty"`
	StaleSignalPenalty      float64 `json:"stale_signal_penalty"`
}

type ServiceRoutingStickyState struct {
	KeyPresent     bool    `json:"key_present,omitempty"`
	Assignment     string  `json:"assignment,omitempty"`
	ServerInstance string  `json:"server_instance,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	Bonus          float64 `json:"bonus"`
}

type ServiceRoutingUtilizationState struct {
	Source         string    `json:"source,omitempty"`
	Freshness      string    `json:"freshness,omitempty"`
	ActiveRequests *int      `json:"active_requests,omitempty"`
	QueuedRequests *int      `json:"queued_requests,omitempty"`
	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
	CachePressure  *float64  `json:"cache_pressure,omitempty"`
	ObservedAt     time.Time `json:"observed_at,omitempty"`
}

type ServiceStallData struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// SessionOutcome is the stable coarse result of one accepted Fizeau session.
// Status remains available on ServiceFinalData only as a compatibility field.
type SessionOutcome string

const (
	SessionOutcomeSuccess   SessionOutcome = "success"
	SessionOutcomeFailed    SessionOutcome = "failed"
	SessionOutcomeCancelled SessionOutcome = "cancelled"
	SessionOutcomeTimedOut  SessionOutcome = "timed_out"
)

// TerminalCause is the stable reason a session terminalized. Consumers must
// preserve unfamiliar additive values and must not derive this value from the
// diagnostic Error string.
type TerminalCause string

const (
	TerminalCauseCompleted        TerminalCause = "completed"
	TerminalCauseRouteUnavailable TerminalCause = "route_unavailable"
	TerminalCauseSpawnFailed      TerminalCause = "spawn_failed"
	TerminalCauseHarnessFailed    TerminalCause = "harness_failed"
	TerminalCauseProviderFailed   TerminalCause = "provider_failed"
	TerminalCauseToolLoopFailed   TerminalCause = "tool_loop_failed"
	TerminalCauseIterationLimit   TerminalCause = "iteration_limit"
	TerminalCauseBudgetHalted     TerminalCause = "budget_halted"
	TerminalCauseDeadlineExceeded TerminalCause = "deadline_exceeded"
	TerminalCauseContextCancelled TerminalCause = "context_cancelled"
	TerminalCauseCallerDied       TerminalCause = "caller_died"
	TerminalCauseCleanupFailed    TerminalCause = "cleanup_failed"
	TerminalCauseInternalError    TerminalCause = "internal_error"
)

// SessionStage is the Fizeau-owned lifecycle stage that determined a terminal
// result. Outer workflow stages such as review or landing are not valid here.
type SessionStage string

const (
	SessionStageRouting      SessionStage = "routing"
	SessionStageSpawn        SessionStage = "spawn"
	SessionStageHarness      SessionStage = "harness"
	SessionStageProvider     SessionStage = "provider"
	SessionStageToolLoop     SessionStage = "tool_loop"
	SessionStageTimeout      SessionStage = "timeout"
	SessionStageCancellation SessionStage = "cancellation"
	SessionStageCleanup      SessionStage = "cleanup"
)

type ServiceFinalData struct {
	Status         string                `json:"status"`
	Outcome        SessionOutcome        `json:"outcome"`
	Cause          TerminalCause         `json:"cause"`
	Stage          SessionStage          `json:"stage"`
	PrimaryOutcome SessionOutcome        `json:"primary_outcome,omitempty"`
	PrimaryCause   TerminalCause         `json:"primary_cause,omitempty"`
	PrimaryStage   SessionStage          `json:"primary_stage,omitempty"`
	ExitCode       int                   `json:"exit_code"`
	Error          string                `json:"error,omitempty"`
	FinalText      string                `json:"final_text,omitempty"`
	DurationMS     int64                 `json:"duration_ms"`
	Usage          *ServiceFinalUsage    `json:"usage,omitempty"`
	Warnings       []ServiceFinalWarning `json:"warnings,omitempty"`
	CostUSD        float64               `json:"cost_usd,omitempty"`
	CostSource     CostSource            `json:"cost_source"`
	SessionLogPath string                `json:"session_log_path,omitempty"`
	RoutingActual  *ServiceRoutingActual `json:"routing_actual,omitempty"`
}

// ServiceFinalUsage is the public token-usage payload emitted on service
// final events. Token-count fields are *int so callers can distinguish an
// explicit upstream zero from "harness did not report this dimension":
//
//   - nil pointer  → the harness did not emit this token count (unknown).
//     Consumers MUST NOT treat nil as zero or use it for budgeting.
//   - non-nil *int → the harness emitted this exact value (including 0).
//     Zero means the upstream provider explicitly reported zero usage; it
//     is a real signal, not a gap.
//
// Consumers that aggregate or compare usage across runs should branch on
// presence (nil vs non-nil) before reading the int. Per CONTRACT-003, the
// service preserves provider provenance verbatim — emitters at the harness
// boundary are forbidden from silently substituting zero for unknown.
type ServiceFinalUsage struct {
	InputTokens      *int                         `json:"input_tokens,omitempty"`
	OutputTokens     *int                         `json:"output_tokens,omitempty"`
	CacheReadTokens  *int                         `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int                         `json:"cache_write_tokens,omitempty"`
	CacheTokens      *int                         `json:"cache_tokens,omitempty"`
	ReasoningTokens  *int                         `json:"reasoning_tokens,omitempty"`
	TotalTokens      *int                         `json:"total_tokens,omitempty"`
	Source           string                       `json:"source,omitempty"`
	Fresh            *bool                        `json:"fresh,omitempty"`
	CapturedAt       string                       `json:"captured_at,omitempty"`
	Sources          []ServiceUsageSourceEvidence `json:"sources,omitempty"`
}

type ServiceFinalWarning struct {
	Code    string                       `json:"code"`
	Message string                       `json:"message,omitempty"`
	Sources []ServiceUsageSourceEvidence `json:"sources,omitempty"`
}

type ServiceUsageSourceEvidence struct {
	Source     string                   `json:"source"`
	Fresh      *bool                    `json:"fresh,omitempty"`
	CapturedAt string                   `json:"captured_at,omitempty"`
	Usage      *ServiceUsageTokenCounts `json:"usage,omitempty"`
	Warning    string                   `json:"warning,omitempty"`
}

type ServiceUsageTokenCounts struct {
	InputTokens      *int `json:"input_tokens,omitempty"`
	OutputTokens     *int `json:"output_tokens,omitempty"`
	CacheReadTokens  *int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int `json:"cache_write_tokens,omitempty"`
	CacheTokens      *int `json:"cache_tokens,omitempty"`
	ReasoningTokens  *int `json:"reasoning_tokens,omitempty"`
	TotalTokens      *int `json:"total_tokens,omitempty"`
}

type ServiceRoutingActual struct {
	Harness            string   `json:"harness"`
	Provider           string   `json:"provider,omitempty"`
	ServerInstance     string   `json:"server_instance,omitempty"`
	Model              string   `json:"model"`
	FallbackChainFired []string `json:"fallback_chain_fired,omitempty"`
	FailureClass       string   `json:"failure_class,omitempty"`
	// Power is the catalog-projected power of the actually-dispatched
	// Model (CONTRACT-003 § Catalog Power Projection). 0 means
	// unknown/exact-pin-only/no catalog entry.
	Power int `json:"power,omitempty"`
}

// ServiceDecodedEvent is a typed view of one ServiceEvent. Exactly one payload
// pointer is non-nil for a known event type.
type ServiceDecodedEvent struct {
	Type     string
	Sequence int64
	Time     time.Time
	Metadata map[string]string

	TextDelta        *ServiceTextDeltaData
	ToolCall         *ServiceToolCallData
	ToolResult       *ServiceToolResultData
	Compaction       *ServiceCompactionData
	Progress         *ServiceProgressData
	RoutingDecision  *ServiceRoutingDecisionData
	Stall            *ServiceStallData
	Final            *ServiceFinalData
	Override         *ServiceOverrideData
	RejectedOverride *ServiceOverrideData
}

func DecodeServiceEvent(ev ServiceEvent) (ServiceDecodedEvent, error) {
	decoded := ServiceDecodedEvent{
		Type:     string(ev.Type),
		Sequence: ev.Sequence,
		Time:     ev.Time,
		Metadata: ev.Metadata,
	}
	switch string(ev.Type) {
	case ServiceEventTypeTextDelta:
		var payload ServiceTextDeltaData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.TextDelta = &payload
	case ServiceEventTypeToolCall:
		var payload ServiceToolCallData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.ToolCall = &payload
	case ServiceEventTypeToolResult:
		var payload ServiceToolResultData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.ToolResult = &payload
	case ServiceEventTypeCompaction:
		var payload ServiceCompactionData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.Compaction = &payload
	case ServiceEventTypeProgress:
		var payload ServiceProgressData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.Progress = &payload
	case ServiceEventTypeRoutingDecision:
		var payload ServiceRoutingDecisionData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.RoutingDecision = &payload
	case ServiceEventTypeStall:
		var payload ServiceStallData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.Stall = &payload
	case ServiceEventTypeFinal:
		var payload ServiceFinalData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.Final = &payload
	case ServiceEventTypeOverride:
		var payload ServiceOverrideData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.Override = &payload
	case ServiceEventTypeRejectedOverride:
		var payload ServiceOverrideData
		if err := decodeServicePayload(ev, &payload); err != nil {
			return decoded, err
		}
		decoded.RejectedOverride = &payload
	default:
		return decoded, fmt.Errorf("decode service event %q: unknown type", ev.Type)
	}
	return decoded, nil
}

func decodeServicePayload(ev ServiceEvent, dst any) error {
	if len(ev.Data) == 0 {
		return fmt.Errorf("decode service event %q: empty data", ev.Type)
	}
	if err := json.Unmarshal(ev.Data, dst); err != nil {
		return fmt.Errorf("decode service event %q: %w", ev.Type, err)
	}
	return nil
}

// DrainExecuteResult is a typed aggregate of one Execute event stream.
type DrainExecuteResult struct {
	Events           []ServiceDecodedEvent
	TextDeltas       []ServiceTextDeltaData
	ToolCalls        []ServiceToolCallData
	ToolResults      []ServiceToolResultData
	Compactions      []ServiceCompactionData
	Progresses       []ServiceProgressData
	Stalls           []ServiceStallData
	RoutingDecision  *ServiceRoutingDecisionData
	Override         *ServiceOverrideData
	RejectedOverride *ServiceOverrideData
	Final            *ServiceFinalData

	FinalStatus    string
	Outcome        SessionOutcome
	Cause          TerminalCause
	Stage          SessionStage
	PrimaryOutcome SessionOutcome
	PrimaryCause   TerminalCause
	PrimaryStage   SessionStage
	FinalText      string
	Usage          *ServiceFinalUsage
	Warnings       []ServiceFinalWarning
	CostUSD        float64
	CostSource     CostSource
	SessionLogPath string
	RoutingActual  *ServiceRoutingActual
	TerminalError  string
}

func DrainExecute(ctx context.Context, events <-chan ServiceEvent) (*DrainExecuteResult, error) {
	result := &DrainExecuteResult{}
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				if result.Final == nil {
					return result, errors.New("execute event stream closed without final event")
				}
				return result, nil
			}
			decoded, err := DecodeServiceEvent(ev)
			if err != nil {
				return result, err
			}
			result.append(decoded)
		}
	}
}

func (r *DrainExecuteResult) append(ev ServiceDecodedEvent) {
	r.Events = append(r.Events, ev)
	switch {
	case ev.TextDelta != nil:
		r.TextDeltas = append(r.TextDeltas, *ev.TextDelta)
	case ev.ToolCall != nil:
		r.ToolCalls = append(r.ToolCalls, *ev.ToolCall)
	case ev.ToolResult != nil:
		r.ToolResults = append(r.ToolResults, *ev.ToolResult)
	case ev.Compaction != nil:
		r.Compactions = append(r.Compactions, *ev.Compaction)
	case ev.Progress != nil:
		r.Progresses = append(r.Progresses, *ev.Progress)
	case ev.RoutingDecision != nil:
		r.RoutingDecision = ev.RoutingDecision
	case ev.Override != nil:
		r.Override = ev.Override
	case ev.RejectedOverride != nil:
		r.RejectedOverride = ev.RejectedOverride
	case ev.Stall != nil:
		r.Stalls = append(r.Stalls, *ev.Stall)
	case ev.Final != nil:
		r.Final = ev.Final
		r.FinalStatus = ev.Final.Status
		r.Outcome = ev.Final.Outcome
		r.Cause = ev.Final.Cause
		r.Stage = ev.Final.Stage
		r.PrimaryOutcome = ev.Final.PrimaryOutcome
		r.PrimaryCause = ev.Final.PrimaryCause
		r.PrimaryStage = ev.Final.PrimaryStage
		r.FinalText = ev.Final.FinalText
		r.Usage = ev.Final.Usage
		r.Warnings = ev.Final.Warnings
		cost, source := ev.Final.CostMeasurement()
		r.CostUSD = 0
		if cost != nil {
			r.CostUSD = *cost
		}
		r.CostSource = source
		r.SessionLogPath = ev.Final.SessionLogPath
		r.RoutingActual = ev.Final.RoutingActual
		r.TerminalError = ev.Final.Error
	}
}
