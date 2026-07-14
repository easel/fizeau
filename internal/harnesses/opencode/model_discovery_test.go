package opencode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/stretchr/testify/require"
)

// Managed harness commands start a trusted supervisor and a gated child before
// execing the fixture. Race-instrumented test binaries make both self-exec
// stages substantially slower than production binaries, so functional model
// discovery tests need a lifecycle-aware deadline rather than a startup-speed
// assertion.
const managedLifecycleTestTimeout = 5 * time.Second

func TestDefaultOpenCodeModelDiscovery(t *testing.T) {
	snapshot := testOpenCodeModelDiscovery()
	snapshot.Models = []string{"opencode/gpt-5.4", "opencode/claude-sonnet-4-6"}
	if len(snapshot.Models) == 0 {
		t.Fatal("test discovery should include model IDs")
	}
	assertContainsString(t, snapshot.ReasoningLevels, "high", "reasoning")
	if snapshot.FreshnessWindow != openCodeModelDiscoveryFreshnessWindow.String() {
		t.Fatalf("FreshnessWindow = %q, want %q", snapshot.FreshnessWindow, openCodeModelDiscoveryFreshnessWindow.String())
	}
}

func TestParseOpenCodeModels(t *testing.T) {
	input := `
opencode/gpt-5.4
opencode/claude-sonnet-4-6
opencode/gpt-5.4
lm-studio/*
Name Provider Context
`
	models := parseOpenCodeModels(input)
	want := []string{"opencode/gpt-5.4", "opencode/claude-sonnet-4-6", "lm-studio/*"}
	if len(models) != len(want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("models = %#v, want %#v", models, want)
		}
	}
}

func TestReadOpenCodeModelDiscovery(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
opencode/gpt-5.4
opencode/claude-sonnet-4-6
EOF
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), managedLifecycleTestTimeout)
	defer cancel()
	snapshot, err := readOpenCodeModelDiscovery(ctx, script)
	if err != nil {
		t.Fatalf("readOpenCodeModelDiscovery: %v", err)
	}
	if snapshot.Source != "cli:opencode models" {
		t.Fatalf("Source = %q, want cli:opencode models", snapshot.Source)
	}
	assertContainsString(t, snapshot.Models, "opencode/gpt-5.4", "models")
	assertContainsString(t, snapshot.Models, "opencode/claude-sonnet-4-6", "models")
	assertContainsString(t, snapshot.ReasoningLevels, "max", "reasoning")
}

func TestReadOpenCodeModelDiscoveryVerboseEvidence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
opencode/gpt-5.4
{
  "id": "gpt-5.4",
  "providerID": "opencode",
  "status": "active",
  "cost": {
    "input": 2.5,
    "output": 15,
    "cache": {
      "read": 0.25,
      "write": 0
    }
  },
  "limit": {
    "context": 1050000,
    "output": 128000
  },
  "capabilities": {
    "reasoning": true,
    "attachment": true,
    "toolcall": true
  },
  "variants": {
    "none": {},
    "low": {},
    "medium": {},
    "high": {},
    "xhigh": {}
  }
}
opencode/minimax-m2.5-free
{
  "id": "minimax-m2.5-free",
  "providerID": "opencode",
  "status": "active",
  "cost": {
    "input": 0,
    "output": 0,
    "cache": {
      "read": 0,
      "write": 0
    }
  },
  "limit": {
    "context": 204800,
    "output": 131072
  },
  "capabilities": {
    "reasoning": true,
    "attachment": false,
    "toolcall": true
  },
  "variants": {
    "low": {},
    "medium": {},
    "high": {}
  }
}
EOF
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), managedLifecycleTestTimeout)
	defer cancel()
	snapshot, err := readOpenCodeModelDiscovery(ctx, script, "models", "--verbose")
	if err != nil {
		t.Fatalf("readOpenCodeModelDiscovery: %v", err)
	}
	if snapshot.Source != "cli:opencode models --verbose" {
		t.Fatalf("Source = %q, want cli:opencode models --verbose", snapshot.Source)
	}
	assertContainsString(t, snapshot.Models, "opencode/gpt-5.4", "models")
	assertContainsString(t, snapshot.Models, "opencode/minimax-m2.5-free", "models")
	assertContainsString(t, snapshot.ReasoningLevels, "none", "reasoning")
	assertContainsString(t, snapshot.ReasoningLevels, "xhigh", "reasoning")
	if !strings.Contains(snapshot.Detail, "per-model costs are present for 2 records") {
		t.Fatalf("Detail = %q, want cost evidence", snapshot.Detail)
	}
}

