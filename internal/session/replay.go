package session

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agent "github.com/easel/fizeau/internal/core"
)

// Replay reads a session log and renders a human-readable conversation.
func Replay(path string, w io.Writer) error {
	events, err := ReadEvents(path)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	for _, e := range events {
		switch e.Type {
		case agent.EventLLMRequest:
			var data LLMRequestData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}
			fmt.Fprintf(w, "\n[LLM Request] (%d messages, %d tools)\n", len(data.Messages), len(data.Tools))
			for _, m := range data.Messages {
				switch m.Role {
				case agent.RoleSystem:
					fmt.Fprintf(w, "  [system] %s\n", m.Content)
				case agent.RoleUser:
					fmt.Fprintf(w, "  [user] %s\n", m.Content)
				case agent.RoleAssistant:
					if m.Content != "" {
						fmt.Fprintf(w, "  [assistant] %s\n", m.Content)
					}
					for _, tc := range m.ToolCalls {
						fmt.Fprintf(w, "  [assistant tool_call] %s(%s)\n", tc.Name, compactJSON(tc.Arguments))
					}
				case agent.RoleTool:
					content := m.Content
					if len(content) > 200 {
						content = content[:200] + "...[truncated]"
					}
					fmt.Fprintf(w, "  [tool result] %s\n", strings.ReplaceAll(content, "\n", "\n              "))
				}
			}

		case agent.EventSessionStart:
			var data SessionStartData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}
			fmt.Fprintf(w, "=== Session %s ===\n", e.SessionID)
			fmt.Fprintf(w, "Time: %s\n", e.Timestamp.Format("2006-01-02 15:04:05 UTC"))
			if data.ParentSessionID != "" {
				fmt.Fprintf(w, "Parent session: %s | Continuation requested: %s | Actual: %s\n",
					data.ParentSessionID, data.ContinuationPolicy, data.Continuation)
			}
			fmt.Fprintf(w, "Provider: %s | Model: %s\n", data.Provider, data.Model)
			if data.SelectedEndpoint != "" {
				fmt.Fprintf(w, "Selected endpoint: %s\n", data.SelectedEndpoint)
			}
			if data.SelectedServerInstance != "" {
				fmt.Fprintf(w, "Selected server instance: %s\n", data.SelectedServerInstance)
			}
			if data.Sticky.KeyPresent || data.Sticky.Assignment != "" || data.Sticky.Reason != "" {
				fmt.Fprintf(w, "Sticky: key=%s assignment=%s",
					routingStickyLabel(data.Sticky.KeyPresent),
					labelOrUnknown(data.Sticky.Assignment))
				if data.Sticky.Reason != "" {
					fmt.Fprintf(w, " reason=%s", data.Sticky.Reason)
				}
				if data.Sticky.Bonus != 0 {
					fmt.Fprintf(w, " bonus=%.2f", data.Sticky.Bonus)
				}
				fmt.Fprintln(w)
			}
			if data.Utilization.Source != "" || data.Utilization.Freshness != "" ||
				data.Utilization.ActiveRequests != nil || data.Utilization.QueuedRequests != nil ||
				data.Utilization.MaxConcurrency != nil || data.Utilization.CachePressure != nil {
				fmt.Fprintf(w, "Utilization: source=%s freshness=%s",
					labelOrUnknown(data.Utilization.Source),
					labelOrUnknown(data.Utilization.Freshness))
				if data.Utilization.ActiveRequests != nil {
					fmt.Fprintf(w, " active=%d", *data.Utilization.ActiveRequests)
				}
				if data.Utilization.QueuedRequests != nil {
					fmt.Fprintf(w, " queued=%d", *data.Utilization.QueuedRequests)
				}
				if data.Utilization.MaxConcurrency != nil {
					fmt.Fprintf(w, " max=%d", *data.Utilization.MaxConcurrency)
				}
				if data.Utilization.CachePressure != nil {
					fmt.Fprintf(w, " cache_pressure=%.2f", *data.Utilization.CachePressure)
				}
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "Max iterations: %d | Work dir: %s\n", data.MaxIterations, data.WorkDir)
			if data.SystemPrompt != "" {
				fmt.Fprintf(w, "\n[System]\n%s\n", data.SystemPrompt)
			}
			fmt.Fprintf(w, "\n[User]\n%s\n", data.Prompt)

		case agent.EventLLMResponse:
			var data LLMResponseData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}
			fmt.Fprintf(w, "\n[Assistant] (%dms, %d in / %d out tokens",
				data.LatencyMs, data.Usage.Input, data.Usage.Output)
			if data.CostUSD > 0 {
				fmt.Fprintf(w, ", $%.4f", data.CostUSD)
			}
			fmt.Fprintf(w, ")\n")
			if data.Content != "" {
				fmt.Fprintf(w, "%s\n", data.Content)
			}
			if len(data.ToolCalls) > 0 {
				fmt.Fprintf(w, "[%d tool call(s)]\n", len(data.ToolCalls))
			}

		case agent.EventToolCall:
			var data ToolCallData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}
			fmt.Fprintf(w, "\n  > %s (%dms)\n", data.Tool, data.DurationMs)
			fmt.Fprintf(w, "    Input:  %s\n", compactJSON(data.Input))
			output := data.Output
			if len(output) > 200 {
				output = output[:200] + "...[truncated]"
			}
			fmt.Fprintf(w, "    Output: %s\n", strings.ReplaceAll(output, "\n", "\n            "))
			if data.Error != "" {
				fmt.Fprintf(w, "    Error:  %s\n", data.Error)
			}

		case agent.EventSessionEnd:
			var data SessionEndData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}
			fmt.Fprintf(w, "\n=== End (%s) ===\n", data.Status)
			if data.ParentSessionID != "" {
				fmt.Fprintf(w, "Parent session: %s | Continuation requested: %s | Actual: %s\n",
					data.ParentSessionID, data.ContinuationPolicy, data.Continuation)
			}
			if data.Model != "" {
				fmt.Fprintf(w, "Model: %s\n", data.Model)
			}
			if data.SelectedEndpoint != "" {
				fmt.Fprintf(w, "Selected endpoint: %s\n", data.SelectedEndpoint)
			}
			if data.SelectedServerInstance != "" {
				fmt.Fprintf(w, "Selected server instance: %s\n", data.SelectedServerInstance)
			}
			fmt.Fprintf(w, "Duration: %dms | Tokens: %d in / %d out",
				data.DurationMs, data.Tokens.Input, data.Tokens.Output)
			if data.CostUSD == nil || *data.CostUSD < 0 {
				fmt.Fprintf(w, " | Cost: unknown")
			} else if *data.CostUSD > 0 {
				fmt.Fprintf(w, " | Cost: $%.4f", *data.CostUSD)
			} else {
				fmt.Fprintf(w, " | Cost: $0 (local)")
			}
			fmt.Fprintln(w)
			if len(data.Metadata) > 0 {
				fmt.Fprintf(w, "Metadata:")
				for k, v := range data.Metadata {
					fmt.Fprintf(w, " %s=%s", k, v)
				}
				fmt.Fprintln(w)
			}
			if data.Error != "" {
				fmt.Fprintf(w, "Error: %s\n", data.Error)
			}
		}
	}
	return nil
}

func routingStickyLabel(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func labelOrUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func compactJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	data, _ := json.Marshal(v)
	return string(data)
}
