// Package session owns direct PTY process lifecycle and timed raw/input events.
//
// See internal/pty/doc.go for the governing ADR/SPIKE citations that authorize
// this package boundary.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Size is a terminal rows/columns pair.
type Size struct {
	Rows uint16
	Cols uint16
}

// OutputChunk is one timed raw PTY read.
type OutputChunk struct {
	Seq       uint64
	At        time.Time
	TMS       int64
	Offset    int64
	Bytes     []byte
	ReadError error
	EOF       bool
}

// EventKind identifies cassette-facing session events.
type EventKind string

const (
	EventOutput EventKind = "output"
	EventInput  EventKind = "input"
	EventResize EventKind = "resize"
	EventSignal EventKind = "signal"
	EventExit   EventKind = "exit"
)

// Event is the narrow timed event shape consumed by follow-on cassette code.
type Event struct {
	Seq    uint64
	Kind   EventKind
	At     time.Time
	TMS    int64
	Offset int64
	Bytes  []byte
	Key    Key
	Size   Size
	Signal string
	Exit   *ExitStatus
	Err    error
}

// Key names common terminal inputs.
type Key string

const (
	KeyEnter     Key = "enter"
	KeyTab       Key = "tab"
	KeyEscape    Key = "escape"
	KeyBackspace Key = "backspace"
	KeyUp        Key = "up"
	KeyDown      Key = "down"
	KeyRight     Key = "right"
	KeyLeft      Key = "left"
)

// CtrlKey returns a letter control-key input such as Ctrl-C.
func CtrlKey(r rune) Key {
	if r >= 'A' && r <= 'Z' {
		r += 'a' - 'A'
	}
	return Key(fmt.Sprintf("ctrl-%c", r))
}

// KeyBytes encodes a supported key as terminal input bytes.
func KeyBytes(k Key) ([]byte, error) {
	switch k {
	case KeyEnter:
		return []byte{'\r'}, nil
	case KeyTab:
		return []byte{'\t'}, nil
	case KeyEscape:
		return []byte{0x1b}, nil
	case KeyBackspace:
		return []byte{0x7f}, nil
	case KeyUp:
		return []byte("\x1b[A"), nil
	case KeyDown:
		return []byte("\x1b[B"), nil
	case KeyRight:
		return []byte("\x1b[C"), nil
	case KeyLeft:
		return []byte("\x1b[D"), nil
	}
	var r rune
	if _, err := fmt.Sscanf(string(k), "ctrl-%c", &r); err == nil {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' {
			return []byte{byte(r - 'a' + 1)}, nil
		}
	}
	return nil, fmt.Errorf("unsupported key %q", k)
}

// Clock lets tests inject deterministic timestamps.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Option configures Start.
type Option func(*Config)

// Config controls session startup.
type Config struct {
	Clock      Clock
	Timeout    time.Duration
	BufferSize int
}

// WithClock injects a deterministic clock.
func WithClock(clock Clock) Option {
	return func(cfg *Config) {
		cfg.Clock = clock
	}
}

// WithTimeout applies a wall-clock timeout in addition to ctx cancellation.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *Config) {
		cfg.Timeout = timeout
	}
}

// WithBufferSize changes the raw read buffer size.
func WithBufferSize(size int) Option {
	return func(cfg *Config) {
		cfg.BufferSize = size
	}
}

func applyOptions(opts []Option) Config {
	cfg := Config{Clock: realClock{}, BufferSize: 32 * 1024}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 32 * 1024
	}
	return cfg
}

// ExitStatus describes process termination.
type ExitStatus struct {
	Code     int
	Signal   string
	Exited   bool
	Signaled bool
	Err      error
}

// Session is a running PTY-backed process.
type Session struct {
	start  time.Time
	clock  Clock
	size   Size
	cancel context.CancelFunc

	mu       sync.Mutex
	seq      uint64
	offset   int64
	closed   bool
	waitOnce sync.Once
	waitDone chan struct{}
	waitStat ExitStatus
	readDone chan struct{}

	output       chan OutputChunk
	events       chan Event
	eventsMu     sync.Mutex
	eventsClosed bool

	impl sessionImpl
}

