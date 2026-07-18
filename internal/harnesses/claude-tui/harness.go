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
	"sort"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/processlifecycle"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
	"github.com/easel/fizeau/internal/pty/terminal"
)

// ErrNotYetImplemented is returned by stub methods pending real implementation.
var ErrNotYetImplemented = errors.New("claude-tui harness: not yet implemented")

// promptSubmitDelay is the settle window between writing the bracketed-paste
// END marker and sending the standalone submit Enter. Proven against live
// Claude Code 2.1.162: a too-eager submit (Enter glued to the paste end in one
// write) is swallowed by Ink and never starts the turn. 400ms is the value the
// live spike validated; it is a package var so tests can shrink it.
var promptSubmitDelay = 400 * time.Millisecond

// SetPromptSubmitDelayForTest overrides promptSubmitDelay and returns a restore
// function. It exists so deterministic tests in the external _test package can
// shrink the live-tuned 400ms settle window to keep the scripted turn loop fast
// while still exercising the paste→settle→submit sequence.
func SetPromptSubmitDelayForTest(d time.Duration) func() {
	prev := promptSubmitDelay
	promptSubmitDelay = d
	return func() { promptSubmitDelay = prev }
}

// drainTimer non-blockingly drains a stopped timer's channel so a later Reset
// does not immediately fire on a stale tick.
func drainTimer(t *time.Timer) {
	select {
	case <-t.C:
	default:
	}
}

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
	harnesses.PortableRuntimeRunnerState

	// Binary is the absolute path to the claude executable. When empty the
	// harness resolves "claude" from PATH for both execution and portable
	// runtime discovery.
	Binary string
}

// PortableRuntimeStructure describes this actual runner without probing PATH.
func (h *Harness) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      "claude-tui",
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

// Info implements harnesses.Harness.
func (h *Harness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{
		Name:                 "claude-tui",
		Type:                 "subprocess",
		Available:            false,
		IsSubscription:       true,
		AutoRoutingEligible:  true,
		DefaultModel:         "claude-sonnet-4-6",
		SupportedPermissions: []string{"unrestricted"},
		CostClass:            "medium",
	}
}

// HealthCheck implements harnesses.Harness.
func (h *Harness) HealthCheck(ctx context.Context) error {
	if ctx == nil {
		return errors.New("claude health check has nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := h.claudePath()
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	return ctx.Err()
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
	Wait() session.ExitStatus
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
	// stopGrace bounds the PTY-EOF race window in which a matching Stop hook
	// payload may still be published atomically.
	stopGrace time.Duration
	// turnTimeout bounds the whole turn.
	turnTimeout time.Duration
	logger      *slog.Logger
}

// runTurn drives a single turn through one request-local PTY session. Adapter
// progress streams immediately, but the final event is withheld until the PTY
// containment boundary is empty and all output has drained.
func (h *Harness) runTurn(ctx context.Context, req harnesses.ExecuteRequest, eventChan chan harnesses.Event) {
	defer close(eventChan)

	logger := slog.Default()
	startTime := time.Now()
	seq := int64(0)

	claudePath, err := h.claudePath()
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

	// Honor req.Model: launch the interactive TUI on the resolved tier model so
	// a default-policy (sonnet-tier) route actually EXECUTES that model rather
	// than whatever the account default happens to be. The resolved catalog
	// surface ID (e.g. "sonnet-4.6"/"opus-4.8") is mapped to the CLI-acceptable
	// model token claude's --model flag accepts (an alias or full ID).
	cliModel := claudeTuiLaunchModel(req.Model)
	args := buildLaunchArgs(settingsJSON, cliModel)

	ptySession, err := startPTYSession(
		ctx, claudePath, args, req.WorkDir, env, session.Size{Rows: 50, Cols: 220},
		session.WithLifecycleOptions(processlifecycle.BatchOptions{
			Harness: "claude-tui", OperationID: req.SessionID, SessionLogDir: req.SessionLogDir,
			LifecycleStateDir: req.LifecycleStateDir, CleanupTimeout: req.CleanupTimeout,
		}),
	)
	if err != nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "error", fmt.Sprintf("failed to start PTY session: %v", err), 1)
		return
	}

	te := turnEnv{
		conn:            ptySession,
		hookDir:         hookDir,
		stopPayloadPath: stopPayloadPath,
		nonce:           nonce,
		readyTimeout:    10 * time.Second,
		pollInterval:    50 * time.Millisecond,
		stopGrace:       250 * time.Millisecond,
		turnTimeout:     effectiveTurnTimeout(req.Timeout),
		logger:          logger,
	}

	turnEvents := make(chan harnesses.Event, 100)
	turnDone := make(chan struct{})
	go func() {
		defer close(turnDone)
		defer close(turnEvents)
		h.runTurnOver(ctx, &te, req, turnEvents, &seq, startTime)
	}()
	var final *harnesses.Event
	for event := range turnEvents {
		if event.Type == harnesses.EventTypeFinal {
			copy := event
			final = &copy
			continue
		}
		eventChan <- event
	}
	<-turnDone

	// runTurnOver was the sole output consumer. Once it returns, keep draining
	// while cleanup closes the terminal so the session read loop cannot block on
	// a full output channel and leak after the final event.
	outputDrained := make(chan struct{})
	go func() {
		defer close(outputDrained)
		for range ptySession.Output() {
		}
	}()
	_ = ptySession.Close()
	_ = ptySession.Wait()
	<-outputDrained

	if final == nil {
		seq++
		emitFinalEvent(eventChan, seq, startTime, "failed", "claude-tui turn ended without a final event", 1)
		return
	}
	eventChan <- *final
}

