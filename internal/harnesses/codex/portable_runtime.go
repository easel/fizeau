package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
	"github.com/pelletier/go-toml/v2"
)

const (
	codexPortableEntrypointTarget = "harnesses/codex/bin/codex"
	codexPortableConfigRoot       = "config/codex"
	codexPortableDataRoot         = "data/codex"
	codexPortableConfigTarget     = codexPortableConfigRoot + "/config.toml"
	codexPortableAuthTarget       = codexPortableDataRoot + "/auth.json"
	codexPortableModelsTarget     = codexPortableDataRoot + "/models_cache.json"
	codexPortableCacheTarget      = codexPortableDataRoot + "/cache"
	codexPortableQuotaTarget      = "state/fizeau/codex-quota.json"
	codexPortableMaxMetadataBytes = 8 << 20
)

var _ harnesses.PortableRuntimeHarness = (*Runner)(nil)

type codexPortableStateDescriptor struct {
	path     string
	target   string
	kind     harnesses.PortableRuntimeAssetKind
	pathKind harnesses.PortableRuntimePathKind
	required bool
}

type codexNPMMetadata struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Bin                  map[string]string `json:"bin"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// PortableRuntimeAssets implements harnesses.PortableRuntimeHarness. It
// recognizes the standalone/Homebrew static ELF layout and the official npm
// shim plus matching platform-native package without executing either layout.
func (r *Runner) PortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	return r.portableRuntimeAssets(ctx, target, nil)
}

func (r *Runner) portableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget, afterConfigRead func()) (harnesses.PortableRuntimeContribution, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.PortableRuntimeContribution{}, codexPortableError("asset discovery canceled")
	}
	launcher := strings.TrimSpace(r.Binary)
	if launcher == "" {
		resolved, err := osexec.LookPath("codex")
		if err != nil {
			return harnesses.PortableRuntimeContribution{}, codexPortableError("launcher is unavailable")
		}
		launcher = resolved
	}
	entrypoint, err := resolveCodexPortableEntrypoint(launcher, target)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	home, err := codexPortableHome()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, codexPortableError("state root is unavailable")
	}
	authPath, err := codexAuthPath()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, codexPortableError("credential state is unavailable")
	}
	quotaPath, err := codexQuotaCachePath()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, codexPortableError("quota state is unavailable")
	}

	configPath := filepath.Join(home, "config.toml")
	environment, configCredential, configPresent, configDigest, err := codexPortableConfigEnvironment(configPath)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if afterConfigRead != nil {
		afterConfigRead()
	}
	for _, name := range []string{"CODEX_API_KEY", "OPENAI_API_KEY"} {
		if codexPortableEnvironmentDefined(name) {
			environment = append(environment, harnesses.PortableRuntimeEnvironment{Name: name})
		}
	}
	environment = normalizeCodexPortableEnvironment(environment)

	contribution, err := harnesses.AnalyzePortableRuntimeStaticClosure(ctx, target, harnesses.PortableRuntimeStaticClosureRequest{
		EntrypointSource: entrypoint,
		EntrypointTarget: codexPortableEntrypointTarget,
		RuntimeLookup:    harnesses.PortableRuntimeLookupClosed,
	})
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	states := []codexPortableStateDescriptor{
		{path: authPath, target: codexPortableAuthTarget, kind: harnesses.PortableRuntimeAssetCredential, pathKind: harnesses.PortableRuntimePathFile},
		{path: configPath, target: codexPortableConfigTarget, kind: harnesses.PortableRuntimeAssetConfig, pathKind: harnesses.PortableRuntimePathFile},
		{path: quotaPath, target: codexPortableQuotaTarget, kind: harnesses.PortableRuntimeAssetQuota, pathKind: harnesses.PortableRuntimePathFile},
		{path: filepath.Join(home, "models_cache.json"), target: codexPortableModelsTarget, kind: harnesses.PortableRuntimeAssetCache, pathKind: harnesses.PortableRuntimePathFile},
		{path: filepath.Join(home, "cache"), target: codexPortableCacheTarget, kind: harnesses.PortableRuntimeAssetCache, pathKind: harnesses.PortableRuntimePathTree},
	}
	authPresent := false
	for _, state := range states {
		present, digest, appendErr := appendCodexPortableState(ctx, &contribution, state)
		if appendErr != nil {
			return harnesses.PortableRuntimeContribution{}, appendErr
		}
		if state.target == codexPortableAuthTarget {
			authPresent = present
		}
		if state.target == codexPortableConfigTarget {
			if present != configPresent || present && digest != configDigest {
				return harnesses.PortableRuntimeContribution{}, codexPortableError("configuration changed during discovery")
			}
		}
	}
	if !authPresent && !configCredential && !codexPortableEnvironmentNonEmpty("CODEX_API_KEY") && !codexPortableEnvironmentNonEmpty("OPENAI_API_KEY") {
		return harnesses.PortableRuntimeContribution{}, codexPortableError("credential state is incomplete")
	}
	if present, presentErr := codexPortablePathExists(filepath.Join(home, ".credentials.json")); presentErr != nil || present {
		return harnesses.PortableRuntimeContribution{}, codexPortableError("credential layout is unsupported")
	}

	contribution.Environment = environment
	if err := projectCodexPortableState(&contribution); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	return harnesses.NormalizePortableRuntimeContribution(target, contribution)
}

func projectCodexPortableState(contribution *harnesses.PortableRuntimeContribution) error {
	if contribution == nil {
		return nil
	}
	projection := harnesses.PortableRuntimeStateProjection{
		Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathData, Target: "codex"},
	}
	hasConfig := false
	hasWritable := false
	for _, asset := range contribution.Assets {
		var relative string
		switch {
		case asset.Kind == harnesses.PortableRuntimeAssetConfig && strings.HasPrefix(asset.Target, codexPortableConfigRoot+"/"):
			relative = strings.TrimPrefix(asset.Target, codexPortableConfigRoot+"/")
			hasConfig = true
		case (asset.Kind == harnesses.PortableRuntimeAssetCredential || asset.Kind == harnesses.PortableRuntimeAssetCache) && strings.HasPrefix(asset.Target, codexPortableDataRoot+"/"):
			relative = strings.TrimPrefix(asset.Target, codexPortableDataRoot+"/")
			hasWritable = true
		default:
			continue
		}
		projection.Entries = append(projection.Entries, harnesses.PortableRuntimeStateProjectionEntry{AssetTarget: asset.Target, Target: relative})
	}

	guestPath := harnesses.PortableRuntimeGuestPath{}
	if hasConfig && hasWritable {
		contribution.StateProjections = append(contribution.StateProjections, projection)
		guestPath = projection.Directory
	} else if hasWritable {
		guestPath = harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathData, Target: "codex"}
	} else if hasConfig {
		return codexPortableError("configuration has no activation-owned state boundary")
	}
	if guestPath.Scope != "" {
		contribution.ExecutionConstraints.Environment = append(contribution.ExecutionConstraints.Environment,
			harnesses.PortableRuntimeEnvironmentConstraint{Name: "CODEX_HOME", Kind: harnesses.PortableRuntimeEnvironmentGuestPath, GuestPath: guestPath})
	}
	return nil
}

func resolveCodexPortableEntrypoint(launcher string, target harnesses.PortableRuntimeTarget) (string, error) {
	if launcher == "" || !filepath.IsAbs(launcher) || filepath.Clean(launcher) != launcher {
		return "", codexPortableError("launcher path is not absolute and normalized")
	}
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", codexPortableError("launcher cannot be resolved")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o100 == 0 {
		return "", codexPortableError("launcher is not an owner-executable regular file")
	}
	magic, err := readCodexPortablePrefix(resolved, 32)
	if err != nil {
		return "", err
	}
	if len(magic) >= 4 && string(magic[:4]) == "\x7fELF" {
		return filepath.Clean(resolved), nil
	}
	if !strings.HasPrefix(string(magic), "#!/usr/bin/env node\n") || filepath.Base(resolved) != "codex.js" || filepath.Base(filepath.Dir(resolved)) != "bin" {
		return "", codexPortableError("launcher layout is not recognized")
	}
	return resolveCodexPortableNPMEntrypoint(filepath.Dir(filepath.Dir(resolved)), target)
}

func resolveCodexPortableNPMEntrypoint(packageRoot string, target harnesses.PortableRuntimeTarget) (string, error) {
	alias, triple, err := codexPortableNPMPlatform(target)
	if err != nil {
		return "", err
	}
	metadata, err := readCodexPortableNPMMetadata(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return "", err
	}
	if metadata.Name != "@openai/codex" || metadata.Version == "" || metadata.Bin["codex"] != "bin/codex.js" {
		return "", codexPortableError("npm launcher metadata is unsupported")
	}
	wantDependency := "npm:@openai/codex@" + metadata.Version + "-" + strings.TrimPrefix(alias, "@openai/codex-")
	if metadata.OptionalDependencies[alias] != wantDependency {
		return "", codexPortableError("npm platform metadata is incompatible")
	}
	candidates := []string{
		filepath.Join(packageRoot, "node_modules", "@openai", strings.TrimPrefix(alias, "@openai/")),
		filepath.Join(filepath.Dir(packageRoot), strings.TrimPrefix(alias, "@openai/")),
	}
	for _, candidate := range candidates {
		present, presentErr := codexPortablePathExists(candidate)
		if presentErr != nil {
			return "", codexPortableError("npm platform package cannot be inspected")
		}
		if !present {
			continue
		}
		return validateCodexPortableNPMPlatformPackage(candidate, metadata.Version, alias, triple)
	}
	return "", codexPortableError("npm platform package is missing")
}

func validateCodexPortableNPMPlatformPackage(root, baseVersion, alias, triple string) (string, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(resolvedRoot) {
		return "", codexPortableError("npm platform package cannot be resolved")
	}
	info, err := os.Lstat(resolvedRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", codexPortableError("npm platform package is not a directory")
	}
	metadata, err := readCodexPortableNPMMetadata(filepath.Join(resolvedRoot, "package.json"))
	if err != nil {
		return "", err
	}
	platformSuffix := strings.TrimPrefix(alias, "@openai/codex-")
	validIdentity := metadata.Name == "@openai/codex" && metadata.Version == baseVersion+"-"+platformSuffix
	validLegacyIdentity := metadata.Name == alias && metadata.Version == baseVersion
	if !validIdentity && !validLegacyIdentity {
		return "", codexPortableError("npm platform package metadata is incompatible")
	}
	payload := filepath.Join(resolvedRoot, "vendor", triple, "codex", "codex")
	resolvedPayload, err := filepath.EvalSymlinks(payload)
	if err != nil || !filepath.IsAbs(resolvedPayload) {
		return "", codexPortableError("npm native payload is missing")
	}
	resolvedPayload = filepath.Clean(resolvedPayload)
	if resolvedPayload == resolvedRoot || !strings.HasPrefix(resolvedPayload, resolvedRoot+string(filepath.Separator)) {
		return "", codexPortableError("npm native payload escapes its package")
	}
	return resolvedPayload, nil
}

func codexPortableNPMPlatform(target harnesses.PortableRuntimeTarget) (string, string, error) {
	if target.GOOS != "linux" {
		return "", "", fmt.Errorf("%w: codex npm layout requires linux", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
	switch target.GOARCH {
	case "amd64":
		return "@openai/codex-linux-x64", "x86_64-unknown-linux-musl", nil
	case "arm64":
		return "@openai/codex-linux-arm64", "aarch64-unknown-linux-musl", nil
	default:
		return "", "", fmt.Errorf("%w: codex npm layout does not support target architecture", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
}

func readCodexPortableNPMMetadata(path string) (codexNPMMetadata, error) {
	data, err := readCodexPortableRegularFile(path, codexPortableMaxMetadataBytes)
	if err != nil {
		return codexNPMMetadata{}, codexPortableError("npm metadata cannot be read")
	}
	var metadata codexNPMMetadata
	if json.Unmarshal(data, &metadata) != nil {
		return codexNPMMetadata{}, codexPortableError("npm metadata is invalid")
	}
	return metadata, nil
}

func codexPortableHome() (string, error) {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		if !filepath.IsAbs(home) || filepath.Clean(home) != home {
			return "", errors.New("invalid codex home")
		}
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", errors.New("codex home unavailable")
	}
	return filepath.Join(home, ".codex"), nil
}

func appendCodexPortableState(ctx context.Context, contribution *harnesses.PortableRuntimeContribution, state codexPortableStateDescriptor) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", codexPortableError("state discovery canceled")
	}
	info, err := os.Lstat(state.path)
	if errors.Is(err, os.ErrNotExist) {
		if state.required {
			return false, "", codexPortableError("required state is missing")
		}
		return false, "", nil
	}
	if err != nil {
		return false, "", codexPortableError("state cannot be inspected")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, "", codexPortableError("state contains an unsupported symlink root")
	}
	asset := harnesses.PortableRuntimeAsset{Kind: state.kind, PathKind: state.pathKind, Source: state.path, Target: state.target}
	switch state.pathKind {
	case harnesses.PortableRuntimePathFile:
		if !info.Mode().IsRegular() {
			return false, "", codexPortableError("state file has an unsupported type")
		}
		asset.ContentSHA256, err = harnesses.PortableRuntimeFileDigest(state.path)
	case harnesses.PortableRuntimePathTree:
		if !info.IsDir() {
			return false, "", codexPortableError("state tree has an unsupported type")
		}
		asset.ContentSHA256, err = harnesses.PortableRuntimeTreeDigest(state.path)
	default:
		return false, "", codexPortableError("state has an unsupported path kind")
	}
	if err != nil {
		return false, "", codexPortableError("state cannot be content-addressed")
	}
	contribution.Assets = append(contribution.Assets, asset)
	return true, asset.ContentSHA256, nil
}

func codexPortableConfigEnvironment(path string) ([]harnesses.PortableRuntimeEnvironment, bool, bool, string, error) {
	present, err := codexPortablePathExists(path)
	if err != nil {
		return nil, false, false, "", codexPortableError("configuration cannot be inspected")
	}
	if !present {
		return nil, false, false, "", nil
	}
	data, digest, err := readCodexPortableRegularFileWithDigest(path, codexPortableMaxMetadataBytes)
	if err != nil {
		return nil, false, true, "", codexPortableError("configuration cannot be read")
	}
	var document map[string]any
	if toml.Unmarshal(data, &document) != nil {
		return nil, false, true, "", codexPortableError("configuration is invalid")
	}
	if err := validateCodexPortableConfigClosure(document); err != nil {
		return nil, false, true, "", err
	}

	names := make([]harnesses.PortableRuntimeEnvironment, 0)
	credentialPresent := false
	providers, err := codexPortableOptionalTable(document, "model_providers")
	if err != nil {
		return nil, false, true, "", err
	}
	for _, raw := range providers {
		provider, ok := raw.(map[string]any)
		if !ok {
			return nil, false, true, "", codexPortableError("configuration contains an invalid model provider")
		}
		if rawName, exists := provider["env_key"]; exists {
			name, ok := rawName.(string)
			if !ok || name == "" {
				return nil, false, true, "", codexPortableError("configuration contains an invalid provider environment reference")
			}
			included, includeErr := appendCodexPortableEnvironment(&names, name, false)
			if includeErr != nil {
				return nil, false, true, "", includeErr
			}
			credentialPresent = credentialPresent || included && codexPortableEnvironmentNonEmpty(name)
		}
		if err := appendCodexPortableEnvironmentMap(&names, provider["env_http_headers"]); err != nil {
			return nil, false, true, "", err
		}
	}
	mcpServers, err := codexPortableOptionalTable(document, "mcp_servers")
	if err != nil {
		return nil, false, true, "", err
	}
	for _, raw := range mcpServers {
		server, ok := raw.(map[string]any)
		if !ok {
			return nil, false, true, "", codexPortableError("configuration contains an invalid MCP server")
		}
		if rawName, exists := server["bearer_token_env_var"]; exists {
			name, ok := rawName.(string)
			if !ok || name == "" {
				return nil, false, true, "", codexPortableError("configuration contains an invalid MCP environment reference")
			}
			if _, err := appendCodexPortableEnvironment(&names, name, false); err != nil {
				return nil, false, true, "", err
			}
		}
		if err := appendCodexPortableEnvironmentMap(&names, server["env_http_headers"]); err != nil {
			return nil, false, true, "", err
		}
		if err := appendCodexPortableMCPEnvironment(&names, server["env_vars"]); err != nil {
			return nil, false, true, "", err
		}
	}
	return normalizeCodexPortableEnvironment(names), credentialPresent, true, digest, nil
}

func validateCodexPortableConfigClosure(document map[string]any) error {
	if _, exists := document["shell_environment_policy"]; exists {
		return codexPortableError("configuration has an unsupported shell environment policy")
	}
	for _, key := range []string{
		"debug", "experimental_compact_prompt_file", "hooks", "log_dir",
		"marketplaces", "model_catalog_json", "model_instructions_file",
		"permissions", "plugins", "sandbox_workspace_write",
	} {
		if codexPortableConfigValuePresent(document[key]) {
			return codexPortableError("configuration references unsupported external state")
		}
	}
	if codexPortableConfigContainsKey(document["skills"], "path") || codexPortableConfigHasAppsMCPPath(document) {
		return codexPortableError("configuration references unsupported external state")
	}
	if codexPortableConfigValuePresent(document["notify"]) {
		return codexPortableError("configuration has an unsupported notification command")
	}
	for _, key := range []string{"agents", "agent_roles"} {
		if roles, _ := document[key].(map[string]any); roles != nil {
			for _, raw := range roles {
				role, _ := raw.(map[string]any)
				if codexPortableConfigValuePresent(role["config_file"]) {
					return codexPortableError("configuration references an external agent-role file")
				}
			}
		}
	}
	if profiles, _ := document["profiles"].(map[string]any); profiles != nil {
		for _, raw := range profiles {
			profile, _ := raw.(map[string]any)
			for _, key := range []string{"experimental_compact_prompt_file", "model_catalog_json", "model_instructions_file"} {
				if codexPortableConfigValuePresent(profile[key]) {
					return codexPortableError("configuration profile references an external file")
				}
			}
			if codexPortableConfigHasAppsMCPPath(profile) {
				return codexPortableError("configuration profile references an external MCP path")
			}
		}
	}
	if servers, _ := document["mcp_servers"].(map[string]any); servers != nil {
		for _, raw := range servers {
			server, _ := raw.(map[string]any)
			if command, _ := server["command"].(string); command != "" {
				return codexPortableError("configuration references an external MCP command")
			}
			if cwd, _ := server["cwd"].(string); cwd != "" {
				return codexPortableError("configuration references an external MCP working directory")
			}
		}
	}
	if providers, _ := document["model_providers"].(map[string]any); providers != nil {
		for _, raw := range providers {
			provider, _ := raw.(map[string]any)
			if auth, exists := provider["auth"]; exists && auth != nil {
				return codexPortableError("configuration references an external provider credential command")
			}
			if aws, exists := provider["aws"]; exists && aws != nil {
				return codexPortableError("configuration references an unsupported AWS credential chain")
			}
		}
	}
	if mode, _ := document["cli_auth_credentials_store"].(string); mode == "keyring" || mode == "ephemeral" {
		return codexPortableError("configuration references an unsupported credential store")
	}
	if codexPortableConfigContainsKey(document["otel"], "ca-certificate", "client-certificate", "client-private-key") {
		return codexPortableError("configuration references an external telemetry credential file")
	}
	return nil
}

func appendCodexPortableEnvironmentMap(environment *[]harnesses.PortableRuntimeEnvironment, raw any) error {
	if raw == nil {
		return nil
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		return codexPortableError("configuration contains an invalid environment map")
	}
	for _, value := range mapping {
		name, ok := value.(string)
		if !ok {
			return codexPortableError("configuration contains an invalid environment reference")
		}
		if _, err := appendCodexPortableEnvironment(environment, name, false); err != nil {
			return err
		}
	}
	return nil
}

func appendCodexPortableMCPEnvironment(environment *[]harnesses.PortableRuntimeEnvironment, raw any) error {
	if raw == nil {
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		return codexPortableError("configuration contains an invalid MCP environment list")
	}
	for _, value := range values {
		switch entry := value.(type) {
		case string:
			if _, err := appendCodexPortableEnvironment(environment, entry, false); err != nil {
				return err
			}
		case map[string]any:
			name, ok := entry["name"].(string)
			if !ok || !validCodexPortableEnvironmentName(name) {
				return codexPortableError("configuration contains an invalid MCP environment reference")
			}
			for key := range entry {
				if key != "name" && key != "source" {
					return codexPortableError("configuration contains an invalid MCP environment reference")
				}
			}
			source := name
			if rawSource, exists := entry["source"]; exists {
				source, ok = rawSource.(string)
				if !ok || source == "" {
					return codexPortableError("configuration contains an invalid MCP environment reference")
				}
			}
			if _, err := appendCodexPortableEnvironment(environment, source, false); err != nil {
				return err
			}
		default:
			return codexPortableError("configuration contains an invalid MCP environment reference")
		}
	}
	return nil
}

func appendCodexPortableEnvironment(environment *[]harnesses.PortableRuntimeEnvironment, name string, required bool) (bool, error) {
	if !validCodexPortableEnvironmentName(name) || forbiddenCodexPortableEnvironmentName(name) {
		return false, codexPortableError("configuration contains an unsupported environment name")
	}
	if !codexPortableEnvironmentDefined(name) {
		if required {
			return false, codexPortableError("configuration requires an unavailable environment name")
		}
		return false, nil
	}
	*environment = append(*environment, harnesses.PortableRuntimeEnvironment{Name: name})
	return true, nil
}

func codexPortableEnvironmentDefined(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func codexPortableEnvironmentNonEmpty(name string) bool {
	value, ok := os.LookupEnv(name)
	return ok && value != ""
}

func forbiddenCodexPortableEnvironmentName(name string) bool {
	if name == "HOME" || name == "PATH" || name == "CODEX_HOME" || name == "CODEX_MANAGED_PACKAGE_ROOT" || strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "FIZEAU_") {
		return true
	}
	return false
}

func validCodexPortableEnvironmentName(name string) bool {
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

func normalizeCodexPortableEnvironment(environment []harnesses.PortableRuntimeEnvironment) []harnesses.PortableRuntimeEnvironment {
	names := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		names[entry.Name] = struct{}{}
	}
	normalized := make([]harnesses.PortableRuntimeEnvironment, 0, len(names))
	for name := range names {
		normalized = append(normalized, harnesses.PortableRuntimeEnvironment{Name: name})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Name < normalized[j].Name })
	return normalized
}

func codexPortableOptionalTable(document map[string]any, key string) (map[string]any, error) {
	raw, exists := document[key]
	if !exists {
		return nil, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return nil, codexPortableError("configuration contains an invalid table")
	}
	return table, nil
}

func codexPortableConfigValuePresent(raw any) bool {
	switch value := raw.(type) {
	case nil:
		return false
	case string:
		return value != ""
	case []any:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	default:
		return true
	}
}

func codexPortableConfigContainsKey(raw any, keys ...string) bool {
	switch value := raw.(type) {
	case map[string]any:
		for key, child := range value {
			for _, wanted := range keys {
				if key == wanted {
					return true
				}
			}
			if codexPortableConfigContainsKey(child, keys...) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if codexPortableConfigContainsKey(child, keys...) {
				return true
			}
		}
	}
	return false
}

func codexPortableConfigHasAppsMCPPath(document map[string]any) bool {
	features, _ := document["features"].(map[string]any)
	override, _ := features["apps_mcp_path_override"].(map[string]any)
	return codexPortableConfigValuePresent(override["path"])
}

func codexPortablePathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func readCodexPortablePrefix(path string, limit int64) ([]byte, error) {
	file, err := safefs.OpenRead(path)
	if err != nil {
		return nil, codexPortableError("launcher cannot be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, codexPortableError("launcher cannot be read")
	}
	return data, nil
}

func readCodexPortableRegularFile(path string, limit int64) ([]byte, error) {
	data, _, err := readCodexPortableRegularFileWithDigest(path, limit)
	return data, err
}

func readCodexPortableRegularFileWithDigest(path string, limit int64) ([]byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("not a regular file")
	}
	before, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil {
		return nil, "", err
	}
	file, err := safefs.OpenRead(path)
	if err != nil {
		return nil, "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > limit {
		return nil, "", errors.New("file cannot be read within bounds")
	}
	after, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil || before != after {
		return nil, "", errors.New("file changed during read")
	}
	return data, after, nil
}

func codexPortableError(message string) error {
	return fmt.Errorf("%w: codex %s", harnesses.ErrPortableRuntimeClosureIncomplete, message)
}
