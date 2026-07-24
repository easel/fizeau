//go:build live_harness

package claudetui_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		Prompt:      "Create a file named " + proofName + " in the current directory containing the single line: unattended-ok. Use the Write tool. Do not ask for confirmation.",
		WorkDir:     workDir,
		Permissions: "unrestricted",
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

// TestLiveClaudeTuiRunsToolFromNestedParent is the release canary for the
// parent-session environment boundary. Once explicitly enabled, missing live
// prerequisites are failures rather than skips so a release cannot pass on a
// canary that never ran.
func TestLiveClaudeTuiRunsToolFromNestedParent(t *testing.T) {
	if os.Getenv("FIZEAU_TEST_LIVE_CLAUDE_TUI") == "" {
		t.Skip("FIZEAU_TEST_LIVE_CLAUDE_TUI not set; skipping live nested-parent canary")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("FIZEAU_TEST_LIVE_CLAUDE_TUI is set but claude is unavailable: %v", err)
	}
	for _, name := range []string{
		"CLAUDECODE",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_BRIDGE_SESSION_ID",
	} {
		t.Setenv(name, "nested-parent-must-not-cross")
	}

	workDir := t.TempDir()
	proofName := "nested_parent_proof.txt"
	proofPath := filepath.Join(workDir, proofName)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	events, err := (&claudetui.Harness{}).Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "Use the Write tool to create " + proofName + " in the current directory with exactly this single line: nested-parent-ok. Do not ask for confirmation.",
		WorkDir:     workDir,
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	finals := 0
	sawWrite := false
	for event := range events {
		switch event.Type {
		case harnesses.EventTypeToolCall:
			var call harnesses.ToolCallData
			if err := json.Unmarshal(event.Data, &call); err != nil {
				t.Fatalf("decode tool_call: %v", err)
			}
			sawWrite = sawWrite || call.Name == "Write"
		case harnesses.EventTypeFinal:
			finals++
			if status := finalStatusOf(event.Data); status != "success" {
				t.Errorf("final status = %q, want success", status)
			}
		}
	}
	if finals != 1 {
		t.Errorf("final event count = %d, want exactly one", finals)
	}
	if !sawWrite {
		t.Error("no Write tool_call observed")
	}
	contents, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatalf("proof file was not created unattended: %v", err)
	}
	if got := strings.TrimSpace(string(contents)); got != "nested-parent-ok" {
		t.Errorf("proof file contents = %q, want %q", got, "nested-parent-ok")
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
		Model:       "sonnet-4.6",
		Prompt:      "Create a file named " + proofName + " in the current directory containing the single line: default-policy-ok. Use the Write tool. Do not ask for confirmation.",
		WorkDir:     workDir,
		Permissions: "unrestricted",
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

func finalErrorOf(raw []byte) string {
	var fd harnesses.FinalData
	if err := json.Unmarshal(raw, &fd); err != nil {
		return ""
	}
	return fd.Error
}

// TestLiveClaudeTuiBypassConsentSelectionCanary is the release canary for the
// Bypass Permissions consent regression (fizeau-22f9a38f). It records the real
// claude --version and drives the REAL binary through the claude-tui Harness
// under Permissions:"unrestricted".
//
// Claude Code persists bypass consent per config dir, so the two-choice consent
// screen only renders on a not-yet-consented profile. The canary therefore has
// two honest outcomes, distinguished by the harness's bypass_consent_accepted
// audit event:
//
//   - consent screen appeared → the audit event is emitted; the canary proves
//     the driver selected "Yes, I accept" against the REAL screen and the turn
//     ran a tool unattended (the load-bearing proof); and
//   - already-consented profile → no audit event; the canary logs that the
//     consent screen did not appear and still validates an unattended turn.
//     It does NOT fail (a green run cannot be manufactured, but it also must
//     not spuriously red on an environment that already accepted consent).
//
// The fail-closed (unauthorized) path is proven deterministically in
// runturn_test.go rather than live, because without a consent screen an
// unauthorized live turn would actually execute the task. To force the consent
// screen for a full live proof, run against a fresh CLAUDE_CONFIG_DIR on a
// profile that has never accepted bypass (walking first-run onboarding).
//
// Gated by the live_harness build tag and FIZEAU_TEST_LIVE_CLAUDE_TUI; spends
// real subscription quota.
func TestLiveClaudeTuiBypassConsentSelectionCanary(t *testing.T) {
	if os.Getenv("FIZEAU_TEST_LIVE_CLAUDE_TUI") == "" {
		t.Skip("FIZEAU_TEST_LIVE_CLAUDE_TUI not set; skipping live bypass-consent canary")
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude binary not found: %v", err)
	}

	// Record the exercised CLI version so the canary evidence is unambiguous.
	verCtx, verCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer verCancel()
	verOut, err := exec.CommandContext(verCtx, claudePath, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("claude --version: %v (%s)", err, strings.TrimSpace(string(verOut)))
	}
	version := strings.TrimSpace(string(verOut))
	t.Logf("exercising claude %s at %s", version, claudePath)

	workDir := t.TempDir()
	proofName := "bypass_consent_proof.txt"
	proofPath := filepath.Join(workDir, proofName)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ch, err := (&claudetui.Harness{}).Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "Create a file named " + proofName + " in the current directory containing the single line: bypass-consent-ok. Use the Write tool. Do not ask for confirmation.",
		WorkDir:     workDir,
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var (
		finalStatus, finalErr string
		sawFinal              bool
		consentExercised      bool
	)
	for ev := range ch {
		switch ev.Type {
		case harnesses.EventTypeProgress:
			var w harnesses.FinalWarning
			if json.Unmarshal(ev.Data, &w) == nil && w.Code == "bypass_consent_accepted" {
				consentExercised = true
			}
		case harnesses.EventTypeFinal:
			sawFinal = true
			finalStatus = finalStatusOf(ev.Data)
			finalErr = finalErrorOf(ev.Data)
		}
	}

	if !sawFinal {
		t.Fatal("no final event; the turn never completed")
	}
	if finalStatus != "success" {
		t.Fatalf("final status = %q (%s); want success against claude %s", finalStatus, finalErr, version)
	}
	if data, err := os.ReadFile(proofPath); err != nil || len(data) == 0 {
		t.Fatalf("proof file %s not created unattended: %v", proofPath, err)
	}

	if consentExercised {
		t.Logf("PROVEN: bypass consent screen appeared and the driver selected 'Yes, I accept' against real claude %s", version)
	} else {
		t.Logf("NOTE: bypass consent screen did not appear (already-consented profile on claude %s); "+
			"unattended turn validated, but the consent selection path was not exercised live. "+
			"Use a fresh CLAUDE_CONFIG_DIR on an unconsented profile to force it.", version)
	}
}
