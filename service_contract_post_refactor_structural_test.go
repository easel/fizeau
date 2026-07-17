package fizeau

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/easel/fizeau/internal/test/structuraldiff"
)

type structuralFixtureHarness struct {
	name    string
	quota   harnesses.QuotaStatus
	account harnesses.AccountSnapshot
}

func (h *structuralFixtureHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: h.name, Type: "subprocess"}
}

func (h *structuralFixtureHarness) HealthCheck(context.Context) error { return nil }

func (h *structuralFixtureHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("structuralFixtureHarness.Execute should not be called")
}

func (h *structuralFixtureHarness) QuotaStatus(context.Context, time.Time) (harnesses.QuotaStatus, error) {
	return h.quota, nil
}

func (h *structuralFixtureHarness) RefreshQuota(context.Context) (harnesses.QuotaStatus, error) {
	return h.quota, nil
}

func (h *structuralFixtureHarness) QuotaFreshness() time.Duration {
	return 15 * time.Minute
}

func (h *structuralFixtureHarness) SupportedLimitIDs() []string {
	ids := make([]string, 0, len(h.quota.Windows))
	for _, window := range h.quota.Windows {
		if window.LimitID != "" {
			ids = append(ids, window.LimitID)
		}
	}
	return ids
}

func (h *structuralFixtureHarness) AccountStatus(context.Context, time.Time) (harnesses.AccountSnapshot, error) {
	return h.account, nil
}

func (h *structuralFixtureHarness) RefreshAccount(context.Context) (harnesses.AccountSnapshot, error) {
	return h.account, nil
}

func (h *structuralFixtureHarness) AccountFreshness() time.Duration {
	return 15 * time.Minute
}

func newStructuralFixtureService() *service {
	capturedAt := preRefactorCapturedAt()
	claudeAccount := harnesses.AccountSnapshot{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "claude_max",
		OrgName:       "Example Org",
		Source:        "~/.claude/.credentials.json",
		CapturedAt:    capturedAt,
		Fresh:         true,
		Detail:        "anthropic subscription",
	}
	codexAccount := harnesses.AccountSnapshot{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "chatgpt_pro",
		Source:        "~/.codex/auth.json",
		CapturedAt:    capturedAt,
		Fresh:         true,
	}
	geminiAccount := harnesses.AccountSnapshot{
		Authenticated: true,
		Email:         "user@example.com",
		PlanType:      "gemini_pro",
		Source:        "~/.gemini/oauth_creds.json",
		CapturedAt:    capturedAt,
		Fresh:         true,
		Detail:        "auth evidence cached for 7d",
	}
	instances := map[string]harnesses.Harness{
		"claude": &structuralFixtureHarness{
			name: "claude",
			quota: harnesses.QuotaStatus{
				Source:            "internal/harnesses/claude/quota_cache.go",
				CapturedAt:        capturedAt,
				Fresh:             true,
				State:             harnesses.QuotaOK,
				Windows:           preRefactorQuotaWindowsClaude(),
				Account:           &claudeAccount,
				RoutingPreference: harnesses.RoutingPreferenceAvailable,
			},
			account: claudeAccount,
		},
		"codex": &structuralFixtureHarness{
			name: "codex",
			quota: harnesses.QuotaStatus{
				Source:            "internal/harnesses/codex/quota_cache.go",
				CapturedAt:        capturedAt,
				Fresh:             true,
				State:             harnesses.QuotaOK,
				Windows:           preRefactorQuotaWindowsCodex(),
				Account:           &codexAccount,
				RoutingPreference: harnesses.RoutingPreferenceAvailable,
			},
			account: codexAccount,
		},
		"gemini": &structuralFixtureHarness{
			name: "gemini",
			quota: harnesses.QuotaStatus{
				Source:            "internal/harnesses/gemini/quota_cache.go",
				CapturedAt:        capturedAt,
				Fresh:             true,
				State:             harnesses.QuotaOK,
				Windows:           preRefactorQuotaWindowsGemini(),
				Account:           &geminiAccount,
				RoutingPreference: harnesses.RoutingPreferenceAvailable,
				Reason:            "Pro tier blocked",
			},
			account: geminiAccount,
		},
	}
	return &service{
		routeRunners: harnesses.NewRouteRunnerAuthority(instances, nil),
		runtime: serviceimpl.NewRuntime(serviceimpl.RuntimeDeps{
			Now: func() time.Time { return capturedAt },
		}),
	}
}

func postRefactorFixtures(t *testing.T) []preRefactorFixture {
	t.Helper()

	svc := newStructuralFixtureService()
	ctx := context.Background()

	claudeQuota := svc.claudeQuotaState(ctx)
	codexQuota := svc.codexQuotaState(ctx)
	geminiQuota := svc.geminiQuotaState(ctx)
	claudeAccount := svc.claudeAccountStatus(ctx)
	codexAccount := svc.codexAccountStatus(ctx)
	geminiAccount := svc.geminiAccountStatus(ctx)

	if claudeQuota == nil || codexQuota == nil || geminiQuota == nil {
		t.Fatal("fixture service returned nil quota projection")
	}
	if claudeAccount == nil || codexAccount == nil || geminiAccount == nil {
		t.Fatal("fixture service returned nil account projection")
	}

	harnessClaude := preRefactorHarnessClaude()
	harnessClaude.Quota = claudeQuota
	harnessClaude.Account = claudeAccount

	harnessCodex := preRefactorHarnessCodex()
	harnessCodex.Quota = codexQuota
	harnessCodex.Account = codexAccount

	harnessGemini := preRefactorHarnessGemini()
	harnessGemini.Quota = geminiQuota
	harnessGemini.Account = geminiAccount

	providerClaude := preRefactorProviderClaudeSubscription()
	providerClaude.Auth = *claudeAccount
	providerClaude.Quota = claudeQuota

	return []preRefactorFixture{
		{"harness-claude.json", harnessClaude},
		{"harness-codex.json", harnessCodex},
		{"harness-gemini.json", harnessGemini},
		{"harness-opencode.json", preRefactorHarnessOpenCode()},
		{"harness-pi.json", preRefactorHarnessPi()},
		{"provider-claude-subscription.json", providerClaude},
		{"quota-claude.json", claudeQuota},
		{"quota-codex.json", codexQuota},
		{"quota-gemini.json", geminiQuota},
		{"account-claude.json", claudeAccount},
		{"account-codex.json", codexAccount},
		{"account-gemini.json", geminiAccount},
	}
}

func TestPostRefactorContract003FixturesStructuralDiff(t *testing.T) {
	for _, fx := range postRefactorFixtures(t) {
		fx := fx
		t.Run(fx.relPath, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(preRefactorFixtureDir, fx.relPath))
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", fx.relPath, err)
			}
			got := marshalIndentJSON(t, fx.value)
			if err := structuraldiff.CompareJSON(want, got, structuraldiff.Config{}); err != nil {
				t.Fatalf("structural diff failed for %s: %v\nwant:\n%s\n\ngot:\n%s", fx.relPath, err, want, got)
			}
		})
	}
}
