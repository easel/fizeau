package claude

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/stretchr/testify/require"
)

func TestModelSurfaceCassetteReturnsNonEmptyModels(t *testing.T) {
	cassetteDir := filepath.Join("testdata", "model_surface")

	snapshot, err := ReadClaudeModelDiscoveryFromCassette(cassetteDir)
	require.NoError(t, err, "cassette should parse without error")

	// AC2: cassette reader returns non-empty Models slice
	require.NotEmpty(t, snapshot.Models, "models should not be empty")

	// The live picker exposes the active tier reliably; other overlay rows are
	// not stable in the terminal emulator. Require that the recorded tier maps
	// to one of the harness's declared family aliases without prescribing which
	// subscription tier an account must have on recording day.
	aliases := (&Runner{}).SupportedAliases()
	hasSupportedFamily := false
	for _, model := range snapshot.Models {
		for _, alias := range aliases {
			if model == alias || strings.HasPrefix(model, alias+"-") || strings.HasPrefix(model, "claude-"+alias+"-") {
				hasSupportedFamily = true
			}
		}
	}
	require.True(t, hasSupportedFamily, "models should include a declared Claude family")

	// Additional: verify source is recorded
	require.True(t, snapshot.Source == "pty" || snapshot.Source == "cassette", "source should be pty or cassette")
}

func TestModelSurfaceCassetteStructure(t *testing.T) {
	// AC1: cassette structure exists per ADR-002 schema v1
	cassetteDir := filepath.Join("testdata", "model_surface")

	reader, err := cassette.Open(cassetteDir)
	require.NoError(t, err, "cassette should open successfully")

	// Verify manifest loads
	manifest := reader.Manifest()
	require.NotNil(t, manifest, "manifest should exist")
	require.Equal(t, 1, manifest.Version, "manifest version should be 1")
	require.Equal(t, "claude", manifest.Harness.Name, "harness name should be claude")

	// Verify discovery record exists and has models
	discovery := reader.Discovery()
	require.NotNil(t, discovery, "discovery record should exist")
	require.Greater(t, len(discovery.Models), 0, "discovery should have models")
	require.Greater(t, len(discovery.ReasoningLevels), 0, "discovery should have reasoning levels")

	// Verify final record exists
	final := reader.Final()
	require.NotNil(t, final, "final record should exist")
	// The recorder terminates the picker after evidence capture. Older CLIs
	// died from the signal (code -1, Signaled); Claude Code >= 2.1.250 traps
	// it and exits 125 on its own. Either is a recorder-driven shutdown, not
	// a harness failure.
	require.Contains(t, []int{0, -1, 125}, final.Exit.Code, "recorder may terminate the picker after evidence capture")

	// Verify frames exist
	frames := reader.Frames()
	require.NotEmpty(t, frames, "frames should be present")

	// Verify scrub report exists
	scrub := reader.ScrubReport()
	require.NotNil(t, scrub, "scrub report should exist")
}
