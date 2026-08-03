package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/processlifecycle"
)

// Fallback only: unpinned runs resolve through model discovery first.
const fallbackDefaultModel = "claude-sonnet-4-6"

// Default size for the per-Execute event channel buffer. Large enough that
// fast tool-call bursts (claude can emit dozens of blocks per turn) do not
// stall the parser, small enough to bound memory in pathological streams.
const defaultEventBuffer = 64

// Default idle/stream timeout when ExecuteRequest leaves IdleTimeout unset.
// Mirrors the wall-clock cap the DDx-side runner used historically; an
// explicit ExecuteRequest.Timeout still wins.
const defaultIdleTimeout = 0 // 0 = no idle cap; rely on Timeout / ctx.

// Runner is the subprocess-backed claude harness. It launches the claude
// CLI in stream-json mode, parses each line into harness Events, and emits
// a final Event when the subprocess exits. On ctx.Done(), the subprocess
// (and any forked children belonging to its process group) is signalled
// SIGTERM and reaped so PTY/tool children don't outlive the request.
type Runner struct {
	harnesses.PortableRuntimeRunnerState

	// Binary is the absolute path to the claude executable. When empty the
	// runner resolves "claude" via PATH at Execute time.
	Binary string

	// BaseArgs is prepended to the per-request argument list; callers use
	// it to pin a consistent invocation profile (e.g. ["--print", "-p",
	// "--output-format", "stream-json", "--verbose"]).
	BaseArgs []string

	// PromptMode controls how the prompt is delivered to claude:
	//   "stdin" (default) — prompt is piped on stdin
	//   "arg"             — prompt is appended as the final positional argument
	PromptMode string

	// EventBuffer overrides the per-Execute channel buffer size. Zero
	// selects defaultEventBuffer.
	EventBuffer int

	// DiscoveryCache overrides model/reasoning discovery evidence in tests.
	DiscoveryCache *harnesses.ModelDiscoveryCache

	// NativeMode routes Execute through the native Anthropic Messages API
	// (streaming HTTP via internal/provider/anthropic) instead of os/exec'ing
	// `claude --print`. In native mode the runner is metered
	// (actual_cash_spend) and reports IsSubscription=false — distinct from the
	// claude-tui flat-subscription surface.
	NativeMode bool

	// NativeProvider overrides the streaming provider used by the native path.
	// When nil, the runner builds the real metered Anthropic provider via
	// nativeFactory. Tests inject a fake here.
	NativeProvider NativeStreamingProvider

	// nativeFactory builds the native provider when NativeProvider is nil. When
	// nil the default Anthropic provider factory is used. Test-only seam.
	nativeFactory nativeProviderFactory

	// NativeTools is the tool set the native agentic loop may execute. The
	// subprocess path delegates tool execution to the claude CLI; the native
	// Messages API only emits tool_use requests, so the runner executes them
	// against this set and feeds back tool_result. Empty = no tools.
	NativeTools []agentcore.Tool

	// NativeAPIKey / NativeBaseURL configure the real metered Anthropic client
	// when NativeProvider is not injected.
	NativeAPIKey  string
	NativeBaseURL string

	// NativeMaxIterations bounds the native agentic loop. Zero selects
	// defaultNativeMaxIterations.
	NativeMaxIterations int

	// AuthUsabilityProbe is an optional offline auth check run before the
	// subprocess path starts. Nil uses anthropic.ProbeClaudeAuthUsability.
	// Tests inject fixed results; a non-empty Class aborts Execute without
	// launching the binary and emits a typed credential final event.
	AuthUsabilityProbe anthropic.ClaudeAuthUsabilityProbe
}

// BindPortableRuntime makes the manifest transport authoritative without
// consulting construction-time environment controls.
func (r *Runner) BindPortableRuntime(binding harnesses.PortableRuntimeRunnerBinding) error {
	if err := r.PortableRuntimeRunnerState.BindPortableRuntime(binding); err != nil {
		return err
	}
	r.NativeMode = binding.Structure().Transport == harnesses.PortableRuntimeTransportNative
	r.NativeAPIKey = ""
	r.NativeBaseURL = ""
	return nil
}

