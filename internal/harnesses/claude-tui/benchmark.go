package claudetui

import (
	"fmt"
)

// BenchmarkPromptFixture is a shared, deterministic canned prompt used for
// benchmarking both claude-tui and claude --print baseline measurements.
// The prompt is stable across Claude versions and CLI modes, allowing
// apples-to-apples wall-time comparison.
const BenchmarkPromptFixture = `Explain the concept of a for loop in programming in 2-3 sentences.`

// ClaudePrintBaselineResult holds wall-time measurement and output from
// a claude --print baseline run. It is produced by ClaudePrintBaseline.
type ClaudePrintBaselineResult struct {
	// WallTimeMS is the wall-clock duration of the claude --print invocation in milliseconds.
	WallTimeMS int64
	// Stdout is the captured standard output from the claude process.
	Stdout string
	// Stderr is the captured standard error from the claude process (may include quota or auth errors).
	Stderr string
	// ExitCode is the exit code from the claude process (0 on success, non-zero on error).
	ExitCode int
	// Skipped indicates the run was skipped due to unavailability (no live claude binary or auth).
	// When Skipped is true, all other fields should be ignored.
	Skipped bool
	// SkipReason explains why the run was skipped (only meaningful when Skipped is true).
	SkipReason string
}

// TurnWallTimeMeasurement holds per-turn wall-time data for threshold validation.
type TurnWallTimeMeasurement struct {
	// BaselineWallTimePerTurnMS is the mean wall-time per turn from claude --print.
	BaselineWallTimePerTurnMS int64
	// TUIWallTimePerTurnMS is the mean wall-time per turn from claude-tui PTY.
	TUIWallTimePerTurnMS int64
	// LoopOverheadMS is the mean overhead per turn beyond measured baseline inference time.
	LoopOverheadMS int64
}

// CheckTurnWallTimeThresholds validates that PTY turn wall-time measurements
// satisfy ADR-013 §3 thresholds: mean PTY wall-time per turn must be within
// 2x the baseline, and loop overhead must be under 10ms.
// Returns nil if thresholds pass, non-nil error with details if either fails.
func CheckTurnWallTimeThresholds(m TurnWallTimeMeasurement) error {
	const (
		maxWallTimeMultiplier = 2
		maxLoopOverheadMS     = 10
	)

	// Check wall-time threshold: PTY wall-time per turn <= 2x baseline
	maxAllowedWallTimeMS := m.BaselineWallTimePerTurnMS * maxWallTimeMultiplier
	if m.TUIWallTimePerTurnMS > maxAllowedWallTimeMS {
		return fmt.Errorf(
			"PTY wall-time per turn exceeds 2x baseline: baseline=%dms, measured=%dms, allowed=%dms",
			m.BaselineWallTimePerTurnMS, m.TUIWallTimePerTurnMS, maxAllowedWallTimeMS,
		)
	}

	// Check loop overhead threshold: loop overhead <= 10ms
	if m.LoopOverheadMS > maxLoopOverheadMS {
		return fmt.Errorf(
			"loop overhead exceeds 10ms threshold: measured=%dms",
			m.LoopOverheadMS,
		)
	}

	return nil
}

// ClaudePrintBaseline runs the shared benchmark prompt fixture through
// the live claude CLI with --print and measures wall-clock time.
// If the claude binary is unavailable or auth fails, it returns a Skipped result.
//
// This helper is intended for integration testing and benchmarking only;
// it requires a working claude CLI installation and valid authentication.
func ClaudePrintBaseline() ClaudePrintBaselineResult {
	// Placeholder implementation that indicates unavailability.
	// Real implementation requires spawning the claude subprocess,
	// which is deferred pending live auth availability in the test environment.
	return ClaudePrintBaselineResult{
		Skipped:    true,
		SkipReason: "live claude binary integration pending; use fake baseline in CI",
	}
}

// FakeClaudePrintBaseline is used in tests to simulate a claude --print
// invocation without requiring live Anthropic credentials.
// It returns synthetic but realistic timing and output.
func FakeClaudePrintBaseline() ClaudePrintBaselineResult {
	return ClaudePrintBaselineResult{
		WallTimeMS: 1500,
		Stdout:     fmt.Sprintf("Prompt: %s\n\nResponse: A for loop is a control structure that repeats a block of code a specified number of times. It typically includes initialization, a condition, and an increment. For loops are commonly used to iterate over arrays, lists, or ranges of numbers.", BenchmarkPromptFixture),
		Stderr:     "",
		ExitCode:   0,
		Skipped:    false,
	}
}

// ValidateFixtureConsistency returns an error if the BenchmarkPromptFixture
// is empty or does not meet minimum requirements for benchmarking.
func ValidateFixtureConsistency() error {
	if len(BenchmarkPromptFixture) == 0 {
		return fmt.Errorf("BenchmarkPromptFixture is empty")
	}
	if len(BenchmarkPromptFixture) < 10 {
		return fmt.Errorf("BenchmarkPromptFixture too short (minimum 10 bytes, got %d)", len(BenchmarkPromptFixture))
	}
	return nil
}
