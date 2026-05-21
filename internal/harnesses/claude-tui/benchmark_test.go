package claudetui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
)

// cannedPrompt is the shared fixture prompt used for benchmarking.
// It is designed to be deterministic and fast to evaluate.
const cannedPrompt = "Hello. Please reply with exactly the word 'ok' and nothing else."

// cannedSystemPrompt is the system prompt for the canned benchmark.
const cannedSystemPrompt = "You are a helpful assistant. Be concise."

// BenchmarkClaudeTuiTurnWallTime benchmarks the wall-time-per-turn for the
// claude-tui PTY harness and validates that:
// 1. Mean wall-time-per-turn is within 2x of the claude --print baseline
// 2. Loop overhead beyond inference is under 10ms
//
// Per ADR-013 §3, these constraints must hold before promotion to auto-routing.
// If the claude --print baseline cannot be measured (e.g., in offline environments),
// the benchmark reports the absolute timing but skips the 2x validation.
func BenchmarkClaudeTuiTurnWallTime(b *testing.B) {
	// Measure the baseline wall time for claude --print on the canned prompt.
	baselineNS := measureClaudeBaseline(b)
	baselineMS := float64(baselineNS) / 1e6
	hasBaseline := baselineNS > 0

	h := &claudetui.Harness{}
	ctx := context.Background()

	// Create a temporary working directory for the benchmark.
	tmpDir := b.TempDir()

	// Run N iterations of the PTY turn path and measure wall time.
	// Per CONTRACT-004 §5, multiple harness instances must share the session pool,
	// so we create a single harness and reuse it across iterations.
	var totalNS int64
	var measurements []int64

	for i := 0; i < b.N; i++ {
		start := time.Now()

		// Run one turn through the PTY path.
		eventCh, err := h.Execute(ctx, harnesses.ExecuteRequest{
			Prompt:       cannedPrompt,
			SystemPrompt: cannedSystemPrompt,
			Model:        "claude-sonnet-4-6",
			WorkDir:      tmpDir,
			Metadata: map[string]string{
				"benchmark_iteration": fmt.Sprintf("%d", i),
			},
		})
		if err != nil {
			b.Fatalf("Execute failed at iteration %d: %v", i, err)
		}

		// Drain the event channel.
		for range eventCh {
		}

		elapsed := time.Since(start)
		elapsedNS := elapsed.Nanoseconds()
		totalNS += elapsedNS
		measurements = append(measurements, elapsedNS)
	}

	// Compute statistics.
	meanNS := totalNS / int64(b.N)
	meanMS := float64(meanNS) / 1e6

	// Report measurements.
	if hasBaseline {
		b.Logf("Baseline (claude --print):        %.3f ms", baselineMS)
		b.Logf("Mean wall-time-per-turn (PTY):   %.3f ms", meanMS)

		// Calculate loop overhead: mean measured time minus baseline inference time.
		// This represents the overhead from PTY session management, parsing, and event emission.
		loopOverheadMS := meanMS - baselineMS
		b.Logf("Loop overhead (beyond inference): %.3f ms", loopOverheadMS)

		// Validate constraint #1: wall-time-per-turn within 2x of baseline.
		const maxWallTimeRatio = 2.0
		actualRatio := meanMS / baselineMS
		if actualRatio > maxWallTimeRatio {
			b.Errorf("wall-time-per-turn %.3f ms exceeds 2x baseline (%.3f x 2 = %.3f ms); ratio: %.2fx",
				meanMS, baselineMS, baselineMS*maxWallTimeRatio, actualRatio)
		}

		// Validate constraint #2: loop overhead < 10ms.
		const maxLoopOverheadMS = 10.0
		if loopOverheadMS > maxLoopOverheadMS {
			b.Errorf("loop overhead %.3f ms exceeds 10ms threshold; consider optimizing PTY session pooling or frame parsing",
				loopOverheadMS)
		}
	} else {
		// Baseline not available (offline environment); report absolute timing.
		b.Logf("Mean wall-time-per-turn (PTY):   %.3f ms (baseline unavailable)", meanMS)
		b.Logf("Note: 2x baseline validation skipped; measure on a system with 'claude' binary installed")
	}

	// Log percentile statistics for additional visibility.
	if len(measurements) > 0 {
		sortMeasurements(measurements)
		p50 := measurements[len(measurements)/2]
		p95 := measurements[len(measurements)*95/100]
		p99 := measurements[len(measurements)*99/100]
		b.Logf("Percentiles: p50=%.3f ms, p95=%.3f ms, p99=%.3f ms",
			float64(p50)/1e6, float64(p95)/1e6, float64(p99)/1e6)
	}

	// Cleanup.
	if err := claudetui.Shutdown(ctx); err != nil {
		b.Logf("Shutdown warning: %v", err)
	}
}

