package portableruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/easel/fizeau/internal/harnesses"
)

const activationChild = "activation"

var fixedPortableToolPath = []string{
	"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
}

type activationEntrypoint struct {
	environment map[string]string
	recipe      ActivationRecipe
}

// activationIdentity is the process-free mapping authority captured for a
// portable activation. It deliberately has no diagnostic representation: the
// later namespace-launch bridge consumes it as opaque recipe state.
type activationIdentity struct {
	effectiveUID int
	primaryGID   int
}

type activationIdentityReader func() (activationIdentity, []int, error)

// ActivationIdentityReader supplies the process-free identity snapshot used
// to authorize portable namespace maps. It is exported only within Fizeau's
// internal package tree so service composition can inject deterministic test
// identities without weakening the production OS reader.
type ActivationIdentityReader func() (effectiveUID, primaryGID int, supplementaryGroups []int, err error)

// activationSubprocessLease serializes portable subprocesses that share one
// writable activation root. It is intentionally in-memory: the activation
// service owns both its lifetime and every recipe that can acquire it.
type activationSubprocessLease struct {
	available chan struct{}
	mu        sync.Mutex
	closed    bool
}

func newActivationSubprocessLease() *activationSubprocessLease {
	lease := &activationSubprocessLease{available: make(chan struct{}, 1)}
	lease.available <- struct{}{}
	return lease
}

func (l *activationSubprocessLease) acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return nil, activationError("subprocess lease")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, activationError("subprocess lease canceled")
	case <-l.available:
	}

	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		l.available <- struct{}{}
		return nil, activationError("subprocess lease closed")
	}

	var once sync.Once
	return func() {
		once.Do(func() { l.available <- struct{}{} })
	}, nil
}

func (l *activationSubprocessLease) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
}

// ActivationRecipe is opaque, service-owned namespace input. Its fields stay
// private so neither callers nor diagnostics can interpret runtime assets,
// writable paths, or enforcement details.
type ActivationRecipe struct {
	scopes            map[harnesses.PortableRuntimeGuestPathScope]string
	immutableBindings []activationImmutableBinding
	readOnlyPaths     []harnesses.PortableRuntimeGuestPath
	requiredAbsent    []harnesses.PortableRuntimeGuestPath
	identity          activationIdentity
	lease             *activationSubprocessLease
	launcherPath      string
}

// PortableRuntimeNamespaceRecipe marks this value as the opaque recipe that
// runner bindings may retain for the canonical spawn seam.
func (ActivationRecipe) PortableRuntimeNamespaceRecipe() {}

// PortableNamespaceLauncherPath is a narrow activation-to-lifecycle handoff.
// The lifecycle package descriptor-opens and seals this path itself, keeping
// activation process-free.
func (r ActivationRecipe) PortableNamespaceLauncherPath() string { return r.launcherPath }

// AcquirePortableNamespaceLease is the sole bridge from activation-owned
// identity and serialization state to the lifecycle spawn seam. The returned
// value intentionally contains no paths, mounts, or recipe details.
func (r ActivationRecipe) AcquirePortableNamespaceLease(ctx context.Context) (uid, gid int, release func(), err error) {
	if r.identity.effectiveUID <= 0 || r.identity.primaryGID <= 0 {
		return 0, 0, nil, activationError("activation identity")
	}
	release, err = r.lease.acquire(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	return r.identity.effectiveUID, r.identity.primaryGID, release, nil
}

type activationImmutableBinding struct {
	runtimeGuestTarget string
	contentSHA256      string
	output             harnesses.PortableRuntimeGuestPath
	identity           fileIdentity
}

func (r ActivationRecipe) String() string {
	return fmt.Sprintf("{ImmutableBindingCount:%d ReadOnlyCount:%d RequiredAbsentCount:%d}",
		len(r.immutableBindings), len(r.readOnlyPaths), len(r.requiredAbsent))
}

func (r ActivationRecipe) GoString() string { return r.String() }

func (r ActivationRecipe) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ImmutableBindingCount int `json:"immutable_binding_count"`
		ReadOnlyCount         int `json:"read_only_count"`
		RequiredAbsentCount   int `json:"required_absent_count"`
	}{len(r.immutableBindings), len(r.readOnlyPaths), len(r.requiredAbsent)})
}

// BackingRoot returns the activation-owned child beneath the caller-supplied
// writable root. This is an internal service bridge, not public bundle data.
func (p ActivationPlan) BackingRoot() string { return p.backingRoot }

