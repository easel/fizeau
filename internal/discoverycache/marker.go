package discoverycache

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// refreshMarker is the tier-2 long-lived marker written when a refresh is
// in progress. Other processes inspect it to decide whether to wait or return
// stale data. JSON payload mirrors ADR-012 §3.
type refreshMarker struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Deadline  time.Time `json:"deadline"`
	LastError string    `json:"last_error,omitempty"`
}

// readMarker reads the .refreshing file for s. Returns (nil, nil) if absent.
func readMarker(path string) (*refreshMarker, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m refreshMarker
	if err := json.Unmarshal(data, &m); err != nil {
		// Corrupt marker — treat as absent so the caller can reclaim.
		return nil, nil
	}
	return &m, nil
}

// writeMarker writes m to path, overwriting any existing file.
func writeMarker(path string, m *refreshMarker) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	// Overwrite (not O_EXCL) because claimRefresh already holds the tier-1 lock.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304
	if err != nil {
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return werr
	}
	return f.Close()
}

// isStale returns true when m should be treated as an orphan.
// A marker is stale when: the owner PID is dead, OR now > deadline + 2×RefreshDeadline.
// The deadline-based check is the safety net for PID reuse (ADR-012 §3, §10 test).
func isStale(s Source, m *refreshMarker) bool {
	if m == nil {
		return true
	}
	if !processAlive(m.PID) {
		return true
	}
	// Failed refreshes use Deadline as a short retry cooldown. They are not
	// active refreshes and must not inherit the source's crash-recovery grace
	// period, which can turn a 250 ms cooldown into minutes of suppression.
	if m.LastError != "" {
		return time.Now().After(m.Deadline)
	}
	// 2× multiplier per ADR-012 "Marker staleness threshold = 2 × refresh deadline"
	threshold := s.RefreshDeadline * stalenessMultiplier
	return time.Now().After(m.Deadline.Add(threshold))
}

// isMarkerActiveForPrune is a conservative check used only by Prune when the
// source's RefreshDeadline is unknown. A marker is active if its PID is alive
// and the deadline itself has not passed (no multiplier).
func isMarkerActiveForPrune(m *refreshMarker) bool {
	if m == nil {
		return false
	}
	return processAlive(m.PID) && time.Now().Before(m.Deadline)
}
