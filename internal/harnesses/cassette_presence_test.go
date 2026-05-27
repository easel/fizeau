package harnesses

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	DefaultCassetteTTL = 30 * 24 * time.Hour
)

// cassetteFreshnessConfig holds the freshness requirements for cassettes.
type cassetteFreshnessConfig struct {
	ttl time.Duration
}

// TestCassettePresenceAndFreshness asserts that every harness with a TUI model
// surface (codex, claude, gemini, opencode, pi) has a testdata/model_surface/
// cassette checked in and that CapturedAt is within the configured TTL
// (default 30 days). Stale fixtures fail because they no longer prove the
// parser works against current TUI output.
func TestCassettePresenceAndFreshness(t *testing.T) {
	now := time.Now()
	cfg := &cassetteFreshnessConfig{ttl: DefaultCassetteTTL}

	harnesses := []string{"claude", "codex", "gemini", "opencode", "pi"}

	for _, name := range harnesses {
		t.Run(name, func(t *testing.T) {
			modelSurfaceDir := filepath.Join("testdata", "model_surface")
			harnessDir := filepath.Join(name, modelSurfaceDir)

			// Check directory exists.
			_, err := os.Stat(harnessDir)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					t.Fatalf("harness %q missing model_surface cassette directory at %s", name, harnessDir)
				}
				t.Fatalf("failed to stat cassette directory: %v", err)
			}

			// All harnesses must have discovery.json with captured_at timestamp.
			checkDiscoveryFreshness(t, harnessDir, name, now, cfg)
		})
	}
}

func checkDiscoveryFreshness(t *testing.T, dir string, name string, now time.Time, cfg *cassetteFreshnessConfig) {
	t.Helper()

	discoveryPath := filepath.Join(dir, "discovery.json")
	data, err := os.ReadFile(discoveryPath)
	if err != nil {
		t.Fatalf("harness %q: failed to read discovery.json: %v", name, err)
	}

	var discovery struct {
		CapturedAt string `json:"captured_at"`
	}
	if err := json.Unmarshal(data, &discovery); err != nil {
		t.Fatalf("harness %q: failed to parse discovery.json: %v", name, err)
	}

	if discovery.CapturedAt == "" {
		t.Fatalf("harness %q: discovery.json missing captured_at field", name)
	}

	capturedAt, err := time.Parse(time.RFC3339, discovery.CapturedAt)
	if err != nil {
		t.Fatalf("harness %q: failed to parse captured_at timestamp: %v", name, err)
	}

	age := now.Sub(capturedAt)
	if age > cfg.ttl {
		t.Fatalf("harness %q: cassette stale: captured %v ago (TTL: %v). "+
			"To re-record, run: FIZEAU_HARNESS_RECORD=1 go test -tags integration ./internal/harnesses/... -run DiscoveryRecord%s",
			name, age, cfg.ttl, capitalizeName(name))
	}
}

func capitalizeName(s string) string {
	if len(s) == 0 {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
