package claudetui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/easel/fizeau/internal/pty/session"
	"github.com/easel/fizeau/internal/pty/terminal"
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

// ptyConn is the minimal PTY surface runTurnOver needs. *session.Session
// satisfies it; tests inject a scripted fake so the turn loop runs with no
// live binary, no network, and no real PTY.
type ptyConn interface {
	Output() <-chan session.OutputChunk
	SendBytes(b []byte) error
	SendKey(k session.Key) error
	Size() session.Size
	Kill() error
}

// turnEnv carries the resolved per-turn collaborators so the turn loop is
// fully injectable from tests.
type turnEnv struct {
	conn ptyConn
	// hookDir holds PreToolUse/PostToolUse tool-event payloads and the Stop
	// payload file for this turn.
	hookDir string
	// stopPayloadPath is the Stop-hook payload file for this turn; it is
	// unlinked before the prompt is sent so a stale payload never short-
	// circuits completion.
	stopPayloadPath string
	// nonce is the per-turn Stop nonce; completion requires the Stop payload
	// to carry this exact nonce.
	nonce string
	// readyTimeout bounds Ink startup; pollInterval paces hook/stop polling.
	readyTimeout time.Duration
	pollInterval time.Duration
	// turnTimeout bounds the whole turn.
	turnTimeout time.Duration
	logger      *slog.Logger
}

// runTurn drives a single turn through a pooled PTY session.
func (h *Harness) runTurn(ctx context.Context, req harnesses.ExecuteRequest, eventChan chan harnesses.Event) {
	defer close(eventChan)

	logger := slog.Default()
	startTime := time.Now()
	seq := int64(0)

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("claude binary not found: %v", err), 1)
		return
	}

	// Build environment allowlist per ADR-013. Honor the allowlist exactly;
	// never append os.Environ wholesale.
	env := BuildEnvironmentAllowlist()

	hookDir, err := os.MkdirTemp("", "claude-tui-hooks-*")
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("failed to create hook dir: %v", err), 1)
		return
	}
	defer os.RemoveAll(hookDir)

	// A per-turn nonce binds the Stop payload to this turn. The Stop hook
	// writes the nonce + transcript path into the payload file.
	nonce := newTurnNonce()
	stopPayloadPath := filepath.Join(hookDir, "stop-hook-payload.json")

	// Emit settings in Claude Code's REAL hook schema.
	settingsJSON, warnings := composeSettingsJSON(buildHookConfigs(hookDir, stopPayloadPath, nonce), logger)
	for _, w := range warnings {
		seq++
		eventChan <- harnesses.Event{
			Type:     harnesses.EventTypeProgress,
			Sequence: seq,
			Time:     time.Now(),
			Data: mustMarshal(harnesses.FinalWarning{
				Code:    "hook_conflict",
				Message: w,
			}),
		}
	}

	args := buildLaunchArgs(settingsJSON)

	ptySession, err := getOrCreatePooledSession(
		ctx, "claude-tui", claudePath, args, req.WorkDir, env,
		session.Size{Rows: 50, Cols: 220},
	)
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("failed to get pooled session: %v", err), 1)
		return
	}
	// Symmetric release: every claimed session is released exactly once.
	defer releasePooledSession("claude-tui", req.WorkDir, ptySession)

	te := turnEnv{
		conn:            ptySession,
		hookDir:         hookDir,
		stopPayloadPath: stopPayloadPath,
		nonce:           nonce,
		readyTimeout:    10 * time.Second,
		pollInterval:    50 * time.Millisecond,
		turnTimeout:     5 * time.Minute,
		logger:          logger,
	}

	status := h.runTurnOver(ctx, te, req, eventChan, &seq, startTime)
	if status == turnEvicted {
		evictPooledSession("claude-tui", req.WorkDir, ptySession)
	}
}

