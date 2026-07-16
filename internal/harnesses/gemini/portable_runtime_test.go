package gemini

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestGeminiPortableRuntimeContribution(t *testing.T) {
	target, paths := requireGeminiPortableRuntimeInstall(t)
	if geminiPortablePackageTreeSHA256 != "31adbda660d392d71583f7649dff2fc22e10d080c6701ff5849505ba0ec2a652" {
		t.Fatal("retained package-tree evidence drifted")
	}
	contribution, err := (&Runner{}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if contribution.ClosureClass != harnesses.PortableRuntimeClosureInterpreted {
		t.Fatalf("closure class = %q", contribution.ClosureClass)
	}
	if len(contribution.ExecutionConstraints.FixedOptionValues) != 1 ||
		contribution.ExecutionConstraints.FixedOptionValues[0] != (harnesses.PortableRuntimeFixedOptionValue{Option: "-e", Value: "none"}) {
		t.Fatalf("execution constraints do not retain fixed extension disablement: %#v", contribution.ExecutionConstraints)
	}
	if contribution.Launch.EntrypointTarget != geminiPortableEntrypointTarget ||
		contribution.Launch.EntrypointTreeMember != geminiPortableEntrypoint ||
		contribution.Launch.InterpreterTarget != geminiPortableInterpreterTarget ||
		contribution.Launch.LoaderTarget != geminiPortableLoaderTarget ||
		len(contribution.Launch.LibraryRootTargets) != 1 || contribution.Launch.LibraryRootTargets[0] != geminiPortableLibraryTarget {
		t.Fatalf("launch recipe = %#v", contribution.Launch)
	}
	if len(contribution.Launch.RuntimeArgs) != 0 {
		t.Fatalf("runtime args unexpectedly follow launcher shebang: %q", contribution.Launch.RuntimeArgs)
	}
	wanted := map[string]harnesses.PortableRuntimeAssetKind{
		geminiPortableInterpreterTarget: harnesses.PortableRuntimeAssetSupport,
		geminiPortableLoaderTarget:      harnesses.PortableRuntimeAssetSupport,
		geminiPortablePackageTarget:     harnesses.PortableRuntimeAssetInstallTree,
	}
	for _, asset := range contribution.Assets {
		if kind, ok := wanted[asset.Target]; ok {
			if asset.Kind != kind || asset.ContentSHA256 == "" {
				t.Fatalf("asset %q = %#v", asset.Target, asset)
			}
			delete(wanted, asset.Target)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing exact closure assets: %#v", wanted)
	}
	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand("/portable", contribution, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/portable/"+geminiPortableLoaderTarget || len(arguments) < 5 ||
		arguments[0] != "--library-path" || arguments[2] != "/portable/"+geminiPortableInterpreterTarget ||
		arguments[3] != "/portable/"+geminiPortableEntrypointTarget {
		t.Fatalf("expanded launch = %q %q", command, arguments)
	}

	assertGeminiPortableAnalyzerBoundary(t, target, paths)
	assertGeminiPortableMutationErrors(t, target, paths)
}

func TestGeminiPortableRuntimeClosureProbe(t *testing.T) {
	target, _ := requireGeminiPortableRuntimeInstall(t)
	contribution, err := (&Runner{}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	root := materializeGeminiPortableContribution(t, contribution)
	command, arguments, err := harnesses.BuildPortableRuntimeLaunchCommand(root, contribution, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := runGeminiPortableIsolated(t, root, command, arguments)
	if err != nil || strings.TrimSpace(string(output)) != geminiPortablePackageVersion {
		t.Fatalf("isolated Gemini version probe: %v: %q", err, output)
	}
	output, err = runGeminiPortableAddonProbe(t, root, contribution)
	if err != nil || strings.TrimSpace(string(output)) != "gemini-addons-ok" {
		t.Fatalf("isolated selected-addon probe: %v: %q", err, output)
	}
}

func TestGeminiPortableRuntimeMissingLibraryProbe(t *testing.T) {
	target, _ := requireGeminiPortableRuntimeInstall(t)
	contribution, err := (&Runner{}).PortableRuntimeAssets(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	root := materializeGeminiPortableContribution(t, contribution)
	var dependencies []string
	for _, asset := range contribution.Assets {
		if asset.Kind == harnesses.PortableRuntimeAssetSupport && asset.PathKind == harnesses.PortableRuntimePathFile &&
			asset.Target != geminiPortableInterpreterTarget && asset.Target != geminiPortableLoaderTarget {
			dependencies = append(dependencies, asset.Target)
		}
	}
	if len(dependencies) < 3 {
		t.Fatalf("dependency table is incomplete: %q", dependencies)
	}
	cases := 0
	for _, target := range dependencies {
		cases++
		t.Run(filepath.Base(target), func(t *testing.T) {
			source := filepath.Join(root, filepath.FromSlash(target))
			removed := source + ".missing"
			if err := os.Rename(source, removed); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Rename(removed, source) })
			if output, err := runGeminiPortableAddonProbe(t, root, contribution); err == nil {
				t.Fatalf("isolated probe succeeded with one declared dependency absent: %q", output)
			} else {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatalf("missing dependency did not cause a process exit failure")
				}
			}
			if err := os.Rename(removed, source); err != nil {
				t.Fatal(err)
			}
		})
	}
	if cases == 0 {
		t.Fatal("missing-library probe exercised no emitted dependencies")
	}
}

func assertGeminiPortableAnalyzerBoundary(t *testing.T, target harnesses.PortableRuntimeTarget, paths geminiPortableRuntimePaths) {
	t.Helper()
	original := analyzeGeminiPortableRuntime
	t.Cleanup(func() { analyzeGeminiPortableRuntime = original })
	var captured harnesses.PortableRuntimeInterpretedClosureRequest
	analyzeGeminiPortableRuntime = func(_ context.Context, _ harnesses.PortableRuntimeTarget, request harnesses.PortableRuntimeInterpretedClosureRequest) (harnesses.PortableRuntimeContribution, error) {
		captured = request
		return harnesses.PortableRuntimeContribution{}, nil
	}
	if _, err := geminiPortableRuntimeAssets(context.Background(), target, paths); err != nil {
		t.Fatal(err)
	}
	if len(captured.NativeAddons) != 2 || len(captured.PackageTrees) != 1 || captured.EntrypointSource != paths.launcher ||
		captured.EntrypointPackageTreeTarget != geminiPortablePackageTarget || captured.InterpreterSource != paths.interpreter {
		t.Fatalf("neutral analyzer request = %#v", captured)
	}
	want, _ := geminiPortableSelectedAddons("linux", "arm64")
	for i := range captured.NativeAddons {
		if captured.NativeAddons[i].PackageTreeTarget != geminiPortablePackageTarget || captured.NativeAddons[i].RelativePath != want[i] {
			t.Fatalf("addon declaration %d = %#v", i, captured.NativeAddons[i])
		}
	}
}

func assertGeminiPortableMutationErrors(t *testing.T, target harnesses.PortableRuntimeTarget, paths geminiPortableRuntimePaths) {
	t.Helper()
	original := analyzeGeminiPortableRuntime
	analyzeGeminiPortableRuntime = harnesses.AnalyzePortableRuntimeInterpretedClosure
	t.Cleanup(func() { analyzeGeminiPortableRuntime = original })
	mutations := []struct {
		name   string
		mutate func(*geminiPortableRuntimePaths)
	}{
		{"interpreter", func(p *geminiPortableRuntimePaths) { p.interpreter = paths.launcher }},
		{"launcher", func(p *geminiPortableRuntimePaths) { p.launcher = paths.interpreter }},
		{"package tree", func(p *geminiPortableRuntimePaths) { p.packageRoot = paths.interpreterRoot }},
		{"selection evidence", func(p *geminiPortableRuntimePaths) { p.packageRoot = filepath.Join(paths.packageRoot, "node_modules") }},
		{"addon", func(p *geminiPortableRuntimePaths) {
			p.packageRoot = filepath.Join(paths.packageRoot, "node_modules", "@github", "keytar")
		}},
	}
	for _, test := range mutations {
		t.Run("reject "+test.name+" mutation", func(t *testing.T) {
			changed := paths
			test.mutate(&changed)
			_, err := geminiPortableRuntimeAssets(context.Background(), target, changed)
			if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("mutation error = %v", err)
			}
			assertGeminiPortableRedacted(t, err.Error(), paths)
		})
	}
	assertTyped := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("mutation error has the wrong class")
		}
		assertGeminiPortableRedacted(t, err.Error(), paths)
	}

	t.Run("reject retained tree evidence mutation", func(t *testing.T) {
		original := geminiPortableVerifiedTreeSHA256
		geminiPortableVerifiedTreeSHA256 = strings.Repeat("0", 64)
		defer func() { geminiPortableVerifiedTreeSHA256 = original }()
		_, err := geminiPortableRuntimeAssets(context.Background(), target, paths)
		assertTyped(t, err)
	})
	t.Run("reject retained selection evidence mutation", func(t *testing.T) {
		original := selectGeminiPortableAddons
		selectGeminiPortableAddons = func(string, string) ([]string, error) {
			return []string{geminiPortableKeytarAddon, "foreign/prebuild.node"}, nil
		}
		defer func() { selectGeminiPortableAddons = original }()
		_, err := geminiPortableRuntimeAssets(context.Background(), target, paths)
		assertTyped(t, err)
	})
	t.Run("reject interpreter identity mutation", func(t *testing.T) {
		original := geminiPortableNodeEvidence
		geminiPortableNodeEvidence.contentSHA256 = strings.Repeat("0", 64)
		defer func() { geminiPortableNodeEvidence = original }()
		_, err := geminiPortableRuntimeAssets(context.Background(), target, paths)
		assertTyped(t, err)
	})
	for _, test := range []struct {
		name     string
		evidence *geminiPortableELFEvidence
	}{
		{"keytar", &geminiPortableKeytarEvidence},
		{"pty", &geminiPortablePTYEvidence},
	} {
		t.Run("reject "+test.name+" identity mutation", func(t *testing.T) {
			original := *test.evidence
			test.evidence.contentSHA256 = strings.Repeat("0", 64)
			defer func() { *test.evidence = original }()
			_, err := geminiPortableRuntimeAssets(context.Background(), target, paths)
			assertTyped(t, err)
		})
	}
	t.Run("reject dependency mutation", func(t *testing.T) {
		original := locateGeminiPortableLibraryRoot
		locateGeminiPortableLibraryRoot = func() (string, error) { return t.TempDir(), nil }
		defer func() { locateGeminiPortableLibraryRoot = original }()
		_, err := geminiPortableRuntimeAssets(context.Background(), target, paths)
		assertTyped(t, err)
	})

	foreign := "sensitive-token@example.com"
	_, err := (&Runner{Binary: filepath.Join(paths.packageInstallRoot, foreign)}).PortableRuntimeAssets(context.Background(), target)
	if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), foreign) {
		t.Fatalf("configured-launcher error is not typed and redacted: %v", err)
	}
}

