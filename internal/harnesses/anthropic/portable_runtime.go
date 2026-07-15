package anthropic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
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
)

const (
	claudePortableEntrypointTarget = "harnesses/anthropic/bin/claude"
	claudePortableConfigTarget     = "home/.claude/settings.json"
	claudePortableLocalTarget      = "home/.claude/settings.local.json"
	claudePortableCredentialTarget = "home/.claude/.credentials.json"
	claudePortableCacheTarget      = "home/.claude/cache"
	claudePortableStatsTarget      = "home/.claude/stats-cache.json"
	claudePortableQuotaTarget      = "state/fizeau/claude-quota.json"
	claudePortableMaxJSONBytes     = 8 << 20
	claudePortableIdentityMarker   = "@anthropic-ai/claude-code"
)

var claudePortableConfigTrees = []string{"agent-memory", "agents", "commands", "config", "output-styles", "rules", "skills", "themes"}

// claudePortableVerifiedReleases is deliberately fail-closed. Each digest is
// copied from Anthropic's immutable release manifest after the corresponding
// native binary has passed the contributor-owned isolated loader probe. A new
// Claude Code release is unsupported until its exact digest is reviewed and
// added; a version-shaped path and an embedded product string are not enough
// evidence for verified-exact runtime lookup.
//
// Manifest source:
// https://downloads.claude.ai/claude-code-releases/<version>/manifest.json
type claudePortableVerifiedRelease struct {
	digest              string
	size                int64
	commit              string
	manifestDigest      string
	signingFingerprint  string
	offlineProbeProfile uint16
}

var claudePortableVerifiedReleases = map[string]map[string]claudePortableVerifiedRelease{
	"2.1.210": {
		"arm64": {
			digest: "84feb193c1d91f3b5eba836ed47c0e4dee953195abba950917c3e101eff174e8", size: 257932016,
			commit: "88e9fbf39bf4efa5bca44549b7fd9461628657e6", manifestDigest: "654eb446e70eaed758a1f1230986d1e87ca9e8ad947b5de6f9b4db8496101ece",
			signingFingerprint: "31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE", offlineProbeProfile: 1,
		},
	},
}

// ClaudePortableRuntimeOptions supplies the actual adapter's launcher and
// environment contract. Production discovery derives all release and library
// evidence internally; callers cannot override either trust boundary.
type ClaudePortableRuntimeOptions struct {
	Launcher                   string
	Arguments                  []string
	EnvironmentNames           []string
	EnvironmentPrefixes        []string
	InheritsProcessEnvironment bool
}

type claudePortableState struct {
	path     string
	target   string
	kind     harnesses.PortableRuntimeAssetKind
	pathKind harnesses.PortableRuntimePathKind
}

