package claudetui

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestEmitTranscriptAndFinalSynthesizesFinalForIncompleteTranscript(t *testing.T) {
	prev := transcriptFinalizationGrace
	transcriptFinalizationGrace = 40 * time.Millisecond
	t.Cleanup(func() { transcriptFinalizationGrace = prev })

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	body := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"prompt-secret sk-ant-incomplete123 account acct-incomplete"}]}}
`
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	events := make(chan harnesses.Event, 4)
	seq := int64(0)
	(&Harness{}).emitTranscriptAndFinal(context.Background(), &turnEnv{}, transcriptPath, events, &seq, time.Now(), nil)
	close(events)

	var (
		finals   []harnesses.FinalData
		finalRaw json.RawMessage
	)
	for ev := range events {
		if ev.Type != harnesses.EventTypeFinal {
			continue
		}
		var final harnesses.FinalData
		if err := json.Unmarshal(ev.Data, &final); err != nil {
			t.Fatalf("unmarshal final: %v", err)
		}
		finals = append(finals, final)
		finalRaw = append(finalRaw[:0], ev.Data...)
	}
	if len(finals) != 1 {
		t.Fatalf("final events = %d, want 1", len(finals))
	}
	if finals[0].Status != "failed" {
		t.Fatalf("final status = %q, want failed", finals[0].Status)
	}
	if finals[0].Error == "" {
		t.Fatal("final error must describe the incomplete transcript")
	}
	if finals[0].ExitCode == 0 {
		t.Fatal("incomplete transcript final exit code = 0, want nonzero")
	}
	if finals[0].FinalText != "" {
		t.Fatalf("incomplete transcript fabricated final text %q", finals[0].FinalText)
	}
	if finals[0].FinalCostUSD != nil || finals[0].FinalCostSource != harnesses.CostSourceUnknown || finals[0].CostUSD != 0 {
		t.Fatalf("incomplete transcript fabricated cost: %+v", finals[0])
	}
	if finals[0].Usage != nil {
		t.Fatalf("incomplete transcript fabricated usage: %+v", finals[0].Usage)
	}
	if strings.Contains(string(finalRaw), `"cost_usd"`) {
		t.Fatalf("incomplete transcript serialized fabricated cost: %s", finalRaw)
	}
	for _, secret := range []string{"prompt-secret", "sk-ant-incomplete123", "acct-incomplete"} {
		if strings.Contains(finals[0].Error, secret) {
			t.Fatalf("incomplete transcript diagnostic retained %q: %q", secret, finals[0].Error)
		}
	}
}

func TestEmitTranscriptAndFinalRejectsNonterminalAssistantTranscript(t *testing.T) {
	// Fail-closed incomplete path still waits the grace window once; keep it short.
	prev := transcriptFinalizationGrace
	transcriptFinalizationGrace = 40 * time.Millisecond
	t.Cleanup(func() { transcriptFinalizationGrace = prev })

	tests := []struct {
		name       string
		stopReason string
	}{
		{name: "tool use is intermediate", stopReason: "tool_use"},
		{name: "missing stop reason", stopReason: ""},
		{name: "unknown stop reason", stopReason: "future_reason"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
			body, err := json.Marshal(map[string]any{
				"type": "assistant",
				"message": map[string]any{
					"role":        "assistant",
					"content":     []map[string]string{{"type": "text", "text": "nonterminal transcript body"}},
					"stop_reason": test.stopReason,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(transcriptPath, append(body, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}

			events := make(chan harnesses.Event, 8)
			seq := int64(0)
			(&Harness{}).emitTranscriptAndFinal(context.Background(), &turnEnv{}, transcriptPath, events, &seq, time.Now(), nil)
			close(events)

			var finals []harnesses.FinalData
			for event := range events {
				if event.Type != harnesses.EventTypeFinal {
					continue
				}
				var final harnesses.FinalData
				if err := json.Unmarshal(event.Data, &final); err != nil {
					t.Fatal(err)
				}
				finals = append(finals, final)
			}
			if len(finals) != 1 || finals[0].Status != "failed" || finals[0].ExitCode == 0 {
				t.Fatalf("finals = %+v, want one failed nonzero final", finals)
			}
			if finals[0].FinalText != "" || finals[0].Usage != nil || finals[0].FinalCostUSD != nil {
				t.Fatalf("nonterminal assistant fabricated completion evidence: %+v", finals[0])
			}
		})
	}
}

func TestEmitTranscriptAndFinalPreservesContextTerminalStatus(t *testing.T) {
	// Cancelled/deadline contexts must not sit on the flush grace.
	prev := transcriptFinalizationGrace
	transcriptFinalizationGrace = 5 * time.Second
	t.Cleanup(func() { transcriptFinalizationGrace = prev })

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"assistant"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		context  func() context.Context
		status   string
		exitCode int
	}{
		{
			name: "cancelled",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			status:   "cancelled",
			exitCode: 130,
		},
		{
			name: "deadline exceeded",
			context: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				t.Cleanup(cancel)
				return ctx
			},
			status:   "timed_out",
			exitCode: 124,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make(chan harnesses.Event, 2)
			seq := int64(0)
			(&Harness{}).emitTranscriptAndFinal(test.context(), &turnEnv{}, transcriptPath, events, &seq, time.Now(), nil)
			close(events)
			event := <-events
			var final harnesses.FinalData
			if err := json.Unmarshal(event.Data, &final); err != nil {
				t.Fatal(err)
			}
			if final.Status != test.status || final.ExitCode != test.exitCode {
				t.Fatalf("final = %+v, want status=%s exit=%d", final, test.status, test.exitCode)
			}
		})
	}
}

func TestEmitTranscriptAndFinalDoesNotLogSensitiveTranscriptFailure(t *testing.T) {
	const sensitive = "sk-ant-log-secret acct-log-secret prompt-log-secret"
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	events := make(chan harnesses.Event, 2)
	seq := int64(0)
	(&Harness{}).emitTranscriptAndFinal(
		context.Background(), &turnEnv{}, filepath.Join(t.TempDir(), sensitive, "transcript.jsonl"),
		events, &seq, time.Now(), logger,
	)
	close(events)

	for _, forbidden := range strings.Fields(sensitive) {
		if strings.Contains(log.String(), forbidden) {
			t.Fatalf("log retained sensitive value %q: %q", forbidden, log.String())
		}
	}
}

func TestEmitFinalEventSanitizesDiagnosticWithoutFailureClass(t *testing.T) {
	events := make(chan harnesses.Event, 1)
	emitFinalEvent(events, 1, time.Now(), "timed_out",
		"turn timeout ANTHROPIC_API_KEY=timeout-secret account acct-raw-secret", 124)
	close(events)

	event := <-events
	var final harnesses.FinalData
	if err := json.Unmarshal(event.Data, &final); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"timeout-secret", "acct-raw-secret"} {
		if strings.Contains(final.Error, secret) {
			t.Errorf("generic final retained %q: %q", secret, final.Error)
		}
	}
	if final.RoutingActual != nil {
		t.Fatalf("generic timeout gained route-failure evidence: %+v", final.RoutingActual)
	}
}

// TestEmitTranscriptAndFinalWaitsForLateEndTurn proves the Claude Code 2.1.x
// Stop-before-end_turn flush race: the Stop hook can land while the transcript
// still ends on tool_use, then end_turn is appended milliseconds later. Without
// the grace wait the harness failed closed despite a completed turn.
func TestEmitTranscriptAndFinalWaitsForLateEndTurn(t *testing.T) {
	prev := transcriptFinalizationGrace
	transcriptFinalizationGrace = 2 * time.Second
	t.Cleanup(func() { transcriptFinalizationGrace = prev })

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	toolUse, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "tool_use", "id": "toolu_1", "name": "Write", "input": map[string]string{"file_path": "x", "content": "ok"}},
			},
			"stop_reason": "tool_use",
			"usage":       map[string]int{"input_tokens": 1, "output_tokens": 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, append(toolUse, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	endTurn, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":        "assistant",
			"content":     []map[string]string{{"type": "text", "text": "done"}},
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": 2, "output_tokens": 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Append end_turn after the reader has entered the grace wait.
	go func() {
		time.Sleep(150 * time.Millisecond)
		f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		_, _ = f.Write(append(endTurn, '\n'))
		_ = f.Close()
	}()

	events := make(chan harnesses.Event, 16)
	seq := int64(0)
	start := time.Now()
	(&Harness{}).emitTranscriptAndFinal(context.Background(), &turnEnv{}, transcriptPath, events, &seq, start, nil)
	close(events)

	var finals []harnesses.FinalData
	for event := range events {
		if event.Type != harnesses.EventTypeFinal {
			continue
		}
		var final harnesses.FinalData
		if err := json.Unmarshal(event.Data, &final); err != nil {
			t.Fatal(err)
		}
		finals = append(finals, final)
	}
	if len(finals) != 1 {
		t.Fatalf("final count = %d, want 1", len(finals))
	}
	if finals[0].Status != "success" {
		t.Fatalf("status = %q err=%q, want success after late end_turn", finals[0].Status, finals[0].Error)
	}
	if !strings.Contains(finals[0].FinalText, "done") {
		t.Fatalf("final text = %q, want done", finals[0].FinalText)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("waited %s for late end_turn; grace should resolve quickly once the line appears", elapsed)
	}
}
