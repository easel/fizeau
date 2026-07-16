package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// claudeStreamEvent is a minimal, lenient view of a claude CLI stream-json
// event. Only the fields we need for progress and final result are decoded.
type claudeStreamEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Message json.RawMessage `json:"message"`
	Result  string          `json:"result"`
	Source  string          `json:"source"`
	Fresh   *bool           `json:"fresh"`

	// result-event fields
	Usage           json.RawMessage `json:"usage"`
	CapturedAt      string          `json:"captured_at"`
	TotalCostUSD    *float64        `json:"total_cost_usd"`
	DurationMsField int64           `json:"duration_ms"`
	SessionID       string          `json:"session_id"`
	IsError         bool            `json:"is_error"`

	// system/init fields
	Model string `json:"model"`
}

// claudeAssistantMessage is the shape of the "message" field in an
// {"type":"assistant",...} stream-json event. It is Claude's native
// Messages API payload.
type claudeAssistantMessage struct {
	ID      string               `json:"id"`
	Model   string               `json:"model"`
	Content []claudeMessageBlock `json:"content"`
	Usage   json.RawMessage      `json:"usage"`
	Stop    string               `json:"stop_reason,omitempty"`
}

type claudeMessageBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// claudeUserMessage is the shape of the "message" field in a
// {"type":"user",...} stream-json event, which carries tool_result content.
type claudeUserMessage struct {
	Content []claudeMessageBlock `json:"content"`
}

// streamAggregate captures running totals exposed by the claude stream so
// the runner can attach final-event usage/cost without re-parsing.
type streamAggregate struct {
	FinalText    string
	SessionID    string
	Model        string
	UsageSources []harnesses.UsageCandidate
	Warnings     []harnesses.FinalWarning
	FinalCostUSD *float64
	CostSource   harnesses.CostSource
	// CostUSD remains an in-memory compatibility mirror. FinalCostUSD and
	// CostSource are authoritative, including for an explicitly reported zero.
	CostUSD     float64
	costUnknown bool
	ToolCalls   int
	TurnCount   int
	IsError     bool
}

// parseClaudeStream reads newline-delimited claude stream-json events from r
// and emits harness Events on out. Each parsed claude event maps to zero or
// more harness Events per CONTRACT-003 §"Event JSON shapes":
//
//   - claude system/init        -> (no event; recorded into aggregate.Model/SessionID)
//   - claude assistant text     -> EventTypeTextDelta
//   - claude assistant tool_use -> EventTypeToolCall
//   - claude user tool_result   -> EventTypeToolResult
//   - claude result             -> (no event; aggregate populated with usage/cost)
//
// metadata is copied onto every emitted Event. seq starts at 0 and is
// incremented per event so callers can reconstruct ordering.
//
// parseClaudeStream returns the aggregated stream state; callers use it to
// build a synthetic final event.
//
// out is NOT closed; the caller owns the channel and is responsible for
// closing once the final event has been emitted.
//
// ctx is honored: when ctx is done the parser returns early with the
// running aggregate and ctx.Err().
func parseClaudeStream(ctx context.Context, r io.Reader, out chan<- harnesses.Event, metadata map[string]string, seq *int64) (*streamAggregate, error) {
	agg := &streamAggregate{CostSource: harnesses.CostSourceUnknown}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024) // 16MB max line — claude can dump big tool results

	emit := func(t harnesses.EventType, data any) error {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		ev := harnesses.Event{
			Type:     t,
			Sequence: *seq,
			Time:     time.Now().UTC(),
			Metadata: metadata,
			Data:     raw,
		}
		*seq++
		select {
		case out <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	for scanner.Scan() {
		// Honor cancellation between lines too.
		select {
		case <-ctx.Done():
			return agg, ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Not a JSON event — skip silently to be lenient. Matches the DDx
			// parser behavior so partial / corrupted lines don't kill the run.
			continue
		}

		switch ev.Type {
		case "system":
			if ev.Subtype == "init" {
				if ev.Model != "" {
					agg.Model = ev.Model
				}
				if ev.SessionID != "" {
					agg.SessionID = ev.SessionID
				}
			}

		case "assistant":
			var msg claudeAssistantMessage
			if len(ev.Message) > 0 {
				_ = json.Unmarshal(ev.Message, &msg)
			}
			if msg.Model != "" && agg.Model == "" {
				agg.Model = msg.Model
			}
			agg.recordUsage(msg.Usage)
			agg.TurnCount++

			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if block.Text == "" {
						continue
					}
					if err := emit(harnesses.EventTypeTextDelta, harnesses.TextDeltaData{Text: block.Text}); err != nil {
						return agg, err
					}
					// Track the most recent text content so a missing
					// result event still leaves us with output text.
					agg.FinalText = block.Text
				case "tool_use":
					name := block.Name
					if name == "" {
						name = "tool"
					}
					agg.ToolCalls++
					if err := emit(harnesses.EventTypeToolCall, harnesses.ToolCallData{
						ID:    block.ID,
						Name:  name,
						Input: block.Input,
					}); err != nil {
						return agg, err
					}
				}
			}

		case "user":
			// User messages carry tool_result content blocks.
			var msg claudeUserMessage
			if len(ev.Message) > 0 {
				_ = json.Unmarshal(ev.Message, &msg)
			}
			for _, block := range msg.Content {
				if block.Type != "tool_result" {
					continue
				}
				output := decodeToolResultContent(block.Content)
				data := harnesses.ToolResultData{
					ID:     block.ToolUseID,
					Output: output,
				}
				if block.IsError {
					data.Error = output
					data.Output = ""
				}
				if err := emit(harnesses.EventTypeToolResult, data); err != nil {
					return agg, err
				}
			}

		case "result":
			if ev.Result != "" {
				agg.FinalText = ev.Result
			}
			agg.recordUsage(ev.Usage)
			agg.recordReportedCost(ev.TotalCostUSD)
			if ev.SessionID != "" {
				agg.SessionID = ev.SessionID
			}
			if ev.IsError {
				agg.IsError = true
			}
		case "ddx.usage_source":
			agg.recordUsageSource(ev.Source, ev.Fresh, ev.CapturedAt, ev.Usage)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return agg, err
	}
	return agg, nil
}