// ClaudePortableRuntimeAssets discovers the shared native Claude Code binary
// and account state without starting Claude, a package manager, or a provider.
func ClaudePortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget, options ClaudePortableRuntimeOptions) (harnesses.PortableRuntimeContribution, error) {
	if ctx == nil {
		return harnesses.PortableRuntimeContribution{}, claudePortableError("asset discovery has a nil context")
	}
	if err := ctx.Err(); err != nil {
		return harnesses.PortableRuntimeContribution{}, claudePortableContextError("asset discovery canceled", err)
	}
	if err := harnesses.ValidatePortableRuntimeTarget(target); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if err := validateClaudePortableArguments(options.Arguments); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	launcher, version, interpreter, launcherDigest, launcherSize, err := claudePortableLauncher(options.Launcher)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if !claudePortableReleaseVerified(target.GOARCH, version, launcherDigest, launcherSize) {
		return harnesses.PortableRuntimeContribution{}, claudePortableError("launcher release lacks verified closure evidence")
	}
	contribution, err := claudePortableExecutableClosure(ctx, target, launcher, interpreter, nil)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if !claudePortableEntrypointIdentityMatches(contribution, launcherDigest) {
		return harnesses.PortableRuntimeContribution{}, claudePortableError("launcher identity changed during discovery")
	}

	configRoot, home, err := claudePortableStateRoots()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, claudePortableError("state root is unavailable")
	}
	settingsPath := filepath.Join(configRoot, "settings.json")
	localSettingsPath := filepath.Join(configRoot, "settings.local.json")
	credentialPath := filepath.Join(configRoot, ".credentials.json")
	appStatePath := filepath.Join(home, ".claude.json")
	remoteSettingsPath := filepath.Join(configRoot, "remote-settings.json")
	policyLimitsPath := filepath.Join(configRoot, "policy-limits.json")

	parsedDigests := make(map[string]string)
	validatedOnlyDigests := make(map[string]string)
	settingsCredential := false
	credentialPresent, credentialDigest, err := claudePortableCredential(credentialPath)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if credentialPresent {
		parsedDigests[credentialPath] = credentialDigest
	}
	for _, config := range []struct {
		path     string
		validate func(map[string]any) error
	}{
		{settingsPath, validateClaudePortableSettings},
		{localSettingsPath, validateClaudePortableSettings},
		{appStatePath, validateClaudePortableAppState},
		{remoteSettingsPath, validateClaudePortableSettings},
		{policyLimitsPath, nil},
	} {
		present, digest, document, readErr := readClaudePortableJSON(config.path)
		if readErr != nil {
			return harnesses.PortableRuntimeContribution{}, readErr
		}
		if !present {
			continue
		}
		if config.validate != nil {
			if err := config.validate(document); err != nil {
				return harnesses.PortableRuntimeContribution{}, err
			}
		}
		if config.path == settingsPath || config.path == localSettingsPath {
			settingsCredential = settingsCredential || claudePortableSettingsCredential(document)
		}
		if config.path == appStatePath {
			validatedOnlyDigests[config.path] = digest
		} else {
			parsedDigests[config.path] = digest
		}
	}

	environment, environmentCredential, err := claudePortableEnvironment(options.EnvironmentNames, options.EnvironmentPrefixes)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if !credentialPresent && !settingsCredential && !environmentCredential {
		return harnesses.PortableRuntimeContribution{}, claudePortableError("credential state is incomplete")
	}
	if options.InheritsProcessEnvironment {
		if err := rejectClaudePortableExternalEnvironment(); err != nil {
			return harnesses.PortableRuntimeContribution{}, err
		}
	}

	quotaPath, err := ClaudeQuotaCachePath()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, claudePortableError("quota state is unavailable")
	}
	states := []claudePortableState{
		{credentialPath, claudePortableCredentialTarget, harnesses.PortableRuntimeAssetCredential, harnesses.PortableRuntimePathFile},
		{settingsPath, claudePortableConfigTarget, harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathFile},
		{localSettingsPath, claudePortableLocalTarget, harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathFile},
		{remoteSettingsPath, "home/.claude/remote-settings.json", harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathFile},
		{policyLimitsPath, "home/.claude/policy-limits.json", harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathFile},
		{filepath.Join(configRoot, "keybindings.json"), "home/.claude/keybindings.json", harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathFile},
		{filepath.Join(configRoot, "CLAUDE.md"), "home/.claude/CLAUDE.md", harnesses.PortableRuntimeAssetConfig, harnesses.PortableRuntimePathFile},
		{filepath.Join(configRoot, "cache"), claudePortableCacheTarget, harnesses.PortableRuntimeAssetCache, harnesses.PortableRuntimePathTree},
		{filepath.Join(configRoot, "stats-cache.json"), claudePortableStatsTarget, harnesses.PortableRuntimeAssetCache, harnesses.PortableRuntimePathFile},
		{quotaPath, claudePortableQuotaTarget, harnesses.PortableRuntimeAssetQuota, harnesses.PortableRuntimePathFile},
	}
	for _, name := range claudePortableConfigTrees {
		states = append(states, claudePortableState{
			path: filepath.Join(configRoot, name), target: "home/.claude/" + name,
			kind: harnesses.PortableRuntimeAssetConfig, pathKind: harnesses.PortableRuntimePathTree,
		})
	}
	if err := rejectClaudePortableUnsupportedState(configRoot); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	for _, state := range states {
		present, digest, appendErr := appendClaudePortableState(ctx, &contribution, state)
		if appendErr != nil {
			return harnesses.PortableRuntimeContribution{}, appendErr
		}
		if parsed, exists := parsedDigests[state.path]; exists && (!present || digest != parsed) {
			return harnesses.PortableRuntimeContribution{}, claudePortableError("state changed during discovery")
		}
	}
	for path, digest := range validatedOnlyDigests {
		present, stableDigest, _, readErr := readClaudePortableJSON(path)
		if readErr != nil || !present || stableDigest != digest {
			return harnesses.PortableRuntimeContribution{}, claudePortableError("validated application state changed during discovery")
		}
	}
	if err := rejectClaudePortableUnsupportedState(configRoot); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	contribution.Environment = environment
	return harnesses.NormalizePortableRuntimeContribution(target, contribution)
}

