package harnesses

import (
	"context"
	"os/exec"
)

// HarnessCommand constructs an *exec.Cmd for a known harness binary.
//
// binary must be a path resolved by the runner from a HarnessConfig.Binary
// (looked up via LookPathFunc / exec.LookPath against a fixed allowlist of
// builtin harness names: "codex", "claude", "gemini", "opencode", "pi", ...).
// args are the harness-specific argument vector assembled from the runner's
// HarnessConfig + per-request fields.
//
// This helper exists to localize the gosec G204 (subprocess launched with
// variable) safety contract in one place rather than annotating each caller.
func HarnessCommand(ctx context.Context, binary string, args ...string) *exec.Cmd {
	// #nosec G204 -- binary is the resolved path of a builtin harness CLI
	// from HarnessConfig (developer-authored allowlist); args come from the
	// same config plus structured request fields, never raw external input.
	return exec.CommandContext(ctx, binary, args...)
}

// HarnessBatchCommand constructs a command whose cancellation is owned by the
// shared process-lifecycle supervisor. Do not replace this with CommandContext:
// its hidden cancellation path can kill the trusted supervisor before it has
// reaped the contained harness group.
func HarnessBatchCommand(binary string, args ...string) *exec.Cmd {
	// #nosec G204 -- same fixed builtin-harness contract as HarnessCommand.
	return exec.Command(binary, args...)
}
