package harnesses

import (
	"bufio"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/easel/fizeau/internal/safefs"
)

// PortableRuntimeSourceTree maps one complete host tree to one private guest
// tree. Contributors must name roots explicitly; closure analysis never falls
// back to PATH, the host loader cache, or a package manager.
type PortableRuntimeSourceTree struct {
	Source string
	Target string
}

// PortableRuntimeLibrarySearchRoot maps one host ELF search directory to one
// private guest search directory. Unlike PortableRuntimeSourceTree, discovery
// emits only the recursive dependency files actually selected from this root.
type PortableRuntimeLibrarySearchRoot struct {
	Source string
	Target string
}

// PortableRuntimeLookupPolicy records the contributor's evidence about
// runtime-only lookup (for example dlopen or plugin discovery). The zero value
// is deliberately invalid so an unknown installed layout fails closed.
type PortableRuntimeLookupPolicy string

type portableRuntimeELFRole uint8

type portableRuntimeELFFile struct {
	*elf.File
	descriptor *os.File
}

func (file *portableRuntimeELFFile) Close() error {
	if file == nil {
		return nil
	}
	elfErr := file.File.Close()
	descriptorErr := file.descriptor.Close()
	if elfErr != nil {
		return elfErr
	}
	return descriptorErr
}

const (
	portableRuntimeELFExecutable portableRuntimeELFRole = iota + 1
	portableRuntimeELFLoader
	portableRuntimeELFDependency
)

const (
	// PortableRuntimeLookupClosed means the recognized layout has no additional
	// runtime-only lookup beyond its statically discoverable closure.
	PortableRuntimeLookupClosed PortableRuntimeLookupPolicy = "closed"
	// PortableRuntimeLookupIncludedTrees means the declared runtime/package
	// trees contain the runtime-only lookup surface exercised by the owning
	// harness's offline layout probe.
	PortableRuntimeLookupIncludedTrees PortableRuntimeLookupPolicy = "included_trees"
	// PortableRuntimeLookupVerifiedExact means the contributor's offline probe
	// verified that a recognized single-file runtime loads no executable or
	// library code beyond the exact dependency files. It is valid only with
	// ExactLibraryRoots and no runtime trees.
	PortableRuntimeLookupVerifiedExact PortableRuntimeLookupPolicy = "verified_exact"
)

// PortableRuntimeStaticClosureRequest describes a recognized static Linux
// executable layout.
type PortableRuntimeStaticClosureRequest struct {
	EntrypointSource string
	EntrypointTarget string
	RuntimeLookup    PortableRuntimeLookupPolicy
	RuntimeTrees     []PortableRuntimeSourceTree
}

// PortableRuntimeDynamicClosureRequest describes a recognized dynamically
// linked Linux executable layout. Tree-backed LibraryRoots are searched in
// order. ExactLibraryRoots require one unique candidate for every dependency;
// their original order is retained after unused roots are removed.
type PortableRuntimeDynamicClosureRequest struct {
	EntrypointSource string
	EntrypointTarget string
	LoaderTarget     string
	LibraryRoots     []PortableRuntimeSourceTree
	// ExactLibraryRoots emit only the recursive DT_NEEDED files selected from
	// unique candidates. Exactly one of LibraryRoots and ExactLibraryRoots must
	// be set.
	ExactLibraryRoots []PortableRuntimeLibrarySearchRoot
	RuntimeLookup     PortableRuntimeLookupPolicy
	RuntimeTrees      []PortableRuntimeSourceTree
}

// PortableRuntimeInterpretedClosureRequest describes a recognized launcher,
// interpreter, and package-tree layout. RuntimeArgs are fixed interpreter
// arguments; request arguments are appended only when the recipe is activated.
type PortableRuntimeInterpretedClosureRequest struct {
	EntrypointSource    string
	EntrypointTarget    string
	InterpreterSource   string
	InterpreterIdentity PortableRuntimeFileIdentity
	InterpreterTarget   string
	LoaderTarget        string
	LibraryRoots        []PortableRuntimeSourceTree
	ExactLibraryRoots   []PortableRuntimeLibrarySearchRoot
	PackageTrees        []PortableRuntimeSourceTree
	RuntimeArgs         []string
	RuntimeLookup       PortableRuntimeLookupPolicy
	RuntimeTrees        []PortableRuntimeSourceTree
}

type portableRuntimeInterpretedClosureHooks struct {
	afterInterpreterResolution            func()
	beforeInterpreterIdentityVerification func()
}

// AnalyzePortableRuntimeStaticClosure resolves a symlinked launcher, verifies
// a same-architecture Linux ELF without PT_INTERP, and emits a direct launch.
func AnalyzePortableRuntimeStaticClosure(ctx context.Context, target PortableRuntimeTarget, request PortableRuntimeStaticClosureRequest) (PortableRuntimeContribution, error) {
	if err := ValidatePortableRuntimeTarget(target); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if err := validatePortableRuntimeLookup(request.RuntimeLookup, request.RuntimeTrees); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if request.RuntimeLookup == PortableRuntimeLookupVerifiedExact {
		return PortableRuntimeContribution{}, closureError("static layout cannot claim verified exact dynamic lookup")
	}

	entrypoint, info, executable, err := inspectPortableRuntimeELF(ctx, request.EntrypointSource, target, portableRuntimeELFExecutable)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	defer executable.Close()
	if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		return PortableRuntimeContribution{}, closureError("static entrypoint is not an owner-executable regular file")
	}
	interpreter, err := portableRuntimeELFInterpreter(executable.File)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if interpreter != "" {
		return PortableRuntimeContribution{}, closureError("static entrypoint has an ELF interpreter")
	}
	if err := validatePortableRuntimeELFDynamicLookup(executable.File, request.RuntimeLookup); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if libraries, libraryErr := executable.ImportedLibraries(); libraryErr != nil || len(libraries) != 0 {
		return PortableRuntimeContribution{}, closureError("static entrypoint has an unverifiable dynamic dependency table")
	}

	digest, err := portableRuntimeDigestInspectedFile(entrypoint, info)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	assets := []PortableRuntimeAsset{{
		Kind:          PortableRuntimeAssetExecutable,
		PathKind:      PortableRuntimePathFile,
		Source:        entrypoint,
		Target:        request.EntrypointTarget,
		ContentSHA256: digest,
		Executable:    true,
	}}
	assets, err = appendPortableRuntimeTrees(ctx, assets, request.RuntimeTrees, PortableRuntimeAssetSupport)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	return NormalizePortableRuntimeContribution(target, PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureStatic,
		Launch:       PortableRuntimeLaunch{EntrypointTarget: request.EntrypointTarget},
		Assets:       assets,
	})
}

