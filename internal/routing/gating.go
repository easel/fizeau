package routing

import (
	"fmt"
	"strings"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/reasoning"
)

// Capabilities describes what a (harness, provider, model) tuple can do.
// Populated from harness config + catalog metadata + provider discovery.
type Capabilities struct {
	ContextWindow      int      // resolved tokens; 0 = unknown
	SupportsTools      bool     // supports tool/function calling
	SupportsStreaming  bool     // supports streaming responses
	SupportedReasoning []string // supported public reasoning values
	MaxReasoningTokens int      // 0 means numeric reasoning is unsupported/unknown
	SupportedPerms     []string // {"safe","supervised","unrestricted"} subset
	ExactPinSupport    bool     // accepts exact concrete model pins
	SupportedModels    []string // nil = no static allow-list
}

// HasReasoning returns true if the candidate supports the requested reasoning
// value. Empty, auto, off, and numeric 0 impose no requirement.
func (c Capabilities) HasReasoning(value string) bool {
	policy, err := reasoning.ParseString(value)
	if err != nil {
		return false
	}
	switch policy.Kind {
	case reasoning.KindUnset, reasoning.KindAuto, reasoning.KindOff:
		return true
	case reasoning.KindTokens:
		return c.MaxReasoningTokens > 0 && policy.Tokens <= c.MaxReasoningTokens
	case reasoning.KindNamed:
		for _, supported := range c.SupportedReasoning {
			normalized, err := reasoning.Normalize(supported)
			if err == nil && normalized == policy.Value {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// HasPermissions returns true if the candidate supports the requested level.
// An empty permission level always returns true.
func (c Capabilities) HasPermissions(perm string) bool {
	if perm == "" {
		return true
	}
	for _, p := range c.SupportedPerms {
		if strings.EqualFold(p, perm) {
			return true
		}
	}
	return false
}

// HasModel returns true when the exact model pin is within the static
// harness allow-list. A nil allow-list means the harness delegates validation
// to provider-side model checks.
func (c Capabilities) HasModel(model string) bool {
	if c.SupportedModels == nil || model == "" {
		return true
	}
	for _, supported := range c.SupportedModels {
		if supported == model {
			return true
		}
	}
	return false
}

// CheckGating applies all capability gates against a request and returns
// the first failure reason (free-form string for diagnostics) plus the
// typed FilterReason category for the failure. Returns ("", FilterReasonEligible)
// when all gates pass.
//
// The typed return is the authoritative classification — callers must not
// re-classify by parsing the string. The string is for human-readable
// diagnostics only.
//
// Fixes ddx-4817edfd subtree: pre-dispatch capability check covering
// context window, tool support, effort, permissions.
func CheckGating(cap Capabilities, req Request) (string, FilterReason) {
	// Context window gating: if the request declares prompt size or an effort
	// that implies a minimum context, reject candidates that can't fit.
	minCtx := req.MinContextWindow()
	if minCtx > 0 {
		if cap.ContextWindow <= 0 {
			return fmt.Sprintf("context window unknown < required %d", minCtx), FilterReasonContextTooSmall
		}
		if cap.ContextWindow < minCtx {
			return fmt.Sprintf("context window %d < required %d", cap.ContextWindow, minCtx), FilterReasonContextTooSmall
		}
	}

	// Tool-calling support gating.
	if req.RequiresTools && !cap.SupportsTools {
		return "tool calling not supported", FilterReasonNoToolSupport
	}

	if !cap.HasReasoning(req.Reasoning) {
		return fmt.Sprintf("reasoning %q not supported", req.Reasoning), FilterReasonReasoningUnsupported
	}

	// Permissions support gating.
	if !cap.HasPermissions(req.Permissions) {
		return fmt.Sprintf("permissions %q not supported", req.Permissions), FilterReasonScoredBelowTop
	}

	if req.Model != "" && !cap.HasModel(req.Model) {
		return "model not in harness allow-list", FilterReasonScoredBelowTop
	}

	// Exact-pin gating: an explicit Model field requires ExactPinSupport.
	if req.Model != "" && !cap.ExactPinSupport {
		return "exact model pin not supported", FilterReasonScoredBelowTop
	}

	return "", FilterReasonEligible
}

// CheckPowerEligibility applies catalog-power gates for automatic model
// selection. Explicit Model and Provider pins bypass this gate so caller-
// chosen routes are never broadened or overridden by power policy.
//
// A bare Harness pin is likewise an explicit operator constraint and must NOT
// require an accompanying model, policy, or power flag: pinning --harness X
// means "route within harness X", so when no policy and no power bound are
// requested the gate is bypassed and the harness resolves its own model
// (catalog power may be unknown, e.g. a subscription TUI whose discovery
// reports only the current model). When the caller DID supply a Policy or a
// MinPower/MaxPower bound alongside the harness pin, that bound still filters
// the harness's models, so we fall through to normal power gating.
func CheckPowerEligibility(lookup func(string) (ModelEligibility, bool), model string, req Request) (string, FilterReason) {
	if req.Model != "" || req.Provider != "" || lookup == nil {
		return "", FilterReasonEligible
	}
	if req.Harness != "" && req.Policy == "" && req.MinPower == 0 && req.MaxPower == 0 {
		return "", FilterReasonEligible
	}
	if model == "" {
		return "model has no catalog power metadata", FilterReasonPowerMissing
	}
	eligibility, ok := lookup(model)
	if !ok {
		if req.MinPower > 0 || req.MaxPower > 0 {
			return fmt.Sprintf("model %q has no catalog power metadata", model), FilterReasonPowerMissing
		}
		return "", FilterReasonEligible
	}
	if eligibility.ExactPinOnly {
		return fmt.Sprintf("model %q is exact-pin-only", model), FilterReasonExactPinOnly
	}
	if !eligibility.AutoRoutable {
		if eligibility.Power <= 0 {
			return fmt.Sprintf("model %q has no catalog power metadata", model), FilterReasonPowerMissing
		}
		return fmt.Sprintf("model %q is not auto-routable", model), FilterReasonNotAutoRoutable
	}
	return "", FilterReasonEligible
}

// CheckProviderDefaultEligibility returns a rejection reason when the provider
// entry has ExcludeFromDefaultRouting=true and the request does not carry an
// explicit provider, harness, or exact model pin. Any explicit hard pin
// bypasses this gate so the operator can still reach opt-out providers
// intentionally.
func CheckProviderDefaultEligibility(providerName string, actualCashSpend bool, billing modelcatalog.BillingModel, excluded bool, req Request) (string, FilterReason) {
	if !excluded {
		return "", FilterReasonEligible
	}
	if req.Provider != "" || req.Harness != "" || req.Model != "" {
		return "", FilterReasonEligible
	}
	if actualCashSpend || billing == modelcatalog.BillingModelPerToken {
		return fmt.Sprintf("pay-per-token provider %s requires metered opt-in for unpinned automatic routing", providerName), FilterReasonMeteredOptInRequired
	}
	return fmt.Sprintf("provider %s excluded from default routing (include_by_default=false)", providerName), FilterReasonProviderExcludedFromDefault
}

// resolveRequestReasoning returns a copy of req with Reasoning resolved to the
// catalog's reasoning default for the request's policy/surface
// when the request asks for Reasoning=auto. This must run before CheckGating
// so candidates that only support a different reasoning level (e.g. an
// off-only variant under a policy whose surface default is "high") are
// correctly disqualified by the capability gate. Other Reasoning values
// (unset, off, named, numeric) are left untouched, preserving the existing
// behavior of those code paths.
func resolveRequestReasoning(req Request, surface string, resolver func(policy, surface string) (string, bool)) Request {
	if resolver == nil || req.Policy == "" {
		return req
	}
	policy, err := reasoning.ParseString(req.Reasoning)
	if err != nil || policy.Kind != reasoning.KindAuto {
		return req
	}
	resolved, ok := resolver(req.Policy, surface)
	if !ok || resolved == "" {
		return req
	}
	out := req
	out.Reasoning = resolved
	return out
}
