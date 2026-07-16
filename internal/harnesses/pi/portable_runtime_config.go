package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const (
	piPortableSettingsTarget   = "config/pi/settings.json"
	piPortableModelsTarget     = "config/pi/models.json"
	piPortableAuthTarget       = "state/pi/auth.json"
	piPortableAgentDirectory   = ".pi/agent"
	piPortableMaxConfigBytes   = 8 << 20
	piPortableSettingsFilename = "settings.json"
	piPortableModelsFilename   = "models.json"
	piPortableAuthFilename     = "auth.json"
)

// piPortableConfiguration is the value-opaque result of validating Pi's exact
// three-file configuration boundary. Environment contains names only. Assets
// retain source paths and digests solely for the later portable contributor;
// they are never projected through a public plan.
type piPortableConfiguration struct {
	assets           []harnesses.PortableRuntimeAsset
	environment      []harnesses.PortableRuntimeEnvironment
	stateProjections []harnesses.PortableRuntimeStateProjection
	authPresent      bool
	oauthRefresh     bool
}

// inspectPiPortableConfiguration validates Pi configuration before control may
// pass to packageManager.resolve. afterValidation is a narrow ordering seam:
// the contributor passes nil, while tests use it to prove unsafe settings and
// agent files fail before package resolution or a package hook can run.
func inspectPiPortableConfiguration(ctx context.Context, agentDir string, afterValidation func() error) (piPortableConfiguration, error) {
	if ctx == nil {
		return piPortableConfiguration{}, piPortableConfigError("configuration context is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return piPortableConfiguration{}, piPortableConfigError("configuration discovery canceled")
	}
	if !filepath.IsAbs(agentDir) || filepath.Clean(agentDir) != agentDir {
		return piPortableConfiguration{}, piPortableConfigError("configuration root is invalid")
	}

	settingsPath := filepath.Join(agentDir, piPortableSettingsFilename)
	settingsData, settingsDigest, err := readPiPortableConfigFile(settingsPath)
	if err != nil {
		return piPortableConfiguration{}, piPortableConfigError("settings state is unavailable")
	}
	if err := validatePiPortableSettings(settingsData); err != nil {
		return piPortableConfiguration{}, err
	}
	if err := rejectPiPortableAgentSources(agentDir); err != nil {
		return piPortableConfiguration{}, err
	}

	modelsPath := filepath.Join(agentDir, piPortableModelsFilename)
	modelsData, modelsDigest, err := readPiPortableConfigFile(modelsPath)
	if err != nil {
		return piPortableConfiguration{}, piPortableConfigError("model state is unavailable")
	}
	environment, err := validatePiPortableModels(modelsData)
	if err != nil {
		return piPortableConfiguration{}, err
	}

	result := piPortableConfiguration{
		assets: []harnesses.PortableRuntimeAsset{
			{Kind: harnesses.PortableRuntimeAssetConfig, PathKind: harnesses.PortableRuntimePathFile, Source: settingsPath, Target: piPortableSettingsTarget, ContentSHA256: settingsDigest},
			{Kind: harnesses.PortableRuntimeAssetConfig, PathKind: harnesses.PortableRuntimePathFile, Source: modelsPath, Target: piPortableModelsTarget, ContentSHA256: modelsDigest},
		},
		environment: environment,
	}

	authPath := filepath.Join(agentDir, piPortableAuthFilename)
	present, err := piPortablePathExists(authPath)
	if err != nil {
		return piPortableConfiguration{}, piPortableConfigError("credential state cannot be inspected")
	}
	if !present {
		if err := runPiPortablePreResolutionHandoff(afterValidation); err != nil {
			return piPortableConfiguration{}, err
		}
		return result, nil
	}
	authData, authDigest, err := readPiPortableConfigFile(authPath)
	if err != nil {
		return piPortableConfiguration{}, piPortableConfigError("credential state is unavailable")
	}
	oauthRefresh, err := validatePiPortableAuth(authData)
	if err != nil {
		return piPortableConfiguration{}, err
	}
	result.authPresent = true
	result.oauthRefresh = oauthRefresh
	result.assets = append(result.assets, harnesses.PortableRuntimeAsset{
		Kind: harnesses.PortableRuntimeAssetCredential, PathKind: harnesses.PortableRuntimePathFile,
		Source: authPath, Target: piPortableAuthTarget, ContentSHA256: authDigest,
	})
	result.stateProjections = []harnesses.PortableRuntimeStateProjection{{
		Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: piPortableAgentDirectory},
		Entries: []harnesses.PortableRuntimeStateProjectionEntry{
			{AssetTarget: piPortableAuthTarget, Target: piPortableAuthFilename},
			{AssetTarget: piPortableModelsTarget, Target: piPortableModelsFilename},
			{AssetTarget: piPortableSettingsTarget, Target: piPortableSettingsFilename},
		},
	}}
	if err := runPiPortablePreResolutionHandoff(afterValidation); err != nil {
		return piPortableConfiguration{}, err
	}
	return result, nil
}