// measureClaudeBaseline runs the claude --print command on the canned prompt
// and measures its wall-clock time. This serves as the baseline against which
// claude-tui's PTY overhead is calculated.
//
// Returns the measured time in nanoseconds, or 0 if the command is not available.
func measureClaudeBaseline(b *testing.B) int64 {
	// Resolve the claude binary.
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		b.Logf("claude binary not found: %v; cannot measure baseline", err)
		return 0
	}

	// Prepare the claude command with --print and stream-json output.
	cmd := exec.Command(claudePath, "--print", "-p", "--output-format", "stream-json")

	// Pipe the canned prompt to stdin.
	cmd.Stdin = strings.NewReader(cannedPrompt)

	// Capture stdout (we don't validate it, just measure).
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard

	// Measure the wall time to completion.
	start := time.Now()
	if err := cmd.Run(); err != nil {
		// claude --print may fail for various reasons (e.g., no API key, quota exceeded).
		// In those cases, log a warning and return 0 to skip the validation.
		b.Logf("claude --print baseline failed (this may be expected in offline environments): %v", err)
		return 0
	}
	elapsed := time.Since(start)

	return elapsed.Nanoseconds()
}

// sortMeasurements sorts a slice of nanosecond measurements in ascending order.
func sortMeasurements(m []int64) {
	// Simple bubble sort for small slices (typically b.N iterations).
	for i := 0; i < len(m); i++ {
		for j := i + 1; j < len(m); j++ {
			if m[j] < m[i] {
				m[i], m[j] = m[j], m[i]
			}
		}
	}
}

// TestCanonicalPromptsAreDeterministic verifies the canned prompt fixture
// is suitable for benchmarking by checking that it parses correctly.
func TestCanonicalPromptsAreDeterministic(t *testing.T) {
	// Verify the canned prompt is a non-empty string.
	if cannedPrompt == "" {
		t.Error("cannedPrompt is empty; benchmark fixture is invalid")
	}
	if cannedSystemPrompt == "" {
		t.Error("cannedSystemPrompt is empty; benchmark fixture is invalid")
	}

	// Verify the prompts don't contain control characters that could confuse parsing.
	if strings.Contains(cannedPrompt, "\r") || strings.Contains(cannedPrompt, "\n") {
		t.Error("cannedPrompt contains newlines; single-line fixture expected")
	}

	// Verify JSON marshaling works (event metadata uses JSON encoding).
	testReq := harnesses.ExecuteRequest{
		Prompt:       cannedPrompt,
		SystemPrompt: cannedSystemPrompt,
		Model:        "claude-sonnet-4-6",
		Metadata: map[string]string{
			"benchmark": "true",
		},
	}
	metadata := testReq.Metadata
	_, err := json.Marshal(metadata)
	if err != nil {
		t.Errorf("metadata not JSON-marshable: %v", err)
	}
}

// BenchmarkClaudeTuiTurnWallTimeParallel is a parallel variant of the wall-time
// benchmark that measures throughput when multiple turns are executed concurrently.
// This helps detect contention in the session pool or event channel buffering.
func BenchmarkClaudeTuiTurnWallTimeParallel(b *testing.B) {
	h := &claudetui.Harness{}
	ctx := context.Background()
	tmpDir := b.TempDir()

	var mu sync.Mutex
	var totalNS int64

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			start := time.Now()

			eventCh, err := h.Execute(ctx, harnesses.ExecuteRequest{
				Prompt:       cannedPrompt,
				SystemPrompt: cannedSystemPrompt,
				Model:        "claude-sonnet-4-6",
				WorkDir:      tmpDir,
				Metadata: map[string]string{
					"parallel_iteration": fmt.Sprintf("%d", i),
				},
			})
			if err != nil {
				b.Fatalf("Execute failed: %v", err)
			}

			for range eventCh {
			}

			elapsed := time.Since(start)
			mu.Lock()
			totalNS += elapsed.Nanoseconds()
			mu.Unlock()
			i++
		}
	})

	// Cleanup.
	if err := claudetui.Shutdown(ctx); err != nil {
		b.Logf("Shutdown warning: %v", err)
	}

	// Report aggregate throughput.
	if b.N > 0 {
		meanMS := float64(totalNS) / float64(b.N) / 1e6
		b.Logf("Parallel mean wall-time-per-turn: %.3f ms", meanMS)
	}
}
