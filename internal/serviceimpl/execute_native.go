package serviceimpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
)

const (
	defaultStallReadOnlyIterations = 25
	defaultMaxIterations           = 200
)

var nativeReadOnlyTools = map[string]bool{
	"read":       true,
	"read_file":  true,
	"grep":       true,
	"ls":         true,
	"find":       true,
	"cat":        true,
	"head":       true,
	"tail":       true,
	"stat":       true,
	"web_fetch":  true,
	"web_search": true,
}

// NativeDecision is the API-neutral routing data needed by the native runner.
type NativeDecision struct {
	Harness               string
	Provider              string
	ServerInstance        string
	Model                 string
	SelectedContextWindow int
	SelectedContextSource string
	Candidates            []NativeRouteCandidate
}

// NativeRouteCandidate is the API-neutral subset of root RouteCandidate used
// by the native failover provider.
type NativeRouteCandidate struct {
	Provider       string
	Endpoint       string
	ServerInstance string
	Model          string
	Eligible       bool
}

// NativeRequest is the API-neutral request data needed by the native runner.
type NativeRequest struct {
	Prompt                    string
	SystemPrompt              string
	Model                     string
	Provider                  string
	Harness                   string
	WorkDir                   string
	Temperature               *float32
	TopP                      *float64
	TopK                      *int
	MinP                      *float64
	RepetitionPenalty         *float64
	Seed                      *int64
	SamplingSource            string
	Reasoning                 agentcore.Reasoning
	NoStream                  bool
	Permissions               string
	Tools                     []agentcore.Tool
	ToolPreset                string
	PlanningMode              bool
	MaxIterations             int
	MaxTokens                 int
	ReasoningByteLimit        int
	SelectedContextWindow     int
	SelectedContextSource     string
	CompactionContextWindow   int
	CompactionReserveTokens   int
	ProviderTimeout           time.Duration
	Timeout                   time.Duration
	CachePolicy               string
	StallMaxReadOnlyIteration *int
	Metadata                  map[string]string
	Decision                  NativeDecision
	Started                   time.Time
	SessionID                 string
	// CostCapUSD mirrors ServiceExecuteRequest.CostCapUSD. See
	// internal/core.Request.CostCapUSD for the enforcement contract.
	CostCapUSD float64
}

// NativeProviderRequest is the narrow provider-selection request that crosses
// from the internal runner back into root-owned provider configuration.
type NativeProviderRequest struct {
	Provider string
	Harness  string
	Model    string
}

// NativeProviderResolution is the API-neutral result of root-owned provider
// construction.
type NativeProviderResolution struct {
	Provider agentcore.Provider
	Name     string
	Model    string
}

// NativeCallbacks are root-owned service seams used by the internal runner
// without importing root public contract types.
type NativeCallbacks struct {
	ResolveProvider            func(NativeProviderRequest) NativeProviderResolution
	ProviderNotConfiguredError func(NativeProviderRequest, NativeDecision) string
	Compactor                  func(model string) agentcore.Compactor
	ObserveAgentEvent          func(agentcore.Event)
	EmitEvent                  func(harnesses.EventType, any)
	BeforeFinal                func(harnesses.FinalData)
	Finalize                   func(harnesses.FinalData, TerminalOrigin)
	ToolWiringHook             func(harness string, toolNames []string)
	PromptAssertionHook        func(systemPrompt, prompt string, contextFiles []string)
	CompactionAssertionHook    func(messagesBefore, messagesAfter, tokensFreed int)
	ObserveTokenUsage          func(provider string, tokens int, at time.Time)
}

