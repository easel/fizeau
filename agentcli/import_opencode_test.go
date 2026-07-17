package agentcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentConfig "github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/occompat"
	"github.com/easel/fizeau/picompat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportOpenCodeUnknownSourceDoesNotPersistProvider(t *testing.T) {
	tests := []struct {
		name     string
		merge    bool
		existing bool
	}{
		{name: "replace preserves existing config", existing: true},
		{name: "merge preserves existing config", merge: true, existing: true},
		{name: "no config remains absent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, sourceDir, configPath := writeOpenCodeImportFixture(t, "mystery", map[string]any{
				"baseURL": "https://gateway.example.invalid/v1?token=url-secret-canary",
				"apiKey":  "config-secret-canary",
				"headers": map[string]string{"Authorization": "header-secret-canary"},
			})
			t.Setenv("HOME", home)

			var before []byte
			var beforeModTime int64
			if tt.existing {
				require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o750))
				before = []byte(`providers:
  retained:
    type: lmstudio
    base_url: http://retained.example:1234/v1
    api_key: retained-secret-canary
  opencode:
    type: openrouter
    base_url: https://custom.example/v1
    api_key: prior-opencode-secret-canary
default: retained
imported_from:
  source: pi
  timestamp: "2026-07-01T00:00:00Z"
  source_hash: deadbeef
`)
				require.NoError(t, os.WriteFile(configPath, before, 0o600))
				info, err := os.Stat(configPath)
				require.NoError(t, err)
				beforeModTime = info.ModTime().UnixNano()
			}

			stdout, stderr, code := captureStdIO(t, func() int {
				return importOpenCodeFrom(sourceDir, configPath, false, tt.merge)
			})
			require.Equal(t, 0, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, `skipped OpenCode provider "mystery"`)
			assert.Contains(t, stderr, "choose a supported provider key")

			for _, forbidden := range []string{
				"[opencode]", "imported: opencode", "added: opencode", "updated: opencode", "imported to",
				"url-secret-canary", "config-secret-canary", "auth-secret-canary", "header-secret-canary",
			} {
				assert.NotContains(t, stdout+stderr, forbidden)
			}

			if tt.existing {
				after, err := os.ReadFile(configPath)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				info, err := os.Stat(configPath)
				require.NoError(t, err)
				assert.Equal(t, beforeModTime, info.ModTime().UnixNano())
			} else {
				_, err := os.Stat(configPath)
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestShowOpenCodeDiffOmitsSkippedProvider(t *testing.T) {
	result := &occompat.TranslationResult{
		HasProvider: false,
		Provider: agentConfig.ProviderConfig{
			Type:    "poisoned-provider",
			BaseURL: "https://url-secret-canary.invalid/v1",
			APIKey:  "api-key-secret-canary",
			Headers: map[string]string{"Authorization": "header-secret-canary"},
		},
		Warnings: []string{"choose a supported provider key"},
	}

	stdout, stderr, code := captureStdIO(t, func() int {
		return showOpenCodeDiff(result)
	})
	require.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "opencode config -- what would be imported")
	assert.Contains(t, stdout, "Warnings:")
	assert.Contains(t, stdout, "choose a supported provider key")
	for _, forbidden := range []string{
		"[opencode]", "type:", "url:", "api_key:", "headers:",
		"poisoned-provider", "url-secret-canary", "api-key-secret-canary", "header-secret-canary",
	} {
		assert.NotContains(t, stdout, forbidden)
	}
}

func TestImportOpenCodeConcreteSourcePersistsProvider(t *testing.T) {
	t.Run("import", func(t *testing.T) {
		home, sourceDir, configPath := writeOpenCodeImportFixture(t, "openrouter", map[string]any{
			"baseURL": "https://custom-proxy.example.invalid/v1",
			"headers": map[string]string{"X-Title": "Fizeau"},
		})
		t.Setenv("HOME", home)
		require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o750))

		stdout, stderr, code := captureStdIO(t, func() int {
			return importOpenCodeFrom(sourceDir, configPath, false, false)
		})
		require.Equal(t, 0, code, "stdout=%s stderr=%s", stdout, stderr)
		assert.Contains(t, stdout, "imported: opencode")
		assert.Contains(t, stdout, "imported to "+configPath)
		assert.Empty(t, stderr)

		cfg, err := agentConfig.Load(filepath.Dir(configPath))
		require.NoError(t, err)
		provider, ok := cfg.Providers["opencode"]
		require.True(t, ok)
		assert.Equal(t, "openrouter", provider.Type)
		assert.NotEqual(t, "openai-compat", provider.Type)
		assert.Equal(t, "https://custom-proxy.example.invalid/v1", provider.BaseURL)
		assert.Equal(t, "auth-secret-canary", provider.APIKey)
		assert.Equal(t, map[string]string{"X-Title": "Fizeau"}, provider.Headers)
		require.NotNil(t, cfg.ImportedFrom)
		assert.Equal(t, "opencode", cfg.ImportedFrom.Source)
		assert.NotEmpty(t, cfg.ImportedFrom.SourceHash)

		info, err := os.Stat(configPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("merge updates only credential", func(t *testing.T) {
		home, sourceDir, configPath := writeOpenCodeImportFixture(t, "openrouter", map[string]any{
			"baseURL": "https://translated.example.invalid/v1",
			"headers": map[string]string{"X-Title": "Translated"},
		})
		t.Setenv("HOME", home)
		require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o750))
		require.NoError(t, os.WriteFile(configPath, []byte(`providers:
  retained:
    type: lmstudio
    base_url: http://retained.example:1234/v1
  opencode:
    type: openrouter
    base_url: https://customized.example/v1
    api_key: old-secret
    model: customized-model
    headers:
      X-Title: Customized
default: retained
`), 0o600))

		stdout, stderr, code := captureStdIO(t, func() int {
			return importOpenCodeFrom(sourceDir, configPath, false, true)
		})
		require.Equal(t, 0, code, "stdout=%s stderr=%s", stdout, stderr)
		assert.Contains(t, stdout, "updated: opencode")
		assert.Empty(t, stderr)

		cfg, err := agentConfig.Load(filepath.Dir(configPath))
		require.NoError(t, err)
		provider := cfg.Providers["opencode"]
		assert.Equal(t, "openrouter", provider.Type)
		assert.Equal(t, "https://customized.example/v1", provider.BaseURL)
		assert.Equal(t, "auth-secret-canary", provider.APIKey)
		assert.Equal(t, "customized-model", provider.Model)
		assert.Equal(t, map[string]string{"X-Title": "Customized"}, provider.Headers)
		assert.Contains(t, cfg.Providers, "retained")
		require.NotNil(t, cfg.ImportedFrom)
		assert.Equal(t, "opencode", cfg.ImportedFrom.Source)
	})
}

