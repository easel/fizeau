package harnesses

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/easel/fizeau/internal/processlifecycle"
)

// HarnessBatchCommand constructs an *exec.Cmd for a known harness binary.
//
// binary must be a path resolved by the runner from a HarnessConfig.Binary
// (looked up via LookPathFunc / exec.LookPath against a fixed allowlist of
// builtin harness names: "codex", "claude", "gemini", "opencode", "pi", ...).
// args are the harness-specific argument vector assembled from the runner's
// HarnessConfig + per-request fields.
//
// This seam exists to localize the gosec G204 (subprocess launched with
// variable) safety contract in one place rather than annotating each caller.
// HarnessBatchCommand constructs a command whose cancellation is owned by the
// shared process-lifecycle supervisor. Do not replace this with CommandContext:
// its hidden cancellation path can kill the trusted supervisor before it has
// reaped the contained harness group.
func HarnessBatchCommand(binary string, args ...string) *exec.Cmd {
	// #nosec G204 -- same fixed builtin-harness contract as HarnessCommand.
	return exec.Command(binary, args...)
}

// HarnessCombinedOutput runs one bounded auxiliary harness command through the
// same durable lifecycle supervisor as a full batch invocation. Model/help/
// version/account/quota probes must use this helper instead of calling an
// *exec.Cmd Run/Output method directly.
func HarnessCombinedOutput(ctx context.Context, harness, binary string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := HarnessBatchCommand(binary, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	batch, err := processlifecycle.StartBatch(ctx, cmd, processlifecycle.BatchOptions{Harness: harness})
	if err != nil {
		return nil, err
	}
	err = batch.Wait()
	return output.Bytes(), err
}
