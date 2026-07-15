package anthropic

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestClaudePortableRuntimeConfigClosure(t *testing.T) {
	for _, test := range []struct {
		name     string
		document map[string]any
	}{
		{name: "plugin", document: map[string]any{"enabledPlugins": map[string]any{"secret-plugin": true}}},
		{name: "hook", document: map[string]any{"hooks": map[string]any{"Stop": []any{"account-secret-hook"}}}},
		{name: "status line", document: map[string]any{"statusLine": map[string]any{"command": "/account-secret/status"}}},
		{name: "MCP", document: map[string]any{"mcpServers": map[string]any{"secret": map[string]any{"command": "/account-secret/mcp"}}}},
		{name: "marketplace", document: map[string]any{"extraKnownMarketplaces": map[string]any{"secret": "file:///account-secret"}}},
		{name: "external directory", document: map[string]any{"permissions": map[string]any{"additionalDirectories": []any{"/account-secret/project"}}}},
		{name: "process wrapper env", document: map[string]any{"env": map[string]any{"CLAUDE_CODE_PROCESS_WRAPPER": "/account-secret/wrapper"}}},
		{name: "loader env", document: map[string]any{"env": map[string]any{"LD_PRELOAD": "/account-secret/library.so"}}},
		{name: "external ripgrep", document: map[string]any{"env": map[string]any{"USE_BUILTIN_RIPGREP": "0"}}},
		{name: "remote refresh", document: map[string]any{"forceRemoteSettingsRefresh": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateClaudePortableSettings(test.document)
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("settings validation error = %v, want closure incomplete", err)
			}
			for _, forbidden := range []string{"account-secret", "secret-plugin", "LD_PRELOAD"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("settings error leaked %q: %v", forbidden, err)
				}
			}
		})
	}

	valid := map[string]any{
		"model": "sonnet",
		"env": map[string]any{
			"ANTHROPIC_API_KEY": "settings-secret",
			"CLAUDE_DEBUG":      "1",
		},
	}
	if err := validateClaudePortableSettings(valid); err != nil {
		t.Fatalf("valid settings error = %v", err)
	}
	if !claudePortableSettingsCredential(valid) {
		t.Fatal("settings credential was not recognized")
	}
	if err := validateClaudePortableAppState(map[string]any{"projects": map[string]any{"/account-bearing/project": map[string]any{"hasTrustDialogAccepted": true}}}); err != nil {
		t.Fatalf("host-indexed project state should be inspected but omitted: %v", err)
	}
	if err := validateClaudePortableAppState(map[string]any{"mcpServers": map[string]any{"secret": true}}); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("app MCP validation error = %v, want closure incomplete", err)
	}
	if err := validateClaudePortableAppState(map[string]any{"projects": map[string]any{"/account-bearing/project": map[string]any{"enabledMcpjsonServers": []any{"secret-server"}}}}); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("nested project MCP validation error = %v, want closure incomplete", err)
	}
	if err := validateClaudePortableArguments([]string{"--print", "--output-format", "stream-json", "--verbose"}); err != nil {
		t.Fatalf("supported configured arguments error = %v", err)
	}
	if err := validateClaudePortableArguments([]string{"--settings", "/account-secret/settings.json"}); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), "account-secret") {
		t.Fatalf("external configured argument error = %v", err)
	}
}

