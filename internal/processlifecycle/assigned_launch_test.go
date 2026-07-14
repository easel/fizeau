package processlifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeAssignedBoundary struct {
	*fakePreparedBoundary
	events    *[]string
	assignErr error
}

func (b *fakeAssignedBoundary) Assign(context.Context) error {
	*b.events = append(*b.events, "assign")
	return b.assignErr
}

func (b *fakeAssignedBoundary) Release(ctx context.Context) error {
	*b.events = append(*b.events, "resume")
	return b.fakePreparedBoundary.Release(ctx)
}

func TestJobAssignmentPrecedesResume(t *testing.T) {
	events := make([]string, 0, 2)
	prepared := &fakeAssignedBoundary{fakePreparedBoundary: testPrepared(), events: &events}
	if _, err := acquireAssignedBoundary(
		context.Background(),
		testOptions(&fakeClock{now: time.Now()}),
		NewMemoryRegistry(),
		testPlatform(),
		prepared,
	); err != nil {
		t.Fatalf("acquire assigned boundary: %v", err)
	}
	if len(events) != 2 || events[0] != "assign" || events[1] != "resume" {
		t.Fatalf("launch ordering = %v, want [assign resume]", events)
	}
}

func TestJobAssignmentFailurePreventsResume(t *testing.T) {
	events := make([]string, 0, 1)
	assignErr := errors.New("nested job rejected assignment")
	prepared := &fakeAssignedBoundary{
		fakePreparedBoundary: testPrepared(),
		events:               &events,
		assignErr:            assignErr,
	}
	_, err := acquireAssignedBoundary(
		context.Background(),
		testOptions(&fakeClock{now: time.Now()}),
		NewMemoryRegistry(),
		testPlatform(),
		prepared,
	)
	if !errors.Is(err, assignErr) {
		t.Fatalf("acquire assigned boundary error = %v, want assignment failure", err)
	}
	if len(events) != 1 || events[0] != "assign" {
		t.Fatalf("failed assignment events = %v, want no resume", events)
	}
	if !prepared.aborted || prepared.released || prepared.started {
		t.Fatalf("failed assignment state: aborted=%v released=%v started=%v", prepared.aborted, prepared.released, prepared.started)
	}
}

func TestUnsupportedPlatformRejectsBeforeSpawn(t *testing.T) {
	spawned := false
	err := rejectUnsupportedPlatform(func() error {
		spawned = true
		return nil
	})
	if !errors.Is(err, ErrPlatformUnsupported) {
		t.Fatalf("unsupported error = %v, want ErrPlatformUnsupported", err)
	}
	if spawned {
		t.Fatal("unsupported platform invoked process creation")
	}
}
