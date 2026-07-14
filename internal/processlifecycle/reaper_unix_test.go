//go:build linux || darwin

package processlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const staleRecoveryHelperEnv = "FIZEAU_STALE_RECOVERY_HELPER"
const staleRecoveryExitSupervisorEnv = "FIZEAU_STALE_RECOVERY_EXIT_SUPERVISOR"

type staleRecoveryFixture struct {
	TargetPID          int             `json:"target_pid"`
	SupervisorIdentity ProcessIdentity `json:"supervisor_identity"`
	TargetIdentity     ProcessIdentity `json:"target_identity"`
}

func TestStaleRecoveryReapsMatchingLiveBoundaryAndDeletesAfterEmpty(t *testing.T) {
	helper, record, registry := startStaleRecoveryFixture(t, false)
	helperDone := make(chan error, 1)
	go func() { helperDone <- helper.Wait() }()

	if err := ReapStaleRecords(context.Background(), registry, time.Millisecond, time.Now().UTC()); err != nil {
		t.Fatalf("ReapStaleRecords: %v", err)
	}
	select {
	case <-helperDone:
	case <-time.After(2 * time.Second):
		t.Fatal("trusted supervisor was not reaped")
	}
	if _, err := registry.Get(context.Background(), record.RecordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("record removed before/without confirmed emptiness: %v", err)
	}
	if alive, err := unixProcessGroupAlive(record.DirectChildIdentity.PID); err != nil || alive {
		t.Fatalf("recorded process group remains alive: alive=%v err=%v", alive, err)
	}
}

func TestStaleRecoveryReapsMatchingGroupAfterSupervisorExit(t *testing.T) {
	helper, record, registry := startStaleRecoveryFixtureWithOptions(t, false, true)
	if err := helper.Wait(); err != nil {
		t.Fatalf("wait for exited supervisor: %v", err)
	}
	if _, err := readUnixProcessIdentity(record.SupervisorIdentity.PID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("supervisor still observable: %v", err)
	}
	if alive, err := unixProcessGroupAlive(record.DirectChildIdentity.PID); err != nil || !alive {
		t.Fatalf("orphaned recorded group not alive before recovery: alive=%v err=%v", alive, err)
	}
	defer syscall.Kill(-record.DirectChildIdentity.PID, syscall.SIGKILL) //nolint:errcheck -- test cleanup

	if err := ReapStaleRecords(context.Background(), registry, time.Millisecond, time.Now().UTC()); err != nil {
		t.Fatalf("ReapStaleRecords: %v", err)
	}
	if _, err := registry.Get(context.Background(), record.RecordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("record retained after orphaned matching group became empty: %v", err)
	}
	if alive, err := unixProcessGroupAlive(record.DirectChildIdentity.PID); err != nil || alive {
		t.Fatalf("orphaned recorded group remains alive: alive=%v err=%v", alive, err)
	}
}

func TestStaleRecoveryRefusesReusedPIDOrPGID(t *testing.T) {
	helper, record, registry := startStaleRecoveryFixture(t, true)
	if err := ReapStaleRecords(context.Background(), registry, time.Millisecond, time.Now().UTC()); err != nil {
		t.Fatalf("ReapStaleRecords: %v", err)
	}
	if alive, err := unixProcessGroupAlive(record.DirectChildIdentity.PID); err != nil || !alive {
		t.Fatalf("identity mismatch signalled the current process group: alive=%v err=%v", alive, err)
	}
	retained, err := registry.Get(context.Background(), record.RecordID)
	if err != nil {
		t.Fatalf("mismatched record not retained: %v", err)
	}
	if retained.State != StateRecoveryBlocked || len(retained.EscapeEvidence) == 0 {
		t.Fatalf("mismatched identity evidence = %+v", retained)
	}
	_ = syscall.Kill(-record.DirectChildIdentity.PID, syscall.SIGKILL)
	_ = helper.Wait()
	if err := ReapStaleRecords(context.Background(), registry, 0, time.Now().UTC()); err != nil {
		t.Fatalf("second ReapStaleRecords: %v", err)
	}
	retained, err = registry.Get(context.Background(), record.RecordID)
	if err != nil || retained.State != StateRecoveryBlocked {
		t.Fatalf("later disappearance erased mismatch evidence: record=%+v err=%v", retained, err)
	}
}