func (h *Harness) claudePath() (string, error) {
	if h.Binary != "" {
		if !filepath.IsAbs(h.Binary) || filepath.Clean(h.Binary) != h.Binary {
			return "", errors.New("configured claude binary is not an absolute normalized path")
		}
		resolved, err := filepath.EvalSymlinks(h.Binary)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return "", errors.New("configured claude binary is not executable")
		}
		return resolved, nil
	}
	return exec.LookPath("claude")
}

func effectiveTurnTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return 5 * time.Minute
}

// startPTYSession is the single claude-tui process startup boundary. Without
// an exact-path test replacement it delegates directly to session.Start here,
// preserving this harness package's lifecycle ownership.
func startPTYSession(
	ctx context.Context,
	command string,
	args []string,
	workdir string,
	env []string,
	size session.Size,
	opts ...session.Option,
) (*session.Session, error) {
	// The default function value is the sole production route to
	// session.Start(ctx, command, args, workdir, env, size, opts...); both it
	// and an exact-key test replacement use the common invocation below.
	starter := harnesses.PTYSessionStarter(session.Start)
	if replacement, ok := harnesses.LookupPTYSessionStarterForTest("claude-tui", command); ok {
		starter = replacement
	}
	return starter(ctx, command, args, workdir, env, size, opts...)
}

// buildLaunchArgs builds the claude CLI argument slice for an unattended TUI
// turn. The harness launches interactively (NO --print) under
// `--permission-mode bypassPermissions` so tools run without prompts on the
// Claude Max subscription, and injects the hook settings via --settings. The
// argument order is asserted by a deterministic test so that dropping either
// the flag or its value regresses the suite.
func buildLaunchArgs(settingsJSON string, cliModel string) []string {
	args := []string{"--permission-mode", "bypassPermissions", "--settings", settingsJSON}
	if cliModel != "" {
		args = append(args, "--model", cliModel)
	}
	return args
}

