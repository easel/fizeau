package fizeau

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
	sessionlog "github.com/easel/fizeau/internal/session"
)

func TestListHarnesses_QuotaAndAccountStatus(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude-quota.json")
	codexPath := filepath.Join(dir, "codex-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", claudePath)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", codexPath)

	capturedAt := time.Now().UTC().Add(-time.Minute)
	writeClaudeQuotaCacheFile(t, claudePath, claudeTestQuotaSnapshot{
		CapturedAt:        capturedAt,
		FiveHourRemaining: 80,
		FiveHourLimit:     100,
		WeeklyRemaining:   90,
		WeeklyLimit:       100,
		Source:            "pty",
		Account:           &harnesses.AccountInfo{PlanType: "Claude Max"},
		Windows: []harnesses.QuotaWindow{
			{Name: "5h", LimitID: "session", WindowMinutes: 300, UsedPercent: 20, State: "ok"},
			{Name: "weekly-all", LimitID: "weekly-all", WindowMinutes: 10080, UsedPercent: 10, State: "ok"},
		},
	})
	writeCodexQuotaCacheFile(t, codexPath, capturedAt, "pty",
		nil,
		[]harnesses.QuotaWindow{
			{Name: "5h", LimitID: "codex", WindowMinutes: 300, UsedPercent: 20, State: "ok"},
		},
	)

	svc := newTestService(t, ServiceOptions{})
	harnesses, err := svc.ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}

	claudeInfo := findHarnessInfo(harnesses, "claude")
	if claudeInfo == nil || claudeInfo.Quota == nil {
		t.Fatalf("expected claude quota, got %#v", claudeInfo)
	}
	if claudeInfo.Quota.Source != "pty" || claudeInfo.Quota.Status != "ok" || !claudeInfo.Quota.Fresh {
		t.Fatalf("claude quota status: %#v", claudeInfo.Quota)
	}
	if claudeInfo.Account == nil || !claudeInfo.Account.Authenticated || claudeInfo.Account.PlanType != "Claude Max" {
		t.Fatalf("claude account: %#v", claudeInfo.Account)
	}

	codexInfo := findHarnessInfo(harnesses, "codex")
	if codexInfo == nil || codexInfo.Quota == nil {
		t.Fatalf("expected codex quota, got %#v", codexInfo)
	}
	if codexInfo.Quota.Source != "pty" || codexInfo.Quota.Status != "ok" || len(codexInfo.Quota.Windows) != 1 {
		t.Fatalf("codex quota status: %#v", codexInfo.Quota)
	}
}

func TestListHarnesses_ClaudeQuotaUsesPreservedWindows(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", claudePath)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))

	writeClaudeQuotaCacheFile(t, claudePath, claudeTestQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: 0,
		FiveHourLimit:     100,
		WeeklyRemaining:   0,
		WeeklyLimit:       100,
		Source:            "runtime_error",
		Account:           &harnesses.AccountInfo{PlanType: "unknown"},
		Windows: []harnesses.QuotaWindow{
			{Name: "extra", LimitID: "claude-extra", UsedPercent: 100, State: "exhausted"},
		},
	})

	svc := newTestService(t, ServiceOptions{})
	infos, err := svc.ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}
	claudeInfo := findHarnessInfo(infos, "claude")
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

