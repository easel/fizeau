package gemini

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
	"github.com/easel/fizeau/internal/processlifecycle"
)

const defaultEventBuffer = 64

// Runner is the subprocess-backed gemini harness. It launches gemini in
// headless mode (-p/--prompt), buffers stream-json stdout, and emits
// text_delta + final events after the process exits.
//
// When the output is valid JSON with a stats.models token block, usage is
// extracted per the DDx ExtractUsage("gemini", ...) shape.
type Runner struct {
	// Binary is the absolute path to the gemini executable. When empty the
	// runner resolves "gemini" via PATH at Execute time.
	Binary string

	// BaseArgs is prepended to the per-request argument list.
	// Gemini default: ["--output-format", "stream-json"].
	BaseArgs []string

	// PromptMode controls how the prompt is delivered.
	// "arg" (default) sends "-p <prompt>"; "stdin" sends "-p ''" and writes
	// the prompt to stdin. Gemini requires -p/--prompt for headless mode.
	PromptMode string

	// EventBuffer overrides the per-Execute channel buffer size.
	EventBuffer int
}

// PortableRuntimeStructure describes this actual runner without probing PATH.
func (r *Runner) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      "gemini",
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

// Info returns identity + capability metadata for this harness.
func (r *Runner) Info() harnesses.HarnessInfo {
	info := harnesses.HarnessInfo{
		Name:                 "gemini",
		Type:                 "subprocess",
		IsLocal:              false,
		IsSubscription:       true,
		AutoRoutingEligible:  false,
		ExactPinSupport:      true,
		DefaultModel:         "gemini-2.5-flash",
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		SupportedReasoning:   nil,
		CostClass:            "medium",
	}
	path := r.Binary
	if path == "" {
		if resolved, err := osexec.LookPath("gemini"); err == nil {
			path = resolved
		}
	}
	if path != "" {
		info.Path = path
		info.Available = true
	} else {
		info.Error = "gemini binary not found in PATH"
	}
	return info
}

// HealthCheck verifies the gemini binary is present.
func (r *Runner) HealthCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := r.Binary
	if path == "" {
		resolved, err := osexec.LookPath("gemini")
		if err != nil {
			return fmt.Errorf("gemini binary not found: %w", err)
		}
		path = resolved
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat gemini binary: %w", err)
	}
	if st.IsDir() {
		return fmt.Errorf("gemini binary path is a directory: %s", path)
	}
	return nil
}