func (p ActivationPlan) WorkDir() string       { return p.workDir }
func (p ActivationPlan) SessionLogDir() string { return p.sessionLogDir }

// EntrypointEnvironment returns an owned closed-world environment for one
// verified entrypoint.
func (p ActivationPlan) EntrypointEnvironment(name string) (map[string]string, bool) {
	entrypoint, ok := p.entrypoints[name]
	if !ok {
		return nil, false
	}
	return cloneStrings(entrypoint.environment), true
}

// EntrypointRecipe returns an owned opaque recipe for one entrypoint.
func (p ActivationPlan) EntrypointRecipe(name string) (ActivationRecipe, bool) {
	entrypoint, ok := p.entrypoints[name]
	if !ok {
		return ActivationRecipe{}, false
	}
	return cloneActivationRecipe(entrypoint.recipe), true
}

// AssembleActivation verifies a mounted runtime and atomically commits one
// caller-owned activation child into an existing empty writable root. It does
// not start a process or enter a namespace.
func AssembleActivation(ctx context.Context, runtimeRoot, writableRoot string, lookupEnv func(string) (string, bool)) (plan ActivationPlan, err error) {
	return AssembleActivationWithIdentityReader(ctx, runtimeRoot, writableRoot, lookupEnv, currentActivationIdentity)
}

// AssembleActivationWithIdentityReader is the internal composition seam used
// by tests. Production callers use AssembleActivation, which reads the actual
// effective UID, primary GID, and supplementary groups before any runtime or
// writable-root access.
func AssembleActivationWithIdentityReader(ctx context.Context, runtimeRoot, writableRoot string, lookupEnv func(string) (string, bool), reader ActivationIdentityReader) (plan ActivationPlan, err error) {
	if reader == nil {
		return ActivationPlan{}, activationError("activation identity")
	}
	return assembleActivationWithIdentity(ctx, runtimeRoot, writableRoot, lookupEnv, nil, func() (activationIdentity, []int, error) {
		effectiveUID, primaryGID, groups, readErr := reader()
		return activationIdentity{effectiveUID: effectiveUID, primaryGID: primaryGID}, append([]int(nil), groups...), readErr
	})
}

type activationCopyHook func(copied int) error

func assembleActivation(ctx context.Context, runtimeRoot, writableRoot string, lookupEnv func(string) (string, bool), hook activationCopyHook) (plan ActivationPlan, err error) {
	return assembleActivationWithIdentity(ctx, runtimeRoot, writableRoot, lookupEnv, hook, nil)
}

