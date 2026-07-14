package pi

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/pty/cassette"
)

const piModelDiscoveryFreshnessWindow = 24 * time.Hour

var piDefaultModelPattern = regexp.MustCompile(`(?i)--model\s+<id>.*\(default:\s*([^)]+)\)`)

// readPiModelDiscoveryFromHelp captures the stable pi --help surface. Help
// exposes the default model and thinking levels without requiring credentials.
func readPiModelDiscoveryFromHelp(ctx context.Context, binary string, args ...string) (harnesses.ModelDiscoverySnapshot, error) {
	if binary == "" {
		binary = "pi"
	}
	if len(args) == 0 {
		args = []string{"--help"}
	}
	out, err := harnesses.HarnessCombinedOutput(ctx, "pi", binary, args...)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("pi help: %w", err)
	}
	snapshot := piDiscoveryFromHelp(string(out), "cli-help:pi")
	if len(snapshot.Models) == 0 && len(snapshot.ReasoningLevels) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("pi help did not expose model or thinking metadata")
	}
	return snapshot, nil
}

// readPiModelDiscoveryFromListModels captures the concrete model table from
// pi --list-models. The command prints catalog metadata and does not execute a
// prompt, but callers can fall back to readPiModelDiscoveryFromHelp if a local
// pi build lacks the command.
func readPiModelDiscoveryFromListModels(ctx context.Context, binary string, args ...string) (harnesses.ModelDiscoverySnapshot, error) {
	if binary == "" {
		binary = "pi"
	}
	if len(args) == 0 {
		args = []string{"--list-models"}
	}
	out, err := harnesses.HarnessCombinedOutput(ctx, "pi", binary, args...)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("pi list models: %w", err)
	}
	models := parsePiListModels(string(out))
	if len(models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("pi list models did not expose any models")
	}
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		Models:          models,
		Source:          "cli:list-models",
		ReasoningLevels: []string{"off", "minimal", "low", "medium", "high", "xhigh"},
		FreshnessWindow: piModelDiscoveryFreshnessWindow.String(),
		Detail:          "pi --list-models returned a concrete provider/model table; thinking levels come from the documented --thinking CLI surface",
	}
	return snapshot, nil
}

// piListModel is one row of the `pi --list-models` provider/model table.
type piListModel struct {
	Provider string
	Model    string
}

// readPiModelDiscoveryFromListModelsForProviders parses `pi --list-models` and
// returns a snapshot whose Models list is restricted to the supplied provider
// names (case-insensitive). This is the surface used when a caller has
// configured local providers (e.g. lmstudio, omlx) and only wants Pi's
// concrete model table for those providers.
//
// If providers is empty the snapshot carries all discovered models.
func readPiModelDiscoveryFromListModelsForProviders(ctx context.Context, binary string, providers []string, args ...string) (harnesses.ModelDiscoverySnapshot, error) {
	if binary == "" {
		binary = "pi"
	}
	if len(args) == 0 {
		args = []string{"--list-models"}
	}
	out, err := harnesses.HarnessCombinedOutput(ctx, "pi", binary, args...)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("pi list models: %w", err)
	}
	rows := parsePiListModelsWithProvider(string(out))
	models := filterPiModelsByProviders(rows, providers)
	if len(models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("pi list models did not expose any models for providers %v", providers)
	}
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		Models:          models,
		Source:          "cli:list-models:providers",
		ReasoningLevels: []string{"off", "minimal", "low", "medium", "high", "xhigh"},
		FreshnessWindow: piModelDiscoveryFreshnessWindow.String(),
		Detail:          "pi --list-models filtered to configured providers; thinking levels come from the documented --thinking CLI surface",
	}
	return snapshot, nil
}

func piDiscoveryFromHelp(text, source string) harnesses.ModelDiscoverySnapshot {
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: []string{"off", "minimal", "low", "medium", "high", "xhigh"},
		FreshnessWindow: piModelDiscoveryFreshnessWindow.String(),
	}
	if source != "" {
		snapshot.Source = source
	}
	if model := parsePiDefaultModel(text); model != "" {
		snapshot.Models = []string{model}
	}
	if levels := parsePiThinkingLevels(text); len(levels) > 0 {
		snapshot.ReasoningLevels = levels
	}
	return snapshot
}

func parsePiDefaultModel(text string) string {
	m := piDefaultModelPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func parsePiThinkingLevels(text string) []string {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if !strings.Contains(line, "--thinking") {
			continue
		}
		_, after, ok := strings.Cut(line, "Set thinking level:")
		if !ok {
			continue
		}
		return uniquePiStrings(strings.Split(after, ","))
	}
	return nil
}

func parsePiListModels(text string) []string {
	rows := parsePiListModelsWithProvider(text)
	models := make([]string, 0, len(rows))
	for _, row := range rows {
		models = append(models, row.Model)
	}
	return uniquePiStrings(models)
}

func parsePiListModelsWithProvider(text string) []piListModel {
	var rows []piListModel
	var sawHeader bool
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "provider") && strings.EqualFold(fields[1], "model") {
			sawHeader = true
			continue
		}
		if !sawHeader || len(fields) < 6 {
			continue
		}
		if fields[4] != "yes" && fields[4] != "no" {
			continue
		}
		rows = append(rows, piListModel{Provider: fields[0], Model: fields[1]})
	}
	return rows
}

func filterPiModelsByProviders(rows []piListModel, providers []string) []string {
	var models []string
	if len(providers) == 0 {
		for _, row := range rows {
			models = append(models, row.Model)
		}
		return uniquePiStrings(models)
	}
	allowed := make(map[string]bool, len(providers))
	for _, p := range providers {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			allowed[p] = true
		}
	}
	for _, row := range rows {
		if allowed[strings.ToLower(row.Provider)] {
			models = append(models, row.Model)
		}
	}
	return uniquePiStrings(models)
}

func uniquePiStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
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

// ReadPiModelDiscoveryFromCassette reads a recorded pi model surface cassette
// and returns the parsed model discovery snapshot. It first checks for a
// pre-parsed discovery record in the cassette; if not found, it parses the
// final text or frames.
func ReadPiModelDiscoveryFromCassette(dir string) (harnesses.ModelDiscoverySnapshot, error) {
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
	models := parsePiListModels(text)
	if len(models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("no models found in pi model cassette")
	}
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		Models:          models,
		Source:          "cassette",
		ReasoningLevels: []string{"off", "minimal", "low", "medium", "high", "xhigh"},
		FreshnessWindow: piModelDiscoveryFreshnessWindow.String(),
		Detail:          "pi model discovery from recorded cassette",
	}
	return snapshot, nil
}

func snapshotFromDiscoveryRecord(rec cassette.DiscoveryRecord) harnesses.ModelDiscoverySnapshot {
	var detail string
	if rec.Metadata != nil {
		if d, ok := rec.Metadata["detail"].(string); ok {
			detail = d
		}
	}
	return harnesses.ModelDiscoverySnapshot{
		Models:          rec.Models,
		Source:          rec.Source,
		ReasoningLevels: rec.ReasoningLevels,
		FreshnessWindow: rec.FreshnessWindow,
		Detail:          detail,
	}
}
