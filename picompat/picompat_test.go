package picompat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPiDir_UsesProcessHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/picompat-home")

	dir := DefaultPiDir()
	assert.Equal(t, filepath.Join("/tmp/picompat-home", ".pi"), dir)
}

func TestLoadAuth(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	// Write test auth.json using pi's actual field names:
	// oauth uses "access", api_key uses "key"
	authJSON := `{
		"anthropic": {
			"type": "oauth",
			"access": "sk-ant-test123",
			"expires": 1749331200000
		},
		"openrouter": {
			"type": "api_key",
			"key": "sk-or-test456"
		}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644)
	require.NoError(t, err)

	// Load and verify
	creds, err := LoadAuth(tmpDir)
	require.NoError(t, err)
	assert.Len(t, creds, 2)

	assert.Equal(t, "sk-ant-test123", creds["anthropic"].AccessToken)
	assert.Equal(t, int64(1749331200000), creds["anthropic"].Expires)
	assert.Equal(t, "sk-or-test456", creds["openrouter"].Key)
}

func TestLoadModels(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	modelsJSON := `{
		"providers": [
			{
				"name": "vidar",
				"baseUrl": "http://vidar:1234/v1",
				"api": "openai-completions",
				"models": ["qwen3.5-7b"]
			}
		]
	}`
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644)
	require.NoError(t, err)

	cfg, err := LoadModels(tmpDir)
	require.NoError(t, err)
	require.Len(t, cfg.Providers, 1)

	prov := cfg.Providers[0]
	assert.Equal(t, "vidar", prov.Name)
	assert.Equal(t, "http://vidar:1234/v1", prov.BaseURL)
	assert.Equal(t, "openai-completions", prov.API)
	assert.Len(t, prov.Models, 1)
	assert.Equal(t, "qwen3.5-7b", prov.Models[0])
}

func TestLoadModels_ObjectMapProviders(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	modelsJSON := `{
		"providers": {
			"vidar": {
				"baseUrl": "http://vidar:1234/v1",
				"api": "openai-completions",
				"models": [
					{ "id": "qwen3.5-27b" },
					{ "id": "openai/gpt-oss-20b" }
				]
			},
			"bragi": {
				"baseUrl": "http://bragi:1234/v1",
				"api": "openai-completions",
				"models": [
					{ "id": "qwen3.5-27b" }
				]
			}
		}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644)
	require.NoError(t, err)

	cfg, err := LoadModels(tmpDir)
	require.NoError(t, err)
	require.Len(t, cfg.Providers, 2)

	vidar := cfg.GetProviderByName("vidar")
	require.NotNil(t, vidar)
	assert.Equal(t, "http://vidar:1234/v1", vidar.BaseURL)
	assert.Equal(t, []string{"qwen3.5-27b", "openai/gpt-oss-20b"}, vidar.Models)

	bragi := cfg.GetProviderByName("bragi")
	require.NotNil(t, bragi)
	assert.Equal(t, []string{"qwen3.5-27b"}, bragi.Models)
}

func TestLoadSettings(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	// settings.json is at the same level as agent/
	settingsJSON := `{
		"defaultProvider": "anthropic",
		"defaultModel": "claude-sonnet-4-20250514",
		"max_iterations": 30
	}`
	err = os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(settingsJSON), 0644)
	require.NoError(t, err)

	settings, err := LoadSettings(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", settings.DefaultProvider)
	assert.Equal(t, "claude-sonnet-4-20250514", settings.DefaultModel)
	assert.Equal(t, 30, settings.MaxIterations)
}

func TestLoadSettings_Optional(t *testing.T) {
	// settings.json is optional
	settings, err := LoadSettings("/nonexistent")
	assert.Error(t, err)
	assert.Nil(t, settings)
}

