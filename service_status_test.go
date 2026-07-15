package fizeau_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

type statusQuotaWindowFixture struct {
	Name          string  `json:"name"`
	LimitID       string  `json:"limit_id,omitempty"`
	WindowMinutes int     `json:"window_minutes"`
	UsedPercent   float64 `json:"used_percent"`
	State         string  `json:"state"`
}

type statusAccountFixture struct {
	Email    string `json:"email,omitempty"`
	PlanType string `json:"plan_type,omitempty"`
	OrgName  string `json:"org_name,omitempty"`
}

type statusClaudeQuotaFixture struct {
	CapturedAt        time.Time                  `json:"captured_at"`
	FiveHourRemaining int                        `json:"five_hour_remaining"`
	FiveHourLimit     int                        `json:"five_hour_limit"`
	WeeklyRemaining   int                        `json:"weekly_remaining"`
	WeeklyLimit       int                        `json:"weekly_limit"`
	Windows           []statusQuotaWindowFixture `json:"windows,omitempty"`
	Source            string                     `json:"source"`
	Account           *statusAccountFixture      `json:"account,omitempty"`
}

type statusQuotaFixture struct {
	CapturedAt time.Time                  `json:"captured_at"`
	Windows    []statusQuotaWindowFixture `json:"windows"`
	Source     string                     `json:"source"`
	Account    *statusAccountFixture      `json:"account,omitempty"`
}

func TestListHarnesses_QuotaAndAccountStatus(t *testing.T) {
	dir := t.TempDir()
	isolateStatusHome(t, dir)
	claudePath := filepath.Join(dir, "claude-quota.json")
	codexPath := filepath.Join(dir, "codex-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", claudePath)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", codexPath)
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", filepath.Join(dir, "missing-gemini-quota.json"))

	capturedAt := time.Now().UTC().Add(-time.Minute)
	writeStatusJSONFixture(t, claudePath, statusClaudeQuotaFixture{
		CapturedAt:        capturedAt,
		FiveHourRemaining: 80,
		FiveHourLimit:     100,
		WeeklyRemaining:   90,
		WeeklyLimit:       100,
		Source:            "pty",
		Account:           &statusAccountFixture{PlanType: "Claude Max"},
		Windows: []statusQuotaWindowFixture{
			{Name: "5h", LimitID: "session", WindowMinutes: 300, UsedPercent: 20, State: "ok"},
			{Name: "weekly-all", LimitID: "weekly-all", WindowMinutes: 10080, UsedPercent: 10, State: "ok"},
		},
	})
	writeStatusJSONFixture(t, codexPath, statusQuotaFixture{
		CapturedAt: capturedAt,
		Source:     "pty",
		Account:    &statusAccountFixture{PlanType: "ChatGPT Plus"},
		Windows: []statusQuotaWindowFixture{
			{Name: "5h", LimitID: "codex", WindowMinutes: 300, UsedPercent: 20, State: "ok"},
		},
	})

	svc := newStatusFacade(t, &statusConfig{})
	infos, err := svc.ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}

	claudeInfo := findPublicHarnessInfo(infos, "claude")
	if claudeInfo == nil || claudeInfo.Quota == nil {
		t.Fatalf("expected claude quota, got %#v", claudeInfo)
	}
	if claudeInfo.Quota.Source != "pty" || claudeInfo.Quota.Status != "ok" || !claudeInfo.Quota.Fresh {
		t.Fatalf("claude quota status: %#v", claudeInfo.Quota)
	}
	if claudeInfo.Account == nil || !claudeInfo.Account.Authenticated || claudeInfo.Account.PlanType != "Claude Max" {
		t.Fatalf("claude account: %#v", claudeInfo.Account)
	}

	codexInfo := findPublicHarnessInfo(infos, "codex")
	if codexInfo == nil || codexInfo.Quota == nil {
		t.Fatalf("expected codex quota, got %#v", codexInfo)
	}
	if codexInfo.Quota.Source != "pty" || codexInfo.Quota.Status != "ok" || len(codexInfo.Quota.Windows) != 1 {
		t.Fatalf("codex quota status: %#v", codexInfo.Quota)
	}
}

