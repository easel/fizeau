package harnesses_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
)

func TestPortableRuntimeInventoryCoversEveryEligibleRegisteredHarness(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	instances := builtin.Instances()
	rows, err := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistry(), instances)
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}

	wantNames := []string{
		"claude", "claude-tui", "codex", "fiz", "gemini", "lmstudio", "lucebox",
		"omlx", "opencode", "openrouter", "pi", "script", "virtual", "vllm",
	}
	wantClassification := map[string]struct {
		transport harnesses.PortableRuntimeTransport
		inclusion harnesses.PortableRuntimeInclusion
	}{
		"claude":     {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"claude-tui": {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"codex":      {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"gemini":     {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
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
			if row.Name == "claude" || row.Name == "claude-tui" {
				if _, ok := row.Instance.(harnesses.PortableRuntimeHarness); !ok {
					t.Errorf("required Anthropic row %q lacks PortableRuntimeHarness", row.Name)
				}
			}
		}
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("inventory names = %v, want %v", gotNames, wantNames)
	}
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
