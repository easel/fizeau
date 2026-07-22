package claudetui

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

func TestClaudeTUIPortableRuntimeContribution(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	unsetClaudeTUIPortablePrefixes(t, "CLAUDE_", "ANTHROPIC_")
	for _, name := range []string{"LANG", "LC_ALL", "TZ", "TERM"} {
		unsetClaudeTUIPortableEnvironment(t, name)
	}
	home := prepareClaudeTUIPortableState(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-cross-tui-boundary")
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	launcher := portableClaudeTUIDynamicFixture(t, home)
	_, err := (&Harness{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("unverified TUI release error = %v, want closure incomplete", err)
	}
	productionDiscovery := discoverClaudeTUIPortableRuntime
	discoverClaudeTUIPortableRuntime = func(ctx context.Context, gotTarget harnesses.PortableRuntimeTarget, options anthropic.ClaudePortableRuntimeOptions) (harnesses.PortableRuntimeContribution, error) {
		if options.Launcher != launcher || len(options.Arguments) != 0 || options.InheritsProcessEnvironment || len(options.EnvironmentPrefixes) != 0 || !reflect.DeepEqual(options.EnvironmentNames, []string{"CLAUDE_CODE_OAUTH_TOKEN"}) {
			t.Fatalf("Claude TUI adapter options = %#v", options)
		}
		contribution, err := portableClaudeTUIFixtureContribution(ctx, gotTarget, launcher)
		contribution.Environment = []harnesses.PortableRuntimeEnvironment{{Name: "CLAUDE_CODE_OAUTH_TOKEN"}}
		return contribution, err
	}
	t.Cleanup(func() { discoverClaudeTUIPortableRuntime = productionDiscovery })
	contribution, err := (&Harness{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatalf("PortableRuntimeAssets() error = %v", err)
	}
	if contribution.ClosureClass != harnesses.PortableRuntimeClosureDynamic || contribution.Launch.EntrypointTarget != "harnesses/anthropic/bin/claude" || contribution.Launch.LoaderTarget == "" || len(contribution.Launch.LibraryRootTargets) == 0 {
		t.Fatalf("TUI dynamic contribution = %#v", contribution)
	}
	wanted := map[string]harnesses.PortableRuntimeAssetKind{
		"harnesses/anthropic/bin/claude": harnesses.PortableRuntimeAssetExecutable,
	}
	for _, asset := range contribution.Assets {
		if kind, exists := wanted[asset.Target]; exists {
			if asset.Kind != kind || asset.ContentSHA256 == "" {
				t.Fatalf("asset %q = %#v", asset.Target, asset)
			}
			delete(wanted, asset.Target)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing TUI assets: %#v", wanted)
	}
	assertClaudeTUIPortableOfflineProbe(t, contribution)
	if len(contribution.Environment) != 1 || contribution.Environment[0].Name != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("TUI environment = %#v", contribution.Environment)
	}
	serialized := fmt.Sprintf("%#v", contribution.Environment)
	for _, forbidden := range []string{"must-not-cross-tui-boundary", "tui-credential-secret", home, "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN="} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("TUI environment leaked %q: %s", forbidden, serialized)
		}
	}
	discoverClaudeTUIPortableRuntime = productionDiscovery

	unknown := filepath.Join(t.TempDir(), "account-secret-wrapper")
	if err := os.WriteFile(unknown, []byte("#!/bin/sh\n# tui-wrapper-secret\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (&Harness{Binary: unknown}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), unknown) || strings.Contains(err.Error(), "tui-wrapper-secret") {
		t.Fatalf("unknown TUI launcher error = %v", err)
	}
	_, err = (&Harness{Binary: "/bin/true"}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("arbitrary TUI dynamic ELF error = %v, want closure incomplete", err)
	}
	discoverClaudeTUIPortableRuntime = func(context.Context, harnesses.PortableRuntimeTarget, anthropic.ClaudePortableRuntimeOptions) (harnesses.PortableRuntimeContribution, error) {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: dynamic loader or library closure is incomplete", harnesses.ErrPortableRuntimeClosureIncomplete)
	}
	_, err = (&Harness{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	discoverClaudeTUIPortableRuntime = productionDiscovery
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("incomplete TUI library error = %v, want redacted closure incomplete", err)
	}
}

func portableClaudeTUIFixtureContribution(ctx context.Context, target harnesses.PortableRuntimeTarget, launcher string) (harnesses.PortableRuntimeContribution, error) {
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	image, err := elf.Open(resolved)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	var interpreter string
	for _, program := range image.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		contents := make([]byte, program.Filesz)
		if _, err := program.ReadAt(contents, 0); err != nil {
			_ = image.Close()
			return harnesses.PortableRuntimeContribution{}, err
		}
		interpreter = strings.TrimSuffix(string(contents), "\x00")
	}
	_ = image.Close()
	return harnesses.AnalyzePortableRuntimeDynamicClosure(ctx, target, harnesses.PortableRuntimeDynamicClosureRequest{
		EntrypointSource: resolved, EntrypointTarget: "harnesses/anthropic/bin/claude",
		LoaderTarget:      "harnesses/anthropic/loader/" + filepath.Base(interpreter),
		ExactLibraryRoots: portableClaudeTUIFixtureLibraryRoots(), RuntimeLookup: harnesses.PortableRuntimeLookupVerifiedExact,
	})
}

func portableClaudeTUIFixtureLibraryRoots() []harnesses.PortableRuntimeLibrarySearchRoot {
	directories := []string{"/usr/lib"}
	switch runtime.GOARCH {
	case "arm64":
		directories = []string{"/usr/lib/aarch64-linux-gnu", "/lib/aarch64-linux-gnu"}
	case "amd64":
		directories = []string{"/usr/lib/x86_64-linux-gnu", "/lib/x86_64-linux-gnu", "/usr/lib64", "/lib64"}
	}
	for _, directory := range directories {
		resolved, err := filepath.EvalSymlinks(directory)
		if err != nil {
			continue
		}
		return []harnesses.PortableRuntimeLibrarySearchRoot{{Source: resolved, Target: "harnesses/anthropic/lib/fixture"}}
	}
	return nil
}

func assertClaudeTUIPortableOfflineProbe(t *testing.T, contribution harnesses.PortableRuntimeContribution) {
	t.Helper()
	if !tuiUnprivilegedNamespacesSupported(t) {
		return
	}
	root := t.TempDir()
	for _, asset := range contribution.Assets {
		if asset.Kind != harnesses.PortableRuntimeAssetExecutable && asset.Kind != harnesses.PortableRuntimeAssetSupport {
			continue
		}
		contents, err := os.ReadFile(asset.Source)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, filepath.FromSlash(asset.Target))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if asset.Executable {
			mode = 0o700
		}
		if err := os.WriteFile(destination, contents, mode); err != nil {
			t.Fatal(err)
		}
	}
	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand(root, contribution, nil)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runClaudeTUIPortableIsolated(t, root, command, arguments)
	if err != nil || strings.TrimSpace(string(output)) != "@anthropic-ai/claude-code" {
		t.Fatalf("credential-free isolated TUI launch probe = %v: %q", err, output)
	}
	removed := ""
	for _, asset := range contribution.Assets {
		if asset.Kind == harnesses.PortableRuntimeAssetSupport && asset.PathKind == harnesses.PortableRuntimePathFile && filepath.Base(asset.Target) == "libc.so.6" {
			removed = filepath.Join(root, filepath.FromSlash(asset.Target))
			break
		}
	}
	if removed == "" {
		t.Fatal("TUI offline launch probe found no required library asset")
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	if output, err := runClaudeTUIPortableIsolated(t, root, command, arguments); err == nil {
		t.Fatalf("isolated TUI launch succeeded after removing required library %q: %q", removed, output)
	}
}

// tuiUnprivilegedNamespacesSupported reports whether the host can create the
// unprivileged user namespace the offline probe needs. Hosts that restrict
// them (for example GitHub's Ubuntu 24.04 runners under the default AppArmor
// policy) surface EPERM writing /proc/self/uid_map; the probe then degrades
// to a logged no-op so the remaining credential-boundary assertions still run.
func tuiUnprivilegedNamespacesSupported(t *testing.T) bool {
	t.Helper()
	if bwrap, err := exec.LookPath("bwrap"); err == nil {
		output, err := exec.Command(bwrap, "--unshare-all", "--die-with-parent", "--ro-bind", "/", "/", "/bin/true").CombinedOutput()
		if err != nil {
			t.Logf("host cannot create unprivileged namespaces via bwrap, skipping offline probe: %v: %q", err, output)
			return false
		}
		return true
	}
	if unshare, err := exec.LookPath("unshare"); err == nil {
		output, err := exec.Command(unshare, "--user", "--map-root-user", "/bin/true").CombinedOutput()
		if err != nil {
			t.Logf("host cannot create unprivileged user namespaces, skipping offline probe: %v: %q", err, output)
			return false
		}
	}
	return true
}

func runClaudeTUIPortableIsolated(t *testing.T, root, command string, arguments []string) ([]byte, error) {
	t.Helper()
	for _, directory := range []string{"dev", "proc", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	guest := func(value string) string { return strings.ReplaceAll(value, root, "") }
	guestArguments := make([]string, len(arguments))
	for index, argument := range arguments {
		guestArguments[index] = guest(argument)
	}
	var probe *exec.Cmd
	if bwrap, err := exec.LookPath("bwrap"); err == nil {
		bwrapArguments := []string{"--unshare-all", "--die-with-parent", "--ro-bind", root, "/", "--dev", "/dev", "--proc", "/proc", guest(command)}
		bwrapArguments = append(bwrapArguments, guestArguments...)
		probe = exec.Command(bwrap, bwrapArguments...)
	} else if unshare, unshareErr := exec.LookPath("unshare"); unshareErr == nil {
		unshareArguments := []string{"--user", "--map-root-user", "--mount", "--pid", "--fork", "--net", "chroot", root, guest(command)}
		unshareArguments = append(unshareArguments, guestArguments...)
		probe = exec.Command(unshare, unshareArguments...)
	} else {
		t.Fatalf("isolated portable-runtime probe requires bubblewrap or unshare: %v", err)
	}
	probe.Env = []string{"HOME=/nonexistent", "LANG=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	return probe.CombinedOutput()
}

func TestClaudeTUIPortableEnvironmentClassifierMatchesExecutionBoundary(t *testing.T) {
	for _, name := range []string{"LANG", "LC_ALL", "TZ", "TERM", "CLAUDE_CODE_DEBUG_LOG_LEVEL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_SESSION_ID", "ANTHROPIC_API_KEY"} {
		unsetClaudeTUIPortableEnvironment(t, name)
	}
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TZ", "UTC")
	t.Setenv("CLAUDE_CODE_DEBUG_LOG_LEVEL", "debug")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "credential-secret")
	t.Setenv("CLAUDE_CONFIG_DIR", "/home/operator/.claude")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "nested-session")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-cross")

	actual := make(map[string]struct{})
	for _, assignment := range BuildEnvironmentAllowlist() {
		name := strings.SplitN(assignment, "=", 2)[0]
		actual[name] = struct{}{}
	}
	portable := claudeTUIPortableInheritedEnvironmentNames()
	for _, name := range []string{"TZ", "CLAUDE_CODE_DEBUG_LOG_LEVEL", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if _, ok := actual[name]; !ok || !containsClaudeTUIEnvironmentName(portable, name) {
			t.Fatalf("inherited environment %q not shared by execution and portable classifier: actual=%v portable=%v", name, actual, portable)
		}
	}
	for _, name := range []string{"HOME", "PATH", "USER", "LOGNAME", "SHELL", "LANG", "TERM", "CLAUDE_CONFIG_DIR"} {
		if _, ok := actual[name]; ok && containsClaudeTUIEnvironmentName(portable, name) {
			t.Fatalf("activation-generated environment %q was declared inherited", name)
		}
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_SESSION_ID"} {
		if _, ok := actual[name]; ok || containsClaudeTUIEnvironmentName(portable, name) {
			t.Fatalf("%s crossed the TUI environment boundary", name)
		}
	}
}

func TestClaudeTUIPortableEnvironmentDropsNestedSessionSentinels(t *testing.T) {
	unsetClaudeTUIPortablePrefixes(t, "CLAUDE_", "CLAUDECODE")
	unsetClaudeTUIPortableEnvironment(t, "TZ")
	rejected := []string{
		"CLAUDECODE",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_BRIDGE_SESSION_ID",
	}
	for _, name := range rejected {
		t.Setenv(name, "must-not-cross")
	}
	approved := map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN":         "oauth-token",
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN": "refresh-token",
		"CLAUDE_CODE_OAUTH_SCOPES":        "user:inference",
		"CLAUDE_CODE_DEBUG_LOG_LEVEL":     "debug",
	}
	for name, value := range approved {
		t.Setenv(name, value)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/host/account/.claude")

	productionDiscovery := discoverClaudeTUIPortableRuntime
	var captured anthropic.ClaudePortableRuntimeOptions
	discoverClaudeTUIPortableRuntime = func(_ context.Context, _ harnesses.PortableRuntimeTarget, options anthropic.ClaudePortableRuntimeOptions) (harnesses.PortableRuntimeContribution, error) {
		captured = options
		contribution := harnesses.PortableRuntimeContribution{}
		for _, name := range options.EnvironmentNames {
			contribution.Environment = append(contribution.Environment, harnesses.PortableRuntimeEnvironment{Name: name})
		}
		contribution.ExecutionConstraints.Environment = append(contribution.ExecutionConstraints.Environment,
			harnesses.PortableRuntimeEnvironmentConstraint{
				Name: "CLAUDE_CONFIG_DIR",
				Kind: harnesses.PortableRuntimeEnvironmentGuestPath,
				GuestPath: harnesses.PortableRuntimeGuestPath{
					Scope:  harnesses.PortableRuntimeGuestPathRuntime,
					Target: "config/claude",
				},
			})
		return contribution, nil
	}
	t.Cleanup(func() { discoverClaudeTUIPortableRuntime = productionDiscovery })

	contribution, err := (&Harness{}).PortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.EnvironmentPrefixes) != 0 {
		t.Fatalf("portable environment prefixes = %v, want none", captured.EnvironmentPrefixes)
	}
	wantNames := []string{"CLAUDE_CODE_DEBUG_LOG_LEVEL", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_SCOPES", "CLAUDE_CODE_OAUTH_TOKEN"}
	if !reflect.DeepEqual(captured.EnvironmentNames, wantNames) {
		t.Fatalf("portable environment names = %v, want %v", captured.EnvironmentNames, wantNames)
	}
	projected := make(map[string]bool)
	for _, environment := range contribution.Environment {
		projected[environment.Name] = true
	}
	for _, name := range rejected {
		if projected[name] || containsClaudeTUIEnvironmentName(captured.EnvironmentNames, name) {
			t.Errorf("nested-session environment %q crossed the portable boundary", name)
		}
	}
	for _, name := range wantNames {
		if !projected[name] {
			t.Errorf("approved portable environment %q was omitted", name)
		}
	}
	if projected["CLAUDE_CONFIG_DIR"] {
		t.Fatal("host CLAUDE_CONFIG_DIR was inherited instead of guest-generated")
	}
	generatedConfig := false
	for _, constraint := range contribution.ExecutionConstraints.Environment {
		if constraint.Name == "CLAUDE_CONFIG_DIR" && constraint.Kind == harnesses.PortableRuntimeEnvironmentGuestPath {
			generatedConfig = true
		}
	}
	if !generatedConfig {
		t.Fatal("portable CLAUDE_CONFIG_DIR guest-path constraint was omitted")
	}
}

func containsClaudeTUIEnvironmentName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func prepareClaudeTUIPortableState(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "account-bearing-tui-home")
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		filepath.Join(root, ".credentials.json"): `{"claudeAiOauth":{"refreshToken":"tui-credential-secret"},"mcpOAuth":{"server":"tui-mcp-secret"}}`,
		filepath.Join(root, "settings.json"):     `{"model":"sonnet"}`,
		filepath.Join(root, "cache", "seed"):     "tui-cache-secret",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	quota := filepath.Join(t.TempDir(), "account-bearing-tui-quota.json")
	if err := os.WriteFile(quota, []byte(`{"quota":"tui-quota-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", quota)
	unsetClaudeTUIPortableEnvironment(t, "CLAUDE_CONFIG_DIR")
	return home
}

func portableClaudeTUIDynamicFixture(t *testing.T, home string) string {
	t.Helper()
	root := home
	source := filepath.Join(root, "claude.c")
	contents := `#include <dlfcn.h>
#include <stdio.h>
int main(void) {
  void *self = dlopen(NULL, RTLD_NOW);
  if (self == NULL) return 2;
  if (dlsym(self, "main") == NULL) return 3;
  puts("@anthropic-ai/claude-code");
  return 0;
}
`
	if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	versioned := filepath.Join(root, ".local", "share", "claude", "versions", "2.1.210")
	if err := os.MkdirAll(filepath.Dir(versioned), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cc", "-rdynamic", "-o", versioned, source, "-ldl")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Claude TUI dynamic fixture: %v: %s", err, output)
	}
	image, err := elf.Open(versioned)
	if err != nil {
		t.Fatal(err)
	}
	symbols, err := image.ImportedSymbols()
	_ = image.Close()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"dlopen": false, "dlsym": false}
	for _, symbol := range symbols {
		if _, exists := wanted[symbol.Name]; exists {
			wanted[symbol.Name] = true
		}
	}
	if !wanted["dlopen"] || !wanted["dlsym"] {
		t.Fatalf("Claude TUI fixture imports = %#v, want dlopen and dlsym", wanted)
	}
	bin := filepath.Join(root, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bin, "claude")
	if err := os.Symlink(versioned, launcher); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func unsetClaudeTUIPortableEnvironment(t *testing.T, name string) {
	t.Helper()
	value, existed := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func unsetClaudeTUIPortablePrefixes(t *testing.T, prefixes ...string) {
	t.Helper()
	for _, assignment := range os.Environ() {
		name := strings.SplitN(assignment, "=", 2)[0]
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				unsetClaudeTUIPortableEnvironment(t, name)
				break
			}
		}
	}
}
