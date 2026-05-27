//go:build harness_integration

package harnesses

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestModelDiscoveryDriftDetection runs against the live authenticated CLI,
// captures a fresh snapshot, and diffs against the checked-in cassette.
// Mismatch fails with a re-record instruction. Missing credentials emit a
// structured SKIP.
//
// This test must be run with FIZEAU_HARNESS_DRIFT_CHECK=1 environment variable
// to execute. It requires authenticated credentials for each harness CLI.
//
// Subtest implementations are defined in each harness subpackage.
func TestModelDiscoveryDriftDetection(t *testing.T) {
	// Skip if we're not running in a harness_integration context with explicit opt-in.
	if os.Getenv("FIZEAU_HARNESS_DRIFT_CHECK") != "1" {
		t.Skip("set FIZEAU_HARNESS_DRIFT_CHECK=1 to enable live drift detection")
	}

	// Run registered drift checks for each harness.
	for _, name := range []string{"claude", "codex", "gemini", "opencode", "pi"} {
		t.Run(name, func(t *testing.T) {
			checkFunc, ok := driftCheckRegistry[name]
			if !ok {
				t.Skipf("no drift check registered for harness %q", name)
			}
			checkFunc(t)
		})
	}
}

// driftCheckRegistry holds registered drift check functions.
// Each harness subpackage registers its own check function during init().
var driftCheckRegistry = make(map[string]func(t *testing.T))

// RegisterDriftCheck registers a drift check function for a harness.
// Harness subpackages call this during init() to register their checks.
func RegisterDriftCheck(name string, fn func(t *testing.T)) {
	if fn == nil {
		return
	}
	driftCheckRegistry[name] = fn
}

// DiscoverySnapshot represents a model discovery cassette snapshot.
type DiscoverySnapshot struct {
	Models          []string `json:"models,omitempty"`
	ReasoningLevels []string `json:"reasoning_levels,omitempty"`
	CapturedAt      string   `json:"captured_at,omitempty"`
	Source          string   `json:"source,omitempty"`
}

// ReadCassettedDiscovery reads a discovery cassette from disk.
func ReadCassettedDiscovery(cassettePath string) (*DiscoverySnapshot, error) {
	discoveryPath := filepath.Join(cassettePath, "discovery.json")
	data, err := os.ReadFile(discoveryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read discovery: %w", err)
	}

	var disc DiscoverySnapshot
	if err := json.Unmarshal(data, &disc); err != nil {
		return nil, fmt.Errorf("failed to parse discovery: %w", err)
	}
	return &disc, nil
}

// DiscoveryMatches compares two discovery snapshots for equality (order-independent).
func DiscoveryMatches(expected, actual *DiscoverySnapshot) bool {
	if expected == nil && actual == nil {
		return true
	}
	if expected == nil || actual == nil {
		return false
	}

	return stringSlicesEqual(expected.Models, actual.Models) &&
		stringSlicesEqual(expected.ReasoningLevels, actual.ReasoningLevels)
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool)
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}
