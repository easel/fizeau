package claude

import (
	"fmt"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

// claudeQuotaSnapshot is now a type alias to the neutral anthropic version.
type claudeQuotaSnapshot = anthropic.ClaudeQuotaSnapshot

// defaultClaudeQuotaStaleAfter references the anthropic package constant.
const defaultClaudeQuotaStaleAfter = anthropic.DefaultClaudeQuotaStaleAfter

// claudeQuotaCacheEnv lets tests override the cache file path.
const claudeQuotaCacheEnv = "FIZEAU_CLAUDE_QUOTA_CACHE"

// claudeQuotaCachePath is now delegated to the anthropic package.
func claudeQuotaCachePath() (string, error) {
	return anthropic.ClaudeQuotaCachePath()
}

// writeClaudeQuota is now delegated to the anthropic package.
func writeClaudeQuota(path string, snapshot claudeQuotaSnapshot) error {
	return anthropic.WriteClaudeQuota(path, snapshot)
}

// readClaudeQuotaFrom is now delegated to the anthropic package.
func readClaudeQuotaFrom(path string) (*claudeQuotaSnapshot, bool) {
	return anthropic.ReadClaudeQuotaFrom(path)
}

// readClaudeQuota reads the cached snapshot from the default location.
// The second return value is false if no snapshot is present or cannot be
// decoded.
//
// Callers SHOULD check snapshot age via claudeQuotaSnapshotAge (or
// isClaudeQuotaFresh) before trusting the values; this function does not
// itself enforce a TTL so that callers can report stale snapshots in
// diagnostic surfaces like `ddx agent doctor --routing`.
func readClaudeQuota() (*claudeQuotaSnapshot, bool) {
	return anthropic.ReadClaudeQuota()
}

// claudeQuotaSnapshotAge reports the age of a snapshot relative to now.
// A zero or future CapturedAt yields a zero age.
func claudeQuotaSnapshotAge(snapshot *claudeQuotaSnapshot, now time.Time) time.Duration {
	return anthropic.ClaudeQuotaSnapshotAge(snapshot, now)
}

// isClaudeQuotaFresh reports whether a snapshot exists and is newer than
// staleAfter relative to now. A nil snapshot is never fresh. A zero
// staleAfter falls back to defaultClaudeQuotaStaleAfter.
func isClaudeQuotaFresh(snapshot *claudeQuotaSnapshot, now time.Time, staleAfter time.Duration) bool {
	return anthropic.IsClaudeQuotaFresh(snapshot, now, staleAfter)
}

// claudeQuotaRoutingDecision summarizes what foreground routing should do
// given the current cached snapshot.
type claudeQuotaRoutingDecision struct {
	// PreferClaude is true when a fresh snapshot shows headroom in both the
	// 5-hour and weekly windows. When false, routing should prefer a
	// non-claude fallback harness.
	PreferClaude bool
	// SnapshotPresent is true when a snapshot was found in the cache (even
	// if stale).
	SnapshotPresent bool
	// Fresh is true when the snapshot is present and newer than staleAfter.
	Fresh bool
	// Age is the age of the snapshot relative to now (zero when absent).
	Age time.Duration
	// Snapshot is the cached snapshot when present.
	Snapshot *claudeQuotaSnapshot
	// Reason describes why the decision was made (diagnostic surface).
	Reason string
}

// decideClaudeQuotaRouting turns a cached snapshot into a routing decision
// for foreground callers. When the snapshot is missing or stale, the safe
// default is NOT to prefer claude (assume limited).
//
// A snapshot counts as "limited" when either window reports zero or
// negative remaining headroom.
func decideClaudeQuotaRouting(snapshot *claudeQuotaSnapshot, now time.Time, staleAfter time.Duration) claudeQuotaRoutingDecision {
	decision := claudeQuotaRoutingDecision{
		Snapshot: snapshot,
	}
	if snapshot == nil {
		decision.Reason = "no cached snapshot; assume limited"
		return decision
	}
	decision.SnapshotPresent = true
	decision.Age = claudeQuotaSnapshotAge(snapshot, now)
	if !isClaudeQuotaFresh(snapshot, now, staleAfter) {
		decision.Reason = "cached snapshot is stale; assume limited"
		return decision
	}
	if err := validateClaudeQuotaSnapshotForRouting(snapshot); err != nil {
		decision.Reason = "cached snapshot is incomplete: " + err.Error() + "; assume limited"
		return decision
	}
	decision.Fresh = true
	if exhausted := exhaustedClaudeQuotaWindow(snapshot.Windows); exhausted != "" {
		decision.Reason = "fresh snapshot reports exhausted " + exhausted + " window; assume limited"
		return decision
	}
	if snapshot.FiveHourRemaining <= 0 || snapshot.WeeklyRemaining <= 0 {
		decision.Reason = "fresh snapshot reports exhausted window; assume limited"
		return decision
	}
	decision.PreferClaude = true
	decision.Reason = "fresh snapshot has headroom"
	return decision
}

func validateClaudeQuotaSnapshotForRouting(snapshot *claudeQuotaSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("missing snapshot")
	}
	if strings.TrimSpace(snapshot.Source) == "" {
		return fmt.Errorf("missing source")
	}
	if snapshot.Account == nil || strings.TrimSpace(snapshot.Account.PlanType) == "" {
		return fmt.Errorf("missing account plan")
	}
	if snapshot.FiveHourLimit <= 0 {
		return fmt.Errorf("invalid 5h limit")
	}
	if snapshot.WeeklyLimit <= 0 {
		return fmt.Errorf("invalid weekly limit")
	}
	if snapshot.FiveHourRemaining < 0 || snapshot.FiveHourRemaining > snapshot.FiveHourLimit {
		return fmt.Errorf("invalid 5h remaining")
	}
	if snapshot.WeeklyRemaining < 0 || snapshot.WeeklyRemaining > snapshot.WeeklyLimit {
		return fmt.Errorf("invalid weekly remaining")
	}
	return nil
}

func exhaustedClaudeQuotaWindow(windows []harnesses.QuotaWindow) string {
	for _, window := range windows {
		if !claudeQuotaWindowExhausted(window) {
			continue
		}
		if strings.TrimSpace(window.LimitID) != "" {
			return window.LimitID
		}
		if strings.TrimSpace(window.Name) != "" {
			return window.Name
		}
		return "unknown"
	}
	return ""
}

func claudeQuotaWindowExhausted(window harnesses.QuotaWindow) bool {
	state := strings.ToLower(strings.TrimSpace(window.State))
	return state == "exhausted" || window.UsedPercent >= 100
}

// isClaudeQuotaExhaustedMessage is now delegated to the anthropic package.
func isClaudeQuotaExhaustedMessage(text string) bool {
	return anthropic.IsClaudeQuotaExhaustedMessage(text)
}

// markClaudeQuotaExhaustedFromMessage is now delegated to the anthropic package.
func markClaudeQuotaExhaustedFromMessage(text string, now time.Time) bool {
	return anthropic.MarkClaudeQuotaExhaustedFromMessage(text, now)
}
