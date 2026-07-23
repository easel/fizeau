package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

const grokModelDiscoveryFreshnessWindow = 24 * time.Hour

// grokReasoningLevels is the CLI-level reasoning surface. The grok CLI
// accepts canonical --reasoning-effort levels and maps them onto each
// model's supported menu (grok-4.5: low/medium/high, default high).
var grokReasoningLevels = []string{"low", "medium", "high", "xhigh", "max"}

var grokModelPattern = regexp.MustCompile(`\bgrok-[A-Za-z0-9][A-Za-z0-9._-]*\b`)

var grokDefaultModelPattern = regexp.MustCompile(`(?im)^\s*Default model:\s*(\S+)\s*$`)

// readGrokModelDiscoveryFromCLI captures the stable `grok models` surface.
// The subcommand lists available models and the default without opening the
// TUI or executing a prompt. Captured output shape (grok 0.2.106):
//
//	You are logged in with grok.com.
//
//	Default model: grok-4.5
//
//	Available models:
//	  * grok-4.5 (default)
func readGrokModelDiscoveryFromCLI(ctx context.Context, binary string, args ...string) (harnesses.ModelDiscoverySnapshot, error) {
	if binary == "" {
		binary = "grok"
	}
	if len(args) == 0 {
		args = []string{"models"}
	}
	out, err := harnesses.HarnessCombinedOutput(ctx, "grok", binary, args...)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("grok models: %w", err)
	}
	snapshot := grokDiscoveryFromText(string(out), "cli:models")
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("grok models did not expose any models")
	}
	return snapshot, nil
}

// grokDiscoveryFromText parses `grok models` output (or equivalent captured
// text) into a discovery snapshot. The default model, when present, is
// ordered first.
func grokDiscoveryFromText(text, source string) harnesses.ModelDiscoverySnapshot {
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: append([]string(nil), grokReasoningLevels...),
		FreshnessWindow: grokModelDiscoveryFreshnessWindow.String(),
	}
	if source != "" {
		snapshot.Source = source
	}
	models := parseGrokModels(text)
	if def := parseGrokDefaultModel(text); def != "" {
		ordered := []string{def}
		for _, m := range models {
			if m != def {
				ordered = append(ordered, m)
			}
		}
		models = ordered
	}
	if len(models) > 0 {
		snapshot.Models = models
	}
	return snapshot
}

func parseGrokModels(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return uniqueGrokStrings(grokModelPattern.FindAllString(text, -1))
}

func parseGrokDefaultModel(text string) string {
	m := grokDefaultModelPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// grokModelsCacheFile mirrors the on-disk shape of ~/.grok/models_cache.json
// (grok 0.2.106). Only the fields the harness consumes are decoded.
type grokModelsCacheFile struct {
	FetchedAt string `json:"fetched_at"`
	Models    map[string]struct {
		Info struct {
			ID               string `json:"id"`
			Name             string `json:"name"`
			ContextWindow    int    `json:"context_window"`
			Hidden           bool   `json:"hidden"`
			ReasoningEffort  string `json:"reasoning_effort"`
			ReasoningEfforts []struct {
				ID      string `json:"id"`
				Default bool   `json:"default"`
			} `json:"reasoning_efforts"`
		} `json:"info"`
	} `json:"models"`
}

// grokModelsCachePath returns the grok CLI's own model cache location.
// FIZEAU_GROK_MODELS_CACHE overrides for tests.
func grokModelsCachePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("FIZEAU_GROK_MODELS_CACHE")); override != "" {
		return override, nil
	}
	if home := os.Getenv("GROK_HOME"); home != "" {
		return filepath.Join(home, "models_cache.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grok", "models_cache.json"), nil
}

// readGrokModelDiscoveryFromModelsCache reads the grok CLI's on-disk model
// cache as fallback discovery evidence when the CLI subcommand is
// unavailable (e.g. binary missing but state present).
func readGrokModelDiscoveryFromModelsCache(path string) (harnesses.ModelDiscoverySnapshot, error) {
	if path == "" {
		resolved, err := grokModelsCachePath()
		if err != nil {
			return harnesses.ModelDiscoverySnapshot{}, err
		}
		path = resolved
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- harness-owned cache path
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, err
	}
	var cache grokModelsCacheFile
	if err := json.Unmarshal(raw, &cache); err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("decode grok models cache: %w", err)
	}
	models := make([]string, 0, len(cache.Models))
	for id, entry := range cache.Models {
		if entry.Info.Hidden {
			continue
		}
		if entry.Info.ID != "" {
			id = entry.Info.ID
		}
		models = append(models, id)
	}
	models = uniqueGrokStrings(models)
	if len(models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("grok models cache %s exposes no models", path)
	}
	capturedAt := time.Now().UTC()
	if ts, err := time.Parse(time.RFC3339Nano, cache.FetchedAt); err == nil {
		capturedAt = ts.UTC()
	}
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      capturedAt,
		Models:          models,
		Source:          "models-cache",
		ReasoningLevels: append([]string(nil), grokReasoningLevels...),
		FreshnessWindow: grokModelDiscoveryFreshnessWindow.String(),
		Detail:          "grok CLI on-disk models_cache.json fallback",
	}, nil
}

var grokMajorAliasPattern = regexp.MustCompile(`^grok-[0-9]+$`)

func resolveGrokModelAlias(model string, snapshot harnesses.ModelDiscoverySnapshot) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return model
	}
	switch {
	case model == "grok":
		if resolved := latestGrokModel("", snapshot.Models); resolved != "" {
			return resolved
		}
	case grokMajorAliasPattern.MatchString(model):
		if resolved := latestGrokModel(strings.TrimPrefix(model, "grok-"), snapshot.Models); resolved != "" {
			return resolved
		}
	}
	return model
}

func latestGrokModel(major string, models []string) string {
	best := ""
	var bestParts []int
	bestHasSuffix := true
	for _, model := range models {
		candidate := strings.ToLower(strings.TrimSpace(model))
		parts, hasSuffix, ok := parseGrokVersion(candidate)
		if !ok {
			continue
		}
		if major != "" && (len(parts) == 0 || fmt.Sprint(parts[0]) != major) {
			continue
		}
		if best == "" || compareGrokVersion(parts, hasSuffix, bestParts, bestHasSuffix) > 0 {
			best = candidate
			bestParts = parts
			bestHasSuffix = hasSuffix
		}
	}
	return best
}

func parseGrokVersion(model string) ([]int, bool, bool) {
	if !strings.HasPrefix(model, "grok-") {
		return nil, false, false
	}
	rest := strings.TrimPrefix(model, "grok-")
	raw := strings.FieldsFunc(rest, func(r rune) bool { return r == '.' || r == '-' })
	if len(raw) == 0 {
		return nil, false, false
	}
	parts := make([]int, 0, len(raw))
	hasSuffix := false
	for _, part := range raw {
		if part == "" {
			return nil, false, false
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				hasSuffix = true
				return parts, hasSuffix, len(parts) > 0
			}
			n = n*10 + int(r-'0')
		}
		parts = append(parts, n)
	}
	return parts, hasSuffix, true
}

func compareGrokVersion(a []int, aHasSuffix bool, b []int, bHasSuffix bool) int {
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
	if aHasSuffix == bHasSuffix {
		return 0
	}
	if !aHasSuffix {
		return 1
	}
	return -1
}

func uniqueGrokStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
