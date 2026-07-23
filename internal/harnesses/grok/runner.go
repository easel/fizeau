package grok

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

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/processlifecycle"
)

const defaultEventBuffer = 64

// Fallback only: unpinned runs resolve through model discovery first.
const fallbackDefaultModel = "grok-4.5"

// Runner is the subprocess-backed grok harness. It launches xAI's Grok Build
// CLI in headless streaming-json mode, parses each NDJSON line into harness
// Events, and emits a final Event when the subprocess exits.
type Runner struct {
	harnesses.PortableRuntimeRunnerState

	// Binary is the absolute path to the grok executable. When empty the
	// runner resolves "grok" via PATH at Execute time.
	Binary string

	// BaseArgs is prepended to the per-request argument list.
	// Grok default: ["--output-format", "streaming-json"]
	BaseArgs []string

	// PromptMode controls how the prompt is delivered. Grok only supports
	// "arg": the prompt is passed as the value of the -p/--single flag,
	// appended after all other flags. ("stdin" falls back to "arg".)
	PromptMode string

	// EventBuffer overrides the per-Execute channel buffer size.
	EventBuffer int

	// DiscoveryCache overrides model/reasoning discovery evidence in tests.
	DiscoveryCache *harnesses.ModelDiscoveryCache
}

// PortableRuntimeStructure describes this actual runner without probing PATH.
func (r *Runner) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      "grok",
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

// Info returns identity + capability metadata for this harness.
func (r *Runner) Info() harnesses.HarnessInfo {
	info := harnesses.HarnessInfo{
		Name:                 "grok",
		Type:                 "subprocess",
		IsLocal:              false,
		IsSubscription:       true,
		AutoRoutingEligible:  true,
		ExactPinSupport:      true,
		DefaultModel:         harnesses.ResolveRunnerModel("grok", modelcatalog.SurfaceGrok, "", fallbackDefaultModel).ResolvedModel,
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		SupportedReasoning:   []string{"low", "medium", "high", "xhigh", "max"},
		CostClass:            "medium",
	}
	path := r.Binary
	if path == "" {
		if resolved, err := osexec.LookPath("grok"); err == nil {
			path = resolved
		}
	}
	if path != "" {
		info.Path = path
		info.Available = true
	} else {
		info.Error = "grok binary not found in PATH"
	}
	return info
}

// HealthCheck verifies the grok binary is present.
func (r *Runner) HealthCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := r.Binary
	if path == "" {
		resolved, err := osexec.LookPath("grok")
		if err != nil {
			return fmt.Errorf("grok binary not found: %w", err)
		}
		path = resolved
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat grok binary: %w", err)
	}
	if st.IsDir() {
		return fmt.Errorf("grok binary path is a directory: %s", path)
	}
	return nil
}

// Execute runs one resolved request through the grok CLI and emits
// NDJSON-derived events on the returned channel.
func (r *Runner) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	binary := r.Binary
	if _, bound := r.PortableRuntimeBinding(); !bound && binary == "" {
		resolved, err := osexec.LookPath("grok")
		if err != nil {
			return nil, fmt.Errorf("grok binary not found: %w", err)
		}
		binary = resolved
	}

	bufSize := r.EventBuffer
	if bufSize <= 0 {
		bufSize = defaultEventBuffer
	}

	out := make(chan harnesses.Event, bufSize)
	go r.run(ctx, binary, req, out)
	return out, nil
}

