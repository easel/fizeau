package harnesses

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type portableRuntimeNodeAddonFixture struct {
	target         PortableRuntimeTarget
	request        PortableRuntimeInterpretedClosureRequest
	packageRoot    string
	commonRoot     string
	unusedRoot     string
	addonRoot      string
	keytarRelative string
	ptyRelative    string
}

func newPortableRuntimeNodeAddonFixture(t *testing.T) portableRuntimeNodeAddonFixture {
	t.Helper()
	requirePortableRuntimeLinux(t)
	base := t.TempDir()
	commonRoot := filepath.Join(base, "common")
	unusedRoot := filepath.Join(base, "unused")
	addonRoot := filepath.Join(base, "addon-libs")
	packageRoot := filepath.Join(base, "package")
	for _, directory := range []string{commonRoot, unusedRoot, addonRoot, packageRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	shared := filepath.Join(commonRoot, "libshared.so")
	compilePortableRuntimeC(t, shared, `int portable_shared(void) { return 7; }`,
		"-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,libshared.so")
	interpreterLibrary := filepath.Join(commonRoot, "libinterpreter.so")
	compilePortableRuntimeC(t, interpreterLibrary, `int portable_interpreter_dependency(void) { return 11; }`,
		"-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,libinterpreter.so")
	loaderLibrary := filepath.Join(commonRoot, "libloader.so")
	compilePortableRuntimeC(t, loaderLibrary, `int portable_loader_dependency(void) { return 13; }`,
		"-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,libloader.so")
	loader := filepath.Join(base, "ld-musl-fixture.so.1")
	compilePortableRuntimeC(t, loader, `
extern int portable_loader_dependency(void);
void portable_loader_start(void) { if (portable_loader_dependency() == 0) { for (;;) {} } }
`, "-shared", "-fPIC", "-nostdlib", "-Wl,-e,portable_loader_start", "-Wl,-soname,ld-musl-fixture.so.1",
		"-L"+commonRoot, "-Wl,--no-as-needed", "-lloader")
	interpreter := filepath.Join(base, "node")
	compilePortableRuntimeC(t, interpreter, `
extern int portable_interpreter_dependency(void);
void portable_node_start(void) { if (portable_interpreter_dependency() == 0) { for (;;) {} } }
`, "-fPIE", "-pie", "-nostdlib", "-Wl,-e,portable_node_start", "-Wl,--dynamic-linker,"+loader,
		"-L"+commonRoot, "-Wl,--no-as-needed", "-linterpreter")
	keytarLibrary := filepath.Join(addonRoot, "libkeytar.so")
	compilePortableRuntimeC(t, keytarLibrary, `
extern int portable_shared(void);
int portable_keytar(void) { return portable_shared(); }
`, "-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,libkeytar.so",
		"-L"+commonRoot, "-Wl,--no-as-needed", "-lshared")
	ptyLibrary := filepath.Join(addonRoot, "libpty.so")
	compilePortableRuntimeC(t, ptyLibrary, `
extern int portable_shared(void);
int portable_pty(void) { return portable_shared(); }
`, "-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,libpty.so",
		"-L"+commonRoot, "-Wl,--no-as-needed", "-lshared")

	keytarRelative := "node_modules/keytar/build/Release/keytar.node"
	ptyRelative := "node_modules/node-pty/build/Release/pty.node"
	keytar := filepath.Join(packageRoot, filepath.FromSlash(keytarRelative))
	pty := filepath.Join(packageRoot, filepath.FromSlash(ptyRelative))
	if err := os.MkdirAll(filepath.Dir(keytar), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pty), 0o700); err != nil {
		t.Fatal(err)
	}
	compilePortableRuntimeC(t, keytar, `
extern int portable_keytar(void);
int portable_addon(void) { return portable_keytar(); }
`, "-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,keytar.node",
		"-L"+addonRoot, "-Wl,--no-as-needed", "-lkeytar")
	compilePortableRuntimeC(t, pty, `
extern int portable_pty(void);
int portable_addon(void) { return portable_pty(); }
`, "-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,pty.node",
		"-L"+addonRoot, "-Wl,--no-as-needed", "-lpty")
	if err := os.MkdirAll(filepath.Join(packageRoot, "prebuilds", "foreign-platform"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "prebuilds", "foreign-platform", "foreign.node"), []byte("seeded-foreign-addon-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"portable-addon-fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := writePortableRuntimeNodeScript(t, "#!/usr/bin/env node\nfixture\n")
	request := PortableRuntimeInterpretedClosureRequest{
		EntrypointSource:    script,
		EntrypointTarget:    "interpreted/script/tool.js",
		InterpreterSource:   interpreter,
		InterpreterIdentity: portableRuntimeFixtureFileIdentity(t, interpreter),
		InterpreterTarget:   "interpreted/bin/node",
		LoaderTarget:        "interpreted/loader/ld-musl-fixture.so.1",
		ExactLibraryRoots: []PortableRuntimeLibrarySearchRoot{
			{Source: commonRoot, Target: "interpreted/lib/common"},
			{Source: unusedRoot, Target: "interpreted/lib/unused"},
			{Source: addonRoot, Target: "interpreted/lib/addons"},
		},
		PackageTrees:  []PortableRuntimeSourceTree{{Source: packageRoot, Target: "interpreted/package"}},
		RuntimeLookup: PortableRuntimeLookupClosed,
	}
	request.NativeAddons = []PortableRuntimeNativeAddon{
		{PackageTreeTarget: "interpreted/package", RelativePath: keytarRelative, Identity: portableRuntimeFixtureFileIdentity(t, keytar)},
		{PackageTreeTarget: "interpreted/package", RelativePath: ptyRelative, Identity: portableRuntimeFixtureFileIdentity(t, pty)},
	}
	return portableRuntimeNodeAddonFixture{
		target: targetForPortableRuntimeTests(), request: request, packageRoot: packageRoot,
		commonRoot: commonRoot, unusedRoot: unusedRoot, addonRoot: addonRoot,
		keytarRelative: keytarRelative, ptyRelative: ptyRelative,
	}
}

func targetForPortableRuntimeTests() PortableRuntimeTarget {
	return PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
}

func compilePortableRuntimeC(t *testing.T, output, source string, arguments ...string) {
	t.Helper()
	sourcePath := output + ".c"
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-o", output, sourcePath}, arguments...)
	command := exec.Command("cc", args...)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build synthetic ELF %s: %v: %s", filepath.Base(output), err, out)
	}
}

func clonePortableRuntimeNodeAddonFixture(t *testing.T, original portableRuntimeNodeAddonFixture) portableRuntimeNodeAddonFixture {
	t.Helper()
	base := t.TempDir()
	clone := original
	clone.packageRoot = filepath.Join(base, "package")
	clone.commonRoot = filepath.Join(base, "common")
	clone.unusedRoot = filepath.Join(base, "unused")
	clone.addonRoot = filepath.Join(base, "addon-libs")
	copyPortableRuntimeTree(t, original.packageRoot, clone.packageRoot)
	copyPortableRuntimeTree(t, original.commonRoot, clone.commonRoot)
	copyPortableRuntimeTree(t, original.unusedRoot, clone.unusedRoot)
	copyPortableRuntimeTree(t, original.addonRoot, clone.addonRoot)
	clone.request.PackageTrees = []PortableRuntimeSourceTree{{Source: clone.packageRoot, Target: original.request.PackageTrees[0].Target}}
	clone.request.ExactLibraryRoots = append([]PortableRuntimeLibrarySearchRoot(nil), original.request.ExactLibraryRoots...)
	clone.request.ExactLibraryRoots[0].Source = clone.commonRoot
	clone.request.ExactLibraryRoots[1].Source = clone.unusedRoot
	clone.request.ExactLibraryRoots[2].Source = clone.addonRoot
	clone.refreshIdentities(t)
	return clone
}

func (fixture *portableRuntimeNodeAddonFixture) refreshIdentities(t *testing.T) {
	t.Helper()
	for i := range fixture.request.NativeAddons {
		member := filepath.Join(fixture.packageRoot, filepath.FromSlash(fixture.request.NativeAddons[i].RelativePath))
		fixture.request.NativeAddons[i].Identity = portableRuntimeFixtureFileIdentity(t, member)
	}
}

func appendPortableRuntimeFixtureSeed(t *testing.T, source, seed string) {
	t.Helper()
	file, err := os.OpenFile(source, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(seed); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func portableRuntimeSeededIdentity(seed string, size int64) PortableRuntimeFileIdentity {
	digest := sha256.Sum256([]byte(seed))
	return PortableRuntimeFileIdentity{Size: size, ContentSHA256: hex.EncodeToString(digest[:])}
}

func portableRuntimeIdentityRedactionValues(t *testing.T, expected, actual PortableRuntimeFileIdentity) []string {
	t.Helper()
	if expected.Size == actual.Size || expected.ContentSHA256 == actual.ContentSHA256 {
		t.Fatalf("redaction fixture identities are not unique: expected=%#v actual=%#v", expected, actual)
	}
	return []string{
		strconv.FormatInt(expected.Size, 10),
		expected.ContentSHA256,
		strconv.FormatInt(actual.Size, 10),
		actual.ContentSHA256,
	}
}

func copyPortableRuntimeTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.Walk(source, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		contents, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, contents, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertPortableRuntimeNodeAddonFailure(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("error leaked %q: %v", value, err)
		}
	}
}

func assetTargets(contribution PortableRuntimeContribution) []string {
	targets := make([]string, len(contribution.Assets))
	for i := range contribution.Assets {
		targets[i] = contribution.Assets[i].Target
	}
	return targets
}

func portableRuntimeELFForTest(t *testing.T, source string) *elf.File {
	t.Helper()
	file, err := elf.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func mutatePortableRuntimeDynamicEntry(t *testing.T, source string, wanted elf.DynTag, mutate func(elf.DynTag, uint64) (elf.DynTag, uint64)) {
	t.Helper()
	file := portableRuntimeELFForTest(t, source)
	section := file.SectionByType(elf.SHT_DYNAMIC)
	if section == nil {
		t.Fatalf("%s has no dynamic section", source)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	order := file.ByteOrder
	entrySize := int(section.Entsize)
	if entrySize == 0 {
		if file.Class == elf.ELFCLASS64 {
			entrySize = 16
		} else {
			entrySize = 8
		}
	}
	start := int(section.Offset)
	end := start + int(section.Size)
	for offset := start; offset+entrySize <= end; offset += entrySize {
		var tag elf.DynTag
		var value uint64
		if file.Class == elf.ELFCLASS64 {
			tag = elf.DynTag(int64(order.Uint64(contents[offset : offset+8])))
			value = order.Uint64(contents[offset+8 : offset+16])
		} else {
			tag = elf.DynTag(int32(order.Uint32(contents[offset : offset+4])))
			value = uint64(order.Uint32(contents[offset+4 : offset+8]))
		}
		if tag != wanted {
			continue
		}
		newTag, newValue := mutate(tag, value)
		if file.Class == elf.ELFCLASS64 {
			order.PutUint64(contents[offset:offset+8], uint64(newTag))
			order.PutUint64(contents[offset+8:offset+16], newValue)
		} else {
			order.PutUint32(contents[offset:offset+4], uint32(newTag))
			order.PutUint32(contents[offset+4:offset+8], uint32(newValue))
		}
		if err := os.WriteFile(source, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s has no dynamic tag %s", source, wanted)
}

func portableRuntimeDynamicTagValue(t *testing.T, source string, wanted elf.DynTag) uint64 {
	t.Helper()
	file := portableRuntimeELFForTest(t, source)
	values, err := file.DynValue(wanted)
	if err != nil || len(values) == 0 {
		t.Fatalf("%s dynamic tag %s = %v, %v", source, wanted, values, err)
	}
	return values[0]
}

func mutatePortableRuntimeSONAMEString(t *testing.T, source string, value byte) {
	t.Helper()
	offset := portableRuntimeDynamicTagValue(t, source, elf.DT_SONAME)
	file := portableRuntimeELFForTest(t, source)
	stringsSection := file.Section(".dynstr")
	if stringsSection == nil || offset >= stringsSection.Size {
		t.Fatalf("%s has invalid SONAME offset", source)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	contents[int(stringsSection.Offset+offset)] = value
	if err := os.WriteFile(source, contents, 0o700); err != nil {
		t.Fatal(err)
	}
}

func addPortableRuntimeProgramInterpreter(t *testing.T, source string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 64 || string(contents[:4]) != "\x7fELF" {
		t.Fatalf("%s is not ELF", source)
	}
	var order binary.ByteOrder
	if elf.Data(contents[5]) == elf.ELFDATA2LSB {
		order = binary.LittleEndian
	} else {
		order = binary.BigEndian
	}
	var phoff uint64
	var phentsize, phnum uint16
	if elf.Class(contents[4]) == elf.ELFCLASS64 {
		phoff = order.Uint64(contents[32:40])
		phentsize = order.Uint16(contents[54:56])
		phnum = order.Uint16(contents[56:58])
	} else {
		phoff = uint64(order.Uint32(contents[28:32]))
		phentsize = order.Uint16(contents[42:44])
		phnum = order.Uint16(contents[44:46])
	}
	for i := uint16(0); i < phnum; i++ {
		offset := int(phoff) + int(i*phentsize)
		programType := elf.ProgType(order.Uint32(contents[offset : offset+4]))
		if programType != elf.PT_GNU_STACK && programType != elf.PT_NOTE {
			continue
		}
		order.PutUint32(contents[offset:offset+4], uint32(elf.PT_INTERP))
		if err := os.WriteFile(source, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s has no patchable program header", source)
}

func corruptPortableRuntimeDynamicStringLink(t *testing.T, source string) {
	t.Helper()
	file := portableRuntimeELFForTest(t, source)
	dynamicIndex := -1
	for i, section := range file.Sections {
		if section.Type == elf.SHT_DYNAMIC {
			dynamicIndex = i
			break
		}
	}
	if dynamicIndex < 0 {
		t.Fatalf("%s has no dynamic section", source)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	order := file.ByteOrder
	var sectionOffset uint64
	var sectionSize uint16
	var linkOffset int
	if file.Class == elf.ELFCLASS64 {
		sectionOffset = order.Uint64(contents[40:48])
		sectionSize = order.Uint16(contents[58:60])
		linkOffset = 40
	} else {
		sectionOffset = uint64(order.Uint32(contents[32:36]))
		sectionSize = order.Uint16(contents[46:48])
		linkOffset = 24
	}
	offset := int(sectionOffset) + dynamicIndex*int(sectionSize) + linkOffset
	order.PutUint32(contents[offset:offset+4], uint32(len(file.Sections)+100))
	if err := os.WriteFile(source, contents, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestPortableRuntimeNodeAddonDeclaration(t *testing.T) {
	requirePortableRuntimeLinux(t)
	interpreter := buildPortableRuntimeStaticFixture(t)
	script := writePortableRuntimeNodeScript(t, "#!/usr/bin/env node\nfixture\n")
	base := portableRuntimeNodeInterpretedRequest(t, script, interpreter)
	validIdentity := portableRuntimeFixtureFileIdentity(t, interpreter)
	valid := PortableRuntimeNativeAddon{
		PackageTreeTarget: base.PackageTrees[0].Target,
		RelativePath:      "build/Release/addon.node",
		Identity:          validIdentity,
	}
	tests := []struct {
		name   string
		mutate func(*PortableRuntimeInterpretedClosureRequest)
	}{
		{name: "missing target", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].PackageTreeTarget = ""
		}},
		{name: "missing path", mutate: func(request *PortableRuntimeInterpretedClosureRequest) { request.NativeAddons[0].RelativePath = "" }},
		{name: "missing identity", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].Identity = PortableRuntimeFileIdentity{}
		}},
		{name: "duplicate", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons = append(request.NativeAddons, request.NativeAddons[0])
		}},
		{name: "dot component", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].RelativePath = "./addon.node"
		}},
		{name: "dot dot component", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].RelativePath = "build/../addon.node"
		}},
		{name: "platform separator", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].RelativePath = `build\addon.node`
		}},
		{name: "absolute path", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].RelativePath = "/build/addon.node"
		}},
		{name: "wrong suffix", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].RelativePath = "build/addon.so"
		}},
		{name: "unknown target", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.NativeAddons[0].PackageTreeTarget = "unknown/package"
		}},
		{name: "ambiguous target", mutate: func(request *PortableRuntimeInterpretedClosureRequest) {
			request.PackageTrees = append(request.PackageTrees, request.PackageTrees[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.PackageTrees = append([]PortableRuntimeSourceTree(nil), base.PackageTrees...)
			request.NativeAddons = []PortableRuntimeNativeAddon{valid}
			test.mutate(&request)
			secretRoot := filepath.Join(t.TempDir(), "account-secret-package-root")
			request.PackageTrees[0].Source = secretRoot
			_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), targetForPortableRuntimeTests(), request)
			assertPortableRuntimeNodeAddonFailure(t, err, secretRoot, request.NativeAddons[0].RelativePath, request.NativeAddons[0].Identity.ContentSHA256)
		})
	}

	base.NativeAddons = nil
	if _, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), targetForPortableRuntimeTests(), base); err != nil {
		t.Fatalf("nil NativeAddons compatibility error = %v", err)
	}
}

