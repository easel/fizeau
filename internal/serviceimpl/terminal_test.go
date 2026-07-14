package serviceimpl

import (
	"context"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestExecuteTerminalClassificationAcrossFailureStages(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		origin  TerminalOrigin
		ctxErr  error
		outcome harnesses.SessionOutcome
		cause   harnesses.TerminalCause
		stage   harnesses.SessionStage
	}{
		{"native success", "success", TerminalOriginProvider, nil, harnesses.SessionOutcomeSuccess, harnesses.TerminalCauseCompleted, harnesses.SessionStageToolLoop},
		{"wrapped success", "success", TerminalOriginHarness, nil, harnesses.SessionOutcomeSuccess, harnesses.TerminalCauseCompleted, harnesses.SessionStageHarness},
		{"routing", "failed", TerminalOriginRouting, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseRouteUnavailable, harnesses.SessionStageRouting},
		{"spawn", "failed", TerminalOriginSpawn, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseSpawnFailed, harnesses.SessionStageSpawn},
		{"harness", "failed", TerminalOriginHarness, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseHarnessFailed, harnesses.SessionStageHarness},
		{"provider", "failed", TerminalOriginProvider, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseProviderFailed, harnesses.SessionStageProvider},
		{"stall", "stalled", TerminalOriginProvider, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseToolLoopFailed, harnesses.SessionStageToolLoop},
		{"iteration", "iteration_limit", TerminalOriginProvider, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseIterationLimit, harnesses.SessionStageToolLoop},
		{"budget", "budget_halted", TerminalOriginProvider, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseBudgetHalted, harnesses.SessionStageToolLoop},
		{"timeout status", "timed_out", TerminalOriginHarness, nil, harnesses.SessionOutcomeTimedOut, harnesses.TerminalCauseDeadlineExceeded, harnesses.SessionStageTimeout},
		{"deadline context", "cancelled", TerminalOriginHarness, context.DeadlineExceeded, harnesses.SessionOutcomeTimedOut, harnesses.TerminalCauseDeadlineExceeded, harnesses.SessionStageTimeout},
		{"cancellation", "cancelled", TerminalOriginHarness, context.Canceled, harnesses.SessionOutcomeCancelled, harnesses.TerminalCauseContextCancelled, harnesses.SessionStageCancellation},
		{"cancelled without evidence", "cancelled", TerminalOriginHarness, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseInternalError, harnesses.SessionStageHarness},
		{"unknown controlled status", "future_status", TerminalOriginProvider, nil, harnesses.SessionOutcomeFailed, harnesses.TerminalCauseInternalError, harnesses.SessionStageProvider},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := ClassifyTerminalFinal(harnesses.FinalData{Status: tt.status, Error: "timeout provider cancelled"}, tt.origin, tt.ctxErr)
			second := ClassifyTerminalFinal(harnesses.FinalData{Status: tt.status, Error: "entirely different diagnostics"}, tt.origin, tt.ctxErr)
			if first.Outcome != tt.outcome || first.Cause != tt.cause || first.Stage != tt.stage {
				t.Fatalf("tuple = %q/%q/%q, want %q/%q/%q", first.Outcome, first.Cause, first.Stage, tt.outcome, tt.cause, tt.stage)
			}
			if first.Outcome != second.Outcome || first.Cause != second.Cause || first.Stage != second.Stage {
				t.Fatalf("diagnostic error text changed classification: %#v vs %#v", first, second)
			}
		})
	}

	t.Run("wrapped primary tuple is not trusted", func(t *testing.T) {
		got := ClassifyTerminalFinal(harnesses.FinalData{
			Status:         "failed",
			PrimaryOutcome: harnesses.SessionOutcomeSuccess,
			PrimaryCause:   harnesses.TerminalCauseCompleted,
			PrimaryStage:   harnesses.SessionStageHarness,
		}, TerminalOriginHarness, nil)
		if got.PrimaryOutcome != "" || got.PrimaryCause != "" || got.PrimaryStage != "" {
			t.Fatalf("unowned primary tuple survived classification: %#v", got)
		}
	})
}

func TestClassifyCleanupFailurePreservesPrimaryTuple(t *testing.T) {
	primary := ClassifyTerminalFinal(harnesses.FinalData{Status: "timed_out"}, TerminalOriginProvider, nil)
	got := SupersedeWithCleanupFailure(primary, "containment still occupied")
	if got.Outcome != harnesses.SessionOutcomeFailed || got.Cause != harnesses.TerminalCauseCleanupFailed || got.Stage != harnesses.SessionStageCleanup {
		t.Fatalf("cleanup tuple = %q/%q/%q", got.Outcome, got.Cause, got.Stage)
	}
	if got.PrimaryOutcome != primary.Outcome || got.PrimaryCause != primary.Cause || got.PrimaryStage != primary.Stage {
		t.Fatalf("primary tuple = %q/%q/%q, want %q/%q/%q", got.PrimaryOutcome, got.PrimaryCause, got.PrimaryStage, primary.Outcome, primary.Cause, primary.Stage)
	}
	again := SupersedeWithCleanupFailure(got, "recovery retry")
	if again.PrimaryOutcome != primary.Outcome || again.PrimaryCause != primary.Cause || again.PrimaryStage != primary.Stage {
		t.Fatalf("repeated cleanup supersession lost primary tuple: %#v", again)
	}
}

func TestClassifyCallerDeath(t *testing.T) {
	got := ClassifyCallerDeath(harnesses.FinalData{Status: "failed", Error: "diagnostic"})
	if got.Outcome != harnesses.SessionOutcomeCancelled || got.Cause != harnesses.TerminalCauseCallerDied || got.Stage != harnesses.SessionStageCancellation {
		t.Fatalf("caller-death tuple = %q/%q/%q", got.Outcome, got.Cause, got.Stage)
	}
}
