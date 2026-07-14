//go:build !linux && !darwin && !windows

package processlifecycle

import (
	"context"
	"os/exec"
)

// Batch is unavailable until the platform has a strong containment adapter.
type Batch struct{}

func StartBatch(_ context.Context, target *exec.Cmd, _ BatchOptions) (*Batch, error) {
	return nil, rejectUnsupportedPlatform(func() error {
		if target == nil {
			return nil
		}
		return target.Start()
	})
}

func (*Batch) Record() Record { return Record{} }
func (*Batch) Stop() error    { return nil }
func (*Batch) Wait() error    { return nil }