// RunNative drives the in-process agent loop without depending on root public
// service types.
func RunNative(ctx context.Context, req NativeRequest, cb NativeCallbacks) {
	workingContextWindow, contextWindowErr := resolveNativeWorkingContextWindow(req)
	if contextWindowErr != nil {
		finalize(cb, harnesses.FinalData{
			Status:     "failed",
			Error:      contextWindowErr.Error(),
			DurationMS: time.Since(req.Started).Milliseconds(),
			RoutingActual: &harnesses.RoutingActual{
				Harness:        req.Decision.Harness,
				Provider:       req.Decision.Provider,
				ServerInstance: req.Decision.ServerInstance,
				Model:          req.Decision.Model,
			},
		}, TerminalOriginToolLoop)
		return
	}
	provider := nativeExecutionProvider(req, cb.ResolveProvider)
	actualHarness := req.Decision.Harness
	if actualHarness == "" {
		actualHarness = "fiz"
	}
	resolvedProvider := resolveProvider(cb.ResolveProvider, nativeProviderRequest(req, req.Decision))
	actualProvider := resolvedProvider.Name
	if actualProvider == "" {
		actualProvider = req.Decision.Provider
	}
	actualModel := req.Decision.Model
	if actualModel == "" {
		actualModel = resolvedProvider.Model
	}
	if provider == nil {
		finalize(cb, harnesses.FinalData{
			Status:     "failed",
			Error:      providerNotConfiguredError(cb.ProviderNotConfiguredError, req, req.Decision),
			DurationMS: time.Since(req.Started).Milliseconds(),
			RoutingActual: &harnesses.RoutingActual{
				Harness:        actualHarness,
				Provider:       actualProvider,
				ServerInstance: req.Decision.ServerInstance,
				Model:          actualModel,
			},
		}, TerminalOriginRouting)
		return
	}
	permission, permissionErr := nativePermissionMode(req.Permissions)
	if permissionErr != nil {
		finalize(cb, harnesses.FinalData{
			Status:     "failed",
			Error:      permissionErr.Error(),
			DurationMS: time.Since(req.Started).Milliseconds(),
			RoutingActual: &harnesses.RoutingActual{
				Harness:        actualHarness,
				Provider:       actualProvider,
				ServerInstance: req.Decision.ServerInstance,
				Model:          actualModel,
			},
		}, TerminalOriginToolLoop)
		return
	}

	if req.ProviderTimeout > 0 {
		provider = wrapProviderRequestTimeout(provider, req.ProviderTimeout)
	}

	policyMax := defaultStallReadOnlyIterations
	if req.StallMaxReadOnlyIteration != nil {
		policyMax = *req.StallMaxReadOnlyIteration
	}
	maxIter := req.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}

	var (
		readOnlyStreak   atomic.Int64
		stalled          atomic.Bool
		stallReason      atomic.Value
		stallCount       atomic.Int64
		rejectedCapacity *harnesses.ContextCapacityData
	)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	loopCB := func(ev agentcore.Event) {
		if cb.ObserveAgentEvent != nil {
			cb.ObserveAgentEvent(ev)
		}
		switch ev.Type {
		case agentcore.EventContextCapacity:
			var payload agentcore.ContextCapacityEventData
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				break
			}
			mapped := contextCapacityDataFromCore(payload)
			if cb.EmitEvent != nil {
				cb.EmitEvent(harnesses.EventTypeContextCapacity, mapped)
			}
			if mapped.Action == agentcore.ContextCapacityActionRejected {
				copy := mapped
				rejectedCapacity = &copy
			}
		case agentcore.EventToolCall:
			var payload nativeToolCallPayload
			_ = json.Unmarshal(ev.Data, &payload)
			if !toolLikelyMakesProgress(payload.Tool, payload) {
				if v := readOnlyStreak.Add(1); policyMax > 0 && int(v) >= policyMax {
					if stalled.CompareAndSwap(false, true) {
						stallReason.Store("no_progress_tools_exceeded")
						stallCount.Store(v)
						cancel()
					}
				}
			} else {
				readOnlyStreak.Store(0)
			}
		case agentcore.EventCompactionEnd:
			if cb.CompactionAssertionHook != nil {
				var payload nativeCompactionPayload
				_ = json.Unmarshal(ev.Data, &payload)
				cb.CompactionAssertionHook(payload.MessagesBefore, payload.MessagesAfter, payload.TokensBefore-payload.TokensAfter)
			}
		}
	}

	tools := filterNativeToolsForPermission(req.Tools, permission)
	if cb.ToolWiringHook != nil {
		cb.ToolWiringHook(req.Decision.Harness, ToolNames(tools))
	}
	if cb.PromptAssertionHook != nil {
		cb.PromptAssertionHook(req.SystemPrompt, req.Prompt, nil)
	}

	var compactor agentcore.Compactor
	if cb.Compactor != nil {
		compactor = cb.Compactor(actualModel)
	} else {
		compactor = newNativeCompactor(req, workingContextWindow)
	}
	var temperature *float64
	if req.Temperature != nil {
		t := float64(*req.Temperature)
		temperature = &t
	}
	var seed int64
	if req.Seed != nil {
		seed = *req.Seed
	}
	loopReq := agentcore.Request{
		Prompt:             req.Prompt,
		SystemPrompt:       req.SystemPrompt,
		Provider:           provider,
		Tools:              tools,
		WorkDir:            req.WorkDir,
		Callback:           loopCB,
		Metadata:           req.Metadata,
		MaxIterations:      maxIter,
		Temperature:        temperature,
		TopP:               req.TopP,
		TopK:               req.TopK,
		MinP:               req.MinP,
		RepetitionPenalty:  req.RepetitionPenalty,
		Seed:               seed,
		SamplingSource:     req.SamplingSource,
		Reasoning:          req.Reasoning,
		NoStream:           req.NoStream,
		MaxTokens:          req.MaxTokens,
		ReasoningByteLimit: req.ReasoningByteLimit,
		Compactor:          compactor,
		CachePolicy:        req.CachePolicy,
		PlanningMode:       req.PlanningMode,
		CostCapUSD:         req.CostCapUSD,
	}
	projectNativeDispatchToCore(&loopReq, req, actualProvider, actualModel)
	result, runErr := agentcore.Run(cancelCtx, loopReq)
	if shouldRetryNativeNoStream(req.NoStream, result, runErr) {
		loopReq.NoStream = true
		loopReq.InitialCapacityAttempts = result.CapacityAttempts
		result, runErr = agentcore.Run(cancelCtx, loopReq)
	}

	finalProvider := actualProvider
	if result.SelectedProvider != "" {
		finalProvider = result.SelectedProvider
	}
	finalModel := actualModel
	if result.ResolvedModel != "" {
		finalModel = result.ResolvedModel
	}
	final := harnesses.FinalData{
		DurationMS:      time.Since(req.Started).Milliseconds(),
		FinalText:       result.Output,
		ContextCapacity: rejectedCapacity,
		RoutingActual: &harnesses.RoutingActual{
			Harness:            actualHarness,
			Provider:           finalProvider,
			ServerInstance:     req.Decision.ServerInstance,
			Model:              finalModel,
			FallbackChainFired: append([]string(nil), result.AttemptedProviders...),
		},
	}
	final.Usage = &harnesses.FinalUsage{
		InputTokens:      harnesses.IntPtr(result.Tokens.Input),
		OutputTokens:     harnesses.IntPtr(result.Tokens.Output),
		CacheReadTokens:  nil,
		CacheWriteTokens: nil,
		TotalTokens:      harnesses.IntPtr(result.Tokens.Total),
		Source:           harnesses.UsageSourceFallback,
	}
	if result.Tokens.CacheRead > 0 {
		final.Usage.CacheReadTokens = harnesses.IntPtr(result.Tokens.CacheRead)
	}
	if result.Tokens.CacheWrite > 0 {
		final.Usage.CacheWriteTokens = harnesses.IntPtr(result.Tokens.CacheWrite)
	}
	if result.Tokens.CacheRead > 0 || result.Tokens.CacheWrite > 0 {
		final.Usage.CacheTokens = harnesses.IntPtr(result.Tokens.CacheRead + result.Tokens.CacheWrite)
	}
	final.FinalCostUSD, final.FinalCostSource = projectNativeFinalCost(result)
	if final.FinalCostUSD != nil {
		final.CostUSD = *final.FinalCostUSD
	}
	if result.Output != "" && cb.EmitEvent != nil {
		cb.EmitEvent(harnesses.EventTypeTextDelta, harnesses.TextDeltaData{Text: result.Output})
	}
	if cb.BeforeFinal != nil {
		cb.BeforeFinal(final)
	}
	switch {
	case stalled.Load():
		final.Status = "stalled"
		reason, _ := stallReason.Load().(string)
		if cb.EmitEvent != nil {
			cb.EmitEvent(harnesses.EventTypeStall, map[string]any{
				"reason": reason,
				"count":  stallCount.Load(),
			})
		}
		final.Error = reason
	case ctx.Err() == context.DeadlineExceeded || (req.Timeout > 0 && time.Since(req.Started) >= req.Timeout):
		final.Status = "timed_out"
		final.Error = "wall-clock timeout"
	case ctx.Err() == context.Canceled:
		final.Status = "cancelled"
	case errors.Is(runErr, errProviderRequestTimeout):
		final.Status = "timed_out"
		final.Error = runErr.Error()
	case runErr != nil:
		final.Status = "failed"
		final.Error = runErr.Error()
	case result.Status == agentcore.StatusError:
		final.Status = "failed"
		if result.Error != nil {
			final.Error = result.Error.Error()
		}
	case result.Status == agentcore.StatusIterationLimit:
		final.Status = string(agentcore.StatusIterationLimit)
	case result.Status == agentcore.StatusBudgetHalted:
		// FEAT-005 §26-29 / AC-FEAT-005-07: per-run cost cap halt is a
		// first-class terminal status. Surface the loop's error string so
		// downstream logs see "running + projected >= cap".
		final.Status = string(agentcore.StatusBudgetHalted)
		if result.Error != nil {
			final.Error = result.Error.Error()
		}
	default:
		final.Status = "success"
	}
	origin := nativeTerminalOrigin(runErr)
	if origin != TerminalOriginContextCapacity && final.Status == "failed" && final.Error != "" && final.RoutingActual != nil {
		final.RoutingActual.FailureClass = classifyDispatchFailure(final.Error)
	}
	if final.RoutingActual != nil && final.Usage != nil && cb.ObserveTokenUsage != nil {
		cb.ObserveTokenUsage(final.RoutingActual.Provider, finalUsageTotalTokens(final.Usage), time.Now())
	}
	finalize(cb, final, origin)
}

