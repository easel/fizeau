package gemini

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/pty/cassette"
)

func TestReadGeminiQuotaViaPTY_CapturesTierUsage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-gemini")
	// Emits a ready prompt, reads the slash-command, then prints a
	// fixture resembling Gemini CLI 0.38.2 /model manage output one tier
	// at a time. The sleeps force the probe to tolerate incremental PTY
	// rendering instead of assuming the first parsed row is the final
	// screen. The final sleep keeps the script alive long enough for the
	// probe to harvest the screen before we send SIGTERM via the probe's
	// stop path.
	body := `#!/bin/sh
printf 'Gemini CLI 0.38.2\r\n> '
IFS= read line
printf 'Model management\r\n\r\n'
printf '  Flash         4%% used      Resets 9:13 PM (23h 46m)\r\n'
sleep 0.1
printf '  Flash Lite    0%% used      Resets 9:27 PM (24h)\r\n'
sleep 0.1
printf '  Pro         100%% used\r\n\r\n'
sleep 2
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cassetteDir := filepath.Join(dir, "cassette")

	windows, err := ReadGeminiQuotaViaPTY(3*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir))
	if err != nil {
		t.Fatalf("ReadGeminiQuotaViaPTY: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("want 3 tier windows, got %d: %#v", len(windows), windows)
	}
	flash := FindGeminiQuotaWindow(windows, "gemini-flash")
	if flash == nil || flash.UsedPercent != 4 {
		t.Fatalf("flash parsed from PTY: %#v", flash)
	}
	lite := FindGeminiQuotaWindow(windows, "gemini-flash-lite")
	if lite == nil || lite.UsedPercent != 0 {
		t.Fatalf("flash-lite parsed from PTY: %#v", lite)
	}
	pro := FindGeminiQuotaWindow(windows, "gemini-pro")
	if pro == nil || pro.UsedPercent != 100 || pro.State != "exhausted" {
		t.Fatalf("pro parsed from PTY: %#v", pro)
	}

	reader, err := cassette.Open(cassetteDir)
	if err != nil {
		t.Fatalf("cassette.Open: %v", err)
	}
	quota := reader.Quota()
	if quota.Source != "pty" {
		t.Fatalf("cassette quota source: %q", quota.Source)
	}
	if quota.CapturedAt == "" {
		t.Fatal("cassette quota must record captured_at")
	}
	if quota.FreshnessWindow != defaultGeminiQuotaStaleAfter.String() {
		t.Fatalf("cassette freshness window: %q", quota.FreshnessWindow)
	}
	if len(quota.Windows) != 3 {
		t.Fatalf("cassette windows: %d", len(quota.Windows))
	}
}

func TestReadGeminiQuotaFromCassette_RoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-backed PTY probes require Unix PTY support")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-gemini")
	body := `#!/bin/sh
printf '> '
IFS= read line
cat <<'EOF'
  Flash    4% used      Resets 9:13 PM
  Pro    100% used
EOF
sleep 2
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}
	cassetteDir := filepath.Join(dir, "cassette")
	if _, err := ReadGeminiQuotaViaPTY(3*time.Second, WithQuotaPTYCommand(script), WithQuotaPTYCassetteDir(cassetteDir)); err != nil {
		t.Fatalf("ReadGeminiQuotaViaPTY: %v", err)
	}
	windows, err := ReadGeminiQuotaFromCassette(cassetteDir)
	if err != nil {
		t.Fatalf("ReadGeminiQuotaFromCassette: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("cassette replay lost a tier: %#v", windows)
	}
}

func TestGeminiQuotaCompleteRequiresRepeatedSubsetConfirmation(t *testing.T) {
	partial := "  Flash    4% used      Resets 9:13 PM\n"
	done := geminiQuotaComplete(time.Millisecond)

	if done(partial) {
		t.Fatal("first parsed tier must not complete an incrementally rendered dialog")
	}
	time.Sleep(2 * time.Millisecond)
	for observation := 1; observation < geminiQuotaStableObservations; observation++ {
		if done(partial) {
			t.Fatalf("partial tier set completed after only %d stable observations", observation)
		}
	}
	if !done(partial) {
		t.Fatalf("stable partial tier set did not complete after %d confirmations", geminiQuotaStableObservations)
	}
}

func TestGeminiQuotaCompleteAcceptsAllKnownTiersImmediately(t *testing.T) {
	done := geminiQuotaComplete(time.Hour)
	if !done(geminiModelManageFixture) {
		t.Fatal("all known Gemini tiers are definitive completion evidence")
	}
}
