package harnesses

import (
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const portableRuntimeTestDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestPortableRuntimeTypesMatchContract(t *testing.T) {
	targetType := reflect.TypeOf(PortableRuntimeTarget{})
	assertPortableRuntimeFields(t, targetType, []fieldContract{
		{name: "GOOS", typ: reflect.TypeOf("")},
		{name: "GOARCH", typ: reflect.TypeOf("")},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeAsset{}), []fieldContract{
		{name: "Kind", typ: reflect.TypeOf(PortableRuntimeAssetKind(""))},
		{name: "PathKind", typ: reflect.TypeOf(PortableRuntimePathKind(""))},
		{name: "Source", typ: reflect.TypeOf("")},
		{name: "Target", typ: reflect.TypeOf("")},
		{name: "ContentSHA256", typ: reflect.TypeOf("")},
		{name: "Executable", typ: reflect.TypeOf(false)},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeLaunch{}), []fieldContract{
		{name: "EntrypointTarget", typ: reflect.TypeOf("")},
		{name: "InterpreterTarget", typ: reflect.TypeOf("")},
		{name: "LoaderTarget", typ: reflect.TypeOf("")},
		{name: "RuntimeArgs", typ: reflect.TypeOf([]string{})},
		{name: "LibraryRootTargets", typ: reflect.TypeOf([]string{})},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeEnvironment{}), []fieldContract{
		{name: "Name", typ: reflect.TypeOf("")},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeGuestPath{}), []fieldContract{
		{name: "Scope", typ: reflect.TypeOf(PortableRuntimeGuestPathScope(""))},
		{name: "Target", typ: reflect.TypeOf("")},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeEnvironmentConstraint{}), []fieldContract{
		{name: "Name", typ: reflect.TypeOf("")},
		{name: "Kind", typ: reflect.TypeOf(PortableRuntimeEnvironmentConstraintKind(""))},
		{name: "GuestPath", typ: reflect.TypeOf(PortableRuntimeGuestPath{})},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeFixedOptionValue{}), []fieldContract{
		{name: "Option", typ: reflect.TypeOf("")},
		{name: "Value", typ: reflect.TypeOf("")},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeStateProjectionEntry{}), []fieldContract{
		{name: "AssetTarget", typ: reflect.TypeOf("")},
		{name: "Target", typ: reflect.TypeOf("")},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeStateProjection{}), []fieldContract{
		{name: "Directory", typ: reflect.TypeOf(PortableRuntimeGuestPath{})},
		{name: "Entries", typ: reflect.TypeOf([]PortableRuntimeStateProjectionEntry{})},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeExecutionConstraints{}), []fieldContract{
		{name: "Environment", typ: reflect.TypeOf([]PortableRuntimeEnvironmentConstraint{})},
		{name: "ReadOnlyPaths", typ: reflect.TypeOf([]PortableRuntimeGuestPath{})},
		{name: "RequiredAbsentPaths", typ: reflect.TypeOf([]PortableRuntimeGuestPath{})},
		{name: "FixedArguments", typ: reflect.TypeOf([]string{})},
		{name: "FixedOptionValues", typ: reflect.TypeOf([]PortableRuntimeFixedOptionValue{})},
	})
	assertPortableRuntimeFields(t, reflect.TypeOf(PortableRuntimeContribution{}), []fieldContract{
		{name: "ClosureClass", typ: reflect.TypeOf(PortableRuntimeClosureClass(""))},
		{name: "Launch", typ: reflect.TypeOf(PortableRuntimeLaunch{})},
		{name: "Assets", typ: reflect.TypeOf([]PortableRuntimeAsset{})},
		{name: "Environment", typ: reflect.TypeOf([]PortableRuntimeEnvironment{})},
		{name: "ExecutionConstraints", typ: reflect.TypeOf(PortableRuntimeExecutionConstraints{})},
		{name: "StateProjections", typ: reflect.TypeOf([]PortableRuntimeStateProjection{})},
	})

	if got, want := []PortableRuntimeAssetKind{
		PortableRuntimeAssetExecutable,
		PortableRuntimeAssetInstallTree,
		PortableRuntimeAssetConfig,
		PortableRuntimeAssetCredential,
		PortableRuntimeAssetQuota,
		PortableRuntimeAssetCache,
		PortableRuntimeAssetSupport,
	}, []PortableRuntimeAssetKind{
		"executable", "install_tree", "config", "credential", "quota", "cache", "runtime_support",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("asset kinds = %q, want %q", got, want)
	}
	if got, want := []PortableRuntimePathKind{PortableRuntimePathFile, PortableRuntimePathTree}, []PortableRuntimePathKind{"file", "tree"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path kinds = %q, want %q", got, want)
	}
	if got, want := []PortableRuntimeClosureClass{
		PortableRuntimeClosureStatic,
		PortableRuntimeClosureDynamic,
		PortableRuntimeClosureInterpreted,
	}, []PortableRuntimeClosureClass{"static", "dynamic", "interpreted"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("closure classes = %q, want %q", got, want)
	}
	if got, want := []PortableRuntimeGuestPathScope{
		PortableRuntimeGuestPathRuntime,
		PortableRuntimeGuestPathHome,
		PortableRuntimeGuestPathConfig,
		PortableRuntimeGuestPathData,
		PortableRuntimeGuestPathCache,
		PortableRuntimeGuestPathState,
		PortableRuntimeGuestPathTmp,
	}, []PortableRuntimeGuestPathScope{"runtime", "home", "config", "data", "cache", "state", "tmp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("guest path scopes = %q, want %q", got, want)
	}
	if got, want := []PortableRuntimeEnvironmentConstraintKind{
		PortableRuntimeEnvironmentFixedTrue,
		PortableRuntimeEnvironmentFixedFalse,
		PortableRuntimeEnvironmentGuestPath,
		PortableRuntimeEnvironmentUnset,
		PortableRuntimeEnvironmentRuntimePath,
	}, []PortableRuntimeEnvironmentConstraintKind{"fixed_true", "fixed_false", "guest_path", "unset", "runtime_path"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment constraint kinds = %q, want %q", got, want)
	}

	capability := reflect.TypeOf((*PortableRuntimeHarness)(nil)).Elem()
	method, ok := capability.MethodByName("PortableRuntimeAssets")
	if !ok {
		t.Fatal("PortableRuntimeHarness lacks PortableRuntimeAssets")
	}
	if got, want := method.Type.NumIn(), 2; got != want {
		t.Fatalf("PortableRuntimeAssets inputs = %d, want %d", got, want)
	}
	if got, want := method.Type.NumOut(), 2; got != want {
		t.Fatalf("PortableRuntimeAssets outputs = %d, want %d", got, want)
	}
	if method.Type.In(1) != targetType || method.Type.Out(0) != reflect.TypeOf(PortableRuntimeContribution{}) || method.Type.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("PortableRuntimeAssets signature drifted: %s", method.Type)
	}

	if runtime.GOOS != "linux" {
		if err := ValidatePortableRuntimeTarget(PortableRuntimeTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}); !errors.Is(err, ErrPortableRuntimeTargetUnsupported) {
			t.Fatalf("non-linux target error = %v, want ErrPortableRuntimeTargetUnsupported", err)
		}
		return
	}

	hostRoot := t.TempDir()
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	tests := []struct {
		name         string
		contribution PortableRuntimeContribution
	}{
		{
			name: "static",
			contribution: PortableRuntimeContribution{
				ClosureClass: PortableRuntimeClosureStatic,
				Launch:       PortableRuntimeLaunch{EntrypointTarget: "bin/static-tool"},
				Assets: []PortableRuntimeAsset{
					portableRuntimeTestAsset(hostRoot, "static-tool", "bin/static-tool", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
				},
				Environment: []PortableRuntimeEnvironment{{Name: "ZZ_TOKEN"}, {Name: "API_KEY"}},
			},
		},
		{
			name: "dynamic",
			contribution: PortableRuntimeContribution{
				ClosureClass: PortableRuntimeClosureDynamic,
				Launch: PortableRuntimeLaunch{
					EntrypointTarget:   "bin/dynamic-tool",
					LoaderTarget:       "lib/ld-linux.so",
					LibraryRootTargets: []string{"lib/dynamic"},
				},
				Assets: []PortableRuntimeAsset{
					portableRuntimeTestAsset(hostRoot, "dynamic-libs", "lib/dynamic", PortableRuntimeAssetInstallTree, PortableRuntimePathTree, false),
					portableRuntimeTestAsset(hostRoot, "dynamic-tool", "bin/dynamic-tool", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
					portableRuntimeTestAsset(hostRoot, "dynamic-loader", "lib/ld-linux.so", PortableRuntimeAssetSupport, PortableRuntimePathFile, true),
				},
			},
		},
		{
			name: "interpreted with dynamic interpreter",
			contribution: PortableRuntimeContribution{
				ClosureClass: PortableRuntimeClosureInterpreted,
				Launch: PortableRuntimeLaunch{
					EntrypointTarget:   "scripts/tool.js",
					InterpreterTarget:  "bin/node",
					LoaderTarget:       "lib/ld-node.so",
					RuntimeArgs:        []string{"--no-warnings"},
					LibraryRootTargets: []string{"lib/node"},
				},
				Assets: []PortableRuntimeAsset{
					portableRuntimeTestAsset(hostRoot, "tool.js", "scripts/tool.js", PortableRuntimeAssetExecutable, PortableRuntimePathFile, false),
					portableRuntimeTestAsset(hostRoot, "node", "bin/node", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
					portableRuntimeTestAsset(hostRoot, "node-loader", "lib/ld-node.so", PortableRuntimeAssetSupport, PortableRuntimePathFile, true),
					portableRuntimeTestAsset(hostRoot, "node-libs", "lib/node", PortableRuntimeAssetInstallTree, PortableRuntimePathTree, false),
					portableRuntimeTestAsset(hostRoot, "package-tree", "share/tool", PortableRuntimeAssetInstallTree, PortableRuntimePathTree, false),
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := NormalizePortableRuntimeContribution(target, tc.contribution)
			if err != nil {
				t.Fatalf("NormalizePortableRuntimeContribution() error = %v", err)
			}
			for i := 1; i < len(normalized.Assets); i++ {
				if normalized.Assets[i-1].Target >= normalized.Assets[i].Target {
					t.Fatalf("assets are not canonically ordered: %#v", normalized.Assets)
				}
			}
			for i := 1; i < len(normalized.Environment); i++ {
				if normalized.Environment[i-1].Name >= normalized.Environment[i].Name {
					t.Fatalf("environment is not canonically ordered: %#v", normalized.Environment)
				}
			}
		})
	}

}

func TestPortableRuntimeExecutionConstraints(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 validation is Linux-only")
	}

	hostRoot := t.TempDir()
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	contribution := validPortableRuntimeExecutionContribution(hostRoot)
	contribution.Assets = append(contribution.Assets,
		portableRuntimeTestAsset(hostRoot, "other-config", "config/other", PortableRuntimeAssetConfig, PortableRuntimePathTree, false))
	contribution.ExecutionConstraints.Environment = []PortableRuntimeEnvironmentConstraint{
		{Name: "PATH", Kind: PortableRuntimeEnvironmentRuntimePath},
		{Name: "XDG_CONFIG_HOME", Kind: PortableRuntimeEnvironmentGuestPath, GuestPath: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: "config"}},
		{Name: "FEATURE_ENABLED", Kind: PortableRuntimeEnvironmentFixedTrue},
		{Name: "FEATURE_DISABLED", Kind: PortableRuntimeEnvironmentFixedFalse},
		{Name: "USER", Kind: PortableRuntimeEnvironmentUnset},
	}
	contribution.ExecutionConstraints.ReadOnlyPaths = []PortableRuntimeGuestPath{
		{Scope: PortableRuntimeGuestPathRuntime, Target: "config/other"},
		{Scope: PortableRuntimeGuestPathRuntime, Target: "config/opencode"},
	}
	contribution.ExecutionConstraints.RequiredAbsentPaths = []PortableRuntimeGuestPath{
		{Scope: PortableRuntimeGuestPathHome, Target: ".config/opencode.json"},
		{Scope: PortableRuntimeGuestPathConfig, Target: "legacy/opencode.json"},
		{Scope: PortableRuntimeGuestPathCache, Target: "installers/opencode"},
	}
	contribution.ExecutionConstraints.FixedArguments = []string{"--pure", "--disable-auto-update"}

	normalized, err := NormalizePortableRuntimeContribution(target, contribution)
	if err != nil {
		t.Fatalf("NormalizePortableRuntimeContribution() error = %v", err)
	}
	if got, want := normalized.ExecutionConstraints.Environment, []PortableRuntimeEnvironmentConstraint{
		{Name: "FEATURE_DISABLED", Kind: PortableRuntimeEnvironmentFixedFalse},
		{Name: "FEATURE_ENABLED", Kind: PortableRuntimeEnvironmentFixedTrue},
		{Name: "PATH", Kind: PortableRuntimeEnvironmentRuntimePath},
		{Name: "USER", Kind: PortableRuntimeEnvironmentUnset},
		{Name: "XDG_CONFIG_HOME", Kind: PortableRuntimeEnvironmentGuestPath, GuestPath: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: "config"}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment constraints = %#v, want %#v", got, want)
	}
	if got, want := normalized.ExecutionConstraints.ReadOnlyPaths, []PortableRuntimeGuestPath{
		{Scope: PortableRuntimeGuestPathRuntime, Target: "config/opencode"},
		{Scope: PortableRuntimeGuestPathRuntime, Target: "config/other"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("read-only paths = %#v, want %#v", got, want)
	}
	if got, want := normalized.ExecutionConstraints.RequiredAbsentPaths, []PortableRuntimeGuestPath{
		{Scope: PortableRuntimeGuestPathCache, Target: "installers/opencode"},
		{Scope: PortableRuntimeGuestPathConfig, Target: "legacy/opencode.json"},
		{Scope: PortableRuntimeGuestPathHome, Target: ".config/opencode.json"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("required-absent paths = %#v, want %#v", got, want)
	}
	if got, want := normalized.ExecutionConstraints.FixedArguments, []string{"--pure", "--disable-auto-update"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed arguments = %#v, want ordered %#v", got, want)
	}

	contribution.ExecutionConstraints.Environment[0].Name = "MUTATED"
	contribution.ExecutionConstraints.ReadOnlyPaths[0].Target = "mutated"
	contribution.ExecutionConstraints.RequiredAbsentPaths[0].Target = "mutated"
	contribution.ExecutionConstraints.FixedArguments[0] = "mutated"
	if normalized.ExecutionConstraints.Environment[2].Name != "PATH" ||
		normalized.ExecutionConstraints.ReadOnlyPaths[1].Target != "config/other" ||
		normalized.ExecutionConstraints.RequiredAbsentPaths[2].Target != ".config/opencode.json" ||
		normalized.ExecutionConstraints.FixedArguments[0] != "--pure" {
		t.Fatal("normalized execution constraints alias contributor-owned slices")
	}

	const sensitive = "FIZEAU_PRIVATE_SENTINEL"
	tests := []struct {
		name   string
		mutate func(*PortableRuntimeContribution)
	}{
		{
			name: "inherited generated baseline name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Environment = append(candidate.Environment, PortableRuntimeEnvironment{Name: "HOME"})
			},
		},
		{
			name: "inherited generated terminal name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Environment = append(candidate.Environment, PortableRuntimeEnvironment{Name: "TERM"})
			},
		},
		{
			name: "inherited generated language name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Environment = append(candidate.Environment, PortableRuntimeEnvironment{Name: "LANG"})
			},
		},
		{
			name: "inherited generated locale name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Environment = append(candidate.Environment, PortableRuntimeEnvironment{Name: "LC_ALL"})
			},
		},
		{
			name: "environment raw assignment",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[0].Name = "FEATURE_ENABLED=" + sensitive
			},
		},
		{
			name: "environment duplicate name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment = append(candidate.ExecutionConstraints.Environment, candidate.ExecutionConstraints.Environment[0])
			},
		},
		{
			name: "environment conflicts with inherited name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[0].Name = "API_KEY"
			},
		},
		{
			name: "unknown environment treatment",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[0].Kind = "raw_value"
			},
		},
		{
			name: "fixed treatment with guest path",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[0].GuestPath = PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathHome, Target: sensitive}
			},
		},
		{
			name: "runtime path for non-PATH name",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[0].Kind = PortableRuntimeEnvironmentRuntimePath
			},
		},
		{
			name: "PATH with non-runtime-path treatment",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[0] = PortableRuntimeEnvironmentConstraint{Name: "PATH", Kind: PortableRuntimeEnvironmentFixedTrue}
			},
		},
		{
			name: "invalid guest path scope",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[1].GuestPath.Scope = "work_dir"
			},
		},
		{
			name: "absolute environment guest path",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[1].GuestPath.Target = "/" + sensitive
			},
		},
		{
			name: "unbacked runtime environment guest path",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[1].GuestPath.Target = sensitive
			},
		},
		{
			name: "runtime environment path is only a file ancestor",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[1] = PortableRuntimeEnvironmentConstraint{
					Name:      "TOOL_BIN_DIR",
					Kind:      PortableRuntimeEnvironmentGuestPath,
					GuestPath: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: "bin"},
				}
			},
		},
		{
			name: "read-only path outside runtime",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.ReadOnlyPaths[0].Scope = PortableRuntimeGuestPathConfig
			},
		},
		{
			name: "read-only path lacks exact asset",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.ReadOnlyPaths[0].Target = sensitive
			},
		},
		{
			name: "read-only config is a file",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Assets[1].PathKind = PortableRuntimePathFile
			},
		},
		{
			name: "read-only tree is writable state kind",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Assets[1].Kind = PortableRuntimeAssetCache
			},
		},
		{
			name: "duplicate read-only path",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.ReadOnlyPaths = append(candidate.ExecutionConstraints.ReadOnlyPaths, candidate.ExecutionConstraints.ReadOnlyPaths[0])
			},
		},
		{
			name: "required absent path is absolute",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.RequiredAbsentPaths[0].Target = "/" + sensitive
			},
		},
		{
			name: "required absent paths overlap",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.RequiredAbsentPaths = append(candidate.ExecutionConstraints.RequiredAbsentPaths,
					PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathHome, Target: ".config/opencode/plugins"})
			},
		},
		{
			name: "required absent path overlaps asset",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.RequiredAbsentPaths[0] = PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: "config"}
			},
		},
		{
			name: "required absent path overlaps read-only path",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.RequiredAbsentPaths[0] = PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: "config/opencode/plugins"}
			},
		},
		{
			name: "required absent path overlaps generated root",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.Environment[1] = PortableRuntimeEnvironmentConstraint{
					Name:      "OTHER_CONFIG_DIR",
					Kind:      PortableRuntimeEnvironmentGuestPath,
					GuestPath: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathCache, Target: "generated"},
				}
				candidate.ExecutionConstraints.RequiredAbsentPaths[0] = PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathCache, Target: "generated/plugins"}
			},
		},
		{
			name: "empty fixed argument",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = ""
			},
		},
		{
			name: "duplicate fixed argument",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments = append(candidate.ExecutionConstraints.FixedArguments, candidate.ExecutionConstraints.FixedArguments[0])
			},
		},
		{
			name: "fixed argument raw assignment",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = "TOKEN=" + sensitive
			},
		},
		{
			name: "fixed argument positional value",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = sensitive
			},
		},
		{
			name: "fixed argument starts with digit",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = "--1"
			},
		},
		{
			name: "fixed argument absolute host source",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = "/" + sensitive
			},
		},
		{
			name: "fixed argument flag value",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = "--config=/" + sensitive
			},
		},
		{
			name: "fixed argument route selector",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedArguments[0] = "--provider=" + sensitive
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := clonePortableRuntimeTestContribution(validPortableRuntimeExecutionContribution(hostRoot))
			tc.mutate(&candidate)
			_, err := NormalizePortableRuntimeContribution(target, candidate)
			if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
			}
			if strings.Contains(err.Error(), sensitive) || strings.Contains(err.Error(), hostRoot) {
				t.Fatalf("error reveals sensitive record data: %v", err)
			}
		})
	}
}

