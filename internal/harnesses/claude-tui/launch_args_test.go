package claudetui

import (
	"testing"
)

// TestBuildLaunchArgsBypassPermissions proves the claude CLI is launched with
// `--permission-mode bypassPermissions` (ADR-013 / CONTRACT-004): the harness
// runs interactively (no --print) under bypassPermissions so tools execute
// unattended on the Claude Max subscription. This test asserts the exact flag,
// its value, and their relative order, so deleting either `--permission-mode`
// or `bypassPermissions` from buildLaunchArgs regresses the suite.
func TestBuildLaunchArgsBypassPermissions(t *testing.T) {
	const settingsJSON = `{"hooks":{}}`
	args := buildLaunchArgs(settingsJSON)

	// Locate --permission-mode and assert its value is the very next element.
	permIdx := -1
	for i, a := range args {
		if a == "--permission-mode" {
			permIdx = i
			break
		}
	}
	if permIdx == -1 {
		t.Fatalf("buildLaunchArgs() = %q; missing --permission-mode flag", args)
	}
	if permIdx+1 >= len(args) {
		t.Fatalf("buildLaunchArgs() = %q; --permission-mode has no value", args)
	}
	if got := args[permIdx+1]; got != "bypassPermissions" {
		t.Fatalf("buildLaunchArgs() = %q; --permission-mode value = %q, want %q", args, got, "bypassPermissions")
	}

	// The interactive launch must NOT include --print (which would force a
	// non-interactive single-shot that cannot answer the folder-trust dialog).
	for _, a := range args {
		if a == "--print" {
			t.Fatalf("buildLaunchArgs() = %q; must not include --print on the interactive bypassPermissions path", args)
		}
	}

	// Settings must be wired through unchanged so the real hook schema reaches
	// the launched process.
	settingsIdx := -1
	for i, a := range args {
		if a == "--settings" {
			settingsIdx = i
			break
		}
	}
	if settingsIdx == -1 {
		t.Fatalf("buildLaunchArgs() = %q; missing --settings flag", args)
	}
	if settingsIdx+1 >= len(args) || args[settingsIdx+1] != settingsJSON {
		t.Fatalf("buildLaunchArgs() = %q; --settings value not wired through, want %q", args, settingsJSON)
	}
}

// TestBuildLaunchArgsExactOrder pins the full argument slice so the launch
// contract is fully specified and a regression in flag ordering is caught.
func TestBuildLaunchArgsExactOrder(t *testing.T) {
	const settingsJSON = `{"hooks":{"Stop":[]}}`
	got := buildLaunchArgs(settingsJSON)
	want := []string{"--permission-mode", "bypassPermissions", "--settings", settingsJSON}
	if len(got) != len(want) {
		t.Fatalf("buildLaunchArgs() = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildLaunchArgs()[%d] = %q, want %q (full: %q)", i, got[i], want[i], got)
		}
	}
}
