package claude

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

func TestClaudePortableRuntimeContribution(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	unsetClaudePortablePrefixes(t, "CLAUDE_", "ANTHROPIC_")
	for _, name := range []string{"LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "BUN_OPTIONS", "BUN_INSTALL", "USE_BUILTIN_RIPGREP", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "AWS_CA_BUNDLE", "REQUESTS_CA_BUNDLE"} {
		unsetClaudePortableEnvironment(t, name)
	}
	home, quota := prepareClaudePortableRuntimeState(t)
	t.Setenv("ANTHROPIC_API_KEY", "environment-secret")
	unsetClaudePortableEnvironment(t, "ANTHROPIC_AUTH_TOKEN")
	unsetClaudePortableEnvironment(t, "ANTHROPIC_BASE_URL")
	unsetClaudePortableEnvironment(t, "CLAUDE_CODE_OAUTH_TOKEN")

	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	launcher := portableClaudeDynamicFixture(t, home)
	_, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("unverified release error = %v, want closure incomplete", err)
	}
	productionDiscovery := discoverClaudePortableRuntime
	discoverClaudePortableRuntime = func(ctx context.Context, gotTarget harnesses.PortableRuntimeTarget, options anthropic.ClaudePortableRuntimeOptions) (harnesses.PortableRuntimeContribution, error) {
		if options.Launcher != launcher || len(options.Arguments) != 0 || !options.InheritsProcessEnvironment {
			t.Fatalf("Claude adapter options = %#v", options)
		}
		contribution, err := portableClaudeFixtureContribution(ctx, gotTarget, launcher)
		contribution.Environment = []harnesses.PortableRuntimeEnvironment{{Name: "ANTHROPIC_API_KEY"}}
		return contribution, err
	}
	t.Cleanup(func() { discoverClaudePortableRuntime = productionDiscovery })
	contribution, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatalf("PortableRuntimeAssets() error = %v", err)
	}
	assertClaudePortableContribution(t, contribution)
	assertClaudePortableOfflineProbe(t, contribution)
	if got := contribution.Environment; len(got) != 1 || got[0].Name != "ANTHROPIC_API_KEY" {
		t.Fatalf("environment names = %#v, want ANTHROPIC_API_KEY", got)
	}
	serialized := fmt.Sprintf("%#v", contribution.Environment)
	for _, forbidden := range []string{"environment-secret", "credential-secret", "quota-secret", home, quota, "ANTHROPIC_API_KEY="} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("environment contribution leaked %q: %s", forbidden, serialized)
		}
	}
	discoverClaudePortableRuntime = productionDiscovery

	_, err = (&Runner{NativeMode: true}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeTargetUnsupported) {
		t.Fatalf("native PortableRuntimeAssets() error = %v, want target unsupported", err)
	}

	unknown := filepath.Join(t.TempDir(), "account-secret-wrapper")
	if err := os.WriteFile(unknown, []byte("#!/bin/sh\n# wrapper-secret\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (&Runner{Binary: unknown}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), unknown) || strings.Contains(err.Error(), "wrapper-secret") {
		t.Fatalf("unknown launcher error = %v, want redacted closure incomplete", err)
	}
	_, err = (&Runner{Binary: "/bin/true"}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("arbitrary dynamic ELF error = %v, want closure incomplete", err)
	}
	fixture := launcher
	resolvedFixture, resolveErr := filepath.EvalSymlinks(fixture)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	fixtureBytes, readErr := os.ReadFile(resolvedFixture)
	if readErr != nil {
		t.Fatal(readErr)
	}
	unrecognizedInstall := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(unrecognizedInstall, fixtureBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (&Runner{Binary: unrecognizedInstall}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("identity-bearing unknown install error = %v, want closure incomplete", err)
	}
	foreignInstall := filepath.Join(t.TempDir(), ".local", "share", "claude", "versions", "2.1.210")
	if err := os.MkdirAll(filepath.Dir(foreignInstall), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignInstall, fixtureBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (&Runner{Binary: foreignInstall}).PortableRuntimeAssets(context.Background(), target)
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), foreignInstall) {
		t.Fatalf("foreign-home install error = %v, want redacted closure incomplete", err)
	}

	discoverClaudePortableRuntime = func(context.Context, harnesses.PortableRuntimeTarget, anthropic.ClaudePortableRuntimeOptions) (harnesses.PortableRuntimeContribution, error) {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: dynamic loader or library closure is incomplete", harnesses.ErrPortableRuntimeClosureIncomplete)
	}
	_, err = (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	discoverClaudePortableRuntime = productionDiscovery
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("incomplete library error = %v, want redacted closure incomplete", err)
	}
}

