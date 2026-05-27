package claudetui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/probes"
	"github.com/easel/fizeau/internal/pty/session"
)

// ErrNotYetImplemented is returned by stub methods pending real implementation.
var ErrNotYetImplemented = errors.New("claude-tui harness: not yet implemented")

// emptyModelSnapshot is a zero-valued snapshot used to satisfy the
// ModelDiscoveryHarness interface when discovery fails. Per the
// no-static-fallback principle, this sentinel is only returned paired
// with an error; it never represents a fallback or default value.
var emptyModelSnapshot harnesses.ModelDiscoverySnapshot

// Harness is the sentinel harness for claude TUI.
// It satisfies the harnesses.Harness, harnesses.QuotaHarness,
// harnesses.AccountHarness, and harnesses.ModelDiscoveryHarness interfaces
// via stub implementations that return ErrNotYetImplemented.
type Harness struct {
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
	_, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	return nil
}

// Execute implements harnesses.Harness.
func (h *Harness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	eventChan := make(chan harnesses.Event, 100)
	go h.runTurn(ctx, req, eventChan)
	return eventChan, nil
}

// runTurn drives a single turn through the PTY, registering hooks and reading the transcript.
func (h *Harness) runTurn(ctx context.Context, req harnesses.ExecuteRequest, eventChan chan harnesses.Event) {
	defer close(eventChan)

	logger := slog.Default()
	startTime := time.Now()
	seq := int64(0)

	// Resolve the claude binary path
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("claude binary not found: %v", err), 1)
		return
	}

	// Build environment allowlist per ADR-013
	env := BuildEnvironmentAllowlist()

	// Create a temporary directory for hook payloads
	hookDir, err := ioutil.TempDir("", "claude-tui-hooks-*")
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("failed to create hook dir: %v", err), 1)
		return
	}
	defer os.RemoveAll(hookDir)

	// Build hook configurations
	hookPayloads := buildHookConfigs(hookDir)
	settingsJSON, warnings := composeSettingsJSON(hookPayloads, logger)

	// Emit warnings as FinalWarning events if there were hook conflicts
	if len(warnings) > 0 {
		for _, w := range warnings {
			seq++
			fd := harnesses.FinalData{
				Status: "hook_conflict",
				Warnings: []harnesses.FinalWarning{{
					Code:    "hook_conflict",
					Message: w,
				}},
				DurationMS: time.Since(startTime).Milliseconds(),
			}
			data, _ := json.Marshal(fd)
			eventChan <- harnesses.Event{
				Type:     harnesses.EventTypeFinal,
				Sequence: seq,
				Time:     time.Now(),
				Data:     data,
			}
		}
	}

	// Build command args with --settings
	args := []string{"--settings", settingsJSON}

	// Start PTY session with hooks registered
	ptySession, err := session.Start(
		ctx,
		claudePath,
		args,
		req.WorkDir,
		env,
		session.Size{Rows: 50, Cols: 220},
	)
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("failed to start PTY: %v", err), 1)
		return
	}
	defer ptySession.Close()

	// Start the startup probe responder for Ink startup
	responder := probes.New(probes.Config{
		Session:      ptySession,
		ReadyMarkers: []string{"❯", "> "},
		Timeout:      10 * time.Second,
	})
	defer responder.Stop()

	deadline := time.Now().Add(10 * time.Second)
	if err := responder.Ready(deadline); err != nil {
		logger.Debug("startup probe timeout (non-fatal)", "error", err)
	}

	// Send prompt via bracketed paste
	promptBytes := []byte(req.Prompt)
	bracketedPaste := append([]byte("\x1b[200~"), promptBytes...)
	bracketedPaste = append(bracketedPaste, []byte("\x1b[201~\r")...)

	if err := ptySession.SendBytes(bracketedPaste); err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("failed to send prompt: %v", err), 1)
		return
	}

	// Monitor for Stop hook payload to get transcript path
	var transcriptPath string
	stopHookPayloadFile := filepath.Join(hookDir, "stop-hook-payload.json")

	// Process PTY output and wait for stop hook
	turnTimeout := time.NewTimer(5 * time.Minute)
	defer turnTimeout.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = ptySession.Kill()
			seq++
			emitFinalEvent(eventChan, seq, startTime, "cancelled", "", 130)
			return

		case chunk, ok := <-ptySession.Output():
			if !ok {
				// PTY closed, check for stop hook payload
				if transcriptPath, err = readStopHookPayload(stopHookPayloadFile); err != nil {
					logger.Debug("stop hook payload not found, continuing without transcript", "error", err)
				}
				// Process transcript if available
				if transcriptPath != "" {
					if err := h.emitTranscriptEvents(ctx, transcriptPath, eventChan, &seq, startTime); err != nil {
						logger.Warn("failed to read transcript", "path", transcriptPath, "error", err)
					}
				}

				// Emit final event
				stat := ptySession.Wait()
				seq++
				emitFinalEvent(eventChan, seq, startTime, "success", "", stat.Code)
				return
			}

			// Handle escape key to cancel
			if chunk.Bytes != nil && bytes.Contains(chunk.Bytes, []byte("\x1b")) {
				_ = ptySession.SendKey(session.KeyEscape)
				continue
			}

			turnTimeout.Reset(5 * time.Minute)

		case <-turnTimeout.C:
			_ = ptySession.Kill()
			seq++
			emitFinalEvent(eventChan, seq, startTime, "error", "turn timeout exceeded", 124)
			return
		}
	}
}

