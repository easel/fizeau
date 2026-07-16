package gemini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestGeminiPortableRuntimeExecutionConstraints(t *testing.T) {
	constraints := geminiPortableExecutionConstraints()
	if got, want := constraints.FixedArguments, []string{"--ignore-env"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed arguments = %#v, want %#v", got, want)
	}
	if got, want := constraints.FixedOptionValues, []harnesses.PortableRuntimeFixedOptionValue{{Option: "-e", Value: "none"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fixed option values = %#v, want %#v", got, want)
	}
	treatments := geminiPortableEnvironmentTreatments(constraints)
	if got := treatments[geminiPortableNoRelaunchEnvironment]; got.Kind != harnesses.PortableRuntimeEnvironmentFixedTrue || got.GuestPath != (harnesses.PortableRuntimeGuestPath{}) {
		t.Fatalf("no-relaunch treatment = %#v", got)
	}

	req := harnesses.ExecuteRequest{Prompt: "ordinary prompt", Model: "gemini-test", Permissions: "supervised"}
	args, err := geminiPortableArguments([]string{"--output-format", "stream-json"}, req, "arg")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--ignore-env", "-e", "none",
		"--output-format", "stream-json",
		"-m", "gemini-test", "--approval-mode", "default", "-p", "ordinary prompt",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("arguments = %#v, want fixed prefix before configured/request arguments %#v", args, want)
	}

	for _, arguments := range [][]string{
		{"-e", "account-secret-extension"},
		{"-e=account-secret-extension"},
		{"-eaccount-secret-extension"},
		{"--extensions", "account-secret-extension"},
		{"--extensions=account-secret-extension"},
		{"--ignore-env=false"},
		{"--list-extensions"},
		{"-l"},
	} {
		t.Run("reject later extension override "+arguments[0], func(t *testing.T) {
			_, err := geminiPortableArguments(arguments, req, "arg")
			if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("conflict error = %v", err)
			}
			assertGeminiExecutionRedacted(t, err, "account-secret-extension", "=account-secret-extension")
		})
	}
}

