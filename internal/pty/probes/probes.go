// Package probes provides a reusable startup-probe responder for PTY sessions.
//
// When the Ink TUI starts, it emits device attribute queries and other control sequences
// to determine terminal capabilities. This package handles those startup probes so the
// PTY session doesn't get stuck waiting for responses.
package probes

import (
	"bytes"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/pty/session"
)

// Responder handles startup probe queries from the terminal.
type Responder struct {
	session *session.Session
	done    chan struct{}
	once    sync.Once
	err     error

	// readyMarkers are terminal prompts that indicate the startup sequence is complete
	readyMarkers []string

	// timeout bounds how long we wait for the session to become ready
	timeout time.Duration
}

// Config configures a Responder.
type Config struct {
	Session      *session.Session
	ReadyMarkers []string
	Timeout      time.Duration
}

// New creates a Responder and starts listening for startup probes.
func New(cfg Config) *Responder {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.ReadyMarkers == nil {
		cfg.ReadyMarkers = []string{"❯", "> "}
	}

	r := &Responder{
		session:      cfg.Session,
		done:         make(chan struct{}),
		readyMarkers: cfg.ReadyMarkers,
		timeout:      cfg.Timeout,
	}

	go r.respondLoop()
	return r
}

// Ready blocks until startup probes are handled or timeout occurs.
func (r *Responder) Ready(deadline time.Time) error {
	select {
	case <-r.done:
		return r.err
	case <-time.After(time.Until(deadline)):
		r.once.Do(func() { close(r.done) })
		return fmt.Errorf("startup probe timeout")
	}
}

// Stop stops the responder.
func (r *Responder) Stop() {
	r.once.Do(func() { close(r.done) })
}

func (r *Responder) respondLoop() {
	defer r.once.Do(func() { close(r.done) })

	outputBuf := bytes.Buffer{}
	outputTimeout := time.NewTimer(r.timeout)
	defer outputTimeout.Stop()

	readyPatterns := make([]*regexp.Regexp, 0, len(r.readyMarkers))
	for _, marker := range r.readyMarkers {
		// Escape special regex characters in the marker
		escaped := regexp.QuoteMeta(marker)
		readyPatterns = append(readyPatterns, regexp.MustCompile(escaped))
	}

	for {
		select {
		case <-r.done:
			return
		case chunk, ok := <-r.session.Output():
			if !ok {
				return
			}
			if chunk.Bytes != nil {
				outputBuf.Write(chunk.Bytes)
				outputStr := outputBuf.String()

				// Handle startup probes
				handled := r.handleProbes(outputStr)

				// Check if we've reached a ready marker
				isReady := false
				for _, pattern := range readyPatterns {
					if pattern.MatchString(outputStr) {
						isReady = true
						break
					}
				}

				if handled || isReady {
					// Reset timeout when we make progress
					outputTimeout.Reset(r.timeout)
				}

				// Keep only the last part of the output (enough for ready marker detection)
				if outputBuf.Len() > 4096 {
					lines := bytes.Split(outputBuf.Bytes(), []byte{'\n'})
					if len(lines) > 10 {
						lines = lines[len(lines)-10:]
						outputBuf.Reset()
						for i, line := range lines {
							if i > 0 {
								outputBuf.WriteByte('\n')
							}
							outputBuf.Write(line)
						}
					}
				}

				if isReady {
					r.err = nil
					return
				}
			}
		case <-outputTimeout.C:
			// Startup sequence took too long; give up
			r.err = fmt.Errorf("startup probe timeout waiting for ready marker")
			return
		}
	}
}

// handleProbes detects and responds to common startup probes.
// Returns true if any probe was handled.
func (r *Responder) handleProbes(outputStr string) bool {
	handled := false

	// DA1: Device Attributes - responds with ESC[?1;0c (VT100)
	if bytes.Contains([]byte(outputStr), []byte("\x1b[c")) ||
		bytes.Contains([]byte(outputStr), []byte("\x1b[?c")) {
		_ = r.session.SendBytes([]byte("\x1b[?1;0c"))
		handled = true
	}

	// DA2: Secondary Device Attributes - responds with ESC[?62;c
	if bytes.Contains([]byte(outputStr), []byte("\x1b[>c")) ||
		bytes.Contains([]byte(outputStr), []byte("\x1b[>?c")) {
		_ = r.session.SendBytes([]byte("\x1b[?62;4;0c"))
		handled = true
	}

	// DSR: Device Status Report (cursor position) - CSI 6n
	if bytes.Contains([]byte(outputStr), []byte("\x1b[6n")) {
		_ = r.session.SendBytes([]byte("\x1b[1;1R"))
		handled = true
	}

	// XTVERSION: Terminal version request
	if bytes.Contains([]byte(outputStr), []byte("\x1b[>q")) {
		// Identify as xterm version 370
		_ = r.session.SendBytes([]byte("\x1b[>0;370;0c"))
		handled = true
	}

	// Handle window size requests (CSI 18t / CSI 19t)
	if bytes.Contains([]byte(outputStr), []byte("\x1b[18t")) {
		// Report window size in characters
		size := r.session.Size()
		resp := fmt.Sprintf("\x1b[8;%d;%dt", size.Rows, size.Cols)
		_ = r.session.SendBytes([]byte(resp))
		handled = true
	}

	if bytes.Contains([]byte(outputStr), []byte("\x1b[19t")) {
		// Report window size in pixels (use character-based fallback)
		size := r.session.Size()
		// Assume 8 pixels per column, 14 pixels per row (rough approximation)
		width := size.Cols * 8
		height := size.Rows * 14
		resp := fmt.Sprintf("\x1b[9;%d;%dt", height, width)
		_ = r.session.SendBytes([]byte(resp))
		handled = true
	}

	return handled
}
