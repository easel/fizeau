package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarizeToolCallSamples(t *testing.T) {
	cases := []struct {
		name       string
		tool       string
		input      string
		wantAction string
		wantTarget string
	}{
		{
			name:       "read lines",
			tool:       "read",
			input:      `{"path":"cli/internal/file.go","offset":21,"limit":20}`,
			wantAction: "inspect lines 22-41 in cli/internal/file.go",
			wantTarget: "cli/internal/file.go",
		},
		{
			name:       "write file",
			tool:       "write",
			input:      `{"path":"cli/internal/file.go","content":"package cli"}`,
			wantAction: "write file",
			wantTarget: "cli/internal/file.go",
		},
		{
			name:       "sed range",
			tool:       "bash",
			input:      `{"command":"sed -n '240,320p' cli/internal/agent/session_log_format.go"}`,
			wantAction: "inspect lines 240,320 in cli/internal/agent/session_log_format.go",
			wantTarget: "cli/internal/agent/session_log_format.go",
		},
		{
			name:       "sh wrapped sed range",
			tool:       "bash",
			input:      `{"command":"/bin/sh -lc \"sed -n '240,320p' cli/internal/agent/session_log_format.go\""}`,
			wantAction: "inspect lines 240,320 in cli/internal/agent/session_log_format.go",
			wantTarget: "cli/internal/agent/session_log_format.go",
		},
		{
			name:       "search",
			tool:       "bash",
			input:      `{"command":"rg -n \"FormatSessionLogLines\" cli/internal/agent/session_log_format_test.go"}`,
			wantAction: `search "FormatSessionLogLines" in cli/internal/agent/session_log_format_test.go`,
			wantTarget: "cli/internal/agent/session_log_format_test.go",
		},
		{
			name:       "git",
			tool:       "bash",
			input:      `{"command":"git add cli/internal/agent/session_log_format.go"}`,
			wantAction: "stage changes",
			wantTarget: "cli/internal/agent/session_log_format.go",
		},
		{
			name:       "test",
			tool:       "bash",
			input:      `{"command":"go test ./internal/agent -run TestFormatSessionLogLines"}`,
			wantAction: "test ./internal/agent -run TestFormatSessionLogLines",
			wantTarget: "./internal/agent -run TestFormatSessionLogLines",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SummarizeToolCall(tc.tool, json.RawMessage(tc.input))
			if got.Action != tc.wantAction {
				t.Fatalf("Action=%q, want %q", got.Action, tc.wantAction)
			}
			if got.Target != tc.wantTarget {
				t.Fatalf("Target=%q, want %q", got.Target, tc.wantTarget)
			}
		})
	}
}

func TestSummarizeOutputCountsAndRedactsExcerpt(t *testing.T) {
	raw := "token=super-secret-value first line with enough characters\nsecond line"
	got := SummarizeOutput(raw)
	if got.Bytes != len(raw) || got.Lines != 2 {
		t.Fatalf("output counts = %#v", got)
	}
	if got.Summary == "" || got.Excerpt == "" {
		t.Fatalf("missing summary/excerpt: %#v", got)
	}
	if strings.Contains(got.Summary, "super-secret-value") || strings.Contains(got.Excerpt, "super-secret-value") {
		t.Fatalf("summary leaked sensitive value: %#v", got)
	}
	if !strings.Contains(got.Excerpt, "[redacted]") {
		t.Fatalf("excerpt did not redact sensitive value: %q", got.Excerpt)
	}
}

func TestTokenThroughput(t *testing.T) {
	got := TokenThroughput(30, 1500)
	if got == nil || *got != 20 {
		t.Fatalf("TokenThroughput=%v, want 20", got)
	}
	if TokenThroughput(0, 1500) != nil || TokenThroughput(30, 0) != nil {
		t.Fatal("TokenThroughput should be nil without output tokens and duration")
	}
}
