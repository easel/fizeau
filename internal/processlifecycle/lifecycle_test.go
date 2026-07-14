package processlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time {
	result := c.now
	c.now = c.now.Add(time.Second)
	return result
}

type fakePlatform struct {
	observation   BoundaryObservation
	observeErr    error
	observeCtxErr bool
}

func (p *fakePlatform) ObserveBoundary(ctx context.Context, _ Record) (BoundaryObservation, error) {
	if p.observeCtxErr && ctx.Err() != nil {
		return BoundaryObservation{Status: BoundaryIndeterminate}, ctx.Err()
	}
	return p.observation, p.observeErr
}

type fakePreparedBoundary struct {
	descriptor  BoundaryDescriptor
	released    bool
	aborted     bool
	started     bool
	onRelease   func()
	releaseErr  error
	abortResult AbortResult
	abortErr    error
}

func (g *fakePreparedBoundary) Descriptor() BoundaryDescriptor { return g.descriptor }

func (g *fakePreparedBoundary) Release(context.Context) error {
	g.released = true
	if g.onRelease != nil {
		g.onRelease()
	}
	if g.releaseErr != nil {
		return g.releaseErr
	}
	g.started = true
	return nil
}

func (g *fakePreparedBoundary) Abort(context.Context) (AbortResult, error) {
	g.aborted = true
	return g.abortResult, g.abortErr
}

type observingRegistry struct {
	*MemoryRegistry
	prepared           *fakePreparedBoundary
	persistedBeforeRun bool
}

func (r *observingRegistry) Create(ctx context.Context, record Record) error {
	if !r.prepared.started && record.State == StateOwned {
		r.persistedBeforeRun = true
	}
	return r.MemoryRegistry.Create(ctx, record)
}

func testOptions(clock Clock) Options {
	return Options{RecordID: "record-1", OperationID: "session-1", Harness: "codex", Clock: clock}
}

func testPrepared() *fakePreparedBoundary {
	return &fakePreparedBoundary{
		descriptor: BoundaryDescriptor{
			OwnerIdentity:           ProcessIdentity{PID: 101, BirthTokenScheme: "test/v1", BirthToken: "owner-birth-a"},
			SupervisorIdentity:      ProcessIdentity{PID: 150, BirthTokenScheme: "test/v1", BirthToken: "supervisor-birth-a"},
			DirectChildIdentity:     ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "boundary-birth-a"},
			BoundaryProcessIdentity: ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "boundary-birth-a"},
			BoundaryID:              "pgid:202",
			BoundaryType:            BoundaryTypeTest,
		},
		abortResult: AbortResult{Status: AbortEmpty},
	}
}

func testPlatform() *fakePlatform {
	return &fakePlatform{}
}

func matchingObservation() BoundaryObservation {
	return BoundaryObservation{
		Status:                  BoundaryMatching,
		BoundaryIdentity:        "pgid:202",
		SupervisorIdentity:      ProcessIdentity{PID: 150, BirthTokenScheme: "test/v1", BirthToken: "supervisor-birth-a"},
		DirectChildIdentity:     ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "boundary-birth-a"},
		BoundaryProcessIdentity: ProcessIdentity{PID: 202, BirthTokenScheme: "test/v1", BirthToken: "boundary-birth-a"},
		OwnerStatus:             OwnerMatching,
		OwnerIdentity:           ProcessIdentity{PID: 101, BirthTokenScheme: "test/v1", BirthToken: "owner-birth-a"},
	}
}

func TestLaunchGatePersistsBeforeRelease(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	prepared := testPrepared()
	registry := &observingRegistry{MemoryRegistry: NewMemoryRegistry(), prepared: prepared}
	prepared.onRelease = func() {
		if _, err := registry.Get(context.Background(), "record-1"); err != nil {
			t.Errorf("launch released before ownership was readable: %v", err)
		}
	}

	if _, err := Acquire(context.Background(), testOptions(clock), registry, testPlatform(), prepared); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !registry.persistedBeforeRun || !prepared.released || !prepared.started {
		t.Fatalf("ordering: persisted=%v released=%v started=%v", registry.persistedBeforeRun, prepared.released, prepared.started)
	}

	failingPrepared := testPrepared()
	failingRegistry := NewMemoryRegistry()
	failingRegistry.PutErr = errors.New("disk full")
	if _, err := Acquire(context.Background(), testOptions(clock), failingRegistry, testPlatform(), failingPrepared); err == nil {
		t.Fatal("Acquire succeeded despite failed durable write")
	}
	if failingPrepared.released || failingPrepared.started || !failingPrepared.aborted {
		t.Fatalf("failed persistence ran untrusted code: released=%v started=%v aborted=%v", failingPrepared.released, failingPrepared.started, failingPrepared.aborted)
	}
}

