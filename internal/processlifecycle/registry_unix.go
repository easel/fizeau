//go:build !windows

package processlifecycle

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- directory is selected by the caller for lifecycle state
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
