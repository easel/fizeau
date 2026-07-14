package processlifecycle

import (
	"fmt"
	"os"
	"path/filepath"
)

func batchRegistryDir(sessionLogDir string) (string, error) {
	if sessionLogDir != "" {
		return filepath.Join(filepath.Dir(sessionLogDir), "harness-sessions"), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve lifecycle state directory: %w", err)
	}
	return filepath.Join(cacheDir, "fizeau", "harness-sessions"), nil
}

// StateDirectory resolves the durable lifecycle registry used by a wrapped
// invocation. Service orchestration resolves this once and forwards the exact
// directory to both the selected adapter and terminalization logic.
func StateDirectory(sessionLogDir string) (string, error) {
	return batchRegistryDir(sessionLogDir)
}
