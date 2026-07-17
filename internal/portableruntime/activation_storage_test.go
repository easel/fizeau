package portableruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestPortableRuntimeActivationBuildsClosedEnvironment(t *testing.T) {
	fixture := newActivationFixture(t)
	secondEnvironment := "FIZEAU_PORTABLE_RUNTIME_SECOND_ENV"
	secondValue := "second-environment-secret-98e1"
	t.Setenv(secondEnvironment, secondValue)
	first := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
	first.ExecutionConstraints.Environment = []harnesses.PortableRuntimeEnvironmentConstraint{
		{Name: "FEATURE_DISABLED", Kind: harnesses.PortableRuntimeEnvironmentFixedFalse},
		{Name: "FEATURE_ENABLED", Kind: harnesses.PortableRuntimeEnvironmentFixedTrue},
		{Name: "PATH", Kind: harnesses.PortableRuntimeEnvironmentRuntimePath},
		{Name: "TERM", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "TOOL_HOME", Kind: harnesses.PortableRuntimeEnvironmentGuestPath, GuestPath: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: "tool"}},
	}
	second := first
	second.Environment = []harnesses.PortableRuntimeEnvironment{{Name: secondEnvironment}}
	second.ExecutionConstraints = harnesses.PortableRuntimeExecutionConstraints{}
	fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: first}
	fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
		Name: "fixture-second", Transport: harnesses.PortableRuntimeTransportSubprocess,
		Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: second},
	})
	bundle := prepareMaterializerFixture(t, fixture)
	writable := emptyActivationWritableRoot(t)
	lookups := make([]string, 0, 2)
	values := map[string]string{fixture.environmentKey: fixture.environmentVal, secondEnvironment: secondValue}
	plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), writable, func(name string) (string, bool) {
		lookups = append(lookups, name)
		value, ok := values[name]
		return value, ok
	}, testActivationIdentityReader)
	if err != nil {
		t.Fatalf("AssembleActivation() error = %v", err)
	}
	sort.Strings(lookups)
	wantLookups := []string{fixture.environmentKey, secondEnvironment}
	sort.Strings(wantLookups)
	if !reflect.DeepEqual(lookups, wantLookups) {
		t.Fatalf("environment lookups = %#v", lookups)
	}

	firstEnvironment, ok := plan.EntrypointEnvironment("fixture")
	if !ok {
		t.Fatal("first entrypoint environment missing")
	}
	secondEnvironmentMap, ok := plan.EntrypointEnvironment("fixture-second")
	if !ok {
		t.Fatal("second entrypoint environment missing")
	}
	backing := filepath.Join(writable, activationChild)
	wantBaseline := map[string]string{
		"HOME": filepath.Join(backing, "home"), "PATH": GuestRoot + "/bin:" + strings.Join(fixedPortableToolPath, ":"),
		"USER": "fizeau", "LOGNAME": "fizeau", "SHELL": "/bin/sh", "TERM": "xterm-256color", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
		"XDG_CONFIG_HOME": filepath.Join(backing, "config"), "XDG_DATA_HOME": filepath.Join(backing, "data"),
		"XDG_CACHE_HOME": filepath.Join(backing, "cache"), "XDG_STATE_HOME": filepath.Join(backing, "state"),
		"XDG_RUNTIME_DIR": filepath.Join(backing, "tmp", "runtime"), "TMPDIR": filepath.Join(backing, "tmp"),
	}
	wantFirst := cloneStrings(wantBaseline)
	delete(wantFirst, "TERM")
	wantFirst["FEATURE_ENABLED"] = "true"
	wantFirst["FEATURE_DISABLED"] = "false"
	wantFirst["TOOL_HOME"] = filepath.Join(backing, "home", "tool")
	wantFirst[fixture.environmentKey] = fixture.environmentVal
	wantSecond := cloneStrings(wantBaseline)
	wantSecond[secondEnvironment] = secondValue
	if !reflect.DeepEqual(firstEnvironment, wantFirst) || !reflect.DeepEqual(secondEnvironmentMap, wantSecond) {
		t.Fatalf("closed environments = first %#v, second %#v; want first %#v, second %#v", firstEnvironment, secondEnvironmentMap, wantFirst, wantSecond)
	}
	for _, environment := range []map[string]string{firstEnvironment, secondEnvironmentMap} {
		for _, absent := range []string{"HOST_SECRET", "UNDECLARED_HOST_VALUE"} {
			if _, exists := environment[absent]; exists {
				t.Fatalf("closed environment contains %q: %#v", absent, environment)
			}
		}
	}
	if firstEnvironment[fixture.environmentKey] != fixture.environmentVal || firstEnvironment[secondEnvironment] != "" {
		t.Fatalf("first inherited environment crossed entrypoints: %#v", firstEnvironment)
	}
	if secondEnvironmentMap[secondEnvironment] != secondValue || secondEnvironmentMap[fixture.environmentKey] != "" {
		t.Fatalf("second inherited environment crossed entrypoints: %#v", secondEnvironmentMap)
	}
	firstEnvironment["HOME"] = "mutated"
	again, _ := plan.EntrypointEnvironment("fixture")
	if again["HOME"] != filepath.Join(backing, "home") {
		t.Fatal("entrypoint environment accessor aliases activation state")
	}
}

