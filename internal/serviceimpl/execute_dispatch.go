package serviceimpl

import (
	"context"
	"fmt"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// ExecuteDispatchRequest carries API-neutral data needed to choose the
// concrete execute runner.
type ExecuteDispatchRequest struct {
	Decision         ExecuteRunnerDecision
	RouteRunner      harnesses.RouteRunnerBinding
	RouteRunnerError error
	Started          time.Time
}

// ExecuteDispatchCallbacks connect the internal dispatcher to root-owned
// public event/session adapters.
type ExecuteDispatchCallbacks struct {
	RunNative      func(context.Context)
	RunSubprocess  func(context.Context, harnesses.Harness)
	RunVirtual     func(context.Context)
	RunScript      func(context.Context)
	IsHTTPProvider func(harness string) bool
	Finalize       func(harnesses.FinalData)
}

// DispatchExecuteRun selects the concrete runner for an Execute request.
func DispatchExecuteRun(ctx context.Context, req ExecuteDispatchRequest, cb ExecuteDispatchCallbacks) {
	switch req.Decision.Harness {
	case "fiz", "":
		if cb.RunNative != nil {
			cb.RunNative(ctx)
		}
	case "claude", "claude-tui", "codex", "gemini", "grok", "opencode", "pi":
		runRegisteredSubprocess(ctx, req, cb)
	case "virtual":
		if cb.RunVirtual != nil {
			cb.RunVirtual(ctx)
		}
	case "script":
		if cb.RunScript != nil {
			cb.RunScript(ctx)
		}
	default:
		if cb.IsHTTPProvider != nil && cb.IsHTTPProvider(req.Decision.Harness) {
			if cb.RunNative != nil {
				cb.RunNative(ctx)
			}
			return
		}
		finalizeDispatch(cb, harnesses.FinalData{
			Status:     "failed",
			Error:      fmt.Sprintf("harness %q dispatch not yet wired in service.Execute", req.Decision.Harness),
			DurationMS: time.Since(req.Started).Milliseconds(),
			RoutingActual: &harnesses.RoutingActual{
				Harness:        req.Decision.Harness,
				Provider:       req.Decision.Provider,
				ServerInstance: req.Decision.ServerInstance,
				Model:          req.Decision.Model,
			},
		})
	}
}

func runRegisteredSubprocess(ctx context.Context, req ExecuteDispatchRequest, cb ExecuteDispatchCallbacks) {
	if req.RouteRunnerError != nil {
		finalizeRunnerBindingFailure(req, cb, req.RouteRunnerError.Error())
		return
	}
	wantKey := routeRunnerKeyFromDecision(req.Decision)
	if !req.RouteRunner.Valid() || req.RouteRunner.Key() != wantKey {
		finalizeRunnerBindingFailure(req, cb, fmt.Sprintf("harness %q has no matching exact registered route runner", req.Decision.Harness))
		return
	}
	runner := req.RouteRunner.Runner()
	descriptor, ok := runner.(harnesses.PortableRuntimeStructuralHarness)
	if !ok || descriptor.PortableRuntimeStructure().Name != req.Decision.Harness {
		finalizeRunnerBindingFailure(req, cb, fmt.Sprintf("harness %q registered route runner has mismatched structure", req.Decision.Harness))
		return
	}
	runSubprocess(ctx, cb, runner)
}

func routeRunnerKeyFromDecision(decision ExecuteRunnerDecision) harnesses.RouteRunnerKey {
	return harnesses.RouteRunnerKey{
		Harness:        decision.Harness,
		Provider:       decision.Provider,
		Endpoint:       decision.Endpoint,
		ServerInstance: decision.ServerInstance,
		Model:          decision.Model,
	}
}

func finalizeRunnerBindingFailure(req ExecuteDispatchRequest, cb ExecuteDispatchCallbacks, message string) {
	finalizeDispatch(cb, harnesses.FinalData{
		Status:     "failed",
		Error:      message,
		DurationMS: time.Since(req.Started).Milliseconds(),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        req.Decision.Harness,
			Provider:       req.Decision.Provider,
			ServerInstance: req.Decision.ServerInstance,
			Model:          req.Decision.Model,
		},
	})
}

func runSubprocess(ctx context.Context, cb ExecuteDispatchCallbacks, runner harnesses.Harness) {
	if cb.RunSubprocess != nil {
		cb.RunSubprocess(ctx, runner)
	}
}

func finalizeDispatch(cb ExecuteDispatchCallbacks, final harnesses.FinalData) {
	if cb.Finalize != nil {
		cb.Finalize(final)
	}
}
