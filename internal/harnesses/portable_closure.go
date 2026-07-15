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
	"path/filepath"
	"sort"
	"strings"

	"github.com/easel/fizeau/internal/safefs"
)

// PortableRuntimeSourceTree maps one complete host tree to one private guest
// tree. Contributors must name roots explicitly; closure analysis never falls
// back to PATH, the host loader cache, or a package manager.
type PortableRuntimeSourceTree struct {
	Source string
	Target string
}

// PortableRuntimeLookupPolicy records the contributor's evidence about
// runtime-only lookup (for example dlopen or plugin discovery). The zero value
// is deliberately invalid so an unknown installed layout fails closed.
type PortableRuntimeLookupPolicy string

type portableRuntimeELFRole uint8

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
// linked Linux executable layout. LibraryRoots are searched in order, which is
// also the order retained in the loader's --library-path recipe.
type PortableRuntimeDynamicClosureRequest struct {
	EntrypointSource string
	EntrypointTarget string
	LoaderTarget     string
	LibraryRoots     []PortableRuntimeSourceTree
	RuntimeLookup    PortableRuntimeLookupPolicy
	RuntimeTrees     []PortableRuntimeSourceTree
}

// PortableRuntimeInterpretedClosureRequest describes a recognized launcher,
// interpreter, and package-tree layout. RuntimeArgs are fixed interpreter
// arguments; request arguments are appended only when the recipe is activated.
type PortableRuntimeInterpretedClosureRequest struct {
	EntrypointSource  string
	EntrypointTarget  string
	InterpreterSource string
	InterpreterTarget string
	LoaderTarget      string
	LibraryRoots      []PortableRuntimeSourceTree
	PackageTrees      []PortableRuntimeSourceTree
	RuntimeArgs       []string
	RuntimeLookup     PortableRuntimeLookupPolicy
	RuntimeTrees      []PortableRuntimeSourceTree
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

	entrypoint, info, executable, err := inspectPortableRuntimeELF(ctx, request.EntrypointSource, target, portableRuntimeELFExecutable)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	defer executable.Close()
	if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		return PortableRuntimeContribution{}, closureError("static entrypoint is not an owner-executable regular file")
	}
	interpreter, err := portableRuntimeELFInterpreter(executable)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if interpreter != "" {
		return PortableRuntimeContribution{}, closureError("static entrypoint has an ELF interpreter")
	}
	if err := validatePortableRuntimeELFDynamicLookup(executable, request.RuntimeLookup); err != nil {
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
	if len(request.LibraryRoots) == 0 || request.LoaderTarget == "" {
		return PortableRuntimeContribution{}, closureError("dynamic layout lacks explicit loader or library roots")
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
	interpreter, err := portableRuntimeELFInterpreter(executable)
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

	roots, err := inspectPortableRuntimeRoots(request.LibraryRoots)
	if err != nil {
		closePortableRuntimeELF(executable)
		return PortableRuntimeContribution{}, err
	}
	if err := resolvePortableRuntimeELFClosure(ctx, executable, target, roots, request.RuntimeLookup); err != nil {
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
	if err := resolvePortableRuntimeELFClosure(ctx, loaderELF, target, roots, request.RuntimeLookup); err != nil {
		closePortableRuntimeELF(loaderELF)
		return PortableRuntimeContribution{}, err
	}
	if err := loaderELF.Close(); err != nil {
		return PortableRuntimeContribution{}, closureError("could not finish inspecting ELF loader")
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
	assets, err = appendInspectedPortableRuntimeRoots(ctx, assets, roots, PortableRuntimeAssetInstallTree)
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
	if err := ValidatePortableRuntimeTarget(target); err != nil {
		return PortableRuntimeContribution{}, err
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

	interpreter, interpreterInfo, interpreterELF, err := inspectPortableRuntimeELF(ctx, request.InterpreterSource, target, portableRuntimeELFExecutable)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	if interpreterInfo.Mode().Perm()&0100 == 0 {
		closePortableRuntimeELF(interpreterELF)
		return PortableRuntimeContribution{}, closureError("interpreter is not owner-executable")
	}
	elfInterpreter, err := portableRuntimeELFInterpreter(interpreterELF)
	if err != nil {
		closePortableRuntimeELF(interpreterELF)
		return PortableRuntimeContribution{}, err
	}

	var roots []portableRuntimeInspectedRoot
	var loader string
	var loaderELF *elf.File
	var loaderInfo os.FileInfo
	if elfInterpreter == "" {
		if request.LoaderTarget != "" || len(request.LibraryRoots) != 0 {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("static interpreter layout declares dynamic loader state")
		}
		if libraries, libraryErr := interpreterELF.ImportedLibraries(); libraryErr != nil || len(libraries) != 0 {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("static interpreter has an unverifiable dynamic dependency table")
		}
		if err := validatePortableRuntimeELFDynamicLookup(interpreterELF, request.RuntimeLookup); err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
	} else {
		if request.LoaderTarget == "" || len(request.LibraryRoots) == 0 {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("dynamic interpreter layout lacks explicit loader or library roots")
		}
		if !portableRuntimeRecognizedLoader(elfInterpreter, target.GOARCH) {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("interpreter loader does not support the portable launch recipe")
		}
		roots, err = inspectPortableRuntimeRoots(request.LibraryRoots)
		if err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
		if err := resolvePortableRuntimeELFClosure(ctx, interpreterELF, target, roots, request.RuntimeLookup); err != nil {
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
		if err := resolvePortableRuntimeELFClosure(ctx, loaderELF, target, roots, request.RuntimeLookup); err != nil {
			closePortableRuntimeELF(loaderELF)
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, err
		}
		if err := loaderELF.Close(); err != nil {
			closePortableRuntimeELF(interpreterELF)
			return PortableRuntimeContribution{}, closureError("could not finish inspecting interpreter loader")
		}
	}
	if err := interpreterELF.Close(); err != nil {
		return PortableRuntimeContribution{}, closureError("could not finish inspecting interpreter")
	}

	entryDigest, err := portableRuntimeDigestInspectedFile(entrypoint, entryInfo)
	if err != nil {
		return PortableRuntimeContribution{}, err
	}
	interpreterDigest, err := portableRuntimeDigestInspectedFile(interpreter, interpreterInfo)
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
		assets, err = appendInspectedPortableRuntimeRoots(ctx, assets, roots, PortableRuntimeAssetInstallTree)
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

func inspectPortableRuntimeELF(ctx context.Context, source string, target PortableRuntimeTarget, role portableRuntimeELFRole) (string, os.FileInfo, *elf.File, error) {
	if err := checkPortableRuntimeContext(ctx); err != nil {
		return "", nil, nil, err
	}
	resolved, info, err := resolvePortableRuntimeRegularFile(source)
	if err != nil {
		return "", nil, nil, err
	}
	file, err := safefs.OpenELF(resolved)
	if err != nil {
		return "", nil, nil, closureError("installed layout is not a recognized Linux ELF file")
	}
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
		validType = (file.Type == elf.ET_EXEC || file.Type == elf.ET_DYN) && portableRuntimeELFHasExecutableEntry(file)
	case portableRuntimeELFLoader:
		validType = file.Type == elf.ET_DYN && portableRuntimeELFHasExecutableEntry(file)
	case portableRuntimeELFDependency:
		validType = file.Type == elf.ET_DYN
	}
	if !validType {
		closePortableRuntimeELF(file)
		return "", nil, nil, closureError("ELF file has an unsupported executable type")
	}
	return resolved, info, file, nil
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

func closePortableRuntimeELF(file *elf.File) {
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

func resolvePortableRuntimeELFClosure(ctx context.Context, entrypoint *elf.File, target PortableRuntimeTarget, roots []portableRuntimeInspectedRoot, lookup PortableRuntimeLookupPolicy) error {
	queue := []*elf.File{entrypoint}
	opened := make([]*elf.File, 0)
	defer func() {
		for _, file := range opened {
			_ = file.Close()
		}
	}()
	seen := make(map[string]struct{})
	for len(queue) != 0 {
		if err := checkPortableRuntimeContext(ctx); err != nil {
			return err
		}
		current := queue[0]
		queue = queue[1:]
		if err := validatePortableRuntimeELFDynamicLookup(current, lookup); err != nil {
			return err
		}
		libraries, err := current.ImportedLibraries()
		if err != nil {
			return closureError("ELF dependency table cannot be verified")
		}
		sort.Strings(libraries)
		for _, library := range libraries {
			if library == "" || filepath.Base(library) != library || strings.ContainsRune(library, '\x00') {
				return closureError("ELF dependency name is invalid")
			}
			resolved, err := resolvePortableRuntimeLibrary(library, roots)
			if err != nil {
				return err
			}
			if _, exists := seen[resolved]; exists {
				continue
			}
			seen[resolved] = struct{}{}
			_, _, dependency, err := inspectPortableRuntimeELF(ctx, resolved, target, portableRuntimeELFDependency)
			if err != nil {
				return err
			}
			opened = append(opened, dependency)
			queue = append(queue, dependency)
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

func resolvePortableRuntimeLibrary(name string, roots []portableRuntimeInspectedRoot) (string, error) {
	for _, root := range roots {
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
			return resolved, nil
		}
	}
	return "", closureError("ELF dependency is unresolved in declared library roots")
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
