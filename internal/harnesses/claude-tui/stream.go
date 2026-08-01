package claudetui

import (
	"bufio"
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

// Real Claude Code 2.1.x transcript .jsonl schema.
//
// Top-level lines are objects with a "type" discriminator in
// {assistant, user, attachment, ai-title, queue-operation, last-prompt}.
// Only assistant and user lines carry model content we translate to events.
//
//	assistant line: {"type":"assistant","message":{"model","id","role",
//	                 "content":[block,...],"stop_reason","usage"}}
//	user line:      {"type":"user","message":{"role","content":[block,...]}}
//
// Each content block has its own "type" in {thinking, text, tool_use,
// tool_result}. tool_result blocks appear inside user lines' content.
//
// We translate:
//   - text block (assistant)   -> text_delta event
//   - tool_use block           -> tool_call event
//   - tool_result block (user) -> tool_result event
//   - last assistant stop_reason + usage -> final event

// transcriptLine is one decoded top-level JSONL line.
type transcriptLine struct {
	Type    string             `json:"type"`
	Message *transcriptMessage `json:"message,omitempty"`
}

// transcriptMessage is the message envelope on assistant/user lines.
type transcriptMessage struct {
	Model      string          `json:"model,omitempty"`
	ID         string          `json:"id,omitempty"`
	Role       string          `json:"role,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Usage      json.RawMessage `json:"usage,omitempty"`
	Content    messageContent  `json:"content,omitempty"`
}

// messageContent is a message's content, which Claude Code emits as EITHER an
// array of content blocks (assistant turns, tool_use/tool_result) OR a plain
// string (simple text messages). Decoding only as []transcriptBlock dropped the
// string form (observed live: "cannot unmarshal string into []transcriptBlock"
// → the line was skipped, losing its text). Accept both: a string becomes a
// single text block.
type messageContent []transcriptBlock

func (c *messageContent) UnmarshalJSON(data []byte) error {
	var blocks []transcriptBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		*c = blocks
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*c = messageContent{{Type: "text", Text: s}}
	return nil
}

// transcriptBlock is one content block inside a message.
type transcriptBlock struct {
	Type string `json:"type"`
	// text block
	Text string `json:"text,omitempty"`
	// tool_use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result block
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// claudeUsage is the assistant-line usage object.
type claudeUsage struct {
	InputTokens              *int `json:"input_tokens,omitempty"`
	OutputTokens             *int `json:"output_tokens,omitempty"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

// ParseTranscriptLine parses a single JSONL line into a transcriptLine.
// Returns an error if the line is empty or fails to parse; callers skip
// malformed lines during streaming.
func ParseTranscriptLine(line string) (transcriptLine, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return transcriptLine{}, fmt.Errorf("empty line")
	}
	var tl transcriptLine
	if err := json.Unmarshal([]byte(line), &tl); err != nil {
		return transcriptLine{}, fmt.Errorf("invalid JSONL: %w", err)
	}
	return tl, nil
}

// TranscriptTailer reads from a Claude Code transcript file, emitting
// canonical Events derived from assistant/user message content blocks.
type TranscriptTailer struct {
	path       string
	logger     *slog.Logger
	session    string
	seqCounter int64
	startTime  time.Time
	readCloser io.ReadCloser
	scanner    *bufio.Scanner

	// startOffset is the byte offset to resume reading from when one invocation
	// incrementally reopens an append-only transcript.
	startOffset int64
	// endOffset is the byte offset just past the last line consumed by the most
	// recent ReadEvents call.
	endOffset int64

	// lastAssistant captures the most recent assistant message so the
	// final event reflects its stop_reason + usage + accumulated text.
	lastAssistantStop  string
	lastAssistantUsage json.RawMessage
	finalText          strings.Builder
	sawAssistant       bool
	emittedFinal       bool
}

// NewTranscriptTailer creates a new tailer for the given transcript path.
func NewTranscriptTailer(path, session string, logger *slog.Logger) *TranscriptTailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &TranscriptTailer{
		path:      path,
		logger:    logger,
		session:   session,
		startTime: time.Now(),
	}
}

