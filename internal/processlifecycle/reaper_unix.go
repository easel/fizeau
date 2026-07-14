//go:build linux || darwin

package processlifecycle

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func reapClaimedPlatformRecord(ctx context.Context, registry *FileRegistry, record Record) error {
	backend := &unixBackend{recovery: true}
	lease, err := Recover(ctx, record.RecordID, registry, backend, systemClock{})
	switch {
	case errors.Is(err, ErrBoundaryAlreadyEmpty):
		return nil
	case errors.Is(err, ErrBoundaryOwned), errors.Is(err, ErrIdentityMismatch), errors.Is(err, ErrBoundaryIndeterminate):
		// Recover retained typed evidence. Never signal an owned, reused, or
		// indeterminate identity.
		return nil
	case err != nil:
		return err
	}
	if lease == nil {
		return nil
	}
	return reapRecoveredUnixLease(ctx, lease, backend, unixRecoveryOps{
		grace: defaultBatchGracePeriod,
		signal: func(pgid int, signal syscall.Signal) error {
			return syscall.Kill(-pgid, signal)
		},
	})
}

type unixRecoveryOps struct {
	grace  time.Duration
	signal func(int, syscall.Signal) error
}

func reapRecoveredUnixLease(ctx context.Context, lease *Lease, backend PlatformBackend, ops unixRecoveryOps) error {
	if lease == nil || backend == nil || ops.signal == nil {
		return ErrInvalidRecord
	}
	record := lease.Record()
	pgid, err := parseUnixProcessGroupIdentity(record.BoundaryIdentity)
	if err != nil || pgid <= 0 {
		return err
	}
	if err := lease.BeginCleanup(ctx); err != nil {
		return err
	}

	// The adoption observation may be stale by the time the durable cleaning
	// transition lands. Revalidate the exact births and PGID membership after
	// BeginCleanup and immediately before every signal.
	observation, status, statusErr := revalidateRecoveredUnixBoundary(ctx, lease, backend)
	if status != BoundaryMatching || observation.OwnerStatus == OwnerMatching {
		return finishRecoveryWithoutSignal(ctx, lease, observation, status, statusErr)
	}

	termErr := ignoreGoneProcessGroup(ops.signal(pgid, syscall.SIGTERM))
	if ops.grace > 0 {
		timer := time.NewTimer(ops.grace)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
	observation, status, statusErr = revalidateRecoveredUnixBoundary(ctx, lease, backend)
	if status != BoundaryMatching || observation.OwnerStatus == OwnerMatching {
		if status == BoundaryIndeterminate {
			observation = waitForBoundaryEmpty(ctx, backend, lease.Record())
			status, statusErr = validateObservation(lease.Record(), observation, true)
		}
		return errors.Join(termErr, finishRecoveryWithoutSignal(ctx, lease, observation, status, statusErr))
	}
	killErr := ignoreGoneProcessGroup(ops.signal(pgid, syscall.SIGKILL))
	observation = waitForBoundaryEmpty(ctx, backend, lease.Record())
	result, completeErr := lease.CompleteCleanup(ctx)
	if result.BoundaryEmpty && !result.RecordRetained {
		return errors.Join(termErr, killErr)
	}
	if observation.Status == BoundaryMismatch {
		return errors.Join(ErrIdentityMismatch, termErr, killErr, completeErr)
	}
	if observation.Status == BoundaryIndeterminate {
		return errors.Join(ErrBoundaryIndeterminate, termErr, killErr, completeErr)
	}
	return errors.Join(ErrBoundaryNotEmpty, termErr, killErr, completeErr)
}

func revalidateRecoveredUnixBoundary(ctx context.Context, lease *Lease, backend PlatformBackend) (BoundaryObservation, BoundaryObservationStatus, error) {
	record := lease.Record()
	observation, observationErr := backend.ObserveBoundary(ctx, record)
	status, statusErr := validateObservation(record, observation, true)
	if observationErr != nil {
		status = BoundaryIndeterminate
		statusErr = errors.Join(ErrBoundaryIndeterminate, observationErr)
	}
	return observation, status, statusErr
}

func finishRecoveryWithoutSignal(ctx context.Context, lease *Lease, observation BoundaryObservation, status BoundaryObservationStatus, statusErr error) error {
	evidence := EscapeEvidence{
		Kind:   "recovery_pre_signal_" + observationKind(status),
		Detail: observationDetail(observation, statusErr), ObservedAt: time.Now().UTC(),
	}
	if status == BoundaryEmpty {
		_, completeErr := lease.CompleteCleanup(ctx, evidence)
		return completeErr
	}
	// Do not ask the platform for a second verdict after mismatch or
	// indeterminacy. If the reused/escaped process exits between observations,
	// a fresh empty verdict must not delete the evidence we just observed.
	persistErr := lease.retainRecoveryBlock(ctx, evidence)
	return errors.Join(statusErr, persistErr)
}

func ignoreGoneProcessGroup(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