func TestListHarnesses_CodexUsageWindowsFromDDXSessionLogs(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".fizeau", "sessions")
	t.Setenv("CODEX_HOME", filepath.Join(dir, "private-codex"))
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))
	setFakeCodexHarness(t, newFakeCodexQuotaHarness())
	if err := os.MkdirAll(filepath.Join(dir, "private-codex", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private-codex", "sessions", "private.jsonl"), []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":999999}}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now().UTC().Add(-time.Hour)
	writeServiceUsageSession(t, logDir, "codex-known", start, sessionlog.SessionStartData{
		Provider: "codex",
		Model:    "gpt-5.4",
		Prompt:   "private prompt must not be read by status aggregation",
	}, sessionlog.SessionEndData{
		Status:     agentcore.StatusSuccess,
		Tokens:     agentcore.TokenUsage{Input: 10, Output: 4, Total: 14, CacheRead: 3, CacheWrite: 2},
		CostUSD:    usageCostPtr(0.12),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})
	writeServiceUsageSession(t, logDir, "codex-unknown", start.Add(time.Minute), sessionlog.SessionStartData{
		Provider: "codex",
		Model:    "gpt-5.4",
		Prompt:   "another prompt",
	}, sessionlog.SessionEndData{
		Status:     agentcore.StatusSuccess,
		Tokens:     agentcore.TokenUsage{Input: 5, Output: 2, Total: 7},
		CostUSD:    usageCostPtr(-1),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})
	writeServiceUsageSession(t, logDir, "provider-not-codex", start.Add(2*time.Minute), sessionlog.SessionStartData{
		Provider: "openrouter",
		Model:    "gpt-5.4",
		Prompt:   "not codex",
	}, sessionlog.SessionEndData{
		Status:     agentcore.StatusSuccess,
		Tokens:     agentcore.TokenUsage{Input: 100, Output: 100, Total: 200},
		CostUSD:    usageCostPtr(1),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})

	svc := &service{
		opts:     ServiceOptions{ServiceConfig: &fakeServiceConfig{workDir: dir}},
		registry: harnesses.NewRegistry(),
	}
	harnesses, err := svc.ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}
	codexInfo := findHarnessInfo(harnesses, "codex")
	if codexInfo == nil {
		t.Fatal("missing codex harness")
	}
	if len(codexInfo.UsageWindows) != 1 {
		t.Fatalf("UsageWindows: got %#v", codexInfo.UsageWindows)
	}
	window := codexInfo.UsageWindows[0]
	if window.Name != "30d" || window.Source != logDir || !window.Fresh {
		t.Fatalf("usage window metadata: %#v", window)
	}
	if window.InputTokens != 15 || window.OutputTokens != 6 || window.TotalTokens != 21 {
		t.Fatalf("usage totals should come only from DDx codex logs, not private Codex sessions or other providers: %#v", window)
	}
	if window.CacheReadTokens != 3 || window.CacheWriteTokens != 2 {
		t.Fatalf("cache tokens: %#v", window)
	}
	if window.KnownCostUSD != nil || window.CostUSD != 0 || window.UnknownCostSessions != 1 {
		t.Fatalf("known/unknown cost state: %#v", window)
	}
}

func TestListHarnesses_GeminiAccountAndUsageWindows(t *testing.T) {
	dir := t.TempDir()
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

	// Anchor "now" at midday UTC so the "today" window deterministically
	// contains start (= now - 1h) regardless of wall-clock time-of-day.
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	writeServiceUsageSession(t, logDir, "gemini-known", start, sessionlog.SessionStartData{
		Provider: "gemini",
		Model:    "gemini-2.5-flash",
		Prompt:   "private prompt must not be read by status aggregation",
	}, sessionlog.SessionEndData{
		Status:     agentcore.StatusSuccess,
		Tokens:     agentcore.TokenUsage{Input: 21, Output: 3, Total: 24, CacheRead: 5},
		CostUSD:    usageCostPtr(0.02),
		DurationMs: 1000,
		Model:      "gemini-2.5-flash",
	})
	writeServiceUsageSession(t, logDir, "not-gemini", start.Add(time.Minute), sessionlog.SessionStartData{
		Provider: "codex",
		Model:    "gpt-5.4",
		Prompt:   "not gemini",
	}, sessionlog.SessionEndData{
		Status:     agentcore.StatusSuccess,
		Tokens:     agentcore.TokenUsage{Input: 100, Output: 100, Total: 200},
		CostUSD:    usageCostPtr(1),
		DurationMs: 1000,
		Model:      "gpt-5.4",
	})

	svc := newTestService(t, ServiceOptions{ServiceConfig: &fakeServiceConfig{workDir: dir}}, func(s *service) {
		s.runtime = serviceimpl.NewRuntime(serviceimpl.RuntimeDeps{Now: func() time.Time { return now }})
	})
	harnesses, err := svc.ListHarnesses(context.Background())
	if err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}
	geminiInfo := findHarnessInfo(harnesses, "gemini")
	if geminiInfo == nil {
		t.Fatal("missing gemini harness")
	}
	if geminiInfo.Quota == nil {
		t.Fatal("gemini quota should be populated (missing cache reports unavailable)")
	}
	if geminiInfo.Quota.Status != "unavailable" {
		t.Fatalf("gemini quota without a cache should be unavailable: %#v", geminiInfo.Quota)
	}
	if geminiInfo.Quota.LastError == nil || geminiInfo.Quota.LastError.Type != "unavailable" {
		t.Fatalf("gemini quota unavailable should include a last error: %#v", geminiInfo.Quota)
	}
	if geminiInfo.Account == nil || !geminiInfo.Account.Authenticated || geminiInfo.Account.PlanType != "Gemini API key" {
		t.Fatalf("gemini account: %#v", geminiInfo.Account)
	}
	if len(geminiInfo.UsageWindows) != 3 {
		t.Fatalf("UsageWindows: got %#v", geminiInfo.UsageWindows)
	}
	for _, window := range geminiInfo.UsageWindows {
		if window.Source != logDir || !window.Fresh {
			t.Fatalf("usage window metadata: %#v", window)
		}
		if window.InputTokens != 21 || window.OutputTokens != 3 || window.TotalTokens != 24 || window.CacheReadTokens != 5 {
			t.Fatalf("usage totals should come only from DDx gemini logs, not other providers: %#v", window)
		}
		if window.KnownCostUSD == nil || *window.KnownCostUSD != 0.02 || window.CostUSD != 0.02 || window.UnknownCostSessions != 0 {
			t.Fatalf("known/unknown cost state: %#v", window)
		}
	}
}