func TestPortableRuntimeNodeAddonDescriptorIdentity(t *testing.T) {
	fixture := newPortableRuntimeNodeAddonFixture(t)
	contribution, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), fixture.target, fixture.request)
	if err != nil {
		t.Fatalf("descriptor-bound addon analysis error = %v", err)
	}
	for _, addon := range fixture.request.NativeAddons {
		addonTarget := filepath.ToSlash(filepath.Join(fixture.request.PackageTrees[0].Target, addon.RelativePath))
		for _, asset := range contribution.Assets {
			if asset.PathKind == PortableRuntimePathFile && asset.Target == addonTarget {
				t.Fatalf("addon escaped its owning package tree as a file asset: %#v", asset)
			}
		}
	}
	if countPortableRuntimeTreeTarget(contribution.Assets, fixture.request.PackageTrees[0].Target) != 1 {
		t.Fatalf("package tree asset missing or duplicated: %v", assetTargets(contribution))
	}

	t.Run("identity mismatch", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		member := filepath.Join(clone.packageRoot, filepath.FromSlash(clone.keytarRelative))
		appendPortableRuntimeFixtureSeed(t, member, "seeded-actual-identity-mismatch-bytes")
		clone.refreshIdentities(t)
		actual := clone.request.NativeAddons[0].Identity
		expected := portableRuntimeSeededIdentity("seeded-expected-identity-mismatch-digest", actual.Size+137)
		clone.request.NativeAddons[0].Identity = expected
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		forbidden := append([]string{clone.packageRoot}, portableRuntimeIdentityRedactionValues(t, expected, actual)...)
		assertPortableRuntimeNodeAddonFailure(t, err, forbidden...)
	})

	t.Run("final symlink", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		member := filepath.Join(clone.packageRoot, filepath.FromSlash(clone.keytarRelative))
		target := filepath.Join(filepath.Dir(member), "real-keytar.node")
		if err := os.Rename(member, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Base(target), member); err != nil {
			t.Fatal(err)
		}
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, clone.keytarRelative)
	})

	t.Run("escaping final symlink", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		member := filepath.Join(clone.packageRoot, filepath.FromSlash(clone.keytarRelative))
		external := copyPortableRuntimeELFFixture(t, member)
		if err := os.Remove(member); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, member); err != nil {
			t.Fatal(err)
		}
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, clone.keytarRelative, external)
	})

	t.Run("intermediate symlink", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		original := filepath.Join(clone.packageRoot, "node_modules", "keytar")
		real := filepath.Join(clone.packageRoot, "node_modules", "keytar-real")
		if err := os.Rename(original, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Base(real), original); err != nil {
			t.Fatal(err)
		}
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, clone.keytarRelative)
	})

	for _, window := range []struct {
		name string
		hook func(*portableRuntimeInterpretedClosureHooks, func())
	}{
		{name: "after package snapshot", hook: func(hooks *portableRuntimeInterpretedClosureHooks, replace func()) {
			hooks.afterNativeAddonPackageSnapshots = replace
		}},
		{name: "before snapshot verification", hook: func(hooks *portableRuntimeInterpretedClosureHooks, replace func()) {
			hooks.beforeNativeAddonSnapshotVerification = replace
		}},
	} {
		t.Run(window.name+" member replacement", func(t *testing.T) {
			clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
			member := filepath.Join(clone.packageRoot, filepath.FromSlash(clone.keytarRelative))
			appendPortableRuntimeFixtureSeed(t, member, "seeded-expected-"+window.name+"-bytes")
			clone.refreshIdentities(t)
			expected := clone.request.NativeAddons[0].Identity
			replacement := copyPortableRuntimeELFFixture(t, member)
			appendPortableRuntimeFixtureSeed(t, replacement, "seeded-actual-"+window.name+"-replacement-binary-bytes")
			actual := portableRuntimeFixtureFileIdentity(t, replacement)
			hooks := portableRuntimeInterpretedClosureHooks{}
			window.hook(&hooks, func() {
				if renameErr := os.Rename(replacement, member); renameErr != nil {
					t.Fatal(renameErr)
				}
			})
			_, err := analyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request, hooks)
			forbidden := append([]string{clone.packageRoot, clone.keytarRelative}, portableRuntimeIdentityRedactionValues(t, expected, actual)...)
			assertPortableRuntimeNodeAddonFailure(t, err, forbidden...)
		})
	}

	t.Run("package root replacement after open", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		expected := clone.request.NativeAddons[0].Identity
		replacement := filepath.Join(t.TempDir(), "replacement-package")
		copyPortableRuntimeTree(t, clone.packageRoot, replacement)
		replacementMember := filepath.Join(replacement, filepath.FromSlash(clone.keytarRelative))
		appendPortableRuntimeFixtureSeed(t, replacementMember, "seeded-actual-package-root-replacement-bytes")
		actual := portableRuntimeFixtureFileIdentity(t, replacementMember)
		old := clone.packageRoot + "-old"
		hooks := portableRuntimeInterpretedClosureHooks{beforeNativeAddonSnapshotVerification: func() {
			if err := os.Rename(clone.packageRoot, old); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, clone.packageRoot); err != nil {
				t.Fatal(err)
			}
		}}
		_, err := analyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request, hooks)
		forbidden := append([]string{clone.packageRoot}, portableRuntimeIdentityRedactionValues(t, expected, actual)...)
		assertPortableRuntimeNodeAddonFailure(t, err, forbidden...)
	})
}

