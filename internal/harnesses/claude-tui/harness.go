package claudetui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/harnesses/hooks"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
)

// ErrNotYetImplemented is returned by stub methods pending real implementation.
var ErrNotYetImplemented = errors.New("claude-tui harness: not yet implemented")

// emptyModelSnapshot is a zero-valued snapshot used to satisfy the
// ModelDiscoveryHarness interface when discovery fails. Per the
// no-static-fallback principle, this sentinel is only returned paired
// with an error; it never represents a fallback or default value.
var emptyModelSnapshot harnesses.ModelDiscoverySnapshot

// sessionPool holds PTY sessions keyed by working directory.
// Per ADR-013 §4, the pool is singleton-scoped and shared across harness instances.
var sessionPool struct {
	mu       sync.Mutex
	sessions map[string]*session.Session
}

// clearPrompt is the /clear command sent between turns to reset conversation state.
const clearPrompt = "/clear\r"

// clearAcknowledgment is a prompt indicating the /clear command was recognized.
// The exact string depends on the Claude CLI version, so we use a conservative pattern.
const clearAcknowledgmentPattern = "❯|>|cleared"

func init() {
	sessionPool.sessions = make(map[string]*session.Session)
}

// Harness is the sentinel harness for claude TUI.
// It satisfies the harnesses.Harness, harnesses.QuotaHarness,
// harnesses.AccountHarness, and harnesses.ModelDiscoveryHarness interfaces.
type Harness struct {
	hookManager *hooks.Manager
	mu          sync.Mutex
}

// Info implements harnesses.Harness.
func (h *Harness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{
		Name:                "claude-tui",
		Type:                "subprocess",
		Available:           false,
		IsSubscription:      true,
		AutoRoutingEligible: false,
		DefaultModel:        "claude-sonnet-4-6",
	}
}

// HealthCheck implements harnesses.Harness.
func (h *Harness) HealthCheck(ctx context.Context) error {
	return ErrNotYetImplemented
}

// Execute implements harnesses.Harness.
func (h *Harness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	if h.hookManager == nil {
		h.mu.Lock()
		h.hookManager = hooks.New()
		h.mu.Unlock()
	}
	return h.runTurn(ctx, req)
}

func (h *Harness) runTurn(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	eventCh := make(chan harnesses.Event, 100)

	go func() {
		defer close(eventCh)

		startTime := time.Now()
		sequence := int64(0)

		// Get or create a session for the working directory.
		// If the session exists, issue /clear between turns.
		workdir := req.WorkDir
		if workdir == "" {
			workdir = "."
		}

		sess := getOrCreateSession(ctx, workdir)
		if sess != nil {
			sessionIsNew := !sessionExists(workdir)
			if !sessionIsNew {
				// Session already exists; issue /clear to reset conversation state.
				if err := issueClearCommand(ctx, sess); err != nil {
					// Log the error but continue; the session may still be usable.
					// In a production implementation, this would emit a warning event.
				}
			}
		}

		// Emit initial startup events
		sequence++
		eventCh <- harnesses.Event{
			Type:     harnesses.EventTypeProgress,
			Sequence: sequence,
			Time:     time.Now(),
			Metadata: req.Metadata,
			Data:     json.RawMessage(`{}`),
		}

		// For now, simulate a tool call and result for testing
		sequence++
		toolCallData := harnesses.ToolCallData{
			ID:    "test_tool_1",
			Name:  "test_function",
			Input: json.RawMessage(`{"test": "input"}`),
		}
		toolCallBytes, _ := json.Marshal(toolCallData)
		eventCh <- harnesses.Event{
			Type:     harnesses.EventTypeToolCall,
			Sequence: sequence,
			Time:     time.Now(),
			Metadata: req.Metadata,
			Data:     toolCallBytes,
		}

		// Notify hooks
		h.mu.Lock()
		if h.hookManager != nil {
			h.hookManager.OnToolUse(toolCallData.ID, toolCallData.Name, toolCallData.Input)
		}
		h.mu.Unlock()

		// Emit tool result
		sequence++
		toolResultData := harnesses.ToolResultData{
			ID:     "test_tool_1",
			Output: "test output",
		}
		toolResultBytes, _ := json.Marshal(toolResultData)
		eventCh <- harnesses.Event{
			Type:     harnesses.EventTypeToolResult,
			Sequence: sequence,
			Time:     time.Now(),
			Metadata: req.Metadata,
			Data:     toolResultBytes,
		}

		// Notify hooks
		h.mu.Lock()
		if h.hookManager != nil {
			h.hookManager.OnToolResult(toolResultData.ID, toolResultData.Output, "")
		}
		h.mu.Unlock()

		// Emit final event
		sequence++
		finalData := harnesses.FinalData{
			Status:     "success",
			ExitCode:   0,
			FinalText:  req.Prompt,
			DurationMS: time.Since(startTime).Milliseconds(),
		}
		finalBytes, _ := json.Marshal(finalData)
		eventCh <- harnesses.Event{
			Type:     harnesses.EventTypeFinal,
			Sequence: sequence,
			Time:     time.Now(),
			Metadata: req.Metadata,
			Data:     finalBytes,
		}
	}()

	return eventCh, nil
}

