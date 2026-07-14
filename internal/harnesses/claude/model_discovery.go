package claude

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
)

const ClaudeModelDiscoveryFreshnessWindow = 24 * time.Hour

var (
	claudeFullModelPattern         = regexp.MustCompile(`\bclaude-[a-z0-9][a-z0-9._-]*\b`)
	claudeFullFamilyVersionPattern = regexp.MustCompile(`\bclaude-(sonnet|opus|haiku|fable)-([0-9]+)[.-]([0-9]{1,2})(?:\b|-)`)
	claudeFamilyVersionPattern     = regexp.MustCompile(`\b(?:claude\s+)?(sonnet|opus|haiku|fable)\s+([0-9]+(?:[.-][0-9]+){0,2})\b`)
	claudeAliasPattern             = regexp.MustCompile(`(?m)(?:^|[\s'"])(sonnet|opus|haiku|fable)(?:$|[\s'"])`)
	claudeEffortPattern            = regexp.MustCompile(`--effort\s+<level>.*?\(([^)]*)\)`)
	claudeValidEffortPattern       = regexp.MustCompile(`(?im)^.*unknown --effort value.*valid values:\s*([^\r\n.]+)`)
)

// claudeTrustInterstitial answers Claude Code's "Do you trust the files in
// this folder?" onboarding dialog, which appears whenever the CLI is launched
// in a not-yet-trusted directory — including every fresh execute-bead worktree.
// The dialog pre-selects "Yes, I trust this folder", so Enter accepts it; the
// driver then proceeds to the normal ready prompt. Without this, PTY model
// discovery (and quota probes) stall on the dialog and time out with zero
// models, making the harness unroutable.
func claudeTrustInterstitial() ptyquota.Interstitial {
	return ptyquota.Interstitial{
		Name: "claude-folder-trust",
		Match: func(screen string) bool {
			return strings.Contains(screen, "trust the files in this folder") ||
				strings.Contains(screen, "trust this folder")
		},
		Send: []byte("\r"),
	}
}

func ReadClaudeModelDiscoveryViaPTY(timeout time.Duration, opts ...QuotaPTYOption) (harnesses.ModelDiscoverySnapshot, error) {
	cfg := quotaPTYOptions{binary: "claude"}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	reasoningCtx, cancelReasoning := context.WithTimeout(context.Background(), 5*time.Second)
	reasoningLevels, _ := readClaudeReasoningFromHelp(reasoningCtx, cfg.binary)
	cancelReasoning()
	var snapshot harnesses.ModelDiscoverySnapshot
	_, err := ptyquota.Run(context.Background(), ptyquota.Config{
		HarnessName:   "claude",
		Binary:        cfg.binary,
		Args:          cfg.args,
		Workdir:       cfg.workdir,
		Env:           cfg.env,
		Command:       "/model\r",
		ReadyMarkers:  []string{"❯", "> "},
		Interstitials: []ptyquota.Interstitial{claudeTrustInterstitial()},
		DoneWhen:      claudeModelDiscoveryComplete,
		Timeout:       timeout,
		Size:          session.Size{Rows: 50, Cols: 220},
		CassetteDir:   cfg.cassetteDir,
		Discovery: func(text string) (cassette.DiscoveryRecord, error) {
			snapshot = claudeDiscoveryFromText(text, "pty")
			if len(snapshot.ReasoningLevels) == 0 {
				snapshot.ReasoningLevels = append([]string(nil), reasoningLevels...)
			}
			if len(snapshot.Models) == 0 {
				return cassette.DiscoveryRecord{}, fmt.Errorf("no models found in claude /model output")
			}
			return discoveryRecord(snapshot), nil
		},
	})
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, err
	}
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("no models found in claude /model output")
	}
	return snapshot, nil
}

func ReadClaudeModelDiscoveryFromCassette(dir string) (harnesses.ModelDiscoverySnapshot, error) {
	reader, err := cassette.Open(dir)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, err
	}
	if rec := reader.Discovery(); rec != nil && len(rec.Models) > 0 {
		return snapshotFromDiscoveryRecord(*rec), nil
	}
	text := reader.Final().FinalText
	if text == "" {
		frames := reader.Frames()
		if len(frames) > 0 {
			text = strings.Join(frames[len(frames)-1].Text, "\n")
		}
	}
	snapshot := claudeDiscoveryFromText(text, "cassette")
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("no models found in claude model cassette")
	}
	return snapshot, nil
}

func readClaudeReasoningFromHelp(ctx context.Context, binary string, args ...string) ([]string, error) {
	if binary == "" {
		binary = "claude"
	}
	if len(args) == 0 {
		args = []string{"--help"}
	}
	out, err := harnesses.HarnessCombinedOutput(ctx, "claude", binary, args...)
	levels := parseClaudeReasoningLevels(string(out))
	if len(levels) > 0 {
		return levels, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claude help: %w", err)
	}

	// Newer Claude Code releases document --effort without enumerating the
	// accepted values in --help. Ask the CLI to validate an impossible value;
	// its local argument parser reports the authoritative choices without
	// starting a model turn.
	probeOut, probeErr := harnesses.HarnessCombinedOutput(ctx, "claude", binary, "--effort", "__fizeau_probe__", "--print", "")
	levels = parseClaudeReasoningLevels(string(probeOut))
	if len(levels) > 0 {
		return levels, nil
	}
	if probeErr != nil {
		return nil, fmt.Errorf("claude effort probe: %w", probeErr)
	}
	return nil, fmt.Errorf("claude CLI did not expose --effort levels")
}