func TestPortableRuntimeMixedStateProjection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 validation is Linux-only")
	}

	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	contribution := validPortableRuntimeMixedStateContribution(filepath.Join(t.TempDir(), "fixture-secret"))
	normalized, err := NormalizePortableRuntimeContribution(target, contribution)
	if err != nil {
		t.Fatalf("NormalizePortableRuntimeContribution() error = %v", err)
	}
	want := []PortableRuntimeStateProjection{
		{
			Directory: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathHome, Target: ".gemini"},
			Entries: []PortableRuntimeStateProjectionEntry{
				{AssetTarget: "state/gemini/quota.json", Target: "oauth_creds.json"},
				{AssetTarget: "config/gemini/settings.json", Target: "settings.json"},
			},
		},
		{
			Directory: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathState, Target: "pi"},
			Entries: []PortableRuntimeStateProjectionEntry{
				{AssetTarget: "state/pi/auth.json", Target: "auth.json"},
				{AssetTarget: "config/pi/settings.json", Target: "settings.json"},
			},
		},
	}
	if !reflect.DeepEqual(normalized.StateProjections, want) {
		t.Fatalf("state projections = %#v, want sorted %#v", normalized.StateProjections, want)
	}
	if got, wantAbsent := normalized.ExecutionConstraints.RequiredAbsentPaths, []PortableRuntimeGuestPath{{Scope: PortableRuntimeGuestPathHome, Target: ".gemini/extensions"}}; !reflect.DeepEqual(got, wantAbsent) {
		t.Fatalf("required-absent sibling = %#v, want allowed %#v", got, wantAbsent)
	}

	contribution.StateProjections[0].Directory.Target = "mutated-input"
	contribution.StateProjections[0].Entries[0] = PortableRuntimeStateProjectionEntry{AssetTarget: "mutated-input", Target: "mutated-input"}
	if !reflect.DeepEqual(normalized.StateProjections, want) {
		t.Fatal("normalized state projections alias contributor-owned outer or entry slices")
	}

	ownedInput := validPortableRuntimeMixedStateContribution(filepath.Join(t.TempDir(), "fixture-secret"))
	ownedSnapshot := clonePortableRuntimeTestContribution(ownedInput)
	ownedNormalized, err := NormalizePortableRuntimeContribution(target, ownedInput)
	if err != nil {
		t.Fatalf("second NormalizePortableRuntimeContribution() error = %v", err)
	}
	ownedNormalized.StateProjections[0].Directory.Target = "mutated-output"
	ownedNormalized.StateProjections[0].Entries[0] = PortableRuntimeStateProjectionEntry{AssetTarget: "mutated-output", Target: "mutated-output"}
	if !reflect.DeepEqual(ownedInput.StateProjections, ownedSnapshot.StateProjections) {
		t.Fatal("mutating normalized state projections changed the contributor-owned input")
	}
}

