package claudetui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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
				return ps.session, nil
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
			return s, nil
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

// clearSession issues /clear to the session and waits for the ready marker.
func clearSession(s *session.Session, readyMarker string, timeout time.Duration) error {
	if err := s.SendBytes([]byte("/clear\r")); err != nil {
		return fmt.Errorf("send /clear: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case chunk, ok := <-s.Output():
			if !ok {
				return errors.New("output channel closed before clear marker seen")
			}
			if chunk.ReadError != nil {
				return fmt.Errorf("read error: %w", chunk.ReadError)
			}
			if strings.Contains(string(chunk.Bytes), readyMarker) {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for clear prompt: %w", ctx.Err())
		}
	}
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
