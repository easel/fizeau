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

// anthropicAPIKeyEnv / anthropicBaseURLEnv are the canonical env vars for the
// Anthropic provider, mirroring the source used by the provider registry.
const (
	// #nosec G101 -- this is an environment variable name, not a credential value.
	anthropicAPIKeyEnv  = "ANTHROPIC_API_KEY"
	anthropicBaseURLEnv = "ANTHROPIC_BASE_URL"
)

// newClaudeRunner constructs the metered claude harness Runner with the
// transport selected by claudeharness.NativeTransportSelected. The default
// (subprocess) build is the zero-value Runner — identical to the prior
// &claudeharness.Runner{}.
//
// When native transport is selected, the Anthropic API key must be present in
// the environment; a missing key is surfaced as a clear early error rather than
// a late nil-deref or opaque failure mid-turn.
func newClaudeRunner() (*claudeharness.Runner, error) {
	if claudeharness.NativeTransportSelected() {
		key := strings.TrimSpace(os.Getenv(anthropicAPIKeyEnv))
		if key == "" {
			return nil, fmt.Errorf("FIZEAU_CLAUDE_TRANSPORT=native but no Anthropic API key found; set ANTHROPIC_API_KEY")
		}
		return &claudeharness.Runner{
			NativeMode:    true,
			NativeAPIKey:  key,
			NativeBaseURL: os.Getenv(anthropicBaseURLEnv),
		}, nil
	}
	return &claudeharness.Runner{NativeMode: false}, nil
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
		runner, err := newClaudeRunner()
		if err != nil {
			finalizeDispatch(cb, harnesses.FinalData{
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
		runSubprocess(ctx, cb, runner)
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
