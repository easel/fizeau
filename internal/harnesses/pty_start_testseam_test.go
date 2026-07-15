package harnesses

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/pty/session"
)

func TestPTYSessionStarterExactKeyCopiesInvocationAndRestores(t *testing.T) {
	const (
		harnessName = "claude-tui"
		executable  = "/test/one/claude"
	)
	sentinel := errors.New("stop before PTY startup")
	originalArgs := []string{"--settings", `{"hooks":{}}`}
	originalEnv := []string{"HOME=/test/home"}
	originalOpts := []session.Option{session.WithTimeout(0)}
	calls := 0
	restore := RegisterPTYSessionStarterForTest(harnessName, executable, func(
		_ context.Context,
		gotExecutable string,
		gotArgs []string,
		gotWorkdir string,
		gotEnv []string,
		gotSize session.Size,
		gotOpts ...session.Option,
	) (*session.Session, error) {
		calls++
		if gotExecutable != executable || gotWorkdir != "/test/work" {
			t.Errorf("invocation path/workdir = %q/%q, want %q/%q", gotExecutable, gotWorkdir, executable, "/test/work")
		}
		if gotSize != (session.Size{Rows: 50, Cols: 220}) || len(gotOpts) != 1 {
			t.Errorf("invocation size/options = %#v/%d, want 50x220/1", gotSize, len(gotOpts))
		}
		gotArgs[0] = "mutated by replacement"
		gotEnv[0] = "mutated by replacement"
		gotOpts[0] = nil
		return nil, sentinel
	})

	if _, ok := LookupPTYSessionStarterForTest("other", executable); ok {
		t.Fatal("replacement matched a different harness")
	}
	if _, ok := LookupPTYSessionStarterForTest(harnessName, "/test/two/claude"); ok {
		t.Fatal("replacement matched a different executable")
	}
	starter, ok := LookupPTYSessionStarterForTest(harnessName, executable)
	if !ok {
		t.Fatal("exact-key replacement not found")
	}
	_, err := starter(context.Background(), executable, originalArgs, "/test/work", originalEnv, session.Size{Rows: 50, Cols: 220}, originalOpts...)
	if !errors.Is(err, sentinel) {
		t.Fatalf("start error = %v, want sentinel", err)
	}
	if calls != 1 {
		t.Fatalf("replacement calls = %d, want 1", calls)
	}
	if originalArgs[0] != "--settings" || originalEnv[0] != "HOME=/test/home" || originalOpts[0] == nil {
		t.Fatalf("replacement mutated production invocation: args=%q env=%q opts=%#v", originalArgs, originalEnv, originalOpts)
	}

	restore()
	restore()
	if _, remains := LookupPTYSessionStarterForTest(harnessName, executable); remains {
		t.Fatal("replacement remains registered after idempotent restore")
	}
}

func TestPTYSessionStarterRejectsDuplicateKey(t *testing.T) {
	const (
		harnessName = "claude-tui"
		executable  = "/test/duplicate/claude"
	)
	restore := RegisterPTYSessionStarterForTest(harnessName, executable, func(context.Context, string, []string, string, []string, session.Size, ...session.Option) (*session.Session, error) {
		return nil, nil
	})
	t.Cleanup(restore)

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	RegisterPTYSessionStarterForTest(harnessName, executable, func(context.Context, string, []string, string, []string, session.Size, ...session.Option) (*session.Session, error) {
		return nil, nil
	})
}

func TestPTYSessionStarterRecoversReplacementPanic(t *testing.T) {
	const (
		harnessName = "claude-tui"
		executable  = "/test/panic/claude"
	)
	restore := RegisterPTYSessionStarterForTest(harnessName, executable, func(context.Context, string, []string, string, []string, session.Size, ...session.Option) (*session.Session, error) {
		panic("fixture panic")
	})
	t.Cleanup(restore)
	starter, ok := LookupPTYSessionStarterForTest(harnessName, executable)
	if !ok {
		t.Fatal("panic replacement not found")
	}

	_, err := starter(context.Background(), executable, nil, "", nil, session.Size{})
	if err == nil || !strings.Contains(err.Error(), "fixture panic") {
		t.Fatalf("panic recovery error = %v, want fixture panic", err)
	}
}
