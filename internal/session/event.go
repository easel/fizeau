package session

import (
	"encoding/json"
	"time"

	agent "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
)

// SessionStartData is the data payload for a session.start event.
type SessionStartData struct {
	Provider               string                  `json:"provider"`
	Model                  string                  `json:"model"`
	SelectedProvider       string                  `json:"selected_provider,omitempty"`
	SelectedEndpoint       string                  `json:"selected_endpoint,omitempty"`
	SelectedServerInstance string                  `json:"selected_server_instance,omitempty"`
	SelectedRoute          string                  `json:"selected_route,omitempty"`
	Sticky                 RoutingStickyState      `json:"sticky,omitempty"`
	Utilization            RoutingUtilizationState `json:"utilization,omitempty"`
	RequestedHarness       string                  `json:"requested_harness,omitempty"`
	ResolvedHarness        string                  `json:"resolved_harness,omitempty"`
	HarnessSource          string                  `json:"harness_source,omitempty"`
	RequestedModel         string                  `json:"requested_model,omitempty"`
	ResolvedModel          string                  `json:"resolved_model,omitempty"`
	Reasoning              agent.Reasoning         `json:"reasoning,omitempty"`
	ReasoningIntent        agent.Reasoning         `json:"reasoning_intent,omitempty"`
	ReasoningEmitted       agent.Reasoning         `json:"reasoning_emitted,omitempty"`
	ResolvedReasoning      agent.Reasoning         `json:"resolved_reasoning,omitempty"`
	ReasoningSource        string                  `json:"reasoning_source,omitempty"`
	AttemptedProviders     []string                `json:"attempted_providers,omitempty"`
	FailoverCount          int                     `json:"failover_count,omitempty"`
	WorkDir                string                  `json:"work_dir"`
	MaxIterations          int                     `json:"max_iterations"`
	Prompt                 string                  `json:"prompt"`
	SystemPrompt           string                  `json:"system_prompt,omitempty"`
	Metadata               map[string]string       `json:"metadata,omitempty"`
}

