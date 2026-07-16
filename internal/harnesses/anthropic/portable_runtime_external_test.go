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

func TestClaudePortableRuntimeSharedAssetsDeduplicate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	clearClaudePortableEnvironment(t)
	home := filepath.Join(t.TempDir(), "account-bearing-shared-home")
	launcher := buildSharedClaudePortableFixture(t, home)
	registerSharedClaudePortableFixture(t, launcher)
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSharedClaudePortableFile(t, filepath.Join(root, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"shared-credential-secret"}}`)
	writeSharedClaudePortableFile(t, filepath.Join(root, "settings.json"), `{"model":"sonnet"}`)
	writeSharedClaudePortableFile(t, filepath.Join(root, "cache", "seed"), "shared-cache-secret")
	writeSharedClaudePortableFile(t, filepath.Join(home, ".claude.json"), `{"projects":{"/account-bearing/project":{"hasTrustDialogAccepted":true}}}`)
	quota := filepath.Join(t.TempDir(), "account-bearing-shared-quota.json")
	writeSharedClaudePortableFile(t, quota, `{"quota":"shared-quota-secret"}`)
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", quota)
	t.Setenv("CLAUDE_DEBUG", "1")
	t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid")

	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	printOptions := ClaudePortableRuntimeOptions{
		Launcher: launcher, EnvironmentNames: []string{"ANTHROPIC_BASE_URL"},
		EnvironmentPrefixes: []string{"ANTHROPIC_", "CLAUDE_"}, InheritsProcessEnvironment: true,
	}
	printContribution, err := ClaudePortableRuntimeAssets(context.Background(), target, printOptions)
	if err != nil {
		t.Fatalf("Claude contribution error = %v", err)
	}
	tuiContribution, err := ClaudePortableRuntimeAssets(context.Background(), target, ClaudePortableRuntimeOptions{
		Launcher: launcher, EnvironmentNames: []string{"CLAUDE_CODE_OAUTH_TOKEN"},
		EnvironmentPrefixes: []string{"CLAUDE_"},
	})
	if err != nil {
		t.Fatalf("Claude TUI contribution error = %v", err)
	}
	repeated, err := ClaudePortableRuntimeAssets(context.Background(), target, printOptions)
	if err != nil {
		t.Fatalf("repeated Claude contribution error = %v", err)
	}
	if printContribution.ClosureClass != tuiContribution.ClosureClass || !reflect.DeepEqual(printContribution.Launch, tuiContribution.Launch) || !reflect.DeepEqual(printContribution.Assets, tuiContribution.Assets) {
		t.Fatalf("shared normalized assets differ:\nClaude: %#v\nTUI: %#v", printContribution, tuiContribution)
	}
	if !reflect.DeepEqual(printContribution, repeated) {
		t.Fatalf("repeated shared contribution is nondeterministic:\nfirst: %#v\nsecond: %#v", printContribution, repeated)
	}
	if reflect.DeepEqual(printContribution.Environment, tuiContribution.Environment) {
		t.Fatalf("transport-specific environment boundaries unexpectedly match: %#v", printContribution.Environment)
	}
	for _, asset := range printContribution.Assets {
		if asset.Target == "home/.claude.json" {
			t.Fatal("host-indexed .claude.json project state must be inspected but not copied")
		}
	}
}

func TestClaudePortableRuntimeMixedStateProjection(t *testing.T) {
	assertSharedClaudePortableMixedStateProjection(t, true)
	assertClaudePortableProjectionEdgeCases(t)
}

func TestClaudeTUIPortableRuntimeMixedStateProjection(t *testing.T) {
	assertSharedClaudePortableMixedStateProjection(t, false)
	assertClaudePortableProjectionEdgeCases(t)
}

func assertClaudePortableProjectionEdgeCases(t *testing.T) {
	t.Helper()
	t.Run("mutable-only uses data scope", func(t *testing.T) {
		contribution := harnesses.PortableRuntimeContribution{Assets: []harnesses.PortableRuntimeAsset{{Kind: harnesses.PortableRuntimeAssetCredential, Target: claudePortableCredentialTarget}}}
		if err := projectClaudePortableState(&contribution); err != nil {
			t.Fatal(err)
		}
		if len(contribution.StateProjections) != 0 || len(contribution.ExecutionConstraints.Environment) != 1 || contribution.ExecutionConstraints.Environment[0].GuestPath != (harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathData, Target: "claude"}) {
			t.Fatalf("mutable-only projection = %#v, constraints = %#v", contribution.StateProjections, contribution.ExecutionConstraints.Environment)
		}
	})
	t.Run("config-only fails closed", func(t *testing.T) {
		contribution := harnesses.PortableRuntimeContribution{Assets: []harnesses.PortableRuntimeAsset{{Kind: harnesses.PortableRuntimeAssetConfig, Target: claudePortableConfigTarget}}}
		if err := projectClaudePortableState(&contribution); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("config-only error = %v", err)
		}
	})
}

func assertSharedClaudePortableMixedStateProjection(t *testing.T, printMode bool) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	clearClaudePortableEnvironment(t)
	home := filepath.Join(t.TempDir(), "account-bearing-mixed-state-home")
	launcher := buildSharedClaudePortableFixture(t, home)
	registerSharedClaudePortableFixture(t, launcher)
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSharedClaudePortableFile(t, filepath.Join(root, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"projection-credential-secret"}}`)
	writeSharedClaudePortableFile(t, filepath.Join(root, "settings.json"), `{"model":"sonnet"}`)
	writeSharedClaudePortableFile(t, filepath.Join(root, "cache", "seed"), "projection-cache-secret")
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "absent-quota.json"))

	options := ClaudePortableRuntimeOptions{Launcher: launcher, EnvironmentPrefixes: []string{"CLAUDE_"}}
	if printMode {
		options.EnvironmentPrefixes = []string{"ANTHROPIC_", "CLAUDE_"}
		options.InheritsProcessEnvironment = true
	}
	contribution, err := ClaudePortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}, options)
	if err != nil {
		t.Fatalf("mixed-state contribution error = %v", err)
	}
	if len(contribution.StateProjections) != 1 {
		t.Fatalf("state projections = %#v", contribution.StateProjections)
	}
	projection := contribution.StateProjections[0]
	wantDirectory := harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathData, Target: "claude"}
	if projection.Directory != wantDirectory {
		t.Fatalf("projection directory = %#v, want %#v", projection.Directory, wantDirectory)
	}
	entries := make(map[string]string, len(projection.Entries))
	for _, entry := range projection.Entries {
		if _, exists := entries[entry.AssetTarget]; exists {
			t.Fatalf("asset projected twice: %#v", entry)
		}
		entries[entry.AssetTarget] = entry.Target
	}
	for target, output := range map[string]string{
		claudePortableConfigTarget:     "settings.json",
		claudePortableCredentialTarget: ".credentials.json",
		claudePortableCacheTarget:      "cache",
	} {
		if entries[target] != output {
			t.Errorf("projection entry %q = %q, want %q", target, entries[target], output)
		}
	}
	for _, asset := range contribution.Assets {
		if (asset.Kind == harnesses.PortableRuntimeAssetCredential || asset.Kind == harnesses.PortableRuntimeAssetCache || asset.Kind == harnesses.PortableRuntimeAssetQuota) && strings.HasPrefix(asset.Target, "home/") {
			t.Errorf("home-scoped mutable asset remains unprojectable: %#v", asset)
		}
	}
	foundConstraint := false
	for _, constraint := range contribution.ExecutionConstraints.Environment {
		if constraint.Name == "CLAUDE_CONFIG_DIR" {
			foundConstraint = constraint.Kind == harnesses.PortableRuntimeEnvironmentGuestPath && constraint.GuestPath == wantDirectory
		}
	}
	if !foundConstraint {
		t.Fatalf("CLAUDE_CONFIG_DIR constraint = %#v", contribution.ExecutionConstraints.Environment)
	}
}