func TestPortableRuntimeActivationCopiesPrefixSeeds(t *testing.T) {
	fixture := newActivationFixture(t)
	contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
	contribution.StateProjections = nil
	quota := writeMaterializerSource(t, fixture.sourceRoot, "quota.json", []byte("quota-seed\n"), 0o600)
	cacheRoot := filepath.Join(fixture.sourceRoot, "cache-tree")
	if err := os.MkdirAll(filepath.Join(cacheRoot, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeMaterializerSource(t, cacheRoot, "cache.json", []byte("cache-seed\n"), 0o600)
	writeMaterializerSource(t, cacheRoot, "nested/member.json", []byte("nested-cache\n"), 0o600)
	cacheDigest, err := harnesses.PortableRuntimeTreeDigest(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	contribution.Assets = append(contribution.Assets,
		portableFileAsset(t, quota, "state/provider/quota.json", harnesses.PortableRuntimeAssetQuota, false),
		harnesses.PortableRuntimeAsset{Kind: harnesses.PortableRuntimeAssetCache, PathKind: harnesses.PortableRuntimePathTree,
			Source: cacheRoot, Target: "cache/provider/cache-v1", ContentSHA256: cacheDigest},
	)
	fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
	bundle := prepareMaterializerFixture(t, fixture)
	runtimeRoot := bundle.RuntimeRoot()
	writable := emptyActivationWritableRoot(t)
	plan, err := AssembleActivationWithIdentityReader(context.Background(), runtimeRoot, writable, os.LookupEnv, testActivationIdentityReader)
	if err != nil {
		t.Fatalf("AssembleActivation() error = %v", err)
	}
	backing := plan.BackingRoot()
	checks := map[string]string{
		"data/tool/auth.json":                        `{"token":"file-secret-4b2a"}` + "\n",
		"state/provider/quota.json":                  "quota-seed\n",
		"cache/provider/cache-v1/cache.json":         "cache-seed\n",
		"cache/provider/cache-v1/nested/member.json": "nested-cache\n",
	}
	for target, want := range checks {
		name := filepath.Join(backing, filepath.FromSlash(target))
		data, err := os.ReadFile(name)
		if err != nil || string(data) != want {
			t.Fatalf("seed %s = %q, %v", target, data, err)
		}
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("seed mode %s = %v, %v", target, info.Mode().Perm(), err)
		}
	}
	for _, directory := range []string{"data", "data/tool", "state", "state/provider", "cache", "cache/provider", "cache/provider/cache-v1", "cache/provider/cache-v1/nested"} {
		info, err := os.Stat(filepath.Join(backing, filepath.FromSlash(directory)))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %s = %#v, %v", directory, info, err)
		}
	}
	writableCredential := filepath.Join(backing, "data", "tool", "auth.json")
	if err := os.WriteFile(writableCredential, []byte("refreshed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeCredential, err := os.ReadFile(filepath.Join(runtimeRoot, "data", "tool", "auth.json"))
	if err != nil || string(runtimeCredential) != `{"token":"file-secret-4b2a"}`+"\n" {
		t.Fatalf("activation copy wrote back to runtime: %q, %v", runtimeCredential, err)
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bundle runtime survived Close: %v", err)
	}
	if data, err := os.ReadFile(writableCredential); err != nil || string(data) != "refreshed\n" {
		t.Fatalf("bundle Close owned activation storage: %q, %v", data, err)
	}

	for _, target := range []string{"data", "state", "cache"} {
		t.Run("rejects-bare-"+target, func(t *testing.T) {
			candidate := newActivationFixture(t)
			candidateContribution := candidate.request.Inventory[0].Instance.(materializerTestHarness).contribution
			candidateContribution.StateProjections = nil
			for i := range candidateContribution.Assets {
				if candidateContribution.Assets[i].Kind == harnesses.PortableRuntimeAssetCredential {
					candidateContribution.Assets[i].Target = target
				}
			}
			candidate.request.Inventory[0].Instance = materializerTestHarness{contribution: candidateContribution}
			if _, err := Prepare(context.Background(), candidate.request); !errors.Is(err, ErrClosureIncomplete) {
				t.Fatalf("bare target Prepare() error = %v", err)
			}
			assertDirectoryEmpty(t, candidate.destination)
		})
	}
}

func TestPortableRuntimeActivationAssemblesProjectionSeeds(t *testing.T) {
	fixture := newActivationFixture(t)
	contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
	for i := range contribution.Assets {
		if contribution.Assets[i].Kind == harnesses.PortableRuntimeAssetCredential {
			contribution.Assets[i].Target = "data/seed/auth.json"
		}
	}
	contribution.StateProjections = []harnesses.PortableRuntimeStateProjection{{
		Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".tool"},
		Entries: []harnesses.PortableRuntimeStateProjectionEntry{
			{AssetTarget: "config/tool/settings.json", Target: "settings.json"},
			{AssetTarget: "data/seed/auth.json", Target: "credentials.json"},
		},
	}}
	fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
	fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
		Name: "fixture-shared", Transport: harnesses.PortableRuntimeTransportSubprocess,
		Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: contribution},
	})
	bundle := prepareMaterializerFixture(t, fixture)
	writable := emptyActivationWritableRoot(t)
	plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), writable, os.LookupEnv, testActivationIdentityReader)
	if err != nil {
		t.Fatalf("AssembleActivation() error = %v", err)
	}
	projectedCredential := filepath.Join(plan.BackingRoot(), "home", ".tool", "credentials.json")
	if data, err := os.ReadFile(projectedCredential); err != nil || string(data) != `{"token":"file-secret-4b2a"}`+"\n" {
		t.Fatalf("projected credential = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(plan.BackingRoot(), "data", "seed", "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("projection-consumed seed also received prefix copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plan.BackingRoot(), "home", ".tool", "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("immutable config was copied into mutable storage: %v", err)
	}
	var firstOutput harnesses.PortableRuntimeGuestPath
	for _, entrypoint := range []string{"fixture", "fixture-shared"} {
		recipe, ok := plan.EntrypointRecipe(entrypoint)
		if !ok || len(recipe.immutableBindings) != 1 {
			t.Fatalf("%s recipe = %#v, %t", entrypoint, recipe, ok)
		}
		binding := recipe.immutableBindings[0]
		if binding.runtimeGuestTarget != GuestRoot+"/config/tool/settings.json" || binding.contentSHA256 == "" || reflect.DeepEqual(binding.identity, fileIdentity{}) ||
			binding.output != (harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".tool/settings.json"}) {
			t.Fatalf("%s immutable binding = %#v", entrypoint, binding)
		}
		if firstOutput == (harnesses.PortableRuntimeGuestPath{}) {
			firstOutput = binding.output
		} else if binding.output != firstOutput {
			t.Fatalf("shared projection outputs differ: %#v != %#v", binding.output, firstOutput)
		}
		recipe.scopes[harnesses.PortableRuntimeGuestPathHome] = "mutated"
		again, _ := plan.EntrypointRecipe(entrypoint)
		if again.scopes[harnesses.PortableRuntimeGuestPathHome] != filepath.Join(plan.BackingRoot(), "home") {
			t.Fatal("recipe accessor aliases activation state")
		}
	}
	info, err := os.Stat(filepath.Dir(projectedCredential))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("projection directory mode = %#v, %v", info, err)
	}

	t.Run("failure-and-cancellation-roll-back-staging", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			prepare func() (context.Context, activationCopyHook)
		}{
			{name: "pre-canceled", prepare: func() (context.Context, activationCopyHook) {
				return canceledActivationContext(t), nil
			}},
			{name: "mid-copy-canceled", prepare: func() (context.Context, activationCopyHook) {
				ctx, cancel := context.WithCancel(context.Background())
				return ctx, func(int) error { cancel(); return nil }
			}},
			{name: "mid-copy-failure", prepare: func() (context.Context, activationCopyHook) {
				return context.Background(), func(int) error { return errors.New("stop") }
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				ctx, hook := test.prepare()
				parent := t.TempDir()
				writable := filepath.Join(parent, "writable")
				if err := os.Mkdir(writable, 0o700); err != nil {
					t.Fatal(err)
				}
				if _, err := assembleActivation(ctx, bundle.RuntimeRoot(), writable, os.LookupEnv, hook); !errors.Is(err, ErrActivationInvalid) {
					t.Fatalf("assembleActivation() error = %v", err)
				}
				assertDirectoryEmpty(t, writable)
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 1 || entries[0].Name() != "writable" {
					t.Fatalf("activation staging survived rollback: %#v, %v", entries, err)
				}
			})
		}
	})

	t.Run("config-replacement-after-load-is-rejected", func(t *testing.T) {
		candidate := newActivationFixture(t)
		candidateBundle := prepareMaterializerFixture(t, candidate)
		writable := emptyActivationWritableRoot(t)
		_, err := assembleActivation(context.Background(), candidateBundle.RuntimeRoot(), writable, os.LookupEnv, func(int) error {
			return os.WriteFile(filepath.Join(candidateBundle.RuntimeRoot(), "config", "tool", "settings.json"), []byte("replaced\n"), 0o600)
		})
		if !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("post-load config replacement error = %v", err)
		}
		assertDirectoryEmpty(t, writable)
	})

	diagnostics := fmt.Sprintf("%v %+v %#v %s", plan, plan, plan, mustActivationJSON(t, plan))
	recipe, _ := plan.EntrypointRecipe("fixture")
	diagnostics += fmt.Sprintf(" %v %+v %#v %s", recipe, recipe, recipe, mustActivationJSON(t, recipe))
	for _, forbidden := range []string{fixture.environmentVal, fixture.apiKey, fixture.headerValue, bundle.RuntimeRoot(), plan.BackingRoot(), projectedCredential} {
		if strings.Contains(diagnostics, forbidden) {
			t.Fatalf("activation diagnostics leak %q: %s", forbidden, diagnostics)
		}
	}
}

