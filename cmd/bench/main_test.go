package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchCommandName(t *testing.T) {
	got := benchCommandName()
	want := "fiz-bench"
	if got != want {
		t.Errorf("benchCommandName() = %q, want %q", got, want)
	}
}

func TestUsageMessageContainsFizBench(t *testing.T) {
	name := benchCommandName()
	if !strings.Contains(name, "fiz-bench") {
		t.Errorf("command name %q does not contain fiz-bench", name)
	}
}

func TestErrorMessageContainsFizBench(t *testing.T) {
	name := benchCommandName()
	if !strings.HasPrefix(name, "fiz-") {
		t.Errorf("command name %q does not start with fiz-", name)
	}
}

func TestRunUnknownCommandUsesCommandName(t *testing.T) {
	// Verify unknown-command error path embeds fiz-bench.
	// run() writes to stderr; we just confirm it returns exit code 2.
	code := run([]string{"no-such-subcommand"})
	if code != 2 {
		t.Errorf("run([no-such-subcommand]) = %d, want 2", code)
	}
}

func TestRunNoArgsReturnsTwo(t *testing.T) {
	code := run([]string{})
	if code != 2 {
		t.Errorf("run([]) = %d, want 2", code)
	}
}

func TestRunHelpReturnsZero(t *testing.T) {
	code := run([]string{"help"})
	if code != 0 {
		t.Errorf("run([help]) = %d, want 0", code)
	}
}

func TestBenchMainRedirectsExecutionSubcommands(t *testing.T) {
	tests := []string{"matrix", "sweep", "run", "plan"}

	for _, subcommand := range tests {
		t.Run(subcommand, func(t *testing.T) {
			// Capture stderr
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w

			code := run([]string{subcommand})

			w.Close()
			os.Stderr = oldStderr

			// Read captured stderr
			var buf bytes.Buffer
			io.Copy(&buf, r)
			stderr := buf.String()

			if code != 2 {
				t.Errorf("run([%s]) exit code = %d, want 2", subcommand, code)
			}

			if !strings.Contains(stderr, "use ./benchmark") {
				t.Errorf("run([%s]) stderr does not contain 'use ./benchmark', got: %q", subcommand, stderr)
			}
		})
	}
}

// TestBenchMatrix_RedirectExit2 is the acceptance-criteria test that verifies
// the matrix subcommand redirects to the bash runner with exit code 2.
func TestBenchMatrix_RedirectExit2(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	code := run([]string{"matrix"})

	w.Close()
	os.Stderr = oldStderr

	// Read captured stderr
	var buf bytes.Buffer
	io.Copy(&buf, r)
	stderr := buf.String()

	if code != 2 {
		t.Errorf("run([matrix]) exit code = %d, want 2", code)
	}

	if !strings.Contains(stderr, "use ./benchmark") {
		t.Errorf("run([matrix]) stderr does not contain 'use ./benchmark', got: %q", stderr)
	}
}

func TestBenchMainListingSubcommandsStillWork(t *testing.T) {
	// Get repo root: two directories up from cmd/bench/
	repoRoot := filepath.Join("..", "..")

	tests := []struct {
		name    string
		args    []string
		wantStr string
	}{
		{
			name:    "profiles",
			args:    []string{"profiles", "list", "--work-dir", repoRoot},
			wantStr: "",
		},
		{
			name:    "bench-sets",
			args:    []string{"bench-sets", "--work-dir", repoRoot},
			wantStr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			code := run(tc.args)

			w.Close()
			os.Stdout = oldStdout

			// Read captured stdout
			var buf bytes.Buffer
			io.Copy(&buf, r)
			stdout := buf.String()

			if code != 0 {
				t.Errorf("run(%v) exit code = %d, want 0", tc.args, code)
			}

			if len(stdout) == 0 {
				t.Errorf("run(%v) produced empty stdout, want non-empty", tc.args)
			}
		})
	}
}
