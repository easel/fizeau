package harnesses

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/easel/fizeau/internal/pty/session"
)

// PTYSessionStarter matches session.Start. Registered test replacements run
// synchronously at the real startup call site and must return promptly.
type PTYSessionStarter func(
	ctx context.Context,
	command string,
	args []string,
	workdir string,
	env []string,
	size session.Size,
	opts ...session.Option,
) (*session.Session, error)

type ptySessionStarterKey struct {
	harness    string
	executable string
}

var testPTYSessionStarters = struct {
	sync.RWMutex
	byKey map[ptySessionStarterKey]PTYSessionStarter
}{
	byKey: make(map[ptySessionStarterKey]PTYSessionStarter),
}

// RegisterPTYSessionStarterForTest installs a harness-agnostic PTY starter for
// one harness identity and exact resolved executable path. Only one
// registration may own a key at a time. The returned restore function is safe
// to call repeatedly.
//
// The callback receives isolated copies of mutable invocation arguments. It is
// invoked outside the registry lock, and a panic is converted to a start error.
func RegisterPTYSessionStarterForTest(harnessName, executable string, starter PTYSessionStarter) func() {
	if harnessName == "" {
		panic("harnesses: empty test PTY starter harness")
	}
	if executable == "" {
		panic("harnesses: empty test PTY starter executable")
	}
	if starter == nil {
		panic("harnesses: nil test PTY starter")
	}

	key := ptySessionStarterKey{harness: harnessName, executable: executable}
	guarded := guardedTestPTYSessionStarter(starter)
	testPTYSessionStarters.Lock()
	if _, exists := testPTYSessionStarters.byKey[key]; exists {
		testPTYSessionStarters.Unlock()
		panic("harnesses: duplicate test PTY starter for " + harnessName + "/" + executable)
	}
	testPTYSessionStarters.byKey[key] = guarded
	testPTYSessionStarters.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			testPTYSessionStarters.Lock()
			delete(testPTYSessionStarters.byKey, key)
			testPTYSessionStarters.Unlock()
		})
	}
}

// LookupPTYSessionStarterForTest returns the replacement for one exact test
// key. Production finds no registration and uses its real starter.
func LookupPTYSessionStarterForTest(harnessName, executable string) (PTYSessionStarter, bool) {
	key := ptySessionStarterKey{harness: harnessName, executable: executable}
	testPTYSessionStarters.RLock()
	starter, ok := testPTYSessionStarters.byKey[key]
	testPTYSessionStarters.RUnlock()
	return starter, ok
}

func guardedTestPTYSessionStarter(starter PTYSessionStarter) PTYSessionStarter {
	return func(
		ctx context.Context,
		command string,
		args []string,
		workdir string,
		env []string,
		size session.Size,
		opts ...session.Option,
	) (started *session.Session, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				started = nil
				err = fmt.Errorf("test PTY starter panic: %v", recovered)
			}
		}()
		return starter(
			ctx,
			strings.Clone(command),
			append([]string(nil), args...),
			strings.Clone(workdir),
			append([]string(nil), env...),
			size,
			append([]session.Option(nil), opts...)...,
		)
	}
}