func TestGeminiPortableRuntimeRunnerArgv(t *testing.T) {
	home := t.TempDir()
	workDir := filepath.Join(home, "project")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	capture := newGeminiScriptCapture(t)
	runner := &Runner{
		Binary:   capture.binary,
		BaseArgs: []string{"--output-format", "stream-json"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := runner.Execute(ctx, harnesses.ExecuteRequest{
		Prompt:      "runner prompt",
		Model:       "gemini-runner-model",
		Permissions: "unrestricted",
		WorkDir:     workDir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for range stream {
	}
	captured := capture.read(t)
	want := []string{
		"--ignore-env", "-e", "none",
		"--output-format", "stream-json",
		"-m", "gemini-runner-model", "--approval-mode", "yolo", "-p", "runner prompt",
	}
	if !reflect.DeepEqual(captured.Args, want) {
		t.Fatalf("runner arguments = %#v, want %#v", captured.Args, want)
	}
	environment, err := os.ReadFile(capture.env)
	if err != nil {
		t.Fatalf("read environment capture: %v", err)
	}
	if string(environment) != "true" {
		t.Fatalf("%s = %q, want generated true", geminiPortableNoRelaunchEnvironment, environment)
	}
	filtered := geminiPortableRunnerEnvironment([]string{
		"SAFE_NAME=retained",
		"NODE_OPTIONS=--require=/private/account/loader.js",
		"NODE_PATH=/private/account/modules",
		"LD_PRELOAD=/private/account/preload.so",
		"BROWSER=account-secret-command",
		"GEMINI_CLI_SYSTEM_SETTINGS_PATH=/private/account/settings.json",
		"GEMINI_CLI_NO_RELAUNCH=false",
	})
	if !reflect.DeepEqual(filtered, []string{"SAFE_NAME=retained", "GEMINI_CLI_NO_RELAUNCH=true"}) {
		t.Fatalf("production environment retained executable controls: %#v", filtered)
	}
}

func TestGeminiPortableRuntimeRejectsExecutableConfiguration(t *testing.T) {
	sensitive := []string{
		"account-secret-command --token=raw-token",
		"/private/account/settings.json",
		"operator@example.invalid",
		strings.Repeat("a", 64),
	}
	settingsCases := []struct {
		name string
		json string
	}{
		{"enabled extensions", `{"extensions":{"disabled":[]},"value":"account-secret-command --token=raw-token"}`},
		{"MCP servers", `{"mcpServers":{"private":{"command":"account-secret-command --token=raw-token"}}}`},
		{"hooks", `{"hooks":{"AfterTool":[{"command":"account-secret-command --token=raw-token"}]}}`},
		{"skills", `{"skills":{"enabled":true,"path":"/private/account/settings.json"}}`},
		{"agents", `{"agents":{"overrides":{"private":{"enabled":true}}}}`},
		{"browser helpers", `{"browserHelpers":{"command":"account-secret-command --token=raw-token"}}`},
		{"commands", `{"commands":{"private":"account-secret-command --token=raw-token"}}`},
		{"tool discovery command", `{"tools":{"discoveryCommand":"account-secret-command --token=raw-token"}}`},
		{"tool call command", `{"tools":{"callCommand":"account-secret-command --token=raw-token"}}`},
		{"external policy paths", `{"policyPaths":["/private/account/settings.json"]}`},
		{"external context paths", `{"context":{"fileName":"/private/account/settings.json"}}`},
		{"system settings paths", `{"systemSettingsPath":"/private/account/settings.json"}`},
	}
	for _, test := range settingsCases {
		t.Run(test.name, func(t *testing.T) {
			err := validateGeminiPortableSettings([]byte(test.json))
			if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("settings error = %v", err)
			}
			assertGeminiExecutionRedacted(t, err, sensitive...)
		})
	}

	t.Run("project commands", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".gemini", "commands"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := inspectGeminiPortableProjectSources(root)
		if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("project command error = %v", err)
		}
		assertGeminiExecutionRedacted(t, err, root)
	})

	t.Run("nested project ambient sources", func(t *testing.T) {
		for _, relative := range []string{".gemini", filepath.Join(".agents", "skills"), "GEMINI.md"} {
			t.Run(relative, func(t *testing.T) {
				root := t.TempDir()
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
				source := filepath.Join(root, relative)
				if filepath.Ext(source) == ".md" {
					if err := os.WriteFile(source, []byte("account-secret-context"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.MkdirAll(source, 0o700); err != nil {
					t.Fatal(err)
				}
				nested := filepath.Join(root, "one", "two")
				if err := os.MkdirAll(nested, 0o700); err != nil {
					t.Fatal(err)
				}
				err := inspectGeminiPortableProjectSources(nested)
				if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
					t.Fatalf("nested project error = %v", err)
				}
				assertGeminiExecutionRedacted(t, err, root, nested, source, "account-secret-context")
			})
		}
	})

	t.Run("user commands", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".gemini", "commands"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := inspectGeminiPortableUserConfiguration(home)
		if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("user command error = %v", err)
		}
		assertGeminiExecutionRedacted(t, err, home)
	})

	for _, arguments := range [][]string{
		{"--policy", "/private/account/settings.json"},
		{"--admin-policy=/private/account/settings.json"},
		{"--include-directories", "/private/account"},
	} {
		t.Run("external argument "+arguments[0], func(t *testing.T) {
			err := validateGeminiPortableLaterArguments(arguments)
			if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("argument error = %v", err)
			}
			assertGeminiExecutionRedacted(t, err, append(sensitive, arguments...)...)
		})
	}

	t.Run("system environment selector", func(t *testing.T) {
		t.Setenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH", "/private/account/settings.json")
		err := inspectGeminiPortableSystemSources(nil)
		if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("system selector error = %v", err)
		}
		assertGeminiExecutionRedacted(t, err, sensitive...)
	})
}

