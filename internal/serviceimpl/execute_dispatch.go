package serviceimpl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	claudetuiharness "github.com/easel/fizeau/internal/harnesses/claude-tui"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	opencodeharness "github.com/easel/fizeau/internal/harnesses/opencode"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
)

// claudeTransportEnv is the kill-switch env var selecting the transport backing
// the metered "claude" harness. Default-off: unset / "subprocess" spawns
// `claude --print` (byte-for-byte unchanged production behavior); "native"
// routes through the in-process Anthropic Messages API (metered,
// actual_cash_spend). Rollback = unset the var or set it to "subprocess".
const claudeTransportEnv = "FIZEAU_CLAUDE_TRANSPORT"

// claudeNativeTransportSelected reports whether the claude harness should use
// the native Anthropic Messages API transport. Only an explicit "native" value
// flips it on; every other value (including empty and "subprocess") keeps the
// default subprocess path.
func claudeNativeTransportSelected() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(claudeTransportEnv)), "native")
}

// newClaudeRunner constructs the metered claude harness Runner with the
// transport selected by claudeTransportEnv. The default (subprocess) build is
// the zero-value Runner — identical to the prior &claudeharness.Runner{}.
func newClaudeRunner() *claudeharness.Runner {
	return &claudeharness.Runner{NativeMode: claudeNativeTransportSelected()}
}

// ExecuteDispatchRequest carries API-neutral data needed to choose the
// concrete execute runner.
type ExecuteDispatchRequest struct {
	Decision ExecuteRunnerDecision
	Started  time.Time
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
	case "claude":
		runSubprocess(ctx, cb, newClaudeRunner())
	case "claude-tui":
		runSubprocess(ctx, cb, &claudetuiharness.Harness{})
	case "codex":
		runSubprocess(ctx, cb, &codexharness.Runner{})
	case "gemini":
		runSubprocess(ctx, cb, &geminiharness.Runner{})
	case "opencode":
		runSubprocess(ctx, cb, &opencodeharness.Runner{})
	case "pi":
		runSubprocess(ctx, cb, &piharness.Runner{})
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
