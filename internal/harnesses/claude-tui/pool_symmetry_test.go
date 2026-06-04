package claudetui

import (
	"context"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/pty/session"
)

// TestPoolClaimReleaseSymmetryReuses proves the claim/release fix: after a
// claimed session is released, the SAME *session.Session is handed back on the
// next claim for the same (harness, workdir) key. The prior bug removed the
// pooled slot at claim time, so release never found it and the session was
// never reused.
func TestPoolClaimReleaseSymmetryReuses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workdir := t.TempDir()

	// First claim creates a session.
	s1, err := getOrCreatePooledSession(ctx, "claude-tui", "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Skipf("session start unavailable: %v", err)
	}

	// Release it back to the pool.
	releasePooledSession("claude-tui", workdir, s1)

	// Second claim must reuse the SAME session (symmetry restored).
	s2, err := getOrCreatePooledSession(ctx, "claude-tui", "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if s1 != s2 {
		t.Errorf("session not reused: claim #2 returned a different *Session than claim #1")
	}
	releasePooledSession("claude-tui", workdir, s2)

	// Cleanup.
	evictPooledSession("claude-tui", workdir, s1)
}

// TestPoolReleaseAfterClaimUnlocksForNextClaimer proves a released slot is
// re-claimable (the mutex was actually unlocked by release). If release had
// failed to find the slot, the second claim would create a NEW session because
// the depth-1 slot would remain perpetually locked.
func TestPoolReleaseAfterClaimUnlocksForNextClaimer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workdir := t.TempDir()
	SetPoolDepth("claude-tui", workdir, 1)

	s1, err := getOrCreatePooledSession(ctx, "claude-tui", "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Skipf("session start unavailable: %v", err)
	}
	releasePooledSession("claude-tui", workdir, s1)

	// With depth 1, a second claim must succeed quickly by reusing the slot,
	// not block forever waiting for capacity.
	done := make(chan *session.Session, 1)
	go func() {
		s2, _ := getOrCreatePooledSession(ctx, "claude-tui", "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
		done <- s2
	}()

	select {
	case s2 := <-done:
		if s2 != s1 {
			t.Errorf("depth-1 reuse returned a different session")
		}
		releasePooledSession("claude-tui", workdir, s2)
		evictPooledSession("claude-tui", workdir, s1)
	case <-time.After(3 * time.Second):
		t.Fatal("second claim blocked; release did not unlock the depth-1 slot")
	}
}
