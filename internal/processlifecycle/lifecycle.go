package processlifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Lease owns one durable process-containment record.
type Lease struct {
	mu       sync.Mutex
	registry Registry
	platform PlatformBackend
	clock    Clock
	record   Record
}

// Acquire captures the stable identities supplied by a prepared boundary,
// persists ownership, and only then releases that same boundary's gate.
func Acquire(ctx context.Context, opts Options, registry Registry, platform PlatformBackend, prepared PreparedBoundary) (*Lease, error) {
	if registry == nil || platform == nil || prepared == nil {
		return nil, fmt.Errorf("%w: registry, platform backend, and prepared boundary are required", ErrInvalidRecord)
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	if opts.RecordID == "" {
		var err error
		opts.RecordID, err = newRecordID()
		if err != nil {
			return nil, abortLaunch(ctx, prepared, err)
		}
	}
	descriptor := prepared.Descriptor()
	now := opts.Clock.Now().UTC()
	record := Record{
		SchemaID:                RecordSchemaID,
		RecordID:                opts.RecordID,
		Revision:                1,
		OperationID:             opts.OperationID,
		Harness:                 opts.Harness,
		OwnerIdentity:           descriptor.OwnerIdentity,
		BoundaryIdentity:        descriptor.BoundaryID,
		BoundaryType:            descriptor.BoundaryType,
		BoundaryProcessIdentity: descriptor.BoundaryProcessIdentity,
		State:                   StateOwned,
		Timestamps:              Timestamps{CreatedAt: now, UpdatedAt: now},
		EscapeEvidence:          make([]EscapeEvidence, 0),
	}
	if err := registry.Create(ctx, record); err != nil {
		return nil, abortLaunch(ctx, prepared, fmt.Errorf("persist lifecycle ownership before launch: %w", err))
	}
	if err := prepared.Release(ctx); err != nil {
		primaryErr := fmt.Errorf("release lifecycle launch gate: %w", err)
		abortResult, abortErr := prepared.Abort(context.WithoutCancel(ctx))
		safetyErr := persistFailedLaunch(ctx, registry, opts.Clock, record, abortResult, abortErr)
		return nil, errors.Join(primaryErr, abortErr, safetyErr, abortStatusError(abortResult))
	}
	return &Lease{registry: registry, platform: platform, clock: opts.Clock, record: record}, nil
}

// Record returns a point-in-time copy of the lease's durable fact.
func (l *Lease) Record() Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return cloneRecord(l.record)
}

// BeginCleanup durably transitions a lease before platform cleanup begins.
func (l *Lease) BeginCleanup(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now().UTC()
	return l.updateDetached(ctx, func(record *Record) {
		record.State = StateCleaning
		record.Timestamps.UpdatedAt = now
		record.Timestamps.CleanupStartedAt = now
	})
}

// CompleteCleanup removes durable ownership only after a typed platform
// observation proves the recorded boundary empty. All safety-state writes are
// detached from the request deadline so cancellation cannot erase evidence.
func (l *Lease) CompleteCleanup(ctx context.Context, evidence ...EscapeEvidence) (CleanupResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	observation, observationErr := l.platform.ObserveBoundary(ctx, cloneRecord(l.record))
	status, statusErr := validateObservation(l.record, observation, false)
	if observationErr != nil {
		status = BoundaryIndeterminate
		statusErr = errors.Join(ErrBoundaryIndeterminate, observationErr)
	}
	if status != BoundaryEmpty {
		now := l.clock.Now().UTC()
		kind := observationKind(status)
		persistErr := l.updateDetached(ctx, func(record *Record) {
			record.State = StateCleanupFailed
			record.Timestamps.UpdatedAt = now
			record.EscapeEvidence = append(record.EscapeEvidence, evidence...)
			record.EscapeEvidence = append(record.EscapeEvidence, EscapeEvidence{
				Kind: kind, Detail: observationDetail(observation, statusErr), ObservedAt: now,
			})
		})
		return CleanupResult{State: StateCleanupFailed, BoundaryEmpty: false, RecordRetained: true}, errors.Join(statusErr, persistErr)
	}

	now := l.clock.Now().UTC()
	if err := l.updateDetached(ctx, func(record *Record) {
		record.State = StateCompleted
		record.Timestamps.UpdatedAt = now
		record.Timestamps.CleanupCompletedAt = now
		record.EscapeEvidence = append(record.EscapeEvidence, evidence...)
	}); err != nil {
		return CleanupResult{State: l.record.State, BoundaryEmpty: true, RecordRetained: true}, err
	}
	if err := l.registry.Delete(context.WithoutCancel(ctx), l.record.RecordID, l.record.Revision); err != nil {
		return CleanupResult{State: StateCompleted, BoundaryEmpty: true, RecordRetained: true}, err
	}
	return CleanupResult{State: StateCompleted, BoundaryEmpty: true, RecordRetained: false}, nil
}

