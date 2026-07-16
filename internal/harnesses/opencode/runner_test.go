package opencode

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
	if info.Name != "opencode" {
		t.Errorf("expected name=opencode, got %q", info.Name)
	}
	if info.Type != "subprocess" {
		t.Errorf("expected type=subprocess, got %q", info.Type)
	}
}

func TestRunner_HealthCheck_NoBinary(t *testing.T) {
	r := &Runner{Binary: "/nonexistent/opencode-binary-xyz"}
	err := r.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
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
	script := fmt.Sprintf(`#!/bin/sh
{
  pwd
  i=0
  for arg in "$@"; do
    printf 'ARG[%%s]=%%s\n' "$i" "$arg"
    i=$((i + 1))
  done
} > %q
printf 'controlled response\n'
`, capture)
	binary := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{Binary: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "hello prompt",
		Model:       "opencode/gpt-5.4",
		Reasoning:   "high",
		WorkDir:     workDir,
		Permissions: "unrestricted",
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
	for _, want := range []string{
		workDir,
		"ARG[0]=run",
		"ARG[1]=--format",
		"ARG[2]=json",
		"ARG[3]=--dir",
		"ARG[4]=" + workDir,
		"ARG[5]=-m",
		"ARG[6]=opencode/gpt-5.4",
		"ARG[7]=--variant",
		"ARG[8]=high",
		"ARG[9]=hello prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capture missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "permission") || strings.Contains(got, "approval") {
		t.Fatalf("opencode permissions should not emit adapter flags:\n%s", got)
	}
}

func TestOpenCodeModelArgPrefixesProviderForBareModels(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{name: "bare local model", provider: "omlx", model: "Qwen3.6-27B-MLX-8bit", want: "omlx/Qwen3.6-27B-MLX-8bit"},
		{name: "already qualified", provider: "omlx", model: "openrouter/gpt-5.4-mini", want: "openrouter/gpt-5.4-mini"},
		{name: "no provider", provider: "", model: "opencode/gpt-5.4", want: "opencode/gpt-5.4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := opencodeModelArg(tt.provider, tt.model); got != tt.want {
				t.Fatalf("opencodeModelArg(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestRunner_Execute_StdinPromptMode(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "stdin.txt")
	script := fmt.Sprintf(`#!/bin/sh
cat > %q
printf 'stdin response\n'
`, capture)
	binary := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{Binary: binary, BaseArgs: []string{}, PromptMode: "stdin"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "prompt over stdin"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for range ch {
	}

	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "prompt over stdin" {
		t.Fatalf("stdin prompt = %q, want %q", got, "prompt over stdin")
	}
}

// TestRunner_Execute_HappyPath cats the real text_only.jsonl fixture through a
// fake opencode binary and verifies the parsed FinalText and event stream.
func TestRunner_Execute_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fixture := filepath.Join("testdata", "jsonl", "text_only.jsonl")
	script := "#!/bin/sh\ncat " + fixture + "\n"
	f, err := os.CreateTemp("", "fake-opencode-*")
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
	} else if textDeltas[0] != "PONG" {
		t.Errorf("unexpected text delta: %q", textDeltas[0])
	}

	if finalEv == nil {
		t.Fatal("no final event received")
	}
	if finalEv.Status != "success" {
		t.Errorf("expected status=success, got %q (error: %s)", finalEv.Status, finalEv.Error)
	}
	if finalEv.FinalText != "PONG" {
		t.Errorf("expected FinalText=%q, got %q", "PONG", finalEv.FinalText)
	}
}

func TestRunner_Execute_RealUsageFromStepFinish(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	fixture := filepath.Join("testdata", "jsonl", "text_only.jsonl")
	script := `#!/bin/sh
cat ` + fixture + `
`
	f, err := os.CreateTemp("", "fake-opencode-*")
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

	r := &Runner{Binary: f.Name(), BaseArgs: []string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "count tokens"})
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
	if finalEv.Usage == nil {
		t.Fatal("expected usage in final event")
	}
	if finalEv.Usage.Source != harnesses.UsageSourceNativeStream {
		t.Fatalf("expected Source=%q, got %q", harnesses.UsageSourceNativeStream, finalEv.Usage.Source)
	}
	if finalEv.Usage.InputTokens == nil || *finalEv.Usage.InputTokens != 13505 {
		t.Errorf("expected InputTokens=13505, got %#v", finalEv.Usage.InputTokens)
	}
	if finalEv.Usage.OutputTokens == nil || *finalEv.Usage.OutputTokens != 3 {
		t.Errorf("expected OutputTokens=3, got %#v", finalEv.Usage.OutputTokens)
	}
	if finalEv.Usage.ReasoningTokens == nil || *finalEv.Usage.ReasoningTokens != 18 {
		t.Errorf("expected ReasoningTokens=18, got %#v", finalEv.Usage.ReasoningTokens)
	}
	if finalEv.Usage.CacheReadTokens == nil || *finalEv.Usage.CacheReadTokens != 0 {
		t.Errorf("expected CacheReadTokens=0, got %#v", finalEv.Usage.CacheReadTokens)
	}
	if finalEv.Usage.CacheWriteTokens == nil || *finalEv.Usage.CacheWriteTokens != 0 {
		t.Errorf("expected CacheWriteTokens=0, got %#v", finalEv.Usage.CacheWriteTokens)
	}
	if finalEv.Usage.TotalTokens == nil || *finalEv.Usage.TotalTokens != 13526 {
		t.Errorf("expected TotalTokens=13526, got %#v", finalEv.Usage.TotalTokens)
	}
}

func TestRunner_Execute_FinalCostPresence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tests := []struct {
		name      string
		output    string
		wantKnown bool
		wantCost  float64
	}{
		{name: "no step_finish", output: `{"type":"text","part":{"type":"text","text":"done"}}`},
		{name: "absent", output: `{"type":"step_finish","part":{"type":"step-finish"}}`},
		{name: "negative", output: `{"type":"step_finish","part":{"type":"step-finish","cost":-0.01}}`},
		{name: "zero", output: `{"type":"step_finish","part":{"type":"step-finish","cost":0}}`, wantKnown: true},
		{name: "positive", output: `{"type":"step_finish","part":{"type":"step-finish","cost":0.0123}}`, wantKnown: true, wantCost: 0.0123},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "fake-opencode-cost")
			script := "#!/bin/sh\ncat <<'EOF'\n" + tt.output + "\nEOF\n"
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
			assertOpenCodeFinalCostJSON(t, raw, tt.wantKnown, tt.wantCost, wantSource)

			var final harnesses.FinalData
			if err := json.Unmarshal(raw, &final); err != nil {
				t.Fatalf("unmarshal final event: %v", err)
			}
			assertOpenCodeCostState(t, final.FinalCostUSD, final.FinalCostSource, final.CostUSD, tt.wantKnown, tt.wantCost)
		})
	}
}