// buildLaunchArgs builds the claude CLI argument slice for an unattended TUI
// turn. The harness launches interactively (NO --print) under
// `--permission-mode bypassPermissions` so tools run without prompts on the
// Claude Max subscription, and injects the hook settings via --settings. The
// argument order is asserted by a deterministic test so that dropping either
// the flag or its value regresses the suite.
func buildLaunchArgs(settingsJSON string) []string {
	return []string{"--permission-mode", "bypassPermissions", "--settings", settingsJSON}
}

type turnOutcome int

const (
	turnOK turnOutcome = iota
	turnEvicted
)

// runTurnOver drives the turn over an injected ptyConn. It uses a SINGLE
// Output() consumer that (1) answers Ink startup probes, (2) feeds a vt10x
// emulator for dialog matching, and (3) answers the folder-trust dialog with
// Enter. Completion is signaled exclusively by the Stop hook payload carrying
// the per-turn nonce. tool_call/tool_result ProgressEvents are emitted from
// the hook-event payload files during the turn.
//
// It is exported to tests via RunTurnForTest.
func (h *Harness) runTurnOver(
	ctx context.Context,
	te turnEnv,
	req harnesses.ExecuteRequest,
	eventChan chan<- harnesses.Event,
	seq *int64,
	startTime time.Time,
) turnOutcome {
	logger := te.logger
	if logger == nil {
		logger = slog.Default()
	}

	// Document req fields with no TUI affordance under bypassPermissions.
	// Model/reasoning/permissions are documented gaps (see ADR-013 capability
	// baseline): the TUI offers no batch flag for these on this path. Emit a
	// progress warning so the gap is observable rather than silent.
	for _, gap := range documentedRequestGaps(req) {
		*seq++
		eventChan <- harnesses.Event{
			Type:     harnesses.EventTypeProgress,
			Sequence: *seq,
			Time:     time.Now(),
			Data:     mustMarshal(harnesses.FinalWarning{Code: "unsupported_request_field", Message: gap}),
		}
	}

	// Unlink any prior Stop payload before sending the prompt so a stale
	// completion signal cannot short-circuit this turn.
	_ = os.Remove(te.stopPayloadPath)

	size := te.conn.Size()
	emu := terminal.New(terminal.Size{Rows: int(size.Rows), Cols: int(size.Cols)})

	probe := newStartupProbe(te.conn)
	trustAnswered := false

	hookTailer := NewHookEventTailer(te.hookDir, logger)

	// Send the prompt via bracketed paste after a brief startup grace. We do
	// not block on a ready marker (it fires mid-turn); the Stop hook is the
	// only reliable turn-end signal.
	promptSent := false
	sendPrompt := func() {
		if promptSent {
			return
		}
		paste := append([]byte("\x1b[200~"), []byte(req.Prompt)...)
		paste = append(paste, []byte("\x1b[201~\r")...)
		if err := te.conn.SendBytes(paste); err != nil {
			logger.Warn("failed to send prompt", "error", err)
		}
		promptSent = true
	}

	turnTimer := time.NewTimer(te.turnTimeout)
	defer turnTimer.Stop()
	startupTimer := time.NewTimer(te.readyTimeout)
	defer startupTimer.Stop()
	poll := time.NewTicker(te.pollInterval)
	defer poll.Stop()

	emitFromTailer := func() {
		*seq = hookTailer.Drain(*seq, func(ev harnesses.Event) {
			select {
			case eventChan <- ev:
			case <-ctx.Done():
			}
		})
	}

	// Send the prompt once we either see a ready marker or the startup grace
	// elapses, whichever comes first.
	for {
		select {
		case <-ctx.Done():
			_ = te.conn.Kill()
			*seq++
			emitFinalEvent(eventChan, *seq, startTime, "cancelled", "", 130)
			return turnEvicted

		case <-turnTimer.C:
			_ = te.conn.Kill()
			*seq++
			emitFinalEvent(eventChan, *seq, startTime, "timed_out", "turn timeout exceeded", 124)
			return turnEvicted

		case <-startupTimer.C:
			sendPrompt()

		case <-poll.C:
			// Drain mid-turn tool events.
			emitFromTailer()
			// Check for Stop completion carrying our nonce.
			if path, ok := readStopHookPayloadNonce(te.stopPayloadPath, te.nonce); ok {
				emitFromTailer()
				h.emitTranscriptAndFinal(ctx, path, eventChan, seq, startTime, logger)
				return turnOK
			}

		case chunk, ok := <-te.conn.Output():
			if !ok {
				// PTY closed; treat as end-of-stream. Try the Stop payload
				// first, else synthesize a final.
				emitFromTailer()
				if path, ok := readStopHookPayloadNonce(te.stopPayloadPath, te.nonce); ok {
					h.emitTranscriptAndFinal(ctx, path, eventChan, seq, startTime, logger)
					return turnEvicted
				}
				*seq++
				emitFinalEvent(eventChan, *seq, startTime, "failed", "session closed before Stop hook", 1)
				return turnEvicted
			}
			if chunk.ReadError != nil {
				continue
			}
			// 1) answer Ink startup probes.
			probe.Feed(chunk.Bytes)
			// 2) feed the emulator and 3) match the folder-trust dialog.
			frame, _ := emu.Feed(chunk.Bytes)
			if !trustAnswered && screenHasFolderTrustDialog(frame) {
				_ = te.conn.SendKey(session.KeyEnter)
				trustAnswered = true
			}
			// Once the prompt UI is ready, send the prompt.
			if !promptSent && screenReadyForPrompt(frame) {
				sendPrompt()
			}
		}
	}
}

