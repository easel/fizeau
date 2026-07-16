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

func TestPortableRuntimeDynamicExactLibraryClosure(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	executable, loader := findPortableRuntimeDynamicFixture(t)
	libraryRoot := collectPortableRuntimeLibraries(t, executable, loader)
	request := PortableRuntimeDynamicClosureRequest{
		EntrypointSource: executable,
		EntrypointTarget: "exact/bin/tool",
		LoaderTarget:     "exact/loader/" + filepath.Base(loader),
		ExactLibraryRoots: []PortableRuntimeLibrarySearchRoot{{
			Source: libraryRoot,
			Target: "exact/lib",
		}},
		RuntimeLookup: PortableRuntimeLookupClosed,
	}

	contribution, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, request)
	if err != nil {
		t.Fatalf("AnalyzePortableRuntimeDynamicClosure() exact error = %v", err)
	}
	if got, want := contribution.Launch.LibraryRootTargets, []string{"exact/lib"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exact library roots = %q, want %q", got, want)
	}
	libraryFiles := 0
	emittedLibraries := make(map[string]struct{})
	for _, asset := range contribution.Assets {
		if asset.PathKind == PortableRuntimePathTree {
			t.Fatalf("exact closure emitted whole tree asset: %#v", asset)
		}
		if strings.HasPrefix(asset.Target, "exact/lib/") {
			libraryFiles++
			emittedLibraries[strings.TrimPrefix(asset.Target, "exact/lib/")] = struct{}{}
			if asset.Kind != PortableRuntimeAssetSupport || asset.ContentSHA256 == "" {
				t.Fatalf("exact library asset = %#v, want digest-bound support file", asset)
			}
		}
	}
	if libraryFiles == 0 {
		t.Fatal("exact closure emitted no recursive library files")
	}
	expectedEntries, err := os.ReadDir(libraryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range expectedEntries {
		if _, exists := emittedLibraries[entry.Name()]; !exists {
			t.Fatalf("recursive executable/loader dependency %q was not emitted", entry.Name())
		}
	}
	command, arguments, err := BuildPortableRuntimeLaunchCommand("/runtime", contribution, []string{"request"})
	if err != nil {
		t.Fatal(err)
	}
	if command != "/runtime/"+request.LoaderTarget || !reflect.DeepEqual(arguments, []string{
		"--library-path", "/runtime/exact/lib", "/runtime/exact/bin/tool", "request",
	}) {
		t.Fatalf("exact loader recipe = %q %q", command, arguments)
	}

	verifiedExact := request
	verifiedExact.RuntimeLookup = PortableRuntimeLookupVerifiedExact
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, verifiedExact); err != nil {
		t.Fatalf("verified-exact dynamic analysis error = %v", err)
	}
	verifiedWithTree := verifiedExact
	verifiedWithTree.RuntimeTrees = []PortableRuntimeSourceTree{{Source: libraryRoot, Target: "exact/lib"}}
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, verifiedWithTree); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("verified-exact runtime tree error = %v, want closure incomplete", err)
	}
	verifiedTreeMode := verifiedExact
	verifiedTreeMode.LibraryRoots = []PortableRuntimeSourceTree{{Source: libraryRoot, Target: "exact/tree"}}
	verifiedTreeMode.ExactLibraryRoots = nil
	if _, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, verifiedTreeMode); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("verified-exact tree-backed libraries error = %v, want closure incomplete", err)
	}

	assertRedactedFailure := func(t *testing.T, got error, forbidden ...string) {
		t.Helper()
		if !errors.Is(got, ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("error = %v, want closure incomplete", got)
		}
		for _, value := range forbidden {
			if value != "" && strings.Contains(got.Error(), value) {
				t.Fatalf("error leaked %q: %v", value, got)
			}
		}
	}

	t.Run("missing dependency", func(t *testing.T) {
		missing := request
		missingRoot := filepath.Join(t.TempDir(), "account-secret-missing-root")
		if err := os.Mkdir(missingRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		missing.ExactLibraryRoots = []PortableRuntimeLibrarySearchRoot{{Source: missingRoot, Target: "exact/lib"}}
		_, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, missing)
		assertRedactedFailure(t, err, missingRoot)
	})

	t.Run("ambiguous SONAME", func(t *testing.T) {
		ambiguous := request
		secondRoot := collectPortableRuntimeLibraries(t, executable, loader)
		ambiguous.ExactLibraryRoots = append(ambiguous.ExactLibraryRoots,
			PortableRuntimeLibrarySearchRoot{Source: secondRoot, Target: "exact/lib-second"})
		_, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, ambiguous)
		assertRedactedFailure(t, err, libraryRoot, secondRoot)
	})

	t.Run("wrong dependency type and architecture", func(t *testing.T) {
		needed, err := firstPortableRuntimeNeeded(executable)
		if err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func([]byte, binary.ByteOrder){
			func(header []byte, order binary.ByteOrder) { order.PutUint16(header[16:18], uint16(elf.ET_EXEC)) },
			func(header []byte, order binary.ByteOrder) { order.PutUint16(header[18:20], uint16(elf.EM_NONE)) },
		} {
			bad := request
			badRoot := collectPortableRuntimeLibraries(t, executable, loader)
			mutatePortableRuntimeELFHeader(t, filepath.Join(badRoot, needed), mutate)
			bad.ExactLibraryRoots = []PortableRuntimeLibrarySearchRoot{{Source: badRoot, Target: "exact/lib"}}
			_, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, bad)
			assertRedactedFailure(t, err, badRoot)
		}

		pieRoot := collectPortableRuntimeLibraries(t, executable, loader)
		pie := buildPortableRuntimePIEFixture(t, needed)
		pieContents, err := os.ReadFile(pie)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pieRoot, needed), pieContents, 0o700); err != nil {
			t.Fatal(err)
		}
		pieRequest := request
		pieRequest.ExactLibraryRoots = []PortableRuntimeLibrarySearchRoot{{Source: pieRoot, Target: "exact/lib"}}
		_, err = AnalyzePortableRuntimeDynamicClosure(context.Background(), target, pieRequest)
		assertRedactedFailure(t, err, pieRoot, pie)
	})

	t.Run("mutation after inspection", func(t *testing.T) {
		mutableRoot := collectPortableRuntimeLibraries(t, executable, loader)
		roots, _, err := inspectPortableRuntimeLibraryRoots(nil, []PortableRuntimeLibrarySearchRoot{{Source: mutableRoot, Target: "exact/lib"}})
		if err != nil {
			t.Fatal(err)
		}
		_, _, executableELF, err := inspectPortableRuntimeELF(context.Background(), executable, target, portableRuntimeELFExecutable)
		if err != nil {
			t.Fatal(err)
		}
		libraries, err := resolvePortableRuntimeELFClosure(context.Background(), executableELF, target, roots, PortableRuntimeLookupClosed)
		_ = executableELF.Close()
		if err != nil || len(libraries) == 0 {
			t.Fatalf("resolve exact mutation fixture = %v, %d libraries", err, len(libraries))
		}
		secret := "mutated-library-secret"
		if err := os.WriteFile(libraries[0].source, []byte(secret), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err = appendPortableRuntimeResolvedLibraries(context.Background(), nil, libraries)
		assertRedactedFailure(t, err, mutableRoot, libraries[0].source, secret)

		raced := filepath.Join(t.TempDir(), "account-secret-open-race")
		original, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(raced, original, 0o700); err != nil {
			t.Fatal(err)
		}
		replacement := filepath.Join(t.TempDir(), "replacement")
		if err := os.WriteFile(replacement, original, 0o700); err != nil {
			t.Fatal(err)
		}
		_, _, opened, err := inspectPortableRuntimeELFWithHook(context.Background(), raced, target, portableRuntimeELFExecutable, func() {
			if renameErr := os.Rename(replacement, raced); renameErr != nil {
				t.Fatal(renameErr)
			}
		})
		closePortableRuntimeELF(opened)
		assertRedactedFailure(t, err, raced)
	})

	t.Run("target and mode collisions", func(t *testing.T) {
		duplicateTarget := request
		duplicateTarget.ExactLibraryRoots = append(duplicateTarget.ExactLibraryRoots,
			PortableRuntimeLibrarySearchRoot{Source: libraryRoot, Target: "exact/lib"})
		_, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, duplicateTarget)
		assertRedactedFailure(t, err)

		mixed := request
		mixed.LibraryRoots = []PortableRuntimeSourceTree{{Source: libraryRoot, Target: "exact/tree"}}
		_, err = AnalyzePortableRuntimeDynamicClosure(context.Background(), target, mixed)
		assertRedactedFailure(t, err)

		needed, neededErr := firstPortableRuntimeNeeded(executable)
		if neededErr != nil {
			t.Fatal(neededErr)
		}
		collision := request
		collision.LoaderTarget = "exact/lib/" + needed
		_, err = AnalyzePortableRuntimeDynamicClosure(context.Background(), target, collision)
		assertRedactedFailure(t, err)
	})

	t.Run("runtime lookup and RPATH", func(t *testing.T) {
		unknownLookup := request
		unknownLookup.RuntimeLookup = ""
		_, err := AnalyzePortableRuntimeDynamicClosure(context.Background(), target, unknownLookup)
		assertRedactedFailure(t, err)

		rpathExecutable := buildPortableRuntimeRPATHFixture(t)
		rpathFile, err := elf.Open(rpathExecutable)
		if err != nil {
			t.Fatal(err)
		}
		rpathLoader, err := portableRuntimeELFInterpreter(rpathFile)
		_ = rpathFile.Close()
		if err != nil {
			t.Fatal(err)
		}
		rpathRoot := collectPortableRuntimeLibraries(t, rpathExecutable, rpathLoader)
		rpathRequest := request
		rpathRequest.EntrypointSource = rpathExecutable
		rpathRequest.LoaderTarget = "exact/loader/" + filepath.Base(rpathLoader)
		rpathRequest.ExactLibraryRoots = []PortableRuntimeLibrarySearchRoot{{Source: rpathRoot, Target: "exact/lib"}}
		_, err = AnalyzePortableRuntimeDynamicClosure(context.Background(), target, rpathRequest)
		assertRedactedFailure(t, err, rpathExecutable, "account-secret-rpath")
	})
}

