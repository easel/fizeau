package claudetui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
		return fmt.Errorf("send /clear: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	for {
		select {
		case chunk, ok := <-s.Output():
			if !ok {
				return errors.New("output channel closed before prompt marker seen")
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

// reapSession reaps a PTY session by killing the process, with a brief
// timeout to allow graceful shutdown.
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