// claudeTuiLaunchModel maps a resolved catalog/tier model reference to the model
// token the claude CLI's --model flag accepts. Catalog claude-code surface IDs
// (sonnet-4.6, opus-4.8, opus-4.7, haiku-4.5, fable-1.0, ...) and full claude-<family>-...
// IDs collapse to the stable family alias (sonnet/opus/haiku/fable) so the launched
// session lands on the requested tier regardless of catalog point-version drift.
// An empty request model returns "" (launch on the account default). An
// unrecognized non-empty value is passed through verbatim so an explicit full
// model ID still reaches the CLI.
func claudeTuiLaunchModel(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return ""
	}
	switch {
	case normalized == "sonnet" || strings.HasPrefix(normalized, "sonnet-") || strings.HasPrefix(normalized, "claude-sonnet-"):
		return "sonnet"
	case normalized == "opus" || strings.HasPrefix(normalized, "opus-") || strings.HasPrefix(normalized, "claude-opus-"):
		return "opus"
	case normalized == "haiku" || strings.HasPrefix(normalized, "haiku-") || strings.HasPrefix(normalized, "claude-haiku-"):
		return "haiku"
	case normalized == "fable" || strings.HasPrefix(normalized, "fable-") || strings.HasPrefix(normalized, "claude-fable-"):
		return "fable"
	default:
		return normalized
	}
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
	te *turnEnv,
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
	// answeredDialogs tracks which first-run interstitials have already been
	// dismissed this turn so each is answered with Enter at most once.
	answeredDialogs := map[string]bool{}

	hookTailer := NewHookEventTailer(te.hookDir, logger)

	// Send the prompt via bracketed paste after a brief startup grace. We do
	// not block on a ready marker (it fires mid-turn); the Stop hook is the
	// only reliable turn-end signal.
	//
	// CRITICAL (proven against live Claude Code 2.1.162): the bracketed-paste
	// END marker must NOT be immediately followed by the submit Enter in the
	// SAME write. Ink lands the pasted text in the input box but does NOT submit
	// it when "\x1b[201~\r" arrives as one chunk — the turn never starts, the
	// Stop hook never fires, and the loop wedges to the turn timeout. The fix is
	// to (1) write the paste WITHOUT a trailing carriage return, then (2) send a
	// SEPARATE Enter keystroke after a brief settle delay so Ink processes the
	// paste-end before it sees the submit. The submit is driven by a one-shot
	// timer fired inside this same single-consumer select loop (no extra
	// goroutine racing the Output() consumer).
	promptSent := false
	submitTimer := time.NewTimer(0)
	submitTimer.Stop()
	drainTimer(submitTimer)
	defer submitTimer.Stop()
	sendPrompt := func() {
		if promptSent {
			return
		}
		paste := append([]byte("\x1b[200~"), []byte(req.Prompt)...)
		paste = append(paste, []byte("\x1b[201~")...)
		if err := te.conn.SendBytes(paste); err != nil {
			logger.Warn("failed to send prompt paste", "error", err)
		}
		// Submit on a separate keystroke after a short settle so the live TUI
		// registers the paste before the Enter. promptSubmitDelay is small but
		// non-zero; tests use a fast clock by passing pollInterval-scaled values.
		submitTimer.Reset(promptSubmitDelay)
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

		case <-submitTimer.C:
			// The paste has settled; submit it with a standalone carriage return.
			// This is the keystroke that actually starts the turn (see
			// sendPrompt). We write a bare "\r" via SendBytes (NOT SendKey) so it
			// is observably distinct from interstitial-dismissal Enters, which use
			// SendKey(KeyEnter).
			if err := te.conn.SendBytes([]byte("\r")); err != nil {
				logger.Warn("failed to submit prompt", "error", err)
			}

		case <-poll.C:
			// Drain mid-turn tool events.
			emitFromTailer()
			// Check for Stop completion carrying our nonce.
			if path, ok := readStopHookPayloadNonce(te.stopPayloadPath, te.nonce); ok {
				emitFromTailer()
				h.emitTranscriptAndFinal(ctx, te, path, eventChan, seq, startTime, logger)
				return turnOK
			}

		case chunk, ok := <-te.conn.Output():
			if !ok {
				// PTY EOF and Stop-hook publication are independent process/file
				// observations. Give the nonce-bound payload one short bounded
				// grace window before classifying the turn as failed.
				emitFromTailer()
				path, ok, exitStatus, exitKnown, waitErr := waitForStopPayloadAfterPTYEOF(ctx, te, turnTimer.C)
				if ok {
					h.emitTranscriptAndFinal(ctx, te, path, eventChan, seq, startTime, logger)
					return turnEvicted
				}
				if errors.Is(waitErr, context.Canceled) {
					*seq++
					emitFinalEvent(eventChan, *seq, startTime, "cancelled", "", 130)
					return turnEvicted
				}
				if errors.Is(waitErr, context.DeadlineExceeded) {
					*seq++
					emitFinalEvent(eventChan, *seq, startTime, "timed_out", "", 124)
					return turnEvicted
				}
				diagnostic, exitCode := ptyEOFDiagnostic(exitStatus, exitKnown)
				*seq++
				// Generic PTY termination has no completion evidence, but it is
				// still an adapter-owned terminal failure. Route it through the
				// shared classifier so unrecognised, sanitized EOF evidence is
				// explicitly typed as unknown. Recognised Claude fatal evidence
				// retains its existing more-specific class.
				emitClaudeFailureFinalEvent(eventChan, *seq, startTime, "failed", diagnostic, exitCode)
				return turnEvicted
			}
			if chunk.ReadError != nil {
				continue
			}
			// 1) answer Ink startup probes.
			probe.Feed(chunk.Bytes)
			// 2) feed the emulator and render the screen.
			frame, _ := emu.Feed(chunk.Bytes)
			// 3) detect a mid-turn fatal screen (error/limit/disconnect) and
			// surface it as a terminal final instead of waiting for the turn
			// timeout. The session is evicted so a reused slot never inherits a
			// broken process.
			if fs, diagnostic, ok := detectFatalScreen(frame, req.Prompt); ok {
				_ = te.conn.Kill()
				*seq++
				emitClaudeFailureFinalEvent(eventChan, *seq, startTime, fs.status, diagnostic, fs.exitCode)
				return turnEvicted
			}
			// 4) dismiss any first-run interstitial (folder-trust, theme/
			// onboarding, MCP/plugin trust, bypass-permissions warning) with its
			// default-accept keystroke (Enter), at most once each.
			if name := detectInterstitial(frame); name != "" && !answeredDialogs[name] {
				_ = te.conn.SendKey(session.KeyEnter)
				answeredDialogs[name] = true
			}
			// Once the prompt UI is ready (and no interstitial is showing), send
			// the prompt.
			if !promptSent && screenReadyForPrompt(frame) {
				sendPrompt()
			}
		}
	}
}

