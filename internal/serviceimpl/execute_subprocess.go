package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/reasoning"
)

// SubprocessRequest is the API-neutral request data needed by subprocess
// harness runner implementations.
type SubprocessRequest struct {
	Prompt         string
	SystemPrompt   string
	WorkDir        string
	Permissions    string
	Temperature    *float32
	Seed           *int64
	Reasoning      reasoning.Reasoning
	Timeout        time.Duration
	IdleTimeout    time.Duration
	SessionLogDir  string
	Metadata       map[string]string
	Decision       ExecuteRunnerDecision
	Started        time.Time
	SessionLogPath string
}

// SubprocessCallbacks bridge service-owned event/progress/session-log behavior
// without importing root public service types.
type SubprocessCallbacks struct {
	BeforeExecute func()
	ObserveEvent  func(harnesses.Event) harnesses.Event
	EmitEvent     func(harnesses.Event) bool
	Finalize      func(harnesses.FinalData)
	WriteEnd      func(map[string]string, harnesses.FinalData)
}

// RunSubprocess executes a subprocess harness and forwards its event stream.
func RunSubprocess(ctx context.Context, req SubprocessRequest, runner harnesses.Harness, cb SubprocessCallbacks) {
	hReq := harnesses.ExecuteRequest{
		Prompt:        req.Prompt,
		SystemPrompt:  req.SystemPrompt,
		Provider:      req.Decision.Provider,
		Model:         req.Decision.Model,
		WorkDir:       req.WorkDir,
		Permissions:   req.Permissions,
		Temperature:   subprocessTemperature(req.Temperature),
		Seed:          subprocessSeed(req.Seed),
		Reasoning:     adapterReasoning(req.Reasoning),
		Timeout:       req.Timeout,
		IdleTimeout:   req.IdleTimeout,
		SessionLogDir: req.SessionLogDir,
		Metadata:      req.Metadata,
	}
	if cb.BeforeExecute != nil {
		cb.BeforeExecute()
	}
	in, err := runner.Execute(ctx, hReq)
	if err != nil {
		finalizeSubprocess(cb, harnesses.FinalData{
			Status:     "failed",
			Error:      err.Error(),
			DurationMS: time.Since(req.Started).Milliseconds(),
			RoutingActual: &harnesses.RoutingActual{
				Harness:        req.Decision.Harness,
				Provider:       req.Decision.Provider,
				ServerInstance: req.Decision.ServerInstance,
				Model:          req.Decision.Model,
			},
		})
		return
	}
	for ev := range in {
		if ev.Metadata == nil {
			ev.Metadata = req.Metadata
		}
		if ev.Type == harnesses.EventTypeFinal {
			var final harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &final); err != nil {
				emitSubprocessProtocolFinal(ctx, req, cb, fmt.Sprintf("malformed harness final event: %v", err))
				return
			}
			emitSubprocessFinal(ctx, req, cb, ev, final)
			return
		}
		if cb.ObserveEvent != nil {
			ev = cb.ObserveEvent(ev)
		}
		if cb.EmitEvent != nil && !cb.EmitEvent(ev) {
			return
		}
	}
	emitSubprocessProtocolFinal(ctx, req, cb, "harness event stream closed without a final event")
}

func replaceSubprocessFinal(ev harnesses.Event, final harnesses.FinalData) harnesses.Event {
	raw, err := json.Marshal(final)
	if err == nil {
		ev.Data = raw
	}
	return ev
}

func emitSubprocessProtocolFinal(ctx context.Context, req SubprocessRequest, cb SubprocessCallbacks, diagnostic string) {
	status := "internal_error"
	if ctx.Err() != nil {
		status = "cancelled"
	}
	durationMS := int64(0)
	if !req.Started.IsZero() {
		durationMS = time.Since(req.Started).Milliseconds()
	}
	final := harnesses.FinalData{
		Status:     status,
		Error:      diagnostic,
		DurationMS: durationMS,
	}
	ev := harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Time:     time.Now().UTC(),
		Metadata: req.Metadata,
	}
	emitSubprocessFinal(ctx, req, cb, ev, final)
}

func emitSubprocessFinal(ctx context.Context, req SubprocessRequest, cb SubprocessCallbacks, ev harnesses.Event, final harnesses.FinalData) {
	final = ClassifyTerminalFinal(final, TerminalOriginHarness, ctx.Err())
	ev = replaceSubprocessFinal(ev, final)
	ev = stampSubprocessFinalRouting(ev, req.Decision)
	ev = stampSubprocessFinalSessionLog(ev, req.SessionLogPath)
	if err := json.Unmarshal(ev.Data, &final); err == nil && cb.WriteEnd != nil {
		cb.WriteEnd(req.Metadata, final)
	}
	if cb.ObserveEvent != nil {
		ev = cb.ObserveEvent(ev)
	}
	if cb.EmitEvent != nil {
		cb.EmitEvent(ev)
	}
}

func subprocessTemperature(v *float32) float32 {
	if v == nil {
		return 0
	}
	return *v
}

func subprocessSeed(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func adapterReasoning(value reasoning.Reasoning) string {
	policy, err := reasoning.ParseString(string(value))
	if err != nil {
		return string(value)
	}
	switch policy.Kind {
	case reasoning.KindUnset, reasoning.KindAuto, reasoning.KindOff:
		return ""
	case reasoning.KindTokens:
		if policy.Tokens == 0 {
			return ""
		}
		return string(policy.Value)
	case reasoning.KindNamed:
		return string(policy.Value)
	default:
		return string(value)
	}
}

func stampSubprocessFinalSessionLog(ev harnesses.Event, sessionLogPath string) harnesses.Event {
	if sessionLogPath == "" {
		return ev
	}
	var final harnesses.FinalData
	if err := json.Unmarshal(ev.Data, &final); err != nil {
		return ev
	}
	final.SessionLogPath = sessionLogPath
	raw, err := json.Marshal(final)
	if err != nil {
		return ev
	}
	ev.Data = raw
	return ev
}

func stampSubprocessFinalRouting(ev harnesses.Event, decision ExecuteRunnerDecision) harnesses.Event {
	var final harnesses.FinalData
	if err := json.Unmarshal(ev.Data, &final); err != nil {
		return ev
	}
	if final.RoutingActual == nil {
		final.RoutingActual = &harnesses.RoutingActual{
			Harness:        decision.Harness,
			Provider:       decision.Provider,
			ServerInstance: decision.ServerInstance,
			Model:          decision.Model,
		}
	}
	raw, err := json.Marshal(final)
	if err != nil {
		return ev
	}
	ev.Data = raw
	return ev
}

func finalizeSubprocess(cb SubprocessCallbacks, final harnesses.FinalData) {
	if cb.Finalize != nil {
		cb.Finalize(final)
	}
}
