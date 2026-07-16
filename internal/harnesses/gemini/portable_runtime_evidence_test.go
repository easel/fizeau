package gemini

import (
	"crypto/sha256"
	"crypto/sha512"
	"debug/elf"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type geminiPortableEvidencePaths struct {
	packageInstallRoot string
	packageRoot        string
	launcher           string
	interpreterRoot    string
	interpreter        string
	homebrewNode       string
	homebrewPackage    string
}

type geminiPortableObservedELF struct {
	size          int64
	contentSHA256 string
	buildID       string
	interpreter   string
	needed        []string
	rpath         []string
	runpath       []string
}

func TestGeminiPortableRuntimeReleaseEvidence(t *testing.T) {
	wantPackage := geminiPortableNPMEvidence{
		name:           "@google/gemini-cli",
		version:        "0.46.0",
		integrity:      "sha512-+HZtuuDKsL8mvOUWgK08GkrL1BQM4IplaSzxIjAM262FmJFp3Jo/zlHUq9ulRKcvx0agZ4KAzuj7jG9yIAxFBw==",
		shasum:         "dd5eb69e39327ca4ac0d57ac4a9aea19a356c89f",
		publisher:      "google-wombot",
		publisherEmail: "node-team-npm+wombot@google.com",
		signatureKeyID: "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U",
		signature:      "MEUCIQDmDoEwMeo5nRqrZdIJ3MmvoEWONgJ4sWYrn61pXKMjowIgbkhXoxOtyq644RUKA9+lXdZXWVqYL4qvBbaHfW1FJQ8=",
	}
	if !reflect.DeepEqual(geminiPortablePackageEvidence, wantPackage) {
		t.Fatalf("Gemini npm release evidence drifted:\n got: %#v\nwant: %#v", geminiPortablePackageEvidence, wantPackage)
	}
	if geminiPortableNodeArchiveSHA256 != "1bf1eb9ee63ffc4e5d324c0b9b62cf4a289f44332dfef9607cea1a0d9596ba6f" {
		t.Fatalf("Node archive SHA256 = %q", geminiPortableNodeArchiveSHA256)
	}
	wantNode := geminiPortableELFEvidence{
		size:          120592136,
		contentSHA256: "8eeefcacdf48f58541a651016e604055d14a992e39df98636b76495bc7244395",
		buildID:       "c917b99f70bd51f3f5f37c6fa71bdea3534e192c",
		interpreter:   "/lib/ld-linux-aarch64.so.1",
	}
	if !reflect.DeepEqual(geminiPortableNodeEvidence, wantNode) {
		t.Fatalf("Node release evidence drifted:\n got: %#v\nwant: %#v", geminiPortableNodeEvidence, wantNode)
	}
	assertGeminiCachedNPMTarball(t, geminiPortablePackageEvidence)
	if archive := os.Getenv("FIZEAU_GEMINI_PORTABLE_EVIDENCE_NODE_ARCHIVE"); archive != "" {
		assertGeminiFileSHA256(t, archive, geminiPortableNodeArchiveSHA256)
	} else {
		t.Log("Node 22.22.0 archive bytes not supplied; pinned archive SHA256 remains statically asserted")
	}

	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Logf("live Node ELF evidence is unavailable on %s/%s; static release assertions passed", runtime.GOOS, runtime.GOARCH)
		return
	}
	paths := localGeminiPortableEvidencePaths(t)
	if _, err := os.Stat(paths.interpreter); errors.Is(err, os.ErrNotExist) {
		t.Log("reviewed Mise Node 22.22.0 binary is not installed; static release assertions passed")
		return
	} else if err != nil {
		t.Fatal(err)
	}
	observed := inspectGeminiPortableELF(t, paths.interpreter)
	assertGeminiPortableELFEvidence(t, observed, geminiPortableNodeEvidence)
	if len(observed.rpath) != 0 || len(observed.runpath) != 0 {
		t.Fatalf("clean Node has RPATH/RUNPATH: rpath=%q runpath=%q", observed.rpath, observed.runpath)
	}

	t.Run("Homebrew Node is an absolute-RPATH negative", func(t *testing.T) {
		if _, err := os.Stat(paths.homebrewNode); errors.Is(err, os.ErrNotExist) {
			t.Skip("Homebrew Node 26.5.0 negative fixture is not installed")
		} else if err != nil {
			t.Fatal(err)
		}
		observed := inspectGeminiPortableELF(t, paths.homebrewNode)
		if len(observed.rpath) == 0 || !containsGeminiAbsoluteRPATH(observed.rpath) {
			t.Fatalf("Homebrew Node %s RPATH = %q, want an absolute Homebrew path", geminiPortableHomebrewNodeVersion, observed.rpath)
		}
	})
}

