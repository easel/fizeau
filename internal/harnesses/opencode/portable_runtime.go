package opencode

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const (
	opencodePortableVersion          = "1.14.33"
	opencodePortableEntrypointTarget = "harnesses/opencode/bin/opencode"
	opencodePortableLoaderTarget     = "harnesses/opencode/loader/ld-linux-aarch64.so.1"
	opencodePortableLibraryTarget    = "harnesses/opencode/lib"
	opencodePortableConfigTarget     = "config/opencode"
	opencodePortableAuthTarget       = "data/opencode/auth.json"
	opencodePortableCacheTarget      = "cache/opencode/models.json"
	opencodePortableMaxStateFile     = 8 << 20
	opencodePortableProbeProfile     = 1
)

var _ harnesses.PortableRuntimeHarness = (*Runner)(nil)

type opencodePortableEvidence struct {
	goos                string
	goarch              string
	contentSHA256       string
	size                int64
	buildID             string
	interpreter         string
	offlineProbeProfile uint16
	libraryRoots        []harnesses.PortableRuntimeLibrarySearchRoot
}

// Tests replace this package-private record with evidence for a small dynamic
// fixture. Production accepts only the audited OpenCode 1.14.33 Linux arm64
// payload.
var opencodePortableVerified = opencodePortableEvidence{
	goos:                "linux",
	goarch:              "arm64",
	contentSHA256:       "66ef27d163a57834a216e0f54a30bd20ea0b82982cd4efbece6a729ee6458e97",
	size:                171116864,
	buildID:             "8c5e2642b94bf1eed8184712a2b0f441196585fa",
	interpreter:         "/lib/ld-linux-aarch64.so.1",
	offlineProbeProfile: opencodePortableProbeProfile,
}

