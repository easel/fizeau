package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const (
	geminiPortableEntrypointTarget  = "harnesses/gemini/package/bundle/gemini.js"
	geminiPortableInterpreterTarget = "harnesses/gemini/bin/node"
	geminiPortableLoaderTarget      = "harnesses/gemini/loader/ld-linux-aarch64.so.1"
	geminiPortableLibraryTarget     = "harnesses/gemini/lib/system"
	geminiPortablePackageTarget     = "harnesses/gemini/package"
)

var _ harnesses.PortableRuntimeHarness = (*Runner)(nil)

var analyzeGeminiPortableRuntime = harnesses.AnalyzePortableRuntimeInterpretedClosure
var selectGeminiPortableAddons = geminiPortableSelectedAddons
var locateGeminiPortableLibraryRoot = geminiPortableRuntimeLibraryRoot
var geminiPortableVerifiedTreeSHA256 = geminiPortablePackageTreeSHA256

type geminiPortableRuntimePaths struct {
	packageInstallRoot string
	packageRoot        string
	launcher           string
	interpreterRoot    string
	interpreter        string
}

type geminiPortablePackageMetadata struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Bin     map[string]string `json:"bin"`
}

// PortableRuntimeAssets contributes the one retained Gemini 0.46.0 npm
// layout paired with the separately installed clean Node 22.22.0 runtime.
// Discovery uses neither PATH nor the launcher's env-based shebang.
func (r *Runner) PortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	if ctx == nil {
		return harnesses.PortableRuntimeContribution{}, geminiPortableRuntimeError("asset discovery context is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return harnesses.PortableRuntimeContribution{}, geminiPortableRuntimeError("asset discovery was canceled")
	}
	if err := harnesses.ValidatePortableRuntimeTarget(target); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if target.GOOS != "linux" || target.GOARCH != "arm64" {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: Gemini portable runtime requires linux arm64", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return harnesses.PortableRuntimeContribution{}, geminiPortableRuntimeError("retained install root is unavailable")
	}
	if err := validateGeminiPortableLaterArguments(r.BaseArgs); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if err := inspectGeminiPortableUserConfiguration(home); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if err := inspectGeminiPortableSystemSources(geminiPortableDefaultSystemSources()); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	paths := geminiPortableRuntimePathsForHome(home)
	if configured := strings.TrimSpace(r.Binary); configured != "" && configured != paths.launcher {
		return harnesses.PortableRuntimeContribution{}, geminiPortableRuntimeError("configured launcher is not the retained install")
	}
	contribution, err := geminiPortableRuntimeAssets(ctx, target, paths)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	return harnesses.NormalizePortableRuntimeContribution(target, contribution)
}

func geminiPortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget, paths geminiPortableRuntimePaths) (harnesses.PortableRuntimeContribution, error) {
	if err := validateGeminiPortableRuntimeLayout(paths); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	selected, err := selectGeminiPortableAddons(target.GOOS, target.GOARCH)
	if err != nil || len(selected) != 2 || selected[0] != geminiPortableKeytarAddon || selected[1] != geminiPortablePTYAddon {
		return harnesses.PortableRuntimeContribution{}, geminiPortableRuntimeError("retained addon selection is unavailable")
	}
	identities := []geminiPortableELFEvidence{geminiPortableKeytarEvidence, geminiPortablePTYEvidence}
	addons := make([]harnesses.PortableRuntimeNativeAddon, len(selected))
	for i := range selected {
		addons[i] = harnesses.PortableRuntimeNativeAddon{
			PackageTreeTarget: geminiPortablePackageTarget,
			RelativePath:      selected[i],
			Identity: harnesses.PortableRuntimeFileIdentity{
				Size: identities[i].size, ContentSHA256: identities[i].contentSHA256,
			},
		}
	}
	libraryRoot, err := locateGeminiPortableLibraryRoot()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	contribution, err := analyzeGeminiPortableRuntime(ctx, target, harnesses.PortableRuntimeInterpretedClosureRequest{
		EntrypointSource:            paths.launcher,
		EntrypointTarget:            geminiPortableEntrypointTarget,
		EntrypointPackageTreeTarget: geminiPortablePackageTarget,
		InterpreterSource:           paths.interpreter,
		InterpreterIdentity: harnesses.PortableRuntimeFileIdentity{
			Size: geminiPortableNodeEvidence.size, ContentSHA256: geminiPortableNodeEvidence.contentSHA256,
		},
		InterpreterTarget: geminiPortableInterpreterTarget,
		LoaderTarget:      geminiPortableLoaderTarget,
		ExactLibraryRoots: []harnesses.PortableRuntimeLibrarySearchRoot{{
			Source: libraryRoot, Target: geminiPortableLibraryTarget,
		}},
		PackageTrees:  []harnesses.PortableRuntimeSourceTree{{Source: paths.packageRoot, Target: geminiPortablePackageTarget}},
		NativeAddons:  addons,
		RuntimeLookup: harnesses.PortableRuntimeLookupVerifiedExact,
	})
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	contribution.ExecutionConstraints = geminiPortableExecutionConstraints()
	return contribution, nil
}

