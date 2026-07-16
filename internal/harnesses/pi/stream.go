package pi

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

// piEvent is a minimal view of a pi --mode json --print JSONL event.
// From DDx ExtractUsage("pi"):
//
//	type=text_end or text_delta: message.usage.{input,output,cost.total}
//	                             partial.usage.{input,output,cost.total}
//
// The last line with a "response" field carries the final answer text
// (per DDx extractOutputPiGemini).
type piUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cost   struct {
		Total *float64 `json:"total"`
	} `json:"cost"`
}

type piEvent struct {
	Type     string `json:"type"`
	Response string `json:"response,omitempty"`
	Message  struct {
		Content []piContentBlock `json:"content"`
		// Pointer so a missing usage object stays nil; presence (even with
		// all-zero counts) signals the upstream provider explicitly reported
		// usage. CONTRACT-003 requires preserving that distinction.
		Usage *piUsage `json:"usage,omitempty"`
	} `json:"message"`
	Partial struct {
		Usage *piUsage `json:"usage,omitempty"`
	} `json:"partial"`
	AssistantMessageEvent struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Delta   string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

type piContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// streamAggregate captures running totals from the pi stream.
//
// HasUsage is true when the upstream provider emitted a usage envelope on
// any line (regardless of values). InputTokens / OutputTokens then carry the
// provider's exact counts, including explicit zero. When HasUsage is false,
// the int fields are not meaningful and the runner emits no FinalUsage.
type streamAggregate struct {
	FinalText    string
	HasUsage     bool
	InputTokens  int
	OutputTokens int
	FinalCostUSD *float64
	CostSource   harnesses.CostSource
	// CostUSD remains an in-memory compatibility mirror. FinalCostUSD and
	// CostSource are authoritative, including for an explicitly reported zero.
	CostUSD float64
}

// parsePiStream reads newline-delimited pi --mode json events from r and
// emits harness Events on out. Mapping per CONTRACT-003:
//
//   - pi events with response text -> EventTypeTextDelta (last response line)
//   - pi events with usage         -> aggregate (no event; drives final Usage)
//
// Pi doesn't emit real-time tool_call/tool_result events in JSONL mode,
// so only text_delta events are emitted during the stream. Usage is captured
// from the last line with usage fields, per DDx behavior.
func parsePiStream(ctx context.Context, r io.Reader, out chan<- harnesses.Event, metadata map[string]string, seq *int64) (*streamAggregate, error) {
	agg := &streamAggregate{CostSource: harnesses.CostSourceUnknown}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)

	// Collect all lines; we need to scan backwards for usage and emit
	// response text as we go.
	var lines []string
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return agg, ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return agg, err
	}

	// Scan backwards for usage (per DDx ExtractUsage("pi")). The first line
	// (newest first) that carries a usage *envelope* wins — including when
	// the provider explicitly reports zero counts. Detection is by JSON
	// presence (pointer non-nil), not by positive value, so explicit zero is
	// preserved verbatim per CONTRACT-003.
	for i := len(lines) - 1; i >= 0; i-- {
		var ev piEvent
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			continue
		}
		if ev.Message.Usage != nil {
			agg.recordUsage(ev.Message.Usage)
			break
		}
		if ev.Partial.Usage != nil {
			agg.recordUsage(ev.Partial.Usage)
			break
		}
	}

	// Extract final response text. Prefer the legacy "response" field, then
	// current pi JSONL text_end/message content shapes.
	for i := len(lines) - 1; i >= 0; i-- {
		var ev piEvent
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			continue
		}
		if ev.Response != "" {
			agg.FinalText = ev.Response
			break
		}
		if ev.AssistantMessageEvent.Type == "text_end" && strings.TrimSpace(ev.AssistantMessageEvent.Content) != "" {
			agg.FinalText = strings.TrimSpace(ev.AssistantMessageEvent.Content)
			break
		}
		for j := len(ev.Message.Content) - 1; j >= 0; j-- {
			block := ev.Message.Content[j]
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				agg.FinalText = strings.TrimSpace(block.Text)
				break
			}
		}
		if agg.FinalText != "" {
			break
		}
	}
	if agg.FinalText == "" && len(lines) > 0 {
		// Fallback: use all lines joined as the output.
		agg.FinalText = strings.Join(lines, "\n")
	}

	// Emit a single text_delta with the final response.
	if agg.FinalText != "" {
		raw, err := json.Marshal(harnesses.TextDeltaData{Text: agg.FinalText})
		if err != nil {
			return agg, err
		}
		ev := harnesses.Event{
			Type:     harnesses.EventTypeTextDelta,
			Sequence: *seq,
			Time:     time.Now().UTC(),
			Metadata: metadata,
			Data:     raw,
		}
		*seq++
		select {
		case out <- ev:
		case <-ctx.Done():
			return agg, ctx.Err()
		}
	}

	return agg, nil
}

func (a *streamAggregate) recordUsage(usage *piUsage) {
	a.HasUsage = true
	a.InputTokens = usage.Input
	a.OutputTokens = usage.Output
	a.FinalCostUSD = nil
	a.CostSource = harnesses.CostSourceUnknown
	a.CostUSD = 0

	if usage.Cost.Total == nil || *usage.Cost.Total < 0 {
		return
	}

	value := *usage.Cost.Total
	a.FinalCostUSD = &value
	a.CostSource = harnesses.CostSourceReported
	a.CostUSD = value
}