func TestStaleRecoveryUsesExclusiveAdoptionClaim(t *testing.T) {
	registry := NewFileRegistry(t.TempDir())
	record := staleRecoveryRecordForTest(t, os.Getpid(), os.Getpid(), true)
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	release, err := registry.claimRecovery(record.RecordID)
	if err != nil {
		t.Fatalf("claimRecovery: %v", err)
	}
	defer release()
	if err := ReapStaleRecords(context.Background(), registry, 0, time.Now().UTC()); err != nil {
		t.Fatalf("ReapStaleRecords: %v", err)
	}
	if _, err := registry.Get(context.Background(), record.RecordID); err != nil {
		t.Fatalf("claimed record was mutated by competing reaper: %v", err)
	}
}

func TestStaleRecoveryDoesNotRetryCleanupFailedIdentityEvidence(t *testing.T) {
	for _, kind := range []string{"boundary_identity_mismatch", "identity_mismatch", "containment_escape"} {
		t.Run(kind, func(t *testing.T) {
			registry := NewFileRegistry(t.TempDir())
			missing := ProcessIdentity{PID: 999999, BirthTokenScheme: "test/v1", BirthToken: "gone"}
			updated := time.Now().Add(-time.Hour).UTC()
			record := Record{
				SchemaID: RecordSchemaID, RecordID: "blocked-evidence-record", Revision: 1,
				OperationID: "blocked-evidence-operation", Harness: "codex",
				OwnerIdentity: missing, SupervisorIdentity: missing, DirectChildIdentity: missing,
				BoundaryIdentity: unixProcessGroupIdentity(unusedProcessGroup(t)), BoundaryType: BoundaryTypeUnixProcessGroup,
				BoundaryProcessIdentity: missing, State: StateCleanupFailed,
				Timestamps:     Timestamps{CreatedAt: updated, UpdatedAt: updated},
				EscapeEvidence: []EscapeEvidence{{Kind: kind, ObservedAt: updated}},
			}
			if err := registry.Create(context.Background(), record); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := ReapStaleRecords(context.Background(), registry, 0, time.Now().UTC()); err != nil {
				t.Fatalf("ReapStaleRecords: %v", err)
			}
			retained, err := registry.Get(context.Background(), record.RecordID)
			if err != nil || retained.Revision != 1 || retained.State != StateCleanupFailed {
				t.Fatalf("blocked cleanup evidence was adopted: record=%+v err=%v", retained, err)
			}
		})
	}
}

func TestEmptyRecordedGroupRetainsLiveOrReusedRecordedPID(t *testing.T) {
	current, err := readUnixProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("current identity: %v", err)
	}
	missingPGID := unusedProcessGroup(t)
	for _, tc := range []struct {
		name     string
		identity ProcessIdentity
	}{
		{name: "live outside group", identity: current},
		{name: "reused pid", identity: ProcessIdentity{PID: current.PID, BirthTokenScheme: current.BirthTokenScheme, BirthToken: current.BirthToken + "-reused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewFileRegistry(t.TempDir())
			missing := ProcessIdentity{PID: 999999, BirthTokenScheme: "test/v1", BirthToken: "gone"}
			updated := time.Now().Add(-time.Hour).UTC()
			record := Record{
				SchemaID: RecordSchemaID, RecordID: "empty-pgid-record", Revision: 1,
				OperationID: "empty-pgid-operation", Harness: "codex",
				OwnerIdentity: missing, SupervisorIdentity: missing, DirectChildIdentity: tc.identity,
				BoundaryIdentity: unixProcessGroupIdentity(missingPGID), BoundaryType: BoundaryTypeUnixProcessGroup,
				BoundaryProcessIdentity: tc.identity, State: StateCleanupFailed,
				Timestamps:     Timestamps{CreatedAt: updated, UpdatedAt: updated},
				EscapeEvidence: []EscapeEvidence{{Kind: "prior_cleanup_failed", ObservedAt: updated}},
			}
			if err := registry.Create(context.Background(), record); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := ReapStaleRecords(context.Background(), registry, 0, time.Now().UTC()); err != nil {
				t.Fatalf("ReapStaleRecords: %v", err)
			}
			retained, err := registry.Get(context.Background(), record.RecordID)
			if err != nil {
				t.Fatalf("unsafe empty-boundary record deleted: %v", err)
			}
			if retained.State != StateRecoveryBlocked || len(retained.EscapeEvidence) < 2 {
				t.Fatalf("unsafe empty-boundary evidence = %+v", retained)
			}
		})
	}
}

