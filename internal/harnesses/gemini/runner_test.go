package gemini

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestRunner_Info(t *testing.T) {
	r := &Runner{}
	info := r.Info()
	if info.Name != "gemini" {
		t.Errorf("expected name=gemini, got %q", info.Name)
	}
	if info.Type != "subprocess" {
		t.Errorf("expected type=subprocess, got %q", info.Type)
	}
}

func TestRunner_HealthCheck_NoBinary(t *testing.T) {
	r := &Runner{Binary: "/nonexistent/gemini-binary-xyz"}
	err := r.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

// TestRunner_Execute_HappyPath runs a fake script that emits gemini-style output.
func TestRunner_Execute_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	// Gemini emits stream-json lines in headless mode.
	script := `#!/bin/sh
cat <<'EOF'
skill conflict warning from stdout
{"type":"init","session_id":"test","model":"gemini-2.0-flash"}
{"type":"message","role":"user","content":"test prompt"}
{"type":"message","role":"assistant","content":"Hello from gemini","delta":true}
{"type":"result","status":"success","stats":{"total_tokens":25,"input_tokens":12,"output_tokens":13,"cached":2,"models":{"gemini-2.0-flash":{"total_tokens":25,"input_tokens":12,"output_tokens":13,"cached":2}}}}
EOF
`
	f, err := os.CreateTemp("", "fake-gemini-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Binary:   f.Name(),
		BaseArgs: []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt: "test prompt",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var textDeltas []string
	var finalEv *harnesses.FinalData
	for ev := range ch {
		switch ev.Type {
		case harnesses.EventTypeTextDelta:
			var d harnesses.TextDeltaData
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				t.Errorf("unmarshal text_delta: %v", err)
			}
			textDeltas = append(textDeltas, d.Text)
		case harnesses.EventTypeFinal:
			var fd harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &fd); err != nil {
				t.Errorf("unmarshal final: %v", err)
			}
			finalEv = &fd
		}
	}

	if len(textDeltas) == 0 {
		t.Error("expected at least one text_delta event")
	} else if !strings.Contains(textDeltas[0], "Hello from gemini") {
		t.Errorf("unexpected text delta: %q", textDeltas[0])
	}

	if finalEv == nil {
		t.Fatal("no final event received")
	}
	if finalEv.Status != "success" {
		t.Errorf("expected status=success, got %q (error: %s)", finalEv.Status, finalEv.Error)
	}
	if finalEv.FinalText != "Hello from gemini" {
		t.Errorf("expected FinalText to contain gemini output, got %q", finalEv.FinalText)
	}
	// Usage from JSON result stats.
	if finalEv.Usage == nil {
		t.Error("expected usage in final event from JSON stats block")
	} else {
		if finalEv.Usage.InputTokens == nil || *finalEv.Usage.InputTokens != 12 {
			t.Errorf("expected InputTokens=12, got %#v", finalEv.Usage.InputTokens)
		}
		if finalEv.Usage.OutputTokens == nil || *finalEv.Usage.OutputTokens != 13 { // total(25) - input(12)
			t.Errorf("expected OutputTokens=13, got %#v", finalEv.Usage.OutputTokens)
		}
		if finalEv.Usage.TotalTokens == nil || *finalEv.Usage.TotalTokens != 25 {
			t.Errorf("expected TotalTokens=25, got %#v", finalEv.Usage.TotalTokens)
		}
		if finalEv.Usage.CacheTokens == nil || *finalEv.Usage.CacheTokens != 2 {
			t.Errorf("expected CacheTokens=2, got %#v", finalEv.Usage.CacheTokens)
		}
	}
}