func runPiPortablePreResolutionHandoff(afterValidation func() error) error {
	if afterValidation == nil {
		return nil
	}
	if err := afterValidation(); err != nil {
		return piPortableConfigError("pre-resolution handoff failed")
	}
	return nil
}

// The accepted settings keys are copied from Pi 0.51.4's Settings interface.
// Resource and executable-source keys are recognized separately and rejected,
// including empty declarations, so a later Pi release cannot turn a tolerated
// value into a code-loading surface.
type piPortableSettings struct {
	LastChangelogVersion   *string                    `json:"lastChangelogVersion"`
	DefaultProvider        *string                    `json:"defaultProvider"`
	DefaultModel           *string                    `json:"defaultModel"`
	DefaultThinkingLevel   *string                    `json:"defaultThinkingLevel"`
	SteeringMode           *string                    `json:"steeringMode"`
	FollowUpMode           *string                    `json:"followUpMode"`
	Theme                  *string                    `json:"theme"`
	Compaction             *piPortableCompaction      `json:"compaction"`
	BranchSummary          *piPortableBranchSummary   `json:"branchSummary"`
	Retry                  *piPortableRetry           `json:"retry"`
	HideThinkingBlock      *bool                      `json:"hideThinkingBlock"`
	QuietStartup           *bool                      `json:"quietStartup"`
	CollapseChangelog      *bool                      `json:"collapseChangelog"`
	EnableSkillCommands    *bool                      `json:"enableSkillCommands"`
	Terminal               *piPortableTerminal        `json:"terminal"`
	Images                 *piPortableImages          `json:"images"`
	EnabledModels          []string                   `json:"enabledModels"`
	DoubleEscapeAction     *string                    `json:"doubleEscapeAction"`
	ThinkingBudgets        *piPortableThinkingBudgets `json:"thinkingBudgets"`
	EditorPaddingX         *float64                   `json:"editorPaddingX"`
	AutocompleteMaxVisible *float64                   `json:"autocompleteMaxVisible"`
	ShowHardwareCursor     *bool                      `json:"showHardwareCursor"`
	Markdown               *piPortableMarkdown        `json:"markdown"`
}

type piPortableCompaction struct {
	Enabled          *bool    `json:"enabled"`
	ReserveTokens    *float64 `json:"reserveTokens"`
	KeepRecentTokens *float64 `json:"keepRecentTokens"`
}

type piPortableBranchSummary struct {
	ReserveTokens *float64 `json:"reserveTokens"`
}

type piPortableRetry struct {
	Enabled     *bool    `json:"enabled"`
	MaxRetries  *float64 `json:"maxRetries"`
	BaseDelayMS *float64 `json:"baseDelayMs"`
	MaxDelayMS  *float64 `json:"maxDelayMs"`
}

type piPortableTerminal struct {
	ShowImages    *bool `json:"showImages"`
	ClearOnShrink *bool `json:"clearOnShrink"`
}

type piPortableImages struct {
	AutoResize  *bool `json:"autoResize"`
	BlockImages *bool `json:"blockImages"`
}

type piPortableThinkingBudgets struct {
	Minimal *float64 `json:"minimal"`
	Low     *float64 `json:"low"`
	Medium  *float64 `json:"medium"`
	High    *float64 `json:"high"`
}

