package fizeau

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/stretchr/testify/require"
)

// preRefactorFixtureDir is where BEAD-HARNESS-IF-00 captures
// CONTRACT-003 JSON shapes for HarnessInfo, ProviderInfo, QuotaState,
// and AccountStatus before the universal-harness-interface refactor
// begins. Step 11 of the plan diffs the post-refactor shapes against
// these files to assert no public-shape regression.
const preRefactorFixtureDir = "testdata/contract-003/pre-refactor"

// regenPreRefactorFixturesEnv, when set to a truthy value, causes the
// fixture test to overwrite the pinned files instead of asserting
// equality. Manual one-shot use only — never set in CI.
const regenPreRefactorFixturesEnv = "FIZEAU_REGEN_PRE_REFACTOR_FIXTURES"

func preRefactorCapturedAt() time.Time {
	return time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
}

// These constructors are fixture-only copies of the pre-refactor public
// values. Production capability status/detail construction is owned by
// internal/serviceimpl.
func capRequired(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityRequired, Detail: detail}
}

func capOptional(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityOptional, Detail: detail}
}

func capUnsupported(detail string) HarnessCapability {
	return HarnessCapability{Status: HarnessCapabilityUnsupported, Detail: detail}
}

func preRefactorQuotaWindowsClaude() []harnesses.QuotaWindow {
	return []harnesses.QuotaWindow{
		{
			Name:          "5h",
			LimitID:       "session",
			LimitName:     "5-hour session",
			WindowMinutes: 300,
			UsedPercent:   42.5,
			ResetsAt:      "2026-05-14T22:00:00Z",
			ResetsAtUnix:  1763157600,
			State:         "ok",
		},
		{
			Name:          "weekly-all",
			LimitID:       "weekly-all",
			LimitName:     "Weekly (all models)",
			WindowMinutes: 10080,
			UsedPercent:   18.0,
			ResetsAt:      "2026-05-19T17:00:00Z",
			ResetsAtUnix:  1763571600,
			State:         "ok",
		},
	}
}

func preRefactorQuotaWindowsCodex() []harnesses.QuotaWindow {
	return []harnesses.QuotaWindow{
		{
			Name:          "5h",
			LimitID:       "session",
			LimitName:     "Session window",
			WindowMinutes: 300,
			UsedPercent:   71.2,
			ResetsAt:      "2026-05-14T22:00:00Z",
			ResetsAtUnix:  1763157600,
			State:         "ok",
		},
	}
}

func preRefactorQuotaWindowsGemini() []harnesses.QuotaWindow {
	return []harnesses.QuotaWindow{
		{
			Name:          "flash",
			LimitID:       "tier-flash",
			LimitName:     "Gemini Flash",
			WindowMinutes: 1440,
			UsedPercent:   12.0,
			ResetsAt:      "2026-05-15T00:00:00Z",
			ResetsAtUnix:  1763164800,
			State:         "ok",
		},
		{
			Name:          "flash-lite",
			LimitID:       "tier-flash-lite",
			LimitName:     "Gemini Flash Lite",
			WindowMinutes: 1440,
			UsedPercent:   8.0,
			ResetsAt:      "2026-05-15T00:00:00Z",
			ResetsAtUnix:  1763164800,
			State:         "ok",
		},
		{
			Name:          "pro",
			LimitID:       "tier-pro",
			LimitName:     "Gemini Pro",
			WindowMinutes: 1440,
			UsedPercent:   97.0,
			ResetsAt:      "2026-05-15T00:00:00Z",
			ResetsAtUnix:  1763164800,
			State:         "blocked",
		},
	}
}

func preRefactorQuotaStateClaude() *QuotaState {
	return &QuotaState{
		Windows:    preRefactorQuotaWindowsClaude(),
		CapturedAt: preRefactorCapturedAt(),
		Fresh:      true,
		Source:     "internal/harnesses/claude/quota_cache.go",
		Status:     "ok",
	}
}

func preRefactorQuotaStateCodex() *QuotaState {
	return &QuotaState{
		Windows:    preRefactorQuotaWindowsCodex(),
		CapturedAt: preRefactorCapturedAt(),
		Fresh:      true,
		Source:     "internal/harnesses/codex/quota_cache.go",
		Status:     "ok",
	}
}

func preRefactorQuotaStateGemini() *QuotaState {
	return &QuotaState{
		Windows:    preRefactorQuotaWindowsGemini(),
		CapturedAt: preRefactorCapturedAt(),
		Fresh:      true,
		Source:     "internal/harnesses/gemini/quota_cache.go",
		Status:     "ok (Pro tier blocked)",
	}
}