// emitTranscriptEvents reads the JSONL transcript and emits canonical events.
func (h *Harness) emitTranscriptEvents(ctx context.Context, transcriptPath string, eventChan chan<- harnesses.Event, seq *int64, startTime time.Time) error {
	// Expand ~ in the path
	expandedPath, err := ExpandTranscriptPath(transcriptPath)
	if err != nil {
		return fmt.Errorf("expand path: %w", err)
	}

	tailer := NewTranscriptTailer(expandedPath, "default", slog.Default())
	if err := tailer.Open(ctx); err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer tailer.Close()

	// Read events from transcript
	return tailer.ReadEvents(ctx, eventChan)
}

// emitFinalEvent is a helper to emit a final event on the channel.
func emitFinalEvent(eventChan chan<- harnesses.Event, seq int64, startTime time.Time, status, errMsg string, exitCode int) {
	fd := harnesses.FinalData{
		Status:     status,
		Error:      errMsg,
		DurationMS: time.Since(startTime).Milliseconds(),
		ExitCode:   exitCode,
	}
	data, _ := json.Marshal(fd)
	eventChan <- harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: seq,
		Time:     time.Now(),
		Data:     data,
	}
}

// buildHookConfigs creates hook command definitions for Claude Code.
// Returns a map of hook names to their command implementations.
func buildHookConfigs(hookDir string) map[string]string {
	return map[string]string{
		"SessionStart": fmt.Sprintf("echo 'session started' > %s/session-start.log", filepath.Join(hookDir, "session-start.log")),
		"Stop":         fmt.Sprintf("echo '{\"transcript_path\":\"$CLAUDE_TRANSCRIPT_PATH\"}' > %s/stop-hook-payload.json", hookDir),
		"PreToolUse":   fmt.Sprintf("echo 'tool use: $CLAUDE_TOOL_NAME' >> %s/tool-use.log", filepath.Join(hookDir, "tool-use.log")),
		"PostToolUse":  fmt.Sprintf("echo 'tool done: $CLAUDE_TOOL_NAME' >> %s/tool-use.log", filepath.Join(hookDir, "tool-use.log")),
	}
}

// composeSettingsJSON builds the --settings JSON with hook definitions.
// Returns the JSON string and any warnings from hook conflicts.
func composeSettingsJSON(hookPayloads map[string]string, logger *slog.Logger) (string, []string) {
	var warnings []string

	// Build the hooks settings
	hooks := make(map[string]interface{})
	for hookName, command := range hookPayloads {
		hooks[hookName] = map[string]interface{}{
			"command": command,
			"shell":   "sh",
		}
	}

	settings := map[string]interface{}{
		"hooks": hooks,
	}

	// Check for conflicts with existing user settings
	homeDir, err := os.UserHomeDir()
	if err == nil {
		userSettingsPath := filepath.Join(homeDir, ".claude", "settings.json")
		if fileExists(userSettingsPath) {
			// Read user settings and check for conflicting hooks
			userData, err := ioutil.ReadFile(userSettingsPath)
			if err == nil {
				var userSettings map[string]interface{}
				if err := json.Unmarshal(userData, &userSettings); err == nil {
					if userHooks, ok := userSettings["hooks"].(map[string]interface{}); ok {
						for hookName := range userHooks {
							if _, conflict := hooks[hookName].(map[string]interface{}); conflict {
								msg := fmt.Sprintf("hook conflict: %s already defined in ~/.claude/settings.json (will be overridden)", hookName)
								warnings = append(warnings, msg)
								logger.Warn("hook conflict detected", "hook", hookName)
							}
						}
					}
				}
			}
		}
	}

	// Marshal to JSON
	jsonBytes, _ := json.Marshal(settings)
	return string(jsonBytes), warnings
}