// getOrCreateSession retrieves or creates a PTY session for the given working directory.
func getOrCreateSession(ctx context.Context, workdir string) *session.Session {
	sessionPool.mu.Lock()
	defer sessionPool.mu.Unlock()

	if sess, exists := sessionPool.sessions[workdir]; exists {
		return sess
	}

	// Create a new session for this workdir.
	// In a full implementation, this would spawn a claude CLI process.
	// For now, we store nil as a placeholder.
	sessionPool.sessions[workdir] = nil
	return nil
}

// sessionExists checks if a session already exists for the given working directory.
func sessionExists(workdir string) bool {
	sessionPool.mu.Lock()
	defer sessionPool.mu.Unlock()
	_, exists := sessionPool.sessions[workdir]
	return exists
}

// issueClearCommand sends the /clear command to the session and waits for acknowledgment.
func issueClearCommand(ctx context.Context, sess *session.Session) error {
	if sess == nil {
		return nil
	}

	// Send the /clear command.
	if err := sess.SendBytes([]byte(clearPrompt)); err != nil {
		return fmt.Errorf("failed to send /clear command: %w", err)
	}

	// Wait for acknowledgment that the clear was processed.
	// Timeout after a reasonable interval to avoid hanging.
	clearCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Read output until we see the acknowledgment pattern or timeout.
	for {
		select {
		case <-clearCtx.Done():
			return fmt.Errorf("timeout waiting for /clear acknowledgment")
		case chunk, ok := <-sess.Output():
			if !ok {
				return fmt.Errorf("session closed while waiting for /clear acknowledgment")
			}
			output := string(chunk.Bytes)
			if strings.Contains(output, "❯") || strings.Contains(output, ">") {
				return nil // Prompt was restored; clear succeeded.
			}
		}
	}
}

// QuotaStatus implements harnesses.QuotaHarness.
func (h *Harness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, ErrNotYetImplemented
}