func TestListHarnesses_ClaudeQuotaUsesPreservedWindows(t *testing.T) {
	dir := t.TempDir()
	isolateStatusHome(t, dir)
	claudePath := filepath.Join(dir, "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", claudePath)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", filepath.Join(dir, "missing-gemini-quota.json"))

	writeStatusJSONFixture(t, claudePath, statusClaudeQuotaFixture{
		CapturedAt:    time.Now().UTC(),
		FiveHourLimit: 100,
		WeeklyLimit:   100,
		Source:        "runtime_error",
		Account:       &statusAccountFixture{PlanType: "unknown"},
		Windows: []statusQuotaWindowFixture{
			{Name: "extra", LimitID: "claude-extra", UsedPercent: 100, State: "exhausted"},
		},
	})

	infos, err := newStatusFacade(t, &statusConfig{}).ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}
	claudeInfo := findPublicHarnessInfo(infos, "claude")
	if claudeInfo == nil || claudeInfo.Quota == nil {
		t.Fatalf("expected claude quota, got %#v", claudeInfo)
	}
	if !strings.HasPrefix(claudeInfo.Quota.Status, "blocked") {
		t.Fatalf("claude quota status=%q, want blocked prefix: %#v", claudeInfo.Quota.Status, claudeInfo.Quota)
	}
	if len(claudeInfo.Quota.Windows) != 1 || claudeInfo.Quota.Windows[0].Name != "extra" || claudeInfo.Quota.Windows[0].State != "exhausted" {
		t.Fatalf("claude quota windows: %#v", claudeInfo.Quota.Windows)
	}
}

func TestListHarnesses_CodexUsageWindowsFromServiceSessionLogs(t *testing.T) {
	dir := t.TempDir()
	isolateStatusHome(t, dir)
	logDir := filepath.Join(dir, ".fizeau", "sessions")
	privateCodexDir := filepath.Join(dir, "private-codex")
	t.Setenv("CODEX_HOME", privateCodexDir)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", filepath.Join(dir, "missing-gemini-quota.json"))
	if err := os.MkdirAll(filepath.Join(privateCodexDir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateCodexDir, "sessions", "private.jsonl"), []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":999999}}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now().UTC().Add(-time.Hour)
	writePublicUsageSession(t, logDir, "codex-known", start, fizeau.SessionStartData{
		Provider: "codex",
		Model:    "gpt-5.4",
		Prompt:   "private prompt must not be read by status aggregation",
	}, fizeau.SessionEndData{
		Status:     fizeau.StatusSuccess,
		Tokens:     fizeau.TokenUsage{Input: 10, Output: 4, Total: 14, CacheRead: 3, CacheWrite: 2},
		CostUSD:    publicUsageCostPtr(0.12),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})
	writePublicUsageSession(t, logDir, "codex-unknown", start.Add(time.Minute), fizeau.SessionStartData{
		Provider: "codex",
		Model:    "gpt-5.4",
	}, fizeau.SessionEndData{
		Status:     fizeau.StatusSuccess,
		Tokens:     fizeau.TokenUsage{Input: 5, Output: 2, Total: 7},
		CostUSD:    publicUsageCostPtr(-1),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})
	writePublicUsageSession(t, logDir, "provider-not-codex", start.Add(2*time.Minute), fizeau.SessionStartData{
		Provider: "openrouter",
		Model:    "gpt-5.4",
	}, fizeau.SessionEndData{
		Status:     fizeau.StatusSuccess,
		Tokens:     fizeau.TokenUsage{Input: 100, Output: 100, Total: 200},
		CostUSD:    publicUsageCostPtr(1),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})

	infos, err := newStatusFacade(t, &statusConfig{sessionLogDir: logDir}).ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}
	codexInfo := findPublicHarnessInfo(infos, "codex")
	if codexInfo == nil || len(codexInfo.UsageWindows) != 1 {
		t.Fatalf("codex UsageWindows: %#v", codexInfo)
	}
	window := codexInfo.UsageWindows[0]
	if window.Name != "30d" || window.Source != logDir || !window.Fresh {
		t.Fatalf("usage window metadata: %#v", window)
	}
	if window.InputTokens != 15 || window.OutputTokens != 6 || window.TotalTokens != 21 {
		t.Fatalf("usage totals must come only from service-owned codex logs: %#v", window)
	}
	if window.CacheReadTokens != 3 || window.CacheWriteTokens != 2 || window.KnownCostUSD != nil || window.CostUSD != 0 || window.UnknownCostSessions != 1 {
		t.Fatalf("usage cost/cache projection: %#v", window)
	}
}