func TestTranslate_TwoSourceMerge(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	// auth.json — use pi's actual field names
	authJSON := `{
		"anthropic": {"type": "oauth", "access": "sk-ant-api-key"},
		"openrouter": {"type": "api_key", "key": "sk-or-api-key"}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644)
	require.NoError(t, err)

	// models.json
	modelsJSON := `{
		"providers": [
			{
				"name": "vidar",
				"baseUrl": "http://vidar:1234/v1",
				"api": "openai-completions",
				"models": ["qwen3.5-7b"]
			}
		]
	}`
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644)
	require.NoError(t, err)

	// settings.json with defaults
	settingsJSON := `{
		"defaultProvider": "anthropic",
		"defaultModel": "claude-sonnet-4-20250514"
	}`
	err = os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(settingsJSON), 0644)
	require.NoError(t, err)

	result, err := Translate(tmpDir)
	require.NoError(t, err)

	// Should have vidar from models.json
	assert.Contains(t, result.Providers, "vidar")
	assert.Equal(t, "lmstudio", result.Providers["vidar"].Type)
	assert.Equal(t, "http://vidar:1234/v1", result.Providers["vidar"].BaseURL)
	assert.Equal(t, "qwen3.5-7b", result.Providers["vidar"].Model)

	// Should have anthropic from auth.json (no model in models)
	assert.Contains(t, result.Providers, "anthropic")
	assert.Equal(t, "anthropic", result.Providers["anthropic"].Type)
	assert.Equal(t, "sk-ant-api-key", result.Providers["anthropic"].APIKey)

	// Should have openrouter from auth.json
	assert.Contains(t, result.Providers, "openrouter")
	assert.Equal(t, "openrouter", result.Providers["openrouter"].Type)
	assert.Equal(t, "https://openrouter.ai/api/v1", result.Providers["openrouter"].BaseURL)

	// Default should be anthropic
	assert.Equal(t, "anthropic", result.Default)
}

func TestTranslate_CurrentPiObjectMapShape(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	authJSON := `{
		"openrouter": {"api_key": "sk-or-api-key"}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644)
	require.NoError(t, err)

	modelsJSON := `{
		"providers": {
			"vidar": {
				"baseUrl": "http://vidar:1234/v1",
				"api": "openai-completions",
				"api_key": "lmstudio",
				"models": [
					{ "id": "qwen3.5-27b" },
					{ "id": "openai/gpt-oss-20b" }
				]
			},
			"grendel": {
				"baseUrl": "http://grendel:1234/v1",
				"api": "openai-completions",
				"api_key": "lmstudio",
				"models": [
					{ "id": "qwen3.5-27b" }
				]
			}
		}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644)
	require.NoError(t, err)

	settingsJSON := `{
		"defaultProvider": "grendel",
		"defaultModel": "qwen3.5-27b"
	}`
	err = os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(settingsJSON), 0644)
	require.NoError(t, err)

	result, err := Translate(tmpDir)
	require.NoError(t, err)

	require.Contains(t, result.Providers, "vidar")
	assert.Equal(t, "http://vidar:1234/v1", result.Providers["vidar"].BaseURL)
	assert.Equal(t, "qwen3.5-27b", result.Providers["vidar"].Model)
	assert.Equal(t, "lmstudio", result.Providers["vidar"].APIKey)

	require.Contains(t, result.Providers, "grendel")
	assert.Equal(t, "http://grendel:1234/v1", result.Providers["grendel"].BaseURL)
	assert.Equal(t, "qwen3.5-27b", result.Providers["grendel"].Model)
	assert.Equal(t, "grendel", result.Default)

	require.Contains(t, result.Providers, "openrouter")
	assert.Equal(t, "https://openrouter.ai/api/v1", result.Providers["openrouter"].BaseURL)
}

func TestTranslate_SkipsUnsupported(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	authJSON := `{
		"google-gemini-cli": {"api_key": "gemini-key"},
		"github-copilot": {"api_key": "copilot-key"}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644)
	require.NoError(t, err)

	modelsJSON := `{"providers": []}`
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644)
	require.NoError(t, err)

	result, err := Translate(tmpDir)
	require.NoError(t, err)

	// Unsupported providers should be skipped with warnings
	assert.Len(t, result.Warnings, 2)
	// Warnings may be in any order
	hasGemini := false
	hasCopilot := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "google-gemini-cli") {
			hasGemini = true
		}
		if strings.Contains(w, "github-copilot") {
			hasCopilot = true
		}
	}
	assert.True(t, hasGemini, "should have warning for google-gemini-cli")
	assert.True(t, hasCopilot, "should have warning for github-copilot")
}

