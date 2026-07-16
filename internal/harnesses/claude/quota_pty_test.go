package claude

import (
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

func TestReadClaudeQuotaViaPTYWaitsForRequiredUsageSections(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'Claude Max\r\n❯ '
IFS= read line
printf 'Current session\r\n4%% used\r\nResets 4pm (UTC)\r\n'
printf 'Current week (all models)\r\n'
sleep 5
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	windows, account, err := readClaudeQuotaViaPTY(200*time.Millisecond, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.Error(t, err)
	require.Empty(t, windows)
	require.Nil(t, account)
	require.Equal(t, ptyquota.StatusError, ptyquota.ErrorStatus(err))
	require.Contains(t, err.Error(), "timed out")
	_, statErr := os.Stat(filepath.Join(cassetteDir, cassette.ManifestFile))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "partial usage output should not promote a cassette")
}

func TestReadClaudeQuotaViaPTYAcceptsSonnetOnlyWeeklyUsage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'Claude Max\r\n❯ '
IFS= read line
cat <<'EOF'
Current session
4% used
Resets 4pm (UTC)
Current week (Sonnet only)
10% used
Resets Monday (UTC)
EOF
sleep 1
`), 0o700))

	windows, account, err := readClaudeQuotaViaPTY(2*time.Second, WithQuotaPTYCommand(script))
	require.NoError(t, err)
	require.NotNil(t, account)
	require.True(t, hasQuotaWindow(windows, "session"))
	require.True(t, hasQuotaWindow(windows, "weekly-sonnet"))
}

func TestReadClaudeQuotaViaPTYRecordsEvidenceFreshness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'claude-test 1.2.3\n'
  exit 0
fi
printf 'Claude Max\r\n❯ '
IFS= read line
cat <<'EOF'
Current session
4% used
Resets 4pm (UTC)
Current week (all models)
10% used
Resets Monday (UTC)
EOF
sleep 1
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	_, _, err := readClaudeQuotaViaPTY(2*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.NoError(t, err)
	reader, err := cassette.Open(cassetteDir)
	require.NoError(t, err)
	require.Equal(t, "claude-test 1.2.3", reader.Manifest().Harness.BinaryVersion)
	require.Equal(t, "Claude Max", reader.Quota().AccountClass)
	require.NotEmpty(t, reader.Quota().CapturedAt)
	require.Equal(t, defaultClaudeQuotaStaleAfter.String(), reader.Quota().FreshnessWindow)
	require.Contains(t, reader.Quota().StalenessBehavior, "automatic routing")
}

func TestReadClaudeQuotaViaPTYRejectsMissingAccountPlan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'Claude Code\r\n❯ '
IFS= read line
cat <<'EOF'
Current session
4% used
Resets 4pm (UTC)
Current week (all models)
10% used
Resets Monday (UTC)
EOF
sleep 1
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	windows, account, err := readClaudeQuotaViaPTY(10*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.Error(t, err)
	require.Empty(t, windows)
	require.Nil(t, account)
	require.Contains(t, err.Error(), "missing account plan")
	_, statErr := os.Stat(filepath.Join(cassetteDir, cassette.ManifestFile))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "account-less usage output should not promote a cassette")
}