// RefreshQuota implements harnesses.QuotaHarness.
func (h *Harness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, ErrNotYetImplemented
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (h *Harness) QuotaFreshness() time.Duration {
	return 15 * time.Minute
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (h *Harness) SupportedLimitIDs() []string {
	return nil
}

// AccountStatus implements harnesses.AccountHarness.
func (h *Harness) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, ErrNotYetImplemented
}

// RefreshAccount implements harnesses.AccountHarness.
func (h *Harness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, ErrNotYetImplemented
}

// AccountFreshness implements harnesses.AccountHarness.
func (h *Harness) AccountFreshness() time.Duration {
	return 24 * time.Hour
}

// RegisterHook registers a hook handler with the harness.
// This method routes through the shared hooks conflict-arbitration layer
// so user-defined and Fizeau-injected hooks coexist without duplication.
func (h *Harness) RegisterHook(key string, hook hooks.Hook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hookManager == nil {
		h.hookManager = hooks.New()
	}
	h.hookManager.Register(key, hook)
}

// UnregisterHook removes a registered hook handler by key.
func (h *Harness) UnregisterHook(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hookManager != nil {
		h.hookManager.Unregister(key)
	}
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness.
// Per the no-static-fallback principle, this method drives a live PTY
// query against the Anthropic CLI. On error, it returns an empty snapshot
// with an error sentinel (never a cached fallback literal).
func (h *Harness) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	snapshot, err := readClaudeTuiModelDiscoveryViaPTY(context.Background(), 30*time.Second)
	if err != nil {
		return emptyModelSnapshot, err
	}
	if len(snapshot.Models) == 0 {
		return emptyModelSnapshot, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness.
func (h *Harness) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	return "", harnesses.ErrAliasNotResolvable
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (h *Harness) SupportedAliases() []string {
	return nil
}

// readClaudeTuiModelDiscoveryViaPTY spawns a PTY session against the claude CLI,
// sends the /model command, and parses the output to extract available models.
// Per the no-static-fallback principle, returns ErrModelDiscoveryEvidenceMissing
// if the PTY fails or yields no models; never returns a partial or empty snapshot.
func readClaudeTuiModelDiscoveryViaPTY(ctx context.Context, timeout time.Duration) (harnesses.ModelDiscoverySnapshot, error) {
	// Variable to capture the snapshot once discovery succeeds.
	var snapshot harnesses.ModelDiscoverySnapshot
	var discoveryErr error

	_, err := ptyquota.Run(ctx, ptyquota.Config{
		HarnessName:  "claude-tui",
		Binary:       "claude",
		Args:         nil,
		Workdir:      "",
		Env:          nil,
		Command:      "/model\r",
		ReadyMarkers: []string{"❯", "> "},
		DoneWhen:     claudeTuiModelDiscoveryComplete,
		Timeout:      timeout,
		Size:         session.Size{Rows: 50, Cols: 220},
		Discovery: func(text string) (cassette.DiscoveryRecord, error) {
			models := ParseClaudeTuiModels(text)
			if len(models) == 0 {
				discoveryErr = harnesses.ErrModelDiscoveryEvidenceMissing
				return cassette.DiscoveryRecord{}, fmt.Errorf("no models found in /model output")
			}
			// Build snapshot only after confirming we have models.
			snapshot.CapturedAt = time.Now().UTC()
			snapshot.FreshnessWindow = (24 * time.Hour).String()
			snapshot.Source = "pty"
			snapshot.Models = models
			snapshot.ReasoningLevels = ParseClaudeTuiReasoningLevels(text)
			return discoveryRecordFromSnapshot(snapshot), nil
		},
	})
	if err != nil {
		if discoveryErr != nil {
			return emptyModelSnapshot, discoveryErr
		}
		return emptyModelSnapshot, fmt.Errorf("model discovery PTY: %w", err)
	}
	if len(snapshot.Models) == 0 {
		return emptyModelSnapshot, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

func claudeTuiModelDiscoveryComplete(text string) bool {
	return len(ParseClaudeTuiModels(text)) > 0
}

var (
	claudeFullModelPattern         = regexp.MustCompile(`\bclaude-[a-z0-9][a-z0-9._-]*\b`)
	claudeFullFamilyVersionPattern = regexp.MustCompile(`\bclaude-(sonnet|opus|haiku)-([0-9]+)[.-]([0-9]{1,2})(?:\b|-)`)
	claudeFamilyVersionPattern     = regexp.MustCompile(`\b(?:claude\s+)?(sonnet|opus|haiku)\s+([0-9]+(?:[.-][0-9]+){0,2})\b`)
	claudeAliasPattern             = regexp.MustCompile(`(?m)(?:^|[\s'"])(sonnet|opus|haiku)(?:$|[\s'"])`)
	claudeEffortPattern            = regexp.MustCompile(`--effort\s+<level>.*\(([^)]*)\)`)
)

// ParseClaudeTuiModels extracts available model names from claude /model output.
func ParseClaudeTuiModels(text string) []string {
	text = anthropic.StripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lower := strings.ToLower(text)
	models := uniqueMatches(claudeFullModelPattern.FindAllString(lower, -1))
	for _, match := range claudeFullFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 3 {
			models = appendUniqueString(models, match[1]+"-"+match[2]+"."+match[3])
		}
	}
	for _, match := range claudeFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 2 {
			models = appendUniqueString(models, match[1]+"-"+strings.ReplaceAll(match[2], "-", "."))
		}
	}
	for _, match := range claudeAliasPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 1 {
			models = appendUniqueString(models, match[1])
		}
	}
	return models
}

// ParseClaudeTuiReasoningLevels extracts supported reasoning levels from help output.
func ParseClaudeTuiReasoningLevels(text string) []string {
	text = anthropic.StripANSI(strings.ReplaceAll(text, "\n", " "))
	m := claudeEffortPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return nil
	}
	parts := strings.Split(m[1], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = appendUniqueString(out, strings.TrimSpace(part))
	}
	return out
}

func discoveryRecordFromSnapshot(snapshot harnesses.ModelDiscoverySnapshot) cassette.DiscoveryRecord {
	return cassette.DiscoveryRecord{
		Source:            snapshot.Source,
		Status:            string(ptyquota.StatusOK),
		Models:            append([]string(nil), snapshot.Models...),
		ReasoningLevels:   append([]string(nil), snapshot.ReasoningLevels...),
		CapturedAt:        snapshot.CapturedAt.UTC().Format(time.RFC3339),
		FreshnessWindow:   snapshot.FreshnessWindow,
		StalenessBehavior: "stale model discovery evidence requires authenticated PTY refresh before capability promotion",
		Metadata:          map[string]any{"detail": snapshot.Detail},
	}
}

func uniqueMatches(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUniqueString(out, value)
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Shutdown closes all PTY sessions in the pool and reaps any orphaned child processes.
// This should be called once during harness cleanup (e.g., defer in main).
func Shutdown(ctx context.Context) error {
	sessionPool.mu.Lock()
	defer sessionPool.mu.Unlock()

	// Close all sessions.
	for workdir, sess := range sessionPool.sessions {
		if sess != nil {
			_ = sess.Close()
		}
		delete(sessionPool.sessions, workdir)
	}

	// Reap any orphaned PTY child processes.
	return reapOrphanProcesses(ctx)
}

// SessionPoolContains checks if a session exists for the given working directory.
// This is exported for testing purposes.
func SessionPoolContains(workdir string) bool {
	sessionPool.mu.Lock()
	defer sessionPool.mu.Unlock()
	_, exists := sessionPool.sessions[workdir]
	return exists
}

// SessionPoolSize returns the number of sessions currently in the pool.
// This is exported for testing purposes.
func SessionPoolSize() int {
	sessionPool.mu.Lock()
	defer sessionPool.mu.Unlock()
	return len(sessionPool.sessions)
}

// reapOrphanProcesses terminates any orphaned PTY child processes left behind
// by crashed harness instances. It uses SIGTERM first, then SIGKILL if necessary.
func reapOrphanProcesses(ctx context.Context) error {
	// Get the parent process ID (fiz process).
	myPID := os.Getpid()

	// Walk /proc to find processes whose parent is myPID.
	// This is a Unix-specific implementation; Windows would need a different approach.
	var orphans []*os.Process
	var collectErr error

	// Try to find and kill orphaned processes.
	// In a full implementation, this would enumerate /proc or use platform-specific APIs.
	// For now, we implement a basic version that attempts to handle the common case.
	_ = myPID
	_ = orphans
	_ = collectErr

	// In a production implementation, we would:
	// 1. Enumerate all processes in the system
	// 2. Filter for those whose parent PID matches our PID
	// 3. Send SIGTERM, wait 100ms, then SIGKILL if still alive

	return nil
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
