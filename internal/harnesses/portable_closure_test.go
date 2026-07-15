package harnesses

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestPortableRuntimeStaticClosure(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	executable := buildPortableRuntimeStaticFixture(t)
	launcher := filepath.Join(t.TempDir(), "tool")
	if err := os.Symlink(executable, launcher); err != nil {
		t.Fatal(err)
	}

	contribution, err := AnalyzePortableRuntimeStaticClosure(context.Background(), target, PortableRuntimeStaticClosureRequest{
		EntrypointSource: launcher,
		EntrypointTarget: "static/bin/tool",
		RuntimeLookup:    PortableRuntimeLookupClosed,
	})
	if err != nil {
		t.Fatalf("AnalyzePortableRuntimeStaticClosure() error = %v", err)
	}
	if contribution.ClosureClass != PortableRuntimeClosureStatic {
		t.Fatalf("closure class = %q, want static", contribution.ClosureClass)
	}
	if got := contribution.Assets[0].Source; got != executable {
		t.Fatalf("resolved source = %q, want %q", got, executable)
	}
	if contribution.Launch.InterpreterTarget != "" || contribution.Launch.LoaderTarget != "" || len(contribution.Launch.LibraryRootTargets) != 0 {
		t.Fatalf("static launch contains non-direct state: %#v", contribution.Launch)
	}
	file, err := elf.Open(contribution.Assets[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if interpreter, err := portableRuntimeELFInterpreter(file); err != nil || interpreter != "" {
		t.Fatalf("static fixture PT_INTERP = %q, err = %v", interpreter, err)
	}
	command, arguments, err := BuildPortableRuntimeLaunchCommand("/opt/fizeau/runtime", contribution, []string{"--fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/opt/fizeau/runtime/static/bin/tool" || !reflect.DeepEqual(arguments, []string{"--fixture"}) {
		t.Fatalf("direct launch = %q %q", command, arguments)
	}
}

func TestPortableRuntimeDynamicClosure(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	executable, loader := findPortableRuntimeDynamicFixture(t)
	libraryRoot := collectPortableRuntimeLibraries(t, executable, loader)

	request := PortableRuntimeDynamicClosureRequest{
		EntrypointSource: executable,
		EntrypointTarget: "dynamic/bin/tool",
		LoaderTarget:     "dynamic/loader/" + filepath.Base(loader),
		LibraryRoots: []PortableRuntimeSourceTree{{
			Source: libraryRoot,
			Target: "dynamic/lib",
		}},
		// Repeating the declared library tree as explicit runtime-lookup
		// coverage records the owning fixture's offline-probe evidence without
		// materializing the same tree twice.
		RuntimeTrees:  []PortableRuntimeSourceTree{{Source: libraryRoot, Target: "dynamic/lib"}},
		RuntimeLookup: PortableRuntimeLookupIncludedTrees,
	}
	withoutCoverage := request
	withoutCoverage.RuntimeTrees = nil
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, withoutCoverage); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("included runtime lookup without explicit coverage error = %v, want closure incomplete", err)
	}
	contribution, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, request)
	if err != nil {
		t.Fatalf("AnalyzePortableRuntimeDynamicClosure() error = %v", err)
	}
	if contribution.ClosureClass != PortableRuntimeClosureDynamic {
		t.Fatalf("closure class = %q, want dynamic", contribution.ClosureClass)
	}
	if contribution.Launch.InterpreterTarget != "" || contribution.Launch.LoaderTarget != request.LoaderTarget {
		t.Fatalf("dynamic launch = %#v", contribution.Launch)
	}
	if got, want := contribution.Launch.LibraryRootTargets, []string{"dynamic/lib"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("library roots = %q, want %q", got, want)
	}
	if got := countPortableRuntimeTreeTarget(contribution.Assets, "dynamic/lib"); got != 1 {
		t.Fatalf("deduplicated dynamic/lib tree count = %d, want 1", got)
	}
	command, arguments, err := BuildPortableRuntimeLaunchCommand("/opt/fizeau/runtime", contribution, []string{"request"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/opt/fizeau/runtime/"+request.LoaderTarget {
		t.Fatalf("command = %q, want bundled loader", command)
	}
	wantArguments := []string{
		"--library-path", "/opt/fizeau/runtime/dynamic/lib",
		"/opt/fizeau/runtime/dynamic/bin/tool", "request",
	}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("loader arguments = %q, want %q", arguments, wantArguments)
	}

	request.RuntimeLookup = ""
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, request); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("unknown runtime lookup error = %v, want closure incomplete", err)
	}

	missing, err := firstPortableRuntimeNeeded(executable)
	if err != nil {
		t.Fatal(err)
	}
	badLibraryRoot := collectPortableRuntimeLibraries(t, executable, loader)
	mutatePortableRuntimeELFHeader(t, filepath.Join(badLibraryRoot, missing), func(header []byte, order binary.ByteOrder) {
		order.PutUint16(header[16:18], uint16(elf.ET_EXEC))
	})
	badDependencyRequest := request
	badDependencyRequest.LibraryRoots = []PortableRuntimeSourceTree{{Source: badLibraryRoot, Target: "dynamic/bad-lib"}}
	badDependencyRequest.RuntimeTrees = []PortableRuntimeSourceTree{{Source: badLibraryRoot, Target: "dynamic/bad-lib"}}
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, badDependencyRequest); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("ET_EXEC dependency error = %v, want closure incomplete", err)
	}
	if err := os.Remove(filepath.Join(libraryRoot, missing)); err != nil {
		t.Fatal(err)
	}
	request.RuntimeLookup = PortableRuntimeLookupIncludedTrees
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, request); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("unresolved SONAME alias error = %v, want closure incomplete", err)
	}
}