func TestBuildRoutingInputs_IgnoresCodexUsageWindows(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".fizeau", "sessions")
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))
	writeServiceUsageSession(t, logDir, "codex-usage", time.Now().UTC().Add(-time.Hour), sessionlog.SessionStartData{
		Provider: "codex",
		Model:    "gpt-5.4",
	}, sessionlog.SessionEndData{
		Status: agentcore.StatusSuccess,
		Tokens: agentcore.TokenUsage{Input: 100, Output: 20, Total: 120},
		Model:  "gpt-5.4",
	})
	svc := &service{
		opts:     ServiceOptions{ServiceConfig: &fakeServiceConfig{workDir: dir}},
		registry: harnesses.NewRegistry(),
	}
	codex := routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "codex")
	if !codex.SubscriptionOK || codex.QuotaOK {
		t.Fatalf("usage logs must not fabricate quota health; got SubscriptionOK=%v QuotaOK=%v", codex.SubscriptionOK, codex.QuotaOK)
	}
}

func TestReferenceConsumerDoctorReportUsesServiceStatus(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"openrouter": {Type: "openrouter", BaseURL: "http://127.0.0.1:1/v1", Model: "model-a"},
		},
		names:       []string{"openrouter"},
		defaultName: "openrouter",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})

	providers, err := svc.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	routes, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}

	var b strings.Builder
	for _, p := range providers {
		b.WriteString(p.Name)
		b.WriteString(":")
		b.WriteString(p.EndpointStatus[0].Status)
		b.WriteString("\n")
	}
	for _, r := range routes.Routes {
		b.WriteString(r.Model)
		b.WriteString(":")
		b.WriteString(r.Strategy)
		b.WriteString("\n")
	}
	report := b.String()
	if !strings.Contains(report, "openrouter:") || !strings.Contains(report, "model-a:auto") {
		t.Fatalf("doctor report missing service data: %q", report)
	}
}

func writeServiceUsageSession(t *testing.T, logDir, sessionID string, startAt time.Time, start sessionlog.SessionStartData, end sessionlog.SessionEndData) {
	t.Helper()
	logger := sessionlog.NewLogger(logDir, sessionID)
	startEvent := sessionlog.NewEvent(sessionID, 0, agentcore.EventSessionStart, start)
	startEvent.Timestamp = startAt
	logger.Write(startEvent)
	endEvent := sessionlog.NewEvent(sessionID, 1, agentcore.EventSessionEnd, end)
	endEvent.Timestamp = startAt.Add(time.Second)
	logger.Write(endEvent)
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}

func usageCostPtr(v float64) *float64 {
	return &v
}

func findHarnessInfo(list []HarnessInfo, name string) *HarnessInfo {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}
