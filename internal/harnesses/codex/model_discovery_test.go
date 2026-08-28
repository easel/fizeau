package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/stretchr/testify/require"
)

func TestCodexDefaultModelSnapshotWithContext_UsesCallerContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'model:     gpt-5.4 medium   /model to change\r\n› '
IFS= read line
sleep 30
`), 0o700))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	go func() {
		_, err := (&Runner{Binary: script}).DefaultModelSnapshotWithContext(ctx)
		result <- err
	}()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("DefaultModelSnapshotWithContext ignored caller cancellation")
	}
}

func TestReadCodexModelDiscoveryViaPTY_ContextCancellationReapsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY process groups require Unix support")
	}
	dir := t.TempDir()
	parentPIDPath := filepath.Join(dir, "parent.pid")
	childPIDPath := filepath.Join(dir, "child.pid")
	lateWritePath := filepath.Join(dir, "late-write")
	cassetteDir := filepath.Join(dir, "cassette")
	script := filepath.Join(dir, "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf '%s' "$$" > "$1"
(sleep 1; printf late > "$3") &
child=$!
printf '%s' "$child" > "$2"
printf 'model:     gpt-5.4 medium   /model to change\r\n› '
IFS= read line
wait "$child"
`), 0o700))

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := ReadCodexModelDiscoveryViaPTYWithContext(
			ctx,
			30*time.Second,
			WithQuotaPTYCommand(script, parentPIDPath, childPIDPath, lateWritePath),
			WithQuotaPTYCassetteDir(cassetteDir),
		)
		result <- err
	}()

	require.Eventually(t, func() bool {
		return fileContainsPID(parentPIDPath) && fileContainsPID(childPIDPath)
	}, time.Second, 10*time.Millisecond)
	parentPID := readPIDFile(t, parentPIDPath)
	childPID := readPIDFile(t, childPIDPath)
	cancelledAt := time.Now()
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, time.Since(cancelledAt), 2*time.Second)
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled Codex model discovery did not return after process cleanup")
	}

	require.Eventually(t, func() bool { return processReaped(parentPID) }, 2*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { return processReaped(childPID) }, 2*time.Second, 20*time.Millisecond)
	time.Sleep(1200 * time.Millisecond)
	_, err := os.Stat(lateWritePath)
	require.ErrorIs(t, err, os.ErrNotExist, "reaped child must not write after discovery returns")
	_, err = os.Stat(filepath.Join(cassetteDir, cassette.ManifestFile))
	require.ErrorIs(t, err, os.ErrNotExist, "cancelled discovery must not commit cassette evidence")
}

func fileContainsPID(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = strconv.Atoi(strings.TrimSpace(string(raw)))
	return err == nil
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	return pid
}

func processReaped(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestParseCodexModels(t *testing.T) {
	models := parseCodexModels("Select Model and Effort\r\n> gpt-5.6-terra\r\n  gpt-5.6-sol\r\n  gpt-5.6-luna\r\n  gpt-5.6-terra\r\n")
	require.Equal(t, []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna"}, models)
}

func TestCodexModelDiscoveryCompleteRequiresRenderedPicker(t *testing.T) {
	startup := "gpt-5.6-terra medium · ~/Projects/fizeau"
	require.False(t, codexModelDiscoveryComplete(startup), "startup footer must not complete /model discovery")

	picker := "Select Model and Effort\n1. gpt-5.6-sol (default)\n2. gpt-5.6-terra (current)\n3. gpt-5.6-luna\nPress enter to confirm or esc to go back"
	require.True(t, codexModelDiscoveryComplete(picker), "fully rendered picker must complete /model discovery")
}

func TestDefaultCodexModelDiscovery_IncludesCurrentFrontier(t *testing.T) {
	snapshot := testCodexModelDiscovery()
	snapshot.Models = []string{"gpt-5.5"}
	require.Contains(t, snapshot.Models, "gpt-5.5")
	require.Equal(t, "gpt-5.5", resolveCodexModelAlias("gpt", snapshot))
	require.Equal(t, "gpt-5.5", resolveCodexModelAlias("gpt-5", snapshot))
}

func TestResolveCodexModelAlias_UsesLatestDiscoveredVersion(t *testing.T) {
	snapshot := testCodexModelDiscovery()
	snapshot.Models = []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.5-mini", "gpt-5.5"}

	require.Equal(t, "gpt-5.5", resolveCodexModelAlias("gpt", snapshot))
	require.Equal(t, "gpt-5.5", resolveCodexModelAlias("gpt-5", snapshot))
	require.Equal(t, "gpt-5.4", resolveCodexModelAlias("gpt-5.4", snapshot))
	require.Equal(t, "qwen3.6", resolveCodexModelAlias("qwen3.6", snapshot))
}

func TestReadCodexModelDiscoveryViaPTYRecordsDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'model:     gpt-5.4 medium   /model to change\r\n› '
IFS= read line
printf '/model\r\nSelect Model and Effort\r\n> gpt-5.6-sol\r\n  gpt-5.6-terra\r\n  gpt-5.6-luna\r\nPress enter to confirm or esc to go back\r\n'
sleep 1
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	snapshot, err := ReadCodexModelDiscoveryViaPTY(2*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}, snapshot.Models)
	require.Contains(t, snapshot.ReasoningLevels, "high")

	replayed, err := ReadCodexModelDiscoveryFromCassette(cassetteDir)
	require.NoError(t, err)
	require.Equal(t, snapshot.Models, replayed.Models)
	reader, err := cassette.Open(cassetteDir)
	require.NoError(t, err)
	require.NotNil(t, reader.Discovery())
	require.NotEmpty(t, reader.Discovery().CapturedAt)
	require.Equal(t, CodexModelDiscoveryFreshnessWindow.String(), reader.Discovery().FreshnessWindow)
	require.Contains(t, reader.Discovery().StalenessBehavior, "authenticated PTY refresh")
}

func TestReadCodexModelDiscoveryViaPTYRejectsEmptyMenu(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
printf 'model:     gpt-5.4 medium   /model to change\r\n› '
IFS= read line
printf '/model\r\nNo models available\r\n'
sleep 5
`), 0o700))
	cassetteDir := filepath.Join(dir, "cassette")

	_, err := ReadCodexModelDiscoveryViaPTY(200*time.Millisecond, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	require.Error(t, err)
	require.Equal(t, ptyquota.StatusError, ptyquota.ErrorStatus(err))
	_, statErr := os.Stat(filepath.Join(cassetteDir, cassette.ManifestFile))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "empty model output should not promote a cassette")
}

func TestReadCodexModelDiscoveryFromModelSurfaceCassette(t *testing.T) {
	snapshot, err := ReadCodexModelDiscoveryFromCassette("testdata/model_surface")
	require.NoError(t, err)
	require.NotEmpty(t, snapshot.Models, "cassette must contain at least one model")
	require.NotEmpty(t, snapshot.ReasoningLevels, "cassette must contain reasoning levels")
	require.Equal(t, "pty", snapshot.Source)
	require.NotEmpty(t, snapshot.FreshnessWindow)

	reader, err := cassette.Open("testdata/model_surface")
	require.NoError(t, err)
	rec := reader.Discovery()
	require.NotNil(t, rec)
	require.Equal(t, "ok", rec.Status)
	require.NotEmpty(t, rec.Models)
	require.NotEmpty(t, rec.ReasoningLevels)
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		require.Contains(t, rec.Models, model)
	}
}