func TestClaudePortableRuntimeOptionalStateAbsent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	clearClaudePortableEnvironment(t)
	home := t.TempDir()
	launcher := buildSharedClaudePortableFixture(t, home)
	registerSharedClaudePortableFixture(t, launcher)
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSharedClaudePortableFile(t, filepath.Join(root, ".credentials.json"), `{"claudeAiOauth":{"refreshToken":"credential-secret"}}`)
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-quota.json"))
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	contribution, err := ClaudePortableRuntimeAssets(context.Background(), target, ClaudePortableRuntimeOptions{
		Launcher: launcher, EnvironmentPrefixes: []string{"ANTHROPIC_", "CLAUDE_"}, InheritsProcessEnvironment: true,
	})
	if err != nil {
		t.Fatalf("optional state contribution error = %v", err)
	}
	for _, asset := range contribution.Assets {
		for _, absent := range []string{claudePortableConfigTarget, claudePortableCacheTarget, claudePortableQuotaTarget} {
			if asset.Target == absent {
				t.Fatalf("absent optional state emitted as %#v", asset)
			}
		}
	}
}

func TestClaudePortableRuntimeRejectsWorkflowCode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	clearClaudePortableEnvironment(t)
	home := t.TempDir()
	launcher := buildSharedClaudePortableFixture(t, home)
	registerSharedClaudePortableFixture(t, launcher)
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSharedClaudePortableFile(t, filepath.Join(root, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"credential-secret"}}`)
	writeSharedClaudePortableFile(t, filepath.Join(root, "workflows", "account-secret.js"), `console.log("workflow-secret")`)
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-quota.json"))
	_, err := ClaudePortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}, ClaudePortableRuntimeOptions{
		Launcher: launcher, EnvironmentPrefixes: []string{"ANTHROPIC_", "CLAUDE_"}, InheritsProcessEnvironment: true,
	})
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), home) || strings.Contains(err.Error(), "account-secret") || strings.Contains(err.Error(), "workflow-secret") {
		t.Fatalf("workflow closure error = %v", err)
	}
}

func TestClaudePortableRuntimeRejectsInstalledPluginWithoutEnablementMap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	clearClaudePortableEnvironment(t)
	home := t.TempDir()
	launcher := buildSharedClaudePortableFixture(t, home)
	registerSharedClaudePortableFixture(t, launcher)
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(root, "plugins", "marketplaces", "account-secret-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSharedClaudePortableFile(t, filepath.Join(root, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"credential-secret"}}`)
	writeSharedClaudePortableFile(t, filepath.Join(root, "settings.json"), `{"model":"sonnet"}`)
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-quota.json"))
	_, err := ClaudePortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}, ClaudePortableRuntimeOptions{
		Launcher: launcher, EnvironmentPrefixes: []string{"ANTHROPIC_", "CLAUDE_"}, InheritsProcessEnvironment: true,
	})
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), home) || strings.Contains(err.Error(), "account-secret") {
		t.Fatalf("installed plugin closure error = %v", err)
	}
}

