package serviceimpl

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/processlifecycle"
)

type cleanupTestHarness struct {
	execute func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error)
}

// cleanupCoordinationTimeout is deliberately much longer than the assertion
// window below. These tests leave a lifecycle record owned until the test
// goroutine completes the simulated cleanup; under repository-wide -race load
// that goroutine may not be scheduled within a sub-second production deadline.
const cleanupCoordinationTimeout = 10 * time.Second

func (h cleanupTestHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "cleanup-test"}
}
func (h cleanupTestHarness) HealthCheck(context.Context) error { return nil }
func (h cleanupTestHarness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return h.execute(ctx, req)
}

func TestTerminalWaitsForHarnessCleanup(t *testing.T) {
	dir := t.TempDir()
	registry := processlifecycle.NewFileRegistry(dir)
	record := cleanupTestRecord("session-wait", processlifecycle.StateOwned)
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	emitted := make(chan harnesses.FinalData, 1)
	harness := cleanupTestHarness{execute: func(_ context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		if req.SessionID != "session-wait" || req.LifecycleStateDir != dir || req.CleanupTimeout != cleanupCoordinationTimeout {
			t.Errorf("forwarded lifecycle request = session %q dir %q timeout %s", req.SessionID, req.LifecycleStateDir, req.CleanupTimeout)
		}
		ch := make(chan harnesses.Event, 1)
		ch <- cleanupFinalEvent("success")
		close(ch)
		return ch, nil
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSubprocess(context.Background(), SubprocessRequest{
			SessionID: "session-wait", LifecycleStateDir: dir, CleanupTimeout: cleanupCoordinationTimeout,
		}, harness, SubprocessCallbacks{EmitEvent: captureCleanupFinal(t, emitted)})
	}()
	select {
	case final := <-emitted:
		t.Fatalf("terminal emitted before cleanup: %+v", final)
	case <-time.After(30 * time.Millisecond):
	}
	record.State = processlifecycle.StateCompleted
	record.Revision = 2
	record.Timestamps.UpdatedAt = time.Now().UTC()
	record.Timestamps.CleanupCompletedAt = record.Timestamps.UpdatedAt
	if err := registry.Update(context.Background(), record, 1); err != nil {
		t.Fatalf("Update completed: %v", err)
	}
	if err := registry.Delete(context.Background(), record.RecordID, 2); err != nil {
		t.Fatalf("Delete completed: %v", err)
	}
	select {
	case final := <-emitted:
		if final.Outcome != harnesses.SessionOutcomeSuccess || final.Cause != harnesses.TerminalCauseCompleted || final.Stage != harnesses.SessionStageHarness {
			t.Fatalf("terminal after cleanup = %+v", final)
		}
	case <-time.After(cleanupCoordinationTimeout):
		t.Fatal("terminal did not follow cleanup success")
	}
	<-done
}

func TestCancelledSubprocessStillDeliversTerminalAfterDetachedCleanup(t *testing.T) {
	dir := t.TempDir()
	registry := processlifecycle.NewFileRegistry(dir)
	record := cleanupTestRecord("session-cancelled", processlifecycle.StateOwned)
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	harness := cleanupTestHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		ch := make(chan harnesses.Event, 2)
		ch <- harnesses.Event{Type: harnesses.EventTypeProgress, Data: json.RawMessage(`{}`)}
		ch <- cleanupFinalEvent("cancelled")
		close(ch)
		return ch, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	emitted := make(chan harnesses.FinalData, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSubprocess(ctx, SubprocessRequest{
			SessionID: "session-cancelled", LifecycleStateDir: dir, CleanupTimeout: cleanupCoordinationTimeout,
		}, harness, SubprocessCallbacks{EmitEvent: func(event harnesses.Event) bool {
			if event.Type != harnesses.EventTypeFinal {
				return false
			}
			return captureCleanupFinal(t, emitted)(event)
		}})
	}()
	select {
	case <-emitted:
		t.Fatal("cancelled terminal preceded cleanup")
	case <-time.After(30 * time.Millisecond):
	}
	record.State = processlifecycle.StateCompleted
	record.Revision = 2
	record.Timestamps.UpdatedAt = time.Now().UTC()
	record.Timestamps.CleanupCompletedAt = record.Timestamps.UpdatedAt
	if err := registry.Update(context.Background(), record, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := registry.Delete(context.Background(), record.RecordID, 2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	select {
	case final := <-emitted:
		if final.Outcome != harnesses.SessionOutcomeCancelled || final.Cause != harnesses.TerminalCauseContextCancelled || final.Stage != harnesses.SessionStageCancellation {
			t.Fatalf("cancelled terminal = %+v", final)
		}
	case <-time.After(cleanupCoordinationTimeout):
		t.Fatal("cancelled terminal was dropped")
	}
	<-done
}

func TestCancelledSubprocessReachesCleanupDeadlineWithoutHarnessFinal(t *testing.T) {
	dir := t.TempDir()
	registry := processlifecycle.NewFileRegistry(dir)
	record := cleanupTestRecord("session-cancelled-no-final", processlifecycle.StateOwned)
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	neverFinal := make(chan harnesses.Event)
	harness := cleanupTestHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		return neverFinal, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	emitted := make(chan harnesses.FinalData, 1)
	started := time.Now()
	RunSubprocess(ctx, SubprocessRequest{
		SessionID: "session-cancelled-no-final", LifecycleStateDir: dir, CleanupTimeout: 25 * time.Millisecond,
	}, harness, SubprocessCallbacks{EmitEvent: captureCleanupFinal(t, emitted)})
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("cleanup deadline elapsed = %s", elapsed)
	}
	final := <-emitted
	if final.Cause != harnesses.TerminalCauseCleanupFailed || final.Stage != harnesses.SessionStageCleanup {
		t.Fatalf("deadline terminal = %+v", final)
	}
	if final.PrimaryOutcome != harnesses.SessionOutcomeCancelled || final.PrimaryCause != harnesses.TerminalCauseContextCancelled || final.PrimaryStage != harnesses.SessionStageCancellation {
		t.Fatalf("deadline primary tuple = %+v", final)
	}
}

func TestExecuteErrorWaitsForHarnessCleanup(t *testing.T) {
	dir := t.TempDir()
	registry := processlifecycle.NewFileRegistry(dir)
	record := cleanupTestRecord("session-execute-error", processlifecycle.StateOwned)
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	harness := cleanupTestHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		return nil, errors.New("spawn setup failed")
	}}
	emitted := make(chan harnesses.FinalData, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunSubprocess(context.Background(), SubprocessRequest{
			SessionID: "session-execute-error", LifecycleStateDir: dir, CleanupTimeout: cleanupCoordinationTimeout,
		}, harness, SubprocessCallbacks{EmitEvent: captureCleanupFinal(t, emitted)})
	}()
	select {
	case final := <-emitted:
		t.Fatalf("execute error terminal preceded cleanup: %+v", final)
	case <-time.After(30 * time.Millisecond):
	}
	record.State = processlifecycle.StateCompleted
	record.Revision = 2
	record.Timestamps.UpdatedAt = time.Now().UTC()
	record.Timestamps.CleanupCompletedAt = record.Timestamps.UpdatedAt
	if err := registry.Update(context.Background(), record, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := registry.Delete(context.Background(), record.RecordID, 2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	select {
	case final := <-emitted:
		if final.Cause != harnesses.TerminalCauseSpawnFailed || final.Stage != harnesses.SessionStageSpawn {
			t.Fatalf("execute error terminal = %+v", final)
		}
	case <-time.After(cleanupCoordinationTimeout):
		t.Fatal("execute error terminal did not follow cleanup")
	}
	<-done
}

func TestCleanupFailureSupersedesPrimaryTuple(t *testing.T) {
	final, _ := runCleanupFailureFixture(t, processlifecycle.StateCleanupFailed, []processlifecycle.EscapeEvidence{{Kind: "boundary_not_empty", Detail: "grandchild remains"}})
	if final.Outcome != harnesses.SessionOutcomeFailed || final.Cause != harnesses.TerminalCauseCleanupFailed || final.Stage != harnesses.SessionStageCleanup {
		t.Fatalf("cleanup tuple = %q/%q/%q", final.Outcome, final.Cause, final.Stage)
	}
	if final.PrimaryOutcome != harnesses.SessionOutcomeSuccess || final.PrimaryCause != harnesses.TerminalCauseCompleted || final.PrimaryStage != harnesses.SessionStageHarness {
		t.Fatalf("primary tuple = %q/%q/%q", final.PrimaryOutcome, final.PrimaryCause, final.PrimaryStage)
	}
}

func TestCommitFinalPreservesCleanupPrimaryTuple(t *testing.T) {
	dir := t.TempDir()
	registry := processlifecycle.NewFileRegistry(dir)
	record := cleanupTestRecord("session-commit-final", processlifecycle.StateCleaning)
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	harness := cleanupTestHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		ch := make(chan harnesses.Event, 1)
		ch <- cleanupFinalEvent("success")
		close(ch)
		return ch, nil
	}}
	committed := make(chan harnesses.FinalData, 1)
	RunSubprocess(context.Background(), SubprocessRequest{
		SessionID: "session-commit-final", LifecycleStateDir: dir, CleanupTimeout: 25 * time.Millisecond,
	}, harness, SubprocessCallbacks{CommitFinal: func(_ harnesses.Event, final harnesses.FinalData) {
		committed <- final
	}})
	final := <-committed
	if final.Cause != harnesses.TerminalCauseCleanupFailed || final.Stage != harnesses.SessionStageCleanup {
		t.Fatalf("cleanup tuple = %q/%q", final.Cause, final.Stage)
	}
	if final.PrimaryOutcome != harnesses.SessionOutcomeSuccess || final.PrimaryCause != harnesses.TerminalCauseCompleted || final.PrimaryStage != harnesses.SessionStageHarness {
		t.Fatalf("primary tuple = %q/%q/%q", final.PrimaryOutcome, final.PrimaryCause, final.PrimaryStage)
	}
}