func TestTranslate_SkipsCommandKeys(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	authJSON := `{
		"openai-codex": {"api_key": "!echo $API_KEY"}
	}`
	err = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644)
	require.NoError(t, err)

	modelsJSON := `{"providers": []}`
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644)
	require.NoError(t, err)

	result, err := Translate(tmpDir)
	require.NoError(t, err)

	assert.Contains(t, result.Warnings[0], "shell-resolved key")
}

func TestAuthEntry_ResolvedKey(t *testing.T) {
	tests := []struct {
		name     string
		entry    AuthEntry
		expected string
	}{
		{"oauth access token", AuthEntry{AccessToken: "sk-ant-oat01-abc"}, "sk-ant-oat01-abc"},
		{"api_key field", AuthEntry{APIKey: "sk-or-v1-abc"}, "sk-or-v1-abc"},
		{"key field (pi api_key type)", AuthEntry{Key: "sk-z-abc"}, "sk-z-abc"},
		{"access takes priority over key", AuthEntry{AccessToken: "oauth-tok", Key: "api-key"}, "oauth-tok"},
		{"key takes priority over api_key", AuthEntry{Key: "key-field", APIKey: "api_key_field"}, "key-field"},
		{"empty entry", AuthEntry{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.entry.ResolvedKey())
		})
	}
}

func TestTranslate_NewCloudProviders(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))

	authJSON := `{
		"qwen":     {"type": "api_key", "key": "sk-qwen-abc"},
		"dashscope":{"type": "api_key", "key": "sk-dash-abc"},
		"minimax":  {"type": "api_key", "key": "sk-mm-abc"},
		"z.ai":     {"type": "api_key", "key": "sk-zai-abc"}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":[]}`), 0644))

	result, err := Translate(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, result.Warnings)

	qwen := result.Providers["qwen"]
	assert.Equal(t, "qwen", qwen.Type)
	assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", qwen.BaseURL)
	// Both qwen and dashscope map to agent name "qwen"; Go map iteration is random
	// so either key may win — just verify one of them was used.
	assert.True(t, qwen.APIKey == "sk-qwen-abc" || qwen.APIKey == "sk-dash-abc",
		"qwen provider should have one of the two keys, got %q", qwen.APIKey)

	// dashscope is an alias for qwen — both map to the same agent name "qwen"
	// the second entry overwrites; we just verify it resolves without error
	minimax := result.Providers["minimax"]
	assert.Equal(t, "minimax", minimax.Type)
	assert.Equal(t, "https://api.minimaxi.chat/v1", minimax.BaseURL)
	assert.Equal(t, "sk-mm-abc", minimax.APIKey)

	zai := result.Providers["z.ai"]
	assert.Equal(t, "zai", zai.Type)
	assert.Equal(t, "https://api.z.ai/v1", zai.BaseURL)
	assert.Equal(t, "sk-zai-abc", zai.APIKey)
}

