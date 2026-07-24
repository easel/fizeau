package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/pty/session"
)

type claudeTUILaunchObservation struct {
	executable  string
	args        []string
	workdir     string
	env         []string
	size        session.Size
	optionCount int
}

// TestExecuteDefaultClaudeUnrestrictedLaunchesTUIYolo joins default-policy
// routing and the production claude-tui launch boundary in one Service.Execute
// call. The starter replacement returns a sentinel at session.Start, keeping the test
// independent of a live PTY, Claude authentication, and network access.
func TestExecuteDefaultClaudeUnrestrictedLaunchesTUIYolo(t *testing.T) {
	t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", "")
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(homeDir, ".claude"))

	binDir := t.TempDir()
	claudeName := "claude"
	claudeMode := os.FileMode(0o700)
	claudeContents := []byte("#!/bin/sh\nexit 99\n")
	if runtime.GOOS == "windows" {
		claudeName = "claude.exe"
		claudeMode = 0o600
		claudeContents = []byte("not executed: starter replacement stops process startup")
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	claudePath := filepath.Join(binDir, claudeName)
	if err := os.WriteFile(claudePath, claudeContents, claudeMode); err != nil {
		t.Fatalf("write fake claude executable: %v", err)
	}
	t.Setenv("PATH", binDir)

	launches := make(chan claudeTUILaunchObservation, 2)
	launchSentinel := errors.New("test stopped claude-tui before PTY startup")
	// The starter registry matches this exact unique temp-dir path, so this
	// seam cannot intercept an unrelated parallel execution of another claude.
	restoreStarter := harnesses.RegisterPTYSessionStarterForTest("claude-tui", claudePath, func(
		_ context.Context,
		executable string,
		args []string,
		workdir string,
		env []string,
		size session.Size,
		opts ...session.Option,
	) (*session.Session, error) {
		launches <- claudeTUILaunchObservation{
			executable:  executable,
			args:        args,
			workdir:     workdir,
			env:         env,
			size:        size,
			optionCount: len(opts),
		}
		return nil, launchSentinel
	})
	t.Cleanup(restoreStarter)

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-14T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  claude-sonnet-fixture:
    family: claude-sonnet
    status: active
    power: 8
    surfaces:
      claude-code: sonnet-fixture
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	cancelRefresh()
	public, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		QuotaRefreshContext: refreshCtx,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := public.(*service)
	svc.registry = harnesses.NewRegistryForTest("claude", "claude-tui")
	forceAvailableHarnessesForTest(t, svc, "claude", "claude-tui")

	dispatched := make(chan string, 1)
	svc.subprocessDispatchObserver = func(runner harnesses.Harness) {
		dispatched <- runner.Info().Name
	}

	events, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:      "exercise the default Claude route and launch",
		Policy:      "default",
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	drained := drainUnifiedServiceEvents(t, events, 5*time.Second)

	var decision *ServiceRoutingDecisionData
	for _, event := range drained {
		if event.Type != ServiceEventTypeRoutingDecision {
			continue
		}
		payload := decodeRoutingDecisionEvent(t, event)
		decision = &payload
		break
	}
	if decision == nil {
		t.Fatalf("missing routing_decision event: %#v", drained)
	}
	if decision.RequestedHarness != "" || decision.RequestedPolicy != "default" || decision.Permissions != "unrestricted" {
		t.Fatalf("routing request = harness %q policy %q permissions %q; want unpinned/default/unrestricted",
			decision.RequestedHarness, decision.RequestedPolicy, decision.Permissions)
	}
	if decision.Harness != "claude-tui" {
		t.Fatalf("routing_decision harness = %q, want claude-tui", decision.Harness)
	}
	if decision.Model != "sonnet-fixture" || !strings.HasPrefix(strings.ToLower(decision.Model), "sonnet-") {
		t.Fatalf("routing_decision model = %q, want the fixture sonnet-family surface", decision.Model)
	}
	var interceptedFinal harnesses.FinalData
	finalCount := 0
	finalIndex := -1
	for i, event := range drained {
		if event.Type != ServiceEventTypeFinal {
			continue
		}
		finalCount++
		finalIndex = i
		interceptedFinal = decodeFinalEvent(t, event)
	}
	if finalCount != 1 {
		t.Fatalf("final event count = %d, want exactly one; events=%#v", finalCount, drained)
	}
	if finalIndex != len(drained)-1 {
		t.Fatalf("final event index = %d, want last index %d", finalIndex, len(drained)-1)
	}
	if !strings.Contains(interceptedFinal.Error, launchSentinel.Error()) {
		t.Fatalf("final = %#v, want failed-start sentinel %q", interceptedFinal, launchSentinel)
	}
	if len(decision.Candidates) != 2 {
		t.Fatalf("routing candidates = %#v, want exactly two rows", decision.Candidates)
	}
	seen := map[string]int{}
	for _, candidate := range decision.Candidates {
		if !candidate.Eligible {
			t.Fatalf("routing candidate = %#v, want both rows eligible", candidate)
		}
		seen[candidate.Harness]++
	}
	if seen["claude"] != 1 || seen["claude-tui"] != 1 || len(seen) != 2 {
		t.Fatalf("candidate harness counts = %v, want exactly one claude and one claude-tui", seen)
	}

	select {
	case got := <-dispatched:
		if got != "claude-tui" {
			t.Fatalf("dispatched runner = %q, want claude-tui", got)
		}
	default:
		t.Fatal("subprocess dispatch did not select a concrete runner")
	}

	var launch claudeTUILaunchObservation
	select {
	case launch = <-launches:
	default:
		t.Fatal("claude-tui did not reach its launch boundary")
	}
	select {
	case duplicate := <-launches:
		t.Fatalf("claude-tui launch boundary called more than once; second = %#v", duplicate)
	default:
	}
	if launch.executable != claudePath {
		t.Fatalf("launch executable = %q, want exact resolved path %q", launch.executable, claudePath)
	}
	if launch.size != (session.Size{Rows: 50, Cols: 220}) || launch.optionCount != 1 {
		t.Fatalf("launch size/options = %#v/%d, want 50x220/1", launch.size, launch.optionCount)
	}
	if launch.workdir != "" || !containsClaudeTUILaunchEnv(launch.env, "HOME="+homeDir) ||
		!containsClaudeTUILaunchEnv(launch.env, "PATH="+binDir) ||
		!containsClaudeTUILaunchEnv(launch.env, "CLAUDE_CONFIG_DIR="+filepath.Join(homeDir, ".claude")) {
		t.Fatalf("launch workdir/env = %q/%q, want empty workdir and isolated HOME/PATH/CLAUDE_CONFIG_DIR", launch.workdir, launch.env)
	}

	assertAdjacentClaudeTUIArgPair(t, launch.args, "--permission-mode", "bypassPermissions")
	settings := claudeTUIArgValue(t, launch.args, "--settings")
	assertClaudeTUIHookSettings(t, settings)
	if model := claudeTUIArgValue(t, launch.args, "--model"); model != "sonnet" {
		t.Fatalf("--model value = %q for routing model %q, want sonnet-family CLI alias sonnet; args=%q", model, decision.Model, launch.args)
	}
	for _, arg := range launch.args {
		if arg == "--print" || arg == "-p" {
			t.Fatalf("interactive claude-tui launch contains headless print flag %q: %q", arg, launch.args)
		}
	}
}

func containsClaudeTUILaunchEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func assertClaudeTUIHookSettings(t *testing.T, settings string) {
	t.Helper()
	if settings == "" {
		t.Fatal("--settings value is empty")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(settings), &root); err != nil || root == nil {
		t.Fatalf("--settings is not a JSON object: %q: %v", settings, err)
	}
	var hooks map[string]json.RawMessage
	hooksRaw, ok := root["hooks"]
	if !ok {
		t.Fatalf("--settings object has no hooks member: %s", settings)
	}
	if err := json.Unmarshal(hooksRaw, &hooks); err != nil || hooks == nil {
		t.Fatalf("--settings hooks is not an object: %s: %v", hooksRaw, err)
	}
	if len(hooks) != 4 {
		t.Fatalf("generated hook events = %v, want exactly PreToolUse, PostToolUse, Stop, UserPromptSubmit", hooks)
	}
	commands := make(map[string]string, len(hooks))
	for _, event := range []string{"PreToolUse", "PostToolUse", "Stop", "UserPromptSubmit"} {
		raw, exists := hooks[event]
		if !exists {
			t.Fatalf("generated settings missing %q hook: %s", event, settings)
		}
		var groups []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &groups); err != nil {
			t.Fatalf("%s hook groups do not decode: %v", event, err)
		}
		if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s hook groups = %#v, want one match-all group with one hook", event, groups)
		}
		if groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Command == "" {
			t.Fatalf("%s hook = %#v, want nonempty command hook", event, groups[0].Hooks[0])
		}
		commands[event] = groups[0].Hooks[0].Command
	}
	if strings.Contains(settings, `"shell"`) {
		t.Fatalf("generated settings contains obsolete flat shell hook schema: %s", settings)
	}

	preDir := firstQuotedClaudeTUICommandValue(t, commands["PreToolUse"])
	postDir := firstQuotedClaudeTUICommandValue(t, commands["PostToolUse"])
	if preDir == "" || preDir != postDir || !strings.Contains(filepath.Base(preDir), "claude-tui-hooks-") {
		t.Fatalf("tool hook directories = pre %q post %q, want same generated claude-tui hook dir", preDir, postDir)
	}
	if !strings.Contains(commands["PreToolUse"], "tool-$(date +%s%N)-pre.json") ||
		!strings.Contains(commands["PostToolUse"], "tool-$(date +%s%N)-post.json") {
		t.Fatalf("tool hooks do not target generated pre/post payload names: pre=%q post=%q", commands["PreToolUse"], commands["PostToolUse"])
	}
	stopCommand := commands["Stop"]
	const destinationPrefix = "dest="
	destinationStart := strings.Index(stopCommand, destinationPrefix)
	if destinationStart < 0 {
		t.Fatalf("Stop hook has no destination assignment: %q", stopCommand)
	}
	stopPath := firstQuotedClaudeTUICommandValue(t, stopCommand[destinationStart+len(destinationPrefix):])
	if filepath.Dir(stopPath) != preDir || filepath.Base(stopPath) != "stop-hook-payload.json" {
		t.Fatalf("Stop hook payload path = %q, want stop-hook-payload.json under %q", stopPath, preDir)
	}
	for _, atomicFragment := range []string{`tmp="${dest}.tmp.$$"`, `> "$tmp"`, `mv -f "$tmp" "$dest"`} {
		if !strings.Contains(stopCommand, atomicFragment) {
			t.Fatalf("Stop hook lacks atomic publication fragment %q: %q", atomicFragment, stopCommand)
		}
	}
	const noncePrefix = `,"nonce":"`
	nonceStart := strings.Index(stopCommand, noncePrefix)
	if nonceStart < 0 {
		t.Fatalf("Stop hook does not emit nonce-bound JSON: %q", stopCommand)
	}
	nonceTail := stopCommand[nonceStart+len(noncePrefix):]
	nonceEnd := strings.IndexByte(nonceTail, '"')
	if nonceEnd <= 0 || !strings.Contains(stopCommand, `"transcript_path"`) {
		t.Fatalf("Stop hook lacks generated nonce/transcript payload semantics: %q", stopCommand)
	}
}