// emitTranscriptAndFinal reads the transcript (which produces its own single
// final event) and, if the transcript is unreadable, synthesizes a final.
func (h *Harness) emitTranscriptAndFinal(
	ctx context.Context,
	transcriptPath string,
	eventChan chan<- harnesses.Event,
	seq *int64,
	startTime time.Time,
	logger *slog.Logger,
) {
	expanded, err := ExpandTranscriptPath(transcriptPath)
	if err == nil {
		tailer := NewTranscriptTailer(expanded, "default", logger)
		// Continue the harness sequence counter through the transcript events.
		tailer.seqCounter = *seq
		tailer.startTime = startTime
		if err := tailer.ReadEvents(ctx, eventChan); err == nil {
			*seq = tailer.seqCounter
			return
		}
		logger.Warn("failed to read transcript", "path", expanded, "error", err)
	} else {
		logger.Warn("failed to expand transcript path", "path", transcriptPath, "error", err)
	}
	// Transcript unreadable: emit exactly one final so the stream still closes.
	*seq++
	emitFinalEvent(eventChan, *seq, startTime, "success", "", 0)
}

// documentedRequestGaps returns one message per ExecuteRequest field that has
// no TUI affordance on the bypassPermissions path (documented gaps per
// ADR-013's capability baseline).
func documentedRequestGaps(req harnesses.ExecuteRequest) []string {
	var gaps []string
	if req.Model != "" {
		gaps = append(gaps, fmt.Sprintf("requested model %q is a documented gap: claude-tui has no batch model flag on the bypassPermissions path", req.Model))
	}
	if req.Reasoning != "" {
		gaps = append(gaps, fmt.Sprintf("requested reasoning %q is a documented gap: no TUI affordance sets per-turn reasoning", req.Reasoning))
	}
	if req.Permissions != "" && req.Permissions != "unrestricted" {
		gaps = append(gaps, fmt.Sprintf("requested permissions %q is a documented gap: claude-tui launches with bypassPermissions", req.Permissions))
	}
	return gaps
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

// HookCommand is one command-hook entry in Claude Code's real settings schema:
//
//	{"hooks":{"<Event>":[{"matcher":"...","hooks":[{"type":"command","command":"..."}]}]}}
type HookCommand struct {
	Matcher string `json:"matcher,omitempty"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

// cmdHook builds a single matcher group wrapping one command hook.
func cmdHook(matcher, command string) HookCommand {
	hc := HookCommand{Matcher: matcher}
	hc.Hooks = append(hc.Hooks, struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}{Type: "command", Command: command})
	return hc
}

// buildHookConfigs builds the per-event hook command groups in Claude Code's
// REAL schema. The Stop hook records the per-turn nonce + transcript path so
// completion can be bound to this turn. PreToolUse/PostToolUse write per-tool
// payload files the HookEventTailer reads for mid-turn ProgressEvents.
//
// Hook commands read the hook payload from stdin (Claude Code passes the hook
// event JSON on stdin) and merge in the nonce for the Stop hook.
func buildHookConfigs(hookDir, stopPayloadPath, nonce string) map[string][]HookCommand {
	toolDir := hookDir
	// PreToolUse/PostToolUse: persist the stdin hook payload to a uniquely
	// named "tool-*.json" file the tailer scans. Use $$ + nanoseconds via a
	// counter file is overkill; a date+pid suffix from the shell is enough to
	// keep filenames distinct and lexically ordered.
	preCmd := fmt.Sprintf(`cat > %q/tool-$(date +%%s%%N)-pre.json`, toolDir)
	postCmd := fmt.Sprintf(`cat > %q/tool-$(date +%%s%%N)-post.json`, toolDir)
	// Stop: capture stdin transcript path, then rewrite a payload carrying the
	// nonce so the harness can verify the Stop belongs to this turn.
	stopCmd := fmt.Sprintf(
		`p=$(cat); t=$(printf '%%s' "$p" | sed -n 's/.*"transcript_path"[ ]*:[ ]*"\([^"]*\)".*/\1/p'); printf '{"nonce":"%s","transcript_path":"%s"}' "$t" > %q`,
		nonce, "%s", stopPayloadPath,
	)

	return map[string][]HookCommand{
		"PreToolUse":  {cmdHook("*", preCmd)},
		"PostToolUse": {cmdHook("*", postCmd)},
		"Stop":        {cmdHook("", stopCmd)},
	}
}

// composeSettingsJSON builds the --settings JSON in Claude Code's real schema
// and returns warnings for any hook events the operator's
// ~/.claude/settings.json already defines (which --settings will override).
func composeSettingsJSON(hooks map[string][]HookCommand, logger *slog.Logger) (string, []string) {
	var warnings []string

	settings := map[string]interface{}{"hooks": hooks}

	if homeDir, err := os.UserHomeDir(); err == nil {
		userSettingsPath := filepath.Join(homeDir, ".claude", "settings.json")
		if fileExists(userSettingsPath) {
			if userData, err := os.ReadFile(userSettingsPath); err == nil {
				var userSettings map[string]interface{}
				if err := json.Unmarshal(userData, &userSettings); err == nil {
					if userHooks, ok := userSettings["hooks"].(map[string]interface{}); ok {
						for hookName := range hooks {
							if _, conflict := userHooks[hookName]; conflict {
								msg := fmt.Sprintf("hook conflict: %s already defined in ~/.claude/settings.json (overridden by --settings)", hookName)
								warnings = append(warnings, msg)
								if logger != nil {
									logger.Warn("hook conflict detected", "hook", hookName)
								}
							}
						}
					}
				}
			}
		}
	}

	jsonBytes, _ := json.Marshal(settings)
	return string(jsonBytes), warnings
}

// readStopHookPayloadNonce reads the Stop payload file and returns the
// transcript path only when the payload's nonce matches the per-turn nonce.
// A missing file or mismatched nonce reports ok=false (turn not yet complete).
func readStopHookPayloadNonce(payloadPath, nonce string) (string, bool) {
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		return "", false
	}
	var payload struct {
		Nonce          string `json:"nonce"`
		TranscriptPath string `json:"transcript_path"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}
	if payload.Nonce != nonce || payload.TranscriptPath == "" {
		return "", false
	}
	return payload.TranscriptPath, true
}