func contextCapacityDataFromCore(payload agentcore.ContextCapacityEventData) harnesses.ContextCapacityData {
	return harnesses.ContextCapacityData{
		Action:                 payload.Action,
		CallKind:               payload.CallKind,
		TurnIndex:              payload.TurnIndex,
		AttemptIndex:           payload.AttemptIndex,
		ContextWindow:          payload.ContextWindow,
		EffectiveContextWindow: payload.EffectiveContextWindow,
		EstimatedInputTokens:   payload.EstimatedInputTokens,
		RequestedMaxTokens:     payload.RequestedMaxTokens,
		EffectiveMaxTokens:     payload.EffectiveMaxTokens,
		AvailableOutputTokens:  payload.AvailableOutputTokens,
	}
}

// projectNativeDispatchToCore copies resolved dispatch identity, selected
// context evidence, and the raw public override into the core request without
// applying capacity policy. Working-window arithmetic and validation belong to
// the core capacity boundary.
func projectNativeDispatchToCore(dst *agentcore.Request, req NativeRequest, actualProvider, actualModel string) {
	dst.SelectedProvider = actualProvider
	dst.ResolvedModel = actualModel
	dst.SelectedContextWindow = req.SelectedContextWindow
	dst.SelectedContextSource = req.SelectedContextSource
	dst.CompactionContextWindow = req.CompactionContextWindow
}

