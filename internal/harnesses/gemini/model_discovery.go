package gemini

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

const GeminiModelDiscoveryFreshnessWindow = 24 * time.Hour

var (
	geminiANSISequencePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	geminiModelPattern        = regexp.MustCompile(`\bgemini-[A-Za-z0-9][A-Za-z0-9._-]*\b`)
	geminiMajorPattern        = regexp.MustCompile(`^gemini-[0-9]+(?:[.-][0-9]+)?$`)
)

// testGeminiModelDiscovery returns a minimal discovery snapshot for testing.
// It is not used in production code paths.
func testGeminiModelDiscovery() harnesses.ModelDiscoverySnapshot {
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		FreshnessWindow: GeminiModelDiscoveryFreshnessWindow.String(),
	}
}

// ModelDiscoveryFromText extracts Gemini model IDs from caller-provided CLI
// output without assuming a current default model list.
func ModelDiscoveryFromText(text, source string) harnesses.ModelDiscoverySnapshot {
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		FreshnessWindow: GeminiModelDiscoveryFreshnessWindow.String(),
	}
	if source == "" {
		source = "cli-output:gemini"
	}
	snapshot.Source = source
	if models := parseGeminiModels(text); len(models) > 0 {
		snapshot.Models = models
	}
	return snapshot
}

func parseGeminiModels(text string) []string {
	text = geminiANSISequencePattern.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), "")
	return uniqueGeminiStrings(geminiModelPattern.FindAllString(text, -1))
}

func uniqueGeminiStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "gemini-cli" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func resolveGeminiModelAlias(model string, snapshot harnesses.ModelDiscoverySnapshot) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return model
	}
	if model == "gemini" {
		if resolved := latestGeminiModel("", snapshot.Models); resolved != "" {
			return resolved
		}
		return model
	}
	if geminiMajorPattern.MatchString(model) {
		prefix := strings.TrimPrefix(model, "gemini-")
		if resolved := latestGeminiModel(prefix, snapshot.Models); resolved != "" {
			return resolved
		}
	}
	return model
}

func latestGeminiModel(prefix string, models []string) string {
	best := ""
	var bestParts []int
	bestRank := -1
	for _, model := range models {
		candidate := strings.ToLower(strings.TrimSpace(model))
		parts, variant, ok := parseGeminiVersion(candidate)
		if !ok {
			continue
		}
		if prefix != "" && !strings.HasPrefix(versionKey(parts), strings.ReplaceAll(prefix, "-", ".")) {
			continue
		}
		rank := geminiVariantRank(variant)
		if best == "" || compareGeminiVersion(parts, rank, bestParts, bestRank) > 0 {
			best = candidate
			bestParts = parts
			bestRank = rank
		}
	}
	return best
}

func parseGeminiVersion(model string) ([]int, string, bool) {
	if !strings.HasPrefix(model, "gemini-") {
		return nil, "", false
	}
	rest := strings.TrimPrefix(model, "gemini-")
	raw := strings.FieldsFunc(rest, func(r rune) bool { return r == '.' || r == '-' })
	if len(raw) == 0 {
		return nil, "", false
	}
	parts := make([]int, 0, len(raw))
	var variant []string
	for _, part := range raw {
		if part == "" {
			return nil, "", false
		}
		n := 0
		numeric := true
		for _, r := range part {
			if r < '0' || r > '9' {
				numeric = false
				break
			}
			n = n*10 + int(r-'0')
		}
		if numeric && len(variant) == 0 {
			parts = append(parts, n)
			continue
		}
		variant = append(variant, part)
	}
	if len(parts) == 0 {
		return nil, "", false
	}
	return parts, strings.Join(variant, "-"), true
}

func versionKey(parts []int) string {
	out := make([]string, len(parts))
	for i, part := range parts {
		out[i] = fmt.Sprint(part)
	}
	return strings.Join(out, ".")
}

func geminiVariantRank(variant string) int {
	switch {
	case strings.Contains(variant, "pro"):
		return 30
	case strings.Contains(variant, "flash") && !strings.Contains(variant, "lite"):
		return 20
	case strings.Contains(variant, "flash-lite"), strings.Contains(variant, "lite"):
		return 10
	case variant == "":
		return 0
	default:
		return 5
	}
}

func compareGeminiVersion(a []int, aRank int, b []int, bRank int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	switch {
	case aRank > bRank:
		return 1
	case aRank < bRank:
		return -1
	default:
		return 0
	}
}