func TestGeminiPortableRuntimeRejectsAmbientSources(t *testing.T) {
	constraints := geminiPortableExecutionConstraints()
	treatments := geminiPortableEnvironmentTreatments(constraints)
	if got := treatments["GEMINI_CLI_HOME"]; got.Kind != harnesses.PortableRuntimeEnvironmentUnset {
		t.Fatalf("GEMINI_CLI_HOME treatment = %#v, want unset so generated HOME is authoritative", got)
	}
	for name, target := range map[string]string{
		"GEMINI_CLI_SYSTEM_DEFAULTS_PATH": "gemini/system-defaults.json",
		"GEMINI_CLI_SYSTEM_SETTINGS_PATH": "gemini/settings.json",
		"GEMINI_CLI_TRUSTED_FOLDERS_PATH": "gemini/trusted-folders.json",
	} {
		got := treatments[name]
		wantPath := harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathTmp, Target: target}
		if got.Kind != harnesses.PortableRuntimeEnvironmentGuestPath || got.GuestPath != wantPath {
			t.Fatalf("%s treatment = %#v, want generated path %#v", name, got, wantPath)
		}
	}
	wantAbsent := []harnesses.PortableRuntimeGuestPath{
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
	}
	if !reflect.DeepEqual(constraints.RequiredAbsentPaths, wantAbsent) {
		t.Fatalf("required-absent paths = %#v, want generated-home guarantees %#v", constraints.RequiredAbsentPaths, wantAbsent)
	}

	for _, relative := range []string{filepath.Join(".gemini", "settings.json"), ".env", filepath.Join(".agents", "skills", "private.md"), "GEMINI.md"} {
		t.Run("project "+relative, func(t *testing.T) {
			home := t.TempDir()
			project := t.TempDir()
			t.Setenv("HOME", home)
			source := filepath.Join(project, relative)
			if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("account-secret-source"), 0o600); err != nil {
				t.Fatal(err)
			}
			capture := newGeminiScriptCapture(t)
			stream, err := (&Runner{Binary: capture.binary, BaseArgs: []string{}}).Execute(context.Background(), harnesses.ExecuteRequest{Prompt: "prompt", WorkDir: project})
			if err == nil || stream != nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("ambient source result = stream %v, error %v", stream, err)
			}
			if _, statErr := os.Stat(capture.args); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("runner started before ambient-source rejection: %v", statErr)
			}
			assertGeminiExecutionRedacted(t, err, project, source, "account-secret-source")
		})
	}

	t.Run("user and home dotenv", func(t *testing.T) {
		for _, relative := range []string{".env", filepath.Join(".gemini", ".env")} {
			home := t.TempDir()
			source := filepath.Join(home, relative)
			if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("TOKEN=account-secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := inspectGeminiPortableUserConfiguration(home)
			if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("dotenv error = %v", err)
			}
			assertGeminiExecutionRedacted(t, err, home, source, "TOKEN=account-secret", "account-secret")
		}
	})

	t.Run("default system settings", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "settings.json")
		if err := os.WriteFile(source, []byte(`{"hooks":{"BeforeAgent":[{"command":"account-secret-command"}]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		err := inspectGeminiPortableSystemSources([]string{source})
		if err == nil || !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("system settings error = %v", err)
		}
		assertGeminiExecutionRedacted(t, err, root, source, "account-secret-command")
	})
}

func geminiPortableEnvironmentTreatments(constraints harnesses.PortableRuntimeExecutionConstraints) map[string]harnesses.PortableRuntimeEnvironmentConstraint {
	result := make(map[string]harnesses.PortableRuntimeEnvironmentConstraint, len(constraints.Environment))
	for _, treatment := range constraints.Environment {
		result[treatment.Name] = treatment
	}
	return result
}

func assertGeminiExecutionRedacted(t *testing.T, err error, sensitive ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected typed redacted error")
	}
	message := err.Error()
	for _, value := range sensitive {
		if value != "" && strings.Contains(message, value) {
			t.Fatalf("diagnostic exposed sensitive input %q: %q", value, message)
		}
	}
}
