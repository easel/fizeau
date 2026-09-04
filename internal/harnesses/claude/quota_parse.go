package claude

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

func stripANSI(s string) string { return anthropic.StripANSI(s) }

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

// parseClaudePlanAccount delegates to the shared anthropic plan scanner. See
// anthropic.ParseClaudePlanAccount for why the plan is captured from the
// startup banner on Claude Code >= 2.1.260.
func parseClaudePlanAccount(text string) *harnesses.AccountInfo {
	return anthropic.ParseClaudePlanAccount(text)
}

// parseClaudeUsageOutput parses the transport-neutral text derived from a
// supervised Claude /usage PTY probe.
func parseClaudeUsageOutput(text string) ([]harnesses.QuotaWindow, *harnesses.AccountInfo) {
	text = stripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")

	acct := parseClaudePlanAccount(text)
	var windows []harnesses.QuotaWindow

	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		var section *claudeUsageSection
		for candidate := range claudeUsageSections {
			if strings.EqualFold(trimmed, claudeUsageSections[candidate].Name) {
				section = &claudeUsageSections[candidate]
				break
			}
		}
		if section == nil {
			continue
		}

		var usedPercent float64
		var resetsAt string
		found := false
		for nextIndex := index + 1; nextIndex < len(lines) && nextIndex <= index+5; nextIndex++ {
			next := strings.TrimSpace(lines[nextIndex])
			if !found {
				if match := claudeUsedPercentPattern.FindStringSubmatch(next); match != nil {
					usedPercent, _ = strconv.ParseFloat(match[1], 64)
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
			Name: section.Name, LimitID: section.LimitID, WindowMinutes: section.WindowMins,
			UsedPercent: usedPercent, ResetsAt: resetsAt,
			State: harnesses.QuotaStateFromUsedPercent(int(math.Ceil(usedPercent))),
		})
	}
	return windows, acct
}

func normalizeClaudePlanType(plan string) string { return anthropic.NormalizeClaudePlanType(plan) }

func extractResetsText(line string) string {
	index := strings.Index(strings.ToLower(line), "resets")
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(line[index+len("resets"):])
}
