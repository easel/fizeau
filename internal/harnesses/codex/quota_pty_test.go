package codex

import (
	"encoding/json"
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

func TestReadCodexQuotaViaPTY_RecordsStatusOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	t.Setenv(codexAuthPathEnv, authPath)
	raw, err := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"id_token": testJWT(map[string]any{
				codexAuthNamespace: map[string]any{"chatgpt_plan_type": "pro"},
			}),
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(authPath, raw, 0o600))
	script := filepath.Join(dir, "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'model:     gpt-5.4 medium   /model to change\r\n› '
IFS= read line
printf '/status\r\n  gpt-5.4 high · 73%% left · /tmp/work\r\n'
printf 'Heads up, you have less than 5%% of your weekly limit left.\r\n› '
sleep 1
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	windows, err := readCodexQuotaViaPTY(2*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.NoError(t, err)
	require.Len(t, windows, 2)
	require.Equal(t, 27.0, windows[0].UsedPercent)
	require.Equal(t, "blocked", windows[1].State)

	reader, err := cassette.Open(cassetteDir)
	require.NoError(t, err)
	require.NotNil(t, reader.Quota())
	require.Equal(t, "ChatGPT Pro", reader.Quota().Metadata["plan_type"])
	require.Equal(t, "ChatGPT Pro", reader.Quota().AccountClass)
	require.NotEmpty(t, reader.Quota().CapturedAt)
	require.Equal(t, defaultCodexQuotaStaleAfter.String(), reader.Quota().FreshnessWindow)
	require.Contains(t, reader.Quota().StalenessBehavior, "automatic routing")
	require.NotEmpty(t, reader.Manifest().Harness.BinaryVersion)
}

func TestReadCodexQuotaViaPTY_DoesNotAcceptStaleStartupStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf '› gpt-5.4 high · 100%% left · /tmp/work\n'
IFS= read line
sleep 5
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	windows, err := readCodexQuotaViaPTY(200*time.Millisecond, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.Error(t, err)
	require.Empty(t, windows)
	require.Equal(t, ptyquota.StatusError, ptyquota.ErrorStatus(err))
	_, statErr := os.Stat(filepath.Join(cassetteDir, cassette.ManifestFile))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "stale startup output should not promote a cassette")
}
