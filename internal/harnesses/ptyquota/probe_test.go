package ptyquota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
	"github.com/stretchr/testify/require"
)

func TestErrorStatus(t *testing.T) {
	require.Equal(t, StatusOK, ErrorStatus(nil))
	require.Equal(t, StatusUnavailable, ErrorStatus(&ProbeError{Status: StatusUnavailable}))
	require.Equal(t, StatusError, ErrorStatus(errors.New("plain error")))
}

func TestPreserveContextFailureKeepsCancellationAndSessionDiagnostic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sessionErr := errors.New("pty session closed")
	probeErr := &ProbeError{Status: StatusError, Reason: "fake quota probe failed", Err: sessionErr}

	err := preserveContextFailure(probeErr, ctx.Err())

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, sessionErr)
	require.Equal(t, StatusError, ErrorStatus(err))
	require.ErrorContains(t, err, "fake quota probe failed: pty session closed")
}

func TestRunMissingBinaryIsUnavailable(t *testing.T) {
	_, err := Run(context.Background(), Config{
		HarnessName: "missing",
		Binary:      "/definitely/missing/quota-probe-binary",
		Timeout:     time.Second,
	})
	require.Error(t, err)
	require.Equal(t, StatusUnavailable, ErrorStatus(err))
}

func TestRunAuthTextIsUnauthenticated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	_, err := Run(context.Background(), Config{
		HarnessName:  "fake",
		Binary:       "sh",
		Args:         []string{"-c", "printf 'Please log in to continue'; sleep 5"},
		ReadyMarkers: []string{"never-ready"},
		Timeout:      2 * time.Second,
		Size:         session.Size{Rows: 8, Cols: 80},
	})
	require.Error(t, err)
	require.Equal(t, StatusUnauthenticated, ErrorStatus(err))
}

func TestRunTimeoutDoesNotPromoteCassette(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	target := filepath.Join(t.TempDir(), "timeout-cassette")
	_, err := Run(context.Background(), Config{
		HarnessName:  "fake",
		Binary:       "sh",
		Args:         []string{"-c", "sleep 5"},
		ReadyMarkers: []string{"never-ready"},
		Timeout:      100 * time.Millisecond,
		Size:         session.Size{Rows: 8, Cols: 80},
		CassetteDir:  target,
	})
	require.Error(t, err)
	require.Equal(t, StatusError, ErrorStatus(err))
	_, statErr := os.Stat(target)
	require.True(t, errors.Is(statErr, os.ErrNotExist), "timeout should not leave accepted cassette evidence")
}

func TestRunReturnsWhenProcessExitsBeforeMarkers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	_, err := Run(context.Background(), Config{
		HarnessName:  "fake",
		Binary:       "sh",
		Args:         []string{"-c", "printf 'started\\n'"},
		ReadyMarkers: []string{"never-ready"},
		Timeout:      2 * time.Second,
		Size:         session.Size{Rows: 8, Cols: 80},
	})
	require.ErrorContains(t, err, "exited before expected output")
}

