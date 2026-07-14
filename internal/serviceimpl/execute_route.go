package serviceimpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	quotaimpl "github.com/easel/fizeau/internal/quota"
	"github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/routing"
)

const defaultExecuteQuotaRecoveryFallback = 5 * time.Minute

// ExecuteRouteRequest is the API-neutral subset of a public execute request
// used to resolve its concrete route.
type ExecuteRouteRequest struct {
	Harness   string
	Provider  string
	Model     string
	Policy    string
	Reasoning string
	MinPower  int
}

// ExecuteRouteDecision is the API-neutral route projection needed by the
// execute coordinator. Root adapters retain any additional public routing
// evidence returned by the routing engine and apply these normalized fields.
type ExecuteRouteDecision struct {
	Harness        string
	Provider       string
	ServerInstance string
	Endpoint       string
	Model          string
	Reason         string
	Power          int
}

// ExecuteHarnessLookup is the narrow registry surface used during explicit
// route validation.
type ExecuteHarnessLookup interface {
	Get(name string) (harnesses.HarnessConfig, bool)
}

// ExecuteRouteQuota is the route resolver's neutral view of subscription
// quota. Missing, stale, and unavailable observations intentionally fail open.
type ExecuteRouteQuota struct {
	OK      bool
	Present bool
	Fresh   bool
	Windows []harnesses.QuotaWindow
}

// ExecuteRouteInput supplies service-owned state and replaceable discovery
// seams without importing the root public package.
type ExecuteRouteInput struct {
	Request ExecuteRouteRequest

	Harnesses        ExecuteHarnessLookup
	Providers        map[string]ProviderEntry
	ProviderNames    []string
	HasServiceConfig bool
	Catalog          *modelcatalog.Catalog

	// ResolveWithEngine is called for an unpinned harness, or for an
	// auto-routing-eligible harness whose model is intentionally omitted.
	ResolveWithEngine func(context.Context) (ExecuteRouteDecision, error)
	// PreserveEngineError identifies public explicit-pin errors that must pass
	// through without the historical "ResolveRoute: " wrapper.
	PreserveEngineError func(error) bool

	// These optional seams preserve the root package's hermetic discovery
	// overrides. Production callers may omit them to use internal defaults.
	DiscoverModels    func(string, harnesses.HarnessConfig) []string
	ResolveModelAlias func(harness, model string) string
	QuotaForHarness   func(string, time.Time) (ExecuteRouteQuota, bool)
	Now               func() time.Time

	QuotaRecoveryFallback time.Duration
}

// ExecuteRouteFailureKind identifies a failure without depending on root
// public error types. The root facade projects structured kinds back onto its
// compatibility errors.
type ExecuteRouteFailureKind string

const (
	ExecuteRouteFailureUnknownHarness            ExecuteRouteFailureKind = "unknown_harness"
	ExecuteRouteFailurePolicyRequirement         ExecuteRouteFailureKind = "policy_requirement_unsatisfied"
	ExecuteRouteFailureUnknownProvider           ExecuteRouteFailureKind = "unknown_provider"
	ExecuteRouteFailureHarnessModelIncompatible  ExecuteRouteFailureKind = "harness_model_incompatible"
	ExecuteRouteFailureUnsupportedReasoning      ExecuteRouteFailureKind = "unsupported_reasoning"
	ExecuteRouteFailureQuotaUnavailable          ExecuteRouteFailureKind = "quota_unavailable"
	ExecuteRouteFailureUnderSpecified            ExecuteRouteFailureKind = "under_specified"
	ExecuteRouteFailureAutoResolutionUnavailable ExecuteRouteFailureKind = "auto_resolution_unavailable"
	ExecuteRouteFailureUnsatisfiablePin          ExecuteRouteFailureKind = "unsatisfiable_pin"
	ExecuteRouteFailureEngine                    ExecuteRouteFailureKind = "engine"
	ExecuteRouteFailureEngineUnavailable         ExecuteRouteFailureKind = "engine_unavailable"
)

// ExecuteRouteFailure carries every field needed to reconstruct current root
// typed errors while retaining engine error identity through Cause.
type ExecuteRouteFailure struct {
	Kind    ExecuteRouteFailureKind
	Message string
	Cause   error

	Harness   string
	Provider  string
	Model     string
	Policy    string
	Reasoning string

	Requirement        string
	AttemptedPin       string
	KnownProviders     []string
	SupportedModels    []string
	RetryAfter         time.Time
	ExhaustedProviders []string
	Pin                string
	Reason             string

	// EngineErrorPassthrough is true when Cause is an explicit-pin error that
	// must retain its original public identity and message without wrapping.
	EngineErrorPassthrough bool
}