func TestPortableRuntimeStateProjectionValidation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 validation is Linux-only")
	}

	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	hostRoot := filepath.Join(t.TempDir(), "fixture-secret")
	tests := []struct {
		name      string
		wantClass string
		mutate    func(*PortableRuntimeContribution)
	}{
		{
			name: "empty directory", wantClass: "invalid activation-owned directory",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Directory.Target = ""
			},
		},
		{
			name: "runtime directory", wantClass: "invalid activation-owned directory",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Directory.Scope = PortableRuntimeGuestPathRuntime
			},
		},
		{
			name: "tmp directory", wantClass: "invalid activation-owned directory",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Directory.Scope = PortableRuntimeGuestPathTmp
			},
		},
		{
			name: "absolute entry target", wantClass: "invalid target",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries[0].Target = "/settings.json"
			},
		},
		{
			name: "traversing entry target", wantClass: "invalid target",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries[0].Target = "nested/../settings.json"
			},
		},
		{
			name: "backslash entry target", wantClass: "invalid target",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries[0].Target = `nested\settings.json`
			},
		},
		{
			name: "empty entry target", wantClass: "invalid target",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries[0].Target = ""
			},
		},
		{
			name: "missing asset reference", wantClass: "missing asset",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries[0].AssetTarget = "state/missing.json"
			},
		},
		{
			name: "unsupported asset kind", wantClass: "unsupported asset kind",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Assets = append(candidate.Assets, portableRuntimeTestAsset(hostRoot, "support", "support/metadata", PortableRuntimeAssetSupport, PortableRuntimePathFile, false))
				candidate.StateProjections[0].Entries[0].AssetTarget = "support/metadata"
			},
		},
		{
			name: "executable asset kind", wantClass: "executable asset",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries[0].AssetTarget = "bin/tool"
			},
		},
		{
			name: "no config member", wantClass: "no immutable config member",
			mutate: func(candidate *PortableRuntimeContribution) {
				for i := range candidate.Assets {
					if candidate.Assets[i].Target == "config/pi/settings.json" {
						candidate.Assets[i].Kind = PortableRuntimeAssetCache
					}
				}
			},
		},
		{
			name: "no credential quota or cache seed", wantClass: "no credential, quota, or cache seed",
			mutate: func(candidate *PortableRuntimeContribution) {
				for i := range candidate.Assets {
					if candidate.Assets[i].Target == "state/pi/auth.json" {
						candidate.Assets[i].Kind = PortableRuntimeAssetConfig
					}
				}
			},
		},
		{
			name: "duplicate outputs", wantClass: "outputs overlap",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Assets = append(candidate.Assets, portableRuntimeTestAsset(hostRoot, "extra-cache", "cache/extra.json", PortableRuntimeAssetCache, PortableRuntimePathFile, false))
				candidate.StateProjections[0].Entries = append(candidate.StateProjections[0].Entries, PortableRuntimeStateProjectionEntry{AssetTarget: "cache/extra.json", Target: candidate.StateProjections[0].Entries[0].Target})
			},
		},
		{
			name: "ancestor descendant outputs", wantClass: "outputs overlap",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.Assets = append(candidate.Assets, portableRuntimeTestAsset(hostRoot, "extra-cache", "cache/extra.json", PortableRuntimeAssetCache, PortableRuntimePathFile, false))
				candidate.StateProjections[0].Entries = append(candidate.StateProjections[0].Entries, PortableRuntimeStateProjectionEntry{AssetTarget: "cache/extra.json", Target: candidate.StateProjections[0].Entries[0].Target + "/nested"})
			},
		},
		{
			name: "duplicate directories", wantClass: "directories overlap",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections = append(candidate.StateProjections, candidate.StateProjections[0])
			},
		},
		{
			name: "ancestor descendant directories", wantClass: "directories overlap",
			mutate: func(candidate *PortableRuntimeContribution) {
				nested := candidate.StateProjections[0]
				nested.Directory.Target += "/nested"
				candidate.StateProjections = append(candidate.StateProjections, nested)
			},
		},
		{
			name: "required absent output overlap", wantClass: "overlaps required-absent path",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.RequiredAbsentPaths = append(candidate.ExecutionConstraints.RequiredAbsentPaths, PortableRuntimeGuestPath{Scope: candidate.StateProjections[0].Directory.Scope, Target: candidate.StateProjections[0].Directory.Target + "/settings.json"})
			},
		},
		{
			name: "reused asset reference", wantClass: "asset is reused",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.StateProjections[0].Entries = append(candidate.StateProjections[0].Entries, PortableRuntimeStateProjectionEntry{AssetTarget: candidate.StateProjections[0].Entries[0].AssetTarget, Target: "second.json"})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := clonePortableRuntimeTestContribution(validPortableRuntimeMixedStateContribution(hostRoot))
			tc.mutate(&candidate)
			_, err := NormalizePortableRuntimeContribution(target, candidate)
			if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
			}
			if !strings.Contains(err.Error(), tc.wantClass) {
				t.Fatalf("error = %v, want redacted class %q", err, tc.wantClass)
			}
			if strings.Contains(err.Error(), hostRoot) || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("error reveals source path or fixture secret: %v", err)
			}
		})
	}
}

