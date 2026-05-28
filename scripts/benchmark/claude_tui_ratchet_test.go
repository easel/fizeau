package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// benchmarkRun represents a single benchmark measurement from the fixture.
type benchmarkRun struct {
	RunID                 string  `json:"run_id"`
	Timestamp             string  `json:"timestamp"`
	Mode                  string  `json:"mode"`
	WallTimeMS            int64   `json:"wall_time_ms"`
	TotalLatencyMS        int64   `json:"total_latency_ms"`
	ModelInferenceTimeMS  int64   `json:"model_inference_time_ms"`
	LoopOverheadMS        int64   `json:"loop_overhead_ms"`
	PromptTokens          int64   `json:"prompt_tokens"`
	CompletionTokens      int64   `json:"completion_tokens"`
	CostCents             float64 `json:"cost_cents"`
	BillingClassification string  `json:"billing_classification"`
}

// benchmarkFixture represents the complete test fixture with baseline measurements.
type benchmarkFixture struct {
	FixtureVersion string         `json:"fixture_version"`
	PromptFixture  string         `json:"prompt_fixture"`
	Runs           []benchmarkRun `json:"runs"`
	Notes          string         `json:"notes"`
}

// loadFixture loads the baseline benchmark fixture from the testdata directory.
func loadFixture(t *testing.T) benchmarkFixture {
	// Locate the fixture relative to this test file.
	// This test is at scripts/benchmark/claude_tui_ratchet_test.go
	// and the fixture is at scripts/benchmark/testdata/claude-tui-ratchet/baseline.json
	fixturePath := filepath.Join("testdata", "claude-tui-ratchet", "baseline.json")
	data, err := os.ReadFile(fixturePath)
	require.NoError(t, err, "failed to load fixture from %s", fixturePath)

	var fixture benchmarkFixture
	err = json.Unmarshal(data, &fixture)
	require.NoError(t, err, "failed to unmarshal fixture JSON")

	return fixture
}

// meanInt64 computes the mean of a slice of int64 values.
func meanInt64(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sum := int64(0)
	for _, v := range values {
		sum += v
	}
	return sum / int64(len(values))
}

// stdDevInt64 computes the standard deviation of a slice of int64 values.
func stdDevInt64(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	mean := float64(meanInt64(values))
	sumSq := 0.0
	for _, v := range values {
		diff := float64(v) - mean
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

// TestClaudeTUIRatchet validates that claude-tui loop overhead meets the documented
// performance bounds from the testing concern (concerns.md).
//
// The test asserts that claude-tui per-turn overhead (beyond model inference time)
// is either:
//  1. Less than 10ms (hard limit), OR
//  2. Within 2x of the claude --print baseline (soft limit)
//
// whichever is looser. Test failure includes measured values for diagnostics.
//
// This test uses recorded fixture data by default (runs in CI without build tags).
// When the harness_integration build tag is present, it may be extended to run
// live measurements against an installed claude binary (not implemented in this pass).
func TestClaudeTUIRatchet(t *testing.T) {
	fixture := loadFixture(t)

	// Separate runs by mode.
	var printRuns, tuiRuns []benchmarkRun
	for _, run := range fixture.Runs {
		switch run.Mode {
		case "claude-print":
			printRuns = append(printRuns, run)
		case "claude-tui":
			tuiRuns = append(tuiRuns, run)
		}
	}

	// Ensure we have measurements from both modes.
	require.Greater(t, len(printRuns), 0, "fixture must include at least one claude-print run")
	require.Greater(t, len(tuiRuns), 0, "fixture must include at least one claude-tui run")

	// Extract loop overhead measurements for each mode.
	var printOverheads, tuiOverheads []int64
	for _, run := range printRuns {
		printOverheads = append(printOverheads, run.LoopOverheadMS)
	}
	for _, run := range tuiRuns {
		tuiOverheads = append(tuiOverheads, run.LoopOverheadMS)
	}

	// Compute mean and stddev for each mode.
	meanPrintOverhead := meanInt64(printOverheads)
	stdDevPrintOverhead := stdDevInt64(printOverheads)
	meanTUIOverhead := meanInt64(tuiOverheads)
	stdDevTUIOverhead := stdDevInt64(tuiOverheads)

	// Validate the ratchet: claude-tui overhead must be <10ms OR within 2x of baseline.
	const (
		hardLimitMS    = 10
		softMultiplier = 2
	)

	passesHardLimit := meanTUIOverhead < hardLimitMS
	passesSoftLimit := meanTUIOverhead <= meanPrintOverhead*softMultiplier

	// Construct diagnostic message with measured values.
	diagnostics := fmt.Sprintf(
		"Loop overhead ratchet validation:\n"+
			"  claude-print: mean=%dms (σ=%.2f, n=%d)\n"+
			"  claude-tui:   mean=%dms (σ=%.2f, n=%d)\n"+
			"  hard limit:   < %dms, passes=%v\n"+
			"  soft limit:   <= %d × baseline = %dms, passes=%v",
		meanPrintOverhead, stdDevPrintOverhead, len(printRuns),
		meanTUIOverhead, stdDevTUIOverhead, len(tuiRuns),
		hardLimitMS, passesHardLimit,
		softMultiplier, meanPrintOverhead*softMultiplier, passesSoftLimit,
	)

	t.Logf("%s", diagnostics)

	// Assert that at least one of the two limits passes.
	// If both fail, the message includes the diagnostic breakdown.
	assert.True(t, passesHardLimit || passesSoftLimit,
		"claude-tui loop overhead exceeds both hard and soft limits:\n"+diagnostics)
}

// TestBenchmarkFixtureConsistency validates that the fixture data is well-formed
// and contains the expected structure.
func TestBenchmarkFixtureConsistency(t *testing.T) {
	fixture := loadFixture(t)

	// Validate fixture metadata.
	assert.NotEmpty(t, fixture.FixtureVersion, "fixture must include version")
	assert.NotEmpty(t, fixture.PromptFixture, "fixture must include prompt")
	assert.Greater(t, len(fixture.Runs), 0, "fixture must include at least one run")

	// Ensure we have at least 3 runs of each mode.
	modeCount := make(map[string]int)
	for _, run := range fixture.Runs {
		modeCount[run.Mode]++
		// Validate required fields in each run.
		assert.NotEmpty(t, run.RunID, "run must have run_id")
		assert.NotEmpty(t, run.Timestamp, "run must have timestamp")
		assert.NotEmpty(t, run.Mode, "run must have mode")
		assert.Greater(t, run.WallTimeMS, int64(0), "run must have positive wall_time_ms")
		assert.Greater(t, run.ModelInferenceTimeMS, int64(0), "run must have positive model_inference_time_ms")
		assert.GreaterOrEqual(t, run.LoopOverheadMS, int64(0), "run must have non-negative loop_overhead_ms")
		assert.NotEmpty(t, run.BillingClassification, "run must have billing_classification")
	}

	// Validate that we have at least 3 runs of each mode.
	for mode, count := range modeCount {
		assert.GreaterOrEqual(t, count, 3, "fixture must include at least 3 runs of mode %q (got %d)", mode, count)
	}

	// Validate billing classifications.
	for _, run := range fixture.Runs {
		switch run.Mode {
		case "claude-print":
			assert.Equal(t, "api-metered", run.BillingClassification,
				"claude-print runs should be classified as api-metered")
		case "claude-tui":
			assert.Equal(t, "subscription", run.BillingClassification,
				"claude-tui runs should be classified as subscription")
		default:
			t.Logf("unknown mode in fixture: %s", run.Mode)
		}
	}
}
