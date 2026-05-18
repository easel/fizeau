// Package harnesstest provides shared conformance assertions and
// in-memory synthetic implementations of the CONTRACT-004 harness
// sub-interfaces. It is intentionally not a test-only package: the
// service-side tests across this repo construct synthetic harnesses to
// drive routing decisions without depending on per-harness snapshot
// constructors.
package harnesstest

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/harnesses"
)

// SyntheticQuotaHarness is an in-memory QuotaHarness used by
// service-level tests that previously constructed per-harness snapshot
// types directly. SupportedLimitIDs and QuotaFreshness are read from
// the values supplied at construction time.
type SyntheticQuotaHarness struct {
	name      string
	limitIDs  []string
	freshness time.Duration

	mu         sync.Mutex
	status     harnesses.QuotaStatus
	probeCount int
	probeLatch <-chan struct{}

	sf singleflight.Group
}

// NewSyntheticQuotaHarness builds an in-memory QuotaHarness whose
// QuotaStatus returns the supplied status and whose SupportedLimitIDs
// returns the supplied set. RefreshQuota is single-flight per harness
// instance (concurrent callers share one underlying probe).
//
// The default QuotaFreshness is 15 minutes; tests that care about a
// different window can call SetQuotaFreshness.
func NewSyntheticQuotaHarness(name string, status harnesses.QuotaStatus, limitIDs []string) *SyntheticQuotaHarness {
	return &SyntheticQuotaHarness{
		name:      name,
		limitIDs:  append([]string(nil), limitIDs...),
		freshness: 15 * time.Minute,
		status:    status,
	}
}

// SetQuotaStatus replaces the synthetic status reported by future
// QuotaStatus / RefreshQuota calls.
func (s *SyntheticQuotaHarness) SetQuotaStatus(status harnesses.QuotaStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// SetQuotaFreshness overrides the freshness window returned by
// QuotaFreshness.
func (s *SyntheticQuotaHarness) SetQuotaFreshness(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.freshness = d
}

// SetProbeLatch installs a channel that RefreshQuota's probe blocks on
// (after incrementing ProbeCount and before returning). Tests use it
// to ensure concurrent RefreshQuota callers collide on the same
// single-flight cohort. Pass a closed or nil channel to clear the latch.
func (s *SyntheticQuotaHarness) SetProbeLatch(ch <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeLatch = ch
}

// ProbeCount returns the number of RefreshQuota cohorts that ran to
// completion. Used by tests to verify single-flight semantics.
func (s *SyntheticQuotaHarness) ProbeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probeCount
}

// Info implements harnesses.Harness.
func (s *SyntheticQuotaHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: s.name, Type: "native", Available: true}
}

// HealthCheck implements harnesses.Harness.
func (s *SyntheticQuotaHarness) HealthCheck(ctx context.Context) error {
	return ctx.Err()
}

// Execute implements harnesses.Harness. The synthetic harness emits a
// single success Final event and closes the channel.
func (s *SyntheticQuotaHarness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return syntheticExecute(ctx)
}

// QuotaStatus implements harnesses.QuotaHarness.
func (s *SyntheticQuotaHarness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, nil
}

// RefreshQuota implements harnesses.QuotaHarness with real single-flight.
// Concurrent callers share one underlying probe; ProbeCount increments
// once per cohort.
func (s *SyntheticQuotaHarness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	v, err, _ := s.sf.Do("refresh", func() (any, error) {
		s.mu.Lock()
		s.probeCount++
		status := s.status
		latch := s.probeLatch
		s.mu.Unlock()
		if latch != nil {
			select {
			case <-latch:
			case <-ctx.Done():
				return harnesses.QuotaStatus{}, ctx.Err()
			}
		}
		return status, nil
	})
	if err != nil {
		return harnesses.QuotaStatus{}, err
	}
	return v.(harnesses.QuotaStatus), nil
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (s *SyntheticQuotaHarness) QuotaFreshness() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.freshness
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (s *SyntheticQuotaHarness) SupportedLimitIDs() []string {
	return append([]string(nil), s.limitIDs...)
}

// SyntheticAccountHarness is an in-memory AccountHarness.
type SyntheticAccountHarness struct {
	name      string
	freshness time.Duration

	mu         sync.Mutex
	snapshot   harnesses.AccountSnapshot
	probeCount int

	sf singleflight.Group
}

// NewSyntheticAccountHarness builds an in-memory AccountHarness whose
// AccountStatus returns the supplied snapshot and whose
// AccountFreshness returns the supplied window.
func NewSyntheticAccountHarness(name string, snapshot harnesses.AccountSnapshot, freshness time.Duration) *SyntheticAccountHarness {
	return &SyntheticAccountHarness{
		name:      name,
		snapshot:  snapshot,
		freshness: freshness,
	}
}

// SetAccountSnapshot replaces the snapshot returned by future calls.
func (s *SyntheticAccountHarness) SetAccountSnapshot(snapshot harnesses.AccountSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snapshot
}

// ProbeCount returns the number of RefreshAccount cohorts that ran.
func (s *SyntheticAccountHarness) ProbeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probeCount
}