func validateClaudePortableArguments(arguments []string) error {
	if arguments == nil {
		return nil
	}
	flagValues := map[string]func(string) bool{
		"--output-format":   func(value string) bool { return value == "json" || value == "stream-json" || value == "text" },
		"--input-format":    func(value string) bool { return value == "stream-json" || value == "text" },
		"--model":           func(value string) bool { return value != "" },
		"--effort":          func(value string) bool { return value != "" },
		"--permission-mode": func(value string) bool { return value != "" },
		"--fallback-model":  func(value string) bool { return value != "" },
		"--max-turns":       func(value string) bool { return value != "" },
		"--max-budget-usd":  func(value string) bool { return value != "" },
	}
	standalone := map[string]bool{
		"--print": true, "-p": true, "--verbose": true,
		"--no-session-persistence": true, "--disable-slash-commands": true,
	}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if standalone[argument] {
			continue
		}
		validate, ok := flagValues[argument]
		if !ok || index+1 >= len(arguments) || !validate(arguments[index+1]) {
			return claudePortableError("configured arguments contain an unsupported launcher control")
		}
		index++
	}
	return nil
}

func claudePortableLauncher(configured string) (string, string, string, string, int64, error) {
	launcher := strings.TrimSpace(configured)
	if launcher == "" {
		resolved, err := osexec.LookPath("claude")
		if err != nil {
			return "", "", "", "", 0, claudePortableError("launcher is unavailable")
		}
		launcher = resolved
	}
	if !filepath.IsAbs(launcher) || filepath.Clean(launcher) != launcher {
		return "", "", "", "", 0, claudePortableError("launcher path is not absolute and normalized")
	}
	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", "", "", "", 0, claudePortableError("launcher cannot be resolved")
	}
	home, homeErr := os.UserHomeDir()
	version, recognizedInstall := claudePortableRecognizedNativeInstall(resolved, home)
	if homeErr != nil || !recognizedInstall {
		return "", "", "", "", 0, claudePortableError("launcher install layout is not recognized")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o100 == 0 {
		return "", "", "", "", 0, claudePortableError("launcher is not an owner-executable regular file")
	}
	file, err := safefs.OpenRead(resolved)
	if err != nil {
		return "", "", "", "", 0, claudePortableError("launcher cannot be inspected")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return "", "", "", "", 0, claudePortableError("launcher changed before inspection")
	}
	digest, recognized, err := claudePortableLauncherIdentity(file)
	if err != nil || !recognized {
		return "", "", "", "", 0, claudePortableError("launcher is not a recognized Claude Code binary")
	}
	image, err := elf.NewFile(file)
	if err != nil {
		return "", "", "", "", 0, claudePortableError("launcher is not a recognized native ELF")
	}
	defer image.Close()
	interpreter, err := claudePortableELFInterpreter(image)
	if err != nil || interpreter == "" {
		return "", "", "", "", 0, claudePortableError("launcher is not a recognized dynamic ELF")
	}
	return filepath.Clean(resolved), version, interpreter, digest, openedInfo.Size(), nil
}

func claudePortableRecognizedNativeInstall(resolved, home string) (string, bool) {
	version := filepath.Base(resolved)
	versionsRoot := filepath.Dir(resolved)
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return version, false
	}
	expectedRoot := filepath.Join(home, ".local", "share", "claude", "versions")
	return version, versionsRoot == expectedRoot &&
		validClaudePortableVersion(version)
}