func TestRunner_Execute_ExitErrorIncludesStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	script := `#!/bin/sh
printf 'bad model\n' >&2
exit 7
`
	f, err := os.CreateTemp("", "fake-opencode-*")
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

	r := &Runner{Binary: f.Name(), BaseArgs: []string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "fail"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	finalEv := readFinalEvent(t, ch)
	if finalEv.Status != "failed" {
		t.Fatalf("status = %q, want failed", finalEv.Status)
	}
	if finalEv.ExitCode != 7 {
		t.Fatalf("exit_code = %d, want 7", finalEv.ExitCode)
	}
	if !strings.Contains(finalEv.Error, "exit status 7") && !strings.Contains(finalEv.Error, "bad model") {
		t.Fatalf("error should include exit status or stderr, got %q", finalEv.Error)
	}
}

func TestRunner_Execute_RequestTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	script := `#!/bin/sh
sleep 5
`
	f, err := os.CreateTemp("", "fake-opencode-*")
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

	r := &Runner{Binary: f.Name(), BaseArgs: []string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:  "timeout",
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	finalEv := readFinalEvent(t, ch)
	if finalEv.Status != "timed_out" {
		t.Fatalf("status = %q, want timed_out (error: %s)", finalEv.Status, finalEv.Error)
	}
}

