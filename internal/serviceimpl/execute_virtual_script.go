package serviceimpl

import (
	"context"
	"fmt"
	"strconv"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	virtualprovider "github.com/easel/fizeau/internal/provider/virtual"
)

// ExecuteRunnerDecision is the API-neutral routing data needed by internal
// virtual/script runner implementations.
type ExecuteRunnerDecision struct {
	Harness        string
	Provider       string
	Endpoint       string
	ServerInstance string
	Model          string
}

// ExecuteRunnerRequest is the API-neutral request data needed by internal
// virtual/script runner implementations.
type ExecuteRunnerRequest struct {
	Prompt   string
	Metadata map[string]string
	Decision ExecuteRunnerDecision
	Started  time.Time
}

// ExecuteRunnerResult is the normalized result produced by an internal runner.
type ExecuteRunnerResult struct {
	Final    harnesses.FinalData
	Text     string
	EmitText bool
}

// RunVirtual executes the service virtual harness mechanics without depending
// on root public service types.
func RunVirtual(ctx context.Context, req ExecuteRunnerRequest) ExecuteRunnerResult {
	meta := req.Metadata
	inlineText := meta["virtual.response"]
	cfg := virtualprovider.Config{
		DictDir: meta["virtual.dict_dir"],
	}
	if inlineText != "" {
		cfg.InlineResponses = []virtualprovider.InlineResponse{{
			PromptMatch: metaValue(meta, "virtual.prompt_match", req.Prompt),
			Response: agentcore.Response{
				Content: inlineText,
				Usage: agentcore.TokenUsage{
					Input:  metadataInt(meta, "virtual.input_tokens"),
					Output: metadataInt(meta, "virtual.output_tokens"),
					Total:  metadataInt(meta, "virtual.total_tokens"),
				},
				Model: metaValue(meta, "virtual.model", req.Decision.Model),
			},
			DelayMS: metadataInt(meta, "virtual.delay_ms"),
		}}
	}
	if cfg.DictDir == "" && len(cfg.InlineResponses) == 0 {
		return ExecuteRunnerResult{Final: failedFinal(req, "virtual harness requires metadata virtual.response or virtual.dict_dir")}
	}

	resp, err := virtualprovider.New(cfg).Chat(ctx, []agentcore.Message{{Role: agentcore.RoleUser, Content: req.Prompt}}, nil, agentcore.Options{})
	final := harnesses.FinalData{
		DurationMS: durationMS(req.Started),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        req.Decision.Harness,
			Provider:       req.Decision.Provider,
			ServerInstance: req.Decision.ServerInstance,
			Model:          metaValue(meta, "virtual.model", req.Decision.Model),
		},
	}
	if err != nil {
		final.Status = "failed"
		final.Error = err.Error()
		return ExecuteRunnerResult{Final: final}
	}
	final.Status = "success"
	final.FinalText = resp.Content
	final.Usage = &harnesses.FinalUsage{
		InputTokens:  harnesses.IntPtr(resp.Usage.Input),
		OutputTokens: harnesses.IntPtr(resp.Usage.Output),
		TotalTokens:  harnesses.IntPtr(resp.Usage.Total),
		Source:       harnesses.UsageSourceFallback,
	}
	return ExecuteRunnerResult{
		Final:    final,
		Text:     resp.Content,
		EmitText: resp.Content != "",
	}
}

// RunScript executes the service script harness mechanics without depending
// on root public service types.
func RunScript(ctx context.Context, req ExecuteRunnerRequest) ExecuteRunnerResult {
	meta := req.Metadata
	delay := metadataInt(meta, "script.delay_ms")
	if delay > 0 {
		timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ExecuteRunnerResult{Final: failedOrCancelledFinal(req, "cancelled", ctx.Err().Error())}
		case <-timer.C:
		}
	}

	text, ok := meta["script.stdout"]
	if !ok {
		return ExecuteRunnerResult{Final: failedFinal(req, "script harness requires metadata script.stdout")}
	}
	exitCode := metadataInt(meta, "script.exit_code")
	final := harnesses.FinalData{
		Status:     "success",
		ExitCode:   exitCode,
		FinalText:  text,
		DurationMS: durationMS(req.Started),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        req.Decision.Harness,
			Provider:       req.Decision.Provider,
			ServerInstance: req.Decision.ServerInstance,
			Model:          req.Decision.Model,
		},
	}
	if exitCode != 0 {
		final.Status = "failed"
		final.Error = metaValue(meta, "script.stderr", fmt.Sprintf("script exited with status %d", exitCode))
	}
	return ExecuteRunnerResult{
		Final:    final,
		Text:     text,
		EmitText: text != "",
	}
}

func failedFinal(req ExecuteRunnerRequest, msg string) harnesses.FinalData {
	return failedOrCancelledFinal(req, "failed", msg)
}

func failedOrCancelledFinal(req ExecuteRunnerRequest, status, msg string) harnesses.FinalData {
	return harnesses.FinalData{
		Status:     status,
		Error:      msg,
		DurationMS: durationMS(req.Started),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        req.Decision.Harness,
			Provider:       req.Decision.Provider,
			ServerInstance: req.Decision.ServerInstance,
			Model:          req.Decision.Model,
		},
	}
}

func durationMS(started time.Time) int64 {
	if started.IsZero() {
		return 0
	}
	return time.Since(started).Milliseconds()
}

func metaValue(meta map[string]string, key, fallback string) string {
	if meta == nil {
		return fallback
	}
	if v := meta[key]; v != "" {
		return v
	}
	return fallback
}

func metadataInt(meta map[string]string, key string) int {
	if meta == nil {
		return 0
	}
	n, _ := strconv.Atoi(meta[key])
	return n
}