// Execute runs one resolved request through the gemini CLI and emits events
// on the returned channel. Since gemini has no stream-json mode, events are
// emitted after the process exits (emit-on-EOF pattern).
func (r *Runner) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	base := r.BaseArgs
	if base == nil {
		base = []string{"--output-format", "stream-json"}
	}
	if err := validateGeminiPortableLaterArguments(base); err != nil {
		return nil, err
	}
	if err := inspectGeminiPortableProjectSources(req.WorkDir); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, geminiPortableRuntimeError("generated-home source is unavailable")
	}
	if err := inspectGeminiPortableUserConfiguration(home); err != nil {
		return nil, err
	}
	if err := inspectGeminiPortableSystemSources(geminiPortableDefaultSystemSources()); err != nil {
		return nil, err
	}
	binary := r.Binary
	if binary == "" {
		resolved, err := osexec.LookPath("gemini")
		if err != nil {
			return nil, fmt.Errorf("gemini binary not found: %w", err)
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

	agg, exitCode, stderr, runErr, status := r.runBuffered(ctx, binary, req, out, &seq)

	final := harnesses.FinalData{
		Status:          status,
		ExitCode:        exitCode,
		DurationMS:      time.Since(start).Milliseconds(),
		FinalCostSource: harnesses.CostSourceUnknown,
	}
	if runErr != nil && status != "success" {
		final.Error = runErr.Error()
	} else if stderr != "" && status != "success" {
		final.Error = trimErrorBlob(stderr)
	}
	if agg != nil {
		final.FinalText = agg.FinalText
		if agg.HasUsage {
			final.Usage = &harnesses.FinalUsage{
				InputTokens:  harnesses.IntPtr(agg.InputTokens),
				OutputTokens: harnesses.IntPtr(agg.OutputTokens),
				Source:       harnesses.UsageSourceNativeStream,
				Fresh:        harnesses.BoolPtr(true),
			}
			if agg.TotalTokens > 0 {
				final.Usage.TotalTokens = harnesses.IntPtr(agg.TotalTokens)
			} else {
				final.Usage.TotalTokens = harnesses.IntPtr(agg.InputTokens + agg.OutputTokens)
			}
			if agg.CacheTokens > 0 {
				final.Usage.CacheTokens = harnesses.IntPtr(agg.CacheTokens)
			}
		}
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

func (r *Runner) runBuffered(ctx context.Context, binary string, req harnesses.ExecuteRequest, out chan<- harnesses.Event, seq *int64) (agg *streamAggregate, exitCode int, stderr string, runErr error, status string) {
	base := r.BaseArgs
	if base == nil {
		base = []string{"--output-format", "stream-json"}
	}

	promptMode := r.PromptMode
	if promptMode == "" {
		promptMode = "arg"
	}
	args, err := geminiPortableArguments(base, req, promptMode)
	if err != nil {
		return nil, -1, "", err, "failed"
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := harnesses.HarnessBatchCommand(binary, args...)
	cmd.Env = geminiPortableRunnerEnvironment(os.Environ())
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if promptMode == "stdin" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}
	outputPipes, err := harnesses.PrepareHarnessOutputPipes(cmd)
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer outputPipes.Close()
	batch, err := processlifecycle.StartBatch(runCtx, cmd, processlifecycle.BatchOptions{
		Harness: "gemini", OperationID: req.SessionID, SessionLogDir: req.SessionLogDir,
		LifecycleStateDir: req.LifecycleStateDir, CleanupTimeout: req.CleanupTimeout,
	})
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer batch.Stop()
	if err := outputPipes.ReleaseWriters(); err != nil {
		return nil, -1, "", err, "failed"
	}

	progressLog, _ := harnesses.OpenProgressLog(req.SessionLogDir, req.SessionID, "gemini")
	if progressLog != nil {
		defer progressLog.Close()
	}

	// Buffer stdout; emit after process exits (no streaming parser for gemini).
	var stdoutBytes []byte
	stdoutReady := make(chan struct{})
	go func() {
		defer close(stdoutReady)
		stdoutBytes, _ = io.ReadAll(outputPipes.Stdout)
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
	// Either way, the defer ensures process group is killed on function exit.
	select {
	case <-runCtx.Done():
		_ = batch.Stop()
	case <-stdoutReady:
	}

	<-stderrDone
	runErr = batch.Wait()
	stderr = stderrBuf.String()

	switch {
	case timedOut:
		return nil, -1, stderr, context.DeadlineExceeded, "timed_out"
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
		return nil, -1, stderr, ctx.Err(), "cancelled"
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, -1, stderr, ctx.Err(), "timed_out"
	case runErr != nil:
		ec := -1
		var exitErr *osexec.ExitError
		if errors.As(runErr, &exitErr) {
			ec = exitErr.ExitCode()
		}
		return nil, ec, stderr, runErr, "failed"
	}

	// Parse buffered output and emit events.
	output := strings.TrimSpace(string(stdoutBytes))
	parseAgg, parseErr := emitGeminiOutput(ctx, output, out, req.Metadata, seq, progressLog)
	if parseErr != nil && !errors.Is(parseErr, context.Canceled) {
		return parseAgg, 0, stderr, parseErr, "failed"
	}
	return parseAgg, 0, stderr, nil, "success"
}

// emitGeminiOutput parses buffered gemini output, emits a text_delta, and
// extracts token usage from the JSON stats block if present.
func emitGeminiOutput(ctx context.Context, output string, out chan<- harnesses.Event, metadata map[string]string, seq *int64, progressLog *os.File) (*streamAggregate, error) {
	agg := &streamAggregate{CostSource: harnesses.CostSourceUnknown}
	if output == "" {
		return agg, nil
	}

	if msg := geminiStreamError(output); msg != "" {
		return agg, errors.New("gemini error: " + msg)
	}

	if parsed, ok := parseGeminiStreamOutput(output); ok {
		agg = parsed
		if agg.FinalText == "" {
			return agg, nil
		}
	} else {
		// Legacy/fallback: extract usage from a trailing stats block and emit
		// the raw text exactly as the CLI returned it.
		agg = parseGeminiUsage(output)
	}

	raw, err := json.Marshal(harnesses.TextDeltaData{Text: agg.FinalText})
	if err != nil {
		return agg, err
	}
	ev := harnesses.Event{
		Type:     harnesses.EventTypeTextDelta,
		Sequence: *seq,
		Time:     time.Now().UTC(),
		Metadata: metadata,
		Data:     raw,
	}
	*seq++

	harnesses.WriteProgressEvent(progressLog, ev)

	select {
	case out <- ev:
	case <-ctx.Done():
		return agg, ctx.Err()
	}
	return agg, nil
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