func claudePortableReleaseVerified(goarch, version, digest string, size int64) bool {
	architectures, ok := claudePortableVerifiedReleases[version]
	evidence, exists := architectures[goarch]
	return ok && exists && evidence.digest == digest && evidence.size == size && evidence.offlineProbeProfile != 0
}

func validClaudePortableVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func claudePortableLauncherIdentity(file *os.File) (string, bool, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", false, err
	}
	hash := sha256.New()
	marker := []byte(claudePortableIdentityMarker)
	buffer := make([]byte, 128<<10)
	tail := make([]byte, 0, len(marker)-1)
	recognized := false
	for {
		count, readErr := file.Read(buffer)
		if count != 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", false, err
			}
			window := append(tail, buffer[:count]...)
			if bytes.Contains(window, marker) {
				recognized = true
			}
			keep := min(len(window), len(marker)-1)
			tail = append(tail[:0], window[len(window)-keep:]...)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", false, readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), recognized, nil
}

func claudePortableEntrypointIdentityMatches(contribution harnesses.PortableRuntimeContribution, digest string) bool {
	for _, asset := range contribution.Assets {
		if asset.Target == claudePortableEntrypointTarget {
			return asset.Kind == harnesses.PortableRuntimeAssetExecutable && asset.ContentSHA256 == digest
		}
	}
	return false
}

func claudePortableELFInterpreter(image *elf.File) (string, error) {
	var interpreter string
	for _, program := range image.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		if interpreter != "" {
			return "", errors.New("multiple ELF interpreters")
		}
		contents := make([]byte, program.Filesz)
		if program.Filesz < 2 || program.Filesz > 4096 {
			return "", errors.New("invalid ELF interpreter")
		}
		if _, err := program.ReadAt(contents, 0); err != nil || contents[len(contents)-1] != 0 {
			return "", errors.New("invalid ELF interpreter")
		}
		interpreter = string(contents[:len(contents)-1])
	}
	if !filepath.IsAbs(interpreter) || filepath.Clean(interpreter) != interpreter {
		return "", errors.New("invalid ELF interpreter")
	}
	return interpreter, nil
}

func claudePortableExecutableClosure(ctx context.Context, target harnesses.PortableRuntimeTarget, launcher, interpreter string, override []harnesses.PortableRuntimeLibrarySearchRoot) (harnesses.PortableRuntimeContribution, error) {
	roots := append([]harnesses.PortableRuntimeLibrarySearchRoot(nil), override...)
	if len(roots) != 0 {
		return harnesses.AnalyzePortableRuntimeDynamicClosure(ctx, target, claudePortableClosureRequest(launcher, interpreter, roots))
	}
	candidates := claudePortableHostLibraryRoots(target, interpreter)
	for size := 1; size <= len(candidates); size++ {
		if err := ctx.Err(); err != nil {
			return harnesses.PortableRuntimeContribution{}, claudePortableContextError("dynamic closure discovery canceled", err)
		}
		var contribution harnesses.PortableRuntimeContribution
		var found bool
		claudePortableRootCombinations(candidates, size, func(candidate []harnesses.PortableRuntimeLibrarySearchRoot) bool {
			if ctx.Err() != nil {
				return true
			}
			analyzed, err := harnesses.AnalyzePortableRuntimeDynamicClosure(ctx, target, claudePortableClosureRequest(launcher, interpreter, candidate))
			if err != nil {
				return false
			}
			contribution = analyzed
			found = true
			return true
		})
		if err := ctx.Err(); err != nil {
			return harnesses.PortableRuntimeContribution{}, claudePortableContextError("dynamic closure discovery canceled", err)
		}
		if found {
			return contribution, nil
		}
	}
	return harnesses.PortableRuntimeContribution{}, claudePortableError("dynamic loader or library closure is incomplete")
}

