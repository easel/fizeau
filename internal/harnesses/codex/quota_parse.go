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
)

// parseCodexStatusOutput parses transport-neutral text produced by the
// supervised Codex /status PTY probe.
func parseCodexStatusOutput(text string) []harnesses.QuotaWindow {
	text = stripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lines := strings.Split(text, "\n")
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
