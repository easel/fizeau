package claude

import (
	"context"
	"fmt"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

var _ harnesses.PortableRuntimeHarness = (*Runner)(nil)

var discoverClaudePortableRuntime = anthropic.ClaudePortableRuntimeAssets

// PortableRuntimeAssets contributes only the subprocess-backed Claude Code
// runner. NativeMode is an HTTP transport and never fabricates a CLI asset.
func (r *Runner) PortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	if r.NativeMode {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: native Claude transport has no subprocess runtime", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
	return discoverClaudePortableRuntime(ctx, target, anthropic.ClaudePortableRuntimeOptions{
		Launcher:  r.Binary,
		Arguments: r.BaseArgs,
		EnvironmentNames: []string{
			"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN",
			"API_TIMEOUT_MS", "BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS", "MAX_THINKING_TOKENS",
			"MCP_TIMEOUT", "MCP_TOOL_TIMEOUT", "MAX_MCP_OUTPUT_TOKENS",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
		},
		EnvironmentPrefixes:        []string{"ANTHROPIC_", "CLAUDE_"},
		InheritsProcessEnvironment: true,
	})
}
