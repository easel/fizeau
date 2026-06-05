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
		Type   string          `json:"type"`
		Text   string          `json:"text"`
		Tokens *opencodeTokens `json:"tokens"`
		Cost   float64         `json:"cost"`
	} `json:"part"`
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// streamAggregate captures running totals from the opencode JSONL stream.
// HasUsage is set when a step_finish event carries a tokens object — token
// counts then reflect verbatim upstream values, including explicit zeros.
type streamAggregate struct {
	FinalText        string
	HasUsage         bool
	InputTokens      int
	OutputTokens     int
	ReasoningTokens  int
	CacheReadTokens  int
	CacheWriteTokens int
	CostUSD          float64
}

// parseOpencodeStream reads opencode --format json newline-delimited JSON
// events from r and emits harness Events on out. Mapping:
//
//   - type==text, part.type==text  -> EventTypeTextDelta (accumulated into agg.FinalText)
//   - type==step_finish             -> aggregate tokens and cost
//   - type==error                   -> return error
//   - all other types               -> skipped
func parseOpencodeStream(ctx context.Context, r io.Reader, out chan<- harnesses.Event, metadata map[string]string, seq *int64) (*streamAggregate, error) {
	agg := &streamAggregate{}

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
		case "step_finish":
			if ev.Part.Tokens != nil {
				agg.HasUsage = true
				agg.InputTokens = ev.Part.Tokens.Input
				agg.OutputTokens = ev.Part.Tokens.Output
				agg.ReasoningTokens = ev.Part.Tokens.Reasoning
				agg.CacheReadTokens = ev.Part.Tokens.Cache.Read
				agg.CacheWriteTokens = ev.Part.Tokens.Cache.Write
			}
			if ev.Part.Cost > 0 {
				agg.CostUSD = ev.Part.Cost
			}
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
