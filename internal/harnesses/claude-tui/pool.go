package claudetui

import (
	"context"
	"sync"

	"github.com/easel/fizeau/internal/pty/session"
)

type poolKey struct {
	workdir string
}

type pooledSession struct {
	session *session.Session
	mu      sync.Mutex
}

var (
	sessionPoolMu sync.Mutex
	sessionPool   = make(map[poolKey]*pooledSession)
)

// getOrCreateSession retrieves a cached session for the given workdir,
// or creates and caches a new one via session.Start.
func getOrCreateSession(ctx context.Context, binary string, args []string, workdir string, env []string, size session.Size) (*session.Session, error) {
	key := poolKey{workdir: workdir}

	sessionPoolMu.Lock()
	if pool, ok := sessionPool[key]; ok {
		sessionPoolMu.Unlock()
		pool.mu.Lock()
		s := pool.session
		pool.mu.Unlock()
		if s != nil {
			return s, nil
		}
	} else {
		sessionPoolMu.Unlock()
	}

	// Create new session
	s, err := session.Start(ctx, binary, args, workdir, env, size)
	if err != nil {
		return nil, err
	}

	// Cache it
	sessionPoolMu.Lock()
	sessionPool[key] = &pooledSession{session: s}
	sessionPoolMu.Unlock()

	return s, nil
}

// clearSession issues /clear to the session and waits for the ready marker.
func clearSession(s *session.Session, readyMarker string, timeout int) error {
	if err := s.SendBytes([]byte("/clear\r")); err != nil {
		return err
	}

	// Wait for the ready marker to reappear
	// This is handled by the caller in the runTurn flow
	return nil
}

// GetPooledSession returns the cached session for a workdir (for testing).
func GetPooledSession(workdir string) *session.Session {
	key := poolKey{workdir: workdir}
	sessionPoolMu.Lock()
	defer sessionPoolMu.Unlock()
	if pool, ok := sessionPool[key]; ok {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return pool.session
	}
	return nil
}

// getLiveSessionsSnapshot returns a snapshot of all live sessions in the pool
// without holding the global lock beyond the snapshot operation.
func getLiveSessionsSnapshot() []*session.Session {
	sessionPoolMu.Lock()
	defer sessionPoolMu.Unlock()
	var sessions []*session.Session
	for _, pool := range sessionPool {
		pool.mu.Lock()
		if pool.session != nil {
			sessions = append(sessions, pool.session)
		}
		pool.mu.Unlock()
	}
	return sessions
}

// GetOrCreateSessionForTest is exposed for testing purposes to create a session
// that will be added to the pool for the orphan reaper test.
func GetOrCreateSessionForTest(ctx context.Context, binary string, args []string, workdir string, env []string, size session.Size) (*session.Session, error) {
	return getOrCreateSession(ctx, binary, args, workdir, env, size)
}