// assembleActivationWithIdentity is internal testability plumbing for the
// process-free identity gate.
func assembleActivationWithIdentity(ctx context.Context, runtimeRoot, writableRoot string, lookupEnv func(string) (string, bool), hook activationCopyHook, identityReader activationIdentityReader) (plan ActivationPlan, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if writableRoot == "" || !filepath.IsAbs(writableRoot) || filepath.Clean(writableRoot) != writableRoot {
		return ActivationPlan{}, activationError("writable root")
	}
	var identity activationIdentity
	if identityReader != nil {
		var groups []int
		identity, groups, err = identityReader()
		if err != nil {
			return ActivationPlan{}, activationError("activation identity")
		}
		if err := validateActivationIdentity(identity, groups); err != nil {
			return ActivationPlan{}, err
		}
	}
	if err := activationContext(ctx); err != nil {
		return ActivationPlan{}, err
	}
	runtime, err := openActivationRoot(runtimeRoot, lookupEnv)
	if err != nil {
		return ActivationPlan{}, err
	}
	defer runtime.Close()
	plan, err = loadActivationFromRoot(runtimeRoot, runtime, lookupEnv)
	if err != nil {
		return ActivationPlan{}, err
	}

	destination, err := openDestination(writableRoot)
	if err != nil {
		return ActivationPlan{}, activationError("writable root validation")
	}
	defer destination.close()
	if err := validateActivationGeneratedPaths(plan.manifest); err != nil {
		return ActivationPlan{}, activationError("generated path conflict")
	}
	stage, err := destination.createStage()
	if err != nil {
		return ActivationPlan{}, activationError("activation staging")
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if cleanupErr := destination.removeStage(stage); cleanupErr != nil {
			rollbackErr := errors.Join(
				activationError("activation rollback"),
				fmt.Errorf("%w: activation staging rollback failed", ErrCleanupIncomplete),
			)
			if err == nil {
				err = rollbackErr
			} else {
				err = errors.Join(err, rollbackErr)
			}
		}
	}()

	backingRoot := filepath.Join(writableRoot, activationChild)
	scopes := activationScopeRoots(backingRoot)
	for _, scope := range []harnesses.PortableRuntimeGuestPathScope{
		harnesses.PortableRuntimeGuestPathHome,
		harnesses.PortableRuntimeGuestPathConfig,
		harnesses.PortableRuntimeGuestPathData,
		harnesses.PortableRuntimeGuestPathCache,
		harnesses.PortableRuntimeGuestPathState,
		harnesses.PortableRuntimeGuestPathTmp,
	} {
		if err := ensureActivationStageDirectory(stage, string(scope)); err != nil {
			return ActivationPlan{}, activationError("scope assembly")
		}
	}
	for _, target := range []string{"tmp/runtime", "state/work", "state/sessions"} {
		if err := ensureActivationStageDirectory(stage, target); err != nil {
			return ActivationPlan{}, activationError("generated directory assembly")
		}
	}
	assets := make(map[string]ManifestAsset, len(plan.manifest.Assets))
	for _, asset := range plan.manifest.Assets {
		assets[asset.Target] = asset
	}
	copied := 0
	copiedAssets := make(map[string]string)
	copySeed := func(asset ManifestAsset, output string) error {
		if previous, exists := copiedAssets[asset.Target]; exists {
			if previous != output {
				return activationError("mutable seed ownership")
			}
			return nil
		}
		if err := activationContext(ctx); err != nil {
			return err
		}
		if err := copyActivationAsset(ctx, runtime, stage, asset, output); err != nil {
			return activationError("mutable seed copy")
		}
		copiedAssets[asset.Target] = output
		copied++
		if hook != nil {
			if err := hook(copied); err != nil {
				return activationError("mutable seed copy interrupted")
			}
		}
		return nil
	}

	for _, asset := range plan.manifest.Assets {
		if asset.SeedDisposition == SeedPrefixPreserving {
			if err := copySeed(asset, asset.Target); err != nil {
				return ActivationPlan{}, err
			}
		}
	}

	entrypointNames := make([]string, 0, len(plan.manifest.Entrypoints))
	for name := range plan.manifest.Entrypoints {
		entrypointNames = append(entrypointNames, name)
	}
	sort.Strings(entrypointNames)
	entrypoints := make(map[string]activationEntrypoint, len(entrypointNames))
	lease := newActivationSubprocessLease()
	var launcher string
	if len(entrypointNames) > 0 {
		launcher = filepath.Join(runtimeRoot, filepath.FromSlash(namespaceLauncherTarget))
	}
	assembledProjections := make(map[harnesses.PortableRuntimeGuestPath]struct{})
	for _, name := range entrypointNames {
		entrypoint := plan.manifest.Entrypoints[name]
		environment, envErr := buildActivationEnvironment(plan, entrypoint, assets, scopes)
		if envErr != nil {
			return ActivationPlan{}, envErr
		}
		recipe := ActivationRecipe{
			scopes:         cloneActivationScopes(scopes),
			readOnlyPaths:  append([]harnesses.PortableRuntimeGuestPath(nil), entrypoint.ExecutionConstraints.ReadOnlyPaths...),
			requiredAbsent: append([]harnesses.PortableRuntimeGuestPath(nil), entrypoint.ExecutionConstraints.RequiredAbsentPaths...),
			identity:       identity,
			lease:          lease,
			launcherPath:   launcher,
		}
		for _, projection := range entrypoint.StateProjections {
			projectionTarget := activationRelativeGuestPath(projection.Directory)
			if _, exists := assembledProjections[projection.Directory]; !exists {
				if err := ensureActivationStageDirectory(stage, projectionTarget); err != nil {
					return ActivationPlan{}, activationError("projection directory assembly")
				}
				assembledProjections[projection.Directory] = struct{}{}
			}
			for _, entry := range projection.Entries {
				asset := assets[entry.AssetTarget]
				outputPath := harnesses.PortableRuntimeGuestPath{
					Scope:  projection.Directory.Scope,
					Target: path.Join(projection.Directory.Target, entry.Target),
				}
				if mutableAsset(asset.Kind) {
					if asset.SeedDisposition != SeedProjectionConsumed {
						return ActivationPlan{}, activationError("projection seed assembly")
					}
					if err := copySeed(asset, activationRelativeGuestPath(outputPath)); err != nil {
						return ActivationPlan{}, err
					}
					continue
				}
				if asset.Kind != harnesses.PortableRuntimeAssetConfig {
					return ActivationPlan{}, activationError("projection config identity")
				}
				identity, err := activationAssetIdentity(runtime, asset)
				if err != nil {
					return ActivationPlan{}, activationError("projection config identity")
				}
				recipe.immutableBindings = append(recipe.immutableBindings, activationImmutableBinding{
					runtimeGuestTarget: path.Join(GuestRoot, asset.Target),
					contentSHA256:      asset.MaterializedSHA256,
					output:             outputPath,
					identity:           identity,
				})
			}
		}
		entrypoints[name] = activationEntrypoint{environment: environment, recipe: recipe}
	}

	if err := activationContext(ctx); err != nil {
		return ActivationPlan{}, err
	}
	if err := verifyRestrictiveMaterialization(stage); err != nil {
		return ActivationPlan{}, activationError("activation storage verification")
	}
	if err := activationContext(ctx); err != nil {
		return ActivationPlan{}, err
	}
	if err := destination.commitNamed(stage, activationChild); err != nil {
		return ActivationPlan{}, activationCommitError(err)
	}
	committed = true
	plan.backingRoot = backingRoot
	plan.entrypoints = entrypoints
	plan.workDir = filepath.Join(scopes[harnesses.PortableRuntimeGuestPathState], "work")
	plan.sessionLogDir = filepath.Join(scopes[harnesses.PortableRuntimeGuestPathState], "sessions")
	return plan, nil
}