func TestGeminiPortableRuntimePackageLayout(t *testing.T) {
	if geminiPortablePackageVersion != "0.46.0" || geminiPortablePackageNodeVersion != "22.21.1" || geminiPortableNodeVersion != "22.22.0" ||
		geminiPortableLauncherRelative != "bin/gemini" ||
		geminiPortableLauncherLink != "../lib/node_modules/@google/gemini-cli/bundle/gemini.js" ||
		geminiPortablePackageRelative != "lib/node_modules/@google/gemini-cli" ||
		geminiPortableEntrypoint != "bundle/gemini.js" {
		t.Fatal("Gemini package layout evidence drifted")
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		paths := localGeminiPortableEvidencePaths(t)
		if _, err := os.Lstat(paths.launcher); err == nil {
			if err := validateGeminiPortablePackageLayout(paths); err != nil {
				t.Fatalf("installed Gemini package layout: %v", err)
			}
		} else if errors.Is(err, os.ErrNotExist) {
			t.Log("reviewed Mise Gemini package is not installed; synthetic fail-closed layout assertions remain active")
		} else {
			t.Fatal(err)
		}
	} else {
		t.Logf("live Gemini package layout is unavailable on %s/%s; synthetic fail-closed assertions remain active", runtime.GOOS, runtime.GOARCH)
	}

	t.Run("fail closed on layout drift", func(t *testing.T) {
		t.Run("launcher symlink", func(t *testing.T) {
			paths := seedGeminiPortablePackageLayout(t)
			requireValidGeminiPortablePackageLayout(t, paths)
			if err := os.Remove(paths.launcher); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../lib/node_modules/@google/gemini-cli/gemini.js", paths.launcher); err != nil {
				t.Fatal(err)
			}
			assertGeminiPortablePackageLayoutError(t, paths, "Gemini launcher target drifted")
		})

		t.Run("package version", func(t *testing.T) {
			paths := seedGeminiPortablePackageLayout(t)
			requireValidGeminiPortablePackageLayout(t, paths)
			writeGeminiPortableTestFile(t, filepath.Join(paths.packageRoot, "package.json"), []byte(`{"name":"@google/gemini-cli","version":"0.46.1","bin":{"gemini":"bundle/gemini.js"}}`), 0o600)
			assertGeminiPortablePackageLayoutError(t, paths, "Gemini package metadata drifted")
		})

		t.Run("separate package and interpreter sources", func(t *testing.T) {
			paths := seedGeminiPortablePackageLayout(t)
			requireValidGeminiPortablePackageLayout(t, paths)
			paths.interpreterRoot = paths.packageInstallRoot
			paths.interpreter = filepath.Join(paths.interpreterRoot, "bin", "node")
			writeGeminiPortableTestFile(t, paths.interpreter, []byte("not the reviewed interpreter"), 0o700)
			assertGeminiPortablePackageLayoutError(t, paths, "package and interpreter sources are not separate")
		})
	})
}

