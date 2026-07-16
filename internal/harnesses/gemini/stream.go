package gemini

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

// streamAggregate captures usage extracted from gemini output. HasUsage is
// set when the gemini stats / quota envelope was present at all (regardless
// of values), so explicit upstream zeros are preserved per CONTRACT-003.
type streamAggregate struct {
	FinalText    string
	HasUsage     bool
	InputTokens  int
	OutputTokens int
	CacheTokens  int
	TotalTokens  int
	FinalCostUSD *float64
	CostSource   harnesses.CostSource
	// CostUSD remains an in-memory compatibility mirror. FinalCostUSD and
	// CostSource are authoritative, including for an explicitly reported zero.
	CostUSD float64
}

// geminiStatsEnvelope is a minimal view of the gemini JSON stats block.
// From DDx ExtractUsage("gemini"):
//
//	{"stats":{"models":{"<model>":{"tokens":{"input":N,"total":M}}}}}
//
// output_tokens = total - input.
type geminiStatsModel struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheTokens  int `json:"cached"`
	TotalTokens  int `json:"total_tokens"`
	Tokens       struct {
		Input int `json:"input"`
		Total int `json:"total"`
	} `json:"tokens"`
}

type geminiStatsBlock struct {
	InputTokens  int                         `json:"input_tokens"`
	OutputTokens int                         `json:"output_tokens"`
	CacheTokens  int                         `json:"cached"`
	TotalTokens  int                         `json:"total_tokens"`
	CostUSD      *float64                    `json:"cost_usd"`
	TotalCostUSD *float64                    `json:"total_cost_usd"`
	Models       map[string]geminiStatsModel `json:"models"`
}

type geminiQuotaTokenCount struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type geminiMetaBlock struct {
	Quota struct {
		TokenCount geminiQuotaTokenCount `json:"token_count"`
		ModelUsage []struct {
			Model      string                `json:"model"`
			TokenCount geminiQuotaTokenCount `json:"token_count"`
		} `json:"model_usage"`
	} `json:"quota"`
}

type geminiStatsEnvelope struct {
	// Pointers so a missing top-level "stats" / "_meta" object stays nil;
	// presence (even all-zero) is preserved as an upstream usage signal per
	// CONTRACT-003.
	Stats *geminiStatsBlock `json:"stats,omitempty"`
	Meta  *geminiMetaBlock  `json:"_meta,omitempty"`
}

type geminiStreamEnvelope struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content string          `json:"content"`
	Delta   bool            `json:"delta"`
	Status  string          `json:"status"`
	Error   json.RawMessage `json:"error"`
}

// parseGeminiUsage extracts token usage from the last non-empty line of
// gemini output that is a valid JSON stats envelope.
// Returns a streamAggregate with FinalText = full output.
func parseGeminiUsage(output string) *streamAggregate {
	agg := &streamAggregate{FinalText: output, CostSource: harnesses.CostSourceUnknown}

	// Try last non-empty line (gemini may emit stats on the last line).
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			last = lines[i]
			break
		}
	}
	if last == "" {
		return agg
	}

	var env geminiStatsEnvelope
	if err := json.Unmarshal([]byte(last), &env); err != nil {
		return agg
	}

	applyGeminiStats(agg, env)
	return agg
}

func parseGeminiStreamOutput(output string) (*streamAggregate, bool) {
	agg := &streamAggregate{CostSource: harnesses.CostSourceUnknown}
	var sawStreamEvent bool
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		var env geminiStreamEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue
		}
		switch env.Type {
		case "init", "message", "result", "error":
			sawStreamEvent = true
		default:
			continue
		}
		if env.Type == "message" && env.Role == "assistant" && env.Content != "" {
			agg.FinalText += env.Content
		}
		if env.Type == "result" {
			var stats geminiStatsEnvelope
			if err := json.Unmarshal([]byte(line), &stats); err == nil {
				applyGeminiStats(agg, stats)
			}
			if env.Status != "" && env.Status != "success" && env.Error != nil {
				agg.FinalText = strings.TrimSpace(agg.FinalText)
			}
		}
	}
	return agg, sawStreamEvent
}