func claudePortableClosureRequest(launcher, interpreter string, roots []harnesses.PortableRuntimeLibrarySearchRoot) harnesses.PortableRuntimeDynamicClosureRequest {
	return harnesses.PortableRuntimeDynamicClosureRequest{
		EntrypointSource:  launcher,
		EntrypointTarget:  claudePortableEntrypointTarget,
		LoaderTarget:      "harnesses/anthropic/loader/" + filepath.Base(interpreter),
		ExactLibraryRoots: roots,
		RuntimeLookup:     harnesses.PortableRuntimeLookupVerifiedExact,
	}
}

func claudePortableHostLibraryRoots(target harnesses.PortableRuntimeTarget, interpreter string) []harnesses.PortableRuntimeLibrarySearchRoot {
	directories := []string{filepath.Dir(interpreter)}
	for _, triple := range claudePortableLibraryTriples(target.GOARCH) {
		directories = append(directories, filepath.Join("/lib", triple), filepath.Join("/usr/lib", triple))
	}
	directories = append(directories, "/lib64", "/usr/lib64", "/lib", "/usr/lib")
	seen := make(map[string]struct{})
	result := make([]harnesses.PortableRuntimeLibrarySearchRoot, 0, len(directories))
	for _, directory := range directories {
		resolved, err := filepath.EvalSymlinks(directory)
		if err != nil || !filepath.IsAbs(resolved) {
			continue
		}
		resolved = filepath.Clean(resolved)
		info, err := os.Lstat(resolved)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if _, exists := seen[resolved]; exists {
			continue
		}
		seen[resolved] = struct{}{}
		result = append(result, harnesses.PortableRuntimeLibrarySearchRoot{
			Source: resolved,
			Target: fmt.Sprintf("harnesses/anthropic/lib/%d", len(result)),
		})
	}
	return result
}

func claudePortableLibraryTriples(goarch string) []string {
	switch goarch {
	case "amd64":
		return []string{"x86_64-linux-gnu"}
	case "arm64":
		return []string{"aarch64-linux-gnu"}
	case "386":
		return []string{"i386-linux-gnu"}
	case "arm":
		return []string{"arm-linux-gnueabihf", "arm-linux-gnueabi"}
	case "ppc64le":
		return []string{"powerpc64le-linux-gnu"}
	case "ppc64":
		return []string{"powerpc64-linux-gnu"}
	case "riscv64":
		return []string{"riscv64-linux-gnu"}
	case "s390x":
		return []string{"s390x-linux-gnu"}
	default:
		return nil
	}
}

func claudePortableRootCombinations(candidates []harnesses.PortableRuntimeLibrarySearchRoot, size int, visit func([]harnesses.PortableRuntimeLibrarySearchRoot) bool) bool {
	combination := make([]harnesses.PortableRuntimeLibrarySearchRoot, 0, size)
	var choose func(int) bool
	choose = func(start int) bool {
		if len(combination) == size {
			return visit(append([]harnesses.PortableRuntimeLibrarySearchRoot(nil), combination...))
		}
		remaining := size - len(combination)
		for index := start; index <= len(candidates)-remaining; index++ {
			combination = append(combination, candidates[index])
			if choose(index + 1) {
				return true
			}
			combination = combination[:len(combination)-1]
		}
		return false
	}
	return choose(0)
}

func claudePortableStateRoots() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", "", errors.New("home unavailable")
	}
	if err := validateClaudePortableDirectoryRoot(home, true); err != nil {
		return "", "", errors.New("home unavailable")
	}
	configRoot := filepath.Join(home, ".claude")
	if configured, exists := os.LookupEnv("CLAUDE_CONFIG_DIR"); exists {
		if configured == "" || !filepath.IsAbs(configured) || filepath.Clean(configured) != configured {
			return "", "", errors.New("invalid config root")
		}
		configRoot = configured
	}
	if err := validateClaudePortableDirectoryRoot(configRoot, false); err != nil {
		return "", "", errors.New("invalid config root")
	}
	return configRoot, home, nil
}

func validateClaudePortableDirectoryRoot(root string, required bool) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("directory root is unavailable")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || filepath.Clean(resolved) != root {
		return errors.New("directory root is not canonical")
	}
	return nil
}