func TestTranslatePiUsesConcreteProviderTypes(t *testing.T) {
	tests := []struct {
		name        string
		piName      string
		wantName    string
		wantType    string
		wantBaseURL string
	}{
		{name: "openai codex", piName: "openai-codex", wantName: "openai", wantType: "openai", wantBaseURL: "https://api.openai.com/v1"},
		{name: "openrouter", piName: "openrouter", wantName: "openrouter", wantType: "openrouter", wantBaseURL: "https://openrouter.ai/api/v1"},
		{name: "qwen", piName: "qwen", wantName: "qwen", wantType: "qwen", wantBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{name: "dashscope alias", piName: "dashscope", wantName: "qwen", wantType: "qwen", wantBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{name: "minimax", piName: "minimax", wantName: "minimax", wantType: "minimax", wantBaseURL: "https://api.minimaxi.chat/v1"},
		{name: "z.ai", piName: "z.ai", wantName: "z.ai", wantType: "zai", wantBaseURL: "https://api.z.ai/v1"},
		{name: "zai alias", piName: "zai", wantName: "z.ai", wantType: "zai", wantBaseURL: "https://api.z.ai/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			agentDir := filepath.Join(tmpDir, "agent")
			require.NoError(t, os.MkdirAll(agentDir, 0755))
			authJSON := fmt.Sprintf(`{%q:{"type":"api_key","key":"test-key"}}`, tt.piName)
			require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":[]}`), 0644))

			result, err := Translate(tmpDir)
			require.NoError(t, err)
			require.Empty(t, result.Warnings)
			require.Len(t, result.Providers, 1)
			provider, ok := result.Providers[tt.wantName]
			require.True(t, ok, "provider keys: %#v", result.Providers)
			assert.Equal(t, tt.wantType, provider.Type)
			assert.Equal(t, tt.wantBaseURL, provider.BaseURL)
			assert.Equal(t, "test-key", provider.APIKey)
		})
	}
}

func TestTranslatePiCrossAliasTwoSourceMerge(t *testing.T) {
	tests := []struct {
		name       string
		modelName  string
		authJSON   string
		wantName   string
		wantAPIKey string
	}{
		{
			name:       "z.ai model uses zai credential",
			modelName:  "z.ai",
			authJSON:   `{"zai":{"type":"api_key","key":"zai-alias-key"}}`,
			wantName:   "z.ai",
			wantAPIKey: "zai-alias-key",
		},
		{
			name:       "zai model uses z.ai credential",
			modelName:  "zai",
			authJSON:   `{"z.ai":{"type":"api_key","key":"z.ai-alias-key"}}`,
			wantName:   "z.ai",
			wantAPIKey: "z.ai-alias-key",
		},
		{
			name:       "qwen model uses dashscope credential",
			modelName:  "qwen",
			authJSON:   `{"dashscope":{"type":"api_key","key":"dashscope-alias-key"}}`,
			wantName:   "qwen",
			wantAPIKey: "dashscope-alias-key",
		},
		{
			name:       "dashscope model uses qwen credential",
			modelName:  "dashscope",
			authJSON:   `{"qwen":{"type":"api_key","key":"qwen-alias-key"}}`,
			wantName:   "qwen",
			wantAPIKey: "qwen-alias-key",
		},
		{
			name:       "qwen exact credential wins over alias",
			modelName:  "qwen",
			authJSON:   `{"dashscope":{"type":"api_key","key":"dashscope-alias-key"},"qwen":{"type":"api_key","key":"qwen-exact-key"}}`,
			wantName:   "qwen",
			wantAPIKey: "qwen-exact-key",
		},
		{
			name:       "zai exact credential wins over alias",
			modelName:  "zai",
			authJSON:   `{"z.ai":{"type":"api_key","key":"z.ai-alias-key"},"zai":{"type":"api_key","key":"zai-exact-key"}}`,
			wantName:   "z.ai",
			wantAPIKey: "zai-exact-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			agentDir := filepath.Join(tmpDir, "agent")
			require.NoError(t, os.MkdirAll(agentDir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(tt.authJSON), 0644))
			modelsJSON := fmt.Sprintf(
				`{"providers":[{"name":%q,"api":"openai-completions","models":["test-model"]}]}`,
				tt.modelName,
			)
			require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644))

			result, err := Translate(tmpDir)
			require.NoError(t, err)
			require.Empty(t, result.Warnings)
			require.Len(t, result.Providers, 1)
			provider, ok := result.Providers[tt.wantName]
			require.True(t, ok, "provider keys: %#v", result.Providers)
			assert.Equal(t, tt.wantAPIKey, provider.APIKey)
		})
	}
}