// AnalyzePortableRuntimeDynamicClosure verifies PT_INTERP and every recursive
// DT_NEEDED edge against the explicitly declared library roots, then emits a
// loader --library-path recipe. The copied binary's PT_INTERP is never used.
func AnalyzePortableRuntimeDynamicClosure(ctx context.Context, target PortableRuntimeTarget, request PortableRuntimeDynamicClosureRequest) (PortableRuntimeContribution, error) {
	if err := ValidatePortableRuntimeTarget(target); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if request.LoaderTarget == "" {
		return PortableRuntimeContribution{}, closureError("dynamic layout lacks explicit loader or library roots")
	}
	roots, exact, err := inspectPortableRuntimeLibraryRoots(request.LibraryRoots, request.ExactLibraryRoots)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if request.RuntimeLookup == PortableRuntimeLookupVerifiedExact && !exact {
		return PortableRuntimeContribution{}, closureError("verified exact lookup requires exact library roots")
	}
	if err := validatePortableRuntimeLookup(request.RuntimeLookup, request.RuntimeTrees); err != nil {
		return PortableRuntimeContribution{}, err
	}

	entrypoint, info, executable, err := inspectPortableRuntimeELF(ctx, request.EntrypointSource, target, portableRuntimeELFExecutable)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		closePortableRuntimeELF(executable)
		return PortableRuntimeContribution{}, closureError("dynamic entrypoint is not an owner-executable regular file")
	}
	interpreter, err := portableRuntimeELFInterpreter(executable.File)
	if err != nil {
		closePortableRuntimeELF(executable)
		return PortableRuntimeContribution{}, err
	}
	if interpreter == "" {
		closePortableRuntimeELF(executable)
		return PortableRuntimeContribution{}, closureError("dynamic entrypoint has no ELF interpreter")
	}
	if !portableRuntimeRecognizedLoader(interpreter, target.GOARCH) {
		closePortableRuntimeELF(executable)
		return PortableRuntimeContribution{}, closureError("ELF loader does not support the portable launch recipe")
	}

	resolvedLibraries, err := resolvePortableRuntimeELFClosure(ctx, executable, target, roots, request.RuntimeLookup)
	if err != nil {
		closePortableRuntimeELF(executable)
		return PortableRuntimeContribution{}, err
	}
	if err := executable.Close(); err != nil {
		return PortableRuntimeContribution{}, closureError("could not finish inspecting dynamic entrypoint")
	}

	loader, loaderInfo, loaderELF, err := inspectPortableRuntimeELF(ctx, interpreter, target, portableRuntimeELFLoader)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if loaderInfo.Mode().Perm()&0100 == 0 {
		closePortableRuntimeELF(loaderELF)
		return PortableRuntimeContribution{}, closureError("ELF loader is not owner-executable")
	}
	if portableRuntimeELFHasInterpreter(loaderELF.File) {
		closePortableRuntimeELF(loaderELF)
		return PortableRuntimeContribution{}, closureError("ELF loader declares an unsupported interpreter")
	}
	loaderLibraries, err := resolvePortableRuntimeELFClosure(ctx, loaderELF, target, roots, request.RuntimeLookup)
	if err != nil {
		closePortableRuntimeELF(loaderELF)
		return PortableRuntimeContribution{}, err
	}
	resolvedLibraries, err = mergePortableRuntimeResolvedLibraries(resolvedLibraries, loaderLibraries)
	if err != nil {
		closePortableRuntimeELF(loaderELF)
		return PortableRuntimeContribution{}, err
	}
	if err := loaderELF.Close(); err != nil {
		return PortableRuntimeContribution{}, closureError("could not finish inspecting ELF loader")
	}
	if exact {
		roots = portableRuntimeUsedExactRoots(roots, resolvedLibraries)
		if len(roots) == 0 {
			return PortableRuntimeContribution{}, closureError("exact library closure resolved no library roots")
		}
	}

	entryDigest, err := portableRuntimeDigestInspectedFile(entrypoint, info)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	loaderDigest, err := portableRuntimeDigestInspectedFile(loader, loaderInfo)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	assets := []PortableRuntimeAsset{
		{Kind: PortableRuntimeAssetExecutable, PathKind: PortableRuntimePathFile, Source: entrypoint, Target: request.EntrypointTarget, ContentSHA256: entryDigest, Executable: true},
		{Kind: PortableRuntimeAssetSupport, PathKind: PortableRuntimePathFile, Source: loader, Target: request.LoaderTarget, ContentSHA256: loaderDigest, Executable: true},
	}
	if exact {
		assets, err = appendPortableRuntimeResolvedLibraries(ctx, assets, resolvedLibraries)
	} else {
		assets, err = appendInspectedPortableRuntimeRoots(ctx, assets, roots, PortableRuntimeAssetInstallTree)
	}
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	assets, err = appendPortableRuntimeTrees(ctx, assets, request.RuntimeTrees, PortableRuntimeAssetSupport)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	libraryTargets := make([]string, len(roots))
	for i := range roots {
		libraryTargets[i] = roots[i].target
	}
	return NormalizePortableRuntimeContribution(target, PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureDynamic,
		Launch: PortableRuntimeLaunch{
			EntrypointTarget:   request.EntrypointTarget,
			LoaderTarget:       request.LoaderTarget,
			LibraryRootTargets: libraryTargets,
		},
		Assets: assets,
	})
}

// AnalyzePortableRuntimeInterpretedClosure verifies a script launcher and the
// interpreter's complete static or dynamic ELF closure. Its launch recipe
// invokes the bundled interpreter directly and never follows the shebang.
func AnalyzePortableRuntimeInterpretedClosure(ctx context.Context, target PortableRuntimeTarget, request PortableRuntimeInterpretedClosureRequest) (PortableRuntimeContribution, error) {
	return analyzePortableRuntimeInterpretedClosure(ctx, target, request, portableRuntimeInterpretedClosureHooks{})
}