// Recover loads a record and asks the platform to identify the exact recorded
// containment boundary. A live matching original owner is not adopted; a gone
// owner is the normal crash-recovery case. Cross-process adoption claims are a
// required follow-up before startup recovery is wired concurrently.
func Recover(ctx context.Context, recordID string, registry Registry, platform PlatformBackend, clock Clock) (*Lease, error) {
	if registry == nil || platform == nil {
		return nil, fmt.Errorf("%w: registry and platform backend are required", ErrInvalidRecord)
	}
	if clock == nil {
		clock = systemClock{}
	}
	record, err := registry.Get(ctx, recordID)
	if err != nil {
		return nil, err
	}
	observation, observationErr := platform.ObserveBoundary(ctx, cloneRecord(record))
	status, statusErr := validateObservation(record, observation, true)
	if observationErr != nil {
		status = BoundaryIndeterminate
		statusErr = errors.Join(ErrBoundaryIndeterminate, observationErr)
	}

	switch status {
	case BoundaryEmpty:
		now := clock.Now().UTC()
		updated, updateErr := updateRecordDetached(ctx, registry, record, func(candidate *Record) {
			candidate.State = StateCompleted
			candidate.Timestamps.UpdatedAt = now
			candidate.Timestamps.CleanupCompletedAt = now
		})
		if updateErr != nil {
			return nil, updateErr
		}
		if err := registry.Delete(context.WithoutCancel(ctx), updated.RecordID, updated.Revision); err != nil {
			return nil, errors.Join(ErrBoundaryAlreadyEmpty, err)
		}
		return nil, ErrBoundaryAlreadyEmpty
	case BoundaryMatching:
		if observation.OwnerStatus == OwnerMatching {
			return nil, ErrBoundaryOwned
		}
		return &Lease{registry: registry, platform: platform, clock: clock, record: record}, nil
	case BoundaryMismatch, BoundaryIndeterminate:
		now := clock.Now().UTC()
		_, persistErr := updateRecordDetached(ctx, registry, record, func(candidate *Record) {
			candidate.State = StateRecoveryBlocked
			candidate.Timestamps.UpdatedAt = now
			candidate.EscapeEvidence = append(candidate.EscapeEvidence, EscapeEvidence{
				Kind: observationKind(status), Detail: observationDetail(observation, statusErr), ObservedAt: now,
			})
		})
		return nil, errors.Join(statusErr, persistErr)
	default:
		return nil, ErrBoundaryIndeterminate
	}
}

func (l *Lease) updateDetached(ctx context.Context, mutate func(*Record)) error {
	updated, err := updateRecordDetached(ctx, l.registry, l.record, mutate)
	if err == nil {
		l.record = updated
	}
	return err
}

func updateRecordDetached(ctx context.Context, registry Registry, record Record, mutate func(*Record)) (Record, error) {
	candidate := cloneRecord(record)
	expectedRevision := candidate.Revision
	mutate(&candidate)
	candidate.Revision++
	if err := registry.Update(context.WithoutCancel(ctx), candidate, expectedRevision); err != nil {
		return record, err
	}
	return candidate, nil
}

