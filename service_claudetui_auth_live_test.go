//go:build live_harness

package fizeau_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	fizeau "github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/processlifecycle"
)

const liveClaudeTUIAuthFailureGate = "FIZEAU_TEST_LIVE_CLAUDE_TUI_AUTH_FAILURE"

var (
	liveEmailPattern   = regexp.MustCompile(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\b`)
	liveAccountPattern = regexp.MustCompile(`(?i)\bacct[-_][a-z0-9_-]+\b`)
)

// TestLiveServiceClaudeTUIRejectedOAuthFailsTypedExactlyOnce is the opt-in
// release canary for a real Claude TUI authentication rejection. Once enabled,
// every missing prerequisite or inconclusive result is a failure, never a skip.
func TestLiveServiceClaudeTUIRejectedOAuthFailsTypedExactlyOnce(t *testing.T) {
	if os.Getenv(liveClaudeTUIAuthFailureGate) != "1" {
		t.Skip(liveClaudeTUIAuthFailureGate + " is not 1; skipping live authentication-failure canary")
	}
	if runtime.GOOS != "linux" {
		t.Fatalf("enabled live authentication-failure canary requires Linux file-backed credential isolation; GOOS=%s", runtime.GOOS)
	}
	liveRequireNoManagedClaudePolicy(t)

	preIsolationValues := liveEnvironmentValues(
		"HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR",
		"USER", "LOGNAME", "HOSTNAME",
	)
	if hostname, err := os.Hostname(); err == nil {
		preIsolationValues = append(preIsolationValues, hostname)
	}
	if operator, err := user.Current(); err == nil {
		preIsolationValues = append(preIsolationValues, operator.Username, operator.Name, operator.HomeDir)
	}

	claudeFromPath, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("live authentication-failure canary enabled but claude is unavailable: %v", err)
	}
	claudeExecutable, err := filepath.EvalSymlinks(claudeFromPath)
	if err != nil {
		t.Fatalf("resolve claude executable: %v", err)
	}
	claudeInfoBefore, err := os.Stat(claudeExecutable)
	if err != nil {
		t.Fatalf("stat claude executable: %v", err)
	}
	claudeDigestBefore, err := liveFileSHA256(claudeExecutable)
	if err != nil {
		t.Fatalf("hash claude executable: %v", err)
	}
	isolationRoot := t.TempDir()
	homeDir := filepath.Join(isolationRoot, "home")
	configDir := filepath.Join(isolationRoot, "claude-config")
	workDir := filepath.Join(isolationRoot, "work")
	serviceLogDir := filepath.Join(isolationRoot, "service-logs")
	binDir := filepath.Join(isolationRoot, "bin")
	xdgConfigDir := filepath.Join(isolationRoot, "xdg-config")
	xdgDataDir := filepath.Join(isolationRoot, "xdg-data")
	xdgCacheDir := filepath.Join(isolationRoot, "xdg-cache")
	xdgStateDir := filepath.Join(isolationRoot, "xdg-state")
	xdgRuntimeDir := filepath.Join(isolationRoot, "xdg-runtime")
	claudeTempDir := filepath.Join(isolationRoot, "claude-tmp")
	for _, dir := range []string{
		homeDir, configDir, workDir, serviceLogDir, binDir,
		xdgConfigDir, xdgDataDir, xdgCacheDir, xdgStateDir, xdgRuntimeDir, claudeTempDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated live-test directory: %v", err)
		}
	}
	isolatedClaude := filepath.Join(binDir, "claude")
	if err := os.Symlink(claudeExecutable, isolatedClaude); err != nil {
		t.Fatalf("pin isolated claude executable: %v", err)
	}

	const (
		promptSentinel     = "fizeau-live-auth-prompt-sentinel-must-not-leak"
		frameSentinel      = "fizeau-live-auth-frame-sentinel-must-not-leak"
		transcriptSentinel = "fizeau-live-auth-transcript-sentinel-must-not-leak"
		isolatedUser       = "fizeau-live-account-sentinel"
	)
	var tokenEntropy [32]byte
	if _, err := rand.Read(tokenEntropy[:]); err != nil {
		t.Fatalf("generate unique invalid OAuth token: %v", err)
	}
	invalidToken := "sk-ant-oat01-" + hex.EncodeToString(tokenEntropy[:])
	prompt := strings.Join([]string{promptSentinel, frameSentinel, transcriptSentinel}, " ")

	settings := map[string]any{
		"theme":                             "dark",
		"skipDangerousModePermissionPrompt": true,
		"env": map[string]string{
			"DISABLE_UPDATES":                                      "1",
			"DISABLE_AUTOUPDATER":                                  "1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":             "1",
			"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1",
			"CLAUDE_CODE_SKIP_PROMPT_HISTORY":                      "1",
			"CLAUDE_CODE_TMPDIR":                                   claudeTempDir,
		},
	}
	liveWriteJSONFile(t, filepath.Join(configDir, "settings.json"), settings)
	liveWriteJSONFile(t, filepath.Join(configDir, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
		"projects": map[string]any{
			workDir: map[string]any{"hasTrustDialogAccepted": true},
		},
	})

	minimalEnvironment := map[string]string{
		"HOME": homeDir, "USERPROFILE": homeDir,
		"USER": isolatedUser, "LOGNAME": isolatedUser,
		"CLAUDE_CONFIG_DIR": configDir,
		"XDG_CONFIG_HOME":   xdgConfigDir, "XDG_DATA_HOME": xdgDataDir,
		"XDG_CACHE_HOME": xdgCacheDir, "XDG_STATE_HOME": xdgStateDir, "XDG_RUNTIME_DIR": xdgRuntimeDir,
		"PATH": liveIsolatedPath(binDir), "SHELL": "/bin/sh", "TERM": "xterm-256color",
		"LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "TZ": "UTC",
		"CLAUDE_CODE_OAUTH_TOKEN": invalidToken,
		"DISABLE_UPDATES":         "1", "DISABLE_AUTOUPDATER": "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":             "1",
		"CLAUDE_CODE_DISABLE_OFFICIAL_MARKETPLACE_AUTOINSTALL": "1",
	}
	liveReplaceEnvironment(t, minimalEnvironment)
	resolvedIsolatedClaude, err := exec.LookPath("claude")
	if err != nil || filepath.Clean(resolvedIsolatedClaude) != filepath.Clean(isolatedClaude) {
		t.Fatal("isolated PATH did not select the pinned claude executable")
	}
	isolatedTarget, err := filepath.EvalSymlinks(resolvedIsolatedClaude)
	if err != nil || filepath.Clean(isolatedTarget) != filepath.Clean(claudeExecutable) {
		t.Fatal("isolated claude launcher did not resolve to the recorded executable")
	}
	claudeVersionBefore, err := liveClaudeVersion(claudeExecutable, workDir)
	if err != nil {
		t.Fatalf("read isolated claude version: %v", err)
	}

	stateDir, err := processlifecycle.StateDirectory(serviceLogDir)
	if err != nil {
		t.Fatalf("resolve lifecycle state directory: %v", err)
	}
	registry := processlifecycle.NewFileRegistry(stateDir)
	liveRequireEmptyLifecycleRegistry(t, registry, "before Execute")

	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	cancelRefresh()
	const cleanupTimeout = 10 * time.Second
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:         &stubServiceConfig{},
		QuotaRefreshContext:   refreshCtx,
		SessionLogDir:         serviceLogDir,
		HarnessCleanupTimeout: cleanupTimeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const requestTimeout = 30 * time.Second
	executeCtx, cancelExecute := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancelExecute()
	events, err := svc.Execute(executeCtx, fizeau.ServiceExecuteRequest{
		Prompt:        prompt,
		Harness:       "claude-tui",
		Model:         "sonnet-4.6",
		WorkDir:       workDir,
		Permissions:   "unrestricted",
		Timeout:       requestTimeout,
		SessionLogDir: serviceLogDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var (
		decodedEvents      []fizeau.ServiceDecodedEvent
		final              *fizeau.ServiceFinalData
		routingDecision    *fizeau.ServiceRoutingDecisionData
		rawFinalCount      int
		typedFinalCount    int
		eventsAfterFinal   int
		finalRegistryError error
		finalRegistryCount int
		streamProblem      string
	)
	deadline := executeCtx.Done()
	var drainTimer *time.Timer
	var drainDeadline <-chan time.Time
	for events != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if rawFinalCount > 0 {
				eventsAfterFinal++
			}
			if string(event.Type) == fizeau.ServiceEventTypeFinal {
				rawFinalCount++
				registryCtx, cancelRegistry := context.WithTimeout(context.Background(), 2*time.Second)
				records, listErr := registry.List(registryCtx)
				cancelRegistry()
				finalRegistryError = listErr
				finalRegistryCount = len(records)
			}
			decoded, decodeErr := fizeau.DecodeServiceEvent(event)
			if decodeErr != nil {
				if streamProblem == "" {
					streamProblem = "public event decode failed"
				}
				cancelExecute()
				continue
			}
			decodedEvents = append(decodedEvents, decoded)
			if decoded.RoutingDecision != nil {
				routingDecision = decoded.RoutingDecision
			}
			if decoded.Final != nil {
				typedFinalCount++
				if final == nil {
					final = decoded.Final
				}
			}
		case <-deadline:
			if streamProblem == "" {
				streamProblem = "outer execution deadline elapsed before stream closure"
			}
			cancelExecute()
			deadline = nil
			drainTimer = time.NewTimer(2*cleanupTimeout + 5*time.Second)
			drainDeadline = drainTimer.C
		case <-drainDeadline:
			t.Fatal("execute stream did not close after cancellation and both cleanup windows")
		}
	}
	if drainTimer != nil {
		drainTimer.Stop()
	}
	if streamProblem != "" {
		t.Fatal(streamProblem)
	}

	liveRequireEmptyLifecycleRegistry(t, registry, "after stream closure")
	claudeInfoAfter, err := os.Stat(claudeExecutable)
	if err != nil {
		t.Fatalf("stat claude executable after canary: %v", err)
	}
	claudeDigestAfter, err := liveFileSHA256(claudeExecutable)
	if err != nil {
		t.Fatalf("hash claude executable after canary: %v", err)
	}
	if !os.SameFile(claudeInfoBefore, claudeInfoAfter) || claudeDigestBefore != claudeDigestAfter {
		t.Fatal("claude executable identity or digest changed during live canary")
	}
	claudeVersionAfter, err := liveClaudeVersion(claudeExecutable, workDir)
	if err != nil || claudeVersionAfter != claudeVersionBefore {
		t.Fatal("claude executable version changed or became unreadable during live canary")
	}
	isolatedTargetAfter, err := filepath.EvalSymlinks(isolatedClaude)
	if err != nil || filepath.Clean(isolatedTargetAfter) != filepath.Clean(claudeExecutable) {
		t.Fatal("isolated claude launcher target changed during live canary")
	}
	t.Logf("live claude-tui auth canary: version=%s sha256=%s", claudeVersionBefore, claudeDigestBefore)

	if rawFinalCount != 1 || typedFinalCount != 1 || final == nil {
		t.Fatalf("public final counts = raw %d typed %d, want exactly one of each after stream closure", rawFinalCount, typedFinalCount)
	}
	if eventsAfterFinal != 0 || len(decodedEvents) == 0 || decodedEvents[len(decodedEvents)-1].Final == nil {
		t.Fatalf("events after Final = %d, want Final to be the last event before closure", eventsAfterFinal)
	}
	if finalRegistryError != nil {
		t.Fatalf("read lifecycle registry at Final: %v", finalRegistryError)
	}
	if finalRegistryCount != 0 {
		t.Fatalf("lifecycle registry held %d record(s) when Final arrived", finalRegistryCount)
	}
	diagnosticClass, diagnosticSanitized := anthropic.ClassifyClaudeRouteFailure(final.Error)
	t.Logf("live claude-tui auth evidence: class=%s bytes=%d already_sanitized=%t", diagnosticClass, len(final.Error), diagnosticSanitized == final.Error)
	if routingDecision == nil || routingDecision.Harness != "claude-tui" || routingDecision.SessionID == "" {
		t.Fatalf("selected route = %+v, want accepted claude-tui session", routingDecision)
	}
	if final.Status != "failed" || final.Outcome != fizeau.SessionOutcomeFailed ||
		final.Cause != fizeau.TerminalCauseHarnessFailed || final.Stage != fizeau.SessionStageHarness {
		t.Fatalf("terminal tuple = status %q outcome %q cause %q stage %q, want failed/failed/harness_failed/harness",
			final.Status, final.Outcome, final.Cause, final.Stage)
	}
	if final.PrimaryOutcome != "" || final.PrimaryCause != "" || final.PrimaryStage != "" {
		t.Fatal("normal cleanup fabricated cleanup-supersession primary terminal fields")
	}
	if final.ExitCode == 0 {
		t.Fatal("live authentication failure exit code is zero")
	}
	if final.RoutingActual == nil || final.RoutingActual.Harness != "claude-tui" ||
		final.RoutingActual.FailureClass != anthropic.FailureClassCredentialInvalid {
		t.Fatalf("routing actual = %+v, want claude-tui credential_invalid", final.RoutingActual)
	}
	if final.FinalText != "" || final.Usage != nil || final.CostUSD != nil ||
		final.CostSource != fizeau.CostSourceUnknown {
		t.Fatal("live authentication failure fabricated text, usage, or cost evidence")
	}
	for _, event := range decodedEvents {
		if event.TextDelta != nil || event.ToolCall != nil || event.ToolResult != nil {
			t.Fatal("live authentication failure emitted text or tool evidence")
		}
	}

	diagnostic := final.Error
	if diagnostic == "" || len(diagnostic) > anthropic.MaxRouteFailureDiagnosticBytes || !utf8.ValidString(diagnostic) {
		t.Fatal("live authentication diagnostic is empty, invalid, or exceeds the public bound")
	}
	failureClass, sanitizedDiagnostic := anthropic.ClassifyClaudeRouteFailure(diagnostic)
	if failureClass != anthropic.FailureClassCredentialInvalid || sanitizedDiagnostic != diagnostic {
		t.Fatal("live authentication diagnostic is not classifier-approved and already sanitized")
	}
	for lineNumber, line := range strings.Split(diagnostic, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !liveContainsCredentialMarker(line) {
			t.Fatalf("live authentication diagnostic line %d lacks an approved credential marker", lineNumber+1)
		}
		lineClass, _ := anthropic.ClassifyClaudeRouteFailure(line)
		if lineClass != anthropic.FailureClassCredentialInvalid {
			t.Fatalf("live authentication diagnostic line %d is not independently credential-invalid", lineNumber+1)
		}
	}
	lowerDiagnostic := strings.ToLower(diagnostic)
	if strings.Contains(diagnostic, "\x1b") || strings.Contains(lowerDiagnostic, "pty closed before stop hook") ||
		strings.Contains(lowerDiagnostic, "session closed before stop") || strings.Contains(lowerDiagnostic, "unexpected eof") ||
		strings.Contains(lowerDiagnostic, "deadline") || strings.Contains(lowerDiagnostic, "timed out") {
		t.Fatal("live authentication diagnostic retained terminal framing or generic EOF evidence")
	}
	for _, sensitive := range liveSensitiveNeedles(append(preIsolationValues,
		invalidToken, promptSentinel, frameSentinel, transcriptSentinel, prompt,
		isolationRoot, homeDir, configDir, workDir, serviceLogDir, binDir,
		xdgConfigDir, xdgDataDir, xdgCacheDir, xdgStateDir, xdgRuntimeDir, claudeTempDir,
		claudeFromPath, claudeExecutable, isolatedClaude, isolatedUser, final.SessionLogPath,
	)...) {
		if liveContainsSensitiveValue(diagnostic, sensitive) {
			t.Fatal("live authentication diagnostic retained credential, path, account, prompt, frame, or transcript material")
		}
	}
	if liveEmailPattern.MatchString(diagnostic) || liveAccountPattern.MatchString(diagnostic) {
		t.Fatal("live authentication diagnostic retained an email address or account identifier")
	}
	for _, credentialPath := range []string{
		filepath.Join(configDir, ".credentials.json"),
		filepath.Join(homeDir, ".claude", ".credentials.json"),
	} {
		if _, err := os.Stat(credentialPath); err == nil || !os.IsNotExist(err) {
			t.Fatal("live authentication canary created or could not exclude an isolated credential file")
		}
	}
}

func liveContainsCredentialMarker(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range []string{
		"failed to authenticate", "could not refresh auth token", "authentication_error",
		"invalid api key", "invalid x-api-key", "oauth token has expired", "oauth token expired",
		"please run /login", "unauthorized", `"status":401`, "401 unauthorized",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func liveRequireNoManagedClaudePolicy(t *testing.T) {
	t.Helper()
	for _, path := range []string{
		"/etc/claude-code/managed-settings.json",
		"/etc/claude-code/managed-mcp.json",
	} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("enabled live canary requires no system Claude policy; found %s", filepath.Base(path))
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect system Claude policy %s: %v", filepath.Base(path), err)
		}
	}
	entries, err := filepath.Glob("/etc/claude-code/managed-settings.d/*.json")
	if err != nil {
		t.Fatalf("inspect managed Claude settings directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatal("enabled live canary requires an empty system managed-settings.d directory")
	}
}

func liveReplaceEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	original := append([]string(nil), os.Environ()...)
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, assignment := range original {
			parts := strings.SplitN(assignment, "=", 2)
			if len(parts) == 2 {
				if err := os.Setenv(parts[0], parts[1]); err != nil {
					t.Errorf("restore environment variable %s: %v", parts[0], err)
				}
			}
		}
	})
	for name, value := range values {
		if err := os.Setenv(name, value); err != nil {
			t.Fatalf("seed isolated environment variable %s: %v", name, err)
		}
	}
}

func liveClaudeVersion(path, workDir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Dir = workDir
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude --version failed: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", fmt.Errorf("claude --version returned empty output")
	}
	return version, nil
}

func liveEnvironmentValues(names ...string) []string {
	values := make([]string, 0, len(names))
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func liveFileSHA256(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- live test hashes the resolved executable selected by the operator.
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func liveIsolatedPath(binDir string) string {
	paths := []string{binDir}
	for _, path := range []string{"/usr/bin", "/bin"} {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func liveRequireEmptyLifecycleRegistry(t *testing.T, registry *processlifecycle.FileRegistry, boundary string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	records, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("read lifecycle registry %s: %v", boundary, err)
	}
	if len(records) != 0 {
		t.Fatalf("lifecycle registry held %d record(s) %s", len(records), boundary)
	}
}

func liveSensitiveNeedles(values ...string) []string {
	needles := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "/" || value == "." {
			continue
		}
		duplicate := false
		for _, existing := range needles {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			needles = append(needles, value)
		}
	}
	return needles
}

func liveContainsSensitiveValue(diagnostic, value string) bool {
	if len(value) >= 4 {
		return strings.Contains(diagnostic, value)
	}
	boundaryPattern := `(?i)(^|[^[:alnum:]_.-])` + regexp.QuoteMeta(value) + `($|[^[:alnum:]_.-])`
	return regexp.MustCompile(boundaryPattern).FindStringIndex(diagnostic) != nil
}

func liveWriteJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode isolated Claude state: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write isolated Claude state: %v", err)
	}
}
