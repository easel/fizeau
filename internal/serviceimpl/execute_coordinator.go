package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/processlifecycle"
	"github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/tool"
	"github.com/easel/fizeau/internal/transcript"
)

// ExecuteDecision is the API-neutral routing identity consumed by the
// service-owned Execute coordinator. The full public routing trace is carried
// separately as pre-marshaled RoutingDecisionData; Candidates contains only
// the subset required by native provider failover.
type ExecuteDecision struct {
	Harness               string
	Provider              string
	Endpoint              string
	ServerInstance        string
	Model                 string
	Reason                string
	Power                 int
	SelectedContextWindow int
	SelectedContextSource string
	Candidates            []NativeRouteCandidate
}

// ExecuteRequest is the API-neutral projection of one already-resolved public
// Execute request. Root owns public validation and conversion; this package
// owns accepted-run orchestration, runner invocation, event delivery, and
// terminal ordering.
type ExecuteRequest struct {
	SessionID string
	// RouteRunner is the authority-issued exact-key binding for the resolved
	// subprocess route. RouteRunnerError preserves a first-bind construction
	// failure for terminal dispatch projection.
	RouteRunner      harnesses.RouteRunnerBinding
	RouteRunnerError error

	Prompt            string
	SystemPrompt      string
	RequestedModel    string
	RequestedProvider string
	RequestedHarness  string
	WorkDir           string

	Temperature       *float32
	TopP              *float64
	TopK              *int
	MinP              *float64
	RepetitionPenalty *float64
	Seed              *int64
	SamplingSource    string
	Reasoning         reasoning.Reasoning
	NoStream          bool
	Permissions       string
	Tools             []agentcore.Tool
	ToolPreset        string
	PlanningMode      bool

	MaxIterations           int
	MaxTokens               int
	ReasoningByteLimit      int
	CompactionContextWindow int
	CompactionReserveTokens int

	Timeout         time.Duration
	IdleTimeout     time.Duration
	ProviderTimeout time.Duration
	CleanupTimeout  time.Duration
	CachePolicy     string
	CostCapUSD      float64

	StallMaxReadOnlyIterations *int

	SessionLogDir    string
	LifecycleBaseDir string
	Metadata         map[string]string
	FinalMetadata    map[string]string
	CollisionWarning *harnesses.FinalWarning

	Decision            ExecuteDecision
	RoutingDecisionData json.RawMessage
	RouteProgress       transcript.ProgressPayload
	OverridePayload     json.RawMessage
}

// ExecuteSessionLog is the API-neutral session-log seam used by the Execute
// coordinator. A root adapter may retain public request/decision projection
// while internal orchestration owns ordering and lifecycle.
type ExecuteSessionLog interface {
	Enabled() bool
	Path() string
	EndWritten() bool
	ProgressIntervalMS(time.Time) int64
	WriteCoreEvent(agentcore.Event)
	WriteOverride(agentcore.EventType, json.RawMessage)
	WriteEnd(map[string]string, harnesses.FinalData)
	Close()
}

// ExecutePorts contains service-state reads/writes and build-tag observation
// hooks. Runner selection, runner invocation, event emission, and terminal
// finalization deliberately do not cross this seam.
type ExecutePorts struct {
	OpenSessionLog func() ExecuteSessionLog

	ResolveNativeProvider      func(NativeProviderRequest) NativeProviderResolution
	ProviderNotConfiguredError func(NativeProviderRequest, NativeDecision) string
	ProjectContextCapacity     func(harnesses.ContextCapacityData) any

	ObserveRouteAttempt        func(harnesses.FinalData)
	ObserveWrappedRouteAttempt func(harnesses.FinalData) error
	ObserveTokenUsage          func(provider string, tokens int, at time.Time)
	CatalogPower               func(model string) int
	RecordOverrideOutcome      func(status string)
	ObserveSubprocessDispatch  func(harnesses.Harness)

	ToolWiringHook          func(harness string, toolNames []string)
	PromptAssertionHook     func(systemPrompt, prompt string, contextFiles []string)
	CompactionAssertionHook func(messagesBefore, messagesAfter, tokensFreed int)
}

// ExecuteCoordinator owns accepted Execute runs without depending on root
// public contract types.
type ExecuteCoordinator struct {
	Hub      *SessionHub
	Registry *harnesses.Registry
}

