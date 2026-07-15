package harnesses

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ValidatePortableRuntimeTarget validates the Linux same-platform target
// required by portable runtime v0.15.
func ValidatePortableRuntimeTarget(target PortableRuntimeTarget) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%w: preparing platform is not linux", ErrPortableRuntimeTargetUnsupported)
	}
	if target.GOOS != "linux" {
		return fmt.Errorf("%w: GOOS must be linux and match the preparing process", ErrPortableRuntimeTargetUnsupported)
	}
	if target.GOARCH == "" || target.GOARCH != runtime.GOARCH {
		return fmt.Errorf("%w: GOARCH must match the preparing process", ErrPortableRuntimeTargetUnsupported)
	}
	return nil
}

// ValidatePortableRuntimeContribution validates a contribution without
// retaining its normalized form.
func ValidatePortableRuntimeContribution(target PortableRuntimeTarget, contribution PortableRuntimeContribution) error {
	_, err := NormalizePortableRuntimeContribution(target, contribution)
	return err
}

// NormalizePortableRuntimeContribution validates and returns an owned,
// deterministic contribution. Asset and environment ordering is canonical;
// launch argument and library-root ordering is retained because it is part of
// execution semantics.
func NormalizePortableRuntimeContribution(target PortableRuntimeTarget, contribution PortableRuntimeContribution) (PortableRuntimeContribution, error) {
	if err := ValidatePortableRuntimeTarget(target); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if !validPortableRuntimeClosureClass(contribution.ClosureClass) {
		return PortableRuntimeContribution{}, closureError("unknown closure class")
	}
	if len(contribution.Assets) == 0 {
		return PortableRuntimeContribution{}, closureError("asset set is empty")
	}

	normalized := PortableRuntimeContribution{
		ClosureClass: contribution.ClosureClass,
		Launch: PortableRuntimeLaunch{
			EntrypointTarget:   contribution.Launch.EntrypointTarget,
			InterpreterTarget:  contribution.Launch.InterpreterTarget,
			LoaderTarget:       contribution.Launch.LoaderTarget,
			RuntimeArgs:        append([]string(nil), contribution.Launch.RuntimeArgs...),
			LibraryRootTargets: append([]string(nil), contribution.Launch.LibraryRootTargets...),
		},
		Assets:      append([]PortableRuntimeAsset(nil), contribution.Assets...),
		Environment: append([]PortableRuntimeEnvironment(nil), contribution.Environment...),
	}

	if err := validatePortableRuntimeAssets(normalized.Assets); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if err := validatePortableRuntimeEnvironment(normalized.Environment); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if err := validatePortableRuntimeLaunch(normalized); err != nil {
		return PortableRuntimeContribution{}, err
	}

	sort.Slice(normalized.Assets, func(i, j int) bool {
		return normalized.Assets[i].Target < normalized.Assets[j].Target
	})
	sort.Slice(normalized.Environment, func(i, j int) bool {
		return normalized.Environment[i].Name < normalized.Environment[j].Name
	})
	return normalized, nil
}

func validatePortableRuntimeAssets(assets []PortableRuntimeAsset) error {
	seenTargets := make(map[string]int, len(assets))
	for i, asset := range assets {
		if !validPortableRuntimeAssetKind(asset.Kind) {
			return closureErrorAt("asset", i, "has unknown kind")
		}
		if asset.PathKind != PortableRuntimePathFile && asset.PathKind != PortableRuntimePathTree {
			return closureErrorAt("asset", i, "has unknown path kind")
		}
		if !validPortableRuntimeSource(asset.Source) {
			return closureErrorAt("asset", i, "has invalid source path")
		}
		if !validPortableRuntimeTargetPath(asset.Target) {
			return closureErrorAt("asset", i, "has invalid target path")
		}
		if !validPortableRuntimeDigest(asset.ContentSHA256) {
			return closureErrorAt("asset", i, "has invalid content digest")
		}
		if err := validatePortableRuntimeAssetShape(asset); err != nil {
			return closureErrorAt("asset", i, err.Error())
		}
		if previous, exists := seenTargets[asset.Target]; exists {
			return closureError("asset target is duplicated at indexes %d and %d", previous, i)
		}
		seenTargets[asset.Target] = i
	}

	// Any ancestor/descendant pair is impossible to materialize independently:
	// a tree already owns its descendants, while a file cannot also be a
	// directory. Reject both forms without making behavior copy-order dependent.
	for i := range assets {
		for j := i + 1; j < len(assets); j++ {
			if strings.HasPrefix(assets[i].Target, assets[j].Target+"/") || strings.HasPrefix(assets[j].Target, assets[i].Target+"/") {
				return closureError("asset targets overlap at indexes %d and %d", i, j)
			}
		}
	}
	return nil
}

