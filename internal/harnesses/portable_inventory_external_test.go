package harnesses_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
)

var (
	_ harnesses.PortableRuntimeHarness = (*geminiharness.Runner)(nil)
	_ harnesses.PortableRuntimeHarness = (*piharness.Runner)(nil)
)

func TestPortableRuntimeInventoryCoversEveryEligibleRegisteredHarness(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	instances := builtin.Instances()
	rows, err := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistry(), instances)
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}

	wantNames := []string{
		"claude", "claude-tui", "codex", "fiz", "gemini", "grok", "lmstudio",
		"lucebox", "omlx", "opencode", "openrouter", "pi", "script", "virtual",
		"vllm",
	}
	wantClassification := map[string]struct {
		transport harnesses.PortableRuntimeTransport
		inclusion harnesses.PortableRuntimeInclusion
	}{
		"claude":     {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"claude-tui": {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"codex":      {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"gemini":     {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"grok":       {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"opencode":   {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"pi":         {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"fiz":        {harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeInclusionNonSubprocess},
		"lmstudio":   {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"lucebox":    {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"omlx":       {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"openrouter": {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"vllm":       {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"script":     {harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeInclusionTestOnly},
		"virtual":    {harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeInclusionTestOnly},
	}
	gotNames := make([]string, 0, len(rows))
	for _, row := range rows {
		gotNames = append(gotNames, row.Name)
		want, ok := wantClassification[row.Name]
		if !ok {
			t.Errorf("unexpected inventory row %q", row.Name)
			continue
		}
		if row.Transport != want.transport || row.Inclusion != want.inclusion {
			t.Errorf("row %q classification = (%q, %q), want (%q, %q)", row.Name, row.Transport, row.Inclusion, want.transport, want.inclusion)
		}
		if row.Inclusion == harnesses.PortableRuntimeInclusionRequired {
			if row.Instance != instances[row.Name] {
				t.Errorf("required row %q does not retain the actual runner instance", row.Name)
			}
			if row.Name == "claude" || row.Name == "claude-tui" || row.Name == "codex" || row.Name == "gemini" || row.Name == "opencode" || row.Name == "pi" {
				if _, ok := row.Instance.(harnesses.PortableRuntimeHarness); !ok {
					t.Errorf("required contributed row %q lacks PortableRuntimeHarness", row.Name)
				}
			}
		}
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("inventory names = %v, want %v", gotNames, wantNames)
	}
}

func TestPortableRuntimeInventoryIncludesGeminiAndPi(t *testing.T) {
	instances := builtin.Instances()
	geminiInstance := instances["gemini"]
	piInstance := instances["pi"]
	selected := map[string]harnesses.Harness{
		"gemini": geminiInstance,
		"pi":     piInstance,
	}
	rows, err := harnesses.BuildPortableRuntimeInventory(
		harnesses.NewRegistryForTest("gemini", "pi"),
		selected,
	)
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}
	if len(rows) != 2 || rows[0].Name != "gemini" || rows[1].Name != "pi" {
		t.Fatalf("Gemini/Pi inventory rows = %#v, want lexical [gemini pi]", rows)
	}

	wantTypes := map[string]reflect.Type{
		"gemini": reflect.TypeOf((*geminiharness.Runner)(nil)),
		"pi":     reflect.TypeOf((*piharness.Runner)(nil)),
	}
	for _, row := range rows {
		if row.Instance != selected[row.Name] {
			t.Fatalf("row %q did not retain the exact production instance", row.Name)
		}
		if reflect.TypeOf(row.Instance) != wantTypes[row.Name] {
			t.Fatalf("row %q instance type = %T, want %v", row.Name, row.Instance, wantTypes[row.Name])
		}
		if row.Transport != harnesses.PortableRuntimeTransportSubprocess || row.Inclusion != harnesses.PortableRuntimeInclusionRequired {
			t.Fatalf("row %q classification = (%q, %q), want required subprocess", row.Name, row.Transport, row.Inclusion)
		}
		if _, ok := row.Instance.(harnesses.PortableRuntimeHarness); !ok {
			t.Fatalf("row %q exact production instance lacks PortableRuntimeHarness", row.Name)
		}
		structure, ok := row.Instance.(harnesses.PortableRuntimeStructuralHarness)
		if !ok {
			t.Fatalf("row %q exact production instance lacks PortableRuntimeStructuralHarness", row.Name)
		}
		if got := structure.PortableRuntimeStructure(); got != (harnesses.PortableRuntimeStructure{
			Name: row.Name, Transport: harnesses.PortableRuntimeTransportSubprocess, Mode: harnesses.PortableRuntimeStructuralUnpinned,
		}) {
			t.Fatalf("row %q structural descriptor = %#v", row.Name, got)
		}

		contributor := row.Instance.(harnesses.PortableRuntimeHarness)
		contribution, targetErr := contributor.PortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{
			GOOS: "portable-inventory-unsupported", GOARCH: "portable-inventory-unsupported",
		})
		if !errors.Is(targetErr, harnesses.ErrPortableRuntimeTargetUnsupported) || !reflect.DeepEqual(contribution, harnesses.PortableRuntimeContribution{}) {
			t.Fatalf("row %q target mismatch = %#v, %v; want zero plus target unsupported", row.Name, contribution, targetErr)
		}
	}

	for _, missing := range []string{"gemini", "pi"} {
		without := map[string]harnesses.Harness{}
		for name, instance := range selected {
			if name != missing {
				without[name] = instance
			}
		}
		_, missingErr := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistryForTest("gemini", "pi"), without)
		if missingErr == nil || !strings.Contains(missingErr.Error(), missing) {
			t.Fatalf("inventory without %q error = %v, want missing-instance failure", missing, missingErr)
		}
		for _, forbidden := range []string{os.Getenv("HOME"), os.Getenv("PATH")} {
			if forbidden != "" && strings.Contains(missingErr.Error(), forbidden) {
				t.Fatalf("inventory without %q leaked ambient path %q: %v", missing, forbidden, missingErr)
			}
		}
	}

	t.Run("supported retained closures", func(t *testing.T) {
		if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
			t.Skipf("retained Gemini/Pi portable runtimes are linux/arm64, host is %s/%s", runtime.GOOS, runtime.GOARCH)
		}

		sources, ok := portableInventoryRetainedSources(t)
		if !ok {
			return
		}
		home := t.TempDir()
		geminiLauncher := filepath.Join(home, ".local", "share", "mise", "installs", "node", "22.21.1", "bin", "gemini")
		geminiPackage := filepath.Join(home, ".local", "share", "mise", "installs", "node", "22.21.1", "lib", "node_modules", "@google", "gemini-cli")
		node := filepath.Join(home, ".local", "share", "mise", "installs", "node", "22.22.0", "bin", "node")
		piPrefix := filepath.Join(t.TempDir(), "pi-install")
		piLauncher := filepath.Join(piPrefix, "bin", "pi")
		piPackage := filepath.Join(piPrefix, "lib", "node_modules", "@mariozechner", "pi-coding-agent")

		copyPortableInventoryTree(t, sources.geminiPackage, geminiPackage)
		copyPortableInventorySymlink(t, sources.geminiLauncher, geminiLauncher)
		copyPortableInventoryFile(t, sources.node, node)
		copyPortableInventoryTree(t, sources.piPackage, piPackage)
		copyPortableInventorySymlink(t, sources.piLauncher, piLauncher)
		writePortableInventoryStateFixtures(t, home)

		geminiRunner := geminiInstance.(*geminiharness.Runner)
		piRunner := piInstance.(*piharness.Runner)
		geminiRunner.Binary = geminiLauncher
		piRunner.Binary = piLauncher

		emptyPath := filepath.Join(t.TempDir(), "empty-path")
		if err := os.MkdirAll(emptyPath, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("PATH", emptyPath)
		t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", filepath.Join(home, "state", "fizeau", "gemini-quota.json"))
		t.Setenv("PORTABLE_PI_INVENTORY_TOKEN", "pi-inventory-secret-value")
		for _, name := range []string{
			"GEMINI_CLI_HOME", "GEMINI_CLI_SYSTEM_DEFAULTS_PATH",
			"GEMINI_CLI_SYSTEM_SETTINGS_PATH", "GEMINI_CLI_TRUSTED_FOLDERS_PATH",
		} {
			t.Setenv(name, "")
		}

		target := harnesses.PortableRuntimeTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
		for _, row := range rows {
			contributor := row.Instance.(harnesses.PortableRuntimeHarness)
			contribution, err := contributor.PortableRuntimeAssets(context.Background(), target)
			if err != nil {
				t.Fatalf("row %q PortableRuntimeAssets() error = %v", row.Name, err)
			}
			assertPortableInventoryContribution(t, row.Name, target, contribution)
		}
	})
}

type portableInventorySources struct {
	geminiLauncher string
	geminiPackage  string
	node           string
	piLauncher     string
	piPackage      string
}

func portableInventoryRetainedSources(t *testing.T) (portableInventorySources, bool) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("retained Gemini/Pi home is unavailable: %v", err)
		return portableInventorySources{}, false
	}
	sources := portableInventorySources{
		geminiLauncher: filepath.Join(home, ".local", "share", "mise", "installs", "node", "22.21.1", "bin", "gemini"),
		geminiPackage:  filepath.Join(home, ".local", "share", "mise", "installs", "node", "22.21.1", "lib", "node_modules", "@google", "gemini-cli"),
		node:           filepath.Join(home, ".local", "share", "mise", "installs", "node", "22.22.0", "bin", "node"),
	}
	launcher, err := exec.LookPath("pi")
	if errors.Is(err, exec.ErrNotFound) {
		t.Skip("retained Pi installation is unavailable")
		return portableInventorySources{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	sources.piLauncher, err = filepath.Abs(launcher)
	if err != nil {
		t.Fatal(err)
	}
	piPrefix := filepath.Dir(filepath.Dir(sources.piLauncher))
	sources.piPackage = filepath.Join(piPrefix, "lib", "node_modules", "@mariozechner", "pi-coding-agent")

	for _, source := range []string{sources.geminiLauncher, sources.geminiPackage, sources.node, sources.piLauncher, sources.piPackage} {
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			t.Skip("retained exact Gemini/Pi installation bytes are unavailable")
			return portableInventorySources{}, false
		} else if err != nil {
			t.Fatal(err)
		}
	}
	return sources, true
}

func assertPortableInventoryContribution(t *testing.T, name string, target harnesses.PortableRuntimeTarget, contribution harnesses.PortableRuntimeContribution) {
	t.Helper()
	if contribution.ClosureClass != harnesses.PortableRuntimeClosureInterpreted {
		t.Fatalf("row %q closure class = %q, want interpreted", name, contribution.ClosureClass)
	}
	normalized, err := harnesses.NormalizePortableRuntimeContribution(target, contribution)
	if err != nil {
		t.Fatalf("row %q normalization error = %v", name, err)
	}
	if !reflect.DeepEqual(normalized, contribution) {
		t.Fatalf("row %q contribution is not already in canonical normalized form", name)
	}

	var wantLaunch harnesses.PortableRuntimeLaunch
	var wantConstraints harnesses.PortableRuntimeExecutionConstraints
	var wantEnvironment []harnesses.PortableRuntimeEnvironment
	var wantProjection []harnesses.PortableRuntimeStateProjection
	var wantStateAssets map[string]harnesses.PortableRuntimeAssetKind
	var packageTarget, packageDigest string
	switch name {
	case "gemini":
		wantLaunch = harnesses.PortableRuntimeLaunch{
			EntrypointTarget: "harnesses/gemini/package/bundle/gemini.js", EntrypointTreeMember: "bundle/gemini.js",
			InterpreterTarget: "harnesses/gemini/bin/node", LoaderTarget: "harnesses/gemini/loader/ld-linux-aarch64.so.1",
			LibraryRootTargets: []string{"harnesses/gemini/lib/system"},
		}
		wantConstraints = harnesses.PortableRuntimeExecutionConstraints{
			Environment: portableInventoryGeminiConstraints(),
			RequiredAbsentPaths: []harnesses.PortableRuntimeGuestPath{
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".agents/skills"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".env"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/.env"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/GEMINI.md"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/agents"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/commands"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/extensions"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/policies"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/skills"},
				{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini/trustedFolders.json"},
			},
			FixedArguments:    []string{"--ignore-env"},
			FixedOptionValues: []harnesses.PortableRuntimeFixedOptionValue{{Option: "-e", Value: "none"}},
		}
		wantProjection = []harnesses.PortableRuntimeStateProjection{{
			Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini"},
			Entries: []harnesses.PortableRuntimeStateProjectionEntry{
				{AssetTarget: "state/gemini/google_accounts.json", Target: "google_accounts.json"},
				{AssetTarget: "state/gemini/oauth_creds.json", Target: "oauth_creds.json"},
				{AssetTarget: "config/gemini/settings.json", Target: "settings.json"},
			},
		}}
		wantStateAssets = map[string]harnesses.PortableRuntimeAssetKind{
			"config/gemini/settings.json":       harnesses.PortableRuntimeAssetConfig,
			"state/fizeau/gemini-quota.json":    harnesses.PortableRuntimeAssetQuota,
			"state/gemini/google_accounts.json": harnesses.PortableRuntimeAssetCache,
			"state/gemini/oauth_creds.json":     harnesses.PortableRuntimeAssetCredential,
		}
		packageTarget = "harnesses/gemini/package"
		packageDigest = "31adbda660d392d71583f7649dff2fc22e10d080c6701ff5849505ba0ec2a652"
	case "pi":
		wantLaunch = harnesses.PortableRuntimeLaunch{
			EntrypointTarget: "harnesses/pi/package/dist/cli.js", EntrypointTreeMember: "dist/cli.js",
			InterpreterTarget: "harnesses/pi/bin/node", LoaderTarget: "harnesses/pi/loader/ld-linux-aarch64.so.1",
			LibraryRootTargets: []string{"harnesses/pi/lib/system"},
		}
		wantConstraints.FixedArguments = []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes"}
		wantEnvironment = []harnesses.PortableRuntimeEnvironment{{Name: "PORTABLE_PI_INVENTORY_TOKEN"}}
		wantProjection = []harnesses.PortableRuntimeStateProjection{{
			Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".pi/agent"},
			Entries: []harnesses.PortableRuntimeStateProjectionEntry{
				{AssetTarget: "state/pi/auth.json", Target: "auth.json"},
				{AssetTarget: "config/pi/models.json", Target: "models.json"},
				{AssetTarget: "config/pi/settings.json", Target: "settings.json"},
			},
		}}
		for _, environmentName := range []string{"DISPLAY", "LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "PI_CODING_AGENT_DIR", "WAYLAND_DISPLAY"} {
			wantConstraints.Environment = append(wantConstraints.Environment, harnesses.PortableRuntimeEnvironmentConstraint{
				Name: environmentName, Kind: harnesses.PortableRuntimeEnvironmentUnset,
			})
		}
		wantStateAssets = map[string]harnesses.PortableRuntimeAssetKind{
			"config/pi/models.json":   harnesses.PortableRuntimeAssetConfig,
			"config/pi/settings.json": harnesses.PortableRuntimeAssetConfig,
			"state/pi/auth.json":      harnesses.PortableRuntimeAssetCredential,
		}
		packageTarget = "harnesses/pi/package"
		packageDigest = "e24e2b681a84d3aa44abc3ff565d23f827f668a6e5325070f738e8a420dc4e09"
	default:
		t.Fatalf("unexpected portable contributor %q", name)
	}

	if !reflect.DeepEqual(contribution.Launch, wantLaunch) {
		t.Fatalf("row %q launch = %#v, want %#v", name, contribution.Launch, wantLaunch)
	}
	if !reflect.DeepEqual(contribution.ExecutionConstraints, wantConstraints) {
		t.Fatalf("row %q execution constraints = %#v, want %#v", name, contribution.ExecutionConstraints, wantConstraints)
	}
	if !reflect.DeepEqual(contribution.Environment, wantEnvironment) {
		t.Fatalf("row %q environment names = %#v, want %#v", name, contribution.Environment, wantEnvironment)
	}
	if !reflect.DeepEqual(contribution.StateProjections, wantProjection) {
		t.Fatalf("row %q mixed-state projection = %#v, want %#v", name, contribution.StateProjections, wantProjection)
	}
	for assetTarget, kind := range wantStateAssets {
		asset, ok := portableInventoryAsset(contribution, assetTarget)
		if !ok || asset.Kind != kind || asset.PathKind != harnesses.PortableRuntimePathFile || asset.ContentSHA256 == "" || asset.Executable {
			t.Fatalf("row %q exact state asset %q = %#v", name, assetTarget, asset)
		}
	}
	packageAsset, ok := portableInventoryAsset(contribution, packageTarget)
	if !ok || packageAsset.Kind != harnesses.PortableRuntimeAssetInstallTree || packageAsset.ContentSHA256 != packageDigest {
		t.Fatalf("row %q package closure asset = %#v", name, packageAsset)
	}
	for _, exactTarget := range []string{contribution.Launch.InterpreterTarget, contribution.Launch.LoaderTarget} {
		asset, ok := portableInventoryAsset(contribution, exactTarget)
		if !ok || asset.Kind != harnesses.PortableRuntimeAssetSupport || asset.ContentSHA256 == "" {
			t.Fatalf("row %q exact launch support asset %q = %#v", name, exactTarget, asset)
		}
	}
	if !portableInventoryHasLibraryClosure(contribution, contribution.Launch.LibraryRootTargets[0]) {
		t.Fatalf("row %q has no emitted exact dynamic-library closure", name)
	}

	serialized := fmt.Sprintf("%#v", contribution)
	for _, forbidden := range []string{
		"gemini-access-secret", "gemini-refresh-secret", "active@example.invalid",
		"pi-access-secret", "pi-refresh-secret", "pi-inventory-secret-value", "PORTABLE_PI_INVENTORY_TOKEN=",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("row %q contribution leaked fixture value %q", name, forbidden)
		}
	}
}

func portableInventoryGeminiConstraints() []harnesses.PortableRuntimeEnvironmentConstraint {
	guest := func(target string) harnesses.PortableRuntimeEnvironmentConstraint {
		return harnesses.PortableRuntimeEnvironmentConstraint{
			Kind:      harnesses.PortableRuntimeEnvironmentGuestPath,
			GuestPath: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathTmp, Target: target},
		}
	}
	result := []harnesses.PortableRuntimeEnvironmentConstraint{
		{Name: "BROWSER", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "CLOUD_SHELL", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "EDITOR", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "GEMINI_CLI_HOME", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "GEMINI_CLI_IDE_SERVER_STDIO_ARGS", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "GEMINI_CLI_IDE_SERVER_STDIO_COMMAND", Kind: harnesses.PortableRuntimeEnvironmentUnset},
		{Name: "GEMINI_CLI_NO_RELAUNCH", Kind: harnesses.PortableRuntimeEnvironmentFixedTrue},
	}
	for _, pair := range []struct{ name, target string }{
		{"GEMINI_CLI_SYSTEM_DEFAULTS_PATH", "gemini/system-defaults.json"},
		{"GEMINI_CLI_SYSTEM_SETTINGS_PATH", "gemini/settings.json"},
		{"GEMINI_CLI_TRUSTED_FOLDERS_PATH", "gemini/trusted-folders.json"},
	} {
		constraint := guest(pair.target)
		constraint.Name = pair.name
		result = append(result, constraint)
	}
	for _, name := range []string{"LD_AUDIT", "LD_LIBRARY_PATH", "LD_PRELOAD", "NODE_OPTIONS", "NODE_PATH", "VISUAL"} {
		result = append(result, harnesses.PortableRuntimeEnvironmentConstraint{Name: name, Kind: harnesses.PortableRuntimeEnvironmentUnset})
	}
	return result
}

func portableInventoryAsset(contribution harnesses.PortableRuntimeContribution, target string) (harnesses.PortableRuntimeAsset, bool) {
	for _, asset := range contribution.Assets {
		if asset.Target == target {
			return asset, true
		}
	}
	return harnesses.PortableRuntimeAsset{}, false
}

func portableInventoryHasLibraryClosure(contribution harnesses.PortableRuntimeContribution, root string) bool {
	for _, asset := range contribution.Assets {
		if asset.Kind == harnesses.PortableRuntimeAssetSupport && strings.HasPrefix(asset.Target, root+"/") && asset.ContentSHA256 != "" {
			return true
		}
	}
	return false
}

func writePortableInventoryStateFixtures(t *testing.T, home string) {
	t.Helper()
	files := map[string]string{
		filepath.Join(home, ".gemini", "settings.json"):             `{"security":{"auth":{"selectedType":"oauth-personal"}}}`,
		filepath.Join(home, ".gemini", "oauth_creds.json"):          `{"access_token":"gemini-access-secret","refresh_token":"gemini-refresh-secret","scope":"openid email","token_type":"Bearer","expiry_date":4102444800000}`,
		filepath.Join(home, ".gemini", "google_accounts.json"):      `{"active":"active@example.invalid","old":["old@example.invalid"]}`,
		filepath.Join(home, "state", "fizeau", "gemini-quota.json"): `{"captured_at":"2026-07-16T12:00:00Z","windows":[{"name":"Pro","limit_id":"gemini-pro","window_minutes":0,"used_percent":10,"state":"ok"}],"source":"pty"}`,
		filepath.Join(home, ".pi", "agent", "settings.json"):        `{}`,
		filepath.Join(home, ".pi", "agent", "models.json"):          `{"providers":{"fixture":{"baseUrl":"https://example.invalid","apiKey":"PORTABLE_PI_INVENTORY_TOKEN"}}}`,
		filepath.Join(home, ".pi", "agent", "auth.json"):            `{"anthropic":{"type":"oauth","refresh":"pi-refresh-secret","access":"pi-access-secret","expires":4102444800000}}`,
	}
	for path, contents := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func copyPortableInventoryTree(t *testing.T, source, destination string) {
	t.Helper()
	var directories []struct {
		path string
		mode os.FileMode
	}
	if err := filepath.Walk(source, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		switch {
		case info.IsDir():
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			directories = append(directories, struct {
				path string
				mode os.FileMode
			}{target, info.Mode().Perm()})
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(current)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyPortableInventoryFileError(current, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported retained fixture entry %q", current)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index].path, directories[index].mode); err != nil {
			t.Fatal(err)
		}
	}
}

func copyPortableInventorySymlink(t *testing.T, source, destination string) {
	t.Helper()
	link, err := os.Readlink(source)
	if err != nil {
		t.Fatalf("retained launcher is not a symlink: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link, destination); err != nil {
		t.Fatal(err)
	}
}

func copyPortableInventoryFile(t *testing.T, source, destination string) {
	t.Helper()
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("retained file fixture %q is not a regular file", source)
	}
	if err := copyPortableInventoryFileError(source, destination, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func copyPortableInventoryFileError(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(destination, mode)
}

func TestPortableRuntimeInventoryUsesActualNativeTransport(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
	instances := builtin.Instances()
	rows, err := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistryForTest("claude"), map[string]harnesses.Harness{
		"claude": instances["claude"],
	})
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Transport != harnesses.PortableRuntimeTransportNative || rows[0].Inclusion != harnesses.PortableRuntimeInclusionNonSubprocess {
		t.Fatalf("native Claude row = %#v", rows[0])
	}
	if rows[0].Instance != instances["claude"] {
		t.Fatal("native Claude row does not retain the actual runner instance")
	}
	contributor, ok := rows[0].Instance.(harnesses.PortableRuntimeHarness)
	if !ok {
		t.Fatal("native Claude actual instance lacks the optional portable capability")
	}
	contribution, err := contributor.PortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if !errors.Is(err, harnesses.ErrPortableRuntimeTargetUnsupported) || !reflect.DeepEqual(contribution, harnesses.PortableRuntimeContribution{}) {
		t.Fatalf("native Claude contribution = %#v, %v; want zero plus target unsupported", contribution, err)
	}
}

func TestPortableRuntimeInventoryContainsNoEnvironmentValues(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime v0.15 is Linux-only")
	}

	binDirectory := t.TempDir()
	launcher := filepath.Join(binDirectory, "codex")
	buildPortableInventoryStaticFixture(t, launcher)
	home := filepath.Join(t.TempDir(), "account-bearing-codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(home, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"OPENAI_API_KEY":"auth-file-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("[model_providers.fixture]\nenv_key = 'PORTABLE_INVENTORY_SECRET'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	quotaPath := filepath.Join(t.TempDir(), "account-bearing-quota.json")
	if err := os.WriteFile(quotaPath, []byte(`{"quota":"quota-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDirectory)
	t.Setenv("CODEX_HOME", home)
	t.Setenv("FIZEAU_CODEX_AUTH", authPath)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", quotaPath)
	t.Setenv("PORTABLE_INVENTORY_SECRET", "environment-secret-value")
	unsetPortableInventoryEnvironment(t, "CODEX_API_KEY")
	unsetPortableInventoryEnvironment(t, "OPENAI_API_KEY")

	instances := builtin.Instances()
	rows, err := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistryForTest("codex"), map[string]harnesses.Harness{
		"codex": instances["codex"],
	})
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Inclusion != harnesses.PortableRuntimeInclusionRequired || rows[0].Instance != instances["codex"] {
		t.Fatalf("Codex inventory row = %#v", rows)
	}
	contributor, ok := rows[0].Instance.(harnesses.PortableRuntimeHarness)
	if !ok {
		t.Fatal("Codex inventory instance does not implement PortableRuntimeHarness")
	}
	contribution, err := contributor.PortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if err != nil {
		t.Fatalf("PortableRuntimeAssets() error = %v", err)
	}
	if !reflect.DeepEqual(contribution.Environment, []harnesses.PortableRuntimeEnvironment{{Name: "PORTABLE_INVENTORY_SECRET"}}) {
		t.Fatalf("Codex environment inventory = %#v", contribution.Environment)
	}
	serialized := fmt.Sprintf("%#v", contribution.Environment)
	for _, forbidden := range []string{"environment-secret-value", "auth-file-secret", "quota-secret", home, quotaPath, "PORTABLE_INVENTORY_SECRET="} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("environment inventory leaked %q: %s", forbidden, serialized)
		}
	}

	badDirectory := filepath.Join(t.TempDir(), "account-bearing-wrapper-root")
	if err := os.MkdirAll(badDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	badLauncher := filepath.Join(badDirectory, "codex")
	if err := os.WriteFile(badLauncher, []byte("#!/bin/sh\n# wrapper-secret-value\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", badDirectory)
	badContributor := builtin.Instances()["codex"].(harnesses.PortableRuntimeHarness)
	_, err = badContributor.PortableRuntimeAssets(context.Background(), harnesses.PortableRuntimeTarget{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
		t.Fatalf("unknown Codex wrapper error = %v", err)
	}
	for _, forbidden := range []string{badDirectory, badLauncher, "wrapper-secret-value"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("unknown Codex wrapper error leaked %q: %v", forbidden, err)
		}
	}
}

func buildPortableInventoryStaticFixture(t *testing.T, destination string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-trimpath", "-ldflags=-buildid=", "-o", destination, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOPROXY=off", "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build static portable-inventory fixture: %v: %s", err, output)
	}
}

func unsetPortableInventoryEnvironment(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "portable-test-unset-sentinel")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}
