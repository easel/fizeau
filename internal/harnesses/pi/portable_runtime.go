package pi

import (
	"context"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/easel/fizeau/internal/harnesses"
)

const (
	piPortableEntrypointTarget  = "harnesses/pi/package/dist/cli.js"
	piPortableInterpreterTarget = "harnesses/pi/bin/node"
	piPortableLoaderTarget      = "harnesses/pi/loader/ld-linux-aarch64.so.1"
	piPortableLibraryTarget     = "harnesses/pi/lib/system"
	piPortablePackageTarget     = "harnesses/pi/package"
)

var _ harnesses.PortableRuntimeHarness = (*Runner)(nil)

var analyzePiPortableRuntime = harnesses.AnalyzePortableRuntimeInterpretedClosure
var locatePiPortableLibraryRoot = piPortableRuntimeLibraryRoot

type piPortableRuntimePaths struct {
	launcher    string
	packageRoot string
	interpreter string
	agentDir    string
}

// PortableRuntimeAssets contributes the one retained Pi 0.51.4 global npm
// layout paired with the separately installed clean Node 22.22.0 runtime.
// Discovery recognizes the npm symlink but the launch recipe invokes Node
// through its explicit loader closure, never through PATH or the env shebang.
func (r *Runner) PortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	if ctx == nil {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("asset discovery context is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("asset discovery was canceled")
	}
	if err := harnesses.ValidatePortableRuntimeTarget(target); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if target.GOOS != piPortableGOOS || target.GOARCH != piPortableGOARCH {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: Pi portable runtime requires linux arm64", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
	if err := validatePiPortableContributorEvidence(); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if err := validatePiPortableLaterArguments(r.BaseArgs); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("retained install root is unavailable")
	}
	launcher := strings.TrimSpace(r.Binary)
	if launcher == "" {
		launcher, err = exec.LookPath("pi")
		if err != nil {
			return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("retained launcher is unavailable")
		}
	}
	launcher, err = filepath.Abs(launcher)
	if err != nil || !filepath.IsAbs(launcher) || filepath.Clean(launcher) != launcher {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("configured launcher is not the retained install")
	}
	prefix := filepath.Dir(filepath.Dir(launcher))
	paths := piPortableRuntimePaths{
		launcher:    launcher,
		packageRoot: filepath.Join(prefix, filepath.FromSlash(piPortableVerifiedRuntime.release.packageRelative)),
		interpreter: filepath.Join(home, ".local", "share", "mise", "installs", "node", piPortableVerifiedRuntime.node.version, "bin", "node"),
		agentDir:    filepath.Join(home, piPortableAgentDirectory),
	}
	return piPortableRuntimeAssets(ctx, target, paths)
}

func piPortableRuntimeAssets(ctx context.Context, target harnesses.PortableRuntimeTarget, paths piPortableRuntimePaths) (harnesses.PortableRuntimeContribution, error) {
	if ctx == nil {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("asset discovery context is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("asset discovery was canceled")
	}
	if err := harnesses.ValidatePortableRuntimeTarget(target); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if target.GOOS != piPortableGOOS || target.GOARCH != piPortableGOARCH {
		return harnesses.PortableRuntimeContribution{}, fmt.Errorf("%w: Pi portable runtime requires linux arm64", harnesses.ErrPortableRuntimeTargetUnsupported)
	}
	if err := validatePiPortableContributorEvidence(); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	configuration, err := inspectPiPortableConfiguration(ctx, paths.agentDir, nil)
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if err := validatePiPortableRuntimePaths(paths); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if err := validatePiPortableRuntimeData(paths.packageRoot); err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	libraryRoot, err := locatePiPortableLibraryRoot()
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}

	contribution, err := analyzePiPortableRuntime(ctx, target, harnesses.PortableRuntimeInterpretedClosureRequest{
		EntrypointSource:            paths.launcher,
		EntrypointTarget:            piPortableEntrypointTarget,
		EntrypointPackageTreeTarget: piPortablePackageTarget,
		InterpreterSource:           paths.interpreter,
		InterpreterIdentity: harnesses.PortableRuntimeFileIdentity{
			Size: piPortableVerifiedRuntime.node.size, ContentSHA256: piPortableVerifiedRuntime.node.sha256,
		},
		InterpreterTarget: piPortableInterpreterTarget,
		LoaderTarget:      piPortableLoaderTarget,
		ExactLibraryRoots: []harnesses.PortableRuntimeLibrarySearchRoot{{Source: libraryRoot, Target: piPortableLibraryTarget}},
		PackageTrees:      []harnesses.PortableRuntimeSourceTree{{Source: paths.packageRoot, Target: piPortablePackageTarget}},
		RuntimeLookup:     harnesses.PortableRuntimeLookupVerifiedExact,
	})
	if err != nil {
		return harnesses.PortableRuntimeContribution{}, err
	}
	if !piPortablePackageTreeIdentity(contribution) {
		return harnesses.PortableRuntimeContribution{}, piPortableRuntimeError("package tree does not match retained evidence")
	}

	contribution.Assets = append(contribution.Assets, configuration.assets...)
	contribution.Environment = configuration.environment
	contribution.ExecutionConstraints = piPortableRuntimeConstraints()
	contribution.StateProjections = configuration.stateProjections
	return harnesses.NormalizePortableRuntimeContribution(target, contribution)
}

