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
// deterministic contribution. Asset, environment, and execution-constraint
// ordering is canonical; launch, fixed-argument, and fixed-option/value ordering
// is retained because it is part of execution semantics.
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
		ExecutionConstraints: PortableRuntimeExecutionConstraints{
			Environment:         append([]PortableRuntimeEnvironmentConstraint(nil), contribution.ExecutionConstraints.Environment...),
			ReadOnlyPaths:       append([]PortableRuntimeGuestPath(nil), contribution.ExecutionConstraints.ReadOnlyPaths...),
			RequiredAbsentPaths: append([]PortableRuntimeGuestPath(nil), contribution.ExecutionConstraints.RequiredAbsentPaths...),
			FixedArguments:      append([]string(nil), contribution.ExecutionConstraints.FixedArguments...),
			FixedOptionValues:   append([]PortableRuntimeFixedOptionValue(nil), contribution.ExecutionConstraints.FixedOptionValues...),
		},
	}

	if err := validatePortableRuntimeAssets(normalized.Assets); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if err := validatePortableRuntimeEnvironment(normalized.Environment); err != nil {
		return PortableRuntimeContribution{}, err
	}
	if err := validatePortableRuntimeExecutionConstraints(normalized); err != nil {
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
	sort.Slice(normalized.ExecutionConstraints.Environment, func(i, j int) bool {
		return normalized.ExecutionConstraints.Environment[i].Name < normalized.ExecutionConstraints.Environment[j].Name
	})
	sortPortableRuntimeGuestPaths(normalized.ExecutionConstraints.ReadOnlyPaths)
	sortPortableRuntimeGuestPaths(normalized.ExecutionConstraints.RequiredAbsentPaths)
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
		if portableRuntimeBaselineEnvironmentName(inherited.Name) {
			return closureErrorAt("environment", i, "conflicts with the generated baseline")
		}
		if previous, exists := seen[inherited.Name]; exists {
			return closureError("environment name is duplicated at indexes %d and %d", previous, i)
		}
		seen[inherited.Name] = i
	}
	return nil
}