// newTurnNonce returns a fresh per-turn Stop nonce.
func newTurnNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// fileExists returns true if the file exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness.
// Per the no-static-fallback principle and ADR-012, this method reads from
// the discovery cache (which may be stale) and triggers a background refresh
// if needed. It never returns a static literal. On cache miss with refresh
// failure, returns ErrModelDiscoveryEvidenceMissing.
func (h *Harness) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	// Read from cache (may be stale, never blocks on IO)
	res, err := modelDiscoveryCache.Read(modelDiscoveryCacheSource)
	if err != nil {
		return emptyModelSnapshot, err
	}

	var snapshot harnesses.ModelDiscoverySnapshot
	if res.Data != nil {
		if err := json.Unmarshal(res.Data, &snapshot); err != nil {
			// Cache corruption; trigger a refresh
			modelDiscoveryCache.MaybeRefresh(modelDiscoveryCacheSource, modelDiscoveryRefresher)
			return emptyModelSnapshot, fmt.Errorf("cache corruption: %w", err)
		}
		// Return the snapshot regardless of freshness; MaybeRefresh will
		// update stale data in the background per ADR-012 Algorithm 6.
		if len(snapshot.Models) > 0 {
			modelDiscoveryCache.MaybeRefresh(modelDiscoveryCacheSource, modelDiscoveryRefresher)
			return snapshot, nil
		}
	}

	// Cache miss or empty snapshot; try to refresh synchronously.
	if err := modelDiscoveryCache.MaybeRefreshSync(modelDiscoveryCacheSource, modelDiscoveryRefresher); err != nil {
		// Refresh failed; return error
		return emptyModelSnapshot, fmt.Errorf("model discovery refresh failed: %w", err)
	}

	// Refresh succeeded; read the freshly written data
	res, err = modelDiscoveryCache.Read(modelDiscoveryCacheSource)
	if err != nil {
		return emptyModelSnapshot, err
	}
	if res.Data == nil {
		return emptyModelSnapshot, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	if err := json.Unmarshal(res.Data, &snapshot); err != nil {
		return emptyModelSnapshot, fmt.Errorf("failed to decode refreshed model discovery: %w", err)
	}
	if len(snapshot.Models) == 0 {
		return emptyModelSnapshot, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness.
func (h *Harness) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(family))
	if !isSupportedClaudeTuiAlias(normalized) {
		return "", harnesses.ErrAliasNotResolvable
	}
	resolved := resolveClaudeTuiFamilyAlias(normalized, snapshot)
	if resolved == "" {
		return "", harnesses.ErrAliasNotResolvable
	}
	return resolved, nil
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
	return append([]string(nil), supportedAliases...)
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

func isSupportedClaudeTuiAlias(family string) bool {
	for _, a := range supportedAliases {
		if a == family {
			return true
		}
	}
	return false
}

func resolveClaudeTuiFamilyAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) string {
	for _, model := range snapshot.Models {
		modelLower := strings.ToLower(model)
		familyLower := strings.ToLower(family)
		if strings.Contains(modelLower, familyLower) {
			return model
		}
	}
	return ""
}