func TestPortableRuntimeFixedOptionValues(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 validation is Linux-only")
	}

	hostRoot := t.TempDir()
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	contribution := validPortableRuntimeExecutionContribution(hostRoot)
	contribution.ExecutionConstraints.FixedArguments = []string{"--pure", "--disable-auto-update"}
	contribution.ExecutionConstraints.FixedOptionValues = []PortableRuntimeFixedOptionValue{
		{Option: "-e", Value: "none"},
		{Option: "--color", Value: "disabled"},
	}

	normalized, err := NormalizePortableRuntimeContribution(target, contribution)
	if err != nil {
		t.Fatalf("NormalizePortableRuntimeContribution() error = %v", err)
	}
	if got, want := normalized.ExecutionConstraints.FixedArguments, []string{"--pure", "--disable-auto-update"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed arguments = %#v, want ordered %#v", got, want)
	}
	if got, want := normalized.ExecutionConstraints.FixedOptionValues, []PortableRuntimeFixedOptionValue{
		{Option: "-e", Value: "none"},
		{Option: "--color", Value: "disabled"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed option/values = %#v, want ordered %#v", got, want)
	}

	contribution.ExecutionConstraints.FixedArguments[0] = "mutated"
	contribution.ExecutionConstraints.FixedOptionValues[0] = PortableRuntimeFixedOptionValue{Option: "--mutated", Value: "mutated"}
	if normalized.ExecutionConstraints.FixedArguments[0] != "--pure" ||
		normalized.ExecutionConstraints.FixedOptionValues[0] != (PortableRuntimeFixedOptionValue{Option: "-e", Value: "none"}) {
		t.Fatal("normalized fixed launch prefix aliases contributor-owned slices")
	}

	tests := []struct {
		name     string
		rejected string
		mutate   func(*PortableRuntimeContribution)
	}{
		{
			name: "empty option", rejected: "",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Option = ""
			},
		},
		{
			name: "empty value", rejected: "",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = ""
			},
		},
		{
			name: "unsupported option spelling", rejected: "-Extensions",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Option = "-Extensions"
			},
		},
		{
			name: "unsupported short option", rejected: "-x",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Option = "-x"
			},
		},
		{
			name: "option assignment", rejected: "--extensions=none",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Option = "--extensions=none"
			},
		},
		{
			name: "value assignment", rejected: "mode=none",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = "mode=none"
			},
		},
		{
			name: "absolute path value", rejected: "/private/runtime-config",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = "/private/runtime-config"
			},
		},
		{
			name: "relative path value", rejected: "private/runtime-config",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = "private/runtime-config"
			},
		},
		{
			name: "secret-like value", rejected: "private-token",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = "private-token"
			},
		},
		{
			name: "trailing hyphen value", rejected: "none-",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = "none-"
			},
		},
		{
			name: "repeated hyphen value", rejected: "none--x",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0].Value = "none--x"
			},
		},
		{
			name: "short model selector", rejected: "-m",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0] = PortableRuntimeFixedOptionValue{Option: "-m", Value: "sonnet"}
			},
		},
		{
			name: "long route selector", rejected: "--route",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0] = PortableRuntimeFixedOptionValue{Option: "--route", Value: "default"}
			},
		},
		{
			name: "long endpoint selector", rejected: "--endpoint",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0] = PortableRuntimeFixedOptionValue{Option: "--endpoint", Value: "remote"}
			},
		},
		{
			name: "duplicate option and value", rejected: "-e",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues = append(candidate.ExecutionConstraints.FixedOptionValues,
					PortableRuntimeFixedOptionValue{Option: "-e", Value: "none"})
			},
		},
		{
			name: "conflicting option values", rejected: "-e",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues = append(candidate.ExecutionConstraints.FixedOptionValues,
					PortableRuntimeFixedOptionValue{Option: "-e", Value: "disabled"})
			},
		},
		{
			name: "short and long spelling conflict", rejected: "--e",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues = append(candidate.ExecutionConstraints.FixedOptionValues,
					PortableRuntimeFixedOptionValue{Option: "--e", Value: "disabled"})
			},
		},
		{
			name: "option conflicts with fixed flag", rejected: "--pure",
			mutate: func(candidate *PortableRuntimeContribution) {
				candidate.ExecutionConstraints.FixedOptionValues[0] = PortableRuntimeFixedOptionValue{Option: "--pure", Value: "disabled"}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := clonePortableRuntimeTestContribution(validPortableRuntimeExecutionContribution(hostRoot))
			candidate.ExecutionConstraints.FixedOptionValues = []PortableRuntimeFixedOptionValue{{Option: "-e", Value: "none"}}
			tc.mutate(&candidate)
			_, err := NormalizePortableRuntimeContribution(target, candidate)
			if !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("error = %v, want ErrPortableRuntimeClosureIncomplete", err)
			}
			if tc.rejected != "" && strings.Contains(err.Error(), tc.rejected) {
				t.Fatalf("error reveals rejected option/value: %v", err)
			}
		})
	}
}