func projectNativeFinalCost(result agentcore.Result) (*float64, harnesses.CostSource) {
	var source harnesses.CostSource
	switch result.FinalCostSource {
	case agentcore.SessionCostSourceReported:
		source = harnesses.CostSourceReported
	case agentcore.SessionCostSourceConfigured:
		source = harnesses.CostSourceConfigured
	default:
		source = harnesses.CostSourceUnknown
	}
	return normalizeProjectedFinalCost(result.FinalCostUSD, source)
}

func normalizeProjectedFinalCost(cost *float64, source harnesses.CostSource) (*float64, harnesses.CostSource) {
	if cost == nil || *cost < 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) {
		return nil, harnesses.CostSourceUnknown
	}
	switch source {
	case harnesses.CostSourceReported, harnesses.CostSourceConfigured:
		cloned := *cost
		return &cloned, source
	default:
		return nil, harnesses.CostSourceUnknown
	}
}

func resolveNativeWorkingContextWindow(req NativeRequest) (int, error) {
	return agentcore.ResolveWorkingContextWindow(req.SelectedContextWindow, req.CompactionContextWindow)
}

func newNativeCompactor(req NativeRequest, workingContextWindow int) agentcore.Compactor {
	return compaction.NewCompactor(nativeCompactionConfig(req, workingContextWindow))
}

