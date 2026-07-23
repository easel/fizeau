package serviceimpl

import (
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
)

// HarnessCapabilityStatus is the API-neutral classification used while
// assembling harness metadata. The public package projects these values onto
// its stable HarnessCapabilityStatus contract.
type HarnessCapabilityStatus string

const (
	HarnessCapabilityRequired      HarnessCapabilityStatus = "required"
	HarnessCapabilityOptional      HarnessCapabilityStatus = "optional"
	HarnessCapabilityUnsupported   HarnessCapabilityStatus = "unsupported"
	HarnessCapabilityNotApplicable HarnessCapabilityStatus = "not_applicable"
)

// HarnessCapability describes one internally classified capability.
type HarnessCapability struct {
	Status HarnessCapabilityStatus
	Detail string
}

// HarnessCapabilityMatrix is the API-neutral capability classification that
// the root facade projects onto its public ListHarnesses response.
type HarnessCapabilityMatrix struct {
	ExecutePrompt   HarnessCapability
	ModelDiscovery  HarnessCapability
	ModelPinning    HarnessCapability
	WorkdirContext  HarnessCapability
	ReasoningLevels HarnessCapability
	PermissionModes HarnessCapability
	ProgressEvents  HarnessCapability
	UsageCapture    HarnessCapability
	FinalText       HarnessCapability
	ToolEvents      HarnessCapability
	QuotaStatus     HarnessCapability
	RecordReplay    HarnessCapability
}

func capabilityRequired(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityRequired, Detail: detail}
}

func capabilityOptional(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityOptional, Detail: detail}
}

func capabilityUnsupported(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityUnsupported, Detail: detail}
}

func capabilityNotApplicable(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityNotApplicable, Detail: detail}
}

// ClassifyHarnessCapabilities returns the exact status and explanatory detail
// for every capability reported by ListHarnesses.
func ClassifyHarnessCapabilities(name string, cfg harnesses.HarnessConfig) HarnessCapabilityMatrix {
	return HarnessCapabilityMatrix{
		ExecutePrompt:   executePromptCapability(name, cfg),
		ModelDiscovery:  modelDiscoveryCapability(name, cfg),
		ModelPinning:    modelPinningCapability(cfg),
		WorkdirContext:  workdirContextCapability(name, cfg),
		ReasoningLevels: reasoningCapability(cfg),
		PermissionModes: permissionCapability(cfg),
		ProgressEvents:  progressEventsCapability(name, cfg),
		UsageCapture:    usageCaptureCapability(name, cfg),
		FinalText:       finalTextCapability(name, cfg),
		ToolEvents:      toolEventsCapability(name, cfg),
		QuotaStatus:     quotaStatusCapability(cfg),
		RecordReplay:    recordReplayCapability(cfg),
	}
}

func serviceExecuteWired(name string, cfg harnesses.HarnessConfig) bool {
	switch name {
	case "fiz", "claude", "codex", "gemini", "grok", "opencode", "pi", "virtual", "script":
		return true
	default:
		return cfg.IsHTTPProvider
	}
}

func executePromptCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if serviceExecuteWired(name, cfg) {
		return capabilityRequired("Service.Execute has a wired dispatch path for this harness")
	}
	return capabilityUnsupported("registered harness is not wired through Service.Execute yet")
}

func modelDiscoveryCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness has no live model catalog")
	}
	if name == "codex" || name == "claude" {
		return capabilityOptional("models are discovered from direct PTY TUI evidence or documented CLI help")
	}
	if name == "gemini" {
		return capabilityOptional("models are discovered from Gemini CLI bundled model configuration and replay fixtures")
	}
	if name == "grok" || name == "opencode" || name == "pi" {
		return capabilityOptional("models are discovered from a stable harness CLI command or documented CLI help")
	}
	if name == "fiz" || cfg.IsHTTPProvider {
		return capabilityOptional("models are discovered through the native provider catalog when configured")
	}
	return capabilityUnsupported("subprocess harness exposes no stable model-discovery API")
}

func modelPinningCapability(cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness uses deterministic fixtures or directives")
	}
	if cfg.ExactPinSupport {
		return capabilityOptional("registry marks exact model pinning as supported")
	}
	return capabilityUnsupported("registry does not mark exact model pinning as supported")
}

func workdirContextCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness does not operate on a real workdir")
	}
	if name == "fiz" || cfg.WorkDirFlag != "" {
		return capabilityOptional("harness accepts an explicit workdir/context")
	}
	if name == "claude" || name == "gemini" || name == "pi" {
		return capabilityOptional("service runner sets the subprocess working directory")
	}
	return capabilityUnsupported("no explicit workdir/context support is registered")
}

func reasoningCapability(cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness does not perform model reasoning")
	}
	if cfg.Name == "codex" || cfg.Name == "claude" {
		return capabilityOptional("reasoning levels are validated against harness CLI evidence before execution")
	}
	if cfg.Name == "gemini" {
		return capabilityUnsupported("Gemini CLI exposes model thinking internally, but the harness has no stable per-request reasoning control")
	}
	if len(cfg.ReasoningLevels) > 0 || cfg.MaxReasoningTokens > 0 {
		return capabilityOptional("registry declares supported reasoning levels or token budget")
	}
	return capabilityUnsupported("registry declares no reasoning control")
}

func permissionCapability(cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness does not enforce tool permissions")
	}
	if hasSupportedPermission(cfg) {
		return capabilityOptional("registry declares permission modes")
	}
	return capabilityUnsupported("registry declares no permission modes")
}

func hasSupportedPermission(cfg harnesses.HarnessConfig) bool {
	for _, level := range []string{"safe", "supervised", "unrestricted"} {
		if _, ok := cfg.PermissionArgs[level]; ok {
			return true
		}
	}
	return false
}

func progressEventsCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if serviceExecuteWired(name, cfg) {
		return capabilityRequired("Service.Execute emits routing/progress/final events")
	}
	return capabilityUnsupported("progress events are unavailable until Service.Execute dispatch is wired")
}

func usageCaptureCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if serviceExecuteWired(name, cfg) {
		return capabilityOptional("usage capture is best-effort and reported on final events when available")
	}
	return capabilityUnsupported("usage capture is unavailable until Service.Execute dispatch is wired")
}

func finalTextCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if name == "virtual" || name == "script" {
		return capabilityOptional("test-only final events include deterministic final_text")
	}
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness does not expose normalized live response text")
	}
	switch name {
	case "fiz", "codex", "claude", "gemini", "grok", "opencode", "pi":
		return capabilityOptional("final events include normalized final_text when response text is available")
	default:
		if cfg.IsHTTPProvider {
			return capabilityOptional("native-provider final events include normalized final_text when response text is available")
		}
		return capabilityUnsupported("final events do not expose normalized final response text")
	}
}

func toolEventsCapability(name string, cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityNotApplicable("test-only harness does not expose live tool events")
	}
	switch name {
	case "fiz", "claude", "codex":
		return capabilityOptional("Service.Execute emits tool_call and tool_result events")
	default:
		return capabilityUnsupported("tool-call/tool-result events are not exposed for this harness")
	}
}

func quotaStatusCapability(cfg harnesses.HarnessConfig) HarnessCapability {
	billing := HarnessPaymentKind(cfg.Name, cfg)
	if cfg.TestOnly || billing == modelcatalog.BillingModelFixed {
		return capabilityNotApplicable("local or test-only harness has no subscription quota")
	}
	if cfg.Name == "gemini" {
		return capabilityOptional("Gemini CLI /model manage tier usage is probed via PTY and persisted to a durable quota cache")
	}
	if billing == modelcatalog.BillingModelSubscription && cfg.TUIQuotaCommand != "" {
		return capabilityOptional("subscription quota can be probed or read from a cache")
	}
	return capabilityUnsupported("no quota/status monitor is registered")
}

func recordReplayCapability(cfg harnesses.HarnessConfig) HarnessCapability {
	if cfg.TestOnly {
		return capabilityRequired("test-only harness provides deterministic replay or directive execution")
	}
	if cfg.Name == "codex" || cfg.Name == "claude" || cfg.Name == "grok" {
		return capabilityOptional("direct PTY discovery and quota probes produce replayable sanitized cassettes")
	}
	if cfg.Name == "gemini" {
		return capabilityOptional("credential-free replay fixtures cover model discovery, auth evidence parsing, and stream-json usage")
	}
	return capabilityUnsupported("production harness does not provide deterministic record/replay")
}
