package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/easel/fizeau/internal/compaction"
	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/easel/fizeau/internal/tool"
)

// generateSessionID returns a unique session identifier for a new Execute.
func generateSessionID() string {
	return fmt.Sprintf("svc-%d", time.Now().UnixNano())
}

// Execute runs an agent task in-process; emits Events on the returned
// channel until the task terminates (channel closes). The final event
// (type=final) carries status, usage, cost, session-log path, and the
// resolved fallback chain that fired.
//
// See CONTRACT-003 §"Behaviors the contract guarantees" for the full
// behavior contract this method honors:
//   - Orphan-model validation (Status=failed when Model unknown)
//   - Provider-deadline wrapping (Timeout + IdleTimeout + ProviderTimeout)
//   - StallPolicy enforcement (stall event before final)
//   - Route-reason attribution (routing_decision start, routing_actual final)
//   - OS-level subprocess cleanup on ctx.Done()
//   - Metadata bidirectional echo (events + session log)
//   - SessionLogDir per-request override
//
// Routing: under-specified requests (no Harness) are dispatched through
// internal/routing.Resolve via ResolveRoute. Callers can run with bare
// Policy/Model/Provider — the engine picks. NativeProvider must
// still be supplied for the native path until provider construction lands
// in a follow-up.
func (s *service) Execute(ctx context.Context, req ServiceExecuteRequest) (<-chan ServiceEvent, error) {
	// Boundary validation: reject unknown CachePolicy values before any
	// session state is opened or events are emitted. Beads C/D consume this
	// field; an unknown value is a caller programming error.
	if err := ValidateCachePolicy(req.CachePolicy); err != nil {
		return nil, err
	}
	if err := ValidatePowerBounds(req.MinPower, req.MaxPower); err != nil {
		return nil, err
	}
	if err := ValidateRole(req.Role); err != nil {
		return nil, err
	}
	if err := ValidateCorrelationID(req.CorrelationID); err != nil {
		return nil, err
	}

	// Generate a session ID and register it in the hub so TailSessionLog
	// callers can subscribe before or during execution.
	sessionID := generateSessionID()
	fanout := s.executeEventFanout()
	fanout.OpenSession(sessionID)

	outer := make(chan ServiceEvent, 64)

	// ADR-006 §3/§4: capture the override context (user pin + unconstrained
	// auto decision) before route resolution so we can fire the matching
	// override / rejected_override event regardless of which path the route
	// resolution takes.
	overrideCtx := s.buildOverrideContext(ctx, req)

	// ADR-006 §5: record this request into the routing-quality store so
	// auto_acceptance_rate / override_disagreement_rate / class_breakdown
	// reflect both overridden and non-overridden traffic. The recorded
	// override payload carries no outcome — outcome aggregation for live
	// requests is best-effort and lives in session logs once that
	// persistence path lands.
	s.recordRoutingQualityForRequest(overrideCtx)

	// Resolve the route.
	decision, err := s.executeRouteResolver().resolveExecuteRouteContext(ctx, req)
	if err != nil {
		// NoViableProviderForNow is a transient quota signal — DDx
		// callers pause their drain loop on RetryAfter and resume.
		// Surface it directly (not via the fatal-final channel) so the
		// typed error reaches errors.As without log scraping.
		var quotaErr *NoViableProviderForNow
		if errors.As(err, &quotaErr) {
			fanout.CloseSession(sessionID, ServiceEvent{})
			return nil, err
		}
		if isExplicitPinError(err) {
			// Emit a rejected_override event (no outcome) when the pin
			// fails pre-dispatch. Surface the typed error wrapped with
			// the rejected_override payload so callers that errors.As
			// the typed pin error still get it; callers wanting the
			// telemetry can extract via AsRejectedOverride.
			pinErr := err
			if overrideCtx != nil {
				if rejectedEv, payload, ok := makeRejectedOverrideEvent(overrideCtx, sessionID, pinErr, req.Metadata); ok {
					fanout.BroadcastEvent(sessionID, rejectedEv)
					// Persist the rejected_override to the session log so
					// UsageReport's windowed scan (which sources from
					// session logs, not the in-memory ring) sees this
					// rejection. The pin failed pre-dispatch, so no
					// runExecute will open a log for this session — open
					// one briefly here, write session.start + the rejected
					// payload, and close.
					s.persistRejectedOverride(req, sessionID, payload)
					pinErr = &ErrRejectedOverride{Inner: err, Event: payload}
				}
			}
			fanout.CloseSession(sessionID, ServiceEvent{})
			return nil, pinErr
		}
		// Still return a channel that yields a single failed final event so
		// downstream consumers don't have to special-case the error path.
		// Also close the hub session so TailSessionLog subscribers unblock.
		go func() {
			emitFatalFinal(outer, req.Metadata, "failed", err.Error())
			// Drain outer to get the final event and forward to hub.
			// emitFatalFinal closes outer; read the single event from it.
		}()
		// We can't easily intercept emitFatalFinal here, so close the hub
		// session with an empty final immediately — callers on TailSessionLog
		// for a failed-route session get an empty close.
		go func() {
			// Wait briefly for emitFatalFinal to write.
			time.Sleep(10 * time.Millisecond)
			fanout.CloseSession(sessionID, ServiceEvent{})
		}()
		return outer, nil
	}

	// Metadata seam: every event we emit echoes req.Metadata.
	meta := req.Metadata

	// Wrap the inner channel through the hub so every event is broadcast to
	// TailSessionLog subscribers. The fan-out goroutine owns outer's close
	// and is responsible for inserting the override event (if any) immediately
	// before the final event per ADR-006 §7.
	inner := wrapExecuteWithHub(fanout, sessionID, outer, overrideCtx, meta)

	// Emit start-of-execution routing_decision so consumers know the picked
	// chain before any real work fires. The actual chain (post-fallback) is
	// stamped onto the final event's RoutingActual field.
	go s.runExecute(ctx, req, *decision, meta, inner, sessionID, overrideCtx)
	return outer, nil
}

