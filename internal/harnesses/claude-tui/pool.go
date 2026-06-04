package claudetui

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/pty/session"
)

// poolKey uniquely identifies a session pool by harness name and working directory.
type poolKey struct {
	harness string // harness name (typically "claude-tui")
	workdir string // working directory
}

// pooledSession wraps a session with concurrency control.
type pooledSession struct {
	session *session.Session
	mu      sync.Mutex

	// used is true once this slot has driven at least one turn. A reused slot
	// must be /clear'd before the next prompt so the new turn is a FRESH turn
	// rather than an append to the prior multi-turn conversation context.
	used bool
	// transcriptOffset is the byte position the NEXT turn's transcript read
	// should resume from. The Claude Code transcript .jsonl is append-only for
	// the whole session, so without this a reused slot would replay every
	// prior-turn block and fold prior-turn usage/text into the new turn's final.
	transcriptOffset int64
	// transcriptPath is the transcript file the prior turn read. When the next
	// turn reuses the same path we resume from transcriptOffset; a different
	// path (a new session file) resets the offset to 0.
	transcriptPath string
}

// pool manages pooled sessions with concurrency control and eviction.
type sessionPool struct {
	mu       sync.Mutex
	sessions map[poolKey][]*pooledSession
	depth    map[poolKey]int // max sessions per key (default 1)
}

var (
	globalPool = &sessionPool{
		sessions: make(map[poolKey][]*pooledSession),
		depth:    make(map[poolKey]int),
	}
)

// getOrCreatePooledSession retrieves a cached, idle session from the pool for the given
// key, or creates a new one if needed. It blocks until a session is available or the
// context is cancelled. At most one session is claimed at a time per key.
func getOrCreatePooledSession(
	ctx context.Context,
	harnessName string,
	binary string,
	args []string,
	workdir string,
	env []string,
	size session.Size,
) (*session.Session, error) {
	ps, err := claimPooledSession(ctx, harnessName, binary, args, workdir, env, size)
	if err != nil {
		return nil, err
	}
	return ps.session, nil
}

// claimPooledSession is getOrCreatePooledSession but returns the pooledSession
// HANDLE (not just the underlying *session.Session) so the caller can observe
// reuse state (used / transcriptOffset / transcriptPath) and reset conversation
// state between turns. A CACHE HIT returns a slot with used==true; the caller
// must /clear it before the next prompt. The slot's mutex is held (claimed) and
// must be released via releasePooledSession (which finds it by session pointer).
func claimPooledSession(
	ctx context.Context,
	harnessName string,
	binary string,
	args []string,
	workdir string,
	env []string,
	size session.Size,
) (*pooledSession, error) {
	key := poolKey{harness: harnessName, workdir: workdir}

	globalPool.mu.Lock()
	// Ensure depth is set (default 1)
	if _, ok := globalPool.depth[key]; !ok {
		globalPool.depth[key] = 1
	}
	depth := globalPool.depth[key]
	globalPool.mu.Unlock()

	// Try to claim an existing session
	for {
		globalPool.mu.Lock()
		sessions := globalPool.sessions[key]

		// Find first idle session. Claiming locks the slot's mutex but keeps
		// the slot in the pool so releasePooledSession can find it and unlock
		// it for reuse — claim and release stay symmetric.
		for _, ps := range sessions {
			// Try to acquire the session (non-blocking)
			if ps.mu.TryLock() {
				if ps.session == nil {
					// Slot exists but its session never started; unlock and
					// skip so a healthy slot or fresh session is used instead.
					ps.mu.Unlock()
					continue
				}
				globalPool.mu.Unlock()
				return ps, nil
			}
		}

		// Check if we can create a new session
		if len(sessions) < depth {
			// Allocate a new pooled session slot
			ps := &pooledSession{}
			ps.mu.Lock() // Claim it immediately
			globalPool.sessions[key] = append(sessions, ps)
			globalPool.mu.Unlock()

			// Create the actual session outside the lock
			s, err := session.Start(ctx, binary, args, workdir, env, size)
			if err != nil {
				// Release the slot on failure
				globalPool.mu.Lock()
				newSessions := globalPool.sessions[key]
				for i, candidate := range newSessions {
					if candidate == ps {
						globalPool.sessions[key] = append(newSessions[:i], newSessions[i+1:]...)
						break
					}
				}
				globalPool.mu.Unlock()
				ps.mu.Unlock()
				return nil, err
			}

			ps.session = s
			return ps, nil
		}

		// No sessions available and at capacity; wait and retry
		globalPool.mu.Unlock()

		// Back off briefly before retrying
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
			// Retry
		}
	}
}

