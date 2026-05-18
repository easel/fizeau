package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

const openCodeModelDiscoveryFreshnessWindow = 24 * time.Hour

var opencodeModelLinePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[^\s]+$`)

// testOpenCodeModelDiscovery returns a minimal discovery snapshot for testing.
// It is not used in production code paths.
func testOpenCodeModelDiscovery() harnesses.ModelDiscoverySnapshot {
	return harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: []string{"minimal", "low", "medium", "high", "max"},
		FreshnessWindow: openCodeModelDiscoveryFreshnessWindow.String(),
	}
}

// openCodeModelCost captures the per-million-token prices printed by
// `opencode models --verbose`.
type openCodeModelCost struct {
	InputUSDPerMTok      float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok     float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok  float64 `json:"cache_read_usd_per_mtok"`
	CacheWriteUSDPerMTok float64 `json:"cache_write_usd_per_mtok"`
}

// openCodeModelEvidence captures the stable model metadata exposed by
// `opencode models --verbose`. It intentionally does not include account or
// quota state because the current opencode CLI does not expose those as
// structured data.
type openCodeModelEvidence struct {
	Model        string             `json:"model"`
	ProviderID   string             `json:"provider_id,omitempty"`
	ModelID      string             `json:"model_id,omitempty"`
	Status       string             `json:"status,omitempty"`
	Cost         *openCodeModelCost `json:"cost,omitempty"`
	ContextLimit int                `json:"context_limit,omitempty"`
	OutputLimit  int                `json:"output_limit,omitempty"`
	Reasoning    bool               `json:"reasoning"`
	ToolCall     bool               `json:"tool_call"`
	Attachment   bool               `json:"attachment"`
	Variants     []string           `json:"variants,omitempty"`
}

func readOpenCodeModelDiscovery(ctx context.Context, binary string, args ...string) (harnesses.ModelDiscoverySnapshot, error) {
	if binary == "" {
		binary = "opencode"
	}
	if len(args) == 0 {
		args = []string{"models"}
	}
	cmd := harnesses.HarnessCommand(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("opencode models: %w", err)
	}
	raw := string(out)
	if len(parseOpenCodeModels(raw)) == 0 {
		if evidence, err := parseOpenCodeVerboseModelEvidence(raw); err != nil || len(evidence) == 0 {
			return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("opencode models returned no provider/model IDs")
		}
	}
	snapshot := opencodeDiscoveryFromText(raw, sourceForOpenCodeModelArgs(args))
	models := snapshot.Models
	if len(models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("opencode models returned no provider/model IDs")
	}
	return snapshot, nil
}

func readOpenCodeVerboseModelEvidence(ctx context.Context, binary string, args ...string) ([]openCodeModelEvidence, error) {
	if binary == "" {
		binary = "opencode"
	}
	if len(args) == 0 {
		args = []string{"models", "--verbose"}
	}
	cmd := harnesses.HarnessCommand(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("opencode models --verbose: %w", err)
	}
	return parseOpenCodeVerboseModelEvidence(string(out))
}

func opencodeDiscoveryFromText(text, source string) harnesses.ModelDiscoverySnapshot {
	snapshot := harnesses.ModelDiscoverySnapshot{
		CapturedAt:      time.Now().UTC(),
		ReasoningLevels: []string{"minimal", "low", "medium", "high", "max"},
		FreshnessWindow: openCodeModelDiscoveryFreshnessWindow.String(),
	}
	if source != "" {
		snapshot.Source = source
	}
	if evidence, err := parseOpenCodeVerboseModelEvidence(text); err == nil && len(evidence) > 0 {
		snapshot.Models = modelsFromOpenCodeEvidence(evidence)
		if levels := reasoningLevelsFromOpenCodeEvidence(evidence); len(levels) > 0 {
			snapshot.ReasoningLevels = levels
		}
		snapshot.Detail = fmt.Sprintf("opencode models --verbose returned %d model records; per-model costs are present for %d records; opencode run --help documents -m/--model and --variant", len(evidence), countOpenCodeCostEvidence(evidence))
		return snapshot
	}
	if models := parseOpenCodeModels(text); len(models) > 0 {
		snapshot.Models = models
	}
	return snapshot
}

func parseOpenCodeModels(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	out := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 1 {
			continue
		}
		model := fields[0]
		if !opencodeModelLinePattern.MatchString(model) {
			continue
		}
		out = appendUniqueString(out, model)
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

func sourceForOpenCodeModelArgs(args []string) string {
	source := "cli:opencode models"
	for _, arg := range args {
		if arg == "--verbose" {
			source = "cli:opencode models --verbose"
			break
		}
	}
	return source
}

func parseOpenCodeVerboseModelEvidence(text string) ([]openCodeModelEvidence, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	records := make([]openCodeModelEvidence, 0)
	for i := 0; i < len(lines); i++ {
		model := strings.TrimSpace(lines[i])
		if !opencodeModelLinePattern.MatchString(model) {
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[j]), "{") {
			continue
		}
		block, next, err := collectOpenCodeJSONBlock(lines, j)
		if err != nil {
			return nil, fmt.Errorf("parse verbose model %q: %w", model, err)
		}
		record, err := decodeOpenCodeModelEvidence(model, block)
		if err != nil {
			return nil, fmt.Errorf("parse verbose model %q: %w", model, err)
		}
		records = append(records, record)
		i = next
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("opencode verbose model output contained no JSON model records")
	}
	return records, nil
}

func collectOpenCodeJSONBlock(lines []string, start int) (string, int, error) {
	var block strings.Builder
	depth := 0
	started := false
	inString := false
	escaped := false
	for i := start; i < len(lines); i++ {
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		line := lines[i]
		block.WriteString(line)
		for _, r := range line {
			if escaped {
				escaped = false
				continue
			}
			if inString {
				switch r {
				case '\\':
					escaped = true
				case '"':
					inString = false
				}
				continue
			}
			switch r {
			case '"':
				inString = true
			case '{':
				started = true
				depth++
			case '}':
				depth--
				if depth < 0 {
					return "", i, fmt.Errorf("unexpected closing brace")
				}
			}
		}
		if started && depth == 0 {
			return block.String(), i, nil
		}
	}
	return "", len(lines) - 1, fmt.Errorf("unterminated JSON object")
}

func decodeOpenCodeModelEvidence(model, raw string) (openCodeModelEvidence, error) {
	var env struct {
		ID         string          `json:"id"`
		ProviderID string          `json:"providerID"`
		Status     string          `json:"status"`
		Cost       json.RawMessage `json:"cost"`
		Limit      struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		} `json:"limit"`
		Capabilities struct {
			Reasoning  bool `json:"reasoning"`
			ToolCall   bool `json:"toolcall"`
			Attachment bool `json:"attachment"`
		} `json:"capabilities"`
		Variants map[string]json.RawMessage `json:"variants"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return openCodeModelEvidence{}, err
	}
	evidence := openCodeModelEvidence{
		Model:        model,
		ProviderID:   env.ProviderID,
		ModelID:      env.ID,
		Status:       env.Status,
		ContextLimit: env.Limit.Context,
		OutputLimit:  env.Limit.Output,
		Reasoning:    env.Capabilities.Reasoning,
		ToolCall:     env.Capabilities.ToolCall,
		Attachment:   env.Capabilities.Attachment,
		Variants:     sortedOpenCodeVariantNames(env.Variants),
	}
	if len(env.Cost) > 0 && string(env.Cost) != "null" {
		var cost struct {
			Input  float64 `json:"input"`
			Output float64 `json:"output"`
			Cache  struct {
				Read  float64 `json:"read"`
				Write float64 `json:"write"`
			} `json:"cache"`
		}
		if err := json.Unmarshal(env.Cost, &cost); err != nil {
			return openCodeModelEvidence{}, err
		}
		evidence.Cost = &openCodeModelCost{
			InputUSDPerMTok:      cost.Input,
			OutputUSDPerMTok:     cost.Output,
			CacheReadUSDPerMTok:  cost.Cache.Read,
			CacheWriteUSDPerMTok: cost.Cache.Write,
		}
	}
	return evidence, nil
}

