package fizeau

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestResolveExplicitClaudeRejectedWhenFreshQuotaExhausted(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", cachePath)

	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour).Unix()
	writeClaudeQuotaCacheFile(t, cachePath, claudeTestQuotaSnapshot{
		CapturedAt:        now,
		FiveHourRemaining: 0,
		FiveHourLimit:     100,
		WeeklyRemaining:   0,
		WeeklyLimit:       100,
		Windows: []harnesses.QuotaWindow{{
			Name:         "Current week (all models)",
			LimitID:      "weekly-all",
			UsedPercent:  100,
			ResetsAtUnix: reset,
			State:        "exhausted",
		}},
		Source:  "runtime_error",
		Account: &harnesses.AccountInfo{PlanType: "Claude Max"},
	})

	svc := testRoutingErrorService()
	_, err := svc.resolveExecuteRoute(ServiceExecuteRequest{Harness: "claude", Model: "opus-4.7"})
	if err == nil {
		t.Fatal("expected exhausted Claude quota to reject explicit claude route")
	}
	var quotaErr *NoViableProviderForNow
	if !errors.As(err, &quotaErr) {
		t.Fatalf("error=%T %v, want NoViableProviderForNow", err, err)
	}
	if !slices.Equal(quotaErr.ExhaustedProviders, []string{"claude"}) {
		t.Fatalf("ExhaustedProviders=%v, want [claude]", quotaErr.ExhaustedProviders)
	}
	if got := quotaErr.RetryAfter.Unix(); got != reset {
		t.Fatalf("RetryAfter unix=%d, want %d", got, reset)
	}
}

func testRoutingErrorService() *service {
	registry := harnesses.NewRegistry()
	registry.LookPath = func(file string) (string, error) { return "/bin/" + file, nil }
	return &service{
		opts:     ServiceOptions{},
		registry: registry,
		hub:      newSessionHub(),
	}
}
