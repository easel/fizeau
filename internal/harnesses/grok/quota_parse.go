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
)

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
	if match := grokWeeklyUsedPattern.FindStringSubmatch(text); match != nil {
		if v, err := strconv.Atoi(match[1]); err == nil && v >= 0 && v <= 100 {
			usedPercent = v
		}
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
	}
	return []harnesses.QuotaWindow{window}
}