func TestPortableRuntimeNodeAddonELFPolicy(t *testing.T) {
	fixture := newPortableRuntimeNodeAddonFixture(t)
	keytarPath := func(f portableRuntimeNodeAddonFixture) string {
		return filepath.Join(f.packageRoot, filepath.FromSlash(f.keytarRelative))
	}
	analyzeFailure := func(t *testing.T, clone portableRuntimeNodeAddonFixture) {
		t.Helper()
		clone.refreshIdentities(t)
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, clone.keytarRelative,
			clone.request.NativeAddons[0].Identity.ContentSHA256, "keytar.node", "libkeytar.so")
	}

	t.Run("static interpreter", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		static := buildPortableRuntimeStaticFixture(t)
		clone.request.InterpreterSource = static
		clone.request.InterpreterIdentity = portableRuntimeFixtureFileIdentity(t, static)
		clone.request.LoaderTarget = ""
		clone.request.ExactLibraryRoots = nil
		file := portableRuntimeELFForTest(t, static)
		if portableRuntimeELFHasInterpreter(file) {
			t.Fatal("static fixture unexpectedly has PT_INTERP")
		}
		analyzeFailure(t, clone)
	})

	t.Run("missing explicit roots", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		clone.request.ExactLibraryRoots = nil
		if file := portableRuntimeELFForTest(t, clone.request.InterpreterSource); !portableRuntimeELFHasInterpreter(file) {
			t.Fatal("dynamic fixture lacks PT_INTERP")
		}
		analyzeFailure(t, clone)
	})

	t.Run("unknown lookup policy", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		clone.request.RuntimeLookup = PortableRuntimeLookupPolicy("unknown")
		analyzeFailure(t, clone)
	})

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
		assert func(*testing.T, *elf.File)
	}{
		{name: "wrong ELF type", mutate: func(t *testing.T, source string) {
			mutatePortableRuntimeELFHeader(t, source, func(header []byte, order binary.ByteOrder) { order.PutUint16(header[16:18], uint16(elf.ET_EXEC)) })
		}, assert: func(t *testing.T, file *elf.File) {
			if file.Type != elf.ET_EXEC {
				t.Fatalf("type = %s", file.Type)
			}
		}},
		{name: "wrong architecture", mutate: func(t *testing.T, source string) {
			mutatePortableRuntimeELFHeader(t, source, func(header []byte, order binary.ByteOrder) { order.PutUint16(header[18:20], uint16(elf.EM_NONE)) })
		}, assert: func(t *testing.T, file *elf.File) {
			if file.Machine != elf.EM_NONE {
				t.Fatalf("machine = %s", file.Machine)
			}
		}},
		{name: "PT_INTERP", mutate: addPortableRuntimeProgramInterpreter, assert: func(t *testing.T, file *elf.File) {
			if !portableRuntimeELFHasInterpreter(file) {
				t.Fatal("fixture lacks PT_INTERP")
			}
		}},
		{name: "DF_1_PIE", mutate: func(t *testing.T, source string) {
			mutatePortableRuntimeDynamicEntry(t, source, elf.DT_FLAGS_1, func(tag elf.DynTag, value uint64) (elf.DynTag, uint64) { return tag, value | uint64(elf.DF_1_PIE) })
		}, assert: func(t *testing.T, file *elf.File) {
			values, _ := file.DynValue(elf.DT_FLAGS_1)
			if len(values) == 0 || values[0]&uint64(elf.DF_1_PIE) == 0 {
				t.Fatal("fixture lacks DF_1_PIE")
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
			source := keytarPath(clone)
			test.mutate(t, source)
			test.assert(t, portableRuntimeELFForTest(t, source))
			analyzeFailure(t, clone)
		})
	}

	t.Run("SONAME absent is accepted", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		source := keytarPath(clone)
		compilePortableRuntimeC(t, source, `extern int portable_keytar(void); int portable_addon(void) { return portable_keytar(); }`,
			"-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-L"+clone.addonRoot, "-Wl,--no-as-needed", "-lkeytar")
		clone.refreshIdentities(t)
		sonames, err := portableRuntimeELFForTest(t, source).DynString(elf.DT_SONAME)
		if err != nil || len(sonames) != 0 {
			t.Fatalf("absent SONAME fixture = %q, %v", sonames, err)
		}
		if _, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request); err != nil {
			t.Fatalf("absent SONAME rejected: %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
		assert func(*testing.T, *elf.File)
	}{
		{name: "empty SONAME", mutate: func(t *testing.T, source string) { mutatePortableRuntimeSONAMEString(t, source, 0) }, assert: func(t *testing.T, file *elf.File) {
			values, err := file.DynString(elf.DT_SONAME)
			if err != nil || len(values) != 1 || values[0] != "" {
				t.Fatalf("SONAME = %q, %v", values, err)
			}
		}},
		{name: "multiple SONAME", mutate: func(t *testing.T, source string) {
			offset := portableRuntimeDynamicTagValue(t, source, elf.DT_SONAME)
			mutatePortableRuntimeDynamicEntry(t, source, elf.DT_FLAGS_1, func(elf.DynTag, uint64) (elf.DynTag, uint64) { return elf.DT_SONAME, offset })
		}, assert: func(t *testing.T, file *elf.File) {
			values, err := file.DynString(elf.DT_SONAME)
			if err != nil || len(values) != 2 {
				t.Fatalf("SONAME = %q, %v", values, err)
			}
		}},
		{name: "mismatched SONAME", mutate: func(t *testing.T, source string) { mutatePortableRuntimeSONAMEString(t, source, 'x') }, assert: func(t *testing.T, file *elf.File) {
			values, err := file.DynString(elf.DT_SONAME)
			if err != nil || len(values) != 1 || values[0] == "keytar.node" {
				t.Fatalf("SONAME = %q, %v", values, err)
			}
		}},
		{name: "malformed SONAME", mutate: corruptPortableRuntimeDynamicStringLink, assert: func(t *testing.T, file *elf.File) {
			if _, err := file.DynString(elf.DT_SONAME); err == nil {
				t.Fatal("malformed SONAME fixture parsed")
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
			source := keytarPath(clone)
			test.mutate(t, source)
			test.assert(t, portableRuntimeELFForTest(t, source))
			analyzeFailure(t, clone)
		})
	}

	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH, elf.DT_AUDIT, elf.DT_DEPAUDIT, elf.DT_FILTER, elf.DT_AUXILIARY} {
		t.Run(tag.String(), func(t *testing.T) {
			clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
			source := keytarPath(clone)
			value := portableRuntimeDynamicTagValue(t, source, elf.DT_SONAME)
			mutatePortableRuntimeDynamicEntry(t, source, elf.DT_FLAGS_1, func(elf.DynTag, uint64) (elf.DynTag, uint64) { return tag, value })
			file := portableRuntimeELFForTest(t, source)
			if tag == elf.DT_RPATH || tag == elf.DT_RUNPATH {
				if values, err := file.DynString(tag); err != nil || len(values) == 0 {
					t.Fatalf("%s = %q, %v", tag, values, err)
				}
			} else if values, err := file.DynValue(tag); err != nil || len(values) == 0 {
				t.Fatalf("%s = %v, %v", tag, values, err)
			}
			analyzeFailure(t, clone)
		})
	}

	t.Run("closed lookup symbol", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		source := keytarPath(clone)
		compilePortableRuntimeC(t, source, `extern void *dlopen(const char *, int); void *portable_addon(void) { return dlopen("seeded-plugin", 0); }`,
			"-shared", "-fPIC", "-nostdlib", "-Wl,-z,now", "-Wl,-soname,keytar.node")
		file := portableRuntimeELFForTest(t, source)
		symbols, err := file.ImportedSymbols()
		if err != nil || !portableRuntimeHasPluginLookupSymbol(symbols) {
			t.Fatalf("lookup symbols = %#v, %v", symbols, err)
		}
		analyzeFailure(t, clone)
	})

	t.Run("recursive dependency lookup metadata", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		source := filepath.Join(clone.addonRoot, "libkeytar.so")
		value := portableRuntimeDynamicTagValue(t, source, elf.DT_SONAME)
		mutatePortableRuntimeDynamicEntry(t, source, elf.DT_FLAGS_1, func(elf.DynTag, uint64) (elf.DynTag, uint64) { return elf.DT_RPATH, value })
		if values, err := portableRuntimeELFForTest(t, source).DynString(elf.DT_RPATH); err != nil || len(values) == 0 {
			t.Fatalf("dependency RPATH = %q, %v", values, err)
		}
		analyzeFailure(t, clone)
	})
}