// PortableRuntimeStructure reports the transport selected on this actual
// runner instance without probing PATH or contacting Anthropic.
func (r *Runner) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	if binding, ok := r.PortableRuntimeBinding(); ok {
		return binding.Structure()
	}
	if r.NativeMode {
		return harnesses.PortableRuntimeStructure{
			Name:      "claude",
			Transport: harnesses.PortableRuntimeTransportNative,
			Mode:      harnesses.PortableRuntimeStructuralNonSubprocess,
		}
	}
	return harnesses.PortableRuntimeStructure{
		Name:      "claude",
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

// Info returns identity + capability metadata for this harness.
//
// Path is best-effort: the runner reports Binary if set, otherwise looks
// up "claude" on PATH. Available tracks whether the lookup succeeded so
// callers can show a useful error in `ddx agent list` without invoking
// HealthCheck synchronously.
func (r *Runner) Info() harnesses.HarnessInfo {
	info := harnesses.HarnessInfo{
		Name:                 "claude",
		Type:                 "subprocess",
		IsLocal:              false,
		IsSubscription:       true,
		AutoRoutingEligible:  true,
		ExactPinSupport:      true,
		DefaultModel:         harnesses.ResolveRunnerModel("claude", modelcatalog.SurfaceClaudeCode, "", fallbackDefaultModel).ResolvedModel,
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		SupportedReasoning:   []string{"low", "medium", "high", "xhigh", "max"},
		CostClass:            "medium",
	}
	if r.NativeMode {
		// Native path uses the Anthropic Messages API directly (metered,
		// actual_cash_spend) — there is no flat subscription and no claude
		// binary involved.
		info.Type = "native"
		info.IsSubscription = false
		info.Available = true
		return info
	}
	path := r.Binary
	if path == "" {
		if resolved, err := osexec.LookPath("claude"); err == nil {
			path = resolved
		}
	}
	if path != "" {
		info.Path = path
		info.Available = true
	} else {
		info.Error = "claude binary not found in PATH"
	}
	return info
}

// HealthCheck verifies the claude binary resolves on PATH (or at the
// configured Binary). It does NOT invoke the binary so it stays cheap and
// safe to call from request hot paths. A future extension can probe quota
// state via the cache layer.
func (r *Runner) HealthCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.NativeMode {
		// Native mode reaches the metered Anthropic Messages API over HTTP and
		// never os/exec's the claude CLI, so binary presence is irrelevant to
		// health. (Execute likewise branches on NativeMode before resolving the
		// binary.) Credential/provider reachability is validated lazily on the
		// first native turn, not here.
		return nil
	}
	path := r.Binary
	if path == "" {
		resolved, err := osexec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("claude binary not found: %w", err)
		}
		path = resolved
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat claude binary: %w", err)
	}
	if st.IsDir() {
		return fmt.Errorf("claude binary path is a directory: %s", path)
	}
	return nil
}

// Execute runs one resolved request through the claude CLI and emits
// stream-derived events on the returned channel. The channel is closed
// once a final event has been emitted. PTY/orphan children are reaped on
// ctx.Done(). If the CLI rejects stream-json flags (older build), the
// runner falls back to a buffered legacy invocation and emits a single
// text_delta + final from the buffered output.
func (r *Runner) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	bufSize := r.EventBuffer
	if bufSize <= 0 {
		bufSize = defaultEventBuffer
	}

	// Channel returned to the caller; the goroutine below owns closing it.
	out := make(chan harnesses.Event, bufSize)

	if r.NativeMode {
		// Native path does not os/exec the claude binary at all.
		go r.run(ctx, "", req, out)
		return out, nil
	}

	binary := r.Binary
	if _, bound := r.PortableRuntimeBinding(); !bound && binary == "" {
		resolved, err := osexec.LookPath("claude")
		if err != nil {
			return nil, fmt.Errorf("claude binary not found: %w", err)
		}
		binary = resolved
	}

	if usability := r.probeAuthUsability(); usability.Class != "" {
		go r.emitAuthPreflightFailure(ctx, out, usability)
		return out, nil
	}

	go r.run(ctx, binary, req, out)
	return out, nil
}

// probeAuthUsability runs the offline Claude auth probe. NativeMode skips it
// (HTTP path validates credentials on first turn). Empty Class means usable.
func (r *Runner) probeAuthUsability() anthropic.AuthUsability {
	if r == nil || r.NativeMode {
		return anthropic.AuthUsability{}
	}
	probe := r.AuthUsabilityProbe
	if probe == nil {
		probe = anthropic.ProbeClaudeAuthUsability
	}
	return probe()
}

