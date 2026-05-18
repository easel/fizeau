package claudetui

import (
	"context"
	"errors"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// ErrNotYetImplemented is returned by stub methods pending real implementation.
var ErrNotYetImplemented = errors.New("claude-tui harness: not yet implemented")

// Harness is the sentinel harness for claude TUI.
// It satisfies the harnesses.Harness, harnesses.QuotaHarness,
// harnesses.AccountHarness, and harnesses.ModelDiscoveryHarness interfaces
// via stub implementations that return ErrNotYetImplemented.
type Harness struct {
}

// Info implements harnesses.Harness.
func (h *Harness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{
		Name:                "claude-tui",
		Type:                "subprocess",
		Available:           false,
		IsSubscription:      true,
		AutoRoutingEligible: false,
		DefaultModel:        "claude-sonnet-4-6",
	}
}

// HealthCheck implements harnesses.Harness.
func (h *Harness) HealthCheck(ctx context.Context) error {
	return ErrNotYetImplemented
}

// Execute implements harnesses.Harness.
func (h *Harness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return nil, ErrNotYetImplemented
}

// QuotaStatus implements harnesses.QuotaHarness.
func (h *Harness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, ErrNotYetImplemented
}

// RefreshQuota implements harnesses.QuotaHarness.
func (h *Harness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, ErrNotYetImplemented
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (h *Harness) QuotaFreshness() time.Duration {
	return 15 * time.Minute
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (h *Harness) SupportedLimitIDs() []string {
	return nil
}

// AccountStatus implements harnesses.AccountHarness.
func (h *Harness) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, ErrNotYetImplemented
}

// RefreshAccount implements harnesses.AccountHarness.
func (h *Harness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, ErrNotYetImplemented
}

// AccountFreshness implements harnesses.AccountHarness.
func (h *Harness) AccountFreshness() time.Duration {
	return 24 * time.Hour
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness.
func (h *Harness) DefaultModelSnapshot() harnesses.ModelDiscoverySnapshot {
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt: time.Now().UTC(),
		Source:     "not-yet-implemented",
	}
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness.
func (h *Harness) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	return "", harnesses.ErrAliasNotResolvable
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (h *Harness) SupportedAliases() []string {
	return nil
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
