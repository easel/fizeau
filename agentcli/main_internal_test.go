package agentcli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/easel/fizeau"
	agentConfig "github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/prompt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func isolateCatalogHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("FIZEAU_CACHE_DIR", filepath.Join(home, ".cache", "fizeau"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	// Unit tests must not discover or launch agent CLIs installed on the host.
	// Modern Codex performs asynchronous marketplace setup on first launch,
	// which otherwise races t.TempDir cleanup and makes the suite non-hermetic.
	t.Setenv("PATH", "")
}

func TestResolvePreset(t *testing.T) {
	cfg := &agentConfig.Config{Preset: "cheap"}

	got, err := resolvePreset("smart", cfg)
	require.NoError(t, err)
	assert.Equal(t, "smart", got)

	got, err = resolvePreset("", cfg)
	require.NoError(t, err)
	assert.Equal(t, "cheap", got)

	got, err = resolvePreset("", &agentConfig.Config{})
	require.NoError(t, err)
	assert.Equal(t, "default", got)

	// Deprecated aliases are now rejected.
	for _, alias := range []string{"agent", "worker", "cursor", "claude", "codex"} {
		_, err := resolvePreset(alias, cfg)
		require.Error(t, err, "alias %q should be rejected", alias)
		assert.Contains(t, err.Error(), "unknown preset")
	}
}

func TestResolveRunReasoningNormalizesExplicitValues(t *testing.T) {
	cfg := &agentConfig.Config{}
	got, err := resolveRunReasoning(cfg, "", fizeau.ReasoningHigh, "x-high")
	require.NoError(t, err)
	assert.Equal(t, fizeau.ReasoningXHigh, got)

	got, err = resolveRunReasoning(cfg, "", fizeau.ReasoningHigh, "auto")
	require.NoError(t, err)
	assert.Equal(t, fizeau.ReasoningHigh, got)
}

func TestBuildToolsForPreset_IncludesTaskTool(t *testing.T) {
	tools := buildToolsForPreset(t.TempDir(), "default")

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name())
	}

	assert.Contains(t, names, "task")
	assert.Contains(t, names, "patch")
	assert.Contains(t, names, "find")
	assert.NotContains(t, names, "glob")
	assert.Contains(t, names, "grep")
	assert.Contains(t, names, "ls")
	assert.NotContains(t, names, "anchor_edit")
}

func TestBuildToolsForPreset_DefaultIncludesTaskTool(t *testing.T) {
	tools := buildToolsForPreset(t.TempDir(), "default")

	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name())
	}

	assert.Contains(t, names, "task")
}

func TestBuildToolsForPresetWithAnchors_RegistersAnchorEditAndStoreBackedRead(t *testing.T) {
	workDir := t.TempDir()
	tools := buildToolsForPresetWithAnchors(workDir, "default", true)

	var read *fizeau.ReadTool
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name())
		if rt, ok := tool.(*fizeau.ReadTool); ok {
			read = rt
		}
	}

	require.NotNil(t, read)
	assert.NotNil(t, read.AnchorStore)
	assert.Contains(t, names, "anchor_edit")
}

func TestBuildToolsForPresetWithAnchors_DisabledKeepsLegacyReadAndNoAnchorEdit(t *testing.T) {
	tools := buildToolsForPresetWithAnchors(t.TempDir(), "default", false)

	var read *fizeau.ReadTool
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name())
		if rt, ok := tool.(*fizeau.ReadTool); ok {
			read = rt
		}
	}

	require.NotNil(t, read)
	assert.Nil(t, read.AnchorStore)
	assert.NotContains(t, names, "anchor_edit")
}

func TestBuildSystemPromptForRun_WithAnchorsAddsAnchorModeAddendum(t *testing.T) {
	workDir := t.TempDir()
	tools := buildToolsForPresetWithAnchors(workDir, "default", true)

	systemPrompt := buildSystemPromptForRun("default", tools, nil, nil, workDir, true, "")

	assert.Contains(t, systemPrompt, "# Anchor Mode")
	assert.Contains(t, systemPrompt, "File read output prefixes each line with anchor words.")
	assert.Contains(t, systemPrompt, "use anchor_edit instead of edit or write")
	assert.Contains(t, systemPrompt, "Do not mix edit/write with anchor_edit on the same file.")
	assert.Contains(t, systemPrompt, "re-read the file before using anchor_edit so you have fresh anchors")
}

func TestBuildSystemPromptForRun_WithoutAnchorsOmitsAnchorModeAddendum(t *testing.T) {
	workDir := t.TempDir()
	tools := buildToolsForPresetWithAnchors(workDir, "default", false)

	systemPrompt := buildSystemPromptForRun("default", tools, nil, []prompt.ContextFile{
		{Path: "AGENTS.md", Content: "Project rules."},
	}, workDir, false, "Extra caller instructions.")

	assert.NotContains(t, systemPrompt, "# Anchor Mode")
	assert.NotContains(t, systemPrompt, "File read output prefixes each line with anchor words.")
	assert.NotContains(t, systemPrompt, "use anchor_edit instead of edit or write")
	assert.Contains(t, systemPrompt, "Extra caller instructions.")
	assert.Contains(t, systemPrompt, "Project rules.")
}

func TestRun_AcceptsAnchorsFlag(t *testing.T) {
	var stdout, stderr strings.Builder
	code := Run(Options{
		Args:   []string{"--anchors", "--version"},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	require.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "fiz ")
	assert.Empty(t, stderr.String())
}

func TestBuildServiceExecuteRequestPreservesNativeLoopSettings(t *testing.T) {
	workDir := t.TempDir()
	tools := buildToolsForPreset(workDir, "default")
	serviceReq := buildServiceExecuteRequest(serviceExecuteRequestParams{
		Prompt:                  "hi",
		SystemPrompt:            "system",
		Tools:                   tools,
		WorkDir:                 workDir,
		Harness:                 "fiz",
		SelectedProvider:        "local",
		SelectedRoute:           "local",
		RequestedModel:          "test-model",
		ResolvedModel:           "test-model",
		Reasoning:               fizeau.ReasoningLow,
		MaxIterations:           7,
		MaxTokens:               2048,
		ReasoningByteLimit:      4096,
		CompactionContextWindow: 128000,
		CompactionReserveTokens: 4096,
		ToolPreset:              "default",
	})

	require.Len(t, serviceReq.Tools, len(tools))
	assert.Equal(t, "default", serviceReq.ToolPreset)
	assert.Equal(t, toolNames(tools), toolNames(serviceReq.Tools))
	assert.Equal(t, workDir, serviceReq.WorkDir)
	assert.Equal(t, "fiz", serviceReq.Harness)
	assert.Equal(t, "local", serviceReq.Provider)
	assert.Equal(t, "test-model", serviceReq.Model)
	assert.True(t, serviceReq.NoStream == false)
	assert.Equal(t, 7, serviceReq.MaxIterations)
	assert.Equal(t, 2048, serviceReq.MaxTokens)
	assert.Equal(t, 4096, serviceReq.ReasoningByteLimit)
	assert.Equal(t, 128000, serviceReq.CompactionContextWindow)
	assert.Equal(t, 4096, serviceReq.CompactionReserveTokens)
}

func toolNames(tools []fizeau.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name()
	}
	return names
}
