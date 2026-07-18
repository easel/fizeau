package claudetui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
	"github.com/easel/fizeau/internal/pty/session"
)

type claudeTuiPortableRecipe struct{}

func (claudeTuiPortableRecipe) PortableRuntimeNamespaceRecipe() {}

func TestBoundClaudeTUIUsesManifestCommandWithoutPATHLookup(t *testing.T) {
	root := t.TempDir()
	poisonDir := t.TempDir()
	poison := filepath.Join(poisonDir, "claude")
	if err := os.WriteFile(poison, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poisonDir)

	manifestCommand := filepath.Join(root, "bin", "claude")
	h := &claudetui.Harness{}
	bindClaudeTUIPortableRuntime(t, h, root, map[string]string{"MANIFEST_ONLY": "yes"})

	started := make(chan struct{}, 1)
	restore := harnesses.RegisterPTYSessionStarterForTest("claude-tui", manifestCommand, func(
		_ context.Context, command string, args []string, _ string, env []string, _ session.Size, _ ...session.Option,
	) (*session.Session, error) {
		if command != manifestCommand {
			t.Errorf("command = %q, want manifest command %q", command, manifestCommand)
		}
		if !strings.Contains(strings.Join(args, "\x00"), "--permission-mode\x00bypassPermissions") {
			t.Errorf("args do not retain TUI launch arguments: %q", args)
		}
		if got := strings.Join(env, "\n"); got != "MANIFEST_ONLY=yes" {
			t.Errorf("environment = %q, want exact closed manifest environment", got)
		}
		started <- struct{}{}
		return nil, errors.New("test stop before PTY startup")
	})
	t.Cleanup(restore)

	events, err := h.Execute(context.Background(), harnesses.ExecuteRequest{Prompt: "portable", WorkDir: root})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	drainClaudeTUIFinal(t, events)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("bound harness did not reach the manifest-keyed PTY starter")
	}
}

func TestUnboundClaudeTUIPreservesConfiguredBinaryAndAllowlist(t *testing.T) {
	command := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANG", "unbound-retained")
	h := &claudetui.Harness{Binary: command}

	started := make(chan struct{}, 1)
	restore := harnesses.RegisterPTYSessionStarterForTest("claude-tui", command, func(
		_ context.Context, gotCommand string, _ []string, _ string, env []string, _ session.Size, _ ...session.Option,
	) (*session.Session, error) {
		if gotCommand != command {
			t.Errorf("command = %q, want configured binary %q", gotCommand, command)
		}
		if !containsEnvironment(env, "LANG=unbound-retained") {
			t.Errorf("unbound environment omitted inherited allowlisted value: %q", env)
		}
		started <- struct{}{}
		return nil, errors.New("test stop before PTY startup")
	})
	t.Cleanup(restore)

	events, err := h.Execute(context.Background(), harnesses.ExecuteRequest{Prompt: "unbound"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	drainClaudeTUIFinal(t, events)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("unbound harness did not use configured PTY starter")
	}
}

func bindClaudeTUIPortableRuntime(t *testing.T, h *claudetui.Harness, root string, environment map[string]string) {
	t.Helper()
	binding, err := harnesses.NewPortableRuntimeRunnerBinding(harnesses.PortableRuntimeRunnerBindingInput{
		Structure: harnesses.PortableRuntimeStructure{
			Name: "claude-tui", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Mode: harnesses.PortableRuntimeStructuralUnpinned,
		},
		GuestRoot: root, ClosureClass: harnesses.PortableRuntimeClosureStatic,
		Launch:      harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/claude"},
		Environment: environment, NamespaceRecipe: claudeTuiPortableRecipe{},
	})
	if err != nil {
		t.Fatalf("NewPortableRuntimeRunnerBinding: %v", err)
	}
	if err := h.BindPortableRuntime(binding); err != nil {
		t.Fatalf("BindPortableRuntime: %v", err)
	}
}

func drainClaudeTUIFinal(t *testing.T, events <-chan harnesses.Event) {
	t.Helper()
	finals := 0
	for event := range events {
		if event.Type == harnesses.EventTypeFinal {
			finals++
		}
	}
	if finals != 1 {
		t.Fatalf("final events = %d, want 1", finals)
	}
}

func containsEnvironment(environment []string, want string) bool {
	for _, entry := range environment {
		if entry == want {
			return true
		}
	}
	return false
}