// waitForStopPayloadAfterPTYEOF waits only for the current turn's nonce-bound
// Stop payload. Process exit collection runs concurrently so a stuck Wait
// cannot extend the grace deadline.
func waitForStopPayloadAfterPTYEOF(
	ctx context.Context,
	te *turnEnv,
	turnDeadline <-chan time.Time,
) (path string, matched bool, status session.ExitStatus, statusKnown bool, err error) {
	// Cancellation and the enclosing turn deadline remain authoritative during
	// the grace window. Check them before observing a payload so a ready
	// terminal condition cannot lose a randomized select race to file polling.
	if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
		return "", false, status, statusKnown, terminalErr
	}
	if transcriptPath, ok := readStopHookPayloadNonce(te.stopPayloadPath, te.nonce); ok {
		if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
			return "", false, status, statusKnown, terminalErr
		}
		return transcriptPath, true, status, statusKnown, nil
	}

	grace := te.stopGrace
	if grace <= 0 {
		grace = 250 * time.Millisecond
	}
	pollInterval := te.pollInterval
	if pollInterval <= 0 || pollInterval > grace {
		pollInterval = grace
	}

	exitCh := make(chan session.ExitStatus, 1)
	go func() {
		exitCh <- te.conn.Wait()
	}()

	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", false, status, statusKnown, ctx.Err()
		case <-turnDeadline:
			if ctx.Err() != nil {
				return "", false, status, statusKnown, ctx.Err()
			}
			return "", false, status, statusKnown, context.DeadlineExceeded
		case exitStatus := <-exitCh:
			status = exitStatus
			statusKnown = true
			exitCh = nil
		case <-poll.C:
			if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
				return "", false, status, statusKnown, terminalErr
			}
			if transcriptPath, ok := readStopHookPayloadNonce(te.stopPayloadPath, te.nonce); ok {
				if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
					return "", false, status, statusKnown, terminalErr
				}
				return transcriptPath, true, status, statusKnown, nil
			}
		case <-timer.C:
			if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
				return "", false, status, statusKnown, terminalErr
			}
			// One final read closes the timer/publication boundary race.
			if transcriptPath, ok := readStopHookPayloadNonce(te.stopPayloadPath, te.nonce); ok {
				if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
					return "", false, status, statusKnown, terminalErr
				}
				return transcriptPath, true, status, statusKnown, nil
			}
			if terminalErr := eofGraceTerminalErr(ctx, turnDeadline); terminalErr != nil {
				return "", false, status, statusKnown, terminalErr
			}
			if !statusKnown {
				select {
				case status = <-exitCh:
					statusKnown = true
				default:
				}
			}
			return "", false, status, statusKnown, nil
		}
	}
}

func eofGraceTerminalErr(ctx context.Context, turnDeadline <-chan time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-turnDeadline:
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.DeadlineExceeded
	default:
	}
	return ctx.Err()
}

// ptyEOFDiagnostic retains only structured process status. It never includes a
// raw Wait error, terminal frame, transcript path, prompt, or account data.
func ptyEOFDiagnostic(status session.ExitStatus, known bool) (string, int) {
	const prefix = "Claude TUI PTY closed before Stop hook"
	if !known {
		return prefix + "; process exit status unavailable", 1
	}
	if status.Signaled {
		return prefix + "; process terminated by signal", 1
	}
	if status.Exited {
		exitCode := status.Code
		if exitCode <= 0 {
			exitCode = 1
		}
		return fmt.Sprintf("%s; process exit code %d", prefix, status.Code), exitCode
	}
	return prefix + "; process exit status unavailable", 1
}

const (
	transcriptPathFailureDiagnostic = "Claude transcript path could not be resolved"
	transcriptReadFailureDiagnostic = "Claude transcript could not be read"
	transcriptIncompleteDiagnostic  = "Claude transcript contained no assistant final event"
)

// emitTranscriptAndFinal reads the request-local transcript and emits exactly
// one final event. Missing or unreadable transcript evidence fails closed: a
// Stop hook proves only that Claude stopped, not that the turn succeeded.
func (h *Harness) emitTranscriptAndFinal(
	ctx context.Context,
	te *turnEnv,
	transcriptPath string,
	eventChan chan<- harnesses.Event,
	seq *int64,
	startTime time.Time,
	logger *slog.Logger,
) {
	if logger == nil {
		logger = slog.Default()
	}
	expanded, err := ExpandTranscriptPath(transcriptPath)
	if err == nil {
		tailer := NewTranscriptTailer(expanded, "default", logger)
		// Continue the harness sequence counter through the transcript events.
		tailer.seqCounter = *seq
		tailer.startTime = startTime
		readErr := tailer.ReadEvents(ctx, eventChan)
		// Preserve sequence continuity even when reading fails after emitting
		// partial transcript events.
		*seq = tailer.seqCounter
		if readErr == nil {
			if !tailer.emittedFinal {
				// Parser-level empty/incomplete transcripts intentionally do
				// not emit finals; the harness-level stream still must.
				*seq++
				emitFinalEvent(eventChan, *seq, startTime, "failed", transcriptIncompleteDiagnostic, 1)
			}
			return
		}
		if errors.Is(readErr, context.Canceled) {
			logger.Warn("Claude transcript read was cancelled")
			*seq++
			emitFinalEvent(eventChan, *seq, startTime, "cancelled", "", 130)
			return
		}
		if errors.Is(readErr, context.DeadlineExceeded) {
			logger.Warn("Claude transcript read timed out")
			*seq++
			emitFinalEvent(eventChan, *seq, startTime, "timed_out", "", 124)
			return
		}
		// The path and wrapped OS error can contain account names, prompts, or
		// credential material. Keep both logs and terminal evidence generic.
		logger.Warn("Claude transcript could not be read")
		*seq++
		emitFinalEvent(eventChan, *seq, startTime, "failed", transcriptReadFailureDiagnostic, 1)
		return
	} else {
		logger.Warn("Claude transcript path could not be resolved")
	}
	// Transcript path unavailable: emit exactly one failed final so the stream
	// closes without inventing completion text, usage, or cost evidence.
	*seq++
	emitFinalEvent(eventChan, *seq, startTime, "failed", transcriptPathFailureDiagnostic, 1)
}