func TestPortableRuntimeActivationRejectsGeneratedRequiredAbsentConflict(t *testing.T) {
	for _, test := range []struct {
		name string
		path harnesses.PortableRuntimeGuestPath
	}{
		{name: "prefix-seed", path: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathData, Target: "tool/auth.json"}},
		{name: "work-directory", path: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathState, Target: "work"}},
		{name: "session-directory", path: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathState, Target: "sessions"}},
		{name: "runtime-temporary-directory", path: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathTmp, Target: "runtime"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newActivationFixture(t)
			contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
			contribution.StateProjections = nil
			contribution.ExecutionConstraints.RequiredAbsentPaths = []harnesses.PortableRuntimeGuestPath{test.path}
			fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
			if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
				t.Fatalf("Prepare() generated/absent conflict error = %v", err)
			}
			assertDirectoryEmpty(t, fixture.destination)
		})
	}
}

func TestPortableRuntimeActivationGuestPathRejectsTraversal(t *testing.T) {
	for _, target := range []string{".", "..", "../escape", "nested/../../escape"} {
		if _, err := (ActivationPlan{}).GuestPath(target); !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("GuestPath(%q) error = %v", target, err)
		}
	}
}

func TestPortableRuntimeActivationCommitErrorPreservesCleanupOwnership(t *testing.T) {
	cleanupFailure := fmt.Errorf("%w: private detail", ErrCleanupIncomplete)
	err := activationCommitError(cleanupFailure)
	if !errors.Is(err, ErrActivationInvalid) || !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("activationCommitError() = %v", err)
	}
	if strings.Contains(err.Error(), "private detail") {
		t.Fatalf("activation commit error leaks private cause: %v", err)
	}
}

