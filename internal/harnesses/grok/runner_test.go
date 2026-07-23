package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestRunner_Info(t *testing.T) {
	r := &Runner{}
	info := r.Info()
	if info.Name != "grok" {
		t.Errorf("expected name=grok, got %q", info.Name)
	}
	if info.Type != "subprocess" {
		t.Errorf("expected type=subprocess, got %q", info.Type)
	}
	if !info.IsSubscription {
		t.Error("expected IsSubscription=true")
	}
}

func TestRunner_HealthCheck_NoBinary(t *testing.T) {
	r := &Runner{Binary: "/nonexistent/grok-binary-xyz"}
	if err := r.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

// writeFakeGrok writes a shell stub that records its argv to capture and
// prints canned streaming-json output.
func writeFakeGrok(t *testing.T, dir, capture string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
{
  pwd
  i=0
  for arg in "$@"; do
    printf 'ARG[%%s]=%%s\n' "$i" "$arg"
    i=$((i + 1))
  done
} > %q
cat <<'EOF'
{"type":"thought","data":"thinking"}
{"type":"text","data":"ok"}
{"type":"end","stopReason":"EndTurn","sessionId":"s-1","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5},"num_turns":1,"total_cost_usd":0.001}
EOF
`, capture)
	binary := filepath.Join(dir, "fake-grok")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestRunner_Execute_AppliesRequestControls(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	workDir := filepath.Join(dir, "work")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := writeFakeGrok(t, dir, capture)

	r := &Runner{Binary: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "hello prompt",
		Model:       "grok-4.5",
		Reasoning:   "high",
		WorkDir:     workDir,
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var final *harnesses.FinalData
	for ev := range ch {
		if ev.Type == harnesses.EventTypeFinal {
			var fd harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &fd); err != nil {
				t.Fatalf("decode final: %v", err)
			}
			final = &fd
		}
	}
	if final == nil {
		t.Fatal("no final event")
	}
	if final.Status != "success" {
		t.Fatalf("final status = %q (%s)", final.Status, final.Error)
	}
	if final.FinalText != "ok" {
		t.Errorf("final text = %q, want ok", final.FinalText)
	}
	if final.FinalCostUSD == nil || *final.FinalCostUSD != 0.001 {
		t.Errorf("final cost = %v, want 0.001", final.FinalCostUSD)
	}
	if final.FinalCostSource != harnesses.CostSourceReported {
		t.Errorf("cost source = %q, want reported", final.FinalCostSource)
	}
	if final.Usage == nil || final.Usage.InputTokens == nil || *final.Usage.InputTokens != 3 {
		t.Errorf("usage input tokens = %+v, want 3", final.Usage)
	}

	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		workDir,
		"ARG[0]=--output-format",
		"ARG[1]=streaming-json",
		"ARG[2]=--always-approve",
		"ARG[3]=--cwd",
		"ARG[4]=" + workDir,
		"ARG[5]=-m",
		"ARG[6]=grok-4.5",
		"ARG[7]=--reasoning-effort",
		"ARG[8]=high",
		"ARG[9]=-p",
		"ARG[10]=hello prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capture missing %q:\n%s", want, got)
		}
	}
}

func TestRunner_Execute_SupervisedPermissionMode(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	binary := writeFakeGrok(t, dir, capture)

	r := &Runner{Binary: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "p",
		Permissions: "supervised",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for range ch {
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "ARG[2]=--permission-mode") || !strings.Contains(got, "ARG[3]=default") {
		t.Fatalf("capture missing supervised permission args:\n%s", got)
	}
	if strings.Contains(got, "--always-approve") {
		t.Fatalf("supervised run must not auto-approve:\n%s", got)
	}
}

func TestRunner_Execute_ErrorEventFailsRun(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
cat <<'EOF'
{"type":"error","message":"Couldn't start session: no auth"}
EOF
`
	binary := filepath.Join(dir, "fake-grok")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{Binary: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "p"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var final *harnesses.FinalData
	for ev := range ch {
		if ev.Type == harnesses.EventTypeFinal {
			var fd harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &fd); err != nil {
				t.Fatalf("decode final: %v", err)
			}
			final = &fd
		}
	}
	if final == nil {
		t.Fatal("no final event")
	}
	if final.Status != "failed" {
		t.Fatalf("final status = %q, want failed", final.Status)
	}
	if !strings.Contains(final.Error, "no auth") {
		t.Errorf("final error = %q, want session error message", final.Error)
	}
}

func TestRunner_Execute_MissingBinary(t *testing.T) {
	r := &Runner{}
	t.Setenv("PATH", "")
	_, err := r.Execute(context.Background(), harnesses.ExecuteRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected setup error for missing binary")
	}
}