func applyGeminiStats(agg *streamAggregate, env geminiStatsEnvelope) {
	if env.Stats != nil {
		agg.HasUsage = true
		if env.Stats.InputTokens > 0 || env.Stats.OutputTokens > 0 || env.Stats.TotalTokens > 0 || env.Stats.CacheTokens > 0 {
			agg.InputTokens = env.Stats.InputTokens
			agg.OutputTokens = env.Stats.OutputTokens
			agg.CacheTokens = env.Stats.CacheTokens
			agg.TotalTokens = env.Stats.TotalTokens
			if agg.TotalTokens == 0 && (agg.InputTokens > 0 || agg.OutputTokens > 0) {
				agg.TotalTokens = agg.InputTokens + agg.OutputTokens
			}
		} else {
			for _, model := range env.Stats.Models {
				input := firstPositive(model.InputTokens, model.Tokens.Input)
				output := model.OutputTokens
				total := firstPositive(model.TotalTokens, model.Tokens.Total)
				if output == 0 && total > 0 {
					output = total - input
				}
				agg.InputTokens += input
				agg.OutputTokens += output
				agg.CacheTokens += model.CacheTokens
				agg.TotalTokens += total
			}
			if agg.TotalTokens == 0 && (agg.InputTokens > 0 || agg.OutputTokens > 0) {
				agg.TotalTokens = agg.InputTokens + agg.OutputTokens
			}
		}
		agg.recordReportedCost(env.Stats.CostUSD, env.Stats.TotalCostUSD)
	}
	if env.Meta != nil {
		agg.HasUsage = true
		if env.Meta.Quota.TokenCount.InputTokens > 0 || env.Meta.Quota.TokenCount.OutputTokens > 0 || env.Meta.Quota.TokenCount.TotalTokens > 0 {
			agg.InputTokens = env.Meta.Quota.TokenCount.InputTokens
			agg.OutputTokens = env.Meta.Quota.TokenCount.OutputTokens
			agg.TotalTokens = env.Meta.Quota.TokenCount.TotalTokens
			if agg.TotalTokens == 0 {
				agg.TotalTokens = agg.InputTokens + agg.OutputTokens
			}
		} else if len(env.Meta.Quota.ModelUsage) > 0 {
			agg.InputTokens = 0
			agg.OutputTokens = 0
			agg.TotalTokens = 0
			for _, model := range env.Meta.Quota.ModelUsage {
				agg.InputTokens += model.TokenCount.InputTokens
				agg.OutputTokens += model.TokenCount.OutputTokens
				agg.TotalTokens += model.TokenCount.TotalTokens
			}
			if agg.TotalTokens == 0 {
				agg.TotalTokens = agg.InputTokens + agg.OutputTokens
			}
		}
	}
}

func (a *streamAggregate) recordReportedCost(costUSD, totalCostUSD *float64) {
	a.FinalCostUSD = nil
	a.CostSource = harnesses.CostSourceUnknown
	a.CostUSD = 0

	selected := costUSD
	if selected == nil {
		selected = totalCostUSD
	}
	if selected == nil || *selected < 0 {
		return
	}

	value := *selected
	a.FinalCostUSD = &value
	a.CostSource = harnesses.CostSourceReported
	a.CostUSD = value
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func geminiStreamError(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		var env geminiStreamEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil || env.Type != "error" {
			continue
		}
		if len(env.Error) == 0 {
			return "gemini stream error"
		}
		var msg string
		if err := json.Unmarshal(env.Error, &msg); err == nil && msg != "" {
			return msg
		}
		var obj map[string]any
		if err := json.Unmarshal(env.Error, &obj); err != nil {
			return string(env.Error)
		}
		for _, key := range []string{"message", "error"} {
			if value, ok := obj[key]; ok {
				return strings.TrimSpace(toString(value))
			}
		}
		return string(env.Error)
	}
	return ""
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	}
}
