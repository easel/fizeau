package opencode

import (
	"context"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// TestHarnessFinalEventPreservesUsageProvenance asserts that the opencode
// harness preserves the upstream provider's usage envelope verbatim,
// including explicit zero. Per CONTRACT-003, harnesses MUST NOT silently
// substitute zero for absent or absent for zero.
func TestHarnessFinalEventPreservesUsageProvenance(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantUsage  bool
		wantInput  *int
		wantOutput *int
	}{
		{
			name:       "explicit_zero_usage_preserved",
			input:      `{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":0,"input":0,"output":0,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}}`,
			wantUsage:  true,
			wantInput:  intPtr(0),
			wantOutput: intPtr(0),
		},
		{
			name:       "positive_usage_preserved",
			input:      `{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":49,"input":42,"output":7,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0}}`,
			wantUsage:  true,
			wantInput:  intPtr(42),
			wantOutput: intPtr(7),
		},
		{
			name:      "no_usage_envelope_stays_unknown",
			input:     `plain output with no usage envelope at all`,
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

			if agg.HasUsage != tc.wantUsage {
				t.Fatalf("HasUsage: got %v, want %v", agg.HasUsage, tc.wantUsage)
			}

			var usage *harnesses.FinalUsage
			if agg.HasUsage {
				usage = &harnesses.FinalUsage{
					InputTokens:  harnesses.IntPtr(agg.InputTokens),
					OutputTokens: harnesses.IntPtr(agg.OutputTokens),
				}
			}

			if !tc.wantUsage {
				if usage != nil {
					t.Fatalf("harness silently emitted usage when provider sent none: %#v", usage)
				}
				return
			}
			if usage == nil {
				t.Fatalf("harness dropped upstream usage envelope")
			}
			if usage.InputTokens == nil || *usage.InputTokens != *tc.wantInput {
				t.Fatalf("InputTokens: got %#v, want *%d", usage.InputTokens, *tc.wantInput)
			}
			if usage.OutputTokens == nil || *usage.OutputTokens != *tc.wantOutput {
				t.Fatalf("OutputTokens: got %#v, want *%d", usage.OutputTokens, *tc.wantOutput)
			}
		})
	}
}

func intPtr(v int) *int { return &v }
