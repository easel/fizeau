//go:build windows

package processlifecycle

import (
	"context"
	"errors"
	"io/fs"

	"golang.org/x/sys/windows"
)

func reapClaimedPlatformRecord(ctx context.Context, registry *FileRegistry, record Record) error {
	backend := &staleWindowsBackend{}
	_, err := Recover(ctx, record.RecordID, registry, backend, systemClock{})
	if errors.Is(err, ErrBoundaryAlreadyEmpty) || errors.Is(err, ErrBoundaryOwned) ||
		errors.Is(err, ErrIdentityMismatch) || errors.Is(err, ErrBoundaryIndeterminate) {
		return nil
	}
	return err
}

type staleWindowsBackend struct{}

func (*staleWindowsBackend) ObserveBoundary(ctx context.Context, record Record) (BoundaryObservation, error) {
	if err := ctx.Err(); err != nil {
		return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity}, err
	}
	owner, ownerErr := observeWindowsIdentity(record.OwnerIdentity.PID)
	child, childErr := observeWindowsIdentity(record.DirectChildIdentity.PID)
	if errors.Is(ownerErr, fs.ErrNotExist) && errors.Is(childErr, fs.ErrNotExist) {
		// Kill-on-job-close guarantees the boundary is gone once its sole
		// non-inheritable owner handle and recorded anchor have disappeared.
		return BoundaryObservation{Status: BoundaryEmpty, BoundaryIdentity: record.BoundaryIdentity}, nil
	}
	if ownerErr == nil && record.OwnerIdentity.matches(owner) && childErr == nil && record.DirectChildIdentity.matches(child) {
		return BoundaryObservation{
			Status: BoundaryMatching, BoundaryIdentity: record.BoundaryIdentity,
			OwnerStatus: OwnerMatching, OwnerIdentity: owner,
			SupervisorIdentity: owner, DirectChildIdentity: child, BoundaryProcessIdentity: child,
		}, nil
	}
	if ownerErr == nil || childErr == nil {
		return BoundaryObservation{Status: BoundaryMismatch, BoundaryIdentity: record.BoundaryIdentity, Detail: "recorded Windows owner or child identity changed"}, nil
	}
	return BoundaryObservation{Status: BoundaryIndeterminate, BoundaryIdentity: record.BoundaryIdentity, Detail: "Windows job recovery identity is not observable"}, errors.Join(ownerErr, childErr)
}

func observeWindowsIdentity(pid int) (ProcessIdentity, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
			return ProcessIdentity{}, fs.ErrNotExist
		}
		return ProcessIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	return windowsProcessIdentity(pid, handle)
}