func validatePortableRuntimeExecutionConstraints(contribution PortableRuntimeContribution) error {
	constraints := contribution.ExecutionConstraints
	inherited := make(map[string]struct{}, len(contribution.Environment))
	for _, environment := range contribution.Environment {
		inherited[environment.Name] = struct{}{}
	}
	seenEnvironment := make(map[string]int, len(constraints.Environment))
	generatedPaths := make([]PortableRuntimeGuestPath, 0, len(constraints.Environment))
	for i, environment := range constraints.Environment {
		if !validPortableRuntimeEnvironmentName(environment.Name) {
			return closureErrorAt("execution environment", i, "has invalid variable name")
		}
		if _, exists := inherited[environment.Name]; exists {
			return closureErrorAt("execution environment", i, "conflicts with inherited environment")
		}
		if previous, exists := seenEnvironment[environment.Name]; exists {
			return closureError("execution environment name is duplicated at indexes %d and %d", previous, i)
		}
		seenEnvironment[environment.Name] = i

		switch environment.Kind {
		case PortableRuntimeEnvironmentFixedTrue, PortableRuntimeEnvironmentFixedFalse, PortableRuntimeEnvironmentUnset:
			if environment.GuestPath != (PortableRuntimeGuestPath{}) {
				return closureErrorAt("execution environment", i, "has an irrelevant guest path")
			}
		case PortableRuntimeEnvironmentGuestPath:
			if !validPortableRuntimeGuestPath(environment.GuestPath, true) {
				return closureErrorAt("execution environment", i, "has an invalid guest path")
			}
			if environment.GuestPath.Scope == PortableRuntimeGuestPathRuntime &&
				!portableRuntimeRuntimeGuestPathBacked(environment.GuestPath, contribution.Assets, constraints.ReadOnlyPaths) {
				return closureErrorAt("execution environment", i, "has an unbacked runtime path")
			}
			generatedPaths = append(generatedPaths, environment.GuestPath)
		case PortableRuntimeEnvironmentRuntimePath:
			if environment.Name != "PATH" {
				return closureErrorAt("execution environment", i, "uses runtime_path for a non-PATH name")
			}
			if environment.GuestPath != (PortableRuntimeGuestPath{}) {
				return closureErrorAt("execution environment", i, "has an irrelevant guest path")
			}
		default:
			return closureErrorAt("execution environment", i, "has unknown treatment")
		}
		if !validPortableRuntimeBaselineOverride(environment) {
			return closureErrorAt("execution environment", i, "has an unsupported generated-baseline override")
		}
	}

	seenArguments := make(map[string]int, len(constraints.FixedArguments)+len(constraints.FixedOptionValues))
	for i, argument := range constraints.FixedArguments {
		if !validPortableRuntimeFixedArgument(argument) || !validPortableRuntimeArgument(argument) {
			return closureErrorAt("fixed argument", i, "is not a fixed non-secret argument")
		}
		key := portableRuntimeFixedOptionKey(argument)
		if previous, exists := seenArguments[key]; exists {
			return closureError("fixed argument is duplicated at indexes %d and %d", previous, i)
		}
		seenArguments[key] = i
	}

	seenOptions := make(map[string]int, len(constraints.FixedOptionValues))
	for i, pair := range constraints.FixedOptionValues {
		if !validPortableRuntimeFixedOption(pair.Option) || !validPortableRuntimeArgument(pair.Option) {
			return closureErrorAt("fixed option/value", i, "has an invalid option")
		}
		if !validPortableRuntimeFixedOptionLiteral(pair.Value) {
			return closureErrorAt("fixed option/value", i, "has an invalid non-secret value")
		}
		key := portableRuntimeFixedOptionKey(pair.Option)
		if previous, exists := seenArguments[key]; exists {
			return closureError("fixed option/value at index %d conflicts with fixed argument index %d", i, previous)
		}
		if previous, exists := seenOptions[key]; exists {
			return closureError("fixed option/value option is duplicated at indexes %d and %d", previous, i)
		}
		seenOptions[key] = i
	}

	for i, readOnly := range constraints.ReadOnlyPaths {
		if !validPortableRuntimeGuestPath(readOnly, false) || readOnly.Scope != PortableRuntimeGuestPathRuntime {
			return closureErrorAt("read-only path", i, "is not an immutable runtime path")
		}
		asset, ok := portableRuntimeExactAsset(contribution.Assets, readOnly.Target)
		if !ok || asset.Kind != PortableRuntimeAssetConfig || asset.PathKind != PortableRuntimePathTree {
			return closureErrorAt("read-only path", i, "is not backed by an exact config tree asset")
		}
		for j := 0; j < i; j++ {
			if portableRuntimeGuestPathsOverlap(readOnly, constraints.ReadOnlyPaths[j]) {
				return closureError("read-only paths overlap at indexes %d and %d", j, i)
			}
		}
	}

	for i, absent := range constraints.RequiredAbsentPaths {
		if !validPortableRuntimeGuestPath(absent, false) {
			return closureErrorAt("required-absent path", i, "has an invalid guest path")
		}
		for j := 0; j < i; j++ {
			if portableRuntimeGuestPathsOverlap(absent, constraints.RequiredAbsentPaths[j]) {
				return closureError("required-absent paths overlap at indexes %d and %d", j, i)
			}
		}
		for j, asset := range contribution.Assets {
			if portableRuntimeGuestPathsOverlap(absent, PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: asset.Target}) {
				return closureError("required-absent path at index %d overlaps asset index %d", i, j)
			}
		}
		for j, readOnly := range constraints.ReadOnlyPaths {
			if portableRuntimeGuestPathsOverlap(absent, readOnly) {
				return closureError("required-absent path at index %d overlaps read-only path index %d", i, j)
			}
		}
		for j, generated := range generatedPaths {
			if portableRuntimeGuestPathsOverlap(absent, generated) {
				return closureError("required-absent path at index %d overlaps generated path index %d", i, j)
			}
		}
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

func validPortableRuntimeGuestPath(guestPath PortableRuntimeGuestPath, allowRoot bool) bool {
	if !validPortableRuntimeGuestPathScope(guestPath.Scope) {
		return false
	}
	if guestPath.Target == "" {
		return allowRoot
	}
	return validPortableRuntimeTargetPath(guestPath.Target)
}

func validPortableRuntimeGuestPathScope(scope PortableRuntimeGuestPathScope) bool {
	switch scope {
	case PortableRuntimeGuestPathRuntime,
		PortableRuntimeGuestPathHome,
		PortableRuntimeGuestPathConfig,
		PortableRuntimeGuestPathData,
		PortableRuntimeGuestPathCache,
		PortableRuntimeGuestPathState,
		PortableRuntimeGuestPathTmp:
		return true
	default:
		return false
	}
}

func portableRuntimeBaselineEnvironmentName(name string) bool {
	switch name {
	case "HOME", "PATH", "USER", "LOGNAME", "SHELL",
		"TERM", "LANG", "LC_ALL",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR":
		return true
	default:
		return false
	}
}

func validPortableRuntimeBaselineOverride(environment PortableRuntimeEnvironmentConstraint) bool {
	switch environment.Name {
	case "PATH":
		return environment.Kind == PortableRuntimeEnvironmentRuntimePath
	case "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR":
		return environment.Kind == PortableRuntimeEnvironmentGuestPath || environment.Kind == PortableRuntimeEnvironmentUnset
	case "USER", "LOGNAME", "SHELL":
		return environment.Kind == PortableRuntimeEnvironmentUnset
	case "TERM", "LANG", "LC_ALL":
		return environment.Kind == PortableRuntimeEnvironmentUnset
	default:
		return environment.Kind != PortableRuntimeEnvironmentRuntimePath
	}
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
		"account", "api-key", "apikey", "authorization", "credential", "endpoint", "harness", "model", "password", "policy", "profile", "provider", "route", "secret", "server-instance", "surface", "token",
	} {
		if normalized == forbidden || strings.HasPrefix(normalized, forbidden+"=") {
			return false
		}
	}
	return true
}