// resolveExecuteRoute reduces the request to a concrete RouteDecision.
// The request is dispatched through the routing engine
// (internal/routing.Resolve) when under-specified (Harness == ""), or when
// Harness is set but Model is empty and routing inputs (Policy or MinPower)
// are present (engine runs within the harness's eligible models). When Harness
// is set and Model is also set, the decision is accepted verbatim.
func (s *service) resolveExecuteRoute(req ServiceExecuteRequest) (*RouteDecision, error) {
	return s.resolveExecuteRouteContext(context.Background(), req)
}

func (s *service) resolveExecuteRouteContext(ctx context.Context, req ServiceExecuteRequest) (*RouteDecision, error) {
	// If Harness is omitted, route through the engine. The engine defaults to
	// local-first and auto-selects from configured endpoints when no other
	// constraints are specified — an empty request is valid if providers exist.
	if req.Harness == "" {
		return s.resolveExecuteRouteWithEngine(ctx, req)
	}
	canonical := harnesses.ResolveHarnessAlias(req.Harness)
	if !s.registry.Has(canonical) {
		return nil, fmt.Errorf("unknown harness %q", req.Harness)
	}
	cfg, _ := s.registry.Get(canonical)

	// Run per-field validators first so callers get the most specific error.
	// Empty-model routing checks run after these, right before model resolution.
	if err := validateExplicitHarnessPolicy(canonical, cfg, req.Policy); err != nil {
		return nil, err
	}
	if err := validateExplicitProvider(s.opts.ServiceConfig, cfg, req.Provider); err != nil {
		return nil, err
	}
	if err := validateExplicitHarnessModel(canonical, cfg, req.Model, req.Provider); err != nil {
		return nil, err
	}
	if err := validateExplicitHarnessReasoning(canonical, cfg, req.Reasoning); err != nil {
		return nil, err
	}
	if err := validateExplicitHarnessQuota(canonical, cfg); err != nil {
		return nil, err
	}

	// Empty-model routing: only applies to subprocess harnesses with concrete
	// model semantics. TestOnly (virtual/script), HTTP-provider (openrouter/
	// lmstudio/etc.), and "fiz" harnesses skip this block — they either use
	// DefaultModel directly or have provider-side model semantics.
	if !cfg.TestOnly && !cfg.IsHTTPProvider && canonical != "fiz" && req.Model == "" {
		if req.Policy == "" && req.MinPower == 0 {
			// Under-specified: no model, no routing inputs → silent empty model
			// avoided by failing early with a clear diagnostic.
			return nil, fmt.Errorf("under-specified routing for harness=%q: "+
				"supply --model, --policy, or --min-power", canonical)
		}
		// Policy or MinPower present: run the routing engine within the
		// harness's eligible models. Class 2 harnesses (AutoRoutingEligible=false:
		// gemini, opencode, pi) require an explicit --model.
		if !cfg.AutoRoutingEligible {
			return nil, fmt.Errorf("no auto-resolution available for harness=%q: "+
				"harness does not support auto-routing; supply an explicit --model", canonical)
		}
		return s.resolveExecuteRouteWithEngine(ctx, req)
	}

	resolvedModel := resolveSubprocessModelAlias(canonical, req.Model)
	decision := &RouteDecision{
		Harness:  canonical,
		Provider: req.Provider,
		Model:    resolvedModel,
		Reason:   "explicit",
		Power:    catalogPowerForModel(serviceRoutingCatalog(), resolvedModel),
	}
	if decision.Endpoint == "" {
		_, endpoint, _ := splitEndpointProviderRef(decision.Provider)
		decision.Endpoint = endpoint
	}
	return decision, nil
}

