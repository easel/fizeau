package occompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAuth(t *testing.T) {
	tmpDir := t.TempDir()

	authJSON := `{
		"openai": {
			"type": "api",
			"key": "sk-test-key"
		}
	}`
	err := os.WriteFile(filepath.Join(tmpDir, "auth.json"), []byte(authJSON), 0644)
	require.NoError(t, err)

	creds, err := LoadAuth(tmpDir)
	require.NoError(t, err)
	assert.Len(t, creds, 1)
	assert.Equal(t, "api", creds["openai"].Type)
	assert.Equal(t, "sk-test-key", creds["openai"].Key)
}

func TestLoadConfig_Project(t *testing.T) {
	tmpDir := t.TempDir()

	configJSON := `{
		"options": {
			"baseURL": "https://api.example.com/v1",
			"apiKey": "test-key",
			"model": "custom-model"
		}
	}`
	err := os.WriteFile(filepath.Join(tmpDir, "opencode.json"), []byte(configJSON), 0644)
	require.NoError(t, err)

	cfg, err := LoadConfig(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com/v1", cfg.Options.BaseURL)
	assert.Equal(t, "test-key", cfg.Options.APIKey)
	assert.Equal(t, "custom-model", cfg.Options.Model)
}

func TestLoadConfig_Global(t *testing.T) {
	// Test that LoadGlobalConfig works when file exists
	// Note: This test may fail if ~/.config/opencode/opencode.json doesn't exist
	// Just verify the function structure works
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}

	// Create temp global config
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, ".config", "opencode")
	err := os.MkdirAll(globalDir, 0755)
	require.NoError(t, err)

	configJSON := `{
		"options": {
			"baseURL": "https://global.example.com/v1",
			"npm": "@ai-sdk/openai-compatible"
		}
	}`
	err = os.WriteFile(filepath.Join(globalDir, "opencode.json"), []byte(configJSON), 0644)
	require.NoError(t, err)

	// Temporarily change home for this test
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://global.example.com/v1", cfg.Options.BaseURL)
	assert.Equal(t, "@ai-sdk/openai-compatible", cfg.Options.NPM)
}

func TestLoadConfig_NotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent")
	assert.Error(t, err)

	_, err = LoadGlobalConfig()
	assert.Error(t, err)
}

func TestTranslate(t *testing.T) {
	tmpDir := t.TempDir()

	// Write opencode.json
	configJSON := `{
		"options": {
			"baseURL": "https://api.example.com/v1",
			"apiKey": "secret-key",
			"headers": {
				"X-Custom": "value"
			}
		}
	}`
	err := os.WriteFile(filepath.Join(tmpDir, "opencode.json"), []byte(configJSON), 0644)
	require.NoError(t, err)

	result := TranslateProvider(tmpDir, "openai", "")

	assert.True(t, result.HasProvider)
	assert.Equal(t, "openai", result.Provider.Type)
	assert.Equal(t, "https://api.example.com/v1", result.Provider.BaseURL)
	assert.Equal(t, "secret-key", result.Provider.APIKey)
	assert.Equal(t, "value", result.Provider.Headers["X-Custom"])
}

func TestTranslate_WithAuthKey(t *testing.T) {
	tmpDir := t.TempDir()

	// Write opencode.json without apiKey
	configJSON := `{
		"options": {
			"baseURL": "https://api.openai.com/v1"
		}
	}`
	err := os.WriteFile(filepath.Join(tmpDir, "opencode.json"), []byte(configJSON), 0644)
	require.NoError(t, err)

	result := Translate(tmpDir, "auth-key-from-json")

	assert.True(t, result.HasProvider)
	assert.Equal(t, "auth-key-from-json", result.Provider.APIKey)
}

func TestTranslateOpenCodeUsesConcreteProviderTypes(t *testing.T) {
	tests := []struct {
		name           string
		sourceIdentity string
		baseURL        string
		wantType       string
	}{
		{name: "explicit OpenAI", sourceIdentity: " OpenAI ", baseURL: "https://gateway.example.invalid/v1", wantType: "openai"},
		{name: "explicit OpenRouter", sourceIdentity: "openrouter", wantType: "openrouter"},
		{name: "explicit DashScope alias", sourceIdentity: "dashscope", wantType: "qwen"},
		{name: "explicit Z.ai alias", sourceIdentity: "z.ai", wantType: "zai"},
		{name: "explicit DS4", sourceIdentity: "ds4", wantType: "ds4"},
		{name: "explicit llama-server", sourceIdentity: "llama-server", wantType: "llama-server"},
		{name: "explicit Lucebox", sourceIdentity: "lucebox", wantType: "lucebox"},
		{name: "explicit Rapid MLX", sourceIdentity: "rapid-mlx", wantType: "rapid-mlx"},
		{name: "explicit vLLM", sourceIdentity: "vllm", wantType: "vllm"},
		{name: "normalized OpenRouter URL", baseURL: " HTTPS://API.OPENROUTER.AI./api/v1 ", wantType: "openrouter"},
		{name: "normalized OpenAI URL", baseURL: "https://API.OPENAI.COM/v1/", wantType: "openai"},
		{name: "normalized MiniMax URL", baseURL: "https://api.minimaxi.chat/v1", wantType: "minimax"},
		{name: "normalized Qwen URL", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", wantType: "qwen"},
		{name: "normalized Z.ai URL", baseURL: "https://api.z.ai/v1", wantType: "zai"},
		{name: "LM Studio port", baseURL: "http://vidar:1234/v1", wantType: "lmstudio"},
		{name: "oMLX port", baseURL: "http://vidar:1235/v1", wantType: "omlx"},
		{name: "Ollama port", baseURL: "http://vidar:11434/v1", wantType: "ollama"},
		{name: "normalized Lucebox port", baseURL: " HTTP://VIDAR:1236/v1 ", wantType: "lucebox"},
		{name: "normalized vLLM port", baseURL: " HTTP://VIDAR:8000/v1 ", wantType: "vllm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeOpenCodeTranslationConfig(t, tmpDir, tt.baseURL, "@ai-sdk/openai-compatible", "", nil)

			result := TranslateProvider(tmpDir, tt.sourceIdentity, "")

			require.True(t, result.HasProvider)
			assert.Equal(t, tt.wantType, result.Provider.Type)
		})
	}
}

