//go:build live_harness

package claudetui_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
)

// TestLiveClaudeTuiRunsToolUnattended is the live smoke test (build-tagged
// `live_harness`, excluded from the default deterministic suite). It drives the
// REAL claude binary through the claude-tui Harness under bypassPermissions and
// asserts that a Write tool ran end-to-end with NO human interaction — the
// fresh worktree must contain the file the prompt asked Claude to create.
//
// Run with:
//
//	GOPRIVATE=github.com/easel/* GOSUMDB=off GOFLAGS=-mod=mod \
//	  go test -tags live_harness -run TestLiveClaudeTuiRunsToolUnattended \
//	  ./internal/harnesses/claude-tui/... -count=1 -v
//
// It is skipped unless FIZEAU_TEST_LIVE_CLAUDE_TUI is set so it never runs in
// CI by accident (it spends real Claude Max subscription quota).
func TestLiveClaudeTuiRunsToolUnattended(t *testing.T) {
	if os.Getenv("FIZEAU_TEST_LIVE_CLAUDE_TUI") == "" {
		t.Skip("FIZEAU_TEST_LIVE_CLAUDE_TUI not set; skipping live claude-tui smoke test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not found: %v", err)
	}

	// Fresh worktree so the proof file cannot pre-exist.
	workDir := t.TempDir()
	proofName := "spike_proof.txt"
	proofPath := filepath.Join(workDir, proofName)
	if _, err := os.Stat(proofPath); err == nil {
		t.Fatalf("proof file already exists before the turn: %s", proofPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h := &claudetui.Harness{}
	req := harnesses.ExecuteRequest{
		Prompt:  "Create a file named " + proofName + " in the current directory containing the single line: unattended-ok. Use the Write tool. Do not ask for confirmation.",
		WorkDir: workDir,
	}

	ch, err := h.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var (
		sawToolCall bool
		sawFinal    bool
		finalStatus string
	)
	for ev := range ch {
		switch ev.Type {
		case harnesses.EventTypeToolCall:
			sawToolCall = true
		case harnesses.EventTypeFinal:
			sawFinal = true
			finalStatus = finalStatusOf(ev.Data)
		}
	}

	if !sawFinal {
		t.Fatal("no final event emitted; the turn never completed")
	}
	if finalStatus != "success" {
		t.Errorf("final status = %q, want success", finalStatus)
	}
	if !sawToolCall {
		t.Error("no tool_call event observed; the model did not run a tool")
	}

	// The load-bearing proof: the file exists, created UNATTENDED.
	data, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatalf("proof file %s was not created unattended: %v", proofPath, err)
	}
	if len(data) == 0 {
		t.Errorf("proof file %s is empty", proofPath)
	}
}

// TestLiveClaudeTuiDefaultPolicyModelRunsTool is the DEFAULT-POLICY live smoke
// (build-tagged `live_harness`, gated by FIZEAU_TEST_LIVE_CLAUDE_TUI). It routes
// a default-policy request through claude-tui by passing the resolved
// default-policy tier model (sonnet-4.6) as req.Model, and asserts the harness
// honors it (launches `claude --model sonnet`) and executes a Write tool
// unattended. This proves a default-policy (sonnet-tier) route EXECUTES the
// resolved tier model via the TUI rather than falling back to claude(--print).
func TestLiveClaudeTuiDefaultPolicyModelRunsTool(t *testing.T) {
	if os.Getenv("FIZEAU_TEST_LIVE_CLAUDE_TUI") == "" {
		t.Skip("FIZEAU_TEST_LIVE_CLAUDE_TUI not set; skipping live claude-tui default-policy smoke test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not found: %v", err)
	}

	workDir := t.TempDir()
	proofName := "default_policy_proof.txt"
	proofPath := filepath.Join(workDir, proofName)
	if _, err := os.Stat(proofPath); err == nil {
		t.Fatalf("proof file already exists before the turn: %s", proofPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	h := &claudetui.Harness{}
	req := harnesses.ExecuteRequest{
		// sonnet-4.6 is the catalog default-policy (power-8) claude-surface tier;
		// the harness maps it to the `sonnet` CLI alias for --model.
		Model:   "sonnet-4.6",
		Prompt:  "Create a file named " + proofName + " in the current directory containing the single line: default-policy-ok. Use the Write tool. Do not ask for confirmation.",
		WorkDir: workDir,
	}

	ch, err := h.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var (
		sawToolCall bool
		sawFinal    bool
		finalStatus string
	)
	for ev := range ch {
		switch ev.Type {
		case harnesses.EventTypeToolCall:
			sawToolCall = true
		case harnesses.EventTypeFinal:
			sawFinal = true
			finalStatus = finalStatusOf(ev.Data)
		}
	}

	if !sawFinal {
		t.Fatal("no final event emitted; the default-policy turn never completed")
	}
	if finalStatus != "success" {
		t.Errorf("final status = %q, want success", finalStatus)
	}
	if !sawToolCall {
		t.Error("no tool_call event observed; the model did not run a tool")
	}

	data, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatalf("proof file %s was not created unattended on the default-policy tier: %v", proofPath, err)
	}
	if len(data) == 0 {
		t.Errorf("proof file %s is empty", proofPath)
	}
}

func finalStatusOf(raw []byte) string {
	var fd harnesses.FinalData
	if err := json.Unmarshal(raw, &fd); err != nil {
		return ""
	}
	return fd.Status
}
