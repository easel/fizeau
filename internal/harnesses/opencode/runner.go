package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/processlifecycle"
)

const defaultEventBuffer = 64

// Runner is the subprocess-backed opencode harness. It launches opencode in
// run --format json mode, parses the JSON output into harness Events, and
// emits a final Event when the subprocess exits.
//
// opencode run auto-approves all tool permissions; no extra flags are needed
// for any permission level.
type Runner struct {
	// Binary is the absolute path to the opencode executable. When empty the
	// runner resolves "opencode" via PATH at Execute time.
	Binary string

	// BaseArgs is prepended to the per-request argument list.
	// opencode default: ["run", "--format", "json"]
	BaseArgs []string

	// PromptMode controls how the prompt is delivered:
	//   "arg" (default) — prompt is appended as the final positional argument
	//   "stdin"         — prompt is piped on stdin
	PromptMode string

	// EventBuffer overrides the per-Execute channel buffer size.
	EventBuffer int

	// DiscoveryCache overrides model/reasoning discovery evidence in tests.
	DiscoveryCache *harnesses.ModelDiscoveryCache
}

// Info returns identity + capability metadata for this harness.
func (r *Runner) Info() harnesses.HarnessInfo {
	info := harnesses.HarnessInfo{
		Name:                 "opencode",
		Type:                 "subprocess",
		IsLocal:              false,
		IsSubscription:       false,
		ExactPinSupport:      true,
		DefaultModel:         "opencode/gpt-5.4",
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		SupportedReasoning:   []string{"minimal", "low", "medium", "high", "max"},
		CostClass:            "medium",
	}
	path := r.Binary
	if path == "" {
		if resolved, err := osexec.LookPath("opencode"); err == nil {
			path = resolved
		}
	}
	if path != "" {
		info.Path = path
		info.Available = true
	} else {
		info.Error = "opencode binary not found in PATH"
	}
	return info
}

// HealthCheck verifies the opencode binary is present.
func (r *Runner) HealthCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := r.Binary
	if path == "" {
		resolved, err := osexec.LookPath("opencode")
		if err != nil {
			return fmt.Errorf("opencode binary not found: %w", err)
		}
		path = resolved
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat opencode binary: %w", err)
	}
	if st.IsDir() {
		return fmt.Errorf("opencode binary path is a directory: %s", path)
	}
	return nil
}

// Execute runs one resolved request through the opencode CLI and emits
// JSON-derived events on the returned channel.
func (r *Runner) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	binary := r.Binary
	if binary == "" {
		resolved, err := osexec.LookPath("opencode")
		if err != nil {
			return nil, fmt.Errorf("opencode binary not found: %w", err)
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
		Status:     status,
		ExitCode:   exitCode,
		DurationMS: time.Since(start).Milliseconds(),
	}
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "opencode", req.Reasoning)
	if harnesses.ShouldEmitRunnerReasoningResolution(reasoningResolution) {
		final.Reasoning = &reasoningResolution
	}
	if runErr != nil && status != "success" {
		final.Error = runErr.Error()
	} else if stderr != "" && status != "success" {
		final.Error = trimErrorBlob(stderr)
	}
	if agg != nil {
		final.FinalText = agg.FinalText
		final.Usage, final.Warnings = harnesses.ResolveFinalUsage(agg.UsageSources)
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
		base = []string{"run", "--format", "json"}
	}
	args := append([]string{}, base...)
	modelResolution := harnesses.ResolveRunnerModelWithCache(r.DiscoveryCache, "opencode", modelcatalog.SurfaceEmbeddedOpenAI, req.Model, "opencode/gpt-5.4")
	reasoningResolution := harnesses.ResolveRunnerReasoningWithCache(r.DiscoveryCache, "opencode", req.Reasoning)

	// opencode run auto-approves all tool permissions; no extra flags per level.

	// WorkDir flag: --dir <dir>
	if req.WorkDir != "" {
		args = append(args, "--dir", req.WorkDir)
	}

	// Model flag: -m <model>
	if modelResolution.ResolvedModel != "" {
		args = append(args, "-m", opencodeModelArg(req.Provider, modelResolution.ResolvedModel))
	}

	// Reasoning flag: --variant <reasoning>
	if value := reasoningResolution.ResolvedReasoning; value != "" {
		args = append(args, "--variant", value)
	}

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
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if promptMode != "arg" {
		cmd.Stdin = strings.NewReader(req.Prompt)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	batch, err := processlifecycle.StartBatch(runCtx, cmd, processlifecycle.BatchOptions{
		Harness: "opencode", OperationID: req.SessionID, SessionLogDir: req.SessionLogDir,
	})
	if err != nil {
		return nil, -1, "", err, "failed"
	}
	defer batch.Stop()

	progressLog, _ := harnesses.OpenProgressLog(req.SessionLogDir, req.SessionID, "opencode")
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
		parseAgg, parseErr = parseOpencodeStream(runCtx, parserReader, mirrored, req.Metadata, seq)
		close(mirrored)
		<-mirrorDone
	}()

	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		_, _ = io.Copy(parserWriter, stdoutPipe)
		_ = parserWriter.Close()
	}()

	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stringBuilderWriter{&stderrBuf}, stderrPipe)
	}()

	var timedOut atomic.Bool
	if req.Timeout > 0 {
		stop := make(chan struct{})
		go func() {
			select {
			case <-stop:
			case <-time.After(req.Timeout):
				timedOut.Store(true)
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
	case <-stdoutDone:
	}

	<-stderrDone
	<-parseDone
	runErr = batch.Wait()
	stderr = stderrBuf.String()

	switch {
	case timedOut.Load():
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

func opencodeModelArg(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" || strings.Contains(model, "/") {
		return model
	}
	return provider + "/" + model
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

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness. It calls
// the live opencode models discovery helper with a sensible timeout; on failure,
// returns ErrModelDiscoveryEvidenceMissing per the no-static-fallback principle.
func (r *Runner) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	binary := r.Binary
	if binary == "" {
		binary = "opencode"
	}
	snapshot, err := readOpenCodeModelDiscovery(context.Background(), binary)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("model discovery: %w", err)
	}
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

// SupportedAliases returns nil: opencode requires exact provider/model
// identifiers and recognizes no family alias. Implements
// harnesses.ModelDiscoveryHarness.
func (r *Runner) SupportedAliases() []string {
	return nil
}

// ResolveModelAlias always returns harnesses.ErrAliasNotResolvable because
// opencode does not recognize family aliases. Implements
// harnesses.ModelDiscoveryHarness.
func (r *Runner) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	return "", harnesses.ErrAliasNotResolvable
}
