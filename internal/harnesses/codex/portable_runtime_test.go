package codex

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
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

func TestCodexPortableRuntimeContribution(t *testing.T) {
	requireCodexPortableRuntimeLinux(t)
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	fixture := buildCodexPortableStaticFixture(t)
	linkRoot := t.TempDir()
	firstLink := filepath.Join(linkRoot, "codex-versioned")
	launcher := filepath.Join(linkRoot, "codex")
	if err := os.Symlink(fixture, firstLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(firstLink, launcher); err != nil {
		t.Fatal(err)
	}

	home, quota := seedCodexPortableState(t, true)
	t.Setenv("CUSTOM_PROVIDER_KEY", "provider-secret-value")
	t.Setenv("CUSTOM_HEADER", "header-secret-value")
	t.Setenv("MCP_TOKEN", "mcp-secret-value")
	t.Setenv("MCP_HEADER", "mcp-header-secret-value")
	t.Setenv("MCP_REQUIRED", "mcp-required-secret-value")
	t.Setenv("CODEX_API_KEY", "codex-api-secret-value")
	config := `[projects."/account-bearing/host/project"]
trust_level = "trusted"

[model_providers.fixture]
name = "fixture"
env_key = "CUSTOM_PROVIDER_KEY"
env_http_headers = { "X-Fixture" = "CUSTOM_HEADER" }

[mcp_servers.remote]
url = "https://example.invalid/mcp"
bearer_token_env_var = "MCP_TOKEN"
env_http_headers = { "X-MCP" = "MCP_HEADER" }
env_vars = [{ name = "MCP_TARGET", source = "MCP_REQUIRED" }, "MCP_OPTIONAL"]
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	contribution, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatalf("PortableRuntimeAssets() error = %v", err)
	}
	if contribution.ClosureClass != harnesses.PortableRuntimeClosureStatic {
		t.Fatalf("closure class = %q, want static", contribution.ClosureClass)
	}
	if contribution.Launch.EntrypointTarget != codexPortableEntrypointTarget || contribution.Launch.InterpreterTarget != "" || contribution.Launch.LoaderTarget != "" || len(contribution.Launch.RuntimeArgs) != 0 || len(contribution.Launch.LibraryRootTargets) != 0 {
		t.Fatalf("static launch recipe = %#v", contribution.Launch)
	}
	assets := codexPortableAssetsByTarget(contribution.Assets)
	wantKinds := map[string]harnesses.PortableRuntimeAssetKind{
		codexPortableEntrypointTarget: harnesses.PortableRuntimeAssetExecutable,
		codexPortableAuthTarget:       harnesses.PortableRuntimeAssetCredential,
		codexPortableConfigTarget:     harnesses.PortableRuntimeAssetConfig,
		codexPortableQuotaTarget:      harnesses.PortableRuntimeAssetQuota,
		codexPortableModelsTarget:     harnesses.PortableRuntimeAssetCache,
		codexPortableCacheTarget:      harnesses.PortableRuntimeAssetCache,
	}
	if len(assets) != len(wantKinds) {
		t.Fatalf("asset count = %d, want %d: %#v", len(assets), len(wantKinds), contribution.Assets)
	}
	for target, kind := range wantKinds {
		asset, ok := assets[target]
		if !ok || asset.Kind != kind {
			t.Errorf("asset %q = %#v, want kind %q", target, asset, kind)
			continue
		}
		if len(asset.ContentSHA256) != sha256.Size*2 {
			t.Errorf("asset %q digest = %q", target, asset.ContentSHA256)
		}
	}
	if got := assets[codexPortableEntrypointTarget].Source; got != fixture {
		t.Fatalf("resolved executable source = %q, want %q", got, fixture)
	}
	wantEnvironment := []harnesses.PortableRuntimeEnvironment{
		{Name: "CODEX_API_KEY"}, {Name: "CUSTOM_HEADER"}, {Name: "CUSTOM_PROVIDER_KEY"},
		{Name: "MCP_HEADER"}, {Name: "MCP_REQUIRED"}, {Name: "MCP_TOKEN"},
	}
	if !reflect.DeepEqual(contribution.Environment, wantEnvironment) {
		t.Fatalf("environment = %#v, want %#v", contribution.Environment, wantEnvironment)
	}
	for _, secret := range []string{"provider-secret-value", "header-secret-value", "mcp-secret-value", "/account-bearing/host/project", quota} {
		if strings.Contains(fmt.Sprint(contribution.Environment), secret) {
			t.Fatalf("environment leaked value %q", secret)
		}
	}
	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand("/runtime", contribution, []string{"exec", "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/runtime/"+codexPortableEntrypointTarget || !reflect.DeepEqual(arguments, []string{"exec", "fixture"}) {
		t.Fatalf("launch command = %q %q", command, arguments)
	}
}

func TestCodexPortableRuntimeContributionNPMLayout(t *testing.T) {
	requireCodexPortableRuntimeLinux(t)
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("official Codex npm packages support Linux amd64 and arm64")
	}
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	fixture := buildCodexPortableStaticFixture(t)
	alias, triple, err := codexPortableNPMPlatform(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, nested := range []bool{true, false} {
		t.Run(map[bool]string{true: "nested", false: "hoisted"}[nested], func(t *testing.T) {
			root := t.TempDir()
			packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
			binRoot := filepath.Join(root, "node_modules", ".bin")
			if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(binRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			shim := filepath.Join(packageRoot, "bin", "codex.js")
			if err := os.WriteFile(shim, []byte("#!/usr/bin/env node\n// fixture shim\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			launcher := filepath.Join(binRoot, "codex")
			if err := os.Symlink(filepath.Join("..", "@openai", "codex", "bin", "codex.js"), launcher); err != nil {
				t.Fatal(err)
			}
			writeCodexPortableJSON(t, filepath.Join(packageRoot, "package.json"), map[string]any{
				"name": "@openai/codex", "version": "1.2.3", "bin": map[string]string{"codex": "bin/codex.js"},
				"optionalDependencies": map[string]string{alias: "npm:@openai/codex@1.2.3-" + strings.TrimPrefix(alias, "@openai/codex-")},
			})
			platformRoot := filepath.Join(filepath.Dir(packageRoot), strings.TrimPrefix(alias, "@openai/"))
			if nested {
				platformRoot = filepath.Join(packageRoot, "node_modules", "@openai", strings.TrimPrefix(alias, "@openai/"))
			}
			payload := filepath.Join(platformRoot, "vendor", triple, "codex", "codex")
			if err := os.MkdirAll(filepath.Dir(payload), 0o700); err != nil {
				t.Fatal(err)
			}
			copyCodexPortableFile(t, fixture, payload, 0o700)
			writeCodexPortableJSON(t, filepath.Join(platformRoot, "package.json"), map[string]any{
				"name": "@openai/codex", "version": "1.2.3-" + strings.TrimPrefix(alias, "@openai/codex-"),
			})
			seedCodexPortableState(t, false)

			contribution, err := (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
			if err != nil {
				t.Fatalf("PortableRuntimeAssets() npm error = %v", err)
			}
			asset := codexPortableAssetsByTarget(contribution.Assets)[codexPortableEntrypointTarget]
			resolvedPayload, err := filepath.EvalSymlinks(payload)
			if err != nil {
				t.Fatal(err)
			}
			if asset.Source != resolvedPayload || contribution.ClosureClass != harnesses.PortableRuntimeClosureStatic {
				t.Fatalf("npm contribution entrypoint = %#v", asset)
			}
		})
	}
}

func TestCodexPortableRuntimeOptionalStateAndCredentialFallback(t *testing.T) {
	requireCodexPortableRuntimeLinux(t)
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	fixture := buildCodexPortableStaticFixture(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv(codexAuthPathEnv, filepath.Join(home, "missing-auth.json"))
	t.Setenv(codexQuotaCacheEnv, filepath.Join(home, "missing-quota.json"))
	t.Setenv("OPENAI_API_KEY", "credential-fallback-secret")
	unsetCodexPortableEnvironment(t, "CODEX_API_KEY")

	contribution, err := (&Runner{Binary: fixture}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatalf("optional state analysis error = %v", err)
	}
	if len(contribution.Assets) != 1 || contribution.Assets[0].Target != codexPortableEntrypointTarget {
		t.Fatalf("optional state assets = %#v, want executable only", contribution.Assets)
	}
	if !reflect.DeepEqual(contribution.Environment, []harnesses.PortableRuntimeEnvironment{{Name: "OPENAI_API_KEY"}}) {
		t.Fatalf("credential fallback environment = %#v", contribution.Environment)
	}

	t.Setenv("OPENAI_API_KEY", "")
	if _, err := (&Runner{Binary: fixture}).PortableRuntimeAssets(context.Background(), target); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("missing credential error = %v, want closure incomplete", err)
	}
}

func TestCodexPortableRuntimeRejectsIncompleteLayoutsAndState(t *testing.T) {
	requireCodexPortableRuntimeLinux(t)
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	fixture := buildCodexPortableStaticFixture(t)

	t.Run("dynamic executable", func(t *testing.T) {
		seedCodexPortableState(t, false)
		dynamic := findCodexPortableDynamicFixture(t)
		if _, err := (&Runner{Binary: dynamic}).PortableRuntimeAssets(context.Background(), target); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("dynamic executable error = %v", err)
		}
	})
	t.Run("wrong architecture", func(t *testing.T) {
		seedCodexPortableState(t, false)
		wrong := filepath.Join(t.TempDir(), "codex")
		copyCodexPortableFile(t, fixture, wrong, 0o700)
		mutateCodexPortableELFMachine(t, wrong)
		if _, err := (&Runner{Binary: wrong}).PortableRuntimeAssets(context.Background(), target); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("wrong architecture error = %v", err)
		}
	})
	t.Run("unknown wrapper redacts", func(t *testing.T) {
		seedCodexPortableState(t, false)
		root := filepath.Join(t.TempDir(), "account-secret-root")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		wrapper := filepath.Join(root, "codex")
		secret := "wrapper-secret-value"
		if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n"+secret+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := (&Runner{Binary: wrapper}).PortableRuntimeAssets(context.Background(), target)
		if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), secret) {
			t.Fatalf("unknown wrapper error = %v", err)
		}
	})
	t.Run("incomplete npm", func(t *testing.T) {
		seedCodexPortableState(t, false)
		root := t.TempDir()
		packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
		if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		launcher := filepath.Join(packageRoot, "bin", "codex.js")
		if err := os.WriteFile(launcher, []byte("#!/usr/bin/env node\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		alias, _, err := codexPortableNPMPlatform(target)
		if err != nil {
			t.Fatal(err)
		}
		writeCodexPortableJSON(t, filepath.Join(packageRoot, "package.json"), map[string]any{
			"name": "@openai/codex", "version": "secret-version", "bin": map[string]string{"codex": "bin/codex.js"},
			"optionalDependencies": map[string]string{alias: "npm:@openai/codex@secret-version-" + strings.TrimPrefix(alias, "@openai/codex-")},
		})
		_, err = (&Runner{Binary: launcher}).PortableRuntimeAssets(context.Background(), target)
		if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "secret-version") {
			t.Fatalf("incomplete npm error = %v", err)
		}
	})

	for _, test := range []struct {
		name string
		make func(t *testing.T, home, quota string)
	}{
		{name: "auth symlink", make: func(t *testing.T, home, _ string) {
			external := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(external, []byte("credential-secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, filepath.Join(home, "auth.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "config directory", make: func(t *testing.T, home, _ string) {
			if err := os.Mkdir(filepath.Join(home, "config.toml"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "quota directory", make: func(t *testing.T, _, quota string) {
			if err := os.Remove(quota); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(quota, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "cache file", make: func(t *testing.T, home, _ string) {
			if err := os.RemoveAll(filepath.Join(home, "cache")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "cache"), []byte("cache-secret"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, quota := seedCodexPortableState(t, true)
			test.make(t, home, quota)
			_, err := (&Runner{Binary: fixture}).PortableRuntimeAssets(context.Background(), target)
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), home) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid state error = %v", err)
			}
		})
	}
}

func TestCodexPortableRuntimeConfigEnvironmentValidation(t *testing.T) {
	requireCodexPortableRuntimeLinux(t)
	target := harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	fixture := buildCodexPortableStaticFixture(t)
	for _, test := range []struct {
		name   string
		config string
		seed   func(t *testing.T)
	}{
		{name: "malformed redacted", config: "secret_header = 'secret-value'\n[broken", seed: func(*testing.T) {}},
		{name: "invalid environment name", config: "[model_providers.fixture]\nenv_key = 'BAD=NAME'\n", seed: func(*testing.T) {}},
		{name: "path environment name", config: "[model_providers.fixture]\nenv_key = 'CODEX_HOME'\n", seed: func(*testing.T) {}},
		{name: "Fizeau cache environment", config: "[model_providers.fixture]\nenv_key = 'FIZEAU_CACHE_DIR'\n", seed: func(t *testing.T) { t.Setenv("FIZEAU_CACHE_DIR", "/account-bearing/fizeau-cache") }},
		{name: "Fizeau skills environment", config: "[model_providers.fixture]\nenv_key = 'FIZEAU_SKILLS_DIR'\n", seed: func(t *testing.T) { t.Setenv("FIZEAU_SKILLS_DIR", "/account-bearing/fizeau-skills") }},
		{name: "invalid MCP environment shape", config: "[mcp_servers.remote]\nurl='https://example.invalid'\nenv_vars=[{name='MCP_VALUE',required=true}]\n", seed: func(*testing.T) {}},
		{name: "invalid provider table", config: "model_providers='secret-value'\n", seed: func(*testing.T) {}},
		{name: "external MCP command", config: "[mcp_servers.local]\ncommand='/account-bearing/bin/server'\n", seed: func(*testing.T) {}},
		{name: "external model catalog", config: "model_catalog_json='/account-bearing/model-catalog.json'\n", seed: func(*testing.T) {}},
		{name: "external agent config", config: "[agents.fixture]\nconfig_file='/account-bearing/agent.toml'\n", seed: func(*testing.T) {}},
		{name: "external skill config", config: "[[skills.config]]\npath='/account-bearing/skill'\n", seed: func(*testing.T) {}},
		{name: "external apps MCP path", config: "[features.apps_mcp_path_override]\nenabled=true\npath='/account-bearing/apps-mcp'\n", seed: func(*testing.T) {}},
		{name: "profile external apps MCP path", config: "[profiles.fixture.features.apps_mcp_path_override]\nenabled=true\npath='/account-bearing/profile-apps-mcp'\n", seed: func(*testing.T) {}},
		{name: "external hook", config: "[[hooks.Stop]]\nmatcher='*'\n", seed: func(*testing.T) {}},
		{name: "shell environment policy", config: "[shell_environment_policy]\ninherit='all'\n", seed: func(*testing.T) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, _ := seedCodexPortableState(t, false)
			test.seed(t)
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := (&Runner{Binary: fixture}).PortableRuntimeAssets(context.Background(), target)
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "/account-bearing/") || strings.Contains(err.Error(), home) {
				t.Fatalf("configuration validation error = %v", err)
			}
		})
	}

	t.Run("present empty source remains distinct from unset", func(t *testing.T) {
		home, _ := seedCodexPortableState(t, false)
		t.Setenv("EMPTY_MCP_SOURCE", "")
		config := "[mcp_servers.remote]\nurl='https://example.invalid'\nenv_vars=[{name='MCP_TARGET',source='EMPTY_MCP_SOURCE'}]\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		contribution, err := (&Runner{Binary: fixture}).PortableRuntimeAssets(context.Background(), target)
		if err != nil {
			t.Fatalf("present-empty environment error = %v", err)
		}
		if !reflect.DeepEqual(contribution.Environment, []harnesses.PortableRuntimeEnvironment{{Name: "EMPTY_MCP_SOURCE"}}) {
			t.Fatalf("present-empty environment = %#v", contribution.Environment)
		}
	})

	t.Run("config identity mutation", func(t *testing.T) {
		home, _ := seedCodexPortableState(t, false)
		t.Setenv("CONFIG_IDENTITY_A", "secret-a")
		t.Setenv("CONFIG_IDENTITY_B", "secret-b")
		configPath := filepath.Join(home, "config.toml")
		if err := os.WriteFile(configPath, []byte("[model_providers.fixture]\nenv_key='CONFIG_IDENTITY_A'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := (&Runner{Binary: fixture}).portableRuntimeAssets(context.Background(), target, func() {
			if writeErr := os.WriteFile(configPath, []byte("[model_providers.fixture]\nenv_key='CONFIG_IDENTITY_B'\n"), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		})
		if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("configuration mutation error = %v", err)
		}
		for _, forbidden := range []string{home, "CONFIG_IDENTITY_A", "CONFIG_IDENTITY_B", "secret-a", "secret-b"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("configuration mutation error leaked %q: %v", forbidden, err)
			}
		}
	})
}

func seedCodexPortableState(t *testing.T, all bool) (string, string) {
	t.Helper()
	home := t.TempDir()
	quota := filepath.Join(t.TempDir(), "codex-quota.json")
	t.Setenv("CODEX_HOME", home)
	t.Setenv(codexAuthPathEnv, filepath.Join(home, "auth.json"))
	t.Setenv(codexQuotaCacheEnv, quota)
	unsetCodexPortableEnvironment(t, "OPENAI_API_KEY")
	unsetCodexPortableEnvironment(t, "CODEX_API_KEY")
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"credential-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !all {
		return home, quota
	}
	if err := os.WriteFile(quota, []byte(`{"quota":"quota-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(`{"models":["fixture"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(home, "cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "fixture.json"), []byte(`{"cached":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, quota
}

func unsetCodexPortableEnvironment(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "portable-test-unset-sentinel")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func buildCodexPortableStaticFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Print(\"portable-codex-fixture\")}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "codex-fixture")
	command := exec.Command("go", "build", "-trimpath", "-ldflags=-buildid=", "-o", executable, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOPROXY=off", "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build static Codex fixture: %v: %s", err, output)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func findCodexPortableDynamicFixture(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/bin/true", "/usr/bin/true", "/bin/echo"} {
		file, err := elf.Open(candidate)
		if err != nil {
			continue
		}
		hasInterpreter := false
		for _, program := range file.Progs {
			hasInterpreter = hasInterpreter || program.Type == elf.PT_INTERP
		}
		_ = file.Close()
		if hasInterpreter {
			return candidate
		}
	}
	t.Fatal("no dynamic Linux fixture")
	return ""
}

func mutateCodexPortableELFMachine(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 20 || string(contents[:4]) != "\x7fELF" {
		t.Fatal("fixture is not ELF")
	}
	var order binary.ByteOrder
	if elf.Data(contents[5]) == elf.ELFDATA2LSB {
		order = binary.LittleEndian
	} else {
		order = binary.BigEndian
	}
	wrong := uint16(elf.EM_386)
	if runtime.GOARCH == "386" {
		wrong = uint16(elf.EM_AARCH64)
	}
	order.PutUint16(contents[18:20], wrong)
	if err := os.WriteFile(path, contents, 0o700); err != nil {
		t.Fatal(err)
	}
}

func codexPortableAssetsByTarget(assets []harnesses.PortableRuntimeAsset) map[string]harnesses.PortableRuntimeAsset {
	indexed := make(map[string]harnesses.PortableRuntimeAsset, len(assets))
	for _, asset := range assets {
		indexed[asset.Target] = asset
	}
	return indexed
}

func writeCodexPortableJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyCodexPortableFile(t *testing.T, source, destination string, mode os.FileMode) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func requireCodexPortableRuntimeLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
}