func claudePortableCredential(path string) (bool, string, error) {
	present, digest, document, err := readClaudePortableJSON(path)
	if err != nil || !present {
		return present, digest, err
	}
	oauth, ok := document["claudeAiOauth"].(map[string]any)
	if !ok || !claudePortableNonEmptyString(oauth["accessToken"]) && !claudePortableNonEmptyString(oauth["refreshToken"]) {
		return true, "", claudePortableError("credential state is invalid")
	}
	return true, digest, nil
}

func readClaudePortableJSON(path string) (bool, string, map[string]any, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, "", nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > claudePortableMaxJSONBytes {
		return false, "", nil, claudePortableError("JSON state cannot be inspected")
	}
	data, err := safefs.ReadFile(path)
	if err != nil || len(data) > claudePortableMaxJSONBytes {
		return false, "", nil, claudePortableError("JSON state cannot be read")
	}
	var document map[string]any
	if json.Unmarshal(data, &document) != nil || document == nil {
		return false, "", nil, claudePortableError("JSON state is invalid")
	}
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	stableDigest, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil || stableDigest != digest {
		return false, "", nil, claudePortableError("JSON state changed during discovery")
	}
	return true, digest, document, nil
}

func validateClaudePortableSettings(document map[string]any) error {
	for _, key := range []string{"apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "fileSuggestion", "forceRemoteSettingsRefresh", "hooks", "mcpServers", "plugins", "pluginConfigs", "policyHelper", "sandbox", "statusLine", "subagentStatusLine", "otelHeadersHelper"} {
		if claudePortableValuePresent(document[key]) {
			return claudePortableError("settings reference unsupported external execution")
		}
	}
	if claudePortableValuePresent(document["extraKnownMarketplaces"]) {
		return claudePortableError("settings reference unsupported marketplaces")
	}
	if enabled, ok := document["enabledPlugins"].(map[string]any); ok {
		for _, raw := range enabled {
			value, valid := raw.(bool)
			if !valid || value {
				return claudePortableError("settings reference unsupported plugins")
			}
		}
	} else if claudePortableValuePresent(document["enabledPlugins"]) {
		return claudePortableError("settings contain invalid plugin configuration")
	}
	if permissions, ok := document["permissions"].(map[string]any); ok && claudePortableValuePresent(permissions["additionalDirectories"]) {
		return claudePortableError("settings reference external directories")
	}
	for _, key := range []string{"autoMemoryDirectory", "plansDirectory", "outputStyleFile", "sandboxCertificate", "sandboxHelper"} {
		if claudePortableValuePresent(document[key]) {
			return claudePortableError("settings reference an external path")
		}
	}
	if environment, ok := document["env"].(map[string]any); ok {
		for name, value := range environment {
			if !validClaudePortableEnvironmentName(name) || forbiddenClaudePortableEnvironmentName(name) {
				return claudePortableError("settings contain an unsupported environment name")
			}
			text, valid := value.(string)
			if !valid {
				return claudePortableError("settings contain an invalid environment value")
			}
			if text != "" && externalClaudePortableEnvironmentName(name) {
				return claudePortableError("settings environment references unsupported external state")
			}
		}
		if claudePortableNonEmptyString(environment["CLAUDE_CODE_OAUTH_REFRESH_TOKEN"]) && !claudePortableNonEmptyString(environment["CLAUDE_CODE_OAUTH_SCOPES"]) {
			return claudePortableError("settings OAuth refresh credential lacks scopes")
		}
	} else if claudePortableValuePresent(document["env"]) {
		return claudePortableError("settings contain an invalid environment map")
	}
	return nil
}

func validateClaudePortableAppState(document map[string]any) error {
	for _, key := range []string{"mcpServers", "plugins"} {
		if claudePortableValuePresent(document[key]) {
			return claudePortableError("application state references unsupported external state")
		}
	}
	if projects, ok := document["projects"].(map[string]any); ok {
		for _, raw := range projects {
			project, valid := raw.(map[string]any)
			if !valid || claudePortableProjectHasExecutableState(project) {
				return claudePortableError("application project state references unsupported external state")
			}
		}
	} else if claudePortableValuePresent(document["projects"]) {
		return claudePortableError("application project state is invalid")
	}
	return nil
}

