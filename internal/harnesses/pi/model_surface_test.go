package pi

import (
	"path/filepath"
	"testing"

	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/stretchr/testify/require"
)

func TestModelSurfaceCassetteReturnsNonEmptyModels(t *testing.T) {
	cassetteDir := filepath.Join("testdata", "model_surface")

	snapshot, err := ReadPiModelDiscoveryFromCassette(cassetteDir)
	require.NoError(t, err, "cassette should parse without error")

	// AC2: cassette reader returns non-empty Models slice
	require.NotEmpty(t, snapshot.Models, "models should not be empty")

	// AC2: covers expected pi models (gemini-2.5-flash and gemini-2.5-pro)
	hasFlash := false
	hasPro := false
	for _, model := range snapshot.Models {
		if model == "gemini-2.5-flash" {
			hasFlash = true
		}
		if model == "gemini-2.5-pro" {
			hasPro = true
		}
	}
	require.True(t, hasFlash, "models should include gemini-2.5-flash")
	require.True(t, hasPro, "models should include gemini-2.5-pro")

	// Additional: verify source is recorded
	require.True(t, snapshot.Source == "cli:list-models" || snapshot.Source == "cassette", "source should be cli:list-models or cassette")
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
	require.Equal(t, "pi", manifest.Harness.Name, "harness name should be pi")

	// Verify discovery record exists and has models
	discovery := reader.Discovery()
	require.NotNil(t, discovery, "discovery record should exist")
	require.Greater(t, len(discovery.Models), 0, "discovery should have models")

	// Verify final record exists
	final := reader.Final()
	require.NotNil(t, final, "final record should exist")
	require.Equal(t, 0, final.Exit.Code, "exit code should be 0")

	// Verify output exists
	require.NotEmpty(t, reader.RawOutput(), "raw output should be present")

	// Verify scrub report exists
	scrub := reader.ScrubReport()
	require.NotNil(t, scrub, "scrub report should exist")
}