func validateExplicitHarnessQuota(name string, cfg harnesses.HarnessConfig) error {
	if harnessPaymentKind(name, cfg) != modelcatalog.BillingModelSubscription {
		return nil
	}
	now := time.Now()
	qs, ok := subscriptionQuotaForHarness(name, now)
	if !ok || !qs.Present || !qs.Fresh || qs.OK {
		return nil
	}
	return explicitQuotaUnavailable(name, qs.Windows, now)
}

func explicitQuotaUnavailable(name string, windows []harnesses.QuotaWindow, now time.Time) error {
	retryAfter := earliestQuotaResetAfter(windows, now)
	if retryAfter.IsZero() {
		retryAfter = now.Add(defaultQuotaRecoveryFallbackInterval)
	}
	return &NoViableProviderForNow{
		RetryAfter:         retryAfter,
		ExhaustedProviders: []string{name},
	}
}

func earliestQuotaResetAfter(windows []harnesses.QuotaWindow, now time.Time) time.Time {
	var earliest time.Time
	for _, window := range windows {
		if window.ResetsAtUnix <= 0 {
			continue
		}
		reset := time.Unix(window.ResetsAtUnix, 0)
		if !reset.After(now) {
			continue
		}
		if earliest.IsZero() || reset.Before(earliest) {
			earliest = reset
		}
	}
	return earliest
}

func validateExplicitHarnessPolicy(name string, cfg harnesses.HarnessConfig, policy string) error {
	constraint, ok := explicitPolicyConstraint(policy)
	if !ok {
		return nil
	}
	switch constraint {
	case routing.ProviderPreferenceLocalOnly:
		if harnessPaymentKind(name, cfg) != modelcatalog.BillingModelFixed {
			return &ErrPolicyRequirementUnsatisfied{
				Policy:       policy,
				Requirement:  constraint,
				AttemptedPin: "Harness=" + name,
			}
		}
	case routing.ProviderPreferenceSubscriptionOnly:
		if harnessPaymentKind(name, cfg) != modelcatalog.BillingModelSubscription {
			return &ErrPolicyRequirementUnsatisfied{
				Policy:       policy,
				Requirement:  constraint,
				AttemptedPin: "Harness=" + name,
			}
		}
	}
	return nil
}

func explicitPolicyConstraint(policy string) (string, bool) {
	switch policy {
	case "air-gapped":
		return routing.ProviderPreferenceLocalOnly, true
	case "smart":
		return routing.ProviderPreferenceSubscriptionOnly, true
	default:
		return "", false
	}
}

func isExplicitPinError(err error) bool {
	var modelConstraintAmbiguous *ErrModelConstraintAmbiguous
	if errors.As(err, &modelConstraintAmbiguous) {
		return true
	}
	var modelConstraintNoMatch *ErrModelConstraintNoMatch
	if errors.As(err, &modelConstraintNoMatch) {
		return true
	}
	var modelErr *ErrHarnessModelIncompatible
	if errors.As(err, &modelErr) {
		return true
	}
	var policyErr *ErrPolicyRequirementUnsatisfied
	if errors.As(err, &policyErr) && policyErr.AttemptedPin != "" {
		return true
	}
	var pinErr *ErrUnsatisfiablePin
	if errors.As(err, &pinErr) {
		return true
	}
	var providerErr *ErrUnknownProvider
	return errors.As(err, &providerErr)
}

// validateExplicitProvider rejects pre-dispatch when the caller pinned a
// provider name that the service configuration does not recognize. Returns
// nil when no provider was pinned, when no ServiceConfig is configured (no
// provider catalog to validate against), when the provider name is known,
// or when the harness is test-only / does not consume Provider (virtual,
// script, etc. have no real provider lookup).
func validateExplicitProvider(sc ServiceConfig, cfg harnesses.HarnessConfig, provider string) error {
	if provider == "" || sc == nil {
		return nil
	}
	if cfg.TestOnly {
		return nil
	}
	lookup := provider
	if base, _, ok := splitEndpointProviderRef(provider); ok {
		lookup = base
	}
	if _, ok := sc.Provider(lookup); ok {
		return nil
	}
	known := sc.ProviderNames()
	return &ErrUnknownProvider{Provider: provider, KnownProviders: append([]string(nil), known...)}
}

func validateExplicitHarnessModel(name string, cfg harnesses.HarnessConfig, model, provider string) error {
	if model == "" || cfg.TestOnly || cfg.IsHTTPProvider || name == "fiz" {
		return nil
	}
	if modelSupportedForHarness(name, cfg, model, provider) {
		return nil
	}
	supportedModels := subprocessHarnessModelIDs(name, cfg)
	return &ErrHarnessModelIncompatible{
		Harness:         name,
		Model:           model,
		SupportedModels: append([]string(nil), supportedModels...),
	}
}

