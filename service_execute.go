package fizeau

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
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
	if err := validateServiceExecuteRequest(req); err != nil {
		return nil, err
	}

	// Generate a session ID and register it in the hub so TailSessionLog
	// callers can subscribe before or during execution.
	sessionID := generateSessionID()
	s.hub.OpenSession(sessionID)
	coordinator := serviceimpl.ExecuteCoordinator{Hub: s.hub, Registry: s.registry}

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
	decision, err := s.resolveExecuteRouteContext(ctx, req)
	if err != nil {
		// NoViableProviderForNow is a transient quota signal — DDx
		// callers pause their drain loop on RetryAfter and resume.
		// Surface it directly (not via the fatal-final channel) so the
		// typed error reaches errors.As without log scraping.
		var quotaErr *NoViableProviderForNow
		if errors.As(err, &quotaErr) {
			s.hub.CloseSession(sessionID, ServiceEvent{})
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
					s.hub.BroadcastEvent(sessionID, rejectedEv)
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
			s.hub.CloseSession(sessionID, ServiceEvent{})
			return nil, pinErr
		}
		return coordinator.RoutingFailure(sessionID, req.Metadata, err.Error()), nil
	}

	return coordinator.RunResolved(
		ctx,
		s.executeCoordinatorRequest(req, *decision, sessionID, overrideCtx),
		s.executeCoordinatorPorts(req, *decision, sessionID, overrideCtx),
	), nil
}

// validateServiceExecuteRequest is the side-effect-free public Execute
// boundary. Keep validation order stable: callers rely on the first error, and
// Continue must apply the same rules to an effective fresh request without
// opening routing, session, log, or process state.
func validateServiceExecuteRequest(req ServiceExecuteRequest) error {
	if err := validateMaxTokens(req.MaxTokens); err != nil {
		return err
	}
	// Boundary validation: reject unknown CachePolicy values before any
	// session state is opened or events are emitted. Beads C/D consume this
	// field; an unknown value is a caller programming error.
	if err := ValidateCachePolicy(req.CachePolicy); err != nil {
		return err
	}
	if err := ValidatePowerBounds(req.MinPower, req.MaxPower); err != nil {
		return err
	}
	if err := ValidateRole(req.Role); err != nil {
		return err
	}
	if err := ValidateCorrelationID(req.CorrelationID); err != nil {
		return err
	}
	return nil
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
	return s.resolveExecuteRouteInternal(ctx, req)
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

func validateExplicitHarnessModel(name string, cfg harnesses.HarnessConfig, model, provider string) error {
	if model == "" || cfg.TestOnly || cfg.IsHTTPProvider || name == "fiz" {
		return nil
	}
	if modelSupportedForHarness(name, cfg, model, provider) {
		return nil
	}
	supportedModels := supportedModelsForHarness(name, cfg, serviceRoutingCatalog())
	return &ErrHarnessModelIncompatible{
		Harness:         name,
		Model:           model,
		SupportedModels: append([]string(nil), supportedModels...),
	}
}

func modelSupportedForHarness(name string, cfg harnesses.HarnessConfig, model, provider string) bool {
	for _, known := range supportedModelsForHarness(name, cfg, serviceRoutingCatalog()) {
		if model == known {
			return true
		}
	}
	switch name {
	case "codex":
		return strings.HasPrefix(model, "gpt-")
	case "claude", "claude-tui":
		return false
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
	normalizedModel := resolveSubprocessModelAliasWithCatalog(canonical, decision.Model, serviceRoutingCatalog())
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

func harnessSource(req ServiceExecuteRequest) string {
	if strings.TrimSpace(req.Harness) != "" {
		return "request_harness"
	}
	return "auto_route"
}
