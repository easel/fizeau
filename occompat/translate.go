package occompat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"

	agentConfig "github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/safefs"
)

// TranslationResult contains the result of translating opencode config to agent config.
type TranslationResult struct {
	Provider    agentConfig.ProviderConfig
	HasProvider bool
	Warnings    []string
}

// Translate converts opencode configuration to agent provider config per SD-007.
// Callers that know the OpenCode provider key should use TranslateProvider so
// an explicit source identity can participate in concrete type resolution.
func Translate(opencodeDir, authKey string) *TranslationResult {
	return TranslateProvider(opencodeDir, "", authKey)
}

// TranslateProvider converts one OpenCode provider into a Fizeau provider.
// sourceIdentity is the OpenCode provider key, not the npm protocol package.
func TranslateProvider(opencodeDir, sourceIdentity, authKey string) *TranslationResult {
	result := &TranslationResult{}

	// Load opencode config
	cfg, err := LoadConfig(opencodeDir)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("could not load opencode config: %v", err))
		return result
	}

	providerType, ok := concreteProviderType(sourceIdentity, cfg.Options.BaseURL)
	if !ok {
		identity := strings.TrimSpace(sourceIdentity)
		if identity == "" {
			identity = "unknown"
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"skipped OpenCode provider %q: could not resolve a concrete provider type; choose a supported provider key or set options.baseURL to a recognized endpoint",
			identity,
		))
		return result
	}

	provider := agentConfig.ProviderConfig{
		Type:    providerType,
		BaseURL: strings.TrimSpace(cfg.Options.BaseURL),
	}

	// Map options.apiKey or auth.json key → api_key.
	if cfg.Options.APIKey != "" {
		provider.APIKey = cfg.Options.APIKey
	} else if authKey != "" {
		provider.APIKey = authKey
	}

	// Map options.headers.
	if len(cfg.Options.Headers) > 0 {
		provider.Headers = cfg.Options.Headers
	}

	// npm identifies the protocol reader only. Preserve the existing warning for
	// protocol packages this translator does not understand, without using npm
	// as evidence for provider identity.
	if npm := strings.TrimSpace(cfg.Options.NPM); npm != "" && npm != "@ai-sdk/openai-compatible" {
		result.Warnings = append(result.Warnings, "unsupported npm protocol reader; only @ai-sdk/openai-compatible is supported")
	}

	result.Provider = provider
	result.HasProvider = true
	return result
}

func concreteProviderType(sourceIdentity, baseURL string) (string, bool) {
	knownIdentities := map[string]string{
		"openai":       "openai",
		"openrouter":   "openrouter",
		"lmstudio":     "lmstudio",
		"minimax":      "minimax",
		"qwen":         "qwen",
		"dashscope":    "qwen",
		"z.ai":         "zai",
		"zai":          "zai",
		"ollama":       "ollama",
		"omlx":         "omlx",
		"ds4":          "ds4",
		"llama-server": "llama-server",
		"lucebox":      "lucebox",
		"rapid-mlx":    "rapid-mlx",
		"vllm":         "vllm",
	}
	if providerType, ok := knownIdentities[strings.ToLower(strings.TrimSpace(sourceIdentity))]; ok {
		return providerType, true
	}
	return concreteProviderTypeFromBaseURL(baseURL)
}

func concreteProviderTypeFromBaseURL(baseURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return "", false
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return "", false
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	switch {
	case hostMatchesDomain(host, "openrouter.ai"):
		return "openrouter", true
	case hostMatchesDomain(host, "openai.com"):
		return "openai", true
	case hostMatchesDomain(host, "minimaxi.chat"):
		return "minimax", true
	case hostMatchesDomain(host, "dashscope.aliyuncs.com"):
		return "qwen", true
	case hostMatchesDomain(host, "z.ai"):
		return "zai", true
	}

	switch parsed.Port() {
	case "11434":
		return "ollama", true
	case "8000":
		return "vllm", true
	case "1236":
		return "lucebox", true
	case "1235":
		return "omlx", true
	case "1234":
		return "lmstudio", true
	default:
		return "", false
	}
}

func hostMatchesDomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// ComputeSourceHash computes a truncated SHA-256 hash of the source files.
func ComputeSourceHash(opencodeDir string) (string, error) {
	authPath := opencodeDir + "/auth.json"
	// Try project config first
	configPath := "opencode.json"
	home, _ := os.UserHomeDir()
	if home != "" {
		configPath = home + "/.config/opencode/opencode.json"
	}

	authData, err := safefs.ReadFile(authPath)
	if err != nil {
		return "", err
	}
	configData, err := safefs.ReadFile(configPath)
	if err != nil {
		return "", err
	}

	combined := append(authData, configData...)
	h := sha256.Sum256(combined)
	return hex.EncodeToString(h[:])[:8], nil
}
