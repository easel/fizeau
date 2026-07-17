// Package compaction provides conversation compaction for the agent loop.
// When the conversation history approaches the model's context window limit,
// compaction summarizes older messages and replaces them with a structured summary.
package compaction

import (
	"encoding/json"

	agent "github.com/easel/fizeau/internal/core"
)

const (
	// charsPerToken converts compaction retention token budgets back to a
	// conservative byte budget. Estimation itself is owned by core.
	charsPerToken = 4

	// imageTokenEstimate is the fixed token estimate for image content.
	// Based on pi's 4800 chars / 4 = 1200 tokens.
	imageTokenEstimate = 1200
)

// EstimateTokens estimates the token count for a string using chars/4.
func EstimateTokens(s string) int {
	return agent.EstimateTextTokens(s)
}

// EstimateMessageTokens estimates the token count for a single message,
// including role, content, tool calls, and tool call arguments.
func EstimateMessageTokens(msg agent.Message) int {
	return agent.EstimateMessageTokens(msg)
}

// EstimateConversationTokens estimates the total tokens for a slice of messages.
func EstimateConversationTokens(messages []agent.Message) int {
	return agent.EstimateProviderCallTokens(messages, nil)
}

// ShouldCompact returns true if the conversation should be compacted.
// effectiveWindow uses overflow-safe quotient/remainder scaling.
func ShouldCompact(estimatedTokens, contextWindow, effectivePercent, reserveTokens int) bool {
	if contextWindow <= 0 || effectivePercent <= 0 {
		return false
	}
	effectiveWindow := scalePercent(contextWindow, effectivePercent)
	threshold := effectiveWindow - reserveTokens
	if threshold < 0 {
		threshold = 0
	}
	return estimatedTokens > threshold
}

func scalePercent(value, percent int) int {
	if value <= 0 || percent <= 0 {
		return 0
	}
	if percent >= 100 {
		return value
	}
	quotient := value / 100
	remainder := value % 100
	return quotient*percent + remainder*percent/100
}

// TruncateToolResult truncates a tool result string to maxChars,
// appending a truncation marker if shortened.
func TruncateToolResult(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	remaining := len(s) - maxChars
	return s[:maxChars] + "\n[... " + formatInt(remaining) + " more characters truncated]"
}

func formatInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