func validatePiPortableContributorEvidence() error {
	release := piPortableVerifiedRuntime.release
	node := piPortableVerifiedRuntime.node
	data := piPortableVerifiedRuntime.data
	if release.packageName != "@mariozechner/pi-coding-agent" || release.version != "0.51.4" ||
		release.integrity != "sha512-agQJ38Hq4vjukzB1AC4Mj2lJ3H3zVBzYz4Fuyu8rvTMRAVkB1zlL+CMHF8FsNZ2+bVkKvMHZusc7nIQ1cPbf4Q==" ||
		release.shasum != "025749df96513e9d328f3c501bdd37ac7e878fe4" ||
		release.signatureKeyID != "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U" ||
		release.packageRelative != "lib/node_modules/@mariozechner/pi-coding-agent" ||
		release.launcherRelative != "lib/node_modules/@mariozechner/pi-coding-agent/dist/cli.js" ||
		release.launcherLink != "../lib/node_modules/@mariozechner/pi-coding-agent/dist/cli.js" ||
		release.binName != "pi" || release.binRelative != "dist/cli.js" || release.launcherSize != 302 ||
		release.launcherSHA256 != "34277c76b394762bc1711e859e4b86caf45ac92a85c1b8894671aa584e53a27a" {
		return piPortableRuntimeError("release evidence is unavailable")
	}
	if piPortableVerifiedRuntime.tree.format != "fizeau-portable-tree-v1" ||
		piPortableVerifiedRuntime.tree.digest != "e24e2b681a84d3aa44abc3ff565d23f827f668a6e5325070f738e8a420dc4e09" ||
		piPortableVerifiedRuntime.tree.records != 17594 || piPortableVerifiedRuntime.tree.goos != piPortableGOOS ||
		piPortableVerifiedRuntime.tree.goarch != piPortableGOARCH {
		return piPortableRuntimeError("package tree evidence is unavailable")
	}
	if node.version != "22.22.0" || node.size != 120592136 ||
		node.sha256 != "8eeefcacdf48f58541a651016e604055d14a992e39df98636b76495bc7244395" ||
		node.buildID != "c917b99f70bd51f3f5f37c6fa71bdea3534e192c" ||
		node.interpreter != "/lib/ld-linux-aarch64.so.1" || node.rejectedBrew != "26.5.0" {
		return piPortableRuntimeError("interpreter evidence is unavailable")
	}
	if data.photonRelative != "node_modules/@silvia-odwyer/photon-node/photon_rs_bg.wasm" ||
		data.photonSize != 1881634 || data.photonSHA256 != "10468181565c56004c867f3a4af96f89a0ef5a63a72f2b5fb12c1f1992a3615c" ||
		data.doomRelative != "examples/extensions/doom-overlay/doom/build/doom.wasm" ||
		data.doomSHA256 != "571d161956593508cf4ade732ae93753f00484bb526667a8676571cca14dec7d" ||
		data.clipboardRelative != "node_modules/@mariozechner/clipboard-linux-arm64-gnu/clipboard.linux-arm64-gnu.node" ||
		data.clipboardSize != 2309056 || data.clipboardSHA256 != "1c15a004a06c9dc5eda5ba0a7a3535203eb141b97098ca033ca49a1269f84663" ||
		data.clipboardClass != elf.ELFCLASS64 ||
		!slices.Equal(data.clipboardNeeded, []string{"libgcc_s.so.1", "libpthread.so.0", "libm.so.6", "libdl.so.2", "libc.so.6"}) ||
		!slices.Equal(data.forbiddenDisplay, []string{"DISPLAY", "WAYLAND_DISPLAY"}) {
		return piPortableRuntimeError("runtime data evidence is unavailable")
	}
	return nil
}