func requireGeminiPortableRuntimeInstall(t *testing.T) (harnesses.PortableRuntimeTarget, geminiPortableRuntimePaths) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skipf("retained Gemini portable runtime is linux/arm64, host is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	paths := geminiPortableRuntimePathsForHome(home)
	for _, source := range []string{paths.launcher, paths.interpreter, paths.packageRoot} {
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			t.Skip("retained Gemini/Node installation is unavailable")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	return harnesses.PortableRuntimeTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, paths
}

func materializeGeminiPortableContribution(t *testing.T, contribution harnesses.PortableRuntimeContribution) string {
	t.Helper()
	root := t.TempDir()
	for _, asset := range contribution.Assets {
		destination := filepath.Join(root, filepath.FromSlash(asset.Target))
		switch asset.PathKind {
		case harnesses.PortableRuntimePathFile:
			copyGeminiPortableFile(t, asset.Source, destination, asset.Executable)
		case harnesses.PortableRuntimePathTree:
			copyGeminiPortableTree(t, asset.Source, destination)
		default:
			t.Fatalf("unknown path kind %q", asset.PathKind)
		}
	}
	return root
}

func copyGeminiPortableTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.Walk(source, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return err
			}
			return copyGeminiPortableFileError(resolved, target, info.Mode().Perm()&0o100 != 0)
		}
		return copyGeminiPortableFileError(current, target, info.Mode().Perm()&0o100 != 0)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyGeminiPortableFile(t *testing.T, source, destination string, executable bool) {
	t.Helper()
	if err := copyGeminiPortableFileError(source, destination, executable); err != nil {
		t.Fatal(err)
	}
}

