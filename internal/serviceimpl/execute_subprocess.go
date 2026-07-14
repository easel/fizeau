package serviceimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/processlifecycle"
	"github.com/easel/fizeau/internal/reasoning"
)

const (
	defaultSubprocessCleanupTimeout             = 10 * time.Second
	subprocessRouteObservationWarningMaxBytes   = 2048
	subprocessRouteObservationWarningTruncation = "...(truncated)"
	subprocessRouteObservationFailedWarningCode = "route_observation_failed"
)

// SubprocessRequest is the API-neutral request data needed by subprocess
// harness runner implementations.
type SubprocessRequest struct {
	Prompt            string
	SystemPrompt      string
	WorkDir           string
	Permissions       string
	Temperature       *float32
	Seed              *int64
	Reasoning         reasoning.Reasoning
	Timeout           time.Duration
	IdleTimeout       time.Duration
	SessionLogDir     string
	SessionID         string
	LifecycleStateDir string
	CleanupTimeout    time.Duration
	Metadata          map[string]string
	Decision          ExecuteRunnerDecision
	Started           time.Time
	SessionLogPath    string
}

// SubprocessCallbacks bridge service-owned event/progress/session-log behavior
// without importing root public service types.
type SubprocessCallbacks struct {
	BeforeExecute func()
	ObserveFinal  func(harnesses.FinalData) error
	ObserveEvent  func(harnesses.Event) harnesses.Event
	EmitEvent     func(harnesses.Event) bool
	// CommitFinal receives the already-classified, cleanup-backed terminal
	// fact. Callers must not classify it again: cleanup supersession carries
	// the primary execution tuple in fields that reclassification would erase.
	CommitFinal func(harnesses.Event, harnesses.FinalData)
	// Finalize and WriteEnd are retained for callers that have not migrated to
	// CommitFinal. New service orchestration should use CommitFinal so one
	// coordinator owns durable and live terminal ordering.
	Finalize func(harnesses.FinalData)
	WriteEnd func(map[string]string, harnesses.FinalData)
}

// RunSubprocess executes a subprocess harness and forwards its event stream.
func RunSubprocess(ctx context.Context, req SubprocessRequest, runner harnesses.Harness, cb SubprocessCallbacks) {
	hReq := harnesses.ExecuteRequest{
		Prompt:            req.Prompt,
		SystemPrompt:      req.SystemPrompt,
		Provider:          req.Decision.Provider,
		Model:             req.Decision.Model,
		WorkDir:           req.WorkDir,
		Permissions:       req.Permissions,
		Temperature:       subprocessTemperature(req.Temperature),
		Seed:              subprocessSeed(req.Seed),
		Reasoning:         adapterReasoning(req.Reasoning),
		Timeout:           req.Timeout,
		IdleTimeout:       req.IdleTimeout,
		SessionLogDir:     req.SessionLogDir,
		SessionID:         req.SessionID,
		LifecycleStateDir: req.LifecycleStateDir,
		CleanupTimeout:    req.CleanupTimeout,
		Metadata:          req.Metadata,
	}
	if cb.BeforeExecute != nil {
		cb.BeforeExecute()
	}
	in, err := runner.Execute(ctx, hReq)
	if err != nil {
		durationMS := int64(0)
		if !req.Started.IsZero() {
			durationMS = time.Since(req.Started).Milliseconds()
		}
		final := harnesses.FinalData{
			Status:     "failed",
			Error:      err.Error(),
			DurationMS: durationMS,
			RoutingActual: &harnesses.RoutingActual{
				Harness:        req.Decision.Harness,
				Provider:       req.Decision.Provider,
				ServerInstance: req.Decision.ServerInstance,
				Model:          req.Decision.Model,
			},
		}
		emitSubprocessFinal(ctx, req, cb, harnesses.Event{
			Type:     harnesses.EventTypeFinal,
			Time:     time.Now().UTC(),
			Metadata: req.Metadata,
		}, final, TerminalOriginSpawn)
		return
	}
	for {
		var (
			ev harnesses.Event
			ok bool
		)
		select {
		case ev, ok = <-in:
		case <-ctx.Done():
			// Prefer a final that is already ready, but do not let an adapter
			// that stalls or forgets to close its stream defeat the independent
			// service-owned cleanup deadline.
			select {
			case ev, ok = <-in:
			default:
				emitSubprocessProtocolFinal(ctx, req, cb, "harness did not emit a final event before request cancellation")
				return
			}
		}
		if !ok {
			emitSubprocessProtocolFinal(ctx, req, cb, "harness event stream closed without a final event")
			return
		}
		if ev.Metadata == nil {
			ev.Metadata = req.Metadata
		}
		if ev.Type == harnesses.EventTypeFinal {
			var final harnesses.FinalData
			if err := json.Unmarshal(ev.Data, &final); err != nil {
				emitSubprocessProtocolFinal(ctx, req, cb, fmt.Sprintf("malformed harness final event: %v", err))
				return
			}
			emitSubprocessFinal(ctx, req, cb, ev, final, TerminalOriginHarness)
			return
		}
		if cb.ObserveEvent != nil {
			ev = cb.ObserveEvent(ev)
		}
		if cb.EmitEvent != nil {
			// Delivery backpressure or request cancellation may suppress a
			// non-terminal event, but must not stop us draining the harness to
			// its cleanup-backed final fact.
			_ = cb.EmitEvent(ev)
		}
	}
}