func TestPortableRuntimeRejectsUnsafeActivationIdentity(t *testing.T) {
	fixture := newActivationFixture(t)
	bundle := prepareMaterializerFixture(t, fixture)

	for _, test := range []struct {
		name     string
		identity activationIdentity
		groups   []int
	}{
		{name: "zero-effective-uid", identity: activationIdentity{effectiveUID: 0, primaryGID: 65532}},
		{name: "zero-primary-gid", identity: activationIdentity{effectiveUID: 65532, primaryGID: 0}},
		{name: "supplementary-group", identity: activationIdentity{effectiveUID: 65532, primaryGID: 65532}, groups: []int{4}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookups := 0
			writable := emptyActivationWritableRoot(t)
			_, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), writable, func(string) (string, bool) {
				lookups++
				return fixture.environmentVal, true
			}, func() (int, int, []int, error) {
				return test.identity.effectiveUID, test.identity.primaryGID, append([]int(nil), test.groups...), nil
			})
			if !errors.Is(err, ErrActivationInvalid) {
				t.Fatalf("assembleActivationWithIdentity() error = %v", err)
			}
			if lookups != 0 {
				t.Fatalf("unsafe identity performed %d runtime environment lookups", lookups)
			}
			assertDirectoryEmpty(t, writable)
		})
	}

	writable := emptyActivationWritableRoot(t)
	plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), writable, func(string) (string, bool) {
		return fixture.environmentVal, true
	}, testActivationIdentityReader)
	if err != nil {
		t.Fatalf("safe identity rejected: %v", err)
	}
	recipe, ok := plan.EntrypointRecipe("fixture")
	if !ok || recipe.identity != (activationIdentity{effectiveUID: 65532, primaryGID: 65532}) || recipe.lease == nil {
		t.Fatalf("activation recipe did not capture safe identity and lease: %#v, %t", recipe.identity, recipe.lease != nil)
	}
	diagnostics := fmt.Sprintf("%v %+v %#v %s", plan, recipe, recipe, mustActivationJSON(t, recipe))
	for _, forbidden := range []string{"65532", writable, bundle.RuntimeRoot()} {
		if strings.Contains(diagnostics, forbidden) {
			t.Fatalf("activation diagnostics leak %q: %s", forbidden, diagnostics)
		}
	}
}

