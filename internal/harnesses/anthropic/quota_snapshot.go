package anthropic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/productinfo"
)

// ClaudeQuotaSnapshot captures Claude's current-quota headroom as absolute
// token/message counts. It is written to a durable per-user cache by an
// asynchronous capture path and read by foreground routing consumers.
//
// The snapshot is intentionally distinct from the percentage-based
// QuotaSignal: foreground routing needs concrete numbers to reason about
// 5-hour / weekly headroom without invoking PTY capture inline.
type ClaudeQuotaSnapshot struct {
	CapturedAt        time.Time               `json:"captured_at"`
	FiveHourRemaining int                     `json:"five_hour_remaining"`
	FiveHourLimit     int                     `json:"five_hour_limit"`
	WeeklyRemaining   int                     `json:"weekly_remaining"`
	WeeklyLimit       int                     `json:"weekly_limit"`
	Windows           []harnesses.QuotaWindow `json:"windows,omitempty"`
	Source            string                  `json:"source"` // e.g. "pty", "heuristic"
	Account           *harnesses.AccountInfo  `json:"account,omitempty"`
}

// defaultClaudeQuotaStaleAfter is the default maximum age before a cached
// snapshot is considered stale and foreground routing should fall back to
// the safe default.
const DefaultClaudeQuotaStaleAfter = 15 * time.Minute

// claudeQuotaCacheEnv lets tests override the cache file path.
const claudeQuotaCacheEnv = "FIZEAU_CLAUDE_QUOTA_CACHE"

func claudeQuotaCachePathImpl() (string, error) {
	if path := os.Getenv(claudeQuotaCacheEnv); path != "" {
		return path, nil
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, productinfo.ConfigDir, "claude-quota.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", productinfo.ConfigDir, "claude-quota.json"), nil
}

// WriteClaudeQuota atomically persists a ClaudeQuotaSnapshot to the given
// path. The parent directory is created if necessary. The file is written
// to a sibling .tmp file and renamed into place so readers never observe a
// partially-written snapshot. The final file mode is 0600.
func WriteClaudeQuota(path string, snapshot ClaudeQuotaSnapshot) error {
	if path == "" {
		return fmt.Errorf("claude quota cache path is empty")
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	} else {
		snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create claude quota cache dir: %w", err)
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claude quota snapshot: %w", err)
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write claude quota cache tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename claude quota cache: %w", err)
	}
	// Ensure final mode is 0600 in case an older file had a different mode.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod claude quota cache: %w", err)
	}
	return nil
}

// ReadClaudeQuotaFrom reads the snapshot at the given path. Returns
// (nil, false) if the file does not exist or cannot be decoded. Non-
// existence is NOT an error: foreground callers are expected to fall back
// to a safe default when no snapshot is present.
func ReadClaudeQuotaFrom(path string) (*ClaudeQuotaSnapshot, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var snap ClaudeQuotaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	return &snap, true
}

// ReadClaudeQuota reads the cached snapshot from the default location.
// The second return value is false if no snapshot is present or cannot be
// decoded.
//
// Callers SHOULD check snapshot age via ClaudeQuotaSnapshotAge (or
// IsClaudeQuotaFresh) before trusting the values; this function does not
// itself enforce a TTL so that callers can report stale snapshots in
// diagnostic surfaces like `ddx agent doctor --routing`.
func ReadClaudeQuota() (*ClaudeQuotaSnapshot, bool) {
	path, err := claudeQuotaCachePathImpl()
	if err != nil {
		return nil, false
	}
	return ReadClaudeQuotaFrom(path)
}

// ClaudeQuotaSnapshotAge reports the age of a snapshot relative to now.
// A zero or future CapturedAt yields a zero age.
func ClaudeQuotaSnapshotAge(snapshot *ClaudeQuotaSnapshot, now time.Time) time.Duration {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return 0
	}
	age := now.UTC().Sub(snapshot.CapturedAt.UTC())
	if age < 0 {
		return 0
	}
	return age
}

// IsClaudeQuotaFresh reports whether a snapshot exists and is newer than
// staleAfter relative to now. A nil snapshot is never fresh. A zero
// staleAfter falls back to DefaultClaudeQuotaStaleAfter.
func IsClaudeQuotaFresh(snapshot *ClaudeQuotaSnapshot, now time.Time, staleAfter time.Duration) bool {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return false
	}
	if staleAfter <= 0 {
		staleAfter = DefaultClaudeQuotaStaleAfter
	}
	return ClaudeQuotaSnapshotAge(snapshot, now) <= staleAfter
}

// ClaudeQuotaCachePath returns the durable location for the Claude quota cache.
func ClaudeQuotaCachePath() (string, error) {
	return claudeQuotaCachePathImpl()
}

// Internal wrappers for backward compatibility with claude package
func claudeQuotaCachePath() (string, error) {
	return ClaudeQuotaCachePath()
}

func writeClaudeQuota(path string, snapshot ClaudeQuotaSnapshot) error {
	return WriteClaudeQuota(path, snapshot)
}