type piPortableMarkdown struct {
	CodeBlockIndent *string `json:"codeBlockIndent"`
}

func validatePiPortableSettings(data []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return piPortableConfigError("settings state is invalid")
	}
	for _, key := range []string{"packages", "extensions", "skills", "prompts", "themes"} {
		if _, exists := fields[key]; exists {
			return piPortableConfigError("settings declare an external resource source")
		}
	}
	for _, key := range []string{"shellPath", "shellCommandPrefix"} {
		if _, exists := fields[key]; exists {
			return piPortableConfigError("settings declare an executable source")
		}
	}
	var settings piPortableSettings
	if !decodePiPortableExactJSON(data, &settings) {
		return piPortableConfigError("settings contain an unsupported field or value")
	}
	return nil
}

type piPortableModels struct {
	Providers map[string]piPortableProvider `json:"providers"`
}

type piPortableProvider struct {
	BaseURL    *string           `json:"baseUrl"`
	APIKey     *string           `json:"apiKey"`
	API        *string           `json:"api"`
	Headers    map[string]string `json:"headers"`
	AuthHeader *bool             `json:"authHeader"`
	Models     []piPortableModel `json:"models"`
}

type piPortableModel struct {
	ID            string                 `json:"id"`
	Name          *string                `json:"name"`
	API           *string                `json:"api"`
	Reasoning     *bool                  `json:"reasoning"`
	Input         []string               `json:"input"`
	Cost          *piPortableModelCost   `json:"cost"`
	ContextWindow *float64               `json:"contextWindow"`
	MaxTokens     *float64               `json:"maxTokens"`
	Headers       map[string]string      `json:"headers"`
	Compat        *piPortableModelCompat `json:"compat"`
}

type piPortableModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type piPortableModelCompat struct {
	SupportsStore                    *bool                   `json:"supportsStore"`
	SupportsDeveloperRole            *bool                   `json:"supportsDeveloperRole"`
	SupportsReasoningEffort          *bool                   `json:"supportsReasoningEffort"`
	SupportsUsageInStreaming         *bool                   `json:"supportsUsageInStreaming"`
	MaxTokensField                   *string                 `json:"maxTokensField"`
	RequiresToolResultName           *bool                   `json:"requiresToolResultName"`
	RequiresAssistantAfterToolResult *bool                   `json:"requiresAssistantAfterToolResult"`
	RequiresThinkingAsText           *bool                   `json:"requiresThinkingAsText"`
	RequiresMistralToolIDs           *bool                   `json:"requiresMistralToolIds"`
	ThinkingFormat                   *string                 `json:"thinkingFormat"`
	OpenRouterRouting                *piPortableModelRouting `json:"openRouterRouting"`
	VercelGatewayRouting             *piPortableModelRouting `json:"vercelGatewayRouting"`
}

type piPortableModelRouting struct {
	Only  []string `json:"only"`
	Order []string `json:"order"`
}

func validatePiPortableModels(data []byte) ([]harnesses.PortableRuntimeEnvironment, error) {
	var models piPortableModels
	if !decodePiPortableExactJSON(data, &models) || models.Providers == nil {
		return nil, piPortableConfigError("model state contains an unsupported field or value")
	}
	names := make(map[string]struct{})
	providerNames := make([]string, 0, len(models.Providers))
	for name := range models.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for providerIndex, name := range providerNames {
		if strings.TrimSpace(name) == "" {
			return nil, piPortableConfigErrorAt("model provider", providerIndex, "has an invalid identifier")
		}
		provider := models.Providers[name]
		if err := validatePiPortableProviderSchema(provider); err != nil {
			return nil, piPortableConfigErrorAt("model provider", providerIndex, err.Error())
		}
		if provider.APIKey != nil {
			if err := inspectPiPortableCredentialReference(*provider.APIKey, names); err != nil {
				return nil, piPortableConfigErrorAt("model provider credential", providerIndex, err.Error())
			}
		}
		if err := inspectPiPortableHeaderReferences(provider.Headers, names); err != nil {
			return nil, piPortableConfigErrorAt("model provider headers", providerIndex, err.Error())
		}
		for modelIndex, model := range provider.Models {
			if strings.TrimSpace(model.ID) == "" {
				return nil, piPortableConfigErrorAt("model", modelIndex, "has an invalid identifier")
			}
			if err := inspectPiPortableHeaderReferences(model.Headers, names); err != nil {
				return nil, piPortableConfigErrorAt("model headers", modelIndex, err.Error())
			}
		}
	}

	environment := make([]harnesses.PortableRuntimeEnvironment, 0, len(names))
	for name := range names {
		environment = append(environment, harnesses.PortableRuntimeEnvironment{Name: name})
	}
	sort.Slice(environment, func(i, j int) bool { return environment[i].Name < environment[j].Name })
	return environment, nil
}