func preRefactorAccountClaude() *AccountStatus {
	return &AccountStatus{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "claude_max",
		OrgName:       "Example Org",
		Source:        "~/.claude/.credentials.json",
		CapturedAt:    preRefactorCapturedAt(),
		Fresh:         true,
		Detail:        "anthropic subscription",
	}
}

func preRefactorAccountCodex() *AccountStatus {
	return &AccountStatus{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "chatgpt_pro",
		Source:        "~/.codex/auth.json",
		CapturedAt:    preRefactorCapturedAt(),
		Fresh:         true,
	}
}

func preRefactorAccountGemini() *AccountStatus {
	return &AccountStatus{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "gemini_pro",
		Source:        "~/.gemini/oauth_creds.json",
		CapturedAt:    preRefactorCapturedAt(),
		Fresh:         true,
		Detail:        "auth evidence cached for 7d",
	}
}

func preRefactorAccountUnauthenticated(source string) *AccountStatus {
	return &AccountStatus{
		Unauthenticated: true,
		Source:          source,
		CapturedAt:      preRefactorCapturedAt(),
		Fresh:           true,
		Detail:          "no auth evidence on disk",
	}
}

func preRefactorCapabilityMatrix(name string) HarnessCapabilityMatrix {
	switch name {
	case "claude":
		return HarnessCapabilityMatrix{
			ExecutePrompt:   capRequired("Service.Execute has a wired dispatch path for this harness"),
			ModelDiscovery:  capOptional("models are discovered from direct PTY TUI evidence or documented CLI help"),
			ModelPinning:    capOptional("registry marks exact model pinning as supported"),
			WorkdirContext:  capOptional("service runner sets the subprocess working directory"),
			ReasoningLevels: capOptional("reasoning levels are validated against harness CLI evidence before execution"),
			PermissionModes: capOptional("registry declares permission modes"),
			ProgressEvents:  capRequired("Service.Execute emits routing/progress/final events"),
			UsageCapture:    capOptional("usage capture is best-effort and reported on final events when available"),
			FinalText:       capOptional("final events include normalized final_text when response text is available"),
			ToolEvents:      capOptional("Service.Execute emits tool_call and tool_result events"),
			QuotaStatus:     capOptional("subscription quota can be probed or read from a cache"),
			RecordReplay:    capOptional("direct PTY discovery and quota probes produce replayable sanitized cassettes"),
		}
	case "codex":
		return HarnessCapabilityMatrix{
			ExecutePrompt:   capRequired("Service.Execute has a wired dispatch path for this harness"),
			ModelDiscovery:  capOptional("models are discovered from direct PTY TUI evidence or documented CLI help"),
			ModelPinning:    capOptional("registry marks exact model pinning as supported"),
			WorkdirContext:  capUnsupported("no explicit workdir/context support is registered"),
			ReasoningLevels: capOptional("reasoning levels are validated against harness CLI evidence before execution"),
			PermissionModes: capOptional("registry declares permission modes"),
			ProgressEvents:  capRequired("Service.Execute emits routing/progress/final events"),
			UsageCapture:    capOptional("usage capture is best-effort and reported on final events when available"),
			FinalText:       capOptional("final events include normalized final_text when response text is available"),
			ToolEvents:      capOptional("Service.Execute emits tool_call and tool_result events"),
			QuotaStatus:     capOptional("subscription quota can be probed or read from a cache"),
			RecordReplay:    capOptional("direct PTY discovery and quota probes produce replayable sanitized cassettes"),
		}
	case "gemini":
		return HarnessCapabilityMatrix{
			ExecutePrompt:   capRequired("Service.Execute has a wired dispatch path for this harness"),
			ModelDiscovery:  capOptional("models are discovered from Gemini CLI bundled model configuration and replay fixtures"),
			ModelPinning:    capOptional("registry marks exact model pinning as supported"),
			WorkdirContext:  capOptional("service runner sets the subprocess working directory"),
			ReasoningLevels: capUnsupported("Gemini CLI exposes model thinking internally, but the harness has no stable per-request reasoning control"),
			PermissionModes: capOptional("registry declares permission modes"),
			ProgressEvents:  capRequired("Service.Execute emits routing/progress/final events"),
			UsageCapture:    capOptional("usage capture is best-effort and reported on final events when available"),
			FinalText:       capOptional("final events include normalized final_text when response text is available"),
			ToolEvents:      capUnsupported("tool-call/tool-result events are not exposed for this harness"),
			QuotaStatus:     capOptional("Gemini CLI /model manage tier usage is probed via PTY and persisted to a durable quota cache"),
			RecordReplay:    capOptional("credential-free replay fixtures cover model discovery, auth evidence parsing, and stream-json usage"),
		}
	case "opencode":
		return HarnessCapabilityMatrix{
			ExecutePrompt:   capRequired("Service.Execute has a wired dispatch path for this harness"),
			ModelDiscovery:  capOptional("models are discovered from a stable harness CLI command or documented CLI help"),
			ModelPinning:    capOptional("registry marks exact model pinning as supported"),
			WorkdirContext:  capUnsupported("no explicit workdir/context support is registered"),
			ReasoningLevels: capUnsupported("registry declares no reasoning control"),
			PermissionModes: capOptional("registry declares permission modes"),
			ProgressEvents:  capRequired("Service.Execute emits routing/progress/final events"),
			UsageCapture:    capOptional("usage capture is best-effort and reported on final events when available"),
			FinalText:       capOptional("final events include normalized final_text when response text is available"),
			ToolEvents:      capUnsupported("tool-call/tool-result events are not exposed for this harness"),
			QuotaStatus:     capUnsupported("no quota/status monitor is registered"),
			RecordReplay:    capUnsupported("production harness does not provide deterministic record/replay"),
		}
	case "pi":
		return HarnessCapabilityMatrix{
			ExecutePrompt:   capRequired("Service.Execute has a wired dispatch path for this harness"),
			ModelDiscovery:  capOptional("models are discovered from a stable harness CLI command or documented CLI help"),
			ModelPinning:    capOptional("registry marks exact model pinning as supported"),
			WorkdirContext:  capOptional("service runner sets the subprocess working directory"),
			ReasoningLevels: capUnsupported("registry declares no reasoning control"),
			PermissionModes: capOptional("registry declares permission modes"),
			ProgressEvents:  capRequired("Service.Execute emits routing/progress/final events"),
			UsageCapture:    capOptional("usage capture is best-effort and reported on final events when available"),
			FinalText:       capOptional("final events include normalized final_text when response text is available"),
			ToolEvents:      capUnsupported("tool-call/tool-result events are not exposed for this harness"),
			QuotaStatus:     capUnsupported("no quota/status monitor is registered"),
			RecordReplay:    capUnsupported("production harness does not provide deterministic record/replay"),
		}
	}
	return HarnessCapabilityMatrix{}
}