func TestTranslatePiInfersConcreteTypeFromRecognizedBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		piName    string
		baseURL   string
		api       string
		wantType  string
		wantAdded bool
	}{
		{name: "explicit concrete identity", piName: "lmstudio", baseURL: "https://proxy.example/v1", api: "openai-completions", wantType: "lmstudio", wantAdded: true},
		{name: "normalized openai host", piName: "custom-openai", baseURL: " HTTPS://API.OPENAI.COM/v1/ ", api: "openai-completions", wantType: "openai", wantAdded: true},
		{name: "openrouter host", piName: "custom-openrouter", baseURL: "https://openrouter.ai/api/v1", api: "openai-completions", wantType: "openrouter", wantAdded: true},
		{name: "minimax host", piName: "custom-minimax", baseURL: "https://api.minimaxi.chat/v1", api: "openai-completions", wantType: "minimax", wantAdded: true},
		{name: "dashscope host", piName: "custom-qwen", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", api: "openai-completions", wantType: "qwen", wantAdded: true},
		{name: "zai host", piName: "custom-zai", baseURL: "https://api.z.ai/v1", api: "openai-completions", wantType: "zai", wantAdded: true},
		{name: "ollama port", piName: "local-ollama", baseURL: "http://localhost:11434/v1", api: "openai-completions", wantType: "ollama", wantAdded: true},
		{name: "lmstudio port", piName: "local-lmstudio", baseURL: "http://localhost:1234/v1", api: "openai-completions", wantType: "lmstudio", wantAdded: true},
		{name: "omlx port", piName: "local-omlx", baseURL: "http://localhost:1235/v1", api: "openai-completions", wantType: "omlx", wantAdded: true},
		{name: "lucebox port", piName: "local-lucebox", baseURL: "http://localhost:1236/v1", api: "openai-completions", wantType: "lucebox", wantAdded: true},
		{name: "vllm port", piName: "local-vllm", baseURL: "http://localhost:8000/v1", api: "openai-completions", wantType: "vllm", wantAdded: true},
		{name: "openai protocol is not identity", piName: "protocol-only-openai", baseURL: "https://unknown.example/v1", api: "openai-completions", wantAdded: false},
		{name: "anthropic protocol is not identity", piName: "protocol-only-anthropic", baseURL: "https://unknown.example/v1", api: "anthropic", wantAdded: false},
		{name: "lookalike host is not identity", piName: "spoofed-openai", baseURL: "https://api.openai.com.evil.example/v1", api: "openai-completions", wantAdded: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			agentDir := filepath.Join(tmpDir, "agent")
			require.NoError(t, os.MkdirAll(agentDir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{}`), 0644))
			modelsJSON := fmt.Sprintf(
				`{"providers":[{"name":%q,"baseUrl":%q,"api":%q,"api_key":"test-key","models":["test-model"]}]}`,
				tt.piName,
				tt.baseURL,
				tt.api,
			)
			require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644))

			result, err := Translate(tmpDir)
			require.NoError(t, err)
			if !tt.wantAdded {
				assert.NotContains(t, result.Providers, tt.piName)
				require.NotEmpty(t, result.Warnings)
				return
			}
			require.Empty(t, result.Warnings)
			provider, ok := result.Providers[tt.piName]
			require.True(t, ok, "provider keys: %#v", result.Providers)
			assert.Equal(t, tt.wantType, provider.Type)
		})
	}
}

func TestTranslatePiSkipsUnknownConcreteType(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{
		"providers": [{
			"name": "unknown-source",
			"baseUrl": "https://operator:base-url-secret@unknown.example/v1",
			"api": "openai-completions",
			"api_key": "api-key-secret",
			"models": ["test-model"]
		}]
	}`), 0644))

	result, err := Translate(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, result.Providers)
	require.Len(t, result.Warnings, 1)
	warning := result.Warnings[0]
	assert.Contains(t, warning, "unknown-source")
	assert.Contains(t, warning, "concrete provider type")
	assert.Contains(t, warning, "supported provider name")
	assert.NotContains(t, warning, "api-key-secret")
	assert.NotContains(t, warning, "base-url-secret")
	for _, provider := range result.Providers {
		assert.NotContains(t, []string{"openai", "lmstudio", "openai-compat"}, provider.Type)
	}
}

func TestTranslate_OAuthAccessToken(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))

	// Matches pi's actual auth.json shape for oauth providers
	authJSON := `{
		"anthropic": {
			"type": "oauth",
			"access": "sk-ant-oat01-real-token",
			"refresh": "sk-ant-ort01-refresh",
			"expires": 1775681733815
		},
		"openrouter": {
			"type": "api_key",
			"key": "sk-or-v1-real-key"
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(authJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":[]}`), 0644))

	result, err := Translate(tmpDir)
	require.NoError(t, err)

	// Anthropic oauth access token should be used as the API key
	assert.Equal(t, "sk-ant-oat01-real-token", result.Providers["anthropic"].APIKey)
	// OpenRouter key field should be used
	assert.Equal(t, "sk-or-v1-real-key", result.Providers["openrouter"].APIKey)
}