// RunResolved starts one already-resolved Execute request. The caller must
// register req.SessionID in Hub before calling so pre-dispatch route failures
// and accepted runs share one TailSessionLog identity.
func (c ExecuteCoordinator) RunResolved(ctx context.Context, req ExecuteRequest, ports ExecutePorts) <-chan harnesses.Event {
	outer := make(chan harnesses.Event, 64)
	inner := c.wrapExecuteStream(req, ports, outer)
	go c.runResolved(ctx, req, ports, inner)
	return outer
}

// RoutingFailure returns the terminal-only stream for an ordinary route
// resolution failure. Typed quota and explicit-pin failures are rejected by
// the root boundary before calling this method. The final is retained for both
// the direct Execute consumer and TailSessionLog subscribers.
func (c ExecuteCoordinator) RoutingFailure(sessionID string, metadata map[string]string, errMsg string) <-chan harnesses.Event {
	out := make(chan harnesses.Event, 1)
	go func() {
		defer close(out)
		final := ClassifyTerminalFinal(harnesses.FinalData{
			Status: "failed",
			Error:  errMsg,
		}, TerminalOriginRouting, nil)
		raw, err := json.Marshal(final)
		if err != nil {
			raw = []byte(`{"status":"failed","error":"marshal final"}`)
		}
		ev := harnesses.Event{
			Type:     harnesses.EventTypeFinal,
			Sequence: 0,
			Time:     time.Now().UTC(),
			Metadata: metadata,
			Data:     raw,
		}
		out <- ev
		if c.Hub != nil {
			c.Hub.BroadcastEvent(sessionID, ev)
			c.Hub.CloseSession(sessionID, ev)
		}
	}()
	return out
}

type executeRunState struct {
	req   ExecuteRequest
	ports ExecutePorts
	out   chan<- harnesses.Event
	seq   atomic.Int64
	start time.Time
	log   ExecuteSessionLog
}

func (c ExecuteCoordinator) runResolved(ctx context.Context, req ExecuteRequest, ports ExecutePorts, out chan<- harnesses.Event) {
	defer close(out)

	state := &executeRunState{
		req:   req,
		ports: ports,
		out:   out,
		start: time.Now(),
	}
	if ports.OpenSessionLog != nil {
		state.log = ports.OpenSessionLog()
	}
	defer state.closeSessionLog(ctx)

	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	if len(req.RoutingDecisionData) > 0 {
		state.emitRaw(harnesses.EventTypeRoutingDecision, req.FinalMetadata, req.RoutingDecisionData)
	}
	state.emitProgress(req.RouteProgress)

	c.dispatch(runCtx, state)
}

func (c ExecuteCoordinator) dispatch(ctx context.Context, state *executeRunState) {
	req := state.req
	DispatchExecuteRun(ctx, ExecuteDispatchRequest{
		Decision:         runnerDecision(req.Decision),
		RouteRunner:      req.RouteRunner,
		RouteRunnerError: req.RouteRunnerError,
		Started:          state.start,
	}, ExecuteDispatchCallbacks{
		RunNative: func(ctx context.Context) {
			state.runNative(ctx)
		},
		RunSubprocess: func(ctx context.Context, runner harnesses.Harness) {
			if state.ports.ObserveSubprocessDispatch != nil {
				state.ports.ObserveSubprocessDispatch(runner)
			}
			state.runSubprocess(ctx, runner)
		},
		RunVirtual: func(ctx context.Context) {
			state.runVirtual(ctx)
		},
		RunScript: func(ctx context.Context) {
			state.runScript(ctx)
		},
		IsHTTPProvider: func(harness string) bool {
			if c.Registry == nil {
				return false
			}
			cfg, ok := c.Registry.Get(harness)
			return ok && cfg.IsHTTPProvider
		},
		Finalize: func(final harnesses.FinalData) {
			state.observeRouteAttempt(final)
			state.commitFinal(ctx, final, TerminalOriginSpawn)
		},
	})
}

func (state *executeRunState) closeSessionLog(ctx context.Context) {
	if state.log == nil {
		return
	}
	if !state.log.EndWritten() {
		final := ClassifyTerminalFinal(harnesses.FinalData{
			Status:     "cancelled",
			Error:      "session ended without final event",
			DurationMS: time.Since(state.start).Milliseconds(),
			RoutingActual: &harnesses.RoutingActual{
				Harness:        state.req.Decision.Harness,
				Provider:       state.req.Decision.Provider,
				ServerInstance: state.req.Decision.ServerInstance,
				Model:          state.req.Decision.Model,
			},
		}, terminalOrigin(state.req.Decision), ctx.Err())
		state.log.WriteEnd(state.req.FinalMetadata, final)
	}
	state.log.Close()
}