func modelSupportedForHarness(name string, cfg harnesses.HarnessConfig, model, provider string) bool {
	for _, known := range subprocessHarnessModelIDs(name, cfg) {
		if model == known {
			return true
		}
	}
	switch name {
	case "codex":
		return strings.HasPrefix(model, "gpt-")
	case "claude", "claude-tui":
		// Gate on the claude model FAMILY, not live /model enumeration. The
		// catalog "claude" surface routes tier IDs like "sonnet-4.6" (note: no
		// "claude-" prefix) alongside "claude-opus-4.7"/"claude-haiku-5.5".
		// claude-tui's interactive /model picker only lists the CURRENT model,
		// so a resolved tier will not appear in discovery — but the running
		// session can /model to any family member. Accept any claude-family ID
		// so a default-policy route (e.g. sonnet-4.6) executes via claude-tui
		// instead of being rejected when discovery is cold/incomplete. (claude
		// shares this case; before, it survived only via a warm-cache exact
		// match and would have rejected the bare-tier "sonnet-4.6" too.)
		m := strings.ToLower(model)
		return strings.HasPrefix(m, "claude-") ||
			strings.HasPrefix(m, "sonnet") ||
			strings.HasPrefix(m, "opus") ||
			strings.HasPrefix(m, "haiku")
	case "pi":
		// Pi can route to non-Gemini backends (lmstudio, omlx, etc.) when a
		// provider is pinned. The pi CLI owns per-provider model validation
		// in that case, so the agent-side gate trusts the provider pin and
		// defers concrete model-ID checks to pi --list-models / pi itself.
		return provider != ""
	case "opencode":
		// OpenCode can route to configured provider/model pairs when the
		// provider is pinned; the generated opencode config and CLI own the
		// concrete model validation in that case.
		return provider != ""
	default:
		return false
	}
}

func (s *service) validateEngineResolvedExecuteDecision(req ServiceExecuteRequest, decision *RouteDecision) error {
	if decision == nil || strings.TrimSpace(req.Harness) == "" || strings.TrimSpace(req.Model) != "" {
		return nil
	}
	canonical := harnesses.ResolveHarnessAlias(req.Harness)
	if decision.Harness != "" && decision.Harness != canonical {
		return &ErrUnsatisfiablePin{
			Pin:    "harness=" + canonical,
			Reason: "routing engine returned a different harness",
		}
	}
	cfg, ok := s.registry.Get(canonical)
	if !ok {
		return fmt.Errorf("unknown harness %q", req.Harness)
	}
	normalizedModel := resolveSubprocessModelAlias(canonical, decision.Model)
	if err := validateExplicitHarnessModel(canonical, cfg, normalizedModel, decision.Provider); err != nil {
		return err
	}
	decision.Harness = canonical
	decision.Model = normalizedModel
	if decision.Endpoint == "" {
		_, endpoint, _ := splitEndpointProviderRef(decision.Provider)
		decision.Endpoint = endpoint
	}
	return nil
}

func validateExplicitHarnessReasoning(name string, cfg harnesses.HarnessConfig, value Reasoning) error {
	if cfg.TestOnly {
		return nil
	}
	if len(cfg.ReasoningLevels) == 0 && cfg.MaxReasoningTokens <= 0 {
		return nil
	}
	policy, err := reasoning.ParseString(string(value))
	if err != nil {
		return fmt.Errorf("unsupported reasoning %q for harness %q: %w", value, name, err)
	}
	switch policy.Kind {
	case reasoning.KindUnset, reasoning.KindAuto, reasoning.KindOff:
		return nil
	case reasoning.KindTokens:
		if cfg.MaxReasoningTokens <= 0 {
			return fmt.Errorf("unsupported reasoning %q for harness %q; token budgets are not supported", value, name)
		}
		if policy.Tokens > cfg.MaxReasoningTokens {
			return fmt.Errorf("unsupported reasoning %q for harness %q; max token budget is %d", value, name, cfg.MaxReasoningTokens)
		}
		return nil
	case reasoning.KindNamed:
		for _, supported := range cfg.ReasoningLevels {
			if string(policy.Value) == supported {
				return nil
			}
		}
		return fmt.Errorf("unsupported reasoning %q for harness %q; supported reasoning: %s", value, name, strings.Join(cfg.ReasoningLevels, ", "))
	default:
		return fmt.Errorf("unsupported reasoning %q for harness %q", value, name)
	}
}

func harnessSource(req ServiceExecuteRequest) string {
	if strings.TrimSpace(req.Harness) != "" {
		return "request_harness"
	}
	return "auto_route"
}

