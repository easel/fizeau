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
