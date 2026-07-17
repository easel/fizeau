package pi

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestPiPortableRuntimeContribution(t *testing.T) {
	target, paths := requirePiPortableRuntimePaths(t)
	originalAnalyzer := analyzePiPortableRuntime
	var captured harnesses.PortableRuntimeInterpretedClosureRequest
	analyzePiPortableRuntime = func(ctx context.Context, target harnesses.PortableRuntimeTarget, request harnesses.PortableRuntimeInterpretedClosureRequest) (harnesses.PortableRuntimeContribution, error) {
		captured = request
		return originalAnalyzer(ctx, target, request)
	}
	t.Cleanup(func() { analyzePiPortableRuntime = originalAnalyzer })
	contribution, err := (&Runner{Binary: paths.launcher}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if contribution.ClosureClass != harnesses.PortableRuntimeClosureInterpreted {
		t.Fatalf("closure class = %q", contribution.ClosureClass)
	}
	if captured.EntrypointSource != paths.launcher || captured.EntrypointPackageTreeTarget != piPortablePackageTarget ||
		captured.InterpreterSource != paths.interpreter || len(captured.PackageTrees) != 1 ||
		captured.PackageTrees[0] != (harnesses.PortableRuntimeSourceTree{Source: paths.packageRoot, Target: piPortablePackageTarget}) ||
		len(captured.NativeAddons) != 0 || captured.RuntimeLookup != harnesses.PortableRuntimeLookupVerifiedExact {
		t.Fatalf("neutral analyzer request = %#v", captured)
	}
	if contribution.Launch.EntrypointTarget != piPortableEntrypointTarget ||
		contribution.Launch.EntrypointTreeMember != piPortableVerifiedRuntime.release.binRelative ||
		contribution.Launch.InterpreterTarget != piPortableInterpreterTarget ||
		contribution.Launch.LoaderTarget != piPortableLoaderTarget ||
		len(contribution.Launch.LibraryRootTargets) != 1 || contribution.Launch.LibraryRootTargets[0] != piPortableLibraryTarget ||
		len(contribution.Launch.RuntimeArgs) != 0 {
		t.Fatalf("launch recipe = %#v", contribution.Launch)
	}

	wanted := map[string]harnesses.PortableRuntimeAssetKind{
		piPortableInterpreterTarget: harnesses.PortableRuntimeAssetSupport,
		piPortableLoaderTarget:      harnesses.PortableRuntimeAssetSupport,
		piPortablePackageTarget:     harnesses.PortableRuntimeAssetInstallTree,
		piPortableSettingsTarget:    harnesses.PortableRuntimeAssetConfig,
		piPortableModelsTarget:      harnesses.PortableRuntimeAssetConfig,
	}
	if paths.authPresent {
		wanted[piPortableAuthTarget] = harnesses.PortableRuntimeAssetCredential
	}
	for _, asset := range contribution.Assets {
		if kind, ok := wanted[asset.Target]; ok {
			if asset.Kind != kind || asset.ContentSHA256 == "" {
				t.Fatalf("asset %q = %#v", asset.Target, asset)
			}
			delete(wanted, asset.Target)
		}
		if asset.PathKind == harnesses.PortableRuntimePathFile && strings.HasSuffix(asset.Source, ".node") {
			t.Fatalf("display-gated native addon entered the emitted file closure: %#v", asset)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing exact closure assets: %#v", wanted)
	}
	packageAsset := piPortableAssetByTarget(t, contribution, piPortablePackageTarget)
	if packageAsset.ContentSHA256 != piPortableVerifiedRuntime.tree.digest {
		t.Fatalf("package tree digest = %q", packageAsset.ContentSHA256)
	}
	photon := filepath.Join(packageAsset.Source, filepath.FromSlash(piPortableVerifiedRuntime.data.photonRelative))
	info, err := os.Lstat(photon)
	if err != nil || !info.Mode().IsRegular() || info.Size() != piPortableVerifiedRuntime.data.photonSize {
		t.Fatalf("required Photon member is unavailable")
	}

	wantUnset := []string{"DISPLAY", "LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "PI_CODING_AGENT_DIR", "WAYLAND_DISPLAY"}
	var gotUnset []string
	for _, constraint := range contribution.ExecutionConstraints.Environment {
		if constraint.Kind == harnesses.PortableRuntimeEnvironmentUnset {
			gotUnset = append(gotUnset, constraint.Name)
		}
	}
	if !slices.Equal(gotUnset, wantUnset) {
		t.Fatalf("unset environment = %q, want %q", gotUnset, wantUnset)
	}
	if !slices.Equal(contribution.ExecutionConstraints.FixedArguments, piPortableFixedArguments) {
		t.Fatalf("fixed arguments = %q", contribution.ExecutionConstraints.FixedArguments)
	}
	if paths.authPresent {
		if len(contribution.StateProjections) != 1 || contribution.StateProjections[0].Directory != (harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: piPortableAgentDirectory}) {
			t.Fatalf("mixed-state projection = %#v", contribution.StateProjections)
		}
	} else if len(contribution.StateProjections) != 0 {
		t.Fatalf("absent auth fabricated a state projection: %#v", contribution.StateProjections)
	}

	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand("/portable", contribution, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/portable/"+piPortableLoaderTarget || len(arguments) < 9 || arguments[0] != "--library-path" ||
		arguments[2] != "/portable/"+piPortableInterpreterTarget || arguments[3] != "/portable/"+piPortableEntrypointTarget ||
		!slices.Equal(arguments[4:8], piPortableFixedArguments) {
		t.Fatalf("expanded launch = %q %q", command, arguments)
	}

	assertPiPortableContributionMutations(t, target, paths)
}

func TestPiPortableRuntimeRunnerArgv(t *testing.T) {
	base := []string{"--mode", "json", "--print", "--no-session"}
	req := harnesses.ExecuteRequest{Provider: "fixture-provider", Model: "fixture-model", Reasoning: "high", Prompt: "fixture prompt"}
	got, err := piPortableArguments(base, req, "arg")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--mode", "json", "--print", "--no-session",
		"--provider", "fixture-provider", "--model", "fixture-model", "--thinking", "high", "fixture prompt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runner argv = %q, want %q", got, want)
	}
	constraints := piPortableRuntimeConstraints()
	if !slices.Equal(constraints.FixedArguments, got[:len(piPortableFixedArguments)]) {
		t.Fatalf("manifest prefix %q differs from runner prefix %q", constraints.FixedArguments, got)
	}

	for _, conflict := range []string{
		"--extension", "-e", "--extension=/private/extension.js", "--no-extensions", "-ne",
		"--skill", "--no-skills", "-ns", "--prompt-template", "--no-prompt-templates", "-np",
		"--theme", "--no-themes",
	} {
		t.Run("reject "+strings.TrimLeft(strings.ReplaceAll(conflict, "/", "_"), "-"), func(t *testing.T) {
			_, err := piPortableArguments(append(slices.Clone(base), conflict), req, "arg")
			assertPiPortableRuntimeFailure(t, err, conflict)
		})
	}
	for _, prompt := range []string{"--extension", "--skill", "--prompt-template", "--theme"} {
		changed := req
		changed.Prompt = prompt
		_, err := piPortableArguments(base, changed, "arg")
		assertPiPortableRuntimeFailure(t, err, prompt)
	}
	stdinArgs, err := piPortableArguments(base, req, "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(stdinArgs, req.Prompt) {
		t.Fatalf("stdin prompt entered argv: %q", stdinArgs)
	}

	t.Run("production dispatch uses the same prefix", func(t *testing.T) {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("sh is unavailable")
		}
		directory := t.TempDir()
		capture := filepath.Join(directory, "argv")
		binary := filepath.Join(directory, "pi")
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%s\\n' '{\"type\":\"text_end\",\"response\":\"ok\"}'\n", capture)
		if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		runner := &Runner{Binary: binary, BaseArgs: base}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		events, err := runner.Execute(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for range events {
		}
		contents, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(strings.Split(strings.TrimSpace(string(contents)), "\n"), want) {
			t.Fatalf("production argv = %q", contents)
		}
	})
}

func TestPiPortableRuntimeRecipeProbe(t *testing.T) {
	target, paths := requirePiPortableRuntimePaths(t)
	contribution, err := (&Runner{Binary: paths.launcher}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}

	positive := runPiPortableContributionProbe(t, contribution, piPortableContributionProbeOptions{})
	if positive.err != nil || positive.timedOut || positive.exitCode != 0 || strings.TrimSpace(positive.output) != piPortableVersion {
		t.Fatalf("isolated Pi recipe probe = %#v", positive)
	}

	var missingLibrary string
	for _, asset := range contribution.Assets {
		if asset.Kind == harnesses.PortableRuntimeAssetSupport && filepath.Base(asset.Target) == "libstdc++.so.6" {
			missingLibrary = asset.Target
			break
		}
	}
	if missingLibrary == "" {
		t.Fatal("retained missing-library member is not emitted")
	}
	missing := runPiPortableContributionProbe(t, contribution, piPortableContributionProbeOptions{omitTarget: missingLibrary})
	if missing.err == nil || missing.timedOut || missing.exitCode == 0 {
		t.Fatalf("missing-library recipe probe unexpectedly succeeded: %#v", missing)
	}

	photon := runPiPortableContributionProbe(t, contribution, piPortableContributionProbeOptions{photon: true, omitPhoton: true})
	if photon.err == nil || photon.timedOut || photon.exitCode != 41 || strings.TrimSpace(photon.output) != "pi-portable-photon-wasm-missing" {
		t.Fatalf("missing-Photon recipe probe did not produce the bound failure: %#v", photon)
	}
}

type piPortableRequiredPaths struct {
	piPortableRuntimePaths
	authPresent bool
}

func requirePiPortableRuntimePaths(t *testing.T) (harnesses.PortableRuntimeTarget, piPortableRequiredPaths) {
	t.Helper()
	if runtime.GOOS != piPortableGOOS || runtime.GOARCH != piPortableGOARCH {
		t.Skipf("retained Pi portable runtime is linux/arm64, host is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	launcher, packageRoot := installedPiPortableLayout(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	interpreter := installedPiPortableNode(t)
	agentDir := filepath.Join(home, piPortableAgentDirectory)
	configuration, err := inspectPiPortableConfiguration(context.Background(), agentDir, nil)
	if err != nil {
		t.Skipf("retained Pi configuration is unavailable: %v", err)
	}
	return harnesses.PortableRuntimeTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, piPortableRequiredPaths{
		piPortableRuntimePaths: piPortableRuntimePaths{launcher: launcher, packageRoot: packageRoot, interpreter: interpreter, agentDir: agentDir},
		authPresent:            configuration.authPresent,
	}
}

func assertPiPortableContributionMutations(t *testing.T, target harnesses.PortableRuntimeTarget, required piPortableRequiredPaths) {
	t.Helper()
	paths := required.piPortableRuntimePaths
	mutations := []struct {
		name    string
		mutate  func()
		restore func()
	}{
		{name: "version", mutate: func() { piPortableVerifiedRuntime.release.version = "0.51.5" }, restore: func() { piPortableVerifiedRuntime.release.version = "0.51.4" }},
		{name: "integrity", mutate: func() { piPortableVerifiedRuntime.release.integrity = "sha512-unreviewed" }, restore: func() {
			piPortableVerifiedRuntime.release.integrity = "sha512-agQJ38Hq4vjukzB1AC4Mj2lJ3H3zVBzYz4Fuyu8rvTMRAVkB1zlL+CMHF8FsNZ2+bVkKvMHZusc7nIQ1cPbf4Q=="
		}},
		{name: "architecture", mutate: func() { piPortableVerifiedRuntime.tree.goarch = "amd64" }, restore: func() { piPortableVerifiedRuntime.tree.goarch = piPortableGOARCH }},
		{name: "metadata", mutate: func() { piPortableVerifiedRuntime.release.packageName = "unreviewed-package" }, restore: func() { piPortableVerifiedRuntime.release.packageName = piPortablePackageName }},
		{name: "interpreter", mutate: func() { piPortableVerifiedRuntime.node.sha256 = strings.Repeat("0", 64) }, restore: func() {
			piPortableVerifiedRuntime.node.sha256 = "8eeefcacdf48f58541a651016e604055d14a992e39df98636b76495bc7244395"
		}},
		{name: "tree", mutate: func() { piPortableVerifiedRuntime.tree.digest = strings.Repeat("0", 64) }, restore: func() {
			piPortableVerifiedRuntime.tree.digest = "e24e2b681a84d3aa44abc3ff565d23f827f668a6e5325070f738e8a420dc4e09"
		}},
		{name: "Photon data", mutate: func() { piPortableVerifiedRuntime.data.photonSHA256 = strings.Repeat("0", 64) }, restore: func() {
			piPortableVerifiedRuntime.data.photonSHA256 = "10468181565c56004c867f3a4af96f89a0ef5a63a72f2b5fb12c1f1992a3615c"
		}},
		{name: "clipboard identity", mutate: func() { piPortableVerifiedRuntime.data.clipboardSHA256 = strings.Repeat("0", 64) }, restore: func() {
			piPortableVerifiedRuntime.data.clipboardSHA256 = "1c15a004a06c9dc5eda5ba0a7a3535203eb141b97098ca033ca49a1269f84663"
		}},
		{name: "clipboard ELF class", mutate: func() { piPortableVerifiedRuntime.data.clipboardClass = elf.ELFCLASS32 }, restore: func() {
			piPortableVerifiedRuntime.data.clipboardClass = elf.ELFCLASS64
		}},
		{name: "clipboard DT_NEEDED", mutate: func() { piPortableVerifiedRuntime.data.clipboardNeeded = []string{"libc.so.6"} }, restore: func() {
			piPortableVerifiedRuntime.data.clipboardNeeded = []string{"libgcc_s.so.1", "libpthread.so.0", "libm.so.6", "libdl.so.2", "libc.so.6"}
		}},
		{name: "Doom classification", mutate: func() { piPortableVerifiedRuntime.data.doomRelative = piPortableVerifiedRuntime.data.photonRelative }, restore: func() {
			piPortableVerifiedRuntime.data.doomRelative = "examples/extensions/doom-overlay/doom/build/doom.wasm"
		}},
	}
	for _, test := range mutations {
		t.Run("reject "+test.name+" mutation", func(t *testing.T) {
			test.mutate()
			defer test.restore()
			_, err := piPortableRuntimeAssets(context.Background(), target, paths)
			assertPiPortableRuntimeFailure(t, err, required.launcher, required.packageRoot, required.interpreter, "token-value", "secret-value")
		})
	}
	t.Run("reject selection evidence mutation", func(t *testing.T) {
		original := slices.Clone(piPortableVerifiedRuntime.data.forbiddenDisplay)
		piPortableVerifiedRuntime.data.forbiddenDisplay = []string{"DISPLAY"}
		defer func() { piPortableVerifiedRuntime.data.forbiddenDisplay = original }()
		_, err := piPortableRuntimeAssets(context.Background(), target, paths)
		assertPiPortableRuntimeFailure(t, err, required.launcher, required.packageRoot)
	})
	t.Run("reject dependency mutation", func(t *testing.T) {
		original := locatePiPortableLibraryRoot
		locatePiPortableLibraryRoot = func() (string, error) { return t.TempDir(), nil }
		defer func() { locatePiPortableLibraryRoot = original }()
		_, err := piPortableRuntimeAssets(context.Background(), target, paths)
		assertPiPortableRuntimeFailure(t, err, required.launcher, required.packageRoot)
	})
}

func piPortableAssetByTarget(t *testing.T, contribution harnesses.PortableRuntimeContribution, target string) harnesses.PortableRuntimeAsset {
	t.Helper()
	for _, asset := range contribution.Assets {
		if asset.Target == target {
			return asset
		}
	}
	t.Fatalf("asset %q is missing", target)
	return harnesses.PortableRuntimeAsset{}
}

func assertPiPortableRuntimeFailure(t *testing.T, err error, sensitive ...string) {
	t.Helper()
	if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("portable error = %v, want closure incomplete", err)
	}
	diagnostic := err.Error()
	for _, value := range sensitive {
		if value != "" && strings.Contains(diagnostic, value) {
			t.Fatalf("portable diagnostic exposed a value")
		}
	}
	for _, forbidden := range []string{"=", "token-value", "secret-value", "#!/", "sha256:", "sha512:"} {
		if strings.Contains(strings.ToLower(diagnostic), strings.ToLower(forbidden)) {
			t.Fatalf("portable diagnostic contains forbidden content")
		}
	}
}

type piPortableContributionProbeOptions struct {
	omitTarget string
	photon     bool
	omitPhoton bool
}

func runPiPortableContributionProbe(t *testing.T, contribution harnesses.PortableRuntimeContribution, options piPortableContributionProbeOptions) piPortableProbeResult {
	t.Helper()
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skipf("isolated Pi recipe probe requires bubblewrap: %v", err)
	}
	root := t.TempDir()
	for _, directory := range []string{"dev", "proc", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	arguments := []string{"--unshare-all", "--die-with-parent", "--new-session", "--ro-bind", root, "/", "--dev", "/dev", "--proc", "/proc"}
	for _, asset := range contribution.Assets {
		if asset.Target == options.omitTarget {
			continue
		}
		destination := filepath.Join(root, filepath.FromSlash(asset.Target))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if asset.PathKind == harnesses.PortableRuntimePathTree {
			if err := os.MkdirAll(destination, 0o700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(destination, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		arguments = append(arguments, "--ro-bind", asset.Source, "/"+asset.Target)
	}
	if options.omitPhoton {
		packageAsset := piPortableAssetByTarget(t, contribution, piPortablePackageTarget)
		source := filepath.Join(packageAsset.Source, "node_modules", "@silvia-odwyer", "photon-node")
		shadow := t.TempDir()
		entries, err := os.ReadDir(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() == "photon_rs_bg.wasm" || !entry.Type().IsRegular() {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(source, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(shadow, entry.Name()), contents, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		arguments = append(arguments, "--ro-bind", shadow, "/"+piPortablePackageTarget+"/node_modules/@silvia-odwyer/photon-node")
	}

	guestRoot := "/"
	var command string
	var commandArguments []string
	if options.photon {
		command = "/" + contribution.Launch.LoaderTarget
		commandArguments = []string{
			"--library-path", "/" + contribution.Launch.LibraryRootTargets[0],
			"/" + contribution.Launch.InterpreterTarget,
			"--input-type=module", "--eval",
			`const {loadPhoton}=await import("file:///` + piPortablePackageTarget + `/dist/utils/photon.js"); const photon=await loadPhoton(); if (!photon || typeof photon.PhotonImage !== "function") { console.error("pi-portable-photon-wasm-missing"); process.exit(41); } process.stdout.write("pi-portable-photon-ok");`,
		}
	} else {
		command, commandArguments, err = harnesses.BuildPortableRuntimeLaunchCommand(guestRoot, contribution, []string{"--version"})
		if err != nil {
			t.Fatal(err)
		}
	}
	arguments = append(arguments, command)
	arguments = append(arguments, commandArguments...)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, bwrap, arguments...)
	probe.Env = []string{"HOME=/nonexistent", "LANG=C", "LC_ALL=C", "PATH=/nonexistent"}
	output, err := probe.CombinedOutput()
	result := piPortableProbeResult{output: string(output), exitCode: -1, timedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), err: err}
	if err == nil {
		result.exitCode = 0
	} else {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.exitCode = exitError.ExitCode()
		}
	}
	return result
}
