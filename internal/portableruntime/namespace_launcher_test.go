package portableruntime

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestPortableRuntimeNamespaceLauncherArtifactParity(t *testing.T) {
	targets := []struct {
		target  harnesses.PortableRuntimeTarget
		machine elf.Machine
	}{
		{target: harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: "amd64"}, machine: elf.EM_X86_64},
		{target: harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: "arm64"}, machine: elf.EM_AARCH64},
	}
	if len(targets) != 2 {
		t.Fatalf("launcher target cardinality = %d", len(targets))
	}
	if namespaceLauncherZigVersion != "0.16.0" || namespaceLauncherSourceVersion != 1 || !validateNamespaceLauncherSourceIdentity() {
		t.Fatal("launcher source/version identity is invalid")
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate launcher parity test")
	}
	packageRoot := filepath.Dir(testFile)
	generatorBytes, err := os.ReadFile(filepath.Join(packageRoot, "..", "..", "scripts", "generate-portable-namespace-launcher.sh"))
	if err != nil {
		t.Fatal(err)
	}
	generator := string(generatorBytes)
	for _, forbidden := range []string{"curl ", "wget ", "go env", "uname ", "-mcpu native"} {
		if strings.Contains(generator, forbidden) {
			t.Fatalf("launcher generator contains ambient or download operation %q", forbidden)
		}
	}
	for _, required := range []string{
		`0.16.0`, `x86_64-linux-musl`, `aarch64-linux-musl`, `-mcpu baseline`, `-O ReleaseSmall`,
		`-static`, `-fstrip`, `-fsingle-threaded`, `--build-id=none`, `--check`, `--write`,
	} {
		if !strings.Contains(generator, required) {
			t.Fatalf("launcher generator is missing governed recipe %q", required)
		}
	}
	source := string(namespaceLauncherSource)
	for _, required := range []string{
		`source_version = 1`, `required_zig_version = "0.16.0"`, `builtin.os.tag != .linux`,
		`builtin.abi != .musl`, `builtin.link_mode != .static`, `builtin.single_threaded`,
		`.x86_64`, `.aarch64`, `std.process.exit(125)`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("launcher source is missing governed identity %q", required)
		}
	}

	for _, test := range targets {
		t.Run(test.target.GOARCH, func(t *testing.T) {
			artifact, err := namespaceLauncherForTarget(test.target)
			if err != nil {
				t.Fatalf("namespaceLauncherForTarget() error = %v", err)
			}
			if artifact.target != test.target || !validDigest(artifact.digest) || len(artifact.bytes) == 0 {
				t.Fatalf("artifact identity = %#v", artifact)
			}
			checkedBytes, err := os.ReadFile(filepath.Join(packageRoot, "nslauncher", "artifacts", "namespace-launcher-linux-"+test.target.GOARCH))
			if err != nil || !bytes.Equal(checkedBytes, artifact.bytes) {
				t.Fatalf("checked/embedded launcher parity error = %v", err)
			}
			assertNamespaceLauncherELF(t, artifact.bytes, test.machine)

			artifact.bytes[0] ^= 0xff
			fresh, err := namespaceLauncherForTarget(test.target)
			if err != nil || bytes.Equal(artifact.bytes, fresh.bytes) || fresh.bytes[0] != 0x7f {
				t.Fatal("launcher lookup returned mutable embedded storage")
			}
		})
	}
	for _, target := range []harnesses.PortableRuntimeTarget{
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "linux", GOARCH: "386"},
		{GOOS: "", GOARCH: ""},
	} {
		if _, err := namespaceLauncherForTarget(target); err == nil {
			t.Fatalf("unsupported launcher target accepted: %#v", target)
		}
	}

	t.Run("required subprocess cardinality and activation", func(t *testing.T) {
		fixture := newActivationFixture(t)
		shared := fixture.request.Inventory[0].Instance.(materializerTestHarness).contribution
		fixture.request.Inventory = append(fixture.request.Inventory, harnesses.PortableRuntimeSurface{
			Name: "second-required", Transport: harnesses.PortableRuntimeTransportSubprocess,
			Inclusion: harnesses.PortableRuntimeInclusionRequired, Instance: materializerTestHarness{contribution: shared},
		})
		bundle := prepareMaterializerFixture(t, fixture)
		manifest, _ := readFixtureManifest(t, bundle)
		artifact, err := namespaceLauncherForTarget(fixture.request.Target)
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.Entrypoints) != 2 || manifest.NamespaceLauncher == nil ||
			*manifest.NamespaceLauncher != (ManifestContentReference{Target: namespaceLauncherTarget, ContentSHA256: artifact.digest}) {
			t.Fatalf("launcher manifest cardinality = %#v", manifest.NamespaceLauncher)
		}
		launcherPath := filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(namespaceLauncherTarget))
		data, err := os.ReadFile(launcherPath)
		if err != nil || !bytes.Equal(data, artifact.bytes) {
			t.Fatalf("materialized launcher identity error = %v", err)
		}
		info, err := os.Stat(launcherPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 {
			t.Fatalf("materialized launcher mode = %v, error %v", info, err)
		}
		count := 0
		if err := filepath.WalkDir(bundle.RuntimeRoot(), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && entry.Name() == "namespace-launcher" {
				count++
			}
			return err
		}); err != nil || count != 1 {
			t.Fatalf("launcher file cardinality = %d, error %v", count, err)
		}
		if _, err := LoadActivation(bundle.RuntimeRoot(), os.LookupEnv); err != nil {
			t.Fatalf("LoadActivation() error = %v", err)
		}
	})

	for _, test := range []struct {
		name      string
		inventory func(materializerFixture) []harnesses.PortableRuntimeSurface
	}{
		{name: "non-subprocess", inventory: func(materializerFixture) []harnesses.PortableRuntimeSurface {
			return []harnesses.PortableRuntimeSurface{{Name: "remote", Transport: harnesses.PortableRuntimeTransportHTTP, Inclusion: harnesses.PortableRuntimeInclusionNonSubprocess}}
		}},
		{name: "exact-pin-only", inventory: func(f materializerFixture) []harnesses.PortableRuntimeSurface {
			return []harnesses.PortableRuntimeSurface{{Name: "pin", Transport: harnesses.PortableRuntimeTransportSubprocess, Inclusion: harnesses.PortableRuntimeInclusionExactPinOnly, Instance: f.request.Inventory[0].Instance}}
		}},
	} {
		t.Run("subprocess-free-"+test.name, func(t *testing.T) {
			fixture := newActivationFixture(t)
			fixture.request.Inventory = test.inventory(fixture)
			bundle := prepareMaterializerFixture(t, fixture)
			manifest, manifestBytes := readFixtureManifest(t, bundle)
			if len(manifest.Entrypoints) != 0 || manifest.NamespaceLauncher != nil || bytes.Contains(manifestBytes, []byte("namespace_launcher")) {
				t.Fatalf("subprocess-free manifest contains launcher: %s", manifestBytes)
			}
			if _, err := os.Stat(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(namespaceLauncherTarget))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("subprocess-free launcher stat error = %v", err)
			}
			if _, err := LoadActivation(bundle.RuntimeRoot(), os.LookupEnv); err != nil {
				t.Fatalf("subprocess-free activation error = %v", err)
			}
		})
	}

	for _, target := range []string{".fizeau", namespaceLauncherTarget, namespaceLauncherTarget + "/child"} {
		t.Run("rejects-contributor-collision-"+strings.ReplaceAll(target, "/", "-"), func(t *testing.T) {
			fixture := newMaterializerFixture(t)
			harness := fixture.request.Inventory[0].Instance.(materializerTestHarness)
			claim := harness.contribution.Assets[1]
			claim.Target = target
			harness.contribution.Assets = append(harness.contribution.Assets, claim)
			fixture.request.Inventory[0].Instance = harness
			if _, err := Prepare(t.Context(), fixture.request); !errors.Is(err, ErrClosureIncomplete) {
				t.Fatalf("Prepare() error = %v, want ErrClosureIncomplete", err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		tamper func(*testing.T, *Bundle)
	}{
		{name: "content", tamper: func(t *testing.T, bundle *Bundle) {
			writeActivationFile(t, bundle.RuntimeRoot(), namespaceLauncherTarget, []byte("tampered\n"), 0o700)
		}},
		{name: "mode", tamper: func(t *testing.T, bundle *Bundle) {
			if err := os.Chmod(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(namespaceLauncherTarget)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "manifest-target", tamper: mutateNamespaceLauncherManifest(func(reference *ManifestContentReference) {
			reference.Target = ".fizeau/other-launcher"
		})},
		{name: "manifest-digest", tamper: mutateNamespaceLauncherManifest(func(reference *ManifestContentReference) {
			reference.ContentSHA256 = strings.Repeat("0", 64)
		})},
		{name: "missing-reference", tamper: mutateNamespaceLauncherManifest(func(reference *ManifestContentReference) {
			*reference = ManifestContentReference{}
		})},
		{name: "required-absent-conflict", tamper: func(t *testing.T, bundle *Bundle) {
			mutateActivationManifest(func(manifest *Manifest) {
				entrypoint := manifest.Entrypoints["fixture"]
				entrypoint.ExecutionConstraints.RequiredAbsentPaths = append(entrypoint.ExecutionConstraints.RequiredAbsentPaths,
					harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathRuntime, Target: namespaceLauncherTarget})
				manifest.Entrypoints["fixture"] = entrypoint
			})(t, materializerFixture{}, bundle)
		}},
	} {
		t.Run("rejects-launcher-"+test.name, func(t *testing.T) {
			_, bundle := prepareActivationFixture(t)
			test.tamper(t, bundle)
			if _, err := LoadActivation(bundle.RuntimeRoot(), func(string) (string, bool) { return "value", true }); !errors.Is(err, ErrActivationInvalid) {
				t.Fatalf("LoadActivation() error = %v, want ErrActivationInvalid", err)
			}
		})
	}

	t.Run("diagnostics remain opaque", func(t *testing.T) {
		fixture := newActivationFixture(t)
		bundle := prepareMaterializerFixture(t, fixture)
		plan, err := LoadActivation(bundle.RuntimeRoot(), os.LookupEnv)
		if err != nil {
			t.Fatal(err)
		}
		diagnostics := fmt.Sprintf("%v %#v %v %#v", bundle, bundle, plan, plan)
		for _, forbidden := range []string{namespaceLauncherTarget, namespaceLauncherZigVersion, strings.TrimSpace(namespaceLauncherSourceDigestRecord)} {
			if strings.Contains(diagnostics, forbidden) {
				t.Fatalf("launcher identity leaked through diagnostics: %s", diagnostics)
			}
		}
		if got := bundle.EnvironmentNames(); !reflect.DeepEqual(got, []string{fixture.environmentKey}) {
			t.Fatalf("launcher changed environment names: %#v", got)
		}
	})
}

func assertNamespaceLauncherELF(t *testing.T, data []byte, machine elf.Machine) {
	t.Helper()
	file, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("launcher is not ELF: %v", err)
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != machine || file.Entry == 0 {
		t.Fatalf("ELF identity = class %v data %v machine %v entry %#x", file.Class, file.Data, file.Machine, file.Entry)
	}
	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		t.Fatalf("ELF type = %v", file.Type)
	}
	for _, program := range file.Progs {
		switch program.Type {
		case elf.PT_INTERP, elf.PT_DYNAMIC, elf.PT_TLS:
			t.Fatalf("launcher contains forbidden program header %v", program.Type)
		}
	}
	for _, section := range file.Sections {
		if section.Type == elf.SHT_SYMTAB || section.Name == ".note.gnu.build-id" || strings.HasPrefix(section.Name, ".debug_") {
			t.Fatalf("launcher contains non-stripped section %q (%v)", section.Name, section.Type)
		}
	}
	if libraries, err := file.ImportedLibraries(); err == nil && len(libraries) != 0 {
		t.Fatalf("launcher imports dynamic libraries: %#v", libraries)
	}
	if bytes.Contains(data, []byte(filepath.Clean("/home/erik/Projects/fizeau"))) || bytes.Contains(data, []byte(runtime.GOROOT())) {
		t.Fatal("launcher embeds a host build path")
	}
}

func mutateNamespaceLauncherManifest(mutate func(*ManifestContentReference)) func(*testing.T, *Bundle) {
	return func(t *testing.T, bundle *Bundle) {
		mutateActivationManifest(func(manifest *Manifest) {
			if manifest.NamespaceLauncher == nil {
				t.Fatal("test manifest has no namespace launcher")
			}
			mutate(manifest.NamespaceLauncher)
		})(t, materializerFixture{}, bundle)
	}
}