func (r *Runner) run(ctx context.Context, binary string, req harnesses.ExecuteRequest, out chan<- harnesses.Event) {
	defer close(out)

	start := time.Now()
	var seq int64

	agg, exitCode, stderr, runErr, status := r.runStreaming(ctx, binary, req, out, &seq)

	final := harnesses.FinalData{
		Status:          status,
		ExitCode:        exitCode,
		DurationMS:      time.Since(start).Milliseconds(),
		FinalCostSource: harnesses.CostSourceUnknown,
	}
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "grok", req.Reasoning)
	if harnesses.ShouldEmitRunnerReasoningResolution(reasoningResolution) {
		final.Reasoning = &reasoningResolution
	}
	if agg != nil && agg.IsError && status == "success" {
		status = "failed"
		final.Status = status
	}
	switch {
	case runErr != nil && status != "success":
		final.Error = runErr.Error()
	case agg != nil && agg.IsError && agg.ErrorMessage != "":
		final.Error = agg.ErrorMessage
	case stderr != "" && status != "success":
		final.Error = trimErrorBlob(stderr)
	}
	if agg != nil {
		final.FinalText = agg.FinalText()
		final.Usage, final.Warnings = harnesses.ResolveFinalUsage(agg.UsageSources)
		final.FinalCostUSD = agg.FinalCostUSD
		final.FinalCostSource = agg.CostSource
		final.CostUSD = agg.CostUSD
	}

	finalRaw, err := json.Marshal(final)
	if err != nil {
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
	}
}

func (r *Runner) runStreaming(ctx context.Context, binary string, req harnesses.ExecuteRequest, out chan<- harnesses.Event, seq *int64) (agg *streamAggregate, exitCode int, stderr string, runErr error, status string) {
	base := r.BaseArgs
	if base == nil {
		base = []string{"--output-format", "streaming-json"}
	}
	args := append([]string{}, base...)
	modelResolution := harnesses.ResolveRunnerModelWithCache(r.DiscoveryCache, "grok", modelcatalog.SurfaceGrok, req.Model, fallbackDefaultModel)
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "grok", req.Reasoning)

	// Permission args: unrestricted auto-approves all tool executions;
	// supervised pins the default interactive permission mode.
	switch req.Permissions {
	case "unrestricted":
		args = append(args, "--always-approve")
	case "supervised":
		args = append(args, "--permission-mode", "default")
	}

	// WorkDir flag: --cwd <dir>
	if req.WorkDir != "" {
		args = append(args, "--cwd", req.WorkDir)
	}

	// Model flag: -m <model>
	if modelResolution.ResolvedModel != "" {
		args = append(args, "-m", modelResolution.ResolvedModel)
	}

	// Reasoning flag: --reasoning-effort <level>
	if value := reasoningResolution.ResolvedReasoning; value != "" {
		args = append(args, "--reasoning-effort", value)
	}

	// Prompt: value of -p/--single, appended last. Grok has no stdin prompt
	// mode; the -p flag requires a value (unlike claude's bare -p toggle).
	if req.Prompt != "" {
		args = append(args, "-p", req.Prompt)
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
	outputPipes, err := harnesses.PrepareHarnessOutputPipes(cmd)
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer outputPipes.Close()
	batch, err := processlifecycle.StartBatch(runCtx, cmd, processlifecycle.BatchOptions{
		Harness: "grok", OperationID: req.SessionID, SessionLogDir: req.SessionLogDir,
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

	progressLog, _ := harnesses.OpenProgressLog(req.SessionLogDir, req.SessionID, "grok")
	if progressLog != nil {
		defer progressLog.Close()
	}
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

	parserReader, parserWriter := io.Pipe()
	parseDone := make(chan struct{})
	var parseAgg *streamAggregate
	var parseErr error
	go func() {
		defer close(parseDone)
		mirrored, mirrorDone := harnesses.MirrorEvents(out, progressLog, ctx)
		defer func() {
			close(mirrored)
			<-mirrorDone
		}()
		parseAgg, parseErr = parseGrokStream(runCtx, parserReader, mirrored, req.Metadata, seq)
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
	// Either way, the defer ensures the process group is killed on exit.
	select {
	case <-runCtx.Done():
		_ = batch.Stop()
	case <-stdoutDone:
	}

	<-stderrDone
	<-parseDone
	runErr = batch.Wait()
	stderr = stderrBuf.String()

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
	return parseAgg, 0, stderr, nil, "success"
}

type stringBuilderWriter struct {
	sb *strings.Builder
}

func (w *stringBuilderWriter) Write(p []byte) (int, error) {
	return w.sb.Write(p)
}

func trimErrorBlob(s string) string {
	const max = 2048
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "...(truncated)"
	}
	return s
}