func TestGeminiPortableRuntimeAddonSelection(t *testing.T) {
	wantRegistry := []geminiPortableNPMEvidence{
		{
			name: "@github/keytar", version: "7.10.6",
			integrity: "sha512-mRW6cUsSG+nj4jp5gp8e91zPySaT73r+2JM6VyMZfrEgksjPmjSMr+tPGNOK3HUHV+GUU9B1LAiiYy/wmAnIxA==", shasum: "528f2c9f8c55a58e38ca271288cc59a2d7aec269",
			publisher: "GitHub Actions", publisherEmail: "npm-oidc-no-reply@github.com", signatureKeyID: "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U",
			signature: "MEYCIQCP64Xw5qxgoqps5j8RE6zfggmMSLK7W6+HbycuEMbEwAIhAMqVPorro4ddiEzGlJ0KJbvZfNmT8bcl9nv6Qwa/XUzw",
		},
		{
			name: "@lydell/node-pty", version: "1.1.0",
			integrity: "sha512-VDD8LtlMTOrPKWMXUAcB9+LTktzuunqrMwkYR1DMRBkS6LQrCt+0/Ws1o2rMml/n3guePpS7cxhHF7Nm5K4iMw==", shasum: "a04715b19078692e0dabf5d6e4bff9e75826a22b",
			publisher: "lydell", publisherEmail: "simon.lydell@gmail.com", signatureKeyID: "SHA256:jl3bwswu80PjjokCgh0o2w5c2U4LhQAE57gj9cz1kzA",
			signature: "MEQCIFth7pIgC3jem8Gi9rQYvNJKDeMsdHkAoGSI1qYK0k7PAiBDaqZPZcn6durcR+/xYL0QVzLI2DqFEw/UAUBNQRx71w==",
		},
		{
			name: "@lydell/node-pty-linux-arm64", version: "1.1.0",
			integrity: "sha512-yyDBmalCfHpLiQMT2zyLcqL2Fay4Xy7rIs8GH4dqKLnEviMvPGOK7LADVkKAsbsyXBSISL3Lt1m1MtxhPH6ckg==", shasum: "a6f8b063d558bc2f4044ee900aef6a6a6bff22f0",
			publisher: "lydell", publisherEmail: "simon.lydell@gmail.com", signatureKeyID: "SHA256:jl3bwswu80PjjokCgh0o2w5c2U4LhQAE57gj9cz1kzA",
			signature: "MEQCID4G+O0r6Bytc2XeG1VGFTux7i0aCwI+awT2LrQYjvjHAiBOD+XBXjp2yuCw23VKqITsX1nKX7DHlKJUO3ko50uFBw==",
		},
	}
	gotRegistry := []geminiPortableNPMEvidence{geminiPortableKeytarPackageEvidence, geminiPortablePTYPackageEvidence, geminiPortablePTYLinuxARM64Evidence}
	if !reflect.DeepEqual(gotRegistry, wantRegistry) {
		t.Fatalf("addon registry evidence drifted:\n got: %#v\nwant: %#v", gotRegistry, wantRegistry)
	}
	for _, evidence := range gotRegistry {
		assertGeminiCachedNPMTarball(t, evidence)
	}

	wantKeytar := geminiPortableELFEvidence{
		size: 134032, contentSHA256: "8f0f32c5d576a0987e294b8dc9f1909133504f4fe20b65888bca0dd3bfaec29c", buildID: "27ccb7ef5a2802fa0d22398f6142bae75b0ab34e",
		needed: []string{"libsecret-1.so.0", "libglib-2.0.so.0", "libstdc++.so.6", "libgcc_s.so.1", "libc.so.6"},
	}
	wantPTY := geminiPortableELFEvidence{
		size: 69064, contentSHA256: "c192e560428e778842fdbbe72f14032824b03373ed49ad3efcdbcf9eb249b75b", buildID: "70823a0341e4d895de4bce9fa49d656b68916cd1",
		needed: []string{"libstdc++.so.6", "libgcc_s.so.1", "libc.so.6", "ld-linux-aarch64.so.1"},
	}
	if !reflect.DeepEqual(geminiPortableKeytarEvidence, wantKeytar) || !reflect.DeepEqual(geminiPortablePTYEvidence, wantPTY) {
		t.Fatal("selected addon ELF evidence drifted")
	}
	selected, err := geminiPortableSelectedAddons("linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	wantSelected := []string{
		"node_modules/@github/keytar/build/Release/keytar.node",
		"node_modules/@lydell/node-pty-linux-arm64/pty.node",
	}
	if !reflect.DeepEqual(selected, wantSelected) {
		t.Fatalf("selected addons = %q, want %q", selected, wantSelected)
	}
	if _, err := geminiPortableSelectedAddons("linux", "amd64"); err == nil {
		t.Fatal("unreviewed addon target unexpectedly selected")
	}

	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Logf("live addon evidence is unavailable on %s/%s; static registry, identity, and selection assertions passed", runtime.GOOS, runtime.GOARCH)
		return
	}
	paths := localGeminiPortableEvidencePaths(t)
	if _, err := os.Stat(paths.packageRoot); errors.Is(err, os.ErrNotExist) {
		t.Log("reviewed Gemini 0.46.0 Mise package is not installed; static addon evidence assertions passed")
		return
	} else if err != nil {
		t.Fatal(err)
	}
	assertGeminiAddonCodePaths(t, paths.packageRoot)
	assertGeminiPortableELFEvidence(t, inspectGeminiPortableELF(t, filepath.Join(paths.packageRoot, filepath.FromSlash(selected[0]))), geminiPortableKeytarEvidence)
	assertGeminiPortableELFEvidence(t, inspectGeminiPortableELF(t, filepath.Join(paths.packageRoot, filepath.FromSlash(selected[1]))), geminiPortablePTYEvidence)
	assertGeminiForeignPrebuildsExcluded(t, paths.packageRoot, selected)

	t.Run("Homebrew keytar is an absolute-RPATH negative", func(t *testing.T) {
		keytar := filepath.Join(paths.homebrewPackage, "node_modules", "@github", "keytar", "build", "Release", "keytar.node")
		if _, err := os.Stat(keytar); errors.Is(err, os.ErrNotExist) {
			t.Skip("Homebrew-selected keytar negative fixture is not installed")
		} else if err != nil {
			t.Fatal(err)
		}
		observed := inspectGeminiPortableELF(t, keytar)
		if len(observed.rpath) == 0 || !containsGeminiAbsoluteRPATH(observed.rpath) {
			t.Fatalf("Homebrew keytar RPATH = %q, want an absolute Homebrew path", observed.rpath)
		}
	})
}