// emitAuthPreflightFailure emits a single failed final event with typed
// credential failure class without launching the claude subprocess.
func (r *Runner) emitAuthPreflightFailure(_ context.Context, out chan<- harnesses.Event, usability anthropic.AuthUsability) {
	defer close(out)
	diagnostic := strings.TrimSpace(usability.Diagnostic)
	if diagnostic == "" {
		diagnostic = usability.Class
	}
	failureClass, classified := anthropic.ClassifyClaudeRouteFailure(diagnostic)
	if failureClass == anthropic.FailureClassUnknown {
		// credential_missing is not a ClassifyClaudeRouteFailure class; keep
		// the probe class for routing while still attaching remediation when
		// the diagnostic already looks like a credential problem.
		failureClass = usability.Class
		classified = diagnostic
		if failureClass == anthropic.AuthUsabilityInvalid || failureClass == anthropic.FailureClassCredentialInvalid {
			_, classified = anthropic.ClassifyClaudeRouteFailure(diagnostic + "\n" + anthropic.CredentialRemediationGuidance)
			failureClass = anthropic.FailureClassCredentialInvalid
		}
	}
	final := harnesses.FinalData{
		Status:   "failed",
		ExitCode: 1,
		Error:    classified,
		RoutingActual: &harnesses.RoutingActual{
			Harness:      "claude",
			FailureClass: failureClass,
		},
	}
	finalRaw, err := json.Marshal(final)
	if err != nil {
		finalRaw = []byte(`{"status":"failed","error":"auth preflight failed"}`)
	}
	out <- harnesses.Event{
		Type: "final",
		Time: time.Now().UTC(),
		Data: finalRaw,
	}
}

// run is the per-Execute goroutine: starts claude, streams events, and
// guarantees a final event + channel close on every termination path.
func (r *Runner) run(ctx context.Context, binary string, req harnesses.ExecuteRequest, out chan<- harnesses.Event) {
	defer close(out)

	start := time.Now()
	var seq int64

	var (
		agg      *streamAggregate
		exitCode int
		stderr   string
		runErr   error
		status   string
	)

	if r.NativeMode {
		agg, exitCode, stderr, runErr, status = r.runNative(ctx, req, out, &seq)
	} else {
		// First attempt: stream-json invocation.
		agg, exitCode, stderr, runErr, status = r.runStreaming(ctx, binary, req, out, &seq)

		// Fallback path: claude rejected the stream-json flags. Retry with the
		// legacy buffered --print/-p/--output-format=json invocation. We surface
		// the legacy output as a single text_delta so consumers still receive
		// the model's final text.
		if status == "failed" && exitCode == 2 && claudeStreamArgsUnsupported(stderr) {
			// Reset the aggregate so legacy output drives final event.
			agg, exitCode, stderr, runErr, status = r.runLegacy(ctx, binary, req, out, &seq)
		}
	}

	// Emit the final event regardless of outcome so downstream consumers
	// always see a terminator. Errors during emit are non-fatal — the
	// channel close still signals end-of-stream.
	final := harnesses.FinalData{
		Status:          status,
		ExitCode:        exitCode,
		DurationMS:      time.Since(start).Milliseconds(),
		FinalCostSource: harnesses.CostSourceUnknown,
	}
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "claude", req.Reasoning)
	if harnesses.ShouldEmitRunnerReasoningResolution(reasoningResolution) {
		final.Reasoning = &reasoningResolution
	}
	quotaMessage := claudeQuotaMessage(stderr, runErr, agg)
	if quotaMessage != "" {
		markClaudeQuotaExhaustedFromMessage(quotaMessage, time.Now())
	}
	if status != "success" {
		final.Error = claudeFinalError(status, runErr, stderr, quotaMessage)
	}
	if status == "failed" || status == "iteration_limit" {
		failureEvidence := claudeFailureEvidence(runErr, stderr, quotaMessage)
		if strings.TrimSpace(failureEvidence) != "" {
			// ClassifyClaudeRouteFailure returns sanitized diagnostic with
			// credential remediation; prefer that as Error so operators see
			// re-auth guidance instead of only "exit status 1".
			failureClass, classified := anthropic.ClassifyClaudeRouteFailure(failureEvidence)
			if failureClass == anthropic.FailureClassCredentialInvalid && classified != "" {
				final.Error = classified
			}
			final.RoutingActual = &harnesses.RoutingActual{
				Harness:      "claude",
				FailureClass: failureClass,
			}
		}
	}
	if agg != nil {
		final.FinalText = agg.FinalText
		final.Usage, final.Warnings = harnesses.ResolveFinalUsage(agg.UsageSources)
		final.FinalCostUSD = agg.FinalCostUSD
		final.FinalCostSource = agg.CostSource
		final.CostUSD = agg.CostUSD
	}

	finalRaw, err := json.Marshal(final)
	if err != nil {
		// Defensive: marshal can only fail on programmer error here.
		finalRaw = []byte(`{"status":"failed","error":"marshal final event"}`)
	}
	ev := harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: seq,
		Time:     time.Now().UTC(),
		Metadata: req.Metadata,
		Data:     finalRaw,
	}
	select {
	case out <- ev:
	case <-time.After(time.Second):
		// Caller has stopped consuming; drop and close.
	}
}