func replaceSubprocessFinal(ev harnesses.Event, final harnesses.FinalData) harnesses.Event {
	raw, err := json.Marshal(final)
	if err == nil {
		ev.Data = raw
	}
	return ev
}

func emitSubprocessProtocolFinal(ctx context.Context, req SubprocessRequest, cb SubprocessCallbacks, diagnostic string) {
	status := "internal_error"
	if ctx.Err() != nil {
		status = "cancelled"
	}
	durationMS := int64(0)
	if !req.Started.IsZero() {
		durationMS = time.Since(req.Started).Milliseconds()
	}
	final := harnesses.FinalData{
		Status:     status,
		Error:      diagnostic,
		DurationMS: durationMS,
	}
	ev := harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Time:     time.Now().UTC(),
		Metadata: req.Metadata,
	}
	emitSubprocessFinal(ctx, req, cb, ev, final, TerminalOriginHarness)
}

func emitSubprocessFinal(ctx context.Context, req SubprocessRequest, cb SubprocessCallbacks, ev harnesses.Event, final harnesses.FinalData, origin TerminalOrigin) {
	final = ClassifyTerminalFinal(final, origin, ctx.Err())
	if failed, diagnostic := waitForSubprocessCleanup(ctx, req); failed {
		final = SupersedeWithCleanupFailure(final, diagnostic)
	}
	ev = replaceSubprocessFinal(ev, final)
	ev = stampSubprocessFinalRouting(ev, req.Decision)
	ev = stampSubprocessFinalSessionLog(ev, req.SessionLogPath)
	if err := json.Unmarshal(ev.Data, &final); err == nil {
		if cb.ObserveFinal != nil {
			var observed harnesses.FinalData
			_ = json.Unmarshal(ev.Data, &observed)
			if err := cb.ObserveFinal(observed); err != nil {
				final.Warnings = append(final.Warnings, harnesses.FinalWarning{
					Code:    subprocessRouteObservationFailedWarningCode,
					Message: boundedSubprocessRouteObservationWarning(err),
				})
				ev = replaceSubprocessFinal(ev, final)
			}
		}
	}
	if cb.CommitFinal != nil {
		if cb.ObserveEvent != nil {
			ev = cb.ObserveEvent(ev)
		}
		cb.CommitFinal(ev, final)
		return
	}
	if cb.WriteEnd != nil {
		var written harnesses.FinalData
		_ = json.Unmarshal(ev.Data, &written)
		cb.WriteEnd(req.Metadata, written)
	}
	if cb.ObserveEvent != nil {
		ev = cb.ObserveEvent(ev)
	}
	if cb.EmitEvent != nil {
		cb.EmitEvent(ev)
	} else if cb.Finalize != nil {
		cb.Finalize(final)
	}
}

func boundedSubprocessRouteObservationWarning(err error) string {
	message := strings.ToValidUTF8("route final observation failed: "+err.Error(), "\uFFFD")
	if len(message) <= subprocessRouteObservationWarningMaxBytes {
		return message
	}
	limit := subprocessRouteObservationWarningMaxBytes - len(subprocessRouteObservationWarningTruncation)
	for limit > 0 && !utf8.RuneStart(message[limit]) {
		limit--
	}
	return message[:limit] + subprocessRouteObservationWarningTruncation
}

