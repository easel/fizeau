package portableruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"golang.org/x/sys/unix"
)

type materializerTestHarness struct {
	contribution harnesses.PortableRuntimeContribution
	beforeReturn func()
}

func (materializerTestHarness) Info() harnesses.HarnessInfo {
	panic("materializer must not inspect harness info")
}
func (materializerTestHarness) HealthCheck(context.Context) error {
	panic("materializer must not contact a harness")
}
func (materializerTestHarness) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("materializer must not execute a harness")
}
func (h materializerTestHarness) PortableRuntimeAssets(context.Context, harnesses.PortableRuntimeTarget) (harnesses.PortableRuntimeContribution, error) {
	if h.beforeReturn != nil {
		h.beforeReturn()
	}
	return h.contribution, nil
}

type materializerFixture struct {
	request        Request
	destination    string
	sourceRoot     string
	executable     string
	config         string
	credential     string
	environmentKey string
	environmentVal string
	apiKey         string
	headerValue    string
}

func newMaterializerFixture(t *testing.T) materializerFixture {
	t.Helper()
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	sourceRoot := t.TempDir()
	executable := writeMaterializerSource(t, sourceRoot, "runner", []byte("portable executable\n"), 0o755)
	config := writeMaterializerSource(t, sourceRoot, "settings.json", []byte(`{"mode":"portable"}`+"\n"), 0o644)
	credential := writeMaterializerSource(t, sourceRoot, "auth.json", []byte(`{"token":"file-secret-4b2a"}`+"\n"), 0o600)
	environmentKey := "FIZEAU_PORTABLE_RUNTIME_TEST_ENV"
	environmentVal := "environment-secret-37d9"
	t.Setenv(environmentKey, environmentVal)

	contribution := harnesses.PortableRuntimeContribution{
		ClosureClass: harnesses.PortableRuntimeClosureStatic,
		Launch:       harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/runner"},
		Assets: []harnesses.PortableRuntimeAsset{
			portableFileAsset(t, executable, "bin/runner", harnesses.PortableRuntimeAssetExecutable, true),
			portableFileAsset(t, config, "config/tool/settings.json", harnesses.PortableRuntimeAssetConfig, false),
			portableFileAsset(t, credential, "data/tool/auth.json", harnesses.PortableRuntimeAssetCredential, false),
		},
		Environment: []harnesses.PortableRuntimeEnvironment{{Name: environmentKey}},
		ExecutionConstraints: harnesses.PortableRuntimeExecutionConstraints{
			FixedArguments: []string{"--fixed", "--verbose"},
			FixedOptionValues: []harnesses.PortableRuntimeFixedOptionValue{
				{Option: "--format", Value: "portable"},
				{Option: "-e", Value: "builtin"},
			},
			RequiredAbsentPaths: []harnesses.PortableRuntimeGuestPath{{
				Scope: harnesses.PortableRuntimeGuestPathData, Target: "tool/forbidden.lock",
			}},
		},
		StateProjections: []harnesses.PortableRuntimeStateProjection{{
			Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathData, Target: "tool"},
			Entries: []harnesses.PortableRuntimeStateProjectionEntry{
				{AssetTarget: "config/tool/settings.json", Target: "settings.json"},
				{AssetTarget: "data/tool/auth.json", Target: "auth.json"},
			},
		}},
	}
	if _, err := harnesses.NormalizePortableRuntimeContribution(harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}, contribution); err != nil {
		t.Fatalf("test contribution is invalid: %v", err)
	}
	providerName := "fixture-provider"
	apiKey := "api-secret-91c83d"
	headerValue := "Bearer header-secret-765a"
	request := Request{
		DestinationRoot: destination,
		Target:          harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH},
		Inventory: []harnesses.PortableRuntimeSurface{
			{Name: "fixture", Transport: harnesses.PortableRuntimeTransportSubprocess, Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: contribution}},
			{Name: "remote", Transport: harnesses.PortableRuntimeTransportHTTP, Inclusion: harnesses.PortableRuntimeInclusionNonSubprocess},
		},
		Providers: ProviderSnapshot{
			ProviderNames:       []string{providerName},
			DefaultProviderName: providerName,
			Providers: []ConfiguredProvider{{
				Name: providerName, Type: "fixture", BaseURL: "https://provider.invalid/v1", Model: "fixture-model",
				ConfigError: "safe structural diagnostic",
			}},
			WorkDir:       ConfigField{Field: "WorkDir", Treatment: "guest_private", Reason: "remapped"},
			SessionLogDir: ConfigField{Field: "SessionLogDir", Treatment: "excluded", Reason: "not portable"},
		},
		ProviderSecrets: []ProviderSecret{NewProviderSecret(providerName, apiKey, map[string]string{"Authorization": headerValue})},
	}
	return materializerFixture{request, destination, sourceRoot, executable, config, credential, environmentKey, environmentVal, apiKey, headerValue}
}