func TestPortableRuntimeNodeInterpreterBypassesShebangAndPATH(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	staticInterpreter := buildPortableRuntimeStaticFixture(t)
	dynamicInterpreter, loader := findPortableRuntimeDynamicFixture(t)
	libraryRoot := collectPortableRuntimeLibraries(t, dynamicInterpreter, loader)
	poisonedPath := t.TempDir()
	poisonedNode := filepath.Join(poisonedPath, "node")
	if err := os.WriteFile(poisonedNode, []byte("poisoned-path-node"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poisonedPath)

	absoluteScript := writePortableRuntimeNodeScript(t, "#!/account-bearing/host/path/node\nfixture\n")
	staticRequest := portableRuntimeNodeInterpretedRequest(t, absoluteScript, staticInterpreter)
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
	if staticContribution.Launch.InterpreterTarget != staticRequest.InterpreterTarget ||
		!reflect.DeepEqual(arguments, wantStaticArgs) ||
		strings.Contains(strings.Join(append([]string{command}, arguments...), " "), "/account-bearing/") ||
		strings.Contains(strings.Join(append([]string{command}, arguments...), " "), poisonedNode) {
		t.Fatalf("static interpreter recipe followed host shebang: %q %q", command, arguments)
	}

	envScript := writePortableRuntimeNodeScript(t, "#!/usr/bin/env node\nfixture\n")
	dynamicRequest := portableRuntimeNodeInterpretedRequest(t, envScript, dynamicInterpreter)
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
	if dynamicContribution.Launch.InterpreterTarget != dynamicRequest.InterpreterTarget ||
		!reflect.DeepEqual(arguments, wantDynamicArgs) ||
		strings.Contains(strings.Join(append([]string{command}, arguments...), " "), "/usr/bin/env") ||
		strings.Contains(strings.Join(append([]string{command}, arguments...), " "), poisonedNode) {
		t.Fatalf("dynamic interpreter recipe followed PT_INTERP or shebang: %q %q", command, arguments)
	}
}

func TestPortableRuntimeNodeInterpreterIdentity(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	staticInterpreter := buildPortableRuntimeStaticFixture(t)
	script := writePortableRuntimeNodeScript(t, "#!/usr/bin/env node\nfixture\n")
	exactIdentity := portableRuntimeFixtureFileIdentity(t, staticInterpreter)

	t.Run("exact regular source", func(t *testing.T) {
		request := portableRuntimeNodeInterpretedRequest(t, script, staticInterpreter)
		contribution, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		if err != nil {
			t.Fatalf("exact identity analysis error = %v", err)
		}
		assertPortableRuntimeInterpreterAsset(t, contribution, request.InterpreterTarget, staticInterpreter, exactIdentity)
	})

	t.Run("exact resolved symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "account-interpreter-link")
		if err := os.Symlink(staticInterpreter, link); err != nil {
			t.Fatal(err)
		}
		request := portableRuntimeNodeInterpretedRequest(t, script, link)
		contribution, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		if err != nil {
			t.Fatalf("symlink identity analysis error = %v", err)
		}
		assertPortableRuntimeInterpreterAsset(t, contribution, request.InterpreterTarget, staticInterpreter, exactIdentity)
	})

	validDigest := exactIdentity.ContentSHA256
	for _, test := range []struct {
		name     string
		identity PortableRuntimeFileIdentity
	}{
		{name: "zero", identity: PortableRuntimeFileIdentity{}},
		{name: "zero size", identity: PortableRuntimeFileIdentity{ContentSHA256: validDigest}},
		{name: "negative size", identity: PortableRuntimeFileIdentity{Size: -1, ContentSHA256: validDigest}},
		{name: "empty digest", identity: PortableRuntimeFileIdentity{Size: exactIdentity.Size}},
		{name: "short digest", identity: PortableRuntimeFileIdentity{Size: exactIdentity.Size, ContentSHA256: validDigest[:63]}},
		{name: "uppercase digest", identity: PortableRuntimeFileIdentity{Size: exactIdentity.Size, ContentSHA256: strings.ToUpper(validDigest)}},
		{name: "nonhex digest", identity: PortableRuntimeFileIdentity{Size: exactIdentity.Size, ContentSHA256: strings.Repeat("z", 64)}},
	} {
		t.Run("malformed "+test.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "account-secret-must-not-be-inspected")
			_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, PortableRuntimeInterpretedClosureRequest{
				InterpreterSource:   source,
				InterpreterIdentity: test.identity,
			})
			if err == nil || !strings.Contains(err.Error(), "invalid interpreter identity") {
				t.Fatalf("malformed identity error = %v, want pre-inspection identity rejection", err)
			}
			assertPortableRuntimeNodeInterpreterFailure(t, err, source, test.identity.ContentSHA256)
		})
	}

	t.Run("size mismatch", func(t *testing.T) {
		request := portableRuntimeNodeInterpretedRequest(t, script, staticInterpreter)
		request.InterpreterIdentity.Size++
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		assertPortableRuntimeNodeInterpreterFailure(t, err, staticInterpreter, validDigest, fmt.Sprint(exactIdentity.Size), fmt.Sprint(request.InterpreterIdentity.Size))
	})

	t.Run("digest mismatch", func(t *testing.T) {
		request := portableRuntimeNodeInterpretedRequest(t, script, staticInterpreter)
		request.InterpreterIdentity.ContentSHA256 = strings.Repeat("0", 64)
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		assertPortableRuntimeNodeInterpreterFailure(t, err, staticInterpreter, validDigest, request.InterpreterIdentity.ContentSHA256)
	})

	t.Run("dangling symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "account-secret-dangling")
		missing := filepath.Join(t.TempDir(), "missing-interpreter")
		if err := os.Symlink(missing, link); err != nil {
			t.Fatal(err)
		}
		request := portableRuntimeNodeInterpretedRequest(t, script, staticInterpreter)
		request.InterpreterSource = link
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		assertPortableRuntimeNodeInterpreterFailure(t, err, link, missing, validDigest)
	})

	t.Run("cyclic symlink", func(t *testing.T) {
		directory := t.TempDir()
		first := filepath.Join(directory, "account-secret-cycle-a")
		second := filepath.Join(directory, "account-secret-cycle-b")
		if err := os.Symlink(second, first); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(first, second); err != nil {
			t.Fatal(err)
		}
		request := portableRuntimeNodeInterpretedRequest(t, script, staticInterpreter)
		request.InterpreterSource = first
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		assertPortableRuntimeNodeInterpreterFailure(t, err, first, second, validDigest)
	})

	t.Run("replacement between resolution and open", func(t *testing.T) {
		installed := copyPortableRuntimeELFFixture(t, staticInterpreter)
		replacement, replacementSecret := portableRuntimeReplacementELFFixture(t, staticInterpreter)
		replacementIdentity := portableRuntimeFixtureFileIdentity(t, replacement)
		request := portableRuntimeNodeInterpretedRequest(t, script, installed)
		_, err := analyzePortableRuntimeInterpretedClosure(context.Background(), target, request, portableRuntimeInterpretedClosureHooks{
			afterInterpreterResolution: func() {
				if renameErr := os.Rename(replacement, installed); renameErr != nil {
					t.Fatal(renameErr)
				}
			},
		})
		if err == nil || !strings.Contains(err.Error(), "changed before it could be inspected") {
			t.Fatalf("pre-open replacement error = %v, want descriptor/path mismatch", err)
		}
		assertPortableRuntimeNodeInterpreterFailure(t, err,
			installed, replacement, request.InterpreterIdentity.ContentSHA256, replacementIdentity.ContentSHA256,
			fmt.Sprint(request.InterpreterIdentity.Size), fmt.Sprint(replacementIdentity.Size), replacementSecret)
	})

	t.Run("replacement after open", func(t *testing.T) {
		installed := copyPortableRuntimeELFFixture(t, staticInterpreter)
		replacement, replacementSecret := portableRuntimeReplacementELFFixture(t, staticInterpreter)
		replacementIdentity := portableRuntimeFixtureFileIdentity(t, replacement)
		request := portableRuntimeNodeInterpretedRequest(t, script, installed)
		_, err := analyzePortableRuntimeInterpretedClosure(context.Background(), target, request, portableRuntimeInterpretedClosureHooks{
			beforeInterpreterIdentityVerification: func() {
				if renameErr := os.Rename(replacement, installed); renameErr != nil {
					t.Fatal(renameErr)
				}
			},
		})
		if err == nil || !strings.Contains(err.Error(), "changed during identity verification") {
			t.Fatalf("post-open replacement error = %v, want final descriptor/path mismatch", err)
		}
		assertPortableRuntimeNodeInterpreterFailure(t, err,
			installed, replacement, request.InterpreterIdentity.ContentSHA256, replacementIdentity.ContentSHA256,
			fmt.Sprint(request.InterpreterIdentity.Size), fmt.Sprint(replacementIdentity.Size), replacementSecret)
	})

	t.Run("wrong architecture with matching identity", func(t *testing.T) {
		wrongArchitecture := copyPortableRuntimeELFFixture(t, staticInterpreter)
		mutatePortableRuntimeELFHeader(t, wrongArchitecture, func(header []byte, order binary.ByteOrder) {
			machine := elf.EM_X86_64
			if portableRuntimeELFMachine(runtime.GOARCH) == machine {
				machine = elf.EM_AARCH64
			}
			order.PutUint16(header[18:20], uint16(machine))
		})
		request := portableRuntimeNodeInterpretedRequest(t, script, wrongArchitecture)
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		if err == nil || !strings.Contains(err.Error(), "architecture") {
			t.Fatalf("wrong-architecture error = %v, want architecture rejection", err)
		}
		assertPortableRuntimeNodeInterpreterFailure(t, err, wrongArchitecture, request.InterpreterIdentity.ContentSHA256)
	})

	t.Run("not owner executable", func(t *testing.T) {
		nonExecutable := copyPortableRuntimeELFFixture(t, staticInterpreter)
		if err := os.Chmod(nonExecutable, 0o600); err != nil {
			t.Fatal(err)
		}
		request := portableRuntimeNodeInterpretedRequest(t, script, nonExecutable)
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		assertPortableRuntimeNodeInterpreterFailure(t, err, nonExecutable, request.InterpreterIdentity.ContentSHA256)
	})

	t.Run("not regular", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "account-secret-interpreter-directory")
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		request := portableRuntimeNodeInterpretedRequest(t, script, staticInterpreter)
		request.InterpreterSource = directory
		_, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
		assertPortableRuntimeNodeInterpreterFailure(t, err, directory, validDigest)
	})
}

