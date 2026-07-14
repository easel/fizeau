//go:build !linux && !darwin && !windows

package processlifecycle

import "context"

func reapClaimedPlatformRecord(context.Context, *FileRegistry, Record) error {
	return ErrPlatformUnsupported
}
