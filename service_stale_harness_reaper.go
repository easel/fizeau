package fizeau

import (
	"context"
	"fmt"
	"time"

	"github.com/easel/fizeau/internal/processlifecycle"
)

const defaultStaleHarnessReaperGrace = 5 * time.Minute

func (s *service) reapStaleHarnessSessions() {
	dir := s.staleHarnessRegistryDir()
	if dir == "" {
		return
	}
	grace := s.opts.staleHarnessReaperGrace()
	now := time.Now().UTC()
	// Startup recovery is independent background maintenance. Per-record OS
	// claims serialize adopters, while New remains nonblocking even when an old
	// boundary needs the full recovery deadline.
	go func() {
		if err := reapStaleHarnessRecords(dir, grace, now); err != nil && s.opts.Logger != nil {
			_, _ = fmt.Fprintf(s.opts.Logger, "fizeau: stale harness recovery failed for %s: %v\n", dir, err)
		}
	}()
}

func (s *service) staleHarnessRegistryDir() string {
	if s == nil {
		return ""
	}
	dir, err := processlifecycle.StateDirectory(s.serviceSessionLogDir())
	if err != nil {
		return ""
	}
	return dir
}

func (o ServiceOptions) staleHarnessReaperGrace() time.Duration {
	if o.StaleHarnessReaperGrace > 0 {
		return o.StaleHarnessReaperGrace
	}
	return defaultStaleHarnessReaperGrace
}

func reapStaleHarnessRecords(dir string, grace time.Duration, now time.Time) error {
	return processlifecycle.ReapStaleRecords(context.Background(), processlifecycle.NewFileRegistry(dir), grace, now)
}