func TestCleanupFailureRetainsRecoveryRecord(t *testing.T) {
	_, registry := runCleanupFailureFixture(t, processlifecycle.StateCleaning, nil)
	record, err := registry.Get(context.Background(), "cleanup-record")
	if err != nil {
		t.Fatalf("cleanup deadline removed recovery record: %v", err)
	}
	if record.State != processlifecycle.StateCleaning {
		t.Fatalf("retained recovery state = %q", record.State)
	}
}

func TestCompletedButRetainedRecordIsCleanupFailure(t *testing.T) {
	final, registry := runCleanupFailureFixture(t, processlifecycle.StateCompleted, nil)
	if final.Cause != harnesses.TerminalCauseCleanupFailed || final.Stage != harnesses.SessionStageCleanup {
		t.Fatalf("retained completed terminal = %+v", final)
	}
	if final.PrimaryOutcome != harnesses.SessionOutcomeSuccess || final.PrimaryCause != harnesses.TerminalCauseCompleted || final.PrimaryStage != harnesses.SessionStageHarness {
		t.Fatalf("retained completed primary tuple = %+v", final)
	}
	if _, err := registry.Get(context.Background(), "cleanup-record"); err != nil {
		t.Fatalf("retained completed record disappeared: %v", err)
	}
}

func TestContainmentEscapeClassifiedCleanupFailed(t *testing.T) {
	evidence := []processlifecycle.EscapeEvidence{{Kind: "identity_mismatch", Detail: "descendant escaped recorded boundary", ObservedAt: time.Now().UTC()}}
	final, registry := runCleanupFailureFixture(t, processlifecycle.StateRecoveryBlocked, evidence)
	if final.Cause != harnesses.TerminalCauseCleanupFailed || final.Stage != harnesses.SessionStageCleanup {
		t.Fatalf("escape tuple = %+v", final)
	}
	if final.PrimaryOutcome != harnesses.SessionOutcomeSuccess || final.PrimaryCause != harnesses.TerminalCauseCompleted || final.PrimaryStage != harnesses.SessionStageHarness {
		t.Fatalf("escape lost primary tuple: %+v", final)
	}
	if _, err := registry.Get(context.Background(), "cleanup-record"); errors.Is(err, fs.ErrNotExist) {
		t.Fatal("escape evidence was deleted as if reaped")
	}
}