// startupProbe answers Ink startup probes (DA1/DA2/DSR/XTVERSION/window-size)
// from bytes fed by the SINGLE Output() consumer. It keeps no goroutine of its
// own so it never competes with the turn loop for Output() chunks.
type startupProbe struct {
	conn ptyConn
	buf  []byte
}

func newStartupProbe(conn ptyConn) *startupProbe {
	return &startupProbe{conn: conn}
}

// Feed appends bytes and answers any complete startup probe found in the
// accumulated buffer. The buffer is trimmed to bound memory.
func (p *startupProbe) Feed(b []byte) {
	if len(b) == 0 {
		return
	}
	p.buf = append(p.buf, b...)
	s := p.buf
	if bytes.Contains(s, []byte("\x1b[c")) || bytes.Contains(s, []byte("\x1b[?c")) {
		_ = p.conn.SendBytes([]byte("\x1b[?1;0c"))
	}
	if bytes.Contains(s, []byte("\x1b[>c")) || bytes.Contains(s, []byte("\x1b[>?c")) {
		_ = p.conn.SendBytes([]byte("\x1b[?62;4;0c"))
	}
	if bytes.Contains(s, []byte("\x1b[6n")) {
		_ = p.conn.SendBytes([]byte("\x1b[1;1R"))
	}
	if bytes.Contains(s, []byte("\x1b[>q")) {
		_ = p.conn.SendBytes([]byte("\x1b[>0;370;0c"))
	}
	if bytes.Contains(s, []byte("\x1b[18t")) {
		size := p.conn.Size()
		_ = p.conn.SendBytes([]byte(fmt.Sprintf("\x1b[8;%d;%dt", size.Rows, size.Cols)))
	}
	if bytes.Contains(s, []byte("\x1b[19t")) {
		size := p.conn.Size()
		_ = p.conn.SendBytes([]byte(fmt.Sprintf("\x1b[9;%d;%dt", size.Rows*14, size.Cols*8)))
	}
	if len(p.buf) > 4096 {
		p.buf = append([]byte(nil), p.buf[len(p.buf)-1024:]...)
	}
}

