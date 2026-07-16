package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
)

func TestOpenCodePortableRuntimeContribution(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skip("verified OpenCode portable payload is Linux arm64 only")
	}

	t.Run("direct verified layout closure state and constraints", func(t *testing.T) {
		fixture := prepareOpenCodePortableFixture(t)
		paths := seedOpenCodePortableState(t, true, true)
		t.Setenv("OPENCODE_FIXTURE_KEY", "provider-secret-value")
		writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.jsonc"), []byte(`{
  "provider": {"fixture": {"options": {"apiKey": "{env:OPENCODE_FIXTURE_KEY}"}}},
  "model": "{env:OPTIONAL_MODEL}"
}`), 0o600)
		t.Setenv("OPTIONAL_MODEL", "fixture/model")
		launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
		copyOpenCodePortableFile(t, fixture, launcher, 0o700)

		contribution, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
		if err != nil {
			t.Fatalf("PortableRuntimeAssets() error = %v", err)
		}
		if contribution.ClosureClass != harnesses.PortableRuntimeClosureDynamic {
			t.Fatalf("closure class = %q, want dynamic", contribution.ClosureClass)
		}
		if contribution.Launch.EntrypointTarget != opencodePortableEntrypointTarget ||
			contribution.Launch.LoaderTarget != opencodePortableLoaderTarget ||
			!reflect.DeepEqual(contribution.Launch.LibraryRootTargets, []string{opencodePortableLibraryTarget}) ||
			contribution.Launch.InterpreterTarget != "" || len(contribution.Launch.RuntimeArgs) != 0 {
			t.Fatalf("launch recipe = %#v", contribution.Launch)
		}
		assets := openCodePortableAssetsByTarget(contribution.Assets)
		for target, want := range map[string]struct {
			kind     harnesses.PortableRuntimeAssetKind
			pathKind harnesses.PortableRuntimePathKind
		}{
			opencodePortableEntrypointTarget: {harnesses.PortableRuntimeAssetExecutable, harnesses.PortableRuntimePathFile},
			opencodePortableLoaderTarget:     {harnesses.PortableRuntimeAssetSupport, harnesses.PortableRuntimePathFile},
			opencodePortableConfigTarget:     {harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathTree},
			opencodePortableAuthTarget:       {harnesses.PortableRuntimeAssetCredential, harnesses.PortableRuntimePathFile},
			opencodePortableCacheTarget:      {harnesses.PortableRuntimeAssetCache, harnesses.PortableRuntimePathFile},
		} {
			asset, exists := assets[target]
			if !exists || asset.Kind != want.kind || asset.PathKind != want.pathKind || len(asset.ContentSHA256) != sha256.Size*2 {
				t.Errorf("asset %q = %#v, want %q/%q with digest", target, asset, want.kind, want.pathKind)
			}
		}
		libraryCount := 0
		for _, asset := range contribution.Assets {
			if strings.HasPrefix(asset.Target, opencodePortableLibraryTarget+"/") {
				libraryCount++
				if asset.Kind != harnesses.PortableRuntimeAssetSupport || asset.PathKind != harnesses.PortableRuntimePathFile || asset.Executable {
					t.Errorf("exact library asset = %#v", asset)
				}
			}
		}
		if libraryCount == 0 {
			t.Fatal("dynamic closure emitted no exact library files")
		}
		wantEnvironment := []harnesses.PortableRuntimeEnvironment{{Name: "OPENCODE_FIXTURE_KEY"}, {Name: "OPTIONAL_MODEL"}}
		if !reflect.DeepEqual(contribution.Environment, wantEnvironment) {
			t.Fatalf("environment = %#v, want %#v", contribution.Environment, wantEnvironment)
		}
		if strings.Contains(fmt.Sprint(contribution.Environment), "provider-secret-value") || strings.Contains(fmt.Sprint(contribution.ExecutionConstraints), paths.home) {
			t.Fatal("contribution leaked a secret value or host path into public environment policy")
		}
		assertOpenCodePortableConstraints(t, contribution.ExecutionConstraints)

		command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand("/runtime", contribution, []string{"run", "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		if command != "/runtime/"+opencodePortableLoaderTarget || !reflect.DeepEqual(arguments, []string{
			"--library-path", "/runtime/" + opencodePortableLibraryTarget,
			"/runtime/" + opencodePortableEntrypointTarget, "run", "fixture",
		}) {
			t.Fatalf("launch command = %q %q", command, arguments)
		}
	})

	for _, cached := range []bool{true, false} {
		name := map[bool]string{true: "npm cached payload", false: "npm nested platform payload"}[cached]
		t.Run(name, func(t *testing.T) {
			fixture := prepareOpenCodePortableFixture(t)
			paths := seedOpenCodePortableState(t, true, false)
			wrapper := filepath.Join(t.TempDir(), "node_modules", "opencode-ai")
			launcher := seedOpenCodePortableNPMWrapper(t, wrapper)
			if cached {
				copyOpenCodePortableFile(t, fixture, filepath.Join(wrapper, "bin", ".opencode"), 0o700)
				// A malformed nested fallback proves the cached payload is preferred.
				writeOpenCodePortableFile(t, filepath.Join(wrapper, "node_modules", "opencode-linux-arm64", "bin", "opencode"), []byte("not an ELF"), 0o700)
			} else {
				seedOpenCodePortableNPMPlatform(t, wrapper, fixture, "opencode-linux-arm64")
			}
			contribution, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
			if err != nil {
				t.Fatalf("PortableRuntimeAssets() npm error = %v", err)
			}
			if openCodePortableAssetsByTarget(contribution.Assets)[opencodePortableEntrypointTarget].ContentSHA256 != opencodePortableVerified.contentSHA256 {
				t.Fatal("npm payload did not retain verified content identity")
			}
			_ = paths
		})
	}

	t.Run("inline config credential fallback", func(t *testing.T) {
		fixture := prepareOpenCodePortableFixture(t)
		paths := seedOpenCodePortableState(t, false, false)
		writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"provider":{"fixture":{"options":{"apiKey":"embedded-fixture-key"}}}}`), 0o600)
		launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
		copyOpenCodePortableFile(t, fixture, launcher, 0o700)
		contribution, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
		if err != nil {
			t.Fatalf("PortableRuntimeAssets() inline credential error = %v", err)
		}
		if _, exists := openCodePortableAssetsByTarget(contribution.Assets)[opencodePortableAuthTarget]; exists {
			t.Fatal("absent optional auth file was emitted")
		}
		if _, exists := openCodePortableAssetsByTarget(contribution.Assets)[opencodePortableCacheTarget]; exists {
			t.Fatal("absent optional cache tree was emitted")
		}
	})

	t.Run("models cache accepts only bundled provider selectors", func(t *testing.T) {
		for provider := range opencodePortableBundledProviders {
			data, err := json.Marshal(map[string]any{
				"fixture": map[string]any{
					"npm": provider,
					"models": map[string]any{
						"fixture-model": map[string]any{"provider": map[string]any{"npm": provider}},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := validateOpenCodePortableModelsCache(data); err != nil {
				t.Errorf("bundled provider %q rejected: %v", provider, err)
			}
		}
		if err := validateOpenCodePortableModelsCache([]byte(`{"fixture":{"models":{"model":{}}}}`)); err != nil {
			t.Errorf("bundled default selector rejected: %v", err)
		}
	})

	t.Run("reject incomplete dynamic library closure", func(t *testing.T) {
		fixture := prepareOpenCodePortableFixture(t)
		paths := seedOpenCodePortableState(t, true, false)
		launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
		copyOpenCodePortableFile(t, fixture, launcher, 0o700)
		opencodePortableVerified.libraryRoots = []harnesses.PortableRuntimeLibrarySearchRoot{{
			Source: t.TempDir(), Target: opencodePortableLibraryTarget,
		}}

		_, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
		assertOpenCodePortableRedactedError(t, err, paths.home)
	})

	negative := []struct {
		name   string
		mutate func(*testing.T, string, openCodePortableTestPaths) string
	}{
		{
			name: "binary override",
			mutate: func(t *testing.T, fixture string, paths openCodePortableTestPaths) string {
				t.Setenv("OPENCODE_BIN_PATH", "/untrusted/override")
				launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
				copyOpenCodePortableFile(t, fixture, launcher, 0o700)
				return launcher
			},
		},
		{
			name: "unknown native layout",
			mutate: func(t *testing.T, fixture string, _ openCodePortableTestPaths) string {
				launcher := filepath.Join(t.TempDir(), "bin", "opencode")
				copyOpenCodePortableFile(t, fixture, launcher, 0o700)
				return launcher
			},
		},
		{
			name: "unverified content",
			mutate: func(t *testing.T, fixture string, paths openCodePortableTestPaths) string {
				launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
				copyOpenCodePortableFile(t, fixture, launcher, 0o700)
				file, err := os.OpenFile(launcher, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = file.Write([]byte("x"))
				_ = file.Close()
				return launcher
			},
		},
		{
			name: "symlinked direct payload",
			mutate: func(t *testing.T, fixture string, paths openCodePortableTestPaths) string {
				launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
				if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(fixture, launcher); err != nil {
					t.Fatal(err)
				}
				return launcher
			},
		},
		{
			name: "unsupported npm metadata",
			mutate: func(t *testing.T, fixture string, _ openCodePortableTestPaths) string {
				wrapper := filepath.Join(t.TempDir(), "node_modules", "opencode-ai")
				launcher := seedOpenCodePortableNPMWrapper(t, wrapper)
				copyOpenCodePortableFile(t, fixture, filepath.Join(wrapper, "bin", ".opencode"), 0o700)
				writeOpenCodePortableJSON(t, filepath.Join(wrapper, "package.json"), map[string]any{
					"name": "opencode-ai", "version": "1.14.32", "bin": map[string]string{"opencode": "./bin/opencode"},
					"optionalDependencies": map[string]string{"opencode-linux-arm64": opencodePortableVersion},
				})
				return launcher
			},
		},
		{
			name: "hoisted npm platform package",
			mutate: func(t *testing.T, fixture string, _ openCodePortableTestPaths) string {
				root := t.TempDir()
				wrapper := filepath.Join(root, "node_modules", "opencode-ai")
				launcher := seedOpenCodePortableNPMWrapper(t, wrapper)
				platform := filepath.Join(root, "node_modules", "opencode-linux-arm64")
				seedOpenCodePortablePlatformAt(t, platform, fixture, "opencode-linux-arm64")
				return launcher
			},
		},
		{
			name: "musl npm platform package",
			mutate: func(t *testing.T, fixture string, _ openCodePortableTestPaths) string {
				wrapper := filepath.Join(t.TempDir(), "node_modules", "opencode-ai")
				launcher := seedOpenCodePortableNPMWrapper(t, wrapper)
				seedOpenCodePortableNPMPlatform(t, wrapper, fixture, "opencode-linux-arm64-musl")
				return launcher
			},
		},
	}
	for _, test := range negative {
		t.Run("reject "+test.name, func(t *testing.T) {
			fixture := prepareOpenCodePortableFixture(t)
			paths := seedOpenCodePortableState(t, true, false)
			launcher := test.mutate(t, fixture, paths)
			_, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
			assertOpenCodePortableRedactedError(t, err, paths.home, "provider-secret-value", "untrusted/override")
		})
	}

	stateNegative := []struct {
		name  string
		setup func(*testing.T, openCodePortableTestPaths)
	}{
		{name: "missing required config tree", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			if err := os.RemoveAll(paths.config); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "external file token", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"provider":{"fixture":{"options":{"apiKey":"{file:/private/key}"}}}}`), 0o600)
		}},
		{name: "missing credential evidence", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			if err := os.Remove(filepath.Join(paths.data, "auth.json")); err != nil {
				t.Fatal(err)
			}
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"model":"fixture/model"}`), 0o600)
		}},
		{name: "empty auth credential record", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.data, "auth.json"), []byte(`{"fixture":{"type":"api","key":""}}`), 0o600)
		}},
		{name: "forbidden inherited environment", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"model":"{env:LD_PRELOAD}"}`), 0o600)
		}},
		{name: "config node modules", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "node_modules", "fixture", "index.js"), []byte(`module.exports = {}`), 0o600)
		}},
		{name: "config plugin directory", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "plugins", "fixture.js"), []byte(`export default {}`), 0o600)
		}},
		{name: "config executable file", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "command", "fixture.sh"), []byte("#!/bin/sh\n"), 0o700)
		}},
		{name: "config command markdown", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "commands", "fixture.md"), []byte("run `unverified-command`\n"), 0o600)
		}},
		{name: "legacy extensionless config", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "config"), []byte("plugin = ['file:///unverified/plugin.js']\n"), 0o600)
		}},
		{name: "config plugin declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"plugin":["file:///unverified/plugin.js"]}`), 0o600)
		}},
		{name: "config mcp declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.jsonc"), []byte(`{"mcp":{"fixture":{"type":"local","command":["unverified"]}}}`), 0o600)
		}},
		{name: "config shell declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"shell":"/unverified/shell"}`), 0o600)
		}},
		{name: "config command declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"command":{"fixture":{"template":"$(unverified)"}}}`), 0o600)
		}},
		{name: "config formatter declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"formatter":{"fixture":{"command":["unverified"]}}}`), 0o600)
		}},
		{name: "config lsp declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"lsp":{"fixture":{"command":["unverified"]}}}`), 0o600)
		}},
		{name: "config skills declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"skills":{"paths":["/unverified/skills"]}}`), 0o600)
		}},
		{name: "config instructions declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"instructions":["https://unverified.invalid/instructions"]}`), 0o600)
		}},
		{name: "config provider package declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"provider":{"fixture":{"npm":"unverified-provider"}}}`), 0o600)
		}},
		{name: "config model package declaration", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"provider":{"fixture":{"models":{"fixture":{"provider":{"npm":"file:///unverified/provider.js"}}}}}}`), 0o600)
		}},
		{name: "wellknown remote auth record", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.data, "auth.json"), []byte(`{"remote":{"type":"wellknown","key":"https://unverified.invalid/config","token":"wellknown-secret"}}`), 0o600)
		}},
		{name: "cache unbundled provider package", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.cache, "models.json"), []byte(`{"fixture":{"npm":"unverified-provider@1.0.0","models":{}}}`), 0o600)
		}},
		{name: "cache file provider package", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.cache, "models.json"), []byte(`{"fixture":{"models":{"fixture":{"provider":{"npm":"file:///unverified/provider.js"}}}}}`), 0o600)
		}},
		{name: "cache malformed provider selector", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.cache, "models.json"), []byte(`{"fixture":{"npm":["@ai-sdk/openai-compatible"],"models":{}}}`), 0o600)
		}},
		{name: "cache executable directory", setup: func(t *testing.T, paths openCodePortableTestPaths) {
			writeOpenCodePortableFile(t, filepath.Join(paths.cache, "bin", "rg"), []byte("unverified"), 0o700)
		}},
	}
	for _, test := range stateNegative {
		t.Run("reject "+test.name, func(t *testing.T) {
			fixture := prepareOpenCodePortableFixture(t)
			paths := seedOpenCodePortableState(t, true, false)
			launcher := filepath.Join(paths.home, ".opencode", "bin", "opencode")
			copyOpenCodePortableFile(t, fixture, launcher, 0o700)
			test.setup(t, paths)
			_, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
			assertOpenCodePortableRedactedError(t, err, paths.home, "/private/key", "wellknown-secret", "unverified-provider")
		})
	}

	t.Run("reject unsupported target", func(t *testing.T) {
		_, err := (&Runner{Binary: "/does/not/matter"}).PortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: "amd64"})
		if !errors.Is(err, harnesses.ErrPortableRuntimeTargetUnsupported) {
			t.Fatalf("error = %v, want target unsupported", err)
		}
	})

	t.Run("reject nil context", func(t *testing.T) {
		_, err := (&Runner{}).PortableRuntimeAssets(nil, portableOpenCodeTarget())
		assertOpenCodePortableRedactedError(t, err)
	})

	t.Run("reject missing offline probe profile", func(t *testing.T) {
		prepareOpenCodePortableFixture(t)
		opencodePortableVerified.offlineProbeProfile = 0
		_, err := (&Runner{}).PortableRuntimeAssets(context.Background(), portableOpenCodeTarget())
		assertOpenCodePortableRedactedError(t, err)
	})
}

func TestOpenCodePortableRuntimeVerifiedReleaseEvidence(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("OpenCode verified release evidence is Linux-only")
	}
	if runtime.GOARCH != "arm64" {
		if opencodePortableVerified.goarch == runtime.GOARCH {
			t.Fatalf("OpenCode %s unexpectedly claims unprobed %s verified-exact evidence", opencodePortableVersion, runtime.GOARCH)
		}
		return
	}

	candidate := os.Getenv("FIZEAU_OPENCODE_PORTABLE_EVIDENCE_BIN")
	explicit := candidate != ""
	if !explicit {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("user home unavailable: %v", err)
		}
		candidate = filepath.Join(home, ".opencode", "bin", "opencode")
	}
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("reviewed OpenCode 1.14.33 release is not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	evidence := cloneOpenCodePortableEvidence(opencodePortableVerified)
	if evidence.goos != "linux" || evidence.goarch != "arm64" ||
		evidence.contentSHA256 != "66ef27d163a57834a216e0f54a30bd20ea0b82982cd4efbece6a729ee6458e97" ||
		evidence.size != 171116864 || evidence.buildID != "8c5e2642b94bf1eed8184712a2b0f441196585fa" ||
		evidence.interpreter != "/lib/ld-linux-aarch64.so.1" || evidence.offlineProbeProfile != 1 {
		t.Fatalf("release evidence audit metadata is incomplete: %#v", evidence)
	}
	if info.Size() != evidence.size {
		if explicit {
			t.Fatalf("explicit evidence binary size = %d, want %d", info.Size(), evidence.size)
		}
		t.Skipf("installed OpenCode is not the reviewed %s release", opencodePortableVersion)
	}
	payload, err := validateOpenCodePortablePayload(candidate, evidence)
	if err != nil {
		if explicit {
			t.Fatal(err)
		}
		t.Skipf("installed OpenCode is not the reviewed %s release: %v", opencodePortableVersion, err)
	}
	roots, err := opencodePortableExactLibraryRoots(evidence)
	if err != nil {
		t.Fatal(err)
	}
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: "arm64"}
	contribution, err := harnesses.AnalyzePortableRuntimeDynamicClosure(context.Background(), target, harnesses.PortableRuntimeDynamicClosureRequest{
		EntrypointSource: payload, EntrypointTarget: opencodePortableEntrypointTarget,
		LoaderTarget: opencodePortableLoaderTarget, ExactLibraryRoots: roots,
		RuntimeLookup: harnesses.PortableRuntimeLookupVerifiedExact,
	})
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
	for _, directory := range []string{"config/opencode", "dev", "proc", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand(root, contribution, []string{"--pure", "--version"})
	if err != nil {
		t.Fatal(err)
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
	bwrapArguments := []string{
		"--unshare-all", "--die-with-parent", "--ro-bind", root, "/", "--dev", "/dev", "--proc", "/proc",
		"--tmpfs", "/tmp", "--dir", "/tmp/data", "--dir", "/tmp/cache", "--dir", "/tmp/state", guest(command),
	}
	bwrapArguments = append(bwrapArguments, guestArguments...)
	probe := exec.Command(bwrap, bwrapArguments...)
	probe.Env = []string{
		"HOME=/nonexistent", "PATH=/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm-256color", "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
		"XDG_CONFIG_HOME=/config", "XDG_DATA_HOME=/tmp/data", "XDG_CACHE_HOME=/tmp/cache", "XDG_STATE_HOME=/tmp/state", "TMPDIR=/tmp",
		"OPENCODE_DISABLE_PROJECT_CONFIG=true", "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true",
		"OPENCODE_DISABLE_LSP_DOWNLOAD=true", "OPENCODE_DISABLE_EXTERNAL_SKILLS=true", "OPENCODE_DISABLE_MODELS_FETCH=true",
		"OPENCODE_DISABLE_CLAUDE_CODE=true",
	}
	output, err := probe.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != opencodePortableVersion {
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

type openCodePortableTestPaths struct {
	home   string
	config string
	data   string
	cache  string
}

func portableOpenCodeTarget() harnesses.PortableRuntimeTarget {
	return harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
}

func prepareOpenCodePortableFixture(t *testing.T) string {
	t.Helper()
	candidate := "/bin/true"
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		t.Skip("host does not provide /bin/true dynamic fixture")
	}
	buildID, interpreter, err := inspectOpenCodePortableELFEvidence(candidate)
	if err != nil || interpreter == "" {
		t.Skipf("host dynamic fixture lacks audited evidence: %v", err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(candidate)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := filepath.EvalSymlinks(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	previous := cloneOpenCodePortableEvidence(opencodePortableVerified)
	opencodePortableVerified = opencodePortableEvidence{
		goos: "linux", goarch: runtime.GOARCH, contentSHA256: digest, size: info.Size(), buildID: buildID, interpreter: interpreter,
		offlineProbeProfile: 1,
		libraryRoots:        []harnesses.PortableRuntimeLibrarySearchRoot{{Source: filepath.Dir(loader), Target: opencodePortableLibraryTarget}},
	}
	t.Cleanup(func() { opencodePortableVerified = previous })
	return candidate
}

func seedOpenCodePortableState(t *testing.T, auth, cache bool) openCodePortableTestPaths {
	t.Helper()
	root := t.TempDir()
	paths := openCodePortableTestPaths{
		home: filepath.Join(root, "home"), config: filepath.Join(root, "config", "opencode"),
		data: filepath.Join(root, "data", "opencode"), cache: filepath.Join(root, "cache", "opencode"),
	}
	for _, directory := range []string{paths.home, paths.config, paths.data} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", paths.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(paths.config))
	t.Setenv("XDG_DATA_HOME", filepath.Dir(paths.data))
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(paths.cache))
	writeOpenCodePortableFile(t, filepath.Join(paths.config, "opencode.json"), []byte(`{"model":"fixture/model"}`), 0o600)
	if auth {
		writeOpenCodePortableFile(t, filepath.Join(paths.data, "auth.json"), []byte(`{"fixture":{"type":"api","key":"fixture-secret"}}`), 0o600)
	}
	if cache {
		writeOpenCodePortableFile(t, filepath.Join(paths.cache, "models.json"), []byte(`{"fixture":{"id":"fixture","name":"Fixture","env":[],"npm":"@ai-sdk/openai-compatible","models":{}}}`), 0o600)
	}
	return paths
}

func seedOpenCodePortableNPMWrapper(t *testing.T, root string) string {
	t.Helper()
	launcher := filepath.Join(root, "bin", "opencode")
	writeOpenCodePortableFile(t, launcher, []byte("#!/usr/bin/env node\n// exact fixture wrapper\n"), 0o700)
	writeOpenCodePortableJSON(t, filepath.Join(root, "package.json"), map[string]any{
		"name": "opencode-ai", "version": opencodePortableVersion,
		"bin":                  map[string]string{"opencode": "./bin/opencode"},
		"optionalDependencies": map[string]string{"opencode-linux-arm64": opencodePortableVersion, "opencode-linux-arm64-musl": opencodePortableVersion},
	})
	return launcher
}

func seedOpenCodePortableNPMPlatform(t *testing.T, wrapper, fixture, name string) {
	t.Helper()
	seedOpenCodePortablePlatformAt(t, filepath.Join(wrapper, "node_modules", name), fixture, name)
}

func seedOpenCodePortablePlatformAt(t *testing.T, root, fixture, name string) {
	t.Helper()
	writeOpenCodePortableJSON(t, filepath.Join(root, "package.json"), map[string]any{
		"name": name, "version": opencodePortableVersion, "os": []string{"linux"}, "cpu": []string{"arm64"},
	})
	copyOpenCodePortableFile(t, fixture, filepath.Join(root, "bin", "opencode"), 0o700)
}

func assertOpenCodePortableConstraints(t *testing.T, got harnesses.PortableRuntimeExecutionConstraints) {
	t.Helper()
	if !reflect.DeepEqual(got.FixedArguments, []string{"--pure"}) {
		t.Errorf("fixed arguments = %#v", got.FixedArguments)
	}
	if !reflect.DeepEqual(got.ReadOnlyPaths, []harnesses.PortableRuntimeGuestPath{{Scope: harnesses.PortableRuntimeGuestPathRuntime, Target: opencodePortableConfigTarget}}) {
		t.Errorf("read-only paths = %#v", got.ReadOnlyPaths)
	}
	if !reflect.DeepEqual(got.RequiredAbsentPaths, []harnesses.PortableRuntimeGuestPath{{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".opencode"}}) {
		t.Errorf("required-absent paths = %#v", got.RequiredAbsentPaths)
	}
	treatments := make(map[string]harnesses.PortableRuntimeEnvironmentConstraint)
	for _, treatment := range got.Environment {
		treatments[treatment.Name] = treatment
	}
	config := treatments["XDG_CONFIG_HOME"]
	if config.Kind != harnesses.PortableRuntimeEnvironmentGuestPath || config.GuestPath != (harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathRuntime, Target: "config"}) {
		t.Errorf("XDG_CONFIG_HOME constraint = %#v", config)
	}
	for _, name := range []string{"XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "TMPDIR"} {
		if _, overridden := treatments[name]; overridden {
			t.Errorf("%s must retain the activation-owned generated scope", name)
		}
	}
	for _, name := range []string{"OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_DEFAULT_PLUGINS", "OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_MODELS_FETCH", "OPENCODE_DISABLE_CLAUDE_CODE"} {
		if treatments[name].Kind != harnesses.PortableRuntimeEnvironmentFixedTrue {
			t.Errorf("%s treatment = %#v", name, treatments[name])
		}
	}
	for _, name := range []string{"OPENCODE_BIN_PATH", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CONFIG_DIR", "OPENCODE_MODELS_PATH", "OPENCODE_MODELS_URL", "LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "BUN_OPTIONS", "BUN_INSTALL"} {
		if treatments[name].Kind != harnesses.PortableRuntimeEnvironmentUnset {
			t.Errorf("%s treatment = %#v", name, treatments[name])
		}
	}
	if len(treatments) != 21 {
		t.Errorf("environment treatment count = %d, want 21: %#v", len(treatments), got.Environment)
	}
}

func assertOpenCodePortableRedactedError(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("error = %v, want typed closure error", err)
	}
	message := err.Error()
	for _, value := range forbidden {
		if value != "" && strings.Contains(message, value) {
			t.Fatalf("error leaked %q: %v", value, err)
		}
	}
}

func openCodePortableAssetsByTarget(assets []harnesses.PortableRuntimeAsset) map[string]harnesses.PortableRuntimeAsset {
	result := make(map[string]harnesses.PortableRuntimeAsset, len(assets))
	for _, asset := range assets {
		result[asset.Target] = asset
	}
	return result
}

func writeOpenCodePortableJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeOpenCodePortableFile(t, path, data, 0o600)
}

func copyOpenCodePortableFile(t *testing.T, source, destination string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	writeOpenCodePortableFile(t, destination, data, mode)
}

func writeOpenCodePortableFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
