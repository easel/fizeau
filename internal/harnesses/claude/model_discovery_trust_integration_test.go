//go:build integration && !windows

package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test_modelDiscoveryHandlesUntrustedFolderTrustPrompt runs real claude model
// discovery from a fresh working directory. Current Claude Code highlights
// "No, exit" on the folder-trust dialog, so the probe records workspace trust
// for the directory before launch instead of confirming that default. The
// user's ~/.claude.json is restored afterward so the temp directory is not
// left trusted.
func Test_modelDiscoveryHandlesUntrustedFolderTrustPrompt(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	configPath := filepath.Join(home, ".claude.json")
	original, readErr := os.ReadFile(configPath)
	t.Cleanup(func() {
		if readErr != nil {
			return
		}
		if err := os.WriteFile(configPath, original, 0o600); err != nil {
			t.Errorf("restore %s: %v", configPath, err)
		}
	})
	dir := t.TempDir()
	t.Setenv("FIZEAU_RECORD_CLAUDE_TRUST", "1")
	snapshot, err := ReadClaudeModelDiscoveryViaPTY(30*time.Second, WithQuotaPTYWorkdir(dir))
	require.NoError(t, err, "discovery must answer the folder-trust dialog and complete")
	require.NotEmpty(t, snapshot.Models, "expected non-empty model list from untrusted dir")
	t.Logf("discovered %d models from untrusted dir %s: %v", len(snapshot.Models), dir, snapshot.Models)
}
