package serviceimpl

import (
	"fmt"
	"sync"

	"github.com/easel/fizeau/internal/harnesses"
)

// SessionHub is a concurrent-safe broadcast store for in-flight and completed
// sessions. It holds only implementation-local fanout state; root facade code
// owns higher-level override-event wiring.
type SessionHub struct {
	mu       sync.Mutex
	sessions map[string]*hubSession
}

type hubSession struct {
	done        bool
	finalEvent  *harnesses.Event
	subscribers []chan harnesses.Event
}

// NewSessionHub constructs a new in-memory execution fanout hub.
func NewSessionHub() *SessionHub {
	return &SessionHub{sessions: make(map[string]*hubSession)}
}

// OpenSession registers a new active session before execution starts.
func (h *SessionHub) OpenSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[sessionID]; ok {
		// Prepared continuations establish the root child admission seam after
		// the coordinator makes the ID tailable. Preserve subscribers that
		// attached in that interval rather than replacing their session.
		return
	}
	h.sessions[sessionID] = &hubSession{}
}

// BroadcastEvent forwards one event to all active subscribers. Slow
// subscribers are skipped so Execute does not block on fanout.
func (h *SessionHub) BroadcastEvent(sessionID string, ev harnesses.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sess, ok := h.sessions[sessionID]
	if !ok || sess.done {
		return
	}
	for _, sub := range sess.subscribers {
		select {
		case sub <- ev:
		default:
		}
	}
}

// CloseSession marks a session complete, stores its final event for late
// subscribers, and closes all active subscriber channels.
func (h *SessionHub) CloseSession(sessionID string, finalEv harnesses.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sess, ok := h.sessions[sessionID]
	if !ok {
		return
	}
	sess.done = true
	sess.finalEvent = &finalEv
	for _, sub := range sess.subscribers {
		close(sub)
	}
	sess.subscribers = nil
}

// Subscribe returns a live subscription for an active session, or a one-shot
// replay channel containing the final event when the session has already ended.
func (h *SessionHub) Subscribe(sessionID string) (<-chan harnesses.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sess, ok := h.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("unknown session %q", sessionID)
	}

	ch := make(chan harnesses.Event, 32)
	if sess.done {
		go func(finalEv *harnesses.Event) {
			if finalEv != nil {
				ch <- *finalEv
			}
			close(ch)
		}(sess.finalEvent)
		return ch, nil
	}

	sess.subscribers = append(sess.subscribers, ch)
	return ch, nil
}