func preRefactorHarnessClaude() HarnessInfo {
	return HarnessInfo{
		Name:                 "claude",
		Type:                 "subprocess",
		Available:            true,
		Path:                 "/usr/local/bin/claude",
		Billing:              modelcatalog.BillingModelSubscription,
		AutoRoutingEligible:  true,
		ExactPinSupport:      true,
		DefaultModel:         "claude-sonnet-4-6",
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		SupportedReasoning:   []string{"low", "medium", "high"},
		CostClass:            "expensive",
		Quota:                preRefactorQuotaStateClaude(),
		Account:              preRefactorAccountClaude(),
		CapabilityMatrix:     preRefactorCapabilityMatrix("claude"),
	}
}

func preRefactorHarnessCodex() HarnessInfo {
	return HarnessInfo{
		Name:                 "codex",
		Type:                 "subprocess",
		Available:            true,
		Path:                 "/usr/local/bin/codex",
		Billing:              modelcatalog.BillingModelSubscription,
		AutoRoutingEligible:  true,
		ExactPinSupport:      true,
		DefaultModel:         "gpt-5",
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		SupportedReasoning:   []string{"low", "medium", "high"},
		CostClass:            "expensive",
		Quota:                preRefactorQuotaStateCodex(),
		Account:              preRefactorAccountCodex(),
		CapabilityMatrix:     preRefactorCapabilityMatrix("codex"),
	}
}

func preRefactorHarnessGemini() HarnessInfo {
	return HarnessInfo{
		Name:                 "gemini",
		Type:                 "subprocess",
		Available:            true,
		Path:                 "/usr/local/bin/gemini",
		Billing:              modelcatalog.BillingModelSubscription,
		AutoRoutingEligible:  true,
		ExactPinSupport:      true,
		DefaultModel:         "gemini-2.5-pro",
		SupportedPermissions: []string{"safe", "supervised"},
		CostClass:            "medium",
		Quota:                preRefactorQuotaStateGemini(),
		Account:              preRefactorAccountGemini(),
		CapabilityMatrix:     preRefactorCapabilityMatrix("gemini"),
	}
}

func preRefactorHarnessOpenCode() HarnessInfo {
	return HarnessInfo{
		Name:                 "opencode",
		Type:                 "subprocess",
		Available:            true,
		Path:                 "/usr/local/bin/opencode",
		Billing:              modelcatalog.BillingModelPerToken,
		AutoRoutingEligible:  false,
		ExactPinSupport:      true,
		SupportedPermissions: []string{"safe", "supervised", "unrestricted"},
		CostClass:            "medium",
		Account:              preRefactorAccountUnauthenticated("opencode harness has no native account file"),
		CapabilityMatrix:     preRefactorCapabilityMatrix("opencode"),
	}
}

