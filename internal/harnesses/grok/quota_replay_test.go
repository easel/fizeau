package grok

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
	"github.com/easel/fizeau/internal/pty/terminal"
	"github.com/stretchr/testify/require"
)

func Test_quotaCassetteReplayGrok(t *testing.T) {
	// Isolate account reads so the recorded cassette carries no developer
	// credentials.
	t.Setenv(grokAuthPathEnv, filepath.Join(t.TempDir(), "absent-auth.json"))
	dir := writeGrokQuotaCassette(t, fixtureGrokUsageOutput)

	windows, err := readGrokQuotaFromCassette(dir)
	require.NoError(t, err)
	require.Len(t, windows, 1)
	require.Equal(t, float64(93), windows[0].UsedPercent)
	require.Equal(t, "grok-weekly", windows[0].LimitID)

	reader, err := cassette.Open(dir)
	require.NoError(t, err)
	require.NotNil(t, reader.Quota())
	require.Equal(t, "pty", reader.Quota().Source)
}

func writeGrokQuotaCassette(t *testing.T, text string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "grok-quota")
	size := session.Size{Rows: 50, Cols: 220}
	rec, err := cassette.Create(dir, cassette.Manifest{
		ID:      "grok-quota-replay",
		Harness: cassette.Harness{Name: "grok"},
		Command: cassette.Command{
			Argv:          []string{"grok", "--no-alt-screen"},
			WorkdirPolicy: "test",
			EnvAllowlist:  []string{"TERM", "LANG", "LC_ALL"},
			TimeoutMS:     1000,
		},
		Terminal: cassette.Terminal{
			InitialRows: int(size.Rows),
			InitialCols: int(size.Cols),
			Locale:      "C.UTF-8",
			Term:        "xterm-256color",
			PTYMode:     map[string]any{"outer": "test"},
			Emulator:    cassette.Emulator{Name: "vt10x", Version: "v0.0.0-20220301184237-5011da428d02"},
		},
		Timing: cassette.Timing{ResolutionMS: 50, ClockPolicy: "test", ReplayDefault: cassette.ReplayCollapsed},
		Provenance: cassette.Provenance{
			OS:              runtime.GOOS,
			Arch:            runtime.GOARCH,
			RecorderVersion: "quota-replay-test",
		},
	})
	require.NoError(t, err)

	emu := terminal.New(terminal.Size{Rows: int(size.Rows), Cols: int(size.Cols)})
	chunk := session.OutputChunk{Bytes: []byte(text)}
	_, err = rec.RecordOutput(chunk)
	require.NoError(t, err)
	frame, err := emu.Feed(chunk.Bytes)
	require.NoError(t, err)
	_, err = rec.RecordFrame(frame)
	require.NoError(t, err)
	windows := parseGrokUsageOutput(text)
	require.NoError(t, rec.WriteQuota(quotaRecord(windows)))
	require.NoError(t, rec.RecordFinal(cassette.FinalRecord{FinalText: text, Metadata: map[string]any{"harness": "grok"}}))
	require.NoError(t, rec.WriteScrubReport(cassette.ScrubReport{
		Status:                 "clean",
		Rules:                  []string{"test-fixture"},
		HitCounts:              map[string]int{},
		IntentionallyPreserved: []string{"sanitized-quota-fixture"},
	}))
	require.NoError(t, rec.Close())
	return dir
}