func localGeminiPortableEvidencePaths(t *testing.T) geminiPortableEvidencePaths {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	packageInstallRoot := filepath.Join(home, ".local", "share", "mise", "installs", "node", geminiPortablePackageNodeVersion)
	interpreterRoot := filepath.Join(home, ".local", "share", "mise", "installs", "node", geminiPortableNodeVersion)
	homebrewRoot := "/home/linuxbrew/.linuxbrew/Cellar"
	return geminiPortableEvidencePaths{
		packageInstallRoot: packageInstallRoot,
		packageRoot:        filepath.Join(packageInstallRoot, filepath.FromSlash(geminiPortablePackageRelative)),
		launcher:           filepath.Join(packageInstallRoot, filepath.FromSlash(geminiPortableLauncherRelative)),
		interpreterRoot:    interpreterRoot,
		interpreter:        filepath.Join(interpreterRoot, "bin", "node"),
		homebrewNode:       filepath.Join(homebrewRoot, "node", geminiPortableHomebrewNodeVersion, "bin", "node"),
		homebrewPackage:    filepath.Join(homebrewRoot, "gemini-cli", geminiPortablePackageVersion, "libexec", "lib", "node_modules", "@google", "gemini-cli"),
	}
}

func validateGeminiPortablePackageLayout(paths geminiPortableEvidencePaths) error {
	for _, path := range []string{paths.packageInstallRoot, paths.packageRoot, paths.launcher, paths.interpreterRoot, paths.interpreter} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("evidence path is not absolute and normalized")
		}
	}
	if paths.packageInstallRoot == paths.interpreterRoot {
		return errors.New("package and interpreter sources are not separate")
	}
	if paths.packageRoot != filepath.Join(paths.packageInstallRoot, filepath.FromSlash(geminiPortablePackageRelative)) ||
		paths.launcher != filepath.Join(paths.packageInstallRoot, filepath.FromSlash(geminiPortableLauncherRelative)) ||
		paths.interpreter != filepath.Join(paths.interpreterRoot, "bin", "node") {
		return errors.New("installed paths do not match the reviewed layout")
	}
	launcherInfo, err := os.Lstat(paths.launcher)
	if err != nil || launcherInfo.Mode()&os.ModeSymlink == 0 {
		return errors.New("Gemini launcher is not the reviewed symlink")
	}
	link, err := os.Readlink(paths.launcher)
	if err != nil || link != geminiPortableLauncherLink {
		return errors.New("Gemini launcher target drifted")
	}
	resolved, err := filepath.EvalSymlinks(paths.launcher)
	if err != nil || resolved != filepath.Join(paths.packageRoot, filepath.FromSlash(geminiPortableEntrypoint)) {
		return errors.New("Gemini launcher does not resolve to the reviewed entrypoint")
	}
	entrypointInfo, err := os.Lstat(resolved)
	if err != nil || !entrypointInfo.Mode().IsRegular() || entrypointInfo.Mode().Perm()&0o100 == 0 {
		return errors.New("Gemini entrypoint is not an owner-executable regular file")
	}
	interpreterInfo, err := os.Lstat(paths.interpreter)
	if err != nil || !interpreterInfo.Mode().IsRegular() || interpreterInfo.Mode().Perm()&0o100 == 0 {
		return errors.New("Node interpreter is not an owner-executable regular file")
	}
	contents, err := os.ReadFile(filepath.Join(paths.packageRoot, "package.json"))
	if err != nil {
		return errors.New("Gemini package metadata is unavailable")
	}
	var metadata struct {
		Name    string            `json:"name"`
		Version string            `json:"version"`
		Bin     map[string]string `json:"bin"`
	}
	if err := json.Unmarshal(contents, &metadata); err != nil || metadata.Name != "@google/gemini-cli" ||
		metadata.Version != geminiPortablePackageVersion || len(metadata.Bin) != 1 || metadata.Bin["gemini"] != geminiPortableEntrypoint {
		return errors.New("Gemini package metadata drifted")
	}
	return nil
}