func TestEmptyRecordedGroupDoesNotDeleteLiveOrReusedOwner(t *testing.T) {
	current, err := readUnixProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("current identity: %v", err)
	}
	missingPGID := unusedProcessGroup(t)
	for _, tc := range []struct {
		name  string
		owner ProcessIdentity
	}{
		{name: "matching live owner", owner: current},
		{name: "reused owner pid", owner: ProcessIdentity{PID: current.PID, BirthTokenScheme: current.BirthTokenScheme, BirthToken: current.BirthToken + "-reused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewFileRegistry(t.TempDir())
			missing := ProcessIdentity{PID: 999999, BirthTokenScheme: "test/v1", BirthToken: "gone"}
			updated := time.Now().Add(-time.Hour).UTC()
			record := Record{
				SchemaID: RecordSchemaID, RecordID: "empty-owner-record", Revision: 1,
				OperationID: "empty-owner-operation", Harness: "codex",
				OwnerIdentity: tc.owner, SupervisorIdentity: missing, DirectChildIdentity: missing,
				BoundaryIdentity: unixProcessGroupIdentity(missingPGID), BoundaryType: BoundaryTypeUnixProcessGroup,
				BoundaryProcessIdentity: missing, State: StateCleanupFailed,
				Timestamps:     Timestamps{CreatedAt: updated, UpdatedAt: updated},
				EscapeEvidence: []EscapeEvidence{{Kind: "prior_cleanup_failed", ObservedAt: updated}},
			}
			if err := registry.Create(context.Background(), record); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := ReapStaleRecords(context.Background(), registry, 0, time.Now().UTC()); err != nil {
				t.Fatalf("ReapStaleRecords: %v", err)
			}
			if _, err := registry.Get(context.Background(), record.RecordID); err != nil {
				t.Fatalf("live/reused owner evidence was deleted: %v", err)
			}
		})
	}
}