func (a *streamAggregate) recordReportedCost(cost *float64) {
	a.FinalCostUSD = nil
	a.CostSource = harnesses.CostSourceUnknown
	a.CostUSD = 0
	if cost == nil || *cost < 0 {
		return
	}
	value := *cost
	a.FinalCostUSD = &value
	a.CostSource = harnesses.CostSourceReported
	a.CostUSD = value
}

// recordNativeCost adds one native provider turn to the configured-price
// total. Once any attempted turn has unknown cost, the aggregate remains
// unknown even if other turns have exact pricing.
func (a *streamAggregate) recordNativeCost(cost *float64) {
	if cost == nil {
		a.costUnknown = true
		a.FinalCostUSD = nil
		a.CostSource = harnesses.CostSourceUnknown
		a.CostUSD = 0
		return
	}
	if a.costUnknown {
		return
	}
	total := *cost
	if a.FinalCostUSD != nil {
		total += *a.FinalCostUSD
	}
	a.FinalCostUSD = &total
	a.CostSource = harnesses.CostSourceConfigured
	a.CostUSD = total
}

func (a *streamAggregate) recordUsage(raw json.RawMessage) {
	a.recordUsageSource(harnesses.UsageSourceNativeStream, harnesses.BoolPtr(true), "", raw)
}

func (a *streamAggregate) recordUsageSource(source string, fresh *bool, capturedAt string, raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	if source == "" {
		source = harnesses.UsageSourceFallback
	}
	counts, err := harnesses.ParseUsageJSON(raw)
	if err != nil {
		a.UsageSources = append(a.UsageSources, harnesses.UsageCandidate{
			Source:     source,
			Fresh:      fresh,
			CapturedAt: capturedAt,
			Warning:    err.Error(),
		})
		return
	}
	if counts.Any() {
		a.UsageSources = append(a.UsageSources, harnesses.UsageCandidate{
			Source:     source,
			Fresh:      fresh,
			CapturedAt: capturedAt,
			Counts:     counts,
		})
	}
}

// decodeToolResultContent normalises the variable shapes claude uses for
// tool_result content. The CLI may emit either a plain string or an array
// of content blocks; we collapse both to a single string so the harness
// Event payload stays simple.
func decodeToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Then an array of blocks like [{"type":"text","text":"..."}].
	var blocks []claudeMessageBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for i, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				if i > 0 && sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}

// claudeStreamArgsUnsupported reports whether the claude CLI rejected the
// stream-json flags. Used by the runner's fallback path to retry with the
// legacy non-streaming invocation.
func claudeStreamArgsUnsupported(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "unknown option") ||
		strings.Contains(lower, "unrecognized option") ||
		strings.Contains(lower, "invalid value for --output-format") ||
		strings.Contains(lower, "unknown argument") ||
		strings.Contains(lower, "unknown flag")
}