func sharedClaudePortableFixtureDigest(t *testing.T, launcher string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func registerSharedClaudePortableFixture(t *testing.T, launcher string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	version := filepath.Base(resolved)
	previous, existed := claudePortableVerifiedReleases[version]
	restored := make(map[string]claudePortableVerifiedRelease, len(previous))
	for architecture, evidence := range previous {
		restored[architecture] = evidence
	}
	updated := make(map[string]claudePortableVerifiedRelease, len(previous)+1)
	for architecture, evidence := range previous {
		updated[architecture] = evidence
	}
	updated[runtime.GOARCH] = claudePortableVerifiedRelease{
		digest: sharedClaudePortableFixtureDigest(t, launcher), size: info.Size(), offlineProbeProfile: 1,
	}
	claudePortableVerifiedReleases[version] = updated
	t.Cleanup(func() {
		if existed {
			claudePortableVerifiedReleases[version] = restored
		} else {
			delete(claudePortableVerifiedReleases, version)
		}
	})
}

func buildSharedClaudePortableFixture(t *testing.T, home string) string {
	t.Helper()
	root := home
	source := filepath.Join(root, "claude.c")
	contents := `#include <dlfcn.h>
#include <stdio.h>
int main(void) {
  void *self = dlopen(NULL, RTLD_NOW);
  if (self == NULL || dlsym(self, "main") == NULL) return 2;
  puts("@anthropic-ai/claude-code");
  return 0;
}
`
	writeSharedClaudePortableFile(t, source, contents)
	versioned := filepath.Join(root, ".local", "share", "claude", "versions", "2.1.210")
	if err := os.MkdirAll(filepath.Dir(versioned), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cc", "-rdynamic", "-o", versioned, source, "-ldl")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build shared Claude fixture: %v: %s", err, output)
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

func writeSharedClaudePortableFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func clearClaudePortableEnvironment(t *testing.T) {
	t.Helper()
	for _, assignment := range os.Environ() {
		name := strings.SplitN(assignment, "=", 2)[0]
		external := map[string]bool{
			"LD_AUDIT": true, "LD_LIBRARY_PATH": true, "LD_PRELOAD": true,
			"NODE_OPTIONS": true, "NODE_PATH": true, "BUN_OPTIONS": true,
			"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true,
			"CURL_CA_BUNDLE": true, "AWS_CA_BUNDLE": true, "REQUESTS_CA_BUNDLE": true,
			"USE_BUILTIN_RIPGREP": true, "BUN_INSTALL": true,
		}
		if !strings.HasPrefix(name, "CLAUDE_") && !strings.HasPrefix(name, "ANTHROPIC_") && !external[name] {
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
	}
}
