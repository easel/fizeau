package portableruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

func TestPortableRuntimeActivationVerifiesManifestAndProviders(t *testing.T) {
	fixture, bundle := prepareActivationFixture(t)
	plan, err := LoadActivation(bundle.RuntimeRoot(), os.LookupEnv)
	if err != nil {
		t.Fatalf("LoadActivation() error = %v", err)
	}
	if got := plan.ProviderSnapshot(); !reflect.DeepEqual(got, fixture.request.Providers) {
		t.Fatalf("provider snapshot = %#v, want %#v", got, fixture.request.Providers)
	}
	secrets := plan.ProviderSecrets()
	if len(secrets) != 1 || secrets[0].ProviderName() != fixture.request.Providers.ProviderNames[0] ||
		secrets[0].APIKey() != fixture.apiKey || secrets[0].Headers()["Authorization"] != fixture.headerValue {
		t.Fatalf("provider secrets did not round trip: %#v", secrets)
	}
	if got, err := plan.GuestPath("bin/runner"); err != nil || got != GuestRoot+"/bin/runner" {
		t.Fatalf("GuestPath() = %q, %v", got, err)
	}
	if got := plan.InheritedEnvironment(); got[fixture.environmentKey] != fixture.environmentVal || len(got) != 1 {
		t.Fatalf("inherited environment = %#v", got)
	}
	diagnostics := fmt.Sprintf("%s %v %+v %#v", mustActivationJSON(t, plan), plan, plan, plan)
	for _, forbidden := range []string{fixture.environmentVal, fixture.apiKey, fixture.headerValue, bundle.RuntimeRoot()} {
		if strings.Contains(diagnostics, forbidden) {
			t.Fatalf("activation diagnostics leak private value %q: %s", forbidden, diagnostics)
		}
	}

	// Every accessor is defensive, including nested maps and slices.
	snapshot := plan.ProviderSnapshot()
	snapshot.ProviderNames[0] = "mutated"
	snapshot.Providers[0].Endpoints = append(snapshot.Providers[0].Endpoints, ProviderEndpoint{Name: "mutated"})
	secrets[0].Headers()["Authorization"] = "mutated"
	environment := plan.InheritedEnvironment()
	environment[fixture.environmentKey] = "mutated"
	if plan.ProviderSnapshot().ProviderNames[0] != fixture.request.Providers.ProviderNames[0] ||
		len(plan.ProviderSnapshot().Providers[0].Endpoints) != len(fixture.request.Providers.Providers[0].Endpoints) ||
		plan.ProviderSecrets()[0].Headers()["Authorization"] != fixture.headerValue ||
		plan.InheritedEnvironment()[fixture.environmentKey] != fixture.environmentVal {
		t.Fatal("activation accessors alias owned state")
	}

	tamperCases := []struct {
		name   string
		tamper func(*testing.T, materializerFixture, *Bundle)
	}{
		{"checksum", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			writeActivationFile(t, bundle.RuntimeRoot(), manifestSum, []byte(strings.Repeat("0", 64)+"\n"), 0o600)
		}},
		{"version", mutateActivationManifest(func(manifest *Manifest) { manifest.Version++ })},
		{"manifest-schema", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			data, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(manifestTarget)))
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("{\n"), []byte("{\n  \"unknown\": true,\n"), 1)
			rewriteActivationManifestBytes(t, bundle, data)
		}},
		{"target", mutateActivationManifest(func(manifest *Manifest) { manifest.TargetGOARCH = "wrong" })},
		{"guest-root", mutateActivationManifest(func(manifest *Manifest) { manifest.GuestRoot = "/tmp/runtime" })},
		{"inventory-entrypoint", mutateActivationManifest(func(manifest *Manifest) {
			entrypoint := manifest.Entrypoints["fixture"]
			delete(manifest.Entrypoints, "fixture")
			manifest.Entrypoints["other"] = entrypoint
		})},
		{"environment-union", mutateActivationManifest(func(manifest *Manifest) {
			manifest.EnvironmentNames = append(manifest.EnvironmentNames, "UNDECLARED_HOST_VALUE")
			sortActivationStrings(manifest.EnvironmentNames)
		})},
		{"provider-identity", mutateActivationManifest(func(manifest *Manifest) { manifest.Providers.Providers[0].Name = "other" })},
		{"provider-treatment", mutateActivationManifest(func(manifest *Manifest) { manifest.Providers.WorkDir.Treatment = "host" })},
		{"secret-reference", mutateActivationManifest(func(manifest *Manifest) { manifest.ProviderSecretsFile.Target = ".fizeau/other.json" })},
		{"secret-version", mutateActivationSecrets(func(document *privateProviderSecretsDocument) { document.Version++ })},
		{"secret-provider-identity", mutateActivationSecrets(func(document *privateProviderSecretsDocument) { document.Providers[0].ProviderName = "other" })},
		{"secret-digest", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			writeActivationFile(t, bundle.RuntimeRoot(), providerSecrets, []byte("{}\n"), 0o600)
		}},
		{"asset-content", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			writeActivationFile(t, bundle.RuntimeRoot(), "config/tool/settings.json", []byte("tampered\n"), 0o600)
		}},
		{"asset-mode", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			if err := os.Chmod(filepath.Join(bundle.RuntimeRoot(), "bin", "runner"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"tree-content", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			writeActivationFile(t, bundle.RuntimeRoot(), "lib/support/member.txt", []byte("tampered\n"), 0o600)
		}},
		{"tree-mode", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			if err := os.Chmod(filepath.Join(bundle.RuntimeRoot(), "lib", "support", "nested"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"hard-link", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			if err := os.Link(filepath.Join(bundle.RuntimeRoot(), "config", "tool", "settings.json"), filepath.Join(bundle.RuntimeRoot(), "undeclared-hardlink")); err != nil {
				t.Fatal(err)
			}
		}},
		{"undeclared-content", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			writeActivationFile(t, bundle.RuntimeRoot(), "undeclared.txt", []byte("extra\n"), 0o600)
		}},
		{"symlink", func(t *testing.T, _ materializerFixture, bundle *Bundle) {
			target := filepath.Join(bundle.RuntimeRoot(), "bin", "runner")
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../config/tool/settings.json", target); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tamperCases {
		t.Run("rejects-"+test.name, func(t *testing.T) {
			fixture, bundle := prepareActivationFixture(t)
			test.tamper(t, fixture, bundle)
			if _, err := LoadActivation(bundle.RuntimeRoot(), func(string) (string, bool) { return "", true }); !errors.Is(err, ErrActivationInvalid) {
				t.Fatalf("LoadActivation() error = %v, want ErrActivationInvalid", err)
			}
		})
	}

	t.Run("rejects-fifo-without-blocking", func(t *testing.T) {
		fixture, bundle := prepareActivationFixture(t)
		target := filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(manifestTarget))
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(target, 0o600); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := LoadActivation(bundle.RuntimeRoot(), func(string) (string, bool) { return fixture.environmentVal, true })
			result <- err
		}()
		select {
		case err := <-result:
			if !errors.Is(err, ErrActivationInvalid) {
				t.Fatalf("FIFO activation error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("activation blocked while opening a FIFO")
		}
	})

	t.Run("rejects-same-content-file-replacement", func(t *testing.T) {
		_, bundle := prepareActivationFixture(t)
		manifest, _ := readFixtureManifest(t, bundle)
		asset := activationManifestAsset(t, manifest, "bin/runner")
		root, err := safefs.OpenNoFollowRoot(bundle.RuntimeRoot())
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		target := filepath.Join(bundle.RuntimeRoot(), "bin", "runner")
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		err = verifyActivationAssetWithHook(root, asset, func() {
			if err := os.Rename(target, filepath.Join(bundle.RuntimeRoot(), "bin", "old-runner")); err != nil {
				t.Fatal(err)
			}
			writeActivationFile(t, bundle.RuntimeRoot(), "bin/runner", content, 0o700)
		})
		if !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("same-content file replacement error = %v", err)
		}
	})

	t.Run("rejects-same-content-tree-replacement", func(t *testing.T) {
		_, bundle := prepareActivationFixture(t)
		manifest, _ := readFixtureManifest(t, bundle)
		asset := activationManifestAsset(t, manifest, "lib/support")
		root, err := safefs.OpenNoFollowRoot(bundle.RuntimeRoot())
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		target := filepath.Join(bundle.RuntimeRoot(), "lib", "support")
		err = verifyActivationAssetWithHook(root, asset, func() {
			if err := os.Rename(target, filepath.Join(bundle.RuntimeRoot(), "lib", "old-support")); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(target, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeActivationFile(t, bundle.RuntimeRoot(), "lib/support/member.txt", []byte("support\n"), 0o600)
			writeActivationFile(t, bundle.RuntimeRoot(), "lib/support/alias.txt", []byte("support\n"), 0o600)
			writeActivationFile(t, bundle.RuntimeRoot(), "lib/support/nested/executable", []byte("executable\n"), 0o700)
		})
		if !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("same-content tree replacement error = %v", err)
		}
	})

	t.Run("interpreted-tree-owned-entrypoint", func(t *testing.T) {
		bundle := prepareInterpretedActivationFixture(t)
		if _, err := LoadActivation(bundle.RuntimeRoot(), func(string) (string, bool) { return "", true }); err != nil {
			t.Fatalf("interpreted activation error = %v", err)
		}
		mutateActivationManifest(func(manifest *Manifest) {
			entrypoint := manifest.Entrypoints["fixture"]
			entrypoint.Launch.EntrypointTreeMember = "missing.js"
			manifest.Entrypoints["fixture"] = entrypoint
		})(t, materializerFixture{}, bundle)
		if _, err := LoadActivation(bundle.RuntimeRoot(), func(string) (string, bool) { return "", true }); !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("missing interpreted member error = %v", err)
		}
	})
}

