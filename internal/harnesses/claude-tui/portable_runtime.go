package claudetui

import (
	"context"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

var _ harnesses.PortableRuntimeHarness = (*Harness)(nil)

var discoverClaudeTUIPortableRuntime = anthropic.ClaudePortableRuntimeAssets

// PortableRuntimeAssets contributes the same native Claude Code installation
// and state as the print-mode adapter while retaining the claude-tui identity.
func (h *Harness) PortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	return discoverClaudeTUIPortableRuntime(ctx, target, anthropic.ClaudePortableRuntimeOptions{
		Launcher:         h.Binary,
		EnvironmentNames: claudeTUIPortableInheritedEnvironmentNames(),
	})
}
