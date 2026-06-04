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

// TestEvictPooledSessionRemovesSlotAndUnlocks proves evictPooledSession removes
// the slot from globalPool.sessions[key] AND unlocks the slot mutex, so a
// subsequent claimPooledSession on the same depth-1 key succeeds immediately by
// allocating a FRESH slot — no deadlock and no leftover/duplicate slot. The prior
// concern: if evict failed to remove the slot (or removed it while leaving the
// mutex locked) a depth-1 claim would either reuse a dead session or block until
// the context deadline.
func TestEvictPooledSessionRemovesSlotAndUnlocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workdir := t.TempDir()
	SetPoolDepth("claude-tui", workdir, 1)
	key := poolKey{harness: "claude-tui", workdir: workdir}

	// First claim creates a session and occupies the single depth-1 slot.
	ps1, err := claimPooledSession(ctx, "claude-tui", "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Skipf("session start unavailable: %v", err)
	}
	s1 := ps1.session

	// The slot is present in the pool while held.
	globalPool.mu.Lock()
	if got := len(globalPool.sessions[key]); got != 1 {
		globalPool.mu.Unlock()
		t.Fatalf("after claim: pool has %d slots for key, want 1", got)
	}
	globalPool.mu.Unlock()

	// Evict the held session. This must remove the slot from the pool AND
	// unlock its mutex so the depth-1 key is immediately re-claimable.
	if err := evictPooledSession("claude-tui", workdir, s1); err != nil {
		t.Fatalf("evictPooledSession: %v", err)
	}

	// The slot must be gone: no leftover/duplicate slot for the key.
	globalPool.mu.Lock()
	if got := len(globalPool.sessions[key]); got != 0 {
		globalPool.mu.Unlock()
		t.Fatalf("after evict: pool has %d slots for key, want 0 (slot not removed)", got)
	}
	globalPool.mu.Unlock()

	// A subsequent depth-1 claim must succeed immediately with a NEW session
	// (no deadlock from a still-locked slot, no reuse of the evicted session).
	done := make(chan *pooledSession, 1)
	go func() {
		ps2, err := claimPooledSession(ctx, "claude-tui", "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
		if err != nil {
			done <- nil
			return
		}
		done <- ps2
	}()

	select {
	case ps2 := <-done:
		if ps2 == nil {
			t.Fatal("second claim after evict failed to start a session")
		}
		if ps2.session == s1 {
			t.Error("second claim reused the evicted session pointer; want a fresh session")
		}
		// Exactly one slot must exist for the key now (the new one, no duplicate).
		globalPool.mu.Lock()
		if got := len(globalPool.sessions[key]); got != 1 {
			globalPool.mu.Unlock()
			t.Errorf("after re-claim: pool has %d slots for key, want 1 (duplicate slot)", got)
		} else {
			globalPool.mu.Unlock()
		}
		releasePooledSession("claude-tui", workdir, ps2.session)
		evictPooledSession("claude-tui", workdir, ps2.session)
	case <-time.After(3 * time.Second):
		t.Fatal("second claim blocked after evict; slot was not removed/unlocked (deadlock)")
	}
}

// TestExecutePoolKeyDistinctPerModel proves req.Model folds into poolKeyName so
// different models claim DIFFERENT pool slots. Turn A launches model=sonnet and
// is released; turn B with model=opus must claim a DIFFERENT session (a separate
// pool key), never turn A's slot. This is the pool-keying half of F5: a session
// launched for one model is never reused to serve a request for another model.
func TestExecutePoolKeyDistinctPerModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workdir := t.TempDir()

	// Resolve the CLI model tokens and pool keys exactly as runTurn does.
	sonnetModel := claudeTuiLaunchModel("sonnet-4.6")
	opusModel := claudeTuiLaunchModel("opus-4.8")
	if sonnetModel == opusModel {
		t.Fatalf("precondition: sonnet and opus must resolve to distinct CLI models, got %q == %q", sonnetModel, opusModel)
	}
	sonnetKey := poolKeyName("claude-tui", sonnetModel)
	opusKey := poolKeyName("claude-tui", opusModel)
	if sonnetKey == opusKey {
		t.Fatalf("poolKeyName must differ by model: %q == %q", sonnetKey, opusKey)
	}

	// Launch args must carry --model for each resolved tier.
	for _, m := range []string{sonnetModel, opusModel} {
		args := buildLaunchArgs(`{"hooks":{}}`, m)
		found := false
		for i, a := range args {
			if a == "--model" && i+1 < len(args) && args[i+1] == m {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("buildLaunchArgs for model %q missing `--model %s`: %q", m, m, args)
		}
	}

	// Turn A: claim on the sonnet pool key, then release it back to the pool.
	psA, err := claimPooledSession(ctx, sonnetKey, "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Skipf("session start unavailable: %v", err)
	}
	sA := psA.session
	releasePooledSession(sonnetKey, workdir, sA)

	// Turn B: claim on the opus pool key. It must NOT reuse turn A's session,
	// because the model is folded into the key (different slot entirely).
	psB, err := claimPooledSession(ctx, opusKey, "sleep", []string{"30"}, workdir, nil, session.Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("opus claim: %v", err)
	}
	sB := psB.session
	if sA == sB {
		t.Error("opus turn reused sonnet's pooled session; model not folded into pool key")
	}

	// The two models occupy two distinct pool keys, each with its own slot.
	keyA := poolKey{harness: sonnetKey, workdir: workdir}
	keyB := poolKey{harness: opusKey, workdir: workdir}
	globalPool.mu.Lock()
	nA := len(globalPool.sessions[keyA])
	nB := len(globalPool.sessions[keyB])
	globalPool.mu.Unlock()
	if nA != 1 || nB != 1 {
		t.Errorf("expected one slot per model key, got sonnet=%d opus=%d", nA, nB)
	}

	releasePooledSession(opusKey, workdir, sB)
	evictPooledSession(sonnetKey, workdir, sA)
	evictPooledSession(opusKey, workdir, sB)
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