func portableClaudeFixtureContribution(ctx context.Context, target harnesses.PortableRuntimeTarget, launcher string) (harnesses.PortableRuntimeContribution, error) {
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
		ExactLibraryRoots: portableClaudeFixtureLibraryRoots(), RuntimeLookup: harnesses.PortableRuntimeLookupVerifiedExact,
	})
}

func portableClaudeFixtureLibraryRoots() []harnesses.PortableRuntimeLibrarySearchRoot {
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

func prepareClaudePortableRuntimeState(t *testing.T) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "account-bearing-home")
	configRoot := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(configRoot, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		filepath.Join(configRoot, ".credentials.json"): `{"claudeAiOauth":{"accessToken":"credential-secret"},"mcpOAuth":{"server":"mcp-secret"}}`,
		filepath.Join(configRoot, "settings.json"):     `{"model":"sonnet"}`,
		filepath.Join(configRoot, "cache", "seed"):     "cache-secret",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	quota := filepath.Join(t.TempDir(), "account-bearing-quota.json")
	if err := os.WriteFile(quota, []byte(`{"quota":"quota-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", quota)
	unsetClaudePortableEnvironment(t, "CLAUDE_CONFIG_DIR")
	return home, quota
}

func portableClaudeDynamicFixture(t *testing.T, home string) string {
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
		t.Fatalf("build Claude dynamic fixture: %v: %s", err, output)
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
		t.Fatalf("Claude fixture imports = %#v, want dlopen and dlsym", wanted)
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

func assertClaudePortableOfflineProbe(t *testing.T, contribution harnesses.PortableRuntimeContribution) {
	t.Helper()
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
	output, err := runClaudePortableIsolated(t, root, command, arguments)
	if err != nil || strings.TrimSpace(string(output)) != "@anthropic-ai/claude-code" {
		t.Fatalf("credential-free offline launch probe = %v: %q", err, output)
	}

	removed := ""
	for _, asset := range contribution.Assets {
		if asset.Kind == harnesses.PortableRuntimeAssetSupport && asset.PathKind == harnesses.PortableRuntimePathFile && filepath.Base(asset.Target) == "libc.so.6" {
			removed = filepath.Join(root, filepath.FromSlash(asset.Target))
			break
		}
	}
	if removed == "" {
		t.Fatal("offline launch probe found no required library asset for the negative check")
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	if output, err := runClaudePortableIsolated(t, root, command, arguments); err == nil {
		t.Fatalf("isolated launch succeeded after removing required library %q: %q", removed, output)
	}
}

func runClaudePortableIsolated(t *testing.T, root, command string, arguments []string) ([]byte, error) {
	t.Helper()
	for _, directory := range []string{"dev", "proc", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	guest := func(value string) string {
		return strings.ReplaceAll(value, root, "")
	}
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

func assertClaudePortableContribution(t *testing.T, contribution harnesses.PortableRuntimeContribution) {
	t.Helper()
	if contribution.ClosureClass != harnesses.PortableRuntimeClosureDynamic || contribution.Launch.LoaderTarget == "" || len(contribution.Launch.LibraryRootTargets) == 0 {
		t.Fatalf("dynamic contribution = %#v", contribution.Launch)
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
		for _, root := range contribution.Launch.LibraryRootTargets {
			if asset.Target == root && asset.PathKind == harnesses.PortableRuntimePathTree {
				t.Fatalf("library root %q was emitted as a whole tree", root)
			}
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing portable assets: %#v", wanted)
	}
}

func unsetClaudePortableEnvironment(t *testing.T, name string) {
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

func unsetClaudePortablePrefixes(t *testing.T, prefixes ...string) {
	t.Helper()
	for _, assignment := range os.Environ() {
		name := strings.SplitN(assignment, "=", 2)[0]
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				unsetClaudePortableEnvironment(t, name)
				break
			}
		}
	}
}