func TestPortableRuntimeActivationRequiresDeclaredEnvironment(t *testing.T) {
	fixture, bundle := prepareActivationFixture(t)
	lookups := make([]string, 0)
	plan, err := LoadActivation(bundle.RuntimeRoot(), func(name string) (string, bool) {
		lookups = append(lookups, name)
		if name != fixture.environmentKey {
			t.Fatalf("unexpected environment lookup %q", name)
		}
		return "", true
	})
	if err != nil {
		t.Fatalf("present-empty environment rejected: %v", err)
	}
	value, present := plan.InheritedEnvironment()[fixture.environmentKey]
	if !present || value != "" || !reflect.DeepEqual(lookups, []string{fixture.environmentKey}) {
		t.Fatalf("present-empty environment = (%q, %t), lookups %#v", value, present, lookups)
	}
	if _, err := LoadActivation(bundle.RuntimeRoot(), func(name string) (string, bool) {
		return "", name != fixture.environmentKey
	}); !errors.Is(err, ErrActivationInvalid) {
		t.Fatalf("missing environment error = %v", err)
	}
}

func prepareActivationFixture(t *testing.T) (materializerFixture, *Bundle) {
	t.Helper()
	fixture := newActivationFixture(t)
	tree := filepath.Join(t.TempDir(), "support")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMaterializerSource(t, tree, "member.txt", []byte("support\n"), 0o600)
	writeMaterializerSource(t, tree, "nested/executable", []byte("executable\n"), 0o700)
	if err := os.Symlink("member.txt", filepath.Join(tree, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeTreeDigest(tree)
	if err != nil {
		t.Fatal(err)
	}
	harness := fixture.request.Inventory[0].Instance.(materializerTestHarness)
	harness.contribution.Assets = append(harness.contribution.Assets, harnesses.PortableRuntimeAsset{
		Kind: harnesses.PortableRuntimeAssetSupport, PathKind: harnesses.PortableRuntimePathTree,
		Source: tree, Target: "lib/support", ContentSHA256: digest,
	})
	fixture.request.Inventory[0].Instance = harness
	bundle := prepareMaterializerFixture(t, fixture)
	return fixture, bundle
}

func newActivationFixture(t *testing.T) materializerFixture {
	t.Helper()
	fixture := newMaterializerFixture(t)
	fixture.request.Providers.WorkDir = ConfigField{Field: WorkDirField, Treatment: ConfigTreatmentGuestPrivate, Reason: WorkDirRemappedReason}
	fixture.request.Providers.SessionLogDir = ConfigField{Field: SessionLogDirField, Treatment: ConfigTreatmentExcluded, Reason: SessionLogExcludedReason}
	fixture.request.Providers.HealthCooldown = 43 * time.Second
	fixture.request.Providers.Providers[0] = ConfiguredProvider{
		Name: fixture.request.Providers.ProviderNames[0], Type: "openai-compatible",
		BaseURL: "https://provider.invalid/v1", ServerInstance: "instance-a",
		Endpoints: []ProviderEndpoint{{Name: "east", BaseURL: "https://east.invalid/v1", ServerInstance: "east-a"}},
		Model:     "model-a", Billing: "per_token", IncludeByDefault: true, IncludeByDefaultSet: true,
		ContextWindow: 131072, ConfigError: "safe structural diagnostic", DailyTokenBudget: 123456,
		CreditBalanceThresholdUSD: 18.75, CreditProbeTTL: 17 * time.Minute,
	}
	return fixture
}

func prepareInterpretedActivationFixture(t *testing.T) *Bundle {
	t.Helper()
	fixture := newActivationFixture(t)
	packageTree := filepath.Join(t.TempDir(), "package")
	if err := os.Mkdir(packageTree, 0o700); err != nil {
		t.Fatal(err)
	}
	writeMaterializerSource(t, packageTree, "cli.js", []byte("console.log('portable')\n"), 0o600)
	digest, err := harnesses.PortableRuntimeTreeDigest(packageTree)
	if err != nil {
		t.Fatal(err)
	}
	harness := fixture.request.Inventory[0].Instance.(materializerTestHarness)
	harness.contribution.ClosureClass = harnesses.PortableRuntimeClosureInterpreted
	harness.contribution.Launch = harnesses.PortableRuntimeLaunch{
		EntrypointTarget: "lib/package/cli.js", EntrypointTreeMember: "cli.js", InterpreterTarget: "bin/runner",
	}
	harness.contribution.Assets = append(harness.contribution.Assets, harnesses.PortableRuntimeAsset{
		Kind: harnesses.PortableRuntimeAssetInstallTree, PathKind: harnesses.PortableRuntimePathTree,
		Source: packageTree, Target: "lib/package", ContentSHA256: digest,
	})
	fixture.request.Inventory[0].Instance = harness
	return prepareMaterializerFixture(t, fixture)
}

func mutateActivationManifest(mutate func(*Manifest)) func(*testing.T, materializerFixture, *Bundle) {
	return func(t *testing.T, _ materializerFixture, bundle *Bundle) {
		manifest, _ := readFixtureManifest(t, bundle)
		mutate(&manifest)
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		rewriteActivationManifestBytes(t, bundle, data)
	}
}

func mutateActivationSecrets(mutate func(*privateProviderSecretsDocument)) func(*testing.T, materializerFixture, *Bundle) {
	return func(t *testing.T, _ materializerFixture, bundle *Bundle) {
		secretBytes, err := os.ReadFile(filepath.Join(bundle.RuntimeRoot(), filepath.FromSlash(providerSecrets)))
		if err != nil {
			t.Fatal(err)
		}
		var document privateProviderSecretsDocument
		if err := json.Unmarshal(secretBytes, &document); err != nil {
			t.Fatal(err)
		}
		mutate(&document)
		secretBytes, err = json.MarshalIndent(document, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		secretBytes = append(secretBytes, '\n')
		writeActivationFile(t, bundle.RuntimeRoot(), providerSecrets, secretBytes, 0o600)
		manifest, _ := readFixtureManifest(t, bundle)
		digest := sha256.Sum256(secretBytes)
		manifest.ProviderSecretsFile.ContentSHA256 = hex.EncodeToString(digest[:])
		manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		rewriteActivationManifestBytes(t, bundle, append(manifestBytes, '\n'))
	}
}

func rewriteActivationManifestBytes(t *testing.T, bundle *Bundle, data []byte) {
	t.Helper()
	writeActivationFile(t, bundle.RuntimeRoot(), manifestTarget, data, 0o600)
	digest := sha256.Sum256(data)
	writeActivationFile(t, bundle.RuntimeRoot(), manifestSum, []byte(hex.EncodeToString(digest[:])+"\n"), 0o600)
}

func writeActivationFile(t *testing.T, root, target string, data []byte, mode os.FileMode) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
}

func activationManifestAsset(t *testing.T, manifest Manifest, target string) ManifestAsset {
	t.Helper()
	for _, asset := range manifest.Assets {
		if asset.Target == target {
			return asset
		}
	}
	t.Fatalf("manifest asset %q missing", target)
	return ManifestAsset{}
}

func sortActivationStrings(values []string) {
	for i := range values {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func mustActivationJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
