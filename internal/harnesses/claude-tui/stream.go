package claudetui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// TranscriptLine represents a single JSONL line from a Claude Code transcript.
// The structure is minimal to allow forward compatibility with evolving Claude formats.
type TranscriptLine struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	Text  string          `json:"text,omitempty"`
	// For tool_result entries
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// For final events
	Status string          `json:"status,omitempty"`
	Usage  json.RawMessage `json:"usage,omitempty"`
	// Extra fields preserved as-is
	Extra map[string]interface{} `json:"-"`
}

// UnmarshalJSON allows TranscriptLine to capture unknown fields.
func (tl *TranscriptLine) UnmarshalJSON(data []byte) error {
	type rawLine struct {
		Type       string          `json:"type"`
		ID         string          `json:"id,omitempty"`
		Name       string          `json:"name,omitempty"`
		Input      json.RawMessage `json:"input,omitempty"`
		Text       string          `json:"text,omitempty"`
		Output     string          `json:"output,omitempty"`
		Error      string          `json:"error,omitempty"`
		DurationMS int64           `json:"duration_ms,omitempty"`
		Status     string          `json:"status,omitempty"`
		Usage      json.RawMessage `json:"usage,omitempty"`
	}
	var raw rawLine
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*tl = TranscriptLine{
		Type:       raw.Type,
		ID:         raw.ID,
		Name:       raw.Name,
		Input:      raw.Input,
		Text:       raw.Text,
		Output:     raw.Output,
		Error:      raw.Error,
		DurationMS: raw.DurationMS,
		Status:     raw.Status,
		Usage:      raw.Usage,
	}
	// Capture extra fields
	var extras map[string]interface{}
	_ = json.Unmarshal(data, &extras)
	tl.Extra = extras
	return nil
}

// ParseTranscriptLine parses a single JSONL line into a TranscriptLine.
// Returns an error if the line fails to parse; the error is logged and skipped during streaming.
func ParseTranscriptLine(line string) (TranscriptLine, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return TranscriptLine{}, fmt.Errorf("empty line")
	}
	var tl TranscriptLine
	if err := json.Unmarshal([]byte(line), &tl); err != nil {
		return TranscriptLine{}, fmt.Errorf("invalid JSONL: %w", err)
	}
	return tl, nil
}

// TranscriptTailer reads from a Claude Code transcript file,
// emitting canonical Events as new lines arrive.
type TranscriptTailer struct {
	path           string
	logger         *slog.Logger
	lastSeenOffset int64  // For multi-turn replay safety
	session        string // Session identifier for tracking
	seqCounter     int64
	startTime      time.Time
	partialBuf     bytes.Buffer
	readCloser     io.ReadCloser
	scanner        *bufio.Scanner
	retryBackoff   time.Duration
	maxRetries     int
}

// NewTranscriptTailer creates a new tailer for the given transcript path.
// The session parameter is used for tracking multi-turn state.
func NewTranscriptTailer(path, session string, logger *slog.Logger) *TranscriptTailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &TranscriptTailer{
		path:         path,
		logger:       logger,
		session:      session,
		retryBackoff: 50 * time.Millisecond,
		maxRetries:   20,
		startTime:    time.Now(),
	}
}

// Open opens the transcript file for reading.
func (t *TranscriptTailer) Open(ctx context.Context) error {
	f, err := os.Open(t.path)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	t.readCloser = f
	t.scanner = bufio.NewScanner(f)
	// Increase buffer size for large JSONL lines
	buf := make([]byte, 0, 64*1024)
	t.scanner.Buffer(buf, 1024*1024)
	return nil
}

// Close closes the transcript file.
func (t *TranscriptTailer) Close() error {
	if t.readCloser != nil {
		return t.readCloser.Close()
	}
	return nil
}