func validatePortableRuntimeAssetShape(asset PortableRuntimeAsset) error {
	switch asset.Kind {
	case PortableRuntimeAssetExecutable:
		if asset.PathKind != PortableRuntimePathFile {
			return errors.New("executable kind must be a file")
		}
	case PortableRuntimeAssetInstallTree:
		if asset.PathKind != PortableRuntimePathTree {
			return errors.New("install-tree kind must be a tree")
		}
		if asset.Executable {
			return errors.New("tree asset cannot declare file executable state")
		}
	case PortableRuntimeAssetCredential:
		if asset.PathKind != PortableRuntimePathFile {
			return errors.New("credential kind must be a file")
		}
		if asset.Executable {
			return errors.New("credential kind cannot be executable")
		}
	case PortableRuntimeAssetConfig, PortableRuntimeAssetQuota, PortableRuntimeAssetCache:
		if asset.Executable {
			return errors.New("state kind cannot be executable")
		}
	case PortableRuntimeAssetSupport:
		if asset.PathKind == PortableRuntimePathTree && asset.Executable {
			return errors.New("tree asset cannot declare file executable state")
		}
	}
	return nil
}

func validatePortableRuntimeEnvironment(environment []PortableRuntimeEnvironment) error {
	seen := make(map[string]int, len(environment))
	for i, inherited := range environment {
		if !validPortableRuntimeEnvironmentName(inherited.Name) {
			return closureErrorAt("environment", i, "has invalid variable name")
		}
		if previous, exists := seen[inherited.Name]; exists {
			return closureError("environment name is duplicated at indexes %d and %d", previous, i)
		}
		seen[inherited.Name] = i
	}
	return nil
}

func validatePortableRuntimeLaunch(contribution PortableRuntimeContribution) error {
	launch := contribution.Launch
	if !validPortableRuntimeTargetPath(launch.EntrypointTarget) {
		return closureError("launch has invalid entrypoint target")
	}
	if launch.InterpreterTarget != "" && !validPortableRuntimeTargetPath(launch.InterpreterTarget) {
		return closureError("launch has invalid interpreter target")
	}
	if launch.LoaderTarget != "" && !validPortableRuntimeTargetPath(launch.LoaderTarget) {
		return closureError("launch has invalid loader target")
	}

	seenRoots := make(map[string]struct{}, len(launch.LibraryRootTargets))
	for i, root := range launch.LibraryRootTargets {
		if !validPortableRuntimeTargetPath(root) || strings.ContainsRune(root, ':') {
			return closureErrorAt("library root", i, "has invalid target path")
		}
		if _, exists := seenRoots[root]; exists {
			return closureErrorAt("library root", i, "duplicates an earlier target")
		}
		seenRoots[root] = struct{}{}
		if !portableRuntimeLibraryRootBacked(contribution.Assets, root) {
			return closureErrorAt("library root", i, "is not backed by declared library assets")
		}
	}
	for i, argument := range launch.RuntimeArgs {
		if !validPortableRuntimeArgument(argument) {
			return closureErrorAt("runtime argument", i, "is not a fixed non-secret argument")
		}
	}

	entrypoint, ok := portableRuntimeFileAsset(contribution.Assets, launch.EntrypointTarget)
	if !ok {
		return closureError("entrypoint target is not a declared file")
	}

	switch contribution.ClosureClass {
	case PortableRuntimeClosureStatic:
		if !entrypoint.Executable {
			return closureError("static entrypoint is not owner-executable")
		}
		if launch.InterpreterTarget != "" || launch.LoaderTarget != "" || len(launch.RuntimeArgs) != 0 || len(launch.LibraryRootTargets) != 0 {
			return closureError("static launch contains interpreter or loader state")
		}
	case PortableRuntimeClosureDynamic:
		if !entrypoint.Executable {
			return closureError("dynamic entrypoint is not owner-executable")
		}
		if launch.InterpreterTarget != "" || len(launch.RuntimeArgs) != 0 {
			return closureError("dynamic launch contains interpreter state")
		}
		if launch.LoaderTarget == "" || len(launch.LibraryRootTargets) == 0 {
			return closureError("dynamic launch lacks loader closure")
		}
		if !portableRuntimeExecutableFile(contribution.Assets, launch.LoaderTarget) {
			return closureError("loader target is not a declared owner-executable file")
		}
	case PortableRuntimeClosureInterpreted:
		if launch.InterpreterTarget == "" {
			return closureError("interpreted launch lacks interpreter target")
		}
		if !portableRuntimeExecutableFile(contribution.Assets, launch.InterpreterTarget) {
			return closureError("interpreter target is not a declared owner-executable file")
		}
		if launch.LoaderTarget == "" && len(launch.LibraryRootTargets) != 0 {
			return closureError("interpreted static launch has library roots without a loader")
		}
		if launch.LoaderTarget != "" {
			if len(launch.LibraryRootTargets) == 0 {
				return closureError("interpreted dynamic launch lacks library roots")
			}
			if !portableRuntimeExecutableFile(contribution.Assets, launch.LoaderTarget) {
				return closureError("loader target is not a declared owner-executable file")
			}
		}
	}
	return nil
}