// documentedRequestGaps returns one message per ExecuteRequest field that has
// no TUI affordance on the bypassPermissions path (documented gaps per
// ADR-013's capability baseline).
func documentedRequestGaps(req harnesses.ExecuteRequest) []string {
	var gaps []string
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
	emitFinalData(eventChan, seq, harnesses.FinalData{
		Status:          status,
		Error:           anthropic.SanitizeClaudeDiagnostic(errMsg),
		DurationMS:      time.Since(startTime).Milliseconds(),
		ExitCode:        exitCode,
		FinalCostSource: harnesses.CostSourceUnknown,
	})
}

func emitClaudeFailureFinalEvent(eventChan chan<- harnesses.Event, seq int64, startTime time.Time, status, evidence string, exitCode int) {
	failureClass, diagnostic := anthropic.ClassifyClaudeRouteFailure(evidence)
	emitFinalData(eventChan, seq, harnesses.FinalData{
		Status:          status,
		Error:           diagnostic,
		DurationMS:      time.Since(startTime).Milliseconds(),
		ExitCode:        exitCode,
		FinalCostSource: harnesses.CostSourceUnknown,
		RoutingActual: &harnesses.RoutingActual{
			Harness:      "claude-tui",
			FailureClass: failureClass,
		},
	})
}

func emitFinalData(eventChan chan<- harnesses.Event, seq int64, fd harnesses.FinalData) {
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
	preCmd := fmt.Sprintf(`cat > %s/tool-$(date +%%s%%N)-pre.json`, shellSingleQuote(toolDir))
	postCmd := fmt.Sprintf(`cat > %s/tool-$(date +%%s%%N)-post.json`, shellSingleQuote(toolDir))
	// Stop: capture stdin transcript path, then publish a nonce-bound payload
	// with a same-directory write-then-rename. Readers can observe either the
	// previous complete payload or the new complete payload, never a partial
	// JSON write.
	stopCmd := "exit 1"
	if isTurnNonce(nonce) {
		stopCmd = fmt.Sprintf(
			`umask 077; dest=%s; tmp="${dest}.tmp.$$"; trap 'rm -f "$tmp"' EXIT HUP INT TERM; p=$(cat) || exit 1; printf '%%s' "$p" | awk %s || exit 1; printf '%%s' "$p" | sed 's/}[[:space:]]*$/,"nonce":"%s"}/' > "$tmp" && mv -f "$tmp" "$dest"`,
			shellSingleQuote(stopPayloadPath), shellSingleQuote(stopTranscriptPathAWK), nonce,
		)
	}

	// Matcher "" is Claude Code's documented match-all form for PreToolUse/
	// PostToolUse/Stop (a NON-empty matcher is treated as a tool-name regex).
	// The prior "*" happened to match as a zero-width regex, but "" is the
	// schema-faithful match-all and is what the live 2.1.x engine documents.
	return map[string][]HookCommand{
		"PreToolUse":  {cmdHook("", preCmd)},
		"PostToolUse": {cmdHook("", postCmd)},
		"Stop":        {cmdHook("", stopCmd)},
	}
}

// stopTranscriptPathAWK validates that the native Stop JSON has exactly one
// top-level, non-empty string transcript_path before the trusted nonce is
// appended. It tracks strings and container depth so key-like user text or a
// nested decoy cannot authorize publication. Full JSON decoding still occurs
// in Go before the payload is consumed.
const stopTranscriptPathAWK = `
{
	text = text $0 "\n"
}
END {
	depth = 0
	mode = "root"
	for (i = 1; i <= length(text); i++) {
		c = substr(text, i, 1)
		if (in_string) {
			if (escaped) {
				token = token c
				escaped = 0
				continue
			}
			if (c == "\\") {
				token = token c
				escaped = 1
				continue
			}
			if (c == "\"") {
				in_string = 0
				if (role == "key" && depth == 1) {
					if (token ~ /\\/) invalid = 1
					key = token
					if (key == "transcript_path") seen++
					mode = "colon"
				} else if (role == "value" && depth == 1) {
					if (key == "transcript_path" && length(token) > 0) valid = 1
					key = ""
					mode = "after_value"
				}
				role = ""
				continue
			}
			token = token c
			continue
		}
		if (c == "\"") {
			in_string = 1
			escaped = 0
			token = ""
			if (depth == 1 && mode == "key") role = "key"
			else if (depth == 1 && mode == "value") role = "value"
			else role = "ignore"
			continue
		}
		if (c == "{" || c == "[") {
			old_depth = depth
			depth++
			if (depth == 1) mode = "key"
			else if (old_depth == 1 && mode == "value") mode = "nested_value"
			continue
		}
		if (c == "}" || c == "]") {
			depth--
			if (depth == 1 && mode == "nested_value") {
				mode = "after_value"
				key = ""
			}
			continue
		}
		if (depth != 1) continue
		if (mode == "colon") {
			if (c == ":") mode = "value"
			else if (c !~ /[[:space:]]/) invalid = 1
			continue
		}
		if (mode == "value") {
			if (c ~ /[[:space:]]/) continue
			mode = "scalar_value"
		}
		if ((mode == "after_value" || mode == "scalar_value") && c == ",") {
			mode = "key"
			key = ""
		}
	}
	exit(invalid || seen != 1 || !valid)
}`

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func isTurnNonce(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
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
	if !isTurnNonce(nonce) {
		return "", false
	}
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
		return fmt.Sprintf("%032x", uint64(time.Now().UnixNano()))
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
		// The startup card contains the active model before /model is sent.
		// Discard it so discovery evidence comes from the picker itself.
		ResetBeforeCommand: true,
		Timeout:            timeout,
		Size:               session.Size{Rows: 50, Cols: 220},
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
	lower := strings.ToLower(anthropic.StripANSI(strings.ReplaceAll(text, "\r\n", "\n")))
	return len(ParseClaudeTuiModels(text)) > 0 &&
		strings.Contains(lower, "enter to set") &&
		strings.Contains(lower, "esc to cancel")
}

