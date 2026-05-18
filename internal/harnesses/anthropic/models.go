package anthropic

import (
	"regexp"
	"strings"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|[^[])`)

// StripANSI removes ANSI escape sequences from a string.
func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// NormalizeClaudePlanType normalizes a plan type string to a canonical form.
// Handles: "Claude Max", "Max", "Pro", etc.
func NormalizeClaudePlanType(plan string) string {
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(plan), "claude ") {
		return plan
	}
	return "Claude " + plan
}