func TestPortableRuntimeExactLibraryRootValidation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}
	hostRoot := t.TempDir()
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	contribution := PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureDynamic,
		Launch: PortableRuntimeLaunch{
			EntrypointTarget:   "bin/tool",
			LoaderTarget:       "loader/ld.so",
			LibraryRootTargets: []string{"lib/exact"},
		},
		Assets: []PortableRuntimeAsset{
			portableRuntimeTestAsset(hostRoot, "tool", "bin/tool", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
			portableRuntimeTestAsset(hostRoot, "loader", "loader/ld.so", PortableRuntimeAssetSupport, PortableRuntimePathFile, true),
			portableRuntimeTestAsset(hostRoot, "libc", "lib/exact/libc.so", PortableRuntimeAssetSupport, PortableRuntimePathFile, false),
		},
	}
	if _, err := NormalizePortableRuntimeContribution(target, contribution); err != nil {
		t.Fatalf("exact support-file root rejected: %v", err)
	}

	for _, mutate := range []func(*PortableRuntimeAsset){
		func(asset *PortableRuntimeAsset) { asset.Kind = PortableRuntimeAssetCredential },
		func(asset *PortableRuntimeAsset) { asset.Executable = true },
	} {
		malformed := contribution
		malformed.Assets = append([]PortableRuntimeAsset(nil), contribution.Assets...)
		mutate(&malformed.Assets[2])
		if _, err := NormalizePortableRuntimeContribution(target, malformed); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("malformed exact library root error = %v, want closure incomplete", err)
		}
	}
	mixed := contribution
	mixed.Assets = append(append([]PortableRuntimeAsset(nil), contribution.Assets...),
		portableRuntimeTestAsset(hostRoot, "credential", "lib/exact/credential.json", PortableRuntimeAssetCredential, PortableRuntimePathFile, false))
	if _, err := NormalizePortableRuntimeContribution(target, mixed); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("mixed-state exact library root error = %v, want closure incomplete", err)
	}
	nested := contribution
	nested.Assets = append([]PortableRuntimeAsset(nil), contribution.Assets...)
	nested.Assets[2].Target = "lib/exact/nested/libc.so"
	if _, err := NormalizePortableRuntimeContribution(target, nested); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("nested exact library root error = %v, want closure incomplete", err)
	}
	nestedTree := contribution
	nestedTree.Assets = append(append([]PortableRuntimeAsset(nil), contribution.Assets...),
		portableRuntimeTestAsset(hostRoot, "nested-tree", "lib/exact/nested", PortableRuntimeAssetSupport, PortableRuntimePathTree, false))
	if _, err := NormalizePortableRuntimeContribution(target, nestedTree); !errors.Is(err, ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("nested-tree exact library root error = %v, want closure incomplete", err)
	}
}

