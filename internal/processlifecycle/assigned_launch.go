package processlifecycle

import (
	"context"
	"errors"
	"fmt"
)

// assignedPreparedBoundary is a suspended process whose containment boundary
// must be assigned before Acquire may persist ownership and release its launch
// gate. Windows uses this ordering for Job Object assignment before resume.
type assignedPreparedBoundary interface {
	PreparedBoundary
	Assign(context.Context) error
}

func acquireAssignedBoundary(
	ctx context.Context,
	opts Options,
	registry Registry,
	platform PlatformBackend,
	prepared assignedPreparedBoundary,
) (*Lease, error) {
	if err := prepared.Assign(ctx); err != nil {
		result, abortErr := prepared.Abort(context.WithoutCancel(ctx))
		return nil, errors.Join(
			fmt.Errorf("assign suspended process to containment boundary: %w", err),
			abortErr,
			abortStatusError(result),
		)
	}
	return Acquire(ctx, opts, registry, platform, prepared)
}

// rejectUnsupportedPlatform is deliberately passed a launch callback so tests
// can prove the unsupported decision happens before any process-creation side
// effect. The callback is never invoked.
func rejectUnsupportedPlatform(_ func() error) error {
	return fmt.Errorf("%w: no strong batch containment adapter", ErrPlatformUnsupported)
}