func (state *executeRunState) runVirtual(ctx context.Context) {
	progress := transcript.NewSubprocessProgressState(state.req.Prompt, state.req.SystemPrompt)
	state.emitProgress(progress.NoteRequestStart())
	result := RunVirtual(ctx, ExecuteRunnerRequest{
		Prompt:   state.req.Prompt,
		Metadata: state.req.Metadata,
		Decision: runnerDecision(state.req.Decision),
		Started:  state.start,
	})
	if result.EmitText {
		state.emitJSON(harnesses.EventTypeTextDelta, state.req.Metadata, harnesses.TextDeltaData{Text: result.Text})
	}
	if finalProgress := progress.NoteResponseComplete(result.Final); finalProgress != nil {
		state.emitProgress(*finalProgress)
	}
	state.observeRouteAttempt(result.Final)
	state.commitFinal(ctx, result.Final, TerminalOriginHarness)
}

func (state *executeRunState) runScript(ctx context.Context) {
	progress := transcript.NewSubprocessProgressState(state.req.Prompt, state.req.SystemPrompt)
	state.emitProgress(progress.NoteRequestStart())
	result := RunScript(ctx, ExecuteRunnerRequest{
		Prompt:   state.req.Prompt,
		Metadata: state.req.Metadata,
		Decision: runnerDecision(state.req.Decision),
		Started:  state.start,
	})
	if result.EmitText {
		state.emitJSON(harnesses.EventTypeTextDelta, state.req.Metadata, harnesses.TextDeltaData{Text: result.Text})
	}
	if finalProgress := progress.NoteResponseComplete(result.Final); finalProgress != nil {
		state.emitProgress(*finalProgress)
	}
	state.observeRouteAttempt(result.Final)
	state.commitFinal(ctx, result.Final, TerminalOriginHarness)
}