func claudeFinalError(status string, runErr error, stderr, quotaMessage string) string {
	if status == "success" {
		return ""
	}
	if diagnostic := strings.TrimSpace(quotaMessage); diagnostic != "" {
		return anthropic.SanitizeClaudeDiagnostic("claude quota exhausted: " + diagnostic)
	}
	if runErr != nil {
		diagnostic := runErr.Error()
		// Failed execution retains the process error for compatibility and adds
		// sanitized stderr so an opaque exit status does not erase the executing
		// surface's classification evidence. Cancellation and timeout keep their
		// legacy process-error text without attaching route-failure evidence.
		if (status == "failed" || status == "iteration_limit") && strings.TrimSpace(stderr) != "" {
			diagnostic += "\n" + stderr
		}
		return anthropic.SanitizeClaudeDiagnostic(diagnostic)
	}
	return anthropic.SanitizeClaudeDiagnostic(stderr)
}

func claudeFailureEvidence(runErr error, stderr, quotaMessage string) string {
	parts := make([]string, 0, 3)
	if diagnostic := strings.TrimSpace(quotaMessage); diagnostic != "" {
		parts = append(parts, "claude quota exhausted: "+diagnostic)
	}
	if runErr != nil {
		parts = append(parts, runErr.Error())
	}
	if diagnostic := strings.TrimSpace(stderr); diagnostic != "" {
		parts = append(parts, diagnostic)
	}
	return strings.Join(parts, "\n")
}

func claudeQuotaMessage(stderr string, runErr error, agg *streamAggregate) string {
	for _, candidate := range []string{
		stderr,
		func() string {
			if agg == nil {
				return ""
			}
			return agg.FinalText
		}(),
		func() string {
			if runErr == nil {
				return ""
			}
			return runErr.Error()
		}(),
	} {
		if isClaudeQuotaExhaustedMessage(candidate) {
			return candidate
		}
	}
	return ""
}