var (
	// claudeFullFamilyVersionPattern matches the full `--model` ID form Claude
	// Code documents, e.g. `claude-opus-4-8` or `claude-sonnet-4.6`. It is
	// allowlisted to the real family stems so arbitrary `claude-<word>` tokens
	// (the harness name `claude-tui`, the temp hooks dir `claude-tui-hooks-NNN`,
	// or a git branch slug like `reliability/claude-tui-models`) can never be
	// admitted as a "model". This replaces the old bare `claude-[a-z0-9...]`
	// pattern that captured those tokens verbatim.
	claudeFullFamilyVersionPattern = regexp.MustCompile(`\bclaude-(sonnet|opus|haiku|fable)-([0-9]+)(?:[.-]([0-9]{1,2}))?(?:\b|-)`)
	// claudeFamilyVersionPattern matches the human-facing picker labels
	// (`Opus 4.8`, `Sonnet 5`, `Fable 5`, `Haiku 4.5`). The family/version separator is
	// OPTIONAL (`\s*`) because the live Claude Code PTY cell stream collapses
	// the space, rendering `Opus4.8`/`Sonnet4.6`/`Haiku4.5`; requiring `\s+`
	// dropped every version-bearing tier and left only bare aliases.
	claudeFamilyVersionPattern = regexp.MustCompile(`\b(?:claude\s+)?(sonnet|opus|haiku|fable)\s*([0-9]+(?:[.-][0-9]+)?)\b`)
	claudeAliasPattern         = regexp.MustCompile(`(?m)(?:^|[\s'"])(sonnet|opus|haiku|fable)(?:$|[\s'"])`)
	claudeEffortPattern        = regexp.MustCompile(`--effort\s+<level>.*\(([^)]*)\)`)
)

