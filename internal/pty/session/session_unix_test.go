//go:build !windows

package session

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	t time.Time
}

func (f *fakeClock) Now() time.Time {
	f.t = f.t.Add(50 * time.Millisecond)
	return f.t
}

func TestPTYSessionUsesSuppliedEnvironmentOnly(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "session_unix.go", nil, 0)
	require.NoError(t, err)

	var start *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "Start" {
			start = function
			break
		}
	}
	require.NotNil(t, start, "session_unix.go must declare Start")

	startCalls := 0
	ast.Inspect(start.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		owner, _ := selector.X.(*ast.Ident)
		if owner != nil && owner.Name == "os" && selector.Sel.Name == "Environ" {
			t.Error("PTY Start must not read or append os.Environ")
		}
		if owner == nil || owner.Name != "processlifecycle" || selector.Sel.Name != "StartPTYCommand" {
			return true
		}
		startCalls++
		require.Greater(t, len(call.Args), 4)
		environment, ok := call.Args[4].(*ast.Ident)
		if !ok || environment.Name != "env" {
			t.Errorf("StartPTYCommand environment argument = %#v, want unmodified env identifier", call.Args[4])
		}
		return true
	})
	require.Equal(t, 1, startCalls, "Start must have one PTY launch boundary")

	t.Setenv("FIZEAU_PTY_HOST_SENTINEL", "must-not-cross")
	session, err := Start(context.Background(), "/usr/bin/env", nil, "", []string{"SUPPLIED_ONLY=child"}, Size{Rows: 10, Cols: 40})
	require.NoError(t, err)
	defer session.Close()
	var output strings.Builder
	for chunk := range session.Output() {
		output.Write(chunk.Bytes)
	}
	require.Equal(t, 0, session.Wait().Code)
	require.Contains(t, output.String(), "SUPPLIED_ONLY=child")
	require.NotContains(t, output.String(), "FIZEAU_PTY_HOST_SENTINEL")
}

func TestStartFailureIsReported(t *testing.T) {
	_, err := Start(context.Background(), "/definitely/missing/agent-pty-command", nil, "", nil, Size{Rows: 24, Cols: 80})
	require.Error(t, err)
}

func TestHostPTYSmokeShellCatResizeAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows support requires a separate adapter bead")
	}
	clock := &fakeClock{}
	s, err := Start(context.Background(), "sh", []string{"-c", "stty size; cat; exit 7"}, "", []string{"TERM=xterm-256color"}, Size{Rows: 12, Cols: 34}, WithClock(clock))
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.SendBytes([]byte("alpha\nbeta\n")))
	require.NoError(t, s.SendKey(CtrlKey('d')))

	var out strings.Builder
	for chunk := range s.Output() {
		out.Write(chunk.Bytes)
	}
	require.Contains(t, out.String(), "12 34")
	require.Contains(t, out.String(), "alpha")
	require.Contains(t, out.String(), "beta")

	status := s.Wait()
	require.Equal(t, 7, status.Code)
	require.True(t, status.Exited)
}

func TestResizeAndKeyEventsAreTimedDeterministically(t *testing.T) {
	clock := &fakeClock{}
	s, err := Start(context.Background(), "cat", nil, "", nil, Size{Rows: 5, Cols: 10}, WithClock(clock))
	require.NoError(t, err)
	defer s.Kill()

	require.NoError(t, s.Resize(Size{Rows: 9, Cols: 40}))
	require.NoError(t, s.SendKey(KeyEnter))
	require.NoError(t, s.SendKey(KeyEscape))

	var sawResize, sawEnter, sawEscape bool
	deadline := time.After(2 * time.Second)
	for !(sawResize && sawEnter && sawEscape) {
		select {
		case ev := <-s.Events():
			require.Greater(t, ev.Seq, uint64(0))
			require.GreaterOrEqual(t, ev.TMS, int64(0))
			if ev.Kind == EventResize && ev.Size.Rows == 9 && ev.Size.Cols == 40 {
				sawResize = true
			}
			if ev.Kind == EventInput && ev.Key == KeyEnter {
				sawEnter = true
			}
			if ev.Kind == EventInput && ev.Key == KeyEscape {
				sawEscape = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for resize/input events")
		}
	}
}

func TestTimeoutCancelCleanupAndEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Start(ctx, "sleep", []string{"30"}, "", nil, Size{Rows: 10, Cols: 20})
	require.NoError(t, err)
	cancel()
	status := s.Wait()
	require.Error(t, status.Err)

	s2, err := Start(context.Background(), "sh", []string{"-c", "printf done"}, "", nil, Size{Rows: 10, Cols: 20}, WithTimeout(5*time.Second))
	require.NoError(t, err)
	defer s2.Close()
	var eof bool
	for chunk := range s2.Output() {
		if errors.Is(chunk.ReadError, io.EOF) || chunk.EOF {
			eof = true
			break
		}
	}
	require.True(t, eof, "output stream should report EOF")
	status = s2.Wait()
	require.Equal(t, 0, status.Code)
}

func TestSessionContextCancelKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Start(ctx, "sh", []string{"-c", "sleep 300 & echo child:$!; wait"}, "", nil, Size{Rows: 10, Cols: 40})
	require.NoError(t, err)

	shellPID, sessionPGID, selfPGID := sessionProcessGroupInfo(t, s)
	require.NotZero(t, shellPID)
	require.NotZero(t, sessionPGID)
	require.NotZero(t, selfPGID)
	require.NotEqual(t, selfPGID, sessionPGID, "PTY session must own a distinct process group")

	childPID := readChildPID(t, s.Output())
	require.NotZero(t, childPID)

	cancel()
	status := s.Wait()
	require.Error(t, status.Err)

	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(shellPID, 0), syscall.ESRCH)
	}, 2*time.Second, 25*time.Millisecond)
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH)
	}, 2*time.Second, 25*time.Millisecond)
}

func TestKillCleansProcessGroup(t *testing.T) {
	s, err := Start(context.Background(), "sh", []string{"-c", "sleep 300 & echo child:$!; wait"}, "", nil, Size{Rows: 10, Cols: 40})
	require.NoError(t, err)

	shellPID, sessionPGID, selfPGID := sessionProcessGroupInfo(t, s)
	require.NotZero(t, shellPID)
	require.NotZero(t, sessionPGID)
	require.NotZero(t, selfPGID)
	require.NotEqual(t, selfPGID, sessionPGID, "PTY session must own a distinct process group")

	childPID := readChildPID(t, s.Output())
	require.NotZero(t, childPID)
	require.NoError(t, s.Kill())
	for range s.Output() {
	}
	status := s.Wait()
	require.Error(t, status.Err)

	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(shellPID, 0), syscall.ESRCH)
	}, 2*time.Second, 25*time.Millisecond)
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH)
	}, 2*time.Second, 25*time.Millisecond)
}

func TestLargeMultilineInputAndOutputByteOffsets(t *testing.T) {
	s, err := Start(context.Background(), "cat", nil, "", nil, Size{Rows: 10, Cols: 80}, WithBufferSize(64))
	require.NoError(t, err)
	defer s.Kill()

	payload := strings.Repeat("line one\nline two\n", 128)
	require.NoError(t, s.SendBytes([]byte(payload)))
	require.NoError(t, s.SendKey(CtrlKey('d')))

	var total int
	var lastOffset int64 = -1
	for chunk := range s.Output() {
		if len(chunk.Bytes) > 0 {
			require.Greater(t, chunk.Offset, lastOffset)
			lastOffset = chunk.Offset
			total += len(chunk.Bytes)
		}
	}
	require.GreaterOrEqual(t, total, len(payload))
}

func TestOutputOnlyConsumerDoesNotDeadlockWhenEventBufferFills(t *testing.T) {
	s, err := Start(context.Background(), "sh", []string{"-c", "i=0; while [ $i -lt 400 ]; do printf x; i=$((i+1)); done"}, "", nil, Size{Rows: 10, Cols: 80}, WithBufferSize(1))
	require.NoError(t, err)
	defer s.Close()

	deadline := time.After(2 * time.Second)
	var total int
	for {
		select {
		case chunk, ok := <-s.Output():
			if !ok {
				require.GreaterOrEqual(t, total, 400)
				status := s.Wait()
				require.Equal(t, 0, status.Code)
				return
			}
			total += len(chunk.Bytes)
		case <-deadline:
			t.Fatalf("timed out reading output-only stream after %d bytes", total)
		}
	}
}

func readChildPID(t *testing.T, out <-chan OutputChunk) int {
	t.Helper()
	deadline := time.After(2 * time.Second)
	var buf strings.Builder
	for {
		select {
		case chunk, ok := <-out:
			if !ok {
				t.Fatalf("output closed before child PID; output=%q", buf.String())
			}
			buf.Write(chunk.Bytes)
			for _, field := range strings.Fields(buf.String()) {
				pidText, ok := strings.CutPrefix(field, "child:")
				if !ok {
					continue
				}
				pid, err := strconv.Atoi(pidText)
				require.NoError(t, err)
				return pid
			}
		case <-deadline:
			t.Fatalf("timed out waiting for child PID; output=%q", buf.String())
		}
	}
}

func sessionProcessGroupInfo(t *testing.T, s *Session) (shellPID int, sessionPGID int, selfPGID int) {
	t.Helper()
	selfPGID, err := syscall.Getpgid(os.Getpid())
	require.NoError(t, err)

	shellPID, err = s.Pid()
	require.NoError(t, err)
	require.Positive(t, shellPID)
	sessionPGID, err = syscall.Getpgid(shellPID)
	require.NoError(t, err)
	return shellPID, sessionPGID, selfPGID
}