// SetStartOffset sets the byte offset ReadEvents resumes from. It is an
// incremental-read seam within one invocation; it does not retain a live PTY.
// Offsets past EOF or <= 0 read from the start.
func (t *TranscriptTailer) SetStartOffset(off int64) {
	if off < 0 {
		off = 0
	}
	t.startOffset = off
	t.endOffset = off
}

// EndOffset returns the byte offset just past the last line consumed. After a
// ReadEvents call it is the position the next turn should resume from.
func (t *TranscriptTailer) EndOffset() int64 { return t.endOffset }

// Open opens the transcript file for reading, seeking to startOffset.
func (t *TranscriptTailer) Open(ctx context.Context) error {
	f, err := os.Open(t.path)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	if t.startOffset > 0 {
		// Clamp to file size so a stale/over-large offset reads nothing rather
		// than erroring; the next turn re-syncs from the true end.
		if info, statErr := f.Stat(); statErr == nil && t.startOffset > info.Size() {
			t.startOffset = info.Size()
		}
		if _, seekErr := f.Seek(t.startOffset, io.SeekStart); seekErr != nil {
			_ = f.Close()
			return fmt.Errorf("seek transcript to offset %d: %w", t.startOffset, seekErr)
		}
		t.endOffset = t.startOffset
	}
	t.readCloser = f
	t.scanner = bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	t.scanner.Buffer(buf, 4*1024*1024)
	return nil
}

// Close closes the transcript file.
func (t *TranscriptTailer) Close() error {
	if t.readCloser != nil {
		return t.readCloser.Close()
	}
	return nil
}