func analyzePortableRuntimeInterpretedClosure(ctx context.Context, target PortableRuntimeTarget, request PortableRuntimeInterpretedClosureRequest, hooks portableRuntimeInterpretedClosureHooks) (PortableRuntimeContribution, error) {
	if err := ValidatePortableRuntimeTarget(target); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if !validPortableRuntimeFileIdentity(request.InterpreterIdentity) {
		return PortableRuntimeContribution{}, closureError("interpreted layout has an invalid interpreter identity")
	}
	if len(request.PackageTrees) == 0 {
		return PortableRuntimeContribution{}, closureError("interpreted layout lacks a package tree")
	}
	if err := validatePortableRuntimeLookup(request.RuntimeLookup, request.RuntimeTrees); err != nil {
		return PortableRuntimeContribution{}, err
	}
	entrypoint, entryInfo, err := inspectPortableRuntimeScript(ctx, request.EntrypointSource)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if !entryInfo.Mode().IsRegular() {
		return PortableRuntimeContribution{}, closureError("interpreted entrypoint is not a regular file")
	}

	interpreter, interpreterInfo, interpreterELF, err := inspectPortableRuntimeELFWithHook(ctx, request.InterpreterSource, target, portableRuntimeELFExecutable, hooks.afterInterpreterResolution)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if interpreterInfo.Mode().Perm()&0100 == 0 {
		closePortableRuntimeELF(interpreterELF)
		return PortableRuntimeContribution{}, closureError("interpreter is not owner-executable")
	}
	elfInterpreter, err := portableRuntimeELFInterpreter(interpreterELF.File)
	if err != nil {
		closePortableRuntimeELF(interpreterELF)
		return PortableRuntimeContribution{}, err
	}

	var roots []portableRuntimeInspectedRoot
	var resolvedLibraries []portableRuntimeResolvedLibrary
	exactLibraries := false
	var loader string
	var loaderELF *portableRuntimeELFFile
	var loaderInfo os.FileInfo
	if elfInterpreter == "" {
		if request.RuntimeLookup == PortableRuntimeLookupVerifiedExact {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("static interpreter cannot claim verified exact dynamic lookup")
		}
		if request.LoaderTarget != "" || len(request.LibraryRoots) != 0 || len(request.ExactLibraryRoots) != 0 {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("static interpreter layout declares dynamic loader state")
		}
		if libraries, libraryErr := interpreterELF.ImportedLibraries(); libraryErr != nil || len(libraries) != 0 {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("static interpreter has an unverifiable dynamic dependency table")
		}
		if err := validatePortableRuntimeELFDynamicLookup(interpreterELF.File, request.RuntimeLookup); err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
	} else {
		if request.LoaderTarget == "" {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("dynamic interpreter layout lacks explicit loader or library roots")
		}
		if !portableRuntimeRecognizedLoader(elfInterpreter, target.GOARCH) {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("interpreter loader does not support the portable launch recipe")
		}
		roots, exactLibraries, err = inspectPortableRuntimeLibraryRoots(request.LibraryRoots, request.ExactLibraryRoots)
		if err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
		if request.RuntimeLookup == PortableRuntimeLookupVerifiedExact && !exactLibraries {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("verified exact lookup requires exact library roots")
		}
		resolvedLibraries, err = resolvePortableRuntimeELFClosure(ctx, interpreterELF, target, roots, request.RuntimeLookup)
		if err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
		loader, loaderInfo, loaderELF, err = inspectPortableRuntimeELF(ctx, elfInterpreter, target, portableRuntimeELFLoader)
		if err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
		if loaderInfo.Mode().Perm()&0100 == 0 {
			closePortableRuntimeELF(loaderELF)
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("interpreter ELF loader is not owner-executable")
		}
		if portableRuntimeELFHasInterpreter(loaderELF.File) {
			closePortableRuntimeELF(loaderELF)
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("interpreter ELF loader declares an unsupported interpreter")
		}
		loaderLibraries, resolveErr := resolvePortableRuntimeELFClosure(ctx, loaderELF, target, roots, request.RuntimeLookup)
		if resolveErr != nil {
			closePortableRuntimeELF(loaderELF)
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, resolveErr
		}
		resolvedLibraries, err = mergePortableRuntimeResolvedLibraries(resolvedLibraries, loaderLibraries)
		if err != nil {
			closePortableRuntimeELF(loaderELF)
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
		if err := loaderELF.Close(); err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("could not finish inspecting interpreter loader")
		}
		if exactLibraries {
			roots = portableRuntimeUsedExactRoots(roots, resolvedLibraries)
			if len(roots) == 0 {
				closePortableRuntimeELF(interpreterELF)
				return PortableRuntimeContribution{}, closureError("exact interpreter closure resolved no library roots")
			}
		}
	}
	if hooks.beforeInterpreterIdentityVerification != nil {
		hooks.beforeInterpreterIdentityVerification()
	}
	interpreterDigest, err := verifyPortableRuntimeInterpreterIdentity(interpreter, interpreterInfo, interpreterELF, request.InterpreterIdentity)
	if err != nil {
		closePortableRuntimeELF(interpreterELF)
		return PortableRuntimeContribution{}, err
	}
	if err := interpreterELF.Close(); err != nil {
		return PortableRuntimeContribution{}, closureError("could not finish inspecting interpreter")
	}

	entryDigest, err := portableRuntimeDigestInspectedFile(entrypoint, entryInfo)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	assets := []PortableRuntimeAsset{
		{Kind: PortableRuntimeAssetExecutable, PathKind: PortableRuntimePathFile, Source: entrypoint, Target: request.EntrypointTarget, ContentSHA256: entryDigest},
		{Kind: PortableRuntimeAssetSupport, PathKind: PortableRuntimePathFile, Source: interpreter, Target: request.InterpreterTarget, ContentSHA256: interpreterDigest, Executable: true},
	}
	if loader != "" {
		loaderDigest, digestErr := portableRuntimeDigestInspectedFile(loader, loaderInfo)
		if digestErr != nil {
			return PortableRuntimeContribution{}, digestErr
		}
		assets = append(assets, PortableRuntimeAsset{Kind: PortableRuntimeAssetSupport, PathKind: PortableRuntimePathFile, Source: loader, Target: request.LoaderTarget, ContentSHA256: loaderDigest, Executable: true})
		if exactLibraries {
			assets, err = appendPortableRuntimeResolvedLibraries(ctx, assets, resolvedLibraries)
		} else {
			assets, err = appendInspectedPortableRuntimeRoots(ctx, assets, roots, PortableRuntimeAssetInstallTree)
		}
		if err != nil {
			return PortableRuntimeContribution{}, err
		}
	}
	assets, err = appendPortableRuntimeTrees(ctx, assets, request.PackageTrees, PortableRuntimeAssetInstallTree)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	assets, err = appendPortableRuntimeTrees(ctx, assets, request.RuntimeTrees, PortableRuntimeAssetSupport)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	libraryTargets := make([]string, len(roots))
	for i := range roots {
		libraryTargets[i] = roots[i].target
	}
	return NormalizePortableRuntimeContribution(target, PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureInterpreted,
		Launch: PortableRuntimeLaunch{
			EntrypointTarget:   request.EntrypointTarget,
			InterpreterTarget:  request.InterpreterTarget,
			LoaderTarget:       request.LoaderTarget,
			RuntimeArgs:        append([]string(nil), request.RuntimeArgs...),
			LibraryRootTargets: libraryTargets,
		},
		Assets: assets,
	})
}

func validPortableRuntimeFileIdentity(identity PortableRuntimeFileIdentity) bool {
	return identity.Size > 0 && validPortableRuntimeDigest(identity.ContentSHA256)
}