func validatePiPortableProviderSchema(provider piPortableProvider) error {
	models := provider.Models
	if len(models) == 0 {
		if provider.BaseURL == nil || strings.TrimSpace(*provider.BaseURL) == "" {
			return errors.New("has an incomplete override schema")
		}
		return nil
	}
	if provider.BaseURL == nil || strings.TrimSpace(*provider.BaseURL) == "" || provider.APIKey == nil || *provider.APIKey == "" {
		return errors.New("has an incomplete replacement schema")
	}
	for _, model := range models {
		if provider.API == nil && model.API == nil {
			return errors.New("has a model without an API schema")
		}
		for _, input := range model.Input {
			if input != "text" && input != "image" {
				return errors.New("has a model with an unsupported input schema")
			}
		}
		if model.ContextWindow != nil && (!finitePositivePiPortableNumber(*model.ContextWindow) || math.Trunc(*model.ContextWindow) != *model.ContextWindow) {
			return errors.New("has a model with an invalid context window")
		}
		if model.MaxTokens != nil && (!finitePositivePiPortableNumber(*model.MaxTokens) || math.Trunc(*model.MaxTokens) != *model.MaxTokens) {
			return errors.New("has a model with an invalid token limit")
		}
		if model.Cost != nil && (!finiteNonNegativePiPortableNumber(model.Cost.Input) ||
			!finiteNonNegativePiPortableNumber(model.Cost.Output) ||
			!finiteNonNegativePiPortableNumber(model.Cost.CacheRead) ||
			!finiteNonNegativePiPortableNumber(model.Cost.CacheWrite)) {
			return errors.New("has a model with an invalid cost schema")
		}
	}
	return nil
}

