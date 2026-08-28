package codex

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|[^[])`)

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

var (
	codexModelLinePattern  = regexp.MustCompile(`\b(gpt-[A-Za-z0-9][A-Za-z0-9._-]*)\b.*?\b(\d{1,3})%\s+(?:left|remaining)\b`)
	codexWeeklyWarnPattern = regexp.MustCompile(`(?i)(less than\s+)?(\d{1,3})%\s+of your weekly limit\s+(?:left|remaining)`)
	// Codex >= 0.148 renders /status limits as labelled rows:
	//   5h limit:      [████] 100% left (resets 04:25 on 28 Aug)
	//   Weekly limit:  [████]  99% left (resets 17:01 on 3 Sep)
	// optionally grouped under a per-model header such as
	// "GPT-5.3-Codex-Spark limit:" with no percentage on the header line.
	codexLimitRowPattern    = regexp.MustCompile(`(?i)^\s*(5h|weekly)\s+limit:.*?\b(\d{1,3})%\s+(?:left|remaining)\b`)
	codexLimitHeaderPattern = regexp.MustCompile(`(?i)^\s*\S.*\blimit:\s*$`)
)

// parseCodexLimitRows parses the labelled-row /status layout. Rows before any
// per-model header describe the account's primary limits and are preferred;
// when the primary section carries no rows, the first model section that does
// is used instead.
func parseCodexLimitRows(lines []string) []harnesses.QuotaWindow {
	var sections [][]harnesses.QuotaWindow
	current := []harnesses.QuotaWindow{}
	flush := func() {
		sections = append(sections, current)
		current = []harnesses.QuotaWindow{}
	}
	for _, raw := range lines {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "│"))
		if match := codexLimitRowPattern.FindStringSubmatch(line); match != nil {
			percentLeft, err := strconv.Atoi(match[2])
			if err != nil || percentLeft < 0 || percentLeft > 100 {
				continue
			}
			used := 100 - percentLeft
			window := harnesses.QuotaWindow{
				LimitID: "codex", UsedPercent: float64(used),
				State: harnesses.QuotaStateFromUsedPercent(used),
			}
			if strings.EqualFold(match[1], "5h") {
				window.Name, window.WindowMinutes = "5h", 300
			} else {
				window.Name, window.WindowMinutes = "7d", 10080
			}
			current = append(current, window)
			continue
		}
		if codexLimitHeaderPattern.MatchString(line) {
			flush()
		}
	}
	flush()
	for _, section := range sections {
		if len(section) > 0 {
			return section
		}
	}
	return nil
}

// parseCodexStatusOutput parses transport-neutral text produced by the
// supervised Codex /status PTY probe.
func parseCodexStatusOutput(text string) []harnesses.QuotaWindow {
	text = stripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")
	if windows := parseCodexLimitRows(lines); len(windows) > 0 {
		return windows
	}
	var windows []harnesses.QuotaWindow
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if match := codexModelLinePattern.FindStringSubmatch(line); match != nil {
			percentLeft, _ := strconv.Atoi(match[2])
			if percentLeft < 0 || percentLeft > 100 {
				continue
			}
			usedPercent := 100 - percentLeft
			windows = append(windows, harnesses.QuotaWindow{
				Name: "5h", LimitID: "codex", WindowMinutes: 300,
				UsedPercent: float64(usedPercent), State: harnesses.QuotaStateFromUsedPercent(usedPercent),
			})
			break
		}
	}
	for _, line := range lines {
		if match := codexWeeklyWarnPattern.FindStringSubmatch(line); match != nil {
			percentLeft, _ := strconv.Atoi(match[2])
			if percentLeft < 0 || percentLeft > 100 {
				continue
			}
			usedFloor := 100 - percentLeft
			statePercent := usedFloor
			if strings.TrimSpace(match[1]) != "" {
				statePercent++
			}
			windows = append(windows, harnesses.QuotaWindow{
				Name: "7d", LimitID: "codex", WindowMinutes: 10080,
				UsedPercent: float64(usedFloor), State: harnesses.QuotaStateFromUsedPercent(statePercent),
			})
			break
		}
	}
	return windows
}
