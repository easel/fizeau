package grok

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/productinfo"
	"github.com/easel/fizeau/internal/safefs"
)

// grokQuotaSnapshot captures grok subscription quota windows in a durable
// cache so foreground service status calls do not need to spawn a live PTY
// probe.
type grokQuotaSnapshot struct {
	CapturedAt time.Time               `json:"captured_at"`
	Windows    []harnesses.QuotaWindow `json:"windows"`
	Source     string                  `json:"source"`
	Account    *harnesses.AccountInfo  `json:"account,omitempty"`
}

const defaultGrokQuotaStaleAfter = 15 * time.Minute

const grokQuotaCacheEnv = "FIZEAU_GROK_QUOTA_CACHE"

// grokQuotaCachePath returns the durable location for the grok quota cache.
func grokQuotaCachePath() (string, error) {
	if path := os.Getenv(grokQuotaCacheEnv); path != "" {
		return path, nil
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, productinfo.ConfigDir, "grok-quota.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", productinfo.ConfigDir, "grok-quota.json"), nil
}

// writeGrokQuota atomically persists a grokQuotaSnapshot to path.
func writeGrokQuota(path string, snapshot grokQuotaSnapshot) error {
	if path == "" {
		return fmt.Errorf("grok quota cache path is empty")
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	} else {
		snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	}
	if snapshot.Source == "" {
		snapshot.Source = "pty"
	}
	if snapshot.Account == nil {
		if account, ok := readGrokAccount(); ok {
			snapshot.Account = account
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create grok quota cache dir: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal grok quota snapshot: %w", err)
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write grok quota cache tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename grok quota cache: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod grok quota cache: %w", err)
	}
	return nil
}

// readGrokQuotaFrom reads one grok quota snapshot.
func readGrokQuotaFrom(path string) (*grokQuotaSnapshot, bool) {
	data, err := safefs.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var snap grokQuotaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	return &snap, true
}

// readGrokQuota reads the default grok quota cache.
func readGrokQuota() (*grokQuotaSnapshot, bool) {
	path, err := grokQuotaCachePath()
	if err != nil {
		return nil, false
	}
	return readGrokQuotaFrom(path)
}

// grokQuotaSnapshotAge reports snapshot age relative to now.
func grokQuotaSnapshotAge(snapshot *grokQuotaSnapshot, now time.Time) time.Duration {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return 0
	}
	age := now.UTC().Sub(snapshot.CapturedAt.UTC())
	if age < 0 {
		return 0
	}
	return age
}

// isGrokQuotaFresh reports whether a snapshot is present and fresh.
func isGrokQuotaFresh(snapshot *grokQuotaSnapshot, now time.Time, staleAfter time.Duration) bool {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return false
	}
	if staleAfter <= 0 {
		staleAfter = defaultGrokQuotaStaleAfter
	}
	return grokQuotaSnapshotAge(snapshot, now) <= staleAfter
}

// grokQuotaRoutingDecision summarises whether foreground routing may select
// grok without probing the CLI inline.
type grokQuotaRoutingDecision struct {
	PreferGrok      bool
	SnapshotPresent bool
	Fresh           bool
	Age             time.Duration
	Snapshot        *grokQuotaSnapshot
	Reason          string
}

// decideGrokQuotaRouting turns a durable quota snapshot into a foreground
// routing decision. Missing, stale, empty, or blocked quota evidence keeps
// grok out of automatic routing; explicit Harness=grok remains available.
func decideGrokQuotaRouting(snapshot *grokQuotaSnapshot, now time.Time, staleAfter time.Duration) grokQuotaRoutingDecision {
	decision := grokQuotaRoutingDecision{Snapshot: snapshot}
	if snapshot == nil {
		decision.Reason = "no cached snapshot; assume limited"
		return decision
	}
	decision.SnapshotPresent = true
	decision.Age = grokQuotaSnapshotAge(snapshot, now)
	if !isGrokQuotaFresh(snapshot, now, staleAfter) {
		decision.Reason = "cached snapshot is stale; assume limited"
		return decision
	}
	decision.Fresh = true
	if len(snapshot.Windows) == 0 {
		decision.Reason = "fresh snapshot has no quota windows; assume limited"
		return decision
	}
	if !grokAccountSupportsAutoRouting(snapshot.Account) {
		decision.Reason = "fresh snapshot has no subsidized account plan; assume limited"
		return decision
	}
	for _, window := range snapshot.Windows {
		if window.State == "blocked" || window.UsedPercent >= 95 {
			decision.Reason = "fresh snapshot reports blocked window; assume limited"
			return decision
		}
	}
	decision.PreferGrok = true
	decision.Reason = "fresh snapshot has headroom"
	return decision
}