func writeMaterializerSource(t *testing.T, root, name string, content []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func portableFileAsset(t *testing.T, source, target string, kind harnesses.PortableRuntimeAssetKind, executable bool) harnesses.PortableRuntimeAsset {
	t.Helper()
	digest, err := harnesses.PortableRuntimeFileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	return harnesses.PortableRuntimeAsset{Kind: kind, PathKind: harnesses.PortableRuntimePathFile, Source: source, Target: target, ContentSHA256: digest, Executable: executable}
}

func prepareMaterializerFixture(t *testing.T, fixture materializerFixture) *Bundle {
	t.Helper()
	bundle, err := Prepare(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	return bundle
}

func readFixtureManifest(t *testing.T, bundle *Bundle) (Manifest, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(manifestTarget)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, data
}

func TestPreparePortableRuntimeOwnsHarnessFilesAndEnvironment(t *testing.T) {
	fixture := newMaterializerFixture(t)
	bundle := prepareMaterializerFixture(t, fixture)
	mounts := bundle.Mounts()
	if len(mounts) != 1 || mounts[0] != (Mount{Source: filepath.Join(fixture.destination, "runtime"), Target: GuestRoot, ReadOnly: true}) {
		t.Fatalf("mounts = %#v", mounts)
	}
	if got := bundle.EnvironmentNames(); !reflect.DeepEqual(got, []string{fixture.environmentKey}) {
		t.Fatalf("environment names = %#v", got)
	}
	manifest, manifestBytes := readFixtureManifest(t, bundle)
	if manifest.Version != manifestVersion || manifest.GuestRoot != GuestRoot || manifest.TargetGOOS != "linux" || manifest.TargetGOARCH != runtime.GOARCH {
		t.Fatalf("manifest header = %#v", manifest)
	}
	if len(manifest.Inventory) != 2 || len(manifest.Entrypoints) != 1 || manifest.Entrypoints["fixture"].Name != "fixture" {
		t.Fatalf("inventory/entrypoints = %#v / %#v", manifest.Inventory, manifest.Entrypoints)
	}
	entrypoint := manifest.Entrypoints["fixture"]
	if !reflect.DeepEqual(entrypoint.ExecutionConstraints.FixedArguments, []string{"--fixed", "--verbose"}) ||
		!reflect.DeepEqual(entrypoint.ExecutionConstraints.FixedOptionValues, []harnesses.PortableRuntimeFixedOptionValue{{Option: "--format", Value: "portable"}, {Option: "-e", Value: "builtin"}}) {
		t.Fatalf("fixed prefix declarations changed: %#v", entrypoint.ExecutionConstraints)
	}
	if len(entrypoint.StateProjections) != 1 || len(manifest.Assets) != 3 {
		t.Fatalf("state/assets missing: %#v / %#v", entrypoint.StateProjections, manifest.Assets)
	}
	for _, target := range []string{"bin/runner", "config/tool/settings.json", "data/tool/auth.json"} {
		if _, err := os.Stat(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(target))); err != nil {
			t.Fatalf("materialized %s: %v", target, err)
		}
	}
	sum, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(manifestSum)))
	if err != nil || strings.TrimSpace(string(sum)) == "" {
		t.Fatalf("manifest checksum = %q, error %v", sum, err)
	}
	if strings.Contains(string(manifestBytes), fixture.sourceRoot) || strings.Contains(string(manifestBytes), fixture.environmentVal) {
		t.Fatal("manifest contains host source or environment value")
	}
	secretBytes, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(providerSecrets)))
	if err != nil || !strings.Contains(string(secretBytes), fixture.apiKey) || !strings.Contains(string(secretBytes), fixture.headerValue) {
		t.Fatalf("private provider snapshot is incomplete: %v", err)
	}
}