// folderTrustMarkers identify the Claude Code folder-trust dialog once the
// screen is rendered through the vt10x emulator (naive ANSI stripping yields
// space-less garbage because the dialog is laid out with cursor positioning).
var folderTrustMarkers = []string{
	"Is this a project you created or one you trust",
	"Yes, I trust this folder",
	"trust the files in this folder",
}

// screenHasFolderTrustDialog reports whether the rendered frame shows the
// folder-trust dialog (answered with Enter = default "Yes, I trust...").
func screenHasFolderTrustDialog(frame terminal.Frame) bool {
	joined := strings.Join(frame.Text, "\n")
	for _, m := range folderTrustMarkers {
		if strings.Contains(joined, m) {
			return true
		}
	}
	return false
}

// screenReadyForPrompt reports whether the rendered frame shows the Claude
// input prompt (the "❯"/"> " affordance) so the prompt can be pasted.
func screenReadyForPrompt(frame terminal.Frame) bool {
	joined := strings.Join(frame.Text, "\n")
	if strings.Contains(joined, "Is this a project") {
		return false // still on trust dialog
	}
	return strings.Contains(joined, "❯") || strings.Contains(joined, "> ")
}

// RunTurnForTest drives a single turn over an injected ptyConn with explicit
// per-turn collaborators, returning the events emitted. It exists so the turn
// loop can be tested deterministically with a scripted fake PTY, no live
// binary, no network, and no real PTY. The caller supplies the hook directory
// and Stop payload path so the test can write fixtures into them.
func RunTurnForTest(
	ctx context.Context,
	conn TestPTYConn,
	req harnesses.ExecuteRequest,
	hookDir, stopPayloadPath, nonce string,
	readyTimeout, pollInterval, turnTimeout time.Duration,
) []harnesses.Event {
	h := &Harness{}
	eventChan := make(chan harnesses.Event, 256)
	seq := int64(0)
	start := time.Now()
	te := turnEnv{
		conn:            conn,
		hookDir:         hookDir,
		stopPayloadPath: stopPayloadPath,
		nonce:           nonce,
		readyTimeout:    readyTimeout,
		pollInterval:    pollInterval,
		turnTimeout:     turnTimeout,
		logger:          slog.Default(),
	}
	go func() {
		h.runTurnOver(ctx, te, req, eventChan, &seq, start)
		close(eventChan)
	}()
	var events []harnesses.Event
	for ev := range eventChan {
		events = append(events, ev)
	}
	return events
}

// TestPTYConn is the injectable PTY surface used by RunTurnForTest. It mirrors
// the unexported ptyConn so tests in the external _test package can build a
// scripted fake.
type TestPTYConn interface {
	Output() <-chan session.OutputChunk
	SendBytes(b []byte) error
	SendKey(k session.Key) error
	Size() session.Size
	Kill() error
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