func preRefactorHarnessPi() HarnessInfo {
	return HarnessInfo{
		Name:                 "pi",
		Type:                 "subprocess",
		Available:            true,
		Path:                 "/usr/local/bin/pi",
		Billing:              modelcatalog.BillingModelPerToken,
		AutoRoutingEligible:  false,
		ExactPinSupport:      true,
		SupportedPermissions: []string{"safe", "supervised"},
		CostClass:            "cheap",
		Account:              preRefactorAccountUnauthenticated("pi harness has no native account file"),
		CapabilityMatrix:     preRefactorCapabilityMatrix("pi"),
	}
}

// preRefactorProviderClaudeSubscription is the representative
// subscription-backed provider entry — claude is the primary
// subscription-billed harness in the registry.
func preRefactorProviderClaudeSubscription() ProviderInfo {
	return ProviderInfo{
		Name:    "claude",
		Type:    "anthropic",
		BaseURL: "https://api.anthropic.com",
		Endpoints: []ServiceProviderEndpoint{
			{
				Name:           "default",
				BaseURL:        "https://api.anthropic.com",
				ServerInstance: "anthropic-cloud",
			},
		},
		Status:           "connected",
		ModelCount:       3,
		Capabilities:     []string{"tool_use", "streaming", "json_mode"},
		Billing:          modelcatalog.BillingModelSubscription,
		IncludeByDefault: true,
		IsDefault:        false,
		DefaultModel:     "claude-sonnet-4-6",
		Auth:             *preRefactorAccountClaude(),
		EndpointStatus: []EndpointStatus{
			{
				Name:           "default",
				BaseURL:        "https://api.anthropic.com",
				ServerInstance: "anthropic-cloud",
				ProbeURL:       "https://api.anthropic.com/v1/models",
				Status:         "connected",
				Source:         "service_providers.go",
				CapturedAt:     preRefactorCapturedAt(),
				Fresh:          true,
				LastSuccessAt:  preRefactorCapturedAt(),
				ModelCount:     3,
			},
		},
		Quota: preRefactorQuotaStateClaude(),
	}
}

func marshalIndentJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return append(out, '\n')
}

type preRefactorFixture struct {
	relPath string
	value   any
}

func preRefactorFixtures() []preRefactorFixture {
	claudeQ := preRefactorQuotaStateClaude()
	codexQ := preRefactorQuotaStateCodex()
	geminiQ := preRefactorQuotaStateGemini()
	claudeA := preRefactorAccountClaude()
	codexA := preRefactorAccountCodex()
	geminiA := preRefactorAccountGemini()
	return []preRefactorFixture{
		{"harness-claude.json", preRefactorHarnessClaude()},
		{"harness-codex.json", preRefactorHarnessCodex()},
		{"harness-gemini.json", preRefactorHarnessGemini()},
		{"harness-opencode.json", preRefactorHarnessOpenCode()},
		{"harness-pi.json", preRefactorHarnessPi()},
		{"provider-claude-subscription.json", preRefactorProviderClaudeSubscription()},
		{"quota-claude.json", claudeQ},
		{"quota-codex.json", codexQ},
		{"quota-gemini.json", geminiQ},
		{"account-claude.json", claudeA},
		{"account-codex.json", codexA},
		{"account-gemini.json", geminiA},
	}
}

// TestPreRefactorContract003Fixtures pins CONTRACT-003 JSON shapes for
// HarnessInfo, ProviderInfo, QuotaState, and AccountStatus across all
// five harnesses plus one subscription-backed provider. Step 11 of the
// universal-harness-interface refactor diffs the post-refactor shapes
// against these files; any structural drift fails this test.
//
// To regenerate the fixtures (e.g. after an intentional CONTRACT-003
// shape change), set FIZEAU_REGEN_PRE_REFACTOR_FIXTURES=1 and re-run.
func TestPreRefactorContract003Fixtures(t *testing.T) {
	regen := os.Getenv(regenPreRefactorFixturesEnv) != ""
	for _, fx := range preRefactorFixtures() {
		fx := fx
		t.Run(fx.relPath, func(t *testing.T) {
			path := filepath.Join(preRefactorFixtureDir, fx.relPath)
			got := marshalIndentJSON(t, fx.value)
			if regen {
				require.NoError(t, os.WriteFile(path, got, 0o644))
				return
			}
			want, err := os.ReadFile(path)
			require.NoErrorf(t, err, "missing fixture %s; regenerate with %s=1", path, regenPreRefactorFixturesEnv)
			if !bytes.Equal(want, got) {
				t.Fatalf("fixture %s drifted from package types.\nwant:\n%s\n\ngot:\n%s", path, string(want), string(got))
			}
		})
	}
}
