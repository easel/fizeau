package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractReasoningTokens covers the five resolution cases from ADR-010
// Amendment §8.
func TestExtractReasoningTokens(t *testing.T) {
	type tc struct {
		name             string
		rawUsageJSON     string
		reasoningContent string
		wantTokens       int
		wantApprox       bool
	}

	tests := []tc{
		{
			// Case 1: usage path present with positive value AND reasoning_content
			// present — usage is authoritative; approx stays false.
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
			// Case 2: usage path absent, reasoning_content present (92 chars) →
			// derive 23 tokens (92÷4). approx=true.
			name: "usage_absent_reasoning_content_present",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
			}),
			reasoningContent: strings.Repeat("0123456789", 9) + "01", // exactly 92 chars
			wantTokens:       23,                                     // 92 / 4
			wantApprox:       true,
		},
		{
			// Case 3: both absent → 0, approx=false.
			name: "both_absent",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
			}),
			reasoningContent: "",
			wantTokens:       0,
			wantApprox:       false,
		},
		{
			// Case 4: usage path present with explicit zero AND reasoning_content
			// present → respect the explicit zero; do not fall back to char-count.
			// Judgment call: the provider signalled no reasoning ran. approx=false.
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
			wantTokens:       0,
			wantApprox:       false,
		},
		{
			// Case 5 (streaming analogue): empty rawUsageJSON with aggregated
			// reasoningContent from stream deltas → content-count fallback fires.
			name:             "streaming_empty_usage_with_aggregated_content",
			rawUsageJSON:     "",
			reasoningContent: "streaming delta one" + "streaming delta two", // 38 chars → 10 tokens (ceiling)
			wantTokens:       10,                                            // (38 + 3) / 4 = 10
			wantApprox:       true,
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

// TestReasoningTokens_FallbackFromContent verifies the reasoning-token
// fallback from message.reasoning_content when usage.reasoning_tokens is absent.
// Per ADR-010 Amendment §10', when the upstream omits reasoning-token counts,
// fizeau derives them from the reasoning_content field via EstimateTokens.
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
			// Case (a): usage present with reasoning_tokens → uses usage,
			// ignores reasoning_content.
			name: "usage_present_beats_content",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 100,
				},
			}),
			reasoningContent: strings.Repeat("x", 500), // would estimate to 126 tokens if used
			wantTokens:       100,                      // usage wins
			wantApprox:       false,
		},
		{
			// Case (b): usage absent, reasoning_content present (120 chars) →
			// derives (120+3)/4 = 30 tokens via EstimateTokens.
			name: "content_fallback_when_usage_absent",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
				// no completion_tokens_details.reasoning_tokens
			}),
			reasoningContent: strings.Repeat("abcdefghij", 12), // exactly 120 chars
			wantTokens:       30,                               // (120 + 3) / 4 = 30
			wantApprox:       true,
		},
		{
			// Case (c): both absent → 0 tokens, approx=false.
			name: "both_absent_returns_zero",
			rawUsageJSON: mustMarshal(map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 50,
				"total_tokens":      60,
			}),
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