func validPortableRuntimeAssetKind(kind PortableRuntimeAssetKind) bool {
	switch kind {
	case PortableRuntimeAssetExecutable,
		PortableRuntimeAssetInstallTree,
		PortableRuntimeAssetConfig,
		PortableRuntimeAssetCredential,
		PortableRuntimeAssetQuota,
		PortableRuntimeAssetCache,
		PortableRuntimeAssetSupport:
		return true
	default:
		return false
	}
}

func validPortableRuntimeClosureClass(class PortableRuntimeClosureClass) bool {
	switch class {
	case PortableRuntimeClosureStatic, PortableRuntimeClosureDynamic, PortableRuntimeClosureInterpreted:
		return true
	default:
		return false
	}
}

func validPortableRuntimeSource(source string) bool {
	return source != "" && !strings.ContainsRune(source, '\x00') && filepath.IsAbs(source) && filepath.Clean(source) == source
}

func validPortableRuntimeTargetPath(target string) bool {
	if target == "" || target == "." || strings.ContainsRune(target, '\x00') || strings.ContainsRune(target, '\\') || strings.HasPrefix(target, "/") {
		return false
	}
	cleaned := path.Clean(target)
	return cleaned == target && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func validPortableRuntimeDigest(digest string) bool {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func validPortableRuntimeEnvironmentName(name string) bool {
	if name == "" || strings.ContainsRune(name, '=') {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i == 0 {
			if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
				return false
			}
			continue
		}
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func validPortableRuntimeArgument(argument string) bool {
	if strings.ContainsRune(argument, '\x00') || strings.Contains(argument, "${") || strings.Contains(argument, "$(") || strings.Contains(argument, "{{") || strings.Contains(argument, "}}") {
		return false
	}
	if equals := strings.IndexByte(argument, '='); equals > 0 && validPortableRuntimeEnvironmentName(argument[:equals]) {
		return false
	}
	normalized := strings.ToLower(strings.TrimLeft(argument, "-"))
	for _, forbidden := range []string{
		"api-key", "apikey", "authorization", "credential", "harness", "model", "password", "profile", "provider", "secret", "server-instance", "token",
	} {
		if normalized == forbidden || strings.HasPrefix(normalized, forbidden+"=") {
			return false
		}
	}
	return true
}

func portableRuntimeFileAsset(assets []PortableRuntimeAsset, target string) (PortableRuntimeAsset, bool) {
	for _, asset := range assets {
		if asset.Target == target && asset.PathKind == PortableRuntimePathFile {
			return asset, true
		}
	}
	return PortableRuntimeAsset{}, false
}

func portableRuntimeExecutableFile(assets []PortableRuntimeAsset, target string) bool {
	asset, ok := portableRuntimeFileAsset(assets, target)
	return ok && asset.Executable
}

func portableRuntimeLibraryRootBacked(assets []PortableRuntimeAsset, target string) bool {
	exactFile := false
	for _, asset := range assets {
		if asset.PathKind == PortableRuntimePathTree && (asset.Kind == PortableRuntimeAssetInstallTree || asset.Kind == PortableRuntimeAssetSupport) &&
			(asset.Target == target || strings.HasPrefix(target, asset.Target+"/")) {
			return true
		}
		if strings.HasPrefix(asset.Target, target+"/") {
			if asset.PathKind != PortableRuntimePathFile || path.Dir(asset.Target) != target || asset.Kind != PortableRuntimeAssetSupport || asset.Executable {
				return false
			}
			exactFile = true
		}
	}
	return exactFile
}

func closureError(message string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPortableRuntimeClosureIncomplete, fmt.Sprintf(message, args...))
}

func closureErrorAt(record string, index int, message string) error {
	return closureError("%s %d %s", record, index, message)
}
