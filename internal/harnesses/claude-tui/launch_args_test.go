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
	args := buildLaunchArgs(settingsJSON, "")

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
	got := buildLaunchArgs(settingsJSON, "")
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

// TestBuildLaunchArgsHonorsModel proves a resolved tier model is wired through
// as `--model <cli-model>` so the interactive TUI launches on the requested
// model (F5: a default-policy sonnet-tier route EXECUTES sonnet via claude-tui).
func TestBuildLaunchArgsHonorsModel(t *testing.T) {
	const settingsJSON = `{"hooks":{}}`
	got := buildLaunchArgs(settingsJSON, "sonnet")
	want := []string{"--permission-mode", "bypassPermissions", "--settings", settingsJSON, "--model", "sonnet"}
	if len(got) != len(want) {
		t.Fatalf("buildLaunchArgs(..., sonnet) = %q (len %d), want %q (len %d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildLaunchArgs(..., sonnet)[%d] = %q, want %q (full: %q)", i, got[i], want[i], got)
		}
	}
}

// TestClaudeTuiLaunchModel proves resolved catalog tier IDs collapse to the
// stable CLI alias so the launched session lands on the requested tier
// regardless of catalog point-version drift (sonnet-4.6 -> sonnet, opus-4.8 ->
// opus, claude-haiku-4-5 -> haiku, fable-1.0 -> fable), an empty model stays empty (account
// default), and an unknown full ID passes through verbatim.
func TestClaudeTuiLaunchModel(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"sonnet-4.6":        "sonnet",
		"opus-4.8":          "opus",
		"opus-4.7":          "opus",
		"claude-haiku-4-5":  "haiku",
		"haiku":             "haiku",
		"fable-1.0":         "fable",
		"claude-fable-1-0":  "fable",
		"fable":             "fable",
		"some-future-model": "some-future-model",
	}
	for in, want := range cases {
		if got := claudeTuiLaunchModel(in); got != want {
			t.Errorf("claudeTuiLaunchModel(%q) = %q, want %q", in, got, want)
		}
	}
}
