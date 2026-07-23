package grok

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

// grokEvent is a minimal, lenient view of a grok CLI streaming-json event.
// The CLI emits one self-contained JSON object per stdout line with a
// discriminating "type" field.
//
// Observed event types (grok 0.2.106):
//
//	{"type":"thought","data":"..."}   — reasoning token delta
//	{"type":"text","data":"..."}      — assistant text token delta
//	{"type":"end","stopReason":"EndTurn","sessionId":"...","requestId":"...",
//	 "usage":{"input_tokens":N,"cache_read_input_tokens":N,"output_tokens":N,
//	          "reasoning_tokens":N,"total_tokens":N},
//	 "num_turns":N,"total_cost_usd":F,"modelUsage":{...}}  — terminal event (always last)
//	{"type":"error","message":"..."}  — error, may carry spend fields
//
// The CLI documents the list as non-exhaustive (e.g. max_turns_reached,
// auto_compact_*); unknown types are skipped.
type grokEvent struct {
	Type       string          `json:"type"`
	Data       string          `json:"data"`
	Message    string          `json:"message"`
	StopReason string          `json:"stopReason"`
	SessionID  string          `json:"sessionId"`
	RequestID  string          `json:"requestId"`
	Usage      json.RawMessage `json:"usage"`
	NumTurns   int             `json:"num_turns"`
	CostUSD    *float64        `json:"total_cost_usd"`

	// ddx.usage_source replay fields
	Source     string `json:"source"`
	Fresh      *bool  `json:"fresh"`
	CapturedAt string `json:"captured_at"`
}

// streamAggregate captures running totals from the grok stream so the runner
// can attach final-event usage/cost without re-parsing.
type streamAggregate struct {
	finalText    strings.Builder
	SessionID    string
	StopReason   string
	TurnCount    int
	UsageSources []harnesses.UsageCandidate
	FinalCostUSD *float64
	CostSource   harnesses.CostSource
	CostUSD      float64
	IsError      bool
	ErrorMessage string
}

// FinalText returns the accumulated assistant text. Grok streams token-level
// deltas rather than whole blocks, so the aggregate concatenates every text
// delta seen during the run.
func (a *streamAggregate) FinalText() string {
	return a.finalText.String()
}

// parseGrokStream reads newline-delimited grok streaming-json events from r
// and emits harness Events on out:
//
//   - grok text    -> EventTypeTextDelta (token-level delta, accumulated)
//   - grok thought -> (no event; reasoning deltas are not surfaced)
//   - grok end     -> (no event; aggregate populated with usage/cost/session)
//   - grok error   -> (no event; aggregate marked failed with message)
//   - other types  -> skipped (the CLI documents the set as non-exhaustive)
//
// metadata is copied onto every emitted Event. seq is incremented per event.
// out is NOT closed; the caller owns the channel and closes it after the
// synthetic final event. ctx cancellation returns the running aggregate.
func parseGrokStream(ctx context.Context, r io.Reader, out chan<- harnesses.Event, metadata map[string]string, seq *int64) (*streamAggregate, error) {
	agg := &streamAggregate{CostSource: harnesses.CostSourceUnknown}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)

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
		select {
		case <-ctx.Done():
			return agg, ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev grokEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Non-JSON line — skip silently so partial/corrupted lines
			// don't kill the run.
			continue
		}

		switch ev.Type {
		case "text":
			if ev.Data == "" {
				continue
			}
			if err := emit(harnesses.EventTypeTextDelta, harnesses.TextDeltaData{Text: ev.Data}); err != nil {
				return agg, err
			}
			agg.finalText.WriteString(ev.Data)
		case "thought":
			// Reasoning deltas are not mapped to harness events (matching
			// the claude harness, which skips thinking blocks).
		case "end":
			agg.recordUsage(ev.Usage)
			agg.recordReportedCost(ev.CostUSD)
			if ev.SessionID != "" {
				agg.SessionID = ev.SessionID
			}
			if ev.StopReason != "" {
				agg.StopReason = ev.StopReason
			}
			if ev.NumTurns > 0 {
				agg.TurnCount = ev.NumTurns
			}
		case "error":
			agg.IsError = true
			if ev.Message != "" {
				agg.ErrorMessage = ev.Message
			}
			agg.recordUsage(ev.Usage)
			agg.recordReportedCost(ev.CostUSD)
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
	if cost == nil || *cost < 0 {
		return
	}
	value := *cost
	a.FinalCostUSD = &value
	a.CostSource = harnesses.CostSourceReported
	a.CostUSD = value
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