func claudePortableProjectHasExecutableState(document map[string]any) bool {
	for key, raw := range document {
		normalized := strings.ToLower(key)
		for _, fragment := range []string{"command", "executable", "externalinclude", "helper", "hook", "marketplace", "mcp", "plugin", "workflow", "wrapper"} {
			if strings.Contains(normalized, fragment) && claudePortableValuePresent(raw) {
				return true
			}
		}
		switch value := raw.(type) {
		case map[string]any:
			if claudePortableProjectHasExecutableState(value) {
				return true
			}
		case []any:
			for _, member := range value {
				if nested, ok := member.(map[string]any); ok && claudePortableProjectHasExecutableState(nested) {
					return true
				}
			}
		}
	}
	return false
}

func claudePortableSettingsCredential(document map[string]any) bool {
	environment, _ := document["env"].(map[string]any)
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN"} {
		if claudePortableNonEmptyString(environment[name]) {
			return true
		}
	}
	return false
}

func claudePortableNonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && text != ""
}

func claudePortableValuePresent(raw any) bool {
	switch value := raw.(type) {
	case nil:
		return false
	case string:
		return value != ""
	case []any:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	case bool:
		return value
	default:
		return true
	}
}

func claudePortableEnvironment(names, prefixes []string) ([]harnesses.PortableRuntimeEnvironment, bool, error) {
	credentialNames := map[string]bool{
		"ANTHROPIC_API_KEY":               true,
		"ANTHROPIC_AUTH_TOKEN":            true,
		"CLAUDE_CODE_OAUTH_TOKEN":         true,
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN": true,
	}
	scalarNames := map[string]bool{
		"ANTHROPIC_BASE_URL": true,
		"LANG":               true, "LC_ALL": true, "TZ": true, "TERM": true,
		"API_TIMEOUT_MS": true, "BASH_DEFAULT_TIMEOUT_MS": true, "BASH_MAX_TIMEOUT_MS": true,
		"MAX_THINKING_TOKENS": true, "MCP_TIMEOUT": true, "MCP_TOOL_TIMEOUT": true,
		"MAX_MCP_OUTPUT_TOKENS": true, "HTTP_PROXY": true, "HTTPS_PROXY": true,
		"NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
	}
	for _, prefix := range prefixes {
		if prefix != "CLAUDE_" && prefix != "ANTHROPIC_" {
			return nil, false, claudePortableError("adapter requested an unsupported environment prefix")
		}
	}
	requested := append([]string(nil), names...)
	for _, assignment := range os.Environ() {
		name := strings.SplitN(assignment, "=", 2)[0]
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				requested = append(requested, name)
				break
			}
		}
	}
	seen := make(map[string]struct{}, len(requested))
	environment := make([]harnesses.PortableRuntimeEnvironment, 0, len(requested))
	credential := false
	refreshCredential := false
	oauthScopes := false
	for _, name := range requested {
		isCredential := credentialNames[name]
		supported := scalarNames[name] || isCredential
		for _, prefix := range prefixes {
			supported = supported || strings.HasPrefix(name, prefix)
		}
		if !supported || !validClaudePortableEnvironmentName(name) {
			return nil, false, claudePortableError("adapter requested an unsupported environment name")
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		value, defined := os.LookupEnv(name)
		if !defined {
			continue
		}
		if name == "CLAUDE_CONFIG_DIR" {
			continue
		}
		if value != "" && externalClaudePortableEnvironmentName(name) {
			return nil, false, claudePortableError("environment references unsupported external state")
		}
		environment = append(environment, harnesses.PortableRuntimeEnvironment{Name: name})
		credential = credential || isCredential && value != ""
		refreshCredential = refreshCredential || name == "CLAUDE_CODE_OAUTH_REFRESH_TOKEN" && value != ""
		oauthScopes = oauthScopes || name == "CLAUDE_CODE_OAUTH_SCOPES" && value != ""
	}
	if refreshCredential && !oauthScopes {
		return nil, false, claudePortableError("OAuth refresh credential lacks scopes")
	}
	sort.Slice(environment, func(i, j int) bool { return environment[i].Name < environment[j].Name })
	return environment, credential, nil
}