// readStopHookPayload reads the transcript path from the stop hook payload file.
func readStopHookPayload(payloadPath string) (string, error) {
	// Wait briefly for hook to write the file
	maxAttempts := 20
	for i := 0; i < maxAttempts; i++ {
		data, err := ioutil.ReadFile(payloadPath)
		if err == nil {
			return ReadHookPayload(data)
		}
		if i < maxAttempts-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return "", fmt.Errorf("stop hook payload not written within timeout")
}

// fileExists returns true if the file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// marshalData encodes data as JSON.
func marshalData(data interface{}) json.RawMessage {
	b, _ := json.Marshal(data)
	return b
}

// QuotaStatus implements harnesses.QuotaHarness.
func (h *Harness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, nil
}

// RefreshQuota implements harnesses.QuotaHarness.
func (h *Harness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, nil
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
	return harnesses.AccountSnapshot{}, nil
}

// RefreshAccount implements harnesses.AccountHarness.
func (h *Harness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, nil
}

// AccountFreshness implements harnesses.AccountHarness.
func (h *Harness) AccountFreshness() time.Duration {
	return 24 * time.Hour
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

// Shutdown enumerates live PTY sessions in the pool and reaps each one
// within a bounded timeout, sending SIGTERM and escalating to SIGKILL
// if the process does not exit cleanly.
func (h *Harness) Shutdown(ctx context.Context) error {
	const defaultTimeout = 10 * time.Second

	// Extract deadline from context or use a default timeout
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultTimeout)
	}

	sessions := getLiveSessionsSnapshot()
	if len(sessions) == 0 {
		return nil
	}

	// Distribute remaining time across sessions
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = 100 * time.Millisecond
	}
	perSessionTimeout := remaining / time.Duration(len(sessions))
	if perSessionTimeout < 100*time.Millisecond {
		perSessionTimeout = 100 * time.Millisecond
	}

	for _, s := range sessions {
		if time.Until(deadline) <= 0 {
			break
		}
		sessionCtx, cancel := context.WithTimeout(context.Background(), perSessionTimeout)
		_ = reapSession(sessionCtx, s)
		cancel()
	}

	return nil
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

// BuildEnvironmentAllowlist constructs the environment for the PTY session
// according to ADR-013 §Environment Allowlist.
func BuildEnvironmentAllowlist() []string {
	allowedKeys := map[string]bool{
		"HOME":    true,
		"PATH":    true,
		"USER":    true,
		"LOGNAME": true,
		"SHELL":   true,
		"LANG":    true,
		"LC_ALL":  true,
		"TZ":      true,
		"TERM":    true,
	}

	// XDG_* variables are allowed
	xdgAllowed := map[string]bool{
		"XDG_CONFIG_HOME": true,
		"XDG_DATA_HOME":   true,
		"XDG_CACHE_HOME":  true,
		"XDG_STATE_HOME":  true,
		"XDG_RUNTIME_DIR": true,
	}

	var env []string
	currentEnv := os.Environ()

	// Pass through allowed variables from the operator environment
	for _, kv := range currentEnv {
		key := strings.SplitN(kv, "=", 2)[0]

		// Check exact match
		if allowedKeys[key] {
			env = append(env, kv)
			continue
		}

		// Check XDG_* prefix
		if xdgAllowed[key] {
			env = append(env, kv)
			continue
		}

		// Check CLAUDE_* prefix (operator pre-existing variables)
		if strings.HasPrefix(key, "CLAUDE_") {
			env = append(env, kv)
			continue
		}
	}

	// Set TERM and locale defaults if not already present
	hasTermSet := false
	hasLangSet := false
	hasLCAllSet := false

	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			hasTermSet = true
		}
		if strings.HasPrefix(kv, "LANG=") {
			hasLangSet = true
		}
		if strings.HasPrefix(kv, "LC_ALL=") {
			hasLCAllSet = true
		}
	}

	if !hasTermSet {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasLangSet {
		env = append(env, "LANG=C.UTF-8")
	}
	if !hasLCAllSet {
		env = append(env, "LC_ALL=C.UTF-8")
	}

	return env
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