func firstQuotedClaudeTUICommandValue(t *testing.T, command string) string {
	t.Helper()
	doubleStart := strings.IndexByte(command, '"')
	singleStart := strings.IndexByte(command, '\'')
	start := doubleStart
	quote := byte('"')
	if singleStart >= 0 && (start < 0 || singleStart < start) {
		start = singleStart
		quote = '\''
	}
	if start < 0 {
		t.Fatalf("hook command has no quoted path: %q", command)
	}
	if quote == '\'' {
		end := strings.IndexByte(command[start+1:], '\'')
		if end < 0 {
			t.Fatalf("hook command has unterminated single-quoted path: %q", command)
		}
		return command[start+1 : start+1+end]
	}
	escaped := false
	for i := start + 1; i < len(command); i++ {
		switch {
		case escaped:
			escaped = false
		case command[i] == '\\':
			escaped = true
		case command[i] == quote:
			value, err := strconv.Unquote(command[start : i+1])
			if err != nil {
				t.Fatalf("decode quoted hook path in %q: %v", command, err)
			}
			return value
		}
	}
	t.Fatalf("hook command has unterminated quoted path: %q", command)
	return ""
}

func assertAdjacentClaudeTUIArgPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return
		}
	}
	t.Fatalf("launch args %q do not contain adjacent pair %q %q", args, flag, value)
}

func claudeTUIArgValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, arg := range args {
		if arg != flag {
			continue
		}
		if i+1 >= len(args) {
			t.Fatalf("launch flag %q has no value in %q", flag, args)
		}
		return args[i+1]
	}
	t.Fatalf("launch args %q do not contain %q", args, flag)
	return ""
}

// TestExecuteDefaultClaudeUnrestrictedSelectsClaudeTUI proves the complete
// Service.Execute composition: an unpinned, default-policy unrestricted
// request routes over the shared Claude surface, selects claude-tui, and hands
// the concrete claude-tui runner to subprocess dispatch.
func TestExecuteDefaultClaudeUnrestrictedSelectsClaudeTUI(t *testing.T) {
	t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", "")
	// The registry below supplies hermetic discovery. Keeping the process PATH
	// empty makes the selected claude-tui runner terminate at its binary lookup,
	// before it can create or start a PTY.
	t.Setenv("PATH", "")

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-14T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  claude-sonnet-fixture:
    family: claude-sonnet
    status: active
    power: 8
    surfaces:
      claude-code: sonnet-fixture
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	cancelRefresh()
	public, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		QuotaRefreshContext: refreshCtx,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := public.(*service)
	// claude and claude-tui share the claude binary. Exposing only that binary
	// yields exactly those two routable subscription candidates.
	forceAvailableHarnessesForTest(t, svc, "claude", "claude-tui")

	dispatched := make(chan string, 1)
	svc.subprocessDispatchObserver = func(runner harnesses.Harness) {
		dispatched <- runner.Info().Name
	}

	events, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:      "exercise the default Claude route",
		Policy:      "default",
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	drained := drainUnifiedServiceEvents(t, events, 5*time.Second)

	var decision *ServiceRoutingDecisionData
	for _, event := range drained {
		if event.Type != ServiceEventTypeRoutingDecision {
			continue
		}
		payload := decodeRoutingDecisionEvent(t, event)
		decision = &payload
		break
	}
	if decision == nil {
		t.Fatalf("missing routing_decision event: %#v", drained)
	}
	if decision.RequestedHarness != "" {
		t.Fatalf("requested harness = %q, want unpinned", decision.RequestedHarness)
	}
	if decision.RequestedPolicy != "default" {
		t.Fatalf("requested policy = %q, want default", decision.RequestedPolicy)
	}
	if decision.Permissions != "unrestricted" {
		t.Fatalf("permissions = %q, want unrestricted", decision.Permissions)
	}
	if decision.Harness != "claude-tui" {
		t.Fatalf("routing_decision harness = %q, want claude-tui", decision.Harness)
	}
	seen := map[string]bool{}
	for _, candidate := range decision.Candidates {
		if !candidate.Eligible {
			continue
		}
		seen[candidate.Harness] = true
	}
	if !seen["claude"] || !seen["claude-tui"] || len(seen) != 2 {
		t.Fatalf("eligible candidate harnesses = %v, want exactly claude and claude-tui; trace=%#v", seen, decision.Candidates)
	}

	select {
	case got := <-dispatched:
		if got != "claude-tui" {
			t.Fatalf("dispatched runner = %q, want claude-tui", got)
		}
	default:
		t.Fatal("subprocess dispatch did not select a concrete runner")
	}
}