func TestTranslateOpenCodeSkipsUnknownConcreteType(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
	}{
		{name: "unknown host", baseURL: "https://api.example.invalid/v1?token=url-secret-canary"},
		{name: "known domain suffix spoof", baseURL: "https://api.openai.com.evil.invalid/v1?token=url-secret-canary"},
		{name: "known port in path", baseURL: "https://api.example.invalid/path/:1234?token=url-secret-canary"},
		{name: "Lucebox port in path", baseURL: "https://api.example.invalid/path/:1236?token=url-secret-canary"},
		{name: "vLLM port in path", baseURL: "https://api.example.invalid/path/:8000?token=url-secret-canary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeOpenCodeTranslationConfig(t, tmpDir,
				tt.baseURL,
				"@ai-sdk/openai-compatible",
				"config-secret-canary",
				map[string]string{"Authorization": "header-secret-canary"},
			)

			result := TranslateProvider(tmpDir, "mystery", "auth-secret-canary")

			assert.False(t, result.HasProvider)
			assert.Zero(t, result.Provider)
			require.NotEmpty(t, result.Warnings)
			warning := strings.Join(result.Warnings, "\n")
			assert.Contains(t, warning, "skipped OpenCode provider")
			assert.Contains(t, warning, "concrete provider type")
			assert.Contains(t, warning, "supported provider key")
			lowerWarning := strings.ToLower(warning)
			assert.NotContains(t, lowerWarning, "openai")
			assert.NotContains(t, lowerWarning, "lmstudio")
			assert.NotContains(t, lowerWarning, "openai-compat")
			for _, secret := range []string{"config-secret-canary", "auth-secret-canary", "header-secret-canary", "url-secret-canary"} {
				assert.NotContains(t, warning, secret)
			}
			assert.NotContains(t, strings.ToLower(result.Provider.Type), "openai")
			assert.NotEqual(t, "lmstudio", result.Provider.Type)
		})
	}
}

func TestTranslateOpenCodeProtocolDoesNotSelectProviderIdentity(t *testing.T) {
	t.Run("protocol with concrete identity", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeOpenCodeTranslationConfig(t, tmpDir, "", "@ai-sdk/openai-compatible", "", nil)

		result := TranslateProvider(tmpDir, "openrouter", "")

		require.True(t, result.HasProvider)
		assert.Equal(t, "openrouter", result.Provider.Type)
	})

	t.Run("protocol without concrete identity", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeOpenCodeTranslationConfig(t, tmpDir, "", "@ai-sdk/openai-compatible", "", nil)

		result := TranslateProvider(tmpDir, "unknown", "")

		assert.False(t, result.HasProvider)
		assert.Empty(t, result.Provider.Type)
	})
}

func TestTranslate_Headers(t *testing.T) {
	tmpDir := t.TempDir()

	configJSON := `{
		"options": {
			"headers": {
				"HTTP-Referer": "https://example.com",
				"X-Title": "My App"
			}
		}
	}`
	err := os.WriteFile(filepath.Join(tmpDir, "opencode.json"), []byte(configJSON), 0644)
	require.NoError(t, err)

	result := TranslateProvider(tmpDir, "openrouter", "")

	assert.True(t, result.HasProvider)
	assert.Equal(t, "https://example.com", result.Provider.Headers["HTTP-Referer"])
	assert.Equal(t, "My App", result.Provider.Headers["X-Title"])
}

func writeOpenCodeTranslationConfig(t *testing.T, dir, baseURL, npm, apiKey string, headers map[string]string) {
	t.Helper()
	options := map[string]any{}
	if baseURL != "" {
		options["baseURL"] = baseURL
	}
	if npm != "" {
		options["npm"] = npm
	}
	if apiKey != "" {
		options["apiKey"] = apiKey
	}
	if headers != nil {
		options["headers"] = headers
	}
	data, err := json.Marshal(map[string]any{"options": options})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "opencode.json"), data, 0o600))
}

func TestComputeSourceHash(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "auth.json"), []byte("test"), 0644)
	require.NoError(t, err)

	// Create global config
	globalDir := filepath.Join(tmpDir, ".config", "opencode")
	err = os.MkdirAll(globalDir, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(globalDir, "opencode.json"), []byte("test"), 0644)
	require.NoError(t, err)

	// Temporarily change home for this test
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	hash, err := ComputeSourceHash(tmpDir)
	require.NoError(t, err)
	assert.Len(t, hash, 8) // truncated to 8 chars
}

func TestDefaultOpenCodeDir(t *testing.T) {
	dir := DefaultOpenCodeDir()
	assert.Contains(t, dir, "opencode")
}

func TestCheckExists(t *testing.T) {
	// Without actual opencode config, should return false
	// This test just verifies the function doesn't panic
	exists := CheckExists()
	// Result depends on whether opencode is installed
	_ = exists // just verify it runs
}