func finitePositivePiPortableNumber(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteNonNegativePiPortableNumber(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func inspectPiPortableHeaderReferences(headers map[string]string, names map[string]struct{}) error {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := inspectPiPortableCredentialReference(headers[key], names); err != nil {
			return err
		}
	}
	return nil
}

func inspectPiPortableCredentialReference(reference string, names map[string]struct{}) error {
	if strings.HasPrefix(reference, "!") {
		return errors.New("declares an executable credential source")
	}
	if !validPiPortableEnvironmentName(reference) {
		return nil // Pi treats non-environment names as literal static values.
	}
	if forbiddenPiPortableEnvironmentName(reference) {
		return errors.New("declares a forbidden environment reference")
	}
	if value, exists := os.LookupEnv(reference); exists && value != "" {
		names[reference] = struct{}{}
	}
	return nil
}

func validatePiPortableAuth(data []byte) (bool, error) {
	var records map[string]json.RawMessage
	if json.Unmarshal(data, &records) != nil || records == nil {
		return false, piPortableConfigError("credential state is invalid")
	}
	oauthRefresh := false
	providerNames := make([]string, 0, len(records))
	for provider := range records {
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)
	for index, provider := range providerNames {
		if provider == "" {
			return false, piPortableConfigErrorAt("credential record", index, "has an invalid provider")
		}
		var discriminator struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(records[provider], &discriminator) != nil {
			return false, piPortableConfigErrorAt("credential record", index, "is invalid")
		}
		switch discriminator.Type {
		case "api_key":
			var credential struct {
				Type string `json:"type"`
				Key  string `json:"key"`
			}
			if !decodePiPortableExactJSON(records[provider], &credential) || credential.Key == "" {
				return false, piPortableConfigErrorAt("credential record", index, "has an invalid API-key schema")
			}
		case "oauth":
			if err := validatePiPortableOAuthRecord(provider, records[provider]); err != nil {
				return false, piPortableConfigErrorAt("credential record", index, err.Error())
			}
			oauthRefresh = true
		default:
			return false, piPortableConfigErrorAt("credential record", index, "has an unsupported type")
		}
	}
	return oauthRefresh, nil
}

type piPortableOAuthCredential struct {
	Type          string  `json:"type"`
	Refresh       string  `json:"refresh"`
	Access        string  `json:"access"`
	Expires       float64 `json:"expires"`
	AccountID     *string `json:"accountId"`
	ProjectID     *string `json:"projectId"`
	EnterpriseURL *string `json:"enterpriseUrl"`
}

func validatePiPortableOAuthRecord(provider string, data []byte) error {
	var credential piPortableOAuthCredential
	if !decodePiPortableExactJSON(data, &credential) || credential.Refresh == "" || credential.Access == "" ||
		credential.Expires <= 0 || math.IsNaN(credential.Expires) || math.IsInf(credential.Expires, 0) || math.Trunc(credential.Expires) != credential.Expires {
		return errors.New("has an invalid OAuth schema")
	}
	switch provider {
	case "anthropic":
		if credential.AccountID != nil || credential.ProjectID != nil || credential.EnterpriseURL != nil {
			return errors.New("has unsupported OAuth fields")
		}
	case "openai-codex":
		if credential.AccountID == nil || *credential.AccountID == "" || credential.ProjectID != nil || credential.EnterpriseURL != nil {
			return errors.New("has an invalid OAuth provider schema")
		}
	case "google-gemini-cli", "google-antigravity":
		if credential.ProjectID == nil || *credential.ProjectID == "" || credential.AccountID != nil || credential.EnterpriseURL != nil {
			return errors.New("has an invalid OAuth provider schema")
		}
	case "github-copilot":
		if credential.AccountID != nil || credential.ProjectID != nil {
			return errors.New("has unsupported OAuth fields")
		}
	default:
		return errors.New("has an unsupported OAuth provider")
	}
	return nil
}

func rejectPiPortableAgentSources(agentDir string) error {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "SYSTEM.md", "APPEND_SYSTEM.md"} {
		present, err := piPortablePathExists(filepath.Join(agentDir, name))
		if err != nil {
			return piPortableConfigError("agent source state cannot be inspected")
		}
		if present {
			return piPortableConfigError("agent directory contains an external prompt source")
		}
	}
	return nil
}

func readPiPortableConfigFile(path string) ([]byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 != 0 {
		return nil, "", errors.New("not a non-executable regular file")
	}
	before, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil {
		return nil, "", err
	}
	file, err := safefs.OpenRead(path)
	if err != nil {
		return nil, "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, piPortableMaxConfigBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > piPortableMaxConfigBytes {
		return nil, "", errors.New("file cannot be read within bounds")
	}
	after, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil || before != after {
		return nil, "", errors.New("file changed during read")
	}
	return data, after, nil
}

func piPortablePathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func decodePiPortableExactJSON(data []byte, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func validPiPortableEnvironmentName(name string) bool {
	if name == "" || strings.ContainsRune(name, '=') {
		return false
	}
	for index := range len(name) {
		character := name[index]
		if index == 0 {
			if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
				return false
			}
			continue
		}
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func forbiddenPiPortableEnvironmentName(name string) bool {
	switch name {
	case "DISPLAY", "WAYLAND_DISPLAY",
		"HOME", "PATH", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "LC_ALL",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR",
		"LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "PI_CODING_AGENT_DIR":
		return true
	default:
		return false
	}
}

func piPortableConfigError(message string) error {
	return fmt.Errorf("%w: Pi portable %s", harnesses.ErrPortableRuntimeClosureIncomplete, message)
}

func piPortableConfigErrorAt(class string, index int, message string) error {
	return piPortableConfigError(fmt.Sprintf("%s at index %d %s", class, index, message))
}