func activationCommitError(cause error) error {
	commitFailure := activationError("activation storage commit")
	if errors.Is(cause, ErrCleanupIncomplete) {
		return errors.Join(commitFailure, fmt.Errorf("%w: activation post-commit rollback failed", ErrCleanupIncomplete))
	}
	return commitFailure
}

func activationScopeRoots(backingRoot string) map[harnesses.PortableRuntimeGuestPathScope]string {
	return map[harnesses.PortableRuntimeGuestPathScope]string{
		harnesses.PortableRuntimeGuestPathRuntime: GuestRoot,
		harnesses.PortableRuntimeGuestPathHome:    filepath.Join(backingRoot, "home"),
		harnesses.PortableRuntimeGuestPathConfig:  filepath.Join(backingRoot, "config"),
		harnesses.PortableRuntimeGuestPathData:    filepath.Join(backingRoot, "data"),
		harnesses.PortableRuntimeGuestPathCache:   filepath.Join(backingRoot, "cache"),
		harnesses.PortableRuntimeGuestPathState:   filepath.Join(backingRoot, "state"),
		harnesses.PortableRuntimeGuestPathTmp:     filepath.Join(backingRoot, "tmp"),
	}
}

func buildActivationEnvironment(plan ActivationPlan, entrypoint ManifestEntrypoint, assets map[string]ManifestAsset, scopes map[harnesses.PortableRuntimeGuestPathScope]string) (map[string]string, error) {
	runtimePath := activationRuntimePath(entrypoint, assets)
	environment := map[string]string{
		"HOME":            scopes[harnesses.PortableRuntimeGuestPathHome],
		"PATH":            runtimePath,
		"USER":            "fizeau",
		"LOGNAME":         "fizeau",
		"SHELL":           "/bin/sh",
		"TERM":            "xterm-256color",
		"LANG":            "C.UTF-8",
		"LC_ALL":          "C.UTF-8",
		"XDG_CONFIG_HOME": scopes[harnesses.PortableRuntimeGuestPathConfig],
		"XDG_DATA_HOME":   scopes[harnesses.PortableRuntimeGuestPathData],
		"XDG_CACHE_HOME":  scopes[harnesses.PortableRuntimeGuestPathCache],
		"XDG_STATE_HOME":  scopes[harnesses.PortableRuntimeGuestPathState],
		"XDG_RUNTIME_DIR": filepath.Join(scopes[harnesses.PortableRuntimeGuestPathTmp], "runtime"),
		"TMPDIR":          scopes[harnesses.PortableRuntimeGuestPathTmp],
	}
	for _, inherited := range entrypoint.Environment {
		value, exists := plan.inheritedEnvironment[inherited.Name]
		if !exists || strings.ContainsRune(value, 0) {
			return nil, activationError("inherited environment value")
		}
		environment[inherited.Name] = value
	}
	for _, constraint := range entrypoint.ExecutionConstraints.Environment {
		switch constraint.Kind {
		case harnesses.PortableRuntimeEnvironmentFixedTrue:
			environment[constraint.Name] = "true"
		case harnesses.PortableRuntimeEnvironmentFixedFalse:
			environment[constraint.Name] = "false"
		case harnesses.PortableRuntimeEnvironmentGuestPath:
			root, exists := scopes[constraint.GuestPath.Scope]
			if !exists {
				return nil, activationError("environment guest scope")
			}
			environment[constraint.Name] = joinActivationGuestPath(root, constraint.GuestPath.Target)
		case harnesses.PortableRuntimeEnvironmentUnset:
			delete(environment, constraint.Name)
		case harnesses.PortableRuntimeEnvironmentRuntimePath:
			environment[constraint.Name] = runtimePath
		default:
			return nil, activationError("environment treatment")
		}
	}
	return environment, nil
}