func copyGeminiPortableFileError(source, destination string, executable bool) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func runGeminiPortableAddonProbe(t *testing.T, root string, contribution harnesses.PortableRuntimeContribution) ([]byte, error) {
	t.Helper()
	guest := func(target string) string { return "/" + target }
	script := `require("/` + geminiPortablePackageTarget + `/node_modules/@github/keytar");require("/` + geminiPortablePackageTarget + `/node_modules/@lydell/node-pty");process.stdout.write("gemini-addons-ok")`
	arguments := []string{"--library-path", guest(contribution.Launch.LibraryRootTargets[0]), guest(contribution.Launch.InterpreterTarget), "-e", script}
	return runGeminiPortableIsolated(t, root, filepath.Join(root, filepath.FromSlash(contribution.Launch.LoaderTarget)), arguments)
}

func runGeminiPortableIsolated(t *testing.T, root, command string, arguments []string) ([]byte, error) {
	t.Helper()
	for _, directory := range []string{"dev", "proc", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	guest := func(value string) string { return strings.ReplaceAll(value, root, "") }
	guestArguments := make([]string, len(arguments))
	for i := range arguments {
		guestArguments[i] = guest(arguments[i])
	}
	var probe *exec.Cmd
	if bwrap, err := exec.LookPath("bwrap"); err == nil {
		args := []string{"--unshare-all", "--die-with-parent", "--ro-bind", root, "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", guest(command)}
		probe = exec.Command(bwrap, append(args, guestArguments...)...)
	} else if unshare, err := exec.LookPath("unshare"); err == nil {
		args := []string{"--user", "--map-root-user", "--mount", "--pid", "--fork", "--net", "chroot", root, guest(command)}
		probe = exec.Command(unshare, append(args, guestArguments...)...)
	} else {
		t.Fatal("isolated portable-runtime probe requires bubblewrap or unshare")
	}
	probe.Env = []string{"GEMINI_CLI_NO_RELAUNCH=true", "HOME=/tmp/gemini-home", "LANG=C", "PATH=/not-used"}
	return probe.CombinedOutput()
}

func assertGeminiPortableRedacted(t *testing.T, output string, paths geminiPortableRuntimePaths) {
	t.Helper()
	for _, forbidden := range []string{paths.packageInstallRoot, paths.packageRoot, paths.launcher, paths.interpreterRoot, paths.interpreter, "@example.com", "sensitive-token"} {
		if forbidden != "" && strings.Contains(output, forbidden) {
			t.Fatalf("diagnostic contains sensitive source-derived value")
		}
	}
}