func TestRunner_Execute_CostPresence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tests := []struct {
		name      string
		costJSON  string
		wantKnown bool
		wantCost  float64
	}{
		{name: "unknown"},
		{name: "zero", costJSON: `,"cost_usd":0`, wantKnown: true},
		{name: "positive", costJSON: `,"total_cost_usd":0.0123`, wantKnown: true, wantCost: 0.0123},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := `#!/bin/sh
cat <<'EOF'
{"type":"message","role":"assistant","content":"done","delta":true}
{"type":"result","status":"success","stats":{"input_tokens":1` + tt.costJSON + `}}
EOF
`
			binary := filepath.Join(t.TempDir(), "fake-gemini-cost")
			if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ch, err := (&Runner{Binary: binary, BaseArgs: []string{}}).Execute(ctx, harnesses.ExecuteRequest{Prompt: "cost"})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}

			var raw json.RawMessage
			for ev := range ch {
				if ev.Type == harnesses.EventTypeFinal {
					raw = append(raw[:0], ev.Data...)
				}
			}
			if len(raw) == 0 {
				t.Fatal("no final event received")
			}

			wantSource := harnesses.CostSourceUnknown
			if tt.wantKnown {
				wantSource = harnesses.CostSourceReported
			}
			assertGeminiFinalCostJSON(t, raw, tt.wantKnown, tt.wantCost, wantSource)

			var final harnesses.FinalData
			if err := json.Unmarshal(raw, &final); err != nil {
				t.Fatalf("unmarshal final event: %v", err)
			}
			assertGeminiCostState(t, final.FinalCostUSD, final.FinalCostSource, final.CostUSD, tt.wantKnown, tt.wantCost)
		})
	}
}

func TestRunner_Execute_RequestControls(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "capture.json")
	workDir := t.TempDir()
	t.Setenv("GO_WANT_GEMINI_HELPER_PROCESS", "1")
	t.Setenv("GEMINI_HELPER_CAPTURE", capturePath)

	r := &Runner{
		Binary:   os.Args[0],
		BaseArgs: []string{"-test.run=TestGeminiHelperProcess", "--"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "prompt over stdin",
		Model:       "gemini-test-model",
		WorkDir:     workDir,
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for range ch {
	}

	var captured geminiHelperCapture
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatalf("unmarshal capture: %v", err)
	}
	if !reflect.DeepEqual(captured.Args, []string{"-m", "gemini-test-model", "--approval-mode", "yolo", "-p", "prompt over stdin"}) {
		t.Fatalf("args: got %v", captured.Args)
	}
	if captured.WorkDir != workDir {
		t.Fatalf("workdir: got %q, want %q", captured.WorkDir, workDir)
	}
	if captured.Stdin != "" {
		t.Fatalf("stdin: got %q", captured.Stdin)
	}
}

func TestRunner_Execute_StdinPromptMode(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "capture.json")
	t.Setenv("GO_WANT_GEMINI_HELPER_PROCESS", "1")
	t.Setenv("GEMINI_HELPER_CAPTURE", capturePath)

	r := &Runner{
		Binary:     os.Args[0],
		BaseArgs:   []string{"-test.run=TestGeminiHelperProcess", "--"},
		PromptMode: "stdin",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt: "prompt over stdin",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for range ch {
	}

	var captured geminiHelperCapture
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatalf("unmarshal capture: %v", err)
	}
	if !reflect.DeepEqual(captured.Args, []string{"--approval-mode", "plan", "-p", ""}) {
		t.Fatalf("args: got %v", captured.Args)
	}
	if captured.Stdin != "prompt over stdin" {
		t.Fatalf("stdin: got %q", captured.Stdin)
	}
}

func TestRunner_Info_UnsupportedReasoningIsExplicit(t *testing.T) {
	info := (&Runner{Binary: os.Args[0]}).Info()
	if !reflect.DeepEqual(info.SupportedPermissions, []string{"safe", "supervised", "unrestricted"}) {
		t.Fatalf("SupportedPermissions: got %v", info.SupportedPermissions)
	}
	if len(info.SupportedReasoning) != 0 {
		t.Fatalf("SupportedReasoning: got %v, want empty", info.SupportedReasoning)
	}
}