func TestTranslate_ThinkingModelAutoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))

	modelsJSON := `{
		"providers": {
			"vidar": {
				"baseUrl": "http://vidar:1234/v1",
				"api": "openai-completions",
				"api_key": "lmstudio",
				"models": [{"id": "qwen3.5-27b"}]
			},
			"bragi": {
				"baseUrl": "http://bragi:1234/v1",
				"api": "openai-completions",
				"api_key": "lmstudio",
				"models": [{"id": "deepseek-r1-distill-qwen-32b"}]
			},
			"grendel": {
				"baseUrl": "http://grendel:1234/v1",
				"api": "openai-completions",
				"api_key": "lmstudio",
				"models": [{"id": "llama3.1-8b"}]
			}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{}`), 0644))

	result, err := Translate(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, "medium", string(result.Providers["vidar"].Reasoning), "qwen3 model should get reasoning")
	assert.Equal(t, "medium", string(result.Providers["bragi"].Reasoning), "deepseek-r1 model should get reasoning")
	assert.Equal(t, "", string(result.Providers["grendel"].Reasoning), "non-reasoning model should not get reasoning")
}

func TestIsReasoningModel(t *testing.T) {
	thinking := []string{"qwen3.5-27b", "qwen3-coder-30b", "Qwen3-72B", "deepseek-r1", "deepseek-r1-distill-qwen-32b", "deepseek_r1", "qwq-32b"}
	notThinking := []string{"qwen2.5-coder", "llama3.1-8b", "gpt-4o", "claude-sonnet-4-6", "gemma-4-26b", "qwen3.5-27b-claude-4.6-opus-distilled-mlx"}

	for _, m := range thinking {
		assert.True(t, isReasoningModel(m), "expected %q to be a reasoning model", m)
	}
	for _, m := range notThinking {
		assert.False(t, isReasoningModel(m), "expected %q to NOT be a reasoning model", m)
	}
}

func TestComputeSourceHash(t *testing.T) {
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, "agent")
	err := os.MkdirAll(agentDir, 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte("test"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte("test"), 0644)
	require.NoError(t, err)

	hash, err := ComputeSourceHash(tmpDir)
	require.NoError(t, err)
	assert.Len(t, hash, 8) // truncated to 8 chars
}

func TestTokenExpiryStatus(t *testing.T) {
	tests := []struct {
		name     string
		entry    AuthEntry
		minHours float64
		hasWarn  bool
	}{
		{
			name:     "no expiry info",
			entry:    AuthEntry{},
			minHours: 24,
			hasWarn:  false,
		},
		{
			name:     "expires in 48 hours",
			entry:    AuthEntry{Expires: 48 * 60 * 60 * 1000},
			minHours: 47,
			hasWarn:  false,
		},
		{
			name:     "expires in 3 hours",
			entry:    AuthEntry{Expires: 3 * 60 * 60 * 1000},
			minHours: 2,
			hasWarn:  true,
		},
		{
			name:     "already expired",
			entry:    AuthEntry{Expires: -1000},
			minHours: -1,
			hasWarn:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hours, warn := tt.entry.TokenExpiryStatus()
			assert.GreaterOrEqual(t, hours, tt.minHours)
			if tt.minHours > 0 {
				assert.LessOrEqual(t, hours, tt.minHours+1)
			}
			assert.Equal(t, tt.hasWarn, warn != "")
		})
	}
}
