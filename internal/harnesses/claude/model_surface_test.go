package claude

import (
	"path/filepath"
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

	// AC2: covers haiku/sonnet/opus families
	hasHaiku := false
	hasSonnet := false
	hasOpus := false
	for _, model := range snapshot.Models {
		if model == "haiku" || model == "haiku-5.5" || model == "claude-haiku-5-5" {
			hasHaiku = true
		}
		if model == "sonnet" || model == "sonnet-4.6" || model == "claude-sonnet-4-6" {
			hasSonnet = true
		}
		if model == "opus" || model == "opus-4.7" || model == "claude-opus-4-7" {
			hasOpus = true
		}
	}
	require.True(t, hasHaiku, "models should include haiku family")
	require.True(t, hasSonnet, "models should include sonnet family")
	require.True(t, hasOpus, "models should include opus family")

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
	require.Equal(t, 0, final.Exit.Code, "exit code should be 0")

	// Verify frames exist
	frames := reader.Frames()
	require.NotEmpty(t, frames, "frames should be present")

	// Verify scrub report exists
	scrub := reader.ScrubReport()
	require.NotNil(t, scrub, "scrub report should exist")
}