// runExecute is the per-Execute goroutine. It owns the channel close path
// and the final event emit. All termination paths funnel through emitFinal
// so the channel always sees a final event before close.
func (s *service) runExecute(ctx context.Context, req ServiceExecuteRequest, decision RouteDecision, meta map[string]string, out chan<- ServiceEvent, sessionID string, overrideCtx *overrideContext) {
	defer close(out)

	start := time.Now()
	var seq atomic.Int64

	// Open the service-owned session log writer and guarantee a terminal
	// session.end record plus a clean file close even on unexpected exits.
	// CONTRACT-003 makes session-log lifecycle a service responsibility; the
	// per-path finalizeAndEmit calls below feed writeEnd in lock-step with
	// the public final event.
	sl := s.executeSessionLogOpener().openSessionLog(req, decision, sessionID)
	if overrideCtx != nil {
		sl.override = overrideCtx
	}
	defer func() {
		if !sl.endWritten() {
			sl.writeEnd(req, meta, harnesses.FinalData{
				Status:     "cancelled",
				Error:      "session ended without final event",
				DurationMS: time.Since(start).Milliseconds(),
				RoutingActual: &harnesses.RoutingActual{
					Harness:  decision.Harness,
					Provider: decision.Provider,
					Model:    decision.Model,
				},
			})
		}
		sl.close()
	}()

	// Wall-clock cap.
	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	// Emit routing_decision start event. Include session_id so callers can
	// extract it and pass to TailSessionLog. Per CONTRACT-003 the
	// top-level Role + CorrelationID are echoed into routing_decision
	// event Metadata (top-level wins over caller Metadata for these
	// reserved keys).
	routingMeta := metaWithRoleAndCorrelation(meta, req.Role, req.CorrelationID)
	emitJSON(out, &seq, harnesses.EventTypeRoutingDecision, routingMeta, serviceRoutingDecisionDataFromDecision(req, decision, sessionID))
	emitProgress(out, &seq, sl, sessionID, meta, routeProgressData(decision))

	s.executeRunnerInvoker().dispatchExecuteRun(runCtx, executeRunContext{
		req:      req,
		decision: decision,
		meta:     meta,
		out:      out,
		seq:      &seq,
		start:    start,
		sl:       sl,
		session:  sessionID,
	})
}

func (s *service) runVirtual(ctx context.Context, req ServiceExecuteRequest, decision RouteDecision, meta map[string]string, out chan<- ServiceEvent, seq *atomic.Int64, start time.Time, sl *serviceSessionLog, sessionID string) {
	progress := newSubprocessProgressState(req)
	emitProgress(out, seq, sl, sessionID, meta, progress.noteRequestStart())
	result := serviceimpl.RunVirtual(ctx, executeRunnerRequest(req, decision, meta, start))
	final := result.Final
	if result.EmitText {
		emitJSONRaw(out, seq, harnesses.EventTypeTextDelta, meta, harnesses.TextDeltaData{Text: result.Text})
	}
	if progressFinal := progress.noteResponseComplete(final); progressFinal != nil {
		emitProgress(out, seq, sl, sessionID, meta, *progressFinal)
	}
	s.recordRouteAttemptFromFinal(final)
	finalizeAndEmit(out, seq, meta, req, sl, final)
}

func (s *service) runScript(ctx context.Context, req ServiceExecuteRequest, decision RouteDecision, meta map[string]string, out chan<- ServiceEvent, seq *atomic.Int64, start time.Time, sl *serviceSessionLog, sessionID string) {
	progress := newSubprocessProgressState(req)
	emitProgress(out, seq, sl, sessionID, meta, progress.noteRequestStart())
	result := serviceimpl.RunScript(ctx, executeRunnerRequest(req, decision, meta, start))
	final := result.Final
	if result.EmitText {
		emitJSONRaw(out, seq, harnesses.EventTypeTextDelta, meta, harnesses.TextDeltaData{Text: result.Text})
	}
	if progressFinal := progress.noteResponseComplete(final); progressFinal != nil {
		emitProgress(out, seq, sl, sessionID, meta, *progressFinal)
	}
	s.recordRouteAttemptFromFinal(final)
	finalizeAndEmit(out, seq, meta, req, sl, final)
}

func executeRunnerRequest(req ServiceExecuteRequest, decision RouteDecision, meta map[string]string, start time.Time) serviceimpl.ExecuteRunnerRequest {
	return serviceimpl.ExecuteRunnerRequest{
		Prompt:   req.Prompt,
		Metadata: meta,
		Decision: serviceimpl.ExecuteRunnerDecision{
			Harness:        decision.Harness,
			Provider:       decision.Provider,
			ServerInstance: decision.ServerInstance,
			Model:          decision.Model,
		},
		Started: start,
	}
}

func nativeToolsForRequest(req ServiceExecuteRequest) []agentcore.Tool {
	if req.Tools != nil {
		return req.Tools
	}
	return tool.BuiltinToolsForPreset(req.WorkDir, req.ToolPreset, tool.BashOutputFilterConfig{})
}

