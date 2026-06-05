package opencode

import (
	"context"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// TestHarnessFinalEventPreservesUsageProvenance asserts that the opencode
// harness preserves the upstream step_finish token payload verbatim,
// including explicit zero. Per CONTRACT-003, harnesses MUST NOT silently
// substitute zero for absent or absent for zero.
func TestHarnessFinalEventPreservesUsageProvenance(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		wantUsage      bool
		wantInput      *int
		wantOutput     *int
		wantReasoning  *int
		wantCacheRead  *int
		wantCacheWrite *int
		wantTotal      *int
	}{
		{
			name:           "explicit_zero_usage_preserved",
			input:          `{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":0,"input":0,"output":0,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}}`,
			wantUsage:      true,
			wantInput:      harnesses.IntPtr(0),
			wantOutput:     harnesses.IntPtr(0),
			wantReasoning:  harnesses.IntPtr(0),
			wantCacheRead:  harnesses.IntPtr(0),
			wantCacheWrite: harnesses.IntPtr(0),
			wantTotal:      harnesses.IntPtr(0),
		},
		{
			name:           "positive_usage_preserved",
			input:          `{"type":"step_start","part":{"type":"step-start"}}` + "\n" + `{"type":"text","part":{"type":"text","text":"PONG"}}` + "\n" + `{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":13526,"input":13505,"output":3,"reasoning":18,"cache":{"write":0,"read":0}},"cost":0}}`,
			wantUsage:      true,
			wantInput:      harnesses.IntPtr(13505),
			wantOutput:     harnesses.IntPtr(3),
			wantReasoning:  harnesses.IntPtr(18),
			wantCacheRead:  harnesses.IntPtr(0),
			wantCacheWrite: harnesses.IntPtr(0),
			wantTotal:      harnesses.IntPtr(13526),
		},
		{
			name:      "no_step_finish_stays_unknown",
			input:     `{"type":"text","part":{"type":"text","text":"PONG"}}`,
			wantUsage: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := make(chan harnesses.Event, 8)
			var seq int64
			agg, err := parseOpencodeStream(context.Background(), strings.NewReader(tc.input), out, nil, &seq)
			close(out)
			if err != nil {
				t.Fatalf("parseOpencodeStream: %v", err)
			}

			usage, warnings := harnesses.ResolveFinalUsage(agg.UsageSources)
			if len(warnings) != 0 {
				t.Fatalf("ResolveFinalUsage warnings = %#v, want none", warnings)
			}

			if tc.wantUsage {
				if usage == nil {
					t.Fatal("harness dropped upstream usage payload")
				}
				if usage.Source != harnesses.UsageSourceNativeStream {
					t.Fatalf("usage.Source = %q, want %q", usage.Source, harnesses.UsageSourceNativeStream)
				}
				if usage.InputTokens == nil || *usage.InputTokens != *tc.wantInput {
					t.Fatalf("InputTokens: got %#v, want *%d", usage.InputTokens, *tc.wantInput)
				}
				if usage.OutputTokens == nil || *usage.OutputTokens != *tc.wantOutput {
					t.Fatalf("OutputTokens: got %#v, want *%d", usage.OutputTokens, *tc.wantOutput)
				}
				if usage.ReasoningTokens == nil || *usage.ReasoningTokens != *tc.wantReasoning {
					t.Fatalf("ReasoningTokens: got %#v, want *%d", usage.ReasoningTokens, *tc.wantReasoning)
				}
				if usage.CacheReadTokens == nil || *usage.CacheReadTokens != *tc.wantCacheRead {
					t.Fatalf("CacheReadTokens: got %#v, want *%d", usage.CacheReadTokens, *tc.wantCacheRead)
				}
				if usage.CacheWriteTokens == nil || *usage.CacheWriteTokens != *tc.wantCacheWrite {
					t.Fatalf("CacheWriteTokens: got %#v, want *%d", usage.CacheWriteTokens, *tc.wantCacheWrite)
				}
				if usage.TotalTokens == nil || *usage.TotalTokens != *tc.wantTotal {
					t.Fatalf("TotalTokens: got %#v, want *%d", usage.TotalTokens, *tc.wantTotal)
				}
				return
			}

			if usage != nil {
				t.Fatalf("harness silently emitted usage when provider sent none: %#v", usage)
			}
		})
	}
}