func TestLaunchAbortReturnsIndeterminateEvidence(t *testing.T) {
	registry := NewMemoryRegistry()
	registry.PutErr = errors.New("disk full")
	prepared := testPrepared()
	prepared.abortResult = AbortResult{Status: AbortIndeterminate, Detail: "suspended child state unknown"}
	prepared.abortErr = errors.New("abort pipe failed")
	_, err := Acquire(context.Background(), testOptions(&fakeClock{now: time.Now()}), registry, testPlatform(), prepared)
	if !errors.Is(err, ErrAbortIndeterminate) || !errors.Is(err, prepared.abortErr) {
		t.Fatalf("Acquire error lacks abort evidence: %v", err)
	}
}

func TestLifecycleRecordUsesProcessBirthIdentity(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	lease, err := Acquire(context.Background(), testOptions(clock), registry, testPlatform(), testPrepared())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	record := lease.Record()
	if record.OwnerIdentity.BirthToken != "owner-birth-a" || record.BoundaryProcessIdentity.BirthToken != "boundary-birth-a" {
		t.Fatalf("birth identities were not captured from backend: %+v", record)
	}
	if record.OwnerIdentity.BirthTokenScheme != "test/v1" || record.BoundaryProcessIdentity.BirthTokenScheme != "test/v1" {
		t.Fatalf("birth-token algorithms are not versioned: %+v", record)
	}
	if record.OwnerIdentity.BirthToken == fmt.Sprint(record.OwnerIdentity.PID) || record.BoundaryProcessIdentity.BirthToken == fmt.Sprint(record.BoundaryProcessIdentity.PID) {
		t.Fatal("PID was used as a process-birth identity")
	}
}

func TestLifecycleRecordSchemaFields(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	lease, err := Acquire(context.Background(), testOptions(clock), registry, testPlatform(), testPrepared())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	data, err := json.Marshal(lease.Record())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{
		"schema_id", "record_id", "revision", "operation_id", "harness", "owner_identity",
		"supervisor_identity", "direct_child_identity",
		"boundary_identity", "boundary_type", "boundary_process_identity", "state",
		"timestamps", "escape_evidence",
	} {
		if _, ok := fields[field]; !ok {
			t.Errorf("durable record is missing %q", field)
		}
	}
	if err := lease.Record().Validate(); err != nil {
		t.Fatalf("record does not validate: %v", err)
	}
}

