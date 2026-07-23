package serviceimpl

import (
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestHarnessCapabilityMatrixClassification(t *testing.T) {
	required := func(detail string) HarnessCapability {
		return HarnessCapability{Status: HarnessCapabilityRequired, Detail: detail}
	}
	optional := func(detail string) HarnessCapability {
		return HarnessCapability{Status: HarnessCapabilityOptional, Detail: detail}
	}
	unsupported := func(detail string) HarnessCapability {
		return HarnessCapability{Status: HarnessCapabilityUnsupported, Detail: detail}
	}
	notApplicable := func(detail string) HarnessCapability {
		return HarnessCapability{Status: HarnessCapabilityNotApplicable, Detail: detail}
	}

	executeRequired := required("Service.Execute has a wired dispatch path for this harness")
	executeUnsupported := unsupported("registered harness is not wired through Service.Execute yet")
	discoveryTUI := optional("models are discovered from direct PTY TUI evidence or documented CLI help")
	discoveryGemini := optional("models are discovered from Gemini CLI bundled model configuration and replay fixtures")
	discoveryCLI := optional("models are discovered from a stable harness CLI command or documented CLI help")
	discoveryNative := optional("models are discovered through the native provider catalog when configured")
	discoveryUnsupported := unsupported("subprocess harness exposes no stable model-discovery API")
	discoveryTest := notApplicable("test-only harness has no live model catalog")
	pinningOptional := optional("registry marks exact model pinning as supported")
	pinningUnsupported := unsupported("registry does not mark exact model pinning as supported")
	pinningTest := notApplicable("test-only harness uses deterministic fixtures or directives")
	workdirExplicit := optional("harness accepts an explicit workdir/context")
	workdirProcess := optional("service runner sets the subprocess working directory")
	workdirUnsupported := unsupported("no explicit workdir/context support is registered")
	workdirTest := notApplicable("test-only harness does not operate on a real workdir")
	reasoningValidated := optional("reasoning levels are validated against harness CLI evidence before execution")
	reasoningGemini := unsupported("Gemini CLI exposes model thinking internally, but the harness has no stable per-request reasoning control")
	reasoningRegistry := optional("registry declares supported reasoning levels or token budget")
	reasoningUnsupported := unsupported("registry declares no reasoning control")
	reasoningTest := notApplicable("test-only harness does not perform model reasoning")
	permissionsOptional := optional("registry declares permission modes")
	permissionsUnsupported := unsupported("registry declares no permission modes")
	permissionsTest := notApplicable("test-only harness does not enforce tool permissions")
	progressRequired := required("Service.Execute emits routing/progress/final events")
	progressUnsupported := unsupported("progress events are unavailable until Service.Execute dispatch is wired")
	usageOptional := optional("usage capture is best-effort and reported on final events when available")
	usageUnsupported := unsupported("usage capture is unavailable until Service.Execute dispatch is wired")
	finalLive := optional("final events include normalized final_text when response text is available")
	finalNative := optional("native-provider final events include normalized final_text when response text is available")
	finalUnsupported := unsupported("final events do not expose normalized final response text")
	finalTest := optional("test-only final events include deterministic final_text")
	toolsOptional := optional("Service.Execute emits tool_call and tool_result events")
	toolsUnsupported := unsupported("tool-call/tool-result events are not exposed for this harness")
	toolsTest := notApplicable("test-only harness does not expose live tool events")
	quotaSubscription := optional("subscription quota can be probed or read from a cache")
	quotaGemini := optional("Gemini CLI /model manage tier usage is probed via PTY and persisted to a durable quota cache")
	quotaUnsupported := unsupported("no quota/status monitor is registered")
	quotaNotApplicable := notApplicable("local or test-only harness has no subscription quota")
	replayTUI := optional("direct PTY discovery and quota probes produce replayable sanitized cassettes")
	replayGemini := optional("credential-free replay fixtures cover model discovery, auth evidence parsing, and stream-json usage")
	replayUnsupported := unsupported("production harness does not provide deterministic record/replay")
	replayTest := required("test-only harness provides deterministic replay or directive execution")

	matrix := func(capabilities ...HarnessCapability) [12]HarnessCapability {
		t.Helper()
		if len(capabilities) != 12 {
			t.Fatalf("matrix capability count = %d, want 12", len(capabilities))
		}
		return [12]HarnessCapability(capabilities)
	}

	want := map[string][12]HarnessCapability{
		"codex": matrix(
			executeRequired, discoveryTUI, pinningOptional, workdirExplicit,
			reasoningValidated, permissionsOptional, progressRequired, usageOptional,
			finalLive, toolsOptional, quotaSubscription, replayTUI,
		),
		"claude-tui": matrix(
			executeUnsupported, discoveryUnsupported, pinningUnsupported, workdirUnsupported,
			reasoningUnsupported, permissionsOptional, progressUnsupported, usageUnsupported,
			finalUnsupported, toolsUnsupported, quotaUnsupported, replayUnsupported,
		),
		"claude": matrix(
			executeRequired, discoveryTUI, pinningOptional, workdirProcess,
			reasoningValidated, permissionsOptional, progressRequired, usageOptional,
			finalLive, toolsOptional, quotaSubscription, replayTUI,
		),
		"opencode": matrix(
			executeRequired, discoveryCLI, pinningOptional, workdirExplicit,
			reasoningRegistry, permissionsOptional, progressRequired, usageOptional,
			finalLive, toolsUnsupported, quotaUnsupported, replayUnsupported,
		),
		"fiz": matrix(
			executeRequired, discoveryNative, pinningOptional, workdirExplicit,
			reasoningRegistry, permissionsOptional, progressRequired, usageOptional,
			finalLive, toolsOptional, quotaNotApplicable, replayUnsupported,
		),
		"pi": matrix(
			executeRequired, discoveryCLI, pinningOptional, workdirProcess,
			reasoningRegistry, permissionsUnsupported, progressRequired, usageOptional,
			finalLive, toolsUnsupported, quotaUnsupported, replayUnsupported,
		),
		"openrouter": matrix(
			executeRequired, discoveryNative, pinningUnsupported, workdirUnsupported,
			reasoningUnsupported, permissionsUnsupported, progressRequired, usageOptional,
			finalNative, toolsUnsupported, quotaUnsupported, replayUnsupported,
		),
		"lmstudio": matrix(
			executeRequired, discoveryNative, pinningUnsupported, workdirUnsupported,
			reasoningUnsupported, permissionsUnsupported, progressRequired, usageOptional,
			finalNative, toolsUnsupported, quotaNotApplicable, replayUnsupported,
		),
		"omlx": matrix(
			executeRequired, discoveryNative, pinningUnsupported, workdirUnsupported,
			reasoningUnsupported, permissionsUnsupported, progressRequired, usageOptional,
			finalNative, toolsUnsupported, quotaNotApplicable, replayUnsupported,
		),
		"lucebox": matrix(
			executeRequired, discoveryNative, pinningUnsupported, workdirUnsupported,
			reasoningUnsupported, permissionsUnsupported, progressRequired, usageOptional,
			finalNative, toolsUnsupported, quotaNotApplicable, replayUnsupported,
		),
		"vllm": matrix(
			executeRequired, discoveryNative, pinningUnsupported, workdirUnsupported,
			reasoningUnsupported, permissionsUnsupported, progressRequired, usageOptional,
			finalNative, toolsUnsupported, quotaNotApplicable, replayUnsupported,
		),
		"gemini": matrix(
			executeRequired, discoveryGemini, pinningOptional, workdirProcess,
			reasoningGemini, permissionsOptional, progressRequired, usageOptional,
			finalLive, toolsUnsupported, quotaGemini, replayGemini,
		),
		"grok": matrix(
			executeRequired, discoveryCLI, pinningOptional, workdirExplicit,
			reasoningRegistry, permissionsOptional, progressRequired, usageOptional,
			finalLive, toolsUnsupported, quotaSubscription, replayTUI,
		),
		"virtual": matrix(
			executeRequired, discoveryTest, pinningTest, workdirTest,
			reasoningTest, permissionsTest, progressRequired, usageOptional,
			finalTest, toolsTest, quotaNotApplicable, replayTest,
		),
		"script": matrix(
			executeRequired, discoveryTest, pinningTest, workdirTest,
			reasoningTest, permissionsTest, progressRequired, usageOptional,
			finalTest, toolsTest, quotaNotApplicable, replayTest,
		),
	}

	registry := harnesses.NewRegistry()
	if got := len(registry.Names()); got != len(want) {
		t.Fatalf("registered harness count = %d, expected classification count = %d", got, len(want))
	}
	for _, name := range registry.Names() {
		cfg, ok := registry.Get(name)
		if !ok {
			t.Fatalf("registry name %q has no config", name)
		}
		expected, ok := want[name]
		if !ok {
			t.Errorf("registered harness %q has no exact capability expectation", name)
			continue
		}
		got := capabilityArray(ClassifyHarnessCapabilities(name, cfg))
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s capability classification mismatch\nwant: %#v\ngot:  %#v", name, expected, got)
		}
	}
}

func capabilityArray(matrix HarnessCapabilityMatrix) [12]HarnessCapability {
	return [12]HarnessCapability{
		matrix.ExecutePrompt,
		matrix.ModelDiscovery,
		matrix.ModelPinning,
		matrix.WorkdirContext,
		matrix.ReasoningLevels,
		matrix.PermissionModes,
		matrix.ProgressEvents,
		matrix.UsageCapture,
		matrix.FinalText,
		matrix.ToolEvents,
		matrix.QuotaStatus,
		matrix.RecordReplay,
	}
}
