package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/compaction"
)

// TestReasoningTokens_FallbackFromContent covers the provider usage path,
// content fallback, and the empty-state zero case.
func TestReasoningTokens_FallbackFromContent(t *testing.T) {
	type tc struct {
		name             string
		rawUsageJSON     string
		reasoningContent string
		wantTokens       int
		wantApprox       bool
	}

	tests := []tc{
		{
			name: "usage_present_with_reasoning_content",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 40,
				},
			}),
			reasoningContent: "some thinking text that is 92 characters long for this test case ok",
			wantTokens:       40,
			wantApprox:       false,
		},
		{
			name: "usage_omits_reasoning_count_reasoning_content_present",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
			}),
			reasoningContent: strings.Repeat("0123456789", 9) + "01", // exactly 92 chars
			wantTokens:       compaction.EstimateTokens(strings.Repeat("0123456789", 9) + "01"),
			wantApprox:       true,
		},
		{
			name:             "usage_block_absent_reasoning_content_present",
			rawUsageJSON:     "",
			reasoningContent: strings.Repeat("0123456789", 9) + "01",
			wantTokens:       compaction.EstimateTokens(strings.Repeat("0123456789", 9) + "01"),
			wantApprox:       true,
		},
		{
			name: "usage_explicit_zero_reasoning_content_present",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 0,
				},
			}),
			reasoningContent: "some thinking text",
			wantTokens:       compaction.EstimateTokens("some thinking text"),
			wantApprox:       true,
		},
		{
			name:             "both_absent",
			rawUsageJSON:     "",
			reasoningContent: "",
			wantTokens:       0,
			wantApprox:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTokens, gotApprox := extractReasoningTokens(tt.rawUsageJSON, tt.reasoningContent)
			if gotTokens != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", gotTokens, tt.wantTokens)
			}
			if gotApprox != tt.wantApprox {
				t.Errorf("approx = %v, want %v", gotApprox, tt.wantApprox)
			}
		})
	}
}

// TestExtractMessageReasoningContent verifies the non-streaming message parser.
func TestExtractMessageReasoningContent(t *testing.T) {
	withContent := mustMarshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": "hi", "reasoning_content": "my thinking"}},
		},
	})
	without := mustMarshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": "hi"}},
		},
	})

	if got := extractMessageReasoningContent(withContent); got != "my thinking" {
		t.Errorf("got %q, want %q", got, "my thinking")
	}
	if got := extractMessageReasoningContent(without); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := extractMessageReasoningContent(""); got != "" {
		t.Errorf("got %q for empty input, want empty", got)
	}
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