// runNative drives the in-process agent loop (loop.go's Run). The provider
// is wrapped so per-HTTP timeouts fire independently of the request wall-clock
// cap.
func (s *service) runNative(ctx context.Context, req ServiceExecuteRequest, decision RouteDecision, meta map[string]string, out chan<- ServiceEvent, seq *atomic.Int64, start time.Time, sl *serviceSessionLog, sessionID string) {
	progress := newNativeProgressState()
	observeAgentEvent := func(ev agentcore.Event) {
		sl.writeEvent(ev)
		switch ev.Type {
		case agentcore.EventLLMRequest:
			var payload nativeLLMRequestPayload
			if err := json.Unmarshal(ev.Data, &payload); err == nil {
				emitProgress(out, seq, sl, sessionID, meta, progress.noteRequest(payload))
			}
		case agentcore.EventLLMResponse:
			var payload nativeLLMResponsePayload
			if err := json.Unmarshal(ev.Data, &payload); err == nil {
				emitProgress(out, seq, sl, sessionID, meta, progress.noteResponse(payload))
			}
		case agentcore.EventToolCall:
			var payload nativeToolCallPayload
			_ = json.Unmarshal(ev.Data, &payload)
			toolName := payload.Tool
			input := payload.Input
			if input == nil {
				if rawIn, err := json.Marshal(map[string]any{"tool": toolName}); err == nil {
					input = rawIn
				}
			}
			callID := fmt.Sprintf("call-%d", ev.Seq)
			_, completeProgress := progress.noteToolCall(callID, payload)
			emitJSONRaw(out, seq, harnesses.EventTypeToolCall, meta, harnesses.ToolCallData{
				ID:    callID,
				Name:  toolName,
				Input: input,
			})
			emitJSONRaw(out, seq, harnesses.EventTypeToolResult, meta, harnesses.ToolResultData{
				ID:         callID,
				Output:     payload.Output,
				Error:      payload.Error,
				DurationMS: payload.DurationMS,
			})
			emitProgress(out, seq, sl, sessionID, meta, completeProgress)
		case agentcore.EventCompactionEnd:
			var payload nativeCompactionPayload
			_ = json.Unmarshal(ev.Data, &payload)
			emitJSONRaw(out, seq, harnesses.EventTypeCompaction, meta, map[string]any{
				"messages_before": payload.MessagesBefore,
				"messages_after":  payload.MessagesAfter,
				"tokens_freed":    payload.TokensBefore - payload.TokensAfter,
			})
			compactionProgress, contextProgress := progress.noteCompaction(payload)
			emitProgress(out, seq, sl, sessionID, meta, compactionProgress)
			emitProgress(out, seq, sl, sessionID, meta, contextProgress)
		}
	}

	var stallMaxReadOnlyIterations *int
	if req.StallPolicy != nil {
		stallMaxReadOnlyIterations = &req.StallPolicy.MaxReadOnlyToolIterations
	}
	serviceimpl.RunNative(ctx, serviceimpl.NativeRequest{
		Prompt:                    req.Prompt,
		SystemPrompt:              req.SystemPrompt,
		Model:                     req.Model,
		Provider:                  req.Provider,
		Harness:                   req.Harness,
		WorkDir:                   req.WorkDir,
		Temperature:               req.Temperature,
		TopP:                      req.TopP,
		TopK:                      req.TopK,
		MinP:                      req.MinP,
		RepetitionPenalty:         req.RepetitionPenalty,
		Seed:                      req.Seed,
		SamplingSource:            req.SamplingSource,
		Reasoning:                 effectiveReasoning(req.Reasoning),
		NoStream:                  req.NoStream,
		Permissions:               req.Permissions,
		Tools:                     nativeToolsForRequest(req),
		ToolPreset:                req.ToolPreset,
		PlanningMode:              req.PlanningMode,
		MaxIterations:             req.MaxIterations,
		MaxTokens:                 req.MaxTokens,
		ReasoningByteLimit:        req.ReasoningByteLimit,
		ProviderTimeout:           req.ProviderTimeout,
		Timeout:                   req.Timeout,
		CachePolicy:               req.CachePolicy,
		CostCapUSD:                req.CostCapUSD,
		StallMaxReadOnlyIteration: stallMaxReadOnlyIterations,
		Metadata:                  meta,
		Decision:                  nativeDecision(decision),
		Started:                   start,
		SessionID:                 sessionID,
	}, serviceimpl.NativeCallbacks{
		ResolveProvider: func(nreq serviceimpl.NativeProviderRequest) serviceimpl.NativeProviderResolution {
			resolved := s.resolveNativeProvider(ServiceExecuteRequest{
				Provider: nreq.Provider,
				Harness:  nreq.Harness,
				Model:    nreq.Model,
			})
			return serviceimpl.NativeProviderResolution{
				Provider: resolved.Provider,
				Name:     resolved.Name,
				Model:    resolved.Entry.Model,
			}
		},
		ProviderNotConfiguredError: func(nreq serviceimpl.NativeProviderRequest, ndecision serviceimpl.NativeDecision) string {
			return s.nativeProviderNotConfiguredError(ServiceExecuteRequest{
				Provider: nreq.Provider,
				Harness:  nreq.Harness,
				Model:    nreq.Model,
			}, routeDecision(ndecision))
		},
		Compactor: func(model string) agentcore.Compactor {
			return newServiceCompactor(req, model)
		},
		ObserveAgentEvent: observeAgentEvent,
		EmitEvent: func(t harnesses.EventType, payload any) {
			emitJSONRaw(out, seq, t, meta, payload)
		},
		BeforeFinal: func(final harnesses.FinalData) {
			if progressFinal := progress.noteFinal(final); progressFinal != nil {
				emitProgress(out, seq, sl, sessionID, meta, *progressFinal)
			}
		},
		Finalize: func(final harnesses.FinalData) {
			s.recordRouteAttemptFromFinal(final)
			finalizeAndEmit(out, seq, meta, req, sl, final)
		},
		ToolWiringHook:          s.toolWiringHook(),
		PromptAssertionHook:     s.promptAssertionHook(),
		CompactionAssertionHook: s.compactionAssertionHook(),
		ObserveTokenUsage:       s.observeTokenUsage,
	})
}