func TestParseOpencodeStream_WithUsage(t *testing.T) {
	// Simulate opencode --format json step_finish event with usage.
	input := `{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":23,"input":15,"output":8,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0.003}}`
	out := make(chan harnesses.Event, 16)
	var seq int64
	agg, err := parseOpencodeStream(context.Background(), strings.NewReader(input), out, nil, &seq)
	close(out)
	if err != nil {
		t.Fatalf("parseOpencodeStream: %v", err)
	}

	if len(agg.UsageSources) != 1 {
		t.Fatalf("expected 1 usage source, got %d", len(agg.UsageSources))
	}
	candidate := agg.UsageSources[0]
	if candidate.Source != harnesses.UsageSourceNativeStream {
		t.Fatalf("expected Source=%q, got %q", harnesses.UsageSourceNativeStream, candidate.Source)
	}
	if candidate.Counts.InputTokens == nil || *candidate.Counts.InputTokens != 15 {
		t.Errorf("expected InputTokens=15, got %#v", candidate.Counts.InputTokens)
	}
	if candidate.Counts.OutputTokens == nil || *candidate.Counts.OutputTokens != 8 {
		t.Errorf("expected OutputTokens=8, got %#v", candidate.Counts.OutputTokens)
	}
	if candidate.Counts.TotalTokens == nil || *candidate.Counts.TotalTokens != 23 {
		t.Errorf("expected TotalTokens=23, got %#v", candidate.Counts.TotalTokens)
	}
	assertOpenCodeCostState(t, agg.FinalCostUSD, agg.CostSource, agg.CostUSD, true, 0.003)

	// step_finish emits no text_delta events.
	var events []harnesses.Event
	for ev := range out {
		events = append(events, ev)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestParseOpencodeStream_ErrorEnvelope(t *testing.T) {
	input := `{"type":"error","error":{"name":"APIError","data":{"message":"Invalid model identifier \"*\"."}}}`
	out := make(chan harnesses.Event, 16)
	var seq int64
	_, err := parseOpencodeStream(context.Background(), strings.NewReader(input), out, nil, &seq)
	close(out)
	if err == nil {
		t.Fatal("expected opencode error envelope to fail parsing")
	}
	if !strings.Contains(err.Error(), `Invalid model identifier "*"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no emitted events for error envelope, got %d", len(out))
	}
}

// TestRunner_EmitsModelResolutionEvent verifies that a routing/resolution event
// is emitted when the default model is resolved (parity with codex).
func TestRunner_EmitsModelResolutionEvent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	script := fmt.Sprintf(`#!/bin/sh
{
  i=0
  for arg in "$@"; do
    printf 'ARG[%%s]=%%s\n' "$i" "$arg"
    i=$((i + 1))
  done
} > %q
printf 'controlled response\n'
`, capture)
	binary := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{Binary: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hello prompt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var resolution *harnesses.RunnerModelResolution
	for ev := range ch {
		if ev.Type != harnesses.EventTypeRoutingDecision {
			continue
		}
		var data harnesses.RunnerModelResolution
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			continue
		}
		if data.ResolvedModel != "" {
			resolution = &data
		}
	}

	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"ARG[0]=run",
		"ARG[1]=--format",
		"ARG[2]=json",
		"ARG[3]=-m",
		"ARG[4]=opencode/gpt-5.4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capture missing %q:\n%s", want, got)
		}
	}
	if resolution == nil {
		t.Fatal("expected runner default-resolution signal")
	}
	if resolution.ResolvedModel != "opencode/gpt-5.4" || resolution.PriorDefaultModel != "opencode/gpt-5.4" || resolution.Surface != "embedded.openai" {
		t.Fatalf("resolution = %#v", resolution)
	}
}

// TestRunner_ReasoningResolutionForwarded verifies that the resolved reasoning
// value reaches the --variant arg when the discovery cache snaps the level.
func TestRunner_ReasoningResolutionForwarded(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.txt")
	script := fmt.Sprintf(`#!/bin/sh
{
  i=0
  for arg in "$@"; do
    printf 'ARG[%%s]=%%s\n' "$i" "$arg"
    i=$((i + 1))
  done
} > %q
printf 'controlled response\n'
`, capture)
	binary := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := harnesses.NewModelDiscoveryCache(func(harnessName, source string) (harnesses.ModelDiscoverySnapshot, error) {
		return harnesses.ModelDiscoverySnapshot{
			CapturedAt:      time.Now().UTC(),
			Models:          []string{"opencode/gpt-5.4"},
			ReasoningLevels: []string{"low", "medium"},
			Source:          source,
		}, nil
	})

	r := &Runner{Binary: binary, DiscoveryCache: cache}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:    "hello prompt",
		Model:     "opencode/gpt-5.4",
		Reasoning: "high",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var reasoning *harnesses.ReasoningActual
	for ev := range ch {
		if ev.Type != harnesses.EventTypeRoutingDecision {
			continue
		}
		var data harnesses.ReasoningActual
		if err := json.Unmarshal(ev.Data, &data); err == nil && data.ResolvedReasoning != "" {
			reasoning = &data
		}
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); !strings.Contains(got, "--variant") || !strings.Contains(got, "medium") {
		t.Fatalf("capture missing snapped reasoning --variant medium:\n%s", got)
	}
	if reasoning == nil || reasoning.ResolvedReasoning != "medium" || reasoning.Source != "snapped" || reasoning.Warning == "" {
		t.Fatalf("reasoning resolution = %#v", reasoning)
	}
}

func readFinalEvent(t *testing.T, ch <-chan harnesses.Event) harnesses.FinalData {
	t.Helper()
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
	return *finalEv
}