func nativeCompactionConfig(req NativeRequest, workingContextWindow int) compaction.Config {
	cfg := compaction.DefaultConfig()
	if workingContextWindow > 0 {
		cfg.ContextWindow = workingContextWindow
		if cfg.ReserveTokens >= cfg.ContextWindow {
			cfg.ReserveTokens = 0
		}
		if cfg.KeepRecentTokens > cfg.ContextWindow {
			cfg.KeepRecentTokens = cfg.ContextWindow / 2
		}
	}
	if req.CompactionReserveTokens > 0 {
		cfg.ReserveTokens = req.CompactionReserveTokens
	}
	return cfg
}

func nativeExecutionProvider(req NativeRequest, resolver func(NativeProviderRequest) NativeProviderResolution) agentcore.Provider {
	if len(req.Decision.Candidates) > 0 {
		return &nativeRouteProvider{
			baseRequest:      req,
			routeKey:         req.Model,
			candidates:       append([]NativeRouteCandidate(nil), req.Decision.Candidates...),
			selectedProvider: req.Decision.Provider,
			resolveProvider:  resolver,
		}
	}
	resolvedProvider := resolveProvider(resolver, nativeProviderRequest(req, req.Decision))
	return resolvedProvider.Provider
}

type nativeRouteProvider struct {
	baseRequest      NativeRequest
	routeKey         string
	candidates       []NativeRouteCandidate
	selectedProvider string
	attempted        []string
	failoverCount    int
	resolveProvider  func(NativeProviderRequest) NativeProviderResolution
}

func (p *nativeRouteProvider) Chat(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolDef, opts agentcore.Options) (agentcore.Response, error) {
	candidate, ok := p.selectedCandidate()
	if !ok {
		return agentcore.Response{}, fmt.Errorf("agent: route %q has no selected provider candidate", p.routeKey)
	}
	p.attempted = append(p.attempted, candidate.Provider)
	req := NativeProviderRequest{
		Provider: candidate.Provider,
		Model:    candidate.Model,
		Harness:  p.baseRequest.Harness,
	}
	if candidate.Endpoint != "" {
		req.Provider = endpointProviderRef(candidate.Provider, candidate.Endpoint)
	}
	resolved := resolveProvider(p.resolveProvider, req)
	if resolved.Provider == nil {
		return agentcore.Response{}, fmt.Errorf("agent: provider error: no provider configured for %q", req.Provider)
	}
	opts.Model = candidate.Model
	resp, err := resolved.Provider.Chat(ctx, messages, tools, opts)
	if err != nil {
		return agentcore.Response{}, err
	}
	p.selectedProvider = candidate.Provider
	if resp.Attempt == nil {
		resp.Attempt = &agentcore.AttemptMetadata{}
	}
	resp.Attempt.ProviderName = candidate.Provider
	resp.Attempt.Route = p.routeKey
	if resp.Attempt.RequestedModel == "" {
		resp.Attempt.RequestedModel = p.routeKey
	}
	return resp, nil
}