// BuildPortableRuntimeLaunchCommand expands a typed launch recipe below one
// fixed guest root. It never consults PATH, PT_INTERP, or a shebang.
func BuildPortableRuntimeLaunchCommand(guestRoot string, contribution PortableRuntimeContribution, requestArgv []string) (string, []string, error) {
	if guestRoot == "" || !filepath.IsAbs(guestRoot) || filepath.Clean(guestRoot) != guestRoot {
		return "", nil, closureError("launch has invalid guest root")
	}
	if err := validatePortableRuntimeLaunch(contribution); err != nil {
		return "", nil, err
	}
	guestTarget := func(target string) (string, error) {
		if !validPortableRuntimeTargetPath(target) {
			return "", closureError("launch contains an invalid guest target")
		}
		joined := filepath.Join(guestRoot, filepath.FromSlash(target))
		relative, err := filepath.Rel(guestRoot, joined)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", closureError("launch target escapes guest root")
		}
		return joined, nil
	}
	entrypoint, err := guestTarget(contribution.Launch.EntrypointTarget)
	if err != nil {
		return "", nil, err
	}
	for _, argument := range requestArgv {
		if strings.ContainsRune(argument, '\x00') {
			return "", nil, closureError("request argument contains an invalid byte")
		}
	}
	requestCopy := append([]string(nil), requestArgv...)

	switch contribution.ClosureClass {
	case PortableRuntimeClosureStatic:
		return entrypoint, requestCopy, nil
	case PortableRuntimeClosureDynamic:
		loader, roots, err := expandPortableRuntimeLoaderRecipe(guestTarget, contribution.Launch)
		if err != nil {
			return "", nil, err
		}
		arguments := []string{"--library-path", strings.Join(roots, ":"), entrypoint}
		return loader, append(arguments, requestCopy...), nil
	case PortableRuntimeClosureInterpreted:
		interpreter, err := guestTarget(contribution.Launch.InterpreterTarget)
		if err != nil {
			return "", nil, err
		}
		arguments := make([]string, 0, len(contribution.Launch.RuntimeArgs)+len(requestCopy)+2)
		if contribution.Launch.LoaderTarget != "" {
			loader, roots, loaderErr := expandPortableRuntimeLoaderRecipe(guestTarget, contribution.Launch)
			if loaderErr != nil {
				return "", nil, loaderErr
			}
			arguments = append(arguments, "--library-path", strings.Join(roots, ":"), interpreter)
			arguments = append(arguments, contribution.Launch.RuntimeArgs...)
			arguments = append(arguments, entrypoint)
			return loader, append(arguments, requestCopy...), nil
		}
		arguments = append(arguments, contribution.Launch.RuntimeArgs...)
		arguments = append(arguments, entrypoint)
		return interpreter, append(arguments, requestCopy...), nil
	default:
		return "", nil, closureError("launch has unknown closure class")
	}
}

// PortableRuntimeFileDigest returns the ordinary lowercase SHA-256 digest of
// one stable regular file. Symlinks, replacements, and concurrent mutation
// fail with a redacted closure error.
func PortableRuntimeFileDigest(source string) (string, error) {
	return portableRuntimeFileDigestWithHook(source, nil)
}

// PortableRuntimeTreeDigest returns SHA-256 over a versioned canonical sorted
// manifest of relative path, declared type, owner permission bits, and regular
// file content digest. Safe in-tree regular-file symlinks are normalized to
// independent file records; other symlinks and special files are rejected.
func PortableRuntimeTreeDigest(source string) (string, error) {
	root, rootInfo, err := inspectPortableRuntimeTreeRoot(source)
	if err != nil {
		return "", err
	}
	var records []portableRuntimeTreeRecord
	err = filepath.Walk(root, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return closureError("could not read tree entry")
		}
		if current == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return closureError("tree entry escapes source root")
		}
		recordName := filepath.ToSlash(relative)
		if !validPortableRuntimeTargetPath(recordName) {
			return closureError("tree entry has an invalid portable target")
		}
		record := portableRuntimeTreeRecord{name: recordName, pathInfo: info}
		switch {
		case info.Mode().IsRegular():
			record.kind = 'f'
			digest, digestInfo, digestErr := portableRuntimeFileDigestBytes(current, nil)
			if digestErr != nil {
				return digestErr
			}
			if !samePortableRuntimeFile(info, digestInfo) {
				return closureError("tree source changed during digest")
			}
			record.owner = uint32(digestInfo.Mode().Perm() & 0700)
			record.digest = digest
			record.contentInfo = digestInfo
		case info.IsDir():
			record.kind = 'd'
			record.owner = uint32(info.Mode().Perm() & 0700)
		case info.Mode()&os.ModeSymlink != 0:
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return closureError("tree contains a dangling or cyclic symlink")
			}
			resolved = filepath.Clean(resolved)
			if resolved == root || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
				return closureError("tree symlink escapes source root")
			}
			resolvedInfo, statErr := os.Lstat(resolved)
			if statErr != nil || !resolvedInfo.Mode().IsRegular() {
				return closureError("tree contains a directory or special-file symlink")
			}
			digest, digestInfo, digestErr := portableRuntimeFileDigestBytes(resolved, nil)
			if digestErr != nil {
				return digestErr
			}
			record.kind = 'f'
			record.owner = uint32(digestInfo.Mode().Perm() & 0700)
			record.digest = digest
			record.contentInfo = digestInfo
		default:
			return closureError("tree contains a special file")
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].name < records[j].name })

	manifest := sha256.New()
	_, _ = io.WriteString(manifest, "fizeau-portable-tree-v1\x00")
	for _, record := range records {
		writePortableRuntimeManifestRecord(manifest, record.kind, record.name, record.owner, record.digest)
	}
	if err := verifyPortableRuntimeTreeSnapshot(root, rootInfo, records); err != nil {
		return "", err
	}
	return hex.EncodeToString(manifest.Sum(nil)), nil
}

type portableRuntimeInspectedRoot struct {
	source string
	target string
	digest string
	exact  bool
}

type portableRuntimeResolvedLibrary struct {
	source string
	target string
	info   os.FileInfo
	digest string
}

type portableRuntimeTreeRecord struct {
	name        string
	kind        byte
	owner       uint32
	digest      [sha256.Size]byte
	pathInfo    os.FileInfo
	contentInfo os.FileInfo
}

func inspectPortableRuntimeRoots(mappings []PortableRuntimeSourceTree) ([]portableRuntimeInspectedRoot, error) {
	roots := make([]portableRuntimeInspectedRoot, len(mappings))
	for i, mapping := range mappings {
		if !validPortableRuntimeTargetPath(mapping.Target) {
			return nil, closureErrorAt("source tree", i, "has invalid target")
		}
		source, _, err := inspectPortableRuntimeTreeRoot(mapping.Source)
		if err != nil {
			return nil, closureErrorAt("source tree", i, "is not a stable regular directory")
		}
		digest, digestErr := PortableRuntimeTreeDigest(source)
		if digestErr != nil {
			return nil, closureErrorAt("source tree", i, "cannot be canonically digested")
		}
		roots[i] = portableRuntimeInspectedRoot{source: source, target: mapping.Target, digest: digest}
	}
	return roots, nil
}