func TestPortableRuntimeValidationRejectsInvalidRecords(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 validation is Linux-only")
	}

	hostRoot := t.TempDir()
	target := PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}
	valid := PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureStatic,
		Launch:       PortableRuntimeLaunch{EntrypointTarget: "bin/tool"},
		Assets: []PortableRuntimeAsset{
			portableRuntimeTestAsset(hostRoot, "tool", "bin/tool", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
		},
		Environment: []PortableRuntimeEnvironment{{Name: "API_KEY"}},
	}

	tests := []struct {
		name         string
		target       PortableRuntimeTarget
		mutate       func(*PortableRuntimeContribution)
		wantSentinel error
	}{
		{
			name:         "target GOOS",
			target:       PortableRuntimeTarget{GOOS: "darwin", GOARCH: runtime.GOARCH},
			wantSentinel: ErrPortableRuntimeTargetUnsupported,
		},
		{
			name:         "target GOARCH",
			target:       PortableRuntimeTarget{GOOS: "linux", GOARCH: "not-" + runtime.GOARCH},
			wantSentinel: ErrPortableRuntimeTargetUnsupported,
		},
		{
			name: "unknown closure class",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = "unknown"
			},
		},
		{
			name: "empty assets",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets = nil
			},
		},
		{
			name: "unknown asset kind",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Kind = "unknown"
			},
		},
		{
			name: "unknown path kind",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].PathKind = "unknown"
			},
		},
		{
			name: "relative source",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Source = "relative/tool"
			},
		},
		{
			name: "unclean source",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Source = filepath.Join(hostRoot, "nested", "..", "tool") + "/../tool"
			},
		},
		{
			name: "absolute guest target",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Target = "/bin/tool"
			},
		},
		{
			name: "traversing guest target",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Target = "bin/../tool"
			},
		},
		{
			name: "backslash guest target",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Target = `bin\tool`
			},
		},
		{
			name: "uppercase digest",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].ContentSHA256 = strings.ToUpper(portableRuntimeTestDigest)
			},
		},
		{
			name: "short digest",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].ContentSHA256 = "abc"
			},
		},
		{
			name: "executable tree",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].PathKind = PortableRuntimePathTree
			},
		},
		{
			name: "credential tree",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Kind = PortableRuntimeAssetCredential
				contribution.Assets[0].PathKind = PortableRuntimePathTree
				contribution.Assets[0].Executable = false
			},
		},
		{
			name: "executable credential",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Kind = PortableRuntimeAssetCredential
			},
		},
		{
			name: "executable config",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Kind = PortableRuntimeAssetConfig
			},
		},
		{
			name: "duplicate asset target",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets = append(contribution.Assets, contribution.Assets[0])
			},
		},
		{
			name: "tree overlaps asset target",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets = append(contribution.Assets,
					portableRuntimeTestAsset(hostRoot, "tree", "bin", PortableRuntimeAssetInstallTree, PortableRuntimePathTree, false))
			},
		},
		{
			name: "file is ancestor of asset target",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets = append(contribution.Assets,
					portableRuntimeTestAsset(hostRoot, "bin-file", "bin", PortableRuntimeAssetSupport, PortableRuntimePathFile, false))
			},
		},
		{
			name: "environment assignment",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Environment[0].Name = "API_KEY=secret-value"
			},
		},
		{
			name: "non-ASCII environment name",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Environment[0].Name = "TÖKEN"
			},
		},
		{
			name: "duplicate environment name",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Environment = append(contribution.Environment, contribution.Environment[0])
			},
		},
		{
			name: "entrypoint not declared",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Launch.EntrypointTarget = "bin/missing"
			},
		},
		{
			name: "static entrypoint not executable",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Assets[0].Executable = false
			},
		},
		{
			name: "static closure with loader",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.Launch.LoaderTarget = "bin/tool"
			},
		},
		{
			name: "dynamic closure without loader",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureDynamic
			},
		},
		{
			name: "dynamic closure with interpreter",
			mutate: func(contribution *PortableRuntimeContribution) {
				makeValidDynamicContribution(hostRoot, contribution)
				contribution.Launch.InterpreterTarget = "bin/tool"
			},
		},
		{
			name: "dynamic loader is not executable",
			mutate: func(contribution *PortableRuntimeContribution) {
				makeValidDynamicContribution(hostRoot, contribution)
				contribution.Assets[1].Executable = false
			},
		},
		{
			name: "dynamic library root is not a tree",
			mutate: func(contribution *PortableRuntimeContribution) {
				makeValidDynamicContribution(hostRoot, contribution)
				contribution.Launch.LibraryRootTargets = []string{"bin/tool"}
			},
		},
		{
			name: "duplicate library root",
			mutate: func(contribution *PortableRuntimeContribution) {
				makeValidDynamicContribution(hostRoot, contribution)
				contribution.Launch.LibraryRootTargets = []string{"lib/runtime", "lib/runtime"}
			},
		},
		{
			name: "colon-delimited library root",
			mutate: func(contribution *PortableRuntimeContribution) {
				makeValidDynamicContribution(hostRoot, contribution)
				contribution.Launch.LibraryRootTargets = []string{"lib:runtime"}
				contribution.Assets[2].Target = "lib:runtime"
			},
		},
		{
			name: "interpreted closure without interpreter",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
			},
		},
		{
			name: "interpreted closure with non-executable interpreter",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
				contribution.Launch.InterpreterTarget = "bin/tool"
				contribution.Assets[0].Executable = false
			},
		},
		{
			name: "static interpreter with library roots",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
				contribution.Launch.InterpreterTarget = "bin/tool"
				contribution.Launch.LibraryRootTargets = []string{"lib/runtime"}
				contribution.Assets = append(contribution.Assets,
					portableRuntimeTestAsset(hostRoot, "runtime", "lib/runtime", PortableRuntimeAssetInstallTree, PortableRuntimePathTree, false))
			},
		},
		{
			name: "dynamic interpreter without library roots",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
				contribution.Launch.InterpreterTarget = "bin/tool"
				contribution.Launch.LoaderTarget = "bin/tool"
			},
		},
		{
			name: "runtime environment assignment argument",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
				contribution.Launch.InterpreterTarget = "bin/tool"
				contribution.Launch.RuntimeArgs = []string{"TOKEN=secret-value"}
			},
		},
		{
			name: "runtime placeholder argument",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
				contribution.Launch.InterpreterTarget = "bin/tool"
				contribution.Launch.RuntimeArgs = []string{"${TOKEN}"}
			},
		},
		{
			name: "runtime route selector argument",
			mutate: func(contribution *PortableRuntimeContribution) {
				contribution.ClosureClass = PortableRuntimeClosureInterpreted
				contribution.Launch.InterpreterTarget = "bin/tool"
				contribution.Launch.RuntimeArgs = []string{"--provider=example"}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := clonePortableRuntimeTestContribution(valid)
			candidateTarget := target
			if tc.target != (PortableRuntimeTarget{}) {
				candidateTarget = tc.target
			}
			if tc.mutate != nil {
				tc.mutate(&candidate)
			}
			_, err := NormalizePortableRuntimeContribution(candidateTarget, candidate)
			wantSentinel := tc.wantSentinel
			if wantSentinel == nil {
				wantSentinel = ErrPortableRuntimeClosureIncomplete
			}
			if !errors.Is(err, wantSentinel) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, wantSentinel)
			}
			if err != nil && (strings.Contains(err.Error(), hostRoot) || strings.Contains(err.Error(), "secret-value")) {
				t.Fatalf("error reveals sensitive record data: %v", err)
			}
		})
	}

	t.Run("structural inventory", func(t *testing.T) {
		assertPortableRuntimeInventoryRejectsUnclassifiedRecords(t)
	})
}