// ParseClaudeTuiModels extracts available model names from claude /model output.
// It only admits real, family-versioned IDs (sonnet/opus/haiku/fable + version) and
// the bare family aliases; arbitrary `claude-<word>` tokens are never emitted.
// Version-bearing IDs are normalized to the catalog claude-code surface form
// `<family>-<major>.<minor>` (e.g. opus-4.8, sonnet-4.6, haiku-4.5).
func ParseClaudeTuiModels(text string) []string {
	text = anthropic.StripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lower := strings.ToLower(text)
	var models []string
	for _, match := range claudeFullFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 3 {
			version := match[2]
			if match[3] != "" {
				version += "." + match[3]
			}
			models = appendUniqueString(models, match[1]+"-"+version)
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

type claudeTUIEnvironmentDisposition uint8

const (
	claudeTUIEnvironmentExcluded claudeTUIEnvironmentDisposition = iota
	claudeTUIEnvironmentInherited
	claudeTUIEnvironmentActivationGenerated
)

func classifyClaudeTUIEnvironment(name string) claudeTUIEnvironmentDisposition {
	switch name {
	case "TZ",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN",
		"CLAUDE_CODE_OAUTH_SCOPES",
		"CLAUDE_CODE_DEBUG_LOG_LEVEL":
		return claudeTUIEnvironmentInherited
	case "HOME", "PATH", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "LC_ALL",
		"CLAUDE_CONFIG_DIR",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR":
		return claudeTUIEnvironmentActivationGenerated
	default:
		return claudeTUIEnvironmentExcluded
	}
}

func claudeTUIPortableInheritedEnvironmentNames() []string {
	var names []string
	for _, assignment := range os.Environ() {
		name := strings.SplitN(assignment, "=", 2)[0]
		if classifyClaudeTUIEnvironment(name) == claudeTUIEnvironmentInherited {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// BuildEnvironmentAllowlist constructs the environment for the PTY session
// according to ADR-013 §Environment Allowlist. Portable preparation consumes
// the same classifier but regenerates host-path platform keys in the guest.
func BuildEnvironmentAllowlist() []string {
	var env []string
	currentEnv := os.Environ()

	// Pass through allowed variables from the operator environment
	for _, kv := range currentEnv {
		key := strings.SplitN(kv, "=", 2)[0]

		if classifyClaudeTUIEnvironment(key) != claudeTUIEnvironmentExcluded {
			env = append(env, kv)
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

// interstitialDialog is one first-run/blocking dialog the turn loop dismisses
// with its default-accept keystroke. On a truly fresh profile any of these can
// sit on screen blocking the ready prompt; left unhandled, the startup grace
// would paste the prompt INTO the dialog and the turn would run to the turn
// timeout. Each is dismissed by pressing Enter (the default highlighted choice
// — "Yes, I trust", "1. Dark mode", "Yes, proceed", "I accept", etc.).
//
// Each dialog is answered AT MOST ONCE per turn (tracked by name) so we do not
// re-press Enter on a screen that lingers a few frames after acceptance.
type interstitialDialog struct {
	name    string
	markers []string
}

// interstitials enumerates the blocking first-run dialogs handled on the turn
// path, mirroring the folder-trust handler. Markers are matched against the
// vt10x-rendered frame text (NOT raw bytes).
var interstitials = []interstitialDialog{
	{name: "folder-trust", markers: folderTrustMarkers},
	{name: "theme-onboarding", markers: []string{
		"Choose the text style that looks best",
		"Choose the option that looks best",
		"Dark mode",
		"Light mode",
	}},
	{name: "mcp-trust", markers: []string{
		"wants to use the following MCP servers",
		"trust this server",
		"Use this MCP server",
	}},
	{name: "plugin-trust", markers: []string{
		"wants to use the following plugins",
		"trust this plugin",
		"Use this plugin",
	}},
	{name: "bypass-warning", markers: []string{
		"Bypass Permissions mode",
		"Bypassing Permissions",
		"WARNING: Claude Code running in Bypass Permissions",
		"I accept",
	}},
}

// screenHasFolderTrustDialog reports whether the rendered frame shows the
// folder-trust dialog (answered with Enter = default "Yes, I trust...").
// Retained for the targeted folder-trust regression test.
func screenHasFolderTrustDialog(frame terminal.Frame) bool {
	return frameHasAnyMarker(frame, folderTrustMarkers)
}

// detectInterstitial returns the name of the first blocking dialog visible on
// the rendered frame, or "" when none is present.
func detectInterstitial(frame terminal.Frame) string {
	for _, d := range interstitials {
		if frameHasAnyMarker(frame, d.markers) {
			return d.name
		}
	}
	return ""
}

func frameHasAnyMarker(frame terminal.Frame, markers []string) bool {
	joined := strings.Join(frame.Text, "\n")
	for _, m := range markers {
		if strings.Contains(joined, m) {
			return true
		}
	}
	return false
}

// screenReadyForPrompt reports whether the rendered frame shows the Claude
// input prompt (the "❯"/"> " affordance) so the prompt can be pasted. It
// returns false while ANY first-run interstitial is on screen so the prompt is
// never pasted into a blocking dialog.
func screenReadyForPrompt(frame terminal.Frame) bool {
	if detectInterstitial(frame) != "" {
		return false
	}
	joined := strings.Join(frame.Text, "\n")
	return strings.Contains(joined, "❯") || strings.Contains(joined, "> ")
}

// fatalScreen is a mid-turn error/limit/disconnect screen the turn loop must
// surface as a terminal final instead of absorbing into the turn timeout.
type fatalScreen struct {
	// status is the CONTRACT-003 final status emitted when this screen matches.
	status string
	// markers are matched against the vt10x-rendered frame text.
	markers []string
	// exitCode is the synthetic exit code for the final.
	exitCode int
}

// fatalScreens enumerates the mid-turn failure screens the loop detects. The
// usage-limit screen maps to iteration_limit (a quota/limit signal, distinct
// from a crash); login/disconnect/error screens map to failed. Each match
// terminates the request-local session so no broken process survives the
// invocation.
var fatalScreens = []fatalScreen{
	{
		status:   "iteration_limit",
		markers:  []string{"usage limit reached", "Claude usage limit reached", "approaching usage limit", "out of free messages", "Credit balance is too low"},
		exitCode: 1,
	},
	{
		status:   "failed",
		markers:  []string{"Please run /login", "Invalid API key", "authentication_error", "OAuth token has expired", "Failed to authenticate", "Could not refresh auth token"},
		exitCode: 1,
	},
	{
		status:   "failed",
		markers:  []string{"Connection error", "network error", "fetch failed", "ECONNREFUSED", "service is temporarily unavailable", "Overloaded"},
		exitCode: 1,
	},
}

// detectFatalScreen returns the first fatal screen backed by non-prompt frame
// evidence. Claude renders the submitted prompt in the same terminal frame as
// failures, so prompt lines must be removed before marker matching; otherwise
// a user merely discussing an error string could terminate their own turn.
func detectFatalScreen(frame terminal.Frame, prompt string) (fatalScreen, string, bool) {
	for _, fs := range fatalScreens {
		if lines := matchedFatalLines(frame, fs.markers, prompt); len(lines) > 0 {
			return fs, strings.Join(lines, "\n"), true
		}
	}
	return fatalScreen{}, "", false
}

// matchedFatalLines returns only rendered lines that carry a marker for the
// selected fatal-screen class. The full frame can include the user's prompt,
// prior output, or unrelated account information and is never retained.
func matchedFatalLines(frame terminal.Frame, markers []string, prompt string) []string {
	var matched []string
	promptRows := promptEchoLineIndexes(frame, prompt)
	for index, line := range frame.Text {
		if _, isPromptRow := promptRows[index]; isPromptRow {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || renderedLineAppearsInPrompt(trimmed, prompt) {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, marker := range markers {
			if strings.Contains(lower, strings.ToLower(marker)) && isUIOwnedFatalLine(trimmed, marker) {
				matched = append(matched, trimmed)
				break
			}
		}
	}
	return matched
}

func isUIOwnedFatalLine(line, marker string) bool {
	normalizedLine := strings.ToLower(normalizeTerminalText(line))
	// Claude Code 2.1.212 renders request failures as a bulleted status line,
	// for example "● Please run /login · API Error: 401 ...". The bullet is
	// fixed UI decoration, not caller or account evidence; remove only that
	// observed prefix before applying the existing ownership checks.
	normalizedLine = strings.TrimSpace(strings.TrimPrefix(normalizedLine, "●"))
	normalizedMarker := strings.ToLower(normalizeTerminalText(marker))
	for _, prefix := range []string{
		"api error:", "authentication error:", "oauth error:",
		"quota error:", "usage error:", "network error:", "connection error:",
	} {
		if strings.HasPrefix(normalizedLine, prefix) {
			return true
		}
	}
	if !strings.HasPrefix(normalizedLine, normalizedMarker) {
		return false
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(normalizedLine, normalizedMarker))
	if remainder == "" {
		return true
	}
	switch []rune(remainder)[0] {
	case ':', '.', '!', '-', '—', '·', '(', '[':
		return true
	default:
		return false
	}
}

// renderedLineAppearsInPrompt covers clipped or scrolled input frames where
// Claude's prompt glyph and the first input row are no longer visible. Safety
// wins over early detection when a rendered line exactly repeats any
// normalized span of caller input; UI-owned error prefixes distinguish real
// fatal evidence in otherwise ambiguous frames.
func renderedLineAppearsInPrompt(line, prompt string) bool {
	normalizedLine := normalizeTerminalText(line)
	for _, prefix := range []string{"❯", ">"} {
		if strings.HasPrefix(normalizedLine, prefix) {
			normalizedLine = normalizeTerminalText(strings.TrimPrefix(normalizedLine, prefix))
			break
		}
	}
	return normalizedLine != "" && strings.Contains(normalizeTerminalText(prompt), normalizedLine)
}

// promptEchoLineIndexes identifies only the contiguous rendered input rows
// beginning at Claude's prompt glyph. Wrapped continuation rows are excluded
// while their accumulated text remains a prefix of the submitted prompt. This
// avoids globally suppressing a real fatal line merely because the same words
// also appeared somewhere in caller input.
func promptEchoLineIndexes(frame terminal.Frame, prompt string) map[int]struct{} {
	rows := make(map[int]struct{})
	normalizedPrompt := normalizeTerminalText(prompt)
	if normalizedPrompt == "" {
		return rows
	}
	for start, rawLine := range frame.Text {
		line := normalizeTerminalText(rawLine)
		var first string
		switch {
		case strings.HasPrefix(line, "❯"):
			first = normalizeTerminalText(strings.TrimPrefix(line, "❯"))
		case strings.HasPrefix(line, ">"):
			first = normalizeTerminalText(strings.TrimPrefix(line, ">"))
		default:
			continue
		}
		if first == "" || !strings.HasPrefix(normalizedPrompt, first) {
			continue
		}

		candidate := first
		rows[start] = struct{}{}
		for index := start + 1; index < len(frame.Text) && candidate != normalizedPrompt; index++ {
			fragment := normalizeTerminalText(frame.Text[index])
			if fragment == "" {
				break
			}
			next := candidate + " " + fragment
			if compact := candidate + fragment; strings.HasPrefix(normalizedPrompt, compact) {
				next = compact
			} else if !strings.HasPrefix(normalizedPrompt, next) {
				break
			}
			candidate = next
			rows[index] = struct{}{}
		}
	}
	return rows
}

func normalizeTerminalText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
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
	return RunTurnForTestWithStopGrace(
		ctx, conn, req, hookDir, stopPayloadPath, nonce,
		readyTimeout, pollInterval, 5*pollInterval, turnTimeout,
	)
}

// RunTurnForTestWithStopGrace is the explicit-duration form used by EOF/Stop
// ordering tests. Keeping the grace request-local avoids package-global timing
// seams that can race under parallel tests.
func RunTurnForTestWithStopGrace(
	ctx context.Context,
	conn TestPTYConn,
	req harnesses.ExecuteRequest,
	hookDir, stopPayloadPath, nonce string,
	readyTimeout, pollInterval, stopGrace, turnTimeout time.Duration,
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
		stopGrace:       stopGrace,
		turnTimeout:     turnTimeout,
		logger:          slog.Default(),
	}
	go func() {
		h.runTurnOver(ctx, &te, req, eventChan, &seq, start)
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
	Wait() session.ExitStatus
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