func inspectPortableRuntimeLibraryRoots(treeMappings []PortableRuntimeSourceTree, exactMappings []PortableRuntimeLibrarySearchRoot) ([]portableRuntimeInspectedRoot, bool, error) {
	if len(treeMappings) == 0 && len(exactMappings) == 0 {
		return nil, false, closureError("dynamic layout lacks explicit library roots")
	}
	if len(treeMappings) != 0 && len(exactMappings) != 0 {
		return nil, false, closureError("dynamic layout mixes tree and exact library roots")
	}
	if len(treeMappings) != 0 {
		roots, err := inspectPortableRuntimeRoots(treeMappings)
		return roots, false, err
	}

	roots := make([]portableRuntimeInspectedRoot, len(exactMappings))
	seenTargets := make(map[string]struct{}, len(exactMappings))
	for i, mapping := range exactMappings {
		if !validPortableRuntimeTargetPath(mapping.Target) {
			return nil, false, closureErrorAt("exact library root", i, "has invalid target")
		}
		if _, exists := seenTargets[mapping.Target]; exists {
			return nil, false, closureErrorAt("exact library root", i, "duplicates an earlier target")
		}
		for previous := range seenTargets {
			if strings.HasPrefix(mapping.Target, previous+"/") || strings.HasPrefix(previous, mapping.Target+"/") {
				return nil, false, closureErrorAt("exact library root", i, "overlaps an earlier target")
			}
		}
		seenTargets[mapping.Target] = struct{}{}
		source, _, err := inspectPortableRuntimeTreeRoot(mapping.Source)
		if err != nil {
			return nil, false, closureErrorAt("exact library root", i, "is not a stable regular directory")
		}
		roots[i] = portableRuntimeInspectedRoot{source: source, target: mapping.Target, exact: true}
	}
	return roots, true, nil
}

func portableRuntimeUsedExactRoots(roots []portableRuntimeInspectedRoot, libraries []portableRuntimeResolvedLibrary) []portableRuntimeInspectedRoot {
	used := make([]portableRuntimeInspectedRoot, 0, len(roots))
	for _, root := range roots {
		for _, library := range libraries {
			if strings.HasPrefix(library.target, root.target+"/") {
				used = append(used, root)
				break
			}
		}
	}
	return used
}

func inspectPortableRuntimeTreeRoot(source string) (string, os.FileInfo, error) {
	if !validPortableRuntimeSource(source) {
		return "", nil, closureError("tree has invalid source path")
	}
	lstat, err := os.Lstat(source)
	if err != nil || !lstat.IsDir() || lstat.Mode()&os.ModeSymlink != 0 {
		return "", nil, closureError("tree source is not a regular directory")
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", nil, closureError("tree source cannot be resolved")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, closureError("tree source is not a regular directory")
	}
	return filepath.Clean(resolved), info, nil
}

func inspectPortableRuntimeELF(ctx context.Context, source string, target PortableRuntimeTarget, role portableRuntimeELFRole) (string, os.FileInfo, *portableRuntimeELFFile, error) {
	return inspectPortableRuntimeELFWithHook(ctx, source, target, role, nil)
}

func inspectPortableRuntimeELFWithHook(ctx context.Context, source string, target PortableRuntimeTarget, role portableRuntimeELFRole, afterPathInspection func()) (string, os.FileInfo, *portableRuntimeELFFile, error) {
	if err := checkPortableRuntimeContext(ctx); err != nil {
		return "", nil, nil, err
	}
	resolved, info, err := resolvePortableRuntimeRegularFile(source)
	if err != nil {
		return "", nil, nil, err
	}
	if afterPathInspection != nil {
		afterPathInspection()
	}
	descriptor, err := safefs.OpenRead(resolved)
	if err != nil {
		return "", nil, nil, closureError("installed layout is not a recognized Linux ELF file")
	}
	descriptorInfo, err := descriptor.Stat()
	if err != nil || !samePortableRuntimeFile(info, descriptorInfo) {
		_ = descriptor.Close()
		return "", nil, nil, closureError("installed ELF changed before it could be inspected")
	}
	parsed, err := elf.NewFile(descriptor)
	if err != nil {
		_ = descriptor.Close()
		return "", nil, nil, closureError("installed layout is not a recognized Linux ELF file")
	}
	file := &portableRuntimeELFFile{File: parsed, descriptor: descriptor}
	if file.OSABI != elf.ELFOSABI_NONE && file.OSABI != elf.ELFOSABI_LINUX {
		closePortableRuntimeELF(file)
		return "", nil, nil, closureError("ELF operating-system ABI is not Linux-compatible")
	}
	machine := portableRuntimeELFMachine(target.GOARCH)
	if machine == elf.EM_NONE || file.Class != portableRuntimeELFClass(target.GOARCH) || file.Machine != machine {
		closePortableRuntimeELF(file)
		return "", nil, nil, closureError("ELF architecture does not match the portable target")
	}
	data := portableRuntimeELFData(target.GOARCH)
	if data == elf.ELFDATANONE || file.Data != data {
		closePortableRuntimeELF(file)
		return "", nil, nil, closureError("ELF byte order does not match the portable target")
	}
	validType := false
	switch role {
	case portableRuntimeELFExecutable:
		// Linux PIE executables use ET_DYN even when they have no PT_INTERP.
		validType = (file.Type == elf.ET_EXEC || file.Type == elf.ET_DYN) && portableRuntimeELFHasExecutableEntry(file.File)
	case portableRuntimeELFLoader:
		validType = file.Type == elf.ET_DYN && portableRuntimeELFHasExecutableEntry(file.File)
	case portableRuntimeELFDependency:
		validType = file.Type == elf.ET_DYN
	}
	if !validType {
		closePortableRuntimeELF(file)
		return "", nil, nil, closureError("ELF file has an unsupported executable type")
	}
	return resolved, descriptorInfo, file, nil
}

func portableRuntimeELFHasExecutableEntry(file *elf.File) bool {
	if file.Entry == 0 {
		return false
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 && file.Entry >= program.Vaddr && file.Entry-program.Vaddr < program.Memsz {
			return true
		}
	}
	return false
}

func closePortableRuntimeELF(file *portableRuntimeELFFile) {
	if file != nil {
		_ = file.Close()
	}
}

func inspectPortableRuntimeScript(ctx context.Context, source string) (string, os.FileInfo, error) {
	if err := checkPortableRuntimeContext(ctx); err != nil {
		return "", nil, err
	}
	resolved, info, err := resolvePortableRuntimeRegularFile(source)
	if err != nil {
		return "", nil, err
	}
	file, err := safefs.OpenRead(resolved)
	if err != nil {
		return "", nil, closureError("could not inspect interpreted launcher")
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, 4097))
	line, readErr := reader.ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", nil, closureError("could not inspect interpreted launcher")
	}
	if len(line) > 4096 || !strings.HasPrefix(line, "#!") || strings.ContainsRune(line, '\x00') || len(strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "#!")))) == 0 {
		return "", nil, closureError("installed layout is not a recognized interpreted launcher")
	}
	return resolved, info, nil
}

func resolvePortableRuntimeRegularFile(source string) (string, os.FileInfo, error) {
	if !validPortableRuntimeSource(source) {
		return "", nil, closureError("file has invalid source path")
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", nil, closureError("installed file cannot be resolved")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, closureError("installed file is not a regular file")
	}
	return filepath.Clean(resolved), info, nil
}

