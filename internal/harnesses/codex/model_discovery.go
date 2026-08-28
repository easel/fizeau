package codex

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

const CodexModelDiscoveryFreshnessWindow = 24 * time.Hour

// codexCommandEnterDelay separates the slash-command text from Enter. Codex
// (>= 0.148, kitty keyboard protocol) opens a command popup on the typed text
// and ignores an Enter delivered in the same PTY write; 50ms is enough, so
// 250ms leaves headroom on a loaded machine.
const codexCommandEnterDelay = 250 * time.Millisecond

var codexModelPattern = regexp.MustCompile(`\bgpt-[A-Za-z0-9][A-Za-z0-9._-]*\b`)

func ReadCodexModelDiscoveryViaPTY(timeout time.Duration, opts ...QuotaPTYOption) (harnesses.ModelDiscoverySnapshot, error) {
	return ReadCodexModelDiscoveryViaPTYWithContext(context.Background(), timeout, opts...)
}

// ReadCodexModelDiscoveryViaPTYWithContext discovers Codex models while the
// caller's context owns the PTY process lifecycle. ptyquota.Run waits for the
// PTY session and its process group to be reaped before returning an error.
func ReadCodexModelDiscoveryViaPTYWithContext(ctx context.Context, timeout time.Duration, opts ...QuotaPTYOption) (harnesses.ModelDiscoverySnapshot, error) {
	cfg := quotaPTYOptions{binary: "codex", args: []string{"--no-alt-screen"}}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	var snapshot harnesses.ModelDiscoverySnapshot
	_, err := ptyquota.Run(ctx, ptyquota.Config{
		HarnessName:        "codex",
		Binary:             cfg.binary,
		Args:               cfg.args,
		Workdir:            cfg.workdir,
		Env:                cfg.env,
		Command:            "/model\r",
		CommandEnterDelay:  codexCommandEnterDelay,
		ReadyMarkers:       []string{"›", "> "},
		ReadyWhen:          codexPromptReady,
		DoneWhen:           codexModelDiscoveryComplete,
		ResetBeforeCommand: true,
		Timeout:            timeout,
		Size:               session.Size{Rows: 50, Cols: 220},
		CassetteDir:        cfg.cassetteDir,
		Discovery: func(text string) (cassette.DiscoveryRecord, error) {
			snapshot = codexDiscoveryFromText(text, "pty")
			if len(snapshot.Models) == 0 {
				return cassette.DiscoveryRecord{}, fmt.Errorf("no models found in codex /model output")
			}
			return discoveryRecord(snapshot), nil
		},
	})
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, err
	}
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("no models found in codex /model output")
	}
	return snapshot, nil
}

func ReadCodexModelDiscoveryFromCassette(dir string) (harnesses.ModelDiscoverySnapshot, error) {
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
	snapshot := codexDiscoveryFromText(text, "cassette")
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("no models found in codex model cassette")
	}
	return snapshot, nil
}

// testCodexModelDiscovery returns a minimal discovery snapshot for testing.
// It is not used in production code paths.
func testCodexModelDiscovery() harnesses.ModelDiscoverySnapshot {
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
		FreshnessWindow: CodexModelDiscoveryFreshnessWindow.String(),
	}
}

// codexPromptReady reports whether the Codex TUI has finished loading. The
// "›" prompt is drawn before the header resolves its model, and slash
// commands sent while the header is absent or still reads "model: loading"
// are dropped. Ready therefore requires a resolved "model: <name>" header.
func codexPromptReady(text string) bool {
	lower := strings.ToLower(stripANSI(strings.ReplaceAll(text, "\r\n", "\n")))
	for _, line := range strings.Split(lower, "\n") {
		idx := strings.Index(line, "model:")
		if idx < 0 || !strings.Contains(line, "/model to change") {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[idx+len("model:"):]), "│"))
		value = strings.TrimSpace(strings.Split(value, "/model to change")[0])
		return value != "" && !strings.HasPrefix(value, "loading")
	}
	return false
}

func codexModelDiscoveryComplete(text string) bool {
	lower := strings.ToLower(stripANSI(strings.ReplaceAll(text, "\r\n", "\n")))
	return len(parseCodexModels(text)) > 0 &&
		strings.Contains(lower, "select model and effort") &&
		strings.Contains(lower, "press enter to confirm") &&
		strings.Contains(lower, "esc to go back")
}

func codexDiscoveryFromText(text, source string) harnesses.ModelDiscoverySnapshot {
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
		FreshnessWindow: CodexModelDiscoveryFreshnessWindow.String(),
	}
	if source != "" {
		snapshot.Source = source
	}
	if models := parseCodexModels(text); len(models) > 0 {
		snapshot.Models = models
	}
	return snapshot
}

func parseCodexModels(text string) []string {
	text = stripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	return uniqueMatches(codexModelPattern.FindAllString(text, -1))
}

func resolveCodexModelAlias(model string, snapshot harnesses.ModelDiscoverySnapshot) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return model
	}
	switch {
	case model == "gpt":
		if resolved := latestCodexModel("", snapshot.Models); resolved != "" {
			return resolved
		}
	case regexp.MustCompile(`^gpt-[0-9]+$`).MatchString(model):
		if resolved := latestCodexModel(strings.TrimPrefix(model, "gpt-"), snapshot.Models); resolved != "" {
			return resolved
		}
	}
	return model
}

func latestCodexModel(major string, models []string) string {
	best := ""
	var bestParts []int
	bestHasSuffix := true
	for _, model := range models {
		candidate := strings.ToLower(strings.TrimSpace(model))
		parts, hasSuffix, ok := parseCodexVersion(candidate)
		if !ok {
			continue
		}
		if major != "" && (len(parts) == 0 || fmt.Sprint(parts[0]) != major) {
			continue
		}
		if best == "" || compareCodexVersion(parts, hasSuffix, bestParts, bestHasSuffix) > 0 {
			best = candidate
			bestParts = parts
			bestHasSuffix = hasSuffix
		}
	}
	return best
}

func parseCodexVersion(model string) ([]int, bool, bool) {
	if !strings.HasPrefix(model, "gpt-") {
		return nil, false, false
	}
	rest := strings.TrimPrefix(model, "gpt-")
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

func compareCodexVersion(a []int, aHasSuffix bool, b []int, bHasSuffix bool) int {
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