func validPortableRuntimeFixedArgument(argument string) bool {
	if len(argument) < 3 || !strings.HasPrefix(argument, "--") {
		return false
	}
	name := argument[2:]
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	previousHyphen := false
	for i := range len(name) {
		c := name[i]
		if c == '-' {
			if i == 0 || i == len(name)-1 || previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
		previousHyphen = false
	}
	return true
}

func validPortableRuntimeFixedOption(option string) bool {
	if len(option) == 2 && option[0] == '-' {
		// v0.15 governs only Gemini's extension selector in short-option
		// form. Other short options are semantically opaque and could select
		// a route (for example -m), so they fail closed until reviewed.
		return option == "-e"
	}
	return validPortableRuntimeFixedArgument(option)
}

func validPortableRuntimeFixedOptionLiteral(value string) bool {
	if value == "" || !validPortableRuntimeArgument(value) || strings.ContainsAny(value, "=/\\") {
		return false
	}
	previousHyphen := false
	for i := range len(value) {
		c := value[i]
		if i == 0 {
			if c < 'a' || c > 'z' {
				return false
			}
			continue
		}
		if c == '-' {
			if i == len(value)-1 || previousHyphen {
				return false
			}
			previousHyphen = true
			continue
		}
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
		previousHyphen = false
	}
	normalized := strings.ReplaceAll(value, "-", "")
	for _, forbidden := range []string{
		"apikey", "authorization", "credential", "harness", "model", "password", "profile", "provider", "secret", "serverinstance", "token",
	} {
		if strings.Contains(normalized, forbidden) {
			return false
		}
	}
	return true
}

func portableRuntimeFixedOptionKey(option string) string {
	return strings.TrimLeft(option, "-")
}

func portableRuntimeFileAsset(assets []PortableRuntimeAsset, target string) (PortableRuntimeAsset, bool) {
	for _, asset := range assets {
		if asset.Target == target && asset.PathKind == PortableRuntimePathFile {
			return asset, true
		}
	}
	return PortableRuntimeAsset{}, false
}

func portableRuntimeExactAsset(assets []PortableRuntimeAsset, target string) (PortableRuntimeAsset, bool) {
	for _, asset := range assets {
		if asset.Target == target {
			return asset, true
		}
	}
	return PortableRuntimeAsset{}, false
}

func portableRuntimeGuestPathsOverlap(left, right PortableRuntimeGuestPath) bool {
	if left.Scope != right.Scope {
		return false
	}
	return left.Target == right.Target ||
		left.Target == "" || right.Target == "" ||
		strings.HasPrefix(left.Target, right.Target+"/") ||
		strings.HasPrefix(right.Target, left.Target+"/")
}

func portableRuntimeRuntimeGuestPathBacked(guestPath PortableRuntimeGuestPath, assets []PortableRuntimeAsset, readOnlyPaths []PortableRuntimeGuestPath) bool {
	for _, asset := range assets {
		if asset.Target == guestPath.Target ||
			(asset.PathKind == PortableRuntimePathTree && (guestPath.Target == "" || strings.HasPrefix(asset.Target, guestPath.Target+"/"))) {
			return true
		}
	}
	for _, readOnly := range readOnlyPaths {
		if readOnly.Scope == PortableRuntimeGuestPathRuntime &&
			(guestPath.Target == "" || readOnly.Target == guestPath.Target || strings.HasPrefix(readOnly.Target, guestPath.Target+"/")) {
			return true
		}
	}
	return false
}

func sortPortableRuntimeGuestPaths(paths []PortableRuntimeGuestPath) {
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Scope != paths[j].Scope {
			return paths[i].Scope < paths[j].Scope
		}
		return paths[i].Target < paths[j].Target
	})
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
