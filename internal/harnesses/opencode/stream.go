package opencode

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

type opencodeTokenCache struct {
	Read  int `json:"read"`
	Write int `json:"write"`
}

type opencodeTokens struct {
	Total     int                `json:"total"`
	Input     int                `json:"input"`
	Output    int                `json:"output"`
	Reasoning int                `json:"reasoning"`
	Cache     opencodeTokenCache `json:"cache"`
}

type opencodeStreamEvent struct {
	Type string `json:"type"`
	Part struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Tool   string `json:"tool"`
		CallID string `json:"callID"`
		State  struct {
			Status   string          `json:"status"`
			Input    json.RawMessage `json:"input,omitempty"`
			Output   string          `json:"output,omitempty"`
			Error    string          `json:"error,omitempty"`
			Metadata json.RawMessage `json:"metadata,omitempty"`
		} `json:"state"`
		Tokens *opencodeTokens `json:"tokens"`
		Cost   *float64        `json:"cost"`
	} `json:"part"`
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// streamAggregate captures running totals from the opencode JSONL stream.
type streamAggregate struct {
	FinalText    string
	UsageSources []harnesses.UsageCandidate
	FinalCostUSD *float64
	CostSource   harnesses.CostSource
	// CostUSD remains an in-memory compatibility mirror. FinalCostUSD and
	// CostSource are authoritative, including for an explicitly reported zero.
	CostUSD float64
}

// parseOpencodeStream reads opencode --format json newline-delimited JSON
// events from r and emits harness Events on out. Mapping:
//
//   - type==text, part.type==text         -> EventTypeTextDelta (accumulated into agg.FinalText)
//   - type==tool_use, part.type==tool     -> EventTypeToolCall then EventTypeToolResult
//   - type==step_finish                   -> aggregate tokens and cost
//   - type==error                         -> return error
//   - all other types                     -> skipped
func parseOpencodeStream(ctx context.Context, r io.Reader, out chan<- harnesses.Event, metadata map[string]string, seq *int64) (*streamAggregate, error) {
	agg := &streamAggregate{CostSource: harnesses.CostSourceUnknown}

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

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return agg, ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var ev opencodeStreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "text":
			if ev.Part.Type == "text" && ev.Part.Text != "" {
				agg.FinalText += ev.Part.Text
				if err := emit(harnesses.EventTypeTextDelta, harnesses.TextDeltaData{Text: ev.Part.Text}); err != nil {
					return agg, err
				}
			}
		case "tool_use":
			if ev.Part.Type != "tool" || ev.Part.CallID == "" || ev.Part.Tool == "" {
				continue
			}
			if err := emit(harnesses.EventTypeToolCall, harnesses.ToolCallData{
				ID:    ev.Part.CallID,
				Name:  ev.Part.Tool,
				Input: ev.Part.State.Input,
			}); err != nil {
				return agg, err
			}
			if ev.Part.State.Status == "completed" || ev.Part.State.Status == "error" {
				result := harnesses.ToolResultData{
					ID:     ev.Part.CallID,
					Output: ev.Part.State.Output,
					Error:  ev.Part.State.Error,
				}
				if result.Error == "" && ev.Part.State.Status == "error" {
					result.Error = result.Output
				}
				if err := emit(harnesses.EventTypeToolResult, result); err != nil {
					return agg, err
				}
			}
		case "step_finish":
			if ev.Part.Tokens != nil {
				agg.recordUsage(ev.Part.Tokens)
			}
			agg.recordReportedCost(ev.Part.Cost)
		case "error":
			if msg, ok := opencodeErrorMessage(line); ok {
				return agg, errors.New("opencode error: " + msg)
			}
			return agg, errors.New("opencode error: unknown")
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

func (a *streamAggregate) recordUsage(tokens *opencodeTokens) {
	if a == nil || tokens == nil {
		return
	}
	cacheTokens := tokens.Cache.Read + tokens.Cache.Write
	a.UsageSources = append(a.UsageSources, harnesses.UsageCandidate{
		Source: harnesses.UsageSourceNativeStream,
		Fresh:  harnesses.BoolPtr(true),
		Counts: harnesses.UsageTokenCounts{
			InputTokens:      harnesses.IntPtr(tokens.Input),
			OutputTokens:     harnesses.IntPtr(tokens.Output),
			CacheReadTokens:  harnesses.IntPtr(tokens.Cache.Read),
			CacheWriteTokens: harnesses.IntPtr(tokens.Cache.Write),
			CacheTokens:      harnesses.IntPtr(cacheTokens),
			ReasoningTokens:  harnesses.IntPtr(tokens.Reasoning),
			TotalTokens:      harnesses.IntPtr(tokens.Total),
		},
	})
}

func opencodeErrorMessage(output string) (string, bool) {
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Name string `json:"name"`
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		return "", false
	}
	if envelope.Type != "error" {
		return "", false
	}
	switch {
	case envelope.Error.Data.Message != "":
		return envelope.Error.Data.Message, true
	case envelope.Error.Name != "":
		return envelope.Error.Name, true
	default:
		return "unknown error", true
	}
}
