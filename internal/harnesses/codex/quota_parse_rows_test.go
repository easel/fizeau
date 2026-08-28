package codex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Codex 0.148 /status layout: labelled limit rows, with per-model sections
// (here GPT-5.3-Codex-Spark) after the account's primary limits.
const codexStatusRows0148 = "/status\n" +
	"│  Model:                       gpt-5.6-sol (reasoning medium, summaries auto)   │\n" +
	"│  Account:                     someone@example.com (Pro)                        │\n" +
	"│  Weekly limit:                [████████████████████] 99% left (resets 17:01 on 3 Sep)  │\n" +
	"│  GPT-5.3-Codex-Spark limit:                                                    │\n" +
	"│  5h limit:                    [████████████████████] 100% left (resets 04:25 on 28 Aug) │\n" +
	"│  Weekly limit:                [████████████████████] 40% left (resets 23:25 on 3 Sep)  │\n"

func TestParseCodexStatusOutputLabelledRowsPrefersPrimarySection(t *testing.T) {
	windows := parseCodexStatusOutput(codexStatusRows0148)
	require.Len(t, windows, 1)
	require.Equal(t, "7d", windows[0].Name)
	require.Equal(t, 10080, windows[0].WindowMinutes)
	require.Equal(t, 1.0, windows[0].UsedPercent)
	require.True(t, codexQuotaOutputComplete(codexStatusRows0148))
}

func TestParseCodexStatusOutputLabelledRowsFallsBackToModelSection(t *testing.T) {
	text := "/status\n" +
		"│  GPT-5.3-Codex-Spark limit:                          │\n" +
		"│  5h limit:      [████] 75% left (resets 04:25 on 28 Aug) │\n" +
		"│  Weekly limit:  [████] 40% left (resets 23:25 on 3 Sep)  │\n"
	windows := parseCodexStatusOutput(text)
	require.Len(t, windows, 2)
	require.Equal(t, "5h", windows[0].Name)
	require.Equal(t, 25.0, windows[0].UsedPercent)
	require.Equal(t, "7d", windows[1].Name)
	require.Equal(t, 60.0, windows[1].UsedPercent)
}