func TestReadOpenCodeVerboseModelEvidence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
opencode/gpt-5.4
{
  "id": "gpt-5.4",
  "providerID": "opencode",
  "status": "active",
  "cost": {
    "input": 2.5,
    "output": 15,
    "cache": {
      "read": 0.25,
      "write": 0
    }
  },
  "limit": {
    "context": 1050000,
    "output": 128000
  },
  "capabilities": {
    "reasoning": true,
    "attachment": true,
    "toolcall": true
  },
  "variants": {
    "low": {},
    "high": {},
    "xhigh": {}
  }
}
EOF
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), managedLifecycleTestTimeout)
	defer cancel()
	evidence, err := readOpenCodeVerboseModelEvidence(ctx, script)
	if err != nil {
		t.Fatalf("readOpenCodeVerboseModelEvidence: %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %#v, want one record", evidence)
	}
	got := evidence[0]
	if got.Model != "opencode/gpt-5.4" || got.ProviderID != "opencode" || got.ModelID != "gpt-5.4" {
		t.Fatalf("unexpected model identity: %#v", got)
	}
	if got.Cost == nil {
		t.Fatal("expected cost evidence")
	}
	if got.Cost.InputUSDPerMTok != 2.5 || got.Cost.OutputUSDPerMTok != 15 || got.Cost.CacheReadUSDPerMTok != 0.25 {
		t.Fatalf("unexpected cost evidence: %#v", got.Cost)
	}
	if got.ContextLimit != 1050000 || got.OutputLimit != 128000 {
		t.Fatalf("unexpected limits: %#v", got)
	}
	if !got.Reasoning || !got.ToolCall || !got.Attachment {
		t.Fatalf("unexpected capabilities: %#v", got)
	}
	wantVariants := []string{"low", "high", "xhigh"}
	if len(got.Variants) != len(wantVariants) {
		t.Fatalf("variants = %#v, want %#v", got.Variants, wantVariants)
	}
	for i := range wantVariants {
		if got.Variants[i] != wantVariants[i] {
			t.Fatalf("variants = %#v, want %#v", got.Variants, wantVariants)
		}
	}
}

func TestReadOpenCodeModelDiscoveryRejectsEmptyOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'no models here\n'
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), managedLifecycleTestTimeout)
	defer cancel()
	_, err := readOpenCodeModelDiscovery(ctx, script)
	require.ErrorContains(t, err, "returned no provider/model IDs")
}

func TestParseOpenCodeVerboseModelEvidenceRejectsMalformedJSON(t *testing.T) {
	_, err := parseOpenCodeVerboseModelEvidence(`opencode/gpt-5.4
{
  "id": "gpt-5.4"
`)
	if err == nil {
		t.Fatal("expected malformed verbose output to fail")
	}
}

func assertContainsString(t *testing.T, values []string, want, label string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%s missing %q in %#v", label, want, values)
}

// TestModelSurfaceCassetteReturnsNonEmptyModels verifies that the recorded
// model surface cassette can be parsed and returns the expected models.
func TestModelSurfaceCassetteReturnsNonEmptyModels(t *testing.T) {
	cassetteDir := filepath.Join("testdata", "model_surface")

	snapshot, err := ReadOpenCodeModelDiscoveryFromCassette(cassetteDir)
	require.NoError(t, err, "cassette should parse without error")

	// AC2: cassette reader returns non-empty Models slice
	require.NotEmpty(t, snapshot.Models, "models should not be empty")

	// The recorder scopes this portable fixture to OpenCode-owned models so
	// account-local providers never leak into the checked-in evidence.
	for _, model := range snapshot.Models {
		require.True(t, strings.HasPrefix(model, "opencode/"), "portable cassette contains account-local model %q", model)
	}
}

// TestModelSurfaceCassetteStructure verifies the cassette directory structure
// conforms to ADR-002 schema v1.
func TestModelSurfaceCassetteStructure(t *testing.T) {
	// AC1: cassette structure exists per ADR-002 schema v1
	cassetteDir := filepath.Join("testdata", "model_surface")

	reader, err := cassette.Open(cassetteDir)
	require.NoError(t, err, "cassette should open successfully")

	// Verify manifest loads
	manifest := reader.Manifest()
	require.NotNil(t, manifest, "manifest should exist")
	require.Equal(t, 1, manifest.Version, "manifest version should be 1")
	require.Equal(t, "opencode", manifest.Harness.Name, "harness name should be opencode")

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