func TestRecoveryRevalidatesAfterBeginCleanupBeforeSignal(t *testing.T) {
	registry := NewMemoryRegistry()
	record := orderedRecoveryRecord()
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	matching := orderedRecoveryMatchingObservation(record)
	backend := &sequencedRecoveryBackend{observations: []BoundaryObservation{
		matching,
		matching,
		matching,
		{Status: BoundaryEmpty, BoundaryIdentity: record.BoundaryIdentity},
		{Status: BoundaryEmpty, BoundaryIdentity: record.BoundaryIdentity},
	}}
	lease, err := Recover(context.Background(), record.RecordID, registry, backend, &fakeClock{now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	signals := 0
	err = reapRecoveredUnixLease(context.Background(), lease, backend, unixRecoveryOps{
		signal: func(_ int, _ syscall.Signal) error {
			signals++
			if backend.calls < signals+1 {
				t.Error("signal preceded recovery revalidation")
			}
			persisted, getErr := registry.Get(context.Background(), record.RecordID)
			if getErr != nil || persisted.State != StateCleaning {
				t.Errorf("signal preceded durable cleaning transition: record=%+v err=%v", persisted, getErr)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reapRecoveredUnixLease: %v", err)
	}
	if signals != 2 {
		t.Fatalf("signals = %d, want graceful and forceful", signals)
	}
	if _, err := registry.Get(context.Background(), record.RecordID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty recovery record retained: %v", err)
	}
}

func TestRecoveryIdentityChangeBeforeSignalIsRetained(t *testing.T) {
	registry := NewMemoryRegistry()
	record := orderedRecoveryRecord()
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	matching := orderedRecoveryMatchingObservation(record)
	mismatch := BoundaryObservation{Status: BoundaryMismatch, BoundaryIdentity: record.BoundaryIdentity, Detail: "identity changed after adoption"}
	backend := &sequencedRecoveryBackend{observations: []BoundaryObservation{
		matching,
		mismatch,
		// Even if a later observation would be empty, the mismatch evidence
		// already seen immediately before signaling must remain durable.
		{Status: BoundaryEmpty, BoundaryIdentity: record.BoundaryIdentity},
	}}
	lease, err := Recover(context.Background(), record.RecordID, registry, backend, &fakeClock{now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	signals := 0
	err = reapRecoveredUnixLease(context.Background(), lease, backend, unixRecoveryOps{signal: func(int, syscall.Signal) error {
		signals++
		return nil
	}})
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("revalidation error = %v, want ErrIdentityMismatch", err)
	}
	if signals != 0 {
		t.Fatalf("identity changed before signal but received %d signals", signals)
	}
	if backend.calls != 2 {
		t.Fatalf("identity mismatch was re-observed %d times; want adoption plus one pre-signal check", backend.calls)
	}
	retained, getErr := registry.Get(context.Background(), record.RecordID)
	if getErr != nil || retained.State != StateRecoveryBlocked {
		t.Fatalf("changed identity evidence not retained: record=%+v err=%v", retained, getErr)
	}
}

func TestStaleRecoveryHelper(t *testing.T) {
	path := os.Getenv(staleRecoveryHelperEnv)
	if path == "" {
		return
	}
	target := exec.Command("sh", "-c", "exec sleep 300")
	target.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := target.Start(); err != nil {
		os.Exit(2)
	}
	supervisorIdentity, supervisorErr := readUnixProcessIdentity(os.Getpid())
	targetIdentity, targetErr := readUnixProcessIdentity(target.Process.Pid)
	if supervisorErr != nil || targetErr != nil {
		_ = target.Process.Kill()
		os.Exit(4)
	}
	data, _ := json.Marshal(staleRecoveryFixture{TargetPID: target.Process.Pid, SupervisorIdentity: supervisorIdentity, TargetIdentity: targetIdentity})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = target.Process.Kill()
		os.Exit(3)
	}
	if os.Getenv(staleRecoveryExitSupervisorEnv) == "1" {
		os.Exit(0)
	}
	_ = target.Wait()
	os.Exit(0)
}

func startStaleRecoveryFixture(t *testing.T, mismatched bool) (*exec.Cmd, Record, *FileRegistry) {
	return startStaleRecoveryFixtureWithOptions(t, mismatched, false)
}

func startStaleRecoveryFixtureWithOptions(t *testing.T, mismatched, exitSupervisor bool) (*exec.Cmd, Record, *FileRegistry) {
	t.Helper()
	dir := t.TempDir()
	descriptorPath := filepath.Join(dir, "fixture.json")
	helper := exec.Command(os.Args[0], "-test.run=^TestStaleRecoveryHelper$", "-test.count=1")
	helper.Env = append(os.Environ(), staleRecoveryHelperEnv+"="+descriptorPath)
	if exitSupervisor {
		helper.Env = append(helper.Env, staleRecoveryExitSupervisorEnv+"=1")
	}
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})
	deadline := time.Now().Add(3 * time.Second)
	var fixture staleRecoveryFixture
	for {
		data, err := os.ReadFile(descriptorPath)
		if err == nil && json.Unmarshal(data, &fixture) == nil && fixture.TargetPID > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not publish target identity")
		}
		time.Sleep(10 * time.Millisecond)
	}
	targetIdentity := fixture.TargetIdentity
	if mismatched {
		targetIdentity.BirthToken += "-reused"
	}
	record := staleRecoveryRecordFromIdentities(fixture.SupervisorIdentity, targetIdentity)
	registry := NewFileRegistry(filepath.Join(dir, "registry"))
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create lifecycle record: %v", err)
	}
	return helper, record, registry
}

func staleRecoveryRecordFromIdentities(supervisor, target ProcessIdentity) Record {
	updated := time.Now().Add(-time.Hour).UTC()
	return Record{
		SchemaID: RecordSchemaID, RecordID: "stale-recovery-record", Revision: 1,
		OperationID: "stale-recovery-operation", Harness: "codex",
		OwnerIdentity:      ProcessIdentity{PID: 999999, BirthTokenScheme: "test/v1", BirthToken: "gone-owner"},
		SupervisorIdentity: supervisor, DirectChildIdentity: target,
		BoundaryIdentity: unixProcessGroupIdentity(target.PID), BoundaryType: BoundaryTypeUnixProcessGroup,
		BoundaryProcessIdentity: target, State: StateCleanupFailed,
		Timestamps:     Timestamps{CreatedAt: updated, UpdatedAt: updated},
		EscapeEvidence: []EscapeEvidence{{Kind: "prior_cleanup_failed", ObservedAt: updated}},
	}
}

func staleRecoveryRecordForTest(t *testing.T, supervisorPID, targetPID int, mismatched bool) Record {
	t.Helper()
	supervisor, err := readUnixProcessIdentity(supervisorPID)
	if err != nil {
		t.Fatalf("supervisor identity: %v", err)
	}
	target, err := readUnixProcessIdentity(targetPID)
	if err != nil {
		t.Fatalf("target identity: %v", err)
	}
	if mismatched {
		target.BirthToken += "-reused"
	}
	return staleRecoveryRecordFromIdentities(supervisor, target)
}

func unusedProcessGroup(t *testing.T) int {
	t.Helper()
	for pgid := 900000; pgid < 1000000; pgid++ {
		alive, err := unixProcessGroupAlive(pgid)
		if err == nil && !alive {
			return pgid
		}
	}
	t.Fatal("could not find an unused process group")
	return 0
}

type sequencedRecoveryBackend struct {
	observations []BoundaryObservation
	calls        int
}

func (b *sequencedRecoveryBackend) ObserveBoundary(context.Context, Record) (BoundaryObservation, error) {
	index := b.calls
	b.calls++
	if index >= len(b.observations) {
		index = len(b.observations) - 1
	}
	return b.observations[index], nil
}

func orderedRecoveryRecord() Record {
	now := time.Now().UTC()
	return Record{
		SchemaID: RecordSchemaID, RecordID: "ordered-recovery", Revision: 1,
		OperationID: "ordered-operation", Harness: "codex",
		OwnerIdentity:       ProcessIdentity{PID: 101, BirthTokenScheme: "test/v1", BirthToken: "owner"},
		SupervisorIdentity:  ProcessIdentity{PID: 150, BirthTokenScheme: "test/v1", BirthToken: "supervisor"},
		DirectChildIdentity: ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "child"},
		BoundaryIdentity:    unixProcessGroupIdentity(202), BoundaryType: BoundaryTypeUnixProcessGroup,
		BoundaryProcessIdentity: ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "child"},
		State:                   StateCleanupFailed, Timestamps: Timestamps{CreatedAt: now, UpdatedAt: now},
		EscapeEvidence: []EscapeEvidence{},
	}
}

func orderedRecoveryMatchingObservation(record Record) BoundaryObservation {
	return BoundaryObservation{
		Status: BoundaryMatching, BoundaryIdentity: record.BoundaryIdentity,
		SupervisorStatus: OwnerGone, DirectChildIdentity: record.DirectChildIdentity,
		BoundaryProcessIdentity: record.BoundaryProcessIdentity, OwnerStatus: OwnerGone,
	}
}