func TestListHarnesses_GeminiAccountAndUsageWindows(t *testing.T) {
	dir := t.TempDir()
	isolateStatusHome(t, dir)
	logDir := filepath.Join(dir, ".fizeau", "sessions")
	t.Setenv("GOOGLE_GENAI_USE_GCA", "")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("GOOGLE_API_KEY", "test-key")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("CLOUD_SHELL", "")
	t.Setenv("GEMINI_CLI_USE_COMPUTE_ADC", "")
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", filepath.Join(dir, "missing-gemini-quota.json"))

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	writePublicUsageSession(t, logDir, "gemini-known", todayStart, fizeau.SessionStartData{
		Provider: "gemini",
		Model:    "gemini-2.5-flash",
	}, fizeau.SessionEndData{
		Status:     fizeau.StatusSuccess,
		Tokens:     fizeau.TokenUsage{Input: 21, Output: 3, Total: 24, CacheRead: 5},
		CostUSD:    publicUsageCostPtr(0.02),
		DurationMs: 1000,
		Model:      "gemini-2.5-flash",
	})

	infos, err := newStatusFacade(t, &statusConfig{sessionLogDir: logDir}).ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}
	geminiInfo := findPublicHarnessInfo(infos, "gemini")
	if geminiInfo == nil || geminiInfo.Quota == nil {
		t.Fatalf("gemini status: %#v", geminiInfo)
	}
	if geminiInfo.Quota.Status != "unavailable" || geminiInfo.Quota.LastError == nil || geminiInfo.Quota.LastError.Type != "unavailable" {
		t.Fatalf("gemini quota without a cache: %#v", geminiInfo.Quota)
	}
	if geminiInfo.Account == nil || !geminiInfo.Account.Authenticated || geminiInfo.Account.PlanType != "Gemini API key" {
		t.Fatalf("gemini account: %#v", geminiInfo.Account)
	}
	if len(geminiInfo.UsageWindows) != 3 {
		t.Fatalf("gemini UsageWindows = %#v, want today, 7d, and 30d", geminiInfo.UsageWindows)
	}
	for _, name := range []string{"today", "7d", "30d"} {
		window := findPublicUsageWindow(geminiInfo.UsageWindows, name)
		if window == nil {
			t.Fatalf("gemini %s usage window missing: %#v", name, geminiInfo.UsageWindows)
		}
		if window.Source != logDir || !window.Fresh || window.InputTokens != 21 || window.OutputTokens != 3 || window.TotalTokens != 24 || window.CacheReadTokens != 5 {
			t.Fatalf("gemini %s usage window: %#v", name, window)
		}
		if window.KnownCostUSD == nil || *window.KnownCostUSD != 0.02 || window.CostUSD != 0.02 || window.UnknownCostSessions != 0 {
			t.Fatalf("gemini %s cost projection: %#v", name, window)
		}
	}
}

func TestReferenceConsumerDoctorReportUsesServiceStatus(t *testing.T) {
	svc := newProviderFacade(t, &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"openrouter": {Type: "openrouter", BaseURL: "http://127.0.0.1:1/v1", Model: "model-a"},
		},
		names:       []string{"openrouter"},
		defaultName: "openrouter",
	})

	providers, err := svc.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	routes, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}

	var report strings.Builder
	for _, provider := range providers {
		report.WriteString(provider.Name)
		report.WriteString(":")
		report.WriteString(provider.EndpointStatus[0].Status)
		report.WriteString("\n")
	}
	for _, route := range routes.Routes {
		report.WriteString(route.Model)
		report.WriteString(":")
		report.WriteString(route.Strategy)
		report.WriteString("\n")
	}
	if got := report.String(); !strings.Contains(got, "openrouter:") || !strings.Contains(got, "model-a:auto") {
		t.Fatalf("doctor report missing service data: %q", got)
	}
}

type statusConfig struct {
	sessionLogDir string
}

func (*statusConfig) ProviderNames() []string { return nil }
func (*statusConfig) DefaultProviderName() string {
	return ""
}
func (*statusConfig) Provider(string) (fizeau.ServiceProviderEntry, bool) {
	return fizeau.ServiceProviderEntry{}, false
}
func (*statusConfig) HealthCooldown() time.Duration { return 0 }
func (*statusConfig) WorkDir() string               { return "" }
func (c *statusConfig) SessionLogDir() string       { return c.sessionLogDir }

func newStatusFacade(t *testing.T, config *statusConfig) fizeau.FizeauService {
	t.Helper()
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       config,
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func isolateStatusHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, ".state"))
}

func writeStatusJSONFixture(t *testing.T, path string, fixture any) {
	t.Helper()
	payload, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("marshal status fixture: %v", err)
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir status fixture: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write status fixture: %v", err)
	}
}

func writePublicUsageSession(t *testing.T, logDir, sessionID string, startAt time.Time, start fizeau.SessionStartData, end fizeau.SessionEndData) {
	t.Helper()
	logger := fizeau.NewSessionLogger(logDir, sessionID)
	startEvent := fizeau.NewSessionEvent(sessionID, 0, fizeau.EventSessionStart, start)
	startEvent.Timestamp = startAt
	logger.Write(startEvent)
	endEvent := fizeau.NewSessionEvent(sessionID, 1, fizeau.EventSessionEnd, end)
	endEvent.Timestamp = startAt.Add(time.Second)
	logger.Write(endEvent)
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}

func publicUsageCostPtr(value float64) *float64 {
	return &value
}

func findPublicHarnessInfo(infos []fizeau.HarnessInfo, name string) *fizeau.HarnessInfo {
	for i := range infos {
		if infos[i].Name == name {
			return &infos[i]
		}
	}
	return nil
}

func findPublicUsageWindow(windows []fizeau.UsageWindow, name string) *fizeau.UsageWindow {
	for i := range windows {
		if windows[i].Name == name {
			return &windows[i]
		}
	}
	return nil
}
