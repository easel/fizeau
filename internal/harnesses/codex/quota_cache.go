package codex

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

// codexQuotaSnapshot captures Codex subscription quota windows in a durable
// cache so foreground service status calls do not need to spawn a live PTY probe.
type codexQuotaSnapshot struct {
	CapturedAt time.Time               `json:"captured_at"`
	Windows    []harnesses.QuotaWindow `json:"windows"`
	Source     string                  `json:"source"`
	Account    *harnesses.AccountInfo  `json:"account,omitempty"`
}

const defaultCodexQuotaStaleAfter = 15 * time.Minute

const codexQuotaCacheEnv = "FIZEAU_CODEX_QUOTA_CACHE"

// codexQuotaCachePath returns the durable location for the Codex quota cache.
func codexQuotaCachePath() (string, error) {
	if path := os.Getenv(codexQuotaCacheEnv); path != "" {
		return path, nil
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, productinfo.ConfigDir, "codex-quota.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", productinfo.ConfigDir, "codex-quota.json"), nil
}

// writeCodexQuota atomically persists a codexQuotaSnapshot to path.
func writeCodexQuota(path string, snapshot codexQuotaSnapshot) error {
	if path == "" {
		return fmt.Errorf("codex quota cache path is empty")
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
		if account, ok := readCodexAccount(); ok {
			snapshot.Account = account
		}
	}
	for i := range snapshot.Windows {
		if snapshot.Windows[i].LimitID == "" {
			snapshot.Windows[i].LimitID = defaultCodexLimitID(snapshot.Windows[i].WindowMinutes)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create codex quota cache dir: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal codex quota snapshot: %w", err)
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write codex quota cache tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename codex quota cache: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod codex quota cache: %w", err)
	}
	return nil
}

// readCodexQuotaFrom reads one Codex quota snapshot.
func readCodexQuotaFrom(path string) (*codexQuotaSnapshot, bool) {
	data, err := safefs.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var snap codexQuotaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	return &snap, true
}

// readCodexQuota reads the default Codex quota cache.
func readCodexQuota() (*codexQuotaSnapshot, bool) {
	path, err := codexQuotaCachePath()
	if err != nil {
		return nil, false
	}
	return readCodexQuotaFrom(path)
}

// codexQuotaSnapshotAge reports snapshot age relative to now.
func codexQuotaSnapshotAge(snapshot *codexQuotaSnapshot, now time.Time) time.Duration {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return 0
	}
	age := now.UTC().Sub(snapshot.CapturedAt.UTC())
	if age < 0 {
		return 0
	}
	return age
}

// isCodexQuotaFresh reports whether a snapshot is present and fresh.
func isCodexQuotaFresh(snapshot *codexQuotaSnapshot, now time.Time, staleAfter time.Duration) bool {
	if snapshot == nil || snapshot.CapturedAt.IsZero() {
		return false
	}
	if staleAfter <= 0 {
		staleAfter = defaultCodexQuotaStaleAfter
	}
	return codexQuotaSnapshotAge(snapshot, now) <= staleAfter
}

// codexQuotaRoutingDecision summarises whether foreground routing may select
// Codex without probing the CLI inline.
type codexQuotaRoutingDecision struct {
	PreferCodex     bool
	SnapshotPresent bool
	Fresh           bool
	Age             time.Duration
	Snapshot        *codexQuotaSnapshot
	Reason          string
}

func defaultCodexLimitID(windowMinutes int) string {
	switch windowMinutes {
	case 300:
		return "codex"
	case 10080:
		return "codex-weekly"
	default:
		return "codex"
	}
}

// decideCodexQuotaRouting turns a durable quota snapshot into a foreground
// routing decision. Missing, stale, empty, or blocked quota evidence keeps
// Codex out of automatic routing; explicit Harness=codex remains available.
func decideCodexQuotaRouting(snapshot *codexQuotaSnapshot, now time.Time, staleAfter time.Duration) codexQuotaRoutingDecision {
	decision := codexQuotaRoutingDecision{Snapshot: snapshot}
	if snapshot == nil {
		decision.Reason = "no cached snapshot; assume limited"
		return decision
	}
	decision.SnapshotPresent = true
	decision.Age = codexQuotaSnapshotAge(snapshot, now)
	if !isCodexQuotaFresh(snapshot, now, staleAfter) {
		decision.Reason = "cached snapshot is stale; assume limited"
		return decision
	}
	decision.Fresh = true
	if len(snapshot.Windows) == 0 {
		decision.Reason = "fresh snapshot has no quota windows; assume limited"
		return decision
	}
	if !codexAccountSupportsAutoRouting(snapshot.Account) {
		decision.Reason = "fresh snapshot has no subsidized account plan; assume limited"
		return decision
	}
	for _, window := range snapshot.Windows {
		if window.State == "blocked" || window.UsedPercent >= 95 {
			decision.Reason = "fresh snapshot reports blocked window; assume limited"
			return decision
		}
	}
	decision.PreferCodex = true
	decision.Reason = "fresh snapshot has headroom"
	return decision
}
