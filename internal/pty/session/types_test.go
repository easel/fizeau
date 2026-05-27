package session

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeImpl struct {
	writes bytes.Buffer
	size   Size
	closed bool
	killed bool
	status ExitStatus
}

type fakeUnitClock struct {
	t time.Time
}

func (f *fakeUnitClock) Now() time.Time {
	f.t = f.t.Add(50 * time.Millisecond)
	return f.t
}

func (f *fakeImpl) write(b []byte) (int, error) { return f.writes.Write(b) }

func (f *fakeImpl) resize(size Size) error {
	f.size = size
	return nil
}

func (f *fakeImpl) close() error {
	f.closed = true
	return nil
}

func (f *fakeImpl) kill() error {
	f.killed = true
	return nil
}

func (f *fakeImpl) wait() ExitStatus {
	return f.status
}

func (f *fakeImpl) pid() int {
	return 12345
}

func newFakeSession(clock Clock, impl *fakeImpl) *Session {
	start := clock.Now()
	readDone := make(chan struct{})
	close(readDone)
	return &Session{
		start:    start,
		clock:    clock,
		size:     Size{Rows: 2, Cols: 4},
		output:   make(chan OutputChunk, 8),
		events:   make(chan Event, 16),
		waitDone: make(chan struct{}),
		readDone: readDone,
		impl:     impl,
	}
}

func TestFakeSessionEventsKeysResizeAndOutputMetadata(t *testing.T) {
	clock := &fakeUnitClock{t: time.Unix(100, 0)}
	impl := &fakeImpl{status: ExitStatus{Code: 3, Exited: true}}
	s := newFakeSession(clock, impl)

	input := []byte("abc")
	require.NoError(t, s.SendBytes(input))
	input[0] = 'z'
	require.NoError(t, s.SendKey(KeyUp))
	require.NoError(t, s.Resize(Size{Rows: 9, Cols: 20}))
	s.emitOutput([]byte("out"), nil, false)

	require.Equal(t, "abc\x1b[A", impl.writes.String())
	require.Equal(t, Size{Rows: 9, Cols: 20}, impl.size)

	events := []Event{<-s.Events(), <-s.Events(), <-s.Events(), <-s.Events()}
	require.Equal(t, EventInput, events[0].Kind)
	require.Equal(t, []byte("abc"), events[0].Bytes)
	require.Equal(t, EventInput, events[1].Kind)
	require.Equal(t, KeyUp, events[1].Key)
	require.Equal(t, EventResize, events[2].Kind)
	require.Equal(t, Size{Rows: 9, Cols: 20}, events[2].Size)
	require.Equal(t, EventOutput, events[3].Kind)
	require.Equal(t, int64(0), events[3].Offset)
	require.Equal(t, []byte("out"), events[3].Bytes)

	chunk := <-s.Output()
	require.Equal(t, events[3].Seq, chunk.Seq)
	require.Equal(t, events[3].TMS, chunk.TMS)
	require.Equal(t, events[3].Offset, chunk.Offset)
	require.Equal(t, []byte("out"), chunk.Bytes)

	status := s.Wait()
	require.Equal(t, 3, status.Code)
	require.Equal(t, EventExit, (<-s.Events()).Kind)
}

func TestFakeSessionValidationAndClosure(t *testing.T) {
	clock := &fakeUnitClock{}
	impl := &fakeImpl{}
	s := newFakeSession(clock, impl)

	_, err := KeyBytes(Key("unknown"))
	require.Error(t, err)
	require.Error(t, s.Resize(Size{}))
	require.NoError(t, s.Close())
	require.True(t, impl.closed)
	require.ErrorIs(t, s.SendBytes([]byte("late")), ErrClosed)

	s = newFakeSession(clock, impl)
	require.NoError(t, s.Kill())
	require.True(t, impl.killed)
	ev := <-s.Events()
	require.Equal(t, EventSignal, ev.Kind)
	require.Equal(t, "kill", ev.Signal)
	require.ErrorIs(t, s.SendBytes([]byte("late")), ErrClosed)
}