func TestRunner_Execute_RejectsReasoning(t *testing.T) {
	r := &Runner{
		Binary:   os.Args[0],
		BaseArgs: []string{"-test.run=TestGeminiHelperProcess", "--"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:    "prompt",
		Reasoning: "high",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var finalEv *harnesses.FinalData
	for ev := range ch {
		if ev.Type != harnesses.EventTypeFinal {
			continue
		}
		var fd harnesses.FinalData
		if err := json.Unmarshal(ev.Data, &fd); err != nil {
			t.Errorf("unmarshal final: %v", err)
		}
		finalEv = &fd
	}
	if finalEv == nil {
		t.Fatal("no final event received")
	}
	if finalEv.Status != "failed" {
		t.Fatalf("status: got %q, want failed", finalEv.Status)
	}
	if !strings.Contains(finalEv.Error, "reasoning control") {
		t.Fatalf("error: got %q", finalEv.Error)
	}
}

type geminiHelperCapture struct {
	Args    []string `json:"args"`
	WorkDir string   `json:"work_dir"`
	Stdin   string   `json:"stdin"`
}

func TestGeminiHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GEMINI_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	data, err := json.Marshal(geminiHelperCapture{
		Args:    args,
		WorkDir: wd,
		Stdin:   string(stdin),
	})
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Getenv("GEMINI_HELPER_CAPTURE"), data, 0o600); err != nil {
		panic(err)
	}
	os.Stdout.WriteString(`{"type":"message","role":"assistant","content":"gemini helper response","delta":true}` + "\n")
	os.Stdout.WriteString(`{"type":"result","status":"success","stats":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}` + "\n")
	os.Exit(0)
}

func TestParseGeminiUsage_Stats(t *testing.T) {
	output := `Hello world
{"stats":{"models":{"gemini-flash":{"tokens":{"input":5,"total":20}}}}}`
	agg := parseGeminiUsage(output)
	if agg.InputTokens != 5 {
		t.Errorf("expected InputTokens=5, got %d", agg.InputTokens)
	}
	if agg.OutputTokens != 15 { // 20-5
		t.Errorf("expected OutputTokens=15, got %d", agg.OutputTokens)
	}
	if agg.TotalTokens != 20 {
		t.Errorf("expected TotalTokens=20, got %d", agg.TotalTokens)
	}
	if agg.FinalText != output {
		t.Errorf("expected FinalText to be full output")
	}
}

func TestParseGeminiStreamOutput(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "usage_cassettes", "live-ok-20260421.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	agg, ok := parseGeminiStreamOutput(output)
	if !ok {
		t.Fatal("expected stream-json output")
	}
	if agg.FinalText != "OK" {
		t.Fatalf("FinalText: got %q", agg.FinalText)
	}
	if agg.InputTokens != 8468 || agg.OutputTokens != 1 || agg.TotalTokens != 8491 || agg.CacheTokens != 7 {
		t.Fatalf("usage: got input=%d output=%d total=%d cache=%d", agg.InputTokens, agg.OutputTokens, agg.TotalTokens, agg.CacheTokens)
	}
}

func TestParseGeminiStreamOutput_ACPMetaQuota(t *testing.T) {
	output := `{"type":"message","role":"assistant","content":"OK","delta":true}
{"type":"result","status":"success","_meta":{"quota":{"token_count":{"input_tokens":8,"output_tokens":5},"model_usage":[{"model":"gemini-2.5-flash","token_count":{"input_tokens":8,"output_tokens":5}}]}}}`
	agg, ok := parseGeminiStreamOutput(output)
	if !ok {
		t.Fatal("expected stream-json output")
	}
	if agg.InputTokens != 8 || agg.OutputTokens != 5 || agg.TotalTokens != 13 {
		t.Fatalf("usage: got input=%d output=%d total=%d", agg.InputTokens, agg.OutputTokens, agg.TotalTokens)
	}
}

func TestParseGeminiUsage_NoStats(t *testing.T) {
	output := "plain text response"
	agg := parseGeminiUsage(output)
	if agg.InputTokens != 0 || agg.OutputTokens != 0 {
		t.Errorf("expected zero tokens for plain text output")
	}
	if agg.FinalText != output {
		t.Errorf("expected FinalText=%q, got %q", output, agg.FinalText)
	}
}

