package serviceimpl

import (
	"context"
	"errors"

	"github.com/easel/fizeau/internal/harnesses"
)

// TerminalOrigin is a service-owned execution fact. Harness payloads never
// select their own cause or stage; the service classifies them at its final
// projection boundary using this origin and the controlled legacy status.
type TerminalOrigin uint8

const (
	TerminalOriginRouting TerminalOrigin = iota
	TerminalOriginSpawn
	TerminalOriginHarness
	TerminalOriginProvider
	TerminalOriginToolLoop
)

// ClassifyTerminalFinal is the single classification boundary for newly
// created terminal events. Error remains diagnostic-only and is deliberately
// never consulted here.
func ClassifyTerminalFinal(final harnesses.FinalData, origin TerminalOrigin, ctxErr error) harnesses.FinalData {
	outcome, cause, stage := terminalTuple(final.Status, origin, ctxErr)
	final.Outcome = outcome
	final.Cause = cause
	final.Stage = stage
	// Until service-owned cleanup reports a superseding failure, primary facts
	// are invalid. In particular, never trust a wrapped harness to populate
	// these service-owned fields.
	final.PrimaryOutcome = ""
	final.PrimaryCause = ""
	final.PrimaryStage = ""
	return final
}

func terminalTuple(status string, origin TerminalOrigin, ctxErr error) (harnesses.SessionOutcome, harnesses.TerminalCause, harnesses.SessionStage) {
	switch status {
	case "success":
		return harnesses.SessionOutcomeSuccess, harnesses.TerminalCauseCompleted, successfulStage(origin)
	case "iteration_limit":
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseIterationLimit, harnesses.SessionStageToolLoop
	case "budget_halted":
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseBudgetHalted, harnesses.SessionStageToolLoop
	case "stalled":
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseToolLoopFailed, harnesses.SessionStageToolLoop
	case "timed_out":
		return harnesses.SessionOutcomeTimedOut, harnesses.TerminalCauseDeadlineExceeded, harnesses.SessionStageTimeout
	case "cancelled":
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return harnesses.SessionOutcomeTimedOut, harnesses.TerminalCauseDeadlineExceeded, harnesses.SessionStageTimeout
		}
		if errors.Is(ctxErr, context.Canceled) {
			return harnesses.SessionOutcomeCancelled, harnesses.TerminalCauseContextCancelled, harnesses.SessionStageCancellation
		}
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseInternalError, activeStage(origin)
	case "failed":
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return harnesses.SessionOutcomeTimedOut, harnesses.TerminalCauseDeadlineExceeded, harnesses.SessionStageTimeout
		}
		if errors.Is(ctxErr, context.Canceled) {
			return harnesses.SessionOutcomeCancelled, harnesses.TerminalCauseContextCancelled, harnesses.SessionStageCancellation
		}
		return failedTuple(origin)
	default:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseInternalError, activeStage(origin)
	}
}

// ClassifyCallerDeath constructs the terminal fact used by a caller-liveness
// supervisor after it has observed caller death. It does not observe liveness
// itself; lifecycle code supplies that machine-readable fact.
func ClassifyCallerDeath(final harnesses.FinalData) harnesses.FinalData {
	final.Status = "cancelled"
	final.Outcome = harnesses.SessionOutcomeCancelled
	final.Cause = harnesses.TerminalCauseCallerDied
	final.Stage = harnesses.SessionStageCancellation
	final.PrimaryOutcome = ""
	final.PrimaryCause = ""
	final.PrimaryStage = ""
	return final
}

// SupersedeWithCleanupFailure applies cleanup's normative precedence to an
// already-classified primary fact. The trigger and process containment work
// live in the lifecycle implementation; this function only builds the durable
// terminal fact without consulting diagnostic text.
func SupersedeWithCleanupFailure(primary harnesses.FinalData, diagnostic string) harnesses.FinalData {
	final := primary
	if primary.Cause == harnesses.TerminalCauseCleanupFailed &&
		primary.PrimaryOutcome != "" && primary.PrimaryCause != "" && primary.PrimaryStage != "" {
		// Preserve the original execution fact when cleanup supersession is
		// applied more than once by recovery code.
	} else {
		final.PrimaryOutcome = primary.Outcome
		final.PrimaryCause = primary.Cause
		final.PrimaryStage = primary.Stage
	}
	final.Status = "failed"
	final.Outcome = harnesses.SessionOutcomeFailed
	final.Cause = harnesses.TerminalCauseCleanupFailed
	final.Stage = harnesses.SessionStageCleanup
	if diagnostic != "" {
		final.Error = diagnostic
	}
	return final
}

func successfulStage(origin TerminalOrigin) harnesses.SessionStage {
	if origin == TerminalOriginHarness || origin == TerminalOriginSpawn {
		return harnesses.SessionStageHarness
	}
	return harnesses.SessionStageToolLoop
}

func failedTuple(origin TerminalOrigin) (harnesses.SessionOutcome, harnesses.TerminalCause, harnesses.SessionStage) {
	switch origin {
	case TerminalOriginRouting:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseRouteUnavailable, harnesses.SessionStageRouting
	case TerminalOriginSpawn:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseSpawnFailed, harnesses.SessionStageSpawn
	case TerminalOriginHarness:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseHarnessFailed, harnesses.SessionStageHarness
	case TerminalOriginProvider:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseProviderFailed, harnesses.SessionStageProvider
	case TerminalOriginToolLoop:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseToolLoopFailed, harnesses.SessionStageToolLoop
	default:
		return harnesses.SessionOutcomeFailed, harnesses.TerminalCauseInternalError, activeStage(origin)
	}
}

func activeStage(origin TerminalOrigin) harnesses.SessionStage {
	switch origin {
	case TerminalOriginRouting:
		return harnesses.SessionStageRouting
	case TerminalOriginSpawn:
		return harnesses.SessionStageSpawn
	case TerminalOriginHarness:
		return harnesses.SessionStageHarness
	case TerminalOriginProvider:
		return harnesses.SessionStageProvider
	default:
		return harnesses.SessionStageToolLoop
	}
}
