package termbench

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

const sampleTaskYAML = `
descriptions:
  - key: base
    instruction: |
      Sort the input file in /app/data.txt and write the result to /app/sorted.txt.
author_email: test@example.com
difficulty: medium
tags:
  - file-operations
  - software-engineering
max_agent_timeout_sec: 300
max_test_timeout_sec: 60
test_scripts:
  - setup-uv-pytest.sh
  - run-uv-pytest.sh
`

// writeFakeTask creates a minimal-but-valid TerminalBench task layout in
// dir/<id>/. It fakes only the files this adapter reads; Dockerfile and
// solution.sh are intentionally omitted because they are Harbor's concern.
func writeFakeTask(t *testing.T, dir, id string) string {
	t.Helper()
	taskDir := filepath.Join(dir, id)
	if err := os.MkdirAll(filepath.Join(taskDir, "tests"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte(sampleTaskYAML), 0o600); err != nil {
		t.Fatalf("write task.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "tests", "test_outputs.py"), []byte("def test_ok(): pass\n"), 0o600); err != nil {
		t.Fatalf("write test_outputs.py: %v", err)
	}
	return taskDir
}

func TestLoadTask_ValidTask(t *testing.T) {
	tmp := t.TempDir()
	taskDir := writeFakeTask(t, tmp, "sort-file")

	task, err := LoadTask(taskDir)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if task.ID != "sort-file" {
		t.Errorf("ID=%q want sort-file", task.ID)
	}
	if task.Difficulty != "medium" {
		t.Errorf("Difficulty=%q want medium", task.Difficulty)
	}
	if task.MaxAgentTimeoutSec != 300 {
		t.Errorf("MaxAgentTimeoutSec=%d want 300", task.MaxAgentTimeoutSec)
	}
	if task.MaxTestTimeoutSec != 60 {
		t.Errorf("MaxTestTimeoutSec=%d want 60", task.MaxTestTimeoutSec)
	}
	if len(task.Tags) != 2 || task.Tags[0] != "file-operations" {
		t.Errorf("Tags=%v want [file-operations software-engineering]", task.Tags)
	}
	if task.Instruction == "" {
		t.Errorf("Instruction empty; want sort prompt")
	}
}

func TestLoadTask_MissingTestsRejected(t *testing.T) {
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "broken")
	if err := os.MkdirAll(taskDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte(sampleTaskYAML), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	// no tests/test_outputs.py
	if _, err := LoadTask(taskDir); err == nil {
		t.Fatal("expected error for missing tests/test_outputs.py")
	}
}

func TestLoadTask_TimeoutDefaults(t *testing.T) {
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "no-timeouts")
	if err := os.MkdirAll(filepath.Join(taskDir, "tests"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yaml := `descriptions:
  - key: base
    instruction: do the thing
author_email: t@e.com
`
	if err := os.WriteFile(filepath.Join(taskDir, "task.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "tests", "test_outputs.py"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write tests: %v", err)
	}
	task, err := LoadTask(taskDir)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if task.MaxAgentTimeoutSec != DefaultAgentTimeoutSec {
		t.Errorf("MaxAgentTimeoutSec=%d want default %d", task.MaxAgentTimeoutSec, DefaultAgentTimeoutSec)
	}
}

func TestLoadTask_TB2TOMLLayout(t *testing.T) {
	tmp := t.TempDir()
	taskDir := filepath.Join(tmp, "cancel-async")
	if err := os.MkdirAll(filepath.Join(taskDir, "tests"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	toml := `version = "1.0"

[metadata]
author_email = "alex@example.com"
difficulty = "hard"
category = "software-engineering"
tags = [ "async", "concurrency", "python",]

[verifier]
timeout_sec = 900.0

[agent]
timeout_sec = 720.0
`
	if err := os.WriteFile(filepath.Join(taskDir, "task.toml"), []byte(toml), 0o600); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "instruction.md"), []byte("Do the async thing."), 0o600); err != nil {
		t.Fatalf("write instr: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "tests", "test_outputs.py"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write test: %v", err)
	}
	task, err := LoadTask(taskDir)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if task.Difficulty != "hard" {
		t.Errorf("Difficulty=%q want hard", task.Difficulty)
	}
	if task.MaxAgentTimeoutSec != 720 {
		t.Errorf("MaxAgentTimeoutSec=%d want 720", task.MaxAgentTimeoutSec)
	}
	if task.MaxTestTimeoutSec != 900 {
		t.Errorf("MaxTestTimeoutSec=%d want 900", task.MaxTestTimeoutSec)
	}
	if len(task.Tags) != 3 || task.Tags[0] != "async" {
		t.Errorf("Tags=%v want [async concurrency python]", task.Tags)
	}
	if task.Instruction != "Do the async thing." {
		t.Errorf("Instruction=%q", task.Instruction)
	}
}

func TestLoadTasks_SkipsNonTaskDirs(t *testing.T) {
	tmp := t.TempDir()
	writeFakeTask(t, tmp, "alpha")
	writeFakeTask(t, tmp, "beta")
	// Add a non-task dir (should be skipped silently).
	if err := os.MkdirAll(filepath.Join(tmp, "_template"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tasks, err := LoadTasks(tmp)
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks; want 2", len(tasks))
	}
	if tasks[0].ID != "alpha" || tasks[1].ID != "beta" {
		t.Errorf("ordering: %v want [alpha beta]", []string{tasks[0].ID, tasks[1].ID})
	}
}

func TestBuildPlan_PassesThroughTimeoutAndPrompt(t *testing.T) {
	task := &Task{
		ID:                 "x",
		Instruction:        "do it",
		MaxAgentTimeoutSec: 120,
	}
	plan := BuildPlan(task, PlanOptions{
		Harness: "fiz",
		Model:   "openrouter/qwen/qwen3.6-plus",
		WorkDir: "/work",
	})
	if plan.Timeout != 120*time.Second {
		t.Errorf("timeout=%v want 120s", plan.Timeout)
	}
	if plan.Request.Prompt != "do it" {
		t.Errorf("prompt=%q want 'do it'", plan.Request.Prompt)
	}
	if plan.Request.Permissions != "trusted" {
		t.Errorf("permissions=%q want trusted", plan.Request.Permissions)
	}
}

func TestCapture_FoldsTextAndFinal(t *testing.T) {
	cost := 0.0012
	ch := make(chan harnesses.Event, 4)
	td, _ := json.Marshal(harnesses.TextDeltaData{Text: "Hello "})
	ch <- harnesses.Event{Type: harnesses.EventTypeTextDelta, Data: td}
	td2, _ := json.Marshal(harnesses.TextDeltaData{Text: "world."})
	ch <- harnesses.Event{Type: harnesses.EventTypeTextDelta, Data: td2}
	final, _ := json.Marshal(harnesses.FinalData{
		Status:     "success",
		ExitCode:   0,
		DurationMS: 42,
		Usage: &harnesses.FinalUsage{
			InputTokens:  harnesses.IntPtr(10),
			OutputTokens: harnesses.IntPtr(7),
		},
		FinalCostUSD:    &cost,
		FinalCostSource: harnesses.CostSourceReported,
	})
	ch <- harnesses.Event{Type: harnesses.EventTypeFinal, Data: final}
	close(ch)

	traj := Capture(ch, CaptureOptions{
		SessionID: "s1",
		TaskID:    "tid",
		Agent:     AgentInfo{Name: "fiz", Version: "test", ModelName: "qwen3.6"},
	})
	if traj.SchemaVersion != ATIFSchemaVersion {
		t.Errorf("SchemaVersion=%q", traj.SchemaVersion)
	}
	if traj.ExitCode != 0 {
		t.Errorf("ExitCode=%d", traj.ExitCode)
	}
	if traj.FinalMetrics.InputTokens != 10 || traj.FinalMetrics.OutputTokens != 7 {
		t.Errorf("metrics tokens=%+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.Cost != 0.0012 {
		t.Errorf("cost=%v", traj.FinalMetrics.Cost)
	}
	// Should have one combined agent step containing both deltas.
	gotText := ""
	for _, s := range traj.Steps {
		if s.Source == "agent" {
			gotText += s.Message
		}
	}
	if gotText != "Hello world." {
		t.Errorf("agent text=%q want 'Hello world.'", gotText)
	}
}

func TestCaptureFinalCostEvidence(t *testing.T) {
	zero := 0.0
	positive := 0.0012
	negative := -0.0012
	nan := math.NaN()
	positiveInf := math.Inf(1)
	negativeInf := math.Inf(-1)
	const (
		inputTokens  = 13
		outputTokens = 8
	)

	representable := []struct {
		name   string
		amount *float64
		source harnesses.CostSource
		raw    string
		want   float64
	}{
		{name: "reported zero", amount: &zero, source: harnesses.CostSourceReported},
		{name: "configured positive", amount: &positive, source: harnesses.CostSourceConfigured, want: positive},
		{name: "nil unknown", source: harnesses.CostSourceUnknown},
		{
			name: "present unknown",
			raw:  `{"cost_usd":0.0012,"cost_source":"unknown","usage":{"input_tokens":13,"output_tokens":8}}`,
		},
		{
			name: "present invalid source",
			raw:  `{"cost_usd":0.0012,"cost_source":"vendor","usage":{"input_tokens":13,"output_tokens":8}}`,
		},
		{
			name: "negative reported",
			raw:  `{"cost_usd":-0.0012,"cost_source":"reported","usage":{"input_tokens":13,"output_tokens":8}}`,
		},
	}

	for _, tt := range representable {
		t.Run(tt.name, func(t *testing.T) {
			final := []byte(tt.raw)
			if tt.raw == "" {
				var err error
				final, err = json.Marshal(harnesses.FinalData{
					FinalCostUSD:    tt.amount,
					FinalCostSource: tt.source,
					Usage: &harnesses.FinalUsage{
						InputTokens:  harnesses.IntPtr(inputTokens),
						OutputTokens: harnesses.IntPtr(outputTokens),
					},
				})
				if err != nil {
					t.Fatalf("marshal final event: %v", err)
				}
			}
			ch := make(chan harnesses.Event, 1)
			ch <- harnesses.Event{Type: harnesses.EventTypeFinal, Data: final}
			close(ch)

			traj := Capture(ch, CaptureOptions{})
			if traj.FinalMetrics.Cost != tt.want {
				t.Fatalf("ATIF final cost = %v, want %v", traj.FinalMetrics.Cost, tt.want)
			}
			if traj.FinalMetrics.InputTokens != inputTokens || traj.FinalMetrics.OutputTokens != outputTokens {
				t.Fatalf("ATIF final tokens = (%d, %d), want (%d, %d)",
					traj.FinalMetrics.InputTokens, traj.FinalMetrics.OutputTokens, inputTokens, outputTokens)
			}
		})
	}

	t.Run("normalization preserves validity before ATIF collapse", func(t *testing.T) {
		direct := []struct {
			name      string
			amount    *float64
			source    harnesses.CostSource
			want      float64
			wantValid bool
		}{
			{name: "reported zero is valid", amount: &zero, source: harnesses.CostSourceReported, wantValid: true},
			{name: "unknown is invalid", amount: &positive, source: harnesses.CostSourceUnknown},
			{name: "invalid source is invalid", amount: &positive, source: harnesses.CostSource("vendor")},
			{name: "negative is invalid", amount: &negative, source: harnesses.CostSourceReported},
			{name: "nan is invalid", amount: &nan, source: harnesses.CostSourceReported},
			{name: "positive infinity is invalid", amount: &positiveInf, source: harnesses.CostSourceReported},
			{name: "negative infinity is invalid", amount: &negativeInf, source: harnesses.CostSourceConfigured},
		}

		for _, tt := range direct {
			t.Run(tt.name, func(t *testing.T) {
				got, valid := normalizedFinalCost(harnesses.FinalData{
					FinalCostUSD:    tt.amount,
					FinalCostSource: tt.source,
				})
				if valid != tt.wantValid || got != tt.want {
					t.Fatalf("normalized final cost = (%v, %v), want (%v, %v)", got, valid, tt.want, tt.wantValid)
				}
			})
		}
	})
}

func TestWriteHarnessOutput_ProducesExpectedLayout(t *testing.T) {
	tmp := t.TempDir()
	traj := &Trajectory{
		SchemaVersion: ATIFSchemaVersion,
		SessionID:     "s",
		TaskID:        "t",
		Agent:         AgentInfo{Name: "fiz", Version: "v0"},
		Steps: []TrajectoryStep{
			{StepID: 1, Source: "agent", Message: "ok"},
		},
	}
	if err := WriteHarnessOutput(tmp, traj); err != nil {
		t.Fatalf("WriteHarnessOutput: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "logs", "agent", "trajectory.json")); err != nil {
		t.Fatalf("trajectory.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "logs", "agent", "transcript.txt")); err != nil {
		t.Fatalf("transcript.txt missing: %v", err)
	}
}

func TestReadGraderResult_PassFailMissing(t *testing.T) {
	tmp := t.TempDir()
	verifier := filepath.Join(tmp, "logs", "verifier")
	if err := os.MkdirAll(verifier, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pass case
	if err := os.WriteFile(filepath.Join(verifier, "reward.txt"), []byte("1\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := ReadGraderResult("t1", tmp)
	if err != nil {
		t.Fatalf("ReadGraderResult: %v", err)
	}
	if !res.Passed() {
		t.Errorf("expected pass, got reward=%d", res.Reward)
	}
	// Fail case
	_ = os.WriteFile(filepath.Join(verifier, "reward.txt"), []byte("0"), 0o600)
	res, _ = ReadGraderResult("t1", tmp)
	if res.Passed() {
		t.Errorf("expected fail")
	}
	// Missing case
	tmp2 := t.TempDir()
	if _, err := ReadGraderResult("t1", tmp2); err != ErrNoVerifierOutput {
		t.Errorf("expected ErrNoVerifierOutput, got %v", err)
	}
}