func TestPortableRuntimeInterpretedClosure(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	packageTree := t.TempDir()
	if err := os.WriteFile(filepath.Join(packageTree, "package.json"), []byte(`{"name":"portable-fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "tool.js")
	absoluteShebang := "#!/account-bearing/host/path/node\nfixture\n"
	if err := os.WriteFile(script, []byte(absoluteShebang), 0o700); err != nil {
		t.Fatal(err)
	}
	staticInterpreter := buildPortableRuntimeStaticFixture(t)

	staticRequest := PortableRuntimeInterpretedClosureRequest{
		EntrypointSource:  script,
		EntrypointTarget:  "interpreted/script/tool.js",
		InterpreterSource: staticInterpreter,
		InterpreterTarget: "interpreted/bin/node",
		PackageTrees: []PortableRuntimeSourceTree{{
			Source: packageTree,
			Target: "interpreted/package",
		}},
		RuntimeTrees:  []PortableRuntimeSourceTree{{Source: packageTree, Target: "interpreted/package"}},
		RuntimeArgs:   []string{"--no-warnings"},
		RuntimeLookup: PortableRuntimeLookupIncludedTrees,
	}
	staticContribution, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, staticRequest)
	if err != nil {
		t.Fatalf("static-interpreter analysis error = %v", err)
	}
	command, arguments, err := BuildPortableRuntimeLaunchCommand("/opt/fizeau/runtime", staticContribution, []string{"request"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/opt/fizeau/runtime/interpreted/bin/node" {
		t.Fatalf("static interpreter command = %q", command)
	}
	if got := countPortableRuntimeTreeTarget(staticContribution.Assets, "interpreted/package"); got != 1 {
		t.Fatalf("deduplicated interpreted/package tree count = %d, want 1", got)
	}
	wantStaticArgs := []string{"--no-warnings", "/opt/fizeau/runtime/interpreted/script/tool.js", "request"}
	if !reflect.DeepEqual(arguments, wantStaticArgs) || strings.Contains(strings.Join(append([]string{command}, arguments...), " "), "/account-bearing/") {
		t.Fatalf("static interpreter recipe followed host shebang: %q %q", command, arguments)
	}

	dynamicInterpreter, loader := findPortableRuntimeDynamicFixture(t)
	libraryRoot := collectPortableRuntimeLibraries(t, dynamicInterpreter, loader)
	dynamicRequest := staticRequest
	dynamicRequest.InterpreterSource = dynamicInterpreter
	dynamicRequest.LoaderTarget = "interpreted/loader/" + filepath.Base(loader)
	dynamicRequest.LibraryRoots = []PortableRuntimeSourceTree{{Source: libraryRoot, Target: "interpreted/lib"}}
	dynamicContribution, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, dynamicRequest)
	if err != nil {
		t.Fatalf("dynamic-interpreter analysis error = %v", err)
	}
	command, arguments, err = BuildPortableRuntimeLaunchCommand("/opt/fizeau/runtime", dynamicContribution, []string{"request"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/opt/fizeau/runtime/"+dynamicRequest.LoaderTarget {
		t.Fatalf("dynamic interpreter command = %q, want bundled loader", command)
	}
	wantDynamicArgs := []string{
		"--library-path", "/opt/fizeau/runtime/interpreted/lib",
		"/opt/fizeau/runtime/interpreted/bin/node", "--no-warnings",
		"/opt/fizeau/runtime/interpreted/script/tool.js", "request",
	}
	if !reflect.DeepEqual(arguments, wantDynamicArgs) || strings.Contains(strings.Join(append([]string{command}, arguments...), " "), "/account-bearing/") {
		t.Fatalf("dynamic interpreter recipe followed PT_INTERP or shebang: %q %q", command, arguments)
	}
}

func TestPortableRuntimeClosureCanonicalDigestAndFailures(t *testing.T) {
	requirePortableRuntimeLinux(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "dir", "payload")
	if err := os.WriteFile(file, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir/payload", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}

	first, err := PortableRuntimeTreeDigest(root)
	if err != nil {
		t.Fatalf("PortableRuntimeTreeDigest() error = %v", err)
	}
	second, err := PortableRuntimeTreeDigest(root)
	if err != nil || first != second {
		t.Fatalf("canonical tree digest = %q, %v; want %q", second, err, first)
	}
	fileDigest, err := PortableRuntimeFileDigest(file)
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte("payload"))); err != nil || fileDigest != want {
		t.Fatalf("file digest = %q, %v; want %q", fileDigest, err, want)
	}

	reordered := t.TempDir()
	if err := os.Symlink("dir/payload", filepath.Join(reordered, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(reordered, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	reorderedFile := filepath.Join(reordered, "dir", "payload")
	if err := os.WriteFile(reorderedFile, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(reorderedFile, filepath.Join(reordered, "hardlink")); err != nil {
		t.Fatal(err)
	}
	reorderedDigest, err := PortableRuntimeTreeDigest(reordered)
	if err != nil || reorderedDigest != first {
		t.Fatalf("creation-order-independent tree digest = %q, %v; want %q", reorderedDigest, err, first)
	}
	if err := os.Chmod(file, 0o700); err != nil {
		t.Fatal(err)
	}
	changed, err := PortableRuntimeTreeDigest(root)
	if err != nil || changed == first {
		t.Fatalf("mode-mutated digest = %q, %v; want different", changed, err)
	}

	external := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(external, []byte("secret-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "external-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := PortableRuntimeTreeDigest(root); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), external) || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("external symlink error = %q; want redacted closure incomplete", err)
	}
	if err := os.Remove(filepath.Join(root, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(root, "directory-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := PortableRuntimeTreeDigest(root); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("directory symlink error = %v, want closure incomplete", err)
	}

	invalidTargetTree := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidTargetTree, `bad\target`), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PortableRuntimeTreeDigest(invalidTargetTree); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("invalid implicit target error = %v, want closure incomplete", err)
	}

	mutable := filepath.Join(t.TempDir(), "account-secret-source")
	if err := os.WriteFile(mutable, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = portableRuntimeFileDigestWithHook(mutable, func() {
		if writeErr := os.WriteFile(mutable, []byte("mutated-secret-value"), 0o600); writeErr != nil {
			t.Fatalf("mutating digest fixture: %v", writeErr)
		}
	})
	if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) || strings.Contains(err.Error(), mutable) || strings.Contains(err.Error(), "mutated-secret-value") {
		t.Fatalf("mutation error = %q; want redacted closure incomplete", err)
	}

	static := buildPortableRuntimeStaticFixture(t)
	_, err = AnalyzePortableRuntimeStaticClosure(context.Background(), PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}, PortableRuntimeStaticClosureRequest{
		EntrypointSource: static,
		EntrypointTarget: "../escape",
		RuntimeLookup:    PortableRuntimeLookupClosed,
	})
	if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("escaping target error = %v, want closure incomplete", err)
	}
}

func TestPortableRuntimeClosureRejectsPluginLookupSymbols(t *testing.T) {
	if !portableRuntimeHasPluginLookupSymbol([]elf.ImportedSymbol{{Name: "dlopen"}}) {
		t.Fatal("dlopen import was not classified as runtime lookup")
	}
	if !portableRuntimeHasPluginLookupSymbol([]elf.ImportedSymbol{{Name: "dlmopen"}}) {
		t.Fatal("dlmopen import was not classified as runtime lookup")
	}
	if portableRuntimeHasPluginLookupSymbol([]elf.ImportedSymbol{{Name: "ordinary_symbol"}}) {
		t.Fatal("ordinary import was classified as runtime lookup")
	}
}

func TestPortableRuntimeClosureELFMetadataValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
		want     string
		wantErr  bool
	}{
		{name: "terminated", contents: "/lib/ld.so\x00", want: "/lib/ld.so"},
		{name: "unterminated", contents: "/lib/ld.so", wantErr: true},
		{name: "embedded nul", contents: "/lib/ld.so\x00junk\x00", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePortableRuntimeELFInterpreter([]byte(test.contents))
			if test.wantErr {
				if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
					t.Fatalf("portableRuntimeELFInterpreter() error = %v, want closure incomplete", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("portableRuntimeELFInterpreter() = %q, %v; want %q", got, err, test.want)
			}
		})
	}

	for goarch, want := range map[string]elf.Data{
		"amd64":   elf.ELFDATA2LSB,
		"arm64":   elf.ELFDATA2LSB,
		"ppc64":   elf.ELFDATA2MSB,
		"s390x":   elf.ELFDATA2MSB,
		"unknown": elf.ELFDATANONE,
	} {
		if got := portableRuntimeELFData(goarch); got != want {
			t.Errorf("portableRuntimeELFData(%q) = %v, want %v", goarch, got, want)
		}
	}
}

func TestPortableRuntimeClosureELFPlatformAndTypes(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	executable := buildPortableRuntimeStaticFixture(t)

	nonLinux := copyPortableRuntimeELFFixture(t, executable)
	mutatePortableRuntimeELFHeader(t, nonLinux, func(header []byte, _ binary.ByteOrder) {
		header[7] = byte(elf.ELFOSABI_FREEBSD)
	})
	if _, err := AnalyzePortableRuntimeStaticClosure(context.Background(), target, PortableRuntimeStaticClosureRequest{
		EntrypointSource: nonLinux,
		EntrypointTarget: "platform/non-linux",
		RuntimeLookup:    PortableRuntimeLookupClosed,
	}); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("non-Linux OSABI error = %v, want closure incomplete", err)
	}

	staticPIE := copyPortableRuntimeELFFixture(t, executable)
	mutatePortableRuntimeELFHeader(t, staticPIE, func(header []byte, order binary.ByteOrder) {
		order.PutUint16(header[16:18], uint16(elf.ET_DYN))
	})
	if _, err := AnalyzePortableRuntimeStaticClosure(context.Background(), target, PortableRuntimeStaticClosureRequest{
		EntrypointSource: staticPIE,
		EntrypointTarget: "platform/static-pie",
		RuntimeLookup:    PortableRuntimeLookupClosed,
	}); err != nil {
		t.Fatalf("static ET_DYN analysis error = %v", err)
	}

	zeroEntryDSO := copyPortableRuntimeELFFixture(t, executable)
	mutatePortableRuntimeELFHeader(t, zeroEntryDSO, func(header []byte, order binary.ByteOrder) {
		order.PutUint16(header[16:18], uint16(elf.ET_DYN))
		switch elf.Class(header[4]) {
		case elf.ELFCLASS32:
			order.PutUint32(header[24:28], 0)
		case elf.ELFCLASS64:
			order.PutUint64(header[24:32], 0)
		default:
			t.Fatal("fixture has unsupported ELF class")
		}
	})
	if _, err := AnalyzePortableRuntimeStaticClosure(context.Background(), target, PortableRuntimeStaticClosureRequest{
		EntrypointSource: zeroEntryDSO,
		EntrypointTarget: "platform/not-an-executable",
		RuntimeLookup:    PortableRuntimeLookupClosed,
	}); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("zero-entry ET_DYN error = %v, want closure incomplete", err)
	}
}

func buildPortableRuntimeStaticFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "fixture")
	command := exec.Command("go", "build", "-o", executable, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOPROXY=off", "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build static ELF fixture: %v: %s", err, output)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func copyPortableRuntimeELFFixture(t *testing.T, source string) string {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(destination, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	return destination
}

func mutatePortableRuntimeELFHeader(t *testing.T, source string, mutate func([]byte, binary.ByteOrder)) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 18 || string(contents[:4]) != "\x7fELF" {
		t.Fatalf("fixture %q does not have a complete ELF header", source)
	}
	var order binary.ByteOrder
	switch elf.Data(contents[5]) {
	case elf.ELFDATA2LSB:
		order = binary.LittleEndian
	case elf.ELFDATA2MSB:
		order = binary.BigEndian
	default:
		t.Fatalf("fixture %q has unsupported ELF byte order", source)
	}
	mutate(contents, order)
	if err := os.WriteFile(source, contents, 0o700); err != nil {
		t.Fatal(err)
	}
}

func countPortableRuntimeTreeTarget(assets []PortableRuntimeAsset, target string) int {
	count := 0
	for _, asset := range assets {
		if asset.PathKind == PortableRuntimePathTree && asset.Target == target {
			count++
		}
	}
	return count
}

func findPortableRuntimeDynamicFixture(t *testing.T) (string, string) {
	t.Helper()
	for _, candidate := range []string{"/bin/true", "/usr/bin/true", "/bin/echo", "/usr/bin/env", "/bin/ls"} {
		file, err := elf.Open(candidate)
		if err != nil {
			continue
		}
		interpreter, interpreterErr := portableRuntimeELFInterpreter(file)
		_ = file.Close()
		if interpreterErr == nil && interpreter != "" && portableRuntimeRecognizedLoader(interpreter, runtime.GOARCH) {
			return candidate, interpreter
		}
	}
	t.Fatal("Linux test host has no recognized glibc/musl dynamic fixture")
	return "", ""
}

func collectPortableRuntimeLibraries(t *testing.T, executable, loader string) string {
	t.Helper()
	destination := t.TempDir()
	searchRoots := portableRuntimeHostLibraryRoots(loader)
	queue := []string{executable, loader}
	seen := make(map[string]struct{})
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		file, err := elf.Open(current)
		if err != nil {
			t.Fatalf("open dynamic fixture: %v", err)
		}
		libraries, err := file.ImportedLibraries()
		_ = file.Close()
		if err != nil {
			t.Fatalf("read fixture dependencies: %v", err)
		}
		sort.Strings(libraries)
		for _, library := range libraries {
			if _, exists := seen[library]; exists {
				continue
			}
			source := findPortableRuntimeHostLibrary(t, library, searchRoots)
			contents, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(destination, library), contents, 0o700); err != nil {
				t.Fatal(err)
			}
			seen[library] = struct{}{}
			queue = append(queue, source)
		}
	}
	return destination
}

func portableRuntimeHostLibraryRoots(loader string) []string {
	roots := []string{filepath.Dir(loader), "/lib", "/lib64", "/usr/lib", "/usr/lib64"}
	for _, pattern := range []string{"/lib/*-linux-gnu", "/usr/lib/*-linux-gnu"} {
		matches, _ := filepath.Glob(pattern)
		roots = append(roots, matches...)
	}
	unique := make(map[string]struct{})
	filtered := roots[:0]
	for _, root := range roots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if _, exists := unique[resolved]; exists {
			continue
		}
		unique[resolved] = struct{}{}
		filtered = append(filtered, resolved)
	}
	return filtered
}

func findPortableRuntimeHostLibrary(t *testing.T, name string, roots []string) string {
	t.Helper()
	for _, root := range roots {
		candidate := filepath.Join(root, name)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			if info, statErr := os.Stat(resolved); statErr == nil && info.Mode().IsRegular() {
				return resolved
			}
		}
	}
	t.Fatalf("cannot resolve dynamic fixture dependency %q in %q", name, roots)
	return ""
}

func firstPortableRuntimeNeeded(source string) (string, error) {
	file, err := elf.Open(source)
	if err != nil {
		return "", err
	}
	defer file.Close()
	libraries, err := file.ImportedLibraries()
	if err != nil {
		return "", err
	}
	if len(libraries) == 0 {
		return "", fmt.Errorf("fixture has no DT_NEEDED entries")
	}
	sort.Strings(libraries)
	return libraries[0], nil
}

func requirePortableRuntimeLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 closure analysis is Linux-only")
	}
}