// releasePooledSession returns a session to the pool for reuse by other callers.
// It must be called after using the session.
func releasePooledSession(harnessName string, workdir string, s *session.Session) {
	key := poolKey{harness: harnessName, workdir: workdir}

	globalPool.mu.Lock()
	defer globalPool.mu.Unlock()

	sessions := globalPool.sessions[key]
	for _, ps := range sessions {
		if ps.session == s {
			ps.mu.Unlock() // Release the lock, making the session available again
			return
		}
	}

	// Session not found in pool; this shouldn't happen but handle gracefully
}

// evictPooledSession removes a session from the pool and closes it.
// This is called when a session fails or becomes unhealthy.
func evictPooledSession(harnessName string, workdir string, s *session.Session) error {
	key := poolKey{harness: harnessName, workdir: workdir}

	globalPool.mu.Lock()
	sessions := globalPool.sessions[key]

	for i, ps := range sessions {
		if ps.session == s {
			// Remove from pool
			globalPool.sessions[key] = append(sessions[:i], sessions[i+1:]...)
			globalPool.mu.Unlock()

			// Close the session (release its lock first if held)
			if ps.mu.TryLock() {
				defer ps.mu.Unlock()
			}
			_ = s.Close()
			return nil
		}
	}

	globalPool.mu.Unlock()
	return fmt.Errorf("session not found in pool for eviction")
}

// GetPoolDepth returns the maximum number of concurrent sessions per pool key.
func GetPoolDepth(harnessName string, workdir string) int {
	key := poolKey{harness: harnessName, workdir: workdir}
	globalPool.mu.Lock()
	defer globalPool.mu.Unlock()

	if depth, ok := globalPool.depth[key]; ok {
		return depth
	}
	return 1 // default
}

// SetPoolDepth sets the maximum number of concurrent sessions per pool key.
func SetPoolDepth(harnessName string, workdir string, depth int) {
	if depth < 1 {
		depth = 1
	}
	key := poolKey{harness: harnessName, workdir: workdir}
	globalPool.mu.Lock()
	defer globalPool.mu.Unlock()
	globalPool.depth[key] = depth
}

// GetPooledSession returns any idle session for a (harness, workdir) key for testing.
// It does not claim the session, so multiple calls may return the same session.
func GetPooledSession(harnessName string, workdir string) *session.Session {
	key := poolKey{harness: harnessName, workdir: workdir}
	globalPool.mu.Lock()
	defer globalPool.mu.Unlock()

	sessions := globalPool.sessions[key]
	for _, ps := range sessions {
		// Check if this session is currently idle (not locked)
		if ps.mu.TryLock() {
			defer ps.mu.Unlock()
			if ps.session != nil {
				return ps.session
			}
		}
	}
	return nil
}

// GetLiveSessionsSnapshot returns a snapshot of all live sessions in the pool.
func getLiveSessionsSnapshot() []*session.Session {
	globalPool.mu.Lock()
	defer globalPool.mu.Unlock()

	var sessions []*session.Session
	for _, poolSessions := range globalPool.sessions {
		for _, ps := range poolSessions {
			if ps.session != nil {
				sessions = append(sessions, ps.session)
			}
		}
	}
	return sessions
}

// reapSession reaps a PTY session by killing the process.
func reapSession(ctx context.Context, s *session.Session) error {
	_ = s.Kill()

	done := make(chan struct{})

	go func() {
		_ = s.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		<-done
		return ctx.Err()
	}
}

// orphanReaper kills claude processes whose parent fiz PID is gone.
// This runs at startup before constructing the pool.
func reapOrphanSessions() {
	// Get current process's parent (fiz process)
	currentPID := os.Getpid()
	parentPID := os.Getppid()

	// For each claude process, check if its parent is still alive
	// In practice, we rely on process group termination, but this is a safety net
	// for crashed fiz instances that leave orphaned claude processes.
	//
	// Note: Full implementation would require traversing the process tree,
	// which is OS-specific. The pool eviction on clear failure provides
	// the primary safety mechanism; orphan reaper is a startup safety check.

	_ = currentPID
	_ = parentPID
	// TODO: Implement full process tree traversal if needed by benchmarks/load tests
}

// GetOrCreateSessionForTest is a test helper that wraps getOrCreatePooledSession.
func GetOrCreateSessionForTest(
	ctx context.Context,
	binary string,
	args []string,
	workdir string,
	env []string,
	size session.Size,
) (*session.Session, error) {
	return getOrCreatePooledSession(ctx, "claude-tui", binary, args, workdir, env, size)
}