func (p *nativeRouteProvider) selectedCandidate() (NativeRouteCandidate, bool) {
	for _, candidate := range p.candidates {
		if !candidate.Eligible || candidate.Provider == "" {
			continue
		}
		if p.selectedProvider == "" || candidate.Provider == p.selectedProvider {
			return candidate, true
		}
	}
	return NativeRouteCandidate{}, false
}

func (p *nativeRouteProvider) RoutingReport() agentcore.RoutingReport {
	return agentcore.RoutingReport{
		SelectedProvider:   p.selectedProvider,
		SelectedRoute:      p.routeKey,
		AttemptedProviders: append([]string(nil), p.attempted...),
		FailoverCount:      p.failoverCount,
	}
}

func nativeProviderRequest(req NativeRequest, decision NativeDecision) NativeProviderRequest {
	out := NativeProviderRequest{
		Provider: req.Provider,
		Harness:  req.Harness,
		Model:    req.Model,
	}
	if decision.Provider != "" {
		out.Provider = decision.Provider
	}
	if decision.Model != "" {
		out.Model = decision.Model
	}
	if decision.Harness != "" {
		out.Harness = decision.Harness
	}
	return out
}

func resolveProvider(resolver func(NativeProviderRequest) NativeProviderResolution, req NativeProviderRequest) NativeProviderResolution {
	if resolver == nil {
		return NativeProviderResolution{}
	}
	return resolver(req)
}

func providerNotConfiguredError(fn func(NativeProviderRequest, NativeDecision) string, req NativeRequest, decision NativeDecision) string {
	if fn != nil {
		return fn(nativeProviderRequest(req, decision), decision)
	}
	if decision.Model == "" {
		return "no provider configured for native harness"
	}
	return "orphan model: " + decision.Model
}

func nativePermissionMode(permissions string) (string, error) {
	switch permissions {
	case "", "safe":
		return "safe", nil
	case "unrestricted":
		return "unrestricted", nil
	case "supervised":
		return "", fmt.Errorf("native agent permission mode %q is unsupported because no approval loop is available", permissions)
	default:
		return "", fmt.Errorf("native agent permission mode %q is unsupported", permissions)
	}
}