func TestPortableRuntimeNodeAddonClosure(t *testing.T) {
	fixture := newPortableRuntimeNodeAddonFixture(t)
	interpreterFile := portableRuntimeELFForTest(t, fixture.request.InterpreterSource)
	interpreterNeeded, err := interpreterFile.ImportedLibraries()
	if err != nil || !reflect.DeepEqual(interpreterNeeded, []string{"libinterpreter.so"}) {
		t.Fatalf("interpreter dependencies = %q, %v", interpreterNeeded, err)
	}
	loader, err := portableRuntimeELFInterpreter(interpreterFile)
	if err != nil || loader == "" {
		t.Fatalf("interpreter loader = %q, %v", loader, err)
	}
	loaderNeeded, err := portableRuntimeELFForTest(t, loader).ImportedLibraries()
	if err != nil || !reflect.DeepEqual(loaderNeeded, []string{"libloader.so"}) {
		t.Fatalf("loader dependencies = %q, %v", loaderNeeded, err)
	}
	keytar := filepath.Join(fixture.packageRoot, filepath.FromSlash(fixture.keytarRelative))
	keytarNeeded, err := portableRuntimeELFForTest(t, keytar).ImportedLibraries()
	if err != nil || !reflect.DeepEqual(keytarNeeded, []string{"libkeytar.so"}) {
		t.Fatalf("keytar direct dependencies = %q, %v", keytarNeeded, err)
	}
	keytarLibraryNeeded, err := portableRuntimeELFForTest(t, filepath.Join(fixture.addonRoot, "libkeytar.so")).ImportedLibraries()
	if err != nil || !reflect.DeepEqual(keytarLibraryNeeded, []string{"libshared.so"}) {
		t.Fatalf("keytar recursive dependencies = %q, %v", keytarLibraryNeeded, err)
	}

	first, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), fixture.target, fixture.request)
	if err != nil {
		t.Fatalf("native addon closure error = %v", err)
	}
	reversedRequest := fixture.request
	reversedRequest.NativeAddons = append([]PortableRuntimeNativeAddon(nil), fixture.request.NativeAddons...)
	for left, right := 0, len(reversedRequest.NativeAddons)-1; left < right; left, right = left+1, right-1 {
		reversedRequest.NativeAddons[left], reversedRequest.NativeAddons[right] = reversedRequest.NativeAddons[right], reversedRequest.NativeAddons[left]
	}
	reversed, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), fixture.target, reversedRequest)
	if err != nil {
		t.Fatalf("reverse-order native addon closure error = %v", err)
	}
	if !reflect.DeepEqual(first, reversed) {
		t.Fatalf("declaration order changed normalized contribution:\nfirst=%#v\nreversed=%#v", first, reversed)
	}
	wantRoots := []string{"interpreted/lib/common", "interpreted/lib/addons"}
	if !reflect.DeepEqual(first.Launch.LibraryRootTargets, wantRoots) {
		t.Fatalf("used library roots = %q, want %q", first.Launch.LibraryRootTargets, wantRoots)
	}
	wantLibraryTargets := map[string]bool{
		"interpreted/lib/common/libinterpreter.so": false,
		"interpreted/lib/common/libloader.so":      false,
		"interpreted/lib/common/libshared.so":      false,
		"interpreted/lib/addons/libkeytar.so":      false,
		"interpreted/lib/addons/libpty.so":         false,
	}
	sharedCount := 0
	for _, asset := range first.Assets {
		if _, exists := wantLibraryTargets[asset.Target]; exists {
			wantLibraryTargets[asset.Target] = true
		}
		if asset.Target == "interpreted/lib/common/libshared.so" {
			sharedCount++
		}
		if strings.Contains(asset.Target, "interpreted/lib/unused") {
			t.Fatalf("unused exact root survived pruning: %#v", asset)
		}
		if strings.HasPrefix(asset.Target, "interpreted/lib/") {
			if _, expected := wantLibraryTargets[asset.Target]; !expected {
				t.Fatalf("synthetic closure unexpectedly included a host or undeclared library: %#v", asset)
			}
		}
		if asset.PathKind == PortableRuntimePathFile && (strings.HasSuffix(asset.Target, "keytar.node") || strings.HasSuffix(asset.Target, "pty.node") || strings.HasSuffix(asset.Target, "foreign.node")) {
			t.Fatalf("package member emitted as overlapping file asset: %#v", asset)
		}
	}
	for target, found := range wantLibraryTargets {
		if !found {
			t.Fatalf("recursive dependency asset %q missing from %v", target, assetTargets(first))
		}
	}
	if sharedCount != 1 {
		t.Fatalf("shared dependency asset count = %d, want 1", sharedCount)
	}
	if countPortableRuntimeTreeTarget(first.Assets, "interpreted/package") != 1 {
		t.Fatalf("package tree count = %d, want 1", countPortableRuntimeTreeTarget(first.Assets, "interpreted/package"))
	}

	for _, missing := range []struct {
		name string
		path func(portableRuntimeNodeAddonFixture) string
	}{
		{name: "direct", path: func(clone portableRuntimeNodeAddonFixture) string {
			return filepath.Join(clone.addonRoot, "libkeytar.so")
		}},
		{name: "recursive", path: func(clone portableRuntimeNodeAddonFixture) string {
			return filepath.Join(clone.commonRoot, "libshared.so")
		}},
	} {
		t.Run("missing "+missing.name+" library", func(t *testing.T) {
			clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
			removed := missing.path(clone)
			if err := os.Remove(removed); err != nil {
				t.Fatal(err)
			}
			_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
			assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, removed, "libkeytar.so", "libshared.so")
		})
	}

	t.Run("ambiguous library", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		contents, err := os.ReadFile(filepath.Join(clone.addonRoot, "libkeytar.so"))
		if err != nil {
			t.Fatal(err)
		}
		duplicate := filepath.Join(clone.commonRoot, "libkeytar.so")
		if err := os.WriteFile(duplicate, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err = AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, duplicate, "libkeytar.so")
	})

	t.Run("same target different sources", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		duplicateRoot := filepath.Join(t.TempDir(), "duplicate-addon-root")
		copyPortableRuntimeTree(t, clone.addonRoot, duplicateRoot)
		clone.request.ExactLibraryRoots = append(clone.request.ExactLibraryRoots,
			PortableRuntimeLibrarySearchRoot{Source: duplicateRoot, Target: "interpreted/lib/addons"})
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, duplicateRoot, clone.addonRoot)
	})

	t.Run("library target collision", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		clone.request.LoaderTarget = "interpreted/lib/addons/libkeytar.so"
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot, "libkeytar.so")
	})

	t.Run("package target collision", func(t *testing.T) {
		clone := clonePortableRuntimeNodeAddonFixture(t, fixture)
		clone.request.PackageTrees[0].Target = clone.request.InterpreterTarget
		for i := range clone.request.NativeAddons {
			clone.request.NativeAddons[i].PackageTreeTarget = clone.request.InterpreterTarget
		}
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), clone.target, clone.request)
		assertPortableRuntimeNodeAddonFailure(t, err, clone.packageRoot)
	})
}
