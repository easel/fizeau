package processlifecycle

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"
)

const defaultStaleRecoveryTimeout = 10 * time.Second

// ReapStaleRecords adopts old lifecycle records one at a time under an
// OS-backed claim. Invalid, legacy, and future records remain untouched.
// A record is deleted only by Recover/CompleteCleanup after the backend has
// observed the exact recorded boundary empty.
func ReapStaleRecords(ctx context.Context, registry *FileRegistry, grace time.Duration, now time.Time) error {
	if registry == nil {
		return ErrInvalidRecord
	}
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := registry.recoverableRecords(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, listed := range records {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if blocksAutomaticRecovery(listed) {
			continue
		}
		if listed.State != StateCompleted && grace > 0 && now.Sub(listed.Timestamps.UpdatedAt) < grace {
			continue
		}
		release, err := registry.claimRecovery(listed.RecordID)
		if errors.Is(err, ErrRecoveryClaimed) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		err = reapClaimedRecord(ctx, registry, listed.RecordID, grace, now)
		release()
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func reapClaimedRecord(ctx context.Context, registry *FileRegistry, recordID string, grace time.Duration, now time.Time) error {
	record, err := registry.Get(ctx, recordID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if blocksAutomaticRecovery(record) {
		return nil
	}
	if record.State != StateCompleted && grace > 0 && now.Sub(record.Timestamps.UpdatedAt) < grace {
		return nil
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStaleRecoveryTimeout)
	defer cancel()
	return reapClaimedPlatformRecord(recoveryCtx, registry, record)
}

// blocksAutomaticRecovery preserves unresolved identity/escape evidence until
// an operator explicitly resolves it. Retrying after the conflicting process
// disappears would turn "identity no longer attributable" into a false empty
// verdict and erase the evidence on a later startup.
func blocksAutomaticRecovery(record Record) bool {
	if record.State == StateRecoveryBlocked {
		return true
	}
	for _, evidence := range record.EscapeEvidence {
		kind := strings.ToLower(evidence.Kind)
		if strings.Contains(kind, "identity_mismatch") || strings.Contains(kind, "containment_escape") {
			return true
		}
	}
	return false
}