func portableRuntimeELFInterpreter(file *elf.File) (string, error) {
	var interpreter string
	for _, program := range file.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		if interpreter != "" {
			return "", closureError("ELF file has multiple interpreters")
		}
		contents, err := io.ReadAll(io.LimitReader(program.Open(), 4097))
		if err != nil {
			return "", closureError("ELF interpreter record is invalid")
		}
		interpreter, err = parsePortableRuntimeELFInterpreter(contents)
		if err != nil {
			return "", err
		}
	}
	return interpreter, nil
}

func parsePortableRuntimeELFInterpreter(contents []byte) (string, error) {
	if len(contents) < 2 || len(contents) > 4096 || contents[len(contents)-1] != 0 {
		return "", closureError("ELF interpreter record is invalid")
	}
	interpreter := string(contents[:len(contents)-1])
	if !filepath.IsAbs(interpreter) || strings.ContainsRune(interpreter, '\x00') || filepath.Clean(interpreter) != interpreter {
		return "", closureError("ELF interpreter record is invalid")
	}
	return interpreter, nil
}

func portableRuntimeELFHasInterpreter(file *elf.File) bool {
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			return true
		}
	}
	return false
}

func resolvePortableRuntimeELFClosure(ctx context.Context, entrypoint *portableRuntimeELFFile, target PortableRuntimeTarget, roots []portableRuntimeInspectedRoot, lookup PortableRuntimeLookupPolicy) ([]portableRuntimeResolvedLibrary, error) {
	queue := []*portableRuntimeELFFile{entrypoint}
	opened := make([]*portableRuntimeELFFile, 0)
	defer func() {
		for _, file := range opened {
			_ = file.Close()
		}
	}()
	seen := make(map[string]portableRuntimeResolvedLibrary)
	resolvedByTarget := make(map[string]portableRuntimeResolvedLibrary)
	for len(queue) != 0 {
		if err := checkPortableRuntimeContext(ctx); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		if err := validatePortableRuntimeELFDynamicLookup(current.File, lookup); err != nil {
			return nil, err
		}
		libraries, err := current.ImportedLibraries()
		if err != nil {
			return nil, closureError("ELF dependency table cannot be verified")
		}
		sort.Strings(libraries)
		for _, library := range libraries {
			if library == "" || library == "." || library == ".." || !utf8.ValidString(library) || filepath.Base(library) != library || strings.ContainsAny(library, "\\\x00") {
				return nil, closureError("ELF dependency name is invalid")
			}
			selected, rootIndex, err := resolvePortableRuntimeLibrary(library, roots)
			if err != nil {
				return nil, err
			}
			libraryTarget := path.Join(roots[rootIndex].target, library)
			if previous, exists := resolvedByTarget[libraryTarget]; exists && previous.source != selected {
				return nil, closureError("ELF dependency target is ambiguous")
			}
			if inspected, exists := seen[selected]; exists {
				if _, recorded := resolvedByTarget[libraryTarget]; !recorded {
					inspected.target = libraryTarget
					resolvedByTarget[libraryTarget] = inspected
				}
				continue
			}
			beforeInfo, statErr := os.Lstat(selected)
			if statErr != nil || !beforeInfo.Mode().IsRegular() || beforeInfo.Mode()&os.ModeSymlink != 0 {
				return nil, closureError("ELF dependency changed before inspection")
			}
			beforeDigest, digestErr := portableRuntimeDigestInspectedFile(selected, beforeInfo)
			if digestErr != nil {
				return nil, digestErr
			}
			resolved, info, dependency, err := inspectPortableRuntimeELF(ctx, selected, target, portableRuntimeELFDependency)
			if err != nil {
				return nil, err
			}
			if resolved != selected || resolved == roots[rootIndex].source || !strings.HasPrefix(resolved, roots[rootIndex].source+string(filepath.Separator)) {
				closePortableRuntimeELF(dependency)
				return nil, closureError("ELF dependency changed or escaped its declared library root")
			}
			if err := validatePortableRuntimeELFDependencyIdentity(dependency.File, library); err != nil {
				closePortableRuntimeELF(dependency)
				return nil, err
			}
			afterDigest, digestErr := portableRuntimeDigestInspectedFile(resolved, info)
			if digestErr != nil || afterDigest != beforeDigest {
				closePortableRuntimeELF(dependency)
				return nil, closureError("ELF dependency changed during inspection")
			}
			inspected := portableRuntimeResolvedLibrary{source: resolved, target: libraryTarget, info: info, digest: afterDigest}
			seen[selected] = inspected
			resolvedByTarget[libraryTarget] = inspected
			opened = append(opened, dependency)
			queue = append(queue, dependency)
		}
	}
	resolvedLibraries := make([]portableRuntimeResolvedLibrary, 0, len(resolvedByTarget))
	for _, library := range resolvedByTarget {
		resolvedLibraries = append(resolvedLibraries, library)
	}
	sort.Slice(resolvedLibraries, func(i, j int) bool { return resolvedLibraries[i].target < resolvedLibraries[j].target })
	return resolvedLibraries, nil
}

func validatePortableRuntimeELFDependencyIdentity(file *elf.File, requestedName string) error {
	sonames, err := file.DynString(elf.DT_SONAME)
	if err != nil {
		return closureError("ELF dependency SONAME cannot be verified")
	}
	if len(sonames) > 1 || len(sonames) == 1 && sonames[0] != requestedName {
		return closureError("ELF dependency SONAME does not match its requested name")
	}
	// Some executable shared objects, notably glibc's libc, legitimately carry
	// PT_INTERP. They are valid dependencies only when an exact matching SONAME
	// proves that the file is intended to be loaded as this library. A renamed
	// PIE normally has no SONAME and is rejected here.
	if portableRuntimeELFHasInterpreter(file) && len(sonames) != 1 {
		return closureError("ELF dependency is an executable without a matching SONAME")
	}
	flags, err := file.DynValue(elf.DT_FLAGS_1)
	if err != nil {
		return closureError("ELF dependency flags cannot be verified")
	}
	for _, value := range flags {
		if value&uint64(elf.DF_1_PIE) != 0 {
			return closureError("ELF dependency is marked as a position-independent executable")
		}
	}
	return nil
}

func validatePortableRuntimeELFDynamicLookup(file *elf.File, lookup PortableRuntimeLookupPolicy) error {
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		values, err := file.DynString(tag)
		if err != nil {
			return closureError("ELF runtime search path cannot be verified")
		}
		if len(values) != 0 {
			return closureError("ELF runtime search path is not represented by the portable recipe")
		}
	}
	for _, tag := range []elf.DynTag{elf.DT_AUDIT, elf.DT_DEPAUDIT, elf.DT_FILTER, elf.DT_AUXILIARY} {
		values, err := file.DynValue(tag)
		if err != nil {
			return closureError("ELF runtime dependency metadata cannot be verified")
		}
		if len(values) != 0 {
			return closureError("ELF runtime dependency metadata is not represented by the portable recipe")
		}
	}
	if lookup == PortableRuntimeLookupClosed {
		symbols, err := file.ImportedSymbols()
		if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
			return closureError("ELF imported symbols cannot be checked for runtime lookup")
		}
		if portableRuntimeHasPluginLookupSymbol(symbols) {
			return closureError("ELF imports runtime lookup symbols but layout claims closed lookup")
		}
	}
	return nil
}