func TestClaudePortableRuntimeCredentialSchema(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".credentials.json")
	for _, test := range []struct {
		name    string
		json    string
		wantErr bool
	}{
		{name: "access token", json: `{"claudeAiOauth":{"accessToken":"credential-secret"}}`},
		{name: "refresh token", json: `{"claudeAiOauth":{"refreshToken":"credential-secret"}}`},
		{name: "MCP only", json: `{"mcpOAuth":{"server":"credential-secret"}}`, wantErr: true},
		{name: "empty Claude auth", json: `{"claudeAiOauth":{}}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
			present, digest, err := claudePortableCredential(path)
			if test.wantErr {
				if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), "credential-secret") || strings.Contains(err.Error(), root) {
					t.Fatalf("credential error = %v", err)
				}
				return
			}
			if err != nil || !present || digest == "" {
				t.Fatalf("credential = (%v, %q, %v)", present, digest, err)
			}
		})
	}
}

func TestClaudeTUIPortableEnvironmentMatchesExecutionBoundary(t *testing.T) {
	unsetAnthropicPortablePrefixes(t, "CLAUDE_", "ANTHROPIC_")
	t.Setenv("CLAUDE_DEBUG", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "credential-secret")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-cross-tui-boundary")

	environment, credential, err := claudePortableEnvironment([]string{"CLAUDE_CODE_OAUTH_TOKEN"}, []string{"CLAUDE_"})
	if err != nil {
		t.Fatal(err)
	}
	want := []harnesses.PortableRuntimeEnvironment{{Name: "CLAUDE_CODE_OAUTH_TOKEN"}, {Name: "CLAUDE_DEBUG"}}
	if !reflect.DeepEqual(environment, want) || !credential {
		t.Fatalf("TUI environment = %#v, credential=%v; want %#v, true", environment, credential, want)
	}
	serialized := strings.Join([]string{environment[0].Name, environment[1].Name}, ",")
	for _, forbidden := range []string{"credential-secret", "must-not-cross-tui-boundary", "ANTHROPIC_API_KEY="} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("TUI environment leaked %q: %s", forbidden, serialized)
		}
	}

	t.Setenv("CLAUDE_CODE_PLUGIN_SEED_DIR", "/account-secret/plugins")
	_, _, err = claudePortableEnvironment(nil, []string{"CLAUDE_"})
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), "account-secret") {
		t.Fatalf("external TUI environment error = %v", err)
	}
}

func TestClaudePortableRuntimeRejectsInheritedExternalEnvironment(t *testing.T) {
	for _, name := range []string{"USE_BUILTIN_RIPGREP", "BUN_INSTALL"} {
		t.Run(name, func(t *testing.T) {
			unsetAnthropicPortablePrefixes(t, name)
			t.Setenv(name, "/account-secret/external")
			err := rejectClaudePortableExternalEnvironment()
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), "account-secret") {
				t.Fatalf("inherited external environment error = %v", err)
			}
		})
	}
}

func TestClaudePortableRuntimeTargetCancellationAndRoots(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ClaudePortableRuntimeAssets(canceled, harnesses.PortableRuntimeTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, ClaudePortableRuntimeOptions{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("canceled discovery error = %v", err)
	}
	_, err = ClaudePortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: "not-linux", GOARCH: runtime.GOARCH}, ClaudePortableRuntimeOptions{})
	if !errors.Is(err, harnesses.ErrPortableRuntimeTargetUnsupported) {
		t.Fatalf("invalid target error = %v, want target unsupported", err)
	}

	home := t.TempDir()
	realConfig := t.TempDir()
	linkedConfig := filepath.Join(home, "linked-config")
	if err := os.Symlink(realConfig, linkedConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", linkedConfig)
	if _, _, err := claudePortableStateRoots(); err == nil {
		t.Fatal("symlinked CLAUDE_CONFIG_DIR was accepted")
	}
}

func TestClaudePortableRuntimeVerifiedReleaseEvidence(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Claude verified release evidence is Linux-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("user home unavailable: %v", err)
	}
	installed := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.210")
	if _, err := os.Stat(installed); errors.Is(err, os.ErrNotExist) {
		t.Skip("reviewed Claude Code 2.1.210 release is not installed")
	} else if err != nil {
		t.Fatal(err)
	}
	launcher, version, interpreter, digest, size, err := claudePortableLauncher(installed)
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.1.210" || !claudePortableReleaseVerified(runtime.GOARCH, version, digest, size) {
		t.Fatalf("installed release evidence mismatch: version=%q digest=%q size=%d", version, digest, size)
	}
	evidence := claudePortableVerifiedReleases[version][runtime.GOARCH]
	if evidence.commit != "88e9fbf39bf4efa5bca44549b7fd9461628657e6" ||
		evidence.manifestDigest != "654eb446e70eaed758a1f1230986d1e87ca9e8ad947b5de6f9b4db8496101ece" ||
		evidence.signingFingerprint != "31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE" || evidence.offlineProbeProfile != 1 {
		t.Fatalf("release evidence audit metadata is incomplete: %#v", evidence)
	}
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	contribution, err := claudePortableExecutableClosure(context.Background(), target, launcher, interpreter, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, asset := range contribution.Assets {
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
	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand(root, contribution, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
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
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skipf("reviewed release isolated probe requires bubblewrap: %v", err)
	}
	bwrapArguments := []string{"--unshare-all", "--die-with-parent", "--ro-bind", root, "/", "--dev", "/dev", "--proc", "/proc", guest(command)}
	bwrapArguments = append(bwrapArguments, guestArguments...)
	probe := exec.Command(bwrap, bwrapArguments...)
	probe.Env = []string{"HOME=/nonexistent", "LANG=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	output, err := probe.CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(output)), version+" ") {
		t.Fatalf("reviewed release isolated probe = %v: %q", err, output)
	}
	removed := ""
	for _, asset := range contribution.Assets {
		if asset.Kind == harnesses.PortableRuntimeAssetSupport && filepath.Base(asset.Target) == "libc.so.6" {
			removed = filepath.Join(root, filepath.FromSlash(asset.Target))
			break
		}
	}
	if removed == "" {
		t.Fatal("reviewed release evidence has no emitted libc dependency")
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	missingProbe := exec.Command(bwrap, bwrapArguments...)
	missingProbe.Env = probe.Env
	if output, err := missingProbe.CombinedOutput(); err == nil {
		t.Fatalf("reviewed release ran without emitted libc: %q", output)
	}
}

func unsetAnthropicPortablePrefixes(t *testing.T, prefixes ...string) {
	t.Helper()
	for _, assignment := range os.Environ() {
		name := strings.SplitN(assignment, "=", 2)[0]
		for _, prefix := range prefixes {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
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
			break
		}
	}
}
