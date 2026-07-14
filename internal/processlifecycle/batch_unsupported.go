//go:build !linux && !darwin

package processlifecycle

import (
	"context"
	"fmt"
	"os/exec"
)

// Batch is unavailable until the platform has a strong containment adapter.
type Batch struct{}

func StartBatch(context.Context, *exec.Cmd, BatchOptions) (*Batch, error) {
	return nil, fmt.Errorf("%w: no strong batch containment adapter", ErrPlatformUnsupported)
}

func (*Batch) Record() Record { return Record{} }
func (*Batch) Stop() error    { return nil }
func (*Batch) Wait() error    { return nil }
