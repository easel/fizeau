package ptyquota

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/pty/session"
	"github.com/stretchr/testify/require"
)

// TestRunAnswersInterstitialPrompt verifies the driver auto-answers a blocking
// prompt (e.g. a folder-trust dialog) that appears before the ready marker.
// The fake binary prints the prompt, blocks on `read`, and only emits the
// ready+done markers after it receives the interstitial's response. Without
// interstitial handling the binary blocks forever and Run times out.
func TestRunAnswersInterstitialPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	script := "printf 'Do you trust the files in this folder?\\r\\n'; read x; printf 'READY-NOW\\r\\nALL-DONE\\r\\n'; sleep 1"
	_, err := Run(context.Background(), Config{
		HarnessName:  "fake",
		Binary:       "sh",
		Args:         []string{"-c", script},
		ReadyMarkers: []string{"READY-NOW"},
		DoneMarkers:  []string{"ALL-DONE"},
		Interstitials: []Interstitial{{
			Name:  "trust",
			Match: func(s string) bool { return strings.Contains(s, "trust the files in this folder") },
			Send:  []byte("\n"),
		}},
		Timeout: 4 * time.Second,
		Size:    session.Size{Rows: 8, Cols: 80},
	})
	require.NoError(t, err, "driver must answer the prompt so the binary proceeds to the markers")
}

// TestRunWithoutInterstitialStallsOnPrompt is the negative control: the same
// blocking prompt with NO interstitial registered must time out.
func TestRunWithoutInterstitialStallsOnPrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	script := "printf 'Do you trust the files in this folder?\\r\\n'; read x; printf 'READY-NOW\\r\\nALL-DONE\\r\\n'; sleep 1"
	_, err := Run(context.Background(), Config{
		HarnessName:  "fake",
		Binary:       "sh",
		Args:         []string{"-c", script},
		ReadyMarkers: []string{"READY-NOW"},
		DoneMarkers:  []string{"ALL-DONE"},
		Timeout:      1 * time.Second,
		Size:         session.Size{Rows: 8, Cols: 80},
	})
	require.Error(t, err, "without interstitial handling the prompt blocks and the probe times out")
}