func nativeDecision(decision RouteDecision) serviceimpl.NativeDecision {
	return serviceimpl.NativeDecision{
		Harness:        decision.Harness,
		Provider:       decision.Provider,
		ServerInstance: decision.ServerInstance,
		Model:          decision.Model,
		Candidates:     nativeRouteCandidates(decision.Candidates),
	}
}

func routeDecision(decision serviceimpl.NativeDecision) RouteDecision {
	return RouteDecision{
		Harness:        decision.Harness,
		Provider:       decision.Provider,
		ServerInstance: decision.ServerInstance,
		Model:          decision.Model,
	}
}

func nativeRouteCandidates(in []RouteCandidate) []serviceimpl.NativeRouteCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]serviceimpl.NativeRouteCandidate, len(in))
	for i, candidate := range in {
		out[i] = serviceimpl.NativeRouteCandidate{
			Provider:       candidate.Provider,
			Endpoint:       candidate.Endpoint,
			ServerInstance: candidate.ServerInstance,
			Model:          candidate.Model,
			Eligible:       candidate.Eligible,
		}
	}
	return out
}

func newServiceCompactor(req ServiceExecuteRequest, model string) agentcore.Compactor {
	cfg := compaction.DefaultConfig()
	if req.CompactionContextWindow > 0 {
		cfg.ContextWindow = req.CompactionContextWindow
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
	if catalog, err := modelcatalog.Default(); err == nil && catalog != nil && model != "" && req.CompactionContextWindow <= 0 {
		if contextWindow := catalog.ContextWindowForModel(model); contextWindow > 0 {
			cfg.ContextWindow = contextWindow
		}
	}
	return compaction.NewCompactor(cfg)
}

// runSubprocess delegates to a Runner under internal/harnesses/<name>. It
// re-uses the wall-clock-bounded ctx so PTY/orphan reaping is automatic
// when our ctx (which already carries the request Timeout) cancels.
func (s *service) runSubprocess(ctx context.Context, req ServiceExecuteRequest, decision RouteDecision, meta map[string]string, out chan<- ServiceEvent, seq *atomic.Int64, start time.Time, sl *serviceSessionLog, sessionID string, runner harnesses.Harness) {
	progress := newSubprocessProgressState(req)
	serviceimpl.RunSubprocess(ctx, serviceimpl.SubprocessRequest{
		Prompt:        req.Prompt,
		SystemPrompt:  req.SystemPrompt,
		WorkDir:       req.WorkDir,
		Permissions:   req.Permissions,
		Temperature:   req.Temperature,
		Seed:          req.Seed,
		Reasoning:     effectiveReasoning(req.Reasoning),
		Timeout:       req.Timeout,
		IdleTimeout:   req.IdleTimeout,
		SessionLogDir: req.SessionLogDir,
		Metadata:      meta,
		Decision: serviceimpl.ExecuteRunnerDecision{
			Harness:  decision.Harness,
			Provider: decision.Provider,
			Model:    decision.Model,
		},
		Started:        start,
		SessionLogPath: sessionLogPath(sl),
	}, runner, serviceimpl.SubprocessCallbacks{
		BeforeExecute: func() {
			emitProgress(out, seq, sl, sessionID, meta, progress.noteRequestStart())
		},
		BeforeFinal: func(final harnesses.FinalData) {
		},
		ObserveEvent: func(ev harnesses.Event) harnesses.Event {
			ev = progress.annotateToolResultDuration(ev)
			if payload, ok := progress.noteEvent(ev); ok && ev.Type != harnesses.EventTypeProgress {
				emitProgress(out, seq, sl, sessionID, meta, payload)
			}
			if ev.Type == harnesses.EventTypeFinal {
				if payload, ok := progress.noteFinal(ev); ok {
					emitProgress(out, seq, sl, sessionID, meta, payload)
				}
			}
			return ev
		},
		EmitEvent: func(ev harnesses.Event) bool {
			ev.Sequence = seq.Add(1) - 1
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		},
		Finalize: func(final harnesses.FinalData) {
			s.recordRouteAttemptFromFinal(final)
			finalizeAndEmit(out, seq, meta, req, sl, final)
		},
		WriteEnd: func(finalMeta map[string]string, final harnesses.FinalData) {
			sl.writeEnd(req, finalMeta, final)
		},
	})
}

func sessionLogPath(sl *serviceSessionLog) string {
	if sl == nil {
		return ""
	}
	return sl.path
}

// emitFinal wraps a FinalData into a ServiceEvent and writes it to out.
// The channel close happens in the caller via defer; this only writes the
// terminator event.
func emitFinal(out chan<- ServiceEvent, seq *atomic.Int64, meta map[string]string, final harnesses.FinalData) {
	raw, err := json.Marshal(final)
	if err != nil {
		raw = []byte(`{"status":"failed","error":"marshal final"}`)
	}
	ev := harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: seq.Add(1) - 1,
		Time:     time.Now().UTC(),
		Metadata: meta,
		Data:     raw,
	}
	select {
	case out <- ev:
	case <-time.After(time.Second):
	}
}