func (state *executeRunState) runNative(ctx context.Context) {
	progress := transcript.NewNativeProgressState()
	observeAgentEvent := func(ev agentcore.Event) {
		if state.log != nil {
			state.log.WriteCoreEvent(ev)
		}
		switch ev.Type {
		case agentcore.EventLLMRequest:
			var payload transcript.NativeLLMRequestPayload
			if err := json.Unmarshal(ev.Data, &payload); err == nil {
				state.emitProgress(progress.NoteRequest(payload))
			}
		case agentcore.EventLLMResponse:
			var payload transcript.NativeLLMResponsePayload
			if err := json.Unmarshal(ev.Data, &payload); err == nil {
				state.emitProgress(progress.NoteResponse(payload))
			}
		case agentcore.EventToolCall:
			var payload transcript.NativeToolCallPayload
			_ = json.Unmarshal(ev.Data, &payload)
			input := payload.Input
			if input == nil {
				input, _ = json.Marshal(map[string]any{"tool": payload.Tool})
			}
			callID := fmt.Sprintf("call-%d", ev.Seq)
			_, complete := progress.NoteToolCall(callID, payload)
			state.emitJSON(harnesses.EventTypeToolCall, state.req.Metadata, harnesses.ToolCallData{ID: callID, Name: payload.Tool, Input: input})
			state.emitJSON(harnesses.EventTypeToolResult, state.req.Metadata, harnesses.ToolResultData{ID: callID, Output: payload.Output, Error: payload.Error, DurationMS: payload.DurationMS})
			state.emitProgress(complete)
		case agentcore.EventCompactionEnd:
			var payload transcript.NativeCompactionPayload
			_ = json.Unmarshal(ev.Data, &payload)
			state.emitJSON(harnesses.EventTypeCompaction, state.req.Metadata, map[string]any{
				"messages_before": payload.MessagesBefore,
				"messages_after":  payload.MessagesAfter,
				"tokens_freed":    payload.TokensBefore - payload.TokensAfter,
			})
			compactionProgress, contextProgress := progress.NoteCompaction(payload)
			state.emitProgress(compactionProgress)
			state.emitProgress(contextProgress)
		}
	}

	tools := state.req.Tools
	if tools == nil {
		tools = tool.BuiltinToolsForPreset(state.req.WorkDir, state.req.ToolPreset, tool.BashOutputFilterConfig{})
	}
	nativeReq := NativeRequest{
		Prompt:                    state.req.Prompt,
		SystemPrompt:              state.req.SystemPrompt,
		Model:                     state.req.RequestedModel,
		Provider:                  state.req.RequestedProvider,
		Harness:                   state.req.RequestedHarness,
		WorkDir:                   state.req.WorkDir,
		Temperature:               state.req.Temperature,
		TopP:                      state.req.TopP,
		TopK:                      state.req.TopK,
		MinP:                      state.req.MinP,
		RepetitionPenalty:         state.req.RepetitionPenalty,
		Seed:                      state.req.Seed,
		SamplingSource:            state.req.SamplingSource,
		Reasoning:                 state.req.Reasoning,
		NoStream:                  state.req.NoStream,
		Permissions:               state.req.Permissions,
		Tools:                     tools,
		ToolPreset:                state.req.ToolPreset,
		PlanningMode:              state.req.PlanningMode,
		MaxIterations:             state.req.MaxIterations,
		MaxTokens:                 state.req.MaxTokens,
		ReasoningByteLimit:        state.req.ReasoningByteLimit,
		CompactionReserveTokens:   state.req.CompactionReserveTokens,
		ProviderTimeout:           state.req.ProviderTimeout,
		Timeout:                   state.req.Timeout,
		CachePolicy:               state.req.CachePolicy,
		CostCapUSD:                state.req.CostCapUSD,
		StallMaxReadOnlyIteration: state.req.StallMaxReadOnlyIterations,
		Metadata:                  state.req.Metadata,
		Decision:                  nativeDecisionFromExecute(state.req.Decision),
		Started:                   state.start,
		SessionID:                 state.req.SessionID,
	}
	projectExecuteContextToNative(&nativeReq, state.req)
	RunNative(ctx, nativeReq, NativeCallbacks{
		ResolveProvider:            state.ports.ResolveNativeProvider,
		ProviderNotConfiguredError: state.ports.ProviderNotConfiguredError,
		ObserveAgentEvent:          observeAgentEvent,
		EmitEvent: func(eventType harnesses.EventType, payload any) {
			if eventType == harnesses.EventTypeContextCapacity && state.ports.ProjectContextCapacity != nil {
				if capacity, ok := payload.(harnesses.ContextCapacityData); ok {
					payload = state.ports.ProjectContextCapacity(capacity)
				}
			}
			state.emitJSON(eventType, state.req.Metadata, payload)
		},
		BeforeFinal: func(final harnesses.FinalData) {
			if finalProgress := progress.NoteFinal(final); finalProgress != nil {
				state.emitProgress(*finalProgress)
			}
		},
		Finalize: func(final harnesses.FinalData, origin TerminalOrigin) {
			if origin != TerminalOriginContextCapacity {
				state.observeRouteAttempt(final)
			}
			state.commitFinal(ctx, final, origin)
		},
		ToolWiringHook:          state.ports.ToolWiringHook,
		PromptAssertionHook:     state.ports.PromptAssertionHook,
		CompactionAssertionHook: state.ports.CompactionAssertionHook,
		ObserveTokenUsage:       state.ports.ObserveTokenUsage,
	})
}

// projectExecuteContextToNative carries the already-resolved selected-route
// context evidence and the raw public override across the coordinator/native
// seam without interpreting either value.
func projectExecuteContextToNative(dst *NativeRequest, req ExecuteRequest) {
	dst.SelectedContextWindow = req.Decision.SelectedContextWindow
	dst.SelectedContextSource = req.Decision.SelectedContextSource
	dst.CompactionContextWindow = req.CompactionContextWindow
}

