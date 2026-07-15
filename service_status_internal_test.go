package fizeau

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	sessionlog "github.com/easel/fizeau/internal/session"
)

// TestBuildRoutingInputs_IgnoresCodexUsageWindows is intentionally retained
// as a narrow root composition seam: public ListHarnesses reports historical
// usage, but that presentation evidence must never become quota health in the
// private routing input assembled by the facade.
func TestBuildRoutingInputs_IgnoresCodexUsageWindows(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".fizeau", "sessions")
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))
	writeRoutingCompositionUsageSession(t, logDir, "codex-usage", time.Now().UTC().Add(-time.Hour), sessionlog.SessionStartData{
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

func writeRoutingCompositionUsageSession(t *testing.T, logDir, sessionID string, startAt time.Time, start sessionlog.SessionStartData, end sessionlog.SessionEndData) {
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