// LLMRequestData is the data payload for an llm.request event.
type LLMRequestData struct {
	Messages          []agent.Message `json:"messages"`
	Tools             []agent.ToolDef `json:"tools,omitempty"`
	Model             string          `json:"model,omitempty"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	TopK              *int            `json:"top_k,omitempty"`
	MinP              *float64        `json:"min_p,omitempty"`
	RepetitionPenalty *float64        `json:"repetition_penalty,omitempty"`
	MaxTokens         int             `json:"max_tokens,omitempty"`
	Seed              int64           `json:"seed,omitempty"`
	Stop              []string        `json:"stop,omitempty"`
	Reasoning         agent.Reasoning `json:"reasoning,omitempty"`
	ReasoningIntent   agent.Reasoning `json:"reasoning_intent,omitempty"`
	CachePolicy       string          `json:"cache_policy,omitempty"`
	// SamplingSource is the comma-separated list of resolution layers that
	// supplied non-nil sampler fields, in chain order. Values:
	// "catalog", "provider_config", "cli", or combinations like
	// "catalog,provider_config". Empty when all sampler fields were nil
	// (server defaults applied). See ADR-007 §5.
	SamplingSource string `json:"sampling_source,omitempty"`
}

// LLMResponseData is the data payload for an llm.response event.
type LLMResponseData struct {
	Content          string           `json:"content,omitempty"`
	ToolCalls        []agent.ToolCall `json:"tool_calls,omitempty"`
	Usage            agent.TokenUsage `json:"usage"`
	CostUSD          float64          `json:"cost_usd"`
	LatencyMs        int64            `json:"latency_ms"`
	Model            string           `json:"model"`
	FinishReason     string           `json:"finish_reason"`
	ReasoningEmitted agent.Reasoning  `json:"reasoning_emitted,omitempty"`
}

// ToolCallData is the data payload for a tool.call event.
type ToolCallData struct {
	Tool       string          `json:"tool"`
	Input      json.RawMessage `json:"input"`
	Output     string          `json:"output"`
	DurationMs int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
}

// SessionEndData is the data payload for a session.end event.
type SessionEndData struct {
	Status                 agent.Status             `json:"status"`
	Outcome                harnesses.SessionOutcome `json:"outcome,omitempty"`
	Cause                  harnesses.TerminalCause  `json:"cause,omitempty"`
	Stage                  harnesses.SessionStage   `json:"stage,omitempty"`
	PrimaryOutcome         harnesses.SessionOutcome `json:"primary_outcome,omitempty"`
	PrimaryCause           harnesses.TerminalCause  `json:"primary_cause,omitempty"`
	PrimaryStage           harnesses.SessionStage   `json:"primary_stage,omitempty"`
	Output                 string                   `json:"output"`
	Tokens                 agent.TokenUsage         `json:"tokens"`
	CostUSD                *float64                 `json:"cost_usd,omitempty"`
	CostSource             harnesses.CostSource     `json:"cost_source"`
	DurationMs             int64                    `json:"duration_ms"`
	Model                  string                   `json:"model,omitempty"`
	SelectedProvider       string                   `json:"selected_provider,omitempty"`
	SelectedEndpoint       string                   `json:"selected_endpoint,omitempty"`
	SelectedServerInstance string                   `json:"selected_server_instance,omitempty"`
	SelectedRoute          string                   `json:"selected_route,omitempty"`
	Sticky                 RoutingStickyState       `json:"sticky,omitempty"`
	Utilization            RoutingUtilizationState  `json:"utilization,omitempty"`
	RequestedHarness       string                   `json:"requested_harness,omitempty"`
	ResolvedHarness        string                   `json:"resolved_harness,omitempty"`
	HarnessSource          string                   `json:"harness_source,omitempty"`
	RequestedModel         string                   `json:"requested_model,omitempty"`
	ResolvedModel          string                   `json:"resolved_model,omitempty"`
	Reasoning              agent.Reasoning          `json:"reasoning,omitempty"`
	ReasoningIntent        agent.Reasoning          `json:"reasoning_intent,omitempty"`
	ReasoningEmitted       agent.Reasoning          `json:"reasoning_emitted,omitempty"`
	ResolvedReasoning      agent.Reasoning          `json:"resolved_reasoning,omitempty"`
	ReasoningSource        string                   `json:"reasoning_source,omitempty"`
	AttemptedProviders     []string                 `json:"attempted_providers,omitempty"`
	FailoverCount          int                      `json:"failover_count,omitempty"`
	Metadata               map[string]string        `json:"metadata,omitempty"`
	Error                  string                   `json:"error,omitempty"`
	// ProcessOutcome surfaces the FEAT-005 §27 / SD-010 failure-taxonomy
	// label for terminal events that map to a first-class outcome
	// distinct from Status (e.g. "budget_halted"). Empty when no outcome
	// label applies — Status alone is then authoritative.
	ProcessOutcome string `json:"process_outcome,omitempty"`
	// CostCapUSD echoes the per-run cost cap (when configured) onto the
	// session.end record so post-hoc tooling can report cost-vs-cap without
	// reading the run's request envelope. Zero or nil when no cap was set.
	CostCapUSD *float64 `json:"cost_cap_usd,omitempty"`
}

// RoutingStickyState summarizes sticky routing behavior without exposing
// the raw sticky key.
type RoutingStickyState struct {
	KeyPresent     bool    `json:"key_present,omitempty"`
	Assignment     string  `json:"assignment,omitempty"`
	ServerInstance string  `json:"server_instance,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	Bonus          float64 `json:"bonus"`
}

// RoutingUtilizationState carries the live endpoint sample that informed a
// routing decision.
type RoutingUtilizationState struct {
	Source         string    `json:"source,omitempty"`
	Freshness      string    `json:"freshness,omitempty"`
	ActiveRequests *int      `json:"active_requests,omitempty"`
	QueuedRequests *int      `json:"queued_requests,omitempty"`
	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
	CachePressure  *float64  `json:"cache_pressure,omitempty"`
	ObservedAt     time.Time `json:"observed_at,omitempty"`
}

// NewEvent creates an Event with the given type and data, auto-assigning
// the timestamp.
func NewEvent(sessionID string, seq int, eventType agent.EventType, data any) agent.Event {
	raw, _ := json.Marshal(data)
	return agent.Event{
		SessionID: sessionID,
		Seq:       seq,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      raw,
	}
}