// runStreaming drives the stream-json path: launches claude with the
// configured BaseArgs, pipes stdout through the parser, and returns the
// aggregated stream state plus exit metadata.
func (r *Runner) runStreaming(ctx context.Context, binary string, req harnesses.ExecuteRequest, out chan<- harnesses.Event, seq *int64) (agg *streamAggregate, exitCode int, stderr string, runErr error, status string) {
	base := r.BaseArgs
	if base == nil {
		base = []string{"--print", "-p", "--verbose", "--output-format", "stream-json"}
	}
	args := r.buildArgs(base, req)

	promptMode := r.PromptMode
	if promptMode == "" {
		promptMode = "arg"
	}
	if promptMode == "arg" && req.Prompt != "" {
		args = append(args, req.Prompt)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := harnesses.HarnessBatchCommand(binary, args...)
	var portableLaunch *processlifecycle.PortableLaunchAttachment
	if binding, bound := r.PortableRuntimeBinding(); bound {
		boundCmd, attachment, buildErr := harnesses.BuildPortableRuntimeBatchCommand(binding, base, args[len(base):])
		if buildErr != nil {
			return nil, -1, "", buildErr, "failed"
		}
		cmd, portableLaunch = boundCmd, attachment
	}
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if promptMode != "arg" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}
	outputPipes, err := harnesses.PrepareHarnessOutputPipes(cmd)
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer outputPipes.Close()
	batch, err := processlifecycle.StartBatch(runCtx, cmd, processlifecycle.BatchOptions{
		Harness: "claude", OperationID: req.SessionID, SessionLogDir: req.SessionLogDir,
		LifecycleStateDir: req.LifecycleStateDir, CleanupTimeout: req.CleanupTimeout,
		PortableLaunch: portableLaunch,
	})
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer batch.Stop()
	if err := outputPipes.ReleaseWriters(); err != nil {
		return nil, -1, "", err, "failed"
	}

	progressLog, _ := harnesses.OpenProgressLog(req.SessionLogDir, req.SessionID, "claude")
	if progressLog != nil {
		defer progressLog.Close()
	}
	modelResolution := harnesses.ResolveRunnerModelWithCache(r.DiscoveryCache, "claude", modelcatalog.SurfaceClaudeCode, req.Model, fallbackDefaultModel)
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "claude", req.Reasoning)
	if harnesses.ShouldEmitRunnerDefaultResolution(modelResolution) {
		ev := harnesses.RunnerDefaultResolutionEvent(modelResolution, req.Metadata, seq)
		harnesses.WriteProgressEvent(progressLog, ev)
		select {
		case out <- ev:
		case <-ctx.Done():
			return nil, -1, "", ctx.Err(), "cancelled"
		}
	}
	if harnesses.ShouldEmitRunnerReasoningResolution(reasoningResolution) {
		harnesses.LogRunnerReasoningWarning(reasoningResolution)
		ev := harnesses.RunnerReasoningResolutionEvent(reasoningResolution, req.Metadata, seq)
		harnesses.WriteProgressEvent(progressLog, ev)
		select {
		case out <- ev:
		case <-ctx.Done():
			return nil, -1, "", ctx.Err(), "cancelled"
		}
	}

	// Tee stdout into the parser and (optionally) into the progress log.
	parserReader, parserWriter := io.Pipe()
	parseDone := make(chan struct{})
	var parseAgg *streamAggregate
	var parseErr error
	// Wrap out so we can also mirror events to disk as JSONL. mirrorDone
	// signals that the mirror goroutine has fully drained — must be awaited
	// before run() lets defer close(out) fire, otherwise we get a close vs.
	// chansend race when the mirror is mid-send to dst.
	mirrored, mirrorDone := harnesses.MirrorEvents(out, progressLog, ctx)
	go func() {
		defer close(parseDone)
		defer close(mirrored) // releases the mirror goroutine's range loop
		parseAgg, parseErr = parseClaudeStream(runCtx, parserReader, mirrored, req.Metadata, seq)
	}()

	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		_, _ = io.Copy(parserWriter, outputPipes.Stdout)
		_ = parserWriter.Close()
	}()

	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stringBuilderWriter{&stderrBuf}, outputPipes.Stderr)
	}()

	// Wall-clock / idle timeout: if Timeout is set, cancel after it fires.
	var timedOut bool
	if req.Timeout > 0 {
		stop := make(chan struct{})
		go func() {
			select {
			case <-stop:
			case <-time.After(req.Timeout):
				timedOut = true
				cancel()
			}
		}()
		defer close(stop)
	}

	// Wait for stdout to be fully read; context cancellation also wakes us up.
	// On cancellation we must enter shared lifecycle cleanup immediately;
	// otherwise the stderr/stdout io.Copy goroutines
	// (and thus the <-stderrDone / <-parseDone waits below) block until the child
	// closes its pipes on its own, which a long-running turn never does. Killing
	// the group here closes the pipes and lets the drain goroutines complete so
	// Execute terminates promptly with a cancelled/timed_out status.
	select {
	case <-runCtx.Done():
		_ = batch.Stop()
	case <-stdoutDone:
	}

	<-stderrDone
	<-parseDone
	<-mirrorDone
	runErr = batch.Wait()
	stderr = stderrBuf.String()

	// Classify exit.
	switch {
	case timedOut:
		return parseAgg, -1, stderr, context.DeadlineExceeded, "timed_out"
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
		return parseAgg, -1, stderr, ctx.Err(), "cancelled"
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return parseAgg, -1, stderr, ctx.Err(), "timed_out"
	case runErr != nil:
		ec := -1
		var exitErr *osexec.ExitError
		if errors.As(runErr, &exitErr) {
			ec = exitErr.ExitCode()
		}
		return parseAgg, ec, stderr, runErr, "failed"
	}
	if parseErr != nil && !errors.Is(parseErr, context.Canceled) {
		return parseAgg, 0, stderr, parseErr, "failed"
	}
	if parseAgg != nil && parseAgg.IsError {
		return parseAgg, 0, stderr, errors.New("claude reported is_error=true"), "failed"
	}
	return parseAgg, 0, stderr, nil, "success"
}