type opencodePortableNPMMetadata struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Bin                  map[string]string `json:"bin"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	OS                   []string          `json:"os"`
	CPU                  []string          `json:"cpu"`
}

// PortableRuntimeAssets implements harnesses.PortableRuntimeHarness for the
// exact audited OpenCode 1.14.33 Linux arm64 binary. Discovery never executes
// the npm wrapper or native payload.
func (r *Runner) PortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	if ctx == nil {
		return harnesses.PortableRuntimeContribution{}, opencodePortableError("asset discovery context is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return harnesses.PortableRuntimeContribution{}, opencodePortableError("asset discovery canceled")
	}
	if err := harnesses.ValidatePortableRuntimeTarget(target); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	evidence := cloneOpenCodePortableEvidence(opencodePortableVerified)
	if evidence.offlineProbeProfile == 0 {
		return harnesses.PortableRuntimeContribution{}, opencodePortableError("offline probe evidence is unavailable")
	}
	if target.GOOS != evidence.goos || target.GOARCH != evidence.goarch {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: opencode verified payload requires linux arm64", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
	if _, exists := os.LookupEnv("OPENCODE_BIN_PATH"); exists {
		return harnesses.PortableRuntimeContribution{}, opencodePortableError("npm binary override is unsupported")
	}

	launcher := strings.TrimSpace(r.Binary)
	if launcher == "" {
		resolved, err := osexec.LookPath("opencode")
		if err != nil {
			return harnesses.PortableRuntimeContribution{}, opencodePortableError("launcher is unavailable")
		}
		launcher = resolved
	}
	payload, err := resolveOpenCodePortablePayload(launcher, evidence)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	roots, err := opencodePortableExactLibraryRoots(evidence)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	contribution, err := harnesses.AnalyzePortableRuntimeDynamicClosure(ctx, target, harnesses.PortableRuntimeDynamicClosureRequest{
		EntrypointSource:  payload,
		EntrypointTarget:  opencodePortableEntrypointTarget,
		LoaderTarget:      opencodePortableLoaderTarget,
		ExactLibraryRoots: roots,
		RuntimeLookup:     harnesses.PortableRuntimeLookupVerifiedExact,
	})
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	home, err := opencodePortableHome()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	configBase, err := opencodePortableXDGRoot("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	dataBase, err := opencodePortableXDGRoot("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	cacheBase, err := opencodePortableXDGRoot("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	configPath := filepath.Join(configBase, "opencode")
	configDigest, environment, configCredential, err := inspectOpenCodePortableConfig(ctx, configPath)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	contribution.Assets = append(contribution.Assets, harnesses.PortableRuntimeAsset{
		Kind: harnesses.PortableRuntimeAssetConfig, PathKind: harnesses.PortableRuntimePathTree,
		Source: configPath, Target: opencodePortableConfigTarget, ContentSHA256: configDigest,
	})

	_, authCredential, err := appendOpenCodePortableAuth(ctx, &contribution, filepath.Join(dataBase, "opencode", "auth.json"))
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if !authCredential && !configCredential {
		return harnesses.PortableRuntimeContribution{}, opencodePortableError("credential state is incomplete")
	}
	if err := appendOpenCodePortableCache(ctx, &contribution, filepath.Join(cacheBase, "opencode")); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	contribution.Environment = environment
	contribution.ExecutionConstraints = opencodePortableConstraints()
	return harnesses.NormalizePortableRuntimeContribution(target, contribution)
}

func cloneOpenCodePortableEvidence(in opencodePortableEvidence) opencodePortableEvidence {
	in.libraryRoots = append([]harnesses.PortableRuntimeLibrarySearchRoot(nil), in.libraryRoots...)
	return in
}

func resolveOpenCodePortablePayload(launcher string, evidence opencodePortableEvidence) (string, error) {
	if launcher == "" || !filepath.IsAbs(launcher) || filepath.Clean(launcher) != launcher {
		return "", opencodePortableError("launcher path is not absolute and normalized")
	}
	home, err := opencodePortableHome()
	if err != nil {
		return "", err
	}
	direct := filepath.Join(home, ".opencode", "bin", "opencode")
	if launcher == direct {
		return validateOpenCodePortablePayload(launcher, evidence)
	}

	resolved, err := filepath.EvalSymlinks(launcher)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", opencodePortableError("launcher cannot be resolved")
	}
	resolved = filepath.Clean(resolved)
	if filepath.Base(resolved) != "opencode" || filepath.Base(filepath.Dir(resolved)) != "bin" {
		return "", opencodePortableError("launcher layout is not recognized")
	}
	prefix, err := readOpenCodePortablePrefix(resolved, 32)
	if err != nil || !strings.HasPrefix(string(prefix), "#!/usr/bin/env node\n") {
		return "", opencodePortableError("launcher layout is not recognized")
	}
	return resolveOpenCodePortableNPMPayload(filepath.Dir(filepath.Dir(resolved)), evidence)
}

func resolveOpenCodePortableNPMPayload(packageRoot string, evidence opencodePortableEvidence) (string, error) {
	rootInfo, err := os.Lstat(packageRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", opencodePortableError("npm wrapper package is not a directory")
	}
	metadata, err := readOpenCodePortableNPMMetadata(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return "", err
	}
	if metadata.Name != "opencode-ai" || metadata.Version != opencodePortableVersion ||
		metadata.Bin["opencode"] != "./bin/opencode" || metadata.OptionalDependencies["opencode-linux-arm64"] != opencodePortableVersion {
		return "", opencodePortableError("npm wrapper metadata is unsupported")
	}

	cached := filepath.Join(packageRoot, "bin", ".opencode")
	if present, inspectErr := opencodePortablePathExists(cached); inspectErr != nil {
		return "", opencodePortableError("npm cached payload cannot be inspected")
	} else if present {
		return validateOpenCodePortablePayload(cached, evidence)
	}

	platformRoot := filepath.Join(packageRoot, "node_modules", "opencode-linux-arm64")
	info, err := os.Lstat(platformRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", opencodePortableError("npm nested platform package is missing")
	}
	platform, err := readOpenCodePortableNPMMetadata(filepath.Join(platformRoot, "package.json"))
	if err != nil {
		return "", err
	}
	if platform.Name != "opencode-linux-arm64" || platform.Version != opencodePortableVersion ||
		len(platform.OS) != 1 || platform.OS[0] != "linux" || len(platform.CPU) != 1 || platform.CPU[0] != "arm64" {
		return "", opencodePortableError("npm platform metadata is unsupported")
	}
	return validateOpenCodePortablePayload(filepath.Join(platformRoot, "bin", "opencode"), evidence)
}

func validateOpenCodePortablePayload(path string, evidence opencodePortableEvidence) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o100 == 0 {
		return "", opencodePortableError("native payload is not an owner-executable regular file")
	}
	if info.Size() != evidence.size {
		return "", opencodePortableError("native payload size is unverified")
	}
	digest, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil || digest != evidence.contentSHA256 {
		return "", opencodePortableError("native payload content is unverified")
	}
	buildID, interpreter, err := inspectOpenCodePortableELFEvidence(path)
	if err != nil || buildID != evidence.buildID || interpreter != evidence.interpreter {
		return "", opencodePortableError("native payload identity is unverified")
	}
	return filepath.Clean(path), nil
}

func inspectOpenCodePortableELFEvidence(path string) (string, string, error) {
	file, err := safefs.OpenRead(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	executable, err := elf.NewFile(file)
	if err != nil {
		return "", "", err
	}
	defer executable.Close()
	var interpreter string
	for _, program := range executable.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(program.Open(), 4096))
		if readErr != nil || len(data) < 2 || data[len(data)-1] != 0 {
			return "", "", errors.New("invalid ELF interpreter")
		}
		interpreter = string(data[:len(data)-1])
	}
	section := executable.Section(".note.gnu.build-id")
	if section == nil {
		return "", "", errors.New("missing GNU build ID")
	}
	notes, err := section.Data()
	if err != nil {
		return "", "", err
	}
	buildID, err := parseOpenCodeGNUNotes(notes, executable.ByteOrder)
	return buildID, interpreter, err
}

func parseOpenCodeGNUNotes(notes []byte, order binary.ByteOrder) (string, error) {
	for len(notes) >= 12 {
		nameSize := int(order.Uint32(notes[0:4]))
		descSize := int(order.Uint32(notes[4:8]))
		typeID := order.Uint32(notes[8:12])
		nameEnd := 12 + nameSize
		descStart := (nameEnd + 3) &^ 3
		descEnd := descStart + descSize
		next := (descEnd + 3) &^ 3
		if nameSize < 0 || descSize < 0 || nameEnd > len(notes) || descStart > len(notes) || descEnd > len(notes) || next > len(notes) {
			break
		}
		if typeID == 3 && string(notes[12:nameEnd]) == "GNU\x00" && descSize != 0 {
			return hex.EncodeToString(notes[descStart:descEnd]), nil
		}
		notes = notes[next:]
	}
	return "", errors.New("missing GNU build ID")
}

func opencodePortableExactLibraryRoots(evidence opencodePortableEvidence) ([]harnesses.PortableRuntimeLibrarySearchRoot, error) {
	if len(evidence.libraryRoots) != 0 {
		return append([]harnesses.PortableRuntimeLibrarySearchRoot(nil), evidence.libraryRoots...), nil
	}
	loader, err := filepath.EvalSymlinks(evidence.interpreter)
	if err != nil || !filepath.IsAbs(loader) {
		return nil, opencodePortableError("verified ELF loader is unavailable")
	}
	return []harnesses.PortableRuntimeLibrarySearchRoot{{Source: filepath.Dir(filepath.Clean(loader)), Target: opencodePortableLibraryTarget}}, nil
}

func inspectOpenCodePortableConfig(ctx context.Context, root string) (string, []harnesses.PortableRuntimeEnvironment, bool, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, false, opencodePortableError("required configuration tree is unavailable")
	}
	before, err := harnesses.PortableRuntimeTreeDigest(root)
	if err != nil {
		return "", nil, false, opencodePortableError("configuration tree cannot be content-addressed")
	}
	names := make(map[string]struct{})
	credential := false
	err = filepath.Walk(root, func(current string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return opencodePortableError("configuration tree cannot be scanned")
		}
		if err := ctx.Err(); err != nil {
			return opencodePortableError("configuration discovery canceled")
		}
		if entry.IsDir() {
			if current != root && forbiddenOpenCodePortableConfigPath(root, current) {
				return opencodePortableError("configuration tree contains executable package state")
			}
			return nil
		}
		if filepath.Dir(current) == root && filepath.Base(current) == "config" {
			return opencodePortableError("configuration contains an unsupported legacy document")
		}
		if forbiddenOpenCodePortableConfigPath(root, current) {
			return opencodePortableError("configuration tree contains executable package state")
		}
		if !entry.Mode().IsRegular() && entry.Mode()&os.ModeSymlink == 0 {
			return opencodePortableError("configuration tree contains unsupported state")
		}
		resolvedInfo, statErr := os.Stat(current)
		if statErr != nil || !resolvedInfo.Mode().IsRegular() || resolvedInfo.Mode().Perm()&0o111 != 0 {
			return opencodePortableError("configuration tree contains executable state")
		}
		data, readErr := readOpenCodePortableRegularFile(current, opencodePortableMaxStateFile)
		if readErr != nil {
			return opencodePortableError("configuration tree contains an unreadable file")
		}
		if strings.Contains(string(data), "{file:") {
			return opencodePortableError("configuration references external file state")
		}
		if openCodePortableConfigFile(current) {
			documentCredential, codeSurface, documentErr := inspectOpenCodePortableConfigDocument(data)
			if documentErr != nil {
				return opencodePortableError("configuration contains invalid JSON state")
			}
			if codeSurface {
				return opencodePortableError("configuration declares an unsupported code surface")
			}
			credential = credential || documentCredential
		}
		for _, match := range opencodePortableEnvironmentToken.FindAllSubmatch(data, -1) {
			name := string(match[1])
			if !validOpenCodePortableEnvironmentName(name) || forbiddenOpenCodePortableEnvironmentName(name) {
				return opencodePortableError("configuration contains an unsupported environment reference")
			}
			if _, exists := os.LookupEnv(name); exists {
				names[name] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, false, err
	}
	after, err := harnesses.PortableRuntimeTreeDigest(root)
	if err != nil || before != after {
		return "", nil, false, opencodePortableError("configuration changed during discovery")
	}
	environment := make([]harnesses.PortableRuntimeEnvironment, 0, len(names))
	for name := range names {
		environment = append(environment, harnesses.PortableRuntimeEnvironment{Name: name})
	}
	sort.Slice(environment, func(i, j int) bool { return environment[i].Name < environment[j].Name })
	return after, environment, credential, nil
}

var opencodePortableEnvironmentToken = regexp.MustCompile(`\{env:([^}]+)\}`)

func forbiddenOpenCodePortableConfigPath(root, current string) bool {
	relative, err := filepath.Rel(root, current)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return err != nil || relative != "."
	}
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		switch strings.ToLower(component) {
		case "command", "commands", "node_modules", "plugin", "plugins":
			return true
		}
	}
	return false
}

func openCodePortableConfigFile(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".json" || extension == ".jsonc"
}

func inspectOpenCodePortableConfigDocument(data []byte) (bool, bool, error) {
	normalized, err := normalizeOpenCodePortableJSONC(data)
	if err != nil {
		return false, false, err
	}
	var document any
	if json.Unmarshal(normalized, &document) != nil {
		return false, false, errors.New("invalid JSONC")
	}
	return inspectOpenCodePortableConfigValue(document)
}

func inspectOpenCodePortableConfigValue(value any) (bool, bool, error) {
	credential := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "command", "formatter", "instructions", "lsp", "mcp", "npm", "plugin", "plugins", "shell", "skills":
				return false, true, nil
			}
			if key == "apiKey" {
				text, ok := child.(string)
				if !ok {
					return false, false, errors.New("apiKey is not a string")
				}
				if token := opencodePortableEnvironmentToken.FindStringSubmatch(text); len(token) == 2 && token[0] == text {
					environmentValue, exists := os.LookupEnv(token[1])
					credential = credential || exists && environmentValue != ""
				} else if text != "" && !strings.Contains(text, "{") {
					credential = true
				}
			}
			childCredential, childCodeSurface, err := inspectOpenCodePortableConfigValue(child)
			if err != nil || childCodeSurface {
				return false, childCodeSurface, err
			}
			credential = credential || childCredential
		}
	case []any:
		for _, child := range typed {
			childCredential, childCodeSurface, err := inspectOpenCodePortableConfigValue(child)
			if err != nil || childCodeSurface {
				return false, childCodeSurface, err
			}
			credential = credential || childCredential
		}
	}
	return credential, false, nil
}

func normalizeOpenCodePortableJSONC(data []byte) ([]byte, error) {
	normalized := append([]byte(nil), data...)
	inString := false
	escaped := false
	for index := 0; index < len(normalized); index++ {
		character := normalized[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
			continue
		}
		if character != '/' || index+1 >= len(normalized) {
			continue
		}
		switch normalized[index+1] {
		case '/':
			normalized[index], normalized[index+1] = ' ', ' '
			index += 2
			for ; index < len(normalized) && normalized[index] != '\n' && normalized[index] != '\r'; index++ {
				normalized[index] = ' '
			}
			index--
		case '*':
			normalized[index], normalized[index+1] = ' ', ' '
			index += 2
			closed := false
			for ; index < len(normalized); index++ {
				if normalized[index] == '*' && index+1 < len(normalized) && normalized[index+1] == '/' {
					normalized[index], normalized[index+1] = ' ', ' '
					index++
					closed = true
					break
				}
				if normalized[index] != '\n' && normalized[index] != '\r' {
					normalized[index] = ' '
				}
			}
			if !closed {
				return nil, errors.New("unterminated JSONC comment")
			}
		}
	}
	if inString {
		return nil, errors.New("unterminated JSON string")
	}
	for index := 0; index < len(normalized); index++ {
		if normalized[index] != ',' {
			continue
		}
		next := index + 1
		for next < len(normalized) && (normalized[next] == ' ' || normalized[next] == '\t' || normalized[next] == '\r' || normalized[next] == '\n') {
			next++
		}
		if next < len(normalized) && (normalized[next] == '}' || normalized[next] == ']') {
			normalized[index] = ' '
		}
	}
	return normalized, nil
}

func appendOpenCodePortableAuth(ctx context.Context, contribution *harnesses.PortableRuntimeContribution, path string) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, opencodePortableError("credential discovery canceled")
	}
	present, err := opencodePortablePathExists(path)
	if err != nil {
		return false, false, opencodePortableError("credential state cannot be inspected")
	}
	if !present {
		return false, false, nil
	}
	data, digest, err := readOpenCodePortableRegularFileWithDigest(path, opencodePortableMaxStateFile)
	if err != nil {
		return false, false, opencodePortableError("credential state cannot be read")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil {
		return false, false, opencodePortableError("credential state is invalid")
	}
	credential := false
	for _, raw := range document {
		var record struct {
			Type    string `json:"type"`
			Key     string `json:"key"`
			Token   string `json:"token"`
			Access  string `json:"access"`
			Refresh string `json:"refresh"`
		}
		if json.Unmarshal(raw, &record) != nil {
			return false, false, opencodePortableError("credential state is invalid")
		}
		switch record.Type {
		case "api":
			credential = credential || record.Key != ""
		case "oauth":
			credential = credential || record.Access != "" || record.Refresh != ""
		case "wellknown":
			return false, false, opencodePortableError("credential state contains an unsupported remote record")
		default:
			return false, false, opencodePortableError("credential state contains an unsupported record")
		}
	}
	contribution.Assets = append(contribution.Assets, harnesses.PortableRuntimeAsset{
		Kind: harnesses.PortableRuntimeAssetCredential, PathKind: harnesses.PortableRuntimePathFile,
		Source: path, Target: opencodePortableAuthTarget, ContentSHA256: digest,
	})
	return true, credential, nil
}

func appendOpenCodePortableCache(ctx context.Context, contribution *harnesses.PortableRuntimeContribution, root string) error {
	if err := ctx.Err(); err != nil {
		return opencodePortableError("state discovery canceled")
	}
	if present, err := opencodePortablePathExists(filepath.Join(root, "bin")); err != nil {
		return opencodePortableError("cache executable state cannot be inspected")
	} else if present {
		return opencodePortableError("cache contains unsupported executable state")
	}
	source := filepath.Join(root, "models.json")
	present, err := opencodePortablePathExists(source)
	if err != nil {
		return opencodePortableError("optional state cannot be inspected")
	}
	if !present {
		return nil
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 != 0 {
		return opencodePortableError("optional cache file has an unsupported type")
	}
	data, digest, err := readOpenCodePortableRegularFileWithDigest(source, opencodePortableMaxStateFile)
	if err != nil {
		return opencodePortableError("optional state cannot be content-addressed")
	}
	if err := validateOpenCodePortableModelsCache(data); err != nil {
		return opencodePortableError("optional model state contains an unsupported provider")
	}
	contribution.Assets = append(contribution.Assets, harnesses.PortableRuntimeAsset{
		Kind: harnesses.PortableRuntimeAssetCache, PathKind: harnesses.PortableRuntimePathFile,
		Source: source, Target: opencodePortableCacheTarget, ContentSHA256: digest,
	})
	return nil
}

// This set is copied from OpenCode 1.14.33's BUNDLED_PROVIDERS registry. A
// models.dev cache can override the provider package at both provider and
// model scope; every explicit npm selector must stay inside the exact audited
// binary or OpenCode will call Npm.add and dynamically import undeclared code.
var opencodePortableBundledProviders = map[string]struct{}{
	"@ai-sdk/amazon-bedrock": {}, "@ai-sdk/anthropic": {}, "@ai-sdk/azure": {},
	"@ai-sdk/google": {}, "@ai-sdk/google-vertex": {}, "@ai-sdk/google-vertex/anthropic": {},
	"@ai-sdk/openai": {}, "@ai-sdk/openai-compatible": {}, "@ai-sdk/xai": {},
	"@ai-sdk/mistral": {}, "@ai-sdk/groq": {}, "@ai-sdk/deepinfra": {},
	"@ai-sdk/cerebras": {}, "@ai-sdk/cohere": {}, "@ai-sdk/gateway": {},
	"@ai-sdk/togetherai": {}, "@ai-sdk/perplexity": {}, "@ai-sdk/vercel": {},
	"@ai-sdk/alibaba": {}, "@ai-sdk/github-copilot": {},
	"@openrouter/ai-sdk-provider": {}, "gitlab-ai-provider": {}, "venice-ai-sdk-provider": {},
}

func validateOpenCodePortableModelsCache(data []byte) error {
	var document map[string]any
	if json.Unmarshal(data, &document) != nil {
		return errors.New("invalid models cache")
	}
	if document == nil {
		return errors.New("models cache root is not an object")
	}
	for _, providerValue := range document {
		provider, ok := providerValue.(map[string]any)
		if !ok {
			return errors.New("models cache provider is not an object")
		}
		if err := validateOpenCodePortableProviderSelector(provider); err != nil {
			return err
		}
		models, ok := provider["models"].(map[string]any)
		if !ok {
			return errors.New("models cache model set is not an object")
		}
		for _, modelValue := range models {
			model, ok := modelValue.(map[string]any)
			if !ok {
				return errors.New("models cache model is not an object")
			}
			selector, exists := model["provider"]
			if !exists {
				continue
			}
			providerOverride, ok := selector.(map[string]any)
			if !ok {
				return errors.New("models cache model provider is not an object")
			}
			if err := validateOpenCodePortableProviderSelector(providerOverride); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOpenCodePortableProviderSelector(value map[string]any) error {
	selector, exists := value["npm"]
	if !exists {
		return nil
	}
	name, ok := selector.(string)
	if !ok {
		return errors.New("provider package is not a string")
	}
	if _, bundled := opencodePortableBundledProviders[name]; !bundled {
		return errors.New("provider package is not bundled")
	}
	return nil
}

func opencodePortableConstraints() harnesses.PortableRuntimeExecutionConstraints {
	constraints := harnesses.PortableRuntimeExecutionConstraints{
		Environment: []harnesses.PortableRuntimeEnvironmentConstraint{
			{Name: "XDG_CONFIG_HOME", Kind: harnesses.PortableRuntimeEnvironmentGuestPath, GuestPath: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathRuntime, Target: "config"}},
		},
		ReadOnlyPaths:       []harnesses.PortableRuntimeGuestPath{{Scope: harnesses.PortableRuntimeGuestPathRuntime, Target: opencodePortableConfigTarget}},
		RequiredAbsentPaths: []harnesses.PortableRuntimeGuestPath{{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".opencode"}},
		FixedArguments:      []string{"--pure"},
	}
	for _, name := range []string{
		"OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_DEFAULT_PLUGINS",
		"OPENCODE_DISABLE_LSP_DOWNLOAD", "OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_MODELS_FETCH",
		"OPENCODE_DISABLE_CLAUDE_CODE",
	} {
		constraints.Environment = append(constraints.Environment, harnesses.PortableRuntimeEnvironmentConstraint{Name: name, Kind: harnesses.PortableRuntimeEnvironmentFixedTrue})
	}
	for _, name := range []string{
		"OPENCODE_BIN_PATH", "OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CONFIG_DIR",
		"OPENCODE_MODELS_PATH", "OPENCODE_MODELS_URL",
		"LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "BUN_OPTIONS", "BUN_INSTALL",
	} {
		constraints.Environment = append(constraints.Environment, harnesses.PortableRuntimeEnvironmentConstraint{Name: name, Kind: harnesses.PortableRuntimeEnvironmentUnset})
	}
	return constraints
}

func opencodePortableHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", opencodePortableError("home directory is unavailable")
	}
	return home, nil
}

func opencodePortableXDGRoot(name, fallback string) (string, error) {
	root := fallback
	if configured, exists := os.LookupEnv(name); exists {
		root = configured
	}
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", opencodePortableError("state root is invalid")
	}
	return root, nil
}

func readOpenCodePortableNPMMetadata(path string) (opencodePortableNPMMetadata, error) {
	data, err := readOpenCodePortableRegularFile(path, opencodePortableMaxStateFile)
	if err != nil {
		return opencodePortableNPMMetadata{}, opencodePortableError("npm metadata cannot be read")
	}
	var metadata opencodePortableNPMMetadata
	if json.Unmarshal(data, &metadata) != nil {
		return opencodePortableNPMMetadata{}, opencodePortableError("npm metadata is invalid")
	}
	return metadata, nil
}

func readOpenCodePortablePrefix(path string, limit int64) ([]byte, error) {
	file, err := safefs.OpenRead(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}

func readOpenCodePortableRegularFile(path string, limit int64) ([]byte, error) {
	data, _, err := readOpenCodePortableRegularFileWithDigest(path, limit)
	return data, err
}

func readOpenCodePortableRegularFileWithDigest(path string, limit int64) ([]byte, string, error) {
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

func opencodePortablePathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func validOpenCodePortableEnvironmentName(name string) bool {
	if name == "" || strings.ContainsRune(name, '=') {
		return false
	}
	for i := range len(name) {
		character := name[i]
		if i == 0 {
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

func forbiddenOpenCodePortableEnvironmentName(name string) bool {
	if name == "HOME" || name == "PATH" || strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "FIZEAU_") {
		return true
	}
	for _, constraint := range opencodePortableConstraints().Environment {
		if constraint.Name == name {
			return true
		}
	}
	return false
}

func opencodePortableError(message string) error {
	return fmt.Errorf("%w: opencode %s", harnesses.ErrPortableRuntimeClosureIncomplete, message)
}