type fieldContract struct {
	name string
	typ  reflect.Type
}

func assertPortableRuntimeFields(t *testing.T, typ reflect.Type, want []fieldContract) {
	t.Helper()
	if got := typ.NumField(); got != len(want) {
		t.Fatalf("%s has %d fields, want %d", typ, got, len(want))
	}
	for i, contract := range want {
		field := typ.Field(i)
		if field.Name != contract.name || field.Type != contract.typ {
			t.Fatalf("%s field %d = %s %s, want %s %s", typ, i, field.Name, field.Type, contract.name, contract.typ)
		}
	}
}

func portableRuntimeTestAsset(hostRoot, source, target string, kind PortableRuntimeAssetKind, pathKind PortableRuntimePathKind, executable bool) PortableRuntimeAsset {
	return PortableRuntimeAsset{
		Kind:          kind,
		PathKind:      pathKind,
		Source:        filepath.Join(hostRoot, source),
		Target:        target,
		ContentSHA256: portableRuntimeTestDigest,
		Executable:    executable,
	}
}

func makeValidDynamicContribution(hostRoot string, contribution *PortableRuntimeContribution) {
	contribution.ClosureClass = PortableRuntimeClosureDynamic
	contribution.Launch = PortableRuntimeLaunch{
		EntrypointTarget:   "bin/tool",
		LoaderTarget:       "lib/loader",
		LibraryRootTargets: []string{"lib/runtime"},
	}
	contribution.Assets = append(contribution.Assets,
		portableRuntimeTestAsset(hostRoot, "loader", "lib/loader", PortableRuntimeAssetSupport, PortableRuntimePathFile, true),
		portableRuntimeTestAsset(hostRoot, "runtime", "lib/runtime", PortableRuntimeAssetInstallTree, PortableRuntimePathTree, false),
	)
}