// testClaudeModelDiscovery returns a minimal discovery snapshot for testing.
// It is not used in production code paths.
func testClaudeModelDiscovery() harnesses.ModelDiscoverySnapshot {
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
		FreshnessWindow: ClaudeModelDiscoveryFreshnessWindow.String(),
	}
}

func claudeModelDiscoveryComplete(text string) bool {
	// NOTE: ideally this would wait for the full /model picker so every tier is
	// scraped, but fizeau's VT10x emulated screen only surfaces the highlighted
	// default line reliably (the rest of the picker overlay is not captured), so
	// requiring the footer/cheapest tier here times out. Completing on the first
	// parsed model keeps discovery working (at minimum the default tier); full
	// multi-tier capture needs emulator/overlay work (tracked separately).
	return len(parseClaudeModels(text)) > 0
}

func claudeDiscoveryFromText(text, source string) harnesses.ModelDiscoverySnapshot {
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		FreshnessWindow: ClaudeModelDiscoveryFreshnessWindow.String(),
	}
	if source != "" {
		snapshot.Source = source
	}
	if models := parseClaudeModels(text); len(models) > 0 {
		snapshot.Models = models
	}
	if levels := parseClaudeReasoningLevels(text); len(levels) > 0 {
		snapshot.ReasoningLevels = levels
	}
	return snapshot
}

func parseClaudeModels(text string) []string {
	text = stripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lower := strings.ToLower(text)
	models := uniqueMatches(claudeFullModelPattern.FindAllString(lower, -1))
	for _, match := range claudeFullFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 3 {
			models = appendUniqueString(models, match[1]+"-"+match[2]+"."+match[3])
		}
	}
	for _, match := range claudeFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 2 {
			models = appendUniqueString(models, match[1]+"-"+strings.ReplaceAll(match[2], "-", "."))
		}
	}
	for _, match := range claudeAliasPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 1 {
			models = appendUniqueString(models, match[1])
		}
	}
	return models
}

func resolveClaudeFamilyAlias(model string, snapshot harnesses.ModelDiscoverySnapshot) string {
	family := strings.ToLower(strings.TrimSpace(model))
	if !isSupportedClaudeAlias(family) {
		return model
	}
	if resolved := latestClaudeFamilyVersion(family, snapshot.Models); resolved != "" {
		return resolved
	}
	return model
}

func latestClaudeFamilyVersion(family string, models []string) string {
	prefix := family + "-"
	best := ""
	var bestParts []int
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		if !strings.HasPrefix(model, prefix) {
			continue
		}
		parts, ok := parseClaudeVersionParts(strings.TrimPrefix(model, prefix))
		if !ok {
			continue
		}
		if best == "" || compareVersionParts(parts, bestParts) > 0 {
			best = model
			bestParts = parts
		}
	}
	return best
}

func parseClaudeVersionParts(version string) ([]int, bool) {
	version = strings.ReplaceAll(version, "-", ".")
	raw := strings.Split(version, ".")
	if len(raw) == 0 {
		return nil, false
	}
	parts := make([]int, 0, len(raw))
	for _, part := range raw {
		if part == "" {
			return nil, false
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil, false
			}
			n = n*10 + int(r-'0')
		}
		parts = append(parts, n)
	}
	return parts, true
}

func compareVersionParts(a, b []int) int {
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
	return 0
}

func parseClaudeReasoningLevels(text string) []string {
	text = stripANSI(text)
	m := claudeEffortPattern.FindStringSubmatch(strings.ReplaceAll(text, "\n", " "))
	if len(m) < 2 {
		m = claudeValidEffortPattern.FindStringSubmatch(text)
	}
	if len(m) < 2 {
		return nil
	}
	parts := strings.Split(m[1], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = appendUniqueString(out, strings.TrimSpace(part))
	}
	return out
}

func discoveryRecord(snapshot harnesses.ModelDiscoverySnapshot) cassette.DiscoveryRecord {
	return cassette.DiscoveryRecord{
		Source:            snapshot.Source,
		Status:            string(ptyquota.StatusOK),
		Models:            append([]string(nil), snapshot.Models...),
		ReasoningLevels:   append([]string(nil), snapshot.ReasoningLevels...),
		CapturedAt:        snapshot.CapturedAt.UTC().Format(time.RFC3339),
		FreshnessWindow:   snapshot.FreshnessWindow,
		StalenessBehavior: "stale model discovery evidence requires authenticated PTY refresh before capability promotion",
		Metadata:          map[string]any{"detail": snapshot.Detail},
	}
}

func snapshotFromDiscoveryRecord(rec cassette.DiscoveryRecord) harnesses.ModelDiscoverySnapshot {
	capturedAt, _ := time.Parse(time.RFC3339, rec.CapturedAt)
	detail, _ := rec.Metadata["detail"].(string)
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      capturedAt,
		Models:          append([]string(nil), rec.Models...),
		ReasoningLevels: append([]string(nil), rec.ReasoningLevels...),
		Source:          rec.Source,
		FreshnessWindow: rec.FreshnessWindow,
		Detail:          detail,
	}
}

func uniqueMatches(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUniqueString(out, value)
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