func TestCompletedBoundaryDeletesRecord(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	platform.observation = BoundaryObservation{Status: BoundaryEmpty, BoundaryIdentity: "pgid:202"}
	lease, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.BeginCleanup(context.Background()); err != nil {
		t.Fatalf("BeginCleanup: %v", err)
	}
	result, err := lease.CompleteCleanup(context.Background())
	if err != nil {
		t.Fatalf("CompleteCleanup: %v", err)
	}
	if result.State != StateCompleted || !result.BoundaryEmpty || result.RecordRetained {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	if _, err := registry.Get(context.Background(), "record-1"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("completed boundary ownership record was retained: %v", err)
	}
}

func TestFailedCleanupRetainsRecord(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	platform.observation = matchingObservation()
	lease, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lease.BeginCleanup(context.Background()); err != nil {
		t.Fatalf("BeginCleanup: %v", err)
	}
	result, err := lease.CompleteCleanup(context.Background())
	if !errors.Is(err, ErrBoundaryNotEmpty) {
		t.Fatalf("CompleteCleanup error = %v, want ErrBoundaryNotEmpty", err)
	}
	if result.State != StateCleanupFailed || !result.RecordRetained {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	record, err := registry.Get(context.Background(), "record-1")
	if err != nil {
		t.Fatalf("retained record: %v", err)
	}
	if record.State != StateCleanupFailed || len(record.EscapeEvidence) == 0 {
		t.Fatalf("retained record lacks cleanup evidence: %+v", record)
	}
}

func TestRecoveryRefusesReusedIdentity(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	if _, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	observation := matchingObservation()
	observation.OwnerStatus = OwnerGone
	observation.BoundaryProcessIdentity.BirthToken = "boundary-birth-reused"
	platform.observation = observation
	if _, err := Recover(context.Background(), "record-1", registry, platform, clock); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("Recover error = %v, want ErrIdentityMismatch", err)
	}
	record, err := registry.Get(context.Background(), "record-1")
	if err != nil {
		t.Fatalf("record should be retained: %v", err)
	}
	if record.State != StateRecoveryBlocked || len(record.EscapeEvidence) == 0 {
		t.Fatalf("identity mismatch evidence not retained: %+v", record)
	}
}

func TestExpiredContextRetainsCleanupFailure(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	platform.observeCtxErr = true
	lease, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := lease.CompleteCleanup(ctx)
	if !errors.Is(err, context.Canceled) || !result.RecordRetained {
		t.Fatalf("CompleteCleanup = %+v, %v; want retained cancellation evidence", result, err)
	}
	record, getErr := registry.Get(context.Background(), "record-1")
	if getErr != nil || record.State != StateCleanupFailed || len(record.EscapeEvidence) == 0 {
		t.Fatalf("cancelled cleanup evidence not persisted: record=%+v err=%v", record, getErr)
	}
}

func TestRecoveryRetainsIndeterminateObservation(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	if _, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	platform.observation = BoundaryObservation{Status: BoundaryIndeterminate, OwnerStatus: OwnerIndeterminate, Detail: "job query denied"}
	if _, err := Recover(context.Background(), "record-1", registry, platform, clock); !errors.Is(err, ErrBoundaryIndeterminate) {
		t.Fatalf("Recover error = %v, want ErrBoundaryIndeterminate", err)
	}
	record, err := registry.Get(context.Background(), "record-1")
	if err != nil || record.State != StateRecoveryBlocked || len(record.EscapeEvidence) == 0 {
		t.Fatalf("indeterminate recovery evidence not retained: record=%+v err=%v", record, err)
	}
}

func TestBoundaryIdentityMismatchIsRetained(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	if _, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared()); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	observation := matchingObservation()
	observation.BoundaryIdentity = "pgid:999"
	platform.observation = observation
	result, err := (&Lease{registry: registry, platform: platform, clock: clock, record: mustRecord(t, registry)}).CompleteCleanup(context.Background())
	if !errors.Is(err, ErrIdentityMismatch) || !result.RecordRetained {
		t.Fatalf("CompleteCleanup = %+v, %v; want retained mismatch", result, err)
	}
}

func TestDeleteFailureReportsCompletedRetainedState(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)}
	registry := NewMemoryRegistry()
	platform := testPlatform()
	platform.observation = BoundaryObservation{Status: BoundaryEmpty, BoundaryIdentity: "pgid:202"}
	lease, err := Acquire(context.Background(), testOptions(clock), registry, platform, testPrepared())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	registry.DelErr = errors.New("directory sync failed")
	result, err := lease.CompleteCleanup(context.Background())
	if err == nil || result.State != StateCompleted || !result.BoundaryEmpty || !result.RecordRetained {
		t.Fatalf("CompleteCleanup = %+v, %v; want completed retained state", result, err)
	}
	record, getErr := registry.Get(context.Background(), "record-1")
	if getErr != nil || record.State != StateCompleted {
		t.Fatalf("durable state disagrees with result: record=%+v err=%v", record, getErr)
	}
}

func mustRecord(t *testing.T, registry Registry) Record {
	t.Helper()
	record, err := registry.Get(context.Background(), "record-1")
	if err != nil {
		t.Fatalf("Get record: %v", err)
	}
	return record
}
