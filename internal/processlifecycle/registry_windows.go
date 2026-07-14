//go:build windows

package processlifecycle

import "golang.org/x/sys/windows"

func replaceFileAtomic(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		from,
		to,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

// Windows does not support opening a directory and flushing it through
// os.File.Sync. The record contents are flushed before replacement and
// MoveFileEx uses MOVEFILE_WRITE_THROUGH; after a successful Remove there is
// no additional portable directory durability operation to perform.
func syncDirectory(string) error { return nil }