// ReadEvents reads all available events from the transcript, emitting them
// on the channel. It walks assistant/user message content blocks into
// text_delta/tool_call/tool_result events and synthesizes a single final
// event from the last assistant stop_reason + usage when the stream ends.
func (t *TranscriptTailer) ReadEvents(ctx context.Context, eventChan chan<- harnesses.Event) error {
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

		if !t.scanner.Scan() {
			if err := t.scanner.Err(); err != nil {
				return fmt.Errorf("scanner error: %w", err)
			}
			break // EOF
		}

		line := t.scanner.Text()
		// Advance the incremental resume offset past this line (+1 for the
		// stripped newline).
		t.endOffset += int64(len(line)) + 1
		if strings.TrimSpace(line) == "" {
			continue
		}

		tl, err := ParseTranscriptLine(line)
		if err != nil {
			t.logger.Warn("malformed JSONL line, skipping", "error", err)
			continue
		}

		events := t.lineToEvents(tl)
		for _, ev := range events {
			select {
			case eventChan <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	// Synthesize the final event from the last assistant turn.
	if fin, ok := t.finalEvent(); ok {
		select {
		case eventChan <- fin:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// lineToEvents walks a single top-level line's content blocks into events.
// It does NOT emit the final event; finalEvent does that after EOF so that
// exactly one final is produced per transcript read.
func (t *TranscriptTailer) lineToEvents(tl transcriptLine) []harnesses.Event {
	if tl.Message == nil {
		return nil
	}

	var out []harnesses.Event
	now := time.Now()

	switch tl.Type {
	case "assistant":
		t.sawAssistant = true
		t.lastAssistantStop = tl.Message.StopReason
		if len(tl.Message.Usage) > 0 {
			t.lastAssistantUsage = tl.Message.Usage
		}
		for _, b := range tl.Message.Content {
			switch b.Type {
			case "text":
				if b.Text == "" {
					continue
				}
				t.finalText.WriteString(b.Text)
				t.seqCounter++
				data, _ := json.Marshal(harnesses.TextDeltaData{Text: b.Text})
				out = append(out, harnesses.Event{
					Type:     harnesses.EventTypeTextDelta,
					Sequence: t.seqCounter,
					Time:     now,
					Data:     data,
				})
			case "tool_use":
				if b.ID == "" || b.Name == "" {
					t.logger.Warn("tool_use block missing id/name", "id", b.ID, "name", b.Name)
					continue
				}
				t.seqCounter++
				data, _ := json.Marshal(harnesses.ToolCallData{
					ID:    b.ID,
					Name:  b.Name,
					Input: b.Input,
				})
				out = append(out, harnesses.Event{
					Type:     harnesses.EventTypeToolCall,
					Sequence: t.seqCounter,
					Time:     now,
					Data:     data,
				})
			}
		}
	case "user":
		for _, b := range tl.Message.Content {
			if b.Type != "tool_result" {
				continue
			}
			if b.ToolUseID == "" {
				t.logger.Warn("tool_result block missing tool_use_id")
				continue
			}
			t.seqCounter++
			out = append(out, harnesses.Event{
				Type:     harnesses.EventTypeToolResult,
				Sequence: t.seqCounter,
				Time:     now,
				Data: mustMarshal(harnesses.ToolResultData{
					ID:     b.ToolUseID,
					Output: toolResultText(b.Content),
					Error:  toolResultError(b.IsError, b.Content),
				}),
			})
		}
	}

	return out
}

// finalEvent synthesizes the single final event from the last assistant
// turn's stop_reason + usage + accumulated text. Returns false unless the
// transcript ends with an authoritative terminal stop reason.
func (t *TranscriptTailer) finalEvent() (harnesses.Event, bool) {
	if !t.sawAssistant {
		return harnesses.Event{}, false
	}
	status, authoritative := mapStopReason(t.lastAssistantStop)
	if !authoritative {
		return harnesses.Event{}, false
	}
	t.seqCounter++
	fd := harnesses.FinalData{
		Status:          status,
		FinalText:       t.finalText.String(),
		DurationMS:      time.Since(t.startTime).Milliseconds(),
		FinalCostSource: harnesses.CostSourceUnknown,
	}
	if u := parseClaudeUsage(t.lastAssistantUsage); u != nil {
		fd.Usage = u
	}
	t.emittedFinal = true
	return harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: t.seqCounter,
		Time:     time.Now(),
		Data:     mustMarshal(fd),
	}, true
}

// mapStopReason maps authoritative Claude terminal stop reasons to
// CONTRACT-003 final statuses. Intermediate, missing, and unknown reasons fail
// closed so a schema change cannot fabricate successful completion evidence.
func mapStopReason(stop string) (string, bool) {
	switch stop {
	case "end_turn", "stop_sequence":
		return "success", true
	case "max_tokens":
		return "iteration_limit", true
	default:
		return "", false
	}
}

// parseClaudeUsage converts a raw assistant-line usage object to FinalUsage.
func parseClaudeUsage(raw json.RawMessage) *harnesses.FinalUsage {
	if len(raw) == 0 {
		return nil
	}
	var u claudeUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	if u.InputTokens == nil && u.OutputTokens == nil &&
		u.CacheReadInputTokens == nil && u.CacheCreationInputTokens == nil {
		return nil
	}
	out := &harnesses.FinalUsage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		Source:           "transcript",
	}
	return out
}

// toolResultText extracts a textual representation from a tool_result block's
// content, which may be a JSON string or an array of {type,text} blocks.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	return string(raw)
}

// toolResultError returns the error text when a tool_result is flagged as an
// error, otherwise the empty string.
func toolResultError(isErr bool, raw json.RawMessage) string {
	if !isErr {
		return ""
	}
	return toolResultText(raw)
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ReadHookPayload extracts the transcript path from a Stop-hook payload.
// The payload is JSON with a transcript_path field.
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

// transcriptFinalizationGrace is how long emitTranscriptAndFinal will re-open
// an append-only Claude Code transcript waiting for a terminal stop_reason.
// Claude Code 2.1.x publishes the Stop hook as soon as the turn ends, but the
// final assistant line with stop_reason=end_turn can land a few hundred
// milliseconds later (observed live: Stop fires with only tool_use on disk,
// then end_turn is appended before stop_hook_summary). Without this grace the
// harness fails closed with "no assistant final event" even though the model
// completed the work.
//
// Tests may shrink this var so fail-closed incomplete cases stay fast.
var transcriptFinalizationGrace = 3 * time.Second

// lastAssistantStopReason scans a Claude Code transcript JSONL file and returns
// the stop_reason of the last assistant message. missing is true when no
// assistant line was found (or the file cannot be read yet).
func lastAssistantStopReason(path string) (stop string, sawAssistant bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)
	for scanner.Scan() {
		tl, parseErr := ParseTranscriptLine(scanner.Text())
		if parseErr != nil || tl.Type != "assistant" || tl.Message == nil {
			continue
		}
		sawAssistant = true
		stop = tl.Message.StopReason
	}
	if err := scanner.Err(); err != nil {
		return stop, sawAssistant, err
	}
	return stop, sawAssistant, nil
}

// transcriptHasAuthoritativeStop reports whether path currently ends with an
// assistant stop_reason that mapStopReason treats as terminal (end_turn,
// stop_sequence, max_tokens).
func transcriptHasAuthoritativeStop(path string) bool {
	stop, saw, err := lastAssistantStopReason(path)
	if err != nil || !saw {
		return false
	}
	_, ok := mapStopReason(stop)
	return ok
}

// waitForAuthoritativeTranscript polls path until it has a terminal assistant
// stop_reason, the context is cancelled, or grace elapses. It is a flush-race
// seam only: it never invents success when the transcript stays intermediate.
func waitForAuthoritativeTranscript(ctx context.Context, path string, grace time.Duration) error {
	if grace <= 0 {
		grace = transcriptFinalizationGrace
	}
	if transcriptHasAuthoritativeStop(path) {
		return nil
	}
	deadline := time.Now().Add(grace)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if transcriptHasAuthoritativeStop(path) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("transcript still non-terminal after %s", grace)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ---------------------------------------------------------------------------
// Hook-event tailer: mid-turn tool_call / tool_result ProgressEvents.
//
// PreToolUse / PostToolUse hooks each write a per-event JSON payload file into
// a hook directory during the turn. The tailer scans that directory for new
// payload files and emits tool_call (PreToolUse) and tool_result (PostToolUse)
// events as they appear, so ddx's idle watchdog observes progress during a
// long turn instead of killing it.
// ---------------------------------------------------------------------------

// HookEvent is one decoded PreToolUse/PostToolUse payload file.
type HookEvent struct {
	Event      string          `json:"hook_event_name"`
	ToolName   string          `json:"tool_name"`
	ToolUseID  string          `json:"tool_use_id"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput json.RawMessage `json:"tool_response,omitempty"`
}

// ParseHookEvent decodes a single PreToolUse/PostToolUse payload file.
func ParseHookEvent(data []byte) (HookEvent, error) {
	var he HookEvent
	if err := json.Unmarshal(data, &he); err != nil {
		return HookEvent{}, fmt.Errorf("invalid hook-event JSON: %w", err)
	}
	return he, nil
}

// HookEventToEvent converts a decoded hook event to a canonical Event, or
// returns ok=false for events that do not map (e.g. unknown hook names).
//
// corrID is the correlation ID to stamp on the event when the real Claude Code
// PreToolUse/PostToolUse payload omits a top-level tool_use_id (which the
// documented 2.1.x schema does — it carries session_id/tool_name/tool_input/
// tool_response but no tool_use_id). The caller derives corrID from emission
// order so a PreToolUse tool_call and its PostToolUse tool_result share an ID.
// When the payload DOES carry a tool_use_id it takes precedence.
func HookEventToEvent(he HookEvent, seq int64, corrID string) (harnesses.Event, bool) {
	now := time.Now()
	id := he.ToolUseID
	if id == "" {
		id = corrID
	}
	switch he.Event {
	case "PreToolUse":
		if he.ToolName == "" {
			return harnesses.Event{}, false
		}
		return harnesses.Event{
			Type:     harnesses.EventTypeToolCall,
			Sequence: seq,
			Time:     now,
			Data: mustMarshal(harnesses.ToolCallData{
				ID:    id,
				Name:  he.ToolName,
				Input: he.ToolInput,
			}),
		}, true
	case "PostToolUse":
		return harnesses.Event{
			Type:     harnesses.EventTypeToolResult,
			Sequence: seq,
			Time:     now,
			Data: mustMarshal(harnesses.ToolResultData{
				ID:     id,
				Output: toolResultText(he.ToolOutput),
				Error:  postToolUseError(he),
			}),
		}, true
	default:
		return harnesses.Event{}, false
	}
}

// postToolUseError surfaces a tool failure from a PostToolUse payload. Claude
// Code marks a failed tool with tool_response.is_error / an error string; we
// best-effort decode that so a downstream consumer sees the failure.
func postToolUseError(he HookEvent) string {
	if len(he.ToolOutput) == 0 {
		return ""
	}
	var resp struct {
		IsError bool   `json:"is_error"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(he.ToolOutput, &resp); err != nil {
		return ""
	}
	if resp.Error != "" {
		return resp.Error
	}
	if resp.IsError {
		return toolResultText(he.ToolOutput)
	}
	return ""
}

// HookEventTailer watches a hook directory for new PreToolUse/PostToolUse
// payload files and emits tool_call/tool_result events during the turn.
type HookEventTailer struct {
	dir    string
	logger *slog.Logger
	seen   map[string]bool
	// preCount/postCount index PreToolUse/PostToolUse events by emission order
	// so the Nth tool_call and the Nth tool_result share a synthetic
	// correlation ID when the real payload omits tool_use_id (see
	// HookEventToEvent). They are only consulted when a payload lacks a real id.
	preCount  int
	postCount int
}

// NewHookEventTailer creates a tailer over the given hook directory.
func NewHookEventTailer(dir string, logger *slog.Logger) *HookEventTailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &HookEventTailer{dir: dir, logger: logger, seen: make(map[string]bool)}
}

// correlationID returns the synthetic correlation ID for a hook event based on
// emission order: the Nth PreToolUse and the Nth PostToolUse both map to
// "tool-N" so a consumer can pair a mid-turn call with its result even though
// the real Claude Code 2.1.x payload carries no top-level tool_use_id.
func (h *HookEventTailer) correlationID(event string) string {
	switch event {
	case "PreToolUse":
		h.preCount++
		return fmt.Sprintf("tool-%d", h.preCount)
	case "PostToolUse":
		h.postCount++
		return fmt.Sprintf("tool-%d", h.postCount)
	default:
		return ""
	}
}

// Drain scans the hook directory once for new tool-event payload files
// (prefixed "tool-") and emits the corresponding events on seq. Returns the
// updated sequence counter. Files already processed are not re-emitted.
func (h *HookEventTailer) Drain(seq int64, emit func(harnesses.Event)) int64 {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return seq
	}
	// Sort by name so PreToolUse/PostToolUse files emit in writual order
	// (filenames embed a monotonically increasing counter).
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "tool-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		if h.seen[name] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.dir, name))
		if err != nil {
			continue
		}
		he, err := ParseHookEvent(data)
		if err != nil {
			if isLikelyPartialHookPayload(data, err) {
				// The Claude hook command writes with `cat > file`, so the
				// tailer can observe the file before the shell closes it.
				// Leave it unseen so the next drain can parse the completed
				// payload instead of permanently dropping the event.
				continue
			}
			h.seen[name] = true
			h.logger.Warn("malformed hook-event payload, skipping", "file", name, "error", err)
			continue
		}
		h.seen[name] = true
		corr := ""
		if he.ToolUseID == "" {
			corr = h.correlationID(he.Event)
		}
		if ev, ok := HookEventToEvent(he, seq+1, corr); ok {
			seq++
			emit(ev)
		}
	}
	return seq
}

func isLikelyPartialHookPayload(data []byte, err error) bool {
	if len(strings.TrimSpace(string(data))) == 0 {
		return true
	}
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "unexpected end of JSON input")
}

// sortStrings is a small insertion sort to avoid importing sort for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