func geminiPortableRuntimePathsForHome(home string) geminiPortableRuntimePaths {
	packageInstallRoot := filepath.Join(home, ".local", "share", "mise", "installs", "node", geminiPortablePackageNodeVersion)
	interpreterRoot := filepath.Join(home, ".local", "share", "mise", "installs", "node", geminiPortableNodeVersion)
	return geminiPortableRuntimePaths{
		packageInstallRoot: packageInstallRoot,
		packageRoot:        filepath.Join(packageInstallRoot, filepath.FromSlash(geminiPortablePackageRelative)),
		launcher:           filepath.Join(packageInstallRoot, filepath.FromSlash(geminiPortableLauncherRelative)),
		interpreterRoot:    interpreterRoot,
		interpreter:        filepath.Join(interpreterRoot, "bin", "node"),
	}
}

func validateGeminiPortableRuntimeLayout(paths geminiPortableRuntimePaths) error {
	for _, source := range []string{paths.packageInstallRoot, paths.packageRoot, paths.launcher, paths.interpreterRoot, paths.interpreter} {
		if !filepath.IsAbs(source) || filepath.Clean(source) != source {
			return geminiPortableRuntimeError("installed layout is not retained")
		}
	}
	if paths.packageInstallRoot == paths.interpreterRoot ||
		paths.packageRoot != filepath.Join(paths.packageInstallRoot, filepath.FromSlash(geminiPortablePackageRelative)) ||
		paths.launcher != filepath.Join(paths.packageInstallRoot, filepath.FromSlash(geminiPortableLauncherRelative)) ||
		paths.interpreter != filepath.Join(paths.interpreterRoot, "bin", "node") {
		return geminiPortableRuntimeError("installed layout is not retained")
	}
	launcherInfo, err := os.Lstat(paths.launcher)
	if err != nil || launcherInfo.Mode()&os.ModeSymlink == 0 {
		return geminiPortableRuntimeError("launcher does not match retained evidence")
	}
	link, err := os.Readlink(paths.launcher)
	if err != nil || filepath.ToSlash(link) != geminiPortableLauncherLink {
		return geminiPortableRuntimeError("launcher does not match retained evidence")
	}
	entrypoint, err := filepath.EvalSymlinks(paths.launcher)
	if err != nil || entrypoint != filepath.Join(paths.packageRoot, filepath.FromSlash(geminiPortableEntrypoint)) {
		return geminiPortableRuntimeError("launcher does not match retained evidence")
	}
	entrypointInfo, err := os.Lstat(entrypoint)
	if err != nil || !entrypointInfo.Mode().IsRegular() || entrypointInfo.Mode().Perm()&0o100 == 0 {
		return geminiPortableRuntimeError("launcher does not match retained evidence")
	}
	interpreterInfo, err := os.Lstat(paths.interpreter)
	if err != nil || !interpreterInfo.Mode().IsRegular() || interpreterInfo.Mode()&os.ModeSymlink != 0 || interpreterInfo.Mode().Perm()&0o100 == 0 {
		return geminiPortableRuntimeError("interpreter does not match retained evidence")
	}
	if err := validateGeminiPortablePackageMetadata(filepath.Join(paths.packageRoot, "package.json"), geminiPortablePackageMetadata{
		Name: "@google/gemini-cli", Version: geminiPortablePackageVersion, Bin: map[string]string{"gemini": geminiPortableEntrypoint},
	}); err != nil {
		return err
	}
	for _, metadata := range []struct {
		relative string
		want     geminiPortablePackageMetadata
	}{
		{"node_modules/@github/keytar/package.json", geminiPortablePackageMetadata{Name: "@github/keytar", Version: geminiPortableKeytarPackageEvidence.version}},
		{"node_modules/@lydell/node-pty/package.json", geminiPortablePackageMetadata{Name: "@lydell/node-pty", Version: geminiPortablePTYPackageEvidence.version}},
		{"node_modules/@lydell/node-pty-linux-arm64/package.json", geminiPortablePackageMetadata{Name: "@lydell/node-pty-linux-arm64", Version: geminiPortablePTYLinuxARM64Evidence.version}},
	} {
		if err := validateGeminiPortablePackageMetadata(filepath.Join(paths.packageRoot, filepath.FromSlash(metadata.relative)), metadata.want); err != nil {
			return err
		}
	}
	treeDigest, err := harnesses.PortableRuntimeTreeDigest(paths.packageRoot)
	if err != nil || treeDigest != geminiPortableVerifiedTreeSHA256 {
		return geminiPortableRuntimeError("package tree does not match retained evidence")
	}
	return nil
}

func validateGeminiPortablePackageMetadata(source string, want geminiPortablePackageMetadata) error {
	contents, err := safefs.ReadFile(source)
	if err != nil {
		return geminiPortableRuntimeError("package selection evidence is unavailable")
	}
	var got geminiPortablePackageMetadata
	if json.Unmarshal(contents, &got) != nil || got.Name != want.Name || got.Version != want.Version ||
		want.Bin != nil && !reflect.DeepEqual(got.Bin, want.Bin) {
		return geminiPortableRuntimeError("package selection evidence does not match")
	}
	return nil
}

func geminiPortableRuntimeLibraryRoot() (string, error) {
	const source = "/usr/lib/aarch64-linux-gnu"
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", geminiPortableRuntimeError("system library evidence is unavailable")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", geminiPortableRuntimeError("system library evidence is unavailable")
	}
	return filepath.Clean(resolved), nil
}

func geminiPortableRuntimeError(message string) error {
	return fmt.Errorf("%w: %s", harnesses.ErrPortableRuntimeClosureIncomplete, message)
}
