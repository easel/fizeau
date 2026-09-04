package anthropic

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

// ParseClaudeUsageOutput parses text captured from a claude /usage command.
// Returns quota windows and optional account info (plan type from header).
// This is a shared parser used by both claude and claude-tui harnesses.
// ParseClaudePlanAccount scans screen text for a plan mention ("Claude Max",
// a standalone "Max plan", or a "Login method: Claude Max account" line) and
// returns account info, or nil when no plan is visible. Claude Code >= 2.1.260
// renders /usage as a full-screen dialog with no plan line, so callers capture
// the plan from the startup banner before the dialog covers it.
func ParseClaudePlanAccount(text string) *harnesses.AccountInfo {
	text = StripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	for _, line := range strings.Split(text, "\n") {
		if m := claudePlanTypePattern.FindString(line); m != "" {
			return &harnesses.AccountInfo{PlanType: NormalizeClaudePlanType(m)}
		}
		if m := claudeStandalonePlanTypePattern.FindStringSubmatch(line); len(m) > 1 {
			return &harnesses.AccountInfo{PlanType: NormalizeClaudePlanType(m[1])}
		}
	}
	return nil
}

func ParseClaudeUsageOutput(text string) ([]harnesses.QuotaWindow, *harnesses.AccountInfo) {
	text = StripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")

	var acct *harnesses.AccountInfo
	var windows []harnesses.QuotaWindow

	// Extract plan type from header line.
	for _, line := range lines {
		if m := claudePlanTypePattern.FindString(line); m != "" {
			acct = &harnesses.AccountInfo{PlanType: NormalizeClaudePlanType(m)}
			break
		}
		if m := claudeStandalonePlanTypePattern.FindStringSubmatch(line); len(m) > 1 {
			acct = &harnesses.AccountInfo{PlanType: NormalizeClaudePlanType(m[1])}
			break
		}
	}

	// Walk lines looking for section headers, then harvest % used and Resets.
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmedLower := strings.ToLower(trimmed)

		var sec *claudeUsageSection
		for j := range claudeUsageSections {
			if trimmedLower == strings.ToLower(claudeUsageSections[j].Name) {
				sec = &claudeUsageSections[j]
				break
			}
		}
		if sec == nil {
			continue
		}

		// Scan ahead up to 5 lines for "% used" then "Resets".
		var usedPct float64
		var resetsAt string
		found := false

		for j := i + 1; j < len(lines) && j <= i+5; j++ {
			next := strings.TrimSpace(lines[j])
			if !found {
				if m := claudeUsedPercentPattern.FindStringSubmatch(next); m != nil {
					pct, _ := strconv.ParseFloat(m[1], 64)
					usedPct = pct
					found = true
				}
			}
			if found && resetsAt == "" && strings.Contains(strings.ToLower(next), "resets") {
				resetsAt = extractResetsText(next)
			}
			if found && resetsAt != "" {
				break
			}
		}

		if !found {
			continue
		}

		windows = append(windows, harnesses.QuotaWindow{
			Name:          sec.Name,
			LimitID:       sec.LimitID,
			WindowMinutes: sec.WindowMins,
			UsedPercent:   usedPct,
			ResetsAt:      resetsAt,
			State:         harnesses.QuotaStateFromUsedPercent(int(math.Ceil(usedPct))),
		})
	}

	return windows, acct
}

// Internal pattern and section definitions used by ParseClaudeUsageOutput.
var (
	claudeUsedPercentPattern        = regexp.MustCompile(`<?(\d+(?:\.\d+)?)%\s+used`)
	claudePlanTypePattern           = regexp.MustCompile(`(?i)(Claude\s+(?:Max|Pro|Team|Enterprise|Free))`)
	claudeStandalonePlanTypePattern = regexp.MustCompile(`(?i)\b(Max|Pro|Team|Enterprise|Free)\b(?:\s+plan)?`)
)

type claudeUsageSection struct {
	Name       string
	LimitID    string
	WindowMins int
}

var claudeUsageSections = []claudeUsageSection{
	{"Current session", "session", 300},
	{"Current week (all models)", "weekly-all", 10080},
	{"Current week (Sonnet only)", "weekly-sonnet", 10080},
	{"Extra usage", "extra", 0},
}

// extractResetsText strips the "Resets" prefix from a line.
// Handles: "Resets 4pm (America/New_York)"
//
//	"$200 spent · Resets May 1 (America/New_York)"
func extractResetsText(line string) string {
	lower := strings.ToLower(line)
	idx := strings.Index(lower, "resets")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+len("resets"):])
}