func TestPortableRuntimeSharedProjectionOwnership(t *testing.T) {
	t.Run("exact shared projection deduplicates", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		shared := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
			Name: "shared-second", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: shared},
		})
		bundle := prepareMaterializerFixture(t, fixture)
		manifest, _ := readFixtureManifest(t, bundle)
		if len(manifest.Entrypoints) != 2 || len(manifest.Assets) != 3 {
			t.Fatalf("shared entrypoints/assets = %d/%d", len(manifest.Entrypoints), len(manifest.Assets))
		}
		for _, asset := range manifest.Assets {
			if asset.Target == "data/tool/auth.json" && asset.SeedDisposition != SeedProjectionConsumed {
				t.Fatalf("shared projected seed disposition = %q", asset.SeedDisposition)
			}
		}
	})

	t.Run("mixed projected and prefix ownership rejects", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		unprojected := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		unprojected.StateProjections = nil
		fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
			Name: "unprojected-second", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: unprojected},
		})
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("Prepare() error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})
}

func TestPortableRuntimeRejectsUnsafeDestination(t *testing.T) {
	base := newMaterializerFixture(t)
	unsafe := map[string]func(*testing.T) string{
		"relative": func(*testing.T) string { return "relative-destination" },
		"traversal": func(t *testing.T) string {
			root := t.TempDir()
			destination := filepath.Join(root, "destination")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			return root + string(filepath.Separator) + "unused" + string(filepath.Separator) + ".." + string(filepath.Separator) + "destination"
		},
		"absent": func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") },
		"file": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		},
		"non-empty": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "destination")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		},
		"leaf symlink": func(t *testing.T) string {
			root := t.TempDir()
			real := filepath.Join(root, "real")
			if err := os.Mkdir(real, 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
		"ancestor symlink": func(t *testing.T) string {
			root := t.TempDir()
			real := filepath.Join(root, "real")
			if err := os.MkdirAll(filepath.Join(real, "destination"), 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(link, "destination")
		},
	}

	t.Run("target mismatch", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		fixture.request.Target.GOARCH = "not-" + runtime.GOARCH
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrRequestInvalid) {
			t.Fatalf("Prepare() error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	t.Run("destination identity replacement", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		moved := fixture.destination + "-original"
		contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		fixture.request.Inventory[0].Instance = materializerTestHarness{
			contribution: contribution,
			beforeReturn: func() {
				if err := os.Rename(fixture.destination, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(fixture.destination, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		}
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrRequestInvalid) {
			t.Fatalf("Prepare() error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
		assertDirectoryEmpty(t, moved)
		assertNoMaterializerStages(t, fixture.destination)
	})
	for name, makeDestination := range unsafe {
		t.Run(name, func(t *testing.T) {
			request := base.request
			request.DestinationRoot = makeDestination(t)
			if bundle, err := Prepare(context.Background(), request); err == nil || bundle != nil {
				t.Fatalf("Prepare() = %#v, %v", bundle, err)
			}
		})
	}

	t.Run("direct source symlink", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		link := filepath.Join(t.TempDir(), "runner-link")
		if err := os.Symlink(fixture.executable, link); err != nil {
			t.Fatal(err)
		}
		contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		contribution.Assets[0].Source = link
		fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("Prepare() error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	for _, target := range []string{"../escape", ".fizeau/foreign"} {
		t.Run("rejected target "+strings.ReplaceAll(target, "/", "-"), func(t *testing.T) {
			fixture := newMaterializerFixture(t)
			contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
			contribution.Assets[0].Target = target
			contribution.Launch.EntrypointTarget = target
			fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
			if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
				t.Fatalf("Prepare() error = %v", err)
			}
			assertDirectoryEmpty(t, fixture.destination)
		})
	}

	t.Run("tree symlink policy", func(t *testing.T) {
		for _, test := range []struct {
			name       string
			linkTarget func(root string) string
			wantOK     bool
		}{
			{name: "safe absolute in-tree", linkTarget: func(root string) string { return filepath.Join(root, "regular") }, wantOK: true},
			{name: "escaping", linkTarget: func(root string) string { return filepath.Join(filepath.Dir(root), "outside") }},
			{name: "directory", linkTarget: func(root string) string { return filepath.Join(root, "directory") }},
			{name: "dangling", linkTarget: func(root string) string { return filepath.Join(root, "missing") }},
		} {
			t.Run(test.name, func(t *testing.T) {
				fixture := newMaterializerFixture(t)
				tree := filepath.Join(t.TempDir(), "tree")
				if err := os.Mkdir(tree, 0o700); err != nil {
					t.Fatal(err)
				}
				_ = writeMaterializerSource(t, tree, "regular", []byte("regular"), 0o600)
				linkTarget := test.linkTarget(tree)
				if test.name == "escaping" {
					if err := os.WriteFile(linkTarget, []byte("outside"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(filepath.Join(tree, "directory"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(linkTarget, filepath.Join(tree, "link")); err != nil {
					t.Fatal(err)
				}
				digest := strings.Repeat("0", 64)
				if test.wantOK {
					var err error
					digest, err = harnesses.PortableRuntimeTreeDigest(tree)
					if err != nil {
						t.Fatal(err)
					}
				}
				contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
				contribution.Assets = append(contribution.Assets, harnesses.PortableRuntimeAsset{
					Kind: harnesses.PortableRuntimeAssetSupport, PathKind: harnesses.PortableRuntimePathTree,
					Source: tree, Target: "lib/symlink-tree", ContentSHA256: digest,
				})
				fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
				bundle, err := Prepare(context.Background(), fixture.request)
				if test.wantOK {
					if err != nil {
						t.Fatalf("Prepare() error = %v", err)
					}
					defer bundle.Close()
					info, statErr := os.Lstat(filepath.Join(bundle.RuntimeRoot(), "lib/symlink-tree/link"))
					if statErr != nil || !info.Mode().IsRegular() {
						t.Fatalf("safe link materialization = %#v, %v", info, statErr)
					}
					return
				}
				if err == nil || bundle != nil || !errors.Is(err, ErrClosureIncomplete) {
					t.Fatalf("Prepare() = %#v, %v", bundle, err)
				}
				assertDirectoryEmpty(t, fixture.destination)
				assertNoMaterializerStages(t, fixture.destination)
			})
		}
	})

	t.Run("conflicting contributor targets", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		first := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		otherSource := writeMaterializerSource(t, t.TempDir(), "other", []byte("other executable"), 0o700)
		second := first
		second.Assets = append([]harnesses.PortableRuntimeAsset(nil), first.Assets...)
		second.Assets[0] = portableFileAsset(t, otherSource, "bin/runner", harnesses.PortableRuntimeAssetExecutable, true)
		fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
			Name: "second", Transport: harnesses.PortableRuntimeTransportSubprocess, Inclusion: harnesses.PortableRuntimeInclusionRequired,
			Instance: materializerTestHarness{contribution: second},
		})
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("Prepare() error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	t.Run("conflicting contributor projections", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		first := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		second := first
		second.StateProjections = append([]harnesses.PortableRuntimeStateProjection(nil), first.StateProjections...)
		second.StateProjections[0].Directory.Target = "tool/nested"
		fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
			Name: "second", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: second},
		})
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("Prepare() error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	for _, changedContent := range []bool{false, true} {
		name := "same-content identity replacement"
		if changedContent {
			name = "content replacement"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newMaterializerFixture(t)
			content := bytes.Repeat([]byte("portable-source-block"), 1<<19)
			if err := os.WriteFile(fixture.executable, content, 0o700); err != nil {
				t.Fatal(err)
			}
			contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
			contribution.Assets[0] = portableFileAsset(t, fixture.executable, "bin/runner", harnesses.PortableRuntimeAssetExecutable, true)
			mutationDone := make(chan struct{})
			fixture.request.Inventory[0].Instance = materializerTestHarness{
				contribution: contribution,
				beforeReturn: func() {
					go func() {
						defer close(mutationDone)
						parent := filepath.Dir(fixture.destination)
						for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
							stages, _ := filepath.Glob(filepath.Join(parent, ".fizeau-runtime-*"))
							for _, stage := range stages {
								if _, err := os.Stat(filepath.Join(stage, "bin/runner")); err == nil {
									replacement := filepath.Join(filepath.Dir(fixture.executable), "replacement")
									replacementContent := content
									if changedContent {
										replacementContent = append([]byte(nil), content...)
										replacementContent[0] ^= 0xff
									}
									if err := os.WriteFile(replacement, replacementContent, 0o700); err == nil {
										_ = os.Rename(replacement, fixture.executable)
									}
									return
								}
							}
							time.Sleep(100 * time.Microsecond)
						}
					}()
				},
			}
			bundle, err := Prepare(context.Background(), fixture.request)
			<-mutationDone
			if err == nil || bundle != nil || !errors.Is(err, ErrClosureIncomplete) {
				t.Fatalf("Prepare() = %#v, %v", bundle, err)
			}
			assertDirectoryEmpty(t, fixture.destination)
			assertNoMaterializerStages(t, fixture.destination)
		})
	}

	t.Run("concurrent no-replace commit", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		var wg sync.WaitGroup
		wg.Add(2)
		bundles := make([]*Bundle, 2)
		errs := make([]error, 2)
		for index := range bundles {
			go func(index int) {
				defer wg.Done()
				bundles[index], errs[index] = Prepare(context.Background(), fixture.request)
			}(index)
		}
		wg.Wait()
		winners := 0
		for index, bundle := range bundles {
			if bundle != nil && errs[index] == nil {
				winners++
				if err := bundle.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		if winners != 1 {
			t.Fatalf("winners = %d, errors = %v", winners, errs)
		}
		matches, err := filepath.Glob(filepath.Join(filepath.Dir(fixture.destination), ".fizeau-runtime-*"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("staging remnants = %#v, %v", matches, err)
		}
	})
}

func TestPortableRuntimeMaterializationUsesRestrictiveModes(t *testing.T) {
	fixture := newMaterializerFixture(t)
	treeRoot := filepath.Join(t.TempDir(), "tree")
	if err := os.Mkdir(treeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	first := writeMaterializerSource(t, treeRoot, "first", []byte("tree executable"), 0o755)
	second := filepath.Join(treeRoot, "second")
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(treeRoot, "link")
	if err := os.Symlink("first", link); err != nil {
		t.Fatal(err)
	}
	treeDigest, err := harnesses.PortableRuntimeTreeDigest(treeRoot)
	if err != nil {
		t.Fatal(err)
	}
	directSupport := writeMaterializerSource(t, t.TempDir(), "library", []byte("library"), 0o755)
	contribution := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
	contribution.Assets = append(contribution.Assets,
		harnesses.PortableRuntimeAsset{Kind: harnesses.PortableRuntimeAssetSupport, PathKind: harnesses.PortableRuntimePathTree, Source: treeRoot, Target: "lib/tree", ContentSHA256: treeDigest},
		portableFileAsset(t, directSupport, "lib/library.so", harnesses.PortableRuntimeAssetSupport, false),
	)
	fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
	bundle := prepareMaterializerFixture(t, fixture)
	if mode := mustMode(t, bundle.RuntimeRoot()); mode != 0o700 {
		t.Fatalf("runtime mode = %#o", mode)
	}
	if mode := mustMode(t, filepath.Join(bundle.RuntimeRoot(), "bin/runner")); mode != 0o700 {
		t.Fatalf("executable mode = %#o", mode)
	}
	if mode := mustMode(t, filepath.Join(bundle.RuntimeRoot(), "lib/library.so")); mode != 0o600 {
		t.Fatalf("undeclared executable mode = %#o", mode)
	}
	for _, target := range []string{"config/tool/settings.json", "data/tool/auth.json", manifestTarget, manifestSum, providerSecrets} {
		if mode := mustMode(t, filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(target))); mode != 0o600 {
			t.Fatalf("%s mode = %#o", target, mode)
		}
	}
	paths := []string{"first", "second", "link"}
	inodes := make(map[uint64]bool)
	for _, name := range paths {
		path := filepath.Join(bundle.RuntimeRoot(), "lib/tree", name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 {
			t.Fatalf("tree member %s = %#v, %v", name, info, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if inodes[stat.Ino] {
			t.Fatalf("tree member %s retained a hardlink", name)
		}
		inodes[stat.Ino] = true
	}
	if err := filepath.Walk(bundle.RuntimeRoot(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output symlink %s", path)
		}
		if info.IsDir() && info.Mode().Perm() != 0o700 {
			return fmt.Errorf("directory mode %s=%#o", path, info.Mode().Perm())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPortableRuntimeBundleRedactsSecretsFromDiagnostics(t *testing.T) {
	fixture := newMaterializerFixture(t)
	bundle := prepareMaterializerFixture(t, fixture)
	manifest, manifestBytes := readFixtureManifest(t, bundle)
	_ = manifest
	diagnostics := []string{
		fixture.request.String(), fixture.request.GoString(), mustJSON(t, fixture.request),
		bundle.String(), bundle.GoString(), mustJSON(t, bundle), fmt.Sprintf("%v %+v %#v", bundle, bundle, bundle),
		fmt.Sprintf("%v", bundle.Mounts()), string(manifestBytes),
	}
	for _, diagnostic := range diagnostics {
		for _, forbidden := range []string{fixture.apiKey, fixture.headerValue, fixture.environmentVal, fixture.sourceRoot, "file-secret-4b2a"} {
			if strings.Contains(diagnostic, forbidden) {
				t.Fatalf("diagnostic leaks %q: %s", forbidden, diagnostic)
			}
		}
	}

	pathBearing := fixture.request
	pathBearing.DestinationRoot = filepath.Join(t.TempDir(), "destination")
	if err := os.Mkdir(pathBearing.DestinationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	pathSecret := "/home/account-name/private-provider.json"
	pathBearing.Providers = cloneProviderSnapshot(pathBearing.Providers)
	pathBearing.Providers.Providers[0].ConfigError = "invalid config at " + pathSecret
	_, err := Prepare(context.Background(), pathBearing)
	if !errors.Is(err, ErrClosureIncomplete) || strings.Contains(fmt.Sprint(err), pathSecret) || strings.Contains(fmt.Sprint(err), "account-name") {
		t.Fatalf("path-bearing provider error = %v", err)
	}
	assertDirectoryEmpty(t, pathBearing.DestinationRoot)

	shortEscaped := fixture.request
	shortEscaped.DestinationRoot = filepath.Join(t.TempDir(), "destination")
	if err := os.Mkdir(shortEscaped.DestinationRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	shortSecret := "a<>&"
	shortEscaped.ProviderSecrets = []ProviderSecret{NewProviderSecret("fixture-provider", shortSecret, nil)}
	shortEscaped.Providers = cloneProviderSnapshot(shortEscaped.Providers)
	shortEscaped.Providers.Providers[0].ConfigError = "invalid " + shortSecret
	if _, err := Prepare(context.Background(), shortEscaped); !errors.Is(err, ErrClosureIncomplete) || strings.Contains(fmt.Sprint(err), shortSecret) {
		t.Fatalf("short escaped secret error = %v", err)
	}
	assertDirectoryEmpty(t, shortEscaped.DestinationRoot)
}

func TestPortableRuntimeManifestVerificationRejectsTampering(t *testing.T) {
	expected := Manifest{
		Version: manifestVersion, TargetGOOS: "linux", TargetGOARCH: runtime.GOARCH, GuestRoot: GuestRoot,
		ProviderSecretsFile: ManifestContentReference{Target: providerSecrets, ContentSHA256: strings.Repeat("a", 64)},
	}
	encode := func(t *testing.T, manifest Manifest) ([]byte, []byte) {
		t.Helper()
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		digest := sha256.Sum256(data)
		return data, []byte(hex.EncodeToString(digest[:]) + "\n")
	}
	valid, validSum := encode(t, expected)
	if _, err := decodeManifest(valid, validSum, expected); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	unknown := append([]byte(nil), bytes.TrimSpace(valid)...)
	unknown[len(unknown)-1] = ','
	unknown = append(unknown, []byte(`"unknown":true}`)...)
	unknown = append(unknown, '\n')
	unknownDigest := sha256.Sum256(unknown)

	wrongVersion := expected
	wrongVersion.Version++
	wrongVersionBytes, wrongVersionSum := encode(t, wrongVersion)
	wrongRoot := expected
	wrongRoot.GuestRoot = "/wrong/root"
	wrongRootBytes, wrongRootSum := encode(t, wrongRoot)
	wrongReference := expected
	wrongReference.ProviderSecretsFile.Target = ".fizeau/wrong.json"
	wrongReferenceBytes, wrongReferenceSum := encode(t, wrongReference)
	trailing := append(append([]byte(nil), valid...), []byte("{}\n")...)
	trailingDigest := sha256.Sum256(trailing)

	for name, test := range map[string]struct{ data, sum []byte }{
		"checksum":          {valid, []byte(strings.Repeat("0", 64) + "\n")},
		"unknown field":     {unknown, []byte(hex.EncodeToString(unknownDigest[:]) + "\n")},
		"trailing content":  {trailing, []byte(hex.EncodeToString(trailingDigest[:]) + "\n")},
		"wrong version":     {wrongVersionBytes, wrongVersionSum},
		"wrong guest root":  {wrongRootBytes, wrongRootSum},
		"content reference": {wrongReferenceBytes, wrongReferenceSum},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeManifest(test.data, test.sum, expected); err == nil {
				t.Fatal("tampered manifest accepted")
			}
		})
	}
}

func TestPortableRuntimeBundleCleanupRemovesCredentialCopies(t *testing.T) {
	t.Run("normal repeated concurrent close", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		bundle, err := Prepare(context.Background(), fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		credentialCopy := filepath.Join(bundle.RuntimeRoot(), "data/tool/auth.json")
		if _, err := os.Stat(credentialCopy); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for index := range errs {
			wg.Add(1)
			go func(index int) { defer wg.Done(); errs[index] = bundle.Close() }(index)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := bundle.Close(); err != nil {
			t.Fatal(err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	t.Run("cancellation and failed preparation rollback", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Prepare(ctx, fixture.request); !errors.Is(err, ErrRequestInvalid) {
			t.Fatalf("canceled error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)

		largeFixture := newMaterializerFixture(t)
		large := bytes.Repeat([]byte("cancel-during-copy"), 1<<19)
		if err := os.WriteFile(largeFixture.executable, large, 0o700); err != nil {
			t.Fatal(err)
		}
		contribution := largeFixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		contribution.Assets[0] = portableFileAsset(t, largeFixture.executable, "bin/runner", harnesses.PortableRuntimeAssetExecutable, true)
		copyContext, cancelCopy := context.WithCancel(context.Background())
		canceled := make(chan struct{})
		largeFixture.request.Inventory[0].Instance = materializerTestHarness{
			contribution: contribution,
			beforeReturn: func() {
				go func() {
					defer close(canceled)
					for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
						stages, _ := filepath.Glob(filepath.Join(filepath.Dir(largeFixture.destination), ".fizeau-runtime-*"))
						for _, stage := range stages {
							if _, err := os.Stat(filepath.Join(stage, "bin/runner")); err == nil {
								cancelCopy()
								return
							}
						}
						time.Sleep(100 * time.Microsecond)
					}
				}()
			},
		}
		if _, err := Prepare(copyContext, largeFixture.request); !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("in-copy canceled error = %v", err)
		}
		<-canceled
		assertDirectoryEmpty(t, largeFixture.destination)
		assertNoMaterializerStages(t, largeFixture.destination)

		link := filepath.Join(t.TempDir(), "credential-link")
		if err := os.Symlink(fixture.credential, link); err != nil {
			t.Fatal(err)
		}
		contribution = fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		contribution.Assets[2].Source = link
		fixture.request.Inventory[0].Instance = materializerTestHarness{contribution: contribution}
		if _, err := Prepare(context.Background(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
			t.Fatalf("failed prepare error = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	t.Run("cleanup failure remains retryable", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		bundle, err := Prepare(context.Background(), fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(fixture.destination, 0o500); err != nil {
			t.Fatal(err)
		}
		firstErr := bundle.Close()
		if err := os.Chmod(fixture.destination, 0o700); err != nil {
			t.Fatal(err)
		}
		if firstErr == nil {
			assertDirectoryEmpty(t, fixture.destination)
			return
		}
		if bundle.RuntimeRoot() == "" {
			t.Fatal("failed cleanup discarded retry ownership")
		}
		if err := bundle.Close(); err != nil {
			t.Fatalf("retry close: %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	t.Run("renamed committed child retains cleanup ownership", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		bundle, err := Prepare(context.Background(), fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		runtimeRoot := bundle.RuntimeRoot()
		renamed := runtimeRoot + "-renamed"
		if err := os.Rename(runtimeRoot, renamed); err != nil {
			t.Fatal(err)
		}
		if err := bundle.Close(); !errors.Is(err, ErrCleanupIncomplete) {
			t.Fatalf("Close() error = %v", err)
		}
		if bundle.RuntimeRoot() == "" {
			t.Fatal("missing child discarded cleanup ownership")
		}
		if _, err := os.Stat(filepath.Join(renamed, "data/tool/auth.json")); err != nil {
			t.Fatalf("renamed credential copy unexpectedly changed: %v", err)
		}
		if err := os.Rename(renamed, runtimeRoot); err != nil {
			t.Fatal(err)
		}
		if err := bundle.Close(); err != nil {
			t.Fatalf("Close() retry = %v", err)
		}
		assertDirectoryEmpty(t, fixture.destination)
	})

	t.Run("replacement committed child is untouched", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		bundle, err := Prepare(context.Background(), fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		runtimeRoot := bundle.RuntimeRoot()
		owned := runtimeRoot + "-owned"
		if err := os.Rename(runtimeRoot, owned); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(runtimeRoot, "foreign")
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := bundle.Close(); !errors.Is(err, ErrCleanupIncomplete) {
			t.Fatalf("Close() error = %v", err)
		}
		if data, err := os.ReadFile(foreign); err != nil || string(data) != "keep" {
			t.Fatalf("replacement changed: %q, %v", data, err)
		}
		if err := os.RemoveAll(runtimeRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(owned, runtimeRoot); err != nil {
			t.Fatal(err)
		}
		if err := bundle.Close(); err != nil {
			t.Fatalf("Close() retry = %v", err)
		}
	})

	t.Run("post-commit rollback failure preserves cleanup error", func(t *testing.T) {
		destinationPath := filepath.Join(t.TempDir(), "destination")
		if err := os.Mkdir(destinationPath, 0o700); err != nil {
			t.Fatal(err)
		}
		destination, err := openDestination(destinationPath)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.close()
		stage, err := destination.createStage()
		if err != nil {
			t.Fatal(err)
		}
		if err := unix.Renameat2(int(destination.parent.Fd()), stage.name, int(destination.directory.Fd()), "runtime", unix.RENAME_NOREPLACE); err != nil {
			t.Fatal(err)
		}
		stage.name = ""
		owned := filepath.Join(destinationPath, "runtime-owned")
		if err := os.Rename(filepath.Join(destinationPath, "runtime"), owned); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(destinationPath, "runtime"), 0o700); err != nil {
			t.Fatal(err)
		}
		validationErr := destination.validateCommittedStage(stage)
		if !errors.Is(validationErr, ErrCleanupIncomplete) {
			t.Fatalf("validateCommittedStage() error = %v", validationErr)
		}
		mapped := portableRuntimeCommitError(validationErr)
		if !errors.Is(mapped, ErrCleanupIncomplete) || !errors.Is(mapped, ErrRequestInvalid) {
			t.Fatalf("commit error mapping = %v", mapped)
		}
		if err := os.Remove(filepath.Join(destinationPath, "runtime")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(owned, filepath.Join(destinationPath, "runtime")); err != nil {
			t.Fatal(err)
		}
		if err := removeCommittedRuntime(destination.directory, stage.identity); err != nil {
			t.Fatal(err)
		}
		_ = stage.file.Close()
		stage.file = nil
	})

	t.Run("destination path replacement is not followed", func(t *testing.T) {
		fixture := newMaterializerFixture(t)
		bundle, err := Prepare(context.Background(), fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		moved := fixture.destination + "-moved"
		if err := os.Rename(fixture.destination, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(fixture.destination, 0o700); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(fixture.destination, "foreign")
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := bundle.Close(); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(foreign); err != nil || string(data) != "keep" {
			t.Fatalf("foreign replacement changed: %q, %v", data, err)
		}
		assertDirectoryEmpty(t, moved)
	})
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for index := range entries {
			names[index] = entries[index].Name()
		}
		sort.Strings(names)
		t.Fatalf("directory %s is not empty: %v", directory, names)
	}
}

func assertNoMaterializerStages(t *testing.T, destination string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".fizeau-runtime-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging remnants = %#v", matches)
	}
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
