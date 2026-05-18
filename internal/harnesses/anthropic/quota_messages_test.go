package anthropic

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsClaudeQuotaExhaustedMessage(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "out of extra usage",
			text:     "You are out of extra usage",
			expected: true,
		},
		{
			name:     "usage limit reached",
			text:     "Usage limit reached",
			expected: true,
		},
		{
			name:     "quota exhausted",
			text:     "Quota exhausted",
			expected: true,
		},
		{
			name:     "weekly quota",
			text:     "Weekly quota exceeded",
			expected: true,
		},
		{
			name:     "current week exhausted",
			text:     "current week is exhausted",
			expected: true,
		},
		{
			name:     "case insensitive",
			text:     "OUT OF EXTRA USAGE",
			expected: true,
		},
		{
			name:     "with whitespace",
			text:     "   out of extra usage   ",
			expected: true,
		},
		{
			name:     "normal message",
			text:     "Hello, how can I help you?",
			expected: false,
		},
		{
			name:     "empty string",
			text:     "",
			expected: false,
		},
		{
			name:     "partial match - only contains week",
			text:     "current week is good",
			expected: false,
		},
		{
			name:     "partial match - only contains exhaust",
			text:     "exhausting day",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsClaudeQuotaExhaustedMessage(tt.text)
			assert.Equal(t, tt.expected, result, "IsClaudeQuotaExhaustedMessage(%q) = %v, want %v", tt.text, result, tt.expected)
		})
	}
}

func TestMarkClaudeQuotaExhaustedFromMessage(t *testing.T) {
	t.Run("marks exhaustion from valid message", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", t.TempDir()+"/quota.json")
		now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
		marked := MarkClaudeQuotaExhaustedFromMessage("out of extra usage", now)
		assert.True(t, marked)
	})

	t.Run("ignores non-quota message", func(t *testing.T) {
		t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", t.TempDir()+"/quota.json")
		marked := MarkClaudeQuotaExhaustedFromMessage("Hello, how can I help?", time.Time{})
		assert.False(t, marked)
	})

	t.Run("writes cache entry with exhausted snapshot", func(t *testing.T) {
		cacheDir := t.TempDir()
		cacheFile := cacheDir + "/quota.json"
		t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", cacheFile)

		now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
		marked := MarkClaudeQuotaExhaustedFromMessage("usage limit reached", now)
		require.True(t, marked)

		// Verify the cache was written
		snap, ok := ReadClaudeQuotaFrom(cacheFile)
		require.True(t, ok)
		require.NotNil(t, snap)
		assert.Equal(t, 0, snap.FiveHourRemaining)
		assert.Equal(t, 0, snap.WeeklyRemaining)
		assert.Equal(t, 100, snap.FiveHourLimit)
		assert.Equal(t, 100, snap.WeeklyLimit)
		assert.Equal(t, "runtime_error", snap.Source)
	})
}
