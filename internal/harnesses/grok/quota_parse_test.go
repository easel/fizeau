package grok

import (
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// fixtureGrokUsageOutput is real grok 0.2.106 /usage show output captured
// from the TUI scrollback (2026-07-23), including the status-bar remaining
// percentage line.
const fixtureGrokUsageOutput = `   master ~/Projects/fizeau                                                 6.8K / 500K
     Weekly limit: 93%
     Next reset: July 27, 09:48
  ╭──────────────────────────────────────────────────────────────────────────────────╮
  │ ❯                                                                                │
  ╰──────────────────────── Weekly limit left: 7% · Grok 4.5 (high) · always-approve ─╯
  Shift+Tab:mode  │  Ctrl+.:shortcuts
`

func TestParseGrokUsageOutputCapturedFixture(t *testing.T) {
	windows := parseGrokUsageOutput(fixtureGrokUsageOutput)
	if len(windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(windows))
	}
	w := windows[0]
	if w.UsedPercent != 93 {
		t.Errorf("UsedPercent = %v, want 93", w.UsedPercent)
	}
	if w.LimitID != "grok-weekly" {
		t.Errorf("LimitID = %q, want grok-weekly", w.LimitID)
	}
	if w.Name != "weekly" {
		t.Errorf("Name = %q, want weekly", w.Name)
	}
	if w.WindowMinutes != 10080 {
		t.Errorf("WindowMinutes = %d, want 10080", w.WindowMinutes)
	}
	if w.ResetsAt != "July 27, 09:48" {
		t.Errorf("ResetsAt = %q, want %q", w.ResetsAt, "July 27, 09:48")
	}
	if w.State != "ok" {
		t.Errorf("State = %q, want ok", w.State)
	}
}

func TestParseGrokUsageOutputStatusBarFallback(t *testing.T) {
	// Only the status bar is visible (command output scrolled away):
	// used percent derives from 100 - remaining.
	text := "╰── Weekly limit left: 7% · Grok 4.5 (high) ─╯"
	windows := parseGrokUsageOutput(text)
	if len(windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(windows))
	}
	if windows[0].UsedPercent != 93 {
		t.Errorf("UsedPercent = %v, want 93", windows[0].UsedPercent)
	}
	if windows[0].ResetsAt != "" {
		t.Errorf("ResetsAt = %q, want empty", windows[0].ResetsAt)
	}
}

func TestParseGrokUsageOutputPrefersExplicitUsedLine(t *testing.T) {
	// Both signals present but disagreeing (rounding drift): the explicit
	// "Weekly limit: N%" used line wins.
	text := "Weekly limit: 93%\nWeekly limit left: 8%\n"
	windows := parseGrokUsageOutput(text)
	if len(windows) != 1 || windows[0].UsedPercent != 93 {
		t.Fatalf("windows = %+v, want one window at 93%% used", windows)
	}
}

func TestParseGrokUsageOutputBlockedState(t *testing.T) {
	text := "Weekly limit: 97%\nNext reset: July 27, 09:48\n"
	windows := parseGrokUsageOutput(text)
	if len(windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(windows))
	}
	if windows[0].State != "blocked" {
		t.Errorf("State = %q, want blocked at >=95%% used", windows[0].State)
	}
}

func TestParseGrokUsageOutputNoSignal(t *testing.T) {
	if windows := parseGrokUsageOutput("no quota text here"); windows != nil {
		t.Fatalf("windows = %+v, want nil", windows)
	}
}

func TestParseGrokUsageOutputANSIStripped(t *testing.T) {
	text := "\x1b[1mWeekly limit:\x1b[0m 93%\r\n\x1b[2mNext reset:\x1b[0m July 27, 09:48\r\n"
	windows := parseGrokUsageOutput(text)
	if len(windows) != 1 || windows[0].UsedPercent != 93 {
		t.Fatalf("windows = %+v, want one window at 93%% used", windows)
	}
	if windows[0].ResetsAt != "July 27, 09:48" {
		t.Errorf("ResetsAt = %q", windows[0].ResetsAt)
	}
}

func TestGrokQuotaStateHelper(t *testing.T) {
	if got := harnesses.QuotaStateFromUsedPercent(93); got != "ok" {
		t.Errorf("state(93) = %q, want ok", got)
	}
	if got := harnesses.QuotaStateFromUsedPercent(95); got != "blocked" {
		t.Errorf("state(95) = %q, want blocked", got)
	}
}