func TestTranslatedProductionConfigNeverPersistsOpenAICompat(t *testing.T) {
	piDir := t.TempDir()
	piAgentDir := filepath.Join(piDir, "agent")
	require.NoError(t, os.MkdirAll(piAgentDir, 0o750))
	writeOpenCodeImportJSON(t, filepath.Join(piAgentDir, "auth.json"), map[string]any{
		"openrouter": map[string]any{"type": "api_key", "key": "pi-auth-key"},
	})
	writeOpenCodeImportJSON(t, filepath.Join(piAgentDir, "models.json"), map[string]any{
		"providers": []map[string]any{
			{
				"name":    "openrouter",
				"baseUrl": "https://custom-proxy.example.invalid/v1",
				"api":     "openai-completions",
				"models":  []string{"known-model"},
			},
			{
				"name":    "protocol-only-unknown",
				"baseUrl": "https://unknown.example.invalid/v1",
				"api":     "openai-completions",
				"models":  []string{"unknown-model"},
			},
		},
	})

	piResult, err := picompat.Translate(piDir)
	require.NoError(t, err)
	require.Contains(t, piResult.Providers, "openrouter")
	assert.NotContains(t, piResult.Providers, "protocol-only-unknown")

	openCodeDir := t.TempDir()
	writeOpenCodeImportJSON(t, filepath.Join(openCodeDir, "opencode.json"), map[string]any{
		"options": map[string]any{
			"baseURL": "https://another-proxy.example.invalid/v1",
			"npm":     "@ai-sdk/openai-compatible",
		},
	})
	openCodeResult := occompat.TranslateProvider(openCodeDir, "openrouter", "opencode-auth-key")
	require.True(t, openCodeResult.HasProvider)

	unknownOpenCodeDir := t.TempDir()
	writeOpenCodeImportJSON(t, filepath.Join(unknownOpenCodeDir, "opencode.json"), map[string]any{
		"options": map[string]any{
			"baseURL": "https://unknown.example.invalid/v1",
			"npm":     "@ai-sdk/openai-compatible",
		},
	})
	unknownOpenCodeResult := occompat.TranslateProvider(unknownOpenCodeDir, "protocol-only-unknown", "unknown-auth-key")
	assert.False(t, unknownOpenCodeResult.HasProvider)

	providers := make(map[string]agentConfig.ProviderConfig, len(piResult.Providers)+1)
	for name, provider := range piResult.Providers {
		providers[name] = provider
	}
	if openCodeResult.HasProvider {
		providers["opencode"] = openCodeResult.Provider
	}
	if unknownOpenCodeResult.HasProvider {
		providers["should-not-persist"] = unknownOpenCodeResult.Provider
	}

	for name, provider := range providers {
		assert.NotEqual(t, "openai-compat", provider.Type, "provider %q", name)
	}
	serialized, err := agentConfig.Save(&agentConfig.Config{Providers: providers})
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "type: openai-compat")
}

func writeOpenCodeImportFixture(t *testing.T, sourceIdentity string, options map[string]any) (home, sourceDir, configPath string) {
	t.Helper()
	home = t.TempDir()
	sourceDir = filepath.Join(home, ".local", "share", "opencode")
	configDir := filepath.Join(home, ".config", "opencode")
	require.NoError(t, os.MkdirAll(sourceDir, 0o750))
	require.NoError(t, os.MkdirAll(configDir, 0o750))
	writeOpenCodeImportJSON(t, filepath.Join(sourceDir, "auth.json"), map[string]any{
		sourceIdentity: map[string]any{"type": "api", "key": "auth-secret-canary"},
	})
	writeOpenCodeImportJSON(t, filepath.Join(configDir, "opencode.json"), map[string]any{"options": options})
	return home, sourceDir, filepath.Join(home, ".config", "fizeau", "config.yaml")
}

func writeOpenCodeImportJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
}