func TestPortableRuntimeExclusiveSubprocessLease(t *testing.T) {
	fixture := newActivationFixture(t)
	bundle := prepareMaterializerFixture(t, fixture)
	assemble := func() ActivationRecipe {
		t.Helper()
		plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), emptyActivationWritableRoot(t), func(string) (string, bool) {
			return fixture.environmentVal, true
		}, testActivationIdentityReader)
		if err != nil {
			t.Fatal(err)
		}
		recipe, ok := plan.EntrypointRecipe("fixture")
		if !ok {
			t.Fatal("activation recipe missing")
		}
		return recipe
	}

	first := assemble()
	clone := cloneActivationRecipe(first)
	releaseFirst, err := first.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	queued, cancelQueued := context.WithCancel(context.Background())
	started := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(started)
		_, err := clone.lease.acquire(queued)
		secondResult <- err
	}()
	<-started
	cancelQueued()
	if err := <-secondResult; !errors.Is(err, ErrActivationInvalid) {
		t.Fatalf("canceled queued acquire error = %v", err)
	}
	releaseFirst()
	releaseFirst()
	releaseClone, err := clone.lease.acquire(context.Background())
	if err != nil {
		t.Fatalf("released clone lease acquire error = %v", err)
	}
	releaseClone()

	releaseHeld, err := first.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	independent := assemble()
	releaseIndependent, err := independent.lease.acquire(context.Background())
	if err != nil {
		t.Fatalf("independent activation root was serialized with first root: %v", err)
	}
	releaseIndependent()
	releaseHeld()

	first.lease.close()
	if _, err := first.lease.acquire(context.Background()); !errors.Is(err, ErrActivationInvalid) {
		t.Fatalf("closed activation lease acquire error = %v", err)
	}
}

func emptyActivationWritableRoot(t *testing.T) string {
	t.Helper()
	writable := filepath.Join(t.TempDir(), "writable")
	if err := os.Mkdir(writable, 0o700); err != nil {
		t.Fatal(err)
	}
	return writable
}

func canceledActivationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func testActivationIdentityReader() (int, int, []int, error) {
	return 65532, 65532, nil, nil
}