// Info implements harnesses.Harness.
func (s *SyntheticAccountHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: s.name, Type: "native", Available: true}
}

// HealthCheck implements harnesses.Harness.
func (s *SyntheticAccountHarness) HealthCheck(ctx context.Context) error {
	return ctx.Err()
}

// Execute implements harnesses.Harness.
func (s *SyntheticAccountHarness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return syntheticExecute(ctx)
}

// AccountStatus implements harnesses.AccountHarness.
func (s *SyntheticAccountHarness) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot, nil
}

// RefreshAccount implements harnesses.AccountHarness with real single-flight.
func (s *SyntheticAccountHarness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	v, err, _ := s.sf.Do("refresh", func() (any, error) {
		s.mu.Lock()
		s.probeCount++
		snapshot := s.snapshot
		s.mu.Unlock()
		return snapshot, nil
	})
	if err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return v.(harnesses.AccountSnapshot), nil
}

// AccountFreshness implements harnesses.AccountHarness.
func (s *SyntheticAccountHarness) AccountFreshness() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.freshness
}

// SyntheticModelDiscoveryHarness is an in-memory ModelDiscoveryHarness.
// ResolveModelAlias resolves any alias in the supplied set to the first
// model in snapshot.Models; aliases outside the set return
// ErrAliasNotResolvable.
type SyntheticModelDiscoveryHarness struct {
	name     string
	snapshot harnesses.ModelDiscoverySnapshot
	aliases  []string
	aliasSet map[string]struct{}
}

// NewSyntheticModelDiscoveryHarness builds an in-memory
// ModelDiscoveryHarness. The supplied aliases set is the exact set
// returned by SupportedAliases.
func NewSyntheticModelDiscoveryHarness(name string, snapshot harnesses.ModelDiscoverySnapshot, aliases []string) *SyntheticModelDiscoveryHarness {
	aliasSet := make(map[string]struct{}, len(aliases))
	for _, a := range aliases {
		aliasSet[a] = struct{}{}
	}
	return &SyntheticModelDiscoveryHarness{
		name:     name,
		snapshot: snapshot,
		aliases:  append([]string(nil), aliases...),
		aliasSet: aliasSet,
	}
}

// Info implements harnesses.Harness.
func (s *SyntheticModelDiscoveryHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: s.name, Type: "native", Available: true}
}

// HealthCheck implements harnesses.Harness.
func (s *SyntheticModelDiscoveryHarness) HealthCheck(ctx context.Context) error {
	return ctx.Err()
}

// Execute implements harnesses.Harness.
func (s *SyntheticModelDiscoveryHarness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return syntheticExecute(ctx)
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness.
func (s *SyntheticModelDiscoveryHarness) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	if len(s.snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return s.snapshot, nil
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness.
// Recognized aliases resolve to the first concrete model in the
// supplied snapshot's Models slice (falling back to s.snapshot.Models[0]
// when the caller passes an empty snapshot).
func (s *SyntheticModelDiscoveryHarness) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	if _, ok := s.aliasSet[family]; !ok {
		return "", harnesses.ErrAliasNotResolvable
	}
	models := snapshot.Models
	if len(models) == 0 {
		models = s.snapshot.Models
	}
	if len(models) == 0 {
		return "", harnesses.ErrAliasNotResolvable
	}
	return models[0], nil
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (s *SyntheticModelDiscoveryHarness) SupportedAliases() []string {
	return append([]string(nil), s.aliases...)
}

// syntheticExecute emits one success Final event and closes the channel.
func syntheticExecute(ctx context.Context) (<-chan harnesses.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(harnesses.FinalData{Status: "success", ExitCode: 0})
	ch := make(chan harnesses.Event, 1)
	ch <- harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: 1,
		Time:     time.Now(),
		Data:     data,
	}
	close(ch)
	return ch, nil
}

// Compile-time interface satisfaction checks for the synthetic types.
var (
	_ harnesses.QuotaHarness          = (*SyntheticQuotaHarness)(nil)
	_ harnesses.AccountHarness        = (*SyntheticAccountHarness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*SyntheticModelDiscoveryHarness)(nil)
)