func runCleanupFailureFixture(t *testing.T, state processlifecycle.State, evidence []processlifecycle.EscapeEvidence) (harnesses.FinalData, *processlifecycle.FileRegistry) {
	t.Helper()
	dir := t.TempDir()
	registry := processlifecycle.NewFileRegistry(dir)
	record := cleanupTestRecord("session-failure", state)
	if evidence != nil {
		record.EscapeEvidence = evidence
	}
	if err := registry.Create(context.Background(), record); err != nil {
		t.Fatalf("Create: %v", err)
	}
	harness := cleanupTestHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		ch := make(chan harnesses.Event, 1)
		ch <- cleanupFinalEvent("success")
		close(ch)
		return ch, nil
	}}
	emitted := make(chan harnesses.FinalData, 1)
	RunSubprocess(context.Background(), SubprocessRequest{
		SessionID: "session-failure", LifecycleStateDir: dir, CleanupTimeout: 25 * time.Millisecond,
	}, harness, SubprocessCallbacks{EmitEvent: captureCleanupFinal(t, emitted)})
	select {
	case final := <-emitted:
		return final, registry
	default:
		t.Fatal("missing terminal event")
		return harnesses.FinalData{}, registry
	}
}

func captureCleanupFinal(t *testing.T, emitted chan<- harnesses.FinalData) func(harnesses.Event) bool {
	t.Helper()
	return func(event harnesses.Event) bool {
		if event.Type != harnesses.EventTypeFinal {
			return true
		}
		var final harnesses.FinalData
		if err := json.Unmarshal(event.Data, &final); err != nil {
			t.Errorf("decode final: %v", err)
			return false
		}
		emitted <- final
		return true
	}
}

func cleanupFinalEvent(status string) harnesses.Event {
	data, _ := json.Marshal(harnesses.FinalData{Status: status})
	return harnesses.Event{Type: harnesses.EventTypeFinal, Data: data}
}

func cleanupTestRecord(operationID string, state processlifecycle.State) processlifecycle.Record {
	now := time.Now().UTC()
	identity := processlifecycle.ProcessIdentity{PID: 101, BirthTokenScheme: "test/v1", BirthToken: "birth"}
	return processlifecycle.Record{
		SchemaID: processlifecycle.RecordSchemaID, RecordID: "cleanup-record", Revision: 1,
		OperationID: operationID, Harness: "cleanup-test",
		OwnerIdentity: identity, SupervisorIdentity: identity, DirectChildIdentity: identity,
		BoundaryIdentity: "test:101", BoundaryType: processlifecycle.BoundaryTypeTest,
		BoundaryProcessIdentity: identity, State: state,
		Timestamps: processlifecycle.Timestamps{CreatedAt: now, UpdatedAt: now}, EscapeEvidence: []processlifecycle.EscapeEvidence{},
	}
}