// ReadEvents reads all available events from the transcript, emitting them on the channel.
// Returns when the file reaches EOF or context is cancelled.
func (t *TranscriptTailer) ReadEvents(ctx context.Context, eventChan chan<- harnesses.Event) error {
	// Ensure file is open
	if t.scanner == nil {
		if err := t.Open(ctx); err != nil {
			return fmt.Errorf("open transcript: %w", err)
		}
	}
	defer t.Close()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Scan next line with retry logic for partial writes
		var line string
		if !t.scanner.Scan() {
			if t.scanner.Err() != nil {
				return fmt.Errorf("scanner error: %w", t.scanner.Err())
			}
			// EOF reached
			return nil
		}

		line = t.scanner.Text()
		if line == "" {
			continue
		}

		// Parse the JSONL line
		tl, err := ParseTranscriptLine(line)
		if err != nil {
			t.logger.Warn("malformed JSONL line, skipping", "error", err, "line", line[:min(len(line), 100)])
			continue
		}

		// Emit events based on the transcript line type
		events := t.transcriptLineToEvents(tl)
		for _, ev := range events {
			select {
			case eventChan <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// transcriptLineToEvents converts a transcript line to one or more canonical events.
func (t *TranscriptTailer) transcriptLineToEvents(tl TranscriptLine) []harnesses.Event {
	t.seqCounter++
	now := time.Now()

	switch tl.Type {
	case "text_delta":
		if tl.Text == "" {
			return nil
		}
		data, _ := json.Marshal(harnesses.TextDeltaData{Text: tl.Text})
		return []harnesses.Event{{
			Type:     harnesses.EventTypeTextDelta,
			Sequence: t.seqCounter,
			Time:     now,
			Data:     data,
		}}

	case "tool_call":
		if tl.ID == "" || tl.Name == "" {
			t.logger.Warn("tool_call missing required fields", "id", tl.ID, "name", tl.Name)
			return nil
		}
		data, _ := json.Marshal(harnesses.ToolCallData{
			ID:    tl.ID,
			Name:  tl.Name,
			Input: tl.Input,
		})
		return []harnesses.Event{{
			Type:     harnesses.EventTypeToolCall,
			Sequence: t.seqCounter,
			Time:     now,
			Data:     data,
		}}

	case "tool_result":
		if tl.ID == "" {
			t.logger.Warn("tool_result missing ID field")
			return nil
		}
		data, _ := json.Marshal(harnesses.ToolResultData{
			ID:         tl.ID,
			Output:     tl.Output,
			Error:      tl.Error,
			DurationMS: tl.DurationMS,
		})
		return []harnesses.Event{{
			Type:     harnesses.EventTypeToolResult,
			Sequence: t.seqCounter,
			Time:     now,
			Data:     data,
		}}

	case "final":
		fd := t.transcriptFinalToFinalData(tl)
		data, _ := json.Marshal(fd)
		return []harnesses.Event{{
			Type:     harnesses.EventTypeFinal,
			Sequence: t.seqCounter,
			Time:     now,
			Data:     data,
		}}

	default:
		// Unknown types are silently skipped
		return nil
	}
}

// transcriptFinalToFinalData converts a transcript final line to FinalData.
func (t *TranscriptTailer) transcriptFinalToFinalData(tl TranscriptLine) harnesses.FinalData {
	fd := harnesses.FinalData{
		Status:     tl.Status,
		FinalText:  tl.Text,
		DurationMS: time.Since(t.startTime).Milliseconds(),
	}

	// Parse usage if present
	if tl.Usage != nil {
		var usage harnesses.FinalUsage
		if err := json.Unmarshal(tl.Usage, &usage); err != nil {
			t.logger.Warn("failed to parse usage from final event", "error", err)
		} else {
			fd.Usage = &usage
		}
	}

	return fd
}

// ReadHookPayload extracts the transcript path from a Stop-hook payload.
// The payload is expected to be JSON with a transcript_path field.
func ReadHookPayload(data []byte) (string, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("invalid hook payload JSON: %w", err)
	}

	transcriptPath, ok := payload["transcript_path"].(string)
	if !ok {
		return "", fmt.Errorf("transcript_path not found in hook payload")
	}
	if transcriptPath == "" {
		return "", fmt.Errorf("transcript_path is empty")
	}

	return transcriptPath, nil
}

// ExpandTranscriptPath resolves ~ to home directory if needed.
func ExpandTranscriptPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return home, nil
	}
	return path, nil
}

// SessionMarker tracks the last-processed position for multi-turn replay safety.
// When /clear is issued, a new transcript file is created; this marker helps
// distinguish new turns from previously-processed turns.
type SessionMarker struct {
	lastTranscriptPath string
	lastOffset         int64
}

// MarkProcessed records that we've processed up to the given line count
// from the transcript at transcriptPath. This enables multi-turn safety.
func (m *SessionMarker) MarkProcessed(transcriptPath string, lineCount int64) {
	m.lastTranscriptPath = transcriptPath
	m.lastOffset = lineCount
}

// IsNewTranscript returns true if the transcript path differs from the last-processed one.
func (m *SessionMarker) IsNewTranscript(transcriptPath string) bool {
	return m.lastTranscriptPath != transcriptPath
}

// helper
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
