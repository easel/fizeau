//go:build integration && !windows

package claude

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test_modelDiscoveryHandlesUntrustedFolderTrustPrompt runs real claude model
// discovery from a fresh (untrusted) working directory. A not-yet-trusted dir
// triggers Claude Code's "Do you trust the files in this folder?" onboarding
// dialog, which previously stalled the PTY driver into a zero-model timeout.
// With the trust interstitial wired in, discovery must succeed.
func Test_modelDiscoveryHandlesUntrustedFolderTrustPrompt(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}
	dir := t.TempDir() // brand-new dir → not in ~/.claude.json trusted set → trust dialog
	snapshot, err := ReadClaudeModelDiscoveryViaPTY(30*time.Second, WithQuotaPTYWorkdir(dir))
	require.NoError(t, err, "discovery must answer the folder-trust dialog and complete")
	require.NotEmpty(t, snapshot.Models, "expected non-empty model list from untrusted dir")
	t.Logf("discovered %d models from untrusted dir %s: %v", len(snapshot.Models), dir, snapshot.Models)
}