func portableRuntimeHasPluginLookupSymbol(symbols []elf.ImportedSymbol) bool {
	for _, symbol := range symbols {
		switch symbol.Name {
		case "dlopen", "dlmopen", "dlsym", "dlvsym":
			return true
		}
	}
	return false
}

func resolvePortableRuntimeLibrary(name string, roots []portableRuntimeInspectedRoot) (string, int, error) {
	var selected string
	selectedRoot := 0
	for i, root := range roots {
		candidate := filepath.Join(root.source, name)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		resolved = filepath.Clean(resolved)
		if resolved != root.source && !strings.HasPrefix(resolved, root.source+string(filepath.Separator)) {
			continue
		}
		info, err := os.Lstat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			if !root.exact {
				return resolved, i, nil
			}
			if selected != "" {
				return "", 0, closureError("ELF dependency is ambiguous across exact library roots")
			}
			selected = resolved
			selectedRoot = i
		}
	}
	if selected != "" {
		return selected, selectedRoot, nil
	}
	return "", 0, closureError("ELF dependency is unresolved in declared library roots")
}

func portableRuntimeELFMachine(goarch string) elf.Machine {
	switch goarch {
	case "386":
		return elf.EM_386
	case "amd64":
		return elf.EM_X86_64
	case "arm":
		return elf.EM_ARM
	case "arm64":
		return elf.EM_AARCH64
	case "ppc64", "ppc64le":
		return elf.EM_PPC64
	case "riscv64":
		return elf.EM_RISCV
	case "s390x":
		return elf.EM_S390
	default:
		return elf.EM_NONE
	}
}

func portableRuntimeELFClass(goarch string) elf.Class {
	switch goarch {
	case "386", "arm":
		return elf.ELFCLASS32
	default:
		return elf.ELFCLASS64
	}
}

func portableRuntimeELFData(goarch string) elf.Data {
	switch goarch {
	case "386", "amd64", "arm", "arm64", "ppc64le", "riscv64":
		return elf.ELFDATA2LSB
	case "ppc64", "s390x":
		return elf.ELFDATA2MSB
	default:
		return elf.ELFDATANONE
	}
}

func portableRuntimeRecognizedLoader(interpreter, goarch string) bool {
	base := filepath.Base(interpreter)
	if strings.HasPrefix(base, "ld-musl-") && strings.HasSuffix(base, ".so.1") {
		return true
	}
	switch goarch {
	case "386":
		return base == "ld-linux.so.2"
	case "amd64":
		return base == "ld-linux-x86-64.so.2"
	case "arm":
		return base == "ld-linux.so.3" || base == "ld-linux-armhf.so.3"
	case "arm64":
		return base == "ld-linux-aarch64.so.1"
	case "ppc64", "ppc64le":
		return base == "ld64.so.1" || base == "ld64.so.2"
	case "riscv64":
		return base == "ld-linux-riscv64-lp64d.so.1"
	case "s390x":
		return base == "ld64.so.1"
	default:
		return false
	}
}

func validatePortableRuntimeLookup(policy PortableRuntimeLookupPolicy, runtimeTrees []PortableRuntimeSourceTree) error {
	switch policy {
	case PortableRuntimeLookupClosed:
		if len(runtimeTrees) != 0 {
			return closureError("closed runtime lookup declares runtime trees")
		}
		return nil
	case PortableRuntimeLookupIncludedTrees:
		if len(runtimeTrees) == 0 {
			return closureError("runtime lookup policy lacks an explicit coverage tree")
		}
		return nil
	case PortableRuntimeLookupVerifiedExact:
		if len(runtimeTrees) != 0 {
			return closureError("verified exact runtime lookup declares runtime trees")
		}
		return nil
	default:
		return closureError("runtime lookup or plugin behavior is unknown")
	}
}

func appendPortableRuntimeTrees(ctx context.Context, assets []PortableRuntimeAsset, mappings []PortableRuntimeSourceTree, kind PortableRuntimeAssetKind) ([]PortableRuntimeAsset, error) {
	roots, err := inspectPortableRuntimeRoots(mappings)
	if err != nil {
		return nil, err
	}
	return appendInspectedPortableRuntimeRoots(ctx, assets, roots, kind)
}

func mergePortableRuntimeResolvedLibraries(left, right []portableRuntimeResolvedLibrary) ([]portableRuntimeResolvedLibrary, error) {
	merged := make(map[string]portableRuntimeResolvedLibrary, len(left)+len(right))
	for _, library := range append(append([]portableRuntimeResolvedLibrary(nil), left...), right...) {
		if previous, exists := merged[library.target]; exists {
			if previous.source != library.source {
				return nil, closureError("ELF dependency target is ambiguous")
			}
			continue
		}
		merged[library.target] = library
	}
	result := make([]portableRuntimeResolvedLibrary, 0, len(merged))
	for _, library := range merged {
		result = append(result, library)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].target < result[j].target })
	return result, nil
}

func appendPortableRuntimeResolvedLibraries(ctx context.Context, assets []PortableRuntimeAsset, libraries []portableRuntimeResolvedLibrary) ([]PortableRuntimeAsset, error) {
	for _, library := range libraries {
		if err := checkPortableRuntimeContext(ctx); err != nil {
			return nil, err
		}
		for _, asset := range assets {
			if asset.Target == library.target {
				return nil, closureError("ELF dependency target collides with another asset")
			}
		}
		digest, err := portableRuntimeDigestInspectedFile(library.source, library.info)
		if err != nil {
			return nil, err
		}
		if digest != library.digest {
			return nil, closureError("ELF dependency changed after inspection")
		}
		assets = append(assets, PortableRuntimeAsset{
			Kind:          PortableRuntimeAssetSupport,
			PathKind:      PortableRuntimePathFile,
			Source:        library.source,
			Target:        library.target,
			ContentSHA256: digest,
		})
	}
	return assets, nil
}

func appendInspectedPortableRuntimeRoots(ctx context.Context, assets []PortableRuntimeAsset, roots []portableRuntimeInspectedRoot, kind PortableRuntimeAssetKind) ([]PortableRuntimeAsset, error) {
	for _, root := range roots {
		if err := checkPortableRuntimeContext(ctx); err != nil {
			return nil, err
		}
		digest, err := PortableRuntimeTreeDigest(root.source)
		if err != nil {
			return nil, err
		}
		if digest != root.digest {
			return nil, closureError("source tree changed during closure analysis")
		}
		if portableRuntimeTreeAssetAlreadyDeclared(assets, root) {
			continue
		}
		assets = append(assets, PortableRuntimeAsset{Kind: kind, PathKind: PortableRuntimePathTree, Source: root.source, Target: root.target, ContentSHA256: digest})
	}
	return assets, nil
}

