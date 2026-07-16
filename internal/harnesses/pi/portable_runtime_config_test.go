package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestPiPortableRuntimeRejectsSettingsPackages(t *testing.T) {
	tests := []struct {
		name     string
		packages string
	}{
		{name: "npm source", packages: `["unsafe-package"]`},
		{name: "local source", packages: `[{"source":"/account-secret/package","extensions":["index.js"]}]`},
		{name: "empty declaration", packages: `[]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writePiPortableConfiguration(t,
				`{"defaultProvider":"fixture","packages":`+tc.packages+`}`,
				validPiPortableModelsJSON(), "")
			resolved := false
			_, err := inspectPiPortableConfiguration(context.Background(), root, func() error {
				resolved = true // Represents packageManager.resolve and any package hook.
				return nil
			})
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
			}
			if resolved {
				t.Fatal("package resolution handoff ran before settings package rejection")
			}
			assertPiPortableRedactedError(t, err, root, "unsafe-package", "/account-secret/package", "index.js")
		})
	}
}

func TestPiPortableRuntimeRejectsExecutableConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		settings     string
		models       string
		extraName    string
		extraBytes   string
		settingsMode os.FileMode
		sensitive    []string
	}{
		{
			name: "command-valued provider credential", settings: `{}`,
			models:    `{"providers":{"fixture":{"baseUrl":"https://example.invalid","api":"openai-completions","apiKey":"!secret-tool read account-token","models":[{"id":"fixture"}]}}}`,
			sensitive: []string{"secret-tool read account-token"},
		},
		{
			name: "command-valued provider header", settings: `{}`,
			models:    `{"providers":{"fixture":{"baseUrl":"https://example.invalid","headers":{"authorization":"!secret-tool header account-token"}}}}`,
			sensitive: []string{"secret-tool header account-token", "authorization"},
		},
		{
			name: "command-valued model header", settings: `{}`,
			models:    `{"providers":{"fixture":{"baseUrl":"https://example.invalid","api":"openai-completions","apiKey":"literal","models":[{"id":"fixture","headers":{"x-secret":"!secret-tool model account-token"}}]}}}`,
			sensitive: []string{"secret-tool model account-token", "x-secret"},
		},
		{
			name: "explicit extension path", settings: `{"extensions":["/account-secret/extension.js"]}`,
			models: validPiPortableModelsJSON(), sensitive: []string{"/account-secret/extension.js"},
		},
		{
			name: "explicit skill path", settings: `{"skills":["/account-secret/SKILL.md"]}`,
			models: validPiPortableModelsJSON(), sensitive: []string{"/account-secret/SKILL.md"},
		},
		{
			name: "global agents file", settings: `{}`, models: validPiPortableModelsJSON(),
			extraName: "AGENTS.md", extraBytes: "account-secret agent instructions", sensitive: []string{"account-secret agent instructions"},
		},
		{
			name: "global system prompt file", settings: `{}`, models: validPiPortableModelsJSON(),
			extraName: "SYSTEM.md", extraBytes: "account-secret system prompt", sensitive: []string{"account-secret system prompt"},
		},
		{
			name: "configured shell path", settings: `{"shellPath":"/account-secret/bin/shell"}`,
			models: validPiPortableModelsJSON(), sensitive: []string{"/account-secret/bin/shell"},
		},
		{
			name: "configured shell command prefix", settings: `{"shellCommandPrefix":"secret-tool assume account"}`,
			models: validPiPortableModelsJSON(), sensitive: []string{"secret-tool assume account"},
		},
		{
			name: "executable settings file", settings: `{}`, models: validPiPortableModelsJSON(),
			settingsMode: 0o700,
		},
		{
			name: "unknown code-loading field", settings: `{"plugins":["/account-secret/plugin.js"]}`,
			models: validPiPortableModelsJSON(), sensitive: []string{"plugins", "/account-secret/plugin.js"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writePiPortableConfiguration(t, tc.settings, tc.models, "")
			if tc.extraName != "" {
				writePiPortableTestFile(t, filepath.Join(root, tc.extraName), tc.extraBytes, 0o600)
			}
			if tc.settingsMode != 0 {
				if err := os.Chmod(filepath.Join(root, piPortableSettingsFilename), tc.settingsMode); err != nil {
					t.Fatal(err)
				}
			}
			resolved := false
			_, err := inspectPiPortableConfiguration(context.Background(), root, func() error {
				resolved = true
				return nil
			})
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
			}
			if resolved {
				t.Fatal("pre-resolution handoff ran for executable or external configuration")
			}
			assertPiPortableRedactedError(t, err, append([]string{root}, tc.sensitive...)...)
		})
	}

	t.Run("request workdir is outside this validator", func(t *testing.T) {
		workdir := t.TempDir()
		writePiPortableTestFile(t, filepath.Join(workdir, ".pi", "settings.json"), `{"packages":["project-hook"]}`, 0o600)
		writePiPortableTestFile(t, filepath.Join(workdir, "AGENTS.md"), "project-only instructions", 0o600)
		t.Chdir(workdir)
		root := writePiPortableConfiguration(t, `{}`, validPiPortableModelsJSON(), "")
		if _, err := inspectPiPortableConfiguration(context.Background(), root, nil); err != nil {
			t.Fatalf("validator inspected request workdir: %v", err)
		}
	})
}

func TestPiPortableRuntimeEnvironmentNames(t *testing.T) {
	t.Setenv("PI_PROVIDER_KEY", "provider-secret")
	t.Setenv("PI_PROVIDER_HEADER", "provider-header-secret")
	t.Setenv("PI_MODEL_HEADER", "model-header-secret")
	root := writePiPortableConfiguration(t, `{}`, `{
  "providers": {
    "fixture": {
      "baseUrl": "https://example.invalid",
      "api": "openai-completions",
      "apiKey": "PI_PROVIDER_KEY",
      "headers": {
        "x-env": "PI_PROVIDER_HEADER",
        "x-literal": "literal-secret",
        "x-unset": "PI_UNSET_REFERENCE",
        "x-invalid-name": "9INVALID"
      },
      "models": [{"id":"fixture","headers":{"x-model":"PI_MODEL_HEADER"}}]
    }
  }
}`, "")
	configuration, err := inspectPiPortableConfiguration(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("inspectPiPortableConfiguration() error = %v", err)
	}
	want := []harnesses.PortableRuntimeEnvironment{{Name: "PI_MODEL_HEADER"}, {Name: "PI_PROVIDER_HEADER"}, {Name: "PI_PROVIDER_KEY"}}
	if !reflect.DeepEqual(configuration.environment, want) {
		t.Fatalf("environment = %#v, want names only %#v", configuration.environment, want)
	}
	encoded, err := json.Marshal(configuration.environment)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"provider-secret", "provider-header-secret", "model-header-secret", "literal-secret",
		"PI_UNSET_REFERENCE", "9INVALID", root, "=", "!", "sha256", "token",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("environment output contains non-name data %q: %s", forbidden, encoded)
		}
	}

	for _, forbiddenName := range []string{"DISPLAY", "WAYLAND_DISPLAY"} {
		t.Run("forbids "+forbiddenName, func(t *testing.T) {
			t.Setenv(forbiddenName, "account-secret-display")
			candidate := writePiPortableConfiguration(t, `{}`, `{"providers":{"fixture":{"baseUrl":"https://example.invalid","apiKey":"`+forbiddenName+`"}}}`, "")
			_, err := inspectPiPortableConfiguration(context.Background(), candidate, nil)
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
			}
			assertPiPortableRedactedError(t, err, candidate, "account-secret-display")
		})
	}
}

func TestPiPortableRuntimeMixedStateProjection(t *testing.T) {
	settings := `{
  "lastChangelogVersion":"0.51.4","defaultProvider":"fixture","defaultModel":"fixture",
  "defaultThinkingLevel":"medium","steeringMode":"all","followUpMode":"one-at-a-time",
  "theme":"dark","compaction":{"enabled":true,"reserveTokens":100,"keepRecentTokens":200},
  "branchSummary":{"reserveTokens":50},"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":10,"maxDelayMs":100},
  "hideThinkingBlock":false,"quietStartup":true,"collapseChangelog":true,"enableSkillCommands":false,
  "terminal":{"showImages":false,"clearOnShrink":true},"images":{"autoResize":true,"blockImages":false},
  "enabledModels":["fixture/*"],"doubleEscapeAction":"none","thinkingBudgets":{"minimal":1,"low":2,"medium":3,"high":4},
  "editorPaddingX":1,"autocompleteMaxVisible":8,"showHardwareCursor":false,"markdown":{"codeBlockIndent":"  "}
}`
	models := `{
  "providers": {
    "fixture": {
      "baseUrl":"https://example.invalid","apiKey":"literal-static-key","api":"openai-completions",
      "headers":{"x-static":"literal-static-header"},"authHeader":true,
      "models":[{
        "id":"fixture","name":"Fixture","api":"openai-completions","reasoning":true,"input":["text","image"],
        "cost":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4},"contextWindow":128000,"maxTokens":16000,
        "headers":{"x-model":"literal-model-header"},
        "compat":{"supportsStore":false,"supportsDeveloperRole":true,"supportsReasoningEffort":true,
          "supportsUsageInStreaming":true,"maxTokensField":"max_tokens","requiresToolResultName":false,
          "requiresAssistantAfterToolResult":false,"requiresThinkingAsText":false,"requiresMistralToolIds":false,
          "thinkingFormat":"qwen","openRouterRouting":{"only":["one"],"order":["two"]},
          "vercelGatewayRouting":{"only":["three"],"order":["four"]}}
      }]
    }
  }
}`
	auth := `{
  "anthropic":{"type":"oauth","refresh":"oauth-refresh-secret","access":"oauth-access-secret","expires":4102444800000},
  "openrouter":{"type":"api_key","key":"api-key-secret"}
}`
	root := writePiPortableConfiguration(t, settings, models, auth)
	configuration, err := inspectPiPortableConfiguration(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("inspectPiPortableConfiguration() error = %v", err)
	}

	wantAssets := []harnesses.PortableRuntimeAsset{
		piPortableTestAsset(t, filepath.Join(root, piPortableSettingsFilename), piPortableSettingsTarget, harnesses.PortableRuntimeAssetConfig),
		piPortableTestAsset(t, filepath.Join(root, piPortableModelsFilename), piPortableModelsTarget, harnesses.PortableRuntimeAssetConfig),
		piPortableTestAsset(t, filepath.Join(root, piPortableAuthFilename), piPortableAuthTarget, harnesses.PortableRuntimeAssetCredential),
	}
	if !reflect.DeepEqual(configuration.assets, wantAssets) {
		t.Fatalf("assets = %#v, want exact three-file schema %#v", configuration.assets, wantAssets)
	}
	wantProjection := []harnesses.PortableRuntimeStateProjection{{
		Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".pi/agent"},
		Entries: []harnesses.PortableRuntimeStateProjectionEntry{
			{AssetTarget: "state/pi/auth.json", Target: "auth.json"},
			{AssetTarget: "config/pi/models.json", Target: "models.json"},
			{AssetTarget: "config/pi/settings.json", Target: "settings.json"},
		},
	}}
	if !reflect.DeepEqual(configuration.stateProjections, wantProjection) {
		t.Fatalf("state projections = %#v, want exact mixed-state projection %#v", configuration.stateProjections, wantProjection)
	}
	if !configuration.authPresent || !configuration.oauthRefresh {
		t.Fatalf("authPresent/oauthRefresh = %v/%v, want validated writable OAuth seed", configuration.authPresent, configuration.oauthRefresh)
	}
	for _, asset := range configuration.assets {
		if asset.PathKind != harnesses.PortableRuntimePathFile || asset.Executable || strings.Contains(asset.Target, "*") {
			t.Fatalf("asset opens an undeclared cache/state surface: %#v", asset)
		}
		if asset.Kind == harnesses.PortableRuntimeAssetCache || asset.Kind == harnesses.PortableRuntimeAssetQuota {
			t.Fatalf("unexpected open-ended state asset: %#v", asset)
		}
	}
}

func TestPiPortableRuntimeAbsentAuth(t *testing.T) {
	t.Run("missing auth does not fabricate state", func(t *testing.T) {
		root := writePiPortableConfiguration(t, `{}`, validPiPortableModelsJSON(), "")
		before := snapshotPiPortableDirectory(t, root)
		configuration, err := inspectPiPortableConfiguration(context.Background(), root, nil)
		if err != nil {
			t.Fatalf("inspectPiPortableConfiguration() error = %v", err)
		}
		after := snapshotPiPortableDirectory(t, root)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("host state changed: before=%#v after=%#v", before, after)
		}
		if configuration.authPresent || configuration.oauthRefresh {
			t.Fatalf("absent auth reported present/refreshable: %#v", configuration)
		}
		if len(configuration.stateProjections) != 0 {
			t.Fatalf("absent auth fabricated a future-writable projection: %#v", configuration.stateProjections)
		}
		if len(configuration.assets) != 2 {
			t.Fatalf("assets = %#v, want immutable settings/models only", configuration.assets)
		}
		for _, asset := range configuration.assets {
			if asset.Kind != harnesses.PortableRuntimeAssetConfig || asset.Target == piPortableAuthTarget {
				t.Fatalf("absent auth fabricated writable state: %#v", asset)
			}
		}
		if _, err := os.Lstat(filepath.Join(root, piPortableAuthFilename)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("auth.json was fabricated: %v", err)
		}
	})

	t.Run("API key seed is writable but not OAuth refreshable", func(t *testing.T) {
		root := writePiPortableConfiguration(t, `{}`, validPiPortableModelsJSON(), `{"fixture":{"type":"api_key","key":"account-secret"}}`)
		before := snapshotPiPortableDirectory(t, root)
		configuration, err := inspectPiPortableConfiguration(context.Background(), root, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !configuration.authPresent || configuration.oauthRefresh {
			t.Fatalf("authPresent/oauthRefresh = %v/%v, want true/false", configuration.authPresent, configuration.oauthRefresh)
		}
		if after := snapshotPiPortableDirectory(t, root); !reflect.DeepEqual(after, before) {
			t.Fatal("validator modified the host API-key seed")
		}
	})

	t.Run("validated OAuth seed enables refresh", func(t *testing.T) {
		root := writePiPortableConfiguration(t, `{}`, validPiPortableModelsJSON(), `{"anthropic":{"type":"oauth","refresh":"refresh-secret","access":"access-secret","expires":4102444800000}}`)
		before := snapshotPiPortableDirectory(t, root)
		configuration, err := inspectPiPortableConfiguration(context.Background(), root, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !configuration.authPresent || !configuration.oauthRefresh {
			t.Fatalf("authPresent/oauthRefresh = %v/%v, want true/true", configuration.authPresent, configuration.oauthRefresh)
		}
		if after := snapshotPiPortableDirectory(t, root); !reflect.DeepEqual(after, before) {
			t.Fatal("validator modified the host OAuth seed")
		}
	})

	t.Run("invalid OAuth seed fails closed", func(t *testing.T) {
		root := writePiPortableConfiguration(t, `{}`, validPiPortableModelsJSON(), `{"anthropic":{"type":"oauth","refresh":"refresh-secret","access":"access-secret","expires":0}}`)
		before := snapshotPiPortableDirectory(t, root)
		configuration, err := inspectPiPortableConfiguration(context.Background(), root, nil)
		if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
		}
		if configuration.oauthRefresh {
			t.Fatal("invalid host seed enabled OAuth refresh")
		}
		if after := snapshotPiPortableDirectory(t, root); !reflect.DeepEqual(after, before) {
			t.Fatal("validator modified the invalid OAuth seed")
		}
		assertPiPortableRedactedError(t, err, root, "refresh-secret", "access-secret")
	})
}

func writePiPortableConfiguration(t *testing.T, settings, models, auth string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "account-secret-agent")
	writePiPortableTestFile(t, filepath.Join(root, piPortableSettingsFilename), settings, 0o600)
	writePiPortableTestFile(t, filepath.Join(root, piPortableModelsFilename), models, 0o600)
	if auth != "" {
		writePiPortableTestFile(t, filepath.Join(root, piPortableAuthFilename), auth, 0o600)
	}
	return root
}

func writePiPortableTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func validPiPortableModelsJSON() string {
	return `{"providers":{"fixture":{"baseUrl":"https://example.invalid"}}}`
}

func piPortableTestAsset(t *testing.T, source, target string, kind harnesses.PortableRuntimeAssetKind) harnesses.PortableRuntimeAsset {
	t.Helper()
	digest, err := harnesses.PortableRuntimeFileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	return harnesses.PortableRuntimeAsset{
		Kind: kind, PathKind: harnesses.PortableRuntimePathFile,
		Source: source, Target: target, ContentSHA256: digest,
	}
}

type piPortableSnapshotEntry struct {
	name   string
	mode   os.FileMode
	size   int64
	digest string
}

func snapshotPiPortableDirectory(t *testing.T, root string) []piPortableSnapshotEntry {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]piPortableSnapshotEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		digest := ""
		if info.Mode().IsRegular() {
			digest, err = harnesses.PortableRuntimeFileDigest(filepath.Join(root, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
		}
		result = append(result, piPortableSnapshotEntry{name: entry.Name(), mode: info.Mode(), size: info.Size(), digest: digest})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

func assertPiPortableRedactedError(t *testing.T, err error, sensitive ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, value := range sensitive {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("error reveals sensitive configuration data %q: %v", value, err)
		}
	}
}
