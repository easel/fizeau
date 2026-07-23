package builtin

import (
	"fmt"
	"os"
	"strings"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	grokharness "github.com/easel/fizeau/internal/harnesses/grok"
	opencodeharness "github.com/easel/fizeau/internal/harnesses/opencode"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
)

const (
	// #nosec G101 -- this is an environment variable name, not a credential value.
	anthropicAPIKeyEnv  = "ANTHROPIC_API_KEY"
	anthropicBaseURLEnv = "ANTHROPIC_BASE_URL"
)

// New returns a fresh built-in subprocess harness runner by canonical name.
func New(name string) harnesses.Harness {
	switch name {
	case "claude":
		return &claudeharness.Runner{NativeMode: claudeharness.NativeTransportSelected()}
	case "claude-tui":
		return &claudetui.Harness{}
	case "codex":
		return &codexharness.Runner{}
	case "gemini":
		return &geminiharness.Runner{}
	case "grok":
		return &grokharness.Runner{}
	case "opencode":
		return &opencodeharness.Runner{}
	case "pi":
		return &piharness.Runner{}
	default:
		return nil
	}
}

// NewRouteRunner constructs the production runner for one exact route from
// the authority-owned structural prototype. Built-ins are cloned so activated
// launch configuration is retained without aliasing endpoint-private state.
func NewRouteRunner(key harnesses.RouteRunnerKey, prototype harnesses.Harness) (harnesses.Harness, error) {
	switch runner := prototype.(type) {
	case *claudeharness.Runner:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		clone.BaseArgs = append([]string(nil), runner.BaseArgs...)
		clone.NativeTools = append([]agentcore.Tool(nil), runner.NativeTools...)
		if _, activated := clone.PortableRuntimeBinding(); activated {
			return &clone, nil
		}
		return configureClaudeRouteRunner(&clone)
	case *claudetui.Harness:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		return &clone, nil
	case *codexharness.Runner:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		clone.BaseArgs = append([]string(nil), runner.BaseArgs...)
		return &clone, nil
	case *geminiharness.Runner:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		clone.BaseArgs = append([]string(nil), runner.BaseArgs...)
		return &clone, nil
	case *grokharness.Runner:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		clone.BaseArgs = append([]string(nil), runner.BaseArgs...)
		return &clone, nil
	case *opencodeharness.Runner:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		clone.BaseArgs = append([]string(nil), runner.BaseArgs...)
		return &clone, nil
	case *piharness.Runner:
		clone := *runner
		clone.PortableRuntimeRunnerState = runner.PortableRuntimeRunnerState.Clone()
		clone.BaseArgs = append([]string(nil), runner.BaseArgs...)
		return &clone, nil
	case nil:
		return nil, fmt.Errorf("unknown subprocess harness %q", key.Harness)
	default:
		// Custom registered prototypes are already caller-owned instances. The
		// interface exposes no safe generic clone operation, so retain them.
		// Do not call Info here: structural inventory composition is required to
		// remain side-effect-free, and dispatch validates the safe structural
		// descriptor before execution.
		return prototype, nil
	}
}

func configureClaudeRouteRunner(runner *claudeharness.Runner) (*claudeharness.Runner, error) {
	if !runner.NativeMode {
		runner.NativeAPIKey = ""
		runner.NativeBaseURL = ""
		return runner, nil
	}
	apiKey := strings.TrimSpace(os.Getenv(anthropicAPIKeyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("FIZEAU_CLAUDE_TRANSPORT=native but no Anthropic API key found; set ANTHROPIC_API_KEY")
	}
	runner.NativeMode = true
	runner.NativeAPIKey = apiKey
	runner.NativeBaseURL = os.Getenv(anthropicBaseURLEnv)
	return runner, nil
}

// Instances returns the production map of built-in subprocess harnesses keyed
// by canonical harness name.
func Instances() map[string]harnesses.Harness {
	return map[string]harnesses.Harness{
		"claude":     New("claude"),
		"claude-tui": New("claude-tui"),
		"codex":      New("codex"),
		"gemini":     New("gemini"),
		"grok":       New("grok"),
		"opencode":   New("opencode"),
		"pi":         New("pi"),
	}
}