func (f *ExecuteRouteFailure) Error() string {
	if f == nil {
		return ""
	}
	if f.Message != "" {
		return f.Message
	}
	if f.Cause != nil {
		return f.Cause.Error()
	}
	return string(f.Kind)
}

func (f *ExecuteRouteFailure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

// ResolveExecuteRoute canonicalizes and validates explicit execute pins. It
// delegates only the routing-engine decision to ResolveWithEngine, then
// validates and normalizes that decision before returning it.
func ResolveExecuteRoute(ctx context.Context, input ExecuteRouteInput) (ExecuteRouteDecision, *ExecuteRouteFailure) {
	req := input.Request
	if req.Harness == "" {
		return resolveExecuteRouteWithEngine(ctx, input)
	}

	canonical := harnesses.ResolveHarnessAlias(req.Harness)
	cfg, ok := executeHarnessConfig(input.Harnesses, canonical)
	if !ok {
		return ExecuteRouteDecision{}, &ExecuteRouteFailure{
			Kind:    ExecuteRouteFailureUnknownHarness,
			Message: fmt.Sprintf("unknown harness %q", req.Harness),
			Harness: req.Harness,
		}
	}

	if failure := validateExecutePolicy(canonical, cfg, req.Policy); failure != nil {
		return ExecuteRouteDecision{}, failure
	}
	if failure := validateExecuteProvider(input, cfg, req.Provider); failure != nil {
		return ExecuteRouteDecision{}, failure
	}
	if failure := validateExecuteModel(input, canonical, cfg, req.Model, req.Provider); failure != nil {
		return ExecuteRouteDecision{}, failure
	}
	if failure := validateExecuteReasoning(canonical, cfg, req.Reasoning); failure != nil {
		return ExecuteRouteDecision{}, failure
	}
	if failure := validateExecuteQuota(input, canonical, cfg); failure != nil {
		return ExecuteRouteDecision{}, failure
	}

	if !cfg.TestOnly && !cfg.IsHTTPProvider && canonical != "fiz" && req.Model == "" {
		if req.Policy == "" && req.MinPower == 0 {
			return ExecuteRouteDecision{}, &ExecuteRouteFailure{
				Kind:    ExecuteRouteFailureUnderSpecified,
				Message: fmt.Sprintf("under-specified routing for harness=%q: supply --model, --policy, or --min-power", canonical),
				Harness: canonical,
			}
		}
		if !cfg.AutoRoutingEligible {
			return ExecuteRouteDecision{}, &ExecuteRouteFailure{
				Kind:    ExecuteRouteFailureAutoResolutionUnavailable,
				Message: fmt.Sprintf("no auto-resolution available for harness=%q: harness does not support auto-routing; supply an explicit --model", canonical),
				Harness: canonical,
			}
		}
		return resolveExecuteRouteWithEngine(ctx, input)
	}

	resolvedModel := resolveExecuteModelAlias(input, canonical, req.Model)
	decision := ExecuteRouteDecision{
		Harness:        canonical,
		Provider:       req.Provider,
		ServerInstance: executeProviderServerInstance(input, req.Provider),
		Model:          resolvedModel,
		Reason:         "explicit",
		Power:          CatalogPowerForModel(input.Catalog, resolvedModel),
	}
	decision.Endpoint = executeDecisionEndpoint(decision.Endpoint, decision.Provider)
	return decision, nil
}

func resolveExecuteRouteWithEngine(ctx context.Context, input ExecuteRouteInput) (ExecuteRouteDecision, *ExecuteRouteFailure) {
	if input.ResolveWithEngine == nil {
		return ExecuteRouteDecision{}, &ExecuteRouteFailure{
			Kind:    ExecuteRouteFailureEngineUnavailable,
			Message: "execute route engine callback is not configured",
		}
	}
	decision, err := input.ResolveWithEngine(ctx)
	if err != nil {
		passthrough := input.PreserveEngineError != nil && input.PreserveEngineError(err)
		message := err.Error()
		if !passthrough {
			message = fmt.Sprintf("ResolveRoute: %v", err)
		}
		return ExecuteRouteDecision{}, &ExecuteRouteFailure{
			Kind:                   ExecuteRouteFailureEngine,
			Message:                message,
			Cause:                  err,
			EngineErrorPassthrough: passthrough,
		}
	}
	if failure := validateEngineExecuteDecision(input, &decision); failure != nil {
		return ExecuteRouteDecision{}, failure
	}
	return decision, nil
}

func validateEngineExecuteDecision(input ExecuteRouteInput, decision *ExecuteRouteDecision) *ExecuteRouteFailure {
	req := input.Request
	if decision == nil || strings.TrimSpace(req.Harness) == "" || strings.TrimSpace(req.Model) != "" {
		return nil
	}
	canonical := harnesses.ResolveHarnessAlias(req.Harness)
	if decision.Harness != "" && decision.Harness != canonical {
		pin := "harness=" + canonical
		reason := "routing engine returned a different harness"
		return &ExecuteRouteFailure{
			Kind:    ExecuteRouteFailureUnsatisfiablePin,
			Message: fmt.Sprintf("unsatisfiable pin %s: %s", pin, reason),
			Harness: canonical,
			Pin:     pin,
			Reason:  reason,
		}
	}
	cfg, ok := executeHarnessConfig(input.Harnesses, canonical)
	if !ok {
		return &ExecuteRouteFailure{
			Kind:    ExecuteRouteFailureUnknownHarness,
			Message: fmt.Sprintf("unknown harness %q", req.Harness),
			Harness: req.Harness,
		}
	}
	normalizedModel := resolveExecuteModelAlias(input, canonical, decision.Model)
	if failure := validateExecuteModel(input, canonical, cfg, normalizedModel, decision.Provider); failure != nil {
		return failure
	}
	decision.Harness = canonical
	decision.Model = normalizedModel
	decision.Endpoint = executeDecisionEndpoint(decision.Endpoint, decision.Provider)
	return nil
}

func executeHarnessConfig(registry ExecuteHarnessLookup, name string) (harnesses.HarnessConfig, bool) {
	if registry == nil {
		return harnesses.HarnessConfig{}, false
	}
	return registry.Get(name)
}

func validateExecutePolicy(name string, cfg harnesses.HarnessConfig, policy string) *ExecuteRouteFailure {
	constraint, ok := executePolicyConstraint(policy)
	if !ok {
		return nil
	}
	billing := HarnessPaymentKind(name, cfg)
	unsatisfied := constraint == routing.ProviderPreferenceLocalOnly && billing != modelcatalog.BillingModelFixed
	unsatisfied = unsatisfied || constraint == routing.ProviderPreferenceSubscriptionOnly && billing != modelcatalog.BillingModelSubscription
	if !unsatisfied {
		return nil
	}
	attemptedPin := "Harness=" + name
	return &ExecuteRouteFailure{
		Kind:         ExecuteRouteFailurePolicyRequirement,
		Message:      fmt.Sprintf("policy %q requires %s but conflicts with %s", policy, constraint, attemptedPin),
		Harness:      name,
		Policy:       policy,
		Requirement:  constraint,
		AttemptedPin: attemptedPin,
	}
}

func executePolicyConstraint(policy string) (string, bool) {
	switch policy {
	case "air-gapped":
		return routing.ProviderPreferenceLocalOnly, true
	case "smart":
		return routing.ProviderPreferenceSubscriptionOnly, true
	default:
		return "", false
	}
}

func validateExecuteProvider(input ExecuteRouteInput, cfg harnesses.HarnessConfig, provider string) *ExecuteRouteFailure {
	if provider == "" || !input.HasServiceConfig || cfg.TestOnly {
		return nil
	}
	lookup := provider
	if base, _, ok := splitExecuteEndpointProviderRef(provider); ok {
		lookup = base
	}
	if _, ok := input.Providers[lookup]; ok {
		return nil
	}
	known := append([]string(nil), input.ProviderNames...)
	message := fmt.Sprintf("unknown provider %q", provider)
	if len(known) > 0 {
		message += "; known providers: " + strings.Join(known, ", ")
	}
	return &ExecuteRouteFailure{
		Kind:           ExecuteRouteFailureUnknownProvider,
		Message:        message,
		Provider:       provider,
		KnownProviders: known,
	}
}

func validateExecuteModel(input ExecuteRouteInput, name string, cfg harnesses.HarnessConfig, model, provider string) *ExecuteRouteFailure {
	if model == "" || cfg.TestOnly || cfg.IsHTTPProvider || name == "fiz" {
		return nil
	}
	supported := executeSupportedModels(input, name, cfg)
	if executeModelSupported(name, model, provider, supported) {
		return nil
	}
	return &ExecuteRouteFailure{
		Kind:            ExecuteRouteFailureHarnessModelIncompatible,
		Message:         fmt.Sprintf("model %q is not supported by harness %q; supported models: %s", model, name, strings.Join(supported, ", ")),
		Harness:         name,
		Model:           model,
		SupportedModels: append([]string(nil), supported...),
	}
}

func executeModelSupported(name, model, provider string, supported []string) bool {
	for _, known := range supported {
		if model == known {
			return true
		}
	}
	switch name {
	case "codex":
		return strings.HasPrefix(model, "gpt-")
	case "claude", "claude-tui":
		return false
	case "pi", "opencode":
		return provider != ""
	default:
		return false
	}
}

func validateExecuteReasoning(name string, cfg harnesses.HarnessConfig, value string) *ExecuteRouteFailure {
	if cfg.TestOnly || len(cfg.ReasoningLevels) == 0 && cfg.MaxReasoningTokens <= 0 {
		return nil
	}
	policy, err := reasoning.ParseString(value)
	if err != nil {
		return &ExecuteRouteFailure{
			Kind:      ExecuteRouteFailureUnsupportedReasoning,
			Message:   fmt.Sprintf("unsupported reasoning %q for harness %q: %v", value, name, err),
			Cause:     err,
			Harness:   name,
			Reasoning: value,
		}
	}
	switch policy.Kind {
	case reasoning.KindUnset, reasoning.KindAuto, reasoning.KindOff:
		return nil
	case reasoning.KindTokens:
		if cfg.MaxReasoningTokens <= 0 {
			return unsupportedExecuteReasoning(name, value, fmt.Sprintf("unsupported reasoning %q for harness %q; token budgets are not supported", value, name))
		}
		if policy.Tokens > cfg.MaxReasoningTokens {
			return unsupportedExecuteReasoning(name, value, fmt.Sprintf("unsupported reasoning %q for harness %q; max token budget is %d", value, name, cfg.MaxReasoningTokens))
		}
		return nil
	case reasoning.KindNamed:
		for _, supported := range cfg.ReasoningLevels {
			if string(policy.Value) == supported {
				return nil
			}
		}
		return unsupportedExecuteReasoning(name, value, fmt.Sprintf("unsupported reasoning %q for harness %q; supported reasoning: %s", value, name, strings.Join(cfg.ReasoningLevels, ", ")))
	default:
		return unsupportedExecuteReasoning(name, value, fmt.Sprintf("unsupported reasoning %q for harness %q", value, name))
	}
}

func unsupportedExecuteReasoning(name, value, message string) *ExecuteRouteFailure {
	return &ExecuteRouteFailure{
		Kind:      ExecuteRouteFailureUnsupportedReasoning,
		Message:   message,
		Harness:   name,
		Reasoning: value,
	}
}

func validateExecuteQuota(input ExecuteRouteInput, name string, cfg harnesses.HarnessConfig) *ExecuteRouteFailure {
	if HarnessPaymentKind(name, cfg) != modelcatalog.BillingModelSubscription {
		return nil
	}
	now := time.Now()
	if input.Now != nil {
		now = input.Now()
	}
	quotaForHarness := input.QuotaForHarness
	if quotaForHarness == nil {
		quotaForHarness = defaultExecuteQuotaForHarness
	}
	qs, ok := quotaForHarness(name, now)
	if !ok || !qs.Present || !qs.Fresh || qs.OK {
		return nil
	}
	retryAfter := earliestExecuteQuotaResetAfter(qs.Windows, now)
	if retryAfter.IsZero() {
		fallback := input.QuotaRecoveryFallback
		if fallback <= 0 {
			fallback = defaultExecuteQuotaRecoveryFallback
		}
		retryAfter = now.Add(fallback)
	}
	exhausted := []string{name}
	message := fmt.Sprintf("no viable provider right now: %s quota-exhausted (retry after %s)", name, retryAfter.Format(time.RFC3339))
	return &ExecuteRouteFailure{
		Kind:               ExecuteRouteFailureQuotaUnavailable,
		Message:            message,
		Harness:            name,
		RetryAfter:         retryAfter,
		ExhaustedProviders: exhausted,
	}
}

func defaultExecuteQuotaForHarness(name string, now time.Time) (ExecuteRouteQuota, bool) {
	view, ok := quotaimpl.SubscriptionForHarness(name, now)
	return ExecuteRouteQuota{
		OK:      view.OK,
		Present: view.Present,
		Fresh:   view.Fresh,
		Windows: append([]harnesses.QuotaWindow(nil), view.Windows...),
	}, ok
}

func earliestExecuteQuotaResetAfter(windows []harnesses.QuotaWindow, now time.Time) time.Time {
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

func executeSupportedModels(input ExecuteRouteInput, name string, cfg harnesses.HarnessConfig) []string {
	models := make([]string, 0)
	models = AppendUniqueModelIDs(models, executeCatalogModelsForHarness(name, cfg, input.Catalog)...)
	if cfg.DefaultModel != "" {
		models = AppendUniqueModelIDs(models, cfg.DefaultModel)
	}
	discover := input.DiscoverModels
	if discover == nil {
		discover = SubprocessHarnessModelIDs
	}
	models = AppendUniqueModelIDs(models, discover(name, cfg)...)
	models = AppendUniqueModelIDs(models, executeStaticHarnessAliases(name)...)
	if len(models) == 0 {
		return nil
	}
	return models
}

func executeCatalogModelsForHarness(name string, cfg harnesses.HarnessConfig, cat *modelcatalog.Catalog) []string {
	if name == "fiz" {
		return nil
	}
	surface := cfg.Surface
	if surface == "" {
		switch name {
		case "codex":
			surface = "codex"
		case "claude", "claude-tui":
			surface = "claude"
		case "gemini", "pi":
			surface = "gemini"
		case "opencode":
			surface = "embedded-openai"
		}
	}
	if name == "pi" {
		surface = "gemini"
	}
	models := AppendUniqueModelIDs(nil, CatalogTierModelsForHarnessSurface(cat, surface)...)
	if surface == "claude" {
		for _, model := range models {
			if strings.HasPrefix(model, "opus-") || strings.HasPrefix(model, "sonnet-") ||
				strings.HasPrefix(model, "haiku-") || strings.HasPrefix(model, "fable-") {
				models = AppendUniqueModelIDs(models, "claude-"+model)
			}
		}
	}
	return models
}

func executeStaticHarnessAliases(name string) []string {
	switch name {
	case "codex":
		return []string{"gpt"}
	case "claude", "claude-tui":
		return []string{"claude-opus-4-6", "opus", "sonnet", "haiku", "fable", "fable-1.0"}
	case "gemini":
		return []string{"gemini", "gemini-2.5"}
	default:
		return nil
	}
}

func resolveExecuteModelAlias(input ExecuteRouteInput, harness, model string) string {
	resolve := input.ResolveModelAlias
	if resolve == nil {
		resolve = ResolveSubprocessModelAlias
	}
	resolved := resolve(harness, model)
	if resolved != model {
		return resolved
	}
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "codex":
		if strings.EqualFold(strings.TrimSpace(model), "gpt") {
			if tiers := CatalogTierModelsForHarnessSurface(input.Catalog, "codex"); len(tiers) > 0 {
				return tiers[0]
			}
		}
	case "gemini":
		normalized := strings.ToLower(strings.TrimSpace(model))
		if normalized == "gemini" || normalized == "gemini-2.5" {
			if tiers := CatalogTierModelsForHarnessSurface(input.Catalog, "gemini"); len(tiers) > 0 {
				return tiers[0]
			}
		}
	}
	return resolved
}

func executeProviderServerInstance(input ExecuteRouteInput, provider string) string {
	if !input.HasServiceConfig || strings.TrimSpace(provider) == "" {
		return ""
	}
	if providerName, endpointName, ok := splitExecuteEndpointProviderRef(provider); ok {
		if entry, exists := input.Providers[providerName]; exists {
			for _, endpoint := range ModelDiscoveryEndpoints(entry) {
				if endpoint.Name == endpointName {
					return strings.TrimSpace(endpoint.ServerInstance)
				}
			}
		}
	}
	entry, ok := input.Providers[strings.TrimSpace(provider)]
	if !ok {
		return ""
	}
	return strings.TrimSpace(entry.ServerInstance)
}

func executeDecisionEndpoint(endpoint, provider string) string {
	if endpoint != "" {
		return endpoint
	}
	_, endpoint, _ = splitExecuteEndpointProviderRef(provider)
	return endpoint
}

func splitExecuteEndpointProviderRef(ref string) (string, string, bool) {
	provider, endpoint, ok := strings.Cut(ref, "@")
	if !ok || provider == "" || endpoint == "" {
		return "", "", false
	}
	return provider, endpoint, true
}