func validatePiPortableRuntimePaths(paths piPortableRuntimePaths) error {
	for _, source := range []string{paths.launcher, paths.packageRoot, paths.interpreter, paths.agentDir} {
		if !filepath.IsAbs(source) || filepath.Clean(source) != source {
			return piPortableRuntimeError("installed layout is not retained")
		}
	}
	wantPackage, err := inspectPiPortableInstalledRelease(paths.launcher, piPortableVerifiedRuntime.release)
	if err != nil || wantPackage != paths.packageRoot {
		return piPortableRuntimeError("installed layout is not retained")
	}
	info, err := os.Lstat(paths.interpreter)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o100 == 0 {
		return piPortableRuntimeError("interpreter does not match retained evidence")
	}
	return nil
}

func validatePiPortableRuntimeData(packageRoot string) error {
	data := piPortableVerifiedRuntime.data
	photon := filepath.Join(packageRoot, filepath.FromSlash(data.photonRelative))
	info, err := os.Lstat(photon)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != data.photonSize {
		return piPortableRuntimeError("required runtime data does not match retained evidence")
	}
	digest, err := harnesses.PortableRuntimeFileDigest(photon)
	if err != nil || digest != data.photonSHA256 {
		return piPortableRuntimeError("required runtime data does not match retained evidence")
	}

	// Doom is retained only as example data. Its exact classified member is
	// bound here while the closed package-tree digest binds the already-reviewed
	// reachability result; the contributor never performs an open-ended scan.
	doom := filepath.Join(packageRoot, filepath.FromSlash(data.doomRelative))
	info, err = os.Lstat(doom)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return piPortableRuntimeError("runtime data classification does not match retained evidence")
	}
	digest, err = harnesses.PortableRuntimeFileDigest(doom)
	if err != nil || digest != data.doomSHA256 {
		return piPortableRuntimeError("runtime data classification does not match retained evidence")
	}

	// Clipboard is absent from NativeAddons only while the exact display-gated
	// binary and its reviewed dynamic dependency table remain unchanged.
	clipboard := filepath.Join(packageRoot, filepath.FromSlash(data.clipboardRelative))
	info, err = os.Lstat(clipboard)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != data.clipboardSize {
		return piPortableRuntimeError("display-gated addon does not match retained evidence")
	}
	digest, err = harnesses.PortableRuntimeFileDigest(clipboard)
	if err != nil || digest != data.clipboardSHA256 {
		return piPortableRuntimeError("display-gated addon does not match retained evidence")
	}
	addon, err := elf.Open(clipboard)
	if err != nil {
		return piPortableRuntimeError("display-gated addon does not match retained evidence")
	}
	if addon.Class != data.clipboardClass || data.clipboardClass != elf.ELFCLASS64 || addon.Machine != elf.EM_AARCH64 {
		_ = addon.Close()
		return piPortableRuntimeError("display-gated addon does not match retained evidence")
	}
	needed, neededErr := addon.ImportedLibraries()
	rpath, rpathErr := addon.DynString(elf.DT_RPATH)
	runpath, runpathErr := addon.DynString(elf.DT_RUNPATH)
	closeErr := addon.Close()
	if neededErr != nil || rpathErr != nil || runpathErr != nil || closeErr != nil ||
		!slices.Equal(needed, data.clipboardNeeded) || len(rpath) != 0 || len(runpath) != 0 {
		return piPortableRuntimeError("display-gated addon dependency evidence does not match")
	}
	return nil
}

func piPortablePackageTreeIdentity(contribution harnesses.PortableRuntimeContribution) bool {
	for _, asset := range contribution.Assets {
		if asset.Target == piPortablePackageTarget {
			return asset.Kind == harnesses.PortableRuntimeAssetInstallTree &&
				asset.PathKind == harnesses.PortableRuntimePathTree &&
				asset.ContentSHA256 == piPortableVerifiedRuntime.tree.digest
		}
	}
	return false
}

func piPortableRuntimeLibraryRoot() (string, error) {
	const source = "/usr/lib/aarch64-linux-gnu"
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || resolved != source {
		return "", piPortableRuntimeError("system library evidence is unavailable")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", piPortableRuntimeError("system library evidence is unavailable")
	}
	return resolved, nil
}

func piPortableRuntimeConstraints() harnesses.PortableRuntimeExecutionConstraints {
	constraints := harnesses.PortableRuntimeExecutionConstraints{
		FixedArguments: append([]string(nil), piPortableFixedArguments...),
	}
	for _, name := range []string{
		"DISPLAY", "WAYLAND_DISPLAY", "PI_CODING_AGENT_DIR",
		"LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH",
	} {
		constraints.Environment = append(constraints.Environment, harnesses.PortableRuntimeEnvironmentConstraint{
			Name: name, Kind: harnesses.PortableRuntimeEnvironmentUnset,
		})
	}
	return constraints
}

func piPortableRuntimeError(message string) error {
	return fmt.Errorf("%w: Pi portable %s", harnesses.ErrPortableRuntimeClosureIncomplete, message)
}