func rejectClaudePortableExternalEnvironment() error {
	for _, assignment := range os.Environ() {
		name, value, _ := strings.Cut(assignment, "=")
		if value != "" && externalClaudePortableEnvironmentName(name) {
			return claudePortableError("environment selects unsupported external execution")
		}
	}
	return nil
}

func externalClaudePortableEnvironmentName(name string) bool {
	if strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") {
		return true
	}
	switch name {
	case "NODE_OPTIONS", "NODE_PATH", "BUN_OPTIONS", "BUN_INSTALL",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "AWS_CA_BUNDLE", "REQUESTS_CA_BUNDLE",
		"CLAUDE_CODE_PROCESS_WRAPPER", "CLAUDE_CODE_SHELL_PREFIX", "CLAUDE_ENV_FILE",
		"CLAUDE_CODE_PLUGIN_SEED_DIR", "CLAUDE_CODE_PLUGIN_CACHE_DIR",
		"CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE",
		"CLAUDE_CODE_DEBUG_LOGS_DIR", "CLAUDE_CODE_TMPDIR", "CLAUDE_CODE_GIT_BASH_PATH",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_VERTEX",
		"ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_FOUNDRY_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL",
		"USE_BUILTIN_RIPGREP":
		return true
	default:
		return false
	}
}

func validClaudePortableEnvironmentName(name string) bool {
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

func forbiddenClaudePortableEnvironmentName(name string) bool {
	return name == "HOME" || name == "PATH" || name == "CLAUDE_CONFIG_DIR" || strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "FIZEAU_")
}

func claudePortablePathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func rejectClaudePortableUnsupportedState(configRoot string) error {
	for _, unsupported := range []string{"plugins", "workflows"} {
		if present, inspectErr := claudePortablePathExists(filepath.Join(configRoot, unsupported)); inspectErr != nil || present {
			return claudePortableError("configuration contains unsupported executable state")
		}
	}
	for _, managed := range []string{
		"/etc/claude-code/managed-settings.json", "/etc/claude-code/managed-mcp.json",
		"/etc/claude-code/managed-settings.d",
	} {
		if present, inspectErr := claudePortablePathExists(managed); inspectErr != nil || present {
			return claudePortableError("managed configuration is outside the portable closure")
		}
	}
	return nil
}

func appendClaudePortableState(ctx context.Context, contribution *harnesses.PortableRuntimeContribution, state claudePortableState) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", claudePortableContextError("state discovery canceled", err)
	}
	info, err := os.Lstat(state.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, "", nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false, "", claudePortableError("state cannot be inspected")
	}
	asset := harnesses.PortableRuntimeAsset{Kind: state.kind, PathKind: state.pathKind, Source: state.path, Target: state.target}
	switch state.pathKind {
	case harnesses.PortableRuntimePathFile:
		if !info.Mode().IsRegular() {
			return false, "", claudePortableError("state file has an unsupported type")
		}
		asset.ContentSHA256, err = harnesses.PortableRuntimeFileDigest(state.path)
	case harnesses.PortableRuntimePathTree:
		if !info.IsDir() {
			return false, "", claudePortableError("state tree has an unsupported type")
		}
		asset.ContentSHA256, err = harnesses.PortableRuntimeTreeDigest(state.path)
	default:
		return false, "", claudePortableError("state has an unsupported type")
	}
	if err != nil {
		return false, "", claudePortableError("state cannot be content-addressed")
	}
	if err := ctx.Err(); err != nil {
		return false, "", claudePortableContextError("state discovery canceled", err)
	}
	contribution.Assets = append(contribution.Assets, asset)
	return true, asset.ContentSHA256, nil
}

func claudePortableError(message string) error {
	return fmt.Errorf("%w: %s", harnesses.ErrPortableRuntimeClosureIncomplete, message)
}

func claudePortableContextError(message string, cause error) error {
	return fmt.Errorf("%w: %s: %w", harnesses.ErrPortableRuntimeClosureIncomplete, message, cause)
}