func activationRuntimePath(entrypoint ManifestEntrypoint, assets map[string]ManifestAsset) string {
	directories := make(map[string]struct{})
	for _, target := range entrypoint.AssetTargets {
		asset := assets[target]
		if asset.Executable {
			directories[path.Dir(path.Join(GuestRoot, asset.Target))] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(directories))
	for directory := range directories {
		sorted = append(sorted, directory)
	}
	sort.Strings(sorted)
	for _, directory := range fixedPortableToolPath {
		if _, exists := directories[directory]; !exists {
			sorted = append(sorted, directory)
		}
	}
	return strings.Join(sorted, ":")
}

func joinActivationGuestPath(root, target string) string {
	if target == "" {
		return root
	}
	return path.Join(root, target)
}

func activationRelativeGuestPath(guestPath harnesses.PortableRuntimeGuestPath) string {
	if guestPath.Target == "" {
		return string(guestPath.Scope)
	}
	return path.Join(string(guestPath.Scope), guestPath.Target)
}

func cloneActivationScopes(src map[harnesses.PortableRuntimeGuestPathScope]string) map[harnesses.PortableRuntimeGuestPathScope]string {
	out := make(map[harnesses.PortableRuntimeGuestPathScope]string, len(src))
	for scope, root := range src {
		out[scope] = root
	}
	return out
}

func cloneActivationRecipe(src ActivationRecipe) ActivationRecipe {
	return ActivationRecipe{
		scopes:            cloneActivationScopes(src.scopes),
		immutableBindings: append([]activationImmutableBinding(nil), src.immutableBindings...),
		readOnlyPaths:     append([]harnesses.PortableRuntimeGuestPath(nil), src.readOnlyPaths...),
		requiredAbsent:    append([]harnesses.PortableRuntimeGuestPath(nil), src.requiredAbsent...),
		identity:          src.identity,
		lease:             src.lease,
		launcherPath:      src.launcherPath,
	}
}

func activationContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return activationError("activation canceled")
	default:
		return nil
	}
}

func validateActivationGeneratedPaths(manifest Manifest) error {
	generated := []harnesses.PortableRuntimeGuestPath{
		{Scope: harnesses.PortableRuntimeGuestPathTmp, Target: "runtime"},
		{Scope: harnesses.PortableRuntimeGuestPathState, Target: "work"},
		{Scope: harnesses.PortableRuntimeGuestPathState, Target: "sessions"},
	}
	absent := make([]harnesses.PortableRuntimeGuestPath, 0)
	for _, asset := range manifest.Assets {
		if asset.SeedDisposition == SeedPrefixPreserving {
			prefix, suffix, ok := strings.Cut(asset.Target, "/")
			if !ok || suffix == "" {
				return errors.New("mutable seed target is invalid")
			}
			generated = append(generated, harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathScope(prefix), Target: suffix})
		}
	}
	for _, entrypoint := range manifest.Entrypoints {
		for _, constraint := range entrypoint.ExecutionConstraints.Environment {
			if constraint.Kind == harnesses.PortableRuntimeEnvironmentGuestPath && constraint.GuestPath.Scope != harnesses.PortableRuntimeGuestPathRuntime {
				generated = append(generated, constraint.GuestPath)
			}
		}
		for _, projection := range entrypoint.StateProjections {
			for _, entry := range projection.Entries {
				generated = append(generated, harnesses.PortableRuntimeGuestPath{Scope: projection.Directory.Scope, Target: path.Join(projection.Directory.Target, entry.Target)})
			}
		}
		absent = append(absent, entrypoint.ExecutionConstraints.RequiredAbsentPaths...)
	}
	for _, required := range absent {
		for _, created := range generated {
			if guestPathsOverlap(required, created) {
				return errors.New("generated path overlaps required-absent path")
			}
		}
	}
	return nil
}