// runLegacy invokes the legacy buffered claude path used when the CLI
// rejected stream-json flags. It surfaces the captured stdout as a single
// text_delta event so callers still receive the model's text.
func (r *Runner) runLegacy(ctx context.Context, binary string, req harnesses.ExecuteRequest, out chan<- harnesses.Event, seq *int64) (*streamAggregate, int, string, error, string) {
	base := []string{"--print", "-p", "--output-format", "json"}
	args := r.buildArgs(base, req)
	promptMode := r.PromptMode
	if promptMode == "" {
		promptMode = "arg"
	}
	if promptMode == "arg" && req.Prompt != "" {
		args = append(args, req.Prompt)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := harnesses.HarnessBatchCommand(binary, args...)
	var portableLaunch *processlifecycle.PortableLaunchAttachment
	if binding, bound := r.PortableRuntimeBinding(); bound {
		boundCmd, attachment, buildErr := harnesses.BuildPortableRuntimeBatchCommand(binding, base, args[len(base):])
		if buildErr != nil {
			return nil, -1, "", buildErr, "failed"
		}
		cmd, portableLaunch = boundCmd, attachment
	}
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if promptMode != "arg" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}
	outputPipes, err := harnesses.PrepareHarnessOutputPipes(cmd)
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer outputPipes.Close()

	batch, err := processlifecycle.StartBatch(runCtx, cmd, processlifecycle.BatchOptions{
		Harness: "claude", OperationID: req.SessionID, SessionLogDir: req.SessionLogDir,
		LifecycleStateDir: req.LifecycleStateDir, CleanupTimeout: req.CleanupTimeout,
		PortableLaunch: portableLaunch,
	})
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer batch.Stop()
	if err := outputPipes.ReleaseWriters(); err != nil {
		return nil, -1, "", err, "failed"
	}

	stdoutBytes, _ := io.ReadAll(outputPipes.Stdout)
	stderrBytes, _ := io.ReadAll(outputPipes.Stderr)
	runErr := batch.Wait()

	stderrBytesStr := string(stderrBytes)
	var exitErr *osexec.ExitError
	if errors.As(runErr, &exitErr) {
		stderrBytesStr = string(exitErr.Stderr)
	}

	if runErr != nil {
		ec := -1
		if exitErr != nil {
			ec = exitErr.ExitCode()
		}
		return nil, ec, stderrBytesStr, runErr, "failed"
	}

	text := strings.TrimSpace(string(stdoutBytes))
	if text != "" {
		raw, _ := json.Marshal(harnesses.TextDeltaData{Text: text})
		ev := harnesses.Event{
			Type:     harnesses.EventTypeTextDelta,
			Sequence: *seq,
			Time:     time.Now().UTC(),
			Metadata: req.Metadata,
			Data:     raw,
		}
		*seq++
		select {
		case out <- ev:
		case <-ctx.Done():
			return nil, 0, stderrBytesStr, ctx.Err(), "cancelled"
		}
	}
	return &streamAggregate{FinalText: text, CostSource: harnesses.CostSourceUnknown}, 0, stderrBytesStr, nil, "success"
}

func (r *Runner) buildArgs(base []string, req harnesses.ExecuteRequest) []string {
	args := append([]string{}, base...)
	modelResolution := harnesses.ResolveRunnerModelWithCache(r.DiscoveryCache, "claude", modelcatalog.SurfaceClaudeCode, req.Model, fallbackDefaultModel)
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "claude", req.Reasoning)
	switch req.Permissions {
	case "supervised":
		args = append(args, "--permission-mode", "default")
	case "unrestricted":
		args = append(args, "--permission-mode", "bypassPermissions", "--dangerously-skip-permissions")
	}
	if modelResolution.ResolvedModel != "" {
		args = append(args, "--model", modelResolution.ResolvedModel)
	}
	if value := reasoningResolution.ResolvedReasoning; value != "" {
		args = append(args, "--effort", value)
	}
	return args
}

// stringBuilderWriter adapts *strings.Builder to io.Writer.
type stringBuilderWriter struct {
	sb *strings.Builder
}

func (w *stringBuilderWriter) Write(p []byte) (int, error) {
	return w.sb.Write(p)
}