func TestPortableRuntimeNodeInterpreterRejectsRPATH(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	homebrewRPATH := "/home/linuxbrew/.linuxbrew/opt/node/lib"
	interpreter := buildPortableRuntimeRPATHFixtureAt(t, homebrewRPATH)
	file, err := elf.Open(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	rpaths, rpathErr := file.DynString(elf.DT_RPATH)
	runpaths, runpathErr := file.DynString(elf.DT_RUNPATH)
	loader, loaderErr := portableRuntimeELFInterpreter(file)
	_ = file.Close()
	if rpathErr != nil || runpathErr != nil || loaderErr != nil {
		t.Fatalf("inspect Homebrew-like RPATH fixture: rpath=%v runpath=%v loader=%v", rpathErr, runpathErr, loaderErr)
	}
	if !reflect.DeepEqual(rpaths, []string{homebrewRPATH}) || len(runpaths) != 0 {
		t.Fatalf("dynamic search metadata = RPATH %q RUNPATH %q, want exact RPATH only", rpaths, runpaths)
	}

	libraryRoot := collectPortableRuntimeLibraries(t, interpreter, loader)
	script := writePortableRuntimeNodeScript(t, "#!/usr/bin/env node\nfixture\n")
	request := portableRuntimeNodeInterpretedRequest(t, script, interpreter)
	request.LoaderTarget = "interpreted/loader/" + filepath.Base(loader)
	request.LibraryRoots = []PortableRuntimeSourceTree{{Source: libraryRoot, Target: "interpreted/lib"}}
	_, err = AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
	if err == nil || !strings.Contains(err.Error(), "runtime search path") {
		t.Fatalf("Homebrew-like RPATH error = %v, want DT_RPATH rejection", err)
	}
	assertPortableRuntimeNodeInterpreterFailure(t, err, interpreter, homebrewRPATH, request.InterpreterIdentity.ContentSHA256)
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

func TestPortableRuntimeInterpretedTreeOwnedEntrypoint(t *testing.T) {
	requirePortableRuntimeLinux(t)
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	interpreter := buildPortableRuntimeStaticFixture(t)
	packageRoot := t.TempDir()
	entrypoint := filepath.Join(packageRoot, "bundle", "tool.js")
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("#!/usr/bin/env node\nfixture\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	otherEntrypoint := filepath.Join(packageRoot, "bundle", "other.js")
	if err := os.WriteFile(otherEntrypoint, []byte("#!/usr/bin/env node\nother\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	request := PortableRuntimeInterpretedClosureRequest{
		EntrypointSource:            entrypoint,
		EntrypointTarget:            "interpreted/package/bundle/tool.js",
		EntrypointPackageTreeTarget: "interpreted/package",
		InterpreterSource:           interpreter,
		InterpreterIdentity:         portableRuntimeFixtureFileIdentity(t, interpreter),
		InterpreterTarget:           "interpreted/bin/node",
		PackageTrees:                []PortableRuntimeSourceTree{{Source: packageRoot, Target: "interpreted/package"}},
		RuntimeLookup:               PortableRuntimeLookupClosed,
	}
	contribution, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, request)
	if err != nil {
		t.Fatal(err)
	}
	if contribution.Launch.EntrypointTreeMember != "bundle/tool.js" {
		t.Fatalf("tree-owned launch = %#v", contribution.Launch)
	}
	for _, asset := range contribution.Assets {
		if asset.Target == request.EntrypointTarget {
			t.Fatalf("tree-owned entrypoint was duplicated as an asset: %#v", asset)
		}
	}

	for _, test := range []struct {
		name   string
		mutate func(*PortableRuntimeInterpretedClosureRequest)
	}{
		{"missing tree", func(r *PortableRuntimeInterpretedClosureRequest) { r.EntrypointPackageTreeTarget = "missing/tree" }},
		{"target drift", func(r *PortableRuntimeInterpretedClosureRequest) {
			r.EntrypointTarget = "interpreted/package/bundle/other.js"
		}},
		{"source outside", func(r *PortableRuntimeInterpretedClosureRequest) {
			r.EntrypointSource = writePortableRuntimeNodeScript(t, "#!/usr/bin/env node\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request
			test.mutate(&changed)
			if _, err := AnalyzePortableRuntimeInterpretedClosure(context.Background(), target, changed); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("tree-owned entrypoint mutation error = %v", err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*PortableRuntimeContribution)
	}{
		{"missing member", func(c *PortableRuntimeContribution) { c.Launch.EntrypointTreeMember = "" }},
		{"target-only drift to existing member", func(c *PortableRuntimeContribution) {
			c.Launch.EntrypointTarget = "interpreted/package/bundle/other.js"
		}},
		{"member-only drift to existing member", func(c *PortableRuntimeContribution) {
			c.Launch.EntrypointTreeMember = "bundle/other.js"
		}},
		{"tree-owner drift", func(c *PortableRuntimeContribution) {
			for i := range c.Assets {
				if c.Assets[i].Target == "interpreted/package" {
					c.Assets[i].Target = "interpreted/other-package"
				}
			}
		}},
	} {
		t.Run("normalize "+test.name, func(t *testing.T) {
			changed := contribution
			changed.Assets = append([]PortableRuntimeAsset(nil), contribution.Assets...)
			test.mutate(&changed)
			if _, err := NormalizePortableRuntimeContribution(target, changed); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("normalization mutation error = %v", err)
			}
		})
	}
}

func TestPortableRuntimeInterpretedClosureOmitsExplicitLoaderDependency(t *testing.T) {
	requirePortableRuntimeLinux(t)
	_, loader := findPortableRuntimeDynamicFixture(t)
	resolved, err := filepath.EvalSymlinks(loader)
	if err != nil {
		t.Fatal(err)
	}
	loaderInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	other := copyPortableRuntimeELFFixture(t, resolved)
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	libraries := []portableRuntimeResolvedLibrary{
		{source: resolved, target: "interpreted/lib/" + filepath.Base(resolved), info: loaderInfo},
		{source: other, target: "interpreted/lib/other.so", info: otherInfo},
	}
	filtered, err := omitPortableRuntimeExplicitLoader(libraries, resolved, loaderInfo)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].source != other {
		t.Fatalf("loader de-dup retained the wrong dependency set")
	}
	if _, err := omitPortableRuntimeExplicitLoader(libraries, resolved, otherInfo); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("loader identity drift error = %v", err)
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

func writePortableRuntimeNodeScript(t *testing.T, contents string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "tool.js")
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func portableRuntimeNodeInterpretedRequest(t *testing.T, script, interpreter string) PortableRuntimeInterpretedClosureRequest {
	t.Helper()
	packageTree := t.TempDir()
	if err := os.WriteFile(filepath.Join(packageTree, "package.json"), []byte(`{"name":"portable-fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return PortableRuntimeInterpretedClosureRequest{
		EntrypointSource:    script,
		EntrypointTarget:    "interpreted/script/tool.js",
		InterpreterSource:   interpreter,
		InterpreterIdentity: portableRuntimeFixtureFileIdentity(t, interpreter),
		InterpreterTarget:   "interpreted/bin/node",
		PackageTrees: []PortableRuntimeSourceTree{{
			Source: packageTree,
			Target: "interpreted/package",
		}},
		RuntimeTrees:  []PortableRuntimeSourceTree{{Source: packageTree, Target: "interpreted/package"}},
		RuntimeArgs:   []string{"--no-warnings"},
		RuntimeLookup: PortableRuntimeLookupIncludedTrees,
	}
}

func portableRuntimeFixtureFileIdentity(t *testing.T, source string) PortableRuntimeFileIdentity {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	return PortableRuntimeFileIdentity{Size: info.Size(), ContentSHA256: fmt.Sprintf("%x", digest)}
}

func assertPortableRuntimeInterpreterAsset(t *testing.T, contribution PortableRuntimeContribution, target, source string, identity PortableRuntimeFileIdentity) {
	t.Helper()
	for _, asset := range contribution.Assets {
		if asset.Target != target {
			continue
		}
		if asset.Source != source || asset.ContentSHA256 != identity.ContentSHA256 || !asset.Executable || asset.PathKind != PortableRuntimePathFile {
			t.Fatalf("interpreter asset = %#v, want exact descriptor-derived identity", asset)
		}
		return
	}
	t.Fatalf("interpreter asset target %q is absent", target)
}

func assertPortableRuntimeNodeInterpreterFailure(t *testing.T, err error, forbidden ...string) {
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

func portableRuntimeReplacementELFFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	replacement := copyPortableRuntimeELFFixture(t, source)
	secret := "seeded-replacement-binary-contents"
	file, err := os.OpenFile(replacement, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(secret); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return replacement, secret
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

func buildPortableRuntimeRPATHFixture(t *testing.T) string {
	return buildPortableRuntimeRPATHFixtureAt(t, "/account-secret-rpath")
}

func buildPortableRuntimeRPATHFixtureAt(t *testing.T, rpath string) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.c")
	if err := os.WriteFile(source, []byte("int main(void) { return 0; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "fixture")
	command := exec.Command("cc", "-Wl,--disable-new-dtags,-rpath,"+rpath, "-o", executable, source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build RPATH ELF fixture: %v: %s", err, output)
	}
	return executable
}

func buildPortableRuntimePIEFixture(t *testing.T, soname string) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.c")
	if err := os.WriteFile(source, []byte("int main(void) { return 0; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "fixture")
	command := exec.Command("cc", "-fPIE", "-pie", "-Wl,-soname,"+soname, "-o", executable, source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build PIE ELF fixture: %v: %s", err, output)
	}
	return executable
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