func TestModelDiscoveryFromText(t *testing.T) {
	snapshot := ModelDiscoveryFromText("models:\r\n  \x1b[32mgemini-pro-test\x1b[0m\r\n  gemini-flash-test\r\n  gemini-pro-test", "unit-test")
	if !reflect.DeepEqual(snapshot.Models, []string{"gemini-pro-test", "gemini-flash-test"}) {
		t.Fatalf("models: got %v", snapshot.Models)
	}
	if len(snapshot.ReasoningLevels) != 0 {
		t.Fatalf("reasoning levels: got %v, want empty", snapshot.ReasoningLevels)
	}
	if snapshot.Source != "unit-test" {
		t.Fatalf("source: got %q", snapshot.Source)
	}
	if snapshot.FreshnessWindow != GeminiModelDiscoveryFreshnessWindow.String() {
		t.Fatalf("freshness window: got %q", snapshot.FreshnessWindow)
	}
}

func TestModelDiscoveryFromRecordedCLISurface(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "model_surface", "gemini-cli-0.46.0-models.txt"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := ModelDiscoveryFromText(string(data), "gemini-cli-bundle:0.46.0")
	want := []string{
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-3.1-flash-lite",
		"gemini-3-pro-preview",
		"gemini-3.1-pro-preview",
		"gemini-3.1-pro-preview-customtools",
		"gemini-3-flash-preview",
		"gemini-3.5-flash",
		"gemini-3-flash",
	}
	if !reflect.DeepEqual(snapshot.Models, want) {
		t.Fatalf("models: got %v, want %v", snapshot.Models, want)
	}
}

// TestRunner_Execute_ProcessGroupCleanupOnSuccess verifies that the process
// group is cleaned up after successful completion (not just on cancellation).
func TestRunner_Execute_ProcessGroupCleanupOnSuccess(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	script := `#!/bin/sh
cat <<'EOF'
{"type":"message","role":"assistant","content":"test","delta":true}
{"type":"result","status":"success","stats":{"input_tokens":1,"output_tokens":1}}
EOF
`
	f, err := os.CreateTemp("", "fake-gemini-cleanup-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Binary:   f.Name(),
		BaseArgs: []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var finalEv *harnesses.FinalData
	for ev := range ch {
		if ev.Type == harnesses.EventTypeFinal {
			var fd harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &fd); err != nil {
				t.Errorf("unmarshal final: %v", err)
			}
			finalEv = &fd
		}
	}

	if finalEv == nil {
		t.Fatal("no final event received")
	}
	if finalEv.Status != "success" {
		t.Errorf("expected status=success, got %q", finalEv.Status)
	}
}

// TestRunner_Execute_TimeoutKillsProcessGroup verifies that timeout triggers
// process group termination with escalation.
func TestRunner_Execute_TimeoutKillsProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	script := `#!/bin/sh
# Sleep longer than the timeout
sleep 10
`
	f, err := os.CreateTemp("", "fake-gemini-timeout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Binary:   f.Name(),
		BaseArgs: []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:  "test",
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var finalEv *harnesses.FinalData
	for ev := range ch {
		if ev.Type == harnesses.EventTypeFinal {
			var fd harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &fd); err != nil {
				t.Errorf("unmarshal final: %v", err)
			}
			finalEv = &fd
		}
	}

	if finalEv == nil {
		t.Fatal("no final event received")
	}
	if finalEv.Status != "timed_out" {
		t.Errorf("expected status=timed_out, got %q", finalEv.Status)
	}
}

// TestRunner_Execute_ContextCancellationKillsProcessGroup verifies that context
// cancellation triggers process group termination.
func TestRunner_Execute_ContextCancellationKillsProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	script := `#!/bin/sh
# Sleep until cancelled
sleep 10
`
	f, err := os.CreateTemp("", "fake-gemini-cancel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Binary:   f.Name(),
		BaseArgs: []string{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Cancel context after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	var finalEv *harnesses.FinalData
	for ev := range ch {
		if ev.Type == harnesses.EventTypeFinal {
			var fd harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &fd); err != nil {
				t.Errorf("unmarshal final: %v", err)
			}
			finalEv = &fd
		}
	}

	if finalEv == nil {
		t.Fatal("no final event received")
	}
	if finalEv.Status != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", finalEv.Status)
	}
}