type sessionImpl interface {
	write([]byte) (int, error)
	resize(Size) error
	close() error
	kill() error
	wait() ExitStatus
	pid() int
}

var (
	// ErrClosed is returned when writing to a closed session.
	ErrClosed = errors.New("pty session closed")
)

// Output returns timed raw PTY chunks. The channel closes after the read loop
// reaches EOF or Close/Kill tears the PTY down.
func (s *Session) Output() <-chan OutputChunk { return s.output }

// Events returns timed input/output/resize/signal/exit events.
func (s *Session) Events() <-chan Event { return s.events }

// Size returns the last requested terminal size.
func (s *Session) Size() Size {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// Pid returns the process ID of the underlying PTY process.
func (s *Session) Pid() (int, error) {
	return s.impl.pid(), nil
}

// SendBytes writes literal bytes to the PTY and records an input event.
func (s *Session) SendBytes(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if _, err := s.impl.write(b); err != nil {
		return err
	}
	s.emit(Event{Kind: EventInput, Bytes: append([]byte(nil), b...)})
	return nil
}

// SendKey writes a named key sequence to the PTY and records an input event.
func (s *Session) SendKey(k Key) error {
	b, err := KeyBytes(k)
	if err != nil {
		return err
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if _, err := s.impl.write(b); err != nil {
		return err
	}
	s.emit(Event{Kind: EventInput, Bytes: b, Key: k})
	return nil
}

// Resize changes the PTY size and records a resize event.
func (s *Session) Resize(size Size) error {
	if err := validateSize(size); err != nil {
		return err
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := s.impl.resize(size); err != nil {
		return err
	}
	s.mu.Lock()
	s.size = size
	s.mu.Unlock()
	s.emit(Event{Kind: EventResize, Size: size})
	return nil
}

// Close cancels the session context and closes the PTY file descriptor.
func (s *Session) Close() error {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	return s.impl.close()
}

// Kill terminates the process group where supported and records a signal event.
// If the process exits naturally at the same time, the signal event is best
// effort and may race with the exit event.
func (s *Session) Kill() error {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.emit(Event{Kind: EventSignal, Signal: "kill"})
	return s.impl.kill()
}

// Wait blocks until the process exits and returns its exit status.
func (s *Session) Wait() ExitStatus {
	s.waitOnce.Do(func() {
		s.waitStat = s.impl.wait()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.emit(Event{Kind: EventExit, Exit: &s.waitStat})
		close(s.waitDone)
		if s.cancel != nil {
			s.cancel()
		}
	})
	<-s.waitDone
	return s.waitStat
}

func (s *Session) ensureOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}

func (s *Session) emit(ev Event) Event {
	s.mu.Lock()
	s.seq++
	ev.Seq = s.seq
	ev.At = s.clock.Now()
	ev.TMS = ev.At.Sub(s.start).Milliseconds()
	if ev.Kind == EventOutput {
		ev.Offset = s.offset
		s.offset += int64(len(ev.Bytes))
	}
	s.mu.Unlock()
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventsClosed {
		return ev
	}
	select {
	case s.events <- ev:
	default:
	}
	return ev
}

func (s *Session) closeEvents() {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventsClosed {
		return
	}
	s.eventsClosed = true
	close(s.events)
}

func (s *Session) emitOutput(b []byte, readErr error, eof bool) {
	ev := Event{Kind: EventOutput, Bytes: append([]byte(nil), b...), Err: readErr}
	ev = s.emit(ev)
	chunk := OutputChunk{
		Seq:       ev.Seq,
		At:        ev.At,
		TMS:       ev.TMS,
		Offset:    ev.Offset,
		Bytes:     ev.Bytes,
		ReadError: readErr,
		EOF:       eof,
	}
	s.output <- chunk
}

func validateSize(size Size) error {
	if size.Rows == 0 || size.Cols == 0 {
		return fmt.Errorf("invalid terminal size rows=%d cols=%d", size.Rows, size.Cols)
	}
	return nil
}
