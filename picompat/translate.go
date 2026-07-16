package picompat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	agentConfig "github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/safefs"
)

// ProviderMapping maps pi provider names to agent configurations.
type ProviderMapping struct {
	AgentName string // name in agent config
	Type      string // agent type, e.g. "anthropic", "openai", "openrouter"
	BaseURL   string // default URL if not specified in pi
}

// Known mappings per SD-007. Keyed by the provider name as it appears in
// pi's auth.json. BaseURL is the canonical OpenAI-compatible endpoint for
// cloud providers; local providers get their URL from models.json instead.
var knownMappings = map[string]ProviderMapping{
	// Established cloud providers
	"anthropic":    {AgentName: "anthropic", Type: "anthropic"},
	"openai-codex": {AgentName: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1"},
	"openrouter":   {AgentName: "openrouter", Type: "openrouter", BaseURL: "https://openrouter.ai/api/v1"},
	// Qwen / Alibaba Cloud DashScope
	"qwen":      {AgentName: "qwen", Type: "qwen", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	"dashscope": {AgentName: "qwen", Type: "qwen", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	// MiniMax
	"minimax": {AgentName: "minimax", Type: "minimax", BaseURL: "https://api.minimaxi.chat/v1"},
	// Z.ai
	"z.ai": {AgentName: "z.ai", Type: "zai", BaseURL: "https://api.z.ai/v1"},
	"zai":  {AgentName: "z.ai", Type: "zai", BaseURL: "https://api.z.ai/v1"},
}

// concreteProviderTypes are the source identities that can be persisted as
// provider identity without consulting protocol compatibility. The api field
// in Pi's models.json selects a wire reader; it is not evidence of who owns the
// endpoint.
var concreteProviderTypes = map[string]string{
	"anthropic":    "anthropic",
	"openai":       "openai",
	"openrouter":   "openrouter",
	"lmstudio":     "lmstudio",
	"llama-server": "llama-server",
	"ds4":          "ds4",
	"omlx":         "omlx",
	"lucebox":      "lucebox",
	"vllm":         "vllm",
	"rapid-mlx":    "rapid-mlx",
	"ollama":       "ollama",
	"minimax":      "minimax",
	"qwen":         "qwen",
	"zai":          "zai",
}

// reasoningModelRe matches model IDs that benefit from an explicit portable
// reasoning default during import.
var reasoningModelRe = regexp.MustCompile(
	`(?i)^(qwen3|qwen-3|deepseek-r1|deepseek_r1|qwq)`,
)

// Warnings collects import warnings.
type Warnings []string

// Add appends a warning message.
func (w *Warnings) Add(format string, args ...interface{}) {
	*w = append(*w, fmt.Sprintf(format, args...))
}

// TranslationResult contains the result of translating pi config to agent config.
type TranslationResult struct {
	Providers map[string]agentConfig.ProviderConfig
	Default   string
	Warnings  Warnings
}

// Translate merges pi auth, models, and settings into agent provider configs.
// It implements the two-source merge algorithm from SD-007.
func Translate(piDir string) (*TranslationResult, error) {
	result := &TranslationResult{
		Providers: make(map[string]agentConfig.ProviderConfig),
	}

	// Load all three sources
	auth, err := LoadAuth(piDir)
	if err != nil {
		return nil, fmt.Errorf("reading pi auth.json: %w", err)
	}

	models, err := LoadModels(piDir)
	if err != nil {
		return nil, fmt.Errorf("reading pi models.json: %w", err)
	}

	var settings *Settings
	settings, _ = LoadSettings(piDir) // settings is optional

	// Step 1: Start with models.json providers (have baseUrl and model IDs)
	for _, provider := range models.Providers {
		sourceName := providerDefinitionName(provider)
		pc, ok := translateProvider(provider, authEntryForProvider(auth, sourceName))
		if !ok {
			result.Warnings.Add(
				"skipped provider %q: could not resolve a concrete provider type; use a supported provider name or recognized base_url",
				sourceName,
			)
			continue
		}
		result.Providers[pc.Name] = pc.Config
	}

	// Step 2 & 3: For auth.json entries with NO matching models.json provider,
	// create agent providers using well-known defaults
	for name, cred := range auth {
		// Skip if already added from models
		targetName := strings.TrimSpace(name)
		if mapping, known := knownProviderMapping(name); known {
			targetName = mapping.AgentName
		}
		if _, exists := result.Providers[targetName]; exists {
			continue
		}

		// Skip unsupported providers per SD-007
		if name == "google-gemini-cli" || name == "github-copilot" {
			result.Warnings.Add("skipped provider %q: not yet supported", name)
			continue
		}

		// Check for !command API key
		resolvedKey := cred.ResolvedKey()
		if len(resolvedKey) > 0 && resolvedKey[0] == '!' {
			result.Warnings.Add("provider %q uses shell-resolved key, set FIZEAU_API_KEY or add api_key manually", name)
			continue
		}

		// Try known mappings
		if mapping, known := knownProviderMapping(name); known {
			pc := agentConfig.ProviderConfig{
				Type:   mapping.Type,
				APIKey: resolvedKey,
			}
			if mapping.BaseURL != "" {
				pc.BaseURL = mapping.BaseURL
			}
			result.Providers[mapping.AgentName] = pc
			continue
		}

		// Unknown provider
		result.Warnings.Add("skipped provider %q: unknown provider type", name)
	}

	// Step 4: Apply settings.json defaultProvider/defaultModel
	if settings != nil && settings.DefaultProvider != "" {
		// Map pi provider name to agent name
		agentName := settings.DefaultProvider
		if mapping, known := knownProviderMapping(settings.DefaultProvider); known {
			agentName = mapping.AgentName
		}
		// Check if this provider exists
		if pc, exists := result.Providers[agentName]; exists {
			result.Default = agentName
			if settings.DefaultModel != "" {
				pc.Model = settings.DefaultModel
				result.Providers[agentName] = pc
			}
		} else if settings.DefaultProvider != "" {
			result.Warnings.Add("default provider %q not found in config", settings.DefaultProvider)
		}
	}

	return result, nil
}

// translatedProvider holds both the agent name and config.
type translatedProvider struct {
	Name   string
	Config agentConfig.ProviderConfig
}

func translateProvider(def ProviderDefinition, cred AuthEntry) (translatedProvider, bool) {
	name := providerDefinitionName(def)
	mapping, ok := concreteProviderIdentity(name, def.BaseURL)
	if !ok {
		return translatedProvider{}, false
	}

	pc := agentConfig.ProviderConfig{
		Type: mapping.Type,
	}
	if baseURL := strings.TrimSpace(def.BaseURL); baseURL != "" {
		pc.BaseURL = baseURL
	} else if mapping.BaseURL != "" {
		pc.BaseURL = mapping.BaseURL
	}

	// Prefer auth.json credential, fall back to model's inline api_key.
	credKey := cred.ResolvedKey()
	if credKey != "" && credKey[0] != '!' {
		pc.APIKey = credKey
	} else if def.APIKey != "" && def.APIKey[0] != '!' {
		pc.APIKey = def.APIKey
	}

	// Set model if specified.
	if len(def.Models) > 0 {
		pc.Model = def.Models[0]
	}

	// Auto-configure reasoning for known reasoning models (Qwen3, DeepSeek-R1,
	// QwQ). Only set when not already specified.
	if pc.Reasoning == "" && pc.Model != "" && isReasoningModel(pc.Model) {
		pc.Reasoning = reasoning.ReasoningMedium
	}

	return translatedProvider{Name: mapping.AgentName, Config: pc}, true
}

func providerDefinitionName(def ProviderDefinition) string {
	if name := strings.TrimSpace(def.Name); name != "" {
		return name
	}
	return strings.TrimSpace(def.Provider)
}

func knownProviderMapping(name string) (ProviderMapping, bool) {
	mapping, ok := knownMappings[strings.ToLower(strings.TrimSpace(name))]
	return mapping, ok
}

// authEntryForProvider applies Pi's two-source merge precedence. An exact
// models.json/auth.json provider-name match wins. When the model source uses a
// known alias, a credential filed under another alias for the same canonical
// provider is the fallback. Sorting makes that fallback stable if a future Pi
// config contains more than one non-exact alias.
func authEntryForProvider(auth AuthCredentials, sourceName string) AuthEntry {
	if cred, ok := auth[sourceName]; ok {
		return cred
	}

	sourceMapping, ok := knownProviderMapping(sourceName)
	if !ok {
		return AuthEntry{}
	}
	aliases := make([]string, 0, len(auth))
	for name := range auth {
		mapping, known := knownProviderMapping(name)
		if known && mapping.AgentName == sourceMapping.AgentName {
			aliases = append(aliases, name)
		}
	}
	if len(aliases) == 0 {
		return AuthEntry{}
	}
	sort.Strings(aliases)
	return auth[aliases[0]]
}

func concreteProviderIdentity(name, baseURL string) (ProviderMapping, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderMapping{}, false
	}
	if mapping, ok := knownProviderMapping(name); ok {
		return mapping, true
	}
	if providerType, ok := concreteProviderTypes[strings.ToLower(name)]; ok {
		return ProviderMapping{AgentName: name, Type: providerType}, true
	}
	if providerType := concreteProviderTypeFromBaseURL(baseURL); providerType != "" {
		return ProviderMapping{AgentName: name, Type: providerType}, true
	}
	return ProviderMapping{}, false
}

func concreteProviderTypeFromBaseURL(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	switch {
	case hostMatchesDomain(host, "openrouter.ai"):
		return "openrouter"
	case hostMatchesDomain(host, "openai.com"):
		return "openai"
	case hostMatchesDomain(host, "minimaxi.chat"):
		return "minimax"
	case hostMatchesDomain(host, "dashscope.aliyuncs.com"):
		return "qwen"
	case hostMatchesDomain(host, "z.ai"):
		return "zai"
	}
	switch parsed.Port() {
	case "11434":
		return "ollama"
	case "1234":
		return "lmstudio"
	case "1235":
		return "omlx"
	case "1236":
		return "lucebox"
	case "8000":
		return "vllm"
	default:
		return ""
	}
}

func hostMatchesDomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// isReasoningModel reports whether modelID belongs to a model family that
// benefits from an explicit reasoning default. Claude-distilled variants (e.g.
// "qwen3-claude-opus-distilled") are excluded.
func isReasoningModel(modelID string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelID))
	if strings.Contains(lower, "claude") {
		return false
	}
	return reasoningModelRe.MatchString(strings.TrimSpace(modelID))
}

// ComputeSourceHash computes a truncated SHA-256 hash of the source files.
func ComputeSourceHash(piDir string) (string, error) {
	authPath := piDir + "/agent/auth.json"
	modelsPath := piDir + "/agent/models.json"

	authData, err := safefs.ReadFile(authPath)
	if err != nil {
		return "", err
	}
	modelsData, err := safefs.ReadFile(modelsPath)
	if err != nil {
		return "", err
	}

	// Concatenate and hash
	combined := append(authData, modelsData...)
	return hashString(combined), nil
}

func hashString(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:8]
}