func seedGeminiPortablePackageLayout(t *testing.T) geminiPortableEvidencePaths {
	t.Helper()
	base := t.TempDir()
	packageInstallRoot := filepath.Join(base, "package-node")
	interpreterRoot := filepath.Join(base, "clean-node")
	paths := geminiPortableEvidencePaths{
		packageInstallRoot: packageInstallRoot,
		packageRoot:        filepath.Join(packageInstallRoot, filepath.FromSlash(geminiPortablePackageRelative)),
		launcher:           filepath.Join(packageInstallRoot, filepath.FromSlash(geminiPortableLauncherRelative)),
		interpreterRoot:    interpreterRoot,
		interpreter:        filepath.Join(interpreterRoot, "bin", "node"),
	}
	writeGeminiPortableTestFile(t, filepath.Join(paths.packageRoot, "package.json"), []byte(`{"name":"@google/gemini-cli","version":"0.46.0","bin":{"gemini":"bundle/gemini.js"}}`), 0o600)
	writeGeminiPortableTestFile(t, filepath.Join(paths.packageRoot, filepath.FromSlash(geminiPortableEntrypoint)), []byte("#!/usr/bin/env node\n"), 0o700)
	writeGeminiPortableTestFile(t, paths.interpreter, []byte("reviewed interpreter fixture"), 0o700)
	if err := os.MkdirAll(filepath.Dir(paths.launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(geminiPortableLauncherLink, paths.launcher); err != nil {
		t.Fatal(err)
	}
	return paths
}

func requireValidGeminiPortablePackageLayout(t *testing.T, paths geminiPortableEvidencePaths) {
	t.Helper()
	if err := validateGeminiPortablePackageLayout(paths); err != nil {
		t.Fatalf("fresh exact fixture rejected before mutation: %v", err)
	}
}

func assertGeminiPortablePackageLayoutError(t *testing.T, paths geminiPortableEvidencePaths, want string) {
	t.Helper()
	err := validateGeminiPortablePackageLayout(paths)
	if err == nil || err.Error() != want {
		t.Fatalf("layout drift error = %v, want %q", err, want)
	}
}

func writeGeminiPortableTestFile(t *testing.T, path string, contents []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func inspectGeminiPortableELF(t *testing.T, path string) geminiPortableObservedELF {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	image, err := elf.NewFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	observed := geminiPortableObservedELF{
		size:          info.Size(),
		contentSHA256: hex.EncodeToString(hasher.Sum(nil)),
	}
	for _, program := range image.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		contents, err := io.ReadAll(io.LimitReader(program.Open(), 4097))
		if err != nil || len(contents) < 2 || contents[len(contents)-1] != 0 {
			t.Fatalf("invalid PT_INTERP in %s", filepath.Base(path))
		}
		observed.interpreter = string(contents[:len(contents)-1])
	}
	section := image.Section(".note.gnu.build-id")
	if section == nil {
		t.Fatalf("%s has no GNU build ID", filepath.Base(path))
	}
	notes, err := section.Data()
	if err != nil {
		t.Fatal(err)
	}
	observed.buildID, err = parseGeminiPortableGNUNotes(notes, image.ByteOrder)
	if err != nil {
		t.Fatal(err)
	}
	observed.needed, err = image.DynString(elf.DT_NEEDED)
	if err != nil {
		t.Fatal(err)
	}
	observed.rpath, err = image.DynString(elf.DT_RPATH)
	if err != nil {
		t.Fatal(err)
	}
	observed.runpath, err = image.DynString(elf.DT_RUNPATH)
	if err != nil {
		t.Fatal(err)
	}
	return observed
}

func parseGeminiPortableGNUNotes(notes []byte, order binary.ByteOrder) (string, error) {
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

func assertGeminiPortableELFEvidence(t *testing.T, observed geminiPortableObservedELF, want geminiPortableELFEvidence) {
	t.Helper()
	if observed.size != want.size || observed.contentSHA256 != want.contentSHA256 || observed.buildID != want.buildID ||
		(want.interpreter != "" && observed.interpreter != want.interpreter) ||
		(len(want.needed) != 0 && !reflect.DeepEqual(observed.needed, want.needed)) {
		t.Fatalf("ELF evidence mismatch:\n got: %#v\nwant: %#v", observed, want)
	}
	if len(observed.rpath) != 0 || len(observed.runpath) != 0 {
		t.Fatalf("selected ELF has RPATH/RUNPATH: rpath=%q runpath=%q", observed.rpath, observed.runpath)
	}
}

func assertGeminiAddonCodePaths(t *testing.T, root string) {
	t.Helper()
	entrypoint := readGeminiPortableText(t, filepath.Join(root, filepath.FromSlash(geminiPortableEntrypoint)))
	if !strings.Contains(entrypoint, `import "./chunk-RCJSF5RP.js";`) {
		t.Fatal("reviewed entrypoint no longer reaches the pinned bundle chunk")
	}
	chunk := readGeminiPortableText(t, filepath.Join(root, filepath.FromSlash(geminiPortableBundleChunk)))
	for _, marker := range []string{`const moduleName = "@github/keytar";`, `const lydell = "@lydell/node-pty";`} {
		if !strings.Contains(chunk, marker) {
			t.Fatalf("reviewed bundle chunk lacks addon marker %q", marker)
		}
	}
	keytar := readGeminiPortableText(t, filepath.Join(root, filepath.FromSlash(geminiPortableKeytarLoader)))
	buildRelease := strings.Index(keytar, "var paths = ['../build/Release'")
	prebuild := strings.Index(keytar, "'../' + prebuildDir")
	if buildRelease < 0 || prebuild < 0 || buildRelease >= prebuild {
		t.Fatal("keytar loader no longer selects build/Release before foreign prebuilds")
	}
	pty := readGeminiPortableText(t, filepath.Join(root, filepath.FromSlash(geminiPortablePTYLoader)))
	if !strings.Contains(pty, `var PACKAGE_NAME = "@lydell/node-pty-" + process.platform + "-" + process.arch;`) {
		t.Fatal("node-pty loader no longer selects the target-specific registry package")
	}
	unixPTY := readGeminiPortableText(t, filepath.Join(root, filepath.FromSlash(geminiPortablePTYUnixLoader)))
	if !strings.Contains(unixPTY, "requireBinary_1.requireBinary('pty.node')") {
		t.Fatal("node-pty Unix path no longer reaches pty.node")
	}
}

func assertGeminiForeignPrebuildsExcluded(t *testing.T, root string, selected []string) {
	t.Helper()
	selectedSet := make(map[string]struct{}, len(selected))
	for _, path := range selected {
		selectedSet[filepath.ToSlash(path)] = struct{}{}
	}
	foreign := 0
	keytarRoot := filepath.Join(root, "node_modules", "@github", "keytar")
	err := filepath.WalkDir(keytarRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".node" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := selectedSet[relative]; !ok {
			foreign++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if foreign == 0 {
		t.Fatal("foreign keytar prebuild fixture is missing")
	}
	if len(selectedSet) != 2 {
		t.Fatalf("selected addon set = %q, want exactly two target files", selected)
	}
}

func readGeminiPortableText(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func assertGeminiCachedNPMTarball(t *testing.T, evidence geminiPortableNPMEvidence) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(evidence.integrity, "sha512-"))
	if err != nil || len(digest) != sha512.Size {
		t.Fatalf("invalid pinned npm integrity for %s@%s", evidence.name, evidence.version)
	}
	hexDigest := hex.EncodeToString(digest)
	path := filepath.Join(home, ".npm", "_cacache", "content-v2", "sha512", hexDigest[:2], hexDigest[2:4], hexDigest[4:])
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Logf("cached npm tarball unavailable for %s@%s; pinned registry identity remains statically asserted", evidence.name, evidence.version)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hasher := sha512.New()
	if _, err := io.Copy(hasher, file); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hasher.Sum(nil), digest) {
		t.Fatalf("cached npm tarball integrity mismatch for %s@%s", evidence.name, evidence.version)
	}
}

func assertGeminiFileSHA256(t *testing.T, path, want string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != want {
		t.Fatalf("SHA256(%s) = %s, want %s", filepath.Base(path), got, want)
	}
}

func containsGeminiAbsoluteRPATH(paths []string) bool {
	for _, pathList := range paths {
		for _, path := range strings.Split(pathList, ":") {
			if filepath.IsAbs(path) && strings.HasPrefix(path, "/home/linuxbrew/.linuxbrew/") {
				return true
			}
		}
	}
	return false
}