func TestMarkerWaitPrefersObservedOutputCompletionOverContext(t *testing.T) {
	tests := []struct {
		name string
		wait func(*runState, context.Context) error
	}{
		{
			name: "ready markers",
			wait: func(run *runState, ctx context.Context) error {
				return run.waitForAnyText(ctx, "never-ready")
			},
		},
		{
			name: "done markers",
			wait: func(run *runState, ctx context.Context) error {
				return run.waitForText(ctx, []string{"never-done"}, nil, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			done := make(chan struct{})
			close(done)

			err := tc.wait(&runState{done: done}, ctx)
			require.ErrorContains(t, err, "exited before expected output")
			require.NotErrorIs(t, err, context.Canceled)
		})
	}
}

func TestMarkerWaitPreservesContextWithoutObservedExit(t *testing.T) {
	tests := []struct {
		name string
		wait func(*runState, context.Context) error
	}{
		{
			name: "ready markers",
			wait: func(run *runState, ctx context.Context) error {
				return run.waitForAnyText(ctx, "never-ready")
			},
		},
		{
			name: "done markers",
			wait: func(run *runState, ctx context.Context) error {
				return run.waitForText(ctx, []string{"never-done"}, nil, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := tc.wait(&runState{done: make(chan struct{})}, ctx)
			require.ErrorContains(t, err, "quota probe timed out")
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestRunRequiresAllDoneMarkers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	target := filepath.Join(t.TempDir(), "partial-cassette")
	_, err := Run(context.Background(), Config{
		HarnessName: "fake",
		Binary:      "sh",
		Args:        []string{"-c", "printf 'first marker'; sleep 5"},
		DoneMarkers: []string{"first marker", "second marker"},
		Timeout:     100 * time.Millisecond,
		Size:        session.Size{Rows: 8, Cols: 80},
		CassetteDir: target,
		Quota: func(string) (cassette.QuotaRecord, error) {
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	require.Error(t, err)
	_, statErr := os.Stat(target)
	require.True(t, errors.Is(statErr, os.ErrNotExist), "partial output should not leave accepted cassette evidence")
}

func TestRunDoesNotTreatHealthyOAuthTextAsAuthFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	_, err := Run(context.Background(), Config{
		HarnessName: "fake",
		Binary:      "sh",
		Args:        []string{"-c", "printf 'Authenticated with OAuth\\r\\n100%% left\\r\\n'; sleep 1"},
		DoneMarkers: []string{"% left"},
		Timeout:     2 * time.Second,
		Size:        session.Size{Rows: 8, Cols: 80},
		Quota: func(string) (cassette.QuotaRecord, error) {
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	require.NoError(t, err)
}

func TestRunDoesNotTreatWorkdirPathContainingSignInAsAuthFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	_, err := Run(context.Background(), Config{
		HarnessName: "fake",
		Binary:      "sh",
		Args:        []string{"-c", "printf 'gpt-5.4 high · 100%% left · /tmp/sign in/project\\r\\n'; sleep 1"},
		DoneMarkers: []string{"% left"},
		Timeout:     2 * time.Second,
		Size:        session.Size{Rows: 8, Cols: 120},
		Quota: func(string) (cassette.QuotaRecord, error) {
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	require.NoError(t, err)
}

func TestRunRefusesToOverwriteNewerCassetteVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	target := filepath.Join(t.TempDir(), "future-cassette")
	require.NoError(t, os.MkdirAll(target, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(target, cassette.ManifestFile), []byte(`{"version":2}`), 0o600))

	_, err := Run(context.Background(), Config{
		HarnessName: "fake",
		Binary:      "sh",
		Args:        []string{"-c", "printf '100%% left\\n'"},
		DoneMarkers: []string{"% left"},
		Timeout:     2 * time.Second,
		Size:        session.Size{Rows: 8, Cols: 80},
		CassetteDir: target,
		Quota: func(string) (cassette.QuotaRecord, error) {
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	require.ErrorContains(t, err, "refuse to overwrite newer schema")
	raw, readErr := os.ReadFile(filepath.Join(target, cassette.ManifestFile))
	require.NoError(t, readErr)
	require.JSONEq(t, `{"version":2}`, string(raw))
}

func TestRunScrubsCassetteOutputBeforePromotion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	root := t.TempDir()
	workdir := filepath.Join(root, "worktree")
	require.NoError(t, os.MkdirAll(workdir, 0o750))
	target := filepath.Join(root, "quota-cassette")

	_, err := Run(context.Background(), Config{
		HarnessName: "fake",
		Binary:      "sh",
		Args:        []string{"-c", "printf 'alice@example.com %s 100%% left\\n' \"$PWD\""},
		Workdir:     workdir,
		DoneMarkers: []string{"% left"},
		Timeout:     2 * time.Second,
		Size:        session.Size{Rows: 8, Cols: 120},
		CassetteDir: target,
		Quota: func(string) (cassette.QuotaRecord, error) {
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(target, cassette.OutputRawFile))
	require.NoError(t, err)
	require.NotContains(t, string(raw), workdir)
	require.Contains(t, string(raw), "$WORKTREE")

	reader, err := cassette.Open(target)
	require.NoError(t, err)
	require.Equal(t, "redacted", reader.ScrubReport().Status)
	require.NotZero(t, reader.ScrubReport().HitCounts["email"])
	require.NotContains(t, strings.Join(reader.Manifest().Command.Argv, " "), workdir)
}

// TestRunReapsProcessGroupOnCompletion verifies that PTY probes properly
// set and reap the process group. AC #2: When a harness IS probed via PTY,
// the probe runs in its own process group that is killed on completion.
// Note: This test requires PTY support and may be skipped in sandboxed environments.
func TestRunReapsProcessGroupOnCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY process groups require Unix support")
	}
	start := time.Now()
	_, err := Run(context.Background(), Config{
		HarnessName: "fake",
		Binary:      "sh",
		// Keep the child alive well past the assertion window so success
		// proves marker-driven process-group cleanup, not natural exit.
		Args:        []string{"-c", "printf '100%% left\\n'; sleep 10"},
		DoneMarkers: []string{"% left"},
		Timeout:     10 * time.Second,
		Size:        session.Size{Rows: 8, Cols: 80},
		Quota: func(string) (cassette.QuotaRecord, error) {
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skip("PTY not available in this environment")
	}
	require.NoError(t, err)
	elapsed := time.Since(start)
	require.Less(t, elapsed, 5*time.Second, "probe should complete from marker-driven cleanup without waiting for natural exit")
}

// TestRunReapsProcessGroupOnTimeout verifies that PTY probes properly
// kill the process group even when the probe times out.
func TestRunReapsProcessGroupOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY process groups require Unix support")
	}
	start := time.Now()
	_, err := Run(context.Background(), Config{
		HarnessName:  "fake",
		Binary:       "sh",
		Args:         []string{"-c", "sleep 10"},
		ReadyMarkers: []string{"never-ready"},
		Timeout:      100 * time.Millisecond,
		Size:         session.Size{Rows: 8, Cols: 80},
	})
	require.Error(t, err)
	require.Equal(t, StatusError, ErrorStatus(err))
	elapsed := time.Since(start)
	require.Less(t, elapsed, 2*time.Second, "probe should kill process group quickly, not wait 10 seconds")

	_, err = os.FindProcess(os.Getpid())
	require.NoError(t, err, "current process should still exist (we're running the test)")
}

func TestRunCommandEnterDelaySendsEnterAsSeparateWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	target := filepath.Join(t.TempDir(), "cassette")
	// The fake harness reads one line and reports how many bytes the first
	// read returned: an atomic "/model\r" delivers 7 bytes in one read, while
	// a delayed Enter delivers the 6-byte body first.
	script := `stty -echo raw 2>/dev/null; printf 'ready> '; ` +
		`first=$(dd bs=64 count=1 2>/dev/null | wc -c); ` +
		`printf 'firstread=%s done\n' "$first"; sleep 0.2`
	_, err := Run(context.Background(), Config{
		HarnessName:       "fake",
		Binary:            "sh",
		Args:              []string{"-c", script},
		ReadyMarkers:      []string{"ready>"},
		Command:           "/model\r",
		CommandEnterDelay: 150 * time.Millisecond,
		DoneMarkers:       []string{"done"},
		Timeout:           5 * time.Second,
		Size:              session.Size{Rows: 8, Cols: 80},
		CassetteDir:       target,
		Quota: func(text string) (cassette.QuotaRecord, error) {
			require.Contains(t, text, "firstread=6")
			return cassette.QuotaRecord{Source: "pty", Status: string(StatusOK)}, nil
		},
	})
	require.NoError(t, err)
	reader, err := cassette.Open(target)
	require.NoError(t, err)
	var writes []string
	for _, in := range reader.Inputs() {
		if len(in.Bytes) > 0 {
			writes = append(writes, string(in.Bytes))
		}
	}
	require.Equal(t, []string{"/model", "\r"}, writes)
}