func (state *executeRunState) runSubprocess(ctx context.Context, runner harnesses.Harness) {
	progress := transcript.NewSubprocessProgressState(state.req.Prompt, state.req.SystemPrompt)
	stateDir, err := processlifecycle.StateDirectory(state.req.LifecycleBaseDir)
	if err != nil {
		stateDir = ""
	}
	logPath := ""
	if state.log != nil {
		logPath = state.log.Path()
	}
	RunSubprocess(ctx, SubprocessRequest{
		Prompt:            state.req.Prompt,
		SystemPrompt:      state.req.SystemPrompt,
		WorkDir:           state.req.WorkDir,
		Permissions:       state.req.Permissions,
		Temperature:       state.req.Temperature,
		Seed:              state.req.Seed,
		Reasoning:         state.req.Reasoning,
		Timeout:           state.req.Timeout,
		IdleTimeout:       state.req.IdleTimeout,
		SessionLogDir:     state.req.SessionLogDir,
		SessionID:         state.req.SessionID,
		LifecycleStateDir: stateDir,
		CleanupTimeout:    state.req.CleanupTimeout,
		Metadata:          state.req.Metadata,
		Decision:          runnerDecision(state.req.Decision),
		Started:           state.start,
		SessionLogPath:    logPath,
	}, runner, SubprocessCallbacks{
		BeforeExecute: func() {
			state.emitProgress(progress.NoteRequestStart())
		},
		ObserveFinal: state.ports.ObserveWrappedRouteAttempt,
		ObserveEvent: func(ev harnesses.Event) harnesses.Event {
			ev = progress.AnnotateToolResultDuration(ev)
			if payload, ok := progress.NoteEvent(ev); ok && ev.Type != harnesses.EventTypeProgress {
				state.emitProgress(payload)
			}
			if ev.Type == harnesses.EventTypeFinal {
				if payload, ok := progress.NoteFinalEvent(ev); ok {
					state.emitProgress(payload)
				}
			}
			return ev
		},
		EmitEvent: func(ev harnesses.Event) bool {
			ev.Sequence = state.seq.Add(1) - 1
			select {
			case state.out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		},
		CommitFinal: func(ev harnesses.Event, final harnesses.FinalData) {
			state.commitSubprocessFinal(ev, final)
		},
	})
}

func (state *executeRunState) observeRouteAttempt(final harnesses.FinalData) {
	if state.ports.ObserveRouteAttempt != nil {
		state.ports.ObserveRouteAttempt(final)
	}
}

func (state *executeRunState) commitFinal(ctx context.Context, final harnesses.FinalData, origin TerminalOrigin) {
	final = ClassifyTerminalFinal(final, origin, ctx.Err())
	if state.log != nil && state.log.Path() != "" {
		final.SessionLogPath = state.log.Path()
	}
	if final.RoutingActual != nil && final.RoutingActual.Power == 0 && state.ports.CatalogPower != nil {
		final.RoutingActual.Power = state.ports.CatalogPower(final.RoutingActual.Model)
	}
	if state.req.CollisionWarning != nil {
		final.Warnings = append(final.Warnings, *state.req.CollisionWarning)
	}
	state.writeTerminal(state.req.FinalMetadata, final)
	state.emitFinal(state.req.FinalMetadata, time.Time{}, final)
}

// commitSubprocessFinal accepts a final that RunSubprocess has already
// classified and possibly superseded with cleanup failure. Reclassification
// here would erase the primary terminal tuple.
func (state *executeRunState) commitSubprocessFinal(ev harnesses.Event, final harnesses.FinalData) {
	meta := ev.Metadata
	if meta == nil {
		meta = state.req.Metadata
	}
	state.writeTerminal(meta, final)
	state.emitFinal(meta, ev.Time, final)
}

func (state *executeRunState) writeTerminal(meta map[string]string, final harnesses.FinalData) {
	if state.log == nil {
		return
	}
	if len(state.req.OverridePayload) > 0 {
		state.log.WriteOverride(agentcore.EventOverride, state.req.OverridePayload)
	}
	state.log.WriteEnd(meta, final)
}

func (state *executeRunState) emitFinal(meta map[string]string, at time.Time, final harnesses.FinalData) {
	raw, err := json.Marshal(final)
	if err != nil {
		raw = []byte(`{"status":"failed","error":"marshal final"}`)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	state.out <- harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: state.seq.Add(1) - 1,
		Time:     at,
		Metadata: meta,
		Data:     raw,
	}
}

func (state *executeRunState) emitJSON(eventType harnesses.EventType, meta map[string]string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	state.emitRaw(eventType, meta, raw)
}

func (state *executeRunState) emitRaw(eventType harnesses.EventType, meta map[string]string, raw json.RawMessage) {
	ev := harnesses.Event{
		Type:     eventType,
		Sequence: state.seq.Add(1) - 1,
		Time:     time.Now().UTC(),
		Metadata: meta,
		Data:     raw,
	}
	select {
	case state.out <- ev:
	case <-time.After(time.Second):
	}
}

func (state *executeRunState) emitProgress(payload transcript.ProgressPayload) {
	now := time.Now().UTC()
	if state.log != nil {
		payload.SinceLastMS = state.log.ProgressIntervalMS(now)
	}
	lineLimit := transcript.DefaultLineLimit
	if payload.Phase == "tool" && payload.Command != "" {
		lineLimit = transcript.ExceptionalToolLineLimit
	}
	transcript.FillProgressIdentity(&payload, state.req.SessionID, state.req.Metadata, lineLimit)
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	seq := state.seq.Add(1) - 1
	ev := harnesses.Event{
		Type:     harnesses.EventTypeProgress,
		Sequence: seq,
		Time:     now,
		Metadata: state.req.Metadata,
		Data:     raw,
	}
	select {
	case state.out <- ev:
	case <-time.After(time.Second):
	}
	if state.log != nil {
		state.log.WriteCoreEvent(agentcore.Event{
			SessionID: state.req.SessionID,
			Seq:       int(seq),
			Type:      agentcore.EventType(harnesses.EventTypeProgress),
			Timestamp: now,
			Data:      raw,
		})
	}
}

func (c ExecuteCoordinator) wrapExecuteStream(req ExecuteRequest, ports ExecutePorts, outer chan harnesses.Event) chan harnesses.Event {
	inner := make(chan harnesses.Event, 64)
	go func() {
		defer close(outer)
		var lastFinal harnesses.Event
		overrideEmitted := false
		for ev := range inner {
			if ev.Type == harnesses.EventTypeFinal && len(req.OverridePayload) > 0 && !overrideEmitted {
				if overrideEv, status, ok := makeExecuteOverrideEvent(req, ev); ok {
					overrideEmitted = true
					if ports.RecordOverrideOutcome != nil {
						ports.RecordOverrideOutcome(status)
					}
					outer <- overrideEv
					if c.Hub != nil {
						c.Hub.BroadcastEvent(req.SessionID, overrideEv)
					}
				}
			}
			if ev.Type == harnesses.EventTypeFinal {
				outer <- ev
			} else {
				select {
				case outer <- ev:
				case <-time.After(5 * time.Second):
				}
			}
			if c.Hub != nil {
				c.Hub.BroadcastEvent(req.SessionID, ev)
			}
			if ev.Type == harnesses.EventTypeFinal {
				lastFinal = ev
			}
		}
		if c.Hub != nil {
			c.Hub.CloseSession(req.SessionID, lastFinal)
		}
	}()
	return inner
}

func makeExecuteOverrideEvent(req ExecuteRequest, finalEv harnesses.Event) (harnesses.Event, string, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(req.OverridePayload, &payload); err != nil {
		return harnesses.Event{}, "", false
	}
	var final harnesses.FinalData
	if err := json.Unmarshal(finalEv.Data, &final); err != nil {
		return harnesses.Event{}, "", false
	}
	cost, costSource := normalizeProjectedFinalCost(final.FinalCostUSD, final.FinalCostSource)
	outcome, err := json.Marshal(struct {
		Status     string               `json:"status"`
		CostUSD    *float64             `json:"cost_usd,omitempty"`
		CostSource harnesses.CostSource `json:"cost_source"`
		DurationMS int64                `json:"duration_ms"`
	}{Status: final.Status, CostUSD: cost, CostSource: costSource, DurationMS: final.DurationMS})
	if err != nil {
		return harnesses.Event{}, "", false
	}
	payload["outcome"] = outcome
	raw, err := json.Marshal(payload)
	if err != nil {
		return harnesses.Event{}, "", false
	}
	seq := finalEv.Sequence
	if seq > 0 {
		seq--
	}
	at := finalEv.Time
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return harnesses.Event{
		Type:     harnesses.EventType("override"),
		Sequence: seq,
		Time:     at,
		Metadata: req.Metadata,
		Data:     raw,
	}, final.Status, true
}

func runnerDecision(decision ExecuteDecision) ExecuteRunnerDecision {
	return ExecuteRunnerDecision{
		Harness:        decision.Harness,
		Provider:       decision.Provider,
		Endpoint:       decision.Endpoint,
		ServerInstance: decision.ServerInstance,
		Model:          decision.Model,
	}
}

func nativeDecisionFromExecute(decision ExecuteDecision) NativeDecision {
	return NativeDecision{
		Harness:               decision.Harness,
		Provider:              decision.Provider,
		ServerInstance:        decision.ServerInstance,
		Model:                 decision.Model,
		SelectedContextWindow: decision.SelectedContextWindow,
		SelectedContextSource: decision.SelectedContextSource,
		Candidates:            append([]NativeRouteCandidate(nil), decision.Candidates...),
	}
}

func terminalOrigin(decision ExecuteDecision) TerminalOrigin {
	if decision.Harness == "fiz" || decision.Harness == "" {
		return TerminalOriginProvider
	}
	return TerminalOriginHarness
}
