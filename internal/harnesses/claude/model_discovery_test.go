package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/stretchr/testify/require"
)

func TestParseClaudeModelsAndReasoning(t *testing.T) {
	text := "Model: 'sonnet' or 'opus' or 'claude-sonnet-4-6'\n--effort <level> (low, medium, high, xhigh, max)"
	require.Equal(t, []string{"claude-sonnet-4-6", "sonnet-4.6", "sonnet", "opus"}, parseClaudeModels(text))
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, parseClaudeReasoningLevels(text))
}

func TestParseClaudeModelsFromTUIVersionLabels(t *testing.T) {
	text := "Select model\n> Opus 4.7\n  Sonnet 4.6\n  Claude Haiku 5.5\n  claude-opus-4-8\n"

	require.Equal(t, []string{"claude-opus-4-8", "opus-4.8", "opus-4.7", "sonnet-4.6", "haiku-5.5", "opus", "sonnet", "haiku"}, parseClaudeModels(text))
}

func TestResolveClaudeFamilyAliasUsesLatestDiscoveredVersion(t *testing.T) {
	snapshot := testClaudeModelDiscovery()
	snapshot.Models = []string{"opus", "opus-4.6", "opus-4.10", "opus-4.7", "sonnet-4.6"}

	require.Equal(t, "opus-4.10", resolveClaudeFamilyAlias("opus", snapshot))
	require.Equal(t, "sonnet-4.6", resolveClaudeFamilyAlias("sonnet", snapshot))
	require.Equal(t, "opus-4.7", resolveClaudeFamilyAlias("opus-4.7", snapshot))
	require.Equal(t, "gpt-5.4", resolveClaudeFamilyAlias("gpt-5.4", snapshot))
}

func TestReadClaudeReasoningFromHelp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed helper requires Unix script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
Usage: claude [options]
  --effort <level>  Effort level for the current session (low, medium, high, xhigh, max)
EOF
`), 0o700))

	levels, err := readClaudeReasoningFromHelp(context.Background(), script)
	require.NoError(t, err)
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, levels)
}

func TestReadClaudeModelDiscoveryViaPTYRecordsDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf '❯ '
IFS= read line
printf '/model\r\nSelect model\r\n> Sonnet 4.6\r\n  Opus 4.7\r\n  claude-sonnet-4-6\r\n❯ '
sleep 1
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	snapshot, err := ReadClaudeModelDiscoveryViaPTY(2*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.NoError(t, err)
	require.Equal(t, []string{"claude-sonnet-4-6", "sonnet-4.6", "opus-4.7", "sonnet", "opus"}, snapshot.Models)
	require.Contains(t, snapshot.ReasoningLevels, "xhigh")

	replayed, err := ReadClaudeModelDiscoveryFromCassette(cassetteDir)
	require.NoError(t, err)
	require.Equal(t, snapshot.Models, replayed.Models)
	reader, err := cassette.Open(cassetteDir)
	require.NoError(t, err)
	require.NotNil(t, reader.Discovery())
	require.NotEmpty(t, reader.Discovery().CapturedAt)
	require.Equal(t, ClaudeModelDiscoveryFreshnessWindow.String(), reader.Discovery().FreshnessWindow)
	require.Contains(t, reader.Discovery().StalenessBehavior, "authenticated PTY refresh")
}

func TestReadClaudeModelDiscoveryViaPTYRejectsEmptyMenu(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf '❯ '
IFS= read line
printf '/model\r\nNo model picker\r\n'
sleep 5
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	_, err := ReadClaudeModelDiscoveryViaPTY(200*time.Millisecond, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.Error(t, err)
	require.Equal(t, ptyquota.StatusError, ptyquota.ErrorStatus(err))
	_, statErr := os.Stat(filepath.Join(cassetteDir, cassette.ManifestFile))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "empty model output should not promote a cassette")
}