func sortedOpenCodeVariantNames(variants map[string]json.RawMessage) []string {
	if len(variants) == 0 {
		return nil
	}
	out := make([]string, 0, len(variants))
	for variant := range variants {
		out = append(out, variant)
	}
	sort.Slice(out, func(i, j int) bool {
		left := openCodeVariantRank(out[i])
		right := openCodeVariantRank(out[j])
		if left == right {
			return out[i] < out[j]
		}
		return left < right
	})
	return out
}

func openCodeVariantRank(variant string) int {
	switch variant {
	case "none", "off":
		return 0
	case "minimal":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "xhigh":
		return 5
	case "max":
		return 6
	default:
		return 100
	}
}

func modelsFromOpenCodeEvidence(evidence []openCodeModelEvidence) []string {
	out := make([]string, 0, len(evidence))
	for _, item := range evidence {
		out = appendUniqueString(out, item.Model)
	}
	return out
}

func reasoningLevelsFromOpenCodeEvidence(evidence []openCodeModelEvidence) []string {
	seen := map[string]bool{}
	for _, item := range evidence {
		for _, variant := range item.Variants {
			seen[variant] = true
		}
	}
	order := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	out := make([]string, 0, len(seen))
	for _, variant := range order {
		if seen[variant] {
			out = append(out, variant)
			delete(seen, variant)
		}
	}
	extra := make([]string, 0, len(seen))
	for variant := range seen {
		extra = append(extra, variant)
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func countOpenCodeCostEvidence(evidence []openCodeModelEvidence) int {
	count := 0
	for _, item := range evidence {
		if item.Cost != nil {
			count++
		}
	}
	return count
}