func validateObservation(record Record, observation BoundaryObservation, recovery bool) (BoundaryObservationStatus, error) {
	switch observation.Status {
	case BoundaryEmpty:
		if observation.BoundaryIdentity != record.BoundaryIdentity {
			return BoundaryMismatch, ErrIdentityMismatch
		}
		return BoundaryEmpty, nil
	case BoundaryMismatch:
		return BoundaryMismatch, ErrIdentityMismatch
	case BoundaryIndeterminate:
		return BoundaryIndeterminate, ErrBoundaryIndeterminate
	case BoundaryMatching:
		if observation.BoundaryIdentity != record.BoundaryIdentity || !record.BoundaryProcessIdentity.matches(observation.BoundaryProcessIdentity) {
			return BoundaryMismatch, ErrIdentityMismatch
		}
		switch observation.OwnerStatus {
		case OwnerMatching:
			if !record.OwnerIdentity.matches(observation.OwnerIdentity) {
				return BoundaryMismatch, ErrIdentityMismatch
			}
		case OwnerGone:
		case OwnerMismatch:
			return BoundaryMismatch, ErrIdentityMismatch
		case OwnerIndeterminate:
			return BoundaryIndeterminate, ErrBoundaryIndeterminate
		default:
			return BoundaryIndeterminate, ErrBoundaryIndeterminate
		}
		if recovery && observation.OwnerStatus == OwnerMatching {
			return BoundaryMatching, ErrBoundaryOwned
		}
		return BoundaryMatching, ErrBoundaryNotEmpty
	default:
		return BoundaryIndeterminate, ErrBoundaryIndeterminate
	}
}

func persistFailedLaunch(ctx context.Context, registry Registry, clock Clock, record Record, result AbortResult, abortErr error) error {
	now := clock.Now().UTC()
	if result.Status == AbortEmpty && abortErr == nil {
		updated, err := updateRecordDetached(ctx, registry, record, func(candidate *Record) {
			candidate.State = StateCompleted
			candidate.Timestamps.UpdatedAt = now
			candidate.Timestamps.CleanupCompletedAt = now
		})
		if err != nil {
			return err
		}
		return registry.Delete(context.WithoutCancel(ctx), updated.RecordID, updated.Revision)
	}
	_, err := updateRecordDetached(ctx, registry, record, func(candidate *Record) {
		candidate.State = StateCleanupFailed
		candidate.Timestamps.UpdatedAt = now
		candidate.EscapeEvidence = append(candidate.EscapeEvidence, EscapeEvidence{
			Kind: "launch_abort_indeterminate", Detail: abortDetail(result, abortErr), ObservedAt: now,
		})
	})
	return err
}

func abortLaunch(ctx context.Context, prepared PreparedBoundary, cause error) error {
	result, abortErr := prepared.Abort(context.WithoutCancel(ctx))
	return errors.Join(cause, abortErr, abortStatusError(result))
}

func abortStatusError(result AbortResult) error {
	if result.Status == AbortEmpty {
		return nil
	}
	if result.Detail == "" {
		return ErrAbortIndeterminate
	}
	return fmt.Errorf("%w: %s", ErrAbortIndeterminate, result.Detail)
}

func abortDetail(result AbortResult, abortErr error) string {
	if abortErr != nil && result.Detail != "" {
		return result.Detail + ": " + abortErr.Error()
	}
	if abortErr != nil {
		return abortErr.Error()
	}
	if result.Detail != "" {
		return result.Detail
	}
	return ErrAbortIndeterminate.Error()
}

func observationKind(status BoundaryObservationStatus) string {
	switch status {
	case BoundaryMatching:
		return "boundary_not_empty"
	case BoundaryMismatch:
		return "boundary_identity_mismatch"
	default:
		return "boundary_indeterminate"
	}
}

func observationDetail(observation BoundaryObservation, err error) string {
	if observation.Detail != "" && err != nil {
		return observation.Detail + ": " + err.Error()
	}
	if observation.Detail != "" {
		return observation.Detail
	}
	if err != nil {
		return err.Error()
	}
	return "containment boundary observation did not prove emptiness"
}

func newRecordID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate lifecycle record ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// CleanupContext returns a deadline-bounded context detached from a cancelled
// request. Platform backends use it for service-owned cleanup work.
func CleanupContext(request context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(request)
	if timeout <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, timeout)
}