func validPortableRuntimeExecutionContribution(hostRoot string) PortableRuntimeContribution {
	return PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureStatic,
		Launch:       PortableRuntimeLaunch{EntrypointTarget: "bin/tool"},
		Assets: []PortableRuntimeAsset{
			portableRuntimeTestAsset(hostRoot, "tool", "bin/tool", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
			portableRuntimeTestAsset(hostRoot, "config", "config/opencode", PortableRuntimeAssetConfig, PortableRuntimePathTree, false),
		},
		Environment: []PortableRuntimeEnvironment{{Name: "API_KEY"}},
		ExecutionConstraints: PortableRuntimeExecutionConstraints{
			Environment: []PortableRuntimeEnvironmentConstraint{
				{Name: "FEATURE_ENABLED", Kind: PortableRuntimeEnvironmentFixedTrue},
				{Name: "XDG_CONFIG_HOME", Kind: PortableRuntimeEnvironmentGuestPath, GuestPath: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathRuntime, Target: "config"}},
				{Name: "PATH", Kind: PortableRuntimeEnvironmentRuntimePath},
			},
			ReadOnlyPaths: []PortableRuntimeGuestPath{
				{Scope: PortableRuntimeGuestPathRuntime, Target: "config/opencode"},
			},
			RequiredAbsentPaths: []PortableRuntimeGuestPath{
				{Scope: PortableRuntimeGuestPathHome, Target: ".config/opencode"},
			},
			FixedArguments: []string{"--pure"},
		},
	}
}

func clonePortableRuntimeTestContribution(in PortableRuntimeContribution) PortableRuntimeContribution {
	out := in
	out.Launch.RuntimeArgs = append([]string(nil), in.Launch.RuntimeArgs...)
	out.Launch.LibraryRootTargets = append([]string(nil), in.Launch.LibraryRootTargets...)
	out.Assets = append([]PortableRuntimeAsset(nil), in.Assets...)
	out.Environment = append([]PortableRuntimeEnvironment(nil), in.Environment...)
	out.ExecutionConstraints.Environment = append([]PortableRuntimeEnvironmentConstraint(nil), in.ExecutionConstraints.Environment...)
	out.ExecutionConstraints.ReadOnlyPaths = append([]PortableRuntimeGuestPath(nil), in.ExecutionConstraints.ReadOnlyPaths...)
	out.ExecutionConstraints.RequiredAbsentPaths = append([]PortableRuntimeGuestPath(nil), in.ExecutionConstraints.RequiredAbsentPaths...)
	out.ExecutionConstraints.FixedArguments = append([]string(nil), in.ExecutionConstraints.FixedArguments...)
	out.ExecutionConstraints.FixedOptionValues = append([]PortableRuntimeFixedOptionValue(nil), in.ExecutionConstraints.FixedOptionValues...)
	out.StateProjections = make([]PortableRuntimeStateProjection, len(in.StateProjections))
	for i, projection := range in.StateProjections {
		out.StateProjections[i] = PortableRuntimeStateProjection{
			Directory: projection.Directory,
			Entries:   append([]PortableRuntimeStateProjectionEntry(nil), projection.Entries...),
		}
	}
	return out
}

func validPortableRuntimeMixedStateContribution(hostRoot string) PortableRuntimeContribution {
	return PortableRuntimeContribution{
		ClosureClass: PortableRuntimeClosureStatic,
		Launch:       PortableRuntimeLaunch{EntrypointTarget: "bin/tool"},
		Assets: []PortableRuntimeAsset{
			portableRuntimeTestAsset(hostRoot, "tool", "bin/tool", PortableRuntimeAssetExecutable, PortableRuntimePathFile, true),
			portableRuntimeTestAsset(hostRoot, "pi-settings", "config/pi/settings.json", PortableRuntimeAssetConfig, PortableRuntimePathFile, false),
			portableRuntimeTestAsset(hostRoot, "pi-auth", "state/pi/auth.json", PortableRuntimeAssetCredential, PortableRuntimePathFile, false),
			portableRuntimeTestAsset(hostRoot, "gemini-settings", "config/gemini/settings.json", PortableRuntimeAssetConfig, PortableRuntimePathFile, false),
			portableRuntimeTestAsset(hostRoot, "gemini-quota", "state/gemini/quota.json", PortableRuntimeAssetQuota, PortableRuntimePathFile, false),
		},
		ExecutionConstraints: PortableRuntimeExecutionConstraints{
			RequiredAbsentPaths: []PortableRuntimeGuestPath{
				{Scope: PortableRuntimeGuestPathHome, Target: ".gemini/extensions"},
			},
		},
		StateProjections: []PortableRuntimeStateProjection{
			{
				Directory: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathState, Target: "pi"},
				Entries: []PortableRuntimeStateProjectionEntry{
					{AssetTarget: "config/pi/settings.json", Target: "settings.json"},
					{AssetTarget: "state/pi/auth.json", Target: "auth.json"},
				},
			},
			{
				Directory: PortableRuntimeGuestPath{Scope: PortableRuntimeGuestPathHome, Target: ".gemini"},
				Entries: []PortableRuntimeStateProjectionEntry{
					{AssetTarget: "config/gemini/settings.json", Target: "settings.json"},
					{AssetTarget: "state/gemini/quota.json", Target: "oauth_creds.json"},
				},
			},
		},
	}
}