// waitForSubprocessCleanup is the service-owned terminal gate. Harness finals
// are primary execution evidence; the lifecycle registry decides whether the
// corresponding containment boundary is empty, still cleaning, or retained as
// failed/escaped evidence. The detached timeout keeps cancellation from
// publishing a terminal fact ahead of cleanup.
func waitForSubprocessCleanup(ctx context.Context, req SubprocessRequest) (bool, string) {
	if req.SessionID == "" || req.LifecycleStateDir == "" {
		return false, ""
	}
	timeout := req.CleanupTimeout
	if timeout <= 0 {
		timeout = defaultSubprocessCleanupTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	registry := processlifecycle.NewFileRegistry(req.LifecycleStateDir)
	var lastDiagnostic string
	for {
		records, err := registry.RecordsForOperation(cleanupCtx, req.SessionID)
		if err == nil {
			switch len(records) {
			case 0:
				return false, ""
			case 1:
				record := records[0]
				if record.State == processlifecycle.StateCompleted {
					// Successful cleanup deletes its durable lease. A completed
					// record that is still present means the deletion/persistence
					// step failed and is therefore a cleanup failure, even though
					// the containment boundary was observed empty. Keep polling to
					// allow the ordinary update-then-delete transition to finish;
					// only the cleanup deadline makes retention terminal.
					lastDiagnostic = fmt.Sprintf("lifecycle record %s remains after completed cleanup", record.RecordID)
					break
				}
				if record.State == processlifecycle.StateCleanupFailed ||
					record.State == processlifecycle.StateRecoveryBlocked || len(record.EscapeEvidence) > 0 {
					return true, cleanupRecordDiagnostic(record)
				}
				lastDiagnostic = fmt.Sprintf("lifecycle record %s remains %s", record.RecordID, record.State)
			default:
				return true, fmt.Sprintf("multiple lifecycle records found for session %s", req.SessionID)
			}
		} else {
			lastDiagnostic = fmt.Sprintf("inspect lifecycle registry: %v", err)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-cleanupCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if lastDiagnostic == "" {
				lastDiagnostic = cleanupCtx.Err().Error()
			}
			return true, fmt.Sprintf("harness cleanup deadline reached: %s", lastDiagnostic)
		case <-timer.C:
		}
	}
}

func cleanupRecordDiagnostic(record processlifecycle.Record) string {
	detail := fmt.Sprintf("lifecycle record %s retained in state %s", record.RecordID, record.State)
	if n := len(record.EscapeEvidence); n > 0 {
		evidence := record.EscapeEvidence[n-1]
		if evidence.Detail != "" {
			detail += ": " + evidence.Detail
		} else if evidence.Kind != "" {
			detail += ": " + evidence.Kind
		}
	}
	return detail
}

func subprocessTemperature(v *float32) float32 {
	if v == nil {
		return 0
	}
	return *v
}

func subprocessSeed(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func adapterReasoning(value reasoning.Reasoning) string {
	policy, err := reasoning.ParseString(string(value))
	if err != nil {
		return string(value)
	}
	switch policy.Kind {
	case reasoning.KindUnset, reasoning.KindAuto, reasoning.KindOff:
		return ""
	case reasoning.KindTokens:
		if policy.Tokens == 0 {
			return ""
		}
		return string(policy.Value)
	case reasoning.KindNamed:
		return string(policy.Value)
	default:
		return string(value)
	}
}

func stampSubprocessFinalSessionLog(ev harnesses.Event, sessionLogPath string) harnesses.Event {
	if sessionLogPath == "" {
		return ev
	}
	var final harnesses.FinalData
	if err := json.Unmarshal(ev.Data, &final); err != nil {
		return ev
	}
	final.SessionLogPath = sessionLogPath
	raw, err := json.Marshal(final)
	if err != nil {
		return ev
	}
	ev.Data = raw
	return ev
}

func stampSubprocessFinalRouting(ev harnesses.Event, decision ExecuteRunnerDecision) harnesses.Event {
	var final harnesses.FinalData
	if err := json.Unmarshal(ev.Data, &final); err != nil {
		return ev
	}
	if final.RoutingActual == nil {
		final.RoutingActual = &harnesses.RoutingActual{}
	}
	// The adapter owns executing-surface failure evidence. The service owns the
	// resolved route identity and overwrites any adapter-supplied tuple so a
	// final cannot contradict the RouteDecision that actually dispatched it.
	final.RoutingActual.Harness = decision.Harness
	final.RoutingActual.Provider = decision.Provider
	final.RoutingActual.ServerInstance = decision.ServerInstance
	final.RoutingActual.Model = decision.Model
	raw, err := json.Marshal(final)
	if err != nil {
		return ev
	}
	ev.Data = raw
	return ev
}