// emitFatalFinal is used when Execute itself can't construct a route. It
// writes a single failed final event then closes the channel — used for
// the "no consumer goroutine" path so we still satisfy the channel
// contract.
func emitFatalFinal(out chan<- ServiceEvent, meta map[string]string, status, errMsg string) {
	defer close(out)
	final := harnesses.FinalData{Status: status, Error: errMsg}
	raw, _ := json.Marshal(final)
	ev := harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: 0,
		Time:     time.Now().UTC(),
		Metadata: meta,
		Data:     raw,
	}
	select {
	case out <- ev:
	case <-time.After(time.Second):
	}
}

// emitJSON marshals payload and writes a typed event to out.
func emitJSON(out chan<- ServiceEvent, seq *atomic.Int64, t harnesses.EventType, meta map[string]string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := harnesses.Event{
		Type:     t,
		Sequence: seq.Add(1) - 1,
		Time:     time.Now().UTC(),
		Metadata: meta,
		Data:     raw,
	}
	select {
	case out <- ev:
	case <-time.After(time.Second):
	}
}

// emitJSONRaw is the typed-payload variant used inside the loop callback.
func emitJSONRaw(out chan<- ServiceEvent, seq *atomic.Int64, t harnesses.EventType, meta map[string]string, payload any) {
	emitJSON(out, seq, t, meta, payload)
}

// finalizeAndEmit stamps the service-owned session-log path onto final,
// records the terminal session.end event, and forwards the final to the
// public event stream. Every terminal emit path in runExecute funnels
// through this helper so the session log and the event channel stay in
// lock-step (CONTRACT-003).
//
// finalizeAndEmit also performs the CONTRACT-003 echo of top-level Role
// and CorrelationID into the final event Metadata (top-level wins over
// any caller-supplied Metadata entry under the same reserved key) and
// stamps RoutingActual.Power from the catalog projection of the
// actually-dispatched Model. When the caller set both top-level
// Role/CorrelationID and the same reserved Metadata key, a
// MetadataKeyCollision warning is appended to final.Warnings.
func finalizeAndEmit(out chan<- ServiceEvent, seq *atomic.Int64, meta map[string]string, req ServiceExecuteRequest, sl *serviceSessionLog, final harnesses.FinalData) {
	if sl != nil && sl.path != "" {
		final.SessionLogPath = sl.path
	}
	// Stamp catalog power onto the actually-dispatched RoutingActual.
	// final.RoutingActual is set by the caller (one per terminal path);
	// when nil we leave it nil to avoid synthesizing routing evidence.
	if final.RoutingActual != nil && final.RoutingActual.Power == 0 {
		final.RoutingActual.Power = catalogPowerForModel(serviceRoutingCatalog(), final.RoutingActual.Model)
	}
	// Detect reserved metadata-key collisions and append a warning so the
	// caller learns when their caller-supplied Metadata entries were
	// overridden by the top-level Role / CorrelationID fields.
	if collisions := metadataReservedKeyCollisions(req.Metadata, req.Role, req.CorrelationID); len(collisions) > 0 {
		final.Warnings = append(final.Warnings, harnesses.FinalWarning{
			Code:    MetadataWarningCodeKeyCollision,
			Message: metadataKeyCollisionMessage(collisions),
		})
	}
	// Echo Role + CorrelationID onto the final event Metadata.
	finalMeta := metaWithRoleAndCorrelation(meta, req.Role, req.CorrelationID)
	if sl != nil {
		if ovr := sl.override; ovr != nil && !ovr.emitted.Load() {
			sl.writeOverrideEvent(ServiceEventTypeOverride, ovr.payload)
		}
		sl.writeEnd(req, finalMeta, final)
	}
	emitFinal(out, seq, finalMeta, final)
}