func portableRuntimeTreeAssetAlreadyDeclared(assets []PortableRuntimeAsset, root portableRuntimeInspectedRoot) bool {
	for _, asset := range assets {
		if asset.PathKind == PortableRuntimePathTree && asset.Source == root.source && asset.Target == root.target && asset.ContentSHA256 == root.digest {
			return true
		}
	}
	return false
}

func expandPortableRuntimeLoaderRecipe(guestTarget func(string) (string, error), launch PortableRuntimeLaunch) (string, []string, error) {
	loader, err := guestTarget(launch.LoaderTarget)
	if err != nil {
		return "", nil, err
	}
	if len(launch.LibraryRootTargets) == 0 {
		return "", nil, closureError("loader recipe has no library roots")
	}
	roots := make([]string, len(launch.LibraryRootTargets))
	for i, target := range launch.LibraryRootTargets {
		roots[i], err = guestTarget(target)
		if err != nil {
			return "", nil, err
		}
	}
	return loader, roots, nil
}

func portableRuntimeFileDigestWithHook(source string, afterOpen func()) (string, error) {
	digest, _, err := portableRuntimeFileDigestBytes(source, afterOpen)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

func verifyPortableRuntimeInterpreterIdentity(source string, inspected os.FileInfo, file *portableRuntimeELFFile, expected PortableRuntimeFileIdentity) (string, error) {
	if file == nil || file.descriptor == nil {
		return "", closureError("interpreter identity cannot be inspected")
	}
	descriptorInfo, descriptorErr := file.descriptor.Stat()
	pathInfo, pathErr := os.Lstat(source)
	if descriptorErr != nil || pathErr != nil ||
		!samePortableRuntimeFile(inspected, descriptorInfo) ||
		!samePortableRuntimeFile(descriptorInfo, pathInfo) {
		return "", closureError("interpreter changed during identity verification")
	}

	hasher := sha256.New()
	read, err := io.Copy(hasher, io.NewSectionReader(file.descriptor, 0, descriptorInfo.Size()))
	afterDescriptor, afterDescriptorErr := file.descriptor.Stat()
	afterPath, afterPathErr := os.Lstat(source)
	if err != nil || read != descriptorInfo.Size() || afterDescriptorErr != nil || afterPathErr != nil ||
		!samePortableRuntimeFile(descriptorInfo, afterDescriptor) ||
		!samePortableRuntimeFile(afterDescriptor, afterPath) {
		return "", closureError("interpreter changed during identity verification")
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	if afterDescriptor.Size() != expected.Size || digest != expected.ContentSHA256 {
		return "", closureError("interpreter does not match its declared identity")
	}
	return digest, nil
}

func portableRuntimeDigestInspectedFile(source string, inspected os.FileInfo) (string, error) {
	digest, current, err := portableRuntimeFileDigestBytes(source, nil)
	if err != nil {
		return "", err
	}
	if !samePortableRuntimeFile(inspected, current) {
		return "", closureError("source file changed during closure analysis")
	}
	return hex.EncodeToString(digest[:]), nil
}

func portableRuntimeFileDigestBytes(source string, afterOpen func()) ([sha256.Size]byte, os.FileInfo, error) {
	var zero [sha256.Size]byte
	if !validPortableRuntimeSource(source) {
		return zero, nil, closureError("file has invalid source path")
	}
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return zero, nil, closureError("digest source is not a regular file")
	}
	file, err := safefs.OpenRead(source)
	if err != nil {
		return zero, nil, closureError("could not read digest source")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !samePortableRuntimeFile(before, opened) {
		return zero, nil, closureError("digest source changed during open")
	}
	if afterOpen != nil {
		afterOpen()
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return zero, nil, closureError("could not read digest source")
	}
	afterRead, err := file.Stat()
	if err != nil || !samePortableRuntimeFile(opened, afterRead) {
		return zero, nil, closureError("digest source changed during read")
	}
	afterPath, err := os.Lstat(source)
	if err != nil || !samePortableRuntimeFile(afterRead, afterPath) {
		return zero, nil, closureError("digest source changed during read")
	}
	copy(zero[:], hasher.Sum(nil))
	return zero, afterRead, nil
}

func samePortableRuntimeFile(first, second os.FileInfo) bool {
	return first != nil && second != nil && os.SameFile(first, second) && first.Mode() == second.Mode() && first.Size() == second.Size() && first.ModTime().Equal(second.ModTime())
}

func writePortableRuntimeManifestRecord(writer hash.Hash, kind byte, name string, owner uint32, digest [sha256.Size]byte) {
	_, _ = writer.Write([]byte{kind})
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], uint32(len(name))) // #nosec G115 -- OS path lengths are bounded far below uint32.
	_, _ = writer.Write(encoded[:])
	_, _ = io.WriteString(writer, name)
	binary.BigEndian.PutUint32(encoded[:], owner)
	_, _ = writer.Write(encoded[:])
	if kind == 'f' {
		_, _ = writer.Write(digest[:])
	}
}

func verifyPortableRuntimeTreeSnapshot(root string, rootInfo os.FileInfo, records []portableRuntimeTreeRecord) error {
	currentRoot, err := os.Lstat(root)
	if err != nil || !samePortableRuntimeFile(rootInfo, currentRoot) {
		return closureError("tree source changed during digest")
	}
	seen := make(map[string]os.FileInfo, len(records))
	err = filepath.Walk(root, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return closureError("could not verify tree source")
		}
		if current == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return closureError("could not verify tree source")
		}
		seen[filepath.ToSlash(relative)] = info
		return nil
	})
	if err != nil || len(seen) != len(records) {
		return closureError("tree source changed during digest")
	}
	for _, record := range records {
		current := seen[record.name]
		if !samePortableRuntimeFile(record.pathInfo, current) {
			return closureError("tree source changed during digest")
		}
		if record.kind == 'f' {
			contentPath := filepath.Join(root, filepath.FromSlash(record.name))
			if current.Mode()&os.ModeSymlink != 0 {
				contentPath, err = filepath.EvalSymlinks(contentPath)
				if err != nil || (contentPath != root && !strings.HasPrefix(filepath.Clean(contentPath), root+string(filepath.Separator))) {
					return closureError("tree source changed during digest")
				}
			}
			contentInfo, statErr := os.Lstat(contentPath)
			if statErr != nil || !samePortableRuntimeFile(record.contentInfo, contentInfo) {
				return closureError("tree source changed during digest")
			}
		}
	}
	return nil
}

func checkPortableRuntimeContext(ctx context.Context) error {
	if ctx == nil {
		return closureError("closure analysis has a nil context")
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: closure analysis canceled: %w", ErrPortableRuntimeClosureIncomplete, ctx.Err())
	default:
		return nil
	}
}
