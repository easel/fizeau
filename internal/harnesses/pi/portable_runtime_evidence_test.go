package pi

import (
	"context"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestPiPortableRuntimeReleaseEvidence(t *testing.T) {
	want := piPortableVerifiedRuntime.release
	if want.packageName != "@mariozechner/pi-coding-agent" || want.version != "0.51.4" ||
		want.integrity != "sha512-agQJ38Hq4vjukzB1AC4Mj2lJ3H3zVBzYz4Fuyu8rvTMRAVkB1zlL+CMHF8FsNZ2+bVkKvMHZusc7nIQ1cPbf4Q==" ||
		want.shasum != "025749df96513e9d328f3c501bdd37ac7e878fe4" ||
		want.signatureKeyID != "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U" ||
		want.launcherLink != "../lib/node_modules/@mariozechner/pi-coding-agent/dist/cli.js" ||
		want.binName != "pi" || want.binRelative != "dist/cli.js" ||
		want.launcherSHA256 != "34277c76b394762bc1711e859e4b86caf45ac92a85c1b8894671aa584e53a27a" {
		t.Fatalf("Pi release evidence is incomplete: %#v", want)
	}

	t.Run("installed global npm layout", func(t *testing.T) {
		launcher, packageRoot := installedPiPortableLayout(t)
		prefix := filepath.Dir(filepath.Dir(launcher))
		if packageRoot != filepath.Join(prefix, filepath.FromSlash(want.packageRelative)) {
			t.Fatal("installed Pi launcher does not match the reviewed global npm layout")
		}
	})

	t.Run("synthetic installed layout fails closed", func(t *testing.T) {
		t.Run("exact layout", func(t *testing.T) {
			launcher, evidence := seedPiPortableReleaseFixture(t)
			if _, err := inspectPiPortableInstalledRelease(launcher, evidence); err != nil {
				t.Fatalf("exact synthetic layout rejected: %v", err)
			}
		})
		for _, test := range []struct {
			name   string
			mutate func(*testing.T, string, string)
		}{
			{name: "wrong version", mutate: func(t *testing.T, _, packageRoot string) {
				writePiPortablePackageMetadata(t, packageRoot, piPortablePackageName, "0.51.5", map[string]string{"pi": "dist/cli.js"})
			}},
			{name: "wrong package name", mutate: func(t *testing.T, _, packageRoot string) {
				writePiPortablePackageMetadata(t, packageRoot, "unreviewed-package", piPortableVersion, map[string]string{"pi": "dist/cli.js"})
			}},
			{name: "missing metadata", mutate: func(t *testing.T, _, packageRoot string) {
				if err := os.Remove(filepath.Join(packageRoot, "package.json")); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "unreadable metadata", mutate: func(t *testing.T, _, packageRoot string) {
				metadata := filepath.Join(packageRoot, "package.json")
				if err := os.Remove(metadata); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(metadata, 0o700); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "invalid metadata", mutate: func(t *testing.T, _, packageRoot string) {
				if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "extra bin metadata", mutate: func(t *testing.T, _, packageRoot string) {
				writePiPortablePackageMetadata(t, packageRoot, piPortablePackageName, piPortableVersion, map[string]string{"pi": "dist/cli.js", "other": "dist/other.js"})
			}},
			{name: "wrong symlink", mutate: func(t *testing.T, launcher, _ string) {
				if err := os.Remove(launcher); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../unreviewed/cli.js", launcher); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				launcher, evidence := seedPiPortableReleaseFixture(t)
				packageRoot := filepath.Join(filepath.Dir(filepath.Dir(launcher)), filepath.FromSlash(evidence.packageRelative))
				test.mutate(t, launcher, packageRoot)
				assertPiPortableEvidenceFailure(t, mustFailPiPortableLayout(t, launcher, evidence), "installed layout")
			})
		}
		t.Run("arbitrary directory", func(t *testing.T) {
			launcher, evidence := seedPiPortableReleaseFixture(t)
			arbitrary := filepath.Join(filepath.Dir(filepath.Dir(launcher)), "arbitrary", "pi")
			if err := os.MkdirAll(filepath.Dir(arbitrary), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(evidence.launcherLink, arbitrary); err != nil {
				t.Fatal(err)
			}
			assertPiPortableEvidenceFailure(t, mustFailPiPortableLayout(t, arbitrary, evidence), "installed layout")
		})
	})

	mutations := []struct {
		name   string
		mutate func(*piPortableReleaseEvidence)
	}{
		{name: "version", mutate: func(e *piPortableReleaseEvidence) { e.version = "0.51.5" }},
		{name: "integrity", mutate: func(e *piPortableReleaseEvidence) { e.integrity = "sha512-drift" }},
		{name: "shasum", mutate: func(e *piPortableReleaseEvidence) { e.shasum = strings.Repeat("0", 40) }},
		{name: "signature", mutate: func(e *piPortableReleaseEvidence) { e.signatureKeyID = "SHA256:drift" }},
		{name: "metadata", mutate: func(e *piPortableReleaseEvidence) { e.packageName = "pi-coding-agent" }},
		{name: "bin metadata", mutate: func(e *piPortableReleaseEvidence) { e.binRelative = "dist/other.js" }},
		{name: "symlink", mutate: func(e *piPortableReleaseEvidence) { e.launcherLink = "../unreviewed/cli.js" }},
		{name: "launcher", mutate: func(e *piPortableReleaseEvidence) { e.launcherSHA256 = strings.Repeat("0", 64) }},
	}
	for _, test := range mutations {
		t.Run("reject "+test.name+" drift", func(t *testing.T) {
			changed := want
			test.mutate(&changed)
			assertPiPortableEvidenceFailure(t, validatePiPortableReleaseEvidence(changed), "release")
		})
	}
}

func TestPiPortableRuntimePackageTree(t *testing.T) {
	want := piPortableVerifiedRuntime.tree
	if want.format != "fizeau-portable-tree-v1" || want.digest != "e24e2b681a84d3aa44abc3ff565d23f827f668a6e5325070f738e8a420dc4e09" ||
		want.records != 17594 || want.goos != "linux" || want.goarch != "arm64" {
		t.Fatalf("Pi package-tree evidence is incomplete: %#v", want)
	}
	t.Run("installed package tree", func(t *testing.T) {
		_, packageRoot := installedPiPortableLayout(t)
		digest, err := harnesses.PortableRuntimeTreeDigest(packageRoot)
		if err != nil {
			t.Fatal(err)
		}
		records := 0
		if err := filepath.Walk(packageRoot, func(path string, _ os.FileInfo, err error) error {
			if err == nil && path != packageRoot {
				records++
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		observed := piPortableTreeEvidence{format: want.format, digest: digest, records: records, goos: runtime.GOOS, goarch: runtime.GOARCH}
		if err := validatePiPortableTreeEvidence(observed); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*piPortableTreeEvidence)
	}{
		{name: "format", mutate: func(e *piPortableTreeEvidence) { e.format = "unversioned-tree" }},
		{name: "digest", mutate: func(e *piPortableTreeEvidence) { e.digest = strings.Repeat("0", 64) }},
		{name: "record count", mutate: func(e *piPortableTreeEvidence) { e.records-- }},
		{name: "operating system", mutate: func(e *piPortableTreeEvidence) { e.goos = "darwin" }},
		{name: "architecture", mutate: func(e *piPortableTreeEvidence) { e.goarch = "amd64" }},
	} {
		t.Run("reject "+test.name+" drift", func(t *testing.T) {
			changed := want
			test.mutate(&changed)
			assertPiPortableEvidenceFailure(t, validatePiPortableTreeEvidence(changed), "package tree")
		})
	}
}

func TestPiPortableRuntimeNodePairing(t *testing.T) {
	want := piPortableVerifiedRuntime.node
	if want.version != "22.22.0" || want.size != 120592136 ||
		want.sha256 != "8eeefcacdf48f58541a651016e604055d14a992e39df98636b76495bc7244395" ||
		want.buildID != "c917b99f70bd51f3f5f37c6fa71bdea3534e192c" ||
		want.interpreter != "/lib/ld-linux-aarch64.so.1" || want.rejectedBrew != "26.5.0" {
		t.Fatalf("Pi Node evidence is incomplete: %#v", want)
	}
	t.Run("separately sourced Node", func(t *testing.T) {
		node := installedPiPortableNode(t)
		observed := inspectPiPortableNode(t, node)
		if err := validatePiPortableNodeEvidence(observed); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*piPortableNodeEvidence)
	}{
		{name: "version", mutate: func(e *piPortableNodeEvidence) { e.version = "22.21.1" }},
		{name: "size", mutate: func(e *piPortableNodeEvidence) { e.size-- }},
		{name: "digest", mutate: func(e *piPortableNodeEvidence) { e.sha256 = strings.Repeat("0", 64) }},
		{name: "build ID", mutate: func(e *piPortableNodeEvidence) { e.buildID = strings.Repeat("0", 40) }},
		{name: "interpreter", mutate: func(e *piPortableNodeEvidence) { e.interpreter = "/unreviewed/loader" }},
	} {
		t.Run("reject "+test.name+" drift", func(t *testing.T) {
			changed := want
			test.mutate(&changed)
			assertPiPortableEvidenceFailure(t, validatePiPortableNodeEvidence(changed), "interpreter")
		})
	}

	t.Run("reject Homebrew Node", func(t *testing.T) {
		brewNode := filepath.Join("/home/linuxbrew/.linuxbrew/Cellar/node", want.rejectedBrew, "bin", "node")
		if _, err := os.Stat(brewNode); errors.Is(err, os.ErrNotExist) {
			t.Skip("reviewed Homebrew Node negative is not installed")
		} else if err != nil {
			t.Fatal(err)
		}
		file, err := elf.Open(brewNode)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		rpath, _ := file.DynString(elf.DT_RPATH)
		if len(rpath) == 0 || !strings.Contains(rpath[0], "/home/linuxbrew/.linuxbrew/Cellar/node/"+want.rejectedBrew) {
			t.Fatalf("Homebrew Node %s no longer supplies the reviewed absolute-RPATH negative: %q", want.rejectedBrew, rpath)
		}
	})
}

func TestPiPortableRuntimeDataSelection(t *testing.T) {
	want := piPortableVerifiedRuntime.data
	if want.photonSHA256 != "10468181565c56004c867f3a4af96f89a0ef5a63a72f2b5fb12c1f1992a3615c" ||
		want.clipboardSHA256 != "1c15a004a06c9dc5eda5ba0a7a3535203eb141b97098ca033ca49a1269f84663" ||
		want.clipboardClass != elf.ELFCLASS64 ||
		!slices.Equal(want.clipboardNeeded, []string{"libgcc_s.so.1", "libpthread.so.0", "libm.so.6", "libdl.so.2", "libc.so.6"}) ||
		!slices.Equal(want.forbiddenDisplay, []string{"DISPLAY", "WAYLAND_DISPLAY"}) {
		t.Fatalf("Pi runtime-data evidence is incomplete: %#v", want)
	}
	for _, test := range []struct {
		name   string
		mutate func(*piPortableDataEvidence)
	}{
		{name: "Photon path", mutate: func(e *piPortableDataEvidence) { e.photonRelative = "unreviewed.wasm" }},
		{name: "Photon digest", mutate: func(e *piPortableDataEvidence) { e.photonSHA256 = strings.Repeat("0", 64) }},
		{name: "Doom classification", mutate: func(e *piPortableDataEvidence) { e.doomRelative = e.photonRelative }},
		{name: "clipboard digest", mutate: func(e *piPortableDataEvidence) { e.clipboardSHA256 = strings.Repeat("0", 64) }},
		{name: "clipboard ELF class", mutate: func(e *piPortableDataEvidence) { e.clipboardClass = elf.ELFCLASS32 }},
		{name: "clipboard dependencies", mutate: func(e *piPortableDataEvidence) { e.clipboardNeeded = e.clipboardNeeded[:len(e.clipboardNeeded)-1] }},
		{name: "display boundary", mutate: func(e *piPortableDataEvidence) { e.forbiddenDisplay = []string{"DISPLAY"} }},
	} {
		t.Run("reject "+test.name+" drift", func(t *testing.T) {
			changed := want
			changed.clipboardNeeded = slices.Clone(want.clipboardNeeded)
			changed.forbiddenDisplay = slices.Clone(want.forbiddenDisplay)
			test.mutate(&changed)
			assertPiPortableEvidenceFailure(t, validatePiPortableDataEvidence(changed), "runtime data")
		})
	}

	t.Run("installed selection and isolated probes", func(t *testing.T) {
		_, packageRoot := installedPiPortableLayout(t)
		observed := want
		observed.photonSize, observed.photonSHA256 = piPortableFileEvidence(t, filepath.Join(packageRoot, filepath.FromSlash(want.photonRelative)))
		_, observed.doomSHA256 = piPortableFileEvidence(t, filepath.Join(packageRoot, filepath.FromSlash(want.doomRelative)))
		observed.clipboardSize, observed.clipboardSHA256 = piPortableFileEvidence(t, filepath.Join(packageRoot, filepath.FromSlash(want.clipboardRelative)))
		clipboard, err := elf.Open(filepath.Join(packageRoot, filepath.FromSlash(want.clipboardRelative)))
		if err != nil {
			t.Fatal(err)
		}
		if clipboard.Machine != elf.EM_AARCH64 {
			t.Fatalf("clipboard architecture = %v", clipboard.Machine)
		}
		observed.clipboardClass = clipboard.Class
		observed.clipboardNeeded, err = clipboard.ImportedLibraries()
		if err != nil {
			_ = clipboard.Close()
			t.Fatal(err)
		}
		rpath, _ := clipboard.DynString(elf.DT_RPATH)
		runpath, _ := clipboard.DynString(elf.DT_RUNPATH)
		if err := clipboard.Close(); err != nil {
			t.Fatal(err)
		}
		if len(rpath) != 0 || len(runpath) != 0 {
			t.Fatalf("clipboard addon has RPATH/RUNPATH: %q %q", rpath, runpath)
		}
		if err := validatePiPortableDataEvidence(observed); err != nil {
			t.Fatal(err)
		}

		reachable := piPortableReachableJavaScript(t, packageRoot, "dist/cli.js")
		for _, required := range []string{"dist/main.js", "dist/modes/interactive/interactive-mode.js", "dist/utils/clipboard-image.js", "dist/utils/clipboard-native.js", "dist/utils/photon.js"} {
			if !reachable[required] {
				t.Errorf("reviewed Pi JavaScript graph does not reach %q", required)
			}
		}
		if reachable[want.doomRelative] {
			t.Fatal("example-only Doom WASM entered the launcher JavaScript graph")
		}
		for source := range reachable {
			data, err := os.ReadFile(filepath.Join(packageRoot, filepath.FromSlash(source)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "doom.wasm") || strings.Contains(string(data), want.doomRelative) {
				t.Fatalf("reachable Pi source %q references example-only Doom WASM", source)
			}
		}
		assertPiPortableDataCodePaths(t, packageRoot)
		assertPiPortableDoomExampleUnreachable(t, packageRoot)

		node := installedPiPortableNode(t)
		t.Run("isolated positive", func(t *testing.T) {
			result := runPiPortableProbe(t, node, packageRoot, piPortableProbeOptions{})
			if result.err != nil || result.timedOut || result.exitCode != 0 || strings.TrimSpace(result.output) != piPortableVersion {
				t.Fatalf("isolated Pi launcher probe = %#v", result)
			}
			result = runPiPortableProbe(t, node, packageRoot, piPortableProbeOptions{photon: true})
			if result.err != nil || result.timedOut || result.exitCode != 0 || strings.TrimSpace(result.output) != "pi-portable-photon-ok" {
				t.Fatalf("isolated Pi Photon probe = %#v", result)
			}
		})
		t.Run("missing interpreter library", func(t *testing.T) {
			result := runPiPortableProbe(t, node, packageRoot, piPortableProbeOptions{omitLibrary: "libstdc++.so.6"})
			if result.err == nil || result.timedOut || result.exitCode == 0 ||
				!strings.Contains(result.output, "error while loading shared libraries") || !strings.Contains(result.output, "libstdc++.so.6") {
				t.Fatalf("missing-library probe did not produce the bound loader failure: %#v", result)
			}
		})
		t.Run("missing Photon", func(t *testing.T) {
			result := runPiPortableProbe(t, node, packageRoot, piPortableProbeOptions{photon: true, omitPhoton: true})
			if result.err == nil || result.timedOut || result.exitCode != 41 || strings.TrimSpace(result.output) != "pi-portable-photon-wasm-missing" {
				t.Fatalf("missing-Photon probe did not produce the intentional WASM failure: %#v", result)
			}
		})
	})
}

func installedPiPortableLayout(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS != piPortableGOOS || runtime.GOARCH != piPortableGOARCH {
		t.Skip("reviewed Pi portable evidence is Linux arm64 only")
	}
	launcher, err := exec.LookPath("pi")
	if errors.Is(err, exec.ErrNotFound) {
		t.Skipf("reviewed Pi release is not installed: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	launcher, err = filepath.Abs(launcher)
	if err != nil {
		t.Fatal(err)
	}
	packageRoot, err := inspectPiPortableInstalledRelease(launcher, piPortableVerifiedRuntime.release)
	if err != nil {
		t.Fatal(err)
	}
	return launcher, packageRoot
}

func seedPiPortableReleaseFixture(t *testing.T) (string, piPortableReleaseEvidence) {
	t.Helper()
	prefix := t.TempDir()
	packageRoot := filepath.Join(prefix, filepath.FromSlash(piPortableVerifiedRuntime.release.packageRelative))
	launcherPath := filepath.Join(packageRoot, "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcherPath, []byte("#!/usr/bin/env node\nfixture\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePiPortablePackageMetadata(t, packageRoot, piPortablePackageName, piPortableVersion, map[string]string{"pi": "dist/cli.js"})
	bin := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bin, "pi")
	if err := os.Symlink(piPortableVerifiedRuntime.release.launcherLink, launcher); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	evidence := piPortableVerifiedRuntime.release
	evidence.launcherSize = info.Size()
	evidence.launcherSHA256 = digest
	return launcher, evidence
}

func writePiPortablePackageMetadata(t *testing.T, packageRoot, name, version string, bin map[string]string) {
	t.Helper()
	if err := os.MkdirAll(packageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(piPortablePackageMetadata{Name: name, Version: version, Bin: bin})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustFailPiPortableLayout(t *testing.T, launcher string, evidence piPortableReleaseEvidence) error {
	t.Helper()
	if _, err := inspectPiPortableInstalledRelease(launcher, evidence); err != nil {
		return err
	}
	t.Fatal("unreviewed Pi installed layout was accepted")
	return nil
}

func installedPiPortableNode(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != piPortableGOOS || runtime.GOARCH != piPortableGOARCH {
		t.Skip("reviewed Pi portable Node is Linux arm64 only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("user home is unavailable: %v", err)
	}
	node := filepath.Join(home, ".local", "share", "mise", "installs", "node", piPortableVerifiedRuntime.node.version, "bin", "node")
	if _, err := os.Stat(node); errors.Is(err, os.ErrNotExist) {
		t.Skip("reviewed separately sourced Node 22.22.0 is not installed")
	} else if err != nil {
		t.Fatal(err)
	}
	return node
}

func inspectPiPortableNode(t *testing.T, node string) piPortableNodeEvidence {
	t.Helper()
	info, err := os.Stat(node)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(node)
	if err != nil {
		t.Fatal(err)
	}
	file, err := elf.Open(node)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if file.Machine != elf.EM_AARCH64 {
		t.Fatalf("Node architecture = %v, want AArch64", file.Machine)
	}
	rpath, _ := file.DynString(elf.DT_RPATH)
	runpath, _ := file.DynString(elf.DT_RUNPATH)
	if len(rpath) != 0 || len(runpath) != 0 {
		t.Fatalf("separately sourced Node has RPATH/RUNPATH: %q %q", rpath, runpath)
	}
	versionOutput, err := exec.Command(node, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("read Node version: %v: %q", err, versionOutput)
	}
	return piPortableNodeEvidence{
		version: strings.TrimPrefix(strings.TrimSpace(string(versionOutput)), "v"), size: info.Size(), sha256: digest,
		buildID: piPortableGNUBuildID(t, file), interpreter: piPortableELFInterpreter(t, file),
		rejectedBrew: piPortableVerifiedRuntime.node.rejectedBrew,
	}
}

func piPortableELFInterpreter(t *testing.T, file *elf.File) string {
	t.Helper()
	for _, program := range file.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		data := make([]byte, program.Filesz)
		if _, err := program.ReadAt(data, 0); err != nil {
			t.Fatal(err)
		}
		return strings.TrimRight(string(data), "\x00")
	}
	t.Fatal("ELF has no PT_INTERP")
	return ""
}

func piPortableGNUBuildID(t *testing.T, file *elf.File) string {
	t.Helper()
	for _, section := range file.Sections {
		if section.Type != elf.SHT_NOTE {
			continue
		}
		data, err := section.Data()
		if err != nil {
			t.Fatal(err)
		}
		for len(data) >= 12 {
			namesz := int(file.ByteOrder.Uint32(data[0:4]))
			descsz := int(file.ByteOrder.Uint32(data[4:8]))
			typeID := file.ByteOrder.Uint32(data[8:12])
			nameEnd := 12 + namesz
			descStart := 12 + alignPiPortableNote(namesz)
			descEnd := descStart + descsz
			next := descStart + alignPiPortableNote(descsz)
			if nameEnd > len(data) || descEnd > len(data) || next > len(data) {
				break
			}
			name := strings.TrimRight(string(data[12:nameEnd]), "\x00")
			if name == "GNU" && typeID == 3 {
				return hex.EncodeToString(data[descStart:descEnd])
			}
			data = data[next:]
		}
	}
	t.Fatal("ELF has no GNU build ID")
	return ""
}

func alignPiPortableNote(size int) int { return (size + 3) &^ 3 }

func piPortableFileEvidence(t *testing.T, path string) (int64, string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size(), digest
}

var piPortableJSImport = regexp.MustCompile(`(?:\bfrom\s+|\bimport\s*\(\s*|\bimport\s+|\brequire\s*\(\s*)["']([^"']+)["']`)

func piPortableReachableJavaScript(t *testing.T, packageRoot, entry string) map[string]bool {
	t.Helper()
	reachable := map[string]bool{}
	queue := []string{entry}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		if reachable[current] {
			continue
		}
		reachable[current] = true
		data, err := os.ReadFile(filepath.Join(packageRoot, filepath.FromSlash(current)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range piPortableJSImport.FindAllSubmatch(data, -1) {
			specifier := string(match[1])
			if !strings.HasPrefix(specifier, ".") {
				continue
			}
			next := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(current), filepath.FromSlash(specifier))))
			if filepath.Ext(next) == "" {
				next += ".js"
			}
			if next == ".." || strings.HasPrefix(next, "../") {
				t.Fatal("JavaScript import escaped the reviewed package tree")
			}
			if _, err := os.Stat(filepath.Join(packageRoot, filepath.FromSlash(next))); err == nil {
				queue = append(queue, next)
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
	}
	return reachable
}

func assertPiPortableDataCodePaths(t *testing.T, packageRoot string) {
	t.Helper()
	read := func(relative string) string {
		data, err := os.ReadFile(filepath.Join(packageRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	photon := read("dist/utils/photon.js")
	if !strings.Contains(photon, `import("@silvia-odwyer/photon-node")`) || !strings.Contains(photon, `WASM_FILENAME = "photon_rs_bg.wasm"`) {
		t.Fatal("Pi Photon wrapper no longer selects the reviewed package data")
	}
	photonPackage := read("node_modules/@silvia-odwyer/photon-node/package.json")
	if !strings.Contains(photonPackage, `"main": "photon_rs.js"`) || !strings.Contains(read("node_modules/@silvia-odwyer/photon-node/photon_rs.js"), "readFileSync(path)") {
		t.Fatal("Photon package no longer reads the reviewed WASM member")
	}
	clipboard := read("dist/utils/clipboard-native.js")
	if !strings.Contains(clipboard, `process.env.DISPLAY || process.env.WAYLAND_DISPLAY`) ||
		!strings.Contains(clipboard, `require("@mariozechner/clipboard")`) {
		t.Fatal("Pi clipboard addon is no longer guarded by the reviewed display boundary")
	}
	args := read("dist/cli/args.js")
	main := read("dist/main.js")
	resources := read("dist/core/resource-loader.js")
	extensions := read("dist/core/extensions/loader.js")
	if !strings.Contains(args, `arg === "--no-extensions"`) ||
		!strings.Contains(main, `additionalExtensionPaths: firstPass.extensions`) ||
		!strings.Contains(main, `noExtensions: firstPass.noExtensions`) ||
		!strings.Contains(resources, `const extensionPaths = this.noExtensions`) ||
		!strings.Contains(resources, `? cliEnabledExtensions`) ||
		!strings.Contains(extensions, `jiti.import(extensionPath`) {
		t.Fatal("Pi extension selection no longer proves that examples require an explicit registered path")
	}
}

func assertPiPortableDoomExampleUnreachable(t *testing.T, packageRoot string) {
	t.Helper()
	doomRoot := filepath.Join(packageRoot, "examples", "extensions", "doom-overlay")
	if info, err := os.Stat(doomRoot); err != nil || !info.IsDir() {
		t.Fatal("reviewed example-only Doom extension is unavailable")
	}
	needles := []string{"examples/extensions/doom-overlay", "doom-overlay/doom", "doom/build/doom.wasm"}
	if err := filepath.Walk(packageRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == doomRoot {
			return filepath.SkipDir
		}
		if !info.Mode().IsRegular() || (filepath.Ext(path) != ".js" && filepath.Ext(path) != ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range needles {
			if strings.Contains(string(data), needle) {
				return errors.New("non-example package source registers the Doom extension")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type piPortableProbeOptions struct {
	photon      bool
	omitPhoton  bool
	omitLibrary string
}

type piPortableProbeResult struct {
	output   string
	exitCode int
	timedOut bool
	err      error
}

func runPiPortableProbe(t *testing.T, node, packageRoot string, options piPortableProbeOptions) piPortableProbeResult {
	t.Helper()
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skipf("isolated Pi evidence probe requires bubblewrap: %v", err)
	}
	root := t.TempDir()
	for _, directory := range []string{"runtime/bin", "runtime/lib", "runtime/package", "dev", "proc"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	libraries := piPortableNodeLibrarySources(t)
	arguments := []string{"--unshare-all", "--die-with-parent", "--new-session", "--ro-bind", root, "/", "--dev", "/dev", "--proc", "/proc"}
	bindFile := func(source, target string) {
		hostTarget := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(target, "/")))
		if err := os.WriteFile(hostTarget, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		arguments = append(arguments, "--ro-bind", source, target)
	}
	bindFile(node, "/runtime/bin/node")
	for _, name := range []string{"ld-linux-aarch64.so.1", "libdl.so.2", "libstdc++.so.6", "libm.so.6", "libgcc_s.so.1", "libpthread.so.0", "libc.so.6"} {
		if name == options.omitLibrary {
			continue
		}
		bindFile(libraries[name], "/runtime/lib/"+name)
	}
	arguments = append(arguments, "--ro-bind", packageRoot, "/runtime/package")
	if options.omitPhoton {
		shadow := t.TempDir()
		photonRoot := filepath.Join(packageRoot, "node_modules", "@silvia-odwyer", "photon-node")
		entries, err := os.ReadDir(photonRoot)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() == "photon_rs_bg.wasm" || !entry.Type().IsRegular() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(photonRoot, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(shadow, entry.Name()), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		arguments = append(arguments, "--ro-bind", shadow, "/runtime/package/node_modules/@silvia-odwyer/photon-node")
	}
	arguments = append(arguments, "/runtime/lib/ld-linux-aarch64.so.1", "--library-path", "/runtime/lib", "/runtime/bin/node")
	if options.photon {
		arguments = append(arguments, "--input-type=module", "--eval", `const {loadPhoton}=await import("file:///runtime/package/dist/utils/photon.js"); const photon=await loadPhoton(); if (!photon || typeof photon.PhotonImage !== "function") { console.error("pi-portable-photon-wasm-missing"); process.exit(41); } process.stdout.write("pi-portable-photon-ok");`)
	} else {
		arguments = append(arguments, "/runtime/package/dist/cli.js", "--version")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func piPortableNodeLibrarySources(t *testing.T) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, name := range []string{"ld-linux-aarch64.so.1", "libdl.so.2", "libstdc++.so.6", "libm.so.6", "libgcc_s.so.1", "libpthread.so.0", "libc.so.6"} {
		for _, directory := range []string{"/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/lib"} {
			candidate := filepath.Join(directory, name)
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				continue
			}
			info, err := os.Stat(resolved)
			if err == nil && info.Mode().IsRegular() {
				result[name] = resolved
				break
			}
		}
		if result[name] == "" {
			t.Fatalf("reviewed Node library %q is unavailable", name)
		}
	}
	return result
}

func assertPiPortableEvidenceFailure(t *testing.T, err error, class string) {
	t.Helper()
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("evidence error = %v, want closure incomplete", err)
	}
	want := piPortableEvidenceError(class).Error()
	if err.Error() != want {
		t.Fatalf("evidence error = %q, want exact generic diagnostic %q", err, want)
	}
}