func filterNativeToolsForPermission(tools []agentcore.Tool, permission string) []agentcore.Tool {
	if permission == "unrestricted" {
		return tools
	}
	filtered := make([]agentcore.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if nativeReadOnlyTools[tool.Name()] {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// ToolNames returns names for non-nil tools.
func ToolNames(tools []agentcore.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		names = append(names, tool.Name())
	}
	return names
}

type nativeToolCallPayload struct {
	Tool       string          `json:"tool"`
	Input      json.RawMessage `json:"input"`
	Output     string          `json:"output"`
	DurationMS int64           `json:"duration_ms"`
	Error      string          `json:"error"`
}

type nativeCompactionPayload struct {
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
	TokensBefore   int    `json:"tokens_before"`
	TokensAfter    int    `json:"tokens_after"`
	Summary        string `json:"summary"`
	Warning        string `json:"warning"`
}

func toolLikelyMakesProgress(toolName string, payload nativeToolCallPayload) bool {
	if strings.TrimSpace(payload.Error) != "" {
		return false
	}
	if nativeReadOnlyTools[toolName] {
		return false
	}
	if toolName == "bash" {
		var input any
		if len(payload.Input) > 0 {
			if err := json.Unmarshal(payload.Input, &input); err == nil {
				return bashCommandLikelyMutates(extractBashCommand(input))
			}
		}
		return false
	}
	return true
}

func shouldRetryNativeNoStream(requestedStream bool, result agentcore.Result, runErr error) bool {
	if requestedStream || runErr != nil {
		return false
	}
	if result.Status != agentcore.StatusSuccess {
		return false
	}
	if result.Output != "" || len(result.ToolCalls) > 0 {
		return false
	}
	return result.Tokens.Input == 0 && result.Tokens.Output == 0 && result.Tokens.Total == 0
}

func classifyDispatchFailure(errMsg string) string {
	msg := strings.ToLower(errMsg)
	switch {
	case strings.Contains(msg, "no provider configured"),
		strings.Contains(msg, "not available"),
		strings.Contains(msg, "exhausted"),
		strings.Contains(msg, "not configured"):
		return "availability"
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "deadline"),
		strings.Contains(msg, "connection"),
		strings.Contains(msg, "refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "transport"):
		return "transport"
	case strings.Contains(msg, "http "),
		strings.Contains(msg, "status "),
		strings.Contains(msg, "bad request"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "not found"),
		strings.Contains(msg, "unsupported"):
		return "protocol"
	default:
		return "unknown"
	}
}

func extractBashCommand(raw any) string {
	input, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	command, _ := input["command"].(string)
	return strings.TrimSpace(command)
}

func bashCommandLikelyMutates(command string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	if command == "" {
		return false
	}
	readOnlyPrefixes := []string{
		"cat ", "head ", "tail ", "ls", "find ", "grep ", "rg ", "sed -n",
		"awk ", "pwd", "git status", "git diff", "git log", "go test",
	}
	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(command, prefix) {
			return false
		}
	}
	mutatingFragments := []string{
		" mv ", " cp ", " rm ", "mkdir", "touch ", "chmod ", "chown ",
		"git add", "git commit", "git merge", "git pull", "git push",
		" gofmt", "go fmt", "npm install", "pip install", "cargo build",
		"cat >",
		"cat <<",
		"echo ",
		"mv ",
		"cp ",
		"rm ",
		"sed -i",
		"tee ",
		">",
		">>",
		"apply_patch",
		"patch ",
	}
	for _, fragment := range mutatingFragments {
		if strings.Contains(command, fragment) {
			return true
		}
	}
	return false
}

func finalUsageTotalTokens(u *harnesses.FinalUsage) int {
	if u == nil {
		return 0
	}
	if u.TotalTokens != nil && *u.TotalTokens > 0 {
		return *u.TotalTokens
	}
	var total int
	if u.InputTokens != nil {
		total += *u.InputTokens
	}
	if u.OutputTokens != nil {
		total += *u.OutputTokens
	}
	return total
}

func endpointProviderRef(providerName, endpointName string) string {
	if endpointName == "" {
		return providerName
	}
	return providerName + "@" + endpointName
}

func finalize(cb NativeCallbacks, final harnesses.FinalData, origin TerminalOrigin) {
	if cb.Finalize != nil {
		cb.Finalize(final, origin)
	}
}

func nativeTerminalOrigin(runErr error) TerminalOrigin {
	switch {
	case errors.Is(runErr, agentcore.ErrContextCapacityExceeded):
		return TerminalOriginContextCapacity
	case errors.Is(runErr, agentcore.ErrToolCallLoop),
		errors.Is(runErr, agentcore.ErrCompactionNoFit),
		errors.Is(runErr, agentcore.ErrCompactionStuck),
		errors.Is(runErr, agentcore.ErrReasoningOverflow),
		errors.Is(runErr, agentcore.ErrReasoningStall):
		return TerminalOriginToolLoop
	default:
		return TerminalOriginProvider
	}
}

var errProviderRequestTimeout = errors.New("provider request timeout")

func wrapProviderRequestTimeout(p agentcore.Provider, requestTimeout time.Duration) agentcore.Provider {
	if p == nil || requestTimeout <= 0 {
		return p
	}
	return &timeoutProviderInline{inner: p, requestTimeout: requestTimeout}
}

type timeoutProviderInline struct {
	inner          agentcore.Provider
	requestTimeout time.Duration
}

func (p *timeoutProviderInline) Chat(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolDef, opts agentcore.Options) (agentcore.Response, error) {
	if p.requestTimeout <= 0 {
		return p.inner.Chat(ctx, messages, tools, opts)
	}
	cctx, cancel := context.WithTimeout(ctx, p.requestTimeout)
	defer cancel()
	resp, err := p.inner.Chat(cctx, messages, tools, opts)
	if err != nil && ctx.Err() == nil && cctx.Err() == context.DeadlineExceeded {
		return resp, fmt.Errorf("%w: wall-clock %s", errProviderRequestTimeout, p.requestTimeout)
	}
	return resp, err
}
