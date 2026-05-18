package anthropic

import (
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// IsClaudeQuotaExhaustedMessage recognizes Claude CLI quota failures that are
// emitted as plain text rather than structured quota data. Claude currently
// reports weekly exhaustion with wording like "out of extra usage", so callers
// must treat these strings as a hard quota signal.
func IsClaudeQuotaExhaustedMessage(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(normalized, "out of extra usage") ||
		strings.Contains(normalized, "usage limit reached") ||
		strings.Contains(normalized, "quota exhausted") ||
		strings.Contains(normalized, "weekly quota") ||
		strings.Contains(normalized, "current week") && strings.Contains(normalized, "exhaust")
}

// MarkClaudeQuotaExhaustedFromMessage records a runtime Claude quota failure
// in the durable cache so later automatic routing avoids Claude until a fresh
// quota probe proves headroom again.
func MarkClaudeQuotaExhaustedFromMessage(text string, now time.Time) bool {
	if !IsClaudeQuotaExhaustedMessage(text) {
		return false
	}
	path, err := claudeQuotaCachePathImpl()
	if err != nil {
		return true
	}
	if now.IsZero() {
		now = time.Now()
	}
	snap := ClaudeQuotaSnapshot{
		CapturedAt:        now.UTC(),
		FiveHourLimit:     100,
		FiveHourRemaining: 0,
		WeeklyLimit:       100,
		WeeklyRemaining:   0,
		Windows: []harnesses.QuotaWindow{
			{Name: "Current week (all models)", LimitID: "weekly-all", WindowMinutes: 10080, UsedPercent: 100, State: "exhausted"},
			{Name: "Extra usage", LimitID: "extra", UsedPercent: 100, State: "exhausted"},
		},
		Source:  "runtime_error",
		Account: &harnesses.AccountInfo{PlanType: "unknown"},
	}
	_ = writeClaudeQuota(path, snap)
	return true
}
