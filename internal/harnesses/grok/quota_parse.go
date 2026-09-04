package grok

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|[^[])`)

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

var (
	// "/usage show" scrollback output: "Weekly limit: 93%" (percent USED).
	grokWeeklyUsedPattern = regexp.MustCompile(`(?i)\bWeekly limit:\s*(\d{1,3})%`)
	// TUI status bar: "Weekly limit left: 7%" (percent REMAINING).
	grokWeeklyLeftPattern = regexp.MustCompile(`(?i)\bWeekly limit left:\s*(\d{1,3})%`)
	// "/usage show" scrollback output: "Next reset: July 27, 09:48".
	grokNextResetPattern = regexp.MustCompile(`(?i)\bNext reset:\s*([^\r\n]+)`)
	// grok >= 1.0 renders /usage show as a dialog: a "Weekly limit (PLAN)"
	// header (no colon), a progress-bar line ending in "N%" (percent USED),
	// and a "Resets: September 7, 09:48" line.
	grokDialogHeaderPattern  = regexp.MustCompile(`(?i)\bWeekly limit\s*(?:\(([^)]+)\))?\s*$`)
	grokDialogPercentPattern = regexp.MustCompile(`(\d{1,3})%\s*$`)
	grokDialogResetPattern   = regexp.MustCompile(`(?i)\bResets:\s*([^\r\n]+)`)
)

// parseGrokDialogUsage parses the grok >= 1.0 /usage dialog layout. It returns
// used percent (or -1), the reset text, and the plan name from the header.
func parseGrokDialogUsage(text string) (int, string, string) {
	lines := strings.Split(text, "\n")
	used, resets, plan := -1, "", ""
	for index, raw := range lines {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "│"))
		match := grokDialogHeaderPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		plan = strings.TrimSpace(match[1])
		for next := index + 1; next < len(lines) && next <= index+6; next++ {
			candidate := strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[next]), "│"))
			if used < 0 {
				if pm := grokDialogPercentPattern.FindStringSubmatch(candidate); pm != nil {
					if v, err := strconv.Atoi(pm[1]); err == nil && v >= 0 && v <= 100 {
						used = v
					}
				}
			}
			if rm := grokDialogResetPattern.FindStringSubmatch(candidate); rm != nil {
				resets = strings.TrimSpace(rm[1])
			}
			if used >= 0 && resets != "" {
				break
			}
		}
		if used >= 0 {
			break
		}
	}
	return used, resets, plan
}

// parseGrokUsageOutput parses transport-neutral text produced by the
// supervised grok /usage show PTY probe. Captured output (grok 0.2.106):
//
//	Weekly limit: 93%
//	Next reset: July 27, 09:48
//
// with the status bar separately showing "Weekly limit left: 7%". The
// explicit "Weekly limit: N%" (used) line is preferred; the status-bar
// remaining percentage is the fallback signal.
func parseGrokUsageOutput(text string) []harnesses.QuotaWindow {
	text = stripANSI(strings.ReplaceAll(text, "\r\n", "\n"))

	usedPercent := -1
	dialogUsed, dialogResets, _ := parseGrokDialogUsage(text)
	if match := grokWeeklyUsedPattern.FindStringSubmatch(text); match != nil {
		if v, err := strconv.Atoi(match[1]); err == nil && v >= 0 && v <= 100 {
			usedPercent = v
		}
	}
	if usedPercent < 0 && dialogUsed >= 0 {
		usedPercent = dialogUsed
	}
	if usedPercent < 0 {
		if match := grokWeeklyLeftPattern.FindStringSubmatch(text); match != nil {
			if v, err := strconv.Atoi(match[1]); err == nil && v >= 0 && v <= 100 {
				usedPercent = 100 - v
			}
		}
	}
	if usedPercent < 0 {
		return nil
	}

	window := harnesses.QuotaWindow{
		Name:          "weekly",
		LimitID:       "grok-weekly",
		WindowMinutes: 10080,
		UsedPercent:   float64(usedPercent),
		State:         harnesses.QuotaStateFromUsedPercent(usedPercent),
	}
	if match := grokNextResetPattern.FindStringSubmatch(text); match != nil {
		window.ResetsAt = strings.TrimSpace(match[1])
	} else if dialogResets != "" {
		window.ResetsAt = dialogResets
	}
	return []harnesses.QuotaWindow{window}
}
